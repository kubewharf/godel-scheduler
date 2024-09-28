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

package podtopologyspread

import (
	"context"
	"fmt"
	"sync/atomic"

	"github.com/kubewharf/godel-scheduler/pkg/binder/framework/handle"
	framework "github.com/kubewharf/godel-scheduler/pkg/framework/api"
	"github.com/kubewharf/godel-scheduler/pkg/plugins/helper"
	"github.com/kubewharf/godel-scheduler/pkg/plugins/podlauncher"
	utils "github.com/kubewharf/godel-scheduler/pkg/plugins/podtopologyspread"
	"github.com/kubewharf/godel-scheduler/pkg/scheduler/apis/config"
	"github.com/kubewharf/godel-scheduler/pkg/scheduler/apis/validation"
	"github.com/kubewharf/godel-scheduler/pkg/util/parallelize"
	podutil "github.com/kubewharf/godel-scheduler/pkg/util/pod"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
)

const (
	Name                                        = "PodTopologySpreadCheck"
	ErrReasonPodTopologySpreadMaxSkewNotMatch   = "pod topology spread constraints not satisfied"
	ErrReasonPodTopologySpreadNodeLabelNotMatch = "node didn't match pod topology spread constraints (missing required label)"
)

type TopologySpreadCondition struct {
	Constraints          []utils.TopologySpreadConstraint
	TpKeyToCriticalPaths map[string]*utils.CriticalPaths
	TpPairToMatchNum     map[utils.TopologyPair]*int32
}

type PodTopologySpreadCheck struct {
	args            config.PodTopologySpreadArgs
	frameworkHandle handle.BinderFrameworkHandle
}

var _ framework.CheckConflictsPlugin = &PodTopologySpreadCheck{}

func (pl *PodTopologySpreadCheck) Name() string {
	return Name
}

func (pl *PodTopologySpreadCheck) CheckConflicts(_ context.Context, cycleState *framework.CycleState, pod *v1.Pod, nodeInfo framework.NodeInfo) *framework.Status {
	podLauncher, status := podlauncher.NodeFits(cycleState, pod, nodeInfo)
	if status != nil {
		return status
	}

	topologySpreadCondition, err := pl.getTopologyCondition(pod, podLauncher)
	if err != nil {
		return framework.NewStatus(framework.Error, err.Error())
	}

	if errReason := pl.isSatisfyPodTopologySpreadConstraints(pod, nodeInfo, topologySpreadCondition, podLauncher); errReason == "" {
		return nil
	} else {
		return framework.NewStatus(framework.Unschedulable, errReason)
	}
}

func New(plArgs runtime.Object, handle handle.BinderFrameworkHandle) (framework.Plugin, error) {
	args, err := utils.GetArgs(plArgs)
	if err != nil {
		return nil, err
	}
	if err := validation.ValidatePodTopologySpreadArgs(&args); err != nil {
		return nil, err
	}
	pl := &PodTopologySpreadCheck{
		args:            args,
		frameworkHandle: handle,
	}

	if len(pl.args.DefaultConstraints) != 0 {
		if handle.SharedInformerFactory() == nil {
			return nil, fmt.Errorf("SharedInformerFactory is nil")
		}
	}

	return pl, nil
}

// defaultConstraints builds the constraints for a pod using
// .DefaultConstraints and the selectors from the services, replication
// controllers, replica sets and stateful sets that match the pod.
func (pl *PodTopologySpreadCheck) defaultConstraints(p *v1.Pod, action v1.UnsatisfiableConstraintAction) ([]utils.TopologySpreadConstraint, error) {
	constraints, err := utils.FilterTopologySpreadConstraints(pl.args.DefaultConstraints, action)
	if err != nil || len(constraints) == 0 {
		return nil, err
	}
	selector := helper.DefaultSelector(p, pl.frameworkHandle.SharedInformerFactory().Core().V1().Services().Lister(),
		pl.frameworkHandle.SharedInformerFactory().Core().V1().ReplicationControllers().Lister(),
		pl.frameworkHandle.SharedInformerFactory().Apps().V1().ReplicaSets().Lister(), pl.frameworkHandle.SharedInformerFactory().Apps().V1().StatefulSets().Lister())
	if selector.Empty() {
		return nil, nil
	}
	for i := range constraints {
		constraints[i].Selector = selector
	}
	return constraints, nil
}

func (pl *PodTopologySpreadCheck) getTopologyCondition(pod *v1.Pod, podLauncher podutil.PodLauncher) (*TopologySpreadCondition, error) {
	var err error
	constraints := []utils.TopologySpreadConstraint{}
	if len(pod.Spec.TopologySpreadConstraints) > 0 {
		// We have feature gating in APIServer to strip the spec
		// so don't need to re-check feature gate, just check length of Constraints.
		constraints, err = utils.FilterTopologySpreadConstraints(pod.Spec.TopologySpreadConstraints, v1.DoNotSchedule)
		if err != nil {
			return nil, fmt.Errorf("obtaining pod's hard topology spread constraints: %v", err)
		}
	} else {
		constraints, err = pl.defaultConstraints(pod, v1.DoNotSchedule)
		if err != nil {
			return nil, fmt.Errorf("setting default hard topology spread constraints: %v", err)
		}
	}
	if len(constraints) == 0 {
		return &TopologySpreadCondition{}, nil
	}

	nodeInfos, err := pl.getAllNodeInfos(podLauncher)
	if err != nil {
		return nil, err
	}
	topologySpreadCondition := TopologySpreadCondition{
		Constraints:          constraints,
		TpKeyToCriticalPaths: make(map[string]*utils.CriticalPaths, len(constraints)),
		TpPairToMatchNum:     make(map[utils.TopologyPair]*int32, utils.SizeHeuristic(len(nodeInfos), constraints)),
	}

	for _, nodeInfo := range nodeInfos {
		nodeLabels := nodeInfo.GetNodeLabels(podLauncher)
		// In accordance to design, if NodeAffinity or NodeSelector is defined,
		// spreading is applied to nodes that pass those filters.
		if !helper.PodMatchesNodeSelectorAndAffinityTerms(pod, nodeInfo) {
			continue
		}
		// Ensure current node's labels contains all topologyKeys in 'Constraints'.
		if !utils.NodeLabelsMatchSpreadConstraints(nodeLabels, constraints) {
			continue
		}
		for _, c := range constraints {
			pair := utils.TopologyPair{Key: c.TopologyKey, Value: nodeLabels[c.TopologyKey]}
			topologySpreadCondition.TpPairToMatchNum[pair] = new(int32)
		}
	}

	processNode := func(i int) {
		nodeInfo := nodeInfos[i]
		nodeLabels := nodeInfo.GetNodeLabels(podLauncher)

		for _, constraint := range constraints {
			pair := utils.TopologyPair{Key: constraint.TopologyKey, Value: nodeLabels[constraint.TopologyKey]}
			tpCount := topologySpreadCondition.TpPairToMatchNum[pair]
			if tpCount == nil {
				continue
			}
			count := utils.CountPodsMatchSelector(nodeInfo.GetPods(), constraint.Selector, pod.Namespace)
			atomic.AddInt32(tpCount, int32(count))
		}
	}
	parallelize.Until(context.Background(), len(nodeInfos), processNode)

	// calculate min match for each topology pair
	for i := 0; i < len(constraints); i++ {
		key := constraints[i].TopologyKey
		topologySpreadCondition.TpKeyToCriticalPaths[key] = utils.NewCriticalPaths()
	}
	for pair, num := range topologySpreadCondition.TpPairToMatchNum {
		topologySpreadCondition.TpKeyToCriticalPaths[pair.Key].Update(pair.Value, *num)
	}

	return &topologySpreadCondition, nil
}

func (pl *PodTopologySpreadCheck) getAllNodeInfos(podLauncher podutil.PodLauncher) ([]framework.NodeInfo, error) {
	if podLauncher == podutil.Kubelet {
		allV1Nodes, err := pl.frameworkHandle.SharedInformerFactory().Core().V1().Nodes().Lister().List(labels.Everything())
		if err != nil {
			return nil, err
		}

		nodeInfos := make([]framework.NodeInfo, 0, len(allV1Nodes))
		for _, node := range allV1Nodes {
			nodeInfos = append(nodeInfos, pl.frameworkHandle.GetNodeInfo(node.Name))
		}

		return nodeInfos, nil
	} else if podLauncher == podutil.NodeManager {
		allNMNodes, err := pl.frameworkHandle.CRDSharedInformerFactory().Node().V1alpha1().NMNodes().Lister().List(labels.Everything())
		if err != nil {
			return nil, err
		}

		nodeInfos := make([]framework.NodeInfo, 0, len(allNMNodes))
		for _, node := range allNMNodes {
			nodeInfos = append(nodeInfos, pl.frameworkHandle.GetNodeInfo(node.Name))
		}

		return nodeInfos, nil
	}
	return nil, fmt.Errorf("unsupported pod launcher: %v", podLauncher)
}

func (pl *PodTopologySpreadCheck) isSatisfyPodTopologySpreadConstraints(pod *v1.Pod, nodeInfo framework.NodeInfo,
	topologySpreadCondition *TopologySpreadCondition, podLauncher podutil.PodLauncher) string {
	if topologySpreadCondition == nil || len(topologySpreadCondition.Constraints) == 0 {
		return ""
	}

	nodeLabels := nodeInfo.GetNodeLabels(podLauncher)
	podLabelSet := labels.Set(pod.Labels)
	for _, c := range topologySpreadCondition.Constraints {
		tpKey := c.TopologyKey
		tpVal, ok := nodeLabels[c.TopologyKey]
		if !ok {
			return ErrReasonPodTopologySpreadNodeLabelNotMatch
		}

		selfMatchNum := int32(0)
		if c.Selector.Matches(podLabelSet) {
			selfMatchNum = 1
		}

		pair := utils.TopologyPair{Key: tpKey, Value: tpVal}
		paths, ok := topologySpreadCondition.TpKeyToCriticalPaths[tpKey]
		if !ok {
			// error which should not happen
			continue
		}
		// judging criteria:
		// 'existing matching num' + 'if self-match (1 or 0)' - 'global min matching num' <= 'maxSkew'
		minMatchNum := paths[0].MatchNum
		matchNum := int32(0)
		if tpCount := topologySpreadCondition.TpPairToMatchNum[pair]; tpCount != nil {
			matchNum = *tpCount
		}
		skew := matchNum + selfMatchNum - minMatchNum
		if skew > c.MaxSkew {
			return ErrReasonPodTopologySpreadMaxSkewNotMatch
		}
	}

	return ""
}
