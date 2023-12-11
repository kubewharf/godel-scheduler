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

package nonnativeresource

import (
	"reflect"
	"testing"

	katalystv1alpha1 "github.com/kubewharf/katalyst-api/pkg/apis/node/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/sets"
	utilfeature "k8s.io/apiserver/pkg/util/feature"

	godelfeatures "github.com/kubewharf/godel-scheduler/pkg/features"
	framework "github.com/kubewharf/godel-scheduler/pkg/framework/api"
	testing_helper "github.com/kubewharf/godel-scheduler/pkg/testing-helper"
	"github.com/kubewharf/godel-scheduler/pkg/util"
)

func TestFitsNumaTopologyForNonExclusiveDedicatedQoS(t *testing.T) {
	utilfeature.DefaultMutableFeatureGate.SetFromMap(map[string]bool{string(godelfeatures.NonNativeResourceSchedulingSupport): true})

	node := &v1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name: "node",
		},
		Status: v1.NodeStatus{
			Allocatable: v1.ResourceList{
				"cpu":            resource.MustParse("94"),
				"memory":         resource.MustParse("252Gi"),
				"nvidia.com/gpu": resource.MustParse("8"),
			},
			Capacity: v1.ResourceList{
				"cpu":            resource.MustParse("96"),
				"memory":         resource.MustParse("256Gi"),
				"nvidia.com/gpu": resource.MustParse("8"),
			},
		},
	}
	node2 := &v1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name: "node",
		},
		Status: v1.NodeStatus{
			Allocatable: v1.ResourceList{
				"cpu":            resource.MustParse("94"),
				"memory":         resource.MustParse("252Gi"),
				"nvidia.com/gpu": resource.MustParse("8"),
			},
			Capacity: v1.ResourceList{
				"cpu":            resource.MustParse("96"),
				"memory":         resource.MustParse("256Gi"),
				"nvidia.com/gpu": resource.MustParse("8"),
			},
		},
	}

	cnr1 := &katalystv1alpha1.CustomNodeResource{
		ObjectMeta: metav1.ObjectMeta{
			Name: "node",
		},
		Spec: katalystv1alpha1.CustomNodeResourceSpec{
			NodeResourceProperties: []*katalystv1alpha1.Property{
				{
					PropertyName:     util.ResourceNuma.String(),
					PropertyQuantity: resource.NewQuantity(4, resource.DecimalSI),
				},
			},
		},
		Status: katalystv1alpha1.CustomNodeResourceStatus{
			TopologyZone: []*katalystv1alpha1.TopologyZone{
				{
					Type: katalystv1alpha1.TopologyTypeSocket,
					Name: "0",
					Children: []*katalystv1alpha1.TopologyZone{
						{
							Type: katalystv1alpha1.TopologyTypeNuma,
							Name: "0",
							Resources: katalystv1alpha1.Resources{
								Allocatable: &v1.ResourceList{
									"cpu":            resource.MustParse("24"),
									"memory":         resource.MustParse("96Gi"),
									"nvidia.com/gpu": resource.MustParse("2"),
								},
								Capacity: &v1.ResourceList{
									"cpu":            resource.MustParse("24"),
									"memory":         resource.MustParse("96Gi"),
									"nvidia.com/gpu": resource.MustParse("2"),
								},
							},
							Allocations: []*katalystv1alpha1.Allocation{
								{
									Consumer: "p1/p1/p1",
									Requests: &v1.ResourceList{
										"cpu":            resource.MustParse("24"),
										"memory":         resource.MustParse("96Gi"),
										"nvidia.com/gpu": resource.MustParse("1"),
									},
								},
							},
						},
						{
							Type: katalystv1alpha1.TopologyTypeNuma,
							Name: "1",
							Resources: katalystv1alpha1.Resources{
								Allocatable: &v1.ResourceList{
									"cpu":            resource.MustParse("24"),
									"memory":         resource.MustParse("96Gi"),
									"nvidia.com/gpu": resource.MustParse("2"),
								},
								Capacity: &v1.ResourceList{
									"cpu":            resource.MustParse("24"),
									"memory":         resource.MustParse("96Gi"),
									"nvidia.com/gpu": resource.MustParse("2"),
								},
							},
							Allocations: []*katalystv1alpha1.Allocation{
								{
									Consumer: "p2/p2/p2",
									Requests: &v1.ResourceList{
										"cpu":    resource.MustParse("10"),
										"memory": resource.MustParse("20Gi"),
									},
								},
							},
						},
					},
				},
				{
					Type: katalystv1alpha1.TopologyTypeSocket,
					Name: "1",
					Children: []*katalystv1alpha1.TopologyZone{
						{
							Type: katalystv1alpha1.TopologyTypeNuma,
							Name: "2",
							Resources: katalystv1alpha1.Resources{
								Allocatable: &v1.ResourceList{
									"cpu":            resource.MustParse("23"),
									"memory":         resource.MustParse("96Gi"),
									"nvidia.com/gpu": resource.MustParse("2"),
								},
								Capacity: &v1.ResourceList{
									"cpu":            resource.MustParse("24"),
									"memory":         resource.MustParse("96Gi"),
									"nvidia.com/gpu": resource.MustParse("2"),
								},
							},
							Allocations: []*katalystv1alpha1.Allocation{
								{
									Consumer: "p3/p3/p3",
									Requests: &v1.ResourceList{
										"cpu":            resource.MustParse("10"),
										"memory":         resource.MustParse("20Gi"),
										"nvidia.com/gpu": resource.MustParse("1"),
									},
								},
							},
						},
						{
							Type: katalystv1alpha1.TopologyTypeNuma,
							Name: "3",
							Resources: katalystv1alpha1.Resources{
								Allocatable: &v1.ResourceList{
									"cpu":            resource.MustParse("23"),
									"memory":         resource.MustParse("96Gi"),
									"nvidia.com/gpu": resource.MustParse("2"),
								},
								Capacity: &v1.ResourceList{
									"cpu":            resource.MustParse("24"),
									"memory":         resource.MustParse("96Gi"),
									"nvidia.com/gpu": resource.MustParse("2"),
								},
							},
							Allocations: []*katalystv1alpha1.Allocation{},
						},
					},
				},
			},
		},
	}

	cnr3 := &katalystv1alpha1.CustomNodeResource{
		ObjectMeta: metav1.ObjectMeta{
			Name: "node",
		},
		Spec: katalystv1alpha1.CustomNodeResourceSpec{
			NodeResourceProperties: []*katalystv1alpha1.Property{
				{
					PropertyName:     util.ResourceNuma.String(),
					PropertyQuantity: resource.NewQuantity(4, resource.DecimalSI),
				},
			},
		},
		Status: katalystv1alpha1.CustomNodeResourceStatus{
			TopologyZone: []*katalystv1alpha1.TopologyZone{
				{
					Type: katalystv1alpha1.TopologyTypeSocket,
					Name: "0",
					Children: []*katalystv1alpha1.TopologyZone{
						{
							Type: katalystv1alpha1.TopologyTypeNuma,
							Name: "0",
							Resources: katalystv1alpha1.Resources{
								Allocatable: &v1.ResourceList{
									"cpu":            resource.MustParse("24"),
									"memory":         resource.MustParse("96Gi"),
									"nvidia.com/gpu": resource.MustParse("2"),
								},
								Capacity: &v1.ResourceList{
									"cpu":            resource.MustParse("24"),
									"memory":         resource.MustParse("96Gi"),
									"nvidia.com/gpu": resource.MustParse("2"),
								},
							},
							Allocations: []*katalystv1alpha1.Allocation{
								{
									Consumer: "p9/p9/p9",
									Requests: &v1.ResourceList{
										"nvidia.com/gpu": resource.MustParse("1"),
									},
								},
							},
						},
						{
							Type: katalystv1alpha1.TopologyTypeNuma,
							Name: "1",
							Resources: katalystv1alpha1.Resources{
								Allocatable: &v1.ResourceList{
									"cpu":            resource.MustParse("24"),
									"memory":         resource.MustParse("96Gi"),
									"nvidia.com/gpu": resource.MustParse("2"),
								},
								Capacity: &v1.ResourceList{
									"cpu":            resource.MustParse("24"),
									"memory":         resource.MustParse("96Gi"),
									"nvidia.com/gpu": resource.MustParse("2"),
								},
							},
							Allocations: []*katalystv1alpha1.Allocation{
								{
									Consumer: "p10/p10/p10",
									Requests: &v1.ResourceList{
										"nvidia.com/gpu": resource.MustParse("1"),
									},
								},
							},
						},
					},
				},
				{
					Type: katalystv1alpha1.TopologyTypeSocket,
					Name: "1",
					Children: []*katalystv1alpha1.TopologyZone{
						{
							Type: katalystv1alpha1.TopologyTypeNuma,
							Name: "2",
							Resources: katalystv1alpha1.Resources{
								Allocatable: &v1.ResourceList{
									"cpu":            resource.MustParse("23"),
									"memory":         resource.MustParse("96Gi"),
									"nvidia.com/gpu": resource.MustParse("2"),
								},
								Capacity: &v1.ResourceList{
									"cpu":            resource.MustParse("24"),
									"memory":         resource.MustParse("96Gi"),
									"nvidia.com/gpu": resource.MustParse("2"),
								},
							},
							Allocations: []*katalystv1alpha1.Allocation{
								{
									Consumer: "p11/p11/p11",
									Requests: &v1.ResourceList{
										"nvidia.com/gpu": resource.MustParse("1"),
									},
								},
							},
						},
						{
							Type: katalystv1alpha1.TopologyTypeNuma,
							Name: "3",
							Resources: katalystv1alpha1.Resources{
								Allocatable: &v1.ResourceList{
									"cpu":            resource.MustParse("23"),
									"memory":         resource.MustParse("96Gi"),
									"nvidia.com/gpu": resource.MustParse("2"),
								},
								Capacity: &v1.ResourceList{
									"cpu":            resource.MustParse("24"),
									"memory":         resource.MustParse("96Gi"),
									"nvidia.com/gpu": resource.MustParse("2"),
								},
							},
							Allocations: []*katalystv1alpha1.Allocation{
								{
									Consumer: "p12/p12/p12",
									Requests: &v1.ResourceList{
										"nvidia.com/gpu": resource.MustParse("1"),
									},
								},
							},
						},
					},
				},
			},
		},
	}

	cnr4 := &katalystv1alpha1.CustomNodeResource{
		ObjectMeta: metav1.ObjectMeta{
			Name: "n",
		},
		Spec: katalystv1alpha1.CustomNodeResourceSpec{
			NodeResourceProperties: []*katalystv1alpha1.Property{
				{
					PropertyName:     util.ResourceNuma.String(),
					PropertyQuantity: resource.NewQuantity(4, resource.DecimalSI),
				},
			},
		},
		Status: katalystv1alpha1.CustomNodeResourceStatus{
			TopologyZone: []*katalystv1alpha1.TopologyZone{
				{
					Type: katalystv1alpha1.TopologyTypeSocket,
					Name: "0",
					Children: []*katalystv1alpha1.TopologyZone{
						{
							Type: katalystv1alpha1.TopologyTypeNuma,
							Name: "0",
							Resources: katalystv1alpha1.Resources{
								Allocatable: &corev1.ResourceList{
									"cpu":            resource.MustParse("23"),
									"memory":         resource.MustParse("255Gi"),
									"nvidia.com/gpu": resource.MustParse("2"),
								},
								Capacity: &corev1.ResourceList{
									"cpu":            resource.MustParse("24"),
									"memory":         resource.MustParse("256Gi"),
									"nvidia.com/gpu": resource.MustParse("2"),
								},
							},
							Allocations: []*katalystv1alpha1.Allocation{
								{
									Consumer: "p1/p1/p1",
									Requests: &corev1.ResourceList{
										"cpu":            resource.MustParse("10"),
										"memory":         resource.MustParse("20Gi"),
										"nvidia.com/gpu": resource.MustParse("1"),
									},
								},
								{
									Consumer: "p12/p12/p12",
									Requests: &corev1.ResourceList{
										"nvidia.com/gpu": resource.MustParse("1"),
									},
								},
							},
						},
						{
							Type: katalystv1alpha1.TopologyTypeNuma,
							Name: "1",
							Resources: katalystv1alpha1.Resources{
								Allocatable: &corev1.ResourceList{
									"cpu":            resource.MustParse("23"),
									"memory":         resource.MustParse("255Gi"),
									"nvidia.com/gpu": resource.MustParse("2"),
								},
								Capacity: &corev1.ResourceList{
									"cpu":            resource.MustParse("24"),
									"memory":         resource.MustParse("256Gi"),
									"nvidia.com/gpu": resource.MustParse("2"),
								},
							},
							Allocations: []*katalystv1alpha1.Allocation{
								{
									Consumer: "p2/p2/p2",
									Requests: &corev1.ResourceList{
										"cpu":    resource.MustParse("10"),
										"memory": resource.MustParse("20Gi"),
									},
								},
								{
									Consumer: "p13/p13/p13",
									Requests: &corev1.ResourceList{
										"nvidia.com/gpu": resource.MustParse("2"),
									},
								},
							},
						},
					},
				},
				{
					Type: katalystv1alpha1.TopologyTypeSocket,
					Name: "2",
					Children: []*katalystv1alpha1.TopologyZone{
						{
							Type: katalystv1alpha1.TopologyTypeNuma,
							Name: "2",
							Resources: katalystv1alpha1.Resources{
								Allocatable: &corev1.ResourceList{
									"cpu":            resource.MustParse("24"),
									"memory":         resource.MustParse("255Gi"),
									"nvidia.com/gpu": resource.MustParse("2"),
								},
								Capacity: &corev1.ResourceList{
									"cpu":            resource.MustParse("24"),
									"memory":         resource.MustParse("256Gi"),
									"nvidia.com/gpu": resource.MustParse("2"),
								},
							},
							Allocations: []*katalystv1alpha1.Allocation{},
						},
						{
							Type: katalystv1alpha1.TopologyTypeNuma,
							Name: "3",
							Resources: katalystv1alpha1.Resources{
								Allocatable: &corev1.ResourceList{
									"cpu":            resource.MustParse("24"),
									"memory":         resource.MustParse("255Gi"),
									"nvidia.com/gpu": resource.MustParse("2"),
								},
								Capacity: &corev1.ResourceList{
									"cpu":            resource.MustParse("24"),
									"memory":         resource.MustParse("256Gi"),
									"nvidia.com/gpu": resource.MustParse("2"),
								},
							},
							Allocations: []*katalystv1alpha1.Allocation{},
						},
					},
				},
			},
		},
	}

	p1 := testing_helper.MakePod().Namespace("p1").Name("p1").UID("p1").
		Req(map[v1.ResourceName]string{"cpu": "10", "memory": "20Gi", "nvidia.com/gpu": "1"}).
		Label("type", "t1").Annotation(util.QoSLevelKey, string(util.DedicatedCores)).Annotation(util.MemoyEnhancementKey, "{\"numa_binding\":\"true\",\"numa_exclusive\":\"false\"}").Obj()
	p2 := testing_helper.MakePod().Namespace("p2").Name("p2").UID("p2").
		Req(map[v1.ResourceName]string{"cpu": "10", "memory": "20Gi"}).
		Label("type", "t1").Annotation(util.QoSLevelKey, string(util.DedicatedCores)).Annotation(util.MemoyEnhancementKey, "{\"numa_binding\":\"true\",\"numa_exclusive\":\"false\"}").Obj()
	p3 := testing_helper.MakePod().Namespace("p3").Name("p3").UID("p3").
		Req(map[v1.ResourceName]string{"cpu": "10", "memory": "20Gi", "nvidia.com/gpu": "1"}).
		Label("type", "t1").Annotation(util.QoSLevelKey, string(util.DedicatedCores)).Annotation(util.MemoyEnhancementKey, "{\"numa_binding\":\"true\",\"numa_exclusive\":\"false\"}").Obj()
	p4 := testing_helper.MakePod().Namespace("p4").Name("p4").UID("p4").
		Req(map[v1.ResourceName]string{"cpu": "23"}).
		Label("type", "t1").Obj()
	p5 := testing_helper.MakePod().Namespace("p5").Name("p5").UID("p5").
		Req(map[v1.ResourceName]string{"cpu": "1"}).
		Label("type", "t1").Obj()
	p9 := testing_helper.MakePod().Namespace("p9").Name("p9").UID("p9").
		Req(map[v1.ResourceName]string{"cpu": "1", "memory": "1Gi", "nvidia.com/gpu": "1"}).
		Label("type", "t1").Obj()
	p10 := testing_helper.MakePod().Namespace("p10").Name("p10").UID("p10").
		Req(map[v1.ResourceName]string{"cpu": "1", "memory": "1Gi", "nvidia.com/gpu": "1"}).
		Label("type", "t1").Obj()
	p11 := testing_helper.MakePod().Namespace("p11").Name("p11").UID("p11").
		Req(map[v1.ResourceName]string{"cpu": "1", "memory": "1Gi", "nvidia.com/gpu": "1"}).
		Label("type", "t1").Obj()
	p12 := testing_helper.MakePod().Namespace("p12").Name("p12").UID("p12").
		Req(map[v1.ResourceName]string{"cpu": "1", "memory": "1Gi", "nvidia.com/gpu": "1"}).
		Label("type", "t1").Obj()
	p13 := testing_helper.MakePod().Namespace("p13").Name("p13").UID("p13").
		Req(map[v1.ResourceName]string{"cpu": "1", "memory": "1Gi", "nvidia.com/gpu": "2"}).
		Label("type", "t1").Obj()

	tests := []struct {
		name             string
		cnr              *katalystv1alpha1.CustomNodeResource
		node             *v1.Node
		resourceRequests map[string]*resource.Quantity
		existingPods     []*v1.Pod
		expectedStatus   *framework.Status
	}{
		{
			name: "pass",
			cnr:  cnr1,
			resourceRequests: map[string]*resource.Quantity{
				"cpu":            resource.NewQuantity(14, resource.DecimalSI),
				"memory":         resource.NewQuantity(76*1024*1024*1024, resource.BinarySI),
				"nvidia.com/gpu": resource.NewQuantity(2, resource.DecimalSI),
			},
			existingPods:   []*v1.Pod{p1, p2, p3, p4},
			expectedStatus: nil,
		},
		{
			name: "pass although node has no socket resource",
			cnr:  cnr1,
			node: node2,
			resourceRequests: map[string]*resource.Quantity{
				"cpu":            resource.NewQuantity(14, resource.DecimalSI),
				"memory":         resource.NewQuantity(76*1024*1024*1024, resource.BinarySI),
				"nvidia.com/gpu": resource.NewQuantity(2, resource.DecimalSI),
			},
			existingPods:   []*v1.Pod{p1, p2, p3, p4},
			expectedStatus: nil,
		},
		{
			name: "pass consider gpu allocations",
			cnr:  cnr4,
			resourceRequests: map[string]*resource.Quantity{
				"cpu":    resource.NewQuantity(14, resource.DecimalSI),
				"memory": resource.NewQuantity(76*1024*1024*1024, resource.BinarySI),
			},
			existingPods:   []*v1.Pod{p1, p2, p12, p13},
			expectedStatus: nil,
		},
		{
			name: "fail because numa available resource",
			cnr:  cnr1,
			resourceRequests: map[string]*resource.Quantity{
				"cpu":            resource.NewQuantity(24, resource.DecimalSI),
				"memory":         resource.NewQuantity(76*1024*1024*1024, resource.BinarySI),
				"nvidia.com/gpu": resource.NewQuantity(2, resource.DecimalSI),
			},
			existingPods:   []*v1.Pod{p1, p2, p3},
			expectedStatus: framework.NewStatus(framework.Unschedulable, NonExclusiveDedicatedCoresPodFailed),
		},
		{
			name: "fail because resources for shared cores pods",
			cnr:  cnr1,
			resourceRequests: map[string]*resource.Quantity{
				"cpu":            resource.NewQuantity(15, resource.DecimalSI),
				"memory":         resource.NewQuantity(76*1024*1024*1024, resource.BinarySI),
				"nvidia.com/gpu": resource.NewQuantity(3, resource.DecimalSI),
			},
			existingPods:   []*v1.Pod{p1, p2, p3, p5},
			expectedStatus: framework.NewStatus(framework.Unschedulable, NonExclusiveDedicatedCoresPodFailed),
		},
		{
			name: "pass although shared cores gpu in all numa",
			cnr:  cnr3,
			resourceRequests: map[string]*resource.Quantity{
				"cpu":    resource.NewQuantity(1, resource.DecimalSI),
				"memory": resource.NewQuantity(1*1024*1024*1024, resource.BinarySI),
			},
			existingPods:   []*v1.Pod{p9, p10, p11, p12},
			expectedStatus: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			nodeInfo := framework.NewNodeInfo()
			nodeInfo.SetCNR(tt.cnr)
			if tt.node != nil {
				nodeInfo.SetNode(tt.node)
			} else {
				nodeInfo.SetNode(node)
			}
			for _, existingPod := range tt.existingPods {
				nodeInfo.AddPod(existingPod)
			}
			podLister := testing_helper.NewFakePodLister(tt.existingPods)
			gotStatus := fitsNumaTopologyForNonExclusiveDedicatedQoS(nodeInfo, tt.resourceRequests, podLister)
			if !reflect.DeepEqual(tt.expectedStatus, gotStatus) {
				t.Errorf("expected status: %v, but got: %v", tt.expectedStatus, gotStatus)
			}
		})
	}
}

func TestFitsNumaTopologyForExclusiveDedicatedQoS(t *testing.T) {
	node := &v1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name: "node",
		},
		Status: v1.NodeStatus{
			Allocatable: v1.ResourceList{
				"cpu":                resource.MustParse("94"),
				"memory":             resource.MustParse("252Gi"),
				"nvidia.com/gpu":     resource.MustParse("8"),
				"bytedance.com/rdma": resource.MustParse("8"),
			},
			Capacity: v1.ResourceList{
				"cpu":                resource.MustParse("96"),
				"memory":             resource.MustParse("256Gi"),
				"nvidia.com/gpu":     resource.MustParse("8"),
				"bytedance.com/rdma": resource.MustParse("8"),
			},
		},
	}
	node2 := &v1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name: "node",
		},
		Status: v1.NodeStatus{
			Allocatable: v1.ResourceList{
				"cpu":                resource.MustParse("94"),
				"memory":             resource.MustParse("252Gi"),
				"nvidia.com/gpu":     resource.MustParse("8"),
				"bytedance.com/rdma": resource.MustParse("8"),
			},
			Capacity: v1.ResourceList{
				"cpu":                resource.MustParse("96"),
				"memory":             resource.MustParse("256Gi"),
				"nvidia.com/gpu":     resource.MustParse("8"),
				"bytedance.com/rdma": resource.MustParse("8"),
			},
		},
	}
	setCNR := func(a1, a2, a3, a4 *katalystv1alpha1.Allocation) *katalystv1alpha1.CustomNodeResource {
		return &katalystv1alpha1.CustomNodeResource{
			ObjectMeta: metav1.ObjectMeta{
				Name: "node",
			},
			Spec: katalystv1alpha1.CustomNodeResourceSpec{
				NodeResourceProperties: []*katalystv1alpha1.Property{
					{
						PropertyName:     util.ResourceNuma.String(),
						PropertyQuantity: resource.NewQuantity(4, resource.DecimalSI),
					},
				},
			},
			Status: katalystv1alpha1.CustomNodeResourceStatus{
				TopologyZone: []*katalystv1alpha1.TopologyZone{
					{
						Type: katalystv1alpha1.TopologyTypeSocket,
						Name: "0",
						Children: []*katalystv1alpha1.TopologyZone{
							{
								Type: katalystv1alpha1.TopologyTypeNuma,
								Name: "0",
								Resources: katalystv1alpha1.Resources{
									Allocatable: &v1.ResourceList{
										"cpu":                resource.MustParse("24"),
										"memory":             resource.MustParse("63Gi"),
										"nvidia.com/gpu":     resource.MustParse("2"),
										"bytedance.com/rdma": resource.MustParse("2"),
									},
									Capacity: &v1.ResourceList{
										"cpu":                resource.MustParse("24"),
										"memory":             resource.MustParse("64Gi"),
										"nvidia.com/gpu":     resource.MustParse("2"),
										"bytedance.com/rdma": resource.MustParse("2"),
									},
								},
								Allocations: []*katalystv1alpha1.Allocation{a1},
							},
							{
								Type: katalystv1alpha1.TopologyTypeNuma,
								Name: "1",
								Resources: katalystv1alpha1.Resources{
									Allocatable: &v1.ResourceList{
										"cpu":                resource.MustParse("24"),
										"memory":             resource.MustParse("63Gi"),
										"nvidia.com/gpu":     resource.MustParse("2"),
										"bytedance.com/rdma": resource.MustParse("2"),
									},
									Capacity: &v1.ResourceList{
										"cpu":                resource.MustParse("24"),
										"memory":             resource.MustParse("64Gi"),
										"nvidia.com/gpu":     resource.MustParse("2"),
										"bytedance.com/rdma": resource.MustParse("2"),
									},
								},
								Allocations: []*katalystv1alpha1.Allocation{a2},
							},
						},
					},
					{
						Type: katalystv1alpha1.TopologyTypeSocket,
						Name: "1",
						Children: []*katalystv1alpha1.TopologyZone{
							{
								Type: katalystv1alpha1.TopologyTypeNuma,
								Name: "2",
								Resources: katalystv1alpha1.Resources{
									Allocatable: &v1.ResourceList{
										"cpu":                resource.MustParse("23"),
										"memory":             resource.MustParse("63Gi"),
										"nvidia.com/gpu":     resource.MustParse("2"),
										"bytedance.com/rdma": resource.MustParse("2"),
									},
									Capacity: &v1.ResourceList{
										"cpu":                resource.MustParse("24"),
										"memory":             resource.MustParse("64Gi"),
										"nvidia.com/gpu":     resource.MustParse("2"),
										"bytedance.com/rdma": resource.MustParse("2"),
									},
								},
								Allocations: []*katalystv1alpha1.Allocation{a3},
							},
							{
								Type: katalystv1alpha1.TopologyTypeNuma,
								Name: "3",
								Resources: katalystv1alpha1.Resources{
									Allocatable: &v1.ResourceList{
										"cpu":                resource.MustParse("23"),
										"memory":             resource.MustParse("63Gi"),
										"nvidia.com/gpu":     resource.MustParse("2"),
										"bytedance.com/rdma": resource.MustParse("2"),
									},
									Capacity: &v1.ResourceList{
										"cpu":                resource.MustParse("24"),
										"memory":             resource.MustParse("64Gi"),
										"nvidia.com/gpu":     resource.MustParse("2"),
										"bytedance.com/rdma": resource.MustParse("2"),
									},
								},
								Allocations: []*katalystv1alpha1.Allocation{a4},
							},
						},
					},
				},
			},
		}
	}

	tests := []struct {
		name             string
		node             *v1.Node
		allocations      []*katalystv1alpha1.Allocation
		resourceRequests map[string]*resource.Quantity
		existingPods     []*v1.Pod
		alignedResources sets.String
		expectedStatus   *framework.Status
	}{
		{
			name: "cpu: 1, {0}; memory 2, {0, 1}; fail because numa 1 can not allocate memory",
			allocations: []*katalystv1alpha1.Allocation{
				{},
				{
					Consumer: "a/a/a",
					Requests: &v1.ResourceList{
						"memory": resource.MustParse("1Gi"),
					},
				},
				{
					Consumer: "b/b/b",
					Requests: &v1.ResourceList{
						"cpu":    resource.MustParse("1"),
						"memory": resource.MustParse("1Gi"),
					},
				},
				{
					Consumer: "b/b/b",
					Requests: &v1.ResourceList{
						"cpu":    resource.MustParse("1"),
						"memory": resource.MustParse("1Gi"),
					},
				},
			},
			resourceRequests: map[string]*resource.Quantity{
				"cpu":    resource.NewQuantity(15, resource.DecimalSI),
				"memory": resource.NewQuantity(100*1024*1024*1024, resource.BinarySI),
			},
			expectedStatus: framework.NewStatus(framework.Unschedulable, ExclusiveDedicatedCoresPodFailed),
		},
		{
			name: "cpu: 2, {0, 1}; memory 1, {0}; fail because numa 1 can not allocate cpu",
			allocations: []*katalystv1alpha1.Allocation{
				{},
				{
					Consumer: "a/a/a",
					Requests: &v1.ResourceList{
						"cpu": resource.MustParse("1"),
					},
				},
				{
					Consumer: "b/b/b",
					Requests: &v1.ResourceList{
						"cpu":    resource.MustParse("1"),
						"memory": resource.MustParse("1Gi"),
					},
				},
				{
					Consumer: "b/b/b",
					Requests: &v1.ResourceList{
						"cpu":    resource.MustParse("1"),
						"memory": resource.MustParse("1Gi"),
					},
				},
			},
			resourceRequests: map[string]*resource.Quantity{
				"cpu":    resource.NewQuantity(40, resource.DecimalSI),
				"memory": resource.NewQuantity(50*1024*1024*1024, resource.BinarySI),
			},
			expectedStatus: framework.NewStatus(framework.Unschedulable, ExclusiveDedicatedCoresPodFailed),
		},
		{
			name: "cpu: 2, {1, 2}; memory 1, {0}, {1}, {2}, {3}; fail because cpu cross socket",
			allocations: []*katalystv1alpha1.Allocation{
				{
					Consumer: "a/a/a",
					Requests: &v1.ResourceList{
						"cpu": resource.MustParse("1"),
					},
				},
				{},
				{},
				{
					Consumer: "b/b/b",
					Requests: &v1.ResourceList{
						"cpu": resource.MustParse("1"),
					},
				},
			},
			resourceRequests: map[string]*resource.Quantity{
				"cpu":    resource.NewQuantity(40, resource.DecimalSI),
				"memory": resource.NewQuantity(50*1024*1024*1024, resource.BinarySI),
			},
			expectedStatus: framework.NewStatus(framework.Unschedulable, ExclusiveDedicatedCoresPodFailed),
		},
		{
			name: "cpu: 1, {0}, {1}, {2}, {3}; memory 2, {1, 2}; fail because memory cross socket",
			allocations: []*katalystv1alpha1.Allocation{
				{
					Consumer: "a/a/a",
					Requests: &v1.ResourceList{
						"memory": resource.MustParse("1Gi"),
					},
				},
				{},
				{},
				{
					Consumer: "b/b/b",
					Requests: &v1.ResourceList{
						"memory": resource.MustParse("1Gi"),
					},
				},
			},
			resourceRequests: map[string]*resource.Quantity{
				"cpu":    resource.NewQuantity(20, resource.DecimalSI),
				"memory": resource.NewQuantity(100*1024*1024*1024, resource.BinarySI),
			},
			expectedStatus: framework.NewStatus(framework.Unschedulable, ExclusiveDedicatedCoresPodFailed),
		},
		{
			name: "cpu: 4, {2, 3}; memory 4, {2, 3}; gpu 1, {2, 3}; fail because gpu can only use one numa",
			allocations: []*katalystv1alpha1.Allocation{
				{
					Consumer: "a/a/a",
					Requests: &v1.ResourceList{
						"cpu":    resource.MustParse("1"),
						"memory": resource.MustParse("1Gi"),
					},
				},
				{
					Consumer: "b/b/b",
					Requests: &v1.ResourceList{
						"nvidia.com/gpu": resource.MustParse("2"),
					},
				},
				{
					Consumer: "c/c/c",
					Requests: &v1.ResourceList{
						"nvidia.com/gpu": resource.MustParse("1"),
					},
				},
				{
					Consumer: "d/d/d",
					Requests: &v1.ResourceList{
						"nvidia.com/gpu": resource.MustParse("1"),
					},
				},
			},
			resourceRequests: map[string]*resource.Quantity{
				"cpu":            resource.NewQuantity(40, resource.DecimalSI),
				"memory":         resource.NewQuantity(100*1024*1024*1024, resource.BinarySI),
				"nvidia.com/gpu": resource.NewQuantity(2, resource.DecimalSI),
			},
			expectedStatus: framework.NewStatus(framework.Unschedulable, ExclusiveDedicatedCoresPodFailed),
		},
		{
			name: "cpu: 4, {0, 1, 2, 3}; memory 4, {0, 1, 2, 3}; gpu 1, {0, 3}; fail because gpu can only use one numa",
			allocations: []*katalystv1alpha1.Allocation{
				{
					Consumer: "a/a/a",
					Requests: &v1.ResourceList{
						"nvidia.com/gpu": resource.MustParse("1"),
					},
				},
				{
					Consumer: "b/b/b",
					Requests: &v1.ResourceList{
						"nvidia.com/gpu": resource.MustParse("2"),
					},
				},
				{
					Consumer: "c/c/c",
					Requests: &v1.ResourceList{
						"nvidia.com/gpu": resource.MustParse("2"),
					},
				},
				{
					Consumer: "d/d/d",
					Requests: &v1.ResourceList{
						"nvidia.com/gpu": resource.MustParse("1"),
					},
				},
			},
			resourceRequests: map[string]*resource.Quantity{
				"cpu":            resource.NewQuantity(90, resource.DecimalSI),
				"memory":         resource.NewQuantity(250*1024*1024*1024, resource.BinarySI),
				"nvidia.com/gpu": resource.NewQuantity(2, resource.DecimalSI),
			},
			expectedStatus: framework.NewStatus(framework.Unschedulable, ExclusiveDedicatedCoresPodFailed),
		},
		{
			name: "cpu: 4, {0, 1, 2, 3}; memory 4, {0, 1, 2, 3}; gpu 1, {0, 3}; align with gpu, fail because gpu can only use one numa",
			allocations: []*katalystv1alpha1.Allocation{
				{
					Consumer: "a/a/a",
					Requests: &v1.ResourceList{
						"nvidia.com/gpu": resource.MustParse("1"),
					},
				},
				{
					Consumer: "b/b/b",
					Requests: &v1.ResourceList{
						"nvidia.com/gpu": resource.MustParse("2"),
					},
				},
				{
					Consumer: "c/c/c",
					Requests: &v1.ResourceList{
						"nvidia.com/gpu": resource.MustParse("2"),
					},
				},
				{
					Consumer: "d/d/d",
					Requests: &v1.ResourceList{
						"nvidia.com/gpu": resource.MustParse("1"),
					},
				},
			},
			resourceRequests: map[string]*resource.Quantity{
				"cpu":            resource.NewQuantity(90, resource.DecimalSI),
				"memory":         resource.NewQuantity(250*1024*1024*1024, resource.BinarySI),
				"nvidia.com/gpu": resource.NewQuantity(2, resource.DecimalSI),
			},
			alignedResources: sets.NewString(util.ResourceGPU.String()),
			expectedStatus:   framework.NewStatus(framework.Unschedulable, ExclusiveDedicatedCoresPodFailed),
		},
		{
			name: "cpu: 2, {0, 1}; memory 2, {0, 1}; gpu 4, {0, 1, 2 3}; fail because shared cores",
			allocations: []*katalystv1alpha1.Allocation{
				{
					Consumer: "a/a/a",
					Requests: &v1.ResourceList{
						"nvidia.com/gpu": resource.MustParse("1"),
					},
				},
				{},
				{
					Consumer: "b/b/b",
					Requests: &v1.ResourceList{
						"cpu":    resource.MustParse("1"),
						"memory": resource.MustParse("1Gi"),
					},
				},
				{
					Consumer: "c/c/c",
					Requests: &v1.ResourceList{
						"cpu":    resource.MustParse("1"),
						"memory": resource.MustParse("1Gi"),
					},
				},
			},
			existingPods: []*v1.Pod{
				testing_helper.MakePod().Namespace("p1").Name("p1").UID("p1").
					Req(map[v1.ResourceName]string{"cpu": "1"}).Obj(),
			},
			resourceRequests: map[string]*resource.Quantity{
				"cpu":            resource.NewQuantity(50, resource.DecimalSI),
				"memory":         resource.NewQuantity(100*1024*1024*1024, resource.BinarySI),
				"nvidia.com/gpu": resource.NewQuantity(7, resource.DecimalSI),
			},
			expectedStatus: framework.NewStatus(framework.Unschedulable, ExclusiveDedicatedCoresPodFailed),
		},
		{
			name: "cpu: 2, {0, 1}; memory 2, {0, 1}; gpu 4, {0, 1, 2 3}; pass",
			allocations: []*katalystv1alpha1.Allocation{
				{
					Consumer: "a/a/a",
					Requests: &v1.ResourceList{
						"nvidia.com/gpu": resource.MustParse("1"),
					},
				},
				{},
				{
					Consumer: "b/b/b",
					Requests: &v1.ResourceList{
						"cpu":    resource.MustParse("1"),
						"memory": resource.MustParse("1Gi"),
					},
				},
				{
					Consumer: "c/c/c",
					Requests: &v1.ResourceList{
						"cpu":    resource.MustParse("1"),
						"memory": resource.MustParse("1Gi"),
					},
				},
			},
			resourceRequests: map[string]*resource.Quantity{
				"cpu":            resource.NewQuantity(40, resource.DecimalSI),
				"memory":         resource.NewQuantity(100*1024*1024*1024, resource.BinarySI),
				"nvidia.com/gpu": resource.NewQuantity(7, resource.DecimalSI),
			},
			expectedStatus: nil,
		},
		{
			name: "cpu: 2, {0, 1}; memory 2, {0, 1}; gpu 4, {0, 1, 2 3}; pass although node has no socket resource",
			node: node2,
			allocations: []*katalystv1alpha1.Allocation{
				{
					Consumer: "a/a/a",
					Requests: &v1.ResourceList{
						"nvidia.com/gpu": resource.MustParse("1"),
					},
				},
				{},
				{
					Consumer: "b/b/b",
					Requests: &v1.ResourceList{
						"cpu":    resource.MustParse("1"),
						"memory": resource.MustParse("1Gi"),
					},
				},
				{
					Consumer: "c/c/c",
					Requests: &v1.ResourceList{
						"cpu":    resource.MustParse("1"),
						"memory": resource.MustParse("1Gi"),
					},
				},
			},
			resourceRequests: map[string]*resource.Quantity{
				"cpu":            resource.NewQuantity(40, resource.DecimalSI),
				"memory":         resource.NewQuantity(100*1024*1024*1024, resource.BinarySI),
				"nvidia.com/gpu": resource.NewQuantity(7, resource.DecimalSI),
			},
			expectedStatus: nil,
		},
		{
			name: "cpu: 2, {0, 1}; memory 2, {0, 1}; gpu 4, {0, 1, 2 3}; align with gpu, fail because numa 2&3 can not locate cpu&emory",
			allocations: []*katalystv1alpha1.Allocation{
				{
					Consumer: "a/a/a",
					Requests: &v1.ResourceList{
						"nvidia.com/gpu": resource.MustParse("1"),
					},
				},
				{},
				{
					Consumer: "b/b/b",
					Requests: &v1.ResourceList{
						"cpu":    resource.MustParse("1"),
						"memory": resource.MustParse("1Gi"),
					},
				},
				{
					Consumer: "c/c/c",
					Requests: &v1.ResourceList{
						"cpu":    resource.MustParse("1"),
						"memory": resource.MustParse("1Gi"),
					},
				},
			},
			resourceRequests: map[string]*resource.Quantity{
				"cpu":            resource.NewQuantity(50, resource.DecimalSI),
				"memory":         resource.NewQuantity(100*1024*1024*1024, resource.BinarySI),
				"nvidia.com/gpu": resource.NewQuantity(7, resource.DecimalSI),
			},
			alignedResources: sets.NewString(util.ResourceGPU.String()),
			expectedStatus:   framework.NewStatus(framework.Unschedulable, ExclusiveDedicatedCoresPodFailed),
		},
		{
			name: "cpu: 2, {0, 1}; memory 2, {0, 1}; gpu 1, {0}; rdma 1, {1}; fail because no containment relationship between gpu and rdma",
			allocations: []*katalystv1alpha1.Allocation{
				{
					Consumer: "a/a/a",
					Requests: &v1.ResourceList{
						"nvidia.com/gpu":     resource.MustParse("1"),
						"bytedance.com/rdma": resource.MustParse("2"),
					},
				},
				{
					Consumer: "a/a/a",
					Requests: &v1.ResourceList{
						"nvidia.com/gpu":     resource.MustParse("2"),
						"bytedance.com/rdma": resource.MustParse("1"),
					},
				},
				{
					Consumer: "b/b/b",
					Requests: &v1.ResourceList{
						"cpu":                resource.MustParse("1"),
						"memory":             resource.MustParse("1Gi"),
						"nvidia.com/gpu":     resource.MustParse("2"),
						"bytedance.com/rdma": resource.MustParse("2"),
					},
				},
				{
					Consumer: "c/c/c",
					Requests: &v1.ResourceList{
						"cpu":                resource.MustParse("1"),
						"memory":             resource.MustParse("1Gi"),
						"nvidia.com/gpu":     resource.MustParse("2"),
						"bytedance.com/rdma": resource.MustParse("2"),
					},
				},
			},
			resourceRequests: map[string]*resource.Quantity{
				"cpu":                resource.NewQuantity(40, resource.DecimalSI),
				"memory":             resource.NewQuantity(100*1024*1024*1024, resource.BinarySI),
				"nvidia.com/gpu":     resource.NewQuantity(1, resource.DecimalSI),
				"bytedance.com/rdma": resource.NewQuantity(1, resource.DecimalSI),
			},
			expectedStatus: framework.NewStatus(framework.Unschedulable, ExclusiveDedicatedCoresPodFailed),
		},
		{
			name: "cpu: 2, {0, 1}; memory 2, {0, 1}; gpu 3, {0, 1, 2}; rdma 3, {0, 1, 3}; fail because no containment relationship between gpu and rdma",
			allocations: []*katalystv1alpha1.Allocation{
				{
					Consumer: "a/a/a",
					Requests: &v1.ResourceList{
						"nvidia.com/gpu": resource.MustParse("1"),
					},
				},
				{
					Consumer: "a/a/a",
					Requests: &v1.ResourceList{
						"bytedance.com/rdma": resource.MustParse("1"),
					},
				},
				{
					Consumer: "b/b/b",
					Requests: &v1.ResourceList{
						"cpu":                resource.MustParse("1"),
						"memory":             resource.MustParse("1Gi"),
						"bytedance.com/rdma": resource.MustParse("2"),
					},
				},
				{
					Consumer: "c/c/c",
					Requests: &v1.ResourceList{
						"cpu":            resource.MustParse("1"),
						"memory":         resource.MustParse("1Gi"),
						"nvidia.com/gpu": resource.MustParse("2"),
					},
				},
			},
			resourceRequests: map[string]*resource.Quantity{
				"cpu":                resource.NewQuantity(50, resource.DecimalSI),
				"memory":             resource.NewQuantity(100*1024*1024*1024, resource.BinarySI),
				"nvidia.com/gpu":     resource.NewQuantity(5, resource.DecimalSI),
				"bytedance.com/rdma": resource.NewQuantity(5, resource.DecimalSI),
			},
			expectedStatus: framework.NewStatus(framework.Unschedulable, ExclusiveDedicatedCoresPodFailed),
		},
		{
			name: "cpu: 1, {2}; memory 1, {2}; gpu 3, {0, 1, 2}; rdma 2, {0, 1}; fail because no containment relationship between cpu/memory and rdma",
			allocations: []*katalystv1alpha1.Allocation{
				{
					Consumer: "a/a/a",
					Requests: &v1.ResourceList{
						"cpu":            resource.MustParse("1"),
						"memory":         resource.MustParse("1Gi"),
						"nvidia.com/gpu": resource.MustParse("1"),
					},
				},
				{
					Consumer: "a/a/a",
					Requests: &v1.ResourceList{
						"cpu":                resource.MustParse("1"),
						"memory":             resource.MustParse("1Gi"),
						"bytedance.com/rdma": resource.MustParse("1"),
					},
				},
				{
					Consumer: "b/b/b",
					Requests: &v1.ResourceList{
						"memory":             resource.MustParse("1Gi"),
						"bytedance.com/rdma": resource.MustParse("2"),
					},
				},
				{
					Consumer: "c/c/c",
					Requests: &v1.ResourceList{
						"cpu":                resource.MustParse("1"),
						"memory":             resource.MustParse("1Gi"),
						"nvidia.com/gpu":     resource.MustParse("2"),
						"bytedance.com/rdma": resource.MustParse("2"),
					},
				},
			},
			resourceRequests: map[string]*resource.Quantity{
				"cpu":                resource.NewQuantity(20, resource.DecimalSI),
				"memory":             resource.NewQuantity(50*1024*1024*1024, resource.BinarySI),
				"nvidia.com/gpu":     resource.NewQuantity(5, resource.DecimalSI),
				"bytedance.com/rdma": resource.NewQuantity(3, resource.DecimalSI),
			},
			expectedStatus: framework.NewStatus(framework.Unschedulable, ExclusiveDedicatedCoresPodFailed),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			nodeInfo := framework.NewNodeInfo()
			cnr := setCNR(tt.allocations[0], tt.allocations[1], tt.allocations[2], tt.allocations[3])
			nodeInfo.SetCNR(cnr)
			if tt.node != nil {
				nodeInfo.SetNode(tt.node)
			} else {
				nodeInfo.SetNode(node)
			}
			for _, existingPod := range tt.existingPods {
				nodeInfo.AddPod(existingPod)
			}
			gotStatus := fitsNumaTopologyForExclusiveDedicatedQoS(nodeInfo, tt.resourceRequests, tt.alignedResources)
			if !reflect.DeepEqual(tt.expectedStatus, gotStatus) {
				t.Errorf("expected status: %v, but got: %v", tt.expectedStatus, gotStatus)
			}
		})
	}
}

func TestFitsNumaTopologyForSharedQoS(t *testing.T) {
	p1 := testing_helper.MakePod().Namespace("default").Name("p1").UID("p1").
		Annotation(util.QoSLevelKey, string(util.DedicatedCores)).
		Annotation(util.MemoyEnhancementKey, "{\"numa_binding\":\"true\",\"numa_exclusive\":\"true\"}").
		Req(map[corev1.ResourceName]string{"cpu": "16", "memory": "32Gi", "nvidia.com/gpu": "1", "bytedance.com/other": "1"}).Obj()
	p2 := testing_helper.MakePod().Namespace("default").Name("p2").UID("p2").
		Annotation(util.QoSLevelKey, string(util.DedicatedCores)).
		Annotation(util.MemoyEnhancementKey, "{\"numa_binding\":\"true\",\"numa_exclusive\":\"false\"}").
		Label("class", "c1").Label("type", "t1").
		Req(map[corev1.ResourceName]string{"cpu": "10", "memory": "120Gi", "nvidia.com/gpu": "1", "bytedance.com/other": "1"}).Obj()
	p3 := testing_helper.MakePod().Namespace("default").Name("p3").UID("p3").
		Req(map[corev1.ResourceName]string{"cpu": "23", "memory": "255Gi", "bytedance.com/other": "1"}).Obj()
	p4 := testing_helper.MakePod().Namespace("default").Name("p4").UID("p4").
		Req(map[corev1.ResourceName]string{"cpu": "22", "memory": "254Gi", "bytedance.com/other": "1"}).Obj()
	p5 := testing_helper.MakePod().Namespace("default").Name("p5").UID("p5").
		Annotation(util.QoSLevelKey, string(util.DedicatedCores)).
		Annotation(util.MemoyEnhancementKey, "{\"numa_binding\":\"true\",\"numa_exclusive\":\"true\"}").
		Req(map[corev1.ResourceName]string{"cpu": "16", "memory": "32Gi", "nvidia.com/gpu": "1", "bytedance.com/other": "1"}).Obj()
	p6 := testing_helper.MakePod().Namespace("default").Name("p6").UID("p6").
		Label("class", "c1").Label("type", "t1").
		Req(map[corev1.ResourceName]string{"cpu": "10", "memory": "120Gi", "nvidia.com/gpu": "1", "bytedance.com/other": "1"}).Obj()
	p7 := testing_helper.MakePod().Namespace("default").Name("p1").UID("p1").
		Req(map[corev1.ResourceName]string{"cpu": "1", "memory": "1Gi"}).Obj()

	cnr1 := &katalystv1alpha1.CustomNodeResource{
		ObjectMeta: metav1.ObjectMeta{
			Name: "n",
		},
		Spec: katalystv1alpha1.CustomNodeResourceSpec{
			NodeResourceProperties: []*katalystv1alpha1.Property{
				{
					PropertyName:     util.ResourceNuma.String(),
					PropertyQuantity: resource.NewQuantity(2, resource.DecimalSI),
				},
			},
		},
		Status: katalystv1alpha1.CustomNodeResourceStatus{
			TopologyZone: []*katalystv1alpha1.TopologyZone{
				{
					Type: katalystv1alpha1.TopologyTypeSocket,
					Name: "0",
					Children: []*katalystv1alpha1.TopologyZone{
						{
							Type: katalystv1alpha1.TopologyTypeNuma,
							Name: "0",
							Resources: katalystv1alpha1.Resources{
								Allocatable: &corev1.ResourceList{
									"cpu":            resource.MustParse("23"),
									"memory":         resource.MustParse("255Gi"),
									"nvidia.com/gpu": resource.MustParse("2"),
								},
								Capacity: &corev1.ResourceList{
									"cpu":            resource.MustParse("24"),
									"memory":         resource.MustParse("256Gi"),
									"nvidia.com/gpu": resource.MustParse("2"),
								},
							},
							Allocations: []*katalystv1alpha1.Allocation{
								{
									Consumer: "default/p1/p1",
									Requests: &corev1.ResourceList{
										"cpu":            resource.MustParse("23"),
										"memory":         resource.MustParse("255Gi"),
										"nvidia.com/gpu": resource.MustParse("2"),
									},
								},
							},
						},
						{
							Type: katalystv1alpha1.TopologyTypeNuma,
							Name: "1",
							Resources: katalystv1alpha1.Resources{
								Allocatable: &corev1.ResourceList{
									"cpu":            resource.MustParse("23"),
									"memory":         resource.MustParse("255Gi"),
									"nvidia.com/gpu": resource.MustParse("2"),
								},
								Capacity: &corev1.ResourceList{
									"cpu":            resource.MustParse("24"),
									"memory":         resource.MustParse("256Gi"),
									"nvidia.com/gpu": resource.MustParse("2"),
								},
							},
							Allocations: []*katalystv1alpha1.Allocation{
								{
									Consumer: "default/p2/p2",
									Requests: &corev1.ResourceList{
										"cpu":            resource.MustParse("10"),
										"memory":         resource.MustParse("120Gi"),
										"nvidia.com/gpu": resource.MustParse("1"),
									},
								},
							},
						},
					},
				},
			},
		},
	}

	cnr2 := &katalystv1alpha1.CustomNodeResource{
		ObjectMeta: metav1.ObjectMeta{
			Name: "n",
		},
		Spec: katalystv1alpha1.CustomNodeResourceSpec{
			NodeResourceProperties: []*katalystv1alpha1.Property{
				{
					PropertyName:     util.ResourceNuma.String(),
					PropertyQuantity: resource.NewQuantity(2, resource.DecimalSI),
				},
			},
		},
		Status: katalystv1alpha1.CustomNodeResourceStatus{
			TopologyZone: []*katalystv1alpha1.TopologyZone{
				{
					Type: katalystv1alpha1.TopologyTypeSocket,
					Name: "0",
					Children: []*katalystv1alpha1.TopologyZone{
						{
							Type: katalystv1alpha1.TopologyTypeNuma,
							Name: "0",
							Resources: katalystv1alpha1.Resources{
								Allocatable: &corev1.ResourceList{
									"cpu":            resource.MustParse("23"),
									"memory":         resource.MustParse("255Gi"),
									"nvidia.com/gpu": resource.MustParse("2"),
								},
								Capacity: &corev1.ResourceList{
									"cpu":            resource.MustParse("24"),
									"memory":         resource.MustParse("256Gi"),
									"nvidia.com/gpu": resource.MustParse("2"),
								},
							},
							Allocations: []*katalystv1alpha1.Allocation{
								{
									Consumer: "default/p1/p1",
									Requests: &corev1.ResourceList{
										"cpu":            resource.MustParse("23"),
										"memory":         resource.MustParse("255Gi"),
										"nvidia.com/gpu": resource.MustParse("2"),
									},
								},
							},
						},
						{
							Type: katalystv1alpha1.TopologyTypeNuma,
							Name: "1",
							Resources: katalystv1alpha1.Resources{
								Allocatable: &corev1.ResourceList{
									"cpu":            resource.MustParse("23"),
									"memory":         resource.MustParse("255Gi"),
									"nvidia.com/gpu": resource.MustParse("2"),
								},
								Capacity: &corev1.ResourceList{
									"cpu":            resource.MustParse("24"),
									"memory":         resource.MustParse("256Gi"),
									"nvidia.com/gpu": resource.MustParse("2"),
								},
							},
							Allocations: []*katalystv1alpha1.Allocation{},
						},
					},
				},
			},
		},
	}

	cnr3 := &katalystv1alpha1.CustomNodeResource{
		ObjectMeta: metav1.ObjectMeta{
			Name: "n",
		},
		Spec: katalystv1alpha1.CustomNodeResourceSpec{
			NodeResourceProperties: []*katalystv1alpha1.Property{
				{
					PropertyName:     util.ResourceNuma.String(),
					PropertyQuantity: resource.NewQuantity(2, resource.DecimalSI),
				},
			},
		},
		Status: katalystv1alpha1.CustomNodeResourceStatus{
			TopologyZone: []*katalystv1alpha1.TopologyZone{
				{
					Type: katalystv1alpha1.TopologyTypeSocket,
					Name: "0",
					Children: []*katalystv1alpha1.TopologyZone{
						{
							Type: katalystv1alpha1.TopologyTypeNuma,
							Name: "0",
							Resources: katalystv1alpha1.Resources{
								Allocatable: &corev1.ResourceList{
									"cpu":            resource.MustParse("23"),
									"memory":         resource.MustParse("255Gi"),
									"nvidia.com/gpu": resource.MustParse("2"),
								},
								Capacity: &corev1.ResourceList{
									"cpu":            resource.MustParse("24"),
									"memory":         resource.MustParse("256Gi"),
									"nvidia.com/gpu": resource.MustParse("2"),
								},
							},
							Allocations: []*katalystv1alpha1.Allocation{},
						},
						{
							Type: katalystv1alpha1.TopologyTypeNuma,
							Name: "1",
							Resources: katalystv1alpha1.Resources{
								Allocatable: &corev1.ResourceList{
									"cpu":            resource.MustParse("23"),
									"memory":         resource.MustParse("255Gi"),
									"nvidia.com/gpu": resource.MustParse("2"),
								},
								Capacity: &corev1.ResourceList{
									"cpu":            resource.MustParse("24"),
									"memory":         resource.MustParse("256Gi"),
									"nvidia.com/gpu": resource.MustParse("2"),
								},
							},
							Allocations: []*katalystv1alpha1.Allocation{
								{
									Consumer: "default/p2/p2",
									Requests: &corev1.ResourceList{
										"cpu":            resource.MustParse("10"),
										"memory":         resource.MustParse("120Gi"),
										"nvidia.com/gpu": resource.MustParse("1"),
									},
								},
							},
						},
					},
				},
			},
		},
	}

	cnr5 := &katalystv1alpha1.CustomNodeResource{
		ObjectMeta: metav1.ObjectMeta{
			Name: "n",
		},
		Spec: katalystv1alpha1.CustomNodeResourceSpec{
			NodeResourceProperties: []*katalystv1alpha1.Property{
				{
					PropertyName:     util.ResourceNuma.String(),
					PropertyQuantity: resource.NewQuantity(2, resource.DecimalSI),
				},
			},
		},
		Status: katalystv1alpha1.CustomNodeResourceStatus{
			TopologyZone: []*katalystv1alpha1.TopologyZone{
				{
					Type: katalystv1alpha1.TopologyTypeSocket,
					Name: "0",
					Children: []*katalystv1alpha1.TopologyZone{
						{
							Type: katalystv1alpha1.TopologyTypeNuma,
							Name: "0",
							Resources: katalystv1alpha1.Resources{
								Allocatable: &corev1.ResourceList{
									"cpu":            resource.MustParse("23"),
									"memory":         resource.MustParse("255Gi"),
									"nvidia.com/gpu": resource.MustParse("2"),
								},
								Capacity: &corev1.ResourceList{
									"cpu":            resource.MustParse("24"),
									"memory":         resource.MustParse("256Gi"),
									"nvidia.com/gpu": resource.MustParse("2"),
								},
							},
							Allocations: []*katalystv1alpha1.Allocation{},
						},
						{
							Type: katalystv1alpha1.TopologyTypeNuma,
							Name: "1",
							Resources: katalystv1alpha1.Resources{
								Allocatable: &corev1.ResourceList{
									"cpu":            resource.MustParse("23"),
									"memory":         resource.MustParse("255Gi"),
									"nvidia.com/gpu": resource.MustParse("2"),
								},
								Capacity: &corev1.ResourceList{
									"cpu":            resource.MustParse("24"),
									"memory":         resource.MustParse("256Gi"),
									"nvidia.com/gpu": resource.MustParse("2"),
								},
							},
							Allocations: []*katalystv1alpha1.Allocation{
								{
									Consumer: "default/p2/p2",
									Requests: &corev1.ResourceList{
										"cpu":            resource.MustParse("10"),
										"memory":         resource.MustParse("120Gi"),
										"nvidia.com/gpu": resource.MustParse("1"),
									},
								},
								{
									Consumer: "default/p6/p6",
									Requests: &corev1.ResourceList{
										"nvidia.com/gpu": resource.MustParse("1"),
									},
								},
							},
						},
					},
				},
			},
		},
	}

	cnr6 := &katalystv1alpha1.CustomNodeResource{
		ObjectMeta: metav1.ObjectMeta{
			Name: "n",
		},
		Spec: katalystv1alpha1.CustomNodeResourceSpec{
			NodeResourceProperties: []*katalystv1alpha1.Property{
				{
					PropertyName:     util.ResourceNuma.String(),
					PropertyQuantity: resource.NewQuantity(2, resource.DecimalSI),
				},
			},
		},
		Status: katalystv1alpha1.CustomNodeResourceStatus{
			TopologyZone: []*katalystv1alpha1.TopologyZone{
				{
					Type: katalystv1alpha1.TopologyTypeSocket,
					Name: "0",
					Children: []*katalystv1alpha1.TopologyZone{
						{
							Type: katalystv1alpha1.TopologyTypeNuma,
							Name: "0",
							Resources: katalystv1alpha1.Resources{
								Allocatable: &corev1.ResourceList{
									"cpu":            resource.MustParse("23"),
									"memory":         resource.MustParse("255Gi"),
									"nvidia.com/gpu": resource.MustParse("2"),
								},
								Capacity: &corev1.ResourceList{
									"cpu":            resource.MustParse("24"),
									"memory":         resource.MustParse("256Gi"),
									"nvidia.com/gpu": resource.MustParse("2"),
								},
							},
							Allocations: []*katalystv1alpha1.Allocation{
								{
									Consumer: "default/p1/p1",
									Requests: &corev1.ResourceList{
										"cpu":            resource.MustParse("23"),
										"memory":         resource.MustParse("255Gi"),
										"nvidia.com/gpu": resource.MustParse("2"),
									},
								},
							},
						},
						{
							Type: katalystv1alpha1.TopologyTypeNuma,
							Name: "1",
							Resources: katalystv1alpha1.Resources{
								Allocatable: &corev1.ResourceList{
									"cpu":            resource.MustParse("23"),
									"memory":         resource.MustParse("255Gi"),
									"nvidia.com/gpu": resource.MustParse("2"),
								},
								Capacity: &corev1.ResourceList{
									"cpu":            resource.MustParse("24"),
									"memory":         resource.MustParse("256Gi"),
									"nvidia.com/gpu": resource.MustParse("2"),
								},
							},
							Allocations: []*katalystv1alpha1.Allocation{
								{
									Consumer: "default/p5/p5",
									Requests: &corev1.ResourceList{
										"cpu":            resource.MustParse("23"),
										"memory":         resource.MustParse("255Gi"),
										"nvidia.com/gpu": resource.MustParse("2"),
									},
								},
							},
						},
					},
				},
			},
		},
	}

	node := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name: "n",
		},
		Status: corev1.NodeStatus{
			Allocatable: corev1.ResourceList{
				"cpu":                 resource.MustParse("46"),
				"memory":              resource.MustParse("510Gi"),
				"nvidia.com/gpu":      resource.MustParse("4"),
				"bytedance.com/other": resource.MustParse("128"),
			},
			Capacity: corev1.ResourceList{
				"cpu":                 resource.MustParse("48"),
				"memory":              resource.MustParse("512Gi"),
				"nvidia.com/gpu":      resource.MustParse("4"),
				"bytedance.com/other": resource.MustParse("128"),
			},
		},
	}
	node2 := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name: "n",
		},
		Status: corev1.NodeStatus{
			Allocatable: corev1.ResourceList{
				"cpu":                 resource.MustParse("46"),
				"memory":              resource.MustParse("510Gi"),
				"nvidia.com/gpu":      resource.MustParse("4"),
				"bytedance.com/other": resource.MustParse("128"),
			},
			Capacity: corev1.ResourceList{
				"cpu":                 resource.MustParse("48"),
				"memory":              resource.MustParse("512Gi"),
				"nvidia.com/gpu":      resource.MustParse("4"),
				"bytedance.com/other": resource.MustParse("128"),
			},
		},
	}

	tests := []struct {
		name             string
		existingPods     []*v1.Pod
		node             *v1.Node
		cnr              *katalystv1alpha1.CustomNodeResource
		resourceRequests map[string]*resource.Quantity
		expectedStatus   *framework.Status
	}{
		{
			name:         "fail, there are monopolize pod and dedicated cores pod",
			existingPods: []*v1.Pod{p1, p2},
			cnr:          cnr1,
			resourceRequests: map[string]*resource.Quantity{
				"cpu":                 resource.NewQuantity(1, resource.DecimalSI),
				"memory":              resource.NewQuantity(1*1024*1024*1024, resource.BinarySI),
				"bytedance.com/other": resource.NewQuantity(1, resource.DecimalSI),
			},
			expectedStatus: framework.NewStatus(framework.Unschedulable, SharedCoresPodFailed),
		},
		{
			name:         "fail, there is monopolize pod",
			existingPods: []*v1.Pod{p1, p3},
			cnr:          cnr2,
			resourceRequests: map[string]*resource.Quantity{
				"cpu":                 resource.NewQuantity(1, resource.DecimalSI),
				"memory":              resource.NewQuantity(1*1024*1024*1024, resource.BinarySI),
				"bytedance.com/other": resource.NewQuantity(1, resource.DecimalSI),
			},
			expectedStatus: framework.NewStatus(framework.Unschedulable, SharedCoresPodFailed),
		},
		{
			name:         "fail, there is dedicated cores pod",
			existingPods: []*v1.Pod{p2, p3},
			cnr:          cnr3,
			resourceRequests: map[string]*resource.Quantity{
				"cpu":                 resource.NewQuantity(1, resource.DecimalSI),
				"memory":              resource.NewQuantity(1*1024*1024*1024, resource.BinarySI),
				"bytedance.com/other": resource.NewQuantity(1, resource.DecimalSI),
			},
			expectedStatus: framework.NewStatus(framework.Unschedulable, SharedCoresPodFailed),
		},
		{
			name:         "pass, there is monopolize pod",
			existingPods: []*v1.Pod{p1, p4},
			cnr:          cnr2,
			resourceRequests: map[string]*resource.Quantity{
				"cpu":                 resource.NewQuantity(1, resource.DecimalSI),
				"memory":              resource.NewQuantity(1*1024*1024*1024, resource.BinarySI),
				"bytedance.com/other": resource.NewQuantity(1, resource.DecimalSI),
			},
			expectedStatus: nil,
		},
		{
			name:         "pass, there is dedicated cores pod",
			existingPods: []*v1.Pod{p2, p4},
			cnr:          cnr3,
			resourceRequests: map[string]*resource.Quantity{
				"cpu":                 resource.NewQuantity(1, resource.DecimalSI),
				"memory":              resource.NewQuantity(1*1024*1024*1024, resource.BinarySI),
				"bytedance.com/other": resource.NewQuantity(1, resource.DecimalSI),
				"nvidia.com/gpu":      resource.NewQuantity(3, resource.DecimalSI),
			},
			expectedStatus: nil,
		},
		{
			name:         "pass, consider gpu allocations",
			existingPods: []*v1.Pod{p2, p6},
			cnr:          cnr5,
			resourceRequests: map[string]*resource.Quantity{
				"cpu":            resource.NewQuantity(1, resource.DecimalSI),
				"memory":         resource.NewQuantity(1*1024*1024*1024, resource.BinarySI),
				"nvidia.com/gpu": resource.NewQuantity(2, resource.DecimalSI),
			},
			expectedStatus: nil,
		},
		{
			name:         "fail, nil cnr, request cpu",
			existingPods: []*v1.Pod{p2, p6},
			resourceRequests: map[string]*resource.Quantity{
				"cpu":                 resource.NewQuantity(1, resource.DecimalSI),
				"memory":              resource.NewQuantity(1*1024*1024*1024, resource.BinarySI),
				"bytedance.com/other": resource.NewQuantity(1, resource.DecimalSI),
			},
			expectedStatus: framework.NewStatus(framework.Unschedulable, SharedCoresPodFailed),
		},
		{
			name:         "pass, nil cnr, not request cpu&memory",
			existingPods: []*v1.Pod{p2, p6},
			resourceRequests: map[string]*resource.Quantity{
				"bytedance.com/other": resource.NewQuantity(1, resource.DecimalSI),
			},
			expectedStatus: nil,
		},
		{
			name:         "pass, although node has no socket resource",
			existingPods: []*v1.Pod{p2, p4},
			node:         node2,
			cnr:          cnr3,
			resourceRequests: map[string]*resource.Quantity{
				"cpu":                 resource.NewQuantity(1, resource.DecimalSI),
				"memory":              resource.NewQuantity(1*1024*1024*1024, resource.BinarySI),
				"bytedance.com/other": resource.NewQuantity(1, resource.DecimalSI),
				"nvidia.com/gpu":      resource.NewQuantity(3, resource.DecimalSI),
			},
			expectedStatus: nil,
		},
		{
			name:         "daemon pod, pass although resource overuse",
			existingPods: []*v1.Pod{p1, p5, p7},
			cnr:          cnr6,
			resourceRequests: map[string]*resource.Quantity{
				"cpu":                 resource.NewQuantity(0, resource.DecimalSI),
				"memory":              resource.NewQuantity(0, resource.BinarySI),
				"bytedance.com/other": resource.NewQuantity(1, resource.DecimalSI),
			},
			expectedStatus: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			nodeInfo := framework.NewNodeInfo()
			if tt.cnr != nil {
				nodeInfo.SetCNR(tt.cnr)
			}
			if tt.node != nil {
				nodeInfo.SetNode(tt.node)
			} else {
				nodeInfo.SetNode(node)
			}
			for _, pod := range tt.existingPods {
				nodeInfo.AddPod(pod)
			}
			gotStatus := fitsNumaTopologyForSharedQoS(nodeInfo, tt.resourceRequests)
			if !reflect.DeepEqual(tt.expectedStatus, gotStatus) {
				t.Errorf("expected status: %v, but got: %v", tt.expectedStatus, gotStatus)
			}
		})
	}
}

func TestCheckNumaTopologyForNonExclusiveDedicatedQoS(t *testing.T) {
	node := &v1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name: "node",
		},
		Status: v1.NodeStatus{
			Allocatable: v1.ResourceList{
				"cpu":            resource.MustParse("94"),
				"memory":         resource.MustParse("384Gi"),
				"nvidia.com/gpu": resource.MustParse("8"),
			},
		},
	}

	cnr1 := &katalystv1alpha1.CustomNodeResource{
		ObjectMeta: metav1.ObjectMeta{
			Name: "node",
		},
		Spec: katalystv1alpha1.CustomNodeResourceSpec{
			NodeResourceProperties: []*katalystv1alpha1.Property{
				{
					PropertyName:     util.ResourceNuma.String(),
					PropertyQuantity: resource.NewQuantity(4, resource.DecimalSI),
				},
			},
		},
		Status: katalystv1alpha1.CustomNodeResourceStatus{
			TopologyZone: []*katalystv1alpha1.TopologyZone{
				{
					Type: katalystv1alpha1.TopologyTypeSocket,
					Name: "0",
					Children: []*katalystv1alpha1.TopologyZone{
						{
							Type: katalystv1alpha1.TopologyTypeNuma,
							Name: "0",
							Resources: katalystv1alpha1.Resources{
								Allocatable: &v1.ResourceList{
									"cpu":            resource.MustParse("23"),
									"memory":         resource.MustParse("96Gi"),
									"nvidia.com/gpu": resource.MustParse("2"),
								},
								Capacity: &v1.ResourceList{
									"cpu":            resource.MustParse("24"),
									"memory":         resource.MustParse("96Gi"),
									"nvidia.com/gpu": resource.MustParse("2"),
								},
							},
							Allocations: []*katalystv1alpha1.Allocation{
								{
									Consumer: "p1/p1/p1",
									Requests: &v1.ResourceList{
										"cpu":            resource.MustParse("24"),
										"memory":         resource.MustParse("96Gi"),
										"nvidia.com/gpu": resource.MustParse("1"),
									},
								},
							},
						},
						{
							Type: katalystv1alpha1.TopologyTypeNuma,
							Name: "1",
							Resources: katalystv1alpha1.Resources{
								Allocatable: &v1.ResourceList{
									"cpu":            resource.MustParse("23"),
									"memory":         resource.MustParse("96Gi"),
									"nvidia.com/gpu": resource.MustParse("2"),
								},
								Capacity: &v1.ResourceList{
									"cpu":            resource.MustParse("24"),
									"memory":         resource.MustParse("96Gi"),
									"nvidia.com/gpu": resource.MustParse("2"),
								},
							},
							Allocations: []*katalystv1alpha1.Allocation{
								{
									Consumer: "p2/p2/p2",
									Requests: &v1.ResourceList{
										"cpu":            resource.MustParse("10"),
										"memory":         resource.MustParse("20Gi"),
										"nvidia.com/gpu": resource.MustParse("1"),
									},
								},
							},
						},
					},
				},
				{
					Type: katalystv1alpha1.TopologyTypeSocket,
					Name: "1",
					Children: []*katalystv1alpha1.TopologyZone{
						{
							Type: katalystv1alpha1.TopologyTypeNuma,
							Name: "2",
							Resources: katalystv1alpha1.Resources{
								Allocatable: &v1.ResourceList{
									"cpu":            resource.MustParse("23"),
									"memory":         resource.MustParse("96Gi"),
									"nvidia.com/gpu": resource.MustParse("2"),
								},
								Capacity: &v1.ResourceList{
									"cpu":            resource.MustParse("24"),
									"memory":         resource.MustParse("96Gi"),
									"nvidia.com/gpu": resource.MustParse("2"),
								},
							},
							Allocations: []*katalystv1alpha1.Allocation{
								{
									Consumer: "p3/p3/p3",
									Requests: &v1.ResourceList{
										"cpu":            resource.MustParse("10"),
										"memory":         resource.MustParse("20Gi"),
										"nvidia.com/gpu": resource.MustParse("1"),
									},
								},
							},
						},
						{
							Type: katalystv1alpha1.TopologyTypeNuma,
							Name: "3",
							Resources: katalystv1alpha1.Resources{
								Allocatable: &v1.ResourceList{
									"cpu":            resource.MustParse("23"),
									"memory":         resource.MustParse("96Gi"),
									"nvidia.com/gpu": resource.MustParse("2"),
								},
								Capacity: &v1.ResourceList{
									"cpu":            resource.MustParse("24"),
									"memory":         resource.MustParse("96Gi"),
									"nvidia.com/gpu": resource.MustParse("2"),
								},
							},
							Allocations: []*katalystv1alpha1.Allocation{},
						},
					},
				},
			},
		},
	}

	cnr2 := &katalystv1alpha1.CustomNodeResource{
		ObjectMeta: metav1.ObjectMeta{
			Name: "node",
		},
		Spec: katalystv1alpha1.CustomNodeResourceSpec{
			NodeResourceProperties: []*katalystv1alpha1.Property{
				{
					PropertyName:     util.ResourceNuma.String(),
					PropertyQuantity: resource.NewQuantity(4, resource.DecimalSI),
				},
			},
		},
		Status: katalystv1alpha1.CustomNodeResourceStatus{
			TopologyZone: []*katalystv1alpha1.TopologyZone{
				{
					Type: katalystv1alpha1.TopologyTypeSocket,
					Name: "0",
					Children: []*katalystv1alpha1.TopologyZone{
						{
							Type: katalystv1alpha1.TopologyTypeNuma,
							Name: "0",
							Resources: katalystv1alpha1.Resources{
								Allocatable: &v1.ResourceList{
									"cpu":            resource.MustParse("23"),
									"memory":         resource.MustParse("96Gi"),
									"nvidia.com/gpu": resource.MustParse("2"),
								},
								Capacity: &v1.ResourceList{
									"cpu":            resource.MustParse("24"),
									"memory":         resource.MustParse("96Gi"),
									"nvidia.com/gpu": resource.MustParse("2"),
								},
							},
							Allocations: []*katalystv1alpha1.Allocation{
								{
									Consumer: "p1/p1/p1",
									Requests: &v1.ResourceList{
										"cpu":            resource.MustParse("24"),
										"memory":         resource.MustParse("96Gi"),
										"nvidia.com/gpu": resource.MustParse("1"),
									},
								},
							},
						},
						{
							Type: katalystv1alpha1.TopologyTypeNuma,
							Name: "1",
							Resources: katalystv1alpha1.Resources{
								Allocatable: &v1.ResourceList{
									"cpu":            resource.MustParse("23"),
									"memory":         resource.MustParse("96Gi"),
									"nvidia.com/gpu": resource.MustParse("2"),
								},
								Capacity: &v1.ResourceList{
									"cpu":            resource.MustParse("24"),
									"memory":         resource.MustParse("96Gi"),
									"nvidia.com/gpu": resource.MustParse("2"),
								},
							},
							Allocations: []*katalystv1alpha1.Allocation{
								{
									Consumer: "p2/p2/p2",
									Requests: &v1.ResourceList{
										"cpu":            resource.MustParse("10"),
										"memory":         resource.MustParse("20Gi"),
										"nvidia.com/gpu": resource.MustParse("1"),
									},
								},
							},
						},
					},
				},
				{
					Type: katalystv1alpha1.TopologyTypeSocket,
					Name: "1",
					Children: []*katalystv1alpha1.TopologyZone{
						{
							Type: katalystv1alpha1.TopologyTypeNuma,
							Name: "2",
							Resources: katalystv1alpha1.Resources{
								Allocatable: &v1.ResourceList{
									"cpu":            resource.MustParse("23"),
									"memory":         resource.MustParse("96Gi"),
									"nvidia.com/gpu": resource.MustParse("2"),
								},
								Capacity: &v1.ResourceList{
									"cpu":            resource.MustParse("24"),
									"memory":         resource.MustParse("96Gi"),
									"nvidia.com/gpu": resource.MustParse("2"),
								},
							},
							Allocations: []*katalystv1alpha1.Allocation{},
						},
						{
							Type: katalystv1alpha1.TopologyTypeNuma,
							Name: "3",
							Resources: katalystv1alpha1.Resources{
								Allocatable: &v1.ResourceList{
									"cpu":            resource.MustParse("23"),
									"memory":         resource.MustParse("96Gi"),
									"nvidia.com/gpu": resource.MustParse("2"),
								},
								Capacity: &v1.ResourceList{
									"cpu":            resource.MustParse("24"),
									"memory":         resource.MustParse("96Gi"),
									"nvidia.com/gpu": resource.MustParse("2"),
								},
							},
							Allocations: []*katalystv1alpha1.Allocation{
								{
									Consumer: "p5/p5/p5",
									Requests: &v1.ResourceList{
										"nvidia.com/gpu": resource.MustParse("1"),
									},
								},
							},
						},
					},
				},
			},
		},
	}

	cnr3 := &katalystv1alpha1.CustomNodeResource{
		ObjectMeta: metav1.ObjectMeta{
			Name: "node",
		},
		Spec: katalystv1alpha1.CustomNodeResourceSpec{
			NodeResourceProperties: []*katalystv1alpha1.Property{
				{
					PropertyName:     util.ResourceNuma.String(),
					PropertyQuantity: resource.NewQuantity(4, resource.DecimalSI),
				},
			},
		},
		Status: katalystv1alpha1.CustomNodeResourceStatus{
			TopologyZone: []*katalystv1alpha1.TopologyZone{
				{
					Type: katalystv1alpha1.TopologyTypeSocket,
					Name: "0",
					Children: []*katalystv1alpha1.TopologyZone{
						{
							Type: katalystv1alpha1.TopologyTypeNuma,
							Name: "0",
							Resources: katalystv1alpha1.Resources{
								Allocatable: &v1.ResourceList{
									"cpu":            resource.MustParse("23"),
									"memory":         resource.MustParse("96Gi"),
									"nvidia.com/gpu": resource.MustParse("2"),
								},
								Capacity: &v1.ResourceList{
									"cpu":            resource.MustParse("24"),
									"memory":         resource.MustParse("96Gi"),
									"nvidia.com/gpu": resource.MustParse("2"),
								},
							},
							Allocations: []*katalystv1alpha1.Allocation{
								{
									Consumer: "p2/p2/p2",
									Requests: &v1.ResourceList{
										"cpu":            resource.MustParse("10"),
										"memory":         resource.MustParse("20Gi"),
										"nvidia.com/gpu": resource.MustParse("1"),
									},
								},
								{
									Consumer: "p8/p8/p8",
									Requests: &v1.ResourceList{
										"nvidia.com/gpu": resource.MustParse("1"),
									},
								},
							},
						},
						{
							Type: katalystv1alpha1.TopologyTypeSocket,
							Name: "1",
							Resources: katalystv1alpha1.Resources{
								Allocatable: &v1.ResourceList{
									"cpu":            resource.MustParse("23"),
									"memory":         resource.MustParse("96Gi"),
									"nvidia.com/gpu": resource.MustParse("2"),
								},
								Capacity: &v1.ResourceList{
									"cpu":            resource.MustParse("24"),
									"memory":         resource.MustParse("96Gi"),
									"nvidia.com/gpu": resource.MustParse("2"),
								},
							},
							Allocations: []*katalystv1alpha1.Allocation{
								{
									Consumer: "p7/p7/p7",
									Requests: &v1.ResourceList{
										"cpu":    resource.MustParse("10"),
										"memory": resource.MustParse("20Gi"),
									},
								},
								{
									Consumer: "p9/p9/p9",
									Requests: &v1.ResourceList{
										"nvidia.com/gpu": resource.MustParse("2"),
									},
								},
							},
						},
					},
				},
				{
					Type: katalystv1alpha1.TopologyTypeSocket,
					Name: "1",
					Children: []*katalystv1alpha1.TopologyZone{
						{
							Type: katalystv1alpha1.TopologyTypeNuma,
							Name: "2",
							Resources: katalystv1alpha1.Resources{
								Allocatable: &v1.ResourceList{
									"cpu":            resource.MustParse("23"),
									"memory":         resource.MustParse("96Gi"),
									"nvidia.com/gpu": resource.MustParse("2"),
								},
								Capacity: &v1.ResourceList{
									"cpu":            resource.MustParse("24"),
									"memory":         resource.MustParse("96Gi"),
									"nvidia.com/gpu": resource.MustParse("2"),
								},
							},
							Allocations: []*katalystv1alpha1.Allocation{},
						},
						{
							Type: katalystv1alpha1.TopologyTypeNuma,
							Name: "3",
							Resources: katalystv1alpha1.Resources{
								Allocatable: &v1.ResourceList{
									"cpu":            resource.MustParse("23"),
									"memory":         resource.MustParse("96Gi"),
									"nvidia.com/gpu": resource.MustParse("2"),
								},
								Capacity: &v1.ResourceList{
									"cpu":            resource.MustParse("24"),
									"memory":         resource.MustParse("96Gi"),
									"nvidia.com/gpu": resource.MustParse("2"),
								},
							},
							Allocations: []*katalystv1alpha1.Allocation{},
						},
					},
				},
			},
		},
	}

	p1 := testing_helper.MakePod().Namespace("p1").Name("p1").UID("p1").
		Req(map[v1.ResourceName]string{"cpu": "10", "memory": "20Gi", "nvidia.com/gpu": "1"}).
		Annotation(util.QoSLevelKey, string(util.DedicatedCores)).
		Annotation(util.MemoyEnhancementKey, "{\"numa_binding\":\"true\",\"numa_exclusive\":\"true\"}").
		Label("type", "t1").Obj()
	p2 := testing_helper.MakePod().Namespace("p2").Name("p2").UID("p2").
		Req(map[v1.ResourceName]string{"cpu": "10", "memory": "20Gi", "nvidia.com/gpu": "1"}).
		Annotation(util.QoSLevelKey, string(util.DedicatedCores)).
		Annotation(util.MemoyEnhancementKey, "{\"numa_binding\":\"true\",\"numa_exclusive\":\"false\"}").
		Label("type", "t1").Obj()
	p3 := testing_helper.MakePod().Namespace("p3").Name("p3").UID("p3").
		Req(map[v1.ResourceName]string{"cpu": "10", "memory": "20Gi", "nvidia.com/gpu": "1"}).
		Annotation(util.QoSLevelKey, string(util.DedicatedCores)).
		Annotation(util.MemoyEnhancementKey, "{\"numa_binding\":\"true\",\"numa_exclusive\":\"false\"}").
		Label("type", "t1").Obj()
	p4 := testing_helper.MakePod().Namespace("p4").Name("p4").UID("p4").
		Req(map[v1.ResourceName]string{"cpu": "10", "memory": "20Gi"}).
		Label("type", "t1").Obj()
	p5 := testing_helper.MakePod().Namespace("p5").Name("p5").UID("p5").
		Req(map[v1.ResourceName]string{"cpu": "10", "memory": "20Gi", "nvidia.com/gpu": "1"}).
		Label("type", "t1").Obj()
	p6 := testing_helper.MakePod().Namespace("p6").Name("p6").UID("p6").
		Req(map[v1.ResourceName]string{"cpu": "13", "memory": "20Gi"}).
		Label("type", "t1").Obj()
	p7 := testing_helper.MakePod().Namespace("p7").Name("p7").UID("p7").
		Req(map[v1.ResourceName]string{"cpu": "10", "memory": "20Gi"}).
		Annotation(util.QoSLevelKey, string(util.DedicatedCores)).
		Annotation(util.MemoyEnhancementKey, "{\"numa_binding\":\"true\",\"numa_exclusive\":\"false\"}").
		Label("type", "t1").Obj()
	p8 := testing_helper.MakePod().Namespace("p8").Name("p8").UID("p8").
		Req(map[v1.ResourceName]string{"cpu": "1", "memory": "1Gi", "nvidia.com/gpu": "1"}).
		Label("type", "t1").Obj()
	p9 := testing_helper.MakePod().Namespace("p9").Name("p9").UID("p9").
		Req(map[v1.ResourceName]string{"cpu": "1", "memory": "1Gi", "nvidia.com/gpu": "2"}).
		Label("type", "t1").Obj()

	tests := []struct {
		name           string
		cnr            *katalystv1alpha1.CustomNodeResource
		podAllocations map[int]*v1.ResourceList
		existingPods   []*v1.Pod
		expectedStatus *framework.Status
	}{
		{
			name: "pass",
			cnr:  cnr1,
			podAllocations: map[int]*v1.ResourceList{
				2: {
					"cpu":    resource.MustParse("13"),
					"memory": resource.MustParse("25Gi"),
				},
			},
			existingPods:   []*v1.Pod{p1, p2, p3, p4, p6},
			expectedStatus: nil,
		},
		{
			name: "pass consider gpu allocations",
			cnr:  cnr3,
			podAllocations: map[int]*v1.ResourceList{
				3: {
					"cpu":    resource.MustParse("1"),
					"memory": resource.MustParse("1Gi"),
				},
			},
			existingPods:   []*v1.Pod{p2, p7, p8, p9},
			expectedStatus: nil,
		},
		{
			name: "failed, insufficient resource for shared cores pods",
			cnr:  cnr1,
			podAllocations: map[int]*v1.ResourceList{
				3: {
					"cpu":    resource.MustParse("1"),
					"memory": resource.MustParse("1Gi"),
				},
			},
			existingPods:   []*v1.Pod{p1, p2, p3, p4},
			expectedStatus: framework.NewStatus(framework.Unschedulable, NonExclusiveDedicatedCoresPodFailed),
		},
		{
			name: "failed, insufficient resource in numa",
			cnr:  cnr1,
			podAllocations: map[int]*v1.ResourceList{
				2: {
					"cpu":    resource.MustParse("14"),
					"memory": resource.MustParse("25Gi"),
				},
			},
			existingPods:   []*v1.Pod{p1, p2, p3, p4, p6},
			expectedStatus: framework.NewStatus(framework.Unschedulable, NonExclusiveDedicatedCoresPodFailed),
		},
		{
			name: "pass, although there are shared cores pods in this numa",
			cnr:  cnr2,
			podAllocations: map[int]*v1.ResourceList{
				3: {
					"cpu":    resource.MustParse("1"),
					"memory": resource.MustParse("1Gi"),
				},
			},
			existingPods:   []*v1.Pod{p1, p2, p5},
			expectedStatus: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			nodeInfo := framework.NewNodeInfo()
			nodeInfo.SetCNR(tt.cnr)
			nodeInfo.SetNode(node)
			for _, p := range tt.existingPods {
				nodeInfo.AddPod(p)
			}
			podLister := testing_helper.NewFakePodLister(tt.existingPods)
			gotStatus := checkNumaTopologyForNonExclusiveDedicatedQoS(nodeInfo, podLister, tt.podAllocations)
			if !reflect.DeepEqual(tt.expectedStatus, gotStatus) {
				t.Errorf("expected status: %v, but got: %v", tt.expectedStatus, gotStatus)
			}
		})
	}
}

func TestCheckNumaTopologyForExclusiveDedicatedQoS(t *testing.T) {
	node := &v1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name: "node",
		},
		Status: v1.NodeStatus{
			Allocatable: v1.ResourceList{
				"cpu":            resource.MustParse("94"),
				"memory":         resource.MustParse("252Gi"),
				"nvidia.com/gpu": resource.MustParse("8"),
			},
			Capacity: v1.ResourceList{
				"cpu":            resource.MustParse("94"),
				"memory":         resource.MustParse("252Gi"),
				"nvidia.com/gpu": resource.MustParse("8"),
			},
		},
	}

	setCNR := func(a1, a2, a3, a4 *katalystv1alpha1.Allocation) *katalystv1alpha1.CustomNodeResource {
		return &katalystv1alpha1.CustomNodeResource{
			ObjectMeta: metav1.ObjectMeta{
				Name: "node",
			},
			Spec: katalystv1alpha1.CustomNodeResourceSpec{
				NodeResourceProperties: []*katalystv1alpha1.Property{
					{
						PropertyName:     util.ResourceNuma.String(),
						PropertyQuantity: resource.NewQuantity(4, resource.DecimalSI),
					},
				},
			},
			Status: katalystv1alpha1.CustomNodeResourceStatus{
				TopologyZone: []*katalystv1alpha1.TopologyZone{
					{
						Type: katalystv1alpha1.TopologyTypeSocket,
						Name: "0",
						Children: []*katalystv1alpha1.TopologyZone{
							{
								Type: katalystv1alpha1.TopologyTypeNuma,
								Name: "0",
								Resources: katalystv1alpha1.Resources{
									Allocatable: &v1.ResourceList{
										"cpu":                resource.MustParse("24"),
										"memory":             resource.MustParse("63Gi"),
										"nvidia.com/gpu":     resource.MustParse("2"),
										"bytedance.com/rdma": resource.MustParse("2"),
									},
									Capacity: &v1.ResourceList{
										"cpu":                resource.MustParse("24"),
										"memory":             resource.MustParse("64Gi"),
										"nvidia.com/gpu":     resource.MustParse("2"),
										"bytedance.com/rdma": resource.MustParse("2"),
									},
								},
								Allocations: []*katalystv1alpha1.Allocation{a1},
							},
							{
								Type: katalystv1alpha1.TopologyTypeNuma,
								Name: "1",
								Resources: katalystv1alpha1.Resources{
									Allocatable: &v1.ResourceList{
										"cpu":                resource.MustParse("24"),
										"memory":             resource.MustParse("63Gi"),
										"nvidia.com/gpu":     resource.MustParse("2"),
										"bytedance.com/rdma": resource.MustParse("2"),
									},
									Capacity: &v1.ResourceList{
										"cpu":                resource.MustParse("24"),
										"memory":             resource.MustParse("64Gi"),
										"nvidia.com/gpu":     resource.MustParse("2"),
										"bytedance.com/rdma": resource.MustParse("2"),
									},
								},
								Allocations: []*katalystv1alpha1.Allocation{a2},
							},
						},
					},
					{
						Type: katalystv1alpha1.TopologyTypeSocket,
						Name: "1",
						Children: []*katalystv1alpha1.TopologyZone{
							{
								Type: katalystv1alpha1.TopologyTypeNuma,
								Name: "2",
								Resources: katalystv1alpha1.Resources{
									Allocatable: &v1.ResourceList{
										"cpu":                resource.MustParse("23"),
										"memory":             resource.MustParse("63Gi"),
										"nvidia.com/gpu":     resource.MustParse("2"),
										"bytedance.com/rdma": resource.MustParse("2"),
									},
									Capacity: &v1.ResourceList{
										"cpu":                resource.MustParse("24"),
										"memory":             resource.MustParse("64Gi"),
										"nvidia.com/gpu":     resource.MustParse("2"),
										"bytedance.com/rdma": resource.MustParse("2"),
									},
								},
								Allocations: []*katalystv1alpha1.Allocation{a3},
							},
							{
								Type: katalystv1alpha1.TopologyTypeNuma,
								Name: "3",
								Resources: katalystv1alpha1.Resources{
									Allocatable: &v1.ResourceList{
										"cpu":                resource.MustParse("23"),
										"memory":             resource.MustParse("63Gi"),
										"nvidia.com/gpu":     resource.MustParse("2"),
										"bytedance.com/rdma": resource.MustParse("2"),
									},
									Capacity: &v1.ResourceList{
										"cpu":                resource.MustParse("24"),
										"memory":             resource.MustParse("64Gi"),
										"nvidia.com/gpu":     resource.MustParse("2"),
										"bytedance.com/rdma": resource.MustParse("2"),
									},
								},
								Allocations: []*katalystv1alpha1.Allocation{a4},
							},
						},
					},
				},
			},
		}
	}

	tests := []struct {
		name           string
		existingPods   []*v1.Pod
		allocations    []*katalystv1alpha1.Allocation
		podAllocations map[int]*v1.ResourceList
		expectedStatus *framework.Status
	}{
		{
			name: "failed, numa of cpu has other cpu users",
			allocations: []*katalystv1alpha1.Allocation{
				{
					Consumer: "a/a/a",
					Requests: &v1.ResourceList{
						"cpu": resource.MustParse("1"),
					},
				},
				{},
				{},
				{},
			},
			podAllocations: map[int]*v1.ResourceList{
				0: {
					"cpu": resource.MustParse("1"),
				},
			},
			expectedStatus: framework.NewStatus(framework.Unschedulable, ExclusiveDedicatedCoresPodFailed),
		},
		{
			name: "failed, numa of memory has other memory users",
			allocations: []*katalystv1alpha1.Allocation{
				{
					Consumer: "a/a/a",
					Requests: &v1.ResourceList{
						"memory": resource.MustParse("1Gi"),
					},
				},
				{},
				{},
				{},
			},
			podAllocations: map[int]*v1.ResourceList{
				0: {
					"memory": resource.MustParse("1Gi"),
				},
			},
			expectedStatus: framework.NewStatus(framework.Unschedulable, ExclusiveDedicatedCoresPodFailed),
		},
		{
			name: "failed, cpu available is not efficient",
			allocations: []*katalystv1alpha1.Allocation{
				{}, {}, {}, {},
			},
			podAllocations: map[int]*v1.ResourceList{
				2: {
					"cpu": resource.MustParse("24"),
				},
			},
			expectedStatus: framework.NewStatus(framework.Unschedulable, ExclusiveDedicatedCoresPodFailed),
		},
		{
			name: "failed, memory available is not efficient",
			allocations: []*katalystv1alpha1.Allocation{
				{}, {}, {}, {},
			},
			podAllocations: map[int]*v1.ResourceList{
				2: {
					"memory": resource.MustParse("64Gi"),
				},
			},
			expectedStatus: framework.NewStatus(framework.Unschedulable, ExclusiveDedicatedCoresPodFailed),
		},
		{
			name: "failed, no efficient cpu for shared cores pods",
			allocations: []*katalystv1alpha1.Allocation{
				{
					Consumer: "a/a/a",
					Requests: &v1.ResourceList{
						"cpu": resource.MustParse("1"),
					},
				},
				{
					Consumer: "b/b/b",
					Requests: &v1.ResourceList{
						"cpu": resource.MustParse("1"),
					},
				},
				{},
				{
					Consumer: "c/c/c",
					Requests: &v1.ResourceList{
						"cpu": resource.MustParse("1"),
					},
				},
			},
			podAllocations: map[int]*v1.ResourceList{
				2: {
					"cpu": resource.MustParse("1"),
				},
			},
			existingPods: []*v1.Pod{
				testing_helper.MakePod().Namespace("p1").Name("p1").UID("p1").
					Req(map[v1.ResourceName]string{"cpu": "1"}).Obj(),
			},
			expectedStatus: framework.NewStatus(framework.Unschedulable, ExclusiveDedicatedCoresPodFailed),
		},
		{
			name: "failed, no efficient memory for shared cores pods",
			allocations: []*katalystv1alpha1.Allocation{
				{
					Consumer: "a/a/a",
					Requests: &v1.ResourceList{
						"memory": resource.MustParse("1Gi"),
					},
				},
				{
					Consumer: "b/b/b",
					Requests: &v1.ResourceList{
						"memory": resource.MustParse("1Gi"),
					},
				},
				{},
				{
					Consumer: "c/c/c",
					Requests: &v1.ResourceList{
						"memory": resource.MustParse("1Gi"),
					},
				},
			},
			podAllocations: map[int]*v1.ResourceList{
				2: {
					"memory": resource.MustParse("1Gi"),
				},
			},
			existingPods: []*v1.Pod{
				testing_helper.MakePod().Namespace("p1").Name("p1").UID("p1").
					Req(map[v1.ResourceName]string{"memory": "1Gi"}).Obj(),
			},
			expectedStatus: framework.NewStatus(framework.Unschedulable, ExclusiveDedicatedCoresPodFailed),
		},
		{
			name: "failed, gpu available is not efficient",
			allocations: []*katalystv1alpha1.Allocation{
				{
					Consumer: "a/a/a",
					Requests: &v1.ResourceList{
						"nvidia.com/gpu": resource.MustParse("1"),
					},
				},
				{},
				{},
				{},
			},
			podAllocations: map[int]*v1.ResourceList{
				0: {
					"nvidia.com/gpu": resource.MustParse("2"),
				},
			},
			expectedStatus: framework.NewStatus(framework.Unschedulable, ExclusiveDedicatedCoresPodFailed),
		},
		{
			name: "pass",
			allocations: []*katalystv1alpha1.Allocation{
				{
					Consumer: "a/a/a",
					Requests: &v1.ResourceList{
						"cpu": resource.MustParse("1"),
					},
				},
				{
					Consumer: "b/b/b",
					Requests: &v1.ResourceList{
						"cpu": resource.MustParse("1"),
					},
				},
				{
					Consumer: "c/c/c",
					Requests: &v1.ResourceList{
						"cpu": resource.MustParse("1"),
					},
				},
				{},
			},
			podAllocations: map[int]*v1.ResourceList{
				3: {
					"cpu": resource.MustParse("1"),
				},
			},
			expectedStatus: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			nodeInfo := framework.NewNodeInfo()
			nodeInfo.SetCNR(setCNR(tt.allocations[0], tt.allocations[1], tt.allocations[2], tt.allocations[3]))
			nodeInfo.SetNode(node)
			for _, p := range tt.existingPods {
				nodeInfo.AddPod(p)
			}
			gotStatus := checkNumaTopologyForExclusiveDedicatedQoS(nodeInfo, tt.podAllocations)
			if !reflect.DeepEqual(tt.expectedStatus, gotStatus) {
				t.Errorf("expected status: %v, but got: %v", tt.expectedStatus, gotStatus)
			}
		})
	}
}

func TestCheckNumaTopologyForSharedQoS(t *testing.T) {
	node := &v1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name: "node",
		},
		Status: v1.NodeStatus{
			Allocatable: v1.ResourceList{
				"cpu":            resource.MustParse("46"),
				"memory":         resource.MustParse("188Gi"),
				"nvidia.com/gpu": resource.MustParse("4"),
			},
			Capacity: v1.ResourceList{
				"cpu":            resource.MustParse("48"),
				"memory":         resource.MustParse("192Gi"),
				"nvidia.com/gpu": resource.MustParse("4"),
			},
		},
	}

	cnr1 := &katalystv1alpha1.CustomNodeResource{
		ObjectMeta: metav1.ObjectMeta{
			Name: "node",
		},
		Spec: katalystv1alpha1.CustomNodeResourceSpec{
			NodeResourceProperties: []*katalystv1alpha1.Property{
				{
					PropertyName:     util.ResourceNuma.String(),
					PropertyQuantity: resource.NewQuantity(2, resource.DecimalSI),
				},
			},
		},
		Status: katalystv1alpha1.CustomNodeResourceStatus{
			TopologyZone: []*katalystv1alpha1.TopologyZone{
				{
					Type: katalystv1alpha1.TopologyTypeSocket,
					Name: "0",
					Children: []*katalystv1alpha1.TopologyZone{
						{
							Type: katalystv1alpha1.TopologyTypeNuma,
							Name: "0",
							Resources: katalystv1alpha1.Resources{
								Allocatable: &v1.ResourceList{
									"cpu":            resource.MustParse("23"),
									"memory":         resource.MustParse("96Gi"),
									"nvidia.com/gpu": resource.MustParse("2"),
								},
								Capacity: &v1.ResourceList{
									"cpu":            resource.MustParse("24"),
									"memory":         resource.MustParse("96Gi"),
									"nvidia.com/gpu": resource.MustParse("2"),
								},
							},
							Allocations: []*katalystv1alpha1.Allocation{
								{
									Consumer: "p1/p1/p1",
									Requests: &v1.ResourceList{
										"cpu":            resource.MustParse("10"),
										"memory":         resource.MustParse("20Gi"),
										"nvidia.com/gpu": resource.MustParse("1"),
									},
								},
							},
						},
						{
							Type: katalystv1alpha1.TopologyTypeNuma,
							Name: "1",
							Resources: katalystv1alpha1.Resources{
								Allocatable: &v1.ResourceList{
									"cpu":            resource.MustParse("23"),
									"memory":         resource.MustParse("96Gi"),
									"nvidia.com/gpu": resource.MustParse("2"),
								},
								Capacity: &v1.ResourceList{
									"cpu":            resource.MustParse("24"),
									"memory":         resource.MustParse("96Gi"),
									"nvidia.com/gpu": resource.MustParse("2"),
								},
							},
							Allocations: []*katalystv1alpha1.Allocation{
								{
									Consumer: "p2/p2/p2",
									Requests: &v1.ResourceList{
										"nvidia.com/gpu": resource.MustParse("1"),
									},
								},
							},
						},
					},
				},
			},
		},
	}

	cnr2 := &katalystv1alpha1.CustomNodeResource{
		ObjectMeta: metav1.ObjectMeta{
			Name: "node",
		},
		Spec: katalystv1alpha1.CustomNodeResourceSpec{
			NodeResourceProperties: []*katalystv1alpha1.Property{
				{
					PropertyName:     util.ResourceNuma.String(),
					PropertyQuantity: resource.NewQuantity(2, resource.DecimalSI),
				},
			},
		},
		Status: katalystv1alpha1.CustomNodeResourceStatus{
			TopologyZone: []*katalystv1alpha1.TopologyZone{
				{
					Type: katalystv1alpha1.TopologyTypeSocket,
					Name: "0",
					Children: []*katalystv1alpha1.TopologyZone{
						{
							Type: katalystv1alpha1.TopologyTypeNuma,
							Name: "0",
							Resources: katalystv1alpha1.Resources{
								Allocatable: &v1.ResourceList{
									"cpu":            resource.MustParse("23"),
									"memory":         resource.MustParse("96Gi"),
									"nvidia.com/gpu": resource.MustParse("2"),
								},
								Capacity: &v1.ResourceList{
									"cpu":            resource.MustParse("24"),
									"memory":         resource.MustParse("96Gi"),
									"nvidia.com/gpu": resource.MustParse("2"),
								},
							},
							Allocations: []*katalystv1alpha1.Allocation{
								{
									Consumer: "p1/p1/p1",
									Requests: &v1.ResourceList{
										"cpu":            resource.MustParse("10"),
										"memory":         resource.MustParse("20Gi"),
										"nvidia.com/gpu": resource.MustParse("1"),
									},
								},
								{
									Consumer: "p2/p2/p2",
									Requests: &v1.ResourceList{
										"nvidia.com/gpu": resource.MustParse("1"),
									},
								},
							},
						},
						{
							Type: katalystv1alpha1.TopologyTypeNuma,
							Name: "1",
							Resources: katalystv1alpha1.Resources{
								Allocatable: &v1.ResourceList{
									"cpu":            resource.MustParse("23"),
									"memory":         resource.MustParse("96Gi"),
									"nvidia.com/gpu": resource.MustParse("2"),
								},
								Capacity: &v1.ResourceList{
									"cpu":            resource.MustParse("24"),
									"memory":         resource.MustParse("96Gi"),
									"nvidia.com/gpu": resource.MustParse("2"),
								},
							},
							Allocations: []*katalystv1alpha1.Allocation{},
						},
					},
				},
			},
		},
	}

	p1 := testing_helper.MakePod().Namespace("p1").Name("p1").UID("p1").
		Req(map[v1.ResourceName]string{"cpu": "10", "memory": "20Gi", "nvidia.com/gpu": "1"}).
		Annotation(util.QoSLevelKey, string(util.DedicatedCores)).
		Annotation(util.MemoyEnhancementKey, "{\"numa_binding\":\"true\",\"numa_exclusive\":\"false\"}").
		Label("type", "t1").Obj()
	p2 := testing_helper.MakePod().Namespace("p2").Name("p2").UID("p2").
		Req(map[v1.ResourceName]string{"cpu": "10", "memory": "20Gi", "nvidia.com/gpu": "1"}).
		Label("type", "t1").Obj()

	tests := []struct {
		name             string
		cnr              *katalystv1alpha1.CustomNodeResource
		existingPods     []*v1.Pod
		resourceRequests map[string]*resource.Quantity
		podAllocations   map[int]*v1.ResourceList
		expectedStatus   *framework.Status
	}{
		{
			name:         "pass",
			cnr:          cnr1,
			existingPods: []*v1.Pod{p1, p2},
			resourceRequests: map[string]*resource.Quantity{
				"cpu":            resource.NewQuantity(13, resource.DecimalSI),
				"memory":         resource.NewQuantity(76*1024*1024*1024, resource.BinarySI),
				"nvidia.com/gpu": resource.NewQuantity(1, resource.DecimalSI),
			},
			podAllocations: map[int]*v1.ResourceList{
				1: {
					"nvidia.com/gpu": resource.MustParse("1"),
				},
			},
			expectedStatus: nil,
		},
		{
			name:         "pass consider gpu allocations",
			cnr:          cnr2,
			existingPods: []*v1.Pod{p1, p2},
			resourceRequests: map[string]*resource.Quantity{
				"cpu":            resource.NewQuantity(1, resource.DecimalSI),
				"memory":         resource.NewQuantity(1*1024*1024*1024, resource.BinarySI),
				"nvidia.com/gpu": resource.NewQuantity(2, resource.DecimalSI),
			},
			podAllocations: map[int]*v1.ResourceList{
				1: {
					"nvidia.com/gpu": resource.MustParse("2"),
				},
			},
			expectedStatus: nil,
		},
		{
			name:         "failed, insufficient cpu resource in numa",
			cnr:          cnr1,
			existingPods: []*v1.Pod{p1, p2},
			resourceRequests: map[string]*resource.Quantity{
				"cpu":            resource.NewQuantity(14, resource.DecimalSI),
				"memory":         resource.NewQuantity(76*1024*1024*1024, resource.BinarySI),
				"nvidia.com/gpu": resource.NewQuantity(1, resource.DecimalSI),
			},
			podAllocations: map[int]*v1.ResourceList{
				1: {
					"nvidia.com/gpu": resource.MustParse("1"),
				},
			},
			expectedStatus: framework.NewStatus(framework.Unschedulable, SharedCoresPodFailed),
		},
		{
			name:         "failed, insufficient gpu resource in numa",
			cnr:          cnr1,
			existingPods: []*v1.Pod{p1, p2},
			resourceRequests: map[string]*resource.Quantity{
				"cpu":            resource.NewQuantity(13, resource.DecimalSI),
				"memory":         resource.NewQuantity(76*1024*1024*1024, resource.BinarySI),
				"nvidia.com/gpu": resource.NewQuantity(2, resource.DecimalSI),
			},
			podAllocations: map[int]*v1.ResourceList{
				1: {
					"nvidia.com/gpu": resource.MustParse("2"),
				},
			},
			expectedStatus: framework.NewStatus(framework.Unschedulable, SharedCoresPodFailed),
		},
		{
			name:         "pass, although there is dedicated cores pod in numa",
			cnr:          cnr1,
			existingPods: []*v1.Pod{p1, p2},
			resourceRequests: map[string]*resource.Quantity{
				"cpu":            resource.NewQuantity(13, resource.DecimalSI),
				"memory":         resource.NewQuantity(76*1024*1024*1024, resource.BinarySI),
				"nvidia.com/gpu": resource.NewQuantity(1, resource.DecimalSI),
			},
			podAllocations: map[int]*v1.ResourceList{
				0: {
					"nvidia.com/gpu": resource.MustParse("1"),
				},
			},
			expectedStatus: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			nodeInfo := framework.NewNodeInfo()
			nodeInfo.SetCNR(tt.cnr)
			nodeInfo.SetNode(node)
			for _, p := range tt.existingPods {
				nodeInfo.AddPod(p)
			}
			gotStatus := checkNumaTopologyForSharedQoS(nodeInfo, tt.resourceRequests, tt.podAllocations)
			if !reflect.DeepEqual(tt.expectedStatus, gotStatus) {
				t.Errorf("expected status: %v, but got: %v", tt.expectedStatus, gotStatus)
			}
		})
	}
}

func TestSufficientResource(t *testing.T) {
	tests := []struct {
		name        string
		request     v1.ResourceList
		available   v1.ResourceList
		expectedRes bool
	}{
		{
			name: "pass",
			request: v1.ResourceList{
				"cpu":    resource.MustParse("10"),
				"memory": resource.MustParse("10Gi"),
			},
			available: v1.ResourceList{
				"cpu":            resource.MustParse("10"),
				"memory":         resource.MustParse("10Gi"),
				"nvidia.com/gpu": resource.MustParse("2"),
			},
			expectedRes: true,
		},
		{
			name: "not pass",
			request: v1.ResourceList{
				"cpu":    resource.MustParse("10"),
				"memory": resource.MustParse("10Gi"),
			},
			available: v1.ResourceList{
				"cpu":            resource.MustParse("1999m"),
				"memory":         resource.MustParse("10Gi"),
				"nvidia.com/gpu": resource.MustParse("2"),
			},
			expectedRes: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotRes := sufficientResource(tt.request, tt.available)
			if tt.expectedRes != gotRes {
				t.Errorf("expected result: %v, but got: %v", tt.expectedRes, gotRes)
			}
		})
	}
}

func TestComb(t *testing.T) {
	gotNumaLists := comb(7, 3, []int{0, 1, 2, 3, 4, 6, 7})
	expectedNumaLists := [][]int{
		{0, 1, 2},
		{0, 1, 3},
		{0, 1, 4},
		{0, 1, 6},
		{0, 1, 7},
		{0, 2, 3},
		{0, 2, 4},
		{0, 2, 6},
		{0, 2, 7},
		{0, 3, 4},
		{0, 3, 6},
		{0, 3, 7},
		{0, 4, 6},
		{0, 4, 7},
		{0, 6, 7},
		{1, 2, 3},
		{1, 2, 4},
		{1, 2, 6},
		{1, 2, 7},
		{1, 3, 4},
		{1, 3, 6},
		{1, 3, 7},
		{1, 4, 6},
		{1, 4, 7},
		{1, 6, 7},
		{2, 3, 4},
		{2, 3, 6},
		{2, 3, 7},
		{2, 4, 6},
		{2, 4, 7},
		{2, 6, 7},
		{3, 4, 6},
		{3, 4, 7},
		{3, 6, 7},
		{4, 6, 7},
	}
	if len(expectedNumaLists) != len(gotNumaLists) {
		t.Errorf("expected count: %d, but got: %d", len(expectedNumaLists), len(gotNumaLists))
	}
	for _, expectedNumaList := range expectedNumaLists {
		pass := false
		expectedNumaListSet := sets.NewInt(expectedNumaList...)
		for _, gotNumaList := range gotNumaLists {
			gotNumaListSet := sets.NewInt(gotNumaList...)
			if expectedNumaListSet.Equal(gotNumaListSet) {
				pass = true
				break
			}
		}
		if !pass {
			t.Errorf("expected numa list %v but noy got it", expectedNumaListSet)
		}
	}
}

func TestGetNumasLists(t *testing.T) {
	existingNumas := []int{0, 4}
	allNumas := []int{0, 1, 2, 3, 4, 5, 6, 7}
	count := 2
	gotNumaLists := getNumasLists(existingNumas, allNumas, int64(count))
	expectedNumaLists := [][]int{
		{2, 1},
		{3, 1},
		{5, 1},
		{6, 1},
		{7, 1},
		{3, 2},
		{5, 2},
		{6, 2},
		{7, 2},
		{5, 3},
		{6, 3},
		{7, 3},
		{6, 5},
		{7, 5},
		{7, 6},
	}
	if !reflect.DeepEqual(expectedNumaLists, gotNumaLists) {
		t.Errorf("expected: %v, but got: %v", expectedNumaLists, gotNumaLists)
	}
}
