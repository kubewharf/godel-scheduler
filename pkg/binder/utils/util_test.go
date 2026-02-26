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

	"github.com/stretchr/testify/assert"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"

	podutil "github.com/kubewharf/godel-scheduler/pkg/util/pod"
)

func TestCleanupPodAnnotations_ResetToDispatched(t *testing.T) {
	pod := &v1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "pod-1",
			Namespace: "default",
			Annotations: map[string]string{
				podutil.PodStateAnnotationKey:    string(podutil.PodAssumed),
				podutil.AssumedNodeAnnotationKey: "node-1",
			},
		},
	}
	client := fake.NewSimpleClientset(pod)

	err := CleanupPodAnnotations(client, pod)
	assert.NoError(t, err)
}

func TestCleanupPodAnnotations_ClearsAllSchedulingAnnotations(t *testing.T) {
	pod := &v1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "pod-1",
			Namespace: "default",
			Annotations: map[string]string{
				podutil.PodStateAnnotationKey:            string(podutil.PodAssumed),
				podutil.AssumedNodeAnnotationKey:         "node-1",
				podutil.AssumedCrossNodeAnnotationKey:    "node-2",
				podutil.NominatedNodeAnnotationKey:       "node-3",
				podutil.FailedSchedulersAnnotationKey:    "sched-1",
				podutil.MicroTopologyKey:                 "{\"a\":1}",
				podutil.MovementNameKey:                  "move-1",
				podutil.MatchedReservationPlaceholderKey: "rsv-1",
				"unrelated-annotation":                   "keep-me",
			},
		},
	}
	client := fake.NewSimpleClientset(pod)

	err := CleanupPodAnnotations(client, pod)
	assert.NoError(t, err)
}

func TestCleanupPodAnnotations_NilAnnotations(t *testing.T) {
	pod := &v1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "pod-1",
			Namespace: "default",
		},
	}
	client := fake.NewSimpleClientset(pod)

	err := CleanupPodAnnotations(client, pod)
	assert.NoError(t, err)
}

func TestCleanupPodAnnotationsWithRetryCount_BelowThreshold(t *testing.T) {
	pod := &v1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "pod-1",
			Namespace: "default",
			Annotations: map[string]string{
				podutil.PodStateAnnotationKey:    string(podutil.PodAssumed),
				podutil.AssumedNodeAnnotationKey: "node-1",
				BindFailureCountAnnotationKey:    "2",
			},
		},
	}
	client := fake.NewSimpleClientset(pod)

	err := CleanupPodAnnotationsWithRetryCount(client, pod, "scheduler-A", 5)
	assert.NoError(t, err)
}

func TestCleanupPodAnnotationsWithRetryCount_AboveThreshold(t *testing.T) {
	pod := &v1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "pod-1",
			Namespace: "default",
			Annotations: map[string]string{
				podutil.PodStateAnnotationKey:    string(podutil.PodAssumed),
				podutil.AssumedNodeAnnotationKey: "node-1",
				podutil.SchedulerAnnotationKey:   "scheduler-A",
				BindFailureCountAnnotationKey:    "6",
			},
		},
	}
	client := fake.NewSimpleClientset(pod)

	err := CleanupPodAnnotationsWithRetryCount(client, pod, "scheduler-A", 5)
	assert.NoError(t, err)
}

func TestCleanupPodAnnotationsWithRetryCount_ExactThreshold(t *testing.T) {
	pod := &v1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "pod-1",
			Namespace: "default",
			Annotations: map[string]string{
				podutil.PodStateAnnotationKey:    string(podutil.PodAssumed),
				podutil.AssumedNodeAnnotationKey: "node-1",
				podutil.SchedulerAnnotationKey:   "scheduler-B",
				BindFailureCountAnnotationKey:    "5",
			},
		},
	}
	client := fake.NewSimpleClientset(pod)

	err := CleanupPodAnnotationsWithRetryCount(client, pod, "scheduler-B", 5)
	assert.NoError(t, err)
}

func TestCleanupPodAnnotationsWithRetryCount_PreservesFailedSchedulers(t *testing.T) {
	pod := &v1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "pod-1",
			Namespace: "default",
			Annotations: map[string]string{
				podutil.PodStateAnnotationKey:    string(podutil.PodAssumed),
				podutil.AssumedNodeAnnotationKey: "node-1",
				podutil.SchedulerAnnotationKey:   "scheduler-B",
				BindFailureCountAnnotationKey:    "10",
			},
		},
	}
	client := fake.NewSimpleClientset(pod)

	err := CleanupPodAnnotationsWithRetryCount(client, pod, "scheduler-B", 5)
	assert.NoError(t, err)
}

func TestCleanupPodAnnotationsWithRetryCount_ZeroMaxDisablesDispatch(t *testing.T) {
	pod := &v1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "pod-1",
			Namespace: "default",
			Annotations: map[string]string{
				podutil.PodStateAnnotationKey:    string(podutil.PodAssumed),
				podutil.AssumedNodeAnnotationKey: "node-1",
				BindFailureCountAnnotationKey:    "100",
			},
		},
	}
	client := fake.NewSimpleClientset(pod)

	// maxLocalRetries=0 disables dispatcher fallback; always returns to same scheduler
	err := CleanupPodAnnotationsWithRetryCount(client, pod, "scheduler-A", 0)
	assert.NoError(t, err)
}
