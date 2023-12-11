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

package preemptibilitychecker

import (
	"reflect"
	"testing"

	v1 "k8s.io/api/core/v1"
	schedulingv1 "k8s.io/api/scheduling/v1"

	framework "github.com/kubewharf/godel-scheduler/pkg/framework/api"
	testing_helper "github.com/kubewharf/godel-scheduler/pkg/testing-helper"
	"github.com/kubewharf/godel-scheduler/pkg/util"
)

func TestPreemptibilityChecker(t *testing.T) {
	tests := []struct {
		name           string
		pod            *v1.Pod
		pc             *schedulingv1.PriorityClass
		expectedStatus *framework.Status
	}{
		{
			name:           "pod has no priority class",
			pod:            testing_helper.MakePod().Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).Obj(),
			expectedStatus: framework.NewStatus(framework.PreemptionFail, "pod marked as non-preemptable"),
		},
		{
			name: "pod has no annotation, has priority class, claim can be preempted",
			pod:  testing_helper.MakePod().PriorityClassName("p").Obj(),
			pc: testing_helper.MakePriorityClass().Name("p").
				Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).Obj(),
			expectedStatus: framework.NewStatus(framework.PreemptionNotSure, ""),
		},
		{
			name: "pod has no annotation, has priority class, claim can not be preempted",
			pod:  testing_helper.MakePod().PriorityClassName("p").Obj(),
			pc: testing_helper.MakePriorityClass().Name("p").
				Annotation(util.CanBePreemptedAnnotationKey, util.CannotBePreempted).Obj(),
			expectedStatus: framework.NewStatus(framework.PreemptionFail, "pod marked as non-preemptable"),
		},
		{
			name: "pod annotation marked can be preempted",
			pod: testing_helper.MakePod().PriorityClassName("p").
				Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).Obj(),
			expectedStatus: framework.NewStatus(framework.PreemptionNotSure, ""),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pcLister := testing_helper.NewFakePriorityClassLister([]*schedulingv1.PriorityClass{tt.pc})
			canBePreempted := &PreemptibilityChecker{
				pcLister: pcLister,
			}
			podInfo := framework.NewPodInfo(tt.pod)
			gotCode, gotMsg := canBePreempted.VictimSearching(nil, podInfo, nil, nil, nil)
			gotStatus := framework.NewStatus(gotCode, gotMsg)
			if !reflect.DeepEqual(tt.expectedStatus, gotStatus) {
				t.Errorf("expected status: %v, but got: %v", tt.expectedStatus, gotStatus)
			}
		})
	}
}
