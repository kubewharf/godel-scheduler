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

package util

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	nodev1alpha1 "github.com/kubewharf/godel-scheduler-api/pkg/apis/node/v1alpha1"
	"github.com/kubewharf/godel-scheduler-api/pkg/apis/scheduling/v1alpha1"
	crdclientset "github.com/kubewharf/godel-scheduler-api/pkg/client/clientset/versioned"
	appsv1 "k8s.io/api/apps/v1"
	v1 "k8s.io/api/core/v1"
	policy "k8s.io/api/policy/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	utilerrors "k8s.io/apimachinery/pkg/util/errors"
	"k8s.io/apimachinery/pkg/util/strategicpatch"
	clientset "k8s.io/client-go/kubernetes"
	"k8s.io/klog/v2"
)

const (
	CanBePreemptedAnnotationKey = "godel.bytedance.com/can-be-preempted"
	CanBePreempted              = "true"
	CannotBePreempted           = "false"
	PreemptionPolicyKey         = "godel.bytedance.com/preemption-policy"
	ObjectNameField             = "metadata.name"
	// hardcode GPU name here
	// TODO: support more GPU names
	ResourceGPU  v1.ResourceName = "nvidia.com/gpu"
	ResourceNuma v1.ResourceName = "numa"
	// Debug Level
	DebugModeAnnotationKey = "godel.bytedance.com/debug-mode"
	// Debug Mode ON
	DebugModeOn = "on"
	// Debug Mode OFF
	DebugModeOff = "off"
	// Node that the pod want to watch by node labels
	WatchNodeNameLabelName = "godel.bytedance.com/watch-node-label"

	ResourceSriov v1.ResourceName = "bytedance.com/sriov.nic"

	SemicolonSeperator = ";"
	CommaSeperator     = ","
	ColonSeperator     = ":"
	EqualSignSeperator = "="

	QoSLevelKey         = "katalyst.kubewharf.io/qos_level"
	MemoyEnhancementKey = "katalyst.kubewharf.io/memory_enhancement"
	// NumaBindingKey is a key that illustrate whether the pod needs bind numa
	NumaBindingKey   = "numa_binding"
	NumaExclusiveKey = "numa_exclusive"

	OwnerTypeDaemonSet  = "DaemonSet"
	OwnerTypeReplicaSet = "ReplicaSet"

	MaxAPICallRetryTimes = 3 // TODO: 5 will cause a timeout in UT (30s)
)

const MemoyEnhancementTrue string = "true"

type QoSLevel string

const (
	DedicatedCores QoSLevel = "dedicated_cores"
	SharedCores    QoSLevel = "shared_cores"
	ReclaimedCores QoSLevel = "reclaimed_cores"
)

// Get Pod debug mode annotation
func GetPodDebugMode(pod *v1.Pod) string {
	debugMode, ok := pod.Annotations[DebugModeAnnotationKey]
	if !ok {
		return DebugModeOff
	}

	return debugMode
}

// Get Pod watch label value string
func GetPodWatchLabelValueString(pod *v1.Pod) string {
	watchNodeLabelString, ok := pod.Annotations[WatchNodeNameLabelName]
	if !ok {
		return ""
	}
	return watchNodeLabelString
}

func CheckIfNodeLabelsInSpecifiedLabels(labels map[string]string, requiredLabels map[string]string) bool {
	// If the node has either of specified label values, record it to print debug message
	for key, value := range requiredLabels {
		if labels[key] != value {
			return false
		}
	}
	return true
}

// GetPodFullName returns a name that uniquely identifies a pod.
func GetPodFullName(pod *v1.Pod) string {
	// Use underscore as the delimiter because it is not allowed in pod name
	// (DNS subdomain format).
	return pod.Name + "_" + pod.Namespace
}

// GetPodStartTime returns start time of the given pod or current timestamp
// if it hasn't started yet.
func GetPodStartTime(pod *v1.Pod) *metav1.Time {
	if pod.Status.StartTime != nil {
		return pod.Status.StartTime
	}
	// Assumed pods and bound pods that haven't started don't have a StartTime yet.
	return &metav1.Time{Time: time.Now()}
}

const (
	deployNameKeyInPodLabels = "name"
)

// TODO: if we support multiple controller kinds later, we need to get the controller name
func GetDeployNameFromPod(pod *v1.Pod) string {
	return pod.Labels[deployNameKeyInPodLabels]
}

// GetPodAffinityTerms gets pod affinity terms by a pod affinity object.
func GetPodAffinityTerms(affinity *v1.Affinity) (terms []v1.PodAffinityTerm) {
	if affinity != nil && affinity.PodAffinity != nil {
		if len(affinity.PodAffinity.RequiredDuringSchedulingIgnoredDuringExecution) != 0 {
			terms = affinity.PodAffinity.RequiredDuringSchedulingIgnoredDuringExecution
		}
		// TODO: Uncomment this block when implement RequiredDuringSchedulingRequiredDuringExecution.
		//if len(affinity.PodAffinity.RequiredDuringSchedulingRequiredDuringExecution) != 0 {
		//	terms = append(terms, affinity.PodAffinity.RequiredDuringSchedulingRequiredDuringExecution...)
		//}
	}
	return terms
}

// GetPodRequiredAntiAffinityTerms gets pod affinity terms by a pod require-anti-affinity.
func GetPodRequiredAntiAffinityTerms(affinity *v1.Affinity) (terms []v1.PodAffinityTerm) {
	if affinity != nil && affinity.PodAntiAffinity != nil {
		if len(affinity.PodAntiAffinity.RequiredDuringSchedulingIgnoredDuringExecution) != 0 {
			terms = affinity.PodAntiAffinity.RequiredDuringSchedulingIgnoredDuringExecution
		}
		// TODO: Uncomment this block when implement RequiredDuringSchedulingRequiredDuringExecution.
		//if len(affinity.PodAntiAffinity.RequiredDuringSchedulingRequiredDuringExecution) != 0 {
		//	terms = append(terms, affinity.PodAntiAffinity.RequiredDuringSchedulingRequiredDuringExecution...)
		//}
	}
	return terms
}

// GetPodPreferedAntiAffinityTerms gets pod affinity terms by a pod prefered-anti-affinity.
func GetPodPreferedAntiAffinityTerms(affinity *v1.Affinity) (terms []v1.WeightedPodAffinityTerm) {
	if affinity != nil && affinity.PodAntiAffinity != nil {
		if len(affinity.PodAntiAffinity.PreferredDuringSchedulingIgnoredDuringExecution) != 0 {
			terms = affinity.PodAntiAffinity.PreferredDuringSchedulingIgnoredDuringExecution
		}
	}
	return terms
}

// PatchPod calculates the delta bytes change from <old> to <new>,
// and then submit a request to API server to patch the pod changes.
func PatchPod(cs clientset.Interface, old *v1.Pod, new *v1.Pod) (err error) {
	if cs == nil {
		return fmt.Errorf("client is nil")
	}
	defer func() {
		if r := recover(); r != nil {
			err = errors.New("json.Marshal panic")
			klog.ErrorS(err, "Panic in PatchPod, return directly.")
		}
	}()

	oldData, err := json.Marshal(old)
	if err != nil {
		return err
	}

	newData, err := json.Marshal(new)
	if err != nil {
		return err
	}
	patchBytes, err := strategicpatch.CreateTwoWayMergePatch(oldData, newData, &v1.Pod{})
	if err != nil {
		return fmt.Errorf("failed to create merge patch for pod %q/%q: %v", old.Namespace, old.Name, err)
	}
	var notFoundErr error
	retryErr := Retry(MaxAPICallRetryTimes, time.Second, func() error {
		_, err = cs.CoreV1().Pods(old.Namespace).Patch(context.TODO(), old.Name, types.StrategicMergePatchType, patchBytes, metav1.PatchOptions{}, "status")
		if err != nil && apierrors.IsNotFound(err) {
			// If the pod has been deleted, end the retry process in advance.
			notFoundErr = err
			return nil
		}
		return err
	})
	if notFoundErr != nil {
		return notFoundErr
	}
	return retryErr
}

// DeletePod deletes the given <pod> from API server
func DeletePod(cs clientset.Interface, pod *v1.Pod) error {
	if cs == nil {
		return fmt.Errorf("client is nil")
	}
	return cs.CoreV1().Pods(pod.Namespace).Delete(context.TODO(), pod.Name, metav1.DeleteOptions{})
}

// ClearNominatedNodeName internally submit a patch request to API server
// to set each pods[*].Status.NominatedNodeName> to "".
func ClearNominatedNodeName(cs clientset.Interface, pods ...*v1.Pod) utilerrors.Aggregate {
	var errs []error
	for _, p := range pods {
		if len(p.Status.NominatedNodeName) == 0 {
			continue
		}
		podCopy := p.DeepCopy()
		podCopy.Status.NominatedNodeName = ""
		if err := PatchPod(cs, p, podCopy); err != nil {
			errs = append(errs, err)
		}
	}
	return utilerrors.NewAggregate(errs)
}

// PostScheduler create v1alpha1.Scheduler in API server
func PostScheduler(cs crdclientset.Interface, scheduler *v1alpha1.Scheduler) (*v1alpha1.Scheduler, error) {
	return cs.SchedulingV1alpha1().Schedulers().Create(context.TODO(), scheduler, metav1.CreateOptions{})
}

// UpdateSchedulerStatus updates v1alpha1.Scheduler in API server
func UpdateSchedulerStatus(cs crdclientset.Interface, scheduler *v1alpha1.Scheduler) (*v1alpha1.Scheduler, error) {
	return cs.SchedulingV1alpha1().Schedulers().UpdateStatus(context.TODO(), scheduler, metav1.UpdateOptions{})
}

// GetScheduler returns the latest <scheduler> from API server
func GetScheduler(cs crdclientset.Interface, schedulerName string) (*v1alpha1.Scheduler, error) {
	return cs.SchedulingV1alpha1().Schedulers().Get(context.TODO(), schedulerName, metav1.GetOptions{})
}

// NeedConsiderTopology checks if need to consider numa topology
func NeedConsiderTopology(pod *v1.Pod) (bool, bool) {
	if pod.Annotations[QoSLevelKey] != string(DedicatedCores) {
		return false, false
	}
	if memoryEnhancementStr, ok := pod.Annotations[MemoyEnhancementKey]; ok {
		return memoryEnhancement(memoryEnhancementStr)
	}
	return false, false
}

// bool, if need bind numa
// bool, if is exclusive
func memoryEnhancement(memoryEnhancementStr string) (bool, bool) {
	if memoryEnhancementStr == "" {
		return false, false
	}
	var memoryEnhancement map[string]string
	if err := json.Unmarshal([]byte(memoryEnhancementStr), &memoryEnhancement); err != nil {
		klog.ErrorS(err, "Failed to unmarshal memoryEnhancement in string", "memoryEnhancementAnnValue", memoryEnhancementStr)
	}
	numaBinding := memoryEnhancement[NumaBindingKey] == MemoyEnhancementTrue
	var numaExclusive bool
	if _, ok := memoryEnhancement[NumaExclusiveKey]; ok {
		numaExclusive = memoryEnhancement[NumaExclusiveKey] == MemoyEnhancementTrue
	} else if numaBinding {
		numaExclusive = true
	}
	return numaBinding, numaExclusive
}

func UnmarshalMicroTopology(microTopologyStr string) (map[int]*v1.ResourceList, error) {
	if microTopologyStr == "" || microTopologyStr == "null" {
		return nil, nil
	}
	res := make(map[int]*v1.ResourceList)
	for _, numaStatusStr := range strings.Split(microTopologyStr, SemicolonSeperator) {
		numaStatus := strings.Split(numaStatusStr, ColonSeperator)
		if len(numaStatus) != 2 {
			return nil, fmt.Errorf("failed to parse micro topology in annotation: %s", numaStatusStr)
		}
		numaId, err := strconv.ParseInt(numaStatus[0], 10, 64)
		if err != nil {
			return nil, fmt.Errorf("failed to parse numa id in annotation '%s': %v", numaStatus[0], err)
		}
		if res[int(numaId)] == nil {
			res[int(numaId)] = &v1.ResourceList{}
		}
		for _, resources := range strings.Split(numaStatus[1], CommaSeperator) {
			resourceRequest := strings.Split(resources, EqualSignSeperator)
			if len(resourceRequest) != 2 {
				return nil, fmt.Errorf("failed to parse resource requests %s", resources)
			}
			resourceName := resourceRequest[0]
			resourceQuan, err := resource.ParseQuantity(resourceRequest[1])
			if err != nil {
				return nil, fmt.Errorf("failed to parse resource quantity %s: %v", resourceRequest[1], err)
			}
			(*res[int(numaId)])[v1.ResourceName(resourceName)] = resourceQuan
		}
	}
	return res, nil
}

func MarshalMicroTopology(topology map[int]*v1.ResourceList) string {
	var str string
	for numaId, resourceList := range topology {
		str = fmt.Sprintf("%s%d%s", str, numaId, ColonSeperator)
		if resourceList == nil {
			continue
		}
		for resourceName, resourceVal := range *resourceList {
			str = fmt.Sprintf("%s%s%s%s%s", str, resourceName, EqualSignSeperator, resourceVal.String(), CommaSeperator)
		}
		str = strings.TrimRight(str, CommaSeperator)
		str = fmt.Sprintf("%s%s", str, SemicolonSeperator)
	}
	str = strings.TrimRight(str, SemicolonSeperator)
	return str
}

func GetPodDebugModeOnNode(pod *v1.Pod, node *v1.Node, nmNode *nodev1alpha1.NMNode) bool {
	if GetPodDebugMode(pod) != DebugModeOn {
		return false
	}
	podWatchLabelValue := GetPodWatchLabelValueString(pod)
	if podWatchLabelValue == "" {
		return false
	}
	var labelSelectorMap map[string]string
	json.Unmarshal([]byte(podWatchLabelValue), &labelSelectorMap)
	if node != nil && CheckIfNodeLabelsInSpecifiedLabels(node.Labels, labelSelectorMap) {
		return true
	}
	if nmNode != nil && CheckIfNodeLabelsInSpecifiedLabels(nmNode.Labels, labelSelectorMap) {
		return true
	}
	return false
}

func Retry(attempts int, sleep time.Duration, f func() error) error {
	if err := f(); err != nil {
		if attempts--; attempts > 0 {
			// Add some randomness to prevent creating a Thundering Herd
			jitter := time.Duration(rand.Int63n(int64(sleep)))
			sleep = sleep + jitter/2

			time.Sleep(sleep)
			return Retry(attempts, 2*sleep, f)
		}
		return err
	}

	return nil
}

func GetPDBKey(pdb *policy.PodDisruptionBudget) string {
	return pdb.Namespace + "/" + pdb.Name
}

func FormatLabels(labels map[string]string) string {
	s := ""
	var keys []string
	for k := range labels {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		s += k + ":" + labels[k] + "&"
	}
	return strings.TrimRight(s, "&")
}

func SetMetaDataMap(obj *metav1.ObjectMeta, key string, value string) {
	metav1.SetMetaDataAnnotation(obj, key, value)
	SetMetaDataLabel(obj, key, value)
}

func SetMetaDataLabel(obj *metav1.ObjectMeta, key string, value string) {
	if obj.Labels == nil {
		obj.Labels = make(map[string]string)
	}
	obj.Labels[key] = value
}

func GetReplicaSetKey(rs *appsv1.ReplicaSet) string {
	return rs.Namespace + "/" + rs.Name
}

func GetDaemonSetKey(ds *appsv1.DaemonSet) string {
	return ds.Namespace + "/" + ds.Name
}

func EqualMap(m1, m2 map[string]string) bool {
	if len(m1) != len(m2) {
		return false
	}
	for k, v := range m1 {
		if v != m2[k] {
			return false
		}
	}
	return true
}

func ParallelizeUntil(stop *bool, workers int, pieces int, doWorkPiece func(piece int)) {
	chunk := (pieces + workers - 1) / workers
	wg := sync.WaitGroup{}
	wg.Add(workers)
	for i := 0; i < workers; i++ {
		go func(i int) {
			defer wg.Done()
			for j := 0; j < chunk; j++ {
				if *stop {
					return
				}
				index := i*chunk + j
				if index >= pieces {
					return
				}
				doWorkPiece(index)
			}
		}(i)
	}
	wg.Wait()
}

func MergeLabels(labels1, labels2 map[string]string) map[string]string {
	labels := make(map[string]string)
	if len(labels1) > 0 {
		for k, v := range labels1 {
			labels[k] = v
		}
	}
	if len(labels2) > 0 {
		for k, v := range labels2 {
			labels[k] = v
		}
	}
	return labels
}

func RemoveLabels(labels1, labels2 map[string]string) map[string]string {
	labels := make(map[string]string)
	if len(labels1) > 0 {
		for k, v := range labels1 {
			labels[k] = v
		}
	}
	if len(labels2) > 0 {
		for k := range labels2 {
			delete(labels, k)
		}
	}
	return labels
}

// TODO: revisit this rule.
func GetOwnerReferenceKey(pod *v1.Pod) string {
	for _, or := range pod.OwnerReferences {
		return getOwnerReferenceKey(or, pod.Namespace)
	}
	return ""
}

func getOwnerReferenceKey(or metav1.OwnerReference, namespace string) string {
	return or.Kind + "/" + namespace + "/" + or.Name + "/" + string(or.UID)
}
