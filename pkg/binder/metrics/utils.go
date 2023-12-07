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
	"time"

	"github.com/kubewharf/godel-scheduler/pkg/framework/api"
	"github.com/kubewharf/godel-scheduler/pkg/util/helper"
	podutil "github.com/kubewharf/godel-scheduler/pkg/util/pod"
)

// SinceInSeconds gets the time since the specified start in seconds.
func SinceInSeconds(start time.Time) float64 {
	return time.Since(start).Seconds()
}

func getPodInitialTimestamp(podInfo *api.QueuedPodInfo) time.Time {
	if podInfo == nil || podInfo.Pod == nil {
		return time.Time{}
	}

	initialTimestamp := podInfo.InitialAttemptTimestamp
	pod := podInfo.Pod
	if timestampStr := pod.Annotations[podutil.InitialHandledTimestampAnnotationKey]; timestampStr != "" {
		value, err := time.Parse(helper.TimestampLayout, timestampStr)
		if err == nil {
			initialTimestamp = value
		}
	}
	return initialTimestamp
}
