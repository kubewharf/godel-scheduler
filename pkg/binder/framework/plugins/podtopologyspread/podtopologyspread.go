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
	"github.com/kubewharf/godel-scheduler/pkg/scheduler/apis/config"
	"github.com/kubewharf/godel-scheduler/pkg/scheduler/apis/validation"
	podtopologyspreadScheduler "github.com/kubewharf/godel-scheduler/pkg/scheduler/framework/plugins/podtopologyspread"
	"github.com/kubewharf/godel-scheduler/pkg/util/parallelize"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/informers"
	appslisters "k8s.io/client-go/listers/apps/v1"
	corelisters "k8s.io/client-go/listers/core/v1"
)

const (
	Name                                        = "PodTopologySpreadCheck"
	ErrReasonPodTopologySpreadMaxSkewNotMatch   = "pod topology spread constraints not satisfied"
	ErrReasonPodTopologySpreadNodeLabelNotMatch = "node didn't match pod topology spread constraints (missing required label)"
)

type TopologySpreadCondition struct {
	Constraints          []podtopologyspreadScheduler.TopologySpreadConstraint
	TpKeyToCriticalPaths map[string]*podtopologyspreadScheduler.CriticalPaths
	TpPairToMatchNum     map[podtopologyspreadScheduler.TopologyPair]*int32
}

type PodTopologySpreadCheck struct {
	args             config.PodTopologySpreadArgs
	frameworkHandle  handle.BinderFrameworkHandle
	services         corelisters.ServiceLister
	replicationCtrls corelisters.ReplicationControllerLister
	replicaSets      appslisters.ReplicaSetLister
	statefulSets     appslisters.StatefulSetLister
}

var _ framework.CheckConflictsPlugin = &PodTopologySpreadCheck{}

func (pl *PodTopologySpreadCheck) Name() string {
	return Name
}

func (pl *PodTopologySpreadCheck) CheckConflicts(_ context.Context, cycleState *framework.CycleState, pod *v1.Pod, nodeInfo framework.NodeInfo) *framework.Status {
	topologySpreadCondition, err := pl.getTopologyCondition(pod)
	if err != nil {
		return framework.NewStatus(framework.Error, err.Error())
	}

	if errReason := pl.isSatisfyPodTopologySpreadConstraints(pod, nodeInfo, topologySpreadCondition); errReason == "" {
		return nil
	} else {
		return framework.NewStatus(framework.Unschedulable, errReason)
	}
}

func New(plArgs runtime.Object, handle handle.BinderFrameworkHandle) (framework.Plugin, error) {
	args, err := podtopologyspreadScheduler.GetArgs(plArgs)
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
		pl.setListers(handle.SharedInformerFactory())
	}

	return pl, nil
}

func (pl *PodTopologySpreadCheck) setListers(factory informers.SharedInformerFactory) {
	pl.services = factory.Core().V1().Services().Lister()
	pl.replicationCtrls = factory.Core().V1().ReplicationControllers().Lister()
	pl.replicaSets = factory.Apps().V1().ReplicaSets().Lister()
	pl.statefulSets = factory.Apps().V1().StatefulSets().Lister()
}

// defaultConstraints builds the constraints for a pod using
// .DefaultConstraints and the selectors from the services, replication
// controllers, replica sets and stateful sets that match the pod.
func (pl *PodTopologySpreadCheck) defaultConstraints(p *v1.Pod, action v1.UnsatisfiableConstraintAction) ([]podtopologyspreadScheduler.TopologySpreadConstraint, error) {
	constraints, err := podtopologyspreadScheduler.FilterTopologySpreadConstraints(pl.args.DefaultConstraints, action)
	if err != nil || len(constraints) == 0 {
		return nil, err
	}
	selector := helper.DefaultSelector(p, pl.services, pl.replicationCtrls, pl.replicaSets, pl.statefulSets)
	if selector.Empty() {
		return nil, nil
	}
	for i := range constraints {
		constraints[i].Selector = selector
	}
	return constraints, nil
}

func (pl *PodTopologySpreadCheck) getTopologyCondition(pod *v1.Pod) (*TopologySpreadCondition, error) {
	constraints := []podtopologyspreadScheduler.TopologySpreadConstraint{}
	allNodes, err := pl.frameworkHandle.SharedInformerFactory().Core().V1().Nodes().Lister().List(labels.Everything())
	if err != nil {
		return nil, err
	}
	if len(pod.Spec.TopologySpreadConstraints) > 0 {
		// We have feature gating in APIServer to strip the spec
		// so don't need to re-check feature gate, just check length of Constraints.
		constraints, err = podtopologyspreadScheduler.FilterTopologySpreadConstraints(pod.Spec.TopologySpreadConstraints, v1.DoNotSchedule)
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

	topologySpreadCondition := TopologySpreadCondition{
		Constraints:          constraints,
		TpKeyToCriticalPaths: make(map[string]*podtopologyspreadScheduler.CriticalPaths, len(constraints)),
		TpPairToMatchNum:     make(map[podtopologyspreadScheduler.TopologyPair]*int32, podtopologyspreadScheduler.SizeHeuristic(len(allNodes), constraints)),
	}

	for _, node := range allNodes {
		// In accordance to design, if NodeAffinity or NodeSelector is defined,
		// spreading is applied to nodes that pass those filters.
		if !helper.PodMatchesNodeSelectorAndAffinityTerms(pod, node) {
			continue
		}
		// Ensure current node's labels contains all topologyKeys in 'Constraints'.
		if !podtopologyspreadScheduler.NodeLabelsMatchSpreadConstraints(node.Labels, constraints) {
			continue
		}
		for _, c := range constraints {
			pair := podtopologyspreadScheduler.TopologyPair{Key: c.TopologyKey, Value: node.Labels[c.TopologyKey]}
			topologySpreadCondition.TpPairToMatchNum[pair] = new(int32)
		}
	}

	processNode := func(i int) {
		node := allNodes[i]
		nodeInfo := pl.frameworkHandle.GetNodeInfo(node.Name)

		for _, constraint := range constraints {
			pair := podtopologyspreadScheduler.TopologyPair{Key: constraint.TopologyKey, Value: node.Labels[constraint.TopologyKey]}
			tpCount := topologySpreadCondition.TpPairToMatchNum[pair]
			if tpCount == nil {
				continue
			}
			count := podtopologyspreadScheduler.CountPodsMatchSelector(nodeInfo.GetPods(), constraint.Selector, pod.Namespace)
			atomic.AddInt32(tpCount, int32(count))
		}
	}
	parallelize.Until(context.Background(), len(allNodes), processNode)

	// calculate min match for each topology pair
	for i := 0; i < len(constraints); i++ {
		key := constraints[i].TopologyKey
		topologySpreadCondition.TpKeyToCriticalPaths[key] = podtopologyspreadScheduler.NewCriticalPaths()
	}
	for pair, num := range topologySpreadCondition.TpPairToMatchNum {
		topologySpreadCondition.TpKeyToCriticalPaths[pair.Key].Update(pair.Value, *num)
	}

	return &topologySpreadCondition, nil
}

func (pl *PodTopologySpreadCheck) isSatisfyPodTopologySpreadConstraints(pod *v1.Pod, nodeInfo framework.NodeInfo,
	topologySpreadCondition *TopologySpreadCondition) string {
	if topologySpreadCondition == nil || len(topologySpreadCondition.Constraints) == 0 {
		return ""
	}

	node := nodeInfo.GetNode()
	podLabelSet := labels.Set(pod.Labels)
	for _, c := range topologySpreadCondition.Constraints {
		tpKey := c.TopologyKey
		tpVal, ok := node.Labels[c.TopologyKey]
		if !ok {
			return ErrReasonPodTopologySpreadNodeLabelNotMatch
		}

		selfMatchNum := int32(0)
		if c.Selector.Matches(podLabelSet) {
			selfMatchNum = 1
		}

		pair := podtopologyspreadScheduler.TopologyPair{Key: tpKey, Value: tpVal}
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
