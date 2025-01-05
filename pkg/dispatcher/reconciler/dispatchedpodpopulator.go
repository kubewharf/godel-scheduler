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

	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/util/wait"
	v1 "k8s.io/client-go/listers/core/v1"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/util/workqueue"
	"k8s.io/klog/v2"

	schemaintainer "github.com/kubewharf/godel-scheduler/pkg/dispatcher/scheduler-maintainer"
	podutil "github.com/kubewharf/godel-scheduler/pkg/util/pod"
)

// DispatchedPodsPopulator is the populator struct for collecting dispatched pods who are in inactive schedulers
// DispatchedPodsPopulator 是 populator 结构，用于收集处于非活动调度器中的已调度 pod。
type DispatchedPodsPopulator struct {
	schedulerName            string
	podLister                v1.PodLister
	staleDispatchedPodsQueue workqueue.Interface
	schedulerMaintainer      *schemaintainer.SchedulerMaintainer
}

// NewDispatchedPodsPopulator creates a new DispatchedPodsPopulator struct
func NewDispatchedPodsPopulator(schedulerName string, podLister v1.PodLister, queue workqueue.Interface,
	maintainer *schemaintainer.SchedulerMaintainer,
) *DispatchedPodsPopulator {
	return &DispatchedPodsPopulator{
		schedulerName:            schedulerName,
		podLister:                podLister,
		staleDispatchedPodsQueue: queue,
		schedulerMaintainer:      maintainer,
	}
}

// Run starts the collection work
func (dpp *DispatchedPodsPopulator) Run(stopCh <-chan struct{}) {
	// 收集并入队所有分配给非活动调度器的已调度 Pod。
	go wait.Until(dpp.collectDispatchedPodsInInactiveSchedulers, 5*time.Minute, stopCh)
}

// collectDispatchedPodsInInactiveSchedulers enqueues all dispatched pods who are assigned to inactive schedulers
// 收集并入队所有分配给非活动调度器的已调度 Pod。
func (dpp *DispatchedPodsPopulator) collectDispatchedPodsInInactiveSchedulers() {
	// TODO: add indexers for PodState and Scheduler annotations
	pods, err := dpp.podLister.List(labels.Everything())
	if err != nil {
		klog.InfoS("Failed to list pods in the dispatched pods populator", "err", err)
		return
	}

	for _, pod := range pods {
		if podutil.DispatchedPodOfGodel(pod, dpp.schedulerName) {
			schedulerName := pod.Annotations[podutil.SchedulerAnnotationKey]
			if dpp.schedulerMaintainer.IsSchedulerInInactiveQueue(schedulerName) || !dpp.schedulerMaintainer.SchedulerExist(schedulerName) {
				if podKey, err := cache.MetaNamespaceKeyFunc(pod); err != nil {
					klog.InfoS("Failed to get the pod key in the dispatched pods populator", "pod", klog.KObj(pod), "err", err)
				} else {
					dpp.staleDispatchedPodsQueue.Add(podKey)
				}
			}
		}
	}
}
