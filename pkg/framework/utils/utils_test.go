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
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	framework "github.com/kubewharf/godel-scheduler/pkg/framework/api"
	podutil "github.com/kubewharf/godel-scheduler/pkg/util/pod"
)

func newPriorityPodWithStartTime(name string, priority int32, startTime time.Time) *v1.Pod {
	return &v1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
		},
		Spec: v1.PodSpec{
			Priority: &priority,
		},
		Status: v1.PodStatus{
			StartTime: &metav1.Time{Time: startTime},
		},
	}
}

func TestGetEarliestPodStartTime(t *testing.T) {
	currentTime := time.Now()
	pod1 := newPriorityPodWithStartTime("pod1", 1, currentTime.Add(time.Second))
	pod2 := newPriorityPodWithStartTime("pod2", 2, currentTime.Add(time.Second))
	pod3 := newPriorityPodWithStartTime("pod3", 2, currentTime)
	victims := &framework.Victims{
		Pods: []*v1.Pod{pod1, pod2, pod3},
	}
	startTime := GetEarliestPodStartTime(victims)
	if !startTime.Equal(pod3.Status.StartTime) {
		t.Errorf("Got wrong earliest pod start time")
	}

	pod1 = newPriorityPodWithStartTime("pod1", 2, currentTime)
	pod2 = newPriorityPodWithStartTime("pod2", 2, currentTime.Add(time.Second))
	pod3 = newPriorityPodWithStartTime("pod3", 2, currentTime.Add(2*time.Second))
	victims = &framework.Victims{
		Pods: []*v1.Pod{pod1, pod2, pod3},
	}
	startTime = GetEarliestPodStartTime(victims)
	if !startTime.Equal(pod1.Status.StartTime) {
		t.Errorf("Got wrong earliest pod start time, got %v, expected %v", startTime, pod1.Status.StartTime)
	}
}

// nominatedNode is supposed to be checked before call SetPodNominatedNode
func TestSetAndGetPodNominatedNode(t *testing.T) {
	fakePodNomination := framework.NominatedNode{
		NodeName: "node1",
		VictimPods: []framework.VictimPod{
			{
				Name:      "pod1",
				Namespace: "test",
				UID:       "pod1",
			},
			{
				Name:      "pod2",
				Namespace: "test",
				UID:       "pod2",
			},
		},
	}
	expectedAnnotation := "{\"node\":\"node1\",\"victims\":[{\"name\":\"pod1\",\"namespace\":\"test\",\"uid\":\"pod1\"},{\"name\":\"pod2\",\"namespace\":\"test\",\"uid\":\"pod2\"}]}"

	pod := &v1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Namespace:   "test",
			Name:        "pod",
			Annotations: map[string]string{},
		},
	}
	if err := SetPodNominatedNode(pod, &fakePodNomination); err != nil {
		t.Error(err)
	}

	assert.Equal(t, expectedAnnotation, pod.Annotations[podutil.NominatedNodeAnnotationKey])

	got, err := GetPodNominatedNode(pod)
	if err != nil {
		t.Error(err)
	}
	assert.Equal(t, &fakePodNomination, got)
}
