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

package podlauncher

import (
	"context"

	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"

	framework "github.com/kubewharf/godel-scheduler/pkg/framework/api"
	utils "github.com/kubewharf/godel-scheduler/pkg/plugins/podlauncher"
	"github.com/kubewharf/godel-scheduler/pkg/scheduler/framework/handle"
)

// PodLauncher is a plugin that checks if a pod can run on a certain node according to pod launcher
type PodLauncher struct {
	handle handle.PodFrameworkHandle
}

var _ framework.FilterPlugin = &PodLauncher{}

const (
	// Name is the name of the plugin used in the plugin registry and configurations.
	Name = "PodLauncher"

	// ErrReasonTemplate returned when node doesn't have the requested launcher in it.
	ErrReasonTemplate = "launcher %v doesn't exist on this node"
)

// Name returns name of the plugin. It is used in logs, etc.
func (pl *PodLauncher) Name() string {
	return Name
}

// Filter invoked at the Filter extension point.
func (pl *PodLauncher) Filter(ctx context.Context, state *framework.CycleState, pod *v1.Pod, nodeInfo framework.NodeInfo) *framework.Status {
	_, status := utils.NodeFits(state, pod, nodeInfo)
	return status
}

// New initializes a new plugin and returns it.
func New(_ runtime.Object, _ handle.PodFrameworkHandle) (framework.Plugin, error) {
	return &PodLauncher{}, nil
}
