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
	"k8s.io/klog/v2"

	"github.com/kubewharf/godel-scheduler/pkg/version"
)

var metricsList = []metrics.Registerable{
	buildInfo,
	pendingPods,
	pendingUnits,
	binderGoroutines,
	CacheSize,

	podRejection,
	podBindingFailure,
	podBindingPhaseDuration,
	podE2EBinderLatency,
	e2eBinderLatencyQuantile,
	bindingStagePluginDuration,
	preemptVictimPodsCycleLatency,
	bindingCycleLatency,
	podOperatingLatency,
	podPendingLatency,

	binderQueueIncomingPods,
	podBindingAttempts,
	podPreemptingAttempts,
	podOperatingAttempts,
	checkConflictFailures,
	bindingFailures,

	podE2ELatency,
	podE2ELatencyQuantile,
	podGroupE2ELatency,

	binderUnitE2ELatency,
	rejectUnitMinMember,
}

var registerMetrics sync.Once

func AddMetrics(r ...metrics.Registerable) {
	metricsList = append(metricsList, r...)
}

// Register all metrics.
func Register() {
	// Register the metrics.
	registerMetrics.Do(func() {
		for _, metric := range metricsList {
			klog.InfoS("Registered metric", "metricName", metric.FQName())
			legacyregistry.MustRegister(metric)
		}
	})
	info := version.Get()
	buildInfo.WithLabelValues(info.Major, info.Minor, info.GitVersion, info.GitCommit, info.GitTreeState, info.BuildDate, info.GoVersion, info.Compiler, info.Platform).Set(1)
}
