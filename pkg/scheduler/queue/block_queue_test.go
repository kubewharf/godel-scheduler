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

package queue

import (
	"fmt"
	"reflect"
	"sync"
	"testing"
	"time"

	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/clock"

	commoncache "github.com/kubewharf/godel-scheduler/pkg/common/cache"
	framework "github.com/kubewharf/godel-scheduler/pkg/framework/api"
	"github.com/kubewharf/godel-scheduler/pkg/framework/utils"
	"github.com/kubewharf/godel-scheduler/pkg/plugins/unitqueuesort"
	"github.com/kubewharf/godel-scheduler/pkg/scheduler/apis/config"
	"github.com/kubewharf/godel-scheduler/pkg/scheduler/cache"
	podutil "github.com/kubewharf/godel-scheduler/pkg/util/pod"
)

func newFCFSUnitQueueSort() framework.UnitLessFunc {
	sort := &unitqueuesort.FCFS{}
	return sort.Less
}

func TestBlockQueue_Add(t *testing.T) {
	q := NewBlockQueue(nil, nil, nil, newFCFSUnitQueueSort())
	if err := q.Add(&medPriorityPod); err != nil {
		t.Errorf("add failed: %v", err)
	}
	if err := q.Add(&unschedulablePod); err != nil {
		t.Errorf("add failed: %v", err)
	}
	if err := q.Add(&highPriorityPod); err != nil {
		t.Errorf("add failed: %v", err)
	}
	if u, err := q.Pop(); err != nil || getOnePodInfo(u).Pod != &highPriorityPod {
		t.Errorf("Expected: %v after Pop, but got: %v", highPriorityPod.Name, getOnePodInfo(u).Pod.Name)
	}
	if u, err := q.Pop(); err != nil || getOnePodInfo(u).Pod != &medPriorityPod {
		t.Errorf("Expected: %v after Pop, but got: %v", medPriorityPod.Name, getOnePodInfo(u).Pod.Name)
	}
	if u, err := q.Pop(); err != nil || getOnePodInfo(u).Pod != &unschedulablePod {
		t.Errorf("Expected: %v after Pop, but got: %v", unschedulablePod.Name, getOnePodInfo(u).Pod.Name)
	}
}

func TestBlockQueue_AddWithReversePriorityLessFunc(t *testing.T) {
	q := NewBlockQueue(nil, nil, nil, newFCFSUnitQueueSort())
	if err := q.Add(&medPriorityPod); err != nil {
		t.Errorf("add failed: %v", err)
	}
	if err := q.Add(&highPriorityPod); err != nil {
		t.Errorf("add failed: %v", err)
	}
	if u, err := q.Pop(); err != nil || getOnePodInfo(u).Pod != &highPriorityPod {
		t.Errorf("Expected: %v after Pop, but got: %v", highPriorityPod.Name, getOnePodInfo(u).Pod.Name)
	}
	if u, err := q.Pop(); err != nil || getOnePodInfo(u).Pod != &medPriorityPod {
		t.Errorf("Expected: %v after Pop, but got: %v", medPriorityPod.Name, getOnePodInfo(u).Pod.Name)
	}
}

func TestBlockQueue_AddUnschedulableIfNotPresent(t *testing.T) {
	q := NewBlockQueue(nil, nil, nil, newFCFSUnitQueueSort())
	q.Add(&highPriNominatedPod)
	q.AddUnschedulableIfNotPresent(framework.NewQueuedUnitInfo(utils.GetUnitIdentifier(&highPriNominatedPod), framework.NewSinglePodUnit(newQueuedPodInfoForLookup(&highPriNominatedPod)), q.clock), q.SchedulingCycle()) // Must not add anything.
	q.AddUnschedulableIfNotPresent(framework.NewQueuedUnitInfo(utils.GetUnitIdentifier(&unschedulablePod), framework.NewSinglePodUnit(newQueuedPodInfoForLookup(&unschedulablePod)), q.clock), q.SchedulingCycle())
	if firstOrNil(q.readyQ.Peek().(*framework.QueuedUnitInfo)).Pod != &highPriNominatedPod {
		t.Errorf("Pod %v was expected in readyQ and at top.", highPriNominatedPod)
	}
	if obj, exist, err := q.readyQ.GetByKey(utils.GetUnitIdentifier(&highPriNominatedPod)); err != nil || !exist || obj == nil {
		t.Errorf("Unit %v was expected in readyQ.", obj.(*framework.QueuedUnitInfo))
	}
	if obj, exist, err := q.readyQ.GetByKey(utils.GetUnitIdentifier(&unschedulablePod)); err != nil || !exist || obj == nil {
		t.Errorf("Unit %v was expected in readyQ.", obj.(*framework.QueuedUnitInfo))
	}
}

// TestBlockQueue_AddUnschedulableIfNotPresent_Backoff tests the scenarios when
// AddUnschedulableIfNotPresent is called asynchronously.
// Pods in and before current scheduling cycle will be put back to activeQueue
// if we were trying to schedule them when we received move request.
func TestBlockQueue_AddUnschedulableIfNotPresent_Backoff(t *testing.T) {
	q := NewBlockQueue(nil, nil, nil, newFCFSUnitQueueSort(), WithClock(clock.NewFakeClock(time.Now())))
	totalNum := 10
	expectedPods := make([]v1.Pod, 0, totalNum)
	for i := 0; i < totalNum; i++ {
		priority := int32(i)
		p := v1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name:      fmt.Sprintf("pod%d", i),
				Namespace: fmt.Sprintf("ns%d", i),
				UID:       types.UID(fmt.Sprintf("upns%d", i)),
			},
			Spec: v1.PodSpec{
				Priority: &priority,
			},
		}
		expectedPods = append(expectedPods, p)
		// priority is to make pods ordered in the PriorityQueue
		q.Add(&p)
	}

	// Pop all pods except for the first one
	for i := totalNum - 1; i > 0; i-- {
		u, _ := q.Pop()
		if !reflect.DeepEqual(&expectedPods[i], getOnePodInfo(u).Pod) {
			t.Errorf("Unexpected pod. Expected: %v, got: %v", &expectedPods[i], getOnePodInfo(u).Pod)
		}
	}

	// move all pods to active queue when we were trying to schedule them
	q.MoveAllToActiveOrBackoffQueue("test")
	oldCycle := q.SchedulingCycle()

	u, _ := q.Pop()
	if !reflect.DeepEqual(&expectedPods[0], getOnePodInfo(u).Pod) {
		t.Errorf("Unexpected pod. Expected: %v, got: %v", &expectedPods[0], getOnePodInfo(u).Pod)
	}

	// mark pods[1] ~ pods[totalNum-1] as unschedulable and add them back
	for i := 1; i < totalNum; i++ {
		unschedulablePod := expectedPods[i].DeepCopy()
		unschedulablePod.Status = v1.PodStatus{
			Conditions: []v1.PodCondition{
				{
					Type:   v1.PodScheduled,
					Status: v1.ConditionFalse,
					Reason: v1.PodReasonUnschedulable,
				},
			},
		}

		unit := framework.NewQueuedUnitInfo(utils.GetUnitIdentifier(unschedulablePod), framework.NewSinglePodUnit(newQueuedPodInfoForLookup(unschedulablePod)), q.clock)
		// We should set the Attempts to 1.
		unit.Attempts = 1
		if err := q.AddUnschedulableIfNotPresent(unit, oldCycle); err != nil {
			t.Errorf("Failed to call AddUnschedulableIfNotPresent(%v): %v", unschedulablePod.Name, err)
		}
	}

	// Since there was a move request at the same cycle as "oldCycle", these pods
	// should be in the backoff queue.
	for i := 1; i < totalNum; i++ {
		unitKey := utils.GetUnitIdentifier(&expectedPods[i])
		obj, exists, _ := q.readyQ.GetByKey(unitKey)
		if !exists {
			t.Errorf("Expected %v to be added to readyQ.", expectedPods[i].Name)
		}
		if unit := obj.(*framework.QueuedUnitInfo); !q.boHandler.isUnitBackoff(unit) {
			t.Errorf("Expected %v to be backoff.", expectedPods[i].Name)
		}
	}
}

func TestBlockQueue_Pop(t *testing.T) {
	q := NewBlockQueue(nil, nil, nil, newFCFSUnitQueueSort())
	wg := sync.WaitGroup{}
	wg.Add(1)
	go func() {
		defer wg.Done()
		if u, err := q.Pop(); err != nil || getOnePodInfo(u).Pod != &medPriorityPod {
			t.Errorf("Expected: %v after Pop, but got: %v", medPriorityPod.Name, getOnePodInfo(u).Pod.Name)
		}
	}()
	q.Add(&medPriorityPod)
	wg.Wait()
}

func TestBlockQueue_Update(t *testing.T) {
	// TODO: remove UnitDequeueStatus
	q := NewBlockQueue(nil, nil, nil, newFCFSUnitQueueSort())
	q.Run()
	q.Update(nil, &highPriorityPod)
	if exists := queueHasPod(q.readyQ, &highPriorityPod); !exists {
		t.Errorf("Expected %v to be added to readyQ.", highPriorityPod.Name)
	}
	// Update highPriorityPod and add a nominatedNodeName to it.
	q.Update(&highPriorityPod, &highPriNominatedPod)
	if q.readyQ.Len() != 1 {
		t.Error("Expected only one item in readyQ.")
	}
	// Updating an unschedulable pod which is not in any of the two queues, should
	// add the pod to readyQ.
	q.Update(&unschedulablePod, &unschedulablePod)
	if exists := queueHasPod(q.readyQ, &unschedulablePod); !exists {
		t.Errorf("Expected %v to be added to readyQ.", unschedulablePod.Name)
	}
	// Updating a pod that is already in readyQ, should not change it.
	q.Update(&unschedulablePod, &unschedulablePod)
	if exists := queueHasPod(q.readyQ, &unschedulablePod); !exists {
		t.Errorf("Expected %v to be added to readyQ.", unschedulablePod.Name)
	}
	if u, err := q.Pop(); err != nil || getOnePodInfo(u).Pod != &highPriNominatedPod {
		t.Errorf("Expected: %v after Pop, but got: %v", highPriorityPod.Name, getOnePodInfo(u).Pod.Name)
	}
	// Updating a pod that is in unschedulableQ in a way that it may
	// become schedulable should add the pod to the readyQ.
	unit := framework.NewQueuedUnitInfo(utils.GetUnitIdentifier(&medPriorityPod), framework.NewSinglePodUnit(newQueuedPodInfoForLookup(&medPriorityPod)), q.clock)
	unit.Attempts = 1
	q.AddUnschedulableIfNotPresent(unit, q.SchedulingCycle())
	updatedPod := medPriorityPod.DeepCopy()
	updatedPod.GenerateName = "test"
	// wait backoff duration to make unschedulable pod skip backoff time, because the implementation of
	// priority queue has changed to follow backoff pattern for pods in unschedulable queue
	// ATTENTION: for BlockQueue, we use `q.Run()` instead of `time.Sleep` here.
	// time.Sleep(config.DefaultUnitInitialBackoffInSeconds * time.Second)
	if err := q.Update(&medPriorityPod, updatedPod); err != nil {
		t.Error(err)
	}
	if u, err := q.Pop(); err != nil || getOnePodInfo(u).Pod != updatedPod {
		t.Errorf("Expected: %v after Pop, but got: %v", updatedPod.Name, getOnePodInfo(u).Pod.Name)
	}
}

func TestBlockQueue_Delete(t *testing.T) {
	stop := make(chan struct{})
	defer close(stop)
	sCache := cache.New(commoncache.MakeCacheHandlerWrapper().
		ComponentName("").SchedulerType("").SubCluster(framework.DefaultSubCluster).
		PodAssumedTTL(time.Second).Period(10 * time.Second).StopCh(make(<-chan struct{})).
		EnableStore("PreemptionStore").
		Obj())
	q := NewBlockQueue(sCache, nil, nil, newFCFSUnitQueueSort())
	q.Update(&highPriorityPod, &highPriNominatedPod)
	q.Add(&unschedulablePod)
	if err := q.Delete(&highPriNominatedPod); err != nil {
		t.Errorf("delete failed: %v", err)
	}
	if exists := queueHasPod(q.readyQ, &unschedulablePod); !exists {
		t.Errorf("Expected %v to be in readyQ.", unschedulablePod.Name)
	}
	if exists := queueHasPod(q.readyQ, &highPriNominatedPod); exists {
		t.Errorf("Didn't expect %v to be in readyQ.", highPriorityPod.Name)
	}
	if err := q.Delete(&unschedulablePod); err != nil {
		t.Errorf("delete failed: %v", err)
	}
}

func TestBlockQueue_MoveAllToActiveOrBackoffQueue(t *testing.T) {
	q := NewBlockQueue(nil, nil, nil, newFCFSUnitQueueSort())
	q.Add(&medPriorityPod)
	{
		unit := framework.NewQueuedUnitInfo(utils.GetUnitIdentifier(&unschedulablePod), framework.NewSinglePodUnit(newQueuedPodInfoForLookup(&unschedulablePod)), q.clock)
		unit.Attempts = 1
		q.AddUnschedulableIfNotPresent(unit, q.SchedulingCycle())
	}
	{
		unit := framework.NewQueuedUnitInfo(utils.GetUnitIdentifier(&highPriorityPod), framework.NewSinglePodUnit(newQueuedPodInfoForLookup(&highPriorityPod)), q.clock)
		unit.Attempts = 1
		q.AddUnschedulableIfNotPresent(unit, q.SchedulingCycle())
	}
	if q.readyQ.Len() != 3 {
		t.Errorf("Expected 1 item to be in readyQ, but got %v", q.readyQ.Len())
	}

	if obj, exist, err := q.readyQ.GetByKey(utils.GetUnitIdentifier(&unschedulablePod)); err != nil || !exist || obj == nil {
		t.Errorf("Unit %v was expected in readyQ.", obj.(*framework.QueuedUnitInfo))
	} else if unit := obj.(*framework.QueuedUnitInfo); !q.boHandler.isUnitBackoff(unit) {
		t.Errorf("Unit %v was expected in backoff.", obj.(*framework.QueuedUnitInfo))
	}
	if obj, exist, err := q.readyQ.GetByKey(utils.GetUnitIdentifier(&highPriorityPod)); err != nil || !exist || obj == nil {
		t.Errorf("Unit %v was expected in readyQ.", obj.(*framework.QueuedUnitInfo))
	} else if unit := obj.(*framework.QueuedUnitInfo); !q.boHandler.isUnitBackoff(unit) {
		t.Errorf("Unit %v was expected in backoff.", obj.(*framework.QueuedUnitInfo))
	}
}

func TestBlockQueue_PendingPods(t *testing.T) {
	makeSet := func(pods []*v1.Pod) map[*v1.Pod]struct{} {
		pendingSet := map[*v1.Pod]struct{}{}
		for _, p := range pods {
			pendingSet[p] = struct{}{}
		}
		return pendingSet
	}

	q := NewBlockQueue(nil, nil, nil, newFCFSUnitQueueSort())
	q.Add(&medPriorityPod)
	q.AddUnschedulableIfNotPresent(framework.NewQueuedUnitInfo(utils.GetUnitIdentifier(&unschedulablePod), framework.NewSinglePodUnit(newQueuedPodInfoForLookup(&unschedulablePod)), q.clock), q.SchedulingCycle())
	q.AddUnschedulableIfNotPresent(framework.NewQueuedUnitInfo(utils.GetUnitIdentifier(&highPriorityPod), framework.NewSinglePodUnit(newQueuedPodInfoForLookup(&highPriorityPod)), q.clock), q.SchedulingCycle())

	expectedSet := makeSet([]*v1.Pod{&medPriorityPod, &unschedulablePod, &highPriorityPod})
	if !reflect.DeepEqual(expectedSet, makeSet(q.PendingPods())) {
		t.Error("Unexpected list of pending Pods.")
	}
	// Move all to active queue. We should still see the same set of pods.
	// q.MoveAllToActiveOrBackoffQueue("test")
	if !reflect.DeepEqual(expectedSet, makeSet(q.PendingPods())) {
		t.Error("Unexpected list of pending Pods...")
	}
}

func TestBlockQueue_NewWithOptions(t *testing.T) {
	q := NewBlockQueue(
		nil, nil, nil, newFCFSUnitQueueSort(),
		WithUnitInitialBackoffDuration(2*time.Second),
		WithPodMaxBackoffDuration(20*time.Second),
	)

	if q.boHandler.initialDuration != 2*time.Second {
		t.Errorf("Unexpected pod backoff initial duration. Expected: %v, got: %v", 2*time.Second, q.boHandler.initialDuration)
	}

	if q.boHandler.maxDuration != 20*time.Second {
		t.Errorf("Unexpected pod backoff max duration. Expected: %v, got: %v", 2*time.Second, q.boHandler.maxDuration)
	}
}

func TestBlockQueue_Close(t *testing.T) {
	q := NewBlockQueue(nil, nil, nil, newFCFSUnitQueueSort())
	wantErr := fmt.Errorf("scheduling queue is closed")
	wg := sync.WaitGroup{}
	wg.Add(1)
	go func() {
		defer wg.Done()
		pod, err := q.Pop()
		if err.Error() != wantErr.Error() {
			t.Errorf("Expected err %q from Pop() if queue is closed, but got %q", wantErr.Error(), err.Error())
		}
		if pod != nil {
			t.Errorf("Expected pod nil from Pop() if queue is closed, but got: %v", pod)
		}
	}()
	q.Close()
	wg.Wait()
}

// TestRecentlyTriedPodsGoBack tests that pods which are recently tried and are
// unschedulable go behind other pods with the same priority. This behavior
// ensures that an unschedulable pod does not block head of the queue when there
// are frequent events that move pods to the active queue.
func TestBlockQueue_RecentlyTriedPodsGoBack(t *testing.T) {
	c := clock.NewFakeClock(time.Now())
	q := NewBlockQueue(nil, nil, nil, newFCFSUnitQueueSort(), WithClock(c))
	// Add a few pods to priority queue.
	for i := 0; i < 5; i++ {
		p := v1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name:      fmt.Sprintf("test-pod-%v", i),
				Namespace: "ns1",
				UID:       types.UID(fmt.Sprintf("tp00%v", i)),
			},
			Spec: v1.PodSpec{
				Priority: &highPriority,
			},
			Status: v1.PodStatus{
				NominatedNodeName: "node1",
			},
		}
		q.Add(&p)
	}
	c.Step(time.Microsecond)
	// Simulate a pod being popped by the scheduler, determined unschedulable, and
	// then moved back to the active queue.
	u1, err := q.Pop()
	if err != nil {
		t.Errorf("Error while popping the head of the queue: %v", err)
	}
	// Update pod condition to unschedulable.
	podutil.UpdatePodCondition(&getOnePodInfo(u1).Pod.Status, &v1.PodCondition{
		Type:          v1.PodScheduled,
		Status:        v1.ConditionFalse,
		Reason:        v1.PodReasonUnschedulable,
		Message:       "fake scheduling failure",
		LastProbeTime: metav1.Now(),
	})
	// Put in the unschedulable queue.
	q.AddUnschedulableIfNotPresent(u1, q.SchedulingCycle())
	c.Step(config.DefaultUnitInitialBackoffInSeconds * time.Second)
	// Move all unschedulable pods to the active queue.
	// q.MoveAllToActiveOrBackoffQueue("test")

	// Simulation is over. Now let's pop all pods. The pod popped first should be
	// the last one we pop here.
	for i := 0; i < 5; i++ {
		u, err := q.Pop()
		if err != nil {
			t.Errorf("Error while popping pods from the queue: %v", err)
		}
		if (i == 4) != (getOnePodInfo(u1) == getOnePodInfo(u)) {
			t.Errorf("A pod tried before is not the last pod popped: i: %v, pod name: %v", i, getOnePodInfo(u).Pod.Name)
		}
	}
}

// [For BlockQueue]
// TestBlockQueue_PodFailedSchedulingMultipleTimesWillBlockNewerPod tests
// that a pod determined as unschedulable multiple times will block newer pod.
func TestBlockQueue_PodFailedSchedulingMultipleTimesWillBlockNewerPod(t *testing.T) {
	c := clock.NewFakeClock(time.Now())
	q := NewBlockQueue(nil, nil, nil, newFCFSUnitQueueSort(), WithClock(c))

	// Add an unschedulable pod to a priority queue.
	// This makes a situation that the pod was tried to schedule
	// and had been determined unschedulable so far
	unschedulablePod := v1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-pod-unscheduled",
			Namespace: "ns1",
			UID:       "tp001",
		},
		Spec: v1.PodSpec{
			Priority: &highPriority,
		},
		Status: v1.PodStatus{
			NominatedNodeName: "node1",
		},
	}

	// Update pod condition to unschedulable.
	podutil.UpdatePodCondition(&unschedulablePod.Status, &v1.PodCondition{
		Type:    v1.PodScheduled,
		Status:  v1.ConditionFalse,
		Reason:  v1.PodReasonUnschedulable,
		Message: "fake scheduling failure",
	})

	// Put in the unschedulable queue
	u := framework.NewSinglePodUnit(newQueuedPodInfoForLookup(&unschedulablePod))
	unit := framework.NewQueuedUnitInfo(u.GetKey(), u, q.clock)
	unit.Attempts = 1
	q.AddUnschedulableIfNotPresent(unit, q.SchedulingCycle())
	// Move clock to make the unschedulable pods complete backoff.
	c.Step(config.DefaultUnitInitialBackoffInSeconds*time.Second + time.Second)
	// Move all unschedulable pods to the active queue.
	// q.MoveAllToActiveOrBackoffQueue("test")

	// Simulate a pod being popped by the scheduler,
	// At this time, unschedulable pod should be popped.
	u1, err := q.Pop()
	if err != nil {
		t.Errorf("Error while popping the head of the queue: %v", err)
	}
	if getOnePodInfo(u1).Pod != &unschedulablePod {
		t.Errorf("Expected that test-pod-unscheduled was popped, got %v", getOnePodInfo(u1).Pod.Name)
	}

	// Assume newer pod was added just after unschedulable pod
	// being popped and before being pushed back to the queue.
	newerPod := v1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:              "test-newer-pod",
			Namespace:         "ns1",
			UID:               "tp002",
			CreationTimestamp: metav1.Now(),
		},
		Spec: v1.PodSpec{
			Priority: &highPriority,
		},
		Status: v1.PodStatus{
			NominatedNodeName: "node1",
		},
	}
	q.Add(&newerPod)

	// And then unschedulablePod was determined as unschedulable AGAIN.
	podutil.UpdatePodCondition(&unschedulablePod.Status, &v1.PodCondition{
		Type:    v1.PodScheduled,
		Status:  v1.ConditionFalse,
		Reason:  v1.PodReasonUnschedulable,
		Message: "fake scheduling failure",
	})

	// And then, put unschedulable pod to the unschedulable queue
	unit = framework.NewQueuedUnitInfo(u.GetKey(), u, q.clock)
	unit.Attempts = 2
	q.AddUnschedulableIfNotPresent(unit, q.SchedulingCycle())
	// Move clock to make the unschedulable pods complete backoff.
	c.Step(config.DefaultUnitInitialBackoffInSeconds*time.Second*2 + time.Second)
	// Move all unschedulable pods to the active queue.

	// For BlockQueue, we pop the olderPod anyway.
	{
		u2, err2 := q.Pop()
		if err2 != nil {
			t.Errorf("Error while popping the head of the queue: %v", err2)
		}
		if getOnePodInfo(u2).Pod != &unschedulablePod {
			t.Errorf("Expected that test-pod-unscheduled was popped, got %v", getOnePodInfo(u2).Pod.Name)
		}
	}
	// At this time, newerPod should be popped
	// because it is the oldest tried pod.
	{
		u2, err2 := q.Pop()
		if err2 != nil {
			t.Errorf("Error while popping the head of the queue: %v", err2)
		}
		if getOnePodInfo(u2).Pod != &newerPod {
			t.Errorf("Expected that test-newer-pod was popped, got %v", getOnePodInfo(u2).Pod.Name)
		}
	}
}

// TestHighPriorityBackoff tests that a high priority pod will block
// other pods if it is unschedulable
func TestBlockQueue_HighPriorityBackoff(t *testing.T) {
	q := NewBlockQueue(nil, nil, nil, newFCFSUnitQueueSort())

	midPod := v1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-midpod",
			Namespace: "ns1",
			UID:       types.UID("tp-mid"),
		},
		Spec: v1.PodSpec{
			Priority: &midPriority,
		},
		Status: v1.PodStatus{
			NominatedNodeName: "node1",
		},
	}
	highPod := v1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-highpod",
			Namespace: "ns1",
			UID:       types.UID("tp-high"),
		},
		Spec: v1.PodSpec{
			Priority: &highPriority,
		},
		Status: v1.PodStatus{
			NominatedNodeName: "node1",
		},
	}
	q.Add(&midPod)
	q.Add(&highPod)
	// Simulate a pod being popped by the scheduler, determined unschedulable, and
	// then moved back to the active queue.
	u, err := q.Pop()
	if err != nil {
		t.Errorf("Error while popping the head of the queue: %v", err)
	}
	if getOnePodInfo(u).Pod != &highPod {
		t.Errorf("Expected to get high priority pod, got: %v", getOnePodInfo(u))
	}
	// Update pod condition to unschedulable.
	podutil.UpdatePodCondition(&getOnePodInfo(u).Pod.Status, &v1.PodCondition{
		Type:    v1.PodScheduled,
		Status:  v1.ConditionFalse,
		Reason:  v1.PodReasonUnschedulable,
		Message: "fake scheduling failure",
	})
	// Put in the unschedulable queue.
	u.Attempts = 0
	q.AddUnschedulableIfNotPresent(u, q.SchedulingCycle())
	u, err = q.Pop()
	if err != nil {
		t.Errorf("Error while popping the head of the queue: %v", err)
	}
	if getOnePodInfo(u).Pod.Name != highPod.Name {
		t.Errorf("Expected to get high priority pod, got: %v", getOnePodInfo(u))
	}
}

// TestHighPriorityFlushUnschedulableQLeftover tests that pods will be moved to
// readyQ after one minutes if it is in unschedulableQ
func TestBlockQueue_HighPriorityFlushUnschedulableQLeftover(t *testing.T) {
	c := clock.NewFakeClock(time.Now())
	q := NewBlockQueue(nil, nil, nil, newFCFSUnitQueueSort(), WithClock(c))
	midPod := v1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-midpod",
			Namespace: "ns1",
			UID:       types.UID("tp-mid"),
		},
		Spec: v1.PodSpec{
			Priority: &midPriority,
		},
		Status: v1.PodStatus{
			NominatedNodeName: "node1",
		},
	}
	highPod := v1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-highpod",
			Namespace: "ns1",
			UID:       types.UID("tp-high"),
		},
		Spec: v1.PodSpec{
			Priority: &highPriority,
		},
		Status: v1.PodStatus{
			NominatedNodeName: "node1",
		},
	}

	// Update pod condition to highPod.
	podutil.UpdatePodCondition(&highPod.Status, &v1.PodCondition{
		Type:    v1.PodScheduled,
		Status:  v1.ConditionFalse,
		Reason:  v1.PodReasonUnschedulable,
		Message: "fake scheduling failure",
	})

	// Update pod condition to midPod.
	podutil.UpdatePodCondition(&midPod.Status, &v1.PodCondition{
		Type:    v1.PodScheduled,
		Status:  v1.ConditionFalse,
		Reason:  v1.PodReasonUnschedulable,
		Message: "fake scheduling failure",
	})

	{
		unit := framework.NewQueuedUnitInfo(utils.GetUnitIdentifier(&highPod), framework.NewSinglePodUnit(newQueuedPodInfoForLookup(&highPod)), q.clock)
		unit.Attempts = 1
		q.AddUnschedulableIfNotPresent(unit, q.SchedulingCycle())
		if obj, _, _ := q.readyQ.GetByKey(unit.UnitKey); obj != unit {
			t.Errorf("Expect: obj %v in readyQ is equal to unit %v\n", obj, unit)
		} else if unit = obj.(*framework.QueuedUnitInfo); !q.boHandler.isUnitBackoff(unit) {
			t.Errorf("Expect: unit %v is backoff", unit)
		}
	}
	{
		unit := framework.NewQueuedUnitInfo(utils.GetUnitIdentifier(&midPod), framework.NewSinglePodUnit(newQueuedPodInfoForLookup(&midPod)), q.clock)
		unit.Attempts = 1
		q.AddUnschedulableIfNotPresent(unit, q.SchedulingCycle())
		if obj, _, _ := q.readyQ.GetByKey(unit.UnitKey); obj != unit {
			t.Errorf("Expect: obj %v in readyQ is equal to unit %v\n", obj, unit)
		} else if unit = obj.(*framework.QueuedUnitInfo); !q.boHandler.isUnitBackoff(unit) {
			t.Errorf("Expect: unit %v is backoff", unit)
		}
	}

	c.Step(config.DefaultUnitInitialBackoffInSeconds*time.Second + time.Second)

	if u, err := q.Pop(); err != nil || getOnePodInfo(u).Pod != &highPod {
		t.Errorf("Expected: %v after Pop, but got: %v", highPriorityPod.Name, getOnePodInfo(u).Pod.Name)
	}
	if u, err := q.Pop(); err != nil || getOnePodInfo(u).Pod != &midPod {
		t.Errorf("Expected: %v after Pop, but got: %v", medPriorityPod.Name, getOnePodInfo(u).Pod.Name)
	}
}

type blockQueue_operation func(queue *BlockQueue, pInfo *framework.QueuedPodInfo)

var (
	blockQueue_addPodActiveQ = func(queue *BlockQueue, pInfo *framework.QueuedPodInfo) {
		unit := framework.NewSinglePodUnit(pInfo)
		queue.readyQ.Add(&framework.QueuedUnitInfo{UnitKey: unit.GetKey(), ScheduleUnit: unit, Timestamp: pInfo.Timestamp, QueuePriorityScore: float64(unit.GetPriority())})
	}
	blockQueue_updatePodActiveQ = func(queue *BlockQueue, pInfo *framework.QueuedPodInfo) {
		unit := framework.NewSinglePodUnit(pInfo)
		queue.readyQ.Update(nil, &framework.QueuedUnitInfo{UnitKey: unit.GetKey(), ScheduleUnit: unit, Timestamp: pInfo.Timestamp, QueuePriorityScore: float64(unit.GetPriority())})
	}
	blockQueue_addPodUnschedulableQ = func(queue *BlockQueue, pInfo *framework.QueuedPodInfo) {
		// Update pod condition to unschedulable.
		podutil.UpdatePodCondition(&pInfo.Pod.Status, &v1.PodCondition{
			Type:    v1.PodScheduled,
			Status:  v1.ConditionFalse,
			Reason:  v1.PodReasonUnschedulable,
			Message: "fake scheduling failure",
		})
		unit := framework.NewSinglePodUnit(pInfo)
		// TODO
		queue.readyQ.Update(nil, &framework.QueuedUnitInfo{UnitKey: unit.GetKey(), ScheduleUnit: unit, Timestamp: pInfo.Timestamp, QueuePriorityScore: float64(unit.GetPriority())})
	}
	blockQueue_addPodBackoffQ = func(queue *BlockQueue, pInfo *framework.QueuedPodInfo) {
		unit := framework.NewSinglePodUnit(pInfo)
		queue.readyQ.Add(&framework.QueuedUnitInfo{UnitKey: unit.GetKey(), ScheduleUnit: unit, Timestamp: pInfo.Timestamp, QueuePriorityScore: float64(unit.GetPriority())})
	}
	blockQueue_moveAllToActiveOrBackoffQ = func(queue *BlockQueue, _ *framework.QueuedPodInfo) {
		queue.MoveAllToActiveOrBackoffQueue("test")
	}
	blockQueue_flushBackoffQ = func(queue *BlockQueue, _ *framework.QueuedPodInfo) {
		queue.clock.(*clock.FakeClock).Step(20 * time.Second)
		// queue.flushBackoffQCompleted()
	}
	blockQueue_moveClockForward = func(queue *BlockQueue, _ *framework.QueuedPodInfo) {
		queue.clock.(*clock.FakeClock).Step(20 * time.Second)
	}
)

// TestPodTimestamp tests the operations related to QueuedPodInfo.
func TestBlockQueue_PodTimestamp(t *testing.T) {
	pod1 := &v1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-pod-1",
			Namespace: "ns1",
			UID:       types.UID("tp-1"),
		},
		Status: v1.PodStatus{
			NominatedNodeName: "node1",
		},
	}

	pod2 := &v1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-pod-2",
			Namespace: "ns2",
			UID:       types.UID("tp-2"),
		},
		Status: v1.PodStatus{
			NominatedNodeName: "node2",
		},
	}

	timestamp := time.Now()
	pInfo1 := &framework.QueuedPodInfo{
		Pod:       pod1,
		Timestamp: timestamp,
	}
	pInfo2 := &framework.QueuedPodInfo{
		Pod:       pod2,
		Timestamp: timestamp.Add(time.Second),
	}

	tests := []struct {
		name       string
		operations []blockQueue_operation
		operands   []*framework.QueuedPodInfo
		expected   []*framework.QueuedPodInfo
	}{
		{
			name: "add two pod to readyQ and sort them by the timestamp",
			operations: []blockQueue_operation{
				blockQueue_addPodActiveQ,
				blockQueue_addPodActiveQ,
			},
			operands: []*framework.QueuedPodInfo{pInfo2, pInfo1},
			expected: []*framework.QueuedPodInfo{pInfo1, pInfo2},
		},
		{
			name: "update two pod to readyQ and sort them by the timestamp",
			operations: []blockQueue_operation{
				blockQueue_updatePodActiveQ,
				blockQueue_updatePodActiveQ,
			},
			operands: []*framework.QueuedPodInfo{pInfo2, pInfo1},
			expected: []*framework.QueuedPodInfo{pInfo1, pInfo2},
		},
		{
			name: "add two pod to unschedulableQ then move them to readyQ and sort them by the timestamp",
			operations: []blockQueue_operation{
				blockQueue_addPodUnschedulableQ,
				blockQueue_addPodUnschedulableQ,
				blockQueue_moveClockForward,
				blockQueue_moveAllToActiveOrBackoffQ,
			},
			operands: []*framework.QueuedPodInfo{pInfo2, pInfo1, nil, nil},
			expected: []*framework.QueuedPodInfo{pInfo1, pInfo2},
		},
		{
			name: "add one pod to BackoffQ and move it to readyQ",
			operations: []blockQueue_operation{
				blockQueue_addPodActiveQ,
				blockQueue_addPodBackoffQ,
				blockQueue_flushBackoffQ,
				blockQueue_moveAllToActiveOrBackoffQ,
			},
			operands: []*framework.QueuedPodInfo{pInfo2, pInfo1, nil, nil},
			expected: []*framework.QueuedPodInfo{pInfo1, pInfo2},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			queue := NewBlockQueue(nil, nil, nil, newFCFSUnitQueueSort(), WithClock(clock.NewFakeClock(timestamp)))
			var podInfoList []*framework.QueuedPodInfo

			for i, op := range test.operations {
				op(queue, test.operands[i])
			}

			for i := 0; i < len(test.expected); i++ {
				if u, err := queue.readyQ.Pop(); err != nil {
					t.Errorf("Error while popping the head of the queue: %v", err)
				} else {
					pInfo := getOnePodInfo(u.(*framework.QueuedUnitInfo))
					podInfoList = append(podInfoList, pInfo)
				}
			}

			if !reflect.DeepEqual(test.expected, podInfoList) {
				t.Errorf("Unexpected QueuedPodInfo list. Expected: %v, got: %v",
					test.expected, podInfoList)
			}
		})
	}
}

// TestPerPodSchedulingMetrics makes sure pod schedule attempts is updated correctly while
// initialAttemptTimestamp stays the same during multiple add/pop operations.
func TestBlockQueue_PerPodSchedulingMetrics(t *testing.T) {
	pod := &v1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-pod",
			Namespace: "test-ns",
			UID:       types.UID("test-uid"),
		},
	}
	timestamp := time.Now()

	// Case 1: A pod is created and scheduled after 1 attempt. The queue operations are
	// Add -> Pop.
	c := clock.NewFakeClock(timestamp)
	queue := NewBlockQueue(nil, nil, nil, newFCFSUnitQueueSort(), WithClock(c))
	queue.Add(pod)
	u, err := queue.Pop()
	if err != nil {
		t.Fatalf("Failed to pop a pod %v", err)
	}
	checkPerUnitSchedulingMetrics("Attempt once", t, u, 1, timestamp)

	// Case 2: A pod is created and scheduled after 2 attempts. The queue operations are
	// Add -> Pop -> AddUnschedulableIfNotPresent -> flushUnschedulableQLeftover -> Pop.
	c = clock.NewFakeClock(timestamp)
	queue = NewBlockQueue(nil, nil, nil, newFCFSUnitQueueSort(), WithClock(c))
	queue.Add(pod)
	u, err = queue.Pop()
	if err != nil {
		t.Fatalf("Failed to pop a pod %v", err)
	}
	queue.AddUnschedulableIfNotPresent(u, 1)
	// Override clock to exceed the unschedulableQTimeInterval so that unschedulable pods
	// will be moved to readyQ
	c.SetTime(timestamp.Add(unschedulableQTimeInterval + 1))

	u, err = queue.Pop()
	if err != nil {
		t.Fatalf("Failed to pop a pod %v", err)
	}
	checkPerUnitSchedulingMetrics("Attempt twice", t, u, 2, timestamp)

	// Case 3: Similar to case 2, but before the second pop, call update, the queue operations are
	// Add -> Pop -> AddUnschedulableIfNotPresent -> flushUnschedulableQLeftover -> Update -> Pop.
	c = clock.NewFakeClock(timestamp)
	queue = NewBlockQueue(nil, nil, nil, newFCFSUnitQueueSort(), WithClock(c))
	queue.Add(pod)
	u, err = queue.Pop()
	if err != nil {
		t.Fatalf("Failed to pop a pod %v", err)
	}
	queue.AddUnschedulableIfNotPresent(u, 1)
	// Override clock to exceed the unschedulableQTimeInterval so that unschedulable pods
	// will be moved to readyQ
	c.SetTime(timestamp.Add(unschedulableQTimeInterval + 1))

	newPod := pod.DeepCopy()
	newPod.Generation = 1
	queue.Update(pod, newPod)
	u, err = queue.Pop()
	if err != nil {
		t.Fatalf("Failed to pop a pod %v", err)
	}
	checkPerUnitSchedulingMetrics("Attempt twice with update", t, u, 2, timestamp)
}

func TestBlockQueue_BackOffFlow(t *testing.T) {
	cl := clock.NewFakeClock(time.Now())
	q := NewBlockQueue(nil, nil, nil, newFCFSUnitQueueSort(), WithClock(cl))
	steps := []struct {
		wantBackoff time.Duration
	}{
		{wantBackoff: 10 * time.Second},
		{wantBackoff: 20 * time.Second},
		{wantBackoff: 40 * time.Second},
		{wantBackoff: 80 * time.Second},
		{wantBackoff: 160 * time.Second},
		{wantBackoff: 300 * time.Second},
		{wantBackoff: 300 * time.Second},
		{wantBackoff: 300 * time.Second},
		{wantBackoff: 300 * time.Second},
		{wantBackoff: 300 * time.Second},
	}
	pod := &v1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-pod",
			Namespace: "test-ns",
			UID:       "test-uid",
		},
	}
	// podID := podutil.GetPodKey(pod)
	if err := q.Add(pod); err != nil {
		t.Fatal(err)
	}

	for i, step := range steps {
		t.Run(fmt.Sprintf("step %d", i), func(t *testing.T) {
			timestamp := cl.Now()
			// Simulate schedule attempt.
			u, err := q.Pop()
			if err != nil {
				t.Fatal(err)
			}
			if u.Attempts != i+1 {
				t.Errorf("got attempts %d, want %d", u.Attempts, i+1)
			}
			if err := q.AddUnschedulableIfNotPresent(u, int64(i)); err != nil {
				t.Fatal(err)
			}

			if obj, _, _ := q.readyQ.GetByKey(utils.GetUnitIdentifier(pod)); obj == nil {
				t.Errorf("Expect: obj %v in readyQ", obj)
			} else if unit := obj.(*framework.QueuedUnitInfo); !q.boHandler.isUnitBackoff(unit) {
				t.Errorf("Expect: unit %v is backoff", unit)
			}

			// Check backoff duration.
			deadline := q.boHandler.getBackoffTime(u)
			backoff := deadline.Sub(timestamp)
			if backoff != step.wantBackoff {
				t.Errorf("got backoff %s, want %s", backoff, step.wantBackoff)
			}

			// Simulate routine that continuously flushes the backoff queue.
			cl.Step(time.Millisecond)
			if obj, _, _ := q.readyQ.GetByKey(utils.GetUnitIdentifier(pod)); obj == nil {
				t.Errorf("Expect: obj %v in readyQ", obj)
			} else if unit := obj.(*framework.QueuedUnitInfo); !q.boHandler.isUnitBackoff(unit) {
				t.Errorf("Expect: unit %v is backoff", unit)
			}

			// Moved out of the backoff queue after timeout.
			cl.Step(backoff)
			if obj, _, _ := q.readyQ.GetByKey(utils.GetUnitIdentifier(pod)); obj == nil {
				t.Errorf("Expect: obj %v in readyQ", obj)
			} else if unit := obj.(*framework.QueuedUnitInfo); q.boHandler.isUnitBackoff(unit) {
				t.Errorf("Expect: unit %v is not backoff", unit)
			}
		})
	}
}
