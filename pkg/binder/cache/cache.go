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

package cache

import (
	"errors"
	"fmt"
	"sync"
	"time"

	nodev1alpha1 "github.com/kubewharf/godel-scheduler-api/pkg/apis/node/v1alpha1"
	schedulingv1a1 "github.com/kubewharf/godel-scheduler-api/pkg/apis/scheduling/v1alpha1"
	katalystv1alpha1 "github.com/kubewharf/katalyst-api/pkg/apis/node/v1alpha1"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/klog/v2"

	"github.com/kubewharf/godel-scheduler/pkg/binder/metrics"
	binderutils "github.com/kubewharf/godel-scheduler/pkg/binder/utils"
	commoncache "github.com/kubewharf/godel-scheduler/pkg/common/cache"
	"github.com/kubewharf/godel-scheduler/pkg/framework/api"
	framework "github.com/kubewharf/godel-scheduler/pkg/framework/api"
	"github.com/kubewharf/godel-scheduler/pkg/framework/utils"
	podutil "github.com/kubewharf/godel-scheduler/pkg/util/pod"
	unitutil "github.com/kubewharf/godel-scheduler/pkg/util/unit"
)

var (
	cleanAssumedPeriod = 10 * time.Second
	// Time period to wait for pod in assumed state when waiting for
	// victim pods to be preempted.
	AssumedPodExpiration           = 900 * time.Second
	reservationTimingWheelInterval = 1 * time.Second
	PodMarkerExpirationPeriod      = 600 * time.Second
)

// New returns a BinderCache implementation.
// It automatically starts a go routine that manages expiration of assumed pods.
// "ttl" is how long the assumed pod will get expired.
// "stop" is the channel that would close the background goroutine.
// "schedulerName" identifies the scheduler
func New(ttl time.Duration, stop <-chan struct{}, binderName string) BinderCache {
	cache := newBinderCache(ttl, cleanAssumedPeriod, stop, binderName)
	cache.run()
	return cache
}

type binderCache struct {
	binderName string

	stop <-chan struct{}
	// ttl period is used to assumed pods after the binding is invoked
	ttl    time.Duration
	period time.Duration

	// This mutex guards all fields within this cache struct.
	mu sync.RWMutex
	// a set of assumed pod keys.
	// The key could further be used to get an entry in podStates.
	assumedPods map[string]bool
	// a set of pods that marked to be deleted.
	podsMarkedToBeDeleted map[string]bool

	// a map from pod key to podState.
	podStates map[string]*podState
	// nodeInfoMap a map of node name to a snapshot of its NodeInfo.
	nodeInfoMap map[string]framework.NodeInfo

	// A map from image name to its imageState.
	imageStates map[string]*imageState

	// podGroupPodMap tracks the pods for that pod group
	podGroupPodMap map[string]map[string]*v1.Pod

	// podGroupInfo is the cache of PodGroup object which is used for gang scheduling
	podGroupInfo map[string]*schedulingv1a1.PodGroup

	unitStatus *binderutils.UnitStatusMap

	pdbItems map[string]*pdbItem
}

type podState struct {
	pod *v1.Pod
	// Used by assumedPod to determinate expiration.
	// TODO: make this work with podGroup expiration
	deadline *time.Time
	// Used to mark the pod which will be removed from the cache eventually
	markedToBeDeleted bool
	// timeout time for pod deletion marker.
	markerExpirationTime *time.Time
	preemptorKey         string
}

type imageState struct {
	// Size of the image
	size int64
	// A set of node names for nodes having this image present
	nodes sets.String
}

type reservationUnit struct {
	// original podKey.
	PlaceholderPod *v1.Pod
	CreateTime     time.Time
	IndexInTimer   int
	MatchedPod     *v1.Pod
}

// createImageStateSummary returns a summarizing snapshot of the given image's state.
func (cache *binderCache) createImageStateSummary(state *imageState) *framework.ImageStateSummary {
	return &framework.ImageStateSummary{
		Size:     state.size,
		NumNodes: len(state.nodes),
	}
}

func newBinderCache(ttl, period time.Duration, stop <-chan struct{}, binderName string) *binderCache {
	bc := &binderCache{
		binderName: binderName,
		ttl:        ttl,
		period:     period,
		stop:       stop,

		assumedPods:           make(map[string]bool),
		podsMarkedToBeDeleted: make(map[string]bool),
		podStates:             make(map[string]*podState),
		nodeInfoMap:           make(map[string]framework.NodeInfo),
		imageStates:           make(map[string]*imageState),
		podGroupPodMap:        make(map[string]map[string]*v1.Pod),
		podGroupInfo:          make(map[string]*schedulingv1a1.PodGroup),
		unitStatus:            binderutils.NewUnitStatusMap(),
		pdbItems:              make(map[string]*pdbItem),
	}
	return bc
}

// PodCount returns the number of pods in the cache (including those from deleted nodes).
// DO NOT use outside of tests.
func (cache *binderCache) PodCount() (int, error) {
	cache.mu.RLock()
	defer cache.mu.RUnlock()
	count := 0
	for _, n := range cache.nodeInfoMap {
		count += n.NumPods()
	}
	return count, nil
}

func (cache *binderCache) GetPodGroupPods(podGroupName string) []*v1.Pod {
	cache.mu.RLock()
	defer cache.mu.RUnlock()

	var pods []*v1.Pod
	for _, pod := range cache.podGroupPodMap[podGroupName] {
		pods = append(pods, pod)
	}

	return pods
}

func (cache *binderCache) GetPodGroupInfo(podGroupName string) (*schedulingv1a1.PodGroup, error) {
	cache.mu.RLock()
	defer cache.mu.RUnlock()

	pg, found := cache.podGroupInfo[podGroupName]
	if !found {
		return nil, errors.New("not found")
	}
	return pg, nil
}

func (cache *binderCache) AssumePod(pod *v1.Pod) error {
	key, err := framework.GetPodKey(pod)
	if err != nil {
		return err
	}

	cache.mu.Lock()
	defer cache.mu.Unlock()
	if _, ok := cache.podStates[key]; ok {
		return fmt.Errorf("pod %v is in the cache, so can't be assumed", key)
	}

	cache.addPod(pod)
	dl := time.Now().Add(AssumedPodExpiration)
	ps := &podState{
		pod:      pod,
		deadline: &dl,
	}
	cache.podStates[key] = ps
	cache.assumedPods[key] = true

	return nil
}

func (cache *binderCache) FinishBinding(pod *v1.Pod) error {
	return cache.finishBinding(pod, time.Now())
}

// finishBinding exists to make tests deterministic by injecting now as an argument
func (cache *binderCache) finishBinding(pod *v1.Pod, now time.Time) error {
	key, err := framework.GetPodKey(pod)
	if err != nil {
		return err
	}

	cache.mu.RLock()
	defer cache.mu.RUnlock()

	klog.V(5).InfoS("Finished binding for pod. Can be expired", "podUID", key, "pod", klog.KObj(pod))
	currState, ok := cache.podStates[key]
	if ok && cache.assumedPods[key] {
		dl := now.Add(cache.ttl)
		currState.deadline = &dl
	}
	return nil
}

func (cache *binderCache) ForgetPod(pod *v1.Pod) error {
	key, err := framework.GetPodKey(pod)
	if err != nil {
		return err
	}

	cache.mu.Lock()
	defer cache.mu.Unlock()

	currState, ok := cache.podStates[key]
	if ok && currState.pod.Spec.NodeName != pod.Spec.NodeName {
		return fmt.Errorf("pod %v was assumed on %v but assigned to %v", key, pod.Spec.NodeName, currState.pod.Spec.NodeName)
	}

	switch {
	// Only assumed pod can be forgotten.
	case ok && cache.assumedPods[key]:
		if err := cache.expirePod(key, currState); err != nil {
			return err
		}
	default:
		return fmt.Errorf("pod %v wasn't assumed so cannot be forgotten", key)
	}
	return nil
}

// Assumes that lock is already acquired.
func (cache *binderCache) addPod(pod *v1.Pod) {
	nodeName := utils.GetNodeNameFromPod(pod)
	if nodeName == "" {
		klog.InfoS("Pod was assigned to empty node", "pod", klog.KObj(pod))
		return
	}
	n, ok := cache.nodeInfoMap[nodeName]
	if !ok {
		n = framework.NewNodeInfo()
		cache.nodeInfoMap[nodeName] = n
	}
	cache.addPodGroupInfo(pod)
	n.AddPod(pod)
}

// Assumes that lock is already acquired.
func (cache *binderCache) updatePod(oldPod, newPod *v1.Pod) error {
	if err := cache.removePod(oldPod); err != nil {
		return err
	}
	cache.addPod(newPod)
	return nil
}

// Assumes that lock is already acquired.
// Removes a pod from the cached node info. If the node information was already
// removed and there are no more pods left in the node, cleans up the node from
// the cache.
func (cache *binderCache) removePod(pod *v1.Pod) error {
	nodeName := utils.GetNodeNameFromPod(pod)
	if nodeName == "" {
		klog.InfoS("Pod was assigned to empty node", "pod", klog.KObj(pod))
		return nil
	}
	n, ok := cache.nodeInfoMap[nodeName]
	if !ok {
		klog.InfoS("Node was not found when trying to remove pod", "nodeName", nodeName, "pod", klog.KObj(pod))
		return nil
	}
	if err := n.RemovePod(pod, false); err != nil {
		return err
	}

	cache.removePodGroupInfo(pod)

	if n.NumPods() == 0 && n.ObjectIsNil() && n.GetCNR() == nil {
		delete(cache.nodeInfoMap, nodeName)
	}
	return nil
}

func (cache *binderCache) AddPod(pod *v1.Pod) error {
	cache.mu.Lock()
	defer cache.mu.Unlock()

	if podutil.BoundPod(pod) {
		cache.addPodToCache(pod)
	}

	return nil
}

func (cache *binderCache) addPodToCache(pod *v1.Pod) error {
	key, err := framework.GetPodKey(pod)
	if err != nil {
		return err
	}

	currState, ok := cache.podStates[key]
	switch {
	case ok && cache.assumedPods[key]:
		if err = cache.updatePod(currState.pod, pod); err != nil {
			klog.InfoS("Error occurred while updating pod", "err", err, "pod", klog.KObj(pod))
		}
		if currState.pod.Spec.NodeName != pod.Spec.NodeName {
			// The pod was added to a different node than it was assumed to.
			klog.InfoS("WARN: Pod was assumed to be on the assumed node, but got added to the current node", "podUID", key, "pod", klog.KObj(pod), "assumedNode", pod.Spec.NodeName, "currentNode", currState.pod.Spec.NodeName)
		}
		delete(cache.assumedPods, key)
		cache.podStates[key].deadline = nil
		cache.podStates[key].pod = pod
	case !ok:
		// Pod was expired. We should add it back.
		cache.addPod(pod)
		ps := &podState{
			pod: pod,
		}
		cache.podStates[key] = ps
	default:
		return fmt.Errorf("pod %v was already in added state", key)
	}
	return nil
}

func (cache *binderCache) UpdatePod(oldPod, newPod *v1.Pod) error {
	cache.mu.Lock()
	defer cache.mu.Unlock()

	podutil.FilteringUpdate(podutil.BoundPod, cache.addPodToCache, cache.updatePodInCache, cache.deletePodFromCache, oldPod, newPod)

	return nil
}

func (cache *binderCache) updatePodInCache(oldPod, newPod *v1.Pod) error {
	// A Pod delete event followed by an immediate Pod add event may be merged
	// into a Pod update event. In this case, we should invalidate the old Pod, and
	// then add the new Pod.
	if oldPod.UID != newPod.UID {
		cache.deletePodFromCache(oldPod)
		cache.addPodToCache(newPod)
		return nil
	}

	key, err := framework.GetPodKey(oldPod)
	if err != nil {
		return err
	}

	currState, ok := cache.podStates[key]
	switch {
	// An assumed pod won't have Update/Remove event. It needs to have Add event
	// before Update event, in which case the state would change from Assumed to Added.
	case ok && !cache.assumedPods[key]:
		// Because pod in BinderCache with Spec.NodeName is not empty for binder, so the empty works for scheduler only.
		// This is tricky, it's better to make implementation of UpdatePod for scheduler and binder separately to make the logic more clear.
		if currState.pod.Spec.NodeName != "" && currState.pod.Spec.NodeName != newPod.Spec.NodeName {
			klog.InfoS("Pod updated on a different node than previously added to", "podUID", key)
			klog.ErrorS(nil, "BinderCache was corrupted and can badly affect scheduling decisions")
			klog.FlushAndExit(klog.ExitFlushTimeout, 1)
		}
		if err := cache.updatePod(oldPod, newPod); err != nil {
			return err
		}
		currState.pod = newPod
	default:
		return fmt.Errorf("pod %v is not added to scheduler cache, so cannot be updated", key)
	}
	return nil
}

func (cache *binderCache) RemovePod(pod *v1.Pod) error {
	cache.mu.Lock()
	defer cache.mu.Unlock()

	if podutil.BoundPod(pod) {
		cache.deletePodFromCache(pod)
	}
	return nil
}

func (cache *binderCache) deletePodFromCache(pod *v1.Pod) error {
	key, err := framework.GetPodKey(pod)
	if err != nil {
		return err
	}

	currState, ok := cache.podStates[key]
	switch {
	// An assumed pod won't have Delete/Remove event. It needs to have Add event
	// before Remove event, in which case the state would change from Assumed to Added.
	case ok && !cache.assumedPods[key]:
		if currState.pod.Spec.NodeName != pod.Spec.NodeName {
			klog.InfoS("Pod was assumed to be on the assumed node but got added to the current node", "podUID", key, "pod", klog.KObj(pod), "assumedNode", pod.Spec.NodeName, "currentNode", currState.pod.Spec.NodeName)
			klog.ErrorS(nil, "BinderCache was corrupted and can badly affect scheduling decisions")
			klog.FlushAndExit(klog.ExitFlushTimeout, 1)
		}
		err := cache.removePod(currState.pod)
		if err != nil {
			return err
		}
		delete(cache.podStates, key)
		delete(cache.podsMarkedToBeDeleted, key)
	default:
		return fmt.Errorf("pod %v is not found in scheduler cache, so cannot be removed from it", key)
	}
	return nil
}

func (cache *binderCache) IsAssumedPod(pod *v1.Pod) (bool, error) {
	key, err := framework.GetPodKey(pod)
	if err != nil {
		return false, err
	}

	cache.mu.RLock()
	defer cache.mu.RUnlock()

	b, found := cache.assumedPods[key]
	if !found {
		return false, nil
	}
	return b, nil
}

// GetPod might return a pod for which its node has already been deleted from
// the main cache. This is useful to properly process pod update events.
func (cache *binderCache) GetPod(pod *v1.Pod) (*v1.Pod, error) {
	key, err := framework.GetPodKey(pod)
	if err != nil {
		return nil, err
	}

	cache.mu.RLock()
	defer cache.mu.RUnlock()

	podState, ok := cache.podStates[key]
	if !ok {
		return nil, fmt.Errorf("pod %v does not exist in binder cache", key)
	}

	return podState.pod, nil
}

func (cache *binderCache) MarkPodToDelete(pod, preemptor *v1.Pod) error {
	key, err := framework.GetPodKey(pod)
	if err != nil {
		return err
	}
	pKey, err := framework.GetPodKey(preemptor)
	if err != nil {
		return err
	}

	cache.mu.Lock()
	defer cache.mu.Unlock()

	currState, ok := cache.podStates[key]
	switch {
	// An assumed pod won't have Delete/Remove event. It needs to have Add event
	// before Remove event, in which case the state would change from Assumed to Added.
	case ok && !cache.assumedPods[key]:
		if currState.pod.Spec.NodeName != pod.Spec.NodeName {
			klog.InfoS("Pod was assumed to be on the assumed node but got added to the current node", "podUID", key, "pod", klog.KObj(pod), "assumedNode", pod.Spec.NodeName, "currentNode", currState.pod.Spec.NodeName)
			klog.ErrorS(nil, "Binder cache was corrupted and can badly affect scheduling decisions")
			klog.FlushAndExit(klog.ExitFlushTimeout, 1)
		}
		currState.markedToBeDeleted = true
		currState.preemptorKey = pKey
		dl := time.Now().Add(PodMarkerExpirationPeriod)
		currState.markerExpirationTime = &dl
		cache.podsMarkedToBeDeleted[key] = true
	default:
		return fmt.Errorf("pod %v is not found in binder cache, so cannot be marked to delete", key)
	}
	return nil
}

func (cache *binderCache) RemoveDeletePodMarker(pod, preemptor *v1.Pod) error {
	key, err := framework.GetPodKey(pod)
	if err != nil {
		return err
	}
	pKey, err := framework.GetPodKey(preemptor)
	if err != nil {
		return err
	}

	cache.mu.Lock()
	defer cache.mu.Unlock()

	currState, ok := cache.podStates[key]
	if !ok {
		return nil
	}
	if currState.preemptorKey != pKey {
		return fmt.Errorf("Pod Marker isn't set by this preemptor %s", pKey)
	}
	currState.markedToBeDeleted = false
	currState.markerExpirationTime = nil
	currState.preemptorKey = ""
	delete(cache.podsMarkedToBeDeleted, key)

	return nil
}

func (cache *binderCache) RemoveDeletePodMarkerByKey(podKey, preemptorKey string) error {
	cache.mu.Lock()
	defer cache.mu.Unlock()

	currState, ok := cache.podStates[podKey]
	if !ok {
		return nil
	}
	if currState.preemptorKey != preemptorKey {
		return fmt.Errorf("Pod Marker isn't set by this preemptor %s", preemptorKey)
	}
	currState.markedToBeDeleted = false
	currState.markerExpirationTime = nil
	currState.preemptorKey = ""
	delete(cache.podsMarkedToBeDeleted, podKey)
	return nil
}

func (cache *binderCache) IsPodMarkedToDelete(pod *v1.Pod) (bool, error) {
	key, err := api.GetPodKey(pod)
	if err != nil {
		return false, err
	}

	cache.mu.RLock()
	defer cache.mu.RUnlock()

	currState, ok := cache.podStates[key]
	if !ok {
		return false, fmt.Errorf("pod %v is not found in binder cache to check is pod is marked to delete", key)
	}

	return currState.markedToBeDeleted, nil
}

func (cache *binderCache) addPodGroupInfo(pod *v1.Pod) {
	pgName := unitutil.GetPodGroupFullName(pod)
	if pgName == "" {
		return
	}

	if _, ok := cache.podGroupPodMap[pgName]; !ok {
		cache.podGroupPodMap[pgName] = make(map[string]*v1.Pod)
	}

	cache.podGroupPodMap[pgName][pod.Name] = pod
}

func (cache *binderCache) removePodGroupInfo(pod *v1.Pod) {
	pgName := unitutil.GetPodGroupFullName(pod)
	if pgName == "" {
		return
	}

	delete(cache.podGroupPodMap[pgName], pod.Name)
}

func (cache *binderCache) AddNode(node *v1.Node) error {
	cache.mu.Lock()
	defer cache.mu.Unlock()

	n, ok := cache.nodeInfoMap[node.Name]
	if !ok {
		n = framework.NewNodeInfo()
		cache.nodeInfoMap[node.Name] = n
	} else {
		cache.removeNodeImageStates(n.GetNode())
	}

	cache.addNodeImageStates(node, n)
	return n.SetNode(node)
}

func (cache *binderCache) AddNMNode(nmNode *nodev1alpha1.NMNode) error {
	cache.mu.Lock()
	defer cache.mu.Unlock()

	n, ok := cache.nodeInfoMap[nmNode.Name]
	if !ok {
		n = framework.NewNodeInfo()
		cache.nodeInfoMap[nmNode.Name] = n
	}

	return n.SetNMNode(nmNode)
}

func (cache *binderCache) AddCNR(cnr *katalystv1alpha1.CustomNodeResource) error {
	cache.mu.Lock()
	defer cache.mu.Unlock()

	n, ok := cache.nodeInfoMap[cnr.Name]
	if !ok {
		n = framework.NewNodeInfo()
		cache.nodeInfoMap[cnr.Name] = n
	}

	return n.SetCNR(cnr)
}

func (cache *binderCache) UpdateNode(oldNode, newNode *v1.Node) error {
	cache.mu.Lock()
	defer cache.mu.Unlock()

	n, ok := cache.nodeInfoMap[newNode.Name]
	if !ok {
		n = framework.NewNodeInfo()
		cache.nodeInfoMap[newNode.Name] = n
	} else {
		cache.removeNodeImageStates(n.GetNode())
	}
	cache.addNodeImageStates(newNode, n)

	return n.SetNode(newNode)
}

func (cache *binderCache) UpdateNMNode(oldNMNode, newNMNode *nodev1alpha1.NMNode) error {
	cache.mu.Lock()
	defer cache.mu.Unlock()

	n, ok := cache.nodeInfoMap[newNMNode.Name]
	if !ok {
		n = framework.NewNodeInfo()
		cache.nodeInfoMap[newNMNode.Name] = n
	}

	return n.SetNMNode(newNMNode)
}

func (cache *binderCache) UpdateCNR(oldCNR, newCNR *katalystv1alpha1.CustomNodeResource) error {
	cache.mu.Lock()
	defer cache.mu.Unlock()

	n, ok := cache.nodeInfoMap[newCNR.Name]
	if !ok {
		n = framework.NewNodeInfo()
		cache.nodeInfoMap[newCNR.Name] = n
	}

	return n.SetCNR(newCNR)
}

// RemoveNode removes a node from the cache's tree.
// The node might still have pods because their deletion events didn't arrive
// yet. Those pods are considered removed from the cache, being the node tree
// the source of truth.
// However, we keep a ghost node with the list of pods until all pod deletion
// events have arrived. A ghost node is skipped from snapshots.
func (cache *binderCache) RemoveNode(node *v1.Node) error {
	cache.mu.Lock()
	defer cache.mu.Unlock()

	n, ok := cache.nodeInfoMap[node.Name]
	if !ok {
		return fmt.Errorf("node %v is not found", node.Name)
	}

	n.RemoveNode()
	cache.removeNodeImageStates(node)

	// don't remove NodeInfo if NMNode exists
	if n.GetNMNode() != nil {
		return nil
	}

	if n.NumPods() == 0 && n.ObjectIsNil() && n.GetCNR() == nil {
		delete(cache.nodeInfoMap, node.Name)
	}
	return nil
}

func (cache *binderCache) RemoveNMNode(nmNode *nodev1alpha1.NMNode) error {
	cache.mu.Lock()
	defer cache.mu.Unlock()

	n, ok := cache.nodeInfoMap[nmNode.Name]
	if !ok {
		return fmt.Errorf("nmNode %v is not found", nmNode.Name)
	}
	n.RemoveNMNode()

	// don't remove NodeInfo if Node exists
	if n.GetNode() != nil {
		return nil
	}

	if n.NumPods() == 0 && n.ObjectIsNil() && n.GetCNR() == nil {
		delete(cache.nodeInfoMap, nmNode.Name)
	}
	return nil
}

// RemoveCNR removes custom node resource.
// The node might be still in the node tree because their deletion events didn't arrive yet.
func (cache *binderCache) RemoveCNR(cnr *katalystv1alpha1.CustomNodeResource) error {
	cache.mu.Lock()
	defer cache.mu.Unlock()

	n, ok := cache.nodeInfoMap[cnr.Name]
	if !ok {
		return fmt.Errorf("custom node %v is not found", cnr.Name)
	}
	n.RemoveCNR()

	if n.NumPods() == 0 && n.ObjectIsNil() && n.GetCNR() == nil {
		delete(cache.nodeInfoMap, cnr.Name)
	}
	return nil
}

// Dump is a dump of the bindCache state
func (cache *binderCache) Dump() *commoncache.Dump {
	cache.mu.RLock()
	defer cache.mu.RUnlock()

	assumedPods := make(map[string]bool, len(cache.assumedPods))
	for k, v := range cache.assumedPods {
		assumedPods[k] = v
	}

	return &commoncache.Dump{
		Nodes:       cache.nodeInfoMap,
		AssumedPods: assumedPods,
	}
}

func (cache *binderCache) GetNode(nodename string) (framework.NodeInfo, error) {
	cache.mu.RLock()
	defer cache.mu.RUnlock()

	nInfo, ok := cache.nodeInfoMap[nodename]
	if !ok {
		return nil, fmt.Errorf("node %s does not exist in cache", nodename)
	}

	return nInfo, nil
}

// AddPodGroup add pod group object into binder cache
func (cache *binderCache) AddPodGroup(podGroup *schedulingv1a1.PodGroup) error {
	cache.mu.Lock()
	defer cache.mu.Unlock()
	return cache.addPodGroup(podGroup)
}

// addPodGroup is private method to add pod group cache. This function assumes the lock to cache has been acquired.
func (cache *binderCache) addPodGroup(podGroup *schedulingv1a1.PodGroup) error {
	key := unitutil.GetPodGroupKey(podGroup)
	cache.podGroupInfo[key] = podGroup
	return nil
}

// UpdatePodGroup update exiting pod group cache
func (cache *binderCache) UpdatePodGroup(oldPodGroup, newPodGroup *schedulingv1a1.PodGroup) error {
	cache.mu.Lock()
	defer cache.mu.Unlock()

	if err := cache.removePodGroup(oldPodGroup); err != nil {
		return err
	}
	if err := cache.addPodGroup(newPodGroup); err != nil {
		return err
	}
	return nil
}

// RemovePodGroup remove pod group in binder cache
func (cache *binderCache) RemovePodGroup(podGroup *schedulingv1a1.PodGroup) error {
	cache.mu.Lock()
	defer cache.mu.Unlock()
	return cache.removePodGroup(podGroup)
}

// removePodGroup is private method to remove pod group cache. This function assumes the lock to cache has been acquired.
func (cache *binderCache) removePodGroup(podGroup *schedulingv1a1.PodGroup) error {
	key := unitutil.GetPodGroupKey(podGroup)
	if _, ok := cache.podGroupInfo[key]; ok {
		delete(cache.podGroupInfo, key)
	}
	return nil
}

// addNodeImageStates adds states of the images on given node to the given nodeInfo and update the imageStates in
// scheduler cache. This function assumes the lock to scheduler cache has been acquired.
func (cache *binderCache) addNodeImageStates(node *v1.Node, nodeInfo framework.NodeInfo) {
	newSum := make(map[string]*framework.ImageStateSummary)

	for _, image := range node.Status.Images {
		for _, name := range image.Names {
			// update the entry in imageStates
			state, ok := cache.imageStates[name]
			if !ok {
				state = &imageState{
					size:  image.SizeBytes,
					nodes: sets.NewString(node.Name),
				}
				cache.imageStates[name] = state
			} else {
				state.nodes.Insert(node.Name)
			}
			// create the imageStateSummary for this image
			if _, ok := newSum[name]; !ok {
				newSum[name] = cache.createImageStateSummary(state)
			}
		}
	}
	nodeInfo.SetImageStates(newSum)
}

// removeNodeImageStates removes the given node record from image entries having the node
// in imageStates cache. After the removal, if any image becomes free, i.e., the image
// is no longer available on any node, the image entry will be removed from imageStates.
func (cache *binderCache) removeNodeImageStates(node *v1.Node) {
	if node == nil {
		return
	}

	for _, image := range node.Status.Images {
		for _, name := range image.Names {
			state, ok := cache.imageStates[name]
			if ok {
				state.nodes.Delete(node.Name)
				if len(state.nodes) == 0 {
					// Remove the unused image to make sure the length of
					// imageStates represents the total number of different
					// images on all nodes
					delete(cache.imageStates, name)
				}
			}
		}
	}
}

func (cache *binderCache) run() {
	go wait.Until(cache.cleanup, cache.period, cache.stop)
}

func (cache *binderCache) cleanup() {
	cache.cleanupExpiredAssumedPods()
	cache.cleanupExpiredPodDeletionMarker()
}

func (cache *binderCache) cleanupExpiredAssumedPods() {
	cache.cleanupAssumedPods(time.Now())
}

func (cache *binderCache) cleanupExpiredPodDeletionMarker() {
	cache.cleanupPodDeletionMarker(time.Now())
}

// cleanupAssumedPods exists for making test deterministic by taking time as input argument.
// It also reports metrics on the cache size for nodes, pods, and assumed pods.
func (cache *binderCache) cleanupAssumedPods(now time.Time) {
	cache.mu.Lock()
	defer cache.mu.Unlock()
	defer cache.updateMetrics()

	// The size of assumedPods should be small
	for key := range cache.assumedPods {
		ps, ok := cache.podStates[key]
		if !ok {
			klog.ErrorS(nil, "WARN: Key found in assumed set but not in podStates. Potentially a logical error")
			klog.FlushAndExit(klog.ExitFlushTimeout, 1)
		}
		if now.After(*ps.deadline) {
			klog.InfoS("Pod expired", "pod", ps.pod)
			if err := cache.expirePod(key, ps); err != nil {
				klog.InfoS("ExpirePod failed", "podState", key, "err", err)
			}
		}
	}
}

func (cache *binderCache) cleanupPodDeletionMarker(now time.Time) {
	cache.mu.Lock()
	defer cache.mu.Unlock()

	for key := range cache.podsMarkedToBeDeleted {
		ps, ok := cache.podStates[key]
		if !ok {
			klog.ErrorS(nil, "Key found in podsMarkedToBeDeleted set but not in podStates. Potentially a logical error.")
			klog.FlushAndExit(klog.ExitFlushTimeout, 1)
		}

		if now.After(*ps.markerExpirationTime) {
			klog.InfoS("WARN: Pod deletion marker expired", "pod", klog.KObj(ps.pod))
			ps.markedToBeDeleted = false
			delete(cache.podsMarkedToBeDeleted, key)
		}
	}
}

func (cache *binderCache) expirePod(key string, ps *podState) error {
	if err := cache.removePod(ps.pod); err != nil {
		return err
	}
	delete(cache.assumedPods, key)
	delete(cache.podStates, key)

	return nil
}

// updateMetrics updates cache size metric values for pods, assumed pods, and nodes
func (cache *binderCache) updateMetrics() {
	metrics.CacheSize.WithLabelValues("assumed_pods").Set(float64(len(cache.assumedPods)))
	metrics.CacheSize.WithLabelValues("pods").Set(float64(len(cache.podStates)))
	metrics.CacheSize.WithLabelValues("nodes").Set(float64(len(cache.nodeInfoMap)))
}

func (cache *binderCache) GetUnitStatus(key string) binderutils.UnitStatus {
	cache.mu.RLock()
	defer cache.mu.RUnlock()
	return cache.unitStatus.GetUnitStatus(key)
}

func (cache *binderCache) SetUnitStatus(key string, status binderutils.UnitStatus) {
	cache.mu.Lock()
	defer cache.mu.Unlock()

	cache.unitStatus.SetUnitStatus(key, status)
}

func (cache *binderCache) DeleteUnitStatus(key string) {
	cache.mu.Lock()
	defer cache.mu.Unlock()

	cache.unitStatus.DeleteUnitStatus(key)
}

func (cache *binderCache) AddOwner(ownerType, key string, labels map[string]string) error {
	return nil
}

func (cache *binderCache) UpdateOwner(ownerType, key string, oldLabels, newLabels map[string]string) error {
	return nil
}

func (cache *binderCache) DeleteOwner(ownerType, key string) error {
	return nil
}
