/*
Copyright 2024 The Godel Scheduler Authors.

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
	"reflect"
	"testing"

	framework "github.com/kubewharf/godel-scheduler/pkg/framework/api"
	framework_helper "github.com/kubewharf/godel-scheduler/pkg/testing-helper/framework-helper"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// TestGetTPMapMatchingIncomingAffinityAntiAffinity tests against method getTPMapMatchingIncomingAffinityAntiAffinity
// on Anti Affinity cases
func TestGetTPMapMatchingIncomingAffinityAntiAffinity(t *testing.T) {
	newPodAffinityTerms := func(keys ...string) []v1.PodAffinityTerm {
		var terms []v1.PodAffinityTerm
		for _, key := range keys {
			terms = append(terms, v1.PodAffinityTerm{
				LabelSelector: &metav1.LabelSelector{
					MatchExpressions: []metav1.LabelSelectorRequirement{
						{
							Key:      key,
							Operator: metav1.LabelSelectorOpExists,
						},
					},
				},
				TopologyKey: "hostname",
			})
		}
		return terms
	}
	newPod := func(labels ...string) *v1.Pod {
		labelMap := make(map[string]string)
		for _, l := range labels {
			labelMap[l] = ""
		}
		return &v1.Pod{
			ObjectMeta: metav1.ObjectMeta{Name: "normal", Labels: labelMap},
			Spec:       v1.PodSpec{NodeName: "nodeA"},
		}
	}
	normalPodA := newPod("aaa")
	normalPodB := newPod("bbb")
	normalPodAB := newPod("aaa", "bbb")
	nodeA := &v1.Node{ObjectMeta: metav1.ObjectMeta{Name: "nodeA", Labels: map[string]string{"hostname": "nodeA"}}}

	tests := []struct {
		name                    string
		existingPods            []*v1.Pod
		nodes                   []*v1.Node
		pod                     *v1.Pod
		wantAffinityPodsMap     TopologyToMatchedTermCount
		wantAntiAffinityPodsMap TopologyToMatchedTermCount
	}{
		{
			name:  "nil test",
			nodes: []*v1.Node{nodeA},
			pod: &v1.Pod{
				ObjectMeta: metav1.ObjectMeta{Name: "aaa-normal"},
			},
			wantAffinityPodsMap:     make(TopologyToMatchedTermCount),
			wantAntiAffinityPodsMap: make(TopologyToMatchedTermCount),
		},
		{
			name:         "incoming pod without affinity/anti-affinity causes a no-op",
			existingPods: []*v1.Pod{normalPodA},
			nodes:        []*v1.Node{nodeA},
			pod: &v1.Pod{
				ObjectMeta: metav1.ObjectMeta{Name: "aaa-normal"},
			},
			wantAffinityPodsMap:     make(TopologyToMatchedTermCount),
			wantAntiAffinityPodsMap: make(TopologyToMatchedTermCount),
		},
		{
			name:         "no pod has label that violates incoming pod's affinity and anti-affinity",
			existingPods: []*v1.Pod{normalPodB},
			nodes:        []*v1.Node{nodeA},
			pod: &v1.Pod{
				ObjectMeta: metav1.ObjectMeta{Name: "aaa-anti"},
				Spec: v1.PodSpec{
					Affinity: &v1.Affinity{
						PodAffinity: &v1.PodAffinity{
							RequiredDuringSchedulingIgnoredDuringExecution: newPodAffinityTerms("aaa"),
						},
						PodAntiAffinity: &v1.PodAntiAffinity{
							RequiredDuringSchedulingIgnoredDuringExecution: newPodAffinityTerms("aaa"),
						},
					},
				},
			},
			wantAffinityPodsMap:     make(TopologyToMatchedTermCount),
			wantAntiAffinityPodsMap: make(TopologyToMatchedTermCount),
		},
		{
			name:         "existing pod matches incoming pod's affinity and anti-affinity - single term case",
			existingPods: []*v1.Pod{normalPodA},
			nodes:        []*v1.Node{nodeA},
			pod: &v1.Pod{
				ObjectMeta: metav1.ObjectMeta{Name: "affi-antiaffi"},
				Spec: v1.PodSpec{
					Affinity: &v1.Affinity{
						PodAffinity: &v1.PodAffinity{
							RequiredDuringSchedulingIgnoredDuringExecution: newPodAffinityTerms("aaa"),
						},
						PodAntiAffinity: &v1.PodAntiAffinity{
							RequiredDuringSchedulingIgnoredDuringExecution: newPodAffinityTerms("aaa"),
						},
					},
				},
			},
			wantAffinityPodsMap: TopologyToMatchedTermCount{
				{Key: "hostname", Value: "nodeA"}: 1,
			},
			wantAntiAffinityPodsMap: TopologyToMatchedTermCount{
				{Key: "hostname", Value: "nodeA"}: 1,
			},
		},
		{
			name:         "existing pod matches incoming pod's affinity and anti-affinity - multiple terms case",
			existingPods: []*v1.Pod{normalPodAB},
			nodes:        []*v1.Node{nodeA},
			pod: &v1.Pod{
				ObjectMeta: metav1.ObjectMeta{Name: "affi-antiaffi"},
				Spec: v1.PodSpec{
					Affinity: &v1.Affinity{
						PodAffinity: &v1.PodAffinity{
							RequiredDuringSchedulingIgnoredDuringExecution: newPodAffinityTerms("aaa", "bbb"),
						},
						PodAntiAffinity: &v1.PodAntiAffinity{
							RequiredDuringSchedulingIgnoredDuringExecution: newPodAffinityTerms("aaa"),
						},
					},
				},
			},
			wantAffinityPodsMap: TopologyToMatchedTermCount{
				{Key: "hostname", Value: "nodeA"}: 2, // 2 one for each term.
			},
			wantAntiAffinityPodsMap: TopologyToMatchedTermCount{
				{Key: "hostname", Value: "nodeA"}: 1,
			},
		},
		{
			name:         "existing pod not match incoming pod's affinity but matches anti-affinity",
			existingPods: []*v1.Pod{normalPodA},
			nodes:        []*v1.Node{nodeA},
			pod: &v1.Pod{
				ObjectMeta: metav1.ObjectMeta{Name: "affi-antiaffi"},
				Spec: v1.PodSpec{
					Affinity: &v1.Affinity{
						PodAffinity: &v1.PodAffinity{
							RequiredDuringSchedulingIgnoredDuringExecution: newPodAffinityTerms("aaa", "bbb"),
						},
						PodAntiAffinity: &v1.PodAntiAffinity{
							RequiredDuringSchedulingIgnoredDuringExecution: newPodAffinityTerms("aaa", "bbb"),
						},
					},
				},
			},
			wantAffinityPodsMap: make(TopologyToMatchedTermCount),
			wantAntiAffinityPodsMap: TopologyToMatchedTermCount{
				{Key: "hostname", Value: "nodeA"}: 1,
			},
		},
		{
			name:         "incoming pod's anti-affinity has more than one term - existing pod violates partial term - case 1",
			existingPods: []*v1.Pod{normalPodAB},
			nodes:        []*v1.Node{nodeA},
			pod: &v1.Pod{
				ObjectMeta: metav1.ObjectMeta{Name: "anaffi-antiaffiti"},
				Spec: v1.PodSpec{
					Affinity: &v1.Affinity{
						PodAffinity: &v1.PodAffinity{
							RequiredDuringSchedulingIgnoredDuringExecution: newPodAffinityTerms("aaa", "ccc"),
						},
						PodAntiAffinity: &v1.PodAntiAffinity{
							RequiredDuringSchedulingIgnoredDuringExecution: newPodAffinityTerms("aaa", "ccc"),
						},
					},
				},
			},
			wantAffinityPodsMap: make(TopologyToMatchedTermCount),
			wantAntiAffinityPodsMap: TopologyToMatchedTermCount{
				{Key: "hostname", Value: "nodeA"}: 1,
			},
		},
		{
			name:         "incoming pod's anti-affinity has more than one term - existing pod violates partial term - case 2",
			existingPods: []*v1.Pod{normalPodB},
			nodes:        []*v1.Node{nodeA},
			pod: &v1.Pod{
				ObjectMeta: metav1.ObjectMeta{Name: "affi-antiaffi"},
				Spec: v1.PodSpec{
					Affinity: &v1.Affinity{
						PodAffinity: &v1.PodAffinity{
							RequiredDuringSchedulingIgnoredDuringExecution: newPodAffinityTerms("aaa", "bbb"),
						},
						PodAntiAffinity: &v1.PodAntiAffinity{
							RequiredDuringSchedulingIgnoredDuringExecution: newPodAffinityTerms("aaa", "bbb"),
						},
					},
				},
			},
			wantAffinityPodsMap: make(TopologyToMatchedTermCount),
			wantAntiAffinityPodsMap: TopologyToMatchedTermCount{
				{Key: "hostname", Value: "nodeA"}: 1,
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			snapshot := framework_helper.MakeSnapShot(tt.existingPods, tt.nodes, nil)

			nodes := snapshot.NodeInfos().List()
			gotAffinityPodsMap, gotAntiAffinityPodsMap := GetTPMapMatchingIncomingAffinityAntiAffinity(framework.NewPodInfo(tt.pod), nodes)
			if !reflect.DeepEqual(gotAffinityPodsMap, tt.wantAffinityPodsMap) {
				t.Errorf("getTPMapMatchingIncomingAffinityAntiAffinity() gotAffinityPodsMap = %#v, want %#v", gotAffinityPodsMap, tt.wantAffinityPodsMap)
			}
			if !reflect.DeepEqual(gotAntiAffinityPodsMap, tt.wantAntiAffinityPodsMap) {
				t.Errorf("getTPMapMatchingIncomingAffinityAntiAffinity() gotAntiAffinityPodsMap = %#v, want %#v", gotAntiAffinityPodsMap, tt.wantAntiAffinityPodsMap)
			}
		})
	}
}
