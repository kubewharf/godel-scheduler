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
	"context"
	"fmt"

	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"

	framework "github.com/kubewharf/godel-scheduler/pkg/framework/api"
	"github.com/kubewharf/godel-scheduler/pkg/scheduler/framework/handle"
	podutil "github.com/kubewharf/godel-scheduler/pkg/util/pod"
)

type AdaptiveCpuToMemRatio struct {
	handle              handle.PodFrameworkHandle
	resourceToWeightMap resourceToWeightMap
}

var _ = framework.ScorePlugin(&AdaptiveCpuToMemRatio{})

// AdaptiveCpuToMemRatioName is the name of the plugin used in the plugin registry and configurations.
const AdaptiveCpuToMemRatioName = "AdaptiveCpuToMemRatio"

// Name returns name of the plugin. It is used in logs, etc.
func (pl *AdaptiveCpuToMemRatio) Name() string {
	return AdaptiveCpuToMemRatioName
}

// NewAdaptiveCpuToMemRatio initializes a new plugin and returns it.
func NewAdaptiveCpuToMemRatio(_ runtime.Object, h handle.PodFrameworkHandle) (framework.Plugin, error) {
	return &AdaptiveCpuToMemRatio{
		handle:              h,
		resourceToWeightMap: defaultRequestedRatioResources,
	}, nil
}

// Score invoked at the score extension point.
func (pl *AdaptiveCpuToMemRatio) Score(ctx context.Context, state *framework.CycleState, pod *v1.Pod, nodeName string) (int64, *framework.Status) {
	nodeInfo, err := pl.handle.SnapshotSharedLister().NodeInfos().Get(nodeName)
	if err != nil {
		return 0, framework.NewStatus(framework.Error, fmt.Sprintf("getting node %q from Snapshot: %v", nodeName, err))
	}

	remain := make(resourceToValueMap, len(pl.resourceToWeightMap))
	podRequest := make(resourceToValueMap, len(pl.resourceToWeightMap))

	switch podResourceType, _ := framework.GetPodResourceType(state); podResourceType {
	case podutil.GuaranteedPod:
		for resource := range pl.resourceToWeightMap {
			if resource == v1.ResourceCPU {
				remain[resource] = nodeInfo.GetGuaranteedAllocatable().MilliCPU - nodeInfo.GetGuaranteedNonZeroRequested().MilliCPU
			} else if resource == v1.ResourceMemory {
				remain[resource] = nodeInfo.GetGuaranteedAllocatable().Memory - nodeInfo.GetGuaranteedNonZeroRequested().Memory
			}
			podRequest[resource] = calculatePodResourceRequest(pod, resource)
		}
	case podutil.BestEffortPod:
		for resource := range pl.resourceToWeightMap {
			if resource == v1.ResourceCPU {
				remain[resource] = nodeInfo.GetBestEffortAllocatable().MilliCPU - nodeInfo.GetBestEffortNonZeroRequested().MilliCPU
			} else if resource == v1.ResourceMemory {
				remain[resource] = nodeInfo.GetBestEffortAllocatable().Memory - nodeInfo.GetBestEffortNonZeroRequested().Memory
			}
			podRequest[resource] = calculatePodResourceRequest(pod, resource)
		}
	default:
		return 0, framework.NewStatus(framework.Error, "unsupported pod resource type")
	}

	return adaptiveCpuToMemRatioScorer(podRequest, remain), nil
}

func adaptiveCpuToMemRatioScorer(podRequest, remain resourceToValueMap) int64 {
	// if the node has 0 memory remained (thus the pod requests 0 memory), we recommend it
	if remain[v1.ResourceMemory] == 0 {
		return 100
	}
	// if pod requests 0 cpu, any of the nodes will be fine.
	if podRequest[v1.ResourceCPU] == 0 {
		return 100
	}
	// if pod requests 0 memory, any of the nodes will be fine. We don't care about the memory fragment at this moment.
	if podRequest[v1.ResourceMemory] == 0 {
		return 100
	}

	podCpuMemFraction := float64(podRequest[v1.ResourceCPU]) / float64(podRequest[v1.ResourceMemory])
	nodeCpuMemFraction := float64(remain[v1.ResourceCPU]) / float64(remain[v1.ResourceMemory])

	if nodeCpuMemFraction <= podCpuMemFraction {
		return int64(50.0*nodeCpuMemFraction/podCpuMemFraction + 50.0)
	} else {
		// Since cpu resource is in millicpus and memory resource is in bytes, we multiply the difference below by 1024*1024
		// so that the fraction difference is in Millicpu: MiB.
		return int64(50.0 / ((nodeCpuMemFraction-podCpuMemFraction)*1024*1024 + 1.0))
	}
}

// ScoreExtensions of the Score plugin.
func (pl *AdaptiveCpuToMemRatio) ScoreExtensions() framework.ScoreExtensions {
	return nil
}
