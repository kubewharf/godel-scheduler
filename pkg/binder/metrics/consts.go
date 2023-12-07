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
	// BinderSubsystem - subsystem name used by Binder
	BinderSubsystem = "binder"

	DeletePod = "delete"
	PatchPod  = "patch"
	BindPod   = "bind"

	// Below are possible values for the operation label. Each represents a substep of e2e binding:
	// CheckConflictsEvaluation - CheckConflicts evaluation operation label value
	CheckConflictsEvaluation = "check_conflicts_evaluation"

	CheckTopologyEvaluation = "check_topology"

	// ReserveEvaluation - operation label value
	ReserveEvaluation = "reserve_evaluation"

	// UnreserveEvaluation - operation label value
	UnreserveEvaluation = "unreserve_evaluation"

	// PermitEvaluation - operation label value
	PermitEvaluation = "permit_evaluation"

	// PreBindEvaluation - operation label value
	PreBindEvaluation = "prebind_evaluation"

	// BindEvaluation - operation label value
	BindEvaluation = "bind_evaluation"

	// PostBindEvaluation - operation label value
	PostBindEvaluation = "postbind_evaluation"

	// SuccessResult - result label value
	SuccessResult = "success"

	// FailureResult - result label value
	FailureResult = "failure"

	// BindingPhase the phase for binding pods
	BindingPhase = "binding"
)

const (
	// reject reason
	BindingTimeout = "timeout"

	// pod binding failure reason
	InitializationFailure  = "initialization"
	InternalErrorFailure   = "internal_error"
	CheckTopologyFailure   = "check_topology"
	CheckPreemptionFailure = "check_preemption"
	PreemptionTimeout      = "preemption_timeout"

	// pod binding phase
	CheckTopologyPhase   = "check_topology"
	CheckConflictPhase   = "check_conflict"
	CheckPreemptionPhase = "check_preemption"
)
