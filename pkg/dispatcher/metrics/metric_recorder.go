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
	pkgmetrics "github.com/kubewharf/godel-scheduler/pkg/common/metrics"
	"github.com/kubewharf/godel-scheduler/pkg/framework/api"
)

var _ pkgmetrics.MetricRecorder = &PendingPodsRecorder{}

// PendingPodsRecorder is an implementation of MetricRecorder
type PendingPodsRecorder struct {
	queue string
}

func NewPendingPodsRecorder(queue string) *PendingPodsRecorder {
	return &PendingPodsRecorder{
		queue: queue,
	}
}

// Inc increases a metric counter by 1, in an atomic way
func (r *PendingPodsRecorder) Inc(obj interface{}) {
	if obj != nil {
		if o, ok := obj.(api.ObservableUnit); ok {
			PendingPodsInc(o.GetUnitProperty().GetPodProperty(), r.queue)
		}
	}
}

// Dec decreases a metric counter by 1, in an atomic way
func (r *PendingPodsRecorder) Dec(obj interface{}) {
	if obj != nil {
		if o, ok := obj.(api.ObservableUnit); ok {
			PendingPodsDec(o.GetUnitProperty().GetPodProperty(), r.queue)
		}
	}
}

// Clear set a metric counter to 0, in an atomic way
func (r *PendingPodsRecorder) Clear() {
	// no-op
}

func (r *PendingPodsRecorder) AddingLatencyInSeconds(obj interface{}, duration float64) {
	if obj != nil {
		if o, ok := obj.(api.ObservableUnit); ok {
			QueueSortingLatencyObserve(o.GetUnitProperty().GetPodProperty(), r.queue, duration)
		}
	}
}
