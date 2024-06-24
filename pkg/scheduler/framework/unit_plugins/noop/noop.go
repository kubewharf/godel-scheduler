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

package noop

import (
	"context"

	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"

	framework "github.com/kubewharf/godel-scheduler/pkg/framework/api"
	"github.com/kubewharf/godel-scheduler/pkg/scheduler/framework/handle"
)

const (
	Name = "Noop"
)

type Noop struct{}

var (
	_ framework.LocatingPlugin      = &Noop{}
	_ framework.GroupingPlugin      = &Noop{}
	_ framework.PreferNodeExtension = &Noop{}
)

func New(_ runtime.Object, _ handle.UnitFrameworkHandle) (framework.Plugin, error) {
	return &Noop{}, nil
}

func (i *Noop) Name() string {
	return Name
}

func (i *Noop) Locating(ctx context.Context, unit framework.ScheduleUnit, unitCycleState *framework.CycleState, nodeGroup framework.NodeGroup) (framework.NodeGroup, *framework.Status) {
	return nodeGroup, nil
}

func (i *Noop) Grouping(ctx context.Context, unit framework.ScheduleUnit, unitCycleState *framework.CycleState, nodeGroup framework.NodeGroup) ([]framework.NodeGroup, *framework.Status) {
	return []framework.NodeGroup{nodeGroup}, nil
}

func (i *Noop) PrePreferNode(ctx context.Context, unitCycleState, podCycleState *framework.CycleState, pod *v1.Pod, nodeInfo framework.NodeInfo) (framework.NodeInfo, *framework.CycleState, *framework.Status) {
	return nodeInfo, podCycleState, nil
}

func (i *Noop) PostPreferNode(ctx context.Context, unitCycleState, podCycleState *framework.CycleState, pod *v1.Pod, nodeInfo framework.NodeInfo, status *framework.Status) *framework.Status {
	return nil
}
