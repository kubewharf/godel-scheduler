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

package unitscheduler

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	v1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/clock"
	clientset "k8s.io/client-go/kubernetes"
	corelisters "k8s.io/client-go/listers/core/v1"
	"k8s.io/client-go/tools/events"
	"k8s.io/klog/v2"

	schedulingv1a1 "github.com/kubewharf/godel-scheduler-api/pkg/apis/scheduling/v1alpha1"
	godelclient "github.com/kubewharf/godel-scheduler-api/pkg/client/clientset/versioned"
	"github.com/kubewharf/godel-scheduler-api/pkg/client/listers/scheduling/v1alpha1"
	commonstore "github.com/kubewharf/godel-scheduler/pkg/common/store"
	framework "github.com/kubewharf/godel-scheduler/pkg/framework/api"
	"github.com/kubewharf/godel-scheduler/pkg/framework/utils"
	"github.com/kubewharf/godel-scheduler/pkg/scheduler/cache"
	"github.com/kubewharf/godel-scheduler/pkg/scheduler/core"
	schedulerframework "github.com/kubewharf/godel-scheduler/pkg/scheduler/framework"
	"github.com/kubewharf/godel-scheduler/pkg/scheduler/framework/handle"
	"github.com/kubewharf/godel-scheduler/pkg/scheduler/framework/runtime"
	unitruntime "github.com/kubewharf/godel-scheduler/pkg/scheduler/framework/unit_runtime"
	"github.com/kubewharf/godel-scheduler/pkg/scheduler/metrics"
	schedulingqueue "github.com/kubewharf/godel-scheduler/pkg/scheduler/queue"
	"github.com/kubewharf/godel-scheduler/pkg/scheduler/reconciler"
	"github.com/kubewharf/godel-scheduler/pkg/util"
	"github.com/kubewharf/godel-scheduler/pkg/util/helper"
	"github.com/kubewharf/godel-scheduler/pkg/util/interpretabity"
	"github.com/kubewharf/godel-scheduler/pkg/util/parallelize"
	podutil "github.com/kubewharf/godel-scheduler/pkg/util/pod"
	"github.com/kubewharf/godel-scheduler/pkg/util/tracing"
	unitstatus "github.com/kubewharf/godel-scheduler/pkg/util/unitstatus"
)

const FailToScheduleUnit = "FailToScheduleUnit"

// ------------------------------------------------------------------------------------------

// unitScheduler is the component managing cache such as node and pod info, and other configs sharing the same life cycle with scheduler
type unitScheduler struct {
	schedulerName     string
	switchType        framework.SwitchType
	subCluster        string
	disablePreemption bool

	client    clientset.Interface
	crdClient godelclient.Interface

	podLister corelisters.PodLister
	pgLister  v1alpha1.PodGroupLister

	Cache      cache.SchedulerCache
	Snapshot   *cache.Snapshot
	Queue      schedulingqueue.SchedulingQueue
	Scheduler  core.PodScheduler
	Reconciler *reconciler.FailedTaskReconciler

	nextUnit func() *framework.QueuedUnitInfo

	PluginRegistry framework.PluginMap
	PluginOrder    framework.PluginOrder

	Recorder events.EventRecorder
	// TODO: following fields useless for now
	MetricsRecorder         *runtime.MetricsRecorder
	Clock                   clock.Clock
	LatestScheduleTimestamp time.Time
}

var (
	_ core.UnitScheduler         = &unitScheduler{}
	_ core.SchedulerHooks        = &unitScheduler{}
	_ handle.UnitFrameworkHandle = &unitScheduler{}
)

func NewUnitScheduler(
	// basic infos...
	schedulerName string,
	switchType framework.SwitchType,
	subCluster string,
	disablePreemption bool,
	// clients...
	client clientset.Interface,
	crdClient godelclient.Interface,
	// listers...
	podLister corelisters.PodLister,
	pgLister v1alpha1.PodGroupLister,
	// components...
	cache cache.SchedulerCache,
	snapshot *cache.Snapshot,
	queue schedulingqueue.SchedulingQueue,
	reconciler *reconciler.FailedTaskReconciler,
	podScheduler core.PodScheduler,
	clock clock.Clock,
	recorder events.EventRecorder,
) core.UnitScheduler {
	gs := &unitScheduler{
		schedulerName:     schedulerName,
		switchType:        switchType,
		subCluster:        subCluster,
		disablePreemption: disablePreemption,

		client:    client,
		crdClient: crdClient,

		podLister: podLister,
		pgLister:  pgLister,

		Cache:      cache,
		Snapshot:   snapshot,
		Queue:      queue,
		Scheduler:  podScheduler,
		Reconciler: reconciler,

		nextUnit: schedulingqueue.MakeNextUnitFunc(queue),

		Recorder:                recorder,
		MetricsRecorder:         runtime.NewMetricsRecorder(1000, time.Second, switchType, subCluster, schedulerName),
		Clock:                   clock,
		LatestScheduleTimestamp: clock.Now(),
	}

	gs.PluginRegistry = schedulerframework.NewUnitPluginsRegistry(schedulerframework.NewUnitInTreeRegistry(), nil, gs)
	gs.PluginOrder = schedulerframework.NewOrderedUnitPluginRegistry()

	return gs
}

// --------------------------------------------------- SchedulerHooks ---------------------------------------------------

func (gs *unitScheduler) PodScheduler() core.PodScheduler {
	return gs.Scheduler
}

func (gs *unitScheduler) EventRecorder() events.EventRecorder {
	return gs.Recorder
}

func (gs *unitScheduler) BootstrapSchedulePod(ctx context.Context, pod *v1.Pod, podTrace tracing.SchedulingTrace, nodeGroup string) (string, framework.SchedulerFramework, framework.SchedulerPreemptionFramework, *framework.CycleState, error) {
	godelScheduler, switchType, subCluster := gs.Scheduler, gs.switchType, gs.subCluster

	fwk, err := godelScheduler.GetFrameworkForPod(pod)
	if err != nil {
		// This shouldn't happen, because we only schedule pods having annotations set correctly.
		// API server is supposed to do the validation for annotation.
		klog.ErrorS(err, "Failed to get framework for pod", "switchType", switchType, "subCluster", subCluster, "pod", klog.KObj(pod))
		return "", nil, nil, nil, err
	}
	godelScheduler.SetFrameworkForPod(fwk)
	klog.V(4).InfoS("Generate ScheduleFramework for pod", "pod", podutil.GetPodKey(pod), "framework", fwk.ListPlugins())

	// init cycle state
	state, err := fwk.InitCycleState(pod)
	if err != nil {
		// This shouldn't happen, because we only schedule pods having annotations set
		klog.ErrorS(err, "Failed to initialize cycle state", "switchType", switchType, "subCluster", subCluster, "pod", klog.KObj(pod))
		return "", nil, nil, nil, err
	}
	state.SetRecordPluginMetrics(true)

	if err = framework.SetPodTrace(podTrace, state); err != nil {
		klog.ErrorS(err, "Fail to set pod tracing context map", "switchType", switchType, "subCluster", subCluster, "pod", podutil.GetPodKey(pod))
		return "", nil, nil, nil, err
	}

	if err = framework.SetNodeGroupKeyState(nodeGroup, state); err != nil {
		klog.ErrorS(err, "Failed to set node group", "switchType", switchType, "subCluster", subCluster, "pod", klog.KObj(pod), "nodeGroup", nodeGroup)
		return "", nil, nil, nil, err
	}

	// set unitProperty to CycleState
	framework.SetPodProperty(framework.ExtractPodProperty(pod), state)

	pfwk := godelScheduler.GetPreemptionFrameworkForPod(pod)
	godelScheduler.SetPreemptionFrameworkForPod(pfwk)

	return podutil.GetPodKey(pod), fwk, pfwk, state, nil
}

func (gs *unitScheduler) ReservePod(ctx context.Context, clonedPod *v1.Pod, scheduleResult core.PodScheduleResult) (string, error) {
	// update pod state
	clonedPod.Annotations[podutil.PodStateAnnotationKey] = string(podutil.PodAssumed)
	// update pod assumed node or nominated node
	targetNode := ""
	if scheduleResult.SuggestedHost != "" {
		// update assumed pod info in cache
		clonedPod.Annotations[podutil.AssumedNodeAnnotationKey] = scheduleResult.SuggestedHost
		targetNode = scheduleResult.SuggestedHost
	} else if scheduleResult.NominatedNode != nil {
		if len(scheduleResult.NominatedNode.VictimPods) == 0 {
			clonedPod.Annotations[podutil.AssumedNodeAnnotationKey] = scheduleResult.NominatedNode.NodeName
		} else {
			if err := utils.SetPodNominatedNode(clonedPod, scheduleResult.NominatedNode); err != nil {
				parsingErr := fmt.Errorf("error updating pod %s/%s with nominated node: %v", clonedPod.Namespace, clonedPod.Name, err)
				return targetNode, parsingErr
			}
		}
		targetNode = scheduleResult.NominatedNode.NodeName
	}

	cachePodInfo := framework.MakeCachePodInfoWrapper().Pod(clonedPod.DeepCopy()).Victims(scheduleResult.Victims).Obj()
	if err := gs.Snapshot.AssumePod(cachePodInfo); err != nil {
		klog.ErrorS(err, "Failed to assume pod in scheduler snapshot", "pod", klog.KObj(clonedPod), "nodeName", utils.GetNodeNameFromPod(clonedPod))
		return targetNode, err
	}

	return targetNode, nil
}

// --------------------------------------------------- UnitFrameworkHandle ---------------------------------------------------

func (gs *unitScheduler) SchedulerName() string {
	return gs.schedulerName
}

func (gs *unitScheduler) SwitchType() framework.SwitchType {
	return gs.switchType
}

func (gs *unitScheduler) SubCluster() string {
	return gs.subCluster
}

func (gs *unitScheduler) GetUnitStatus(unitKey string) unitstatus.UnitStatus {
	return gs.Cache.GetUnitStatus(unitKey)
}

func (gs *unitScheduler) IsCachedPod(pod *v1.Pod) (bool, error) {
	return gs.Cache.IsCachedPod(pod)
}

func (gs *unitScheduler) GetNodeInfo(nodeName string) framework.NodeInfo {
	return gs.Snapshot.GetNodeInfo(nodeName)
}

func (gs *unitScheduler) FindStore(storeName commonstore.StoreName) commonstore.Store {
	return gs.Snapshot.FindStore(storeName)
}

func (gs *unitScheduler) IsAssumedPod(pod *v1.Pod) (bool, error) {
	return gs.Cache.IsAssumedPod(pod)
}

// --------------------------------------------------- UnitScheduler ---------------------------------------------------

func (gs *unitScheduler) CanBeRecycle() bool {
	return gs.LatestScheduleTimestamp.Add(framework.RecycleExpiration).Before(gs.Clock.Now())
}

func (gs *unitScheduler) Close() {
	gs.MetricsRecorder.Close()
	gs.PodScheduler().Close()
}

func (gs *unitScheduler) Schedule(ctx context.Context) {
	gs.LatestScheduleTimestamp = gs.Clock.Now()
	snapshot, switchType, subCluster := gs.Snapshot, gs.switchType, gs.subCluster
	queuedUnitInfo := gs.nextUnit()
	if inValidUnit(queuedUnitInfo) {
		klog.InfoS("Empty unit or invalid queued pod info, ignore this unit and don't re-enqueue", "unit", queuedUnitInfo)
		gs.recordUnitSchedulingResults(queuedUnitInfo, false, "InvalidUnit", core.ReturnAction, "Empty unit or invalid queued pod info, ignore this unit and don't re-enqueue")
		return
	}
	klog.V(4).InfoS("Attempting to schedule unit", "switchType", switchType, "subCluster", subCluster, "unitKey", queuedUnitInfo.UnitKey)

	unitInfo, err := gs.constructSchedulingUnitInfo(ctx, queuedUnitInfo)
	if err != nil {
		klog.InfoS("Failed to construct scheduling unit info", "switchType", switchType, "subCluster", subCluster, "unitKey", queuedUnitInfo.UnitKey, "err", err)
		gs.recordUnitSchedulingResults(queuedUnitInfo, false, "FailToConstructUnitInfo", core.ReturnAction, helper.TruncateMessage(err.Error()))
		gs.handleSchedulingUnitFailure(ctx, core.NewUnitResult(false, 0), unitInfo, err, "FailToConstructUnitInfo")
		return
	}

	if err = gs.Cache.UpdateSnapshot(snapshot); err != nil {
		klog.InfoS("Failed to update snapshot", "switchType", switchType, "subCluster", subCluster, "unitKey", unitInfo.UnitKey, "err", err)
		gs.recordUnitSchedulingResults(queuedUnitInfo, false, "FailToUpdateSnapshot", core.ReturnAction, helper.TruncateMessage(err.Error()))
		gs.handleSchedulingUnitFailure(ctx, core.NewUnitResult(false, 0), unitInfo, err, "FailToUpdateSnapshot")
		return
	}

	unitFramework := unitruntime.NewUnitFramework(gs, gs, gs.PluginRegistry, gs.PluginOrder, unitInfo.QueuedUnitInfo)

	nodeGroup, status := unitFramework.RunLocatingPlugins(ctx, unitInfo.QueuedUnitInfo, unitInfo.UnitCycleState, snapshot.MakeBasicNodeGroup())
	if !status.IsSuccess() {
		klog.InfoS("Failed to run locating plugins", "switchType", switchType, "subCluster", subCluster, "unitKey", unitInfo.UnitKey, "status", status)
		gs.recordUnitSchedulingResults(queuedUnitInfo, false, "FailToLocating", core.ReturnAction, helper.TruncateMessage(status.AsError().Error()))
		gs.handleSchedulingUnitFailure(ctx, core.NewUnitResult(false, 0), unitInfo, err, "FailToLocating")
		return
	}

	nodeGroups, status := unitFramework.RunGroupingPlugin(ctx, unitInfo.QueuedUnitInfo, unitInfo.UnitCycleState, nodeGroup)
	if !status.IsSuccess() {
		klog.InfoS("Failed to run grouping plugin", "switchType", switchType, "subCluster", subCluster, "unitKey", unitInfo.UnitKey, "status", status)
		gs.recordUnitSchedulingResults(queuedUnitInfo, false, "FailToGrouping", core.ReturnAction, helper.TruncateMessage(status.AsError().Error()))
		gs.handleSchedulingUnitFailure(ctx, core.NewUnitResult(false, 0), unitInfo, err, "FailToGrouping")
		return
	}

	var (
		// basic message for unit.
		unitMessage = fmt.Sprintf("uint key=%v, ever scheduled=%v, allMember=%d, minMember=%d", unitInfo.UnitKey, unitInfo.EverScheduled, unitInfo.AllMember, unitInfo.MinMember)

		// record final scheduling result,
		finalUnitResult = core.NewUnitResult(false, unitInfo.AllMember)
	)

	// TODO: we will cache some feasible nodes based on pod owners, make sure this (per node group scheduling) will not affect that
	// if there may be some conflicts, we need to revisit these two features
	for _, nodeGroup := range nodeGroups {
		nodeGroupName := nodeGroup.GetKey()
		klog.V(4).InfoS("Attempting to schedule unit in this node group", "switchType", switchType, "subCluster", subCluster, "unitKey", unitInfo.UnitKey, "nodeGroup", nodeGroupName)

		unitInfo.StartUnitTraceContext(tracing.RootSpan, tracing.SchedulerScheduleSpan, tracing.WithEverScheduledTag(unitInfo.EverScheduled))
		unitInfo.SetUnitTraceContextFields(tracing.SchedulerScheduleSpan, tracing.WithNodeGroupField(nodeGroupName))

		unitResult := gs.scheduleUnitInNodeGroup(ctx, unitInfo, unitFramework, nodeGroup)
		scheduleSucceed := (unitInfo.EverScheduled && len(unitResult.SuccessfulPods) > 0) || len(unitResult.SuccessfulPods) >= unitInfo.MinMember
		if scheduleSucceed && gs.applyToCache(ctx, unitInfo, unitResult) {
			msg := "Schedule unit succeeded both for snapshot and cache"
			klog.V(4).InfoS(msg, "switchType", switchType, "subCluster", subCluster, "unitKey", unitInfo.UnitKey, "nodeGroup", nodeGroupName)

			unitInfo.SetUnitTraceContextTags(tracing.SchedulerScheduleSpan, tracing.WithResultTag(tracing.ResultSuccess))
			unitInfo.SetUnitTraceContextFields(tracing.SchedulerScheduleSpan, tracing.WithMessageField(msg))

			// scheduling successfully, don't un-reserve all successful pods to avoid redundant caching operations
			unitResult.Successfully = true
			metrics.UnitScheduleResultObserve(unitInfo.QueuedUnitInfo.GetUnitProperty(), metrics.UnitScheduleSucceed, float64(unitInfo.MinMember))
		} else {
			// reset running unit info and un-reserve successful pods
			gs.resetRunningUnitInfo(ctx, unitInfo, unitResult, nodeGroupName)

			msg := "Failed to schedule unit in this node group"
			klog.InfoS(msg,
				"switchType", switchType, "subCluster", subCluster, "nodeGroup", nodeGroupName, "scheduleSucceedInSnapshot", scheduleSucceed,
				"unitMessage", unitMessage, "failureMessage", unitResult.Details.FailureMessage())

			unitInfo.SetUnitTraceContextTags(tracing.SchedulerScheduleSpan, tracing.WithResultTag(tracing.ResultFailure))
			unitInfo.SetUnitTraceContextFields(tracing.SchedulerScheduleSpan, tracing.WithMessageField(msg),
				tracing.WithMessageField(unitResult.Details.FailureMessage()))

			// TODO: remove this debug mode annotation setting after printing the detailed messages
			if queuedUnitInfo.IsDebugModeOn() {
				klog.V(4).InfoS("DEBUG: unit can not be scheduled in this node group",
					"switchType", switchType,
					"subCluster", subCluster,
					"unitKey", unitInfo.UnitKey,
					"nodeGroup", nodeGroupName)
				for _, podKey := range unitResult.SuccessfulPods {
					klog.V(4).InfoS("DEBUG: this pod can be scheduled in this attempt",
						"switchType", switchType,
						"unit", unitInfo.UnitKey,
						"pod", podKey,
						"node", unitInfo.DispatchedPods[podKey].NodeToPlace,
						"victims", unitInfo.DispatchedPods[podKey].Victims)
				}
			}

			failedReason := metrics.UnitScheduleFailed
			if scheduleSucceed {
				failedReason = metrics.UnitApplyToCacheFailed
			}
			metrics.UnitScheduleResultObserve(unitInfo.QueuedUnitInfo.GetUnitProperty(), failedReason, float64(unitInfo.MinMember))
		}

		unitInfo.FinishUnitTraceContext(tracing.SchedulerScheduleSpan)
		// keep the scheduling result with most successful Pods.
		if len(unitResult.SuccessfulPods) >= len(finalUnitResult.SuccessfulPods) {
			finalUnitResult = unitResult
		}

		if unitResult.Successfully {
			break
		}
	}

	errMessage := fmt.Sprintf("Failed to schedule unit. unit message:%v; failure message:%v", unitMessage, finalUnitResult.Details.FailureMessage())

	// if scheduling failed, stop the workflow and return
	if !finalUnitResult.Successfully {
		gs.recordUnitSchedulingResults(queuedUnitInfo, false, FailToScheduleUnit, core.ReturnAction, helper.TruncateMessage(errMessage))
		klog.V(4).InfoS(errMessage)

		if err := gs.updateFailedScheduleUnit(unitInfo.QueuedUnitInfo.ScheduleUnit, finalUnitResult.Details); err != nil {
			klog.InfoS(
				"Failed to update schedule unit",
				"switchType", switchType,
				"subCluster", subCluster,
				"unitKey", unitInfo.UnitKey,
				"message", finalUnitResult.Details.FailureMessage())
		}

		// re-enqueue pods based on the `schedulingSuccessfully` value of scheduling result
		// TODO: add more specific error messages -> attach scheduling errors to scheduling result
		gs.handleSchedulingUnitFailure(ctx, finalUnitResult, unitInfo, errors.New(errMessage), "SchedulingFailed")

		return
	}

	message := fmt.Sprintf("Schedule unit successfully. uint message: %v; successful pods:%d, failed pods:%d",
		unitMessage, len(finalUnitResult.SuccessfulPods), len(finalUnitResult.FailedPods))
	klog.V(4).InfoS("Scheduled unit successfully", "unitKey", unitInfo.UnitKey, "numSuccessfulPods", len(finalUnitResult.SuccessfulPods), "numFailedPods", len(finalUnitResult.FailedPods))
	gs.recordUnitSchedulingResults(queuedUnitInfo, true,
		"ScheduleUnitSuccessfully", core.ContinueAction, helper.TruncateMessage(message))

	// in case of scheduling partially success
	gs.handleSchedulingUnitFailure(ctx, finalUnitResult, unitInfo, errors.New(errMessage), "SchedulingFailed")

	// TODO: actually we'd better delete cached nodes from scheduler cache for PodGroup(instances)
	// in order not to cause conflicts with next round of scheduling (rejected by binder),
	// especially for pods asking for job level affinities(node group)
	// but since it is hard to figure out if the incoming pods are "next round" (may also be all-min instances)
	// so leave a TODO here
	// we have already taken this into account in pod scheduling workflow (predicate & priority),
	// so keeping these nodes in cache is ok

	// TODO: reserve all successful pods when we implement mark/unmark in cache

	// scheduling successfully, update the successful scheduled pods
	go gs.PersistSuccessfulPods(ctx, finalUnitResult, unitInfo)
}

func (gs *unitScheduler) constructSchedulingUnitInfo(ctx context.Context, queuedUnitInfo *framework.QueuedUnitInfo) (*core.SchedulingUnitInfo, error) {
	unitInfo := &core.SchedulingUnitInfo{
		UnitKey:        queuedUnitInfo.UnitKey,
		QueuedUnitInfo: queuedUnitInfo,
		UnitCycleState: framework.NewCycleState(),
	}

	unit := queuedUnitInfo.ScheduleUnit
	minMember, err := unit.GetMinMember()
	if err != nil {
		return unitInfo, err
	}
	unitInfo.MinMember = minMember
	unitInfo.EverScheduled = gs.Cache.GetUnitSchedulingStatus(unitInfo.UnitKey) == unitstatus.ScheduledStatus

	// run this before any potential error to make sure that all pods(queued pod info) are stored in RunningUnitInfo
	gs.constructRunningUnitInfo(ctx, unit, unitInfo)
	if !unitInfo.EverScheduled && unitInfo.MinMember > unitInfo.AllMember {
		return unitInfo, fmt.Errorf("min member is greater than all member which is unexpected")
	}

	// only when the node partition is Physical and the preemption feature is disabled,
	// we will reset the selected scheduler annotation and let dispatcher re-dispatch these pods when scheduling failed
	// TODO: revisit this
	if gs.disablePreemption {
		unitInfo.DispatchToAnotherScheduler = true
	}

	return unitInfo, nil
}

func (gs *unitScheduler) constructRunningUnitInfo(ctx context.Context, unit framework.ScheduleUnit, unitInfo *core.SchedulingUnitInfo) {
	runningUnitMap := unitInfo.DispatchedPods
	if runningUnitMap == nil {
		runningUnitMap = make(map[string]*core.RunningUnitInfo)
		unitInfo.DispatchedPods = runningUnitMap
	}

	allMember := 0
	for _, podInfo := range unit.GetPods() {
		podKey := podutil.GetPodKey(podInfo.Pod)
		podProperty := podInfo.GetPodProperty()
		podTrace := tracing.NewSchedulingTrace(
			podInfo.Pod,
			podProperty.ConvertToTracingTags(),
			tracing.WithSchedulerOption(),
			tracing.WithScheduler(gs.schedulerName), // TODO: hook?
		)

		runningUnitMap[podKey] = &core.RunningUnitInfo{
			QueuedPodInfo: podInfo,
			ClonedPod:     getAndInitClonedPod(podTrace.GetRootSpanContext(), podInfo),
			Trace:         podTrace,
		}
		allMember++
	}
	unitInfo.AllMember = allMember
}

func (gs *unitScheduler) scheduleUnitInNodeGroup(ctx context.Context, unitInfo *core.SchedulingUnitInfo, unitFramework unitruntime.SchedulerUnitFramework, nodeGroup framework.NodeGroup) *core.UnitResult {
	// Reset SchedulingUnitInfo
	unitInfo.Reset()

	// Scheduling & Preempting
	unitInfo.StartUnitTraceContext(tracing.SchedulerScheduleSpan, tracing.SchedulerScheduleUnitSpan)
	scheduleResult := unitFramework.Scheduling(ctx, unitInfo, nodeGroup)

	unitInfo.SetUnitTraceContextFields(tracing.SchedulerScheduleUnitSpan, tracing.WithMessageField(scheduleResult.Marshal()))
	unitInfo.SetUnitTraceContextFields(tracing.SchedulerScheduleUnitSpan, tracing.WithErrorFields(tracing.TruncateErrors(scheduleResult.Details.GetErrors()))...)
	unitInfo.FinishUnitTraceContext(tracing.SchedulerScheduleUnitSpan)

	if gs.disablePreemption {
		return core.TransferToUnitResult(unitInfo, scheduleResult.Details, scheduleResult.SuccessfulPods, scheduleResult.FailedPods)
	}

	unitInfo.StartUnitTraceContext(tracing.SchedulerScheduleSpan, tracing.SchedulerPreemptUnitSpan)
	preemptResult := unitFramework.Preempting(ctx, unitInfo, nodeGroup)

	unitInfo.SetUnitTraceContextFields(tracing.SchedulerPreemptUnitSpan, tracing.WithMessageField(preemptResult.Marshal()))
	unitInfo.SetUnitTraceContextFields(tracing.SchedulerPreemptUnitSpan, tracing.WithErrorFields(tracing.TruncateErrors(preemptResult.Details.GetErrors()))...)
	unitInfo.FinishUnitTraceContext(tracing.SchedulerPreemptUnitSpan)
	return core.TransferToUnitResult(unitInfo, preemptResult.Details, append(scheduleResult.SuccessfulPods, preemptResult.SuccessfulPods...), preemptResult.FailedPods)
}

func (gs *unitScheduler) resetRunningUnitInfo(ctx context.Context, unitInfo *core.SchedulingUnitInfo, result *core.UnitResult, nodeGroupName string) {
	// un-reserve pods
	gs.unReservePods(ctx, unitInfo, result, nodeGroupName)

	for _, runningUnitInfo := range unitInfo.DispatchedPods {
		runningUnitInfo.Victims = nil
		runningUnitInfo.NodeToPlace = ""
		runningUnitInfo.ClonedPod = getAndInitClonedPod(runningUnitInfo.Trace.GetRootSpanContext(), runningUnitInfo.QueuedPodInfo)
	}
}

func (gs *unitScheduler) unReservePods(ctx context.Context, unitInfo *core.SchedulingUnitInfo, result *core.UnitResult, nodeGroupName string) {
	snapshot, switchType, subCluster := gs.Snapshot, gs.switchType, gs.subCluster
	for _, podKey := range result.SuccessfulPods {
		runningUnitInfo := unitInfo.DispatchedPods[podKey]
		if runningUnitInfo != nil && runningUnitInfo.ClonedPod != nil {
			cachePodInfo := framework.MakeCachePodInfoWrapper().
				Pod(runningUnitInfo.ClonedPod).
				Victims(runningUnitInfo.Victims).
				Obj()
			if err := snapshot.ForgetPod(cachePodInfo); err != nil {
				// TODO: do we need to panic here ?
				klog.InfoS("Failed to un-reserve pod", "switchType", switchType, "subCluster", subCluster, "pod", klog.KObj(unitInfo.DispatchedPods[podKey].ClonedPod),
					"unitKey", unitInfo.UnitKey, "nodeGroup", nodeGroupName, "err", err)
			}
		}
	}
}

func (gs *unitScheduler) handleSchedulingUnitFailure(ctx context.Context, result *core.UnitResult, unitInfo *core.SchedulingUnitInfo,
	err error, reason string,
) {
	queue, switchType, subCluster := gs.Queue, gs.switchType, gs.subCluster
	// 1. remove successful pods before re-enqueue
	if result.Successfully {
		for _, podKey := range result.SuccessfulPods {
			podInfo := unitInfo.DispatchedPods[podKey].QueuedPodInfo
			if podInfo != nil {
				unitInfo.QueuedUnitInfo.DeletePod(podInfo)
			}
		}
	}
	// 2. refresh the pod info
	podInfos := unitInfo.QueuedUnitInfo.GetPods()
	for i := range podInfos {
		podInfo := podInfos[i]
		cachedPod, err := gs.podLister.Pods(podInfo.Pod.Namespace).Get(podInfo.Pod.Name)
		if apierrors.IsNotFound(err) {
			klog.InfoS("WARN: failed to re-enqueue the pod cause it doesn't exist in informer cache", "pod", klog.KObj(podInfo.Pod))
			_ = unitInfo.QueuedUnitInfo.DeletePod(podInfo)
		} else if gs.skipPodSchedule(cachedPod) {
			klog.InfoS("WARN: failed to re-enqueue the pod cause it already exist in scheduler cache", "pod", klog.KObj(cachedPod))
			_ = unitInfo.QueuedUnitInfo.DeletePod(podInfo)
		} else {
			// refresh queue span
			podInfo.QueueSpan = tracing.NewSpanInfo(podInfo.GetPodProperty().ConvertToTracingTags())
			if err == nil {
				podInfo.Pod = cachedPod.DeepCopy()
			}
		}
	}

	// 3. re-enqueue
	if !unitInfo.DispatchToAnotherScheduler && unitInfo.QueuedUnitInfo.NumPods() > 0 {
		unitInfo.QueuedUnitInfo.SetEnqueuedTimeStamp(time.Now())
		reEnqueueErr := queue.AddUnschedulableIfNotPresent(unitInfo.QueuedUnitInfo, queue.SchedulingCycle())
		if reEnqueueErr != nil {
			klog.InfoS("Failed to re-enqueue the unit", "switchType", switchType, "subCluster", subCluster, "unitKey", unitInfo.UnitKey, "err", reEnqueueErr)
		}
	}
	// 4. update pod condition
	for i := range podInfos {
		// use the error from scheduling result if it exists, it's more accurate
		podError := err
		if result.Details != nil {
			if got := result.Details.GetPodError(podutil.GetPodKey(podInfos[i].Pod)); got != nil {
				podError = got
			}
		}
		if updateErr := updateFailedSchedulingPod(gs.client, gs.schedulerName, podInfos[i].Pod, !unitInfo.DispatchToAnotherScheduler, podError, reason); updateErr != nil {
			klog.InfoS("Failed to update the failed scheduling pod", "switchType", switchType, "subCluster", subCluster, "pod", klog.KObj(podInfos[i].Pod), "err", updateErr)
		}
	}
}

func (gs *unitScheduler) recordUnitSchedulingResults(unitInfo *framework.QueuedUnitInfo, successful bool, reason string, action string, message string) {
	if unitInfo == nil || unitInfo.ScheduleUnit == nil {
		return
	}
	// TODO: send warning event to unit object, e.g. PodGroup
	// send events to each pod
	var eventType string
	if successful {
		eventType = v1.EventTypeNormal
	} else {
		eventType = v1.EventTypeWarning
	}
	for _, podInfo := range unitInfo.GetPods() {
		if podInfo == nil {
			klog.ErrorS(nil, "DEBUG: got a nil PodInfo, which shouldn't happen", "unitKey", unitInfo.UnitKey, "unitPodInfos", unitInfo.ScheduleUnit)
			klog.FlushAndExit(klog.ExitFlushTimeout, 1)
		}
		if podInfo.Pod == nil {
			continue
		}
		gs.Recorder.Eventf(podInfo.Pod, nil, eventType, reason, action, message)
	}

	if unitInfo.Type() == framework.PodGroupUnitType {
		// record event for PodGroup.
		pg, err := gs.pgLister.PodGroups(unitInfo.GetNamespace()).Get(unitInfo.GetName())
		if err != nil {
			if !apierrors.IsNotFound(err) {
				klog.InfoS("Failed to get PodGroup", "unitNamespace", unitInfo.GetNamespace(), "unitName", unitInfo.GetName(), "err", err)
			}
			// do nothing, if PodGroup is not found.
			return
		}
		gs.Recorder.Eventf(pg, nil, eventType, reason, action, helper.TruncateMessage(message))
	}
}

// updateFailedScheduleUnit reports the failure details for PodGroupUnit, by updating the condition.
func (gs *unitScheduler) updateFailedScheduleUnit(scheduleUnit framework.ScheduleUnit, failureDetails *interpretabity.UnitSchedulingDetails) error {
	// do nothing for other Unit, right now.
	if scheduleUnit.Type() != framework.PodGroupUnitType {
		return nil
	}

	pg, err := gs.pgLister.PodGroups(scheduleUnit.GetNamespace()).Get(scheduleUnit.GetName())
	if err != nil {
		return fmt.Errorf("failed to update ScheduleUnit. type:%v; key:%v/%v; err:%v", scheduleUnit.Type(), scheduleUnit.GetNamespace(), scheduleUnit.GetName(), err)
	}

	cond := schedulingv1a1.PodGroupCondition{
		Phase:              schedulingv1a1.PodGroupPreScheduling,
		Status:             v1.ConditionTrue,
		LastTransitionTime: metav1.Now(),
		Reason:             FailToScheduleUnit,
		Message:            failureDetails.FailureMessage(),
	}

	return interpretabity.UpdatePreSchedulingCondition(gs.crdClient, pg, cond)
}

func (gs *unitScheduler) applyToCache(ctx context.Context, unitInfo *core.SchedulingUnitInfo, result *core.UnitResult) bool {
	cache, switchType, subCluster := gs.Cache, gs.switchType, gs.subCluster
	for i, key := range result.SuccessfulPods {
		runningPodInfo := unitInfo.DispatchedPods[key]
		cachePodInfo := framework.MakeCachePodInfoWrapper().Pod(runningPodInfo.ClonedPod).Victims(runningPodInfo.Victims).Obj()

		traceContext := runningPodInfo.Trace.NewTraceContext(tracing.SchedulerScheduleSpan, tracing.SchedulerAssumePodSpan)
		err := cache.AssumePod(cachePodInfo)
		defer tracing.AsyncFinishTraceContext(traceContext, time.Now())

		if err != nil {
			klog.ErrorS(err, "Failed to assume pod in scheduler cache, will forget the pod",
				"switchType", switchType, "subCluster", subCluster,
				"pod", klog.KObj(runningPodInfo.ClonedPod),
				"node", runningPodInfo.NodeToPlace)

			if err := cache.ForgetPod(cachePodInfo); err != nil {
				msg := "Failed to forget pod in scheduler cache after the assume pod failure occured"
				traceContext.WithFields(tracing.WithMessageField(msg))
				traceContext.WithFields(tracing.WithErrorField(err))
				traceContext.WithTags(tracing.WithResultTag(tracing.ResultFailure))
				klog.ErrorS(err, msg, "pod", klog.KObj(runningPodInfo.ClonedPod), "node", runningPodInfo.NodeToPlace)
			}

			// Considering that there may be sequential dependencies during different Pods, once an AssumePod failure occured,
			// subsequent operations will no longer continue.
			//
			// At the same time, we will decide to apply or roll back the previous operations based on whether the unit
			// scheduling conditions are met.
			if !unitInfo.EverScheduled && i < unitInfo.MinMember {
				// For min-fail, we revert the operations and return false.
				msg := "Failed to assume pod in scheduler cache and result in a min-fail, ready to revert"
				klog.InfoS(msg, "numSuccessfulPods", len(result.SuccessfulPods), "podIndex", i)
				for index := 0; index < i; index++ {
					runningPodInfo := unitInfo.DispatchedPods[result.SuccessfulPods[index]]
					previousTraceContext := runningPodInfo.Trace.GetTraceContext(tracing.SchedulerAssumePodSpan)
					if err := cache.ForgetPod(cachePodInfo); err != nil {
						msg := "Failed to forget pod in scheduler cache during revert"
						previousTraceContext.WithFields(tracing.WithMessageField(msg))
						previousTraceContext.WithFields(tracing.WithErrorField(err))
						previousTraceContext.WithTags(tracing.WithResultTag(tracing.ResultFailure))
						klog.ErrorS(err, msg,
							"pod", klog.KObj(runningPodInfo.ClonedPod),
							"node", runningPodInfo.NodeToPlace)
					}
				}

				// Remove all Pods from successfulPods, which failed to assume cache, and mark them as FailedPods.
				result.Details.AddPodsError(err, result.SuccessfulPods...)
				result.FailedPods = append(result.FailedPods, result.SuccessfulPods...)
				result.SuccessfulPods = []string{}
				return false
			} else {
				msg := "Failed to assume pod in scheduler cache but the min-member can be met or unit ever scheduled"
				traceContext.WithFields(tracing.WithMessageField(msg))
				klog.InfoS(msg, "unitKey", unitInfo.UnitKey, "numSuccessfulPods", len(result.SuccessfulPods), "podIndex", i)

				// Otherwise, move the left pods from successfulPods to failedPods and return true.
				result.Details.AddPodsError(err, result.SuccessfulPods[i:]...)
				result.FailedPods = append(result.FailedPods, result.SuccessfulPods[i:]...)
				result.SuccessfulPods = result.SuccessfulPods[:i]
				return true
			}
		} else {
			traceContext.WithTags(tracing.WithResultTag(tracing.ResultSuccess))
		}
	}
	return true
}

func (gs *unitScheduler) PersistSuccessfulPods(ctx context.Context,
	result *core.UnitResult, unitInfo *core.SchedulingUnitInfo,
) {
	var failedPods []string
	var fpMutex sync.Mutex

	cache, switchType, subCluster := gs.Cache, gs.switchType, gs.subCluster
	unitProperty := unitInfo.QueuedUnitInfo.GetUnitProperty()

	updatePod := func(i int) {
		podKey := result.SuccessfulPods[i]
		runningUnitInfo := unitInfo.DispatchedPods[podKey]

		podProperty := runningUnitInfo.QueuedPodInfo.GetPodProperty()
		metrics.SchedulerGoroutinesInc(podProperty, metrics.UpdatingPod)
		defer metrics.SchedulerGoroutinesDec(podProperty, metrics.UpdatingPod)

		podTrace := runningUnitInfo.Trace
		updatingTraceContext := podTrace.NewTraceContext(tracing.RootSpan, tracing.SchedulerUpdatingPodSpan)
		defer tracing.AsyncFinishTraceContext(updatingTraceContext, time.Now())

		// exclude pods that are not scheduled successfully within one scheduling attempt
		if unitInfo.QueuedUnitInfo.Attempts > 1 ||
			!runningUnitInfo.QueuedPodInfo.InitialPreemptAttemptTimestamp.IsZero() {
			runningUnitInfo.ClonedPod.Annotations[podutil.E2EExcludedPodAnnotationKey] = "true"
		}

		err := util.PatchPod(gs.client, runningUnitInfo.QueuedPodInfo.Pod, runningUnitInfo.ClonedPod)
		if err == nil {
			updatingTraceContext.WithTags(tracing.WithResultTag(tracing.ResultSuccess))
			if err := cache.FinishReserving(runningUnitInfo.ClonedPod); err != nil {
				klog.InfoS("Failed to finish reserving", "switchType", switchType, "subCluster", subCluster, "podKey", podKey, "unitKey", unitInfo.UnitKey, "err", err)
			}

			metrics.ObservePodSchedulingLatency(podProperty, getAttemptsLabel(runningUnitInfo.QueuedPodInfo), helper.SinceInSeconds(runningUnitInfo.QueuedPodInfo.InitialAttemptTimestamp))

			klog.V(2).InfoS("Persisted this pod successfully, and the pod now is in assumed state",
				"switchType", switchType, "subCluster", subCluster,
				"pod", klog.KObj(runningUnitInfo.ClonedPod),
				"unitKey", unitInfo.UnitKey)
			gs.Recorder.Eventf(runningUnitInfo.ClonedPod, nil, v1.EventTypeNormal,
				"PersistPodSuccessfully", core.ContinueAction, "Reserve pod successfully, and pod now is in assumed state")
		} else {
			updatingTraceContext.WithTags(tracing.WithResultTag(tracing.ResultFailure))
			updatingTraceContext.WithFields(tracing.WithErrorField(err))

			if apierrors.IsNotFound(err) {
				klog.InfoS("Failed to persist this pod because the pod has been deleted",
					"switchType", switchType, "subCluster", subCluster,
					"pod", klog.KObj(runningUnitInfo.ClonedPod),
					"unitKey", unitInfo.UnitKey, "err", err)
				// this pod is deleted, forget it from cache if it is still in assumed state in cache
				if assumed, _ := gs.Cache.IsAssumedPod(runningUnitInfo.ClonedPod); assumed {
					cachePodInfo := framework.MakeCachePodInfoWrapper().
						Pod(unitInfo.DispatchedPods[podKey].ClonedPod).
						Obj()
					if err := gs.Cache.ForgetPod(cachePodInfo); err != nil {
						klog.InfoS("Failed to forget pod when we found out that the pod had been deleted", "pod", klog.KObj(runningUnitInfo.ClonedPod), "err", err)
					}
				}
				return
			}
			// TODO: do we want to forget this pod directly
			// TODO: even not: if this pod is deleted or updated to other states, we need to try to forget it too,
			// since this pod is reserved to cache in scheduling workflow
			/*if err := gs.SchedulerCache.ForgetPod(unitInfo.DispatchedPods[podKey].ClonedPod); err != nil {
				klog.ErrorS(err, "Forget pod failed", "podKey", podKey, "unit", unitInfo.UnitKey)
				panic("reserve pod successfully but forget failed, this may cause data inconsistent, so panic...")
			}*/
			fpMutex.Lock()
			failedPods = append(failedPods, podKey)
			fpMutex.Unlock()

			klog.InfoS("Failed to persist this pod",
				"switchType", switchType, "subCluster", subCluster,
				"pod", klog.KObj(runningUnitInfo.ClonedPod),
				"unitKey", unitInfo.UnitKey, "err", err)
			gs.Recorder.Eventf(runningUnitInfo.ClonedPod, nil, v1.EventTypeWarning,
				"FailToPersistPod", core.ContinueAction, helper.TruncateMessage(err.Error()))
		}
	}

	parallelize.Until(ctx, len(result.SuccessfulPods), updatePod)
	metrics.SchedulerUnitE2ELatencyObserve(unitProperty, helper.SinceInSeconds(unitInfo.QueuedUnitInfo.InitialAttemptTimestamp))

	// if we fail to patch some of them pods, this probably may due to network problem.
	// even if we try to reset these pods (API call), error will still appear.
	// so, keep reserving them in cache and add them to failed-task-reconciler to retry periodically.
	// since these failed pods are in assumed state in cache, if they are deleted or updated, cache won't react to these events,
	// so we need to let the reconciler take this into account
	// TODO: revisit this later
	if len(failedPods) > 0 {
		klog.InfoS("Failed to patch some pods, and will add them to failed task reconciler", "switchType", switchType, "subCluster", subCluster, "failedPods", failedPods)
		for _, podKey := range failedPods {
			gs.Reconciler.AddFailedTask(reconciler.NewFailedPatchTask(framework.MakeCachePodInfoWrapper().Pod(unitInfo.DispatchedPods[podKey].ClonedPod).Obj()))
		}
	}
}

func (gs *unitScheduler) skipPodSchedule(pod *v1.Pod) bool {
	isCached, err := gs.Cache.IsCachedPod(pod)
	if err != nil {
		return false
	}
	// Since we got the Pod from informer cache, it's ok to call `podutil.AssumedPod` or `podutil.BoundPod` here.
	return isCached || podutil.AssumedPod(pod) || podutil.BoundPod(pod)

}
