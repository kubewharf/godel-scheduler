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

package binder

import (
	"context"
	"time"

	"github.com/kubewharf/godel-scheduler/pkg/binder/framework/handle"
	"github.com/kubewharf/godel-scheduler/pkg/binder/metrics"
	framework "github.com/kubewharf/godel-scheduler/pkg/framework/api"
	"github.com/kubewharf/godel-scheduler/pkg/util/tracing"
)

// TODO: re-implement...
func CheckPreemptionPhase(
	ctx context.Context,
	rui *runningUnitInfo,
	commonState *framework.CycleState,
) (returnStatus *framework.Status) {
	canBePreempted := true

	if len(rui.victims) == 0 {
		return nil
	}

	podTrace := rui.getSchedulingTrace()
	checkPreemptionTraceContext := podTrace.NewTraceContext(tracing.RootSpan, tracing.BinderCheckPreemptionSpan)
	checkPreemptionTraceContext.WithFields(tracing.WithMessageField("check preemption for new task"))
	// trace and metrics
	{
		defer func() {
			tracing.AsyncFinishTraceContext(checkPreemptionTraceContext, time.Now())
		}()

		defer func() {
			checkPreemptionTraceContext.WithFields(tracing.WithNominatedNodeField(rui.queuedPodInfo.NominatedNode.Marshall()))
			if !returnStatus.IsSuccess() {
				checkPreemptionTraceContext.WithTags(tracing.WithResultTag(tracing.ResultFailure))
				checkPreemptionTraceContext.WithFields(tracing.WithErrorField(returnStatus.AsError()))
				return
			}
			checkPreemptionTraceContext.WithTags(tracing.WithResultTag(tracing.ResultSuccess))
		}()

		defer func(startTime time.Time) {
			metrics.PodBindingPhaseDurationObserve(rui.queuedPodInfo.GetPodProperty(), metrics.CheckPreemptionPhase, returnStatus.Code().String(), metrics.SinceInSeconds(startTime))
		}(time.Now())
	}

	returnStatus = rui.Framework.RunClusterPrePreemptingPlugins(rui.queuedPodInfo.Pod, rui.State, commonState)
	if returnStatus != nil {
		return returnStatus
	}

	for _, victim := range rui.victims {
		returnStatus = rui.Framework.RunVictimCheckingPlugins(rui.queuedPodInfo.Pod, victim, rui.State, commonState)
		if returnStatus.Code() != framework.PreemptionSucceed {
			canBePreempted = false
			break
		}
		returnStatus = rui.Framework.RunPostVictimCheckingPlugins(rui.queuedPodInfo.Pod, victim, rui.State, commonState)
		if returnStatus != nil {
			canBePreempted = false
			break
		}
	}

	if canBePreempted {
		return nil
	}
	return returnStatus
}

func CheckTopologyPhase(
	ctx context.Context,
	rui *runningUnitInfo,
	commonState *framework.CycleState,
	nInfo framework.NodeInfo,
) *framework.Status {
	if !rui.Framework.HasCheckTopologyPlugins() {
		return nil
	}

	startTime := time.Now()
	podTrace := rui.getSchedulingTrace()
	traceContext := podTrace.NewTraceContext(tracing.RootSpan, tracing.BinderCheckTopologySpan)

	statusMap := rui.Framework.RunCheckTopologyPlugins(ctx, commonState, rui.queuedPodInfo.Pod, nInfo)
	defer tracing.AsyncFinishTraceContext(traceContext, time.Now())
	status := statusMap.Merge()

	if !status.IsSuccess() {
		traceContext.WithTags(tracing.WithResultTag(tracing.ResultFailure))
		traceContext.WithFields(tracing.WithErrorField(status.AsError()))
	} else {
		traceContext.WithTags(tracing.WithResultTag(tracing.ResultSuccess))
	}

	metrics.PodBindingPhaseDurationObserve(
		rui.queuedPodInfo.GetPodProperty(),
		metrics.CheckTopologyPhase, status.Code().String(),
		metrics.SinceInSeconds(startTime))
	return status
}

func CheckConflictPhase(
	ctx context.Context,
	rui *runningUnitInfo,
	nInfo framework.NodeInfo,
) *framework.Status {
	if !rui.Framework.HasCheckConflictsPlugins() {
		return nil
	}

	podTrace := rui.getSchedulingTrace()
	traceContext := podTrace.NewTraceContext(tracing.RootSpan, tracing.BinderCheckConflictsSpan)

	nodeName := nInfo.GetNodeName()
	traceContext.WithFields(tracing.WithNodeNameField(nodeName))

	startTime := time.Now()
	statusMap := rui.Framework.RunCheckConflictsPlugins(ctx, rui.State, rui.queuedPodInfo.Pod, nInfo)
	status := statusMap.Merge()

	metrics.PodBindingPhaseDurationObserve(rui.queuedPodInfo.GetPodProperty(), metrics.CheckConflictPhase, status.Code().String(), metrics.SinceInSeconds(startTime))
	defer tracing.AsyncFinishTraceContext(traceContext, time.Now())
	if !status.IsSuccess() {
		traceContext.WithTags(tracing.WithResultTag(tracing.ResultFailure))
		traceContext.WithFields(tracing.WithErrorField(status.AsError()))
	} else {
		traceContext.WithTags(tracing.WithResultTag(tracing.ResultSuccess))
	}
	return status
}

func runBindPhase(ctx context.Context,
	handler handle.BinderFrameworkHandle,
	rui *runningUnitInfo,
) *framework.Status {
	// Run the Reserve method of reserve plugins.
	// TODO: no plugin implements this function, move assume to this
	if sts := rui.Framework.RunReservePluginsReserve(ctx, rui.State, rui.queuedPodInfo.ReservedPod, rui.suggestedNode); !sts.IsSuccess() {
		// trigger un-reserve to clean up state associated with the reserved Pod
		rui.Framework.RunReservePluginsUnreserve(ctx, rui.State, rui.queuedPodInfo.ReservedPod, rui.suggestedNode)
		return sts
	}

	// bind volumes
	if !rui.queuedPodInfo.AllVolumeBound {
		// BindPodVolumes will make the API update with the assumed bindings and wait until
		// the PV controller has completely finished the binding operation.
		if err := handler.VolumeBinder().BindPodVolumes(rui.queuedPodInfo.ReservedPod); err != nil {
			// trigger un-reserve to clean up state associated with the reserved Pod
			rui.Framework.RunReservePluginsUnreserve(ctx, rui.State, rui.queuedPodInfo.ReservedPod, rui.suggestedNode)
			return framework.NewStatus(framework.Error, err.Error())
		}
	}

	// run prebind plugins
	if status := rui.Framework.RunPreBindPlugins(ctx, rui.State, rui.queuedPodInfo.ReservedPod, rui.suggestedNode); !status.IsSuccess() {
		// trigger un-reserve to clean up state associated with the reserved Pod
		rui.Framework.RunReservePluginsUnreserve(ctx, rui.State, rui.queuedPodInfo.ReservedPod, rui.suggestedNode)
		return status
	}

	// run bind plugins
	if status := rui.Framework.RunBindPlugins(ctx, rui.State, rui.queuedPodInfo.ReservedPod, rui.suggestedNode); !status.IsSuccess() {
		// trigger un-reserve to clean up state associated with the reserved Pod
		rui.Framework.RunReservePluginsUnreserve(ctx, rui.State, rui.queuedPodInfo.ReservedPod, rui.suggestedNode)
		return status
	} else {
		// Run "postbind" plugins.
		rui.Framework.RunPostBindPlugins(ctx, rui.State, rui.queuedPodInfo.ReservedPod, rui.suggestedNode)
	}

	return nil
}

func BindPhase(
	ctx context.Context,
	handler handle.BinderFrameworkHandle,
	rui *runningUnitInfo,
) *framework.Status {
	startTime := time.Now()
	podTrace := rui.getSchedulingTrace()
	traceContext := podTrace.NewTraceContext(tracing.RootSpan, tracing.BinderBindTaskSpan)

	status := runBindPhase(ctx, handler, rui)
	defer tracing.AsyncFinishTraceContext(traceContext, time.Now())
	if !status.IsSuccess() {
		traceContext.WithTags(tracing.WithResultTag(tracing.ResultFailure))
		traceContext.WithFields(tracing.WithErrorField(status.AsError()))
	} else {
		traceContext.WithTags(tracing.WithResultTag(tracing.ResultSuccess))
	}
	metrics.PodBindingPhaseDurationObserve(
		rui.queuedPodInfo.GetPodProperty(), metrics.BindingPhase,
		status.Code().String(), metrics.SinceInSeconds(startTime))
	return status
}
