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
	pendingUnits = metrics.NewGaugeVec(
		&metrics.GaugeOpts{
			Subsystem:      BinderSubsystem,
			Name:           "pending_units",
			Help:           "Number of pending units, by the queue type. 'ready' means number of pods in readyQ; 'waiting' means number of pods in waitingQ, 'backoff' means number of pods in backoffQ.",
			StabilityLevel: metrics.ALPHA,
		}, []string{pkgmetrics.QueueLabel, pkgmetrics.QosLabel, pkgmetrics.SubClusterLabel, pkgmetrics.UnitTypeLabel})

	rejectUnitMinMember = metrics.NewHistogramVec(
		&metrics.HistogramOpts{
			Subsystem:      BinderSubsystem,
			Name:           "reject_unit_min_member",
			Help:           "Min member of unit rejection.",
			Buckets:        []float64{1, 10, 50, 100, 500, 1000, 5000, 10000},
			StabilityLevel: metrics.ALPHA,
		}, []string{pkgmetrics.QosLabel, pkgmetrics.SubClusterLabel, pkgmetrics.UnitTypeLabel, pkgmetrics.StageLabel})

	binderUnitE2ELatency = metrics.NewHistogramVec(
		&metrics.HistogramOpts{
			Subsystem:      BinderSubsystem,
			Name:           "unit_e2e_duration_seconds",
			Help:           "Units e2e scheduling latency in binder, which is the time from the arrival of the first pod to the completion of binding all pods",
			Buckets:        metrics.ExponentialBuckets(0.001, 2, 20),
			StabilityLevel: metrics.ALPHA,
		}, []string{pkgmetrics.QosLabel, pkgmetrics.SubClusterLabel, pkgmetrics.UnitTypeLabel})
)

// newPendingUnitsGaugeMetric returns the GaugeMetric for given labels by PendingUnits
func newPendingUnitsGaugeMetric(labels metrics.Labels) metrics.GaugeMetric {
	return pendingUnits.With(labels)
}

// PendingUnitsInc Invoke Inc method
// basicLabels contains basic object property labels
func PendingUnitsInc(unitProperty api.UnitProperty, queue string) {
	unitLabels := api.MustConvertToMetricsLabels(unitProperty)
	unitLabels[pkgmetrics.QueueLabel] = queue
	newPendingUnitsGaugeMetric(unitLabels).Inc()
}

// PendingUnitsDec Invoke Dec method
// basicLabels contains basic object property labels
func PendingUnitsDec(unitProperty api.UnitProperty, queue string) {
	unitLabels := api.MustConvertToMetricsLabels(unitProperty)
	unitLabels[pkgmetrics.QueueLabel] = queue
	newPendingUnitsGaugeMetric(unitLabels).Dec()
}

func newRejectUnitMinMember(labels metrics.Labels) metrics.ObserverMetric {
	return rejectUnitMinMember.With(labels)
}

func RejectUnitObserve(unitProperty api.UnitProperty, minMember float64, stage string) {
	unitLabels := api.MustConvertToMetricsLabels(unitProperty)
	unitLabels[pkgmetrics.StageLabel] = stage
	newRejectUnitMinMember(unitLabels).Observe(minMember)
}

func newBinderUnitE2ELatency(labels metrics.Labels) metrics.ObserverMetric {
	return binderUnitE2ELatency.With(labels)
}

func BinderUnitE2ELatencyObserve(unitProperty api.UnitProperty, duration float64) {
	unitLabels := api.MustConvertToMetricsLabels(unitProperty)
	newBinderUnitE2ELatency(unitLabels).Observe(duration)
}
