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

package api

import (
	"fmt"
	"strconv"
	"strings"
	"testing"

	schedulingv1a1 "github.com/kubewharf/godel-scheduler-api/pkg/apis/scheduling/v1alpha1"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	podutil "github.com/kubewharf/godel-scheduler/pkg/util/pod"
)

const (
	// const values shared by all test cases
	pgDefaultName              = "test-podgroup"
	pgDefaultNamespace         = "default"
	pgDefaultMinMember         = 2
	pgDefaultPriorityClassName = "high-priority"
	pgDefaultPriorityValue     = int32(80)
)

func TestPodGroupUnit_GetType(t *testing.T) {
	for _, tt := range []struct {
		desc     string
		unit     PodGroupUnit
		expected ScheduleUnitType
	}{
		{
			desc: "get unit type",
			unit: PodGroupUnit{
				podGroup: createPodGroup(pgDefaultNamespace, pgDefaultName, pgDefaultMinMember, ""),
			},
			expected: PodGroupUnitType,
		},
	} {
		t.Run(tt.desc, func(t *testing.T) {
			if got := tt.unit.Type(); got != tt.expected {
				t.Errorf("expected %v, got %v", tt.expected, got)
			}
		})
	}
}

func TestPodGroupUnit_GetKey(t *testing.T) {
	for _, tt := range []struct {
		desc     string
		unit     PodGroupUnit
		expected string
	}{
		{
			desc:     "get unit key",
			unit:     *NewPodGroupUnit(createPodGroup(pgDefaultNamespace, pgDefaultName, pgDefaultMinMember, pgDefaultPriorityClassName), pgDefaultPriorityValue),
			expected: fmt.Sprintf("%s/%s/%s", PodGroupUnitType, pgDefaultNamespace, pgDefaultName),
		},
	} {
		t.Run(tt.desc, func(t *testing.T) {
			if got := tt.unit.GetKey(); strings.Compare(got, tt.expected) != 0 {
				t.Errorf("expected %v, got %v", tt.expected, got)
			}
		})
	}
}

func TestPodGroupUnit_GetPriority(t *testing.T) {
	for _, tt := range []struct {
		desc     string
		unit     PodGroupUnit
		expected int32
	}{
		{
			desc: "non-empty priority score, get priority score",
			unit: PodGroupUnit{
				podGroup: createPodGroup(pgDefaultNamespace, pgDefaultName, pgDefaultMinMember, pgDefaultPriorityClassName),
				priority: pgDefaultPriorityValue,
			},
			expected: pgDefaultPriorityValue,
		},
		{
			desc: "empty priority score, get default priority score 0",
			unit: PodGroupUnit{
				podGroup: createPodGroup(pgDefaultNamespace, pgDefaultName, pgDefaultMinMember, ""),
			},
			expected: 0,
		},
	} {
		t.Run(tt.desc, func(t *testing.T) {
			if got := tt.unit.GetPriority(); got != tt.expected {
				t.Errorf("expected %v, got %v", tt.expected, got)
			}
		})
	}
}

func TestPodGroupUnit_PodBelongToUnit(t *testing.T) {
	podName := "test-pod1"
	for _, tt := range []struct {
		desc     string
		unit     PodGroupUnit
		pod      *v1.Pod
		expected bool
	}{
		{
			desc: "pod belong to unit, no pod annotation",
			unit: PodGroupUnit{
				podGroup: createPodGroup(pgDefaultNamespace, pgDefaultName, pgDefaultMinMember, ""),
			},
			pod: &v1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: pgDefaultNamespace,
					Name:      podName,
				},
			},
			expected: false,
		},
		{
			desc: "pod belong to unit, pod group is nil",
			unit: PodGroupUnit{},
			pod: &v1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: pgDefaultNamespace,
					Name:      podName,
				},
			},
			expected: false,
		},
		{
			desc: "pod belong to unit, same namespace and but different name",
			unit: PodGroupUnit{
				podGroup: createPodGroup(pgDefaultNamespace, pgDefaultName, pgDefaultMinMember, pgDefaultPriorityClassName),
				priority: pgDefaultPriorityValue,
			},
			pod: &v1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Namespace:   pgDefaultNamespace,
					Name:        podName,
					Annotations: map[string]string{podutil.PodGroupNameAnnotationKey: "unknown_podgroup"},
				},
			},
			expected: false,
		},
		{
			desc: "pod belong to unit, same namespace and name",
			unit: PodGroupUnit{
				podGroup: createPodGroup(pgDefaultNamespace, pgDefaultName, pgDefaultMinMember, ""),
			},
			pod: &v1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Namespace:   pgDefaultNamespace,
					Name:        podName,
					Annotations: map[string]string{podutil.PodGroupNameAnnotationKey: pgDefaultName},
				},
			},
			expected: true,
		},
	} {
		t.Run(tt.desc, func(t *testing.T) {
			if got := tt.unit.PodBelongToUnit(tt.pod); got != tt.expected {
				t.Errorf("expected %v, got %v", tt.expected, got)
			}
		})
	}
}

func TestPodGroupUnit_ReadyToBePopulated(t *testing.T) {
	for _, tt := range []struct {
		desc      string
		unit      PodGroupUnit
		podsToAdd []*QueuedPodInfo
		expected  bool
	}{
		{
			desc: "ready to be populated, not enough pods",
			unit: PodGroupUnit{
				podGroup: createPodGroup(pgDefaultNamespace, pgDefaultName, pgDefaultMinMember, pgDefaultPriorityClassName),
				priority: pgDefaultPriorityValue,
			},
			podsToAdd: createQueuedPodInfo(1),
			expected:  false,
		},
		{
			desc: "ready to be populated, enough pods",
			unit: PodGroupUnit{
				podGroup: createPodGroup(pgDefaultNamespace, pgDefaultName, pgDefaultMinMember, pgDefaultPriorityClassName),
				priority: pgDefaultPriorityValue,
			},
			podsToAdd: createQueuedPodInfo(3),
			expected:  false,
		},
	} {
		t.Run(tt.desc, func(t *testing.T) {
			if got := tt.unit.ReadyToBePopulated(); got != tt.expected {
				t.Errorf("expected %v, got %v", tt.expected, got)
			}
		})
	}
}

func createPodGroup(namespace, name string, minMember int32, priorityClassName string) *schedulingv1a1.PodGroup {
	pg := &schedulingv1a1.PodGroup{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
			UID:       types.UID(name),
		},
		Spec: schedulingv1a1.PodGroupSpec{
			MinMember: minMember,
		},
	}

	if len(priorityClassName) != 0 {
		pg.Spec.PriorityClassName = priorityClassName
	}

	return pg
}

func createQueuedPodInfo(num int) []*QueuedPodInfo {
	queue := make([]*QueuedPodInfo, num)

	for i := 0; i < num; i++ {
		queue[i] = &QueuedPodInfo{
			Pod: &v1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					UID: types.UID(strconv.Itoa(i)),
				},
			},
		}
	}

	return queue
}
