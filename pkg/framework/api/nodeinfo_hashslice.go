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

var GlobalNodeInfoPlaceHolder = NewNodeInfo()

// NodeHashSlice maintains a linear NodeInfo's slice. The time complexity of all methods is O(1).
type NodeHashSlice interface {
	Add(NodeInfo) bool
	Del(NodeInfo) bool
	Has(string) bool
	Nodes() []NodeInfo
	Len() int
}

// NodeHashSliceImpl holds a mapping of string keys to slice index, and slice to NodeInfo.
// When adding a NodeInfo, we will place it directly at the end of the slice.
// When deleting a NodeInfo, we swap it with the last NodeInfo and delete the last NodeInfo.
type NodeHashSliceImpl struct {
	hash  map[string]int
	items []NodeInfo
	count int
}

var _ NodeHashSlice = &NodeHashSliceImpl{}

func NewNodeHashSlice() NodeHashSlice {
	return &NodeHashSliceImpl{
		hash:  make(map[string]int),
		items: make([]NodeInfo, 0),
		count: 0,
	}
}

func (hs *NodeHashSliceImpl) Add(n NodeInfo) bool {
	key := n.GetNodeName()
	if p, ok := hs.hash[key]; !ok {
		hs.items = append(hs.items, n)
		hs.hash[key] = hs.count
		hs.count++
		return true
	} else {
		hs.items[p] = n
		return false
	}
}

func (hs *NodeHashSliceImpl) Del(n NodeInfo) bool {
	key := n.GetNodeName()
	if px, ok := hs.hash[key]; ok {
		hs.count--
		y := hs.items[hs.count]
		hs.items[px], hs.hash[y.GetNodeName()] = y, px
		delete(hs.hash, key)
		hs.items = hs.items[:hs.count]
		return true
	}
	return false
}

func (hs *NodeHashSliceImpl) Has(key string) bool {
	_, ok := hs.hash[key]
	return ok
}

func (hs *NodeHashSliceImpl) Nodes() []NodeInfo {
	return hs.items
}

func (hs *NodeHashSliceImpl) Len() int {
	return hs.count
}

// NodeSlices is mainly used to maintain all nodeInfos in the cluster.
type NodeSlices struct {
	InPartitionNodeSlice                      NodeHashSlice
	OutOfPartitionNodeSlice                   NodeHashSlice
	HavePodsWithAffinityNodeSlice             NodeHashSlice
	HavePodsWithRequiredAntiAffinityNodeSlice NodeHashSlice
}

func NewNodeSlices() *NodeSlices {
	return &NodeSlices{
		InPartitionNodeSlice:                      NewNodeHashSlice(),
		OutOfPartitionNodeSlice:                   NewNodeHashSlice(),
		HavePodsWithAffinityNodeSlice:             NewNodeHashSlice(),
		HavePodsWithRequiredAntiAffinityNodeSlice: NewNodeHashSlice(),
	}
}

func op(slice NodeHashSlice, n NodeInfo, isAdd bool) {
	if isAdd {
		_ = slice.Add(n)
	} else {
		_ = slice.Del(n)
	}
}

func (s *NodeSlices) Update(n NodeInfo, isAdd bool) {
	// ATTENTION: We should ensure that the `globalNodeInfoPlaceHolder` will not be added to nodelice.
	if n == GlobalNodeInfoPlaceHolder {
		return
	}

	if n.GetNodeInSchedulerPartition() || n.GetNMNodeInSchedulerPartition() {
		op(s.InPartitionNodeSlice, n, isAdd)
	} else {
		op(s.OutOfPartitionNodeSlice, n, isAdd)
	}
	if len(n.GetPodsWithAffinity()) > 0 {
		op(s.HavePodsWithAffinityNodeSlice, n, isAdd)
	}
	if len(n.GetPodsWithRequiredAntiAffinity()) > 0 {
		op(s.HavePodsWithRequiredAntiAffinityNodeSlice, n, isAdd)
	}
}
