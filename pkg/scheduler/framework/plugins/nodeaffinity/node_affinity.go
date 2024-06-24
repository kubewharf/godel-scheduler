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

package nodeaffinity

import (
	"context"
	"fmt"

	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"

	framework "github.com/kubewharf/godel-scheduler/pkg/framework/api"
	pluginhelper "github.com/kubewharf/godel-scheduler/pkg/plugins/helper"
	"github.com/kubewharf/godel-scheduler/pkg/plugins/podlauncher"
	"github.com/kubewharf/godel-scheduler/pkg/scheduler/framework/handle"
	"github.com/kubewharf/godel-scheduler/pkg/util/helper"
	podutil "github.com/kubewharf/godel-scheduler/pkg/util/pod"
)

const (
	// Name is the name of the plugin used in the plugin registry and configurations.
	Name              = "NodeAffinity"
	preFilterStateKey = "PreFilter" + Name
)

type requiredNodeAffinityTermSelector struct {
	LabelSelector labels.Selector
	FieldSelector fields.Selector
}

type preFilterState struct {
	nodeLabelSelector                 labels.Selector
	requiredNodeAffinityTermSelectors []*requiredNodeAffinityTermSelector
}

func getPreFilterState(cycleState *framework.CycleState) (*preFilterState, error) {
	c, err := cycleState.Read(preFilterStateKey)
	if err != nil {
		// preFilterState doesn't exist, likely PreFilter wasn't invoked.
		return nil, fmt.Errorf("error reading %q from cycleState: %v", preFilterStateKey, err)
	}

	s, ok := c.(*preFilterState)
	if !ok {
		return nil, fmt.Errorf("%+v convert to NodeAffinity.preFilterState error", c)
	}
	return s, nil
}

func (state *preFilterState) Clone() framework.StateData {
	// Do not clone for NodeAffinity.
	return state
}

// NodeAffinity is a plugin that checks if a pod node selector matches the node label.
type NodeAffinity struct {
	handle handle.PodFrameworkHandle
}

var (
	_ framework.PreFilterPlugin = &NodeAffinity{}
	_ framework.FilterPlugin    = &NodeAffinity{}
	_ framework.ScorePlugin     = &NodeAffinity{}
)

// Name returns name of the plugin. It is used in logs, etc.
func (pl *NodeAffinity) Name() string {
	return Name
}

func (a *NodeAffinity) PreFilter(ctx context.Context, state *framework.CycleState, pod *v1.Pod) *framework.Status {
	data := &preFilterState{nodeLabelSelector: labels.SelectorFromSet(pod.Spec.NodeSelector)}
	affinity := pod.Spec.Affinity
	if affinity != nil && affinity.NodeAffinity != nil && affinity.NodeAffinity.RequiredDuringSchedulingIgnoredDuringExecution != nil {
		var selectors []*requiredNodeAffinityTermSelector
		nodeSelectorTerms := affinity.NodeAffinity.RequiredDuringSchedulingIgnoredDuringExecution.NodeSelectorTerms
		for _, req := range nodeSelectorTerms {
			// nil or empty term selects no objects
			if len(req.MatchExpressions) == 0 && len(req.MatchFields) == 0 {
				continue
			}

			selector := &requiredNodeAffinityTermSelector{}
			if len(req.MatchExpressions) != 0 {
				if labelSelector, err := helper.NodeSelectorRequirementsAsSelector(req.MatchExpressions); err == nil {
					selector.LabelSelector = labelSelector
				}
			}
			if len(req.MatchFields) != 0 {
				if fieldSelector, err := helper.NodeSelectorRequirementsAsFieldSelector(req.MatchFields); err == nil {
					selector.FieldSelector = fieldSelector
				}
			}

			if selector.LabelSelector != nil || selector.FieldSelector != nil {
				selectors = append(selectors, selector)
			}
		}
		data.requiredNodeAffinityTermSelectors = selectors
	}
	state.Write(preFilterStateKey, data)
	return nil
}

func (a *NodeAffinity) PreFilterExtensions() framework.PreFilterExtensions {
	return nil
}

// Filter invoked at the filter extension point.
func (pl *NodeAffinity) Filter(ctx context.Context, state *framework.CycleState, pod *v1.Pod, nodeInfo framework.NodeInfo) *framework.Status {
	podLauncher, status := podlauncher.NodeFits(state, pod, nodeInfo)
	if status != nil {
		return status
	}

	var nodeLabels map[string]string
	switch podLauncher {
	case podutil.Kubelet:
		nodeLabels = nodeInfo.GetNode().Labels
	case podutil.NodeManager:
		nodeLabels = nodeInfo.GetNMNode().Labels
	}

	nodeName := nodeInfo.GetNodeName()
	nodeAffinityRelated, err := getPreFilterState(state)
	if err != nil {
		return framework.NewStatus(framework.UnschedulableAndUnresolvable, err.Error())
	}
	if nodeAffinityRelated == nil {
		return framework.NewStatus(framework.UnschedulableAndUnresolvable, ErrNilPreFilterState.Error())
	}

	if err := podMatchesNodeSelectorAndAffinityTerms(pod, nodeAffinityRelated, nodeLabels, nodeName); err != nil {
		return framework.NewStatus(framework.UnschedulableAndUnresolvable, err.Error())
	}
	return nil
}

// Score invoked at the Score extension point.
func (pl *NodeAffinity) Score(ctx context.Context, state *framework.CycleState, pod *v1.Pod, nodeName string) (int64, *framework.Status) {
	nodeInfo, err := pl.handle.SnapshotSharedLister().NodeInfos().Get(nodeName)
	if err != nil {
		return 0, framework.NewStatus(framework.Error, fmt.Sprintf("getting node %q from Snapshot: %v", nodeName, err))
	}

	// PodLauncher plugin is supposed to run in default, no need to do node check here
	var nodeLabels map[string]string
	switch podLauncher, _ := podutil.GetPodLauncher(pod); podLauncher {
	case podutil.Kubelet:
		nodeLabels = nodeInfo.GetNode().Labels
	case podutil.NodeManager:
		nodeLabels = nodeInfo.GetNMNode().Labels
	}

	affinity := pod.Spec.Affinity

	var count int64
	// A nil element of PreferredDuringSchedulingIgnoredDuringExecution matches no objects.
	// An element of PreferredDuringSchedulingIgnoredDuringExecution that refers to an
	// empty PreferredSchedulingTerm matches all objects.
	if affinity != nil && affinity.NodeAffinity != nil && affinity.NodeAffinity.PreferredDuringSchedulingIgnoredDuringExecution != nil {
		// Match PreferredDuringSchedulingIgnoredDuringExecution term by term.
		for i := range affinity.NodeAffinity.PreferredDuringSchedulingIgnoredDuringExecution {
			preferredSchedulingTerm := &affinity.NodeAffinity.PreferredDuringSchedulingIgnoredDuringExecution[i]
			if preferredSchedulingTerm.Weight == 0 {
				continue
			}

			// TODO: Avoid computing it for all nodes if this becomes a performance problem.
			nodeSelector, err := helper.NodeSelectorRequirementsAsSelector(preferredSchedulingTerm.Preference.MatchExpressions)
			if err != nil {
				return 0, framework.NewStatus(framework.Error, err.Error())
			}

			if nodeSelector.Matches(labels.Set(nodeLabels)) {
				count += int64(preferredSchedulingTerm.Weight)
			}
		}
	}

	return count, nil
}

// NormalizeScore invoked after scoring all nodes.
func (pl *NodeAffinity) NormalizeScore(ctx context.Context, state *framework.CycleState, pod *v1.Pod, scores framework.NodeScoreList) *framework.Status {
	return pluginhelper.DefaultNormalizeScore(framework.MaxNodeScore, false, scores)
}

// ScoreExtensions of the Score plugin.
func (pl *NodeAffinity) ScoreExtensions() framework.ScoreExtensions {
	return pl
}

// New initializes a new plugin and returns it.
func New(_ runtime.Object, h handle.PodFrameworkHandle) (framework.Plugin, error) {
	return &NodeAffinity{handle: h}, nil
}
