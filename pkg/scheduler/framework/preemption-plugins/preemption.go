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

package preemptionplugins

import (
	"fmt"
	"math"

	v1 "k8s.io/api/core/v1"
	"k8s.io/klog/v2"

	framework "github.com/kubewharf/godel-scheduler/pkg/framework/api"
	frameworkutils "github.com/kubewharf/godel-scheduler/pkg/framework/utils"
	preemptionstore "github.com/kubewharf/godel-scheduler/pkg/scheduler/cache/commonstores/preemption_store"
	"github.com/kubewharf/godel-scheduler/pkg/scheduler/framework/handle"
	"github.com/kubewharf/godel-scheduler/pkg/util"
	podutil "github.com/kubewharf/godel-scheduler/pkg/util/pod"
)

// Before performing the real preemption calculation, we perform heuristic check to end the preemption
// process in advance in some scenarios.
// Ref: https://en.wikipedia.org/wiki/Heuristic_(computer_science)
func HeuristicCheck(handle handle.PodFrameworkHandle, pod *v1.Pod, node framework.NodeInfo) bool {
	// 0) Check OccupiableResources
	{
		// We have maintained all pods in node by Splay-Tree.
		// Check the resource dimension quickly by performing the partition operation on the splay,
		// and terminate it in advance when the preemption must fail.
		// More at: pkg/util/splay
		resourceType, _ := podutil.GetPodResourceType(pod)
		priority := GetPodPartitionPriority(pod)
		podResource, _, _ := framework.CalculateResource(pod)
		if !OccupiableResourcesCheck(priority, resourceType, podResource, node) {
			return false
		}
	}
	// ...
	return true
}

// ATTENTION: This function is only used when filtering victim candidates.
// GetPodPartitionPriority returns a priority value based on the current cluster status,
// which needs to be bigger than of all potentially victims's priority.
func GetPodPartitionPriority(pod *v1.Pod) int64 {
	return int64(podutil.GetPodPriority(pod)) + 1
}

func OccupiableResourcesCheck(priority int64, podResourceType podutil.PodResourceType, podResource framework.Resource, node framework.NodeInfo) bool {
	occupiableResource := node.GetOccupiableResources(framework.NewPartitionInfo(math.MinInt64, priority, podResourceType))
	if podResource.MilliCPU > occupiableResource.MilliCPU || podResource.Memory > occupiableResource.Memory {
		return false
	}
	return true
}

// filterVictimsPods groups the given "pods" into two groups of "violatingPods"
// and "nonViolatingPods" based on whether their PDBs will be violated if they are
// preempted.
// This function is stable and does not change the order of received pods. So, if it
// receives a sorted list, grouping will preserve the order of the input list.
func FilterVictimsPods(
	fwh handle.PodFrameworkHandle,
	pfw framework.SchedulerPreemptionFramework,
	state *framework.CycleState,
	preemptionState *framework.CycleState,
	nodeInfo framework.NodeInfo,
	preemptor *v1.Pod,
	priorityLower, priorityUpper int64,
	checked bool,
) (potentialVictims []*v1.Pod) {
	debugModeOnNode := util.GetPodDebugModeOnNode(preemptor, nodeInfo.GetNode(), nodeInfo.GetNMNode())
	nodeName := nodeInfo.GetNodeName()

	podSpecifyVictims, _ := frameworkutils.GetPotentialVictims(preemptor)
	podResourceType, err := framework.GetPodResourceType(state)
	if err != nil {
		return potentialVictims
	}

	if !checked {
		if err := pfw.RunNodePrePreemptingPlugins(preemptor, nodeInfo, state, preemptionState); err != nil {
			return potentialVictims
		}
	}

	var preemptionStoreHandle preemptionstore.StoreHandle
	if ins := fwh.FindStore(preemptionstore.Name); ins != nil {
		preemptionStoreHandle = ins.(preemptionstore.StoreHandle)
	}

	for _, pi := range nodeInfo.GetVictimCandidates(framework.NewPartitionInfo(priorityLower, priorityUpper, podResourceType)) {
		pod := pi.Pod
		podKey := podutil.GeneratePodKey(pod)
		if preemptors := preemptionStoreHandle.GetPreemptorsByVictim(nodeName, podKey); len(preemptors) > 0 {
			klog.InfoS("WARN: Failed to select the pod as a victim for the new preemptor as it was already a victim of others",
				"pod", klog.KObj(pod), "preemptor", klog.KObj(preemptor), "otherPreemptors", preemptors)
			continue
		}
		code, msg := func() (framework.Code, string) {
			// TODO: (godel)avoid preempting AM pods.
			// for BE pods:victims can only come from the pods belonging to specific applications.
			if !isPodInSpecifiedVictimList(pod, podSpecifyVictims) {
				return framework.Error, "victim is not in specific victim list"
			}

			victimState := framework.NewVictimState()
			code, msg := pfw.RunVictimSearchingPlugins(preemptor, pi, state, preemptionState, victimState)
			switch code {
			case framework.PreemptionFail:
				return code, msg
			case framework.PreemptionSucceed:
			default:
				return framework.PreemptionFail, fmt.Sprintf("not support plugin result %s", code)
			}
			postPreemptRes := pfw.RunPostVictimSearchingPlugins(preemptor, pi, state, preemptionState, victimState)
			if !postPreemptRes.IsSuccess() {
				return postPreemptRes.Code(), postPreemptRes.Message()
			}
			return framework.PreemptionSucceed, ""
		}()
		if code != framework.PreemptionSucceed {
			if debugModeOnNode {
				klog.ErrorS(nil, "DEBUG: Failed to preempt victim on certain node", "preemptor", klog.KObj(preemptor), "pod", klog.KObj(pod), "podUID", pod.GetUID(), "node", nodeInfo.GetNodeName(), "reason", msg)
			}
			continue
		}

		potentialVictims = append(potentialVictims, pod)
	}
	return potentialVictims
}

func isPodInSpecifiedVictimList(pod *v1.Pod, specifiedVictimList *framework.PotentialVictims) bool {
	if specifiedVictimList == nil {
		// pod do not specify victim application list, return true to skip.
		return true
	}
	podAppName := podutil.GetBestEffortPodAppName(pod)
	// if podAppName is empty, means the pod is not BE pod or has no specified victims.
	if len(podAppName) == 0 {
		// no application limit, return true
		return true
	}
	for _, victim := range *specifiedVictimList {
		if podAppName == victim.Application {
			return true
		}
	}
	return false
}
