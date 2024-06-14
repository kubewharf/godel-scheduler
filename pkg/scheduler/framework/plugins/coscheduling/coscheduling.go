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

package coscheduling

import (
	"context"
	"fmt"

	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/klog/v2"

	"github.com/kubewharf/godel-scheduler-api/pkg/apis/scheduling/v1alpha1"
	framework "github.com/kubewharf/godel-scheduler/pkg/framework/api"
	podgroupstore "github.com/kubewharf/godel-scheduler/pkg/scheduler/cache/commonstores/podgroup_store"
	"github.com/kubewharf/godel-scheduler/pkg/scheduler/framework/handle"
	unitutil "github.com/kubewharf/godel-scheduler/pkg/util/unit"
)

// Coscheduling is a plugin that schedules pods in a group.
type Coscheduling struct {
	frameworkHandler handle.PodFrameworkHandle
	pluginHandle     podgroupstore.StoreHandle
}

var (
	_ framework.PreFilterPlugin = &Coscheduling{}
	_ framework.FilterPlugin    = &Coscheduling{}
)

const (
	// Name is the name of the plugin used in Registry and configurations.
	Name = "Coscheduling"
)

// New initializes and returns a new Coscheduling plugin.
func New(_ runtime.Object, handle handle.PodFrameworkHandle) (framework.Plugin, error) {
	var pluginHandle podgroupstore.StoreHandle
	if ins := handle.FindStore(podgroupstore.Name); ins != nil {
		pluginHandle = ins.(podgroupstore.StoreHandle)
	}

	return &Coscheduling{
		frameworkHandler: handle,
		pluginHandle:     pluginHandle,
	}, nil
}

// Name returns name of the plugin. It is used in logs, etc.
func (cs *Coscheduling) Name() string {
	return Name
}

// PreFilter reject pods without associate pod group or invalid pod group objects (e.g. timeout)
func (cs *Coscheduling) PreFilter(ctx context.Context, state *framework.CycleState, pod *v1.Pod) *framework.Status {
	podGroup, err := cs.getPodGroup(pod)
	if err != nil {
		klog.ErrorS(err, "Prefilter failed", "pod", klog.KObj(pod))
		return framework.NewStatus(framework.UnschedulableAndUnresolvable, err.Error())
	}

	if podGroup == nil {
		return framework.NewStatus(framework.Success, "")
	}

	// TODO: https://github.com/kubewharf/godel-scheduler/issues/45
	// Currently, even UnschedulableAndUnresolvable is emitted, scheduler still try to run preemption for the pod.
	// TODO: https://github.com/kubewharf/godel-scheduler/issues/46
	// Ideally, we should also create a separate queue to reduce side effect on regular pods.
	if podGroup.Status.Phase == v1alpha1.PodGroupTimeout {
		msg := fmt.Sprintf("Pods could not be scheduled since PodGroup %v/%v was timed out", podGroup.Namespace, podGroup.Name)
		klog.InfoS("WARN: Pods could not be scheduled since PodGroup was timed out", "podGroup", klog.KObj(podGroup))
		return framework.NewStatus(framework.UnschedulableAndUnresolvable, msg)
	}

	return framework.NewStatus(framework.Success, "")
}

func (cs *Coscheduling) PreFilterExtensions() framework.PreFilterExtensions {
	return nil
}

// TODO: remove Filter and refactor plugin registration in scheduler.
func (cs *Coscheduling) Filter(ctx context.Context, state *framework.CycleState, pod *v1.Pod, nodeInfo framework.NodeInfo) *framework.Status {
	return framework.NewStatus(framework.Success, "")
}

func (cs *Coscheduling) getPodGroup(pod *v1.Pod) (*v1alpha1.PodGroup, error) {
	pgName := unitutil.GetPodGroupFullName(pod)
	if len(pgName) == 0 {
		return nil, nil
	}

	podGroup, err := cs.pluginHandle.GetPodGroupInfo(pgName)
	if err != nil {
		return nil, err
	}

	if podGroup.Spec.MinMember <= 0 {
		return nil, fmt.Errorf("PodGroup %v/%v minMember: %v <= 0", podGroup.Namespace, podGroup.Name, podGroup.Spec.MinMember)
	}

	return podGroup, nil
}
