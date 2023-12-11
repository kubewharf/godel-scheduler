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
	"sync"
	"sync/atomic"
	"time"

	"github.com/kubewharf/godel-scheduler-api/pkg/client/listers/scheduling/v1alpha1"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	schedulingv1 "k8s.io/client-go/listers/scheduling/v1"
	"k8s.io/klog/v2"

	framework "github.com/kubewharf/godel-scheduler/pkg/framework/api"
	"github.com/kubewharf/godel-scheduler/pkg/framework/utils"
	"github.com/kubewharf/godel-scheduler/pkg/scheduler/cache"
	"github.com/kubewharf/godel-scheduler/pkg/scheduler/metrics"
	godelutil "github.com/kubewharf/godel-scheduler/pkg/util"
	"github.com/kubewharf/godel-scheduler/pkg/util/heap"
	metricsutil "github.com/kubewharf/godel-scheduler/pkg/util/metrics"
	unitstatus "github.com/kubewharf/godel-scheduler/pkg/util/unitstatus"
)

// PriorityQueue implements a scheduling queue.
// The head of PriorityQueue is the highest priority pending pod. This structure
// has three sub queues. One sub-queue holds pods that are being considered for
// scheduling. This is called activeQ and is a Heap. Another queue holds
// pods that are already tried and are determined to be unschedulable. The latter
// is called unschedulableQ. The third queue holds pods that are moved from
// unschedulable queues and will be moved to active queue when backoff are completed.
// TODO(lintong.jiang): LOG - revisit the logging messages in this file
type PriorityQueue struct {
	lock  sync.RWMutex
	cond  sync.Cond
	stop  chan struct{}
	clock godelutil.Clock

	qos, subCluster string

	latestOperationTimestamp time.Time
	// closed indicates that the queue is closed.
	// It is mainly used to let Pop() exit its control loop while waiting for an item.
	closed bool
	// boHandler handle anything about backoff calculation
	boHandler       *backoffHandler
	metricsRecorder *metricsRecorder

	cache    cache.SchedulerCache
	pcLister schedulingv1.PriorityClassLister
	pgLister v1alpha1.PodGroupLister

	// waitingPodsList stores all the pods that can't create unit for now.
	waitingPodsList SubQueue

	// 1. ReadyQueue
	// readyQ is a heap structure that scheduler actively looks at to find units to
	// schedule. Head of heap is the highest priority unit.
	readyQ SubQueue
	// 2. BackoffQueue
	// backoffQ is a heap ordered by backoff expiry. Units which have completed backoff
	// are popped from this heap before the scheduler looks at readyQ.
	backoffQ SubQueue

	// For WaitingQueue and UnschedulableQueue, we use heap.Heap instead of a map to:
	//	1) Simplify the code logic
	//	2) Do sth in parallel (such as checkUnitTimestamp/checkAffinityTerms)
	// At the same time, by setting the comp function to always return false, we can keep it O(1)
	// by avoiding maintaining the heap nature.
	//
	// 3. WaitingQueue
	// waitingQ holds the units that doesn't have enough pods (such as podgroup MimMember).
	waitingQ SubQueue
	// 4. UnschedulableQueue
	// unschedulableQ holds units that have been tried and determined unschedulable.
	//
	// ATTENTION: All PodGroupUnits that should not be scheduled due to Timeout will also be
	// placed in the unscheduableQ.
	unschedulableQ SubQueue

	// schedulingCycle represents sequence number of scheduling cycle and is incremented
	// when a pod is popped.
	schedulingCycle int64
	// moveRequestCycle caches the sequence number of scheduling cycle when we
	// received a move request. Unscheduable pods in and before this scheduling
	// cycle will be put back to activeQueue if we were trying to schedule them
	// when we received move request.
	moveRequestCycle int64

	attemptImpactFactorOnPriority float64

	priorityHeap SubQueue
}

// Making sure that PriorityQueue implements SchedulingQueue.
var _ SchedulingQueue = &PriorityQueue{}

// NewPriorityQueue creates a PriorityQueue object.
func NewPriorityQueue(
	cache cache.SchedulerCache,
	pcLister schedulingv1.PriorityClassLister,
	pgLister v1alpha1.PodGroupLister,
	lessFn framework.UnitLessFunc,
	opts ...Option,
) *PriorityQueue {
	options := defaultPriorityQueueOptions
	for _, opt := range opts {
		opt(&options)
	}

	comp := func(unitInfo1, unitInfo2 interface{}) bool {
		u1 := unitInfo1.(*framework.QueuedUnitInfo)
		u2 := unitInfo2.(*framework.QueuedUnitInfo)
		return lessFn(u1, u2)
	}
	boHandler := newBackoffHandler(options.unitInitialBackoffDuration, options.unitMaxBackoffDuration, options.clock)
	qos := metricsutil.SwitchTypeToQos(options.switchType)

	pq := &PriorityQueue{
		stop:  make(chan struct{}),
		clock: options.clock,

		qos:        qos,
		subCluster: options.subCluster,

		cache:           cache,
		pcLister:        pcLister,
		pgLister:        pgLister,
		boHandler:       boHandler,
		metricsRecorder: newMetricsRecorder(),

		waitingPodsList: newWaitingPodsList(qos, options.subCluster, options.owner, options.clock),
		readyQ:          heap.NewWithRecorder("ready", unitInfoKeyFunc, comp, metrics.NewPendingUnitsRecorder("ready")),
		backoffQ:        heap.NewWithRecorder("backoff", unitInfoKeyFunc, boHandler.unitsCompareBackoffCompleted, metrics.NewPendingUnitsRecorder("backoff")),
		waitingQ:        heap.NewWithRecorder("waiting", unitInfoKeyFunc, alwaysFalse, metrics.NewPendingUnitsRecorder("waiting")),
		unschedulableQ:  heap.NewWithRecorder("unschedulable", unitInfoKeyFunc, alwaysFalse, metrics.NewPendingUnitsRecorder("unschedulable")),

		moveRequestCycle:              -1,
		attemptImpactFactorOnPriority: options.attemptImpactFactorOnPriority,
		priorityHeap: heap.New("priority", unitInfoKeyFunc, func(unitInfo1, unitInfo2 interface{}) bool {
			u1 := unitInfo1.(*framework.QueuedUnitInfo)
			u2 := unitInfo2.(*framework.QueuedUnitInfo)
			return u1.GetPriority() < u2.GetPriority()
		}),
	}
	pq.cond.L = &pq.lock
	pq.latestOperationTimestamp = pq.clock.Now()

	return pq
}

// Run starts the goroutine to pump from podBackoffQ to activeQ
func (p *PriorityQueue) Run() {
	go wait.Until(p.flushBackoffQCompleted, 1.0*time.Second, p.stop)
	go wait.Until(p.flushUnschedulableQLeftover, 30*time.Second, p.stop)
	// TODO: discuss remove flushScheduledUnitsInWaitingQ & flushWaitingPodsList
	go wait.Until(p.flushScheduledUnitsInWaitingQ, 2.0*time.Second, p.stop)
	go wait.Until(p.flushWaitingPodsList, 20.0*time.Second, p.stop)
}

// flushBackoffQCompleted Moves all units from backoffQ which have completed backoff in to readyQ
func (p *PriorityQueue) flushBackoffQCompleted() {
	p.lock.Lock()
	defer p.lock.Unlock()
	for {
		rawUnit := p.backoffQ.Peek()
		if rawUnit == nil {
			return
		}
		unitInfo := rawUnit.(*framework.QueuedUnitInfo)
		if p.boHandler.isUnitBackoff(unitInfo) {
			return
		}
		_, err := p.backoffQ.Pop()
		if err != nil {
			klog.ErrorS(err, "Failed to pop unit from backoff queue despite backoff completion", "subCluster", p.subCluster, "qos", p.qos, "unitKey", unitInfo.UnitKey)
			return
		}
		if err := p.readyQ.Add(unitInfo); err != nil {
			klog.ErrorS(err, "Failed to add unit from backoff queue to ready queue", "subCluster", p.subCluster, "qos", p.qos, "unitKey", unitInfo.UnitKey)
			return
		}
		klog.V(4).InfoS("Flushed scheduling units in the PriorityQueue", "subCluster", p.subCluster, "qos", p.qos, "unitKey", unitInfo.UnitKey, "oldQueue", p.backoffQ, "newQueue", p.readyQ)
		// TODO: more metrics and tracing
		p.metricsRecorder.recordIncoming(unitInfo, p.readyQ.String(), godelutil.BackoffComplete)
		defer p.cond.Broadcast()
	}
}

// flushScheduledUnitsInWaitingQ Moves all units from waitingQ which have been scheduled to readyQ.
// This case occurs when the scheduler is processing the scheduling unit but does not end.
// At this time, the all-min pods come into queue. And they will be stored in waitingQ.
// All-min pods (as a new unit) must be popped when scheduler schedule the previous unit successfully.
// TODO: discuss whether it would be better to move the unit directly while UpdateSchedulingUnitStatus
// instead of using the following loop.
func (p *PriorityQueue) flushScheduledUnitsInWaitingQ() {
	p.lock.Lock()
	defer p.lock.Unlock()
	var unitsToMove []*framework.QueuedUnitInfo
	var mutex sync.Mutex
	p.waitingQ.Process(func(_ int, _ string, obj interface{}) {
		unitInfo := obj.(*framework.QueuedUnitInfo)
		if p.readyToBeScheduled(unitInfo) {
			mutex.Lock()
			unitsToMove = append(unitsToMove, unitInfo)
			mutex.Unlock()
		}
	})
	for _, unit := range unitsToMove {
		p.waitingQ.Delete(unit)
		p.readyQ.Add(unit)
		klog.V(4).InfoS("Flushed scheduling units in the PriorityQueue", "subCluster", p.subCluster, "qos", p.qos, "unitKey", unit.UnitKey, "oldQueue", p.waitingQ, "newQueue", p.readyQ)
		p.metricsRecorder.recordIncoming(unit, p.readyQ.String(), godelutil.WaitingComplete)
		defer p.cond.Broadcast()
	}
}

// flushUnschedulableQLeftover moves units which stays in unschedulableQ longer than the unschedulableQTimeInterval
// to readyQ.
func (p *PriorityQueue) flushUnschedulableQLeftover() {
	p.lock.Lock()
	defer p.lock.Unlock()
	currentTime := p.clock.Now()
	checkUnitTimestamp := func(unitInfo *framework.QueuedUnitInfo) bool {
		return currentTime.Sub(unitInfo.Timestamp) > unschedulableQTimeInterval
	}

	var unitsToMove []*framework.QueuedUnitInfo
	var mutex sync.Mutex
	p.unschedulableQ.Process(func(_ int, _ string, obj interface{}) {
		unitInfo := obj.(*framework.QueuedUnitInfo)
		if checkUnitTimestamp(unitInfo) && !p.schedulingStatusTimeout(unitInfo) {
			mutex.Lock()
			unitsToMove = append(unitsToMove, unitInfo)
			mutex.Unlock()
		}
	})
	p.moveUnitsToReadyOrBackoffQueue(unitsToMove, godelutil.UnschedulableTimeout)
}

// flushWaitingPodsList moves pods that can create ScheduleUnit.
func (p *PriorityQueue) flushWaitingPodsList() {
	p.lock.Lock()
	defer p.lock.Unlock()

	var unitsToMove []*framework.QueuedUnitInfo
	var mutex sync.Mutex
	p.waitingPodsList.Process(func(_ int, _ string, obj interface{}) {
		unitInfo := obj.(*waitingPods).transferToPodGroupUnit(p.pcLister, p.pgLister, p.clock)
		if unitInfo != nil {
			mutex.Lock()
			unitsToMove = append(unitsToMove, unitInfo)
			mutex.Unlock()
		}
	})
	var queue SubQueue
	for _, unit := range unitsToMove {
		p.waitingPodsList.DeleteByKey(unit.UnitKey)
		if p.readyToBeScheduled(unit) {
			queue = p.readyQ
			defer p.cond.Broadcast()
		} else {
			queue = p.waitingQ
		}
		queue.Add(unit)
		klog.V(4).InfoS("Flushed scheduling units in the PriorityQueue", "subCluster", p.subCluster, "qos", p.qos, "unitKey", unit.UnitKey, "oldQueue", p.waitingPodsList, "newQueue", queue)
		p.metricsRecorder.recordIncoming(unit, queue.String(), godelutil.CreateUnitSucceed)
	}
}

// Close closes the priority queue.
func (p *PriorityQueue) Close() {
	p.lock.Lock()
	defer p.lock.Unlock()
	close(p.stop)
	p.closed = true
	p.cond.Broadcast()
}

func (p *PriorityQueue) CanBeRecycle() bool {
	p.lock.RLock()
	defer p.lock.RUnlock()
	if !p.latestOperationTimestamp.Add(framework.RecycleExpiration).Before(p.clock.Now()) {
		return false
	}
	for _, queue := range []SubQueue{p.waitingPodsList, p.waitingQ, p.readyQ, p.backoffQ, p.unschedulableQ} {
		if queue.Len() > 0 {
			return false
		}
	}
	return true
}

// findAndRemoveUnitFromQueue will find the SubQueue where the unit is located and remove it from the SubQueue (if it exists).
// Unless unit is empty, we must ensure that unit is added back to a SubQueue after calling this function.
func (p *PriorityQueue) findAndRemoveUnitFromQueue(unitKey string) (SubQueue, *framework.QueuedUnitInfo, bool) {
	for _, queue := range []SubQueue{p.waitingQ, p.readyQ, p.backoffQ, p.unschedulableQ} {
		if u, exist, _ := queue.GetByKey(unitKey); exist {
			// For the accuracy of metrics, delete the unit before modifying it.
			if err := queue.DeleteByKey(unitKey); err != nil {
				klog.V(4).InfoS("Error occurred when deleting unit in SchedulingQueue.findQueueForUnit", "subCluster", p.subCluster, "qos", p.qos, "unitKey", unitKey, "queue", queue, "err", err)
			}
			return queue, u.(*framework.QueuedUnitInfo), true
		}
	}
	return nil, nil, false
}

// addPodToUnitInfo return the SubQueue and the QueuedUnitInfo if unit info already exists in some queue,
// else returned SubQueue should be nil and QueuedUnitInfo will be the created one
func (p *PriorityQueue) addPodToUnitInfo(pod *v1.Pod) (SubQueue, *framework.QueuedUnitInfo, error) {
	unitKey := utils.GetUnitIdentifier(pod)
	queue, unitInfo, exists := p.findAndRemoveUnitFromQueue(unitKey)
	if exists {
		return queue, unitInfo, unitInfo.AddPod(newQueuedPodInfo(pod, p.clock))
	}

	created, err := p.createUnitInfo(pod)
	return nil, created, err
}

func (p *PriorityQueue) updatePodInUnitInfo(oldPod, newPod *v1.Pod) (SubQueue, *framework.QueuedUnitInfo, error) {
	unitKey := utils.GetUnitIdentifier(newPod)
	queue, unitInfo, exists := p.findAndRemoveUnitFromQueue(unitKey)
	if !exists {
		created, err := p.createUnitInfo(newPod)
		return nil, created, err
	}

	if oldPodInfo := unitInfo.GetPod(newQueuedPodInfoForLookup(oldPod)); oldPodInfo != nil {
		unitInfo.DeletePod(oldPodInfo)
		unitInfo.AddPod(updatePodInfo(oldPodInfo, newPod))
	} else {
		unitInfo.AddPod(newQueuedPodInfo(newPod, p.clock))
	}
	return queue, unitInfo, nil
}

func (p *PriorityQueue) deletePodFromUnitInfo(pod *v1.Pod) (SubQueue, *framework.QueuedUnitInfo, error) {
	unitKey := utils.GetUnitIdentifier(pod)
	queue, unitInfo, exists := p.findAndRemoveUnitFromQueue(unitKey)
	if exists {
		return queue, unitInfo, unitInfo.DeletePod(newQueuedPodInfoForLookup(pod))
	}

	return nil, nil, nil
}

func (p *PriorityQueue) createUnitInfo(pod *v1.Pod) (*framework.QueuedUnitInfo, error) {
	pInfo := newQueuedPodInfo(pod, p.clock)
	unitKey := utils.GetUnitIdentifier(pod)
	scheduleUnit, err := utils.CreateScheduleUnit(p.pcLister, p.pgLister, pInfo)
	if err != nil {
		// This means that we haven't got the relevant information about PodGroup or PriorityClass yet.
		return nil, fmt.Errorf("error occurred when create unit for pod %v/%v: %v", pod.Namespace, pod.Name, err)
	}
	if err = scheduleUnit.AddPod(pInfo); err != nil {
		return nil, fmt.Errorf("error occurred when adding pod to unit %v for pod %v/%v: %v", scheduleUnit.GetKey(), pod.Namespace, pod.Name, err)
	}

	// Since `PodGroup` and `Pod` are two different resource types, we can't ensure the order of their arrival.
	// If some pods come before their PodGroup, these pods will be stored in `waitingPodsList`. And then, after the PodGroup is created, there may
	// be a race between `CreateScheduleUnit` (getting PodGroup from local informer cache) and ActivePodGroupUnit (triggered by PodGroup events).
	// e.g.
	//  1. Some pods (PodSet1) come before their PodGroup, they will be stored in `waitingPodsList` since we will fail to create unit for them;
	//  2. And then PodGroup is created successfully and stored in local cache, but corresponding `Add` event is not captured by scheduler;
	//  3. Another several pods (PodSet2) come, these pods will be stored in `waitingQ` since we can successfully create unit for them;
	//  4. And then, PodGroup `Add` event is captured, we will execute `ActivePodGroupUnit`, PodSet2 will be overwritten by PodSet1 during this
	//     process which is not expected;
	// To avoid this, when we create unit successfully for the first time, we propose to check `waitingPodsList` and pick up the pods belonging
	// to the same unit.
	if waitingPodsObj, exist, _ := p.waitingPodsList.GetByKey(unitKey); exist {
		if waitingPodsObj != nil {
			waitingPods := waitingPodsObj.(*waitingPods)
			if err := scheduleUnit.AddPods(waitingPods.GetPods()); err != nil {
				return nil, fmt.Errorf("error occurred when adding waitingPodsList pods to unit %v for pod %v/%v: %v", scheduleUnit.GetKey(), pod.Namespace, pod.Name, err)
			}
		}
		p.waitingPodsList.DeleteByKey(unitKey)
	}
	queuedUnitInfo := framework.NewQueuedUnitInfo(unitKey, scheduleUnit, p.clock)
	return queuedUnitInfo, nil
}

func (p *PriorityQueue) readyToBeScheduled(unitInfo *framework.QueuedUnitInfo) bool {
	if unitInfo == nil || unitInfo.ScheduleUnit == nil {
		return false
	}
	return unitInfo.ReadyToBePopulated() || p.cache.GetUnitSchedulingStatus(unitInfo.UnitKey) == unitstatus.ScheduledStatus
}

// TODO: Add more interpretable information for pods that can't be scheduled due to timeout.
func (p *PriorityQueue) schedulingStatusTimeout(unitInfo *framework.QueuedUnitInfo) bool {
	if unitInfo == nil || unitInfo.ScheduleUnit == nil || unitInfo.Type() == framework.SinglePodUnitType {
		return false
	}
	return p.cache.GetUnitSchedulingStatus(unitInfo.UnitKey) == unitstatus.TimeoutStatus
}

// Add adds a pod to the ready queue. It should be called only when a new pod
// is added so there is no chance the pod is already in waiting/ready/unschedulable/backoff queues
func (p *PriorityQueue) Add(pod *v1.Pod) error {
	p.lock.Lock()
	defer p.lock.Unlock()
	p.latestOperationTimestamp = p.clock.Now()

	queue, unitInfo, err := p.addPodToUnitInfo(pod)
	if err != nil {
		klog.V(4).InfoS("SchedulingQueue Add, add to waitingPodsList directly", "subCluster", p.subCluster, "qos", p.qos, "pod", klog.KObj(pod), "podUID", pod.GetUID(), "err", err)
		return p.waitingPodsList.Add(pod)
	}
	if queue != nil {
		// The unit has been removed from SubQueue in addPodToUnitInfo.
		klog.V(4).InfoS("SchedulingQueue Add, delete from oldQueue", "subCluster", p.subCluster, "qos", p.qos, "unitKey", unitInfo.UnitKey, "oldQueue", queue)
	}
	switch {
	case queue == p.backoffQ:
		// Keep the unit in backoffQ unchanged.
	case p.readyToBeScheduled(unitInfo):
		queue = p.readyQ
		defer p.cond.Broadcast()
	default:
		queue = p.waitingQ
	}
	klog.V(4).InfoS("SchedulingQueue Add, add to newQueue", "subCluster", p.subCluster, "qos", p.qos, "unitKey", unitInfo.UnitKey, "newQueue", queue)
	// TODO: improve metrics
	p.metricsRecorder.recordIncoming(unitInfo, queue.String(), godelutil.PodAdd)
	p.priorityHeap.Add(unitInfo)
	return queue.Add(unitInfo)
}

// AddUnschedulableIfNotPresent inserts a unit that cannot be scheduled into
// the queue, unless it is already in the queue. Normally, PriorityQueue puts
// unschedulable units in `unschedulableQ`. But if there has been a recent move
// request, then the unit is put in `backoffQ`.
func (p *PriorityQueue) AddUnschedulableIfNotPresent(unitInfo *framework.QueuedUnitInfo, unitSchedulingCycle int64) error {
	p.lock.Lock()
	defer p.lock.Unlock()
	p.latestOperationTimestamp = p.clock.Now()

	if unitInfo == nil || unitInfo.NumPods() == 0 {
		return nil
	}
	var queue SubQueue
	queue, prevUnitInfo, exists := p.findAndRemoveUnitFromQueue(unitInfo.UnitKey)
	if exists {
		klog.V(4).InfoS("SchedulingQueue AddUnschedulable, exist same unit and will merge them", "unitKey", unitInfo.UnitKey, "oldQueue", queue)
		// We add the unitInfo to prevUnitInfo cause we may update the old pods.
		prevUnitInfo.AddPods(unitInfo.GetPods())
		unitInfo = prevUnitInfo
	}
	// Refresh the timestamp since the unit is re-added.
	unitInfo.Timestamp = p.clock.Now()
	// If a move request has been received, move it to the BackoffQ, otherwise move
	// it to unschedulableQ.
	if p.moveRequestCycle >= unitSchedulingCycle && !p.schedulingStatusTimeout(unitInfo) {
		queue = p.backoffQ
	} else {
		queue = p.unschedulableQ
	}
	klog.V(4).InfoS("SchedulingQueue AddUnschedulable, add to newQueue", "subCluster", p.subCluster, "qos", p.qos, "unitKey", unitInfo.UnitKey, "newQueue", queue)
	p.metricsRecorder.recordIncoming(unitInfo, queue.String(), godelutil.ScheduleAttemptFailure)
	p.priorityHeap.Add(unitInfo)
	return queue.Add(unitInfo)
}

// Update updates a pod in the ready/waiting/backoff queue if present. Otherwise, it removes
// the item from the unschedulable queue if pod is updated in a way that it may
// become schedulable and adds the updated one to the ready/waiting queue.
// If pod is not present in any of the queues, it is added to the waiting queue.
func (p *PriorityQueue) Update(oldPod, newPod *v1.Pod) error {
	p.lock.Lock()
	defer p.lock.Unlock()
	p.latestOperationTimestamp = p.clock.Now()

	// If the pod is in the unschedulable queue, updating it may make it schedulable.
	queue, unitInfo, err := p.updatePodInUnitInfo(oldPod, newPod)
	if err != nil {
		klog.V(4).InfoS("SchedulingQueue Update, update to waitingPodsList directly", "subCluster", p.subCluster, "qos", p.qos, "pod", klog.KObj(newPod), "podUID", newPod.GetUID(), "err", err)
		return p.waitingPodsList.Update(oldPod, newPod)
	}
	defer p.priorityHeap.Add(unitInfo)
	if queue != nil {
		// The unit has been removed from SubQueue in updatePodInUnitInfo.
		if queue == p.unschedulableQ && isPodUpdated(oldPod, newPod) {
			klog.V(4).InfoS("SchedulingQueue Update, delete from oldQueue", "subCluster", p.subCluster, "qos", p.qos, "unitKey", unitInfo.UnitKey, "oldQueue", queue)
			if p.boHandler.isUnitBackoff(unitInfo) {
				queue = p.backoffQ
			} else {
				if p.readyToBeScheduled(unitInfo) {
					queue = p.readyQ
					defer p.cond.Broadcast()
				} else {
					queue = p.waitingQ
				}
			}
			klog.V(4).InfoS("SchedulingQueue Update, add to newQueue", "subCluster", p.subCluster, "qos", p.qos, "unitKey", unitInfo.UnitKey, "newQueue", queue)
			return queue.Add(unitInfo)
		}
		klog.V(4).InfoS("SchedulingQueue Update, update the unit", "subCluster", p.subCluster, "qos", p.qos, "unitKey", unitInfo.UnitKey, "oldQueue", queue, "newQueue", queue)
		// The unit isn't in unschedulableQ or the pod update didn't make it schedulable,
		// keep it in the unschedulable queue.
		return queue.Add(unitInfo)
	}

	if p.readyToBeScheduled(unitInfo) {
		queue = p.readyQ
		defer p.cond.Broadcast()
	} else {
		queue = p.waitingQ
	}
	klog.V(4).InfoS("SchedulingQueue Update, add to newQueue", "subCluster", p.subCluster, "qos", p.qos, "unitKey", unitInfo.UnitKey, "newQueue", queue)
	return queue.Add(unitInfo)
}

// Delete deletes the item from either of the two queues. It assumes the pod is
// only in one queue.
func (p *PriorityQueue) Delete(pod *v1.Pod) error {
	p.lock.Lock()
	defer p.lock.Unlock()
	p.latestOperationTimestamp = p.clock.Now()

	var err error
	queue, unitInfo, _ := p.deletePodFromUnitInfo(pod)
	err = p.waitingPodsList.Delete(pod)
	if err != nil {
		klog.V(4).InfoS("Error occurred in SchedulingQueue.Delete", "subCluster", p.subCluster, "qos", p.qos, "queue", p.waitingPodsList, "err", err)
	}
	if queue == nil {
		klog.V(4).InfoS("SchedulingQueue Delete, delete from waitingPodsList directly", "subCluster", p.subCluster, "qos", p.qos, "pod", klog.KObj(pod), "podUID", pod.GetUID())
		return nil
	}

	// The unit has been removed from SubQueue in deletePodFromUnitInfo.
	if unitInfo.NumPods() == 0 {
		klog.V(4).InfoS("SchedulingQueue Delete, delete from oldQueue and won't add back", "subCluster", p.subCluster, "qos", p.qos, "unitKey", unitInfo.UnitKey, "oldQueue", queue)
		err = p.priorityHeap.Delete(unitInfo)
		if err != nil {
			klog.V(4).InfoS("Error occurred when deleting unit in SchedulingQueue.Delete", "subCluster", p.subCluster, "qos", p.qos, "queue", p.priorityHeap, "err", err)
		}
	} else {
		if queue != p.waitingQ && !p.readyToBeScheduled(unitInfo) {
			klog.V(4).InfoS("SchedulingQueue Delete, move the unit", "subCluster", p.subCluster, "qos", p.qos, "unitKey", unitInfo.UnitKey, "oldQueue", queue, "newQueue", p.waitingQ)
			err = p.waitingQ.Add(unitInfo)
			if err != nil {
				klog.V(4).InfoS("Error occurred when moving unit in SchedulingQueue.Delete", "subCluster", p.subCluster, "qos", p.qos, "queue", p.waitingQ, "err", err)
			}
		} else {
			klog.V(4).InfoS("SchedulingQueue Delete, update the unit", "subCluster", p.subCluster, "qos", p.qos, "unitKey", unitInfo.UnitKey, "oldQueue", queue, "newQueue", queue)
			err = queue.Add(unitInfo)
			if err != nil {
				klog.V(4).InfoS("Error occurred when updating unit in SchedulingQueue.Delete", "subCluster", p.subCluster, "qos", p.qos, "queue", queue, "err", err)
			}
		}
	}
	return err
}

// AssignedPodAdded is called when a bound pod is added. Creation of this pod
// may make pending pods with matching affinity terms schedulable.
func (p *PriorityQueue) AssignedPodAdded(pod *v1.Pod) {
	p.lock.Lock()
	defer p.lock.Unlock()
	p.moveUnitsToReadyOrBackoffQueue(p.getUnschedulablePodsWithMatchingAffinityTerm(pod), godelutil.AssignedPodAdd)
}

// AssignedPodUpdated is called when a bound pod is updated. Change of labels
// may make pending pods with matching affinity terms schedulable.
func (p *PriorityQueue) AssignedPodUpdated(pod *v1.Pod) {
	p.lock.Lock()
	defer p.lock.Unlock()
	p.moveUnitsToReadyOrBackoffQueue(p.getUnschedulablePodsWithMatchingAffinityTerm(pod), godelutil.AssignedPodUpdate)
}

// MoveAllToActiveOrBackoffQueue moves all pods from unschedulableQ to activeQ or backoffQ.
// This function adds all pods and then signals the condition variable to ensure that
// if Pop() is waiting for an item, it receives it after all the pods are in the
// queue and the head is the highest priority pod.
func (p *PriorityQueue) MoveAllToActiveOrBackoffQueue(event string) {
	p.lock.Lock()
	defer p.lock.Unlock()
	unschedulableUnits := make([]*framework.QueuedUnitInfo, p.unschedulableQ.Len())
	index := int32(-1)
	p.unschedulableQ.Process(func(_ int, _ string, obj interface{}) {
		unitInfo := obj.(*framework.QueuedUnitInfo)
		if !p.schedulingStatusTimeout(unitInfo) {
			unschedulableUnits[atomic.AddInt32(&index, 1)] = obj.(*framework.QueuedUnitInfo)
		}
	})
	unschedulableUnits = unschedulableUnits[:index+1]
	p.moveUnitsToReadyOrBackoffQueue(unschedulableUnits, event)
}

func (p *PriorityQueue) ActivePodGroupUnit(unitKey string) {
	p.lock.Lock()
	defer p.lock.Unlock()
	wpObj, exist, _ := p.waitingPodsList.GetByKey(unitKey)
	if !exist || wpObj == nil {
		return
	}
	unitInfo := wpObj.(*waitingPods).transferToPodGroupUnit(p.pcLister, p.pgLister, p.clock)
	if unitInfo == nil {
		return
	}
	var queue SubQueue
	if p.readyToBeScheduled(unitInfo) {
		queue = p.readyQ
		defer p.cond.Broadcast()
	} else {
		queue = p.waitingQ
	}
	klog.V(4).InfoS("SchedulingQueue ActivePodGroupUnit, will move the unit", "subCluster", p.subCluster, "unitKey", unitInfo.UnitKey, "oldQueue", p.waitingPodsList, "newQueue", queue)
	if err := queue.Add(unitInfo); err != nil {
		klog.V(4).InfoS("SchedulingQueue ActivePodGroupUnit, failed to move the unit", "unitKey", unitInfo.UnitKey, "oldQueue", p.waitingPodsList, "newQueue", queue, "err", err)
	} else {
		p.waitingPodsList.DeleteByKey(unitKey)
	}
}

// NOTE: this function assumes lock has been acquired in caller
func (p *PriorityQueue) moveUnitsToReadyOrBackoffQueue(unitInfoList []*framework.QueuedUnitInfo, event string) {
	var queue SubQueue
	for i := range unitInfoList {
		unitInfo := unitInfoList[i]
		if p.boHandler.isUnitBackoff(unitInfo) {
			queue = p.backoffQ
		} else {
			queue = p.readyQ
		}
		queue.Add(unitInfo)
		p.unschedulableQ.Delete(unitInfo)
		klog.V(4).InfoS("Flushed scheduling units in the PriorityQueue", "subCluster", p.subCluster, "qos", p.qos, "unitKey", unitInfo.UnitKey, "oldQueue", p.unschedulableQ, "newQueue", queue)
		p.metricsRecorder.recordIncoming(unitInfo, queue.String(), event)
	}
	p.moveRequestCycle = p.schedulingCycle
	p.cond.Broadcast()
}

// getUnschedulablePodsWithMatchingAffinityTerm returns unschedulable pods which have
// any affinity term that matches "pod".
// NOTE: this function assumes lock has been acquired in caller.
func (p *PriorityQueue) getUnschedulablePodsWithMatchingAffinityTerm(pod *v1.Pod) []*framework.QueuedUnitInfo {
	checkAffinityTerms := func(unit framework.ScheduleUnit) bool {
		for _, pInfo := range unit.GetPods() {
			up := pInfo.Pod
			terms := godelutil.GetPodAffinityTerms(up.Spec.Affinity)
			for i, term := range terms {
				namespaces := godelutil.GetNamespacesFromPodAffinityTerm(up, &terms[i])
				selector, err := metav1.LabelSelectorAsSelector(term.LabelSelector)
				if err != nil {
					klog.InfoS("Error getting label selectors for pod", "pod", klog.KObj(up), "err", err)
				}
				if godelutil.PodMatchesTermsNamespaceAndSelector(pod, namespaces, selector) {
					return true
				}
			}
		}
		return false
	}

	var unitsToMove []*framework.QueuedUnitInfo
	var mutex sync.Mutex
	p.unschedulableQ.Process(func(_ int, _ string, obj interface{}) {
		unitInfo := obj.(*framework.QueuedUnitInfo)
		if checkAffinityTerms(unitInfo) && !p.schedulingStatusTimeout(unitInfo) {
			mutex.Lock()
			unitsToMove = append(unitsToMove, unitInfo)
			mutex.Unlock()
		}
	})
	return unitsToMove
}

// PopUnit pops a unit for batch scheduling.
func (p *PriorityQueue) Pop() (*framework.QueuedUnitInfo, error) {
	p.lock.Lock()
	defer p.lock.Unlock()
	for p.readyQ.Len() == 0 {
		if p.closed {
			return nil, fmt.Errorf(queueClosed)
		}
		p.cond.Wait()
	}
	obj, err := p.readyQ.Pop()
	if err != nil {
		return nil, err
	}
	unitInfo := obj.(*framework.QueuedUnitInfo)
	unitInfo.Attempts++
	p.setQueuedPriorityScore(unitInfo)
	p.schedulingCycle++
	// TODO: improve metrics
	p.metricsRecorder.recordPending(unitInfo, time.Since(unitInfo.Timestamp))
	p.priorityHeap.Delete(unitInfo)
	return unitInfo, nil
}

func (p *PriorityQueue) Peek() *framework.QueuedUnitInfo {
	p.lock.RLock()
	defer p.lock.RUnlock()
	if p.readyQ.Len() == 0 {
		return nil
	}
	obj := p.readyQ.Peek()
	if obj == nil {
		return nil
	}
	return obj.(*framework.QueuedUnitInfo)
}

// PendingPods returns all the pending pods in the queue. This function is
// used for debugging purposes in the scheduler cache dumper and comparer.
func (p *PriorityQueue) PendingPods() []*v1.Pod {
	p.lock.RLock()
	defer p.lock.RUnlock()
	var result []*v1.Pod
	for _, queue := range []SubQueue{p.waitingPodsList, p.waitingQ, p.readyQ, p.backoffQ, p.unschedulableQ} {
		for _, u := range queue.List() {
			for _, pInfo := range u.(framework.StoredUnit).GetPods() {
				result = append(result, pInfo.Pod)
			}
		}
	}
	return result
}

// NumUnschedulableUnits returns the number of unschedulable pods exist in the SchedulingQueue.
func (p *PriorityQueue) NumUnschedulableUnits() int {
	p.lock.RLock()
	defer p.lock.RUnlock()
	return p.unschedulableQ.Len()
}

// SchedulingCycle returns current scheduling cycle.
func (p *PriorityQueue) SchedulingCycle() int64 {
	p.lock.RLock()
	defer p.lock.RUnlock()
	return p.schedulingCycle
}
