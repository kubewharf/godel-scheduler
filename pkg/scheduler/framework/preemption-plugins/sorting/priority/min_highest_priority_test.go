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
	"reflect"
	"sort"
	"testing"

	v1 "k8s.io/api/core/v1"

	framework "github.com/kubewharf/godel-scheduler/pkg/framework/api"
	testinghelper "github.com/kubewharf/godel-scheduler/pkg/testing-helper"
	"github.com/kubewharf/godel-scheduler/pkg/util"
)

func TestMinHighestPriority(t *testing.T) {
	tests := []struct {
		name          string
		candidates    []*framework.Candidate
		expectedOrder []string
	}{
		{
			name: "case 1",
			candidates: []*framework.Candidate{
				{
					Victims: &framework.Victims{
						Pods: []*v1.Pod{
							testinghelper.MakePod().Name("p1").UID("p1").Node("node1").Priority(10).
								Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).Obj(),
							testinghelper.MakePod().Name("p2").UID("p2").Node("node1").Priority(9).
								Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).Obj(),
						},
						PreemptionState: framework.NewCycleState(),
					},
					Name: "n1",
				},
				{
					Victims: &framework.Victims{
						Pods: []*v1.Pod{
							testinghelper.MakePod().Name("p3").UID("p3").Node("node2").Priority(9).
								Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).Obj(),
							testinghelper.MakePod().Name("p4").UID("p4").Node("node2").Priority(9).
								Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).Obj(),
						},
						PreemptionState: framework.NewCycleState(),
					},
					Name: "n2",
				},
			},
			expectedOrder: []string{"n2", "n1"},
		},
		{
			name: "case 2",
			candidates: []*framework.Candidate{
				{
					Victims: &framework.Victims{
						Pods: []*v1.Pod{
							testinghelper.MakePod().Name("p1").UID("p1").Node("n1").Priority(9).
								Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).Obj(),
							testinghelper.MakePod().Name("p2").UID("p2").Node("n1").Priority(9).
								Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).Obj(),
						},
						PreemptionState: framework.NewCycleState(),
					},
					Name: "n1",
				},
				{
					Victims: &framework.Victims{
						Pods: []*v1.Pod{
							testinghelper.MakePod().Name("p4").UID("p4").Node("n2").Priority(10).
								Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).Obj(),
							testinghelper.MakePod().Name("p5").UID("p5").Node("n2").Priority(9).
								Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).Obj(),
						},
						PreemptionState: framework.NewCycleState(),
					},
					Name: "n2",
				},
			},
			expectedOrder: []string{"n1", "n2"},
		},
		{
			name: "case 3",
			candidates: []*framework.Candidate{
				{
					Victims: &framework.Victims{
						Pods: []*v1.Pod{
							testinghelper.MakePod().Name("p1").UID("p1").Node("n1").Priority(9).
								Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).Obj(),
							testinghelper.MakePod().Name("p2").UID("p2").Node("n1").Priority(9).
								Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).Obj(),
						},
						PreemptionState: framework.NewCycleState(),
					},
					Name: "n1",
				},
				{
					Victims: &framework.Victims{
						Pods: []*v1.Pod{
							testinghelper.MakePod().Name("p3").UID("p3").Node("n2").Priority(9).
								Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).Obj(),
							testinghelper.MakePod().Name("p4").UID("p4").Node("n2").Priority(9).
								Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).Obj(),
						},
						PreemptionState: framework.NewCycleState(),
					},
					Name: "n2",
				},
			},
			expectedOrder: []string{"n1", "n2"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			plugin := &MinHighestPriority{}
			sort.SliceStable(tt.candidates, func(i, j int) bool {
				cmp := plugin.Compare(tt.candidates[i], tt.candidates[j])
				return cmp > 0
			})
			var gotOrder []string
			for _, candidate := range tt.candidates {
				gotOrder = append(gotOrder, candidate.Name)
			}
			if !reflect.DeepEqual(tt.expectedOrder, gotOrder) {
				t.Errorf("expected %v but got %v", tt.expectedOrder, gotOrder)
			}
		})
	}
}
