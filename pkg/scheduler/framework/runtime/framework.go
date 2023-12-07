/*
Copyright 2019 The Kubernetes Authors.

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

package runtime

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"time"

	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/klog/v2"

	framework "github.com/kubewharf/godel-scheduler/pkg/framework/api"
	"github.com/kubewharf/godel-scheduler/pkg/scheduler/apis/config"
	"github.com/kubewharf/godel-scheduler/pkg/scheduler/metrics"
	"github.com/kubewharf/godel-scheduler/pkg/util"
	"github.com/kubewharf/godel-scheduler/pkg/util/helper"
	"github.com/kubewharf/godel-scheduler/pkg/util/parallelize"
	podutil "github.com/kubewharf/godel-scheduler/pkg/util/pod"
)

type PluginsConstraint struct {
	filterConstraint map[string]interface{}
	scoreConstraint  map[string]interface{}
}

// GodelSchedulerFramework is the component responsible for initializing and running scheduler
// plugins, determining plugins to run in each scheduling phase(or extension point)
type GodelSchedulerFramework struct {
	preFilterPlugins  []framework.PreFilterPlugin
	filterPlugins     []framework.FilterPlugin
	preScorePlugins   []framework.PreScorePlugin
	scorePlugins      []framework.ScorePlugin
	crossNodesPlugins []framework.CrossNodesPlugin

	// weight config for each score plugin
	scoreWeightMap map[string]int64

	preFilterPluginsSet  map[string]int
	filterPluginsSet     map[string]int
	preScorePluginsSet   map[string]int
	scorePluginsSet      map[string]int
	crossNodesPluginsSet map[string]int

	podResourceType podutil.PodResourceType
	podLauncher     podutil.PodLauncher
	metricsRecorder *MetricsRecorder

	potentialVictimsInNodes *framework.PotentialVictimsInNodes
}

func (f *GodelSchedulerFramework) runPreFilterPlugin(ctx context.Context, pl framework.PreFilterPlugin, state *framework.CycleState, pod *v1.Pod) *framework.Status {
	if !state.ShouldRecordPluginMetrics() {
		return pl.PreFilter(ctx, state, pod)
	}
	startTime := time.Now()
	status := pl.PreFilter(ctx, state, pod)
	nodeGroupKey, err := framework.GetNodeGroupKey(state)
	if err != nil {
		klog.InfoS("Node group key should not be empty in state", "err", err, "pluginName", pl.Name(), "pod", klog.KObj(pod))
	}

	podProperty, _ := framework.GetPodProperty(state)
	f.metricsRecorder.observePluginDurationAsync(podProperty, metrics.PreFilterEvaluation, pl.Name(), status, nodeGroupKey, helper.SinceInSeconds(startTime))
	return status
}

func (f *GodelSchedulerFramework) RunPreFilterPlugins(ctx context.Context, state *framework.CycleState, pod *v1.Pod) *framework.Status {
	var finalStatus *framework.Status
	debugMode := util.GetPodDebugMode(pod)

	for _, pl := range f.preFilterPlugins {
		status := f.runPreFilterPlugin(ctx, pl, state, pod)
		if !status.IsSuccess() {
			// Haven't return even if not in debug mode
			if finalStatus == nil {
				if status.IsUnschedulable() {
					finalStatus = status
				} else {
					msg := fmt.Sprintf("Failed to run PreFilter plugin %q for pod %q: %v", pl.Name(), pod.Name, status.Message())
					klog.ErrorS(nil, "Failed to run PreFilter plugin", "pluginName", pl.Name(), "pod", klog.KObj(pod), "statusMessage", status.Message())
					finalStatus = framework.NewStatus(framework.Error, msg)
				}
			}

			if debugMode != util.DebugModeOn {
				if finalStatus != nil {
					return finalStatus
				}
			} else {
				// Print Debug Plugin Error Message
				klog.ErrorS(nil, "DEBUG: Failed to run PreFilter plugin", "pluginName", pl.Name(), "pod", klog.KObj(pod), "statusMessage", status.Message())
			}
		}
	}

	return finalStatus
}

// RunPreFilterExtensionAddPod calls the AddPod interface for the set of configured
// PreFilter plugins. It returns directly if any of the plugins return any
// status other than Success.
func (f *GodelSchedulerFramework) RunPreFilterExtensionAddPod(
	ctx context.Context,
	state *framework.CycleState,
	podToSchedule *v1.Pod,
	podToAdd *v1.Pod,
	nodeInfo framework.NodeInfo,
) (status *framework.Status) {
	for _, pl := range f.preFilterPlugins {
		if pl.PreFilterExtensions() == nil {
			continue
		}
		status = f.runPreFilterExtensionAddPod(ctx, pl, state, podToSchedule, podToAdd, nodeInfo)
		if !status.IsSuccess() {
			msg := fmt.Sprintf("Failed to run AddPod of PreFilterExtensions plugin %q while scheduling pod %q: %v",
				pl.Name(), podToSchedule.Name, status.Message())
			klog.ErrorS(nil, "Failed to run AddPod of PreFilterExtensions",
				"pluginName", pl.Name(), "pod", klog.KObj(podToSchedule), "statusMessage", status.Message())
			return framework.NewStatus(framework.Error, msg)
		}
	}

	return nil
}

func (f *GodelSchedulerFramework) runPreFilterExtensionAddPod(ctx context.Context, pl framework.PreFilterPlugin, state *framework.CycleState, podToSchedule *v1.Pod, podToAdd *v1.Pod, nodeInfo framework.NodeInfo) *framework.Status {
	if !state.ShouldRecordPluginMetrics() {
		return pl.PreFilterExtensions().AddPod(ctx, state, podToSchedule, podToAdd, nodeInfo)
	}
	startTime := time.Now()
	status := pl.PreFilterExtensions().AddPod(ctx, state, podToSchedule, podToAdd, nodeInfo)
	nodeGroupKey, err := framework.GetNodeGroupKey(state)
	if err != nil {
		klog.InfoS("Node group key should not be empty in state", "err", err, "pluginName", pl.Name(), "pod", klog.KObj(podToSchedule))
	}

	podProperty, _ := framework.GetPodProperty(state)
	f.metricsRecorder.observePluginDurationAsync(podProperty, metrics.PreFilterAddPodEvaluation, pl.Name(), status, nodeGroupKey, helper.SinceInSeconds(startTime))
	return status
}

// RunPreFilterExtensionRemovePod calls the RemovePod interface for the set of configured
// PreFilter plugins. It returns directly if any of the plugins return any
// status other than Success.
func (f *GodelSchedulerFramework) RunPreFilterExtensionRemovePod(
	ctx context.Context,
	state *framework.CycleState,
	podToSchedule *v1.Pod,
	podToRemove *v1.Pod,
	nodeInfo framework.NodeInfo,
) (status *framework.Status) {
	for _, pl := range f.preFilterPlugins {
		if pl.PreFilterExtensions() == nil {
			continue
		}
		status = f.runPreFilterExtensionRemovePod(ctx, pl, state, podToSchedule, podToRemove, nodeInfo)
		if !status.IsSuccess() {
			msg := fmt.Sprintf("Failed to run RemovePod of PreFilterExtensions plugin %q while scheduling pod %q: %v",
				pl.Name(), podToSchedule.Name, status.Message())
			klog.ErrorS(nil, "Failed to run RemovePod of PreFilterExtensions",
				"pluginName", pl.Name(), "pod", klog.KObj(podToSchedule), "statusMessage", status.Message())
			return framework.NewStatus(framework.Error, msg)
		}
	}

	return nil
}

func (f *GodelSchedulerFramework) runPreFilterExtensionRemovePod(ctx context.Context, pl framework.PreFilterPlugin, state *framework.CycleState, podToSchedule *v1.Pod, podToAdd *v1.Pod, nodeInfo framework.NodeInfo) *framework.Status {
	if !state.ShouldRecordPluginMetrics() {
		return pl.PreFilterExtensions().RemovePod(ctx, state, podToSchedule, podToAdd, nodeInfo)
	}
	startTime := time.Now()
	status := pl.PreFilterExtensions().RemovePod(ctx, state, podToSchedule, podToAdd, nodeInfo)
	nodeGroupKey, err := framework.GetNodeGroupKey(state)
	if err != nil {
		klog.InfoS("Node group key should not be empty in state", "err", err, "pluginName", pl.Name(), "pod", klog.KObj(podToSchedule))
	}

	podProperty, _ := framework.GetPodProperty(state)
	f.metricsRecorder.observePluginDurationAsync(podProperty, metrics.PreFilterRemovePodEvaluation, pl.Name(), status, nodeGroupKey, helper.SinceInSeconds(startTime))
	return status
}

func (f *GodelSchedulerFramework) runFilterPlugin(ctx context.Context, pl framework.FilterPlugin, state *framework.CycleState, pod *v1.Pod, nodeInfo framework.NodeInfo) *framework.Status {
	if !state.ShouldRecordPluginMetrics() {
		return pl.Filter(ctx, state, pod, nodeInfo)
	}
	startTime := time.Now()
	status := pl.Filter(ctx, state, pod, nodeInfo)
	nodeGroupKey, err := framework.GetNodeGroupKey(state)
	if err != nil {
		klog.InfoS("Node group key should not be empty in state", "err", err, "pluginName", pl.Name(), "pod", klog.KObj(pod))
	}

	podProperty, _ := framework.GetPodProperty(state)
	f.metricsRecorder.observePluginDurationAsync(podProperty, metrics.FilterEvaluation, pl.Name(), status, nodeGroupKey, helper.SinceInSeconds(startTime))
	return status
}

func (f *GodelSchedulerFramework) RunFilterPlugins(ctx context.Context, state *framework.CycleState, pod *v1.Pod, nodeInfo framework.NodeInfo, skipPlugins ...string) framework.PluginToStatus {
	finalStatus := make(framework.PluginToStatus)
	debugMode := util.GetPodDebugMode(pod)
	statuses := make(framework.PluginToStatus)

	// Create debug mode label selector map
	var labelSelectorMap map[string]string
	json.Unmarshal([]byte(util.GetPodWatchLabelValueString(pod)), &labelSelectorMap)

	skipPluginsSet := sets.NewString(skipPlugins...)
	for _, pl := range f.filterPlugins {
		if skipPluginsSet.Has(pl.Name()) {
			continue
		}
		pluginStatus := f.runFilterPlugin(ctx, pl, state, pod, nodeInfo)
		// Stop the filter process immediately when any filter fails as filters are hard constraints
		if !pluginStatus.IsSuccess() {
			// Only print the error messages of the nodes with either of the specified labels
			fitLabelFlag := false
			if debugMode == util.DebugModeOn {
				podLauncher, err := podutil.GetPodLauncher(pod)
				if err != nil {
					klog.ErrorS(err, "DEBUG: Failed to get pod launcher type after Filter plugin was failed",
						"pluginName", pl.Name(), "pod", klog.KObj(pod), "statusMessage", pluginStatus.Message())
				}

				switch podLauncher {
				case podutil.Kubelet:
					if nodeInfo.GetNode() != nil {
						fitLabelFlag = util.CheckIfNodeLabelsInSpecifiedLabels(nodeInfo.GetNode().Labels, labelSelectorMap)
					}
				case podutil.NodeManager:
					if nodeInfo.GetNMNode() != nil {
						fitLabelFlag = util.CheckIfNodeLabelsInSpecifiedLabels(nodeInfo.GetNMNode().Labels, labelSelectorMap)
					}
				}
			}

			// Filter plugins are not supposed to return any status other than
			// Success or Unschedulable.
			if !pluginStatus.IsUnschedulable() {
				// No plugin has been unsuccessful and schedulable yet
				if len(finalStatus) == 0 {
					errStatus := framework.NewStatus(framework.Error, fmt.Sprintf("failed to run %q filter plugin for pod %q: %v", pl.Name(), pod.Name, pluginStatus.Message()))
					finalStatus = map[string]*framework.Status{pl.Name(): errStatus}
				}
			} else {
				statuses[pl.Name()] = pluginStatus
			}

			if !fitLabelFlag {
				if len(finalStatus) > 0 {
					return finalStatus
				} else {
					return statuses
				}
			} else {
				klog.ErrorS(nil, "DEBUG: Failed to run Filter plugin",
					"pluginName", pl.Name(), "pod", klog.KObj(pod), "statusMessage", pluginStatus.Message())
			}
		} else {
			statuses[pl.Name()] = pluginStatus
		}
	}

	// There exist a plugin that is unsuccessful and schedulable
	if len(finalStatus) != 0 {
		return finalStatus
	}

	return statuses
}

func (f *GodelSchedulerFramework) runPreScorePlugin(ctx context.Context, pl framework.PreScorePlugin, state *framework.CycleState, pod *v1.Pod, nodes []framework.NodeInfo) *framework.Status {
	if !state.ShouldRecordPluginMetrics() {
		return pl.PreScore(ctx, state, pod, nodes)
	}
	startTime := time.Now()
	status := pl.PreScore(ctx, state, pod, nodes)
	nodeGroupKey, err := framework.GetNodeGroupKey(state)
	if err != nil {
		klog.InfoS("Node group key should not be empty in state", "err", err, "pluginName", pl.Name(), "pod", klog.KObj(pod))
	}

	podProperty, _ := framework.GetPodProperty(state)
	f.metricsRecorder.observePluginDurationAsync(podProperty, metrics.PreScoreEvaluation, pl.Name(), status, nodeGroupKey, helper.SinceInSeconds(startTime))
	return status
}

func (f *GodelSchedulerFramework) RunPreScorePlugins(ctx context.Context, state *framework.CycleState, pod *v1.Pod, nodes []framework.NodeInfo) *framework.Status {
	for _, pl := range f.preScorePlugins {
		status := f.runPreScorePlugin(ctx, pl, state, pod, nodes)
		if !status.IsSuccess() {
			msg := fmt.Sprintf("Failed to run PreScore plugin %q for pod %q: %v", pl.Name(), pod.Name, status.Message())
			klog.ErrorS(nil, "Failed to run PreScore plugin", "pluginName", pl.Name(), "pod", klog.KObj(pod), "statusMessage", status.Message())
			return framework.NewStatus(framework.Error, msg)
		}
	}
	return nil
}

func (f *GodelSchedulerFramework) runScorePlugin(ctx context.Context, pl framework.ScorePlugin, state *framework.CycleState, pod *v1.Pod, nodeName string) (int64, *framework.Status) {
	if !state.ShouldRecordPluginMetrics() {
		return pl.Score(ctx, state, pod, nodeName)
	}
	startTime := time.Now()
	s, status := pl.Score(ctx, state, pod, nodeName)
	nodeGroupKey, err := framework.GetNodeGroupKey(state)
	if err != nil {
		klog.InfoS("Node group key should not be empty in state", "err", err, "pluginName", pl.Name(), "pod", klog.KObj(pod))
	}

	podProperty, _ := framework.GetPodProperty(state)
	f.metricsRecorder.observePluginDurationAsync(podProperty, metrics.ScoreEvaluation, pl.Name(), status, nodeGroupKey, helper.SinceInSeconds(startTime))
	return s, status
}

func (f *GodelSchedulerFramework) RunScorePlugins(ctx context.Context, state *framework.CycleState, pod *v1.Pod, nodeNames []string) (framework.PluginToNodeScores, *framework.Status) {
	pluginToNodeScores := make(framework.PluginToNodeScores, len(f.scorePlugins))
	nodeSize := len(nodeNames)
	for _, pl := range f.scorePlugins {
		pluginToNodeScores[pl.Name()] = make(framework.NodeScoreList, nodeSize)
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	errCh := parallelize.NewErrorChannel()

	// Run Score method for each node in parallel.
	parallelize.Until(ctx, nodeSize, func(index int) {
		for _, pl := range f.scorePlugins {
			nodeName := nodeNames[index]
			if nodeName == "" {
				klog.InfoS("WARN: Skipped running Score plugin since nodeName is empty", "nodeIndex", index)
				continue
			}
			s, status := f.runScorePlugin(ctx, pl, state, pod, nodeName)
			if !status.IsSuccess() {
				errCh.SendErrorWithCancel(fmt.Errorf("plugin %v failed to run, status is: %v", pl.Name(), status.Message()), cancel)
				return
			}
			pluginToNodeScores[pl.Name()][index] = framework.NodeScore{
				Name:  nodeName,
				Score: s,
			}
		}
	})
	if err := errCh.ReceiveError(); err != nil {
		msg := fmt.Sprintf("Failed to run Score plugin for pod %q: %v", pod.Name, err)
		klog.ErrorS(err, "Failed to run Score plugin", "pod", klog.KObj(pod))
		return nil, framework.NewStatus(framework.Error, msg)
	}

	// Run NormalizeScore method for each ScorePlugin in parallel.
	parallelize.Until(ctx, len(f.scorePlugins), func(index int) {
		pl := f.scorePlugins[index]
		nodeScoreList := pluginToNodeScores[pl.Name()]
		if pl.ScoreExtensions() == nil {
			return
		}
		status := f.runScoreExtension(ctx, pl, state, pod, nodeScoreList)
		if !status.IsSuccess() {
			err := fmt.Errorf("normalize score plugin %q failed with error %v", pl.Name(), status.Message())
			errCh.SendErrorWithCancel(err, cancel)
			return
		}
	})
	if err := errCh.ReceiveError(); err != nil {
		msg := fmt.Sprintf("Failed to run ScoreExtension plugin for pod %q: %v", pod.Name, err)
		klog.ErrorS(err, "Failed to run ScoreExtension plugin", "pod", klog.KObj(pod))
		return nil, framework.NewStatus(framework.Error, msg)
	}

	// Apply score defaultWeights for each ScorePlugin in parallel.
	parallelize.Until(ctx, len(f.scorePlugins), func(index int) {
		pl := f.scorePlugins[index]
		// Score plugins' weight has been checked when they are initialized.
		weight := f.scoreWeightMap[pl.Name()]
		nodeScoreList := pluginToNodeScores[pl.Name()]

		for i, nodeScore := range nodeScoreList {
			// return error if score plugin returns invalid score.
			if nodeScore.Score > framework.MaxNodeScore || nodeScore.Score < framework.MinNodeScore {
				err := fmt.Errorf("score plugin %q returns an invalid score %v, it should in the range of [%v, %v] after normalizing", pl.Name(), nodeScore.Score, framework.MinNodeScore, framework.MaxNodeScore)
				errCh.SendErrorWithCancel(err, cancel)
				return
			}
			nodeScoreList[i].Score = nodeScore.Score * weight
		}
	})

	if err := errCh.ReceiveError(); err != nil {
		msg := fmt.Sprintf("Failed to apply score defaultWeights after running ScoreExtension plugin for pod %q: %v", pod.Name, err)
		klog.ErrorS(err, "Failed to apply score defaultWeights after running ScoreExtension plugin", "pod", klog.KObj(pod))
		return nil, framework.NewStatus(framework.Error, msg)
	}

	return pluginToNodeScores, nil
}

func (f *GodelSchedulerFramework) runScoreExtension(ctx context.Context, pl framework.ScorePlugin, state *framework.CycleState, pod *v1.Pod, nodeScoreList framework.NodeScoreList) *framework.Status {
	if !state.ShouldRecordPluginMetrics() {
		return pl.ScoreExtensions().NormalizeScore(ctx, state, pod, nodeScoreList)
	}
	startTime := time.Now()
	status := pl.ScoreExtensions().NormalizeScore(ctx, state, pod, nodeScoreList)
	nodeGroupKey, err := framework.GetNodeGroupKey(state)
	if err != nil {
		klog.InfoS("Node group key should not be empty in state", "err", err, "pluginName", pl.Name(), "pod", klog.KObj(pod))
	}

	podProperty, _ := framework.GetPodProperty(state)
	f.metricsRecorder.observePluginDurationAsync(podProperty, metrics.ScoreNormalizeEvaluation, pl.Name(), status, nodeGroupKey, helper.SinceInSeconds(startTime))
	return status
}

func (f *GodelSchedulerFramework) HasFilterPlugins() bool {
	return len(f.filterPlugins) > 0
}

func (f *GodelSchedulerFramework) HasScorePlugins() bool {
	return len(f.scorePlugins) > 0
}

func (f *GodelSchedulerFramework) ListPlugins() map[string]sets.String {
	m := map[string]sets.String{
		framework.PreFilterPhase:         sets.NewString(),
		framework.FilterPhase:            sets.NewString(),
		framework.PreScorePhase:          sets.NewString(),
		framework.ScorePhase:             sets.NewString(),
		framework.CrossNodesCheckerPhase: sets.NewString(),
	}

	for _, plugin := range f.preFilterPlugins {
		m[framework.PreFilterPhase].Insert(plugin.Name())
	}
	for _, plugin := range f.filterPlugins {
		m[framework.FilterPhase].Insert(plugin.Name())
	}
	for _, plugin := range f.preScorePlugins {
		m[framework.PreScorePhase].Insert(plugin.Name())
	}
	for _, plugin := range f.scorePlugins {
		m[framework.ScorePhase].Insert(plugin.Name())
	}
	for _, plugin := range f.crossNodesPlugins {
		m[framework.CrossNodesCheckerPhase].Insert(plugin.Name())
	}

	return m
}

func (f *GodelSchedulerFramework) InitCycleState(pod *v1.Pod) (*framework.CycleState, error) {
	state := framework.NewCycleState()
	podResourceType, err := podutil.GetPodResourceType(pod)
	if err != nil {
		return nil, err
	}
	if err = framework.SetPodResourceTypeState(podResourceType, state); err != nil {
		return nil, err
	}

	return state, nil
}

// PodPassesFiltersOnNode checks whether a node given by NodeInfo satisfies the
// filter plugins.
// This function is called from two different places: Schedule and Preempt.
// When it is called from Schedule, we want to test whether the pod is
// schedulable on the node with all the existing pods on the node plus higher
// and equal priority pods nominated to run on the node.
// When it is called from Preempt, we should remove the victims of preemption
// and add the nominated pods. Removal of the victims is done by
// SelectVictimsOnNode(). Preempt removes victims from PreFilter state and
// NodeInfo before calling this function.
func PodPassesFiltersOnNode(
	ctx context.Context,
	fw framework.SchedulerFramework,
	state *framework.CycleState,
	pod *v1.Pod,
	info framework.NodeInfo,
	skipPlugins ...string,
) (bool, *framework.Status, framework.PluginToStatus, error) {
	// In Godel Scheduler, preemptor pods are add to cache as reserve resource,
	// so there are no needs to do double-check here.
	// pod will always check filter with node where all pods in cache are counted
	statusMap := fw.RunFilterPlugins(ctx, state, pod, info, skipPlugins...)
	status := statusMap.Merge()
	if !status.IsSuccess() && !status.IsUnschedulable() {
		return false, status, statusMap, status.AsError()
	}
	return status.IsSuccess(), status, statusMap, nil
}

// New creates a new GodelSchedulerFramework, where pluginRegistry marks which plugins are supported, basePlugins presents which plugins are enabled by default.
// podConstraintConfigs are used in pod annotation, where hard constraint will be taken as filter plugins and soft constraint will be taken as score plugins.
// If plugin in podConstraintConfigs not exists in basePlugins, add this plugin to the new Godel Framework.
func NewPodFramework(pluginRegistry framework.PluginMap, pluginOrder framework.PluginOrder,
	basePlugins, hardConstraints, softConstraints *framework.PluginCollection,
	metricsRecorder *MetricsRecorder,
) (*GodelSchedulerFramework, error) {
	f := &GodelSchedulerFramework{
		scoreWeightMap:          map[string]int64{},
		preFilterPlugins:        make([]framework.PreFilterPlugin, 0),
		filterPlugins:           make([]framework.FilterPlugin, 0),
		preScorePlugins:         make([]framework.PreScorePlugin, 0),
		scorePlugins:            make([]framework.ScorePlugin, 0),
		crossNodesPlugins:       make([]framework.CrossNodesPlugin, 0),
		preFilterPluginsSet:     make(map[string]int),
		filterPluginsSet:        make(map[string]int),
		preScorePluginsSet:      make(map[string]int),
		scorePluginsSet:         make(map[string]int),
		crossNodesPluginsSet:    make(map[string]int),
		metricsRecorder:         metricsRecorder,
		potentialVictimsInNodes: framework.NewPotentialVictimsInNodes(),
	}

	totalScore := framework.MaxTotalScore

	if basePlugins != nil {
		// prepare filter plugins
		for _, pluginSpec := range basePlugins.Filters {
			f.addFilterPlugin(pluginSpec, pluginRegistry)
		}
		// prepare score plugins
		for _, pluginSpec := range basePlugins.Scores {
			if pluginSpec.GetWeight() > config.MaxWeight {
				return nil, fmt.Errorf("weight for score plugin %v is overflow: %v", pluginSpec.GetName(), pluginSpec.GetWeight())
			}
			pluginMaxScore := pluginSpec.GetWeight() * framework.MaxNodeScore
			if pluginMaxScore >= totalScore {
				return nil, fmt.Errorf("total score of all score plugins could overflow")
			}
			f.addScorePlugin(pluginSpec, pluginRegistry)
			totalScore -= pluginMaxScore
		}
	}

	// should check plugins enabled before generate framework
	for _, pluginSpec := range hardConstraints.Filters {
		f.addFilterPlugin(pluginSpec, pluginRegistry)
	}
	for _, pluginSpec := range softConstraints.Scores {
		if pluginSpec.GetWeight() > config.MaxWeight {
			return nil, fmt.Errorf("weight for score plugin %v is overflow: %v", pluginSpec.GetName(), pluginSpec.GetWeight())
		}
		pluginMaxScore := pluginSpec.GetWeight() * framework.MaxNodeScore
		if pluginMaxScore >= totalScore {
			return nil, fmt.Errorf("total score of all score plugins could overflow")
		}
		f.addScorePlugin(pluginSpec, pluginRegistry)
		totalScore -= pluginMaxScore
	}

	f.orderFilterPlugins(pluginOrder)

	return f, nil
}

// orderFilterPlugins orders the filter plugins by the specified order.
func (f *GodelSchedulerFramework) orderFilterPlugins(pluginOrder framework.PluginOrder) {
	if pluginOrder == nil {
		klog.InfoS("WARN: PluginOrder was nil")
		return
	}

	sort.SliceStable(f.filterPlugins, func(i, j int) bool {
		// if the index of plugin is not in pluginOrder, use infinity as the index
		// so that the plugin will be put at the end of the slice
		iIndex, exist := pluginOrder[f.filterPlugins[i].Name()]
		if !exist {
			klog.InfoS("WARN: Plugin was not found in the PluginOrder map", "pluginName", f.filterPlugins[i].Name())
			iIndex = math.MaxInt32
		}
		jIndex, exist := pluginOrder[f.filterPlugins[j].Name()]
		if !exist {
			klog.InfoS("WARN: Plugin was not found in the PluginOrder map", "pluginName", f.filterPlugins[j].Name())
			jIndex = math.MaxInt32
		}

		return iIndex < jIndex
	})
}

func (f *GodelSchedulerFramework) addFilterPlugin(pluginSpec *framework.PluginSpec, pluginRegistry framework.PluginMap) {
	plgName := pluginSpec.GetName()
	if _, ok := pluginRegistry[plgName]; !ok {
		klog.InfoS("WARN: Some Filter plugin was not supported", "pluginName", plgName)
		return
	}

	if index, ok := f.filterPluginsSet[plgName]; !ok {
		if pl, ok := pluginRegistry[plgName].(framework.FilterPlugin); ok {
			f.filterPluginsSet[plgName] = len(f.filterPlugins)
			f.filterPlugins = append(f.filterPlugins, pl.(framework.FilterPlugin))
		} else {
			klog.InfoS("WARN: The Filter plugin did not implement the expected interface", "pluginName", plgName)
		}
	} else {
		klog.V(5).InfoS("Filter plugin already registered and would be overridden", "pluginName", plgName)
		if pl, ok := pluginRegistry[plgName].(framework.FilterPlugin); ok {
			f.filterPlugins[index] = pl.(framework.FilterPlugin)
		} else {
			klog.InfoS("WARN: The Filter plugin did not exist as expected", "pluginName", plgName)
		}
	}

	if index, ok := f.preFilterPluginsSet[plgName]; !ok {
		if pl, ok := pluginRegistry[plgName].(framework.PreFilterPlugin); ok {
			f.preFilterPluginsSet[plgName] = len(f.preFilterPlugins)
			f.preFilterPlugins = append(f.preFilterPlugins, pl)
		}
	} else {
		klog.V(5).InfoS("PreFilter plugin already registered and would be overridden", "pluginName", plgName)
		if pl, ok := pluginRegistry[plgName].(framework.PreFilterPlugin); ok {
			f.preFilterPlugins[index] = pl
		}
	}

	if index, ok := f.crossNodesPluginsSet[plgName]; !ok {
		if pl, ok := pluginRegistry[plgName].(framework.CrossNodesPlugin); ok {
			f.crossNodesPluginsSet[plgName] = len(f.crossNodesPlugins)
			f.crossNodesPlugins = append(f.crossNodesPlugins, pl)
		}
	} else {
		klog.V(5).InfoS("CrossNodes plugin already registered and would be overridden", "pluginName", plgName)
		if pl, ok := pluginRegistry[plgName].(framework.CrossNodesPlugin); ok {
			f.crossNodesPlugins[index] = pl
		}
	}
}

func (f *GodelSchedulerFramework) addScorePlugin(pluginSpec *framework.PluginSpec, pluginRegistry framework.PluginMap) {
	plgName := pluginSpec.GetName()
	if _, ok := pluginRegistry[plgName]; !ok {
		klog.InfoS("WARN: The Score plugin was not supported", "pluginName", plgName)
		return
	}

	if index, ok := f.scorePluginsSet[plgName]; !ok {
		if pl, ok := pluginRegistry[plgName].(framework.ScorePlugin); ok {
			f.scorePluginsSet[plgName] = len(f.scorePlugins)
			f.scorePlugins = append(f.scorePlugins, pl)
			f.scoreWeightMap[plgName] = pluginSpec.GetWeight()
		} else {
			klog.InfoS("WARN: The Score plugin did not implement the expected interface", "pluginName", plgName)
		}
	} else {
		klog.V(5).InfoS("Score plugin already registered and would be overridden", "pluginName", plgName)
		if pl, ok := pluginRegistry[plgName].(framework.ScorePlugin); ok {
			f.scorePlugins[index] = pl
			f.scoreWeightMap[plgName] = pluginSpec.GetWeight()
		} else {
			klog.InfoS("WARN: The Score plugin did not exist as expected", "pluginName", plgName)
		}
	}

	if index, ok := f.preScorePluginsSet[plgName]; !ok {
		if pl, ok := pluginRegistry[plgName].(framework.PreScorePlugin); ok {
			f.preScorePluginsSet[plgName] = len(f.preScorePlugins)
			f.preScorePlugins = append(f.preScorePlugins, pl)
		}
	} else {
		klog.V(5).InfoS("PreScore plugin already registered and would be overridden", "pluginName", plgName)
		if pl, ok := pluginRegistry[plgName].(framework.PreScorePlugin); ok {
			f.preScorePlugins[index] = pl
		}
	}
}

func (f *GodelSchedulerFramework) SetPotentialVictims(node string, potentialVictims []string) {
	f.potentialVictimsInNodes.SetPotentialVictims(node, potentialVictims)
}

func (f *GodelSchedulerFramework) GetPotentialVictims(node string) []string {
	return f.potentialVictimsInNodes.GetPotentialVictims(node)
}

func (f *GodelSchedulerFramework) HasCrossNodesConstraints(ctx context.Context, pod *v1.Pod) bool {
	for _, pl := range f.crossNodesPlugins {
		if pl.HasCrossNodesConstraints(ctx, pod) {
			return true
		}
	}
	return false
}
