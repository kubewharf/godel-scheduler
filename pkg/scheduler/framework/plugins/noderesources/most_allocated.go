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

package noderesources

import (
	"context"
	"fmt"

	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/klog/v2"

	framework "github.com/kubewharf/godel-scheduler/pkg/framework/api"
	"github.com/kubewharf/godel-scheduler/pkg/scheduler/apis/config"
	"github.com/kubewharf/godel-scheduler/pkg/scheduler/apis/validation"
	"github.com/kubewharf/godel-scheduler/pkg/scheduler/framework/handle"
)

// MostAllocated is a score plugin that favors nodes with high allocation based on requested resources.
type MostAllocated struct {
	handle handle.PodFrameworkHandle
	resourceAllocationScorer
}

var _ = framework.ScorePlugin(&MostAllocated{})

// MostAllocatedName is the name of the plugin used in the plugin registry and configurations.
const MostAllocatedName = "NodeResourcesMostAllocated"

// Name returns name of the plugin. It is used in logs, etc.
func (ma *MostAllocated) Name() string {
	return MostAllocatedName
}

// Score invoked at the Score extension point.
func (ma *MostAllocated) Score(ctx context.Context, state *framework.CycleState, pod *v1.Pod, nodeName string) (int64, *framework.Status) {
	nodeInfo, err := ma.handle.SnapshotSharedLister().NodeInfos().Get(nodeName)
	nodeAndNMNodeAreNil := nodeInfo.ObjectIsNil()
	if err != nil || nodeAndNMNodeAreNil {
		return 0, framework.NewStatus(framework.Error, fmt.Sprintf("getting node %q from Snapshot: %v, node and nmNode are nil: %v", nodeName, err, nodeAndNMNodeAreNil))
	}

	// ma.score favors nodes with most requested resources.
	// It calculates the percentage of memory and CPU requested by pods scheduled on the node, and prioritizes
	// based on the maximum of the average of the fraction of requested to capacity.
	// Details: (cpu(MaxNodeScore * sum(requested) / capacity) + memory(MaxNodeScore * sum(requested) / capacity)) / weightSum
	return ma.score(state, pod, nodeInfo)
}

// ScoreExtensions of the Score plugin.
func (ma *MostAllocated) ScoreExtensions() framework.ScoreExtensions {
	return nil
}

// NewMostAllocated initializes a new plugin and returns it.
func NewMostAllocated(maArgs runtime.Object, h handle.PodFrameworkHandle) (framework.Plugin, error) {
	var resToWeightMap resourceToWeightMap
	args, ok := maArgs.(*config.NodeResourcesMostAllocatedArgs)
	if ok {
		if err := validation.ValidateNodeResourcesMostAllocatedArgs(args); err != nil {
			return nil, err
		}
		resToWeightMap = make(resourceToWeightMap)
		for _, resource := range (*args).Resources {
			resToWeightMap[v1.ResourceName(resource.Name)] = resource.Weight
		}
	} else {
		klog.InfoS("WARN: Got unexpected types of plugin args while wanted the type NodeResourcesMostAllocatedArgs")
		resToWeightMap = defaultRequestedRatioResources
	}
	weightSum := calcWeightSum(resToWeightMap)
	if weightSum == 0 {
		return nil, fmt.Errorf("want the sum of resources weight to be non-zero, got 0")
	}

	return &MostAllocated{
		handle: h,
		resourceAllocationScorer: resourceAllocationScorer{
			Name:                MostAllocatedName,
			scorer:              mostResourceScorer(resToWeightMap, weightSum),
			resourceToWeightMap: resToWeightMap,
		},
	}, nil
}

func mostResourceScorer(resToWeightMap resourceToWeightMap, weightSum int64) scoreFunc {
	return func(state *framework.CycleState, requested, allocatable resourceToValueMap, includeVolumes bool, requestedVolumes int, allocatableVolumes int) int64 {
		var nodeScore int64
		for resource, weight := range resToWeightMap {
			resourceScore := mostRequestedScore(requested[resource], allocatable[resource])
			nodeScore += resourceScore * weight
		}
		return (nodeScore / weightSum)
	}
}

// The used capacity is calculated on a scale of 0-MaxNodeScore (MaxNodeScore is
// constant with value set to 100).
// 0 being the lowest priority and 100 being the highest.
// The more resources are used the higher the score is. This function
// is almost a reversed version of noderesources.leastRequestedScore.
func mostRequestedScore(requested, capacity int64) int64 {
	if capacity == 0 {
		return 0
	}
	if requested > capacity {
		return 0
	}

	return (requested * framework.MaxNodeScore) / capacity
}
