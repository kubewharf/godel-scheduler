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
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	framework "github.com/kubewharf/godel-scheduler/pkg/framework/api"
	fakecache "github.com/kubewharf/godel-scheduler/pkg/scheduler/cache/fake"
	unitstatus "github.com/kubewharf/godel-scheduler/pkg/util/unitstatus"
)

func newFakeSchedulerCache() *fakecache.Cache {
	return &fakecache.Cache{
		AssumeFunc:       func(pod *v1.Pod) {},
		ForgetFunc:       func(pod *v1.Pod) {},
		IsAssumedPodFunc: func(pod *v1.Pod) bool { return false },
		IsCachedPodFunc:  func(pod *v1.Pod) bool { return false },
		GetPodFunc:       func(pod *v1.Pod) *v1.Pod { return pod },
		UnitStatus:       unitstatus.NewUnitStatusMap(),
	}
}

func makePod(name, namespace string) *v1.Pod {
	return &v1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
	}
}

func TestCacheAdapter_NewCacheAdapter_NilPanics(t *testing.T) {
	assert.Panics(t, func() {
		NewCacheAdapter(nil)
	})
}

func TestCacheAdapter_NewCacheAdapter_Success(t *testing.T) {
	fc := newFakeSchedulerCache()
	adapter := NewCacheAdapter(fc)
	assert.NotNil(t, adapter)
	assert.NotNil(t, adapter.assumedPods)
	assert.NotNil(t, adapter.podMarkers)
}

func TestCacheAdapter_AssumePod(t *testing.T) {
	var assumed []string
	fc := newFakeSchedulerCache()
	fc.AssumeFunc = func(pod *v1.Pod) { assumed = append(assumed, pod.Name) }
	adapter := NewCacheAdapter(fc)

	pod := makePod("pod-1", "default")
	podInfo := framework.MakeCachePodInfoWrapper().Pod(pod).Obj()

	err := adapter.AssumePod(podInfo)
	assert.NoError(t, err)
	assert.Contains(t, assumed, "pod-1")

	// IsAssumedPod should return true for locally assumed pod
	isAssumed, err := adapter.IsAssumedPod(pod)
	assert.NoError(t, err)
	assert.True(t, isAssumed)
}

func TestCacheAdapter_AssumePod_AlreadyAssumed(t *testing.T) {
	fc := newFakeSchedulerCache()
	adapter := NewCacheAdapter(fc)

	pod := makePod("pod-1", "default")
	podInfo := framework.MakeCachePodInfoWrapper().Pod(pod).Obj()

	err := adapter.AssumePod(podInfo)
	assert.NoError(t, err)

	// Second assume should fail
	err = adapter.AssumePod(podInfo)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "already assumed")
}

func TestCacheAdapter_ForgetPod(t *testing.T) {
	var forgotten []string
	fc := newFakeSchedulerCache()
	fc.ForgetFunc = func(pod *v1.Pod) { forgotten = append(forgotten, pod.Name) }
	adapter := NewCacheAdapter(fc)

	pod := makePod("pod-1", "default")
	podInfo := framework.MakeCachePodInfoWrapper().Pod(pod).Obj()

	// Assume then forget
	_ = adapter.AssumePod(podInfo)
	err := adapter.ForgetPod(podInfo)
	assert.NoError(t, err)
	assert.Contains(t, forgotten, "pod-1")

	// IsAssumedPod should return false after forget
	isAssumed, err := adapter.IsAssumedPod(pod)
	assert.NoError(t, err)
	assert.False(t, isAssumed)
}

func TestCacheAdapter_GetNodeInfo_ReturnsNil(t *testing.T) {
	fc := newFakeSchedulerCache()
	adapter := NewCacheAdapter(fc)

	nodeInfo := adapter.GetNodeInfo("node-1")
	assert.Nil(t, nodeInfo)
}

func TestCacheAdapter_GetPod_AssumedPod(t *testing.T) {
	fc := newFakeSchedulerCache()
	adapter := NewCacheAdapter(fc)

	pod := makePod("pod-1", "default")
	podInfo := framework.MakeCachePodInfoWrapper().Pod(pod).Obj()
	_ = adapter.AssumePod(podInfo)

	got, err := adapter.GetPod(pod)
	assert.NoError(t, err)
	assert.Equal(t, "pod-1", got.Name)
}

func TestCacheAdapter_GetPod_NonAssumedPod(t *testing.T) {
	returnedPod := makePod("pod-2", "default")
	fc := newFakeSchedulerCache()
	fc.GetPodFunc = func(pod *v1.Pod) *v1.Pod { return returnedPod }
	adapter := NewCacheAdapter(fc)

	got, err := adapter.GetPod(makePod("pod-2", "default"))
	assert.NoError(t, err)
	assert.Equal(t, "pod-2", got.Name)
}

func TestCacheAdapter_FinishBinding(t *testing.T) {
	finishReservingCalled := false
	fc := newFakeSchedulerCache()
	fc.AssumeFunc = func(pod *v1.Pod) {}
	adapter := NewCacheAdapter(fc)
	// Override FinishReserving behavior isn't directly possible in fake, but it returns nil by default
	_ = finishReservingCalled // suppress unused warning

	pod := makePod("pod-1", "default")
	podInfo := framework.MakeCachePodInfoWrapper().Pod(pod).Obj()
	_ = adapter.AssumePod(podInfo)

	err := adapter.FinishBinding(pod)
	assert.NoError(t, err)

	// Pod should no longer be in assumed pods
	isAssumed, _ := adapter.IsAssumedPod(pod)
	assert.False(t, isAssumed)
}

func TestCacheAdapter_MarkPodToDelete(t *testing.T) {
	fc := newFakeSchedulerCache()
	adapter := NewCacheAdapter(fc)

	pod := makePod("pod-1", "default")
	preemptor := makePod("preemptor-1", "default")

	err := adapter.MarkPodToDelete(pod, preemptor)
	assert.NoError(t, err)

	marked, err := adapter.IsPodMarkedToDelete(pod)
	assert.NoError(t, err)
	assert.True(t, marked)
}

func TestCacheAdapter_RemoveDeletePodMarker(t *testing.T) {
	fc := newFakeSchedulerCache()
	adapter := NewCacheAdapter(fc)

	pod := makePod("pod-1", "default")
	preemptor := makePod("preemptor-1", "default")

	_ = adapter.MarkPodToDelete(pod, preemptor)
	err := adapter.RemoveDeletePodMarker(pod, preemptor)
	assert.NoError(t, err)

	marked, err := adapter.IsPodMarkedToDelete(pod)
	assert.NoError(t, err)
	assert.False(t, marked)
}

func TestCacheAdapter_IsPodMarkedToDelete_NotMarked(t *testing.T) {
	fc := newFakeSchedulerCache()
	adapter := NewCacheAdapter(fc)

	pod := makePod("pod-1", "default")
	marked, err := adapter.IsPodMarkedToDelete(pod)
	assert.NoError(t, err)
	assert.False(t, marked)
}

func TestCacheAdapter_AddPod_UpdatePod_DeletePod(t *testing.T) {
	fc := newFakeSchedulerCache()
	adapter := NewCacheAdapter(fc)

	pod := makePod("pod-1", "default")
	assert.NoError(t, adapter.AddPod(pod))
	assert.NoError(t, adapter.UpdatePod(pod, pod))
	assert.NoError(t, adapter.DeletePod(pod))
}

func TestCacheAdapter_AddNode_UpdateNode_DeleteNode(t *testing.T) {
	fc := newFakeSchedulerCache()
	adapter := NewCacheAdapter(fc)

	node := &v1.Node{ObjectMeta: metav1.ObjectMeta{Name: "node-1"}}
	assert.NoError(t, adapter.AddNode(node))
	assert.NoError(t, adapter.UpdateNode(node, node))
	assert.NoError(t, adapter.DeleteNode(node))
}

func TestCacheAdapter_ConcurrentAccess(t *testing.T) {
	fc := newFakeSchedulerCache()
	adapter := NewCacheAdapter(fc)

	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			pod := makePod("pod-"+string(rune('a'+idx%26)), "default")
			podInfo := framework.MakeCachePodInfoWrapper().Pod(pod).Obj()

			_ = adapter.AssumePod(podInfo)
			_, _ = adapter.IsAssumedPod(pod)
			_, _ = adapter.GetPod(pod)
			_ = adapter.ForgetPod(podInfo)

			_ = adapter.MarkPodToDelete(pod, nil)
			_, _ = adapter.IsPodMarkedToDelete(pod)
			_ = adapter.RemoveDeletePodMarker(pod, nil)
		}(i)
	}
	wg.Wait()
}

func TestCacheAdapter_UnitSchedulingStatus(t *testing.T) {
	fc := newFakeSchedulerCache()
	fc.UnitStatus = unitstatus.NewUnitStatusMap()
	adapter := NewCacheAdapter(fc)

	adapter.SetUnitSchedulingStatus("unit-1", unitstatus.ScheduledStatus)
	got := adapter.GetUnitSchedulingStatus("unit-1")
	assert.Equal(t, unitstatus.ScheduledStatus, got)
}

func TestCacheAdapter_GetAvailablePlaceholderPod(t *testing.T) {
	fc := newFakeSchedulerCache()
	adapter := NewCacheAdapter(fc)

	pod := makePod("pod-1", "default")
	_, err := adapter.GetAvailablePlaceholderPod(pod)
	assert.Error(t, err)
}

func TestCacheAdapter_DeletePod_CleansLocalState(t *testing.T) {
	fc := newFakeSchedulerCache()
	adapter := NewCacheAdapter(fc)

	pod := makePod("pod-1", "default")
	podInfo := framework.MakeCachePodInfoWrapper().Pod(pod).Obj()

	// Assume and mark
	_ = adapter.AssumePod(podInfo)
	_ = adapter.MarkPodToDelete(pod, nil)

	// Delete should clean up local state
	_ = adapter.DeletePod(pod)

	isAssumed, _ := adapter.IsAssumedPod(pod)
	assert.False(t, isAssumed)

	marked, _ := adapter.IsPodMarkedToDelete(pod)
	assert.False(t, marked)
}
