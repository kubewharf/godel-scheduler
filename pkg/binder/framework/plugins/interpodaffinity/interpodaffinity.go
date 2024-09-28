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

package interpodaffinity

import (
	"context"
	"fmt"

	"github.com/kubewharf/godel-scheduler/pkg/binder/framework/handle"
	framework "github.com/kubewharf/godel-scheduler/pkg/framework/api"
	utils "github.com/kubewharf/godel-scheduler/pkg/plugins/interpodaffinity"
	"github.com/kubewharf/godel-scheduler/pkg/plugins/podlauncher"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/selection"
)

const (
	Name                                      = "InterPodAffinityCheck"
	ErrorReasonWhenFilterNodeWithSameTopology = "failed to get nodes with same topology labels"
)

type InterPodAffinity struct {
	frameworkHandle handle.BinderFrameworkHandle
}

var _ framework.CheckConflictsPlugin = &InterPodAffinity{}

func (pl *InterPodAffinity) Name() string {
	return Name
}

func (pl *InterPodAffinity) CheckConflicts(_ context.Context, cycleState *framework.CycleState, pod *v1.Pod, nodeInfo framework.NodeInfo) *framework.Status {
	// Get the nodes with the same topology labels as the node to be scheduled
	podLauncher, status := podlauncher.NodeFits(nil, pod, nodeInfo)
	if status != nil {
		return status
	}
	topologyLabels := nodeInfo.GetNodeLabels(podLauncher)
	matchedNodeInfos, err := pl.getNodesWithSameTopologyLabels(topologyLabels)
	if err != nil {
		return framework.NewStatus(framework.Unschedulable, ErrorReasonWhenFilterNodeWithSameTopology)
	}

	existingPodAntiAffinityMap := utils.GetTPMapMatchingExistingAntiAffinity(pod, matchedNodeInfos)

	podInfo := framework.NewPodInfo(pod)
	incomingPodAffinityMap, incomingPodAntiAffinityMap := utils.GetTPMapMatchingIncomingAffinityAntiAffinity(podInfo, matchedNodeInfos)

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

func (pl *InterPodAffinity) getNodesWithSameTopologyLabels(topologyLabels map[string]string) ([]framework.NodeInfo, error) {
	var matchedNodeInfos []framework.NodeInfo
	nodeInfoSet := make(map[string]framework.NodeInfo) // Used to remove duplicates

	for key, value := range topologyLabels {
		selector := labels.NewSelector()

		requirement, _ := labels.NewRequirement(key, selection.Equals, []string{value})
		selector = selector.Add(*requirement)

		if pl.frameworkHandle.SharedInformerFactory() != nil {
			nodes, err := pl.frameworkHandle.SharedInformerFactory().Core().V1().Nodes().Lister().List(selector)
			if err != nil {
				return nil, fmt.Errorf("failed to list nodes for selector %s: %v", selector.String(), err)
			}
			for _, node := range nodes {
				nodeInfo := pl.frameworkHandle.GetNodeInfo(node.Name)
				nodeInfoSet[nodeInfo.GetNodeName()] = nodeInfo
			}
		}

		if pl.frameworkHandle.CRDSharedInformerFactory() != nil {
			nmNodes, err := pl.frameworkHandle.CRDSharedInformerFactory().Node().V1alpha1().NMNodes().Lister().List(selector)
			if err != nil {
				return nil, fmt.Errorf("failed to list nodes for selector %s: %v", selector.String(), err)
			}
			for _, nmNode := range nmNodes {
				nodeInfo := pl.frameworkHandle.GetNodeInfo(nmNode.Name)
				nodeInfoSet[nodeInfo.GetNodeName()] = nodeInfo
			}
		}
	}
	for _, nodeInfo := range nodeInfoSet {
		matchedNodeInfos = append(matchedNodeInfos, nodeInfo)
	}

	return matchedNodeInfos, nil
}
