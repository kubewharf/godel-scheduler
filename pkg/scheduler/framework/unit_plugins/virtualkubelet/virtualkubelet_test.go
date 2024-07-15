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

package virtualkubelet

import (
	"context"
	"testing"
	"time"

	"github.com/kubewharf/godel-scheduler-api/pkg/apis/scheduling/v1alpha1"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/sets"

	commoncache "github.com/kubewharf/godel-scheduler/pkg/common/cache"
	framework "github.com/kubewharf/godel-scheduler/pkg/framework/api"
	"github.com/kubewharf/godel-scheduler/pkg/framework/api/fake"
	godelcache "github.com/kubewharf/godel-scheduler/pkg/scheduler/cache"
	"github.com/kubewharf/godel-scheduler/pkg/scheduler/testing/fakehandle"
	podutil "github.com/kubewharf/godel-scheduler/pkg/util/pod"
)

func makeVirtualKubeletNodeSelector() map[string]string {
	return map[string]string{VirtualKubeletKey: VirtualKubeletValue}
}

func createPodGroupUnit(pods []*v1.Pod) framework.ScheduleUnit {
	unit := framework.NewPodGroupUnit(&v1alpha1.PodGroup{}, 0)
	for _, p := range pods {
		unit.AddPod(&framework.QueuedPodInfo{
			Pod: p,
		})
	}
	return unit
}

func TestLocating(t *testing.T) {
	node1 := &v1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name:   "node-1",
			Labels: makeVirtualKubeletNodeSelector(),
		},
	}
	node2 := &v1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name: "node-2",
		},
	}
	nodeLister := fake.NewNodeInfoLister([]*v1.Node{node1, node2})

	tests := []struct {
		name     string
		unit     framework.ScheduleUnit
		expected sets.String
	}{
		{
			name: "non-virtualkubelet pod",
			unit: framework.NewSinglePodUnit(&framework.QueuedPodInfo{
				Pod: &v1.Pod{
					ObjectMeta: metav1.ObjectMeta{
						Name: "p",
						UID:  "p",
					},
				},
			}),
			expected: sets.NewString("node-1", "node-2"),
		},
		{
			name: "virtualkubelet pod",
			unit: framework.NewSinglePodUnit(&framework.QueuedPodInfo{
				Pod: &v1.Pod{
					ObjectMeta: metav1.ObjectMeta{
						Name: "p",
						UID:  "p",
						OwnerReferences: []metav1.OwnerReference{
							{Kind: podutil.DaemonSetKind},
						},
					},
					Spec: v1.PodSpec{
						NodeSelector: makeVirtualKubeletNodeSelector(),
					},
				},
			}),
			expected: sets.NewString("node-1"),
		},
		{
			name: "mixed multiple pods",
			unit: createPodGroupUnit([]*v1.Pod{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: "p",
						UID:  "p",
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: "p2",
						UID:  "p2",
						OwnerReferences: []metav1.OwnerReference{
							{Kind: podutil.DaemonSetKind},
						},
					},
					Spec: v1.PodSpec{
						NodeSelector: makeVirtualKubeletNodeSelector(),
					},
				},
			}),
			expected: sets.NewString("node-1", "node-2"),
		},
	}

	for _, tt := range tests {
		cache := godelcache.New(commoncache.MakeCacheHandlerWrapper().
			ComponentName("").SchedulerType("").SubCluster(framework.DefaultSubCluster).
			PodAssumedTTL(time.Second).Period(10 * time.Second).StopCh(make(<-chan struct{})).
			EnableStore("PreemptionStore").
			Obj())

		nodeGroup := framework.NewNodeGroup(framework.DefaultNodeGroupName, []framework.NodeCircle{framework.NewNodeCircle(framework.DefaultNodeCircleName, nodeLister)})

		pl, err := New(nil, &fakehandle.MockUnitFrameworkHandle{Cache: cache})
		if err != nil {
			t.Fatalf("err: %v", err)
		}

		outputNodeGroup, status := pl.(framework.LocatingPlugin).Locating(context.Background(), tt.unit, framework.NewCycleState(), nodeGroup)
		if status != nil {
			t.Fatalf("failed to locating node group: %v", status)
		}
		nodes := outputNodeGroup.GetNodeCircles()[0].OutOfPartitionList()

		gotNodeNames := sets.NewString()
		for _, node := range nodes {
			gotNodeNames.Insert(node.GetNodeName())
		}
		if !tt.expected.Equal(gotNodeNames) {
			t.Errorf("test case %v expected %#v, got %#v", tt.name, tt.expected.List(), gotNodeNames.List())
		}
	}
}
