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

import (
	"context"
	"fmt"
	"strings"

	"github.com/pkg/errors"
	v1 "k8s.io/api/core/v1"
)

const (
	DefaultNodeCircleName string = ""
	DefaultNodeGroupName  string = ""
)

type NodeCircle interface {
	ClusterNodeInfoLister
	GetKey() string
	Validate() error
}

type NodeCircleList []NodeCircle

type PreferNodeExtension interface {
	Plugin
	PrePreferNode(context.Context, *CycleState, *CycleState, *v1.Pod, NodeInfo) (NodeInfo, *CycleState, *Status)
	PostPreferNode(context.Context, *CycleState, *CycleState, *v1.Pod, NodeInfo, *Status) *Status // TODO: revisit this, should we pass in Status instead of `fit` bool?
}

type PreferNodeExtensionList []PreferNodeExtension

func (l PreferNodeExtensionList) PrePreferNode(ctx context.Context, unitCycleState *CycleState, podCycleState *CycleState, pod *v1.Pod, nodeInfo NodeInfo) (NodeInfo, *CycleState, *Status) {
	if l == nil {
		return nodeInfo, podCycleState, nil
	}
	preferStatus := &Status{}
	for _, locatingHook := range l {
		nodeInfo, podCycleState, preferStatus = locatingHook.PrePreferNode(ctx, unitCycleState, podCycleState, pod, nodeInfo)
		if !preferStatus.IsSuccess() {
			break
		}
	}
	return nodeInfo, podCycleState, preferStatus
}

func (l PreferNodeExtensionList) PostPreferNode(ctx context.Context, unitCycleState *CycleState, podCycleState *CycleState, pod *v1.Pod, nodeInfo NodeInfo, status *Status) *Status {
	if l == nil {
		return nil
	}
	preferStatus := &Status{}
	for _, locatingHook := range l {
		preferStatus = locatingHook.PostPreferNode(ctx, unitCycleState, podCycleState, pod, nodeInfo, status)
		if !preferStatus.IsSuccess() {
			break
		}
	}
	return preferStatus
}

type PreferredNodes interface {
	Add(NodeInfo, ...PreferNodeExtension)
	Get(nodeName string) PreferNodeExtensionList
	List() []NodeInfo
}

type NodeGroup interface {
	GetKey() string
	Validate() error
	ClusterNodeInfoGetter

	GetNodeCircles() NodeCircleList
	SetNodeCircles(NodeCircleList)

	GetPreferredNodes() PreferredNodes
	SetPreferredNodes(PreferredNodes)
}

// ------------------------------------------------------------------------------------------

type NodeCircleImpl struct {
	key string
	ClusterNodeInfoLister
}

var _ NodeCircle = &NodeCircleImpl{}

func NewNodeCircle(key string, lister ClusterNodeInfoLister) NodeCircle {
	if lister == nil {
		lister = NewClusterNodeInfoLister()
	}
	return &NodeCircleImpl{key: key, ClusterNodeInfoLister: lister}
}

func (nc *NodeCircleImpl) GetKey() string {
	return GenerateReadableKey(nc.key)
}

func (nc *NodeCircleImpl) Validate() error {
	if nc.ClusterNodeInfoLister == nil {
		return fmt.Errorf("lister is nil")
	}
	nodes := nc.ClusterNodeInfoLister.List()
	if len(nodes) == 0 {
		return fmt.Errorf("no nodes in this node circle")
	}
	return nil
}

// ------------------------------------------------------------------------------------------

type PreferredNodesImpl struct {
	NodeInfoHooks map[string]PreferNodeExtensionList
	NodeHashSlice NodeHashSlice
}

var _ PreferredNodes = &PreferredNodesImpl{}

func NewPreferredNodes() PreferredNodes {
	return &PreferredNodesImpl{
		NodeInfoHooks: make(map[string]PreferNodeExtensionList),
		NodeHashSlice: NewNodeHashSlice(),
	}
}

func (i *PreferredNodesImpl) Add(nodeInfo NodeInfo, hooks ...PreferNodeExtension) {
	i.NodeInfoHooks[nodeInfo.GetNodeName()] = append(i.NodeInfoHooks[nodeInfo.GetNodeName()], hooks...)
	i.NodeHashSlice.Add(nodeInfo)
}

func (i *PreferredNodesImpl) Get(nodeName string) PreferNodeExtensionList {
	if hooks, ok := i.NodeInfoHooks[nodeName]; ok {
		return hooks
	}
	return nil
}

func (i *PreferredNodesImpl) List() []NodeInfo {
	return i.NodeHashSlice.Nodes()
}

// ------------------------------------------------------------------------------------------

var _ NodeGroup = &NodeGroupImpl{}

type NodeGroupImpl struct {
	Key string
	ClusterNodeInfoGetter
	NodeCircles    []NodeCircle
	PreferredNodes PreferredNodes
}

// NewNodeGroup creates and returns a new NodeGroup. A non-nil `getter` is passed usually when the
// node set is large but unchanged, for example, when we get a basic NodeGroup from the snapshot.
func NewNodeGroup(key string, getter ClusterNodeInfoGetter, nodeCircles []NodeCircle) NodeGroup {
	if getter == nil {
		getterImpl := &NodeInfoGetterImpl{NodeInfoMap: make(map[string]NodeInfo)}
		for _, nc := range nodeCircles {
			for _, nodeInfo := range nc.List() {
				getterImpl.NodeInfoMap[nodeInfo.GetNodeName()] = nodeInfo
			}
		}
		getter = getterImpl
	}
	return &NodeGroupImpl{
		Key:                   key,
		ClusterNodeInfoGetter: getter,
		NodeCircles:           nodeCircles,
		PreferredNodes:        NewPreferredNodes(),
	}
}

func (ng *NodeGroupImpl) GetKey() string {
	return GenerateReadableKey(ng.Key)
}

func (ng *NodeGroupImpl) Validate() error {
	if ng.ClusterNodeInfoGetter == nil {
		return fmt.Errorf("getter is nil")
	}

	var hasAvailableNodes bool
	if preferredNodes := ng.PreferredNodes; preferredNodes != nil && len(preferredNodes.List()) > 0 {
		hasAvailableNodes = true
	}

	for _, nodeCircle := range ng.GetNodeCircles() {
		if err := nodeCircle.Validate(); err != nil {
			return errors.Wrap(err, fmt.Sprintf("failed to validate node circle: %v", nodeCircle.GetKey()))
		}
		hasAvailableNodes = true
	}

	if !hasAvailableNodes {
		return fmt.Errorf("failed to validate node group: %v because no avaliable nodes", ng.Key)
	}

	return nil
}

func (ng *NodeGroupImpl) GetNodeCircles() NodeCircleList {
	return ng.NodeCircles
}

func (ng *NodeGroupImpl) SetNodeCircles(nodeCircleList NodeCircleList) {
	ng.NodeCircles = nodeCircleList
}

func (ng *NodeGroupImpl) GetPreferredNodes() PreferredNodes {
	return ng.PreferredNodes
}

func (ng *NodeGroupImpl) SetPreferredNodes(preferredNodes PreferredNodes) {
	ng.PreferredNodes = preferredNodes
}

// ------------------------------------------------------------------------------------------

type NodeInfoGetterImpl struct {
	NodeInfoMap map[string]NodeInfo
}

func (g *NodeInfoGetterImpl) Get(nodeName string) (NodeInfo, error) {
	if nodeInfo, ok := g.NodeInfoMap[nodeName]; ok && nodeInfo != nil {
		if nodeInfo.GetNode() != nil || nodeInfo.GetNMNode() != nil {
			return nodeInfo, nil
		}
	}
	return nil, fmt.Errorf("nodeinfo not found for node name %q", nodeName)
}

// ------------------------------------------------------------------------------------------

// NodeInfoListerImpl implements ClusterNodeInfoLister interface.
type NodeInfoListerImpl struct {
	// InPartitionNodes is the list of nodes in the partition of the scheduler.
	InPartitionNodes []NodeInfo
	// OutOfPartitionNodes is the list of nodes out of the partition of the scheduler.
	OutOfPartitionNodes []NodeInfo
	// HavePodsWithAffinityNodes is the list of nodes with at least one pod declaring affinity terms.
	HavePodsWithAffinityNodes []NodeInfo
	// HavePodsWithRequiredAntiAffinityNodes is the list of nodes with at least one pod declaring
	// required anti-affinity terms.
	HavePodsWithRequiredAntiAffinityNodes []NodeInfo
}

var _ ClusterNodeInfoLister = &NodeInfoListerImpl{}

// NewClusterNodeInfoLister creates a new NodeInfoLister object.
func NewClusterNodeInfoLister() ClusterNodeInfoLister {
	return &NodeInfoListerImpl{}
}

func (i *NodeInfoListerImpl) List() []NodeInfo {
	if i == nil {
		return nil
	}
	return append(i.InPartitionNodes, i.OutOfPartitionNodes...)
}

func (i *NodeInfoListerImpl) InPartitionList() []NodeInfo {
	if i == nil {
		return nil
	}
	return i.InPartitionNodes
}

func (i *NodeInfoListerImpl) OutOfPartitionList() []NodeInfo {
	if i == nil {
		return nil
	}
	return i.OutOfPartitionNodes
}

// HavePodsWithAffinityList returns the list of NodeInfos of nodes with pods with affinity terms.
func (i *NodeInfoListerImpl) HavePodsWithAffinityList() []NodeInfo {
	return i.HavePodsWithAffinityNodes
}

// HavePodsWithRequiredAntiAffinityList returns the list of NodeInfos of nodes with pods with required anti-affinity terms.
func (i *NodeInfoListerImpl) HavePodsWithRequiredAntiAffinityList() []NodeInfo {
	return i.HavePodsWithRequiredAntiAffinityNodes
}

func (i *NodeInfoListerImpl) Len() int {
	if i == nil {
		return 0
	}
	return len(i.InPartitionNodes) + len(i.OutOfPartitionNodes)
}

func (i *NodeInfoListerImpl) AddNodeInfo(nodeInfo NodeInfo) {
	if nodeInfo.GetNodeInSchedulerPartition() || nodeInfo.GetNMNodeInSchedulerPartition() {
		i.InPartitionNodes = append(i.InPartitionNodes, nodeInfo)
	} else {
		i.OutOfPartitionNodes = append(i.OutOfPartitionNodes, nodeInfo)
	}

	if len(nodeInfo.GetPodsWithAffinity()) > 0 {
		i.HavePodsWithAffinityNodes = append(i.HavePodsWithAffinityNodes, nodeInfo)
	}
	if len(nodeInfo.GetPodsWithRequiredAntiAffinity()) > 0 {
		i.HavePodsWithRequiredAntiAffinityNodes = append(i.HavePodsWithRequiredAntiAffinityNodes, nodeInfo)
	}
}

// ------------------------------------------------------------------------------------------

func FilterNodeInfoLister(lister ClusterNodeInfoLister, filterFunc func(NodeInfo) bool) ClusterNodeInfoLister {
	nodes := lister.List()
	ret := &NodeInfoListerImpl{}
	for _, node := range nodes {
		if !filterFunc(node) {
			continue
		}
		ret.AddNodeInfo(node)
	}
	return ret
}

func FilterPreferredNodes(preferredNodes PreferredNodes, filterFunc func(NodeInfo) bool) PreferredNodes {
	nodeInfos := preferredNodes.List()
	ret := NewPreferredNodes()
	for _, nodeInfo := range nodeInfos {
		if !filterFunc(nodeInfo) {
			continue
		}
		ret.Add(nodeInfo, preferredNodes.Get(nodeInfo.GetNodeName())...)
	}
	return ret
}

func FilterNodeGroup(nodeGroup NodeGroup, filterFunc func(NodeInfo) bool) NodeGroup {
	nodeCircles := nodeGroup.GetNodeCircles()
	newNodeCircles := make([]NodeCircle, 0, len(nodeCircles))
	for _, nodeCircle := range nodeCircles {
		newNodeCircle := NewNodeCircle(
			nodeCircle.GetKey(),
			FilterNodeInfoLister(nodeCircle, func(nodeInfo NodeInfo) bool {
				return filterFunc(nodeInfo)
			}),
		)
		if len(newNodeCircle.List()) > 0 {
			newNodeCircles = append(newNodeCircles, newNodeCircle)
		}
	}
	newNodeGroup := NewNodeGroup(nodeGroup.GetKey(), nil, newNodeCircles)

	preferredNodes := nodeGroup.GetPreferredNodes()
	if preferredNodes != nil {
		newNodeGroup.SetPreferredNodes(FilterPreferredNodes(preferredNodes, filterFunc))
	}

	return newNodeGroup
}

// ------------------------------------------------------------------------------------------

func UnitRequireJobLevelAffinity(unit ScheduleUnit) bool {
	if unit == nil {
		return false
	}
	if affinity, err := unit.GetRequiredAffinity(); len(affinity) > 0 && err == nil {
		return true
	}
	if affinity, err := unit.GetPreferredAffinity(); len(affinity) > 0 && err == nil {
		return true
	}
	return false
}

func GenerateReadableKey(name string) string {
	if strings.Contains(name, "[") || strings.Contains(name, "]") {
		return name
	}
	return "[" + name + "]"
}
