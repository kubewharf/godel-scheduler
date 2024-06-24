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

package daemonset

import (
	"context"

	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/klog/v2"

	framework "github.com/kubewharf/godel-scheduler/pkg/framework/api"
	"github.com/kubewharf/godel-scheduler/pkg/scheduler/framework/handle"
	podutil "github.com/kubewharf/godel-scheduler/pkg/util/pod"
)

const Name = "DaemonSet"

type DaemonSet struct {
	handler handle.UnitFrameworkHandle
}

var _ framework.LocatingPlugin = &DaemonSet{}

func New(_ runtime.Object, handler handle.UnitFrameworkHandle) (framework.Plugin, error) {
	return &DaemonSet{handler: handler}, nil
}

func (i *DaemonSet) Name() string {
	return Name
}

func (i *DaemonSet) Locating(ctx context.Context, unit framework.ScheduleUnit, unitCycleState *framework.CycleState, nodeGroup framework.NodeGroup) (framework.NodeGroup, *framework.Status) {
	pods := unit.GetPods()
	set := sets.NewString()
	for _, podInfo := range pods {
		pod := podInfo.Pod
		if podutil.PodHasDaemonSetOwnerReference(pod) {
			set.Insert(parseDaemonSetAffinityTerm(pod)...)
		} else {
			// If there are pods that are not belong to DaemonSet, then all nodes should be available.
			// TODO: revisit this rule.
			return nodeGroup, nil
		}
	}

	klog.InfoS("DaemonSet Locating for ScheduleUnit", "unitKey", unit.GetKey(), "nodeNames", set.UnsortedList())

	return framework.FilterNodeGroup(nodeGroup, func(ni framework.NodeInfo) bool { return set.Has(ni.GetNodeName()) }), nil
}
