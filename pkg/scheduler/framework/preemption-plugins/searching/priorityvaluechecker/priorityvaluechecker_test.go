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
	"reflect"
	"testing"

	v1 "k8s.io/api/core/v1"

	framework "github.com/kubewharf/godel-scheduler/pkg/framework/api"
	testing_helper "github.com/kubewharf/godel-scheduler/pkg/testing-helper"
)

func TestPriorityValueChecker(t *testing.T) {
	tests := []struct {
		name           string
		preemptor      *v1.Pod
		pod            *v1.Pod
		expectedStatus *framework.Status
	}{
		{
			name:           "preemptor has larger priority",
			preemptor:      testing_helper.MakePod().Priority(100).Obj(),
			pod:            testing_helper.MakePod().Priority(99).Obj(),
			expectedStatus: framework.NewStatus(framework.PreemptionSucceed, ""),
		},
		{
			name:           "preemptor and pod have smaller priority",
			preemptor:      testing_helper.MakePod().Priority(99).Obj(),
			pod:            testing_helper.MakePod().Priority(100).Obj(),
			expectedStatus: framework.NewStatus(framework.PreemptionFail, "pod with lower priority could not preempt pod with higher priority"),
		},
		{
			name:           "preemptor and pod have same priority",
			preemptor:      testing_helper.MakePod().Priority(100).Obj(),
			pod:            testing_helper.MakePod().Priority(100).Obj(),
			expectedStatus: framework.NewStatus(framework.PreemptionFail, "pod with lower priority could not preempt pod with higher priority"),
		},
	}

	for _, tt := range tests {
		checker := &PriorityValueChecker{}
		state := framework.NewCycleState()
		if gotStatus := checker.ClusterPrePreempting(tt.preemptor, state, nil); gotStatus != nil {
			t.Errorf("failed to run prepare preemption plugin: %v", gotStatus)
		}
		podInfo := framework.NewPodInfo(tt.pod)
		gotCode, gotMsg := checker.VictimSearching(tt.preemptor, podInfo, state, nil, nil)
		gotStatus := framework.NewStatus(gotCode, gotMsg)
		if !reflect.DeepEqual(tt.expectedStatus, gotStatus) {
			t.Errorf("expected to get status: %v, but got: %v", tt.expectedStatus, gotStatus)
		}
	}
}
