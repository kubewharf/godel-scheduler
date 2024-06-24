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

package api

import (
	"context"
)

// UnitSchedulingRequest contains necessary info used for making better scheduling decisions
type UnitSchedulingRequest struct {
	EverScheduled bool
	AllMember     int
}

// ------------------------------------------------------------------------------------------

type LocatingPlugin interface {
	Plugin
	Locating(context.Context, ScheduleUnit, *CycleState, NodeGroup) (NodeGroup, *Status)
}

type GroupingPlugin interface {
	Plugin
	Grouping(context.Context, ScheduleUnit, *CycleState, NodeGroup) ([]NodeGroup, *Status)
}
