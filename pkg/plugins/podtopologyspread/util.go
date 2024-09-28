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
	"fmt"
	"math"

	framework "github.com/kubewharf/godel-scheduler/pkg/framework/api"
	"github.com/kubewharf/godel-scheduler/pkg/scheduler/apis/config"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
)

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

func CountPodsMatchSelector(podInfos []*framework.PodInfo, selector labels.Selector, ns string) int {
	count := 0
	for _, p := range podInfos {
		// Bypass terminating Pod (see #87621).
		if p.Pod.DeletionTimestamp != nil || p.Pod.Namespace != ns {
			continue
		}
		if selector.Matches(labels.Set(p.Pod.Labels)) {
			count++
		}
	}
	return count
}
