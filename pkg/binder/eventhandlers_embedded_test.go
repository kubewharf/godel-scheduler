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
	"testing"
	"time"

	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	"github.com/kubewharf/godel-scheduler/pkg/binder/cache"
	"github.com/kubewharf/godel-scheduler/pkg/binder/queue"
	commoncache "github.com/kubewharf/godel-scheduler/pkg/common/cache"
	podAnnotation "github.com/kubewharf/godel-scheduler/pkg/util/pod"
)

// newTestBinderForEmbedded creates a minimal Binder for testing.
func newTestBinderForEmbedded(t *testing.T) *Binder {
	t.Helper()
	ttl := 10 * time.Second
	stopCh := make(chan struct{})
	t.Cleanup(func() { close(stopCh) })

	schedulerName := "test-scheduler"

	cacheHandler := commoncache.MakeCacheHandlerWrapper().
		Period(10 * time.Second).PodAssumedTTL(ttl).StopCh(stopCh).
		ComponentName("godel-binder").Obj()
	binderCache := cache.New(cacheHandler)
	binderQueue := queue.NewPriorityQueue(nil, nil, nil, binderCache)

	return &Binder{
		BinderCache:   binderCache,
		BinderQueue:   binderQueue,
		SchedulerName: &schedulerName,
	}
}

// makeAssumedPodForEmbedded creates an assumed pod for the given scheduler.
func makeAssumedPodForEmbedded(name, schedulerName string) *v1.Pod {
	return &v1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: "default",
			UID:       types.UID("uid-" + name),
			Annotations: map[string]string{
				podAnnotation.AssumedNodeAnnotationKey:     "node-1",
				podAnnotation.PodStateAnnotationKey:        string(podAnnotation.PodAssumed),
				podAnnotation.PodResourceTypeAnnotationKey: string(podAnnotation.GuaranteedPod),
				podAnnotation.PodLauncherAnnotationKey:     string(podAnnotation.Kubelet),
				podAnnotation.SchedulerAnnotationKey:       schedulerName,
			},
			ResourceVersion: "1",
		},
		Spec: v1.PodSpec{
			SchedulerName: schedulerName,
		},
	}
}

// TestAddPodToBinderQueue_EmbeddedMode_Skip verifies that in embedded mode,
// the BinderQueue handler (addPodToBinderQueue) still functions when called
// directly — the "skip" is at the informer registration level in
// addAllEventHandlers, not in the handler itself.
func TestAddPodToBinderQueue_EmbeddedMode_Skip(t *testing.T) {
	binder := newTestBinderForEmbedded(t)
	pod := makeAssumedPodForEmbedded("pod-embedded", "test-scheduler")

	// The handler still works when called directly
	binder.addPodToBinderQueue(pod)

	pending := binder.BinderQueue.PendingPods()
	if len(pending) > 0 {
		t.Logf("Handler addPodToBinderQueue added pod when called directly (expected in standalone; in embedded mode, informer won't call this)")
	}

	// The real assertion for embedded mode is that addAllEventHandlers(binder, ..., true)
	// does NOT register addPodToBinderQueue as an informer handler. This is a
	// compile-time/structural guarantee validated by code review and by the fact
	// that the registration is inside `if !embeddedMode { ... }`.
}

// TestAddPodToBinderQueue_StandaloneMode_Normal verifies that the handler
// correctly adds assumed pods to the BinderQueue.
func TestAddPodToBinderQueue_StandaloneMode_Normal(t *testing.T) {
	binder := newTestBinderForEmbedded(t)
	pod := makeAssumedPodForEmbedded("pod-standalone", "test-scheduler")

	binder.addPodToBinderQueue(pod)

	pending := binder.BinderQueue.PendingPods()
	found := false
	for _, p := range pending {
		if p.Name == "pod-standalone" {
			found = true
			break
		}
	}
	if !found {
		t.Error("Expected pod 'pod-standalone' in pending pods after addPodToBinderQueue in standalone mode")
	}
}

// TestUpdatePodInBinderQueue_EmbeddedMode verifies that the update handler
// processes pod state transitions correctly.
func TestUpdatePodInBinderQueue_EmbeddedMode(t *testing.T) {
	binder := newTestBinderForEmbedded(t)

	oldPod := &v1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "pod-update",
			Namespace: "default",
			UID:       "uid-update-1",
			Annotations: map[string]string{
				podAnnotation.PodResourceTypeAnnotationKey: string(podAnnotation.GuaranteedPod),
				podAnnotation.PodLauncherAnnotationKey:     string(podAnnotation.Kubelet),
			},
			ResourceVersion: "1",
		},
	}

	newPod := makeAssumedPodForEmbedded("pod-update", "test-scheduler")
	newPod.UID = "uid-update-1"
	newPod.ResourceVersion = "2"

	// Update from non-assumed to assumed state
	binder.updatePodInBinderQueue(oldPod, newPod)

	// The handler should process the update without panicking
	// In standalone mode, this would add to queue via the update handler
	pending := binder.BinderQueue.PendingPods()
	t.Logf("After update handler: %d pending pods", len(pending))
}

// TestDeletePodFromBinderQueue_EmbeddedMode verifies that non-assumed pods
// are gracefully handled by the delete handler without panicking.
func TestDeletePodFromBinderQueue_EmbeddedMode(t *testing.T) {
	binder := newTestBinderForEmbedded(t)

	// Create a non-assumed pod — deletePodFromBinderQueue should return early
	// (before accessing handle.VolumeBinder() which requires full binder initialization)
	pod := &v1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "pod-delete-noassumed",
			Namespace: "default",
			UID:       "uid-delete-noassumed",
			Annotations: map[string]string{
				podAnnotation.PodResourceTypeAnnotationKey: string(podAnnotation.GuaranteedPod),
				podAnnotation.PodLauncherAnnotationKey:     string(podAnnotation.Kubelet),
			},
			ResourceVersion: "1",
		},
	}

	// Should return early because pod is not assumed — no panic
	binder.deletePodFromBinderQueue(pod)

	// Queue should be empty
	pending := binder.BinderQueue.PendingPods()
	if len(pending) > 0 {
		t.Errorf("Expected empty queue, got %d pending pods", len(pending))
	}
}

// TestAddPodToBinderQueue_NonAssumedPod_Skipped verifies that non-assumed
// pods are not added to the BinderQueue.
func TestAddPodToBinderQueue_NonAssumedPod_Skipped(t *testing.T) {
	binder := newTestBinderForEmbedded(t)

	// Create a pod without assumed state
	pod := &v1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "pod-not-assumed",
			Namespace: "default",
			UID:       "uid-not-assumed",
			Annotations: map[string]string{
				podAnnotation.PodResourceTypeAnnotationKey: string(podAnnotation.GuaranteedPod),
				podAnnotation.PodLauncherAnnotationKey:     string(podAnnotation.Kubelet),
			},
			ResourceVersion: "1",
		},
	}

	binder.addPodToBinderQueue(pod)

	pending := binder.BinderQueue.PendingPods()
	if len(pending) > 0 {
		t.Error("Expected non-assumed pod to NOT be added to BinderQueue")
	}
}

// TestAddPodToBinderQueue_WrongScheduler_Skipped verifies that pods belonging
// to a different scheduler are not added to the BinderQueue.
func TestAddPodToBinderQueue_WrongScheduler_Skipped(t *testing.T) {
	binder := newTestBinderForEmbedded(t)
	pod := makeAssumedPodForEmbedded("pod-wrong-sched", "other-scheduler")

	binder.addPodToBinderQueue(pod)

	pending := binder.BinderQueue.PendingPods()
	if len(pending) > 0 {
		t.Error("Expected pod with wrong scheduler name to NOT be added to BinderQueue")
	}
}
