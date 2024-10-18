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
	"sync"

	"k8s.io/component-base/metrics"
	"k8s.io/component-base/metrics/legacyregistry"

	"github.com/kubewharf/godel-scheduler/pkg/version"
)

var metricsList = []metrics.Registerable{
	buildInfo,
	pendingPods,
	pendingUnits,
	schedulerGoroutines,
	CacheSize,

	podE2ESchedulingLatency,
	podE2ESchedulingLatencyQuantile,
	queueSortingLatency,
	podSchedulingStageDuration,
	schedulingUpdateSnapshotDuration,
	schedulingAlgorithmDuration,
	preemptingEvaluationDuration,
	preemptingEvaluationQuantile,
	preemptingNominatorVictims,
	podUpdatingLatency,
	podPendingLatency,
	unitPendingLatency,

	schedulerQueueIncomingPods,
	schedulerQueueIncomingUnits,
	scheduleAttempts,
	preemptingAttempts,
	podUpdatingAttempts,
	preemptingStageLatency,
	pendingPodsRequestedResource,

	// cache related metrics
	ClusterAllocatable,
	ClusterPodRequested,
	NodeCounter,
	ClusterReservedResource,

	podsUseMovement,
	podEvaluatedNodes,
	podFeasibleNodes,

	schedulerUnitE2ELatency,
	unitScheduleResult,
}

// SchedulerName name of scheduler to produce metrics
var SchedulerName string

// RegisterSchedulerName registers scheduler name, which is used as a value of ScheduleLabel
func RegisterSchedulerName(name string) {
	SchedulerName = name
}

func AddMetrics(r ...metrics.Registerable) {
	metricsList = append(metricsList, r...)
}

var registerMetrics sync.Once

// Register all metrics.
func Register(schedulerName string) {
	// Register the metrics.
	registerMetrics.Do(func() {
		for _, metric := range metricsList {
			legacyregistry.MustRegister(metric)
		}
	})

	info := version.Get()
	buildInfo.WithLabelValues(info.Major, info.Minor, info.GitVersion, info.GitCommit, info.GitTreeState, info.BuildDate, info.GoVersion, info.Compiler, info.Platform).Set(1)
	RegisterSchedulerName(schedulerName)
}
