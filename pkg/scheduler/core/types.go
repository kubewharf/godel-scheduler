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

package core

import (
	"context"
	"fmt"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/client-go/tools/events"

	framework "github.com/kubewharf/godel-scheduler/pkg/framework/api"
	"github.com/kubewharf/godel-scheduler/pkg/util/interpretabity"
	podutil "github.com/kubewharf/godel-scheduler/pkg/util/pod"
	"github.com/kubewharf/godel-scheduler/pkg/util/tracing"
)

// PodScheduler is the interface handling scheduling work
type PodScheduler interface {
	GetFrameworkForPod(*v1.Pod) (framework.SchedulerFramework, error)
	SetFrameworkForPod(framework.SchedulerFramework)

	GetPreemptionFrameworkForPod(*v1.Pod) framework.SchedulerPreemptionFramework
	SetPreemptionFrameworkForPod(framework.SchedulerPreemptionFramework)

	ScheduleInSpecificNodeGroup(ctx context.Context, scheduleFramework framework.SchedulerFramework,
		unitState, commonPreemptionState, podState *framework.CycleState, pod *v1.Pod, nodeGroup framework.NodeGroup,
		request *framework.UnitSchedulingRequest, nodeToStatus framework.NodeToStatusMap) (podScheduleResult PodScheduleResult, err error)

	PreemptInSpecificNodeGroup(ctx context.Context, scheduleFramework framework.SchedulerFramework, preemptFramework framework.SchedulerPreemptionFramework,
		unitState, commonPreemptionState, podState *framework.CycleState, pod *v1.Pod, nodeGroup framework.NodeGroup,
		nodeToStatus framework.NodeToStatusMap, cachedNominatedNodes *framework.CachedNominatedNodes) (podScheduleResult PodScheduleResult, err error)

	DisablePreemption() bool
	Close()
}

type UnitScheduler interface {
	Schedule(context.Context)

	CanBeRecycle() bool
	Close()
}

// TODO: revisit this.
type SchedulerHooks interface {
	PodScheduler() PodScheduler
	EventRecorder() events.EventRecorder
	BootstrapSchedulePod(ctx context.Context, pod *v1.Pod, podTrace tracing.SchedulingTrace, nodeGroup string) (string, framework.SchedulerFramework, framework.SchedulerPreemptionFramework, *framework.CycleState, error)
	ReservePod(ctx context.Context, clonedPod *v1.Pod, scheduleResult PodScheduleResult) (string, error)
}

const (
	// ReturnAction means scheduler will record the result and stop scheduling
	ReturnAction = "RecordAndReturn"
	// ReturnAction means scheduler will record the result and continue scheduling
	ContinueAction = "RecordAndContinue"

	// SchedulingAction is the event action used in scheduling process
	SchedulingAction = "Scheduling"
	// PreemptingAction is the event action used in preempting process
	PreemptingAction = "Preempting"
)

// PodScheduleResult represents the result of one pod scheduled or preempted. It will contain
// the final selected Node, along with the selected intermediate information or nominated node information.
type PodScheduleResult struct {
	// Number of nodes scheduler evaluated on one pod scheduled
	NumberOfEvaluatedNodes int
	// Number of feasible nodes on one pod scheduled
	NumberOfFeasibleNodes int
	// Name of the scheduler suggest host
	SuggestedHost string
	// other feasible nodes, besides SuggestedHost,
	// we will only fill this with direct schedulable nodes at the first stage
	// TODO: this may be useful if we don't want to cache scheduling results in scheduler cache
	// figure out if we can re-use the scheduling results in scheduling main workflow (per unit, per node group)
	OtherFeasibleNodes []string
	// NominatedNode stores the nominated node information for preemption
	NominatedNode *framework.NominatedNode

	Victims *framework.Victims

	// ATTENTION: We reserve this field to take into account the possibility of modifying the original data (SchedulingUnitInfo.NodeToStatusMapByTemplate)
	// in the event of pod failure at any stage.
	FilteredNodesStatuses framework.NodeToStatusMap
}

type UnitSchedulingResult struct {
	// Details is used to describe the detail of each attempted Pod.
	Details *interpretabity.UnitSchedulingDetails

	// TODO: store NodeToStatus etc.
	SuccessfulPods []string
	FailedPods     []string
}

func (usr *UnitSchedulingResult) Marshal() string {
	return fmt.Sprintf("scheduling result: %v", usr.Details.FailureMessage())
}

type UnitPreemptionResult struct {
	// Details is used to describe the detail of each attempted Pod.
	Details *interpretabity.UnitSchedulingDetails

	SuccessfulPods []string
	FailedPods     []string
}

func (usr *UnitPreemptionResult) Marshal() string {
	return fmt.Sprintf("preempting result: %v", usr.Details.FailureMessage())
}

type UnitResult struct {
	Successfully bool

	// Details is used to describe the detail of each attempted Pod.
	Details *interpretabity.UnitSchedulingDetails

	SuccessfulPods []string
	FailedPods     []string
}

// NewUnitResult returns a new UnitResult.
func NewUnitResult(schedulingResult bool, allPods int) *UnitResult {
	return &UnitResult{
		Successfully: schedulingResult,
		Details:      interpretabity.NewUnitSchedulingDetails(interpretabity.Scheduling, allPods),
	}
}

func TransferToUnitResult(unitInfo *SchedulingUnitInfo, details *interpretabity.UnitSchedulingDetails, successfulPods []string, failedPods []string) *UnitResult {
	return &UnitResult{
		// ATTENTION: DO NOT set Successfully here.
		// Successfully:   unitInfo.AllMember-len(unitInfo.NotScheduledDispatchedPodsKey) >= unitInfo.MinMember,
		Details:        details,
		SuccessfulPods: successfulPods,
		FailedPods:     failedPods,
	}
}

type RunningUnitInfo struct {
	QueuedPodInfo *framework.QueuedPodInfo
	Trace         tracing.SchedulingTrace

	// clonedPod is used to store those changes to the original pods in the workflow
	// e.g. span initialization, reservation info, preemption info ...
	// this will be cloned at the beginning of the workflow
	// TODO: for those unschedulable pods, this deep copy cost is unnecessary
	ClonedPod   *v1.Pod
	NodeToPlace string
	Victims     *framework.Victims
}

type SchedulingUnitInfo struct {
	UnitKey   string
	MinMember int
	AllMember int
	// everScheduled indicates whether we have ever scheduled some instances of this unit
	// if unit is pod group, this mean whether min member instances have been scheduled
	EverScheduled bool
	// key is running unit key
	DispatchedPods map[string]*RunningUnitInfo

	QueuedUnitInfo *framework.QueuedUnitInfo

	// if true, we need to reset the dispatched pods of this unit to pending state
	// and let dispatcher to re-dispatch them to another scheduler instance
	DispatchToAnotherScheduler bool

	SchedulingSuccessfully bool
	// schedulingResults []*SchedulingResult

	UnitCycleState *framework.CycleState

	// ATTENTION: The following fields will be RESET during scheduling.
	// So we don't need to care about them during initialization.
	NotScheduledPodKeysByTemplate map[string]sets.String
	ScheduledIndex                int
	NodeToStatusMapByTemplate     framework.NodeToStatusMapByTemplate
}

// Reset cleanup some fields at the beginning of NodeGroup Scheduling and Preempting.
func (s *SchedulingUnitInfo) Reset() {
	notScheduledPodKeysByTemplate := make(map[string]sets.String, s.AllMember)
	for _, p := range s.DispatchedPods {
		ownerKey := p.QueuedPodInfo.OwnerReferenceKey
		keys := notScheduledPodKeysByTemplate[ownerKey]
		if keys == nil {
			keys = sets.NewString()
			notScheduledPodKeysByTemplate[ownerKey] = keys
		}
		keys.Insert(podutil.GetPodKey(p.QueuedPodInfo.Pod))
	}
	s.NotScheduledPodKeysByTemplate = notScheduledPodKeysByTemplate
	s.ScheduledIndex = 0

	s.NodeToStatusMapByTemplate = make(framework.NodeToStatusMapByTemplate, len(notScheduledPodKeysByTemplate))
	for tmplKey := range notScheduledPodKeysByTemplate {
		s.NodeToStatusMapByTemplate[tmplKey] = make(framework.NodeToStatusMap)
	}
}

// StartUnitTraceContext starts trace context for each RunningUnitInfo
func (s *SchedulingUnitInfo) StartUnitTraceContext(parentSpanName, name string, options ...trace.SpanOption) {
	var opts []trace.SpanOption
	unitProperty := s.QueuedUnitInfo.GetUnitProperty()
	if unitProperty != nil {
		opts = append(opts, unitProperty.ConvertToTracingTags())
	}

	opts = append(opts, options...)
	for _, podInfo := range s.DispatchedPods {
		if podTrace := podInfo.Trace; podTrace != nil {
			podTrace.NewTraceContext(parentSpanName, name, opts...)
		}
	}
}

func (s *SchedulingUnitInfo) SetUnitTraceContextTags(name string, tags ...attribute.KeyValue) {
	if len(tags) == 0 {
		return
	}

	for _, podInfo := range s.DispatchedPods {
		if podTrace := podInfo.Trace; podTrace != nil {
			tc := podTrace.GetTraceContext(name)
			if tc != nil {
				tc.WithTags(tags...)
			}
		}
	}
}

func (s *SchedulingUnitInfo) SetUnitTraceContextFields(name string, fields ...attribute.KeyValue) {
	if len(fields) == 0 {
		return
	}

	for _, podInfo := range s.DispatchedPods {
		if podTrace := podInfo.Trace; podTrace != nil {
			tc := podTrace.GetTraceContext(name)
			if tc != nil {
				tc.WithFields(fields...)
			}
		}
	}
}

// FinishUnitTraceContext finishes trace context of each RunningUnitInfo
func (s *SchedulingUnitInfo) FinishUnitTraceContext(name string, fields ...attribute.KeyValue) {
	for _, podInfo := range s.DispatchedPods {
		if podTrace := podInfo.Trace; podTrace != nil {
			tc := podTrace.GetTraceContext(name)
			if tc != nil {
				tc.WithFields(fields...)
			}
			tracing.AsyncFinishTraceContext(tc, time.Now())
		}
	}
}
