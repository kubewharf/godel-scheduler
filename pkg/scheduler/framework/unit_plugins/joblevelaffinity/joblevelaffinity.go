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

	nodeGroups, err := i.findNodeGroups(ctx, unit, podLauncher, nodeGroup, assignedNodes)
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
	originNodeGroup framework.NodeGroup,
	assignedNodes sets.String,
) ([]framework.NodeGroup, error) {
	nodeCircles := originNodeGroup.GetNodeCircles()
	if len(nodeCircles) == 0 {
		klog.InfoS("No available node circles found in findNodeGroups", "unitKey", unit.GetKey())
		return []framework.NodeGroup{originNodeGroup}, nil
	}
	originalNodeCircle := nodeCircles[0]

	// nodeCircleTree is the result of reversing the real topology graph.
	// We build this tree to get the bottom-up level order traversal of the nodes in the original topology graph more easily.
	var nodeCircleTree []*nodeCircleElem
	nodeCircleTree = append(nodeCircleTree, newNodeCircleElem(originalNodeCircle))

	if required, err := unit.GetRequiredAffinity(); err == nil && len(required) != 0 {
		nodeCircleTree, err = findNodeCirclesByRequireAffinity(ctx, podLauncher, unit, required, originalNodeCircle, assignedNodes, originalNodeCircle)
		if err != nil {
			return nil, err
		}
	}

	if preferred, err := unit.GetPreferredAffinity(); err == nil && len(preferred) != 0 {
		startIndexOfNewElem := 0
		nextStartIndex := len(nodeCircleTree)
		for _, term := range preferred {
			nodeCircleTree, err = findNodeGroupsByPreferAffinity(ctx, podLauncher, unit, term, nodeCircleTree, startIndexOfNewElem, assignedNodes, originalNodeCircle)
			if err != nil {
				return nil, err
			}
			startIndexOfNewElem = nextStartIndex
			if startIndexOfNewElem == len(nodeCircleTree) { // this happens only when there are running pods which are, however, in different preferred topology areas
				break
			}
			nextStartIndex = len(nodeCircleTree)
		}
	}

	nodeGroups, err := getNodeGroupsFromTree(nodeCircleTree)
	if err != nil {
		return nil, errors.Wrap(err, "failed to get node pools from tree")
	}

	if originPreferredNodes := originNodeGroup.GetPreferredNodes(); originPreferredNodes != nil {
		for _, nodeGroup := range nodeGroups {
			nodeGroup.SetPreferredNodes(framework.FilterPreferredNodes(originPreferredNodes, func(ni framework.NodeInfo) bool {
				for _, nodeCircle := range nodeGroup.GetNodeCircles() {
					if n, err := nodeCircle.Get(ni.GetNodeName()); err == nil && n != nil {
						return true
					}
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
