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

package utils

import (
	"strings"
	"testing"

	schedulingv1alpha1 "github.com/kubewharf/godel-scheduler-api/pkg/apis/scheduling/v1alpha1"
	"github.com/kubewharf/godel-scheduler-api/pkg/client/listers/scheduling/v1alpha1"
	v1 "k8s.io/api/core/v1"
	scheduling "k8s.io/api/scheduling/v1"
	schedulingv1listers "k8s.io/client-go/listers/scheduling/v1"

	framework "github.com/kubewharf/godel-scheduler/pkg/framework/api"
	testinghelper "github.com/kubewharf/godel-scheduler/pkg/testing-helper"
	podutil "github.com/kubewharf/godel-scheduler/pkg/util/pod"
)

func TestGetUnitIdentifier(t *testing.T) {
	pgName := "test-podgroup"
	namespace := "default"
	for _, tt := range []struct {
		name     string
		pod      *framework.QueuedPodInfo
		expected string
	}{
		{
			name: "get unit type, unknown unit type",
			pod: &framework.QueuedPodInfo{
				Pod: testinghelper.MakePod().Namespace(namespace).Obj(),
			},
			expected: string(framework.SinglePodUnitType) + "/" + namespace + "/" + "",
		},
		{
			name: "get unit identifier",
			pod: &framework.QueuedPodInfo{
				Pod: testinghelper.MakePod().Namespace(namespace).Annotation(podutil.PodGroupNameAnnotationKey, pgName).Obj(),
			},
			expected: string(framework.PodGroupUnitType) + "/" + namespace + "/" + pgName,
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if got := GetUnitIdentifier(tt.pod.Pod); strings.Compare(got, tt.expected) != 0 {
				t.Errorf("expected %v, got %v", tt.expected, got)
			}
		})
	}
}

func TestUnitType(t *testing.T) {
	for _, tt := range []struct {
		name     string
		pod      *v1.Pod
		expected framework.ScheduleUnitType
	}{
		{
			name:     "get unit type, unknown unit type",
			pod:      testinghelper.MakePod().Obj(),
			expected: framework.SinglePodUnitType,
		},
		{
			name:     "get unit type, with pod group annotation",
			pod:      testinghelper.MakePod().Annotation(podutil.PodGroupNameAnnotationKey, "exist").Obj(),
			expected: framework.PodGroupUnitType,
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if got := GetUnitType(tt.pod); got != tt.expected {
				t.Errorf("expected %v, got %v", tt.expected, got)
			}
		})
	}
}

func TestPodBelongToUnit(t *testing.T) {
	for _, tt := range []struct {
		name     string
		pod      *v1.Pod
		expected bool
	}{
		{
			name:     "pod belong to unit, empty annotation",
			pod:      testinghelper.MakePod().Obj(),
			expected: false,
		},
		{
			name:     "pod belong to unit, with annotation",
			pod:      testinghelper.MakePod().Annotation(podutil.PodGroupNameAnnotationKey, "exist").Obj(),
			expected: true,
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if got := PodBelongToUnit(tt.pod); got != tt.expected {
				t.Errorf("expected %v, got %v", tt.expected, got)
			}
		})
	}
}

func TestCreateUnit(t *testing.T) {
	minMember := 2
	pcValue := int32(50)
	pgName := "pg1"
	pcName := "pc1"
	// compare unit pg.name and expected pc
	pcLister := testinghelper.NewFakePriorityClassLister([]*scheduling.PriorityClass{
		testinghelper.MakePriorityClass().Name(pcName).Annotation("godel.bytedance.com/can-be-preempted", "true").Value(pcValue).Obj(),
	})

	pgLister := testinghelper.NewFakePodGroupLister([]*schedulingv1alpha1.PodGroup{
		testinghelper.MakePodGroup().Name(pgName).MinMember(uint(minMember)).ProrityClassName(pcName).Obj(),
	})

	normalPod := testinghelper.MakePod().Annotation(podutil.PodGroupNameAnnotationKey, pgName).Obj()

	tests := []struct {
		name           string
		podInfo        *framework.QueuedPodInfo
		pcLister       schedulingv1listers.PriorityClassLister
		pgLister       v1alpha1.PodGroupLister
		expectError    bool
		expectPriority int32
	}{
		{
			name:     "unit is invalid, pod doesn't have pod group name",
			pcLister: pcLister,
			pgLister: pgLister,
			podInfo: &framework.QueuedPodInfo{
				Pod: testinghelper.MakePod().Obj(),
			},
			expectError: false,
		},
		{
			name:     "unit is valid, can not find pod group",
			pcLister: pcLister,
			pgLister: testinghelper.NewFakePodGroupLister([]*schedulingv1alpha1.PodGroup{}),
			podInfo: &framework.QueuedPodInfo{
				Pod: normalPod,
			},
			expectError: true,
		},
		{
			name:     "unit is valid, can not find priority class",
			pcLister: testinghelper.NewFakePriorityClassLister([]*scheduling.PriorityClass{}),
			pgLister: pgLister,
			podInfo: &framework.QueuedPodInfo{
				Pod: normalPod,
			},
			expectError: true,
		},
		{
			name:     "unit is valid, priority class name is empty",
			pcLister: pcLister,
			pgLister: testinghelper.NewFakePodGroupLister([]*schedulingv1alpha1.PodGroup{
				testinghelper.MakePodGroup().Name(pgName).MinMember(uint(minMember)).Obj(),
			}),
			podInfo: &framework.QueuedPodInfo{
				Pod: normalPod,
			},
			expectError:    false,
			expectPriority: podutil.GetDefaultPriorityForGodelPod(normalPod),
		},
		{
			name:     "unit is valid",
			pcLister: pcLister,
			pgLister: pgLister,
			podInfo: &framework.QueuedPodInfo{
				Pod: normalPod,
			},
			expectError:    false,
			expectPriority: pcValue,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			unit, err := CreateScheduleUnit(tt.pcLister, tt.pgLister, tt.podInfo)
			if tt.expectError == (err == nil) {
				t.Errorf("unexpected returned error value: %v", err)
			}

			if tt.expectPriority != 0 {
				if tt.expectPriority != unit.GetPriority() {
					t.Errorf("expected to get xxx: %#v, but actually got %#v", unit.GetPriority(), unit.Type())
				}
			}
		})
	}
}
