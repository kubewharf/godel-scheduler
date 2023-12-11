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

package unitscheduler

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	clientsetfake "k8s.io/client-go/kubernetes/fake"
	clienttesting "k8s.io/client-go/testing"

	framework "github.com/kubewharf/godel-scheduler/pkg/framework/api"
	podutil "github.com/kubewharf/godel-scheduler/pkg/util/pod"
)

const (
	// podInitialBackoffDurationSeconds = 1
	// podMaxBackoffDurationSeconds     = 10
	testSchedulerName = "test-scheduler"
)

func TestUpdatePodCondition(t *testing.T) {
	tests := []struct {
		name                  string
		podState              podutil.PodState
		expectedPodState      podutil.PodState
		pod                   *v1.Pod
		failed                *v1.Pod
		reason                string
		expectedPatchRequests int
	}{
		{
			name:             "make pod back to dispatcher",
			podState:         podutil.PodDispatched,
			expectedPodState: podutil.PodPending,
			pod: &v1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name: "foo",
					Annotations: map[string]string{
						podutil.SchedulerAnnotationKey: testSchedulerName,
						podutil.PodStateAnnotationKey:  string(podutil.PodDispatched),
					},
				},
			},
			failed: &v1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name: "foo",
					Annotations: map[string]string{
						podutil.PodStateAnnotationKey:         string(podutil.PodPending),
						podutil.FailedSchedulersAnnotationKey: testSchedulerName,
					},
				},
			},
			reason:                "SchedulerUnexpectedError",
			expectedPatchRequests: 1,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			actualPatchRequests := 0
			client := clientsetfake.NewSimpleClientset(test.pod)
			client.PrependReactor("patch", "pods", func(action clienttesting.Action) (bool, runtime.Object, error) {
				actualPatchRequests++
				// For this test, we don't care about the result of the patched pod, just that we got the expected
				// patch request, so just returning &v1.Pod{} here is OK because scheduler doesn't use the response.
				return false, &v1.Pod{}, nil
			})

			if err := updateFailedPodCondition(client, test.pod, test.failed, test.reason, &framework.FitError{Pod: test.failed}); err != nil {
				t.Fatalf("Error calling update: %v", err)
			}

			actualPod, err := client.CoreV1().Pods("").Get(context.TODO(), test.pod.Name, metav1.GetOptions{})
			if err != nil {
				t.Fatal(err)
			}
			assert.Equal(t, test.expectedPodState, podutil.GetPodState(actualPod.Annotations))
			failedSchedulers := podutil.GetFailedSchedulersNames(actualPod)
			assert.Contains(t, failedSchedulers, testSchedulerName)
			assert.NotContains(t, actualPod.Annotations, podutil.SchedulerAnnotationKey)
			for _, condition := range actualPod.Status.Conditions {
				if condition.Type == v1.PodScheduled {
					assert.Equal(t, condition.Status, v1.ConditionFalse)
					assert.Equal(t, condition.Reason, test.reason)
				}
			}

			if actualPatchRequests != test.expectedPatchRequests {
				t.Fatalf("expected %v, but got %v", test.expectedPatchRequests, actualPatchRequests)
			}
		})
	}
}
