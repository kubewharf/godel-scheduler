/*
Copyright 2024 The Godel Scheduler Authors.

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
	"reflect"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/kubewharf/godel-scheduler/pkg/binder/cache"
	"github.com/kubewharf/godel-scheduler/pkg/binder/framework/handle"
	pt "github.com/kubewharf/godel-scheduler/pkg/binder/testing"
	commoncache "github.com/kubewharf/godel-scheduler/pkg/common/cache"
	framework "github.com/kubewharf/godel-scheduler/pkg/framework/api"
	utils "github.com/kubewharf/godel-scheduler/pkg/plugins/podtopologyspread"
	testing_helper "github.com/kubewharf/godel-scheduler/pkg/testing-helper"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes/fake"
)

var cmpOpts = []cmp.Option{
	cmp.Comparer(func(s1 labels.Selector, s2 labels.Selector) bool {
		return reflect.DeepEqual(s1, s2)
	}),
	cmp.Comparer(func(p1, p2 utils.CriticalPaths) bool {
		p1.Sort()
		p2.Sort()
		return p1[0] == p2[0] && p1[1] == p2[1]
	}),
}

func mustConvertLabelSelectorAsSelector(t *testing.T, ls *metav1.LabelSelector) labels.Selector {
	t.Helper()
	s, err := metav1.LabelSelectorAsSelector(ls)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func initInformerFactoryInformers(informerFactory *informers.SharedInformerFactory) {
	(*informerFactory).Core().V1().Nodes().Informer()
	(*informerFactory).Core().V1().Pods().Informer()
	(*informerFactory).Core().V1().Services().Informer()
	(*informerFactory).Apps().V1().ReplicaSets().Informer()
	(*informerFactory).Apps().V1().StatefulSets().Informer()
}

func initFrameworkHandle(client *fake.Clientset, nodes []*v1.Node, existingPods []*v1.Pod) (handle.BinderFrameworkHandle, error) {
	ctx := context.Background()
	informerFactory := informers.NewSharedInformerFactory(client, 0)
	initInformerFactoryInformers(&informerFactory)
	cacheHandler := commoncache.MakeCacheHandlerWrapper().
		Period(10 * time.Second).PodAssumedTTL(30 * time.Second).StopCh(make(chan struct{})).
		ComponentName("godel-binder").Obj()
	cache := cache.New(cacheHandler)

	for _, node := range nodes {
		_, err := client.CoreV1().Nodes().Create(context.TODO(), node, metav1.CreateOptions{})
		if err != nil {
			return nil, err
		}
		cache.AddNode(node)
	}

	for _, pod := range existingPods {
		pod.UID = types.UID(pod.Name)
		cache.AddPod(pod)
	}

	informerFactory.Start(ctx.Done())
	informerFactory.WaitForCacheSync(ctx.Done())
	return pt.NewBinderFrameworkHandle(client, nil, informerFactory, nil, cache)
}

func TestCheckConflictsForSingleConstraint(t *testing.T) {
	tests := []struct {
		name           string
		pod            *v1.Pod
		nodes          []*v1.Node
		existingPods   []*v1.Pod
		wantStatusCode map[string]framework.Code
	}{
		{
			name: "no existing pods",
			pod: testing_helper.MakePod().Name("p").Label("foo", "").SpreadConstraint(
				1, "zone", v1.DoNotSchedule, testing_helper.MakeLabelSelector().Exists("foo").Obj(),
			).Obj(),
			nodes: []*v1.Node{
				testing_helper.MakeNode().Name("node-a").Label("zone", "zone1").Label("node", "node-a").Obj(),
				testing_helper.MakeNode().Name("node-b").Label("zone", "zone1").Label("node", "node-b").Obj(),
				testing_helper.MakeNode().Name("node-x").Label("zone", "zone2").Label("node", "node-x").Obj(),
				testing_helper.MakeNode().Name("node-y").Label("zone", "zone2").Label("node", "node-y").Obj(),
			},
			wantStatusCode: map[string]framework.Code{
				"node-a": framework.Success,
				"node-b": framework.Success,
				"node-x": framework.Success,
				"node-y": framework.Success,
			},
		},
		{
			name: "no existing pods, incoming pod doesn't match itself",
			pod: testing_helper.MakePod().Name("p").Label("foo", "").SpreadConstraint(
				1, "zone", v1.DoNotSchedule, testing_helper.MakeLabelSelector().Exists("bar").Obj(),
			).Obj(),
			nodes: []*v1.Node{
				testing_helper.MakeNode().Name("node-a").Label("zone", "zone1").Label("node", "node-a").Obj(),
				testing_helper.MakeNode().Name("node-b").Label("zone", "zone1").Label("node", "node-b").Obj(),
				testing_helper.MakeNode().Name("node-x").Label("zone", "zone2").Label("node", "node-x").Obj(),
				testing_helper.MakeNode().Name("node-y").Label("zone", "zone2").Label("node", "node-y").Obj(),
			},
			wantStatusCode: map[string]framework.Code{
				"node-a": framework.Success,
				"node-b": framework.Success,
				"node-x": framework.Success,
				"node-y": framework.Success,
			},
		},
		{
			name: "existing pods in a different namespace do not count",
			pod: testing_helper.MakePod().Name("p").Label("foo", "").SpreadConstraint(
				1, "zone", v1.DoNotSchedule, testing_helper.MakeLabelSelector().Exists("foo").Obj(),
			).Obj(),
			nodes: []*v1.Node{
				testing_helper.MakeNode().Name("node-a").Label("zone", "zone1").Label("node", "node-a").Obj(),
				testing_helper.MakeNode().Name("node-b").Label("zone", "zone1").Label("node", "node-b").Obj(),
				testing_helper.MakeNode().Name("node-x").Label("zone", "zone2").Label("node", "node-x").Obj(),
				testing_helper.MakeNode().Name("node-y").Label("zone", "zone2").Label("node", "node-y").Obj(),
			},
			existingPods: []*v1.Pod{
				testing_helper.MakePod().Name("p-a1").Namespace("ns1").Node("node-a").Label("foo", "").Obj(),
				testing_helper.MakePod().Name("p-b1").Namespace("ns2").Node("node-a").Label("foo", "").Obj(),
				testing_helper.MakePod().Name("p-x1").Node("node-x").Label("foo", "").Obj(),
				testing_helper.MakePod().Name("p-y1").Node("node-y").Label("foo", "").Obj(),
			},
			wantStatusCode: map[string]framework.Code{
				"node-a": framework.Success,
				"node-b": framework.Success,
				"node-x": framework.Unschedulable,
				"node-y": framework.Unschedulable,
			},
		},
		{
			name: "pods spread across zones as 3/3, all nodes fit",
			pod: testing_helper.MakePod().Name("p").Label("foo", "").SpreadConstraint(
				1, "zone", v1.DoNotSchedule, testing_helper.MakeLabelSelector().Exists("foo").Obj(),
			).Obj(),
			nodes: []*v1.Node{
				testing_helper.MakeNode().Name("node-a").Label("zone", "zone1").Label("node", "node-a").Obj(),
				testing_helper.MakeNode().Name("node-b").Label("zone", "zone1").Label("node", "node-b").Obj(),
				testing_helper.MakeNode().Name("node-x").Label("zone", "zone2").Label("node", "node-x").Obj(),
				testing_helper.MakeNode().Name("node-y").Label("zone", "zone2").Label("node", "node-y").Obj(),
			},
			existingPods: []*v1.Pod{
				testing_helper.MakePod().Name("p-a1").Node("node-a").Label("foo", "").Obj(),
				testing_helper.MakePod().Name("p-a2").Node("node-a").Label("foo", "").Obj(),
				testing_helper.MakePod().Name("p-b1").Node("node-b").Label("foo", "").Obj(),
				testing_helper.MakePod().Name("p-y1").Node("node-y").Label("foo", "").Obj(),
				testing_helper.MakePod().Name("p-y2").Node("node-y").Label("foo", "").Obj(),
				testing_helper.MakePod().Name("p-y3").Node("node-y").Label("foo", "").Obj(),
			},
			wantStatusCode: map[string]framework.Code{
				"node-a": framework.Success,
				"node-b": framework.Success,
				"node-x": framework.Success,
				"node-y": framework.Success,
			},
		},
		{
			// TODO(Huang-Wei): maybe document this to remind users that typos on node labels
			// can cause unexpected behavior
			name: "pods spread across zones as 1/2 due to absence of label 'zone' on node-b",
			pod: testing_helper.MakePod().Name("p").Label("foo", "").SpreadConstraint(
				1, "zone", v1.DoNotSchedule, testing_helper.MakeLabelSelector().Exists("foo").Obj(),
			).Obj(),
			nodes: []*v1.Node{
				testing_helper.MakeNode().Name("node-a").Label("zone", "zone1").Label("node", "node-a").Obj(),
				testing_helper.MakeNode().Name("node-b").Label("zon", "zone1").Label("node", "node-b").Obj(),
				testing_helper.MakeNode().Name("node-x").Label("zone", "zone2").Label("node", "node-x").Obj(),
				testing_helper.MakeNode().Name("node-y").Label("zone", "zone2").Label("node", "node-y").Obj(),
			},
			existingPods: []*v1.Pod{
				testing_helper.MakePod().Name("p-a1").Node("node-a").Label("foo", "").Obj(),
				testing_helper.MakePod().Name("p-b1").Node("node-b").Label("foo", "").Obj(),
				testing_helper.MakePod().Name("p-x1").Node("node-x").Label("foo", "").Obj(),
				testing_helper.MakePod().Name("p-y1").Node("node-y").Label("foo", "").Obj(),
			},
			wantStatusCode: map[string]framework.Code{
				"node-a": framework.Success,
				"node-b": framework.UnschedulableAndUnresolvable,
				"node-x": framework.Unschedulable,
				"node-y": framework.Unschedulable,
			},
		},
		{
			name: "pod cannot be scheduled as all nodes don't have label 'rack'",
			pod: testing_helper.MakePod().Name("p").Label("foo", "").SpreadConstraint(
				1, "rack", v1.DoNotSchedule, testing_helper.MakeLabelSelector().Exists("foo").Obj(),
			).Obj(),
			nodes: []*v1.Node{
				testing_helper.MakeNode().Name("node-a").Label("zone", "zone1").Label("node", "node-a").Obj(),
				testing_helper.MakeNode().Name("node-x").Label("zone", "zone2").Label("node", "node-x").Obj(),
			},
			wantStatusCode: map[string]framework.Code{
				"node-a": framework.UnschedulableAndUnresolvable,
				"node-x": framework.UnschedulableAndUnresolvable,
			},
		},
		{
			name: "pods spread across nodes as 2/1/0/3, only node-x fits",
			pod: testing_helper.MakePod().Name("p").Label("foo", "").SpreadConstraint(
				1, "node", v1.DoNotSchedule, testing_helper.MakeLabelSelector().Exists("foo").Obj(),
			).Obj(),
			nodes: []*v1.Node{
				testing_helper.MakeNode().Name("node-a").Label("zone", "zone1").Label("node", "node-a").Obj(),
				testing_helper.MakeNode().Name("node-b").Label("zone", "zone1").Label("node", "node-b").Obj(),
				testing_helper.MakeNode().Name("node-x").Label("zone", "zone2").Label("node", "node-x").Obj(),
				testing_helper.MakeNode().Name("node-y").Label("zone", "zone2").Label("node", "node-y").Obj(),
			},
			existingPods: []*v1.Pod{
				testing_helper.MakePod().Name("p-a1").Node("node-a").Label("foo", "").Obj(),
				testing_helper.MakePod().Name("p-a2").Node("node-a").Label("foo", "").Obj(),
				testing_helper.MakePod().Name("p-b1").Node("node-b").Label("foo", "").Obj(),
				testing_helper.MakePod().Name("p-y1").Node("node-y").Label("foo", "").Obj(),
				testing_helper.MakePod().Name("p-y2").Node("node-y").Label("foo", "").Obj(),
				testing_helper.MakePod().Name("p-y3").Node("node-y").Label("foo", "").Obj(),
			},
			wantStatusCode: map[string]framework.Code{
				"node-a": framework.Unschedulable,
				"node-b": framework.Unschedulable,
				"node-x": framework.Success,
				"node-y": framework.Unschedulable,
			},
		},
		{
			name: "pods spread across nodes as 2/1/0/3, maxSkew is 2, node-b and node-x fit",
			pod: testing_helper.MakePod().Name("p").Label("foo", "").SpreadConstraint(
				2, "node", v1.DoNotSchedule, testing_helper.MakeLabelSelector().Exists("foo").Obj(),
			).Obj(),
			nodes: []*v1.Node{
				testing_helper.MakeNode().Name("node-a").Label("zone", "zone1").Label("node", "node-a").Obj(),
				testing_helper.MakeNode().Name("node-b").Label("zone", "zone1").Label("node", "node-b").Obj(),
				testing_helper.MakeNode().Name("node-x").Label("zone", "zone2").Label("node", "node-x").Obj(),
				testing_helper.MakeNode().Name("node-y").Label("zone", "zone2").Label("node", "node-y").Obj(),
			},
			existingPods: []*v1.Pod{
				testing_helper.MakePod().Name("p-a1").Node("node-a").Label("foo", "").Obj(),
				testing_helper.MakePod().Name("p-a2").Node("node-a").Label("foo", "").Obj(),
				testing_helper.MakePod().Name("p-b1").Node("node-b").Label("foo", "").Obj(),
				testing_helper.MakePod().Name("p-y1").Node("node-y").Label("foo", "").Obj(),
				testing_helper.MakePod().Name("p-y2").Node("node-y").Label("foo", "").Obj(),
				testing_helper.MakePod().Name("p-y3").Node("node-y").Label("foo", "").Obj(),
			},
			wantStatusCode: map[string]framework.Code{
				"node-a": framework.Unschedulable,
				"node-b": framework.Success,
				"node-x": framework.Success,
				"node-y": framework.Unschedulable,
			},
		},
		{
			// not a desired case, but it can happen
			// TODO(Huang-Wei): document this "pod-not-match-itself" case
			// in this case, placement of the new pod doesn't change pod distribution of the cluster
			// as the incoming pod doesn't have label "foo"
			name: "pods spread across nodes as 2/1/0/3, but pod doesn't match itself",
			pod: testing_helper.MakePod().Name("p").Label("bar", "").SpreadConstraint(
				1, "node", v1.DoNotSchedule, testing_helper.MakeLabelSelector().Exists("foo").Obj(),
			).Obj(),
			nodes: []*v1.Node{
				testing_helper.MakeNode().Name("node-a").Label("zone", "zone1").Label("node", "node-a").Obj(),
				testing_helper.MakeNode().Name("node-b").Label("zone", "zone1").Label("node", "node-b").Obj(),
				testing_helper.MakeNode().Name("node-x").Label("zone", "zone2").Label("node", "node-x").Obj(),
				testing_helper.MakeNode().Name("node-y").Label("zone", "zone2").Label("node", "node-y").Obj(),
			},
			existingPods: []*v1.Pod{
				testing_helper.MakePod().Name("p-a1").Node("node-a").Label("foo", "").Obj(),
				testing_helper.MakePod().Name("p-a2").Node("node-a").Label("foo", "").Obj(),
				testing_helper.MakePod().Name("p-b1").Node("node-b").Label("foo", "").Obj(),
				testing_helper.MakePod().Name("p-y1").Node("node-y").Label("foo", "").Obj(),
				testing_helper.MakePod().Name("p-y2").Node("node-y").Label("foo", "").Obj(),
				testing_helper.MakePod().Name("p-y3").Node("node-y").Label("foo", "").Obj(),
			},
			wantStatusCode: map[string]framework.Code{
				"node-a": framework.Unschedulable,
				"node-b": framework.Success,
				"node-x": framework.Success,
				"node-y": framework.Unschedulable,
			},
		},
		{
			// only node-a and node-y are considered, so pods spread as 2/~1~/~0~/3
			// ps: '~num~' is a markdown symbol to denote a crossline through 'num'
			// but in this unit test, we don't run NodeAffinity Predicate, so node-b and node-x are
			// still expected to be fits;
			// the fact that node-a fits can prove the underlying logic works
			name: "incoming pod has nodeAffinity, pods spread as 2/~1~/~0~/3, hence node-a fits",
			pod: testing_helper.MakePod().Name("p").Label("foo", "").
				NodeAffinityIn("node", []string{"node-a", "node-y"}, testing_helper.NodeAffinityWithRequiredReq).
				SpreadConstraint(1, "node", v1.DoNotSchedule, testing_helper.MakeLabelSelector().Exists("foo").Obj()).
				Obj(),
			nodes: []*v1.Node{
				testing_helper.MakeNode().Name("node-a").Label("zone", "zone1").Label("node", "node-a").Obj(),
				testing_helper.MakeNode().Name("node-b").Label("zone", "zone1").Label("node", "node-b").Obj(),
				testing_helper.MakeNode().Name("node-x").Label("zone", "zone2").Label("node", "node-x").Obj(),
				testing_helper.MakeNode().Name("node-y").Label("zone", "zone2").Label("node", "node-y").Obj(),
			},
			existingPods: []*v1.Pod{
				testing_helper.MakePod().Name("p-a1").Node("node-a").Label("foo", "").Obj(),
				testing_helper.MakePod().Name("p-a2").Node("node-a").Label("foo", "").Obj(),
				testing_helper.MakePod().Name("p-b1").Node("node-b").Label("foo", "").Obj(),
				testing_helper.MakePod().Name("p-y1").Node("node-y").Label("foo", "").Obj(),
				testing_helper.MakePod().Name("p-y2").Node("node-y").Label("foo", "").Obj(),
				testing_helper.MakePod().Name("p-y3").Node("node-y").Label("foo", "").Obj(),
			},
			wantStatusCode: map[string]framework.Code{
				"node-a": framework.Success,
				"node-b": framework.Success, // in real case, it's false
				"node-x": framework.Success, // in real case, it's false
				"node-y": framework.Unschedulable,
			},
		},
		{
			name: "terminating Pods should be excluded",
			pod: testing_helper.MakePod().Name("p").Label("foo", "").SpreadConstraint(
				1, "node", v1.DoNotSchedule, testing_helper.MakeLabelSelector().Exists("foo").Obj(),
			).Obj(),
			nodes: []*v1.Node{
				testing_helper.MakeNode().Name("node-a").Label("node", "node-a").Obj(),
				testing_helper.MakeNode().Name("node-b").Label("node", "node-b").Obj(),
			},
			existingPods: []*v1.Pod{
				testing_helper.MakePod().Name("p-a").Node("node-a").Label("foo", "").Terminating().Obj(),
				testing_helper.MakePod().Name("p-b").Node("node-b").Label("foo", "").Obj(),
			},
			wantStatusCode: map[string]framework.Code{
				"node-a": framework.Success,
				"node-b": framework.Unschedulable,
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			frameworkHandle, err := initFrameworkHandle(fake.NewSimpleClientset(), tt.nodes, tt.existingPods)
			if err != nil {
				t.Fatal(err)
			}

			pl, err := New(nil, frameworkHandle)
			if err != nil {
				t.Fatal(err)
			}

			for _, node := range tt.nodes {
				nodeInfo := frameworkHandle.GetNodeInfo(node.Name)
				gotStatus := pl.(framework.CheckTopologyPlugin).CheckTopology(context.Background(), nil, tt.pod, nodeInfo)
				if len(tt.wantStatusCode) != 0 && gotStatus.Code() != tt.wantStatusCode[node.Name] {
					t.Errorf("[%s]: expected error code %v got %v", node.Name, tt.wantStatusCode[node.Name], gotStatus.Code())
				}
			}
		})
	}
}

func TestCheckConflictsForMultipleConstraint(t *testing.T) {
	tests := []struct {
		name           string
		pod            *v1.Pod
		nodes          []*v1.Node
		existingPods   []*v1.Pod
		wantStatusCode map[string]framework.Code
	}{
		{
			// 1. to fulfil "zone" constraint, incoming pod can be placed on any zone (hence any node)
			// 2. to fulfil "node" constraint, incoming pod can be placed on node-x
			// intersection of (1) and (2) returns node-x
			name: "two Constraints on zone and node, spreads = [3/3, 2/1/0/3]",
			pod: testing_helper.MakePod().Name("p").Label("foo", "").
				SpreadConstraint(1, "zone", v1.DoNotSchedule, testing_helper.MakeLabelSelector().Exists("foo").Obj()).
				SpreadConstraint(1, "node", v1.DoNotSchedule, testing_helper.MakeLabelSelector().Exists("foo").Obj()).
				Obj(),
			nodes: []*v1.Node{
				testing_helper.MakeNode().Name("node-a").Label("zone", "zone1").Label("node", "node-a").Obj(),
				testing_helper.MakeNode().Name("node-b").Label("zone", "zone1").Label("node", "node-b").Obj(),
				testing_helper.MakeNode().Name("node-x").Label("zone", "zone2").Label("node", "node-x").Obj(),
				testing_helper.MakeNode().Name("node-y").Label("zone", "zone2").Label("node", "node-y").Obj(),
			},
			existingPods: []*v1.Pod{
				testing_helper.MakePod().Name("p-a1").Node("node-a").Label("foo", "").Obj(),
				testing_helper.MakePod().Name("p-a2").Node("node-a").Label("foo", "").Obj(),
				testing_helper.MakePod().Name("p-b1").Node("node-b").Label("foo", "").Obj(),
				testing_helper.MakePod().Name("p-y1").Node("node-y").Label("foo", "").Obj(),
				testing_helper.MakePod().Name("p-y2").Node("node-y").Label("foo", "").Obj(),
				testing_helper.MakePod().Name("p-y3").Node("node-y").Label("foo", "").Obj(),
			},
			wantStatusCode: map[string]framework.Code{
				"node-a": framework.Unschedulable,
				"node-b": framework.Unschedulable,
				"node-x": framework.Success,
				"node-y": framework.Unschedulable,
			},
		},
		{
			// 1. to fulfil "zone" constraint, incoming pod can be placed on zone1 (node-a or node-b)
			// 2. to fulfil "node" constraint, incoming pod can be placed on node-x
			// intersection of (1) and (2) returns no node
			name: "two Constraints on zone and node, spreads = [3/4, 2/1/0/4]",
			pod: testing_helper.MakePod().Name("p").Label("foo", "").
				SpreadConstraint(1, "zone", v1.DoNotSchedule, testing_helper.MakeLabelSelector().Exists("foo").Obj()).
				SpreadConstraint(1, "node", v1.DoNotSchedule, testing_helper.MakeLabelSelector().Exists("foo").Obj()).
				Obj(),
			nodes: []*v1.Node{
				testing_helper.MakeNode().Name("node-a").Label("zone", "zone1").Label("node", "node-a").Obj(),
				testing_helper.MakeNode().Name("node-b").Label("zone", "zone1").Label("node", "node-b").Obj(),
				testing_helper.MakeNode().Name("node-x").Label("zone", "zone2").Label("node", "node-x").Obj(),
				testing_helper.MakeNode().Name("node-y").Label("zone", "zone2").Label("node", "node-y").Obj(),
			},
			existingPods: []*v1.Pod{
				testing_helper.MakePod().Name("p-a1").Node("node-a").Label("foo", "").Obj(),
				testing_helper.MakePod().Name("p-a2").Node("node-a").Label("foo", "").Obj(),
				testing_helper.MakePod().Name("p-b1").Node("node-b").Label("foo", "").Obj(),
				testing_helper.MakePod().Name("p-y1").Node("node-y").Label("foo", "").Obj(),
				testing_helper.MakePod().Name("p-y2").Node("node-y").Label("foo", "").Obj(),
				testing_helper.MakePod().Name("p-y3").Node("node-y").Label("foo", "").Obj(),
				testing_helper.MakePod().Name("p-y4").Node("node-y").Label("foo", "").Obj(),
			},
			wantStatusCode: map[string]framework.Code{
				"node-a": framework.Unschedulable,
				"node-b": framework.Unschedulable,
				"node-x": framework.Unschedulable,
				"node-y": framework.Unschedulable,
			},
		},
		{
			// 1. to fulfil "zone" constraint, incoming pod can be placed on zone2 (node-x or node-y)
			// 2. to fulfil "node" constraint, incoming pod can be placed on node-a, node-b or node-x
			// intersection of (1) and (2) returns node-x
			name: "Constraints hold different labelSelectors, spreads = [1/0, 1/0/0/1]",
			pod: testing_helper.MakePod().Name("p").Label("foo", "").Label("bar", "").
				SpreadConstraint(1, "zone", v1.DoNotSchedule, testing_helper.MakeLabelSelector().Exists("foo").Obj()).
				SpreadConstraint(1, "node", v1.DoNotSchedule, testing_helper.MakeLabelSelector().Exists("bar").Obj()).
				Obj(),
			nodes: []*v1.Node{
				testing_helper.MakeNode().Name("node-a").Label("zone", "zone1").Label("node", "node-a").Obj(),
				testing_helper.MakeNode().Name("node-b").Label("zone", "zone1").Label("node", "node-b").Obj(),
				testing_helper.MakeNode().Name("node-x").Label("zone", "zone2").Label("node", "node-x").Obj(),
				testing_helper.MakeNode().Name("node-y").Label("zone", "zone2").Label("node", "node-y").Obj(),
			},
			existingPods: []*v1.Pod{
				testing_helper.MakePod().Name("p-a1").Node("node-a").Label("foo", "").Obj(),
				testing_helper.MakePod().Name("p-y1").Node("node-y").Label("bar", "").Obj(),
			},
			wantStatusCode: map[string]framework.Code{
				"node-a": framework.Unschedulable,
				"node-b": framework.Unschedulable,
				"node-x": framework.Success,
				"node-y": framework.Unschedulable,
			},
		},
		{
			// 1. to fulfil "zone" constraint, incoming pod can be placed on zone2 (node-x or node-y)
			// 2. to fulfil "node" constraint, incoming pod can be placed on node-a or node-b
			// intersection of (1) and (2) returns no node
			name: "Constraints hold different labelSelectors, spreads = [1/0, 0/0/1/1]",
			pod: testing_helper.MakePod().Name("p").Label("foo", "").Label("bar", "").
				SpreadConstraint(1, "zone", v1.DoNotSchedule, testing_helper.MakeLabelSelector().Exists("foo").Obj()).
				SpreadConstraint(1, "node", v1.DoNotSchedule, testing_helper.MakeLabelSelector().Exists("bar").Obj()).
				Obj(),
			nodes: []*v1.Node{
				testing_helper.MakeNode().Name("node-a").Label("zone", "zone1").Label("node", "node-a").Obj(),
				testing_helper.MakeNode().Name("node-b").Label("zone", "zone1").Label("node", "node-b").Obj(),
				testing_helper.MakeNode().Name("node-x").Label("zone", "zone2").Label("node", "node-x").Obj(),
				testing_helper.MakeNode().Name("node-y").Label("zone", "zone2").Label("node", "node-y").Obj(),
			},
			existingPods: []*v1.Pod{
				testing_helper.MakePod().Name("p-a1").Node("node-a").Label("foo", "").Obj(),
				testing_helper.MakePod().Name("p-x1").Node("node-x").Label("bar", "").Obj(),
				testing_helper.MakePod().Name("p-y1").Node("node-y").Label("bar", "").Obj(),
			},
			wantStatusCode: map[string]framework.Code{
				"node-a": framework.Unschedulable,
				"node-b": framework.Unschedulable,
				"node-x": framework.Unschedulable,
				"node-y": framework.Unschedulable,
			},
		},
		{
			// 1. to fulfil "zone" constraint, incoming pod can be placed on zone1 (node-a or node-b)
			// 2. to fulfil "node" constraint, incoming pod can be placed on node-b or node-x
			// intersection of (1) and (2) returns node-b
			name: "Constraints hold different labelSelectors, spreads = [2/3, 1/0/0/1]",
			pod: testing_helper.MakePod().Name("p").Label("foo", "").Label("bar", "").
				SpreadConstraint(1, "zone", v1.DoNotSchedule, testing_helper.MakeLabelSelector().Exists("foo").Obj()).
				SpreadConstraint(1, "node", v1.DoNotSchedule, testing_helper.MakeLabelSelector().Exists("bar").Obj()).
				Obj(),
			nodes: []*v1.Node{
				testing_helper.MakeNode().Name("node-a").Label("zone", "zone1").Label("node", "node-a").Obj(),
				testing_helper.MakeNode().Name("node-b").Label("zone", "zone1").Label("node", "node-b").Obj(),
				testing_helper.MakeNode().Name("node-x").Label("zone", "zone2").Label("node", "node-x").Obj(),
				testing_helper.MakeNode().Name("node-y").Label("zone", "zone2").Label("node", "node-y").Obj(),
			},
			existingPods: []*v1.Pod{
				testing_helper.MakePod().Name("p-a1").Node("node-a").Label("foo", "").Obj(),
				testing_helper.MakePod().Name("p-a2").Node("node-a").Label("foo", "").Label("bar", "").Obj(),
				testing_helper.MakePod().Name("p-y1").Node("node-y").Label("foo", "").Obj(),
				testing_helper.MakePod().Name("p-y2").Node("node-y").Label("foo", "").Label("bar", "").Obj(),
				testing_helper.MakePod().Name("p-y3").Node("node-y").Label("foo", "").Obj(),
			},
			wantStatusCode: map[string]framework.Code{
				"node-a": framework.Unschedulable,
				"node-b": framework.Success,
				"node-x": framework.Unschedulable,
				"node-y": framework.Unschedulable,
			},
		},
		{
			// 1. pod doesn't match itself on "zone" constraint, so it can be put onto any zone
			// 2. to fulfil "node" constraint, incoming pod can be placed on node-a or node-b
			// intersection of (1) and (2) returns node-a and node-b
			name: "Constraints hold different labelSelectors but pod doesn't match itself on 'zone' constraint",
			pod: testing_helper.MakePod().Name("p").Label("bar", "").
				SpreadConstraint(1, "zone", v1.DoNotSchedule, testing_helper.MakeLabelSelector().Exists("foo").Obj()).
				SpreadConstraint(1, "node", v1.DoNotSchedule, testing_helper.MakeLabelSelector().Exists("bar").Obj()).
				Obj(),
			nodes: []*v1.Node{
				testing_helper.MakeNode().Name("node-a").Label("zone", "zone1").Label("node", "node-a").Obj(),
				testing_helper.MakeNode().Name("node-b").Label("zone", "zone1").Label("node", "node-b").Obj(),
				testing_helper.MakeNode().Name("node-x").Label("zone", "zone2").Label("node", "node-x").Obj(),
				testing_helper.MakeNode().Name("node-y").Label("zone", "zone2").Label("node", "node-y").Obj(),
			},
			existingPods: []*v1.Pod{
				testing_helper.MakePod().Name("p-a1").Node("node-a").Label("foo", "").Obj(),
				testing_helper.MakePod().Name("p-x1").Node("node-x").Label("bar", "").Obj(),
				testing_helper.MakePod().Name("p-y1").Node("node-y").Label("bar", "").Obj(),
			},
			wantStatusCode: map[string]framework.Code{
				"node-a": framework.Success,
				"node-b": framework.Success,
				"node-x": framework.Unschedulable,
				"node-y": framework.Unschedulable,
			},
		},
		{
			// 1. to fulfil "zone" constraint, incoming pod can be placed on any zone (hence any node)
			// 2. to fulfil "node" constraint, incoming pod can be placed on node-b (node-x doesn't have the required label)
			// intersection of (1) and (2) returns node-b
			name: "two Constraints on zone and node, absence of label 'node' on node-x, spreads = [1/1, 1/0/0/1]",
			pod: testing_helper.MakePod().Name("p").Label("foo", "").
				SpreadConstraint(1, "zone", v1.DoNotSchedule, testing_helper.MakeLabelSelector().Exists("foo").Obj()).
				SpreadConstraint(1, "node", v1.DoNotSchedule, testing_helper.MakeLabelSelector().Exists("foo").Obj()).
				Obj(),
			nodes: []*v1.Node{
				testing_helper.MakeNode().Name("node-a").Label("zone", "zone1").Label("node", "node-a").Obj(),
				testing_helper.MakeNode().Name("node-b").Label("zone", "zone1").Label("node", "node-b").Obj(),
				testing_helper.MakeNode().Name("node-x").Label("zone", "zone2").Obj(),
				testing_helper.MakeNode().Name("node-y").Label("zone", "zone2").Label("node", "node-y").Obj(),
			},
			existingPods: []*v1.Pod{
				testing_helper.MakePod().Name("p-a1").Node("node-a").Label("foo", "").Obj(),
				testing_helper.MakePod().Name("p-y3").Node("node-y").Label("foo", "").Obj(),
			},
			wantStatusCode: map[string]framework.Code{
				"node-a": framework.Unschedulable,
				"node-b": framework.Success,
				"node-x": framework.UnschedulableAndUnresolvable,
				"node-y": framework.Unschedulable,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			frameworkHandle, err := initFrameworkHandle(fake.NewSimpleClientset(), tt.nodes, tt.existingPods)
			if err != nil {
				t.Fatal(err)
			}

			pl, err := New(nil, frameworkHandle)
			if err != nil {
				t.Fatal(err)
			}

			for _, node := range tt.nodes {
				nodeInfo := frameworkHandle.GetNodeInfo(node.Name)
				gotStatus := pl.(framework.CheckTopologyPlugin).CheckTopology(context.Background(), nil, tt.pod, nodeInfo)
				if len(tt.wantStatusCode) != 0 && gotStatus.Code() != tt.wantStatusCode[node.Name] {
					t.Errorf("[%s]: expected error code %v got %v", node.Name, tt.wantStatusCode[node.Name], gotStatus.Code())
				}
			}
		})
	}
}
