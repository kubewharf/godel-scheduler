/*
Copyright 2018 The Kubernetes Authors.

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

	framework "github.com/kubewharf/godel-scheduler/pkg/framework/api"
	"github.com/kubewharf/godel-scheduler/pkg/framework/utils"
	"github.com/kubewharf/godel-scheduler/pkg/plugins/unitqueuesort"
	"github.com/kubewharf/godel-scheduler/pkg/scheduler/apis/config"
	"github.com/kubewharf/godel-scheduler/pkg/scheduler/cache"
	"github.com/kubewharf/godel-scheduler/pkg/scheduler/cache/handler"
	podutil "github.com/kubewharf/godel-scheduler/pkg/util/pod"
)

var (
	lowPriority, midPriority, highPriority                                 = int32(0), int32(100), int32(1000)
	mediumPriority                                                         = (lowPriority + highPriority) / 2
	highPriorityPod, highPriNominatedPod, medPriorityPod, unschedulablePod = v1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "hpp",
			Namespace: "ns1",
			UID:       "hppns1",
		},
		Spec: v1.PodSpec{
			Priority: &highPriority,
		},
	},
		v1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "hpp",
				Namespace: "ns1",
				UID:       "hppns1",
			},
			Spec: v1.PodSpec{
				Priority: &highPriority,
			},
			Status: v1.PodStatus{
				NominatedNodeName: "node1",
			},
		},
		v1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "mpp",
				Namespace: "ns2",
				UID:       "mppns2",
				Annotations: map[string]string{
					"annot2": "val2",
				},
			},
			Spec: v1.PodSpec{
				Priority: &mediumPriority,
			},
			Status: v1.PodStatus{
				NominatedNodeName: "node1",
			},
		},
		v1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "up",
				Namespace: "ns1",
				UID:       "upns1",
				Annotations: map[string]string{
					"annot2": "val2",
				},
			},
			Spec: v1.PodSpec{
				Priority: &lowPriority,
			},
			Status: v1.PodStatus{
				Conditions: []v1.PodCondition{
					{
						Type:   v1.PodScheduled,
						Status: v1.ConditionFalse,
						Reason: v1.PodReasonUnschedulable,
					},
				},
				NominatedNodeName: "node1",
			},
		}
)

func newDefaultUnitQueueSort() framework.UnitLessFunc {
	sort := &unitqueuesort.DefaultUnitQueueSort{}
	return sort.Less
}

func getUnschedulablePod(p *PriorityQueue, pod *v1.Pod) *v1.Pod {
	u, exist, _ := p.unschedulableQ.GetByKey(utils.GetUnitIdentifier(pod))
	if exist {
		pInfo := u.(*framework.QueuedUnitInfo).GetPod(newQueuedPodInfoForLookup(pod))
		if pInfo != nil {
			return pInfo.Pod
		}
	}
	return nil
}

func getOnePodInfo(unitInfo *framework.QueuedUnitInfo) *framework.QueuedPodInfo {
	return unitInfo.GetPods()[0]
}

func queueHasPod(queue SubQueue, pod *v1.Pod) bool {
	u, exist, _ := queue.GetByKey(utils.GetUnitIdentifier(pod))
	return exist && u.(*framework.QueuedUnitInfo).GetPod(newQueuedPodInfoForLookup(pod)) != nil
}

func TestPriorityQueue_Add(t *testing.T) {
	q := NewPriorityQueue(nil, nil, nil, newDefaultUnitQueueSort())
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

func TestPriorityQueue_AddWithReversePriorityLessFunc(t *testing.T) {
	q := NewPriorityQueue(nil, nil, nil, newDefaultUnitQueueSort())
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

func TestPriorityQueue_AddUnschedulableIfNotPresent(t *testing.T) {
	q := NewPriorityQueue(nil, nil, nil, newDefaultUnitQueueSort())
	q.Add(&highPriNominatedPod)
	q.AddUnschedulableIfNotPresent(framework.NewQueuedUnitInfo(utils.GetUnitIdentifier(&highPriNominatedPod), framework.NewSinglePodUnit(newQueuedPodInfoForLookup(&highPriNominatedPod)), q.clock), q.SchedulingCycle()) // Must not add anything.
	q.AddUnschedulableIfNotPresent(framework.NewQueuedUnitInfo(utils.GetUnitIdentifier(&unschedulablePod), framework.NewSinglePodUnit(newQueuedPodInfoForLookup(&unschedulablePod)), q.clock), q.SchedulingCycle())
	if getUnschedulablePod(q, &unschedulablePod) != &unschedulablePod {
		t.Errorf("Pod %v was not found in the unschedulableQ.", unschedulablePod.Name)
	}
}

// TestPriorityQueue_AddUnschedulableIfNotPresent_Backoff tests the scenarios when
// AddUnschedulableIfNotPresent is called asynchronously.
// Pods in and before current scheduling cycle will be put back to activeQueue
// if we were trying to schedule them when we received move request.
func TestPriorityQueue_AddUnschedulableIfNotPresent_Backoff(t *testing.T) {
	q := NewPriorityQueue(nil, nil, nil, newDefaultUnitQueueSort(), WithClock(clock.NewFakeClock(time.Now())))
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

		if err := q.AddUnschedulableIfNotPresent(framework.NewQueuedUnitInfo(utils.GetUnitIdentifier(unschedulablePod), framework.NewSinglePodUnit(newQueuedPodInfoForLookup(unschedulablePod)), q.clock), oldCycle); err != nil {
			t.Errorf("Failed to call AddUnschedulableIfNotPresent(%v): %v", unschedulablePod.Name, err)
		}
	}

	// Since there was a move request at the same cycle as "oldCycle", these pods
	// should be in the backoff queue.
	for i := 1; i < totalNum; i++ {
		unitKey := utils.GetUnitIdentifier(&expectedPods[i])
		if _, exists, _ := q.backoffQ.GetByKey(unitKey); !exists {
			t.Errorf("Expected %v to be added to backoffQ.", expectedPods[i].Name)
		}
	}
}

func TestPriorityQueue_Pop(t *testing.T) {
	q := NewPriorityQueue(nil, nil, nil, newDefaultUnitQueueSort())
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

func TestPriorityQueue_Update(t *testing.T) {
	// TODO: remove UnitDequeueStatus
	q := NewPriorityQueue(nil, nil, nil, newDefaultUnitQueueSort())
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
	if q.unschedulableQ.Len() != 0 {
		t.Error("Expected unschedulableQ to be empty.")
	}
	if exists := queueHasPod(q.readyQ, &unschedulablePod); !exists {
		t.Errorf("Expected %v to be added to readyQ.", unschedulablePod.Name)
	}
	if u, err := q.Pop(); err != nil || getOnePodInfo(u).Pod != &highPriNominatedPod {
		t.Errorf("Expected: %v after Pop, but got: %v", highPriorityPod.Name, getOnePodInfo(u).Pod.Name)
	}
	// Updating a pod that is in unschedulableQ in a way that it may
	// become schedulable should add the pod to the readyQ.
	q.AddUnschedulableIfNotPresent(framework.NewQueuedUnitInfo(utils.GetUnitIdentifier(&medPriorityPod), framework.NewSinglePodUnit(newQueuedPodInfoForLookup(&medPriorityPod)), q.clock), q.SchedulingCycle())
	if q.unschedulableQ.Len() != 1 {
		t.Error("Expected unschedulableQ to be 1.")
	}
	updatedPod := medPriorityPod.DeepCopy()
	updatedPod.GenerateName = "test" // used to force an update on an old pod
	// wait backoff duration to make unschedulable pod skip backoff time, because the implementation of
	// priority queue has changed to follow backoff pattern for pods in unschedulable queue
	time.Sleep(config.DefaultUnitInitialBackoffInSeconds * time.Second)
	if err := q.Update(&medPriorityPod, updatedPod); err != nil {
		t.Error(err)
	}
	if u, err := q.Pop(); err != nil || getOnePodInfo(u).Pod != updatedPod {
		t.Errorf("Expected: %v after Pop, but got: %v", updatedPod.Name, getOnePodInfo(u).Pod.Name)
	}
}

func TestPriorityQueue_Delete(t *testing.T) {
	stop := make(chan struct{})
	defer close(stop)
	sCache := cache.New(handler.MakeCacheHandlerWrapper().
		SchedulerName("").SchedulerType("").SubCluster(framework.DefaultSubCluster).
		TTL(time.Second).Period(10 * time.Second).StopCh(make(<-chan struct{})).
		EnableStore("PreemptionStore").
		Obj())
	q := NewPriorityQueue(sCache, nil, nil, newDefaultUnitQueueSort())
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

func TestPriorityQueue_MoveAllToActiveOrBackoffQueue(t *testing.T) {
	q := NewPriorityQueue(nil, nil, nil, newDefaultUnitQueueSort())
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
	q.MoveAllToActiveOrBackoffQueue("test")
	if q.readyQ.Len() != 1 {
		t.Errorf("Expected 1 item to be in readyQ, but got %v", q.readyQ.Len())
	}
	if q.backoffQ.Len() != 2 {
		t.Errorf("Expected 2 items to be in backoffQ, but got %v", q.backoffQ.Len())
	}
}

// TestPriorityQueue_AssignedPodAdded tests AssignedPodAdded. It checks that
// when a pod with pod affinity is in unschedulableQ and another pod with a
// matching label is added, the unschedulable pod is moved to readyQ.
func TestPriorityQueue_AssignedPodAdded(t *testing.T) {
	affinityPod := unschedulablePod.DeepCopy()
	affinityPod.Name = "afp"
	affinityPod.Spec = v1.PodSpec{
		Affinity: &v1.Affinity{
			PodAffinity: &v1.PodAffinity{
				RequiredDuringSchedulingIgnoredDuringExecution: []v1.PodAffinityTerm{
					{
						LabelSelector: &metav1.LabelSelector{
							MatchExpressions: []metav1.LabelSelectorRequirement{
								{
									Key:      "service",
									Operator: metav1.LabelSelectorOpIn,
									Values:   []string{"securityscan", "value2"},
								},
							},
						},
						TopologyKey: "region",
					},
				},
			},
		},
		Priority: &mediumPriority,
	}
	labelPod := v1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "lbp",
			Namespace: affinityPod.Namespace,
			Labels:    map[string]string{"service": "securityscan"},
		},
		Spec: v1.PodSpec{NodeName: "machine1"},
	}

	c := clock.NewFakeClock(time.Now())
	q := NewPriorityQueue(nil, nil, nil, newDefaultUnitQueueSort(), WithClock(c))
	q.Add(&medPriorityPod)
	// Add a couple of pods to the unschedulableQ.
	q.AddUnschedulableIfNotPresent(framework.NewQueuedUnitInfo(utils.GetUnitIdentifier(&unschedulablePod), framework.NewSinglePodUnit(newQueuedPodInfoForLookup(&unschedulablePod)), q.clock), q.SchedulingCycle())
	q.AddUnschedulableIfNotPresent(framework.NewQueuedUnitInfo(utils.GetUnitIdentifier(affinityPod), framework.NewSinglePodUnit(newQueuedPodInfoForLookup(affinityPod)), q.clock), q.SchedulingCycle())

	// Move clock to make the unschedulable pods complete backoff.
	c.Step(config.DefaultUnitInitialBackoffInSeconds*time.Second + time.Second)
	// Simulate addition of an assigned pod. The pod has matching labels for
	// affinityPod. So, affinityPod should go to readyQ.
	q.AssignedPodAdded(&labelPod)
	if getUnschedulablePod(q, affinityPod) != nil {
		t.Error("affinityPod is still in the unschedulableQ.")
	}
	if exists := queueHasPod(q.readyQ, affinityPod); !exists {
		t.Error("affinityPod is not moved to readyQ.")
	}
	// Check that the other pod is still in the unschedulableQ.
	if getUnschedulablePod(q, &unschedulablePod) == nil {
		t.Error("unschedulablePod is not in the unschedulableQ.")
	}
}

func TestPriorityQueue_PendingPods(t *testing.T) {
	makeSet := func(pods []*v1.Pod) map[*v1.Pod]struct{} {
		pendingSet := map[*v1.Pod]struct{}{}
		for _, p := range pods {
			pendingSet[p] = struct{}{}
		}
		return pendingSet
	}

	q := NewPriorityQueue(nil, nil, nil, newDefaultUnitQueueSort())
	q.Add(&medPriorityPod)
	q.AddUnschedulableIfNotPresent(framework.NewQueuedUnitInfo(utils.GetUnitIdentifier(&unschedulablePod), framework.NewSinglePodUnit(newQueuedPodInfoForLookup(&unschedulablePod)), q.clock), q.SchedulingCycle())
	q.AddUnschedulableIfNotPresent(framework.NewQueuedUnitInfo(utils.GetUnitIdentifier(&highPriorityPod), framework.NewSinglePodUnit(newQueuedPodInfoForLookup(&highPriorityPod)), q.clock), q.SchedulingCycle())

	expectedSet := makeSet([]*v1.Pod{&medPriorityPod, &unschedulablePod, &highPriorityPod})
	if !reflect.DeepEqual(expectedSet, makeSet(q.PendingPods())) {
		t.Error("Unexpected list of pending Pods.")
	}
	// Move all to active queue. We should still see the same set of pods.
	q.MoveAllToActiveOrBackoffQueue("test")
	if !reflect.DeepEqual(expectedSet, makeSet(q.PendingPods())) {
		t.Error("Unexpected list of pending Pods...")
	}
}

func TestPriorityQueue_NewWithOptions(t *testing.T) {
	q := NewPriorityQueue(
		nil, nil, nil, newDefaultUnitQueueSort(),
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

func TestPriorityQueue_Close(t *testing.T) {
	q := NewPriorityQueue(nil, nil, nil, newDefaultUnitQueueSort())
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
func TestPriorityQueue_RecentlyTriedPodsGoBack(t *testing.T) {
	c := clock.NewFakeClock(time.Now())
	q := NewPriorityQueue(nil, nil, nil, newDefaultUnitQueueSort(), WithClock(c))
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
	q.MoveAllToActiveOrBackoffQueue("test")
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

// TestPodFailedSchedulingMultipleTimesDoesNotBlockNewerPod tests
// that a pod determined as unschedulable multiple times doesn't block any newer pod.
// This behavior ensures that an unschedulable pod does not block head of the queue when there
// are frequent events that move pods to the active queue.
func TestPriorityQueue_PodFailedSchedulingMultipleTimesDoesNotBlockNewerPod(t *testing.T) {
	c := clock.NewFakeClock(time.Now())
	q := NewPriorityQueue(nil, nil, nil, newDefaultUnitQueueSort(), WithClock(c))

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

	u := framework.NewSinglePodUnit(newQueuedPodInfoForLookup(&unschedulablePod))

	// Put in the unschedulable queue
	q.AddUnschedulableIfNotPresent(framework.NewQueuedUnitInfo(u.GetKey(), u, q.clock), q.SchedulingCycle())
	// Move clock to make the unschedulable pods complete backoff.
	c.Step(config.DefaultUnitInitialBackoffInSeconds*time.Second + time.Second)
	// Move all unschedulable pods to the active queue.
	q.MoveAllToActiveOrBackoffQueue("test")

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
	q.AddUnschedulableIfNotPresent(framework.NewQueuedUnitInfo(u.GetKey(), u, q.clock), q.SchedulingCycle())
	// Move clock to make the unschedulable pods complete backoff.
	c.Step(config.DefaultUnitInitialBackoffInSeconds*time.Second + time.Second)
	// Move all unschedulable pods to the active queue.
	q.MoveAllToActiveOrBackoffQueue("test")

	// At this time, newerPod should be popped
	// because it is the oldest tried pod.
	u2, err2 := q.Pop()
	if err2 != nil {
		t.Errorf("Error while popping the head of the queue: %v", err2)
	}
	if getOnePodInfo(u2).Pod != &newerPod {
		t.Errorf("Expected that test-newer-pod was popped, got %v", getOnePodInfo(u2).Pod.Name)
	}
}

// TestHighPriorityBackoff tests that a high priority pod does not block
// other pods if it is unschedulable
func TestPriorityQueue_HighPriorityBackoff(t *testing.T) {
	q := NewPriorityQueue(nil, nil, nil, newDefaultUnitQueueSort())

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
	q.AddUnschedulableIfNotPresent(u, q.SchedulingCycle())
	// Move all unschedulable pods to the active queue.
	q.MoveAllToActiveOrBackoffQueue("test")

	u, err = q.Pop()
	if err != nil {
		t.Errorf("Error while popping the head of the queue: %v", err)
	}
	if getOnePodInfo(u).Pod != &midPod {
		t.Errorf("Expected to get mid priority pod, got: %v", getOnePodInfo(u))
	}
}

// TestHighPriorityFlushUnschedulableQLeftover tests that pods will be moved to
// readyQ after one minutes if it is in unschedulableQ
func TestPriorityQueue_HighPriorityFlushUnschedulableQLeftover(t *testing.T) {
	c := clock.NewFakeClock(time.Now())
	q := NewPriorityQueue(nil, nil, nil, newDefaultUnitQueueSort(), WithClock(c))
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

	q.AddUnschedulableIfNotPresent(framework.NewQueuedUnitInfo(utils.GetUnitIdentifier(&highPod), framework.NewSinglePodUnit(newQueuedPodInfoForLookup(&highPod)), q.clock), q.SchedulingCycle())
	q.AddUnschedulableIfNotPresent(framework.NewQueuedUnitInfo(utils.GetUnitIdentifier(&midPod), framework.NewSinglePodUnit(newQueuedPodInfoForLookup(&midPod)), q.clock), q.SchedulingCycle())
	c.Step(unschedulableQTimeInterval + time.Second)
	q.flushUnschedulableQLeftover()

	if u, err := q.Pop(); err != nil || getOnePodInfo(u).Pod != &highPod {
		t.Errorf("Expected: %v after Pop, but got: %v", highPriorityPod.Name, getOnePodInfo(u).Pod.Name)
	}
	if u, err := q.Pop(); err != nil || getOnePodInfo(u).Pod != &midPod {
		t.Errorf("Expected: %v after Pop, but got: %v", medPriorityPod.Name, getOnePodInfo(u).Pod.Name)
	}
}

type operation func(queue *PriorityQueue, pInfo *framework.QueuedPodInfo)

var (
	addPodActiveQ = func(queue *PriorityQueue, pInfo *framework.QueuedPodInfo) {
		unit := framework.NewSinglePodUnit(pInfo)
		queue.readyQ.Add(&framework.QueuedUnitInfo{UnitKey: unit.GetKey(), ScheduleUnit: unit, Timestamp: pInfo.Timestamp, QueuePriorityScore: float64(unit.GetPriority())})
	}
	updatePodActiveQ = func(queue *PriorityQueue, pInfo *framework.QueuedPodInfo) {
		unit := framework.NewSinglePodUnit(pInfo)
		queue.readyQ.Update(nil, &framework.QueuedUnitInfo{UnitKey: unit.GetKey(), ScheduleUnit: unit, Timestamp: pInfo.Timestamp, QueuePriorityScore: float64(unit.GetPriority())})
	}
	addPodUnschedulableQ = func(queue *PriorityQueue, pInfo *framework.QueuedPodInfo) {
		// Update pod condition to unschedulable.
		podutil.UpdatePodCondition(&pInfo.Pod.Status, &v1.PodCondition{
			Type:    v1.PodScheduled,
			Status:  v1.ConditionFalse,
			Reason:  v1.PodReasonUnschedulable,
			Message: "fake scheduling failure",
		})
		unit := framework.NewSinglePodUnit(pInfo)
		queue.unschedulableQ.Update(nil, &framework.QueuedUnitInfo{UnitKey: unit.GetKey(), ScheduleUnit: unit, Timestamp: pInfo.Timestamp, QueuePriorityScore: float64(unit.GetPriority())})
	}
	addPodBackoffQ = func(queue *PriorityQueue, pInfo *framework.QueuedPodInfo) {
		unit := framework.NewSinglePodUnit(pInfo)
		queue.backoffQ.Add(&framework.QueuedUnitInfo{UnitKey: unit.GetKey(), ScheduleUnit: unit, Timestamp: pInfo.Timestamp, QueuePriorityScore: float64(unit.GetPriority())})
	}
	moveAllToActiveOrBackoffQ = func(queue *PriorityQueue, _ *framework.QueuedPodInfo) {
		queue.MoveAllToActiveOrBackoffQueue("test")
	}
	flushBackoffQ = func(queue *PriorityQueue, _ *framework.QueuedPodInfo) {
		queue.clock.(*clock.FakeClock).Step(20 * time.Second)
		queue.flushBackoffQCompleted()
	}
	moveClockForward = func(queue *PriorityQueue, _ *framework.QueuedPodInfo) {
		queue.clock.(*clock.FakeClock).Step(20 * time.Second)
	}
)

// TestPodTimestamp tests the operations related to QueuedPodInfo.
func TestPriorityQueue_PodTimestamp(t *testing.T) {
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
		operations []operation
		operands   []*framework.QueuedPodInfo
		expected   []*framework.QueuedPodInfo
	}{
		{
			name: "add two pod to readyQ and sort them by the timestamp",
			operations: []operation{
				addPodActiveQ,
				addPodActiveQ,
			},
			operands: []*framework.QueuedPodInfo{pInfo2, pInfo1},
			expected: []*framework.QueuedPodInfo{pInfo1, pInfo2},
		},
		{
			name: "update two pod to readyQ and sort them by the timestamp",
			operations: []operation{
				updatePodActiveQ,
				updatePodActiveQ,
			},
			operands: []*framework.QueuedPodInfo{pInfo2, pInfo1},
			expected: []*framework.QueuedPodInfo{pInfo1, pInfo2},
		},
		{
			name: "add two pod to unschedulableQ then move them to readyQ and sort them by the timestamp",
			operations: []operation{
				addPodUnschedulableQ,
				addPodUnschedulableQ,
				moveClockForward,
				moveAllToActiveOrBackoffQ,
			},
			operands: []*framework.QueuedPodInfo{pInfo2, pInfo1, nil, nil},
			expected: []*framework.QueuedPodInfo{pInfo1, pInfo2},
		},
		{
			name: "add one pod to BackoffQ and move it to readyQ",
			operations: []operation{
				addPodActiveQ,
				addPodBackoffQ,
				flushBackoffQ,
				moveAllToActiveOrBackoffQ,
			},
			operands: []*framework.QueuedPodInfo{pInfo2, pInfo1, nil, nil},
			expected: []*framework.QueuedPodInfo{pInfo1, pInfo2},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			queue := NewPriorityQueue(nil, nil, nil, newDefaultUnitQueueSort(), WithClock(clock.NewFakeClock(timestamp)))
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
func TestPriorityQueue_PerPodSchedulingMetrics(t *testing.T) {
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
	queue := NewPriorityQueue(nil, nil, nil, newDefaultUnitQueueSort(), WithClock(c))
	queue.Add(pod)
	u, err := queue.Pop()
	if err != nil {
		t.Fatalf("Failed to pop a pod %v", err)
	}
	checkPerUnitSchedulingMetrics("Attempt once", t, u, 1, timestamp)

	// Case 2: A pod is created and scheduled after 2 attempts. The queue operations are
	// Add -> Pop -> AddUnschedulableIfNotPresent -> flushUnschedulableQLeftover -> Pop.
	c = clock.NewFakeClock(timestamp)
	queue = NewPriorityQueue(nil, nil, nil, newDefaultUnitQueueSort(), WithClock(c))
	queue.Add(pod)
	u, err = queue.Pop()
	if err != nil {
		t.Fatalf("Failed to pop a pod %v", err)
	}
	queue.AddUnschedulableIfNotPresent(u, 1)
	// Override clock to exceed the unschedulableQTimeInterval so that unschedulable pods
	// will be moved to readyQ
	c.SetTime(timestamp.Add(unschedulableQTimeInterval + 1))
	queue.flushUnschedulableQLeftover()
	u, err = queue.Pop()
	if err != nil {
		t.Fatalf("Failed to pop a pod %v", err)
	}
	checkPerUnitSchedulingMetrics("Attempt twice", t, u, 2, timestamp)

	// Case 3: Similar to case 2, but before the second pop, call update, the queue operations are
	// Add -> Pop -> AddUnschedulableIfNotPresent -> flushUnschedulableQLeftover -> Update -> Pop.
	c = clock.NewFakeClock(timestamp)
	queue = NewPriorityQueue(nil, nil, nil, newDefaultUnitQueueSort(), WithClock(c))
	queue.Add(pod)
	u, err = queue.Pop()
	if err != nil {
		t.Fatalf("Failed to pop a pod %v", err)
	}
	queue.AddUnschedulableIfNotPresent(u, 1)
	// Override clock to exceed the unschedulableQTimeInterval so that unschedulable pods
	// will be moved to readyQ
	c.SetTime(timestamp.Add(unschedulableQTimeInterval + 1))
	queue.flushUnschedulableQLeftover()
	newPod := pod.DeepCopy()
	newPod.Generation = 1
	queue.Update(pod, newPod)
	u, err = queue.Pop()
	if err != nil {
		t.Fatalf("Failed to pop a pod %v", err)
	}
	checkPerUnitSchedulingMetrics("Attempt twice with update", t, u, 2, timestamp)
}

func checkPerUnitSchedulingMetrics(name string, t *testing.T, unitInfo *framework.QueuedUnitInfo, wantAttemtps int, wantInitialAttemptTs time.Time) {
	if unitInfo.Attempts != wantAttemtps {
		t.Errorf("[%s] Pod schedule attempt unexpected, got %v, want %v", name, unitInfo.Attempts, wantAttemtps)
	}
	if unitInfo.InitialAttemptTimestamp != wantInitialAttemptTs {
		t.Errorf("[%s] Pod initial schedule attempt timestamp unexpected, got %v, want %v", name, unitInfo.InitialAttemptTimestamp, wantInitialAttemptTs)
	}
}

func TestPriorityQueue_BackOffFlow(t *testing.T) {
	cl := clock.NewFakeClock(time.Now())
	q := NewPriorityQueue(nil, nil, nil, newDefaultUnitQueueSort(), WithClock(cl))
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
	podID := podutil.GetPodKey(pod)
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

			// An event happens.
			q.MoveAllToActiveOrBackoffQueue("deleted pod")

			podInfo := firstOrNil(u)
			if ok := queueHasPod(q.backoffQ, podInfo.Pod); !ok {
				t.Errorf("pod %v is not in the backoff queue", podID)
			}

			// Check backoff duration.
			deadline := q.boHandler.getBackoffTime(u)
			backoff := deadline.Sub(timestamp)
			if backoff != step.wantBackoff {
				t.Errorf("got backoff %s, want %s", backoff, step.wantBackoff)
			}

			// Simulate routine that continuously flushes the backoff queue.
			cl.Step(time.Millisecond)
			q.flushBackoffQCompleted()
			// Still in backoff queue after an early flush.
			if ok := queueHasPod(q.backoffQ, podInfo.Pod); !ok {
				t.Errorf("pod %v is not in the backoff queue", podID)
			}
			// Moved out of the backoff queue after timeout.
			cl.Step(backoff)
			q.flushBackoffQCompleted()
			if ok := queueHasPod(q.backoffQ, podInfo.Pod); ok {
				t.Errorf("pod %v is still in the backoff queue", podID)
			}
		})
	}
}

func firstOrNil(u framework.ScheduleUnit) *framework.QueuedPodInfo {
	for _, pInfo := range u.GetPods() {
		return pInfo
	}
	return nil
}
