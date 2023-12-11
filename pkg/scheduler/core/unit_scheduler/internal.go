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
	"fmt"
	"strconv"
	"strings"
	"time"

	v1 "k8s.io/api/core/v1"
	clientset "k8s.io/client-go/kubernetes"
	"k8s.io/klog/v2"

	framework "github.com/kubewharf/godel-scheduler/pkg/framework/api"
	"github.com/kubewharf/godel-scheduler/pkg/scheduler/metrics"
	"github.com/kubewharf/godel-scheduler/pkg/util"
	"github.com/kubewharf/godel-scheduler/pkg/util/helper"
	podutil "github.com/kubewharf/godel-scheduler/pkg/util/pod"
	"github.com/kubewharf/godel-scheduler/pkg/util/tracing"
)

const (
	// RetryAttempts is the maximum times we will retry for one operation (probably an API call)
	RetryAttempts = 3
	// RetryWaitTime is the waiting time duration before we retry for a failed operation
	RetryWaitTime = 1 * time.Second
)

// ------------------------------------------ Internal Functions ------------------------------------------

func getAndInitClonedPod(spanContext tracing.SpanContext, podInfo *framework.QueuedPodInfo) *v1.Pod {
	clonedPod := podInfo.Pod.DeepCopy()

	{
		// fillClonedPodAnnotation
		if clonedPod.Annotations == nil {
			clonedPod.Annotations = make(map[string]string)
		}
		if clonedPod.Annotations[podutil.TraceContext] == "" {
			tracing.SetSpanContextForPod(clonedPod, spanContext)
		}
		if _, ok := clonedPod.Annotations[podutil.InitialHandledTimestampAnnotationKey]; !ok {
			clonedPod.Annotations[podutil.InitialHandledTimestampAnnotationKey] = podInfo.InitialAttemptTimestamp.Format(helper.TimestampLayout)
		}
	}

	return clonedPod
}

func updateFailedSchedulingPod(cs clientset.Interface, schedulerName string,
	failedPod *v1.Pod, reEnqueue bool, err error, reason string,
) error {
	podCopy := failedPod.DeepCopy()
	if !reEnqueue {
		failedSchedulerSet := podutil.GetFailedSchedulersNames(failedPod)
		if !failedSchedulerSet.Has(schedulerName) {
			// add this scheduler to failed scheduler annotation
			failedSchedulerSet.Insert(schedulerName)
			podCopy.Annotations[podutil.FailedSchedulersAnnotationKey] = strings.Join(failedSchedulerSet.List(), ",")
		}
		// send the pod back to dispatcher for redispatching
		podCopy.Annotations[podutil.PodStateAnnotationKey] = string(podutil.PodPending)
		delete(podCopy.Annotations, podutil.SchedulerAnnotationKey)
	}
	delete(podCopy.Annotations, podutil.MicroTopologyKey)

	if util.GetPodDebugMode(failedPod) == util.DebugModeOn {
		delete(podCopy.Annotations, util.DebugModeAnnotationKey)
	}

	// mark failed pods to be e2e exclusive when calculating slo
	podCopy.Annotations[podutil.E2EExcludedPodAnnotationKey] = "true"

	// update pod via API server
	return updateFailedPodCondition(cs, failedPod, podCopy, reason, err)
}

func updateFailedPodCondition(cs clientset.Interface, pod, failed *v1.Pod, reason string, err error) error {
	var message string
	if err == nil {
		message = "scheduling failed"
	} else {
		message = err.Error()
	}
	podutil.UpdatePodCondition(&failed.Status, &v1.PodCondition{
		Type:    v1.PodScheduled,
		Status:  v1.ConditionFalse,
		Reason:  reason,
		Message: message,
	})

	updatingStart := time.Now()
	// update pod state to api server
	podProperty := framework.ExtractPodProperty(pod)
	if err := util.PatchPod(cs, pod, failed); err != nil {
		metrics.ObservePodUpdatingAttemptAndLatency(podProperty, metrics.FailureResult, helper.SinceInSeconds(updatingStart))
		return fmt.Errorf("error updating pod %s/%s: %v", pod.Namespace, pod.Name, err)
	}
	metrics.ObservePodUpdatingAttemptAndLatency(podProperty, metrics.SuccessResult, helper.SinceInSeconds(updatingStart))
	return nil
}

func getAttemptsLabel(p *framework.QueuedPodInfo) string {
	// We break down the pod scheduling duration by attempts capped to a limit
	// to avoid ending up with a high cardinality metric.
	if p.Attempts >= 15 {
		return "15+"
	}
	return strconv.Itoa(p.Attempts)
}

// inValidUnit check if the unit is empty or if the queued pod info is invalid in this unit
func inValidUnit(unitInfo *framework.QueuedUnitInfo) bool {
	if unitInfo == nil || unitInfo.ScheduleUnit == nil {
		return true
	}
	pods := unitInfo.GetPods()
	for _, podInfo := range pods {
		// this should not happen, but just in case
		if podInfo.Pod == nil {
			klog.InfoS("Pod was nil in queued pod info", "pod", podInfo, "unitKey", unitInfo.GetKey())
			return true
		}
	}

	return len(pods) == 0
}
