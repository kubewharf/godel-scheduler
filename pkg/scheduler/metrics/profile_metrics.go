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
	k8smetrics "k8s.io/component-base/metrics"

	"github.com/kubewharf/godel-scheduler/pkg/common/metrics"
	"github.com/kubewharf/godel-scheduler/pkg/framework/api"
)

type ScheduleResult string

var (
	// ScheduledResult is marked as successful scheduling with feasible nodes
	ScheduledResult ScheduleResult = "scheduled"

	// UnschedulableResult is marked as successful scheduling but no suitable nodes found, exclude preempting nomination
	UnschedulableResult ScheduleResult = "unschedulable"

	// NominatedResult is marked as successful preempting nomination
	NominatedResult ScheduleResult = "nominated"

	// NominatedSchedulingResult is marked as successful scheduling in preempting
	NominatedSchedulingResult ScheduleResult = "nominatedScheduling"

	// NominatedFailureResult is marked as uns
	NominatedFailureResult ScheduleResult = "nominatedFailure"

	// ErrorResult is marked as internal error, including scheduling and preempting nomination
	ErrorResult ScheduleResult = "error"
)

// PodScheduled can record a successful scheduling attempt and the duration
// since `start`.
func PodScheduled(podProperty *api.PodProperty, duration float64) {
	observeScheduleAttemptAndLatency(podProperty, string(ScheduledResult), duration)
}

// PodUnschedulable can record a scheduling attempt for an unschedulable pod
// and the duration since `start`.
func PodUnschedulable(podProperty *api.PodProperty, duration float64) {
	observeScheduleAttemptAndLatency(podProperty, string(UnschedulableResult), duration)
}

// PodScheduleError can record a scheduling attempt that had an error and the
// duration since `start`.
func PodScheduleError(podProperty *api.PodProperty, duration float64) {
	observeScheduleAttemptAndLatency(podProperty, string(ErrorResult), duration)
}

// PodNominated can record a successful preempting nomination attempt and the duration
// since `preemptingStart`.
func PodNominated(podProperty *api.PodProperty, duration float64) {
	observePreemptingAttemptAndLatency(podProperty, string(NominatedResult), duration)
}

// PodScheduledInPreempting can record a successful scheduling in preempting nomination attempt and the duration
// since `preemptingStart`.
func PodScheduledInPreempting(podProperty *api.PodProperty, duration float64) {
	observePreemptingAttemptAndLatency(podProperty, string(NominatedSchedulingResult), duration)
}

// PodNominatedFailure can record a failed preempting nomination attempt and the duration
// since `start`.
func PodNominatedFailure(podProperty *api.PodProperty, duration float64) {
	observePreemptingAttemptAndLatency(podProperty, string(NominatedFailureResult), duration)
}

func observeScheduleAttemptAndLatency(podProperty *api.PodProperty, result string, duration float64) {
	SchedulingAlgorithmDurationObserve(podProperty, result, duration)
	ScheduleAttemptsInc(podProperty, result)
}

func observePreemptingAttemptAndLatency(podProperty *api.PodProperty, result string, duration float64) {
	PreemptingEvaluationDurationObserve(podProperty, result, duration)
	PreemptingAttemptsInc(podProperty, result)
	PreemptingEvaluationQuantileObserve(podProperty, result, duration)
}

func ObservePodUpdatingAttemptAndLatency(podProperty *api.PodProperty, result string, duration float64) {
	PodUpdatingLatencyObserve(podProperty, result, duration)
	PodUpdatingAttemptsInc(podProperty, result)
}

func ObservePodSchedulingLatency(podProperty *api.PodProperty, attempts string, duration float64) {
	PodE2eSchedulingLatencyQuantileObserve(podProperty, duration)
	PodE2eSchedulingLatencyObserve(podProperty, attempts, duration)
}

func ObserveUpdateSnapshotAttemptAndLatency(subCluster, qos, scheduler string, duration float64) {
	SchedulingUpdateSnapshotDurationObserve(k8smetrics.Labels{
		metrics.SubClusterLabel: subCluster,
		metrics.QosLabel:        qos,
		metrics.SchedulerLabel:  scheduler,
	}, duration)
}

func ObservePodEvaluatedNodes(subCluster, qos, scheduler string, count float64) {
	podEvaluatedNodes.With(k8smetrics.Labels{
		metrics.SubClusterLabel: subCluster,
		metrics.QosLabel:        qos,
		metrics.SchedulerLabel:  scheduler,
	}).Observe(count)
}

func ObservePodFeasibleNodes(subCluster, qos, scheduler string, count float64) {
	podFeasibleNodes.With(k8smetrics.Labels{
		metrics.SubClusterLabel: subCluster,
		metrics.QosLabel:        qos,
		metrics.SchedulerLabel:  scheduler,
	}).Observe(count)
}
