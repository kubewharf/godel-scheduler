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

// topologyElem is the node in a topology tree. It includes the nodeCircle, the children nodes in the tree,
// and the bottomNodeCirclesInTree, which will be written while getting the node group slice from the tree.
type topologyElem struct {
	nodeCircle              framework.NodeCircle
	cutOff                  bool
	children                []int
	bottomNodeCirclesInTree []framework.NodeCircle
}

func newTopologyElem(nodeCircle framework.NodeCircle) *topologyElem {
	return &topologyElem{
		nodeCircle: nodeCircle,
	}
}

/*
The topologyTree is the result of reversing the real topology graph. For example, if the real topology graph is:

				AllNodes

			/              \

	bigPod1				 bigPod2

	/	   \		     /      \

miniPod1  miniPod2	miniPod3	miniPod4

The topology tree will be:

				AllNodes

			/              \

	bigPod2				 bigPod1

	/	   \		     /      \

miniPod4  miniPod3	miniPod2	miniPod1
*/
func getNodeGroupsFromTree(topologyTree []*topologyElem) ([]framework.NodeGroup, error) {
	if len(topologyTree) == 0 {
		return nil, fmt.Errorf("empty topology tree")
	}
	nodeGroups := make([]framework.NodeGroup, 0)
	for i := len(topologyTree) - 1; i >= 0; i-- {
		var ncs []framework.NodeCircle
		elem := topologyTree[i]
		if elem.nodeCircle == nil {
			return nil, fmt.Errorf("nil node circle in elem %v", i)
		}

		if len(elem.children) == 0 { // when it is a leaf node
			ncs = append(ncs, elem.nodeCircle)
		} else {
			childrenNodeCount := 0
			for k := len(elem.children) - 1; k >= 0; k-- {
				childIndex := elem.children[k]
				if childIndex >= len(topologyTree) {
					return nil, fmt.Errorf("unexpected child index %v in elem %v while length of tree is %v", childIndex, i, len(topologyTree))
				}
				childrenNodeCount += topologyTree[childIndex].nodeCircle.Len()
			}
			if childrenNodeCount < elem.nodeCircle.Len() {
				ncs = append(ncs, elem.nodeCircle)
			} else {
				for k := len(elem.children) - 1; k >= 0; k-- {
					ncs = append(ncs, topologyTree[elem.children[k]].bottomNodeCirclesInTree...)
				}
			}
		}
		elem.bottomNodeCirclesInTree = ncs

		if !elem.cutOff {
			nodeGroups = append(nodeGroups, framework.NewNodeGroup(elem.nodeCircle.GetKey(), nil, ncs))
		} else {
			sz := 0
			for _, nc := range ncs {
				sz += nc.Len()
			}
			klog.V(4).InfoS("cut off a node group while getting node groups from tree", "key", elem.nodeCircle.GetKey(), "numberOfNodeCircles", len(ncs), "numberOfNodes", sz)
		}
	}

	return nodeGroups, nil
}

// divideNodesByRequireAffinity divides the original nodes into several topology domains which will be sorted.
// It returns a one level tree with multiple root nodes.
func divideNodesByRequireAffinity(
	ctx context.Context,
	podLauncher podutil.PodLauncher,
	unit framework.ScheduleUnit,
	unitAffinityTerms []framework.UnitAffinityTerm,
	assignedNodes sets.String,
	nodeGroup framework.NodeGroup,
	request *preCheckResource,
) ([]*topologyElem, error) {
	requiredAffinityTerms := newNodeGroupAffinityTerms(unitAffinityTerms)
	nodeCircle := nodeGroup.GetNodeCircles()[0] // the caller must ensure that the node group has at least one node circle.

	var topologyElems []*topologyElem
	if assignedNodes.Len() != 0 {
		klog.V(4).InfoS("Unit has running pods on nodes", "unitKey", unit.GetKey(), "numNodes", len(assignedNodes))
		// there are running pods in the unit, filter nodes matching the same affnity specs of these running pods
		requiredAffinitySpecs, err := getRequiredAffinitySpecs(ctx, podLauncher, requiredAffinityTerms, assignedNodes, nodeGroup)
		if err != nil {
			return nil, err
		}
		requiredNodeCircleList, err := groupNodesByAffinitySpecs(podLauncher, nodeCircle, assignedNodes, requiredAffinitySpecs)
		if err != nil {
			return nil, err
		}
		topologyElems = getTopologyElems(requiredNodeCircleList)
	} else {
		klog.V(4).InfoS("Unit has no running pods", "unitKey", unit.GetKey())
		// no running pods, then nodes should be grouped by affinity terms
		requiredNodeCircleList, err := groupNodesByAffinityTerms(ctx, podLauncher, nodeCircle, requiredAffinityTerms)
		if err != nil {
			return nil, err
		}
		topologyElems = getTopologyElems(requiredNodeCircleList)

		sortRules := getSortRules(unit)
		klog.V(4).InfoS("Started to sort node circles", "unitKey", unit.GetKey(), "sortRules", sortRules)
		sortAndMarkTopologyElems(ctx, unit, topologyElems, sortRules, request, false)
	}

	sz := len(topologyElems)
	topologyTree := make([]*topologyElem, sz)
	for i := range topologyElems {
		topologyTree[sz-1-i] = topologyElems[i]
	}
	return topologyTree, nil
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
			listers[topologyKey] = &framework.NodeInfoListerImpl{}
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

// sortAndMarkTopologyElems marks those topologyElems which have insufficient resource as `cutOff`, and sorts elems
// according to the unit's sort rules. If there are no sort rules, we sort by the default rule.
func sortAndMarkTopologyElems(
	ctx context.Context,
	unit framework.ScheduleUnit,
	topologyElems []*topologyElem,
	sortRules []framework.SortRule,
	unitRequest *preCheckResource,
	isParentCutOff bool,
) {
	if len(topologyElems) == 0 {
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

	var lock sync.Mutex
	totalAllocatable, totalRequested := make(map[string]*framework.Resource), make(map[string]*framework.Resource)
	errCh := parallelize.NewErrorChannel()
	parallelCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	parallelize.Until(parallelCtx, len(topologyElems), func(index int) {
		nodeCircle := topologyElems[index].nodeCircle
		allocatable, requested := getNodeCircleAllocatableResource(nodeCircle, resourceType)
		if isParentCutOff || unitRequest.greater(allocatable) {
			topologyElems[index].cutOff = true
		}

		lock.Lock()
		defer lock.Unlock()
		totalAllocatable[nodeCircle.GetKey()] = allocatable
		totalRequested[nodeCircle.GetKey()] = requested
	})

	if err := errCh.ReceiveError(); err != nil {
		return
	}

	sort.SliceStable(topologyElems, func(i, j int) bool {
		nodeCircle1, nodeCircle2 := topologyElems[i].nodeCircle, topologyElems[j].nodeCircle
		return compareAccordingToSortRules(totalAllocatable, totalRequested, sortRules, nodeCircle1.GetKey(), nodeCircle2.GetKey())
	})
}

func compareAccordingToSortRules(allocatable, requested map[string]*framework.Resource, sortRules []framework.SortRule, k1, k2 string) bool {
	for _, rule := range sortRules {
		var v1, v2 int64
		if rule.Dimension == framework.Capacity {
			switch rule.Resource {
			case framework.CPUResource:
				v1, v2 = allocatable[k1].MilliCPU, allocatable[k2].MilliCPU
			case framework.MemoryResource:
				v1, v2 = allocatable[k1].Memory, allocatable[k2].Memory
			case framework.GPUResource:
				v1, v2 = allocatable[k1].ScalarResources[util.ResourceGPU], allocatable[k2].ScalarResources[util.ResourceGPU]
			}
		} else if rule.Dimension == framework.Available {
			var cap1, req1, cap2, req2 int64
			switch rule.Resource {
			case framework.CPUResource:
				cap1, req1 = allocatable[k1].MilliCPU, requested[k1].MilliCPU
				cap2, req2 = allocatable[k2].MilliCPU, requested[k2].MilliCPU
			case framework.MemoryResource:
				cap1, req1 = allocatable[k1].Memory, requested[k1].Memory
				cap2, req2 = allocatable[k2].Memory, requested[k2].Memory
			case framework.GPUResource:
				cap1, req1 = allocatable[k1].ScalarResources[util.ResourceGPU], requested[k1].ScalarResources[util.ResourceGPU]
				cap2, req2 = allocatable[k2].ScalarResources[util.ResourceGPU], requested[k2].ScalarResources[util.ResourceGPU]
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
func getNodeCircleAllocatableResource(nodeCircle framework.NodeCircle, resourceType podutil.PodResourceType) (*framework.Resource, *framework.Resource) {
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
	return totalAllocatable, totalRequested
}

// divideNodesByPreferAffinity divides the bottom level leaf nodes in the original tree, and the new leaf nodes should be sorted.
func divideNodesByPreferAffinity(
	ctx context.Context,
	podLauncher podutil.PodLauncher,
	unit framework.ScheduleUnit,
	unitAffinityTerm framework.UnitAffinityTerm,
	topologyTree []*topologyElem,
	startIndexOfNewElem int,
	assignedOrPreferredNodes sets.String,
	nodeGroup framework.NodeGroup,
	request *preCheckResource,
) ([]*topologyElem, error) {
	preferAffinityTerms := newNodeGroupAffinityTerms([]framework.UnitAffinityTerm{unitAffinityTerm})
	preferAffinitySpecs, err := getPreferAffinitySpecs(ctx, podLauncher, preferAffinityTerms, assignedOrPreferredNodes, nodeGroup)
	if err != nil {
		return nil, err
	}

	newTopologyElems := topologyTree[startIndexOfNewElem:]
	size := len(newTopologyElems)
	ordered := make([][]*topologyElem, size)

	errCh := parallelize.NewErrorChannel()
	parallelCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	sortRules := getSortRules(unit)

	parallelize.Until(parallelCtx, size, func(index int) {
		nc := newTopologyElems[index].nodeCircle
		var preferTopologyElems []*topologyElem
		// If there are assigned or preferred nodes in the nodegroup，just get the sub topology area where they are from the original topology area.
		if assignedOrPreferredNodes.Len() != 0 {
			klog.V(4).InfoS("Unit has assigned or preferred nodes", "unitKey", unit.GetKey(), "numNodes", len(assignedOrPreferredNodes))
			// If there are assigned or preferred nodes in the unit, filter out nodes not matching the same affinity specs
			if preferAffinitySpecs != nil { // If these nodes are in different topologies, `preferAffinitySpecs` will be nil (refer to UT for getPreferAffinitySpecs)
				preferNodeCircles, err := groupNodesByAffinitySpecs(podLauncher, nc, assignedOrPreferredNodes, preferAffinitySpecs)
				if err != nil {
					errCh.SendErrorWithCancel(err, cancel)
					return
				}
				preferTopologyElems = getTopologyElems(preferNodeCircles)
			}
		} else {
			// No assigned or preferred nodes, then nodes should be grouped by affinity terms
			preferNodeCircles, err := groupNodesByAffinityTerms(parallelCtx, podLauncher, nc, preferAffinityTerms)
			if err != nil {
				errCh.SendErrorWithCancel(err, cancel)
				return
			}
			preferTopologyElems = getTopologyElems(preferNodeCircles)
			klog.V(4).InfoS("Started to sort node circles", "unitKey", unit.GetKey(), "sortRules", sortRules)
			sortAndMarkTopologyElems(parallelCtx, unit, preferTopologyElems, sortRules, request, newTopologyElems[index].cutOff)
		}

		ordered[index] = preferTopologyElems
	})

	if err := errCh.ReceiveError(); err != nil {
		return nil, err
	}

	for i, topologyElems := range ordered {
		for j := len(topologyElems) - 1; j >= 0; j-- {
			topologyTree[startIndexOfNewElem+i].children = append(topologyTree[startIndexOfNewElem+i].children, len(topologyTree))
			topologyTree = append(topologyTree, topologyElems[j])
		}
	}

	return topologyTree, nil
}

func getPreferAffinitySpecs(
	ctx context.Context,
	podLauncher podutil.PodLauncher,
	preferAffinityTerms *nodeGroupAffinityTerms,
	assigned sets.String,
	nodeGroup framework.NodeGroup,
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
		nodeInfo, err := nodeGroup.Get(nodeName)
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
