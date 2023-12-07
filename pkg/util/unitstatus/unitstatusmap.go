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

// UnitStatusMap store the scheduling status, running pods and assumed pods in cache.
// We need to control the lock of this data structure at the upper level.
type UnitStatusMap struct {
	// tracks the status of the unit scheduling, if true means min member criteria is met
	schedulingStatus unitSchedulingStatusMap

	runningPods unitPodSetMap

	// TODO: we may need this in future. (application cross multi-scheduler)
	// assumedPods unitPodSetMap
}

// NewUnitStatusMap return a pointer to UnitStatusMap.
// We need to control the lock of this data structure at the upper level.
func NewUnitStatusMap() *UnitStatusMap {
	return &UnitStatusMap{
		schedulingStatus: make(unitSchedulingStatusMap),
		runningPods:      make(unitPodSetMap),
	}
}

func (u *UnitStatusMap) SetUnitSchedulingStatus(unitKey string, status SchedulingStatus) {
	u.schedulingStatus.set(unitKey, status)
}

func (u *UnitStatusMap) GetUnitSchedulingStatus(unitKey string) SchedulingStatus {
	return u.schedulingStatus.get(unitKey)
}

func (u *UnitStatusMap) DeleteUnitSchedulingStatus(unitKey string) {
	u.schedulingStatus.delete(unitKey)
}

func (u *UnitStatusMap) AddUnitRunningPods(unitKey string, pods ...*v1.Pod) {
	u.runningPods.addUnitPods(unitKey, pods...)
}

func (u *UnitStatusMap) GetUnitRunningPods(unitKey string) []*v1.Pod {
	return u.runningPods.getUnitPods(unitKey)
}

func (u *UnitStatusMap) DeleteUnitRunningPods(unitKey string, pods ...*v1.Pod) {
	u.runningPods.deleteUnitPods(unitKey, pods...)
}

func (u *UnitStatusMap) GetUnitStatus(unitKey string) UnitStatus {
	return &unitStatus{
		schedulingStatus: u.schedulingStatus.get(unitKey),
		runningPods:      u.runningPods.getUnitPods(unitKey),
	}
}
