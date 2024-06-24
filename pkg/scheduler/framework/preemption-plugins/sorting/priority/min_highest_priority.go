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

package priority

import (
	"k8s.io/apimachinery/pkg/runtime"

	framework "github.com/kubewharf/godel-scheduler/pkg/framework/api"
	"github.com/kubewharf/godel-scheduler/pkg/scheduler/framework/handle"
	podutil "github.com/kubewharf/godel-scheduler/pkg/util/pod"
)

const MinHighestPriorityName = "MinHighestPriority"

type MinHighestPriority struct{}

var _ framework.CandidatesSortingPlugin = &MinHighestPriority{}

func NewMinHighestPriority(_ runtime.Object, _ handle.PodFrameworkHandle) (framework.Plugin, error) {
	return &MinHighestPriority{}, nil
}

func (mhp *MinHighestPriority) Name() string {
	return MinHighestPriorityName
}

func (mhp *MinHighestPriority) Compare(c1, c2 *framework.Candidate) int {
	highestPodPriority1 := podutil.GetPodPriority(c1.Victims.Pods[0])
	highestPodPriority2 := podutil.GetPodPriority(c2.Victims.Pods[0])
	if highestPodPriority1 < highestPodPriority2 {
		return 1
	} else if highestPodPriority1 > highestPodPriority2 {
		return -1
	} else {
		return 0
	}
}
