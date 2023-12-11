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

package podscheduler

import (
	"context"
	"reflect"
	"testing"
	"time"

	godelclientfake "github.com/kubewharf/godel-scheduler-api/pkg/client/clientset/versioned/fake"
	crdinformers "github.com/kubewharf/godel-scheduler-api/pkg/client/informers/externalversions"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/client-go/informers"
	clientsetfake "k8s.io/client-go/kubernetes/fake"

	framework "github.com/kubewharf/godel-scheduler/pkg/framework/api"
	godelcache "github.com/kubewharf/godel-scheduler/pkg/scheduler/cache"
	"github.com/kubewharf/godel-scheduler/pkg/scheduler/cache/handler"
	"github.com/kubewharf/godel-scheduler/pkg/scheduler/cache/isolatedcache"
	"github.com/kubewharf/godel-scheduler/pkg/scheduler/core"
	schedulerframework "github.com/kubewharf/godel-scheduler/pkg/scheduler/framework"
	"github.com/kubewharf/godel-scheduler/pkg/scheduler/framework/plugins/noderesources"
	"github.com/kubewharf/godel-scheduler/pkg/scheduler/framework/preemption-plugins/searching/priorityvaluechecker"
	"github.com/kubewharf/godel-scheduler/pkg/scheduler/framework/preemption-plugins/sorting/priority"
	frameworkruntime "github.com/kubewharf/godel-scheduler/pkg/scheduler/framework/runtime"
	testinghelper "github.com/kubewharf/godel-scheduler/pkg/testing-helper"
	podutil "github.com/kubewharf/godel-scheduler/pkg/util/pod"
	"github.com/kubewharf/godel-scheduler/pkg/util/tracing"
)

func TestSelectHost(t *testing.T) {
	scheduler := podScheduler{}
	tests := []struct {
		name          string
		list          framework.NodeScoreList
		possibleHosts sets.String
		expectsErr    bool
	}{
		{
			name: "unique properly ordered scores",
			list: []framework.NodeScore{
				{Name: "machine1.1", Score: 1},
				{Name: "machine2.1", Score: 2},
			},
			possibleHosts: sets.NewString("machine2.1"),
			expectsErr:    false,
		},
		{
			name: "equal scores",
			list: []framework.NodeScore{
				{Name: "machine1.1", Score: 1},
				{Name: "machine1.2", Score: 2},
				{Name: "machine1.3", Score: 2},
				{Name: "machine2.1", Score: 2},
			},
			possibleHosts: sets.NewString("machine1.2", "machine1.3", "machine2.1"),
			expectsErr:    false,
		},
		{
			name: "out of order scores",
			list: []framework.NodeScore{
				{Name: "machine1.1", Score: 3},
				{Name: "machine1.2", Score: 3},
				{Name: "machine2.1", Score: 2},
				{Name: "machine3.1", Score: 1},
				{Name: "machine1.3", Score: 3},
			},
			possibleHosts: sets.NewString("machine1.1", "machine1.2", "machine1.3"),
			expectsErr:    false,
		},
		{
			name:          "empty priority list",
			list:          []framework.NodeScore{},
			possibleHosts: sets.NewString(),
			expectsErr:    true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			// increase the randomness
			for i := 0; i < 10; i++ {
				// TODO: add unit test cases for caching node logic
				got, err := scheduler.selectHostAndCacheResults(test.list, &v1.Pod{}, "", "", &framework.UnitSchedulingRequest{})
				if test.expectsErr {
					if err == nil {
						t.Error("Unexpected non-error")
					}
				} else {
					if err != nil {
						t.Errorf("Unexpected error: %v", err)
					}
					if !test.possibleHosts.Has(got) {
						t.Errorf("got %s is not in the possible map %v", got, test.possibleHosts)
					}
				}
			}
		})
	}
}

func TestNumFeasibleNodesToFind(t *testing.T) {
	podWithOutIncreasePercentageOfNodesToScoreAnnotationKey := testinghelper.MakePod().Namespace("default").Name("foo").UID("foo").Obj()
	podWithIncreasePercentageOfNodesToScoreAnnotationKey := testinghelper.MakePod().Namespace("default").Name("foo").UID("foo").Obj()
	podWithIncreasePercentageOfNodesToScoreAnnotationKey.Annotations = map[string]string{podutil.IncreasePercentageOfNodesToScoreAnnotationKey: "true"}

	tests := []struct {
		name                              string
		pod                               *v1.Pod
		percentageOfNodesToScore          int32
		increasedPercentageOfNodesToScore int32
		numAllNodes                       int32
		wantNumNodes                      int32
	}{
		{
			name:         "not set percentageOfNodesToScore and nodes number not more than 50",
			pod:          podWithOutIncreasePercentageOfNodesToScoreAnnotationKey,
			numAllNodes:  10,
			wantNumNodes: 10,
		},
		{
			name:                     "set percentageOfNodesToScore and nodes number not more than 50",
			pod:                      podWithOutIncreasePercentageOfNodesToScoreAnnotationKey,
			percentageOfNodesToScore: 40,
			numAllNodes:              10,
			wantNumNodes:             4,
		},
		{
			name:         "not set percentageOfNodesToScore and nodes number more than 50",
			pod:          podWithOutIncreasePercentageOfNodesToScoreAnnotationKey,
			numAllNodes:  1000,
			wantNumNodes: 60,
		},
		{
			name:                     "set percentageOfNodesToScore and nodes number more than 50",
			pod:                      podWithOutIncreasePercentageOfNodesToScoreAnnotationKey,
			percentageOfNodesToScore: 40,
			numAllNodes:              1000,
			wantNumNodes:             400,
		},
		{
			name:         "not set percentageOfNodesToScore and nodes number more than 50*125",
			pod:          podWithOutIncreasePercentageOfNodesToScoreAnnotationKey,
			numAllNodes:  6000,
			wantNumNodes: 60,
		},
		{
			name:                     "set percentageOfNodesToScore and nodes number more than 50*125",
			pod:                      podWithOutIncreasePercentageOfNodesToScoreAnnotationKey,
			percentageOfNodesToScore: 40,
			numAllNodes:              6000,
			wantNumNodes:             2400,
		},
		{
			name:                              "increasedPercentageOfNodesToScore set but pod does not have annotation key",
			pod:                               podWithOutIncreasePercentageOfNodesToScoreAnnotationKey,
			percentageOfNodesToScore:          10,
			increasedPercentageOfNodesToScore: 20,
			numAllNodes:                       1000,
			wantNumNodes:                      100,
		},
		{
			name:                              "increasedPercentageOfNodesToScore set and pod has annotation key",
			pod:                               podWithIncreasePercentageOfNodesToScoreAnnotationKey,
			percentageOfNodesToScore:          10,
			increasedPercentageOfNodesToScore: 20,
			numAllNodes:                       1000,
			wantNumNodes:                      200,
		},
		{
			name:                              "increasedPercentageOfNodesToScore not set but pod has annotation key",
			pod:                               podWithIncreasePercentageOfNodesToScoreAnnotationKey,
			percentageOfNodesToScore:          10,
			increasedPercentageOfNodesToScore: 0,
			numAllNodes:                       1000,
			wantNumNodes:                      100,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := &podScheduler{
				percentageOfNodesToScore:          tt.percentageOfNodesToScore,
				increasedPercentageOfNodesToScore: tt.increasedPercentageOfNodesToScore,
			}
			var increasePercentageOfNodesToScore bool
			if _, ok := tt.pod.Annotations[podutil.IncreasePercentageOfNodesToScoreAnnotationKey]; ok {
				increasePercentageOfNodesToScore = true
			}
			if gotNumNodes := g.numFeasibleNodesToFind(tt.numAllNodes, true, &framework.UnitSchedulingRequest{AllMember: 10}, increasePercentageOfNodesToScore); gotNumNodes != tt.wantNumNodes {
				t.Errorf("genericScheduler.numFeasibleNodesToFind() = %v, want %v", gotNumNodes, tt.wantNumNodes)
			}
		})
	}
}

func newBasePlugins() framework.PluginCollectionSet {
	basePlugins := make(framework.PluginCollectionSet)
	basePlugins[string(podutil.Kubelet)] = &framework.PluginCollection{
		Filters: []*framework.PluginSpec{
			framework.NewPluginSpec(noderesources.FitName),
		},
		Searchings: []*framework.VictimSearchingPluginCollectionSpec{
			{
				RejectNotSureVal: true,
				Plugins: []*framework.PluginSpec{
					{
						Name: priorityvaluechecker.PriorityValueCheckerName,
					},
				},
			},
		},
		Sortings: []*framework.PluginSpec{
			framework.NewPluginSpec(priority.MinHighestPriorityName),
		},
	}
	return basePlugins
}

func TestScheduleInSpecificNodeCircle(t *testing.T) {
	tests := []struct {
		name           string
		pod            *v1.Pod
		terminatingPod *v1.Pod
		cachedPod      *v1.Pod
		nodes          []*v1.Node
		expectedResult core.PodScheduleResult
		expectedErr    string
	}{
		{
			name: "schedule success, using cache",
			pod: testinghelper.MakePod().Namespace("default").Name("foo").UID("foo").
				ControllerRef(metav1.OwnerReference{Kind: "ReplicaSet", Name: "rs", UID: "rs"}).
				Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Obj(),
			cachedPod: testinghelper.MakePod().Namespace("default").Name("foo1").UID("foo1").Node("n2").
				ControllerRef(metav1.OwnerReference{Kind: "ReplicaSet", Name: "rs", UID: "rs"}).Obj(),
			nodes: []*v1.Node{
				testinghelper.MakeNode().Name("n1").Capacity(map[v1.ResourceName]string{v1.ResourceCPU: "900m"}).Obj(),
				testinghelper.MakeNode().Name("n2").Capacity(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Obj(),
			},
			expectedResult: core.PodScheduleResult{
				NumberOfEvaluatedNodes: 1,
				NumberOfFeasibleNodes:  1,
				SuggestedHost:          "n2",
			},
			expectedErr: "",
		},
		{
			name: "schedule success, find in all nodes",
			pod: testinghelper.MakePod().Namespace("default").Name("foo").UID("foo").
				ControllerRef(metav1.OwnerReference{Kind: "ReplicaSet", Name: "rs", UID: "rs"}).
				Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Obj(),
			nodes: []*v1.Node{
				testinghelper.MakeNode().Name("n1").Capacity(map[v1.ResourceName]string{v1.ResourceCPU: "900m"}).Obj(),
				testinghelper.MakeNode().Name("n2").Capacity(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Obj(),
			},
			expectedResult: core.PodScheduleResult{
				NumberOfEvaluatedNodes: 2,
				NumberOfFeasibleNodes:  1,
				SuggestedHost:          "n2",
			},
			expectedErr: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			stopCh := make(chan struct{})
			defer close(stopCh)
			schedulerCache := godelcache.New(handler.MakeCacheHandlerWrapper().
				SchedulerName("").SchedulerType("").SubCluster(framework.DefaultSubCluster).
				TTL(time.Second).Period(10 * time.Second).StopCh(make(<-chan struct{})).
				EnableStore("PreemptionStore").
				Obj())
			snapshot := godelcache.NewEmptySnapshot(handler.MakeCacheHandlerWrapper().
				SubCluster(framework.DefaultSubCluster).SwitchType(framework.DefaultSubClusterSwitchType).
				EnableStore("PreemptionStore").
				Obj())

			isolationCache := isolatedcache.NewIsolatedCache()
			for _, node := range tt.nodes {
				schedulerCache.AddNode(node)
			}
			if tt.terminatingPod != nil {
				schedulerCache.AddPod(tt.terminatingPod)
			}
			if tt.cachedPod != nil {
				isolationCache.CacheNodeForPodOwner(podutil.GetPodOwner(tt.cachedPod), tt.cachedPod.Spec.NodeName, "")
			}
			schedulerCache.UpdateSnapshot(snapshot)

			client := clientsetfake.NewSimpleClientset()
			crdClient := godelclientfake.NewSimpleClientset()
			informerFactory := informers.NewSharedInformerFactory(client, 0)
			crdInformerFactory := crdinformers.NewSharedInformerFactory(crdClient, 0)
			gs := &podScheduler{
				clientSet:          client,
				crdClient:          crdClient,
				informerFactory:    informerFactory,
				crdInformerFactory: crdInformerFactory,
				basePlugins:        newBasePlugins(),
				isolatedCache:      isolationCache,
				snapshot:           snapshot,
			}
			registry := schedulerframework.NewInTreeRegistry()
			pluginRegistry, err := schedulerframework.NewPluginsRegistry(registry, nil, gs)
			if err != nil {
				t.Errorf("failed to new plugins registry: %v", err)
			}
			f, err := frameworkruntime.NewPodFramework(pluginRegistry, nil, gs.getBasePluginsForPod(tt.pod), &framework.PluginCollection{}, &framework.PluginCollection{}, gs.metricsRecorder)

			nodeGroup := snapshot.MakeBasicNodeGroup()
			state := framework.NewCycleState()
			framework.SetPodResourceTypeState(podutil.GuaranteedPod, state)
			framework.SetPodTrace(&tracing.NoopSchedulingTrace{}, state)

			gotResult, gotErr := gs.ScheduleInSpecificNodeGroup(context.Background(), f, framework.NewCycleState(), framework.NewCycleState(), state, tt.pod, nodeGroup, &framework.UnitSchedulingRequest{EverScheduled: false, AllMember: 1}, make(framework.NodeToStatusMap))
			gotResult.FilteredNodesStatuses = nil
			if !reflect.DeepEqual(tt.expectedResult, gotResult) {
				t.Errorf("expected result: %v, but got: %v", tt.expectedResult, gotResult)
			}
			if gotErr == nil {
				if tt.expectedErr != "" {
					t.Errorf("expected error: %v, but got nil", tt.expectedErr)
				}
			} else if !reflect.DeepEqual(tt.expectedErr, gotErr.Error()) {
				t.Errorf("expected error: %v, but got: %v", tt.expectedErr, gotErr.Error())
			}
		})
	}
}
