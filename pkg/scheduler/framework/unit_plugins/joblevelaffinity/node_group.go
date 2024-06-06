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

package joblevelaffinity

import (
	"context"
	"fmt"
	"sort"
	"sync"

	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/klog/v2"

	framework "github.com/kubewharf/godel-scheduler/pkg/framework/api"
	"github.com/kubewharf/godel-scheduler/pkg/util"
	"github.com/kubewharf/godel-scheduler/pkg/util/parallelize"
	podutil "github.com/kubewharf/godel-scheduler/pkg/util/pod"
)

// nodeCircleElem is the node in a node group tree. It includes the nodeGroup, the children nodes in the tree,
// and the bottomNodeGroupsInTree, which will be written while getting node pools from the tree.
type nodeCircleElem struct {
	nodeCircle             framework.NodeCircle
	children               []int
	bottomNodeGroupsInTree []framework.NodeCircle
}

func newNodeCircleElem(nodeCircle framework.NodeCircle) *nodeCircleElem {
	return &nodeCircleElem{nodeCircle: nodeCircle}
}

func getNodeGroupsFromTree(nodeCircleTree []*nodeCircleElem) ([]framework.NodeGroup, error) {
	if len(nodeCircleTree) == 0 {
		return nil, fmt.Errorf("empty node group tree")
	}
	nodeGroups := make([]framework.NodeGroup, len(nodeCircleTree))
	j := 0
	for i := len(nodeCircleTree) - 1; i >= 0; i-- {
		var ngs []framework.NodeCircle
		elem := nodeCircleTree[i]
		if len(elem.children) == 0 {
			if elem.nodeCircle == nil {
				return nil, fmt.Errorf("nil node group in elem %v", i)
			}
			ngs = append(ngs, elem.nodeCircle)
		} else {
			for k := len(elem.children) - 1; k >= 0; k-- {
				child := elem.children[k]
				if child >= len(nodeCircleTree) {
					return nil, fmt.Errorf("unexpected child index %v in elem %v while length of tree is %v", child, i, len(nodeCircleTree))
				}
				ngs = append(ngs, nodeCircleTree[child].bottomNodeGroupsInTree...)
			}
		}
		elem.bottomNodeGroupsInTree = ngs
		np := framework.NewNodeGroup(elem.nodeCircle.GetKey(), nil, ngs)
		nodeGroups[j] = np
		j++
	}

	return nodeGroups, nil
}

// findNodeCirclesByRequireAffinity returns NodeGroupList in order, and the parameter nodeGroup should be sorted
func findNodeCirclesByRequireAffinity(
	ctx context.Context,
	podLauncher podutil.PodLauncher,
	unit framework.ScheduleUnit,
	unitAffinityTerms []framework.UnitAffinityTerm,
	nodeCircle framework.NodeCircle,
	assignedNodes sets.String,
	nodeLister framework.NodeInfoLister,
) ([]*nodeCircleElem, error) {
	requiredAffinityTerms := newNodeGroupAffinityTerms(unitAffinityTerms)

	var requiredNodeCircleList framework.NodeCircleList
	if assignedNodes.Len() != 0 {
		klog.V(4).InfoS("Unit has running pods on nodes", "unitKey", unit.GetKey(), "numNodes", len(assignedNodes))
		// there are running pods in the unit, filter nodes matching the same affinity specs of these running pods
		requiredAffinitySpecs, err := getRequiredAffinitySpecs(ctx, podLauncher, requiredAffinityTerms, assignedNodes, nodeLister)
		if err != nil {
			return nil, err
		}
		requiredNodeCircleList, err = groupNodesByAffinitySpecs(podLauncher, nodeCircle, assignedNodes, requiredAffinitySpecs)
		if err != nil {
			return nil, err
		}
	} else {
		klog.V(4).InfoS("Unit has no running pods", "unitKey", unit.GetKey())
		// no running pods, then nodes should be grouped by affinity terms
		tmpNodeCircles, err := groupNodesByAffinityTerms(ctx, podLauncher, nodeCircle, requiredAffinityTerms)
		if err != nil {
			return nil, err
		}
		requiredNodeCircleList = tmpNodeCircles
	}

	sortRules, sortRulesErr := getSortRules(unit)
	if sortRulesErr == nil {
		klog.V(4).InfoS("Started to sort node circles according to sort rules", "unitKey", unit.GetKey(), "sortRules", sortRules)
		sortNodeCircles(ctx, unit, requiredNodeCircleList, sortRules)
	}

	sz := len(requiredNodeCircleList)
	nodeGroupTree := make([]*nodeCircleElem, sz)
	for i, ng := range requiredNodeCircleList {
		nodeGroupTree[sz-1-i] = newNodeCircleElem(ng)
	}
	return nodeGroupTree, nil
}

func buildNodeGroupsFromCandidates(sourceNodeCircle framework.NodeCircle, candidates *nodeGroupCandidates) (framework.NodeCircleList, error) {
	listers := make(map[string]*framework.NodeInfoListerImpl)

	nodes := sourceNodeCircle.List()

	for _, node := range nodes {
		nodeName := node.GetNodeName()
		if !candidates.isNodeIn(nodeName) {
			continue
		}

		topologyKey := candidates.getNodeTopology(nodeName)
		if _, ok := listers[topologyKey]; !ok {
			listers[topologyKey] = &framework.NodeInfoListerImpl{NodeInfoMap: make(map[string]framework.NodeInfo)}
		}
		listers[topologyKey].AddNodeInfo(node)
	}

	result := make([]framework.NodeCircle, 0)
	for key, lister := range listers {
		result = append(result, framework.NewNodeCircle(key, lister))
	}

	return result, nil
}

// groupNodesByAffinitySpecs return node groups from the original node group according to required affinity term and status, then there should be only one node group
func groupNodesByAffinitySpecs(podLauncher podutil.PodLauncher, nodeCircle framework.NodeCircle, assignedNodes sets.String, affinities *nodeGroupAffinitySpecs) (framework.NodeCircleList, error) {
	nodes := nodeCircle.List()
	nodeGroupKey := affinities.getNodeGroupKey()
	candidates := getPotentialCandidates(podLauncher, nodeGroupKey, assignedNodes, nodes, affinities)
	nodeGroups, err := buildNodeGroupsFromCandidates(nodeCircle, candidates)
	if err != nil {
		return nil, err
	}

	return nodeGroups, nil
}

func groupNodesByAffinityTerms(_ context.Context, podLauncher podutil.PodLauncher, nodeCircle framework.NodeCircle, affinities *nodeGroupAffinityTerms) (framework.NodeCircleList, error) {
	nodes := nodeCircle.List()
	allCandidates := newNodeGroupCandidates()
	for _, node := range nodes {
		labels, matched := getNodeLabelsIfLauncherMatches(podLauncher, node)
		if !matched {
			continue
		}
		specs, err := affinities.getAffinitySpecs(labels)
		if err != nil {
			klog.V(4).InfoS("Skipped the node that can not meet all affinities", "node", klog.KObj(node.GetNode()), "affinities", affinities.String(), "nodeLabels", labels)
			continue
		}
		nodeGroupKey := specs.getNodeGroupKey()
		allCandidates.addNodes(nodeGroupKey, node.GetNodeName())
	}

	nodeCircles, err := buildNodeGroupsFromCandidates(nodeCircle, allCandidates)
	if err != nil {
		return nil, err
	}

	return nodeCircles, nil
}

// sortNodeCircles sorts the nodeGroups according to unit's sort rules.
// If there is no sort rules, we sort by default rule.
func sortNodeCircles(ctx context.Context, unit framework.ScheduleUnit, nodeCircles []framework.NodeCircle, sortRules []framework.SortRule) {
	if len(nodeCircles) <= 1 {
		return
	}

	podInfos := unit.GetPods()
	if len(podInfos) == 0 {
		return
	}
	resourceType, err := podutil.GetPodResourceType(podInfos[0].Pod)
	if err != nil {
		klog.InfoS("Wrong resource type", "pod", klog.KObj(podInfos[0].Pod), "err", err)
		return
	}

	// Check sort rules are valid or not.
	for _, rule := range sortRules {
		if !rule.Valid() {
			klog.InfoS("Invalid sort rule", "unitKey", unit.GetKey())
			return
		}
	}

	// Get nodeGroup allocatable resource by resource type
	var lock sync.Mutex
	allocatableByNodeGroup, requestedByNodeGroup := make(map[string]*framework.Resource), make(map[string]*framework.Resource)
	errCh := parallelize.NewErrorChannel()
	parallelCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	parallelize.Until(parallelCtx, len(nodeCircles), func(index int) {
		allocatable, requested, err := getNodeCircleAllocatableResource(nodeCircles[index], resourceType)
		if err != nil {
			errCh.SendErrorWithCancel(err, cancel)
			klog.InfoS("Failed to get nodeGroup allocatable resource", "nodeGroup", nodeCircles[index].GetKey(), "err", err)
			return
		}
		lock.Lock()
		defer lock.Unlock()
		allocatableByNodeGroup[nodeCircles[index].GetKey()] = allocatable
		requestedByNodeGroup[nodeCircles[index].GetKey()] = requested
	})

	if err := errCh.ReceiveError(); err != nil {
		return
	}

	sort.SliceStable(nodeCircles, func(i, j int) bool {
		k1, k2 := nodeCircles[i].GetKey(), nodeCircles[j].GetKey()
		return compareAccordingToSortRules(allocatableByNodeGroup, requestedByNodeGroup, sortRules, k1, k2)
	})
}

func compareAccordingToSortRules(allocatableByNodeGroup, requestedByNodeGroup map[string]*framework.Resource, sortRules []framework.SortRule, k1, k2 string) bool {
	for _, rule := range sortRules {
		var v1, v2 int64
		if rule.Dimension == framework.Capacity {
			switch rule.Resource {
			case framework.CPUResource:
				v1, v2 = allocatableByNodeGroup[k1].MilliCPU, allocatableByNodeGroup[k2].MilliCPU
			case framework.MemoryResource:
				v1, v2 = allocatableByNodeGroup[k1].Memory, allocatableByNodeGroup[k2].Memory
			case framework.GPUResource:
				v1, v2 = allocatableByNodeGroup[k1].ScalarResources[util.ResourceGPU], allocatableByNodeGroup[k2].ScalarResources[util.ResourceGPU]
			}
		} else if rule.Dimension == framework.Available {
			var cap1, req1, cap2, req2 int64
			switch rule.Resource {
			case framework.CPUResource:
				cap1, req1 = allocatableByNodeGroup[k1].MilliCPU, requestedByNodeGroup[k1].MilliCPU
				cap2, req2 = allocatableByNodeGroup[k2].MilliCPU, requestedByNodeGroup[k2].MilliCPU
			case framework.MemoryResource:
				cap1, req1 = allocatableByNodeGroup[k1].Memory, requestedByNodeGroup[k1].Memory
				cap2, req2 = allocatableByNodeGroup[k2].Memory, requestedByNodeGroup[k2].Memory
			case framework.GPUResource:
				cap1, req1 = allocatableByNodeGroup[k1].ScalarResources[util.ResourceGPU], requestedByNodeGroup[k1].ScalarResources[util.ResourceGPU]
				cap2, req2 = allocatableByNodeGroup[k2].ScalarResources[util.ResourceGPU], requestedByNodeGroup[k2].ScalarResources[util.ResourceGPU]
			}
			v1, v2 = getCapacityRequestDiff(cap1, req1), getCapacityRequestDiff(cap2, req2)
		}

		if v1 == v2 {
			continue
		}
		if rule.Order == framework.AscendingOrder {
			return v1 < v2
		}
		return v1 > v2
	}

	return false
}

func getCapacityRequestDiff(capacity, request int64) int64 {
	if capacity < request { // This situation should not happen.
		return 0
	}
	return capacity - request
}

// getNodeCircleAllocatableResource sum all nodes' guaranteedAllocatable in given nodeGroup.
func getNodeCircleAllocatableResource(nodeCircle framework.NodeCircle, resourceType podutil.PodResourceType) (*framework.Resource, *framework.Resource, error) {
	nodes := nodeCircle.List()
	totalAllocatable, totalRequested := &framework.Resource{}, &framework.Resource{}
	for _, node := range nodes {
		var allocatable, requested *framework.Resource
		switch resourceType {
		case podutil.GuaranteedPod:
			allocatable = node.GetGuaranteedAllocatable()
			requested = node.GetGuaranteedRequested()
		case podutil.BestEffortPod:
			allocatable = node.GetBestEffortAllocatable()
			requested = node.GetBestEffortRequested()
		}
		if allocatable != nil {
			totalAllocatable.AddResource(allocatable)
		}
		if requested != nil {
			totalRequested.AddResource(requested)
		}
	}
	return totalAllocatable, totalRequested, nil
}

func findNodeGroupsByPreferAffinity(
	ctx context.Context,
	podLauncher podutil.PodLauncher,
	unit framework.ScheduleUnit,
	unitAffinityTerm framework.UnitAffinityTerm,
	nodeGroupTree []*nodeCircleElem,
	startIndexOfNewElem int,
	assignedOrPreferredNodes sets.String,
	nodeLister framework.NodeInfoLister,
) ([]*nodeCircleElem, error) {
	preferAffinityTerms := newNodeGroupAffinityTerms([]framework.UnitAffinityTerm{unitAffinityTerm})
	preferAffinitySpecs, err := getPreferAffinitySpecs(ctx, podLauncher, preferAffinityTerms, assignedOrPreferredNodes, nodeLister)
	if err != nil {
		return nil, err
	}

	hasRunningPods := assignedOrPreferredNodes.Len() != 0
	newTopologyElems := nodeGroupTree[startIndexOfNewElem:]
	size := len(newTopologyElems)
	ordered := make([]framework.NodeCircleList, size)

	errCh := parallelize.NewErrorChannel()
	parallelCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	sortRules, sortRulesErr := getSortRules(unit)

	parallelize.Until(parallelCtx, size, func(index int) {
		nodeGroup := newTopologyElems[index].nodeCircle
		preferNodeGroups := make([]framework.NodeCircle, 0)
		// If there are assigned or preferred nodes in the nodegroup，just get the sub topology area where they are from the original topology area.
		if hasRunningPods {
			klog.V(4).InfoS("Unit has assigned or preferred nodes", "unitKey", unit.GetKey(), "numNodes", len(assignedOrPreferredNodes))
			// If there are assigned or preferred nodes in the unit, filter out nodes not matching the same affnity specs
			if preferAffinitySpecs != nil { // If these nodes are in different topologies, `preferAffinitySpecs` will be nil (refer to UT for getPreferAffinitySpecs)
				preferNodeGroups, err = groupNodesByAffinitySpecs(podLauncher, nodeGroup, assignedOrPreferredNodes, preferAffinitySpecs)
				if err != nil {
					errCh.SendErrorWithCancel(err, cancel)
					return
				}
			}
		} else {
			// No assigned or preferred nodes, then nodes should be grouped by affinity terms
			tmpNodeGroups, err := groupNodesByAffinityTerms(parallelCtx, podLauncher, nodeGroup, preferAffinityTerms)
			if err != nil {
				errCh.SendErrorWithCancel(err, cancel)
				return
			}
			preferNodeGroups = tmpNodeGroups
		}
		// first sort by required, then sort by prefer
		if sortRulesErr == nil {
			klog.V(4).InfoS("Started to sort node groups according to sort rules", "unitKey", unit.GetKey(), "sortRules", sortRules)
			sortNodeCircles(parallelCtx, unit, preferNodeGroups, sortRules)
		}
		ordered[index] = preferNodeGroups
	})

	if err := errCh.ReceiveError(); err != nil {
		return nil, err
	}

	for i, nodeGroupList := range ordered {
		for j := len(nodeGroupList) - 1; j >= 0; j-- {
			nodeGroupTree[startIndexOfNewElem+i].children = append(nodeGroupTree[startIndexOfNewElem+i].children, len(nodeGroupTree))
			nodeGroupTree = append(nodeGroupTree, newNodeCircleElem(nodeGroupList[j]))
		}
	}

	return nodeGroupTree, nil
}

func getPreferAffinitySpecs(
	ctx context.Context,
	podLauncher podutil.PodLauncher,
	preferAffinityTerms *nodeGroupAffinityTerms,
	assigned sets.String,
	nodeLister framework.NodeInfoLister,
) (*nodeGroupAffinitySpecs, error) {
	keys := sets.NewString()
	var result *nodeGroupAffinitySpecs
	assignedNodes := assigned.List()
	size := len(assignedNodes)

	var lock sync.Mutex
	errCh := parallelize.NewErrorChannel()
	parallelCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	// find all node groups that can satisfy all preferred affinity requirements
	parallelize.Until(parallelCtx, size, func(index int) {
		nodeName := assignedNodes[index]
		nodeInfo, err := nodeLister.Get(nodeName)
		if err != nil {
			errCh.SendErrorWithCancel(err, cancel)
			return
		}

		matchedLabels, matched := getNodeLabelsIfLauncherMatches(podLauncher, nodeInfo)
		if matched {
			nodeLabels := matchedLabels
			specs, err := preferAffinityTerms.getAffinitySpecs(nodeLabels)
			if err != nil {
				klog.V(4).InfoS("Running pods did not meet preferred requirements", "preferAffinity", preferAffinityTerms.String(), "nodeLabels", nodeLabels)
				lock.Lock()
				defer lock.Unlock()
				result = nil
				cancel()
				return
			}
			key := specs.getNodeGroupKey()
			lock.Lock()
			defer lock.Unlock()
			if keys.Has(key) {
				return
			} else if len(keys) == 0 {
				keys.Insert(key)
				result = specs
			} else {
				result = nil
				cancel()
				return
			}
		}
	})

	if err := errCh.ReceiveError(); err != nil {
		return nil, err
	}

	return result, nil
}
