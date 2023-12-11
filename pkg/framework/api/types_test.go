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
	"reflect"
	"strings"
	"testing"

	nodev1alpha1 "github.com/kubewharf/godel-scheduler-api/pkg/apis/node/v1alpha1"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	utilfeature "k8s.io/apiserver/pkg/util/feature"
	"k8s.io/component-base/featuregate"

	"github.com/kubewharf/godel-scheduler/pkg/util"
	"github.com/kubewharf/godel-scheduler/pkg/util/features"
)

func TestNewResource(t *testing.T) {
	tests := []struct {
		resourceList v1.ResourceList
		expected     *Resource
	}{
		{
			resourceList: map[v1.ResourceName]resource.Quantity{},
			expected:     &Resource{},
		},
		{
			resourceList: map[v1.ResourceName]resource.Quantity{
				v1.ResourceCPU:                      *resource.NewScaledQuantity(4, -3),
				v1.ResourceMemory:                   *resource.NewQuantity(2000, resource.BinarySI),
				v1.ResourcePods:                     *resource.NewQuantity(80, resource.BinarySI),
				v1.ResourceEphemeralStorage:         *resource.NewQuantity(5000, resource.BinarySI),
				"scalar.test/" + "scalar1":          *resource.NewQuantity(1, resource.DecimalSI),
				v1.ResourceHugePagesPrefix + "test": *resource.NewQuantity(2, resource.BinarySI),
			},
			expected: &Resource{
				MilliCPU:         4,
				Memory:           2000,
				EphemeralStorage: 5000,
				AllowedPodNumber: 80,
				ScalarResources:  map[v1.ResourceName]int64{"scalar.test/scalar1": 1, "hugepages-test": 2},
			},
		},
	}

	for _, test := range tests {
		r := NewResource(test.resourceList)
		if !reflect.DeepEqual(test.expected, r) {
			t.Errorf("expected: %#v, got: %#v", test.expected, r)
		}
	}
}

func TestResourceList(t *testing.T) {
	tests := []struct {
		resource *Resource
		expected v1.ResourceList
	}{
		{
			resource: &Resource{},
			expected: map[v1.ResourceName]resource.Quantity{
				v1.ResourceCPU:              *resource.NewScaledQuantity(0, -3),
				v1.ResourceMemory:           *resource.NewQuantity(0, resource.BinarySI),
				v1.ResourcePods:             *resource.NewQuantity(0, resource.BinarySI),
				v1.ResourceEphemeralStorage: *resource.NewQuantity(0, resource.BinarySI),
			},
		},
		{
			resource: &Resource{
				MilliCPU:         4,
				Memory:           2000,
				EphemeralStorage: 5000,
				AllowedPodNumber: 80,
				ScalarResources: map[v1.ResourceName]int64{
					"scalar.test/scalar1":        1,
					"hugepages-test":             2,
					"attachable-volumes-aws-ebs": 39,
				},
			},
			expected: map[v1.ResourceName]resource.Quantity{
				v1.ResourceCPU:                      *resource.NewScaledQuantity(4, -3),
				v1.ResourceMemory:                   *resource.NewQuantity(2000, resource.BinarySI),
				v1.ResourcePods:                     *resource.NewQuantity(80, resource.BinarySI),
				v1.ResourceEphemeralStorage:         *resource.NewQuantity(5000, resource.BinarySI),
				"scalar.test/" + "scalar1":          *resource.NewQuantity(1, resource.DecimalSI),
				"attachable-volumes-aws-ebs":        *resource.NewQuantity(39, resource.DecimalSI),
				v1.ResourceHugePagesPrefix + "test": *resource.NewQuantity(2, resource.BinarySI),
			},
		},
	}

	for _, test := range tests {
		rl := test.resource.ResourceList()
		if !reflect.DeepEqual(test.expected, rl) {
			t.Errorf("expected: %#v, got: %#v", test.expected, rl)
		}
	}
}

func TestResourceClone(t *testing.T) {
	tests := []struct {
		resource *Resource
		expected *Resource
	}{
		{
			resource: &Resource{},
			expected: &Resource{},
		},
		{
			resource: &Resource{
				MilliCPU:         4,
				Memory:           2000,
				EphemeralStorage: 5000,
				AllowedPodNumber: 80,
				ScalarResources:  map[v1.ResourceName]int64{"scalar.test/scalar1": 1, "hugepages-test": 2},
			},
			expected: &Resource{
				MilliCPU:         4,
				Memory:           2000,
				EphemeralStorage: 5000,
				AllowedPodNumber: 80,
				ScalarResources:  map[v1.ResourceName]int64{"scalar.test/scalar1": 1, "hugepages-test": 2},
			},
		},
	}

	for _, test := range tests {
		r := test.resource.Clone()
		// Modify the field to check if the result is a clone of the origin one.
		test.resource.MilliCPU += 1000
		if !reflect.DeepEqual(test.expected, r) {
			t.Errorf("expected: %#v, got: %#v", test.expected, r)
		}
	}
}

func TestResourceAddScalar(t *testing.T) {
	tests := []struct {
		resource       *Resource
		scalarName     v1.ResourceName
		scalarQuantity int64
		expected       *Resource
	}{
		{
			resource:       &Resource{},
			scalarName:     "scalar1",
			scalarQuantity: 100,
			expected: &Resource{
				ScalarResources: map[v1.ResourceName]int64{"scalar1": 100},
			},
		},
		{
			resource: &Resource{
				MilliCPU:         4,
				Memory:           2000,
				EphemeralStorage: 5000,
				AllowedPodNumber: 80,
				ScalarResources:  map[v1.ResourceName]int64{"hugepages-test": 2},
			},
			scalarName:     "scalar2",
			scalarQuantity: 200,
			expected: &Resource{
				MilliCPU:         4,
				Memory:           2000,
				EphemeralStorage: 5000,
				AllowedPodNumber: 80,
				ScalarResources:  map[v1.ResourceName]int64{"hugepages-test": 2, "scalar2": 200},
			},
		},
	}

	for _, test := range tests {
		test.resource.AddScalar(test.scalarName, test.scalarQuantity)
		if !reflect.DeepEqual(test.expected, test.resource) {
			t.Errorf("expected: %#v, got: %#v", test.expected, test.resource)
		}
	}
}

func TestSetMaxResource(t *testing.T) {
	tests := []struct {
		resource     *Resource
		resourceList v1.ResourceList
		expected     *Resource
	}{
		{
			resource: &Resource{},
			resourceList: map[v1.ResourceName]resource.Quantity{
				v1.ResourceCPU:              *resource.NewScaledQuantity(4, -3),
				v1.ResourceMemory:           *resource.NewQuantity(2000, resource.BinarySI),
				v1.ResourceEphemeralStorage: *resource.NewQuantity(5000, resource.BinarySI),
			},
			expected: &Resource{
				MilliCPU:         4,
				Memory:           2000,
				EphemeralStorage: 5000,
			},
		},
		{
			resource: &Resource{
				MilliCPU:         4,
				Memory:           4000,
				EphemeralStorage: 5000,
				ScalarResources:  map[v1.ResourceName]int64{"scalar.test/scalar1": 1, "hugepages-test": 2},
			},
			resourceList: map[v1.ResourceName]resource.Quantity{
				v1.ResourceCPU:                      *resource.NewScaledQuantity(4, -3),
				v1.ResourceMemory:                   *resource.NewQuantity(2000, resource.BinarySI),
				v1.ResourceEphemeralStorage:         *resource.NewQuantity(7000, resource.BinarySI),
				"scalar.test/scalar1":               *resource.NewQuantity(4, resource.DecimalSI),
				v1.ResourceHugePagesPrefix + "test": *resource.NewQuantity(5, resource.BinarySI),
			},
			expected: &Resource{
				MilliCPU:         4,
				Memory:           4000,
				EphemeralStorage: 7000,
				ScalarResources:  map[v1.ResourceName]int64{"scalar.test/scalar1": 4, "hugepages-test": 5},
			},
		},
	}

	for _, test := range tests {
		test.resource.SetMaxResource(test.resourceList)
		if !reflect.DeepEqual(test.expected, test.resource) {
			t.Errorf("expected: %#v, got: %#v", test.expected, test.resource)
		}
	}
}

type testingMode interface {
	Fatalf(format string, args ...interface{})
}

func makeBasePod(t testingMode, nodeName, objName, cpu, mem, extended string, ports []v1.ContainerPort) *v1.Pod {
	req := v1.ResourceList{}
	if cpu != "" {
		req = v1.ResourceList{
			v1.ResourceCPU:    resource.MustParse(cpu),
			v1.ResourceMemory: resource.MustParse(mem),
		}
		if extended != "" {
			parts := strings.Split(extended, ":")
			if len(parts) != 2 {
				t.Fatalf("Invalid extended resource string: \"%s\"", extended)
			}
			req[v1.ResourceName(parts[0])] = resource.MustParse(parts[1])
		}
	}
	return &v1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			UID:       types.UID(objName),
			Namespace: "node_info_cache_test",
			Name:      objName,
		},
		Spec: v1.PodSpec{
			Containers: []v1.Container{{
				Resources: v1.ResourceRequirements{
					Requests: req,
				},
				Ports: ports,
			}},
			NodeName: nodeName,
		},
	}
}

type hostPortInfoParam struct {
	protocol, ip string
	port         int32
}

func TestHostPortInfo_AddRemove(t *testing.T) {
	tests := []struct {
		desc    string
		added   []hostPortInfoParam
		removed []hostPortInfoParam
		length  int
	}{
		{
			desc: "normal add case",
			added: []hostPortInfoParam{
				{"TCP", "127.0.0.1", 79},
				{"UDP", "127.0.0.1", 80},
				{"TCP", "127.0.0.1", 81},
				{"TCP", "127.0.0.1", 82},
				// this might not make sense in real case, but the struct doesn't forbid it.
				{"TCP", "0.0.0.0", 79},
				{"UDP", "0.0.0.0", 80},
				{"TCP", "0.0.0.0", 81},
				{"TCP", "0.0.0.0", 82},
				{"TCP", "0.0.0.0", 0},
				{"TCP", "0.0.0.0", -1},
			},
			length: 8,
		},
		{
			desc: "empty ip and protocol add should work",
			added: []hostPortInfoParam{
				{"", "127.0.0.1", 79},
				{"UDP", "127.0.0.1", 80},
				{"", "127.0.0.1", 81},
				{"", "127.0.0.1", 82},
				{"", "", 79},
				{"UDP", "", 80},
				{"", "", 81},
				{"", "", 82},
				{"", "", 0},
				{"", "", -1},
			},
			length: 8,
		},
		{
			desc: "normal remove case",
			added: []hostPortInfoParam{
				{"TCP", "127.0.0.1", 79},
				{"UDP", "127.0.0.1", 80},
				{"TCP", "127.0.0.1", 81},
				{"TCP", "127.0.0.1", 82},
				{"TCP", "0.0.0.0", 79},
				{"UDP", "0.0.0.0", 80},
				{"TCP", "0.0.0.0", 81},
				{"TCP", "0.0.0.0", 82},
			},
			removed: []hostPortInfoParam{
				{"TCP", "127.0.0.1", 79},
				{"UDP", "127.0.0.1", 80},
				{"TCP", "127.0.0.1", 81},
				{"TCP", "127.0.0.1", 82},
				{"TCP", "0.0.0.0", 79},
				{"UDP", "0.0.0.0", 80},
				{"TCP", "0.0.0.0", 81},
				{"TCP", "0.0.0.0", 82},
			},
			length: 0,
		},
		{
			desc: "empty ip and protocol remove should work",
			added: []hostPortInfoParam{
				{"TCP", "127.0.0.1", 79},
				{"UDP", "127.0.0.1", 80},
				{"TCP", "127.0.0.1", 81},
				{"TCP", "127.0.0.1", 82},
				{"TCP", "0.0.0.0", 79},
				{"UDP", "0.0.0.0", 80},
				{"TCP", "0.0.0.0", 81},
				{"TCP", "0.0.0.0", 82},
			},
			removed: []hostPortInfoParam{
				{"", "127.0.0.1", 79},
				{"", "127.0.0.1", 81},
				{"", "127.0.0.1", 82},
				{"UDP", "127.0.0.1", 80},
				{"", "", 79},
				{"", "", 81},
				{"", "", 82},
				{"UDP", "", 80},
			},
			length: 0,
		},
	}

	for _, test := range tests {
		hp := make(HostPortInfo)
		for _, param := range test.added {
			hp.Add(param.ip, param.protocol, param.port)
		}
		for _, param := range test.removed {
			hp.Remove(param.ip, param.protocol, param.port)
		}
		if hp.Len() != test.length {
			t.Errorf("%v failed: expect length %d; got %d", test.desc, test.length, hp.Len())
			t.Error(hp)
		}
	}
}

func TestHostPortInfo_Check(t *testing.T) {
	tests := []struct {
		desc   string
		added  []hostPortInfoParam
		check  hostPortInfoParam
		expect bool
	}{
		{
			desc: "empty check should check 0.0.0.0 and TCP",
			added: []hostPortInfoParam{
				{"TCP", "127.0.0.1", 80},
			},
			check:  hostPortInfoParam{"", "", 81},
			expect: false,
		},
		{
			desc: "empty check should check 0.0.0.0 and TCP (conflicted)",
			added: []hostPortInfoParam{
				{"TCP", "127.0.0.1", 80},
			},
			check:  hostPortInfoParam{"", "", 80},
			expect: true,
		},
		{
			desc: "empty port check should pass",
			added: []hostPortInfoParam{
				{"TCP", "127.0.0.1", 80},
			},
			check:  hostPortInfoParam{"", "", 0},
			expect: false,
		},
		{
			desc: "0.0.0.0 should check all registered IPs",
			added: []hostPortInfoParam{
				{"TCP", "127.0.0.1", 80},
			},
			check:  hostPortInfoParam{"TCP", "0.0.0.0", 80},
			expect: true,
		},
		{
			desc: "0.0.0.0 with different protocol should be allowed",
			added: []hostPortInfoParam{
				{"UDP", "127.0.0.1", 80},
			},
			check:  hostPortInfoParam{"TCP", "0.0.0.0", 80},
			expect: false,
		},
		{
			desc: "0.0.0.0 with different port should be allowed",
			added: []hostPortInfoParam{
				{"TCP", "127.0.0.1", 79},
				{"TCP", "127.0.0.1", 81},
				{"TCP", "127.0.0.1", 82},
			},
			check:  hostPortInfoParam{"TCP", "0.0.0.0", 80},
			expect: false,
		},
		{
			desc: "normal ip should check all registered 0.0.0.0",
			added: []hostPortInfoParam{
				{"TCP", "0.0.0.0", 80},
			},
			check:  hostPortInfoParam{"TCP", "127.0.0.1", 80},
			expect: true,
		},
		{
			desc: "normal ip with different port/protocol should be allowed (0.0.0.0)",
			added: []hostPortInfoParam{
				{"TCP", "0.0.0.0", 79},
				{"UDP", "0.0.0.0", 80},
				{"TCP", "0.0.0.0", 81},
				{"TCP", "0.0.0.0", 82},
			},
			check:  hostPortInfoParam{"TCP", "127.0.0.1", 80},
			expect: false,
		},
		{
			desc: "normal ip with different port/protocol should be allowed",
			added: []hostPortInfoParam{
				{"TCP", "127.0.0.1", 79},
				{"UDP", "127.0.0.1", 80},
				{"TCP", "127.0.0.1", 81},
				{"TCP", "127.0.0.1", 82},
			},
			check:  hostPortInfoParam{"TCP", "127.0.0.1", 80},
			expect: false,
		},
	}

	for _, test := range tests {
		hp := make(HostPortInfo)
		for _, param := range test.added {
			hp.Add(param.ip, param.protocol, param.port)
		}
		if hp.CheckConflict(test.check.ip, test.check.protocol, test.check.port) != test.expect {
			t.Errorf("%v failed, expected %t; got %t", test.desc, test.expect, !test.expect)
		}
	}
}

func TestGetAllocatableResources(t *testing.T) {
	tests := []struct {
		desc   string
		node   *v1.Node
		nmnode *nodev1alpha1.NMNode
		expect *Resource
	}{
		{
			desc: "capacity and allocatable exist",
			node: &v1.Node{
				Status: v1.NodeStatus{
					Capacity: map[v1.ResourceName]resource.Quantity{
						v1.ResourceCPU: resource.MustParse("200m"),
					},
					Allocatable: map[v1.ResourceName]resource.Quantity{
						v1.ResourceCPU: resource.MustParse("190m"),
					},
				},
			},
			nmnode: &nodev1alpha1.NMNode{
				Status: nodev1alpha1.NMNodeStatus{
					ResourceCapacity: &v1.ResourceList{
						v1.ResourceCPU: resource.MustParse("200m"),
					},
					ResourceAllocatable: &v1.ResourceList{
						v1.ResourceCPU: resource.MustParse("180m"),
					},
				},
			},
			expect: &Resource{
				MilliCPU: 170,
			},
		},
		{
			desc: "node has extra resource",
			node: &v1.Node{
				Status: v1.NodeStatus{
					Capacity: map[v1.ResourceName]resource.Quantity{
						v1.ResourceCPU:    resource.MustParse("200m"),
						v1.ResourceMemory: resource.MustParse("200"),
					},
					Allocatable: map[v1.ResourceName]resource.Quantity{
						v1.ResourceCPU:    resource.MustParse("200m"),
						v1.ResourceMemory: resource.MustParse("200"),
					},
				},
			},
			nmnode: &nodev1alpha1.NMNode{
				Status: nodev1alpha1.NMNodeStatus{
					ResourceCapacity: &v1.ResourceList{
						v1.ResourceCPU: resource.MustParse("200m"),
					},
					ResourceAllocatable: &v1.ResourceList{
						v1.ResourceCPU: resource.MustParse("200m"),
					},
				},
			},
			expect: &Resource{
				MilliCPU:        200,
				Memory:          200,
				ScalarResources: nil,
			},
		},
		{
			desc: "nmnode has extra resource",
			node: &v1.Node{
				Status: v1.NodeStatus{
					Capacity: map[v1.ResourceName]resource.Quantity{
						v1.ResourceCPU: resource.MustParse("200m"),
					},
					Allocatable: map[v1.ResourceName]resource.Quantity{
						v1.ResourceCPU: resource.MustParse("200m"),
					},
				},
			},
			nmnode: &nodev1alpha1.NMNode{
				Status: nodev1alpha1.NMNodeStatus{
					ResourceCapacity: &v1.ResourceList{
						v1.ResourceCPU:    resource.MustParse("200m"),
						v1.ResourceMemory: resource.MustParse("200"),
					},
					ResourceAllocatable: &v1.ResourceList{
						v1.ResourceCPU:    resource.MustParse("200m"),
						v1.ResourceMemory: resource.MustParse("200"),
					},
				},
			},
			expect: &Resource{
				MilliCPU:        200,
				Memory:          200,
				ScalarResources: nil,
			},
		},
		{
			desc: "node and nmnode has different capacity on the same resource",
			node: &v1.Node{
				Status: v1.NodeStatus{
					Capacity: map[v1.ResourceName]resource.Quantity{
						v1.ResourceCPU: resource.MustParse("200m"),
					},
					Allocatable: map[v1.ResourceName]resource.Quantity{
						v1.ResourceCPU: resource.MustParse("190m"),
					},
				},
			},
			nmnode: &nodev1alpha1.NMNode{
				Status: nodev1alpha1.NMNodeStatus{
					ResourceCapacity: &v1.ResourceList{
						v1.ResourceCPU: resource.MustParse("100m"),
					},
					ResourceAllocatable: &v1.ResourceList{
						v1.ResourceCPU: resource.MustParse("99m"),
					},
				},
			},
			expect: &Resource{
				MilliCPU:        189,
				ScalarResources: nil,
			},
		},
		{
			desc: "node allocatable bigger than capacity",
			node: &v1.Node{
				Status: v1.NodeStatus{
					Capacity: map[v1.ResourceName]resource.Quantity{
						v1.ResourceCPU: resource.MustParse("200m"),
					},
					Allocatable: map[v1.ResourceName]resource.Quantity{
						v1.ResourceCPU: resource.MustParse("190m"),
					},
				},
			},
			nmnode: &nodev1alpha1.NMNode{
				Status: nodev1alpha1.NMNodeStatus{
					ResourceCapacity: &v1.ResourceList{
						v1.ResourceCPU: resource.MustParse("100m"),
					},
					ResourceAllocatable: &v1.ResourceList{
						v1.ResourceCPU: resource.MustParse("100m"),
					},
				},
			},
			expect: &Resource{
				MilliCPU:        190,
				ScalarResources: nil,
			},
		},
		{
			desc: "nmnode allocatable bigger than capacity",
			node: &v1.Node{
				Status: v1.NodeStatus{
					Capacity: map[v1.ResourceName]resource.Quantity{
						v1.ResourceCPU: resource.MustParse("200m"),
					},
					Allocatable: map[v1.ResourceName]resource.Quantity{
						v1.ResourceCPU: resource.MustParse("200m"),
					},
				},
			},
			nmnode: &nodev1alpha1.NMNode{
				Status: nodev1alpha1.NMNodeStatus{
					ResourceCapacity: &v1.ResourceList{
						v1.ResourceCPU: resource.MustParse("100m"),
					},
					ResourceAllocatable: &v1.ResourceList{
						v1.ResourceCPU: resource.MustParse("190m"),
					},
				},
			},
			expect: &Resource{
				MilliCPU:        200,
				ScalarResources: nil,
			},
		},
		{
			desc: "node resources with capacity only",
			node: &v1.Node{
				Status: v1.NodeStatus{
					Capacity: map[v1.ResourceName]resource.Quantity{
						v1.ResourceCPU: resource.MustParse("200m"),
					},
				},
			},
			nmnode: &nodev1alpha1.NMNode{
				Status: nodev1alpha1.NMNodeStatus{
					ResourceCapacity: &v1.ResourceList{
						v1.ResourceCPU: resource.MustParse("200m"),
					},
					ResourceAllocatable: &v1.ResourceList{
						v1.ResourceCPU: resource.MustParse("190m"),
					},
				},
			},
			expect: &Resource{
				MilliCPU:        190,
				ScalarResources: nil,
			},
		},
		{
			desc: "node resources with allocatable only",
			node: &v1.Node{
				Status: v1.NodeStatus{
					Allocatable: map[v1.ResourceName]resource.Quantity{
						v1.ResourceCPU: resource.MustParse("200m"),
					},
				},
			},
			nmnode: &nodev1alpha1.NMNode{
				Status: nodev1alpha1.NMNodeStatus{
					ResourceCapacity: &v1.ResourceList{
						v1.ResourceCPU: resource.MustParse("100m"),
					},
					ResourceAllocatable: &v1.ResourceList{
						v1.ResourceCPU:    resource.MustParse("90m"),
						v1.ResourceMemory: resource.MustParse("100"),
					},
				},
			},
			expect: &Resource{
				MilliCPU:        190,
				Memory:          100,
				ScalarResources: nil,
			},
		},
		{
			desc: "capacity resources only",
			node: &v1.Node{
				Status: v1.NodeStatus{
					Capacity: map[v1.ResourceName]resource.Quantity{
						v1.ResourceCPU: resource.MustParse("200m"),
					},
				},
			},
			nmnode: &nodev1alpha1.NMNode{
				Status: nodev1alpha1.NMNodeStatus{
					ResourceCapacity: &v1.ResourceList{
						v1.ResourceCPU: resource.MustParse("200m"),
					},
				},
			},
			expect: &Resource{},
		},
		{
			desc: "nmnode allocatable resources only",
			node: &v1.Node{
				Status: v1.NodeStatus{
					Capacity: map[v1.ResourceName]resource.Quantity{
						v1.ResourceCPU: resource.MustParse("200m"),
					},
				},
			},
			nmnode: &nodev1alpha1.NMNode{
				Status: nodev1alpha1.NMNodeStatus{
					ResourceAllocatable: &v1.ResourceList{
						v1.ResourceCPU: resource.MustParse("200m"),
					},
				},
			},
			expect: &Resource{
				MilliCPU: 200,
			},
		},
	}

	for _, test := range tests {
		got := getAllocatableResources(test.node, test.nmnode)
		if !reflect.DeepEqual(got, test.expect) {
			t.Errorf("%s test failed, expected %+v; got %+v", test.desc, test.expect, got)
		}
	}
}

var (
	onlyGPU = map[v1.ResourceName]string{
		util.ResourceGPU: "1",
	}
	smallRes = map[v1.ResourceName]string{
		v1.ResourceCPU:    "8",
		v1.ResourceMemory: "20Gi",
		util.ResourceGPU:  "1",
	}
	largeRes = map[v1.ResourceName]string{
		v1.ResourceCPU:    "24",
		v1.ResourceMemory: "40Gi",
		util.ResourceGPU:  "2",
	}
)

func parseResourceList(resourceMap map[v1.ResourceName]string) *v1.ResourceList {
	resourceList := make(v1.ResourceList)
	for k, v := range resourceMap {
		resourceList[k] = resource.MustParse(v)
	}
	return &resourceList
}

func numaTopologyStatusEqual(topology1, topology2 *NumaTopologyStatus) bool {
	if !podAllocationsMapEqual(topology1.podAllocations, topology2.podAllocations) {
		return false
	}
	if !numaTopologyMapEqual(topology1.topology, topology2.topology) {
		return false
	}
	if !resourcesEqual(topology1.requestsOfSharedCores, topology2.requestsOfSharedCores) {
		return false
	}
	if !resourcesEqual(topology1.availableOfSharedCores, topology2.availableOfSharedCores) {
		return false
	}
	if !reflect.DeepEqual(topology1.socketToFreeNumasOfConflictResources, topology2.socketToFreeNumasOfConflictResources) {
		return false
	}
	return true
}

func podAllocationsMapEqual(podAllocations1, podAllocations2 map[string]*PodAllocation) bool {
	if len(podAllocations1) != len(podAllocations2) {
		return false
	}
	for podKey, podAllocation := range podAllocations1 {
		if !podAllocation.Equal(podAllocations2[podKey]) {
			return false
		}
	}
	return true
}

func numaTopologyMapEqual(topology1, topology2 map[int]*NumaStatus) bool {
	if len(topology1) != len(topology2) {
		return false
	}
	for numa, numaStatus := range topology1 {
		if !numaStatus.Equal(topology2[numa]) {
			return false
		}
	}
	return true
}

func numaTopologyEqual(topology1, topology2 *NumaStatus) bool {
	if !reflect.DeepEqual(topology1.socketId, topology2.socketId) {
		return false
	}
	if !resourceStatuesEqual(topology1.resourceStatuses, topology2.resourceStatuses) {
		return false
	}
	return true
}

func resourceStatuesEqual(resourceStatuses1, resourceStatuses2 map[string]*ResourceStatus) bool {
	if len(resourceStatuses1) != len(resourceStatuses2) {
		return false
	}
	for resourceName, resourceStatus := range resourceStatuses1 {
		if !resourceStatuses2[resourceName].Equal(resourceStatus) {
			return false
		}
	}
	return true
}

func resourcesEqual(r1, r2 *Resource) bool {
	if r1 == nil {
		r1 = &Resource{}
	}
	if r2 == nil {
		r2 = &Resource{}
	}
	r1Copy := r1.Clone()
	r1Copy.ScalarResources = nil
	r2Copy := r2.Clone()
	r2Copy.ScalarResources = nil
	if !reflect.DeepEqual(r1Copy, r2Copy) {
		return false
	}
	for rName, rVal := range r1.ScalarResources {
		if rVal != r2.ScalarResources[rName] {
			return false
		}
	}
	for rName, rVal := range r2.ScalarResources {
		if rVal != r1.ScalarResources[rName] {
			return false
		}
	}
	return true
}

func TestSubResource(t *testing.T) {
	tests := []struct {
		name                          string
		localStorageCapacityIsolation bool
		r1                            *Resource
		r2                            *Resource
		expectedRes                   *Resource
	}{
		{
			name:                          "resource in r1, not in r2",
			localStorageCapacityIsolation: true,
			r1: &Resource{
				MilliCPU:         2000,
				Memory:           1024 * 2,
				AllowedPodNumber: 10,
				EphemeralStorage: 1024 * 3,
				ScalarResources: map[v1.ResourceName]int64{
					util.ResourceGPU: 4,
				},
			},
			r2: &Resource{
				MilliCPU:         1000,
				Memory:           1024,
				AllowedPodNumber: 2,
				EphemeralStorage: 1024 * 2,
				ScalarResources:  map[v1.ResourceName]int64{},
			},
			expectedRes: &Resource{
				MilliCPU:         1000,
				Memory:           1024,
				AllowedPodNumber: 8,
				EphemeralStorage: 1024,
				ScalarResources: map[v1.ResourceName]int64{
					util.ResourceGPU: 4,
				},
			},
		},
		{
			name:                          "resource in r2, not in r1",
			localStorageCapacityIsolation: true,
			r1: &Resource{
				MilliCPU:         2000,
				Memory:           1024 * 2,
				AllowedPodNumber: 10,
				EphemeralStorage: 1024 * 3,
				ScalarResources:  map[v1.ResourceName]int64{},
			},
			r2: &Resource{
				MilliCPU:         1000,
				Memory:           1024,
				AllowedPodNumber: 2,
				EphemeralStorage: 1024 * 2,
				ScalarResources: map[v1.ResourceName]int64{
					util.ResourceGPU: 4,
				},
			},
			expectedRes: &Resource{
				MilliCPU:         1000,
				Memory:           1024,
				AllowedPodNumber: 8,
				EphemeralStorage: 1024,
				ScalarResources: map[v1.ResourceName]int64{
					util.ResourceGPU: -4,
				},
			},
		},
		{
			name:                          "resource both in r1 & r2",
			localStorageCapacityIsolation: true,
			r1: &Resource{
				MilliCPU:         2000,
				Memory:           1024 * 2,
				AllowedPodNumber: 10,
				EphemeralStorage: 1024 * 3,
				ScalarResources: map[v1.ResourceName]int64{
					util.ResourceGPU: 4,
				},
			},
			r2: &Resource{
				MilliCPU:         1000,
				Memory:           1024,
				AllowedPodNumber: 2,
				EphemeralStorage: 1024 * 2,
				ScalarResources: map[v1.ResourceName]int64{
					util.ResourceGPU: 2,
				},
			},
			expectedRes: &Resource{
				MilliCPU:         1000,
				Memory:           1024,
				AllowedPodNumber: 8,
				EphemeralStorage: 1024,
				ScalarResources: map[v1.ResourceName]int64{
					util.ResourceGPU: 2,
				},
			},
		},
		{
			name:                          "resource both in r1 & r2, LocalStorageCapacityIsolation is false",
			localStorageCapacityIsolation: false,
			r1: &Resource{
				MilliCPU:         2000,
				Memory:           1024 * 2,
				AllowedPodNumber: 10,
				EphemeralStorage: 1024 * 3,
				ScalarResources: map[v1.ResourceName]int64{
					util.ResourceGPU: 4,
				},
			},
			r2: &Resource{
				MilliCPU:         1000,
				Memory:           1024,
				AllowedPodNumber: 2,
				EphemeralStorage: 1024 * 2,
				ScalarResources: map[v1.ResourceName]int64{
					util.ResourceGPU: 2,
				},
			},
			expectedRes: &Resource{
				MilliCPU:         1000,
				Memory:           1024,
				AllowedPodNumber: 8,
				EphemeralStorage: 1024 * 3,
				ScalarResources: map[v1.ResourceName]int64{
					util.ResourceGPU: 2,
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gate := utilfeature.DefaultFeatureGate.(featuregate.MutableFeatureGate)
			if err := gate.SetFromMap(map[string]bool{string(features.LocalStorageCapacityIsolation): tt.localStorageCapacityIsolation}); err != nil {
				t.Errorf("failed to set featuregate %s", features.LocalStorageCapacityIsolation)
			}
			if utilfeature.DefaultFeatureGate.Enabled(features.LocalStorageCapacityIsolation) != tt.localStorageCapacityIsolation {
				t.Errorf("failed to set featuregate %s", features.LocalStorageCapacityIsolation)
			}
			tt.r1.SubResource(tt.r2)
			if !reflect.DeepEqual(tt.expectedRes, tt.r1) {
				t.Errorf("expected %v, but got %v", tt.expectedRes, tt.r1)
			}
		})
	}
}

func TestSatisfy(t *testing.T) {
	tests := []struct {
		name                          string
		localStorageCapacityIsolation bool
		r1                            *Resource
		r2                            *Resource
		expectedRes                   bool
	}{
		{
			name:                          "cpu not satisfy",
			localStorageCapacityIsolation: true,
			r1: &Resource{
				MilliCPU: 1000,
			},
			r2: &Resource{
				MilliCPU: 2000,
			},
			expectedRes: false,
		},
		{
			name:                          "memory not satisfy",
			localStorageCapacityIsolation: true,
			r1: &Resource{
				Memory: 0,
			},
			r2: &Resource{
				Memory: 1024,
			},
			expectedRes: false,
		},
		{
			name:                          "EphemeralStorage not satisfy",
			localStorageCapacityIsolation: true,
			r1: &Resource{
				EphemeralStorage: 1024,
			},
			r2: &Resource{
				EphemeralStorage: 1024 * 2,
			},
			expectedRes: false,
		},
		{
			name:                          "scalar resource not satisfy",
			localStorageCapacityIsolation: true,
			r1: &Resource{
				ScalarResources: map[v1.ResourceName]int64{
					util.ResourceGPU: 0,
				},
			},
			r2: &Resource{
				ScalarResources: map[v1.ResourceName]int64{
					util.ResourceGPU: 1,
				},
			},
			expectedRes: false,
		},
		{
			name:                          "all satisfy",
			localStorageCapacityIsolation: false,
			r1: &Resource{
				MilliCPU:         1000,
				Memory:           1024,
				EphemeralStorage: 1024 * 2,
				ScalarResources: map[v1.ResourceName]int64{
					util.ResourceGPU: 1,
				},
			},
			r2: &Resource{
				MilliCPU:         1000,
				Memory:           1024,
				EphemeralStorage: 1024,
				ScalarResources: map[v1.ResourceName]int64{
					"test-resource":  1,
					util.ResourceGPU: 1,
				},
			},
			expectedRes: true,
		},
		{
			name:                          "all satisfy, although gpu available is less than or equal to zero",
			localStorageCapacityIsolation: false,
			r1: &Resource{
				ScalarResources: map[v1.ResourceName]int64{
					util.ResourceGPU: -1,
				},
			},
			r2: &Resource{
				ScalarResources: map[v1.ResourceName]int64{
					util.ResourceGPU: 0,
				},
			},
			expectedRes: true,
		},
		{
			name:                          "all satisfy, although cpu available is less than or equal to zero",
			localStorageCapacityIsolation: false,
			r1: &Resource{
				MilliCPU: -1,
			},
			r2: &Resource{
				MilliCPU: 0,
			},
			expectedRes: true,
		},
		{
			name:                          "all satisfy, although memory available is less than or equal to zero",
			localStorageCapacityIsolation: false,
			r1: &Resource{
				Memory: -1,
			},
			r2: &Resource{
				Memory: 0,
			},
			expectedRes: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gate := utilfeature.DefaultFeatureGate.(featuregate.MutableFeatureGate)
			if err := gate.SetFromMap(map[string]bool{string(features.LocalStorageCapacityIsolation): tt.localStorageCapacityIsolation}); err != nil {
				t.Errorf("failed to set featuregate %s", features.LocalStorageCapacityIsolation)
			}
			if utilfeature.DefaultFeatureGate.Enabled(features.LocalStorageCapacityIsolation) != tt.localStorageCapacityIsolation {
				t.Errorf("failed to set featuregate %s", features.LocalStorageCapacityIsolation)
			}
			gotRes := tt.r1.Satisfy(tt.r2)
			if tt.expectedRes != gotRes {
				t.Errorf("expected %v, but got %v", tt.expectedRes, gotRes)
			}
		})
	}
}
