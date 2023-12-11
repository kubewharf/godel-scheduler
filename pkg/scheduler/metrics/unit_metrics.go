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

var (
	schedulerQueueIncomingUnits = metrics.NewCounterVec(
		&metrics.CounterOpts{
			Subsystem:      SchedulerSubsystem,
			Name:           "incoming_units_total",
			Help:           "Number of units added to scheduling queues by event and queue type.",
			StabilityLevel: metrics.ALPHA,
		}, []string{pkgmetrics.QueueLabel, pkgmetrics.EventLabel, pkgmetrics.QosLabel, pkgmetrics.SubClusterLabel, pkgmetrics.SchedulerLabel, pkgmetrics.UnitTypeLabel})

	pendingUnits = metrics.NewGaugeVec(
		&metrics.GaugeOpts{
			Subsystem:      SchedulerSubsystem,
			Name:           "pending_units",
			Help:           "Number of pending units, by the queue type. 'ready' means number of pods in readyQ; 'backoff' means number of pods in backoffQ; 'unschedulable' means number of pods in unschedulableQ.",
			StabilityLevel: metrics.ALPHA,
		}, []string{pkgmetrics.QosLabel, pkgmetrics.SubClusterLabel, pkgmetrics.UnitTypeLabel, pkgmetrics.QueueLabel, pkgmetrics.SchedulerLabel})

	unitPendingLatency = metrics.NewHistogramVec(
		&metrics.HistogramOpts{
			Subsystem: SchedulerSubsystem,
			Name:      "unit_pending_in_queue_duration_seconds",
			Help: "Latency for units staying in queue in each scheduling cycle, from the last time it's " +
				"enqueued to the time it's popped from queue",
			Buckets:        metrics.ExponentialBuckets(0.001, 2, 20),
			StabilityLevel: metrics.ALPHA,
		}, []string{pkgmetrics.QosLabel, pkgmetrics.SubClusterLabel, pkgmetrics.UnitTypeLabel, pkgmetrics.SchedulerLabel})

	schedulerUnitE2ELatency = metrics.NewHistogramVec(
		&metrics.HistogramOpts{
			Subsystem:      SchedulerSubsystem,
			Name:           "unit_e2e_duration_seconds",
			Help:           "Units e2e scheduling latency in scheduler, which is the time from the arrival of the first pod to the completion of persisting all pods",
			Buckets:        metrics.ExponentialBuckets(0.001, 2, 20),
			StabilityLevel: metrics.ALPHA,
		}, []string{pkgmetrics.QosLabel, pkgmetrics.SubClusterLabel, pkgmetrics.UnitTypeLabel, pkgmetrics.SchedulerLabel})

	unitScheduleResult = metrics.NewHistogramVec(
		&metrics.HistogramOpts{
			Subsystem:      SchedulerSubsystem,
			Name:           "schedule_unit_result",
			Help:           "unit schedule result and it's minmember distribution",
			Buckets:        []float64{1, 10, 50, 100, 500, 1000, 5000, 10000},
			StabilityLevel: metrics.ALPHA,
		}, []string{pkgmetrics.QosLabel, pkgmetrics.SubClusterLabel, pkgmetrics.UnitTypeLabel, pkgmetrics.SchedulerLabel, pkgmetrics.ResultLabel})
)

// newSchedulerQueueIncomingUnitsCounterMetric returns the CounterMetric for given labels by SchedulerQueueIncomingUnits
func newSchedulerQueueIncomingUnitsCounterMetric(labels metrics.Labels) metrics.CounterMetric {
	setScheduler(labels)
	return schedulerQueueIncomingUnits.With(labels)
}

// SchedulerQueueIncomingUnitsInc Invoke Inc method
// podLabels contains basic object property labels
func SchedulerQueueIncomingUnitsInc(unitProperty api.UnitProperty, queue, event string) {
	unitLabels := api.MustConvertToMetricsLabels(unitProperty)
	unitLabels[pkgmetrics.QueueLabel] = queue
	unitLabels[pkgmetrics.EventLabel] = event
	newSchedulerQueueIncomingUnitsCounterMetric(unitLabels).Inc()
}

// newPendingUnitsGaugeMetric returns the GaugeMetric for given labels by PendingUnits
func newPendingUnitsGaugeMetric(labels metrics.Labels) metrics.GaugeMetric {
	setScheduler(labels)
	return pendingUnits.With(labels)
}

// PendingUnitsInc Invoke Inc method
// podLabels contains basic object property labels
func PendingUnitsInc(unitProperty api.UnitProperty, queue string) {
	unitLabels := api.MustConvertToMetricsLabels(unitProperty)
	unitLabels[pkgmetrics.QueueLabel] = queue
	newPendingUnitsGaugeMetric(unitLabels).Inc()
}

// PendingUnitsDec Invoke Dec method
// podLabels contains basic object property labels
func PendingUnitsDec(unitProperty api.UnitProperty, queue string) {
	unitLabels := api.MustConvertToMetricsLabels(unitProperty)
	unitLabels[pkgmetrics.QueueLabel] = queue
	newPendingUnitsGaugeMetric(unitLabels).Dec()
}

// newUnitPendingLatencyObserverMetric returns the ObserverMetric for given labels by UnitPendingLatency
func newUnitPendingLatencyObserverMetric(labels metrics.Labels) metrics.ObserverMetric {
	setScheduler(labels)
	return unitPendingLatency.With(labels)
}

// UnitPendingLatencyObserve Invoke Observe method
// podLabels contains basic object property labels
func UnitPendingLatencyObserve(unitProperty api.UnitProperty, duration float64) {
	newUnitPendingLatencyObserverMetric(unitProperty.ConvertToMetricsLabels()).Observe(duration)
}

func newSchedulerUnitE2ELatency(labels metrics.Labels) metrics.ObserverMetric {
	return schedulerUnitE2ELatency.With(labels)
}

func SchedulerUnitE2ELatencyObserve(unitProperty api.UnitProperty, duration float64) {
	unitLabels := api.MustConvertToMetricsLabels(unitProperty)
	setScheduler(unitLabels)
	newSchedulerUnitE2ELatency(unitLabels).Observe(duration)
}

func newUnitScheduleResultObserverMetric(labels metrics.Labels) metrics.ObserverMetric {
	setScheduler(labels)
	return unitScheduleResult.With(labels)
}

func UnitScheduleResultObserve(unitProperty api.UnitProperty, result string, minMember float64) {
	unitLabels := api.MustConvertToMetricsLabels(unitProperty)
	unitLabels[pkgmetrics.ResultLabel] = result
	newUnitScheduleResultObserverMetric(unitLabels).Observe(minMember)
}
