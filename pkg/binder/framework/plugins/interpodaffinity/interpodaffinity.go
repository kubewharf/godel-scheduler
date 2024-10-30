/*
Copyright 2024 The Godel Scheduler Authors.

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

	"github.com/kubewharf/godel-scheduler/pkg/binder/framework/handle"
	framework "github.com/kubewharf/godel-scheduler/pkg/framework/api"
	utils "github.com/kubewharf/godel-scheduler/pkg/plugins/interpodaffinity"
	"github.com/kubewharf/godel-scheduler/pkg/plugins/podlauncher"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

const (
	Name                                      = "InterPodAffinityCheck"
	ErrorReasonWhenFilterNodeWithSameTopology = "failed to get nodes with same topology labels"
)

type InterPodAffinity struct {
	frameworkHandle handle.BinderFrameworkHandle
}

var _ framework.CheckTopologyPlugin = &InterPodAffinity{}

func (pl *InterPodAffinity) Name() string {
	return Name
}

func (pl *InterPodAffinity) CheckTopology(_ context.Context, cycleState *framework.CycleState, pod *v1.Pod, nodeInfo framework.NodeInfo) *framework.Status {
	// Get the nodes with the same topology labels as the node to be scheduled
	podLauncher, status := podlauncher.NodeFits(nil, pod, nodeInfo)
	if status != nil {
		return status
	}

	nodeInfos := pl.frameworkHandle.ListNodeInfos()

	existingPodAntiAffinityMap := utils.GetTPMapMatchingExistingAntiAffinity(pod, nodeInfos)

	podInfo := framework.NewPodInfo(pod)
	incomingPodAffinityMap, incomingPodAntiAffinityMap := utils.GetTPMapMatchingIncomingAffinityAntiAffinity(podInfo, nodeInfos)

	state := &utils.PreFilterState{
		TopologyToMatchedExistingAntiAffinityTerms: existingPodAntiAffinityMap,
		TopologyToMatchedAffinityTerms:             incomingPodAffinityMap,
		TopologyToMatchedAntiAffinityTerms:         incomingPodAntiAffinityMap,
		PodInfo:                                    podInfo,
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

func New(_ runtime.Object, handle handle.BinderFrameworkHandle) (framework.Plugin, error) {
	return &InterPodAffinity{
		frameworkHandle: handle,
	}, nil
}
