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
	interpodScheduler "github.com/kubewharf/godel-scheduler/pkg/scheduler/framework/plugins/interpodaffinity"
	podutil "github.com/kubewharf/godel-scheduler/pkg/util/pod"
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
	podLauncher, err := podutil.GetPodLauncher(pod)
	if err != nil {
		return framework.NewStatus(framework.Error, err.Error())
	}
	topologyLabels := nodeInfo.GetNodeLabels(podLauncher)
	matchedNodeInfos, err := pl.getNodesWithSameTopologyLabels(topologyLabels)
	if err != nil {
		return framework.NewStatus(framework.Unschedulable, ErrorReasonWhenFilterNodeWithSameTopology)
	}

	existingPodAntiAffinityMap := interpodScheduler.GetTPMapMatchingExistingAntiAffinity(pod, matchedNodeInfos, podLauncher)

	podInfo := framework.NewPodInfo(pod)
	incomingPodAffinityMap, incomingPodAntiAffinityMap := interpodScheduler.GetTPMapMatchingIncomingAffinityAntiAffinity(podInfo, matchedNodeInfos, podLauncher)

	state := &interpodScheduler.PreFilterState{
		TopologyToMatchedExistingAntiAffinityTerms: existingPodAntiAffinityMap,
		TopologyToMatchedAffinityTerms:             incomingPodAffinityMap,
		TopologyToMatchedAntiAffinityTerms:         incomingPodAntiAffinityMap,
		PodInfo:                                    podInfo,
	}

	if !interpodScheduler.SatisfyPodAffinity(state, nodeInfo, podLauncher) {
		return framework.NewStatus(framework.UnschedulableAndUnresolvable, interpodScheduler.ErrReasonAffinityNotMatch, interpodScheduler.ErrReasonAffinityRulesNotMatch)
	}

	if !interpodScheduler.SatisfyPodAntiAffinity(state, nodeInfo, podLauncher) {
		return framework.NewStatus(framework.Unschedulable, interpodScheduler.ErrReasonAffinityNotMatch, interpodScheduler.ErrReasonAntiAffinityRulesNotMatch)
	}

	if !interpodScheduler.SatisfyExistingPodsAntiAffinity(state, nodeInfo, podLauncher) {
		return framework.NewStatus(framework.Unschedulable, interpodScheduler.ErrReasonAffinityNotMatch, interpodScheduler.ErrReasonExistingAntiAffinityRulesNotMatch)
	}

	return nil
}

func New(_ runtime.Object, handle handle.BinderFrameworkHandle) (framework.Plugin, error) {
	return &InterPodAffinity{
		frameworkHandle: handle,
	}, nil
}

func (pl *InterPodAffinity) getNodesWithSameTopologyLabels(topologyLabels map[string]string) ([]framework.NodeInfo, error) {
	nodeLister := pl.frameworkHandle.SharedInformerFactory().Core().V1().Nodes().Lister()

	var matchedNodeInfos []framework.NodeInfo
	nodeSet := make(map[string]*v1.Node) // Used to remove duplicates

	// 针对每个 label key-value 进行筛选，并合并结果
	for key, value := range topologyLabels {
		selector := labels.NewSelector()

		// 为每个 label key-value 创建一个筛选条件
		requirement, _ := labels.NewRequirement(key, selection.Equals, []string{value})
		selector = selector.Add(*requirement)

		// 获取符合条件的节点
		nodes, err := nodeLister.List(selector)
		if err != nil {
			return nil, fmt.Errorf("failed to list nodes for selector %s: %v", selector.String(), err)
		}

		// 将筛选结果加入到 nodeSet 中，确保不重复添加节点
		for _, node := range nodes {
			nodeSet[node.Name] = node
		}
	}

	// 将去重后的节点列表转为切片
	for _, node := range nodeSet {
		nodeInfo := pl.frameworkHandle.GetNodeInfo(node.Name)
		matchedNodeInfos = append(matchedNodeInfos, nodeInfo)
	}

	return matchedNodeInfos, nil
}
