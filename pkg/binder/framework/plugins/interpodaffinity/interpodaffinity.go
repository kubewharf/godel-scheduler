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

package interpodaffinity

import (
	"context"

	"github.com/kubewharf/godel-scheduler/pkg/binder/framework/handle"
	framework "github.com/kubewharf/godel-scheduler/pkg/framework/api"
	godelutil "github.com/kubewharf/godel-scheduler/pkg/util"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

const (
	Name                                       = "InterPodAffinityCheck"
	ErrReasonExistingAntiAffinityRulesNotMatch = "node didn't satisfy existing pods anti-affinity rules"
	ErrReasonAffinityRulesNotMatch             = "node didn't match pod affinity rules"
	ErrReasonAntiAffinityRulesNotMatch         = "node didn't match pod anti-affinity rules"
)

type InterPodAffinity struct{}

var _ framework.CheckConflictsPlugin = &InterPodAffinity{}

func (pl *InterPodAffinity) Name() string {
	return Name
}

func (pl *InterPodAffinity) CheckConflicts(_ context.Context, cycleState *framework.CycleState, pod *v1.Pod, nodeInfo framework.NodeInfo) *framework.Status {
	checkRes := []string{}
	// Check whether the pod satisfies the node's existing pod anti-affinity.
	if res := pl.checkNodeExistingPodAntiAffinity(pod, nodeInfo); res != "" {
		checkRes = append(checkRes, res)
	}
	// Check whether the pods on the node satisfy the pod's anti-affinity and affinity.
	if res := pl.checkPodAffinityAntiAffinity(pod, nodeInfo); len(res) > 0 {
		checkRes = append(checkRes, res...)
	}

	if len(checkRes) > 0 {
		return framework.NewStatus(framework.Unschedulable, checkRes...)
	}
	return nil
}

func New(_ runtime.Object, handle handle.BinderFrameworkHandle) (framework.Plugin, error) {
	return &InterPodAffinity{}, nil
}

func (pl *InterPodAffinity) checkNodeExistingPodAntiAffinity(pod *v1.Pod, nodeInfo framework.NodeInfo) string {
	for _, existingPod := range nodeInfo.GetPodsWithRequiredAntiAffinity() {
		for _, antiAffinityTerm := range existingPod.RequiredAntiAffinityTerms {
			if godelutil.PodMatchesTermsNamespaceAndSelector(pod, antiAffinityTerm.Namespaces, antiAffinityTerm.Selector) {
				return ErrReasonExistingAntiAffinityRulesNotMatch
			}
		}
	}
	return ""
}

func (pl *InterPodAffinity) checkPodAffinityAntiAffinity(pod *v1.Pod, nodeInfo framework.NodeInfo) []string {
	podInfo := framework.NewPodInfo(pod)
	checkRes := []string{}

	isAllAffinityTermSatisfied := true
	for _, podAffinityTerm := range podInfo.RequiredAffinityTerms {
		isTermSatified := false
		for _, existingPod := range nodeInfo.GetPods() {
			if godelutil.PodMatchesTermsNamespaceAndSelector(existingPod.Pod, podAffinityTerm.Namespaces, podAffinityTerm.Selector) {
				isTermSatified = true
				continue
			}
		}
		if !isTermSatified {
			isAllAffinityTermSatisfied = false
			break
		}
	}
	if !isAllAffinityTermSatisfied {
		checkRes = append(checkRes, ErrReasonAffinityRulesNotMatch)
	}

	isAllAntiAffinityTermSatisfied := true
	for _, podAntiAffinityTerm := range podInfo.RequiredAntiAffinityTerms {
		for _, existingPod := range nodeInfo.GetPods() {
			if godelutil.PodMatchesTermsNamespaceAndSelector(existingPod.Pod, podAntiAffinityTerm.Namespaces, podAntiAffinityTerm.Selector) {
				isAllAntiAffinityTermSatisfied = false
				break
			}
		}
		if !isAllAntiAffinityTermSatisfied {
			break
		}
	}
	if !isAllAntiAffinityTermSatisfied {
		checkRes = append(checkRes, ErrReasonAntiAffinityRulesNotMatch)
	}

	return checkRes
}
