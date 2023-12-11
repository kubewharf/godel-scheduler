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

package util

import (
	"fmt"
	"math"

	v1 "k8s.io/api/core/v1"
	"k8s.io/klog/v2"
)

type DRFResource struct {
	MilliCPU int64
	Memory   int64

	// TODO: GPU ?
}

// resource type
func (r *DRFResource) GetResourceValue(rName v1.ResourceName) int64 {
	switch rName {
	case v1.ResourceCPU:
		return r.MilliCPU
	case v1.ResourceMemory:
		return r.Memory
	default:
		klog.InfoS("Wrong resource name was provided which is not supported by DRF policy", "resourceName", rName)
		return 0
	}
}

// IsInit checks if the DRFResource has been initialized.
func (r DRFResource) IsInit() bool {
	return r != DRFResource{}
}

// SetSpecificResourceValue sets the resource value for that specific
// resource type.
func (r *DRFResource) SetResourceValue(rName v1.ResourceName, quantity int64) {
	switch rName {
	case v1.ResourceCPU:
		r.MilliCPU = quantity
	case v1.ResourceMemory:
		r.Memory = quantity
	default:
		klog.InfoS("Wrong resource name was provided which is not supported by DRF policy", "resourceName", rName)
	}
}

const ZERO = int64(0)

// IsEmpty returns bool after checking any of resource is less than min possible value
func (r *DRFResource) IsEmpty() bool {
	if r.MilliCPU <= ZERO && r.Memory <= ZERO {
		return true
	}

	return false
}

// IsZero checks whether that resource is less than min possible value
func (r *DRFResource) IsZero(rn v1.ResourceName) bool {
	switch rn {
	case v1.ResourceCPU:
		return r.MilliCPU <= ZERO
	case v1.ResourceMemory:
		return r.Memory <= ZERO
	default:
		klog.InfoS("Wrong resource name was provided which is not supported by DRF policy")
		return false
	}
}

// Add is used to add the two resources
func (r *DRFResource) AddResource(rr DRFResource) *DRFResource {
	r.MilliCPU += rr.MilliCPU
	r.Memory += rr.Memory

	return r
}

// Sub subtracts two Resource objects.
func (r *DRFResource) SubResource(rr DRFResource) *DRFResource {
	r.MilliCPU -= rr.MilliCPU
	r.Memory -= rr.Memory

	if r.Memory < 0 || r.MilliCPU < 0 {
		klog.InfoS("Got negative result for SubResource and returned empty resource")
		return EmptyResource()
	} else {
		return r
	}
}

// Multi multiples the resource with ratio provided
func (r *DRFResource) Multi(ratio float64) *DRFResource {
	r.MilliCPU = int64(float64(r.MilliCPU) * ratio)
	r.Memory = int64(float64(r.Memory) * ratio)

	return r
}

// EmptyResource creates an empty resource object and returns
func EmptyResource() *DRFResource {
	return &DRFResource{
		MilliCPU: 0,
		Memory:   0,
	}
}

func MaxResource() DRFResource {
	return DRFResource{
		MilliCPU: math.MaxInt64,
		Memory:   math.MaxInt64,
	}
}

// ResourceNames returns all resource types
func (r *DRFResource) ResourceNames() []v1.ResourceName {
	resNames := []v1.ResourceName{v1.ResourceCPU, v1.ResourceMemory}
	return resNames
}

// String returns resource details in string format
func (r *DRFResource) String() string {
	str := fmt.Sprintf("cpu %d, memory %d", r.MilliCPU, r.Memory)
	return str
}

// SetMaxResource compares with ResourceList and takes max value for each Resource.
func (r *DRFResource) SetMaxResource(rl v1.ResourceList) {
	if r == nil {
		return
	}

	for rName, rQuantity := range rl {
		switch rName {
		case v1.ResourceMemory:
			if mem := rQuantity.Value(); mem > r.Memory {
				r.Memory = mem
			}
		case v1.ResourceCPU:
			if cpu := rQuantity.MilliValue(); cpu > r.MilliCPU {
				r.MilliCPU = cpu
			}
		}
	}
}

// AddFromResourceList adds ResourceList into Resource.
func (r *DRFResource) AddFromResourceList(rl v1.ResourceList) {
	if r == nil {
		return
	}

	for rName, rQuant := range rl {
		switch rName {
		case v1.ResourceCPU:
			r.MilliCPU += rQuant.MilliValue()
		case v1.ResourceMemory:
			r.Memory += rQuant.Value()
		}
	}
}

func GetPodResourceRequest(pod *v1.Pod) *DRFResource {
	result := &DRFResource{}
	for _, container := range pod.Spec.Containers {
		result.AddFromResourceList(container.Resources.Requests)
	}

	// take max_resource(sum_pod, any_init_container)
	for _, container := range pod.Spec.InitContainers {
		result.SetMaxResource(container.Resources.Requests)
	}

	// If Overhead is being utilized, add to the total requests for the pod
	if pod.Spec.Overhead != nil {
		result.AddFromResourceList(pod.Spec.Overhead)
	}

	return result
}
