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

package config

import (
	"reflect"
	"testing"

	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/kubewharf/godel-scheduler/pkg/util/constraints"
)

func WithAnnotations(annotations map[string]string) *v1.Pod {
	return &v1.Pod{
		ObjectMeta: metav1.ObjectMeta{Annotations: annotations, Name: "test"},
	}
}

func TestGetConstraints(t *testing.T) {
	tests := []struct {
		name               string
		pod                *v1.Pod
		constraintType     string
		expectedConstraint []Constraint
		expectedError      error
	}{
		{
			name:           "success parse hard constraint",
			pod:            WithAnnotations(map[string]string{constraints.HardConstraintsAnnotationKey: "a,b"}),
			constraintType: constraints.HardConstraintsAnnotationKey,
			expectedConstraint: []Constraint{
				{
					PluginName: "a",
					Weight:     1,
				},
				{
					PluginName: "b",
					Weight:     1,
				},
			},
		},
		{
			name:           "success parse soft constraint",
			pod:            WithAnnotations(map[string]string{constraints.SoftConstraintsAnnotationKey: "a:2,b:2"}),
			constraintType: constraints.SoftConstraintsAnnotationKey,
			expectedConstraint: []Constraint{
				{
					PluginName: "a",
					Weight:     2,
				},
				{
					PluginName: "b",
					Weight:     2,
				},
			},
		},
		{
			name:           "success parse soft constraint no weight",
			pod:            WithAnnotations(map[string]string{constraints.SoftConstraintsAnnotationKey: "a,b"}),
			constraintType: constraints.SoftConstraintsAnnotationKey,
			expectedConstraint: []Constraint{
				{
					PluginName: "a",
					Weight:     1,
				},
				{
					PluginName: "b",
					Weight:     1,
				},
			},
		},
		{
			name:           "success parse soft constraint half weight",
			pod:            WithAnnotations(map[string]string{constraints.SoftConstraintsAnnotationKey: "a:3,b"}),
			constraintType: constraints.SoftConstraintsAnnotationKey,
			expectedConstraint: []Constraint{
				{
					PluginName: "a",
					Weight:     3,
				},
				{
					PluginName: "b",
					Weight:     1,
				},
			},
		},
		{
			name:               "failure parse constraint",
			pod:                WithAnnotations(map[string]string{constraints.SoftConstraintsAnnotationKey: ",a"}),
			constraintType:     constraints.SoftConstraintsAnnotationKey,
			expectedConstraint: []Constraint{},
		},
		{
			name:               "error soft constraints with too many options",
			pod:                WithAnnotations(map[string]string{constraints.SoftConstraintsAnnotationKey: "a:1:2"}),
			constraintType:     constraints.SoftConstraintsAnnotationKey,
			expectedConstraint: []Constraint{},
		},
		{
			name:               "error soft constraints with no name",
			pod:                WithAnnotations(map[string]string{constraints.SoftConstraintsAnnotationKey: ":2"}),
			constraintType:     constraints.SoftConstraintsAnnotationKey,
			expectedConstraint: []Constraint{},
		},
		{
			name:               "error soft constraints with invalid weight",
			pod:                WithAnnotations(map[string]string{constraints.SoftConstraintsAnnotationKey: "a:2a"}),
			constraintType:     constraints.SoftConstraintsAnnotationKey,
			expectedConstraint: []Constraint{},
		},
		{
			name:               "error soft constraints with zero weight",
			pod:                WithAnnotations(map[string]string{constraints.SoftConstraintsAnnotationKey: "a:0"}),
			constraintType:     constraints.SoftConstraintsAnnotationKey,
			expectedConstraint: []Constraint{},
		},
		{
			name:               "error soft constraints with negative weight",
			pod:                WithAnnotations(map[string]string{constraints.SoftConstraintsAnnotationKey: "a:-1"}),
			constraintType:     constraints.SoftConstraintsAnnotationKey,
			expectedConstraint: []Constraint{},
		},
		{
			name:           "duplicates hard constraints",
			pod:            WithAnnotations(map[string]string{constraints.SoftConstraintsAnnotationKey: "a:2,b, c, a"}),
			constraintType: constraints.SoftConstraintsAnnotationKey,
			expectedConstraint: []Constraint{
				{
					PluginName: "a",
					Weight:     1,
				},
				{
					PluginName: "b",
					Weight:     1,
				},
				{
					PluginName: "c",
					Weight:     1,
				},
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			constraints, _ := GetConstraints(test.pod, test.constraintType)
			if !reflect.DeepEqual(test.expectedConstraint, constraints) {
				t.Errorf("expected: %#v, got: %#v", test.expectedConstraint, constraints)
			}
		})
	}
}
