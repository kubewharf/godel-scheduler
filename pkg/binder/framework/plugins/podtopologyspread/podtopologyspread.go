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

package podtopologyspread

import (
	"context"
	"fmt"

	"github.com/kubewharf/godel-scheduler/pkg/binder/framework/handle"
	binderutils "github.com/kubewharf/godel-scheduler/pkg/binder/utils"
	framework "github.com/kubewharf/godel-scheduler/pkg/framework/api"
	"github.com/kubewharf/godel-scheduler/pkg/plugins/helper"
	"github.com/kubewharf/godel-scheduler/pkg/plugins/podlauncher"
	utils "github.com/kubewharf/godel-scheduler/pkg/plugins/podtopologyspread"
	"github.com/kubewharf/godel-scheduler/pkg/scheduler/apis/config"
	"github.com/kubewharf/godel-scheduler/pkg/scheduler/apis/validation"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
)

const (
	Name = "PodTopologySpreadCheck" //Name
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

var _ framework.CheckTopologyPlugin = &PodTopologySpreadCheck{}

func (pl *PodTopologySpreadCheck) Name() string {
	return Name
}

func (pl *PodTopologySpreadCheck) CheckTopology(_ context.Context, cycleState *framework.CycleState, pod *v1.Pod, nodeInfo framework.NodeInfo) *framework.Status {
	podLauncher, status := podlauncher.NodeFits(cycleState, pod, nodeInfo)
	if status != nil {
		return status
	}

	constraints, err := pl.getConstraints(pod)
	if err != nil {
		return framework.NewStatus(framework.Error, err.Error())
	}
	if len(constraints) == 0 {
		return nil
	}

	nodeInfos := pl.frameworkHandle.ListNodeInfos()

	state := utils.GetPreFilterState(pod, nodeInfos, constraints)

	commonState, err := binderutils.ReadCommonState(cycleState)
	if err != nil {
		return framework.NewStatus(framework.Error, err.Error())
	}
	if commonState != nil {
		pl.updateStateByVictims(&state, commonState.VictimsGroupByNode)
	}

	return utils.IsSatisfyPodTopologySpreadConstraints(&state, pod, nodeInfo, podLauncher)
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

func (pl *PodTopologySpreadCheck) getConstraints(pod *v1.Pod) ([]utils.TopologySpreadConstraint, error) {
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

	return constraints, nil
}

func (pl *PodTopologySpreadCheck) updateStateByVictims(state *utils.PreFilterState, victimsGroupByNode map[string]map[types.UID]*v1.Pod) error {
	for nodeName, victimsMap := range victimsGroupByNode {
		nodeInfo := pl.frameworkHandle.GetNodeInfo(nodeName)
		if nodeInfo == nil {
			continue
		}

		launcherToPods, err := binderutils.GroupPodsByLauncher(victimsMap)
		if err != nil {
			return err
		}

		for podLanucher, pods := range launcherToPods {
			nodeLabels := nodeInfo.GetNodeLabels(podLanucher)
			for _, constraint := range state.Constraints {
				pair := utils.TopologyPair{Key: constraint.TopologyKey, Value: nodeLabels[constraint.TopologyKey]}
				tpCount := state.TpPairToMatchNum[pair]
				if state.TpPairToMatchNum[pair] != nil {
					*tpCount = *tpCount - int32(len(pods))
				}
			}
		}
	}

	return nil
}
