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

package noderesources

import (
	"context"

	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"

	"github.com/kubewharf/godel-scheduler/pkg/binder/framework/handle"
	framework "github.com/kubewharf/godel-scheduler/pkg/framework/api"
	utils "github.com/kubewharf/godel-scheduler/pkg/plugins/noderesources"
)

const (
	// ConflictCheckName is the name of the plugin used in the plugin registry and configurations.
	ConflictCheckName = "NodeResourcesCheck"
)

var _ framework.CheckConflictsPlugin = &ConflictCheck{}

// Fit is a plugin that checks if a node has sufficient resources.
type ConflictCheck struct{}

// Name returns name of the plugin. It is used in logs, etc.
func (pl *ConflictCheck) Name() string {
	return ConflictCheckName
}

func (pl *ConflictCheck) CheckConflicts(_ context.Context, state *framework.CycleState, pod *v1.Pod, nodeInfo framework.NodeInfo) *framework.Status {
	s := utils.ComputePodResourceRequest(state, pod)
	if s.Err != nil {
		return framework.NewStatus(framework.Error, s.Err.Error())
	}
	insufficientResources := utils.FitsRequest(s, nodeInfo, nil, nil)

	if len(insufficientResources) != 0 {
		// We will keep all failure reasons.
		failureReasons := make([]string, 0, len(insufficientResources))
		for _, r := range insufficientResources {
			failureReasons = append(failureReasons, r.Reason)
		}
		return framework.NewStatus(framework.Unschedulable, failureReasons...)
	}
	return nil
}

func NewConflictCheck(_ runtime.Object, _ handle.BinderFrameworkHandle) (framework.Plugin, error) {
	return &ConflictCheck{}, nil
}
