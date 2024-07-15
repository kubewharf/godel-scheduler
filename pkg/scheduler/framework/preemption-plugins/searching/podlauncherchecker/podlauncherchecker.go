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

package podlauncherchecker

import (
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"

	framework "github.com/kubewharf/godel-scheduler/pkg/framework/api"
	"github.com/kubewharf/godel-scheduler/pkg/scheduler/framework/handle"
	podutil "github.com/kubewharf/godel-scheduler/pkg/util/pod"
)

// If pod's launcher is not kubelet and
// launcher is unable to respond to apiserver's pod delete event,
// need to add PodLauncherChecker preemption plugin
const PodLauncherCheckerName string = "PodLauncherChecker"

type PodLauncherChecker struct{}

var _ framework.VictimSearchingPlugin = &PodLauncherChecker{}

// New initializes a new plugin and returns it.
func NewPodLauncherChecker(_ runtime.Object, handle handle.PodFrameworkHandle) (framework.Plugin, error) {
	return &PodLauncherChecker{}, nil
}

func (plc *PodLauncherChecker) Name() string {
	return PodLauncherCheckerName
}

func (plc *PodLauncherChecker) VictimSearching(_ *v1.Pod, podInfo *framework.PodInfo, _, _ *framework.CycleState, _ *framework.VictimState) (framework.Code, string) {
	if podInfo.PodLauncher != podutil.Kubelet {
		return framework.PreemptionFail, "pod with launcher node manager can not be preempted"
	}
	return framework.PreemptionNotSure, ""
}
