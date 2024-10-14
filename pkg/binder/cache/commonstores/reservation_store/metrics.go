/*
Copyright 2024 The Godel Scheduler Authors.

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

package reservationstore

import (
	"k8s.io/component-base/metrics"

	bmetrics "github.com/kubewharf/godel-scheduler/pkg/binder/metrics"
	pkgmetrics "github.com/kubewharf/godel-scheduler/pkg/common/metrics"
)

func init() {
	bmetrics.AddMetrics(reservationInfoCount, expiredReservationInfoCount)
}

var (
	reservationInfoCount = metrics.NewGaugeVec(
		&metrics.GaugeOpts{
			Subsystem: bmetrics.BinderSubsystem,
			Name:      "existing_reservation_info_count",
			Help:      "The number of reservation info",
		}, []string{pkgmetrics.SubClusterLabel, pkgmetrics.QosLabel, pkgmetrics.PriorityLabel, pkgmetrics.StatusLabel})

	expiredReservationInfoCount = metrics.NewCounterVec(
		&metrics.CounterOpts{
			Subsystem: bmetrics.BinderSubsystem,
			Name:      "expired_reservation_info_count",
			Help:      "The number of expired reservation info",
		}, []string{pkgmetrics.SubClusterLabel, pkgmetrics.QosLabel, pkgmetrics.PriorityLabel})
)
