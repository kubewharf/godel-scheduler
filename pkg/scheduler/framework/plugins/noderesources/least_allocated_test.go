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

package noderesources

import (
	"context"
	"reflect"
	"testing"
	"time"

	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	commoncache "github.com/kubewharf/godel-scheduler/pkg/common/cache"
	framework "github.com/kubewharf/godel-scheduler/pkg/framework/api"
	"github.com/kubewharf/godel-scheduler/pkg/scheduler/apis/config"
	godelcache "github.com/kubewharf/godel-scheduler/pkg/scheduler/cache"
	st "github.com/kubewharf/godel-scheduler/pkg/scheduler/testing"
	podutil "github.com/kubewharf/godel-scheduler/pkg/util/pod"
)

func TestNodeResourcesLeastAllocated(t *testing.T) {
	labels1 := map[string]string{
		"foo": "bar",
		"baz": "blah",
	}
	labels2 := map[string]string{
		"bar": "foo",
		"baz": "blah",
	}
	guaranteedPodAnnotations := map[string]string{
		podutil.PodResourceTypeAnnotationKey: string(podutil.GuaranteedPod),
		podutil.PodLauncherAnnotationKey:     string(podutil.Kubelet),
	}
	guaranteedPodMeta := metav1.ObjectMeta{
		Annotations: guaranteedPodAnnotations,
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
	defaultResourceLeastAllocatedSet := []config.ResourceSpec{
		{Name: string(v1.ResourceCPU), Weight: 1},
		{Name: string(v1.ResourceMemory), Weight: 1},
	}
	tests := []struct {
		pod          *v1.Pod
		pods         []*v1.Pod
		nodes        []*v1.Node
		args         config.NodeResourcesLeastAllocatedArgs
		wantErr      string
		expectedList framework.NodeScoreList
		name         string
	}{
		{
			// Node1 scores (remaining resources) on 0-MaxNodeScore scale
			// CPU Score: ((4000 - 0) * MaxNodeScore) / 4000 = MaxNodeScore
			// Memory Score: ((10000 - 0) * MaxNodeScore) / 10000 = MaxNodeScore
			// Node1 Score: (100 + 100) / 2 = 100
			// Node2 scores (remaining resources) on 0-MaxNodeScore scale
			// CPU Score: ((4000 - 0) * MaxNodeScore) / 4000 = MaxNodeScore
			// Memory Score: ((10000 - 0) * MaxNodeScore) / 10000 = MaxNodeScore
			// Node2 Score: (MaxNodeScore + MaxNodeScore) / 2 = MaxNodeScore
			pod:          &v1.Pod{Spec: noResources, ObjectMeta: guaranteedPodMeta},
			nodes:        []*v1.Node{MakeNode("machine1", 4000, 10000), MakeNode("machine2", 4000, 10000)},
			args:         config.NodeResourcesLeastAllocatedArgs{Resources: defaultResourceLeastAllocatedSet},
			expectedList: []framework.NodeScore{{Name: "machine1", Score: framework.MaxNodeScore}, {Name: "machine2", Score: framework.MaxNodeScore}},
			name:         "nothing scheduled, nothing requested",
		},
		{
			// Node1 scores on 0-MaxNodeScore scale
			// CPU Score: ((4000 - 3000) * MaxNodeScore) / 4000 = 25
			// Memory Score: ((10000 - 5000) * MaxNodeScore) / 10000 = 50
			// Node1 Score: (25 + 50) / 2 = 37
			// Node2 scores on 0-MaxNodeScore scale
			// CPU Score: ((6000 - 3000) * MaxNodeScore) / 6000 = 50
			// Memory Score: ((10000 - 5000) * MaxNodeScore) / 10000 = 50
			// Node2 Score: (50 + 50) / 2 = 50
			pod:          &v1.Pod{Spec: cpuAndMemory, ObjectMeta: guaranteedPodMeta},
			nodes:        []*v1.Node{MakeNode("machine1", 4000, 10000), MakeNode("machine2", 6000, 10000)},
			args:         config.NodeResourcesLeastAllocatedArgs{Resources: defaultResourceLeastAllocatedSet},
			expectedList: []framework.NodeScore{{Name: "machine1", Score: 37}, {Name: "machine2", Score: 50}},
			name:         "nothing scheduled, resources requested, differently sized machines",
		},
		{
			// Node1 scores on 0-MaxNodeScore scale
			// CPU Score: ((4000 - 0) * MaxNodeScore) / 4000 = MaxNodeScore
			// Memory Score: ((10000 - 0) * MaxNodeScore) / 10000 = MaxNodeScore
			// Node1 Score: (MaxNodeScore + MaxNodeScore) / 2 = MaxNodeScore
			// Node2 scores on 0-MaxNodeScore scale
			// CPU Score: ((4000 - 0) * MaxNodeScore) / 4000 = MaxNodeScore
			// Memory Score: ((10000 - 0) * MaxNodeScore) / 10000 = MaxNodeScore
			// Node2 Score: (MaxNodeScore + MaxNodeScore) / 2 = MaxNodeScore
			pod:          &v1.Pod{Spec: noResources, ObjectMeta: guaranteedPodMeta},
			nodes:        []*v1.Node{MakeNode("machine1", 4000, 10000), MakeNode("machine2", 4000, 10000)},
			args:         config.NodeResourcesLeastAllocatedArgs{Resources: defaultResourceLeastAllocatedSet},
			expectedList: []framework.NodeScore{{Name: "machine1", Score: framework.MaxNodeScore}, {Name: "machine2", Score: framework.MaxNodeScore}},
			name:         "no resources requested, pods scheduled",
			pods: []*v1.Pod{
				{Spec: machine1Spec, ObjectMeta: metav1.ObjectMeta{Labels: labels2, Annotations: guaranteedPodAnnotations}},
				{Spec: machine1Spec, ObjectMeta: metav1.ObjectMeta{Labels: labels1, Annotations: guaranteedPodAnnotations}},
				{Spec: machine2Spec, ObjectMeta: metav1.ObjectMeta{Labels: labels1, Annotations: guaranteedPodAnnotations}},
				{Spec: machine2Spec, ObjectMeta: metav1.ObjectMeta{Labels: labels1, Annotations: guaranteedPodAnnotations}},
			},
		},
		{
			// Node1 scores on 0-MaxNodeScore scale
			// CPU Score: ((10000 - 6000) * MaxNodeScore) / 10000 = 40
			// Memory Score: ((20000 - 0) * MaxNodeScore) / 20000 = MaxNodeScore
			// Node1 Score: (40 + 100) / 2 = 70
			// Node2 scores on 0-MaxNodeScore scale
			// CPU Score: ((10000 - 6000) * MaxNodeScore) / 10000 = 40
			// Memory Score: ((20000 - 5000) * MaxNodeScore) / 20000 = 75
			// Node2 Score: (40 + 75) / 2 = 57
			pod:          &v1.Pod{Spec: noResources, ObjectMeta: guaranteedPodMeta},
			nodes:        []*v1.Node{MakeNode("machine1", 10000, 20000), MakeNode("machine2", 10000, 20000)},
			args:         config.NodeResourcesLeastAllocatedArgs{Resources: defaultResourceLeastAllocatedSet},
			expectedList: []framework.NodeScore{{Name: "machine1", Score: 70}, {Name: "machine2", Score: 57}},
			name:         "no resources requested, pods scheduled with resources",
			pods: []*v1.Pod{
				{Spec: cpuOnly, ObjectMeta: metav1.ObjectMeta{Labels: labels2, Annotations: guaranteedPodAnnotations}},
				{Spec: cpuOnly, ObjectMeta: metav1.ObjectMeta{Labels: labels1, Annotations: guaranteedPodAnnotations}},
				{Spec: cpuOnly2, ObjectMeta: metav1.ObjectMeta{Labels: labels1, Annotations: guaranteedPodAnnotations}},
				{Spec: cpuAndMemory, ObjectMeta: metav1.ObjectMeta{Labels: labels1, Annotations: guaranteedPodAnnotations}},
			},
		},
		{
			// Node1 scores on 0-MaxNodeScore scale
			// CPU Score: ((10000 - 6000) * MaxNodeScore) / 10000 = 40
			// Memory Score: ((20000 - 5000) * MaxNodeScore) / 20000 = 75
			// Node1 Score: (40 + 75) / 2 = 57
			// Node2 scores on 0-MaxNodeScore scale
			// CPU Score: ((10000 - 6000) * MaxNodeScore) / 10000 = 40
			// Memory Score: ((20000 - 10000) * MaxNodeScore) / 20000 = 50
			// Node2 Score: (40 + 50) / 2 = 45
			pod:          &v1.Pod{Spec: cpuAndMemory, ObjectMeta: guaranteedPodMeta},
			nodes:        []*v1.Node{MakeNode("machine1", 10000, 20000), MakeNode("machine2", 10000, 20000)},
			args:         config.NodeResourcesLeastAllocatedArgs{Resources: defaultResourceLeastAllocatedSet},
			expectedList: []framework.NodeScore{{Name: "machine1", Score: 57}, {Name: "machine2", Score: 45}},
			name:         "resources requested, pods scheduled with resources",
			pods: []*v1.Pod{
				{Spec: cpuOnly, ObjectMeta: guaranteedPodMeta},
				{Spec: cpuAndMemory, ObjectMeta: guaranteedPodMeta},
			},
		},
		{
			// Node1 scores on 0-MaxNodeScore scale
			// CPU Score: ((10000 - 6000) * MaxNodeScore) / 10000 = 40
			// Memory Score: ((20000 - 5000) * MaxNodeScore) / 20000 = 75
			// Node1 Score: (40 + 75) / 2 = 57
			// Node2 scores on 0-MaxNodeScore scale
			// CPU Score: ((10000 - 6000) * MaxNodeScore) / 10000 = 40
			// Memory Score: ((50000 - 10000) * MaxNodeScore) / 50000 = 80
			// Node2 Score: (40 + 80) / 2 = 60
			pod:          &v1.Pod{Spec: cpuAndMemory, ObjectMeta: guaranteedPodMeta},
			nodes:        []*v1.Node{MakeNode("machine1", 10000, 20000), MakeNode("machine2", 10000, 50000)},
			args:         config.NodeResourcesLeastAllocatedArgs{Resources: defaultResourceLeastAllocatedSet},
			expectedList: []framework.NodeScore{{Name: "machine1", Score: 57}, {Name: "machine2", Score: 60}},
			name:         "resources requested, pods scheduled with resources, differently sized machines",
			pods: []*v1.Pod{
				{Spec: cpuOnly, ObjectMeta: guaranteedPodMeta},
				{Spec: cpuAndMemory, ObjectMeta: guaranteedPodMeta},
			},
		},
		{
			// Node1 scores on 0-MaxNodeScore scale
			// CPU Score: ((4000 - 6000) * MaxNodeScore) / 4000 = 0
			// Memory Score: ((10000 - 0) * MaxNodeScore) / 10000 = MaxNodeScore
			// Node1 Score: (0 + MaxNodeScore) / 2 = 50
			// Node2 scores on 0-MaxNodeScore scale
			// CPU Score: ((4000 - 6000) * MaxNodeScore) / 4000 = 0
			// Memory Score: ((10000 - 5000) * MaxNodeScore) / 10000 = 50
			// Node2 Score: (0 + 50) / 2 = 25
			pod:          &v1.Pod{Spec: cpuOnly, ObjectMeta: guaranteedPodMeta},
			nodes:        []*v1.Node{MakeNode("machine1", 4000, 10000), MakeNode("machine2", 4000, 10000)},
			args:         config.NodeResourcesLeastAllocatedArgs{Resources: defaultResourceLeastAllocatedSet},
			expectedList: []framework.NodeScore{{Name: "machine1", Score: 50}, {Name: "machine2", Score: 25}},
			name:         "requested resources exceed node capacity",
			pods: []*v1.Pod{
				{Spec: cpuOnly, ObjectMeta: guaranteedPodMeta},
				{Spec: cpuAndMemory, ObjectMeta: guaranteedPodMeta},
			},
		},
		{
			pod:          &v1.Pod{Spec: noResources, ObjectMeta: guaranteedPodMeta},
			nodes:        []*v1.Node{MakeNode("machine1", 0, 0), MakeNode("machine2", 0, 0)},
			args:         config.NodeResourcesLeastAllocatedArgs{Resources: defaultResourceLeastAllocatedSet},
			expectedList: []framework.NodeScore{{Name: "machine1", Score: 0}, {Name: "machine2", Score: 0}},
			name:         "zero node resources, pods scheduled with resources",
			pods: []*v1.Pod{
				{Spec: cpuOnly, ObjectMeta: guaranteedPodMeta},
				{Spec: cpuAndMemory, ObjectMeta: guaranteedPodMeta},
			},
		},
		{
			// CPU Score: ((4000 - 3000) *100) / 4000 = 25
			// Memory Score: ((10000 - 5000) *100) / 10000 = 50
			// Node1 Score: (25 * 1 + 50 * 2) / (1 + 2) = 41
			// CPU Score: ((6000 - 3000) *100) / 6000 = 50
			// Memory Score: ((10000 - 5000) *100) / 10000 = 50
			// Node2 Score: (50 * 1 + 50 * 2) / (1 + 2) = 50
			pod:          &v1.Pod{Spec: cpuAndMemory, ObjectMeta: guaranteedPodMeta},
			nodes:        []*v1.Node{MakeNode("machine1", 4000, 10000), MakeNode("machine2", 6000, 10000)},
			args:         config.NodeResourcesLeastAllocatedArgs{Resources: []config.ResourceSpec{{Name: "memory", Weight: 2}, {Name: "cpu", Weight: 1}}},
			expectedList: []framework.NodeScore{{Name: "machine1", Score: 41}, {Name: "machine2", Score: 50}},
			name:         "nothing scheduled, resources requested with different weight on CPU and memory, differently sized machines",
		},
		{
			// resource with negtive weight is not allowed
			pod:     &v1.Pod{Spec: cpuAndMemory, ObjectMeta: guaranteedPodMeta},
			nodes:   []*v1.Node{MakeNode("machine", 4000, 10000)},
			args:    config.NodeResourcesLeastAllocatedArgs{Resources: []config.ResourceSpec{{Name: "memory", Weight: -1}, {Name: "cpu", Weight: 1}}},
			wantErr: "resource Weight of memory should be a positive value, got -1",
			name:    "resource with negtive weight",
		},
		{
			// resource with zero weight is not allowed
			pod:     &v1.Pod{Spec: cpuAndMemory, ObjectMeta: guaranteedPodMeta},
			nodes:   []*v1.Node{MakeNode("machine", 4000, 10000)},
			args:    config.NodeResourcesLeastAllocatedArgs{Resources: []config.ResourceSpec{{Name: "memory", Weight: 1}, {Name: "cpu", Weight: 0}}},
			wantErr: "resource Weight of cpu should be a positive value, got 0",
			name:    "resource with zero weight",
		},
		{
			// resource weight should be less than MaxNodeScore
			pod:     &v1.Pod{Spec: cpuAndMemory, ObjectMeta: guaranteedPodMeta},
			nodes:   []*v1.Node{MakeNode("machine", 4000, 10000)},
			args:    config.NodeResourcesLeastAllocatedArgs{Resources: []config.ResourceSpec{{Name: "memory", Weight: 120}}},
			wantErr: "resource Weight of memory should be less than 100, got 120",
			name:    "resource weight larger than MaxNodeScore",
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

			fh, _ := st.NewPodFrameworkHandle(nil, nil, nil, nil, nil, snapshot, nil, nil, nil, nil)
			p, err := NewLeastAllocated(&test.args, fh)

			if len(test.wantErr) != 0 {
				if err != nil && test.wantErr != err.Error() {
					t.Fatalf("got err %v, want %v", err.Error(), test.wantErr)
				} else if err == nil {
					t.Fatalf("no error produced, wanted %v", test.wantErr)
				}
				return
			}

			if err != nil && len(test.wantErr) == 0 {
				t.Fatalf("failed to initialize plugin NodeResourcesLeastAllocated, got error: %v", err)
			}

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
