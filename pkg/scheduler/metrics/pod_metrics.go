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
	"k8s.io/component-base/metrics"

	pkgmetrics "github.com/kubewharf/godel-scheduler/pkg/common/metrics"
	"github.com/kubewharf/godel-scheduler/pkg/framework/api"
)

// All the histogram based metrics have 1ms as size for the smallest bucket.

var (
	buildInfo = metrics.NewGaugeVec(
		&metrics.GaugeOpts{
			Subsystem:      SchedulerSubsystem,
			Name:           "build_info",
			Help:           "A metric with a constant value which indicates the build info of the scheduler.",
			StabilityLevel: metrics.ALPHA,
		}, []string{"major", "minor", "gitVersion", "gitCommit", "gitTreeState", "buildDate", "goVersion", "compiler", "platform"})

	CacheSize = metrics.NewGaugeVec(
		&metrics.GaugeOpts{
			Subsystem:      SchedulerSubsystem,
			Name:           "cache_size",
			Help:           "Number of nodes, pods, and reserved (bound) pods in the scheduler cache.",
			StabilityLevel: metrics.ALPHA,
		}, []string{pkgmetrics.TypeLabel, pkgmetrics.SchedulerLabel})

	ClusterPodRequested = metrics.NewGaugeVec(
		&metrics.GaugeOpts{
			Subsystem: SchedulerSubsystem,
			Name:      "cluster_pod_requested",
			Help:      "The allocatable of one instance observed from node status.",
		}, []string{pkgmetrics.SubClusterLabel, pkgmetrics.QosLabel, pkgmetrics.ResourceLabel, pkgmetrics.SchedulerLabel})

	ClusterAllocatable = metrics.NewGaugeVec(
		&metrics.GaugeOpts{
			Subsystem: SchedulerSubsystem,
			Name:      "cluster_allocatable",
			Help:      "The capacity of one instance observed from node status.",
		}, []string{pkgmetrics.SubClusterLabel, pkgmetrics.QosLabel, pkgmetrics.ResourceLabel, pkgmetrics.SchedulerLabel})

	NodeCounter = metrics.NewGaugeVec(
		&metrics.GaugeOpts{
			Subsystem: SchedulerSubsystem,
			Name:      "cluster_nodes_total",
			Help:      "The total number of cluster nodes.",
		}, []string{pkgmetrics.StatusLabel, pkgmetrics.SchedulerLabel})

	pendingPods = metrics.NewGaugeVec(
		&metrics.GaugeOpts{
			Subsystem:      SchedulerSubsystem,
			Name:           "pending_pods",
			Help:           "Number of pending pods, by the queue type. 'ready' means number of pods in readyQ; 'backoff' means number of pods in backoffQ; 'unschedulable' means number of pods in unschedulableQ.",
			StabilityLevel: metrics.ALPHA,
		}, []string{pkgmetrics.QueueLabel, pkgmetrics.QosLabel, pkgmetrics.SubClusterLabel, pkgmetrics.SchedulerLabel})

	schedulerGoroutines = metrics.NewGaugeVec(
		&metrics.GaugeOpts{
			Subsystem:      SchedulerSubsystem,
			Name:           "goroutines",
			Help:           "Number of running goroutines split by the work they do such as updating pod status.",
			StabilityLevel: metrics.ALPHA,
		}, []string{pkgmetrics.WorkLabel, pkgmetrics.QosLabel, pkgmetrics.SubClusterLabel, pkgmetrics.SchedulerLabel})

	podE2ESchedulingLatency = metrics.NewHistogramVec(
		&metrics.HistogramOpts{
			Subsystem:      SchedulerSubsystem,
			Name:           "e2e_scheduling_duration_seconds",
			Help:           "E2e scheduling latency between the pods comes into scheduler and leave the scheduler, in seconds",
			Buckets:        metrics.ExponentialBuckets(0.001, 2, 20),
			StabilityLevel: metrics.ALPHA,
		}, []string{pkgmetrics.AttemptsLabel, pkgmetrics.QosLabel, pkgmetrics.SubClusterLabel, pkgmetrics.SchedulerLabel})

	podE2ESchedulingLatencyQuantile = metrics.NewSummaryVec(
		&metrics.SummaryOpts{
			Subsystem:      SchedulerSubsystem,
			Name:           "e2e_scheduling_duration_quantile",
			Help:           "E2e scheduling latency between the pods comes into scheduler and leave the scheduler, in seconds",
			Objectives:     map[float64]float64{0.5: 0.05, 0.9: 0.01, 0.95: 0.001, 0.99: 0.001},
			StabilityLevel: metrics.ALPHA,
		}, []string{pkgmetrics.QosLabel, pkgmetrics.SubClusterLabel, pkgmetrics.SchedulerLabel})

	queueSortingLatency = metrics.NewHistogramVec(
		&metrics.HistogramOpts{
			Subsystem:      SchedulerSubsystem,
			Name:           "queue_sorting_duration_seconds",
			Help:           "Duration for calculating pod priority in queue, in seconds",
			Buckets:        metrics.ExponentialBuckets(0.001, 2, 20),
			StabilityLevel: metrics.ALPHA,
		}, []string{pkgmetrics.QueueLabel, pkgmetrics.QosLabel, pkgmetrics.SubClusterLabel, pkgmetrics.SchedulerLabel})

	podSchedulingStageDuration = metrics.NewHistogramVec(
		&metrics.HistogramOpts{
			Subsystem:      SchedulerSubsystem,
			Name:           "scheduling_stage_duration_seconds",
			Help:           "Scheduling latency in seconds split by sub-parts of the scheduling operation",
			StabilityLevel: metrics.ALPHA,
		}, []string{pkgmetrics.OperationLabel, pkgmetrics.PluginLabel, pkgmetrics.StatusLabel, pkgmetrics.NodeGroupLabel, pkgmetrics.QosLabel, pkgmetrics.SubClusterLabel, pkgmetrics.SchedulerLabel})

	schedulingUpdateSnapshotDuration = metrics.NewHistogramVec(
		&metrics.HistogramOpts{
			Subsystem:      SchedulerSubsystem,
			Name:           "scheduling_updatesnapshot_duration_seconds",
			Help:           "Scheduling update snapshot latency in seconds",
			Buckets:        metrics.ExponentialBuckets(0.001, 2, 14),
			StabilityLevel: metrics.ALPHA,
		}, []string{pkgmetrics.QosLabel, pkgmetrics.SubClusterLabel, pkgmetrics.SchedulerLabel})

	schedulingAlgorithmDuration = metrics.NewHistogramVec(
		&metrics.HistogramOpts{
			Subsystem:      SchedulerSubsystem,
			Name:           "scheduling_algorithm_duration_seconds",
			Help:           "Scheduling evaluation latency in seconds",
			Buckets:        metrics.ExponentialBuckets(0.001, 2, 20),
			StabilityLevel: metrics.ALPHA,
		}, []string{pkgmetrics.ResultLabel, pkgmetrics.QosLabel, pkgmetrics.SubClusterLabel, pkgmetrics.SchedulerLabel})

	podUpdatingLatency = metrics.NewHistogramVec(
		&metrics.HistogramOpts{
			Subsystem:      SchedulerSubsystem,
			Name:           "pod_updating_duration_seconds",
			Help:           "Successful pod updating to API server latency in seconds",
			Buckets:        metrics.ExponentialBuckets(0.001, 2, 20),
			StabilityLevel: metrics.ALPHA,
		}, []string{pkgmetrics.ResultLabel, pkgmetrics.QosLabel, pkgmetrics.SubClusterLabel, pkgmetrics.SchedulerLabel})

	podPendingLatency = metrics.NewHistogramVec(
		&metrics.HistogramOpts{
			Subsystem: SchedulerSubsystem,
			Name:      "pod_pending_in_queue_duration_seconds",
			Help: "Latency for pods staying in queue in each scheduling cycle, from the last time it's " +
				"enqueued to the time it's popped from queue",
			Buckets:        metrics.ExponentialBuckets(0.001, 2, 20),
			StabilityLevel: metrics.ALPHA,
		}, []string{pkgmetrics.AttemptsLabel, pkgmetrics.QosLabel, pkgmetrics.SubClusterLabel, pkgmetrics.SchedulerLabel})

	schedulerQueueIncomingPods = metrics.NewCounterVec(
		&metrics.CounterOpts{
			Subsystem:      SchedulerSubsystem,
			Name:           "incoming_pods_total",
			Help:           "Number of pods added to scheduling queues by event and queue type.",
			StabilityLevel: metrics.ALPHA,
		}, []string{pkgmetrics.QueueLabel, pkgmetrics.EventLabel, pkgmetrics.QosLabel, pkgmetrics.SubClusterLabel, pkgmetrics.SchedulerLabel})

	scheduleAttempts = metrics.NewCounterVec(
		&metrics.CounterOpts{
			Subsystem: SchedulerSubsystem,
			Name:      "pod_scheduling_attempts",
			Help: "Number of attempts to schedule pods, by the result. 'scheduled' means a pod is scheduled, 'unschedulable' " +
				"means a pod could not be scheduled, while 'error' means an internal scheduler problem",
			StabilityLevel: metrics.ALPHA,
		}, []string{pkgmetrics.ResultLabel, pkgmetrics.QosLabel, pkgmetrics.SubClusterLabel, pkgmetrics.SchedulerLabel})

	podUpdatingAttempts = metrics.NewCounterVec(
		&metrics.CounterOpts{
			Subsystem:      SchedulerSubsystem,
			Name:           "pod_updating_count",
			Help:           "Number of attempts to successfully update a pod.",
			StabilityLevel: metrics.ALPHA,
		}, []string{pkgmetrics.ResultLabel, pkgmetrics.QosLabel, pkgmetrics.SubClusterLabel, pkgmetrics.SchedulerLabel})

	// Preempting metrics
	preemptingEvaluationDuration = metrics.NewHistogramVec(
		&metrics.HistogramOpts{
			Subsystem:      SchedulerSubsystem,
			Name:           "preempting_duration_seconds",
			Help:           "preemption evaluation latency in seconds, including the case of scheduling pods with no victims",
			Buckets:        metrics.ExponentialBuckets(0.001, 2, 20),
			StabilityLevel: metrics.ALPHA,
		}, []string{pkgmetrics.ResultLabel, pkgmetrics.QosLabel, pkgmetrics.SubClusterLabel, pkgmetrics.SchedulerLabel})

	preemptingEvaluationQuantile = metrics.NewSummaryVec(
		&metrics.SummaryOpts{
			Subsystem:      SchedulerSubsystem,
			Name:           "preempting_duration_quantile",
			Help:           "preemption evaluation latency in seconds, including the case of scheduling pods with no victims",
			Objectives:     map[float64]float64{0.5: 0.05, 0.9: 0.01, 0.95: 0.001, 0.99: 0.001},
			StabilityLevel: metrics.ALPHA,
		}, []string{pkgmetrics.ResultLabel, pkgmetrics.QosLabel, pkgmetrics.SubClusterLabel, pkgmetrics.SchedulerLabel})

	preemptingAttempts = metrics.NewCounterVec(
		&metrics.CounterOpts{
			Subsystem: SchedulerSubsystem,
			Name:      "pod_preempting_attempts",
			Help: "Number of attempts to preempt pods, by the result. 'nominated' means a pod is nominated to preempt " +
				"others, 'nominatedScheduling' means a pod is scheduled in preempting and 'nominatedFailure' means a pod " +
				"failed to schedule and nominate preemption.",
			StabilityLevel: metrics.ALPHA,
		}, []string{pkgmetrics.ResultLabel, pkgmetrics.QosLabel, pkgmetrics.SubClusterLabel, pkgmetrics.SchedulerLabel})

	preemptingNominatorVictims = metrics.NewHistogramVec(
		&metrics.HistogramOpts{
			Subsystem:      SchedulerSubsystem,
			Name:           "nominator_victims",
			Help:           "Number of selected preemption victims",
			Buckets:        metrics.LinearBuckets(5, 5, 10),
			StabilityLevel: metrics.ALPHA,
		}, []string{pkgmetrics.SchedulerLabel})

	preemptingStageLatency = metrics.NewHistogramVec(
		&metrics.HistogramOpts{
			Subsystem:      SchedulerSubsystem,
			Name:           "preempting_stage_duration_seconds",
			Buckets:        metrics.ExponentialBuckets(0.001, 2, 20),
			StabilityLevel: metrics.ALPHA,
		}, []string{pkgmetrics.PreemptingStageLabel, pkgmetrics.QosLabel, pkgmetrics.SubClusterLabel, pkgmetrics.SchedulerLabel})

	// Pending resource metrics
	pendingPodsRequestedResource = metrics.NewGaugeVec(
		&metrics.GaugeOpts{
			Subsystem: SchedulerSubsystem,
			Name:      "pending_pods_requested_resource",
			Help:      "The total resources of pending pod requested.",
		}, []string{pkgmetrics.QueueLabel, pkgmetrics.ResourceLabel, pkgmetrics.QosLabel, pkgmetrics.SubClusterLabel, pkgmetrics.SchedulerLabel})

	podEvaluatedNodes = metrics.NewHistogramVec(
		&metrics.HistogramOpts{
			Subsystem:      SchedulerSubsystem,
			Name:           "pod_evaluated_nodes",
			Buckets:        metrics.ExponentialBuckets(50, 2, 10),
			StabilityLevel: metrics.ALPHA,
		}, []string{pkgmetrics.QosLabel, pkgmetrics.SubClusterLabel, pkgmetrics.SchedulerLabel})

	podFeasibleNodes = metrics.NewHistogramVec(
		&metrics.HistogramOpts{
			Subsystem:      SchedulerSubsystem,
			Name:           "pod_feasible_nodes",
			Buckets:        metrics.ExponentialBuckets(1, 2, 10),
			StabilityLevel: metrics.ALPHA,
		}, []string{pkgmetrics.QosLabel, pkgmetrics.SubClusterLabel, pkgmetrics.SchedulerLabel})
)

// setScheduler sets pkgmetrics.SchedulerLabel with scheduler instance name.
func setScheduler(labels metrics.Labels) {
	if _, ok := labels[pkgmetrics.SchedulerLabel]; !ok {
		labels[pkgmetrics.SchedulerLabel] = SchedulerName
	}
}

// newPendingPodsGaugeMetric returns the GaugeMetric for given labels by PendingPods
func newPendingPodsGaugeMetric(labels metrics.Labels) metrics.GaugeMetric {
	setScheduler(labels)
	return pendingPods.With(labels)
}

// PendingPodsAdd Invoke Add method
// podLabels contains basic object property labels
func PendingPodsAdd(podProperty *api.PodProperty, queue string, value float64) {
	podLabels := podProperty.ConvertToMetricsLabels()
	podLabels[pkgmetrics.QueueLabel] = queue
	newPendingPodsGaugeMetric(podLabels).Add(value)
}

// newScheduleAttemptsCounterMetric returns the CounterMetric for given labels by ScheduleAttempts
func newScheduleAttemptsCounterMetric(labels metrics.Labels) metrics.CounterMetric {
	setScheduler(labels)
	return scheduleAttempts.With(labels)
}

// ScheduleAttemptsInc Invoke Inc method
// podLabels contains basic object property labels
func ScheduleAttemptsInc(podProperty *api.PodProperty, result string) {
	podLabels := podProperty.ConvertToMetricsLabels()
	podLabels[pkgmetrics.ResultLabel] = result
	newScheduleAttemptsCounterMetric(podLabels).Inc()
}

// newPreemptingEvaluationDurationObserverMetric returns the ObserverMetric for given labels by PreemptingEvaluationDuration
func newPreemptingEvaluationDurationObserverMetric(labels metrics.Labels) metrics.ObserverMetric {
	setScheduler(labels)
	return preemptingEvaluationDuration.With(labels)
}

// PreemptingEvaluationDurationObserve Invoke Observe method
// podLabels contains basic object property labels
func PreemptingEvaluationDurationObserve(podProperty *api.PodProperty, result string, duration float64) {
	podLabels := podProperty.ConvertToMetricsLabels()
	podLabels[pkgmetrics.ResultLabel] = result
	newPreemptingEvaluationDurationObserverMetric(podLabels).Observe(duration)
}

// newPreemptingAttemptsCounterMetric returns the CounterMetric for given labels by PreemptingAttempts
func newPreemptingAttemptsCounterMetric(labels metrics.Labels) metrics.CounterMetric {
	setScheduler(labels)
	return preemptingAttempts.With(labels)
}

// PreemptingAttemptsInc Invoke Inc method
// podLabels contains basic object property labels
func PreemptingAttemptsInc(podProperty *api.PodProperty, result string) {
	podLabels := podProperty.ConvertToMetricsLabels()
	podLabels[pkgmetrics.ResultLabel] = result
	newPreemptingAttemptsCounterMetric(podLabels).Inc()
}

// newPreemptingStageLatencyObserverMetric returns the ObserverMetric for given labels by PreemptingStageLatency
func newPreemptingStageLatencyObserverMetric(labels metrics.Labels) metrics.ObserverMetric {
	setScheduler(labels)
	return preemptingStageLatency.With(labels)
}

// PreemptingStageLatencyObserve Invoke Observe method
// podLabels contains basic object property labels
func PreemptingStageLatencyObserve(podProperty *api.PodProperty, preemptingStage string, duration float64) {
	podLabels := podProperty.ConvertToMetricsLabels()
	podLabels[pkgmetrics.PreemptingStageLabel] = preemptingStage
	newPreemptingStageLatencyObserverMetric(podLabels).Observe(duration)
}

// newSchedulerGoroutinesGaugeMetric returns the GaugeMetric for given labels by SchedulerGoroutines
func newSchedulerGoroutinesGaugeMetric(labels metrics.Labels) metrics.GaugeMetric {
	setScheduler(labels)
	return schedulerGoroutines.With(labels)
}

// SchedulerGoroutinesInc Invoke Inc method
// podLabels contains basic object property labels
func SchedulerGoroutinesInc(podProperty *api.PodProperty, work string) {
	podLabels := podProperty.ConvertToMetricsLabels()
	podLabels[pkgmetrics.WorkLabel] = work
	newSchedulerGoroutinesGaugeMetric(podLabels).Inc()
}

// SchedulerGoroutinesDec Invoke Dec method
// podLabels contains basic object property labels
func SchedulerGoroutinesDec(podProperty *api.PodProperty, work string) {
	podLabels := podProperty.ConvertToMetricsLabels()
	podLabels[pkgmetrics.WorkLabel] = work
	newSchedulerGoroutinesGaugeMetric(podLabels).Dec()
}

// newPodE2ESchedulingLatencyObserverMetric returns the ObserverMetric for given labels by E2eSchedulingLatency
func newPodE2ESchedulingLatencyObserverMetric(labels metrics.Labels) metrics.ObserverMetric {
	setScheduler(labels)
	return podE2ESchedulingLatency.With(labels)
}

// PodE2eSchedulingLatencyObserve Invoke Observe method
// podLabels contains basic object property labels
func PodE2eSchedulingLatencyObserve(podProperty *api.PodProperty, attempts string, duration float64) {
	podLabels := podProperty.ConvertToMetricsLabels()
	podLabels[pkgmetrics.AttemptsLabel] = attempts
	newPodE2ESchedulingLatencyObserverMetric(podLabels).Observe(duration)
}

// newSchedulerQueueIncomingPodsCounterMetric returns the CounterMetric for given labels by SchedulerQueueIncomingPods
func newSchedulerQueueIncomingPodsCounterMetric(labels metrics.Labels) metrics.CounterMetric {
	setScheduler(labels)
	return schedulerQueueIncomingPods.With(labels)
}

// SchedulerQueueIncomingPodsAdd Invoke Add method
// podLabels contains basic object property labels
func SchedulerQueueIncomingPodsAdd(podProperty *api.PodProperty, queue, event string, value float64) {
	podLabels := podProperty.ConvertToMetricsLabels()
	podLabels[pkgmetrics.QueueLabel] = queue
	podLabels[pkgmetrics.EventLabel] = event
	newSchedulerQueueIncomingPodsCounterMetric(podLabels).Add(value)
}

// SchedulerQueueIncomingPodsInc Invoke Inc method
// podLabels contains basic object property labels
func SchedulerQueueIncomingPodsInc(podProperty *api.PodProperty, queue, event string) {
	podLabels := podProperty.ConvertToMetricsLabels()
	podLabels[pkgmetrics.QueueLabel] = queue
	podLabels[pkgmetrics.EventLabel] = event
	newSchedulerQueueIncomingPodsCounterMetric(podLabels).Inc()
}

// newPreemptingEvaluationQuantileObserverMetric returns the ObserverMetric for given labels by PreemptingEvaluationQuantile
func newPreemptingEvaluationQuantileObserverMetric(labels metrics.Labels) metrics.ObserverMetric {
	setScheduler(labels)
	return preemptingEvaluationQuantile.With(labels)
}

// PreemptingEvaluationQuantileObserve Invoke Observe method
// podLabels contains basic object property labels
func PreemptingEvaluationQuantileObserve(podProperty *api.PodProperty, result string, duration float64) {
	podLabels := podProperty.ConvertToMetricsLabels()
	podLabels[pkgmetrics.ResultLabel] = result
	newPreemptingEvaluationQuantileObserverMetric(podLabels).Observe(duration)
}

// newPodUpdatingAttemptsCounterMetric returns the CounterMetric for given labels by PodUpdatingAttempts
func newPodUpdatingAttemptsCounterMetric(labels metrics.Labels) metrics.CounterMetric {
	setScheduler(labels)
	return podUpdatingAttempts.With(labels)
}

// PodUpdatingAttemptsInc Invoke Inc method
// podLabels contains basic object property labels
func PodUpdatingAttemptsInc(podProperty *api.PodProperty, result string) {
	podLabels := podProperty.ConvertToMetricsLabels()
	podLabels[pkgmetrics.ResultLabel] = result
	newPodUpdatingAttemptsCounterMetric(podLabels).Inc()
}

// newPodE2eSchedulingLatencyQuantileObserverMetric returns the ObserverMetric for given labels by E2eSchedulingLatencyQuantile
func newPodE2eSchedulingLatencyQuantileObserverMetric(labels metrics.Labels) metrics.ObserverMetric {
	setScheduler(labels)
	return podE2ESchedulingLatencyQuantile.With(labels)
}

// PodE2eSchedulingLatencyQuantileObserve Invoke Observe method
// podLabels contains basic object property labels
func PodE2eSchedulingLatencyQuantileObserve(podProperty *api.PodProperty, duration float64) {
	podLabels := podProperty.ConvertToMetricsLabels()
	newPodE2eSchedulingLatencyQuantileObserverMetric(podLabels).Observe(duration)
}

// newPodSchedulingStageDurationObserverMetric returns the ObserverMetric for given labels by PodSchedulingStageDuration
func newPodSchedulingStageDurationObserverMetric(labels metrics.Labels) metrics.ObserverMetric {
	setScheduler(labels)
	return podSchedulingStageDuration.With(labels)
}

// PodSchedulingStageDurationObserve Invoke Observe method
// podLabels contains basic object property labels
func PodSchedulingStageDurationObserve(podProperty *api.PodProperty, operation, plugin, status, nodeGroup string, duration float64) {
	podLabels := podProperty.ConvertToMetricsLabels()
	podLabels[pkgmetrics.OperationLabel] = operation
	podLabels[pkgmetrics.PluginLabel] = plugin
	podLabels[pkgmetrics.StatusLabel] = status
	podLabels[pkgmetrics.NodeGroupLabel] = nodeGroup
	newPodSchedulingStageDurationObserverMetric(podLabels).Observe(duration)
}

// newSchedulingUpdateSnapshotDurationObserverMetric returns the ObserverMetric for given labels by SchedulingUpdateSnapshotDuration
func newSchedulingUpdateSnapshotDurationObserverMetric(labels metrics.Labels) metrics.ObserverMetric {
	setScheduler(labels)
	return schedulingUpdateSnapshotDuration.With(labels)
}

// SchedulingUpdateSnapshotDurationObserve Invoke Observe method
// basicLabels contains basic object property labels
func SchedulingUpdateSnapshotDurationObserve(basicLabels metrics.Labels, duration float64) {
	newSchedulingUpdateSnapshotDurationObserverMetric(basicLabels).Observe(duration)
}

// newSchedulingAlgorithmDurationObserverMetric returns the ObserverMetric for given labels by SchedulingAlgorithmDuration
func newSchedulingAlgorithmDurationObserverMetric(labels metrics.Labels) metrics.ObserverMetric {
	setScheduler(labels)
	return schedulingAlgorithmDuration.With(labels)
}

// SchedulingAlgorithmDurationObserve Invoke Observe method
// podLabels contains basic object property labels
func SchedulingAlgorithmDurationObserve(podProperty *api.PodProperty, result string, duration float64) {
	podLabels := podProperty.ConvertToMetricsLabels()
	podLabels[pkgmetrics.ResultLabel] = result
	newSchedulingAlgorithmDurationObserverMetric(podLabels).Observe(duration)
}

// newPodPendingLatencyObserverMetric returns the ObserverMetric for given labels by PodPendingLatency
func newPodPendingLatencyObserverMetric(labels metrics.Labels) metrics.ObserverMetric {
	setScheduler(labels)
	return podPendingLatency.With(labels)
}

// PodPendingLatencyObserve Invoke Observe method
// podLabels contains basic object property labels
func PodPendingLatencyObserve(podProperty *api.PodProperty, attempts string, duration float64) {
	podLabels := podProperty.ConvertToMetricsLabels()
	podLabels[pkgmetrics.AttemptsLabel] = attempts
	newPodPendingLatencyObserverMetric(podLabels).Observe(duration)
}

// newQueueSortingLatencyObserverMetric returns the ObserverMetric for given labels by QueueSortingLatency
func newQueueSortingLatencyObserverMetric(labels metrics.Labels) metrics.ObserverMetric {
	setScheduler(labels)
	return queueSortingLatency.With(labels)
}

// QueueSortingLatencyObserve Invoke Observe method
// podLabels contains basic object property labels
func QueueSortingLatencyObserve(podProperty *api.PodProperty, queue string, duration float64) {
	podLabels := podProperty.ConvertToMetricsLabels()
	podLabels[pkgmetrics.QueueLabel] = queue
	newQueueSortingLatencyObserverMetric(podLabels).Observe(duration)
}

// newPodUpdatingLatencyObserverMetric returns the ObserverMetric for given labels by PodUpdatingLatency
func newPodUpdatingLatencyObserverMetric(labels metrics.Labels) metrics.ObserverMetric {
	setScheduler(labels)
	return podUpdatingLatency.With(labels)
}

// PodUpdatingLatencyObserve Invoke Observe method
// podLabels contains basic object property labels
func PodUpdatingLatencyObserve(podProperty *api.PodProperty, result string, duration float64) {
	podLabels := podProperty.ConvertToMetricsLabels()
	podLabels[pkgmetrics.ResultLabel] = result
	newPodUpdatingLatencyObserverMetric(podLabels).Observe(duration)
}
