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

package tracing

const (
	RootSpan = "rootSpan"

	SchedulerPendingInQueueSpan      = "scheduler::pendingInQueue"
	SchedulerScheduleSpan            = "scheduler::schedule"
	SchedulerScheduleUnitSpan        = "scheduler::scheduleUnit"
	SchedulerPreemptUnitSpan         = "scheduler::preemptUnit"
	SchedulerSchedulePodSpan         = "scheduler::schedulePod"
	SchedulerPreemptPodSpan          = "scheduler::preemptPod"
	SchedulerGetCachedNodesSpan      = "scheduler::getCachedNodes"
	SchedulerCheckPreferredNodesSpan = "scheduler::checkPreferredNodes"
	SchedulerFilterSpan              = "scheduler::filter"
	SchedulerPrioritizeSpan          = "scheduler::prioritize"
	SchedulerAssumePodSpan           = "scheduler::assume"
	SchedulerForgetPodSpan           = "scheduler::forget"
	SchedulerUpdatingPodSpan         = "scheduler::update"

	BinderPendingInQueueSpan      = "binder::pendingInQueue"
	BinderAddPodToQueueSpan       = "binder::addPodToBinderQueue"
	BinderUpdatePodToQueueSpan    = "binder::updatePodInBinderQueue"
	BinderDeletePodFromQueueSpan  = "binder::deletePodFromBinderQueue"
	BinderInitializeTaskSpan      = "binder::initializeTask"
	BinderGetReservedResourceSpan = "binder::getReservedResource"
	BinderCheckPreemptionSpan     = "binder::checkPreemption"
	BinderCheckTopologySpan       = "binder::checkTopology"
	BinderCheckConflictsSpan      = "binder::checkConflicts"
	BinderAssumeTaskSpan          = "binder::assumeTask"
	BinderDeleteVictimsSpan       = "binder::deleteVictims"
	BinderBindTaskSpan            = "binder::bindTask"
)
