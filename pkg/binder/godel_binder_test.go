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

package binder

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	godelclientfake "github.com/kubewharf/godel-scheduler-api/pkg/client/clientset/versioned/fake"
	crdinformers "github.com/kubewharf/godel-scheduler-api/pkg/client/informers/externalversions"
	"github.com/stretchr/testify/assert"
	v1 "k8s.io/api/core/v1"
	policy "k8s.io/api/policy/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/client-go/informers"
	clientsetfake "k8s.io/client-go/kubernetes/fake"
	clienttesting "k8s.io/client-go/testing"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/tools/events"

	"github.com/kubewharf/godel-scheduler/pkg/binder/apis/config"
	godelcache "github.com/kubewharf/godel-scheduler/pkg/binder/cache"
	fakecache "github.com/kubewharf/godel-scheduler/pkg/binder/cache/fake"
	"github.com/kubewharf/godel-scheduler/pkg/binder/framework/plugins/defaultpreemption"
	"github.com/kubewharf/godel-scheduler/pkg/binder/queue"
	binderutils "github.com/kubewharf/godel-scheduler/pkg/binder/utils"
	"github.com/kubewharf/godel-scheduler/pkg/framework/api"
	framework "github.com/kubewharf/godel-scheduler/pkg/framework/api"
	"github.com/kubewharf/godel-scheduler/pkg/framework/utils"
	"github.com/kubewharf/godel-scheduler/pkg/scheduler/apis/config/scheme"
	testinghelper "github.com/kubewharf/godel-scheduler/pkg/testing-helper"
	"github.com/kubewharf/godel-scheduler/pkg/util"
	cmdutil "github.com/kubewharf/godel-scheduler/pkg/util/cmd"
	"github.com/kubewharf/godel-scheduler/pkg/util/constraints"
	podutil "github.com/kubewharf/godel-scheduler/pkg/util/pod"
)

const (
	volumeBindingTimeoutSeconds int64 = 200
)

var defaultAssumedPodAnno map[string]string = map[string]string{
	podutil.AssumedNodeAnnotationKey: "1",
}

func podWithAnnotationsAndLabels(id string, annotations, labels map[string]string) *v1.Pod {
	return &v1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:        id,
			Namespace:   id,
			UID:         types.UID(id),
			Annotations: annotations,
			Labels:      labels,
		},
		Spec: v1.PodSpec{
			SchedulerName: testSchedulerName,
		},
	}
}

func podWithAnnotations(id string, annotations map[string]string) *v1.Pod {
	return &v1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:        id,
			Namespace:   id,
			UID:         types.UID(id),
			Annotations: annotations,
		},
		Spec: v1.PodSpec{
			SchedulerName: testSchedulerName,
		},
	}
}

func setResources(pod *v1.Pod, resources map[string]string) {
	resourceLists := v1.ResourceList{}
	for resourceName, resourceQuan := range resources {
		resourceLists[v1.ResourceName(resourceName)] = resource.MustParse(resourceQuan)
	}
	pod.Spec.Containers = []v1.Container{
		{
			Resources: v1.ResourceRequirements{
				Requests: resourceLists,
			},
		},
	}
}

func podAssumed(nodename string, pod *v1.Pod) *v1.Pod {
	pod.Spec.NodeName = nodename
	return pod
}

func deletingPod(id string) *v1.Pod {
	deletionTimestamp := metav1.Now()
	return &v1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:              id,
			Namespace:         id,
			UID:               types.UID(id),
			DeletionTimestamp: &deletionTimestamp,
			Annotations:       defaultAssumedPodAnno,
		},
		Spec: v1.PodSpec{
			NodeName:      "",
			SchedulerName: testSchedulerName,
		},
	}
}

func TestBinderResolveSameNodeConflict(t *testing.T) {
	testNode := v1.Node{ObjectMeta: metav1.ObjectMeta{Name: "machine1", UID: types.UID("machine1")}}
	testNode.Status.Allocatable = v1.ResourceList{v1.ResourcePods: resource.MustParse("1")}
	client := clientsetfake.NewSimpleClientset(&testNode)
	stop := make(chan struct{})
	defer close(stop)

	informerFactory := informers.NewSharedInformerFactory(client, 0)
	informerFactory.Start(stop)
	informerFactory.WaitForCacheSync(stop)

	table := []struct {
		name             string
		sendPod          *v1.Pod
		expectErrorPod   *v1.Pod
		expectForgetPod  *v1.Pod
		expectAssumedPod *v1.Pod
		eventReason      string
	}{
		{
			name:             "nil pod",
			sendPod:          nil,
			expectAssumedPod: nil,
			eventReason:      "FailedScheduling",
		},
		{
			name:        "deleting pod",
			sendPod:     deletingPod("foo"),
			eventReason: "FailedScheduling",
		},
		{
			name: "pod resource type not set",
			sendPod: podWithAnnotations("foo", map[string]string{
				podutil.AssumedNodeAnnotationKey:         testNode.Name,
				constraints.HardConstraintsAnnotationKey: "a",
				podutil.PodResourceTypeAnnotationKey:     "",
				podutil.PodLauncherAnnotationKey:         string(podutil.Kubelet),
			}),
			expectErrorPod: podWithAnnotations("foo", map[string]string{
				podutil.AssumedNodeAnnotationKey:         testNode.Name,
				constraints.HardConstraintsAnnotationKey: "a",
				podutil.PodResourceTypeAnnotationKey:     "",
				podutil.PodLauncherAnnotationKey:         string(podutil.Kubelet),
			}),
			eventReason: "FailedScheduling",
		},
		{
			name: "pod launcher not set",
			sendPod: podWithAnnotations("foo", map[string]string{
				podutil.AssumedNodeAnnotationKey:         testNode.Name,
				constraints.HardConstraintsAnnotationKey: "a",
				podutil.PodResourceTypeAnnotationKey:     string(podutil.GuaranteedPod),
				podutil.PodLauncherAnnotationKey:         "",
			}),
			expectErrorPod: podWithAnnotations("foo", map[string]string{
				podutil.AssumedNodeAnnotationKey:         testNode.Name,
				constraints.HardConstraintsAnnotationKey: "a",
				podutil.PodResourceTypeAnnotationKey:     string(podutil.GuaranteedPod),
				podutil.PodLauncherAnnotationKey:         "",
			}),
			eventReason: "FailedScheduling",
		},
		{
			name: "assumed pod scheduled",
			sendPod: podWithAnnotations("foo", map[string]string{
				podutil.AssumedNodeAnnotationKey:         testNode.Name,
				constraints.HardConstraintsAnnotationKey: "a",
				podutil.PodResourceTypeAnnotationKey:     string(podutil.GuaranteedPod),
				podutil.PodLauncherAnnotationKey:         string(podutil.Kubelet),
			}),
			expectAssumedPod: podAssumed(testNode.Name, podWithAnnotations("foo", map[string]string{
				podutil.AssumedNodeAnnotationKey:         testNode.Name,
				constraints.HardConstraintsAnnotationKey: "a",
				podutil.PodResourceTypeAnnotationKey:     string(podutil.GuaranteedPod),
				podutil.PodLauncherAnnotationKey:         string(podutil.Kubelet),
			})),
			eventReason: "Scheduled",
		},
	}

	for _, item := range table {
		t.Run(item.name, func(t *testing.T) {
			var gotPod *v1.Pod
			var gotForgetPod *v1.Pod
			var gotAssumedPod *v1.Pod
			pCache := &fakecache.Cache{
				ForgetFunc: func(pod *v1.Pod) {
					if _, ok := pod.Annotations[podutil.TraceContext]; ok {
						delete(pod.Annotations, podutil.TraceContext)
					}
					gotForgetPod = pod
				},
				AssumeFunc: func(pod *v1.Pod) error {
					if _, ok := pod.Annotations[podutil.TraceContext]; ok {
						delete(pod.Annotations, podutil.TraceContext)
					}
					gotAssumedPod = pod
					return nil
				},
				IsAssumedPodFunc: func(pod *v1.Pod) bool {
					if pod == nil || gotAssumedPod == nil {
						return false
					}
					return pod.UID == gotAssumedPod.UID
				},
				GetNodeFunc: func(s string) (framework.NodeInfo, error) {
					nInfo := framework.NewNodeInfo()
					nInfo.SetNode(&testNode)
					return nInfo, nil
				},
			}

			var client *clientsetfake.Clientset
			if item.sendPod != nil {
				client = clientsetfake.NewSimpleClientset(item.sendPod)
			} else {
				client = clientsetfake.NewSimpleClientset()
			}
			crdClient := godelclientfake.NewSimpleClientset()

			broadcaster := cmdutil.NewEventBroadcasterAdapter(client)
			eventRecorder := broadcaster.NewRecorder(testSchedulerName)

			binder := &Binder{
				NextUnit: func() *framework.QueuedUnitInfo {
					return &framework.QueuedUnitInfo{
						ScheduleUnit: &framework.SinglePodUnit{
							Pod: &framework.QueuedPodInfo{Pod: item.sendPod},
						},
						InitialAttemptTimestamp: time.Now(),
					}
				},
				Error: func(p *framework.QueuedPodInfo, err error) {
					gotPod = p.Pod
					if _, ok := gotPod.Annotations[podutil.TraceContext]; ok {
						delete(gotPod.Annotations, podutil.TraceContext)
					}
				},
				BinderCache: pCache,
				handle: NewFrameworkHandle(
					client, crdClient,
					informerFactory, crdinformers.NewSharedInformerFactory(crdClient, 0),
					binderOptions{},
					pCache, volumeBindingTimeoutSeconds,
				),
				recorder: eventRecorder,
			}

			// TODO: we added a rescheduled sleep here to avoid failure due to timing issue.
			// However, to resolve this timing issue completely, we need to check if all the
			// data is correctly populated and this needs further discussion in the future.
			utils.SchedSleep(100 * time.Millisecond)
			binder.CheckAndBindUnit(context.Background())
			if e, a := item.expectAssumedPod, gotAssumedPod; !reflect.DeepEqual(e, a) {
				t.Errorf("assumed pod: wanted %v, got %v", e, a)
			}
			if e, a := item.expectErrorPod, gotPod; !reflect.DeepEqual(e, a) {
				t.Errorf("error pod: wanted %v, got %v", e, a)
			}
			if e, a := item.expectForgetPod, gotForgetPod; !reflect.DeepEqual(e, a) {
				t.Errorf("forget pod: wanted %v, got %v", e, a)
			}
		})
	}
}

func TestAssumePodError(t *testing.T) {
	return
	testNode := v1.Node{ObjectMeta: metav1.ObjectMeta{Name: "machine1", UID: types.UID("machine1")}}
	pod := podWithAnnotations("foo", map[string]string{
		podutil.AssumedNodeAnnotationKey:         testNode.Name,
		constraints.HardConstraintsAnnotationKey: "a",
		podutil.PodResourceTypeAnnotationKey:     string(podutil.GuaranteedPod),
		podutil.PodLauncherAnnotationKey:         string(podutil.Kubelet),
	})
	client := clientsetfake.NewSimpleClientset(&testNode, pod)
	stop := make(chan struct{})
	defer close(stop)
	informerFactory := informers.NewSharedInformerFactory(client, 0)
	informerFactory.Start(stop)
	informerFactory.WaitForCacheSync(stop)
	broadcaster := cmdutil.NewEventBroadcasterAdapter(client)
	eventRecorder := broadcaster.NewRecorder(testSchedulerName)

	var actualPatchData string
	client.PrependReactor("patch", "pods", func(action clienttesting.Action) (bool, runtime.Object, error) {
		patch := action.(clienttesting.PatchAction)
		actualPatchData = string(patch.GetPatch())
		// For this test, we don't care about the result of the patched pod, just that we got the expected
		// patch request, so just returning &v1.Pod{} here is OK because scheduler doesn't use the response.
		return true, &v1.Pod{}, nil
	})

	pCache := &fakecache.Cache{
		AssumeFunc: func(pod *v1.Pod) error {
			return errors.New("assume pod error")
		},
		IsAssumedPodFunc: func(pod *v1.Pod) bool {
			if pod == nil {
				return false
			}
			return false
		},
		GetNodeFunc: func(s string) (framework.NodeInfo, error) {
			nInfo := framework.NewNodeInfo()
			nInfo.SetNode(&testNode)
			return nInfo, nil
		},
	}

	var gotPod *v1.Pod
	var gotError error
	binder := &Binder{
		NextUnit: func() *framework.QueuedUnitInfo {
			return &framework.QueuedUnitInfo{
				ScheduleUnit: &framework.SinglePodUnit{
					Pod: &framework.QueuedPodInfo{Pod: pod},
				},
				InitialAttemptTimestamp: time.Now(),
			}
		},
		Error: func(p *framework.QueuedPodInfo, err error) {
			gotPod = p.Pod
			gotPod.Spec.NodeName = "" // reset assumed pod name for testing purpose
			gotError = err
		},
		handle: NewFrameworkHandle(
			client, nil,
			informerFactory, crdinformers.NewSharedInformerFactory(nil, 0),
			binderOptions{},
			pCache, volumeBindingTimeoutSeconds,
		),
		recorder: eventRecorder,
	}

	binder.CheckAndBindUnit(context.Background())

	if e, a := pod, gotPod; !reflect.DeepEqual(e, a) {
		t.Errorf("pod: wanted %v, got %v", e, a)
	}
	if e, a := errors.New("assume pod error"), gotError; !reflect.DeepEqual(e, a) {
		t.Errorf("error: wanted %v, got %v", e, a)
	}
	assert.Equal(t, `{"metadata":{"annotations":{"godel.bytedance.com/assumed-node":null,"godel.bytedance.com/pod-state":"dispatched"}}}`,
		actualPatchData)

	// Check error for node
	pCache.GetNodeFunc = func(s string) (framework.NodeInfo, error) {
		nInfo := framework.NewNodeInfo()
		nInfo.SetNode(&testNode)
		return nil, errors.New("node not found")
	}
	binder.CheckAndBindUnit(context.Background())
	if e, a := pod, gotPod; !reflect.DeepEqual(e, a) {
		t.Errorf("pod: wanted %v, got %v", e, a)
	}
	if e, a := errors.New("node not found"), gotError; !reflect.DeepEqual(e, a) {
		t.Errorf("error: wanted %v, got %v", e, a)
	}
	assert.Equal(t, `{"metadata":{"annotations":{"godel.bytedance.com/assumed-node":null,"godel.bytedance.com/pod-state":"dispatched"}}}`,
		actualPatchData)
}

func TestBinding(t *testing.T) {
	return
	chanTimeout := 2 * time.Second
	errChan := make(chan error, 1)

	testNode := v1.Node{
		ObjectMeta: metav1.ObjectMeta{Name: "machine1", UID: types.UID("machine1")},
		Status:     v1.NodeStatus{Capacity: makeResources(10, 20, 32, 20).Capacity, Allocatable: makeAllocatableResources(10, 20, 32, 20)},
	}
	pod := podWithAnnotations("foo", map[string]string{
		podutil.AssumedNodeAnnotationKey:         testNode.Name,
		constraints.HardConstraintsAnnotationKey: "a",
		podutil.PodResourceTypeAnnotationKey:     string(podutil.GuaranteedPod),
		podutil.PodLauncherAnnotationKey:         string(podutil.Kubelet),
	})
	resource := framework.Resource{MilliCPU: 1, Memory: 1}
	pod.Spec.Containers = []v1.Container{{
		Resources: v1.ResourceRequirements{Requests: resource.ResourceList()},
	}}
	pod.Spec.Volumes = append(pod.Spec.Volumes, v1.Volume{
		Name:         "testVol",
		VolumeSource: v1.VolumeSource{PersistentVolumeClaim: &v1.PersistentVolumeClaimVolumeSource{ClaimName: "testPVC"}},
	})
	client := clientsetfake.NewSimpleClientset(&testNode, pod)
	broadcaster := cmdutil.NewEventBroadcasterAdapter(client)
	eventRecorder := broadcaster.NewRecorder(testSchedulerName)
	testPVC := &v1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{Namespace: "foo", Name: "testPVC", Annotations: map[string]string{"pv.kubernetes.io/bind-completed": "yes"}},
		Spec:       v1.PersistentVolumeClaimSpec{VolumeName: "testPV"},
	}
	testPV := &v1.PersistentVolume{
		ObjectMeta: metav1.ObjectMeta{Name: "testPV"},
	}

	stop := make(chan struct{})
	defer close(stop)

	informerFactory := informers.NewSharedInformerFactory(client, 0)
	pvcInformer := informerFactory.Core().V1().PersistentVolumeClaims().Informer()
	pvInformer := informerFactory.Core().V1().PersistentVolumes().Informer()
	informerFactory.Start(stop)
	informerFactory.WaitForCacheSync(stop)
	pvcInformer.GetIndexer().Add(testPVC)
	pvInformer.GetIndexer().Add(testPV)
	crdClient := godelclientfake.NewSimpleClientset()
	crdInformerFactory := crdinformers.NewSharedInformerFactory(crdClient, 0)

	bindingChan := make(chan *v1.Binding, 1)
	client.PrependReactor("create", "pods", func(action clienttesting.Action) (bool, runtime.Object, error) {
		var b *v1.Binding
		if action.GetSubresource() == "binding" {
			b := action.(clienttesting.CreateAction).GetObject().(*v1.Binding)
			bindingChan <- b
		}
		return true, b, nil
	})
	expectPodBind := &v1.Binding{
		ObjectMeta: metav1.ObjectMeta{Name: "foo", Namespace: "foo", UID: types.UID("foo")}, Target: v1.ObjectReference{Kind: "Node", Name: "machine1"},
	}

	var gotAssumedPod *v1.Pod
	pCache := &fakecache.Cache{
		ForgetFunc: func(pod *v1.Pod) {
		},
		AssumeFunc: func(pod *v1.Pod) error {
			gotAssumedPod = pod
			return nil
		},
		IsAssumedPodFunc: func(pod *v1.Pod) bool {
			if pod == nil || gotAssumedPod == nil {
				return false
			}
			return pod.UID == gotAssumedPod.UID
		},
		GetNodeFunc: func(s string) (framework.NodeInfo, error) {
			nInfo := framework.NewNodeInfo()
			nInfo.SetNode(&testNode)
			return nInfo, nil
		},
	}
	pCache.AddNode(&testNode)

	binder := &Binder{
		NextUnit: func() *framework.QueuedUnitInfo {
			return &framework.QueuedUnitInfo{
				ScheduleUnit: &framework.SinglePodUnit{
					Pod: &framework.QueuedPodInfo{Pod: pod},
				},
				InitialAttemptTimestamp: time.Now(),
			}
		},
		Error: func(p *framework.QueuedPodInfo, err error) {
			errChan <- err
		},
		BinderCache: pCache,
		handle: NewFrameworkHandle(
			client, crdClient,
			informerFactory, crdInformerFactory,
			binderOptions{},
			pCache, volumeBindingTimeoutSeconds,
		),
		recorder: eventRecorder,
		pgLister: crdInformerFactory.Scheduling().V1alpha1().PodGroups().Lister(),
	}

	utils.SchedSleep(100 * time.Millisecond)
	binder.CheckAndBindUnit(context.Background())

	// Wait for scheduling to return an error or succeed binding.
	var (
		gotErr  error
		gotBind *v1.Binding
	)
	select {
	case gotErr = <-errChan:
	case gotBind = <-bindingChan:
	case <-time.After(chanTimeout):
		t.Fatalf("did not receive pod binding or error after %v", chanTimeout)
	}

	assert.Equal(t, nil, gotErr)
	if !cmp.Equal(expectPodBind, gotBind) {
		t.Errorf("err \nWANT=%+v,\nGOT=%+v", expectPodBind, gotBind)
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

func makeAllocatableResources(milliCPU, memory, pods, storage int64) v1.ResourceList {
	return v1.ResourceList{
		v1.ResourceCPU:              *resource.NewMilliQuantity(milliCPU, resource.DecimalSI),
		v1.ResourceMemory:           *resource.NewQuantity(memory, resource.BinarySI),
		v1.ResourcePods:             *resource.NewQuantity(pods, resource.DecimalSI),
		v1.ResourceEphemeralStorage: *resource.NewQuantity(storage, resource.BinarySI),
	}
}

func withNodeName(p *v1.Pod, n string) *v1.Pod {
	p.Spec.NodeName = n
	return p
}

func TestPreemptAndResolveSameNodeConflict_Errors(t *testing.T) {
	errChan := make(chan error, 1)
	chanTimeout := 60 * time.Second // Considering the retry mechanism, this value should be longer.
	testNode := v1.Node{
		ObjectMeta: metav1.ObjectMeta{Name: "machine1", UID: types.UID("machine1")},
		Status:     v1.NodeStatus{Capacity: makeResources(10, 20, 32, 20).Capacity, Allocatable: makeAllocatableResources(10, 20, 32, 20)},
	}
	pod := podWithAnnotations("foo", map[string]string{
		constraints.HardConstraintsAnnotationKey: "a",
		podutil.PodResourceTypeAnnotationKey:     string(podutil.GuaranteedPod),
		podutil.PodLauncherAnnotationKey:         string(podutil.Kubelet),
	})
	pod2 := podWithAnnotations("foo2", map[string]string{
		podutil.PodResourceTypeAnnotationKey: string(podutil.GuaranteedPod),
		podutil.PodLauncherAnnotationKey:     string(podutil.Kubelet),
	})
	testpod1 := podWithAnnotations("testpod1", map[string]string{})
	testpod1.Spec.NodeName = testNode.Name
	testpod2 := podWithAnnotations("testpod2", map[string]string{})
	testpod2.Spec.NodeName = testNode.Name
	setResources(testpod2, map[string]string{"cpu": "10"})
	testpod3 := podWithAnnotations("testpod3", map[string]string{})
	testpod3.Spec.NodeName = testNode.Name
	setResources(testpod3, map[string]string{"cpu": "10"})
	testpod4 := podWithAnnotations("testpod4", map[string]string{})
	testpod4.Spec.NodeName = testNode.Name
	setResources(testpod4, map[string]string{"cpu": "10"})
	testpod5 := podWithAnnotations("testpod5", map[string]string{})
	testpod5.Spec.NodeName = testNode.Name
	setResources(testpod5, map[string]string{"cpu": "10"})

	client := clientsetfake.NewSimpleClientset(&testNode)
	stop := make(chan struct{})
	defer close(stop)

	broadcaster := cmdutil.NewEventBroadcasterAdapter(client)
	eventRecorder := broadcaster.NewRecorder(testSchedulerName)
	informerFactory := informers.NewSharedInformerFactory(client, 0)
	informerFactory.Start(stop)
	informerFactory.WaitForCacheSync(stop)
	informerFactory.Core().V1().Pods().Informer().GetIndexer().Add(testpod1)
	informerFactory.Core().V1().Pods().Informer().GetIndexer().Add(testpod2)
	informerFactory.Core().V1().Pods().Informer().GetIndexer().Add(testpod3)
	informerFactory.Core().V1().Pods().Informer().GetIndexer().Add(testpod4)
	informerFactory.Core().V1().Pods().Informer().GetIndexer().Add(testpod5)
	crdClient := godelclientfake.NewSimpleClientset()
	crdInformerFactory := crdinformers.NewSharedInformerFactory(crdClient, 0)

	pCache := godelcache.New(30*time.Second, stop, "binder")

	pCache.AddNode(&testNode)
	pCache.AddPod(testpod1)
	pCache.AddPod(testpod2)
	pCache.AddPod(testpod3)
	pCache.AddPod(testpod4)
	pCache.AddPod(testpod5)

	table := []struct {
		name         string
		podInfo      []*api.QueuedPodInfo
		setNilClient bool
		expectError  bool
	}{
		{
			name:        "podinfo is nil",
			podInfo:     nil,
			expectError: false,
		},
		{
			name: "podinfo.pod.NominatedNode is nil",
			podInfo: []*framework.QueuedPodInfo{
				{
					Pod:                     pod,
					InitialAttemptTimestamp: time.Now(),
					Timestamp:               time.Now(),
				},
			},
			expectError: true,
		},
		{
			name: "assumed node is nil",
			podInfo: []*framework.QueuedPodInfo{
				{
					Pod: podWithAnnotations("foo", map[string]string{
						constraints.HardConstraintsAnnotationKey: "a",
						podutil.PodResourceTypeAnnotationKey:     string(podutil.GuaranteedPod),
						podutil.PodLauncherAnnotationKey:         string(podutil.Kubelet),
						podutil.AssumedNodeAnnotationKey:         "",
					}),
					Timestamp:               time.Now(),
					InitialAttemptTimestamp: time.Now(),
				},
			},
			expectError: true,
		},
		{
			name: "skip pod schedule",
			podInfo: []*framework.QueuedPodInfo{
				{
					Pod:       deletingPod("testpod"),
					Timestamp: time.Now(),
					NominatedNode: &api.NominatedNode{
						NodeName: testNode.Name,
						VictimPods: framework.VictimPods{
							framework.VictimPod{
								Name:      "testpod1",
								Namespace: "testpod1",
								UID:       "testpod1",
							},
						},
					},
					Attempts:                       1,
					InitialAttemptTimestamp:        time.Now(),
					InitialPreemptAttemptTimestamp: time.Now(),
				},
			},
			expectError: false,
		},
		// TODO: combine pod annotationn in binder.framework
		// {
		// 	name: "get framework pod error",
		// 	podInfo: []*framework.QueuedPodInfo{
		// 		{
		// 			Pod: podWithAnnotations("foo", map[string]string{
		// 				podutil.AssumedNodeAnnotationKey:         testNode.Name,
		// 				constraints.HardConstraintsAnnotationKey: "a,,",
		// 				podutil.PodResourceTypeAnnotationKey:     "guaranteed",
		// 				podutil.PodLauncherAnnotationKey:         string(podutil.Kubelet),
		// 			}),
		// 			Timestamp: time.Now(),
		// 			NominatedNode: &api.NominatedNode{
		// 				NodeName: testNode.Name,
		// 				VictimPods: framework.VictimPods{
		// 					framework.VictimPod{
		// 						Name:      "testpod1",
		// 						Namespace: "testpod1",
		// 						UID:       "testpod1",
		// 					},
		// 				},
		// 			},
		// 			Attempts:                1,
		// 			InitialAttemptTimestamp: time.Now(),
		// 		},
		// 	},
		// 	expectQueueInfo: originQueueInfo,
		// 	expectError:     true,
		// },
		{
			name: "init cycle state error",
			podInfo: []*framework.QueuedPodInfo{
				{
					Pod: podWithAnnotations("foo", map[string]string{
						podutil.AssumedNodeAnnotationKey:         testNode.Name,
						podutil.NominatedNodeAnnotationKey:       fmt.Sprintf(`{"node":"fakenode","victims":[{"name": "%s", "namespace": "%s"}]`, testpod1.Name, testpod1.Namespace),
						constraints.HardConstraintsAnnotationKey: "a",
						podutil.PodResourceTypeAnnotationKey:     "X",
						podutil.PodLauncherAnnotationKey:         string(podutil.Kubelet),
					}),
					Timestamp: time.Now(),
					NominatedNode: &api.NominatedNode{
						NodeName: testNode.Name,
						VictimPods: framework.VictimPods{
							framework.VictimPod{
								Name:      "testpod1",
								Namespace: "testpod1",
								UID:       "testpod1",
							},
						},
					},
					Attempts:                1,
					InitialAttemptTimestamp: time.Now(),
				},
			},
			expectError: true,
		},
		{
			name: "Nominated node does not exist",
			podInfo: []*framework.QueuedPodInfo{
				{
					Pod: podWithAnnotations("foo", map[string]string{
						podutil.NominatedNodeAnnotationKey:       fmt.Sprintf(`{"node":"fakenode","victims":[{"name": "%s", "namespace": "%s"}]`, testpod1.Name, testpod1.Namespace),
						constraints.HardConstraintsAnnotationKey: "a",
						podutil.PodResourceTypeAnnotationKey:     "guaranteed",
						podutil.PodLauncherAnnotationKey:         string(podutil.Kubelet),
					}),
					Timestamp: time.Now(),
					NominatedNode: &api.NominatedNode{
						NodeName: "fakenode",
						VictimPods: framework.VictimPods{
							framework.VictimPod{
								Name:      "testpod1",
								Namespace: "testpod1",
								UID:       "testpod1",
							},
						},
					},
					Attempts:                1,
					InitialAttemptTimestamp: time.Now(),
				},
			},
			expectError: true,
		},
		{
			name: "podInfo.pod.InitialAttemptTimestamp has expired",
			podInfo: []*framework.QueuedPodInfo{
				{
					Pod: podWithAnnotations("foo", map[string]string{
						podutil.AssumedNodeAnnotationKey:         testNode.Name,
						podutil.NominatedNodeAnnotationKey:       fmt.Sprintf(`{"node":"fakenode","victims":[{"name": "%s", "namespace": "%s", "uid": "%s"}]`, testpod1.Name, testpod1.Namespace, testpod1.UID),
						constraints.HardConstraintsAnnotationKey: "a",
						podutil.PodResourceTypeAnnotationKey:     "guaranteed",
						podutil.PodLauncherAnnotationKey:         string(podutil.Kubelet),
					}),
					ReservedPod: podWithAnnotations("foo", map[string]string{
						podutil.AssumedNodeAnnotationKey:         testNode.Name,
						podutil.NominatedNodeAnnotationKey:       fmt.Sprintf(`{"node":"fakenode","victims":[{"name": "%s", "namespace": "%s", "uid": "%s"}]`, testpod1.Name, testpod1.Namespace, testpod1.UID),
						constraints.HardConstraintsAnnotationKey: "a",
						podutil.PodResourceTypeAnnotationKey:     "guaranteed",
						podutil.PodLauncherAnnotationKey:         string(podutil.Kubelet),
					}),
					NominatedNode: &api.NominatedNode{
						NodeName: testNode.Name,
						VictimPods: framework.VictimPods{
							framework.VictimPod{
								Name:      "testpod1",
								Namespace: "testpod1",
								UID:       "testpod1",
							},
						},
					},
					Timestamp:                      time.Now(),
					Attempts:                       2,
					InitialAttemptTimestamp:        time.Now().Add(-1500 * time.Second), // >= 600s
					InitialPreemptAttemptTimestamp: time.Now().Add(-1250 * time.Second), // >= 600s
					NewlyAssumedButStillInHandling: true,
				},
			},
			expectError: true,
		},
		{
			name: "handling pod, check victim pods error",
			podInfo: []*framework.QueuedPodInfo{
				{
					Pod: podWithAnnotations("foo1", map[string]string{
						podutil.NominatedNodeAnnotationKey:       fmt.Sprintf(`{"node":"%s","victims":[{"name": "%s", "namespace": "%s", "uid": "%s"}]}`, testNode.Name, testpod1.Name, testpod1.Namespace, testpod1.UID),
						constraints.HardConstraintsAnnotationKey: "a",
						podutil.PodResourceTypeAnnotationKey:     "guaranteed",
						podutil.PodLauncherAnnotationKey:         string(podutil.Kubelet),
					}),
					ReservedPod: withNodeName(podWithAnnotations("foo1", map[string]string{
						podutil.NominatedNodeAnnotationKey:       fmt.Sprintf(`{"node":"%s","victims":[{"name": "%s", "namespace": "%s", "uid": "%s"}]}`, testNode.Name, testpod1.Name, testpod1.Namespace, testpod1.UID),
						constraints.HardConstraintsAnnotationKey: "a",
						podutil.PodResourceTypeAnnotationKey:     "guaranteed",
						podutil.PodLauncherAnnotationKey:         string(podutil.Kubelet),
					}), "node"),
					NominatedNode: &api.NominatedNode{
						NodeName: testNode.Name,
						VictimPods: framework.VictimPods{
							framework.VictimPod{
								Name:      testpod1.Name,
								Namespace: testpod1.Namespace,
								UID:       string(testpod1.UID),
							},
						},
					},
					Timestamp:                      time.Now(),
					Attempts:                       1,
					InitialAttemptTimestamp:        time.Now(),
					InitialPreemptAttemptTimestamp: time.Now(),
					NewlyAssumedButStillInHandling: true,
				},
			},
			expectError: false,
		},
		// TODO: ATTENTION
		// A non-existent pod would be considered a reasonable victim.
		// {
		// 	name: "getVictimPods error for a fake pod",
		// 	podInfo: []*framework.QueuedPodInfo{
		// 		{
		// 			Pod: podWithAnnotations("foo", map[string]string{
		// 				podutil.NominatedNodeAnnotationKey:       fmt.Sprintf(`{"node":"%s","victims":[{"name": "fake pod", "namespace": "%s"}]}`, testNode.Name, testpod1.Namespace),
		// 				constraints.HardConstraintsAnnotationKey: "a",
		// 				podutil.PodResourceTypeAnnotationKey:     "guaranteed",
		// 				podutil.PodLauncherAnnotationKey:         string(podutil.Kubelet),
		// 			}),
		// 			Timestamp: time.Now(),
		// 			NominatedNode: &api.NominatedNode{
		// 				NodeName: testNode.Name,
		// 				VictimPods: framework.VictimPods{
		// 					framework.VictimPod{
		// 						Name:      "fake pod",
		// 						Namespace: testpod1.Namespace,
		// 						UID:       "fake pod",
		// 					},
		// 				},
		// 			},
		// 			Attempts:                       1,
		// 			InitialAttemptTimestamp:        time.Now(),
		// 			NewlyAssumedButStillInHandling: false,
		// 		},
		// 	},
		// 	expectQueueInfo: originQueueInfo,
		// 	expectError:     true,
		// },
		{
			name: "delete pod failed",
			podInfo: []*framework.QueuedPodInfo{
				{
					Pod: pod2,
					NominatedNode: &api.NominatedNode{
						NodeName: testNode.Name,
						VictimPods: framework.VictimPods{
							{
								Name:      testpod4.Name,
								Namespace: testpod4.Namespace,
								UID:       string(testpod4.UID),
							},
							{
								Name:      testpod5.Name,
								Namespace: testpod5.Namespace,
								UID:       string(testpod5.UID),
							},
						},
					},
					Attempts:                       1,
					InitialPreemptAttemptTimestamp: time.Now(),
					NewlyAssumedButStillInHandling: false,
				},
			},
			setNilClient: true,
			expectError:  true,
		},
		{
			name: "delete failed and forget pod",
			podInfo: []*framework.QueuedPodInfo{
				{
					Pod: pod2,
					NominatedNode: &api.NominatedNode{
						NodeName: testNode.Name,
						VictimPods: framework.VictimPods{
							{
								Name:      testpod4.Name,
								Namespace: testpod4.Namespace,
								UID:       string(testpod4.UID),
							},
							{
								Name:      testpod5.Name,
								Namespace: testpod5.Namespace,
								UID:       string(testpod5.UID),
							},
						},
					},
					Attempts:                       1,
					InitialPreemptAttemptTimestamp: time.Now(),
					NewlyAssumedButStillInHandling: false,
				},
				{
					Pod: pod2,
					NominatedNode: &api.NominatedNode{
						NodeName: testNode.Name,
						VictimPods: framework.VictimPods{
							{
								Name:      testpod4.Name,
								Namespace: testpod4.Namespace,
								UID:       string(testpod4.UID),
							},
							{
								Name:      testpod5.Name,
								Namespace: testpod5.Namespace,
								UID:       string(testpod5.UID),
							},
						},
					},
					Attempts:                       1,
					InitialPreemptAttemptTimestamp: time.Now().Add(-1250 * time.Second),
					NewlyAssumedButStillInHandling: true,
				},
			},
			setNilClient: true,
			expectError:  true,
		},
	}

	for _, item := range table {
		t.Run(item.name, func(t *testing.T) {
			popPod := func(podsPtr *[]*framework.QueuedPodInfo) *framework.QueuedPodInfo {
				if podsPtr == nil {
					return nil
				}
				pods := *podsPtr
				if len(pods) == 0 {
					return nil
				}
				gotPod := pods[0]
				pods = pods[1:]
				*podsPtr = pods
				return gotPod
			}
			binder := &Binder{
				NextUnit: func() *framework.QueuedUnitInfo {
					return &framework.QueuedUnitInfo{
						ScheduleUnit: &framework.SinglePodUnit{
							Pod: popPod(&item.podInfo),
						},
						InitialAttemptTimestamp: time.Now(),
					}
				},
				BinderQueue: queue.NewBinderQueue(DefaultUnitQueueSortFunc(), nil, nil),
				Error: func(p *framework.QueuedPodInfo, err error) {
					errChan <- err
				},
				BinderCache: pCache,
				handle: NewFrameworkHandle(
					client, crdClient,
					informerFactory, crdInformerFactory,
					binderOptions{},
					pCache, volumeBindingTimeoutSeconds,
				),
				recorder:   eventRecorder,
				podLister:  informerFactory.Core().V1().Pods().Lister(),
				pgLister:   crdInformerFactory.Scheduling().V1alpha1().PodGroups().Lister(),
				reconciler: NewBinderTaskReconciler(client),
			}
			if item.setNilClient {
				binder.handle.(*frameworkHandleImpl).client = nil
			}

			binder.CheckAndBindUnit(context.Background())
			time.Sleep(1 * time.Second)

			if item.expectError {
				select {
				case <-errChan:
				case <-time.After(chanTimeout):
					t.Fatalf("did not receive pod binding or error after %v", chanTimeout)
				}
			}
		})
	}
}

func TestPreemptAndResolveSameNodeConflict(t *testing.T) {
	var (
		lowPriority  int32 = 90
		highPriority int32 = 100
	)

	chanTimeout := 30 * time.Second
	errChan := make(chan error, 1)

	testNode := v1.Node{
		ObjectMeta: metav1.ObjectMeta{Name: "machine1", UID: types.UID("machine1")},
		Status:     v1.NodeStatus{Capacity: makeResources(10, 20, 32, 20).Capacity, Allocatable: makeAllocatableResources(10, 20, 32, 20)},
	}
	pod := testinghelper.MakePod().Namespace("foo").Name("foo").UID("foo").
		SchedulerName(testSchedulerName).Priority(highPriority).
		Req(map[v1.ResourceName]string{"cpu": "1m", "memory": "1"}).
		Annotation(constraints.HardConstraintsAnnotationKey, "a").
		Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).
		Annotation(podutil.PodLauncherAnnotationKey, string(podutil.Kubelet)).
		Annotation(podutil.NominatedNodeAnnotationKey, "{\"node\":\"machine1\",\"victims\":[{\"name\":\"testpod1\",\"namespace\":\"testpod1\",\"uid\":\"testpod1\"}, {\"name\":\"testpod2\",\"namespace\":\"testpod2\",\"uid\":\"testpod2\"}]}").Obj()
	pod.Spec.Volumes = append(pod.Spec.Volumes, v1.Volume{
		Name:         "testVol",
		VolumeSource: v1.VolumeSource{PersistentVolumeClaim: &v1.PersistentVolumeClaimVolumeSource{ClaimName: "testPVC"}},
	})
	testpod1 := testinghelper.MakePod().Namespace("testpod1").Name("testpod1").UID("testpod1").
		SchedulerName(testSchedulerName).Node(testNode.Name).Priority(lowPriority).PriorityClassName("pc").
		Req(map[v1.ResourceName]string{"cpu": "1m", "memory": "1"}).
		Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).Obj()
	testpod2 := testinghelper.MakePod().Namespace("testpod2").Name("testpod2").UID("testpod2").
		SchedulerName(testSchedulerName).Node(testNode.Name).Priority(lowPriority).PriorityClassName("pc").
		Req(map[v1.ResourceName]string{"cpu": "1m", "memory": "1"}).
		Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).Obj()
	testpod3 := testinghelper.MakePod().Namespace("testpod3").Name("testpod3").UID("testpod3").
		SchedulerName(testSchedulerName).Node(testNode.Name).Priority(lowPriority).PriorityClassName("pc").
		Req(map[v1.ResourceName]string{"cpu": "1m", "memory": "1"}).
		Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).Obj()
	client := clientsetfake.NewSimpleClientset(&testNode, pod, testpod1, testpod2)
	broadcaster := cmdutil.NewEventBroadcasterAdapter(client)
	eventRecorder := broadcaster.NewRecorder(testSchedulerName)
	testPVC := &v1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{Namespace: "foo", Name: "testPVC", Annotations: map[string]string{"pv.kubernetes.io/bind-completed": "yes"}},
		Spec:       v1.PersistentVolumeClaimSpec{VolumeName: "testPV"},
	}
	testPV := &v1.PersistentVolume{
		ObjectMeta: metav1.ObjectMeta{Name: "testPV"},
	}

	stop := make(chan struct{})
	defer close(stop)

	informerFactory := informers.NewSharedInformerFactory(client, 0)
	pvcInformer := informerFactory.Core().V1().PersistentVolumeClaims().Informer()
	pvInformer := informerFactory.Core().V1().PersistentVolumes().Informer()
	informerFactory.Start(stop)
	informerFactory.WaitForCacheSync(stop)
	pvcInformer.GetIndexer().Add(testPVC)
	pvInformer.GetIndexer().Add(testPV)
	informerFactory.Core().V1().Pods().Informer().GetIndexer().Add(testpod1)
	informerFactory.Core().V1().Pods().Informer().GetIndexer().Add(testpod2)
	crdClient := godelclientfake.NewSimpleClientset()
	crdInformerFactory := crdinformers.NewSharedInformerFactory(crdClient, 0)

	bindingChan := make(chan *v1.Binding, 1)
	client.PrependReactor("create", "pods", func(action clienttesting.Action) (bool, runtime.Object, error) {
		var b *v1.Binding
		if action.GetSubresource() == "binding" {
			b := action.(clienttesting.CreateAction).GetObject().(*v1.Binding)
			bindingChan <- b
		}
		return true, b, nil
	})
	expectPodBind := &v1.Binding{
		ObjectMeta: metav1.ObjectMeta{Name: "foo", Namespace: "foo", UID: types.UID("foo")}, Target: v1.ObjectReference{Kind: "Node", Name: "machine1"},
	}

	deletedPodNames := make(sets.String)
	client.PrependReactor("delete", "pods", func(action clienttesting.Action) (bool, runtime.Object, error) {
		deletedPodNames.Insert(action.(clienttesting.DeleteAction).GetName())
		return true, nil, nil
	})
	expectedDeletedPodNames := make(sets.String)
	expectedDeletedPodNames.Insert(testpod1.Name, testpod2.Name)

	pCache := godelcache.New(30*time.Second, stop, "binder")
	pCache.AddNode(&testNode)
	pCache.AddPod(testpod1)
	pCache.AddPod(testpod2)
	pCache.AddPod(testpod3)
	podInfo := &framework.QueuedPodInfo{
		Pod: pod,
		NominatedNode: &api.NominatedNode{
			NodeName: testNode.Name,
			VictimPods: framework.VictimPods{
				framework.VictimPod{
					Name:      testpod1.Name,
					Namespace: testpod1.Namespace,
					UID:       string(testpod1.UID),
				},
				framework.VictimPod{
					Name:      testpod2.Name,
					Namespace: testpod2.Namespace,
					UID:       string(testpod2.UID),
				},
			},
		},
		Timestamp:                      time.Now(),
		InitialAttemptTimestamp:        time.Now(),
		InitialPreemptAttemptTimestamp: time.Now(),
	}

	binder := &Binder{
		NextUnit: func() *framework.QueuedUnitInfo {
			return &framework.QueuedUnitInfo{
				ScheduleUnit:            framework.NewSinglePodUnit(podInfo),
				InitialAttemptTimestamp: time.Now(),
			}
		},
		BinderQueue: queue.NewBinderQueue(DefaultUnitQueueSortFunc(), nil, nil),
		Error: func(p *framework.QueuedPodInfo, err error) {
			errChan <- err
		},
		BinderCache: pCache,
		handle: NewFrameworkHandle(
			client, crdClient,
			informerFactory, crdInformerFactory,
			binderOptions{},
			pCache, volumeBindingTimeoutSeconds,
		),
		recorder:   eventRecorder,
		podLister:  informerFactory.Core().V1().Pods().Lister(),
		pgLister:   crdInformerFactory.Scheduling().V1alpha1().PodGroups().Lister(),
		reconciler: NewBinderTaskReconciler(client),
	}

	utils.SchedSleep(100 * time.Millisecond)
	binder.CheckAndBindUnit(context.Background())
	time.Sleep(100 * time.Millisecond)

	assert.Equal(t, 1, len(binder.BinderQueue.PendingPods()))
	podInfo.NewlyAssumedButStillInHandling = true
	pCache.RemovePod(testpod1)
	pCache.RemovePod(testpod2)
	informerFactory.Core().V1().Pods().Informer().GetIndexer().Delete(testpod1)
	informerFactory.Core().V1().Pods().Informer().GetIndexer().Delete(testpod2)

	utils.SchedSleep(100 * time.Millisecond)
	binder.CheckAndBindUnit(context.Background())

	// Wait for scheduling to return an error or succeed binding.
	var (
		gotErr  error
		gotBind *v1.Binding
	)
	select {
	case gotErr = <-errChan:
	case gotBind = <-bindingChan:
	case <-time.After(chanTimeout):
		t.Fatalf("did not receive pod binding or error after %v", chanTimeout)
	}

	assert.Equal(t, nil, gotErr)
	if !cmp.Equal(expectedDeletedPodNames, deletedPodNames) {
		t.Errorf("err \nWANT=%+v,\nGOT=%+v", expectedDeletedPodNames, deletedPodNames)
	}
	if !cmp.Equal(expectPodBind, gotBind) {
		t.Errorf("err \nWANT=%+v,\nGOT=%+v", expectPodBind, gotBind)
	}
}

/*
func TestCoscheduling(t *testing.T) {
	chanTimeout := 2 * time.Second
	errChan := make(chan error, 1)
	pgName := "testpg"

	testNode := v1.Node{
		ObjectMeta: metav1.ObjectMeta{Name: "machine1", UID: types.UID("machine1")},
		Status:     v1.NodeStatus{Capacity: makeResources(10, 20, 32, 20).Capacity, Allocatable: makeAllocatableResources(10, 20, 32, 20)},
	}
	pod := podWithAnnotations("foo", map[string]string{
		podutil.AssumedNodeAnnotationKey:         testNode.Name,
		constraints.HardConstraintsAnnotationKey: "a",
		podutil.PodResourceTypeAnnotationKey:     string(podutil.GuaranteedPod),
		podutil.PodLauncherAnnotationKey:         string(podutil.Kubelet),
		podutil.PodGroupNameAnnotationKey:        pgName,
	})
	resource := framework.Resource{MilliCPU: 1, Memory: 1}
	pod.Spec.Containers = []v1.Container{{
		Resources: v1.ResourceRequirements{Requests: resource.ResourceList()},
	}}
	pod.Spec.Volumes = append(pod.Spec.Volumes, v1.Volume{
		Name:         "testVol",
		VolumeSource: v1.VolumeSource{PersistentVolumeClaim: &v1.PersistentVolumeClaimVolumeSource{ClaimName: "testPVC"}},
	})
	testpod1 := podWithAnnotations("testpod1", map[string]string{
		podutil.PodGroupNameAnnotationKey: pgName,
	})
	testpod1.Spec.NodeName = testNode.Name
	testpod1.Namespace = pod.Namespace
	testPVC := &v1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{Namespace: "foo", Name: "testPVC", Annotations: map[string]string{"pv.kubernetes.io/bind-completed": "yes"}},
		Spec:       v1.PersistentVolumeClaimSpec{VolumeName: "testPV"},
	}
	testPV := &v1.PersistentVolume{
		ObjectMeta: metav1.ObjectMeta{Name: "testPV"},
	}

	testPodGroup := createPodGroup(pod.Namespace, pgName, 2)

	client := clientsetfake.NewSimpleClientset(&testNode, pod, testpod1)
	broadcaster := cmdutil.NewEventBroadcasterAdapter(client)
	eventRecorder := broadcaster.NewRecorder(testSchedulerName)

	stop := make(chan struct{})
	defer close(stop)

	informerFactory := informers.NewSharedInformerFactory(client, 0)
	pvcInformer := informerFactory.Core().V1().PersistentVolumeClaims().Informer()
	pvInformer := informerFactory.Core().V1().PersistentVolumes().Informer()
	informerFactory.Start(stop)
	informerFactory.WaitForCacheSync(stop)
	pvcInformer.GetIndexer().Add(testPVC)
	pvInformer.GetIndexer().Add(testPV)
	informerFactory.Core().V1().Pods().Informer().GetIndexer().Add(testpod1)
	crdClient := godelclientfake.NewSimpleClientset()
	crdInformerFactory := crdinformers.NewSharedInformerFactory(crdClient, 0)

	bindingChan := make(chan *v1.Binding, 1)
	client.PrependReactor("create", "pods", func(action clienttesting.Action) (bool, runtime.Object, error) {
		var b *v1.Binding
		if action.GetSubresource() == "binding" {
			b := action.(clienttesting.CreateAction).GetObject().(*v1.Binding)
			bindingChan <- b
		}
		return true, b, nil
	})
	expectPodBind := &v1.Binding{
		ObjectMeta: metav1.ObjectMeta{Name: "foo", Namespace: "foo", UID: types.UID("foo")}, Target: v1.ObjectReference{Kind: "Node", Name: "machine1"},
	}

	pCache := godelcache.New(30*time.Second, stop, "binder")
	pCache.AddNode(&testNode)
	pCache.AddPod(testpod1)
	pCache.AddPodGroup(testPodGroup)

	binder := &Binder{
		NextUnit: func() framework.ScheduleUnit {
			return &framework.SinglePodUnit{Pod: &framework.QueuedPodInfo{Pod: pod}}
		},
		BinderQueue: queue.NewBinderQueue(DefaultUnitQueueSortFunc(), nil, nil),
		Error: func(p *framework.QueuedPodInfo, err error) {
			errChan <- err
		},
		BinderCache: pCache,
		client:      client,
		locks:       &locker.Locker{},
		recorder:    eventRecorder,
		basePlugins: NewBasePlugins(),

		informerFactory:    informerFactory,
		crdInformerFactory: crdInformerFactory,
		podLister:          informerFactory.Core().V1().Pods().Lister(),
		pvcLister:          informerFactory.Core().V1().PersistentVolumeClaims().Lister(),
		volumeBinder: scheduling.NewVolumeBinder(
			client,
			informerFactory.Core().V1().Nodes(),
			informerFactory.Storage().V1().CSINodes(),
			informerFactory.Core().V1().PersistentVolumeClaims(),
			informerFactory.Core().V1().PersistentVolumes(),
			informerFactory.Storage().V1().StorageClasses(),
			time.Duration(volumeBindingTimeoutSeconds)*time.Second,
		),
	}
	pluginRegistry := binderframework.NewInTreeRegistry()
	pluginMaps, _ := binderframework.NewPluginsRegistry(pluginRegistry, binder)
	binder.pluginRegistry = pluginMaps

	utils.SchedSleep(100 * time.Millisecond)
	binder.CheckAndBindUnit(context.Background())

	// Wait for scheduling to return an error or succeed binding.
	var (
		gotErr  error
		gotBind *v1.Binding
	)
	select {
	case gotErr = <-errChan:
	case gotBind = <-bindingChan:
	case <-time.After(chanTimeout):
		t.Fatalf("did not receive pod binding or error after %v", chanTimeout)
	}

	assert.Equal(t, nil, gotErr)
	if !cmp.Equal(expectPodBind, gotBind) {
		t.Errorf("err \nWANT=%+v,\nGOT=%+v", expectPodBind, gotBind)
	}
}

func TestCoscheduling_Error_InvalidPodGroup(t *testing.T) {
	chanTimeout := 2 * time.Second
	errChan := make(chan error, 1)

	pgName := "testpg"
	testNode := v1.Node{
		ObjectMeta: metav1.ObjectMeta{Name: "machine1", UID: types.UID("machine1")},
		Status:     v1.NodeStatus{Capacity: makeResources(10, 20, 32, 20).Capacity, Allocatable: makeAllocatableResources(10, 20, 32, 20)},
	}
	pod := podWithAnnotations("foo", map[string]string{
		podutil.AssumedNodeAnnotationKey:         testNode.Name,
		constraints.HardConstraintsAnnotationKey: "a",
		podutil.PodResourceTypeAnnotationKey:     string(podutil.GuaranteedPod),
		podutil.PodLauncherAnnotationKey:         string(podutil.Kubelet),
		podutil.PodGroupNameAnnotationKey:        pgName,
	})
	resource := framework.Resource{MilliCPU: 1, Memory: 1}
	pod.Spec.Containers = []v1.Container{{
		Resources: v1.ResourceRequirements{Requests: resource.ResourceList()},
	}}

	testPodGroup := createPodGroup(pod.Namespace, pgName, -1)
	client := clientsetfake.NewSimpleClientset(&testNode, pod)
	broadcaster := cmdutil.NewEventBroadcasterAdapter(client)
	eventRecorder := broadcaster.NewRecorder(testSchedulerName)

	stop := make(chan struct{})
	defer close(stop)

	informerFactory := informers.NewSharedInformerFactory(client, 0)
	informerFactory.Start(stop)
	informerFactory.WaitForCacheSync(stop)
	crdClient := godelclientfake.NewSimpleClientset()
	crdInformerFactory := crdinformers.NewSharedInformerFactory(crdClient, 0)

	pCache := godelcache.New(30*time.Second, stop, "binder")
	pCache.AddNode(&testNode)
	pCache.AddPodGroup(testPodGroup)

	binder := &Binder{
		NextUnit: func() framework.ScheduleUnit {
			return &framework.SinglePodUnit{Pod: &framework.QueuedPodInfo{Pod: pod}}
		},
		BinderQueue: queue.NewBinderQueue(DefaultUnitQueueSortFunc(), nil, nil),
		Error: func(p *framework.QueuedPodInfo, err error) {
			errChan <- err
		},
		BinderCache: pCache,
		client:      client,
		locks:       &locker.Locker{},
		recorder:    eventRecorder,
		basePlugins: NewBasePlugins(),

		informerFactory:    informerFactory,
		crdInformerFactory: crdInformerFactory,
		podLister:          informerFactory.Core().V1().Pods().Lister(),
		pvcLister:          informerFactory.Core().V1().PersistentVolumeClaims().Lister(),
		volumeBinder: scheduling.NewVolumeBinder(
			client,
			informerFactory.Core().V1().Nodes(),
			informerFactory.Storage().V1().CSINodes(),
			informerFactory.Core().V1().PersistentVolumeClaims(),
			informerFactory.Core().V1().PersistentVolumes(),
			informerFactory.Storage().V1().StorageClasses(),
			time.Duration(volumeBindingTimeoutSeconds)*time.Second,
		),
	}
	pluginRegistry := binderframework.NewInTreeRegistry()
	pluginMaps, _ := binderframework.NewPluginsRegistry(pluginRegistry, binder)
	binder.pluginRegistry = pluginMaps

	binder.CheckAndBindUnit(context.Background())

	// Wait for scheduling to return an error or succeed binding.
	var (
		gotErr error
	)
	select {
	case gotErr = <-errChan:
	case <-time.After(chanTimeout):
		t.Fatalf("did not receive pod binding or error after %v", chanTimeout)
	}

	assert.NotNil(t, gotErr)
}

func TestCoscheduling_Waiting(t *testing.T) {
	chanTimeout := 2 * time.Second
	errChan := make(chan error, 1)

	pgName := "testpg"
	testNode := v1.Node{
		ObjectMeta: metav1.ObjectMeta{Name: "machine1", UID: types.UID("machine1")},
		Status:     v1.NodeStatus{Capacity: makeResources(10, 20, 32, 20).Capacity, Allocatable: makeAllocatableResources(10, 20, 32, 20)},
	}
	pod := podWithAnnotations("foo", map[string]string{
		podutil.AssumedNodeAnnotationKey:         testNode.Name,
		constraints.HardConstraintsAnnotationKey: "a",
		podutil.PodResourceTypeAnnotationKey:     string(podutil.GuaranteedPod),
		podutil.PodLauncherAnnotationKey:         string(podutil.Kubelet),
		podutil.PodGroupNameAnnotationKey:        pgName,
	})
	resource := framework.Resource{MilliCPU: 1, Memory: 1}
	pod.Spec.Containers = []v1.Container{{
		Resources: v1.ResourceRequirements{Requests: resource.ResourceList()},
	}}

	testPodGroup := createPodGroup(pod.Namespace, pgName, 2)
	client := clientsetfake.NewSimpleClientset(&testNode, pod)
	broadcaster := cmdutil.NewEventBroadcasterAdapter(client)
	eventRecorder := broadcaster.NewRecorder(testSchedulerName)

	stop := make(chan struct{})
	defer close(stop)

	informerFactory := informers.NewSharedInformerFactory(client, 0)
	informerFactory.Start(stop)
	informerFactory.WaitForCacheSync(stop)
	crdClient := godelclientfake.NewSimpleClientset()
	crdInformerFactory := crdinformers.NewSharedInformerFactory(crdClient, 0)

	pCache := godelcache.New(30*time.Second, stop, "binder")
	pCache.AddNode(&testNode)
	pCache.AddPodGroup(testPodGroup)

	binder := &Binder{
		NextUnit: func() framework.ScheduleUnit {
			return &framework.SinglePodUnit{Pod: &framework.QueuedPodInfo{Pod: pod}}
		},
		BinderQueue: queue.NewBinderQueue(DefaultUnitQueueSortFunc(), nil, nil),
		Error: func(p *framework.QueuedPodInfo, err error) {
			errChan <- err
		},
		BinderCache: pCache,
		client:      client,
		locks:       &locker.Locker{},
		recorder:    eventRecorder,
		basePlugins: NewBasePlugins(),

		informerFactory:    informerFactory,
		crdInformerFactory: crdInformerFactory,
		podLister:          informerFactory.Core().V1().Pods().Lister(),
		pvcLister:          informerFactory.Core().V1().PersistentVolumeClaims().Lister(),
		volumeBinder: scheduling.NewVolumeBinder(
			client,
			informerFactory.Core().V1().Nodes(),
			informerFactory.Storage().V1().CSINodes(),
			informerFactory.Core().V1().PersistentVolumeClaims(),
			informerFactory.Core().V1().PersistentVolumes(),
			informerFactory.Storage().V1().StorageClasses(),
			time.Duration(volumeBindingTimeoutSeconds)*time.Second,
		),
	}
	pluginRegistry := binderframework.NewInTreeRegistry()
	pluginMaps, _ := binderframework.NewPluginsRegistry(pluginRegistry, binder)
	binder.pluginRegistry = pluginMaps

	binder.CheckAndBindUnit(context.Background())

	// Wait for scheduling to return an error or succeed binding.
	var (
		gotErr error
	)
	waitUntilTimeout := false
	select {
	case gotErr = <-errChan:
	case <-time.After(chanTimeout):
		waitUntilTimeout = true
	}

	assert.Nil(t, gotErr)
	assert.True(t, waitUntilTimeout)
}

func TestCoscheduling_Timeout(t *testing.T) {
	chanTimeout := 30 * time.Second
	errChan := make(chan error, 1)

	pgName := "testpg"
	testNode := v1.Node{
		ObjectMeta: metav1.ObjectMeta{Name: "machine1", UID: types.UID("machine1")},
		Status:     v1.NodeStatus{Capacity: makeResources(10, 20, 32, 20).Capacity, Allocatable: makeAllocatableResources(10, 20, 32, 20)},
	}
	pod := podWithAnnotations("foo", map[string]string{
		podutil.AssumedNodeAnnotationKey:         testNode.Name,
		constraints.HardConstraintsAnnotationKey: "a",
		podutil.PodResourceTypeAnnotationKey:     string(podutil.GuaranteedPod),
		podutil.PodLauncherAnnotationKey:         string(podutil.Kubelet),
		podutil.PodGroupNameAnnotationKey:        pgName,
	})
	resource := framework.Resource{MilliCPU: 1, Memory: 1}
	pod.Spec.Containers = []v1.Container{{
		Resources: v1.ResourceRequirements{Requests: resource.ResourceList()},
	}}

	testPodGroup := createPodGroup(pod.Namespace, pgName, 2)
	timeoutDuration := int32(chanTimeout.Seconds() - 1)
	testPodGroup.Spec.ScheduleTimeoutSeconds = &timeoutDuration
	client := clientsetfake.NewSimpleClientset(&testNode, pod)
	broadcaster := cmdutil.NewEventBroadcasterAdapter(client)
	eventRecorder := broadcaster.NewRecorder(testSchedulerName)

	stop := make(chan struct{})
	defer close(stop)

	informerFactory := informers.NewSharedInformerFactory(client, 0)
	informerFactory.Start(stop)
	informerFactory.WaitForCacheSync(stop)
	crdClient := godelclientfake.NewSimpleClientset()
	crdInformerFactory := crdinformers.NewSharedInformerFactory(crdClient, 0)

	pCache := godelcache.New(30*time.Second, stop, "binder")
	pCache.AddNode(&testNode)
	pCache.AddPodGroup(testPodGroup)

	binder := &Binder{
		NextUnit: func() framework.ScheduleUnit {
			return &framework.SinglePodUnit{Pod: &framework.QueuedPodInfo{Pod: pod}}
		},
		BinderQueue: queue.NewBinderQueue(DefaultUnitQueueSortFunc(), nil, nil),
		Error: func(p *framework.QueuedPodInfo, err error) {
			errChan <- err
		},
		BinderCache: pCache,
		client:      client,
		locks:       &locker.Locker{},
		recorder:    eventRecorder,
		basePlugins: NewBasePlugins(),

		informerFactory:    informerFactory,
		crdInformerFactory: crdInformerFactory,
		podLister:          informerFactory.Core().V1().Pods().Lister(),
		pvcLister:          informerFactory.Core().V1().PersistentVolumeClaims().Lister(),
		volumeBinder: scheduling.NewVolumeBinder(
			client,
			informerFactory.Core().V1().Nodes(),
			informerFactory.Storage().V1().CSINodes(),
			informerFactory.Core().V1().PersistentVolumeClaims(),
			informerFactory.Core().V1().PersistentVolumes(),
			informerFactory.Storage().V1().StorageClasses(),
			time.Duration(volumeBindingTimeoutSeconds)*time.Second,
		),
	}
	pluginRegistry := binderframework.NewInTreeRegistry()
	pluginMaps, _ := binderframework.NewPluginsRegistry(pluginRegistry, binder)
	binder.pluginRegistry = pluginMaps

	binder.CheckAndBindUnit(context.Background())

	// Wait for scheduling to return an error or succeed binding.
	var (
		gotErr error
	)
	select {
	case gotErr = <-errChan:
	case <-time.After(chanTimeout):
		t.Fatalf("did not receive pod binding or error after %v", chanTimeout)
	}

	assert.NotNil(t, gotErr)
}

func createPodGroup(namespace, name string, minMember int32) *schedulingv1a1.PodGroup {
	duration := int32(10)
	return &schedulingv1a1.PodGroup{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
			UID:       types.UID(name),
		},
		Spec: schedulingv1a1.PodGroupSpec{
			MinMember:              minMember,
			ScheduleTimeoutSeconds: &duration,
		},
	}
}
*/

func TestCheckCrossNodePreemptionForUnit(t *testing.T) {
	var (
		largeRes = map[v1.ResourceName]string{
			v1.ResourceCPU:    "500m",
			v1.ResourceMemory: "500",
		}

		highPriority int32 = 100
		lowPriority  int32 = 90
	)
	fakeNode := testinghelper.MakeNode().Name("fakenode").Capacity(largeRes).Obj()
	pg := testinghelper.MakePodGroup().MinMember(1).Obj() // min-member should not be 0
	victim1 := testinghelper.MakePod().Namespace("default").Name("p1").UID("p1").Node("fakenode").
		Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
		Label("name", "p").
		Req(map[v1.ResourceName]string{"cpu": "100m"}).
		Priority(lowPriority).PriorityClassName("sc").Obj()
	victim2 := testinghelper.MakePod().Namespace("default").Name("p2").UID("p2").Node("fakenode").
		Annotation(util.CanBePreemptedAnnotationKey, util.CanBePreempted).
		Label("name", "p").
		Req(map[v1.ResourceName]string{"cpu": "100m"}).
		Priority(lowPriority).PriorityClassName("sc").Obj()

	table := []struct {
		name        string
		podInfo     []*api.QueuedPodInfo
		pdbs        []*policy.PodDisruptionBudget
		existingPod []*v1.Pod
		expectedRes bool
	}{
		{
			name: "victims all could be preempted",
			podInfo: []*framework.QueuedPodInfo{
				{
					Pod: testinghelper.MakePod().Namespace("foo").Name("foo").UID("foo").SchedulerName(testSchedulerName).
						Annotation(podutil.PodResourceTypeAnnotationKey, "guaranteed").
						Annotation(podutil.PodLauncherAnnotationKey, string(podutil.Kubelet)).
						Annotation(podutil.NominatedNodeAnnotationKey, "{\"node\":\"fakenode\",\"victims\":[{\"name\":\"p1\",\"namespace\":\"default\",\"uid\":\"p1\"},{\"name\":\"p2\",\"namespace\":\"default\",\"uid\":\"p2\"}]}"). // only fill node field.
						Priority(highPriority).
						Req(map[v1.ResourceName]string{v1.ResourceCPU: "300m"}).Obj(),
					Timestamp: time.Now(),
					NominatedNode: &api.NominatedNode{
						NodeName: "fakenode",
						VictimPods: framework.VictimPods{
							framework.VictimPod{
								Name:      victim1.Name,
								Namespace: victim1.Namespace,
								UID:       string(victim1.UID),
							},
							framework.VictimPod{
								Name:      victim2.Name,
								Namespace: victim2.Namespace,
								UID:       string(victim2.UID),
							},
						},
					},
					Attempts:                1,
					InitialAttemptTimestamp: time.Now(),
				},
			},
			existingPod: []*v1.Pod{victim1, victim2},
			expectedRes: true,
		},
		{
			name: "one of the pod needs preemption and preempt success",
			podInfo: []*framework.QueuedPodInfo{
				{
					Pod: testinghelper.MakePod().Namespace("foo1").Name("foo1").UID("foo1").SchedulerName(testSchedulerName).
						Annotation(podutil.AssumedNodeAnnotationKey, "fakenode").
						Annotation(podutil.PodResourceTypeAnnotationKey, "guaranteed").
						Annotation(podutil.PodLauncherAnnotationKey, string(podutil.Kubelet)).
						Priority(highPriority).
						Req(map[v1.ResourceName]string{v1.ResourceCPU: "300m"}).Obj(),
					Timestamp:               time.Now(),
					NominatedNode:           nil,
					Attempts:                1,
					InitialAttemptTimestamp: time.Now(),
				},
				{
					Pod: testinghelper.MakePod().Namespace("foo2").Name("foo2").UID("foo2").SchedulerName(testSchedulerName).
						Annotation(podutil.PodResourceTypeAnnotationKey, "guaranteed").
						Annotation(podutil.PodLauncherAnnotationKey, string(podutil.Kubelet)).
						Annotation(podutil.NominatedNodeAnnotationKey, "{\"node\":\"fakenode\",\"victims\":[{\"name\":\"p1\",\"namespace\":\"default\",\"uid\":\"p1\"},{\"name\":\"p2\",\"namespace\":\"default\",\"uid\":\"p2\"}]}"). // only fill node field.
						Priority(highPriority).
						Req(map[v1.ResourceName]string{v1.ResourceCPU: "300m"}).Obj(),
					Timestamp: time.Now(),
					NominatedNode: &api.NominatedNode{
						NodeName: "fakenode",
						VictimPods: framework.VictimPods{
							framework.VictimPod{
								Name:      victim1.Name,
								Namespace: victim1.Namespace,
								UID:       string(victim1.UID),
							},
							framework.VictimPod{
								Name:      victim2.Name,
								Namespace: victim2.Namespace,
								UID:       string(victim2.UID),
							},
						},
					},
					Attempts:                1,
					InitialAttemptTimestamp: time.Now(),
				},
			},
			existingPod: []*v1.Pod{victim1, victim2},
			expectedRes: true,
		},
		{
			name: "one of the pod needs preemption and preempt success, different order",
			podInfo: []*framework.QueuedPodInfo{
				{
					Pod: testinghelper.MakePod().Namespace("foo2").Name("foo2").UID("foo2").SchedulerName(testSchedulerName).
						Annotation(podutil.NominatedNodeAnnotationKey, "{\"node\":\"fakenode\",\"victims\":[{\"name\":\"p1\",\"namespace\":\"default\",\"uid\":\"p1\"},{\"name\":\"p2\",\"namespace\":\"default\",\"uid\":\"p2\"}]}"). // only fill node field.
						Annotation(podutil.PodResourceTypeAnnotationKey, "guaranteed").
						Annotation(podutil.PodLauncherAnnotationKey, string(podutil.Kubelet)).
						Priority(highPriority).
						Req(map[v1.ResourceName]string{v1.ResourceCPU: "300m"}).Obj(),
					Timestamp: time.Now(),
					NominatedNode: &api.NominatedNode{
						NodeName: "fakenode",
						VictimPods: framework.VictimPods{
							framework.VictimPod{
								Name:      victim1.Name,
								Namespace: victim1.Namespace,
								UID:       string(victim1.UID),
							},
							framework.VictimPod{
								Name:      victim2.Name,
								Namespace: victim2.Namespace,
								UID:       string(victim2.UID),
							},
						},
					},
					Attempts:                1,
					InitialAttemptTimestamp: time.Now(),
				},
				{
					Pod: testinghelper.MakePod().Namespace("foo1").Name("foo1").UID("foo1").SchedulerName(testSchedulerName).
						Annotation(podutil.AssumedNodeAnnotationKey, "node").
						Annotation(podutil.PodResourceTypeAnnotationKey, "guaranteed").
						Annotation(podutil.PodLauncherAnnotationKey, string(podutil.Kubelet)).
						Priority(highPriority).
						Req(map[v1.ResourceName]string{v1.ResourceCPU: "300m"}).Obj(),
					Timestamp:               time.Now(),
					NominatedNode:           nil,
					Attempts:                1,
					InitialAttemptTimestamp: time.Now(),
				},
			},
			existingPod: []*v1.Pod{victim1, victim2},
			expectedRes: true,
		},
		{
			name: "victims could not be preempted because of pdb",
			podInfo: []*framework.QueuedPodInfo{
				{
					Pod: testinghelper.MakePod().Namespace("foo").Name("foo").UID("foo").SchedulerName(testSchedulerName).
						Annotation(podutil.PodResourceTypeAnnotationKey, "guaranteed").
						Annotation(podutil.PodLauncherAnnotationKey, string(podutil.Kubelet)).
						Annotation(podutil.NominatedNodeAnnotationKey, "{\"node\":\"fakenode\",\"victims\":[{\"name\":\"p1\",\"namespace\":\"default\",\"uid\":\"p1\"},{\"name\":\"p2\",\"namespace\":\"default\",\"uid\":\"p2\"}]}"). // only fill node field.
						Priority(highPriority).
						Req(map[v1.ResourceName]string{v1.ResourceCPU: "300m"}).Obj(),
					Timestamp: time.Now(),
					NominatedNode: &api.NominatedNode{
						NodeName: "fakenode",
						VictimPods: framework.VictimPods{
							framework.VictimPod{
								Name:      victim1.Name,
								Namespace: victim1.Namespace,
								UID:       string(victim1.UID),
							},
							framework.VictimPod{
								Name:      victim2.Name,
								Namespace: victim2.Namespace,
								UID:       string(victim2.UID),
							},
						},
					},
					Attempts:                1,
					InitialAttemptTimestamp: time.Now(),
				},
			},
			pdbs: []*policy.PodDisruptionBudget{
				testinghelper.MakePdb().Namespace("default").Name("pdb").Label("name", "p").DisruptionsAllowed(1).Obj(),
			},
			existingPod: []*v1.Pod{victim1, victim2},
			expectedRes: false,
		},
	}

	for _, item := range table {
		t.Run(item.name, func(t *testing.T) {
			stop := make(chan struct{})
			defer close(stop)

			client := clientsetfake.NewSimpleClientset(item.existingPod[0], item.existingPod[1])
			crdClient := godelclientfake.NewSimpleClientset()
			informerFactory := informers.NewSharedInformerFactory(client, 0)
			crdInformerFactory := crdinformers.NewSharedInformerFactory(crdClient, 0)
			podInformer := informerFactory.Core().V1().Pods().Informer()
			informerFactory.Start(stop)
			cache.WaitForCacheSync(stop, podInformer.HasSynced)

			pCache := godelcache.New(30*time.Second, stop, "binder")
			pCache.AddNode(fakeNode)
			pCache.AddPod(item.existingPod[0])
			pCache.AddPod(item.existingPod[1])
			for _, pdb := range item.pdbs {
				pCache.AddPDB(pdb)
			}

			pgu := framework.NewPodGroupUnit(pg, 100)
			for _, pInfo := range item.podInfo {
				pgu.AddPod(pInfo)
			}

			eventBroadcaster := events.NewBroadcaster(&events.EventSinkImpl{Interface: client.EventsV1()})
			eventRecorder := eventBroadcaster.NewRecorder(scheme.Scheme, testSchedulerName)

			binder := &Binder{
				NextUnit: func() *framework.QueuedUnitInfo {
					return &framework.QueuedUnitInfo{
						ScheduleUnit:            pgu,
						InitialAttemptTimestamp: time.Now(),
					}
				},
				BinderQueue: queue.NewBinderQueue(DefaultUnitQueueSortFunc(), nil, nil),
				Error:       func(p *framework.QueuedPodInfo, err error) {},
				BinderCache: pCache,
				handle: NewFrameworkHandle(
					client, crdClient,
					informerFactory, crdInformerFactory,
					binderOptions{
						victimCheckingPluginSet: []*framework.VictimCheckingPluginCollectionSpec{
							framework.NewVictimCheckingPluginCollectionSpec([]config.Plugin{{Name: defaultpreemption.PDBCheckerName}}, false, false),
						},
					},
					pCache, volumeBindingTimeoutSeconds,
				),
				recorder:   eventRecorder,
				podLister:  informerFactory.Core().V1().Pods().Lister(),
				pgLister:   crdInformerFactory.Scheduling().V1alpha1().PodGroups().Lister(),
				reconciler: NewBinderTaskReconciler(client),
			}

			unit := &binderutils.BinderUnitInfo{
				QueuedUnitInfo: binder.NextUnit(),
				NodeToVictims:  make(map[string][]*v1.Pod),
			}

			unitInfo := binder.InitializeUnit(&framework.QueuedUnitInfo{ScheduleUnit: unit})

			gotRes := binder.CheckCrossNodePreemptionForUnit(context.TODO(), unitInfo)
			if item.expectedRes != (gotRes == nil) {
				t.Errorf("expected result: %v, but got: %v", item.expectedRes, gotRes)
			}
		})
	}
}
