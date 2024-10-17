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

package podtopologyspread

import (
	"context"
	"fmt"
	"math"
	"sync/atomic"

	framework "github.com/kubewharf/godel-scheduler/pkg/framework/api"
	"github.com/kubewharf/godel-scheduler/pkg/plugins/helper"
	"github.com/kubewharf/godel-scheduler/pkg/plugins/podlauncher"
	utils "github.com/kubewharf/godel-scheduler/pkg/plugins/podtopologyspread"
	"github.com/kubewharf/godel-scheduler/pkg/util/parallelize"
	podutil "github.com/kubewharf/godel-scheduler/pkg/util/pod"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/sets"
)

const preScoreStateKey = "PreScore" + Name

// preScoreState computed at PreScore and used at Score.
// Fields are exported for comparison during testing.
type preScoreState struct {
	Constraints []utils.TopologySpreadConstraint
	// IgnoredNodes records a set of node names which match the  scheduling pod's launcher type but miss some Constraints[*].topologyKey.
	// It also records the node names that don't match the scheduling pod's launcher type
	IgnoredNodes sets.String
	// TopologyPairToPodCounts is keyed with topologyPair, and valued with the number of matching pods.
	TopologyPairToPodCounts map[utils.TopologyPair]*int64
	// TopologyNormalizingWeight is the weight we give to the counts per topology.
	// This allows the pod counts of smaller topologies to not be watered down by
	// bigger ones.
	TopologyNormalizingWeight []float64
}

// Clone implements the mandatory Clone interface. We don't really copy the data since
// there is no need for that.
func (s *preScoreState) Clone() framework.StateData {
	return s
}

// initPreScoreState iterates "filteredNodes" to filter out the nodes which
// don't have required topologyKey(s), and initialize:
// 1) s.TopologyPairToPodCounts: keyed with both eligible topology pair and node names.
// 2) s.IgnoredNodes: the set of nodes that shouldn't be scored.
// 3) s.TopologyNormalizingWeight: The weight to be given to each constraint based on the number of values in a topology.
func (pl *PodTopologySpread) initPreScoreState(s *preScoreState, pod *v1.Pod, filteredNodes []framework.NodeInfo) error {
	var err error
	if len(pod.Spec.TopologySpreadConstraints) > 0 {
		s.Constraints, err = utils.FilterTopologySpreadConstraints(pod.Spec.TopologySpreadConstraints, v1.ScheduleAnyway)
		if err != nil {
			return fmt.Errorf("obtaining pod's soft topology spread constraints: %v", err)
		}
	} else {
		s.Constraints, err = pl.defaultConstraints(pod, v1.ScheduleAnyway)
		if err != nil {
			return fmt.Errorf("setting default soft topology spread constraints: %v", err)
		}
	}
	if len(s.Constraints) == 0 {
		return nil
	}

	scheduledPodLauncher, err := podutil.GetPodLauncher(pod)
	if err != nil {
		return err
	}

	topoSize := make([]int, len(s.Constraints))
	for _, nodeInfo := range filteredNodes {
		for _, podLanucher := range podutil.PodLanucherTypes {
			if utils.IsNodeNil(nodeInfo, podLanucher) {
				continue
			}

			nodeLabels := nodeInfo.GetNodeLabels(podLanucher)
			if !utils.NodeLabelsMatchSpreadConstraints(nodeLabels, s.Constraints) {
				// Nodes which don't have all required topologyKeys present are ignored
				// when scoring later.
				if podLanucher == scheduledPodLauncher {
					s.IgnoredNodes.Insert(getNodeNameByPodLauncher(nodeInfo, podLanucher))
				}
				continue
			}
			for i, constraint := range s.Constraints {
				// per-node counts are calculated during Score.
				if constraint.TopologyKey == v1.LabelHostname {
					continue
				}
				pair := utils.TopologyPair{Key: constraint.TopologyKey, Value: nodeLabels[constraint.TopologyKey]}
				if s.TopologyPairToPodCounts[pair] == nil {
					s.TopologyPairToPodCounts[pair] = new(int64)
					topoSize[i]++
				}
			}
		}
		if utils.IsNodeNil(nodeInfo, scheduledPodLauncher) {
			s.IgnoredNodes.Insert(nodeInfo.GetNodeName())
		}
	}

	s.TopologyNormalizingWeight = make([]float64, len(s.Constraints))
	for i, c := range s.Constraints {
		sz := topoSize[i]
		if c.TopologyKey == v1.LabelHostname {
			sz = len(filteredNodes) - len(s.IgnoredNodes)
		}
		s.TopologyNormalizingWeight[i] = topologyNormalizingWeight(sz)
	}
	return nil
}

// PreScore builds and writes cycle state used by Score and NormalizeScore.
func (pl *PodTopologySpread) PreScore(
	ctx context.Context,
	cycleState *framework.CycleState,
	pod *v1.Pod,
	filteredNodes []framework.NodeInfo,
) *framework.Status {
	allNodes := pl.sharedLister.NodeInfos().List()

	if len(filteredNodes) == 0 || len(allNodes) == 0 {
		// No nodes to score.
		return nil
	}

	state := &preScoreState{
		IgnoredNodes:            sets.NewString(),
		TopologyPairToPodCounts: make(map[utils.TopologyPair]*int64),
	}
	err := pl.initPreScoreState(state, pod, filteredNodes)
	if err != nil {
		return framework.NewStatus(framework.Error, fmt.Sprintf("error when calculating preScoreState: %v", err))
	}

	// return if incoming pod doesn't have soft topology spread Constraints.
	if len(state.Constraints) == 0 {
		cycleState.Write(preScoreStateKey, state)
		return nil
	}

	processAllNode := func(i int) {
		nodeInfo := allNodes[i]
		for _, podLanucher := range podutil.PodLanucherTypes {
			if utils.IsNodeNil(nodeInfo, podLanucher) {
				continue
			}

			nodeLabels := nodeInfo.GetNodeLabels(podLanucher)
			// (1) `node` should satisfy incoming pod's NodeSelector/NodeAffinity
			// (2) All topologyKeys need to be present in `node`
			if !helper.PodMatchesNodeSelectorAndAffinityTerms(pod, nodeInfo, podLanucher) ||
				!utils.NodeLabelsMatchSpreadConstraints(nodeLabels, state.Constraints) {
				return
			}

			for _, c := range state.Constraints {
				pair := utils.TopologyPair{Key: c.TopologyKey, Value: nodeLabels[c.TopologyKey]}
				// If current topology pair is not associated with any candidate node,
				// continue to avoid unnecessary calculation.
				// Per-node counts are also skipped, as they are done during Score.
				tpCount := state.TopologyPairToPodCounts[pair]
				if tpCount == nil {
					continue
				}
				count := utils.CountPodsMatchSelector(nodeInfo.GetPods(), c.Selector, pod.Namespace, podLanucher)
				atomic.AddInt64(tpCount, int64(count))
			}
		}
	}
	parallelize.Until(ctx, len(allNodes), processAllNode)

	cycleState.Write(preScoreStateKey, state)
	return nil
}

// Score invoked at the Score extension point.
// The "score" returned in this function is the matching number of pods on the `nodeName`,
// it is normalized later.
func (pl *PodTopologySpread) Score(ctx context.Context, cycleState *framework.CycleState, pod *v1.Pod, nodeName string) (int64, *framework.Status) {
	nodeInfo, err := pl.sharedLister.NodeInfos().Get(nodeName)
	if err != nil {
		return 0, framework.NewStatus(framework.Error, fmt.Sprintf("getting node %q from Snapshot: %v,", nodeName, err))
	}
	podLauncher, status := podlauncher.NodeFits(cycleState, pod, nodeInfo)
	if status != nil {
		return 0, nil
	}

	nodeLabels := nodeInfo.GetNodeLabels(podLauncher)

	s, err := getPreScoreState(cycleState)
	if err != nil {
		return 0, framework.NewStatus(framework.Error, err.Error())
	}

	// Return if the node is not qualified.
	if s.IgnoredNodes.Has(getNodeNameByPodLauncher(nodeInfo, podLauncher)) {
		return 0, nil
	}

	// For each present <pair>, current node gets a credit of <matchSum>.
	// And we sum up <matchSum> and return it as this node's score.
	var score float64
	for i, c := range s.Constraints {
		if tpVal, ok := nodeLabels[c.TopologyKey]; ok {
			var cnt int64
			if c.TopologyKey == v1.LabelHostname {
				cnt = int64(utils.CountPodsMatchSelector(nodeInfo.GetPods(), c.Selector, pod.Namespace, podLauncher))
			} else {
				pair := utils.TopologyPair{Key: c.TopologyKey, Value: tpVal}
				cnt = *s.TopologyPairToPodCounts[pair]
			}
			score += scoreForCount(cnt, c.MaxSkew, s.TopologyNormalizingWeight[i])
		}
	}
	return int64(score), nil
}

// NormalizeScore invoked after scoring all nodes.
func (pl *PodTopologySpread) NormalizeScore(ctx context.Context, cycleState *framework.CycleState, pod *v1.Pod, scores framework.NodeScoreList) *framework.Status {
	s, err := getPreScoreState(cycleState)
	if err != nil {
		return framework.NewStatus(framework.Error, err.Error())
	}
	if s == nil {
		return nil
	}

	// Calculate <minScore> and <maxScore>
	var minScore int64 = math.MaxInt64
	var maxScore int64
	for _, score := range scores {
		// it's mandatory to check if <score.Name> is present in m.IgnoredNodes
		if s.IgnoredNodes.Has(score.Name) {
			continue
		}
		if score.Score < minScore {
			minScore = score.Score
		}
		if score.Score > maxScore {
			maxScore = score.Score
		}
	}

	for i := range scores {
		nodeInfo, err := pl.sharedLister.NodeInfos().Get(scores[i].Name)
		if err != nil {
			return framework.NewStatus(framework.Error, err.Error())
		}

		if s.IgnoredNodes.Has(nodeInfo.GetNodeName()) {
			scores[i].Score = 0
			continue
		}

		if maxScore == 0 {
			scores[i].Score = framework.MaxNodeScore
			continue
		}

		s := scores[i].Score
		scores[i].Score = framework.MaxNodeScore * (maxScore + minScore - s) / maxScore
	}
	return nil
}

// ScoreExtensions of the Score plugin.
func (pl *PodTopologySpread) ScoreExtensions() framework.ScoreExtensions {
	return pl
}

func getPreScoreState(cycleState *framework.CycleState) (*preScoreState, error) {
	c, err := cycleState.Read(preScoreStateKey)
	if err != nil {
		return nil, fmt.Errorf("error reading %q from cycleState: %v", preScoreStateKey, err)
	}

	s, ok := c.(*preScoreState)
	if !ok {
		return nil, fmt.Errorf("%+v  convert to podtopologyspread.preScoreState error", c)
	}
	return s, nil
}

// topologyNormalizingWeight calculates the weight for the topology, based on
// the number of values that exist for a topology.
// Since <size> is at least 1 (all nodes that passed the Filters are in the
// same topology), and k8s supports 5k nodes, the result is in the interval
// <1.09, 8.52>.
//
// Note: <size> could also be zero when no nodes have the required topologies,
// however we don't care about topology weight in this case as we return a 0
// score for all nodes.
func topologyNormalizingWeight(size int) float64 {
	return math.Log(float64(size + 2))
}

// scoreForCount calculates the score based on number of matching pods in a
// topology domain, the constraint's maxSkew and the topology weight.
// `maxSkew-1` is added to the score so that differences between topology
// domains get watered down, controlling the tolerance of the score to skews.
func scoreForCount(cnt int64, maxSkew int32, tpWeight float64) float64 {
	return float64(cnt)*tpWeight + float64(maxSkew-1)
}
