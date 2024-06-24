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

package priority

import (
	"math"

	"k8s.io/apimachinery/pkg/runtime"

	framework "github.com/kubewharf/godel-scheduler/pkg/framework/api"
	"github.com/kubewharf/godel-scheduler/pkg/scheduler/framework/handle"
	podutil "github.com/kubewharf/godel-scheduler/pkg/util/pod"
)

const MinPrioritySumName = "MinPrioritySum"

type MinPrioritySum struct{}

var _ framework.CandidatesSortingPlugin = &MinPrioritySum{}

func NewMinPrioritySum(_ runtime.Object, _ handle.PodFrameworkHandle) (framework.Plugin, error) {
	return &MinPrioritySum{}, nil
}

func (mps *MinPrioritySum) Name() string {
	return MinPrioritySumName
}

func (mps *MinPrioritySum) Compare(c1, c2 *framework.Candidate) int {
	prioritySum1 := getPrioritySum(c1)
	prioritySum2 := getPrioritySum(c2)
	if prioritySum1 < prioritySum2 {
		return 1
	} else if prioritySum1 > prioritySum2 {
		return -1
	} else {
		return 0
	}
}

func getPrioritySum(c *framework.Candidate) int64 {
	var sumPriorities int64 = 0
	for _, pod := range c.Victims.Pods {
		// We add MaxInt32+1 to all priorities to make all of them >= 0. This is
		// needed so that a node with a few pods with negative priority is not
		// picked over a node with a smaller number of pods with the same negative
		// priority (and similar scenarios).
		sumPriorities += int64(podutil.GetPodPriority(pod)) + int64(math.MaxInt32+1)
	}
	return sumPriorities
}
