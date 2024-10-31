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
	"fmt"

	commoncache "github.com/kubewharf/godel-scheduler/pkg/common/cache"
	commonstore "github.com/kubewharf/godel-scheduler/pkg/common/store"
	framework "github.com/kubewharf/godel-scheduler/pkg/framework/api"
	"github.com/kubewharf/godel-scheduler/pkg/scheduler/cache/commonstores"
	nodestore "github.com/kubewharf/godel-scheduler/pkg/scheduler/cache/commonstores/node_store"
)

// Snapshot is a snapshot of s NodeInfo and NodeTree order. The scheduler takes a
// snapshot at the beginning of each scheduling cycle and uses it for its operations in that cycle.
//
// Note: Snapshot operations are lock-free. Our premise for removing lock: even if read operations
// are concurrent, write operations(AssumePod/ForgetPod/AddOneVictim) should always be serial.
type Snapshot struct {
	commonstore.CommonStoresSwitch

	handler commoncache.CacheHandler

	nodeSlices *framework.NodeSlices
}

var _ framework.SharedLister = &Snapshot{}

// NewEmptySnapshot initializes a Snapshot struct and returns it.
func NewEmptySnapshot(handler commoncache.CacheHandler) *Snapshot {
	nodeSlices := framework.NewNodeSlices()

	s := &Snapshot{
		CommonStoresSwitch: commonstore.MakeStoreSwitch(handler, commonstore.Snapshot, commonstores.GlobalRegistries, orderedStoreNames),

		handler: handler,

		nodeSlices: nodeSlices,
	}
	nodeStore := s.CommonStoresSwitch.Find(nodestore.Name)
	nodeStore.(*nodestore.NodeStore).AfterAdd = func(n framework.NodeInfo) { nodeSlices.Update(n, true) }
	nodeStore.(*nodestore.NodeStore).AfterDelete = func(n framework.NodeInfo) { nodeSlices.Update(n, false) }

	handler.SetNodeHandler(nodeStore.(*nodestore.NodeStore).GetNodeInfo)
	handler.SetPodOpFunc(podOpFunc(s.CommonStoresSwitch))

	return s
}

func (s *Snapshot) MakeBasicNodeGroup() framework.NodeGroup {
	nodeCircle := framework.NewNodeCircle(framework.DefaultNodeCircleName, s)
	nodeGroup := framework.NewNodeGroup(framework.DefaultNodeGroupName, s, []framework.NodeCircle{nodeCircle})
	nodeGroup.SetPreferredNodes(framework.NewPreferredNodes())
	return nodeGroup
}

// GetNodeInfo returns a NodeInfo according to the nodeName.
//
// Note: Snapshot operations are lock-free. Our premise for removing lock: even if read operations
// are concurrent, write operations(AssumePod/ForgetPod/AddOneVictim) should always be serial.
func (s *Snapshot) GetNodeInfo(nodeName string) framework.NodeInfo {
	return s.CommonStoresSwitch.Find(nodestore.Name).(*nodestore.NodeStore).Get(nodeName)
}

// NodeInfos returns a NodeInfoLister.
// Note: Snapshot operations are lock-free. Our premise for removing lock: even if read operations
// are concurrent, write operations(AssumePod/ForgetPod/AddOneVictim) should always be serial.
func (s *Snapshot) NodeInfos() framework.NodeInfoLister {
	return s
}

// NumNodes returns the number of nodes in the snapshot.
//
// Note: Snapshot operations are lock-free. Our premise for removing lock: even if read operations
// are concurrent, write operations(AssumePod/ForgetPod/AddOneVictim) should always be serial.
func (s *Snapshot) NumNodes() int {
	return s.nodeSlices.InPartitionNodeSlice.Len() + s.nodeSlices.OutOfPartitionNodeSlice.Len()
}

// List returns the list of nodes in the snapshot.
//
// Note: Snapshot operations are lock-free. Our premise for removing lock: even if read operations
// are concurrent, write operations(AssumePod/ForgetPod/AddOneVictim) should always be serial.
func (s *Snapshot) List() []framework.NodeInfo {
	return append(s.nodeSlices.InPartitionNodeSlice.Nodes(), s.nodeSlices.OutOfPartitionNodeSlice.Nodes()...)
}

// InPartitionList returns the list of nodes which are in the partition of the scheduler
// Note: Snapshot operations are lock-free. Our premise for removing lock: even if read operations
// are concurrent, write operations(AssumePod/ForgetPod/AddOneVictim) should always be serial.
func (s *Snapshot) InPartitionList() []framework.NodeInfo {
	return s.nodeSlices.InPartitionNodeSlice.Nodes()
}

// OutOfPartitionList returns the list of nodes which are out of the partition of the scheduler
//
// Note: Snapshot operations are lock-free. Our premise for removing lock: even if read operations
// are concurrent, write operations(AssumePod/ForgetPod/AddOneVictim) should always be serial.
func (s *Snapshot) OutOfPartitionList() []framework.NodeInfo {
	return s.nodeSlices.OutOfPartitionNodeSlice.Nodes()
}

// HavePodsWithAffinityList returns the list of nodes with at least one pod with inter-pod affinity
//
// Note: Snapshot operations are lock-free. Our premise for removing lock: even if read operations
// are concurrent, write operations(AssumePod/ForgetPod/AddOneVictim) should always be serial.
func (s *Snapshot) HavePodsWithAffinityList() []framework.NodeInfo {
	return s.nodeSlices.HavePodsWithAffinityNodeSlice.Nodes()
}

// HavePodsWithRequiredAntiAffinityList returns the list of nodes with at least one pod with
// required inter-pod anti-affinity
//
// Note: Snapshot operations are lock-free. Our premise for removing lock: even if read operations
// are concurrent, write operations(AssumePod/ForgetPod/AddOneVictim) should always be serial.
func (s *Snapshot) HavePodsWithRequiredAntiAffinityList() []framework.NodeInfo {
	return s.nodeSlices.HavePodsWithRequiredAntiAffinityNodeSlice.Nodes()
}

func (s *Snapshot) Len() int {
	return len(s.nodeSlices.InPartitionNodeSlice.Nodes()) + len(s.nodeSlices.OutOfPartitionNodeSlice.Nodes())
}

// Get returns the NodeInfo of the given node name.
//
// Note: Snapshot operations are lock-free. Our premise for removing lock: even if read operations
// are concurrent, write operations(AssumePod/ForgetPod/AddOneVictim) should always be serial.
func (s *Snapshot) Get(nodeName string) (framework.NodeInfo, error) {
	if nodeInfo := s.CommonStoresSwitch.Find(nodestore.Name).(*nodestore.NodeStore).Get(nodeName); nodeInfo != nil {
		if nodeInfo.GetNode() != nil || nodeInfo.GetNMNode() != nil {
			return nodeInfo, nil
		}
	}
	return nil, fmt.Errorf("nodeinfo not found for node name %q", nodeName)
}

// AssumePod add pod and remove victims in snapshot.
//
// Note: Snapshot operations are lock-free. Our premise for removing lock: even if read operations
// are concurrent, write operations(AssumePod/ForgetPod/AddOneVictim) should always be serial.
func (s *Snapshot) AssumePod(podInfo *framework.CachePodInfo) error {
	return s.CommonStoresSwitch.Range(func(cs commonstore.Store) error { return cs.AssumePod(podInfo) })
}

// ForgetPod remove pod and add-back victims in snapshot.
//
// Note: Snapshot operations are lock-free. Our premise for removing lock: even if read operations
// are concurrent, write operations(AssumePod/ForgetPod/AddOneVictim) should always be serial.
func (s *Snapshot) ForgetPod(podInfo *framework.CachePodInfo) error {
	return s.CommonStoresSwitch.Range(func(cs commonstore.Store) error { return cs.ForgetPod(podInfo) })
}

func (s *Snapshot) FindStore(storeName commonstore.StoreName) commonstore.Store {
	return s.CommonStoresSwitch.Find(storeName)
}
