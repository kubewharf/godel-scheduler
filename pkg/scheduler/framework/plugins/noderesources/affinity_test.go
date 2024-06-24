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
	"testing"
	"time"

	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	commoncache "github.com/kubewharf/godel-scheduler/pkg/common/cache"
	framework "github.com/kubewharf/godel-scheduler/pkg/framework/api"
	"github.com/kubewharf/godel-scheduler/pkg/scheduler/apis/config"
	godelcache "github.com/kubewharf/godel-scheduler/pkg/scheduler/cache"
	testutil "github.com/kubewharf/godel-scheduler/pkg/scheduler/testing"
	testing_helper "github.com/kubewharf/godel-scheduler/pkg/testing-helper"
	podutil "github.com/kubewharf/godel-scheduler/pkg/util/pod"
)

func makeResourcePod(res map[v1.ResourceName]string) *v1.Pod {
	return testing_helper.MakePod().Req(res).Obj()
}

func makeResourceNode(name string, resMap map[v1.ResourceName]string) *v1.Node {
	rl := v1.ResourceList{}
	for resourceName, v := range resMap {
		rl[resourceName] = resource.MustParse(v)
	}

	return &v1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
		},
		Status: v1.NodeStatus{
			Capacity: v1.NodeResources{
				Capacity: rl,
			}.Capacity,
			Allocatable: rl,
		},
	}
}

func newFakeNodeInfo(node *v1.Node) framework.NodeInfo {
	nodeInfo := framework.NewNodeInfo()
	nodeInfo.SetNode(node)
	return nodeInfo
}

var (
	fooResource = v1.ResourceName("scheduling.k8s.io/foo")
	barResource = v1.ResourceName("scheduling.k8s.io/bar")
)

func TestNodeResourcesAffinity_Score(t *testing.T) {
	pluginArgs := &config.NodeResourcesAffinityArgs{
		Resources: []config.ResourceSpec{
			{fooResource.String(), 1, podutil.GuaranteedPod},
			{barResource.String(), 3, podutil.GuaranteedPod},
		},
	}

	tests := []struct {
		name         string
		pod          *v1.Pod
		nodes        []framework.NodeInfo
		expectedList framework.NodeScoreList
	}{
		{
			name: "pod require resources (foo: 1, bar: 1)",
			pod:  makeResourcePod(map[v1.ResourceName]string{fooResource: "1", barResource: "1"}),
			nodes: []framework.NodeInfo{
				newFakeNodeInfo(makeResourceNode("node-0", map[v1.ResourceName]string{})),
				newFakeNodeInfo(makeResourceNode("node-1", map[v1.ResourceName]string{fooResource: "1"})),
				newFakeNodeInfo(makeResourceNode("node-2", map[v1.ResourceName]string{fooResource: "1", barResource: "1"})),
				newFakeNodeInfo(makeResourceNode("node-3", map[v1.ResourceName]string{barResource: "1"})),
			},
			expectedList: framework.NodeScoreList{{"node-0", 0}, {"node-1", 25}, {"node-2", 100}, {"node-3", 75}},
		},
		{
			name: "pod require resources (foo: 1, bar: 0)",
			pod:  makeResourcePod(map[v1.ResourceName]string{fooResource: "1", barResource: "0"}),
			nodes: []framework.NodeInfo{
				newFakeNodeInfo(makeResourceNode("node-0", map[v1.ResourceName]string{})),
				newFakeNodeInfo(makeResourceNode("node-1", map[v1.ResourceName]string{fooResource: "1"})),
				newFakeNodeInfo(makeResourceNode("node-2", map[v1.ResourceName]string{fooResource: "1", barResource: "1"})),
				newFakeNodeInfo(makeResourceNode("node-3", map[v1.ResourceName]string{barResource: "1"})),
			},
			expectedList: framework.NodeScoreList{{"node-0", 75}, {"node-1", 100}, {"node-2", 25}, {"node-3", 0}},
		},
		{
			name: "pod require resources (foo: 0, bar: 1)",
			pod:  makeResourcePod(map[v1.ResourceName]string{fooResource: "0", barResource: "1"}),
			nodes: []framework.NodeInfo{
				newFakeNodeInfo(makeResourceNode("node-0", map[v1.ResourceName]string{})),
				newFakeNodeInfo(makeResourceNode("node-1", map[v1.ResourceName]string{fooResource: "1"})),
				newFakeNodeInfo(makeResourceNode("node-2", map[v1.ResourceName]string{fooResource: "1", barResource: "1"})),
				newFakeNodeInfo(makeResourceNode("node-3", map[v1.ResourceName]string{barResource: "1"})),
			},
			expectedList: framework.NodeScoreList{{"node-0", 25}, {"node-1", 0}, {"node-2", 75}, {"node-3", 100}},
		},
		{
			name: "pod require resources (foo: 0, bar: 0)",
			pod:  makeResourcePod(map[v1.ResourceName]string{fooResource: "0", barResource: "0"}),
			nodes: []framework.NodeInfo{
				newFakeNodeInfo(makeResourceNode("node-0", map[v1.ResourceName]string{})),
				newFakeNodeInfo(makeResourceNode("node-1", map[v1.ResourceName]string{fooResource: "1"})),
				newFakeNodeInfo(makeResourceNode("node-2", map[v1.ResourceName]string{fooResource: "1", barResource: "1"})),
				newFakeNodeInfo(makeResourceNode("node-3", map[v1.ResourceName]string{barResource: "1"})),
			},
			expectedList: framework.NodeScoreList{{"node-0", 100}, {"node-1", 75}, {"node-2", 0}, {"node-3", 25}},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			nodes := make([]*v1.Node, len(test.nodes))
			for i := 0; i < len(test.nodes); i++ {
				nodes[i] = test.nodes[i].GetNode()
			}
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
				cache.AddNode(n.GetNode())
			}
			cache.UpdateSnapshot(snapshot)

			cycleState := framework.NewCycleState()
			framework.SetPodResourceTypeState(podutil.GuaranteedPod, cycleState)

			fh, _ := testutil.NewPodFrameworkHandle(nil, nil, nil, nil, nil, snapshot, nil, nil, nil, nil)
			pl, err := NewNodeResourcesAffinity(pluginArgs, fh)
			if err != nil {
				t.Fatalf("failed to new anti-affinity plugin, %v", err)
			}

			pl.(framework.PreScorePlugin).PreScore(context.Background(), cycleState, test.pod, test.nodes)
			var scoreList framework.NodeScoreList
			for _, node := range nodes {
				gotScore, status := pl.(framework.ScorePlugin).Score(context.Background(), cycleState, test.pod, node.Name)
				if status != nil {
					t.Errorf("unexpected error: %v", status)
				}
				scoreList = append(scoreList, framework.NodeScore{Name: node.Name, Score: gotScore})
			}

			status := pl.(framework.ScoreExtensions).NormalizeScore(context.Background(), cycleState, test.pod, scoreList)
			if status != nil {
				t.Errorf("unexpected error: %v", status)
			}

			for i, targetScore := range test.expectedList {
				if scoreList[i].Name != targetScore.Name || scoreList[i].Score != targetScore.Score {
					t.Errorf("expect score list: %v, but got: %v", test.expectedList, scoreList)
				}
			}
		})
	}
}
