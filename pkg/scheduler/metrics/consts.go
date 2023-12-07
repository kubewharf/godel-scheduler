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
	// SchedulerSubsystem - subsystem name used by scheduler
	SchedulerSubsystem = "scheduler"

	UpdatingPod = "updating"

	// Below are possible values for the operation label. Each represents a substep of e2e scheduling:

	// PreFilterAddPodEvaluation - PreFilter add pod evaluation operation label value
	PreFilterAddPodEvaluation = "prefilter_evaluation"

	// PreFilterRemovePodEvaluation - PreFilter remove pod evaluation operation label value
	PreFilterRemovePodEvaluation = "prefilter_evaluation"

	// PreFilterEvaluation - PreFilter evaluation operation label value
	PreFilterEvaluation = "prefilter_evaluation"

	// FilterEvaluation - Filter evaluation operation label value
	FilterEvaluation = "filter_evaluation"

	// PreScoreEvaluation - PreScore evaluation operation label value
	PreScoreEvaluation = "prescore_evaluation"

	// ScoreEvaluation - Score evaluation operation label value
	ScoreEvaluation = "score_evaluation"

	// ScoreNormalizeEvaluation - Score normalize evaluation operation label value
	ScoreNormalizeEvaluation = "score_normalize_evaluation"

	// SuccessResult - result label value
	SuccessResult = "success"

	// FailureResult - result label value
	FailureResult = "failure"

	// Preempting phase label value
	PreemptingFindCandidates    = "find_candidates"
	PreemptingSelectCandidate   = "select_candidate"
	PreemptingFilterVictims     = "filter_victims"
	PreemptingPreparePriorities = "prepare_priorities"

	// ScheduledSuccessPhase - the scheduling cycle in which pod is successfully scheduled or preempted
	ScheduledSuccessPhase = "scheduled"
	// SchedulingWaitingPhase - The scheduling waiting process, which is the total latency of successfully scheduled
	// pod extracts the last successful scheduling latency, in seconds. This includes queue waiting duration as well.
	SchedulingWaitingPhase = "waiting"

	// unit schedule result
	UnitScheduleSucceed    = "succeed"
	UnitScheduleFailed     = "schedule_failed"
	UnitApplyToCacheFailed = "apply_to_cache_failed"
)
