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

package runtime

import (
	"context"
	"fmt"
	"time"

	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/klog/v2"

	"github.com/kubewharf/godel-scheduler/pkg/binder/apis"
	"github.com/kubewharf/godel-scheduler/pkg/binder/metrics"
	framework "github.com/kubewharf/godel-scheduler/pkg/framework/api"
	podutil "github.com/kubewharf/godel-scheduler/pkg/util/pod"
)

const (
	// Specifies the maximum timeout a permit plugin can return.
	maxTimeout = 15 * time.Minute
)

// GodelFramework is the component responsible for initializing and running scheduler
// plugins, determining plugins to run in each scheduling phase(or extension point)
type GodelFramework struct {
	checkConflictsPlugins       []framework.CheckConflictsPlugin
	checkTopologyPlugins        []framework.CheckTopologyPlugin
	reservePlugins              []framework.ReservePlugin
	permitPlugins               []framework.PermitPlugin
	preBindPlugins              []framework.PreBindPlugin
	bindPlugins                 []framework.BindPlugin
	postBindPlugins             []framework.PostBindPlugin
	clusterPrePreemptingPlugins []framework.ClusterPrePreemptingPlugin
	victimCheckingPlugins       []*framework.VictimCheckingPluginCollection
	postVictimCheckingPlugins   []framework.PostVictimCheckingPlugin
}

func (f *GodelFramework) runCheckConflictsPlugin(ctx context.Context, pl framework.CheckConflictsPlugin, state *framework.CycleState, pod *v1.Pod, nodeInfo framework.NodeInfo) *framework.Status {
	if !state.ShouldRecordPluginMetrics() {
		return pl.CheckConflicts(ctx, state, pod, nodeInfo)
	}
	startTime := time.Now()
	status := pl.CheckConflicts(ctx, state, pod, nodeInfo)

	podProperty, _ := framework.GetPodProperty(state)
	metrics.ObserveBindingStageDuration(podProperty, metrics.CheckConflictsEvaluation, pl.Name(), status.Code().String(), metrics.SinceInSeconds(startTime))
	return status
}

func (f *GodelFramework) RunCheckConflictsPlugins(ctx context.Context, state *framework.CycleState, pod *v1.Pod, nodeInfo framework.NodeInfo) framework.PluginToStatus {
	statuses := make(framework.PluginToStatus)
	for _, pl := range f.checkConflictsPlugins {
		pluginStatus := f.runCheckConflictsPlugin(ctx, pl, state, pod, nodeInfo)
		// Stop the filter process immediately when any filter fails as filters are hard constraints
		if !pluginStatus.IsSuccess() {
			// Filter plugins are not supposed to return any status other than
			// Success or Unschedulable.
			if pluginStatus.IsUnschedulable() {
				statuses[pl.Name()] = pluginStatus
			}
			returnStatus := framework.NewStatus(pluginStatus.Code(), fmt.Sprintf("failed to run %q check conflict plugin for pod %q: %v", pl.Name(), pod.Name, pluginStatus.Message()))
			return map[string]*framework.Status{pl.Name(): returnStatus}
		}
	}

	return statuses
}

func (f *GodelFramework) runCheckTopologyPlugin(ctx context.Context, pl framework.CheckTopologyPlugin, state *framework.CycleState, pod *v1.Pod, nodeInfo framework.NodeInfo) *framework.Status {
	if !state.ShouldRecordPluginMetrics() {
		return pl.CheckTopology(ctx, state, pod, nodeInfo)
	}
	startTime := time.Now()
	status := pl.CheckTopology(ctx, state, pod, nodeInfo)

	podProperty, _ := framework.GetPodProperty(state)
	metrics.ObserveBindingStageDuration(podProperty, metrics.CheckTopologyEvaluation, pl.Name(), status.Code().String(), metrics.SinceInSeconds(startTime))
	return status
}

func (f *GodelFramework) RunCheckTopologyPlugins(ctx context.Context, state *framework.CycleState, pod *v1.Pod, nodeInfo framework.NodeInfo) framework.PluginToStatus {
	statuses := make(framework.PluginToStatus)
	for _, pl := range f.checkTopologyPlugins {
		pluginStatus := f.runCheckTopologyPlugin(ctx, pl, state, pod, nodeInfo)
		// Stop the filter process immediately when any filter fails as filters are hard constraints
		if !pluginStatus.IsSuccess() {
			// Filter plugins are not supposed to return any status other than
			// Success or Unschedulable.
			if pluginStatus.IsUnschedulable() {
				statuses[pl.Name()] = pluginStatus
			}
			returnStatus := framework.NewStatus(pluginStatus.Code(), fmt.Sprintf("failed to run %q check topology plugin for pod %q: %v", pl.Name(), pod.Name, pluginStatus.Message()))
			return map[string]*framework.Status{pl.Name(): returnStatus}
		}
	}

	return statuses
}

// RunPreBindPlugins runs the set of configured binder plugins. It returns a
// failure (bool) if any of the plugins returns an error. It also returns an
// error containing the rejection message or the error occurred in the plugin.
func (f *GodelFramework) RunPreBindPlugins(ctx context.Context, state *framework.CycleState, pod *v1.Pod, nodeName string) (status *framework.Status) {
	for _, pl := range f.preBindPlugins {
		status = f.runPreBindPlugin(ctx, pl, state, pod, nodeName)
		if !status.IsSuccess() {
			err := status.AsError()
			klog.ErrorS(err, "Failed to run PreBind plugin", "plugin", pl.Name(), "pod", klog.KObj(pod))
			return framework.AsStatus(fmt.Errorf("running PreBind plugin %q: %w", pl.Name(), err))
		}
	}
	return nil
}

func (f *GodelFramework) runPreBindPlugin(ctx context.Context, pl framework.PreBindPlugin, state *framework.CycleState, pod *v1.Pod, nodeName string) *framework.Status {
	if !state.ShouldRecordPluginMetrics() {
		return pl.PreBind(ctx, state, pod, nodeName)
	}
	startTime := time.Now()
	status := pl.PreBind(ctx, state, pod, nodeName)

	podProperty, _ := framework.GetPodProperty(state)
	metrics.ObserveBindingStageDuration(podProperty, metrics.PreBindEvaluation, pl.Name(), status.Code().String(), metrics.SinceInSeconds(startTime))
	return status
}

// RunBindPlugins runs the set of configured bind plugins until one returns a non `Skip` status.
func (f *GodelFramework) RunBindPlugins(ctx context.Context, state *framework.CycleState, pod *v1.Pod, nodeName string) (status *framework.Status) {
	if len(f.bindPlugins) == 0 {
		return framework.NewStatus(framework.Skip, "")
	}
	for _, bp := range f.bindPlugins {
		status = f.runBindPlugin(ctx, bp, state, pod, nodeName)
		if status != nil && status.Code() == framework.Skip {
			continue
		}
		if !status.IsSuccess() {
			err := status.AsError()
			klog.ErrorS(err, "Failed to run Bind plugin", "plugin", bp.Name(), "pod", klog.KObj(pod))
			return framework.AsStatus(fmt.Errorf("running Bind plugin %q: %w", bp.Name(), err))
		}
		return status
	}
	return status
}

func (f *GodelFramework) runBindPlugin(ctx context.Context, bp framework.BindPlugin, state *framework.CycleState, pod *v1.Pod, nodeName string) *framework.Status {
	if !state.ShouldRecordPluginMetrics() {
		return bp.Bind(ctx, state, pod, nodeName)
	}
	startTime := time.Now()
	status := bp.Bind(ctx, state, pod, nodeName)

	podProperty, _ := framework.GetPodProperty(state)
	metrics.ObserveBindingStageDuration(podProperty, metrics.BindEvaluation, bp.Name(), status.Code().String(), metrics.SinceInSeconds(startTime))
	return status
}

// RunPostBindPlugins runs the set of configured postbind plugins.
func (f *GodelFramework) RunPostBindPlugins(ctx context.Context, state *framework.CycleState, pod *v1.Pod, nodeName string) {
	for _, pl := range f.postBindPlugins {
		f.runPostBindPlugin(ctx, pl, state, pod, nodeName)
	}
}

func (f *GodelFramework) runPostBindPlugin(ctx context.Context, pl framework.PostBindPlugin, state *framework.CycleState, pod *v1.Pod, nodeName string) {
	if !state.ShouldRecordPluginMetrics() {
		pl.PostBind(ctx, state, pod, nodeName)
		return
	}
	startTime := time.Now()
	pl.PostBind(ctx, state, pod, nodeName)

	podProperty, _ := framework.GetPodProperty(state)
	metrics.ObserveBindingStageDuration(podProperty, metrics.PostBindEvaluation, pl.Name(), metrics.SuccessResult, metrics.SinceInSeconds(startTime))
}

// RunReservePluginsReserve runs the Reserve method in the set of configured
// reserve plugins. If any of these plugins returns an error, it does not
// continue running the remaining ones and returns the error. In such a case,
// the pod will not be scheduled and the caller will be expected to call
// RunReservePluginsUnreserve.
func (f *GodelFramework) RunReservePluginsReserve(ctx context.Context, state *framework.CycleState, pod *v1.Pod, nodeName string) (status *framework.Status) {
	for _, pl := range f.reservePlugins {
		status = f.runReservePluginReserve(ctx, pl, state, pod, nodeName)
		if !status.IsSuccess() {
			err := status.AsError()
			klog.ErrorS(err, "Failed running Reserve plugin", "plugin", pl.Name(), "pod", podutil.GetPodKey(pod))
			return framework.AsStatus(fmt.Errorf("running Reserve plugin %q: %w", pl.Name(), err))
		}
	}
	return nil
}

func (f *GodelFramework) runReservePluginReserve(ctx context.Context, pl framework.ReservePlugin, state *framework.CycleState, pod *v1.Pod, nodeName string) *framework.Status {
	if !state.ShouldRecordPluginMetrics() {
		return pl.Reserve(ctx, state, pod, nodeName)
	}
	startTime := time.Now()
	status := pl.Reserve(ctx, state, pod, nodeName)

	podProperty, _ := framework.GetPodProperty(state)
	metrics.ObserveBindingStageDuration(podProperty, metrics.ReserveEvaluation, pl.Name(), status.Code().String(), metrics.SinceInSeconds(startTime))
	return status
}

// RunReservePluginsUnreserve runs the Unreserve method in the set of
// configured reserve plugins.
func (f *GodelFramework) RunReservePluginsUnreserve(ctx context.Context, state *framework.CycleState, pod *v1.Pod, nodeName string) {
	// Execute the Unreserve operation of each reserve plugin in the
	// *reverse* order in which the Reserve operation was executed.
	for i := len(f.reservePlugins) - 1; i >= 0; i-- {
		f.runReservePluginUnreserve(ctx, f.reservePlugins[i], state, pod, nodeName)
	}
}

func (f *GodelFramework) runReservePluginUnreserve(ctx context.Context, pl framework.ReservePlugin, state *framework.CycleState, pod *v1.Pod, nodeName string) {
	if !state.ShouldRecordPluginMetrics() {
		pl.Unreserve(ctx, state, pod, nodeName)
		return
	}
	startTime := time.Now()
	pl.Unreserve(ctx, state, pod, nodeName)

	podProperty, _ := framework.GetPodProperty(state)
	metrics.ObserveBindingStageDuration(podProperty, metrics.UnreserveEvaluation, pl.Name(), metrics.SuccessResult, metrics.SinceInSeconds(startTime))
}

// RunPermitPlugins runs the set of configured permit plugins. If any of these
// plugins returns a status other than "Success" or "Wait", it does not continue
// running the remaining plugins and returns an error. Otherwise, if any of the
// plugins returns "Wait", then this function will create and add waiting pod
// to a map of currently waiting pods and return status with "Wait" code.
// Pod will remain waiting pod for the minimum duration returned by the permit plugins.
func (f *GodelFramework) RunPermitPlugins(ctx context.Context, state *framework.CycleState, pod *v1.Pod, nodeName string) (status *framework.Status) {
	statusCode := framework.Success
	for _, pl := range f.permitPlugins {
		status, _ := f.runPermitPlugin(ctx, pl, state, pod, nodeName)
		if !status.IsSuccess() {
			if status.IsUnschedulable() {
				klog.V(4).InfoS("Rejected pod by permit plugin", "pod", klog.KObj(pod), "plugin", pl.Name(), "status", status.Message())
				return framework.NewStatus(status.Code(), fmt.Sprintf("rejected pod %q by permit plugin %q: %v", pod.Name, pl.Name(), status.Message()))
			}
			if status.Code() == framework.Wait {
				statusCode = framework.Wait
			} else {
				err := status.AsError()
				klog.ErrorS(err, "Failed running Permit plugin", "plugin", pl.Name(), "pod", podutil.GetPodKey(pod))
				return framework.AsStatus(fmt.Errorf("running Permit plugin %q: %w", pl.Name(), err))
			}
		}
	}
	if statusCode == framework.Wait {
		klog.V(4).InfoS("One or more plugins asked to wait and no plugin rejected pod", "pod", klog.KObj(pod))
		return framework.NewStatus(framework.Wait, fmt.Sprintf("one or more plugins asked to wait and no plugin rejected pod %q", pod.Name))
	}
	return nil
}

func (f *GodelFramework) runPermitPlugin(ctx context.Context, pl framework.PermitPlugin, state *framework.CycleState, pod *v1.Pod, nodeName string) (*framework.Status, time.Duration) {
	if !state.ShouldRecordPluginMetrics() {
		return pl.Permit(ctx, state, pod, nodeName)
	}
	startTime := time.Now()
	status, timeout := pl.Permit(ctx, state, pod, nodeName)

	podProperty, _ := framework.GetPodProperty(state)
	metrics.ObserveBindingStageDuration(podProperty, metrics.PermitEvaluation, pl.Name(), status.Code().String(), metrics.SinceInSeconds(startTime))
	return status, timeout
}

// WaitOnPermit will block, if the pod is a waiting pod, until the waiting pod is rejected or allowed.
func (f *GodelFramework) WaitOnPermit(ctx context.Context, pod *v1.Pod) (status *framework.Status) {
	/*
		waitingPod := f.waitingPods.Get(pod.UID)
			waitingPod := f.waitingTasksManager.GetWaitingTask(pod)
			if waitingPod == nil {
				// TODO: if pod does not exist in bound map, return error
				// that is to sya: waiting pod is deleted...
				return nil
			}
			defer f.waitingTasksManager.RemoveWaitingTask(pod)
		klog.V(4).Infof("pod %q waiting on permit", pod.Name)

		start := time.Now()
		s := <-waitingPod.statusChan
		metrics.ObserveBindingStageDuration(pod,metrics.WaitOnPermitEvaluation, "waiting", s.Code().String()).Observe(metrics.SinceInSeconds(start))

		if !s.IsSuccess() {
			if s.IsUnschedulable() {
				msg := fmt.Sprintf("pod %q rejected while waiting on permit: %v", pod.Name, s.Message())
				klog.V(4).Infof(msg)
				return framework.NewStatus(s.Code(), msg)
			}
			err := s.AsError()
			klog.ErrorS(err, "Failed waiting on permit for pod", "pod", klog.KObj(pod))
			return framework.AsStatus(fmt.Errorf("waiting on permit for pod: %w", err))
		}
	*/
	return nil
}

func (f *GodelFramework) ListPlugins() map[string]sets.String {
	m := map[string]sets.String{
		framework.CheckTopologyPhase: sets.NewString(),
		framework.CheckConflictPhase: sets.NewString(),
		framework.PermitPhase:        sets.NewString(),
		framework.PreBindPhase:       sets.NewString(),
		framework.PostBindPhase:      sets.NewString(),
		framework.BindPhase:          sets.NewString(),
		framework.ReservePhase:       sets.NewString(),
	}

	for _, plugin := range f.checkTopologyPlugins {
		m[framework.CheckTopologyPhase].Insert(plugin.Name())
	}
	for _, plugin := range f.checkConflictsPlugins {
		m[framework.CheckConflictPhase].Insert(plugin.Name())
	}
	for _, plugin := range f.permitPlugins {
		m[framework.PermitPhase].Insert(plugin.Name())
	}
	for _, plugin := range f.preBindPlugins {
		m[framework.PreBindPhase].Insert(plugin.Name())
	}
	for _, plugin := range f.postBindPlugins {
		m[framework.PostBindPhase].Insert(plugin.Name())
	}
	for _, plugin := range f.bindPlugins {
		m[framework.BindPhase].Insert(plugin.Name())
	}
	for _, plugin := range f.reservePlugins {
		m[framework.ReservePhase].Insert(plugin.Name())
	}

	return m
}

func (f *GodelFramework) HasCheckConflictsPlugins() bool {
	return len(f.checkConflictsPlugins) > 0
}

func (f *GodelFramework) HasCheckTopologyPlugins() bool {
	return len(f.checkTopologyPlugins) > 0
}

func (f *GodelFramework) HasPlugin(pluginName string) bool {
	for _, plugin := range f.checkTopologyPlugins {
		if plugin.Name() == pluginName {
			return true
		}
	}
	for _, plugin := range f.checkConflictsPlugins {
		if plugin.Name() == pluginName {
			return true
		}
	}
	for _, plugin := range f.permitPlugins {
		if plugin.Name() == pluginName {
			return true
		}
	}
	for _, plugin := range f.preBindPlugins {
		if plugin.Name() == pluginName {
			return true
		}
	}
	for _, plugin := range f.postBindPlugins {
		if plugin.Name() == pluginName {
			return true
		}
	}
	for _, plugin := range f.bindPlugins {
		if plugin.Name() == pluginName {
			return true
		}
	}
	for _, plugin := range f.reservePlugins {
		if plugin.Name() == pluginName {
			return true
		}
	}
	return false
}

func (f *GodelFramework) InitCycleState(pod *v1.Pod) (*framework.CycleState, error) {
	state := framework.NewCycleState()
	podResourceType, err := podutil.GetPodResourceType(pod)
	if err != nil {
		return nil, err
	}
	if err = framework.SetPodResourceTypeState(podResourceType, state); err != nil {
		return nil, err
	}

	podLauncher, err := podutil.GetPodLauncher(pod)
	if err != nil {
		return nil, err
	}
	if err = framework.SetPodLauncherState(podLauncher, state); err != nil {
		return nil, err
	}

	// Set pod property
	framework.SetPodProperty(framework.ExtractPodProperty(pod), state)
	return state, nil
}

func (f *GodelFramework) RunClusterPrePreemptingPlugins(preemptor *v1.Pod, state, commonState *framework.CycleState) *framework.Status {
	for _, plugin := range f.clusterPrePreemptingPlugins {
		if err := plugin.ClusterPrePreempting(preemptor, state, commonState); err != nil {
			return err
		}
	}
	return nil
}

func (f *GodelFramework) RunVictimCheckingPlugins(preemptor, pod *v1.Pod, state, commonState *framework.CycleState) *framework.Status {
	for i, pluginCollection := range f.victimCheckingPlugins {
		status := f.runVictimCheckingPluginCollection(pluginCollection, preemptor, pod, state, commonState)
		switch status.Code() {
		case framework.PreemptionFail:
			return status
		case framework.PreemptionSucceed:
			if pluginCollection.ForceQuickPass() || pluginCollection.EnableQuickPass() {
				return status
			}
		case framework.PreemptionNotSure:
			continue
		default:
			return framework.NewStatus(framework.PreemptionFail, fmt.Sprintf("unknown preemption code %v for collection %d", status.Code(), i))
		}
	}
	return framework.NewStatus(framework.PreemptionSucceed)
}

func (f *GodelFramework) runVictimCheckingPluginCollection(pluginCollection *framework.VictimCheckingPluginCollection, preemptor, pod *v1.Pod, state, commonState *framework.CycleState) *framework.Status {
	for _, plugin := range pluginCollection.GetVictimCheckingPlugins() {
		code, msg := plugin.VictimChecking(preemptor, pod, state, commonState)
		switch code {
		case framework.PreemptionFail:
			return framework.NewStatus(code, msg)
		case framework.PreemptionSucceed:
			return framework.NewStatus(code)
		case framework.PreemptionNotSure:
			continue
		default:
			return framework.NewStatus(framework.PreemptionFail, fmt.Sprintf("unknown preemption code %v for plugin %v", code, plugin))
		}
	}
	return framework.NewStatus(framework.PreemptionNotSure)
}

func (f *GodelFramework) RunPostVictimCheckingPlugins(preemptor, pod *v1.Pod, state, commonState *framework.CycleState) *framework.Status {
	for _, plugin := range f.postVictimCheckingPlugins {
		if err := plugin.PostVictimChecking(preemptor, pod, state, commonState); err != nil {
			return err
		}
	}
	return nil
}

// New creates a new GodelBinderFramework, where pluginRegistry marks which plugins are supported, basePlugins presents which plugins are enabled by default.
// podConstraintConfigs are used in pod annotation, where hard constraint will be taken as filter plugins and soft constraint will be taken as score plugins.
// If plugin in podConstraintConfigs not exists in basePlugins, add this plugin to the new Godel Framework.
func New(
	pluginRegistry framework.PluginMap,
	preemptionPluginRegistry framework.PluginMap,
	basePlugins *apis.BinderPluginCollection,
) framework.BinderFramework {
	f := &GodelFramework{
		checkConflictsPlugins: make([]framework.CheckConflictsPlugin, 0),
		checkTopologyPlugins:  make([]framework.CheckTopologyPlugin, 0),
		reservePlugins:        make([]framework.ReservePlugin, 0),
		permitPlugins:         make([]framework.PermitPlugin, 0),
		preBindPlugins:        make([]framework.PreBindPlugin, 0),
		bindPlugins:           make([]framework.BindPlugin, 0),
		postBindPlugins:       make([]framework.PostBindPlugin, 0),
	}

	if basePlugins != nil {
		// prepare CheckConflicts plugins
		for _, name := range basePlugins.CheckConflicts {
			if plugin, ok := pluginRegistry[name]; ok {
				if pl, ok := plugin.(framework.CheckConflictsPlugin); ok {
					f.checkConflictsPlugins = append(f.checkConflictsPlugins, pl)
				} else {
					klog.InfoS("WARN: Expected a CheckConflict plugin, but it did not implement the interface", "plugin", name)
				}
			} else {
				klog.InfoS("WARN: Expected a CheckConflict plugin, but it did not exist", "plugin", name)
			}
		}
		// prepare CheckTopology plugins
		for _, name := range basePlugins.CheckTopology {
			if plugin, ok := pluginRegistry[name]; ok {
				if pl, ok := plugin.(framework.CheckTopologyPlugin); ok {
					f.checkTopologyPlugins = append(f.checkTopologyPlugins, pl)
				} else {
					klog.InfoS("WARN: Expected a CheckTopology plugin, but it did not implement the interface", "plugin", name)
				}
			} else {
				klog.InfoS("WARN: Expected a CheckTopology plugin, but it did not exist", "plugin", name)
			}
		}
		// prepare Reserve plugins
		for _, name := range basePlugins.Reserves {
			if plugin, ok := pluginRegistry[name]; ok {
				if pl, ok := plugin.(framework.ReservePlugin); ok {
					f.reservePlugins = append(f.reservePlugins, pl)
				} else {
					klog.InfoS("WARN: Expected a Reserve plugin, but it did not implement the interface", "plugin", name)
				}
			} else {
				klog.InfoS("WARN: Expected a Reserve plugin, but it did not exist", "plugin", name)
			}
		}
		// prepare Permit plugins
		for _, name := range basePlugins.Permits {
			if plugin, ok := pluginRegistry[name]; ok {
				if pl, ok := plugin.(framework.PermitPlugin); ok {
					f.permitPlugins = append(f.permitPlugins, pl)
				} else {
					klog.InfoS("WARN: Expected a Permit plugin, but it did not implement the interface", "plugin", name)
				}
			} else {
				klog.InfoS("WARN: Expected a Permit plugin, but it did not exist", "plugin", name)
			}
		}
		// prepare PreBind plugins
		for _, name := range basePlugins.PreBinds {
			if plugin, ok := pluginRegistry[name]; ok {
				if pl, ok := plugin.(framework.PreBindPlugin); ok {
					f.preBindPlugins = append(f.preBindPlugins, pl)
				} else {
					klog.InfoS("WARN: Expected a PreBind plugin, but it did not implement the interface", "plugin", name)
				}
			} else {
				klog.InfoS("WARN: Expected a PreBind plugin, but it did not exist", "plugin", name)
			}
		}
		// prepare Bind plugins
		for _, name := range basePlugins.Binds {
			if plugin, ok := pluginRegistry[name]; ok {
				if pl, ok := plugin.(framework.BindPlugin); ok {
					f.bindPlugins = append(f.bindPlugins, pl)
				} else {
					klog.InfoS("WARN: Expected a Bind plugin, but it did not implement the interface", "plugin", name)
				}
			} else {
				klog.InfoS("WARN: Expected a Bind plugin, but it did not exist", "plugin", name)
			}
		}
		// prepare PostBind plugins
		for _, name := range basePlugins.PostBinds {
			if plugin, ok := pluginRegistry[name]; ok {
				if pl, ok := plugin.(framework.PostBindPlugin); ok {
					f.postBindPlugins = append(f.postBindPlugins, pl)
				} else {
					klog.InfoS("WARN: Expected a PostBind plugin, but it did not implement the interface", "plugin", name)
				}
			} else {
				klog.InfoS("WARN: Expected a PostBind plugin, but it did not exist", "plugin", name)
			}
		}
		// prepare preemption plugins
		for _, pluginCollectionSpec := range basePlugins.VictimCheckings {
			f.addVictimCheckingPlugin(pluginCollectionSpec, preemptionPluginRegistry)
		}
	}
	return f
}

func (f *GodelFramework) addVictimCheckingPlugin(pluginCollectionSpec *framework.VictimCheckingPluginCollectionSpec, preemptionPluginRegistry framework.PluginMap) {
	var victimCheckingPlugins []framework.VictimCheckingPlugin
	for _, pluginSpec := range pluginCollectionSpec.GetSearchingPlugins() {
		plgName := pluginSpec.GetName()
		plugin, ok := preemptionPluginRegistry[plgName]
		if !ok {
			klog.InfoS("WARN: Preemption plugin not supported", "plugin", plgName)
			continue
		}
		if victimCheckingPlugin, ok := plugin.(framework.VictimCheckingPlugin); !ok {
			klog.InfoS("WARN: Plugin was not a VictimCheckingPlugin", "plugin", plgName)
		} else {
			victimCheckingPlugins = append(victimCheckingPlugins, victimCheckingPlugin)
		}
		if clusterPrePreemptingPlugin, ok := plugin.(framework.ClusterPrePreemptingPlugin); ok {
			f.clusterPrePreemptingPlugins = append(f.clusterPrePreemptingPlugins, clusterPrePreemptingPlugin)
		}
		if postVictimCheckingPlugin, ok := plugin.(framework.PostVictimCheckingPlugin); ok {
			f.postVictimCheckingPlugins = append(f.postVictimCheckingPlugins, postVictimCheckingPlugin)
		}
	}
	preemptionPluginCollection := framework.NewVictimCheckingPluginCollection(victimCheckingPlugins, pluginCollectionSpec.EnableQuickPass(), pluginCollectionSpec.ForceQuickPass())
	f.victimCheckingPlugins = append(f.victimCheckingPlugins, preemptionPluginCollection)
}

const (
	cleanDeadLockCachePeriod        = 5 * time.Second
	podsMembersInSmallJob           = 10
	podsMembersInMidJob             = 30
	podsMembersInLargeJob           = 50
	resourceLockDurationForSmallJob = time.Minute * 1
	resourceLockDurationForMidJob   = time.Minute * 2
	resourceLockDurationForLargeJob = time.Minute * 3
)

var DefaultGangTimeout = 5 * time.Minute
