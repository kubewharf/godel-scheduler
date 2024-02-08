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

package reconciler

import (
	"time"

	v1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/util/wait"
	clientset "k8s.io/client-go/kubernetes"
	corelisters "k8s.io/client-go/listers/core/v1"
	"k8s.io/client-go/util/workqueue"
	"k8s.io/klog/v2"

	framework "github.com/kubewharf/godel-scheduler/pkg/framework/api"
	godelcache "github.com/kubewharf/godel-scheduler/pkg/scheduler/cache"
	"github.com/kubewharf/godel-scheduler/pkg/util"
	podutil "github.com/kubewharf/godel-scheduler/pkg/util/pod"
)

type FailedTaskReconciler struct {
	schedulerName            string
	takeOverDefaultScheduler bool
	// client syncs K8S object
	client clientset.Interface

	podLister corelisters.PodLister

	failedPatchTaskQueue workqueue.RateLimitingInterface

	schedulerCache godelcache.SchedulerCache

	stop chan struct{}
}

type FailedPatchTask struct {
	podInfo *framework.CachePodInfo
}

func NewFailedPatchTask(podInfo *framework.CachePodInfo) *FailedPatchTask {
	return &FailedPatchTask{podInfo: podInfo}
}

func NewFailedTaskReconciler(client clientset.Interface, podLister corelisters.PodLister,
	schedulerCache godelcache.SchedulerCache, schedulerName string,
	takeOverDefaultScheduler bool,
) *FailedTaskReconciler {
	return &FailedTaskReconciler{
		schedulerName:            schedulerName,
		takeOverDefaultScheduler: takeOverDefaultScheduler,
		client:                   client,
		podLister:                podLister,
		failedPatchTaskQueue:     workqueue.NewNamedRateLimitingQueue(workqueue.NewItemExponentialFailureRateLimiter(5*time.Millisecond, 5*time.Second), "task-queue"),
		schedulerCache:           schedulerCache,
		stop:                     make(chan struct{}),
	}
}

func (re *FailedTaskReconciler) AddFailedTask(fpt *FailedPatchTask) {
	re.failedPatchTaskQueue.Add(fpt)
}

func (re *FailedTaskReconciler) Run() {
	go wait.Until(re.failedPatchTaskWorker, time.Second, re.stop)
}

func (re *FailedTaskReconciler) Close() {
	close(re.stop)
}

func (re *FailedTaskReconciler) failedPatchTaskWorker() {
	workFunc := func() bool {
		obj, quit := re.failedPatchTaskQueue.Get()
		if quit {
			return true
		}
		defer re.failedPatchTaskQueue.Done(obj)

		fpt := obj.(*FailedPatchTask)
		if fpt.podInfo == nil || fpt.podInfo.Pod == nil {
			klog.InfoS("WARN: the reserved pod was nil")
			return false
		}

		latestPod, err := re.podLister.Pods(fpt.podInfo.Pod.Namespace).Get(fpt.podInfo.Pod.Name)
		if goOn := re.checkPodState(latestPod, err, fpt); !goOn {
			return false
		}

		clonedPod := latestPod.DeepCopy()
		clonedPod.Annotations = fpt.podInfo.Pod.Annotations
		// try to patch again
		err = util.PatchPod(re.client, latestPod, clonedPod)
		if err != nil {
			klog.InfoS("Failed to patch pod in reconciler", "pod", klog.KObj(fpt.podInfo.Pod), "err", err)
			if apierrors.IsNotFound(err) {
				// Do not add back to task queue again.
				return false
			}
			re.failedPatchTaskQueue.Add(fpt)
			return false
		} else {
			// TODO: add event ?
		}
		return false
	}
	for {
		if quit := workFunc(); quit {
			klog.InfoS("Shut down the patch task worker of the FailedTaskReconciler")
			return
		}
	}
}

func (re *FailedTaskReconciler) checkPodState(latestPod *v1.Pod, err error, fpt *FailedPatchTask) (goOn bool) {
	if err != nil {
		if apierrors.IsNotFound(err) {
			// this pod is deleted, forget it from cache if it is still in assumed state in cache
			if err := re.schedulerCache.ForgetPod(fpt.podInfo); err != nil {
				klog.InfoS("Failed to forget pod", "pod", podutil.GetPodKey(fpt.podInfo.Pod), "err", err)
				re.failedPatchTaskQueue.Add(fpt)
			}
			return false
		}
		klog.InfoS("Failed to get pod in reconciler", "pod", klog.KObj(fpt.podInfo.Pod), "err", err)
		re.failedPatchTaskQueue.Add(fpt)
		return false
	}

	if !podutil.DispatchedPodOfGodel(latestPod, re.schedulerName, re.takeOverDefaultScheduler) {
		// pod is not in dispatched state, we need to forget the pod from cache no matter what state it is now.
		// if it is assumed or bound now, the pod will be added to cache and removed from assumed pods map
		// if it is pending now, we need to remove this pod from assumed pod map too.
		assumed, err := re.schedulerCache.IsAssumedPod(fpt.podInfo.Pod)
		if err != nil {
			klog.InfoS("Failed to check if this pod is an assumed pod", "pod", podutil.GetPodKey(fpt.podInfo.Pod), "err", err)
			re.failedPatchTaskQueue.Add(fpt)
			return false
		}
		if assumed {
			if err := re.schedulerCache.ForgetPod(fpt.podInfo); err != nil {
				klog.InfoS("Failed to forget pod", "pod", podutil.GetPodKey(fpt.podInfo.Pod), "err", err)
				re.failedPatchTaskQueue.Add(fpt)
			}
		}

		return false
	}

	return true
}
