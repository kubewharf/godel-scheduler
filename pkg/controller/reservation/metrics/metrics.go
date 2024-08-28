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
)

const (
	MatchStatus   = "match"
	TimeoutStatus = "timeout"
	ActiveStatus  = "active"

	SuccessResult = "success"
	FailureResult = "failure"
)

var (
	// requestLatency is a Prometheus Histogram metric type partitioned by
	// "verb", and "host" labels. It is used for the rest client latency metrics.
	recycleLatency = k8smetrics.NewHistogramVec(
		&k8smetrics.HistogramOpts{
			Name:           "reservation_recycle_duration_seconds",
			Help:           "reservation recycle latency in seconds. Broken down by result.",
			StabilityLevel: k8smetrics.ALPHA,
			Buckets:        []float64{1.0, 5.0, 10.0, 30.0, 60.0, 300.0, 600.0, 1800.0, 3600.0, 10800.0, 18000.0, 36000.0},
		},
		[]string{"status"},
	)

	reservationAPICallCounter = k8smetrics.NewCounterVec(
		&k8smetrics.CounterOpts{
			Name:           "reservation_api_call_counter",
			Help:           "the number of created reservations",
			StabilityLevel: k8smetrics.ALPHA,
		}, []string{"verb", "result"})

	existingReservation = k8smetrics.NewGaugeVec(&k8smetrics.GaugeOpts{
		Name:           "existing_reservation_gauge",
		Help:           "the number of existing reservations",
		StabilityLevel: k8smetrics.ALPHA,
	}, []string{"status"})
)

func IncreaseReservationAPICall(verb string, result string) {
	reservationAPICallCounter.WithLabelValues(verb, result).Inc()
}

func ObserveReservationRecycleLatency(status string, latency float64) {
	recycleLatency.WithLabelValues(status).Observe(latency)
}

func SetExistingReservationCount(active int) {
	existingReservation.WithLabelValues(ActiveStatus).Set(float64(active))
}

func Install(metricList *[]k8smetrics.Registerable) {
	*metricList = append(*metricList,
		recycleLatency,
		reservationAPICallCounter,
		existingReservation,
	)
}
