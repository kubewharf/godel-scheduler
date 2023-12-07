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
	"github.com/kubewharf/godel-scheduler/pkg/framework/api"
)

// This file contains helpers for metrics that are associated to a profile.
type DispatchResult string

var (
	// DispatchedResult is marked as successful dispatching with feasible scheduler
	DispatchedResult DispatchResult = "dispatched"
	// ErrorResult is marked as internal error
	ErrorResult DispatchResult = "error"
)

// PodDispatched can record a successful dispatching attempt and the duration
// since `start`.
func PodDispatched(duration float64) {
	observeDispatchAttemptAndLatency(string(DispatchedResult), duration)
}

// PodDispatchingFailure can record a failed dispatching attempt and the duration
// since `start`.
func PodDispatchingFailure(duration float64) {
	observeDispatchAttemptAndLatency(string(ErrorResult), duration)
}

func observeDispatchAttemptAndLatency(result string, duration float64) {
	SelectingSchedulerLatencyObserve(result, duration)
	DispatchingAttemptsInc(result)
}

func ObservePodUpdatingAttemptAndLatency(podProperty *api.PodProperty, result string, duration float64) {
	PodUpdatingLatencyObserve(podProperty, result, duration)
	PodUpdatingAttemptsInc(podProperty, result)
}

func ObservePodDispatchingLatency(duration float64) {
	E2EDispatchingLatencyObserve(duration)
	E2EDispatchingLatencyQuantileObserve(duration)
}
