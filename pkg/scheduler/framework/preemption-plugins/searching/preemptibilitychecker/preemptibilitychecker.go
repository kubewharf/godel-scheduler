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

package preemptibilitychecker

import (
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	schedulingv1listers "k8s.io/client-go/listers/scheduling/v1"
	"k8s.io/klog/v2"

	framework "github.com/kubewharf/godel-scheduler/pkg/framework/api"
	"github.com/kubewharf/godel-scheduler/pkg/scheduler/framework/handle"
	"github.com/kubewharf/godel-scheduler/pkg/util"
)

const PreemptibilityCheckerName string = "PreemptibilityChecker"

type PreemptibilityChecker struct {
	pcLister schedulingv1listers.PriorityClassLister
}

var _ framework.VictimSearchingPlugin = &PreemptibilityChecker{}

// New initializes a new plugin and returns it.
func NewPreemptibilityChecker(_ runtime.Object, handle handle.PodFrameworkHandle) (framework.Plugin, error) {
	checker := &PreemptibilityChecker{
		pcLister: handle.SharedInformerFactory().Scheduling().V1().PriorityClasses().Lister(),
	}
	return checker, nil
}

func (pc *PreemptibilityChecker) Name() string {
	return PreemptibilityCheckerName
}

func (pc *PreemptibilityChecker) VictimSearching(_ *v1.Pod, podInfo *framework.PodInfo, _, _ *framework.CycleState, _ *framework.VictimState) (framework.Code, string) {
	if podInfo.PodPreemptionInfo.CanBePreempted > 0 {
		return framework.PreemptionNotSure, ""
	} else if podInfo.PodPreemptionInfo.CanBePreempted < 0 {
		return framework.PreemptionFail, "pod marked as non-preemptable"
	}
	if !checkPreemptibility(podInfo.Pod, pc.pcLister) {
		return framework.PreemptionFail, "pod marked as non-preemptable"
	}
	return framework.PreemptionNotSure, ""
}

func checkPreemptibility(pod *v1.Pod, pcLister schedulingv1listers.PriorityClassLister) bool {
	// TODO: remove this policy: pod without priorityclass could not be preempted
	if len(pod.Spec.PriorityClassName) == 0 {
		return false
	}

	pc, err := pcLister.Get(pod.Spec.PriorityClassName)
	if err != nil {
		klog.InfoS("Failed to get PriorityClassName for pod", "pod", klog.KObj(pod), "err", err)
		return false
	}
	pcAnnotations := pc.Annotations
	return pcAnnotations[util.CanBePreemptedAnnotationKey] == util.CanBePreempted
}
