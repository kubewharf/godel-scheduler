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

package scheduler

import (
	v1 "k8s.io/api/core/v1"
	utilfeature "k8s.io/apiserver/pkg/util/feature"
	"k8s.io/klog/v2"

	"github.com/kubewharf/godel-scheduler/pkg/features"
	framework "github.com/kubewharf/godel-scheduler/pkg/framework/api"
	"github.com/kubewharf/godel-scheduler/pkg/util"
	podutil "github.com/kubewharf/godel-scheduler/pkg/util/pod"
	"github.com/kubewharf/godel-scheduler/pkg/util/tracing"
)

func (sched *Scheduler) assumedOrBoundPod(pod *v1.Pod) bool {
	return podutil.BoundPod(pod) || podutil.AssumedPodOfGodel(pod, *sched.SchedulerName)
}

func (sched *Scheduler) dispatchedPodOfThisScheduler(pod *v1.Pod) bool {
	return podutil.DispatchedPodOfGodel(pod, *sched.SchedulerName) && podutil.DispatchedPodOfThisScheduler(pod, sched.Name)
}

func (sched *Scheduler) triggerQueueOnAssumedOrBoundPodAdd(pod *v1.Pod) error {
	sched.ScheduleSwitch.Process(
		ParseSwitchTypeForPod(pod),
		func(dataSet ScheduleDataSet) {
			dataSet.SchedulingQueue().AssignedPodAdded(pod)
		},
	)
	return nil
}

func (sched *Scheduler) triggerQueueOnAssumedOrBoundPodUpdate(oldPod, newPod *v1.Pod) error {
	sched.ScheduleSwitch.Process(
		ParseSwitchTypeForPod(newPod),
		func(dataSet ScheduleDataSet) {
			dataSet.SchedulingQueue().AssignedPodUpdated(newPod)
		},
	)
	return nil
}

func (sched *Scheduler) triggerQueueOnAssumedOrBoundPodDelete(pod *v1.Pod) error {
	sched.ScheduleSwitch.Process(
		ParseSwitchTypeForPod(pod),
		func(dataSet ScheduleDataSet) {
			dataSet.SchedulingQueue().MoveAllToActiveOrBackoffQueue(util.AssignedPodDelete)
		},
	)
	return nil
}

func (sched *Scheduler) addDispatchedPodToQueue(pod *v1.Pod) error {
	podProperty := framework.ExtractPodProperty(pod)
	parentSpanContext := tracing.GetSpanContextFromPod(pod)
	traceContext, _ := tracing.StartSpanForPodWithParentSpan(
		podutil.GeneratePodKey(pod),
		"scheduler::addPodToSchedulingQueue",
		parentSpanContext,
		tracing.WithComponent("scheduler"),
		tracing.WithScheduler(sched.Name),
		podProperty.ConvertToTracingTags(),
	)
	defer func() {
		go traceContext.Finish()
	}()

	if utilfeature.DefaultFeatureGate.Enabled(features.SchedulerSubClusterConcurrentScheduling) {
		subCluster := pod.Spec.NodeSelector[framework.GetGlobalSubClusterKey()]
		idx, exist := framework.GetOrCreateClusterIndex(subCluster)
		if !exist {
			// ATTENTION: It is possible to be called before `sched.Run`, so we don't run workflow immediately.
			sched.createSubClusterWorkflow(idx, subCluster)
		}
	}

	sched.ScheduleSwitch.Process(
		ParseSwitchTypeForPod(pod),
		func(dataSet ScheduleDataSet) {
			if err := dataSet.SchedulingQueue().Add(pod); err != nil {
				klog.InfoS("Failed to add pod to scheduler queue", "err", err)
			}
		},
	)
	return nil
}

func (sched *Scheduler) updateDispatchedPodInQueue(oldPod, newPod *v1.Pod) error {
	if sched.skipPodUpdate(newPod) {
		return nil
	}
	podProperty := framework.ExtractPodProperty(newPod)
	parentSpanContext := tracing.GetSpanContextFromPod(newPod)
	traceContext, _ := tracing.StartSpanForPodWithParentSpan(
		podutil.GetPodKey(newPod),
		"scheduler::updatePodInSchedulingQueue",
		parentSpanContext,
		tracing.WithComponent("scheduler"),
		tracing.WithScheduler(sched.Name),
		podProperty.ConvertToTracingTags(),
	)
	defer func() {
		go traceContext.Finish()
	}()

	if utilfeature.DefaultFeatureGate.Enabled(features.SchedulerSubClusterConcurrentScheduling) {
		// If the sub-cluster changes, it should be removed from the old sub-cluster.
		// ATTENTION: This should not happen, but be careful in case
		{
			oldSt, newSt := ParseSwitchTypeForPod(oldPod), ParseSwitchTypeForPod(newPod)
			if oldSt != newSt {
				sched.ScheduleSwitch.Process(
					oldSt,
					func(dataSet ScheduleDataSet) {
						if err := dataSet.SchedulingQueue().Delete(oldPod); err != nil {
							klog.InfoS("Failed to update new pod: deleted oldPod from another workflow", "newPod", klog.KObj(newPod), "oldPod", klog.KObj(oldPod), "err", err)
						}
					},
				)
			}
		}

		subCluster := newPod.Spec.NodeSelector[framework.GetGlobalSubClusterKey()]
		idx, exist := framework.GetOrCreateClusterIndex(subCluster)
		if !exist {
			// ATTENTION: It is possible to be called before `sched.Run`, so we don't run workflow immediately.
			sched.createSubClusterWorkflow(idx, subCluster)
		}
	}

	sched.ScheduleSwitch.Process(
		ParseSwitchTypeForPod(newPod),
		func(dataSet ScheduleDataSet) {
			// TODO: handle special case: newPod belongs to unit and oldPod belongs to Pod. vice versa.
			if err := dataSet.SchedulingQueue().Update(oldPod, newPod); err != nil {
				klog.InfoS("Failed to update new pod", "newPod", klog.KObj(newPod), "err", err)
			}
		},
	)
	return nil
}

func (sched *Scheduler) deleteDispatchedPodFromQueue(pod *v1.Pod) error {
	podProperty := framework.ExtractPodProperty(pod)
	parentSpanContext := tracing.GetSpanContextFromPod(pod)
	traceContext, _ := tracing.StartSpanForPodWithParentSpan(
		podutil.GetPodKey(pod),
		"scheduler::deletePodFromSchedulingQueue",
		parentSpanContext,
		tracing.WithComponent("scheduler"),
		tracing.WithScheduler(sched.Name),
		podProperty.ConvertToTracingTags(),
	)
	defer func() {
		go traceContext.Finish()
	}()

	sched.ScheduleSwitch.Process(
		ParseSwitchTypeForPod(pod),
		func(dataSet ScheduleDataSet) {
			// TODO: handle special case: newPod belongs to unit and oldPod belongs to Pod. vice versa.
			if err := dataSet.SchedulingQueue().Delete(pod); err != nil {
				klog.InfoS("Failed to dequeue", "pod", klog.KObj(pod), "err", err)
			}
		},
	)
	return nil
}

func (sched *Scheduler) addPod(obj interface{}) {
	pod, err := podutil.ConvertToPod(obj)
	if err != nil {
		klog.InfoS("Failed to add pod", "err", err, "pod", klog.KObj(pod))
		return
	}

	klog.V(3).InfoS("Detected an Add event for pod", "state", podutil.GetPodState(pod.Annotations), "pod", podutil.GetPodKey(pod))

	if err := sched.commonCache.AddPod(pod); err != nil {
		klog.InfoS("Failed to add pod to scheduler cache", "err", err)
	}
	if sched.assumedOrBoundPod(pod) {
		sched.triggerQueueOnAssumedOrBoundPodAdd(pod)
	} else if sched.dispatchedPodOfThisScheduler(pod) {
		sched.addDispatchedPodToQueue(pod)
	}
}

func (sched *Scheduler) updatePod(oldObj, newObj interface{}) {
	oldPod, err := podutil.ConvertToPod(oldObj)
	if err != nil {
		klog.InfoS("Failed to update pod with oldObj", "err", err)
		return
	}
	newPod, err := podutil.ConvertToPod(newObj)
	if err != nil {
		klog.InfoS("Failed to update pod with newObj", "err", err)
		return
	}

	klog.V(3).InfoS("Detected an Update event for pod", "pod", klog.KObj(newPod))

	// Corresponding cache operations
	if err := sched.updatePodInCache(oldPod, newPod); err != nil {
		klog.InfoS("Failed to update pod in scheduler cache", "err", err)
	}

	// Corresponding Queue operations: add, update, or delete the pod from scheduler queues if the pod is in Dispatched status
	{
		podutil.FilteringUpdate(sched.dispatchedPodOfThisScheduler, sched.addDispatchedPodToQueue, sched.updateDispatchedPodInQueue,
			sched.deleteDispatchedPodFromQueue, oldPod, newPod)
	}
}

func (sched *Scheduler) updatePodInCache(oldPod *v1.Pod, newPod *v1.Pod) error {
	// First, updating pod information in the scheduler cache
	if err := sched.commonCache.UpdatePod(oldPod, newPod); err != nil {
		return err
	}

	// Second, as a cascading operation we need to update queues; because as the pod state changes, some unschedulable pods may become schedulable.
	// For example, due to pod affinity-related reasons, a pod in the unschedulable queue becomes schedulable, then this unschedulable pod can be moved from the unschedulable queue to the ready or backoff queue for scheduling retries.
	podutil.FilteringUpdate(sched.assumedOrBoundPod, sched.triggerQueueOnAssumedOrBoundPodAdd, sched.triggerQueueOnAssumedOrBoundPodUpdate,
		sched.triggerQueueOnAssumedOrBoundPodDelete, oldPod, newPod)

	return nil
}

func (sched *Scheduler) deletePod(obj interface{}) {
	pod, err := podutil.ConvertToPod(obj)
	if err != nil {
		klog.InfoS("Failed to delete pod", "err", err)
		return
	}

	klog.V(3).InfoS("Detected a Delete event for pod", "state", podutil.GetPodState(pod.Annotations), "pod", podutil.GetPodKey(pod))

	if err = sched.commonCache.DeletePod(pod); err != nil {
		klog.InfoS("Scheduler cache RemovePod failed", "err", err)
	}
	if sched.assumedOrBoundPod(pod) {
		sched.triggerQueueOnAssumedOrBoundPodDelete(pod)
	} else {
		// just in case
		if err == nil {
			sched.ScheduleSwitch.Process(
				ParseSwitchTypeForPod(pod),
				func(dataSet ScheduleDataSet) {
					dataSet.SchedulingQueue().MoveAllToActiveOrBackoffQueue(util.AssignedPodDelete)
				},
			)
		}
	}

	if sched.dispatchedPodOfThisScheduler(pod) {
		sched.deleteDispatchedPodFromQueue(pod)
	}

	// TODO: we may need to take victims throttle into account here
}
