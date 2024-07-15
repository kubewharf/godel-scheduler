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

// LeastAllocated is a score plugin that favors nodes with fewer allocation requested resources based on requested resources.
type LeastAllocated struct {
	handle handle.PodFrameworkHandle
	resourceAllocationScorer
}

var _ = framework.ScorePlugin(&LeastAllocated{})

// LeastAllocatedName is the name of the plugin used in the plugin registry and configurations.
const LeastAllocatedName = "NodeResourcesLeastAllocated"

// Name returns name of the plugin. It is used in logs, etc.
func (la *LeastAllocated) Name() string {
	return LeastAllocatedName
}

// Score invoked at the score extension point.
func (la *LeastAllocated) Score(ctx context.Context, state *framework.CycleState, pod *v1.Pod, nodeName string) (int64, *framework.Status) {
	nodeInfo, err := la.handle.SnapshotSharedLister().NodeInfos().Get(nodeName)
	if err != nil {
		return 0, framework.NewStatus(framework.Error, fmt.Sprintf("getting node %q from Snapshot: %v", nodeName, err))
	}

	// la.score favors nodes with fewer requested resources.
	// It calculates the percentage of memory and CPU requested by pods scheduled on the node, and
	// prioritizes based on the minimum of the average of the fraction of requested to capacity.
	//
	// Details:
	// (cpu((capacity-sum(requested))*MaxNodeScore/capacity) + memory((capacity-sum(requested))*MaxNodeScore/capacity))/weightSum
	return la.score(state, pod, nodeInfo)
}

// ScoreExtensions of the Score plugin.
func (la *LeastAllocated) ScoreExtensions() framework.ScoreExtensions {
	return nil
}

// NewLeastAllocated initializes a new plugin and returns it.
func NewLeastAllocated(laArgs runtime.Object, h handle.PodFrameworkHandle) (framework.Plugin, error) {
	var resToWeightMap resourceToWeightMap
	args, ok := laArgs.(*config.NodeResourcesLeastAllocatedArgs)
	if ok {
		if err := validation.ValidateNodeResourcesLeastAllocatedArgs(args); err != nil {
			return nil, err
		}
		resToWeightMap = make(resourceToWeightMap)
		for _, resource := range (*args).Resources {
			resToWeightMap[v1.ResourceName(resource.Name)] = resource.Weight
		}
	} else {
		klog.InfoS("WARN: Got unexpected types of plugin args while wanted the type NodeResourcesLeastAllocatedArgs")
		resToWeightMap = defaultRequestedRatioResources
	}
	weightSum := calcWeightSum(resToWeightMap)
	if weightSum == 0 {
		return nil, fmt.Errorf("want the sum of resources weight to be non-zero, got 0")
	}

	return &LeastAllocated{
		handle: h,
		resourceAllocationScorer: resourceAllocationScorer{
			Name:                LeastAllocatedName,
			scorer:              leastResourceScorer(resToWeightMap, weightSum),
			resourceToWeightMap: resToWeightMap,
		},
	}, nil
}

func leastResourceScorer(resToWeightMap resourceToWeightMap, weightSum int64) scoreFunc {
	return func(state *framework.CycleState, requested, allocatable resourceToValueMap, includeVolumes bool, requestedVolumes int, allocatableVolumes int) int64 {
		var nodeScore int64
		for resource, weight := range resToWeightMap {
			resourceScore := leastRequestedScore(requested[resource], allocatable[resource])
			nodeScore += resourceScore * weight
		}
		return nodeScore / weightSum
	}
}

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
