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
	"strings"

	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/apimachinery/pkg/util/sets"
	utilfeature "k8s.io/apiserver/pkg/util/feature"

	framework "github.com/kubewharf/godel-scheduler/pkg/framework/api"
	"github.com/kubewharf/godel-scheduler/pkg/util/features"
	v1helper "github.com/kubewharf/godel-scheduler/pkg/util/helper"
	podutil "github.com/kubewharf/godel-scheduler/pkg/util/pod"
)

// PodRequest computed at PreFilter and used at Filter.
type PodRequest struct {
	framework.Resource
	ResourceType    podutil.PodResourceType
	IgnorePodsLimit bool
	Err             error
}

// ComputePodResourceRequest returns a framework.Resource that covers the largest
// width in each resource dimension. Because init-containers run sequentially, we collect
// the max in each dimension iteratively. In contrast, we sum the resource vectors for
// regular containers since they run simultaneously.
//
// Example:
//
// Pod:
//
//	InitContainers
//	  IC1:
//	    CPU: 2
//	    Memory: 1G
//	  IC2:
//	    CPU: 2
//	    Memory: 3G
//	Containers
//	  C1:
//	    CPU: 2
//	    Memory: 1G
//	  C2:
//	    CPU: 1
//	    Memory: 1G
//
// Result: CPU: 3, Memory: 3G
func ComputePodResourceRequest(pod *v1.Pod) *PodRequest {
	result := &PodRequest{}
	for _, container := range pod.Spec.Containers {
		result.Add(container.Resources.Requests)
	}

	// take max_resource(sum_pod, any_init_container)
	for _, container := range pod.Spec.InitContainers {
		result.SetMaxResource(container.Resources.Requests)
	}

	// If Overhead is being utilized, add to the total requests for the pod
	if pod.Spec.Overhead != nil && utilfeature.DefaultFeatureGate.Enabled(features.PodOverhead) {
		result.Add(pod.Spec.Overhead)
	}

	result.ResourceType, _ = podutil.GetPodResourceType(pod)
	result.IgnorePodsLimit = podutil.IgnorePodsLimit(pod)
	return result
}

// InsufficientResource describes what kind of resource limit is hit and caused the pod to not fit the node.
type InsufficientResource struct {
	ResourceName v1.ResourceName
	// We explicitly have a parameter for reason to avoid formatting a message on the fly
	// for common resources, which is expensive for cluster autoscaler simulations.
	Reason    string
	Requested int64
	Used      int64
	Capacity  int64
}

func FitsRequest(podRequest *PodRequest, nodeInfo framework.NodeInfo, ignoredExtendedResources, ignoredResourceGroups sets.String) *InsufficientResource {
	switch podRequest.ResourceType {
	case podutil.GuaranteedPod:
		return fitsRequestCore(podRequest, nodeInfo.NumPods(), nodeInfo.GetGuaranteedAllocatable(), nodeInfo.GetGuaranteedRequested(), ignoredExtendedResources, ignoredResourceGroups)

	case podutil.BestEffortPod:
		return fitsRequestCore(podRequest, nodeInfo.NumPods(), nodeInfo.GetBestEffortAllocatable(), nodeInfo.GetBestEffortRequested(), ignoredExtendedResources, ignoredResourceGroups)
	default:
		return nil
	}
}

const ErrReasonTooManyPods = "node(s) had too many pods"

func ErrReasonRequestNotFitMessageFunc(request, resource string) string {
	return "node(s) could not satisfy " + request + " " + resource + " " + "request"
}

func fitsRequestCore(
	podRequest *PodRequest,
	podNumber int,
	allocatable,
	requested *framework.Resource,
	ignoredExtendedResources,
	ignoredResourceGroups sets.String,
) *InsufficientResource {
	allowedPodNumber := allocatable.AllowedPodNumber
	if !podRequest.IgnorePodsLimit && podNumber+1 > allowedPodNumber {
		return &InsufficientResource{
			ResourceName: v1.ResourcePods,
			Reason:       ErrReasonTooManyPods,
			Requested:    1,
			Used:         int64(podNumber),
			Capacity:     int64(allowedPodNumber),
		}
	}

	if podRequest.MilliCPU == 0 &&
		podRequest.Memory == 0 &&
		podRequest.EphemeralStorage == 0 &&
		len(podRequest.ScalarResources) == 0 {
		return nil
	}

	if allocatable.MilliCPU < podRequest.MilliCPU+requested.MilliCPU {
		q := resource.NewMilliQuantity(podRequest.MilliCPU, resource.DecimalSI)
		return &InsufficientResource{
			ResourceName: v1.ResourceCPU,
			Reason:       ErrReasonRequestNotFitMessageFunc(q.String(), string(v1.ResourceCPU)),
			Requested:    podRequest.MilliCPU,
			Used:         requested.MilliCPU,
			Capacity:     allocatable.MilliCPU,
		}
	}

	if allocatable.Memory < podRequest.Memory+requested.Memory {
		q := resource.NewQuantity(podRequest.Memory, resource.BinarySI)
		return &InsufficientResource{
			ResourceName: v1.ResourceMemory,
			Reason:       ErrReasonRequestNotFitMessageFunc(q.String(), string(v1.ResourceMemory)),
			Requested:    podRequest.Memory,
			Used:         requested.Memory,
			Capacity:     allocatable.Memory,
		}
	}

	if allocatable.EphemeralStorage < podRequest.EphemeralStorage+requested.EphemeralStorage {
		q := resource.NewQuantity(podRequest.EphemeralStorage, resource.BinarySI)
		return &InsufficientResource{
			ResourceName: v1.ResourceEphemeralStorage,
			Reason:       ErrReasonRequestNotFitMessageFunc(q.String(), string(v1.ResourceEphemeralStorage)),
			Requested:    podRequest.EphemeralStorage,
			Used:         requested.EphemeralStorage,
			Capacity:     allocatable.EphemeralStorage,
		}
	}

	for rName, rQuant := range podRequest.ScalarResources {
		if v1helper.IsExtendedResourceName(rName) {
			// If this resource is one of the extended resources that should be ignored, we will skip checking it.
			// rName is guaranteed to have a slash due to API validation.
			var rNamePrefix string
			if ignoredResourceGroups.Len() > 0 {
				rNamePrefix = strings.Split(string(rName), "/")[0]
			}
			if ignoredExtendedResources.Has(string(rName)) || ignoredResourceGroups.Has(rNamePrefix) {
				continue
			}
		}

		if allocatable.ScalarResources[rName] < rQuant+requested.ScalarResources[rName] {
			q := resource.NewQuantity(podRequest.ScalarResources[rName], resource.DecimalSI)
			if v1helper.IsHugePageResourceName(rName) {
				q = resource.NewQuantity(podRequest.ScalarResources[rName], resource.BinarySI)
			}
			return &InsufficientResource{
				ResourceName: rName,
				Reason:       ErrReasonRequestNotFitMessageFunc(q.String(), string(rName)),
				Requested:    podRequest.ScalarResources[rName],
				Used:         requested.ScalarResources[rName],
				Capacity:     allocatable.ScalarResources[rName],
			}
		}
	}
	return nil
}
