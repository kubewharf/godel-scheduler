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

package utils

type UnitStatusMap struct {
	// set unit key as the map key.
	statusMap map[string]UnitStatus
}

type UnitStatus string

const (
	PendingStatus   UnitStatus = "Pending"
	ScheduledStatus UnitStatus = "Scheduled"
	TimeoutStatus   UnitStatus = "Timeout"
	UnKnownStatus   UnitStatus = ""
)

func NewUnitStatusMap() *UnitStatusMap {
	return &UnitStatusMap{
		statusMap: make(map[string]UnitStatus),
	}
}

func (u UnitStatusMap) GetUnitStatus(key string) UnitStatus {
	if us, ok := u.statusMap[key]; ok {
		return us
	}
	return UnKnownStatus
}

func (u UnitStatusMap) SetUnitStatus(key string, status UnitStatus) {
	u.statusMap[key] = status
}

func (u UnitStatusMap) DeleteUnitStatus(key string) {
	delete(u.statusMap, key)
}
