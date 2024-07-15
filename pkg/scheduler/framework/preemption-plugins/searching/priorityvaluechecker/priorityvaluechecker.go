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

package priorityvaluechecker

import (
	"fmt"

	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"

	framework "github.com/kubewharf/godel-scheduler/pkg/framework/api"
	"github.com/kubewharf/godel-scheduler/pkg/scheduler/framework/handle"
	podutil "github.com/kubewharf/godel-scheduler/pkg/util/pod"
)

const (
	PriorityValueCheckerName        = "PriorityValueChecker"
	clusterPrePreemptingPriorityKey = "ClusterPrePreempting-" + PriorityValueCheckerName
)

type PriorityValueChecker struct{}

var (
	_ framework.ClusterPrePreemptingPlugin = &PriorityValueChecker{}
	_ framework.VictimSearchingPlugin      = &PriorityValueChecker{}
)

// New initializes a new plugin and returns it.
func NewPriorityValueChecker(_ runtime.Object, handle handle.PodFrameworkHandle) (framework.Plugin, error) {
	checker := &PriorityValueChecker{}
	return checker, nil
}

func (pvc *PriorityValueChecker) Name() string {
	return PriorityValueCheckerName
}

func (pvc *PriorityValueChecker) ClusterPrePreempting(preemptor *v1.Pod, state, _ *framework.CycleState) *framework.Status {
	priority := podutil.GetPodPriority(preemptor)
	state.Write(clusterPrePreemptingPriorityKey, &priorityState{
		priority: priority,
	})
	return nil
}

func (pvc *PriorityValueChecker) VictimSearching(preemptor *v1.Pod, podInfo *framework.PodInfo, state, _ *framework.CycleState, _ *framework.VictimState) (framework.Code, string) {
	s, err := getPriorityPreparePreemptionState(state)
	if err != nil {
		return framework.Error, err.Error()
	}
	preemptorPriority := s.priority
	podPriority := podInfo.PodPriority
	// only get pods in lower priority
	if podPriority >= int64(preemptorPriority) {
		return framework.PreemptionFail, "pod with lower priority could not preempt pod with higher priority"
	} else {
		return framework.PreemptionSucceed, ""
	}
}

type priorityState struct {
	priority int32
}

func (state *priorityState) Clone() framework.StateData {
	return state
}

func getPriorityPreparePreemptionState(state *framework.CycleState) (*priorityState, error) {
	c, err := state.Read(clusterPrePreemptingPriorityKey)
	if err != nil {
		// preFilterState doesn't exist, likely PreFilter wasn't invoked.
		return nil, fmt.Errorf("error reading %q from cycleState: %v", clusterPrePreemptingPriorityKey, err)
	}

	s, ok := c.(*priorityState)
	if !ok {
		return nil, fmt.Errorf("%+v convert to PriorityLimitation.preparePreemptionState error", c)
	}
	return s, nil
}
