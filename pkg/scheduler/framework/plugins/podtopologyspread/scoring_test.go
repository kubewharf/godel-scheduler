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

package podtopologyspread

import (
	"context"
	"testing"

	"github.com/google/go-cmp/cmp"
	nodev1alpha1 "github.com/kubewharf/godel-scheduler-api/pkg/apis/node/v1alpha1"
	framework "github.com/kubewharf/godel-scheduler/pkg/framework/api"
	utils "github.com/kubewharf/godel-scheduler/pkg/plugins/podtopologyspread"
	"github.com/kubewharf/godel-scheduler/pkg/scheduler/apis/config"
	testing_helper "github.com/kubewharf/godel-scheduler/pkg/testing-helper"
	framework_helper "github.com/kubewharf/godel-scheduler/pkg/testing-helper/framework-helper"
	"github.com/kubewharf/godel-scheduler/pkg/util/parallelize"
	podutil "github.com/kubewharf/godel-scheduler/pkg/util/pod"
	appsv1 "k8s.io/api/apps/v1"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes/fake"
	"k8s.io/utils/pointer"
)

func TestPreScoreStateEmptyNodes(t *testing.T) {
	tests := []struct {
		name               string
		pod                *v1.Pod
		nodes              []*v1.Node
		objs               []runtime.Object
		defaultConstraints []v1.TopologySpreadConstraint
		want               *preScoreState
	}{
		{
			name: "normal case",
			pod: testing_helper.MakePod().Name("p").Label("foo", "").
				SpreadConstraint(1, "zone", v1.ScheduleAnyway, testing_helper.MakeLabelSelector().Exists("foo").Obj()).
				SpreadConstraint(1, v1.LabelHostname, v1.ScheduleAnyway, testing_helper.MakeLabelSelector().Exists("foo").Obj()).
				Obj(),
			nodes: []*v1.Node{
				testing_helper.MakeNode().Name("node-a").Label("zone", "zone1").Label(v1.LabelHostname, "node-a").Obj(),
				testing_helper.MakeNode().Name("node-b").Label("zone", "zone1").Label(v1.LabelHostname, "node-b").Obj(),
				testing_helper.MakeNode().Name("node-x").Label("zone", "zone2").Label(v1.LabelHostname, "node-x").Obj(),
			},
			want: &preScoreState{
				Constraints: []utils.TopologySpreadConstraint{
					{
						MaxSkew:     1,
						TopologyKey: "zone",
						Selector:    mustConvertLabelSelectorAsSelector(t, testing_helper.MakeLabelSelector().Exists("foo").Obj()),
					},
					{
						MaxSkew:     1,
						TopologyKey: v1.LabelHostname,
						Selector:    mustConvertLabelSelectorAsSelector(t, testing_helper.MakeLabelSelector().Exists("foo").Obj()),
					},
				},
				IgnoredNodes: sets.NewString(),
				TopologyPairToPodCounts: map[utils.TopologyPair]*int64{
					{Key: "zone", Value: "zone1"}: pointer.Int64Ptr(0),
					{Key: "zone", Value: "zone2"}: pointer.Int64Ptr(0),
				},
				TopologyNormalizingWeight: []float64{topologyNormalizingWeight(2), topologyNormalizingWeight(3)},
			},
		},
		{
			name: "node-x doesn't have label zone",
			pod: testing_helper.MakePod().Name("p").Label("foo", "").
				SpreadConstraint(1, "zone", v1.ScheduleAnyway, testing_helper.MakeLabelSelector().Exists("foo").Obj()).
				SpreadConstraint(1, v1.LabelHostname, v1.ScheduleAnyway, testing_helper.MakeLabelSelector().Exists("bar").Obj()).
				Obj(),
			nodes: []*v1.Node{
				testing_helper.MakeNode().Name("node-a").Label("zone", "zone1").Label(v1.LabelHostname, "node-a").Obj(),
				testing_helper.MakeNode().Name("node-b").Label("zone", "zone1").Label(v1.LabelHostname, "node-b").Obj(),
				testing_helper.MakeNode().Name("node-x").Label(v1.LabelHostname, "node-x").Obj(),
			},
			want: &preScoreState{
				Constraints: []utils.TopologySpreadConstraint{
					{
						MaxSkew:     1,
						TopologyKey: "zone",
						Selector:    mustConvertLabelSelectorAsSelector(t, testing_helper.MakeLabelSelector().Exists("foo").Obj()),
					},
					{
						MaxSkew:     1,
						TopologyKey: v1.LabelHostname,
						Selector:    mustConvertLabelSelectorAsSelector(t, testing_helper.MakeLabelSelector().Exists("bar").Obj()),
					},
				},
				IgnoredNodes: sets.NewString("node-x"),
				TopologyPairToPodCounts: map[utils.TopologyPair]*int64{
					{Key: "zone", Value: "zone1"}: pointer.Int64Ptr(0),
				},
				TopologyNormalizingWeight: []float64{topologyNormalizingWeight(1), topologyNormalizingWeight(2)},
			},
		},
		{
			name: "defaults constraints and a replica set",
			pod:  testing_helper.MakePod().Name("p").Label("foo", "tar").Label("baz", "sup").Obj(),
			defaultConstraints: []v1.TopologySpreadConstraint{
				{MaxSkew: 1, TopologyKey: v1.LabelHostname, WhenUnsatisfiable: v1.ScheduleAnyway},
				{MaxSkew: 2, TopologyKey: "rack", WhenUnsatisfiable: v1.DoNotSchedule},
				{MaxSkew: 2, TopologyKey: "planet", WhenUnsatisfiable: v1.ScheduleAnyway},
			},
			nodes: []*v1.Node{
				testing_helper.MakeNode().Name("node-a").Label("rack", "rack1").Label(v1.LabelHostname, "node-a").Label("planet", "mars").Obj(),
			},
			objs: []runtime.Object{
				&appsv1.ReplicaSet{Spec: appsv1.ReplicaSetSpec{Selector: testing_helper.MakeLabelSelector().Exists("foo").Obj()}},
			},
			want: &preScoreState{
				Constraints: []utils.TopologySpreadConstraint{
					{
						MaxSkew:     1,
						TopologyKey: v1.LabelHostname,
						Selector:    mustConvertLabelSelectorAsSelector(t, testing_helper.MakeLabelSelector().Exists("foo").Obj()),
					},
					{
						MaxSkew:     2,
						TopologyKey: "planet",
						Selector:    mustConvertLabelSelectorAsSelector(t, testing_helper.MakeLabelSelector().Exists("foo").Obj()),
					},
				},
				IgnoredNodes: sets.NewString(),
				TopologyPairToPodCounts: map[utils.TopologyPair]*int64{
					{Key: "planet", Value: "mars"}: pointer.Int64Ptr(0),
				},
				TopologyNormalizingWeight: []float64{topologyNormalizingWeight(1), topologyNormalizingWeight(1)},
			},
		},
		{
			name: "defaults constraints and a replica set that doesn't match",
			pod:  testing_helper.MakePod().Name("p").Label("foo", "bar").Label("baz", "sup").Obj(),
			defaultConstraints: []v1.TopologySpreadConstraint{
				{MaxSkew: 2, TopologyKey: "planet", WhenUnsatisfiable: v1.ScheduleAnyway},
			},
			nodes: []*v1.Node{
				testing_helper.MakeNode().Name("node-a").Label("planet", "mars").Obj(),
			},
			objs: []runtime.Object{
				&appsv1.ReplicaSet{Spec: appsv1.ReplicaSetSpec{Selector: testing_helper.MakeLabelSelector().Exists("tar").Obj()}},
			},
			want: &preScoreState{
				TopologyPairToPodCounts: make(map[utils.TopologyPair]*int64),
			},
		},
		{
			name: "defaults constraints and a replica set, but pod has constraints",
			pod: testing_helper.MakePod().Name("p").Label("foo", "bar").Label("baz", "sup").
				SpreadConstraint(1, "zone", v1.DoNotSchedule, testing_helper.MakeLabelSelector().Label("foo", "bar").Obj()).
				SpreadConstraint(2, "planet", v1.ScheduleAnyway, testing_helper.MakeLabelSelector().Label("baz", "sup").Obj()).Obj(),
			defaultConstraints: []v1.TopologySpreadConstraint{
				{MaxSkew: 2, TopologyKey: "galaxy", WhenUnsatisfiable: v1.ScheduleAnyway},
			},
			nodes: []*v1.Node{
				testing_helper.MakeNode().Name("node-a").Label("planet", "mars").Label("galaxy", "andromeda").Obj(),
			},
			objs: []runtime.Object{
				&appsv1.ReplicaSet{Spec: appsv1.ReplicaSetSpec{Selector: testing_helper.MakeLabelSelector().Exists("foo").Obj()}},
			},
			want: &preScoreState{
				Constraints: []utils.TopologySpreadConstraint{
					{
						MaxSkew:     2,
						TopologyKey: "planet",
						Selector:    mustConvertLabelSelectorAsSelector(t, testing_helper.MakeLabelSelector().Label("baz", "sup").Obj()),
					},
				},
				IgnoredNodes: sets.NewString(),
				TopologyPairToPodCounts: map[utils.TopologyPair]*int64{
					{"planet", "mars"}: pointer.Int64Ptr(0),
				},
				TopologyNormalizingWeight: []float64{topologyNormalizingWeight(1)},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			informerFactory := informers.NewSharedInformerFactory(fake.NewSimpleClientset(tt.objs...), 0)
			snapshot := framework_helper.MakeSnapShot(nil, tt.nodes, nil)

			pl := PodTopologySpread{
				sharedLister: snapshot,
				args: config.PodTopologySpreadArgs{
					DefaultConstraints: tt.defaultConstraints,
				},
			}
			pl.setListers(informerFactory)
			informerFactory.Start(ctx.Done())
			informerFactory.WaitForCacheSync(ctx.Done())
			cs := framework.NewCycleState()
			nodeInfos := make([]framework.NodeInfo, len(tt.nodes))
			for index, node := range tt.nodes {
				nodeInfos[index] = framework_helper.WithNode(node)
			}

			if s := pl.PreScore(context.Background(), cs, tt.pod, nodeInfos); !s.IsSuccess() {
				t.Fatal(s.AsError())
			}

			got, err := getPreScoreState(cs)
			if err != nil {
				t.Fatal(err)
			}
			if diff := cmp.Diff(tt.want, got, cmpOpts...); diff != "" {
				t.Errorf("PodTopologySpread#PreScore() returned (-want, +got):\n%s", diff)
			}
		})
	}
}

func TestPodTopologySpreadScore(t *testing.T) {
	tests := []struct {
		name         string
		pod          *v1.Pod
		existingPods []*v1.Pod
		nodes        []*v1.Node
		failedNodes  []*v1.Node // nodes + failedNodes = all nodes
		want         framework.NodeScoreList
	}{
		// Explanation on the Legend:
		// a) X/Y means there are X matching pods on node1 and Y on node2, both nodes are candidates
		//   (i.e. they have passed all predicates)
		// b) X/~Y~ means there are X matching pods on node1 and Y on node2, but node Y is NOT a candidate
		// c) X/?Y? means there are X matching pods on node1 and Y on node2, both nodes are candidates
		//    but node2 either i) doesn't have all required topologyKeys present, or ii) doesn't match
		//    incoming pod's nodeSelector/nodeAffinity
		{
			name: "one constraint on node, no existing pods",
			pod: testing_helper.MakePod().Name("p").Label("foo", "").
				SpreadConstraint(1, v1.LabelHostname, v1.ScheduleAnyway, testing_helper.MakeLabelSelector().Exists("foo").Obj()).
				Obj(),
			nodes: []*v1.Node{
				testing_helper.MakeNode().Name("node-a").Label(v1.LabelHostname, "node-a").Obj(),
				testing_helper.MakeNode().Name("node-b").Label(v1.LabelHostname, "node-b").Obj(),
			},
			want: []framework.NodeScore{
				{Name: "node-a", Score: 100},
				{Name: "node-b", Score: 100},
			},
		},
		{
			// if there is only one candidate node, it should be scored to 100
			name: "one constraint on node, only one node is candidate",
			pod: testing_helper.MakePod().Name("p").Label("foo", "").
				SpreadConstraint(1, v1.LabelHostname, v1.ScheduleAnyway, testing_helper.MakeLabelSelector().Exists("foo").Obj()).
				Obj(),
			existingPods: []*v1.Pod{
				testing_helper.MakePod().Name("p-a1").Node("node-a").Label("foo", "").Obj(),
				testing_helper.MakePod().Name("p-a2").Node("node-a").Label("foo", "").Obj(),
				testing_helper.MakePod().Name("p-b1").Node("node-b").Label("foo", "").Obj(),
			},
			nodes: []*v1.Node{
				testing_helper.MakeNode().Name("node-a").Label(v1.LabelHostname, "node-a").Obj(),
			},
			failedNodes: []*v1.Node{
				testing_helper.MakeNode().Name("node-b").Label(v1.LabelHostname, "node-b").Obj(),
			},
			want: []framework.NodeScore{
				{Name: "node-a", Score: 100},
			},
		},
		{
			name: "one constraint on node, all nodes have the same number of matching pods",
			pod: testing_helper.MakePod().Name("p").Label("foo", "").
				SpreadConstraint(1, v1.LabelHostname, v1.ScheduleAnyway, testing_helper.MakeLabelSelector().Exists("foo").Obj()).
				Obj(),
			existingPods: []*v1.Pod{
				testing_helper.MakePod().Name("p-a1").Node("node-a").Label("foo", "").Obj(),
				testing_helper.MakePod().Name("p-b1").Node("node-b").Label("foo", "").Obj(),
			},
			nodes: []*v1.Node{
				testing_helper.MakeNode().Name("node-a").Label(v1.LabelHostname, "node-a").Obj(),
				testing_helper.MakeNode().Name("node-b").Label(v1.LabelHostname, "node-b").Obj(),
			},
			want: []framework.NodeScore{
				{Name: "node-a", Score: 100},
				{Name: "node-b", Score: 100},
			},
		},
		{
			// matching pods spread as 2/1/0/3.
			name: "one constraint on node, all 4 nodes are candidates",
			pod: testing_helper.MakePod().Name("p").Label("foo", "").
				SpreadConstraint(1, v1.LabelHostname, v1.ScheduleAnyway, testing_helper.MakeLabelSelector().Exists("foo").Obj()).
				Obj(),
			existingPods: []*v1.Pod{
				testing_helper.MakePod().Name("p-a1").Node("node-a").Label("foo", "").Obj(),
				testing_helper.MakePod().Name("p-a2").Node("node-a").Label("foo", "").Obj(),
				testing_helper.MakePod().Name("p-b1").Node("node-b").Label("foo", "").Obj(),
				testing_helper.MakePod().Name("p-d1").Node("node-d").Label("foo", "").Obj(),
				testing_helper.MakePod().Name("p-d2").Node("node-d").Label("foo", "").Obj(),
				testing_helper.MakePod().Name("p-d3").Node("node-d").Label("foo", "").Obj(),
			},
			nodes: []*v1.Node{
				testing_helper.MakeNode().Name("node-a").Label(v1.LabelHostname, "node-a").Obj(),
				testing_helper.MakeNode().Name("node-b").Label(v1.LabelHostname, "node-b").Obj(),
				testing_helper.MakeNode().Name("node-c").Label(v1.LabelHostname, "node-c").Obj(),
				testing_helper.MakeNode().Name("node-d").Label(v1.LabelHostname, "node-d").Obj(),
			},
			failedNodes: []*v1.Node{},
			want: []framework.NodeScore{
				{Name: "node-a", Score: 40},
				{Name: "node-b", Score: 80},
				{Name: "node-c", Score: 100},
				{Name: "node-d", Score: 0},
			},
		},
		{
			name: "one constraint on node, all 4 nodes are candidates, maxSkew=2",
			pod: testing_helper.MakePod().Name("p").Label("foo", "").
				SpreadConstraint(2, v1.LabelHostname, v1.ScheduleAnyway, testing_helper.MakeLabelSelector().Exists("foo").Obj()).
				Obj(),
			// matching pods spread as 2/1/0/3.
			existingPods: []*v1.Pod{
				testing_helper.MakePod().Name("p-a1").Node("node-a").Label("foo", "").Obj(),
				testing_helper.MakePod().Name("p-a2").Node("node-a").Label("foo", "").Obj(),
				testing_helper.MakePod().Name("p-b1").Node("node-b").Label("foo", "").Obj(),
				testing_helper.MakePod().Name("p-d1").Node("node-d").Label("foo", "").Obj(),
				testing_helper.MakePod().Name("p-d2").Node("node-d").Label("foo", "").Obj(),
				testing_helper.MakePod().Name("p-d3").Node("node-d").Label("foo", "").Obj(),
			},
			nodes: []*v1.Node{
				testing_helper.MakeNode().Name("node-a").Label(v1.LabelHostname, "node-a").Obj(),
				testing_helper.MakeNode().Name("node-b").Label(v1.LabelHostname, "node-b").Obj(),
				testing_helper.MakeNode().Name("node-c").Label(v1.LabelHostname, "node-c").Obj(),
				testing_helper.MakeNode().Name("node-d").Label(v1.LabelHostname, "node-d").Obj(),
			},
			failedNodes: []*v1.Node{},
			want: []framework.NodeScore{
				{Name: "node-a", Score: 50}, // +10, compared to maxSkew=1
				{Name: "node-b", Score: 83}, // +3, compared to maxSkew=1
				{Name: "node-c", Score: 100},
				{Name: "node-d", Score: 16}, // +16, compared to maxSkew=1
			},
		},
		{
			name: "one constraint on node, all 4 nodes are candidates, maxSkew=3",
			pod: testing_helper.MakePod().Name("p").Label("foo", "").
				SpreadConstraint(3, v1.LabelHostname, v1.ScheduleAnyway, testing_helper.MakeLabelSelector().Exists("foo").Obj()).
				Obj(),
			existingPods: []*v1.Pod{
				// matching pods spread as 4/3/2/1.
				testing_helper.MakePod().Name("p-a1").Node("node-a").Label("foo", "").Obj(),
				testing_helper.MakePod().Name("p-a2").Node("node-a").Label("foo", "").Obj(),
				testing_helper.MakePod().Name("p-a3").Node("node-a").Label("foo", "").Obj(),
				testing_helper.MakePod().Name("p-a4").Node("node-a").Label("foo", "").Obj(),
				testing_helper.MakePod().Name("p-b1").Node("node-b").Label("foo", "").Obj(),
				testing_helper.MakePod().Name("p-b2").Node("node-b").Label("foo", "").Obj(),
				testing_helper.MakePod().Name("p-b3").Node("node-b").Label("foo", "").Obj(),
				testing_helper.MakePod().Name("p-c1").Node("node-c").Label("foo", "").Obj(),
				testing_helper.MakePod().Name("p-c2").Node("node-c").Label("foo", "").Obj(),
				testing_helper.MakePod().Name("p-d1").Node("node-d").Label("foo", "").Obj(),
			},
			nodes: []*v1.Node{
				testing_helper.MakeNode().Name("node-a").Label(v1.LabelHostname, "node-a").Obj(),
				testing_helper.MakeNode().Name("node-b").Label(v1.LabelHostname, "node-b").Obj(),
				testing_helper.MakeNode().Name("node-c").Label(v1.LabelHostname, "node-c").Obj(),
				testing_helper.MakeNode().Name("node-d").Label(v1.LabelHostname, "node-d").Obj(),
			},
			failedNodes: []*v1.Node{},
			want: []framework.NodeScore{
				{Name: "node-a", Score: 33}, // +19 compared to maxSkew=1
				{Name: "node-b", Score: 55}, // +13 compared to maxSkew=1
				{Name: "node-c", Score: 77}, // +6 compared to maxSkew=1
				{Name: "node-d", Score: 100},
			},
		},
		{
			// matching pods spread as 4/2/1/~3~ (node4 is not a candidate)
			name: "one constraint on node, 3 out of 4 nodes are candidates",
			pod: testing_helper.MakePod().Name("p").Label("foo", "").
				SpreadConstraint(1, v1.LabelHostname, v1.ScheduleAnyway, testing_helper.MakeLabelSelector().Exists("foo").Obj()).
				Obj(),
			existingPods: []*v1.Pod{
				testing_helper.MakePod().Name("p-a1").Node("node-a").Label("foo", "").Obj(),
				testing_helper.MakePod().Name("p-a2").Node("node-a").Label("foo", "").Obj(),
				testing_helper.MakePod().Name("p-a3").Node("node-a").Label("foo", "").Obj(),
				testing_helper.MakePod().Name("p-a4").Node("node-a").Label("foo", "").Obj(),
				testing_helper.MakePod().Name("p-b1").Node("node-b").Label("foo", "").Obj(),
				testing_helper.MakePod().Name("p-b2").Node("node-b").Label("foo", "").Obj(),
				testing_helper.MakePod().Name("p-x1").Node("node-x").Label("foo", "").Obj(),
				testing_helper.MakePod().Name("p-y1").Node("node-y").Label("foo", "").Obj(),
				testing_helper.MakePod().Name("p-y2").Node("node-y").Label("foo", "").Obj(),
				testing_helper.MakePod().Name("p-y3").Node("node-y").Label("foo", "").Obj(),
			},
			nodes: []*v1.Node{
				testing_helper.MakeNode().Name("node-a").Label(v1.LabelHostname, "node-a").Obj(),
				testing_helper.MakeNode().Name("node-b").Label(v1.LabelHostname, "node-b").Obj(),
				testing_helper.MakeNode().Name("node-x").Label(v1.LabelHostname, "node-x").Obj(),
			},
			failedNodes: []*v1.Node{
				testing_helper.MakeNode().Name("node-y").Label(v1.LabelHostname, "node-y").Obj(),
			},
			want: []framework.NodeScore{
				{Name: "node-a", Score: 16},
				{Name: "node-b", Score: 66},
				{Name: "node-x", Score: 100},
			},
		},
		{
			// matching pods spread as 4/?2?/1/~3~, total = 4+?+1 = 5 (as node2 is problematic)
			name: "one constraint on node, 3 out of 4 nodes are candidates, one node doesn't match topology key",
			pod: testing_helper.MakePod().Name("p").Label("foo", "").
				SpreadConstraint(1, v1.LabelHostname, v1.ScheduleAnyway, testing_helper.MakeLabelSelector().Exists("foo").Obj()).
				Obj(),
			existingPods: []*v1.Pod{
				testing_helper.MakePod().Name("p-a1").Node("node-a").Label("foo", "").Obj(),
				testing_helper.MakePod().Name("p-a2").Node("node-a").Label("foo", "").Obj(),
				testing_helper.MakePod().Name("p-a3").Node("node-a").Label("foo", "").Obj(),
				testing_helper.MakePod().Name("p-a4").Node("node-a").Label("foo", "").Obj(),
				testing_helper.MakePod().Name("p-b1").Node("node-b").Label("foo", "").Obj(),
				testing_helper.MakePod().Name("p-b2").Node("node-b").Label("foo", "").Obj(),
				testing_helper.MakePod().Name("p-x1").Node("node-x").Label("foo", "").Obj(),
				testing_helper.MakePod().Name("p-y1").Node("node-y").Label("foo", "").Obj(),
				testing_helper.MakePod().Name("p-y2").Node("node-y").Label("foo", "").Obj(),
				testing_helper.MakePod().Name("p-y3").Node("node-y").Label("foo", "").Obj(),
			},
			nodes: []*v1.Node{
				testing_helper.MakeNode().Name("node-a").Label(v1.LabelHostname, "node-a").Obj(),
				testing_helper.MakeNode().Name("node-b").Label("n", "node-b").Obj(), // label `n` doesn't match topologyKey
				testing_helper.MakeNode().Name("node-x").Label(v1.LabelHostname, "node-x").Obj(),
			},
			failedNodes: []*v1.Node{
				testing_helper.MakeNode().Name("node-y").Label(v1.LabelHostname, "node-y").Obj(),
			},
			want: []framework.NodeScore{
				{Name: "node-a", Score: 20},
				{Name: "node-b", Score: 0},
				{Name: "node-x", Score: 100},
			},
		},
		{
			// matching pods spread as 4/2/1/~3~
			name: "one constraint on zone, 3 out of 4 nodes are candidates",
			pod: testing_helper.MakePod().Name("p").Label("foo", "").
				SpreadConstraint(1, "zone", v1.ScheduleAnyway, testing_helper.MakeLabelSelector().Exists("foo").Obj()).
				Obj(),
			existingPods: []*v1.Pod{
				testing_helper.MakePod().Name("p-a1").Node("node-a").Label("foo", "").Obj(),
				testing_helper.MakePod().Name("p-a2").Node("node-a").Label("foo", "").Obj(),
				testing_helper.MakePod().Name("p-a3").Node("node-a").Label("foo", "").Obj(),
				testing_helper.MakePod().Name("p-a4").Node("node-a").Label("foo", "").Obj(),
				testing_helper.MakePod().Name("p-b1").Node("node-b").Label("foo", "").Obj(),
				testing_helper.MakePod().Name("p-b2").Node("node-b").Label("foo", "").Obj(),
				testing_helper.MakePod().Name("p-x1").Node("node-x").Label("foo", "").Obj(),
				testing_helper.MakePod().Name("p-y1").Node("node-y").Label("foo", "").Obj(),
				testing_helper.MakePod().Name("p-y2").Node("node-y").Label("foo", "").Obj(),
				testing_helper.MakePod().Name("p-y3").Node("node-y").Label("foo", "").Obj(),
			},
			nodes: []*v1.Node{
				testing_helper.MakeNode().Name("node-a").Label("zone", "zone1").Label(v1.LabelHostname, "node-a").Obj(),
				testing_helper.MakeNode().Name("node-b").Label("zone", "zone1").Label(v1.LabelHostname, "node-b").Obj(),
				testing_helper.MakeNode().Name("node-x").Label("zone", "zone2").Label(v1.LabelHostname, "node-x").Obj(),
			},
			failedNodes: []*v1.Node{
				testing_helper.MakeNode().Name("node-y").Label("zone", "zone2").Label(v1.LabelHostname, "node-y").Obj(),
			},
			want: []framework.NodeScore{
				{Name: "node-a", Score: 62},
				{Name: "node-b", Score: 62},
				{Name: "node-x", Score: 100},
			},
		},
		{
			// matching pods spread as 2/~1~/2/~4~.
			name: "two Constraints on zone and node, 2 out of 4 nodes are candidates",
			pod: testing_helper.MakePod().Name("p").Label("foo", "").
				SpreadConstraint(1, "zone", v1.ScheduleAnyway, testing_helper.MakeLabelSelector().Exists("foo").Obj()).
				SpreadConstraint(1, v1.LabelHostname, v1.ScheduleAnyway, testing_helper.MakeLabelSelector().Exists("foo").Obj()).
				Obj(),
			existingPods: []*v1.Pod{
				testing_helper.MakePod().Name("p-a1").Node("node-a").Label("foo", "").Obj(),
				testing_helper.MakePod().Name("p-a2").Node("node-a").Label("foo", "").Obj(),
				testing_helper.MakePod().Name("p-b1").Node("node-b").Label("foo", "").Obj(),
				testing_helper.MakePod().Name("p-x1").Node("node-x").Label("foo", "").Obj(),
				testing_helper.MakePod().Name("p-x2").Node("node-x").Label("foo", "").Obj(),
				testing_helper.MakePod().Name("p-y1").Node("node-y").Label("foo", "").Obj(),
				testing_helper.MakePod().Name("p-y2").Node("node-y").Label("foo", "").Obj(),
				testing_helper.MakePod().Name("p-y3").Node("node-y").Label("foo", "").Obj(),
				testing_helper.MakePod().Name("p-y4").Node("node-y").Label("foo", "").Obj(),
			},
			nodes: []*v1.Node{
				testing_helper.MakeNode().Name("node-a").Label("zone", "zone1").Label(v1.LabelHostname, "node-a").Obj(),
				testing_helper.MakeNode().Name("node-x").Label("zone", "zone2").Label(v1.LabelHostname, "node-x").Obj(),
			},
			failedNodes: []*v1.Node{
				testing_helper.MakeNode().Name("node-b").Label("zone", "zone1").Label(v1.LabelHostname, "node-b").Obj(),
				testing_helper.MakeNode().Name("node-y").Label("zone", "zone2").Label(v1.LabelHostname, "node-y").Obj(),
			},
			want: []framework.NodeScore{
				{Name: "node-a", Score: 100},
				{Name: "node-x", Score: 54},
			},
		},
		{
			// If Constraints hold different labelSelectors, it's a little complex.
			// +----------------------+------------------------+
			// |         zone1        |          zone2         |
			// +----------------------+------------------------+
			// | node-a |    node-b   | node-x |     node-y    |
			// +--------+-------------+--------+---------------+
			// | P{foo} | P{foo, bar} |        | P{foo} P{bar} |
			// +--------+-------------+--------+---------------+
			// For the first constraint (zone): the matching pods spread as 2/2/1/1
			// For the second constraint (node): the matching pods spread as 0/1/0/1
			name: "two Constraints on zone and node, with different labelSelectors",
			pod: testing_helper.MakePod().Name("p").Label("foo", "").Label("bar", "").
				SpreadConstraint(1, "zone", v1.ScheduleAnyway, testing_helper.MakeLabelSelector().Exists("foo").Obj()).
				SpreadConstraint(1, v1.LabelHostname, v1.ScheduleAnyway, testing_helper.MakeLabelSelector().Exists("bar").Obj()).
				Obj(),
			existingPods: []*v1.Pod{
				testing_helper.MakePod().Name("p-a1").Node("node-a").Label("foo", "").Obj(),
				testing_helper.MakePod().Name("p-b1").Node("node-b").Label("foo", "").Label("bar", "").Obj(),
				testing_helper.MakePod().Name("p-y1").Node("node-y").Label("foo", "").Obj(),
				testing_helper.MakePod().Name("p-y2").Node("node-y").Label("bar", "").Obj(),
			},
			nodes: []*v1.Node{
				testing_helper.MakeNode().Name("node-a").Label("zone", "zone1").Label(v1.LabelHostname, "node-a").Obj(),
				testing_helper.MakeNode().Name("node-b").Label("zone", "zone1").Label(v1.LabelHostname, "node-b").Obj(),
				testing_helper.MakeNode().Name("node-x").Label("zone", "zone2").Label(v1.LabelHostname, "node-x").Obj(),
				testing_helper.MakeNode().Name("node-y").Label("zone", "zone2").Label(v1.LabelHostname, "node-y").Obj(),
			},
			failedNodes: []*v1.Node{},
			want: []framework.NodeScore{
				{Name: "node-a", Score: 75},
				{Name: "node-b", Score: 25},
				{Name: "node-x", Score: 100},
				{Name: "node-y", Score: 50},
			},
		},
		{
			// For the first constraint (zone): the matching pods spread as 0/0/2/2
			// For the second constraint (node): the matching pods spread as 0/1/0/1
			name: "two Constraints on zone and node, with different labelSelectors, some nodes have 0 pods",
			pod: testing_helper.MakePod().Name("p").Label("foo", "").Label("bar", "").
				SpreadConstraint(1, "zone", v1.ScheduleAnyway, testing_helper.MakeLabelSelector().Exists("foo").Obj()).
				SpreadConstraint(1, v1.LabelHostname, v1.ScheduleAnyway, testing_helper.MakeLabelSelector().Exists("bar").Obj()).
				Obj(),
			existingPods: []*v1.Pod{
				testing_helper.MakePod().Name("p-b1").Node("node-b").Label("bar", "").Obj(),
				testing_helper.MakePod().Name("p-x1").Node("node-x").Label("foo", "").Obj(),
				testing_helper.MakePod().Name("p-y1").Node("node-y").Label("foo", "").Label("bar", "").Obj(),
			},
			nodes: []*v1.Node{
				testing_helper.MakeNode().Name("node-a").Label("zone", "zone1").Label(v1.LabelHostname, "node-a").Obj(),
				testing_helper.MakeNode().Name("node-b").Label("zone", "zone1").Label(v1.LabelHostname, "node-b").Obj(),
				testing_helper.MakeNode().Name("node-x").Label("zone", "zone2").Label(v1.LabelHostname, "node-x").Obj(),
				testing_helper.MakeNode().Name("node-y").Label("zone", "zone2").Label(v1.LabelHostname, "node-y").Obj(),
			},
			failedNodes: []*v1.Node{},
			want: []framework.NodeScore{
				{Name: "node-a", Score: 100},
				{Name: "node-b", Score: 75},
				{Name: "node-x", Score: 50},
				{Name: "node-y", Score: 0},
			},
		},
		{
			// For the first constraint (zone): the matching pods spread as 2/2/1/~1~
			// For the second constraint (node): the matching pods spread as 0/1/0/~1~
			name: "two Constraints on zone and node, with different labelSelectors, 3 out of 4 nodes are candidates",
			pod: testing_helper.MakePod().Name("p").Label("foo", "").Label("bar", "").
				SpreadConstraint(1, "zone", v1.ScheduleAnyway, testing_helper.MakeLabelSelector().Exists("foo").Obj()).
				SpreadConstraint(1, v1.LabelHostname, v1.ScheduleAnyway, testing_helper.MakeLabelSelector().Exists("bar").Obj()).
				Obj(),
			existingPods: []*v1.Pod{
				testing_helper.MakePod().Name("p-a1").Node("node-a").Label("foo", "").Obj(),
				testing_helper.MakePod().Name("p-b1").Node("node-b").Label("foo", "").Label("bar", "").Obj(),
				testing_helper.MakePod().Name("p-y1").Node("node-y").Label("foo", "").Obj(),
				testing_helper.MakePod().Name("p-y2").Node("node-y").Label("bar", "").Obj(),
			},
			nodes: []*v1.Node{
				testing_helper.MakeNode().Name("node-a").Label("zone", "zone1").Label(v1.LabelHostname, "node-a").Obj(),
				testing_helper.MakeNode().Name("node-b").Label("zone", "zone1").Label(v1.LabelHostname, "node-b").Obj(),
				testing_helper.MakeNode().Name("node-x").Label("zone", "zone2").Label(v1.LabelHostname, "node-x").Obj(),
			},
			failedNodes: []*v1.Node{
				testing_helper.MakeNode().Name("node-y").Label("zone", "zone2").Label(v1.LabelHostname, "node-y").Obj(),
			},
			want: []framework.NodeScore{
				{Name: "node-a", Score: 75},
				{Name: "node-b", Score: 25},
				{Name: "node-x", Score: 100},
			},
		},
		{
			name: "existing pods in a different namespace do not count",
			pod: testing_helper.MakePod().Name("p").Label("foo", "").
				SpreadConstraint(1, v1.LabelHostname, v1.ScheduleAnyway, testing_helper.MakeLabelSelector().Exists("foo").Obj()).
				Obj(),
			existingPods: []*v1.Pod{
				testing_helper.MakePod().Name("p-a1").Namespace("ns1").Node("node-a").Label("foo", "").Obj(),
				testing_helper.MakePod().Name("p-a2").Node("node-a").Label("foo", "").Obj(),
				testing_helper.MakePod().Name("p-b1").Node("node-b").Label("foo", "").Obj(),
				testing_helper.MakePod().Name("p-b2").Node("node-b").Label("foo", "").Obj(),
			},
			nodes: []*v1.Node{
				testing_helper.MakeNode().Name("node-a").Label(v1.LabelHostname, "node-a").Obj(),
				testing_helper.MakeNode().Name("node-b").Label(v1.LabelHostname, "node-b").Obj(),
			},
			want: []framework.NodeScore{
				{Name: "node-a", Score: 100},
				{Name: "node-b", Score: 50},
			},
		},
		{
			name: "terminating Pods should be excluded",
			pod: testing_helper.MakePod().Name("p").Label("foo", "").SpreadConstraint(
				1, v1.LabelHostname, v1.ScheduleAnyway, testing_helper.MakeLabelSelector().Exists("foo").Obj(),
			).Obj(),
			nodes: []*v1.Node{
				testing_helper.MakeNode().Name("node-a").Label(v1.LabelHostname, "node-a").Obj(),
				testing_helper.MakeNode().Name("node-b").Label(v1.LabelHostname, "node-b").Obj(),
			},
			existingPods: []*v1.Pod{
				testing_helper.MakePod().Name("p-a").Node("node-a").Label("foo", "").Terminating().Obj(),
				testing_helper.MakePod().Name("p-b").Node("node-b").Label("foo", "").Obj(),
			},
			want: []framework.NodeScore{
				{Name: "node-a", Score: 100},
				{Name: "node-b", Score: 0},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			allNodes := append([]*v1.Node{}, tt.nodes...)
			allNodes = append(allNodes, tt.failedNodes...)
			state := framework.NewCycleState()

			snapshot := framework_helper.MakeSnapShot(tt.existingPods, allNodes, nil)

			p := &PodTopologySpread{sharedLister: snapshot}

			nodeInfos := make([]framework.NodeInfo, len(tt.nodes))
			for index, node := range tt.nodes {
				nodeInfos[index] = framework_helper.WithNode(node)
			}

			status := p.PreScore(context.Background(), state, tt.pod, nodeInfos)
			if !status.IsSuccess() {
				t.Errorf("unexpected error: %v", status)
			}

			var gotList framework.NodeScoreList
			for _, n := range tt.nodes {
				nodeName := n.Name
				score, status := p.Score(context.Background(), state, tt.pod, nodeName)
				if !status.IsSuccess() {
					t.Errorf("unexpected error: %v", status)
				}
				gotList = append(gotList, framework.NodeScore{Name: nodeName, Score: score})
			}

			status = p.NormalizeScore(context.Background(), state, tt.pod, gotList)
			if !status.IsSuccess() {
				t.Errorf("unexpected error: %v", status)
			}
			if diff := cmp.Diff(tt.want, gotList, cmpOpts...); diff != "" {
				t.Errorf("unexpected scores (-want,+got):\n%s", diff)
			}
		})
	}
}

func TestNMNodesScore(t *testing.T) {
	tests := []struct {
		name         string
		pod          *v1.Pod
		existingPods []*v1.Pod
		nodes        []*v1.Node
		nmNodes      []*nodev1alpha1.NMNode
		want         framework.NodeScoreList
	}{
		{
			// matching pods spread as 2/1/0/3.
			name: "All nodes are of NMNode type, that is, they are managed by the node manager. One constraint on node, all 4 nodes are candidates",
			pod: testing_helper.MakePod().Name("p").Label("foo", "").
				SpreadConstraint(1, v1.LabelHostname, v1.ScheduleAnyway, testing_helper.MakeLabelSelector().Exists("foo").Obj()).
				Obj(),
			existingPods: []*v1.Pod{
				testing_helper.MakePod().Name("p-a1").Node("node-a").Label("foo", "").Annotation(podutil.PodLauncherAnnotationKey, string(podutil.NodeManager)).Obj(),
				testing_helper.MakePod().Name("p-a2").Node("node-a").Label("foo", "").Annotation(podutil.PodLauncherAnnotationKey, string(podutil.NodeManager)).Obj(),
				testing_helper.MakePod().Name("p-b1").Node("node-b").Label("foo", "").Annotation(podutil.PodLauncherAnnotationKey, string(podutil.NodeManager)).Obj(),
				testing_helper.MakePod().Name("p-d1").Node("node-d").Label("foo", "").Annotation(podutil.PodLauncherAnnotationKey, string(podutil.NodeManager)).Obj(),
				testing_helper.MakePod().Name("p-d2").Node("node-d").Label("foo", "").Annotation(podutil.PodLauncherAnnotationKey, string(podutil.NodeManager)).Obj(),
				testing_helper.MakePod().Name("p-d3").Node("node-d").Label("foo", "").Annotation(podutil.PodLauncherAnnotationKey, string(podutil.NodeManager)).Obj(),
			},
			nmNodes: []*nodev1alpha1.NMNode{
				{ObjectMeta: metav1.ObjectMeta{Name: "node-a", Labels: map[string]string{v1.LabelHostname: "node-a"}}},
				{ObjectMeta: metav1.ObjectMeta{Name: "node-b", Labels: map[string]string{v1.LabelHostname: "node-b"}}},
				{ObjectMeta: metav1.ObjectMeta{Name: "node-c", Labels: map[string]string{v1.LabelHostname: "node-c"}}},
				{ObjectMeta: metav1.ObjectMeta{Name: "node-d", Labels: map[string]string{v1.LabelHostname: "node-d"}}},
			},
			want: []framework.NodeScore{
				{Name: "node-a", Score: 40},
				{Name: "node-b", Score: 80},
				{Name: "node-c", Score: 100},
				{Name: "node-d", Score: 0},
			},
		},
		{
			// matching pods spread as 2/1/0/3.
			name: "The first node-a is v1.node and the others are of NMNode type. Since node-a is of v1.Node type, it does not meet the requirements of the pod to be scheduled, so the score is 0.",
			pod: testing_helper.MakePod().Name("p").Label("foo", "").
				SpreadConstraint(1, v1.LabelHostname, v1.ScheduleAnyway, testing_helper.MakeLabelSelector().Exists("foo").Obj()).
				Obj(),
			existingPods: []*v1.Pod{
				testing_helper.MakePod().Name("p-a1").Node("node-a").Label("foo", "").Obj(),
				testing_helper.MakePod().Name("p-a2").Node("node-a").Label("foo", "").Obj(),
				testing_helper.MakePod().Name("p-b1").Node("node-b").Label("foo", "").Annotation(podutil.PodLauncherAnnotationKey, string(podutil.NodeManager)).Obj(),
				testing_helper.MakePod().Name("p-d1").Node("node-d").Label("foo", "").Annotation(podutil.PodLauncherAnnotationKey, string(podutil.NodeManager)).Obj(),
				testing_helper.MakePod().Name("p-d2").Node("node-d").Label("foo", "").Annotation(podutil.PodLauncherAnnotationKey, string(podutil.NodeManager)).Obj(),
				testing_helper.MakePod().Name("p-d3").Node("node-d").Label("foo", "").Annotation(podutil.PodLauncherAnnotationKey, string(podutil.NodeManager)).Obj(),
			},
			nodes: []*v1.Node{
				{ObjectMeta: metav1.ObjectMeta{Name: "node-a", Labels: map[string]string{v1.LabelHostname: "node-a"}}},
			},
			nmNodes: []*nodev1alpha1.NMNode{
				{ObjectMeta: metav1.ObjectMeta{Name: "node-b", Labels: map[string]string{v1.LabelHostname: "node-b"}}},
				{ObjectMeta: metav1.ObjectMeta{Name: "node-c", Labels: map[string]string{v1.LabelHostname: "node-c"}}},
				{ObjectMeta: metav1.ObjectMeta{Name: "node-d", Labels: map[string]string{v1.LabelHostname: "node-d"}}},
			},
			want: []framework.NodeScore{
				{Name: "node-a", Score: 0},
				{Name: "node-b", Score: 75},
				{Name: "node-c", Score: 100},
				{Name: "node-d", Score: 0},
			},
		},
		{
			// matching pods spread as 2-0/1/0/3. The first node-a has v1.node and NMNode.
			// When counting the pods of each topology, we will record all the pods of node-a.
			// However, when calculating its score, since the podLanucher of the pod to be scheduled is the node manager,
			//only the pods on the NMNode of node-a will be considered. Since the number of pods on the NMNode is 0, the score is 100.
			name: "The first node-a has v1.node and NMNode, and the others are of NMNode type. The score of node-a is 100",
			pod: testing_helper.MakePod().Name("p").Label("foo", "").
				SpreadConstraint(1, v1.LabelHostname, v1.ScheduleAnyway, testing_helper.MakeLabelSelector().Exists("foo").Obj()).
				Obj(),
			existingPods: []*v1.Pod{
				testing_helper.MakePod().Name("p-a1").Node("node-a").Label("foo", "").Obj(),
				testing_helper.MakePod().Name("p-a2").Node("node-a").Label("foo", "").Obj(),
				testing_helper.MakePod().Name("p-b1").Node("node-b").Label("foo", "").Annotation(podutil.PodLauncherAnnotationKey, string(podutil.NodeManager)).Obj(),
				testing_helper.MakePod().Name("p-d1").Node("node-d").Label("foo", "").Annotation(podutil.PodLauncherAnnotationKey, string(podutil.NodeManager)).Obj(),
				testing_helper.MakePod().Name("p-d2").Node("node-d").Label("foo", "").Annotation(podutil.PodLauncherAnnotationKey, string(podutil.NodeManager)).Obj(),
				testing_helper.MakePod().Name("p-d3").Node("node-d").Label("foo", "").Annotation(podutil.PodLauncherAnnotationKey, string(podutil.NodeManager)).Obj(),
			},
			nodes: []*v1.Node{
				{ObjectMeta: metav1.ObjectMeta{Name: "node-a", Labels: map[string]string{v1.LabelHostname: "node-a"}}},
			},
			nmNodes: []*nodev1alpha1.NMNode{
				{ObjectMeta: metav1.ObjectMeta{Name: "node-a", Labels: map[string]string{v1.LabelHostname: "node-a"}}},
				{ObjectMeta: metav1.ObjectMeta{Name: "node-b", Labels: map[string]string{v1.LabelHostname: "node-b"}}},
				{ObjectMeta: metav1.ObjectMeta{Name: "node-c", Labels: map[string]string{v1.LabelHostname: "node-c"}}},
				{ObjectMeta: metav1.ObjectMeta{Name: "node-d", Labels: map[string]string{v1.LabelHostname: "node-d"}}},
			},
			want: []framework.NodeScore{
				{Name: "node-a", Score: 100},
				{Name: "node-b", Score: 80},
				{Name: "node-c", Score: 100},
				{Name: "node-d", Score: 0},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.pod.Annotations = map[string]string{podutil.PodLauncherAnnotationKey: string(podutil.NodeManager)}
			state := framework.NewCycleState()

			snapshot := framework_helper.MakeSnapShot(tt.existingPods, tt.nodes, tt.nmNodes)

			p := &PodTopologySpread{sharedLister: snapshot}

			nodeNames := getNodeNames(tt.nodes, tt.nmNodes)
			nodeInfos := make([]framework.NodeInfo, len(nodeNames))
			for indexNode := 0; indexNode < len(nodeNames); indexNode++ {
				nodeInfos[indexNode], _ = snapshot.NodeInfos().Get(nodeNames[indexNode])
			}

			status := p.PreScore(context.Background(), state, tt.pod, nodeInfos)
			if !status.IsSuccess() {
				t.Errorf("unexpected error: %v", status)
			}

			var gotList framework.NodeScoreList
			for _, nodeInfo := range nodeInfos {
				nodeName := nodeInfo.GetNodeName()
				score, status := p.Score(context.Background(), state, tt.pod, nodeName)
				if !status.IsSuccess() {
					t.Errorf("unexpected error: %v", status)
				}
				gotList = append(gotList, framework.NodeScore{Name: nodeName, Score: score})
			}

			status = p.NormalizeScore(context.Background(), state, tt.pod, gotList)
			if !status.IsSuccess() {
				t.Errorf("unexpected error: %v", status)
			}
			if diff := cmp.Diff(tt.want, gotList, cmpOpts...); diff != "" {
				t.Errorf("unexpected scores (-want,+got):\n%s", diff)
			}
		})
	}
}

func getNodeNames(nodes []*v1.Node, nmNodes []*nodev1alpha1.NMNode) []string {
	nameSet := sets.NewString()
	for _, node := range nodes {
		nameSet.Insert(node.Name)
	}
	for _, nmNode := range nmNodes {
		nameSet.Insert(nmNode.Name)
	}
	return nameSet.List()
}

func BenchmarkTestPodTopologySpreadScore(b *testing.B) {
	tests := []struct {
		name             string
		pod              *v1.Pod
		existingPodsNum  int
		allNodesNum      int
		filteredNodesNum int
	}{
		{
			name: "1000nodes/single-constraint-zone",
			pod: testing_helper.MakePod().Name("p").Label("foo", "").
				SpreadConstraint(1, v1.LabelZoneFailureDomain, v1.ScheduleAnyway, testing_helper.MakeLabelSelector().Exists("foo").Obj()).
				Obj(),
			existingPodsNum:  10000,
			allNodesNum:      1000,
			filteredNodesNum: 500,
		},
		{
			name: "1000nodes/single-constraint-node",
			pod: testing_helper.MakePod().Name("p").Label("foo", "").
				SpreadConstraint(1, v1.LabelHostname, v1.ScheduleAnyway, testing_helper.MakeLabelSelector().Exists("foo").Obj()).
				Obj(),
			existingPodsNum:  10000,
			allNodesNum:      1000,
			filteredNodesNum: 500,
		},
		{
			name: "1000nodes/two-Constraints-zone-node",
			pod: testing_helper.MakePod().Name("p").Label("foo", "").Label("bar", "").
				SpreadConstraint(1, v1.LabelZoneFailureDomain, v1.ScheduleAnyway, testing_helper.MakeLabelSelector().Exists("foo").Obj()).
				SpreadConstraint(1, v1.LabelHostname, v1.ScheduleAnyway, testing_helper.MakeLabelSelector().Exists("bar").Obj()).
				Obj(),
			existingPodsNum:  10000,
			allNodesNum:      1000,
			filteredNodesNum: 500,
		},
	}
	for _, tt := range tests {
		b.Run(tt.name, func(b *testing.B) {
			existingPods, allNodes, filteredNodes := testing_helper.MakeNodesAndPodsForEvenPodsSpread(tt.pod.Labels, tt.existingPodsNum, tt.allNodesNum, tt.filteredNodesNum)
			state := framework.NewCycleState()
			snapshot := framework_helper.MakeSnapShot(existingPods, allNodes, nil)

			p := &PodTopologySpread{sharedLister: snapshot}

			nodeInfos := make([]framework.NodeInfo, len(filteredNodes))
			for index, node := range filteredNodes {
				nodeInfos[index] = framework_helper.WithNode(node)
			}
			status := p.PreScore(context.Background(), state, tt.pod, nodeInfos)
			if !status.IsSuccess() {
				b.Fatalf("unexpected error: %v", status)
			}
			b.ResetTimer()

			for i := 0; i < b.N; i++ {
				var gotList framework.NodeScoreList
				for _, n := range filteredNodes {
					nodeName := n.Name
					score, status := p.Score(context.Background(), state, tt.pod, nodeName)
					if !status.IsSuccess() {
						b.Fatalf("unexpected error: %v", status)
					}
					gotList = append(gotList, framework.NodeScore{Name: nodeName, Score: score})
				}

				status = p.NormalizeScore(context.Background(), state, tt.pod, gotList)
				if !status.IsSuccess() {
					b.Fatal(status)
				}
			}
		})
	}
}

// The following test allows to compare PodTopologySpread.Score with
// SelectorSpread.Score by using a similar rule.
// See pkg/scheduler/framework/plugins/selectorspread/selector_spread_perf_test.go
// for the equivalent test.

var (
	tests = []struct {
		name            string
		existingPodsNum int
		allNodesNum     int
	}{
		{
			name:            "100nodes",
			existingPodsNum: 1000,
			allNodesNum:     100,
		},
		{
			name:            "1000nodes",
			existingPodsNum: 10000,
			allNodesNum:     1000,
		},
	}
)

func BenchmarkTestDefaultEvenPodsSpreadPriority(b *testing.B) {
	for _, tt := range tests {
		b.Run(tt.name, func(b *testing.B) {
			pod := testing_helper.MakePod().Name("p").Label("foo", "").Obj()
			existingPods, allNodes, filteredNodes := testing_helper.MakeNodesAndPodsForEvenPodsSpread(pod.Labels, tt.existingPodsNum, tt.allNodesNum, tt.allNodesNum)
			state := framework.NewCycleState()
			snapshot := framework_helper.MakeSnapShot(existingPods, allNodes, nil)

			p := &PodTopologySpread{
				sharedLister: snapshot,
				args: config.PodTopologySpreadArgs{
					DefaultConstraints: []v1.TopologySpreadConstraint{
						{MaxSkew: 1, TopologyKey: v1.LabelHostname, WhenUnsatisfiable: v1.ScheduleAnyway},
						{MaxSkew: 1, TopologyKey: v1.LabelZoneFailureDomain, WhenUnsatisfiable: v1.ScheduleAnyway},
					},
				},
			}
			client := fake.NewSimpleClientset(
				&v1.Service{Spec: v1.ServiceSpec{Selector: map[string]string{"foo": ""}}},
			)
			ctx := context.Background()
			informerFactory := informers.NewSharedInformerFactory(client, 0)
			p.setListers(informerFactory)
			informerFactory.Start(ctx.Done())
			informerFactory.WaitForCacheSync(ctx.Done())

			b.ResetTimer()

			nodeInfos := make([]framework.NodeInfo, len(filteredNodes))
			for index, node := range filteredNodes {
				nodeInfos[index] = framework_helper.WithNode(node)
			}

			for i := 0; i < b.N; i++ {
				status := p.PreScore(ctx, state, pod, nodeInfos)
				if !status.IsSuccess() {
					b.Fatalf("unexpected error: %v", status)
				}
				gotList := make(framework.NodeScoreList, len(filteredNodes))
				scoreNode := func(i int) {
					n := filteredNodes[i]
					score, _ := p.Score(ctx, state, pod, n.Name)
					gotList[i] = framework.NodeScore{Name: n.Name, Score: score}
				}
				parallelize.Until(ctx, len(filteredNodes), scoreNode)
				status = p.NormalizeScore(ctx, state, pod, gotList)
				if !status.IsSuccess() {
					b.Fatal(status)
				}
			}
		})
	}
}
