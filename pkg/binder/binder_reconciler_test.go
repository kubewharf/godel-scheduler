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

package binder

import (
	"sync"
	"testing"
	"time"

	v1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"

	"github.com/kubewharf/godel-scheduler/pkg/framework/api"
)

// ------------------------------------------------------------------
// Helpers
// ------------------------------------------------------------------

func makeReconcilerPod(name string) *v1.Pod {
	return &v1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: "default",
			UID:       types.UID("uid-" + name),
			Annotations: map[string]string{
				"godel.bytedance.com/pod-state":         "assumed",
				"godel.bytedance.com/assumed-node":      "node-1",
				"godel.bytedance.com/pod-resource-type": "guaranteed",
				"godel.bytedance.com/pod-launcher":      "kubelet",
			},
		},
	}
}

func makeQueuedPodInfoForTest(pod *v1.Pod) *api.QueuedPodInfo {
	return &api.QueuedPodInfo{Pod: pod}
}

// waitForQueueDrain polls until the reconciler's queue is empty or the timeout
// elapses. Returns true if drained.
func waitForQueueDrain(btr *BinderTasksReconciler, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if btr.Len() == 0 {
			return true
		}
		time.Sleep(10 * time.Millisecond)
	}
	return btr.Len() == 0
}

// ------------------------------------------------------------------
// Tests
// ------------------------------------------------------------------

func TestReconciler_AddFailedTask_Enqueue(t *testing.T) {
	client := fake.NewSimpleClientset()
	btr := NewBinderTaskReconciler(client)
	defer btr.Close()

	pod := makeReconcilerPod("pod-enqueue")
	btr.AddFailedTask(&APICallFailedTask{
		reason: RejectFailed,
		qpi:    makeQueuedPodInfoForTest(pod),
	})

	if btr.Len() != 1 {
		t.Errorf("expected queue length 1, got %d", btr.Len())
	}
}

func TestReconciler_Worker_RejectFailed_Success(t *testing.T) {
	pod := makeReconcilerPod("pod-success")
	client := fake.NewSimpleClientset(pod)

	btr := NewBinderTaskReconciler(client)
	btr.Run()
	defer btr.Close()

	btr.AddFailedTask(&APICallFailedTask{
		reason: RejectFailed,
		qpi:    makeQueuedPodInfoForTest(pod),
	})

	if !waitForQueueDrain(btr, 5*time.Second) {
		t.Error("task was not drained from queue in time")
	}
}

func TestReconciler_Worker_RejectFailed_TransientError(t *testing.T) {
	pod := makeReconcilerPod("pod-transient")
	client := fake.NewSimpleClientset(pod)

	var mu sync.Mutex
	callCount := 0
	successCh := make(chan struct{})
	client.PrependReactor("patch", "pods", func(action k8stesting.Action) (bool, runtime.Object, error) {
		mu.Lock()
		callCount++
		c := callCount
		mu.Unlock()
		if c <= 2 {
			// Return a Conflict error (transient) that causes re-enqueue
			return true, nil, apierrors.NewConflict(
				schema.GroupResource{Group: "", Resource: "pods"},
				"pod-transient",
				nil,
			)
		}
		// Signal success on the 3rd call, then let default handler proceed
		defer func() {
			select {
			case successCh <- struct{}{}:
			default:
			}
		}()
		return false, nil, nil
	})

	btr := NewBinderTaskReconciler(client)
	btr.Run()
	defer btr.Close()

	btr.AddFailedTask(&APICallFailedTask{
		reason: RejectFailed,
		qpi:    makeQueuedPodInfoForTest(pod),
	})

	// Wait for the reconciler to succeed on its 3rd attempt.
	select {
	case <-successCh:
		// OK
	case <-time.After(15 * time.Second):
		mu.Lock()
		c := callCount
		mu.Unlock()
		t.Fatalf("timed out waiting for successful patch; callCount=%d", c)
	}

	mu.Lock()
	finalCount := callCount
	mu.Unlock()
	if finalCount < 3 {
		t.Errorf("expected at least 3 patch calls (2 failures + 1 success), got %d", finalCount)
	}
}

func TestReconciler_Worker_RejectFailed_NotFound(t *testing.T) {
	pod := makeReconcilerPod("pod-not-found")
	client := fake.NewSimpleClientset() // pod not in API

	client.PrependReactor("patch", "pods", func(action k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, apierrors.NewNotFound(
			schema.GroupResource{Group: "", Resource: "pods"},
			"pod-not-found",
		)
	})

	btr := NewBinderTaskReconciler(client)
	btr.Run()
	defer btr.Close()

	btr.AddFailedTask(&APICallFailedTask{
		reason: RejectFailed,
		qpi:    makeQueuedPodInfoForTest(pod),
	})

	// NotFound errors should NOT be re-enqueued.
	if !waitForQueueDrain(btr, 5*time.Second) {
		t.Error("task was not drained for NotFound pod")
	}
}

func TestReconciler_Run_Stop(t *testing.T) {
	client := fake.NewSimpleClientset()
	btr := NewBinderTaskReconciler(client)

	// Should not panic on Run + Close.
	btr.Run()
	time.Sleep(50 * time.Millisecond)
	btr.Close()
}

func TestReconciler_ConcurrentAdd(t *testing.T) {
	client := fake.NewSimpleClientset()
	btr := NewBinderTaskReconciler(client)
	defer btr.Close()

	var wg sync.WaitGroup
	n := 20
	wg.Add(n)
	for i := 0; i < n; i++ {
		go func(idx int) {
			defer wg.Done()
			pod := makeReconcilerPod("pod-concurrent-" + string(rune('a'+idx)))
			btr.AddFailedTask(&APICallFailedTask{
				reason: RejectFailed,
				qpi:    makeQueuedPodInfoForTest(pod),
			})
		}(i)
	}
	wg.Wait()

	if btr.Len() == 0 {
		t.Error("expected non-empty queue after concurrent adds")
	}
}

func TestReconciler_WithRetry_DispatcherFallback(t *testing.T) {
	// Pod has bind-failure-count = 5 (>= maxLocalRetries=5)
	pod := makeReconcilerPod("pod-fallback")
	pod.Annotations["godel.bytedance.com/bind-failure-count"] = "5"
	pod.Annotations["godel.bytedance.com/selected-scheduler"] = "scheduler-A"
	client := fake.NewSimpleClientset(pod)

	var patchedState string
	var patchedSchedulerRemoved bool
	client.PrependReactor("patch", "pods", func(action k8stesting.Action) (bool, runtime.Object, error) {
		patchAction := action.(k8stesting.PatchAction)
		patchBytes := patchAction.GetPatch()
		patchStr := string(patchBytes)
		// Check if the patch sets state to "pending" (dispatcher fallback)
		if containsString(patchStr, `"pending"`) {
			patchedState = "pending"
		} else if containsString(patchStr, `"dispatched"`) {
			patchedState = "dispatched"
		}
		if containsString(patchStr, `"godel.bytedance.com/selected-scheduler":null`) {
			patchedSchedulerRemoved = true
		}
		return false, nil, nil // let default handler proceed
	})

	btr := NewBinderTaskReconcilerWithRetry(client, "scheduler-A", 5)
	btr.Run()
	defer btr.Close()

	btr.AddFailedTask(&APICallFailedTask{
		reason: RejectFailed,
		qpi:    makeQueuedPodInfoForTest(pod),
	})

	if !waitForQueueDrain(btr, 5*time.Second) {
		t.Fatal("task was not drained in time")
	}

	if patchedState != "pending" {
		t.Errorf("expected state 'pending' for dispatcher fallback, got %q", patchedState)
	}
	if !patchedSchedulerRemoved {
		t.Error("expected scheduler annotation to be removed for dispatcher fallback")
	}
}

func TestReconciler_WithRetry_LocalRetry(t *testing.T) {
	// Pod has bind-failure-count = 2 (< maxLocalRetries=5) — should return to same scheduler
	pod := makeReconcilerPod("pod-local-retry")
	pod.Annotations["godel.bytedance.com/bind-failure-count"] = "2"
	pod.Annotations["godel.bytedance.com/selected-scheduler"] = "scheduler-A"
	client := fake.NewSimpleClientset(pod)

	var patchedState string
	client.PrependReactor("patch", "pods", func(action k8stesting.Action) (bool, runtime.Object, error) {
		patchAction := action.(k8stesting.PatchAction)
		patchStr := string(patchAction.GetPatch())
		if containsString(patchStr, `"pending"`) {
			patchedState = "pending"
		} else if containsString(patchStr, `"dispatched"`) {
			patchedState = "dispatched"
		}
		return false, nil, nil
	})

	btr := NewBinderTaskReconcilerWithRetry(client, "scheduler-A", 5)
	btr.Run()
	defer btr.Close()

	btr.AddFailedTask(&APICallFailedTask{
		reason: RejectFailed,
		qpi:    makeQueuedPodInfoForTest(pod),
	})

	if !waitForQueueDrain(btr, 5*time.Second) {
		t.Fatal("task was not drained in time")
	}

	if patchedState != "dispatched" {
		t.Errorf("expected state 'dispatched' for local retry, got %q", patchedState)
	}
}

func TestReconciler_Len(t *testing.T) {
	client := fake.NewSimpleClientset()
	btr := NewBinderTaskReconciler(client)
	defer btr.Close()

	if btr.Len() != 0 {
		t.Errorf("expected empty queue, got %d", btr.Len())
	}

	for i := 0; i < 3; i++ {
		pod := makeReconcilerPod("pod-len-" + string(rune('a'+i)))
		btr.AddFailedTask(&APICallFailedTask{
			reason: RejectFailed,
			qpi:    makeQueuedPodInfoForTest(pod),
		})
	}

	// Note: workqueue deduplication may affect exact count, but it should be > 0
	if btr.Len() == 0 {
		t.Error("expected non-empty queue after adding tasks")
	}
}

// containsString is a simple helper to check substring presence.
func containsString(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || len(s) > 0 && stringContains(s, sub))
}

func stringContains(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
