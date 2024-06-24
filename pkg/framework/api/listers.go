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

package api

// NodeInfoLister interface represents anything that can list/get NodeInfo objects from node name.
type NodeInfoLister interface {
	ClusterNodeInfoLister
	// InPartitionList Returns the list of NodeInfos in the partition of the scheduler
	InPartitionList() []NodeInfo
	// OutOfPartitionList Returns the list of NodeInfos out of the partition of the scheduler
	OutOfPartitionList() []NodeInfo
}

// SharedLister groups scheduler-specific listers.
type SharedLister interface {
	NodeInfos() NodeInfoLister
}

// ClusterNodeInfoLister interface represents anything that can list/get NodeInfo objects from node name.
type ClusterNodeInfoLister interface {
	// List Returns the list of NodeInfos.
	List() []NodeInfo
	// HavePodsWithAffinityList Returns the list of NodeInfos of nodes with pods with affinity terms.
	HavePodsWithAffinityList() []NodeInfo
	// HavePodsWithRequiredAntiAffinityList Returns the list of NodeInfos of nodes with pods with required anti-affinity terms.
	HavePodsWithRequiredAntiAffinityList() []NodeInfo
	// Get Returns the NodeInfo of the given node name.
	Get(nodeName string) (NodeInfo, error)
}

// ClusterSharedLister groups scheduler-specific listers.
type ClusterSharedLister interface {
	NodeInfos() ClusterNodeInfoLister
}
