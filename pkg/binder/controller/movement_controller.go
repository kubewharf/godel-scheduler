/*
Copyright 2024 The Godel Scheduler Authors.

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

package controller

import (
	"time"

	godelclient "github.com/kubewharf/godel-scheduler-api/pkg/client/clientset/versioned"
	schedulinglisterv1a1 "github.com/kubewharf/godel-scheduler-api/pkg/client/listers/scheduling/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/util/workqueue"
	"k8s.io/klog/v2"

	binderutils "github.com/kubewharf/godel-scheduler/pkg/binder/utils"
	podutil "github.com/kubewharf/godel-scheduler/pkg/util/pod"
)

const MaxRetryAttempts int = 10

type CommonController interface {
	// Run starts the goroutines managing the queue.
	Run()
	// Close closes the queue so that the goroutine which is
	// waiting to pop items can exit gracefully.
	Close()
	// add pod into queue
	AddPod(pod *corev1.Pod)
}

var _ CommonController = &MovementController{}

type MovementController struct {
	queue          workqueue.RateLimitingInterface
	stopCh         <-chan struct{}
	movementLister schedulinglisterv1a1.MovementLister
	crdClient      godelclient.Interface
	// return (err error, needReEnqueue bool)
	syncFunc func(pod *corev1.Pod) bool
}

func NewMovementController(queue workqueue.RateLimitingInterface, stopCh <-chan struct{},
	movementLister schedulinglisterv1a1.MovementLister, crdClient godelclient.Interface,
) CommonController {
	controller := &MovementController{
		queue:          queue,
		stopCh:         stopCh,
		movementLister: movementLister,
		crdClient:      crdClient,
	}
	controller.syncFunc = func(pod *corev1.Pod) bool {
		// update movement status if necessary
		return binderutils.UpdateMovement(crdClient, movementLister, pod)
	}
	return controller
}

// Close closes the priority queue.
func (mc *MovementController) Close() {
	mc.queue.ShutDown()
}

func (mc *MovementController) AddPod(pod *corev1.Pod) {
	movementName := podutil.GetMovementNameFromPod(pod)
	if movementName == "" {
		return
	}
	mc.queue.Add(pod)
}

func (mc *MovementController) Run() {
	go wait.Until(mc.runMovementWorker, time.Second, mc.stopCh)
}

func (mc *MovementController) runMovementWorker() {
	for mc.processNextPod() {
	}
}

func (mc *MovementController) processNextPod() bool {
	obj, shutdown := mc.queue.Get()
	if shutdown {
		return false
	}

	defer mc.queue.Done(obj)
	if pod, ok := obj.(*corev1.Pod); !ok {
		mc.queue.Forget(obj)
		return true
	} else {
		if needReEnqueue := mc.syncFunc(pod); needReEnqueue {
			if mc.queue.NumRequeues(obj) < MaxRetryAttempts {
				mc.queue.AddRateLimited(obj)
				klog.InfoS("Re-enqueue pod to update movement", "pod", podutil.GetPodKey(pod))
				return true
			}
			klog.InfoS("Cannot re-enqueue pod to update movement because max retry attempts reached", "pod", podutil.GetPodKey(pod))
			mc.queue.Forget(obj)
			return true
		} else {
			mc.queue.Forget(obj)
			return true
		}
	}
}
