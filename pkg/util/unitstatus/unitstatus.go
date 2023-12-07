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

package status

import v1 "k8s.io/api/core/v1"

type SchedulingStatus int8

const (
	UnknownStatus SchedulingStatus = iota
	PendingStatus
	ScheduledStatus
	TimeoutStatus
)

var schedulingStatusStr = []string{"UnknownStatus", "PendingStatus", "ScheduledStatus", "TimeoutStatus"}

func (s SchedulingStatus) String() string {
	return schedulingStatusStr[s]
}

// UnitStatus contains that the scheduling status, running pods and assumed pods.
// This will be used when we need all the unit's information.
type UnitStatus interface {
	GetSchedulingStatus() SchedulingStatus
	GetRunningPods() []*v1.Pod
}

type unitStatus struct {
	// Tracks the status of the unit scheduling.
	// If there is no this unit, we will get UnknownStatus.
	schedulingStatus SchedulingStatus

	runningPods []*v1.Pod
}

func (s *unitStatus) GetSchedulingStatus() SchedulingStatus {
	return s.schedulingStatus
}

func (s *unitStatus) GetRunningPods() []*v1.Pod {
	return s.runningPods
}
