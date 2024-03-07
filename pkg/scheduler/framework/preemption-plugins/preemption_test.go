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

package preemptionplugins

import (
	"math"
	"testing"
	"time"

	schedulingv1a1 "github.com/kubewharf/godel-scheduler-api/pkg/apis/scheduling/v1alpha1"
	godelclientfake "github.com/kubewharf/godel-scheduler-api/pkg/client/clientset/versioned/fake"
	crdinformers "github.com/kubewharf/godel-scheduler-api/pkg/client/informers/externalversions"
	schedulingv1a1listers "github.com/kubewharf/godel-scheduler-api/pkg/client/listers/scheduling/v1alpha1"
	v1 "k8s.io/api/core/v1"
	policy "k8s.io/api/policy/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/client-go/informers"
	clientsetfake "k8s.io/client-go/kubernetes/fake"
	appv1listers "k8s.io/client-go/listers/apps/v1"
	schedulingv1beta1listers "k8s.io/client-go/listers/scheduling/v1beta1"

	framework "github.com/kubewharf/godel-scheduler/pkg/framework/api"
	godelcache "github.com/kubewharf/godel-scheduler/pkg/scheduler/cache"
	"github.com/kubewharf/godel-scheduler/pkg/scheduler/cache/handler"
	st "github.com/kubewharf/godel-scheduler/pkg/scheduler/testing"
	testinghelper "github.com/kubewharf/godel-scheduler/pkg/testing-helper"
	"github.com/kubewharf/godel-scheduler/pkg/util"
	podutil "github.com/kubewharf/godel-scheduler/pkg/util/pod"
	"github.com/kubewharf/godel-scheduler/test/utils"
)

var (
	reservationTTL                         = 30 * time.Second
	lowPriority, midPriority, highPriority = int32(0), int32(100), int32(1000)

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

	epochTime = metav1.NewTime(time.Unix(0, 0))
)

func newPriorityPodWithStartTime(name string, priority int32, startTime time.Time) *v1.Pod {
	return &v1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
		},
		Spec: v1.PodSpec{
			Priority: &priority,
		},
		Status: v1.PodStatus{
			StartTime: &metav1.Time{Time: startTime},
		},
	}
}

func TestFilterVictimsPods(t *testing.T) {
	tests := []struct {
		name                     string
		nodeName                 string
		pod                      *v1.Pod
		pods                     []*v1.Pod
		podsInCache              sets.String
		podsToBePreempted        sets.String
		pdbs                     []*policy.PodDisruptionBudget
		pcLister                 schedulingv1beta1listers.PriorityClassLister
		deployLister             appv1listers.DeploymentLister
		pgLister                 schedulingv1a1listers.PodGroupLister
		expectedPotentialVictims sets.String
		expectedPodsViolatingPod sets.String
	}{
		{
			name:     "p1 is not in cache",
			nodeName: "node1",
			pod: testinghelper.MakePod().Namespace("default").Name("p").UID("p").Priority(highPriority).Req(largeRes).Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).
				Annotation(podutil.PodLauncherAnnotationKey, string(podutil.Kubelet)).Obj(),
			pods: []*v1.Pod{
				testinghelper.MakePod().Namespace("default").Name("p1").UID("p1").Node("node1").Priority(lowPriority).Req(largeRes).StartTime(epochTime).PriorityClassName("pc").
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).Annotation(podutil.PodLauncherAnnotationKey, string(podutil.Kubelet)).
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).Obj(),
				testinghelper.MakePod().Namespace("default").Name("p2").UID("p2").Node("node1").Priority(lowPriority).Req(largeRes).StartTime(epochTime).PriorityClassName("pc").
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).Annotation(podutil.PodLauncherAnnotationKey, string(podutil.Kubelet)).
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).Obj(),
			},
			podsInCache:              sets.NewString("default/p2/p2"),
			podsToBePreempted:        sets.NewString("default/p1/p1", "default/p2/p2"),
			pgLister:                 testinghelper.NewFakePodGroupLister([]*schedulingv1a1.PodGroup{}),
			expectedPotentialVictims: sets.NewString("default/p1/p1", "default/p2/p2"),
		},
		{
			name:     "could not preempt because victim is already preempted by other pods",
			nodeName: "node1",
			pod: testinghelper.MakePod().Namespace("default").Name("p").UID("p").Priority(highPriority).Req(largeRes).Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).
				Annotation(podutil.PodLauncherAnnotationKey, string(podutil.Kubelet)).Obj(),
			pods: []*v1.Pod{
				testinghelper.MakePod().Namespace("default").Name("p1").UID("p1").Node("node1").Priority(lowPriority).Req(largeRes).StartTime(epochTime).PriorityClassName("pc").
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).Annotation(podutil.PodLauncherAnnotationKey, string(podutil.Kubelet)).
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).Obj(),
				testinghelper.MakePod().Namespace("default").Name("p11").UID("p11").Node("node1").Priority(lowPriority).StartTime(epochTime).PriorityClassName("pc").
					Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).Annotation(podutil.PodLauncherAnnotationKey, string(podutil.Kubelet)).
					Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
					Annotation(podutil.NominatedNodeAnnotationKey, "{\"node\":\"node1\",\"victims\":[{\"name\":\"p1\",\"namespace\":\"default\",\"uid\":\"p1\"}]}").Obj(),
			},
			podsInCache:              sets.NewString("default/p11/p11"),
			podsToBePreempted:        sets.NewString("default/p1/p1"),
			pgLister:                 testinghelper.NewFakePodGroupLister([]*schedulingv1a1.PodGroup{}),
			expectedPotentialVictims: sets.NewString(),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			node := testinghelper.MakeNode().Name(tt.nodeName).Capacity(veryLargeRes).Obj()
			var pods []*v1.Pod
			for _, pod := range tt.pods {
				podKey := podutil.GeneratePodKey(pod)
				if !tt.podsToBePreempted.Has(podKey) {
					continue
				}
				pods = append(pods, pod)
			}
			podLister := testinghelper.NewFakePodLister(tt.pods)
			nodeInfo := framework.NewNodeInfo(pods...)
			nodeInfo.SetNode(node)

			cache := godelcache.New(handler.MakeCacheHandlerWrapper().
				SchedulerName("").SchedulerType("").SubCluster(framework.DefaultSubCluster).
				TTL(time.Second).Period(10 * time.Second).StopCh(make(<-chan struct{})).
				EnableStore("PreemptionStore").
				PodLister(podLister).
				Obj())
			for _, pod := range tt.pods {
				podKey := podutil.GeneratePodKey(pod)
				if !tt.podsInCache.Has(podKey) {
					continue
				}
				cache.AddPod(pod)
			}
			for _, pdb := range tt.pdbs {
				cache.AddPDB(pdb)
			}

			crdClient := godelclientfake.NewSimpleClientset()
			crdInformerFactory := crdinformers.NewSharedInformerFactory(crdClient, 0)
			client := clientsetfake.NewSimpleClientset()
			informerFactory := informers.NewSharedInformerFactory(client, 0)
			stop := make(chan struct{})
			crdInformerFactory.Start(stop)
			crdInformerFactory.WaitForCacheSync(stop)
			informerFactory.Start(stop)
			informerFactory.WaitForCacheSync(stop)

			if tt.pcLister != nil {
				pcInformer := informerFactory.Scheduling().V1().PriorityClasses().Informer()
				pcList, err := tt.pcLister.List(labels.Everything())
				if err != nil {
					t.Fatal(err)
				}
				for _, pc := range pcList {
					pcInformer.GetIndexer().Add(pc)
				}
			}
			if tt.deployLister != nil {
				deployInformer := informerFactory.Apps().V1().Deployments().Informer()
				deployList, err := tt.deployLister.List(labels.Everything())
				if err != nil {
					t.Fatal(err)
				}
				for _, deploy := range deployList {
					deployInformer.GetIndexer().Add(deploy)
				}
			}
			if tt.pgLister != nil {
				pgInformer := crdInformerFactory.Scheduling().V1alpha1().PodGroups().Informer()
				pgList, err := tt.pgLister.List(labels.Everything())
				if err != nil {
					t.Fatal(err)
				}

				for _, pg := range pgList {
					pgInformer.GetIndexer().Add(pg)
				}
			}

			snapshot := godelcache.NewEmptySnapshot(handler.MakeCacheHandlerWrapper().
				SubCluster(framework.DefaultSubCluster).SwitchType(framework.DefaultSubClusterSwitchType).
				EnableStore("PreemptionStore").
				Obj())
			cache.UpdateSnapshot(snapshot)

			fh, _ := st.NewSchedulerFrameworkHandle(client, crdClient, informerFactory, crdInformerFactory, cache, snapshot, nil, nil, nil, nil)
			basePlugins, preemptionPluginRegistry, err := utils.GetPreemptionRelatedPlugins(fh)
			if err != nil {
				t.Errorf("failed to get preemption plugins: %v", err)
			}
			fh, _ = st.NewSchedulerFrameworkHandle(client, crdClient, informerFactory, crdInformerFactory, cache, snapshot, nil, preemptionPluginRegistry, nil, basePlugins)

			fwk, _ := fh.GetFrameworkForPod(tt.pod)
			state, err := fwk.InitCycleState(tt.pod)
			if err != nil {
				t.Fatal(err)
			}
			commonState := framework.NewCycleState()
			pfwk := fh.GetPreemptionFrameworkForPod(tt.pod)
			preemptionState := framework.NewCycleState()
			pfwk.RunClusterPrePreemptingPlugins(tt.pod, state, commonState)
			priority := GetPodPartitionPriority(tt.pod)
			potentialVictims := FilterVictimsPods(fh, pfwk, state, preemptionState, nodeInfo, tt.pod, math.MinInt64, priority, false)
			potentialVictimsSet := sets.NewString()
			for _, potentialVictim := range potentialVictims {
				potentialVictimsSet.Insert(podutil.GeneratePodKey(potentialVictim))
			}
			if !potentialVictimsSet.Equal(tt.expectedPotentialVictims) {
				t.Errorf("expected to get potentialVictims: %v, but got: %v", tt.expectedPotentialVictims, potentialVictimsSet)
			}
		})
	}
}
