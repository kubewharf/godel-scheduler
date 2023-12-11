/*
Copyright 2019 The Kubernetes Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package scheduling

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	v1 "k8s.io/api/core/v1"
	storagev1 "k8s.io/api/storage/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/apiserver/pkg/storage/etcd3"
	utilfeature "k8s.io/apiserver/pkg/util/feature"
	coreinformers "k8s.io/client-go/informers/core/v1"
	storageinformers "k8s.io/client-go/informers/storage/v1"
	clientset "k8s.io/client-go/kubernetes"
	storagelisters "k8s.io/client-go/listers/storage/v1"
	csitrans "k8s.io/csi-translation-lib"
	csiplugins "k8s.io/csi-translation-lib/plugins"
	"k8s.io/klog/v2"

	"github.com/kubewharf/godel-scheduler/pkg/util"
	"github.com/kubewharf/godel-scheduler/pkg/util/features"
	"github.com/kubewharf/godel-scheduler/pkg/util/helper"
	volumeutil "github.com/kubewharf/godel-scheduler/pkg/util/volume/util"
	pvutil "github.com/kubewharf/godel-scheduler/pkg/volume/persistentvolume/util"
	"github.com/kubewharf/godel-scheduler/pkg/volume/scheduling/metrics"
)

// ConflictReason is used for the special strings which explain why
// volume binding is impossible for a node.
type ConflictReason string

// ConflictReasons contains all reasons that explain why volume binding is impossible for a node.
type ConflictReasons []ConflictReason

func (reasons ConflictReasons) Len() int           { return len(reasons) }
func (reasons ConflictReasons) Less(i, j int) bool { return reasons[i] < reasons[j] }
func (reasons ConflictReasons) Swap(i, j int)      { reasons[i], reasons[j] = reasons[j], reasons[i] }

const (
	// ErrReasonBindConflict is used for VolumeBindingNoMatch predicate error.
	ErrReasonBindConflict ConflictReason = "node(s) didn't find available persistent volumes to bind"
	// ErrReasonNodeConflict is used for VolumeNodeAffinityConflict predicate error.
	ErrReasonNodeConflict ConflictReason = "node(s) had volume node affinity conflict"
	// ErrUnboundImmediatePVC is used when the pod has an unbound PVC in immedate binding mode.
	ErrUnboundImmediatePVC ConflictReason = "pod has unbound immediate PersistentVolumeClaims"
)

// InTreeToCSITranslator contains methods required to check migratable status
// and perform translations from InTree PV's to CSI
type InTreeToCSITranslator interface {
	IsPVMigratable(pv *v1.PersistentVolume) bool
	GetInTreePluginNameFromSpec(pv *v1.PersistentVolume, vol *v1.Volume) (string, error)
	TranslateInTreePVToCSI(pv *v1.PersistentVolume) (*v1.PersistentVolume, error)
}

// GodelVolumeBinder is used by the scheduler to handle PVC/PV binding
// and dynamic provisioning.  The binding decisions are integrated into the pod scheduling
// workflow so that the PV NodeAffinity is also considered along with the pod's other
// scheduling requirements.
//
// This integrates into the existing default scheduler workflow as follows:
//  1. The scheduler takes a Pod off the scheduler queue and processes it serially:
//     a. Invokes all predicate functions, parallelized across nodes.  FindPodVolumes() is invoked here.
//     b. Invokes all priority functions.  Future/TBD
//     c. Selects the best node for the Pod.
//     d. Cache the node selection for the Pod. AssumePodVolumes() is invoked here.
//     i.  If PVC binding is required, cache in-memory only:
//     * For manual binding: update PV objects for prebinding to the corresponding PVCs.
//     * For dynamic provisioning: update PVC object with a selected node from c)
//     * For the pod, which PVCs and PVs need API updates.
//     ii. Afterwards, the main scheduler caches the Pod->Node binding in the scheduler's pod cache,
//     This is handled in the scheduler and not here.
//     e. Asynchronously bind volumes and pod in a separate goroutine
//     i.  BindPodVolumes() is called first. It makes all the necessary API updates and waits for
//     PV controller to fully bind and provision the PVCs. If binding fails, the Pod is sent
//     back through the scheduler.
//     ii. After BindPodVolumes() is complete, then the scheduler does the final Pod->Node binding.
//  2. Once all the assume operations are done in d), the scheduler processes the next Pod in the scheduler queue
//     while the actual binding operation occurs in the background.
type GodelVolumeBinder interface {
	// FindPodVolumes checks if all of a Pod's PVCs can be satisfied by the node.
	//
	// If a PVC is bound, it checks if the PV's NodeAffinity matches the Node.
	// Otherwise, it tries to find an available PV to bind to the PVC.
	//
	// It returns an error when something went wrong or a list of reasons why the node is
	// (currently) not usable for the pod.
	//
	// This function is called by the volume binding scheduler predicate and can be called in parallel
	FindPodVolumes(pod *v1.Pod, nodeName string, nodeLabels map[string]string) (reasons ConflictReasons, err error)

	// AssumePodVolumes will:
	// 1. Take the PV matches for unbound PVCs and update the PV cache assuming
	// that the PV is prebound to the PVC.
	// 2. Take the PVCs that need provisioning and update the PVC cache with related
	// annotations set.
	//
	// It returns true if all volumes are fully bound
	//
	// This function will modify assumedPod with the node name.
	// This function is called serially.
	AssumePodVolumes(assumedPod *v1.Pod, nodeName string) (allFullyBound bool, err error)

	// BindPodVolumes will:
	// 1. Initiate the volume binding by making the API call to prebind the PV
	// to its matching PVC.
	// 2. Trigger the volume provisioning by making the API call to set related
	// annotations on the PVC
	// 3. Wait for PVCs to be completely bound by the PV controller
	//
	// This function can be called in parallel.
	BindPodVolumes(assumedPod *v1.Pod) error

	// GetBindingsCache returns the cache used (if any) to store volume binding decisions.
	GetBindingsCache() PodBindingCache

	// DeletePodBindings will delete pod's bindingDecisions in podBindingCache.
	DeletePodBindings(pod *v1.Pod)
}

type BaseVolumeBinder interface {
	// FindPodVolumes checks if all of a Pod's PVCs can be satisfied by the node.
	//
	// If a PVC is bound, it checks if the PV's NodeAffinity matches the Node.
	// Otherwise, it tries to find an available PV to bind to the PVC.
	//
	// It returns an error when something went wrong or a list of reasons why the node is
	// (currently) not usable for the pod.
	//
	// This function is called by the volume binding scheduler predicate and can be called in parallel
	FindPodVolumes(pod *v1.Pod, nodeName string, nodeLabels map[string]string) (reasons ConflictReasons, err error)
}

type volumeBinder struct {
	*baseVolumeBinder
	kubeClient   clientset.Interface
	nodeInformer coreinformers.NodeInformer
	// Stores binding decisions that were made in FindPodVolumes for use in AssumePodVolumes.
	// AssumePodVolumes modifies the bindings again for use in BindPodVolumes.
	podBindingCache PodBindingCache
	// Amount of time to wait for the bind operation to succeed
	bindTimeout time.Duration
}

type baseVolumeBinder struct {
	classLister     storagelisters.StorageClassLister
	csiNodeInformer storageinformers.CSINodeInformer
	pvcCache        PVCAssumeCache
	pvCache         PVAssumeCache
	translator      InTreeToCSITranslator
}

func newBaseVolumeBinder(csiNodeInformer storageinformers.CSINodeInformer,
	pvcInformer coreinformers.PersistentVolumeClaimInformer,
	pvInformer coreinformers.PersistentVolumeInformer,
	storageClassInformer storageinformers.StorageClassInformer,
) *baseVolumeBinder {
	b := &baseVolumeBinder{
		classLister:     storageClassInformer.Lister(),
		csiNodeInformer: csiNodeInformer,
		pvcCache:        NewPVCAssumeCache(pvcInformer.Informer()),
		pvCache:         NewPVAssumeCache(pvInformer.Informer()),
		translator:      csitrans.New(),
	}
	return b
}

func NewBaseVolumeBinder(
	csiNodeInformer storageinformers.CSINodeInformer,
	pvcInformer coreinformers.PersistentVolumeClaimInformer,
	pvInformer coreinformers.PersistentVolumeInformer,
	storageClassInformer storageinformers.StorageClassInformer,
) BaseVolumeBinder {
	b := newBaseVolumeBinder(csiNodeInformer, pvcInformer, pvInformer, storageClassInformer)
	return b
}

func (b *baseVolumeBinder) FindPodVolumes(pod *v1.Pod, nodeName string, nodeLabels map[string]string) (reasons ConflictReasons, err error) {
	// Warning: Below log needs high verbosity as it can be printed several times (#60933).
	klog.V(5).InfoS("Entered FindPodVolumes in baseVolumeBinder", "pod", klog.KObj(pod), "nodeName", nodeName)

	// Initialize to true for pods that don't have volumes. These
	// booleans get translated into reason strings when the function
	// returns without an error.
	unboundVolumesSatisfied := true
	boundVolumesSatisfied := true
	defer func() {
		if err != nil {
			return
		}
		if !boundVolumesSatisfied {
			reasons = append(reasons, ErrReasonNodeConflict)
		}
		if !unboundVolumesSatisfied {
			reasons = append(reasons, ErrReasonBindConflict)
		}
	}()

	start := time.Now()
	defer func() {
		metrics.VolumeSchedulingStageLatency.WithLabelValues("scheduler", "predicate").Observe(time.Since(start).Seconds())
		if err != nil {
			metrics.VolumeSchedulingStageFailed.WithLabelValues("scheduler", "predicate").Inc()
		}
	}()

	podVolumes := podVolumesInfo{
		boundVolumesSatisfied:   boundVolumesSatisfied,
		unboundVolumesSatisfied: unboundVolumesSatisfied,
	}
	podVolumes, reasons, err = b.findPodVolumes(pod, nodeName, nodeLabels, podVolumes)
	boundVolumesSatisfied = podVolumes.boundVolumesSatisfied
	unboundVolumesSatisfied = podVolumes.unboundVolumesSatisfied
	return
}

// NewVolumeBinder sets up all the caches needed for the scheduler to make volume binding decisions.
func NewVolumeBinder(
	kubeClient clientset.Interface,
	nodeInformer coreinformers.NodeInformer,
	csiNodeInformer storageinformers.CSINodeInformer,
	pvcInformer coreinformers.PersistentVolumeClaimInformer,
	pvInformer coreinformers.PersistentVolumeInformer,
	storageClassInformer storageinformers.StorageClassInformer,
	bindTimeout time.Duration,
) GodelVolumeBinder {
	b := &volumeBinder{
		baseVolumeBinder: newBaseVolumeBinder(csiNodeInformer, pvcInformer, pvInformer, storageClassInformer),
		kubeClient:       kubeClient,
		nodeInformer:     nodeInformer,
		podBindingCache:  NewPodBindingCache(),
		bindTimeout:      bindTimeout,
	}

	return b
}

func (b *volumeBinder) GetBindingsCache() PodBindingCache {
	return b.podBindingCache
}

// DeletePodBindings will delete pod's bindingDecisions in podBindingCache.
func (b *volumeBinder) DeletePodBindings(pod *v1.Pod) {
	cache := b.podBindingCache
	if pod != nil {
		cache.DeleteBindings(pod)
	}
}

// FindPodVolumes caches the matching PVs and PVCs to provision per node in podBindingCache.
// This method intentionally takes in a *v1.Node object instead of using volumebinder.nodeInformer.
// That's necessary because some operations will need to pass in to the predicate fake node objects.
func (b *volumeBinder) FindPodVolumes(pod *v1.Pod, nodeName string, nodeLabels map[string]string) (reasons ConflictReasons, err error) {
	// Warning: Below log needs high verbosity as it can be printed several times (#60933).
	klog.V(5).InfoS("Entered FindPodVolumes in volumeBinder", "pod", klog.KObj(pod), "nodeName", nodeName)

	// Initialize to true for pods that don't have volumes. These
	// booleans get translated into reason strings when the function
	// returns without an error.
	unboundVolumesSatisfied := true
	boundVolumesSatisfied := true
	defer func() {
		if err != nil {
			return
		}
		if !boundVolumesSatisfied {
			reasons = append(reasons, ErrReasonNodeConflict)
		}
		if !unboundVolumesSatisfied {
			reasons = append(reasons, ErrReasonBindConflict)
		}
	}()

	start := time.Now()
	defer func() {
		metrics.VolumeSchedulingStageLatency.WithLabelValues("binder", "predicate").Observe(time.Since(start).Seconds())
		if err != nil {
			metrics.VolumeSchedulingStageFailed.WithLabelValues("binder", "predicate").Inc()
		}
	}()

	var (
		matchedBindings   []*bindingInfo
		provisionedClaims []*v1.PersistentVolumeClaim
	)
	defer func() {
		// We recreate bindings for each new schedule loop.
		if len(matchedBindings) == 0 && len(provisionedClaims) == 0 {
			// Clear cache if no claims to bind or provision for this node.
			b.podBindingCache.ClearBindings(pod, nodeName)
			return
		}
		// Although we do not distinguish nil from empty in this function, for
		// easier testing, we normalize empty to nil.
		if len(matchedBindings) == 0 {
			matchedBindings = nil
		}
		if len(provisionedClaims) == 0 {
			provisionedClaims = nil
		}
		// Mark cache with all matched and provisioned claims for this node
		b.podBindingCache.UpdateBindings(pod, nodeName, matchedBindings, provisionedClaims)
	}()

	podVolumes := podVolumesInfo{
		boundVolumesSatisfied:   boundVolumesSatisfied,
		unboundVolumesSatisfied: unboundVolumesSatisfied,
		matchedBindings:         matchedBindings,
		provisionedClaims:       provisionedClaims,
	}
	podVolumes, reasons, err = b.findPodVolumes(pod, nodeName, nodeLabels, podVolumes)
	boundVolumesSatisfied = podVolumes.boundVolumesSatisfied
	unboundVolumesSatisfied = podVolumes.unboundVolumesSatisfied
	matchedBindings = podVolumes.matchedBindings
	provisionedClaims = podVolumes.provisionedClaims
	return
}

// AssumePodVolumes will take the cached matching PVs and PVCs to provision
// in podBindingCache for the chosen node, and:
// 1. Update the pvCache with the new prebound PV.
// 2. Update the pvcCache with the new PVCs with annotations set
// 3. Update podBindingCache again with cached API updates for PVs and PVCs.
func (b *volumeBinder) AssumePodVolumes(assumedPod *v1.Pod, nodeName string) (allFullyBound bool, err error) {
	klog.V(4).InfoS("Entered AssumePodVolumes in volumeBinder", "pod", klog.KObj(assumedPod), "nodeName", nodeName)
	start := time.Now()
	defer func() {
		metrics.VolumeSchedulingStageLatency.WithLabelValues("assume").Observe(time.Since(start).Seconds())
		if err != nil {
			metrics.VolumeSchedulingStageFailed.WithLabelValues("assume").Inc()
		}
	}()

	if allBound := b.arePodVolumesBound(assumedPod); allBound {
		klog.V(4).InfoS("Confirmed all PVCs bound", "pod", klog.KObj(assumedPod), "nodeName", nodeName)
		return true, nil
	}

	assumedPod.Spec.NodeName = nodeName

	claimsToBind := b.podBindingCache.GetBindings(assumedPod, nodeName)
	claimsToProvision := b.podBindingCache.GetProvisionedPVCs(assumedPod, nodeName)

	// Assume PV
	newBindings := []*bindingInfo{}
	for _, binding := range claimsToBind {
		newPV, dirty, err := pvutil.GetBindVolumeToClaim(binding.pv, binding.pvc)
		klog.V(4).InfoS("Called GetBindVolumeToClaim",
			"pod", klog.KObj(assumedPod),
			"PV", klog.KObj(binding.pv),
			"PVC", klog.KObj(binding.pvc),
			"newPV", klog.KObj(newPV),
			"isModified", dirty,
			"err", err)
		if err != nil {
			b.revertAssumedPVs(newBindings)
			return false, err
		}
		// TODO: can we assume everytime?
		if dirty {
			err = b.pvCache.Assume(newPV)
			if err != nil {
				b.revertAssumedPVs(newBindings)
				return false, err
			}
		}
		newBindings = append(newBindings, &bindingInfo{pv: newPV, pvc: binding.pvc})
	}

	// Assume PVCs
	newProvisionedPVCs := []*v1.PersistentVolumeClaim{}
	for _, claim := range claimsToProvision {
		// The claims from method args can be pointing to watcher cache. We must not
		// modify these, therefore create a copy.
		claimClone := claim.DeepCopy()
		util.SetMetaDataMap(&claimClone.ObjectMeta, pvutil.AnnSelectedNode, nodeName)
		err = b.pvcCache.Assume(claimClone)
		if err != nil {
			b.revertAssumedPVs(newBindings)
			b.revertAssumedPVCs(newProvisionedPVCs)
			return
		}

		newProvisionedPVCs = append(newProvisionedPVCs, claimClone)
	}

	// Update cache with the assumed pvcs and pvs
	// Even if length is zero, update the cache with an empty slice to indicate that no
	// operations are needed
	b.podBindingCache.UpdateBindings(assumedPod, nodeName, newBindings, newProvisionedPVCs)

	return
}

// BindPodVolumes gets the cached bindings and PVCs to provision in podBindingCache,
// makes the API update for those PVs/PVCs, and waits for the PVCs to be completely bound
// by the PV controller.
func (b *volumeBinder) BindPodVolumes(assumedPod *v1.Pod) (err error) {
	podName := getPodName(assumedPod)
	klog.V(4).InfoS("Entered BindPodVolumes in volumeBinder", "pod", klog.KObj(assumedPod), "nodeName", assumedPod.Spec.NodeName)

	start := time.Now()
	defer func() {
		metrics.VolumeSchedulingStageLatency.WithLabelValues("bind").Observe(time.Since(start).Seconds())
		if err != nil {
			metrics.VolumeSchedulingStageFailed.WithLabelValues("bind").Inc()
		}
	}()

	bindings := b.podBindingCache.GetBindings(assumedPod, assumedPod.Spec.NodeName)
	claimsToProvision := b.podBindingCache.GetProvisionedPVCs(assumedPod, assumedPod.Spec.NodeName)

	// Start API operations
	err = b.bindAPIUpdate(podName, bindings, claimsToProvision)
	if err != nil {
		return err
	}

	err = wait.Poll(time.Second, b.bindTimeout, func() (bool, error) {
		b, err := b.checkBindings(assumedPod, bindings, claimsToProvision)
		return b, err
	})
	if err != nil {
		return fmt.Errorf("Failed to bind volumes: %v", err)
	}
	return nil
}

func getPodName(pod *v1.Pod) string {
	return pod.Namespace + "/" + pod.Name
}

func getPVCName(pvc *v1.PersistentVolumeClaim) string {
	return pvc.Namespace + "/" + pvc.Name
}

// bindAPIUpdate gets the cached bindings and PVCs to provision in podBindingCache
// and makes the API update for those PVs/PVCs.
func (b *volumeBinder) bindAPIUpdate(podName string, bindings []*bindingInfo, claimsToProvision []*v1.PersistentVolumeClaim) error {
	if bindings == nil {
		return fmt.Errorf("failed to get cached bindings for pod %q", podName)
	}
	if claimsToProvision == nil {
		return fmt.Errorf("failed to get cached claims to provision for pod %q", podName)
	}

	lastProcessedBinding := 0
	lastProcessedProvisioning := 0
	defer func() {
		// only revert assumed cached updates for volumes we haven't successfully bound
		if lastProcessedBinding < len(bindings) {
			b.revertAssumedPVs(bindings[lastProcessedBinding:])
		}
		// only revert assumed cached updates for claims we haven't updated,
		if lastProcessedProvisioning < len(claimsToProvision) {
			b.revertAssumedPVCs(claimsToProvision[lastProcessedProvisioning:])
		}
	}()

	var (
		binding *bindingInfo
		i       int
		claim   *v1.PersistentVolumeClaim
	)

	// Do the actual prebinding. Let the PV controller take care of the rest
	// There is no API rollback if the actual binding fails
	for _, binding = range bindings {
		klog.V(4).InfoS("Binding PV to PVC", "podName", podName, "PV", klog.KObj(binding.pv), "PVC", klog.KObj(binding.pvc))
		// TODO: does it hurt if we make an api call and nothing needs to be updated?
		klog.V(2).InfoS("PVC was bound to PV", "PVC", klog.KObj(binding.pvc), "PV", klog.KObj(binding.pv))
		newPV, err := b.kubeClient.CoreV1().PersistentVolumes().Update(context.TODO(), binding.pv, metav1.UpdateOptions{})
		if err != nil {
			klog.ErrorS(err, "Failed to bind PV to PVC", "podName", podName, "PV", klog.KObj(binding.pv), "PVC", klog.KObj(binding.pvc), "err", err)
			return err
		}
		klog.V(4).InfoS("Bound PV to PVC", "podName", podName, "PV", klog.KObj(binding.pv), "PVC", klog.KObj(binding.pvc))
		// Save updated object from apiserver for later checking.
		binding.pv = newPV
		lastProcessedBinding++
	}

	// Update claims objects to trigger volume provisioning. Let the PV controller take care of the rest
	// PV controller is expect to signal back by removing related annotations if actual provisioning fails
	for i, claim = range claimsToProvision {
		klog.V(4).InfoS("Updating PVC to trigger volume provisioning", "podName", podName, "PVC", klog.KObj(claim))
		newClaim, err := b.kubeClient.CoreV1().PersistentVolumeClaims(claim.Namespace).Update(context.TODO(), claim, metav1.UpdateOptions{})
		if err != nil {
			klog.ErrorS(err, "Failed to update PVC to trigger volume provisioning", "podName", podName)
			return err
		}
		// Save updated object from apiserver for later checking.
		claimsToProvision[i] = newClaim
		lastProcessedProvisioning++
	}

	return nil
}

var versioner = etcd3.APIObjectVersioner{}

// checkBindings runs through all the PVCs in the Pod and checks:
// * if the PVC is fully bound
// * if there are any conditions that require binding to fail and be retried
//
// It returns true when all of the Pod's PVCs are fully bound, and error if
// binding (and scheduling) needs to be retried
// Note that it checks on API objects not PV/PVC cache, this is because
// PV/PVC cache can be assumed again in main scheduler loop, we must check
// latest state in API server which are shared with PV controller and
// provisioners
func (b *volumeBinder) checkBindings(pod *v1.Pod, bindings []*bindingInfo, claimsToProvision []*v1.PersistentVolumeClaim) (bool, error) {
	podName := getPodName(pod)
	if bindings == nil {
		return false, fmt.Errorf("failed to get cached bindings for pod %q", podName)
	}
	if claimsToProvision == nil {
		return false, fmt.Errorf("failed to get cached claims to provision for pod %q", podName)
	}

	node, err := b.nodeInformer.Lister().Get(pod.Spec.NodeName)
	if err != nil {
		return false, fmt.Errorf("failed to get node %q: %v", pod.Spec.NodeName, err)
	}

	csiNode, err := b.csiNodeInformer.Lister().Get(node.Name)
	if err != nil {
		// TODO: return the error once CSINode is created by default
		klog.InfoS("Failed to get a CSINode object for the node", "nodeName", node.Name, "err", err)
	}

	// Check for any conditions that might require scheduling retry

	// When pod is removed from scheduling queue because of deletion or any
	// other reasons, binding operation should be cancelled. There is no need
	// to check PV/PVC bindings any more.
	// We check pod binding cache here which will be cleared when pod is
	// removed from scheduling queue.
	if b.podBindingCache.GetDecisions(pod) == nil {
		return false, fmt.Errorf("pod %q does not exist any more", podName)
	}

	for _, binding := range bindings {
		pv, err := b.pvCache.GetAPIPV(binding.pv.Name)
		if err != nil {
			return false, fmt.Errorf("failed to check binding: %v", err)
		}

		pvc, err := b.pvcCache.GetAPIPVC(getPVCName(binding.pvc))
		if err != nil {
			return false, fmt.Errorf("failed to check binding: %v", err)
		}

		// Because we updated PV in apiserver, skip if API object is older
		// and wait for new API object propagated from apiserver.
		if versioner.CompareResourceVersion(binding.pv, pv) > 0 {
			return false, nil
		}

		pv, err = b.tryTranslatePVToCSI(pv, csiNode)
		if err != nil {
			return false, fmt.Errorf("failed to translate pv to csi: %v", err)
		}

		// Check PV's node affinity (the node might not have the proper label)
		if err := volumeutil.CheckNodeAffinity(pv, node.Labels); err != nil {
			return false, fmt.Errorf("pv %q node affinity doesn't match node %q: %v", pv.Name, node.Name, err)
		}

		// Check if pv.ClaimRef got dropped by unbindVolume()
		if pv.Spec.ClaimRef == nil || pv.Spec.ClaimRef.UID == "" {
			return false, fmt.Errorf("ClaimRef got reset for pv %q", pv.Name)
		}

		// Check if pvc is fully bound
		if !b.isPVCFullyBound(pvc) {
			return false, nil
		}
	}

	for _, claim := range claimsToProvision {
		pvc, err := b.pvcCache.GetAPIPVC(getPVCName(claim))
		if err != nil {
			return false, fmt.Errorf("failed to check provisioning pvc: %v", err)
		}

		// Because we updated PVC in apiserver, skip if API object is older
		// and wait for new API object propagated from apiserver.
		if versioner.CompareResourceVersion(claim, pvc) > 0 {
			return false, nil
		}

		// Check if selectedNode annotation is still set
		if pvc.Annotations == nil {
			return false, fmt.Errorf("selectedNode annotation reset for PVC %q", pvc.Name)
		}
		selectedNode := pvc.Annotations[pvutil.AnnSelectedNode]
		if selectedNode != pod.Spec.NodeName {
			// If provisioner fails to provision a volume, selectedNode
			// annotation will be removed to signal back to the scheduler to
			// retry.
			return false, fmt.Errorf("provisioning failed for PVC %q", pvc.Name)
		}

		// If the PVC is bound to a PV, check its node affinity
		if pvc.Spec.VolumeName != "" {
			pv, err := b.pvCache.GetAPIPV(pvc.Spec.VolumeName)
			if err != nil {
				if _, ok := err.(*errNotFound); ok {
					// We tolerate NotFound error here, because PV is possibly
					// not found because of API delay, we can check next time.
					// And if PV does not exist because it's deleted, PVC will
					// be unbound eventually.
					return false, nil
				}
				return false, fmt.Errorf("failed to get pv %q from cache: %v", pvc.Spec.VolumeName, err)
			}

			pv, err = b.tryTranslatePVToCSI(pv, csiNode)
			if err != nil {
				return false, err
			}

			if err := volumeutil.CheckNodeAffinity(pv, node.Labels); err != nil {
				return false, fmt.Errorf("pv %q node affinity doesn't match node %q: %v", pv.Name, node.Name, err)
			}
		}

		// Check if pvc is fully bound
		if !b.isPVCFullyBound(pvc) {
			return false, nil
		}
	}

	// All pvs and pvcs that we operated on are bound
	klog.V(4).InfoS("Confirmed all PVCs are bound", "pod", klog.KObj(pod))
	return true, nil
}

func (b *baseVolumeBinder) isVolumeBound(namespace string, vol *v1.Volume) (bool, *v1.PersistentVolumeClaim, error) {
	if vol.PersistentVolumeClaim == nil {
		return true, nil, nil
	}

	pvcName := vol.PersistentVolumeClaim.ClaimName
	return b.isPVCBound(namespace, pvcName)
}

func (b *baseVolumeBinder) isPVCBound(namespace, pvcName string) (bool, *v1.PersistentVolumeClaim, error) {
	claim := &v1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{
			Name:      pvcName,
			Namespace: namespace,
		},
	}
	pvcKey := getPVCName(claim)
	pvc, err := b.pvcCache.GetPVC(pvcKey)
	if err != nil || pvc == nil {
		return false, nil, fmt.Errorf("error getting PVC %q: %v", pvcKey, err)
	}

	fullyBound := b.isPVCFullyBound(pvc)
	if fullyBound {
		klog.V(4).InfoS("PVC was fully bound to PV", "PVC", klog.KObj(claim), "volumeName", pvc.Spec.VolumeName)
	} else {
		if pvc.Spec.VolumeName != "" {
			klog.V(4).InfoS("PVC was not fully bound to PV", "PVC", klog.KObj(claim), "volumeName", pvc.Spec.VolumeName)
		} else {
			klog.V(4).InfoS("PVC was not bound", "PVC", klog.KObj(claim))
		}
	}
	return fullyBound, pvc, nil
}

func (b *baseVolumeBinder) isPVCFullyBound(pvc *v1.PersistentVolumeClaim) bool {
	return pvc.Spec.VolumeName != "" && metav1.HasAnnotation(pvc.ObjectMeta, pvutil.AnnBindCompleted)
}

// arePodVolumesBound returns true if all volumes are fully bound
func (b *volumeBinder) arePodVolumesBound(pod *v1.Pod) bool {
	for _, vol := range pod.Spec.Volumes {
		if isBound, _, _ := b.isVolumeBound(pod.Namespace, &vol); !isBound {
			// Pod has at least one PVC that needs binding
			return false
		}
	}
	return true
}

// getPodVolumes returns a pod's PVCs separated into bound, unbound with delayed binding (including provisioning)
// and unbound with immediate binding (including prebound)
func (b *baseVolumeBinder) getPodVolumes(pod *v1.Pod) (boundClaims []*v1.PersistentVolumeClaim, unboundClaimsDelayBinding []*v1.PersistentVolumeClaim, unboundClaimsImmediate []*v1.PersistentVolumeClaim, err error) {
	boundClaims = []*v1.PersistentVolumeClaim{}
	unboundClaimsImmediate = []*v1.PersistentVolumeClaim{}
	unboundClaimsDelayBinding = []*v1.PersistentVolumeClaim{}

	for _, vol := range pod.Spec.Volumes {
		volumeBound, pvc, err := b.isVolumeBound(pod.Namespace, &vol)
		if err != nil {
			return nil, nil, nil, err
		}
		if pvc == nil {
			continue
		}
		if volumeBound {
			boundClaims = append(boundClaims, pvc)
		} else {
			delayBindingMode, err := pvutil.IsDelayBindingMode(pvc, b.classLister)
			if err != nil {
				return nil, nil, nil, err
			}
			// Prebound PVCs are treated as unbound immediate binding
			if delayBindingMode && pvc.Spec.VolumeName == "" {
				// Scheduler path
				unboundClaimsDelayBinding = append(unboundClaimsDelayBinding, pvc)
			} else {
				// !delayBindingMode || pvc.Spec.VolumeName != ""
				// Immediate binding should have already been bound
				unboundClaimsImmediate = append(unboundClaimsImmediate, pvc)
			}
		}
	}
	return boundClaims, unboundClaimsDelayBinding, unboundClaimsImmediate, nil
}

func (b *baseVolumeBinder) checkBoundClaims(claims []*v1.PersistentVolumeClaim, nodeName string, nodeLabels map[string]string, podName string) (bool, error) {
	csiNode, err := b.csiNodeInformer.Lister().Get(nodeName)
	if err != nil {
		// TODO: return the error once CSINode is created by default
		klog.InfoS("Failed to get a CSINode object for the node", "nodeName", nodeName, "err", err)
	}

	for _, pvc := range claims {
		pvName := pvc.Spec.VolumeName
		pv, err := b.pvCache.GetPV(pvName)
		if err != nil {
			return false, err
		}

		pv, err = b.tryTranslatePVToCSI(pv, csiNode)
		if err != nil {
			return false, err
		}

		err = volumeutil.CheckNodeAffinity(pv, nodeLabels)
		if err != nil {
			klog.InfoS("PersistentVolume and Node mismatched for Pod", "volumeName", pvName, "nodeName", nodeName, "podName", podName, "err", err)
			return false, nil
		}
		klog.V(4).InfoS("PersistentVolume and Node matched for Pod", "volumeName", pvName, "nodeName", nodeName, "podName", podName)
	}

	klog.V(4).InfoS("Confirmed All bound volumes for Pod matched with Node", "nodeName", nodeName, "podName", podName)
	return true, nil
}

// findMatchingVolumes tries to find matching volumes for given claims,
// and return unbound claims for further provision.
func (b *baseVolumeBinder) findMatchingVolumes(pod *v1.Pod, claimsToBind []*v1.PersistentVolumeClaim, nodeName string, nodeLabels map[string]string) (foundMatches bool, bindings []*bindingInfo, unboundClaims []*v1.PersistentVolumeClaim, err error) {
	// Sort all the claims by increasing size request to get the smallest fits
	sort.Sort(byPVCSize(claimsToBind))

	chosenPVs := map[string]*v1.PersistentVolume{}

	foundMatches = true

	for _, pvc := range claimsToBind {
		// Get storage class name from each PVC
		storageClassName := helper.GetPersistentVolumeClaimClass(pvc)
		allPVs := b.pvCache.ListPVs(storageClassName)

		// Find a matching PV
		pv, err := pvutil.FindMatchingVolume(pvc, allPVs, nodeLabels, chosenPVs, true)
		if err != nil {
			return false, nil, nil, err
		}
		if pv == nil {
			klog.V(4).InfoS("No matching volumes found", "nodeName", nodeName, "pod", klog.KObj(pod), "PVC", klog.KObj(pvc))
			unboundClaims = append(unboundClaims, pvc)
			foundMatches = false
			continue
		}

		// matching PV needs to be excluded so we don't select it again
		chosenPVs[pv.Name] = pv
		bindings = append(bindings, &bindingInfo{pv: pv, pvc: pvc})
		klog.V(4).InfoS("Found a matching volume", "nodeName", nodeName, "pod", klog.KObj(pod), "PVC", klog.KObj(pvc), "PV", klog.KObj(pv))
	}

	if foundMatches {
		klog.V(4).InfoS("Found matching volumes", "nodeName", nodeName, "pod", klog.KObj(pod))
	}

	return
}

// checkVolumeProvisions checks given unbound claims (the claims have gone through func
// findMatchingVolumes, and do not have matching volumes for binding), and return true
// if all of the claims are eligible for dynamic provision.
func (b *baseVolumeBinder) checkVolumeProvisions(pod *v1.Pod, claimsToProvision []*v1.PersistentVolumeClaim, nodeName string, nodeLabels map[string]string) (provisionSatisfied bool, provisionedClaims []*v1.PersistentVolumeClaim, err error) {
	provisionedClaims = []*v1.PersistentVolumeClaim{}

	for _, claim := range claimsToProvision {
		pvcName := getPVCName(claim)
		className := helper.GetPersistentVolumeClaimClass(claim)
		if className == "" {
			return false, nil, fmt.Errorf("no class for claim %q", pvcName)
		}

		class, err := b.classLister.Get(className)
		if err != nil {
			return false, nil, fmt.Errorf("failed to find storage class %q", className)
		}
		provisioner := class.Provisioner
		if provisioner == "" || provisioner == pvutil.NotSupportedProvisioner {
			klog.V(4).InfoS("StorageClass of PVC did not support dynamic provisioning", "storageClass", klog.KObj(class), "PVC", klog.KObj(claim))
			return false, nil, nil
		}

		// Check if the node can satisfy the topology requirement in the class
		if !helper.MatchTopologySelectorTerms(class.AllowedTopologies, labels.Set(nodeLabels)) {
			klog.V(4).InfoS("Node failed to satisfy provisioning topology requirements of claim", "nodeName", nodeName, "PVC", klog.KObj(claim))
			return false, nil, nil
		}

		// TODO: Check if capacity of the node domain in the storage class
		// can satisfy resource requirement of given claim

		provisionedClaims = append(provisionedClaims, claim)

	}
	klog.V(4).InfoS("Provisioning for claims of pod that has no matching volumes on node", "pod", klog.KObj(pod), "nodeName", nodeName)

	return true, provisionedClaims, nil
}

func (b *volumeBinder) revertAssumedPVs(bindings []*bindingInfo) {
	for _, bindingInfo := range bindings {
		b.pvCache.Restore(bindingInfo.pv.Name)
	}
}

func (b *volumeBinder) revertAssumedPVCs(claims []*v1.PersistentVolumeClaim) {
	for _, claim := range claims {
		b.pvcCache.Restore(getPVCName(claim))
	}
}

type bindingInfo struct {
	// Claim that needs to be bound
	pvc *v1.PersistentVolumeClaim

	// Proposed PV to bind to this claim
	pv *v1.PersistentVolume
}

type byPVCSize []*v1.PersistentVolumeClaim

func (a byPVCSize) Len() int {
	return len(a)
}

func (a byPVCSize) Swap(i, j int) {
	a[i], a[j] = a[j], a[i]
}

func (a byPVCSize) Less(i, j int) bool {
	iSize := a[i].Spec.Resources.Requests[v1.ResourceStorage]
	jSize := a[j].Spec.Resources.Requests[v1.ResourceStorage]
	// return true if iSize is less than jSize
	return iSize.Cmp(jSize) == -1
}

func claimToClaimKey(claim *v1.PersistentVolumeClaim) string {
	return fmt.Sprintf("%s/%s", claim.Namespace, claim.Name)
}

// isCSIMigrationOnForPlugin checks if CSI migrartion is enabled for a given plugin.
func isCSIMigrationOnForPlugin(pluginName string) bool {
	switch pluginName {
	case csiplugins.AWSEBSInTreePluginName:
		return utilfeature.DefaultFeatureGate.Enabled(features.CSIMigrationAWS)
	case csiplugins.GCEPDInTreePluginName:
		return utilfeature.DefaultFeatureGate.Enabled(features.CSIMigrationGCE)
	case csiplugins.AzureDiskInTreePluginName:
		return utilfeature.DefaultFeatureGate.Enabled(features.CSIMigrationAzureDisk)
	case csiplugins.CinderInTreePluginName:
		return utilfeature.DefaultFeatureGate.Enabled(features.CSIMigrationOpenStack)
	}
	return false
}

// isPluginMigratedToCSIOnNode checks if an in-tree plugin has been migrated to a CSI driver on the node.
func isPluginMigratedToCSIOnNode(pluginName string, csiNode *storagev1.CSINode) bool {
	if csiNode == nil {
		return false
	}

	csiNodeAnn := csiNode.GetAnnotations()
	if csiNodeAnn == nil {
		return false
	}

	var mpaSet sets.String
	mpa := csiNodeAnn[v1.MigratedPluginsAnnotationKey]
	if len(mpa) == 0 {
		mpaSet = sets.NewString()
	} else {
		tok := strings.Split(mpa, ",")
		mpaSet = sets.NewString(tok...)
	}

	return mpaSet.Has(pluginName)
}

// tryTranslatePVToCSI will translate the in-tree PV to CSI if it meets the criteria. If not, it returns the unmodified in-tree PV.
func (b *baseVolumeBinder) tryTranslatePVToCSI(pv *v1.PersistentVolume, csiNode *storagev1.CSINode) (*v1.PersistentVolume, error) {
	if !b.translator.IsPVMigratable(pv) {
		return pv, nil
	}

	if !utilfeature.DefaultFeatureGate.Enabled(features.CSIMigration) {
		return pv, nil
	}

	pluginName, err := b.translator.GetInTreePluginNameFromSpec(pv, nil)
	if err != nil {
		return nil, fmt.Errorf("could not get plugin name from pv: %v", err)
	}

	if !isCSIMigrationOnForPlugin(pluginName) {
		return pv, nil
	}

	if !isPluginMigratedToCSIOnNode(pluginName, csiNode) {
		return pv, nil
	}

	transPV, err := b.translator.TranslateInTreePVToCSI(pv)
	if err != nil {
		return nil, fmt.Errorf("could not translate pv: %v", err)
	}

	return transPV, nil
}

type podVolumesInfo struct {
	boundVolumesSatisfied   bool
	unboundVolumesSatisfied bool
	matchedBindings         []*bindingInfo
	provisionedClaims       []*v1.PersistentVolumeClaim
}

func (b *baseVolumeBinder) findPodVolumes(pod *v1.Pod, nodeName string, nodeLabels map[string]string, podVolumes podVolumesInfo) (podVolumesInfo, ConflictReasons, error) {
	podName := getPodName(pod)
	// The pod's volumes need to be processed in one call to avoid the race condition where
	// volumes can get bound/provisioned in between calls.
	boundClaims, claimsToBind, unboundClaimsImmediate, err := b.getPodVolumes(pod)
	if err != nil {
		return podVolumes, nil, err
	}

	// Immediate claims should be bound
	if len(unboundClaimsImmediate) > 0 {
		return podVolumes, ConflictReasons{ErrUnboundImmediatePVC}, nil
	}

	// Check PV node affinity on bound volumes
	if len(boundClaims) > 0 {
		podVolumes.boundVolumesSatisfied, err = b.checkBoundClaims(boundClaims, nodeName, nodeLabels, podName)
		if err != nil {
			return podVolumes, nil, err
		}
	}

	// Find matching volumes and node for unbound claims
	if len(claimsToBind) > 0 {
		var (
			claimsToFindMatching []*v1.PersistentVolumeClaim
			claimsToProvision    []*v1.PersistentVolumeClaim
		)

		// Filter out claims to provision
		for _, claim := range claimsToBind {
			if selectedNode, ok := claim.Annotations[pvutil.AnnSelectedNode]; ok {
				if selectedNode != nodeName {
					// Fast path, skip unmatched node.
					podVolumes.unboundVolumesSatisfied = false
					return podVolumes, nil, nil
				}
				claimsToProvision = append(claimsToProvision, claim)
			} else {
				claimsToFindMatching = append(claimsToFindMatching, claim)
			}
		}

		// Find matching volumes
		if len(claimsToFindMatching) > 0 {
			var unboundClaims []*v1.PersistentVolumeClaim
			podVolumes.unboundVolumesSatisfied, podVolumes.matchedBindings, unboundClaims, err = b.findMatchingVolumes(pod, claimsToFindMatching, nodeName, nodeLabels)
			if err != nil {
				return podVolumes, nil, err
			}
			claimsToProvision = append(claimsToProvision, unboundClaims...)
		}

		// Check for claims to provision
		if len(claimsToProvision) > 0 {
			podVolumes.unboundVolumesSatisfied, podVolumes.provisionedClaims, err = b.checkVolumeProvisions(pod, claimsToProvision, nodeName, nodeLabels)
			if err != nil {
				return podVolumes, nil, err
			}
		}
	}

	return podVolumes, nil, nil
}
