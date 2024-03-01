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

package loadaware

import (
	v1 "k8s.io/api/core/v1"

	framework "github.com/kubewharf/godel-scheduler/pkg/framework/api"
	"github.com/kubewharf/godel-scheduler/pkg/scheduler/apis/config"
)

// The unused capacity is calculated on a scale of 0-MaxNodeScore
// 0 being the lowest priority and `MaxNodeScore` being the highest.
// The more unused resources the higher the score is.
func leastRequestedScore(requested, capacity int64) int64 {
	if capacity == 0 {
		return 0
	}
	if requested > capacity {
		return 0
	}

	return ((capacity - requested) * int64(framework.MaxNodeScore)) / capacity
}

func loadAwareScore(resourceToWeightMap resourceToWeightMap, resourceUsed, allocatable *framework.Resource) int64 {
	if len(resourceToWeightMap) == 0 || allocatable == nil {
		return 0
	}
	var nodeScore, weightSum int64
	for resourceName, weight := range resourceToWeightMap {
		var resourceScore int64 = 0
		switch resourceName {
		case v1.ResourceCPU:
			resourceScore = leastRequestedScore(resourceUsed.MilliCPU, allocatable.MilliCPU)
		case v1.ResourceMemory:
			resourceScore = leastRequestedScore(resourceUsed.Memory, allocatable.Memory)
		}
		nodeScore += resourceScore * weight
		weightSum += weight
	}
	if weightSum == 0 {
		return 0
	}
	return nodeScore / weightSum
}

func GenerateResourceTypeNameToWeightMap(resources []config.ResourceSpec) ResourceTypeNameToWeightMap {
	resTypeNameToWeightMap := make(ResourceTypeNameToWeightMap)
	for _, res := range resources {
		weightMap, ok := resTypeNameToWeightMap[res.ResourceType]
		if !ok {
			weightMap = make(resourceToWeightMap)
		}
		weightMap[v1.ResourceName(res.Name)] = res.Weight
		resTypeNameToWeightMap[res.ResourceType] = weightMap
	}
	return resTypeNameToWeightMap
}
