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

package joblevelaffinity

import (
	"context"
	"fmt"

	"github.com/pkg/errors"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/klog/v2"

	framework "github.com/kubewharf/godel-scheduler/pkg/framework/api"
	"github.com/kubewharf/godel-scheduler/pkg/scheduler/framework/handle"
	"github.com/kubewharf/godel-scheduler/pkg/util/helper"
	podutil "github.com/kubewharf/godel-scheduler/pkg/util/pod"
)

const (
	Name = "JobLevelAffinity"
)

type JobLevelAffinity struct {
	handler handle.UnitFrameworkHandle
}

var (
	_ framework.LocatingPlugin = &JobLevelAffinity{}
	_ framework.GroupingPlugin = &JobLevelAffinity{}
)

func New(_ runtime.Object, handler handle.UnitFrameworkHandle) (framework.Plugin, error) {
	return &JobLevelAffinity{handler: handler}, nil
}

func (i *JobLevelAffinity) Name() string {
	return Name
}

func (i *JobLevelAffinity) Locating(ctx context.Context, unit framework.ScheduleUnit, unitCycleState *framework.CycleState, nodeGroup framework.NodeGroup) (framework.NodeGroup, *framework.Status) {
	if unit.Type() == framework.SinglePodUnitType {
		return nodeGroup, nil
	}

	nodeSelector, err := unit.GetAffinityNodeSelector()
	if err != nil {
		return nil, framework.AsStatus(err)
	}
	if nodeSelector == nil {
		return nodeGroup, nil
	}

	klog.InfoS("JobLevelAffinity Locating for ScheduleUnit", "unitKey", unit.GetKey(), "nodeSelector", nodeSelector)

	pods := unit.GetPods()
	podLauncher, err := podutil.GetPodLauncher(pods[0].Pod)
	if err != nil {
		return nil, framework.AsStatus(fmt.Errorf("pod launcher in unit %v is invalid: %v", unit.GetKey(), podLauncher))
	}

	return framework.FilterNodeGroup(nodeGroup, func(ni framework.NodeInfo) bool {
		// MatchFields in NodeSelector is not supported here.
		return helper.MatchNodeSelectorTerms(nodeSelector.NodeSelectorTerms, ni.GetNodeLabels(podLauncher), nil)
	}), nil
}

func (i *JobLevelAffinity) PreparePreferNode(ctx context.Context, unitCycleState, state *framework.CycleState, pod *v1.Pod) *framework.Status {
	return nil
}

func (i *JobLevelAffinity) Grouping(ctx context.Context, unit framework.ScheduleUnit, unitCycleState *framework.CycleState, nodeGroup framework.NodeGroup) ([]framework.NodeGroup, *framework.Status) {
	if unit.Type() == framework.SinglePodUnitType {
		return []framework.NodeGroup{nodeGroup}, nil
	}

	required, _ := unit.GetRequiredAffinity()
	preferred, _ := unit.GetPreferredAffinity()
	if len(required)+len(preferred) == 0 {
		return []framework.NodeGroup{nodeGroup}, nil
	}

	klog.InfoS("JobLevelAffinity Grouping for ScheduleUnit", "unitKey", unit.GetKey(), "requiredAffinity", required, "preferredAffinity", preferred)

	pods := unit.GetPods()
	podLauncher, err := podutil.GetPodLauncher(pods[0].Pod)
	if err != nil {
		return nil, framework.AsStatus(fmt.Errorf("pod launcher in unit %v is invalid: %v", unit.GetKey(), podLauncher))
	}

	assignedNodes := i.getAssignedNodesOfUnit(ctx, unit)

	everScheduled, err := framework.GetEverScheduledState(unitCycleState)
	if err != nil {
		return nil, framework.AsStatus(err)
	}
	nodeGroups, err := i.findNodeGroups(ctx, unit, podLauncher, nodeGroup, assignedNodes, everScheduled)
	if err != nil {
		return nil, framework.AsStatus(err)
	}

	klog.InfoS("JobLevelAffinity Grouping for ScheduleUnit got nodeGroups", "unitKey", unit.GetKey(), "nodeGroups", printNodeGroups(nodeGroups))

	return nodeGroups, nil
}

func (i *JobLevelAffinity) findNodeGroups(
	ctx context.Context,
	unit framework.ScheduleUnit,
	podLauncher podutil.PodLauncher,
	originalNodeGroup framework.NodeGroup,
	assignedNodes sets.String,
	everScheduled bool,
) ([]framework.NodeGroup, error) {
	nodeCircles := originalNodeGroup.GetNodeCircles()
	if len(nodeCircles) == 0 {
		klog.InfoS("No available node circles found in findNodeGroups", "unitKey", unit.GetKey())
		return []framework.NodeGroup{originalNodeGroup}, nil
	}

	// topologyTree is the result of reversing the real topology graph.
	// We build this tree to get the bottom-up level order traversal of the nodes in the original topology graph more easily.
	var topologyTree []*topologyElem
	topologyTree = append(topologyTree, newTopologyElem(nodeCircles[0]))

	minRequest, err := computeUnitMinResourceRequest(unit, everScheduled)
	if err != nil {
		return nil, err
	}

	if required, err := unit.GetRequiredAffinity(); err == nil && len(required) != 0 {
		topologyTree, err = divideNodesByRequireAffinity(ctx, podLauncher, unit, required, assignedNodes, originalNodeGroup, minRequest)
		if err != nil {
			return nil, err
		}
	}

	for _, n := range originalNodeGroup.GetPreferredNodes().List() {
		assignedNodes.Insert(n.GetNodeName())
	}

	if preferred, err := unit.GetPreferredAffinity(); err == nil && len(preferred) != 0 {
		startIndexOfNewElem := 0
		nextStartIndex := len(topologyTree)
		for _, term := range preferred {
			topologyTree, err = divideNodesByPreferAffinity(ctx, podLauncher, unit, term, topologyTree, startIndexOfNewElem, assignedNodes, originalNodeGroup, minRequest)
			if err != nil {
				return nil, err
			}
			startIndexOfNewElem = nextStartIndex
			if startIndexOfNewElem == len(topologyTree) { // no new topologyElems added
				break
			}
			nextStartIndex = len(topologyTree)
		}
	}

	nodeGroups, err := getNodeGroupsFromTree(topologyTree)
	if err != nil {
		return nil, errors.Wrap(err, "failed to get node groups from tree")
	}

	if originPreferredNodes := originalNodeGroup.GetPreferredNodes(); originPreferredNodes != nil {
		for _, nodeGroup := range nodeGroups {
			nodeGroup.SetPreferredNodes(framework.FilterPreferredNodes(originPreferredNodes, func(ni framework.NodeInfo) bool {
				if n, err := nodeGroup.Get(ni.GetNodeName()); err == nil && n != nil {
					return true
				}
				return false
			}))
		}
	}

	return nodeGroups, nil
}

// getAssignedNodesOfUnit returns nodes where running pods in the unit are assigned
func (i *JobLevelAffinity) getAssignedNodesOfUnit(ctx context.Context, unit framework.ScheduleUnit) sets.String {
	unitStatus := i.handler.GetUnitStatus(unit.GetKey())
	pods := unitStatus.GetRunningPods()

	nodeCollection := sets.NewString()
	for _, pod := range pods {
		nodeName := pod.Spec.NodeName
		if len(nodeName) == 0 || nodeCollection.Has(nodeName) {
			continue
		}
		nodeCollection.Insert(nodeName)
	}
	return nodeCollection
}
