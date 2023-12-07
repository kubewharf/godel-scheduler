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
	"context"
	"reflect"
	"testing"

	katalystv1alpha1 "github.com/kubewharf/katalyst-api/pkg/apis/node/v1alpha1"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	utilfeature "k8s.io/apiserver/pkg/util/feature"
	"k8s.io/component-base/featuregate"

	"github.com/kubewharf/godel-scheduler/pkg/features"
	godelfeatures "github.com/kubewharf/godel-scheduler/pkg/features"
	framework "github.com/kubewharf/godel-scheduler/pkg/framework/api"
	"github.com/kubewharf/godel-scheduler/pkg/plugins/nonnativeresource"
	testing_helper "github.com/kubewharf/godel-scheduler/pkg/testing-helper"
	"github.com/kubewharf/godel-scheduler/pkg/util"
	podutil "github.com/kubewharf/godel-scheduler/pkg/util/pod"
)

func TestFitsNonNativeTopology(t *testing.T) {
	utilfeature.DefaultMutableFeatureGate.SetFromMap(map[string]bool{string(godelfeatures.NonNativeResourceSchedulingSupport): true})

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
			Capacity: v1.ResourceList{
				"cpu":            resource.MustParse("96"),
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
									Consumer: "p3/p3/p3",
									Requests: &v1.ResourceList{
										"cpu":            resource.MustParse("12"),
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
									"cpu":            resource.MustParse("22"),
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
									Consumer: "p6/p6/p6",
									Requests: &v1.ResourceList{
										"cpu":            resource.MustParse("22"),
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
									Consumer: "p7/p7/p7",
									Requests: &v1.ResourceList{
										"cpu":            resource.MustParse("24"),
										"memory":         resource.MustParse("96Gi"),
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
									Consumer: "p8/p8/p8",
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
							Name: "3",
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
									"cpu":            resource.MustParse("22"),
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
									Consumer: "p6/p6/p6",
									Requests: &v1.ResourceList{
										"cpu":            resource.MustParse("22"),
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
									Consumer: "p7/p7/p7",
									Requests: &v1.ResourceList{
										"cpu":            resource.MustParse("24"),
										"memory":         resource.MustParse("96Gi"),
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
							Allocations: []*katalystv1alpha1.Allocation{},
						},
						{
							Type: katalystv1alpha1.TopologyTypeNuma,
							Name: "3",
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
							Allocations: []*katalystv1alpha1.Allocation{},
						},
					},
				},
			},
		},
	}

	cnr4 := &katalystv1alpha1.CustomNodeResource{
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
									"cpu":            resource.MustParse("22"),
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
									Consumer: "p6/p6/p6",
									Requests: &v1.ResourceList{
										"cpu":            resource.MustParse("22"),
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
									Consumer: "p7/p7/p7",
									Requests: &v1.ResourceList{
										"cpu":            resource.MustParse("24"),
										"memory":         resource.MustParse("96Gi"),
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
										"cpu":    resource.MustParse("1"),
										"memory": resource.MustParse("1Gi"),
									},
								},
							},
						},
						{
							Type: katalystv1alpha1.TopologyTypeNuma,
							Name: "3",
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
		Req(map[v1.ResourceName]string{"cpu": "12", "memory": "20Gi", "nvidia.com/gpu": "1"}).
		Annotation(util.QoSLevelKey, string(util.DedicatedCores)).
		Annotation(util.MemoyEnhancementKey, "{\"numa_binding\":\"true\",\"numa_exclusive\":\"false\"}").
		Label("type", "t1").Obj()
	p4 := testing_helper.MakePod().Namespace("p4").Name("p4").UID("p4").
		Req(map[v1.ResourceName]string{"cpu": "24", "memory": "20Gi"}).
		Label("type", "t1").Obj()
	p5 := testing_helper.MakePod().Namespace("p5").Name("p5").UID("p5").
		Req(map[v1.ResourceName]string{"cpu": "1", "memory": "1Gi"}).
		Label("type", "t1").Obj()
	p6 := testing_helper.MakePod().Namespace("p6").Name("p6").UID("p6").
		Req(map[v1.ResourceName]string{"cpu": "22", "memory": "80Gi", "nvidia.com/gpu": "1"}).
		Annotation(util.QoSLevelKey, string(util.DedicatedCores)).
		Annotation(util.MemoyEnhancementKey, "{\"numa_binding\":\"true\",\"numa_exclusive\":\"true\"}").
		Label("type", "t1").Obj()
	p7 := testing_helper.MakePod().Namespace("p7").Name("p7").UID("p7").
		Req(map[v1.ResourceName]string{"cpu": "24", "memory": "80Gi", "nvidia.com/gpu": "1"}).
		Annotation(util.QoSLevelKey, string(util.DedicatedCores)).
		Annotation(util.MemoyEnhancementKey, "{\"numa_binding\":\"true\",\"numa_exclusive\":\"true\"}").
		Label("type", "t1").Obj()
	p8 := testing_helper.MakePod().Namespace("p8").Name("p8").UID("p8").
		Req(map[v1.ResourceName]string{"cpu": "24", "memory": "80Gi", "nvidia.com/gpu": "1"}).
		Annotation(util.QoSLevelKey, string(util.DedicatedCores)).
		Annotation(util.MemoyEnhancementKey, "{\"numa_binding\":\"true\",\"numa_exclusive\":\"true\"}").
		Label("type", "t1").Obj()
	p9 := testing_helper.MakePod().Namespace("p9").Name("p9").UID("p9").
		Req(map[v1.ResourceName]string{"cpu": "1", "memory": "1Gi"}).
		Annotation(util.QoSLevelKey, string(util.DedicatedCores)).
		Annotation(util.MemoyEnhancementKey, "{\"numa_binding\":\"true\",\"numa_exclusive\":\"false\"}").
		Label("type", "t1").Obj()

	tests := []struct {
		name             string
		pod              *v1.Pod
		existingPods     []*v1.Pod
		cnr              *katalystv1alpha1.CustomNodeResource
		enableColocation bool
		expectedStatus   *framework.Status
	}{
		{
			name: "[colocation] pass, exclusive dedicated cores, align with gpu",
			pod: testing_helper.MakePod().Namespace("foo").Name("foo").UID("foo").
				Req(map[v1.ResourceName]string{"cpu": "20", "memory": "90Gi", "nvidia.com/gpu": "4"}).
				Annotation(util.QoSLevelKey, string(util.DedicatedCores)).
				Annotation(util.MemoyEnhancementKey, "{\"numa_binding\":\"true\",\"numa_exclusive\":\"true\"}").Obj(),
			existingPods:     []*v1.Pod{p6, p7},
			cnr:              cnr3,
			enableColocation: true,
			expectedStatus:   nil,
		},
		{
			name: "[colocation] pass, exclusive dedicated cores, not align with gpu",
			pod: testing_helper.MakePod().Namespace("foo").Name("foo").UID("foo").
				Req(map[v1.ResourceName]string{"cpu": "20", "memory": "20Gi", "nvidia.com/gpu": "4"}).
				Annotation(podutil.AlignedResources, "").
				Annotation(util.QoSLevelKey, string(util.DedicatedCores)).
				Annotation(util.MemoyEnhancementKey, "{\"numa_binding\":\"true\",\"numa_exclusive\":\"true\"}").Obj(),
			existingPods:     []*v1.Pod{p6, p7, p9},
			cnr:              cnr4,
			enableColocation: true,
			expectedStatus:   nil,
		},
		{
			name: "[colocation] pass, non-exclusive dedicated cores",
			pod: testing_helper.MakePod().Namespace("foo").Name("foo").UID("foo").
				Req(map[v1.ResourceName]string{"cpu": "24", "memory": "96Gi", "nvidia.com/gpu": "2"}).
				Annotation(util.QoSLevelKey, string(util.DedicatedCores)).
				Annotation(util.MemoyEnhancementKey, "{\"numa_binding\":\"true\",\"numa_exclusive\":\"false\"}").Obj(),
			existingPods:     []*v1.Pod{p1, p2, p3},
			cnr:              cnr1,
			enableColocation: true,
			expectedStatus:   nil,
		},
		{
			name: "[colocation] pass, shared cores",
			pod: testing_helper.MakePod().Namespace("foo").Name("foo").UID("foo").
				Req(map[v1.ResourceName]string{"cpu": "24", "memory": "96Gi", "nvidia.com/gpu": "1"}).Obj(),
			existingPods:     []*v1.Pod{p1, p2, p3},
			cnr:              cnr1,
			enableColocation: true,
			expectedStatus:   nil,
		},
		{
			name: "[colocation] failed, exclusive dedicated cores, align with gpu",
			pod: testing_helper.MakePod().Namespace("foo").Name("foo").UID("foo").
				Req(map[v1.ResourceName]string{"cpu": "20", "memory": "20Gi", "nvidia.com/gpu": "4"}).
				Annotation(util.QoSLevelKey, string(util.DedicatedCores)).
				Annotation(util.MemoyEnhancementKey, "{\"numa_binding\":\"true\",\"numa_exclusive\":\"true\"}").Obj(),
			existingPods:     []*v1.Pod{p6, p7, p9},
			cnr:              cnr4,
			enableColocation: true,
			expectedStatus:   framework.NewStatus(framework.Unschedulable, nonnativeresource.ExclusiveDedicatedCoresPodFailed),
		},
		{
			name: "[colocation] failed, exclusive dedicated cores, not align with gpu, no efficient resources for shared cores",
			pod: testing_helper.MakePod().Namespace("foo").Name("foo").UID("foo").
				Req(map[v1.ResourceName]string{"cpu": "20", "memory": "20Gi", "nvidia.com/gpu": "4"}).
				Annotation(podutil.AlignedResources, "").
				Annotation(util.QoSLevelKey, string(util.DedicatedCores)).
				Annotation(util.MemoyEnhancementKey, "{\"numa_binding\":\"true\",\"numa_exclusive\":\"true\"}").Obj(),
			existingPods:     []*v1.Pod{p6, p7, p9, p5},
			cnr:              cnr4,
			enableColocation: true,
			expectedStatus:   framework.NewStatus(framework.Unschedulable, nonnativeresource.ExclusiveDedicatedCoresPodFailed),
		},
		{
			name: "[colocation] failed, non-exclusive dedicated cores",
			pod: testing_helper.MakePod().Namespace("foo").Name("foo").UID("foo").
				Req(map[v1.ResourceName]string{"cpu": "24", "memory": "96Gi", "nvidia.com/gpu": "2"}).
				Annotation(util.QoSLevelKey, string(util.DedicatedCores)).
				Annotation(util.MemoyEnhancementKey, "{\"numa_binding\":\"true\",\"numa_exclusive\":\"false\"}").Obj(),
			existingPods:     []*v1.Pod{p1, p2, p3, p5},
			cnr:              cnr1,
			enableColocation: true,
			expectedStatus:   framework.NewStatus(framework.Unschedulable, nonnativeresource.NonExclusiveDedicatedCoresPodFailed),
		},
		{
			name: "[colocation] failed, shared cores",
			pod: testing_helper.MakePod().Namespace("foo").Name("foo").UID("foo").
				Req(map[v1.ResourceName]string{"cpu": "24", "memory": "96Gi", "nvidia.com/gpu": "1"}).Obj(),
			existingPods:     []*v1.Pod{p1, p2, p3, p5},
			cnr:              cnr1,
			enableColocation: true,
			expectedStatus:   framework.NewStatus(framework.Unschedulable, nonnativeresource.SharedCoresPodFailed),
		},
		{
			name: "[colocation] pass, no need to consider resources ratio",
			pod: testing_helper.MakePod().Namespace("foo").Name("foo").UID("foo").
				Req(map[v1.ResourceName]string{"cpu": "24", "memory": "80Gi", "nvidia.com/gpu": "2"}).Obj(),
			existingPods:     []*v1.Pod{p6, p7, p8},
			cnr:              cnr2,
			enableColocation: true,
			expectedStatus:   nil,
		},
		{
			name: "[colocation] pass, best-effort pod",
			pod: testing_helper.MakePod().Namespace("foo").Name("foo").UID("foo").
				Req(map[v1.ResourceName]string{"cpu": "1", "memory": "1Gi"}).
				Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.BestEffortPod)).Obj(),
			existingPods:     []*v1.Pod{p6, p7, p8, p4},
			cnr:              cnr2,
			enableColocation: true,
			expectedStatus:   nil,
		},
		{
			name: "[colocation] failed, guaranteed pod",
			pod: testing_helper.MakePod().Namespace("foo").Name("foo").UID("foo").
				Req(map[v1.ResourceName]string{"cpu": "1", "memory": "1Gi"}).Obj(),
			existingPods:     []*v1.Pod{p6, p7, p8, p4},
			cnr:              cnr2,
			enableColocation: true,
			expectedStatus:   framework.NewStatus(framework.Unschedulable, nonnativeresource.SharedCoresPodFailed),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.enableColocation {
				gate := utilfeature.DefaultFeatureGate.(featuregate.MutableFeatureGate)
				if err := gate.SetFromMap(map[string]bool{string(features.EnableColocation): true}); err != nil {
					t.Errorf("failed to set featuregate %s", features.EnableColocation)
				}
				if !utilfeature.DefaultFeatureGate.Enabled(features.EnableColocation) {
					t.Errorf("failed to set featuregate %s", features.EnableColocation)
				}
			} else {
				gate := utilfeature.DefaultFeatureGate.(featuregate.MutableFeatureGate)
				if err := gate.SetFromMap(map[string]bool{string(features.EnableColocation): false}); err != nil {
					t.Errorf("failed to set featuregate %s", features.EnableColocation)
				}
				if utilfeature.DefaultFeatureGate.Enabled(features.EnableColocation) {
					t.Errorf("failed to set featuregate %s", features.EnableColocation)
				}
			}

			podLister := testing_helper.NewFakePodLister(tt.existingPods)
			p := &NonNativeTopology{
				podLister: podLister,
			}
			cycleState := framework.NewCycleState()
			podResourceType, err := podutil.GetPodResourceType(tt.pod)
			if err != nil {
				t.Errorf("failed to get pod resource type: %v", err)
			}
			if err = framework.SetPodResourceTypeState(podResourceType, cycleState); err != nil {
				t.Errorf("failed to set pod resource type: %v", err)
			}
			preFilterStatus := p.PreFilter(context.Background(), cycleState, tt.pod)
			if !preFilterStatus.IsSuccess() {
				t.Errorf("prefilter failed with status: %v", preFilterStatus)
			}

			nodeInfo := framework.NewNodeInfo()
			nodeInfo.SetCNR(tt.cnr)
			nodeInfo.SetNode(node)
			for _, p := range tt.existingPods {
				nodeInfo.AddPod(p)
			}
			gotStatus := p.Filter(context.Background(), cycleState, tt.pod, nodeInfo)
			if !reflect.DeepEqual(tt.expectedStatus, gotStatus) {
				t.Errorf("expected status: %v, but got: %v", tt.expectedStatus, gotStatus)
			}
		})
	}
}
