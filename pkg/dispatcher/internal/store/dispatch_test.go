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

package store

import (
	"reflect"
	"testing"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/sets"

	testing_helper "github.com/kubewharf/godel-scheduler/pkg/testing-helper"
	podutil "github.com/kubewharf/godel-scheduler/pkg/util/pod"
)

func TestGroupPodsAddPod(t *testing.T) {
	g := make(podStore)
	g.addPod("scheduler1", "pod1")
	if !g["scheduler1"].Has("pod1") || g["scheduler1"].Len() != 1 {
		t.Errorf("unexpected result for adding a pod to a new group, len: %d", g["scheduler1"].Len())
	}
	g.addPod("scheduler1", "pod2")
	if !g["scheduler1"].Has("pod2") || g["scheduler1"].Len() != 2 {
		t.Errorf("unexpected result for adding a pod to an existing group, len: %d", g["scheduler1"].Len())
	}
}

func TestGroupPodsRemovePod(t *testing.T) {
	g := make(podStore)
	g.addPod("scheduler1", "pod1")
	g.addPod("scheduler1", "pod2")
	g.removePod("scheduler1", "pod2")
	if g["scheduler1"].Has("pod2") || g["scheduler1"].Len() != 1 {
		t.Errorf("unexpected result for removing a pod from an existing group, len: %d", g["scheduler1"].Len())
	}
	g.removePod("scheduler1", "pod1")
	if g["scheduler1"].Has("pod1") || g["scheduler1"].Len() != 0 {
		t.Errorf("unexpected result for removing a pod from an existing group, len: %d", g["scheduler1"].Len())
	}
}

func TestGroupPodsGetLeastGroup(t *testing.T) {
	g := make(podStore)
	g.addPod("scheduler1", "pod1")
	g.addPod("scheduler1", "pod2")
	g.addPod("scheduler2", "pod3")
	group := g.getLeastGroup()
	if group == "scheduler1" {
		t.Errorf("unexpected result, group: %s", group)
	}
}

func newSimplePodWithSchedulerName(ns, name, schedulerName string) *corev1.Pod {
	return testing_helper.MakePod().Namespace(ns).Name(name).Annotation(podutil.SchedulerAnnotationKey, schedulerName).Obj()
}

func Test_dispatchInfo_GetPodsOfOneScheduler(t *testing.T) {
	tests := []struct {
		name          string
		schedulerName string
		schedulers    []string
		addedPods     []*corev1.Pod
		removedPods   []*corev1.Pod
		want          sets.String
	}{
		{
			name:          "scheduler not exist",
			schedulerName: "test-scheduler-0",
			schedulers:    []string{"test-scheduler-1", "test-scheduler-2", "test-scheduler-3"},
			addedPods: []*corev1.Pod{
				newSimplePodWithSchedulerName("test-ns", "pod1", "test-scheduler-1"),
				newSimplePodWithSchedulerName("test-ns", "pod2", "test-scheduler-2"),
				newSimplePodWithSchedulerName("test-ns", "pod3", "test-scheduler-3"),
			},
			removedPods: nil,
			want:        sets.NewString(),
		},
		{
			name:          "no pods belong to scheduler",
			schedulerName: "test-scheduler-0",
			schedulers:    []string{"test-scheduler-0", "test-scheduler-1", "test-scheduler-2", "test-scheduler-3"},
			addedPods: []*corev1.Pod{
				newSimplePodWithSchedulerName("test-ns", "pod1", "test-scheduler-1"),
				newSimplePodWithSchedulerName("test-ns", "pod2", "test-scheduler-2"),
				newSimplePodWithSchedulerName("test-ns", "pod3", "test-scheduler-3"),
			},
			removedPods: nil,
			want:        sets.NewString(),
		},
		{
			name:          "pods have been deleted",
			schedulerName: "test-scheduler-0",
			schedulers:    []string{"test-scheduler-0"},
			addedPods: []*corev1.Pod{
				newSimplePodWithSchedulerName("test-ns", "pod0", "test-scheduler-0"),
				newSimplePodWithSchedulerName("test-ns", "pod1", "test-scheduler-0"),
				newSimplePodWithSchedulerName("test-ns", "pod2", "test-scheduler-0"),
				newSimplePodWithSchedulerName("test-ns", "pod3", "test-scheduler-0"),
			},
			removedPods: []*corev1.Pod{
				newSimplePodWithSchedulerName("test-ns", "pod0", "test-scheduler-0"),
				newSimplePodWithSchedulerName("test-ns", "pod1", "test-scheduler-0"),
				newSimplePodWithSchedulerName("test-ns", "pod2", "test-scheduler-0"),
				newSimplePodWithSchedulerName("test-ns", "pod3", "test-scheduler-0"),
			},
			want: sets.NewString(),
		},
		{
			name:          "get pods belong to specified scheduler",
			schedulerName: "test-scheduler-0",
			schedulers:    []string{"test-scheduler-0", "test-scheduler-1"},
			addedPods: []*corev1.Pod{
				newSimplePodWithSchedulerName("test-ns", "pod0", "test-scheduler-0"),
				newSimplePodWithSchedulerName("test-ns", "pod1", "test-scheduler-0"),
				newSimplePodWithSchedulerName("test-ns", "pod2", "test-scheduler-1"),
				newSimplePodWithSchedulerName("test-ns", "pod3", "test-scheduler-1"),
			},
			want: sets.NewString("test-ns/pod0", "test-ns/pod1"),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dq := NewDispatchInfo()
			for _, scheduler := range tt.schedulers {
				dq.AddScheduler(scheduler)
			}

			for _, pod := range tt.addedPods {
				dq.AddPod(pod)
			}

			for _, pod := range tt.removedPods {
				dq.RemovePod(pod)
			}

			got := sets.NewString(dq.GetPodsOfOneScheduler(tt.schedulerName)...)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("GetPodsOfOneScheduler() = %v, want %v", got, tt.want)
			}
		})
	}
}

func Test_dispatchInfo_GetMostIdleSchedulerAndAddPodInAdvance(t *testing.T) {
	tests := []struct {
		name        string
		pod         *corev1.Pod
		schedulers  []string
		existedPods []*corev1.Pod
		assert      func(result string) bool
		expected    string
	}{
		{
			name:        "only one empty scheduler exist",
			pod:         newSimplePodWithSchedulerName("test-ns", "pod0", ""),
			schedulers:  []string{"test-scheduler-0"},
			existedPods: nil,
			assert: func(result string) bool {
				return result == "test-scheduler-0"
			},
			expected: "test-scheduler-0",
		},
		{
			name:       "return most idle scheduler",
			pod:        newSimplePodWithSchedulerName("test-ns", "pod0", ""),
			schedulers: []string{"test-scheduler-0", "test-scheduler-1"},
			existedPods: []*corev1.Pod{
				newSimplePodWithSchedulerName("test-ns", "pod0", "test-scheduler-0"),
				newSimplePodWithSchedulerName("test-ns", "pod1", "test-scheduler-0"),
				newSimplePodWithSchedulerName("test-ns", "pod2", "test-scheduler-0"),
				newSimplePodWithSchedulerName("test-ns", "pod3", "test-scheduler-1"),
			},
			assert: func(result string) bool {
				return result == "test-scheduler-1"
			},
			expected: "test-scheduler-1",
		},
		{
			name:       "return random one scheduler, when all scheduler have same length",
			pod:        newSimplePodWithSchedulerName("test-ns", "pod0", ""),
			schedulers: []string{"test-scheduler-0", "test-scheduler-1"},
			existedPods: []*corev1.Pod{
				newSimplePodWithSchedulerName("test-ns", "pod0", "test-scheduler-0"),
				newSimplePodWithSchedulerName("test-ns", "pod1", "test-scheduler-0"),
				newSimplePodWithSchedulerName("test-ns", "pod2", "test-scheduler-1"),
				newSimplePodWithSchedulerName("test-ns", "pod3", "test-scheduler-1"),
			},
			assert: func(result string) bool {
				return result == "test-scheduler-1" || result == "test-scheduler-0"
			},
			expected: "test-scheduler-1 or test-scheduler-0",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dq := NewDispatchInfo()
			for _, scheduler := range tt.schedulers {
				dq.AddScheduler(scheduler)
			}

			for _, pod := range tt.existedPods {
				dq.AddPod(pod)
			}

			if got := dq.GetMostIdleSchedulerAndAddPodInAdvance(tt.pod); !tt.assert(got) {
				t.Errorf("GetMostIdleSchedulerAndAddPodInAdvance() = %v, expected %v", got, tt.expected)
			}
		})
	}
}
