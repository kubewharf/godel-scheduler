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

package api

import (
	"fmt"
	"sort"
	"strings"

	v1 "k8s.io/api/core/v1"
)

// PreemptionError describe that pod can't preempt any of candidate nodes.
// Note: we only return PreemptionError when PostPlugins return `Unschedulable` or `UnschedulableAndUnresolvable`.
type PreemptionError string

func NewPreemptionError(podKey string, candidateNodes int, status *Status) PreemptionError {
	reasons := make(map[string]int)
	for _, reason := range status.Reasons() {
		reasons[reason]++
	}

	sortReasonsHistogram := func() []string {
		var reasonStrings []string
		for k, v := range reasons {
			reasonStrings = append(reasonStrings, fmt.Sprintf("%v %v", v, k))
		}
		sort.Strings(reasonStrings)
		return reasonStrings
	}

	errMsg := fmt.Sprintf("0/%v nodes are available to preempt, pod:%v, reason:%v.", candidateNodes, podKey, strings.Join(sortReasonsHistogram(), ", "))
	return PreemptionError(errMsg)
}

func (err PreemptionError) Error() string {
	return string(err)
}

// FitError describes a fit error of a pod.
type FitError struct {
	Pod                   *v1.Pod
	NumAllNodes           int
	FilteredNodesStatuses NodeToStatusMap
}

// NoNodeAvailableMsg is used to format message when no nodes available.
const NoNodeAvailableMsg = "0/%v nodes are available to run pod %v: %v.\n"

// Error returns detailed information of why the pod failed to fit on each node
func (f *FitError) Error() string {
	// If debug mode is off, print aggregated error message
	reasons := make(map[string]int)
	for _, status := range f.FilteredNodesStatuses {
		for _, reason := range status.Reasons() {
			reasons[reason]++
		}
	}

	sortReasonsHistogram := func() []string {
		var reasonStrings []string
		for k, v := range reasons {
			reasonStrings = append(reasonStrings, fmt.Sprintf("%v %v", v, k))
		}
		sort.Strings(reasonStrings)
		return reasonStrings
	}
	reasonMsg := fmt.Sprintf(NoNodeAvailableMsg, f.NumAllNodes, f.Pod.Name, strings.Join(sortReasonsHistogram(), ", "))
	return reasonMsg
}
