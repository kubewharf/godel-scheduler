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

package dispatcher

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/kubewharf/godel-scheduler/pkg/features"
	testing_helper "github.com/kubewharf/godel-scheduler/pkg/testing-helper"
	podutil "github.com/kubewharf/godel-scheduler/pkg/util/pod"

	schedulingv1a1 "github.com/kubewharf/godel-scheduler-api/pkg/apis/scheduling/v1alpha1"
	godelclientfake "github.com/kubewharf/godel-scheduler-api/pkg/client/clientset/versioned/fake"
	crdinformers "github.com/kubewharf/godel-scheduler-api/pkg/client/informers/externalversions"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/sets"
	utilfeature "k8s.io/apiserver/pkg/util/feature"
	"k8s.io/client-go/informers"
	clientsetfake "k8s.io/client-go/kubernetes/fake"
	"k8s.io/client-go/tools/cache"
	"k8s.io/klog/v2"
)

const (
	schedulerName = "godel-scheduler"
)

func TestSelectSchedulerAccordingToOwner(t *testing.T) {
	timeNow := metav1.NewTime(time.Now())
	scheduler0 := &schedulingv1a1.Scheduler{
		ObjectMeta: metav1.ObjectMeta{
			Name: "godel-scheduler-0",
		},
		Status: schedulingv1a1.SchedulerStatus{
			LastUpdateTime: &timeNow,
		},
	}
	scheduler1 := &schedulingv1a1.Scheduler{
		ObjectMeta: metav1.ObjectMeta{
			Name: "godel-scheduler-1",
		},
		Status: schedulingv1a1.SchedulerStatus{
			LastUpdateTime: &timeNow,
		},
	}
	scheduler2 := &schedulingv1a1.Scheduler{
		ObjectMeta: metav1.ObjectMeta{
			Name: "godel-scheduler-2",
		},
		Status: schedulingv1a1.SchedulerStatus{
			LastUpdateTime: &timeNow,
		},
	}

	tests := []struct {
		name                   string
		pods                   []*v1.Pod
		expectedSameSchedulers [][]string
	}{
		{
			name: "all pods have the same owner",
			pods: []*v1.Pod{
				testing_helper.MakePod().Namespace("default").Name("p0").UID("p0").
					ControllerRef(metav1.OwnerReference{Kind: podutil.ReplicaSetKind, Name: "rs1", UID: "rs1"}).Obj(),
				testing_helper.MakePod().Namespace("default").Name("p1").UID("p1").
					ControllerRef(metav1.OwnerReference{Kind: podutil.ReplicaSetKind, Name: "rs1", UID: "rs1"}).Obj(),
				testing_helper.MakePod().Namespace("default").Name("p2").UID("p2").
					ControllerRef(metav1.OwnerReference{Kind: podutil.ReplicaSetKind, Name: "rs1", UID: "rs1"}).Obj(),
				testing_helper.MakePod().Namespace("default").Name("p3").UID("p3").
					ControllerRef(metav1.OwnerReference{Kind: podutil.ReplicaSetKind, Name: "rs1", UID: "rs1"}).Obj(),
				testing_helper.MakePod().Namespace("default").Name("p4").UID("p4").
					ControllerRef(metav1.OwnerReference{Kind: podutil.ReplicaSetKind, Name: "rs1", UID: "rs1"}).Obj(),
				testing_helper.MakePod().Namespace("default").Name("p5").UID("p5").
					ControllerRef(metav1.OwnerReference{Kind: podutil.ReplicaSetKind, Name: "rs1", UID: "rs1"}).Obj(),
				testing_helper.MakePod().Namespace("default").Name("p6").UID("p6").
					ControllerRef(metav1.OwnerReference{Kind: podutil.ReplicaSetKind, Name: "rs1", UID: "rs1"}).Obj(),
				testing_helper.MakePod().Namespace("default").Name("p7").UID("p7").
					ControllerRef(metav1.OwnerReference{Kind: podutil.ReplicaSetKind, Name: "rs1", UID: "rs1"}).Obj(),
				testing_helper.MakePod().Namespace("default").Name("p8").UID("p8").
					ControllerRef(metav1.OwnerReference{Kind: podutil.ReplicaSetKind, Name: "rs1", UID: "rs1"}).Obj(),
				testing_helper.MakePod().Namespace("default").Name("p9").UID("p9").
					ControllerRef(metav1.OwnerReference{Kind: podutil.ReplicaSetKind, Name: "rs1", UID: "rs1"}).Obj(),
				testing_helper.MakePod().Namespace("default").Name("p10").UID("p10").
					ControllerRef(metav1.OwnerReference{Kind: podutil.ReplicaSetKind, Name: "rs1", UID: "rs1"}).Obj(),
				testing_helper.MakePod().Namespace("default").Name("p11").UID("p11").
					ControllerRef(metav1.OwnerReference{Kind: podutil.ReplicaSetKind, Name: "rs1", UID: "rs1"}).Obj(),
				testing_helper.MakePod().Namespace("default").Name("p12").UID("p12").
					ControllerRef(metav1.OwnerReference{Kind: podutil.ReplicaSetKind, Name: "rs1", UID: "rs1"}).Obj(),
				testing_helper.MakePod().Namespace("default").Name("p13").UID("p13").
					ControllerRef(metav1.OwnerReference{Kind: podutil.ReplicaSetKind, Name: "rs1", UID: "rs1"}).Obj(),
				testing_helper.MakePod().Namespace("default").Name("p14").UID("p14").
					ControllerRef(metav1.OwnerReference{Kind: podutil.ReplicaSetKind, Name: "rs1", UID: "rs1"}).Obj(),
				testing_helper.MakePod().Namespace("default").Name("p15").UID("p15").
					ControllerRef(metav1.OwnerReference{Kind: podutil.ReplicaSetKind, Name: "rs1", UID: "rs1"}).Obj(),
				testing_helper.MakePod().Namespace("default").Name("p16").UID("p16").
					ControllerRef(metav1.OwnerReference{Kind: podutil.ReplicaSetKind, Name: "rs1", UID: "rs1"}).Obj(),
				testing_helper.MakePod().Namespace("default").Name("p17").UID("p17").
					ControllerRef(metav1.OwnerReference{Kind: podutil.ReplicaSetKind, Name: "rs1", UID: "rs1"}).Obj(),
				testing_helper.MakePod().Namespace("default").Name("p18").UID("p18").
					ControllerRef(metav1.OwnerReference{Kind: podutil.ReplicaSetKind, Name: "rs1", UID: "rs1"}).Obj(),
				testing_helper.MakePod().Namespace("default").Name("p19").UID("p19").
					ControllerRef(metav1.OwnerReference{Kind: podutil.ReplicaSetKind, Name: "rs1", UID: "rs1"}).Obj(),
			},
			expectedSameSchedulers: [][]string{
				{
					"default/p0", "default/p1", "default/p2", "default/p3", "default/p4", "default/p5", "default/p6",
					"default/p7", "default/p8", "default/p9", "default/p10", "default/p11", "default/p12", "default/p13",
					"default/p14", "default/p15", "default/p16", "default/p17", "default/p18", "default/p19",
				},
			},
		},
		{
			name: "all pods have no owner",
			pods: []*v1.Pod{
				testing_helper.MakePod().Namespace("default").Name("p0").UID("p0").Obj(),
				testing_helper.MakePod().Namespace("default").Name("p1").UID("p1").Obj(),
				testing_helper.MakePod().Namespace("default").Name("p2").UID("p2").Obj(),
				testing_helper.MakePod().Namespace("default").Name("p3").UID("p3").Obj(),
				testing_helper.MakePod().Namespace("default").Name("p4").UID("p4").Obj(),
				testing_helper.MakePod().Namespace("default").Name("p5").UID("p5").Obj(),
				testing_helper.MakePod().Namespace("default").Name("p6").UID("p6").Obj(),
				testing_helper.MakePod().Namespace("default").Name("p7").UID("p7").Obj(),
				testing_helper.MakePod().Namespace("default").Name("p8").UID("p8").Obj(),
				testing_helper.MakePod().Namespace("default").Name("p9").UID("p9").Obj(),
				testing_helper.MakePod().Namespace("default").Name("p10").UID("p10").Obj(),
				testing_helper.MakePod().Namespace("default").Name("p11").UID("p11").Obj(),
				testing_helper.MakePod().Namespace("default").Name("p12").UID("p12").Obj(),
				testing_helper.MakePod().Namespace("default").Name("p13").UID("p13").Obj(),
				testing_helper.MakePod().Namespace("default").Name("p14").UID("p14").Obj(),
				testing_helper.MakePod().Namespace("default").Name("p15").UID("p15").Obj(),
				testing_helper.MakePod().Namespace("default").Name("p16").UID("p16").Obj(),
				testing_helper.MakePod().Namespace("default").Name("p17").UID("p17").Obj(),
				testing_helper.MakePod().Namespace("default").Name("p18").UID("p18").Obj(),
				testing_helper.MakePod().Namespace("default").Name("p19").UID("p19").Obj(),
			},
		},
		{
			name: "some pods have owners, some have no",
			pods: []*v1.Pod{
				testing_helper.MakePod().Namespace("default").Name("p0").UID("p0").
					ControllerRef(metav1.OwnerReference{Kind: podutil.ReplicaSetKind, Name: "rs1", UID: "rs1"}).Obj(),
				testing_helper.MakePod().Namespace("default").Name("p1").UID("p1").Obj(),
				testing_helper.MakePod().Namespace("default").Name("p2").UID("p2").
					ControllerRef(metav1.OwnerReference{Kind: podutil.ReplicaSetKind, Name: "rs2", UID: "rs2"}).Obj(),
				testing_helper.MakePod().Namespace("default").Name("p3").UID("p3").Obj(),
				testing_helper.MakePod().Namespace("default").Name("p4").UID("p4").
					ControllerRef(metav1.OwnerReference{Kind: podutil.ReplicaSetKind, Name: "rs2", UID: "rs2"}).Obj(),
				testing_helper.MakePod().Namespace("default").Name("p5").UID("p5").
					ControllerRef(metav1.OwnerReference{Kind: podutil.ReplicaSetKind, Name: "rs1", UID: "rs1"}).Obj(),
				testing_helper.MakePod().Namespace("default").Name("p6").UID("p6").
					ControllerRef(metav1.OwnerReference{Kind: podutil.ReplicaSetKind, Name: "rs2", UID: "rs2"}).Obj(),
				testing_helper.MakePod().Namespace("default").Name("p7").UID("p7").Obj(),
				testing_helper.MakePod().Namespace("default").Name("p8").UID("p8").
					ControllerRef(metav1.OwnerReference{Kind: podutil.ReplicaSetKind, Name: "rs1", UID: "rs1"}).Obj(),
				testing_helper.MakePod().Namespace("default").Name("p9").UID("p9").Obj(),
				testing_helper.MakePod().Namespace("default").Name("p10").UID("p10").
					ControllerRef(metav1.OwnerReference{Kind: podutil.ReplicaSetKind, Name: "rs1", UID: "rs1"}).Obj(),
				testing_helper.MakePod().Namespace("default").Name("p11").UID("p11").Obj(),
				testing_helper.MakePod().Namespace("default").Name("p12").UID("p12").
					ControllerRef(metav1.OwnerReference{Kind: podutil.ReplicaSetKind, Name: "rs2", UID: "rs2"}).Obj(),
				testing_helper.MakePod().Namespace("default").Name("p13").UID("p13").Obj(),
				testing_helper.MakePod().Namespace("default").Name("p14").UID("p14").
					ControllerRef(metav1.OwnerReference{Kind: podutil.ReplicaSetKind, Name: "rs2", UID: "rs2"}).Obj(),
				testing_helper.MakePod().Namespace("default").Name("p15").UID("p15").
					ControllerRef(metav1.OwnerReference{Kind: podutil.ReplicaSetKind, Name: "rs1", UID: "rs1"}).Obj(),
				testing_helper.MakePod().Namespace("default").Name("p16").UID("p16").
					ControllerRef(metav1.OwnerReference{Kind: podutil.ReplicaSetKind, Name: "rs2", UID: "rs2"}).Obj(),
				testing_helper.MakePod().Namespace("default").Name("p17").UID("p17").Obj(),
				testing_helper.MakePod().Namespace("default").Name("p18").UID("p18").
					ControllerRef(metav1.OwnerReference{Kind: podutil.ReplicaSetKind, Name: "rs1", UID: "rs1"}).Obj(),
				testing_helper.MakePod().Namespace("default").Name("p19").UID("p19").Obj(),
			},
			expectedSameSchedulers: [][]string{
				{"default/p0", "default/p5", "default/p8", "default/p10", "default/p15", "default/p18"},
				{"default/p2", "default/p4", "default/p6", "default/p12", "default/p14", "default/p16"},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			utilfeature.DefaultMutableFeatureGate.SetFromMap(map[string]bool{string(features.SupportRescheduling): true})

			stopCh := make(chan struct{})
			defer close(stopCh)
			client := clientsetfake.NewSimpleClientset()
			informerFactory := informers.NewSharedInformerFactory(client, 0)
			crdClient := godelclientfake.NewSimpleClientset(scheduler0, scheduler1, scheduler2)
			crdInformerFactory := crdinformers.NewSharedInformerFactory(crdClient, 0)
			podInformer := informerFactory.Core().V1().Pods()
			nodeInformer := informerFactory.Core().V1().Nodes()
			schedulerInformer := crdInformerFactory.Scheduling().V1alpha1().Schedulers()
			nmNodeInformer := crdInformerFactory.Node().V1alpha1().NMNodes()
			podGroupInformer := crdInformerFactory.Scheduling().V1alpha1().PodGroups()
			pcInformer := informerFactory.Scheduling().V1().PriorityClasses()
			schedulerSharedInformer := schedulerInformer.Informer()
			crdInformerFactory.Start(stopCh)
			podSharedInformer := podInformer.Informer()
			informerFactory.Start(stopCh)
			cache.WaitForCacheSync(stopCh, podSharedInformer.HasSynced, schedulerSharedInformer.HasSynced)

			dispatcher := New(stopCh, client, crdClient, podInformer, nodeInformer, schedulerInformer, nmNodeInformer, podGroupInformer, pcInformer, schedulerName, nil)

			for _, p := range tt.pods {
				dispatcher.addPodToPendingOrSortedQueue(p)
				if _, err := client.CoreV1().Pods(p.Namespace).Create(context.Background(), p, metav1.CreateOptions{}); err != nil {
					t.Errorf("failed to create pod %s: %v", p.Name, err)
				}
			}
			ctx, cancelR := context.WithCancel(context.Background())
			dispatcher.Run(ctx)
			time.Sleep(time.Second)
			cancelR()

			podListInScheduler0 := sets.NewString()
			for _, pod := range dispatcher.DispatchInfo.GetPodsOfOneScheduler("godel-scheduler-0") {
				podListInScheduler0.Insert(pod)
			}
			klog.InfoS(fmt.Sprintf("got pods in godel-scheduler-0: %v", podListInScheduler0))
			podListInScheduler1 := sets.NewString()
			for _, pod := range dispatcher.DispatchInfo.GetPodsOfOneScheduler("godel-scheduler-1") {
				podListInScheduler1.Insert(pod)
			}
			klog.InfoS(fmt.Sprintf("got pods in godel-scheduler-1: %v", podListInScheduler1))
			podListInScheduler2 := sets.NewString()
			for _, pod := range dispatcher.DispatchInfo.GetPodsOfOneScheduler("godel-scheduler-2") {
				podListInScheduler2.Insert(pod)
			}
			klog.InfoS(fmt.Sprintf("got pods in godel-scheduler-2: %v", podListInScheduler2))
			for _, podsInSameScheduler := range tt.expectedSameSchedulers {
				if !podListInScheduler0.HasAll(podsInSameScheduler...) && podListInScheduler0.HasAny(podsInSameScheduler...) {
					t.Errorf("these pods need be in the same scheduler: %v", podsInSameScheduler)
					t.Errorf("actual pods in scheduler-0: %v\nactual pods in scheduler-1: %v\nactual pods in scheduler-2: %v", podListInScheduler0, podListInScheduler1, podListInScheduler2)
				}
				if !podListInScheduler1.HasAll(podsInSameScheduler...) && podListInScheduler1.HasAny(podsInSameScheduler...) {
					t.Errorf("these pods need be in the same scheduler: %v", podsInSameScheduler)
					t.Errorf("actual pods in scheduler-0: %v\nactual pods in scheduler-1: %v\nactual pods in scheduler-2: %v", podListInScheduler0, podListInScheduler1, podListInScheduler2)
				}
				if !podListInScheduler2.HasAll(podsInSameScheduler...) && podListInScheduler2.HasAny(podsInSameScheduler...) {
					t.Errorf("these pods need be in the same scheduler: %v", podsInSameScheduler)
					t.Errorf("actual pods in scheduler-0: %v\nactual pods in scheduler-1: %v\nactual pods in scheduler-2: %v", podListInScheduler0, podListInScheduler1, podListInScheduler2)
				}
			}
		})
	}
}
