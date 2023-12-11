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
	"strconv"

	"k8s.io/component-base/metrics"

	pkgmetrics "github.com/kubewharf/godel-scheduler/pkg/common/metrics"
	"github.com/kubewharf/godel-scheduler/pkg/framework/api"
)

var (
	podE2ELatency = metrics.NewHistogramVec(
		&metrics.HistogramOpts{
			Subsystem: "godel",
			Name:      "pod_e2e_duration_seconds",
			Help: "pod e2e latency for pod in Godel Scheduler, in seconds. This duration is calculated from the time " +
				"the pod is first handled by Godel Scheduler(including dispatcher, scheduler and binder) " +
				"to the time the pod leaves Godel Scheduler.",
			Buckets:        metrics.ExponentialBuckets(0.001, 2, 20),
			StabilityLevel: metrics.ALPHA,
		}, []string{pkgmetrics.QosLabel, pkgmetrics.SubClusterLabel, pkgmetrics.PriorityLabel})

	podE2ELatencyQuantile = metrics.NewSummaryVec(
		&metrics.SummaryOpts{
			Subsystem: "godel",
			Name:      "pod_e2e_duration_quantile",
			Help: "pod e2e latency quantiles for pod in Godel Scheduler. The quantile is calculated from the time " +
				"the pod is first handled by Godel Scheduler(including dispatcher, scheduler and binder) " +
				"to the time the pod leaves Godel Scheduler.",
			Objectives:     map[float64]float64{0.5: 0.05, 0.9: 0.01, 0.95: 0.001, 0.99: 0.001},
			StabilityLevel: metrics.ALPHA,
		}, []string{pkgmetrics.QosLabel, pkgmetrics.SubClusterLabel, pkgmetrics.PriorityLabel})

	podGroupE2ELatency = metrics.NewHistogramVec(
		&metrics.HistogramOpts{
			Subsystem:      "godel",
			Name:           "podgroup_e2e_duration_seconds",
			Help:           "E2e scheduling latency for podgroups in seconds, which is the time from the prescheduling state to the scheduled state",
			Buckets:        metrics.ExponentialBuckets(0.001, 2, 20),
			StabilityLevel: metrics.ALPHA,
		}, []string{pkgmetrics.QosLabel, pkgmetrics.SubClusterLabel})
)

// newPodE2ELatencyObserverMetric returns the ObserverMetric for given labels by PodE2ELatency
func newPodE2ELatencyObserverMetric(labels metrics.Labels) metrics.ObserverMetric {
	return podE2ELatency.With(labels)
}

// podE2ELatencyObserve Invoke Observe method
// basicLabels contains basic object property labels
func podE2ELatencyObserve(basicLabels metrics.Labels, priority string, duration float64) {
	basicLabels[pkgmetrics.PriorityLabel] = priority
	newPodE2ELatencyObserverMetric(basicLabels).Observe(duration)
}

// newPodE2ELatencyQuantileObserverMetric returns the ObserverMetric for given labels by PodE2ELatencyQuantile
func newPodE2ELatencyQuantileObserverMetric(labels metrics.Labels) metrics.ObserverMetric {
	return podE2ELatencyQuantile.With(labels)
}

// podE2ELatencyQuantileObserve Invoke Observe method
// basicLabels contains basic object property labels
func podE2ELatencyQuantileObserve(basicLabels metrics.Labels, priority string, duration float64) {
	basicLabels[pkgmetrics.PriorityLabel] = priority
	newPodE2ELatencyQuantileObserverMetric(basicLabels).Observe(duration)
}

func observePodE2ELatency(podProperty *api.PodProperty, duration float64) {
	priority := strconv.Itoa(int(podProperty.Priority))
	if len(priority) == 0 {
		priority = pkgmetrics.UndefinedLabelValue
	}

	labels := podProperty.ConvertToMetricsLabels()
	podE2ELatencyObserve(labels, priority, duration)
	podE2ELatencyQuantileObserve(labels, priority, duration)
}

func ObservePodGodelE2E(podInfo *api.QueuedPodInfo) {
	if podInfo.Pod == nil || len(podInfo.Pod.Annotations) == 0 {
		return
	}

	podProperty := podInfo.GetPodProperty()
	if podProperty == nil {
		return
	}

	duration := SinceInSeconds(getPodInitialTimestamp(podInfo))
	observePodE2ELatency(podProperty, duration)
}

// newPodPodGroupE2ELatencyObserverMetric returns the ObserverMetric for given labels by PodE2ELatency
func newPodGroupE2ELatencyObserverMetric(labels metrics.Labels) metrics.ObserverMetric {
	return podGroupE2ELatency.With(labels)
}

func PodGroupE2ELatencyObserve(labels metrics.Labels, duration float64) {
	newPodGroupE2ELatencyObserverMetric(labels).Observe(duration)
}
