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

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/util/wait"
	clientset "k8s.io/client-go/kubernetes"
	"k8s.io/client-go/util/workqueue"
	"k8s.io/klog/v2"

	"github.com/kubewharf/godel-scheduler/pkg/binder/utils"
	"github.com/kubewharf/godel-scheduler/pkg/framework/api"
)

type BinderTasksReconciler struct {
	// client syncs K8S object
	client clientset.Interface

	APICallFailedTaskQueue workqueue.RateLimitingInterface

	stop chan struct{}
}

type FailedReason string

const (
	RejectFailed FailedReason = "RejectFailed"
	// TODO: revisit later: for now, if bind fails, we add tasks to failed list and reject
)

type APICallFailedTask struct {
	reason FailedReason
	qpi    *api.QueuedPodInfo
}

func NewBinderTaskReconciler(client clientset.Interface) *BinderTasksReconciler {
	return &BinderTasksReconciler{
		client:                 client,
		APICallFailedTaskQueue: workqueue.NewNamedRateLimitingQueue(workqueue.NewItemExponentialFailureRateLimiter(5*time.Millisecond, 10*time.Second), "binder-failed-task-queue"),
		stop:                   make(chan struct{}),
	}
}

func (btr *BinderTasksReconciler) AddFailedTask(ft *APICallFailedTask) {
	btr.APICallFailedTaskQueue.Add(ft)
}

func (btr *BinderTasksReconciler) Run() {
	go wait.Until(btr.APICallFailedWorker, time.Second, btr.stop)
}

func (btr *BinderTasksReconciler) Close() {
	close(btr.stop)
}

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
				if err := utils.CleanupPodAnnotations(btr.client, cft.qpi.Pod); err != nil {
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
