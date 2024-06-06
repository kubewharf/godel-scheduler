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

package metrics

import (
	"github.com/kubewharf/godel-scheduler/pkg/framework/api"
	"github.com/kubewharf/godel-scheduler/pkg/util/helper"
)

func ObserveUnitBindingAttempts(unit *api.QueuedUnitInfo, successfulPods, failedPods int) {
	if unit == nil {
		return
	}

	up := unit.GetUnitProperty()
	if up == nil {
		return
	}

	p := up.GetPodProperty()
	if p == nil {
		return
	}

	if successfulPods > 0 {
		PodBindingAttemptsAdd(p, SuccessResult, float64(successfulPods))
	}

	if failedPods > 0 {
		PodBindingAttemptsAdd(p, FailureResult, float64(failedPods))
	}
}

func ObservePodBinderE2ELatency(podInfo *api.QueuedPodInfo) {
	if podInfo.Pod == nil || len(podInfo.Pod.Annotations) == 0 {
		return
	}

	podProperty := podInfo.GetPodProperty()
	if podProperty == nil {
		return
	}

	PodE2EBinderLatencyObserve(podProperty, helper.SinceInSeconds(podInfo.InitialAttemptTimestamp))
	E2eBinderLatencyQuantileObserve(podProperty, helper.SinceInSeconds(podInfo.InitialAttemptTimestamp))
}

func ObserveBindingStageDuration(podProperty *api.PodProperty, operation, plugin, status string, duration float64) {
	BindingStageDurationObserve(podProperty, operation, plugin, status, duration)
}

func ObserveMovementUpdateAttempts(rescheduleAlgorithm, updateResult, suggestResult string) {
	movementUpdateAttempts.WithLabelValues(rescheduleAlgorithm, updateResult, suggestResult).Inc()
}

func ObserveRejectUnit(unit *api.QueuedUnitInfo, stage string) {
	minMember, _ := unit.GetMinMember()
	up := unit.GetUnitProperty()
	if up == nil {
		return
	}

	RejectUnitObserve(up, float64(minMember), stage)
	for _, podInfo := range unit.GetPods() {
		RejectPodInc(podInfo.GetPodProperty(), stage)
	}
}

func IncPreemptingAttempts(podProperty *api.PodProperty, success bool) {
	result := SuccessResult
	if !success {
		result = FailureResult
	}

	PreemptingAttemptsInc(podProperty, result)
}
