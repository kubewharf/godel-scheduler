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
	"fmt"
	"sort"

	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/klog/v2"

	framework "github.com/kubewharf/godel-scheduler/pkg/framework/api"
	podutil "github.com/kubewharf/godel-scheduler/pkg/util/pod"
)

// GodelSchedulerPreemptionFramework is the component responsible for initializing and running scheduler
// plugins, determining plugins to run in each scheduling phase(or extension point)
type GodelSchedulerPreemptionFramework struct {
	clusterPrePreemptingPlugins []framework.ClusterPrePreemptingPlugin
	nodePrePreemptingPlugins    []framework.NodePrePreemptingPlugin
	victimSearchingPlugins      []*framework.VictimSearchingPluginCollection
	postVictimSearchingPlugins  []framework.PostVictimSearchingPlugin
	nodePostPreemptingPlugins   []framework.NodePostPreemptingPlugin
	candidatesSortingPlugins    []framework.CandidatesSortingPlugin
}

func NewPreemptionFramework(preemptionPluginRegistry framework.PluginMap,
	basePlugins *framework.PluginCollection,
) *GodelSchedulerPreemptionFramework {
	f := &GodelSchedulerPreemptionFramework{
		victimSearchingPlugins:      make([]*framework.VictimSearchingPluginCollection, 0),
		clusterPrePreemptingPlugins: make([]framework.ClusterPrePreemptingPlugin, 0),
		nodePrePreemptingPlugins:    make([]framework.NodePrePreemptingPlugin, 0),
		postVictimSearchingPlugins:  make([]framework.PostVictimSearchingPlugin, 0),
		nodePostPreemptingPlugins:   make([]framework.NodePostPreemptingPlugin, 0),
		candidatesSortingPlugins:    make([]framework.CandidatesSortingPlugin, 0),
	}

	if basePlugins != nil {
		// prepare preemption plugins
		for _, pluginCollectionSpec := range basePlugins.Searchings {
			f.addVictimSearchingPlugin(pluginCollectionSpec, preemptionPluginRegistry)
		}
		for _, plugin := range basePlugins.Sortings {
			f.addSortingPlugin(plugin, preemptionPluginRegistry)
		}
	}

	return f
}

func (f *GodelSchedulerPreemptionFramework) addVictimSearchingPlugin(pluginCollectionSpec *framework.VictimSearchingPluginCollectionSpec, preemptionPluginRegistry framework.PluginMap) {
	var searchingPlugins []framework.VictimSearchingPlugin
	for _, pluginSpec := range pluginCollectionSpec.GetSearchingPlugins() {
		plgName := pluginSpec.GetName()
		plugin, ok := preemptionPluginRegistry[plgName]
		if !ok {
			klog.InfoS("WARN: Some Searching plugin was not supported", "pluginName", plgName)
			continue
		}
		if searchingPlugin, ok := plugin.(framework.VictimSearchingPlugin); !ok {
			klog.InfoS("WARN: The Searching plugin did not implement the expected interface", "pluginName", plgName)
		} else {
			searchingPlugins = append(searchingPlugins, searchingPlugin)
		}
		if clusterPrePreemptingPlugin, ok := plugin.(framework.ClusterPrePreemptingPlugin); ok {
			f.clusterPrePreemptingPlugins = append(f.clusterPrePreemptingPlugins, clusterPrePreemptingPlugin)
		}
		if nodePrePreemptingPlugin, ok := plugin.(framework.NodePrePreemptingPlugin); ok {
			f.nodePrePreemptingPlugins = append(f.nodePrePreemptingPlugins, nodePrePreemptingPlugin)
		}
		if postVictimSearchingPlugin, ok := plugin.(framework.PostVictimSearchingPlugin); ok {
			f.postVictimSearchingPlugins = append(f.postVictimSearchingPlugins, postVictimSearchingPlugin)
		}
		if nodePostPreemptingPlugin, ok := plugin.(framework.NodePostPreemptingPlugin); ok {
			f.nodePostPreemptingPlugins = append(f.nodePostPreemptingPlugins, nodePostPreemptingPlugin)
		}
	}
	searchingPluginCollection := framework.NewVictimSearchingPluginCollection(searchingPlugins, pluginCollectionSpec.EnableQuickPass(), pluginCollectionSpec.ForceQuickPass(), pluginCollectionSpec.RejectNotSure())
	f.victimSearchingPlugins = append(f.victimSearchingPlugins, searchingPluginCollection)
}

func (f *GodelSchedulerPreemptionFramework) addSortingPlugin(pluginSpec *framework.PluginSpec, preemptionPluginRegistry framework.PluginMap) {
	plgName := pluginSpec.GetName()
	if _, ok := preemptionPluginRegistry[plgName]; !ok {
		klog.InfoS("WARN: Some Sorting plugin was not supported", "pluginName", plgName)
		return
	}

	if pl, ok := preemptionPluginRegistry[plgName].(framework.CandidatesSortingPlugin); ok {
		f.candidatesSortingPlugins = append(f.candidatesSortingPlugins, pl)
	} else {
		klog.InfoS("WARN: The Sorting plugin did not implement the expected interface", "pluginName", plgName)
	}
}

func (f *GodelSchedulerPreemptionFramework) HasVictimSearchingPlugin(pluginName string) bool {
	for _, pluginCollection := range f.victimSearchingPlugins {
		for _, plugin := range pluginCollection.GetVictimSearchingPlugins() {
			if plugin.Name() == pluginName {
				return true
			}
		}
	}
	return false
}

func (f *GodelSchedulerPreemptionFramework) RunVictimSearchingPlugins(preemptor *v1.Pod, podInfo *framework.PodInfo, state, preemptionState *framework.CycleState, victimState *framework.VictimState) (framework.Code, string) {
	pass := false
	quickPass := false
	podKey := podutil.GeneratePodKey(podInfo.Pod)
	for i, pluginCollection := range f.victimSearchingPlugins {
		code, msg := f.runVictimSearchingPluginCollection(pluginCollection, preemptor, podInfo, state, preemptionState, victimState)
		switch code {
		case framework.PreemptionFail:
			if quickPass {
				framework.SetPodsCanNotBePreempted(podKey, preemptionState)
				return framework.PreemptionSucceed, ""
			}
			return code, msg
		case framework.PreemptionSucceed:
			if pluginCollection.ForceQuickPass() {
				return framework.PreemptionSucceed, ""
			}
			if pluginCollection.EnableQuickPass() {
				quickPass = true
			} else {
				pass = true
			}
		case framework.PreemptionNotSure:
			if pluginCollection.RejectNotSure() {
				if quickPass {
					framework.SetPodsCanNotBePreempted(podKey, preemptionState)
					return framework.PreemptionSucceed, ""
				}
				return framework.PreemptionFail, fmt.Sprintf("reject not sure result for collection %d", i)
			}
		}
	}
	if quickPass {
		framework.SetPodsCanNotBePreempted(podKey, preemptionState)
		return framework.PreemptionSucceed, ""
	} else if pass {
		return framework.PreemptionSucceed, ""
	}
	return framework.PreemptionFail, "all plugins return not sure"
}

func (f *GodelSchedulerPreemptionFramework) RunClusterPrePreemptingPlugins(preemptor *v1.Pod, state, commonState *framework.CycleState) *framework.Status {
	for _, pl := range f.clusterPrePreemptingPlugins {
		if err := pl.ClusterPrePreempting(preemptor, state, commonState); err != nil {
			return err
		}
	}
	return nil
}

func (f *GodelSchedulerPreemptionFramework) RunNodePrePreemptingPlugins(preemptor *v1.Pod, nodeInfo framework.NodeInfo, state, preemptionState *framework.CycleState) *framework.Status {
	for _, pl := range f.nodePrePreemptingPlugins {
		if err := pl.NodePrePreempting(preemptor, nodeInfo, state, preemptionState); err != nil {
			return err
		}
	}
	return nil
}

func (f *GodelSchedulerPreemptionFramework) runVictimSearchingPluginCollection(
	pluginCollection *framework.VictimSearchingPluginCollection,
	preemptor *v1.Pod, podInfo *framework.PodInfo,
	state, preemptionState *framework.CycleState,
	victimState *framework.VictimState,
) (framework.Code, string) {
	for _, plugin := range pluginCollection.GetVictimSearchingPlugins() {
		code, msg := plugin.VictimSearching(preemptor, podInfo, state, preemptionState, victimState)
		switch code {
		case framework.PreemptionFail:
			return code, msg
		case framework.PreemptionSucceed:
			return code, msg
		case framework.PreemptionNotSure:
			continue
		case framework.Error:
			return framework.PreemptionFail, fmt.Sprintf("unexpected preemption result %s for plugin %v", code, plugin.Name())
		default:
			return framework.PreemptionFail, fmt.Sprintf("unknown preemption result %s for plugin %v", code, plugin.Name())
		}
	}
	return framework.PreemptionNotSure, ""
}

func (f *GodelSchedulerPreemptionFramework) RunPostVictimSearchingPlugins(preemptor *v1.Pod, podInfo *framework.PodInfo, state, preemptionState *framework.CycleState, victimState *framework.VictimState) *framework.Status {
	for _, plugin := range f.postVictimSearchingPlugins {
		if err := plugin.PostVictimSearching(preemptor, podInfo, state, preemptionState, victimState); err != nil {
			return err
		}
	}
	return nil
}

func (f *GodelSchedulerPreemptionFramework) RunNodePostPreemptingPlugins(preemptor *v1.Pod, victims []*v1.Pod, state, commonState *framework.CycleState) *framework.Status {
	for _, plugin := range f.nodePostPreemptingPlugins {
		if err := plugin.NodePostPreempting(preemptor, victims, state, commonState); err != nil {
			return err
		}
	}
	return nil
}

func (f *GodelSchedulerPreemptionFramework) RunCandidatesSortingPlugins(
	candidates []*framework.Candidate,
	candidate *framework.Candidate,
) []*framework.Candidate {
	if candidate == nil {
		sort.SliceStable(candidates, func(i, j int) bool {
			return f.compareNominatedNodes(candidates[i], candidates[j])
		})
		return candidates
	}
	for i, node := range candidates {
		if !f.compareNominatedNodes(node, candidate) {
			candidates = append(candidates[:i], append([]*framework.Candidate{candidate}, candidates[i:]...)...)
			return candidates
		}
	}
	candidates = append(candidates, candidate)
	return candidates
}

func (f *GodelSchedulerPreemptionFramework) compareNominatedNodes(c1, c2 *framework.Candidate) bool {
	for _, plugin := range f.candidatesSortingPlugins {
		var numPods1, numPods2 int
		if c1.Victims != nil {
			numPods1 = len(c1.Victims.Pods)
		}
		if c2.Victims != nil {
			numPods2 = len(c2.Victims.Pods)
		}
		if numPods1 == 0 {
			if numPods2 == 0 {
				return false
			} else {
				return true
			}
		} else {
			if numPods2 == 0 {
				return false
			}
		}
		cmp := plugin.Compare(c1, c2)
		if cmp > 0 {
			return true
		} else if cmp < 0 {
			return false
		}
	}
	return false
}

func (f *GodelSchedulerPreemptionFramework) ListPlugins() map[string]sets.String {
	m := map[string]sets.String{
		framework.ClusterPrePreemptingPhase: sets.NewString(),
		framework.NodePrePreemptingPhase:    sets.NewString(),
		framework.VictimSearchingPhase:      sets.NewString(),
		framework.PostVictimSearchingPhase:  sets.NewString(),
		framework.NodePostPreemptingPhase:   sets.NewString(),
	}

	for _, plugin := range f.clusterPrePreemptingPlugins {
		m[framework.ClusterPrePreemptingPhase].Insert(plugin.Name())
	}
	for _, plugin := range f.nodePrePreemptingPlugins {
		m[framework.NodePrePreemptingPhase].Insert(plugin.Name())
	}
	for _, collection := range f.victimSearchingPlugins {
		for _, plugin := range collection.GetVictimSearchingPlugins() {
			m[framework.VictimSearchingPhase].Insert(plugin.Name())
		}
	}
	for _, plugin := range f.postVictimSearchingPlugins {
		m[framework.PostVictimSearchingPhase].Insert(plugin.Name())
	}
	for _, plugin := range f.nodePostPreemptingPlugins {
		m[framework.NodePostPreemptingPhase].Insert(plugin.Name())
	}

	return m
}
