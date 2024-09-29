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

package joblevelaffinity

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/kubewharf/godel-scheduler-api/pkg/apis/scheduling/v1alpha1"
	commoncache "github.com/kubewharf/godel-scheduler/pkg/common/cache"
	framework "github.com/kubewharf/godel-scheduler/pkg/framework/api"
	"github.com/kubewharf/godel-scheduler/pkg/framework/api/fake"
	godelcache "github.com/kubewharf/godel-scheduler/pkg/scheduler/cache"
	"github.com/kubewharf/godel-scheduler/pkg/scheduler/testing/fakehandle"
	"github.com/kubewharf/godel-scheduler/pkg/util"
	podutil "github.com/kubewharf/godel-scheduler/pkg/util/pod"

	"github.com/stretchr/testify/assert"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/sets"
)

func makeUnitWithRequest(resourceName v1.ResourceName, quantity resource.Quantity) framework.ScheduleUnit {
	pg := &v1alpha1.PodGroup{}
	pg.Spec.Affinity = &v1alpha1.Affinity{
		PodGroupAffinity: &v1alpha1.PodGroupAffinity{},
	}
	pg.Spec.MinMember = 1
	unit := framework.NewPodGroupUnit(pg, 0)
	var requests v1.ResourceList
	if resourceName.String() != "" {
		requests = v1.ResourceList{
			resourceName: quantity,
		}
	}
	unit.AddPod(&framework.QueuedPodInfo{
		Pod: &v1.Pod{
			Spec: v1.PodSpec{
				Containers: []v1.Container{
					{
						Resources: v1.ResourceRequirements{
							Requests: requests,
						},
					},
				},
			},
		},
	})

	return unit
}

func TestSortAndMarkTopologyElems(t *testing.T) {
	lister1 := framework.NewClusterNodeInfoLister().(*framework.NodeInfoListerImpl)
	lister2 := framework.NewClusterNodeInfoLister().(*framework.NodeInfoListerImpl)
	lister3 := framework.NewClusterNodeInfoLister().(*framework.NodeInfoListerImpl)

	lister1.InPartitionNodes = append(lister1.InPartitionNodes, &framework.NodeInfoImpl{
		GuaranteedAllocatable: &framework.Resource{
			MilliCPU: 1000,
			Memory:   1024,
		},
		GuaranteedNonZeroRequested: &framework.Resource{
			MilliCPU: 500,
			Memory:   512,
		},
	})
	lister2.InPartitionNodes = append(lister2.InPartitionNodes, &framework.NodeInfoImpl{
		GuaranteedAllocatable: &framework.Resource{
			MilliCPU: 1000,
			Memory:   1024,
			ScalarResources: map[v1.ResourceName]int64{
				util.ResourceGPU: 4,
			},
		},
		GuaranteedNonZeroRequested: &framework.Resource{
			MilliCPU: 500,
			Memory:   512,
			ScalarResources: map[v1.ResourceName]int64{
				util.ResourceGPU: 1,
			},
		},
	}, &framework.NodeInfoImpl{
		GuaranteedAllocatable: &framework.Resource{
			MilliCPU: 1000,
			Memory:   1024,
			ScalarResources: map[v1.ResourceName]int64{
				util.ResourceGPU: 4,
			},
		},
		GuaranteedNonZeroRequested: &framework.Resource{
			MilliCPU: 500,
			Memory:   512,
			ScalarResources: map[v1.ResourceName]int64{
				util.ResourceGPU: 1,
			},
		},
	})
	lister3.InPartitionNodes = append(lister3.InPartitionNodes, &framework.NodeInfoImpl{
		GuaranteedAllocatable: &framework.Resource{
			MilliCPU: 3000,
			Memory:   2048,
			ScalarResources: map[v1.ResourceName]int64{
				util.ResourceGPU: 12,
			},
		},
		GuaranteedNonZeroRequested: &framework.Resource{
			MilliCPU: 1000,
			Memory:   1024,
			ScalarResources: map[v1.ResourceName]int64{
				util.ResourceGPU: 4,
			},
		},
	})

	nc1 := framework.NewNodeCircle("nc1", lister1)
	nc2 := framework.NewNodeCircle("nc2", lister2)
	nc3 := framework.NewNodeCircle("nc3", lister3)

	testCases := []struct {
		name        string
		unit        framework.ScheduleUnit
		nodeCircles []framework.NodeCircle
		sortRules   []framework.SortRule
		expected    []framework.NodeCircle
	}{
		{
			name:        "empty sortRule, sort by cpu capacity asc",
			unit:        makeUnitWithRequest("", resource.Quantity{}),
			nodeCircles: []framework.NodeCircle{nc2, nc1, nc3},
			// The default sortRule (cpu|capacity|ascending) will be added in the real scehduling process,
			// but no sort rule will be added in this unit test. Thus the nodeGroup order here is not changed.
			expected: []framework.NodeCircle{nc2, nc1, nc3},
		},
		{
			name: "sort by gpu capacity desc",
			unit: makeUnitWithRequest("", resource.Quantity{}),
			sortRules: []framework.SortRule{
				{
					Resource:  framework.GPUResource,
					Dimension: framework.Capacity,
					Order:     framework.DescendingOrder,
				},
			},
			nodeCircles: []framework.NodeCircle{nc2, nc1, nc3},
			expected:    []framework.NodeCircle{nc3, nc2, nc1},
		},
		{
			name: "sort by mem capacity asc, gpu capacity asc",
			unit: makeUnitWithRequest("", resource.Quantity{}),
			sortRules: []framework.SortRule{
				{
					Resource:  framework.MemoryResource,
					Dimension: framework.Capacity,
					Order:     framework.AscendingOrder,
				},
				{
					Resource:  framework.GPUResource,
					Dimension: framework.Capacity,
					Order:     framework.AscendingOrder,
				},
			},
			nodeCircles: []framework.NodeCircle{nc2, nc1, nc3},
			expected:    []framework.NodeCircle{nc1, nc2, nc3},
		},
		{
			name: "sort by mem capacity desc, gpu capacity asc",
			unit: makeUnitWithRequest("", resource.Quantity{}),
			sortRules: []framework.SortRule{
				{
					Resource:  framework.MemoryResource,
					Dimension: framework.Capacity,
					Order:     framework.DescendingOrder,
				},
				{
					Resource:  framework.GPUResource,
					Dimension: framework.Capacity,
					Order:     framework.AscendingOrder,
				},
			},
			nodeCircles: []framework.NodeCircle{nc2, nc1, nc3},
			expected:    []framework.NodeCircle{nc2, nc3, nc1},
		},
		{
			name: "sort by available gpu, desc",
			unit: makeUnitWithRequest("", resource.Quantity{}),
			sortRules: []framework.SortRule{
				{
					Resource:  framework.GPUResource,
					Dimension: framework.Available,
					Order:     framework.DescendingOrder,
				},
			},
			nodeCircles: []framework.NodeCircle{nc2, nc1, nc3},
			expected:    []framework.NodeCircle{nc3, nc2, nc1},
		},
		{
			name: "sort by available mem asc, available gpu asc",
			unit: makeUnitWithRequest("", resource.Quantity{}),
			sortRules: []framework.SortRule{
				{
					Resource:  framework.MemoryResource,
					Dimension: framework.Available,
					Order:     framework.AscendingOrder,
				},
				{
					Resource:  framework.GPUResource,
					Dimension: framework.Available,
					Order:     framework.AscendingOrder,
				},
			},
			nodeCircles: []framework.NodeCircle{nc2, nc1, nc3},
			expected:    []framework.NodeCircle{nc1, nc2, nc3},
		},
		{
			name: "sort by available mem desc, available gpu asc",
			unit: makeUnitWithRequest("", resource.Quantity{}),
			sortRules: []framework.SortRule{
				{
					Resource:  framework.MemoryResource,
					Dimension: framework.Available,
					Order:     framework.DescendingOrder,
				},
				{
					Resource:  framework.GPUResource,
					Dimension: framework.Available,
					Order:     framework.AscendingOrder,
				},
			},
			nodeCircles: []framework.NodeCircle{nc2, nc1, nc3},
			expected:    []framework.NodeCircle{nc2, nc3, nc1},
		},
		{
			name: "sort by gpu capacity desc, unit requests cpu resource and no topology domains filtered out",
			unit: makeUnitWithRequest(v1.ResourceCPU, resource.MustParse("1000m")),
			sortRules: []framework.SortRule{
				{
					Resource:  framework.GPUResource,
					Dimension: framework.Capacity,
					Order:     framework.DescendingOrder,
				},
			},
			nodeCircles: []framework.NodeCircle{nc2, nc1, nc3},
			expected:    []framework.NodeCircle{nc3, nc2, nc1},
		},
		{
			name: "sort by gpu capacity desc, unit requests cpu resource and 1 topology domain filtered out",
			unit: makeUnitWithRequest(v1.ResourceCPU, resource.MustParse("1500m")),
			sortRules: []framework.SortRule{
				{
					Resource:  framework.GPUResource,
					Dimension: framework.Capacity,
					Order:     framework.DescendingOrder,
				},
			},
			nodeCircles: []framework.NodeCircle{nc2, nc1, nc3},
			expected:    []framework.NodeCircle{nc3, nc2},
		},
		{
			name: "sort by gpu capacity desc, unit requests cpu resource and 2 topology domains filtered out",
			unit: makeUnitWithRequest(v1.ResourceCPU, resource.MustParse("2001m")),
			sortRules: []framework.SortRule{
				{
					Resource:  framework.GPUResource,
					Dimension: framework.Capacity,
					Order:     framework.DescendingOrder,
				},
			},
			nodeCircles: []framework.NodeCircle{nc2, nc1, nc3},
			expected:    []framework.NodeCircle{nc3},
		},
		{
			name: "sort by gpu capacity desc, unit requests cpu resource and 3 topology domains filtered out",
			unit: makeUnitWithRequest(v1.ResourceCPU, resource.MustParse("5000m")),
			sortRules: []framework.SortRule{
				{
					Resource:  framework.GPUResource,
					Dimension: framework.Capacity,
					Order:     framework.DescendingOrder,
				},
			},
			nodeCircles: []framework.NodeCircle{nc2, nc1, nc3},
			expected:    []framework.NodeCircle{},
		},
		{
			name: "sort by gpu capacity desc, unit requests memory resource and no topology domains filtered out",
			unit: makeUnitWithRequest(v1.ResourceMemory, resource.MustParse("1024")),
			sortRules: []framework.SortRule{
				{
					Resource:  framework.GPUResource,
					Dimension: framework.Capacity,
					Order:     framework.DescendingOrder,
				},
			},
			nodeCircles: []framework.NodeCircle{nc2, nc1, nc3},
			expected:    []framework.NodeCircle{nc3, nc2, nc1},
		},
		{
			name: "sort by gpu capacity desc, unit requests memory resource and 1 topology domain filtered out",
			unit: makeUnitWithRequest(v1.ResourceMemory, resource.MustParse("1025")),
			sortRules: []framework.SortRule{
				{
					Resource:  framework.GPUResource,
					Dimension: framework.Capacity,
					Order:     framework.DescendingOrder,
				},
			},
			nodeCircles: []framework.NodeCircle{nc2, nc1, nc3},
			expected:    []framework.NodeCircle{nc3, nc2},
		},
		{
			name: "sort by gpu capacity desc, unit requests memory resource and 3 topology domains filtered out",
			unit: makeUnitWithRequest(v1.ResourceMemory, resource.MustParse("2049")),
			sortRules: []framework.SortRule{
				{
					Resource:  framework.GPUResource,
					Dimension: framework.Capacity,
					Order:     framework.DescendingOrder,
				},
			},
			nodeCircles: []framework.NodeCircle{nc2, nc1, nc3},
			expected:    []framework.NodeCircle{},
		},
		{
			name: "sort by gpu capacity desc, unit requests gpu resource and no topology domains filtered out",
			unit: makeUnitWithRequest(util.ResourceGPU, resource.MustParse("0")),
			sortRules: []framework.SortRule{
				{
					Resource:  framework.GPUResource,
					Dimension: framework.Capacity,
					Order:     framework.DescendingOrder,
				},
			},
			nodeCircles: []framework.NodeCircle{nc2, nc1, nc3},
			expected:    []framework.NodeCircle{nc3, nc2, nc1},
		},
		{
			name: "sort by gpu capacity desc, unit requests gpu resource and 1 topology domain filtered out",
			unit: makeUnitWithRequest(util.ResourceGPU, resource.MustParse("2")),
			sortRules: []framework.SortRule{
				{
					Resource:  framework.GPUResource,
					Dimension: framework.Capacity,
					Order:     framework.DescendingOrder,
				},
			},
			nodeCircles: []framework.NodeCircle{nc2, nc1, nc3},
			expected:    []framework.NodeCircle{nc3, nc2},
		},
		{
			name: "sort by gpu capacity desc, unit requests gpu resource and 2 topology domains filtered out",
			unit: makeUnitWithRequest(util.ResourceGPU, resource.MustParse("10")),
			sortRules: []framework.SortRule{
				{
					Resource:  framework.GPUResource,
					Dimension: framework.Capacity,
					Order:     framework.DescendingOrder,
				},
			},
			nodeCircles: []framework.NodeCircle{nc2, nc1, nc3},
			expected:    []framework.NodeCircle{nc3},
		},
		{
			name: "sort by gpu capacity desc, unit requests gpu resource and 3 topology domains filtered out",
			unit: makeUnitWithRequest(util.ResourceGPU, resource.MustParse("13")),
			sortRules: []framework.SortRule{
				{
					Resource:  framework.GPUResource,
					Dimension: framework.Capacity,
					Order:     framework.DescendingOrder,
				},
			},
			nodeCircles: []framework.NodeCircle{nc2, nc1, nc3},
			expected:    []framework.NodeCircle{},
		},
	}

	for i := range testCases {
		tt := &testCases[i]
		t.Run(tt.name, func(t *testing.T) {
			minRequest, err := computeUnitMinResourceRequest(tt.unit, false)
			if err != nil {
				t.Errorf("failed to get min request: %v", err)
			}
			topologyElems := getTopologyElems(tt.nodeCircles)
			sortAndMarkTopologyElems(context.TODO(), tt.unit, topologyElems, tt.sortRules, minRequest, false)
			var got []framework.NodeCircle
			for j := range topologyElems {
				if !topologyElems[j].cutOff {
					got = append(got, topologyElems[j].nodeCircle)
				}
			}
			if len(got) != len(tt.expected) {
				t.Errorf("expected length %#v, got %#v", len(tt.expected), len(got))
			}
			for j := range got {
				if got[j].GetKey() != tt.expected[j].GetKey() {
					t.Errorf("expected %#v, got %#v", tt.expected[j].GetKey(), got[j].GetKey())
				}
			}
		})
	}
}

func TestGetRequiredAffinitySpecs(t *testing.T) {
	nodes := make([]*v1.Node, 0)
	nodes = append(nodes, &v1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name:   "node-1",
			Labels: map[string]string{"topologyKey1": "topologyVal1", "otherKey": "otherVal", "topologyKey2": "topologyVal1"},
		},
	}, &v1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name:   "node-2",
			Labels: map[string]string{"topologyKey1": "topologyVal1", "otherKey": "otherVal", "topologyKey2": "topologyVal1"},
		},
	}, &v1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name:   "node-3",
			Labels: map[string]string{"topologyKey1": "topologyVal1", "otherKey": "otherVal", "topologyKey2": "topologyVal2"},
		},
	}, &v1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name:   "node-4",
			Labels: map[string]string{"topologyKey1": "topologyVal2", "otherKey": "otherVal", "topologyKey2": "topologyVal1"},
		},
	}, &v1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name:   "node-5",
			Labels: map[string]string{"topologyKey2": "topologyVal1", "otherKey": "otherVal"},
		},
	},
	)

	nodeLister := fake.NewNodeInfoLister(nodes)
	nodeGroup := framework.NewNodeGroup(framework.DefaultNodeGroupName, nil, []framework.NodeCircle{
		framework.NewNodeCircle(framework.DefaultNodeCircleName, nodeLister),
	})
	for _, test := range []struct {
		name          string
		podLauncher   podutil.PodLauncher
		assignedNodes sets.String
		affinityTerms []framework.UnitAffinityTerm
		expectedSpecs *nodeGroupAffinitySpecs
		expectedError bool
	}{
		{
			name:          "there are running pods, and running pods having the same required topology",
			podLauncher:   podutil.Kubelet,
			assignedNodes: sets.NewString("node-1", "node-2"),
			affinityTerms: []framework.UnitAffinityTerm{
				{
					TopologyKey: "topologyKey1",
				},
			},
			expectedSpecs: &nodeGroupAffinitySpecs{
				specs: []nodeGroupAffinitySpec{
					{
						topologyKey:   "topologyKey1",
						topologyValue: "topologyVal1",
					},
				},
				key: "topologyKey1:topologyVal1;",
			},
			expectedError: false,
		},
		{
			name:          "there are running pods, but running pods having different required topologies",
			podLauncher:   podutil.Kubelet,
			assignedNodes: sets.NewString("node-1", "node-2", "node-4"),
			affinityTerms: []framework.UnitAffinityTerm{
				{
					TopologyKey: "topologyKey1",
				},
			},
			expectedSpecs: nil,
			expectedError: true,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			affinitySpecs, err := getRequiredAffinitySpecs(context.TODO(), test.podLauncher, newNodeGroupAffinityTerms(test.affinityTerms), test.assignedNodes, nodeGroup)
			// compare groupList and expectedGroups
			assert.Equal(t, test.expectedError, err != nil)
			assert.Equal(t, test.expectedSpecs, affinitySpecs)
		})
	}
}

func addNodesToNodeGroup(sameNodeCircle framework.NodeCircle, nodes ...*v1.Node) {
	impl := sameNodeCircle.(*framework.NodeCircleImpl)
	lister := impl.ClusterNodeInfoLister.(*framework.NodeInfoListerImpl)

	for _, node := range nodes {
		nodeInfo := framework.NewNodeInfo()
		_ = nodeInfo.SetNode(node)
		lister.OutOfPartitionNodes = append(lister.OutOfPartitionNodes, nodeInfo)
	}
}

func TestGroupNodesByAffinitySpecs(t *testing.T) {
	nodes := make([]*v1.Node, 0)
	nodes = append(nodes, &v1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name:   "node-1",
			Labels: map[string]string{"topologyKey1": "topologyVal1", "otherKey": "otherVal"},
		},
	}, &v1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name:   "node-2",
			Labels: map[string]string{"topologyKey1": "topologyVal1", "otherKey": "otherVal"},
		},
	}, &v1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name:   "node-3",
			Labels: map[string]string{"topologyKey1": "topologyVal1", "otherKey": "otherVal"},
		},
	}, &v1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name:   "node-4",
			Labels: map[string]string{"topologyKey1": "topologyVal2", "otherKey": "otherVal"},
		},
	}, &v1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name:   "node-5",
			Labels: map[string]string{"topologyKey2": "topologyVal1", "otherKey": "otherVal"},
		},
	},
	)
	nodeLister := fake.NewNodeInfoLister(nodes)
	nodeGroup := framework.NewNodeGroup(framework.DefaultNodeGroupName, nil, []framework.NodeCircle{
		framework.NewNodeCircle(framework.DefaultNodeCircleName, nodeLister),
	})
	nodeCircle := framework.NewNodeCircle("test-nodeCircle", framework.NewClusterNodeInfoLister())
	addNodesToNodeGroup(nodeCircle, nodes...)

	podLauncher := podutil.Kubelet
	assignedNodes := sets.NewString("node-1", "node-2")
	affinityTerms := []framework.UnitAffinityTerm{
		{
			TopologyKey: "topologyKey1",
		},
	}
	ctx := context.TODO()
	specs, err := getRequiredAffinitySpecs(ctx, podLauncher, newNodeGroupAffinityTerms(affinityTerms), assignedNodes, nodeGroup)
	assert.NoError(t, err)
	groupList, err := groupNodesByAffinitySpecs(podLauncher, nodeCircle, assignedNodes, specs)
	assert.NoError(t, err)
	assert.Equal(t, 1, len(groupList))
	actualNodeGroup := groupList[0]
	actualNodes := actualNodeGroup.List()
	assert.Equal(t, 3, len(actualNodes))
	expectedNodeSet := sets.NewString("node-1", "node-2", "node-3")
	actualSet := sets.NewString()
	for _, actualNode := range actualNodes {
		actualSet.Insert(actualNode.GetNodeName())
	}
	assert.Equal(t, expectedNodeSet, actualSet)
}

func TestGroupNodesByAffinityTerms(t *testing.T) {
	nodes := make([]*v1.Node, 0)
	nodes = append(nodes, &v1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name:   "node-1",
			Labels: map[string]string{"topologyKey1": "topologyVal1", "otherKey": "otherVal", "topologyKey2": "topologyVal1"},
		},
	}, &v1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name:   "node-2",
			Labels: map[string]string{"topologyKey1": "topologyVal1", "otherKey": "otherVal", "topologyKey2": "topologyVal1"},
		},
	}, &v1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name:   "node-3",
			Labels: map[string]string{"topologyKey1": "topologyVal1", "otherKey": "otherVal", "topologyKey2": "topologyVal2"},
		},
	}, &v1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name:   "node-4",
			Labels: map[string]string{"topologyKey1": "topologyVal2", "otherKey": "otherVal", "topologyKey2": "topologyVal1"},
		},
	}, &v1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name:   "node-5",
			Labels: map[string]string{"topologyKey2": "topologyVal1", "otherKey": "otherVal"},
		},
	},
	)
	nodeGroup := framework.NewNodeCircle("", framework.NewClusterNodeInfoLister())
	addNodesToNodeGroup(nodeGroup, nodes...)
	// same format with nodegroup key [kv]
	topologyKey1 := "[topologyKey1:topologyVal1;]"
	nodeGroup1 := framework.NewNodeCircle(topologyKey1, framework.NewClusterNodeInfoLister())
	addNodesToNodeGroup(nodeGroup1, nodes[:3]...)
	topologyKey2 := "[topologyKey1:topologyVal2;]"
	nodeGroup2 := framework.NewNodeCircle(topologyKey2, framework.NewClusterNodeInfoLister())
	addNodesToNodeGroup(nodeGroup2, nodes[3])
	topologyKey3 := "[topologyKey1:topologyVal1;topologyKey2:topologyVal1;]"
	nodeGroup3 := framework.NewNodeCircle(topologyKey3, framework.NewClusterNodeInfoLister())
	addNodesToNodeGroup(nodeGroup3, nodes[:2]...)
	topologyKey4 := "[topologyKey1:topologyVal1;topologyKey2:topologyVal2;]"
	nodeGroup4 := framework.NewNodeCircle(topologyKey4, framework.NewClusterNodeInfoLister())
	addNodesToNodeGroup(nodeGroup4, nodes[2])
	topologyKey5 := "[topologyKey1:topologyVal2;topologyKey2:topologyVal1;]"
	nodeGroup5 := framework.NewNodeCircle(topologyKey5, framework.NewClusterNodeInfoLister())
	addNodesToNodeGroup(nodeGroup5, nodes[3])

	for _, test := range []struct {
		name            string
		podLauncher     podutil.PodLauncher
		affinityTerms   []framework.UnitAffinityTerm
		expectedCircles map[string]framework.NodeCircle
	}{
		{
			name:        "group nodes by one matched topology key",
			podLauncher: podutil.Kubelet,
			affinityTerms: []framework.UnitAffinityTerm{
				{
					TopologyKey: "topologyKey1",
				},
			},
			expectedCircles: map[string]framework.NodeCircle{
				topologyKey1: nodeGroup1,
				topologyKey2: nodeGroup2,
			},
		},
		{
			name:        "group nodes by by multiple matched topology keys",
			podLauncher: podutil.Kubelet,
			affinityTerms: []framework.UnitAffinityTerm{
				{
					TopologyKey: "topologyKey1",
				},
				{
					TopologyKey: "topologyKey2",
				},
			},
			expectedCircles: map[string]framework.NodeCircle{
				topologyKey3: nodeGroup3,
				topologyKey4: nodeGroup4,
				topologyKey5: nodeGroup5,
			},
		},
		{
			name:        "group nodes by by partial matched topology keys",
			podLauncher: podutil.Kubelet,
			affinityTerms: []framework.UnitAffinityTerm{
				{
					TopologyKey: "topologyKey1",
				},
				{
					TopologyKey: "topologyKey3",
				},
			},
			expectedCircles: map[string]framework.NodeCircle{},
		},
		{
			name:        "group nodes by by unmatched topology keys",
			podLauncher: podutil.Kubelet,
			affinityTerms: []framework.UnitAffinityTerm{
				{
					TopologyKey: "topologyKey3",
				},
			},
			expectedCircles: map[string]framework.NodeCircle{},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			circleList, err := groupNodesByAffinityTerms(context.TODO(), test.podLauncher, nodeGroup, newNodeGroupAffinityTerms(test.affinityTerms))
			assert.NoError(t, err)
			// compare circleList and expectedCircles
			assert.Equal(t, len(test.expectedCircles), len(circleList))
			for _, nc := range circleList {
				expectedNC, ok := test.expectedCircles[nc.GetKey()]
				assert.True(t, ok)
				expected := expectedNC.List()
				actual := nc.List()
				assert.Equal(t, len(expected), len(actual))
				expectedSet := sets.NewString()
				actualSet := sets.NewString()
				for _, node := range expected {
					expectedSet.Insert(node.GetNodeName())
				}
				for _, node := range actual {
					actualSet.Insert(node.GetNodeName())
				}
				assert.True(t, expectedSet.Equal(actualSet))
			}
		})
	}
}

func TestGetPreferAffinitySpecs(t *testing.T) {
	nodes := make([]*v1.Node, 0)
	nodes = append(nodes, &v1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name:   "node-1",
			Labels: map[string]string{"topologyKey1": "topologyVal1", "otherKey": "otherVal", "topologyKey2": "topologyVal1"},
		},
	}, &v1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name:   "node-2",
			Labels: map[string]string{"topologyKey1": "topologyVal1", "otherKey": "otherVal", "topologyKey2": "topologyVal1"},
		},
	}, &v1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name:   "node-3",
			Labels: map[string]string{"topologyKey1": "topologyVal1", "otherKey": "otherVal", "topologyKey2": "topologyVal2"},
		},
	}, &v1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name:   "node-4",
			Labels: map[string]string{"topologyKey1": "topologyVal2", "otherKey": "otherVal", "topologyKey2": "topologyVal1"},
		},
	}, &v1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name:   "node-5",
			Labels: map[string]string{"topologyKey2": "topologyVal1", "otherKey": "otherVal"},
		},
	},
	)
	nodeLister := fake.NewNodeInfoLister(nodes)
	nodeGroup := framework.NewNodeGroup(framework.DefaultNodeGroupName, nil, []framework.NodeCircle{
		framework.NewNodeCircle(framework.DefaultNodeCircleName, nodeLister),
	})

	for _, test := range []struct {
		name          string
		podLauncher   podutil.PodLauncher
		assignedNodes sets.String
		affinityTerms []framework.UnitAffinityTerm
		expectedSpecs *nodeGroupAffinitySpecs
		expectedError bool
	}{
		{
			name:          "there are running pods, and running pods having the same preferred topology",
			podLauncher:   podutil.Kubelet,
			assignedNodes: sets.NewString("node-1", "node-2"),
			affinityTerms: []framework.UnitAffinityTerm{
				{
					TopologyKey: "topologyKey1",
				},
			},
			expectedSpecs: &nodeGroupAffinitySpecs{
				specs: []nodeGroupAffinitySpec{
					{
						topologyKey:   "topologyKey1",
						topologyValue: "topologyVal1",
					},
				},
				key: "topologyKey1:topologyVal1;",
			},
			expectedError: false,
		},
		{
			name:          "there are running pods, but running pods having different preferred topologies",
			podLauncher:   podutil.Kubelet,
			assignedNodes: sets.NewString("node-1", "node-2", "node-4"),
			affinityTerms: []framework.UnitAffinityTerm{
				{
					TopologyKey: "topologyKey1",
				},
			},
			expectedSpecs: nil,
			expectedError: false,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			affinitySpecs, err := getPreferAffinitySpecs(context.TODO(), test.podLauncher, newNodeGroupAffinityTerms(test.affinityTerms), test.assignedNodes, nodeGroup)
			// compare groupList and expectedGroups
			assert.Equal(t, test.expectedError, err != nil)
			if test.expectedSpecs == nil {
				assert.Nil(t, affinitySpecs)
			} else {
				expectedSet := sets.NewString()
				expectedSet.Insert(test.expectedSpecs.getNodeGroupKey())
				actualSet := sets.NewString()
				actualSet.Insert(affinitySpecs.getNodeGroupKey())
				assert.True(t, expectedSet.Equal(actualSet))
			}
		})
	}
}

func TestLocating(t *testing.T) {
	node1 := &v1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name:   "node-1",
			Labels: map[string]string{"spec": "spec1"},
		},
	}
	node2 := &v1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name:   "node-2",
			Labels: map[string]string{"spec": "spec1"},
		},
	}
	node3 := &v1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name:   "node-3",
			Labels: map[string]string{"spec": "spec2"},
		},
	}
	node4 := &v1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name:   "node-4",
			Labels: map[string]string{"spec": "spec2"},
		},
	}
	node5 := &v1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name:   "node-5",
			Labels: map[string]string{"spec": "spec2"},
		},
	}

	totalNodes := make([]*v1.Node, 0)
	totalNodes = append(totalNodes, node1, node2, node3, node4, node5)
	totalNodesLister := fake.NewNodeInfoLister(totalNodes)

	spec1Nodes := make([]*v1.Node, 0)
	spec1Nodes = append(spec1Nodes, node1, node2)
	spec1NodesLister := fake.NewNodeInfoLister(spec1Nodes)

	spec2Nodes := make([]*v1.Node, 0)
	spec2Nodes = append(spec2Nodes, node3, node4, node5)
	spec2NodesLister := fake.NewNodeInfoLister(spec2Nodes)

	createPodGroupUnit := func(matchExpressions []v1.NodeSelectorRequirement) *framework.PodGroupUnit {
		pg := &v1alpha1.PodGroup{}
		if len(matchExpressions) != 0 {
			pg.Spec.Affinity = &v1alpha1.Affinity{
				PodGroupAffinity: &v1alpha1.PodGroupAffinity{
					NodeSelector: &v1.NodeSelector{
						NodeSelectorTerms: []v1.NodeSelectorTerm{
							{
								MatchExpressions: matchExpressions,
							},
						},
					},
				},
			}
		}

		unit := framework.NewPodGroupUnit(pg, 0)
		unit.AddPod(
			&framework.QueuedPodInfo{
				Pod: &v1.Pod{
					ObjectMeta: metav1.ObjectMeta{
						Name: "testpod",
						Annotations: map[string]string{
							podutil.PodLauncherAnnotationKey:     string(podutil.Kubelet),
							podutil.PodResourceTypeAnnotationKey: string(podutil.GuaranteedPod),
						},
					},
				},
			},
		)
		return unit
	}

	tests := []struct {
		name       string
		unit       *framework.PodGroupUnit
		nodeLister framework.NodeInfoLister
		expected   framework.NodeCircle
	}{
		{
			name:       "empty nodeSelector",
			unit:       createPodGroupUnit([]v1.NodeSelectorRequirement{}),
			nodeLister: totalNodesLister,
			expected:   framework.NewNodeCircle("", totalNodesLister),
		},
		{
			name: "nodeSelector requires spec1 nodes",
			unit: createPodGroupUnit([]v1.NodeSelectorRequirement{
				{
					Key:      "spec",
					Operator: "In",
					Values:   []string{"spec1"},
				},
			}),
			nodeLister: totalNodesLister,
			expected:   framework.NewNodeCircle("", spec1NodesLister),
		},
		{
			name: "nodeSelector requires spec2 nodes",
			unit: createPodGroupUnit([]v1.NodeSelectorRequirement{
				{
					Key:      "spec",
					Operator: "In",
					Values:   []string{"spec2"},
				},
			}),
			nodeLister: totalNodesLister,
			expected:   framework.NewNodeCircle("", spec2NodesLister),
		},
	}

	for _, tt := range tests {
		cache := godelcache.New(commoncache.MakeCacheHandlerWrapper().
			ComponentName("").SchedulerType("").SubCluster(framework.DefaultSubCluster).
			PodAssumedTTL(time.Second).Period(10*time.Second).StopCh(make(<-chan struct{})).
			EnableStore("PreemptionStore", "QueueStore", "HostUniqueStore").
			Obj())
		nodeGroup := framework.NewNodeGroup(framework.DefaultNodeGroupName, nil, []framework.NodeCircle{
			framework.NewNodeCircle(framework.DefaultNodeCircleName, tt.nodeLister),
		})

		pl, err := New(nil, &fakehandle.MockUnitFrameworkHandle{Cache: cache})
		if err != nil {
			t.Fatalf("err: %v", err)
		}

		originalNodeCircle, status := pl.(framework.LocatingPlugin).Locating(context.Background(), tt.unit, framework.NewCycleState(), nodeGroup)
		if status != nil {
			t.Fatalf("failed to locating node group: %v", status)
		}

		expectedNodes := tt.expected.InPartitionList()
		nodesInOriginalNodeCircle := originalNodeCircle.GetNodeCircles()[0].OutOfPartitionList()
		if len(expectedNodes) != len(nodesInOriginalNodeCircle) {
			t.Fatalf("expect length of the original node circle: %v, got: %v", len(expectedNodes), len(nodesInOriginalNodeCircle))
		}
		for i, node := range expectedNodes {
			if node.GetNodeName() != nodesInOriginalNodeCircle[i].GetNodeName() {
				t.Fatalf("expected node %v, got %v", node.GetNodeName(), nodesInOriginalNodeCircle[i].GetNodeName())
			}
		}

	}
}

func makeNodeInfo(name string) framework.NodeInfo {
	nodeinfo := framework.NewNodeInfo()
	nodeinfo.SetNode(&v1.Node{ObjectMeta: metav1.ObjectMeta{Name: name}})
	return nodeinfo
}

func TestGetNodeGroupsFromTree(t *testing.T) {
	wrongTree := []*topologyElem{
		{
			nodeCircle: framework.NewNodeCircle("nc0", nil),
			children:   []int{1, 2},
		},
		{
			nodeCircle: framework.NewNodeCircle("nc1", nil),
			children:   []int{3, 4},
		},
		{
			nodeCircle: framework.NewNodeCircle("nc2", nil),
			children:   []int{5, 7},
		},
		{
			nodeCircle: framework.NewNodeCircle("nc3", nil),
			children:   []int{},
		},
		{
			nodeCircle: framework.NewNodeCircle("nc4", nil),
			children:   []int{},
		},
		{
			nodeCircle: framework.NewNodeCircle("nc5", nil),
			children:   []int{},
		},
		{
			nodeCircle: framework.NewNodeCircle("nc6", nil),
			children:   []int{},
		},
	}
	wrongTree2 := []*topologyElem{
		{
			nodeCircle: framework.NewNodeCircle("nc0", nil),
			children:   []int{1, 2},
		},
		{
			nodeCircle: framework.NewNodeCircle("nc1", nil),
			children:   []int{3, 4},
		},
		{
			nodeCircle: framework.NewNodeCircle("nc2", nil),
			children:   []int{5, 7},
		},
		{
			nodeCircle: nil,
			children:   []int{},
		},
		{
			nodeCircle: framework.NewNodeCircle("nc4", nil),
			children:   []int{},
		},
		{
			nodeCircle: framework.NewNodeCircle("nc5", nil),
			children:   []int{},
		},
		{
			nodeCircle: framework.NewNodeCircle("nc6", nil),
			children:   []int{},
		},
	}
	oneRequiredAndTwoPreferredTopologyTree := []*topologyElem{
		{
			nodeCircle: framework.NewNodeCircle("BigPodA", nil), // index 0
			children:   []int{2, 3},
		},
		{
			nodeCircle: framework.NewNodeCircle("BigPodB", nil), // index 1
			children:   []int{4, 5},
		},
		{
			nodeCircle: framework.NewNodeCircle("MiniPodA1", nil), // index 2
			children:   []int{6, 7, 8},
		},
		{
			nodeCircle: framework.NewNodeCircle("MiniPodA2", nil), // index 3
			children:   []int{9, 10},
		},
		{
			nodeCircle: framework.NewNodeCircle("MiniPodB1", nil), // index 4
			children:   []int{11, 12},
		},
		{
			nodeCircle: framework.NewNodeCircle("MiniPodB2", nil), // index 5
			children:   []int{13, 14},
		},
		{
			nodeCircle: framework.NewNodeCircle("Tor1", nil), // index 6
			children:   []int{},
		},
		{
			nodeCircle: framework.NewNodeCircle("Tor2", nil), // index 7
			children:   []int{},
		},
		{
			nodeCircle: framework.NewNodeCircle("Tor3", nil), // index 8
			children:   []int{},
		},
		{
			nodeCircle: framework.NewNodeCircle("Tor4", nil), // index 9
			children:   []int{},
		},
		{
			nodeCircle: framework.NewNodeCircle("Tor5", nil), // index 10
			children:   []int{},
		},
		{
			nodeCircle: framework.NewNodeCircle("Tor6", nil), // index 11
			children:   []int{},
		},
		{
			nodeCircle: framework.NewNodeCircle("Tor7", nil), // index 12
			children:   []int{},
		},
		{
			nodeCircle: framework.NewNodeCircle("Tor8", nil), // index 13
			children:   []int{},
		},
		{
			nodeCircle: framework.NewNodeCircle("Tor9", nil), // index 14
			children:   []int{},
		},
	}
	oneRequiredAndTwoPreferredNodeGroups := []framework.NodeGroup{
		&framework.NodeGroupImpl{
			Key:         "Tor9",
			NodeCircles: []framework.NodeCircle{framework.NewNodeCircle("Tor9", nil)},
		},
		&framework.NodeGroupImpl{
			Key:         "Tor8",
			NodeCircles: []framework.NodeCircle{framework.NewNodeCircle("Tor8", nil)},
		},
		&framework.NodeGroupImpl{
			Key:         "Tor7",
			NodeCircles: []framework.NodeCircle{framework.NewNodeCircle("Tor7", nil)},
		},
		&framework.NodeGroupImpl{
			Key:         "Tor6",
			NodeCircles: []framework.NodeCircle{framework.NewNodeCircle("Tor6", nil)},
		},
		&framework.NodeGroupImpl{
			Key:         "Tor5",
			NodeCircles: []framework.NodeCircle{framework.NewNodeCircle("Tor5", nil)},
		},
		&framework.NodeGroupImpl{
			Key:         "Tor4",
			NodeCircles: []framework.NodeCircle{framework.NewNodeCircle("Tor4", nil)},
		},
		&framework.NodeGroupImpl{
			Key:         "Tor3",
			NodeCircles: []framework.NodeCircle{framework.NewNodeCircle("Tor3", nil)},
		},
		&framework.NodeGroupImpl{
			Key:         "Tor2",
			NodeCircles: []framework.NodeCircle{framework.NewNodeCircle("Tor2", nil)},
		},
		&framework.NodeGroupImpl{
			Key:         "Tor1",
			NodeCircles: []framework.NodeCircle{framework.NewNodeCircle("Tor1", nil)},
		},
		&framework.NodeGroupImpl{
			Key:         "MiniPodB2",
			NodeCircles: []framework.NodeCircle{framework.NewNodeCircle("Tor9", nil), framework.NewNodeCircle("Tor8", nil)},
		},
		&framework.NodeGroupImpl{
			Key:         "MiniPodB1",
			NodeCircles: []framework.NodeCircle{framework.NewNodeCircle("Tor7", nil), framework.NewNodeCircle("Tor6", nil)},
		},
		&framework.NodeGroupImpl{
			Key:         "MiniPodA2",
			NodeCircles: []framework.NodeCircle{framework.NewNodeCircle("Tor5", nil), framework.NewNodeCircle("Tor4", nil)},
		},
		&framework.NodeGroupImpl{
			Key:         "MiniPodA1",
			NodeCircles: []framework.NodeCircle{framework.NewNodeCircle("Tor3", nil), framework.NewNodeCircle("Tor2", nil), framework.NewNodeCircle("Tor1", nil)},
		},
		&framework.NodeGroupImpl{
			Key:         "BigPodB",
			NodeCircles: []framework.NodeCircle{framework.NewNodeCircle("Tor9", nil), framework.NewNodeCircle("Tor8", nil), framework.NewNodeCircle("Tor7", nil), framework.NewNodeCircle("Tor6", nil)},
		},
		&framework.NodeGroupImpl{
			Key:         "BigPodA",
			NodeCircles: []framework.NodeCircle{framework.NewNodeCircle("Tor5", nil), framework.NewNodeCircle("Tor4", nil), framework.NewNodeCircle("Tor3", nil), framework.NewNodeCircle("Tor2", nil), framework.NewNodeCircle("Tor1", nil)},
		},
	}

	nodeInfo1 := makeNodeInfo("node1")
	nodeInfo2 := makeNodeInfo("node2")
	nodeInfo3 := makeNodeInfo("node3")
	nodeInfoLister1 := framework.NodeInfoListerImpl{
		InPartitionNodes: []framework.NodeInfo{nodeInfo1, nodeInfo2, nodeInfo3},
	}
	nodeInfoLister2 := framework.NodeInfoListerImpl{
		InPartitionNodes: []framework.NodeInfo{nodeInfo2, nodeInfo3},
	}
	nodeInfoLister3 := framework.NodeInfoListerImpl{
		InPartitionNodes: []framework.NodeInfo{nodeInfo3},
	}
	treeWithRunningPodsInSamePreferredDomain := []*topologyElem{
		{
			nodeCircle: framework.NewNodeCircle("BigPodA", &nodeInfoLister1), // index 0
			children:   []int{1},
		},
		{
			nodeCircle: framework.NewNodeCircle("MiniPodA1", &nodeInfoLister2), // index 1
			children:   []int{2},
		},
		{
			nodeCircle: framework.NewNodeCircle("Tor1", &nodeInfoLister3), // index 2
			children:   []int{},
		},
	}
	nodeGroupsWithRunningPodsInSamePreferredDomain := []framework.NodeGroup{
		&framework.NodeGroupImpl{
			Key:         "Tor1",
			NodeCircles: []framework.NodeCircle{framework.NewNodeCircle("Tor1", &nodeInfoLister3)},
		},
		&framework.NodeGroupImpl{
			Key:         "MiniPodA1",
			NodeCircles: []framework.NodeCircle{framework.NewNodeCircle("MiniPodA1", &nodeInfoLister2)},
		},
		&framework.NodeGroupImpl{
			Key:         "BigPodA",
			NodeCircles: []framework.NodeCircle{framework.NewNodeCircle("BigPodA", &nodeInfoLister1)},
		},
	}
	oneRequiredAndTwoPreferredTopologyTreeWithSomeTermsCutOff := []*topologyElem{
		{
			nodeCircle: framework.NewNodeCircle("BigPodA", nil), // index 0
			children:   []int{2, 3},
		},
		{
			nodeCircle: framework.NewNodeCircle("BigPodB", nil), // index 1
			children:   []int{4, 5},
		},
		{
			nodeCircle: framework.NewNodeCircle("MiniPodA1", nil), // index 2
			children:   []int{6, 7, 8},
		},
		{
			nodeCircle: framework.NewNodeCircle("MiniPodA2", nil), // index 3
			children:   []int{9, 10},
			cutOff:     true,
		},
		{
			nodeCircle: framework.NewNodeCircle("MiniPodB1", nil), // index 4
			children:   []int{11, 12},
		},
		{
			nodeCircle: framework.NewNodeCircle("MiniPodB2", nil), // index 5
			children:   []int{13, 14},
			cutOff:     true,
		},
		{
			nodeCircle: framework.NewNodeCircle("Tor1", nil), // index 6
			children:   []int{},
		},
		{
			nodeCircle: framework.NewNodeCircle("Tor2", nil), // index 7
			children:   []int{},
		},
		{
			nodeCircle: framework.NewNodeCircle("Tor3", nil), // index 8
			children:   []int{},
		},
		{
			nodeCircle: framework.NewNodeCircle("Tor4", nil), // index 9
			children:   []int{},
			cutOff:     true,
		},
		{
			nodeCircle: framework.NewNodeCircle("Tor5", nil), // index 10
			children:   []int{},
			cutOff:     true,
		},
		{
			nodeCircle: framework.NewNodeCircle("Tor6", nil), // index 11
			children:   []int{},
		},
		{
			nodeCircle: framework.NewNodeCircle("Tor7", nil), // index 12
			children:   []int{},
		},
		{
			nodeCircle: framework.NewNodeCircle("Tor8", nil), // index 13
			children:   []int{},
			cutOff:     true,
		},
		{
			nodeCircle: framework.NewNodeCircle("Tor9", nil), // index 14
			children:   []int{},
			cutOff:     true,
		},
	}
	oneRequiredAndTwoPreferredNodeGroupsWithSomeGroupsCutOff := []framework.NodeGroup{
		&framework.NodeGroupImpl{
			Key:         "Tor7",
			NodeCircles: []framework.NodeCircle{framework.NewNodeCircle("Tor7", nil)},
		},
		&framework.NodeGroupImpl{
			Key:         "Tor6",
			NodeCircles: []framework.NodeCircle{framework.NewNodeCircle("Tor6", nil)},
		},
		&framework.NodeGroupImpl{
			Key:         "Tor3",
			NodeCircles: []framework.NodeCircle{framework.NewNodeCircle("Tor3", nil)},
		},
		&framework.NodeGroupImpl{
			Key:         "Tor2",
			NodeCircles: []framework.NodeCircle{framework.NewNodeCircle("Tor2", nil)},
		},
		&framework.NodeGroupImpl{
			Key:         "Tor1",
			NodeCircles: []framework.NodeCircle{framework.NewNodeCircle("Tor1", nil)},
		},
		&framework.NodeGroupImpl{
			Key:         "MiniPodB1",
			NodeCircles: []framework.NodeCircle{framework.NewNodeCircle("Tor7", nil), framework.NewNodeCircle("Tor6", nil)},
		},
		&framework.NodeGroupImpl{
			Key:         "MiniPodA1",
			NodeCircles: []framework.NodeCircle{framework.NewNodeCircle("Tor3", nil), framework.NewNodeCircle("Tor2", nil), framework.NewNodeCircle("Tor1", nil)},
		},
		&framework.NodeGroupImpl{
			Key:         "BigPodB",
			NodeCircles: []framework.NodeCircle{framework.NewNodeCircle("Tor9", nil), framework.NewNodeCircle("Tor8", nil), framework.NewNodeCircle("Tor7", nil), framework.NewNodeCircle("Tor6", nil)},
		},
		&framework.NodeGroupImpl{
			Key:         "BigPodA",
			NodeCircles: []framework.NodeCircle{framework.NewNodeCircle("Tor5", nil), framework.NewNodeCircle("Tor4", nil), framework.NewNodeCircle("Tor3", nil), framework.NewNodeCircle("Tor2", nil), framework.NewNodeCircle("Tor1", nil)},
		},
	}

	tests := []struct {
		name               string
		topologyElem       []*topologyElem
		expectedNodeGroups []framework.NodeGroup
		expectedError      error
	}{
		{
			name:               "nil tree",
			topologyElem:       nil,
			expectedNodeGroups: nil,
			expectedError:      fmt.Errorf("empty topology tree"),
		},
		{
			name:               "empty tree",
			topologyElem:       []*topologyElem{},
			expectedNodeGroups: nil,
			expectedError:      fmt.Errorf("empty topology tree"),
		},
		{
			name:               "wrong child index in some element",
			topologyElem:       wrongTree,
			expectedNodeGroups: nil,
			expectedError:      fmt.Errorf("unexpected child index 7 in elem 2 while length of tree is 7"),
		},
		{
			name:               "some elem has nil node group",
			topologyElem:       wrongTree2,
			expectedNodeGroups: nil,
			expectedError:      fmt.Errorf("nil node circle in elem 3"),
		},
		{
			name:               "1 required and 2 preferred affinity terms",
			topologyElem:       oneRequiredAndTwoPreferredTopologyTree,
			expectedNodeGroups: oneRequiredAndTwoPreferredNodeGroups,
			expectedError:      nil,
		},
		{
			name:               "running pods in same preferred domain",
			topologyElem:       treeWithRunningPodsInSamePreferredDomain,
			expectedNodeGroups: nodeGroupsWithRunningPodsInSamePreferredDomain,
			expectedError:      nil,
		},
		{
			name:               "1 required and 2 preferred affinity term, and some node groups cut off",
			topologyElem:       oneRequiredAndTwoPreferredTopologyTreeWithSomeTermsCutOff,
			expectedNodeGroups: oneRequiredAndTwoPreferredNodeGroupsWithSomeGroupsCutOff,
			expectedError:      nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			nodeGroups, err := getNodeGroupsFromTree(tt.topologyElem)
			if err != nil || tt.expectedError != nil {
				if err == nil || tt.expectedError == nil || err.Error() != tt.expectedError.Error() {
					t.Fatalf("expected err: %v, got: %v", tt.expectedError, err)
				}
			}

			if len(nodeGroups) != len(tt.expectedNodeGroups) {
				t.Fatalf("expected length of node groups: %v, got %v", len(tt.expectedNodeGroups), len(nodeGroups))
			}
			for i, ng := range tt.expectedNodeGroups {
				if ng.GetKey() != nodeGroups[i].GetKey() {
					t.Fatalf("expected node group name: %v, got: %v", ng.GetKey(), nodeGroups[i].GetKey())
				}
				expectedNodeCircles, nodeCircles := ng.GetNodeCircles(), nodeGroups[i].GetNodeCircles()
				if len(expectedNodeCircles) != len(nodeCircles) {
					t.Fatalf("expected node circle length: %v, got: %v, node group: %v", len(expectedNodeCircles), len(nodeCircles), ng.GetKey())
				}
				for j, nc := range expectedNodeCircles {
					if nc.GetKey() != nodeCircles[j].GetKey() {
						t.Fatalf("expected node circle name: %v, got: %v, node group: %v", nc.GetKey(), nodeCircles[j].GetKey(), ng.GetKey())
					}
					if nc.Len() != nodeCircles[j].Len() {
						t.Fatalf("expected length of nodes : %v, got : %v, node circle: %v, node group: %v", nc.Len(), nodeCircles[j].Len(), nc.GetKey(), ng.GetKey())
					}
				}
			}
		})
	}
}

func TestGrouping(t *testing.T) {
	stop := make(chan struct{})
	defer close(stop)
	cache := godelcache.New(commoncache.MakeCacheHandlerWrapper().
		ComponentName("").SchedulerType("").SubCluster(framework.DefaultSubCluster).
		PodAssumedTTL(time.Second).Period(10*time.Second).StopCh(make(<-chan struct{})).
		EnableStore("PreemptionStore", "QueueStore", "HostUniqueStore").
		Obj())

	nodes := []*v1.Node{
		{
			ObjectMeta: metav1.ObjectMeta{
				Name:   "node-1",
				Labels: map[string]string{"bigPod": "bigPodA", "miniPod": "miniPodA1", "tor": "tor1"},
			},
			Status: v1.NodeStatus{
				Allocatable: v1.ResourceList{
					v1.ResourceCPU: resource.MustParse("4"),
				},
			},
		},
		{
			ObjectMeta: metav1.ObjectMeta{
				Name:   "node-2",
				Labels: map[string]string{"bigPod": "bigPodA", "miniPod": "miniPodA1", "tor": "tor1"},
			},
			Status: v1.NodeStatus{
				Allocatable: v1.ResourceList{
					v1.ResourceCPU: resource.MustParse("4"),
				},
			},
		},
		{
			ObjectMeta: metav1.ObjectMeta{
				Name:   "node-3",
				Labels: map[string]string{"bigPod": "bigPodA", "miniPod": "miniPodA1", "tor": "tor2"},
			},
			Status: v1.NodeStatus{
				Allocatable: v1.ResourceList{
					v1.ResourceCPU: resource.MustParse("3"),
				},
			},
		},
		{
			ObjectMeta: metav1.ObjectMeta{
				Name:   "node-4",
				Labels: map[string]string{"bigPod": "bigPodA", "miniPod": "miniPodA1", "tor": "tor2"},
			},
			Status: v1.NodeStatus{
				Allocatable: v1.ResourceList{
					v1.ResourceCPU: resource.MustParse("3"),
				},
			},
		},
		{
			ObjectMeta: metav1.ObjectMeta{
				Name:   "node-5",
				Labels: map[string]string{"bigPod": "bigPodA", "miniPod": "miniPodA1", "tor": "tor3"},
			},
			Status: v1.NodeStatus{
				Allocatable: v1.ResourceList{
					v1.ResourceCPU: resource.MustParse("2"),
				},
			},
		},
		{
			ObjectMeta: metav1.ObjectMeta{
				Name:   "node-6",
				Labels: map[string]string{"bigPod": "bigPodA", "miniPod": "miniPodA1", "tor": "tor3"},
			},
			Status: v1.NodeStatus{
				Allocatable: v1.ResourceList{
					v1.ResourceCPU: resource.MustParse("2"),
				},
			},
		},
		{
			ObjectMeta: metav1.ObjectMeta{
				Name:   "node-7",
				Labels: map[string]string{"bigPod": "bigPodA", "miniPod": "miniPodA2", "tor": "tor4"},
			},
			Status: v1.NodeStatus{
				Allocatable: v1.ResourceList{
					v1.ResourceCPU: resource.MustParse("4"),
				},
			},
		},
		{
			ObjectMeta: metav1.ObjectMeta{
				Name:   "node-8",
				Labels: map[string]string{"bigPod": "bigPodA", "miniPod": "miniPodA2", "tor": "tor4"},
			},
			Status: v1.NodeStatus{
				Allocatable: v1.ResourceList{
					v1.ResourceCPU: resource.MustParse("4"),
				},
			},
		},
		{
			ObjectMeta: metav1.ObjectMeta{
				Name:   "node-9",
				Labels: map[string]string{"bigPod": "bigPodA", "miniPod": "miniPodA2", "tor": "tor5"},
			},
			Status: v1.NodeStatus{
				Allocatable: v1.ResourceList{
					v1.ResourceCPU: resource.MustParse("3"),
				},
			},
		},
		{
			ObjectMeta: metav1.ObjectMeta{
				Name:   "node-10",
				Labels: map[string]string{"bigPod": "bigPodA", "miniPod": "miniPodA2", "tor": "tor5"},
			},
			Status: v1.NodeStatus{
				Allocatable: v1.ResourceList{
					v1.ResourceCPU: resource.MustParse("3"),
				},
			},
		},
		{
			ObjectMeta: metav1.ObjectMeta{
				Name:   "node-11",
				Labels: map[string]string{"bigPod": "bigPodB", "miniPod": "miniPodB1", "tor": "tor6"},
			},
			Status: v1.NodeStatus{
				Allocatable: v1.ResourceList{
					v1.ResourceCPU: resource.MustParse("4"),
				},
			},
		},
		{
			ObjectMeta: metav1.ObjectMeta{
				Name:   "node-12",
				Labels: map[string]string{"bigPod": "bigPodB", "miniPod": "miniPodB1", "tor": "tor6"},
			},
			Status: v1.NodeStatus{
				Allocatable: v1.ResourceList{
					v1.ResourceCPU: resource.MustParse("4"),
				},
			},
		},
		{
			ObjectMeta: metav1.ObjectMeta{
				Name:   "node-13",
				Labels: map[string]string{"bigPod": "bigPodB", "miniPod": "miniPodB1", "tor": "tor7"},
			},
			Status: v1.NodeStatus{
				Allocatable: v1.ResourceList{
					v1.ResourceCPU: resource.MustParse("3"),
				},
			},
		},
		{
			ObjectMeta: metav1.ObjectMeta{
				Name:   "node-14",
				Labels: map[string]string{"bigPod": "bigPodB", "miniPod": "miniPodB1", "tor": "tor7"},
			},
			Status: v1.NodeStatus{
				Allocatable: v1.ResourceList{
					v1.ResourceCPU: resource.MustParse("3"),
				},
			},
		},
		{
			ObjectMeta: metav1.ObjectMeta{
				Name:   "node-15",
				Labels: map[string]string{"bigPod": "bigPodB", "miniPod": "miniPodB2", "tor": "tor8"},
			},
			Status: v1.NodeStatus{
				Allocatable: v1.ResourceList{
					v1.ResourceCPU: resource.MustParse("3"),
				},
			},
		},
		{
			ObjectMeta: metav1.ObjectMeta{
				Name:   "node-16",
				Labels: map[string]string{"bigPod": "bigPodB", "miniPod": "miniPodB2", "tor": "tor8"},
			},
			Status: v1.NodeStatus{
				Allocatable: v1.ResourceList{
					v1.ResourceCPU: resource.MustParse("3"),
				},
			},
		},
		{
			ObjectMeta: metav1.ObjectMeta{
				Name:   "node-17",
				Labels: map[string]string{"bigPod": "bigPodB", "miniPod": "miniPodB2", "tor": "tor9"},
			},
			Status: v1.NodeStatus{
				Allocatable: v1.ResourceList{
					v1.ResourceCPU: resource.MustParse("2"),
				},
			},
		},
		{
			ObjectMeta: metav1.ObjectMeta{
				Name:   "node-18",
				Labels: map[string]string{"bigPod": "bigPodB", "miniPod": "miniPodB2", "tor": "tor9"},
			},
			Status: v1.NodeStatus{
				Allocatable: v1.ResourceList{
					v1.ResourceCPU: resource.MustParse("2"),
				},
			},
		},
	}
	nodeLister := fake.NewNodeInfoLister(nodes)

	pg := &v1alpha1.PodGroup{
		Spec: v1alpha1.PodGroupSpec{
			MinMember: 1,
			Affinity: &v1alpha1.Affinity{
				PodGroupAffinity: &v1alpha1.PodGroupAffinity{
					Required: []v1alpha1.PodGroupAffinityTerm{
						{
							TopologyKey: "bigPod",
						},
					},
					Preferred: []v1alpha1.PodGroupAffinityTerm{
						{
							TopologyKey: "miniPod",
						},
						{
							TopologyKey: "tor",
						},
					},
					SortRules: []v1alpha1.SortRule{
						{
							Resource:  v1alpha1.CPUResource,
							Dimension: v1alpha1.Capacity,
							Order:     v1alpha1.DescendingOrder,
						},
					},
				},
			},
		},
	}
	queuedUnitInfo := &framework.QueuedUnitInfo{
		ScheduleUnit: framework.NewPodGroupUnit(pg, 100),
	}

	queuedPodInfo := &framework.QueuedPodInfo{
		Pod: &v1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name: "testpod",
				Annotations: map[string]string{
					podutil.PodLauncherAnnotationKey:     string(podutil.Kubelet),
					podutil.PodResourceTypeAnnotationKey: string(podutil.GuaranteedPod),
				},
			},
			Spec: v1.PodSpec{
				Containers: []v1.Container{
					{
						Resources: v1.ResourceRequirements{
							Requests: v1.ResourceList{
								v1.ResourceCPU: resource.MustParse("7"),
							},
						},
					},
				},
			},
		},
	}
	queuedUnitInfo.AddPod(queuedPodInfo)

	expectedNodeGroupList := []framework.NodeGroup{
		&framework.NodeGroupImpl{
			Key:         "tor:tor1;",
			NodeCircles: []framework.NodeCircle{framework.NewNodeCircle("tor:tor1;", nil)},
		},
		// &framework.NodeGroupImpl{
		// 	Key:         "tor:tor2;",
		// 	NodeCircles: []framework.NodeCircle{framework.NewNodeCircle("tor:tor2;", nil)},
		// },
		// &framework.NodeGroupImpl{
		// 	Key:         "tor:tor3;",
		// 	NodeCircles: []framework.NodeCircle{framework.NewNodeCircle("tor:tor3;", nil)},
		// },
		&framework.NodeGroupImpl{
			Key:         "tor:tor4;",
			NodeCircles: []framework.NodeCircle{framework.NewNodeCircle("tor:tor4;", nil)},
		},
		// &framework.NodeGroupImpl{
		// 	Key:         "tor:tor5;",
		// 	NodeCircles: []framework.NodeCircle{framework.NewNodeCircle("tor:tor5;", nil)},
		// },
		&framework.NodeGroupImpl{
			Key:         "tor:tor6;",
			NodeCircles: []framework.NodeCircle{framework.NewNodeCircle("tor:tor6;", nil)},
		},
		// &framework.NodeGroupImpl{
		// 	Key:         "tor:tor7;",
		// 	NodeCircles: []framework.NodeCircle{framework.NewNodeCircle("tor:tor7;", nil)},
		// },
		// &framework.NodeGroupImpl{
		// 	Key:         "tor:tor8;",
		// 	NodeCircles: []framework.NodeCircle{framework.NewNodeCircle("tor:tor8;", nil)},
		// },
		// &framework.NodeGroupImpl{
		// 	Key:         "tor:tor9;",
		// 	NodeCircles: []framework.NodeCircle{framework.NewNodeCircle("tor:tor9;", nil)},
		// },
		&framework.NodeGroupImpl{
			Key:         "miniPod:miniPodA1;",
			NodeCircles: []framework.NodeCircle{framework.NewNodeCircle("tor:tor1;", nil), framework.NewNodeCircle("tor:tor2;", nil), framework.NewNodeCircle("tor:tor3;", nil)},
		},
		&framework.NodeGroupImpl{
			Key:         "miniPod:miniPodA2;",
			NodeCircles: []framework.NodeCircle{framework.NewNodeCircle("tor:tor4;", nil), framework.NewNodeCircle("tor:tor5;", nil)},
		},
		&framework.NodeGroupImpl{
			Key:         "miniPod:miniPodB1;",
			NodeCircles: []framework.NodeCircle{framework.NewNodeCircle("tor:tor6;", nil), framework.NewNodeCircle("tor:tor7;", nil)},
		},
		&framework.NodeGroupImpl{
			Key:         "miniPod:miniPodB2;",
			NodeCircles: []framework.NodeCircle{framework.NewNodeCircle("tor:tor8;", nil), framework.NewNodeCircle("tor:tor9;", nil)},
		},
		&framework.NodeGroupImpl{
			Key:         "bigPod:bigPodA;",
			NodeCircles: []framework.NodeCircle{framework.NewNodeCircle("tor:tor1;", nil), framework.NewNodeCircle("tor:tor2;", nil), framework.NewNodeCircle("tor:tor3;", nil), framework.NewNodeCircle("tor:tor4;", nil), framework.NewNodeCircle("tor:tor5;", nil)},
		},
		&framework.NodeGroupImpl{
			Key:         "bigPod:bigPodB;",
			NodeCircles: []framework.NodeCircle{framework.NewNodeCircle("tor:tor6;", nil), framework.NewNodeCircle("tor:tor7;", nil), framework.NewNodeCircle("tor:tor8;", nil), framework.NewNodeCircle("tor:tor9;", nil)},
		},
	}

	pl, err := New(nil, &fakehandle.MockUnitFrameworkHandle{Cache: cache})
	if err != nil {
		t.Fatalf("err: %v", err)
	}

	nodeGroup := framework.NewNodeGroup(framework.DefaultNodeGroupName, nil, []framework.NodeCircle{
		framework.NewNodeCircle(framework.DefaultNodeCircleName, nodeLister),
	})

	pl.(framework.LocatingPlugin).Locating(context.Background(), queuedUnitInfo, framework.NewCycleState(), nodeGroup)

	unitCycleState := framework.NewCycleState()
	framework.SetEverScheduledState(false, unitCycleState)
	gotNodeGroupList, status := pl.(framework.GroupingPlugin).Grouping(context.Background(), queuedUnitInfo, unitCycleState, nodeGroup)
	if !status.IsSuccess() {
		t.Fatalf("status: %v", status)
	}
	if len(gotNodeGroupList) != len(expectedNodeGroupList) {
		t.Fatalf("expected length of node groups: %v, got %v", len(expectedNodeGroupList), len(gotNodeGroupList))
	}
	for i, ng := range expectedNodeGroupList {
		if ng.GetKey() != gotNodeGroupList[i].GetKey() {
			t.Fatalf("index: %v, expected node group name: %v, got: %v", i, ng.GetKey(), gotNodeGroupList[i].GetKey())
		}
		expectedNodeCircles, nodeCircles := ng.GetNodeCircles(), gotNodeGroupList[i].GetNodeCircles()
		if len(expectedNodeCircles) != len(nodeCircles) {
			t.Fatalf("expected node circle length: %v, got: %v", len(expectedNodeCircles), len(nodeCircles))
		}
		for j, nc := range expectedNodeCircles {
			if nc.GetKey() != nodeCircles[j].GetKey() {
				t.Fatalf("index: %v, expected node circle name: %v in node group %v, got: %v", j, nc.GetKey(), ng.GetKey(), nodeCircles[j].GetKey())
			}
		}
	}
}

func TestFindNodeGroups(t *testing.T) {
	nodes := []*v1.Node{
		{
			ObjectMeta: metav1.ObjectMeta{
				Name:   "node1",
				Labels: map[string]string{"miniPod": "miniPod1", "tor": "tor1"},
			},
		},
		{
			ObjectMeta: metav1.ObjectMeta{
				Name:   "node2",
				Labels: map[string]string{"miniPod": "miniPod1", "tor": "tor2"},
			},
		},
		{
			ObjectMeta: metav1.ObjectMeta{
				Name:   "node3",
				Labels: map[string]string{"miniPod": "miniPod2", "tor": "tor3"},
			},
		},
		{
			ObjectMeta: metav1.ObjectMeta{
				Name:   "node4",
				Labels: map[string]string{"miniPod": "miniPod2", "tor": "tor4"},
			},
		},
	}
	nodeLister := fake.NewNodeInfoLister(nodes)
	originalNodeGroup := framework.NewNodeGroup(framework.DefaultNodeGroupName, nil, []framework.NodeCircle{
		framework.NewNodeCircle(framework.DefaultNodeCircleName, nodeLister),
	})

	pg := &v1alpha1.PodGroup{
		Spec: v1alpha1.PodGroupSpec{
			MinMember: 1,
			Affinity: &v1alpha1.Affinity{
				PodGroupAffinity: &v1alpha1.PodGroupAffinity{
					Preferred: []v1alpha1.PodGroupAffinityTerm{
						{
							TopologyKey: "miniPod",
						},
						{
							TopologyKey: "tor",
						},
					},
				},
			},
		},
	}
	queuedUnitInfo := &framework.QueuedUnitInfo{
		ScheduleUnit: framework.NewPodGroupUnit(pg, 100),
	}
	queuedPodInfo := &framework.QueuedPodInfo{
		Pod: &v1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name: "testpod",
				Annotations: map[string]string{
					podutil.PodLauncherAnnotationKey:     string(podutil.Kubelet),
					podutil.PodResourceTypeAnnotationKey: string(podutil.GuaranteedPod),
				},
			},
		},
	}
	queuedUnitInfo.AddPod(queuedPodInfo)

	for _, test := range []struct {
		name               string
		assignedNodes      []string
		preferredNodes     []string
		expectedNodeGroups []framework.NodeGroup
	}{
		{
			name:           "stop dividing topology domains before preferred nodes split - 1",
			assignedNodes:  nil,
			preferredNodes: []string{"node1", "node2"},
			expectedNodeGroups: []framework.NodeGroup{
				&framework.NodeGroupImpl{
					Key:         "miniPod:miniPod1;",
					NodeCircles: []framework.NodeCircle{framework.NewNodeCircle("miniPod:miniPod1;", nodeLister)},
				},
				&framework.NodeGroupImpl{
					Key:         "",
					NodeCircles: []framework.NodeCircle{framework.NewNodeCircle("", nodeLister)},
				},
			},
		},
		{
			name:           "stop dividing topology domains before preferred nodes split - 2",
			assignedNodes:  nil,
			preferredNodes: []string{"node1", "node3"},
			expectedNodeGroups: []framework.NodeGroup{
				&framework.NodeGroupImpl{
					Key:         "",
					NodeCircles: []framework.NodeCircle{framework.NewNodeCircle("", nodeLister)},
				},
			},
		},
		{
			name:           "stop dividing topology domains before assigned nodes split - 1",
			assignedNodes:  []string{"node3", "node4"},
			preferredNodes: nil,
			expectedNodeGroups: []framework.NodeGroup{
				&framework.NodeGroupImpl{
					Key:         "miniPod:miniPod2;",
					NodeCircles: []framework.NodeCircle{framework.NewNodeCircle("miniPod:miniPod2;", nodeLister)},
				},
				&framework.NodeGroupImpl{
					Key:         "",
					NodeCircles: []framework.NodeCircle{framework.NewNodeCircle("", nodeLister)},
				},
			},
		},
		{
			name:           "stop dividing topology domains before assigned nodes split - 2",
			assignedNodes:  []string{"node1", "node3"},
			preferredNodes: nil,
			expectedNodeGroups: []framework.NodeGroup{
				&framework.NodeGroupImpl{
					Key:         "",
					NodeCircles: []framework.NodeCircle{framework.NewNodeCircle("", nodeLister)},
				},
			},
		},
		{
			name:           "stop dividing topology domains before the split of assigned nodes and preferred nodes - 1",
			assignedNodes:  []string{"node1"},
			preferredNodes: []string{"node2"},
			expectedNodeGroups: []framework.NodeGroup{
				&framework.NodeGroupImpl{
					Key:         "miniPod:miniPod1;",
					NodeCircles: []framework.NodeCircle{framework.NewNodeCircle("miniPod:miniPod1;", nodeLister)},
				},
				&framework.NodeGroupImpl{
					Key:         "",
					NodeCircles: []framework.NodeCircle{framework.NewNodeCircle("", nodeLister)},
				},
			},
		},
		{
			name:           "stop dividing topology domains before the split of assigned nodes and preferred nodes - 2",
			assignedNodes:  []string{"node1"},
			preferredNodes: []string{"node3"},
			expectedNodeGroups: []framework.NodeGroup{
				&framework.NodeGroupImpl{
					Key:         "",
					NodeCircles: []framework.NodeCircle{framework.NewNodeCircle("", nodeLister)},
				},
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			cache := godelcache.New(commoncache.MakeCacheHandlerWrapper().
				ComponentName("").SchedulerType("").SubCluster(framework.DefaultSubCluster).
				Period(10 * time.Second).StopCh(make(<-chan struct{})).Obj())
			pl, err := New(nil, &fakehandle.MockUnitFrameworkHandle{Cache: cache})
			if err != nil {
				t.Fatalf("err: %v", err)
			}

			assignedNodes := sets.String{}
			assignedNodes.Insert(test.assignedNodes...)
			originalNodeGroup.SetPreferredNodes(framework.NewPreferredNodes())
			for _, n := range test.preferredNodes {
				nodeInfo, _ := originalNodeGroup.Get(n)
				originalNodeGroup.GetPreferredNodes().Add(nodeInfo)
			}

			gotNodeGroups, err := pl.(*JobLevelAffinity).findNodeGroups(context.Background(), queuedUnitInfo, podutil.Kubelet, originalNodeGroup, assignedNodes, false)
			if err != nil {
				t.Fatalf("findNodeGroups failed, err: %v", err)
			}
			if err := checkNodeGroupsEquality(test.expectedNodeGroups, gotNodeGroups); err != nil {
				t.Fatalf("node groups not equal: %v", err)
			}
		})
	}
}

func checkNodeGroupsEquality(expected, got []framework.NodeGroup) error {
	if len(got) != len(expected) {
		return fmt.Errorf("expected length of node groups: %v, got %v", len(expected), len(got))
	}
	for i, ng := range expected {
		if ng.GetKey() != got[i].GetKey() {
			return fmt.Errorf("index: %v, expected node group name: %v, got: %v", i, ng.GetKey(), got[i].GetKey())
		}
		expectedNodeCircles, nodeCircles := ng.GetNodeCircles(), got[i].GetNodeCircles()
		if len(expectedNodeCircles) != len(nodeCircles) {
			return fmt.Errorf("expected node circle length: %v, got: %v", len(expectedNodeCircles), len(nodeCircles))
		}
		for j, nc := range expectedNodeCircles {
			if nc.GetKey() != nodeCircles[j].GetKey() {
				return fmt.Errorf("index: %v, expected node circle name: %v in node group %v, got: %v", j, nc.GetKey(), ng.GetKey(), nodeCircles[j].GetKey())
			}
		}
	}
	return nil
}
