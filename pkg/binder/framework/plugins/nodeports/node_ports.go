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

package nodeports

import (
	"context"

	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"

	"github.com/kubewharf/godel-scheduler/pkg/binder/framework/handle"
	framework "github.com/kubewharf/godel-scheduler/pkg/framework/api"
	utils "github.com/kubewharf/godel-scheduler/pkg/plugins/nodeports"
)

// NodePorts is a plugin that checks if a node has free ports for the requested pod ports.
type NodePorts struct{}

var _ framework.CheckConflictsPlugin = &NodePorts{}

const (
	Name = utils.Name
	// ErrReason when node ports aren't available.
	ErrReason = "node(s) didn't have free ports for the requested pod ports"
)

type preFilterState []*v1.ContainerPort

// Clone the prefilter state.
func (s preFilterState) Clone() framework.StateData {
	// The state is not impacted by adding/removing existing pods, hence we don't need to make a deep copy.
	return s
}

// Name returns name of the plugin. It is used in logs, etc.
func (pl *NodePorts) Name() string {
	return Name
}

// CheckConflicts invoked at the CheckConflicts extension point.
func (pl *NodePorts) CheckConflicts(_ context.Context, _ *framework.CycleState, pod *v1.Pod, nodeInfo framework.NodeInfo) *framework.Status {
	fits := utils.Fits(pod, nodeInfo)
	if !fits {
		return framework.NewStatus(framework.Unschedulable, ErrReason)
	}

	return nil
}

// New initializes a new plugin and returns it.
func New(_ runtime.Object, _ handle.BinderFrameworkHandle) (framework.Plugin, error) {
	return &NodePorts{}, nil
}
