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

package interpodaffinity

import (
	"context"
	"fmt"

	framework "github.com/kubewharf/godel-scheduler/pkg/framework/api"
	utils "github.com/kubewharf/godel-scheduler/pkg/plugins/interpodaffinity"
	"github.com/kubewharf/godel-scheduler/pkg/plugins/podlauncher"
	v1 "k8s.io/api/core/v1"
)

const (
	// preFilterStateKey is the key in CycleState to InterPodAffinity pre-computed data for Filtering.
	// Using the name of the plugin will likely help us avoid collisions with other plugins.
	preFilterStateKey = "PreFilter" + Name
)

// PreFilter invoked at the prefilter extension point.
func (pl *InterPodAffinity) PreFilter(ctx context.Context, cycleState *framework.CycleState, pod *v1.Pod) *framework.Status {
	var allNodes []framework.NodeInfo
	var nodesWithRequiredAntiAffinityPods []framework.NodeInfo
	allNodes = pl.sharedLister.NodeInfos().List()
	nodesWithRequiredAntiAffinityPods = pl.sharedLister.NodeInfos().HavePodsWithRequiredAntiAffinityList()

	podInfo := framework.NewPodInfo(pod)
	if podInfo.ParseError != nil {
		return framework.NewStatus(framework.UnschedulableAndUnresolvable, fmt.Sprintf("parsing pod: %+v", podInfo.ParseError))
	}

	// existingPodAntiAffinityMap will be used later for efficient check on existing pods' anti-affinity
	existingPodAntiAffinityMap := utils.GetTPMapMatchingExistingAntiAffinity(pod, nodesWithRequiredAntiAffinityPods)

	// incomingPodAffinityMap will be used later for efficient check on incoming pod's affinity
	// incomingPodAntiAffinityMap will be used later for efficient check on incoming pod's anti-affinity
	incomingPodAffinityMap, incomingPodAntiAffinityMap := utils.GetTPMapMatchingIncomingAffinityAntiAffinity(podInfo, allNodes)

	s := &utils.PreFilterState{
		TopologyToMatchedAffinityTerms:             incomingPodAffinityMap,
		TopologyToMatchedAntiAffinityTerms:         incomingPodAntiAffinityMap,
		TopologyToMatchedExistingAntiAffinityTerms: existingPodAntiAffinityMap,
		PodInfo: podInfo,
	}

	cycleState.Write(preFilterStateKey, s)
	return nil
}

// PreFilterExtensions returns prefilter extensions, pod add and remove.
func (pl *InterPodAffinity) PreFilterExtensions() framework.PreFilterExtensions {
	return pl
}

// AddPod from pre-computed data in cycleState.
func (pl *InterPodAffinity) AddPod(ctx context.Context, cycleState *framework.CycleState, podToSchedule *v1.Pod, podToAdd *v1.Pod, nodeInfo framework.NodeInfo) *framework.Status {
	state, err := getPreFilterState(cycleState)
	if err != nil {
		return framework.NewStatus(framework.Error, err.Error())
	}
	state.UpdateWithPod(podToAdd, nodeInfo, 1)
	return nil
}

// RemovePod from pre-computed data in cycleState.
func (pl *InterPodAffinity) RemovePod(ctx context.Context, cycleState *framework.CycleState, podToSchedule *v1.Pod, podToRemove *v1.Pod, nodeInfo framework.NodeInfo) *framework.Status {
	state, err := getPreFilterState(cycleState)
	if err != nil {
		return framework.NewStatus(framework.Error, err.Error())
	}
	state.UpdateWithPod(podToRemove, nodeInfo, -1)
	return nil
}

func getPreFilterState(cycleState *framework.CycleState) (*utils.PreFilterState, error) {
	c, err := cycleState.Read(preFilterStateKey)
	if err != nil {
		// utils.PreFilterState doesn't exist, likely PreFilter wasn't invoked.
		return nil, fmt.Errorf("error reading %q from cycleState: %v", preFilterStateKey, err)
	}

	s, ok := c.(*utils.PreFilterState)
	if !ok {
		return nil, fmt.Errorf("%+v  convert to interpodaffinity.state error", c)
	}
	return s, nil
}

// Filter invoked at the filter extension point.
// It checks if a pod can be scheduled on the specified node with pod affinity/anti-affinity configuration.
func (pl *InterPodAffinity) Filter(ctx context.Context, cycleState *framework.CycleState, pod *v1.Pod, nodeInfo framework.NodeInfo) *framework.Status {
	podLauncher, status := podlauncher.NodeFits(cycleState, pod, nodeInfo)
	if status != nil {
		return status
	}

	state, err := getPreFilterState(cycleState)
	if err != nil {
		return framework.NewStatus(framework.Error, err.Error())
	}

	if !utils.SatisfyPodAffinity(state, nodeInfo, podLauncher) {
		return framework.NewStatus(framework.UnschedulableAndUnresolvable, utils.ErrReasonAffinityNotMatch, utils.ErrReasonAffinityRulesNotMatch)
	}

	if !utils.SatisfyPodAntiAffinity(state, nodeInfo, podLauncher) {
		return framework.NewStatus(framework.Unschedulable, utils.ErrReasonAffinityNotMatch, utils.ErrReasonAntiAffinityRulesNotMatch)
	}

	if !utils.SatisfyExistingPodsAntiAffinity(state, nodeInfo, podLauncher) {
		return framework.NewStatus(framework.Unschedulable, utils.ErrReasonAffinityNotMatch, utils.ErrReasonExistingAntiAffinityRulesNotMatch)
	}

	return nil
}
