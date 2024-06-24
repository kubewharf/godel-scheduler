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

package victimscount

import (
	"k8s.io/apimachinery/pkg/runtime"

	framework "github.com/kubewharf/godel-scheduler/pkg/framework/api"
	"github.com/kubewharf/godel-scheduler/pkg/scheduler/framework/handle"
)

const LeastVictimsName = "LeastVictims"

type LeastVictims struct{}

var _ framework.CandidatesSortingPlugin = &LeastVictims{}

func NewLeastVictims(_ runtime.Object, _ handle.PodFrameworkHandle) (framework.Plugin, error) {
	return &LeastVictims{}, nil
}

func (lv *LeastVictims) Name() string {
	return LeastVictimsName
}

func (lv *LeastVictims) Compare(c1, c2 *framework.Candidate) int {
	numPods1 := len(c1.Victims.Pods)
	numPods2 := len(c2.Victims.Pods)
	if numPods1 < numPods2 {
		return 1
	} else if numPods1 > numPods2 {
		return -1
	} else {
		return 0
	}
}
