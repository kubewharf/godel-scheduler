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

package nonnativeresource

import (
	"fmt"
	"math"
	"sort"

	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/apimachinery/pkg/util/sets"
	utilfeature "k8s.io/apiserver/pkg/util/feature"
	listerv1 "k8s.io/client-go/listers/core/v1"
	"k8s.io/klog/v2"

	"github.com/kubewharf/godel-scheduler/pkg/features"
	framework "github.com/kubewharf/godel-scheduler/pkg/framework/api"
	"github.com/kubewharf/godel-scheduler/pkg/util"
	godelfeatures "github.com/kubewharf/godel-scheduler/pkg/util/features"
	"github.com/kubewharf/godel-scheduler/pkg/util/helper"
	podutil "github.com/kubewharf/godel-scheduler/pkg/util/pod"
	katalystv1alpha1 "github.com/kubewharf/katalyst-api/pkg/apis/node/v1alpha1"
)

const (
	MonopolizedPodFailed                = "node topology not satisfy socket pod requests"
	SharedCoresPodFailed                = "node topology not satisfy shared qos pod requests"
	NonExclusiveDedicatedCoresPodFailed = "node topology not satisfy non-exclusive dedicated cores pod requests"
	ExclusiveDedicatedCoresPodFailed    = "node topology not satisfy exclusive dedicated cores pod requests"
)

var defaultAlignedResources = []string{string(util.ResourceGPU)}

func FeasibleNonNativeTopology(pod *v1.Pod,
	resourceType podutil.PodResourceType,
	resourcesRequests map[string]*resource.Quantity,
	nodeInfo framework.NodeInfo,
	podLister listerv1.PodLister,
) *framework.Status {
	if resourceType == podutil.BestEffortPod {
		return nil
	}

	var podAllocations map[int]*v1.ResourceList
	if microTopologyStr := pod.GetAnnotations()[podutil.MicroTopologyKey]; microTopologyStr != "" {
		var err error
		if podAllocations, err = util.UnmarshalMicroTopology(microTopologyStr); err != nil {
			return framework.NewStatus(framework.Error, fmt.Sprintf("failed to parse micro topology: %v", err))
		}
	}

	if len(podAllocations) == 0 {
		return FitsNonNativeTopology(pod, resourcesRequests, nodeInfo, podLister)
	}
	return CheckNonNativeTopology(pod, resourcesRequests, nodeInfo, podLister, podAllocations)
}

func FitsNonNativeTopology(
	pod *v1.Pod,
	resourcesRequests map[string]*resource.Quantity,
	nodeInfo framework.NodeInfo,
	podLister listerv1.PodLister,
) *framework.Status {
	if !utilfeature.DefaultFeatureGate.Enabled(features.EnableColocation) {
		return nil
	}
	// if pod is dedicated_cores & kubecrane.bytedance.com/numa_binding=true, need consider topology and fine-grained pod (anti-)affinity
	if numaBinding, isExcusive := util.NeedConsiderTopology(pod); numaBinding {
		if isExcusive {
			alignedResources := defaultAlignedResources
			if gotAlignedResources, ok := podutil.GetPodAlignedResources(pod); ok {
				alignedResources = gotAlignedResources
			}
			return fitsNumaTopologyForExclusiveDedicatedQoS(nodeInfo, resourcesRequests, sets.NewString(alignedResources...))
		}
		// score is the max number of pods match affinity label selectors in each located numa
		return fitsNumaTopologyForNonExclusiveDedicatedQoS(nodeInfo, resourcesRequests, podLister)
	}
	// TODO: remove when there is no existing monopolized pod
	// other pods, need consider total available resources, no need to consider topology
	return fitsNumaTopologyForSharedQoS(nodeInfo, resourcesRequests)
}

func resourcesSufficientForCommingPod(resourceRequests map[string]*resource.Quantity, numaList []int, numaTopology *framework.NumaTopologyStatus) bool {
	resourceAvailableList := numaTopology.GetFreeResourcesInNumaList(numaList)
	for resourceName, resourceAvailable := range resourceAvailableList {
		resourceRequest := resourceRequests[resourceName.String()]
		if resourceRequest == nil {
			continue
		}
		if resourceRequest.Cmp(resourceAvailable) > 0 {
			return false
		}
	}
	return true
}

func getNumaCapacity(cnr *katalystv1alpha1.CustomNodeResource, nodeCapacity *framework.Resource) int64 {
	var numaCapacity int64
	for _, property := range cnr.Spec.NodeResourceProperties {
		if property.PropertyName == util.ResourceNuma.String() {
			numaCapacity = property.PropertyQuantity.Value()
		}
	}
	return numaCapacity
}

func resourcesSufficientForSharedCoresPods(unavailableNumaList []int, nodeInfo framework.NodeInfo, podRequests map[string]*resource.Quantity) bool {
	available := nodeInfo.GetResourcesAvailableForSharedCoresPods(unavailableNumaList)
	requests := nodeInfo.GetResourcesRequestsOfSharedCoresPods()
	if podRequests == nil {
		return satisfyRequestsForExistingSharedCoresPods(available, requests)
	}
	return satisfyRequestsForIncomingSharedCoresPods(available, requests, podRequests)
}

func numaOverUsed(numaForSharedCores int64, numaForSocketPods int64, numasUnavailable int64, numaCapacity int64) bool {
	return numaForSharedCores+numaForSocketPods+numasUnavailable > numaCapacity
}

func satisfyRequestsForExistingSharedCoresPods(available, existingRequests *framework.Resource) bool {
	return available.Satisfy(existingRequests)
}

func satisfyRequestsForIncomingSharedCoresPods(available, existingRequests *framework.Resource, podRequests map[string]*resource.Quantity) bool {
	for resourceName, resourceVal := range podRequests {
		if resourceVal.IsZero() {
			continue
		}
		if !framework.IsConflictResourcesForDifferentQoS(v1.ResourceName(resourceName)) {
			continue
		}
		switch resourceName {
		case v1.ResourceCPU.String():
			if available.MilliCPU < existingRequests.MilliCPU+resourceVal.MilliValue() {
				return false
			}
		case v1.ResourceMemory.String():
			if available.Memory < existingRequests.Memory+resourceVal.Value() {
				return false
			}
		case v1.ResourceEphemeralStorage.String():
			if utilfeature.DefaultFeatureGate.Enabled(godelfeatures.LocalStorageCapacityIsolation) {
				// if the local storage capacity isolation feature gate is disabled, pods request 0 disk.
				if available.EphemeralStorage < existingRequests.EphemeralStorage+resourceVal.Value() {
					return false
				}
			}
		default:
			if helper.IsScalarResourceName(v1.ResourceName(resourceName)) {
				if available.ScalarResources[v1.ResourceName(resourceName)] <
					existingRequests.ScalarResources[v1.ResourceName(resourceName)]+resourceVal.Value() {
					return false
				}
			}
		}
	}
	return true
}

func fitsNumaTopologyForExclusiveDedicatedQoS(nodeInfo framework.NodeInfo,
	resourcesRequests map[string]*resource.Quantity,
	alignedResources sets.String,
) *framework.Status {
	numaTopology := nodeInfo.GetNumaTopologyStatus()
	nodeCapacity := nodeInfo.GetGuaranteedCapacity()
	numaCapacity := getNumaCapacity(nodeInfo.GetCNR(), nodeCapacity)
	if numaCapacity == 0 {
		return framework.NewStatus(framework.Unschedulable, ExclusiveDedicatedCoresPodFailed)
	}
	var maxNumaRequestForConflictResources int64 = 0
	resourcesRequestsForConflictResources := map[string]*resource.Quantity{}
	resourcesRequestsForNonConflictResources := map[string]*resource.Quantity{}
	resourcesToNuma := map[string]int64{}
	for resourceName, resourceReq := range resourcesRequests {
		requestNuma := int64(getNumaRequest(v1.ResourceName(resourceName), resourceReq, numaTopology, nodeCapacity, numaCapacity))
		if requestNuma <= 0 {
			continue
		}
		resourcesToNuma[resourceName] = requestNuma
		isConflictResources := framework.IsConflictResourcesForDifferentQoS(v1.ResourceName(resourceName))
		if isConflictResources {
			if requestNuma > maxNumaRequestForConflictResources {
				maxNumaRequestForConflictResources = requestNuma
			}
			resourcesRequestsForConflictResources[resourceName] = resourceReq
		} else {
			if alignedResources.Has(resourceName) {
				if requestNuma > maxNumaRequestForConflictResources {
					maxNumaRequestForConflictResources = requestNuma
				}
			}
			resourcesRequestsForNonConflictResources[resourceName] = resourceReq
		}
	}
	resourcesRequestsLessThanConflictResources := map[string]*resource.Quantity{}
	resourcesRequestsMoreThanConflictResources := map[string]*resource.Quantity{}
	var (
		resourcesLessThanConflictResources []string
		resourcesMoreThanConflictResources []string
	)
	for resourceName, resourceVal := range resourcesRequestsForNonConflictResources {
		if resourcesToNuma[resourceName] <= maxNumaRequestForConflictResources {
			resourcesRequestsLessThanConflictResources[resourceName] = resourceVal
			resourcesLessThanConflictResources = append(resourcesLessThanConflictResources, resourceName)
		} else {
			resourcesRequestsMoreThanConflictResources[resourceName] = resourceVal
			resourcesMoreThanConflictResources = append(resourcesMoreThanConflictResources, resourceName)
		}
	}
	sort.Slice(resourcesLessThanConflictResources, func(i, j int) bool {
		return resourcesToNuma[resourcesLessThanConflictResources[i]] < resourcesToNuma[resourcesLessThanConflictResources[j]]
	})
	sort.Slice(resourcesMoreThanConflictResources, func(i, j int) bool {
		return resourcesToNuma[resourcesMoreThanConflictResources[i]] < resourcesToNuma[resourcesMoreThanConflictResources[j]]
	})

	return fitsNumaTopologyTraversingNumaList(nodeInfo, resourcesToNuma, resourcesRequestsForConflictResources, maxNumaRequestForConflictResources, resourcesRequestsLessThanConflictResources, resourcesRequestsMoreThanConflictResources, resourcesLessThanConflictResources, resourcesMoreThanConflictResources)
}

func fitsNumaTopologyTraversingNumaList(nodeInfo framework.NodeInfo,
	resourcesToNuma map[string]int64,
	resourcesRequestsForConflictResources map[string]*resource.Quantity,
	maxNumaRequestForConflictResources int64,
	resourcesRequestsLessThanConflictResources, resourcesRequestsMoreThanConflictResources map[string]*resource.Quantity,
	resourcesLessThanConflictResources, resourcesMoreThanConflictResources []string,
) *framework.Status {
	numaTopology := nodeInfo.GetNumaTopologyStatus()

	// get least socket count
	nodeNumaNum := numaTopology.GetNumaNum()
	nodeSocketList := numaTopology.GetSocketList()
	nodeSocketNum := len(nodeSocketList)
	if nodeNumaNum == 0 || nodeSocketNum == 0 {
		return framework.NewStatus(framework.Unschedulable, ExclusiveDedicatedCoresPodFailed)
	}
	numaNumPerSocket := nodeNumaNum / nodeSocketNum
	requestSocketForConflictResources := int64(math.Ceil(float64(maxNumaRequestForConflictResources) / float64(numaNumPerSocket)))
	nodeNumaList := numaTopology.GetNumaList()

	// traverse all numa lists for conflict resources
	selectSocketListsForConflictResources := comb(nodeSocketNum, int(requestSocketForConflictResources), nodeSocketList)
	for _, socketList := range selectSocketListsForConflictResources {
		freeNumaList := sets.NewInt()
		for _, socketId := range socketList {
			numas := numaTopology.GetFreeNumasInSocketForConflictResources(socketId)
			freeNumaList.Insert(numas.List()...)
		}
		if len(freeNumaList) < int(maxNumaRequestForConflictResources) {
			continue
		}
		selectNumasListsForConflictResources := comb(len(freeNumaList), int(maxNumaRequestForConflictResources), freeNumaList.List())
		for _, numaListForConflictResources := range selectNumasListsForConflictResources {
			// check whether resources in numa list are sufficient for coming pod
			if !resourcesSufficientForCommingPod(resourcesRequestsForConflictResources, numaListForConflictResources, numaTopology) {
				continue
			}
			// check whether resources in free numa(concluding numas for coming pod) are sufficient for shared pods
			if !resourcesSufficientForSharedCoresPods(numaListForConflictResources, nodeInfo, nil) {
				continue
			}
			// check non-conflict resources less than conflict resources
			if !recursiveNumaListForLessResources(numaTopology, resourcesToNuma, numaListForConflictResources, len(resourcesLessThanConflictResources)-1,
				resourcesLessThanConflictResources, resourcesRequestsLessThanConflictResources) {
				continue
			}
			// check non-conflict resources more than conflict resources
			if !recursiveNumaListForMoreResources(numaTopology, resourcesToNuma, numaListForConflictResources, nodeNumaList, 0,
				resourcesMoreThanConflictResources, resourcesRequestsMoreThanConflictResources) {
				continue
			}
			return nil
		}
	}
	return framework.NewStatus(framework.Unschedulable, ExclusiveDedicatedCoresPodFailed)
}

func recursiveNumaListForLessResources(
	numaTopology *framework.NumaTopologyStatus,
	resourcesToNuma map[string]int64,
	existingNumaLists []int, resourceIdx int,
	resourcesLessThanConflictResources []string,
	resourcesRequestsLessThanConflictResources map[string]*resource.Quantity,
) bool {
	if resourceIdx < 0 {
		return true
	}
	resourceName := resourcesLessThanConflictResources[resourceIdx]
	numaReq := resourcesToNuma[resourceName]
	numasListsForNonConflictResources := comb(len(existingNumaLists), int(numaReq), existingNumaLists)
	for _, numaListForNonConflictResources := range numasListsForNonConflictResources {
		if !resourcesSufficientForCommingPod(resourcesRequestsLessThanConflictResources, numaListForNonConflictResources, numaTopology) {
			continue
		}
		if !recursiveNumaListForLessResources(numaTopology, resourcesToNuma,
			numaListForNonConflictResources, resourceIdx-1,
			resourcesLessThanConflictResources, resourcesRequestsLessThanConflictResources) {
			continue
		}
		return true
	}
	return false
}

func recursiveNumaListForMoreResources(
	numaTopology *framework.NumaTopologyStatus,
	resourcesToNuma map[string]int64,
	existingNumaLists, allNumaLists []int, resourceIdx int,
	resourcesMoreThanConflictResources []string,
	resourcesRequestsMoreThanConflictResources map[string]*resource.Quantity,
) bool {
	if resourceIdx == len(resourcesMoreThanConflictResources) {
		return true
	}
	resourceName := resourcesMoreThanConflictResources[resourceIdx]
	numaReq := resourcesToNuma[resourceName]
	numasListsForNonConflictResources := getNumasLists(existingNumaLists, allNumaLists, numaReq-int64(len(existingNumaLists)))
	for _, numaListForNonConflictResources := range numasListsForNonConflictResources {
		numaListForNonConflictResources = append(numaListForNonConflictResources, existingNumaLists...)
		if !resourcesSufficientForCommingPod(resourcesRequestsMoreThanConflictResources, numaListForNonConflictResources, numaTopology) {
			continue
		}
		if !recursiveNumaListForMoreResources(numaTopology, resourcesToNuma,
			numaListForNonConflictResources, allNumaLists, resourceIdx+1,
			resourcesMoreThanConflictResources, resourcesRequestsMoreThanConflictResources) {
			continue
		}
		return true
	}
	return false
}

func getNumasLists(existingNumas []int, allNumas []int, count int64) [][]int {
	existingNumasSet := sets.NewInt(existingNumas...)
	var numasCanBeSelected []int
	for _, numa := range allNumas {
		if existingNumasSet.Has(numa) {
			continue
		}
		numasCanBeSelected = append(numasCanBeSelected, numa)
	}
	return comb(len(numasCanBeSelected), int(count), numasCanBeSelected)
}

func getNumaRequest(rName v1.ResourceName, rVal *resource.Quantity, numaTopology *framework.NumaTopologyStatus, nodeCapacity *framework.Resource, numaCapacity int64) float64 {
	if !numaTopology.HasResourceInTopology(rName) {
		return 0
	}
	switch rName {
	case v1.ResourceCPU:
		resourcePerNuma := math.Ceil(float64(nodeCapacity.MilliCPU) / float64(numaCapacity))
		return math.Ceil(float64(rVal.MilliValue()) / resourcePerNuma)
	case v1.ResourceMemory:
		resourcePerNuma := math.Ceil(float64(nodeCapacity.Memory) / float64(numaCapacity))
		return math.Ceil(float64(rVal.Value()) / resourcePerNuma)
	case v1.ResourceEphemeralStorage:
		resourcePerNuma := math.Ceil(float64(nodeCapacity.EphemeralStorage) / float64(numaCapacity))
		return math.Ceil(float64(rVal.Value()) / resourcePerNuma)
	default:
		resourcePerNuma := math.Ceil(float64(nodeCapacity.ScalarResources[rName]) / float64(numaCapacity))
		return math.Ceil(float64(rVal.Value()) / resourcePerNuma)
	}
}

func fitsNumaTopologyForNonExclusiveDedicatedQoS(nodeInfo framework.NodeInfo,
	resourcesRequests map[string]*resource.Quantity,
	podLister listerv1.PodLister,
) *framework.Status {
	var rNames []string
	for rName := range resourcesRequests {
		rNames = append(rNames, rName)
	}
	numaTopology := nodeInfo.GetNumaTopologyStatus()
	numaList := numaTopology.GetNumaList()
	for numa := range numaList {
		// check whether resources in numa are sufficient for coming pod
		if !resourcesSufficientForCommingPod(resourcesRequests, []int{numa}, numaTopology) {
			continue
		}
		// check whether resources in free numa(concluding numas for coming pod) are sufficient for shared pods
		if !resourcesSufficientForSharedCoresPods([]int{numa}, nodeInfo, nil) {
			continue
		}
		return nil
	}
	return framework.NewStatus(framework.Unschedulable, NonExclusiveDedicatedCoresPodFailed)
}

func fitsNumaTopologyForSharedQoS(nodeInfo framework.NodeInfo, resourcesRequests map[string]*resource.Quantity) *framework.Status {
	if !resourcesSufficientForSharedCoresPods([]int{}, nodeInfo, resourcesRequests) {
		return framework.NewStatus(framework.Unschedulable, SharedCoresPodFailed)
	}
	return nil
}

func CheckNonNativeTopology(
	pod *v1.Pod,
	resourcesRequests map[string]*resource.Quantity,
	nodeInfo framework.NodeInfo,
	podLister listerv1.PodLister,
	podAllocations map[int]*v1.ResourceList,
) *framework.Status {
	// if pod is dedicated_cores & kubecrane.bytedance.com/numa_binding=true, need consider topology and fine-grained pod (anti-)affinity
	if numaBinding, isExculive := util.NeedConsiderTopology(pod); numaBinding {
		if isExculive {
			return checkNumaTopologyForExclusiveDedicatedQoS(nodeInfo, podAllocations)
		}
		// score is the max number of pods match affinity label selectors in each located numa
		return checkNumaTopologyForNonExclusiveDedicatedQoS(nodeInfo, podLister, podAllocations)
	}
	// TODO: remove when there is no existing monopolized pod
	// other pods, need consider total available resources, no need to consider topology
	return checkNumaTopologyForSharedQoS(nodeInfo, resourcesRequests, podAllocations)
}

func checkNumaTopologyForExclusiveDedicatedQoS(nodeInfo framework.NodeInfo,
	podAllocations map[int]*v1.ResourceList,
) *framework.Status {
	numaTopology := nodeInfo.GetNumaTopologyStatus()
	var numaList []int
	for numaId, allocationInNuma := range podAllocations {
		// check available resources
		if !sufficientResource(*allocationInNuma, numaTopology.GetFreeResourcesInNuma(numaId)) {
			return framework.NewStatus(framework.Unschedulable, ExclusiveDedicatedCoresPodFailed)
		}

		// check exclusive & shared cores
		var (
			rNames              []string
			hasConflictResource bool
		)
		for rName := range *allocationInNuma {
			rNames = append(rNames, rName.String())
			if framework.IsConflictResourcesForDifferentQoS(rName) {
				hasConflictResource = true
				// check exclusive
				if !numaTopology.IsNumaEmptyForConflictResources(numaId) {
					return framework.NewStatus(framework.Unschedulable, ExclusiveDedicatedCoresPodFailed)
				}
			}
		}
		if hasConflictResource {
			numaList = append(numaList, numaId)
		}
	}
	// check whether resources in free numa(concluding numas for coming pod) are sufficient for shared pods
	if !resourcesSufficientForSharedCoresPods(numaList, nodeInfo, nil) {
		return framework.NewStatus(framework.Unschedulable, ExclusiveDedicatedCoresPodFailed)
	}

	return nil
}

func checkNumaTopologyForNonExclusiveDedicatedQoS(nodeInfo framework.NodeInfo,
	podLister listerv1.PodLister,
	podAllocations map[int]*v1.ResourceList,
) *framework.Status {
	numaTopology := nodeInfo.GetNumaTopologyStatus()
	var numaList []int
	for numaId, allocationInNuma := range podAllocations {
		if !sufficientResource(*allocationInNuma, numaTopology.GetFreeResourcesInNuma(numaId)) {
			return framework.NewStatus(framework.Unschedulable, NonExclusiveDedicatedCoresPodFailed)
		}

		var (
			rNames              []string
			hasConflictResource bool
		)
		for rName := range *allocationInNuma {
			rNames = append(rNames, rName.String())
			if framework.IsConflictResourcesForDifferentQoS(rName) {
				hasConflictResource = true
			}
		}
		if hasConflictResource {
			numaList = append(numaList, numaId)
		}
	}
	// check whether resources in free numa(concluding numas for coming pod) are sufficient for shared pods
	if !resourcesSufficientForSharedCoresPods(numaList, nodeInfo, nil) {
		return framework.NewStatus(framework.Unschedulable, NonExclusiveDedicatedCoresPodFailed)
	}

	return nil
}

func checkNumaTopologyForSharedQoS(nodeInfo framework.NodeInfo, resourcesRequests map[string]*resource.Quantity, podAllocations map[int]*v1.ResourceList) *framework.Status {
	numaTopology := nodeInfo.GetNumaTopologyStatus()
	for numaId, allocationInNuma := range podAllocations {
		// check if resources in numa is sufficient
		if !sufficientResource(*allocationInNuma, numaTopology.GetFreeResourcesInNuma(numaId)) {
			return framework.NewStatus(framework.Unschedulable, SharedCoresPodFailed)
		}
	}

	// check whether resources in free numa are sufficient for shared pods
	if !resourcesSufficientForSharedCoresPods(nil, nodeInfo, resourcesRequests) {
		return framework.NewStatus(framework.Unschedulable, SharedCoresPodFailed)
	}

	return nil
}

func sufficientResource(requestList, availableList v1.ResourceList) bool {
	for resourceName, resourceVal := range requestList {
		if resourceVal.Cmp(availableList[resourceName]) > 0 {
			return false
		}
	}
	return true
}

func comb(totalCount, selectCount int, items []int) [][]int {
	var selectedItemsLists [][]int
	for i := 0; i < len(items); i++ {
		if selectCount == 1 {
			selectedItemsLists = append(selectedItemsLists, []int{items[i]})
			continue
		}
		subItemsLists := comb(totalCount-1, selectCount-1, items[i+1:])
		for _, subNumaList := range subItemsLists {
			subNumaList = append(subNumaList, items[i])
			selectedItemsLists = append(selectedItemsLists, subNumaList)
		}
	}
	return selectedItemsLists
}

func AssignMicroTopology(node framework.NodeInfo, pod *v1.Pod, state *framework.CycleState) string {
	podAllocation := allocate(node, pod, state)
	if len(podAllocation) == 0 {
		return ""
	}
	microTopologyStr := util.MarshalMicroTopology(podAllocation)
	if pod.Annotations == nil {
		pod.Annotations = make(map[string]string)
	}
	return microTopologyStr
}

func allocate(node framework.NodeInfo, pod *v1.Pod, state *framework.CycleState) map[int]*v1.ResourceList {
	numaBinding, isExcusive := util.NeedNumaBinding(pod)
	qosLevel := podutil.GetQoSLevelForPod(pod)
	if !numaBinding && qosLevel != util.DedicatedCores {
		return nil
	}

	numas, err := framework.GetAssignedNumas(state)
	if err != nil {
		klog.ErrorS(err, "Get assumed numas failed", "podKey", podutil.GetPodKey(pod), "nodeName", node.GetNodeName())
		return nil
	} else if len(numas) == 0 {
		klog.InfoS("Get nil assigned numas", "podKey", podutil.GetPodKey(pod), "nodeName", node.GetNodeName())
		return nil
	}

	resourcesRequests := podutil.GetPodRequests(pod)

	switch qosLevel {
	case util.DedicatedCores:
		if isExcusive {
			return getExclusiveAllocation(node, numas)
		} else {
			return getNonExclusiveAllocation(resourcesRequests, numas)
		}
	case util.SharedCores:
		return getNonExclusiveAllocation(resourcesRequests, numas)
	}
	return nil
}

func getExclusiveAllocation(nodeInfo framework.NodeInfo, numas []int) map[int]*v1.ResourceList {
	allocation := map[int]*v1.ResourceList{}
	numaTopo := nodeInfo.GetNumaTopologyStatus().GetNumaTopology()
	for _, numa := range numas {
		numaStatus := numaTopo[numa]
		if numaStatus == nil {
			continue
		}
		rList := numaStatus.GetNumaResourcesAllocatable()
		allocation[numa] = &v1.ResourceList{
			v1.ResourceCPU:    (*rList)[v1.ResourceCPU],
			v1.ResourceMemory: (*rList)[v1.ResourceMemory],
		}
	}
	return allocation
}

func getNonExclusiveAllocation(resourcesRequests map[string]*resource.Quantity, numas []int) map[int]*v1.ResourceList {
	allocation := map[int]*v1.ResourceList{}
	if len(numas) != 1 {
		return nil
	}
	numa := numas[0]
	allocation[numa] = &v1.ResourceList{}
	if cpuReq := resourcesRequests[v1.ResourceCPU.String()]; cpuReq != nil && !cpuReq.IsZero() {
		(*allocation[numa])[v1.ResourceCPU] = *cpuReq
	}
	if memReq := resourcesRequests[v1.ResourceMemory.String()]; memReq != nil && !memReq.IsZero() {
		(*allocation[numa])[v1.ResourceMemory] = *memReq
	}
	return allocation
}
