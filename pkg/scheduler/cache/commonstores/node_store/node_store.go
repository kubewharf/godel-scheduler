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

package nodestore

import (
	"fmt"

	nodev1alpha1 "github.com/kubewharf/godel-scheduler-api/pkg/apis/node/v1alpha1"
	katalystv1alpha1 "github.com/kubewharf/katalyst-api/pkg/apis/node/v1alpha1"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/sets"
	utilfeature "k8s.io/apiserver/pkg/util/feature"
	"k8s.io/klog/v2"

	commoncache "github.com/kubewharf/godel-scheduler/pkg/common/cache"
	commonstore "github.com/kubewharf/godel-scheduler/pkg/common/store"
	"github.com/kubewharf/godel-scheduler/pkg/features"
	framework "github.com/kubewharf/godel-scheduler/pkg/framework/api"
	"github.com/kubewharf/godel-scheduler/pkg/framework/utils"
	"github.com/kubewharf/godel-scheduler/pkg/scheduler/cache/commonstores"
	utilfeatures "github.com/kubewharf/godel-scheduler/pkg/util/features"
	"github.com/kubewharf/godel-scheduler/pkg/util/generationstore"
	nodeutil "github.com/kubewharf/godel-scheduler/pkg/util/node"
	podutil "github.com/kubewharf/godel-scheduler/pkg/util/pod"
)

const Name commonstore.StoreName = "NodeStore"

func (c *NodeStore) Name() commonstore.StoreName {
	return Name
}

func init() {
	commonstores.GlobalRegistries.Register(
		Name,
		func(h commoncache.CacheHandler) bool { return true },
		NewCache,
		NewSnapshot)
}

// ---------------------------------------------------------------------------------------

var GlobalNodeInfoPlaceHolder = framework.NewNodeInfo()

func nodeInfoBelongToSubCluster(n framework.NodeInfo, matchedSubCluster string) bool {
	return matchedSubCluster == framework.DefaultSubCluster ||
		n.GetNode() != nil && n.GetNode().Labels[framework.GetGlobalSubClusterKey()] == matchedSubCluster ||
		n.GetNMNode() != nil && n.GetNMNode().Labels[framework.GetGlobalSubClusterKey()] == matchedSubCluster
}

// The NodeStore under this file will be held by Cache and Snapshot respectively.
// The difference is that:
//	- The Store used in the Cache is generationstore.ListStore which will be organized in the form of linked list and hash table.
//	- The Store used in the Snapshot is generationstore.RawStore which will be organized by a hash table.
// For more information, please see "godel/pkg/util/generationstore"

// -------------------------------------- NodeStore --------------------------------------
type NodeStore struct {
	commonstore.BaseStore
	storeType commonstore.StoreType
	handler   commoncache.CacheHandler

	Store generationstore.Store // Holds all nodes including those that have been Deleted but still have residual pods.
	// `Deleted` holds all the nodes:
	// 1. that have been deleted but still have residual pods.
	// 2. that its pod comes before its own, so we can't use it to schedule.
	Deleted     sets.String
	AfterAdd    func(framework.NodeInfo) // Triggered by a call to the NodeStore.Add function, used to maintain additional information about the node.
	AfterDelete func(framework.NodeInfo) // Triggered by a call to the NodeStore.Delete function, used to maintain additional information about the node.

	// A map from image name to its imageState.
	// ATTENTION: Like `Deleted` field, it will only be modified and used in the Cache.
	imageStates map[string]*imageState
}

var _ commonstore.Store = &NodeStore{}

func NewCache(handler commoncache.CacheHandler) commonstore.Store {
	return &NodeStore{
		BaseStore: commonstore.NewBaseStore(),
		storeType: commonstore.Cache,
		handler:   handler,

		Store:       generationstore.NewListStore(),
		Deleted:     sets.NewString(),
		imageStates: make(map[string]*imageState),
	}
}

func NewSnapshot(handler commoncache.CacheHandler) commonstore.Store {
	return &NodeStore{
		BaseStore: commonstore.NewBaseStore(),
		storeType: commonstore.Snapshot,
		handler:   handler,

		Store:       generationstore.NewRawStore(),
		Deleted:     sets.NewString(),
		imageStates: make(map[string]*imageState), // This will not be used in Snapshot.
	}
}

func (s *NodeStore) AddNode(node *v1.Node) error {
	var nodeInfo framework.NodeInfo
	if obj := s.Get(node.GetName()); obj != nil {
		nodeInfo = obj
		s.removeNodeImageStates(nodeInfo.GetNode())
		s.Deleted.Delete(node.GetName())
		s.Delete(node.GetName(), nodeInfo)
	} else {
		nodeInfo = framework.NewNodeInfo()
	}
	s.addNodeImageStates(node, nodeInfo)
	nodeInfo.SetNodeInSchedulerPartition(nodeutil.NodeOfThisScheduler(node.Annotations, s.handler.ComponentName()))
	err := nodeInfo.SetNode(node)
	s.Add(node.GetName(), nodeInfo)
	return err
}

func (s *NodeStore) AddNMNode(nmNode *nodev1alpha1.NMNode) error {
	var nodeInfo framework.NodeInfo
	if obj := s.Get(nmNode.GetName()); obj != nil {
		nodeInfo = obj
		s.Deleted.Delete(nmNode.GetName())
		s.Delete(nmNode.GetName(), nodeInfo)
	} else {
		nodeInfo = framework.NewNodeInfo()
	}
	nodeInfo.SetNMNodeInSchedulerPartition(nodeutil.NodeOfThisScheduler(nmNode.Annotations, s.handler.ComponentName()))
	err := nodeInfo.SetNMNode(nmNode)
	s.Add(nmNode.GetName(), nodeInfo)
	return err
}

func (s *NodeStore) AddCNR(cnr *katalystv1alpha1.CustomNodeResource) error {
	var nodeInfo framework.NodeInfo
	if obj := s.Get(cnr.Name); obj != nil {
		nodeInfo = obj
	} else {
		nodeInfo = framework.NewNodeInfo()
	}
	err := nodeInfo.SetCNR(cnr)
	s.Set(cnr.Name, nodeInfo)
	return err
}

func (s *NodeStore) UpdateNode(oldNode, newNode *v1.Node) error {
	var nodeInfo framework.NodeInfo
	if obj := s.Get(newNode.GetName()); obj != nil {
		nodeInfo = obj
		s.removeNodeImageStates(nodeInfo.GetNode())
		s.Deleted.Delete(newNode.GetName())
		s.Delete(newNode.GetName(), nodeInfo)
	} else {
		nodeInfo = framework.NewNodeInfo()
	}
	s.addNodeImageStates(newNode, nodeInfo)
	nodeInfo.SetNodeInSchedulerPartition(nodeutil.NodeOfThisScheduler(newNode.Annotations, s.handler.ComponentName()))
	err := nodeInfo.SetNode(newNode)
	s.Add(newNode.GetName(), nodeInfo)
	return err
}

func (s *NodeStore) UpdateNMNode(oldNMNode, newNMNode *nodev1alpha1.NMNode) error {
	var nodeInfo framework.NodeInfo
	if obj := s.Get(newNMNode.GetName()); obj != nil {
		nodeInfo = obj
		s.Deleted.Delete(newNMNode.GetName())
		s.Delete(newNMNode.GetName(), nodeInfo)
	} else {
		nodeInfo = framework.NewNodeInfo()
	}
	nodeInfo.SetNMNodeInSchedulerPartition(nodeutil.NodeOfThisScheduler(newNMNode.Annotations, s.handler.ComponentName()))
	err := nodeInfo.SetNMNode(newNMNode)
	s.Add(newNMNode.GetName(), nodeInfo)
	return err
}

func (s *NodeStore) UpdateCNR(oldCNR, newCNR *katalystv1alpha1.CustomNodeResource) error {
	var nodeInfo framework.NodeInfo
	if obj := s.Get(newCNR.Name); obj != nil {
		nodeInfo = obj
	} else {
		nodeInfo = framework.NewNodeInfo()
	}
	err := nodeInfo.SetCNR(newCNR)
	s.Set(newCNR.Name, nodeInfo)
	return err
}

func (s *NodeStore) DeleteNode(node *v1.Node) error {
	var nodeInfo framework.NodeInfo
	if obj := s.Get(node.GetName()); obj != nil {
		nodeInfo = obj
	} else {
		return fmt.Errorf("node %v is not found", node.GetName())
	}
	s.Delete(node.GetName(), nodeInfo)
	nodeInfo.RemoveNode()
	s.removeNodeImageStates(node)

	if nodeInfo.GetNMNode() != nil {
		s.Add(node.GetName(), nodeInfo)
	} else if nodeInfo.NumPods() != 0 || nodeInfo.GetCNR() != nil {
		// The node should be deleted but still have residual pods, so store the node in nodeStore without trigger afterAdd function.
		s.Deleted.Insert(node.GetName())
		s.Set(node.GetName(), nodeInfo)
	}
	return nil
}

func (s *NodeStore) DeleteNMNode(nmNode *nodev1alpha1.NMNode) error {
	var nodeInfo framework.NodeInfo
	if obj := s.Get(nmNode.GetName()); obj != nil {
		nodeInfo = obj
	} else {
		return fmt.Errorf("node %v is not found", nmNode.GetName())
	}
	s.Delete(nmNode.GetName(), nodeInfo)
	nodeInfo.RemoveNMNode()

	if nodeInfo.GetNode() != nil {
		s.Add(nmNode.GetName(), nodeInfo)
	} else if nodeInfo.NumPods() != 0 || nodeInfo.GetCNR() != nil {
		// The node should be deleted but still have residual pods, so store the node in nodeStore without trigger afterAdd function.
		s.Deleted.Insert(nmNode.GetName())
		s.Set(nmNode.GetName(), nodeInfo)
	}
	return nil
}

// DeleteCNR removes custom node resource.
// The node might be still in the node tree because their deletion events didn't arrive yet.
func (s *NodeStore) DeleteCNR(cnr *katalystv1alpha1.CustomNodeResource) error {
	var nodeInfo framework.NodeInfo
	if obj := s.Get(cnr.Name); obj != nil {
		nodeInfo = obj
	} else {
		return fmt.Errorf("node %v is not found", cnr.Name)
	}
	nodeInfo.RemoveCNR()
	if nodeInfo.NumPods() == 0 && nodeInfo.ObjectIsNil() {
		// This node was previously a node with residual pods, and the current pod is the last pod it has left.
		// We will delete this node and remove it from `NodeStore.Deleted`.
		// This can only happen in cache.NodeStore.
		s.Deleted.Delete(cnr.Name)
		s.Store.Delete(cnr.Name)
	} else {
		s.Set(cnr.Name, nodeInfo)
	}
	return nil
}

func (s *NodeStore) AddPod(pod *v1.Pod) error {
	if !podutil.BoundPod(pod) && !podutil.AssumedPodOfGodel(pod, s.handler.SchedulerType()) {
		return nil
	}
	nodeName := utils.GetNodeNameFromPod(pod)
	if nodeName == "" {
		klog.InfoS("WARN: Pod was assigned to empty node", "pod", pod.Name)
		return nil
	}
	nodeInfo := s.getOrCreateNode(nodeName)
	nodeInfo.AddPod(pod)
	return nil
}

func (s *NodeStore) UpdatePod(oldPod, newPod *v1.Pod) error {
	// Remove the oldPod if existed.
	{
		key, err := framework.GetPodKey(oldPod)
		if err != nil {
			return err
		}
		if ps, _ := s.handler.GetPodState(key); ps != nil {
			// Use the pod stored in Cache instead of oldPod.
			if err := s.DeletePod(ps.Pod); err != nil {
				return err
			}
		}
	}
	// Add the newPod if needed.
	{
		if err := s.AddPod(newPod); err != nil {
			return err
		}
	}
	return nil
}

func (s *NodeStore) DeletePod(pod *v1.Pod) error {
	if !podutil.BoundPod(pod) && !podutil.AssumedPodOfGodel(pod, s.handler.SchedulerType()) {
		return nil
	}
	nodeName := utils.GetNodeNameFromPod(pod)
	if nodeName == "" {
		klog.InfoS("WARN: Pod was assigned to empty node", "pod", pod.Name)
		return nil
	}
	nodeInfo := s.Get(nodeName)
	if nodeInfo == nil {
		klog.InfoS("WARN: Node not found when trying to remove pod", "node", nodeName, "pod", pod.Name)
		return nil
	}
	if err := nodeInfo.RemovePod(pod, false); err != nil {
		return err
	}
	if nodeInfo.NumPods() == 0 && nodeInfo.ObjectIsNil() {
		// This node was previously a node with residual pods, and the current pod is the last pod it has left.
		// We will delete this node and remove it from `NodeStore.Deleted`.
		// This can only happen in cache.NodeStore.
		s.Deleted.Delete(nodeName)
		s.Store.Delete(nodeName)
	} else {
		s.Set(nodeName, nodeInfo)
	}
	return nil
}

func (s *NodeStore) AssumePod(podInfo *framework.CachePodInfo) error {
	nodeName := utils.GetNodeNameFromPod(podInfo.Pod)
	if nodeName == "" {
		klog.InfoS("WARN: Pod was assigned to empty node", "pod", podInfo.Pod.Name)
		return nil
	}
	nodeInfo := s.getOrCreateNode(nodeName)
	if err := AssignMicroTopology(nodeInfo, podInfo.Pod); err != nil {
		return err
	}
	nodeInfo.AddPod(podInfo.Pod)

	if s.storeType == commonstore.Snapshot {
		if podInfo.Victims != nil && len(podInfo.Victims.Pods) > 0 {
			for _, victim := range podInfo.Victims.Pods {
				if err := nodeInfo.RemovePod(victim, true); err != nil {
					klog.InfoS("Failed to remove victim in node", "err", err, "victim", podutil.GeneratePodKey(victim), "node", nodeName)
				}
			}
		}
	}
	return nil
}

func (s *NodeStore) ForgetPod(podInfo *framework.CachePodInfo) error {
	nodeName := utils.GetNodeNameFromPod(podInfo.Pod)
	if nodeName == "" {
		klog.InfoS("WARN: Pod was assigned to empty node", "pod", podInfo.Pod.Name)
		return nil
	}
	nodeInfo := s.Get(nodeName)
	if nodeInfo == nil {
		klog.InfoS("WARN: Node not found when trying to remove pod", "node", nodeName, "pod", podInfo.Pod.Name)
		return nil
	}
	if err := nodeInfo.RemovePod(podInfo.Pod, false); err != nil {
		return err
	}
	if s.storeType == commonstore.Snapshot {
		if podInfo.Victims != nil && len(podInfo.Victims.Pods) > 0 {
			for _, victim := range podInfo.Victims.Pods {
				nodeInfo.AddPod(victim)
			}
		}
	}
	if nodeInfo.NumPods() == 0 && nodeInfo.ObjectIsNil() {
		// This node was previously a node with residual pods, and the current pod is the last pod it has left.
		// We will delete this node and remove it from `NodeStore.Deleted`.
		// This can only happen in cache.NodeStore.
		s.Deleted.Delete(nodeName)
		s.Store.Delete(nodeName)
	} else {
		s.Set(nodeName, nodeInfo)
	}
	return nil
}

func (cacheStore *NodeStore) UpdateSnapshot(store commonstore.Store) error {
	snapshotStore := store.(*NodeStore)
	cache, snapshot := framework.TransferGenerationStore(cacheStore.Store, snapshotStore.Store)

	balancedVolumesEnabled := utilfeature.DefaultFeatureGate.Enabled(utilfeatures.BalanceAttachedNodeVolumes)
	subClusterConcurrentSchedulingEnabled := utilfeature.DefaultFeatureGate.Enabled(features.SchedulerSubClusterConcurrentScheduling)

	subCluster := snapshotStore.handler.SubCluster()

	cache.UpdateRawStore(
		snapshot,
		func(key string, obj generationstore.StoredObj) {
			nodeInfo := obj.(framework.NodeInfo)
			if balancedVolumesEnabled && nodeInfo.GetTransientInfo() != nil {
				// Transient scheduler info is reset here.
				nodeInfo.GetTransientInfo().ResetTransientSchedulerInfo()
			}
			nodeName := key
			if nodeName != "" {
				if obj := snapshot.Get(nodeName); obj != nil {
					// Use `snapshotStore.Delete` instead of `snapshot.Delete` to maintain nodeSlices.
					snapshotStore.Delete(nodeName, obj.(framework.NodeInfo))
				}

				if subClusterConcurrentSchedulingEnabled && !nodeInfoBelongToSubCluster(nodeInfo, subCluster) {
					// ATTENTION: We should ensure that the `globalNodeInfoPlaceHolder` will not be added to nodeslice.
					snapshotStore.Add(nodeName, GlobalNodeInfoPlaceHolder)
				} else {
					// Use `s.Add` instead of `snapshot.Add` to maintain nodeSlices.
					snapshotStore.Add(nodeName, nodeInfo.Clone())
				}
			}
		},
		func() {
			// Comparing to pods in nodeTree.
			// Deleted nodes get removed from the tree, but they might remain in the nodes map
			// if they still have non-deleted Pods.
			toDelete := snapshot.Len() - (cache.Len() - snapshotStore.Deleted.Len())
			if toDelete > 0 {
				snapshot.ConditionRange(func(key string, existingObj generationstore.StoredObj) bool {
					if toDelete <= 0 {
						return false
					}
					if nodeInfo := cache.Get(key); nodeInfo == nil || nodeInfo.(framework.NodeInfo).ObjectIsNil() {
						// Use `snapshotStore.nodeStore.Delete` instead of `snapshot.Delete` to maintain nodeSlices.
						snapshotStore.Delete(key, existingObj.(framework.NodeInfo))
						toDelete--
					}
					return true
				})
			}
		},
	)
	return nil
}

// -------------------------------------- Other Interface --------------------------------------

func (s *NodeStore) getOrCreateNode(nodeName string) framework.NodeInfo {
	var nodeInfo framework.NodeInfo
	if obj := s.Store.Get(nodeName); obj != nil {
		nodeInfo = obj.(framework.NodeInfo)
	} else {
		nodeInfo = framework.NewNodeInfo()
		// If this is a node that did not exist before, we need to treat it as a Deleted node for now
		// because we cannot use it in the scheduling process.
		s.Deleted.Insert(nodeName)
	}
	// If s.Store implement the generation.ListStore, we will move the nodeInfo to list's head in `Set`,
	// and make the generation plus one.
	s.Store.Set(nodeName, nodeInfo)
	return nodeInfo
}

func (s *NodeStore) SetNodeInPartition(nodeName string) error {
	nodeInfo := s.Get(nodeName)
	if nodeInfo == nil {
		return fmt.Errorf("node %v is not found", nodeName)
	}
	s.Delete(nodeName, nodeInfo)
	err := nodeInfo.SetNodePartition(true)
	s.Add(nodeName, nodeInfo)
	return err
}

func (s *NodeStore) SetNodeOutOfPartition(nodeName string) error {
	nodeInfo := s.Get(nodeName)
	if nodeInfo == nil {
		return fmt.Errorf("node %v is not found", nodeName)
	}
	s.Delete(nodeName, nodeInfo)
	err := nodeInfo.SetNodePartition(false)
	s.Add(nodeName, nodeInfo)
	return err
}

func (s *NodeStore) NodeInThisPartition(nodeName string) bool {
	nodeInfo := s.Get(nodeName)
	if nodeInfo == nil {
		return false
	}
	return nodeInfo.GetNodeInSchedulerPartition() || nodeInfo.GetNMNodeInSchedulerPartition()
}

func (s *NodeStore) GetNodeInfo(nodeName string) framework.NodeInfo {
	if obj := s.Store.Get(nodeName); obj != nil {
		return obj.(framework.NodeInfo)
	}
	return nil
}

func (s *NodeStore) Len() int {
	return s.Store.Len()
}

// Set will get the node without trigger any function.
func (s *NodeStore) Get(nodeName string) framework.NodeInfo {
	if obj := s.Store.Get(nodeName); obj != nil {
		return obj.(framework.NodeInfo)
	}
	return nil
}

// Set will Store the node without trigger any function.
func (s *NodeStore) Set(nodeName string, nodeInfo framework.NodeInfo) {
	s.Store.Set(nodeName, nodeInfo)
}

// Add will Store the node and trigger the AfterAdd.
func (s *NodeStore) Add(nodeName string, nodeInfo framework.NodeInfo) {
	s.Store.Set(nodeName, nodeInfo)
	if s.AfterAdd != nil {
		s.AfterAdd(nodeInfo)
	}
}

// Delete will delete the node and trigger the AfterDelete.
func (s *NodeStore) Delete(nodeName string, nodeInfo framework.NodeInfo) {
	s.Store.Delete(nodeName)
	if s.AfterDelete != nil {
		s.AfterDelete(nodeInfo)
	}
}

// AllNodesClone return all nodes's deepcopy and organize them in map.
func (s *NodeStore) AllNodesClone() map[string]framework.NodeInfo {
	nodes := make(map[string]framework.NodeInfo, s.Store.Len())
	s.Store.Range(func(k string, v generationstore.StoredObj) {
		nodeInfo := v.(framework.NodeInfo)
		nodes[k] = nodeInfo.Clone()
	})
	return nodes
}

// --------

type imageState struct {
	// Size of the image
	size int64
	// A set of node names for nodes having this image present
	nodes sets.String
}

// createImageStateSummary returns a summarizing snapshot of the given image's state.
func createImageStateSummary(state *imageState) *framework.ImageStateSummary {
	return &framework.ImageStateSummary{
		Size:     state.size,
		NumNodes: len(state.nodes),
	}
}

// addNodeImageStates adds states of the images on given node to the given nodeInfo and update the imageStates in
// scheduler cache. This function assumes the lock to scheduler cache has been acquired.
func (s *NodeStore) addNodeImageStates(node *v1.Node, nodeInfo framework.NodeInfo) {
	newSum := make(map[string]*framework.ImageStateSummary)

	for _, image := range node.Status.Images {
		for _, name := range image.Names {
			// update the entry in imageStates
			state, ok := s.imageStates[name]
			if !ok {
				state = &imageState{
					size:  image.SizeBytes,
					nodes: sets.NewString(node.GetName()),
				}
				s.imageStates[name] = state
			} else {
				state.nodes.Insert(node.GetName())
			}
			// create the imageStateSummary for this image
			if _, ok := newSum[name]; !ok {
				newSum[name] = createImageStateSummary(state)
			}
		}
	}
	nodeInfo.SetImageStates(newSum)
}

// removeNodeImageStates removes the given node record from image entries having the node
// in imageStates cache. After the removal, if any image becomes free, i.e., the image
// is no longer available on any node, the image entry will be removed from imageStates.
func (s *NodeStore) removeNodeImageStates(node *v1.Node) {
	if node == nil {
		return
	}

	for _, image := range node.Status.Images {
		for _, name := range image.Names {
			state, ok := s.imageStates[name]
			if ok {
				state.nodes.Delete(node.GetName())
				if len(state.nodes) == 0 {
					// Remove the unused image to make sure the length of
					// imageStates represents the total number of different
					// images on all nodes
					delete(s.imageStates, name)
				}
			}
		}
	}
}
