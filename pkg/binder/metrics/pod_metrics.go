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
	CacheSize = metrics.NewGaugeVec(
		&metrics.GaugeOpts{
			Subsystem:      BinderSubsystem,
			Name:           "cache_size",
			Help:           "Number of nodes, pods, and assumed (bound) pods in the binder cache.",
			StabilityLevel: metrics.ALPHA,
		}, []string{"type"})

	buildInfo = metrics.NewGaugeVec(
		&metrics.GaugeOpts{
			Subsystem:      BinderSubsystem,
			Name:           "build_info",
			Help:           "A metric with a constant value which indicates the build info of the scheduler.",
			StabilityLevel: metrics.ALPHA,
		}, []string{"major", "minor", "gitVersion", "gitCommit", "gitTreeState", "buildDate", "goVersion", "compiler", "platform"})
)

// binder workflow
var (
	podRejection = metrics.NewCounterVec(
		&metrics.CounterOpts{
			Subsystem:      BinderSubsystem,
			Name:           "pod_rejection",
			Help:           "Number of pods rejected by binder workflow.",
			StabilityLevel: metrics.ALPHA,
		}, []string{pkgmetrics.QosLabel, pkgmetrics.SubClusterLabel, pkgmetrics.StageLabel})

	podBindingFailure = metrics.NewCounterVec(
		&metrics.CounterOpts{
			Subsystem:      BinderSubsystem,
			Name:           "pod_binding_failure",
			Help:           "Number of failed pods with different reason",
			StabilityLevel: metrics.ALPHA,
		}, []string{pkgmetrics.QosLabel, pkgmetrics.SubClusterLabel, pkgmetrics.ReasonLabel})

	podBindingAttempts = metrics.NewCounterVec(
		&metrics.CounterOpts{
			Subsystem: BinderSubsystem,
			Name:      "binding_pod_attempts",
			Help: "Number of attempts to bind pods, by the result. 'bound' means a pod is scheduled and bound, 'unschedulable' " +
				"means a pod could not be scheduled due to conflicts, while 'error' means an internal Binder problem",
			StabilityLevel: metrics.ALPHA,
		}, []string{pkgmetrics.ResultLabel, pkgmetrics.QosLabel, pkgmetrics.SubClusterLabel})

	podBindingPhaseDuration = metrics.NewHistogramVec(
		&metrics.HistogramOpts{
			Subsystem:      BinderSubsystem,
			Name:           "pod_binding_phase_duration_seconds",
			Help:           "bind phase latency in seconds",
			Buckets:        metrics.ExponentialBuckets(0.001, 2, 20),
			StabilityLevel: metrics.ALPHA,
		}, []string{pkgmetrics.QosLabel, pkgmetrics.SubClusterLabel, pkgmetrics.PhaseLabel, pkgmetrics.ResultLabel})

	podE2EBinderLatency = metrics.NewHistogramVec(
		&metrics.HistogramOpts{
			Subsystem:      BinderSubsystem,
			Name:           "e2e_duration_seconds",
			Help:           "E2e binder latency for checking conflicts to binding, check conflict + binding in seconds",
			Buckets:        metrics.ExponentialBuckets(0.001, 2, 20),
			StabilityLevel: metrics.ALPHA,
		}, []string{pkgmetrics.QosLabel, pkgmetrics.SubClusterLabel})

	e2eBinderLatencyQuantile = metrics.NewSummaryVec(
		&metrics.SummaryOpts{
			Subsystem:      BinderSubsystem,
			Name:           "e2e_duration_quantile",
			Help:           "E2e binder latency quantiles for checking conflicts to binding, check conflict + binding in seconds",
			Objectives:     map[float64]float64{0.5: 0.05, 0.9: 0.01, 0.95: 0.001, 0.99: 0.001},
			StabilityLevel: metrics.ALPHA,
		}, []string{pkgmetrics.QosLabel, pkgmetrics.SubClusterLabel})

	podPreemptingAttempts = metrics.NewCounterVec(
		&metrics.CounterOpts{
			Subsystem: BinderSubsystem,
			Name:      "pod_preempting_attempts",
			Help: "Number of attempts to preempt pods, by the result. 'preempting' means victim pods are being preempted, " +
				"'preempted' means a pod is scheduled in preempting and 'preemptingFailure' means a pod failed in preemption.",
			StabilityLevel: metrics.ALPHA,
		}, []string{pkgmetrics.ResultLabel, pkgmetrics.QosLabel, pkgmetrics.SubClusterLabel})

	preemptVictimPodsCycleLatency = metrics.NewHistogramVec(
		&metrics.HistogramOpts{
			Subsystem:      BinderSubsystem,
			Name:           "preempting_victim_pods_duration_seconds",
			Help:           "Preempt victim pods cycle latency in seconds",
			Buckets:        metrics.ExponentialBuckets(0.001, 2, 20),
			StabilityLevel: metrics.ALPHA,
		}, []string{pkgmetrics.ResultLabel, pkgmetrics.QosLabel, pkgmetrics.SubClusterLabel})

	// TODO: remove
	podOperatingAttempts = metrics.NewCounterVec(
		&metrics.CounterOpts{
			Subsystem:      BinderSubsystem,
			Name:           "operating_pod_count",
			Help:           "Number of attempts to successfully operate a pod.",
			StabilityLevel: metrics.ALPHA,
		}, []string{pkgmetrics.ResultLabel, pkgmetrics.OperationLabel, pkgmetrics.QosLabel, pkgmetrics.SubClusterLabel})

	podOperatingLatency = metrics.NewHistogramVec(
		&metrics.HistogramOpts{
			Subsystem:      BinderSubsystem,
			Name:           "pod_operating_duration_seconds",
			Help:           "Successful pod updating to API server latency in seconds",
			Buckets:        metrics.ExponentialBuckets(0.001, 2, 20),
			StabilityLevel: metrics.ALPHA,
		}, []string{pkgmetrics.ResultLabel, pkgmetrics.OperationLabel, pkgmetrics.QosLabel, pkgmetrics.SubClusterLabel})

	// TODO: remove
	checkConflictFailures = metrics.NewCounterVec(
		&metrics.CounterOpts{
			Subsystem:      BinderSubsystem,
			Name:           "check_conflict_failure_count",
			Help:           "Number of check conflict failures.",
			StabilityLevel: metrics.ALPHA,
		}, []string{pkgmetrics.QosLabel, pkgmetrics.SubClusterLabel, pkgmetrics.ReasonLabel})

	bindingStagePluginDuration = metrics.NewHistogramVec(
		&metrics.HistogramOpts{
			Subsystem:      BinderSubsystem,
			Name:           "bind_duration_seconds",
			Help:           "bind stage latency in seconds",
			Buckets:        metrics.ExponentialBuckets(0.001, 2, 20),
			StabilityLevel: metrics.ALPHA,
		}, []string{pkgmetrics.OperationLabel, pkgmetrics.PluginLabel, pkgmetrics.StatusLabel, pkgmetrics.QosLabel, pkgmetrics.SubClusterLabel})

	// TODO: remove
	bindingFailures = metrics.NewCounterVec(
		&metrics.CounterOpts{
			Subsystem:      BinderSubsystem,
			Name:           "binding_failures_count",
			Help:           "Number of binding failures",
			StabilityLevel: metrics.ALPHA,
		}, []string{pkgmetrics.QosLabel, pkgmetrics.SubClusterLabel})

	// TODO: remove
	bindingCycleLatency = metrics.NewHistogramVec(
		&metrics.HistogramOpts{
			Subsystem:      BinderSubsystem,
			Name:           "binding_duration_seconds",
			Help:           "bind cycle latency in seconds",
			Buckets:        metrics.ExponentialBuckets(0.001, 2, 20),
			StabilityLevel: metrics.ALPHA,
		}, []string{pkgmetrics.AttemptsLabel, pkgmetrics.QosLabel, pkgmetrics.SubClusterLabel})
)

var (
	binderQueueIncomingPods = metrics.NewCounterVec(
		&metrics.CounterOpts{
			Subsystem:      BinderSubsystem,
			Name:           "incoming_pods_total",
			Help:           "Number of pods added to scheduling queues by event and queue type.",
			StabilityLevel: metrics.ALPHA,
		}, []string{pkgmetrics.QueueLabel, pkgmetrics.EventLabel, pkgmetrics.QosLabel, pkgmetrics.SubClusterLabel})

	pendingPods = metrics.NewGaugeVec(
		&metrics.GaugeOpts{
			Subsystem:      BinderSubsystem,
			Name:           "pending_pods",
			Help:           "Number of pending pods, by the queue type. 'ready' means number of pods in readyQ; 'waiting' means number of pods in waitingQ, 'backoff' means number of pods in backoffQ.",
			StabilityLevel: metrics.ALPHA,
		}, []string{pkgmetrics.QueueLabel, pkgmetrics.QosLabel, pkgmetrics.SubClusterLabel})

	podPendingLatency = metrics.NewHistogramVec(
		&metrics.HistogramOpts{
			Subsystem:      BinderSubsystem,
			Name:           "pod_pending_in_queue_duration_seconds",
			Help:           "Pods staying in queue latency, from the last time it's enqueued to the time it's popped from queue",
			Buckets:        metrics.ExponentialBuckets(0.001, 2, 20),
			StabilityLevel: metrics.ALPHA,
		}, []string{pkgmetrics.QueueLabel, pkgmetrics.QosLabel, pkgmetrics.SubClusterLabel})

	// TODO: remove
	binderGoroutines = metrics.NewGaugeVec(
		&metrics.GaugeOpts{
			Subsystem:      BinderSubsystem,
			Name:           "goroutines",
			Help:           "Number of running goroutines split by the work such as running bind phase.",
			StabilityLevel: metrics.ALPHA,
		}, []string{pkgmetrics.WorkLabel, pkgmetrics.QosLabel, pkgmetrics.SubClusterLabel})
)

func newRejectPodCounterMetric(labels metrics.Labels) metrics.CounterMetric {
	return podRejection.With(labels)
}

func RejectPodInc(podProperty *api.PodProperty, reason string) {
	podLabels := podProperty.ConvertToMetricsLabels()
	podLabels[pkgmetrics.StageLabel] = reason
	newRejectPodCounterMetric(podLabels).Inc()
}

func newPodBindingFailureCounterMetric(labels metrics.Labels) metrics.CounterMetric {
	return podBindingFailure.With(labels)
}

func PodBindingFailureInc(property *api.PodProperty, reason string) {
	podLabels := property.ConvertToMetricsLabels()
	podLabels[pkgmetrics.ReasonLabel] = reason
	newPodBindingFailureCounterMetric(podLabels).Inc()
}

func newPodBindingPhaseDurationObserverMetric(labels metrics.Labels) metrics.ObserverMetric {
	return podBindingPhaseDuration.With(labels)
}

func PodBindingPhaseDurationObserve(podProperty *api.PodProperty, phase, result string, duration float64) {
	podLabels := podProperty.ConvertToMetricsLabels()
	podLabels[pkgmetrics.PhaseLabel] = phase
	podLabels[pkgmetrics.ResultLabel] = result
	newPodBindingPhaseDurationObserverMetric(podLabels).Observe(duration)
}

// newE2eBinderLatencyObserverMetric returns the ObserverMetric for given labels by E2eBinderLatency
func newPodE2EBinderLatencyObserverMetric(labels metrics.Labels) metrics.ObserverMetric {
	return podE2EBinderLatency.With(labels)
}

// PodE2EBinderLatencyObserve Invoke Observe method
// podLabels contains basic object property labels
func PodE2EBinderLatencyObserve(podProperty *api.PodProperty, duration float64) {
	podLabels := podProperty.ConvertToMetricsLabels()
	newPodE2EBinderLatencyObserverMetric(podLabels).Observe(duration)
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

// newBinderGoroutinesGaugeMetric returns the GaugeMetric for given labels by BinderGoroutines
func newBinderGoroutinesGaugeMetric(labels metrics.Labels) metrics.GaugeMetric {
	return binderGoroutines.With(labels)
}

// BinderGoroutinesInc Invoke Inc method
// podLabels contains basic object property labels
func BinderGoroutinesInc(podProperty *api.PodProperty, work string) {
	podLabels := podProperty.ConvertToMetricsLabels()
	podLabels[pkgmetrics.WorkLabel] = work
	newBinderGoroutinesGaugeMetric(podLabels).Inc()
}

// BinderGoroutinesDec Invoke Dec method
// podLabels contains basic object property labels
func BinderGoroutinesDec(podProperty *api.PodProperty, work string) {
	podLabels := podProperty.ConvertToMetricsLabels()
	podLabels[pkgmetrics.WorkLabel] = work
	newBinderGoroutinesGaugeMetric(podLabels).Dec()
}

// newE2eBinderLatencyQuantileObserverMetric returns the ObserverMetric for given labels by E2eBinderLatencyQuantile
func newE2eBinderLatencyQuantileObserverMetric(labels metrics.Labels) metrics.ObserverMetric {
	return e2eBinderLatencyQuantile.With(labels)
}

// E2eBinderLatencyQuantileObserve Invoke Observe method
// podLabels contains basic object property labels
func E2eBinderLatencyQuantileObserve(podProperty *api.PodProperty, duration float64) {
	podLabels := podProperty.ConvertToMetricsLabels()
	newE2eBinderLatencyQuantileObserverMetric(podLabels).Observe(duration)
}

// newPodOperatingAttemptsCounterMetric returns the CounterMetric for given labels by PodOperatingAttempts
func newPodOperatingAttemptsCounterMetric(labels metrics.Labels) metrics.CounterMetric {
	return podOperatingAttempts.With(labels)
}

// PodOperatingAttemptsInc Invoke Inc method
// podLabels contains basic object property labels
func PodOperatingAttemptsInc(podProperty *api.PodProperty, result, operation string) {
	podLabels := podProperty.ConvertToMetricsLabels()
	podLabels[pkgmetrics.ResultLabel] = result
	podLabels[pkgmetrics.OperationLabel] = operation
	newPodOperatingAttemptsCounterMetric(podLabels).Inc()
}

// newBindingStageDurationObserverMetric returns the ObserverMetric for given labels by BindingStageDuration
func newBindingStageDurationObserverMetric(labels metrics.Labels) metrics.ObserverMetric {
	return bindingStagePluginDuration.With(labels)
}

// BindingStageDurationObserve Invoke Observe method
// podLabels contains basic object property labels
func BindingStageDurationObserve(podProperty *api.PodProperty, operation, plugin, status string, duration float64) {
	podLabels := podProperty.ConvertToMetricsLabels()
	podLabels[pkgmetrics.OperationLabel] = operation
	podLabels[pkgmetrics.PluginLabel] = plugin
	podLabels[pkgmetrics.StatusLabel] = status
	newBindingStageDurationObserverMetric(podLabels).Observe(duration)
}

// newPendingPodsGaugeMetric returns the GaugeMetric for given labels by PendingPods
func newPendingPodsGaugeMetric(labels metrics.Labels) metrics.GaugeMetric {
	return pendingPods.With(labels)
}

// PendingPodsAdd Invoke Add method
// podLabels contains basic object property labels
func PendingPodsAdd(podProperty *api.PodProperty, queue string, value float64) {
	podLabels := podProperty.ConvertToMetricsLabels()
	podLabels[pkgmetrics.QueueLabel] = queue
	newPendingPodsGaugeMetric(podLabels).Add(value)
}

// newPreemptVictimPodsCycleLatencyObserverMetric returns the ObserverMetric for given labels by PreemptVictimPodsCycleLatency
func newPreemptVictimPodsCycleLatencyObserverMetric(labels metrics.Labels) metrics.ObserverMetric {
	return preemptVictimPodsCycleLatency.With(labels)
}

// PreemptVictimPodsCycleLatencyObserve Invoke Observe method
// podLabels contains basic object property labels
func PreemptVictimPodsCycleLatencyObserve(podProperty *api.PodProperty, result string, duration float64) {
	podLabels := podProperty.ConvertToMetricsLabels()
	podLabels[pkgmetrics.ResultLabel] = result
	newPreemptVictimPodsCycleLatencyObserverMetric(podLabels).Observe(duration)
}

// newBindingFailuresCounterMetric returns the CounterMetric for given labels by BindingFailures
func newBindingFailuresCounterMetric(labels metrics.Labels) metrics.CounterMetric {
	return bindingFailures.With(labels)
}

// BindingFailuresInc Invoke Inc method
// podLabels contains basic object property labels
func BindingFailuresInc(podProperty *api.PodProperty) {
	podLabels := podProperty.ConvertToMetricsLabels()
	newBindingFailuresCounterMetric(podLabels).Inc()
}

// newPreemptingAttemptsCounterMetric returns the CounterMetric for given labels by PreemptingAttempts
func newPreemptingAttemptsCounterMetric(labels metrics.Labels) metrics.CounterMetric {
	return podPreemptingAttempts.With(labels)
}

// PreemptingAttemptsInc Invoke Inc method
// podLabels contains basic object property labels
func PreemptingAttemptsInc(podProperty *api.PodProperty, result string) {
	podLabels := podProperty.ConvertToMetricsLabels()
	podLabels[pkgmetrics.ResultLabel] = result
	newPreemptingAttemptsCounterMetric(podLabels).Inc()
}

// newCheckConflictFailuresCounterMetric returns the CounterMetric for given labels by CheckConflictFailures
func newCheckConflictFailuresCounterMetric(labels metrics.Labels) metrics.CounterMetric {
	return checkConflictFailures.With(labels)
}

// CheckConflictFailuresInc Invoke Inc method
// podLabels contains basic object property labels
func CheckConflictFailuresInc(podProperty *api.PodProperty, reason string) {
	podLabels := podProperty.ConvertToMetricsLabels()
	podLabels[pkgmetrics.ReasonLabel] = reason
	newCheckConflictFailuresCounterMetric(podLabels).Inc()
}

// newBindingCycleLatencyObserverMetric returns the ObserverMetric for given labels by BindingCycleLatency
func newBindingCycleLatencyObserverMetric(labels metrics.Labels) metrics.ObserverMetric {
	return bindingCycleLatency.With(labels)
}

// BindingCycleLatencyObserve Invoke Observe method
// podLabels contains basic object property labels
func BindingCycleLatencyObserve(podProperty *api.PodProperty, attempts string, duration float64) {
	podLabels := podProperty.ConvertToMetricsLabels()
	podLabels[pkgmetrics.AttemptsLabel] = attempts
	newBindingCycleLatencyObserverMetric(podLabels).Observe(duration)
}

// newBinderQueueIncomingPodsCounterMetric returns the CounterMetric for given labels by BinderQueueIncomingPods
func newBinderQueueIncomingPodsCounterMetric(labels metrics.Labels) metrics.CounterMetric {
	return binderQueueIncomingPods.With(labels)
}

// BinderQueueIncomingPodsAdd Invoke Add method
// podLabels contains basic object property labels
func BinderQueueIncomingPodsAdd(podProperty *api.PodProperty, queue, event string, value float64) {
	podLabels := podProperty.ConvertToMetricsLabels()
	podLabels[pkgmetrics.QueueLabel] = queue
	podLabels[pkgmetrics.EventLabel] = event
	newBinderQueueIncomingPodsCounterMetric(podLabels).Add(value)
}

// BinderQueueIncomingPodsInc Invoke Inc method
// podLabels contains basic object property labels
func BinderQueueIncomingPodsInc(podProperty *api.PodProperty, queue, event string) {
	podLabels := podProperty.ConvertToMetricsLabels()
	podLabels[pkgmetrics.QueueLabel] = queue
	podLabels[pkgmetrics.EventLabel] = event
	newBinderQueueIncomingPodsCounterMetric(podLabels).Inc()
}

// newPodBindingAttemptsCounterMetric returns the CounterMetric for given labels by BindingAttempts
func newPodBindingAttemptsCounterMetric(labels metrics.Labels) metrics.CounterMetric {
	return podBindingAttempts.With(labels)
}

// PodBindingAttemptsInc Invoke Inc method
// podLabels contains basic object property labels
func PodBindingAttemptsInc(podProperty *api.PodProperty, result string) {
	podLabels := podProperty.ConvertToMetricsLabels()
	podLabels[pkgmetrics.ResultLabel] = result
	newPodBindingAttemptsCounterMetric(podLabels).Inc()
}

func PodBindingAttemptsAdd(podProperty *api.PodProperty, result string, count float64) {
	podLabels := podProperty.ConvertToMetricsLabels()
	podLabels[pkgmetrics.ResultLabel] = result
	newPodBindingAttemptsCounterMetric(podLabels).Add(count)
}

// newPodOperatingLatencyObserverMetric returns the ObserverMetric for given labels by PodOperatingLatency
func newPodOperatingLatencyObserverMetric(labels metrics.Labels) metrics.ObserverMetric {
	return podOperatingLatency.With(labels)
}

// PodOperatingLatencyObserve Invoke Observe method
// podLabels contains basic object property labels
func PodOperatingLatencyObserve(podProperty *api.PodProperty, result, operation string, duration float64) {
	podLabels := podProperty.ConvertToMetricsLabels()
	podLabels[pkgmetrics.ResultLabel] = result
	podLabels[pkgmetrics.OperationLabel] = operation
	newPodOperatingLatencyObserverMetric(podLabels).Observe(duration)
}
