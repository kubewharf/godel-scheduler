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

package unitscheduler

import (
	"context"
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/kubewharf/godel-scheduler-api/pkg/apis/scheduling/v1alpha1"
	godelclientfake "github.com/kubewharf/godel-scheduler-api/pkg/client/clientset/versioned/fake"
	crdinformers "github.com/kubewharf/godel-scheduler-api/pkg/client/informers/externalversions"
	"github.com/stretchr/testify/assert"
	v1 "k8s.io/api/core/v1"
	policy "k8s.io/api/policy/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/clock"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/client-go/informers"
	clientset "k8s.io/client-go/kubernetes"
	clientsetfake "k8s.io/client-go/kubernetes/fake"
	clienttesting "k8s.io/client-go/testing"

	framework "github.com/kubewharf/godel-scheduler/pkg/framework/api"
	schedulingconfig "github.com/kubewharf/godel-scheduler/pkg/framework/api/config"
	"github.com/kubewharf/godel-scheduler/pkg/framework/utils"
	"github.com/kubewharf/godel-scheduler/pkg/scheduler/apis/config"
	godelcache "github.com/kubewharf/godel-scheduler/pkg/scheduler/cache"
	"github.com/kubewharf/godel-scheduler/pkg/scheduler/cache/handler"
	"github.com/kubewharf/godel-scheduler/pkg/scheduler/core"
	podscheduler "github.com/kubewharf/godel-scheduler/pkg/scheduler/core/pod_scheduler"
	schedulerframework "github.com/kubewharf/godel-scheduler/pkg/scheduler/framework"
	"github.com/kubewharf/godel-scheduler/pkg/scheduler/framework/plugins/nodeaffinity"
	"github.com/kubewharf/godel-scheduler/pkg/scheduler/framework/plugins/noderesources"
	"github.com/kubewharf/godel-scheduler/pkg/scheduler/framework/preemption-plugins/searching/pdbchecker"
	"github.com/kubewharf/godel-scheduler/pkg/scheduler/framework/preemption-plugins/searching/priorityvaluechecker"
	"github.com/kubewharf/godel-scheduler/pkg/scheduler/framework/preemption-plugins/sorting/priority"
	frameworkruntime "github.com/kubewharf/godel-scheduler/pkg/scheduler/framework/runtime"
	fwkruntime "github.com/kubewharf/godel-scheduler/pkg/scheduler/framework/runtime"
	unitruntime "github.com/kubewharf/godel-scheduler/pkg/scheduler/framework/unit_runtime"
	schedulingqueue "github.com/kubewharf/godel-scheduler/pkg/scheduler/queue"
	"github.com/kubewharf/godel-scheduler/pkg/scheduler/reconciler"
	schedulerutil "github.com/kubewharf/godel-scheduler/pkg/scheduler/util"
	testing_helper "github.com/kubewharf/godel-scheduler/pkg/testing-helper"
	"github.com/kubewharf/godel-scheduler/pkg/util"
	cmdutil "github.com/kubewharf/godel-scheduler/pkg/util/cmd"
	"github.com/kubewharf/godel-scheduler/pkg/util/constraints"
	podutil "github.com/kubewharf/godel-scheduler/pkg/util/pod"
)

var (
	disablePodPreemption = false
	testSchedulerSysName = "test-scheduler"
)

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

func podWithAnnotation(pod *v1.Pod, annotations map[string]string) *v1.Pod {
	pod.Annotations = annotations
	return pod
}

const TestPreemptionName string = "TestPreemption"

type mockScheduler struct {
	result        core.PodScheduleResult
	err           error
	basePlugins   *framework.PluginCollection
	nominatedNode string
	victims       *framework.Victims
	errMap        map[string]error
	resultMap     map[string]core.PodScheduleResult
	victimsMap    map[string]*framework.Victims
	preemptErrMap map[string]error
}

func (ms mockScheduler) SnapshotSharedLister() framework.SharedLister {
	return nil
}

func (ms mockScheduler) CanBeRecycle() bool {
	return false
}

func (ms mockScheduler) Close() {
	return
}

// ClientSet returns a kubernetes clientSet.
func (ms mockScheduler) ClientSet() clientset.Interface {
	return nil
}

func (ms mockScheduler) SharedInformerFactory() informers.SharedInformerFactory {
	return nil
}

func (ms mockScheduler) ScheduleInSpecificNodeGroup(_ context.Context, _ framework.SchedulerFramework, _, _, _ *framework.CycleState, pod *v1.Pod, _ framework.NodeGroup, _ *framework.UnitSchedulingRequest, _ framework.NodeToStatusMap) (core.PodScheduleResult, error) {
	res := ms.result
	err := ms.err
	podKey := podutil.GeneratePodKey(pod)
	if _, ok := ms.resultMap[podKey]; ok {
		res = ms.resultMap[podKey]
	}
	if _, ok := ms.errMap[podKey]; ok {
		err = ms.errMap[podKey]
	}

	return res, err
}

func (ms mockScheduler) DisablePreemption() bool {
	return false
}

func (ms mockScheduler) GetPreemptionFrameworkForPod(_ *v1.Pod) framework.SchedulerPreemptionFramework {
	registry := framework.PluginMap{}
	return fwkruntime.NewPreemptionFramework(registry, ms.basePlugins)
}

func (ms mockScheduler) PreemptInSpecificNodeGroup(_ context.Context, _ framework.SchedulerFramework, _ framework.SchedulerPreemptionFramework, _, _, _ *framework.CycleState, pod *v1.Pod, _ framework.NodeGroup, _ framework.NodeToStatusMap, _ *framework.CachedNominatedNodes) (core.PodScheduleResult, error) {
	podKey := podutil.GeneratePodKey(pod)

	err := ms.err
	victims := ms.victims
	if _, ok := ms.preemptErrMap[podKey]; ok {
		err = ms.preemptErrMap[podKey]
	}
	if vs, ok := ms.victimsMap[podKey]; ok {
		victims = vs
	}

	res := core.PodScheduleResult{
		NominatedNode: utils.ConstructNominatedNode(ms.nominatedNode, victims),
		Victims:       victims,
	}
	return res, err
}

func (ms mockScheduler) SetPreemptionFrameworkForPod(pf framework.SchedulerPreemptionFramework) {}

func (ms mockScheduler) retrievePluginsFromPodConstraints(pod *v1.Pod, constraintAnnotationKey string) (*framework.PluginCollection, error) {
	podConstraints, err := schedulingconfig.GetConstraints(pod, constraintAnnotationKey)
	if err != nil {
		return nil, err
	}
	size := len(podConstraints)
	specs := make([]*framework.PluginSpec, size)
	for index, constraint := range podConstraints {
		specs[index] = framework.NewPluginSpecWithWeight(constraint.PluginName, constraint.Weight)
	}
	switch constraintAnnotationKey {
	case constraints.HardConstraintsAnnotationKey:
		return &framework.PluginCollection{
			Filters: specs,
		}, nil
	case constraints.SoftConstraintsAnnotationKey:
		return &framework.PluginCollection{
			Scores: specs,
		}, nil
	default:
		return nil, fmt.Errorf("unsupported constraintType %v", constraintAnnotationKey)
	}
}

func (ms mockScheduler) GetFrameworkForPod(pod *v1.Pod) (framework.SchedulerFramework, error) {
	hardConstraints, err := ms.retrievePluginsFromPodConstraints(pod, constraints.HardConstraintsAnnotationKey)
	if err != nil {
		return nil, err
	}
	softConstraints, err := ms.retrievePluginsFromPodConstraints(pod, constraints.SoftConstraintsAnnotationKey)
	if err != nil {
		return nil, err
	}

	registry := framework.PluginMap{}
	orderedPluginRegistry := schedulerframework.NewOrderedPluginRegistry()
	pluginOrder := schedulerutil.GetListIndex(orderedPluginRegistry)

	recorder := frameworkruntime.NewMetricsRecorder(1000, time.Second, framework.SwitchType(1), framework.DefaultSubCluster, testSchedulerName)
	return frameworkruntime.NewPodFramework(registry, pluginOrder, ms.basePlugins, hardConstraints, softConstraints, recorder)
}

func (gs mockScheduler) SetFrameworkForPod(f framework.SchedulerFramework) {}

func TestSchedulerPreemptionWithErrors(t *testing.T) {
	testNode := v1.Node{ObjectMeta: metav1.ObjectMeta{Name: "machine1", UID: types.UID("machine1")}}

	stop := make(chan struct{})
	defer close(stop)
	// errChan := make(chan error, 1)
	// chanTimeout := 5 * time.Second

	sCache := godelcache.New(handler.MakeCacheHandlerWrapper().
		SchedulerName("").SchedulerType("").SubCluster(framework.DefaultSubCluster).
		TTL(30 * time.Second).Period(10 * time.Second).StopCh(make(<-chan struct{})).
		EnableStore("PreemptionStore").
		Obj())
	snapshot := godelcache.NewEmptySnapshot(handler.MakeCacheHandlerWrapper().
		SubCluster(framework.DefaultSubCluster).SwitchType(framework.DefaultSubClusterSwitchType).
		EnableStore("PreemptionStore").
		Obj())

	sCache.AddNode(&testNode)

	table := []struct {
		name        string
		testPod     *v1.Pod
		expectError bool
	}{
		{
			name: "preemption with asssumed node",
			testPod: podWithAnnotation(podWithID("foo", ""), map[string]string{
				podutil.PodLauncherAnnotationKey:     string(podutil.Kubelet),
				podutil.PodResourceTypeAnnotationKey: string(podutil.GuaranteedPod),
				podutil.AssumedNodeAnnotationKey:     testNode.Name,
			}),
			expectError: true,
		},
		{
			name: "preemption with nominated node",
			testPod: podWithAnnotation(podWithID("foo", ""), map[string]string{
				podutil.PodLauncherAnnotationKey:     string(podutil.Kubelet),
				podutil.PodResourceTypeAnnotationKey: string(podutil.GuaranteedPod),
				podutil.NominatedNodeAnnotationKey:   testNode.Name,
			}),
			expectError: true,
		},
	}
	for _, item := range table {

		client := clientsetfake.NewSimpleClientset(&testNode, item.testPod)
		broadcaster := cmdutil.NewEventBroadcasterAdapter(client)

		informerFactory := informers.NewSharedInformerFactory(client, 0)
		informerFactory.Core().V1().Pods().Informer().GetIndexer().Add(item.testPod)
		informerFactory.Start(stop)
		informerFactory.WaitForCacheSync(stop)

		godelScheduler := mockScheduler{core.PodScheduleResult{}, &framework.FitError{
			Pod:         item.testPod,
			NumAllNodes: 1,
		}, nil, "", nil, nil, nil, nil, nil}

		queue := schedulingqueue.NewSchedulingQueue(sCache,
			informerFactory.Scheduling().V1().PriorityClasses().Lister(),
			nil, nil, false)

		s := &unitScheduler{
			schedulerName:     testSchedulerName,
			switchType:        framework.SwitchType(1),
			subCluster:        "",
			disablePreemption: false,

			client:    client,
			crdClient: nil,

			podLister: informerFactory.Core().V1().Pods().Lister(),
			pgLister:  nil,

			Cache:      sCache,
			Snapshot:   snapshot,
			Queue:      queue,
			Reconciler: reconciler.NewFailedTaskReconciler(nil, nil, sCache, ""),
			Scheduler:  godelScheduler,

			nextUnit: func() *framework.QueuedUnitInfo {
				unit := framework.NewSinglePodUnit(&framework.QueuedPodInfo{Pod: item.testPod})
				return &framework.QueuedUnitInfo{
					UnitKey:            unit.GetKey(),
					ScheduleUnit:       unit,
					QueuePriorityScore: float64(unit.GetPriority()),
				}
			},

			PluginRegistry: nil, // TODO:

			Recorder: broadcaster.NewRecorder(testSchedulerName),
			Clock:    clock.RealClock{},
		}

		// TODO: FIXME we have removed the sched.Error, so we can't get anything from errChan for now
		//
		// if item.expectError {
		// 	select {
		// 	case <-errChan:
		// 	case <-time.After(chanTimeout):
		// 		t.Fatalf("did not receive pod binding or error after %v", chanTimeout)
		// 	}
		// }
		s.Schedule(context.TODO())
		if item.expectError {
			isAssumed, _ := s.Cache.IsAssumedPod(item.testPod)
			assert.Equal(t, false, isAssumed)
			assert.Equal(t, 1, s.Queue.NumUnschedulableUnits())
		}
	}
}

func TestSchedulerPreemptionWithReserveError(t *testing.T) {
	testNode := v1.Node{
		ObjectMeta: metav1.ObjectMeta{Name: "machine1", UID: types.UID("machine1")},
		Status:     v1.NodeStatus{Capacity: makeResources(10, 20, 32, 20).Capacity, Allocatable: makeAllocatableResources(10, 20, 32, 20)},
	}
	testPod1 := podWithAnnotation(podWithID("testpod1", testNode.Name),
		map[string]string{
			podutil.PodLauncherAnnotationKey:     string(podutil.Kubelet),
			podutil.PodResourceTypeAnnotationKey: string(podutil.GuaranteedPod),
			util.CanBePreemptedAnnotationKey:     util.CanBePreempted,
		})
	resource := framework.Resource{MilliCPU: 10, Memory: 10}
	testPod1.Spec.Containers = []v1.Container{{
		Resources: v1.ResourceRequirements{Requests: resource.ResourceList()},
	}}
	lowPriority := int32(2)
	testPod1.Spec.Priority = &lowPriority
	testPod1.Spec.PriorityClassName = "low-priority"

	testpod := podWithAnnotation(podWithID("testpod", ""),
		map[string]string{
			podutil.PodLauncherAnnotationKey:     string(podutil.Kubelet),
			podutil.PodResourceTypeAnnotationKey: string(podutil.GuaranteedPod),
		})
	testpod.Spec.Containers = []v1.Container{{
		Resources: v1.ResourceRequirements{Requests: resource.ResourceList()},
	}}
	highPriority := int32(10)
	testpod.Spec.Priority = &highPriority
	policy := v1.PreemptLowerPriority
	testpod.Spec.PreemptionPolicy = &policy
	testpod.UID = ""

	client := clientsetfake.NewSimpleClientset(&testNode, testpod, testPod1)
	broadcaster := cmdutil.NewEventBroadcasterAdapter(client)

	actualPatchRequests := 0
	client.PrependReactor("patch", "pods", func(action clienttesting.Action) (bool, runtime.Object, error) {
		actualPatchRequests++
		// For this test, we don't care about the result of the patched pod, just that we got the expected
		// patch request, so just returning &v1.Pod{} here is OK because scheduler doesn't use the response.
		return false, &v1.Pod{}, nil
	})

	stop := make(chan struct{})
	defer close(stop)

	informerFactory := informers.NewSharedInformerFactory(client, 0)
	informerFactory.Start(stop)
	informerFactory.WaitForCacheSync(stop)
	informerFactory.Core().V1().Pods().Informer().GetIndexer().Add(testPod1)
	informerFactory.Core().V1().Pods().Informer().GetIndexer().Add(testpod)
	crdClient := godelclientfake.NewSimpleClientset()
	// crdInformerFactory := crdinformers.NewSharedInformerFactory(crdClient, 0)

	sCache := godelcache.New(handler.MakeCacheHandlerWrapper().
		SchedulerName("").SchedulerType("").SubCluster(framework.DefaultSubCluster).
		TTL(30 * time.Second).Period(10 * time.Second).StopCh(make(<-chan struct{})).
		EnableStore("PreemptionStore").
		Obj())
	snapshot := godelcache.NewEmptySnapshot(handler.MakeCacheHandlerWrapper().
		SubCluster(framework.DefaultSubCluster).SwitchType(framework.DefaultSubClusterSwitchType).
		EnableStore("PreemptionStore").
		Obj())
	godelScheduler := mockScheduler{core.PodScheduleResult{
		SuggestedHost: testNode.Name,
	}, nil, nil, "", nil, nil, nil, nil, nil}

	queue := schedulingqueue.NewSchedulingQueue(sCache,
		informerFactory.Scheduling().V1().PriorityClasses().Lister(),
		nil, nil, false)

	s := &unitScheduler{
		schedulerName:     testSchedulerName,
		switchType:        framework.SwitchType(1),
		subCluster:        "",
		disablePreemption: false,

		client:    client,
		crdClient: crdClient,

		podLister: informerFactory.Core().V1().Pods().Lister(),
		pgLister:  nil,

		Cache:      sCache,
		Snapshot:   snapshot,
		Queue:      queue,
		Reconciler: reconciler.NewFailedTaskReconciler(nil, nil, sCache, ""),
		Scheduler:  godelScheduler,

		nextUnit: func() *framework.QueuedUnitInfo {
			unit := framework.NewSinglePodUnit(&framework.QueuedPodInfo{Pod: testpod})
			return &framework.QueuedUnitInfo{
				UnitKey:            unit.GetKey(),
				ScheduleUnit:       unit,
				QueuePriorityScore: float64(unit.GetPriority()),
			}
		},

		PluginRegistry: nil, // TODO:

		Recorder: broadcaster.NewRecorder(testSchedulerName),
		Clock:    clock.RealClock{},
	}
	sCache.AddNode(&testNode)
	sCache.AddPod(testPod1)

	s.Schedule(context.Background())

	time.Sleep(1 * time.Second)
	assert.Equal(t, actualPatchRequests, 1)
}

func TestSchedulerPreemption(t *testing.T) {
	testNode := v1.Node{
		ObjectMeta: metav1.ObjectMeta{Name: "machine1", UID: types.UID("machine1")},
		Status:     v1.NodeStatus{Capacity: makeResources(10, 20, 32, 20).Capacity, Allocatable: makeAllocatableResources(10, 20, 32, 20)},
	}
	testPod1 := podWithAnnotation(podWithID("testpod1", testNode.Name),
		map[string]string{
			podutil.PodLauncherAnnotationKey:     string(podutil.Kubelet),
			podutil.PodResourceTypeAnnotationKey: string(podutil.GuaranteedPod),
			util.CanBePreemptedAnnotationKey:     util.CanBePreempted,
		})
	resource := framework.Resource{MilliCPU: 10, Memory: 10}
	testPod1.Spec.Containers = []v1.Container{{
		Resources: v1.ResourceRequirements{Requests: resource.ResourceList()},
	}}
	lowPriority := int32(2)
	testPod1.Spec.Priority = &lowPriority
	testPod1.Spec.PriorityClassName = "low-priority"

	testpod := podWithAnnotation(podWithID("testpod", ""),
		map[string]string{
			podutil.PodLauncherAnnotationKey:     string(podutil.Kubelet),
			podutil.PodResourceTypeAnnotationKey: string(podutil.GuaranteedPod),
		})
	testpod.Spec.Containers = []v1.Container{{
		Resources: v1.ResourceRequirements{Requests: resource.ResourceList()},
	}}
	highPriority := int32(10)
	testpod.Spec.Priority = &highPriority
	policy := v1.PreemptLowerPriority
	testpod.Spec.PreemptionPolicy = &policy

	client := clientsetfake.NewSimpleClientset(&testNode, testpod, testPod1)
	broadcaster := cmdutil.NewEventBroadcasterAdapter(client)

	actualPatchRequests := 0
	client.PrependReactor("patch", "pods", func(action clienttesting.Action) (bool, runtime.Object, error) {
		actualPatchRequests++
		// For this test, we don't care about the result of the patched pod, just that we got the expected
		// patch request, so just returning &v1.Pod{} here is OK because scheduler doesn't use the response.
		return false, &v1.Pod{}, nil
	})

	stop := make(chan struct{})
	defer close(stop)

	informerFactory := informers.NewSharedInformerFactory(client, 0)
	informerFactory.Core().V1().Pods().Lister()
	informerFactory.Start(stop)
	informerFactory.WaitForCacheSync(stop)
	crdClient := godelclientfake.NewSimpleClientset()
	// crdInformerFactory := crdinformers.NewSharedInformerFactory(crdClient, 0)

	sCache := godelcache.New(handler.MakeCacheHandlerWrapper().
		SchedulerName("").SchedulerType("").SubCluster(framework.DefaultSubCluster).
		TTL(30 * time.Second).Period(10 * time.Second).StopCh(make(<-chan struct{})).
		PodLister(informerFactory.Core().V1().Pods().Lister()).
		EnableStore("PreemptionStore").
		Obj())
	snapshot := godelcache.NewEmptySnapshot(handler.MakeCacheHandlerWrapper().
		SubCluster(framework.DefaultSubCluster).SwitchType(framework.DefaultSubClusterSwitchType).
		PodLister(informerFactory.Core().V1().Pods().Lister()).
		EnableStore("PreemptionStore").
		Obj())
	godelScheduler := mockScheduler{core.PodScheduleResult{}, &framework.FitError{Pod: testpod}, nil, testNode.Name, &framework.Victims{Pods: []*v1.Pod{testPod1}}, nil, nil, nil, map[string]error{podutil.GeneratePodKey(testpod): nil}}

	queue := schedulingqueue.NewSchedulingQueue(sCache,
		informerFactory.Scheduling().V1().PriorityClasses().Lister(),
		nil, nil, false)

	s := &unitScheduler{
		schedulerName:     testSchedulerName,
		switchType:        framework.SwitchType(1),
		subCluster:        "",
		disablePreemption: false,

		client:    client,
		crdClient: crdClient,

		podLister: informerFactory.Core().V1().Pods().Lister(),
		pgLister:  nil,

		Cache:      sCache,
		Snapshot:   snapshot,
		Queue:      queue,
		Reconciler: reconciler.NewFailedTaskReconciler(nil, nil, sCache, ""),
		Scheduler:  godelScheduler,

		nextUnit: func() *framework.QueuedUnitInfo {
			unit := framework.NewSinglePodUnit(&framework.QueuedPodInfo{Pod: testpod})
			return &framework.QueuedUnitInfo{
				UnitKey:            unit.GetKey(),
				ScheduleUnit:       unit,
				QueuePriorityScore: float64(unit.GetPriority()),
			}
		},

		PluginRegistry: nil, // TODO:

		Recorder: broadcaster.NewRecorder(testSchedulerName),
		Clock:    clock.RealClock{},
	}
	sCache.AddNode(&testNode)
	sCache.AddPod(testPod1)

	s.Schedule(context.Background())

	time.Sleep(1 * time.Second)
	// assert.Equal(t, actualPatchRequests, 1)

	isAssumed, err := s.Cache.IsAssumedPod(testpod)
	assert.Equal(t, true, isAssumed)
	assert.NoError(t, err)
}

func TestScheduleUnit(t *testing.T) {
	testNode := &v1.Node{
		ObjectMeta: metav1.ObjectMeta{Name: "machine1", UID: types.UID("machine1")},
		Status:     v1.NodeStatus{Capacity: makeResources(10, 20, 32, 20).Capacity, Allocatable: makeAllocatableResources(10, 20, 32, 20)},
	}

	minMembers := 3
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
	testPodGroupPriorityValue := int32(100)

	var podInfos []*framework.QueuedPodInfo
	for i := 0; i < minMembers; i++ {
		podName := fmt.Sprintf("testpod-%d", i)
		resource := framework.Resource{MilliCPU: 1, Memory: 1}
		testPod := &v1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name:      podName,
				Namespace: namespace,
				UID:       types.UID(podName),
				Annotations: map[string]string{
					podutil.PodLauncherAnnotationKey:     string(podutil.Kubelet),
					podutil.PodResourceTypeAnnotationKey: string(podutil.GuaranteedPod),
					podutil.PodGroupNameAnnotationKey:    podGroupName,
				},
			},
			Spec: v1.PodSpec{
				SchedulerName: testSchedulerName,
				Containers: []v1.Container{{
					Resources: v1.ResourceRequirements{Requests: resource.ResourceList()},
				}},
			},
		}
		podInfos = append(podInfos, &framework.QueuedPodInfo{
			Pod:                     testPod,
			Timestamp:               time.Now(),
			InitialAttemptTimestamp: time.Now(),
			Attempts:                0,
		})
	}

	client := clientsetfake.NewSimpleClientset(testNode)
	broadcaster := cmdutil.NewEventBroadcasterAdapter(client)

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
	crdClient := godelclientfake.NewSimpleClientset()
	crdInformerFactory := crdinformers.NewSharedInformerFactory(crdClient, 0)
	crdInformerFactory.Start(stop)
	crdInformerFactory.WaitForCacheSync(stop)

	sCache := godelcache.New(handler.MakeCacheHandlerWrapper().
		SchedulerName("").SchedulerType("").SubCluster(framework.DefaultSubCluster).
		TTL(30 * time.Second).Period(10 * time.Second).StopCh(make(<-chan struct{})).
		EnableStore("PreemptionStore").
		Obj())
	snapshot := godelcache.NewEmptySnapshot(handler.MakeCacheHandlerWrapper().
		SubCluster(framework.DefaultSubCluster).SwitchType(framework.DefaultSubClusterSwitchType).
		EnableStore("PreemptionStore").
		Obj())

	sCache.AddNode(testNode)
	pgu := framework.NewPodGroupUnit(testPodGroup, testPodGroupPriorityValue)
	pgu.AddPods(podInfos)

	godelScheduler := mockScheduler{result: core.PodScheduleResult{SuggestedHost: testNode.Name, NumberOfEvaluatedNodes: 1, NumberOfFeasibleNodes: 1}}

	queue := schedulingqueue.NewSchedulingQueue(sCache,
		informerFactory.Scheduling().V1().PriorityClasses().Lister(),
		nil, nil, false)

	s := &unitScheduler{
		schedulerName:     testSchedulerName,
		switchType:        framework.SwitchType(1),
		subCluster:        "",
		disablePreemption: false,

		client:    client,
		crdClient: crdClient,

		podLister: informerFactory.Core().V1().Pods().Lister(),
		pgLister:  testing_helper.NewFakePodGroupLister(nil),

		Cache:      sCache,
		Snapshot:   snapshot,
		Queue:      queue,
		Reconciler: reconciler.NewFailedTaskReconciler(nil, nil, sCache, ""),
		Scheduler:  godelScheduler,

		nextUnit: func() *framework.QueuedUnitInfo {
			unit := pgu
			return &framework.QueuedUnitInfo{
				UnitKey:            unit.GetKey(),
				ScheduleUnit:       unit,
				QueuePriorityScore: float64(unit.GetPriority()),
			}
		},

		PluginRegistry: nil, // TODO:

		Recorder: broadcaster.NewRecorder(testSchedulerName),
		Clock:    clock.RealClock{},
	}

	s.Schedule(context.Background())

	// assert.Equal(t, minMembers, actualPatchRequests)
	for _, podInfo := range podInfos {
		isAssumed, err := s.Cache.IsAssumedPod(podInfo.Pod)
		assert.Equal(t, true, isAssumed)
		assert.NoError(t, err)
	}
}

func TestScheduleUnit_PlacementFailure(t *testing.T) {
	testNode := &v1.Node{
		ObjectMeta: metav1.ObjectMeta{Name: "machine1", UID: types.UID("machine1")},
		Status:     v1.NodeStatus{Capacity: makeResources(10, 20, 32, 20).Capacity, Allocatable: makeAllocatableResources(10, 20, 32, 20)},
	}

	minMembers := 3
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
		resource := framework.Resource{MilliCPU: 15, Memory: 10}
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
				SchedulerName: testSchedulerName,
				Containers: []v1.Container{{
					Resources: v1.ResourceRequirements{Requests: resource.ResourceList()},
				}},
				NodeName: "",
			},
		}

		pods = append(pods, testPod)
	}

	client := clientsetfake.NewSimpleClientset(testNode)
	broadcaster := cmdutil.NewEventBroadcasterAdapter(client)

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
	crdClient := godelclientfake.NewSimpleClientset()
	crdInformerFactory := crdinformers.NewSharedInformerFactory(crdClient, 0)
	crdInformerFactory.Start(stop)
	crdInformerFactory.WaitForCacheSync(stop)
	crdInformerFactory.Scheduling().V1alpha1().PodGroups().Informer().GetIndexer().Add(testPodGroup)

	sCache := godelcache.New(handler.MakeCacheHandlerWrapper().
		SchedulerName("").SchedulerType("").SubCluster(framework.DefaultSubCluster).
		TTL(30 * time.Second).Period(10 * time.Second).StopCh(make(<-chan struct{})).
		EnableStore("PreemptionStore").
		Obj())
	snapshot := godelcache.NewEmptySnapshot(handler.MakeCacheHandlerWrapper().
		SubCluster(framework.DefaultSubCluster).SwitchType(framework.DefaultSubClusterSwitchType).
		EnableStore("PreemptionStore").
		Obj())

	sCache.AddNode(testNode)
	sCache.AddPodGroup(testPodGroup)

	queue := schedulingqueue.NewSchedulingQueue(sCache,
		informerFactory.Scheduling().V1().PriorityClasses().Lister(),
		crdInformerFactory.Scheduling().V1alpha1().PodGroups().Lister(), nil, false)

	s := &unitScheduler{
		schedulerName:     testSchedulerName,
		switchType:        framework.SwitchType(1),
		subCluster:        "",
		disablePreemption: false,

		client:    client,
		crdClient: crdClient,

		podLister: informerFactory.Core().V1().Pods().Lister(),
		pgLister:  crdInformerFactory.Scheduling().V1alpha1().PodGroups().Lister(),

		Cache:      sCache,
		Snapshot:   snapshot,
		Queue:      queue,
		Reconciler: reconciler.NewFailedTaskReconciler(nil, nil, sCache, ""),
		Scheduler:  mockScheduler{result: core.PodScheduleResult{}, err: &framework.FitError{Pod: &v1.Pod{}}},

		nextUnit: schedulingqueue.MakeNextUnitFunc(queue),

		PluginRegistry: nil, // TODO:

		Recorder: broadcaster.NewRecorder(testSchedulerName),
		Clock:    clock.RealClock{},
	}

	// 1) No nodes present in the cache
	for _, pod := range pods {
		queue.Add(pod)
	}

	s.Schedule(context.Background())
	for _, pod := range pods {
		isAssumed, err := s.Cache.IsAssumedPod(pod)
		assert.Equal(t, false, isAssumed)
		assert.NoError(t, err)
	}

	// 2) node is present but pods do not fit
	s.Cache.AddNode(testNode)
	for _, pod := range pods {
		queue.Add(pod)
	}

	s.Schedule(context.Background())
	for _, pod := range pods {
		isAssumed, err := s.Cache.IsAssumedPod(pod)
		assert.Equal(t, false, isAssumed)
		assert.NoError(t, err)
	}
}

func TestScheduleUnitInNodeGroup_SinglePod(t *testing.T) {
	tests := []struct {
		name                    string
		pod                     *v1.Pod
		nodes                   []*v1.Node
		existingPods            []*v1.Pod
		disablePreemption       bool
		expectedUnitResult      *core.UnitResult
		expectedPods            sets.String
		expectedRunningUnitInfo *core.RunningUnitInfo
	}{
		{
			name: "[enable preemption] schedule successfully",
			pod: testing_helper.MakePod().Namespace("foo").Name("foo").UID("foo").
				Priority(110).PriorityClassName("pc").
				Req(map[v1.ResourceName]string{v1.ResourceCPU: "5"}).Obj(),
			nodes: []*v1.Node{
				testing_helper.MakeNode().Name("n").Capacity(map[v1.ResourceName]string{v1.ResourceCPU: "10"}).Obj(),
			},
			existingPods: []*v1.Pod{
				testing_helper.MakePod().Namespace("p1").Name("p1").UID("p1").Node("n").
					Priority(100).PriorityClassName("pc").
					Req(map[v1.ResourceName]string{v1.ResourceCPU: "5"}).Obj(),
			},
			expectedUnitResult: &core.UnitResult{
				SuccessfulPods: []string{"foo/foo"},
				FailedPods:     []string{},
			},
			expectedPods: sets.NewString("foo", "p1"),
			expectedRunningUnitInfo: &core.RunningUnitInfo{
				NodeToPlace: "n",
				ClonedPod: testing_helper.MakePod().Namespace("foo").Name("foo").UID("foo").
					Priority(110).PriorityClassName("pc").
					Req(map[v1.ResourceName]string{v1.ResourceCPU: "5"}).
					Annotation(podutil.AssumedNodeAnnotationKey, "n").
					Annotation(podutil.InitialHandledTimestampAnnotationKey, "0001-01-01T00:00:00Z").
					Annotation(podutil.PodStateAnnotationKey, string(podutil.PodAssumed)).
					Annotation(podutil.UnitScheduledIndexAnnotationKey, "0").
					Annotation(podutil.TraceContext, "").Obj(),
			},
		},
		{
			name:              "[disable preemption] schedule failed",
			disablePreemption: true,
			pod: testing_helper.MakePod().Namespace("foo").Name("foo").UID("foo").
				Priority(110).PriorityClassName("pc").
				Req(map[v1.ResourceName]string{v1.ResourceCPU: "10"}).Obj(),
			nodes: []*v1.Node{
				testing_helper.MakeNode().Name("n").Capacity(map[v1.ResourceName]string{v1.ResourceCPU: "10"}).Obj(),
			},
			existingPods: []*v1.Pod{
				testing_helper.MakePod().Namespace("p1").Name("p1").UID("p1").Node("n").
					Priority(100).PriorityClassName("pc").
					Req(map[v1.ResourceName]string{v1.ResourceCPU: "5"}).Obj(),
				testing_helper.MakePod().Namespace("p2").Name("p2").UID("p2").Node("n").
					Priority(100).PriorityClassName("pc").
					Req(map[v1.ResourceName]string{v1.ResourceCPU: "5"}).Obj(),
			},
			expectedUnitResult: &core.UnitResult{
				SuccessfulPods: []string{},
				FailedPods:     []string{"foo/foo"},
			},
			expectedPods: sets.NewString("p1", "p2"),
			expectedRunningUnitInfo: &core.RunningUnitInfo{
				NodeToPlace: "",
				ClonedPod: testing_helper.MakePod().Namespace("foo").Name("foo").UID("foo").
					Priority(110).PriorityClassName("pc").
					Req(map[v1.ResourceName]string{v1.ResourceCPU: "10"}).
					Annotation(podutil.InitialHandledTimestampAnnotationKey, "0001-01-01T00:00:00Z").
					Annotation(podutil.TraceContext, "").Obj(),
			},
		},
		{
			name: "[enable preemption] preempt successfully",
			pod: testing_helper.MakePod().Namespace("foo").Name("foo").UID("foo").
				Priority(100).PriorityClassName("pc").
				Req(map[v1.ResourceName]string{v1.ResourceCPU: "10"}).Obj(),
			nodes: []*v1.Node{
				testing_helper.MakeNode().Name("n").Capacity(map[v1.ResourceName]string{v1.ResourceCPU: "10"}).Obj(),
			},
			existingPods: []*v1.Pod{
				testing_helper.MakePod().Namespace("p1").Name("p1").UID("p1").Node("n").
					Priority(10).PriorityClassName("pc").
					Req(map[v1.ResourceName]string{v1.ResourceCPU: "5"}).Obj(),
			},
			expectedUnitResult: &core.UnitResult{
				SuccessfulPods: []string{"foo/foo"},
				FailedPods:     []string{},
			},
			expectedPods: sets.NewString("foo"),
			expectedRunningUnitInfo: &core.RunningUnitInfo{
				NodeToPlace: "n",
				Victims: &framework.Victims{
					Pods: []*v1.Pod{
						testing_helper.MakePod().Namespace("p1").Name("p1").UID("p1").Node("n").
							Priority(10).PriorityClassName("pc").
							Req(map[v1.ResourceName]string{v1.ResourceCPU: "5"}).Obj(),
					},
					PreemptionState: framework.NewCycleState(),
				},
				ClonedPod: testing_helper.MakePod().Namespace("foo").Name("foo").UID("foo").
					Priority(100).PriorityClassName("pc").
					Req(map[v1.ResourceName]string{v1.ResourceCPU: "10"}).
					Annotation(podutil.NominatedNodeAnnotationKey, `{"node":"n","victims":[{"name":"p1","namespace":"p1","uid":"p1"}]}`).
					Annotation(podutil.InitialHandledTimestampAnnotationKey, "0001-01-01T00:00:00Z").
					Annotation(podutil.PodStateAnnotationKey, string(podutil.PodAssumed)).
					Annotation(podutil.UnitScheduledIndexAnnotationKey, "0").
					Annotation(podutil.TraceContext, "").Obj(),
			},
		},
		{
			name: "[enable preemption] preempt failed",
			pod: testing_helper.MakePod().Namespace("foo").Name("foo").UID("foo").
				Priority(100).PriorityClassName("pc").
				Req(map[v1.ResourceName]string{v1.ResourceCPU: "10"}).Obj(),
			nodes: []*v1.Node{
				testing_helper.MakeNode().Name("n").Capacity(map[v1.ResourceName]string{v1.ResourceCPU: "10"}).Obj(),
			},
			existingPods: []*v1.Pod{
				testing_helper.MakePod().Namespace("p1").Name("p1").UID("p1").Node("n").
					Priority(110).PriorityClassName("pc").
					Req(map[v1.ResourceName]string{v1.ResourceCPU: "5"}).Obj(),
			},
			expectedUnitResult: &core.UnitResult{
				SuccessfulPods: []string{},
				FailedPods:     []string{"foo/foo"},
			},
			expectedRunningUnitInfo: &core.RunningUnitInfo{
				NodeToPlace: "",
				ClonedPod: testing_helper.MakePod().Namespace("foo").Name("foo").UID("foo").
					Priority(100).PriorityClassName("pc").
					Req(map[v1.ResourceName]string{v1.ResourceCPU: "10"}).
					Annotation(podutil.InitialHandledTimestampAnnotationKey, "0001-01-01T00:00:00Z").
					Annotation(podutil.TraceContext, "").Obj(),
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			schedulerName := "scheduler"
			stop := make(chan struct{})
			defer close(stop)

			client := clientsetfake.NewSimpleClientset()
			informerFactory := informers.NewSharedInformerFactory(client, 0)
			broadcaster := cmdutil.NewEventBroadcasterAdapter(client)
			crdClient := godelclientfake.NewSimpleClientset()
			crdInformerFactory := crdinformers.NewSharedInformerFactory(crdClient, 0)

			podInformer := informerFactory.Core().V1().Pods().Informer()
			pcInformer := informerFactory.Scheduling().V1().PriorityClasses().Informer()
			informerFactory.Start(stop)
			informerFactory.WaitForCacheSync(stop)
			podInformer.GetIndexer().Add(tt.pod)
			pc := testing_helper.MakePriorityClass().Name("pc").Obj()
			pcInformer.GetIndexer().Add(pc)

			sCache := godelcache.New(handler.MakeCacheHandlerWrapper().
				SchedulerName("scheduler").SchedulerType(schedulerName).SubCluster(framework.DefaultSubCluster).
				TTL(30 * time.Second).Period(10 * time.Second).StopCh(make(<-chan struct{})).
				EnableStore("PreemptionStore").
				Obj())
			snapshot := godelcache.NewEmptySnapshot(handler.MakeCacheHandlerWrapper().
				SubCluster(framework.DefaultSubCluster).SwitchType(framework.DefaultSubClusterSwitchType).
				PodLister(informerFactory.Core().V1().Pods().Lister()).
				EnableStore("PreemptionStore").
				Obj())

			queue := schedulingqueue.NewSchedulingQueue(sCache, nil, nil, nil, false)

			for _, n := range tt.nodes {
				sCache.AddNode(n)
			}
			for _, p := range tt.existingPods {
				sCache.AddPod(p)
			}
			sCache.UpdateSnapshot(snapshot)

			basePlugins := framework.PluginCollectionSet{
				string(podutil.Kubelet): &framework.PluginCollection{
					Filters: []*framework.PluginSpec{
						framework.NewPluginSpec(noderesources.FitName),
					},
					Scores: []*framework.PluginSpec{
						framework.NewPluginSpec(nodeaffinity.Name),
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
				},
			}
			globalClock := clock.RealClock{}
			podScheduler := podscheduler.NewPodScheduler(
				schedulerName,
				framework.DisableScheduleSwitch,
				"",
				client,
				crdClient,
				informerFactory,
				crdInformerFactory,
				snapshot,
				globalClock,
				tt.disablePreemption,
				config.CandidateSelectPolicyBest,
				[]string{config.BetterPreemptionPolicyDichotomy},
				100,
				100,
				basePlugins,
				nil,
				nil,
			)

			gs := &unitScheduler{
				schedulerName:     testSchedulerName,
				switchType:        framework.SwitchType(1),
				subCluster:        "",
				disablePreemption: tt.disablePreemption,

				client:    nil,
				crdClient: nil,

				podLister: testing_helper.NewFakePodLister(nil),
				pgLister:  testing_helper.NewFakePodGroupLister(nil),

				Cache:      sCache,
				Snapshot:   snapshot,
				Queue:      queue,
				Reconciler: reconciler.NewFailedTaskReconciler(nil, nil, sCache, ""),
				Scheduler:  podScheduler,

				PluginRegistry: nil, // TODO:

				Recorder: broadcaster.NewRecorder(testSchedulerName),
			}

			unit := framework.NewSinglePodUnit(&framework.QueuedPodInfo{Pod: tt.pod})
			queuedUnitInfo := &framework.QueuedUnitInfo{
				UnitKey:            unit.GetKey(),
				ScheduleUnit:       unit,
				QueuePriorityScore: float64(unit.GetPriority()),
			}
			unitInfo, _ := gs.constructSchedulingUnitInfo(context.Background(), queuedUnitInfo)
			unitFramework := unitruntime.NewUnitFramework(gs, gs, gs.PluginRegistry, nil, unitInfo.QueuedUnitInfo)

			lister := framework.NewNodeInfoLister().(*framework.NodeInfoListerImpl)
			for _, n := range tt.nodes {
				lister.AddNodeInfo(snapshot.GetNodeInfo(n.Name))
			}
			nodeCircle := framework.NewNodeCircle("", lister)
			nodeGroup := snapshot.MakeBasicNodeGroup()
			nodeGroup.SetNodeCircles([]framework.NodeCircle{nodeCircle})
			unitResult := gs.scheduleUnitInNodeGroup(context.Background(), unitInfo, unitFramework, nodeGroup)
			// check running unit info
			runningUnitInfo := unitInfo.DispatchedPods[podutil.GetPodKey(tt.pod)]
			runningUnitInfo.QueuedPodInfo = nil
			runningUnitInfo.Trace = nil
			if !reflect.DeepEqual(tt.expectedRunningUnitInfo, runningUnitInfo) {
				t.Errorf("expected %v but got %v", tt.expectedRunningUnitInfo, runningUnitInfo)
			}

			// check unit result
			unitResult.Details = nil
			if !reflect.DeepEqual(tt.expectedUnitResult, unitResult) {
				t.Errorf("expected %v but got %v", tt.expectedUnitResult, unitResult)
			}

			if runningUnitInfo.NodeToPlace != "" {
				// check node info share gpu topology
				nInfo := gs.Snapshot.GetNodeInfo(runningUnitInfo.NodeToPlace)

				// check pods in snapshot
				pods := sets.NewString()
				for _, p := range nInfo.GetPods() {
					pods.Insert(p.Key())
				}
				if !reflect.DeepEqual(tt.expectedPods, pods) {
					t.Errorf("expected %v but got %v", tt.expectedPods, pods)
				}
			}
		})
	}
}

type expectedResult struct {
	expectedUnitResult      *core.UnitResult
	expectedPods            map[string]sets.String
	expectedRunningUnitInfo map[string]*core.RunningUnitInfo
}

func TestScheduleUnitInNodeGroup_PodGroup(t *testing.T) {
	tests := []struct {
		name            string
		podGroup        *v1alpha1.PodGroup
		pods            []*v1.Pod
		nodes           []*v1.Node
		pdbs            []*policy.PodDisruptionBudget
		existingPods    []*v1.Pod
		expectedResults []expectedResult
	}{
		{
			name:     "all need preemption, second pod preempt in same node failed because of pdb",
			podGroup: testing_helper.MakePodGroup().Namespace("default").Name("pg").MinMember(2).Obj(),
			pods: []*v1.Pod{
				testing_helper.MakePod().Namespace("default").Name("foo1").UID("foo1").
					Priority(100).PriorityClassName("pc").
					Req(map[v1.ResourceName]string{"cpu": "5"}).
					Annotation(podutil.PodGroupNameAnnotationKey, "pg").Obj(),
				testing_helper.MakePod().Namespace("default").Name("foo2").UID("foo2").
					Priority(100).PriorityClassName("pc").
					Req(map[v1.ResourceName]string{"cpu": "5"}).
					Annotation(podutil.PodGroupNameAnnotationKey, "pg").Obj(),
			},
			nodes: []*v1.Node{
				testing_helper.MakeNode().Name("n").Capacity(map[v1.ResourceName]string{"cpu": "10"}).Obj(),
			},
			pdbs: []*policy.PodDisruptionBudget{
				testing_helper.MakePdb().Namespace("default").Name("pdb").Label("key", "val").DisruptionsAllowed(1).Obj(),
			},
			existingPods: []*v1.Pod{
				testing_helper.MakePod().Namespace("default").Name("p1").UID("p1").Node("n").
					Priority(90).PriorityClassName("pc").
					Label("key", "val").
					Req(map[v1.ResourceName]string{"cpu": "5"}).Obj(),
				testing_helper.MakePod().Namespace("default").Name("p2").UID("p2").Node("n").
					Priority(80).PriorityClassName("pc").
					Label("key", "val").
					Req(map[v1.ResourceName]string{"cpu": "5"}).Obj(),
			},
			expectedResults: []expectedResult{
				{
					expectedUnitResult: &core.UnitResult{
						SuccessfulPods: []string{"default/foo1"},
						FailedPods:     []string{"default/foo2"},
					},
					expectedPods: map[string]sets.String{
						"n": sets.NewString("foo1", "p1"),
					},
					expectedRunningUnitInfo: map[string]*core.RunningUnitInfo{
						"default/foo1": {
							NodeToPlace: "n",
							Victims: &framework.Victims{
								Pods: []*v1.Pod{
									testing_helper.MakePod().Namespace("default").Name("p2").UID("p2").Node("n").
										Priority(80).PriorityClassName("pc").
										Label("key", "val").
										Req(map[v1.ResourceName]string{"cpu": "5"}).Obj(),
								},
							},
							ClonedPod: testing_helper.MakePod().Namespace("default").Name("foo1").UID("foo1").
								Priority(100).PriorityClassName("pc").
								Req(map[v1.ResourceName]string{"cpu": "5"}).
								Annotation(podutil.PodGroupNameAnnotationKey, "pg").
								Annotation(podutil.NominatedNodeAnnotationKey, `{"node":"n","victims":[{"name":"p2","namespace":"default","uid":"p2"}]}`).
								Annotation(podutil.InitialHandledTimestampAnnotationKey, "0001-01-01T00:00:00Z").
								Annotation(podutil.PodStateAnnotationKey, string(podutil.PodAssumed)).
								Annotation(podutil.UnitScheduledIndexAnnotationKey, "0").
								Annotation(podutil.TraceContext, "").Obj(),
						},
						"default/foo2": {
							ClonedPod: testing_helper.MakePod().Namespace("default").Name("foo2").UID("foo2").
								Priority(100).PriorityClassName("pc").
								Req(map[v1.ResourceName]string{"cpu": "5"}).
								Annotation(podutil.PodGroupNameAnnotationKey, "pg").
								Annotation(podutil.InitialHandledTimestampAnnotationKey, "0001-01-01T00:00:00Z").
								Annotation(podutil.TraceContext, "").Obj(),
						},
					},
				},
				{
					expectedUnitResult: &core.UnitResult{
						SuccessfulPods: []string{"default/foo2"},
						FailedPods:     []string{"default/foo1"},
					},
					expectedPods: map[string]sets.String{
						"n": sets.NewString("foo2", "p1"),
					},
					expectedRunningUnitInfo: map[string]*core.RunningUnitInfo{
						"default/foo2": {
							NodeToPlace: "n",
							Victims: &framework.Victims{
								Pods: []*v1.Pod{
									testing_helper.MakePod().Namespace("default").Name("p2").UID("p2").Node("n").
										Priority(80).PriorityClassName("pc").
										Label("key", "val").
										Req(map[v1.ResourceName]string{"cpu": "5"}).Obj(),
								},
							},
							ClonedPod: testing_helper.MakePod().Namespace("default").Name("foo2").UID("foo2").
								Priority(100).PriorityClassName("pc").
								Req(map[v1.ResourceName]string{"cpu": "5"}).
								Annotation(podutil.PodGroupNameAnnotationKey, "pg").
								Annotation(podutil.NominatedNodeAnnotationKey, `{"node":"n","victims":[{"name":"p2","namespace":"default","uid":"p2"}]}`).
								Annotation(podutil.InitialHandledTimestampAnnotationKey, "0001-01-01T00:00:00Z").
								Annotation(podutil.PodStateAnnotationKey, string(podutil.PodAssumed)).
								Annotation(podutil.UnitScheduledIndexAnnotationKey, "0").
								Annotation(podutil.TraceContext, "").Obj(),
						},
						"default/foo1": {
							ClonedPod: testing_helper.MakePod().Namespace("default").Name("foo1").UID("foo1").
								Priority(100).PriorityClassName("pc").
								Req(map[v1.ResourceName]string{"cpu": "5"}).
								Annotation(podutil.PodGroupNameAnnotationKey, "pg").
								Annotation(podutil.InitialHandledTimestampAnnotationKey, "0001-01-01T00:00:00Z").
								Annotation(podutil.TraceContext, "").Obj(),
						},
					},
				},
			},
		},
		{
			name:     "all need preemption, second pod preempt in different nodes failed because of pdb",
			podGroup: testing_helper.MakePodGroup().Namespace("default").Name("pg").MinMember(2).Obj(),
			pods: []*v1.Pod{
				testing_helper.MakePod().Namespace("default").Name("foo1").UID("foo1").
					Priority(100).PriorityClassName("pc").
					Req(map[v1.ResourceName]string{"cpu": "5"}).
					Annotation(podutil.PodGroupNameAnnotationKey, "pg").Obj(),
				testing_helper.MakePod().Namespace("default").Name("foo2").UID("foo2").
					Priority(100).PriorityClassName("pc").
					Req(map[v1.ResourceName]string{"cpu": "5"}).
					Annotation(podutil.PodGroupNameAnnotationKey, "pg").Obj(),
			},
			nodes: []*v1.Node{
				testing_helper.MakeNode().Name("n1").Capacity(map[v1.ResourceName]string{"cpu": "5"}).Obj(),
				testing_helper.MakeNode().Name("n2").Capacity(map[v1.ResourceName]string{"cpu": "5"}).Obj(),
			},
			pdbs: []*policy.PodDisruptionBudget{
				testing_helper.MakePdb().Namespace("default").Name("pdb").Label("key", "val").DisruptionsAllowed(1).Obj(),
			},
			existingPods: []*v1.Pod{
				testing_helper.MakePod().Namespace("default").Name("p1").UID("p1").Node("n1").
					Priority(90).PriorityClassName("pc").
					Label("key", "val").
					Req(map[v1.ResourceName]string{"cpu": "5"}).Obj(),
				testing_helper.MakePod().Namespace("default").Name("p2").UID("p2").Node("n2").
					Priority(80).PriorityClassName("pc").
					Label("key", "val").
					Req(map[v1.ResourceName]string{"cpu": "5"}).Obj(),
			},
			expectedResults: []expectedResult{
				{
					expectedUnitResult: &core.UnitResult{
						SuccessfulPods: []string{"default/foo1"},
						FailedPods:     []string{"default/foo2"},
					},
					expectedPods: map[string]sets.String{
						"n1": sets.NewString("p1"),
						"n2": sets.NewString("foo1"),
					},
					expectedRunningUnitInfo: map[string]*core.RunningUnitInfo{
						"default/foo1": {
							NodeToPlace: "n2",
							Victims: &framework.Victims{
								Pods: []*v1.Pod{
									testing_helper.MakePod().Namespace("default").Name("p2").UID("p2").Node("n2").
										Priority(80).PriorityClassName("pc").
										Label("key", "val").
										Req(map[v1.ResourceName]string{"cpu": "5"}).Obj(),
								},
							},
							ClonedPod: testing_helper.MakePod().Namespace("default").Name("foo1").UID("foo1").
								Priority(100).PriorityClassName("pc").
								Req(map[v1.ResourceName]string{"cpu": "5"}).
								Annotation(podutil.PodGroupNameAnnotationKey, "pg").
								Annotation(podutil.NominatedNodeAnnotationKey, `{"node":"n2","victims":[{"name":"p2","namespace":"default","uid":"p2"}]}`).
								Annotation(podutil.InitialHandledTimestampAnnotationKey, "0001-01-01T00:00:00Z").
								Annotation(podutil.PodStateAnnotationKey, string(podutil.PodAssumed)).
								Annotation(podutil.UnitScheduledIndexAnnotationKey, "0").
								Annotation(podutil.TraceContext, "").Obj(),
						},
						"default/foo2": {
							ClonedPod: testing_helper.MakePod().Namespace("default").Name("foo2").UID("foo2").
								Priority(100).PriorityClassName("pc").
								Req(map[v1.ResourceName]string{"cpu": "5"}).
								Annotation(podutil.PodGroupNameAnnotationKey, "pg").
								Annotation(podutil.InitialHandledTimestampAnnotationKey, "0001-01-01T00:00:00Z").
								Annotation(podutil.TraceContext, "").Obj(),
						},
					},
				},
				{
					expectedUnitResult: &core.UnitResult{
						SuccessfulPods: []string{"default/foo2"},
						FailedPods:     []string{"default/foo1"},
					},
					expectedPods: map[string]sets.String{
						"n1": sets.NewString("p1"),
						"n2": sets.NewString("foo2"),
					},
					expectedRunningUnitInfo: map[string]*core.RunningUnitInfo{
						"default/foo2": {
							NodeToPlace: "n2",
							Victims: &framework.Victims{
								Pods: []*v1.Pod{
									testing_helper.MakePod().Namespace("default").Name("p2").UID("p2").Node("n2").
										Priority(80).PriorityClassName("pc").
										Label("key", "val").
										Req(map[v1.ResourceName]string{"cpu": "5"}).Obj(),
								},
							},
							ClonedPod: testing_helper.MakePod().Namespace("default").Name("foo2").UID("foo2").
								Priority(100).PriorityClassName("pc").
								Req(map[v1.ResourceName]string{"cpu": "5"}).
								Annotation(podutil.PodGroupNameAnnotationKey, "pg").
								Annotation(podutil.NominatedNodeAnnotationKey, `{"node":"n2","victims":[{"name":"p2","namespace":"default","uid":"p2"}]}`).
								Annotation(podutil.InitialHandledTimestampAnnotationKey, "0001-01-01T00:00:00Z").
								Annotation(podutil.PodStateAnnotationKey, string(podutil.PodAssumed)).
								Annotation(podutil.UnitScheduledIndexAnnotationKey, "0").
								Annotation(podutil.TraceContext, "").Obj(),
						},
						"default/foo1": {
							ClonedPod: testing_helper.MakePod().Namespace("default").Name("foo1").UID("foo1").
								Priority(100).PriorityClassName("pc").
								Req(map[v1.ResourceName]string{"cpu": "5"}).
								Annotation(podutil.PodGroupNameAnnotationKey, "pg").
								Annotation(podutil.InitialHandledTimestampAnnotationKey, "0001-01-01T00:00:00Z").
								Annotation(podutil.TraceContext, "").Obj(),
						},
					},
				},
			},
		},
		{
			name:     "all need preemption, all succeed in same node",
			podGroup: testing_helper.MakePodGroup().Namespace("default").Name("pg").MinMember(2).Obj(),
			pods: []*v1.Pod{
				testing_helper.MakePod().Namespace("default").Name("foo1").UID("foo1").
					Priority(100).PriorityClassName("pc").
					Req(map[v1.ResourceName]string{"cpu": "5"}).
					Annotation(podutil.PodGroupNameAnnotationKey, "pg").Obj(),
				testing_helper.MakePod().Namespace("default").Name("foo2").UID("foo2").
					Priority(100).PriorityClassName("pc").
					Req(map[v1.ResourceName]string{"cpu": "5"}).
					Annotation(podutil.PodGroupNameAnnotationKey, "pg").Obj(),
			},
			nodes: []*v1.Node{
				testing_helper.MakeNode().Name("n").Capacity(map[v1.ResourceName]string{"cpu": "10"}).Obj(),
			},
			existingPods: []*v1.Pod{
				testing_helper.MakePod().Namespace("default").Name("p1").UID("p1").Node("n").
					Priority(90).PriorityClassName("pc").
					Req(map[v1.ResourceName]string{"cpu": "5"}).Obj(),
				testing_helper.MakePod().Namespace("default").Name("p2").UID("p2").Node("n").
					Priority(80).PriorityClassName("pc").
					Req(map[v1.ResourceName]string{"cpu": "5"}).Obj(),
			},
			expectedResults: []expectedResult{
				{
					expectedUnitResult: &core.UnitResult{
						SuccessfulPods: []string{"default/foo1", "default/foo2"},
						FailedPods:     []string{},
					},
					expectedPods: map[string]sets.String{
						"n": sets.NewString("foo1", "foo2"),
					},
					expectedRunningUnitInfo: map[string]*core.RunningUnitInfo{
						"default/foo1": {
							NodeToPlace: "n",
							Victims: &framework.Victims{
								Pods: []*v1.Pod{
									testing_helper.MakePod().Namespace("default").Name("p2").UID("p2").Node("n").
										Priority(80).PriorityClassName("pc").
										Req(map[v1.ResourceName]string{"cpu": "5"}).Obj(),
								},
							},
							ClonedPod: testing_helper.MakePod().Namespace("default").Name("foo1").UID("foo1").
								Priority(100).PriorityClassName("pc").
								Req(map[v1.ResourceName]string{"cpu": "5"}).
								Annotation(podutil.PodGroupNameAnnotationKey, "pg").
								Annotation(podutil.NominatedNodeAnnotationKey, `{"node":"n","victims":[{"name":"p2","namespace":"default","uid":"p2"}]}`).
								Annotation(podutil.InitialHandledTimestampAnnotationKey, "0001-01-01T00:00:00Z").
								Annotation(podutil.PodStateAnnotationKey, string(podutil.PodAssumed)).
								Annotation(podutil.UnitScheduledIndexAnnotationKey, "0").
								Annotation(podutil.TraceContext, "").Obj(),
						},
						"default/foo2": {
							NodeToPlace: "n",
							Victims: &framework.Victims{
								Pods: []*v1.Pod{
									testing_helper.MakePod().Namespace("default").Name("p1").UID("p1").Node("n").
										Priority(90).PriorityClassName("pc").
										Req(map[v1.ResourceName]string{"cpu": "5"}).Obj(),
								},
							},
							ClonedPod: testing_helper.MakePod().Namespace("default").Name("foo2").UID("foo2").
								Priority(100).PriorityClassName("pc").
								Req(map[v1.ResourceName]string{"cpu": "5"}).
								Annotation(podutil.PodGroupNameAnnotationKey, "pg").
								Annotation(podutil.NominatedNodeAnnotationKey, `{"node":"n","victims":[{"name":"p1","namespace":"default","uid":"p1"}]}`).
								Annotation(podutil.InitialHandledTimestampAnnotationKey, "0001-01-01T00:00:00Z").
								Annotation(podutil.PodStateAnnotationKey, string(podutil.PodAssumed)).
								Annotation(podutil.UnitScheduledIndexAnnotationKey, "1").
								Annotation(podutil.TraceContext, "").Obj(),
						},
					},
				},
				{
					expectedUnitResult: &core.UnitResult{
						SuccessfulPods: []string{"default/foo2", "default/foo1"},
						FailedPods:     []string{},
					},
					expectedPods: map[string]sets.String{
						"n": sets.NewString("foo2", "foo1"),
					},
					expectedRunningUnitInfo: map[string]*core.RunningUnitInfo{
						"default/foo2": {
							NodeToPlace: "n",
							Victims: &framework.Victims{
								Pods: []*v1.Pod{
									testing_helper.MakePod().Namespace("default").Name("p2").UID("p2").Node("n").
										Priority(80).PriorityClassName("pc").
										Req(map[v1.ResourceName]string{"cpu": "5"}).Obj(),
								},
							},
							ClonedPod: testing_helper.MakePod().Namespace("default").Name("foo2").UID("foo2").
								Priority(100).PriorityClassName("pc").
								Req(map[v1.ResourceName]string{"cpu": "5"}).
								Annotation(podutil.PodGroupNameAnnotationKey, "pg").
								Annotation(podutil.NominatedNodeAnnotationKey, `{"node":"n","victims":[{"name":"p2","namespace":"default","uid":"p2"}]}`).
								Annotation(podutil.InitialHandledTimestampAnnotationKey, "0001-01-01T00:00:00Z").
								Annotation(podutil.PodStateAnnotationKey, string(podutil.PodAssumed)).
								Annotation(podutil.UnitScheduledIndexAnnotationKey, "0").
								Annotation(podutil.TraceContext, "").Obj(),
						},
						"default/foo1": {
							NodeToPlace: "n",
							Victims: &framework.Victims{
								Pods: []*v1.Pod{
									testing_helper.MakePod().Namespace("default").Name("p1").UID("p1").Node("n").
										Priority(90).PriorityClassName("pc").
										Req(map[v1.ResourceName]string{"cpu": "5"}).Obj(),
								},
							},
							ClonedPod: testing_helper.MakePod().Namespace("default").Name("foo1").UID("foo1").
								Priority(100).PriorityClassName("pc").
								Req(map[v1.ResourceName]string{"cpu": "5"}).
								Annotation(podutil.PodGroupNameAnnotationKey, "pg").
								Annotation(podutil.NominatedNodeAnnotationKey, `{"node":"n","victims":[{"name":"p1","namespace":"default","uid":"p1"}]}`).
								Annotation(podutil.InitialHandledTimestampAnnotationKey, "0001-01-01T00:00:00Z").
								Annotation(podutil.PodStateAnnotationKey, string(podutil.PodAssumed)).
								Annotation(podutil.UnitScheduledIndexAnnotationKey, "1").
								Annotation(podutil.TraceContext, "").Obj(),
						},
					},
				},
			},
		},
		{
			name:     "all need preemption, all succeed in different node",
			podGroup: testing_helper.MakePodGroup().Namespace("default").Name("pg").MinMember(2).Obj(),
			pods: []*v1.Pod{
				testing_helper.MakePod().Namespace("default").Name("foo1").UID("foo1").
					Priority(100).PriorityClassName("pc").
					Req(map[v1.ResourceName]string{"cpu": "5"}).
					Annotation(podutil.PodGroupNameAnnotationKey, "pg").Obj(),
				testing_helper.MakePod().Namespace("default").Name("foo2").UID("foo2").
					Priority(100).PriorityClassName("pc").
					Req(map[v1.ResourceName]string{"cpu": "5"}).
					Annotation(podutil.PodGroupNameAnnotationKey, "pg").Obj(),
			},
			nodes: []*v1.Node{
				testing_helper.MakeNode().Name("n1").Capacity(map[v1.ResourceName]string{"cpu": "5"}).Obj(),
				testing_helper.MakeNode().Name("n2").Capacity(map[v1.ResourceName]string{"cpu": "5"}).Obj(),
			},
			existingPods: []*v1.Pod{
				testing_helper.MakePod().Namespace("default").Name("p1").UID("p1").Node("n1").
					Priority(90).PriorityClassName("pc").
					Req(map[v1.ResourceName]string{"cpu": "5"}).Obj(),
				testing_helper.MakePod().Namespace("default").Name("p2").UID("p2").Node("n2").
					Priority(80).PriorityClassName("pc").
					Req(map[v1.ResourceName]string{"cpu": "5"}).Obj(),
			},
			expectedResults: []expectedResult{
				{
					expectedUnitResult: &core.UnitResult{
						SuccessfulPods: []string{"default/foo1", "default/foo2"},
						FailedPods:     []string{},
					},
					expectedPods: map[string]sets.String{
						"n1": sets.NewString("foo2"),
						"n2": sets.NewString("foo1"),
					},
					expectedRunningUnitInfo: map[string]*core.RunningUnitInfo{
						"default/foo1": {
							NodeToPlace: "n2",
							Victims: &framework.Victims{
								Pods: []*v1.Pod{
									testing_helper.MakePod().Namespace("default").Name("p2").UID("p2").Node("n2").
										Priority(80).PriorityClassName("pc").
										Req(map[v1.ResourceName]string{"cpu": "5"}).Obj(),
								},
							},
							ClonedPod: testing_helper.MakePod().Namespace("default").Name("foo1").UID("foo1").
								Priority(100).PriorityClassName("pc").
								Req(map[v1.ResourceName]string{"cpu": "5"}).
								Annotation(podutil.PodGroupNameAnnotationKey, "pg").
								Annotation(podutil.NominatedNodeAnnotationKey, `{"node":"n2","victims":[{"name":"p2","namespace":"default","uid":"p2"}]}`).
								Annotation(podutil.InitialHandledTimestampAnnotationKey, "0001-01-01T00:00:00Z").
								Annotation(podutil.PodStateAnnotationKey, string(podutil.PodAssumed)).
								Annotation(podutil.UnitScheduledIndexAnnotationKey, "0").
								Annotation(podutil.TraceContext, "").Obj(),
						},
						"default/foo2": {
							NodeToPlace: "n1",
							Victims: &framework.Victims{
								Pods: []*v1.Pod{
									testing_helper.MakePod().Namespace("default").Name("p1").UID("p1").Node("n1").
										Priority(90).PriorityClassName("pc").
										Req(map[v1.ResourceName]string{"cpu": "5"}).Obj(),
								},
							},
							ClonedPod: testing_helper.MakePod().Namespace("default").Name("foo2").UID("foo2").
								Priority(100).PriorityClassName("pc").
								Req(map[v1.ResourceName]string{"cpu": "5"}).
								Annotation(podutil.PodGroupNameAnnotationKey, "pg").
								Annotation(podutil.NominatedNodeAnnotationKey, `{"node":"n1","victims":[{"name":"p1","namespace":"default","uid":"p1"}]}`).
								Annotation(podutil.InitialHandledTimestampAnnotationKey, "0001-01-01T00:00:00Z").
								Annotation(podutil.PodStateAnnotationKey, string(podutil.PodAssumed)).
								Annotation(podutil.UnitScheduledIndexAnnotationKey, "1").
								Annotation(podutil.TraceContext, "").Obj(),
						},
					},
				},
				{
					expectedUnitResult: &core.UnitResult{
						SuccessfulPods: []string{"default/foo2", "default/foo1"},
						FailedPods:     []string{},
					},
					expectedPods: map[string]sets.String{
						"n1": sets.NewString("foo1"),
						"n2": sets.NewString("foo2"),
					},
					expectedRunningUnitInfo: map[string]*core.RunningUnitInfo{
						"default/foo2": {
							NodeToPlace: "n2",
							Victims: &framework.Victims{
								Pods: []*v1.Pod{
									testing_helper.MakePod().Namespace("default").Name("p2").UID("p2").Node("n2").
										Priority(80).PriorityClassName("pc").
										Req(map[v1.ResourceName]string{"cpu": "5"}).Obj(),
								},
							},
							ClonedPod: testing_helper.MakePod().Namespace("default").Name("foo2").UID("foo2").
								Priority(100).PriorityClassName("pc").
								Req(map[v1.ResourceName]string{"cpu": "5"}).
								Annotation(podutil.PodGroupNameAnnotationKey, "pg").
								Annotation(podutil.NominatedNodeAnnotationKey, `{"node":"n2","victims":[{"name":"p2","namespace":"default","uid":"p2"}]}`).
								Annotation(podutil.InitialHandledTimestampAnnotationKey, "0001-01-01T00:00:00Z").
								Annotation(podutil.PodStateAnnotationKey, string(podutil.PodAssumed)).
								Annotation(podutil.UnitScheduledIndexAnnotationKey, "0").
								Annotation(podutil.TraceContext, "").Obj(),
						},
						"default/foo1": {
							NodeToPlace: "n1",
							Victims: &framework.Victims{
								Pods: []*v1.Pod{
									testing_helper.MakePod().Namespace("default").Name("p1").UID("p1").Node("n1").
										Priority(90).PriorityClassName("pc").
										Req(map[v1.ResourceName]string{"cpu": "5"}).Obj(),
								},
							},
							ClonedPod: testing_helper.MakePod().Namespace("default").Name("foo1").UID("foo1").
								Priority(100).PriorityClassName("pc").
								Req(map[v1.ResourceName]string{"cpu": "5"}).
								Annotation(podutil.PodGroupNameAnnotationKey, "pg").
								Annotation(podutil.NominatedNodeAnnotationKey, `{"node":"n1","victims":[{"name":"p1","namespace":"default","uid":"p1"}]}`).
								Annotation(podutil.InitialHandledTimestampAnnotationKey, "0001-01-01T00:00:00Z").
								Annotation(podutil.PodStateAnnotationKey, string(podutil.PodAssumed)).
								Annotation(podutil.UnitScheduledIndexAnnotationKey, "1").
								Annotation(podutil.TraceContext, "").Obj(),
						},
					},
				},
			},
		},
		{
			name:     "all need preemption, second pod use cached nominated nodes, fail because of pdb",
			podGroup: testing_helper.MakePodGroup().Namespace("default").Name("pg").MinMember(2).Obj(),
			pods: []*v1.Pod{
				testing_helper.MakePod().Namespace("default").Name("foo1").UID("foo1").
					ControllerRef(metav1.OwnerReference{
						Kind: "ReplicaSet",
						Name: "rs",
						UID:  "rs",
					}).
					Priority(100).PriorityClassName("pc").
					Req(map[v1.ResourceName]string{"cpu": "5"}).
					Annotation(podutil.PodGroupNameAnnotationKey, "pg").Obj(),
				testing_helper.MakePod().Namespace("default").Name("foo2").UID("foo2").
					ControllerRef(metav1.OwnerReference{
						Kind: "ReplicaSet",
						Name: "rs",
						UID:  "rs",
					}).
					Priority(100).PriorityClassName("pc").
					Req(map[v1.ResourceName]string{"cpu": "5"}).
					Annotation(podutil.PodGroupNameAnnotationKey, "pg").Obj(),
			},
			nodes: []*v1.Node{
				testing_helper.MakeNode().Name("n1").Capacity(map[v1.ResourceName]string{"cpu": "5"}).Obj(),
				testing_helper.MakeNode().Name("n2").Capacity(map[v1.ResourceName]string{"cpu": "5"}).Obj(),
			},
			pdbs: []*policy.PodDisruptionBudget{
				testing_helper.MakePdb().Namespace("default").Name("pdb").Label("key", "val").DisruptionsAllowed(1).Obj(),
			},
			existingPods: []*v1.Pod{
				testing_helper.MakePod().Namespace("default").Name("p1").UID("p1").Node("n1").
					Priority(90).PriorityClassName("pc").
					Label("key", "val").
					Req(map[v1.ResourceName]string{"cpu": "5"}).Obj(),
				testing_helper.MakePod().Namespace("default").Name("p2").UID("p2").Node("n2").
					Priority(80).PriorityClassName("pc").
					Label("key", "val").
					Req(map[v1.ResourceName]string{"cpu": "5"}).Obj(),
			},
			expectedResults: []expectedResult{
				{
					expectedUnitResult: &core.UnitResult{
						SuccessfulPods: []string{"default/foo1"},
						FailedPods:     []string{"default/foo2"},
					},
					expectedPods: map[string]sets.String{
						"n1": sets.NewString("p1"),
						"n2": sets.NewString("foo1"),
					},
					expectedRunningUnitInfo: map[string]*core.RunningUnitInfo{
						"default/foo1": {
							NodeToPlace: "n2",
							Victims: &framework.Victims{
								Pods: []*v1.Pod{
									testing_helper.MakePod().Namespace("default").Name("p2").UID("p2").Node("n2").
										Priority(80).PriorityClassName("pc").
										Label("key", "val").
										Req(map[v1.ResourceName]string{"cpu": "5"}).Obj(),
								},
							},
							ClonedPod: testing_helper.MakePod().Namespace("default").Name("foo1").UID("foo1").
								ControllerRef(metav1.OwnerReference{
									Kind: "ReplicaSet",
									Name: "rs",
									UID:  "rs",
								}).
								Priority(100).PriorityClassName("pc").
								Req(map[v1.ResourceName]string{"cpu": "5"}).
								Annotation(podutil.PodGroupNameAnnotationKey, "pg").
								Annotation(podutil.NominatedNodeAnnotationKey, `{"node":"n2","victims":[{"name":"p2","namespace":"default","uid":"p2"}]}`).
								Annotation(podutil.InitialHandledTimestampAnnotationKey, "0001-01-01T00:00:00Z").
								Annotation(podutil.PodStateAnnotationKey, string(podutil.PodAssumed)).
								Annotation(podutil.UnitScheduledIndexAnnotationKey, "0").
								Annotation(podutil.TraceContext, "").Obj(),
						},
						"default/foo2": {
							ClonedPod: testing_helper.MakePod().Namespace("default").Name("foo2").UID("foo2").
								ControllerRef(metav1.OwnerReference{
									Kind: "ReplicaSet",
									Name: "rs",
									UID:  "rs",
								}).
								Priority(100).PriorityClassName("pc").
								Req(map[v1.ResourceName]string{"cpu": "5"}).
								Annotation(podutil.PodGroupNameAnnotationKey, "pg").
								Annotation(podutil.InitialHandledTimestampAnnotationKey, "0001-01-01T00:00:00Z").
								Annotation(podutil.TraceContext, "").Obj(),
						},
					},
				},
				{
					expectedUnitResult: &core.UnitResult{
						SuccessfulPods: []string{"default/foo2"},
						FailedPods:     []string{"default/foo1"},
					},
					expectedPods: map[string]sets.String{
						"n1": sets.NewString("p1"),
						"n2": sets.NewString("foo2"),
					},
					expectedRunningUnitInfo: map[string]*core.RunningUnitInfo{
						"default/foo2": {
							NodeToPlace: "n2",
							Victims: &framework.Victims{
								Pods: []*v1.Pod{
									testing_helper.MakePod().Namespace("default").Name("p2").UID("p2").Node("n2").
										Priority(80).PriorityClassName("pc").
										Label("key", "val").
										Req(map[v1.ResourceName]string{"cpu": "5"}).Obj(),
								},
							},
							ClonedPod: testing_helper.MakePod().Namespace("default").Name("foo2").UID("foo2").
								ControllerRef(metav1.OwnerReference{
									Kind: "ReplicaSet",
									Name: "rs",
									UID:  "rs",
								}).
								Priority(100).PriorityClassName("pc").
								Req(map[v1.ResourceName]string{"cpu": "5"}).
								Annotation(podutil.PodGroupNameAnnotationKey, "pg").
								Annotation(podutil.NominatedNodeAnnotationKey, `{"node":"n2","victims":[{"name":"p2","namespace":"default","uid":"p2"}]}`).
								Annotation(podutil.InitialHandledTimestampAnnotationKey, "0001-01-01T00:00:00Z").
								Annotation(podutil.PodStateAnnotationKey, string(podutil.PodAssumed)).
								Annotation(podutil.UnitScheduledIndexAnnotationKey, "0").
								Annotation(podutil.TraceContext, "").Obj(),
						},
						"default/foo1": {
							ClonedPod: testing_helper.MakePod().Namespace("default").Name("foo1").UID("foo1").
								ControllerRef(metav1.OwnerReference{
									Kind: "ReplicaSet",
									Name: "rs",
									UID:  "rs",
								}).
								Priority(100).PriorityClassName("pc").
								Req(map[v1.ResourceName]string{"cpu": "5"}).
								Annotation(podutil.PodGroupNameAnnotationKey, "pg").
								Annotation(podutil.InitialHandledTimestampAnnotationKey, "0001-01-01T00:00:00Z").
								Annotation(podutil.TraceContext, "").Obj(),
						},
					},
				},
			},
		},
		{
			name:     "all need preemption, second pod use cached nominated nodes, all succeed",
			podGroup: testing_helper.MakePodGroup().Namespace("default").Name("pg").MinMember(2).Obj(),
			pods: []*v1.Pod{
				testing_helper.MakePod().Namespace("default").Name("foo1").UID("foo1").
					ControllerRef(metav1.OwnerReference{
						Kind: "ReplicaSet",
						Name: "rs",
						UID:  "rs",
					}).
					Priority(100).PriorityClassName("pc").
					Req(map[v1.ResourceName]string{"cpu": "5"}).
					Annotation(podutil.PodGroupNameAnnotationKey, "pg").Obj(),
				testing_helper.MakePod().Namespace("default").Name("foo2").UID("foo2").
					ControllerRef(metav1.OwnerReference{
						Kind: "ReplicaSet",
						Name: "rs",
						UID:  "rs",
					}).
					Priority(100).PriorityClassName("pc").
					Req(map[v1.ResourceName]string{"cpu": "5"}).
					Annotation(podutil.PodGroupNameAnnotationKey, "pg").Obj(),
			},
			nodes: []*v1.Node{
				testing_helper.MakeNode().Name("n1").Capacity(map[v1.ResourceName]string{"cpu": "5"}).Obj(),
				testing_helper.MakeNode().Name("n2").Capacity(map[v1.ResourceName]string{"cpu": "5"}).Obj(),
			},
			existingPods: []*v1.Pod{
				testing_helper.MakePod().Namespace("default").Name("p1").UID("p1").Node("n1").
					Priority(90).PriorityClassName("pc").
					Req(map[v1.ResourceName]string{"cpu": "5"}).Obj(),
				testing_helper.MakePod().Namespace("default").Name("p2").UID("p2").Node("n2").
					Priority(80).PriorityClassName("pc").
					Req(map[v1.ResourceName]string{"cpu": "5"}).Obj(),
			},
			expectedResults: []expectedResult{
				{
					expectedUnitResult: &core.UnitResult{
						SuccessfulPods: []string{"default/foo1", "default/foo2"},
						FailedPods:     []string{},
					},
					expectedPods: map[string]sets.String{
						"n1": sets.NewString("foo2"),
						"n2": sets.NewString("foo1"),
					},
					expectedRunningUnitInfo: map[string]*core.RunningUnitInfo{
						"default/foo1": {
							NodeToPlace: "n2",
							Victims: &framework.Victims{
								Pods: []*v1.Pod{
									testing_helper.MakePod().Namespace("default").Name("p2").UID("p2").Node("n2").
										Priority(80).PriorityClassName("pc").
										Req(map[v1.ResourceName]string{"cpu": "5"}).Obj(),
								},
							},
							ClonedPod: testing_helper.MakePod().Namespace("default").Name("foo1").UID("foo1").
								ControllerRef(metav1.OwnerReference{
									Kind: "ReplicaSet",
									Name: "rs",
									UID:  "rs",
								}).
								Priority(100).PriorityClassName("pc").
								Req(map[v1.ResourceName]string{"cpu": "5"}).
								Annotation(podutil.PodGroupNameAnnotationKey, "pg").
								Annotation(podutil.NominatedNodeAnnotationKey, `{"node":"n2","victims":[{"name":"p2","namespace":"default","uid":"p2"}]}`).
								Annotation(podutil.InitialHandledTimestampAnnotationKey, "0001-01-01T00:00:00Z").
								Annotation(podutil.PodStateAnnotationKey, string(podutil.PodAssumed)).
								Annotation(podutil.UnitScheduledIndexAnnotationKey, "0").
								Annotation(podutil.TraceContext, "").Obj(),
						},
						"default/foo2": {
							NodeToPlace: "n1",
							Victims: &framework.Victims{
								Pods: []*v1.Pod{
									testing_helper.MakePod().Namespace("default").Name("p1").UID("p1").Node("n1").
										Priority(90).PriorityClassName("pc").
										Req(map[v1.ResourceName]string{"cpu": "5"}).Obj(),
								},
							},
							ClonedPod: testing_helper.MakePod().Namespace("default").Name("foo2").UID("foo2").
								ControllerRef(metav1.OwnerReference{
									Kind: "ReplicaSet",
									Name: "rs",
									UID:  "rs",
								}).
								Priority(100).PriorityClassName("pc").
								Req(map[v1.ResourceName]string{"cpu": "5"}).
								Annotation(podutil.PodGroupNameAnnotationKey, "pg").
								Annotation(podutil.NominatedNodeAnnotationKey, `{"node":"n1","victims":[{"name":"p1","namespace":"default","uid":"p1"}]}`).
								Annotation(podutil.InitialHandledTimestampAnnotationKey, "0001-01-01T00:00:00Z").
								Annotation(podutil.PodStateAnnotationKey, string(podutil.PodAssumed)).
								Annotation(podutil.UnitScheduledIndexAnnotationKey, "1").
								Annotation(podutil.TraceContext, "").Obj(),
						},
					},
				},
				{
					expectedUnitResult: &core.UnitResult{
						SuccessfulPods: []string{"default/foo2", "default/foo1"},
						FailedPods:     []string{},
					},
					expectedPods: map[string]sets.String{
						"n1": sets.NewString("foo1"),
						"n2": sets.NewString("foo2"),
					},
					expectedRunningUnitInfo: map[string]*core.RunningUnitInfo{
						"default/foo2": {
							NodeToPlace: "n2",
							Victims: &framework.Victims{
								Pods: []*v1.Pod{
									testing_helper.MakePod().Namespace("default").Name("p2").UID("p2").Node("n2").
										Priority(80).PriorityClassName("pc").
										Req(map[v1.ResourceName]string{"cpu": "5"}).Obj(),
								},
							},
							ClonedPod: testing_helper.MakePod().Namespace("default").Name("foo2").UID("foo2").
								ControllerRef(metav1.OwnerReference{
									Kind: "ReplicaSet",
									Name: "rs",
									UID:  "rs",
								}).
								Priority(100).PriorityClassName("pc").
								Req(map[v1.ResourceName]string{"cpu": "5"}).
								Annotation(podutil.PodGroupNameAnnotationKey, "pg").
								Annotation(podutil.NominatedNodeAnnotationKey, `{"node":"n2","victims":[{"name":"p2","namespace":"default","uid":"p2"}]}`).
								Annotation(podutil.InitialHandledTimestampAnnotationKey, "0001-01-01T00:00:00Z").
								Annotation(podutil.PodStateAnnotationKey, string(podutil.PodAssumed)).
								Annotation(podutil.UnitScheduledIndexAnnotationKey, "0").
								Annotation(podutil.TraceContext, "").Obj(),
						},
						"default/foo1": {
							NodeToPlace: "n1",
							Victims: &framework.Victims{
								Pods: []*v1.Pod{
									testing_helper.MakePod().Namespace("default").Name("p1").UID("p1").Node("n1").
										Priority(90).PriorityClassName("pc").
										Req(map[v1.ResourceName]string{"cpu": "5"}).Obj(),
								},
							},
							ClonedPod: testing_helper.MakePod().Namespace("default").Name("foo1").UID("foo1").
								ControllerRef(metav1.OwnerReference{
									Kind: "ReplicaSet",
									Name: "rs",
									UID:  "rs",
								}).
								Priority(100).PriorityClassName("pc").
								Req(map[v1.ResourceName]string{"cpu": "5"}).
								Annotation(podutil.PodGroupNameAnnotationKey, "pg").
								Annotation(podutil.NominatedNodeAnnotationKey, `{"node":"n1","victims":[{"name":"p1","namespace":"default","uid":"p1"}]}`).
								Annotation(podutil.InitialHandledTimestampAnnotationKey, "0001-01-01T00:00:00Z").
								Annotation(podutil.PodStateAnnotationKey, string(podutil.PodAssumed)).
								Annotation(podutil.UnitScheduledIndexAnnotationKey, "1").
								Annotation(podutil.TraceContext, "").Obj(),
						},
					},
				},
			},
		},
		{
			name:     "first preempt successfully, second schedule successfully",
			podGroup: testing_helper.MakePodGroup().Namespace("default").Name("pg").MinMember(2).Obj(),
			pods: []*v1.Pod{
				testing_helper.MakePod().Namespace("default").Name("foo1").UID("foo1").
					Priority(100).PriorityClassName("pc").
					Req(map[v1.ResourceName]string{"cpu": "5"}).
					Annotation(podutil.PodGroupNameAnnotationKey, "pg").Obj(),
				testing_helper.MakePod().Namespace("default").Name("foo2").UID("foo2").
					Priority(100).PriorityClassName("pc").
					Req(map[v1.ResourceName]string{"cpu": "5"}).
					Annotation(podutil.PodGroupNameAnnotationKey, "pg").Obj(),
			},
			nodes: []*v1.Node{
				testing_helper.MakeNode().Name("n").Capacity(map[v1.ResourceName]string{"cpu": "10"}).Obj(),
			},
			existingPods: []*v1.Pod{
				testing_helper.MakePod().Namespace("default").Name("p1").UID("p1").Node("n").
					Priority(90).PriorityClassName("pc").
					Req(map[v1.ResourceName]string{"cpu": "10"}).Obj(),
			},
			expectedResults: []expectedResult{
				{
					expectedUnitResult: &core.UnitResult{
						SuccessfulPods: []string{"default/foo1", "default/foo2"},
						FailedPods:     []string{},
					},
					expectedPods: map[string]sets.String{
						"n": sets.NewString("foo1", "foo2"),
					},
					expectedRunningUnitInfo: map[string]*core.RunningUnitInfo{
						"default/foo1": {
							NodeToPlace: "n",
							Victims: &framework.Victims{
								Pods: []*v1.Pod{
									testing_helper.MakePod().Namespace("default").Name("p1").UID("p1").Node("n").
										Priority(90).PriorityClassName("pc").
										Req(map[v1.ResourceName]string{"cpu": "10"}).Obj(),
								},
							},
							ClonedPod: testing_helper.MakePod().Namespace("default").Name("foo1").UID("foo1").
								Priority(100).PriorityClassName("pc").
								Req(map[v1.ResourceName]string{"cpu": "5"}).
								Annotation(podutil.PodGroupNameAnnotationKey, "pg").
								Annotation(podutil.NominatedNodeAnnotationKey, `{"node":"n","victims":[{"name":"p1","namespace":"default","uid":"p1"}]}`).
								Annotation(podutil.InitialHandledTimestampAnnotationKey, "0001-01-01T00:00:00Z").
								Annotation(podutil.PodStateAnnotationKey, string(podutil.PodAssumed)).
								Annotation(podutil.UnitScheduledIndexAnnotationKey, "0").
								Annotation(podutil.TraceContext, "").Obj(),
						},
						"default/foo2": {
							NodeToPlace: "n",
							Victims:     &framework.Victims{},
							ClonedPod: testing_helper.MakePod().Namespace("default").Name("foo2").UID("foo2").
								Priority(100).PriorityClassName("pc").
								Req(map[v1.ResourceName]string{"cpu": "5"}).
								Annotation(podutil.PodGroupNameAnnotationKey, "pg").
								Annotation(podutil.AssumedNodeAnnotationKey, "n").
								Annotation(podutil.InitialHandledTimestampAnnotationKey, "0001-01-01T00:00:00Z").
								Annotation(podutil.PodStateAnnotationKey, string(podutil.PodAssumed)).
								Annotation(podutil.UnitScheduledIndexAnnotationKey, "1").
								Annotation(podutil.TraceContext, "").Obj(),
						},
					},
				},
				{
					expectedUnitResult: &core.UnitResult{
						SuccessfulPods: []string{"default/foo2", "default/foo1"},
						FailedPods:     []string{},
					},
					expectedPods: map[string]sets.String{
						"n": sets.NewString("foo2", "foo1"),
					},
					expectedRunningUnitInfo: map[string]*core.RunningUnitInfo{
						"default/foo2": {
							NodeToPlace: "n",
							Victims: &framework.Victims{
								Pods: []*v1.Pod{
									testing_helper.MakePod().Namespace("default").Name("p1").UID("p1").Node("n").
										Priority(90).PriorityClassName("pc").
										Req(map[v1.ResourceName]string{"cpu": "10"}).Obj(),
								},
							},
							ClonedPod: testing_helper.MakePod().Namespace("default").Name("foo2").UID("foo2").
								Priority(100).PriorityClassName("pc").
								Req(map[v1.ResourceName]string{"cpu": "5"}).
								Annotation(podutil.PodGroupNameAnnotationKey, "pg").
								Annotation(podutil.NominatedNodeAnnotationKey, `{"node":"n","victims":[{"name":"p1","namespace":"default","uid":"p1"}]}`).
								Annotation(podutil.InitialHandledTimestampAnnotationKey, "0001-01-01T00:00:00Z").
								Annotation(podutil.PodStateAnnotationKey, string(podutil.PodAssumed)).
								Annotation(podutil.UnitScheduledIndexAnnotationKey, "0").
								Annotation(podutil.TraceContext, "").Obj(),
						},
						"default/foo1": {
							NodeToPlace: "n",
							Victims:     &framework.Victims{},
							ClonedPod: testing_helper.MakePod().Namespace("default").Name("foo1").UID("foo1").
								Priority(100).PriorityClassName("pc").
								Req(map[v1.ResourceName]string{"cpu": "5"}).
								Annotation(podutil.PodGroupNameAnnotationKey, "pg").
								Annotation(podutil.AssumedNodeAnnotationKey, "n").
								Annotation(podutil.InitialHandledTimestampAnnotationKey, "0001-01-01T00:00:00Z").
								Annotation(podutil.PodStateAnnotationKey, string(podutil.PodAssumed)).
								Annotation(podutil.UnitScheduledIndexAnnotationKey, "1").
								Annotation(podutil.TraceContext, "").Obj(),
						},
					},
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			schedulerName := "scheduler"
			stop := make(chan struct{})
			defer close(stop)

			client := clientsetfake.NewSimpleClientset()
			informerFactory := informers.NewSharedInformerFactory(client, 0)
			broadcaster := cmdutil.NewEventBroadcasterAdapter(client)
			crdClient := godelclientfake.NewSimpleClientset()
			crdInformerFactory := crdinformers.NewSharedInformerFactory(crdClient, 0)

			podInformer := informerFactory.Core().V1().Pods().Informer()
			pcInformer := informerFactory.Scheduling().V1().PriorityClasses().Informer()
			informerFactory.Start(stop)
			informerFactory.WaitForCacheSync(stop)
			for _, p := range tt.pods {
				podInformer.GetIndexer().Add(p)
			}
			for _, p := range tt.existingPods {
				podInformer.GetIndexer().Add(p)
			}

			pc := testing_helper.MakePriorityClass().Name("pc").Obj()
			pcInformer.GetIndexer().Add(pc)

			sCache := godelcache.New(handler.MakeCacheHandlerWrapper().
				SchedulerName("scheduler").SchedulerType(schedulerName).SubCluster(framework.DefaultSubCluster).
				TTL(30 * time.Second).Period(10 * time.Second).StopCh(make(<-chan struct{})).
				EnableStore("PreemptionStore").
				Obj())
			snapshot := godelcache.NewEmptySnapshot(handler.MakeCacheHandlerWrapper().
				SubCluster(framework.DefaultSubCluster).SwitchType(framework.DefaultSubClusterSwitchType).
				PodLister(informerFactory.Core().V1().Pods().Lister()).
				EnableStore("PreemptionStore").
				Obj())

			queue := schedulingqueue.NewSchedulingQueue(sCache, nil, nil, nil, false)

			for _, n := range tt.nodes {
				sCache.AddNode(n)
			}
			for _, p := range tt.existingPods {
				sCache.AddPod(p)
			}
			for _, pdb := range tt.pdbs {
				sCache.AddPDB(pdb)
			}
			sCache.UpdateSnapshot(snapshot)

			basePlugins := framework.PluginCollectionSet{
				string(podutil.Kubelet): &framework.PluginCollection{
					Filters: []*framework.PluginSpec{
						framework.NewPluginSpec(noderesources.FitName),
					},
					Scores: []*framework.PluginSpec{
						framework.NewPluginSpec(nodeaffinity.Name),
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
						{
							Plugins: []*framework.PluginSpec{
								{
									Name: pdbchecker.PDBCheckerName,
								},
							},
						},
					},
					Sortings: []*framework.PluginSpec{
						framework.NewPluginSpec(priority.MinHighestPriorityName),
					},
				},
			}
			preemptionPluginArgs := map[string]*config.PluginConfig{}
			globalClock := clock.RealClock{}
			podScheduler := podscheduler.NewPodScheduler(
				schedulerName,
				framework.DisableScheduleSwitch,
				"",
				client,
				crdClient,
				informerFactory,
				crdInformerFactory,
				snapshot,
				globalClock,
				false,
				config.CandidateSelectPolicyBest,
				[]string{config.BetterPreemptionPolicyAscending},
				100,
				100,
				basePlugins,
				nil,
				preemptionPluginArgs,
			)

			gs := &unitScheduler{
				schedulerName:     testSchedulerName,
				switchType:        framework.SwitchType(1),
				subCluster:        "",
				disablePreemption: false,

				client:    nil,
				crdClient: nil,

				podLister: testing_helper.NewFakePodLister(nil),
				pgLister:  testing_helper.NewFakePodGroupLister(nil),

				Cache:      sCache,
				Snapshot:   snapshot,
				Queue:      queue,
				Reconciler: reconciler.NewFailedTaskReconciler(nil, nil, sCache, ""),
				Scheduler:  podScheduler,

				PluginRegistry: nil, // TODO:

				Recorder: broadcaster.NewRecorder(testSchedulerName),
			}

			unit := framework.NewPodGroupUnit(tt.podGroup, 100)
			for _, p := range tt.pods {
				unit.AddPod(&framework.QueuedPodInfo{Pod: p})
			}
			queuedUnitInfo := &framework.QueuedUnitInfo{
				UnitKey:            unit.GetKey(),
				ScheduleUnit:       unit,
				QueuePriorityScore: float64(unit.GetPriority()),
			}
			unitInfo, _ := gs.constructSchedulingUnitInfo(context.Background(), queuedUnitInfo)
			unitFramework := unitruntime.NewUnitFramework(gs, gs, gs.PluginRegistry, nil, unitInfo.QueuedUnitInfo)

			lister := framework.NewNodeInfoLister().(*framework.NodeInfoListerImpl)
			for _, n := range tt.nodes {
				lister.AddNodeInfo(snapshot.GetNodeInfo(n.Name))
			}
			nodeCircle := framework.NewNodeCircle("", lister)
			nodeGroup := snapshot.MakeBasicNodeGroup()
			nodeGroup.SetNodeCircles([]framework.NodeCircle{nodeCircle})
			unitResult := gs.scheduleUnitInNodeGroup(context.Background(), unitInfo, unitFramework, nodeGroup)
			// check running unit info
			var allErrs [][]error
			for _, res := range tt.expectedResults {
				var errs []error
				for key, runningUnitInfo := range unitInfo.DispatchedPods {
					runningUnitInfo.QueuedPodInfo = nil
					runningUnitInfo.Trace = nil
					if runningUnitInfo.Victims != nil {
						runningUnitInfo.Victims.PreemptionState = nil
					}
					if !reflect.DeepEqual(res.expectedRunningUnitInfo[key], runningUnitInfo) {
						errs = append(errs, fmt.Errorf("for pod %s, expected get running unit info %v but got %v", key, res.expectedRunningUnitInfo[key], runningUnitInfo))
					}
				}

				for _, node := range tt.nodes {
					nInfo := gs.Snapshot.GetNodeInfo(node.Name)
					// check pods in snapshot
					pods := sets.NewString()
					for _, p := range nInfo.GetPods() {
						pods.Insert(p.Key())
					}
					if !reflect.DeepEqual(res.expectedPods[node.Name], pods) {
						errs = append(errs, fmt.Errorf("for node %s, expected get pods %v but got %v", node.Name, res.expectedPods[node.Name], pods))
					}
				}

				// check unit result
				unitResult.Details = nil
				if !reflect.DeepEqual(sets.NewString(res.expectedUnitResult.SuccessfulPods...), sets.NewString(unitResult.SuccessfulPods...)) {
					errs = append(errs, fmt.Errorf("expected get successful pods %v but got %v", res.expectedUnitResult.SuccessfulPods, unitResult.SuccessfulPods))
				}
				if !reflect.DeepEqual(sets.NewString(res.expectedUnitResult.FailedPods...), sets.NewString(unitResult.FailedPods...)) {
					errs = append(errs, fmt.Errorf("expected get failed pods %v but got %v", res.expectedUnitResult.FailedPods, unitResult.FailedPods))
				}
				if len(errs) > 0 {
					allErrs = append(allErrs, errs)
				} else {
					break
				}
			}
			if len(allErrs) == len(tt.expectedResults) {
				for i, errs := range allErrs {
					t.Errorf("index %d all errs: %v", i, errs)
				}
			}
		})
	}
}
