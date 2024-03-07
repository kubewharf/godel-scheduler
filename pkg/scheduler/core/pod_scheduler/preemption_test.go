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
	"fmt"
	"math"
	"reflect"
	"sort"
	"testing"
	"time"

	godelclientfake "github.com/kubewharf/godel-scheduler-api/pkg/client/clientset/versioned/fake"
	crdinformers "github.com/kubewharf/godel-scheduler-api/pkg/client/informers/externalversions"
	katalystv1alpha1 "github.com/kubewharf/katalyst-api/pkg/apis/node/v1alpha1"
	v1 "k8s.io/api/core/v1"
	policy "k8s.io/api/policy/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/sets"
	utilfeature "k8s.io/apiserver/pkg/util/feature"
	"k8s.io/client-go/informers"
	clientsetfake "k8s.io/client-go/kubernetes/fake"
	"k8s.io/component-base/featuregate"

	"github.com/kubewharf/godel-scheduler/pkg/features"
	framework "github.com/kubewharf/godel-scheduler/pkg/framework/api"
	"github.com/kubewharf/godel-scheduler/pkg/scheduler/apis/config"
	godelcache "github.com/kubewharf/godel-scheduler/pkg/scheduler/cache"
	"github.com/kubewharf/godel-scheduler/pkg/scheduler/cache/handler"
	"github.com/kubewharf/godel-scheduler/pkg/scheduler/cache/isolatedcache"
	"github.com/kubewharf/godel-scheduler/pkg/scheduler/core"
	schedulerframework "github.com/kubewharf/godel-scheduler/pkg/scheduler/framework"
	"github.com/kubewharf/godel-scheduler/pkg/scheduler/framework/plugins/noderesources"
	"github.com/kubewharf/godel-scheduler/pkg/scheduler/framework/plugins/nonnativeresource"
	"github.com/kubewharf/godel-scheduler/pkg/scheduler/framework/preemption-plugins/searching/pdbchecker"
	"github.com/kubewharf/godel-scheduler/pkg/scheduler/framework/preemption-plugins/searching/priorityvaluechecker"
	"github.com/kubewharf/godel-scheduler/pkg/scheduler/framework/preemption-plugins/sorting/priority"
	starttime "github.com/kubewharf/godel-scheduler/pkg/scheduler/framework/preemption-plugins/sorting/start_time"
	victimscount "github.com/kubewharf/godel-scheduler/pkg/scheduler/framework/preemption-plugins/sorting/victims_count"
	frameworkruntime "github.com/kubewharf/godel-scheduler/pkg/scheduler/framework/runtime"
	st "github.com/kubewharf/godel-scheduler/pkg/scheduler/testing"
	testinghelper "github.com/kubewharf/godel-scheduler/pkg/testing-helper"
	"github.com/kubewharf/godel-scheduler/pkg/util"
	"github.com/kubewharf/godel-scheduler/pkg/util/constraints"
	podutil "github.com/kubewharf/godel-scheduler/pkg/util/pod"
	"github.com/kubewharf/godel-scheduler/pkg/util/tracing"
	"github.com/kubewharf/godel-scheduler/test/utils"
)

var (
	reservationTTL                                                        = 30 * time.Second
	negPriority, lowPriority, midPriority, highPriority, veryHighPriority = int32(-100), int32(0), int32(100), int32(1000), int32(10000)

	smallRes = map[v1.ResourceName]string{
		v1.ResourceCPU:    "100m",
		v1.ResourceMemory: "100",
	}
	mediumRes = map[v1.ResourceName]string{
		v1.ResourceCPU:    "200m",
		v1.ResourceMemory: "200",
	}
	largeRes = map[v1.ResourceName]string{
		v1.ResourceCPU:    "300m",
		v1.ResourceMemory: "300",
	}
	largeResWithGPU = map[v1.ResourceName]string{
		v1.ResourceCPU:    "300m",
		v1.ResourceMemory: "300",
		util.ResourceGPU:  "100m",
	}
	veryLargeRes = map[v1.ResourceName]string{
		v1.ResourceCPU:    "500m",
		v1.ResourceMemory: "500",
	}

	epochTime  = metav1.NewTime(time.Unix(0, 0))
	epochTime1 = metav1.NewTime(time.Unix(0, 1))
	epochTime2 = metav1.NewTime(time.Unix(0, 2))
	epochTime3 = metav1.NewTime(time.Unix(0, 3))
	epochTime4 = metav1.NewTime(time.Unix(0, 4))
	epochTime5 = metav1.NewTime(time.Unix(0, 5))
	epochTime6 = metav1.NewTime(time.Unix(0, 6))
)

func mergeObjs(pod *v1.Pod, pods []*v1.Pod) []runtime.Object {
	var objs []runtime.Object
	if pod != nil {
		objs = append(objs, pod)
	}
	for i := range pods {
		objs = append(objs, pods[i])
	}
	return objs
}

func newPreemptionBasedPlugins() framework.PluginCollectionSet {
	basePlugins := make(framework.PluginCollectionSet)
	basePlugins[string(podutil.Kubelet)] = &framework.PluginCollection{
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
			framework.NewPluginSpec(priority.MinPrioritySumName),
			framework.NewPluginSpec(victimscount.LeastVictimsName),
			framework.NewPluginSpec(starttime.LatestEarliestStartTimeName),
		},
	}
	return basePlugins
}

func TestPrepareNodes(t *testing.T) {
	// Prepare 4 nodes names.
	nodeNames := []string{"node1", "node2", "node3", "node4"}

	tests := []struct {
		name          string
		nodesStatuses framework.NodeToStatusMap
		expected      map[string]bool // set of expected node names. Value is ignored.
	}{
		{
			name: "UnschedulableAndUnresolvable status should be skipped but Unschedulable should be tried",
			nodesStatuses: framework.NodeToStatusMap{
				"node2": framework.NewStatus(framework.UnschedulableAndUnresolvable, ""),
				"node3": framework.NewStatus(framework.Unschedulable, ""),
				"node4": framework.NewStatus(framework.UnschedulableAndUnresolvable, ""),
			},
			expected: map[string]bool{"node1": true, "node3": true},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var nodeInfos []framework.NodeInfo
			for _, name := range nodeNames {
				ni := framework.NewNodeInfo()
				ni.SetNode(testinghelper.MakeNode().Name(name).Obj())
				nodeInfos = append(nodeInfos, ni)
			}
			gs := &podScheduler{}
			nodes, _ := gs.prepareNodes(context.Background(), nodeInfos, tt.nodesStatuses)
			gotFound := map[string]bool{}
			for _, node := range nodes {
				if node == nil {
					continue
				}
				gotFound[node.GetNodeName()] = true
				name := node.GetNodeName()
				if _, found := tt.expected[name]; !found {
					t.Errorf("node %v is not expected.", name)
				}
			}
			for nodeName, found := range tt.expected {
				if gotFound[nodeName] != found {
					t.Errorf("node %v is expected %v, but got %v", nodeName, found, gotFound[nodeName])
				}
			}
		})
	}
}

func TestSelectBestCandidate_NotUseCachedNominatedNodes(t *testing.T) {
	tests := []struct {
		name                         string
		preemptor                    *v1.Pod
		candidates                   []*framework.Candidate
		pods                         sets.String
		expectedNode                 string // any of the items is valid
		expectedUnusedNominatedNodes []string
	}{
		{
			name:      "select node with no victims",
			preemptor: testinghelper.MakePod().Priority(100).Obj(),
			candidates: []*framework.Candidate{
				{
					Victims: &framework.Victims{
						Pods: []*v1.Pod{
							testinghelper.MakePod().Name("p1").UID("p1").Node("node1").Priority(10).
								Annotation(util.CanBePreemptedAnnotationKey, util.CannotBePreempted).Obj(),
						},
					},
					Name: "node1",
				},
				{
					Victims: &framework.Victims{},
					Name:    "node2",
				},
			},
			expectedNode:                 "node2",
			expectedUnusedNominatedNodes: []string{"node1"},
		},
		{
			name:      "select node with min highest priority",
			preemptor: testinghelper.MakePod().Priority(highPriority).Obj(),
			candidates: []*framework.Candidate{
				{
					Victims: &framework.Victims{
						Pods: []*v1.Pod{
							testinghelper.MakePod().Name("p1").UID("p1").Node("node1").Priority(10).
								Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).Obj(),
							testinghelper.MakePod().Name("p2").UID("p2").Node("node1").Priority(9).
								Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).Obj(),
						},
						PreemptionState: framework.NewCycleState(),
					},
					Name: "node1",
				},
				{
					Victims: &framework.Victims{
						Pods: []*v1.Pod{
							testinghelper.MakePod().Name("p3").UID("p3").Node("node2").Priority(9).
								Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).Obj(),
							testinghelper.MakePod().Name("p4").UID("p4").Node("node2").Priority(9).
								Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).Obj(),
						},
						PreemptionState: framework.NewCycleState(),
					},
					Name: "node2",
				},
			},
			expectedNode:                 "node2",
			expectedUnusedNominatedNodes: []string{"node1"},
		},
		{
			name:      "select node with min sum priority",
			preemptor: testinghelper.MakePod().Priority(highPriority).Obj(),
			candidates: []*framework.Candidate{
				{
					Victims: &framework.Victims{
						Pods: []*v1.Pod{
							testinghelper.MakePod().Name("p1").UID("p1").Node("node1").Priority(10).
								Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).Obj(),
							testinghelper.MakePod().Name("p2").UID("p2").Node("node1").Priority(10).
								Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).Obj(),
						},
						PreemptionState: framework.NewCycleState(),
					},
					Name: "node1",
				},
				{
					Victims: &framework.Victims{
						Pods: []*v1.Pod{
							testinghelper.MakePod().Name("p3").UID("p3").Node("node2").Priority(10).
								Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).Obj(),
							testinghelper.MakePod().Name("p4").UID("p4").Node("node2").Priority(9).
								Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).Obj(),
						},
						PreemptionState: framework.NewCycleState(),
					},
					Name: "node2",
				},
			},
			expectedNode:                 "node2",
			expectedUnusedNominatedNodes: []string{"node1"},
		},
		{
			name:      "select node with the minimum number of pods",
			preemptor: testinghelper.MakePod().Priority(highPriority).Obj(),
			candidates: []*framework.Candidate{
				{
					Victims: &framework.Victims{
						Pods: []*v1.Pod{
							testinghelper.MakePod().Name("p1").UID("p1").Node("node1").Priority(math.MinInt32).
								Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).Obj(),
							testinghelper.MakePod().Name("p2").UID("p2").Node("node2").Priority(0).
								Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).Obj(),
						},
						PreemptionState: framework.NewCycleState(),
					},
					Name: "node1",
				},
				{
					Victims: &framework.Victims{
						Pods: []*v1.Pod{
							testinghelper.MakePod().Name("p3").UID("p3").Node("node2").Priority(math.MinInt32).Req(smallRes).
								Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).Obj(),
							testinghelper.MakePod().Name("p4").UID("p4").Node("node2").Priority(math.MinInt32).Req(smallRes).
								Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).Obj(),
							testinghelper.MakePod().Name("p5").UID("p5").Node("node2").Priority(0).Req(smallRes).
								Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).Obj(),
						},
						PreemptionState: framework.NewCycleState(),
					},
					Name: "node2",
				},
			},
			expectedNode:                 "node1",
			expectedUnusedNominatedNodes: []string{"node2"},
		},
		{
			name:      "the node that satisfies latest earliestStartTime",
			preemptor: testinghelper.MakePod().Priority(highPriority).Obj(),
			candidates: []*framework.Candidate{
				{
					Victims: &framework.Victims{
						Pods: []*v1.Pod{
							testinghelper.MakePod().Name("p1").UID("p1").Node("node1").Priority(lowPriority).Req(smallRes).StartTime(epochTime1).
								Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).Obj(),
							testinghelper.MakePod().Name("p2").UID("p2").Node("node1").Priority(lowPriority).Req(smallRes).StartTime(epochTime3).
								Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).Obj(),
						},
						PreemptionState: framework.NewCycleState(),
					},
					Name: "node1",
				},
				{
					Victims: &framework.Victims{
						Pods: []*v1.Pod{
							testinghelper.MakePod().Name("p3").UID("p3").Node("node2").Priority(lowPriority).Req(smallRes).StartTime(epochTime2).
								Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).Obj(),
							testinghelper.MakePod().Name("p4").UID("p4").Node("node2").Priority(lowPriority).Req(smallRes).StartTime(epochTime3).
								Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).Obj(),
						},
						PreemptionState: framework.NewCycleState(),
					},
					Name: "node2",
				},
			},
			expectedNode:                 "node2",
			expectedUnusedNominatedNodes: []string{"node1"},
		},
		{
			name:      "sort policy return nil",
			preemptor: testinghelper.MakePod().Priority(highPriority).Obj(),
			candidates: []*framework.Candidate{
				{
					Victims: &framework.Victims{
						Pods: []*v1.Pod{
							testinghelper.MakePod().Name("p1").UID("p1").Node("node1").Priority(lowPriority).Req(smallRes).StartTime(epochTime1).
								Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).Obj(),
							testinghelper.MakePod().Name("p2").UID("p2").Node("node2").Priority(lowPriority).Req(smallRes).StartTime(epochTime1).
								Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).Obj(),
						},
						PreemptionState: framework.NewCycleState(),
					},
					Name: "node1",
				},
				{
					Victims: &framework.Victims{
						Pods: []*v1.Pod{
							testinghelper.MakePod().Name("p3").UID("p3").Node("node2").Priority(lowPriority).Req(smallRes).StartTime(epochTime1).
								Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).Obj(),
							testinghelper.MakePod().Name("p4").UID("p4").Node("node2").Priority(lowPriority).Req(smallRes).StartTime(epochTime1).
								Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).Obj(),
						},
						PreemptionState: framework.NewCycleState(),
					},
					Name: "node2",
				},
			},
			pods:                         sets.NewString("/p1/p1", "/p2/p2", "/p3/p3", "/p4/p4"),
			expectedNode:                 "node1",
			expectedUnusedNominatedNodes: []string{"node2"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cache := godelcache.New(handler.MakeCacheHandlerWrapper().
				SchedulerName("").SchedulerType("").SubCluster(framework.DefaultSubCluster).
				TTL(time.Second).Period(10 * time.Second).StopCh(make(<-chan struct{})).
				EnableStore("PreemptionStore").
				Obj())
			snapshot := godelcache.NewEmptySnapshot(handler.MakeCacheHandlerWrapper().
				SubCluster(framework.DefaultSubCluster).SwitchType(framework.DefaultSubClusterSwitchType).
				EnableStore("PreemptionStore").
				Obj())
			for _, candidate := range tt.candidates {
				if candidate.Victims == nil {
					continue
				}
				for _, pod := range candidate.Victims.Pods {
					if !tt.pods.Has(podutil.GeneratePodKey(pod)) {
						continue
					}
					cache.AddPod(pod)
				}
			}
			cache.UpdateSnapshot(snapshot)

			crdClient := godelclientfake.NewSimpleClientset()
			crdInformerFactory := crdinformers.NewSharedInformerFactory(crdClient, 0)
			client := clientsetfake.NewSimpleClientset()
			informerFactory := informers.NewSharedInformerFactory(client, 0)
			fh, _ := st.NewSchedulerFrameworkHandle(client, crdClient, informerFactory, crdInformerFactory, cache, snapshot, nil, nil, nil, nil)

			registry := schedulerframework.NewInTreePreemptionRegistry()
			preemptionPluginRegistry, err := schedulerframework.NewPluginsRegistry(registry, nil, fh)
			if err != nil {
				t.Errorf("failed to new plugins registry: %v", err)
			}
			basePlugins := newPreemptionBasedPlugins()
			fh, _ = st.NewSchedulerFrameworkHandle(nil, nil, nil, nil, cache, snapshot, nil, preemptionPluginRegistry, nil, basePlugins[string(podutil.Kubelet)])

			fwk, err := fh.GetFrameworkForPod(tt.preemptor)
			state, err := fwk.InitCycleState(tt.preemptor)
			if err != nil {
				t.Fatal(err)
			}
			pfwk := fh.GetPreemptionFrameworkForPod(tt.preemptor)
			gs := &podScheduler{
				basePlugins: basePlugins,
				snapshot:    snapshot,
			}
			cachedNominatdNodes := framework.CachedNominatedNodes{}
			cachedNominatdNodes.SetPodCount(2)
			s := gs.SelectCandidate(context.Background(), fwk, pfwk, state, tt.preemptor, tt.candidates, nil, &cachedNominatdNodes, false)
			if s == nil {
				if len(tt.expectedNode) > 0 {
					t.Errorf("expect node %v, but got nil", tt.expectedNode)
				}
			} else {
				if tt.expectedNode != s.Name {
					t.Errorf("expect node %v, but got %v", tt.expectedNode, s.Name)
				}
			}

			if tt.expectedNode != cachedNominatdNodes.GetUsedNominatedNode().Name {
				t.Errorf("expected %v, but got %v", tt.expectedNode, cachedNominatdNodes.GetUsedNominatedNode().Name)
			}

			var gotUsedNodes []string
			for _, c := range cachedNominatdNodes.GetUnusedNominatedNodes() {
				gotUsedNodes = append(gotUsedNodes, c.Name)
			}
			if !reflect.DeepEqual(tt.expectedUnusedNominatedNodes, gotUsedNodes) {
				t.Errorf("expected %v but got %v", tt.expectedUnusedNominatedNodes, gotUsedNodes)
			}
		})
	}
}

func TestSelectBestCandidate_UseCachedNominatedNodes(t *testing.T) {
	tests := []struct {
		name                         string
		nodes                        []*v1.Node
		existingPods                 []*v1.Pod
		preemptor                    *v1.Pod
		candidates                   []*framework.Candidate
		candidate                    *framework.Candidate
		expectedNode                 string // any of the items is valid
		expectedUsedNominatedNode    string
		expectedUnusedNominatedNodes []string
	}{
		{
			name: "candidate is not nil, and is the most feasible candidate",
			nodes: []*v1.Node{
				testinghelper.MakeNode().Name("n").Obj(),
				testinghelper.MakeNode().Name("n1").Obj(),
				testinghelper.MakeNode().Name("n2").Obj(),
			},
			preemptor: testinghelper.MakePod().Obj(),
			candidate: &framework.Candidate{
				Name: "n",
				Victims: &framework.Victims{
					Pods: []*v1.Pod{
						testinghelper.MakePod().Name("p").Priority(9).Obj(),
					},
					PreemptionState: framework.NewCycleState(),
				},
			},
			candidates: []*framework.Candidate{
				{
					Name: "n1",
					Victims: &framework.Victims{
						Pods: []*v1.Pod{
							testinghelper.MakePod().Name("p1").Priority(10).Obj(),
						},
						PreemptionState: framework.NewCycleState(),
					},
				},
				{
					Name: "n2",
					Victims: &framework.Victims{
						Pods: []*v1.Pod{
							testinghelper.MakePod().Name("p2").Priority(11).Obj(),
						},
						PreemptionState: framework.NewCycleState(),
					},
				},
			},
			expectedNode:                 "n",
			expectedUsedNominatedNode:    "n",
			expectedUnusedNominatedNodes: []string{"n1", "n2"},
		},
		{
			name: "candidate is not nil, and is the most feasible candidate with the other",
			nodes: []*v1.Node{
				testinghelper.MakeNode().Name("n").Obj(),
				testinghelper.MakeNode().Name("n1").Obj(),
				testinghelper.MakeNode().Name("n2").Obj(),
			},
			preemptor: testinghelper.MakePod().Obj(),
			candidate: &framework.Candidate{
				Name: "n",
				Victims: &framework.Victims{
					Pods: []*v1.Pod{
						testinghelper.MakePod().Name("p").Priority(10).Obj(),
					},
					PreemptionState: framework.NewCycleState(),
				},
			},
			candidates: []*framework.Candidate{
				{
					Name: "n1",
					Victims: &framework.Victims{
						Pods: []*v1.Pod{
							testinghelper.MakePod().Name("p1").Priority(10).Obj(),
						},
						PreemptionState: framework.NewCycleState(),
					},
				},
				{
					Name: "n2",
					Victims: &framework.Victims{
						Pods: []*v1.Pod{
							testinghelper.MakePod().Name("p2").Priority(11).Obj(),
						},
						PreemptionState: framework.NewCycleState(),
					},
				},
			},
			expectedNode:                 "n",
			expectedUsedNominatedNode:    "n",
			expectedUnusedNominatedNodes: []string{"n1", "n2"},
		},
		{
			name: "candidate is not nil, and is not the most feasible candidate",
			nodes: []*v1.Node{
				testinghelper.MakeNode().Name("n").Capacity(map[v1.ResourceName]string{"cpu": "10"}).Obj(),
				testinghelper.MakeNode().Name("n1").Capacity(map[v1.ResourceName]string{"cpu": "10"}).Obj(),
				testinghelper.MakeNode().Name("n2").Capacity(map[v1.ResourceName]string{"cpu": "10"}).Obj(),
			},
			existingPods: []*v1.Pod{
				testinghelper.MakePod().Name("p").UID("p").Node("n").Priority(11).Req(map[v1.ResourceName]string{"cpu": "10"}).Obj(),
				testinghelper.MakePod().Name("p1").UID("p1").Node("n1").Priority(10).Req(map[v1.ResourceName]string{"cpu": "10"}).Obj(),
				testinghelper.MakePod().Name("p2").UID("p2").Node("n2").Priority(11).Req(map[v1.ResourceName]string{"cpu": "10"}).Obj(),
			},
			preemptor: testinghelper.MakePod().Name("foo").Priority(20).Req(map[v1.ResourceName]string{"cpu": "10"}).
				Annotation(constraints.HardConstraintsAnnotationKey, noderesources.FitName).Obj(),
			candidate: &framework.Candidate{
				Name: "n",
				Victims: &framework.Victims{
					Pods: []*v1.Pod{
						testinghelper.MakePod().Name("p").UID("p").Node("n").Priority(11).Req(map[v1.ResourceName]string{"cpu": "10"}).Obj(),
					},
					PreemptionState: framework.NewCycleState(),
				},
			},
			candidates: []*framework.Candidate{
				{
					Name: "n1",
					Victims: &framework.Victims{
						Pods: []*v1.Pod{
							testinghelper.MakePod().Name("p1").UID("p1").Node("n1").Priority(10).Req(map[v1.ResourceName]string{"cpu": "10"}).Obj(),
						},
						PreemptionState: framework.NewCycleState(),
					},
				},
				{
					Name: "n2",
					Victims: &framework.Victims{
						Pods: []*v1.Pod{
							testinghelper.MakePod().Name("p2").UID("p2").Node("n2").Priority(11).Req(map[v1.ResourceName]string{"cpu": "10"}).Obj(),
						},
						PreemptionState: framework.NewCycleState(),
					},
				},
			},
			expectedNode:                 "n1",
			expectedUsedNominatedNode:    "n1",
			expectedUnusedNominatedNodes: []string{"n", "n2"},
		},
		{
			name: "candidate is not nil, is the worst candidate",
			nodes: []*v1.Node{
				testinghelper.MakeNode().Name("n").Capacity(map[v1.ResourceName]string{"cpu": "10"}).Obj(),
				testinghelper.MakeNode().Name("n1").Capacity(map[v1.ResourceName]string{"cpu": "10"}).Obj(),
				testinghelper.MakeNode().Name("n2").Capacity(map[v1.ResourceName]string{"cpu": "10"}).Obj(),
			},
			existingPods: []*v1.Pod{
				testinghelper.MakePod().Name("p").UID("p").Node("n").Priority(12).Req(map[v1.ResourceName]string{"cpu": "10"}).Obj(),
				testinghelper.MakePod().Name("p1").UID("p1").Node("n1").Priority(10).Req(map[v1.ResourceName]string{"cpu": "10"}).Obj(),
				testinghelper.MakePod().Name("p2").UID("p2").Node("n2").Priority(11).Req(map[v1.ResourceName]string{"cpu": "10"}).Obj(),
			},
			preemptor: testinghelper.MakePod().Name("foo").Priority(20).Req(map[v1.ResourceName]string{"cpu": "10"}).
				Annotation(constraints.HardConstraintsAnnotationKey, noderesources.FitName).Obj(),
			candidate: &framework.Candidate{
				Name: "n",
				Victims: &framework.Victims{
					Pods: []*v1.Pod{
						testinghelper.MakePod().Name("p").UID("p").Node("n").Priority(12).Req(map[v1.ResourceName]string{"cpu": "10"}).Obj(),
					},
					PreemptionState: framework.NewCycleState(),
				},
			},
			candidates: []*framework.Candidate{
				{
					Name: "n1",
					Victims: &framework.Victims{
						Pods: []*v1.Pod{
							testinghelper.MakePod().Name("p1").UID("p1").Node("n1").Priority(10).Req(map[v1.ResourceName]string{"cpu": "10"}).Obj(),
						},
						PreemptionState: framework.NewCycleState(),
					},
				},
				{
					Name: "n2",
					Victims: &framework.Victims{
						Pods: []*v1.Pod{
							testinghelper.MakePod().Name("p2").UID("p2").Node("n2").Priority(11).Req(map[v1.ResourceName]string{"cpu": "10"}).Obj(),
						},
						PreemptionState: framework.NewCycleState(),
					},
				},
			},
			expectedNode:                 "n1",
			expectedUsedNominatedNode:    "n1",
			expectedUnusedNominatedNodes: []string{"n2"},
		},
		{
			name: "candidate is not nil, all candidates are not feasible",
			nodes: []*v1.Node{
				testinghelper.MakeNode().Name("n").Capacity(map[v1.ResourceName]string{"cpu": "10"}).Obj(),
				testinghelper.MakeNode().Name("n1").Capacity(map[v1.ResourceName]string{"cpu": "10"}).Obj(),
				testinghelper.MakeNode().Name("n2").Capacity(map[v1.ResourceName]string{"cpu": "10"}).Obj(),
			},
			existingPods: []*v1.Pod{
				testinghelper.MakePod().Name("p").UID("p").Node("n").Priority(12).Req(map[v1.ResourceName]string{"cpu": "10"}).Obj(),
				testinghelper.MakePod().Name("p1").UID("p1").Node("n1").Priority(10).Req(map[v1.ResourceName]string{"cpu": "10"}).Obj(),
				testinghelper.MakePod().Name("p2").UID("p2").Node("n2").Priority(11).Req(map[v1.ResourceName]string{"cpu": "10"}).Obj(),
			},
			preemptor: testinghelper.MakePod().Name("foo").Priority(20).Req(map[v1.ResourceName]string{"cpu": "15"}).
				Annotation(constraints.HardConstraintsAnnotationKey, noderesources.FitName).Obj(),
			candidate: &framework.Candidate{
				Name: "n",
				Victims: &framework.Victims{
					Pods: []*v1.Pod{
						testinghelper.MakePod().Name("p").UID("p").Node("n").Priority(12).Req(map[v1.ResourceName]string{"cpu": "10"}).Obj(),
					},
					PreemptionState: framework.NewCycleState(),
				},
			},
			candidates: []*framework.Candidate{
				{
					Name: "n1",
					Victims: &framework.Victims{
						Pods: []*v1.Pod{
							testinghelper.MakePod().Name("p1").UID("p1").Node("n1").Priority(10).Req(map[v1.ResourceName]string{"cpu": "10"}).Obj(),
						},
						PreemptionState: framework.NewCycleState(),
					},
				},
				{
					Name: "n2",
					Victims: &framework.Victims{
						Pods: []*v1.Pod{
							testinghelper.MakePod().Name("p2").UID("p2").Node("n2").Priority(11).Req(map[v1.ResourceName]string{"cpu": "10"}).Obj(),
						},
						PreemptionState: framework.NewCycleState(),
					},
				},
			},
			expectedNode:                 "",
			expectedUsedNominatedNode:    "",
			expectedUnusedNominatedNodes: nil,
		},
		{
			name: "candidate is nil, the first one is the most feasible candidate",
			nodes: []*v1.Node{
				testinghelper.MakeNode().Name("n1").Capacity(map[v1.ResourceName]string{"cpu": "10"}).Obj(),
				testinghelper.MakeNode().Name("n2").Capacity(map[v1.ResourceName]string{"cpu": "10"}).Obj(),
			},
			existingPods: []*v1.Pod{
				testinghelper.MakePod().Name("p1").UID("p1").Node("n1").Priority(10).Req(map[v1.ResourceName]string{"cpu": "10"}).Obj(),
				testinghelper.MakePod().Name("p2").UID("p2").Node("n2").Priority(11).Req(map[v1.ResourceName]string{"cpu": "10"}).Obj(),
			},
			preemptor: testinghelper.MakePod().Name("foo").Priority(20).Req(map[v1.ResourceName]string{"cpu": "10"}).
				Annotation(constraints.HardConstraintsAnnotationKey, noderesources.FitName).Obj(),
			candidates: []*framework.Candidate{
				{
					Name: "n1",
					Victims: &framework.Victims{
						Pods: []*v1.Pod{
							testinghelper.MakePod().Name("p1").UID("p1").Node("n1").Priority(10).Req(map[v1.ResourceName]string{"cpu": "10"}).Obj(),
						},
						PreemptionState: framework.NewCycleState(),
					},
				},
				{
					Name: "n2",
					Victims: &framework.Victims{
						Pods: []*v1.Pod{
							testinghelper.MakePod().Name("p2").UID("p2").Node("n2").Priority(11).Req(map[v1.ResourceName]string{"cpu": "10"}).Obj(),
						},
						PreemptionState: framework.NewCycleState(),
					},
				},
			},
			expectedNode:                 "n1",
			expectedUsedNominatedNode:    "n1",
			expectedUnusedNominatedNodes: []string{"n2"},
		},
		{
			name: "candidate is nil, the first one is not the most feasible candidate",
			nodes: []*v1.Node{
				testinghelper.MakeNode().Name("n1").Capacity(map[v1.ResourceName]string{"cpu": "10"}).Obj(),
				testinghelper.MakeNode().Name("n2").Capacity(map[v1.ResourceName]string{"cpu": "10"}).Obj(),
			},
			existingPods: []*v1.Pod{
				testinghelper.MakePod().Name("p1").UID("p1").Node("n1").Priority(10).Req(map[v1.ResourceName]string{"cpu": "0"}).Obj(),
				testinghelper.MakePod().Name("p3").UID("p3").Node("n1").Priority(12).Req(map[v1.ResourceName]string{"cpu": "10"}).Obj(),
				testinghelper.MakePod().Name("p2").UID("p2").Node("n2").Priority(11).Req(map[v1.ResourceName]string{"cpu": "10"}).Obj(),
			},
			preemptor: testinghelper.MakePod().Name("foo").Priority(20).Req(map[v1.ResourceName]string{"cpu": "10"}).
				Annotation(constraints.HardConstraintsAnnotationKey, noderesources.FitName).Obj(),
			candidates: []*framework.Candidate{
				{
					Name: "n1",
					Victims: &framework.Victims{
						Pods: []*v1.Pod{
							testinghelper.MakePod().Name("p1").UID("p1").Node("n1").Priority(10).Req(map[v1.ResourceName]string{"cpu": "0"}).Obj(),
						},
						PreemptionState: framework.NewCycleState(),
					},
				},
				{
					Name: "n2",
					Victims: &framework.Victims{
						Pods: []*v1.Pod{
							testinghelper.MakePod().Name("p2").UID("p2").Node("n2").Priority(11).Req(map[v1.ResourceName]string{"cpu": "10"}).Obj(),
						},
						PreemptionState: framework.NewCycleState(),
					},
				},
			},
			expectedNode:                 "n2",
			expectedUsedNominatedNode:    "",
			expectedUnusedNominatedNodes: nil,
		},
		{
			name: "candidate is nil, the first one is not feasible",
			nodes: []*v1.Node{
				testinghelper.MakeNode().Name("n1").Capacity(map[v1.ResourceName]string{"cpu": "10"}).Obj(),
				testinghelper.MakeNode().Name("n2").Capacity(map[v1.ResourceName]string{"cpu": "15"}).Obj(),
			},
			existingPods: []*v1.Pod{
				testinghelper.MakePod().Name("p1").UID("p1").Node("n1").Priority(10).Req(map[v1.ResourceName]string{"cpu": "10"}).Obj(),
				testinghelper.MakePod().Name("p2").UID("p2").Node("n2").Priority(11).Req(map[v1.ResourceName]string{"cpu": "10"}).Obj(),
			},
			preemptor: testinghelper.MakePod().Name("foo").Priority(20).Req(map[v1.ResourceName]string{"cpu": "15"}).
				Annotation(constraints.HardConstraintsAnnotationKey, noderesources.FitName).Obj(),
			candidates: []*framework.Candidate{
				{
					Name: "n1",
					Victims: &framework.Victims{
						Pods: []*v1.Pod{
							testinghelper.MakePod().Name("p1").UID("p1").Node("n1").Priority(10).Req(map[v1.ResourceName]string{"cpu": "10"}).Obj(),
						},
						PreemptionState: framework.NewCycleState(),
					},
				},
				{
					Name: "n2",
					Victims: &framework.Victims{
						Pods: []*v1.Pod{
							testinghelper.MakePod().Name("p2").UID("p2").Node("n2").Priority(11).Req(map[v1.ResourceName]string{"cpu": "10"}).Obj(),
						},
						PreemptionState: framework.NewCycleState(),
					},
				},
			},
			expectedNode:                 "n2",
			expectedUsedNominatedNode:    "",
			expectedUnusedNominatedNodes: nil,
		},
		{
			name: "candidate is nil, first one is worst, second one is not feasible",
			nodes: []*v1.Node{
				testinghelper.MakeNode().Name("n1").Capacity(map[v1.ResourceName]string{"cpu": "10"}).Obj(),
				testinghelper.MakeNode().Name("n2").Capacity(map[v1.ResourceName]string{"cpu": "10"}).Obj(),
			},
			existingPods: []*v1.Pod{
				testinghelper.MakePod().Name("p1").UID("p1").Node("n1").Priority(12).Req(map[v1.ResourceName]string{"cpu": "10"}).Obj(),
				testinghelper.MakePod().Name("p2").UID("p2").Node("n2").Priority(11).Req(map[v1.ResourceName]string{"cpu": "0"}).Obj(),
				testinghelper.MakePod().Name("p3").UID("p3").Node("n2").Priority(20).Req(map[v1.ResourceName]string{"cpu": "10"}).Obj(),
			},
			preemptor: testinghelper.MakePod().Name("foo").Priority(20).Req(map[v1.ResourceName]string{"cpu": "10"}).
				Annotation(constraints.HardConstraintsAnnotationKey, noderesources.FitName).Obj(),
			candidates: []*framework.Candidate{
				{
					Name: "n1",
					Victims: &framework.Victims{
						Pods: []*v1.Pod{
							testinghelper.MakePod().Name("p1").UID("p1").Node("n1").Priority(12).Req(map[v1.ResourceName]string{"cpu": "10"}).Obj(),
						},
						PreemptionState: framework.NewCycleState(),
					},
				},
				{
					Name: "n2",
					Victims: &framework.Victims{
						Pods: []*v1.Pod{
							testinghelper.MakePod().Name("p2").UID("p2").Node("n2").Priority(11).Req(map[v1.ResourceName]string{"cpu": "0"}).Obj(),
						},
						PreemptionState: framework.NewCycleState(),
					},
				},
			},
			expectedNode:                 "",
			expectedUsedNominatedNode:    "",
			expectedUnusedNominatedNodes: nil,
		},
		{
			name: "candidate is nil, first one is worst, second one is worst too",
			nodes: []*v1.Node{
				testinghelper.MakeNode().Name("n1").Capacity(map[v1.ResourceName]string{"cpu": "10"}).Obj(),
				testinghelper.MakeNode().Name("n2").Capacity(map[v1.ResourceName]string{"cpu": "10"}).Obj(),
			},
			existingPods: []*v1.Pod{
				testinghelper.MakePod().Name("p1").UID("p1").Node("n1").Priority(12).Req(map[v1.ResourceName]string{"cpu": "10"}).Obj(),
				testinghelper.MakePod().Name("p2").UID("p2").Node("n2").Priority(11).Req(map[v1.ResourceName]string{"cpu": "0"}).Obj(),
				testinghelper.MakePod().Name("p3").UID("p3").Node("n2").Priority(12).Req(map[v1.ResourceName]string{"cpu": "10"}).Obj(),
			},
			preemptor: testinghelper.MakePod().Name("foo").Priority(20).Req(map[v1.ResourceName]string{"cpu": "10"}).
				Annotation(constraints.HardConstraintsAnnotationKey, noderesources.FitName).Obj(),
			candidates: []*framework.Candidate{
				{
					Name: "n1",
					Victims: &framework.Victims{
						Pods: []*v1.Pod{
							testinghelper.MakePod().Name("p1").UID("p1").Node("n1").Priority(12).Req(map[v1.ResourceName]string{"cpu": "10"}).Obj(),
						},
						PreemptionState: framework.NewCycleState(),
					},
				},
				{
					Name: "n2",
					Victims: &framework.Victims{
						Pods: []*v1.Pod{
							testinghelper.MakePod().Name("p2").UID("p2").Node("n2").Priority(11).Req(map[v1.ResourceName]string{"cpu": "0"}).Obj(),
						},
						PreemptionState: framework.NewCycleState(),
					},
				},
			},
			expectedNode:                 "",
			expectedUsedNominatedNode:    "",
			expectedUnusedNominatedNodes: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cache := godelcache.New(handler.MakeCacheHandlerWrapper().
				SchedulerName("").SchedulerType("").SubCluster(framework.DefaultSubCluster).
				TTL(time.Second).Period(10 * time.Second).StopCh(make(<-chan struct{})).
				EnableStore("PreemptionStore").
				Obj())
			snapshot := godelcache.NewEmptySnapshot(handler.MakeCacheHandlerWrapper().
				SubCluster(framework.DefaultSubCluster).SwitchType(framework.DefaultSubClusterSwitchType).
				EnableStore("PreemptionStore").
				Obj())
			for _, n := range tt.nodes {
				cache.AddNode(n)
			}
			for _, p := range tt.existingPods {
				cache.AddPod(p)
			}
			cache.UpdateSnapshot(snapshot)

			crdClient := godelclientfake.NewSimpleClientset()
			crdInformerFactory := crdinformers.NewSharedInformerFactory(crdClient, 0)
			client := clientsetfake.NewSimpleClientset()
			informerFactory := informers.NewSharedInformerFactory(client, 0)
			fh, _ := st.NewSchedulerFrameworkHandle(client, crdClient, informerFactory, crdInformerFactory, cache, snapshot, nil, nil, nil, nil)
			nodeResourcesPlugin, err := noderesources.NewFit(nil, fh)
			if err != nil {
				t.Errorf("Could not new nonnative topology plugin: %v", err)
			}

			registry := schedulerframework.NewInTreePreemptionRegistry()
			preemptionPluginRegistry, err := schedulerframework.NewPluginsRegistry(registry, nil, fh)
			if err != nil {
				t.Errorf("failed to new plugins registry: %v", err)
			}
			basePlugins := newPreemptionBasedPlugins()
			fh, _ = st.NewSchedulerFrameworkHandle(nil, nil, nil, nil, cache, snapshot, framework.PluginMap{noderesources.FitName: nodeResourcesPlugin}, preemptionPluginRegistry, nil, basePlugins[string(podutil.Kubelet)])

			fwk, err := fh.GetFrameworkForPod(tt.preemptor)
			state, err := fwk.InitCycleState(tt.preemptor)
			if err != nil {
				t.Fatal(err)
			}
			pfwk := fh.GetPreemptionFrameworkForPod(tt.preemptor)
			gs := &podScheduler{
				basePlugins: basePlugins,
				snapshot:    snapshot,
			}
			cachedNominatdNodes := framework.CachedNominatedNodes{}
			cachedNominatdNodes.SetPodCount(10)

			fwk.RunPreFilterPlugins(context.Background(), state, tt.preemptor)
			pfwk.RunClusterPrePreemptingPlugins(tt.preemptor, state, framework.NewCycleState())
			s := gs.SelectCandidate(context.Background(), fwk, pfwk, state, tt.preemptor, tt.candidates, tt.candidate, &cachedNominatdNodes, true)
			if s == nil {
				if len(tt.expectedNode) > 0 {
					t.Errorf("expect node %v, but got nil", tt.expectedNode)
				}
			} else {
				if tt.expectedNode != s.Name {
					t.Errorf("expect node %v, but got %v", tt.expectedNode, s.Name)
				}
			}

			if tt.expectedUsedNominatedNode == "" {
				if cachedNominatdNodes.GetUsedNominatedNode() != nil {
					t.Errorf("expected nil used nominated node but got %s", cachedNominatdNodes.GetUsedNominatedNode().Name)
				}
			} else {
				if cachedNominatdNodes.GetUsedNominatedNode() == nil {
					t.Errorf("expected used nominated %s node but got nil", tt.expectedUsedNominatedNode)
				} else if tt.expectedUsedNominatedNode != cachedNominatdNodes.GetUsedNominatedNode().Name {
					t.Errorf("expected %v, but got %v", tt.expectedUsedNominatedNode, cachedNominatdNodes.GetUsedNominatedNode().Name)
				}
			}

			var gotUsedNodes []string
			for _, c := range cachedNominatdNodes.GetUnusedNominatedNodes() {
				gotUsedNodes = append(gotUsedNodes, c.Name)
			}
			if !reflect.DeepEqual(tt.expectedUnusedNominatedNodes, gotUsedNodes) {
				t.Errorf("expected %v but got %v", tt.expectedUnusedNominatedNodes, gotUsedNodes)
			}
		})
	}
}

func TestFindCandidates(t *testing.T) {
	tests := []struct {
		name               string
		pod                *v1.Pod
		existingPods       []*v1.Pod
		node               *v1.Node
		cnr                *katalystv1alpha1.CustomNodeResource
		pdbs               []*policy.PodDisruptionBudget
		enableColocation   bool
		expectedVictims    []*v1.Pod
		expectedPDBAllowed []int32
	}{
		{
			name: "first half and second half",
			pod: testinghelper.MakePod().Namespace("default").Name("p").UID("p").Req(map[v1.ResourceName]string{v1.ResourceCPU: "5"}).Priority(20).
				Annotation(constraints.HardConstraintsAnnotationKey, noderesources.FitName).Obj(),
			existingPods: []*v1.Pod{
				testinghelper.MakePod().Namespace("default").Name("p1").UID("p1").Node("n").Req(map[v1.ResourceName]string{v1.ResourceCPU: "2"}).Priority(10).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).
					Label("key", "val").Obj(),
				testinghelper.MakePod().Namespace("default").Name("p2").UID("p2").Node("n").Req(map[v1.ResourceName]string{v1.ResourceCPU: "2"}).Priority(11).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).Obj(),
				testinghelper.MakePod().Namespace("default").Name("p3").UID("p3").Node("n").Req(map[v1.ResourceName]string{v1.ResourceCPU: "3"}).Priority(12).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).
					Label("key", "val").Obj(),
				testinghelper.MakePod().Namespace("default").Name("p4").UID("p4").Node("n").Req(map[v1.ResourceName]string{v1.ResourceCPU: "3"}).Priority(13).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).Obj(),
			},
			node: testinghelper.MakeNode().Name("n").Capacity(map[v1.ResourceName]string{v1.ResourceCPU: "10"}).Obj(),
			pdbs: []*policy.PodDisruptionBudget{
				testinghelper.MakePdb().Namespace("default").Name("pdb").DisruptionsAllowed(1).
					Label("key", "val").Obj(),
			},
			expectedVictims: []*v1.Pod{
				testinghelper.MakePod().Namespace("default").Name("p4").UID("p4").Node("n").Req(map[v1.ResourceName]string{v1.ResourceCPU: "3"}).Priority(13).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).Obj(),
				testinghelper.MakePod().Namespace("default").Name("p1").UID("p1").Node("n").Req(map[v1.ResourceName]string{v1.ResourceCPU: "2"}).Priority(10).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).
					Label("key", "val").Obj(),
			},
			expectedPDBAllowed: []int32{0},
		},
		{
			name: "first half",
			pod: testinghelper.MakePod().Namespace("default").Name("p").UID("p").Req(map[v1.ResourceName]string{v1.ResourceCPU: "4"}).Priority(20).
				Annotation(constraints.HardConstraintsAnnotationKey, noderesources.FitName).Obj(),
			existingPods: []*v1.Pod{
				testinghelper.MakePod().Namespace("default").Name("p1").UID("p1").Node("n").Req(map[v1.ResourceName]string{v1.ResourceCPU: "2"}).Priority(10).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).
					Label("key", "val").Obj(),
				testinghelper.MakePod().Namespace("default").Name("p2").UID("p2").Node("n").Req(map[v1.ResourceName]string{v1.ResourceCPU: "2"}).Priority(11).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).Obj(),
				testinghelper.MakePod().Namespace("default").Name("p3").UID("p3").Node("n").Req(map[v1.ResourceName]string{v1.ResourceCPU: "3"}).Priority(12).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).
					Label("key", "val").Obj(),
				testinghelper.MakePod().Namespace("default").Name("p4").UID("p4").Node("n").Req(map[v1.ResourceName]string{v1.ResourceCPU: "3"}).Priority(13).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).Obj(),
			},
			node: testinghelper.MakeNode().Name("n").Capacity(map[v1.ResourceName]string{v1.ResourceCPU: "10"}).Obj(),
			pdbs: []*policy.PodDisruptionBudget{
				testinghelper.MakePdb().Namespace("default").Name("pdb").DisruptionsAllowed(1).
					Label("key", "val").Obj(),
			},
			expectedVictims: []*v1.Pod{
				testinghelper.MakePod().Namespace("default").Name("p2").UID("p2").Node("n").Req(map[v1.ResourceName]string{v1.ResourceCPU: "2"}).Priority(11).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).Obj(),
				testinghelper.MakePod().Namespace("default").Name("p1").UID("p1").Node("n").Req(map[v1.ResourceName]string{v1.ResourceCPU: "2"}).Priority(10).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).
					Label("key", "val").Obj(),
			},
			expectedPDBAllowed: []int32{0},
		},
		{
			name: "second half",
			pod: testinghelper.MakePod().Namespace("default").Name("p").UID("p").Req(map[v1.ResourceName]string{v1.ResourceCPU: "8"}).Priority(20).
				Annotation(constraints.HardConstraintsAnnotationKey, noderesources.FitName).Obj(),
			existingPods: []*v1.Pod{
				testinghelper.MakePod().Namespace("default").Name("p1").UID("p1").Node("n").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(10).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).
					Label("key", "val").Obj(),
				testinghelper.MakePod().Namespace("default").Name("p2").UID("p2").Node("n").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(11).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).Obj(),
				testinghelper.MakePod().Namespace("default").Name("p3").UID("p3").Node("n").Req(map[v1.ResourceName]string{v1.ResourceCPU: "4"}).Priority(12).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).
					Label("key", "val").Obj(),
				testinghelper.MakePod().Namespace("default").Name("p4").UID("p4").Node("n").Req(map[v1.ResourceName]string{v1.ResourceCPU: "4"}).Priority(13).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).Obj(),
			},
			node: testinghelper.MakeNode().Name("n").Capacity(map[v1.ResourceName]string{v1.ResourceCPU: "10"}).Obj(),
			pdbs: []*policy.PodDisruptionBudget{
				testinghelper.MakePdb().Namespace("default").Name("pdb").DisruptionsAllowed(2).
					Label("key", "val").Obj(),
			},
			expectedVictims: []*v1.Pod{
				testinghelper.MakePod().Namespace("default").Name("p4").UID("p4").Node("n").Req(map[v1.ResourceName]string{v1.ResourceCPU: "4"}).Priority(13).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).Obj(),
				testinghelper.MakePod().Namespace("default").Name("p3").UID("p3").Node("n").Req(map[v1.ResourceName]string{v1.ResourceCPU: "4"}).Priority(12).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).
					Label("key", "val").Obj(),
			},
			expectedPDBAllowed: []int32{0},
		},
		{
			name: "first quarter",
			pod: testinghelper.MakePod().Namespace("default").Name("p").UID("p").Req(map[v1.ResourceName]string{v1.ResourceCPU: "2"}).Priority(20).
				Annotation(constraints.HardConstraintsAnnotationKey, noderesources.FitName).Obj(),
			existingPods: []*v1.Pod{
				testinghelper.MakePod().Namespace("default").Name("p1").UID("p1").Node("n").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(10).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).
					Label("key", "val").Obj(),
				testinghelper.MakePod().Namespace("default").Name("p2").UID("p2").Node("n").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(11).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).Obj(),
				testinghelper.MakePod().Namespace("default").Name("p3").UID("p3").Node("n").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(12).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).
					Label("key", "val").Obj(),
				testinghelper.MakePod().Namespace("default").Name("p4").UID("p4").Node("n").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(13).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).Obj(),
				testinghelper.MakePod().Namespace("default").Name("p5").UID("p5").Node("n").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(14).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).Obj(),
				testinghelper.MakePod().Namespace("default").Name("p6").UID("p6").Node("n").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(15).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).Obj(),
				testinghelper.MakePod().Namespace("default").Name("p7").UID("p7").Node("n").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(16).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).Obj(),
				testinghelper.MakePod().Namespace("default").Name("p8").UID("p8").Node("n").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(17).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).Obj(),
			},
			node: testinghelper.MakeNode().Name("n").Capacity(map[v1.ResourceName]string{v1.ResourceCPU: "8"}).Obj(),
			pdbs: []*policy.PodDisruptionBudget{
				testinghelper.MakePdb().Namespace("default").Name("pdb").DisruptionsAllowed(2).
					Label("key", "val").Obj(),
			},
			expectedVictims: []*v1.Pod{
				testinghelper.MakePod().Namespace("default").Name("p2").UID("p2").Node("n").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(11).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).Obj(),
				testinghelper.MakePod().Namespace("default").Name("p1").UID("p1").Node("n").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(10).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).
					Label("key", "val").Obj(),
			},
			expectedPDBAllowed: []int32{0},
		},
		{
			name: "first and second quarter",
			pod: testinghelper.MakePod().Namespace("default").Name("p").UID("p").Req(map[v1.ResourceName]string{v1.ResourceCPU: "2"}).Priority(20).
				Annotation(constraints.HardConstraintsAnnotationKey, noderesources.FitName).Obj(),
			existingPods: []*v1.Pod{
				testinghelper.MakePod().Namespace("default").Name("p1").UID("p1").Node("n").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(10).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).
					Label("key", "val").Obj(),
				testinghelper.MakePod().Namespace("default").Name("p2").UID("p2").Node("n").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(11).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).
					Label("key", "val").Obj(),
				testinghelper.MakePod().Namespace("default").Name("p3").UID("p3").Node("n").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(12).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).
					Label("key", "val").Obj(),
				testinghelper.MakePod().Namespace("default").Name("p4").UID("p4").Node("n").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(13).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).Obj(),
				testinghelper.MakePod().Namespace("default").Name("p5").UID("p5").Node("n").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(14).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).Obj(),
				testinghelper.MakePod().Namespace("default").Name("p6").UID("p6").Node("n").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(15).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).Obj(),
				testinghelper.MakePod().Namespace("default").Name("p7").UID("p7").Node("n").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(16).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).Obj(),
				testinghelper.MakePod().Namespace("default").Name("p8").UID("p8").Node("n").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(17).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).Obj(),
			},
			node: testinghelper.MakeNode().Name("n").Capacity(map[v1.ResourceName]string{v1.ResourceCPU: "8"}).Obj(),
			pdbs: []*policy.PodDisruptionBudget{
				testinghelper.MakePdb().Namespace("default").Name("pdb").DisruptionsAllowed(1).
					Label("key", "val").Obj(),
			},
			expectedVictims: []*v1.Pod{
				testinghelper.MakePod().Namespace("default").Name("p4").UID("p4").Node("n").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(13).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).Obj(),
				testinghelper.MakePod().Namespace("default").Name("p1").UID("p1").Node("n").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(10).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).
					Label("key", "val").Obj(),
			},
			expectedPDBAllowed: []int32{0},
		},
		{
			name: "first and third quarter",
			pod: testinghelper.MakePod().Namespace("default").Name("p").UID("p").Req(map[v1.ResourceName]string{v1.ResourceCPU: "2"}).Priority(20).
				Annotation(constraints.HardConstraintsAnnotationKey, noderesources.FitName).Obj(),
			existingPods: []*v1.Pod{
				testinghelper.MakePod().Namespace("default").Name("p1").UID("p1").Node("n").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(10).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).
					Label("key", "val").Obj(),
				testinghelper.MakePod().Namespace("default").Name("p2").UID("p2").Node("n").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(11).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).
					Label("key", "val").Obj(),
				testinghelper.MakePod().Namespace("default").Name("p3").UID("p3").Node("n").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(12).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).
					Label("key", "val").Obj(),
				testinghelper.MakePod().Namespace("default").Name("p4").UID("p4").Node("n").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(13).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).
					Label("key", "val").Obj(),
				testinghelper.MakePod().Namespace("default").Name("p5").UID("p5").Node("n").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(14).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).Obj(),
				testinghelper.MakePod().Namespace("default").Name("p6").UID("p6").Node("n").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(15).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).Obj(),
				testinghelper.MakePod().Namespace("default").Name("p7").UID("p7").Node("n").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(16).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).Obj(),
				testinghelper.MakePod().Namespace("default").Name("p8").UID("p8").Node("n").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(17).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).Obj(),
			},
			node: testinghelper.MakeNode().Name("n").Capacity(map[v1.ResourceName]string{v1.ResourceCPU: "8"}).Obj(),
			pdbs: []*policy.PodDisruptionBudget{
				testinghelper.MakePdb().Namespace("default").Name("pdb").DisruptionsAllowed(1).
					Label("key", "val").Obj(),
			},
			expectedVictims: []*v1.Pod{
				testinghelper.MakePod().Namespace("default").Name("p5").UID("p5").Node("n").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(14).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).Obj(),
				testinghelper.MakePod().Namespace("default").Name("p1").UID("p1").Node("n").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(10).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).
					Label("key", "val").Obj(),
			},
			expectedPDBAllowed: []int32{0},
		},
		{
			name: "first and forth quarter",
			pod: testinghelper.MakePod().Namespace("default").Name("p").UID("p").Req(map[v1.ResourceName]string{v1.ResourceCPU: "2"}).Priority(20).
				Annotation(constraints.HardConstraintsAnnotationKey, noderesources.FitName).Obj(),
			existingPods: []*v1.Pod{
				testinghelper.MakePod().Namespace("default").Name("p1").UID("p1").Node("n").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(10).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).
					Label("key", "val").Obj(),
				testinghelper.MakePod().Namespace("default").Name("p2").UID("p2").Node("n").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(11).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).
					Label("key", "val").Obj(),
				testinghelper.MakePod().Namespace("default").Name("p3").UID("p3").Node("n").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(12).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).
					Label("key", "val").Obj(),
				testinghelper.MakePod().Namespace("default").Name("p4").UID("p4").Node("n").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(13).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).
					Label("key", "val").Obj(),
				testinghelper.MakePod().Namespace("default").Name("p5").UID("p5").Node("n").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(14).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).
					Label("key", "val").Obj(),
				testinghelper.MakePod().Namespace("default").Name("p6").UID("p6").Node("n").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(15).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).
					Label("key", "val").Obj(),
				testinghelper.MakePod().Namespace("default").Name("p7").UID("p7").Node("n").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(16).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).Obj(),
				testinghelper.MakePod().Namespace("default").Name("p8").UID("p8").Node("n").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(17).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).Obj(),
			},
			node: testinghelper.MakeNode().Name("n").Capacity(map[v1.ResourceName]string{v1.ResourceCPU: "8"}).Obj(),
			pdbs: []*policy.PodDisruptionBudget{
				testinghelper.MakePdb().Namespace("default").Name("pdb").DisruptionsAllowed(1).
					Label("key", "val").Obj(),
			},
			expectedVictims: []*v1.Pod{
				testinghelper.MakePod().Namespace("default").Name("p7").UID("p7").Node("n").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(16).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).Obj(),
				testinghelper.MakePod().Namespace("default").Name("p1").UID("p1").Node("n").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(10).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).
					Label("key", "val").Obj(),
			},
			expectedPDBAllowed: []int32{0},
		},
		{
			name: "second quarter",
			pod: testinghelper.MakePod().Namespace("default").Name("p").UID("p").Req(map[v1.ResourceName]string{v1.ResourceCPU: "2"}).Priority(20).
				Annotation(constraints.HardConstraintsAnnotationKey, noderesources.FitName).Obj(),
			existingPods: []*v1.Pod{
				testinghelper.MakePod().Namespace("default").Name("p1").UID("p1").Node("n").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(10).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).
					Label("key", "val").Obj(),
				testinghelper.MakePod().Namespace("default").Name("p2").UID("p2").Node("n").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(11).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CannotBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).Obj(),
				testinghelper.MakePod().Namespace("default").Name("p3").UID("p3").Node("n").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(12).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).Obj(),
				testinghelper.MakePod().Namespace("default").Name("p4").UID("p4").Node("n").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(13).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).Obj(),
				testinghelper.MakePod().Namespace("default").Name("p5").UID("p5").Node("n").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(14).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).Obj(),
				testinghelper.MakePod().Namespace("default").Name("p6").UID("p6").Node("n").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(15).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).Obj(),
				testinghelper.MakePod().Namespace("default").Name("p7").UID("p7").Node("n").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(16).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).Obj(),
				testinghelper.MakePod().Namespace("default").Name("p8").UID("p8").Node("n").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(17).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).Obj(),
			},
			node: testinghelper.MakeNode().Name("n").Capacity(map[v1.ResourceName]string{v1.ResourceCPU: "8"}).Obj(),
			pdbs: []*policy.PodDisruptionBudget{
				testinghelper.MakePdb().Namespace("default").Name("pdb").DisruptionsAllowed(0).
					Label("key", "val").Obj(),
			},
			expectedVictims: []*v1.Pod{
				testinghelper.MakePod().Namespace("default").Name("p4").UID("p4").Node("n").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(13).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).Obj(),
				testinghelper.MakePod().Namespace("default").Name("p3").UID("p3").Node("n").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(12).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).Obj(),
			},
			expectedPDBAllowed: []int32{0},
		},
		{
			name: "second and third quarter",
			pod: testinghelper.MakePod().Namespace("default").Name("p").UID("p").Req(map[v1.ResourceName]string{v1.ResourceCPU: "2"}).Priority(20).
				Annotation(constraints.HardConstraintsAnnotationKey, noderesources.FitName).Obj(),
			existingPods: []*v1.Pod{
				testinghelper.MakePod().Namespace("default").Name("p1").UID("p1").Node("n").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(10).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).
					Label("key", "val").Obj(),
				testinghelper.MakePod().Namespace("default").Name("p2").UID("p2").Node("n").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(11).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CannotBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).Obj(),
				testinghelper.MakePod().Namespace("default").Name("p3").UID("p3").Node("n").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(12).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CannotBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).Obj(),
				testinghelper.MakePod().Namespace("default").Name("p4").UID("p4").Node("n").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(13).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).Obj(),
				testinghelper.MakePod().Namespace("default").Name("p5").UID("p5").Node("n").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(14).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).Obj(),
				testinghelper.MakePod().Namespace("default").Name("p6").UID("p6").Node("n").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(15).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).Obj(),
				testinghelper.MakePod().Namespace("default").Name("p7").UID("p7").Node("n").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(16).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).Obj(),
				testinghelper.MakePod().Namespace("default").Name("p8").UID("p8").Node("n").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(17).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).Obj(),
			},
			node: testinghelper.MakeNode().Name("n").Capacity(map[v1.ResourceName]string{v1.ResourceCPU: "8"}).Obj(),
			pdbs: []*policy.PodDisruptionBudget{
				testinghelper.MakePdb().Namespace("default").Name("pdb").DisruptionsAllowed(0).
					Label("key", "val").Obj(),
			},
			expectedVictims: []*v1.Pod{
				testinghelper.MakePod().Namespace("default").Name("p5").UID("p5").Node("n").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(14).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).Obj(),
				testinghelper.MakePod().Namespace("default").Name("p4").UID("p4").Node("n").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(13).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).Obj(),
			},
			expectedPDBAllowed: []int32{0},
		},
		{
			name: "second and forth quarter",
			pod: testinghelper.MakePod().Namespace("default").Name("p").UID("p").Req(map[v1.ResourceName]string{v1.ResourceCPU: "2"}).Priority(20).
				Annotation(constraints.HardConstraintsAnnotationKey, noderesources.FitName).Obj(),
			existingPods: []*v1.Pod{
				testinghelper.MakePod().Namespace("default").Name("p1").UID("p1").Node("n").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(10).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).
					Label("key", "val").Obj(),
				testinghelper.MakePod().Namespace("default").Name("p2").UID("p2").Node("n").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(11).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CannotBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).Obj(),
				testinghelper.MakePod().Namespace("default").Name("p3").UID("p3").Node("n").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(12).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CannotBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).Obj(),
				testinghelper.MakePod().Namespace("default").Name("p4").UID("p4").Node("n").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(13).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).Obj(),
				testinghelper.MakePod().Namespace("default").Name("p5").UID("p5").Node("n").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(14).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CannotBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).Obj(),
				testinghelper.MakePod().Namespace("default").Name("p6").UID("p6").Node("n").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(15).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CannotBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).Obj(),
				testinghelper.MakePod().Namespace("default").Name("p7").UID("p7").Node("n").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(16).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).Obj(),
				testinghelper.MakePod().Namespace("default").Name("p8").UID("p8").Node("n").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(17).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).Obj(),
			},
			node: testinghelper.MakeNode().Name("n").Capacity(map[v1.ResourceName]string{v1.ResourceCPU: "8"}).Obj(),
			pdbs: []*policy.PodDisruptionBudget{
				testinghelper.MakePdb().Namespace("default").Name("pdb").DisruptionsAllowed(0).
					Label("key", "val").Obj(),
			},
			expectedVictims: []*v1.Pod{
				testinghelper.MakePod().Namespace("default").Name("p7").UID("p7").Node("n").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(16).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).Obj(),
				testinghelper.MakePod().Namespace("default").Name("p4").UID("p4").Node("n").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(13).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).Obj(),
			},
			expectedPDBAllowed: []int32{0},
		},
		{
			name: "third quarter",
			pod: testinghelper.MakePod().Namespace("default").Name("p").UID("p").Req(map[v1.ResourceName]string{v1.ResourceCPU: "2"}).Priority(20).
				Annotation(constraints.HardConstraintsAnnotationKey, noderesources.FitName).Obj(),
			existingPods: []*v1.Pod{
				testinghelper.MakePod().Namespace("default").Name("p1").UID("p1").Node("n").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(10).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).
					Label("key", "val").Obj(),
				testinghelper.MakePod().Namespace("default").Name("p2").UID("p2").Node("n").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(11).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CannotBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).Obj(),
				testinghelper.MakePod().Namespace("default").Name("p3").UID("p3").Node("n").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(12).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CannotBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).Obj(),
				testinghelper.MakePod().Namespace("default").Name("p4").UID("p4").Node("n").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(13).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).
					Label("key", "val").Obj(),
				testinghelper.MakePod().Namespace("default").Name("p5").UID("p5").Node("n").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(14).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).Obj(),
				testinghelper.MakePod().Namespace("default").Name("p6").UID("p6").Node("n").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(15).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).Obj(),
				testinghelper.MakePod().Namespace("default").Name("p7").UID("p7").Node("n").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(16).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).Obj(),
				testinghelper.MakePod().Namespace("default").Name("p8").UID("p8").Node("n").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(17).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).Obj(),
			},
			node: testinghelper.MakeNode().Name("n").Capacity(map[v1.ResourceName]string{v1.ResourceCPU: "8"}).Obj(),
			pdbs: []*policy.PodDisruptionBudget{
				testinghelper.MakePdb().Namespace("default").Name("pdb").DisruptionsAllowed(0).
					Label("key", "val").Obj(),
			},
			expectedVictims: []*v1.Pod{
				testinghelper.MakePod().Namespace("default").Name("p6").UID("p6").Node("n").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(15).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).Obj(),
				testinghelper.MakePod().Namespace("default").Name("p5").UID("p5").Node("n").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(14).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).Obj(),
			},
			expectedPDBAllowed: []int32{0},
		},
		{
			name: "third and forth quarter",
			pod: testinghelper.MakePod().Namespace("default").Name("p").UID("p").Req(map[v1.ResourceName]string{v1.ResourceCPU: "2"}).Priority(20).
				Annotation(constraints.HardConstraintsAnnotationKey, noderesources.FitName).Obj(),
			existingPods: []*v1.Pod{
				testinghelper.MakePod().Namespace("default").Name("p1").UID("p1").Node("n").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(10).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).
					Label("key", "val").Obj(),
				testinghelper.MakePod().Namespace("default").Name("p2").UID("p2").Node("n").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(11).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CannotBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).Obj(),
				testinghelper.MakePod().Namespace("default").Name("p3").UID("p3").Node("n").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(12).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CannotBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).Obj(),
				testinghelper.MakePod().Namespace("default").Name("p4").UID("p4").Node("n").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(13).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).
					Label("key", "val").Obj(),
				testinghelper.MakePod().Namespace("default").Name("p5").UID("p5").Node("n").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(14).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CannotBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).Obj(),
				testinghelper.MakePod().Namespace("default").Name("p6").UID("p6").Node("n").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(15).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).Obj(),
				testinghelper.MakePod().Namespace("default").Name("p7").UID("p7").Node("n").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(16).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).Obj(),
				testinghelper.MakePod().Namespace("default").Name("p8").UID("p8").Node("n").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(17).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).Obj(),
			},
			node: testinghelper.MakeNode().Name("n").Capacity(map[v1.ResourceName]string{v1.ResourceCPU: "8"}).Obj(),
			pdbs: []*policy.PodDisruptionBudget{
				testinghelper.MakePdb().Namespace("default").Name("pdb").DisruptionsAllowed(0).
					Label("key", "val").Obj(),
			},
			expectedVictims: []*v1.Pod{
				testinghelper.MakePod().Namespace("default").Name("p7").UID("p7").Node("n").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(16).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).Obj(),
				testinghelper.MakePod().Namespace("default").Name("p6").UID("p6").Node("n").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(15).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).Obj(),
			},
			expectedPDBAllowed: []int32{0},
		},
		{
			name: "forth quarter",
			pod: testinghelper.MakePod().Namespace("default").Name("p").UID("p").Req(map[v1.ResourceName]string{v1.ResourceCPU: "2"}).Priority(20).
				Annotation(constraints.HardConstraintsAnnotationKey, noderesources.FitName).Obj(),
			existingPods: []*v1.Pod{
				testinghelper.MakePod().Namespace("default").Name("p1").UID("p1").Node("n").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(10).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).
					Label("key", "val").Obj(),
				testinghelper.MakePod().Namespace("default").Name("p2").UID("p2").Node("n").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(11).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CannotBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).Obj(),
				testinghelper.MakePod().Namespace("default").Name("p3").UID("p3").Node("n").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(12).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CannotBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).Obj(),
				testinghelper.MakePod().Namespace("default").Name("p4").UID("p4").Node("n").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(13).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).
					Label("key", "val").Obj(),
				testinghelper.MakePod().Namespace("default").Name("p5").UID("p5").Node("n").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(14).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CannotBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).Obj(),
				testinghelper.MakePod().Namespace("default").Name("p6").UID("p6").Node("n").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(15).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CannotBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).Obj(),
				testinghelper.MakePod().Namespace("default").Name("p7").UID("p7").Node("n").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(16).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).Obj(),
				testinghelper.MakePod().Namespace("default").Name("p8").UID("p8").Node("n").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(17).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).Obj(),
			},
			node: testinghelper.MakeNode().Name("n").Capacity(map[v1.ResourceName]string{v1.ResourceCPU: "8"}).Obj(),
			pdbs: []*policy.PodDisruptionBudget{
				testinghelper.MakePdb().Namespace("default").Name("pdb").DisruptionsAllowed(0).
					Label("key", "val").Obj(),
			},
			expectedVictims: []*v1.Pod{
				testinghelper.MakePod().Namespace("default").Name("p8").UID("p8").Node("n").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(17).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).Obj(),
				testinghelper.MakePod().Namespace("default").Name("p7").UID("p7").Node("n").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(16).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).Obj(),
			},
			expectedPDBAllowed: []int32{0},
		},
	}

	testFunc := func(gs podScheduler) {
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				gate := utilfeature.DefaultFeatureGate.(featuregate.MutableFeatureGate)
				if err := gate.SetFromMap(map[string]bool{string(features.EnableColocation): tt.enableColocation}); err != nil {
					t.Errorf("failed to set featuregate %s", features.EnableColocation)
				}
				if utilfeature.DefaultFeatureGate.Enabled(features.EnableColocation) != tt.enableColocation {
					t.Errorf("failed to set featuregate %s", features.EnableColocation)
				}

				cache := godelcache.New(handler.MakeCacheHandlerWrapper().
					SchedulerName("").SchedulerType("").SubCluster(framework.DefaultSubCluster).
					TTL(time.Second).Period(10 * time.Second).StopCh(make(<-chan struct{})).
					EnableStore("PreemptionStore").
					Obj())
				snapshot := godelcache.NewEmptySnapshot(handler.MakeCacheHandlerWrapper().
					SubCluster(framework.DefaultSubCluster).SwitchType(framework.DefaultSubClusterSwitchType).
					EnableStore("PreemptionStore").
					Obj())
				gs.snapshot = snapshot

				for _, pod := range tt.existingPods {
					cache.AddPod(pod)
				}
				cache.AddNode(tt.node)
				if tt.cnr != nil {
					cache.AddCNR(tt.cnr)
				}

				for _, pdb := range tt.pdbs {
					cache.AddPDB(pdb)
				}

				cache.UpdateSnapshot(snapshot)

				nodeResourcesPlugin, err := noderesources.NewFit(nil, nil)
				if err != nil {
					t.Errorf("Could not new node resources plugin: %v", err)
				}

				fh, _ := st.NewSchedulerFrameworkHandle(gs.clientSet, gs.crdClient, gs.informerFactory, gs.crdInformerFactory, cache, snapshot, framework.PluginMap{noderesources.FitName: nodeResourcesPlugin}, nil, nil, nil)
				nonNativeTopologyPlugin, err := nonnativeresource.NewNonNativeTopology(nil, fh)
				if err != nil {
					t.Errorf("Could not new nonnative topology plugin: %v", err)
				}
				basePlugins, preemptionPluginRegistry, err := utils.GetPreemptionRelatedPlugins(fh)
				if err != nil {
					t.Errorf("failed to get defualt plugins and configs: %v", err)
				}

				filterPluginRegistry := &framework.OrderedPluginRegistry{Plugins: []string{noderesources.FitName}}
				fh, _ = st.NewSchedulerFrameworkHandle(gs.clientSet, gs.crdClient, gs.informerFactory, gs.crdInformerFactory, cache, snapshot, framework.PluginMap{noderesources.FitName: nodeResourcesPlugin, nonnativeresource.NonNativeTopologyName: nonNativeTopologyPlugin}, preemptionPluginRegistry, filterPluginRegistry, basePlugins)
				gs.betterSelectPoliciesRegistry = map[string]betterSelectPolicy{
					config.BetterPreemptionPolicyAscending: gs.ascendingOrderPreemption,
					config.BetterPreemptionPolicyDichotomy: gs.dichotomyPreemption,
				}
				fwk, _ := fh.GetFrameworkForPod(tt.pod)
				state, err := fwk.InitCycleState(tt.pod)
				if err != nil {
					t.Fatal(err)
				}
				pfwk := fh.GetPreemptionFrameworkForPod(tt.pod)
				commonState := framework.NewCycleState()

				if status := fwk.RunPreFilterPlugins(context.Background(), state, tt.pod); !status.IsSuccess() {
					t.Errorf("Unexpected PreFilter Status: %v", status)
				}

				nodeLister := snapshot.NodeInfos()
				nodeInfoList := nodeLister.List()
				cachedNominatedNodes := &framework.CachedNominatedNodes{}
				cachedNominatedNodes.SetPodCount(1)
				candidates, err := gs.FindCandidates(context.Background(), fwk, pfwk, state, commonState, tt.pod, nodeInfoList, cachedNominatedNodes)
				if err != nil {
					t.Fatal(err)
				}
				if len(candidates) != 1 {
					t.Errorf("expect get 1 candidate but got %d", len(candidates))
					return
				}
				if !reflect.DeepEqual(tt.expectedVictims, candidates[0].Victims.Pods) {
					t.Errorf("expect to get victims: %v, but got: %v", tt.expectedVictims, candidates[0].Victims.Pods)
				}

				c, _ := candidates[0].Victims.PreemptionState.Read("Searching-PDBChecker")
				s := c.(*pdbchecker.PDBPreemptionState)
				pdbsAllowed := s.GetPDBsAllowed()
				if !reflect.DeepEqual(tt.expectedPDBAllowed, pdbsAllowed) {
					t.Errorf("expected pdbs allowed: %v, but got: %v", tt.expectedPDBAllowed, pdbsAllowed)
				}
			})
		}
	}

	// randomOrBestPreemption
	{
		crdClient := godelclientfake.NewSimpleClientset()
		crdInformerFactory := crdinformers.NewSharedInformerFactory(crdClient, 0)
		client := clientsetfake.NewSimpleClientset()
		informerFactory := informers.NewSharedInformerFactory(client, 0)
		isolatedcache := isolatedcache.NewIsolatedCache()
		pl1 := podScheduler{
			clientSet:             client,
			informerFactory:       informerFactory,
			crdClient:             crdClient,
			crdInformerFactory:    crdInformerFactory,
			isolatedCache:         isolatedcache,
			candidateSelectPolicy: config.CandidateSelectPolicyRandom,
		}
		testFunc(pl1)
	}

	// ascendingPreemption
	{
		crdClient := godelclientfake.NewSimpleClientset()
		crdInformerFactory := crdinformers.NewSharedInformerFactory(crdClient, 0)
		client := clientsetfake.NewSimpleClientset()
		informerFactory := informers.NewSharedInformerFactory(client, 0)
		isolatedcache := isolatedcache.NewIsolatedCache()
		pl2 := podScheduler{
			clientSet:             client,
			informerFactory:       informerFactory,
			crdClient:             crdClient,
			crdInformerFactory:    crdInformerFactory,
			isolatedCache:         isolatedcache,
			candidateSelectPolicy: config.CandidateSelectPolicyBetter,
			betterSelectPolicies:  []string{config.BetterPreemptionPolicyAscending},
		}
		testFunc(pl2)
	}

	// dichotomyPreemption
	{
		crdClient := godelclientfake.NewSimpleClientset()
		crdInformerFactory := crdinformers.NewSharedInformerFactory(crdClient, 0)
		client := clientsetfake.NewSimpleClientset()
		informerFactory := informers.NewSharedInformerFactory(client, 0)
		isolatedcache := isolatedcache.NewIsolatedCache()
		pl3 := podScheduler{
			clientSet:             client,
			informerFactory:       informerFactory,
			crdClient:             crdClient,
			crdInformerFactory:    crdInformerFactory,
			isolatedCache:         isolatedcache,
			candidateSelectPolicy: config.CandidateSelectPolicyBetter,
			betterSelectPolicies:  []string{config.BetterPreemptionPolicyDichotomy},
		}
		testFunc(pl3)
	}

	// ascendingPreemption or dichotomyPreemption
	{
		crdClient := godelclientfake.NewSimpleClientset()
		crdInformerFactory := crdinformers.NewSharedInformerFactory(crdClient, 0)
		client := clientsetfake.NewSimpleClientset()
		informerFactory := informers.NewSharedInformerFactory(client, 0)
		isolatedcache := isolatedcache.NewIsolatedCache()
		pl4 := podScheduler{
			clientSet:             client,
			informerFactory:       informerFactory,
			crdClient:             crdClient,
			crdInformerFactory:    crdInformerFactory,
			isolatedCache:         isolatedcache,
			candidateSelectPolicy: config.CandidateSelectPolicyBetter,
			betterSelectPolicies:  []string{config.BetterPreemptionPolicyAscending, config.BetterPreemptionPolicyDichotomy},
		}
		testFunc(pl4)
	}
}

func TestMoreImportantPod(t *testing.T) {
	potentialVictims := []*v1.Pod{
		testinghelper.MakePod().Namespace("p1").Name("p1").UID("p1").Priority(10).Obj(),
		testinghelper.MakePod().Namespace("p2").Name("p2").UID("p2").Priority(20).Obj(),
		testinghelper.MakePod().Namespace("p3").Name("p3").UID("p3").Priority(30).Obj(),
		testinghelper.MakePod().Namespace("p4").Name("p4").UID("p4").Priority(30).Obj(),
		testinghelper.MakePod().Namespace("p5").Name("p5").UID("p5").Priority(40).Obj(),
		testinghelper.MakePod().Namespace("p6").Name("p6").UID("p6").Priority(40).Obj(),
		testinghelper.MakePod().Namespace("p7").Name("p7").UID("p7").Priority(50).Obj(),
		testinghelper.MakePod().Namespace("p8").Name("p8").UID("p8").Priority(50).Obj(),
		testinghelper.MakePod().Namespace("p9").Name("p9").UID("p9").Priority(60).Obj(),
	}
	podsCanNotBePreemptedSet := sets.NewString("p2/p2/p2", "p4/p4/p4", "p8/p8/p8")
	sort.SliceStable(potentialVictims, func(i, j int) bool {
		return moreImportantPod(potentialVictims[i], potentialVictims[j], podsCanNotBePreemptedSet)
	})
	gotRes := []string{}
	for _, p := range potentialVictims {
		gotRes = append(gotRes, podutil.GeneratePodKey(p))
	}
	expectedRes := []string{
		"p1/p1/p1",
		"p3/p3/p3",
		"p5/p5/p5",
		"p6/p6/p6",
		"p7/p7/p7",
		"p9/p9/p9",
		"p2/p2/p2",
		"p4/p4/p4",
		"p8/p8/p8",
	}
	if !reflect.DeepEqual(expectedRes, gotRes) {
		t.Errorf("expected: %v, but got: %v", expectedRes, gotRes)
	}
}

func TestFindCandidates_SelectPolicy(t *testing.T) {
	tests := []struct {
		name                      string
		defaultBetterSelectPolicy string
		foo1                      *v1.Pod
		foo2                      *v1.Pod
		expectedPolicies          []string
	}{
		{
			name:                      "first ascending, then dichotomy",
			defaultBetterSelectPolicy: config.BetterPreemptionPolicyAscending,
			foo1: testinghelper.MakePod().Namespace("p").Name("p").UID("p").Priority(40).PriorityClassName("sc").
				Req(map[v1.ResourceName]string{"cpu": "2"}).
				Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
				Annotation(constraints.HardConstraintsAnnotationKey, noderesources.FitName).
				Label("name", "dp").Obj(),
			foo2: testinghelper.MakePod().Namespace("p").Name("p").UID("p").Priority(40).PriorityClassName("sc").
				Req(map[v1.ResourceName]string{"cpu": "4"}).
				Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
				Annotation(constraints.HardConstraintsAnnotationKey, noderesources.FitName).
				Label("name", "dp").Obj(),
			expectedPolicies: []string{config.BetterPreemptionPolicyAscending, config.BetterPreemptionPolicyDichotomy},
		},
		{
			name:                      "first ascending, then ascending",
			defaultBetterSelectPolicy: config.BetterPreemptionPolicyAscending,
			foo1: testinghelper.MakePod().Namespace("p").Name("p").UID("p").Priority(40).PriorityClassName("sc").
				Req(map[v1.ResourceName]string{"cpu": "2"}).
				Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
				Annotation(constraints.HardConstraintsAnnotationKey, noderesources.FitName).
				Label("name", "dp").Obj(),
			foo2: testinghelper.MakePod().Namespace("p").Name("p").UID("p").Priority(40).PriorityClassName("sc").
				Req(map[v1.ResourceName]string{"cpu": "3"}).
				Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
				Annotation(constraints.HardConstraintsAnnotationKey, noderesources.FitName).
				Label("name", "dp").Obj(),
			expectedPolicies: []string{config.BetterPreemptionPolicyAscending, config.BetterPreemptionPolicyAscending},
		},
		{
			name:                      "first ascending, then dichotomy, preemption failed",
			defaultBetterSelectPolicy: config.BetterPreemptionPolicyAscending,
			foo1: testinghelper.MakePod().Namespace("p").Name("p").UID("p").Priority(40).PriorityClassName("sc").
				Req(map[v1.ResourceName]string{"cpu": "3"}).
				Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
				Annotation(constraints.HardConstraintsAnnotationKey, noderesources.FitName).
				Label("name", "dp").Obj(),
			foo2: testinghelper.MakePod().Namespace("p").Name("p").UID("p").Priority(40).PriorityClassName("sc").
				Req(map[v1.ResourceName]string{"cpu": "7"}).
				Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
				Annotation(constraints.HardConstraintsAnnotationKey, noderesources.FitName).
				Label("name", "dp").Obj(),
			expectedPolicies: []string{config.BetterPreemptionPolicyAscending, config.BetterPreemptionPolicyDichotomy},
		},
		{
			name:                      "first dichotomy, then ascending",
			defaultBetterSelectPolicy: config.BetterPreemptionPolicyDichotomy,
			foo1: testinghelper.MakePod().Namespace("p").Name("p").UID("p").Priority(40).PriorityClassName("sc").
				Req(map[v1.ResourceName]string{"cpu": "4"}).
				Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
				Annotation(constraints.HardConstraintsAnnotationKey, noderesources.FitName).
				Label("name", "dp").Obj(),
			foo2: testinghelper.MakePod().Namespace("p").Name("p").UID("p").Priority(40).PriorityClassName("sc").
				Req(map[v1.ResourceName]string{"cpu": "3"}).
				Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
				Annotation(constraints.HardConstraintsAnnotationKey, noderesources.FitName).
				Label("name", "dp").Obj(),
			expectedPolicies: []string{config.BetterPreemptionPolicyDichotomy, config.BetterPreemptionPolicyAscending},
		},
		{
			name:                      "first dichotomy, then dichotomy",
			defaultBetterSelectPolicy: config.BetterPreemptionPolicyDichotomy,
			foo1: testinghelper.MakePod().Namespace("p").Name("p").UID("p").Priority(40).PriorityClassName("sc").
				Req(map[v1.ResourceName]string{"cpu": "4"}).
				Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
				Annotation(constraints.HardConstraintsAnnotationKey, noderesources.FitName).
				Label("name", "dp").Obj(),
			foo2: testinghelper.MakePod().Namespace("p").Name("p").UID("p").Priority(40).PriorityClassName("sc").
				Req(map[v1.ResourceName]string{"cpu": "5"}).
				Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
				Annotation(constraints.HardConstraintsAnnotationKey, noderesources.FitName).
				Label("name", "dp").Obj(),
			expectedPolicies: []string{config.BetterPreemptionPolicyDichotomy, config.BetterPreemptionPolicyDichotomy},
		},
		{
			name:                      "first dichotomy, then dichotomy, preemption failed",
			defaultBetterSelectPolicy: config.BetterPreemptionPolicyDichotomy,
			foo1: testinghelper.MakePod().Namespace("p").Name("p").UID("p").Priority(40).PriorityClassName("sc").
				Req(map[v1.ResourceName]string{"cpu": "4"}).
				Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
				Annotation(constraints.HardConstraintsAnnotationKey, noderesources.FitName).
				Label("name", "dp").Obj(),
			foo2: testinghelper.MakePod().Namespace("p").Name("p").UID("p").Priority(40).PriorityClassName("sc").
				Req(map[v1.ResourceName]string{"cpu": "7"}).
				Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
				Annotation(constraints.HardConstraintsAnnotationKey, noderesources.FitName).
				Label("name", "dp").Obj(),
			expectedPolicies: []string{config.BetterPreemptionPolicyDichotomy, config.BetterPreemptionPolicyDichotomy},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			nodeResourcesPlugin, err := noderesources.NewFit(nil, nil)
			if err != nil {
				t.Errorf("Could not new node resources plugin: %v", err)
			}
			crdClient := godelclientfake.NewSimpleClientset()
			crdInformerFactory := crdinformers.NewSharedInformerFactory(crdClient, 0)
			client := clientsetfake.NewSimpleClientset()
			informerFactory := informers.NewSharedInformerFactory(client, 0)
			cache := godelcache.New(handler.MakeCacheHandlerWrapper().
				SchedulerName("").SchedulerType("").SubCluster(framework.DefaultSubCluster).
				TTL(time.Second).Period(10 * time.Second).StopCh(make(<-chan struct{})).
				EnableStore("PreemptionStore").
				Obj())
			snapshot := godelcache.NewEmptySnapshot(handler.MakeCacheHandlerWrapper().
				SubCluster(framework.DefaultSubCluster).SwitchType(framework.DefaultSubClusterSwitchType).
				EnableStore("PreemptionStore").
				Obj())
			cache.UpdateSnapshot(snapshot)
			fh, _ := st.NewSchedulerFrameworkHandle(client, crdClient, informerFactory, crdInformerFactory, cache, snapshot, framework.PluginMap{noderesources.FitName: nodeResourcesPlugin}, nil, nil, nil)
			basePlugins, preemptionPluginRegistry, err := utils.GetPreemptionRelatedPlugins(fh)
			if err != nil {
				t.Errorf("failed to get defualt plugins and configs: %v", err)
			}

			filterPluginRegistry := &framework.OrderedPluginRegistry{Plugins: []string{noderesources.FitName}}
			fh, _ = st.NewSchedulerFrameworkHandle(client, crdClient, informerFactory, crdInformerFactory, cache, snapshot, framework.PluginMap{noderesources.FitName: nodeResourcesPlugin}, preemptionPluginRegistry, filterPluginRegistry, basePlugins)

			node := &v1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Name: "n",
				},
				Status: v1.NodeStatus{
					Allocatable: v1.ResourceList{
						"cpu":  resource.MustParse("6"),
						"pods": resource.MustParse("100"),
					},
				},
			}
			existingPods := []*v1.Pod{
				testinghelper.MakePod().Namespace("p1").Name("p1").UID("p1").Priority(10).PriorityClassName("sc").
					Req(map[v1.ResourceName]string{"cpu": "1"}).Node("n").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Label("name", "dp").Obj(),
				testinghelper.MakePod().Namespace("p2").Name("p2").UID("p2").Priority(20).PriorityClassName("sc").
					Req(map[v1.ResourceName]string{"cpu": "2"}).Node("n").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Label("name", "dp").Obj(),
				testinghelper.MakePod().Namespace("p3").Name("p3").UID("p3").Priority(30).PriorityClassName("sc").
					Req(map[v1.ResourceName]string{"cpu": "3"}).Node("n").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Label("name", "dp").Obj(),
			}
			nodeInfo := framework.NewNodeInfo(existingPods...)
			nodeInfo.SetNode(node)
			gs := podScheduler{
				clientSet:             client,
				informerFactory:       informerFactory,
				crdClient:             crdClient,
				crdInformerFactory:    crdInformerFactory,
				snapshot:              snapshot,
				isolatedCache:         isolatedcache.NewIsolatedCache(),
				candidateSelectPolicy: config.CandidateSelectPolicyBetter,
				betterSelectPolicies:  []string{config.BetterPreemptionPolicyAscending, config.BetterPreemptionPolicyDichotomy},
			}
			gs.betterSelectPoliciesRegistry = map[string]betterSelectPolicy{
				config.BetterPreemptionPolicyAscending: gs.ascendingOrderPreemption,
				config.BetterPreemptionPolicyDichotomy: gs.dichotomyPreemption,
			}

			fwk, _ := fh.GetFrameworkForPod(tt.foo1)
			state, err := fwk.InitCycleState(tt.foo1)
			if err != nil {
				t.Fatal(err)
			}
			pfwk := fh.GetPreemptionFrameworkForPod(tt.foo1)
			commonState := framework.NewCycleState()
			if status := fwk.RunPreFilterPlugins(context.Background(), state, tt.foo1); !status.IsSuccess() {
				t.Errorf("Unexpected PreFilter Status: %v", status)
			}
			nodeInfo = nodeInfo.Clone()
			cachedNominatedNodes := &framework.CachedNominatedNodes{}
			cachedNominatedNodes.SetPodCount(1)
			gs.FindCandidates(context.Background(), fwk, pfwk, state, commonState, tt.foo1, []framework.NodeInfo{nodeInfo}, cachedNominatedNodes)
			gotPolicy1 := gs.GetPreemptionPolicy("dp")

			fwk, _ = fh.GetFrameworkForPod(tt.foo2)
			state, err = fwk.InitCycleState(tt.foo2)
			if err != nil {
				t.Fatal(err)
			}
			pfwk = fh.GetPreemptionFrameworkForPod(tt.foo2)
			commonState = framework.NewCycleState()
			if status := fwk.RunPreFilterPlugins(context.Background(), state, tt.foo2); !status.IsSuccess() {
				t.Errorf("Unexpected PreFilter Status: %v", status)
			}
			gs.FindCandidates(context.Background(), fwk, pfwk, state, commonState, tt.foo2, []framework.NodeInfo{nodeInfo}, cachedNominatedNodes)
			gotPolicy2 := gs.GetPreemptionPolicy("dp")
			gotPolicies := []string{gotPolicy1, gotPolicy2}
			if !reflect.DeepEqual(tt.expectedPolicies, gotPolicies) {
				t.Errorf("expected policies: %v, but got: %v", tt.expectedPolicies, gotPolicies)
			}
		})
	}
}

func TestFindCandidates_CheckNominatedNodeCount(t *testing.T) {
	existingNodes := []*v1.Node{
		testinghelper.MakeNode().Name("n1").Capacity(map[v1.ResourceName]string{"cpu": "10"}).Obj(),
		testinghelper.MakeNode().Name("n2").Capacity(map[v1.ResourceName]string{"cpu": "10"}).Obj(),
		testinghelper.MakeNode().Name("n3").Capacity(map[v1.ResourceName]string{"cpu": "10"}).Obj(),
		testinghelper.MakeNode().Name("n4").Capacity(map[v1.ResourceName]string{"cpu": "10"}).Obj(),
	}

	existingPods := []*v1.Pod{
		testinghelper.MakePod().Name("p1").UID("p1").Node("n1").Priority(10).PriorityClassName("pc").
			Req(map[v1.ResourceName]string{"cpu": "5"}).
			Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).Obj(),
		testinghelper.MakePod().Name("p2").UID("p2").Node("n1").Priority(11).PriorityClassName("pc").
			Req(map[v1.ResourceName]string{"cpu": "5"}).
			Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).Obj(),
		testinghelper.MakePod().Name("p3").UID("p3").Node("n2").Priority(12).PriorityClassName("pc").
			Req(map[v1.ResourceName]string{"cpu": "5"}).
			Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).Obj(),
		testinghelper.MakePod().Name("p4").UID("p4").Node("n2").Priority(13).PriorityClassName("pc").
			Req(map[v1.ResourceName]string{"cpu": "5"}).
			Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).Obj(),
		testinghelper.MakePod().Name("p5").UID("p5").Node("n3").Priority(14).PriorityClassName("pc").
			Req(map[v1.ResourceName]string{"cpu": "5"}).
			Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).Obj(),
		testinghelper.MakePod().Name("p6").UID("p6").Node("n3").Priority(15).PriorityClassName("pc").
			Req(map[v1.ResourceName]string{"cpu": "5"}).
			Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).Obj(),
		testinghelper.MakePod().Name("p7").UID("p7").Node("n4").Priority(16).PriorityClassName("pc").
			Req(map[v1.ResourceName]string{"cpu": "5"}).
			Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).Obj(),
		testinghelper.MakePod().Name("p8").UID("p8").Node("n4").Priority(17).PriorityClassName("pc").
			Req(map[v1.ResourceName]string{"cpu": "5"}).
			Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).Obj(),
	}

	tests := []struct {
		name                  string
		candidateSelectPolicy string
		betterSelectPolicy    string
		expectedNodeCount     int
		expectedGotNodeCount  int
		expectedCanidates     []string
	}{
		{
			name:                  "random preemption, expected 1 node",
			candidateSelectPolicy: config.CandidateSelectPolicyRandom,
			expectedNodeCount:     1,
			expectedGotNodeCount:  1,
		},
		{
			name:                  "random preemption, expected 2 node",
			candidateSelectPolicy: config.CandidateSelectPolicyRandom,
			expectedNodeCount:     2,
			expectedGotNodeCount:  2,
		},
		{
			name:                  "random preemption, expected 5 node",
			candidateSelectPolicy: config.CandidateSelectPolicyRandom,
			expectedNodeCount:     5,
			expectedGotNodeCount:  4,
		},
		{
			name:                  "best preemption, expected 1 node",
			candidateSelectPolicy: config.CandidateSelectPolicyBest,
			expectedNodeCount:     1,
			expectedGotNodeCount:  4,
		},
		{
			name:                  "best preemption, expected 2 node",
			candidateSelectPolicy: config.CandidateSelectPolicyBest,
			expectedNodeCount:     2,
			expectedGotNodeCount:  4,
		},
		{
			name:                  "best preemption, expected 5 node",
			candidateSelectPolicy: config.CandidateSelectPolicyBest,
			expectedNodeCount:     5,
			expectedGotNodeCount:  4,
		},
		{
			name:                  "ascending better preemption, expected 1 node",
			candidateSelectPolicy: config.CandidateSelectPolicyBetter,
			betterSelectPolicy:    config.BetterPreemptionPolicyAscending,
			expectedNodeCount:     1,
			expectedGotNodeCount:  1,
			expectedCanidates:     []string{"n1"},
		},
		{
			name:                  "ascending better preemption, expected 2 node",
			candidateSelectPolicy: config.CandidateSelectPolicyBetter,
			betterSelectPolicy:    config.BetterPreemptionPolicyAscending,
			expectedNodeCount:     2,
			expectedGotNodeCount:  2,
			expectedCanidates:     []string{"n1", "n2"},
		},
		{
			name:                  "ascending better preemption, expected 5 node",
			candidateSelectPolicy: config.CandidateSelectPolicyBetter,
			betterSelectPolicy:    config.BetterPreemptionPolicyAscending,
			expectedNodeCount:     5,
			expectedGotNodeCount:  4,
			expectedCanidates:     []string{"n1", "n2", "n3", "n4"},
		},
		{
			name:                  "dichotomy better preemption, expected 1 node",
			candidateSelectPolicy: config.CandidateSelectPolicyBetter,
			betterSelectPolicy:    config.BetterPreemptionPolicyDichotomy,
			expectedNodeCount:     1,
			expectedGotNodeCount:  1,
			expectedCanidates:     []string{"n1"},
		},
		{
			name:                  "dichotomy better preemption, expected 2 node",
			candidateSelectPolicy: config.CandidateSelectPolicyBetter,
			betterSelectPolicy:    config.BetterPreemptionPolicyDichotomy,
			expectedNodeCount:     2,
			expectedGotNodeCount:  2,
			expectedCanidates:     []string{"n1", "n2"},
		},
		{
			name:                  "dichotomy better preemption, expected 5 node",
			candidateSelectPolicy: config.CandidateSelectPolicyBetter,
			betterSelectPolicy:    config.BetterPreemptionPolicyDichotomy,
			expectedNodeCount:     5,
			expectedGotNodeCount:  2,
			expectedCanidates:     []string{"n1", "n2"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			preemptor := testinghelper.MakePod().Name("foo").UID("foo").Priority(100).
				Req(map[v1.ResourceName]string{"cpu": "10"}).
				Annotation(constraints.HardConstraintsAnnotationKey, noderesources.FitName).Obj()
			crdClient := godelclientfake.NewSimpleClientset()
			crdInformerFactory := crdinformers.NewSharedInformerFactory(crdClient, 0)
			client := clientsetfake.NewSimpleClientset()
			informerFactory := informers.NewSharedInformerFactory(client, 0)
			isolatedcache := isolatedcache.NewIsolatedCache()
			gs := podScheduler{
				clientSet:             client,
				informerFactory:       informerFactory,
				crdClient:             crdClient,
				crdInformerFactory:    crdInformerFactory,
				isolatedCache:         isolatedcache,
				candidateSelectPolicy: tt.candidateSelectPolicy,
				betterSelectPolicies:  []string{tt.betterSelectPolicy},
			}

			cache := godelcache.New(handler.MakeCacheHandlerWrapper().
				SchedulerName("").SchedulerType("").SubCluster(framework.DefaultSubCluster).
				TTL(time.Second).Period(10 * time.Second).StopCh(make(<-chan struct{})).
				EnableStore("PreemptionStore").
				Obj())
			snapshot := godelcache.NewEmptySnapshot(handler.MakeCacheHandlerWrapper().
				SubCluster(framework.DefaultSubCluster).SwitchType(framework.DefaultSubClusterSwitchType).
				EnableStore("PreemptionStore").
				Obj())
			gs.snapshot = snapshot

			for _, pod := range existingPods {
				cache.AddPod(pod)
			}
			for _, node := range existingNodes {
				cache.AddNode(node)
			}
			cache.UpdateSnapshot(snapshot)

			nodeResourcesPlugin, err := noderesources.NewFit(nil, nil)
			if err != nil {
				t.Errorf("Could not new node resources plugin: %v", err)
			}

			fh, _ := st.NewSchedulerFrameworkHandle(gs.clientSet, gs.crdClient, gs.informerFactory, gs.crdInformerFactory, cache, snapshot, framework.PluginMap{noderesources.FitName: nodeResourcesPlugin}, nil, nil, nil)
			nonNativeTopologyPlugin, err := nonnativeresource.NewNonNativeTopology(nil, fh)
			if err != nil {
				t.Errorf("Could not new nonnative topology plugin: %v", err)
			}
			basePlugins, preemptionPluginRegistry, err := utils.GetPreemptionRelatedPlugins(fh)
			if err != nil {
				t.Errorf("failed to get defualt plugins and configs: %v", err)
			}

			filterPluginRegistry := &framework.OrderedPluginRegistry{Plugins: []string{noderesources.FitName}}
			fh, _ = st.NewSchedulerFrameworkHandle(gs.clientSet, gs.crdClient, gs.informerFactory, gs.crdInformerFactory, cache, snapshot, framework.PluginMap{noderesources.FitName: nodeResourcesPlugin, nonnativeresource.NonNativeTopologyName: nonNativeTopologyPlugin}, preemptionPluginRegistry, filterPluginRegistry, basePlugins)
			gs.betterSelectPoliciesRegistry = map[string]betterSelectPolicy{
				config.BetterPreemptionPolicyAscending: gs.ascendingOrderPreemption,
				config.BetterPreemptionPolicyDichotomy: gs.dichotomyPreemption,
			}
			fwk, _ := fh.GetFrameworkForPod(preemptor)
			state, err := fwk.InitCycleState(preemptor)
			if err != nil {
				t.Fatal(err)
			}
			pfwk := fh.GetPreemptionFrameworkForPod(preemptor)
			commonState := framework.NewCycleState()

			if status := fwk.RunPreFilterPlugins(context.Background(), state, preemptor); !status.IsSuccess() {
				t.Errorf("Unexpected PreFilter Status: %v", status)
			}

			nodeLister := snapshot.NodeInfos()
			nodeInfoList := nodeLister.List()
			cachedNominatedNodes := &framework.CachedNominatedNodes{}
			cachedNominatedNodes.SetPodCount(tt.expectedNodeCount)
			candidates, err := gs.FindCandidates(context.Background(), fwk, pfwk, state, commonState, preemptor, nodeInfoList, cachedNominatedNodes)
			if err != nil {
				t.Fatal(err)
			}

			if tt.candidateSelectPolicy == config.CandidateSelectPolicyRandom || tt.candidateSelectPolicy == config.CandidateSelectPolicyBest {
				if len(candidates) != tt.expectedGotNodeCount {
					t.Errorf("expected %d node but got %d", tt.expectedGotNodeCount, len(candidates))
				}
			} else if tt.candidateSelectPolicy == config.CandidateSelectPolicyBetter {
				if len(candidates) != tt.expectedGotNodeCount {
					t.Errorf("expected %d node but got %d", tt.expectedGotNodeCount, len(candidates))
				}
				var gotCandidates []string
				for _, c := range candidates {
					gotCandidates = append(gotCandidates, c.Name)
				}
				if !reflect.DeepEqual(tt.expectedCanidates, gotCandidates) {
					t.Errorf("expected %v but got %v", tt.expectedCanidates, gotCandidates)
				}
			}
		})
	}
}

func TestBetterPreemption_PDB(t *testing.T) {
	tests := []struct {
		name                      string
		pod                       *v1.Pod
		existingPods              []*v1.Pod
		nodeList                  []*v1.Node
		pdbs                      []*policy.PodDisruptionBudget
		expectedNodeIndexes       [][]int
		expectedMaxVictimPriority int
	}{
		{
			name: "first and second",
			pod: testinghelper.MakePod().Namespace("default").Name("p").UID("p").Req(map[v1.ResourceName]string{v1.ResourceCPU: "2"}).Priority(20).
				Annotation(constraints.HardConstraintsAnnotationKey, noderesources.FitName).Obj(),
			existingPods: []*v1.Pod{
				testinghelper.MakePod().Namespace("default").Name("p1").UID("p1").Node("n2").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(10).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).
					Label("key", "val").Obj(),
				testinghelper.MakePod().Namespace("default").Name("p2").UID("p2").Node("n2").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(11).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).
					Label("key", "val").Obj(),
				testinghelper.MakePod().Namespace("default").Name("p3").UID("p3").Node("n2").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(12).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).Obj(),
				testinghelper.MakePod().Namespace("default").Name("p4").UID("p4").Node("n2").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(13).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).Obj(),
				testinghelper.MakePod().Namespace("default").Name("p5").UID("p5").Node("n2").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(14).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).Obj(),
				testinghelper.MakePod().Namespace("default").Name("p6").UID("p6").Node("n2").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(15).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).Obj(),
				testinghelper.MakePod().Namespace("default").Name("p7").UID("p7").Node("n2").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(16).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).Obj(),
				testinghelper.MakePod().Namespace("default").Name("p8").UID("p8").Node("n2").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(17).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).Obj(),
				testinghelper.MakePod().Namespace("default").Name("p9").UID("p9").Node("n1").Req(map[v1.ResourceName]string{v1.ResourceCPU: "2"}).Priority(12).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).Obj(),
				testinghelper.MakePod().Namespace("default").Name("p10").UID("p10").Node("n1").Req(map[v1.ResourceName]string{v1.ResourceCPU: "6"}).Priority(12).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).Obj(),
			},
			nodeList: []*v1.Node{
				testinghelper.MakeNode().Name("n1").Capacity(map[v1.ResourceName]string{v1.ResourceCPU: "8"}).Obj(),
				testinghelper.MakeNode().Name("n2").Capacity(map[v1.ResourceName]string{v1.ResourceCPU: "8"}).Obj(),
			},
			pdbs: []*policy.PodDisruptionBudget{
				testinghelper.MakePdb().Namespace("default").Name("pdb").DisruptionsAllowed(2).
					Label("key", "val").Obj(),
			},
			expectedNodeIndexes: [][]int{
				{1, 0},
				{1, 0},
			},
			expectedMaxVictimPriority: 11,
		},
		{
			name: "first and third",
			pod: testinghelper.MakePod().Namespace("default").Name("p").UID("p").Req(map[v1.ResourceName]string{v1.ResourceCPU: "2"}).Priority(20).
				Annotation(constraints.HardConstraintsAnnotationKey, noderesources.FitName).Obj(),
			existingPods: []*v1.Pod{
				testinghelper.MakePod().Namespace("default").Name("p1").UID("p1").Node("n2").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(10).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).
					Label("key", "val").Obj(),
				testinghelper.MakePod().Namespace("default").Name("p2").UID("p2").Node("n2").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(11).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).
					Label("key", "val").Obj(),
				testinghelper.MakePod().Namespace("default").Name("p3").UID("p3").Node("n2").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(12).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).Obj(),
				testinghelper.MakePod().Namespace("default").Name("p4").UID("p4").Node("n2").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(13).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).Obj(),
				testinghelper.MakePod().Namespace("default").Name("p5").UID("p5").Node("n2").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(14).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).Obj(),
				testinghelper.MakePod().Namespace("default").Name("p6").UID("p6").Node("n2").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(15).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).Obj(),
				testinghelper.MakePod().Namespace("default").Name("p7").UID("p7").Node("n2").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(16).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).Obj(),
				testinghelper.MakePod().Namespace("default").Name("p8").UID("p8").Node("n2").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(17).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).Obj(),
				testinghelper.MakePod().Namespace("default").Name("p9").UID("p9").Node("n1").Req(map[v1.ResourceName]string{v1.ResourceCPU: "2"}).Priority(13).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).Obj(),
				testinghelper.MakePod().Namespace("default").Name("p10").UID("p10").Node("n1").Req(map[v1.ResourceName]string{v1.ResourceCPU: "6"}).Priority(13).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).Obj(),
			},
			nodeList: []*v1.Node{
				testinghelper.MakeNode().Name("n1").Capacity(map[v1.ResourceName]string{v1.ResourceCPU: "8"}).Obj(),
				testinghelper.MakeNode().Name("n2").Capacity(map[v1.ResourceName]string{v1.ResourceCPU: "8"}).Obj(),
			},
			pdbs: []*policy.PodDisruptionBudget{
				testinghelper.MakePdb().Namespace("default").Name("pdb").DisruptionsAllowed(1).
					Label("key", "val").Obj(),
			},
			expectedNodeIndexes: [][]int{
				{1, 0},
				{1, 0},
			},
			expectedMaxVictimPriority: 12,
		},
		{
			name: "first and forth",
			pod: testinghelper.MakePod().Namespace("default").Name("p").UID("p").Req(map[v1.ResourceName]string{v1.ResourceCPU: "2"}).Priority(20).
				Annotation(constraints.HardConstraintsAnnotationKey, noderesources.FitName).Obj(),
			existingPods: []*v1.Pod{
				testinghelper.MakePod().Namespace("default").Name("p1").UID("p1").Node("n1").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(10).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).
					Label("key", "val").Obj(),
				testinghelper.MakePod().Namespace("default").Name("p2").UID("p2").Node("n1").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(11).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).
					Label("key", "val").Obj(),
				testinghelper.MakePod().Namespace("default").Name("p3").UID("p3").Node("n1").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(12).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).
					Label("key", "val").Obj(),
				testinghelper.MakePod().Namespace("default").Name("p4").UID("p4").Node("n1").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(13).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).Obj(),
				testinghelper.MakePod().Namespace("default").Name("p5").UID("p5").Node("n1").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(14).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).Obj(),
				testinghelper.MakePod().Namespace("default").Name("p6").UID("p6").Node("n1").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(15).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).Obj(),
				testinghelper.MakePod().Namespace("default").Name("p7").UID("p7").Node("n1").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(16).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).Obj(),
				testinghelper.MakePod().Namespace("default").Name("p8").UID("p8").Node("n1").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(17).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).Obj(),
				testinghelper.MakePod().Namespace("default").Name("p9").UID("p9").Node("n2").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(13).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).Obj(),
				testinghelper.MakePod().Namespace("default").Name("p10").UID("p10").Node("n2").Req(map[v1.ResourceName]string{v1.ResourceCPU: "7"}).Priority(14).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).Obj(),
			},
			nodeList: []*v1.Node{
				testinghelper.MakeNode().Name("n1").Capacity(map[v1.ResourceName]string{v1.ResourceCPU: "8"}).Obj(),
				testinghelper.MakeNode().Name("n2").Capacity(map[v1.ResourceName]string{v1.ResourceCPU: "8"}).Obj(),
			},
			pdbs: []*policy.PodDisruptionBudget{
				testinghelper.MakePdb().Namespace("default").Name("pdb").DisruptionsAllowed(1).
					Label("key", "val").Obj(),
			},
			expectedNodeIndexes: [][]int{
				{0, 1},
				{0},
			},
			expectedMaxVictimPriority: 13,
		},
		{
			name: "first and fifth",
			pod: testinghelper.MakePod().Namespace("default").Name("p").UID("p").Req(map[v1.ResourceName]string{v1.ResourceCPU: "2"}).Priority(20).
				Annotation(constraints.HardConstraintsAnnotationKey, noderesources.FitName).Obj(),
			existingPods: []*v1.Pod{
				testinghelper.MakePod().Namespace("default").Name("p1").UID("p1").Node("n2").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(10).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).
					Label("key", "val").Obj(),
				testinghelper.MakePod().Namespace("default").Name("p2").UID("p2").Node("n2").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(11).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).
					Label("key", "val").Obj(),
				testinghelper.MakePod().Namespace("default").Name("p3").UID("p3").Node("n2").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(12).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).
					Label("key", "val").Obj(),
				testinghelper.MakePod().Namespace("default").Name("p4").UID("p4").Node("n2").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(13).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).
					Label("key", "val").Obj(),
				testinghelper.MakePod().Namespace("default").Name("p5").UID("p5").Node("n2").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(14).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).Obj(),
				testinghelper.MakePod().Namespace("default").Name("p6").UID("p6").Node("n2").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(15).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).Obj(),
				testinghelper.MakePod().Namespace("default").Name("p7").UID("p7").Node("n2").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(16).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).Obj(),
				testinghelper.MakePod().Namespace("default").Name("p8").UID("p8").Node("n2").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(17).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).Obj(),
				testinghelper.MakePod().Namespace("default").Name("p9").UID("p9").Node("n1").Req(map[v1.ResourceName]string{v1.ResourceCPU: "2"}).Priority(15).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).
					Label("key", "val").Obj(),
				testinghelper.MakePod().Namespace("default").Name("p10").UID("p10").Node("n1").Req(map[v1.ResourceName]string{v1.ResourceCPU: "6"}).Priority(15).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).Obj(),
			},
			nodeList: []*v1.Node{
				testinghelper.MakeNode().Name("n1").Capacity(map[v1.ResourceName]string{v1.ResourceCPU: "8"}).Obj(),
				testinghelper.MakeNode().Name("n2").Capacity(map[v1.ResourceName]string{v1.ResourceCPU: "8"}).Obj(),
			},
			pdbs: []*policy.PodDisruptionBudget{
				testinghelper.MakePdb().Namespace("default").Name("pdb").DisruptionsAllowed(1).
					Label("key", "val").Obj(),
			},
			expectedNodeIndexes: [][]int{
				{1, 0},
				{1, 0},
			},
			expectedMaxVictimPriority: 14,
		},
		{
			name: "first and sixth",
			pod: testinghelper.MakePod().Namespace("default").Name("p").UID("p").Req(map[v1.ResourceName]string{v1.ResourceCPU: "2"}).Priority(20).
				Annotation(constraints.HardConstraintsAnnotationKey, noderesources.FitName).Obj(),
			existingPods: []*v1.Pod{
				testinghelper.MakePod().Namespace("default").Name("p1").UID("p1").Node("n2").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(10).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).
					Label("key", "val").Obj(),
				testinghelper.MakePod().Namespace("default").Name("p2").UID("p2").Node("n2").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(11).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).
					Label("key", "val").Obj(),
				testinghelper.MakePod().Namespace("default").Name("p3").UID("p3").Node("n2").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(12).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).
					Label("key", "val").Obj(),
				testinghelper.MakePod().Namespace("default").Name("p4").UID("p4").Node("n2").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(13).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).
					Label("key", "val").Obj(),
				testinghelper.MakePod().Namespace("default").Name("p5").UID("p5").Node("n2").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(14).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).
					Label("key", "val").Obj(),
				testinghelper.MakePod().Namespace("default").Name("p6").UID("p6").Node("n2").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(15).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).Obj(),
				testinghelper.MakePod().Namespace("default").Name("p7").UID("p7").Node("n2").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(16).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).Obj(),
				testinghelper.MakePod().Namespace("default").Name("p8").UID("p8").Node("n2").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(17).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).Obj(),
				testinghelper.MakePod().Namespace("default").Name("p9").UID("p9").Node("n1").Req(map[v1.ResourceName]string{v1.ResourceCPU: "2"}).Priority(16).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).
					Label("key", "val").Obj(),
				testinghelper.MakePod().Namespace("default").Name("p10").UID("p10").Node("n1").Req(map[v1.ResourceName]string{v1.ResourceCPU: "6"}).Priority(16).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).Obj(),
			},
			nodeList: []*v1.Node{
				testinghelper.MakeNode().Name("n1").Capacity(map[v1.ResourceName]string{v1.ResourceCPU: "8"}).Obj(),
				testinghelper.MakeNode().Name("n2").Capacity(map[v1.ResourceName]string{v1.ResourceCPU: "8"}).Obj(),
			},
			pdbs: []*policy.PodDisruptionBudget{
				testinghelper.MakePdb().Namespace("default").Name("pdb").DisruptionsAllowed(1).
					Label("key", "val").Obj(),
			},
			expectedNodeIndexes: [][]int{
				{1, 0},
				{1},
			},
			expectedMaxVictimPriority: 15,
		},
		{
			name: "first and seventh",
			pod: testinghelper.MakePod().Namespace("default").Name("p").UID("p").Req(map[v1.ResourceName]string{v1.ResourceCPU: "2"}).Priority(20).
				Annotation(constraints.HardConstraintsAnnotationKey, noderesources.FitName).Obj(),
			existingPods: []*v1.Pod{
				testinghelper.MakePod().Namespace("default").Name("p1").UID("p1").Node("n1").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(10).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).
					Label("key", "val").Obj(),
				testinghelper.MakePod().Namespace("default").Name("p2").UID("p2").Node("n1").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(11).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).
					Label("key", "val").Obj(),
				testinghelper.MakePod().Namespace("default").Name("p3").UID("p3").Node("n1").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(12).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).
					Label("key", "val").Obj(),
				testinghelper.MakePod().Namespace("default").Name("p4").UID("p4").Node("n1").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(13).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).
					Label("key", "val").Obj(),
				testinghelper.MakePod().Namespace("default").Name("p5").UID("p5").Node("n1").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(14).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).
					Label("key", "val").Obj(),
				testinghelper.MakePod().Namespace("default").Name("p6").UID("p6").Node("n1").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(15).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).
					Label("key", "val").Obj(),
				testinghelper.MakePod().Namespace("default").Name("p7").UID("p7").Node("n1").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(16).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).Obj(),
				testinghelper.MakePod().Namespace("default").Name("p8").UID("p8").Node("n1").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(17).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).Obj(),
				testinghelper.MakePod().Namespace("default").Name("p9").UID("p9").Node("n2").Req(map[v1.ResourceName]string{v1.ResourceCPU: "4"}).Priority(17).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).Obj(),
				testinghelper.MakePod().Namespace("default").Name("p10").UID("p10").Node("n2").Req(map[v1.ResourceName]string{v1.ResourceCPU: "4"}).Priority(17).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).Obj(),
			},
			nodeList: []*v1.Node{
				testinghelper.MakeNode().Name("n1").Capacity(map[v1.ResourceName]string{v1.ResourceCPU: "8"}).Obj(),
				testinghelper.MakeNode().Name("n2").Capacity(map[v1.ResourceName]string{v1.ResourceCPU: "8"}).Obj(),
			},
			pdbs: []*policy.PodDisruptionBudget{
				testinghelper.MakePdb().Namespace("default").Name("pdb").DisruptionsAllowed(1).
					Label("key", "val").Obj(),
			},
			expectedNodeIndexes: [][]int{
				{0, 1},
				{0},
			},
			expectedMaxVictimPriority: 16,
		},
		{
			name: "first and eighth",
			pod: testinghelper.MakePod().Namespace("default").Name("p").UID("p").Req(map[v1.ResourceName]string{v1.ResourceCPU: "2"}).Priority(20).
				Annotation(constraints.HardConstraintsAnnotationKey, noderesources.FitName).Obj(),
			existingPods: []*v1.Pod{
				testinghelper.MakePod().Namespace("default").Name("p1").UID("p1").Node("n1").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(10).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).
					Label("key", "val").Obj(),
				testinghelper.MakePod().Namespace("default").Name("p2").UID("p2").Node("n1").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(11).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).
					Label("key", "val").Obj(),
				testinghelper.MakePod().Namespace("default").Name("p3").UID("p3").Node("n1").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(12).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).
					Label("key", "val").Obj(),
				testinghelper.MakePod().Namespace("default").Name("p4").UID("p4").Node("n1").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(13).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).
					Label("key", "val").Obj(),
				testinghelper.MakePod().Namespace("default").Name("p5").UID("p5").Node("n1").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(14).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).
					Label("key", "val").Obj(),
				testinghelper.MakePod().Namespace("default").Name("p6").UID("p6").Node("n1").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(15).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).
					Label("key", "val").Obj(),
				testinghelper.MakePod().Namespace("default").Name("p7").UID("p7").Node("n1").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(16).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).
					Label("key", "val").Obj(),
				testinghelper.MakePod().Namespace("default").Name("p8").UID("p8").Node("n1").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(17).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).Obj(),
				testinghelper.MakePod().Namespace("default").Name("p9").UID("p9").Node("n2").Req(map[v1.ResourceName]string{v1.ResourceCPU: "4"}).Priority(18).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).Obj(),
				testinghelper.MakePod().Namespace("default").Name("p10").UID("p10").Node("n2").Req(map[v1.ResourceName]string{v1.ResourceCPU: "4"}).Priority(18).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).Obj(),
			},
			nodeList: []*v1.Node{
				testinghelper.MakeNode().Name("n1").Capacity(map[v1.ResourceName]string{v1.ResourceCPU: "8"}).Obj(),
				testinghelper.MakeNode().Name("n2").Capacity(map[v1.ResourceName]string{v1.ResourceCPU: "8"}).Obj(),
			},
			pdbs: []*policy.PodDisruptionBudget{
				testinghelper.MakePdb().Namespace("default").Name("pdb").DisruptionsAllowed(1).
					Label("key", "val").Obj(),
			},
			expectedNodeIndexes: [][]int{
				{0, 1},
				{0},
			},
			expectedMaxVictimPriority: 17,
		},
		{
			name: "second and third",
			pod: testinghelper.MakePod().Namespace("default").Name("p").UID("p").Req(map[v1.ResourceName]string{v1.ResourceCPU: "2"}).Priority(20).
				Annotation(constraints.HardConstraintsAnnotationKey, noderesources.FitName).Obj(),
			existingPods: []*v1.Pod{
				testinghelper.MakePod().Namespace("default").Name("p1").UID("p1").Node("n2").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(10).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).
					Label("key", "val").Obj(),
				testinghelper.MakePod().Namespace("default").Name("p2").UID("p2").Node("n2").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(11).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).
					Label("key", "val2").Obj(),
				testinghelper.MakePod().Namespace("default").Name("p3").UID("p3").Node("n2").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(12).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).Obj(),
				testinghelper.MakePod().Namespace("default").Name("p4").UID("p4").Node("n2").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(13).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).Obj(),
				testinghelper.MakePod().Namespace("default").Name("p5").UID("p5").Node("n2").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(14).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).Obj(),
				testinghelper.MakePod().Namespace("default").Name("p6").UID("p6").Node("n2").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(15).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).Obj(),
				testinghelper.MakePod().Namespace("default").Name("p7").UID("p7").Node("n2").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(16).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).Obj(),
				testinghelper.MakePod().Namespace("default").Name("p8").UID("p8").Node("n2").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(17).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).Obj(),
				testinghelper.MakePod().Namespace("default").Name("p9").UID("p9").Node("n1").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(13).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).Obj(),
				testinghelper.MakePod().Namespace("default").Name("p10").UID("p10").Node("n1").Req(map[v1.ResourceName]string{v1.ResourceCPU: "6"}).Priority(13).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).Obj(),
			},
			nodeList: []*v1.Node{
				testinghelper.MakeNode().Name("n1").Capacity(map[v1.ResourceName]string{v1.ResourceCPU: "8"}).Obj(),
				testinghelper.MakeNode().Name("n2").Capacity(map[v1.ResourceName]string{v1.ResourceCPU: "8"}).Obj(),
			},
			pdbs: []*policy.PodDisruptionBudget{
				testinghelper.MakePdb().Namespace("default").Name("pdb").DisruptionsAllowed(0).
					Label("key", "val").Obj(),
				testinghelper.MakePdb().Namespace("default").Name("pdb2").DisruptionsAllowed(1).
					Label("key", "val2").Obj(),
			},
			expectedNodeIndexes: [][]int{
				{1, 0},
				{1, 0},
			},
			expectedMaxVictimPriority: 12,
		},
		{
			name: "second and forth",
			pod: testinghelper.MakePod().Namespace("default").Name("p").UID("p").Req(map[v1.ResourceName]string{v1.ResourceCPU: "2"}).Priority(20).
				Annotation(constraints.HardConstraintsAnnotationKey, noderesources.FitName).Obj(),
			existingPods: []*v1.Pod{
				testinghelper.MakePod().Namespace("default").Name("p1").UID("p1").Node("n2").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(10).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).
					Label("key", "val").Obj(),
				testinghelper.MakePod().Namespace("default").Name("p2").UID("p2").Node("n2").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(11).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).
					Label("key", "val2").Obj(),
				testinghelper.MakePod().Namespace("default").Name("p3").UID("p3").Node("n2").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(12).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).
					Label("key", "val2").Obj(),
				testinghelper.MakePod().Namespace("default").Name("p4").UID("p4").Node("n2").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(13).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).Obj(),
				testinghelper.MakePod().Namespace("default").Name("p5").UID("p5").Node("n2").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(14).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).Obj(),
				testinghelper.MakePod().Namespace("default").Name("p6").UID("p6").Node("n2").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(15).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).Obj(),
				testinghelper.MakePod().Namespace("default").Name("p7").UID("p7").Node("n2").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(16).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).Obj(),
				testinghelper.MakePod().Namespace("default").Name("p8").UID("p8").Node("n2").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(17).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).Obj(),
				testinghelper.MakePod().Namespace("default").Name("p9").UID("p9").Node("n1").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(14).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).Obj(),
				testinghelper.MakePod().Namespace("default").Name("p10").UID("p10").Node("n1").Req(map[v1.ResourceName]string{v1.ResourceCPU: "6"}).Priority(14).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).Obj(),
			},
			nodeList: []*v1.Node{
				testinghelper.MakeNode().Name("n1").Capacity(map[v1.ResourceName]string{v1.ResourceCPU: "8"}).Obj(),
				testinghelper.MakeNode().Name("n2").Capacity(map[v1.ResourceName]string{v1.ResourceCPU: "8"}).Obj(),
			},
			pdbs: []*policy.PodDisruptionBudget{
				testinghelper.MakePdb().Namespace("default").Name("pdb").DisruptionsAllowed(0).
					Label("key", "val").Obj(),
				testinghelper.MakePdb().Namespace("default").Name("pdb2").DisruptionsAllowed(1).
					Label("key", "val2").Obj(),
			},
			expectedNodeIndexes: [][]int{
				{1, 0},
				{1},
			},
			expectedMaxVictimPriority: 13,
		},
		{
			name: "second and fifth",
			pod: testinghelper.MakePod().Namespace("default").Name("p").UID("p").Req(map[v1.ResourceName]string{v1.ResourceCPU: "2"}).Priority(20).
				Annotation(constraints.HardConstraintsAnnotationKey, noderesources.FitName).Obj(),
			existingPods: []*v1.Pod{
				testinghelper.MakePod().Namespace("default").Name("p1").UID("p1").Node("n2").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(10).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).
					Label("key", "val").Obj(),
				testinghelper.MakePod().Namespace("default").Name("p2").UID("p2").Node("n2").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(11).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).
					Label("key", "val2").Obj(),
				testinghelper.MakePod().Namespace("default").Name("p3").UID("p3").Node("n2").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(12).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).
					Label("key", "val2").Obj(),
				testinghelper.MakePod().Namespace("default").Name("p4").UID("p4").Node("n2").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(13).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).
					Label("key", "val2").Obj(),
				testinghelper.MakePod().Namespace("default").Name("p5").UID("p5").Node("n2").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(14).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).Obj(),
				testinghelper.MakePod().Namespace("default").Name("p6").UID("p6").Node("n2").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(15).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).Obj(),
				testinghelper.MakePod().Namespace("default").Name("p7").UID("p7").Node("n2").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(16).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).Obj(),
				testinghelper.MakePod().Namespace("default").Name("p8").UID("p8").Node("n2").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(17).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).Obj(),
				testinghelper.MakePod().Namespace("default").Name("p9").UID("p9").Node("n1").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(15).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).Obj(),
				testinghelper.MakePod().Namespace("default").Name("p10").UID("p10").Node("n1").Req(map[v1.ResourceName]string{v1.ResourceCPU: "6"}).Priority(15).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).Obj(),
			},
			nodeList: []*v1.Node{
				testinghelper.MakeNode().Name("n1").Capacity(map[v1.ResourceName]string{v1.ResourceCPU: "8"}).Obj(),
				testinghelper.MakeNode().Name("n2").Capacity(map[v1.ResourceName]string{v1.ResourceCPU: "8"}).Obj(),
			},
			pdbs: []*policy.PodDisruptionBudget{
				testinghelper.MakePdb().Namespace("default").Name("pdb").DisruptionsAllowed(0).
					Label("key", "val").Obj(),
				testinghelper.MakePdb().Namespace("default").Name("pdb2").DisruptionsAllowed(1).
					Label("key", "val2").Obj(),
			},
			expectedNodeIndexes: [][]int{
				{1, 0},
				{1, 0},
			},
			expectedMaxVictimPriority: 14,
		},
		{
			name: "second and sixth",
			pod: testinghelper.MakePod().Namespace("default").Name("p").UID("p").Req(map[v1.ResourceName]string{v1.ResourceCPU: "2"}).Priority(20).
				Annotation(constraints.HardConstraintsAnnotationKey, noderesources.FitName).Obj(),
			existingPods: []*v1.Pod{
				testinghelper.MakePod().Namespace("default").Name("p1").UID("p1").Node("n2").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(10).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).
					Label("key", "val").Obj(),
				testinghelper.MakePod().Namespace("default").Name("p2").UID("p2").Node("n2").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(11).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).
					Label("key", "val2").Obj(),
				testinghelper.MakePod().Namespace("default").Name("p3").UID("p3").Node("n2").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(12).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).
					Label("key", "val2").Obj(),
				testinghelper.MakePod().Namespace("default").Name("p4").UID("p4").Node("n2").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(13).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).
					Label("key", "val2").Obj(),
				testinghelper.MakePod().Namespace("default").Name("p5").UID("p5").Node("n2").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(14).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).
					Label("key", "val2").Obj(),
				testinghelper.MakePod().Namespace("default").Name("p6").UID("p6").Node("n2").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(15).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).Obj(),
				testinghelper.MakePod().Namespace("default").Name("p7").UID("p7").Node("n2").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(16).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).Obj(),
				testinghelper.MakePod().Namespace("default").Name("p8").UID("p8").Node("n2").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(17).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).Obj(),
				testinghelper.MakePod().Namespace("default").Name("p9").UID("p9").Node("n1").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(16).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).Obj(),
				testinghelper.MakePod().Namespace("default").Name("p10").UID("p10").Node("n1").Req(map[v1.ResourceName]string{v1.ResourceCPU: "6"}).Priority(16).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).Obj(),
			},
			nodeList: []*v1.Node{
				testinghelper.MakeNode().Name("n1").Capacity(map[v1.ResourceName]string{v1.ResourceCPU: "8"}).Obj(),
				testinghelper.MakeNode().Name("n2").Capacity(map[v1.ResourceName]string{v1.ResourceCPU: "8"}).Obj(),
			},
			pdbs: []*policy.PodDisruptionBudget{
				testinghelper.MakePdb().Namespace("default").Name("pdb").DisruptionsAllowed(0).
					Label("key", "val").Obj(),
				testinghelper.MakePdb().Namespace("default").Name("pdb2").DisruptionsAllowed(1).
					Label("key", "val2").Obj(),
			},
			expectedNodeIndexes: [][]int{
				{1, 0},
				{1},
			},
			expectedMaxVictimPriority: 15,
		},
		{
			name: "second and seventh",
			pod: testinghelper.MakePod().Namespace("default").Name("p").UID("p").Req(map[v1.ResourceName]string{v1.ResourceCPU: "2"}).Priority(20).
				Annotation(constraints.HardConstraintsAnnotationKey, noderesources.FitName).Obj(),
			existingPods: []*v1.Pod{
				testinghelper.MakePod().Namespace("default").Name("p1").UID("p1").Node("n2").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(10).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).
					Label("key", "val").Obj(),
				testinghelper.MakePod().Namespace("default").Name("p2").UID("p2").Node("n2").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(11).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).
					Label("key", "val2").Obj(),
				testinghelper.MakePod().Namespace("default").Name("p3").UID("p3").Node("n2").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(12).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).
					Label("key", "val2").Obj(),
				testinghelper.MakePod().Namespace("default").Name("p4").UID("p4").Node("n2").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(13).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).
					Label("key", "val2").Obj(),
				testinghelper.MakePod().Namespace("default").Name("p5").UID("p5").Node("n2").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(14).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).
					Label("key", "val2").Obj(),
				testinghelper.MakePod().Namespace("default").Name("p6").UID("p6").Node("n2").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(15).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).
					Label("key", "val2").Obj(),
				testinghelper.MakePod().Namespace("default").Name("p7").UID("p7").Node("n2").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(16).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).Obj(),
				testinghelper.MakePod().Namespace("default").Name("p8").UID("p8").Node("n2").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(17).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).Obj(),
				testinghelper.MakePod().Namespace("default").Name("p9").UID("p9").Node("n1").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(17).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).Obj(),
				testinghelper.MakePod().Namespace("default").Name("p10").UID("p10").Node("n1").Req(map[v1.ResourceName]string{v1.ResourceCPU: "6"}).Priority(17).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).Obj(),
			},
			nodeList: []*v1.Node{
				testinghelper.MakeNode().Name("n1").Capacity(map[v1.ResourceName]string{v1.ResourceCPU: "8"}).Obj(),
				testinghelper.MakeNode().Name("n2").Capacity(map[v1.ResourceName]string{v1.ResourceCPU: "8"}).Obj(),
			},
			pdbs: []*policy.PodDisruptionBudget{
				testinghelper.MakePdb().Namespace("default").Name("pdb").DisruptionsAllowed(0).
					Label("key", "val").Obj(),
				testinghelper.MakePdb().Namespace("default").Name("pdb2").DisruptionsAllowed(1).
					Label("key", "val2").Obj(),
			},
			expectedNodeIndexes: [][]int{
				{1, 0},
				{1},
			},
			expectedMaxVictimPriority: 16,
		},
		{
			name: "second and eighth",
			pod: testinghelper.MakePod().Namespace("default").Name("p").UID("p").Req(map[v1.ResourceName]string{v1.ResourceCPU: "2"}).Priority(20).
				Annotation(constraints.HardConstraintsAnnotationKey, noderesources.FitName).Obj(),
			existingPods: []*v1.Pod{
				testinghelper.MakePod().Namespace("default").Name("p1").UID("p1").Node("n2").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(10).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).
					Label("key", "val").Obj(),
				testinghelper.MakePod().Namespace("default").Name("p2").UID("p2").Node("n2").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(11).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).
					Label("key", "val2").Obj(),
				testinghelper.MakePod().Namespace("default").Name("p3").UID("p3").Node("n2").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(12).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).
					Label("key", "val2").Obj(),
				testinghelper.MakePod().Namespace("default").Name("p4").UID("p4").Node("n2").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(13).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).
					Label("key", "val2").Obj(),
				testinghelper.MakePod().Namespace("default").Name("p5").UID("p5").Node("n2").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(14).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).
					Label("key", "val2").Obj(),
				testinghelper.MakePod().Namespace("default").Name("p6").UID("p6").Node("n2").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(15).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).
					Label("key", "val2").Obj(),
				testinghelper.MakePod().Namespace("default").Name("p7").UID("p7").Node("n2").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(16).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).
					Label("key", "val2").Obj(),
				testinghelper.MakePod().Namespace("default").Name("p8").UID("p8").Node("n2").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(17).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).Obj(),
				testinghelper.MakePod().Namespace("default").Name("p9").UID("p9").Node("n1").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(18).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).Obj(),
				testinghelper.MakePod().Namespace("default").Name("p10").UID("p10").Node("n1").Req(map[v1.ResourceName]string{v1.ResourceCPU: "6"}).Priority(18).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).Obj(),
			},
			nodeList: []*v1.Node{
				testinghelper.MakeNode().Name("n1").Capacity(map[v1.ResourceName]string{v1.ResourceCPU: "8"}).Obj(),
				testinghelper.MakeNode().Name("n2").Capacity(map[v1.ResourceName]string{v1.ResourceCPU: "8"}).Obj(),
			},
			pdbs: []*policy.PodDisruptionBudget{
				testinghelper.MakePdb().Namespace("default").Name("pdb").DisruptionsAllowed(0).
					Label("key", "val").Obj(),
				testinghelper.MakePdb().Namespace("default").Name("pdb2").DisruptionsAllowed(1).
					Label("key", "val2").Obj(),
			},
			expectedNodeIndexes: [][]int{
				{1, 0},
				{1},
			},
			expectedMaxVictimPriority: 17,
		},
		{
			name: "third and forth",
			pod: testinghelper.MakePod().Namespace("default").Name("p").UID("p").Req(map[v1.ResourceName]string{v1.ResourceCPU: "2"}).Priority(20).
				Annotation(constraints.HardConstraintsAnnotationKey, noderesources.FitName).Obj(),
			existingPods: []*v1.Pod{
				testinghelper.MakePod().Namespace("default").Name("p1").UID("p1").Node("n2").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(10).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).
					Label("key", "val").Obj(),
				testinghelper.MakePod().Namespace("default").Name("p2").UID("p2").Node("n2").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(11).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).
					Label("key", "val").Obj(),
				testinghelper.MakePod().Namespace("default").Name("p3").UID("p3").Node("n2").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(12).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).
					Label("key", "val2").Obj(),
				testinghelper.MakePod().Namespace("default").Name("p4").UID("p4").Node("n2").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(13).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).Obj(),
				testinghelper.MakePod().Namespace("default").Name("p5").UID("p5").Node("n2").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(14).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).Obj(),
				testinghelper.MakePod().Namespace("default").Name("p6").UID("p6").Node("n2").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(15).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).Obj(),
				testinghelper.MakePod().Namespace("default").Name("p7").UID("p7").Node("n2").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(16).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).Obj(),
				testinghelper.MakePod().Namespace("default").Name("p8").UID("p8").Node("n2").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(17).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).Obj(),
				testinghelper.MakePod().Namespace("default").Name("p9").UID("p9").Node("n1").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(14).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).Obj(),
				testinghelper.MakePod().Namespace("default").Name("p10").UID("p10").Node("n1").Req(map[v1.ResourceName]string{v1.ResourceCPU: "6"}).Priority(14).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).Obj(),
			},
			nodeList: []*v1.Node{
				testinghelper.MakeNode().Name("n1").Capacity(map[v1.ResourceName]string{v1.ResourceCPU: "8"}).Obj(),
				testinghelper.MakeNode().Name("n2").Capacity(map[v1.ResourceName]string{v1.ResourceCPU: "8"}).Obj(),
			},
			pdbs: []*policy.PodDisruptionBudget{
				testinghelper.MakePdb().Namespace("default").Name("pdb").DisruptionsAllowed(0).
					Label("key", "val").Obj(),
				testinghelper.MakePdb().Namespace("default").Name("pdb2").DisruptionsAllowed(1).
					Label("key", "val2").Obj(),
			},
			expectedNodeIndexes: [][]int{
				{1, 0},
				{1},
			},
			expectedMaxVictimPriority: 13,
		},
		{
			name: "third and fifth",
			pod: testinghelper.MakePod().Namespace("default").Name("p").UID("p").Req(map[v1.ResourceName]string{v1.ResourceCPU: "2"}).Priority(20).
				Annotation(constraints.HardConstraintsAnnotationKey, noderesources.FitName).Obj(),
			existingPods: []*v1.Pod{
				testinghelper.MakePod().Namespace("default").Name("p1").UID("p1").Node("n2").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(10).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).
					Label("key", "val").Obj(),
				testinghelper.MakePod().Namespace("default").Name("p2").UID("p2").Node("n2").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(11).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).
					Label("key", "val").Obj(),
				testinghelper.MakePod().Namespace("default").Name("p3").UID("p3").Node("n2").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(12).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).
					Label("key", "val2").Obj(),
				testinghelper.MakePod().Namespace("default").Name("p4").UID("p4").Node("n2").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(13).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).
					Label("key", "val2").Obj(),
				testinghelper.MakePod().Namespace("default").Name("p5").UID("p5").Node("n2").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(14).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).Obj(),
				testinghelper.MakePod().Namespace("default").Name("p6").UID("p6").Node("n2").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(15).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).Obj(),
				testinghelper.MakePod().Namespace("default").Name("p7").UID("p7").Node("n2").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(16).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).Obj(),
				testinghelper.MakePod().Namespace("default").Name("p8").UID("p8").Node("n2").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(17).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).Obj(),
				testinghelper.MakePod().Namespace("default").Name("p9").UID("p9").Node("n1").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(15).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).Obj(),
				testinghelper.MakePod().Namespace("default").Name("p10").UID("p10").Node("n1").Req(map[v1.ResourceName]string{v1.ResourceCPU: "6"}).Priority(15).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).Obj(),
			},
			nodeList: []*v1.Node{
				testinghelper.MakeNode().Name("n1").Capacity(map[v1.ResourceName]string{v1.ResourceCPU: "8"}).Obj(),
				testinghelper.MakeNode().Name("n2").Capacity(map[v1.ResourceName]string{v1.ResourceCPU: "8"}).Obj(),
			},
			pdbs: []*policy.PodDisruptionBudget{
				testinghelper.MakePdb().Namespace("default").Name("pdb").DisruptionsAllowed(0).
					Label("key", "val").Obj(),
				testinghelper.MakePdb().Namespace("default").Name("pdb2").DisruptionsAllowed(1).
					Label("key", "val2").Obj(),
			},
			expectedNodeIndexes: [][]int{
				{1, 0},
				{1, 0},
			},
			expectedMaxVictimPriority: 14,
		},
		{
			name: "third and sixth",
			pod: testinghelper.MakePod().Namespace("default").Name("p").UID("p").Req(map[v1.ResourceName]string{v1.ResourceCPU: "2"}).Priority(20).
				Annotation(constraints.HardConstraintsAnnotationKey, noderesources.FitName).Obj(),
			existingPods: []*v1.Pod{
				testinghelper.MakePod().Namespace("default").Name("p1").UID("p1").Node("n2").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(10).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).
					Label("key", "val").Obj(),
				testinghelper.MakePod().Namespace("default").Name("p2").UID("p2").Node("n2").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(11).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).
					Label("key", "val").Obj(),
				testinghelper.MakePod().Namespace("default").Name("p3").UID("p3").Node("n2").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(12).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).
					Label("key", "val2").Obj(),
				testinghelper.MakePod().Namespace("default").Name("p4").UID("p4").Node("n2").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(13).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).
					Label("key", "val2").Obj(),
				testinghelper.MakePod().Namespace("default").Name("p5").UID("p5").Node("n2").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(14).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).
					Label("key", "val2").Obj(),
				testinghelper.MakePod().Namespace("default").Name("p6").UID("p6").Node("n2").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(15).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).Obj(),
				testinghelper.MakePod().Namespace("default").Name("p7").UID("p7").Node("n2").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(16).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).Obj(),
				testinghelper.MakePod().Namespace("default").Name("p8").UID("p8").Node("n2").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(17).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).Obj(),
				testinghelper.MakePod().Namespace("default").Name("p9").UID("p9").Node("n1").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(16).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).Obj(),
				testinghelper.MakePod().Namespace("default").Name("p10").UID("p10").Node("n1").Req(map[v1.ResourceName]string{v1.ResourceCPU: "6"}).Priority(16).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).Obj(),
			},
			nodeList: []*v1.Node{
				testinghelper.MakeNode().Name("n1").Capacity(map[v1.ResourceName]string{v1.ResourceCPU: "8"}).Obj(),
				testinghelper.MakeNode().Name("n2").Capacity(map[v1.ResourceName]string{v1.ResourceCPU: "8"}).Obj(),
			},
			pdbs: []*policy.PodDisruptionBudget{
				testinghelper.MakePdb().Namespace("default").Name("pdb").DisruptionsAllowed(0).
					Label("key", "val").Obj(),
				testinghelper.MakePdb().Namespace("default").Name("pdb2").DisruptionsAllowed(1).
					Label("key", "val2").Obj(),
			},
			expectedNodeIndexes: [][]int{
				{1, 0},
				{1},
			},
			expectedMaxVictimPriority: 15,
		},
		{
			name: "third and seventh",
			pod: testinghelper.MakePod().Namespace("default").Name("p").UID("p").Req(map[v1.ResourceName]string{v1.ResourceCPU: "2"}).Priority(20).
				Annotation(constraints.HardConstraintsAnnotationKey, noderesources.FitName).Obj(),
			existingPods: []*v1.Pod{
				testinghelper.MakePod().Namespace("default").Name("p1").UID("p1").Node("n2").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(10).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).
					Label("key", "val").Obj(),
				testinghelper.MakePod().Namespace("default").Name("p2").UID("p2").Node("n2").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(11).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).
					Label("key", "val").Obj(),
				testinghelper.MakePod().Namespace("default").Name("p3").UID("p3").Node("n2").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(12).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).
					Label("key", "val2").Obj(),
				testinghelper.MakePod().Namespace("default").Name("p4").UID("p4").Node("n2").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(13).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).
					Label("key", "val2").Obj(),
				testinghelper.MakePod().Namespace("default").Name("p5").UID("p5").Node("n2").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(14).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).
					Label("key", "val2").Obj(),
				testinghelper.MakePod().Namespace("default").Name("p6").UID("p6").Node("n2").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(15).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).
					Label("key", "val2").Obj(),
				testinghelper.MakePod().Namespace("default").Name("p7").UID("p7").Node("n2").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(16).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).Obj(),
				testinghelper.MakePod().Namespace("default").Name("p8").UID("p8").Node("n2").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(17).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).Obj(),
				testinghelper.MakePod().Namespace("default").Name("p9").UID("p9").Node("n1").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(17).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).Obj(),
				testinghelper.MakePod().Namespace("default").Name("p10").UID("p10").Node("n1").Req(map[v1.ResourceName]string{v1.ResourceCPU: "6"}).Priority(17).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).Obj(),
			},
			nodeList: []*v1.Node{
				testinghelper.MakeNode().Name("n1").Capacity(map[v1.ResourceName]string{v1.ResourceCPU: "8"}).Obj(),
				testinghelper.MakeNode().Name("n2").Capacity(map[v1.ResourceName]string{v1.ResourceCPU: "8"}).Obj(),
			},
			pdbs: []*policy.PodDisruptionBudget{
				testinghelper.MakePdb().Namespace("default").Name("pdb").DisruptionsAllowed(0).
					Label("key", "val").Obj(),
				testinghelper.MakePdb().Namespace("default").Name("pdb2").DisruptionsAllowed(1).
					Label("key", "val2").Obj(),
			},
			expectedNodeIndexes: [][]int{
				{1, 0},
				{1},
			},
			expectedMaxVictimPriority: 16,
		},
		{
			name: "third and eighth",
			pod: testinghelper.MakePod().Namespace("default").Name("p").UID("p").Req(map[v1.ResourceName]string{v1.ResourceCPU: "2"}).Priority(20).
				Annotation(constraints.HardConstraintsAnnotationKey, noderesources.FitName).Obj(),
			existingPods: []*v1.Pod{
				testinghelper.MakePod().Namespace("default").Name("p1").UID("p1").Node("n2").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(10).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).
					Label("key", "val").Obj(),
				testinghelper.MakePod().Namespace("default").Name("p2").UID("p2").Node("n2").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(11).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).
					Label("key", "val").Obj(),
				testinghelper.MakePod().Namespace("default").Name("p3").UID("p3").Node("n2").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(12).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).
					Label("key", "val2").Obj(),
				testinghelper.MakePod().Namespace("default").Name("p4").UID("p4").Node("n2").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(13).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).
					Label("key", "val2").Obj(),
				testinghelper.MakePod().Namespace("default").Name("p5").UID("p5").Node("n2").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(14).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).
					Label("key", "val2").Obj(),
				testinghelper.MakePod().Namespace("default").Name("p6").UID("p6").Node("n2").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(15).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).
					Label("key", "val2").Obj(),
				testinghelper.MakePod().Namespace("default").Name("p7").UID("p7").Node("n2").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(16).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).
					Label("key", "val2").Obj(),
				testinghelper.MakePod().Namespace("default").Name("p8").UID("p8").Node("n2").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(17).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).Obj(),
				testinghelper.MakePod().Namespace("default").Name("p9").UID("p9").Node("n1").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(18).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).Obj(),
				testinghelper.MakePod().Namespace("default").Name("p10").UID("p10").Node("n1").Req(map[v1.ResourceName]string{v1.ResourceCPU: "6"}).Priority(18).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).Obj(),
			},
			nodeList: []*v1.Node{
				testinghelper.MakeNode().Name("n1").Capacity(map[v1.ResourceName]string{v1.ResourceCPU: "8"}).Obj(),
				testinghelper.MakeNode().Name("n2").Capacity(map[v1.ResourceName]string{v1.ResourceCPU: "8"}).Obj(),
			},
			pdbs: []*policy.PodDisruptionBudget{
				testinghelper.MakePdb().Namespace("default").Name("pdb").DisruptionsAllowed(0).
					Label("key", "val").Obj(),
				testinghelper.MakePdb().Namespace("default").Name("pdb2").DisruptionsAllowed(1).
					Label("key", "val2").Obj(),
			},
			expectedNodeIndexes: [][]int{
				{1, 0},
				{1},
			},
			expectedMaxVictimPriority: 17,
		},
		{
			name: "forth and fifth",
			pod: testinghelper.MakePod().Namespace("default").Name("p").UID("p").Req(map[v1.ResourceName]string{v1.ResourceCPU: "2"}).Priority(20).
				Annotation(constraints.HardConstraintsAnnotationKey, noderesources.FitName).Obj(),
			existingPods: []*v1.Pod{
				testinghelper.MakePod().Namespace("default").Name("p1").UID("p1").Node("n2").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(10).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).
					Label("key", "val").Obj(),
				testinghelper.MakePod().Namespace("default").Name("p2").UID("p2").Node("n2").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(11).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).
					Label("key", "val").Obj(),
				testinghelper.MakePod().Namespace("default").Name("p3").UID("p3").Node("n2").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(12).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).
					Label("key", "val").Obj(),
				testinghelper.MakePod().Namespace("default").Name("p4").UID("p4").Node("n2").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(13).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).
					Label("key", "val2").Obj(),
				testinghelper.MakePod().Namespace("default").Name("p5").UID("p5").Node("n2").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(14).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).Obj(),
				testinghelper.MakePod().Namespace("default").Name("p6").UID("p6").Node("n2").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(15).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).Obj(),
				testinghelper.MakePod().Namespace("default").Name("p7").UID("p7").Node("n2").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(16).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).Obj(),
				testinghelper.MakePod().Namespace("default").Name("p8").UID("p8").Node("n2").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(17).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).Obj(),
				testinghelper.MakePod().Namespace("default").Name("p9").UID("p9").Node("n1").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(15).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).Obj(),
				testinghelper.MakePod().Namespace("default").Name("p10").UID("p10").Node("n1").Req(map[v1.ResourceName]string{v1.ResourceCPU: "6"}).Priority(15).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).Obj(),
			},
			nodeList: []*v1.Node{
				testinghelper.MakeNode().Name("n1").Capacity(map[v1.ResourceName]string{v1.ResourceCPU: "8"}).Obj(),
				testinghelper.MakeNode().Name("n2").Capacity(map[v1.ResourceName]string{v1.ResourceCPU: "8"}).Obj(),
			},
			pdbs: []*policy.PodDisruptionBudget{
				testinghelper.MakePdb().Namespace("default").Name("pdb").DisruptionsAllowed(0).
					Label("key", "val").Obj(),
				testinghelper.MakePdb().Namespace("default").Name("pdb2").DisruptionsAllowed(1).
					Label("key", "val2").Obj(),
			},
			expectedNodeIndexes: [][]int{
				{1, 0},
				{1, 0},
			},
			expectedMaxVictimPriority: 14,
		},
		{
			name: "forth and sixth",
			pod: testinghelper.MakePod().Namespace("default").Name("p").UID("p").Req(map[v1.ResourceName]string{v1.ResourceCPU: "2"}).Priority(20).
				Annotation(constraints.HardConstraintsAnnotationKey, noderesources.FitName).Obj(),
			existingPods: []*v1.Pod{
				testinghelper.MakePod().Namespace("default").Name("p1").UID("p1").Node("n2").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(10).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).
					Label("key", "val").Obj(),
				testinghelper.MakePod().Namespace("default").Name("p2").UID("p2").Node("n2").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(11).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).
					Label("key", "val").Obj(),
				testinghelper.MakePod().Namespace("default").Name("p3").UID("p3").Node("n2").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(12).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).
					Label("key", "val").Obj(),
				testinghelper.MakePod().Namespace("default").Name("p4").UID("p4").Node("n2").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(13).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).
					Label("key", "val2").Obj(),
				testinghelper.MakePod().Namespace("default").Name("p5").UID("p5").Node("n2").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(14).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).
					Label("key", "val2").Obj(),
				testinghelper.MakePod().Namespace("default").Name("p6").UID("p6").Node("n2").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(15).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).Obj(),
				testinghelper.MakePod().Namespace("default").Name("p7").UID("p7").Node("n2").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(16).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).Obj(),
				testinghelper.MakePod().Namespace("default").Name("p8").UID("p8").Node("n2").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(17).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).Obj(),
				testinghelper.MakePod().Namespace("default").Name("p9").UID("p9").Node("n1").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(16).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).Obj(),
				testinghelper.MakePod().Namespace("default").Name("p10").UID("p10").Node("n1").Req(map[v1.ResourceName]string{v1.ResourceCPU: "6"}).Priority(16).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).Obj(),
			},
			nodeList: []*v1.Node{
				testinghelper.MakeNode().Name("n1").Capacity(map[v1.ResourceName]string{v1.ResourceCPU: "8"}).Obj(),
				testinghelper.MakeNode().Name("n2").Capacity(map[v1.ResourceName]string{v1.ResourceCPU: "8"}).Obj(),
			},
			pdbs: []*policy.PodDisruptionBudget{
				testinghelper.MakePdb().Namespace("default").Name("pdb").DisruptionsAllowed(0).
					Label("key", "val").Obj(),
				testinghelper.MakePdb().Namespace("default").Name("pdb2").DisruptionsAllowed(1).
					Label("key", "val2").Obj(),
			},
			expectedNodeIndexes: [][]int{
				{1, 0},
				{1},
			},
			expectedMaxVictimPriority: 15,
		},
		{
			name: "forth and seventh",
			pod: testinghelper.MakePod().Namespace("default").Name("p").UID("p").Req(map[v1.ResourceName]string{v1.ResourceCPU: "2"}).Priority(20).
				Annotation(constraints.HardConstraintsAnnotationKey, noderesources.FitName).Obj(),
			existingPods: []*v1.Pod{
				testinghelper.MakePod().Namespace("default").Name("p1").UID("p1").Node("n2").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(10).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).
					Label("key", "val").Obj(),
				testinghelper.MakePod().Namespace("default").Name("p2").UID("p2").Node("n2").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(11).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).
					Label("key", "val").Obj(),
				testinghelper.MakePod().Namespace("default").Name("p3").UID("p3").Node("n2").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(12).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).
					Label("key", "val").Obj(),
				testinghelper.MakePod().Namespace("default").Name("p4").UID("p4").Node("n2").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(13).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).
					Label("key", "val2").Obj(),
				testinghelper.MakePod().Namespace("default").Name("p5").UID("p5").Node("n2").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(14).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).
					Label("key", "val2").Obj(),
				testinghelper.MakePod().Namespace("default").Name("p6").UID("p6").Node("n2").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(15).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).
					Label("key", "val2").Obj(),
				testinghelper.MakePod().Namespace("default").Name("p7").UID("p7").Node("n2").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(16).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).Obj(),
				testinghelper.MakePod().Namespace("default").Name("p8").UID("p8").Node("n2").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(17).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).Obj(),
				testinghelper.MakePod().Namespace("default").Name("p9").UID("p9").Node("n1").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(17).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).Obj(),
				testinghelper.MakePod().Namespace("default").Name("p10").UID("p10").Node("n1").Req(map[v1.ResourceName]string{v1.ResourceCPU: "6"}).Priority(17).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).Obj(),
			},
			nodeList: []*v1.Node{
				testinghelper.MakeNode().Name("n1").Capacity(map[v1.ResourceName]string{v1.ResourceCPU: "8"}).Obj(),
				testinghelper.MakeNode().Name("n2").Capacity(map[v1.ResourceName]string{v1.ResourceCPU: "8"}).Obj(),
			},
			pdbs: []*policy.PodDisruptionBudget{
				testinghelper.MakePdb().Namespace("default").Name("pdb").DisruptionsAllowed(0).
					Label("key", "val").Obj(),
				testinghelper.MakePdb().Namespace("default").Name("pdb2").DisruptionsAllowed(1).
					Label("key", "val2").Obj(),
			},
			expectedNodeIndexes: [][]int{
				{1, 0},
				{1},
			},
			expectedMaxVictimPriority: 16,
		},
		{
			name: "forth and eighth",
			pod: testinghelper.MakePod().Namespace("default").Name("p").UID("p").Req(map[v1.ResourceName]string{v1.ResourceCPU: "2"}).Priority(20).
				Annotation(constraints.HardConstraintsAnnotationKey, noderesources.FitName).Obj(),
			existingPods: []*v1.Pod{
				testinghelper.MakePod().Namespace("default").Name("p1").UID("p1").Node("n2").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(10).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).
					Label("key", "val").Obj(),
				testinghelper.MakePod().Namespace("default").Name("p2").UID("p2").Node("n2").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(11).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).
					Label("key", "val").Obj(),
				testinghelper.MakePod().Namespace("default").Name("p3").UID("p3").Node("n2").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(12).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).
					Label("key", "val").Obj(),
				testinghelper.MakePod().Namespace("default").Name("p4").UID("p4").Node("n2").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(13).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).
					Label("key", "val2").Obj(),
				testinghelper.MakePod().Namespace("default").Name("p5").UID("p5").Node("n2").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(14).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).
					Label("key", "val2").Obj(),
				testinghelper.MakePod().Namespace("default").Name("p6").UID("p6").Node("n2").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(15).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).
					Label("key", "val2").Obj(),
				testinghelper.MakePod().Namespace("default").Name("p7").UID("p7").Node("n2").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(16).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).
					Label("key", "val2").Obj(),
				testinghelper.MakePod().Namespace("default").Name("p8").UID("p8").Node("n2").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(17).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).Obj(),
				testinghelper.MakePod().Namespace("default").Name("p9").UID("p9").Node("n1").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(18).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).Obj(),
				testinghelper.MakePod().Namespace("default").Name("p10").UID("p10").Node("n1").Req(map[v1.ResourceName]string{v1.ResourceCPU: "6"}).Priority(18).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).Obj(),
			},
			nodeList: []*v1.Node{
				testinghelper.MakeNode().Name("n1").Capacity(map[v1.ResourceName]string{v1.ResourceCPU: "8"}).Obj(),
				testinghelper.MakeNode().Name("n2").Capacity(map[v1.ResourceName]string{v1.ResourceCPU: "8"}).Obj(),
			},
			pdbs: []*policy.PodDisruptionBudget{
				testinghelper.MakePdb().Namespace("default").Name("pdb").DisruptionsAllowed(0).
					Label("key", "val").Obj(),
				testinghelper.MakePdb().Namespace("default").Name("pdb2").DisruptionsAllowed(1).
					Label("key", "val2").Obj(),
			},
			expectedNodeIndexes: [][]int{
				{1, 0},
				{1},
			},
			expectedMaxVictimPriority: 17,
		},
		{
			name: "fifth and sixth",
			pod: testinghelper.MakePod().Namespace("default").Name("p").UID("p").Req(map[v1.ResourceName]string{v1.ResourceCPU: "2"}).Priority(20).
				Annotation(constraints.HardConstraintsAnnotationKey, noderesources.FitName).Obj(),
			existingPods: []*v1.Pod{
				testinghelper.MakePod().Namespace("default").Name("p1").UID("p1").Node("n2").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(10).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).
					Label("key", "val").Obj(),
				testinghelper.MakePod().Namespace("default").Name("p2").UID("p2").Node("n2").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(11).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).
					Label("key", "val").Obj(),
				testinghelper.MakePod().Namespace("default").Name("p3").UID("p3").Node("n2").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(12).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).
					Label("key", "val").Obj(),
				testinghelper.MakePod().Namespace("default").Name("p4").UID("p4").Node("n2").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(13).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).
					Label("key", "val").Obj(),
				testinghelper.MakePod().Namespace("default").Name("p5").UID("p5").Node("n2").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(14).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).
					Label("key", "val2").Obj(),
				testinghelper.MakePod().Namespace("default").Name("p6").UID("p6").Node("n2").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(15).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).Obj(),
				testinghelper.MakePod().Namespace("default").Name("p7").UID("p7").Node("n2").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(16).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).Obj(),
				testinghelper.MakePod().Namespace("default").Name("p8").UID("p8").Node("n2").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(17).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).Obj(),
				testinghelper.MakePod().Namespace("default").Name("p9").UID("p9").Node("n1").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(16).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).Obj(),
				testinghelper.MakePod().Namespace("default").Name("p10").UID("p10").Node("n1").Req(map[v1.ResourceName]string{v1.ResourceCPU: "6"}).Priority(16).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).Obj(),
			},
			nodeList: []*v1.Node{
				testinghelper.MakeNode().Name("n1").Capacity(map[v1.ResourceName]string{v1.ResourceCPU: "8"}).Obj(),
				testinghelper.MakeNode().Name("n2").Capacity(map[v1.ResourceName]string{v1.ResourceCPU: "8"}).Obj(),
			},
			pdbs: []*policy.PodDisruptionBudget{
				testinghelper.MakePdb().Namespace("default").Name("pdb").DisruptionsAllowed(0).
					Label("key", "val").Obj(),
				testinghelper.MakePdb().Namespace("default").Name("pdb2").DisruptionsAllowed(1).
					Label("key", "val2").Obj(),
			},
			expectedNodeIndexes: [][]int{
				{1, 0},
				{1},
			},
			expectedMaxVictimPriority: 15,
		},
		{
			name: "fifth and seventh",
			pod: testinghelper.MakePod().Namespace("default").Name("p").UID("p").Req(map[v1.ResourceName]string{v1.ResourceCPU: "2"}).Priority(20).
				Annotation(constraints.HardConstraintsAnnotationKey, noderesources.FitName).Obj(),
			existingPods: []*v1.Pod{
				testinghelper.MakePod().Namespace("default").Name("p1").UID("p1").Node("n2").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(10).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).
					Label("key", "val").Obj(),
				testinghelper.MakePod().Namespace("default").Name("p2").UID("p2").Node("n2").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(11).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).
					Label("key", "val").Obj(),
				testinghelper.MakePod().Namespace("default").Name("p3").UID("p3").Node("n2").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(12).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).
					Label("key", "val").Obj(),
				testinghelper.MakePod().Namespace("default").Name("p4").UID("p4").Node("n2").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(13).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).
					Label("key", "val").Obj(),
				testinghelper.MakePod().Namespace("default").Name("p5").UID("p5").Node("n2").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(14).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).
					Label("key", "val2").Obj(),
				testinghelper.MakePod().Namespace("default").Name("p6").UID("p6").Node("n2").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(15).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).
					Label("key", "val2").Obj(),
				testinghelper.MakePod().Namespace("default").Name("p7").UID("p7").Node("n2").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(16).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).Obj(),
				testinghelper.MakePod().Namespace("default").Name("p8").UID("p8").Node("n2").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(17).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).Obj(),
				testinghelper.MakePod().Namespace("default").Name("p9").UID("p9").Node("n1").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(17).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).Obj(),
				testinghelper.MakePod().Namespace("default").Name("p10").UID("p10").Node("n1").Req(map[v1.ResourceName]string{v1.ResourceCPU: "6"}).Priority(17).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).Obj(),
			},
			nodeList: []*v1.Node{
				testinghelper.MakeNode().Name("n1").Capacity(map[v1.ResourceName]string{v1.ResourceCPU: "8"}).Obj(),
				testinghelper.MakeNode().Name("n2").Capacity(map[v1.ResourceName]string{v1.ResourceCPU: "8"}).Obj(),
			},
			pdbs: []*policy.PodDisruptionBudget{
				testinghelper.MakePdb().Namespace("default").Name("pdb").DisruptionsAllowed(0).
					Label("key", "val").Obj(),
				testinghelper.MakePdb().Namespace("default").Name("pdb2").DisruptionsAllowed(1).
					Label("key", "val2").Obj(),
			},
			expectedNodeIndexes: [][]int{
				{1, 0},
				{1},
			},
			expectedMaxVictimPriority: 16,
		},
		{
			name: "fifth and eighth",
			pod: testinghelper.MakePod().Namespace("default").Name("p").UID("p").Req(map[v1.ResourceName]string{v1.ResourceCPU: "2"}).Priority(20).
				Annotation(constraints.HardConstraintsAnnotationKey, noderesources.FitName).Obj(),
			existingPods: []*v1.Pod{
				testinghelper.MakePod().Namespace("default").Name("p1").UID("p1").Node("n2").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(10).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).
					Label("key", "val").Obj(),
				testinghelper.MakePod().Namespace("default").Name("p2").UID("p2").Node("n2").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(11).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).
					Label("key", "val").Obj(),
				testinghelper.MakePod().Namespace("default").Name("p3").UID("p3").Node("n2").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(12).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).
					Label("key", "val").Obj(),
				testinghelper.MakePod().Namespace("default").Name("p4").UID("p4").Node("n2").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(13).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).
					Label("key", "val").Obj(),
				testinghelper.MakePod().Namespace("default").Name("p5").UID("p5").Node("n2").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(14).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).
					Label("key", "val2").Obj(),
				testinghelper.MakePod().Namespace("default").Name("p6").UID("p6").Node("n2").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(15).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).
					Label("key", "val2").Obj(),
				testinghelper.MakePod().Namespace("default").Name("p7").UID("p7").Node("n2").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(16).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).
					Label("key", "val2").Obj(),
				testinghelper.MakePod().Namespace("default").Name("p8").UID("p8").Node("n2").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(17).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).Obj(),
				testinghelper.MakePod().Namespace("default").Name("p9").UID("p9").Node("n1").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(18).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).Obj(),
				testinghelper.MakePod().Namespace("default").Name("p10").UID("p10").Node("n1").Req(map[v1.ResourceName]string{v1.ResourceCPU: "6"}).Priority(18).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).Obj(),
			},
			nodeList: []*v1.Node{
				testinghelper.MakeNode().Name("n1").Capacity(map[v1.ResourceName]string{v1.ResourceCPU: "8"}).Obj(),
				testinghelper.MakeNode().Name("n2").Capacity(map[v1.ResourceName]string{v1.ResourceCPU: "8"}).Obj(),
			},
			pdbs: []*policy.PodDisruptionBudget{
				testinghelper.MakePdb().Namespace("default").Name("pdb").DisruptionsAllowed(0).
					Label("key", "val").Obj(),
				testinghelper.MakePdb().Namespace("default").Name("pdb2").DisruptionsAllowed(1).
					Label("key", "val2").Obj(),
			},
			expectedNodeIndexes: [][]int{
				{1, 0},
				{1},
			},
			expectedMaxVictimPriority: 17,
		},
		{
			name: "sixth and seventh",
			pod: testinghelper.MakePod().Namespace("default").Name("p").UID("p").Req(map[v1.ResourceName]string{v1.ResourceCPU: "2"}).Priority(20).
				Annotation(constraints.HardConstraintsAnnotationKey, noderesources.FitName).Obj(),
			existingPods: []*v1.Pod{
				testinghelper.MakePod().Namespace("default").Name("p1").UID("p1").Node("n2").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(10).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).
					Label("key", "val").Obj(),
				testinghelper.MakePod().Namespace("default").Name("p2").UID("p2").Node("n2").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(11).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).
					Label("key", "val").Obj(),
				testinghelper.MakePod().Namespace("default").Name("p3").UID("p3").Node("n2").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(12).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).
					Label("key", "val").Obj(),
				testinghelper.MakePod().Namespace("default").Name("p4").UID("p4").Node("n2").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(13).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).
					Label("key", "val").Obj(),
				testinghelper.MakePod().Namespace("default").Name("p5").UID("p5").Node("n2").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(14).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).
					Label("key", "val").Obj(),
				testinghelper.MakePod().Namespace("default").Name("p6").UID("p6").Node("n2").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(15).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).
					Label("key", "val2").Obj(),
				testinghelper.MakePod().Namespace("default").Name("p7").UID("p7").Node("n2").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(16).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).Obj(),
				testinghelper.MakePod().Namespace("default").Name("p8").UID("p8").Node("n2").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(17).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).Obj(),
				testinghelper.MakePod().Namespace("default").Name("p9").UID("p9").Node("n1").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(17).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).Obj(),
				testinghelper.MakePod().Namespace("default").Name("p10").UID("p10").Node("n1").Req(map[v1.ResourceName]string{v1.ResourceCPU: "6"}).Priority(17).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).Obj(),
			},
			nodeList: []*v1.Node{
				testinghelper.MakeNode().Name("n1").Capacity(map[v1.ResourceName]string{v1.ResourceCPU: "8"}).Obj(),
				testinghelper.MakeNode().Name("n2").Capacity(map[v1.ResourceName]string{v1.ResourceCPU: "8"}).Obj(),
			},
			pdbs: []*policy.PodDisruptionBudget{
				testinghelper.MakePdb().Namespace("default").Name("pdb").DisruptionsAllowed(0).
					Label("key", "val").Obj(),
				testinghelper.MakePdb().Namespace("default").Name("pdb2").DisruptionsAllowed(1).
					Label("key", "val2").Obj(),
			},
			expectedNodeIndexes: [][]int{
				{1, 0},
				{1},
			},
			expectedMaxVictimPriority: 16,
		},
		{
			name: "sixth and eighth",
			pod: testinghelper.MakePod().Namespace("default").Name("p").UID("p").Req(map[v1.ResourceName]string{v1.ResourceCPU: "2"}).Priority(20).
				Annotation(constraints.HardConstraintsAnnotationKey, noderesources.FitName).Obj(),
			existingPods: []*v1.Pod{
				testinghelper.MakePod().Namespace("default").Name("p1").UID("p1").Node("n2").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(10).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).
					Label("key", "val").Obj(),
				testinghelper.MakePod().Namespace("default").Name("p2").UID("p2").Node("n2").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(11).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).
					Label("key", "val").Obj(),
				testinghelper.MakePod().Namespace("default").Name("p3").UID("p3").Node("n2").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(12).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).
					Label("key", "val").Obj(),
				testinghelper.MakePod().Namespace("default").Name("p4").UID("p4").Node("n2").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(13).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).
					Label("key", "val").Obj(),
				testinghelper.MakePod().Namespace("default").Name("p5").UID("p5").Node("n2").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(14).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).
					Label("key", "val").Obj(),
				testinghelper.MakePod().Namespace("default").Name("p6").UID("p6").Node("n2").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(15).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).
					Label("key", "val2").Obj(),
				testinghelper.MakePod().Namespace("default").Name("p7").UID("p7").Node("n2").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(16).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).
					Label("key", "val2").Obj(),
				testinghelper.MakePod().Namespace("default").Name("p8").UID("p8").Node("n2").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(17).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).Obj(),
				testinghelper.MakePod().Namespace("default").Name("p9").UID("p9").Node("n1").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(18).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).Obj(),
				testinghelper.MakePod().Namespace("default").Name("p10").UID("p10").Node("n1").Req(map[v1.ResourceName]string{v1.ResourceCPU: "6"}).Priority(18).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).Obj(),
			},
			nodeList: []*v1.Node{
				testinghelper.MakeNode().Name("n1").Capacity(map[v1.ResourceName]string{v1.ResourceCPU: "8"}).Obj(),
				testinghelper.MakeNode().Name("n2").Capacity(map[v1.ResourceName]string{v1.ResourceCPU: "8"}).Obj(),
			},
			pdbs: []*policy.PodDisruptionBudget{
				testinghelper.MakePdb().Namespace("default").Name("pdb").DisruptionsAllowed(0).
					Label("key", "val").Obj(),
				testinghelper.MakePdb().Namespace("default").Name("pdb2").DisruptionsAllowed(1).
					Label("key", "val2").Obj(),
			},
			expectedNodeIndexes: [][]int{
				{1, 0},
				{1},
			},
			expectedMaxVictimPriority: 17,
		},
		{
			name: "seventh and eighth",
			pod: testinghelper.MakePod().Namespace("default").Name("p").UID("p").Req(map[v1.ResourceName]string{v1.ResourceCPU: "2"}).Priority(20).
				Annotation(constraints.HardConstraintsAnnotationKey, noderesources.FitName).Obj(),
			existingPods: []*v1.Pod{
				testinghelper.MakePod().Namespace("default").Name("p1").UID("p1").Node("n2").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(10).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).
					Label("key", "val").Obj(),
				testinghelper.MakePod().Namespace("default").Name("p2").UID("p2").Node("n2").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(11).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).
					Label("key", "val").Obj(),
				testinghelper.MakePod().Namespace("default").Name("p3").UID("p3").Node("n2").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(12).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).
					Label("key", "val").Obj(),
				testinghelper.MakePod().Namespace("default").Name("p4").UID("p4").Node("n2").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(13).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).
					Label("key", "val").Obj(),
				testinghelper.MakePod().Namespace("default").Name("p5").UID("p5").Node("n2").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(14).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).
					Label("key", "val").Obj(),
				testinghelper.MakePod().Namespace("default").Name("p6").UID("p6").Node("n2").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(15).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).
					Label("key", "val").Obj(),
				testinghelper.MakePod().Namespace("default").Name("p7").UID("p7").Node("n2").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(16).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).
					Label("key", "val2").Obj(),
				testinghelper.MakePod().Namespace("default").Name("p8").UID("p8").Node("n2").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(17).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).Obj(),
				testinghelper.MakePod().Namespace("default").Name("p9").UID("p9").Node("n1").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(18).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).Obj(),
				testinghelper.MakePod().Namespace("default").Name("p10").UID("p10").Node("n1").Req(map[v1.ResourceName]string{v1.ResourceCPU: "6"}).Priority(18).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).Obj(),
			},
			nodeList: []*v1.Node{
				testinghelper.MakeNode().Name("n1").Capacity(map[v1.ResourceName]string{v1.ResourceCPU: "8"}).Obj(),
				testinghelper.MakeNode().Name("n2").Capacity(map[v1.ResourceName]string{v1.ResourceCPU: "8"}).Obj(),
			},
			pdbs: []*policy.PodDisruptionBudget{
				testinghelper.MakePdb().Namespace("default").Name("pdb").DisruptionsAllowed(0).
					Label("key", "val").Obj(),
				testinghelper.MakePdb().Namespace("default").Name("pdb2").DisruptionsAllowed(1).
					Label("key", "val2").Obj(),
			},
			expectedNodeIndexes: [][]int{
				{1, 0},
				{1},
			},
			expectedMaxVictimPriority: 17,
		},
		{
			name: "can not be preempted",
			pod: testinghelper.MakePod().Namespace("default").Name("p").UID("p").Req(map[v1.ResourceName]string{v1.ResourceCPU: "2"}).Priority(20).
				Annotation(constraints.HardConstraintsAnnotationKey, noderesources.FitName).Obj(),
			existingPods: []*v1.Pod{
				testinghelper.MakePod().Namespace("default").Name("p1").UID("p1").Node("n2").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(10).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).
					Label("key", "val").Obj(),
				testinghelper.MakePod().Namespace("default").Name("p2").UID("p2").Node("n2").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(11).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).
					Label("key", "val").Obj(),
				testinghelper.MakePod().Namespace("default").Name("p3").UID("p3").Node("n2").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(12).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).
					Label("key", "val").Obj(),
				testinghelper.MakePod().Namespace("default").Name("p4").UID("p4").Node("n2").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(13).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).
					Label("key", "val").Obj(),
				testinghelper.MakePod().Namespace("default").Name("p5").UID("p5").Node("n2").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(14).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).
					Label("key", "val").Obj(),
				testinghelper.MakePod().Namespace("default").Name("p6").UID("p6").Node("n2").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(15).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).
					Label("key", "val").Obj(),
				testinghelper.MakePod().Namespace("default").Name("p7").UID("p7").Node("n2").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(16).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).
					Label("key", "val2").Obj(),
				testinghelper.MakePod().Namespace("default").Name("p8").UID("p8").Node("n2").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(17).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).
					Label("key", "val2").Obj(),
				testinghelper.MakePod().Namespace("default").Name("p9").UID("p9").Node("n1").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(18).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).
					Label("key", "val").Obj(),
				testinghelper.MakePod().Namespace("default").Name("p10").UID("p10").Node("n1").Req(map[v1.ResourceName]string{v1.ResourceCPU: "6"}).Priority(18).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).
					Label("key", "val").Obj(),
			},
			nodeList: []*v1.Node{
				testinghelper.MakeNode().Name("n1").Capacity(map[v1.ResourceName]string{v1.ResourceCPU: "8"}).Obj(),
				testinghelper.MakeNode().Name("n2").Capacity(map[v1.ResourceName]string{v1.ResourceCPU: "8"}).Obj(),
			},
			pdbs: []*policy.PodDisruptionBudget{
				testinghelper.MakePdb().Namespace("default").Name("pdb").DisruptionsAllowed(0).
					Label("key", "val").Obj(),
				testinghelper.MakePdb().Namespace("default").Name("pdb2").DisruptionsAllowed(1).
					Label("key", "val2").Obj(),
			},
			expectedNodeIndexes: [][]int{
				nil,
				nil,
			},
			expectedMaxVictimPriority: -1,
		},
	}

	testFunc := func(gs podScheduler, index int) {
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				cache := godelcache.New(handler.MakeCacheHandlerWrapper().
					SchedulerName("").SchedulerType("").SubCluster(framework.DefaultSubCluster).
					TTL(time.Second).Period(10 * time.Second).StopCh(make(<-chan struct{})).
					EnableStore("PreemptionStore").
					Obj())
				snapshot := godelcache.NewEmptySnapshot(handler.MakeCacheHandlerWrapper().
					SubCluster(framework.DefaultSubCluster).SwitchType(framework.DefaultSubClusterSwitchType).
					EnableStore("PreemptionStore").
					Obj())

				for _, pod := range tt.existingPods {
					cache.AddPod(pod)
				}
				for _, node := range tt.nodeList {
					cache.AddNode(node)
				}

				for _, pdb := range tt.pdbs {
					cache.AddPDB(pdb)
				}

				cache.UpdateSnapshot(snapshot)
				gs.snapshot = snapshot

				nodeResourcesPlugin, err := noderesources.NewFit(nil, nil)
				if err != nil {
					t.Errorf("Could not new node resources plugin: %v", err)
				}

				fh, _ := st.NewSchedulerFrameworkHandle(gs.clientSet, gs.crdClient, gs.informerFactory, gs.crdInformerFactory, cache, snapshot, framework.PluginMap{noderesources.FitName: nodeResourcesPlugin}, nil, nil, nil)
				nonNativeTopologyPlugin, err := nonnativeresource.NewNonNativeTopology(nil, fh)
				if err != nil {
					t.Errorf("Could not new nonnative topology plugin: %v", err)
				}
				basePlugins, preemptionPluginRegistry, err := utils.GetPreemptionRelatedPlugins(fh)
				if err != nil {
					t.Errorf("failed to get defualt plugins and configs: %v", err)
				}

				filterPluginRegistry := &framework.OrderedPluginRegistry{Plugins: []string{noderesources.FitName}}
				fh, _ = st.NewSchedulerFrameworkHandle(gs.clientSet, gs.crdClient, gs.informerFactory, gs.crdInformerFactory, cache, snapshot, framework.PluginMap{noderesources.FitName: nodeResourcesPlugin, nonnativeresource.NonNativeTopologyName: nonNativeTopologyPlugin}, preemptionPluginRegistry, filterPluginRegistry, basePlugins)
				gs.betterSelectPoliciesRegistry = map[string]betterSelectPolicy{
					config.BetterPreemptionPolicyAscending: gs.ascendingOrderPreemption,
					config.BetterPreemptionPolicyDichotomy: gs.dichotomyPreemption,
				}
				fwk, _ := fh.GetFrameworkForPod(tt.pod)
				state, err := fwk.InitCycleState(tt.pod)
				if err != nil {
					t.Fatal(err)
				}

				pfwk := fh.GetPreemptionFrameworkForPod(tt.pod)
				gs.schedulerPreemptionFramework = pfwk
				commonState := framework.NewCycleState()

				if status := fwk.RunPreFilterPlugins(context.Background(), state, tt.pod); !status.IsSuccess() {
					t.Errorf("Unexpected PreFilter Status: %v", status)
				}

				var nodeInfoList []framework.NodeInfo
				nodeLister := snapshot.NodeInfos()
				for _, node := range tt.nodeList {
					nInfo, _ := nodeLister.Get(node.Name)
					nodeInfoList = append(nodeInfoList, nInfo)
				}

				resourceType, _ := podutil.GetPodResourceType(tt.pod)
				stop := false
				priorities := make([][]int64, len(nodeInfoList))
				getPriorities := func(i int) {
					nodeInfo := nodeInfoList[i]
					if nodeInfo == nil {
						return
					}
					priorities[i] = nodeInfo.GetPrioritiesForPodsMayBePreempted(resourceType)
				}
				util.ParallelizeUntil(&stop, 32, len(nodeInfoList), getPriorities)

				prioritySet := sets.NewInt()
				for _, prioritiesEachNode := range priorities {
					for _, priority := range prioritiesEachNode {
						prioritySet.Insert(int(priority))
					}
				}

				podPriority := podutil.GetPodPriority(tt.pod)
				var prioritiesSlice []int
				for _, priority := range prioritySet.List() {
					if priority > int(podPriority) {
						continue
					}
					prioritiesSlice = append(prioritiesSlice, priority)
				}

				pfwk.RunClusterPrePreemptingPlugins(tt.pod, state, commonState)
				nodeIndexes, maxVictimPriorityIndex := gs.betterSelectPoliciesRegistry[gs.betterSelectPolicies[0]](context.Background(), state, tt.pod, resourceType, prioritiesSlice, nodeInfoList, 3, fwk, pfwk)
				if !reflect.DeepEqual(tt.expectedNodeIndexes[index], nodeIndexes) {
					t.Errorf("expected %v but got %v", tt.expectedNodeIndexes[index], nodeIndexes)
				}
				if maxVictimPriorityIndex == -1 && tt.expectedMaxVictimPriority != -1 {
					t.Errorf("expected max victim priority %d but got nil", tt.expectedMaxVictimPriority)
				} else if maxVictimPriorityIndex != -1 && tt.expectedMaxVictimPriority == -1 {
					t.Errorf("expected max victim priority nil but got %d", prioritiesSlice[maxVictimPriorityIndex])
				} else if maxVictimPriorityIndex != -1 && tt.expectedMaxVictimPriority != -1 {
					if tt.expectedMaxVictimPriority != prioritiesSlice[maxVictimPriorityIndex] {
						t.Errorf("expected max victim priority %d but got %d", tt.expectedMaxVictimPriority, prioritiesSlice[maxVictimPriorityIndex])
					}
				}
			})
		}
	}

	// ascendingPreemption
	{
		crdClient := godelclientfake.NewSimpleClientset()
		crdInformerFactory := crdinformers.NewSharedInformerFactory(crdClient, 0)
		client := clientsetfake.NewSimpleClientset()
		informerFactory := informers.NewSharedInformerFactory(client, 0)
		pl1 := podScheduler{
			clientSet:             client,
			informerFactory:       informerFactory,
			crdClient:             crdClient,
			crdInformerFactory:    crdInformerFactory,
			candidateSelectPolicy: config.CandidateSelectPolicyBetter,
			betterSelectPolicies:  []string{config.BetterPreemptionPolicyAscending},
		}
		testFunc(pl1, 0)
	}

	// dichotomyPreemption
	{
		crdClient := godelclientfake.NewSimpleClientset()
		crdInformerFactory := crdinformers.NewSharedInformerFactory(crdClient, 0)
		client := clientsetfake.NewSimpleClientset()
		informerFactory := informers.NewSharedInformerFactory(client, 0)
		pl2 := podScheduler{
			clientSet:             client,
			informerFactory:       informerFactory,
			crdClient:             crdClient,
			crdInformerFactory:    crdInformerFactory,
			candidateSelectPolicy: config.CandidateSelectPolicyBetter,
			betterSelectPolicies:  []string{config.BetterPreemptionPolicyDichotomy},
		}
		testFunc(pl2, 1)
	}
}

func TestBetterPreemption_PDB2(t *testing.T) {
	tests := []struct {
		name                      string
		pod                       *v1.Pod
		existingPods              []*v1.Pod
		nodeList                  []*v1.Node
		pdbs                      []*policy.PodDisruptionBudget
		expectedNodeIndexes       [][]int
		expectedMaxVictimPriority int
	}{
		{
			name: "first and second",
			pod: testinghelper.MakePod().Namespace("default").Name("p").UID("p").Req(map[v1.ResourceName]string{v1.ResourceCPU: "2"}).Priority(20).
				Annotation(constraints.HardConstraintsAnnotationKey, noderesources.FitName).Obj(),
			existingPods: []*v1.Pod{
				testinghelper.MakePod().Namespace("default").Name("p1").UID("p1").Node("n2").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(10).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).
					Label("key", "val").Obj(),
				testinghelper.MakePod().Namespace("default").Name("p2").UID("p2").Node("n2").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(11).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).
					Label("key", "val").Obj(),
				testinghelper.MakePod().Namespace("default").Name("p3").UID("p3").Node("n2").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(12).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).
					Label("key", "val").Obj(),
				testinghelper.MakePod().Namespace("default").Name("p4").UID("p4").Node("n2").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(13).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).
					Label("key", "val").Obj(),
				testinghelper.MakePod().Namespace("default").Name("p5").UID("p5").Node("n2").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(14).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).
					Label("key", "val").Obj(),
				testinghelper.MakePod().Namespace("default").Name("p6").UID("p6").Node("n2").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(15).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).
					Label("key", "val").Obj(),
				testinghelper.MakePod().Namespace("default").Name("p7").UID("p7").Node("n2").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(16).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).
					Label("key", "val").Obj(),
				testinghelper.MakePod().Namespace("default").Name("p8").UID("p8").Node("n2").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(17).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).
					Label("key", "val").Obj(),
				testinghelper.MakePod().Namespace("default").Name("p9").UID("p9").Node("n1").Req(map[v1.ResourceName]string{v1.ResourceCPU: "2"}).Priority(12).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).Obj(),
				testinghelper.MakePod().Namespace("default").Name("p10").UID("p10").Node("n1").Req(map[v1.ResourceName]string{v1.ResourceCPU: "6"}).Priority(12).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).Obj(),
			},
			nodeList: []*v1.Node{
				testinghelper.MakeNode().Name("n1").Capacity(map[v1.ResourceName]string{v1.ResourceCPU: "8"}).Obj(),
				testinghelper.MakeNode().Name("n2").Capacity(map[v1.ResourceName]string{v1.ResourceCPU: "8"}).Obj(),
			},
			pdbs: []*policy.PodDisruptionBudget{
				testinghelper.MakePdb().Namespace("default").Name("pdb").DisruptionsAllowed(2).
					Label("key", "val").Obj(),
			},
			expectedNodeIndexes: [][]int{
				{1, 0},
				{1, 0},
			},
			expectedMaxVictimPriority: 11,
		},
		{
			name: "first and third",
			pod: testinghelper.MakePod().Namespace("default").Name("p").UID("p").Req(map[v1.ResourceName]string{v1.ResourceCPU: "2"}).Priority(20).
				Annotation(constraints.HardConstraintsAnnotationKey, noderesources.FitName).Obj(),
			existingPods: []*v1.Pod{
				testinghelper.MakePod().Namespace("default").Name("p1").UID("p1").Node("n2").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(10).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).
					Label("key", "val").Obj(),
				testinghelper.MakePod().Namespace("default").Name("p2").UID("p2").Node("n2").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(11).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CannotBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).
					Label("key", "val").Obj(),
				testinghelper.MakePod().Namespace("default").Name("p3").UID("p3").Node("n2").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(12).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).
					Label("key", "val").Obj(),
				testinghelper.MakePod().Namespace("default").Name("p4").UID("p4").Node("n2").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(13).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).
					Label("key", "val").Obj(),
				testinghelper.MakePod().Namespace("default").Name("p5").UID("p5").Node("n2").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(14).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).
					Label("key", "val").Obj(),
				testinghelper.MakePod().Namespace("default").Name("p6").UID("p6").Node("n2").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(15).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).
					Label("key", "val").Obj(),
				testinghelper.MakePod().Namespace("default").Name("p7").UID("p7").Node("n2").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(16).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).
					Label("key", "val").Obj(),
				testinghelper.MakePod().Namespace("default").Name("p8").UID("p8").Node("n2").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(17).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).
					Label("key", "val").Obj(),
				testinghelper.MakePod().Namespace("default").Name("p9").UID("p9").Node("n1").Req(map[v1.ResourceName]string{v1.ResourceCPU: "2"}).Priority(13).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).Obj(),
				testinghelper.MakePod().Namespace("default").Name("p10").UID("p10").Node("n1").Req(map[v1.ResourceName]string{v1.ResourceCPU: "6"}).Priority(13).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).Obj(),
			},
			nodeList: []*v1.Node{
				testinghelper.MakeNode().Name("n1").Capacity(map[v1.ResourceName]string{v1.ResourceCPU: "8"}).Obj(),
				testinghelper.MakeNode().Name("n2").Capacity(map[v1.ResourceName]string{v1.ResourceCPU: "8"}).Obj(),
			},
			pdbs: []*policy.PodDisruptionBudget{
				testinghelper.MakePdb().Namespace("default").Name("pdb").DisruptionsAllowed(3).
					Label("key", "val").Obj(),
			},
			expectedNodeIndexes: [][]int{
				{1, 0},
				{1, 0},
			},
			expectedMaxVictimPriority: 12,
		},
		{
			name: "first and forth",
			pod: testinghelper.MakePod().Namespace("default").Name("p").UID("p").Req(map[v1.ResourceName]string{v1.ResourceCPU: "2"}).Priority(20).
				Annotation(constraints.HardConstraintsAnnotationKey, noderesources.FitName).Obj(),
			existingPods: []*v1.Pod{
				testinghelper.MakePod().Namespace("default").Name("p1").UID("p1").Node("n1").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(10).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).
					Label("key", "val").Obj(),
				testinghelper.MakePod().Namespace("default").Name("p2").UID("p2").Node("n1").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(11).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CannotBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).
					Label("key", "val").Obj(),
				testinghelper.MakePod().Namespace("default").Name("p3").UID("p3").Node("n1").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(12).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CannotBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).
					Label("key", "val").Obj(),
				testinghelper.MakePod().Namespace("default").Name("p4").UID("p4").Node("n1").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(13).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).
					Label("key", "val").Obj(),
				testinghelper.MakePod().Namespace("default").Name("p5").UID("p5").Node("n1").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(14).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).
					Label("key", "val").Obj(),
				testinghelper.MakePod().Namespace("default").Name("p6").UID("p6").Node("n1").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(15).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).
					Label("key", "val").Obj(),
				testinghelper.MakePod().Namespace("default").Name("p7").UID("p7").Node("n1").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(16).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).
					Label("key", "val").Obj(),
				testinghelper.MakePod().Namespace("default").Name("p8").UID("p8").Node("n1").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(17).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).
					Label("key", "val").Obj(),
				testinghelper.MakePod().Namespace("default").Name("p9").UID("p9").Node("n2").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(13).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).Obj(),
				testinghelper.MakePod().Namespace("default").Name("p10").UID("p10").Node("n2").Req(map[v1.ResourceName]string{v1.ResourceCPU: "7"}).Priority(14).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).Obj(),
			},
			nodeList: []*v1.Node{
				testinghelper.MakeNode().Name("n1").Capacity(map[v1.ResourceName]string{v1.ResourceCPU: "8"}).Obj(),
				testinghelper.MakeNode().Name("n2").Capacity(map[v1.ResourceName]string{v1.ResourceCPU: "8"}).Obj(),
			},
			pdbs: []*policy.PodDisruptionBudget{
				testinghelper.MakePdb().Namespace("default").Name("pdb").DisruptionsAllowed(2).
					Label("key", "val").Obj(),
			},
			expectedNodeIndexes: [][]int{
				{0, 1},
				{0},
			},
			expectedMaxVictimPriority: 13,
		},
		{
			name: "first and fifth",
			pod: testinghelper.MakePod().Namespace("default").Name("p").UID("p").Req(map[v1.ResourceName]string{v1.ResourceCPU: "2"}).Priority(20).
				Annotation(constraints.HardConstraintsAnnotationKey, noderesources.FitName).Obj(),
			existingPods: []*v1.Pod{
				testinghelper.MakePod().Namespace("default").Name("p1").UID("p1").Node("n2").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(10).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).
					Label("key", "val").Obj(),
				testinghelper.MakePod().Namespace("default").Name("p2").UID("p2").Node("n2").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(11).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CannotBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).
					Label("key", "val").Obj(),
				testinghelper.MakePod().Namespace("default").Name("p3").UID("p3").Node("n2").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(12).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CannotBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).
					Label("key", "val").Obj(),
				testinghelper.MakePod().Namespace("default").Name("p4").UID("p4").Node("n2").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(13).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CannotBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).
					Label("key", "val").Obj(),
				testinghelper.MakePod().Namespace("default").Name("p5").UID("p5").Node("n2").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(14).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).
					Label("key", "val").Obj(),
				testinghelper.MakePod().Namespace("default").Name("p6").UID("p6").Node("n2").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(15).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).
					Label("key", "val").Obj(),
				testinghelper.MakePod().Namespace("default").Name("p7").UID("p7").Node("n2").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(16).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).
					Label("key", "val").Obj(),
				testinghelper.MakePod().Namespace("default").Name("p8").UID("p8").Node("n2").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(17).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).
					Label("key", "val").Obj(),
				testinghelper.MakePod().Namespace("default").Name("p9").UID("p9").Node("n1").Req(map[v1.ResourceName]string{v1.ResourceCPU: "2"}).Priority(15).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).
					Label("key", "val").Obj(),
				testinghelper.MakePod().Namespace("default").Name("p10").UID("p10").Node("n1").Req(map[v1.ResourceName]string{v1.ResourceCPU: "6"}).Priority(15).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).Obj(),
			},
			nodeList: []*v1.Node{
				testinghelper.MakeNode().Name("n1").Capacity(map[v1.ResourceName]string{v1.ResourceCPU: "8"}).Obj(),
				testinghelper.MakeNode().Name("n2").Capacity(map[v1.ResourceName]string{v1.ResourceCPU: "8"}).Obj(),
			},
			pdbs: []*policy.PodDisruptionBudget{
				testinghelper.MakePdb().Namespace("default").Name("pdb").DisruptionsAllowed(2).
					Label("key", "val").Obj(),
			},
			expectedNodeIndexes: [][]int{
				{1, 0},
				{1, 0},
			},
			expectedMaxVictimPriority: 14,
		},
		{
			name: "first and sixth",
			pod: testinghelper.MakePod().Namespace("default").Name("p").UID("p").Req(map[v1.ResourceName]string{v1.ResourceCPU: "2"}).Priority(20).
				Annotation(constraints.HardConstraintsAnnotationKey, noderesources.FitName).Obj(),
			existingPods: []*v1.Pod{
				testinghelper.MakePod().Namespace("default").Name("p1").UID("p1").Node("n2").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(10).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).
					Label("key", "val").Obj(),
				testinghelper.MakePod().Namespace("default").Name("p2").UID("p2").Node("n2").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(11).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CannotBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).
					Label("key", "val").Obj(),
				testinghelper.MakePod().Namespace("default").Name("p3").UID("p3").Node("n2").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(12).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CannotBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).
					Label("key", "val").Obj(),
				testinghelper.MakePod().Namespace("default").Name("p4").UID("p4").Node("n2").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(13).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CannotBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).
					Label("key", "val").Obj(),
				testinghelper.MakePod().Namespace("default").Name("p5").UID("p5").Node("n2").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(14).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CannotBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).
					Label("key", "val").Obj(),
				testinghelper.MakePod().Namespace("default").Name("p6").UID("p6").Node("n2").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(15).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).
					Label("key", "val").Obj(),
				testinghelper.MakePod().Namespace("default").Name("p7").UID("p7").Node("n2").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(16).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).
					Label("key", "val").Obj(),
				testinghelper.MakePod().Namespace("default").Name("p8").UID("p8").Node("n2").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(17).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).
					Label("key", "val").Obj(),
				testinghelper.MakePod().Namespace("default").Name("p9").UID("p9").Node("n1").Req(map[v1.ResourceName]string{v1.ResourceCPU: "2"}).Priority(16).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).
					Label("key", "val").Obj(),
				testinghelper.MakePod().Namespace("default").Name("p10").UID("p10").Node("n1").Req(map[v1.ResourceName]string{v1.ResourceCPU: "6"}).Priority(16).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).Obj(),
			},
			nodeList: []*v1.Node{
				testinghelper.MakeNode().Name("n1").Capacity(map[v1.ResourceName]string{v1.ResourceCPU: "8"}).Obj(),
				testinghelper.MakeNode().Name("n2").Capacity(map[v1.ResourceName]string{v1.ResourceCPU: "8"}).Obj(),
			},
			pdbs: []*policy.PodDisruptionBudget{
				testinghelper.MakePdb().Namespace("default").Name("pdb").DisruptionsAllowed(2).
					Label("key", "val").Obj(),
			},
			expectedNodeIndexes: [][]int{
				{1, 0},
				{1},
			},
			expectedMaxVictimPriority: 15,
		},
		{
			name: "first and seventh",
			pod: testinghelper.MakePod().Namespace("default").Name("p").UID("p").Req(map[v1.ResourceName]string{v1.ResourceCPU: "2"}).Priority(20).
				Annotation(constraints.HardConstraintsAnnotationKey, noderesources.FitName).Obj(),
			existingPods: []*v1.Pod{
				testinghelper.MakePod().Namespace("default").Name("p1").UID("p1").Node("n1").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(10).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).
					Label("key", "val").Obj(),
				testinghelper.MakePod().Namespace("default").Name("p2").UID("p2").Node("n1").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(11).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CannotBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).
					Label("key", "val").Obj(),
				testinghelper.MakePod().Namespace("default").Name("p3").UID("p3").Node("n1").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(12).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CannotBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).
					Label("key", "val").Obj(),
				testinghelper.MakePod().Namespace("default").Name("p4").UID("p4").Node("n1").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(13).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CannotBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).
					Label("key", "val").Obj(),
				testinghelper.MakePod().Namespace("default").Name("p5").UID("p5").Node("n1").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(14).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CannotBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).
					Label("key", "val").Obj(),
				testinghelper.MakePod().Namespace("default").Name("p6").UID("p6").Node("n1").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(15).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CannotBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).
					Label("key", "val").Obj(),
				testinghelper.MakePod().Namespace("default").Name("p7").UID("p7").Node("n1").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(16).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).
					Label("key", "val").Obj(),
				testinghelper.MakePod().Namespace("default").Name("p8").UID("p8").Node("n1").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(17).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).
					Label("key", "val").Obj(),
				testinghelper.MakePod().Namespace("default").Name("p9").UID("p9").Node("n2").Req(map[v1.ResourceName]string{v1.ResourceCPU: "4"}).Priority(17).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).Obj(),
				testinghelper.MakePod().Namespace("default").Name("p10").UID("p10").Node("n2").Req(map[v1.ResourceName]string{v1.ResourceCPU: "4"}).Priority(17).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).Obj(),
			},
			nodeList: []*v1.Node{
				testinghelper.MakeNode().Name("n1").Capacity(map[v1.ResourceName]string{v1.ResourceCPU: "8"}).Obj(),
				testinghelper.MakeNode().Name("n2").Capacity(map[v1.ResourceName]string{v1.ResourceCPU: "8"}).Obj(),
			},
			pdbs: []*policy.PodDisruptionBudget{
				testinghelper.MakePdb().Namespace("default").Name("pdb").DisruptionsAllowed(2).
					Label("key", "val").Obj(),
			},
			expectedNodeIndexes: [][]int{
				{0, 1},
				{0},
			},
			expectedMaxVictimPriority: 16,
		},
		{
			name: "first and eighth",
			pod: testinghelper.MakePod().Namespace("default").Name("p").UID("p").Req(map[v1.ResourceName]string{v1.ResourceCPU: "2"}).Priority(20).
				Annotation(constraints.HardConstraintsAnnotationKey, noderesources.FitName).Obj(),
			existingPods: []*v1.Pod{
				testinghelper.MakePod().Namespace("default").Name("p1").UID("p1").Node("n1").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(10).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).
					Label("key", "val").Obj(),
				testinghelper.MakePod().Namespace("default").Name("p2").UID("p2").Node("n1").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(11).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CannotBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).
					Label("key", "val").Obj(),
				testinghelper.MakePod().Namespace("default").Name("p3").UID("p3").Node("n1").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(12).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CannotBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).
					Label("key", "val").Obj(),
				testinghelper.MakePod().Namespace("default").Name("p4").UID("p4").Node("n1").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(13).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CannotBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).
					Label("key", "val").Obj(),
				testinghelper.MakePod().Namespace("default").Name("p5").UID("p5").Node("n1").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(14).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CannotBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).
					Label("key", "val").Obj(),
				testinghelper.MakePod().Namespace("default").Name("p6").UID("p6").Node("n1").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(15).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CannotBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).
					Label("key", "val").Obj(),
				testinghelper.MakePod().Namespace("default").Name("p7").UID("p7").Node("n1").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(16).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CannotBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).
					Label("key", "val").Obj(),
				testinghelper.MakePod().Namespace("default").Name("p8").UID("p8").Node("n1").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(17).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).
					Label("key", "val").Obj(),
				testinghelper.MakePod().Namespace("default").Name("p9").UID("p9").Node("n2").Req(map[v1.ResourceName]string{v1.ResourceCPU: "4"}).Priority(18).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).Obj(),
				testinghelper.MakePod().Namespace("default").Name("p10").UID("p10").Node("n2").Req(map[v1.ResourceName]string{v1.ResourceCPU: "4"}).Priority(18).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).Obj(),
			},
			nodeList: []*v1.Node{
				testinghelper.MakeNode().Name("n1").Capacity(map[v1.ResourceName]string{v1.ResourceCPU: "8"}).Obj(),
				testinghelper.MakeNode().Name("n2").Capacity(map[v1.ResourceName]string{v1.ResourceCPU: "8"}).Obj(),
			},
			pdbs: []*policy.PodDisruptionBudget{
				testinghelper.MakePdb().Namespace("default").Name("pdb").DisruptionsAllowed(2).
					Label("key", "val").Obj(),
			},
			expectedNodeIndexes: [][]int{
				{0, 1},
				{0},
			},
			expectedMaxVictimPriority: 17,
		},
		{
			name: "second and third",
			pod: testinghelper.MakePod().Namespace("default").Name("p").UID("p").Req(map[v1.ResourceName]string{v1.ResourceCPU: "2"}).Priority(20).
				Annotation(constraints.HardConstraintsAnnotationKey, noderesources.FitName).Obj(),
			existingPods: []*v1.Pod{
				testinghelper.MakePod().Namespace("default").Name("p1").UID("p1").Node("n2").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(10).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).
					Label("key", "val").Obj(),
				testinghelper.MakePod().Namespace("default").Name("p2").UID("p2").Node("n2").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(11).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).
					Label("key", "val2").Obj(),
				testinghelper.MakePod().Namespace("default").Name("p3").UID("p3").Node("n2").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(12).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).
					Label("key", "val2").Obj(),
				testinghelper.MakePod().Namespace("default").Name("p4").UID("p4").Node("n2").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(13).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).
					Label("key", "val2").Obj(),
				testinghelper.MakePod().Namespace("default").Name("p5").UID("p5").Node("n2").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(14).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).
					Label("key", "val2").Obj(),
				testinghelper.MakePod().Namespace("default").Name("p6").UID("p6").Node("n2").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(15).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).
					Label("key", "val2").Obj(),
				testinghelper.MakePod().Namespace("default").Name("p7").UID("p7").Node("n2").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(16).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).
					Label("key", "val2").Obj(),
				testinghelper.MakePod().Namespace("default").Name("p8").UID("p8").Node("n2").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(17).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).
					Label("key", "val2").Obj(),
				testinghelper.MakePod().Namespace("default").Name("p9").UID("p9").Node("n1").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(13).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).Obj(),
				testinghelper.MakePod().Namespace("default").Name("p10").UID("p10").Node("n1").Req(map[v1.ResourceName]string{v1.ResourceCPU: "6"}).Priority(13).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).Obj(),
			},
			nodeList: []*v1.Node{
				testinghelper.MakeNode().Name("n1").Capacity(map[v1.ResourceName]string{v1.ResourceCPU: "8"}).Obj(),
				testinghelper.MakeNode().Name("n2").Capacity(map[v1.ResourceName]string{v1.ResourceCPU: "8"}).Obj(),
			},
			pdbs: []*policy.PodDisruptionBudget{
				testinghelper.MakePdb().Namespace("default").Name("pdb").DisruptionsAllowed(0).
					Label("key", "val").Obj(),
				testinghelper.MakePdb().Namespace("default").Name("pdb2").DisruptionsAllowed(2).
					Label("key", "val2").Obj(),
			},
			expectedNodeIndexes: [][]int{
				{1, 0},
				{1, 0},
			},
			expectedMaxVictimPriority: 12,
		},
		{
			name: "second and forth",
			pod: testinghelper.MakePod().Namespace("default").Name("p").UID("p").Req(map[v1.ResourceName]string{v1.ResourceCPU: "2"}).Priority(20).
				Annotation(constraints.HardConstraintsAnnotationKey, noderesources.FitName).Obj(),
			existingPods: []*v1.Pod{
				testinghelper.MakePod().Namespace("default").Name("p1").UID("p1").Node("n2").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(10).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).
					Label("key", "val").Obj(),
				testinghelper.MakePod().Namespace("default").Name("p2").UID("p2").Node("n2").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(11).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).
					Label("key", "val2").Obj(),
				testinghelper.MakePod().Namespace("default").Name("p3").UID("p3").Node("n2").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(12).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CannotBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).
					Label("key", "val2").Obj(),
				testinghelper.MakePod().Namespace("default").Name("p4").UID("p4").Node("n2").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(13).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).
					Label("key", "val2").Obj(),
				testinghelper.MakePod().Namespace("default").Name("p5").UID("p5").Node("n2").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(14).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).
					Label("key", "val2").Obj(),
				testinghelper.MakePod().Namespace("default").Name("p6").UID("p6").Node("n2").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(15).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).
					Label("key", "val2").Obj(),
				testinghelper.MakePod().Namespace("default").Name("p7").UID("p7").Node("n2").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(16).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).Obj(),
				testinghelper.MakePod().Namespace("default").Name("p8").UID("p8").Node("n2").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(17).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).
					Label("key", "val2").Obj(),
				testinghelper.MakePod().Namespace("default").Name("p9").UID("p9").Node("n1").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(14).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).Obj(),
				testinghelper.MakePod().Namespace("default").Name("p10").UID("p10").Node("n1").Req(map[v1.ResourceName]string{v1.ResourceCPU: "6"}).Priority(14).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).Obj(),
			},
			nodeList: []*v1.Node{
				testinghelper.MakeNode().Name("n1").Capacity(map[v1.ResourceName]string{v1.ResourceCPU: "8"}).Obj(),
				testinghelper.MakeNode().Name("n2").Capacity(map[v1.ResourceName]string{v1.ResourceCPU: "8"}).Obj(),
			},
			pdbs: []*policy.PodDisruptionBudget{
				testinghelper.MakePdb().Namespace("default").Name("pdb").DisruptionsAllowed(0).
					Label("key", "val").Obj(),
				testinghelper.MakePdb().Namespace("default").Name("pdb2").DisruptionsAllowed(2).
					Label("key", "val2").Obj(),
			},
			expectedNodeIndexes: [][]int{
				{1, 0},
				{1},
			},
			expectedMaxVictimPriority: 13,
		},
		{
			name: "second and fifth",
			pod: testinghelper.MakePod().Namespace("default").Name("p").UID("p").Req(map[v1.ResourceName]string{v1.ResourceCPU: "2"}).Priority(20).
				Annotation(constraints.HardConstraintsAnnotationKey, noderesources.FitName).Obj(),
			existingPods: []*v1.Pod{
				testinghelper.MakePod().Namespace("default").Name("p1").UID("p1").Node("n2").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(10).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).
					Label("key", "val").Obj(),
				testinghelper.MakePod().Namespace("default").Name("p2").UID("p2").Node("n2").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(11).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).
					Label("key", "val2").Obj(),
				testinghelper.MakePod().Namespace("default").Name("p3").UID("p3").Node("n2").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(12).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CannotBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).
					Label("key", "val2").Obj(),
				testinghelper.MakePod().Namespace("default").Name("p4").UID("p4").Node("n2").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(13).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CannotBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).
					Label("key", "val2").Obj(),
				testinghelper.MakePod().Namespace("default").Name("p5").UID("p5").Node("n2").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(14).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).
					Label("key", "val2").Obj(),
				testinghelper.MakePod().Namespace("default").Name("p6").UID("p6").Node("n2").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(15).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).
					Label("key", "val2").Obj(),
				testinghelper.MakePod().Namespace("default").Name("p7").UID("p7").Node("n2").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(16).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).
					Label("key", "val2").Obj(),
				testinghelper.MakePod().Namespace("default").Name("p8").UID("p8").Node("n2").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(17).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).
					Label("key", "val2").Obj(),
				testinghelper.MakePod().Namespace("default").Name("p9").UID("p9").Node("n1").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(15).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).Obj(),
				testinghelper.MakePod().Namespace("default").Name("p10").UID("p10").Node("n1").Req(map[v1.ResourceName]string{v1.ResourceCPU: "6"}).Priority(15).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).Obj(),
			},
			nodeList: []*v1.Node{
				testinghelper.MakeNode().Name("n1").Capacity(map[v1.ResourceName]string{v1.ResourceCPU: "8"}).Obj(),
				testinghelper.MakeNode().Name("n2").Capacity(map[v1.ResourceName]string{v1.ResourceCPU: "8"}).Obj(),
			},
			pdbs: []*policy.PodDisruptionBudget{
				testinghelper.MakePdb().Namespace("default").Name("pdb").DisruptionsAllowed(0).
					Label("key", "val").Obj(),
				testinghelper.MakePdb().Namespace("default").Name("pdb2").DisruptionsAllowed(2).
					Label("key", "val2").Obj(),
			},
			expectedNodeIndexes: [][]int{
				{1, 0},
				{1, 0},
			},
			expectedMaxVictimPriority: 14,
		},
		{
			name: "second and sixth",
			pod: testinghelper.MakePod().Namespace("default").Name("p").UID("p").Req(map[v1.ResourceName]string{v1.ResourceCPU: "2"}).Priority(20).
				Annotation(constraints.HardConstraintsAnnotationKey, noderesources.FitName).Obj(),
			existingPods: []*v1.Pod{
				testinghelper.MakePod().Namespace("default").Name("p1").UID("p1").Node("n2").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(10).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).
					Label("key", "val").Obj(),
				testinghelper.MakePod().Namespace("default").Name("p2").UID("p2").Node("n2").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(11).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).
					Label("key", "val2").Obj(),
				testinghelper.MakePod().Namespace("default").Name("p3").UID("p3").Node("n2").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(12).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CannotBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).
					Label("key", "val2").Obj(),
				testinghelper.MakePod().Namespace("default").Name("p4").UID("p4").Node("n2").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(13).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CannotBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).
					Label("key", "val2").Obj(),
				testinghelper.MakePod().Namespace("default").Name("p5").UID("p5").Node("n2").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(14).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CannotBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).
					Label("key", "val2").Obj(),
				testinghelper.MakePod().Namespace("default").Name("p6").UID("p6").Node("n2").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(15).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).
					Label("key", "val2").Obj(),
				testinghelper.MakePod().Namespace("default").Name("p7").UID("p7").Node("n2").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(16).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).
					Label("key", "val2").Obj(),
				testinghelper.MakePod().Namespace("default").Name("p8").UID("p8").Node("n2").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(17).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).
					Label("key", "val2").Obj(),
				testinghelper.MakePod().Namespace("default").Name("p9").UID("p9").Node("n1").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(16).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).Obj(),
				testinghelper.MakePod().Namespace("default").Name("p10").UID("p10").Node("n1").Req(map[v1.ResourceName]string{v1.ResourceCPU: "6"}).Priority(16).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).Obj(),
			},
			nodeList: []*v1.Node{
				testinghelper.MakeNode().Name("n1").Capacity(map[v1.ResourceName]string{v1.ResourceCPU: "8"}).Obj(),
				testinghelper.MakeNode().Name("n2").Capacity(map[v1.ResourceName]string{v1.ResourceCPU: "8"}).Obj(),
			},
			pdbs: []*policy.PodDisruptionBudget{
				testinghelper.MakePdb().Namespace("default").Name("pdb").DisruptionsAllowed(0).
					Label("key", "val").Obj(),
				testinghelper.MakePdb().Namespace("default").Name("pdb2").DisruptionsAllowed(2).
					Label("key", "val2").Obj(),
			},
			expectedNodeIndexes: [][]int{
				{1, 0},
				{1},
			},
			expectedMaxVictimPriority: 15,
		},
		{
			name: "second and seventh",
			pod: testinghelper.MakePod().Namespace("default").Name("p").UID("p").Req(map[v1.ResourceName]string{v1.ResourceCPU: "2"}).Priority(20).
				Annotation(constraints.HardConstraintsAnnotationKey, noderesources.FitName).Obj(),
			existingPods: []*v1.Pod{
				testinghelper.MakePod().Namespace("default").Name("p1").UID("p1").Node("n2").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(10).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).
					Label("key", "val").Obj(),
				testinghelper.MakePod().Namespace("default").Name("p2").UID("p2").Node("n2").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(11).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).
					Label("key", "val2").Obj(),
				testinghelper.MakePod().Namespace("default").Name("p3").UID("p3").Node("n2").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(12).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CannotBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).
					Label("key", "val2").Obj(),
				testinghelper.MakePod().Namespace("default").Name("p4").UID("p4").Node("n2").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(13).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CannotBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).
					Label("key", "val2").Obj(),
				testinghelper.MakePod().Namespace("default").Name("p5").UID("p5").Node("n2").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(14).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CannotBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).
					Label("key", "val2").Obj(),
				testinghelper.MakePod().Namespace("default").Name("p6").UID("p6").Node("n2").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(15).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CannotBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).
					Label("key", "val2").Obj(),
				testinghelper.MakePod().Namespace("default").Name("p7").UID("p7").Node("n2").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(16).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).
					Label("key", "val2").Obj(),
				testinghelper.MakePod().Namespace("default").Name("p8").UID("p8").Node("n2").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(17).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).
					Label("key", "val2").Obj(),
				testinghelper.MakePod().Namespace("default").Name("p9").UID("p9").Node("n1").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(17).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).Obj(),
				testinghelper.MakePod().Namespace("default").Name("p10").UID("p10").Node("n1").Req(map[v1.ResourceName]string{v1.ResourceCPU: "6"}).Priority(17).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).Obj(),
			},
			nodeList: []*v1.Node{
				testinghelper.MakeNode().Name("n1").Capacity(map[v1.ResourceName]string{v1.ResourceCPU: "8"}).Obj(),
				testinghelper.MakeNode().Name("n2").Capacity(map[v1.ResourceName]string{v1.ResourceCPU: "8"}).Obj(),
			},
			pdbs: []*policy.PodDisruptionBudget{
				testinghelper.MakePdb().Namespace("default").Name("pdb").DisruptionsAllowed(0).
					Label("key", "val").Obj(),
				testinghelper.MakePdb().Namespace("default").Name("pdb2").DisruptionsAllowed(2).
					Label("key", "val2").Obj(),
			},
			expectedNodeIndexes: [][]int{
				{1, 0},
				{1},
			},
			expectedMaxVictimPriority: 16,
		},
		{
			name: "second and eighth",
			pod: testinghelper.MakePod().Namespace("default").Name("p").UID("p").Req(map[v1.ResourceName]string{v1.ResourceCPU: "2"}).Priority(20).
				Annotation(constraints.HardConstraintsAnnotationKey, noderesources.FitName).Obj(),
			existingPods: []*v1.Pod{
				testinghelper.MakePod().Namespace("default").Name("p1").UID("p1").Node("n2").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(10).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).
					Label("key", "val").Obj(),
				testinghelper.MakePod().Namespace("default").Name("p2").UID("p2").Node("n2").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(11).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).
					Label("key", "val2").Obj(),
				testinghelper.MakePod().Namespace("default").Name("p3").UID("p3").Node("n2").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(12).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CannotBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).
					Label("key", "val2").Obj(),
				testinghelper.MakePod().Namespace("default").Name("p4").UID("p4").Node("n2").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(13).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CannotBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).
					Label("key", "val2").Obj(),
				testinghelper.MakePod().Namespace("default").Name("p5").UID("p5").Node("n2").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(14).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CannotBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).
					Label("key", "val2").Obj(),
				testinghelper.MakePod().Namespace("default").Name("p6").UID("p6").Node("n2").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(15).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CannotBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).
					Label("key", "val2").Obj(),
				testinghelper.MakePod().Namespace("default").Name("p7").UID("p7").Node("n2").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(16).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CannotBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).
					Label("key", "val2").Obj(),
				testinghelper.MakePod().Namespace("default").Name("p8").UID("p8").Node("n2").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(17).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).
					Label("key", "val2").Obj(),
				testinghelper.MakePod().Namespace("default").Name("p9").UID("p9").Node("n1").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(18).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).Obj(),
				testinghelper.MakePod().Namespace("default").Name("p10").UID("p10").Node("n1").Req(map[v1.ResourceName]string{v1.ResourceCPU: "6"}).Priority(18).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).Obj(),
			},
			nodeList: []*v1.Node{
				testinghelper.MakeNode().Name("n1").Capacity(map[v1.ResourceName]string{v1.ResourceCPU: "8"}).Obj(),
				testinghelper.MakeNode().Name("n2").Capacity(map[v1.ResourceName]string{v1.ResourceCPU: "8"}).Obj(),
			},
			pdbs: []*policy.PodDisruptionBudget{
				testinghelper.MakePdb().Namespace("default").Name("pdb").DisruptionsAllowed(0).
					Label("key", "val").Obj(),
				testinghelper.MakePdb().Namespace("default").Name("pdb2").DisruptionsAllowed(2).
					Label("key", "val2").Obj(),
			},
			expectedNodeIndexes: [][]int{
				{1, 0},
				{1},
			},
			expectedMaxVictimPriority: 17,
		},
		{
			name: "third and forth",
			pod: testinghelper.MakePod().Namespace("default").Name("p").UID("p").Req(map[v1.ResourceName]string{v1.ResourceCPU: "2"}).Priority(20).
				Annotation(constraints.HardConstraintsAnnotationKey, noderesources.FitName).Obj(),
			existingPods: []*v1.Pod{
				testinghelper.MakePod().Namespace("default").Name("p1").UID("p1").Node("n2").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(10).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).
					Label("key", "val").Obj(),
				testinghelper.MakePod().Namespace("default").Name("p2").UID("p2").Node("n2").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(11).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).
					Label("key", "val").Obj(),
				testinghelper.MakePod().Namespace("default").Name("p3").UID("p3").Node("n2").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(12).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).
					Label("key", "val2").Obj(),
				testinghelper.MakePod().Namespace("default").Name("p4").UID("p4").Node("n2").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(13).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).
					Label("key", "val2").Obj(),
				testinghelper.MakePod().Namespace("default").Name("p5").UID("p5").Node("n2").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(14).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).
					Label("key", "val2").Obj(),
				testinghelper.MakePod().Namespace("default").Name("p6").UID("p6").Node("n2").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(15).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).
					Label("key", "val2").Obj(),
				testinghelper.MakePod().Namespace("default").Name("p7").UID("p7").Node("n2").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(16).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).
					Label("key", "val2").Obj(),
				testinghelper.MakePod().Namespace("default").Name("p8").UID("p8").Node("n2").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(17).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).
					Label("key", "val2").Obj(),
				testinghelper.MakePod().Namespace("default").Name("p9").UID("p9").Node("n1").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(14).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).Obj(),
				testinghelper.MakePod().Namespace("default").Name("p10").UID("p10").Node("n1").Req(map[v1.ResourceName]string{v1.ResourceCPU: "6"}).Priority(14).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).Obj(),
			},
			nodeList: []*v1.Node{
				testinghelper.MakeNode().Name("n1").Capacity(map[v1.ResourceName]string{v1.ResourceCPU: "8"}).Obj(),
				testinghelper.MakeNode().Name("n2").Capacity(map[v1.ResourceName]string{v1.ResourceCPU: "8"}).Obj(),
			},
			pdbs: []*policy.PodDisruptionBudget{
				testinghelper.MakePdb().Namespace("default").Name("pdb").DisruptionsAllowed(0).
					Label("key", "val").Obj(),
				testinghelper.MakePdb().Namespace("default").Name("pdb2").DisruptionsAllowed(2).
					Label("key", "val2").Obj(),
			},
			expectedNodeIndexes: [][]int{
				{1, 0},
				{1},
			},
			expectedMaxVictimPriority: 13,
		},
		{
			name: "third and fifth",
			pod: testinghelper.MakePod().Namespace("default").Name("p").UID("p").Req(map[v1.ResourceName]string{v1.ResourceCPU: "2"}).Priority(20).
				Annotation(constraints.HardConstraintsAnnotationKey, noderesources.FitName).Obj(),
			existingPods: []*v1.Pod{
				testinghelper.MakePod().Namespace("default").Name("p1").UID("p1").Node("n2").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(10).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).
					Label("key", "val").Obj(),
				testinghelper.MakePod().Namespace("default").Name("p2").UID("p2").Node("n2").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(11).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).
					Label("key", "val").Obj(),
				testinghelper.MakePod().Namespace("default").Name("p3").UID("p3").Node("n2").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(12).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).
					Label("key", "val2").Obj(),
				testinghelper.MakePod().Namespace("default").Name("p4").UID("p4").Node("n2").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(13).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CannotBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).
					Label("key", "val2").Obj(),
				testinghelper.MakePod().Namespace("default").Name("p5").UID("p5").Node("n2").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(14).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).
					Label("key", "val2").Obj(),
				testinghelper.MakePod().Namespace("default").Name("p6").UID("p6").Node("n2").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(15).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).
					Label("key", "val2").Obj(),
				testinghelper.MakePod().Namespace("default").Name("p7").UID("p7").Node("n2").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(16).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).
					Label("key", "val2").Obj(),
				testinghelper.MakePod().Namespace("default").Name("p8").UID("p8").Node("n2").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(17).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).
					Label("key", "val2").Obj(),
				testinghelper.MakePod().Namespace("default").Name("p9").UID("p9").Node("n1").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(15).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).Obj(),
				testinghelper.MakePod().Namespace("default").Name("p10").UID("p10").Node("n1").Req(map[v1.ResourceName]string{v1.ResourceCPU: "6"}).Priority(15).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).Obj(),
			},
			nodeList: []*v1.Node{
				testinghelper.MakeNode().Name("n1").Capacity(map[v1.ResourceName]string{v1.ResourceCPU: "8"}).Obj(),
				testinghelper.MakeNode().Name("n2").Capacity(map[v1.ResourceName]string{v1.ResourceCPU: "8"}).Obj(),
			},
			pdbs: []*policy.PodDisruptionBudget{
				testinghelper.MakePdb().Namespace("default").Name("pdb").DisruptionsAllowed(0).
					Label("key", "val").Obj(),
				testinghelper.MakePdb().Namespace("default").Name("pdb2").DisruptionsAllowed(2).
					Label("key", "val2").Obj(),
			},
			expectedNodeIndexes: [][]int{
				{1, 0},
				{1, 0},
			},
			expectedMaxVictimPriority: 14,
		},
		{
			name: "third and sixth",
			pod: testinghelper.MakePod().Namespace("default").Name("p").UID("p").Req(map[v1.ResourceName]string{v1.ResourceCPU: "2"}).Priority(20).
				Annotation(constraints.HardConstraintsAnnotationKey, noderesources.FitName).Obj(),
			existingPods: []*v1.Pod{
				testinghelper.MakePod().Namespace("default").Name("p1").UID("p1").Node("n2").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(10).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).
					Label("key", "val").Obj(),
				testinghelper.MakePod().Namespace("default").Name("p2").UID("p2").Node("n2").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(11).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).
					Label("key", "val").Obj(),
				testinghelper.MakePod().Namespace("default").Name("p3").UID("p3").Node("n2").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(12).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).
					Label("key", "val2").Obj(),
				testinghelper.MakePod().Namespace("default").Name("p4").UID("p4").Node("n2").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(13).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CannotBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).
					Label("key", "val2").Obj(),
				testinghelper.MakePod().Namespace("default").Name("p5").UID("p5").Node("n2").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(14).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CannotBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).
					Label("key", "val2").Obj(),
				testinghelper.MakePod().Namespace("default").Name("p6").UID("p6").Node("n2").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(15).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).
					Label("key", "val2").Obj(),
				testinghelper.MakePod().Namespace("default").Name("p7").UID("p7").Node("n2").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(16).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).
					Label("key", "val2").Obj(),
				testinghelper.MakePod().Namespace("default").Name("p8").UID("p8").Node("n2").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(17).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).
					Label("key", "val2").Obj(),
				testinghelper.MakePod().Namespace("default").Name("p9").UID("p9").Node("n1").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(16).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).Obj(),
				testinghelper.MakePod().Namespace("default").Name("p10").UID("p10").Node("n1").Req(map[v1.ResourceName]string{v1.ResourceCPU: "6"}).Priority(16).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).Obj(),
			},
			nodeList: []*v1.Node{
				testinghelper.MakeNode().Name("n1").Capacity(map[v1.ResourceName]string{v1.ResourceCPU: "8"}).Obj(),
				testinghelper.MakeNode().Name("n2").Capacity(map[v1.ResourceName]string{v1.ResourceCPU: "8"}).Obj(),
			},
			pdbs: []*policy.PodDisruptionBudget{
				testinghelper.MakePdb().Namespace("default").Name("pdb").DisruptionsAllowed(0).
					Label("key", "val").Obj(),
				testinghelper.MakePdb().Namespace("default").Name("pdb2").DisruptionsAllowed(2).
					Label("key", "val2").Obj(),
			},
			expectedNodeIndexes: [][]int{
				{1, 0},
				{1},
			},
			expectedMaxVictimPriority: 15,
		},
		{
			name: "third and seventh",
			pod: testinghelper.MakePod().Namespace("default").Name("p").UID("p").Req(map[v1.ResourceName]string{v1.ResourceCPU: "2"}).Priority(20).
				Annotation(constraints.HardConstraintsAnnotationKey, noderesources.FitName).Obj(),
			existingPods: []*v1.Pod{
				testinghelper.MakePod().Namespace("default").Name("p1").UID("p1").Node("n2").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(10).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).
					Label("key", "val").Obj(),
				testinghelper.MakePod().Namespace("default").Name("p2").UID("p2").Node("n2").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(11).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).
					Label("key", "val").Obj(),
				testinghelper.MakePod().Namespace("default").Name("p3").UID("p3").Node("n2").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(12).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).
					Label("key", "val2").Obj(),
				testinghelper.MakePod().Namespace("default").Name("p4").UID("p4").Node("n2").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(13).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CannotBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).
					Label("key", "val2").Obj(),
				testinghelper.MakePod().Namespace("default").Name("p5").UID("p5").Node("n2").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(14).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CannotBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).
					Label("key", "val2").Obj(),
				testinghelper.MakePod().Namespace("default").Name("p6").UID("p6").Node("n2").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(15).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CannotBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).
					Label("key", "val2").Obj(),
				testinghelper.MakePod().Namespace("default").Name("p7").UID("p7").Node("n2").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(16).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).
					Label("key", "val2").Obj(),
				testinghelper.MakePod().Namespace("default").Name("p8").UID("p8").Node("n2").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(17).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).
					Label("key", "val2").Obj(),
				testinghelper.MakePod().Namespace("default").Name("p9").UID("p9").Node("n1").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(17).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).Obj(),
				testinghelper.MakePod().Namespace("default").Name("p10").UID("p10").Node("n1").Req(map[v1.ResourceName]string{v1.ResourceCPU: "6"}).Priority(17).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).Obj(),
			},
			nodeList: []*v1.Node{
				testinghelper.MakeNode().Name("n1").Capacity(map[v1.ResourceName]string{v1.ResourceCPU: "8"}).Obj(),
				testinghelper.MakeNode().Name("n2").Capacity(map[v1.ResourceName]string{v1.ResourceCPU: "8"}).Obj(),
			},
			pdbs: []*policy.PodDisruptionBudget{
				testinghelper.MakePdb().Namespace("default").Name("pdb").DisruptionsAllowed(0).
					Label("key", "val").Obj(),
				testinghelper.MakePdb().Namespace("default").Name("pdb2").DisruptionsAllowed(2).
					Label("key", "val2").Obj(),
			},
			expectedNodeIndexes: [][]int{
				{1, 0},
				{1},
			},
			expectedMaxVictimPriority: 16,
		},
		{
			name: "third and eighth",
			pod: testinghelper.MakePod().Namespace("default").Name("p").UID("p").Req(map[v1.ResourceName]string{v1.ResourceCPU: "2"}).Priority(20).
				Annotation(constraints.HardConstraintsAnnotationKey, noderesources.FitName).Obj(),
			existingPods: []*v1.Pod{
				testinghelper.MakePod().Namespace("default").Name("p1").UID("p1").Node("n2").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(10).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).
					Label("key", "val").Obj(),
				testinghelper.MakePod().Namespace("default").Name("p2").UID("p2").Node("n2").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(11).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).
					Label("key", "val").Obj(),
				testinghelper.MakePod().Namespace("default").Name("p3").UID("p3").Node("n2").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(12).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).
					Label("key", "val2").Obj(),
				testinghelper.MakePod().Namespace("default").Name("p4").UID("p4").Node("n2").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(13).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CannotBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).
					Label("key", "val2").Obj(),
				testinghelper.MakePod().Namespace("default").Name("p5").UID("p5").Node("n2").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(14).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CannotBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).
					Label("key", "val2").Obj(),
				testinghelper.MakePod().Namespace("default").Name("p6").UID("p6").Node("n2").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(15).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CannotBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).
					Label("key", "val2").Obj(),
				testinghelper.MakePod().Namespace("default").Name("p7").UID("p7").Node("n2").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(16).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CannotBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).
					Label("key", "val2").Obj(),
				testinghelper.MakePod().Namespace("default").Name("p8").UID("p8").Node("n2").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(17).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).
					Label("key", "val2").Obj(),
				testinghelper.MakePod().Namespace("default").Name("p9").UID("p9").Node("n1").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(18).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).Obj(),
				testinghelper.MakePod().Namespace("default").Name("p10").UID("p10").Node("n1").Req(map[v1.ResourceName]string{v1.ResourceCPU: "6"}).Priority(18).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).Obj(),
			},
			nodeList: []*v1.Node{
				testinghelper.MakeNode().Name("n1").Capacity(map[v1.ResourceName]string{v1.ResourceCPU: "8"}).Obj(),
				testinghelper.MakeNode().Name("n2").Capacity(map[v1.ResourceName]string{v1.ResourceCPU: "8"}).Obj(),
			},
			pdbs: []*policy.PodDisruptionBudget{
				testinghelper.MakePdb().Namespace("default").Name("pdb").DisruptionsAllowed(0).
					Label("key", "val").Obj(),
				testinghelper.MakePdb().Namespace("default").Name("pdb2").DisruptionsAllowed(2).
					Label("key", "val2").Obj(),
			},
			expectedNodeIndexes: [][]int{
				{1, 0},
				{1},
			},
			expectedMaxVictimPriority: 17,
		},
		{
			name: "forth and fifth",
			pod: testinghelper.MakePod().Namespace("default").Name("p").UID("p").Req(map[v1.ResourceName]string{v1.ResourceCPU: "2"}).Priority(20).
				Annotation(constraints.HardConstraintsAnnotationKey, noderesources.FitName).Obj(),
			existingPods: []*v1.Pod{
				testinghelper.MakePod().Namespace("default").Name("p1").UID("p1").Node("n2").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(10).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).
					Label("key", "val").Obj(),
				testinghelper.MakePod().Namespace("default").Name("p2").UID("p2").Node("n2").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(11).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).
					Label("key", "val").Obj(),
				testinghelper.MakePod().Namespace("default").Name("p3").UID("p3").Node("n2").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(12).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).
					Label("key", "val").Obj(),
				testinghelper.MakePod().Namespace("default").Name("p4").UID("p4").Node("n2").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(13).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).
					Label("key", "val2").Obj(),
				testinghelper.MakePod().Namespace("default").Name("p5").UID("p5").Node("n2").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(14).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).
					Label("key", "val2").Obj(),
				testinghelper.MakePod().Namespace("default").Name("p6").UID("p6").Node("n2").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(15).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).
					Label("key", "val2").Obj(),
				testinghelper.MakePod().Namespace("default").Name("p7").UID("p7").Node("n2").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(16).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).
					Label("key", "val2").Obj(),
				testinghelper.MakePod().Namespace("default").Name("p8").UID("p8").Node("n2").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(17).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).
					Label("key", "val2").Obj(),
				testinghelper.MakePod().Namespace("default").Name("p9").UID("p9").Node("n1").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(15).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).Obj(),
				testinghelper.MakePod().Namespace("default").Name("p10").UID("p10").Node("n1").Req(map[v1.ResourceName]string{v1.ResourceCPU: "6"}).Priority(15).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).Obj(),
			},
			nodeList: []*v1.Node{
				testinghelper.MakeNode().Name("n1").Capacity(map[v1.ResourceName]string{v1.ResourceCPU: "8"}).Obj(),
				testinghelper.MakeNode().Name("n2").Capacity(map[v1.ResourceName]string{v1.ResourceCPU: "8"}).Obj(),
			},
			pdbs: []*policy.PodDisruptionBudget{
				testinghelper.MakePdb().Namespace("default").Name("pdb").DisruptionsAllowed(0).
					Label("key", "val").Obj(),
				testinghelper.MakePdb().Namespace("default").Name("pdb2").DisruptionsAllowed(2).
					Label("key", "val2").Obj(),
			},
			expectedNodeIndexes: [][]int{
				{1, 0},
				{1, 0},
			},
			expectedMaxVictimPriority: 14,
		},
		{
			name: "forth and sixth",
			pod: testinghelper.MakePod().Namespace("default").Name("p").UID("p").Req(map[v1.ResourceName]string{v1.ResourceCPU: "2"}).Priority(20).
				Annotation(constraints.HardConstraintsAnnotationKey, noderesources.FitName).Obj(),
			existingPods: []*v1.Pod{
				testinghelper.MakePod().Namespace("default").Name("p1").UID("p1").Node("n2").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(10).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).
					Label("key", "val").Obj(),
				testinghelper.MakePod().Namespace("default").Name("p2").UID("p2").Node("n2").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(11).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).
					Label("key", "val").Obj(),
				testinghelper.MakePod().Namespace("default").Name("p3").UID("p3").Node("n2").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(12).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).
					Label("key", "val").Obj(),
				testinghelper.MakePod().Namespace("default").Name("p4").UID("p4").Node("n2").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(13).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).
					Label("key", "val2").Obj(),
				testinghelper.MakePod().Namespace("default").Name("p5").UID("p5").Node("n2").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(14).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CannotBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).
					Label("key", "val2").Obj(),
				testinghelper.MakePod().Namespace("default").Name("p6").UID("p6").Node("n2").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(15).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).
					Label("key", "val2").Obj(),
				testinghelper.MakePod().Namespace("default").Name("p7").UID("p7").Node("n2").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(16).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).
					Label("key", "val2").Obj(),
				testinghelper.MakePod().Namespace("default").Name("p8").UID("p8").Node("n2").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(17).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).
					Label("key", "val2").Obj(),
				testinghelper.MakePod().Namespace("default").Name("p9").UID("p9").Node("n1").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(16).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).Obj(),
				testinghelper.MakePod().Namespace("default").Name("p10").UID("p10").Node("n1").Req(map[v1.ResourceName]string{v1.ResourceCPU: "6"}).Priority(16).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).Obj(),
			},
			nodeList: []*v1.Node{
				testinghelper.MakeNode().Name("n1").Capacity(map[v1.ResourceName]string{v1.ResourceCPU: "8"}).Obj(),
				testinghelper.MakeNode().Name("n2").Capacity(map[v1.ResourceName]string{v1.ResourceCPU: "8"}).Obj(),
			},
			pdbs: []*policy.PodDisruptionBudget{
				testinghelper.MakePdb().Namespace("default").Name("pdb").DisruptionsAllowed(0).
					Label("key", "val").Obj(),
				testinghelper.MakePdb().Namespace("default").Name("pdb2").DisruptionsAllowed(2).
					Label("key", "val2").Obj(),
			},
			expectedNodeIndexes: [][]int{
				{1, 0},
				{1},
			},
			expectedMaxVictimPriority: 15,
		},
		{
			name: "forth and seventh",
			pod: testinghelper.MakePod().Namespace("default").Name("p").UID("p").Req(map[v1.ResourceName]string{v1.ResourceCPU: "2"}).Priority(20).
				Annotation(constraints.HardConstraintsAnnotationKey, noderesources.FitName).Obj(),
			existingPods: []*v1.Pod{
				testinghelper.MakePod().Namespace("default").Name("p1").UID("p1").Node("n2").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(10).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).
					Label("key", "val").Obj(),
				testinghelper.MakePod().Namespace("default").Name("p2").UID("p2").Node("n2").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(11).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).
					Label("key", "val").Obj(),
				testinghelper.MakePod().Namespace("default").Name("p3").UID("p3").Node("n2").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(12).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).
					Label("key", "val").Obj(),
				testinghelper.MakePod().Namespace("default").Name("p4").UID("p4").Node("n2").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(13).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).
					Label("key", "val2").Obj(),
				testinghelper.MakePod().Namespace("default").Name("p5").UID("p5").Node("n2").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(14).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CannotBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).
					Label("key", "val2").Obj(),
				testinghelper.MakePod().Namespace("default").Name("p6").UID("p6").Node("n2").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(15).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CannotBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).
					Label("key", "val2").Obj(),
				testinghelper.MakePod().Namespace("default").Name("p7").UID("p7").Node("n2").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(16).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).
					Label("key", "val2").Obj(),
				testinghelper.MakePod().Namespace("default").Name("p8").UID("p8").Node("n2").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(17).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).
					Label("key", "val2").Obj(),
				testinghelper.MakePod().Namespace("default").Name("p9").UID("p9").Node("n1").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(17).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).Obj(),
				testinghelper.MakePod().Namespace("default").Name("p10").UID("p10").Node("n1").Req(map[v1.ResourceName]string{v1.ResourceCPU: "6"}).Priority(17).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).Obj(),
			},
			nodeList: []*v1.Node{
				testinghelper.MakeNode().Name("n1").Capacity(map[v1.ResourceName]string{v1.ResourceCPU: "8"}).Obj(),
				testinghelper.MakeNode().Name("n2").Capacity(map[v1.ResourceName]string{v1.ResourceCPU: "8"}).Obj(),
			},
			pdbs: []*policy.PodDisruptionBudget{
				testinghelper.MakePdb().Namespace("default").Name("pdb").DisruptionsAllowed(0).
					Label("key", "val").Obj(),
				testinghelper.MakePdb().Namespace("default").Name("pdb2").DisruptionsAllowed(2).
					Label("key", "val2").Obj(),
			},
			expectedNodeIndexes: [][]int{
				{1, 0},
				{1},
			},
			expectedMaxVictimPriority: 16,
		},
		{
			name: "forth and eighth",
			pod: testinghelper.MakePod().Namespace("default").Name("p").UID("p").Req(map[v1.ResourceName]string{v1.ResourceCPU: "2"}).Priority(20).
				Annotation(constraints.HardConstraintsAnnotationKey, noderesources.FitName).Obj(),
			existingPods: []*v1.Pod{
				testinghelper.MakePod().Namespace("default").Name("p1").UID("p1").Node("n2").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(10).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).
					Label("key", "val").Obj(),
				testinghelper.MakePod().Namespace("default").Name("p2").UID("p2").Node("n2").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(11).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).
					Label("key", "val").Obj(),
				testinghelper.MakePod().Namespace("default").Name("p3").UID("p3").Node("n2").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(12).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).
					Label("key", "val").Obj(),
				testinghelper.MakePod().Namespace("default").Name("p4").UID("p4").Node("n2").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(13).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).
					Label("key", "val2").Obj(),
				testinghelper.MakePod().Namespace("default").Name("p5").UID("p5").Node("n2").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(14).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CannotBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).
					Label("key", "val2").Obj(),
				testinghelper.MakePod().Namespace("default").Name("p6").UID("p6").Node("n2").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(15).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CannotBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).
					Label("key", "val2").Obj(),
				testinghelper.MakePod().Namespace("default").Name("p7").UID("p7").Node("n2").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(16).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CannotBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).
					Label("key", "val2").Obj(),
				testinghelper.MakePod().Namespace("default").Name("p8").UID("p8").Node("n2").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(17).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).
					Label("key", "val2").Obj(),
				testinghelper.MakePod().Namespace("default").Name("p9").UID("p9").Node("n1").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(18).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).Obj(),
				testinghelper.MakePod().Namespace("default").Name("p10").UID("p10").Node("n1").Req(map[v1.ResourceName]string{v1.ResourceCPU: "6"}).Priority(18).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).Obj(),
			},
			nodeList: []*v1.Node{
				testinghelper.MakeNode().Name("n1").Capacity(map[v1.ResourceName]string{v1.ResourceCPU: "8"}).Obj(),
				testinghelper.MakeNode().Name("n2").Capacity(map[v1.ResourceName]string{v1.ResourceCPU: "8"}).Obj(),
			},
			pdbs: []*policy.PodDisruptionBudget{
				testinghelper.MakePdb().Namespace("default").Name("pdb").DisruptionsAllowed(0).
					Label("key", "val").Obj(),
				testinghelper.MakePdb().Namespace("default").Name("pdb2").DisruptionsAllowed(2).
					Label("key", "val2").Obj(),
			},
			expectedNodeIndexes: [][]int{
				{1, 0},
				{1},
			},
			expectedMaxVictimPriority: 17,
		},
		{
			name: "fifth and sixth",
			pod: testinghelper.MakePod().Namespace("default").Name("p").UID("p").Req(map[v1.ResourceName]string{v1.ResourceCPU: "2"}).Priority(20).
				Annotation(constraints.HardConstraintsAnnotationKey, noderesources.FitName).Obj(),
			existingPods: []*v1.Pod{
				testinghelper.MakePod().Namespace("default").Name("p1").UID("p1").Node("n2").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(10).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).
					Label("key", "val").Obj(),
				testinghelper.MakePod().Namespace("default").Name("p2").UID("p2").Node("n2").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(11).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).
					Label("key", "val").Obj(),
				testinghelper.MakePod().Namespace("default").Name("p3").UID("p3").Node("n2").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(12).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).
					Label("key", "val").Obj(),
				testinghelper.MakePod().Namespace("default").Name("p4").UID("p4").Node("n2").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(13).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).
					Label("key", "val").Obj(),
				testinghelper.MakePod().Namespace("default").Name("p5").UID("p5").Node("n2").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(14).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).
					Label("key", "val2").Obj(),
				testinghelper.MakePod().Namespace("default").Name("p6").UID("p6").Node("n2").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(15).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).
					Label("key", "val2").Obj(),
				testinghelper.MakePod().Namespace("default").Name("p7").UID("p7").Node("n2").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(16).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).
					Label("key", "val2").Obj(),
				testinghelper.MakePod().Namespace("default").Name("p8").UID("p8").Node("n2").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(17).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).
					Label("key", "val2").Obj(),
				testinghelper.MakePod().Namespace("default").Name("p9").UID("p9").Node("n1").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(16).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).Obj(),
				testinghelper.MakePod().Namespace("default").Name("p10").UID("p10").Node("n1").Req(map[v1.ResourceName]string{v1.ResourceCPU: "6"}).Priority(16).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).Obj(),
			},
			nodeList: []*v1.Node{
				testinghelper.MakeNode().Name("n1").Capacity(map[v1.ResourceName]string{v1.ResourceCPU: "8"}).Obj(),
				testinghelper.MakeNode().Name("n2").Capacity(map[v1.ResourceName]string{v1.ResourceCPU: "8"}).Obj(),
			},
			pdbs: []*policy.PodDisruptionBudget{
				testinghelper.MakePdb().Namespace("default").Name("pdb").DisruptionsAllowed(0).
					Label("key", "val").Obj(),
				testinghelper.MakePdb().Namespace("default").Name("pdb2").DisruptionsAllowed(2).
					Label("key", "val2").Obj(),
			},
			expectedNodeIndexes: [][]int{
				{1, 0},
				{1},
			},
			expectedMaxVictimPriority: 15,
		},
		{
			name: "fifth and seventh",
			pod: testinghelper.MakePod().Namespace("default").Name("p").UID("p").Req(map[v1.ResourceName]string{v1.ResourceCPU: "2"}).Priority(20).
				Annotation(constraints.HardConstraintsAnnotationKey, noderesources.FitName).Obj(),
			existingPods: []*v1.Pod{
				testinghelper.MakePod().Namespace("default").Name("p1").UID("p1").Node("n2").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(10).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).
					Label("key", "val").Obj(),
				testinghelper.MakePod().Namespace("default").Name("p2").UID("p2").Node("n2").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(11).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).
					Label("key", "val").Obj(),
				testinghelper.MakePod().Namespace("default").Name("p3").UID("p3").Node("n2").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(12).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).
					Label("key", "val").Obj(),
				testinghelper.MakePod().Namespace("default").Name("p4").UID("p4").Node("n2").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(13).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).
					Label("key", "val").Obj(),
				testinghelper.MakePod().Namespace("default").Name("p5").UID("p5").Node("n2").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(14).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).
					Label("key", "val2").Obj(),
				testinghelper.MakePod().Namespace("default").Name("p6").UID("p6").Node("n2").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(15).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CannotBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).
					Label("key", "val2").Obj(),
				testinghelper.MakePod().Namespace("default").Name("p7").UID("p7").Node("n2").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(16).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).
					Label("key", "val2").Obj(),
				testinghelper.MakePod().Namespace("default").Name("p8").UID("p8").Node("n2").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(17).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).
					Label("key", "val2").Obj(),
				testinghelper.MakePod().Namespace("default").Name("p9").UID("p9").Node("n1").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(17).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).Obj(),
				testinghelper.MakePod().Namespace("default").Name("p10").UID("p10").Node("n1").Req(map[v1.ResourceName]string{v1.ResourceCPU: "6"}).Priority(17).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).Obj(),
			},
			nodeList: []*v1.Node{
				testinghelper.MakeNode().Name("n1").Capacity(map[v1.ResourceName]string{v1.ResourceCPU: "8"}).Obj(),
				testinghelper.MakeNode().Name("n2").Capacity(map[v1.ResourceName]string{v1.ResourceCPU: "8"}).Obj(),
			},
			pdbs: []*policy.PodDisruptionBudget{
				testinghelper.MakePdb().Namespace("default").Name("pdb").DisruptionsAllowed(0).
					Label("key", "val").Obj(),
				testinghelper.MakePdb().Namespace("default").Name("pdb2").DisruptionsAllowed(2).
					Label("key", "val2").Obj(),
			},
			expectedNodeIndexes: [][]int{
				{1, 0},
				{1},
			},
			expectedMaxVictimPriority: 16,
		},
		{
			name: "fifth and eighth",
			pod: testinghelper.MakePod().Namespace("default").Name("p").UID("p").Req(map[v1.ResourceName]string{v1.ResourceCPU: "2"}).Priority(20).
				Annotation(constraints.HardConstraintsAnnotationKey, noderesources.FitName).Obj(),
			existingPods: []*v1.Pod{
				testinghelper.MakePod().Namespace("default").Name("p1").UID("p1").Node("n2").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(10).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).
					Label("key", "val").Obj(),
				testinghelper.MakePod().Namespace("default").Name("p2").UID("p2").Node("n2").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(11).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).
					Label("key", "val").Obj(),
				testinghelper.MakePod().Namespace("default").Name("p3").UID("p3").Node("n2").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(12).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).
					Label("key", "val").Obj(),
				testinghelper.MakePod().Namespace("default").Name("p4").UID("p4").Node("n2").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(13).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).
					Label("key", "val").Obj(),
				testinghelper.MakePod().Namespace("default").Name("p5").UID("p5").Node("n2").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(14).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).
					Label("key", "val2").Obj(),
				testinghelper.MakePod().Namespace("default").Name("p6").UID("p6").Node("n2").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(15).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CannotBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).
					Label("key", "val2").Obj(),
				testinghelper.MakePod().Namespace("default").Name("p7").UID("p7").Node("n2").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(16).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CannotBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).
					Label("key", "val2").Obj(),
				testinghelper.MakePod().Namespace("default").Name("p8").UID("p8").Node("n2").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(17).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).
					Label("key", "val2").Obj(),
				testinghelper.MakePod().Namespace("default").Name("p9").UID("p9").Node("n1").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(18).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).Obj(),
				testinghelper.MakePod().Namespace("default").Name("p10").UID("p10").Node("n1").Req(map[v1.ResourceName]string{v1.ResourceCPU: "6"}).Priority(18).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).Obj(),
			},
			nodeList: []*v1.Node{
				testinghelper.MakeNode().Name("n1").Capacity(map[v1.ResourceName]string{v1.ResourceCPU: "8"}).Obj(),
				testinghelper.MakeNode().Name("n2").Capacity(map[v1.ResourceName]string{v1.ResourceCPU: "8"}).Obj(),
			},
			pdbs: []*policy.PodDisruptionBudget{
				testinghelper.MakePdb().Namespace("default").Name("pdb").DisruptionsAllowed(0).
					Label("key", "val").Obj(),
				testinghelper.MakePdb().Namespace("default").Name("pdb2").DisruptionsAllowed(2).
					Label("key", "val2").Obj(),
			},
			expectedNodeIndexes: [][]int{
				{1, 0},
				{1},
			},
			expectedMaxVictimPriority: 17,
		},
		{
			name: "sixth and seventh",
			pod: testinghelper.MakePod().Namespace("default").Name("p").UID("p").Req(map[v1.ResourceName]string{v1.ResourceCPU: "2"}).Priority(20).
				Annotation(constraints.HardConstraintsAnnotationKey, noderesources.FitName).Obj(),
			existingPods: []*v1.Pod{
				testinghelper.MakePod().Namespace("default").Name("p1").UID("p1").Node("n2").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(10).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).
					Label("key", "val").Obj(),
				testinghelper.MakePod().Namespace("default").Name("p2").UID("p2").Node("n2").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(11).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).
					Label("key", "val").Obj(),
				testinghelper.MakePod().Namespace("default").Name("p3").UID("p3").Node("n2").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(12).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).
					Label("key", "val").Obj(),
				testinghelper.MakePod().Namespace("default").Name("p4").UID("p4").Node("n2").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(13).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).
					Label("key", "val").Obj(),
				testinghelper.MakePod().Namespace("default").Name("p5").UID("p5").Node("n2").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(14).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).
					Label("key", "val").Obj(),
				testinghelper.MakePod().Namespace("default").Name("p6").UID("p6").Node("n2").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(15).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).
					Label("key", "val2").Obj(),
				testinghelper.MakePod().Namespace("default").Name("p7").UID("p7").Node("n2").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(16).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).
					Label("key", "val2").Obj(),
				testinghelper.MakePod().Namespace("default").Name("p8").UID("p8").Node("n2").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(17).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).
					Label("key", "val2").Obj(),
				testinghelper.MakePod().Namespace("default").Name("p9").UID("p9").Node("n1").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(17).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).Obj(),
				testinghelper.MakePod().Namespace("default").Name("p10").UID("p10").Node("n1").Req(map[v1.ResourceName]string{v1.ResourceCPU: "6"}).Priority(17).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).Obj(),
			},
			nodeList: []*v1.Node{
				testinghelper.MakeNode().Name("n1").Capacity(map[v1.ResourceName]string{v1.ResourceCPU: "8"}).Obj(),
				testinghelper.MakeNode().Name("n2").Capacity(map[v1.ResourceName]string{v1.ResourceCPU: "8"}).Obj(),
			},
			pdbs: []*policy.PodDisruptionBudget{
				testinghelper.MakePdb().Namespace("default").Name("pdb").DisruptionsAllowed(0).
					Label("key", "val").Obj(),
				testinghelper.MakePdb().Namespace("default").Name("pdb2").DisruptionsAllowed(2).
					Label("key", "val2").Obj(),
			},
			expectedNodeIndexes: [][]int{
				{1, 0},
				{1},
			},
			expectedMaxVictimPriority: 16,
		},
		{
			name: "sixth and eighth",
			pod: testinghelper.MakePod().Namespace("default").Name("p").UID("p").Req(map[v1.ResourceName]string{v1.ResourceCPU: "2"}).Priority(20).
				Annotation(constraints.HardConstraintsAnnotationKey, noderesources.FitName).Obj(),
			existingPods: []*v1.Pod{
				testinghelper.MakePod().Namespace("default").Name("p1").UID("p1").Node("n2").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(10).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).
					Label("key", "val").Obj(),
				testinghelper.MakePod().Namespace("default").Name("p2").UID("p2").Node("n2").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(11).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).
					Label("key", "val").Obj(),
				testinghelper.MakePod().Namespace("default").Name("p3").UID("p3").Node("n2").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(12).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).
					Label("key", "val").Obj(),
				testinghelper.MakePod().Namespace("default").Name("p4").UID("p4").Node("n2").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(13).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).
					Label("key", "val").Obj(),
				testinghelper.MakePod().Namespace("default").Name("p5").UID("p5").Node("n2").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(14).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).
					Label("key", "val").Obj(),
				testinghelper.MakePod().Namespace("default").Name("p6").UID("p6").Node("n2").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(15).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).
					Label("key", "val2").Obj(),
				testinghelper.MakePod().Namespace("default").Name("p7").UID("p7").Node("n2").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(16).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CannotBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).
					Label("key", "val2").Obj(),
				testinghelper.MakePod().Namespace("default").Name("p8").UID("p8").Node("n2").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(17).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).
					Label("key", "val2").Obj(),
				testinghelper.MakePod().Namespace("default").Name("p9").UID("p9").Node("n1").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(18).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).Obj(),
				testinghelper.MakePod().Namespace("default").Name("p10").UID("p10").Node("n1").Req(map[v1.ResourceName]string{v1.ResourceCPU: "6"}).Priority(18).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).Obj(),
			},
			nodeList: []*v1.Node{
				testinghelper.MakeNode().Name("n1").Capacity(map[v1.ResourceName]string{v1.ResourceCPU: "8"}).Obj(),
				testinghelper.MakeNode().Name("n2").Capacity(map[v1.ResourceName]string{v1.ResourceCPU: "8"}).Obj(),
			},
			pdbs: []*policy.PodDisruptionBudget{
				testinghelper.MakePdb().Namespace("default").Name("pdb").DisruptionsAllowed(0).
					Label("key", "val").Obj(),
				testinghelper.MakePdb().Namespace("default").Name("pdb2").DisruptionsAllowed(2).
					Label("key", "val2").Obj(),
			},
			expectedNodeIndexes: [][]int{
				{1, 0},
				{1},
			},
			expectedMaxVictimPriority: 17,
		},
		{
			name: "seventh and eighth",
			pod: testinghelper.MakePod().Namespace("default").Name("p").UID("p").Req(map[v1.ResourceName]string{v1.ResourceCPU: "2"}).Priority(20).
				Annotation(constraints.HardConstraintsAnnotationKey, noderesources.FitName).Obj(),
			existingPods: []*v1.Pod{
				testinghelper.MakePod().Namespace("default").Name("p1").UID("p1").Node("n2").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(10).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).
					Label("key", "val").Obj(),
				testinghelper.MakePod().Namespace("default").Name("p2").UID("p2").Node("n2").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(11).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).
					Label("key", "val").Obj(),
				testinghelper.MakePod().Namespace("default").Name("p3").UID("p3").Node("n2").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(12).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).
					Label("key", "val").Obj(),
				testinghelper.MakePod().Namespace("default").Name("p4").UID("p4").Node("n2").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(13).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).
					Label("key", "val").Obj(),
				testinghelper.MakePod().Namespace("default").Name("p5").UID("p5").Node("n2").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(14).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).
					Label("key", "val").Obj(),
				testinghelper.MakePod().Namespace("default").Name("p6").UID("p6").Node("n2").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(15).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).
					Label("key", "val").Obj(),
				testinghelper.MakePod().Namespace("default").Name("p7").UID("p7").Node("n2").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(16).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).
					Label("key", "val2").Obj(),
				testinghelper.MakePod().Namespace("default").Name("p8").UID("p8").Node("n2").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(17).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).
					Label("key", "val2").Obj(),
				testinghelper.MakePod().Namespace("default").Name("p9").UID("p9").Node("n1").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(18).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).Obj(),
				testinghelper.MakePod().Namespace("default").Name("p10").UID("p10").Node("n1").Req(map[v1.ResourceName]string{v1.ResourceCPU: "6"}).Priority(18).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).Obj(),
			},
			nodeList: []*v1.Node{
				testinghelper.MakeNode().Name("n1").Capacity(map[v1.ResourceName]string{v1.ResourceCPU: "8"}).Obj(),
				testinghelper.MakeNode().Name("n2").Capacity(map[v1.ResourceName]string{v1.ResourceCPU: "8"}).Obj(),
			},
			pdbs: []*policy.PodDisruptionBudget{
				testinghelper.MakePdb().Namespace("default").Name("pdb").DisruptionsAllowed(0).
					Label("key", "val").Obj(),
				testinghelper.MakePdb().Namespace("default").Name("pdb2").DisruptionsAllowed(2).
					Label("key", "val2").Obj(),
			},
			expectedNodeIndexes: [][]int{
				{1, 0},
				{1},
			},
			expectedMaxVictimPriority: 17,
		},
		{
			name: "can not be preempted",
			pod: testinghelper.MakePod().Namespace("default").Name("p").UID("p").Req(map[v1.ResourceName]string{v1.ResourceCPU: "2"}).Priority(20).
				Annotation(constraints.HardConstraintsAnnotationKey, noderesources.FitName).Obj(),
			existingPods: []*v1.Pod{
				testinghelper.MakePod().Namespace("default").Name("p1").UID("p1").Node("n2").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(10).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).
					Label("key", "val").Obj(),
				testinghelper.MakePod().Namespace("default").Name("p2").UID("p2").Node("n2").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(11).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).
					Label("key", "val").Obj(),
				testinghelper.MakePod().Namespace("default").Name("p3").UID("p3").Node("n2").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(12).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).
					Label("key", "val").Obj(),
				testinghelper.MakePod().Namespace("default").Name("p4").UID("p4").Node("n2").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(13).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).
					Label("key", "val").Obj(),
				testinghelper.MakePod().Namespace("default").Name("p5").UID("p5").Node("n2").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(14).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).
					Label("key", "val").Obj(),
				testinghelper.MakePod().Namespace("default").Name("p6").UID("p6").Node("n2").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(15).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).
					Label("key", "val").Obj(),
				testinghelper.MakePod().Namespace("default").Name("p7").UID("p7").Node("n2").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(16).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).
					Label("key", "val2").Obj(),
				testinghelper.MakePod().Namespace("default").Name("p8").UID("p8").Node("n2").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(17).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).
					Label("key", "val2").Obj(),
				testinghelper.MakePod().Namespace("default").Name("p9").UID("p9").Node("n1").Req(map[v1.ResourceName]string{v1.ResourceCPU: "1"}).Priority(18).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).
					Label("key", "val").Obj(),
				testinghelper.MakePod().Namespace("default").Name("p10").UID("p10").Node("n1").Req(map[v1.ResourceName]string{v1.ResourceCPU: "6"}).Priority(18).PriorityClassName("pc").
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).
					Label("key", "val").Obj(),
			},
			nodeList: []*v1.Node{
				testinghelper.MakeNode().Name("n1").Capacity(map[v1.ResourceName]string{v1.ResourceCPU: "8"}).Obj(),
				testinghelper.MakeNode().Name("n2").Capacity(map[v1.ResourceName]string{v1.ResourceCPU: "8"}).Obj(),
			},
			pdbs: []*policy.PodDisruptionBudget{
				testinghelper.MakePdb().Namespace("default").Name("pdb").DisruptionsAllowed(0).
					Label("key", "val").Obj(),
				testinghelper.MakePdb().Namespace("default").Name("pdb2").DisruptionsAllowed(1).
					Label("key", "val2").Obj(),
			},
			expectedNodeIndexes: [][]int{
				nil,
				nil,
			},
			expectedMaxVictimPriority: -1,
		},
	}

	testFunc := func(gs podScheduler, index int) {
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				cache := godelcache.New(handler.MakeCacheHandlerWrapper().
					SchedulerName("").SchedulerType("").SubCluster(framework.DefaultSubCluster).
					TTL(time.Second).Period(10 * time.Second).StopCh(make(<-chan struct{})).
					EnableStore("PreemptionStore").
					Obj())
				snapshot := godelcache.NewEmptySnapshot(handler.MakeCacheHandlerWrapper().
					SubCluster(framework.DefaultSubCluster).SwitchType(framework.DefaultSubClusterSwitchType).
					EnableStore("PreemptionStore").
					Obj())

				for _, pod := range tt.existingPods {
					cache.AddPod(pod)
				}
				for _, node := range tt.nodeList {
					cache.AddNode(node)
				}

				for _, pdb := range tt.pdbs {
					cache.AddPDB(pdb)
				}

				cache.UpdateSnapshot(snapshot)
				gs.snapshot = snapshot

				nodeResourcesPlugin, err := noderesources.NewFit(nil, nil)
				if err != nil {
					t.Errorf("Could not new node resources plugin: %v", err)
				}

				fh, _ := st.NewSchedulerFrameworkHandle(gs.clientSet, gs.crdClient, gs.informerFactory, gs.crdInformerFactory, cache, snapshot, framework.PluginMap{noderesources.FitName: nodeResourcesPlugin}, nil, nil, nil)
				nonNativeTopologyPlugin, err := nonnativeresource.NewNonNativeTopology(nil, fh)
				if err != nil {
					t.Errorf("Could not new nonnative topology plugin: %v", err)
				}
				basePlugins, preemptionPluginRegistry, err := utils.GetPreemptionRelatedPlugins(fh)
				if err != nil {
					t.Errorf("failed to get defualt plugins and configs: %v", err)
				}

				filterPluginRegistry := &framework.OrderedPluginRegistry{Plugins: []string{noderesources.FitName}}
				fh, _ = st.NewSchedulerFrameworkHandle(gs.clientSet, gs.crdClient, gs.informerFactory, gs.crdInformerFactory, cache, snapshot, framework.PluginMap{noderesources.FitName: nodeResourcesPlugin, nonnativeresource.NonNativeTopologyName: nonNativeTopologyPlugin}, preemptionPluginRegistry, filterPluginRegistry, basePlugins)
				gs.betterSelectPoliciesRegistry = map[string]betterSelectPolicy{
					config.BetterPreemptionPolicyAscending: gs.ascendingOrderPreemption,
					config.BetterPreemptionPolicyDichotomy: gs.dichotomyPreemption,
				}
				fwk, _ := fh.GetFrameworkForPod(tt.pod)
				state, err := fwk.InitCycleState(tt.pod)
				if err != nil {
					t.Fatal(err)
				}
				pfwk := fh.GetPreemptionFrameworkForPod(tt.pod)
				gs.schedulerPreemptionFramework = pfwk
				commonState := framework.NewCycleState()

				if status := fwk.RunPreFilterPlugins(context.Background(), state, tt.pod); !status.IsSuccess() {
					t.Errorf("Unexpected PreFilter Status: %v", status)
				}

				var nodeInfoList []framework.NodeInfo
				nodeLister := snapshot.NodeInfos()
				for _, node := range tt.nodeList {
					nInfo, _ := nodeLister.Get(node.Name)
					nodeInfoList = append(nodeInfoList, nInfo)
				}

				resourceType, _ := podutil.GetPodResourceType(tt.pod)
				stop := false
				priorities := make([][]int64, len(nodeInfoList))
				getPriorities := func(i int) {
					nodeInfo := nodeInfoList[i]
					if nodeInfo == nil {
						return
					}
					priorities[i] = nodeInfo.GetPrioritiesForPodsMayBePreempted(resourceType)
				}
				util.ParallelizeUntil(&stop, 32, len(nodeInfoList), getPriorities)

				prioritySet := sets.NewInt()
				for _, prioritiesEachNode := range priorities {
					for _, priority := range prioritiesEachNode {
						prioritySet.Insert(int(priority))
					}
				}

				podPriority := podutil.GetPodPriority(tt.pod)
				var prioritiesSlice []int
				indexedPriorities := map[int]int{}
				for _, priority := range prioritySet.List() {
					if priority > int(podPriority) {
						continue
					}
					indexedPriorities[priority] = len(prioritiesSlice)
					prioritiesSlice = append(prioritiesSlice, priority)
				}

				pfwk.RunClusterPrePreemptingPlugins(tt.pod, state, commonState)
				nodeIndexes, maxVictimPriorityIndex := gs.betterSelectPoliciesRegistry[gs.betterSelectPolicies[0]](context.Background(), state, tt.pod, resourceType, prioritiesSlice, nodeInfoList, 3, fwk, pfwk)
				if !reflect.DeepEqual(tt.expectedNodeIndexes[index], nodeIndexes) {
					t.Errorf("expected %v but got %v", tt.expectedNodeIndexes[index], nodeIndexes)
				}
				if maxVictimPriorityIndex == -1 && tt.expectedMaxVictimPriority != -1 {
					t.Errorf("expected max victim priority %d but got nil", tt.expectedMaxVictimPriority)
				} else if maxVictimPriorityIndex != -1 && tt.expectedMaxVictimPriority == -1 {
					t.Errorf("expected max victim priority nil but got %d", prioritiesSlice[maxVictimPriorityIndex])
				} else if maxVictimPriorityIndex != -1 && tt.expectedMaxVictimPriority != -1 {
					if tt.expectedMaxVictimPriority != prioritiesSlice[maxVictimPriorityIndex] {
						t.Errorf("expected max victim priority %d but got %d", tt.expectedMaxVictimPriority, prioritiesSlice[maxVictimPriorityIndex])
					}
				}
			})
		}
	}

	// ascendingPreemption
	{
		crdClient := godelclientfake.NewSimpleClientset()
		crdInformerFactory := crdinformers.NewSharedInformerFactory(crdClient, 0)
		client := clientsetfake.NewSimpleClientset()
		informerFactory := informers.NewSharedInformerFactory(client, 0)
		pl1 := podScheduler{
			clientSet:             client,
			informerFactory:       informerFactory,
			crdClient:             crdClient,
			crdInformerFactory:    crdInformerFactory,
			candidateSelectPolicy: config.CandidateSelectPolicyBetter,
			betterSelectPolicies:  []string{config.BetterPreemptionPolicyAscending},
		}
		testFunc(pl1, 0)
	}

	// dichotomyPreemption
	{
		crdClient := godelclientfake.NewSimpleClientset()
		crdInformerFactory := crdinformers.NewSharedInformerFactory(crdClient, 0)
		client := clientsetfake.NewSimpleClientset()
		informerFactory := informers.NewSharedInformerFactory(client, 0)
		pl2 := podScheduler{
			clientSet:             client,
			informerFactory:       informerFactory,
			crdClient:             crdClient,
			crdInformerFactory:    crdInformerFactory,
			candidateSelectPolicy: config.CandidateSelectPolicyBetter,
			betterSelectPolicies:  []string{config.BetterPreemptionPolicyDichotomy},
		}
		testFunc(pl2, 1)
	}
}

func TestPreemptBasedOnCachedNominatedNodes(t *testing.T) {
	pdbs := []*policy.PodDisruptionBudget{
		testinghelper.MakePdb().Namespace("default").Name("pdb1").DisruptionsAllowed(0).
			Label("key1", "val1").Obj(),
		testinghelper.MakePdb().Namespace("default").Name("pdb2").DisruptionsAllowed(2).
			Label("key2", "val2").Obj(),
	}
	existingNodes := []*v1.Node{
		testinghelper.MakeNode().Name("n1").Capacity(map[v1.ResourceName]string{"cpu": "10"}).Obj(),
		testinghelper.MakeNode().Name("n2").Capacity(map[v1.ResourceName]string{"cpu": "10"}).Obj(),
		testinghelper.MakeNode().Name("n3").Capacity(map[v1.ResourceName]string{"cpu": "10"}).Obj(),
	}

	existingPods := []*v1.Pod{
		testinghelper.MakePod().Namespace("default").Name("p1").UID("p1").Node("n1").Priority(10).PriorityClassName("pc").
			Req(map[v1.ResourceName]string{"cpu": "5"}).
			Label("key1", "val1").
			Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).Obj(),
		testinghelper.MakePod().Namespace("default").Name("p2").UID("p2").Node("n1").Priority(11).PriorityClassName("pc").
			Req(map[v1.ResourceName]string{"cpu": "5"}).
			Label("key2", "val2").
			Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).Obj(),
		testinghelper.MakePod().Namespace("default").Name("p3").UID("p3").Node("n2").Priority(12).PriorityClassName("pc").
			Req(map[v1.ResourceName]string{"cpu": "5"}).
			Label("key1", "val1").
			Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).Obj(),
		testinghelper.MakePod().Namespace("default").Name("p4").UID("p4").Node("n2").Priority(13).PriorityClassName("pc").
			Req(map[v1.ResourceName]string{"cpu": "5"}).
			Label("key2", "val2").
			Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).Obj(),
		testinghelper.MakePod().Namespace("default").Name("p5").UID("p5").Node("n3").Priority(14).PriorityClassName("pc").
			Req(map[v1.ResourceName]string{"cpu": "5"}).
			Label("key2", "val2").
			Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).Obj(),
		testinghelper.MakePod().Namespace("default").Name("p6").UID("p6").Node("n3").Priority(15).PriorityClassName("pc").
			Req(map[v1.ResourceName]string{"cpu": "5"}).
			Label("key2", "val2").
			Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).Obj(),
	}

	preemptor := testinghelper.MakePod().Name("foo").UID("foo").Priority(100).
		Req(map[v1.ResourceName]string{"cpu": "10"}).
		Annotation(constraints.HardConstraintsAnnotationKey, noderesources.FitName).Obj()
	crdClient := godelclientfake.NewSimpleClientset()
	crdInformerFactory := crdinformers.NewSharedInformerFactory(crdClient, 0)
	client := clientsetfake.NewSimpleClientset()
	informerFactory := informers.NewSharedInformerFactory(client, 0)
	isolatedcache := isolatedcache.NewIsolatedCache()
	gs := podScheduler{
		clientSet:             client,
		informerFactory:       informerFactory,
		crdClient:             crdClient,
		crdInformerFactory:    crdInformerFactory,
		isolatedCache:         isolatedcache,
		candidateSelectPolicy: config.CandidateSelectPolicyBetter,
		betterSelectPolicies:  []string{config.BetterPreemptionPolicyAscending},
	}

	cache := godelcache.New(handler.MakeCacheHandlerWrapper().
		SchedulerName("").SchedulerType("").SubCluster(framework.DefaultSubCluster).
		TTL(time.Second).Period(10 * time.Second).StopCh(make(<-chan struct{})).
		EnableStore("PreemptionStore").
		Obj())
	snapshot := godelcache.NewEmptySnapshot(handler.MakeCacheHandlerWrapper().
		SubCluster(framework.DefaultSubCluster).SwitchType(framework.DefaultSubClusterSwitchType).
		EnableStore("PreemptionStore").
		Obj())
	gs.snapshot = snapshot

	for _, pod := range existingPods {
		cache.AddPod(pod)
	}
	for _, node := range existingNodes {
		cache.AddNode(node)
	}
	for _, pdb := range pdbs {
		cache.AddPDB(pdb)
	}
	cache.UpdateSnapshot(snapshot)

	nodeResourcesPlugin, err := noderesources.NewFit(nil, nil)
	if err != nil {
		t.Errorf("Could not new node resources plugin: %v", err)
	}

	fh, _ := st.NewSchedulerFrameworkHandle(gs.clientSet, gs.crdClient, gs.informerFactory, gs.crdInformerFactory, cache, snapshot, framework.PluginMap{noderesources.FitName: nodeResourcesPlugin}, nil, nil, nil)
	nonNativeTopologyPlugin, err := nonnativeresource.NewNonNativeTopology(nil, fh)
	if err != nil {
		t.Errorf("Could not new nonnative topology plugin: %v", err)
	}
	basePlugins, preemptionPluginRegistry, err := utils.GetPreemptionRelatedPlugins(fh)
	if err != nil {
		t.Errorf("failed to get defualt plugins and configs: %v", err)
	}

	filterPluginRegistry := &framework.OrderedPluginRegistry{Plugins: []string{noderesources.FitName}}
	fh, _ = st.NewSchedulerFrameworkHandle(gs.clientSet, gs.crdClient, gs.informerFactory, gs.crdInformerFactory, cache, snapshot, framework.PluginMap{noderesources.FitName: nodeResourcesPlugin, nonnativeresource.NonNativeTopologyName: nonNativeTopologyPlugin}, preemptionPluginRegistry, filterPluginRegistry, basePlugins)
	gs.betterSelectPoliciesRegistry = map[string]betterSelectPolicy{
		config.BetterPreemptionPolicyAscending: gs.ascendingOrderPreemption,
		config.BetterPreemptionPolicyDichotomy: gs.dichotomyPreemption,
	}
	fwk, _ := fh.GetFrameworkForPod(preemptor)
	state, err := fwk.InitCycleState(preemptor)
	if err != nil {
		t.Fatal(err)
	}
	pfwk := fh.GetPreemptionFrameworkForPod(preemptor)
	commonState := framework.NewCycleState()

	cachedNominatedNodes := &framework.CachedNominatedNodes{}
	cachedNominatedNodes.SetPodCount(3)
	used := &framework.Candidate{
		Name: "n1",
		Victims: &framework.Victims{
			Pods:            []*v1.Pod{existingPods[1], existingPods[0]},
			PreemptionState: framework.NewCycleState(),
		},
	}
	cachedNominatedNodes.SetUsedNominatedNode(used)
	unused := []*framework.Candidate{
		{
			Name: "n2",
			Victims: &framework.Victims{
				Pods:            []*v1.Pod{existingPods[3], existingPods[2]},
				PreemptionState: framework.NewCycleState(),
			},
		},
		{
			Name: "n3",
			Victims: &framework.Victims{
				Pods:            []*v1.Pod{existingPods[5], existingPods[4]},
				PreemptionState: framework.NewCycleState(),
			},
		},
	}
	cachedNominatedNodes.SetUnusedNominatedNodes(unused)
	nodeName, victims := gs.PreemptBasedOnCachedNominatedNodes(context.Background(), fwk, pfwk, state, commonState, preemptor, cachedNominatedNodes)
	if nodeName != "n3" {
		t.Errorf("expected n3 but got %s", nodeName)
	}

	if !reflect.DeepEqual(unused[1].Victims.Pods, victims.Pods) {
		t.Errorf("expected %v but got %v", unused[1].Victims, victims)
	}
	newUsed := cachedNominatedNodes.GetUsedNominatedNode()
	if newUsed != nil {
		t.Errorf("expected nil but got %v", newUsed)
	}
	newUnused := cachedNominatedNodes.GetUnusedNominatedNodes()
	if len(newUnused) != 0 {
		t.Errorf("expected nil but got %v", newUnused)
	}
	pdbCommonState, _, _ := pdbchecker.GetPDBCommonState(commonState)
	pdbsAllowed := pdbCommonState.GetPDBsAllowed()
	pdbItems := pdbCommonState.GetPDBItems()
	pdbAllowedMap := map[string]int32{}
	for i, item := range pdbItems {
		pdbAllowedMap[item.GetPDB().Name] = pdbsAllowed[i]
	}
	expectedPDBAllowedMap := map[string]int32{
		"pdb1": 0,
		"pdb2": 0,
	}
	if !reflect.DeepEqual(expectedPDBAllowedMap, pdbAllowedMap) {
		t.Errorf("expected %v but got %v", expectedPDBAllowedMap, pdbAllowedMap)
	}
}

func TestPreemptInSpecificNodeGroup(t *testing.T) {
	tests := []struct {
		name                      string
		existingPods              []*v1.Pod
		podCount                  int
		cachedUsedNode            *framework.Candidate
		cachedUnusedNodes         []*framework.Candidate
		crossNodesConstraints     bool
		expectedResult            core.PodScheduleResult
		expectedPodCount          int
		expectedCachedUsedNode    *framework.Candidate
		expectedCachedUnusedNodes []*framework.Candidate
	}{
		{
			name: "in cache used, has victims, cached pod count is 2",
			existingPods: []*v1.Pod{
				testinghelper.MakePod().Namespace("p1").Name("p1").UID("p1").Node("n1").
					Priority(10).PriorityClassName("pc").
					Req(map[v1.ResourceName]string{"cpu": "10"}).
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).Obj(),
				testinghelper.MakePod().Namespace("p2").Name("p2").UID("p2").Node("n2").
					Priority(10).PriorityClassName("pc").
					Req(map[v1.ResourceName]string{"cpu": "10"}).
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).Obj(),
				testinghelper.MakePod().Namespace("p3").Name("p3").UID("p3").Node("n3").
					Priority(10).PriorityClassName("pc").
					Req(map[v1.ResourceName]string{"cpu": "10"}).
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).Obj(),
				testinghelper.MakePod().Namespace("p4").Name("p4").UID("p4").Node("n4").
					Priority(10).PriorityClassName("pc").
					Req(map[v1.ResourceName]string{"cpu": "10"}).
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).Obj(),
				testinghelper.MakePod().Namespace("p5").Name("p5").UID("p5").Node("n5").
					Priority(10).PriorityClassName("pc").
					Req(map[v1.ResourceName]string{"cpu": "10"}).
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).Obj(),
				testinghelper.MakePod().Namespace("p6").Name("p6").UID("p6").Node("n6").
					Priority(5).PriorityClassName("pc").
					Req(map[v1.ResourceName]string{"cpu": "10"}).
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).Obj(),
			},
			podCount: 2,
			cachedUsedNode: &framework.Candidate{
				Name: "n6",
				Victims: &framework.Victims{
					Pods: []*v1.Pod{
						testinghelper.MakePod().Namespace("p6").Name("p6").UID("p6").Node("n6").
							Priority(5).PriorityClassName("pc").
							Req(map[v1.ResourceName]string{"cpu": "10"}).
							Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).Obj(),
					},
					PreemptionState: framework.NewCycleState(),
				},
			},
			cachedUnusedNodes: []*framework.Candidate{
				{
					Name: "n5",
					Victims: &framework.Victims{
						Pods: []*v1.Pod{
							testinghelper.MakePod().Namespace("p5").Name("p5").UID("p5").Node("n5").
								Priority(10).PriorityClassName("pc").
								Req(map[v1.ResourceName]string{"cpu": "10"}).
								Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).Obj(),
						},
						PreemptionState: framework.NewCycleState(),
					},
				},
			},
			expectedResult: core.PodScheduleResult{
				NominatedNode: &framework.NominatedNode{
					NodeName: "n6",
					VictimPods: framework.VictimPods{
						{
							Name:      "p6",
							Namespace: "p6",
							UID:       "p6",
						},
					},
				},
				Victims: &framework.Victims{
					Pods: []*v1.Pod{
						testinghelper.MakePod().Namespace("p6").Name("p6").UID("p6").Node("n6").
							Priority(5).PriorityClassName("pc").
							Req(map[v1.ResourceName]string{"cpu": "10"}).
							Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).Obj(),
					},
					PreemptionState: framework.NewCycleState(),
				},
			},
			expectedPodCount: 1,
			expectedCachedUsedNode: &framework.Candidate{
				Name: "n6",
				Victims: &framework.Victims{
					Pods: []*v1.Pod{
						testinghelper.MakePod().Namespace("p6").Name("p6").UID("p6").Node("n6").
							Priority(5).PriorityClassName("pc").
							Req(map[v1.ResourceName]string{"cpu": "10"}).
							Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).Obj(),
					},
					PreemptionState: framework.NewCycleState(),
				},
			},
			expectedCachedUnusedNodes: []*framework.Candidate{
				{
					Name: "n5",
					Victims: &framework.Victims{
						Pods: []*v1.Pod{
							testinghelper.MakePod().Namespace("p5").Name("p5").UID("p5").Node("n5").
								Priority(10).PriorityClassName("pc").
								Req(map[v1.ResourceName]string{"cpu": "10"}).
								Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).Obj(),
						},
						PreemptionState: framework.NewCycleState(),
					},
				},
			},
		},
		{
			name: "in cache used, has victims, cached pod count is 1",
			existingPods: []*v1.Pod{
				testinghelper.MakePod().Namespace("p1").Name("p1").UID("p1").Node("n1").
					Priority(10).PriorityClassName("pc").
					Req(map[v1.ResourceName]string{"cpu": "10"}).
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).Obj(),
				testinghelper.MakePod().Namespace("p2").Name("p2").UID("p2").Node("n2").
					Priority(10).PriorityClassName("pc").
					Req(map[v1.ResourceName]string{"cpu": "10"}).
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).Obj(),
				testinghelper.MakePod().Namespace("p3").Name("p3").UID("p3").Node("n3").
					Priority(10).PriorityClassName("pc").
					Req(map[v1.ResourceName]string{"cpu": "10"}).
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).Obj(),
				testinghelper.MakePod().Namespace("p4").Name("p4").UID("p4").Node("n4").
					Priority(10).PriorityClassName("pc").
					Req(map[v1.ResourceName]string{"cpu": "10"}).
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).Obj(),
				testinghelper.MakePod().Namespace("p5").Name("p5").UID("p5").Node("n5").
					Priority(10).PriorityClassName("pc").
					Req(map[v1.ResourceName]string{"cpu": "10"}).
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).Obj(),
				testinghelper.MakePod().Namespace("p6").Name("p6").UID("p6").Node("n6").
					Priority(5).PriorityClassName("pc").
					Req(map[v1.ResourceName]string{"cpu": "10"}).
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).Obj(),
			},
			podCount: 1,
			cachedUsedNode: &framework.Candidate{
				Name: "n6",
				Victims: &framework.Victims{
					Pods: []*v1.Pod{
						testinghelper.MakePod().Namespace("p6").Name("p6").UID("p6").Node("n6").
							Priority(5).PriorityClassName("pc").
							Req(map[v1.ResourceName]string{"cpu": "10"}).
							Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).Obj(),
					},
					PreemptionState: framework.NewCycleState(),
				},
			},
			cachedUnusedNodes: []*framework.Candidate{
				{
					Name: "n5",
					Victims: &framework.Victims{
						Pods: []*v1.Pod{
							testinghelper.MakePod().Namespace("p5").Name("p5").UID("p5").Node("n5").
								Priority(10).PriorityClassName("pc").
								Req(map[v1.ResourceName]string{"cpu": "10"}).
								Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).Obj(),
						},
						PreemptionState: framework.NewCycleState(),
					},
				},
			},
			expectedResult: core.PodScheduleResult{
				NominatedNode: &framework.NominatedNode{
					NodeName: "n6",
					VictimPods: framework.VictimPods{
						{
							Name:      "p6",
							Namespace: "p6",
							UID:       "p6",
						},
					},
				},
				Victims: &framework.Victims{
					Pods: []*v1.Pod{
						testinghelper.MakePod().Namespace("p6").Name("p6").UID("p6").Node("n6").
							Priority(5).PriorityClassName("pc").
							Req(map[v1.ResourceName]string{"cpu": "10"}).
							Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).Obj(),
					},
					PreemptionState: framework.NewCycleState(),
				},
			},
			expectedPodCount:          0,
			expectedCachedUsedNode:    nil,
			expectedCachedUnusedNodes: nil,
		},
		{
			name: "in cache used, no victims, cached pod count is 2",
			existingPods: []*v1.Pod{
				testinghelper.MakePod().Namespace("p1").Name("p1").UID("p1").Node("n1").
					Priority(10).PriorityClassName("pc").
					Req(map[v1.ResourceName]string{"cpu": "10"}).
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).Obj(),
				testinghelper.MakePod().Namespace("p2").Name("p2").UID("p2").Node("n2").
					Priority(10).PriorityClassName("pc").
					Req(map[v1.ResourceName]string{"cpu": "10"}).
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).Obj(),
				testinghelper.MakePod().Namespace("p3").Name("p3").UID("p3").Node("n3").
					Priority(10).PriorityClassName("pc").
					Req(map[v1.ResourceName]string{"cpu": "10"}).
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).Obj(),
				testinghelper.MakePod().Namespace("p4").Name("p4").UID("p4").Node("n4").
					Priority(10).PriorityClassName("pc").
					Req(map[v1.ResourceName]string{"cpu": "10"}).
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).Obj(),
				testinghelper.MakePod().Namespace("p5").Name("p5").UID("p5").Node("n5").
					Priority(10).PriorityClassName("pc").
					Req(map[v1.ResourceName]string{"cpu": "10"}).
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).Obj(),
			},
			podCount: 2,
			cachedUsedNode: &framework.Candidate{
				Name: "n6",
				Victims: &framework.Victims{
					Pods:            []*v1.Pod{},
					PreemptionState: framework.NewCycleState(),
				},
			},
			cachedUnusedNodes: []*framework.Candidate{
				{
					Name: "n5",
					Victims: &framework.Victims{
						Pods: []*v1.Pod{
							testinghelper.MakePod().Namespace("p5").Name("p5").UID("p5").Node("n5").
								Priority(10).PriorityClassName("pc").
								Req(map[v1.ResourceName]string{"cpu": "10"}).
								Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).Obj(),
						},
						PreemptionState: framework.NewCycleState(),
					},
				},
			},
			expectedResult: core.PodScheduleResult{
				NominatedNode: &framework.NominatedNode{
					NodeName: "n6",
				},
				Victims: &framework.Victims{
					PreemptionState: framework.NewCycleState(),
				},
			},
			expectedPodCount: 1,
			expectedCachedUsedNode: &framework.Candidate{
				Name: "n6",
				Victims: &framework.Victims{
					PreemptionState: framework.NewCycleState(),
				},
			},
			expectedCachedUnusedNodes: []*framework.Candidate{
				{
					Name: "n5",
					Victims: &framework.Victims{
						Pods: []*v1.Pod{
							testinghelper.MakePod().Namespace("p5").Name("p5").UID("p5").Node("n5").
								Priority(10).PriorityClassName("pc").
								Req(map[v1.ResourceName]string{"cpu": "10"}).
								Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).Obj(),
						},
						PreemptionState: framework.NewCycleState(),
					},
				},
			},
		},
		{
			name: "in cache used, no victims, cached pod count is 1",
			existingPods: []*v1.Pod{
				testinghelper.MakePod().Namespace("p1").Name("p1").UID("p1").Node("n1").
					Priority(10).PriorityClassName("pc").
					Req(map[v1.ResourceName]string{"cpu": "10"}).
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).Obj(),
				testinghelper.MakePod().Namespace("p2").Name("p2").UID("p2").Node("n2").
					Priority(10).PriorityClassName("pc").
					Req(map[v1.ResourceName]string{"cpu": "10"}).
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).Obj(),
				testinghelper.MakePod().Namespace("p3").Name("p3").UID("p3").Node("n3").
					Priority(10).PriorityClassName("pc").
					Req(map[v1.ResourceName]string{"cpu": "10"}).
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).Obj(),
				testinghelper.MakePod().Namespace("p4").Name("p4").UID("p4").Node("n4").
					Priority(10).PriorityClassName("pc").
					Req(map[v1.ResourceName]string{"cpu": "10"}).
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).Obj(),
				testinghelper.MakePod().Namespace("p5").Name("p5").UID("p5").Node("n5").
					Priority(10).PriorityClassName("pc").
					Req(map[v1.ResourceName]string{"cpu": "10"}).
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).Obj(),
			},
			podCount: 1,
			cachedUsedNode: &framework.Candidate{
				Name: "n6",
			},
			cachedUnusedNodes: []*framework.Candidate{
				{
					Name: "n5",
					Victims: &framework.Victims{
						Pods: []*v1.Pod{
							testinghelper.MakePod().Namespace("p5").Name("p5").UID("p5").Node("n5").
								Priority(10).PriorityClassName("pc").
								Req(map[v1.ResourceName]string{"cpu": "10"}).
								Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).Obj(),
						},
						PreemptionState: framework.NewCycleState(),
					},
				},
			},
			expectedResult: core.PodScheduleResult{
				NominatedNode: &framework.NominatedNode{
					NodeName: "n6",
				},
				Victims: &framework.Victims{
					PreemptionState: framework.NewCycleState(),
				},
			},
			expectedPodCount:          0,
			expectedCachedUsedNode:    nil,
			expectedCachedUnusedNodes: nil,
		},
		{
			name: "in cache unused, succeed, has victims",
			existingPods: []*v1.Pod{
				testinghelper.MakePod().Namespace("p1").Name("p1").UID("p1").Node("n1").
					Priority(10).PriorityClassName("pc").
					Req(map[v1.ResourceName]string{"cpu": "10"}).
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).Obj(),
				testinghelper.MakePod().Namespace("p2").Name("p2").UID("p2").Node("n2").
					Priority(10).PriorityClassName("pc").
					Req(map[v1.ResourceName]string{"cpu": "10"}).
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).Obj(),
				testinghelper.MakePod().Namespace("p3").Name("p3").UID("p3").Node("n3").
					Priority(10).PriorityClassName("pc").
					Req(map[v1.ResourceName]string{"cpu": "10"}).
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).Obj(),
				testinghelper.MakePod().Namespace("p4").Name("p4").UID("p4").Node("n4").
					Priority(10).PriorityClassName("pc").
					Req(map[v1.ResourceName]string{"cpu": "10"}).
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).Obj(),
				testinghelper.MakePod().Namespace("p5").Name("p5").UID("p5").Node("n5").
					Priority(10).PriorityClassName("pc").
					Req(map[v1.ResourceName]string{"cpu": "10"}).
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).Obj(),
				testinghelper.MakePod().Namespace("p6").Name("p6").UID("p6").Node("n6").
					Priority(5).PriorityClassName("pc").
					Req(map[v1.ResourceName]string{"cpu": "10"}).
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).Obj(),
			},
			podCount: 2,
			cachedUsedNode: &framework.Candidate{
				Name: "n5",
				Victims: &framework.Victims{
					Pods: []*v1.Pod{
						testinghelper.MakePod().Namespace("p5").Name("p5").UID("p5").Node("n5").
							Priority(10).PriorityClassName("pc").
							Req(map[v1.ResourceName]string{"cpu": "10"}).
							Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).Obj(),
					},
					PreemptionState: framework.NewCycleState(),
				},
			},
			cachedUnusedNodes: []*framework.Candidate{
				{
					Name: "n6",
					Victims: &framework.Victims{
						Pods: []*v1.Pod{
							testinghelper.MakePod().Namespace("p6").Name("p6").UID("p6").Node("n6").
								Priority(5).PriorityClassName("pc").
								Req(map[v1.ResourceName]string{"cpu": "10"}).
								Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).Obj(),
						},
						PreemptionState: framework.NewCycleState(),
					},
				},
				{
					Name: "n4",
					Victims: &framework.Victims{
						Pods: []*v1.Pod{
							testinghelper.MakePod().Namespace("p4").Name("p4").UID("p4").Node("n4").
								Priority(10).PriorityClassName("pc").
								Req(map[v1.ResourceName]string{"cpu": "10"}).
								Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).Obj(),
						},
						PreemptionState: framework.NewCycleState(),
					},
				},
			},
			expectedResult: core.PodScheduleResult{
				NominatedNode: &framework.NominatedNode{
					NodeName: "n6",
					VictimPods: framework.VictimPods{
						{
							Name:      "p6",
							Namespace: "p6",
							UID:       "p6",
						},
					},
				},
				Victims: &framework.Victims{
					Pods: []*v1.Pod{
						testinghelper.MakePod().Namespace("p6").Name("p6").UID("p6").Node("n6").
							Priority(5).PriorityClassName("pc").
							Req(map[v1.ResourceName]string{"cpu": "10"}).
							Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).Obj(),
					},
					PreemptionState: framework.NewCycleState(),
				},
			},
			expectedPodCount: 1,
			expectedCachedUsedNode: &framework.Candidate{
				Name: "n6",
				Victims: &framework.Victims{
					Pods: []*v1.Pod{
						testinghelper.MakePod().Namespace("p6").Name("p6").UID("p6").Node("n6").
							Priority(5).PriorityClassName("pc").
							Req(map[v1.ResourceName]string{"cpu": "10"}).
							Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).Obj(),
					},
					PreemptionState: framework.NewCycleState(),
				},
			},
			expectedCachedUnusedNodes: []*framework.Candidate{
				{
					Name: "n5",
					Victims: &framework.Victims{
						Pods: []*v1.Pod{
							testinghelper.MakePod().Namespace("p5").Name("p5").UID("p5").Node("n5").
								Priority(10).PriorityClassName("pc").
								Req(map[v1.ResourceName]string{"cpu": "10"}).
								Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).Obj(),
						},
						PreemptionState: framework.NewCycleState(),
					},
				},
			},
		},
		{
			name: "in cache unused, succeed, no victims",
			existingPods: []*v1.Pod{
				testinghelper.MakePod().Namespace("p1").Name("p1").UID("p1").Node("n1").
					Priority(10).PriorityClassName("pc").
					Req(map[v1.ResourceName]string{"cpu": "10"}).
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).Obj(),
				testinghelper.MakePod().Namespace("p2").Name("p2").UID("p2").Node("n2").
					Priority(10).PriorityClassName("pc").
					Req(map[v1.ResourceName]string{"cpu": "10"}).
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).Obj(),
				testinghelper.MakePod().Namespace("p3").Name("p3").UID("p3").Node("n3").
					Priority(10).PriorityClassName("pc").
					Req(map[v1.ResourceName]string{"cpu": "10"}).
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).Obj(),
				testinghelper.MakePod().Namespace("p4").Name("p4").UID("p4").Node("n4").
					Priority(10).PriorityClassName("pc").
					Req(map[v1.ResourceName]string{"cpu": "10"}).
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).Obj(),
				testinghelper.MakePod().Namespace("p5").Name("p5").UID("p5").Node("n5").
					Priority(10).PriorityClassName("pc").
					Req(map[v1.ResourceName]string{"cpu": "10"}).
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).Obj(),
			},
			podCount: 1,
			cachedUsedNode: &framework.Candidate{
				Name: "n5",
				Victims: &framework.Victims{
					Pods: []*v1.Pod{
						testinghelper.MakePod().Namespace("p5").Name("p5").UID("p5").Node("n5").
							Priority(10).PriorityClassName("pc").
							Req(map[v1.ResourceName]string{"cpu": "10"}).
							Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).Obj(),
					},
					PreemptionState: framework.NewCycleState(),
				},
			},
			cachedUnusedNodes: []*framework.Candidate{
				{
					Name: "n6",
				},
			},
			expectedResult: core.PodScheduleResult{
				NominatedNode: &framework.NominatedNode{
					NodeName: "n6",
				},
				Victims: &framework.Victims{
					PreemptionState: framework.NewCycleState(),
				},
			},
			expectedPodCount:          0,
			expectedCachedUsedNode:    nil,
			expectedCachedUnusedNodes: nil,
		},
		{
			name: "in prefer nodes, succeed, has victims",
			existingPods: []*v1.Pod{
				testinghelper.MakePod().Namespace("p1").Name("p1").UID("p1").Node("n1").
					Priority(11).PriorityClassName("pc").
					Req(map[v1.ResourceName]string{"cpu": "10"}).
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).Obj(),
				testinghelper.MakePod().Namespace("p2").Name("p2").UID("p2").Node("n2").
					Priority(10).PriorityClassName("pc").
					Req(map[v1.ResourceName]string{"cpu": "10"}).
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).Obj(),
				testinghelper.MakePod().Namespace("p3").Name("p3").UID("p3").Node("n3").
					Priority(10).PriorityClassName("pc").
					Req(map[v1.ResourceName]string{"cpu": "10"}).
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).Obj(),
				testinghelper.MakePod().Namespace("p4").Name("p4").UID("p4").Node("n4").
					Priority(10).PriorityClassName("pc").
					Req(map[v1.ResourceName]string{"cpu": "10"}).
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).Obj(),
				testinghelper.MakePod().Namespace("p5").Name("p5").UID("p5").Node("n5").
					Priority(10).PriorityClassName("pc").
					Req(map[v1.ResourceName]string{"cpu": "10"}).
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).Obj(),
				testinghelper.MakePod().Namespace("p6").Name("p6").UID("p6").Node("n6").
					Priority(10).PriorityClassName("pc").
					Req(map[v1.ResourceName]string{"cpu": "10"}).
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).Obj(),
			},
			podCount: 2,
			expectedResult: core.PodScheduleResult{
				NominatedNode: &framework.NominatedNode{
					NodeName: "n1",
					VictimPods: framework.VictimPods{
						{
							Name:      "p1",
							Namespace: "p1",
							UID:       "p1",
						},
					},
				},
				Victims: &framework.Victims{
					Pods: []*v1.Pod{
						testinghelper.MakePod().Namespace("p1").Name("p1").UID("p1").Node("n1").
							Priority(11).PriorityClassName("pc").
							Req(map[v1.ResourceName]string{"cpu": "10"}).
							Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).Obj(),
					},
					PreemptionState: framework.NewCycleState(),
				},
			},
			expectedPodCount:          1,
			expectedCachedUsedNode:    nil,
			expectedCachedUnusedNodes: nil,
		},
		{
			name: "in prefer nodes, succeed, no victims",
			existingPods: []*v1.Pod{
				testinghelper.MakePod().Namespace("p2").Name("p2").UID("p2").Node("n2").
					Priority(10).PriorityClassName("pc").
					Req(map[v1.ResourceName]string{"cpu": "10"}).
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).Obj(),
				testinghelper.MakePod().Namespace("p3").Name("p3").UID("p3").Node("n3").
					Priority(10).PriorityClassName("pc").
					Req(map[v1.ResourceName]string{"cpu": "10"}).
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).Obj(),
				testinghelper.MakePod().Namespace("p4").Name("p4").UID("p4").Node("n4").
					Priority(10).PriorityClassName("pc").
					Req(map[v1.ResourceName]string{"cpu": "10"}).
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).Obj(),
				testinghelper.MakePod().Namespace("p5").Name("p5").UID("p5").Node("n5").
					Priority(10).PriorityClassName("pc").
					Req(map[v1.ResourceName]string{"cpu": "10"}).
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).Obj(),
				testinghelper.MakePod().Namespace("p6").Name("p6").UID("p6").Node("n6").
					Priority(10).PriorityClassName("pc").
					Req(map[v1.ResourceName]string{"cpu": "10"}).
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).Obj(),
			},
			podCount: 2,
			expectedResult: core.PodScheduleResult{
				NominatedNode: &framework.NominatedNode{
					NodeName: "n1",
				},
				Victims: &framework.Victims{
					PreemptionState: framework.NewCycleState(),
				},
			},
			expectedPodCount:          1,
			expectedCachedUsedNode:    nil,
			expectedCachedUnusedNodes: nil,
		},
		{
			name: "in first node circle, succeed, has victims",
			existingPods: []*v1.Pod{
				testinghelper.MakePod().Namespace("p1").Name("p1").UID("p1").Node("n1").
					Priority(110).PriorityClassName("pc").
					Req(map[v1.ResourceName]string{"cpu": "10"}).
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).Obj(),
				testinghelper.MakePod().Namespace("p2").Name("p2").UID("p2").Node("n2").
					Priority(110).PriorityClassName("pc").
					Req(map[v1.ResourceName]string{"cpu": "10"}).
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).Obj(),
				testinghelper.MakePod().Namespace("p3").Name("p3").UID("p3").Node("n3").
					Priority(20).PriorityClassName("pc").
					Req(map[v1.ResourceName]string{"cpu": "10"}).
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).Obj(),
				testinghelper.MakePod().Namespace("p4").Name("p4").UID("p4").Node("n4").
					Priority(10).PriorityClassName("pc").
					Req(map[v1.ResourceName]string{"cpu": "10"}).
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).Obj(),
				testinghelper.MakePod().Namespace("p5").Name("p5").UID("p5").Node("n5").
					Priority(10).PriorityClassName("pc").
					Req(map[v1.ResourceName]string{"cpu": "10"}).
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).Obj(),
				testinghelper.MakePod().Namespace("p6").Name("p6").UID("p6").Node("n6").
					Priority(5).PriorityClassName("pc").
					Req(map[v1.ResourceName]string{"cpu": "10"}).
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).Obj(),
			},
			podCount: 2,
			expectedResult: core.PodScheduleResult{
				NominatedNode: &framework.NominatedNode{
					NodeName: "n4",
					VictimPods: framework.VictimPods{
						{
							Name:      "p4",
							Namespace: "p4",
							UID:       "p4",
						},
					},
				},
				Victims: &framework.Victims{
					Pods: []*v1.Pod{
						testinghelper.MakePod().Namespace("p4").Name("p4").UID("p4").Node("n4").
							Priority(10).PriorityClassName("pc").
							Req(map[v1.ResourceName]string{"cpu": "10"}).
							Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).Obj(),
					},
					PreemptionState: framework.NewCycleState(),
				},
			},
			expectedPodCount: 1,
			expectedCachedUsedNode: &framework.Candidate{
				Name: "n4",
				Victims: &framework.Victims{
					Pods: []*v1.Pod{
						testinghelper.MakePod().Namespace("p4").Name("p4").UID("p4").Node("n4").
							Priority(10).PriorityClassName("pc").
							Req(map[v1.ResourceName]string{"cpu": "10"}).
							Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).Obj(),
					},
					PreemptionState: framework.NewCycleState(),
				},
			},
			expectedCachedUnusedNodes: []*framework.Candidate{
				{
					Name: "n3",
					Victims: &framework.Victims{
						Pods: []*v1.Pod{
							testinghelper.MakePod().Namespace("p3").Name("p3").UID("p3").Node("n3").
								Priority(20).PriorityClassName("pc").
								Req(map[v1.ResourceName]string{"cpu": "10"}).
								Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).Obj(),
						},
						PreemptionState: framework.NewCycleState(),
					},
				},
			},
		},
		{
			name: "in first node circle, succeed, has victims, has cross nodes constraints",
			existingPods: []*v1.Pod{
				testinghelper.MakePod().Namespace("p1").Name("p1").UID("p1").Node("n1").
					Priority(110).PriorityClassName("pc").
					Req(map[v1.ResourceName]string{"cpu": "10"}).
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).Obj(),
				testinghelper.MakePod().Namespace("p2").Name("p2").UID("p2").Node("n2").
					Priority(110).PriorityClassName("pc").
					Req(map[v1.ResourceName]string{"cpu": "10"}).
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).Obj(),
				testinghelper.MakePod().Namespace("p3").Name("p3").UID("p3").Node("n3").
					Priority(20).PriorityClassName("pc").
					Req(map[v1.ResourceName]string{"cpu": "10"}).
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).Obj(),
				testinghelper.MakePod().Namespace("p4").Name("p4").UID("p4").Node("n4").
					Priority(10).PriorityClassName("pc").
					Req(map[v1.ResourceName]string{"cpu": "10"}).
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).Obj(),
				testinghelper.MakePod().Namespace("p5").Name("p5").UID("p5").Node("n5").
					Priority(10).PriorityClassName("pc").
					Req(map[v1.ResourceName]string{"cpu": "10"}).
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).Obj(),
				testinghelper.MakePod().Namespace("p6").Name("p6").UID("p6").Node("n6").
					Priority(5).PriorityClassName("pc").
					Req(map[v1.ResourceName]string{"cpu": "10"}).
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).Obj(),
			},
			podCount:              2,
			crossNodesConstraints: true,
			expectedResult: core.PodScheduleResult{
				NominatedNode: &framework.NominatedNode{
					NodeName: "n4",
					VictimPods: framework.VictimPods{
						{
							Name:      "p4",
							Namespace: "p4",
							UID:       "p4",
						},
					},
				},
				Victims: &framework.Victims{
					Pods: []*v1.Pod{
						testinghelper.MakePod().Namespace("p4").Name("p4").UID("p4").Node("n4").
							Priority(10).PriorityClassName("pc").
							Req(map[v1.ResourceName]string{"cpu": "10"}).
							Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).Obj(),
					},
					PreemptionState: framework.NewCycleState(),
				},
			},
			expectedPodCount:          0,
			expectedCachedUsedNode:    nil,
			expectedCachedUnusedNodes: nil,
		},
		{
			name: "in first node circle, succeed, no victims",
			existingPods: []*v1.Pod{
				testinghelper.MakePod().Namespace("p1").Name("p1").UID("p1").Node("n1").
					Priority(110).PriorityClassName("pc").
					Req(map[v1.ResourceName]string{"cpu": "10"}).
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).Obj(),
				testinghelper.MakePod().Namespace("p2").Name("p2").UID("p2").Node("n2").
					Priority(110).PriorityClassName("pc").
					Req(map[v1.ResourceName]string{"cpu": "10"}).
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).Obj(),
				testinghelper.MakePod().Namespace("p3").Name("p3").UID("p3").Node("n3").
					Priority(20).PriorityClassName("pc").
					Req(map[v1.ResourceName]string{"cpu": "10"}).
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).Obj(),
				testinghelper.MakePod().Namespace("p5").Name("p5").UID("p5").Node("n5").
					Priority(10).PriorityClassName("pc").
					Req(map[v1.ResourceName]string{"cpu": "10"}).
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).Obj(),
				testinghelper.MakePod().Namespace("p6").Name("p6").UID("p6").Node("n6").
					Priority(5).PriorityClassName("pc").
					Req(map[v1.ResourceName]string{"cpu": "10"}).
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).Obj(),
			},
			podCount: 2,
			expectedResult: core.PodScheduleResult{
				NominatedNode: &framework.NominatedNode{
					NodeName: "n4",
				},
				Victims: &framework.Victims{
					PreemptionState: framework.NewCycleState(),
				},
			},
			expectedPodCount: 1,
			expectedCachedUsedNode: &framework.Candidate{
				Name: "n4",
				Victims: &framework.Victims{
					PreemptionState: framework.NewCycleState(),
				},
			},
			expectedCachedUnusedNodes: []*framework.Candidate{
				{
					Name: "n3",
					Victims: &framework.Victims{
						Pods: []*v1.Pod{
							testinghelper.MakePod().Namespace("p3").Name("p3").UID("p3").Node("n3").
								Priority(20).PriorityClassName("pc").
								Req(map[v1.ResourceName]string{"cpu": "10"}).
								Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).Obj(),
						},
						PreemptionState: framework.NewCycleState(),
					},
				},
			},
		},
		{
			name: "in second node circle, succeed, has victims",
			existingPods: []*v1.Pod{
				testinghelper.MakePod().Namespace("p1").Name("p1").UID("p1").Node("n1").
					Priority(110).PriorityClassName("pc").
					Req(map[v1.ResourceName]string{"cpu": "10"}).
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).Obj(),
				testinghelper.MakePod().Namespace("p2").Name("p2").UID("p2").Node("n2").
					Priority(110).PriorityClassName("pc").
					Req(map[v1.ResourceName]string{"cpu": "10"}).
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).Obj(),
				testinghelper.MakePod().Namespace("p3").Name("p3").UID("p3").Node("n3").
					Priority(110).PriorityClassName("pc").
					Req(map[v1.ResourceName]string{"cpu": "10"}).
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).Obj(),
				testinghelper.MakePod().Namespace("p4").Name("p4").UID("p4").Node("n4").
					Priority(110).PriorityClassName("pc").
					Req(map[v1.ResourceName]string{"cpu": "10"}).
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).Obj(),
				testinghelper.MakePod().Namespace("p5").Name("p5").UID("p5").Node("n5").
					Priority(10).PriorityClassName("pc").
					Req(map[v1.ResourceName]string{"cpu": "10"}).
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).Obj(),
				testinghelper.MakePod().Namespace("p6").Name("p6").UID("p6").Node("n6").
					Priority(5).PriorityClassName("pc").
					Req(map[v1.ResourceName]string{"cpu": "10"}).
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).Obj(),
			},
			podCount: 2,
			expectedResult: core.PodScheduleResult{
				NominatedNode: &framework.NominatedNode{
					NodeName: "n6",
					VictimPods: framework.VictimPods{
						{
							Name:      "p6",
							Namespace: "p6",
							UID:       "p6",
						},
					},
				},
				Victims: &framework.Victims{
					Pods: []*v1.Pod{
						testinghelper.MakePod().Namespace("p6").Name("p6").UID("p6").Node("n6").
							Priority(5).PriorityClassName("pc").
							Req(map[v1.ResourceName]string{"cpu": "10"}).
							Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).Obj(),
					},
					PreemptionState: framework.NewCycleState(),
				},
			},
			expectedPodCount: 1,
			expectedCachedUsedNode: &framework.Candidate{
				Name: "n6",
				Victims: &framework.Victims{
					Pods: []*v1.Pod{
						testinghelper.MakePod().Namespace("p6").Name("p6").UID("p6").Node("n6").
							Priority(5).PriorityClassName("pc").
							Req(map[v1.ResourceName]string{"cpu": "10"}).
							Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).Obj(),
					},
					PreemptionState: framework.NewCycleState(),
				},
			},
			expectedCachedUnusedNodes: []*framework.Candidate{
				{
					Name: "n5",
					Victims: &framework.Victims{
						Pods: []*v1.Pod{
							testinghelper.MakePod().Namespace("p5").Name("p5").UID("p5").Node("n5").
								Priority(10).PriorityClassName("pc").
								Req(map[v1.ResourceName]string{"cpu": "10"}).
								Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).Obj(),
						},
						PreemptionState: framework.NewCycleState(),
					},
				},
			},
		},
		{
			name: "in second node circle, succeed, no victims",
			existingPods: []*v1.Pod{
				testinghelper.MakePod().Namespace("p1").Name("p1").UID("p1").Node("n1").
					Priority(110).PriorityClassName("pc").
					Req(map[v1.ResourceName]string{"cpu": "10"}).
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).Obj(),
				testinghelper.MakePod().Namespace("p2").Name("p2").UID("p2").Node("n2").
					Priority(110).PriorityClassName("pc").
					Req(map[v1.ResourceName]string{"cpu": "10"}).
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).Obj(),
				testinghelper.MakePod().Namespace("p3").Name("p3").UID("p3").Node("n3").
					Priority(110).PriorityClassName("pc").
					Req(map[v1.ResourceName]string{"cpu": "10"}).
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).Obj(),
				testinghelper.MakePod().Namespace("p4").Name("p4").UID("p4").Node("n4").
					Priority(110).PriorityClassName("pc").
					Req(map[v1.ResourceName]string{"cpu": "10"}).
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).Obj(),
				testinghelper.MakePod().Namespace("p6").Name("p6").UID("p6").Node("n6").
					Priority(5).PriorityClassName("pc").
					Req(map[v1.ResourceName]string{"cpu": "10"}).
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).Obj(),
			},
			podCount: 2,
			expectedResult: core.PodScheduleResult{
				NominatedNode: &framework.NominatedNode{
					NodeName: "n5",
				},
				Victims: &framework.Victims{
					PreemptionState: framework.NewCycleState(),
				},
			},
			expectedPodCount: 1,
			expectedCachedUsedNode: &framework.Candidate{
				Name: "n5",
				Victims: &framework.Victims{
					PreemptionState: framework.NewCycleState(),
				},
			},
			expectedCachedUnusedNodes: []*framework.Candidate{
				{
					Name: "n6",
					Victims: &framework.Victims{
						Pods: []*v1.Pod{
							testinghelper.MakePod().Namespace("p6").Name("p6").UID("p6").Node("n6").
								Priority(5).PriorityClassName("pc").
								Req(map[v1.ResourceName]string{"cpu": "10"}).
								Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).Obj(),
						},
						PreemptionState: framework.NewCycleState(),
					},
				},
			},
		},
		{
			name: "fail",
			existingPods: []*v1.Pod{
				testinghelper.MakePod().Namespace("p1").Name("p1").UID("p1").Node("n1").
					Priority(110).PriorityClassName("pc").
					Req(map[v1.ResourceName]string{"cpu": "10"}).
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).Obj(),
				testinghelper.MakePod().Namespace("p2").Name("p2").UID("p2").Node("n2").
					Priority(110).PriorityClassName("pc").
					Req(map[v1.ResourceName]string{"cpu": "10"}).
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).Obj(),
				testinghelper.MakePod().Namespace("p3").Name("p3").UID("p3").Node("n3").
					Priority(110).PriorityClassName("pc").
					Req(map[v1.ResourceName]string{"cpu": "10"}).
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).Obj(),
				testinghelper.MakePod().Namespace("p4").Name("p4").UID("p4").Node("n4").
					Priority(110).PriorityClassName("pc").
					Req(map[v1.ResourceName]string{"cpu": "10"}).
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).Obj(),
				testinghelper.MakePod().Namespace("p5").Name("p5").UID("p5").Node("n5").
					Priority(110).PriorityClassName("pc").
					Req(map[v1.ResourceName]string{"cpu": "10"}).
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).Obj(),
				testinghelper.MakePod().Namespace("p6").Name("p6").UID("p6").Node("n6").
					Priority(110).PriorityClassName("pc").
					Req(map[v1.ResourceName]string{"cpu": "10"}).
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).Obj(),
			},
			podCount:                  2,
			expectedResult:            core.PodScheduleResult{},
			expectedPodCount:          2,
			expectedCachedUsedNode:    nil,
			expectedCachedUnusedNodes: nil,
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

			nodesInPreferNodes := []*v1.Node{
				testinghelper.MakeNode().Name("n1").Capacity(map[v1.ResourceName]string{"cpu": "10"}).Obj(),
				testinghelper.MakeNode().Name("n2").Capacity(map[v1.ResourceName]string{"cpu": "10"}).Obj(),
			}
			nodesInNodeCircles := [][]*v1.Node{
				{
					testinghelper.MakeNode().Name("n3").Capacity(map[v1.ResourceName]string{"cpu": "10"}).Obj(),
					testinghelper.MakeNode().Name("n4").Capacity(map[v1.ResourceName]string{"cpu": "10"}).Obj(),
				},
				{
					testinghelper.MakeNode().Name("n5").Capacity(map[v1.ResourceName]string{"cpu": "10"}).Obj(),
					testinghelper.MakeNode().Name("n6").Capacity(map[v1.ResourceName]string{"cpu": "10"}).Obj(),
				},
			}
			for _, node := range nodesInPreferNodes {
				schedulerCache.AddNode(node)
			}
			for _, nodeCircle := range nodesInNodeCircles {
				for _, node := range nodeCircle {
					schedulerCache.AddNode(node)
				}
			}
			for _, pod := range tt.existingPods {
				schedulerCache.AddPod(pod)
			}
			schedulerCache.UpdateSnapshot(snapshot)

			preemptor := testinghelper.MakePod().Namespace("foo").Name("foo").UID("foo").Priority(100).Req(map[v1.ResourceName]string{"cpu": "10"}).Obj()
			client := clientsetfake.NewSimpleClientset()
			crdClient := godelclientfake.NewSimpleClientset()
			informerFactory := informers.NewSharedInformerFactory(client, 0)
			crdInformerFactory := crdinformers.NewSharedInformerFactory(crdClient, 0)
			podLister := testinghelper.NewFakePodLister([]*v1.Pod{preemptor})

			gs := &podScheduler{
				clientSet:             client,
				crdClient:             crdClient,
				informerFactory:       informerFactory,
				crdInformerFactory:    crdInformerFactory,
				basePlugins:           newBasePlugins(),
				isolatedCache:         isolationCache,
				snapshot:              snapshot,
				podLister:             podLister,
				candidateSelectPolicy: config.CandidateSelectPolicyBetter,
				betterSelectPolicies:  []string{config.BetterPreemptionPolicyAscending},
			}
			gs.betterSelectPoliciesRegistry = map[string]betterSelectPolicy{
				config.BetterPreemptionPolicyAscending: gs.ascendingOrderPreemption,
				config.BetterPreemptionPolicyDichotomy: gs.dichotomyPreemption,
			}
			registry := schedulerframework.NewInTreeRegistry()
			pluginRegistry, err := schedulerframework.NewPluginsRegistry(registry, nil, gs)
			if err != nil {
				t.Errorf("failed to new plugins registry: %v", err)
			}
			f, err := frameworkruntime.NewPodFramework(pluginRegistry, nil, gs.getBasePluginsForPod(preemptor), &framework.PluginCollection{}, &framework.PluginCollection{}, gs.metricsRecorder)
			preemptionRegistry := schedulerframework.NewInTreePreemptionRegistry()
			preemptionPluginRegistry, err := schedulerframework.NewPluginsRegistry(preemptionRegistry, nil, gs)
			if err != nil {
				t.Errorf("failed to new plugins registry: %v", err)
			}
			pf := frameworkruntime.NewPreemptionFramework(preemptionPluginRegistry, gs.getBasePluginsForPod(preemptor))
			nodeGroup := snapshot.MakeBasicNodeGroup()
			preferNodes := framework.NewPreferredNodes()
			for _, node := range nodesInPreferNodes {
				nodeInfo := snapshot.GetNodeInfo(node.Name)
				preferNodes.Add(nodeInfo)
			}
			nodeGroup.SetPreferredNodes(preferNodes)

			var nodeCircles []framework.NodeCircle
			for i, nodesInCircle := range nodesInNodeCircles {
				lister := framework.NewNodeInfoLister().(*framework.NodeInfoListerImpl)
				for _, node := range nodesInCircle {
					nodeInfo := snapshot.GetNodeInfo(node.Name)
					lister.AddNodeInfo(nodeInfo)
				}
				nodeCircle := framework.NewNodeCircle(fmt.Sprintf("%d", i), lister)
				nodeCircles = append(nodeCircles, nodeCircle)
			}
			nodeGroup.SetNodeCircles(nodeCircles)

			state := framework.NewCycleState()
			framework.SetPodResourceTypeState(podutil.GuaranteedPod, state)
			framework.SetPodTrace(&tracing.NoopSchedulingTrace{}, state)

			cachedNominatedNodes := &framework.CachedNominatedNodes{}
			cachedNominatedNodes.SetPodCount(tt.podCount)
			cachedNominatedNodes.SetUnusedNominatedNodes(tt.cachedUnusedNodes)
			cachedNominatedNodes.SetUsedNominatedNode(tt.cachedUsedNode)
			cachedNominatedNodes.SetHasCrossNodesConstraints(tt.crossNodesConstraints)
			gotResult, _ := gs.PreemptInSpecificNodeGroup(context.Background(), f, pf, framework.NewCycleState(), framework.NewCycleState(), state, preemptor, nodeGroup, nil, cachedNominatedNodes)
			gotResult.FilteredNodesStatuses = nil
			if !reflect.DeepEqual(tt.expectedResult, gotResult) {
				t.Errorf("expected result: %v, but got: %v", tt.expectedResult, gotResult)
			}

			if !reflect.DeepEqual(tt.expectedCachedUnusedNodes, cachedNominatedNodes.GetUnusedNominatedNodes()) {
				t.Errorf("expected %v but got %v", tt.expectedCachedUnusedNodes, cachedNominatedNodes.GetUnusedNominatedNodes())
			}

			if !reflect.DeepEqual(tt.expectedCachedUsedNode, cachedNominatedNodes.GetUsedNominatedNode()) {
				t.Errorf("expected %v but got %v", tt.expectedCachedUsedNode, cachedNominatedNodes.GetUsedNominatedNode())
			}

			if tt.expectedPodCount != cachedNominatedNodes.GetPodCount() {
				t.Errorf("expected %d but got %d", tt.expectedPodCount, cachedNominatedNodes.GetPodCount())
			}
		})
	}
}
