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

const (
	UndefinedLabelValue = "-"
)

const (

	// OperationLabel - operation label name
	OperationLabel = "operation"

	// QueueLabel - the label used to identify the subQueue name in SchedulingQueue and BinderQueue
	QueueLabel = "queue"

	// EventLabel - the label used by event
	EventLabel = "event"

	QosLabel             = "qos"
	PriorityLabel        = "priority"
	SubClusterLabel      = "sub_cluster"
	ResultLabel          = "result"
	AttemptsLabel        = "attempts"
	PluginLabel          = "plugin"
	StatusLabel          = "status"
	WorkLabel            = "work"
	TypeLabel            = "type"
	ResourceLabel        = "resource"
	NodeGroupLabel       = "node_group"
	PreemptingStageLabel = "stage"
	SchedulerLabel       = "scheduler"
	StateLabel           = "state"
	ReasonLabel          = "reason"
	UnitTypeLabel        = "unit_type"
	StageLabel           = "stage"
)

const (
	UpdateResultLabel  = "updateResult"
	SuggestResultLabel = "suggestResult"
	PhaseLabel         = "phase"
)
