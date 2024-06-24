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

package virtualkubelet

import (
	"context"
	"fmt"

	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/klog/v2"

	framework "github.com/kubewharf/godel-scheduler/pkg/framework/api"
	"github.com/kubewharf/godel-scheduler/pkg/scheduler/framework/handle"
	podutil "github.com/kubewharf/godel-scheduler/pkg/util/pod"
)

const Name = "VirtualKubelet"

type VirtualKubelet struct {
	handler handle.UnitFrameworkHandle
}

var _ framework.LocatingPlugin = &VirtualKubelet{}

func New(_ runtime.Object, handler handle.UnitFrameworkHandle) (framework.Plugin, error) {
	return &VirtualKubelet{handler: handler}, nil
}

func (i *VirtualKubelet) Name() string {
	return Name
}

func (i *VirtualKubelet) Locating(ctx context.Context, unit framework.ScheduleUnit, unitCycleState *framework.CycleState, nodeGroup framework.NodeGroup) (framework.NodeGroup, *framework.Status) {
	pods := unit.GetPods()
	podLauncher, err := podutil.GetPodLauncher(pods[0].Pod)
	if err != nil {
		return nil, framework.AsStatus(fmt.Errorf("pod launcher in unit %v is invalid: %v", unit.GetKey(), podLauncher))
	}
	for _, podInfo := range pods {
		pod := podInfo.Pod
		if !isVirtualKubeletPod(pod) {
			// If there are pods that are not belong to DaemonSet, then all nodes should be available.
			// TODO: revisit this rule.
			return nodeGroup, nil
		}
	}

	klog.InfoS("VirtualKubelet Locating for ScheduleUnit", "unitKey", unit.GetKey())
	return framework.FilterNodeGroup(nodeGroup, func(ni framework.NodeInfo) bool {
		return isVirtualKubeletNode(ni, podLauncher)
	}), nil
}
