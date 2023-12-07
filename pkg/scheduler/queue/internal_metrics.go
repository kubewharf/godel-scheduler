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

package queue

import (
	"strconv"
	"time"

	framework "github.com/kubewharf/godel-scheduler/pkg/framework/api"
	"github.com/kubewharf/godel-scheduler/pkg/scheduler/metrics"
	"github.com/kubewharf/godel-scheduler/pkg/util"
)

// metricsRecorder
type metricsRecorder struct{}

func newMetricsRecorder() *metricsRecorder {
	return &metricsRecorder{}
}

func (r *metricsRecorder) recordPending(unitInfo *framework.QueuedUnitInfo, duration time.Duration) {
	// TODO: record Attempts ...
	d := duration.Seconds()
	unitProperty := unitInfo.GetUnitProperty()
	if unitProperty == nil {
		return
	}

	attempts := strconv.Itoa(unitInfo.Attempts)
	if unitInfo.Attempts > 5 {
		attempts = "5+"
	}

	podProperty := unitProperty.GetPodProperty()
	if podProperty == nil {
		return
	}

	nPods := len(unitInfo.GetPods())
	for i := 0; i < nPods; i++ {
		metrics.PodPendingLatencyObserve(podProperty, attempts, d)
	}

	metrics.UnitPendingLatencyObserve(unitProperty, d)
}

func (r *metricsRecorder) recordIncoming(unitInfo *framework.QueuedUnitInfo, queue, event string) {
	// TODO: record Attempts ...
	unitProperty := unitInfo.GetUnitProperty()
	if unitProperty == nil {
		return
	}

	podProperty := unitProperty.GetPodProperty()
	if podProperty == nil {
		return
	}

	metrics.SchedulerQueueIncomingUnitsInc(unitProperty, queue, event)
	// Since pod added one by one, it's incorrect to add whole number of pods in the unit to the metrics again.
	if event == util.PodAdd {
		metrics.SchedulerQueueIncomingPodsInc(podProperty, queue, event)
		return
	}
	metrics.SchedulerQueueIncomingPodsAdd(podProperty, queue, event, float64(unitInfo.NumPods()))
}
