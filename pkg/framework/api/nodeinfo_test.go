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
	"math"
	"reflect"
	"strconv"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	katalystv1alpha1 "github.com/kubewharf/katalyst-api/pkg/apis/node/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/sets"
	utilfeature "k8s.io/apiserver/pkg/util/feature"

	godelfeatures "github.com/kubewharf/godel-scheduler/pkg/features"
	"github.com/kubewharf/godel-scheduler/pkg/util"
	podutil "github.com/kubewharf/godel-scheduler/pkg/util/pod"
)

func fakeNodeInfo(pods ...*v1.Pod) NodeInfo {
	ni := NewNodeInfo(pods...)
	ni.SetNode(&v1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-node",
		},
	})
	return ni
}

func TestNewNodeInfo(t *testing.T) {
	utilfeature.DefaultMutableFeatureGate.SetFromMap(map[string]bool{string(godelfeatures.NonNativeResourceSchedulingSupport): true})

	nodeName := "test-node"
	pods := []*v1.Pod{
		makeBasePod(t, nodeName, "test-1", "100m", "500", "", []v1.ContainerPort{{HostIP: "127.0.0.1", HostPort: 80, Protocol: "TCP"}}),
		makeBasePod(t, nodeName, "test-2", "200m", "1Ki", "", []v1.ContainerPort{{HostIP: "127.0.0.1", HostPort: 8080, Protocol: "TCP"}}),
	}

	expected := &NodeInfoImpl{
		GuaranteedRequested: &Resource{
			MilliCPU:         300,
			Memory:           1524,
			EphemeralStorage: 0,
			AllowedPodNumber: 0,
			ScalarResources:  map[v1.ResourceName]int64(nil),
		},
		GuaranteedNonZeroRequested: &Resource{
			MilliCPU:         300,
			Memory:           1524,
			EphemeralStorage: 0,
			AllowedPodNumber: 0,
			ScalarResources:  map[v1.ResourceName]int64(nil),
		},
		BestEffortRequested:        &Resource{},
		BestEffortNonZeroRequested: &Resource{},
		BestEffortAllocatable:      &Resource{},
		TransientInfo:              NewTransientSchedulerInfo(),
		GuaranteedAllocatable:      &Resource{},
		GuaranteedCapacity:         &Resource{},
		Generation:                 2,
		UsedPorts: HostPortInfo{
			"127.0.0.1": map[ProtocolPort]struct{}{
				{Protocol: "TCP", Port: 80}:   {},
				{Protocol: "TCP", Port: 8080}: {},
			},
		},
		ImageStates: map[string]*ImageStateSummary{},
		PodInfoMaintainer: NewPodInfoMaintainer([]*PodInfo{
			NewPodInfo(
				&v1.Pod{
					ObjectMeta: metav1.ObjectMeta{
						Namespace: "node_info_cache_test",
						Name:      "test-1",
						UID:       types.UID("test-1"),
					},
					Spec: v1.PodSpec{
						Containers: []v1.Container{
							{
								Resources: v1.ResourceRequirements{
									Requests: v1.ResourceList{
										v1.ResourceCPU:    resource.MustParse("100m"),
										v1.ResourceMemory: resource.MustParse("500"),
									},
								},
								Ports: []v1.ContainerPort{
									{
										HostIP:   "127.0.0.1",
										HostPort: 80,
										Protocol: "TCP",
									},
								},
							},
						},
						NodeName: nodeName,
					},
				}),
			NewPodInfo(
				&v1.Pod{
					ObjectMeta: metav1.ObjectMeta{
						Namespace: "node_info_cache_test",
						Name:      "test-2",
						UID:       types.UID("test-2"),
					},
					Spec: v1.PodSpec{
						Containers: []v1.Container{
							{
								Resources: v1.ResourceRequirements{
									Requests: v1.ResourceList{
										v1.ResourceCPU:    resource.MustParse("200m"),
										v1.ResourceMemory: resource.MustParse("1Ki"),
									},
								},
								Ports: []v1.ContainerPort{
									{
										HostIP:   "127.0.0.1",
										HostPort: 8080,
										Protocol: "TCP",
									},
								},
							},
						},
						NodeName: nodeName,
					},
				}),
		}...),
		NumaTopologyStatus: &NumaTopologyStatus{
			podAllocations:                       make(map[string]*PodAllocation),
			topology:                             make(map[int]*NumaStatus),
			requestsOfSharedCores:                &Resource{},
			socketToFreeNumasOfConflictResources: map[int]sets.Int{},
		},
	}

	ni := NewNodeInfo(pods...)
	expected.Generation = ni.GetGeneration()

	if !cmp.Equal(expected, ni, cmpopts.IgnoreUnexported(NodeInfoImpl{})) {
		t.Errorf("expected: %#v, got: %#v", expected, ni)
	}
}

func TestNodeInfoClone(t *testing.T) {
	nodeName := "test-node"
	tests := []struct {
		nodeInfo NodeInfo
		expected NodeInfo
	}{
		{
			nodeInfo: &NodeInfoImpl{
				GuaranteedRequested:        &Resource{},
				GuaranteedNonZeroRequested: &Resource{},
				BestEffortRequested:        &Resource{},
				BestEffortNonZeroRequested: &Resource{},
				BestEffortAllocatable:      &Resource{},
				TransientInfo:              NewTransientSchedulerInfo(),
				GuaranteedAllocatable:      &Resource{},
				GuaranteedCapacity:         &Resource{},
				Generation:                 2,
				UsedPorts: HostPortInfo{
					"127.0.0.1": map[ProtocolPort]struct{}{
						{Protocol: "TCP", Port: 80}:   {},
						{Protocol: "TCP", Port: 8080}: {},
					},
				},
				ImageStates: map[string]*ImageStateSummary{},
				PodInfoMaintainer: NewPodInfoMaintainer([]*PodInfo{
					NewPodInfo(
						&v1.Pod{
							ObjectMeta: metav1.ObjectMeta{
								Namespace: "node_info_cache_test",
								Name:      "test-1",
								UID:       types.UID("test-1"),
							},
							Spec: v1.PodSpec{
								Containers: []v1.Container{
									{
										Resources: v1.ResourceRequirements{
											Requests: v1.ResourceList{
												v1.ResourceCPU:    resource.MustParse("100m"),
												v1.ResourceMemory: resource.MustParse("500"),
											},
										},
										Ports: []v1.ContainerPort{
											{
												HostIP:   "127.0.0.1",
												HostPort: 80,
												Protocol: "TCP",
											},
										},
									},
								},
								NodeName: nodeName,
							},
						},
					),
					NewPodInfo(
						&v1.Pod{
							ObjectMeta: metav1.ObjectMeta{
								Namespace: "node_info_cache_test",
								Name:      "test-2",
								UID:       types.UID("test-2"),
							},
							Spec: v1.PodSpec{
								Containers: []v1.Container{
									{
										Resources: v1.ResourceRequirements{
											Requests: v1.ResourceList{
												v1.ResourceCPU:    resource.MustParse("200m"),
												v1.ResourceMemory: resource.MustParse("1Ki"),
											},
										},
										Ports: []v1.ContainerPort{
											{
												HostIP:   "127.0.0.1",
												HostPort: 8080,
												Protocol: "TCP",
											},
										},
									},
								},
								NodeName: nodeName,
							},
						},
					),
				}...),
				NumaTopologyStatus: &NumaTopologyStatus{
					podAllocations:         make(map[string]*PodAllocation),
					topology:               make(map[int]*NumaStatus),
					requestsOfSharedCores:  &Resource{},
					availableOfSharedCores: &Resource{},
				},
			},
			expected: &NodeInfoImpl{
				GuaranteedRequested:        &Resource{},
				GuaranteedNonZeroRequested: &Resource{},
				BestEffortRequested:        &Resource{},
				BestEffortNonZeroRequested: &Resource{},
				BestEffortAllocatable:      &Resource{},
				TransientInfo:              NewTransientSchedulerInfo(),
				GuaranteedAllocatable:      &Resource{},
				GuaranteedCapacity:         &Resource{},
				Generation:                 2,
				UsedPorts: HostPortInfo{
					"127.0.0.1": map[ProtocolPort]struct{}{
						{Protocol: "TCP", Port: 80}:   {},
						{Protocol: "TCP", Port: 8080}: {},
					},
				},
				ImageStates: map[string]*ImageStateSummary{},
				PodInfoMaintainer: NewPodInfoMaintainer([]*PodInfo{
					NewPodInfo(
						&v1.Pod{
							ObjectMeta: metav1.ObjectMeta{
								Namespace: "node_info_cache_test",
								Name:      "test-1",
								UID:       types.UID("test-1"),
							},
							Spec: v1.PodSpec{
								Containers: []v1.Container{
									{
										Resources: v1.ResourceRequirements{
											Requests: v1.ResourceList{
												v1.ResourceCPU:    resource.MustParse("100m"),
												v1.ResourceMemory: resource.MustParse("500"),
											},
										},
										Ports: []v1.ContainerPort{
											{
												HostIP:   "127.0.0.1",
												HostPort: 80,
												Protocol: "TCP",
											},
										},
									},
								},
								NodeName: nodeName,
							},
						},
					),
					NewPodInfo(
						&v1.Pod{
							ObjectMeta: metav1.ObjectMeta{
								Namespace: "node_info_cache_test",
								Name:      "test-2",
								UID:       types.UID("test-2"),
							},
							Spec: v1.PodSpec{
								Containers: []v1.Container{
									{
										Resources: v1.ResourceRequirements{
											Requests: v1.ResourceList{
												v1.ResourceCPU:    resource.MustParse("200m"),
												v1.ResourceMemory: resource.MustParse("1Ki"),
											},
										},
										Ports: []v1.ContainerPort{
											{
												HostIP:   "127.0.0.1",
												HostPort: 8080,
												Protocol: "TCP",
											},
										},
									},
								},
								NodeName: nodeName,
							},
						},
					),
				}...),
				NumaTopologyStatus: &NumaTopologyStatus{
					podAllocations:                       make(map[string]*PodAllocation),
					topology:                             make(map[int]*NumaStatus),
					requestsOfSharedCores:                &Resource{},
					socketToFreeNumasOfConflictResources: map[int]sets.Int{},
				},
			},
		},
	}

	for _, test := range tests {
		ni := test.nodeInfo.Clone()
		// Modify the field to check if the result is a clone of the origin one.
		test.nodeInfo.SetGeneration(test.nodeInfo.GetGeneration() + 10)
		test.nodeInfo.GetUsedPorts().Remove("127.0.0.1", "TCP", 80)
		if !cmp.Equal(test.expected, ni, cmpopts.IgnoreUnexported(NodeInfoImpl{})) {
			t.Errorf("expected: %#v, got: %#v", test.expected, ni)
		}
	}
}

func TestNodeInfoAddPod(t *testing.T) {
	utilfeature.DefaultMutableFeatureGate.SetFromMap(map[string]bool{string(godelfeatures.NonNativeResourceSchedulingSupport): true})

	nodeName := "test-node"
	pods := []*v1.Pod{
		{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: "node_info_cache_test",
				Name:      "test-1",
				UID:       types.UID("test-1"),
			},
			Spec: v1.PodSpec{
				Containers: []v1.Container{
					{
						Resources: v1.ResourceRequirements{
							Requests: v1.ResourceList{
								v1.ResourceCPU:    resource.MustParse("100m"),
								v1.ResourceMemory: resource.MustParse("500"),
							},
						},
						Ports: []v1.ContainerPort{
							{
								HostIP:   "127.0.0.1",
								HostPort: 80,
								Protocol: "TCP",
							},
						},
					},
				},
				NodeName: nodeName,
				Overhead: v1.ResourceList{
					v1.ResourceCPU: resource.MustParse("500m"),
				},
			},
		},
		{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: "node_info_cache_test",
				Name:      "test-2",
				UID:       types.UID("test-2"),
			},
			Spec: v1.PodSpec{
				Containers: []v1.Container{
					{
						Resources: v1.ResourceRequirements{
							Requests: v1.ResourceList{
								v1.ResourceCPU: resource.MustParse("200m"),
							},
						},
						Ports: []v1.ContainerPort{
							{
								HostIP:   "127.0.0.1",
								HostPort: 8080,
								Protocol: "TCP",
							},
						},
					},
				},
				NodeName: nodeName,
				Overhead: v1.ResourceList{
					v1.ResourceCPU:    resource.MustParse("500m"),
					v1.ResourceMemory: resource.MustParse("500"),
				},
			},
		},
		{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: "node_info_cache_test",
				Name:      "test-3",
				UID:       types.UID("test-3"),
			},
			Spec: v1.PodSpec{
				Containers: []v1.Container{
					{
						Resources: v1.ResourceRequirements{
							Requests: v1.ResourceList{
								v1.ResourceCPU: resource.MustParse("200m"),
							},
						},
						Ports: []v1.ContainerPort{
							{
								HostIP:   "127.0.0.1",
								HostPort: 8080,
								Protocol: "TCP",
							},
						},
					},
				},
				InitContainers: []v1.Container{
					{
						Resources: v1.ResourceRequirements{
							Requests: v1.ResourceList{
								v1.ResourceCPU:    resource.MustParse("500m"),
								v1.ResourceMemory: resource.MustParse("200Mi"),
							},
						},
					},
				},
				NodeName: nodeName,
				Overhead: v1.ResourceList{
					v1.ResourceCPU:    resource.MustParse("500m"),
					v1.ResourceMemory: resource.MustParse("500"),
				},
			},
		},
	}
	expected := &NodeInfoImpl{
		Node: &v1.Node{
			ObjectMeta: metav1.ObjectMeta{
				Name: "test-node",
			},
		},
		GuaranteedRequested: &Resource{
			MilliCPU:         2300,
			Memory:           209716700, // 1500 + 200MB in initContainers
			EphemeralStorage: 0,
			AllowedPodNumber: 0,
			ScalarResources:  map[v1.ResourceName]int64(nil),
		},
		GuaranteedNonZeroRequested: &Resource{
			MilliCPU:         2300,
			Memory:           419431900, // 200MB(initContainers) + 200MB(default memory value) + 1500 specified in requests/overhead
			EphemeralStorage: 0,
			AllowedPodNumber: 0,
			ScalarResources:  map[v1.ResourceName]int64(nil),
		},
		BestEffortRequested:        &Resource{},
		BestEffortNonZeroRequested: &Resource{},
		BestEffortAllocatable:      &Resource{},
		TransientInfo:              NewTransientSchedulerInfo(),
		GuaranteedAllocatable:      &Resource{},
		GuaranteedCapacity:         &Resource{},
		Generation:                 2,
		UsedPorts: HostPortInfo{
			"127.0.0.1": map[ProtocolPort]struct{}{
				{Protocol: "TCP", Port: 80}:   {},
				{Protocol: "TCP", Port: 8080}: {},
			},
		},
		ImageStates: map[string]*ImageStateSummary{},
		PodInfoMaintainer: NewPodInfoMaintainer([]*PodInfo{
			NewPodInfo(
				&v1.Pod{
					ObjectMeta: metav1.ObjectMeta{
						Namespace: "node_info_cache_test",
						Name:      "test-1",
						UID:       types.UID("test-1"),
					},
					Spec: v1.PodSpec{
						Containers: []v1.Container{
							{
								Resources: v1.ResourceRequirements{
									Requests: v1.ResourceList{
										v1.ResourceCPU:    resource.MustParse("100m"),
										v1.ResourceMemory: resource.MustParse("500"),
									},
								},
								Ports: []v1.ContainerPort{
									{
										HostIP:   "127.0.0.1",
										HostPort: 80,
										Protocol: "TCP",
									},
								},
							},
						},
						NodeName: nodeName,
						Overhead: v1.ResourceList{
							v1.ResourceCPU: resource.MustParse("500m"),
						},
					},
				}),
			NewPodInfo(
				&v1.Pod{
					ObjectMeta: metav1.ObjectMeta{
						Namespace: "node_info_cache_test",
						Name:      "test-2",
						UID:       types.UID("test-2"),
					},
					Spec: v1.PodSpec{
						Containers: []v1.Container{
							{
								Resources: v1.ResourceRequirements{
									Requests: v1.ResourceList{
										v1.ResourceCPU: resource.MustParse("200m"),
									},
								},
								Ports: []v1.ContainerPort{
									{
										HostIP:   "127.0.0.1",
										HostPort: 8080,
										Protocol: "TCP",
									},
								},
							},
						},
						NodeName: nodeName,
						Overhead: v1.ResourceList{
							v1.ResourceCPU:    resource.MustParse("500m"),
							v1.ResourceMemory: resource.MustParse("500"),
						},
					},
				}),
			NewPodInfo(
				&v1.Pod{
					ObjectMeta: metav1.ObjectMeta{
						Namespace: "node_info_cache_test",
						Name:      "test-3",
						UID:       types.UID("test-3"),
					},
					Spec: v1.PodSpec{
						Containers: []v1.Container{
							{
								Resources: v1.ResourceRequirements{
									Requests: v1.ResourceList{
										v1.ResourceCPU: resource.MustParse("200m"),
									},
								},
								Ports: []v1.ContainerPort{
									{
										HostIP:   "127.0.0.1",
										HostPort: 8080,
										Protocol: "TCP",
									},
								},
							},
						},
						InitContainers: []v1.Container{
							{
								Resources: v1.ResourceRequirements{
									Requests: v1.ResourceList{
										v1.ResourceCPU:    resource.MustParse("500m"),
										v1.ResourceMemory: resource.MustParse("200Mi"),
									},
								},
							},
						},
						NodeName: nodeName,
						Overhead: v1.ResourceList{
							v1.ResourceCPU:    resource.MustParse("500m"),
							v1.ResourceMemory: resource.MustParse("500"),
						},
					},
				}),
		}...),
		NumaTopologyStatus: &NumaTopologyStatus{
			podAllocations: make(map[string]*PodAllocation),
			topology:       make(map[int]*NumaStatus),
			requestsOfSharedCores: &Resource{
				MilliCPU: 2300,
				Memory:   209716700,
			},
			availableOfSharedCores:               &Resource{},
			socketToFreeNumasOfConflictResources: map[int]sets.Int{},
		},
	}

	ni := fakeNodeInfo()
	for _, pod := range pods {
		ni.AddPod(pod)
	}

	if !reflect.DeepEqual(expected.NumaTopologyStatus, ni.GetNumaTopologyStatus()) {
		t.Errorf("expected: %#v, got: %#v", expected.NumaTopologyStatus, ni.GetNumaTopologyStatus())
	}

	expected.Generation = ni.GetGeneration()
	if !cmp.Equal(NodeInfo(expected), ni, cmpopts.IgnoreUnexported(NodeInfoImpl{})) {
		// if !reflect.DeepEqual(NodeInfo(expected), ni) {
		t.Errorf("expected: %#v, got: %#v", expected, ni)
	}
}

func TestNodeInfoRemovePod(t *testing.T) {
	utilfeature.DefaultMutableFeatureGate.SetFromMap(map[string]bool{string(godelfeatures.NonNativeResourceSchedulingSupport): true})

	nodeName := "test-node"
	pods := []*v1.Pod{
		makeBasePod(t, nodeName, "test-1", "100m", "500", "", []v1.ContainerPort{{HostIP: "127.0.0.1", HostPort: 80, Protocol: "TCP"}}),

		makeBasePod(t, nodeName, "test-2", "200m", "1Ki", "", []v1.ContainerPort{{HostIP: "127.0.0.1", HostPort: 8080, Protocol: "TCP"}}),
	}

	// add pod Overhead
	for _, pod := range pods {
		pod.Spec.Overhead = v1.ResourceList{
			v1.ResourceCPU:    resource.MustParse("500m"),
			v1.ResourceMemory: resource.MustParse("500"),
		}
	}

	tests := []struct {
		pod              *v1.Pod
		errExpected      bool
		expectedNodeInfo NodeInfo
	}{
		{
			pod:         makeBasePod(t, nodeName, "non-exist", "0", "0", "", []v1.ContainerPort{{}}),
			errExpected: true,
			expectedNodeInfo: &NodeInfoImpl{
				Node: &v1.Node{
					ObjectMeta: metav1.ObjectMeta{
						Name: "test-node",
					},
				},
				GuaranteedRequested: &Resource{
					MilliCPU:         1300,
					Memory:           2524,
					EphemeralStorage: 0,
					AllowedPodNumber: 0,
					ScalarResources:  map[v1.ResourceName]int64(nil),
				},
				GuaranteedNonZeroRequested: &Resource{
					MilliCPU:         1300,
					Memory:           2524,
					EphemeralStorage: 0,
					AllowedPodNumber: 0,
					ScalarResources:  map[v1.ResourceName]int64(nil),
				},
				BestEffortRequested:        &Resource{},
				BestEffortNonZeroRequested: &Resource{},
				BestEffortAllocatable:      &Resource{},
				TransientInfo:              NewTransientSchedulerInfo(),
				GuaranteedAllocatable:      &Resource{},
				GuaranteedCapacity:         &Resource{},
				Generation:                 2,
				UsedPorts: HostPortInfo{
					"127.0.0.1": map[ProtocolPort]struct{}{
						{Protocol: "TCP", Port: 80}:   {},
						{Protocol: "TCP", Port: 8080}: {},
					},
				},
				ImageStates: map[string]*ImageStateSummary{},
				PodInfoMaintainer: NewPodInfoMaintainer([]*PodInfo{
					NewPodInfo(
						&v1.Pod{
							ObjectMeta: metav1.ObjectMeta{
								Namespace: "node_info_cache_test",
								Name:      "test-1",
								UID:       types.UID("test-1"),
							},
							Spec: v1.PodSpec{
								Containers: []v1.Container{
									{
										Resources: v1.ResourceRequirements{
											Requests: v1.ResourceList{
												v1.ResourceCPU:    resource.MustParse("100m"),
												v1.ResourceMemory: resource.MustParse("500"),
											},
										},
										Ports: []v1.ContainerPort{
											{
												HostIP:   "127.0.0.1",
												HostPort: 80,
												Protocol: "TCP",
											},
										},
									},
								},
								NodeName: nodeName,
								Overhead: v1.ResourceList{
									v1.ResourceCPU:    resource.MustParse("500m"),
									v1.ResourceMemory: resource.MustParse("500"),
								},
							},
						}),
					NewPodInfo(
						&v1.Pod{
							ObjectMeta: metav1.ObjectMeta{
								Namespace: "node_info_cache_test",
								Name:      "test-2",
								UID:       types.UID("test-2"),
							},
							Spec: v1.PodSpec{
								Containers: []v1.Container{
									{
										Resources: v1.ResourceRequirements{
											Requests: v1.ResourceList{
												v1.ResourceCPU:    resource.MustParse("200m"),
												v1.ResourceMemory: resource.MustParse("1Ki"),
											},
										},
										Ports: []v1.ContainerPort{
											{
												HostIP:   "127.0.0.1",
												HostPort: 8080,
												Protocol: "TCP",
											},
										},
									},
								},
								NodeName: nodeName,
								Overhead: v1.ResourceList{
									v1.ResourceCPU:    resource.MustParse("500m"),
									v1.ResourceMemory: resource.MustParse("500"),
								},
							},
						}),
				}...),
				NumaTopologyStatus: &NumaTopologyStatus{
					podAllocations: make(map[string]*PodAllocation),
					topology:       make(map[int]*NumaStatus),
					requestsOfSharedCores: &Resource{
						MilliCPU: 1300,
						Memory:   2524,
					},
					availableOfSharedCores:               &Resource{},
					socketToFreeNumasOfConflictResources: map[int]sets.Int{},
				},
			},
		},
		{
			pod: &v1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: "node_info_cache_test",
					Name:      "test-1",
					UID:       types.UID("test-1"),
				},
				Spec: v1.PodSpec{
					Containers: []v1.Container{
						{
							Resources: v1.ResourceRequirements{
								Requests: v1.ResourceList{
									v1.ResourceCPU:    resource.MustParse("100m"),
									v1.ResourceMemory: resource.MustParse("500"),
								},
							},
							Ports: []v1.ContainerPort{
								{
									HostIP:   "127.0.0.1",
									HostPort: 80,
									Protocol: "TCP",
								},
							},
						},
					},
					NodeName: nodeName,
					Overhead: v1.ResourceList{
						v1.ResourceCPU:    resource.MustParse("500m"),
						v1.ResourceMemory: resource.MustParse("500"),
					},
				},
			},
			errExpected: false,
			expectedNodeInfo: &NodeInfoImpl{
				Node: &v1.Node{
					ObjectMeta: metav1.ObjectMeta{
						Name: "test-node",
					},
				},
				GuaranteedRequested: &Resource{
					MilliCPU:         700,
					Memory:           1524,
					EphemeralStorage: 0,
					AllowedPodNumber: 0,
					ScalarResources:  map[v1.ResourceName]int64(nil),
				},
				GuaranteedNonZeroRequested: &Resource{
					MilliCPU:         700,
					Memory:           1524,
					EphemeralStorage: 0,
					AllowedPodNumber: 0,
					ScalarResources:  map[v1.ResourceName]int64(nil),
				},
				BestEffortRequested:        &Resource{},
				BestEffortNonZeroRequested: &Resource{},
				BestEffortAllocatable:      &Resource{},
				TransientInfo:              NewTransientSchedulerInfo(),
				GuaranteedAllocatable:      &Resource{},
				GuaranteedCapacity:         &Resource{},
				Generation:                 3,
				UsedPorts: HostPortInfo{
					"127.0.0.1": map[ProtocolPort]struct{}{
						{Protocol: "TCP", Port: 8080}: {},
					},
				},
				ImageStates: map[string]*ImageStateSummary{},
				PodInfoMaintainer: NewPodInfoMaintainer([]*PodInfo{
					NewPodInfo(
						&v1.Pod{
							ObjectMeta: metav1.ObjectMeta{
								Namespace: "node_info_cache_test",
								Name:      "test-2",
								UID:       types.UID("test-2"),
							},
							Spec: v1.PodSpec{
								Containers: []v1.Container{
									{
										Resources: v1.ResourceRequirements{
											Requests: v1.ResourceList{
												v1.ResourceCPU:    resource.MustParse("200m"),
												v1.ResourceMemory: resource.MustParse("1Ki"),
											},
										},
										Ports: []v1.ContainerPort{
											{
												HostIP:   "127.0.0.1",
												HostPort: 8080,
												Protocol: "TCP",
											},
										},
									},
								},
								NodeName: nodeName,
								Overhead: v1.ResourceList{
									v1.ResourceCPU:    resource.MustParse("500m"),
									v1.ResourceMemory: resource.MustParse("500"),
								},
							},
						},
					),
				}...),
				NumaTopologyStatus: &NumaTopologyStatus{
					podAllocations: make(map[string]*PodAllocation),
					topology:       make(map[int]*NumaStatus),
					requestsOfSharedCores: &Resource{
						MilliCPU: 700,
						Memory:   1524,
					},
					availableOfSharedCores:               &Resource{},
					socketToFreeNumasOfConflictResources: map[int]sets.Int{},
				},
			},
		},
	}

	for _, test := range tests {
		ni := fakeNodeInfo(pods...)

		err := ni.RemovePod(test.pod, false)
		if err != nil {
			if test.errExpected {
				expectedErrorMsg := fmt.Errorf("no corresponding pod %s in pods of node %s", test.pod.Name, ni.GetNode().Name)
				if expectedErrorMsg == err {
					t.Errorf("expected error: %v, got: %v", expectedErrorMsg, err)
				}
			} else {
				t.Errorf("expected no error, got: %v", err)
			}
		}

		test.expectedNodeInfo.SetGeneration(ni.GetGeneration())

		if !cmp.Equal(test.expectedNodeInfo, ni, cmpopts.IgnoreUnexported(NodeInfoImpl{})) {
			// if !reflect.DeepEqual(test.expectedNodeInfo, ni) {
			t.Errorf("expected: %#v, got: %#v", test.expectedNodeInfo, ni)
		}

		if !reflect.DeepEqual(test.expectedNodeInfo.GetNumaTopologyStatus(), ni.GetNumaTopologyStatus()) {
			t.Errorf("expected: %#v, got: %#v", test.expectedNodeInfo.GetNumaTopologyStatus(), ni.GetNumaTopologyStatus())
		}
	}
}

func TestNonNativeInNodeInfoOnlyAgent(t *testing.T) {
	utilfeature.DefaultMutableFeatureGate.SetFromMap(map[string]bool{string(godelfeatures.NonNativeResourceSchedulingSupport): true})

	setPod := func(name string, res map[v1.ResourceName]string, annotations map[string]string) *v1.Pod {
		return &v1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Namespace:   "default",
				Name:        name,
				UID:         types.UID(name),
				Annotations: annotations,
			},
			Spec: v1.PodSpec{
				Containers: []v1.Container{
					{
						Resources: v1.ResourceRequirements{
							Requests: *parseResourceList(res),
						},
					},
				},
			},
		}
	}

	cnr := &katalystv1alpha1.CustomNodeResource{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-node",
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
								Allocatable: parseResourceList(largeRes),
								Capacity:    parseResourceList(largeRes),
							},
							Allocations: []*katalystv1alpha1.Allocation{
								{
									Consumer: "default/p1/p1",
									Requests: parseResourceList(smallRes),
								},
							},
						},
						{
							Type: katalystv1alpha1.TopologyTypeNuma,
							Name: "1",
							Resources: katalystv1alpha1.Resources{
								Allocatable: parseResourceList(largeRes),
								Capacity:    parseResourceList(largeRes),
							},
							Allocations: []*katalystv1alpha1.Allocation{
								{
									Consumer: "default/p2/p2",
									Requests: parseResourceList(largeRes),
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
								Allocatable: parseResourceList(largeRes),
								Capacity:    parseResourceList(largeRes),
							},
							Allocations: []*katalystv1alpha1.Allocation{},
						},
						{
							Type: katalystv1alpha1.TopologyTypeNuma,
							Name: "3",
							Resources: katalystv1alpha1.Resources{
								Allocatable: parseResourceList(largeRes),
								Capacity:    parseResourceList(largeRes),
							},
							Allocations: []*katalystv1alpha1.Allocation{},
						},
					},
				},
			},
		},
	}
	numaTopologyStatus := &NumaTopologyStatus{
		podAllocations: map[string]*PodAllocation{
			"default/p1/p1": {
				agent: true,
				numaAllocations: map[int]*v1.ResourceList{
					0: parseResourceList(smallRes),
				},
			},
			"default/p2/p2": {
				agent: true,
				numaAllocations: map[int]*v1.ResourceList{
					1: parseResourceList(largeRes),
				},
			},
		},
		topology: map[int]*NumaStatus{
			0: {
				socketId: 0,
				resourceStatuses: map[string]*ResourceStatus{
					"cpu": {
						Allocatable: resource.NewQuantity(24, resource.DecimalSI),
						Available:   resource.NewQuantity(16, resource.DecimalSI),
						Users:       sets.NewString("default/p1/p1"),
					},
					"memory": {
						Allocatable: resource.NewQuantity(40*1024*1024*1024, resource.BinarySI),
						Available:   resource.NewQuantity(20*1024*1024*1024, resource.BinarySI),
						Users:       sets.NewString("default/p1/p1"),
					},
					"nvidia.com/gpu": {
						Allocatable: resource.NewQuantity(2, resource.DecimalSI),
						Available:   resource.NewQuantity(1, resource.DecimalSI),
						Users:       sets.NewString("default/p1/p1"),
					},
				},
			},
			1: {
				socketId: 0,
				resourceStatuses: map[string]*ResourceStatus{
					"cpu": {
						Allocatable: resource.NewQuantity(24, resource.DecimalSI),
						Available:   resource.NewQuantity(0, resource.DecimalSI),
						Users:       sets.NewString("default/p2/p2"),
					},
					"memory": {
						Allocatable: resource.NewQuantity(40*1024*1024*1024, resource.BinarySI),
						Available:   resource.NewQuantity(0, resource.BinarySI),
						Users:       sets.NewString("default/p2/p2"),
					},
					"nvidia.com/gpu": {
						Allocatable: resource.NewQuantity(2, resource.DecimalSI),
						Available:   resource.NewQuantity(0, resource.DecimalSI),
						Users:       sets.NewString("default/p2/p2"),
					},
				},
			},
			2: {
				socketId: 1,
				resourceStatuses: map[string]*ResourceStatus{
					"cpu": {
						Allocatable: resource.NewQuantity(24, resource.DecimalSI),
						Available:   resource.NewQuantity(24, resource.DecimalSI),
						Users:       sets.NewString(),
					},
					"memory": {
						Allocatable: resource.NewQuantity(40*1024*1024*1024, resource.BinarySI),
						Available:   resource.NewQuantity(40*1024*1024*1024, resource.BinarySI),
						Users:       sets.NewString(),
					},
					"nvidia.com/gpu": {
						Allocatable: resource.NewQuantity(2, resource.DecimalSI),
						Available:   resource.NewQuantity(2, resource.DecimalSI),
						Users:       sets.NewString(),
					},
				},
			},
			3: {
				socketId: 1,
				resourceStatuses: map[string]*ResourceStatus{
					"cpu": {
						Allocatable: resource.NewQuantity(24, resource.DecimalSI),
						Available:   resource.NewQuantity(24, resource.DecimalSI),
						Users:       sets.NewString(),
					},
					"memory": {
						Allocatable: resource.NewQuantity(40*1024*1024*1024, resource.BinarySI),
						Available:   resource.NewQuantity(40*1024*1024*1024, resource.BinarySI),
						Users:       sets.NewString(),
					},
					"nvidia.com/gpu": {
						Allocatable: resource.NewQuantity(2, resource.DecimalSI),
						Available:   resource.NewQuantity(2, resource.DecimalSI),
						Users:       sets.NewString(),
					},
				},
			},
		},
		requestsOfSharedCores: &Resource{
			MilliCPU: 8000,
			Memory:   20 * 1024 * 1024 * 1024,
			ScalarResources: map[v1.ResourceName]int64{
				util.ResourceGPU: 1,
			},
		},
		availableOfSharedCores: &Resource{
			MilliCPU: 48000,
			Memory:   80 * 1024 * 1024 * 1024,
		},
		socketToFreeNumasOfConflictResources: map[int]sets.Int{
			1: sets.NewInt(2, 3),
		},
	}
	topologyAfterPreemptP1 := &NumaTopologyStatus{
		podAllocations: map[string]*PodAllocation{
			"default/p1/p1": {
				agent: true,
				numaAllocations: map[int]*v1.ResourceList{
					0: parseResourceList(smallRes),
				},
			},
			"default/p2/p2": {
				agent: true,
				numaAllocations: map[int]*v1.ResourceList{
					1: parseResourceList(largeRes),
				},
			},
		},
		topology: map[int]*NumaStatus{
			0: {
				socketId: 0,
				resourceStatuses: map[string]*ResourceStatus{
					"cpu": {
						Allocatable: resource.NewQuantity(24, resource.DecimalSI),
						Available:   resource.NewQuantity(24, resource.DecimalSI),
						Users:       sets.NewString(),
					},
					"memory": {
						Allocatable: resource.NewQuantity(40*1024*1024*1024, resource.BinarySI),
						Available:   resource.NewQuantity(40*1024*1024*1024, resource.BinarySI),
						Users:       sets.NewString(),
					},
					"nvidia.com/gpu": {
						Allocatable: resource.NewQuantity(2, resource.DecimalSI),
						Available:   resource.NewQuantity(2, resource.DecimalSI),
						Users:       sets.NewString(),
					},
				},
			},
			1: {
				socketId: 0,
				resourceStatuses: map[string]*ResourceStatus{
					"cpu": {
						Allocatable: resource.NewQuantity(24, resource.DecimalSI),
						Available:   resource.NewQuantity(0, resource.DecimalSI),
						Users:       sets.NewString("default/p2/p2"),
					},
					"memory": {
						Allocatable: resource.NewQuantity(40*1024*1024*1024, resource.BinarySI),
						Available:   resource.NewQuantity(0, resource.BinarySI),
						Users:       sets.NewString("default/p2/p2"),
					},
					"nvidia.com/gpu": {
						Allocatable: resource.NewQuantity(2, resource.DecimalSI),
						Available:   resource.NewQuantity(0, resource.DecimalSI),
						Users:       sets.NewString("default/p2/p2"),
					},
				},
			},
			2: {
				socketId: 1,
				resourceStatuses: map[string]*ResourceStatus{
					"cpu": {
						Allocatable: resource.NewQuantity(24, resource.DecimalSI),
						Available:   resource.NewQuantity(24, resource.DecimalSI),
						Users:       sets.NewString(),
					},
					"memory": {
						Allocatable: resource.NewQuantity(40*1024*1024*1024, resource.BinarySI),
						Available:   resource.NewQuantity(40*1024*1024*1024, resource.BinarySI),
						Users:       sets.NewString(),
					},
					"nvidia.com/gpu": {
						Allocatable: resource.NewQuantity(2, resource.DecimalSI),
						Available:   resource.NewQuantity(2, resource.DecimalSI),
						Users:       sets.NewString(),
					},
				},
			},
			3: {
				socketId: 1,
				resourceStatuses: map[string]*ResourceStatus{
					"cpu": {
						Allocatable: resource.NewQuantity(24, resource.DecimalSI),
						Available:   resource.NewQuantity(24, resource.DecimalSI),
						Users:       sets.NewString(),
					},
					"memory": {
						Allocatable: resource.NewQuantity(40*1024*1024*1024, resource.BinarySI),
						Available:   resource.NewQuantity(40*1024*1024*1024, resource.BinarySI),
						Users:       sets.NewString(),
					},
					"nvidia.com/gpu": {
						Allocatable: resource.NewQuantity(2, resource.DecimalSI),
						Available:   resource.NewQuantity(2, resource.DecimalSI),
						Users:       sets.NewString(),
					},
				},
			},
		},
		requestsOfSharedCores: &Resource{
			MilliCPU: 8000,
			Memory:   20 * 1024 * 1024 * 1024,
			ScalarResources: map[v1.ResourceName]int64{
				util.ResourceGPU: 1,
			},
		},
		availableOfSharedCores: &Resource{
			MilliCPU: 72000,
			Memory:   120 * 1024 * 1024 * 1024,
		},
		socketToFreeNumasOfConflictResources: map[int]sets.Int{
			0: sets.NewInt(0),
			1: sets.NewInt(2, 3),
		},
	}
	topologyAfterRemoveP1 := &NumaTopologyStatus{
		podAllocations: map[string]*PodAllocation{
			"default/p1/p1": {
				agent: true,
				numaAllocations: map[int]*v1.ResourceList{
					0: parseResourceList(smallRes),
				},
			},
			"default/p2/p2": {
				agent: true,
				numaAllocations: map[int]*v1.ResourceList{
					1: parseResourceList(largeRes),
				},
			},
		},
		topology: map[int]*NumaStatus{
			0: {
				socketId: 0,
				resourceStatuses: map[string]*ResourceStatus{
					"cpu": {
						Allocatable: resource.NewQuantity(24, resource.DecimalSI),
						Available:   resource.NewQuantity(16, resource.DecimalSI),
						Users:       sets.NewString("default/p1/p1"),
					},
					"memory": {
						Allocatable: resource.NewQuantity(40*1024*1024*1024, resource.BinarySI),
						Available:   resource.NewQuantity(20*1024*1024*1024, resource.BinarySI),
						Users:       sets.NewString("default/p1/p1"),
					},
					"nvidia.com/gpu": {
						Allocatable: resource.NewQuantity(2, resource.DecimalSI),
						Available:   resource.NewQuantity(1, resource.DecimalSI),
						Users:       sets.NewString("default/p1/p1"),
					},
				},
			},
			1: {
				socketId: 0,
				resourceStatuses: map[string]*ResourceStatus{
					"cpu": {
						Allocatable: resource.NewQuantity(24, resource.DecimalSI),
						Available:   resource.NewQuantity(0, resource.DecimalSI),
						Users:       sets.NewString("default/p2/p2"),
					},
					"memory": {
						Allocatable: resource.NewQuantity(40*1024*1024*1024, resource.BinarySI),
						Available:   resource.NewQuantity(0, resource.BinarySI),
						Users:       sets.NewString("default/p2/p2"),
					},
					"nvidia.com/gpu": {
						Allocatable: resource.NewQuantity(2, resource.DecimalSI),
						Available:   resource.NewQuantity(0, resource.DecimalSI),
						Users:       sets.NewString("default/p2/p2"),
					},
				},
			},
			2: {
				socketId: 1,
				resourceStatuses: map[string]*ResourceStatus{
					"cpu": {
						Allocatable: resource.NewQuantity(24, resource.DecimalSI),
						Available:   resource.NewQuantity(24, resource.DecimalSI),
						Users:       sets.NewString(),
					},
					"memory": {
						Allocatable: resource.NewQuantity(40*1024*1024*1024, resource.BinarySI),
						Available:   resource.NewQuantity(40*1024*1024*1024, resource.BinarySI),
						Users:       sets.NewString(),
					},
					"nvidia.com/gpu": {
						Allocatable: resource.NewQuantity(2, resource.DecimalSI),
						Available:   resource.NewQuantity(2, resource.DecimalSI),
						Users:       sets.NewString(),
					},
				},
			},
			3: {
				socketId: 1,
				resourceStatuses: map[string]*ResourceStatus{
					"cpu": {
						Allocatable: resource.NewQuantity(24, resource.DecimalSI),
						Available:   resource.NewQuantity(24, resource.DecimalSI),
						Users:       sets.NewString(),
					},
					"memory": {
						Allocatable: resource.NewQuantity(40*1024*1024*1024, resource.BinarySI),
						Available:   resource.NewQuantity(40*1024*1024*1024, resource.BinarySI),
						Users:       sets.NewString(),
					},
					"nvidia.com/gpu": {
						Allocatable: resource.NewQuantity(2, resource.DecimalSI),
						Available:   resource.NewQuantity(2, resource.DecimalSI),
						Users:       sets.NewString(),
					},
				},
			},
		},
		requestsOfSharedCores: &Resource{
			MilliCPU: 8000,
			Memory:   20 * 1024 * 1024 * 1024,
			ScalarResources: map[v1.ResourceName]int64{
				util.ResourceGPU: 1,
			},
		},
		availableOfSharedCores: &Resource{
			MilliCPU: 48000,
			Memory:   80 * 1024 * 1024 * 1024,
		},
		socketToFreeNumasOfConflictResources: map[int]sets.Int{
			1: sets.NewInt(2, 3),
		},
	}
	topologyAfterPreemptP2 := &NumaTopologyStatus{
		podAllocations: map[string]*PodAllocation{
			"default/p1/p1": {
				agent: true,
				numaAllocations: map[int]*v1.ResourceList{
					0: parseResourceList(smallRes),
				},
			},
			"default/p2/p2": {
				agent: true,
				numaAllocations: map[int]*v1.ResourceList{
					1: parseResourceList(largeRes),
				},
			},
		},
		topology: map[int]*NumaStatus{
			0: {
				socketId: 0,
				resourceStatuses: map[string]*ResourceStatus{
					"cpu": {
						Allocatable: resource.NewQuantity(24, resource.DecimalSI),
						Available:   resource.NewQuantity(16, resource.DecimalSI),
						Users:       sets.NewString("default/p1/p1"),
					},
					"memory": {
						Allocatable: resource.NewQuantity(40*1024*1024*1024, resource.BinarySI),
						Available:   resource.NewQuantity(20*1024*1024*1024, resource.BinarySI),
						Users:       sets.NewString("default/p1/p1"),
					},
					"nvidia.com/gpu": {
						Allocatable: resource.NewQuantity(2, resource.DecimalSI),
						Available:   resource.NewQuantity(1, resource.DecimalSI),
						Users:       sets.NewString("default/p1/p1"),
					},
				},
			},
			1: {
				socketId: 0,
				resourceStatuses: map[string]*ResourceStatus{
					"cpu": {
						Allocatable: resource.NewQuantity(24, resource.DecimalSI),
						Available:   resource.NewQuantity(24, resource.DecimalSI),
					},
					"memory": {
						Allocatable: resource.NewQuantity(40*1024*1024*1024, resource.BinarySI),
						Available:   resource.NewQuantity(40*1024*1024*1024, resource.BinarySI),
					},
					"nvidia.com/gpu": {
						Allocatable: resource.NewQuantity(2, resource.DecimalSI),
						Available:   resource.NewQuantity(2, resource.DecimalSI),
					},
				},
			},
			2: {
				socketId: 1,
				resourceStatuses: map[string]*ResourceStatus{
					"cpu": {
						Allocatable: resource.NewQuantity(24, resource.DecimalSI),
						Available:   resource.NewQuantity(24, resource.DecimalSI),
					},
					"memory": {
						Allocatable: resource.NewQuantity(40*1024*1024*1024, resource.BinarySI),
						Available:   resource.NewQuantity(40*1024*1024*1024, resource.BinarySI),
					},
					"nvidia.com/gpu": {
						Allocatable: resource.NewQuantity(2, resource.DecimalSI),
						Available:   resource.NewQuantity(2, resource.DecimalSI),
					},
				},
			},
			3: {
				socketId: 1,
				resourceStatuses: map[string]*ResourceStatus{
					"cpu": {
						Allocatable: resource.NewQuantity(24, resource.DecimalSI),
						Available:   resource.NewQuantity(24, resource.DecimalSI),
					},
					"memory": {
						Allocatable: resource.NewQuantity(40*1024*1024*1024, resource.BinarySI),
						Available:   resource.NewQuantity(40*1024*1024*1024, resource.BinarySI),
					},
					"nvidia.com/gpu": {
						Allocatable: resource.NewQuantity(2, resource.DecimalSI),
						Available:   resource.NewQuantity(2, resource.DecimalSI),
					},
				},
			},
		},
		requestsOfSharedCores: &Resource{
			MilliCPU: 8000,
			Memory:   20 * 1024 * 1024 * 1024,
			ScalarResources: map[v1.ResourceName]int64{
				util.ResourceGPU: 1,
			},
		},
		availableOfSharedCores: &Resource{
			MilliCPU: 72000,
			Memory:   120 * 1024 * 1024 * 1024,
		},
		socketToFreeNumasOfConflictResources: map[int]sets.Int{
			0: sets.NewInt(1),
			1: sets.NewInt(2, 3),
		},
	}
	topologyAfterRemoveP2 := &NumaTopologyStatus{
		podAllocations: map[string]*PodAllocation{
			"default/p1/p1": {
				agent: true,
				numaAllocations: map[int]*v1.ResourceList{
					0: parseResourceList(smallRes),
				},
			},
			"default/p2/p2": {
				agent: true,
				numaAllocations: map[int]*v1.ResourceList{
					1: parseResourceList(largeRes),
				},
			},
		},
		topology: map[int]*NumaStatus{
			0: {
				socketId: 0,
				resourceStatuses: map[string]*ResourceStatus{
					"cpu": {
						Allocatable: resource.NewQuantity(24, resource.DecimalSI),
						Available:   resource.NewQuantity(16, resource.DecimalSI),
						Users:       sets.NewString("default/p1/p1"),
					},
					"memory": {
						Allocatable: resource.NewQuantity(40*1024*1024*1024, resource.BinarySI),
						Available:   resource.NewQuantity(20*1024*1024*1024, resource.BinarySI),
						Users:       sets.NewString("default/p1/p1"),
					},
					"nvidia.com/gpu": {
						Allocatable: resource.NewQuantity(2, resource.DecimalSI),
						Available:   resource.NewQuantity(1, resource.DecimalSI),
						Users:       sets.NewString("default/p1/p1"),
					},
				},
			},
			1: {
				socketId: 0,
				resourceStatuses: map[string]*ResourceStatus{
					"cpu": {
						Allocatable: resource.NewQuantity(24, resource.DecimalSI),
						Available:   resource.NewQuantity(0, resource.DecimalSI),
						Users:       sets.NewString("default/p2/p2"),
					},
					"memory": {
						Allocatable: resource.NewQuantity(40*1024*1024*1024, resource.BinarySI),
						Available:   resource.NewQuantity(0*1024*1024*1024, resource.BinarySI),
						Users:       sets.NewString("default/p2/p2"),
					},
					"nvidia.com/gpu": {
						Allocatable: resource.NewQuantity(2, resource.DecimalSI),
						Available:   resource.NewQuantity(0, resource.DecimalSI),
						Users:       sets.NewString("default/p2/p2"),
					},
				},
			},
			2: {
				socketId: 1,
				resourceStatuses: map[string]*ResourceStatus{
					"cpu": {
						Allocatable: resource.NewQuantity(24, resource.DecimalSI),
						Available:   resource.NewQuantity(24, resource.DecimalSI),
					},
					"memory": {
						Allocatable: resource.NewQuantity(40*1024*1024*1024, resource.BinarySI),
						Available:   resource.NewQuantity(40*1024*1024*1024, resource.BinarySI),
					},
					"nvidia.com/gpu": {
						Allocatable: resource.NewQuantity(2, resource.DecimalSI),
						Available:   resource.NewQuantity(2, resource.DecimalSI),
					},
				},
			},
			3: {
				socketId: 1,
				resourceStatuses: map[string]*ResourceStatus{
					"cpu": {
						Allocatable: resource.NewQuantity(24, resource.DecimalSI),
						Available:   resource.NewQuantity(24, resource.DecimalSI),
					},
					"memory": {
						Allocatable: resource.NewQuantity(40*1024*1024*1024, resource.BinarySI),
						Available:   resource.NewQuantity(40*1024*1024*1024, resource.BinarySI),
					},
					"nvidia.com/gpu": {
						Allocatable: resource.NewQuantity(2, resource.DecimalSI),
						Available:   resource.NewQuantity(2, resource.DecimalSI),
					},
				},
			},
		},
		requestsOfSharedCores: &Resource{
			MilliCPU: 8000,
			Memory:   20 * 1024 * 1024 * 1024,
			ScalarResources: map[v1.ResourceName]int64{
				util.ResourceGPU: 1,
			},
		},
		availableOfSharedCores: &Resource{
			MilliCPU: 48000,
			Memory:   80 * 1024 * 1024 * 1024,
		},
		socketToFreeNumasOfConflictResources: map[int]sets.Int{
			1: sets.NewInt(2, 3),
		},
	}
	topologyAfterPreemptP3 := &NumaTopologyStatus{
		podAllocations: map[string]*PodAllocation{
			"default/p1/p1": {
				agent: true,
				numaAllocations: map[int]*v1.ResourceList{
					0: parseResourceList(smallRes),
				},
			},
			"default/p2/p2": {
				agent: true,
				numaAllocations: map[int]*v1.ResourceList{
					1: parseResourceList(largeRes),
				},
			},
		},
		topology: map[int]*NumaStatus{
			0: {
				socketId: 0,
				resourceStatuses: map[string]*ResourceStatus{
					"cpu": {
						Allocatable: resource.NewQuantity(24, resource.DecimalSI),
						Available:   resource.NewQuantity(16, resource.DecimalSI),
						Users:       sets.NewString("default/p1/p1"),
					},
					"memory": {
						Allocatable: resource.NewQuantity(40*1024*1024*1024, resource.BinarySI),
						Available:   resource.NewQuantity(20*1024*1024*1024, resource.BinarySI),
						Users:       sets.NewString("default/p1/p1"),
					},
					"nvidia.com/gpu": {
						Allocatable: resource.NewQuantity(2, resource.DecimalSI),
						Available:   resource.NewQuantity(1, resource.DecimalSI),
						Users:       sets.NewString("default/p1/p1"),
					},
				},
			},
			1: {
				socketId: 0,
				resourceStatuses: map[string]*ResourceStatus{
					"cpu": {
						Allocatable: resource.NewQuantity(24, resource.DecimalSI),
						Available:   resource.NewQuantity(0, resource.DecimalSI),
						Users:       sets.NewString("default/p2/p2"),
					},
					"memory": {
						Allocatable: resource.NewQuantity(40*1024*1024*1024, resource.BinarySI),
						Available:   resource.NewQuantity(0, resource.BinarySI),
						Users:       sets.NewString("default/p2/p2"),
					},
					"nvidia.com/gpu": {
						Allocatable: resource.NewQuantity(2, resource.DecimalSI),
						Available:   resource.NewQuantity(0, resource.DecimalSI),
						Users:       sets.NewString("default/p2/p2"),
					},
				},
			},
			2: {
				socketId: 1,
				resourceStatuses: map[string]*ResourceStatus{
					"cpu": {
						Allocatable: resource.NewQuantity(24, resource.DecimalSI),
						Available:   resource.NewQuantity(24, resource.DecimalSI),
						Users:       sets.NewString(),
					},
					"memory": {
						Allocatable: resource.NewQuantity(40*1024*1024*1024, resource.BinarySI),
						Available:   resource.NewQuantity(40*1024*1024*1024, resource.BinarySI),
						Users:       sets.NewString(),
					},
					"nvidia.com/gpu": {
						Allocatable: resource.NewQuantity(2, resource.DecimalSI),
						Available:   resource.NewQuantity(2, resource.DecimalSI),
						Users:       sets.NewString(),
					},
				},
			},
			3: {
				socketId: 1,
				resourceStatuses: map[string]*ResourceStatus{
					"cpu": {
						Allocatable: resource.NewQuantity(24, resource.DecimalSI),
						Available:   resource.NewQuantity(24, resource.DecimalSI),
						Users:       sets.NewString(),
					},
					"memory": {
						Allocatable: resource.NewQuantity(40*1024*1024*1024, resource.BinarySI),
						Available:   resource.NewQuantity(40*1024*1024*1024, resource.BinarySI),
						Users:       sets.NewString(),
					},
					"nvidia.com/gpu": {
						Allocatable: resource.NewQuantity(2, resource.DecimalSI),
						Available:   resource.NewQuantity(2, resource.DecimalSI),
						Users:       sets.NewString(),
					},
				},
			},
		},
		requestsOfSharedCores: &Resource{},
		availableOfSharedCores: &Resource{
			MilliCPU: 48000,
			Memory:   80 * 1024 * 1024 * 1024,
		},
		socketToFreeNumasOfConflictResources: map[int]sets.Int{
			1: sets.NewInt(2, 3),
		},
	}
	topologyAfterRemoveP3 := &NumaTopologyStatus{
		podAllocations: map[string]*PodAllocation{
			"default/p1/p1": {
				agent: true,
				numaAllocations: map[int]*v1.ResourceList{
					0: parseResourceList(smallRes),
				},
			},
			"default/p2/p2": {
				agent: true,
				numaAllocations: map[int]*v1.ResourceList{
					1: parseResourceList(largeRes),
				},
			},
		},
		topology: map[int]*NumaStatus{
			0: {
				socketId: 0,
				resourceStatuses: map[string]*ResourceStatus{
					"cpu": {
						Allocatable: resource.NewQuantity(24, resource.DecimalSI),
						Available:   resource.NewQuantity(16, resource.DecimalSI),
						Users:       sets.NewString("default/p1/p1"),
					},
					"memory": {
						Allocatable: resource.NewQuantity(40*1024*1024*1024, resource.BinarySI),
						Available:   resource.NewQuantity(20*1024*1024*1024, resource.BinarySI),
						Users:       sets.NewString("default/p1/p1"),
					},
					"nvidia.com/gpu": {
						Allocatable: resource.NewQuantity(2, resource.DecimalSI),
						Available:   resource.NewQuantity(1, resource.DecimalSI),
						Users:       sets.NewString("default/p1/p1"),
					},
				},
			},
			1: {
				socketId: 0,
				resourceStatuses: map[string]*ResourceStatus{
					"cpu": {
						Allocatable: resource.NewQuantity(24, resource.DecimalSI),
						Available:   resource.NewQuantity(0, resource.DecimalSI),
						Users:       sets.NewString("default/p2/p2"),
					},
					"memory": {
						Allocatable: resource.NewQuantity(40*1024*1024*1024, resource.BinarySI),
						Available:   resource.NewQuantity(0, resource.BinarySI),
						Users:       sets.NewString("default/p2/p2"),
					},
					"nvidia.com/gpu": {
						Allocatable: resource.NewQuantity(2, resource.DecimalSI),
						Available:   resource.NewQuantity(0, resource.DecimalSI),
						Users:       sets.NewString("default/p2/p2"),
					},
				},
			},
			2: {
				socketId: 1,
				resourceStatuses: map[string]*ResourceStatus{
					"cpu": {
						Allocatable: resource.NewQuantity(24, resource.DecimalSI),
						Available:   resource.NewQuantity(24, resource.DecimalSI),
						Users:       sets.NewString(),
					},
					"memory": {
						Allocatable: resource.NewQuantity(40*1024*1024*1024, resource.BinarySI),
						Available:   resource.NewQuantity(40*1024*1024*1024, resource.BinarySI),
						Users:       sets.NewString(),
					},
					"nvidia.com/gpu": {
						Allocatable: resource.NewQuantity(2, resource.DecimalSI),
						Available:   resource.NewQuantity(2, resource.DecimalSI),
						Users:       sets.NewString(),
					},
				},
			},
			3: {
				socketId: 1,
				resourceStatuses: map[string]*ResourceStatus{
					"cpu": {
						Allocatable: resource.NewQuantity(24, resource.DecimalSI),
						Available:   resource.NewQuantity(24, resource.DecimalSI),
						Users:       sets.NewString(),
					},
					"memory": {
						Allocatable: resource.NewQuantity(40*1024*1024*1024, resource.BinarySI),
						Available:   resource.NewQuantity(40*1024*1024*1024, resource.BinarySI),
						Users:       sets.NewString(),
					},
					"nvidia.com/gpu": {
						Allocatable: resource.NewQuantity(2, resource.DecimalSI),
						Available:   resource.NewQuantity(2, resource.DecimalSI),
						Users:       sets.NewString(),
					},
				},
			},
		},
		requestsOfSharedCores: &Resource{},
		availableOfSharedCores: &Resource{
			MilliCPU: 48000,
			Memory:   80 * 1024 * 1024 * 1024,
		},
		socketToFreeNumasOfConflictResources: map[int]sets.Int{
			1: sets.NewInt(2, 3),
		},
	}

	tests := []struct {
		name             string
		pod              *v1.Pod
		preempt          bool
		expectedTopology *NumaTopologyStatus
	}{
		{
			name:             "for dedicated cores pod, preempt",
			pod:              setPod("p1", smallRes, map[string]string{util.QoSLevelKey: string(util.DedicatedCores), util.MemoyEnhancementKey: "{\"numa_binding\":\"true\",\"numa_exclusive\":\"false\"}"}),
			expectedTopology: topologyAfterPreemptP1,
			preempt:          true,
		},
		{
			name:             "for dedicated cores pod, not preempt",
			pod:              setPod("p1", smallRes, map[string]string{util.QoSLevelKey: string(util.DedicatedCores), util.MemoyEnhancementKey: "{\"numa_binding\":\"true\",\"numa_exclusive\":\"false\"}"}),
			expectedTopology: topologyAfterRemoveP1,
			preempt:          false,
		},
		{
			name:             "for exclusive pod, preempt",
			pod:              setPod("p2", largeRes, map[string]string{util.QoSLevelKey: string(util.DedicatedCores), util.MemoyEnhancementKey: "{\"numa_binding\":\"true\",\"numa_exclusive\":\"true\"}"}),
			expectedTopology: topologyAfterPreemptP2,
			preempt:          true,
		},
		{
			name:             "for exclusive pod, not preempt",
			pod:              setPod("p2", largeRes, map[string]string{util.QoSLevelKey: string(util.DedicatedCores), util.MemoyEnhancementKey: "{\"numa_binding\":\"true\",\"numa_exclusive\":\"true\"}"}),
			expectedTopology: topologyAfterRemoveP2,
			preempt:          false,
		},
		{
			name:             "for shared cores pod, preempt",
			pod:              setPod("p3", smallRes, nil),
			expectedTopology: topologyAfterPreemptP3,
			preempt:          true,
		},
		{
			name:             "for shared cores pod, not preempt",
			pod:              setPod("p3", smallRes, nil),
			expectedTopology: topologyAfterRemoveP3,
			preempt:          false,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ni := fakeNodeInfo(
				setPod("p1", smallRes, map[string]string{util.QoSLevelKey: string(util.DedicatedCores), util.MemoyEnhancementKey: "{\"numa_binding\":\"true\",\"numa_exclusive\":\"false\"}"}),
				setPod("p2", largeRes, map[string]string{util.QoSLevelKey: string(util.DedicatedCores), util.MemoyEnhancementKey: "{\"numa_binding\":\"true\",\"numa_exclusive\":\"true\"}"}),
				setPod("p3", smallRes, nil),
			)
			ni.SetCNR(cnr)
			if !numaTopologyStatusEqual(numaTopologyStatus, ni.GetNumaTopologyStatus()) {
				t.Errorf("expected 1: %#v, got: %#v", numaTopologyStatus, ni.GetNumaTopologyStatus())
			}
			ni.RemovePod(test.pod, test.preempt)
			if !numaTopologyStatusEqual(test.expectedTopology, ni.GetNumaTopologyStatus()) {
				t.Errorf("expected 2: %#v, got: %#v", test.expectedTopology, ni.GetNumaTopologyStatus())
			}
			ni.AddPod(test.pod)
			if !numaTopologyStatusEqual(numaTopologyStatus, ni.GetNumaTopologyStatus()) {
				t.Errorf("expected 3: %#v, got: %#v", numaTopologyStatus, ni.GetNumaTopologyStatus())
			}
		})
	}
}

func TestNonNativeInNodeInfoOnlyPodInfo(t *testing.T) {
	utilfeature.DefaultMutableFeatureGate.SetFromMap(map[string]bool{string(godelfeatures.NonNativeResourceSchedulingSupport): true})

	setPod := func(name string, res map[v1.ResourceName]string, anns map[string]string) *v1.Pod {
		return &v1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Namespace:   "default",
				Name:        name,
				UID:         types.UID(name),
				Annotations: anns,
			},
			Spec: v1.PodSpec{
				Containers: []v1.Container{
					{
						Resources: v1.ResourceRequirements{
							Requests: *parseResourceList(res),
						},
					},
				},
			},
		}
	}

	cnr := &katalystv1alpha1.CustomNodeResource{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-node",
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
								Allocatable: parseResourceList(largeRes),
								Capacity:    parseResourceList(largeRes),
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
							Name: "1",
							Resources: katalystv1alpha1.Resources{
								Allocatable: parseResourceList(largeRes),
								Capacity:    parseResourceList(largeRes),
							},
						},
					},
				},
			},
		},
	}

	numaTopologyStatus := &NumaTopologyStatus{
		podAllocations: map[string]*PodAllocation{},
		topology: map[int]*NumaStatus{
			0: {
				socketId: 0,
				resourceStatuses: map[string]*ResourceStatus{
					"cpu": {
						Allocatable: resource.NewQuantity(24, resource.DecimalSI),
						Available:   resource.NewQuantity(24, resource.DecimalSI),
					},
					"memory": {
						Allocatable: resource.NewQuantity(40*1024*1024*1024, resource.BinarySI),
						Available:   resource.NewQuantity(40*1024*1024*1024, resource.BinarySI),
					},
					"nvidia.com/gpu": {
						Allocatable: resource.NewQuantity(2, resource.DecimalSI),
						Available:   resource.NewQuantity(2, resource.DecimalSI),
					},
				},
			},
			1: {
				socketId: 1,
				resourceStatuses: map[string]*ResourceStatus{
					"cpu": {
						Allocatable: resource.NewQuantity(24, resource.DecimalSI),
						Available:   resource.NewQuantity(24, resource.DecimalSI),
					},
					"memory": {
						Allocatable: resource.NewQuantity(40*1024*1024*1024, resource.BinarySI),
						Available:   resource.NewQuantity(40*1024*1024*1024, resource.BinarySI),
					},
					"nvidia.com/gpu": {
						Allocatable: resource.NewQuantity(2, resource.DecimalSI),
						Available:   resource.NewQuantity(2, resource.DecimalSI),
					},
				},
			},
		},
		requestsOfSharedCores: &Resource{},
		availableOfSharedCores: &Resource{
			MilliCPU: 48000,
			Memory:   80 * 1024 * 1024 * 1024,
		},
		socketToFreeNumasOfConflictResources: map[int]sets.Int{
			0: sets.NewInt(0),
			1: sets.NewInt(1),
		},
	}

	topologyAfterAddP1 := &NumaTopologyStatus{
		podAllocations: map[string]*PodAllocation{
			"default/p1/p1": {
				numaAllocations: map[int]*v1.ResourceList{
					0: parseResourceList(onlyGPU),
				},
			},
		},
		topology: map[int]*NumaStatus{
			0: {
				socketId: 0,
				resourceStatuses: map[string]*ResourceStatus{
					"cpu": {
						Allocatable: resource.NewQuantity(24, resource.DecimalSI),
						Available:   resource.NewQuantity(24, resource.DecimalSI),
					},
					"memory": {
						Allocatable: resource.NewQuantity(40*1024*1024*1024, resource.BinarySI),
						Available:   resource.NewQuantity(40*1024*1024*1024, resource.BinarySI),
					},
					"nvidia.com/gpu": {
						Allocatable: resource.NewQuantity(2, resource.DecimalSI),
						Available:   resource.NewQuantity(1, resource.DecimalSI),
						Users:       sets.NewString("default/p1/p1"),
					},
				},
			},
			1: {
				socketId: 1,
				resourceStatuses: map[string]*ResourceStatus{
					"cpu": {
						Allocatable: resource.NewQuantity(24, resource.DecimalSI),
						Available:   resource.NewQuantity(24, resource.DecimalSI),
					},
					"memory": {
						Allocatable: resource.NewQuantity(40*1024*1024*1024, resource.BinarySI),
						Available:   resource.NewQuantity(40*1024*1024*1024, resource.BinarySI),
					},
					"nvidia.com/gpu": {
						Allocatable: resource.NewQuantity(2, resource.DecimalSI),
						Available:   resource.NewQuantity(2, resource.DecimalSI),
					},
				},
			},
		},
		requestsOfSharedCores: &Resource{
			MilliCPU: 8000,
			Memory:   20 * 1024 * 1024 * 1024,
			ScalarResources: map[v1.ResourceName]int64{
				util.ResourceGPU: 1,
			},
		},
		availableOfSharedCores: &Resource{
			MilliCPU: 48000,
			Memory:   80 * 1024 * 1024 * 1024,
		},
		socketToFreeNumasOfConflictResources: map[int]sets.Int{
			0: sets.NewInt(0),
			1: sets.NewInt(1),
		},
	}

	topologyAfterRemoveP1 := &NumaTopologyStatus{
		podAllocations: map[string]*PodAllocation{},
		topology: map[int]*NumaStatus{
			0: {
				socketId: 0,
				resourceStatuses: map[string]*ResourceStatus{
					"cpu": {
						Allocatable: resource.NewQuantity(24, resource.DecimalSI),
						Available:   resource.NewQuantity(24, resource.DecimalSI),
					},
					"memory": {
						Allocatable: resource.NewQuantity(40*1024*1024*1024, resource.BinarySI),
						Available:   resource.NewQuantity(40*1024*1024*1024, resource.BinarySI),
					},
					"nvidia.com/gpu": {
						Allocatable: resource.NewQuantity(2, resource.DecimalSI),
						Available:   resource.NewQuantity(2, resource.DecimalSI),
					},
				},
			},
			1: {
				socketId: 1,
				resourceStatuses: map[string]*ResourceStatus{
					"cpu": {
						Allocatable: resource.NewQuantity(24, resource.DecimalSI),
						Available:   resource.NewQuantity(24, resource.DecimalSI),
					},
					"memory": {
						Allocatable: resource.NewQuantity(40*1024*1024*1024, resource.BinarySI),
						Available:   resource.NewQuantity(40*1024*1024*1024, resource.BinarySI),
					},
					"nvidia.com/gpu": {
						Allocatable: resource.NewQuantity(2, resource.DecimalSI),
						Available:   resource.NewQuantity(2, resource.DecimalSI),
					},
				},
			},
		},
		requestsOfSharedCores: &Resource{},
		availableOfSharedCores: &Resource{
			MilliCPU: 48000,
			Memory:   80 * 1024 * 1024 * 1024,
		},
		socketToFreeNumasOfConflictResources: map[int]sets.Int{
			0: sets.NewInt(0),
			1: sets.NewInt(1),
		},
	}

	topologyAfterPreemptP1 := &NumaTopologyStatus{
		podAllocations: map[string]*PodAllocation{
			"default/p1/p1": {
				numaAllocations: map[int]*v1.ResourceList{
					0: parseResourceList(onlyGPU),
				},
			},
		},
		topology: map[int]*NumaStatus{
			0: {
				socketId: 0,
				resourceStatuses: map[string]*ResourceStatus{
					"cpu": {
						Allocatable: resource.NewQuantity(24, resource.DecimalSI),
						Available:   resource.NewQuantity(24, resource.DecimalSI),
					},
					"memory": {
						Allocatable: resource.NewQuantity(40*1024*1024*1024, resource.BinarySI),
						Available:   resource.NewQuantity(40*1024*1024*1024, resource.BinarySI),
					},
					"nvidia.com/gpu": {
						Allocatable: resource.NewQuantity(2, resource.DecimalSI),
						Available:   resource.NewQuantity(2, resource.DecimalSI),
					},
				},
			},
			1: {
				socketId: 1,
				resourceStatuses: map[string]*ResourceStatus{
					"cpu": {
						Allocatable: resource.NewQuantity(24, resource.DecimalSI),
						Available:   resource.NewQuantity(24, resource.DecimalSI),
					},
					"memory": {
						Allocatable: resource.NewQuantity(40*1024*1024*1024, resource.BinarySI),
						Available:   resource.NewQuantity(40*1024*1024*1024, resource.BinarySI),
					},
					"nvidia.com/gpu": {
						Allocatable: resource.NewQuantity(2, resource.DecimalSI),
						Available:   resource.NewQuantity(2, resource.DecimalSI),
					},
				},
			},
		},
		requestsOfSharedCores: &Resource{},
		availableOfSharedCores: &Resource{
			MilliCPU: 48000,
			Memory:   80 * 1024 * 1024 * 1024,
		},
		socketToFreeNumasOfConflictResources: map[int]sets.Int{
			0: sets.NewInt(0),
			1: sets.NewInt(1),
		},
	}

	topologyAfterAddP2 := &NumaTopologyStatus{
		podAllocations: map[string]*PodAllocation{
			"default/p2/p2": {
				numaAllocations: map[int]*v1.ResourceList{
					0: parseResourceList(largeRes),
				},
			},
		},
		topology: map[int]*NumaStatus{
			0: {
				socketId: 0,
				resourceStatuses: map[string]*ResourceStatus{
					"cpu": {
						Allocatable: resource.NewQuantity(24, resource.DecimalSI),
						Available:   resource.NewQuantity(0, resource.DecimalSI),
						Users:       sets.NewString("default/p2/p2"),
					},
					"memory": {
						Allocatable: resource.NewQuantity(40*1024*1024*1024, resource.BinarySI),
						Available:   resource.NewQuantity(0*1024*1024*1024, resource.BinarySI),
						Users:       sets.NewString("default/p2/p2"),
					},
					"nvidia.com/gpu": {
						Allocatable: resource.NewQuantity(2, resource.DecimalSI),
						Available:   resource.NewQuantity(0, resource.DecimalSI),
						Users:       sets.NewString("default/p2/p2"),
					},
				},
			},
			1: {
				socketId: 1,
				resourceStatuses: map[string]*ResourceStatus{
					"cpu": {
						Allocatable: resource.NewQuantity(24, resource.DecimalSI),
						Available:   resource.NewQuantity(24, resource.DecimalSI),
					},
					"memory": {
						Allocatable: resource.NewQuantity(40*1024*1024*1024, resource.BinarySI),
						Available:   resource.NewQuantity(40*1024*1024*1024, resource.BinarySI),
					},
					"nvidia.com/gpu": {
						Allocatable: resource.NewQuantity(2, resource.DecimalSI),
						Available:   resource.NewQuantity(2, resource.DecimalSI),
					},
				},
			},
		},
		requestsOfSharedCores: &Resource{},
		availableOfSharedCores: &Resource{
			MilliCPU: 24000,
			Memory:   40 * 1024 * 1024 * 1024,
		},
		socketToFreeNumasOfConflictResources: map[int]sets.Int{
			1: sets.NewInt(1),
		},
	}

	topologyAfterPreemptP2 := &NumaTopologyStatus{
		podAllocations: map[string]*PodAllocation{
			"default/p2/p2": {
				numaAllocations: map[int]*v1.ResourceList{
					0: parseResourceList(largeRes),
				},
			},
		},
		topology: map[int]*NumaStatus{
			0: {
				socketId: 0,
				resourceStatuses: map[string]*ResourceStatus{
					"cpu": {
						Allocatable: resource.NewQuantity(24, resource.DecimalSI),
						Available:   resource.NewQuantity(24, resource.DecimalSI),
					},
					"memory": {
						Allocatable: resource.NewQuantity(40*1024*1024*1024, resource.BinarySI),
						Available:   resource.NewQuantity(40*1024*1024*1024, resource.BinarySI),
					},
					"nvidia.com/gpu": {
						Allocatable: resource.NewQuantity(2, resource.DecimalSI),
						Available:   resource.NewQuantity(2, resource.DecimalSI),
					},
				},
			},
			1: {
				socketId: 1,
				resourceStatuses: map[string]*ResourceStatus{
					"cpu": {
						Allocatable: resource.NewQuantity(24, resource.DecimalSI),
						Available:   resource.NewQuantity(24, resource.DecimalSI),
					},
					"memory": {
						Allocatable: resource.NewQuantity(40*1024*1024*1024, resource.BinarySI),
						Available:   resource.NewQuantity(40*1024*1024*1024, resource.BinarySI),
					},
					"nvidia.com/gpu": {
						Allocatable: resource.NewQuantity(2, resource.DecimalSI),
						Available:   resource.NewQuantity(2, resource.DecimalSI),
					},
				},
			},
		},
		requestsOfSharedCores: &Resource{},
		availableOfSharedCores: &Resource{
			MilliCPU: 48000,
			Memory:   80 * 1024 * 1024 * 1024,
		},
		socketToFreeNumasOfConflictResources: map[int]sets.Int{
			0: sets.NewInt(0),
			1: sets.NewInt(1),
		},
	}

	topologyAfterAddP3 := &NumaTopologyStatus{
		podAllocations: map[string]*PodAllocation{
			"default/p3/p3": {
				numaAllocations: map[int]*v1.ResourceList{
					0: parseResourceList(smallRes),
				},
			},
		},
		topology: map[int]*NumaStatus{
			0: {
				socketId: 0,
				resourceStatuses: map[string]*ResourceStatus{
					"cpu": {
						Allocatable: resource.NewQuantity(24, resource.DecimalSI),
						Available:   resource.NewQuantity(16, resource.DecimalSI),
						Users:       sets.NewString("default/p3/p3"),
					},
					"memory": {
						Allocatable: resource.NewQuantity(40*1024*1024*1024, resource.BinarySI),
						Available:   resource.NewQuantity(20*1024*1024*1024, resource.BinarySI),
						Users:       sets.NewString("default/p3/p3"),
					},
					"nvidia.com/gpu": {
						Allocatable: resource.NewQuantity(2, resource.DecimalSI),
						Available:   resource.NewQuantity(1, resource.DecimalSI),
						Users:       sets.NewString("default/p3/p3"),
					},
				},
			},
			1: {
				socketId: 1,
				resourceStatuses: map[string]*ResourceStatus{
					"cpu": {
						Allocatable: resource.NewQuantity(24, resource.DecimalSI),
						Available:   resource.NewQuantity(24, resource.DecimalSI),
					},
					"memory": {
						Allocatable: resource.NewQuantity(40*1024*1024*1024, resource.BinarySI),
						Available:   resource.NewQuantity(40*1024*1024*1024, resource.BinarySI),
					},
					"nvidia.com/gpu": {
						Allocatable: resource.NewQuantity(2, resource.DecimalSI),
						Available:   resource.NewQuantity(2, resource.DecimalSI),
					},
				},
			},
		},
		requestsOfSharedCores: &Resource{},
		availableOfSharedCores: &Resource{
			MilliCPU: 24000,
			Memory:   40 * 1024 * 1024 * 1024,
		},
		socketToFreeNumasOfConflictResources: map[int]sets.Int{
			1: sets.NewInt(1),
		},
	}

	topologyAfterPreemptP3 := &NumaTopologyStatus{
		podAllocations: map[string]*PodAllocation{
			"default/p3/p3": {
				numaAllocations: map[int]*v1.ResourceList{
					0: parseResourceList(smallRes),
				},
			},
		},
		topology: map[int]*NumaStatus{
			0: {
				socketId: 0,
				resourceStatuses: map[string]*ResourceStatus{
					"cpu": {
						Allocatable: resource.NewQuantity(24, resource.DecimalSI),
						Available:   resource.NewQuantity(24, resource.DecimalSI),
					},
					"memory": {
						Allocatable: resource.NewQuantity(40*1024*1024*1024, resource.BinarySI),
						Available:   resource.NewQuantity(40*1024*1024*1024, resource.BinarySI),
					},
					"nvidia.com/gpu": {
						Allocatable: resource.NewQuantity(2, resource.DecimalSI),
						Available:   resource.NewQuantity(2, resource.DecimalSI),
					},
				},
			},
			1: {
				socketId: 1,
				resourceStatuses: map[string]*ResourceStatus{
					"cpu": {
						Allocatable: resource.NewQuantity(24, resource.DecimalSI),
						Available:   resource.NewQuantity(24, resource.DecimalSI),
					},
					"memory": {
						Allocatable: resource.NewQuantity(40*1024*1024*1024, resource.BinarySI),
						Available:   resource.NewQuantity(40*1024*1024*1024, resource.BinarySI),
					},
					"nvidia.com/gpu": {
						Allocatable: resource.NewQuantity(2, resource.DecimalSI),
						Available:   resource.NewQuantity(2, resource.DecimalSI),
					},
				},
			},
		},
		requestsOfSharedCores: &Resource{},
		availableOfSharedCores: &Resource{
			MilliCPU: 48000,
			Memory:   80 * 1024 * 1024 * 1024,
		},
		socketToFreeNumasOfConflictResources: map[int]sets.Int{
			0: sets.NewInt(0),
			1: sets.NewInt(1),
		},
	}

	tests := []struct {
		name             string
		pod              *v1.Pod
		expectedTopology []*NumaTopologyStatus
		preempt          bool
	}{
		{
			name:             "for shared cores pod, not preempt",
			pod:              setPod("p1", smallRes, map[string]string{podutil.MicroTopologyKey: "0:nvidia.com/gpu=1"}),
			expectedTopology: []*NumaTopologyStatus{topologyAfterAddP1, topologyAfterRemoveP1},
		},
		{
			name:             "for shared cores pod, preempt",
			pod:              setPod("p1", smallRes, map[string]string{podutil.MicroTopologyKey: "0:nvidia.com/gpu=1"}),
			expectedTopology: []*NumaTopologyStatus{topologyAfterAddP1, topologyAfterPreemptP1},
			preempt:          true,
		},
		{
			name:             "for exclusive pod, not preempt",
			pod:              setPod("p2", largeRes, map[string]string{podutil.MicroTopologyKey: "0:cpu=24,memory=40Gi,nvidia.com/gpu=2", util.QoSLevelKey: string(util.DedicatedCores), util.MemoyEnhancementKey: "{\"numa_binding\":\"true\",\"numa_exclusive\":\"true\"}"}),
			expectedTopology: []*NumaTopologyStatus{topologyAfterAddP2, numaTopologyStatus},
		},
		{
			name:             "for exclusive pod, preempt",
			pod:              setPod("p2", largeRes, map[string]string{podutil.MicroTopologyKey: "0:cpu=24,memory=40Gi,nvidia.com/gpu=2", util.QoSLevelKey: string(util.DedicatedCores), util.MemoyEnhancementKey: "{\"numa_binding\":\"true\",\"numa_exclusive\":\"true\"}"}),
			expectedTopology: []*NumaTopologyStatus{topologyAfterAddP2, topologyAfterPreemptP2},
			preempt:          true,
		},
		{
			name:             "for dedicated cores pod, not preempt",
			pod:              setPod("p3", smallRes, map[string]string{podutil.MicroTopologyKey: "0:cpu=8,memory=20Gi,nvidia.com/gpu=1", util.QoSLevelKey: string(util.DedicatedCores), util.MemoyEnhancementKey: "{\"numa_binding\":\"true\",\"numa_exclusive\":\"false\"}"}),
			expectedTopology: []*NumaTopologyStatus{topologyAfterAddP3, numaTopologyStatus},
			preempt:          false,
		},
		{
			name:             "for dedicated cores pod, preempt",
			pod:              setPod("p3", smallRes, map[string]string{podutil.MicroTopologyKey: "0:cpu=8,memory=20Gi,nvidia.com/gpu=1", util.QoSLevelKey: string(util.DedicatedCores), util.MemoyEnhancementKey: "{\"numa_binding\":\"true\",\"numa_exclusive\":\"false\"}"}),
			expectedTopology: []*NumaTopologyStatus{topologyAfterAddP3, topologyAfterPreemptP3},
			preempt:          true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ni := fakeNodeInfo()
			ni.SetCNR(cnr)
			if !numaTopologyStatusEqual(numaTopologyStatus, ni.GetNumaTopologyStatus()) {
				t.Errorf("expected 1: %#v, got: %#v", numaTopologyStatus, ni.GetNumaTopologyStatus())
			}
			ni.AddPod(test.pod)
			if !numaTopologyStatusEqual(test.expectedTopology[0], ni.GetNumaTopologyStatus()) {
				t.Errorf("expected 2: %#v, got: %#v", test.expectedTopology[0], ni.GetNumaTopologyStatus())
			}
			ni.RemovePod(test.pod, test.preempt)
			if !numaTopologyStatusEqual(test.expectedTopology[1], ni.GetNumaTopologyStatus()) {
				t.Errorf("expected 3: %#v, got: %#v", test.expectedTopology[1], ni.GetNumaTopologyStatus())
			}
		})
	}
}

func TestNonNativeInNodeInfoBothAgentAndPodInfo(t *testing.T) {
	utilfeature.DefaultMutableFeatureGate.SetFromMap(map[string]bool{string(godelfeatures.NonNativeResourceSchedulingSupport): true})

	setPod := func(name string, res map[v1.ResourceName]string, anns map[string]string) *v1.Pod {
		return &v1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Namespace:   "default",
				Name:        name,
				UID:         types.UID(name),
				Annotations: anns,
			},
			Spec: v1.PodSpec{
				Containers: []v1.Container{
					{
						Resources: v1.ResourceRequirements{
							Requests: *parseResourceList(res),
						},
					},
				},
			},
		}
	}

	cnrNoPod := &katalystv1alpha1.CustomNodeResource{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-node",
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
								Allocatable: parseResourceList(largeRes),
								Capacity:    parseResourceList(largeRes),
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
							Name: "1",
							Resources: katalystv1alpha1.Resources{
								Allocatable: parseResourceList(largeRes),
								Capacity:    parseResourceList(largeRes),
							},
						},
					},
				},
			},
		},
	}

	cnrHasSharedCoresPod := &katalystv1alpha1.CustomNodeResource{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-node",
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
								Allocatable: parseResourceList(largeRes),
								Capacity:    parseResourceList(largeRes),
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
							Name: "1",
							Resources: katalystv1alpha1.Resources{
								Allocatable: parseResourceList(largeRes),
								Capacity:    parseResourceList(largeRes),
							},
							Allocations: []*katalystv1alpha1.Allocation{
								{
									Consumer: "default/p1/p1",
									Requests: parseResourceList(onlyGPU),
								},
							},
						},
					},
				},
			},
		},
	}

	cnrHasDedicatedCoresPod := &katalystv1alpha1.CustomNodeResource{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-node",
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
								Allocatable: parseResourceList(largeRes),
								Capacity:    parseResourceList(largeRes),
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
							Name: "1",
							Resources: katalystv1alpha1.Resources{
								Allocatable: parseResourceList(largeRes),
								Capacity:    parseResourceList(largeRes),
							},
							Allocations: []*katalystv1alpha1.Allocation{
								{
									Consumer: "default/p3/p3",
									Requests: parseResourceList(smallRes),
								},
							},
						},
					},
				},
			},
		},
	}

	cnrHasSocketPod := &katalystv1alpha1.CustomNodeResource{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-node",
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
								Allocatable: parseResourceList(largeRes),
								Capacity:    parseResourceList(largeRes),
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
							Name: "1",
							Resources: katalystv1alpha1.Resources{
								Allocatable: parseResourceList(largeRes),
								Capacity:    parseResourceList(largeRes),
							},
							Allocations: []*katalystv1alpha1.Allocation{
								{
									Consumer: "default/p2/p2",
									Requests: parseResourceList(largeRes),
								},
							},
						},
					},
				},
			},
		},
	}

	numaTopologyStatus := &NumaTopologyStatus{
		topology: map[int]*NumaStatus{
			0: {
				socketId: 0,
				resourceStatuses: map[string]*ResourceStatus{
					"cpu": {
						Allocatable: resource.NewQuantity(24, resource.DecimalSI),
						Available:   resource.NewQuantity(24, resource.DecimalSI),
					},
					"memory": {
						Allocatable: resource.NewQuantity(40*1024*1024*1024, resource.BinarySI),
						Available:   resource.NewQuantity(40*1024*1024*1024, resource.BinarySI),
					},
					"nvidia.com/gpu": {
						Allocatable: resource.NewQuantity(2, resource.DecimalSI),
						Available:   resource.NewQuantity(2, resource.DecimalSI),
					},
				},
			},
			1: {
				socketId: 1,
				resourceStatuses: map[string]*ResourceStatus{
					"cpu": {
						Allocatable: resource.NewQuantity(24, resource.DecimalSI),
						Available:   resource.NewQuantity(24, resource.DecimalSI),
					},
					"memory": {
						Allocatable: resource.NewQuantity(40*1024*1024*1024, resource.BinarySI),
						Available:   resource.NewQuantity(40*1024*1024*1024, resource.BinarySI),
					},
					"nvidia.com/gpu": {
						Allocatable: resource.NewQuantity(2, resource.DecimalSI),
						Available:   resource.NewQuantity(2, resource.DecimalSI),
					},
				},
			},
		},
		requestsOfSharedCores: &Resource{},
		availableOfSharedCores: &Resource{
			MilliCPU: 48000,
			Memory:   80 * 1024 * 1024 * 1024,
		},
		socketToFreeNumasOfConflictResources: map[int]sets.Int{
			0: sets.NewInt(0),
			1: sets.NewInt(1),
		},
	}

	topologyAfterAddP1 := &NumaTopologyStatus{
		podAllocations: map[string]*PodAllocation{
			"default/p1/p1": {
				numaAllocations: map[int]*v1.ResourceList{
					0: parseResourceList(onlyGPU),
				},
			},
		},
		topology: map[int]*NumaStatus{
			0: {
				socketId: 0,
				resourceStatuses: map[string]*ResourceStatus{
					"cpu": {
						Allocatable: resource.NewQuantity(24, resource.DecimalSI),
						Available:   resource.NewQuantity(24, resource.DecimalSI),
						Users:       sets.NewString(),
					},
					"memory": {
						Allocatable: resource.NewQuantity(40*1024*1024*1024, resource.BinarySI),
						Available:   resource.NewQuantity(40*1024*1024*1024, resource.BinarySI),
						Users:       sets.NewString(),
					},
					"nvidia.com/gpu": {
						Allocatable: resource.NewQuantity(2, resource.DecimalSI),
						Available:   resource.NewQuantity(1, resource.DecimalSI),
						Users:       sets.NewString("default/p1/p1"),
					},
				},
			},
			1: {
				socketId: 1,
				resourceStatuses: map[string]*ResourceStatus{
					"cpu": {
						Allocatable: resource.NewQuantity(24, resource.DecimalSI),
						Available:   resource.NewQuantity(24, resource.DecimalSI),
					},
					"memory": {
						Allocatable: resource.NewQuantity(40*1024*1024*1024, resource.BinarySI),
						Available:   resource.NewQuantity(40*1024*1024*1024, resource.BinarySI),
					},
					"nvidia.com/gpu": {
						Allocatable: resource.NewQuantity(2, resource.DecimalSI),
						Available:   resource.NewQuantity(2, resource.DecimalSI),
					},
				},
			},
		},
		requestsOfSharedCores: &Resource{
			MilliCPU: 8000,
			Memory:   20 * 1024 * 1024 * 1024,
			ScalarResources: map[v1.ResourceName]int64{
				util.ResourceGPU: 1,
			},
		},
		availableOfSharedCores: &Resource{
			MilliCPU: 48000,
			Memory:   80 * 1024 * 1024 * 1024,
		},
		socketToFreeNumasOfConflictResources: map[int]sets.Int{
			0: sets.NewInt(0),
			1: sets.NewInt(1),
		},
	}

	topologyAfterSetCNRForP1_1 := &NumaTopologyStatus{
		podAllocations: map[string]*PodAllocation{
			"default/p1/p1": {
				agent: true,
				numaAllocations: map[int]*v1.ResourceList{
					1: parseResourceList(onlyGPU),
				},
			},
		},
		topology: map[int]*NumaStatus{
			0: {
				socketId: 0,
				resourceStatuses: map[string]*ResourceStatus{
					"cpu": {
						Allocatable: resource.NewQuantity(24, resource.DecimalSI),
						Available:   resource.NewQuantity(24, resource.DecimalSI),
					},
					"memory": {
						Allocatable: resource.NewQuantity(40*1024*1024*1024, resource.BinarySI),
						Available:   resource.NewQuantity(40*1024*1024*1024, resource.BinarySI),
					},
					"nvidia.com/gpu": {
						Allocatable: resource.NewQuantity(2, resource.DecimalSI),
						Available:   resource.NewQuantity(2, resource.DecimalSI),
					},
				},
			},
			1: {
				socketId: 1,
				resourceStatuses: map[string]*ResourceStatus{
					"cpu": {
						Allocatable: resource.NewQuantity(24, resource.DecimalSI),
						Available:   resource.NewQuantity(24, resource.DecimalSI),
						Users:       sets.NewString(),
					},
					"memory": {
						Allocatable: resource.NewQuantity(40*1024*1024*1024, resource.BinarySI),
						Available:   resource.NewQuantity(40*1024*1024*1024, resource.BinarySI),
						Users:       sets.NewString(),
					},
					"nvidia.com/gpu": {
						Allocatable: resource.NewQuantity(2, resource.DecimalSI),
						Available:   resource.NewQuantity(1, resource.DecimalSI),
						Users:       sets.NewString("default/p1/p1"),
					},
				},
			},
		},
		requestsOfSharedCores: &Resource{
			MilliCPU: 8000,
			Memory:   20 * 1024 * 1024 * 1024,
			ScalarResources: map[v1.ResourceName]int64{
				util.ResourceGPU: 1,
			},
		},
		availableOfSharedCores: &Resource{
			MilliCPU: 48000,
			Memory:   80 * 1024 * 1024 * 1024,
		},
		socketToFreeNumasOfConflictResources: map[int]sets.Int{
			0: sets.NewInt(0),
			1: sets.NewInt(1),
		},
	}

	topologyAfterSetCNRForP1_2 := &NumaTopologyStatus{
		podAllocations: map[string]*PodAllocation{
			"default/p1/p1": {
				agent: true,
				numaAllocations: map[int]*v1.ResourceList{
					1: parseResourceList(onlyGPU),
				},
			},
		},
		topology: map[int]*NumaStatus{
			0: {
				socketId: 0,
				resourceStatuses: map[string]*ResourceStatus{
					"cpu": {
						Allocatable: resource.NewQuantity(24, resource.DecimalSI),
						Available:   resource.NewQuantity(24, resource.DecimalSI),
					},
					"memory": {
						Allocatable: resource.NewQuantity(40*1024*1024*1024, resource.BinarySI),
						Available:   resource.NewQuantity(40*1024*1024*1024, resource.BinarySI),
					},
					"nvidia.com/gpu": {
						Allocatable: resource.NewQuantity(2, resource.DecimalSI),
						Available:   resource.NewQuantity(2, resource.DecimalSI),
					},
				},
			},
			1: {
				socketId: 1,
				resourceStatuses: map[string]*ResourceStatus{
					"cpu": {
						Allocatable: resource.NewQuantity(24, resource.DecimalSI),
						Available:   resource.NewQuantity(24, resource.DecimalSI),
						Users:       sets.NewString(),
					},
					"memory": {
						Allocatable: resource.NewQuantity(40*1024*1024*1024, resource.BinarySI),
						Available:   resource.NewQuantity(40*1024*1024*1024, resource.BinarySI),
						Users:       sets.NewString(),
					},
					"nvidia.com/gpu": {
						Allocatable: resource.NewQuantity(2, resource.DecimalSI),
						Available:   resource.NewQuantity(1, resource.DecimalSI),
						Users:       sets.NewString("default/p1/p1"),
					},
				},
			},
		},
		requestsOfSharedCores: &Resource{},
		availableOfSharedCores: &Resource{
			MilliCPU: 48000,
			Memory:   80 * 1024 * 1024 * 1024,
		},
		socketToFreeNumasOfConflictResources: map[int]sets.Int{
			0: sets.NewInt(0),
			1: sets.NewInt(1),
		},
	}

	topologyAfterPreemptP1_1 := &NumaTopologyStatus{
		podAllocations: map[string]*PodAllocation{
			"default/p1/p1": {
				agent: true,
				numaAllocations: map[int]*v1.ResourceList{
					1: parseResourceList(onlyGPU),
				},
			},
		},
		topology: map[int]*NumaStatus{
			0: {
				socketId: 0,
				resourceStatuses: map[string]*ResourceStatus{
					"cpu": {
						Allocatable: resource.NewQuantity(24, resource.DecimalSI),
						Available:   resource.NewQuantity(24, resource.DecimalSI),
					},
					"memory": {
						Allocatable: resource.NewQuantity(40*1024*1024*1024, resource.BinarySI),
						Available:   resource.NewQuantity(40*1024*1024*1024, resource.BinarySI),
					},
					"nvidia.com/gpu": {
						Allocatable: resource.NewQuantity(2, resource.DecimalSI),
						Available:   resource.NewQuantity(2, resource.DecimalSI),
					},
				},
			},
			1: {
				socketId: 1,
				resourceStatuses: map[string]*ResourceStatus{
					"cpu": {
						Allocatable: resource.NewQuantity(24, resource.DecimalSI),
						Available:   resource.NewQuantity(24, resource.DecimalSI),
					},
					"memory": {
						Allocatable: resource.NewQuantity(40*1024*1024*1024, resource.BinarySI),
						Available:   resource.NewQuantity(40*1024*1024*1024, resource.BinarySI),
					},
					"nvidia.com/gpu": {
						Allocatable: resource.NewQuantity(2, resource.DecimalSI),
						Available:   resource.NewQuantity(2, resource.DecimalSI),
					},
				},
			},
		},
		requestsOfSharedCores: &Resource{},
		availableOfSharedCores: &Resource{
			MilliCPU: 48000,
			Memory:   80 * 1024 * 1024 * 1024,
		},
		socketToFreeNumasOfConflictResources: map[int]sets.Int{
			0: sets.NewInt(0),
			1: sets.NewInt(1),
		},
	}

	topologyAfterPreemptP1_2 := &NumaTopologyStatus{
		podAllocations: map[string]*PodAllocation{
			"default/p1/p1": {
				agent: false,
				numaAllocations: map[int]*v1.ResourceList{
					0: parseResourceList(onlyGPU),
				},
			},
		},
		topology: map[int]*NumaStatus{
			0: {
				socketId: 0,
				resourceStatuses: map[string]*ResourceStatus{
					"cpu": {
						Allocatable: resource.NewQuantity(24, resource.DecimalSI),
						Available:   resource.NewQuantity(24, resource.DecimalSI),
					},
					"memory": {
						Allocatable: resource.NewQuantity(40*1024*1024*1024, resource.BinarySI),
						Available:   resource.NewQuantity(40*1024*1024*1024, resource.BinarySI),
					},
					"nvidia.com/gpu": {
						Allocatable: resource.NewQuantity(2, resource.DecimalSI),
						Available:   resource.NewQuantity(2, resource.DecimalSI),
					},
				},
			},
			1: {
				socketId: 1,
				resourceStatuses: map[string]*ResourceStatus{
					"cpu": {
						Allocatable: resource.NewQuantity(24, resource.DecimalSI),
						Available:   resource.NewQuantity(24, resource.DecimalSI),
					},
					"memory": {
						Allocatable: resource.NewQuantity(40*1024*1024*1024, resource.BinarySI),
						Available:   resource.NewQuantity(40*1024*1024*1024, resource.BinarySI),
					},
					"nvidia.com/gpu": {
						Allocatable: resource.NewQuantity(2, resource.DecimalSI),
						Available:   resource.NewQuantity(2, resource.DecimalSI),
					},
				},
			},
		},
		requestsOfSharedCores: &Resource{},
		availableOfSharedCores: &Resource{
			MilliCPU: 48000,
			Memory:   80 * 1024 * 1024 * 1024,
		},
		socketToFreeNumasOfConflictResources: map[int]sets.Int{
			0: sets.NewInt(0),
			1: sets.NewInt(1),
		},
	}

	topologyAfterRemoveP1 := &NumaTopologyStatus{
		podAllocations: map[string]*PodAllocation{
			"default/p1/p1": {
				agent: true,
				numaAllocations: map[int]*v1.ResourceList{
					1: parseResourceList(onlyGPU),
				},
			},
		},
		topology: map[int]*NumaStatus{
			0: {
				socketId: 0,
				resourceStatuses: map[string]*ResourceStatus{
					"cpu": {
						Allocatable: resource.NewQuantity(24, resource.DecimalSI),
						Available:   resource.NewQuantity(24, resource.DecimalSI),
					},
					"memory": {
						Allocatable: resource.NewQuantity(40*1024*1024*1024, resource.BinarySI),
						Available:   resource.NewQuantity(40*1024*1024*1024, resource.BinarySI),
					},
					"nvidia.com/gpu": {
						Allocatable: resource.NewQuantity(2, resource.DecimalSI),
						Available:   resource.NewQuantity(2, resource.DecimalSI),
					},
				},
			},
			1: {
				socketId: 1,
				resourceStatuses: map[string]*ResourceStatus{
					"cpu": {
						Allocatable: resource.NewQuantity(24, resource.DecimalSI),
						Available:   resource.NewQuantity(24, resource.DecimalSI),
					},
					"memory": {
						Allocatable: resource.NewQuantity(40*1024*1024*1024, resource.BinarySI),
						Available:   resource.NewQuantity(40*1024*1024*1024, resource.BinarySI),
					},
					"nvidia.com/gpu": {
						Allocatable: resource.NewQuantity(2, resource.DecimalSI),
						Available:   resource.NewQuantity(1, resource.DecimalSI),
						Users:       sets.NewString("default/p1/p1"),
					},
				},
			},
		},
		requestsOfSharedCores: &Resource{},
		availableOfSharedCores: &Resource{
			MilliCPU: 48000,
			Memory:   80 * 1024 * 1024 * 1024,
		},
		socketToFreeNumasOfConflictResources: map[int]sets.Int{
			0: sets.NewInt(0),
			1: sets.NewInt(1),
		},
	}

	topologyAfterAddP2 := &NumaTopologyStatus{
		podAllocations: map[string]*PodAllocation{
			"default/p2/p2": {
				agent: false,
				numaAllocations: map[int]*v1.ResourceList{
					0: parseResourceList(largeRes),
				},
			},
		},
		topology: map[int]*NumaStatus{
			0: {
				socketId: 0,
				resourceStatuses: map[string]*ResourceStatus{
					"cpu": {
						Allocatable: resource.NewQuantity(24, resource.DecimalSI),
						Available:   resource.NewQuantity(0, resource.DecimalSI),
						Users:       sets.NewString("default/p2/p2"),
					},
					"memory": {
						Allocatable: resource.NewQuantity(40*1024*1024*1024, resource.BinarySI),
						Available:   resource.NewQuantity(0*1024*1024*1024, resource.BinarySI),
						Users:       sets.NewString("default/p2/p2"),
					},
					"nvidia.com/gpu": {
						Allocatable: resource.NewQuantity(2, resource.DecimalSI),
						Available:   resource.NewQuantity(0, resource.DecimalSI),
						Users:       sets.NewString("default/p2/p2"),
					},
				},
			},
			1: {
				socketId: 1,
				resourceStatuses: map[string]*ResourceStatus{
					"cpu": {
						Allocatable: resource.NewQuantity(24, resource.DecimalSI),
						Available:   resource.NewQuantity(24, resource.DecimalSI),
					},
					"memory": {
						Allocatable: resource.NewQuantity(40*1024*1024*1024, resource.BinarySI),
						Available:   resource.NewQuantity(40*1024*1024*1024, resource.BinarySI),
					},
					"nvidia.com/gpu": {
						Allocatable: resource.NewQuantity(2, resource.DecimalSI),
						Available:   resource.NewQuantity(2, resource.DecimalSI),
					},
				},
			},
		},
		availableOfSharedCores: &Resource{
			MilliCPU: 24000,
			Memory:   40 * 1024 * 1024 * 1024,
		},
		socketToFreeNumasOfConflictResources: map[int]sets.Int{
			1: sets.NewInt(1),
		},
	}

	topologyAfterSetCNRForP2 := &NumaTopologyStatus{
		podAllocations: map[string]*PodAllocation{
			"default/p2/p2": {
				agent: true,
				numaAllocations: map[int]*v1.ResourceList{
					1: parseResourceList(largeRes),
				},
			},
		},
		topology: map[int]*NumaStatus{
			0: {
				socketId: 0,
				resourceStatuses: map[string]*ResourceStatus{
					"cpu": {
						Allocatable: resource.NewQuantity(24, resource.DecimalSI),
						Available:   resource.NewQuantity(24, resource.DecimalSI),
					},
					"memory": {
						Allocatable: resource.NewQuantity(40*1024*1024*1024, resource.BinarySI),
						Available:   resource.NewQuantity(40*1024*1024*1024, resource.BinarySI),
					},
					"nvidia.com/gpu": {
						Allocatable: resource.NewQuantity(2, resource.DecimalSI),
						Available:   resource.NewQuantity(2, resource.DecimalSI),
					},
				},
			},
			1: {
				socketId: 1,
				resourceStatuses: map[string]*ResourceStatus{
					"cpu": {
						Allocatable: resource.NewQuantity(24, resource.DecimalSI),
						Available:   resource.NewQuantity(0, resource.DecimalSI),
						Users:       sets.NewString("default/p2/p2"),
					},
					"memory": {
						Allocatable: resource.NewQuantity(40*1024*1024*1024, resource.BinarySI),
						Available:   resource.NewQuantity(0*1024*1024*1024, resource.BinarySI),
						Users:       sets.NewString("default/p2/p2"),
					},
					"nvidia.com/gpu": {
						Allocatable: resource.NewQuantity(2, resource.DecimalSI),
						Available:   resource.NewQuantity(0, resource.DecimalSI),
						Users:       sets.NewString("default/p2/p2"),
					},
				},
			},
		},
		availableOfSharedCores: &Resource{
			MilliCPU: 24000,
			Memory:   40 * 1024 * 1024 * 1024,
		},
		socketToFreeNumasOfConflictResources: map[int]sets.Int{
			0: sets.NewInt(0),
		},
	}

	topologyAfterPreemptP2_1 := &NumaTopologyStatus{
		podAllocations: map[string]*PodAllocation{
			"default/p2/p2": {
				agent: true,
				numaAllocations: map[int]*v1.ResourceList{
					1: parseResourceList(largeRes),
				},
			},
		},
		topology: map[int]*NumaStatus{
			0: {
				socketId: 0,
				resourceStatuses: map[string]*ResourceStatus{
					"cpu": {
						Allocatable: resource.NewQuantity(24, resource.DecimalSI),
						Available:   resource.NewQuantity(24, resource.DecimalSI),
					},
					"memory": {
						Allocatable: resource.NewQuantity(40*1024*1024*1024, resource.BinarySI),
						Available:   resource.NewQuantity(40*1024*1024*1024, resource.BinarySI),
					},
					"nvidia.com/gpu": {
						Allocatable: resource.NewQuantity(2, resource.DecimalSI),
						Available:   resource.NewQuantity(2, resource.DecimalSI),
					},
				},
			},
			1: {
				socketId: 1,
				resourceStatuses: map[string]*ResourceStatus{
					"cpu": {
						Allocatable: resource.NewQuantity(24, resource.DecimalSI),
						Available:   resource.NewQuantity(24, resource.DecimalSI),
					},
					"memory": {
						Allocatable: resource.NewQuantity(40*1024*1024*1024, resource.BinarySI),
						Available:   resource.NewQuantity(40*1024*1024*1024, resource.BinarySI),
					},
					"nvidia.com/gpu": {
						Allocatable: resource.NewQuantity(2, resource.DecimalSI),
						Available:   resource.NewQuantity(2, resource.DecimalSI),
					},
				},
			},
		},
		availableOfSharedCores: &Resource{
			MilliCPU: 48000,
			Memory:   80 * 1024 * 1024 * 1024,
		},
		socketToFreeNumasOfConflictResources: map[int]sets.Int{
			0: sets.NewInt(0),
			1: sets.NewInt(1),
		},
	}

	topologyAfterPreemptP2_2 := &NumaTopologyStatus{
		podAllocations: map[string]*PodAllocation{
			"default/p2/p2": {
				agent: false,
				numaAllocations: map[int]*v1.ResourceList{
					0: parseResourceList(largeRes),
				},
			},
		},
		topology: map[int]*NumaStatus{
			0: {
				socketId: 0,
				resourceStatuses: map[string]*ResourceStatus{
					"cpu": {
						Allocatable: resource.NewQuantity(24, resource.DecimalSI),
						Available:   resource.NewQuantity(24, resource.DecimalSI),
					},
					"memory": {
						Allocatable: resource.NewQuantity(40*1024*1024*1024, resource.BinarySI),
						Available:   resource.NewQuantity(40*1024*1024*1024, resource.BinarySI),
					},
					"nvidia.com/gpu": {
						Allocatable: resource.NewQuantity(2, resource.DecimalSI),
						Available:   resource.NewQuantity(2, resource.DecimalSI),
					},
				},
			},
			1: {
				socketId: 1,
				resourceStatuses: map[string]*ResourceStatus{
					"cpu": {
						Allocatable: resource.NewQuantity(24, resource.DecimalSI),
						Available:   resource.NewQuantity(24, resource.DecimalSI),
					},
					"memory": {
						Allocatable: resource.NewQuantity(40*1024*1024*1024, resource.BinarySI),
						Available:   resource.NewQuantity(40*1024*1024*1024, resource.BinarySI),
					},
					"nvidia.com/gpu": {
						Allocatable: resource.NewQuantity(2, resource.DecimalSI),
						Available:   resource.NewQuantity(2, resource.DecimalSI),
					},
				},
			},
		},
		availableOfSharedCores: &Resource{
			MilliCPU: 48000,
			Memory:   80 * 1024 * 1024 * 1024,
		},
		socketToFreeNumasOfConflictResources: map[int]sets.Int{
			0: sets.NewInt(0),
			1: sets.NewInt(1),
		},
	}

	topologyAfterRemoveP2 := &NumaTopologyStatus{
		podAllocations: map[string]*PodAllocation{
			"default/p2/p2": {
				agent: true,
				numaAllocations: map[int]*v1.ResourceList{
					1: parseResourceList(largeRes),
				},
			},
		},
		topology: map[int]*NumaStatus{
			0: {
				socketId: 0,
				resourceStatuses: map[string]*ResourceStatus{
					"cpu": {
						Allocatable: resource.NewQuantity(24, resource.DecimalSI),
						Available:   resource.NewQuantity(24, resource.DecimalSI),
					},
					"memory": {
						Allocatable: resource.NewQuantity(40*1024*1024*1024, resource.BinarySI),
						Available:   resource.NewQuantity(40*1024*1024*1024, resource.BinarySI),
					},
					"nvidia.com/gpu": {
						Allocatable: resource.NewQuantity(2, resource.DecimalSI),
						Available:   resource.NewQuantity(2, resource.DecimalSI),
					},
				},
			},
			1: {
				socketId: 1,
				resourceStatuses: map[string]*ResourceStatus{
					"cpu": {
						Allocatable: resource.NewQuantity(24, resource.DecimalSI),
						Available:   resource.NewQuantity(0, resource.DecimalSI),
						Users:       sets.NewString("default/p2/p2"),
					},
					"memory": {
						Allocatable: resource.NewQuantity(40*1024*1024*1024, resource.BinarySI),
						Available:   resource.NewQuantity(0*1024*1024*1024, resource.BinarySI),
						Users:       sets.NewString("default/p2/p2"),
					},
					"nvidia.com/gpu": {
						Allocatable: resource.NewQuantity(2, resource.DecimalSI),
						Available:   resource.NewQuantity(0, resource.DecimalSI),
						Users:       sets.NewString("default/p2/p2"),
					},
				},
			},
		},
		availableOfSharedCores: &Resource{
			MilliCPU: 24000,
			Memory:   40 * 1024 * 1024 * 1024,
		},
		socketToFreeNumasOfConflictResources: map[int]sets.Int{
			0: sets.NewInt(0),
		},
	}

	topologyAfterAddP3 := &NumaTopologyStatus{
		podAllocations: map[string]*PodAllocation{
			"default/p3/p3": {
				agent: false,
				numaAllocations: map[int]*v1.ResourceList{
					0: parseResourceList(smallRes),
				},
			},
		},
		topology: map[int]*NumaStatus{
			0: {
				socketId: 0,
				resourceStatuses: map[string]*ResourceStatus{
					"cpu": {
						Allocatable: resource.NewQuantity(24, resource.DecimalSI),
						Available:   resource.NewQuantity(16, resource.DecimalSI),
						Users:       sets.NewString("default/p3/p3"),
					},
					"memory": {
						Allocatable: resource.NewQuantity(40*1024*1024*1024, resource.BinarySI),
						Available:   resource.NewQuantity(20*1024*1024*1024, resource.BinarySI),
						Users:       sets.NewString("default/p3/p3"),
					},
					"nvidia.com/gpu": {
						Allocatable: resource.NewQuantity(2, resource.DecimalSI),
						Available:   resource.NewQuantity(1, resource.DecimalSI),
						Users:       sets.NewString("default/p3/p3"),
					},
				},
			},
			1: {
				socketId: 1,
				resourceStatuses: map[string]*ResourceStatus{
					"cpu": {
						Allocatable: resource.NewQuantity(24, resource.DecimalSI),
						Available:   resource.NewQuantity(24, resource.DecimalSI),
					},
					"memory": {
						Allocatable: resource.NewQuantity(40*1024*1024*1024, resource.BinarySI),
						Available:   resource.NewQuantity(40*1024*1024*1024, resource.BinarySI),
					},
					"nvidia.com/gpu": {
						Allocatable: resource.NewQuantity(2, resource.DecimalSI),
						Available:   resource.NewQuantity(2, resource.DecimalSI),
					},
				},
			},
		},
		availableOfSharedCores: &Resource{
			MilliCPU: 24000,
			Memory:   40 * 1024 * 1024 * 1024,
		},
		socketToFreeNumasOfConflictResources: map[int]sets.Int{
			1: sets.NewInt(1),
		},
	}

	topologyAfterSetCNRForP3 := &NumaTopologyStatus{
		podAllocations: map[string]*PodAllocation{
			"default/p3/p3": {
				agent: true,
				numaAllocations: map[int]*v1.ResourceList{
					1: parseResourceList(smallRes),
				},
			},
		},
		topology: map[int]*NumaStatus{
			0: {
				socketId: 0,
				resourceStatuses: map[string]*ResourceStatus{
					"cpu": {
						Allocatable: resource.NewQuantity(24, resource.DecimalSI),
						Available:   resource.NewQuantity(24, resource.DecimalSI),
					},
					"memory": {
						Allocatable: resource.NewQuantity(40*1024*1024*1024, resource.BinarySI),
						Available:   resource.NewQuantity(40*1024*1024*1024, resource.BinarySI),
					},
					"nvidia.com/gpu": {
						Allocatable: resource.NewQuantity(2, resource.DecimalSI),
						Available:   resource.NewQuantity(2, resource.DecimalSI),
					},
				},
			},
			1: {
				socketId: 1,
				resourceStatuses: map[string]*ResourceStatus{
					"cpu": {
						Allocatable: resource.NewQuantity(24, resource.DecimalSI),
						Available:   resource.NewQuantity(16, resource.DecimalSI),
						Users:       sets.NewString("default/p3/p3"),
					},
					"memory": {
						Allocatable: resource.NewQuantity(40*1024*1024*1024, resource.BinarySI),
						Available:   resource.NewQuantity(20*1024*1024*1024, resource.BinarySI),
						Users:       sets.NewString("default/p3/p3"),
					},
					"nvidia.com/gpu": {
						Allocatable: resource.NewQuantity(2, resource.DecimalSI),
						Available:   resource.NewQuantity(1, resource.DecimalSI),
						Users:       sets.NewString("default/p3/p3"),
					},
				},
			},
		},
		availableOfSharedCores: &Resource{
			MilliCPU: 24000,
			Memory:   40 * 1024 * 1024 * 1024,
		},
		socketToFreeNumasOfConflictResources: map[int]sets.Int{
			0: sets.NewInt(0),
		},
	}

	topologyAfterPreemptP3_1 := &NumaTopologyStatus{
		podAllocations: map[string]*PodAllocation{
			"default/p3/p3": {
				agent: true,
				numaAllocations: map[int]*v1.ResourceList{
					1: parseResourceList(smallRes),
				},
			},
		},
		topology: map[int]*NumaStatus{
			0: {
				socketId: 0,
				resourceStatuses: map[string]*ResourceStatus{
					"cpu": {
						Allocatable: resource.NewQuantity(24, resource.DecimalSI),
						Available:   resource.NewQuantity(24, resource.DecimalSI),
					},
					"memory": {
						Allocatable: resource.NewQuantity(40*1024*1024*1024, resource.BinarySI),
						Available:   resource.NewQuantity(40*1024*1024*1024, resource.BinarySI),
					},
					"nvidia.com/gpu": {
						Allocatable: resource.NewQuantity(2, resource.DecimalSI),
						Available:   resource.NewQuantity(2, resource.DecimalSI),
					},
				},
			},
			1: {
				socketId: 1,
				resourceStatuses: map[string]*ResourceStatus{
					"cpu": {
						Allocatable: resource.NewQuantity(24, resource.DecimalSI),
						Available:   resource.NewQuantity(24, resource.DecimalSI),
					},
					"memory": {
						Allocatable: resource.NewQuantity(40*1024*1024*1024, resource.BinarySI),
						Available:   resource.NewQuantity(40*1024*1024*1024, resource.BinarySI),
					},
					"nvidia.com/gpu": {
						Allocatable: resource.NewQuantity(2, resource.DecimalSI),
						Available:   resource.NewQuantity(2, resource.DecimalSI),
					},
				},
			},
		},
		availableOfSharedCores: &Resource{
			MilliCPU: 48000,
			Memory:   80 * 1024 * 1024 * 1024,
		},
		socketToFreeNumasOfConflictResources: map[int]sets.Int{
			0: sets.NewInt(0),
			1: sets.NewInt(1),
		},
	}

	topologyAfterPreemptP3_2 := &NumaTopologyStatus{
		podAllocations: map[string]*PodAllocation{
			"default/p3/p3": {
				agent: false,
				numaAllocations: map[int]*v1.ResourceList{
					0: parseResourceList(smallRes),
				},
			},
		},
		topology: map[int]*NumaStatus{
			0: {
				socketId: 0,
				resourceStatuses: map[string]*ResourceStatus{
					"cpu": {
						Allocatable: resource.NewQuantity(24, resource.DecimalSI),
						Available:   resource.NewQuantity(24, resource.DecimalSI),
					},
					"memory": {
						Allocatable: resource.NewQuantity(40*1024*1024*1024, resource.BinarySI),
						Available:   resource.NewQuantity(40*1024*1024*1024, resource.BinarySI),
					},
					"nvidia.com/gpu": {
						Allocatable: resource.NewQuantity(2, resource.DecimalSI),
						Available:   resource.NewQuantity(2, resource.DecimalSI),
					},
				},
			},
			1: {
				socketId: 1,
				resourceStatuses: map[string]*ResourceStatus{
					"cpu": {
						Allocatable: resource.NewQuantity(24, resource.DecimalSI),
						Available:   resource.NewQuantity(24, resource.DecimalSI),
					},
					"memory": {
						Allocatable: resource.NewQuantity(40*1024*1024*1024, resource.BinarySI),
						Available:   resource.NewQuantity(40*1024*1024*1024, resource.BinarySI),
					},
					"nvidia.com/gpu": {
						Allocatable: resource.NewQuantity(2, resource.DecimalSI),
						Available:   resource.NewQuantity(2, resource.DecimalSI),
					},
				},
			},
		},
		availableOfSharedCores: &Resource{
			MilliCPU: 48000,
			Memory:   80 * 1024 * 1024 * 1024,
		},
		socketToFreeNumasOfConflictResources: map[int]sets.Int{
			0: sets.NewInt(0),
			1: sets.NewInt(1),
		},
	}

	topologyAfterRemoveP3 := &NumaTopologyStatus{
		podAllocations: map[string]*PodAllocation{
			"default/p3/p3": {
				agent: true,
				numaAllocations: map[int]*v1.ResourceList{
					1: parseResourceList(smallRes),
				},
			},
		},
		topology: map[int]*NumaStatus{
			0: {
				socketId: 0,
				resourceStatuses: map[string]*ResourceStatus{
					"cpu": {
						Allocatable: resource.NewQuantity(24, resource.DecimalSI),
						Available:   resource.NewQuantity(24, resource.DecimalSI),
					},
					"memory": {
						Allocatable: resource.NewQuantity(40*1024*1024*1024, resource.BinarySI),
						Available:   resource.NewQuantity(40*1024*1024*1024, resource.BinarySI),
					},
					"nvidia.com/gpu": {
						Allocatable: resource.NewQuantity(2, resource.DecimalSI),
						Available:   resource.NewQuantity(2, resource.DecimalSI),
					},
				},
			},
			1: {
				socketId: 1,
				resourceStatuses: map[string]*ResourceStatus{
					"cpu": {
						Allocatable: resource.NewQuantity(24, resource.DecimalSI),
						Available:   resource.NewQuantity(16, resource.DecimalSI),
						Users:       sets.NewString("default/p3/p3"),
					},
					"memory": {
						Allocatable: resource.NewQuantity(40*1024*1024*1024, resource.BinarySI),
						Available:   resource.NewQuantity(20*1024*1024*1024, resource.BinarySI),
						Users:       sets.NewString("default/p3/p3"),
					},
					"nvidia.com/gpu": {
						Allocatable: resource.NewQuantity(2, resource.DecimalSI),
						Available:   resource.NewQuantity(1, resource.DecimalSI),
						Users:       sets.NewString("default/p3/p3"),
					},
				},
			},
		},
		availableOfSharedCores: &Resource{
			MilliCPU: 24000,
			Memory:   40 * 1024 * 1024 * 1024,
		},
		socketToFreeNumasOfConflictResources: map[int]sets.Int{
			0: sets.NewInt(0),
		},
	}

	tests := []struct {
		name             string
		pod              *v1.Pod
		cnr              *katalystv1alpha1.CustomNodeResource
		expectedTopology []*NumaTopologyStatus
		preempt          bool
	}{
		{
			name:             "for share cores pod, not preempt",
			pod:              setPod("p1", smallRes, map[string]string{podutil.MicroTopologyKey: "0:nvidia.com/gpu=1"}),
			cnr:              cnrHasSharedCoresPod,
			expectedTopology: []*NumaTopologyStatus{topologyAfterAddP1, topologyAfterSetCNRForP1_1, topologyAfterRemoveP1, numaTopologyStatus, topologyAfterRemoveP1},
		},
		{
			name:             "for share cores pod, preempt",
			pod:              setPod("p1", smallRes, map[string]string{podutil.MicroTopologyKey: "0:nvidia.com/gpu=1"}),
			cnr:              cnrHasSharedCoresPod,
			expectedTopology: []*NumaTopologyStatus{topologyAfterAddP1, topologyAfterSetCNRForP1_1, topologyAfterPreemptP1_1, topologyAfterPreemptP1_2, topologyAfterSetCNRForP1_2},
			preempt:          true,
		},
		{
			name:             "for exclusive pod, not preempt",
			pod:              setPod("p2", largeRes, map[string]string{podutil.MicroTopologyKey: "0:cpu=24,memory=40Gi,nvidia.com/gpu=2", util.QoSLevelKey: string(util.DedicatedCores), util.MemoyEnhancementKey: "{\"numa_binding\":\"true\",\"numa_exclusive\":\"true\"}"}),
			cnr:              cnrHasSocketPod,
			expectedTopology: []*NumaTopologyStatus{topologyAfterAddP2, topologyAfterSetCNRForP2, topologyAfterRemoveP2, numaTopologyStatus, topologyAfterSetCNRForP2},
		},
		{
			name:             "for exclusive pod, preempt",
			pod:              setPod("p2", largeRes, map[string]string{podutil.MicroTopologyKey: "0:cpu=24,memory=40Gi,nvidia.com/gpu=2", util.QoSLevelKey: string(util.DedicatedCores), util.MemoyEnhancementKey: "{\"numa_binding\":\"true\",\"numa_exclusive\":\"true\"}"}),
			cnr:              cnrHasSocketPod,
			expectedTopology: []*NumaTopologyStatus{topologyAfterAddP2, topologyAfterSetCNRForP2, topologyAfterPreemptP2_1, topologyAfterPreemptP2_2, topologyAfterSetCNRForP2},
			preempt:          true,
		},
		{
			name:             "for dedicated cores pod, not preempt",
			pod:              setPod("p3", smallRes, map[string]string{podutil.MicroTopologyKey: "0:cpu=8,memory=20Gi,nvidia.com/gpu=1", util.QoSLevelKey: string(util.DedicatedCores), util.MemoyEnhancementKey: "{\"numa_binding\":\"true\",\"numa_exclusive\":\"false\"}"}),
			cnr:              cnrHasDedicatedCoresPod,
			expectedTopology: []*NumaTopologyStatus{topologyAfterAddP3, topologyAfterSetCNRForP3, topologyAfterRemoveP3, numaTopologyStatus, topologyAfterSetCNRForP3},
		},
		{
			name:             "for dedicated cores pod, preempt",
			pod:              setPod("p3", smallRes, map[string]string{podutil.MicroTopologyKey: "0:cpu=8,memory=20Gi,nvidia.com/gpu=1", util.QoSLevelKey: string(util.DedicatedCores), util.MemoyEnhancementKey: "{\"numa_binding\":\"true\",\"numa_exclusive\":\"false\"}"}),
			cnr:              cnrHasDedicatedCoresPod,
			expectedTopology: []*NumaTopologyStatus{topologyAfterAddP3, topologyAfterSetCNRForP3, topologyAfterPreemptP3_1, topologyAfterPreemptP3_2, topologyAfterSetCNRForP3},
			preempt:          true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ni := fakeNodeInfo()
			ni.SetCNR(cnrNoPod)

			if !numaTopologyStatusEqual(numaTopologyStatus, ni.GetNumaTopologyStatus()) {
				t.Errorf("expected 1: %#v, got: %#v", numaTopologyStatus, ni.GetNumaTopologyStatus())
			}
			ni.AddPod(test.pod)
			if !numaTopologyStatusEqual(test.expectedTopology[0], ni.GetNumaTopologyStatus()) {
				t.Errorf("expected 2: %#v, got: %#v", test.expectedTopology[0], ni.GetNumaTopologyStatus())
			}
			ni.SetCNR(test.cnr)
			if !numaTopologyStatusEqual(test.expectedTopology[1], ni.GetNumaTopologyStatus()) {
				t.Errorf("expected 3: %#v, got: %#v", test.expectedTopology[1], ni.GetNumaTopologyStatus())
			}
			ni.RemovePod(test.pod, test.preempt)
			if !numaTopologyStatusEqual(test.expectedTopology[2], ni.GetNumaTopologyStatus()) {
				t.Errorf("expected 4: %#v, got: %#v", test.expectedTopology[2], ni.GetNumaTopologyStatus())
			}
			ni.SetCNR(cnrNoPod)
			if !numaTopologyStatusEqual(numaTopologyStatus, ni.GetNumaTopologyStatus()) {
				t.Errorf("expected 5: %#v, got: %#v", numaTopologyStatus, ni.GetNumaTopologyStatus())
			}

			ni.SetCNR(test.cnr)
			if !numaTopologyStatusEqual(test.expectedTopology[4], ni.GetNumaTopologyStatus()) {
				t.Errorf("expected 6: %#v, got: %#v", test.expectedTopology[4], ni.GetNumaTopologyStatus())
			}
			ni.AddPod(test.pod)
			if !numaTopologyStatusEqual(test.expectedTopology[1], ni.GetNumaTopologyStatus()) {
				t.Errorf("expected 7: %#v, got: %#v", test.expectedTopology[1], ni.GetNumaTopologyStatus())
			}
			ni.SetCNR(cnrNoPod)
			if !numaTopologyStatusEqual(test.expectedTopology[0], ni.GetNumaTopologyStatus()) {
				t.Errorf("expected 8: %#v, got: %#v", test.expectedTopology[0], ni.GetNumaTopologyStatus())
			}
			ni.RemovePod(test.pod, test.preempt)
			if !numaTopologyStatusEqual(test.expectedTopology[3], ni.GetNumaTopologyStatus()) {
				t.Errorf("expected 9: %#v, got: %#v", test.expectedTopology[3], ni.GetNumaTopologyStatus())
			}
		})
	}
}

func TestSharedCoresPodsOperation(t *testing.T) {
	utilfeature.DefaultMutableFeatureGate.SetFromMap(map[string]bool{string(godelfeatures.NonNativeResourceSchedulingSupport): true})

	makePod := func(namespace, name, uid string, req corev1.ResourceList, annotations map[string]string) *corev1.Pod {
		return &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Namespace:   namespace,
				Name:        name,
				UID:         types.UID(uid),
				Annotations: annotations,
			},
			Spec: corev1.PodSpec{
				Containers: []corev1.Container{
					{
						Resources: v1.ResourceRequirements{
							Requests: req,
						},
					},
				},
			},
		}
	}

	p1 := makePod("default", "p1", "p1", corev1.ResourceList{"cpu": resource.MustParse("10"), "memory": resource.MustParse("10Gi")}, map[string]string{util.QoSLevelKey: string(util.DedicatedCores), util.MemoyEnhancementKey: "{\"numa_binding\":\"true\",\"numa_exclusive\":\"false\"}"})
	p2 := makePod("default", "p2", "p2", corev1.ResourceList{"cpu": resource.MustParse("10"), "memory": resource.MustParse("10Gi"), "nvidia.com/gpu": resource.MustParse("1")}, map[string]string{util.QoSLevelKey: string(util.DedicatedCores), util.MemoyEnhancementKey: "{\"numa_binding\":\"true\",\"numa_exclusive\":\"true\"}"})
	p3 := makePod("default", "p3", "p3", corev1.ResourceList{"cpu": resource.MustParse("10"), "memory": resource.MustParse("10Gi"), "nvidia.com/gpu": resource.MustParse("1")}, map[string]string{util.QoSLevelKey: string(util.DedicatedCores), util.MemoyEnhancementKey: "{\"numa_binding\":\"true\",\"numa_exclusive\":\"true\"}"})
	p4 := makePod("default", "p4", "p4", corev1.ResourceList{"cpu": resource.MustParse("10"), "memory": resource.MustParse("10Gi")}, nil)

	node := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name: "n",
		},
		Status: corev1.NodeStatus{
			Allocatable: corev1.ResourceList{
				"cpu":            resource.MustParse("94"),
				"memory":         resource.MustParse("252Gi"),
				"nvidia.com/gpu": resource.MustParse("8"),
			},
		},
	}
	cnr := &katalystv1alpha1.CustomNodeResource{
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
									"cpu":            resource.MustParse("24"),
									"memory":         resource.MustParse("64Gi"),
									"nvidia.com/gpu": resource.MustParse("2"),
								},
								Capacity: &corev1.ResourceList{
									"cpu":            resource.MustParse("24"),
									"memory":         resource.MustParse("64Gi"),
									"nvidia.com/gpu": resource.MustParse("2"),
								},
							},
							Allocations: []*katalystv1alpha1.Allocation{
								{
									Consumer: "default/p1/p1",
									Requests: &corev1.ResourceList{
										"cpu":    resource.MustParse("10"),
										"memory": resource.MustParse("10Gi"),
									},
								},
							},
						},
						{
							Type: katalystv1alpha1.TopologyTypeNuma,
							Name: "1",
							Resources: katalystv1alpha1.Resources{
								Allocatable: &corev1.ResourceList{
									"cpu":            resource.MustParse("24"),
									"memory":         resource.MustParse("64Gi"),
									"nvidia.com/gpu": resource.MustParse("2"),
								},
								Capacity: &corev1.ResourceList{
									"cpu":            resource.MustParse("24"),
									"memory":         resource.MustParse("64Gi"),
									"nvidia.com/gpu": resource.MustParse("2"),
								},
							},
							Allocations: []*katalystv1alpha1.Allocation{
								{
									Consumer: "default/p2/p2",
									Requests: &corev1.ResourceList{
										"cpu":            resource.MustParse("24"),
										"memory":         resource.MustParse("64Gi"),
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
								Allocatable: &corev1.ResourceList{
									"cpu":            resource.MustParse("24"),
									"memory":         resource.MustParse("64Gi"),
									"nvidia.com/gpu": resource.MustParse("2"),
								},
								Capacity: &corev1.ResourceList{
									"cpu":            resource.MustParse("24"),
									"memory":         resource.MustParse("64Gi"),
									"nvidia.com/gpu": resource.MustParse("2"),
								},
							},
							Allocations: []*katalystv1alpha1.Allocation{
								{
									Consumer: "default/p3/p3",
									Requests: &corev1.ResourceList{
										"cpu":            resource.MustParse("24"),
										"memory":         resource.MustParse("64Gi"),
										"nvidia.com/gpu": resource.MustParse("1"),
									},
								},
							},
						},
						{
							Type: katalystv1alpha1.TopologyTypeNuma,
							Name: "3",
							Resources: katalystv1alpha1.Resources{
								Allocatable: &corev1.ResourceList{
									"cpu":            resource.MustParse("24"),
									"memory":         resource.MustParse("64Gi"),
									"nvidia.com/gpu": resource.MustParse("2"),
								},
								Capacity: &corev1.ResourceList{
									"cpu":            resource.MustParse("24"),
									"memory":         resource.MustParse("64Gi"),
									"nvidia.com/gpu": resource.MustParse("2"),
								},
							},
							Allocations: []*katalystv1alpha1.Allocation{
								{
									Consumer: "default/p3/p3",
									Requests: &corev1.ResourceList{
										"cpu":    resource.MustParse("24"),
										"memory": resource.MustParse("64Gi"),
									},
								},
							},
						},
					},
				},
			},
		},
	}

	nodeInfo := &NodeInfoImpl{
		PodInfoMaintainer:          NewPodInfoMaintainer(),
		GuaranteedRequested:        &Resource{},
		GuaranteedNonZeroRequested: &Resource{},
		GuaranteedAllocatable:      &Resource{},
		BestEffortRequested:        &Resource{},
		BestEffortNonZeroRequested: &Resource{},
		BestEffortAllocatable:      &Resource{},
		TransientInfo:              NewTransientSchedulerInfo(),
		Generation:                 0,
		UsedPorts:                  make(HostPortInfo),
		ImageStates:                make(map[string]*ImageStateSummary),
		NumaTopologyStatus:         newNumaTopologyStatus(NewResource(nil)),
	}
	nodeInfo.SetNode(node)
	nodeInfo.SetCNR(cnr)
	nodeInfo.AddPod(p1)
	nodeInfo.AddPod(p2)
	nodeInfo.AddPod(p3)
	nodeInfo.AddPod(p4)

	gotResourceAvailable := nodeInfo.GetResourcesAvailableForSharedCoresPods(nil)
	gotResourceRequests := nodeInfo.GetResourcesRequestsOfSharedCoresPods()
	expectedResourceAvailable := &Resource{
		MilliCPU: 0,
		Memory:   0,
	}
	expectedResourceRequests := &Resource{
		MilliCPU: 10000,
		Memory:   10 * 1024 * 1024 * 1024,
	}
	if !reflect.DeepEqual(expectedResourceAvailable, gotResourceAvailable) {
		t.Errorf("expected available resource: %v, but got: %v", expectedResourceAvailable, gotResourceAvailable)
	}
	if !reflect.DeepEqual(expectedResourceRequests, gotResourceRequests) {
		t.Errorf("expected requests resource: %v, but got: %v", expectedResourceRequests, gotResourceRequests)
	}

	nodeInfo.RemovePod(p3, true)
	gotResourceAvailable = nodeInfo.GetResourcesAvailableForSharedCoresPods(nil)
	gotResourceRequests = nodeInfo.GetResourcesRequestsOfSharedCoresPods()
	expectedResourceAvailable = &Resource{
		MilliCPU: 48000,
		Memory:   128 * 1024 * 1024 * 1024,
	}
	expectedResourceRequests = &Resource{
		MilliCPU: 10000,
		Memory:   10 * 1024 * 1024 * 1024,
	}
	if !reflect.DeepEqual(expectedResourceAvailable, gotResourceAvailable) {
		t.Errorf("expected available resource: %v, but got: %v", expectedResourceAvailable, gotResourceAvailable)
	}
	if !reflect.DeepEqual(expectedResourceRequests, gotResourceRequests) {
		t.Errorf("expected requests resource: %v, but got: %v", expectedResourceRequests, gotResourceRequests)
	}

	nodeInfo.AddPod(p3)
	gotResourceAvailable = nodeInfo.GetResourcesAvailableForSharedCoresPods(nil)
	gotResourceRequests = nodeInfo.GetResourcesRequestsOfSharedCoresPods()
	expectedResourceAvailable = &Resource{
		MilliCPU: 0,
		Memory:   0,
	}
	expectedResourceRequests = &Resource{
		MilliCPU: 10000,
		Memory:   10 * 1024 * 1024 * 1024,
	}
	if !reflect.DeepEqual(expectedResourceAvailable, gotResourceAvailable) {
		t.Errorf("expected available resource: %v, but got: %v", expectedResourceAvailable, gotResourceAvailable)
	}
	if !reflect.DeepEqual(expectedResourceRequests, gotResourceRequests) {
		t.Errorf("expected requests resource: %v, but got: %v", expectedResourceRequests, gotResourceRequests)
	}

	nodeInfo.RemovePod(p4, false)
	gotResourceAvailable = nodeInfo.GetResourcesAvailableForSharedCoresPods(nil)
	gotResourceRequests = nodeInfo.GetResourcesRequestsOfSharedCoresPods()
	expectedResourceAvailable = &Resource{
		MilliCPU: 0,
		Memory:   0,
	}
	expectedResourceRequests = &Resource{
		MilliCPU: 0,
		Memory:   0,
	}
	if !reflect.DeepEqual(expectedResourceAvailable, gotResourceAvailable) {
		t.Errorf("expected available resource: %v, but got: %v", expectedResourceAvailable, gotResourceAvailable)
	}
	if !reflect.DeepEqual(expectedResourceRequests, gotResourceRequests) {
		t.Errorf("expected requests resource: %v, but got: %v", expectedResourceRequests, gotResourceRequests)
	}

	nodeInfo.AddPod(p4)
	gotResourceAvailable = nodeInfo.GetResourcesAvailableForSharedCoresPods(nil)
	gotResourceRequests = nodeInfo.GetResourcesRequestsOfSharedCoresPods()
	expectedResourceAvailable = &Resource{
		MilliCPU: 0,
		Memory:   0,
	}
	expectedResourceRequests = &Resource{
		MilliCPU: 10000,
		Memory:   10 * 1024 * 1024 * 1024,
	}
	if !reflect.DeepEqual(expectedResourceAvailable, gotResourceAvailable) {
		t.Errorf("expected available resource: %v, but got: %v", expectedResourceAvailable, gotResourceAvailable)
	}
	if !reflect.DeepEqual(expectedResourceRequests, gotResourceRequests) {
		t.Errorf("expected requests resource: %v, but got: %v", expectedResourceRequests, gotResourceRequests)
	}
}

func TestSharedCoresPodsOperationWithScalarResource(t *testing.T) {
	makePod := func(namespace, name, uid string, req corev1.ResourceList, annotations map[string]string) *corev1.Pod {
		return &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Namespace:   namespace,
				Name:        name,
				UID:         types.UID(uid),
				Annotations: annotations,
			},
			Spec: corev1.PodSpec{
				Containers: []corev1.Container{
					{
						Resources: v1.ResourceRequirements{
							Requests: req,
						},
					},
				},
			},
		}
	}

	p1 := makePod("default", "p1", "p1", corev1.ResourceList{"cpu": resource.MustParse("10"), "memory": resource.MustParse("10Gi")}, map[string]string{util.QoSLevelKey: string(util.DedicatedCores), util.MemoyEnhancementKey: "{\"numa_binding\":\"true\",\"numa_exclusive\":\"false\"}"})
	p2 := makePod("default", "p2", "p2", corev1.ResourceList{"cpu": resource.MustParse("10"), "memory": resource.MustParse("10Gi"), "nvidia.com/gpu": resource.MustParse("2")}, map[string]string{util.QoSLevelKey: string(util.DedicatedCores), util.MemoyEnhancementKey: "{\"numa_binding\":\"true\",\"numa_exclusive\":\"true\"}"})
	p3 := makePod("default", "p3", "p3", corev1.ResourceList{"cpu": resource.MustParse("10"), "memory": resource.MustParse("10Gi"), "nvidia.com/gpu": resource.MustParse("1")}, nil)

	node := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name: "n",
		},
		Status: corev1.NodeStatus{
			Allocatable: corev1.ResourceList{
				"cpu":            resource.MustParse("94"),
				"memory":         resource.MustParse("252Gi"),
				"nvidia.com/gpu": resource.MustParse("8"),
			},
		},
	}
	cnr := &katalystv1alpha1.CustomNodeResource{
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
									"cpu":            resource.MustParse("24"),
									"memory":         resource.MustParse("64Gi"),
									"nvidia.com/gpu": resource.MustParse("2"),
								},
								Capacity: &corev1.ResourceList{
									"cpu":            resource.MustParse("24"),
									"memory":         resource.MustParse("64Gi"),
									"nvidia.com/gpu": resource.MustParse("2"),
								},
							},
							Allocations: []*katalystv1alpha1.Allocation{
								{
									Consumer: "default/p1/p1",
									Requests: &corev1.ResourceList{
										"cpu":    resource.MustParse("10"),
										"memory": resource.MustParse("10Gi"),
									},
								},
							},
						},
						{
							Type: katalystv1alpha1.TopologyTypeNuma,
							Name: "1",
							Resources: katalystv1alpha1.Resources{
								Allocatable: &corev1.ResourceList{
									"cpu":            resource.MustParse("24"),
									"memory":         resource.MustParse("62Gi"),
									"nvidia.com/gpu": resource.MustParse("2"),
								},
								Capacity: &corev1.ResourceList{
									"cpu":            resource.MustParse("24"),
									"memory":         resource.MustParse("64Gi"),
									"nvidia.com/gpu": resource.MustParse("2"),
								},
							},
							Allocations: []*katalystv1alpha1.Allocation{
								{
									Consumer: "default/p2/p2",
									Requests: &corev1.ResourceList{
										"cpu":            resource.MustParse("24"),
										"memory":         resource.MustParse("64Gi"),
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
								Allocatable: &corev1.ResourceList{
									"cpu":            resource.MustParse("23"),
									"memory":         resource.MustParse("62Gi"),
									"nvidia.com/gpu": resource.MustParse("2"),
								},
								Capacity: &corev1.ResourceList{
									"cpu":            resource.MustParse("24"),
									"memory":         resource.MustParse("64Gi"),
									"nvidia.com/gpu": resource.MustParse("2"),
								},
							},
							Allocations: []*katalystv1alpha1.Allocation{
								{
									Consumer: "default/p3/p3",
									Requests: &corev1.ResourceList{
										"nvidia.com/gpu": resource.MustParse("1"),
									},
								},
							},
						},
						{
							Type: katalystv1alpha1.TopologyTypeNuma,
							Name: "3",
							Resources: katalystv1alpha1.Resources{
								Allocatable: &corev1.ResourceList{
									"cpu":            resource.MustParse("23"),
									"memory":         resource.MustParse("62Gi"),
									"nvidia.com/gpu": resource.MustParse("2"),
								},
								Capacity: &corev1.ResourceList{
									"cpu":            resource.MustParse("24"),
									"memory":         resource.MustParse("64Gi"),
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

	nodeInfo := &NodeInfoImpl{
		PodInfoMaintainer:          NewPodInfoMaintainer(),
		GuaranteedRequested:        &Resource{},
		GuaranteedNonZeroRequested: &Resource{},
		GuaranteedAllocatable:      &Resource{},
		BestEffortRequested:        &Resource{},
		BestEffortNonZeroRequested: &Resource{},
		BestEffortAllocatable:      &Resource{},
		TransientInfo:              NewTransientSchedulerInfo(),
		Generation:                 0,
		UsedPorts:                  make(HostPortInfo),
		ImageStates:                make(map[string]*ImageStateSummary),
		NumaTopologyStatus:         newNumaTopologyStatus(NewResource(nil)),
	}
	nodeInfo.SetNode(node)
	nodeInfo.SetCNR(cnr)
	nodeInfo.AddPod(p1)
	nodeInfo.AddPod(p2)
	nodeInfo.AddPod(p3)

	gotResourceAvailable := nodeInfo.GetResourcesAvailableForSharedCoresPods(nil)
	gotResourceRequests := nodeInfo.GetResourcesRequestsOfSharedCoresPods()
	expectedResourceAvailable := &Resource{
		MilliCPU: 46000,
		Memory:   124 * 1024 * 1024 * 1024,
	}
	expectedResourceRequests := &Resource{
		MilliCPU: 10000,
		Memory:   10 * 1024 * 1024 * 1024,
		ScalarResources: map[v1.ResourceName]int64{
			util.ResourceGPU: 1,
		},
	}
	if !reflect.DeepEqual(expectedResourceAvailable, gotResourceAvailable) {
		t.Errorf("expected available resource: %v, but got: %v", expectedResourceAvailable, gotResourceAvailable)
	}
	if !reflect.DeepEqual(expectedResourceRequests, gotResourceRequests) {
		t.Errorf("expected requests resource: %v, but got: %v", expectedResourceRequests, gotResourceRequests)
	}

	nodeInfo.RemovePod(p3, true)
	gotResourceAvailable = nodeInfo.GetResourcesAvailableForSharedCoresPods(nil)
	gotResourceRequests = nodeInfo.GetResourcesRequestsOfSharedCoresPods()
	expectedResourceAvailable = &Resource{
		MilliCPU: 46000,
		Memory:   124 * 1024 * 1024 * 1024,
	}
	expectedResourceRequests = &Resource{
		MilliCPU: 0,
		Memory:   0,
		ScalarResources: map[v1.ResourceName]int64{
			util.ResourceGPU: 0,
		},
	}
	if !reflect.DeepEqual(expectedResourceAvailable, gotResourceAvailable) {
		t.Errorf("expected available resource: %v, but got: %v", expectedResourceAvailable, gotResourceAvailable)
	}
	if !reflect.DeepEqual(expectedResourceRequests, gotResourceRequests) {
		t.Errorf("expected requests resource: %v, but got: %v", expectedResourceRequests, gotResourceRequests)
	}

	nodeInfo.AddPod(p3)
	gotResourceAvailable = nodeInfo.GetResourcesAvailableForSharedCoresPods(nil)
	gotResourceRequests = nodeInfo.GetResourcesRequestsOfSharedCoresPods()
	expectedResourceAvailable = &Resource{
		MilliCPU: 46000,
		Memory:   124 * 1024 * 1024 * 1024,
	}
	expectedResourceRequests = &Resource{
		MilliCPU: 10000,
		Memory:   10 * 1024 * 1024 * 1024,
		ScalarResources: map[v1.ResourceName]int64{
			util.ResourceGPU: 1,
		},
	}
	if !reflect.DeepEqual(expectedResourceAvailable, gotResourceAvailable) {
		t.Errorf("expected available resource: %v, but got: %v", expectedResourceAvailable, gotResourceAvailable)
	}
	if !reflect.DeepEqual(expectedResourceRequests, gotResourceRequests) {
		t.Errorf("expected requests resource: %v, but got: %v", expectedResourceRequests, gotResourceRequests)
	}
}

func TestGetFreeResourcesInNuma(t *testing.T) {
	numaTopologyStatus := &NumaTopologyStatus{
		topology: map[int]*NumaStatus{
			0: {
				resourceStatuses: map[string]*ResourceStatus{
					"cpu": {
						Available: resource.NewQuantity(10, resource.DecimalSI),
					},
					"memory": {
						Available: resource.NewQuantity(10*1024*1024*1024, resource.BinarySI),
					},
				},
			},
			1: {
				resourceStatuses: map[string]*ResourceStatus{
					"cpu": {
						Available: resource.NewQuantity(50, resource.DecimalSI),
					},
					"memory": {
						Available: resource.NewQuantity(50*1024*1024*1024, resource.BinarySI),
					},
				},
			},
		},
	}

	gotFreeResources := numaTopologyStatus.GetFreeResourcesInNuma(0)
	cpu := resource.NewQuantity(10, resource.DecimalSI)
	mem := resource.NewQuantity(10*1024*1024*1024, resource.BinarySI)
	expectedFreeResources := v1.ResourceList{
		"cpu":    *cpu,
		"memory": *mem,
	}
	if !reflect.DeepEqual(expectedFreeResources, gotFreeResources) {
		t.Errorf("expected resources: %v, but got: %v", expectedFreeResources, gotFreeResources)
	}

	gotFreeResources = numaTopologyStatus.GetFreeResourcesInNuma(1)
	cpu = resource.NewQuantity(50, resource.DecimalSI)
	mem = resource.NewQuantity(50*1024*1024*1024, resource.BinarySI)
	expectedFreeResources = v1.ResourceList{
		"cpu":    *cpu,
		"memory": *mem,
	}
	if !reflect.DeepEqual(expectedFreeResources, gotFreeResources) {
		t.Errorf("expected resources: %v, but got: %v", expectedFreeResources, gotFreeResources)
	}
}

func TestIsNumaEmpty(t *testing.T) {
	numaTopologyStatus := &NumaTopologyStatus{
		topology: map[int]*NumaStatus{
			0: {
				resourceStatuses: map[string]*ResourceStatus{
					"cpu": {
						Users: sets.NewString("p1/p1/p1"),
					},
					"memory": {
						Users: sets.NewString("p1/p1/p1", "p2/p2/p2"),
					},
				},
			},
			1: {
				resourceStatuses: map[string]*ResourceStatus{
					"cpu":            {},
					"nvidia.com/gpu": {},
				},
			},
			2: {
				resourceStatuses: map[string]*ResourceStatus{
					"cpu": {},
					"nvidia.com/gpu": {
						Users: sets.NewString("p1/p1/p1"),
					},
				},
			},
		},
	}
	if gotIsEmpty := numaTopologyStatus.IsNumaEmptyForConflictResources(0); gotIsEmpty {
		t.Errorf("numa 0 is not empty")
	}
	if gotIsEmpty := numaTopologyStatus.IsNumaEmptyForConflictResources(1); !gotIsEmpty {
		t.Errorf("numa 1 is empty")
	}
	if gotIsEmpty := numaTopologyStatus.IsNumaEmptyForConflictResources(2); !gotIsEmpty {
		t.Errorf("numa 2 is empty")
	}
}

func TestNodeInfoImpl_GetVictimCandidates(t *testing.T) {
	node := NewNodeInfo(
		[]*v1.Pod{
			// 1. Pod doesn't have resource type.
			{
				ObjectMeta: metav1.ObjectMeta{
					UID:         types.UID("p1"),
					Name:        "p1",
					Annotations: map[string]string{podutil.PodResourceTypeAnnotationKey: "invalid-value"},
				},
				Spec: v1.PodSpec{
					NodeName: "node",
				},
			},
			// 2. Pod isn't bound
			{
				ObjectMeta: metav1.ObjectMeta{
					UID:  types.UID("p2"),
					Name: "p2",
				},
			},
			// 3. GT Pod
			{
				ObjectMeta: metav1.ObjectMeta{
					UID:         types.UID("p3"),
					Name:        "p3",
					Annotations: map[string]string{podutil.PodResourceTypeAnnotationKey: string(podutil.GuaranteedPod)},
				},
				Spec: v1.PodSpec{
					NodeName: "node",
				},
			},
			// 4. BE Pod
			{
				ObjectMeta: metav1.ObjectMeta{
					UID:         types.UID("p4"),
					Name:        "p4",
					Annotations: map[string]string{podutil.PodResourceTypeAnnotationKey: string(podutil.BestEffortPod)},
				},
				Spec: v1.PodSpec{
					NodeName: "node",
				},
			},
		}...,
	)

	if node.NumPods() != 4 {
		t.Errorf("NodeInfoImpl.GetPods() = %v, want %v", node.NumPods(), 4)
	}

	type args struct {
		resourceType podutil.PodResourceType
	}
	tests := []struct {
		name string
		args args
		want []string
	}{
		{
			name: "Get GT Pods",
			args: args{podutil.GuaranteedPod},
			want: []string{"p3"},
		},
		{
			name: "Get GT Pods",
			args: args{podutil.BestEffortPod},
			want: []string{"p4"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := node.GetVictimCandidates(NewPartitionInfo(math.MinInt64, math.MaxInt32, tt.args.resourceType))
			if len(got) != len(tt.want) {
				t.Errorf("NodeInfoImpl.GetVictimCandidates() = %v, want %v", got, tt.want)
			}
			for i := range got {
				if got[i].PodKey != tt.want[i] {
					t.Errorf("NodeInfoImpl.GetVictimCandidates() at index = %v, PodKey = %v, want %v", i, got[i].PodKey, tt.want)
				}
			}
		})
	}
}

func TestNodeInfoImpl_GetOccupiableResources(t *testing.T) {
	var lowPriority, midPriority, highPriority, infPriority int32
	lowPriority, midPriority, highPriority, infPriority = 50, 100, 150, 1e9
	podRequirements := v1.ResourceRequirements{
		Requests: v1.ResourceList{
			v1.ResourceCPU:    resource.MustParse("100m"),
			v1.ResourceMemory: resource.MustParse("100"),
		},
	}

	node := NewNodeInfo(
		[]*v1.Pod{
			// 1. Pod doesn't have resource type.
			{
				ObjectMeta: metav1.ObjectMeta{
					UID:         types.UID("p1"),
					Name:        "p1",
					Annotations: map[string]string{podutil.PodResourceTypeAnnotationKey: "invalid-value"},
				},
				Spec: v1.PodSpec{
					NodeName:   "node",
					Containers: []v1.Container{{Resources: podRequirements}},
				},
			},
			// 2. GT Pods
			{
				ObjectMeta: metav1.ObjectMeta{
					UID:         types.UID("p2"),
					Name:        "p2",
					Annotations: map[string]string{podutil.PodResourceTypeAnnotationKey: string(podutil.GuaranteedPod)},
				},
				Spec: v1.PodSpec{
					Priority:   &lowPriority,
					NodeName:   "node",
					Containers: []v1.Container{{Resources: podRequirements}},
				},
			},
			{
				ObjectMeta: metav1.ObjectMeta{
					UID:         types.UID("p3"),
					Name:        "p3",
					Annotations: map[string]string{podutil.PodResourceTypeAnnotationKey: string(podutil.GuaranteedPod)},
				},
				Spec: v1.PodSpec{
					Priority:   &midPriority,
					NodeName:   "node",
					Containers: []v1.Container{{Resources: podRequirements}},
				},
			},
			{
				ObjectMeta: metav1.ObjectMeta{
					UID:         types.UID("p4"),
					Name:        "p4",
					Annotations: map[string]string{podutil.PodResourceTypeAnnotationKey: string(podutil.GuaranteedPod)},
				},
				Spec: v1.PodSpec{
					Priority:   &midPriority,
					NodeName:   "node",
					Containers: []v1.Container{{Resources: podRequirements}},
				},
			},
			{
				ObjectMeta: metav1.ObjectMeta{
					UID:         types.UID("p5"),
					Name:        "p5",
					Annotations: map[string]string{podutil.PodResourceTypeAnnotationKey: string(podutil.GuaranteedPod)},
				},
				Spec: v1.PodSpec{
					Priority:   &highPriority,
					NodeName:   "node",
					Containers: []v1.Container{{Resources: podRequirements}},
				},
			},
		}...,
	)
	node.SetNode(
		&v1.Node{
			ObjectMeta: metav1.ObjectMeta{
				Name: "node",
			},
			Status: v1.NodeStatus{
				Capacity: v1.ResourceList{
					v1.ResourceCPU:    resource.MustParse("1000m"),
					v1.ResourceMemory: resource.MustParse("1000"),
				},
				Allocatable: v1.ResourceList{
					v1.ResourceCPU:    resource.MustParse("1000m"),
					v1.ResourceMemory: resource.MustParse("1000"),
				},
			},
		},
	)

	type args struct {
		partitionInfo *PodPartitionInfo
	}
	tests := []struct {
		name string
		args args
		want *Resource
	}{
		{
			name: "lowPriority Partition",
			args: args{NewPartitionInfo(math.MinInt64, int64(lowPriority), podutil.GuaranteedPod)},
			want: &Resource{MilliCPU: 500, Memory: 500},
		},
		{
			name: "midPriority Partition",
			args: args{NewPartitionInfo(math.MinInt64, int64(midPriority), podutil.GuaranteedPod)},
			want: &Resource{MilliCPU: 600, Memory: 600},
		},
		{
			name: "highPriority Partition",
			args: args{NewPartitionInfo(math.MinInt64, int64(highPriority), podutil.GuaranteedPod)},
			want: &Resource{MilliCPU: 800, Memory: 800},
		},
		{
			name: "infPriority Partition",
			args: args{NewPartitionInfo(math.MinInt64, int64(infPriority), podutil.GuaranteedPod)},
			want: &Resource{MilliCPU: 900, Memory: 900},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := node.GetOccupiableResources(tt.args.partitionInfo); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("NodeInfoImpl.GetReleasableResources() = %v, want %v", got, tt.want)
			}
		})
	}
}

func BenchmarkNodeInfoClone(b *testing.B) {
	makePod := func(namespace, name, uid string, req corev1.ResourceList) *corev1.Pod {
		return &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: namespace,
				Name:      name,
				UID:       types.UID(uid),
			},
			Spec: corev1.PodSpec{
				Containers: []corev1.Container{
					{
						Resources: v1.ResourceRequirements{
							Requests: req,
						},
					},
				},
				NodeName: "n", // Must have NodeName to be stored in Splay tree.
			},
		}
	}
	type testcase struct {
		name  string
		pod   int
		clone int
	}

	testcases := []testcase{
		{name: "1k pod, 10k clone", pod: 1000, clone: 10000},
		{name: "5k pod, 10k clone", pod: 5000, clone: 10000},
		{name: "10k pod, 10k clone", pod: 10000, clone: 10000},
		{name: "10k pod, 5k clone", pod: 10000, clone: 5000},
		{name: "10k pod, 1k clone", pod: 10000, clone: 1000},
		{name: "50k pod, 1k clone", pod: 50000, clone: 1000},
		{name: "100k pod, 1k clone", pod: 100000, clone: 1000},
	}

	for _, tc := range testcases {
		// 1. Prepare Pods
		pods := make([]*v1.Pod, 0)
		for i := 0; i < tc.pod; i++ {
			name := strconv.Itoa(i)
			pods = append(pods, makePod("default", name, name, corev1.ResourceList{"cpu": resource.MustParse("10"), "memory": resource.MustParse("10Gi")}))
		}
		// 2. Prepare node
		nodeInfo := NewNodeInfo(pods...)

		start := time.Now()
		for i := 0; i < tc.clone; i++ {
			nodeInfo.Clone()
		}
		cost := float64(time.Since(start).Milliseconds())
		fmt.Printf("testcase[%v] total cost: %v, avg cost: %v\n", tc.name, cost, cost/float64(tc.clone))
	}
}

func TestGetFreeResourcesInNumaList(t *testing.T) {
	numaTopologyStatus := &NumaTopologyStatus{
		topology: map[int]*NumaStatus{
			0: {
				resourceStatuses: map[string]*ResourceStatus{
					"cpu": {
						Available: resource.NewQuantity(20, resource.DecimalSI),
						Users:     sets.NewString("p1/p1/p1"),
					},
					"memory": {
						Available: resource.NewQuantity(10*1024*1024*1024, resource.BinarySI),
						Users:     sets.NewString("p1/p1/p1", "p2/p2/p2"),
					},
				},
			},
			1: {
				resourceStatuses: map[string]*ResourceStatus{
					"cpu": {
						Available: resource.NewQuantity(24, resource.DecimalSI),
					},
					"memory": {
						Available: resource.NewQuantity(60*1024*1024*1024, resource.BinarySI),
					},
				},
			},
		},
	}
	gotFreeResources := numaTopologyStatus.GetFreeResourcesInNumaList([]int{0, 1})
	expectedCPU := resource.NewQuantity(44, resource.DecimalSI)
	expectedMemory := resource.NewQuantity(70*1024*1024*1024, resource.BinarySI)
	expectedFreeResources := v1.ResourceList{
		"cpu":    *expectedCPU,
		"memory": *expectedMemory,
	}
	if !reflect.DeepEqual(expectedFreeResources, gotFreeResources) {
		t.Errorf("expected free resources: %v, but got: %v", expectedFreeResources, gotFreeResources)
	}

	gotFreeResources = numaTopologyStatus.GetFreeResourcesInNumaList([]int{0})
	expectedCPU = resource.NewQuantity(20, resource.DecimalSI)
	expectedMemory = resource.NewQuantity(10*1024*1024*1024, resource.BinarySI)
	expectedFreeResources = v1.ResourceList{
		"cpu":    *expectedCPU,
		"memory": *expectedMemory,
	}
	if !reflect.DeepEqual(expectedFreeResources, gotFreeResources) {
		t.Errorf("expected free resources: %v, but got: %v", expectedFreeResources, gotFreeResources)
	}
}

func TestUpdateFreeNumaForConflictResources(t *testing.T) {
	utilfeature.DefaultMutableFeatureGate.SetFromMap(map[string]bool{string(godelfeatures.NonNativeResourceSchedulingSupport): true})

	setPod := func(name string, res map[v1.ResourceName]string, annotations map[string]string) *v1.Pod {
		return &v1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Namespace:   "default",
				Name:        name,
				UID:         types.UID(name),
				Annotations: annotations,
			},
			Spec: v1.PodSpec{
				Containers: []v1.Container{
					{
						Resources: v1.ResourceRequirements{
							Requests: *parseResourceList(res),
						},
					},
				},
			},
		}
	}

	cnr := &katalystv1alpha1.CustomNodeResource{
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
									"cpu":            resource.MustParse("24"),
									"memory":         resource.MustParse("64Gi"),
									"nvidia.com/gpu": resource.MustParse("2"),
								},
								Capacity: &corev1.ResourceList{
									"cpu":            resource.MustParse("24"),
									"memory":         resource.MustParse("64Gi"),
									"nvidia.com/gpu": resource.MustParse("2"),
								},
							},
							Allocations: []*katalystv1alpha1.Allocation{
								{
									Consumer: "default/p1/p1",
									Requests: &corev1.ResourceList{
										"cpu":    resource.MustParse("1"),
										"memory": resource.MustParse("1Gi"),
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
							Name: "1",
							Resources: katalystv1alpha1.Resources{
								Allocatable: &corev1.ResourceList{
									"cpu":            resource.MustParse("23"),
									"memory":         resource.MustParse("62Gi"),
									"nvidia.com/gpu": resource.MustParse("2"),
								},
								Capacity: &corev1.ResourceList{
									"cpu":            resource.MustParse("24"),
									"memory":         resource.MustParse("64Gi"),
									"nvidia.com/gpu": resource.MustParse("2"),
								},
							},
							Allocations: []*katalystv1alpha1.Allocation{
								{
									Consumer: "default/p2/p2",
									Requests: &corev1.ResourceList{
										"cpu": resource.MustParse("1"),
									},
								},
								{
									Consumer: "default/p3/p3",
									Requests: &corev1.ResourceList{
										"memory": resource.MustParse("1"),
									},
								},
							},
						},
					},
				},
			},
		},
	}

	cnrAfterRemoveP1 := &katalystv1alpha1.CustomNodeResource{
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
									"cpu":            resource.MustParse("24"),
									"memory":         resource.MustParse("64Gi"),
									"nvidia.com/gpu": resource.MustParse("2"),
								},
								Capacity: &corev1.ResourceList{
									"cpu":            resource.MustParse("24"),
									"memory":         resource.MustParse("64Gi"),
									"nvidia.com/gpu": resource.MustParse("2"),
								},
							},
							Allocations: []*katalystv1alpha1.Allocation{},
						},
					},
				},
				{
					Type: katalystv1alpha1.TopologyTypeSocket,
					Name: "1",
					Children: []*katalystv1alpha1.TopologyZone{
						{
							Type: katalystv1alpha1.TopologyTypeNuma,
							Name: "1",
							Resources: katalystv1alpha1.Resources{
								Allocatable: &corev1.ResourceList{
									"cpu":            resource.MustParse("23"),
									"memory":         resource.MustParse("62Gi"),
									"nvidia.com/gpu": resource.MustParse("2"),
								},
								Capacity: &corev1.ResourceList{
									"cpu":            resource.MustParse("24"),
									"memory":         resource.MustParse("64Gi"),
									"nvidia.com/gpu": resource.MustParse("2"),
								},
							},
							Allocations: []*katalystv1alpha1.Allocation{
								{
									Consumer: "default/p2/p2",
									Requests: &corev1.ResourceList{
										"cpu": resource.MustParse("1"),
									},
								},
								{
									Consumer: "default/p3/p3",
									Requests: &corev1.ResourceList{
										"memory": resource.MustParse("1"),
									},
								},
							},
						},
					},
				},
			},
		},
	}

	cnrAfterRemoveP2 := &katalystv1alpha1.CustomNodeResource{
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
									"cpu":            resource.MustParse("24"),
									"memory":         resource.MustParse("64Gi"),
									"nvidia.com/gpu": resource.MustParse("2"),
								},
								Capacity: &corev1.ResourceList{
									"cpu":            resource.MustParse("24"),
									"memory":         resource.MustParse("64Gi"),
									"nvidia.com/gpu": resource.MustParse("2"),
								},
							},
							Allocations: []*katalystv1alpha1.Allocation{},
						},
					},
				},
				{
					Type: katalystv1alpha1.TopologyTypeSocket,
					Name: "1",
					Children: []*katalystv1alpha1.TopologyZone{
						{
							Type: katalystv1alpha1.TopologyTypeNuma,
							Name: "1",
							Resources: katalystv1alpha1.Resources{
								Allocatable: &corev1.ResourceList{
									"cpu":            resource.MustParse("23"),
									"memory":         resource.MustParse("62Gi"),
									"nvidia.com/gpu": resource.MustParse("2"),
								},
								Capacity: &corev1.ResourceList{
									"cpu":            resource.MustParse("24"),
									"memory":         resource.MustParse("64Gi"),
									"nvidia.com/gpu": resource.MustParse("2"),
								},
							},
							Allocations: []*katalystv1alpha1.Allocation{
								{
									Consumer: "default/p3/p3",
									Requests: &corev1.ResourceList{
										"memory": resource.MustParse("1"),
									},
								},
							},
						},
					},
				},
			},
		},
	}

	cnrAfterRemoveP3 := &katalystv1alpha1.CustomNodeResource{
		ObjectMeta: metav1.ObjectMeta{
			Name: "n",
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
									"cpu":            resource.MustParse("24"),
									"memory":         resource.MustParse("64Gi"),
									"nvidia.com/gpu": resource.MustParse("2"),
								},
								Capacity: &corev1.ResourceList{
									"cpu":            resource.MustParse("24"),
									"memory":         resource.MustParse("64Gi"),
									"nvidia.com/gpu": resource.MustParse("2"),
								},
							},
							Allocations: []*katalystv1alpha1.Allocation{},
						},
					},
				},
				{
					Type: katalystv1alpha1.TopologyTypeSocket,
					Name: "1",
					Children: []*katalystv1alpha1.TopologyZone{
						{
							Type: katalystv1alpha1.TopologyTypeNuma,
							Name: "1",
							Resources: katalystv1alpha1.Resources{
								Allocatable: &corev1.ResourceList{
									"cpu":            resource.MustParse("23"),
									"memory":         resource.MustParse("62Gi"),
									"nvidia.com/gpu": resource.MustParse("2"),
								},
								Capacity: &corev1.ResourceList{
									"cpu":            resource.MustParse("24"),
									"memory":         resource.MustParse("64Gi"),
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

	p1 := setPod("p1", map[v1.ResourceName]string{"cpu": "1", "memory": "1"}, nil)
	p2 := setPod("p2", map[v1.ResourceName]string{"cpu": "1"}, nil)
	p3 := setPod("p3", map[v1.ResourceName]string{"memory": "1"}, nil)
	ni := fakeNodeInfo(p1, p2, p3)

	ni.SetCNR(cnr)
	expected := map[int]sets.Int{}
	if !reflect.DeepEqual(expected, ni.GetNumaTopologyStatus().socketToFreeNumasOfConflictResources) {
		t.Errorf("expected %v but got %v", expected, ni.GetNumaTopologyStatus().socketToFreeNumasOfConflictResources)
	}

	ni.RemovePod(p1, true)
	expected = map[int]sets.Int{
		0: sets.NewInt(0),
	}
	if !reflect.DeepEqual(expected, ni.GetNumaTopologyStatus().socketToFreeNumasOfConflictResources) {
		t.Errorf("expected %v but got %v", expected, ni.GetNumaTopologyStatus().socketToFreeNumasOfConflictResources)
	}

	ni.RemovePod(p2, true)
	expected = map[int]sets.Int{
		0: sets.NewInt(0),
	}
	if !reflect.DeepEqual(expected, ni.GetNumaTopologyStatus().socketToFreeNumasOfConflictResources) {
		t.Errorf("expected %v but got %v", expected, ni.GetNumaTopologyStatus().socketToFreeNumasOfConflictResources)
	}

	ni.RemovePod(p3, true)
	expected = map[int]sets.Int{
		0: sets.NewInt(0),
		1: sets.NewInt(1),
	}
	if !reflect.DeepEqual(expected, ni.GetNumaTopologyStatus().socketToFreeNumasOfConflictResources) {
		t.Errorf("expected %v but got %v", expected, ni.GetNumaTopologyStatus().socketToFreeNumasOfConflictResources)
	}

	ni.AddPod(p1)
	expected = map[int]sets.Int{
		1: sets.NewInt(1),
	}
	if !reflect.DeepEqual(expected, ni.GetNumaTopologyStatus().socketToFreeNumasOfConflictResources) {
		t.Errorf("expected %v but got %v", expected, ni.GetNumaTopologyStatus().socketToFreeNumasOfConflictResources)
	}

	ni.AddPod(p2)
	expected = map[int]sets.Int{}
	if !reflect.DeepEqual(expected, ni.GetNumaTopologyStatus().socketToFreeNumasOfConflictResources) {
		t.Errorf("expected %v but got %v", expected, ni.GetNumaTopologyStatus().socketToFreeNumasOfConflictResources)
	}

	ni.AddPod(p3)
	expected = map[int]sets.Int{}
	if !reflect.DeepEqual(expected, ni.GetNumaTopologyStatus().socketToFreeNumasOfConflictResources) {
		t.Errorf("expected %v but got %v", expected, ni.GetNumaTopologyStatus().socketToFreeNumasOfConflictResources)
	}

	ni.RemovePod(p1, false)
	expected = map[int]sets.Int{}
	if !reflect.DeepEqual(expected, ni.GetNumaTopologyStatus().socketToFreeNumasOfConflictResources) {
		t.Errorf("expected %v but got %v", expected, ni.GetNumaTopologyStatus().socketToFreeNumasOfConflictResources)
	}
	ni.SetCNR(cnrAfterRemoveP1)
	expected = map[int]sets.Int{
		0: sets.NewInt(0),
	}
	if !reflect.DeepEqual(expected, ni.GetNumaTopologyStatus().socketToFreeNumasOfConflictResources) {
		t.Errorf("expected %v but got %v", expected, ni.GetNumaTopologyStatus().socketToFreeNumasOfConflictResources)
	}

	ni.RemovePod(p2, false)
	expected = map[int]sets.Int{
		0: sets.NewInt(0),
	}
	if !reflect.DeepEqual(expected, ni.GetNumaTopologyStatus().socketToFreeNumasOfConflictResources) {
		t.Errorf("expected %v but got %v", expected, ni.GetNumaTopologyStatus().socketToFreeNumasOfConflictResources)
	}
	ni.SetCNR(cnrAfterRemoveP2)
	expected = map[int]sets.Int{
		0: sets.NewInt(0),
	}
	if !reflect.DeepEqual(expected, ni.GetNumaTopologyStatus().socketToFreeNumasOfConflictResources) {
		t.Errorf("expected %v but got %v", expected, ni.GetNumaTopologyStatus().socketToFreeNumasOfConflictResources)
	}

	ni.RemovePod(p3, false)
	expected = map[int]sets.Int{
		0: sets.NewInt(0),
	}
	if !reflect.DeepEqual(expected, ni.GetNumaTopologyStatus().socketToFreeNumasOfConflictResources) {
		t.Errorf("expected %v but got %v", expected, ni.GetNumaTopologyStatus().socketToFreeNumasOfConflictResources)
	}
	ni.SetCNR(cnrAfterRemoveP3)
	expected = map[int]sets.Int{
		0: sets.NewInt(0),
		1: sets.NewInt(1),
	}
	if !reflect.DeepEqual(expected, ni.GetNumaTopologyStatus().socketToFreeNumasOfConflictResources) {
		t.Errorf("expected %v but got %v", expected, ni.GetNumaTopologyStatus().socketToFreeNumasOfConflictResources)
	}
}
