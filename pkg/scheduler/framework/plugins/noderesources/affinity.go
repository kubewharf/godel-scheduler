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
	"math"

	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/klog/v2"

	framework "github.com/kubewharf/godel-scheduler/pkg/framework/api"
	"github.com/kubewharf/godel-scheduler/pkg/scheduler/apis/config"
	"github.com/kubewharf/godel-scheduler/pkg/scheduler/apis/validation"
	"github.com/kubewharf/godel-scheduler/pkg/scheduler/framework/handle"
	"github.com/kubewharf/godel-scheduler/pkg/util"
	podutil "github.com/kubewharf/godel-scheduler/pkg/util/pod"
)

// NodeResourcesAffinity is a score plugin that favors nodes affinity
// with the resource request of pod get high score, vice versa.
type NodeResourcesAffinity struct {
	handle handle.PodFrameworkHandle
	resourceAllocationScorer
}

var _ = framework.ScorePlugin(&NodeResourcesAffinity{})

const (
	// NodeResourcesAffinityName is the name of the plugin used in the plugin registry and configurations.
	NodeResourcesAffinityName             = "NodeResourcesAffinity"
	NodeResourcesAffinityPreScoreStateKey = "PreScore" + NodeResourcesAffinityName
)

// Name returns name of the plugin. It is used in logs, etc.
func (a *NodeResourcesAffinity) Name() string {
	return NodeResourcesAffinityName
}

type nodeResourcesAffinityPreScoreState struct {
	hasResourceRequest sets.String
}

func (a *nodeResourcesAffinityPreScoreState) Clone() framework.StateData {
	return a
}

// PreScore builds and writes cycle state used by Score and NormalizeScore.
func (a *NodeResourcesAffinity) PreScore(_ context.Context, cycleState *framework.CycleState, pod *v1.Pod, nodes []framework.NodeInfo) *framework.Status {
	if len(nodes) == 0 {
		return nil
	}

	hasRequest := sets.NewString()
	requests := podutil.GetPodRequests(pod)
	for name := range a.resourceToWeightMap {
		sName := name.String()
		r, ok := requests[sName]
		if ok && !r.IsZero() {
			hasRequest.Insert(sName)
		}
	}

	cycleState.Write(NodeResourcesAffinityPreScoreStateKey, &nodeResourcesAffinityPreScoreState{hasResourceRequest: hasRequest})
	return nil
}

func getNodeResourcesAffinityPreScoreState(cycleState *framework.CycleState) (*nodeResourcesAffinityPreScoreState, error) {
	c, err := cycleState.Read(NodeResourcesAffinityPreScoreStateKey)
	if err != nil {
		return nil, fmt.Errorf("error reading %q from cycleState: %v", NodeResourcesAffinityPreScoreStateKey, err)
	}

	s, ok := c.(*nodeResourcesAffinityPreScoreState)
	if !ok {
		return nil, fmt.Errorf("%+v convert to tainttoleration.preScoreState error", c)
	}
	return s, nil
}

// Score invoked at the score extension point.
func (a *NodeResourcesAffinity) Score(_ context.Context, state *framework.CycleState, pod *v1.Pod, nodeName string) (int64, *framework.Status) {
	nodeInfo, err := a.handle.SnapshotSharedLister().NodeInfos().Get(nodeName)
	if err != nil {
		return 0, framework.NewStatus(framework.Error, fmt.Sprintf("getting node %q from Snapshot: %v", nodeName, err))
	}

	return a.score(state, pod, nodeInfo)
}

// ScoreExtensions of the Score plugin.
func (a *NodeResourcesAffinity) ScoreExtensions() framework.ScoreExtensions {
	return a
}

func (a *NodeResourcesAffinity) NormalizeScore(ctx context.Context, state *framework.CycleState, p *v1.Pod, scores framework.NodeScoreList) *framework.Status {
	return normalizeScore(framework.MaxNodeScore, scores)
}

var defaultAffinityResources = map[v1.ResourceName]int64{
	util.ResourceSriov: 1,
}

// NewNodeResourcesAffinity initializes a new plugin and returns it.
func NewNodeResourcesAffinity(atArgs runtime.Object, h handle.PodFrameworkHandle) (framework.Plugin, error) {
	var resToWeightMap resourceToWeightMap
	args, ok := atArgs.(*config.NodeResourcesAffinityArgs)
	if ok {
		if err := validation.ValidateNodeResourcesAffinityArgs(args); err != nil {
			return nil, err
		}

		resToWeightMap = make(resourceToWeightMap)
		for _, resource := range (*args).Resources {
			resToWeightMap[v1.ResourceName(resource.Name)] = resource.Weight
		}
	} else {
		klog.InfoS(fmt.Sprintf("WARN: expected args to be of type NodeResourcesAffinityArgs, got %T", atArgs))
		resToWeightMap = defaultAffinityResources
	}

	weightSum := calcWeightSum(resToWeightMap)
	if weightSum == 0 {
		return nil, fmt.Errorf("want the sum of resources weight to be non-zero, got 0")
	}

	return &NodeResourcesAffinity{
		handle: h,
		resourceAllocationScorer: resourceAllocationScorer{
			Name:                NodeResourcesAffinityName,
			scorer:              nodeResourcesAffinityScorer(resToWeightMap, weightSum),
			resourceToWeightMap: resToWeightMap,
		},
	}, nil
}

func getAllocatableScore(allocatable resourceToValueMap) map[v1.ResourceName]int64 {
	resourceScores := make(map[v1.ResourceName]int64)
	for name, val := range allocatable {
		resourceScores[name] = 0
		if val > 0 {
			resourceScores[name] = framework.MaxNodeScore
		}
	}
	return resourceScores
}

func nodeResourcesAffinityScorer(resToWeightMap resourceToWeightMap, weightSum int64) scoreFunc {
	return func(state *framework.CycleState, requested, allocatable resourceToValueMap, includeVolumes bool, requestedVolumes int, allocatableVolumes int) int64 {
		score := int64(0)
		scoreState, err := getNodeResourcesAffinityPreScoreState(state)
		if err != nil {
			klog.ErrorS(err, "failed to get node resources affinity pre-score state")
			return 0
		}

		hasRequest := scoreState.hasResourceRequest
		allocatableScores := getAllocatableScore(allocatable)
		// For each resource r, if pod requested then resourceScore is 100 * weight[r], otherwise is 0.
		// Final score is weighted average of all resourceScores.
		for name, weight := range resToWeightMap {
			resourceScore := allocatableScores[name]
			if !hasRequest.Has(name.String()) {
				resourceScore = framework.MaxNodeScore - resourceScore
			}
			score += resourceScore * weight
		}
		return score / weightSum
	}
}

func normalizeScore(maxPriority int64, scores framework.NodeScoreList) *framework.Status {
	var maxValue, minValue int64
	maxValue, minValue = math.MinInt64, math.MaxInt64
	for i := range scores {
		if scores[i].Score > maxValue {
			maxValue = scores[i].Score
		}
		if scores[i].Score < minValue {
			minValue = scores[i].Score
		}
	}
	// This will not happen
	if maxValue == math.MinInt64 {
		return nil
	}

	interval := maxValue - minValue
	if interval == 0 {
		for i := range scores {
			scores[i].Score = maxPriority
		}
		return nil
	}

	for i := range scores {
		scores[i].Score -= minValue
		scores[i].Score = scores[i].Score * maxPriority / interval
	}
	return nil
}
