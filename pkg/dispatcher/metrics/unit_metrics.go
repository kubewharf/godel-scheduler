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
			Subsystem:      DispatcherSubsystem,
			Name:           "pending_units",
			Help:           "Number of pending units",
			StabilityLevel: metrics.ALPHA,
		}, []string{pkgmetrics.QosLabel, pkgmetrics.SubClusterLabel, pkgmetrics.UnitTypeLabel, pkgmetrics.QueueLabel})

	unitPendingDuration = metrics.NewHistogramVec(
		&metrics.HistogramOpts{
			Subsystem:      DispatcherSubsystem,
			Name:           "unit_pending_duration_seconds",
			Help:           "Duration for unit in pending status",
			Buckets:        metrics.ExponentialBuckets(0.001, 2, 20),
			StabilityLevel: metrics.ALPHA,
		}, []string{pkgmetrics.QosLabel, pkgmetrics.SubClusterLabel, pkgmetrics.UnitTypeLabel, pkgmetrics.QueueLabel})
)

func newPendingUnitsGaugeMetric(labels metrics.Labels) metrics.GaugeMetric {
	return pendingUnits.With(labels)
}

func PendingUnitsSet(unitProperty api.UnitProperty, queue string, value float64) {
	unitLabels := api.MustConvertToMetricsLabels(unitProperty)
	unitLabels[pkgmetrics.QueueLabel] = queue
	newPendingUnitsGaugeMetric(unitLabels).Set(value)
}

func newUnitPendingDurationObserverMetric(labels metrics.Labels) metrics.ObserverMetric {
	return unitPendingDuration.With(labels)
}

func UnitPendingDurationObserve(unitProperty api.UnitProperty, queue string, duration float64) {
	unitLabels := api.MustConvertToMetricsLabels(unitProperty)
	unitLabels[pkgmetrics.QueueLabel] = queue
	newUnitPendingDurationObserverMetric(unitLabels).Observe(duration)
}
