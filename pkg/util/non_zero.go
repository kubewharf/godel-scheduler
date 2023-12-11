/*
Copyright 2019 The Kubernetes Authors.

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

package util

import (
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	utilfeature "k8s.io/apiserver/pkg/util/feature"

	"github.com/kubewharf/godel-scheduler/pkg/util/features"
	"github.com/kubewharf/godel-scheduler/pkg/util/helper"
)

// For each of these resources, a pod that doesn't request the resource explicitly
// will be treated as having requested the amount indicated below, for the purpose
// of computing priority only. This ensures that when scheduling zero-request pods, such
// pods will not all be scheduled to the machine with the smallest in-use request,
// and that when scheduling regular pods, such pods will not see zero-request pods as
// consuming no resources whatsoever. We chose these values to be similar to the
// resources that we give to cluster addon pods (#10653). But they are pretty arbitrary.
// As described in #11713, we use request instead of limit to deal with resource requirements.
const (
	// DefaultMilliCPURequest defines default milli cpu request number.
	DefaultMilliCPURequest int64 = 100 // 0.1 core
	// DefaultMemoryRequest defines default memory request size.
	DefaultMemoryRequest int64 = 200 * 1024 * 1024 // 200 MB
)

// GetNonzeroRequests returns the default cpu and memory resource request if none is found or
// what is provided on the request.
func GetNonzeroRequests(requests *v1.ResourceList) (int64, int64) {
	return GetNonzeroRequestForResource(v1.ResourceCPU, requests),
		GetNonzeroRequestForResource(v1.ResourceMemory, requests)
}

// GetNonzeroRequestForResource returns the default resource request if none is found or
// what is provided on the request.
func GetNonzeroRequestForResource(resource v1.ResourceName, requests *v1.ResourceList) int64 {
	switch resource {
	case v1.ResourceCPU:
		// Override if un-set, but not if explicitly set to zero
		if _, found := (*requests)[v1.ResourceCPU]; !found {
			return DefaultMilliCPURequest
		}
		return requests.Cpu().MilliValue()
	case v1.ResourceMemory:
		// Override if un-set, but not if explicitly set to zero
		if _, found := (*requests)[v1.ResourceMemory]; !found {
			return DefaultMemoryRequest
		}
		return requests.Memory().Value()
	case v1.ResourceEphemeralStorage:
		// if the local storage capacity isolation feature gate is disabled, pods request 0 disk.
		if !utilfeature.DefaultFeatureGate.Enabled(features.LocalStorageCapacityIsolation) {
			return 0
		}

		quantity, found := (*requests)[v1.ResourceEphemeralStorage]
		if !found {
			return 0
		}
		return quantity.Value()
	default:
		if helper.IsScalarResourceName(resource) {
			quantity, found := (*requests)[resource]
			if !found {
				return 0
			}
			return quantity.Value()
		}
	}
	return 0
}

func GetNonZeroQuantityForResource(name v1.ResourceName, requests v1.ResourceList) *resource.Quantity {
	switch name {
	case v1.ResourceCPU:
		// Override if un-set, but not if explicitly set to zero
		if _, found := requests[v1.ResourceCPU]; !found {
			return resource.NewMilliQuantity(DefaultMilliCPURequest, resource.DecimalSI)
		}
		return requests.Cpu()
	case v1.ResourceMemory:
		// Override if un-set, but not if explicitly set to zero
		if _, found := requests[v1.ResourceMemory]; !found {
			return resource.NewQuantity(DefaultMemoryRequest, resource.BinarySI)
		}
		return requests.Memory()
	case v1.ResourceEphemeralStorage:
		// if the local storage capacity isolation feature gate is disabled, pods request 0 disk.
		if !utilfeature.DefaultFeatureGate.Enabled(features.LocalStorageCapacityIsolation) {
			resource.NewQuantity(0, resource.BinarySI)
		}

		quantity, found := requests[v1.ResourceEphemeralStorage]
		if !found {
			resource.NewQuantity(0, resource.BinarySI)
		}
		return &quantity
	default:
		if helper.IsScalarResourceName(name) {
			quantity, found := requests[name]
			if !found {
				resource.NewQuantity(0, resource.DecimalSI)
			}
			return &quantity
		}
	}
	return resource.NewQuantity(0, resource.DecimalSI)
}
