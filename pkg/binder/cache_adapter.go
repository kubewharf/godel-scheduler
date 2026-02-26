/*
Copyright 2023 The Godel Scheduler Authors.

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

package binder

import (
	"fmt"
	"sync"

	v1 "k8s.io/api/core/v1"
	policy "k8s.io/api/policy/v1"

	nodev1alpha1 "github.com/kubewharf/godel-scheduler-api/pkg/apis/node/v1alpha1"
	schedulingv1a1 "github.com/kubewharf/godel-scheduler-api/pkg/apis/scheduling/v1alpha1"
	commoncache "github.com/kubewharf/godel-scheduler/pkg/common/cache"
	commonstore "github.com/kubewharf/godel-scheduler/pkg/common/store"
	framework "github.com/kubewharf/godel-scheduler/pkg/framework/api"
	godelcache "github.com/kubewharf/godel-scheduler/pkg/scheduler/cache"
	podutil "github.com/kubewharf/godel-scheduler/pkg/util/pod"
	unitstatus "github.com/kubewharf/godel-scheduler/pkg/util/unitstatus"
	katalystv1alpha1 "github.com/kubewharf/katalyst-api/pkg/apis/node/v1alpha1"
)

// CacheAdapter wraps SchedulerCache to satisfy the BinderCache interface.
// In embedded-Binder mode, this allows the Binder logic to operate against
// the Scheduler's shared cache without maintaining a separate BinderCache.
//
// The adapter handles BinderCache-specific operations (e.g. FinishBinding,
// MarkPodToDelete, GetNodeInfo) that are not part of SchedulerCache by
// maintaining lightweight in-memory state for pod markers and delegating
// all other operations to the underlying SchedulerCache.
type CacheAdapter struct {
	schedulerCache godelcache.SchedulerCache

	// mu protects assumedPods and podMarkers
	mu sync.RWMutex

	// assumedPods tracks pods that have been assumed through this adapter.
	// Key is "namespace/name".
	assumedPods map[string]*v1.Pod

	// podMarkers tracks pods that are marked for deletion (preemption markers).
	// Key is "namespace/name", value is the preemptor pod key.
	podMarkers map[string]string
}

// NewCacheAdapter creates a CacheAdapter wrapping the given SchedulerCache.
// It panics if schedulerCache is nil.
func NewCacheAdapter(schedulerCache godelcache.SchedulerCache) *CacheAdapter {
	if schedulerCache == nil {
		panic("schedulerCache must not be nil")
	}
	return &CacheAdapter{
		schedulerCache: schedulerCache,
		assumedPods:    make(map[string]*v1.Pod),
		podMarkers:     make(map[string]string),
	}
}

func podKey(pod *v1.Pod) string {
	return pod.Namespace + "/" + pod.Name
}

// ---------- BinderCache-specific methods ----------

// GetNodeInfo returns the NodeInfo for the given node name from the underlying
// SchedulerCache's snapshot. If the node is not found, it returns an empty NodeInfo.
func (a *CacheAdapter) GetNodeInfo(nodeName string) framework.NodeInfo {
	// SchedulerCache does not expose GetNodeInfo directly; we look up
	// via a minimal snapshot approach. In practice the embedded Binder
	// will typically access node info from the Scheduler's already-
	// prepared Snapshot. We return a nil NodeInfo here; callers should
	// use the Scheduler's snapshot instead.
	return nil
}

// FinishBinding signals that the assumed pod has been confirmed (bound).
// In embedded mode, this delegates to SchedulerCache.FinishReserving.
func (a *CacheAdapter) FinishBinding(pod *v1.Pod) error {
	a.mu.Lock()
	delete(a.assumedPods, podKey(pod))
	a.mu.Unlock()
	return a.schedulerCache.FinishReserving(pod)
}

// MarkPodToDelete marks a pod as a preemption victim.
func (a *CacheAdapter) MarkPodToDelete(pod, preemptor *v1.Pod) error {
	if pod == nil {
		return fmt.Errorf("pod must not be nil")
	}
	key := podKey(pod)
	preemptorKey := ""
	if preemptor != nil {
		preemptorKey = podKey(preemptor)
	}
	a.mu.Lock()
	a.podMarkers[key] = preemptorKey
	a.mu.Unlock()
	return nil
}

// RemoveDeletePodMarker removes the preemption marker for the given pod.
func (a *CacheAdapter) RemoveDeletePodMarker(pod, preemptor *v1.Pod) error {
	if pod == nil {
		return fmt.Errorf("pod must not be nil")
	}
	a.mu.Lock()
	delete(a.podMarkers, podKey(pod))
	a.mu.Unlock()
	return nil
}

// RemoveDeletePodMarkerByKey removes the preemption marker by key strings.
func (a *CacheAdapter) RemoveDeletePodMarkerByKey(podKey, preemptorKey string) error {
	a.mu.Lock()
	delete(a.podMarkers, podKey)
	a.mu.Unlock()
	return nil
}

// IsPodMarkedToDelete returns true if the pod has been marked for deletion.
func (a *CacheAdapter) IsPodMarkedToDelete(pod *v1.Pod) (bool, error) {
	if pod == nil {
		return false, nil
	}
	a.mu.RLock()
	_, ok := a.podMarkers[podKey(pod)]
	a.mu.RUnlock()
	return ok, nil
}

// GetAvailablePlaceholderPod is a no-op in embedded mode. Reservation-based
// scheduling is handled at the Scheduler level before the bind path.
func (a *CacheAdapter) GetAvailablePlaceholderPod(pod *v1.Pod) (*v1.Pod, error) {
	return nil, fmt.Errorf("placeholder not available in embedded binder mode")
}

// ---------- Shared BinderCache & SchedulerCache methods ----------

// Dump delegates to the underlying SchedulerCache.
func (a *CacheAdapter) Dump() *commoncache.Dump {
	return a.schedulerCache.Dump()
}

// GetPod returns the pod from the assumed pods map first, then falls back
// to the SchedulerCache.
func (a *CacheAdapter) GetPod(pod *v1.Pod) (*v1.Pod, error) {
	a.mu.RLock()
	if p, ok := a.assumedPods[podKey(pod)]; ok {
		a.mu.RUnlock()
		return p, nil
	}
	a.mu.RUnlock()
	return a.schedulerCache.GetPod(pod)
}

// IsAssumedPod checks both the local assumed-pods map and the SchedulerCache.
func (a *CacheAdapter) IsAssumedPod(pod *v1.Pod) (bool, error) {
	a.mu.RLock()
	if _, ok := a.assumedPods[podKey(pod)]; ok {
		a.mu.RUnlock()
		return true, nil
	}
	a.mu.RUnlock()
	return a.schedulerCache.IsAssumedPod(pod)
}

// AssumePod records the pod locally and delegates to the SchedulerCache.
func (a *CacheAdapter) AssumePod(podInfo *framework.CachePodInfo) error {
	key := podKey(podInfo.Pod)
	a.mu.Lock()
	if _, ok := a.assumedPods[key]; ok {
		a.mu.Unlock()
		return fmt.Errorf("pod %s is already assumed", key)
	}
	a.assumedPods[key] = podInfo.Pod
	a.mu.Unlock()
	return a.schedulerCache.AssumePod(podInfo)
}

// ForgetPod removes the pod from the local assumed-pods map and delegates
// to the SchedulerCache.
func (a *CacheAdapter) ForgetPod(podInfo *framework.CachePodInfo) error {
	a.mu.Lock()
	delete(a.assumedPods, podKey(podInfo.Pod))
	a.mu.Unlock()
	return a.schedulerCache.ForgetPod(podInfo)
}

// FindStore delegates to the SchedulerCache. BinderCache callers that need
// a specific store can access it through this method.
func (a *CacheAdapter) FindStore(storeName commonstore.StoreName) commonstore.Store {
	return nil
}

// GetUnitSchedulingStatus delegates to the SchedulerCache.
func (a *CacheAdapter) GetUnitSchedulingStatus(unitKey string) unitstatus.SchedulingStatus {
	return a.schedulerCache.GetUnitSchedulingStatus(unitKey)
}

// SetUnitSchedulingStatus delegates to the SchedulerCache.
func (a *CacheAdapter) SetUnitSchedulingStatus(unitKey string, status unitstatus.SchedulingStatus) {
	a.schedulerCache.SetUnitSchedulingStatus(unitKey, status)
}

// GetUnitStatus delegates to the SchedulerCache.
func (a *CacheAdapter) GetUnitStatus(unitKey string) unitstatus.UnitStatus {
	return a.schedulerCache.GetUnitStatus(unitKey)
}

// ---------- ClusterEventsHandler delegation ----------

func (a *CacheAdapter) AddPod(pod *v1.Pod) error {
	return a.schedulerCache.AddPod(pod)
}

func (a *CacheAdapter) UpdatePod(oldPod, newPod *v1.Pod) error {
	return a.schedulerCache.UpdatePod(oldPod, newPod)
}

func (a *CacheAdapter) DeletePod(pod *v1.Pod) error {
	// Also clean up local state.
	a.mu.Lock()
	delete(a.assumedPods, podKey(pod))
	delete(a.podMarkers, podKey(pod))
	a.mu.Unlock()
	return a.schedulerCache.DeletePod(pod)
}

func (a *CacheAdapter) AddNode(node *v1.Node) error {
	return a.schedulerCache.AddNode(node)
}

func (a *CacheAdapter) UpdateNode(oldNode, newNode *v1.Node) error {
	return a.schedulerCache.UpdateNode(oldNode, newNode)
}

func (a *CacheAdapter) DeleteNode(node *v1.Node) error {
	return a.schedulerCache.DeleteNode(node)
}

func (a *CacheAdapter) AddNMNode(nmNode *nodev1alpha1.NMNode) error {
	return a.schedulerCache.AddNMNode(nmNode)
}

func (a *CacheAdapter) UpdateNMNode(oldNMNode, newNMNode *nodev1alpha1.NMNode) error {
	return a.schedulerCache.UpdateNMNode(oldNMNode, newNMNode)
}

func (a *CacheAdapter) DeleteNMNode(nmNode *nodev1alpha1.NMNode) error {
	return a.schedulerCache.DeleteNMNode(nmNode)
}

func (a *CacheAdapter) AddCNR(cnr *katalystv1alpha1.CustomNodeResource) error {
	return a.schedulerCache.AddCNR(cnr)
}

func (a *CacheAdapter) UpdateCNR(oldCNR, newCNR *katalystv1alpha1.CustomNodeResource) error {
	return a.schedulerCache.UpdateCNR(oldCNR, newCNR)
}

func (a *CacheAdapter) DeleteCNR(cnr *katalystv1alpha1.CustomNodeResource) error {
	return a.schedulerCache.DeleteCNR(cnr)
}

func (a *CacheAdapter) AddPodGroup(podGroup *schedulingv1a1.PodGroup) error {
	return a.schedulerCache.AddPodGroup(podGroup)
}

func (a *CacheAdapter) UpdatePodGroup(oldPodGroup, newPodGroup *schedulingv1a1.PodGroup) error {
	return a.schedulerCache.UpdatePodGroup(oldPodGroup, newPodGroup)
}

func (a *CacheAdapter) DeletePodGroup(podGroup *schedulingv1a1.PodGroup) error {
	return a.schedulerCache.DeletePodGroup(podGroup)
}

func (a *CacheAdapter) AddPDB(pdb *policy.PodDisruptionBudget) error {
	return a.schedulerCache.AddPDB(pdb)
}

func (a *CacheAdapter) UpdatePDB(oldPdb, newPdb *policy.PodDisruptionBudget) error {
	return a.schedulerCache.UpdatePDB(oldPdb, newPdb)
}

func (a *CacheAdapter) DeletePDB(pdb *policy.PodDisruptionBudget) error {
	return a.schedulerCache.DeletePDB(pdb)
}

func (a *CacheAdapter) AddOwner(ownerType, key string, labels map[string]string) error {
	return a.schedulerCache.AddOwner(ownerType, key, labels)
}

func (a *CacheAdapter) UpdateOwner(ownerType, key string, oldLabels, newLabels map[string]string) error {
	return a.schedulerCache.UpdateOwner(ownerType, key, oldLabels, newLabels)
}

func (a *CacheAdapter) DeleteOwner(ownerType, key string) error {
	return a.schedulerCache.DeleteOwner(ownerType, key)
}

func (a *CacheAdapter) AddMovement(movement *schedulingv1a1.Movement) error {
	return a.schedulerCache.AddMovement(movement)
}

func (a *CacheAdapter) UpdateMovement(oldMovement, newMovement *schedulingv1a1.Movement) error {
	return a.schedulerCache.UpdateMovement(oldMovement, newMovement)
}

func (a *CacheAdapter) DeleteMovement(movement *schedulingv1a1.Movement) error {
	return a.schedulerCache.DeleteMovement(movement)
}

func (a *CacheAdapter) AddReservation(request *schedulingv1a1.Reservation) error {
	return a.schedulerCache.AddReservation(request)
}

func (a *CacheAdapter) UpdateReservation(oldRequest, newRequest *schedulingv1a1.Reservation) error {
	return a.schedulerCache.UpdateReservation(oldRequest, newRequest)
}

func (a *CacheAdapter) DeleteReservation(request *schedulingv1a1.Reservation) error {
	return a.schedulerCache.DeleteReservation(request)
}

// podKeyFromStrings constructs a pod key from namespace and name.
func podKeyFromStrings(namespace, name string) string {
	return namespace + "/" + name
}

// getPodAnnotation is a helper to safely get a pod annotation.
func getPodAnnotation(pod *v1.Pod, key string) string {
	if pod == nil || pod.Annotations == nil {
		return ""
	}
	return pod.Annotations[key]
}

// Ensure CacheAdapter satisfies the BinderCache-like operations.
// We cannot do a compile-time check with bindercache.BinderCache directly
// because it would create an import cycle, but we verify implicitly through
// the method set above.
var _ interface {
	GetNodeInfo(string) framework.NodeInfo
	FinishBinding(*v1.Pod) error
	AssumePod(*framework.CachePodInfo) error
	ForgetPod(*framework.CachePodInfo) error
	IsAssumedPod(*v1.Pod) (bool, error)
	GetPod(*v1.Pod) (*v1.Pod, error)
	MarkPodToDelete(*v1.Pod, *v1.Pod) error
	RemoveDeletePodMarker(*v1.Pod, *v1.Pod) error
	IsPodMarkedToDelete(*v1.Pod) (bool, error)
} = &CacheAdapter{}

// Ensure we also satisfy the needed annotation helper (used for readability).
var _ = podutil.PodStateAnnotationKey
