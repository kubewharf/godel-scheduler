/*
Copyright 2014 The Kubernetes Authors.

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

package scheduler

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	v1 "k8s.io/api/core/v1"
	eventsv1 "k8s.io/api/events/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/client-go/informers"
	clientsetfake "k8s.io/client-go/kubernetes/fake"
	"k8s.io/client-go/kubernetes/scheme"
	clienttesting "k8s.io/client-go/testing"
	"k8s.io/client-go/tools/events"

	"github.com/kubewharf/godel-scheduler-api/pkg/apis/scheduling/v1alpha1"
	godelclientfake "github.com/kubewharf/godel-scheduler-api/pkg/client/clientset/versioned/fake"
	crdinformers "github.com/kubewharf/godel-scheduler-api/pkg/client/informers/externalversions"
	framework "github.com/kubewharf/godel-scheduler/pkg/framework/api"
	"github.com/kubewharf/godel-scheduler/pkg/scheduler/apis/config"
	"github.com/kubewharf/godel-scheduler/pkg/scheduler/cache"
	preemptionstore "github.com/kubewharf/godel-scheduler/pkg/scheduler/cache/commonstores/preemption_store"
	"github.com/kubewharf/godel-scheduler/pkg/util"
	cmdutil "github.com/kubewharf/godel-scheduler/pkg/util/cmd"
	"github.com/kubewharf/godel-scheduler/pkg/util/node"
	podutil "github.com/kubewharf/godel-scheduler/pkg/util/pod"
	unitstatus "github.com/kubewharf/godel-scheduler/pkg/util/unitstatus"
	katalystv1alpha1 "github.com/kubewharf/katalyst-api/pkg/apis/node/v1alpha1"
	katalystclientfake "github.com/kubewharf/katalyst-api/pkg/client/clientset/versioned/fake"
	katalystinformers "github.com/kubewharf/katalyst-api/pkg/client/informers/externalversions"
	"github.com/stretchr/testify/assert"
)

func TestSchedulerCreation(t *testing.T) {
	cases := []struct {
		name    string
		opts    []Option
		wantErr string
	}{
		{
			name: "default scheduler",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			client := clientsetfake.NewSimpleClientset()
			crdClient := godelclientfake.NewSimpleClientset()
			katalystCrdClient := katalystclientfake.NewSimpleClientset()
			informerFactory := informers.NewSharedInformerFactory(client, 0)
			crdInformerFactory := crdinformers.NewSharedInformerFactory(crdClient, 0)
			katalystInformerFactory := katalystinformers.NewSharedInformerFactory(katalystCrdClient, 0)

			eventBroadcaster := events.NewBroadcaster(&events.EventSinkImpl{Interface: client.EventsV1()})
			eventRecorder := eventBroadcaster.NewRecorder(scheme.Scheme, testSchedulerName)

			stopCh := make(chan struct{})
			defer close(stopCh)
			_, err := New(
				testSchedulerName,
				&testSchedulerSysName,
				client,
				crdClient,
				informerFactory,
				crdInformerFactory,
				katalystInformerFactory,
				stopCh,
				eventRecorder,
			)
			if len(tc.wantErr) != 0 {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Errorf("got error %q, want %q", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("Failed to create scheduler: %v", err)
			}
		})
	}
}

func TestInitSchedulerCRD(t *testing.T) {
	cases := []struct {
		name    string
		opts    []Option
		wantErr string
	}{
		{
			name: "default scheduler",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			client := clientsetfake.NewSimpleClientset()
			crdClient := godelclientfake.NewSimpleClientset()
			katalystCrdClient := katalystclientfake.NewSimpleClientset()
			informerFactory := informers.NewSharedInformerFactory(client, 0)
			crdInformerFactory := crdinformers.NewSharedInformerFactory(crdClient, 0)
			katalystInformerFactory := katalystinformers.NewSharedInformerFactory(katalystCrdClient, 0)

			eventBroadcaster := events.NewBroadcaster(&events.EventSinkImpl{Interface: client.EventsV1()})
			eventRecorder := eventBroadcaster.NewRecorder(scheme.Scheme, testSchedulerName)

			stopCh := make(chan struct{})
			defer close(stopCh)
			testingScheduler, err := New(
				testSchedulerName,
				&testSchedulerSysName,
				client,
				crdClient,
				informerFactory,
				crdInformerFactory,
				katalystInformerFactory,
				stopCh,
				eventRecorder,
			)
			assert.Nil(t, err)

			err = ensureSchedulerUpToDate(testingScheduler.crdClient, testingScheduler.clock, testingScheduler.Name)
			assert.NoError(t, err, "unexpected error %v", err)

			expectedScheduler, err := crdClient.SchedulingV1alpha1().Schedulers().Get(context.TODO(), testSchedulerName, metav1.GetOptions{})
			assert.NoError(t, err, "unexpected error %v", err)
			assert.NotNil(t, expectedScheduler)

			err = ensureSchedulerUpToDate(testingScheduler.crdClient, testingScheduler.clock, testingScheduler.Name)
			assert.NoError(t, err, "unexpected error %v", err)
		})
	}
}

func TestSchedulerStatusErrors(t *testing.T) {
	client := clientsetfake.NewSimpleClientset()
	crdClient := godelclientfake.NewSimpleClientset()
	katalystCrdClient := katalystclientfake.NewSimpleClientset()
	informerFactory := informers.NewSharedInformerFactory(client, 0)
	crdInformerFactory := crdinformers.NewSharedInformerFactory(crdClient, 0)
	katalystInformerFactory := katalystinformers.NewSharedInformerFactory(katalystCrdClient, 0)

	eventBroadcaster := events.NewBroadcaster(&events.EventSinkImpl{Interface: client.EventsV1()})
	eventRecorder := eventBroadcaster.NewRecorder(scheme.Scheme, testSchedulerName)

	stopCh := make(chan struct{})
	defer close(stopCh)
	testingScheduler, _ := New(
		testSchedulerName,
		&testSchedulerSysName,
		client,
		crdClient,
		informerFactory,
		crdInformerFactory,
		katalystInformerFactory,
		stopCh,
		eventRecorder,
	)

	expectedScheduler := &v1alpha1.Scheduler{
		ObjectMeta: metav1.ObjectMeta{
			Name: "testscheduler",
		},
	}

	crdClient.PrependReactor("get", "schedulers", func(action clienttesting.Action) (bool, runtime.Object, error) {
		getAction := action.(clienttesting.GetAction)
		if getAction.GetName() == "no-scheduler" {
			return true, nil, fmt.Errorf("unable to fetch scheduler info %s", getAction.GetName())
		}

		return true, expectedScheduler, nil
	})

	crdClient.PrependReactor("update", "schedulers", func(action clienttesting.Action) (bool, runtime.Object, error) {
		updateAction := action.(clienttesting.UpdateAction)
		obj := updateAction.GetObject().(*v1alpha1.Scheduler)

		if updateAction.GetSubresource() == "status" {
			return true, nil, fmt.Errorf("unable to update scheduler %s", obj.GetName())
		}

		return true, obj, nil
	})

	err := ensureSchedulerUpToDate(testingScheduler.crdClient, testingScheduler.clock, "no-scheduler")
	assert.Error(t, err)

	err = ensureSchedulerUpToDate(testingScheduler.crdClient, testingScheduler.clock, testingScheduler.Name)
	assert.Error(t, err, "unexpected error %v", err)
}

func podWithAnnotation(pod *v1.Pod, annotations map[string]string) *v1.Pod {
	pod.Annotations = annotations
	return pod
}

func makeAllocatableResources(milliCPU, memory, pods, storage int64) v1.ResourceList {
	return v1.ResourceList{
		v1.ResourceCPU:              *resource.NewMilliQuantity(milliCPU, resource.DecimalSI),
		v1.ResourceMemory:           *resource.NewQuantity(memory, resource.BinarySI),
		v1.ResourcePods:             *resource.NewQuantity(pods, resource.DecimalSI),
		v1.ResourceEphemeralStorage: *resource.NewQuantity(storage, resource.BinarySI),
	}
}

func podWithID(id, desiredHost string) *v1.Pod {
	return &v1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      id,
			Namespace: "test",
			UID:       types.UID(id),
		},
		Spec: v1.PodSpec{
			NodeName:      desiredHost,
			SchedulerName: testSchedulerName,
		},
	}
}

func TestSchedulerEvent(t *testing.T) {
	cases := []struct {
		name   string
		pod    *v1.Pod
		reason string
		action string
	}{
		{
			name:   "event test",
			pod:    podWithAnnotation(podWithID("test", ""), map[string]string{}),
			reason: "FailedScheduling",
			action: "Scheduling",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			client := clientsetfake.NewSimpleClientset()
			eventBroadcaster := events.NewBroadcaster(&events.EventSinkImpl{Interface: client.EventsV1()})
			eventRecorder := eventBroadcaster.NewRecorder(scheme.Scheme, testSchedulerName)

			added := false
			now := time.Now()
			client.PrependReactor("create", "events", func(action clienttesting.Action) (handled bool, obj runtime.Object, err error) {
				var event *eventsv1.Event
				if action.GetResource() == eventsv1.SchemeGroupVersion.WithResource("events") {
					event = action.(clienttesting.CreateAction).GetObject().(*eventsv1.Event)
					added = true
				}
				t.Logf("costs %v", time.Since(now))
				return added, event, nil
			})
			eventBroadcaster.StartRecordingToSink(ctx.Done())

			eventRecorder.Eventf(tc.pod, nil, v1.EventTypeWarning, tc.reason, tc.action, "skip schedule deleting pod: %v/%v", tc.pod.Namespace, tc.pod.Name)

			time.Sleep(1 * time.Second)
			assert.True(t, added)
		})
	}
}

func makeResources(milliCPU, memory, pods, storage int64) v1.NodeResources {
	return v1.NodeResources{
		Capacity: v1.ResourceList{
			v1.ResourceCPU:              *resource.NewMilliQuantity(milliCPU, resource.DecimalSI),
			v1.ResourceMemory:           *resource.NewQuantity(memory, resource.BinarySI),
			v1.ResourcePods:             *resource.NewQuantity(pods, resource.DecimalSI),
			v1.ResourceEphemeralStorage: *resource.NewQuantity(storage, resource.BinarySI),
		},
	}
}

func TestScheduleUnitMultiScheduleSwitch(t *testing.T) {
	testNode := &v1.Node{
		ObjectMeta: metav1.ObjectMeta{Name: "machine1", UID: types.UID("machine1")},
		Status:     v1.NodeStatus{Capacity: makeResources(1, 1, 10, 10).Capacity, Allocatable: makeAllocatableResources(1, 1, 10, 10)},
	}
	beResource := makeAllocatableResources(1, 1, 10, 10)
	cnr := &katalystv1alpha1.CustomNodeResource{
		ObjectMeta: metav1.ObjectMeta{Name: "machine1", UID: types.UID("machine1")},
		Status: katalystv1alpha1.CustomNodeResourceStatus{
			Resources: katalystv1alpha1.Resources{
				Allocatable: &beResource,
				Capacity:    &beResource,
			},
		},
	}
	resource := framework.Resource{MilliCPU: 1, Memory: 1}

	gtPodInfo := &framework.QueuedPodInfo{
		Pod: &v1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "pod1",
				Namespace: "default",
				UID:       types.UID("pod1"),
				Annotations: map[string]string{
					podutil.PodLauncherAnnotationKey:     string(podutil.Kubelet),
					podutil.PodResourceTypeAnnotationKey: string(podutil.GuaranteedPod),
					podutil.SchedulerAnnotationKey:       testSchedulerSysName,
					podutil.PodStateAnnotationKey:        string(podutil.PodDispatched),
				},
			},
			Spec: v1.PodSpec{
				SchedulerName: testSchedulerSysName,
				Containers: []v1.Container{{
					Resources: v1.ResourceRequirements{Requests: resource.ResourceList()},
				}},
			},
		},
	}
	bePodInfo := &framework.QueuedPodInfo{
		Pod: &v1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "pod2",
				Namespace: "default",
				UID:       types.UID("pod2"),
				Annotations: map[string]string{
					podutil.PodLauncherAnnotationKey:     string(podutil.Kubelet),
					podutil.PodResourceTypeAnnotationKey: string(podutil.BestEffortPod),
					podutil.SchedulerAnnotationKey:       testSchedulerSysName,
					podutil.PodStateAnnotationKey:        string(podutil.PodDispatched),
				},
			},
			Spec: v1.PodSpec{
				SchedulerName: testSchedulerSysName,
				Containers: []v1.Container{{
					Resources: v1.ResourceRequirements{Requests: resource.ResourceList()},
				}},
			},
		},
	}

	client := clientsetfake.NewSimpleClientset(testNode)
	broadcaster := cmdutil.NewEventBroadcasterAdapter(client)
	eventRecorder := broadcaster.NewRecorder(testSchedulerName)
	client.PrependReactor("patch", "pods", func(action clienttesting.Action) (bool, runtime.Object, error) {
		return true, &v1.Pod{}, nil
	})

	stop := make(chan struct{})
	defer close(stop)

	informerFactory := informers.NewSharedInformerFactory(client, 0)
	informerFactory.Start(stop)
	informerFactory.WaitForCacheSync(stop)
	crdClient := godelclientfake.NewSimpleClientset()
	crdInformerFactory := crdinformers.NewSharedInformerFactory(crdClient, 0)
	crdInformerFactory.Start(stop)
	crdInformerFactory.WaitForCacheSync(stop)
	katalystCrdClient := katalystclientfake.NewSimpleClientset()
	katalystInformerFactory := katalystinformers.NewSharedInformerFactory(katalystCrdClient, 0)
	katalystInformerFactory.Start(stop)
	katalystInformerFactory.WaitForCacheSync(stop)

	s, _ := New(
		testSchedulerSysName,
		&testSchedulerSysName,
		client,
		crdClient,
		informerFactory,
		crdInformerFactory,
		katalystInformerFactory,
		stop,
		eventRecorder,
	)
	s.ScheduleSwitch.Process(
		framework.SwitchTypeAll,
		func(dataSet ScheduleDataSet) {
			dataSet.SchedulingQueue().Run()
		},
	)
	s.addPod(bePodInfo.Pod)
	s.addPod(gtPodInfo.Pod)
	s.addNodeToCache(testNode)
	s.addCNRToCache(cnr)

	{
		dataSet := s.ScheduleSwitch.Get(framework.SwitchType(1 << framework.MaxSwitchNum))
		pendingPods := dataSet.SchedulingQueue().PendingPods()
		assert.Equal(t, 1, len(pendingPods))
		assert.Equal(t, pendingPods[0], bePodInfo.Pod)

		dataSet.ScheduleFunc()(context.TODO())

		isAssumed, err := s.commonCache.IsAssumedPod(bePodInfo.Pod)
		assert.Equal(t, true, isAssumed)
		assert.NoError(t, err)
	}
	{
		dataSet := s.ScheduleSwitch.Get(framework.SwitchType(1))
		pendingPods := dataSet.SchedulingQueue().PendingPods()
		assert.Equal(t, 1, len(pendingPods))
		assert.Equal(t, pendingPods[0], gtPodInfo.Pod)

		dataSet.ScheduleFunc()(context.TODO())

		isAssumed, err := s.commonCache.IsAssumedPod(gtPodInfo.Pod)
		assert.Equal(t, true, isAssumed)
		assert.NoError(t, err)
	}
}

func TestScheduleUnit_Preemption(t *testing.T) {
	testNode := &v1.Node{
		ObjectMeta: metav1.ObjectMeta{Name: "machine1", UID: types.UID("machine1")},
		Status:     v1.NodeStatus{Capacity: makeResources(50, 20, 32, 20).Capacity, Allocatable: makeAllocatableResources(50, 20, 32, 20)},
	}
	testNodeSuccess := &v1.Node{
		ObjectMeta: metav1.ObjectMeta{Name: "machine2", UID: types.UID("machine2"), Annotations: map[string]string{node.GodelSchedulerNodeAnnotationKey: testSchedulerSysName}},
		Status:     v1.NodeStatus{Capacity: makeResources(50, 20, 32, 20).Capacity, Allocatable: makeAllocatableResources(50, 20, 32, 20)},
	}

	minMembers := 3
	highPri := int32(10)
	lowPri := int32(5)
	namespace := "test"
	podGroupName := "testPodGroup"
	testPodGroup := &v1alpha1.PodGroup{
		ObjectMeta: metav1.ObjectMeta{
			Name:      podGroupName,
			Namespace: namespace,
			UID:       types.UID(podGroupName),
		},
		Spec: v1alpha1.PodGroupSpec{
			MinMember: int32(minMembers),
		},
	}

	var pods []*v1.Pod
	for i := 0; i < minMembers; i++ {
		podName := fmt.Sprintf("testpod-%d", i)
		resource := framework.Resource{MilliCPU: 15, Memory: 1}
		testPod := &v1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name:      podName,
				Namespace: namespace,
				UID:       types.UID(podName),
				Annotations: map[string]string{
					podutil.PodLauncherAnnotationKey:     string(podutil.Kubelet),
					podutil.PodResourceTypeAnnotationKey: string(podutil.GuaranteedPod),
					podutil.PodGroupNameAnnotationKey:    podGroupName,
					podutil.SchedulerAnnotationKey:       testSchedulerSysName,
				},
			},
			Spec: v1.PodSpec{
				Priority:      &highPri,
				SchedulerName: testSchedulerName,
				Containers: []v1.Container{{
					Resources: v1.ResourceRequirements{Requests: resource.ResourceList()},
				}},
				NodeName: "",
			},
		}

		pods = append(pods, testPod)
	}

	// When we remove testPod1 and testPod2, the testNode is available, so we set the
	// MilliCPU to 40 instead of any number that less than (50-15-15=20)
	resource := framework.Resource{MilliCPU: 40, Memory: 1}
	testPodSuccess := &v1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "testPod",
			Namespace: namespace,
			UID:       types.UID("testPod"),
			Annotations: map[string]string{
				podutil.PodLauncherAnnotationKey:     string(podutil.Kubelet),
				podutil.PodResourceTypeAnnotationKey: string(podutil.GuaranteedPod),
				podutil.PodGroupNameAnnotationKey:    podGroupName,
				podutil.SchedulerAnnotationKey:       testSchedulerSysName,
				util.DebugModeAnnotationKey:          util.DebugModeOn,
			},
		},
		Spec: v1.PodSpec{
			Priority:      &highPri,
			SchedulerName: testSchedulerName,
			Containers: []v1.Container{{
				Resources: v1.ResourceRequirements{Requests: resource.ResourceList()},
			}},
			NodeName: "",
		},
	}

	resource = framework.Resource{MilliCPU: 20, Memory: 10}
	testPod1 := &v1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "testpod1",
			Namespace: namespace,
			UID:       types.UID("testpod1"),
			Annotations: map[string]string{
				podutil.PodLauncherAnnotationKey:     string(podutil.Kubelet),
				podutil.PodResourceTypeAnnotationKey: string(podutil.GuaranteedPod),
				podutil.SchedulerAnnotationKey:       testSchedulerSysName,
				util.CanBePreemptedAnnotationKey:     util.CanBePreempted,
			},
			Labels: map[string]string{
				"name": "testpod1",
			},
		},
		Spec: v1.PodSpec{
			Priority:          &lowPri,
			PriorityClassName: "pc",
			SchedulerName:     testSchedulerName,
			Containers: []v1.Container{{
				Resources: v1.ResourceRequirements{Requests: resource.ResourceList()},
			}},
			NodeName: testNode.Name,
		},
	}
	testPod2 := &v1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "testpod2",
			Namespace: namespace,
			UID:       types.UID("testpod2"),
			Annotations: map[string]string{
				podutil.PodLauncherAnnotationKey:     string(podutil.Kubelet),
				podutil.PodResourceTypeAnnotationKey: string(podutil.GuaranteedPod),
				podutil.SchedulerAnnotationKey:       testSchedulerSysName,
				util.CanBePreemptedAnnotationKey:     util.CanBePreempted,
			},
			Labels: map[string]string{
				"name": "testpod1",
			},
		},
		Spec: v1.PodSpec{
			Priority:          &lowPri,
			PriorityClassName: "pc",
			SchedulerName:     testSchedulerName,
			Containers: []v1.Container{{
				Resources: v1.ResourceRequirements{Requests: resource.ResourceList()},
			}},
			NodeName: testNode.Name,
		},
	}

	client := clientsetfake.NewSimpleClientset(testNode, testNodeSuccess, testPodSuccess, testPod1, testPod2, pods[0], pods[1], pods[2])
	broadcaster := cmdutil.NewEventBroadcasterAdapter(client)
	eventRecorder := broadcaster.NewRecorder(testSchedulerName)

	actualPatchRequests := 0
	client.PrependReactor("patch", "pods", func(action clienttesting.Action) (bool, runtime.Object, error) {
		actualPatchRequests++
		// For this test, we don't care about the result of the patched pod, just that we got the expected
		// patch request, so just returning &v1.Pod{} here is OK because scheduler doesn't use the response.
		return true, &v1.Pod{}, nil
	})

	stop := make(chan struct{})
	defer close(stop)

	informerFactory := informers.NewSharedInformerFactory(client, 0)
	informerFactory.Start(stop)
	informerFactory.WaitForCacheSync(stop)
	informerFactory.Core().V1().Pods().Informer().GetIndexer().Add(testPodSuccess)
	informerFactory.Core().V1().Pods().Informer().GetIndexer().Add(testPod1)
	informerFactory.Core().V1().Pods().Informer().GetIndexer().Add(testPod2)
	crdClient := godelclientfake.NewSimpleClientset()
	crdInformerFactory := crdinformers.NewSharedInformerFactory(crdClient, 0)
	crdInformerFactory.Start(stop)
	crdInformerFactory.WaitForCacheSync(stop)
	crdInformerFactory.Scheduling().V1alpha1().PodGroups().Informer().GetIndexer().Add(testPodGroup)
	katalystCrdClient := katalystclientfake.NewSimpleClientset()
	katalystInformerFactory := katalystinformers.NewSharedInformerFactory(katalystCrdClient, 0)
	katalystInformerFactory.Start(stop)
	katalystInformerFactory.WaitForCacheSync(stop)

	s, _ := New(
		testSchedulerSysName,
		&testSchedulerSysName,
		client,
		crdClient,
		informerFactory,
		crdInformerFactory,
		katalystInformerFactory,
		stop,
		eventRecorder,
		WithDefaultProfile(
			&config.GodelSchedulerProfile{
				DisablePreemption: &disablePodPreemption,
			},
		),
	)
	dataSet := s.ScheduleSwitch.Get(framework.SwitchType(1))
	cache, queue := s.commonCache, dataSet.SchedulingQueue()
	queue.Run()
	s.addNodeToCache(testNode)
	s.addPodGroupToCache(testPodGroup)
	s.addPod(testPod1)
	s.addPod(testPod2)

	for _, pod := range pods {
		informerFactory.Core().V1().Pods().Informer().GetIndexer().Add(pod)
		queue.Add(pod)
	}

	dataSet.ScheduleFunc()(context.Background())
	for _, pod := range pods {
		isAssumed, err := cache.IsAssumedPod(pod)
		assert.Equal(t, true, isAssumed)
		assert.NoError(t, err)
	}

	count, _ := cache.PodCount()
	assert.Equal(t, 5, count)

	// TODO: improve this code logic, we should make sure the unit status is scheduled.
	// For this case, we have 5 pods in cache, two of them are victims, so we can says the unit is scheduled cause
	// the other 3 pods (which satisfy min-member) has been scheduled.
	cache.SetUnitSchedulingStatus(fmt.Sprintf("%s/%s/%s", framework.PodGroupUnitType, namespace, podGroupName), unitstatus.ScheduledStatus)

	// 2) Adding more pods of a pod group with failure
	queue.Add(testPodSuccess)
	dataSet.ScheduleFunc()(context.Background())
	isAssumed, _ := cache.IsAssumedPod(testPodSuccess)
	assert.Equal(t, false, isAssumed)

	// 3) Adding more pods of a pod group with success
	s.addNodeToCache(testNodeSuccess)
	dataSet.ScheduleFunc()(context.Background())
	isAssumed, _ = cache.IsAssumedPod(testPodSuccess)
	assert.Equal(t, true, isAssumed)
}

func TestScheduleUnit_RemoveVictims(t *testing.T) {
	testNode := &v1.Node{
		ObjectMeta: metav1.ObjectMeta{Name: "machine1", UID: types.UID("machine1")},
		Status:     v1.NodeStatus{Capacity: makeResources(50, 20, 32, 20).Capacity, Allocatable: makeAllocatableResources(50, 20, 32, 20)},
	}

	minMembers := 2
	highPri := int32(10)
	lowPri := int32(5)
	namespace := "test"
	podGroupName := "testPodGroup"
	testPodGroup := &v1alpha1.PodGroup{
		ObjectMeta: metav1.ObjectMeta{
			Name:      podGroupName,
			Namespace: namespace,
			UID:       types.UID(podGroupName),
		},
		Spec: v1alpha1.PodGroupSpec{
			MinMember: int32(minMembers),
		},
	}

	var pods []*v1.Pod
	for i := 0; i < minMembers; i++ {
		podName := fmt.Sprintf("testpod-%d", i)
		resource := framework.Resource{MilliCPU: 25, Memory: 10}
		testPod := &v1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name:      podName,
				Namespace: namespace,
				UID:       types.UID(podName),
				Annotations: map[string]string{
					podutil.PodLauncherAnnotationKey:     string(podutil.Kubelet),
					podutil.PodResourceTypeAnnotationKey: string(podutil.GuaranteedPod),
					podutil.PodGroupNameAnnotationKey:    podGroupName,
					podutil.SchedulerAnnotationKey:       testSchedulerSysName,
				},
			},
			Spec: v1.PodSpec{
				Priority:      &highPri,
				SchedulerName: testSchedulerName,
				Containers: []v1.Container{{
					Resources: v1.ResourceRequirements{Requests: resource.ResourceList()},
				}},
				NodeName: "",
			},
		}

		pods = append(pods, testPod)
	}

	resource := framework.Resource{MilliCPU: 25, Memory: 10}
	testPod1 := &v1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "testpod1",
			Namespace: namespace,
			UID:       types.UID("testpod1"),
			Annotations: map[string]string{
				podutil.PodLauncherAnnotationKey:     string(podutil.Kubelet),
				podutil.PodResourceTypeAnnotationKey: string(podutil.GuaranteedPod),
				podutil.SchedulerAnnotationKey:       testSchedulerSysName,
				util.CanBePreemptedAnnotationKey:     util.CanBePreempted,
			},
			Labels: map[string]string{
				"name": "testpod1",
			},
		},
		Spec: v1.PodSpec{
			Priority:          &lowPri,
			PriorityClassName: "pc",
			SchedulerName:     testSchedulerName,
			Containers: []v1.Container{{
				Resources: v1.ResourceRequirements{Requests: resource.ResourceList()},
			}},
			NodeName: testNode.Name,
		},
	}
	testPod2 := &v1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "testpod2",
			Namespace: namespace,
			UID:       types.UID("testpod2"),
			Annotations: map[string]string{
				podutil.PodLauncherAnnotationKey:     string(podutil.Kubelet),
				podutil.PodResourceTypeAnnotationKey: string(podutil.GuaranteedPod),
				podutil.SchedulerAnnotationKey:       testSchedulerSysName,
				util.CanBePreemptedAnnotationKey:     util.CanBePreempted,
			},
			Labels: map[string]string{
				"name": "testpod2",
			},
		},
		Spec: v1.PodSpec{
			Priority:          &lowPri,
			PriorityClassName: "pc",
			SchedulerName:     testSchedulerName,
			Containers: []v1.Container{{
				Resources: v1.ResourceRequirements{Requests: resource.ResourceList()},
			}},
			NodeName: testNode.Name,
		},
	}

	client := clientsetfake.NewSimpleClientset(testNode, testPod1, testPod2, pods[0], pods[1])
	broadcaster := cmdutil.NewEventBroadcasterAdapter(client)
	eventRecorder := broadcaster.NewRecorder(testSchedulerName)

	actualPatchRequests := 0
	client.PrependReactor("patch", "pods", func(action clienttesting.Action) (bool, runtime.Object, error) {
		actualPatchRequests++
		// For this test, we don't care about the result of the patched pod, just that we got the expected
		// patch request, so just returning &v1.Pod{} here is OK because scheduler doesn't use the response.
		return true, &v1.Pod{}, nil
	})

	stop := make(chan struct{})
	defer close(stop)

	informerFactory := informers.NewSharedInformerFactory(client, 0)
	informerFactory.Start(stop)
	informerFactory.WaitForCacheSync(stop)
	informerFactory.Core().V1().Pods().Informer().GetIndexer().Add(testPod1)
	informerFactory.Core().V1().Pods().Informer().GetIndexer().Add(testPod2)
	crdClient := godelclientfake.NewSimpleClientset()
	crdInformerFactory := crdinformers.NewSharedInformerFactory(crdClient, 0)
	crdInformerFactory.Start(stop)
	crdInformerFactory.WaitForCacheSync(stop)
	crdInformerFactory.Scheduling().V1alpha1().PodGroups().Informer().GetIndexer().Add(testPodGroup)
	katalystCrdClient := katalystclientfake.NewSimpleClientset()
	katalystInformerFactory := katalystinformers.NewSharedInformerFactory(katalystCrdClient, 0)
	katalystInformerFactory.Start(stop)
	katalystInformerFactory.WaitForCacheSync(stop)

	s, _ := New(
		testSchedulerSysName,
		&testSchedulerSysName,
		client,
		crdClient,
		informerFactory,
		crdInformerFactory,
		katalystInformerFactory,
		stop,
		eventRecorder,
		WithDefaultProfile(
			&config.GodelSchedulerProfile{
				DisablePreemption: &disablePodPreemption,
			},
		),
	)
	dataSet := s.ScheduleSwitch.Get(framework.SwitchType(1))
	cache, queue, snapshot := s.commonCache, dataSet.SchedulingQueue(), dataSet.Snapshot()
	queue.Run()
	s.addNodeToCache(testNode)
	s.addPodGroupToCache(testPodGroup)
	s.addPod(testPod1)
	s.addPod(testPod2)

	for _, pod := range pods {
		informerFactory.Core().V1().Pods().Informer().GetIndexer().Add(pod)
		queue.Add(pod)
	}

	dataSet.ScheduleFunc()(context.Background())
	for _, pod := range pods {
		isAssumed, err := cache.IsAssumedPod(pod)
		assert.Equal(t, true, isAssumed)
		assert.NoError(t, err)
	}
	count, _ := cache.PodCount()
	assert.Equal(t, 4, count)
	if checkIfVictimExistInSnapshot(snapshot, string(testPod1.UID)) && checkIfVictimExistInSnapshot(snapshot, string(testPod2.UID)) {
		t.Errorf("failed to remove victims from snapshot")
	}

	gotVictimToPreemptors := map[string]sets.String{}
	{
		victims := []string{"test/testpod1/testpod1", "test/testpod2/testpod2"}
		for _, victim := range victims {
			preemptors := snapshot.FindStore(preemptionstore.Name).(*preemptionstore.PreemptionStore).GetPreemptorsByVictim(testNode.Name, victim)
			gotVictimToPreemptors[victim] = sets.NewString(preemptors...)
		}
	}

	expectedVictimToPreemptors1 := map[string]sets.String{
		"test/testpod1/testpod1": sets.NewString("test/testpod-0/testpod-0"),
		"test/testpod2/testpod2": sets.NewString("test/testpod-1/testpod-1"),
	}
	expectedVictimToPreemptors2 := map[string]sets.String{
		"test/testpod1/testpod1": sets.NewString("test/testpod-1/testpod-1"),
		"test/testpod2/testpod2": sets.NewString("test/testpod-0/testpod-0"),
	}
	if !reflect.DeepEqual(expectedVictimToPreemptors1, gotVictimToPreemptors) && !reflect.DeepEqual(expectedVictimToPreemptors2, gotVictimToPreemptors) {
		t.Errorf("expected get VictimToPreemptors: %v or %v, but got: %v", expectedVictimToPreemptors1, expectedVictimToPreemptors2, gotVictimToPreemptors)
	}
}

func checkIfVictimExistInSnapshot(snapshot *cache.Snapshot, uid string) bool {
	nodeInfos := snapshot.List()
	for _, nInfo := range nodeInfos {
		for _, pInfo := range nInfo.GetPods() {
			if string(pInfo.Pod.UID) == uid {
				return true
			}
		}
	}
	return false
}
