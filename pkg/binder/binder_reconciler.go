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
	"time"

	v1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/util/wait"
	clientset "k8s.io/client-go/kubernetes"
	"k8s.io/client-go/util/workqueue"
	"k8s.io/klog/v2"

	"github.com/kubewharf/godel-scheduler/pkg/binder/utils"
	"github.com/kubewharf/godel-scheduler/pkg/framework/api"
)

// BinderTasksReconciler processes asynchronous failed-task cleanup actions
// (e.g. re-patching Pod annotations after a bind failure). It runs a single
// worker goroutine that drains a rate-limited work queue.
type BinderTasksReconciler struct {
	// client syncs K8S object
	client clientset.Interface

	APICallFailedTaskQueue workqueue.RateLimitingInterface

	// schedulerName identifies the Scheduler that owns this reconciler.
	// Used when falling back to Dispatcher via CleanupPodAnnotationsWithRetryCount.
	schedulerName string

	// maxLocalRetries is the threshold for dispatching back to the Dispatcher.
	// A value of 0 disables the fallback (always retries locally).
	maxLocalRetries int

	stop chan struct{}
}

// FailedReason enumerates the reasons a task was added to the reconciler queue.
type FailedReason string

const (
	RejectFailed FailedReason = "RejectFailed"
	// TODO: revisit later: for now, if bind fails, we add tasks to failed list and reject
)

// APICallFailedTask represents a failed API operation that must be retried.
type APICallFailedTask struct {
	reason FailedReason
	qpi    *api.QueuedPodInfo
}

// NewBinderTaskReconciler creates a reconciler that uses the original
// CleanupPodAnnotations (no retry-count awareness). This preserves the
// standalone Binder behaviour.
func NewBinderTaskReconciler(client clientset.Interface) *BinderTasksReconciler {
	return &BinderTasksReconciler{
		client:                 client,
		APICallFailedTaskQueue: workqueue.NewNamedRateLimitingQueue(workqueue.NewItemExponentialFailureRateLimiter(5*time.Millisecond, 10*time.Second), "binder-failed-task-queue"),
		stop:                   make(chan struct{}),
	}
}

// NewBinderTaskReconcilerWithRetry creates a reconciler that is aware of the
// bind-failure count on each Pod. When the cumulative failure count reaches
// maxLocalRetries, the reconciler dispatches the Pod back to the Dispatcher
// (via CleanupPodAnnotationsWithRetryCount) instead of the same Scheduler.
func NewBinderTaskReconcilerWithRetry(client clientset.Interface, schedulerName string, maxLocalRetries int) *BinderTasksReconciler {
	return &BinderTasksReconciler{
		client:                 client,
		schedulerName:          schedulerName,
		maxLocalRetries:        maxLocalRetries,
		APICallFailedTaskQueue: workqueue.NewNamedRateLimitingQueue(workqueue.NewItemExponentialFailureRateLimiter(5*time.Millisecond, 10*time.Second), "binder-failed-task-queue"),
		stop:                   make(chan struct{}),
	}
}

// AddFailedTask enqueues a failed task for asynchronous retry.
func (btr *BinderTasksReconciler) AddFailedTask(ft *APICallFailedTask) {
	btr.APICallFailedTaskQueue.Add(ft)
}

// Run starts the reconciler worker loop. It returns immediately; the
// worker runs in a background goroutine until Close() is called.
func (btr *BinderTasksReconciler) Run() {
	go wait.Until(btr.APICallFailedWorker, time.Second, btr.stop)
}

// Close shuts down the reconciler. It is safe to call multiple times.
func (btr *BinderTasksReconciler) Close() {
	close(btr.stop)
}

// Len returns the current length of the failed-task queue.
func (btr *BinderTasksReconciler) Len() int {
	return btr.APICallFailedTaskQueue.Len()
}

// cleanupPod applies the appropriate annotation-cleanup strategy for the
// given Pod. When retry-count awareness is configured (maxLocalRetries > 0
// and schedulerName != ""), it uses CleanupPodAnnotationsWithRetryCount;
// otherwise it falls back to the original CleanupPodAnnotations.
func (btr *BinderTasksReconciler) cleanupPod(pod *v1.Pod) error {
	if btr.maxLocalRetries > 0 && btr.schedulerName != "" {
		return utils.CleanupPodAnnotationsWithRetryCount(btr.client, pod, btr.schedulerName, btr.maxLocalRetries)
	}
	return utils.CleanupPodAnnotations(btr.client, pod)
}

// APICallFailedWorker is the main processing loop for the reconciler.
// It processes one item at a time: on success the item is removed from the
// queue, on transient failure it is re-enqueued with exponential backoff.
func (btr *BinderTasksReconciler) APICallFailedWorker() {
	workFunc := func() bool {
		obj, quit := btr.APICallFailedTaskQueue.Get()
		if quit {
			return true
		}
		defer btr.APICallFailedTaskQueue.Done(obj)

		cft, ok := obj.(*APICallFailedTask)
		if !ok {
			// can not convert to APICallFailedTask, don't add it back, just ignore this
			klog.InfoS("Failed to convert obj to APICallFailedTask", "object", obj)
			return false
		}

		if cft.reason == RejectFailed {
			if cft.qpi.Pod != nil {
				// reject failed tasks should have been removed from cache,
				// so don't need to delete pod markers and forget pod
				if err := btr.cleanupPod(cft.qpi.Pod); err != nil {
					if apierrors.IsNotFound(err) {
						// Do not add back to task queue again.
						return false
					}
					klog.InfoS("Failed to clean up pod annotations", "pod", klog.KObj(cft.qpi.Pod), "err", err)
					btr.APICallFailedTaskQueue.AddRateLimited(cft)
				}
			}
			return false
		}
		return false
	}
	for {
		if quit := workFunc(); quit {
			klog.InfoS("Failed calling API, task worker queue shutting down")
			return
		}
	}
}
