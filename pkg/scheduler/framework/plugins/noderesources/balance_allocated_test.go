/*
Copyright 2023 The Godel Authors.

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
package noderesources

import (
	"context"
	"reflect"
	"testing"
	"time"

	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	utilfeature "k8s.io/apiserver/pkg/util/feature"
	featuregatetesting "k8s.io/component-base/featuregate/testing"

	commoncache "github.com/kubewharf/godel-scheduler/pkg/common/cache"
	framework "github.com/kubewharf/godel-scheduler/pkg/framework/api"
	godelcache "github.com/kubewharf/godel-scheduler/pkg/scheduler/cache"
	testutil "github.com/kubewharf/godel-scheduler/pkg/scheduler/testing"
	"github.com/kubewharf/godel-scheduler/pkg/util/features"
	podutil "github.com/kubewharf/godel-scheduler/pkg/util/pod"
)

// getExistingVolumeCountForNode gets the current number of volumes on node.
func getExistingVolumeCountForNode(pods []*framework.PodInfo, maxVolumes int) int {
	volumeCount := 0
	for _, pod := range pods {
		volumeCount += len(pod.Pod.Spec.Volumes)
	}
	if maxVolumes-volumeCount > 0 {
		return maxVolumes - volumeCount
	}
	return 0
}

func TestNodeResourcesBalancedAllocation(t *testing.T) {
	// Enable volumesOnNodeForBalancing to do balanced node resource allocation
	defer featuregatetesting.SetFeatureGateDuringTest(t, utilfeature.DefaultFeatureGate, features.BalanceAttachedNodeVolumes, true)()
	podwithVol1 := v1.PodSpec{
		Containers: []v1.Container{
			{
				Resources: v1.ResourceRequirements{
					Requests: v1.ResourceList{
						v1.ResourceCPU:    resource.MustParse("1000m"),
						v1.ResourceMemory: resource.MustParse("2000"),
					},
				},
			},
			{
				Resources: v1.ResourceRequirements{
					Requests: v1.ResourceList{
						v1.ResourceCPU:    resource.MustParse("2000m"),
						v1.ResourceMemory: resource.MustParse("3000"),
					},
				},
			},
		},
		Volumes: []v1.Volume{
			{
				VolumeSource: v1.VolumeSource{
					AWSElasticBlockStore: &v1.AWSElasticBlockStoreVolumeSource{VolumeID: "ovp"},
				},
			},
		},
		NodeName: "machine4",
	}
	podwithVol2 := v1.PodSpec{
		Containers: []v1.Container{
			{
				Resources: v1.ResourceRequirements{
					Requests: v1.ResourceList{
						v1.ResourceCPU:    resource.MustParse("0m"),
						v1.ResourceMemory: resource.MustParse("0"),
					},
				},
			},
			{
				Resources: v1.ResourceRequirements{
					Requests: v1.ResourceList{
						v1.ResourceCPU:    resource.MustParse("0m"),
						v1.ResourceMemory: resource.MustParse("0"),
					},
				},
			},
		},
		Volumes: []v1.Volume{
			{
				VolumeSource: v1.VolumeSource{
					AWSElasticBlockStore: &v1.AWSElasticBlockStoreVolumeSource{VolumeID: "ovp1"},
				},
			},
		},
		NodeName: "machine4",
	}
	podwithVol3 := v1.PodSpec{
		Containers: []v1.Container{
			{
				Resources: v1.ResourceRequirements{
					Requests: v1.ResourceList{
						v1.ResourceCPU:    resource.MustParse("0m"),
						v1.ResourceMemory: resource.MustParse("0"),
					},
				},
			},
			{
				Resources: v1.ResourceRequirements{
					Requests: v1.ResourceList{
						v1.ResourceCPU:    resource.MustParse("0m"),
						v1.ResourceMemory: resource.MustParse("0"),
					},
				},
			},
		},
		Volumes: []v1.Volume{
			{
				VolumeSource: v1.VolumeSource{
					AWSElasticBlockStore: &v1.AWSElasticBlockStoreVolumeSource{VolumeID: "ovp1"},
				},
			},
		},
		NodeName: "machine4",
	}
	labels1 := map[string]string{
		"foo": "bar",
		"baz": "blah",
	}
	labels2 := map[string]string{
		"bar": "foo",
		"baz": "blah",
	}
	machine1Spec := v1.PodSpec{
		NodeName: "machine1",
	}
	machine2Spec := v1.PodSpec{
		NodeName: "machine2",
	}
	noResources := v1.PodSpec{
		Containers: []v1.Container{},
	}
	cpuOnly := v1.PodSpec{
		NodeName: "machine1",
		Containers: []v1.Container{
			{
				Resources: v1.ResourceRequirements{
					Requests: v1.ResourceList{
						v1.ResourceCPU:    resource.MustParse("1000m"),
						v1.ResourceMemory: resource.MustParse("0"),
					},
				},
			},
			{
				Resources: v1.ResourceRequirements{
					Requests: v1.ResourceList{
						v1.ResourceCPU:    resource.MustParse("2000m"),
						v1.ResourceMemory: resource.MustParse("0"),
					},
				},
			},
		},
	}
	cpuOnly2 := cpuOnly
	cpuOnly2.NodeName = "machine2"
	cpuAndMemory := v1.PodSpec{
		NodeName: "machine2",
		Containers: []v1.Container{
			{
				Resources: v1.ResourceRequirements{
					Requests: v1.ResourceList{
						v1.ResourceCPU:    resource.MustParse("1000m"),
						v1.ResourceMemory: resource.MustParse("2000"),
					},
				},
			},
			{
				Resources: v1.ResourceRequirements{
					Requests: v1.ResourceList{
						v1.ResourceCPU:    resource.MustParse("2000m"),
						v1.ResourceMemory: resource.MustParse("3000"),
					},
				},
			},
		},
	}
	cpuAndMemory3 := v1.PodSpec{
		NodeName: "machine3",
		Containers: []v1.Container{
			{
				Resources: v1.ResourceRequirements{
					Requests: v1.ResourceList{
						v1.ResourceCPU:    resource.MustParse("1000m"),
						v1.ResourceMemory: resource.MustParse("2000"),
					},
				},
			},
			{
				Resources: v1.ResourceRequirements{
					Requests: v1.ResourceList{
						v1.ResourceCPU:    resource.MustParse("2000m"),
						v1.ResourceMemory: resource.MustParse("3000"),
					},
				},
			},
		},
	}
	tests := []struct {
		pod          *v1.Pod
		pods         []*v1.Pod
		nodes        []*v1.Node
		expectedList framework.NodeScoreList
		name         string
	}{
		{
			// Node1 scores (remaining resources) on 0-10 scale
			// CPU Fraction: 0 / 4000 = 0%
			// Memory Fraction: 0 / 10000 = 0%
			// Node1 Score: 10 - (0-0)*100 = 100
			// Node2 scores (remaining resources) on 0-10 scale
			// CPU Fraction: 0 / 4000 = 0 %
			// Memory Fraction: 0 / 10000 = 0%
			// Node2 Score: 10 - (0-0)*100 = 100
			pod:          &v1.Pod{Spec: noResources, ObjectMeta: metav1.ObjectMeta{UID: types.UID("p")}},
			nodes:        []*v1.Node{MakeNode("machine1", 4000, 10000), MakeNode("machine2", 4000, 10000)},
			expectedList: []framework.NodeScore{{Name: "machine1", Score: framework.MaxNodeScore}, {Name: "machine2", Score: framework.MaxNodeScore}},
			name:         "nothing scheduled, nothing requested",
		},
		{
			// Node1 scores on 0-10 scale
			// CPU Fraction: 3000 / 4000= 75%
			// Memory Fraction: 5000 / 10000 = 50%
			// Node1 Score: 10 - (0.75-0.5)*100 = 75
			// Node2 scores on 0-10 scale
			// CPU Fraction: 3000 / 6000= 50%
			// Memory Fraction: 5000/10000 = 50%
			// Node2 Score: 10 - (0.5-0.5)*100 = 100
			pod:          &v1.Pod{Spec: cpuAndMemory, ObjectMeta: metav1.ObjectMeta{UID: types.UID("p")}},
			nodes:        []*v1.Node{MakeNode("machine1", 4000, 10000), MakeNode("machine2", 6000, 10000)},
			expectedList: []framework.NodeScore{{Name: "machine1", Score: 75}, {Name: "machine2", Score: framework.MaxNodeScore}},
			name:         "nothing scheduled, resources requested, differently sized machines",
		},
		{
			// Node1 scores on 0-10 scale
			// CPU Fraction: 0 / 4000= 0%
			// Memory Fraction: 0 / 10000 = 0%
			// Node1 Score: 10 - (0-0)*100 = 100
			// Node2 scores on 0-10 scale
			// CPU Fraction: 0 / 4000= 0%
			// Memory Fraction: 0 / 10000 = 0%
			// Node2 Score: 10 - (0-0)*100 = 100
			pod:          &v1.Pod{Spec: noResources, ObjectMeta: metav1.ObjectMeta{UID: types.UID("p")}},
			nodes:        []*v1.Node{MakeNode("machine1", 4000, 10000), MakeNode("machine2", 4000, 10000)},
			expectedList: []framework.NodeScore{{Name: "machine1", Score: framework.MaxNodeScore}, {Name: "machine2", Score: framework.MaxNodeScore}},
			name:         "no resources requested, pods scheduled",
			pods: []*v1.Pod{
				{Spec: machine1Spec, ObjectMeta: metav1.ObjectMeta{Labels: labels2, UID: types.UID("p1")}},
				{Spec: machine1Spec, ObjectMeta: metav1.ObjectMeta{Labels: labels1, UID: types.UID("p2")}},
				{Spec: machine2Spec, ObjectMeta: metav1.ObjectMeta{Labels: labels1, UID: types.UID("p3")}},
				{Spec: machine2Spec, ObjectMeta: metav1.ObjectMeta{Labels: labels1, UID: types.UID("p4")}},
			},
		},
		{
			// Node1 scores on 0-10 scale
			// CPU Fraction: 6000 / 10000 = 60%
			// Memory Fraction: 0 / 20000 = 0%
			// Node1 Score: 10 - (0.6-0)*100 = 40
			// Node2 scores on 0-10 scale
			// CPU Fraction: 6000 / 10000 = 60%
			// Memory Fraction: 5000 / 20000 = 25%
			// Node2 Score: 10 - (0.6-0.25)*100 = 65
			pod:          &v1.Pod{Spec: noResources, ObjectMeta: metav1.ObjectMeta{UID: types.UID("p")}},
			nodes:        []*v1.Node{MakeNode("machine1", 10000, 20000), MakeNode("machine2", 10000, 20000)},
			expectedList: []framework.NodeScore{{Name: "machine1", Score: 40}, {Name: "machine2", Score: 65}},
			name:         "no resources requested, pods scheduled with resources",
			pods: []*v1.Pod{
				{Spec: cpuOnly, ObjectMeta: metav1.ObjectMeta{Labels: labels2, UID: types.UID("p1")}},
				{Spec: cpuOnly, ObjectMeta: metav1.ObjectMeta{Labels: labels1, UID: types.UID("p2")}},
				{Spec: cpuOnly2, ObjectMeta: metav1.ObjectMeta{Labels: labels1, UID: types.UID("p3")}},
				{Spec: cpuAndMemory, ObjectMeta: metav1.ObjectMeta{Labels: labels1, UID: types.UID("p4")}},
			},
		},
		{
			// Node1 scores on 0-10 scale
			// CPU Fraction: 6000 / 10000 = 60%
			// Memory Fraction: 5000 / 20000 = 25%
			// Node1 Score: 10 - (0.6-0.25)*100 = 65
			// Node2 scores on 0-10 scale
			// CPU Fraction: 6000 / 10000 = 60%
			// Memory Fraction: 10000 / 20000 = 50%
			// Node2 Score: 10 - (0.6-0.5)*100 = 9
			pod:          &v1.Pod{Spec: cpuAndMemory, ObjectMeta: metav1.ObjectMeta{UID: types.UID("p")}},
			nodes:        []*v1.Node{MakeNode("machine1", 10000, 20000), MakeNode("machine2", 10000, 20000)},
			expectedList: []framework.NodeScore{{Name: "machine1", Score: 65}, {Name: "machine2", Score: 90}},
			name:         "resources requested, pods scheduled with resources",
			pods: []*v1.Pod{
				{Spec: cpuOnly, ObjectMeta: metav1.ObjectMeta{UID: types.UID("p1")}},
				{Spec: cpuAndMemory, ObjectMeta: metav1.ObjectMeta{UID: types.UID("p2")}},
			},
		},
		{
			// Node1 scores on 0-10 scale
			// CPU Fraction: 6000 / 10000 = 60%
			// Memory Fraction: 5000 / 20000 = 25%
			// Node1 Score: 10 - (0.6-0.25)*100 = 65
			// Node2 scores on 0-10 scale
			// CPU Fraction: 6000 / 10000 = 60%
			// Memory Fraction: 10000 / 50000 = 20%
			// Node2 Score: 10 - (0.6-0.2)*100 = 60
			pod:          &v1.Pod{Spec: cpuAndMemory, ObjectMeta: metav1.ObjectMeta{UID: types.UID("p")}},
			nodes:        []*v1.Node{MakeNode("machine1", 10000, 20000), MakeNode("machine2", 10000, 50000)},
			expectedList: []framework.NodeScore{{Name: "machine1", Score: 65}, {Name: "machine2", Score: 60}},
			name:         "resources requested, pods scheduled with resources, differently sized machines",
			pods: []*v1.Pod{
				{Spec: cpuOnly, ObjectMeta: metav1.ObjectMeta{UID: types.UID("p1")}},
				{Spec: cpuAndMemory, ObjectMeta: metav1.ObjectMeta{UID: types.UID("p2")}},
			},
		},
		{
			// Node1 scores on 0-10 scale
			// CPU Fraction: 6000 / 4000 > 100% ==> Score := 0
			// Memory Fraction: 0 / 10000 = 0
			// Node1 Score: 0
			// Node2 scores on 0-10 scale
			// CPU Fraction: 6000 / 4000 > 100% ==> Score := 0
			// Memory Fraction 5000 / 10000 = 50%
			// Node2 Score: 0
			pod:          &v1.Pod{Spec: cpuOnly, ObjectMeta: metav1.ObjectMeta{UID: types.UID("p")}},
			nodes:        []*v1.Node{MakeNode("machine1", 4000, 10000), MakeNode("machine2", 4000, 10000)},
			expectedList: []framework.NodeScore{{Name: "machine1", Score: 0}, {Name: "machine2", Score: 0}},
			name:         "requested resources exceed node capacity",
			pods: []*v1.Pod{
				{Spec: cpuOnly, ObjectMeta: metav1.ObjectMeta{UID: types.UID("p1")}},
				{Spec: cpuAndMemory, ObjectMeta: metav1.ObjectMeta{UID: types.UID("p2")}},
			},
		},
		{
			pod:          &v1.Pod{Spec: noResources, ObjectMeta: metav1.ObjectMeta{UID: types.UID("p")}},
			nodes:        []*v1.Node{MakeNode("machine1", 0, 0), MakeNode("machine2", 0, 0)},
			expectedList: []framework.NodeScore{{Name: "machine1", Score: 0}, {Name: "machine2", Score: 0}},
			name:         "zero node resources, pods scheduled with resources",
			pods: []*v1.Pod{
				{Spec: cpuOnly, ObjectMeta: metav1.ObjectMeta{UID: types.UID("p1")}},
				{Spec: cpuAndMemory, ObjectMeta: metav1.ObjectMeta{UID: types.UID("p2")}},
			},
		},
		{
			// Machine4 will be chosen here because it already has a existing volume making the variance
			// of volume count, CPU usage, memory usage closer.
			pod: &v1.Pod{
				Spec: v1.PodSpec{
					Volumes: []v1.Volume{
						{
							VolumeSource: v1.VolumeSource{
								AWSElasticBlockStore: &v1.AWSElasticBlockStoreVolumeSource{VolumeID: "ovp2"},
							},
						},
					},
				},
				ObjectMeta: metav1.ObjectMeta{UID: types.UID("p")},
			},
			nodes:        []*v1.Node{MakeNode("machine3", 3500, 40000), MakeNode("machine4", 4000, 10000)},
			expectedList: []framework.NodeScore{{Name: "machine3", Score: 89}, {Name: "machine4", Score: 98}},
			name:         "Include volume count on a node for balanced resource allocation",
			pods: []*v1.Pod{
				{Spec: cpuAndMemory3, ObjectMeta: metav1.ObjectMeta{UID: types.UID("p1")}},
				{Spec: podwithVol1, ObjectMeta: metav1.ObjectMeta{UID: types.UID("p2")}},
				{Spec: podwithVol2, ObjectMeta: metav1.ObjectMeta{UID: types.UID("p3")}},
				{Spec: podwithVol3, ObjectMeta: metav1.ObjectMeta{UID: types.UID("p4")}},
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			cache := godelcache.New(commoncache.MakeCacheHandlerWrapper().
				ComponentName("").SchedulerType("").SubCluster(framework.DefaultSubCluster).
				PodAssumedTTL(time.Second).Period(10 * time.Second).StopCh(make(<-chan struct{})).
				EnableStore("PreemptionStore").
				Obj())
			snapshot := godelcache.NewEmptySnapshot(commoncache.MakeCacheHandlerWrapper().
				SubCluster(framework.DefaultSubCluster).SwitchType(framework.DefaultSubClusterSwitchType).
				EnableStore("PreemptionStore").
				Obj())

			for _, p := range test.pods {
				cache.AddPod(p)
			}
			for _, n := range test.nodes {
				cache.AddNode(n)
			}
			cache.UpdateSnapshot(snapshot)

			if len(test.pod.Spec.Volumes) > 0 {
				maxVolumes := 5
				nodeInfoList := snapshot.NodeInfos().List()
				for _, info := range nodeInfoList {
					info.GetTransientInfo().TransNodeInfo.AllocatableVolumesCount = getExistingVolumeCountForNode(info.GetPods(), maxVolumes)
					info.GetTransientInfo().TransNodeInfo.RequestedVolumes = len(test.pod.Spec.Volumes)
				}
			}
			fh, _ := testutil.NewPodFrameworkHandle(nil, nil, nil, nil, nil, snapshot, nil, nil, nil, nil)
			p, _ := NewBalancedAllocation(nil, fh)
			cycleState := framework.NewCycleState()
			framework.SetPodResourceTypeState(podutil.GuaranteedPod, cycleState)
			for i := range test.nodes {
				hostResult, err := p.(framework.ScorePlugin).Score(context.Background(), cycleState, test.pod, test.nodes[i].Name)
				if err != nil {
					t.Errorf("unexpected error: %v", err)
				}
				if !reflect.DeepEqual(test.expectedList[i].Score, hostResult) {
					t.Errorf("expected %#v, got %#v", test.expectedList[i].Score, hostResult)
				}
			}
		})
	}
}
