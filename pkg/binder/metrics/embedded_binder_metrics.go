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
)

var (
	// nodeValidationFailures counts the number of times a bind was rejected
	// because the target node does not belong to the requesting Scheduler's
	// partition. This typically happens during node reshuffles.
	nodeValidationFailures = metrics.NewCounterVec(
		&metrics.CounterOpts{
			Subsystem:      BinderSubsystem,
			Name:           "node_validation_failures_total",
			Help:           "Total number of bind rejections due to node ownership validation failures.",
			StabilityLevel: metrics.ALPHA,
		}, []string{pkgmetrics.SchedulerLabel, pkgmetrics.ReasonLabel})

	// embeddedBinderBindTotal counts bind attempts by the embedded binder,
	// partitioned by scheduler and result.
	embeddedBinderBindTotal = metrics.NewCounterVec(
		&metrics.CounterOpts{
			Subsystem:      BinderSubsystem,
			Name:           "embedded_bind_total",
			Help:           "Total bind attempts via the embedded (per-Scheduler) binder.",
			StabilityLevel: metrics.ALPHA,
		}, []string{pkgmetrics.SchedulerLabel, pkgmetrics.ResultLabel})

	// embeddedBinderBindLatency measures end-to-end latency of a single
	// embedded BindUnit call.
	embeddedBinderBindLatency = metrics.NewHistogramVec(
		&metrics.HistogramOpts{
			Subsystem:      BinderSubsystem,
			Name:           "embedded_bind_duration_seconds",
			Help:           "End-to-end latency of a single embedded BindUnit call, in seconds.",
			Buckets:        metrics.ExponentialBuckets(0.001, 2, 16),
			StabilityLevel: metrics.ALPHA,
		}, []string{pkgmetrics.SchedulerLabel, pkgmetrics.ResultLabel})

	// dispatcherFallbackTotal counts the number of Pods that were sent back
	// to the Dispatcher because they exceeded MaxLocalRetries.
	dispatcherFallbackTotal = metrics.NewCounterVec(
		&metrics.CounterOpts{
			Subsystem:      BinderSubsystem,
			Name:           "dispatcher_fallback_total",
			Help:           "Total Pods sent back to Dispatcher after exceeding MaxLocalRetries.",
			StabilityLevel: metrics.ALPHA,
		}, []string{pkgmetrics.SchedulerLabel})
)

func init() {
	AddMetrics(
		nodeValidationFailures,
		embeddedBinderBindTotal,
		embeddedBinderBindLatency,
		dispatcherFallbackTotal,
	)
}

// ObserveNodeValidationFailure increments the node-validation failure counter.
func ObserveNodeValidationFailure(scheduler, reason string) {
	nodeValidationFailures.With(metrics.Labels{
		pkgmetrics.SchedulerLabel: scheduler,
		pkgmetrics.ReasonLabel:    reason,
	}).Inc()
}

// ObserveEmbeddedBind records a bind attempt from the embedded binder.
func ObserveEmbeddedBind(scheduler, result string, durationSeconds float64) {
	embeddedBinderBindTotal.With(metrics.Labels{
		pkgmetrics.SchedulerLabel: scheduler,
		pkgmetrics.ResultLabel:    result,
	}).Inc()

	embeddedBinderBindLatency.With(metrics.Labels{
		pkgmetrics.SchedulerLabel: scheduler,
		pkgmetrics.ResultLabel:    result,
	}).Observe(durationSeconds)
}

// ObserveDispatcherFallback increments the dispatcher-fallback counter.
func ObserveDispatcherFallback(scheduler string) {
	dispatcherFallbackTotal.With(metrics.Labels{
		pkgmetrics.SchedulerLabel: scheduler,
	}).Inc()
}
