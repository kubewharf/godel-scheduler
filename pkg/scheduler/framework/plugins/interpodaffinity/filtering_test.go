/*
Copyright 2019 The Kubernetes Authors.

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
	"fmt"
	"reflect"
	"strings"
	"testing"

	nodev1alpha1 "github.com/kubewharf/godel-scheduler-api/pkg/apis/node/v1alpha1"
	framework "github.com/kubewharf/godel-scheduler/pkg/framework/api"
	utils "github.com/kubewharf/godel-scheduler/pkg/plugins/interpodaffinity"

	"github.com/kubewharf/godel-scheduler/pkg/plugins/podlauncher"
	godelcache "github.com/kubewharf/godel-scheduler/pkg/scheduler/cache"
	framework_helper "github.com/kubewharf/godel-scheduler/pkg/testing-helper/framework-helper"
	podutil "github.com/kubewharf/godel-scheduler/pkg/util/pod"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/sets"
)

var (
	defaultNamespace = ""
)

func createPodWithAffinityTerms(namespace, nodeName string, labels map[string]string, affinity, antiAffinity []v1.PodAffinityTerm) *v1.Pod {
	return &v1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Labels:    labels,
			Namespace: namespace,
		},
		Spec: v1.PodSpec{
			NodeName: nodeName,
			Affinity: &v1.Affinity{
				PodAffinity: &v1.PodAffinity{
					RequiredDuringSchedulingIgnoredDuringExecution: affinity,
				},
				PodAntiAffinity: &v1.PodAntiAffinity{
					RequiredDuringSchedulingIgnoredDuringExecution: antiAffinity,
				},
			},
		},
	}

}

func TestRequiredAffinitySingleNode(t *testing.T) {
	podLabel := map[string]string{"service": "securityscan"}
	labels1 := map[string]string{
		"region": "r1",
		"zone":   "z11",
	}
	podLabel2 := map[string]string{"security": "S1"}
	node1 := v1.Node{ObjectMeta: metav1.ObjectMeta{Name: "machine1", Labels: labels1}}
	tests := []struct {
		pod        *v1.Pod
		pods       []*v1.Pod
		node       *v1.Node
		name       string
		wantStatus *framework.Status
	}{
		{
			pod:  new(v1.Pod),
			node: &node1,
			name: "A pod that has no required pod affinity scheduling rules can schedule onto a node with no existing pods",
		},
		{
			pod: createPodWithAffinityTerms(defaultNamespace, "", podLabel2,
				[]v1.PodAffinityTerm{
					{
						LabelSelector: &metav1.LabelSelector{
							MatchExpressions: []metav1.LabelSelectorRequirement{
								{
									Key:      "service",
									Operator: metav1.LabelSelectorOpIn,
									Values:   []string{"securityscan", "value2"},
								},
							},
						},
						TopologyKey: "region",
					},
				}, nil),
			pods: []*v1.Pod{{Spec: v1.PodSpec{NodeName: "machine1"}, ObjectMeta: metav1.ObjectMeta{Labels: podLabel}}},
			node: &node1,
			name: "satisfies with requiredDuringSchedulingIgnoredDuringExecution in PodAffinity using In operator that matches the existing pod",
		},
		{
			pod: createPodWithAffinityTerms(defaultNamespace, "", podLabel2,
				[]v1.PodAffinityTerm{
					{
						LabelSelector: &metav1.LabelSelector{
							MatchExpressions: []metav1.LabelSelectorRequirement{
								{
									Key:      "service",
									Operator: metav1.LabelSelectorOpNotIn,
									Values:   []string{"securityscan3", "value3"},
								},
							},
						},
						TopologyKey: "region",
					},
				}, nil),
			pods: []*v1.Pod{{Spec: v1.PodSpec{NodeName: "machine1"}, ObjectMeta: metav1.ObjectMeta{Labels: podLabel}}},
			node: &node1,
			name: "satisfies the pod with requiredDuringSchedulingIgnoredDuringExecution in PodAffinity using not in operator in labelSelector that matches the existing pod",
		},
		{
			pod: createPodWithAffinityTerms(defaultNamespace, "", podLabel2,
				[]v1.PodAffinityTerm{
					{
						LabelSelector: &metav1.LabelSelector{
							MatchExpressions: []metav1.LabelSelectorRequirement{
								{
									Key:      "service",
									Operator: metav1.LabelSelectorOpIn,
									Values:   []string{"securityscan", "value2"},
								},
							},
						},
						Namespaces: []string{"DiffNameSpace"},
					},
				}, nil),
			pods: []*v1.Pod{{Spec: v1.PodSpec{NodeName: "machine1"}, ObjectMeta: metav1.ObjectMeta{Labels: podLabel, Namespace: "ns"}}},
			node: &node1,
			name: "Does not satisfy the PodAffinity with labelSelector because of diff Namespace",
			wantStatus: framework.NewStatus(
				framework.UnschedulableAndUnresolvable,
				utils.ErrReasonAffinityNotMatch,
				utils.ErrReasonAffinityRulesNotMatch,
			),
		},
		{
			pod: createPodWithAffinityTerms(defaultNamespace, "", podLabel,
				[]v1.PodAffinityTerm{
					{
						LabelSelector: &metav1.LabelSelector{
							MatchExpressions: []metav1.LabelSelectorRequirement{
								{
									Key:      "service",
									Operator: metav1.LabelSelectorOpIn,
									Values:   []string{"antivirusscan", "value2"},
								},
							},
						},
					},
				}, nil),
			pods: []*v1.Pod{{Spec: v1.PodSpec{NodeName: "machine1"}, ObjectMeta: metav1.ObjectMeta{Labels: podLabel}}},
			node: &node1,
			name: "Doesn't satisfy the PodAffinity because of unmatching labelSelector with the existing pod",
			wantStatus: framework.NewStatus(
				framework.UnschedulableAndUnresolvable,
				utils.ErrReasonAffinityNotMatch,
				utils.ErrReasonAffinityRulesNotMatch,
			),
		},
		{
			pod: createPodWithAffinityTerms(defaultNamespace, "", podLabel2,
				[]v1.PodAffinityTerm{
					{
						LabelSelector: &metav1.LabelSelector{
							MatchExpressions: []metav1.LabelSelectorRequirement{
								{
									Key:      "service",
									Operator: metav1.LabelSelectorOpExists,
								}, {
									Key:      "wrongkey",
									Operator: metav1.LabelSelectorOpDoesNotExist,
								},
							},
						},
						TopologyKey: "region",
					}, {
						LabelSelector: &metav1.LabelSelector{
							MatchExpressions: []metav1.LabelSelectorRequirement{
								{
									Key:      "service",
									Operator: metav1.LabelSelectorOpIn,
									Values:   []string{"securityscan"},
								}, {
									Key:      "service",
									Operator: metav1.LabelSelectorOpNotIn,
									Values:   []string{"WrongValue"},
								},
							},
						},
						TopologyKey: "region",
					},
				}, nil),
			pods: []*v1.Pod{{Spec: v1.PodSpec{NodeName: "machine1"}, ObjectMeta: metav1.ObjectMeta{Labels: podLabel}}},
			node: &node1,
			name: "satisfies the PodAffinity with different label Operators in multiple RequiredDuringSchedulingIgnoredDuringExecution ",
		},
		{
			pod: createPodWithAffinityTerms(defaultNamespace, "", podLabel2,
				[]v1.PodAffinityTerm{
					{
						LabelSelector: &metav1.LabelSelector{
							MatchExpressions: []metav1.LabelSelectorRequirement{
								{
									Key:      "service",
									Operator: metav1.LabelSelectorOpExists,
								}, {
									Key:      "wrongkey",
									Operator: metav1.LabelSelectorOpDoesNotExist,
								},
							},
						},
						TopologyKey: "region",
					}, {
						LabelSelector: &metav1.LabelSelector{
							MatchExpressions: []metav1.LabelSelectorRequirement{
								{
									Key:      "service",
									Operator: metav1.LabelSelectorOpIn,
									Values:   []string{"securityscan2"},
								}, {
									Key:      "service",
									Operator: metav1.LabelSelectorOpNotIn,
									Values:   []string{"WrongValue"},
								},
							},
						},
						TopologyKey: "region",
					},
				}, nil),
			pods: []*v1.Pod{{Spec: v1.PodSpec{NodeName: "machine1"}, ObjectMeta: metav1.ObjectMeta{Labels: podLabel}}},
			node: &node1,
			name: "The labelSelector requirements(items of matchExpressions) are ANDed, the pod cannot schedule onto the node because one of the matchExpression item don't match.",
			wantStatus: framework.NewStatus(
				framework.UnschedulableAndUnresolvable,
				utils.ErrReasonAffinityNotMatch,
				utils.ErrReasonAffinityRulesNotMatch,
			),
		},
		{
			pod: createPodWithAffinityTerms(defaultNamespace, "", podLabel2,
				[]v1.PodAffinityTerm{
					{
						LabelSelector: &metav1.LabelSelector{
							MatchExpressions: []metav1.LabelSelectorRequirement{
								{
									Key:      "service",
									Operator: metav1.LabelSelectorOpIn,
									Values:   []string{"securityscan", "value2"},
								},
							},
						},
						TopologyKey: "region",
					},
				},
				[]v1.PodAffinityTerm{
					{
						LabelSelector: &metav1.LabelSelector{
							MatchExpressions: []metav1.LabelSelectorRequirement{
								{
									Key:      "service",
									Operator: metav1.LabelSelectorOpIn,
									Values:   []string{"antivirusscan", "value2"},
								},
							},
						},
						TopologyKey: "node",
					},
				}),
			pods: []*v1.Pod{{Spec: v1.PodSpec{NodeName: "machine1"}, ObjectMeta: metav1.ObjectMeta{Labels: podLabel}}},
			node: &node1,
			name: "satisfies the PodAffinity and PodAntiAffinity with the existing pod",
		},
		{
			pod: createPodWithAffinityTerms(defaultNamespace, "", podLabel2,
				[]v1.PodAffinityTerm{
					{
						LabelSelector: &metav1.LabelSelector{
							MatchExpressions: []metav1.LabelSelectorRequirement{
								{
									Key:      "service",
									Operator: metav1.LabelSelectorOpIn,
									Values:   []string{"securityscan", "value2"},
								},
							},
						},
						TopologyKey: "region",
					},
				},
				[]v1.PodAffinityTerm{
					{
						LabelSelector: &metav1.LabelSelector{
							MatchExpressions: []metav1.LabelSelectorRequirement{
								{
									Key:      "service",
									Operator: metav1.LabelSelectorOpIn,
									Values:   []string{"antivirusscan", "value2"},
								},
							},
						},
						TopologyKey: "node",
					},
				}),
			pods: []*v1.Pod{
				createPodWithAffinityTerms(defaultNamespace, "machine1", podLabel, nil,
					[]v1.PodAffinityTerm{
						{
							LabelSelector: &metav1.LabelSelector{
								MatchExpressions: []metav1.LabelSelectorRequirement{
									{
										Key:      "service",
										Operator: metav1.LabelSelectorOpIn,
										Values:   []string{"antivirusscan", "value2"},
									},
								},
							},
							TopologyKey: "node",
						},
					}),
			},
			node: &node1,
			name: "satisfies the PodAffinity and PodAntiAffinity and PodAntiAffinity symmetry with the existing pod",
		},
		{
			pod: createPodWithAffinityTerms(defaultNamespace, "", podLabel2,
				[]v1.PodAffinityTerm{
					{
						LabelSelector: &metav1.LabelSelector{
							MatchExpressions: []metav1.LabelSelectorRequirement{
								{
									Key:      "service",
									Operator: metav1.LabelSelectorOpIn,
									Values:   []string{"securityscan", "value2"},
								},
							},
						},
						TopologyKey: "region",
					},
				},
				[]v1.PodAffinityTerm{
					{
						LabelSelector: &metav1.LabelSelector{
							MatchExpressions: []metav1.LabelSelectorRequirement{
								{
									Key:      "service",
									Operator: metav1.LabelSelectorOpIn,
									Values:   []string{"securityscan", "value2"},
								},
							},
						},
						TopologyKey: "zone",
					},
				}),
			pods: []*v1.Pod{{Spec: v1.PodSpec{NodeName: "machine1"}, ObjectMeta: metav1.ObjectMeta{Labels: podLabel}}},
			node: &node1,
			name: "satisfies the PodAffinity but doesn't satisfy the PodAntiAffinity with the existing pod",
			wantStatus: framework.NewStatus(
				framework.Unschedulable,
				utils.ErrReasonAffinityNotMatch,
				utils.ErrReasonAntiAffinityRulesNotMatch,
			),
		},
		{
			pod: createPodWithAffinityTerms(defaultNamespace, "", podLabel,
				[]v1.PodAffinityTerm{
					{
						LabelSelector: &metav1.LabelSelector{
							MatchExpressions: []metav1.LabelSelectorRequirement{
								{
									Key:      "service",
									Operator: metav1.LabelSelectorOpIn,
									Values:   []string{"securityscan", "value2"},
								},
							},
						},
						TopologyKey: "region",
					},
				},
				[]v1.PodAffinityTerm{
					{
						LabelSelector: &metav1.LabelSelector{
							MatchExpressions: []metav1.LabelSelectorRequirement{
								{
									Key:      "service",
									Operator: metav1.LabelSelectorOpIn,
									Values:   []string{"antivirusscan", "value2"},
								},
							},
						},
						TopologyKey: "node",
					},
				}),
			pods: []*v1.Pod{
				createPodWithAffinityTerms(defaultNamespace, "machine1", podLabel, nil,
					[]v1.PodAffinityTerm{
						{
							LabelSelector: &metav1.LabelSelector{
								MatchExpressions: []metav1.LabelSelectorRequirement{
									{
										Key:      "service",
										Operator: metav1.LabelSelectorOpIn,
										Values:   []string{"securityscan", "value2"},
									},
								},
							},
							TopologyKey: "zone",
						},
					}),
			},
			node: &node1,
			name: "satisfies the PodAffinity and PodAntiAffinity but doesn't satisfy PodAntiAffinity symmetry with the existing pod",
			wantStatus: framework.NewStatus(
				framework.Unschedulable,
				utils.ErrReasonAffinityNotMatch,
				utils.ErrReasonExistingAntiAffinityRulesNotMatch,
			),
		},
		{
			pod: createPodWithAffinityTerms(defaultNamespace, "", podLabel,
				[]v1.PodAffinityTerm{
					{
						LabelSelector: &metav1.LabelSelector{
							MatchExpressions: []metav1.LabelSelectorRequirement{
								{
									Key:      "service",
									Operator: metav1.LabelSelectorOpNotIn,
									Values:   []string{"securityscan", "value2"},
								},
							},
						},
						TopologyKey: "region",
					},
				}, nil),
			pods: []*v1.Pod{{Spec: v1.PodSpec{NodeName: "machine2"}, ObjectMeta: metav1.ObjectMeta{Labels: podLabel}}},
			node: &node1,
			name: "pod matches its own Label in PodAffinity and that matches the existing pod Labels",
			wantStatus: framework.NewStatus(
				framework.UnschedulableAndUnresolvable,
				utils.ErrReasonAffinityNotMatch,
				utils.ErrReasonAffinityRulesNotMatch,
			),
		},
		{
			pod: &v1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Labels: podLabel,
				},
			},
			pods: []*v1.Pod{
				createPodWithAffinityTerms(defaultNamespace, "machine1", podLabel, nil,
					[]v1.PodAffinityTerm{
						{
							LabelSelector: &metav1.LabelSelector{
								MatchExpressions: []metav1.LabelSelectorRequirement{
									{
										Key:      "service",
										Operator: metav1.LabelSelectorOpIn,
										Values:   []string{"securityscan", "value2"},
									},
								},
							},
							TopologyKey: "zone",
						},
					}),
			},
			node: &node1,
			name: "verify that PodAntiAffinity from existing pod is respected when pod has no AntiAffinity constraints. doesn't satisfy PodAntiAffinity symmetry with the existing pod",
			wantStatus: framework.NewStatus(
				framework.Unschedulable,
				utils.ErrReasonAffinityNotMatch,
				utils.ErrReasonExistingAntiAffinityRulesNotMatch,
			),
		},
		{
			pod: &v1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Labels: podLabel,
				},
			},
			pods: []*v1.Pod{
				createPodWithAffinityTerms(defaultNamespace, "machine1", podLabel, nil,
					[]v1.PodAffinityTerm{
						{
							LabelSelector: &metav1.LabelSelector{
								MatchExpressions: []metav1.LabelSelectorRequirement{
									{
										Key:      "service",
										Operator: metav1.LabelSelectorOpNotIn,
										Values:   []string{"securityscan", "value2"},
									},
								},
							},
							TopologyKey: "zone",
						},
					}),
			},
			node: &node1,
			name: "verify that PodAntiAffinity from existing pod is respected when pod has no AntiAffinity constraints. satisfy PodAntiAffinity symmetry with the existing pod",
		},
		{
			pod: createPodWithAffinityTerms(defaultNamespace, "", podLabel, nil,
				[]v1.PodAffinityTerm{
					{
						LabelSelector: &metav1.LabelSelector{
							MatchExpressions: []metav1.LabelSelectorRequirement{
								{
									Key:      "service",
									Operator: metav1.LabelSelectorOpExists,
								},
							},
						},
						TopologyKey: "region",
					},
					{
						LabelSelector: &metav1.LabelSelector{
							MatchExpressions: []metav1.LabelSelectorRequirement{
								{
									Key:      "security",
									Operator: metav1.LabelSelectorOpExists,
								},
							},
						},
						TopologyKey: "region",
					},
				}),
			pods: []*v1.Pod{
				createPodWithAffinityTerms(defaultNamespace, "machine1", podLabel2, nil,
					[]v1.PodAffinityTerm{
						{
							LabelSelector: &metav1.LabelSelector{
								MatchExpressions: []metav1.LabelSelectorRequirement{
									{
										Key:      "security",
										Operator: metav1.LabelSelectorOpExists,
									},
								},
							},
							TopologyKey: "zone",
						},
					}),
			},
			node: &node1,
			name: "satisfies the PodAntiAffinity with existing pod but doesn't satisfy PodAntiAffinity symmetry with incoming pod",
			wantStatus: framework.NewStatus(
				framework.Unschedulable,
				utils.ErrReasonAffinityNotMatch,
				utils.ErrReasonAntiAffinityRulesNotMatch,
			),
		},
		{
			pod: createPodWithAffinityTerms(defaultNamespace, "", podLabel, nil,
				[]v1.PodAffinityTerm{
					{
						LabelSelector: &metav1.LabelSelector{
							MatchExpressions: []metav1.LabelSelectorRequirement{
								{
									Key:      "service",
									Operator: metav1.LabelSelectorOpExists,
								},
							},
						},
						TopologyKey: "zone",
					},
					{
						LabelSelector: &metav1.LabelSelector{
							MatchExpressions: []metav1.LabelSelectorRequirement{
								{
									Key:      "security",
									Operator: metav1.LabelSelectorOpExists,
								},
							},
						},
						TopologyKey: "zone",
					},
				}),
			pods: []*v1.Pod{
				createPodWithAffinityTerms(defaultNamespace, "machine1", podLabel2, nil,
					[]v1.PodAffinityTerm{
						{
							LabelSelector: &metav1.LabelSelector{
								MatchExpressions: []metav1.LabelSelectorRequirement{
									{
										Key:      "security",
										Operator: metav1.LabelSelectorOpExists,
									},
								},
							},
							TopologyKey: "zone",
						},
					}),
			},
			node: &node1,
			wantStatus: framework.NewStatus(
				framework.Unschedulable,
				utils.ErrReasonAffinityNotMatch,
				utils.ErrReasonAntiAffinityRulesNotMatch,
			),
			name: "PodAntiAffinity symmetry check a1: incoming pod and existing pod partially match each other on AffinityTerms",
		},
		{
			pod: createPodWithAffinityTerms(defaultNamespace, "", podLabel2, nil,
				[]v1.PodAffinityTerm{
					{
						LabelSelector: &metav1.LabelSelector{
							MatchExpressions: []metav1.LabelSelectorRequirement{
								{
									Key:      "security",
									Operator: metav1.LabelSelectorOpExists,
								},
							},
						},
						TopologyKey: "zone",
					},
				}),
			pods: []*v1.Pod{
				createPodWithAffinityTerms(defaultNamespace, "machine1", podLabel, nil,
					[]v1.PodAffinityTerm{
						{
							LabelSelector: &metav1.LabelSelector{
								MatchExpressions: []metav1.LabelSelectorRequirement{
									{
										Key:      "service",
										Operator: metav1.LabelSelectorOpExists,
									},
								},
							},
							TopologyKey: "zone",
						},
						{
							LabelSelector: &metav1.LabelSelector{
								MatchExpressions: []metav1.LabelSelectorRequirement{
									{
										Key:      "security",
										Operator: metav1.LabelSelectorOpExists,
									},
								},
							},
							TopologyKey: "zone",
						},
					}),
			},
			node: &node1,
			wantStatus: framework.NewStatus(
				framework.Unschedulable,
				utils.ErrReasonAffinityNotMatch,
				utils.ErrReasonExistingAntiAffinityRulesNotMatch,
			),
			name: "PodAntiAffinity symmetry check a2: incoming pod and existing pod partially match each other on AffinityTerms",
		},
		{
			pod: createPodWithAffinityTerms(defaultNamespace, "", map[string]string{"abc": "", "xyz": ""}, nil,
				[]v1.PodAffinityTerm{
					{
						LabelSelector: &metav1.LabelSelector{
							MatchExpressions: []metav1.LabelSelectorRequirement{
								{
									Key:      "abc",
									Operator: metav1.LabelSelectorOpExists,
								},
							},
						},
						TopologyKey: "zone",
					},
					{
						LabelSelector: &metav1.LabelSelector{
							MatchExpressions: []metav1.LabelSelectorRequirement{
								{
									Key:      "def",
									Operator: metav1.LabelSelectorOpExists,
								},
							},
						},
						TopologyKey: "zone",
					},
				}),
			pods: []*v1.Pod{
				createPodWithAffinityTerms(defaultNamespace, "machine1", map[string]string{"def": "", "xyz": ""}, nil,
					[]v1.PodAffinityTerm{
						{
							LabelSelector: &metav1.LabelSelector{
								MatchExpressions: []metav1.LabelSelectorRequirement{
									{
										Key:      "abc",
										Operator: metav1.LabelSelectorOpExists,
									},
								},
							},
							TopologyKey: "zone",
						},
						{
							LabelSelector: &metav1.LabelSelector{
								MatchExpressions: []metav1.LabelSelectorRequirement{
									{
										Key:      "def",
										Operator: metav1.LabelSelectorOpExists,
									},
								},
							},
							TopologyKey: "zone",
						},
					}),
			},
			node: &node1,
			wantStatus: framework.NewStatus(
				framework.Unschedulable,
				utils.ErrReasonAffinityNotMatch,
				utils.ErrReasonAntiAffinityRulesNotMatch,
			),
			name: "PodAntiAffinity symmetry check b1: incoming pod and existing pod partially match each other on AffinityTerms",
		},
		{
			pod: createPodWithAffinityTerms(defaultNamespace, "", map[string]string{"def": "", "xyz": ""}, nil,
				[]v1.PodAffinityTerm{
					{
						LabelSelector: &metav1.LabelSelector{
							MatchExpressions: []metav1.LabelSelectorRequirement{
								{
									Key:      "abc",
									Operator: metav1.LabelSelectorOpExists,
								},
							},
						},
						TopologyKey: "zone",
					},
					{
						LabelSelector: &metav1.LabelSelector{
							MatchExpressions: []metav1.LabelSelectorRequirement{
								{
									Key:      "def",
									Operator: metav1.LabelSelectorOpExists,
								},
							},
						},
						TopologyKey: "zone",
					},
				}),
			pods: []*v1.Pod{
				createPodWithAffinityTerms(defaultNamespace, "machine1", map[string]string{"abc": "", "xyz": ""}, nil,
					[]v1.PodAffinityTerm{
						{
							LabelSelector: &metav1.LabelSelector{
								MatchExpressions: []metav1.LabelSelectorRequirement{
									{
										Key:      "abc",
										Operator: metav1.LabelSelectorOpExists,
									},
								},
							},
							TopologyKey: "zone",
						},
						{
							LabelSelector: &metav1.LabelSelector{
								MatchExpressions: []metav1.LabelSelectorRequirement{
									{
										Key:      "def",
										Operator: metav1.LabelSelectorOpExists,
									},
								},
							},
							TopologyKey: "zone",
						},
					}),
			},
			node: &node1,
			wantStatus: framework.NewStatus(
				framework.Unschedulable,
				utils.ErrReasonAffinityNotMatch,
				utils.ErrReasonAntiAffinityRulesNotMatch,
			),
			name: "PodAntiAffinity symmetry check b2: incoming pod and existing pod partially match each other on AffinityTerms",
		},
		{
			name: "PodAffinity fails PreFilter with an invalid affinity label syntax",
			pod: createPodWithAffinityTerms(defaultNamespace, "", podLabel,
				[]v1.PodAffinityTerm{
					{
						LabelSelector: &metav1.LabelSelector{
							MatchExpressions: []metav1.LabelSelectorRequirement{
								{
									Key:      "service",
									Operator: metav1.LabelSelectorOpIn,
									Values:   []string{"{{.bad-value.}}"},
								},
							},
						},
						TopologyKey: "region",
					},
				},
				[]v1.PodAffinityTerm{
					{
						LabelSelector: &metav1.LabelSelector{
							MatchExpressions: []metav1.LabelSelectorRequirement{
								{
									Key:      "service",
									Operator: metav1.LabelSelectorOpIn,
									Values:   []string{"antivirusscan", "value2"},
								},
							},
						},
						TopologyKey: "node",
					},
				}),
			node: &node1,
			wantStatus: framework.NewStatus(
				framework.UnschedulableAndUnresolvable,
				"Invalid value",
			),
		},
		{
			name: "PodAntiAffinity fails PreFilter with an invalid antiaffinity label syntax",
			pod: createPodWithAffinityTerms(defaultNamespace, "", podLabel,
				[]v1.PodAffinityTerm{
					{
						LabelSelector: &metav1.LabelSelector{
							MatchExpressions: []metav1.LabelSelectorRequirement{
								{
									Key:      "service",
									Operator: metav1.LabelSelectorOpIn,
									Values:   []string{"foo"},
								},
							},
						},
						TopologyKey: "region",
					},
				},
				[]v1.PodAffinityTerm{
					{
						LabelSelector: &metav1.LabelSelector{
							MatchExpressions: []metav1.LabelSelectorRequirement{
								{
									Key:      "service",
									Operator: metav1.LabelSelectorOpIn,
									Values:   []string{"{{.bad-value.}}"},
								},
							},
						},
						TopologyKey: "node",
					},
				}),
			node: &node1,
			wantStatus: framework.NewStatus(
				framework.UnschedulableAndUnresolvable,
				"Invalid value",
			),
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			snapshot := framework_helper.MakeSnapShot(test.pods, []*v1.Node{test.node}, nil)

			p := &InterPodAffinity{
				sharedLister: snapshot,
			}
			state := framework.NewCycleState()
			preFilterStatus := p.PreFilter(context.Background(), state, test.pod)
			if !preFilterStatus.IsSuccess() {
				if !strings.Contains(preFilterStatus.Message(), test.wantStatus.Message()) {
					t.Errorf("prefilter failed with status: %v", preFilterStatus)
				}
			} else {
				nodeInfo := mustGetNodeInfo(t, snapshot, test.node.Name)
				gotStatus := p.Filter(context.Background(), state, test.pod, nodeInfo)
				if !reflect.DeepEqual(gotStatus, test.wantStatus) {
					t.Errorf("status does not match: %v, want: %v", gotStatus, test.wantStatus)
				}
			}
		})
	}
}

func TestRequiredAffinityMultipleNodes(t *testing.T) {
	podLabelA := map[string]string{
		"foo": "bar",
	}
	labelRgChina := map[string]string{
		"region": "China",
	}
	labelRgChinaAzAz1 := map[string]string{
		"region": "China",
		"az":     "az1",
	}
	labelRgIndia := map[string]string{
		"region": "India",
	}

	tests := []struct {
		pod          *v1.Pod
		pods         []*v1.Pod
		nodes        []*v1.Node
		wantStatuses []*framework.Status
		name         string
	}{
		{
			pod: createPodWithAffinityTerms(defaultNamespace, "", nil,
				[]v1.PodAffinityTerm{
					{
						LabelSelector: &metav1.LabelSelector{
							MatchExpressions: []metav1.LabelSelectorRequirement{
								{
									Key:      "foo",
									Operator: metav1.LabelSelectorOpIn,
									Values:   []string{"bar"},
								},
							},
						},
						TopologyKey: "region",
					},
				}, nil),
			pods: []*v1.Pod{
				{Spec: v1.PodSpec{NodeName: "machine1"}, ObjectMeta: metav1.ObjectMeta{Name: "p1", Labels: podLabelA}},
			},
			nodes: []*v1.Node{
				{ObjectMeta: metav1.ObjectMeta{Name: "machine1", Labels: labelRgChina}},
				{ObjectMeta: metav1.ObjectMeta{Name: "machine2", Labels: labelRgChinaAzAz1}},
				{ObjectMeta: metav1.ObjectMeta{Name: "machine3", Labels: labelRgIndia}},
			},
			wantStatuses: []*framework.Status{
				nil,
				nil,
				framework.NewStatus(
					framework.UnschedulableAndUnresolvable,
					utils.ErrReasonAffinityNotMatch,
					utils.ErrReasonAffinityRulesNotMatch,
				),
			},
			name: "A pod can be scheduled onto all the nodes that have the same topology key & label value with one of them has an existing pod that matches the affinity rules",
		},
		{
			pod: createPodWithAffinityTerms(defaultNamespace, "", map[string]string{"foo": "bar", "service": "securityscan"},
				[]v1.PodAffinityTerm{
					{
						LabelSelector: &metav1.LabelSelector{
							MatchExpressions: []metav1.LabelSelectorRequirement{
								{
									Key:      "foo",
									Operator: metav1.LabelSelectorOpIn,
									Values:   []string{"bar"},
								},
							},
						},
						TopologyKey: "zone",
					},
					{
						LabelSelector: &metav1.LabelSelector{
							MatchExpressions: []metav1.LabelSelectorRequirement{
								{
									Key:      "service",
									Operator: metav1.LabelSelectorOpIn,
									Values:   []string{"securityscan"},
								},
							},
						},
						TopologyKey: "zone",
					},
				}, nil),
			pods: []*v1.Pod{{Spec: v1.PodSpec{NodeName: "nodeA"}, ObjectMeta: metav1.ObjectMeta{Name: "p1", Labels: map[string]string{"foo": "bar"}}}},
			nodes: []*v1.Node{
				{ObjectMeta: metav1.ObjectMeta{Name: "nodeA", Labels: map[string]string{"zone": "az1", "hostname": "h1"}}},
				{ObjectMeta: metav1.ObjectMeta{Name: "nodeB", Labels: map[string]string{"zone": "az2", "hostname": "h2"}}},
			},
			wantStatuses: []*framework.Status{nil, nil},
			name: "The affinity rule is to schedule all of the pods of this collection to the same zone. The first pod of the collection " +
				"should not be blocked from being scheduled onto any node, even there's no existing pod that matches the rule anywhere.",
		},
		{
			pod: createPodWithAffinityTerms(defaultNamespace, "", map[string]string{"foo": "bar", "service": "securityscan"},
				[]v1.PodAffinityTerm{
					{
						LabelSelector: &metav1.LabelSelector{
							MatchExpressions: []metav1.LabelSelectorRequirement{
								{
									Key:      "foo",
									Operator: metav1.LabelSelectorOpIn,
									Values:   []string{"bar"},
								},
							},
						},
						TopologyKey: "zone",
					},
					{
						LabelSelector: &metav1.LabelSelector{
							MatchExpressions: []metav1.LabelSelectorRequirement{
								{
									Key:      "service",
									Operator: metav1.LabelSelectorOpIn,
									Values:   []string{"securityscan"},
								},
							},
						},
						TopologyKey: "zone",
					},
				}, nil),
			pods: []*v1.Pod{{Spec: v1.PodSpec{NodeName: "nodeA"}, ObjectMeta: metav1.ObjectMeta{Name: "p1", Labels: map[string]string{"foo": "bar"}}}},
			nodes: []*v1.Node{
				{ObjectMeta: metav1.ObjectMeta{Name: "nodeA", Labels: map[string]string{"zoneLabel": "az1", "hostname": "h1"}}},
				{ObjectMeta: metav1.ObjectMeta{Name: "nodeB", Labels: map[string]string{"zoneLabel": "az2", "hostname": "h2"}}},
			},
			wantStatuses: []*framework.Status{
				framework.NewStatus(
					framework.UnschedulableAndUnresolvable,
					utils.ErrReasonAffinityNotMatch,
					utils.ErrReasonAffinityRulesNotMatch,
				),
				framework.NewStatus(
					framework.UnschedulableAndUnresolvable,
					utils.ErrReasonAffinityNotMatch,
					utils.ErrReasonAffinityRulesNotMatch,
				),
			},
			name: "The first pod of the collection can only be scheduled on nodes labelled with the requested topology keys",
		},
		{
			pod: createPodWithAffinityTerms(defaultNamespace, "", nil, nil,
				[]v1.PodAffinityTerm{
					{
						LabelSelector: &metav1.LabelSelector{
							MatchExpressions: []metav1.LabelSelectorRequirement{
								{
									Key:      "foo",
									Operator: metav1.LabelSelectorOpIn,
									Values:   []string{"abc"},
								},
							},
						},
						TopologyKey: "region",
					},
				}),
			pods: []*v1.Pod{
				{Spec: v1.PodSpec{NodeName: "nodeA"}, ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{"foo": "abc"}}},
			},
			nodes: []*v1.Node{
				{ObjectMeta: metav1.ObjectMeta{Name: "nodeA", Labels: map[string]string{"region": "r1", "hostname": "nodeA"}}},
				{ObjectMeta: metav1.ObjectMeta{Name: "nodeB", Labels: map[string]string{"region": "r1", "hostname": "nodeB"}}},
			},
			wantStatuses: []*framework.Status{
				framework.NewStatus(
					framework.Unschedulable,
					utils.ErrReasonAffinityNotMatch,
					utils.ErrReasonAntiAffinityRulesNotMatch,
				),
				framework.NewStatus(
					framework.Unschedulable,
					utils.ErrReasonAffinityNotMatch,
					utils.ErrReasonAntiAffinityRulesNotMatch,
				),
			},
			name: "NodeA and nodeB have same topologyKey and label value. NodeA has an existing pod that matches the inter pod affinity rule. The pod can not be scheduled onto nodeA and nodeB.",
		},
		{
			pod: createPodWithAffinityTerms(defaultNamespace, "", nil, nil,
				[]v1.PodAffinityTerm{
					{
						LabelSelector: &metav1.LabelSelector{
							MatchExpressions: []metav1.LabelSelectorRequirement{
								{
									Key:      "foo",
									Operator: metav1.LabelSelectorOpIn,
									Values:   []string{"abc"},
								},
							},
						},
						TopologyKey: "region",
					},
					{
						LabelSelector: &metav1.LabelSelector{
							MatchExpressions: []metav1.LabelSelectorRequirement{
								{
									Key:      "service",
									Operator: metav1.LabelSelectorOpIn,
									Values:   []string{"securityscan"},
								},
							},
						},
						TopologyKey: "zone",
					},
				}),
			pods: []*v1.Pod{
				{Spec: v1.PodSpec{NodeName: "nodeA"}, ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{"foo": "abc", "service": "securityscan"}}},
			},
			nodes: []*v1.Node{
				{ObjectMeta: metav1.ObjectMeta{Name: "nodeA", Labels: map[string]string{"region": "r1", "zone": "z1", "hostname": "nodeA"}}},
				{ObjectMeta: metav1.ObjectMeta{Name: "nodeB", Labels: map[string]string{"region": "r1", "zone": "z2", "hostname": "nodeB"}}},
			},
			wantStatuses: []*framework.Status{
				framework.NewStatus(
					framework.Unschedulable,
					utils.ErrReasonAffinityNotMatch,
					utils.ErrReasonAntiAffinityRulesNotMatch,
				),
				framework.NewStatus(
					framework.Unschedulable,
					utils.ErrReasonAffinityNotMatch,
					utils.ErrReasonAntiAffinityRulesNotMatch,
				),
			},
			name: "This test ensures that anti-affinity matches a pod when any term of the anti-affinity rule matches a pod.",
		},
		{
			pod: createPodWithAffinityTerms(defaultNamespace, "", nil, nil,
				[]v1.PodAffinityTerm{
					{
						LabelSelector: &metav1.LabelSelector{
							MatchExpressions: []metav1.LabelSelectorRequirement{
								{
									Key:      "foo",
									Operator: metav1.LabelSelectorOpIn,
									Values:   []string{"abc"},
								},
							},
						},
						TopologyKey: "region",
					},
				}),
			pods: []*v1.Pod{
				{Spec: v1.PodSpec{NodeName: "nodeA"}, ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{"foo": "abc"}}},
			},
			nodes: []*v1.Node{
				{ObjectMeta: metav1.ObjectMeta{Name: "nodeA", Labels: labelRgChina}},
				{ObjectMeta: metav1.ObjectMeta{Name: "nodeB", Labels: labelRgChinaAzAz1}},
				{ObjectMeta: metav1.ObjectMeta{Name: "nodeC", Labels: labelRgIndia}},
			},
			wantStatuses: []*framework.Status{
				framework.NewStatus(
					framework.Unschedulable,
					utils.ErrReasonAffinityNotMatch,
					utils.ErrReasonAntiAffinityRulesNotMatch,
				),
				framework.NewStatus(
					framework.Unschedulable,
					utils.ErrReasonAffinityNotMatch,
					utils.ErrReasonAntiAffinityRulesNotMatch,
				),
				nil,
			},
			name: "NodeA and nodeB have same topologyKey and label value. NodeA has an existing pod that matches the inter pod affinity rule. The pod can not be scheduled onto nodeA and nodeB but can be scheduled onto nodeC",
		},
		{
			pod: createPodWithAffinityTerms("NS1", "", map[string]string{"foo": "123"}, nil,
				[]v1.PodAffinityTerm{
					{
						LabelSelector: &metav1.LabelSelector{
							MatchExpressions: []metav1.LabelSelectorRequirement{
								{
									Key:      "foo",
									Operator: metav1.LabelSelectorOpIn,
									Values:   []string{"bar"},
								},
							},
						},
						TopologyKey: "region",
					},
				}),
			pods: []*v1.Pod{
				{
					ObjectMeta: metav1.ObjectMeta{
						Labels:    map[string]string{"foo": "bar"},
						Namespace: "NS1",
					},
					Spec: v1.PodSpec{NodeName: "nodeA"},
				},
				createPodWithAffinityTerms("NS2", "nodeC", nil, nil,
					[]v1.PodAffinityTerm{
						{
							LabelSelector: &metav1.LabelSelector{
								MatchExpressions: []metav1.LabelSelectorRequirement{
									{
										Key:      "foo",
										Operator: metav1.LabelSelectorOpIn,
										Values:   []string{"123"},
									},
								},
							},
							TopologyKey: "region",
						},
					}),
			},
			nodes: []*v1.Node{
				{ObjectMeta: metav1.ObjectMeta{Name: "nodeA", Labels: labelRgChina}},
				{ObjectMeta: metav1.ObjectMeta{Name: "nodeB", Labels: labelRgChinaAzAz1}},
				{ObjectMeta: metav1.ObjectMeta{Name: "nodeC", Labels: labelRgIndia}},
			},
			wantStatuses: []*framework.Status{
				framework.NewStatus(
					framework.Unschedulable,
					utils.ErrReasonAffinityNotMatch,
					utils.ErrReasonAntiAffinityRulesNotMatch,
				),
				framework.NewStatus(
					framework.Unschedulable,
					utils.ErrReasonAffinityNotMatch,
					utils.ErrReasonAntiAffinityRulesNotMatch,
				),
				nil,
			},
			name: "NodeA and nodeB have same topologyKey and label value. NodeA has an existing pod that matches the inter pod affinity rule. The pod can not be scheduled onto nodeA, nodeB, but can be scheduled onto nodeC (NodeC has an existing pod that match the inter pod affinity rule but in different namespace)",
		},
		{
			pod: &v1.Pod{
				ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{"foo": ""}},
			},
			pods: []*v1.Pod{
				createPodWithAffinityTerms(defaultNamespace, "nodeA", nil, nil,
					[]v1.PodAffinityTerm{
						{
							LabelSelector: &metav1.LabelSelector{
								MatchExpressions: []metav1.LabelSelectorRequirement{
									{
										Key:      "foo",
										Operator: metav1.LabelSelectorOpExists,
									},
								},
							},
							TopologyKey: "invalid-node-label",
						},
					}),
			},
			nodes: []*v1.Node{
				{ObjectMeta: metav1.ObjectMeta{Name: "nodeA", Labels: map[string]string{"region": "r1", "zone": "z1", "hostname": "nodeA"}}},
				{ObjectMeta: metav1.ObjectMeta{Name: "nodeB", Labels: map[string]string{"region": "r1", "zone": "z1", "hostname": "nodeB"}}},
			},
			wantStatuses: []*framework.Status{nil, nil},
			name:         "Test existing pod's anti-affinity: if an existing pod has a term with invalid topologyKey, labelSelector of the term is firstly checked, and then topologyKey of the term is also checked",
		},
		{
			pod: createPodWithAffinityTerms(defaultNamespace, "", nil, nil,
				[]v1.PodAffinityTerm{
					{
						LabelSelector: &metav1.LabelSelector{
							MatchExpressions: []metav1.LabelSelectorRequirement{
								{
									Key:      "foo",
									Operator: metav1.LabelSelectorOpExists,
								},
							},
						},
						TopologyKey: "invalid-node-label",
					},
				}),
			pods: []*v1.Pod{
				{
					ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{"foo": ""}},
					Spec: v1.PodSpec{
						NodeName: "nodeA",
					},
				},
			},
			nodes: []*v1.Node{
				{ObjectMeta: metav1.ObjectMeta{Name: "nodeA", Labels: map[string]string{"region": "r1", "zone": "z1", "hostname": "nodeA"}}},
				{ObjectMeta: metav1.ObjectMeta{Name: "nodeB", Labels: map[string]string{"region": "r1", "zone": "z1", "hostname": "nodeB"}}},
			},
			wantStatuses: []*framework.Status{nil, nil},
			name:         "Test incoming pod's anti-affinity: even if labelSelector matches, we still check if topologyKey matches",
		},
		{
			pod: &v1.Pod{
				ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{"foo": "", "bar": ""}},
			},
			pods: []*v1.Pod{
				createPodWithAffinityTerms(defaultNamespace, "nodeA", nil, nil,
					[]v1.PodAffinityTerm{
						{
							LabelSelector: &metav1.LabelSelector{
								MatchExpressions: []metav1.LabelSelectorRequirement{
									{
										Key:      "foo",
										Operator: metav1.LabelSelectorOpExists,
									},
								},
							},
							TopologyKey: "zone",
						},
					}),
				createPodWithAffinityTerms(defaultNamespace, "nodeA", nil, nil,
					[]v1.PodAffinityTerm{
						{
							LabelSelector: &metav1.LabelSelector{
								MatchExpressions: []metav1.LabelSelectorRequirement{
									{
										Key:      "bar",
										Operator: metav1.LabelSelectorOpExists,
									},
								},
							},
							TopologyKey: "region",
						},
					}),
			},
			nodes: []*v1.Node{
				{ObjectMeta: metav1.ObjectMeta{Name: "nodeA", Labels: map[string]string{"region": "r1", "zone": "z1", "hostname": "nodeA"}}},
				{ObjectMeta: metav1.ObjectMeta{Name: "nodeB", Labels: map[string]string{"region": "r1", "zone": "z2", "hostname": "nodeB"}}},
			},
			wantStatuses: []*framework.Status{
				framework.NewStatus(
					framework.Unschedulable,
					utils.ErrReasonAffinityNotMatch,
					utils.ErrReasonExistingAntiAffinityRulesNotMatch,
				),
				framework.NewStatus(
					framework.Unschedulable,
					utils.ErrReasonAffinityNotMatch,
					utils.ErrReasonExistingAntiAffinityRulesNotMatch,
				),
			},
			name: "Test existing pod's anti-affinity: incoming pod wouldn't considered as a fit as it violates each existingPod's terms on all nodes",
		},
		{
			pod: createPodWithAffinityTerms(defaultNamespace, "", nil, nil,
				[]v1.PodAffinityTerm{
					{
						LabelSelector: &metav1.LabelSelector{
							MatchExpressions: []metav1.LabelSelectorRequirement{
								{
									Key:      "foo",
									Operator: metav1.LabelSelectorOpExists,
								},
							},
						},
						TopologyKey: "zone",
					},
					{
						LabelSelector: &metav1.LabelSelector{
							MatchExpressions: []metav1.LabelSelectorRequirement{
								{
									Key:      "bar",
									Operator: metav1.LabelSelectorOpExists,
								},
							},
						},
						TopologyKey: "region",
					},
				}),
			pods: []*v1.Pod{
				{
					ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{"foo": ""}},
					Spec: v1.PodSpec{
						NodeName: "nodeA",
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{"bar": ""}},
					Spec: v1.PodSpec{
						NodeName: "nodeB",
					},
				},
			},
			nodes: []*v1.Node{
				{ObjectMeta: metav1.ObjectMeta{Name: "nodeA", Labels: map[string]string{"region": "r1", "zone": "z1", "hostname": "nodeA"}}},
				{ObjectMeta: metav1.ObjectMeta{Name: "nodeB", Labels: map[string]string{"region": "r1", "zone": "z2", "hostname": "nodeB"}}},
			},
			wantStatuses: []*framework.Status{
				framework.NewStatus(
					framework.Unschedulable,
					utils.ErrReasonAffinityNotMatch,
					utils.ErrReasonAntiAffinityRulesNotMatch,
				),
				framework.NewStatus(
					framework.Unschedulable,
					utils.ErrReasonAffinityNotMatch,
					utils.ErrReasonAntiAffinityRulesNotMatch,
				),
			},
			name: "Test incoming pod's anti-affinity: incoming pod wouldn't considered as a fit as it at least violates one anti-affinity rule of existingPod",
		},
		{
			pod: &v1.Pod{
				ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{"foo": "", "bar": ""}},
			},
			pods: []*v1.Pod{
				createPodWithAffinityTerms(defaultNamespace, "nodeA", nil, nil,
					[]v1.PodAffinityTerm{
						{
							LabelSelector: &metav1.LabelSelector{
								MatchExpressions: []metav1.LabelSelectorRequirement{
									{
										Key:      "foo",
										Operator: metav1.LabelSelectorOpExists,
									},
								},
							},
							TopologyKey: "invalid-node-label",
						},
						{
							LabelSelector: &metav1.LabelSelector{
								MatchExpressions: []metav1.LabelSelectorRequirement{
									{
										Key:      "bar",
										Operator: metav1.LabelSelectorOpExists,
									},
								},
							},
							TopologyKey: "zone",
						},
					}),
			},
			nodes: []*v1.Node{
				{ObjectMeta: metav1.ObjectMeta{Name: "nodeA", Labels: map[string]string{"region": "r1", "zone": "z1", "hostname": "nodeA"}}},
				{ObjectMeta: metav1.ObjectMeta{Name: "nodeB", Labels: map[string]string{"region": "r1", "zone": "z2", "hostname": "nodeB"}}},
			},
			wantStatuses: []*framework.Status{
				framework.NewStatus(
					framework.Unschedulable,
					utils.ErrReasonAffinityNotMatch,
					utils.ErrReasonExistingAntiAffinityRulesNotMatch,
				),
				nil,
			},
			name: "Test existing pod's anti-affinity: only when labelSelector and topologyKey both match, it's counted as a single term match - case when one term has invalid topologyKey",
		},
		{
			pod: createPodWithAffinityTerms(defaultNamespace, "", nil, nil,
				[]v1.PodAffinityTerm{
					{
						LabelSelector: &metav1.LabelSelector{
							MatchExpressions: []metav1.LabelSelectorRequirement{
								{
									Key:      "foo",
									Operator: metav1.LabelSelectorOpExists,
								},
							},
						},
						TopologyKey: "invalid-node-label",
					},
					{
						LabelSelector: &metav1.LabelSelector{
							MatchExpressions: []metav1.LabelSelectorRequirement{
								{
									Key:      "bar",
									Operator: metav1.LabelSelectorOpExists,
								},
							},
						},
						TopologyKey: "zone",
					},
				}),
			pods: []*v1.Pod{
				{
					ObjectMeta: metav1.ObjectMeta{Name: "podA", Labels: map[string]string{"foo": "", "bar": ""}},
					Spec: v1.PodSpec{
						NodeName: "nodeA",
					},
				},
			},
			nodes: []*v1.Node{
				{ObjectMeta: metav1.ObjectMeta{Name: "nodeA", Labels: map[string]string{"region": "r1", "zone": "z1", "hostname": "nodeA"}}},
				{ObjectMeta: metav1.ObjectMeta{Name: "nodeB", Labels: map[string]string{"region": "r1", "zone": "z2", "hostname": "nodeB"}}},
			},
			wantStatuses: []*framework.Status{
				framework.NewStatus(
					framework.Unschedulable,
					utils.ErrReasonAffinityNotMatch,
					utils.ErrReasonAntiAffinityRulesNotMatch,
				),
				nil,
			},
			name: "Test incoming pod's anti-affinity: only when labelSelector and topologyKey both match, it's counted as a single term match - case when one term has invalid topologyKey",
		},
		{
			pod: &v1.Pod{
				ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{"foo": "", "bar": ""}},
			},
			pods: []*v1.Pod{
				createPodWithAffinityTerms(defaultNamespace, "nodeA", nil, nil,
					[]v1.PodAffinityTerm{
						{
							LabelSelector: &metav1.LabelSelector{
								MatchExpressions: []metav1.LabelSelectorRequirement{
									{
										Key:      "foo",
										Operator: metav1.LabelSelectorOpExists,
									},
								},
							},
							TopologyKey: "region",
						},
						{
							LabelSelector: &metav1.LabelSelector{
								MatchExpressions: []metav1.LabelSelectorRequirement{
									{
										Key:      "bar",
										Operator: metav1.LabelSelectorOpExists,
									},
								},
							},
							TopologyKey: "zone",
						},
					}),
			},
			nodes: []*v1.Node{
				{ObjectMeta: metav1.ObjectMeta{Name: "nodeA", Labels: map[string]string{"region": "r1", "zone": "z1", "hostname": "nodeA"}}},
				{ObjectMeta: metav1.ObjectMeta{Name: "nodeB", Labels: map[string]string{"region": "r1", "zone": "z2", "hostname": "nodeB"}}},
			},
			wantStatuses: []*framework.Status{
				framework.NewStatus(
					framework.Unschedulable,
					utils.ErrReasonAffinityNotMatch,
					utils.ErrReasonExistingAntiAffinityRulesNotMatch,
				),
				framework.NewStatus(
					framework.Unschedulable,
					utils.ErrReasonAffinityNotMatch,
					utils.ErrReasonExistingAntiAffinityRulesNotMatch,
				),
			},
			name: "Test existing pod's anti-affinity: only when labelSelector and topologyKey both match, it's counted as a single term match - case when all terms have valid topologyKey",
		},
		{
			pod: createPodWithAffinityTerms(defaultNamespace, "", nil, nil,
				[]v1.PodAffinityTerm{
					{
						LabelSelector: &metav1.LabelSelector{
							MatchExpressions: []metav1.LabelSelectorRequirement{
								{
									Key:      "foo",
									Operator: metav1.LabelSelectorOpExists,
								},
							},
						},
						TopologyKey: "region",
					},
					{
						LabelSelector: &metav1.LabelSelector{
							MatchExpressions: []metav1.LabelSelectorRequirement{
								{
									Key:      "bar",
									Operator: metav1.LabelSelectorOpExists,
								},
							},
						},
						TopologyKey: "zone",
					},
				}),
			pods: []*v1.Pod{
				{
					ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{"foo": "", "bar": ""}},
					Spec: v1.PodSpec{
						NodeName: "nodeA",
					},
				},
			},
			nodes: []*v1.Node{
				{ObjectMeta: metav1.ObjectMeta{Name: "nodeA", Labels: map[string]string{"region": "r1", "zone": "z1", "hostname": "nodeA"}}},
				{ObjectMeta: metav1.ObjectMeta{Name: "nodeB", Labels: map[string]string{"region": "r1", "zone": "z2", "hostname": "nodeB"}}},
			},
			wantStatuses: []*framework.Status{
				framework.NewStatus(
					framework.Unschedulable,
					utils.ErrReasonAffinityNotMatch,
					utils.ErrReasonAntiAffinityRulesNotMatch,
				),
				framework.NewStatus(
					framework.Unschedulable,
					utils.ErrReasonAffinityNotMatch,
					utils.ErrReasonAntiAffinityRulesNotMatch,
				),
			},
			name: "Test incoming pod's anti-affinity: only when labelSelector and topologyKey both match, it's counted as a single term match - case when all terms have valid topologyKey",
		},
		{
			pod: &v1.Pod{
				ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{"foo": "", "bar": ""}},
			},
			pods: []*v1.Pod{
				createPodWithAffinityTerms(defaultNamespace, "nodeA", nil, nil,
					[]v1.PodAffinityTerm{
						{
							LabelSelector: &metav1.LabelSelector{
								MatchExpressions: []metav1.LabelSelectorRequirement{
									{
										Key:      "foo",
										Operator: metav1.LabelSelectorOpExists,
									},
								},
							},
							TopologyKey: "zone",
						},
						{
							LabelSelector: &metav1.LabelSelector{
								MatchExpressions: []metav1.LabelSelectorRequirement{
									{
										Key:      "labelA",
										Operator: metav1.LabelSelectorOpExists,
									},
								},
							},
							TopologyKey: "zone",
						},
					}),
				createPodWithAffinityTerms(defaultNamespace, "nodeB", nil, nil,
					[]v1.PodAffinityTerm{
						{
							LabelSelector: &metav1.LabelSelector{
								MatchExpressions: []metav1.LabelSelectorRequirement{
									{
										Key:      "bar",
										Operator: metav1.LabelSelectorOpExists,
									},
								},
							},
							TopologyKey: "zone",
						},
						{
							LabelSelector: &metav1.LabelSelector{
								MatchExpressions: []metav1.LabelSelectorRequirement{
									{
										Key:      "labelB",
										Operator: metav1.LabelSelectorOpExists,
									},
								},
							},
							TopologyKey: "zone",
						},
					}),
			},
			nodes: []*v1.Node{
				{ObjectMeta: metav1.ObjectMeta{Name: "nodeA", Labels: map[string]string{"region": "r1", "zone": "z1", "hostname": "nodeA"}}},
				{ObjectMeta: metav1.ObjectMeta{Name: "nodeB", Labels: map[string]string{"region": "r1", "zone": "z2", "hostname": "nodeB"}}},
				{ObjectMeta: metav1.ObjectMeta{Name: "nodeC", Labels: map[string]string{"region": "r1", "zone": "z3", "hostname": "nodeC"}}},
			},
			wantStatuses: []*framework.Status{
				framework.NewStatus(
					framework.Unschedulable,
					utils.ErrReasonAffinityNotMatch,
					utils.ErrReasonExistingAntiAffinityRulesNotMatch,
				),
				framework.NewStatus(
					framework.Unschedulable,
					utils.ErrReasonAffinityNotMatch,
					utils.ErrReasonExistingAntiAffinityRulesNotMatch,
				),
				nil,
			},
			name: "Test existing pod's anti-affinity: existingPod on nodeA and nodeB has at least one anti-affinity term matches incoming pod, so incoming pod can only be scheduled to nodeC",
		},
		{
			pod: createPodWithAffinityTerms(defaultNamespace, "", nil,
				[]v1.PodAffinityTerm{
					{
						LabelSelector: &metav1.LabelSelector{
							MatchExpressions: []metav1.LabelSelectorRequirement{
								{
									Key:      "foo",
									Operator: metav1.LabelSelectorOpExists,
								},
							},
						},
						TopologyKey: "region",
					},
					{
						LabelSelector: &metav1.LabelSelector{
							MatchExpressions: []metav1.LabelSelectorRequirement{
								{
									Key:      "bar",
									Operator: metav1.LabelSelectorOpExists,
								},
							},
						},
						TopologyKey: "zone",
					},
				}, nil),
			pods: []*v1.Pod{
				{
					ObjectMeta: metav1.ObjectMeta{Name: "pod1", Labels: map[string]string{"foo": "", "bar": ""}},
					Spec: v1.PodSpec{
						NodeName: "nodeA",
					},
				},
			},
			nodes: []*v1.Node{
				{ObjectMeta: metav1.ObjectMeta{Name: "nodeA", Labels: map[string]string{"region": "r1", "zone": "z1", "hostname": "nodeA"}}},
				{ObjectMeta: metav1.ObjectMeta{Name: "nodeB", Labels: map[string]string{"region": "r1", "zone": "z1", "hostname": "nodeB"}}},
			},
			wantStatuses: []*framework.Status{nil, nil},
			name:         "Test incoming pod's affinity: firstly check if all affinityTerms match, and then check if all topologyKeys match",
		},
		{
			pod: createPodWithAffinityTerms(defaultNamespace, "", nil,
				[]v1.PodAffinityTerm{
					{
						LabelSelector: &metav1.LabelSelector{
							MatchExpressions: []metav1.LabelSelectorRequirement{
								{
									Key:      "foo",
									Operator: metav1.LabelSelectorOpExists,
								},
							},
						},
						TopologyKey: "region",
					},
					{
						LabelSelector: &metav1.LabelSelector{
							MatchExpressions: []metav1.LabelSelectorRequirement{
								{
									Key:      "bar",
									Operator: metav1.LabelSelectorOpExists,
								},
							},
						},
						TopologyKey: "zone",
					},
				}, nil),
			pods: []*v1.Pod{
				{
					ObjectMeta: metav1.ObjectMeta{Name: "pod1", Labels: map[string]string{"foo": ""}},
					Spec: v1.PodSpec{
						NodeName: "nodeA",
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{Name: "pod2", Labels: map[string]string{"bar": ""}},
					Spec: v1.PodSpec{
						NodeName: "nodeB",
					},
				},
			},
			nodes: []*v1.Node{
				{ObjectMeta: metav1.ObjectMeta{Name: "nodeA", Labels: map[string]string{"region": "r1", "zone": "z1", "hostname": "nodeA"}}},
				{ObjectMeta: metav1.ObjectMeta{Name: "nodeB", Labels: map[string]string{"region": "r1", "zone": "z2", "hostname": "nodeB"}}},
			},
			wantStatuses: []*framework.Status{
				framework.NewStatus(
					framework.UnschedulableAndUnresolvable,
					utils.ErrReasonAffinityNotMatch,
					utils.ErrReasonAffinityRulesNotMatch,
				),
				framework.NewStatus(
					framework.UnschedulableAndUnresolvable,
					utils.ErrReasonAffinityNotMatch,
					utils.ErrReasonAffinityRulesNotMatch,
				),
			},
			name: "Test incoming pod's affinity: firstly check if all affinityTerms match, and then check if all topologyKeys match, and the match logic should be satisfied on the same pod",
		},
	}

	for indexTest, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			snapshot := framework_helper.MakeSnapShot(test.pods, test.nodes, nil)

			for indexNode, node := range test.nodes {
				p := &InterPodAffinity{
					sharedLister: snapshot,
				}
				state := framework.NewCycleState()
				preFilterStatus := p.PreFilter(context.Background(), state, test.pod)
				if !preFilterStatus.IsSuccess() {
					t.Errorf("prefilter failed with status: %v", preFilterStatus)
				}
				nodeInfo := mustGetNodeInfo(t, snapshot, node.Name)
				gotStatus := p.Filter(context.Background(), state, test.pod, nodeInfo)
				if !reflect.DeepEqual(gotStatus, test.wantStatuses[indexNode]) {
					t.Errorf("index: %d status does not match: %v, want: %v", indexTest, gotStatus, test.wantStatuses[indexNode])
				}
			}
		})
	}
}

func TestNMNodesFilter(t *testing.T) {
	podLabelA := map[string]string{
		"foo": "bar",
	}
	labelRgChina := map[string]string{
		"region": "China",
	}
	labelRgChinaAzAz1 := map[string]string{
		"region": "China",
		"az":     "az1",
	}
	labelRgIndia := map[string]string{
		"region": "India",
	}
	tests := []struct {
		pod          *v1.Pod
		pods         []*v1.Pod
		nodes        []*v1.Node
		nmNodes      []*nodev1alpha1.NMNode
		wantStatuses []*framework.Status
		name         string
	}{
		{
			pod: createPodWithAffinityTerms(defaultNamespace, "", nil,
				[]v1.PodAffinityTerm{
					{
						LabelSelector: &metav1.LabelSelector{
							MatchExpressions: []metav1.LabelSelectorRequirement{
								{
									Key:      "foo",
									Operator: metav1.LabelSelectorOpIn,
									Values:   []string{"bar"},
								},
							},
						},
						TopologyKey: "region",
					},
				}, nil),
			pods: []*v1.Pod{
				{Spec: v1.PodSpec{NodeName: "machine1"}, ObjectMeta: metav1.ObjectMeta{Name: "p1", Labels: podLabelA, Annotations: map[string]string{podutil.PodLauncherAnnotationKey: string(podutil.NodeManager)}}},
			},
			nmNodes: []*nodev1alpha1.NMNode{
				{ObjectMeta: metav1.ObjectMeta{Name: "machine1", Labels: labelRgChina}},
				{ObjectMeta: metav1.ObjectMeta{Name: "machine2", Labels: labelRgChinaAzAz1}},
				{ObjectMeta: metav1.ObjectMeta{Name: "machine3", Labels: labelRgIndia}},
			},
			wantStatuses: []*framework.Status{
				nil,
				nil,
				framework.NewStatus(
					framework.UnschedulableAndUnresolvable,
					utils.ErrReasonAffinityNotMatch,
					utils.ErrReasonAffinityRulesNotMatch,
				),
			},
			name: "All nodes are of NMNode type, that is, they are managed by the node manager. A pod can be scheduled onto all the nodes that have the same topology key & label value with one of them has an existing pod that matches the affinity rules",
		},
		{
			pod: createPodWithAffinityTerms(defaultNamespace, "", nil,
				[]v1.PodAffinityTerm{
					{
						LabelSelector: &metav1.LabelSelector{
							MatchExpressions: []metav1.LabelSelectorRequirement{
								{
									Key:      "foo",
									Operator: metav1.LabelSelectorOpIn,
									Values:   []string{"bar"},
								},
							},
						},
						TopologyKey: "region",
					},
				}, nil),
			pods: []*v1.Pod{
				{Spec: v1.PodSpec{NodeName: "machine0"}, ObjectMeta: metav1.ObjectMeta{Name: "p0", Labels: podLabelA}},
			},
			nodes: []*v1.Node{
				{ObjectMeta: metav1.ObjectMeta{Name: "machine0", Labels: labelRgIndia}},
				{ObjectMeta: metav1.ObjectMeta{Name: "machine1", Labels: labelRgChina}},
			},
			nmNodes: []*nodev1alpha1.NMNode{
				{ObjectMeta: metav1.ObjectMeta{Name: "machine2", Labels: labelRgChinaAzAz1}},
				{ObjectMeta: metav1.ObjectMeta{Name: "machine3", Labels: labelRgIndia}},
			},
			wantStatuses: []*framework.Status{
				framework.NewStatus(
					framework.UnschedulableAndUnresolvable,
					fmt.Sprintf(podlauncher.ErrReasonTemplate, podutil.NodeManager),
				),
				framework.NewStatus(
					framework.UnschedulableAndUnresolvable,
					fmt.Sprintf(podlauncher.ErrReasonTemplate, podutil.NodeManager),
				),
				framework.NewStatus(
					framework.UnschedulableAndUnresolvable,
					utils.ErrReasonAffinityNotMatch,
					utils.ErrReasonAffinityRulesNotMatch,
				),
				nil,
			},
			name: "Since the pod required by affinity is on v1.node of machine0, all nodes corresponding to NMNode in the India region can be scheduled. However, since both machine0 and machine1 only have v1.Node, they cannot be scheduled.",
		},
		{
			pod: createPodWithAffinityTerms(defaultNamespace, "", nil,
				[]v1.PodAffinityTerm{
					{
						LabelSelector: &metav1.LabelSelector{
							MatchExpressions: []metav1.LabelSelectorRequirement{
								{
									Key:      "foo",
									Operator: metav1.LabelSelectorOpIn,
									Values:   []string{"bar"},
								},
							},
						},
						TopologyKey: "region",
					},
				}, nil),
			pods: []*v1.Pod{
				{Spec: v1.PodSpec{NodeName: "machine1"}, ObjectMeta: metav1.ObjectMeta{Name: "p1", Labels: podLabelA}},
			},
			nodes: []*v1.Node{
				{ObjectMeta: metav1.ObjectMeta{Name: "machine1", Labels: labelRgChina}},
			},
			nmNodes: []*nodev1alpha1.NMNode{
				{ObjectMeta: metav1.ObjectMeta{Name: "machine1", Labels: labelRgChina}},
				{ObjectMeta: metav1.ObjectMeta{Name: "machine2", Labels: labelRgChinaAzAz1}},
				{ObjectMeta: metav1.ObjectMeta{Name: "machine3", Labels: labelRgIndia}},
			},
			wantStatuses: []*framework.Status{
				nil,
				nil,
				framework.NewStatus(
					framework.UnschedulableAndUnresolvable,
					utils.ErrReasonAffinityNotMatch,
					utils.ErrReasonAffinityRulesNotMatch,
				),
			},
			name: "Machine 1 has v1.Node and NMNode, the others are of NMNode type. Since the pod required by affinity is on v1.node of machine1, all nodes corresponding to NMNode in the China region can be scheduled.",
		},
	}

	for indexTest, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			test.pod.Annotations = map[string]string{podutil.PodLauncherAnnotationKey: string(podutil.NodeManager)}
			snapshot := framework_helper.MakeSnapShot(test.pods, test.nodes, test.nmNodes)

			nodeNames := getNodeNames(test.nodes, test.nmNodes)
			for indexNode := 0; indexNode < len(nodeNames); indexNode++ {
				p := &InterPodAffinity{
					sharedLister: snapshot,
				}
				var nodeInfo framework.NodeInfo
				nodeInfo = mustGetNodeInfo(t, snapshot, nodeNames[indexNode])

				state := framework.NewCycleState()
				preFilterStatus := p.PreFilter(context.Background(), state, test.pod)
				if !preFilterStatus.IsSuccess() {
					t.Errorf("prefilter failed with status: %v", preFilterStatus)
				}
				gotStatus := p.Filter(context.Background(), state, test.pod, nodeInfo)
				if !reflect.DeepEqual(gotStatus, test.wantStatuses[indexNode]) {
					t.Errorf("index: %d status does not match: %v, want: %v", indexTest, gotStatus, test.wantStatuses[indexNode])
				}
			}
		})
	}
}

func getNodeNames(nodes []*v1.Node, nmNodes []*nodev1alpha1.NMNode) []string {
	nameSet := sets.NewString()
	for _, node := range nodes {
		nameSet.Insert(node.Name)
	}
	for _, nmNode := range nmNodes {
		nameSet.Insert(nmNode.Name)
	}
	return nameSet.List()
}

func TestPreFilterDisabled(t *testing.T) {
	pod := &v1.Pod{}
	nodeInfo := framework.NewNodeInfo()
	node := v1.Node{}
	nodeInfo.SetNode(&node)
	p := &InterPodAffinity{}
	cycleState := framework.NewCycleState()
	gotStatus := p.Filter(context.Background(), cycleState, pod, nodeInfo)
	wantStatus := framework.NewStatus(framework.Error, `error reading "PreFilterInterPodAffinity" from cycleState: not found`)
	if !reflect.DeepEqual(gotStatus, wantStatus) {
		t.Errorf("status does not match: %v, want: %v", gotStatus, wantStatus)
	}
}

func TestPreFilterStateAddRemovePod(t *testing.T) {
	var label1 = map[string]string{
		"region": "r1",
		"zone":   "z11",
	}
	var label2 = map[string]string{
		"region": "r1",
		"zone":   "z12",
	}
	var label3 = map[string]string{
		"region": "r2",
		"zone":   "z21",
	}
	selector1 := map[string]string{"foo": "bar"}
	antiAffinityFooBar := &v1.PodAntiAffinity{
		RequiredDuringSchedulingIgnoredDuringExecution: []v1.PodAffinityTerm{
			{
				LabelSelector: &metav1.LabelSelector{
					MatchExpressions: []metav1.LabelSelectorRequirement{
						{
							Key:      "foo",
							Operator: metav1.LabelSelectorOpIn,
							Values:   []string{"bar"},
						},
					},
				},
				TopologyKey: "region",
			},
		},
	}
	antiAffinityComplex := &v1.PodAntiAffinity{
		RequiredDuringSchedulingIgnoredDuringExecution: []v1.PodAffinityTerm{
			{
				LabelSelector: &metav1.LabelSelector{
					MatchExpressions: []metav1.LabelSelectorRequirement{
						{
							Key:      "foo",
							Operator: metav1.LabelSelectorOpIn,
							Values:   []string{"bar", "buzz"},
						},
					},
				},
				TopologyKey: "region",
			},
			{
				LabelSelector: &metav1.LabelSelector{
					MatchExpressions: []metav1.LabelSelectorRequirement{
						{
							Key:      "service",
							Operator: metav1.LabelSelectorOpNotIn,
							Values:   []string{"bar", "security", "test"},
						},
					},
				},
				TopologyKey: "zone",
			},
		},
	}
	affinityComplex := &v1.PodAffinity{
		RequiredDuringSchedulingIgnoredDuringExecution: []v1.PodAffinityTerm{
			{
				LabelSelector: &metav1.LabelSelector{
					MatchExpressions: []metav1.LabelSelectorRequirement{
						{
							Key:      "foo",
							Operator: metav1.LabelSelectorOpIn,
							Values:   []string{"bar", "buzz"},
						},
					},
				},
				TopologyKey: "region",
			},
			{
				LabelSelector: &metav1.LabelSelector{
					MatchExpressions: []metav1.LabelSelectorRequirement{
						{
							Key:      "service",
							Operator: metav1.LabelSelectorOpNotIn,
							Values:   []string{"bar", "security", "test"},
						},
					},
				},
				TopologyKey: "zone",
			},
		},
	}

	tests := []struct {
		name                 string
		pendingPod           *v1.Pod
		addedPod             *v1.Pod
		existingPods         []*v1.Pod
		nodes                []*v1.Node
		expectedAntiAffinity utils.TopologyToMatchedTermCount
		expectedAffinity     utils.TopologyToMatchedTermCount
	}{
		{
			name: "no affinity exist",
			pendingPod: &v1.Pod{
				ObjectMeta: metav1.ObjectMeta{Name: "pending", Labels: selector1},
			},
			existingPods: []*v1.Pod{
				{ObjectMeta: metav1.ObjectMeta{Name: "p1", Labels: selector1},
					Spec: v1.PodSpec{NodeName: "nodeA"},
				},
				{ObjectMeta: metav1.ObjectMeta{Name: "p2"},
					Spec: v1.PodSpec{NodeName: "nodeC"},
				},
			},
			addedPod: &v1.Pod{
				ObjectMeta: metav1.ObjectMeta{Name: "addedPod", Labels: selector1},
				Spec:       v1.PodSpec{NodeName: "nodeB"},
			},
			nodes: []*v1.Node{
				{ObjectMeta: metav1.ObjectMeta{Name: "nodeA", Labels: label1}},
				{ObjectMeta: metav1.ObjectMeta{Name: "nodeB", Labels: label2}},
				{ObjectMeta: metav1.ObjectMeta{Name: "nodeC", Labels: label3}},
			},
			expectedAntiAffinity: utils.TopologyToMatchedTermCount{},
			expectedAffinity:     utils.TopologyToMatchedTermCount{},
		},
		{
			name: "preFilterState anti-affinity terms are updated correctly after adding and removing a pod",
			pendingPod: &v1.Pod{
				ObjectMeta: metav1.ObjectMeta{Name: "pending", Labels: selector1},
				Spec: v1.PodSpec{
					Affinity: &v1.Affinity{
						PodAntiAffinity: antiAffinityFooBar,
					},
				},
			},
			existingPods: []*v1.Pod{
				{ObjectMeta: metav1.ObjectMeta{Name: "p1", Labels: selector1},
					Spec: v1.PodSpec{NodeName: "nodeA"},
				},
				{ObjectMeta: metav1.ObjectMeta{Name: "p2"},
					Spec: v1.PodSpec{
						NodeName: "nodeC",
						Affinity: &v1.Affinity{
							PodAntiAffinity: antiAffinityFooBar,
						},
					},
				},
			},
			addedPod: &v1.Pod{
				ObjectMeta: metav1.ObjectMeta{Name: "addedPod", Labels: selector1},
				Spec: v1.PodSpec{
					NodeName: "nodeB",
					Affinity: &v1.Affinity{
						PodAntiAffinity: antiAffinityFooBar,
					},
				},
			},
			nodes: []*v1.Node{
				{ObjectMeta: metav1.ObjectMeta{Name: "nodeA", Labels: label1}},
				{ObjectMeta: metav1.ObjectMeta{Name: "nodeB", Labels: label2}},
				{ObjectMeta: metav1.ObjectMeta{Name: "nodeC", Labels: label3}},
			},
			expectedAntiAffinity: utils.TopologyToMatchedTermCount{
				{Key: "region", Value: "r1"}: 2,
			},
			expectedAffinity: utils.TopologyToMatchedTermCount{},
		},
		{
			name: "preFilterState anti-affinity terms are updated correctly after adding and removing a pod",
			pendingPod: &v1.Pod{
				ObjectMeta: metav1.ObjectMeta{Name: "pending", Labels: selector1},
				Spec: v1.PodSpec{
					Affinity: &v1.Affinity{
						PodAntiAffinity: antiAffinityComplex,
					},
				},
			},
			existingPods: []*v1.Pod{
				{ObjectMeta: metav1.ObjectMeta{Name: "p1", Labels: selector1},
					Spec: v1.PodSpec{NodeName: "nodeA"},
				},
				{ObjectMeta: metav1.ObjectMeta{Name: "p2"},
					Spec: v1.PodSpec{
						NodeName: "nodeC",
						Affinity: &v1.Affinity{
							PodAntiAffinity: antiAffinityFooBar,
						},
					},
				},
			},
			addedPod: &v1.Pod{
				ObjectMeta: metav1.ObjectMeta{Name: "addedPod", Labels: selector1},
				Spec: v1.PodSpec{
					NodeName: "nodeA",
					Affinity: &v1.Affinity{
						PodAntiAffinity: antiAffinityComplex,
					},
				},
			},
			nodes: []*v1.Node{
				{ObjectMeta: metav1.ObjectMeta{Name: "nodeA", Labels: label1}},
				{ObjectMeta: metav1.ObjectMeta{Name: "nodeB", Labels: label2}},
				{ObjectMeta: metav1.ObjectMeta{Name: "nodeC", Labels: label3}},
			},
			expectedAntiAffinity: utils.TopologyToMatchedTermCount{
				{Key: "region", Value: "r1"}: 2,
				{Key: "zone", Value: "z11"}:  2,
				{Key: "zone", Value: "z21"}:  1,
			},
			expectedAffinity: utils.TopologyToMatchedTermCount{},
		},
		{
			name: "preFilterState matching pod affinity and anti-affinity are updated correctly after adding and removing a pod",
			pendingPod: &v1.Pod{
				ObjectMeta: metav1.ObjectMeta{Name: "pending", Labels: selector1},
				Spec: v1.PodSpec{
					Affinity: &v1.Affinity{
						PodAffinity: affinityComplex,
					},
				},
			},
			existingPods: []*v1.Pod{
				{ObjectMeta: metav1.ObjectMeta{Name: "p1", Labels: selector1},
					Spec: v1.PodSpec{NodeName: "nodeA"},
				},
				{ObjectMeta: metav1.ObjectMeta{Name: "p2"},
					Spec: v1.PodSpec{
						NodeName: "nodeC",
						Affinity: &v1.Affinity{
							PodAntiAffinity: antiAffinityFooBar,
							PodAffinity:     affinityComplex,
						},
					},
				},
			},
			addedPod: &v1.Pod{
				ObjectMeta: metav1.ObjectMeta{Name: "addedPod", Labels: selector1},
				Spec: v1.PodSpec{
					NodeName: "nodeA",
					Affinity: &v1.Affinity{
						PodAntiAffinity: antiAffinityComplex,
					},
				},
			},
			nodes: []*v1.Node{
				{ObjectMeta: metav1.ObjectMeta{Name: "nodeA", Labels: label1}},
				{ObjectMeta: metav1.ObjectMeta{Name: "nodeB", Labels: label2}},
				{ObjectMeta: metav1.ObjectMeta{Name: "nodeC", Labels: label3}},
			},
			expectedAntiAffinity: utils.TopologyToMatchedTermCount{},
			expectedAffinity: utils.TopologyToMatchedTermCount{
				{Key: "region", Value: "r1"}: 2,
				{Key: "zone", Value: "z11"}:  2,
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			// getMeta creates predicate meta data given the list of pods.
			getState := func(pods []*v1.Pod) (*InterPodAffinity, *framework.CycleState, *utils.PreFilterState, *godelcache.Snapshot) {
				snapshot := framework_helper.MakeSnapShot(pods, test.nodes, nil)

				p := &InterPodAffinity{
					sharedLister: snapshot,
				}
				cycleState := framework.NewCycleState()
				preFilterStatus := p.PreFilter(context.Background(), cycleState, test.pendingPod)
				if !preFilterStatus.IsSuccess() {
					t.Errorf("prefilter failed with status: %v", preFilterStatus)
				}

				state, err := getPreFilterState(cycleState)
				if err != nil {
					t.Errorf("failed to get preFilterState from cycleState: %v", err)
				}

				return p, cycleState, state, snapshot
			}

			// allPodsState is the state produced when all pods, including test.addedPod are given to prefilter.
			_, _, allPodsState, _ := getState(append(test.existingPods, test.addedPod))

			// state is produced for test.existingPods (without test.addedPod).
			ipa, cycleState, state, snapshot := getState(test.existingPods)
			// clone the state so that we can compare it later when performing Remove.
			originalState := state.Clone()

			// Add test.addedPod to state1 and verify it is equal to allPodsState.
			nodeInfo := mustGetNodeInfo(t, snapshot, test.addedPod.Spec.NodeName)
			if err := ipa.AddPod(context.Background(), cycleState, test.pendingPod, test.addedPod, nodeInfo); err != nil {
				t.Errorf("error adding pod to meta: %v", err)
			}

			newState, err := getPreFilterState(cycleState)
			if err != nil {
				t.Errorf("failed to get preFilterState from cycleState: %v", err)
			}

			if !reflect.DeepEqual(newState.TopologyToMatchedAntiAffinityTerms, test.expectedAntiAffinity) {
				t.Errorf("State is not equal, got: %v, want: %v", newState.TopologyToMatchedAntiAffinityTerms, test.expectedAntiAffinity)
			}

			if !reflect.DeepEqual(newState.TopologyToMatchedAffinityTerms, test.expectedAffinity) {
				t.Errorf("State is not equal, got: %v, want: %v", newState.TopologyToMatchedAffinityTerms, test.expectedAffinity)
			}

			fmt.Printf("name: %v,allPodsState: %v, state: %v ", test.name, allPodsState, state)
			if !reflect.DeepEqual(allPodsState, state) {
				t.Errorf("State is not equal, got: %v, want: %v", state, allPodsState)
			}

			// Remove the added pod pod and make sure it is equal to the original state.
			if err := ipa.RemovePod(context.Background(), cycleState, test.pendingPod, test.addedPod, nodeInfo); err != nil {
				t.Errorf("error removing pod from meta: %v", err)
			}
			if !reflect.DeepEqual(originalState, state) {
				t.Errorf("State is not equal, got: %v, want: %v", state, originalState)
			}
		})
	}
}

func TestPreFilterStateClone(t *testing.T) {
	source := &utils.PreFilterState{
		TopologyToMatchedExistingAntiAffinityTerms: utils.TopologyToMatchedTermCount{
			{Key: "name", Value: "machine1"}: 1,
			{Key: "name", Value: "machine2"}: 1,
		},
		TopologyToMatchedAffinityTerms: utils.TopologyToMatchedTermCount{
			{Key: "name", Value: "nodeA"}: 1,
			{Key: "name", Value: "nodeC"}: 2,
		},
		TopologyToMatchedAntiAffinityTerms: utils.TopologyToMatchedTermCount{
			{Key: "name", Value: "nodeN"}: 3,
			{Key: "name", Value: "nodeM"}: 1,
		},
	}

	clone := source.Clone()
	if clone == source {
		t.Errorf("Clone returned the exact same object!")
	}
	if !reflect.DeepEqual(clone, source) {
		t.Errorf("Copy is not equal to source!")
	}
}

func mustGetNodeInfo(t *testing.T, snapshot *godelcache.Snapshot, name string) framework.NodeInfo {
	t.Helper()
	nodeInfo, err := snapshot.NodeInfos().Get(name)
	if err != nil {
		t.Fatal(err)
	}
	return nodeInfo
}
