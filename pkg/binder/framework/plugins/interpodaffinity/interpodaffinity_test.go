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
	"reflect"
	"testing"

	framework "github.com/kubewharf/godel-scheduler/pkg/framework/api"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
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

func newPodWithLabels(labels map[string]string) *v1.Pod {
	return &v1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Labels: labels,
		},
	}
}

func newPodWithLabelsAndNamespace(labels map[string]string, namespace string) *v1.Pod {
	return &v1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: namespace,
			Labels:    labels,
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
		nodeInfo   framework.NodeInfo
		name       string
		wantStatus *framework.Status
	}{
		{
			pod:      new(v1.Pod),
			nodeInfo: framework.NewNodeInfo(),
			name:     "A pod that has no required pod affinity scheduling rules can schedule onto a node with no existing pods",
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
			nodeInfo: framework.NewNodeInfo(
				newPodWithLabels(podLabel)),
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
			nodeInfo: framework.NewNodeInfo(
				newPodWithLabels(podLabel)),
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
			nodeInfo: framework.NewNodeInfo(
				newPodWithLabelsAndNamespace(podLabel, "ns")),
			name: "Does not satisfy the PodAffinity with labelSelector because of diff Namespace",
			wantStatus: framework.NewStatus(
				framework.Unschedulable,
				ErrReasonAffinityRulesNotMatch,
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
			nodeInfo: framework.NewNodeInfo(
				newPodWithLabels(podLabel)),
			name: "Doesn't satisfy the PodAffinity because of unmatching labelSelector with the existing pod",
			wantStatus: framework.NewStatus(
				framework.Unschedulable,
				ErrReasonAffinityRulesNotMatch,
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
			nodeInfo: framework.NewNodeInfo(
				newPodWithLabels(podLabel)),
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
			nodeInfo: framework.NewNodeInfo(
				newPodWithLabels(podLabel)),
			name: "The labelSelector requirements(items of matchExpressions) are ANDed, the pod cannot schedule onto the node because one of the matchExpression item don't match.",
			wantStatus: framework.NewStatus(
				framework.Unschedulable,
				ErrReasonAffinityRulesNotMatch,
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
			nodeInfo: framework.NewNodeInfo(
				newPodWithLabels(podLabel)),
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
			nodeInfo: framework.NewNodeInfo(
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
					})),
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
			nodeInfo: framework.NewNodeInfo(
				newPodWithLabels(podLabel)),
			name: "satisfies the PodAffinity but doesn't satisfy the PodAntiAffinity with the existing pod",
			wantStatus: framework.NewStatus(
				framework.Unschedulable,
				ErrReasonAntiAffinityRulesNotMatch,
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
			nodeInfo: framework.NewNodeInfo(
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
					})),
			name: "satisfies the PodAffinity and PodAntiAffinity but doesn't satisfy PodAntiAffinity symmetry with the existing pod",
			wantStatus: framework.NewStatus(
				framework.Unschedulable,
				ErrReasonExistingAntiAffinityRulesNotMatch,
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
			nodeInfo: framework.NewNodeInfo(
				newPodWithLabels(podLabel)),
			name: "pod matches its own Label in PodAffinity and that matches the existing pod Labels",
			wantStatus: framework.NewStatus(
				framework.Unschedulable,
				ErrReasonAffinityRulesNotMatch,
			),
		},
		{
			pod: &v1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Labels: podLabel,
				},
			},
			nodeInfo: framework.NewNodeInfo(
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
					})),
			name: "verify that PodAntiAffinity from existing pod is respected when pod has no AntiAffinity constraints. doesn't satisfy PodAntiAffinity symmetry with the existing pod",
			wantStatus: framework.NewStatus(
				framework.Unschedulable,
				ErrReasonExistingAntiAffinityRulesNotMatch,
			),
		},
		{
			pod: &v1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Labels: podLabel,
				},
			},
			nodeInfo: framework.NewNodeInfo(
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
					})),
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
			nodeInfo: framework.NewNodeInfo(
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
					})),
			name: "satisfies the PodAntiAffinity with existing pod but doesn't satisfy PodAntiAffinity symmetry with incoming pod",
			wantStatus: framework.NewStatus(
				framework.Unschedulable,
				ErrReasonAntiAffinityRulesNotMatch,
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
			nodeInfo: framework.NewNodeInfo(
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
					})),
			wantStatus: framework.NewStatus(
				framework.Unschedulable,
				ErrReasonAntiAffinityRulesNotMatch,
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
			nodeInfo: framework.NewNodeInfo(
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
					})),
			wantStatus: framework.NewStatus(
				framework.Unschedulable,
				ErrReasonExistingAntiAffinityRulesNotMatch,
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
			nodeInfo: framework.NewNodeInfo(
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
					})),
			wantStatus: framework.NewStatus(
				framework.Unschedulable,
				ErrReasonExistingAntiAffinityRulesNotMatch,
				ErrReasonAntiAffinityRulesNotMatch,
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
			nodeInfo: framework.NewNodeInfo(
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
					})),
			wantStatus: framework.NewStatus(
				framework.Unschedulable,
				ErrReasonExistingAntiAffinityRulesNotMatch,
				ErrReasonAntiAffinityRulesNotMatch,
			),
			name: "PodAntiAffinity symmetry check b2: incoming pod and existing pod partially match each other on AffinityTerms",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.nodeInfo.SetNode(&node1)

			p, err := New(nil, nil)
			if err != nil {
				t.Fatal(err)
			}

			gotStatus := p.(framework.CheckConflictsPlugin).CheckConflicts(context.Background(), nil, tt.pod, tt.nodeInfo)
			if !reflect.DeepEqual(gotStatus, tt.wantStatus) {
				t.Errorf("status does not match: %v, want: %v", gotStatus, tt.wantStatus)
			}
		})
	}
}
