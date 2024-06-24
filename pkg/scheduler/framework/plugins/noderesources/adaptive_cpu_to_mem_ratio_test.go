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

package noderesources

import (
	"context"
	"reflect"
	"testing"
	"time"

	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"

	commoncache "github.com/kubewharf/godel-scheduler/pkg/common/cache"
	framework "github.com/kubewharf/godel-scheduler/pkg/framework/api"
	godelcache "github.com/kubewharf/godel-scheduler/pkg/scheduler/cache"
	st "github.com/kubewharf/godel-scheduler/pkg/scheduler/testing"
	podutil "github.com/kubewharf/godel-scheduler/pkg/util/pod"
)

func TestAdaptiveCpuToMemRatio(t *testing.T) {
	cpuAndMemory := v1.PodSpec{
		Containers: []v1.Container{
			{
				Resources: v1.ResourceRequirements{
					Requests: v1.ResourceList{
						v1.ResourceCPU:    resource.MustParse("2000m"),
						v1.ResourceMemory: resource.MustParse("2Gi"),
					},
				},
			},
			{
				Resources: v1.ResourceRequirements{
					Requests: v1.ResourceList{
						v1.ResourceCPU:    resource.MustParse("2000m"),
						v1.ResourceMemory: resource.MustParse("4Gi"),
					},
				},
			},
		},
	}
	onlyCpu := v1.PodSpec{
		Containers: []v1.Container{
			{
				Resources: v1.ResourceRequirements{
					Requests: v1.ResourceList{
						v1.ResourceCPU:    resource.MustParse("2000m"),
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
	onlyMemory := v1.PodSpec{
		Containers: []v1.Container{
			{
				Resources: v1.ResourceRequirements{
					Requests: v1.ResourceList{
						v1.ResourceCPU:    resource.MustParse("0m"),
						v1.ResourceMemory: resource.MustParse("1Gi"),
					},
				},
			},
			{
				Resources: v1.ResourceRequirements{
					Requests: v1.ResourceList{
						v1.ResourceCPU:    resource.MustParse("0m"),
						v1.ResourceMemory: resource.MustParse("1Gi"),
					},
				},
			},
		},
	}
	tests := []struct {
		pod          *v1.Pod
		nodes        []*v1.Node
		expectedList framework.NodeScoreList
		name         string
	}{
		{
			pod:          &v1.Pod{Spec: cpuAndMemory},
			nodes:        []*v1.Node{MakeNode("machine1", 8000, 12884901888), MakeNode("machine2", 8000, 10737418240), MakeNode("machine3", 8000, 15032385536)}, // 8C 12Gi, 8C 10Gi, 8C 14Gi
			expectedList: []framework.NodeScore{{Name: "machine1", Score: framework.MaxNodeScore}, {Name: "machine2", Score: 44}, {Name: "machine3", Score: 92}},
			name:         "test1",
		},
		{
			pod:          &v1.Pod{Spec: onlyCpu},
			nodes:        []*v1.Node{MakeNode("machine1", 8000, 0), MakeNode("machine2", 8000, 10737418240)}, // 8C 0Gi, 8C, 10Gi
			expectedList: []framework.NodeScore{{Name: "machine1", Score: framework.MaxNodeScore}, {Name: "machine2", Score: framework.MaxNodeScore}},
			name:         "test2",
		},
		{
			pod:          &v1.Pod{Spec: onlyMemory},
			nodes:        []*v1.Node{MakeNode("machine1", 8000, 2147483648), MakeNode("machine2", 8000, 16106127360)}, // 8C 2Gi, 8C 15Gi
			expectedList: []framework.NodeScore{{Name: "machine1", Score: framework.MaxNodeScore}, {Name: "machine2", Score: framework.MaxNodeScore}},
			name:         "test3",
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

			for _, n := range test.nodes {
				cache.AddNode(n)
			}
			cache.UpdateSnapshot(snapshot)
			fh, _ := st.NewPodFrameworkHandle(nil, nil, nil, nil, nil, snapshot, nil, nil, nil, nil)
			p, _ := NewAdaptiveCpuToMemRatio(nil, fh)
			state := framework.NewCycleState()
			framework.SetPodResourceTypeState(podutil.GuaranteedPod, state)
			for i := range test.nodes {
				hostResult, err := p.(framework.ScorePlugin).Score(context.Background(), state, test.pod, test.nodes[i].Name)
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
