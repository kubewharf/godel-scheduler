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
	"sync/atomic"

	framework "github.com/kubewharf/godel-scheduler/pkg/framework/api"
	"github.com/kubewharf/godel-scheduler/pkg/plugins/helper"
	"github.com/kubewharf/godel-scheduler/pkg/plugins/podlauncher"
	utils "github.com/kubewharf/godel-scheduler/pkg/plugins/podtopologyspread"
	"github.com/kubewharf/godel-scheduler/pkg/util/parallelize"
	podutil "github.com/kubewharf/godel-scheduler/pkg/util/pod"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/klog/v2"
)

const preFilterStateKey = "PreFilter" + Name

// preFilterState computed at PreFilter and used at Filter.
// It combines TpKeyToCriticalPaths and TpPairToMatchNum to represent:
// (1) critical paths where the least pods are matched on each spread constraint.
// (2) number of pods matched on each spread constraint.
// A nil preFilterState denotes it's not set at all (in PreFilter phase);
// An empty preFilterState object denotes it's a legit state and is set in PreFilter phase.
// Fields are exported for comparison during testing.
type preFilterState struct {
	Constraints []utils.TopologySpreadConstraint
	// We record 2 critical paths instead of all critical paths here.
	// criticalPaths[0].MatchNum always holds the minimum matching number.
	// criticalPaths[1].MatchNum is always greater or equal to criticalPaths[0].MatchNum, but
	// it's not guaranteed to be the 2nd minimum match number.
	TpKeyToCriticalPaths map[string]*utils.CriticalPaths
	// TpPairToMatchNum is keyed with topologyPair, and valued with the number of matching pods.
	TpPairToMatchNum map[utils.TopologyPair]*int32
}

// Clone makes a copy of the given state.
func (s *preFilterState) Clone() framework.StateData {
	if s == nil {
		return nil
	}
	copy := preFilterState{
		// Constraints are shared because they don't change.
		Constraints:          s.Constraints,
		TpKeyToCriticalPaths: make(map[string]*utils.CriticalPaths, len(s.TpKeyToCriticalPaths)),
		TpPairToMatchNum:     make(map[utils.TopologyPair]*int32, len(s.TpPairToMatchNum)),
	}
	for tpKey, paths := range s.TpKeyToCriticalPaths {
		copy.TpKeyToCriticalPaths[tpKey] = &utils.CriticalPaths{paths[0], paths[1]}
	}
	for tpPair, matchNum := range s.TpPairToMatchNum {
		copyPair := utils.TopologyPair{Key: tpPair.Key, Value: tpPair.Value}
		copyCount := *matchNum
		copy.TpPairToMatchNum[copyPair] = &copyCount
	}
	return &copy
}

func (s *preFilterState) updateWithPod(updatedPod, preemptorPod *v1.Pod, nodeInfo framework.NodeInfo, delta int32) {
	if s == nil || updatedPod.Namespace != preemptorPod.Namespace || nodeInfo == nil {
		return
	}
	podLauncher, _ := podutil.GetPodLauncher(updatedPod)
	nodeLabels := nodeInfo.GetNodeLabels(podLauncher)

	if !utils.NodeLabelsMatchSpreadConstraints(nodeLabels, s.Constraints) {
		return
	}

	podLabelSet := labels.Set(updatedPod.Labels)
	for _, constraint := range s.Constraints {
		if !constraint.Selector.Matches(podLabelSet) {
			continue
		}

		k, v := constraint.TopologyKey, nodeLabels[constraint.TopologyKey]
		pair := utils.TopologyPair{Key: k, Value: v}
		*s.TpPairToMatchNum[pair] += delta

		s.TpKeyToCriticalPaths[k].Update(v, *s.TpPairToMatchNum[pair])
	}
}

// PreFilter invoked at the prefilter extension point.
func (pl *PodTopologySpread) PreFilter(ctx context.Context, cycleState *framework.CycleState, pod *v1.Pod) *framework.Status {
	s, err := pl.calPreFilterState(pod)
	if err != nil {
		return framework.NewStatus(framework.Error, err.Error())
	}
	cycleState.Write(preFilterStateKey, s)
	return nil
}

// PreFilterExtensions returns prefilter extensions, pod add and remove.
func (pl *PodTopologySpread) PreFilterExtensions() framework.PreFilterExtensions {
	return pl
}

// AddPod from pre-computed data in cycleState.
func (pl *PodTopologySpread) AddPod(ctx context.Context, cycleState *framework.CycleState, podToSchedule *v1.Pod, podToAdd *v1.Pod, nodeInfo framework.NodeInfo) *framework.Status {
	s, err := getPreFilterState(cycleState)
	if err != nil {
		return framework.NewStatus(framework.Error, err.Error())
	}

	s.updateWithPod(podToAdd, podToSchedule, nodeInfo, 1)
	return nil
}

// RemovePod from pre-computed data in cycleState.
func (pl *PodTopologySpread) RemovePod(ctx context.Context, cycleState *framework.CycleState, podToSchedule *v1.Pod, podToRemove *v1.Pod, nodeInfo framework.NodeInfo) *framework.Status {
	s, err := getPreFilterState(cycleState)
	if err != nil {
		return framework.NewStatus(framework.Error, err.Error())
	}

	s.updateWithPod(podToRemove, podToSchedule, nodeInfo, -1)
	return nil
}

// getPreFilterState fetches a pre-computed preFilterState.
func getPreFilterState(cycleState *framework.CycleState) (*preFilterState, error) {
	c, err := cycleState.Read(preFilterStateKey)
	if err != nil {
		// preFilterState doesn't exist, likely PreFilter wasn't invoked.
		return nil, fmt.Errorf("error reading %q from cycleState: %v", preFilterStateKey, err)
	}

	s, ok := c.(*preFilterState)
	if !ok {
		return nil, fmt.Errorf("%+v convert to podtopologyspread.preFilterState error", c)
	}
	return s, nil
}

// calPreFilterState computes preFilterState describing how pods are spread on topologies.
func (pl *PodTopologySpread) calPreFilterState(pod *v1.Pod) (*preFilterState, error) {
	podLauncher, err := podutil.GetPodLauncher(pod)
	if err != nil {
		return nil, fmt.Errorf("getting pod launcher: %v", err)
	}

	allNodes := pl.sharedLister.NodeInfos().List()
	var constraints []utils.TopologySpreadConstraint
	if len(pod.Spec.TopologySpreadConstraints) > 0 {
		// We have feature gating in APIServer to strip the spec
		// so don't need to re-check feature gate, just check length of Constraints.
		constraints, err = utils.FilterTopologySpreadConstraints(pod.Spec.TopologySpreadConstraints, v1.DoNotSchedule)
		if err != nil {
			return nil, fmt.Errorf("obtaining pod's hard topology spread constraints: %v", err)
		}
	} else {
		constraints, err = pl.defaultConstraints(pod, v1.DoNotSchedule)
		if err != nil {
			return nil, fmt.Errorf("setting default hard topology spread constraints: %v", err)
		}
	}
	if len(constraints) == 0 {
		return &preFilterState{}, nil
	}

	s := preFilterState{
		Constraints:          constraints,
		TpKeyToCriticalPaths: make(map[string]*utils.CriticalPaths, len(constraints)),
		TpPairToMatchNum:     make(map[utils.TopologyPair]*int32, utils.SizeHeuristic(len(allNodes), constraints)),
	}
	for _, nodeInfo := range allNodes {
		// In accordance to design, if NodeAffinity or NodeSelector is defined,
		// spreading is applied to nodes that pass those filters.
		if !helper.PodMatchesNodeSelectorAndAffinityTerms(pod, nodeInfo) {
			continue
		}
		nodeLabels := nodeInfo.GetNodeLabels(podLauncher)
		// Ensure current node's labels contains all topologyKeys in 'Constraints'.
		if !utils.NodeLabelsMatchSpreadConstraints(nodeLabels, constraints) {
			continue
		}
		for _, c := range constraints {
			pair := utils.TopologyPair{Key: c.TopologyKey, Value: nodeLabels[c.TopologyKey]}
			s.TpPairToMatchNum[pair] = new(int32)
		}
	}

	processNode := func(i int) {
		nodeInfo := allNodes[i]
		nodeLabels := allNodes[i].GetNodeLabels(podLauncher)

		for _, constraint := range constraints {
			pair := utils.TopologyPair{Key: constraint.TopologyKey, Value: nodeLabels[constraint.TopologyKey]}
			tpCount := s.TpPairToMatchNum[pair]
			if tpCount == nil {
				continue
			}
			count := utils.CountPodsMatchSelector(nodeInfo.GetPods(), constraint.Selector, pod.Namespace)
			atomic.AddInt32(tpCount, int32(count))
		}
	}
	parallelize.Until(context.Background(), len(allNodes), processNode)

	// calculate min match for each topology pair
	for i := 0; i < len(constraints); i++ {
		key := constraints[i].TopologyKey
		s.TpKeyToCriticalPaths[key] = utils.NewCriticalPaths()
	}
	for pair, num := range s.TpPairToMatchNum {
		s.TpKeyToCriticalPaths[pair.Key].Update(pair.Value, *num)
	}

	return &s, nil
}

// Filter invoked at the filter extension point.
func (pl *PodTopologySpread) Filter(ctx context.Context, cycleState *framework.CycleState, pod *v1.Pod, nodeInfo framework.NodeInfo) *framework.Status {
	podLauncher, status := podlauncher.NodeFits(cycleState, pod, nodeInfo)
	if status != nil {
		return status
	}
	nodeLabels := nodeInfo.GetNodeLabels(podLauncher)

	s, err := getPreFilterState(cycleState)
	if err != nil {
		return framework.NewStatus(framework.Error, err.Error())
	}

	// However, "empty" preFilterState is legit which tolerates every toSchedule Pod.
	if len(s.Constraints) == 0 {
		return nil
	}

	podLabelSet := labels.Set(pod.Labels)
	for _, c := range s.Constraints {
		tpKey := c.TopologyKey
		tpVal, ok := nodeLabels[c.TopologyKey]
		if !ok {
			klog.V(5).Infof("node '%s' doesn't have required label '%s'", nodeInfo.GetNodeName(), tpKey)
			return framework.NewStatus(framework.UnschedulableAndUnresolvable, ErrReasonNodeLabelNotMatch)
		}

		selfMatchNum := int32(0)
		if c.Selector.Matches(podLabelSet) {
			selfMatchNum = 1
		}

		pair := utils.TopologyPair{Key: tpKey, Value: tpVal}
		paths, ok := s.TpKeyToCriticalPaths[tpKey]
		if !ok {
			// error which should not happen
			klog.Errorf("internal error: get paths from key %q of %#v", tpKey, s.TpKeyToCriticalPaths)
			continue
		}
		// judging criteria:
		// 'existing matching num' + 'if self-match (1 or 0)' - 'global min matching num' <= 'maxSkew'
		minMatchNum := paths[0].MatchNum
		matchNum := int32(0)
		if tpCount := s.TpPairToMatchNum[pair]; tpCount != nil {
			matchNum = *tpCount
		}
		skew := matchNum + selfMatchNum - minMatchNum
		if skew > c.MaxSkew {
			klog.V(5).Infof("node '%s' failed spreadConstraint[%s]: MatchNum(%d) + selfMatchNum(%d) - minMatchNum(%d) > maxSkew(%d)", nodeInfo.GetNodeName(), tpKey, matchNum, selfMatchNum, minMatchNum, c.MaxSkew)
			return framework.NewStatus(framework.Unschedulable, ErrReasonConstraintsNotMatch)
		}
	}

	return nil
}
