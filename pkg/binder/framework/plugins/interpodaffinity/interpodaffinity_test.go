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
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/kubewharf/godel-scheduler/pkg/binder/cache"
	"github.com/kubewharf/godel-scheduler/pkg/binder/framework/handle"
	pt "github.com/kubewharf/godel-scheduler/pkg/binder/testing"
	commoncache "github.com/kubewharf/godel-scheduler/pkg/common/cache"
	framework "github.com/kubewharf/godel-scheduler/pkg/framework/api"
	utils "github.com/kubewharf/godel-scheduler/pkg/plugins/interpodaffinity"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/uuid"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes/fake"
)

var (
	defaultNamespace = ""
)

func initFrameworkHandle(client *fake.Clientset, nodes []*v1.Node, existingPods []*v1.Pod) (handle.BinderFrameworkHandle, error) {
	ctx := context.Background()
	informerFactory := informers.NewSharedInformerFactory(client, 0)
	informerFactory.Core().V1().Nodes().Informer()
	cacheHandler := commoncache.MakeCacheHandlerWrapper().
		Period(10 * time.Second).PodAssumedTTL(30 * time.Second).StopCh(make(chan struct{})).
		ComponentName("godel-binder").Obj()
	cache := cache.New(cacheHandler)

	for _, node := range nodes {
		_, err := client.CoreV1().Nodes().Create(context.TODO(), node, metav1.CreateOptions{})
		if err != nil {
			return nil, err
		}
		cache.AddNode(node)
	}

	for _, pod := range existingPods {
		pod.UID = types.UID(pod.Name)
		cache.AddPod(pod)
	}

	informerFactory.Start(ctx.Done())
	informerFactory.WaitForCacheSync(ctx.Done())
	return pt.NewBinderFrameworkHandle(client, nil, informerFactory, nil, cache)
}

func initFrameworkHandleWithSingleNode(client *fake.Clientset, nodeInfo *framework.NodeInfo) (handle.BinderFrameworkHandle, error) {
	ctx := context.Background()
	informerFactory := informers.NewSharedInformerFactory(client, 0)
	informerFactory.Core().V1().Nodes().Informer()
	cacheHandler := commoncache.MakeCacheHandlerWrapper().
		Period(10 * time.Second).PodAssumedTTL(30 * time.Second).StopCh(make(chan struct{})).
		ComponentName("godel-binder").Obj()
	cache := cache.New(cacheHandler)

	node := (*nodeInfo).GetNode()
	_, err := client.CoreV1().Nodes().Create(context.TODO(), node, metav1.CreateOptions{})
	if err != nil {
		return nil, err
	}
	cache.AddNode(node)

	for _, podInfo := range (*nodeInfo).GetPods() {
		pod := podInfo.Pod
		pod.Spec.NodeName = node.Name
		pod.UID = types.UID(pod.Name)
		if err := cache.AddPod(pod); err != nil {
			return nil, err
		}
	}

	informerFactory.Start(ctx.Done())
	informerFactory.WaitForCacheSync(ctx.Done())
	return pt.NewBinderFrameworkHandle(client, nil, informerFactory, nil, cache)
}

func createPodWithAffinityTerms(namespace, nodeName string, labels map[string]string, affinity, antiAffinity []v1.PodAffinityTerm) *v1.Pod {
	return &v1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "pod-" + string(uuid.NewUUID()),
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
	return newPodWithLabelsAndNamespace(labels, defaultNamespace)
}

func newPodWithLabelsAndNamespace(labels map[string]string, namespace string) *v1.Pod {
	return &v1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "pod-" + string(uuid.NewUUID()),
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
			nodeInfo: framework.NewNodeInfo(
				newPodWithLabels(podLabel)),
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
			nodeInfo: framework.NewNodeInfo(
				newPodWithLabels(podLabel)),
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
				utils.ErrReasonAffinityNotMatch,
				utils.ErrReasonAntiAffinityRulesNotMatch,
			),
			name: "PodAntiAffinity symmetry check b2: incoming pod and existing pod partially match each other on AffinityTerms",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.nodeInfo.SetNode(&node1)

			client := fake.NewSimpleClientset()
			frameworkHandle, err := initFrameworkHandleWithSingleNode(client, &tt.nodeInfo)
			if err != nil {
				t.Fatal(err)
			}

			p, err := New(nil, frameworkHandle)
			if err != nil {
				t.Fatal(err)
			}

			gotStatus := p.(framework.CheckTopologyPlugin).CheckTopology(context.Background(), nil, tt.pod, tt.nodeInfo)
			if !reflect.DeepEqual(gotStatus, tt.wantStatus) {
				t.Errorf("status does not match: %v, want: %v", gotStatus, tt.wantStatus)
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

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := fake.NewSimpleClientset()
			frameworkHandle, err := initFrameworkHandle(client, tt.nodes, tt.pods)
			if err != nil {
				t.Fatal(err)
			}

			p, err := New(nil, frameworkHandle)
			if err != nil {
				t.Fatal(err)
			}

			for i, node := range tt.nodes {
				nodeInfo := frameworkHandle.GetNodeInfo(node.Name)
				gotStatus := p.(framework.CheckTopologyPlugin).CheckTopology(context.Background(), nil, tt.pod, nodeInfo)
				if !reflect.DeepEqual(gotStatus, tt.wantStatuses[i]) {
					t.Errorf("status does not match: %v, want: %v", gotStatus, tt.wantStatuses[i])
				}
			}
		})
	}
}
