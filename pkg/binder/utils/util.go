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

package utils

import (
	"time"

	v1 "k8s.io/api/core/v1"
	clientset "k8s.io/client-go/kubernetes"

	"github.com/kubewharf/godel-scheduler/pkg/binder/metrics"
	framework "github.com/kubewharf/godel-scheduler/pkg/framework/api"
	"github.com/kubewharf/godel-scheduler/pkg/util"
	podutil "github.com/kubewharf/godel-scheduler/pkg/util/pod"
)

// cleanupSchedulingAnnotations removes all scheduling-decision annotations
// from the given Pod copy. This is a shared helper used by both the original
// CleanupPodAnnotations and the enhanced CleanupPodAnnotationsWithRetryCount.
func cleanupSchedulingAnnotations(podCopy *v1.Pod) {
	delete(podCopy.Annotations, podutil.AssumedNodeAnnotationKey)
	delete(podCopy.Annotations, podutil.AssumedCrossNodeAnnotationKey)
	delete(podCopy.Annotations, podutil.NominatedNodeAnnotationKey)
	delete(podCopy.Annotations, podutil.FailedSchedulersAnnotationKey)
	delete(podCopy.Annotations, podutil.MicroTopologyKey)
	delete(podCopy.Annotations, podutil.MovementNameKey)
	delete(podCopy.Annotations, podutil.MatchedReservationPlaceholderKey)
}

// CleanupPodAnnotations resets all scheduling-decision annotations and sets
// the Pod state back to Dispatched so that the same Scheduler can re-schedule it.
// This preserves the original behaviour before the embedded-Binder enhancement.
func CleanupPodAnnotations(client clientset.Interface, pod *v1.Pod) error {
	podCopy := pod.DeepCopy()
	if podCopy.Annotations == nil {
		podCopy.Annotations = map[string]string{}
	}

	cleanupSchedulingAnnotations(podCopy)

	// reset pod state to dispatched
	podCopy.Annotations[podutil.PodStateAnnotationKey] = string(podutil.PodDispatched)

	startTime := time.Now()
	err := util.PatchPod(client, pod, podCopy)
	if err != nil {
		metrics.PodOperatingLatencyObserve(framework.ExtractPodProperty(pod), metrics.FailureResult, metrics.PatchPod, metrics.SinceInSeconds(startTime))
		return err
	}
	metrics.PodOperatingLatencyObserve(framework.ExtractPodProperty(pod), metrics.SuccessResult, metrics.PatchPod, metrics.SinceInSeconds(startTime))
	return nil
}

// CleanupPodAnnotationsWithRetryCount behaves like CleanupPodAnnotations but
// checks the cumulative bind-failure count (stored in the Pod's annotations)
// against maxLocalRetries. When the threshold is reached or exceeded, the Pod
// is dispatched back to the Dispatcher (state = Pending, Scheduler annotation
// removed) so that it can be re-assigned to a different Scheduler. Otherwise
// the Pod is sent back to the same Scheduler (state = Dispatched).
//
// A maxLocalRetries value of 0 disables the Dispatcher fallback and always
// returns the Pod to the same Scheduler.
func CleanupPodAnnotationsWithRetryCount(client clientset.Interface, pod *v1.Pod, schedulerName string, maxLocalRetries int) error {
	podCopy := pod.DeepCopy()
	if podCopy.Annotations == nil {
		podCopy.Annotations = map[string]string{}
	}

	cleanupSchedulingAnnotations(podCopy)

	if ShouldDispatchToAnotherScheduler(podCopy, maxLocalRetries) {
		// Exceeded local retries – dispatch back to Dispatcher.
		// Record the current scheduler in the failed-schedulers list.
		failedSchedulers := podCopy.Annotations[podutil.FailedSchedulersAnnotationKey]
		if failedSchedulers == "" {
			failedSchedulers = schedulerName
		} else {
			failedSchedulers = failedSchedulers + "," + schedulerName
		}
		podCopy.Annotations[podutil.FailedSchedulersAnnotationKey] = failedSchedulers
		podCopy.Annotations[podutil.PodStateAnnotationKey] = string(podutil.PodPending)
		delete(podCopy.Annotations, podutil.SchedulerAnnotationKey)
	} else {
		// Still within retry budget – return to same Scheduler.
		podCopy.Annotations[podutil.PodStateAnnotationKey] = string(podutil.PodDispatched)
	}

	startTime := time.Now()
	err := util.PatchPod(client, pod, podCopy)
	if err != nil {
		metrics.PodOperatingLatencyObserve(framework.ExtractPodProperty(pod), metrics.FailureResult, metrics.PatchPod, metrics.SinceInSeconds(startTime))
		return err
	}
	metrics.PodOperatingLatencyObserve(framework.ExtractPodProperty(pod), metrics.SuccessResult, metrics.PatchPod, metrics.SinceInSeconds(startTime))
	return nil
}
