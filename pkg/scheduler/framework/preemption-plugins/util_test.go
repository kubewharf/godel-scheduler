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

package preemptionplugins

import (
	"testing"

	v1 "k8s.io/api/core/v1"
	schedulingv1 "k8s.io/api/scheduling/v1"

	testing_helper "github.com/kubewharf/godel-scheduler/pkg/testing-helper"
	"github.com/kubewharf/godel-scheduler/pkg/util"
)

func TestPodEligibleToPreemptOthers(t *testing.T) {
	tests := []struct {
		name     string
		pod      *v1.Pod
		pods     []*v1.Pod
		nodes    []string
		expected bool
	}{
		{
			name: "Pod with 'PreemptNever' preemption policy in spec",
			pod: testing_helper.MakePod().Name("p_with_preempt_never_policy").UID("p").
				Priority(highPriority).PreemptionPolicy(v1.PreemptNever).PriorityClassName("preempt-lower-priority").
				Annotation(util.PreemptionPolicyKey, string(v1.PreemptLowerPriority)).Obj(),
			pods:     []*v1.Pod{},
			nodes:    []string{},
			expected: false,
		},
		{
			name: "Pod with 'PreemptLowerPriority' preemption policy in spec",
			pod: testing_helper.MakePod().Name("p_with_preempt_never_policy").UID("p").
				Priority(highPriority).PreemptionPolicy(v1.PreemptLowerPriority).PriorityClassName("never").
				Annotation(util.PreemptionPolicyKey, string(v1.PreemptNever)).Obj(),
			pods:     []*v1.Pod{},
			nodes:    []string{},
			expected: true,
		},
		{
			name: "Pod with 'PreemptNever' preemption policy in pc",
			pod: testing_helper.MakePod().Name("p_with_preempt_never_policy").UID("p").
				Priority(highPriority).PriorityClassName("never").
				Annotation(util.PreemptionPolicyKey, string(v1.PreemptLowerPriority)).Obj(),
			pods:     []*v1.Pod{},
			nodes:    []string{},
			expected: false,
		},
		{
			name: "Pod with 'PreemptLowerPriority' preemption policy in pc",
			pod: testing_helper.MakePod().Name("p_with_preempt_never_policy").UID("p").
				Priority(highPriority).PriorityClassName("preempt-lower-priority").
				Annotation(util.PreemptionPolicyKey, string(v1.PreemptNever)).Obj(),
			pods:     []*v1.Pod{},
			nodes:    []string{},
			expected: true,
		},
		{
			name: "Pod with 'PreemptNever' preemption policy in annotation",
			pod: testing_helper.MakePod().Name("p_with_preempt_never_policy").UID("p").
				Priority(highPriority).PriorityClassName("default").
				Annotation(util.PreemptionPolicyKey, string(v1.PreemptNever)).Obj(),
			pods:     []*v1.Pod{},
			nodes:    []string{},
			expected: false,
		},
		{
			name: "Pod with 'PreemptLowerPriority' preemption policy in annotation",
			pod: testing_helper.MakePod().Name("p_with_preempt_never_policy").UID("p").
				Priority(highPriority).PriorityClassName("default").
				Annotation(util.PreemptionPolicyKey, string(v1.PreemptLowerPriority)).Obj(),
			pods:     []*v1.Pod{},
			nodes:    []string{},
			expected: true,
		},
		{
			name: "normal Pod waiting for preemption, have pc",
			pod: testing_helper.MakePod().Name("p_wait_for_preemption").UID("p").
				Priority(highPriority).PriorityClassName("default").Obj(),
			pods:     []*v1.Pod{},
			nodes:    []string{},
			expected: true,
		},
		{
			name:     "normal Pod waiting for preemption, have no pc",
			pod:      testing_helper.MakePod().Name("p_wait_for_preemption").UID("p").Priority(highPriority).Obj(),
			pods:     []*v1.Pod{},
			nodes:    []string{},
			expected: true,
		},
	}

	for _, test := range tests {
		var nodes []*v1.Node
		for _, n := range test.nodes {
			nodes = append(nodes, testing_helper.MakeNode().Name(n).Obj())
		}
		pcs := []*schedulingv1.PriorityClass{
			testing_helper.MakePriorityClass().Name("preempt-lower-priority").SetPreemptionPolicy(v1.PreemptLowerPriority).Obj(),
			testing_helper.MakePriorityClass().Name("never").SetPreemptionPolicy(v1.PreemptNever).Obj(),
			testing_helper.MakePriorityClass().Name("default").Obj(),
		}
		pcLister := testing_helper.NewFakePriorityClassLister(pcs)
		if got := PodEligibleToPreemptOthers(test.pod, pcLister); got != test.expected {
			t.Errorf("expected %t, got %t for pod: %s", test.expected, got, test.pod.Name)
		}
	}
}
