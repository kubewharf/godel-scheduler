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

package podtopologyspread

import (
	framework "github.com/kubewharf/godel-scheduler/pkg/framework/api"
	"github.com/kubewharf/godel-scheduler/pkg/plugins/helper"
	utils "github.com/kubewharf/godel-scheduler/pkg/plugins/podtopologyspread"
	podutil "github.com/kubewharf/godel-scheduler/pkg/util/pod"
	v1 "k8s.io/api/core/v1"
)

// defaultConstraints builds the constraints for a pod using
// .DefaultConstraints and the selectors from the services, replication
// controllers, replica sets and stateful sets that match the pod.
func (pl *PodTopologySpread) defaultConstraints(p *v1.Pod, action v1.UnsatisfiableConstraintAction) ([]utils.TopologySpreadConstraint, error) {
	constraints, err := utils.FilterTopologySpreadConstraints(pl.args.DefaultConstraints, action)
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

func getNodeNameByPodLauncher(nodeInfo framework.NodeInfo, podLanucher podutil.PodLauncher) string {
	if nodeInfo == nil {
		return ""
	}

	switch podLanucher {
	case podutil.Kubelet:
		if nodeInfo.GetNode() != nil {
			return nodeInfo.GetNode().Name
		}
	case podutil.NodeManager:
		if nodeInfo.GetNMNode() != nil {
			return nodeInfo.GetNMNode().Name
		}
	}
	return ""
}
