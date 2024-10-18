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

package rescheduling

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/kubewharf/godel-scheduler/pkg/features"
	framework "github.com/kubewharf/godel-scheduler/pkg/framework/api"
	movementstore "github.com/kubewharf/godel-scheduler/pkg/scheduler/cache/commonstores/movement_store"
	"github.com/kubewharf/godel-scheduler/pkg/scheduler/framework/handle"
	"github.com/kubewharf/godel-scheduler/pkg/scheduler/metrics"
	podutil "github.com/kubewharf/godel-scheduler/pkg/util/pod"

	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/sets"
	utilfeature "k8s.io/apiserver/pkg/util/feature"
	"k8s.io/klog/v2"
)

const (
	Name         = "Rescheduling"
	unitStateKey = "UnitState" + Name
)

type Rescheduling struct {
	handler      handle.UnitFrameworkHandle
	pluginHandle movementstore.StoreHandle
}

var (
	_ framework.LocatingPlugin      = &Rescheduling{}
	_ framework.PreferNodeExtension = &Rescheduling{}
)

func New(_ runtime.Object, handle handle.UnitFrameworkHandle) (framework.Plugin, error) {
	var pluginHandle movementstore.StoreHandle
	if ins := handle.FindStore(movementstore.Name); ins != nil {
		pluginHandle = ins.(movementstore.StoreHandle)
	}
	return &Rescheduling{handler: handle, pluginHandle: pluginHandle}, nil
}

func (i *Rescheduling) Name() string {
	return Name
}

func (i *Rescheduling) Locating(ctx context.Context, unit framework.ScheduleUnit, unitCycleState *framework.CycleState, nodeGroup framework.NodeGroup) (framework.NodeGroup, *framework.Status) {
	if !utilfeature.DefaultFeatureGate.Enabled(features.SupportRescheduling) {
		return nodeGroup, nil
	}

	pods := unit.GetPods()
	podOwners := map[string]int{}
	for _, p := range pods {
		podOwner := podutil.GetPodOwnerInfoKey(p.Pod)
		if podOwner == podutil.GeneratePodKey(p.Pod) {
			continue
		}
		if len(podOwner) > 0 {
			podOwners[podOwner]++
		}
	}
	if len(podOwners) == 0 {
		return nodeGroup, nil
	}
	klog.InfoS("Rescheduling Locating for ScheduleUnit", "unitKey", unit.GetKey(), "podOwners", podOwners)

	preferredNodes := nodeGroup.GetPreferredNodes()
	index := make(map[string]*ownerRecommondations, len(podOwners))
	now := time.Now()

	for podOwner := range podOwners {
		suggestedMovementNodes := i.pluginHandle.GetSuggestedMovementAndNodes(podOwner)
		if len(suggestedMovementNodes) == 0 {
			continue
		}

		ownerRecommondations := newOwnerRecommondations()
		for nodeName, suggestions := range suggestedMovementNodes {
			nodeInfo, err := nodeGroup.Get(nodeName)
			if err != nil || nodeInfo == nil {
				continue
			}

			validSuggestions := []*framework.MovementDetailOnNode{}
			for _, suggestion := range suggestions {
				if suggestion.AvailableCount <= 0 {
					continue
				}
				movementName := suggestion.MovementName
				if nodeInfoHasDeletedPod(nodeInfo, i.pluginHandle.GetDeletedPodsFromMovement(movementName)) {
					if now.After(suggestion.CreationTimestamp.Add(i.handler.GetMaxWaitingDeletionDuration())) {
						suggestion.WaitingTimeout = true
						validSuggestions = append(validSuggestions, suggestion)
					} else {
						ownerRecommondations.waitingPodCount += int(suggestion.AvailableCount)
					}
				} else {
					validSuggestions = append(validSuggestions, suggestion)
				}
			}
			if len(validSuggestions) == 0 {
				continue
			}

			// ATTENTION
			// 1. Add to `preferredNodes` anyway.
			// 2. Add to `nodeToMovements` if and only if there are valid recommendations.
			preferredNodes.Add(nodeInfo, i)
			ownerRecommondations.nodeToMovements[nodeName] = validSuggestions
		}
		index[podOwner] = ownerRecommondations
	}

	// Store the index.
	unitCycleState.Write(unitStateKey, &unitState{index: index})

	return nodeGroup, nil
}

func (i *Rescheduling) PreparePreferNode(ctx context.Context, unitCycleState, state *framework.CycleState, pod *v1.Pod) *framework.Status {
	if !utilfeature.DefaultFeatureGate.Enabled(features.SupportRescheduling) {
		return nil
	}

	unitState, err := getUnitState(unitCycleState)
	if err != nil || unitState.index == nil {
		return nil
	}

	podTemplateKey := podutil.GetPodOwnerInfoKey(pod)
	ownerRecommondations, ok := unitState.index[podTemplateKey]
	if !ok || ownerRecommondations == nil {
		return nil
	}

	notScheduledPodCount, err := framework.GetNotScheduledPodKeysCountByTemplate(unitCycleState, podTemplateKey)
	if err != nil {
		return framework.NewStatus(framework.Error, err.Error())
	}
	if notScheduledPodCount > ownerRecommondations.waitingPodCount {
		return nil
	} else if time.Now().After(pod.CreationTimestamp.Add(i.handler.GetMaxWaitingDeletionDuration())) {
		return nil
	}
	onlyExecuteScheduleInPreferredNodes(state)
	return nil
}

func (i *Rescheduling) PrePreferNode(ctx context.Context, unitCycleState, podCycleState *framework.CycleState, pod *v1.Pod, nodeInfo framework.NodeInfo) (framework.NodeInfo, *framework.CycleState, *framework.Status) {
	unitState, err := getUnitState(unitCycleState)
	if err != nil || unitState.index == nil {
		return nodeInfo, podCycleState, framework.NewStatus(framework.Error, "failed to getUnitState for Rescheduling")
	}
	// Reset choosedMovement.
	unitState.choosedMovement = ""
	unitState.algorithmName = ""

	podTemplateKey := podutil.GetPodOwnerInfoKey(pod)
	ownerRecommondations, ok := unitState.index[podTemplateKey]
	if !ok || ownerRecommondations == nil {
		return nodeInfo, podCycleState, framework.NewStatus(framework.Error, fmt.Sprintf("empty nodeToMovements for owner(%s)", podTemplateKey))
	}
	movements, ok := ownerRecommondations.nodeToMovements[nodeInfo.GetNodeName()]
	if !ok || len(movements) == 0 {
		return nodeInfo, podCycleState, framework.NewStatus(framework.Error, fmt.Sprintf("empty movements for owner(%s) on node(%s)", podTemplateKey, nodeInfo.GetNodeName()))
	}
	for _, movement := range movements {
		// TODO: double check this logic with yuquan, xinyi.
		// What if the preference node is recommended to be fully used and there are resources available?
		// The preferred nodes are no longer in other node circles and cannot be used anymore.
		if movement.AvailableCount <= 0 {
			continue
		}
		pod.Annotations[podutil.MovementNameKey] = movement.MovementName
		unitState.choosedMovement = movement.MovementName
		unitState.algorithmName = movement.AlgorithmName
		if movement.WaitingTimeout {
			continue
		}
		return nodeInfo, podCycleState, nil
	}
	// TODO: double check this logic with yuquan, xinyi.
	// Return nil with empty `choosedMovement` or just return error?
	return nodeInfo, podCycleState, framework.NewStatus(framework.Error, fmt.Sprintf("no valid movements for owner(%s) on node(%s)", podTemplateKey, nodeInfo.GetNodeName()))
}

func (i *Rescheduling) PostPreferNode(ctx context.Context, unitCycleState, podCycleState *framework.CycleState, pod *v1.Pod, nodeInfo framework.NodeInfo, status *framework.Status) *framework.Status {
	unitState, err := getUnitState(unitCycleState)
	if err != nil || unitState.index == nil {
		return framework.NewStatus(framework.Error, "failed to getUnitState for Rescheduling")
	}

	if status.IsSuccess() {
		if len(unitState.choosedMovement) > 0 {
			ownerRecommondations := unitState.index[podutil.GetPodOwnerInfoKey(pod)]
			movements := ownerRecommondations.nodeToMovements[nodeInfo.GetNodeName()]
			for _, movement := range movements {
				if movement.MovementName == unitState.choosedMovement {
					movement.AvailableCount--
					break
				}
			}
		}
		metrics.ObservePodsUseMovement(unitState.algorithmName, "succeed", "nil", i.handler.SchedulerName())
	} else {
		metrics.ObservePodsUseMovement(unitState.algorithmName, "fail", formatMetricsTagValue(status.Message()), i.handler.SchedulerName())
	}
	return nil
}

func formatMetricsTagValue(msg string) string {
	msg = strings.ReplaceAll(msg, " ", "_")
	msg = strings.ReplaceAll(msg, ",", "_")
	msg = strings.ReplaceAll(msg, ";", "_")
	msg = strings.ReplaceAll(msg, ":", "_")
	msg = strings.ReplaceAll(msg, "(", "/")
	msg = strings.ReplaceAll(msg, ")", "/")
	msg = strings.ReplaceAll(msg, "'", "/")
	msg = strings.ReplaceAll(msg, "{", "/")
	msg = strings.ReplaceAll(msg, "}", "/")
	msg = strings.ReplaceAll(msg, "[", "/")
	msg = strings.ReplaceAll(msg, "]", "/")
	if len(msg) < 1 {
		msg = "nil"
	} else if len(msg) > 255 {
		msg = msg[0:255]
	}
	return msg
}

func nodeInfoHasDeletedPod(nodeInfo framework.NodeInfo, deletedPodSet sets.String) bool {
	for _, pInfo := range nodeInfo.GetPods() {
		podKey := podutil.GeneratePodKey(pInfo.Pod)
		if deletedPodSet.Has(podKey) {
			return true
		}
	}
	return false
}

func onlyExecuteScheduleInPreferredNodes(podState *framework.CycleState) {
	framework.SetPodSchedulingStageInCycleState(podState, framework.ScheduleInPreferredNodes, true)
	framework.SetPodSchedulingStageInCycleState(podState, framework.ScheduleInNodeCircles, false)
	framework.SetPodSchedulingStageInCycleState(podState, framework.PreemptInPreferredNodes, false)
	framework.SetPodSchedulingStageInCycleState(podState, framework.PreemptInNodeCircles, false)
}
