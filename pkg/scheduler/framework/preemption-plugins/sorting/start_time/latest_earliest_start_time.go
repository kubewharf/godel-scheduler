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

package starttime

import (
	"k8s.io/apimachinery/pkg/runtime"

	framework "github.com/kubewharf/godel-scheduler/pkg/framework/api"
	frameworkutils "github.com/kubewharf/godel-scheduler/pkg/framework/utils"
	"github.com/kubewharf/godel-scheduler/pkg/scheduler/framework/handle"
)

const LatestEarliestStartTimeName = "LatestEarliestStartTime"

type LatestEarliestStartTime struct{}

var _ framework.CandidatesSortingPlugin = &LatestEarliestStartTime{}

func NewLatestEarliestStartTime(_ runtime.Object, _ handle.PodFrameworkHandle) (framework.Plugin, error) {
	return &LatestEarliestStartTime{}, nil
}

func (lest *LatestEarliestStartTime) Name() string {
	return LatestEarliestStartTimeName
}

func (lest *LatestEarliestStartTime) Compare(c1, c2 *framework.Candidate) int {
	earliestStartTimeOnNode1 := frameworkutils.GetEarliestPodStartTime(c1.Victims)
	earliestStartTimeOnNode2 := frameworkutils.GetEarliestPodStartTime(c2.Victims)
	if earliestStartTimeOnNode1 != nil && earliestStartTimeOnNode2 != nil {
		if earliestStartTimeOnNode1.After(earliestStartTimeOnNode2.Time) {
			return 1
		} else if earliestStartTimeOnNode1.Before(earliestStartTimeOnNode2) {
			return -1
		} else {
			return 0
		}
	} else if earliestStartTimeOnNode1 != nil {
		return 1
	} else if earliestStartTimeOnNode2 != nil {
		return -1
	} else {
		return 0
	}
}
