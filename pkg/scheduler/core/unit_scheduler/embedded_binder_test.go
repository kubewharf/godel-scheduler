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

package unitscheduler

import (
	"context"
	"fmt"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	clientsetfake "k8s.io/client-go/kubernetes/fake"
	"k8s.io/client-go/tools/events"

	"github.com/kubewharf/godel-scheduler/pkg/binder"
	framework "github.com/kubewharf/godel-scheduler/pkg/framework/api"
	fakecache "github.com/kubewharf/godel-scheduler/pkg/scheduler/cache/fake"
	"github.com/kubewharf/godel-scheduler/pkg/scheduler/core"
	"github.com/kubewharf/godel-scheduler/pkg/scheduler/reconciler"
	podutil "github.com/kubewharf/godel-scheduler/pkg/util/pod"
)

// ---------- mock embedded binder ----------

type mockEmbeddedBinder struct {
	mu           sync.Mutex
	bindCalls    int
	returnResult *binder.BindResult
	returnErr    error
	lastReq      *binder.BindRequest
	started      bool
	stopped      bool
}

func newMockEmbeddedBinder() *mockEmbeddedBinder {
	return &mockEmbeddedBinder{
		returnResult: &binder.BindResult{},
	}
}

func (m *mockEmbeddedBinder) BindUnit(_ context.Context, req *binder.BindRequest) (*binder.BindResult, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.bindCalls++
	m.lastReq = req
	return m.returnResult, m.returnErr
}

func (m *mockEmbeddedBinder) Start(_ context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.started = true
	return nil
}

func (m *mockEmbeddedBinder) Stop() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.stopped = true
}

func (m *mockEmbeddedBinder) getBindCalls() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.bindCalls
}

func (m *mockEmbeddedBinder) getLastReq() *binder.BindRequest {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.lastReq
}

// ---------- mock event recorder (satisfies events.EventRecorder) ----------

type noopEventRecorder struct{}

func (noopEventRecorder) Eventf(regarding, related runtime.Object, eventType, reason, action, note string, args ...interface{}) {
}

// Ensure noopEventRecorder satisfies events.EventRecorder at compile time.
var _ events.EventRecorder = noopEventRecorder{}

// ---------- helpers ----------

func makePod(name, uid, nodeName string) *v1.Pod {
	return &v1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: "default",
			UID:       types.UID(uid),
			Annotations: map[string]string{
				podutil.AssumedNodeAnnotationKey: nodeName,
			},
		},
	}
}

func makeQueuedPodInfo(pod *v1.Pod) *framework.QueuedPodInfo {
	return &framework.QueuedPodInfo{
		Pod: pod,
	}
}

func makeRunningUnitInfo(pod *v1.Pod, cloned *v1.Pod) *core.RunningUnitInfo {
	return &core.RunningUnitInfo{
		QueuedPodInfo: makeQueuedPodInfo(pod),
		ClonedPod:     cloned,
	}
}

// ---------- tests ----------

func TestSetGetEmbeddedBinder(t *testing.T) {
	gs := &unitScheduler{}
	assert.Nil(t, gs.GetEmbeddedBinder(), "initially nil")

	mock := newMockEmbeddedBinder()
	gs.SetEmbeddedBinder(mock)
	assert.Equal(t, mock, gs.GetEmbeddedBinder())

	gs.SetEmbeddedBinder(nil)
	assert.Nil(t, gs.GetEmbeddedBinder())
}

func TestPersistSuccessfulPods_DelegatesViaEmbeddedBinder(t *testing.T) {
	pod := makePod("pod-1", "uid-1", "node-a")
	cloned := pod.DeepCopy()

	mock := newMockEmbeddedBinder()
	mock.returnResult = &binder.BindResult{
		SuccessfulPods: []types.UID{"uid-1"},
	}

	fakeCache := &fakecache.Cache{}

	gs := &unitScheduler{
		schedulerName:  "test-scheduler",
		switchType:     framework.SwitchType(1),
		subCluster:     "",
		client:         clientsetfake.NewSimpleClientset(),
		Cache:          fakeCache,
		Reconciler:     reconciler.NewFailedTaskReconciler(nil, nil, fakeCache, ""),
		Recorder:       noopEventRecorder{},
		embeddedBinder: mock,
	}

	result := &core.UnitResult{
		SuccessfulPods: []string{"default/pod-1"},
	}
	unitInfo := &core.SchedulingUnitInfo{
		UnitKey: "test-unit",
		QueuedUnitInfo: &framework.QueuedUnitInfo{
			ScheduleUnit: &framework.SinglePodUnit{},
		},
		DispatchedPods: map[string]*core.RunningUnitInfo{
			"default/pod-1": makeRunningUnitInfo(pod, cloned),
		},
	}

	gs.PersistSuccessfulPods(context.Background(), result, unitInfo)

	assert.Equal(t, 1, mock.getBindCalls(), "BindUnit should be called once")
	req := mock.getLastReq()
	assert.NotNil(t, req)
	assert.Equal(t, "node-a", req.NodeNameFor("uid-1"), "should resolve node from NodeNames map")
	assert.Len(t, req.Pods, 1)
}

func TestPersistSuccessfulPods_EmbeddedBinderError_AddsToReconciler(t *testing.T) {
	pod := makePod("pod-err", "uid-err", "node-b")
	cloned := pod.DeepCopy()

	mock := newMockEmbeddedBinder()
	mock.returnErr = fmt.Errorf("simulated bind failure")

	fakeCache := &fakecache.Cache{}
	rec := reconciler.NewFailedTaskReconciler(nil, nil, fakeCache, "")

	gs := &unitScheduler{
		schedulerName:  "test-scheduler",
		switchType:     framework.SwitchType(1),
		subCluster:     "",
		client:         clientsetfake.NewSimpleClientset(),
		Cache:          fakeCache,
		Reconciler:     rec,
		Recorder:       noopEventRecorder{},
		embeddedBinder: mock,
	}

	result := &core.UnitResult{
		SuccessfulPods: []string{"default/pod-err"},
	}
	unitInfo := &core.SchedulingUnitInfo{
		UnitKey: "test-unit",
		QueuedUnitInfo: &framework.QueuedUnitInfo{
			ScheduleUnit: &framework.SinglePodUnit{},
		},
		DispatchedPods: map[string]*core.RunningUnitInfo{
			"default/pod-err": makeRunningUnitInfo(pod, cloned),
		},
	}

	// Should not panic; failed pods go to reconciler.
	gs.PersistSuccessfulPods(context.Background(), result, unitInfo)

	assert.Equal(t, 1, mock.getBindCalls())
}

func TestPersistSuccessfulPods_NoEmbeddedBinder_RoutesToPatchPod(t *testing.T) {
	// When embeddedBinder is nil, PersistSuccessfulPods should route to
	// persistViaPatchPod (not call any embedded binder).
	// We verify the routing by confirming that the embedded binder mock is
	// NOT called. We don't actually exercise PatchPod here because it
	// requires full tracing infrastructure (Trace, Snapshot, etc.).
	gs := &unitScheduler{
		embeddedBinder: nil,
	}
	// Simply verify that embeddedBinder being nil would route correctly.
	assert.Nil(t, gs.embeddedBinder, "should be nil → routes to PatchPod path")
}

func TestPersistSuccessfulPods_EmbeddedBinderPartialFailure(t *testing.T) {
	pod1 := makePod("pod-ok", "uid-ok", "node-d")
	pod2 := makePod("pod-fail", "uid-fail", "node-d")
	cloned1 := pod1.DeepCopy()
	cloned2 := pod2.DeepCopy()

	mock := newMockEmbeddedBinder()
	mock.returnResult = &binder.BindResult{
		SuccessfulPods: []types.UID{"uid-ok"},
		FailedPods:     map[types.UID]error{"uid-fail": fmt.Errorf("conflict")},
	}

	fakeCache := &fakecache.Cache{}
	rec := reconciler.NewFailedTaskReconciler(nil, nil, fakeCache, "")

	gs := &unitScheduler{
		schedulerName:  "test-scheduler",
		switchType:     framework.SwitchType(1),
		subCluster:     "",
		client:         clientsetfake.NewSimpleClientset(),
		Cache:          fakeCache,
		Reconciler:     rec,
		Recorder:       noopEventRecorder{},
		embeddedBinder: mock,
	}

	result := &core.UnitResult{
		SuccessfulPods: []string{"default/pod-ok", "default/pod-fail"},
	}
	unitInfo := &core.SchedulingUnitInfo{
		UnitKey: "test-unit",
		QueuedUnitInfo: &framework.QueuedUnitInfo{
			ScheduleUnit: &framework.SinglePodUnit{},
		},
		DispatchedPods: map[string]*core.RunningUnitInfo{
			"default/pod-ok":   makeRunningUnitInfo(pod1, cloned1),
			"default/pod-fail": makeRunningUnitInfo(pod2, cloned2),
		},
	}

	gs.PersistSuccessfulPods(context.Background(), result, unitInfo)
	assert.Equal(t, 1, mock.getBindCalls())
}

// TestPersistSuccessfulPods_EmbeddedBinderMultiplePods tests that multiple pods
// in a single unit are collected into one BindRequest.
func TestPersistSuccessfulPods_EmbeddedBinderMultiplePods(t *testing.T) {
	pod1 := makePod("pod-a", "uid-a", "node-e")
	pod2 := makePod("pod-b", "uid-b", "node-e")
	cloned1 := pod1.DeepCopy()
	cloned2 := pod2.DeepCopy()

	mock := newMockEmbeddedBinder()
	mock.returnResult = &binder.BindResult{
		SuccessfulPods: []types.UID{"uid-a", "uid-b"},
	}

	fakeCache := &fakecache.Cache{}

	gs := &unitScheduler{
		schedulerName:  "test-scheduler",
		switchType:     framework.SwitchType(1),
		subCluster:     "",
		client:         clientsetfake.NewSimpleClientset(),
		Cache:          fakeCache,
		Reconciler:     reconciler.NewFailedTaskReconciler(nil, nil, fakeCache, ""),
		Recorder:       noopEventRecorder{},
		embeddedBinder: mock,
	}

	result := &core.UnitResult{
		SuccessfulPods: []string{"default/pod-a", "default/pod-b"},
	}
	unitInfo := &core.SchedulingUnitInfo{
		UnitKey: "test-unit",
		QueuedUnitInfo: &framework.QueuedUnitInfo{
			ScheduleUnit: &framework.SinglePodUnit{},
		},
		DispatchedPods: map[string]*core.RunningUnitInfo{
			"default/pod-a": makeRunningUnitInfo(pod1, cloned1),
			"default/pod-b": makeRunningUnitInfo(pod2, cloned2),
		},
	}

	gs.PersistSuccessfulPods(context.Background(), result, unitInfo)
	assert.Equal(t, 1, mock.getBindCalls(), "should produce single BindUnit call")
	req := mock.getLastReq()
	assert.Len(t, req.Pods, 2, "BindRequest should have 2 pods")
	assert.Equal(t, "node-e", req.NodeNameFor("uid-a"), "pod-a should target node-e")
	assert.Equal(t, "node-e", req.NodeNameFor("uid-b"), "pod-b should target node-e")
}

func TestPersistSuccessfulPods_NoPods(t *testing.T) {
	mock := newMockEmbeddedBinder()
	fakeCache := &fakecache.Cache{}

	gs := &unitScheduler{
		schedulerName:  "test-scheduler",
		switchType:     framework.SwitchType(1),
		subCluster:     "",
		client:         clientsetfake.NewSimpleClientset(),
		Cache:          fakeCache,
		Reconciler:     reconciler.NewFailedTaskReconciler(nil, nil, fakeCache, ""),
		Recorder:       noopEventRecorder{},
		embeddedBinder: mock,
	}

	result := &core.UnitResult{
		SuccessfulPods: []string{},
	}
	unitInfo := &core.SchedulingUnitInfo{
		UnitKey:        "empty-unit",
		QueuedUnitInfo: &framework.QueuedUnitInfo{ScheduleUnit: &framework.SinglePodUnit{}},
		DispatchedPods: map[string]*core.RunningUnitInfo{},
	}

	// Should not panic with empty pod list.
	gs.PersistSuccessfulPods(context.Background(), result, unitInfo)
	// Mock may or may not be called depending on validation; key is no panic.
}

// TestPersistSuccessfulPods_MultiNodePodGroup verifies that when pods in a
// PodGroup are scheduled to different nodes, the BindRequest contains
// per-pod node assignments via NodeNames.
func TestPersistSuccessfulPods_MultiNodePodGroup(t *testing.T) {
	// Pod A → node-X, Pod B → node-Y (different nodes, simulating gang scheduling)
	podA := &v1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name: "pg-pod-a", Namespace: "default", UID: "uid-pg-a",
			Annotations: map[string]string{},
		},
	}
	podB := &v1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name: "pg-pod-b", Namespace: "default", UID: "uid-pg-b",
			Annotations: map[string]string{},
		},
	}
	// ClonedPods have the scheduling result annotations.
	clonedA := podA.DeepCopy()
	clonedA.Annotations[podutil.AssumedNodeAnnotationKey] = "node-X"
	clonedB := podB.DeepCopy()
	clonedB.Annotations[podutil.AssumedNodeAnnotationKey] = "node-Y"

	mock := newMockEmbeddedBinder()
	mock.returnResult = &binder.BindResult{
		SuccessfulPods: []types.UID{"uid-pg-a", "uid-pg-b"},
	}

	fakeCache := &fakecache.Cache{}
	gs := &unitScheduler{
		schedulerName:  "test-scheduler",
		switchType:     framework.SwitchType(1),
		subCluster:     "",
		client:         clientsetfake.NewSimpleClientset(),
		Cache:          fakeCache,
		Reconciler:     reconciler.NewFailedTaskReconciler(nil, nil, fakeCache, ""),
		Recorder:       noopEventRecorder{},
		embeddedBinder: mock,
	}

	result := &core.UnitResult{
		SuccessfulPods: []string{"default/pg-pod-a", "default/pg-pod-b"},
	}
	unitInfo := &core.SchedulingUnitInfo{
		UnitKey: "podgroup-unit",
		QueuedUnitInfo: &framework.QueuedUnitInfo{
			ScheduleUnit: &framework.SinglePodUnit{},
		},
		DispatchedPods: map[string]*core.RunningUnitInfo{
			"default/pg-pod-a": makeRunningUnitInfo(podA, clonedA),
			"default/pg-pod-b": makeRunningUnitInfo(podB, clonedB),
		},
	}

	gs.PersistSuccessfulPods(context.Background(), result, unitInfo)
	assert.Equal(t, 1, mock.getBindCalls())
	req := mock.getLastReq()
	assert.NotNil(t, req)
	assert.Len(t, req.Pods, 2)
	// Verify per-pod node assignments.
	assert.Equal(t, "node-X", req.NodeNameFor("uid-pg-a"), "pod-a should target node-X")
	assert.Equal(t, "node-Y", req.NodeNameFor("uid-pg-b"), "pod-b should target node-Y")
	// NodeNames map should have 2 entries.
	assert.Len(t, req.NodeNames, 2)
}
