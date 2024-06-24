/*
Copyright 2018 The Kubernetes Authors.

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

package nodeunschedulable

import (
	"context"

	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"

	framework "github.com/kubewharf/godel-scheduler/pkg/framework/api"
	"github.com/kubewharf/godel-scheduler/pkg/plugins/podlauncher"
	"github.com/kubewharf/godel-scheduler/pkg/scheduler/framework/handle"
	"github.com/kubewharf/godel-scheduler/pkg/util/helper"
	podutil "github.com/kubewharf/godel-scheduler/pkg/util/pod"
)

// NodeUnschedulable plugin filters nodes that set node.Spec.Unschedulable=true unless
// the pod tolerates {key=node.kubernetes.io/unschedulable, effect:NoSchedule} taint.
type NodeUnschedulable struct{}

var _ framework.FilterPlugin = &NodeUnschedulable{}

// Name is the name of the plugin used in the plugin registry and configurations.
const Name = "NodeUnschedulable"

const (
	// ErrReasonUnknownCondition is used for NodeUnknownCondition predicate error.
	ErrReasonUnknownCondition = "node(s) had unknown conditions"
	// ErrReasonUnschedulable is used for NodeUnschedulable predicate error.
	ErrReasonUnschedulable = "node(s) were unschedulable"
)

// Name returns name of the plugin. It is used in logs, etc.
func (pl *NodeUnschedulable) Name() string {
	return Name
}

// Filter invoked at the filter extension point.
func (pl *NodeUnschedulable) Filter(ctx context.Context, state *framework.CycleState, pod *v1.Pod, nodeInfo framework.NodeInfo) *framework.Status {
	launcher, status := podlauncher.NodeFits(state, pod, nodeInfo)
	if status != nil {
		return status
	}

	switch launcher {
	case podutil.Kubelet:
		// If pod tolerate unschedulable taint, it's also tolerate `node.Spec.Unschedulable`.
		podToleratesUnschedulable := helper.TolerationsTolerateTaint(pod.Spec.Tolerations, &v1.Taint{
			Key:    v1.TaintNodeUnschedulable,
			Effect: v1.TaintEffectNoSchedule,
		})

		// TODO (k82cn): deprecates `node.Spec.Unschedulable` in 1.13.
		if nodeInfo.GetNode().Spec.Unschedulable && !podToleratesUnschedulable {
			return framework.NewStatus(framework.UnschedulableAndUnresolvable, ErrReasonUnschedulable)
		}
	case podutil.NodeManager:
		nmNode := nodeInfo.GetNMNode()
		if nmNode == nil {
			return framework.NewStatus(framework.UnschedulableAndUnresolvable, ErrReasonUnknownCondition)
		}
	}
	return nil
}

// New initializes a new plugin and returns it.
func New(_ runtime.Object, _ handle.PodFrameworkHandle) (framework.Plugin, error) {
	return &NodeUnschedulable{}, nil
}
