/*
Copyright 2024 The Godel Scheduler Authors.

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
	"github.com/kubewharf/godel-scheduler/pkg/scheduler/apis/config"
	"github.com/kubewharf/godel-scheduler/pkg/util/parallelize"
	podutil "github.com/kubewharf/godel-scheduler/pkg/util/pod"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/klog/v2"
)

const (
	// ErrReasonConstraintsNotMatch is used for PodTopologySpread filter error.
	ErrReasonConstraintsNotMatch = "node(s) didn't match pod topology spread constraints"
	// ErrReasonNodeLabelNotMatch is used when the node doesn't hold the required label.
	ErrReasonNodeLabelNotMatch = ErrReasonConstraintsNotMatch + " (missing required label)"
)

// PreFilterState computed at PreFilter and used at Filter.
// It combines TpKeyToCriticalPaths and TpPairToMatchNum to represent:
// (1) critical paths where the least pods are matched on each spread constraint.
// (2) number of pods matched on each spread constraint.
// A nil PreFilterState denotes it's not set at all (in PreFilter phase);
// An empty PreFilterState object denotes it's a legit state and is set in PreFilter phase.
// Fields are exported for comparison during testing.
type PreFilterState struct {
	Constraints []TopologySpreadConstraint
	// We record 2 critical paths instead of all critical paths here.
	// criticalPaths[0].MatchNum always holds the minimum matching number.
	// criticalPaths[1].MatchNum is always greater or equal to criticalPaths[0].MatchNum, but
	// it's not guaranteed to be the 2nd minimum match number.
	TpKeyToCriticalPaths map[string]*CriticalPaths
	// TpPairToMatchNum is keyed with topologyPair, and valued with the number of matching pods.
	TpPairToMatchNum map[TopologyPair]*int32
}

// Clone makes a copy of the given state.
func (s *PreFilterState) Clone() framework.StateData {
	if s == nil {
		return nil
	}
	copy := PreFilterState{
		// Constraints are shared because they don't change.
		Constraints:          s.Constraints,
		TpKeyToCriticalPaths: make(map[string]*CriticalPaths, len(s.TpKeyToCriticalPaths)),
		TpPairToMatchNum:     make(map[TopologyPair]*int32, len(s.TpPairToMatchNum)),
	}
	for tpKey, paths := range s.TpKeyToCriticalPaths {
		copy.TpKeyToCriticalPaths[tpKey] = &CriticalPaths{paths[0], paths[1]}
	}
	for tpPair, matchNum := range s.TpPairToMatchNum {
		copyPair := TopologyPair{Key: tpPair.Key, Value: tpPair.Value}
		copyCount := *matchNum
		copy.TpPairToMatchNum[copyPair] = &copyCount
	}
	return &copy
}

func (s *PreFilterState) UpdateWithPod(updatedPod, preemptorPod *v1.Pod, nodeInfo framework.NodeInfo, delta int32) {
	if s == nil || updatedPod.Namespace != preemptorPod.Namespace || nodeInfo == nil {
		return
	}
	podLauncher, _ := podutil.GetPodLauncher(updatedPod)
	nodeLabels := nodeInfo.GetNodeLabels(podLauncher)

	if !NodeLabelsMatchSpreadConstraints(nodeLabels, s.Constraints) {
		return
	}

	podLabelSet := labels.Set(updatedPod.Labels)
	for _, constraint := range s.Constraints {
		if !constraint.Selector.Matches(podLabelSet) {
			continue
		}

		k, v := constraint.TopologyKey, nodeLabels[constraint.TopologyKey]
		pair := TopologyPair{Key: k, Value: v}
		*s.TpPairToMatchNum[pair] += delta

		s.TpKeyToCriticalPaths[k].Update(v, *s.TpPairToMatchNum[pair])
	}
}

type TopologyPair struct {
	Key   string
	Value string
}

// TopologySpreadConstraint is an internal version for v1.TopologySpreadConstraint
// and where the selector is parsed.
// Fields are exported for comparison during testing.
type TopologySpreadConstraint struct {
	MaxSkew     int32
	TopologyKey string
	Selector    labels.Selector
}

// CAVEAT: the reason that `[2]criticalPath` can work is based on the implementation of current
// preemption algorithm, in particular the following 2 facts:
// Fact 1: we only preempt pods on the same node, instead of pods on multiple nodes.
// Fact 2: each node is evaluated on a separate copy of the preFilterState during its preemption cycle.
// If we plan to turn to a more complex algorithm like "arbitrary pods on multiple nodes", this
// structure needs to be revisited.
// Fields are exported for comparison during testing.
type CriticalPaths [2]struct {
	// TopologyValue denotes the topology value mapping to topology key.
	TopologyValue string
	// MatchNum denotes the number of matching pods.
	MatchNum int32
}

func NewCriticalPaths() *CriticalPaths {
	return &CriticalPaths{{MatchNum: math.MaxInt32}, {MatchNum: math.MaxInt32}}
}

func (p *CriticalPaths) Sort() {
	if p[0].MatchNum == p[1].MatchNum && p[0].TopologyValue > p[1].TopologyValue {
		// Swap TopologyValue to make them sorted alphabetically.
		p[0].TopologyValue, p[1].TopologyValue = p[1].TopologyValue, p[0].TopologyValue
	}
}

func (p *CriticalPaths) Update(tpVal string, num int32) {
	// first verify if `tpVal` exists or not
	i := -1
	if tpVal == p[0].TopologyValue {
		i = 0
	} else if tpVal == p[1].TopologyValue {
		i = 1
	}

	if i >= 0 {
		// `tpVal` exists
		p[i].MatchNum = num
		if p[0].MatchNum > p[1].MatchNum {
			// swap paths[0] and paths[1]
			p[0], p[1] = p[1], p[0]
		}
	} else {
		// `tpVal` doesn't exist
		if num < p[0].MatchNum {
			// update paths[1] with paths[0]
			p[1] = p[0]
			// update paths[0]
			p[0].TopologyValue, p[0].MatchNum = tpVal, num
		} else if num < p[1].MatchNum {
			// update paths[1]
			p[1].TopologyValue, p[1].MatchNum = tpVal, num
		}
	}
}

func GetArgs(obj runtime.Object) (config.PodTopologySpreadArgs, error) {
	if obj == nil {
		return config.PodTopologySpreadArgs{}, nil
	}

	ptr, ok := obj.(*config.PodTopologySpreadArgs)
	if !ok {
		return config.PodTopologySpreadArgs{}, fmt.Errorf("want args to be of type PodTopologySpreadArgs, got %T", obj)
	}
	return *ptr, nil
}

func FilterTopologySpreadConstraints(constraints []v1.TopologySpreadConstraint, action v1.UnsatisfiableConstraintAction) ([]TopologySpreadConstraint, error) {
	var result []TopologySpreadConstraint
	for _, c := range constraints {
		if c.WhenUnsatisfiable == action {
			selector, err := metav1.LabelSelectorAsSelector(c.LabelSelector)
			if err != nil {
				return nil, err
			}
			result = append(result, TopologySpreadConstraint{
				MaxSkew:     c.MaxSkew,
				TopologyKey: c.TopologyKey,
				Selector:    selector,
			})
		}
	}
	return result, nil
}

func SizeHeuristic(nodes int, constraints []TopologySpreadConstraint) int {
	for _, c := range constraints {
		if c.TopologyKey == v1.LabelHostname {
			return nodes
		}
	}
	return 0
}

// NodeLabelsMatchSpreadConstraints checks if ALL topology keys in spread Constraints are present in node labels.
func NodeLabelsMatchSpreadConstraints(nodeLabels map[string]string, constraints []TopologySpreadConstraint) bool {
	for _, c := range constraints {
		if _, ok := nodeLabels[c.TopologyKey]; !ok {
			return false
		}
	}
	return true
}

func CountPodsMatchSelector(podInfos []*framework.PodInfo, selector labels.Selector, ns string, podLanucher podutil.PodLauncher) int {
	count := 0
	for _, p := range podInfos {
		// Bypass terminating Pod (see #87621).
		if p.Pod.DeletionTimestamp != nil || p.Pod.Namespace != ns || p.PodLauncher != podLanucher {
			continue
		}
		if selector.Matches(labels.Set(p.Pod.Labels)) {
			count++
		}
	}
	return count
}

func IsNodeNil(nodeInfo framework.NodeInfo, podLanucher podutil.PodLauncher) bool {
	if nodeInfo == nil {
		return true
	}

	switch podLanucher {
	case podutil.Kubelet:
		if nodeInfo.GetNode() != nil {
			return false
		}
	case podutil.NodeManager:
		if nodeInfo.GetNMNode() != nil {
			return false
		}
	}
	return true
}

func GetPreFilterState(pod *v1.Pod, allNodes []framework.NodeInfo, constraints []TopologySpreadConstraint) PreFilterState {
	state := PreFilterState{
		Constraints:          constraints,
		TpKeyToCriticalPaths: make(map[string]*CriticalPaths, len(constraints)),
		TpPairToMatchNum:     make(map[TopologyPair]*int32, SizeHeuristic(len(allNodes), constraints)),
	}
	for _, nodeInfo := range allNodes {
		for _, podLanucher := range podutil.PodLanucherTypes {
			if IsNodeNil(nodeInfo, podLanucher) {
				continue
			}

			// In accordance to design, if NodeAffinity or NodeSelector is defined,
			// spreading is applied to nodes that pass those filters.
			if !helper.PodMatchesNodeSelectorAndAffinityTerms(pod, nodeInfo, podLanucher) {
				continue
			}
			nodeLabels := nodeInfo.GetNodeLabels(podLanucher)
			// Ensure current node's labels contains all topologyKeys in 'Constraints'.
			if !NodeLabelsMatchSpreadConstraints(nodeLabels, constraints) {
				continue
			}
			for _, c := range constraints {
				pair := TopologyPair{Key: c.TopologyKey, Value: nodeLabels[c.TopologyKey]}
				state.TpPairToMatchNum[pair] = new(int32)
			}
		}
	}

	processNode := func(i int) {
		nodeInfo := allNodes[i]

		for _, podLanucher := range podutil.PodLanucherTypes {
			if IsNodeNil(nodeInfo, podLanucher) {
				continue
			}

			nodeLabels := nodeInfo.GetNodeLabels(podLanucher)
			for _, constraint := range constraints {
				pair := TopologyPair{Key: constraint.TopologyKey, Value: nodeLabels[constraint.TopologyKey]}
				tpCount := state.TpPairToMatchNum[pair]
				if state.TpPairToMatchNum[pair] != nil {
					count := CountPodsMatchSelector(nodeInfo.GetPods(), constraint.Selector, pod.Namespace, podLanucher)
					atomic.AddInt32(tpCount, int32(count))
				}
			}
		}
	}
	parallelize.Until(context.Background(), len(allNodes), processNode)

	// calculate min match for each topology pair
	for i := 0; i < len(constraints); i++ {
		key := constraints[i].TopologyKey
		state.TpKeyToCriticalPaths[key] = NewCriticalPaths()
	}
	for pair, num := range state.TpPairToMatchNum {
		state.TpKeyToCriticalPaths[pair.Key].Update(pair.Value, *num)
	}

	return state
}

func IsSatisfyPodTopologySpreadConstraints(s *PreFilterState, pod *v1.Pod, nodeInfo framework.NodeInfo, podLauncher podutil.PodLauncher) *framework.Status {
	nodeLabels := nodeInfo.GetNodeLabels(podLauncher)
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

		pair := TopologyPair{Key: tpKey, Value: tpVal}
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
