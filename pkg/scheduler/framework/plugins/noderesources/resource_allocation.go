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

package noderesources

import (
	v1 "k8s.io/api/core/v1"
	utilfeature "k8s.io/apiserver/pkg/util/feature"
	"k8s.io/klog/v2"

	framework "github.com/kubewharf/godel-scheduler/pkg/framework/api"
	schedutil "github.com/kubewharf/godel-scheduler/pkg/util"
	"github.com/kubewharf/godel-scheduler/pkg/util/features"
	v1helper "github.com/kubewharf/godel-scheduler/pkg/util/helper"
	podutil "github.com/kubewharf/godel-scheduler/pkg/util/pod"
)

// resourceToWeightMap contains resource name and weight.
type resourceToWeightMap map[v1.ResourceName]int64

func calcWeightSum(r resourceToWeightMap) int64 {
	var weightSum int64
	for _, weight := range r {
		weightSum += weight
	}
	return weightSum
}

// defaultRequestedRatioResources is used to set default requestToWeight map for CPU and memory
var defaultRequestedRatioResources = resourceToWeightMap{v1.ResourceMemory: 1, v1.ResourceCPU: 1}

type scoreFunc func(*framework.CycleState, resourceToValueMap, resourceToValueMap, bool, int, int) int64

// resourceAllocationScorer contains information to calculate resource allocation score.
type resourceAllocationScorer struct {
	Name                string
	scorer              scoreFunc
	resourceToWeightMap resourceToWeightMap
}

// resourceToValueMap contains resource name and score.
type resourceToValueMap map[v1.ResourceName]int64

// score will use `scorer` function to calculate the score.
func (r *resourceAllocationScorer) score(
	state *framework.CycleState,
	pod *v1.Pod,
	nodeInfo framework.NodeInfo,
) (int64, *framework.Status) {
	if r.resourceToWeightMap == nil {
		return 0, framework.NewStatus(framework.Error, "resources not found")
	}
	requested := make(resourceToValueMap, len(r.resourceToWeightMap))
	allocatable := make(resourceToValueMap, len(r.resourceToWeightMap))
	for resource := range r.resourceToWeightMap {
		allocatable[resource], requested[resource] = calculateResourceAllocatableRequest(state, nodeInfo, pod, resource)
	}
	var score int64

	// Check if the pod has volumes and this could be added to scorer function for balanced resource allocation.
	if len(pod.Spec.Volumes) >= 0 && utilfeature.DefaultFeatureGate.Enabled(features.BalanceAttachedNodeVolumes) && nodeInfo.GetTransientInfo() != nil {
		score = r.scorer(state, requested, allocatable, true, nodeInfo.GetTransientInfo().TransNodeInfo.RequestedVolumes, nodeInfo.GetTransientInfo().TransNodeInfo.AllocatableVolumesCount)
	} else {
		score = r.scorer(state, requested, allocatable, false, 0, 0)
	}
	if klogV := klog.V(6); klogV.Enabled() {
		if len(pod.Spec.Volumes) >= 0 && utilfeature.DefaultFeatureGate.Enabled(features.BalanceAttachedNodeVolumes) && nodeInfo.GetTransientInfo() != nil {
			klogV.InfoS("Dumped the score detail", "pod", klog.KObj(pod), "node", nodeInfo.GetNodeName(),
				"resourceName", r.Name, "allocatableResourcesMap", allocatable, "requestedResourcesMap", requested,
				"numAllocatableVolumes", nodeInfo.GetTransientInfo().TransNodeInfo.AllocatableVolumesCount,
				"numRequestedVolumes", nodeInfo.GetTransientInfo().TransNodeInfo.RequestedVolumes,
				"score", score)
		} else {
			klogV.InfoS("Dumped the score detail", "pod", klog.KObj(pod), "node", nodeInfo.GetNodeName(),
				"resourceName", r.Name, "allocatableResourcesMap", allocatable, "requestedResourcesMap", requested, "score", score)
		}
	}

	return score, nil
}

// calculateResourceAllocatableRequest returns resources Allocatable and Requested values
func calculateResourceAllocatableRequest(state *framework.CycleState, nodeInfo framework.NodeInfo, pod *v1.Pod, resource v1.ResourceName) (int64, int64) {
	podRequest := calculatePodResourceRequest(pod, resource)
	switch podResourceType, _ := framework.GetPodResourceType(state); podResourceType {
	case podutil.GuaranteedPod:
		return calculateResourceAllocatableRequestCore(nodeInfo.GetGuaranteedAllocatable(), nodeInfo.GetGuaranteedRequested(), nodeInfo.GetGuaranteedNonZeroRequested(), podRequest, resource)
	case podutil.BestEffortPod:
		return calculateResourceAllocatableRequestCore(nodeInfo.GetBestEffortAllocatable(), nodeInfo.GetBestEffortRequested(), nodeInfo.GetBestEffortNonZeroRequested(), podRequest, resource)
	}
	return 0, 0
}

func calculateResourceAllocatableRequestCore(allocatable, requested, nonZeroRequested *framework.Resource, podRequest int64, resource v1.ResourceName) (int64, int64) {
	switch resource {
	case v1.ResourceCPU:
		return allocatable.MilliCPU, nonZeroRequested.MilliCPU + podRequest
	case v1.ResourceMemory:
		return allocatable.Memory, nonZeroRequested.Memory + podRequest

	case v1.ResourceEphemeralStorage:
		return allocatable.EphemeralStorage, requested.EphemeralStorage + podRequest
	default:
		if v1helper.IsScalarResourceName(resource) {
			return allocatable.ScalarResources[resource], requested.ScalarResources[resource] + podRequest
		}
	}

	if klogV := klog.V(6); klogV.Enabled() {
		klogV.InfoS("Skipped node score calculation", "resourceType", resource)
	}
	return 0, 0
}

// calculatePodResourceRequest returns the total non-zero requests. If Overhead is defined for the pod and the
// PodOverhead feature is enabled, the Overhead is added to the result.
// podResourceRequest = max(sum(podSpec.Containers), podSpec.InitContainers) + overHead
func calculatePodResourceRequest(pod *v1.Pod, resource v1.ResourceName) int64 {
	var podRequest int64
	for i := range pod.Spec.Containers {
		container := &pod.Spec.Containers[i]
		value := schedutil.GetNonzeroRequestForResource(resource, &container.Resources.Requests)
		podRequest += value
	}

	for i := range pod.Spec.InitContainers {
		initContainer := &pod.Spec.InitContainers[i]
		value := schedutil.GetNonzeroRequestForResource(resource, &initContainer.Resources.Requests)
		if podRequest < value {
			podRequest = value
		}
	}

	// If Overhead is being utilized, add to the total requests for the pod
	if pod.Spec.Overhead != nil && utilfeature.DefaultFeatureGate.Enabled(features.PodOverhead) {
		if quantity, found := pod.Spec.Overhead[resource]; found {
			podRequest += quantity.Value()
		}
	}

	return podRequest
}
