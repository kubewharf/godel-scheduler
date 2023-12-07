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
	"context"
	"fmt"

	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/klog/v2"

	framework "github.com/kubewharf/godel-scheduler/pkg/framework/api"
	"github.com/kubewharf/godel-scheduler/pkg/scheduler/apis/config"
	"github.com/kubewharf/godel-scheduler/pkg/scheduler/apis/validation"
	"github.com/kubewharf/godel-scheduler/pkg/scheduler/framework/plugins/loadaware/estimator"
	podutil "github.com/kubewharf/godel-scheduler/pkg/util/pod"
)

const (
	LoadAwareName = "LoadAware"
)

// resourceToWeightMap contains resource name and weight.
type resourceToWeightMap map[v1.ResourceName]int64

// ResourceTypeNameToWeightMap contains resource name, resource type and weight.
type ResourceTypeNameToWeightMap map[podutil.PodResourceType]resourceToWeightMap

// LoadAware is a score plugin that favors nodes with low resource realtime utilization.
type LoadAware struct {
	handle    framework.SchedulerFrameworkHandle
	args      *config.LoadAwareArgs
	weightMap ResourceTypeNameToWeightMap
	estimator estimator.Estimator
}

var _ = framework.ScorePlugin(&LoadAware{})

var defaultLoadAwareArgs = config.LoadAwareArgs{
	Resources: []config.ResourceSpec{
		{
			Name:         string(v1.ResourceCPU),
			Weight:       1,
			ResourceType: podutil.BestEffortPod,
		},
		{
			Name:         string(v1.ResourceMemory),
			Weight:       1,
			ResourceType: podutil.BestEffortPod,
		},
	},
	Estimator: "defaultEstimator",
}

// Name returns name of the plugin. It is used in logs, etc.
func (la *LoadAware) Name() string {
	return LoadAwareName
}

// Score invoked at the Score extension point.
func (la *LoadAware) Score(ctx context.Context, state *framework.CycleState, pod *v1.Pod, nodeName string) (int64, *framework.Status) {
	nodeInfo, err := la.handle.SnapshotSharedLister().NodeInfos().Get(nodeName)
	if err != nil {
		return 0, framework.NewStatus(framework.Error, fmt.Sprintf("getting node %q from Snapshot: %v", nodeName, err))
	}

	resourceType, err := framework.GetPodResourceType(state)
	if err != nil {
		return 0, framework.NewStatus(framework.Error, fmt.Sprintf("failed to get resource type of pod (%v/%v) from state", pod.Namespace, pod.Name))
	}

	allocatable := framework.NewResource(nil)
	switch resourceType {
	case podutil.GuaranteedPod:
		allocatable = nodeInfo.GetGuaranteedAllocatable()
	case podutil.BestEffortPod:
		allocatable = nodeInfo.GetBestEffortAllocatable()
	}

	if allocatable.MilliCPU == 0 || allocatable.Memory == 0 {
		return 0, nil
	}

	resourceToWeight := la.weightMap[resourceType]
	if resourceToWeight == nil {
		return 0, nil
	}

	resourcesUsed, err := la.estimator.EstimatePod(pod)
	if err != nil {
		return 0, framework.NewStatus(framework.Error, fmt.Sprintf("failed to estimate pod (%v/%v)", pod.Namespace, pod.Name))
	}

	nodeResourcesUsed, err := la.estimator.EstimateNode(nodeInfo, resourceType)
	if err != nil {
		return 0, framework.NewStatus(framework.Error, fmt.Sprintf("failed to estimate node %v", nodeInfo.GetNodeName()))
	}

	resourcesUsed.AddResource(nodeResourcesUsed)

	score := loadAwareScore(resourceToWeight, resourcesUsed, allocatable)

	return score, nil
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

// ScoreExtensions of the Score plugin.
func (la *LoadAware) ScoreExtensions() framework.ScoreExtensions {
	return nil
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

// NewLoadAware initializes a new plugin and returns it.
func NewLoadAware(laArgs runtime.Object, h framework.SchedulerFrameworkHandle) (framework.Plugin, error) {
	args, ok := laArgs.(*config.LoadAwareArgs)
	if !ok {
		klog.InfoS(fmt.Sprintf("WARN: want args to be of type LoadAwareArgs, got %T", args))
		args = &defaultLoadAwareArgs
	}
	if err := validation.ValidateLoadAwareArgs(args); err != nil {
		return nil, err
	}

	estimator, err := estimator.NewEstimator(args, h)
	if err != nil {
		return nil, err
	}

	return &LoadAware{
		handle:    h,
		args:      args,
		weightMap: GenerateResourceTypeNameToWeightMap(args.Resources),
		estimator: estimator,
	}, nil
}
