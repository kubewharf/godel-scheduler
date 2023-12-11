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
			Subsystem:      DispatcherSubsystem,
			Name:           "build_info",
			Help:           "A metric with a constant value which indicates the build info of the dispatcher.",
			StabilityLevel: metrics.ALPHA,
		}, []string{"major", "minor", "gitVersion", "gitCommit", "gitTreeState", "buildDate", "goVersion", "compiler", "platform"})

	cacheSize = metrics.NewGaugeVec(
		&metrics.GaugeOpts{
			Subsystem:      DispatcherSubsystem,
			Name:           "cache_size",
			Help:           "Number of nodes, nmnodes, applications, queues and etc in the dispatcher's cache.",
			StabilityLevel: metrics.ALPHA,
		}, []string{pkgmetrics.TypeLabel})

	e2eDispatchingLatencyQuantile = metrics.NewSummary(
		&metrics.SummaryOpts{
			Subsystem:      DispatcherSubsystem,
			Name:           "e2e_dispatching_duration_quantile",
			Help:           "E2e dispatching latency between the pods comes into dispatcher and leave the dispatcher, in seconds",
			Objectives:     map[float64]float64{0.5: 0.05, 0.9: 0.01, 0.95: 0.001, 0.99: 0.001},
			StabilityLevel: metrics.ALPHA,
		})

	nodePartitionSize = metrics.NewGauge(
		&metrics.GaugeOpts{
			Subsystem:      DispatcherSubsystem,
			Name:           "existing_node_partitions",
			Help:           "Number of node partitions in Godel Scheduler.",
			StabilityLevel: metrics.ALPHA,
		})

	e2eDispatchingLatency = metrics.NewHistogram(
		&metrics.HistogramOpts{
			Subsystem:      DispatcherSubsystem,
			Name:           "e2e_dispatching_duration_seconds",
			Help:           "E2E dispatching latency between the pods comes into dispatcher and leave the dispatcher, in seconds",
			Buckets:        metrics.ExponentialBuckets(0.001, 2, 20),
			StabilityLevel: metrics.ALPHA,
		})

	podShufflingCount = metrics.NewCounter(
		&metrics.CounterOpts{
			Subsystem:      DispatcherSubsystem,
			Name:           "node_shuffling_count",
			Help:           "Number of node shuffling",
			StabilityLevel: metrics.ALPHA,
		})

	dispatcherGoroutines = metrics.NewGauge(
		&metrics.GaugeOpts{
			Subsystem:      DispatcherSubsystem,
			Name:           "goroutines",
			Help:           "Number of running goroutines split by the work they do such as updating pod status.",
			StabilityLevel: metrics.ALPHA,
		})
)

var (
	pendingPods = metrics.NewGaugeVec(
		&metrics.GaugeOpts{
			Subsystem:      DispatcherSubsystem,
			Name:           "pending_pods",
			Help:           "Number of pending pods, by the queue type. .",
			StabilityLevel: metrics.ALPHA,
		}, []string{pkgmetrics.QueueLabel, pkgmetrics.QosLabel, pkgmetrics.SubClusterLabel})

	queueSortingLatency = metrics.NewHistogramVec(
		&metrics.HistogramOpts{
			Subsystem:      DispatcherSubsystem,
			Name:           "queue_sorting_duration_seconds",
			Help:           "Duration for calculating pod priority in queue, in seconds",
			Buckets:        metrics.ExponentialBuckets(0.001, 2, 20),
			StabilityLevel: metrics.ALPHA,
		}, []string{pkgmetrics.QueueLabel, pkgmetrics.QosLabel, pkgmetrics.SubClusterLabel})

	schedulerSize = metrics.NewGaugeVec(
		&metrics.GaugeOpts{
			Subsystem:      DispatcherSubsystem,
			Name:           "scheduler_size",
			Help:           "Number of schedulers in Godel Scheduler, including active schedulers and inactive scheduler.",
			StabilityLevel: metrics.ALPHA,
		}, []string{pkgmetrics.TypeLabel})

	nodeInPartitionSize = metrics.NewGaugeVec(
		&metrics.GaugeOpts{
			Subsystem:      DispatcherSubsystem,
			Name:           "nodes_partition_count",
			Help:           "Number of nodes in each partition in Godel Scheduler.",
			StabilityLevel: metrics.ALPHA,
		}, []string{pkgmetrics.SchedulerLabel, pkgmetrics.TypeLabel})

	fairShareComputingLatency = metrics.NewHistogramVec(
		&metrics.HistogramOpts{
			Subsystem:      DispatcherSubsystem,
			Name:           "fairshare_computation_duration_seconds",
			Help:           "Duration of computing fairshare the dispatcher, in seconds",
			Buckets:        metrics.ExponentialBuckets(0.001, 2, 20),
			StabilityLevel: metrics.ALPHA,
		}, []string{pkgmetrics.AttemptsLabel})

	podsInPartitionSize = metrics.NewGaugeVec(
		&metrics.GaugeOpts{
			Subsystem:      DispatcherSubsystem,
			Name:           "pods_in_partition",
			Help:           "Number of bound & dispatched pods in each node partition in Godel Scheduler.",
			StabilityLevel: metrics.ALPHA,
		}, []string{pkgmetrics.SchedulerLabel, pkgmetrics.StateLabel})

	selectingSchedulerLatency = metrics.NewHistogramVec(
		&metrics.HistogramOpts{
			Subsystem:      DispatcherSubsystem,
			Name:           "selecting_scheduler_duration_seconds",
			Help:           "duration of selecting a suitable scheduler for the pod, in seconds",
			Buckets:        metrics.ExponentialBuckets(0.001, 2, 20),
			StabilityLevel: metrics.ALPHA,
		}, []string{pkgmetrics.ResultLabel})

	podUpdatingLatency = metrics.NewHistogramVec(
		&metrics.HistogramOpts{
			Subsystem:      DispatcherSubsystem,
			Name:           "pod_updating_duration_seconds",
			Help:           "pod updating to API server latency in seconds",
			Buckets:        metrics.ExponentialBuckets(0.001, 2, 20),
			StabilityLevel: metrics.ALPHA,
		}, []string{pkgmetrics.QosLabel, pkgmetrics.SubClusterLabel, pkgmetrics.ResultLabel})

	podPendingLatency = metrics.NewHistogramVec(
		&metrics.HistogramOpts{
			Subsystem:      DispatcherSubsystem,
			Name:           "pod_pending_in_queue_duration_seconds",
			Help:           "Latency for pods staying in queue in dispatcher",
			Buckets:        metrics.ExponentialBuckets(0.001, 2, 20),
			StabilityLevel: metrics.ALPHA,
		}, []string{pkgmetrics.QosLabel, pkgmetrics.SubClusterLabel, pkgmetrics.QueueLabel})

	dispatcherIncomingPods = metrics.NewCounterVec(
		&metrics.CounterOpts{
			Subsystem:      DispatcherSubsystem,
			Name:           "incoming_pods_total",
			Help:           "Number of pods added to dispatcher.",
			StabilityLevel: metrics.ALPHA,
		}, []string{pkgmetrics.QosLabel, pkgmetrics.SubClusterLabel})

	dispatchedPods = metrics.NewCounterVec(
		&metrics.CounterOpts{
			Subsystem:      DispatcherSubsystem,
			Name:           "dispatched_pods_total",
			Help:           "Number of pods dispatched to each scheduler.",
			StabilityLevel: metrics.ALPHA,
		}, []string{pkgmetrics.QosLabel, pkgmetrics.SubClusterLabel, pkgmetrics.SchedulerLabel})

	dispatchingAttempts = metrics.NewCounterVec(
		&metrics.CounterOpts{
			Subsystem:      DispatcherSubsystem,
			Name:           "pod_dispatch_attempts",
			Help:           "Number of attempts to dispatch pods",
			StabilityLevel: metrics.ALPHA,
		}, []string{pkgmetrics.ResultLabel})

	podUpdatingAttempts = metrics.NewCounterVec(
		&metrics.CounterOpts{
			Subsystem:      DispatcherSubsystem,
			Name:           "pod_updating_count",
			Help:           "Number of attempts to successfully update a pod.",
			StabilityLevel: metrics.ALPHA,
		}, []string{pkgmetrics.QosLabel, pkgmetrics.SubClusterLabel, pkgmetrics.ResultLabel})
)

func DispatcherGoroutinesInc() {
	dispatcherGoroutines.Inc()
}

func DispatcherGoroutinesDec() {
	dispatcherGoroutines.Dec()
}

func PodShufflingCountInc() {
	podShufflingCount.Inc()
}

func E2EDispatchingLatencyQuantileObserve(duration float64) {
	e2eDispatchingLatencyQuantile.Observe(duration)
}

func E2EDispatchingLatencyObserve(duration float64) {
	e2eDispatchingLatency.Observe(duration)
}

// newPodPendingLatencyObserverMetric returns the ObserverMetric for given labels by PodPendingLatency
func newPodPendingLatencyObserverMetric(labels metrics.Labels) metrics.ObserverMetric {
	return podPendingLatency.With(labels)
}

// PodPendingLatencyObserve Invoke Observe method
// podLabels contains basic object property labels
func PodPendingLatencyObserve(podProperty *api.PodProperty, queue string, duration float64) {
	podLabels := podProperty.ConvertToMetricsLabels()
	podLabels[pkgmetrics.QueueLabel] = queue
	newPodPendingLatencyObserverMetric(podLabels).Observe(duration)
}

// newDispatcherIncomingPodsCounterMetric returns the CounterMetric for given labels by DispatcherIncomingPods
func newDispatcherIncomingPodsCounterMetric(labels metrics.Labels) metrics.CounterMetric {
	return dispatcherIncomingPods.With(labels)
}

// DispatcherIncomingPodsInc Invoke Inc method
// podLabels contains basic object property labels
func DispatcherIncomingPodsInc(podProperty *api.PodProperty) {
	podLabels := podProperty.ConvertToMetricsLabels()
	newDispatcherIncomingPodsCounterMetric(podLabels).Inc()
}

// newPendingPodsGaugeMetric returns the GaugeMetric for given labels by PendingPods
func newPendingPodsGaugeMetric(labels metrics.Labels) metrics.GaugeMetric {
	return pendingPods.With(labels)
}

// PendingPodsInc Invoke Inc method
// podLabels contains basic object property labels
func PendingPodsInc(podProperty *api.PodProperty, queue string) {
	podLabels := podProperty.ConvertToMetricsLabels()
	podLabels[pkgmetrics.QueueLabel] = queue
	newPendingPodsGaugeMetric(podLabels).Inc()
}

// PendingPodsDec Invoke Dec method
// podLabels contains basic object property labels
func PendingPodsDec(podProperty *api.PodProperty, queue string) {
	podLabels := podProperty.ConvertToMetricsLabels()
	podLabels[pkgmetrics.QueueLabel] = queue
	newPendingPodsGaugeMetric(podLabels).Dec()
}

// PendingPodsSet Invoke Set method
// podLabels contains basic object property labels
func PendingPodsSet(podProperty *api.PodProperty, queue string, value float64) {
	podLabels := podProperty.ConvertToMetricsLabels()
	podLabels[pkgmetrics.QueueLabel] = queue
	newPendingPodsGaugeMetric(podLabels).Set(value)
}

func PendingPodsReset() {
	pendingPods.Reset()
}

// PendingPodsAdd Invoke Add method
// podLabels contains basic object property labels
func PendingPodsAdd(podProperty *api.PodProperty, queue string, value float64) {
	podLabels := podProperty.ConvertToMetricsLabels()
	podLabels[pkgmetrics.QueueLabel] = queue
	newPendingPodsGaugeMetric(podLabels).Add(value)
}

// newQueueSortingLatencyObserverMetric returns the ObserverMetric for given labels by QueueSortingLatency
func newQueueSortingLatencyObserverMetric(labels metrics.Labels) metrics.ObserverMetric {
	return queueSortingLatency.With(labels)
}

// QueueSortingLatencyObserve Invoke Observe method
// podLabels contains basic object property labels
func QueueSortingLatencyObserve(podProperty *api.PodProperty, queue string, duration float64) {
	podLabels := podProperty.ConvertToMetricsLabels()
	podLabels[pkgmetrics.QueueLabel] = queue
	newQueueSortingLatencyObserverMetric(podLabels).Observe(duration)
}

// newSchedulerSizeGaugeMetric returns the GaugeMetric for given labels by SchedulerSize
func newSchedulerSizeGaugeMetric(labels metrics.Labels) metrics.GaugeMetric {
	return schedulerSize.With(labels)
}

// SchedulerSizeInc Invoke Inc method
func SchedulerSizeInc(stype string) {
	labels := metrics.Labels{pkgmetrics.TypeLabel: stype}
	newSchedulerSizeGaugeMetric(labels).Inc()
}

// SchedulerSizeDec Invoke Dec method
func SchedulerSizeDec(stype string) {
	labels := metrics.Labels{pkgmetrics.TypeLabel: stype}
	newSchedulerSizeGaugeMetric(labels).Dec()
}

// newNodeInPartitionSizeGaugeMetric returns the GaugeMetric for given labels by NodeInPartitionSize
func newNodeInPartitionSizeGaugeMetric(labels metrics.Labels) metrics.GaugeMetric {
	return nodeInPartitionSize.With(labels)
}

// NodeInPartitionSizeInc Invoke Inc method
func NodeInPartitionSizeInc(stype, scheduler string) {
	labels := metrics.Labels{pkgmetrics.TypeLabel: stype, pkgmetrics.SchedulerLabel: scheduler}
	newNodeInPartitionSizeGaugeMetric(labels).Inc()
}

// NodeInPartitionSizeDec Invoke Dec method
func NodeInPartitionSizeDec(stype, scheduler string) {
	labels := metrics.Labels{pkgmetrics.TypeLabel: stype, pkgmetrics.SchedulerLabel: scheduler}
	newNodeInPartitionSizeGaugeMetric(labels).Dec()
}

// newPodsInPartitionSizeGaugeMetric returns the GaugeMetric for given labels by PodsInPartitionSize
func newPodsInPartitionSizeGaugeMetric(labels metrics.Labels) metrics.GaugeMetric {
	return podsInPartitionSize.With(labels)
}

// PodsInPartitionSizeInc Invoke Inc method
func PodsInPartitionSizeInc(scheduler, state string) {
	labels := metrics.Labels{pkgmetrics.StateLabel: state, pkgmetrics.SchedulerLabel: scheduler}
	newPodsInPartitionSizeGaugeMetric(labels).Inc()
}

// PodsInPartitionSizeDec Invoke Dec method
func PodsInPartitionSizeDec(scheduler, state string) {
	labels := metrics.Labels{pkgmetrics.StateLabel: state, pkgmetrics.SchedulerLabel: scheduler}
	newPodsInPartitionSizeGaugeMetric(labels).Dec()
}

// newSelectingSchedulerLatencyObserverMetric returns the ObserverMetric for given labels by SelectingSchedulerLatency
func newSelectingSchedulerLatencyObserverMetric(labels metrics.Labels) metrics.ObserverMetric {
	return selectingSchedulerLatency.With(labels)
}

// SelectingSchedulerLatencyObserve Invoke Observe method
func SelectingSchedulerLatencyObserve(result string, duration float64) {
	labels := metrics.Labels{pkgmetrics.ResultLabel: result}
	newSelectingSchedulerLatencyObserverMetric(labels).Observe(duration)
}

// newPodUpdatingLatencyObserverMetric returns the ObserverMetric for given labels by PodUpdatingLatency
func newPodUpdatingLatencyObserverMetric(labels metrics.Labels) metrics.ObserverMetric {
	return podUpdatingLatency.With(labels)
}

// PodUpdatingLatencyObserve Invoke Observe method
// podLabels contains basic object property labels
func PodUpdatingLatencyObserve(podProperty *api.PodProperty, result string, duration float64) {
	podLabels := podProperty.ConvertToMetricsLabels()
	podLabels[pkgmetrics.ResultLabel] = result
	newPodUpdatingLatencyObserverMetric(podLabels).Observe(duration)
}

// newDispatchedPodsCounterMetric returns the CounterMetric for given labels by DispatchedPods
func newDispatchedPodsCounterMetric(labels metrics.Labels) metrics.CounterMetric {
	return dispatchedPods.With(labels)
}

// DispatchedPodsInc Invoke Inc method
func DispatchedPodsInc(podProperty *api.PodProperty, scheduler string) {
	podLabels := podProperty.ConvertToMetricsLabels()
	podLabels[pkgmetrics.SchedulerLabel] = scheduler
	newDispatchedPodsCounterMetric(podLabels).Inc()
}

// newDispatchingAttemptsCounterMetric returns the CounterMetric for given labels by DispatchingAttempts
func newDispatchingAttemptsCounterMetric(labels metrics.Labels) metrics.CounterMetric {
	return dispatchingAttempts.With(labels)
}

// DispatchingAttemptsInc Invoke Inc method
func DispatchingAttemptsInc(result string) {
	labels := metrics.Labels{pkgmetrics.ResultLabel: result}
	newDispatchingAttemptsCounterMetric(labels).Inc()
}

// newPodUpdatingAttemptsCounterMetric returns the CounterMetric for given labels by PodUpdatingAttempts
func newPodUpdatingAttemptsCounterMetric(labels metrics.Labels) metrics.CounterMetric {
	return podUpdatingAttempts.With(labels)
}

// PodUpdatingAttemptsInc Invoke Inc method
// podLabels contains basic object property labels
func PodUpdatingAttemptsInc(podProperty *api.PodProperty, result string) {
	podLabels := podProperty.ConvertToMetricsLabels()
	podLabels[pkgmetrics.ResultLabel] = result
	newPodUpdatingAttemptsCounterMetric(podLabels).Inc()
}
