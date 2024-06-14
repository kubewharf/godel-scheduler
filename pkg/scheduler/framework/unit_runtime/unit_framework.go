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

package unitruntime

import (
	"context"
	"fmt"
	"math"
	"sort"
	"strconv"
	"time"

	v1 "k8s.io/api/core/v1"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/klog/v2"

	framework "github.com/kubewharf/godel-scheduler/pkg/framework/api"
	"github.com/kubewharf/godel-scheduler/pkg/scheduler/core"
	"github.com/kubewharf/godel-scheduler/pkg/scheduler/framework/handle"
	"github.com/kubewharf/godel-scheduler/pkg/scheduler/framework/unit_plugins/joblevelaffinity"
	"github.com/kubewharf/godel-scheduler/pkg/scheduler/metrics"
	"github.com/kubewharf/godel-scheduler/pkg/util/helper"
	"github.com/kubewharf/godel-scheduler/pkg/util/interpretabity"
	podutil "github.com/kubewharf/godel-scheduler/pkg/util/pod"
	"github.com/kubewharf/godel-scheduler/pkg/util/tracing"
)

type SchedulerUnitFramework interface {
	RunLocatingPlugins(ctx context.Context, unit framework.ScheduleUnit, unitCycleState *framework.CycleState, nodeGroup framework.NodeGroup) (framework.NodeGroup, *framework.Status)
	RunGroupingPlugin(ctx context.Context, unit framework.ScheduleUnit, unitCycleState *framework.CycleState, nodeGroup framework.NodeGroup) ([]framework.NodeGroup, *framework.Status)

	// Scheduling & Preempting in a specific NodeGroup instead of NodeGroups.
	Scheduling(ctx context.Context, unitInfo *core.SchedulingUnitInfo, nodeGroup framework.NodeGroup) *core.UnitSchedulingResult
	Preempting(ctx context.Context, unitInfo *core.SchedulingUnitInfo, nodeGroup framework.NodeGroup) *core.UnitPreemptionResult
}

// ------------------------------------------------------------------------------------------

type UnitFramework struct {
	handle         handle.UnitFrameworkHandle
	schedulerHooks core.SchedulerHooks

	locatingPlugins []framework.LocatingPlugin
	groupingPlugins map[string]framework.GroupingPlugin
}

// ATTENTION: Considering that UnitPlugin belongs to scheduling optimization behavior, all implemented plugins should be registered.
// The plugin should adaptively execute the corresponding logic based on the FeatureGate and the ScheduleUnit to be scheduled,
// without the need to adjust the plugin's registration or not through a configuration file.
func NewUnitFramework(
	handle handle.UnitFrameworkHandle,
	schedulerHooks core.SchedulerHooks,
	pluginRegistry framework.PluginMap,
	pluginOrder framework.PluginOrder,
	unit framework.ScheduleUnit, // TODO: Support customized plugins based on specific ScheduleUnit.
) SchedulerUnitFramework {
	f := &UnitFramework{
		handle:          handle,
		schedulerHooks:  schedulerHooks,
		locatingPlugins: make([]framework.LocatingPlugin, 0),
		groupingPlugins: make(map[string]framework.GroupingPlugin),
	}

	for _, pl := range pluginRegistry {
		if locatingPlugin, ok := pl.(framework.LocatingPlugin); ok && locatingPlugin != nil {
			f.locatingPlugins = append(f.locatingPlugins, locatingPlugin)
		}
		if groupingPlugin, ok := pl.(framework.GroupingPlugin); ok && groupingPlugin != nil {
			f.groupingPlugins[groupingPlugin.Name()] = groupingPlugin
		}
	}

	// Sorting locating plugins
	if pluginOrder != nil {
		sort.SliceStable(f.locatingPlugins, func(i, j int) bool {
			// if the index of plugin is not in pluginOrder, use infinity as the index
			// so that the plugin will be put at the end of the slice
			iIndex, exist := pluginOrder[f.locatingPlugins[i].Name()]
			if !exist {
				klog.InfoS("WARN: Plugin was not found in the PluginOrder map", "pluginName", f.locatingPlugins[i].Name())
				iIndex = math.MaxInt32
			}
			jIndex, exist := pluginOrder[f.locatingPlugins[j].Name()]
			if !exist {
				klog.InfoS("WARN: Plugin was not found in the PluginOrder map", "pluginName", f.locatingPlugins[j].Name())
				jIndex = math.MaxInt32
			}
			return iIndex < jIndex
		})
	}

	return f
}

func (f *UnitFramework) RunLocatingPlugins(ctx context.Context, unit framework.ScheduleUnit, unitCycleState *framework.CycleState, nodeGroup framework.NodeGroup) (framework.NodeGroup, *framework.Status) {
	// Run all Locating Plugins.
	for _, pl := range f.locatingPlugins {
		var status *framework.Status
		nodeGroup, status = pl.Locating(ctx, unit, unitCycleState, nodeGroup)
		if !status.IsSuccess() {
			return nil, status
		}
	}
	return nodeGroup, nil
}

func (f *UnitFramework) RunGroupingPlugin(ctx context.Context, unit framework.ScheduleUnit, unitCycleState *framework.CycleState, nodeGroup framework.NodeGroup) ([]framework.NodeGroup, *framework.Status) {
	var nodeGroups []framework.NodeGroup
	switch {
	case framework.UnitRequireJobLevelAffinity(unit):
		groupingPlugin, ok := f.groupingPlugins[joblevelaffinity.Name]
		if ok && groupingPlugin != nil {
			gotNodeGroups, status := groupingPlugin.Grouping(ctx, unit, unitCycleState, nodeGroup)
			if !status.IsSuccess() {
				return nil, status
			}
			nodeGroups = gotNodeGroups
		} else {
			return nil, framework.AsStatus(fmt.Errorf("No JobLevelAffinity Grouping registered, which is unexpected"))
		}
	default:
		// By default
		nodeGroups = []framework.NodeGroup{nodeGroup}
	}

	availableCount := 0
	for i := 0; i < len(nodeGroups); i++ {
		if nodeGroups[i] == nil {
			// This shouldn't happen, but just in case.
			continue
		}
		if err := nodeGroups[i].Validate(); err != nil {
			klog.ErrorS(err, "Fail to validate node group", "unit", unit.GetKey(), "nodeGroup", nodeGroup.GetKey())
			continue
		}
		nodeGroups[availableCount] = nodeGroups[i]
		availableCount++
	}
	nodeGroups = nodeGroups[:availableCount]
	if availableCount == 0 {
		return nil, framework.AsStatus(fmt.Errorf("no valid node groups are available, please double check if the subcluster exists"))
	}

	return nodeGroups, nil
}

func (f *UnitFramework) Scheduling(ctx context.Context, unitInfo *core.SchedulingUnitInfo, nodeGroup framework.NodeGroup) *core.UnitSchedulingResult {
	// TODO: carry more unit indicators for making better scheduling decisions
	// TODO: get all member from pod owner if unit is not pod group
	usr := &framework.UnitSchedulingRequest{
		EverScheduled: unitInfo.EverScheduled,
		AllMember:     unitInfo.AllMember,
	}
	markIndex := !unitInfo.EverScheduled

	result := &core.UnitSchedulingResult{
		SuccessfulPods: []string{},
		FailedPods:     []string{},
		Details:        interpretabity.NewUnitSchedulingDetails(interpretabity.Scheduling, len(unitInfo.DispatchedPods)),
	}

	templateCount := len(unitInfo.NotScheduledPodKeysByTemplate)
	commonPreemptionState := framework.NewCycleState()

	for tmplKey, podKeys := range unitInfo.NotScheduledPodKeysByTemplate {
		klog.InfoS("Will schedule in specific node group for the pod template", "template", tmplKey, "nodeGroup", nodeGroup.GetKey())

		podKeysList := podKeys.UnsortedList()
		for i, podKey := range podKeysList {
			runningUnitInfo := unitInfo.DispatchedPods[podKey]

			podTrace := runningUnitInfo.Trace
			scheduleTraceContext := podTrace.NewTraceContext(tracing.SchedulerScheduleUnitSpan, tracing.SchedulerSchedulePodSpan)
			scheduleTraceContext.WithFields(tracing.WithTemplateKeyField(tmplKey))

			scheduled, err := f.scheduleOneUnitInstance(ctx, unitInfo.ScheduledIndex, markIndex, unitInfo.NodeToStatusMapByTemplate,
				runningUnitInfo, unitInfo.UnitKey, unitInfo.QueuedUnitInfo.QueuePriorityScore, unitInfo.UnitCycleState, commonPreemptionState,
				nodeGroup, usr)
			defer tracing.AsyncFinishTraceContext(scheduleTraceContext, time.Now())

			if scheduled {
				scheduleTraceContext.WithTags(tracing.WithResultTag(tracing.ResultSuccess))

				klog.V(4).InfoS("Pod was able to be scheduled in this attempt",
					"switchType", f.handle.SwitchType(), "subCluster", f.handle.SubCluster(),
					"pod", podKey,
					"unitKey", unitInfo.UnitKey,
					"nodeGroup", nodeGroup.GetKey())

				// TODO: more infos...
				result.SuccessfulPods = append(result.SuccessfulPods, podKey)
				result.Details.AddSuccessfulPods(podKey)

				unitInfo.ScheduledIndex = (unitInfo.ScheduledIndex) + 1
				delete(unitInfo.NotScheduledPodKeysByTemplate[tmplKey], podKey)
			} else {
				scheduleTraceContext.WithTags(tracing.WithResultTag(tracing.ResultFailure))
				scheduleTraceContext.WithFields(tracing.WithErrorField(err))

				klog.V(4).InfoS("Failed to schedule pod in this attempt",
					"switchType", f.handle.SwitchType(), "subCluster", f.handle.SubCluster(),
					"pod", klog.KObj(runningUnitInfo.ClonedPod),
					"unitKey", unitInfo.UnitKey,
					"nodeGroup", nodeGroup.GetKey(),
					"err", err)

				result.FailedPods = append(result.FailedPods, podKey)
				result.Details.AddPodsError(err, podKey)
				// Move the rest pods belong to same template to FailedPods.
				for j := i + 1; j < len(podKeysList); j++ {
					podKey := podKeysList[j]
					runningUnitInfo := unitInfo.DispatchedPods[podKey]
					klog.V(4).InfoS("Quick fail template pod in this attempt",
						"switchType", f.handle.SwitchType(), "subCluster", f.handle.SubCluster(),
						"pod", klog.KObj(runningUnitInfo.ClonedPod),
						"unitKey", unitInfo.UnitKey,
						"nodeGroup", nodeGroup.GetKey(),
						"err", err)
					result.FailedPods = append(result.FailedPods, podKey)
				}
				break
			}
		}

		// TODO: revisit the quick logic.
		templateCount--
		if templateCount == 0 && !unitInfo.EverScheduled && unitInfo.AllMember-unitInfo.MinMember < len(result.FailedPods) {
			break
		}
	}

	return result
}

func (f *UnitFramework) Preempting(ctx context.Context, unitInfo *core.SchedulingUnitInfo, nodeGroup framework.NodeGroup) *core.UnitPreemptionResult {
	// TODO: carry more unit indicators for making better scheduling decisions
	// TODO: get all member from pod owner if unit is not pod group
	markIndex := !unitInfo.EverScheduled

	needPreempt := 0
	templateToNominatedNodes := map[string]*framework.CachedNominatedNodes{}
	for tmplKey, podKeys := range unitInfo.NotScheduledPodKeysByTemplate {
		if tmplKey == "" {
			continue
		}

		if len(podKeys) == 0 {
			delete(unitInfo.NotScheduledPodKeysByTemplate, tmplKey)
			continue
		}
		needPreempt += len(podKeys)
		templateToNominatedNodes[tmplKey] = framework.NewCachedNominatedNodes()
		templateToNominatedNodes[tmplKey].SetPodCount(len(podKeys))
	}

	result := &core.UnitPreemptionResult{
		SuccessfulPods: []string{},
		FailedPods:     []string{},
		Details:        interpretabity.NewUnitSchedulingDetails(interpretabity.Scheduling, needPreempt),
	}

	commonPreemptionState := framework.NewCycleState()
	for tmplKey, podKeys := range unitInfo.NotScheduledPodKeysByTemplate {
		klog.InfoS("Will preempt in specific node group for the pod template", "template", tmplKey, "nodeGroup", nodeGroup.GetKey())

		podKeysList := podKeys.UnsortedList()
		for i, podKey := range podKeysList {
			runningUnitInfo := unitInfo.DispatchedPods[podKey]

			podTrace := runningUnitInfo.Trace
			preemptTraceContext := podTrace.NewTraceContext(tracing.SchedulerPreemptUnitSpan, tracing.SchedulerPreemptPodSpan)
			preemptTraceContext.WithFields(tracing.WithTemplateKeyField(tmplKey), tracing.WithNodeGroupField(nodeGroup.GetKey()))

			scheduled, err := f.preemptOneUnitInstance(ctx, unitInfo.ScheduledIndex, markIndex, runningUnitInfo, unitInfo.UnitKey,
				unitInfo.QueuedUnitInfo.QueuePriorityScore, unitInfo.UnitCycleState, commonPreemptionState,
				nodeGroup, unitInfo.NodeToStatusMapByTemplate[tmplKey], templateToNominatedNodes[runningUnitInfo.QueuedPodInfo.OwnerReferenceKey])
			defer tracing.AsyncFinishTraceContext(preemptTraceContext, time.Now())

			if scheduled {
				klog.V(4).InfoS("Pod was able to preempt in this attempt",
					"switchType", f.handle.SwitchType(), "subCluster", f.handle.SubCluster(),
					"pod", podKey,
					"unitKey", unitInfo.UnitKey,
					"nodeGroup", nodeGroup.GetKey())

				preemptTraceContext.WithTags(tracing.WithResultTag(tracing.ResultSuccess))
				preemptTraceContext.WithFields(tracing.WithMessageField("Pod was able to preempt in this attempt"))

				// TODO: more infos...
				result.SuccessfulPods = append(result.SuccessfulPods, podKey)
				result.Details.AddSuccessfulPods(podKey)

				unitInfo.ScheduledIndex = (unitInfo.ScheduledIndex) + 1
				delete(unitInfo.NotScheduledPodKeysByTemplate[tmplKey], podKey)
			} else {
				klog.V(4).InfoS("Failed to preempt for pod in this attempt",
					"switchType", f.handle.SwitchType(), "subCluster", f.handle.SubCluster(),
					"pod", klog.KObj(runningUnitInfo.ClonedPod),
					"unitKey", unitInfo.UnitKey,
					"nodeGroup", nodeGroup.GetKey(),
					"err", err)

				result.FailedPods = append(result.FailedPods, podKey)
				result.Details.AddPodsError(err, podKey)

				preemptTraceContext.WithFields(tracing.WithMessageField("Failed to preempt for pod in this attempt"))
				preemptTraceContext.WithTags(tracing.WithResultTag(tracing.ResultFailure))

				// Move the rest pods belong to same template to FailedPods.
				for j := i + 1; j < len(podKeysList); j++ {
					podKey := podKeysList[j]
					runningUnitInfo := unitInfo.DispatchedPods[podKeysList[j]]
					klog.V(4).InfoS("Quick fail template pod in this attempt",
						"switchType", f.handle.SwitchType(), "subCluster", f.handle.SubCluster(),
						"pod", klog.KObj(runningUnitInfo.ClonedPod),
						"unitKey", unitInfo.UnitKey,
						"nodeGroup", nodeGroup.GetKey(),
						"err", err)
					result.FailedPods = append(result.FailedPods, podKey)
					result.Details.AddPodsError(err, podKey)
				}
				break
			}
		}

		if !unitInfo.EverScheduled && unitInfo.AllMember-unitInfo.MinMember < len(result.FailedPods) {
			break
		}
	}

	return result
}

// ----------------------------------------------------------------------------------------------------------------

// return value indicates whether we can schedule this pod, no matter in what ways
func (f *UnitFramework) scheduleOneUnitInstance(ctx context.Context, scheduledIndex int, markIndex bool, statusByTemplate framework.NodeToStatusMapByTemplate,
	runningUnitInfo *core.RunningUnitInfo, unitKey string, queuePriorityScore float64, unitCycleState, commonPreemptionState *framework.CycleState,
	nodeGroup framework.NodeGroup,
	usr *framework.UnitSchedulingRequest,
) (success bool, err error) {
	switchType, subCluster := f.handle.SwitchType(), f.handle.SubCluster()
	godelScheduler := f.schedulerHooks.PodScheduler()

	klog.V(4).InfoS("Attempting to schedule for pod",
		"switchType", switchType, "subCluster", subCluster,
		"pod", klog.KObj(runningUnitInfo.ClonedPod),
		"podPriority", podutil.GetPodPriority(runningUnitInfo.ClonedPod),
		"podQueuePriorityScore", queuePriorityScore,
		"unitKey", unitKey, "nodeGroup", nodeGroup.GetKey())

	clonedPod := runningUnitInfo.ClonedPod
	podProperty := runningUnitInfo.QueuedPodInfo.GetPodProperty()
	// TODO: if the pod is in assumed state, skip scheduling
	if f.skipPodSchedule(clonedPod) {
		klog.InfoS("WARN: Skipped scheduling this pod", "switchType", switchType, "subCluster", subCluster, "unitKey", unitKey, "pod", klog.KObj(clonedPod))
		return false, fmt.Errorf("skip scheduling this pod, key:%v", podutil.GetPodKey(clonedPod))
	}

	start := time.Now()
	defer func() {
		// add schedule result metric
		if success {
			metrics.PodScheduled(podProperty, helper.SinceInSeconds(start))
		} else if godelScheduler.DisablePreemption() {
			metrics.PodUnschedulable(podProperty, helper.SinceInSeconds(start))
		}
	}()

	podTrace := runningUnitInfo.Trace
	scheduleTraceContext := podTrace.GetTraceContext(tracing.SchedulerSchedulePodSpan)

	podKey, fwk, _, state, err := f.schedulerHooks.BootstrapSchedulePod(ctx, clonedPod, podTrace, nodeGroup.GetKey())
	if err != nil {
		errMessage := fmt.Sprintf("Failed to initialize pod: %v in node group: %v", err.Error(), nodeGroup.GetKey())
		f.schedulerHooks.EventRecorder().Eventf(clonedPod, nil, v1.EventTypeWarning, "FailToInitializePod", core.ReturnAction, helper.TruncateMessage(errMessage))
		klog.ErrorS(err, "Failed to initialize pod", "switchType", switchType, "subCluster", subCluster,
			"unitKey", unitKey, "pod", klog.KObj(clonedPod), "nodeGroup", nodeGroup.GetKey())
		metrics.PodScheduleError(podProperty, helper.SinceInSeconds(start))
		scheduleTraceContext.WithFields(tracing.WithMessageField("Failed to initialize pod"), tracing.WithErrorField(err))
		return false, err
	}

	scheduleTraceContext.WithFields(tracing.WithPlugins(fwk.ListPlugins())...)
	schedulingCycleCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	scheduleResult, err := godelScheduler.ScheduleInSpecificNodeGroup(schedulingCycleCtx, fwk, unitCycleState, commonPreemptionState, state, clonedPod, nodeGroup, usr, statusByTemplate[runningUnitInfo.QueuedPodInfo.OwnerReferenceKey])
	metrics.ObservePodEvaluatedNodes(podProperty.SubCluster, string(podProperty.Qos), f.handle.SchedulerName(), float64(scheduleResult.NumberOfEvaluatedNodes))
	metrics.ObservePodFeasibleNodes(podProperty.SubCluster, string(podProperty.Qos), f.handle.SchedulerName(), float64(scheduleResult.NumberOfFeasibleNodes))

	scheduleFailed := len(scheduleResult.SuggestedHost) == 0
	// scheduling failed
	if scheduleFailed {
		eventMsg := "Failed to schedule pod in node group: " + nodeGroup.GetKey() + ", error: " + errorStr(err)
		f.schedulerHooks.EventRecorder().Eventf(clonedPod, nil, v1.EventTypeWarning, "FailToSchedule", core.ContinueAction, helper.TruncateMessage(eventMsg))

		errMsg := "Failed to run scheduling, and will try preemption"
		klog.ErrorS(err, errMsg, "switchType", switchType, "subCluster", subCluster, "podKey", podKey, "nodeGroup", nodeGroup.GetKey())
		scheduleTraceContext.WithFields(tracing.WithMessageField(errMsg))
		return false, err
	}

	klog.V(4).InfoS("Pod can be scheduled directly without preemption",
		"switchType", switchType, "subCluster", subCluster,
		"podKey", podKey,
		"suggestedHost", scheduleResult.SuggestedHost,
		"nodeGroup", nodeGroup.GetKey())

	message := "Succeeded to schedule pod in node group: " + nodeGroup.GetKey()
	f.schedulerHooks.EventRecorder().Eventf(clonedPod, nil, v1.EventTypeWarning, "SchedulePodSuccessfully", core.ReturnAction, helper.TruncateMessage(message))
	scheduleTraceContext.WithFields(tracing.WithMessageField("Pod can be placed by scheduling"))

	// reserve the requested resource and other information on the target node in advance before the API call
	// so that we can do the update asynchronously
	// TODO: Compared to updating scheduler cache directly, we'd better operate the snapshot to avoid data corruption.
	NodeToPlace, err := f.schedulerHooks.ReservePod(ctx, clonedPod, scheduleResult)
	if err != nil {
		klog.ErrorS(err, "Failed to reserve pod", "switchType", switchType, "subCluster", subCluster, "podKey", podKey, "nodeGroup", nodeGroup.GetKey())
		errMessage := fmt.Sprintf("Fail to reserve pod in node group: %v, err: %v", nodeGroup.GetKey(), err.Error())
		f.schedulerHooks.EventRecorder().Eventf(clonedPod, nil, v1.EventTypeWarning,
			"FailToAssumePod", core.ReturnAction, helper.TruncateMessage(errMessage))
		metrics.PodScheduleError(podProperty, helper.SinceInSeconds(start))
		scheduleTraceContext.WithFields(tracing.WithMessageField(errMessage))
		return false, err
	}

	// ATTENTION: For pods that require CrossNodesConstraints and are successfully scheduled, the cache will be cleared.
	if fwk.HasCrossNodesConstraints(ctx, clonedPod) {
		statusByTemplate[runningUnitInfo.QueuedPodInfo.OwnerReferenceKey] = make(framework.NodeToStatusMap)
	}

	// make these scheduled pods ordered so that binder can make better decisions
	// this won't affect cache data, so we don't do this in reservePod function
	if markIndex {
		clonedPod.Annotations[podutil.UnitScheduledIndexAnnotationKey] = strconv.Itoa(scheduledIndex)
	}

	// update the running unit info
	runningUnitInfo.Victims = scheduleResult.Victims
	runningUnitInfo.NodeToPlace = NodeToPlace

	return true, nil
}

func (f *UnitFramework) preemptOneUnitInstance(ctx context.Context, scheduledIndex int, markIndex bool,
	runningUnitInfo *core.RunningUnitInfo, unitKey string, queuePriorityScore float64,
	unitCycleState, commonPreemptionState *framework.CycleState,
	nodeGroup framework.NodeGroup, nodeToStatus framework.NodeToStatusMap,
	cachedNominatedNodes *framework.CachedNominatedNodes,
) (success bool, err error) {
	switchType, subCluster := f.handle.SwitchType(), f.handle.SubCluster()
	godelScheduler := f.schedulerHooks.PodScheduler()

	klog.V(4).InfoS("Attempting to preempt for pod",
		"switchType", switchType, "subCluster", subCluster,
		"pod", klog.KObj(runningUnitInfo.ClonedPod),
		"podPriority", podutil.GetPodPriority(runningUnitInfo.ClonedPod),
		"podQueuePriorityScore", queuePriorityScore,
		"unitKey", unitKey, "nodeGroup", nodeGroup.GetKey())

	clonedPod := runningUnitInfo.ClonedPod
	podProperty := runningUnitInfo.QueuedPodInfo.GetPodProperty()
	// TODO: if the pod is in assumed state, skip scheduling
	if f.skipPodSchedule(clonedPod) {
		klog.InfoS("WARN: Skipped scheduling this pod", "switchType", switchType, "subCluster", subCluster, "unitKey", unitKey, "pod", klog.KObj(clonedPod))
		return false, fmt.Errorf("skip scheduling this pod, key:%v", podutil.GetPodKey(clonedPod))
	}

	start := time.Now()
	defer func() {
		// add schedule & preemption result metric
		if success {
			metrics.PodNominated(podProperty, helper.SinceInSeconds(start))
			metrics.PodScheduled(podProperty, helper.SinceInSeconds(start))
		} else {
			metrics.PodNominatedFailure(podProperty, helper.SinceInSeconds(start))
			metrics.PodUnschedulable(podProperty, helper.SinceInSeconds(start))
		}
	}()

	podTrace := runningUnitInfo.Trace
	preemptionTraceContext := podTrace.GetTraceContext(tracing.SchedulerPreemptPodSpan)

	podKey, fwk, pfwk, state, err := f.schedulerHooks.BootstrapSchedulePod(ctx, clonedPod, podTrace, nodeGroup.GetKey())
	if err != nil {
		errMessage := fmt.Sprintf("Failed to initialize pod: %v in node group: %v", err.Error(), nodeGroup.GetKey())
		f.schedulerHooks.EventRecorder().Eventf(clonedPod, nil, v1.EventTypeWarning, "FailToInitializePod", core.ReturnAction, helper.TruncateMessage(errMessage))
		klog.ErrorS(err, "Failed to initialize pod", "switchType", switchType, "subCluster", subCluster, "pod", klog.KObj(clonedPod), "nodeGroup", nodeGroup.GetKey())
		metrics.PodScheduleError(podProperty, helper.SinceInSeconds(start))
		preemptionTraceContext.WithFields(tracing.WithMessageField("Failed to initialize pod"), tracing.WithErrorField(err))
		return false, err
	}

	preemptionTraceContext.WithFields(tracing.WithPlugins(pfwk.ListPlugins())...)
	preemptionCycleCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	if cachedNominatedNodes != nil && cachedNominatedNodes.HasCrossNodeConstraints() == nil {
		hasCrossNodesConstraints := fwk.HasCrossNodesConstraints(preemptionCycleCtx, runningUnitInfo.ClonedPod)
		cachedNominatedNodes.SetHasCrossNodesConstraints(hasCrossNodesConstraints)
	}

	if runningUnitInfo.QueuedPodInfo != nil && runningUnitInfo.QueuedPodInfo.InitialPreemptAttemptTimestamp.IsZero() {
		runningUnitInfo.QueuedPodInfo.InitialPreemptAttemptTimestamp = start
	}

	preemptionResult, err := godelScheduler.PreemptInSpecificNodeGroup(preemptionCycleCtx, fwk, pfwk, unitCycleState, commonPreemptionState, state, clonedPod, nodeGroup, nodeToStatus, cachedNominatedNodes)
	if err != nil || preemptionResult.NominatedNode == nil {
		klog.ErrorS(err, "Failed to run preemption", "switchType", switchType, "subCluster", subCluster, "podKey", podKey, "nodeGroup", nodeGroup.GetKey())
		preemptionTraceContext.WithFields(tracing.WithReasonField(fmt.Sprintf("Failed to run preemption")), tracing.WithErrorField(err))
		errMessage := fmt.Sprintf("Fail to preempt for this pod in node group: %v, err: %v", nodeGroup.GetKey(), err.Error())
		f.schedulerHooks.EventRecorder().Eventf(clonedPod, nil, v1.EventTypeWarning, "FailToPreempt", core.ReturnAction, helper.TruncateMessage(errMessage))
		return false, err
	}

	klog.V(4).InfoS("Pod can be placed by evicting some other pods",
		"switchType", switchType, "subCluster", subCluster,
		"podKey", podKey,
		"nominatedNode", preemptionResult.NominatedNode.NodeName,
		"victims", preemptionResult.NominatedNode.VictimPods,
		"nodeGroup", nodeGroup.GetKey())

	message := fmt.Sprintf("Pod can be placed by evicting some other pods, nominated node: %v, victims: %+v, in node group: %v", preemptionResult.NominatedNode.NodeName, preemptionResult.NominatedNode.VictimPods, nodeGroup.GetKey())
	preemptionTraceContext.WithFields(tracing.WithMessageField(message))
	f.schedulerHooks.EventRecorder().Eventf(clonedPod, nil, v1.EventTypeNormal, "PreemptForPodSuccessfully", core.ContinueAction,
		helper.TruncateMessage(message))

	// reserve the requested resource and other information on the target node in advance before the API call
	// so that we can do the update asynchronously
	// TODO: Compared to updating scheduler cache directly, we'd better operate the snapshot to avoid data corruption.
	NodeToPlace, err := f.schedulerHooks.ReservePod(ctx, clonedPod, preemptionResult)
	if err != nil {
		klog.ErrorS(err, "Failed to reserve pod", "switchType", switchType, "subCluster", subCluster,
			"unitKey", unitKey, "podKey", podKey, "nodeGroup", nodeGroup.GetKey())
		errMessage := fmt.Sprintf("Fail to reserve pod in node group: %v, err: %v", nodeGroup.GetKey(), err.Error())
		f.schedulerHooks.EventRecorder().Eventf(clonedPod, nil, v1.EventTypeWarning,
			"FailToAssumePod", core.ReturnAction, helper.TruncateMessage(errMessage))
		metrics.PodScheduleError(podProperty, helper.SinceInSeconds(start))
		preemptionTraceContext.WithFields(tracing.WithReasonField("Failed to reserve pod"), tracing.WithErrorField(err))
		return false, err
	}

	// TODO: if any of the following operations fails, we need to un-reserve the cloned pod and return false

	// make these scheduled pods ordered so that binder can make better decisions
	// this won't affect cache data, so we don't do this in reservePod function
	if markIndex {
		clonedPod.Annotations[podutil.UnitScheduledIndexAnnotationKey] = strconv.Itoa(scheduledIndex)
	}

	// update the running unit info
	runningUnitInfo.Victims = preemptionResult.Victims
	runningUnitInfo.NodeToPlace = NodeToPlace

	return true, nil
}

// skipPodSchedule returns true if we could skip scheduling the pod for specified cases.
// TODO: skip assumed or nominated state pods
func (f *UnitFramework) skipPodSchedule(pod *v1.Pod) bool {
	// Case 1: pod is being deleted.
	if pod.DeletionTimestamp != nil {
		f.schedulerHooks.EventRecorder().Eventf(pod, nil, v1.EventTypeWarning, "FailedScheduling", core.SchedulingAction, "skip schedule pod which is being deleted: %v/%v", pod.Namespace, pod.Name)
		klog.V(4).InfoS("Skipped scheduling pod which is being deleted", "switchType", f.handle.SwitchType(), "subCluster", f.handle.SubCluster(), "pod", klog.KObj(pod))
		return true
	}

	// Case 2: pod has been cached.
	// An cached pod can be added again to the scheduling queue if it got an update event
	// during its previous scheduling cycle.
	isCached, err := f.handle.IsCachedPod(pod)
	if err != nil {
		utilruntime.HandleError(fmt.Errorf("failed to check whether pod %s/%s is assumed: %v", pod.Namespace, pod.Name, err))
		return false
	}
	if isCached {
		klog.V(3).InfoS("Skipping cached pod scheduling", "pod", klog.KObj(pod))
	}

	return isCached
}

func errorStr(err error) string {
	if err != nil {
		return err.Error()
	}
	return ""
}
