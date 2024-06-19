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
	"sync"
	"time"

	"github.com/kubewharf/godel-scheduler-api/pkg/client/listers/scheduling/v1alpha1"
	v1 "k8s.io/api/core/v1"
	ktypes "k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/wait"
	schev1 "k8s.io/client-go/listers/scheduling/v1"
	k8scache "k8s.io/client-go/tools/cache"
	"k8s.io/klog/v2"

	"github.com/kubewharf/godel-scheduler/pkg/binder/cache"
	"github.com/kubewharf/godel-scheduler/pkg/binder/metrics"
	binderutils "github.com/kubewharf/godel-scheduler/pkg/binder/utils"
	framework "github.com/kubewharf/godel-scheduler/pkg/framework/api"
	"github.com/kubewharf/godel-scheduler/pkg/framework/utils"
	"github.com/kubewharf/godel-scheduler/pkg/util"
	"github.com/kubewharf/godel-scheduler/pkg/util/heap"
	"github.com/kubewharf/godel-scheduler/pkg/util/tracing"
	status "github.com/kubewharf/godel-scheduler/pkg/util/unitstatus"
)

const (
	queueClosed = "binder queue is closed"
	// DefaultPodInitialBackoffDuration is the default value for the initial backoff duration
	// for unschedulable pods. To change the default podInitialBackoffDurationSeconds used by the
	// scheduler, update the ComponentConfig value in defaults.go
	DefaultPodInitialBackoffDuration = 1 * time.Second
	// DefaultPodMaxBackoffDuration is the default value for the max backoff duration
	// for unschedulable pods. To change the default podMaxBackoffDurationSeconds used by the
	// scheduler, update the ComponentConfig value in defaults.go
	DefaultPodMaxBackoffDuration = 10 * time.Second
)

// BinderQueue watches on pod which need to resolve conflict for same node, cross node,
// preemption and gang scheduling
type BinderQueue interface {
	// Add adds a pod which has a node assumed from scheduler
	// and need to resolve the conflict
	Add(pod *v1.Pod) error
	Update(oldPod, newPod *v1.Pod) error
	Delete(pod *v1.Pod) error

	// Pop removes the pod for resolving same node constraints
	Pop() (*framework.QueuedUnitInfo, error)
	AddUnitPreemptor(*framework.QueuedUnitInfo)
	PendingPods() []*v1.Pod

	// Run starts the goroutines managing the queue.
	Run()
	Close()

	// ActiveWaitingUnit move unit from waitingQ to readyQ
	ActiveWaitingUnit(unitKey string)
}

// NewBinderQueue initializes a priority queue as a new binder queue.
func NewBinderQueue(lessFn framework.UnitLessFunc,
	pgLister v1alpha1.PodGroupLister,
	pcLister schev1.PriorityClassLister,
	cache cache.BinderCache,
	opts ...Option,
) BinderQueue {
	return NewPriorityQueue(lessFn, pgLister, pcLister, cache, opts...)
}

// PriorityQueue provides the priority queues for resolving scheduling conflicts
type PriorityQueue struct {
	lock sync.RWMutex
	cond sync.Cond

	stop  chan struct{}
	clock util.Clock

	cache cache.BinderCache

	// pod initial backoff duration.
	podInitialBackoffDuration time.Duration
	// pod maximum backoff duration.
	podMaxBackoffDuration time.Duration

	readyUnitQ *heap.Heap

	unitBackoffQ *heap.Heap

	// if unit is already be created but still not ready to be popped,
	// it will be stored in this queue.
	waitingUnitQ *heap.Heap

	// closed indicates that the queue is closed.
	// It is mainly used to let Pop() exit its control loop while waiting for an item.
	closed bool

	// TODO: move out from queue.
	pgLister v1alpha1.PodGroupLister
	pcLister schev1.PriorityClassLister

	// the key is pod UID
	// if pods are not ready to be grouped as a unit(such as podgroup
	// create events are still not caught), they will be stored in this list.
	waitingPodsList map[ktypes.UID]*framework.QueuedPodInfo
}

type priorityQueueOptions struct {
	clock                     util.Clock
	podInitialBackoffDuration time.Duration
	podMaxBackoffDuration     time.Duration
}

// Option configures a PriorityQueue
type Option func(*priorityQueueOptions)

// WithClock sets clock for PriorityQueue, the default clock is util.RealClock.
func WithClock(clock util.Clock) Option {
	return func(o *priorityQueueOptions) {
		o.clock = clock
	}
}

// WithPodInitialBackoffDuration sets pod initial backoff duration for PriorityQueue.
func WithPodInitialBackoffDuration(duration time.Duration) Option {
	return func(o *priorityQueueOptions) {
		o.podInitialBackoffDuration = duration
	}
}

// WithPodMaxBackoffDuration sets pod max backoff duration for PriorityQueue.
func WithPodMaxBackoffDuration(duration time.Duration) Option {
	return func(o *priorityQueueOptions) {
		o.podMaxBackoffDuration = duration
	}
}

var defaultPriorityQueueOptions = priorityQueueOptions{
	clock:                     util.RealClock{},
	podInitialBackoffDuration: DefaultPodInitialBackoffDuration,
	podMaxBackoffDuration:     DefaultPodMaxBackoffDuration,
}

var _ BinderQueue = &PriorityQueue{}

func NewPriorityQueue(
	lessFn framework.UnitLessFunc,
	pgLister v1alpha1.PodGroupLister,
	pcLister schev1.PriorityClassLister,
	cache cache.BinderCache,
	opts ...Option,
) *PriorityQueue {
	options := defaultPriorityQueueOptions
	for _, opt := range opts {
		opt(&options)
	}

	comp := func(unit1, unit2 interface{}) bool {
		u1 := unit1.(*framework.QueuedUnitInfo)
		u2 := unit2.(*framework.QueuedUnitInfo)
		return lessFn(u1, u2)
	}

	pq := &PriorityQueue{
		clock:                     options.clock,
		stop:                      make(chan struct{}),
		cache:                     cache,
		waitingUnitQ:              heap.NewWithRecorder("waiting", unitKeyFunc, comp, metrics.NewPendingUnitsRecorder("waiting")),
		readyUnitQ:                heap.NewWithRecorder("ready", unitKeyFunc, comp, metrics.NewPendingUnitsRecorder("ready")),
		podInitialBackoffDuration: options.podInitialBackoffDuration,
		podMaxBackoffDuration:     options.podMaxBackoffDuration,
		pcLister:                  pcLister,
		pgLister:                  pgLister,
		waitingPodsList:           make(map[ktypes.UID]*framework.QueuedPodInfo),
	}
	pq.cond.L = &pq.lock
	pq.unitBackoffQ = heap.NewWithRecorder("backoff", unitKeyFunc, pq.unitCompareBackoffCompleted, metrics.NewPendingUnitsRecorder("backoff"))
	return pq
}

// newQueuedPodInfo builds a QueuedPodInfo object.
func (p *PriorityQueue) newQueuedPodInfo(pod *v1.Pod) *framework.QueuedPodInfo {
	now := p.clock.Now()
	nominatedNode, _ := utils.GetPodNominatedNode(pod)
	return &framework.QueuedPodInfo{
		Pod:                     pod,
		Timestamp:               now,
		InitialAttemptTimestamp: now,
		NominatedNode:           nominatedNode,
		QueueSpan:               tracing.NewSpanInfo(framework.ExtractPodProperty(pod).ConvertToTracingTags()),
	}
}

// newQueuedPodInfoWithoutTimestamp builds a QueuedPodInfo object without timestamp.
func newQueuedPodInfoWithoutTimestamp(pod *v1.Pod) *framework.QueuedPodInfo {
	return &framework.QueuedPodInfo{
		Pod: pod,
	}
}

func (p *PriorityQueue) newQueuedUnitInfo(unit framework.ScheduleUnit) *framework.QueuedUnitInfo {
	return framework.NewQueuedUnitInfo(unit.GetKey(), unit, p.clock)
}

func (p *PriorityQueue) newUnitInfoWithoutTimestamp(unit framework.ScheduleUnit) *framework.QueuedUnitInfo {
	return &framework.QueuedUnitInfo{
		ScheduleUnit: unit,
	}
}

// nsNameForPod returns a namespaced name for a pod
func nsNameForPod(pod *v1.Pod) ktypes.NamespacedName {
	return ktypes.NamespacedName{
		Namespace: pod.Namespace,
		Name:      pod.Name,
	}
}

// Add adds pod with assumed node from the scheduler into respective queue
// based on its annotations
func (p *PriorityQueue) Add(pod *v1.Pod) error {
	p.lock.Lock()
	defer p.lock.Unlock()

	pInfo := p.newQueuedPodInfo(pod)
	if err := p.addPodUsingAnnotations(pInfo); err != nil {
		return err
	}

	return nil
}

func (p *PriorityQueue) addPodUsingAnnotations(pInfo *framework.QueuedPodInfo) error {
	pod := pInfo.Pod
	// belong to a unit, or is a single pod.
	key := binderutils.GetUnitIdentifier(pInfo)
	podProperty := pInfo.GetPodProperty()
	unit, theQueueUnitIn := p.getUnit(key)

	if unit == nil {
		// unit isn't found in the queue, add it into waiting unit queue.
		// TODO: wait for podgroup event instead of list it.
		scheduleUnit, err := binderutils.CreateScheduleUnit(p.pcLister, p.pgLister, pInfo)
		if err != nil {
			klog.ErrorS(err, "Failed to create unit", "pod", klog.KObj(pod))
			// TODO: retry
			p.addToPodWaitList(pInfo)
			return err
		}
		klog.V(4).InfoS("Created a new unit for pod", "unitKey", scheduleUnit.GetKey(), "pod", klog.KObj(pod))

		// new queuedUnitInfo
		queuedUnitInfo := p.newQueuedUnitInfo(scheduleUnit)
		if queuedUnitInfo.ReadyToBePopulated() || p.cache.GetUnitSchedulingStatus(scheduleUnit.GetKey()) == status.ScheduledStatus {
			if err := p.readyUnitQ.Add(queuedUnitInfo); err != nil {
				klog.ErrorS(err, "Failed to add unit into readyQ", "unitKey", key)
				return err
			}
			klog.V(4).InfoS("Added pod to readyUnitQ", "pod", klog.KObj(pod))
			// ready to be populated
			defer p.cond.Broadcast()
			metrics.BinderQueueIncomingPodsInc(podProperty, "ready", PodAdd)
			return nil
		} else if err := p.waitingUnitQ.Add(queuedUnitInfo); err != nil {
			klog.ErrorS(err, "Error adding unit to the waitingUnitQ", "unitKey", key)
			return err
		}
		metrics.BinderQueueIncomingPodsInc(podProperty, "waiting", PodAdd)
		klog.V(4).InfoS("Added pod to waitingUnitQ", "pod", klog.KObj(pod))
		return nil
	}

	if theQueueUnitIn == p.waitingUnitQ {
		// found unit in waiting unit queue.
		unit.AddPod(pInfo)
		// move to ready queue if unit is ready to be popped.
		if unit.ReadyToBePopulated() || p.cache.GetUnitSchedulingStatus(unit.GetKey()) == status.ScheduledStatus {
			if err := p.waitingUnitQ.Delete(unit); err != nil {
				klog.ErrorS(err, "Unable to delete unit from waitingQ", "unitKey", unit.GetKey())
				return err
			}
			if err := p.readyUnitQ.Add(unit); err != nil {
				klog.ErrorS(err, "Unable to add unit into readyQ", "unitKey", unit.GetKey())
				return err
			}
			// ready to be populated
			defer p.cond.Broadcast()
			metrics.BinderQueueIncomingPodsInc(podProperty, "ready", PodAdd)
			klog.V(4).InfoS("Added pod to readyUnitQ", "pod", klog.KObj(pod))
			return nil
		}

		if err := p.waitingUnitQ.Update(nil, unit); err != nil {
			klog.ErrorS(err, "Failed to update unit to the waitingUnitQ", "unitKey", unit.GetKey())
			return err
		}
		metrics.BinderQueueIncomingPodsInc(podProperty, "waiting", PodAdd)
		klog.V(4).InfoS("Added pod to waitingUnitQ", "pod", klog.KObj(pod))

		return nil
	} else if theQueueUnitIn == p.readyUnitQ {
		// found unit in ready unit queue.
		unit.AddPod(pInfo)
		if err := p.readyUnitQ.Update(nil, unit); err != nil {
			klog.ErrorS(err, "Error update unit to the readyUnitQ", "unitKey", unit.GetKey())
			return err
		}
		metrics.BinderQueueIncomingPodsInc(podProperty, "ready", PodAdd)
		klog.V(4).InfoS("Added pod to readyUnitQ", "pod", klog.KObj(pod))
		return nil
	} else if theQueueUnitIn == p.unitBackoffQ {
		// found unit in unit backoff queue.
		unit.AddPod(pInfo)
		if err := p.unitBackoffQ.Update(nil, unit); err != nil {
			klog.ErrorS(err, "Error update unit to the unitBackoffQ", "unitKey", unit.GetKey())
			return err
		}
		metrics.BinderQueueIncomingPodsInc(podProperty, "backoff", PodAdd)
		klog.V(4).InfoS("Added pod to unitBackoffQ", "pod", klog.KObj(pod))
		return nil
	}

	return nil
}

func (p *PriorityQueue) addToPodWaitList(pInfo *framework.QueuedPodInfo) {
	if pInfo == nil || pInfo.Pod == nil {
		return
	}
	p.waitingPodsList[pInfo.Pod.UID] = pInfo
}

func (p *PriorityQueue) updatePodInWaitingList(oldPod, newPod *v1.Pod) error {
	if oldPodInfo, ok := p.waitingPodsList[oldPod.UID]; ok {
		p.waitingPodsList[oldPod.UID] = updatePod(oldPodInfo, newPod)
		return nil
	}
	return fmt.Errorf("pod %s/%s not found in waiting list", oldPod.Namespace, oldPod.Name)
}

func (p *PriorityQueue) deletePodInWaitingList(pod *v1.Pod) error {
	delete(p.waitingPodsList, pod.UID)
	return nil
}

// Update the pod into respective queue, if not present then it is added to
// respective queue
func (p *PriorityQueue) Update(oldPod, newPod *v1.Pod) error {
	p.lock.Lock()
	defer p.lock.Unlock()

	if oldPod != nil {
		oldPodInfo := newQueuedPodInfoWithoutTimestamp(oldPod)
		// get unit key
		key := binderutils.GetUnitIdentifier(oldPodInfo)

		// get unit object
		unit, theQueueUnitIn := p.getUnit(key)
		var storedOldPodInfo *framework.QueuedPodInfo
		if unit != nil {
			storedOldPodInfo = unit.GetPod(oldPodInfo)
		}
		if storedOldPodInfo != nil {
			// update podInfo
			unit.UpdatePod(updatePod(storedOldPodInfo, newPod))
			// update unit
			if err := p.updateUnit(unit, theQueueUnitIn); err != nil {
				klog.ErrorS(err, "Failed to update pod in the unit", "pod", klog.KObj(oldPod))
				return err
			}
			klog.V(4).InfoS("Updated pod in the queue", "pod", klog.KObj(newPod))

			return nil
		} else {
			// unit not found or pod is still in the waitingList
			// if the pod is in the waitingList, update it.
			if err := p.updatePodInWaitingList(oldPod, newPod); err == nil {
				// found in the waitingList, update the pod.
				klog.V(4).InfoS("Updated pod in waiting list", "pod", klog.KObj(newPod))
				return nil
			} else {
				// not found in the waitingList, add the pod later.
				klog.V(6).InfoS("Unit not found in the queue", "oldPod", klog.KObj(oldPod), "newPod", klog.KObj(newPod))
			}
		}

	}

	// oldPod is nil, or unit is nil
	// If pod is not present in any of the queues, add to the respective queue
	err := p.addPodUsingAnnotations(p.newQueuedPodInfo(newPod))
	return err
}

func updatePod(oldPodInfo interface{}, newPod *v1.Pod) *framework.QueuedPodInfo {
	pInfo := oldPodInfo.(*framework.QueuedPodInfo)
	pInfo.Pod = newPod

	nominatedNode, _ := utils.GetPodNominatedNode(newPod)
	pInfo.NominatedNode = nominatedNode

	return pInfo
}

// Delete deletes the pod from its respective queue. It assumes that pod is only
// present in one queue
func (p *PriorityQueue) Delete(pod *v1.Pod) error {
	p.lock.Lock()
	defer p.lock.Unlock()

	pInfo := newQueuedPodInfoWithoutTimestamp(pod)
	// get unit key
	key := binderutils.GetUnitIdentifier(pInfo)

	// get unit object
	unit, theQueueUnitIn := p.getUnit(key)
	if unit == nil {
		// unit not found in the queue, just return
		klog.V(6).InfoS("In Delete, unit not found", "pod", klog.KObj(pod))
		// whether the pods are in the waiting list
		p.deletePodInWaitingList(pod)
		return nil
	}

	if len(unit.GetPods()) == 1 {
		// delete unit
		if unit.GetPod(pInfo) != nil {
			if err := p.deleteUnit(unit, theQueueUnitIn); err != nil {
				klog.ErrorS(err, "Failed to delete pod in the unit", "pod", klog.KObj(pod))
				unit.DeletePod(pInfo)
				return err
			}
			// TODO: delete pod before unit deletion. note the unit key
			unit.DeletePod(pInfo)
		} else {
			klog.V(5).InfoS("Pod was not in the unit now, skipping", "pod", klog.KObj(pod), "unitKey", key)
		}
	} else {
		// update unit
		unit.DeletePod(pInfo)
		if err := p.updateUnit(unit, theQueueUnitIn); err != nil {
			klog.ErrorS(err, "Failed to update pod in the unit", "pod", klog.KObj(pod))
			return err
		}
	}

	return nil
}

// Pop removes the head of the active queue and returns it. It blocks if the
// waitingUnitQ is empty and waits until a new item is added to the queue. It
// increments scheduling cycle when a pod is popped.
func (p *PriorityQueue) Pop() (*framework.QueuedUnitInfo, error) {
	p.lock.Lock()
	defer p.lock.Unlock()

	for p.readyUnitQ.Len() == 0 {
		// When the queue is empty, invocation of Pop() is blocked until new item is enqueued.
		// When Close() is called, the p.closed is set and the condition is broadcast,
		// which causes this loop to continue and return from the Pop().
		if p.closed {
			return nil, fmt.Errorf(queueClosed)
		}
		p.cond.Wait()
	}

	obj, err := p.readyUnitQ.Pop()
	if err != nil {
		return nil, err
	}

	unit := obj.(*framework.QueuedUnitInfo)
	for _, pInfo := range unit.GetPods() {
		if pInfo == nil {
			continue
		}
		pInfo.Attempts++
	}
	unit.Attempts++

	return unit, err
}

// TODO: avoid polling the waitingUnit
func (p *PriorityQueue) moveToReadyQueue() {
	p.lock.Lock()
	defer p.lock.Unlock()

	for {
		u := p.unitBackoffQ.Peek()
		if u == nil {
			return
		}
		unit := u.(*framework.QueuedUnitInfo)

		boTime := p.getUnitBackoffTime(unit)
		if boTime.After(p.clock.Now()) {
			return
		}

		_, err := p.unitBackoffQ.Pop()
		if err != nil {
			klog.InfoS("Unable to pop unit from unitBackoffQ", "unitKey", unit.GetKey())
			return
		}

		if err := p.readyUnitQ.Add(unit); err != nil {
			klog.InfoS("Unable to add unit into readyQ", "unitKey", unit.GetKey())
			return
		}

		defer p.cond.Broadcast()
	}
}

// TODO :remove this func, can be triggered by podGroup add event.
func (p *PriorityQueue) checkWaitingPod() {
	p.lock.Lock()
	defer p.lock.Unlock()
	tmpList := p.waitingPodsList
	p.waitingPodsList = make(map[ktypes.UID]*framework.QueuedPodInfo)
	for _, pInfo := range tmpList {
		p.addPodUsingAnnotations(pInfo)
	}
}

// MakeNextUnitFunc returns a function to retrieve the next pod from a given
// active queue
func MakeNextUnitFunc(queue BinderQueue) func() *framework.QueuedUnitInfo {
	return func() *framework.QueuedUnitInfo {
		unit, err := queue.Pop()
		if err == nil {
			klog.V(4).InfoS("About to try and schedule unit", "unitKey", unit.GetKey())
			for _, info := range unit.GetPods() {
				info.UpdateQueueStage("-")
				if info.QueueSpan != nil {
					// finish queue span
					info.QueueSpan.FinishSpan(tracing.BinderPendingInQueueSpan, tracing.GetSpanContextFromPod(info.Pod), info.Timestamp)
				}
			}
			return unit
		}
		klog.InfoS("Error occurred while retrieving next pod from scheduling queue", "err", err)
		return nil
	}
}

func podInfoKeyFunc(obj interface{}) (string, error) {
	return k8scache.MetaNamespaceKeyFunc(obj.(*framework.QueuedPodInfo).Pod)
}

func unitKeyFunc(obj interface{}) (string, error) {
	return obj.(*framework.QueuedUnitInfo).GetKey(), nil
}

func (p *PriorityQueue) unitCompareBackoffCompleted(unit1, unit2 interface{}) bool {
	bo1 := p.getUnitBackoffTime(unit1.(*framework.QueuedUnitInfo))
	bo2 := p.getUnitBackoffTime(unit2.(*framework.QueuedUnitInfo))
	return bo1.Before(bo2)
}

// getBackoffTime returns the time that unitInfo completes backoff
func (p *PriorityQueue) getUnitBackoffTime(unitInfo *framework.QueuedUnitInfo) time.Time {
	duration := p.calculateBackoffDuration(unitInfo)
	backoffTime := unitInfo.Timestamp.Add(duration)
	return backoffTime
}

// calculateBackoffDuration is a helper function for calculating the backoffDuration
// based on the number of attempts the pod has made.
func (p *PriorityQueue) calculateBackoffDuration(unitInfo *framework.QueuedUnitInfo) time.Duration {
	duration := p.podInitialBackoffDuration
	// TODO: get attempts from unit
	for i := 1; i < unitInfo.Attempts; i++ {
		duration = duration * 2
		if duration > p.podMaxBackoffDuration {
			return p.podMaxBackoffDuration
		}
	}
	return duration
}

// Run starts the goroutine to pump from podBackoffQ to waitingUnitQ
func (p *PriorityQueue) Run() {
	go wait.Until(p.moveToReadyQueue, 1.0*time.Second, p.stop)
	go wait.Until(p.checkWaitingPod, 1.0*time.Second, p.stop)
}

// Close closes the priority queue
func (p *PriorityQueue) Close() {
	p.lock.Lock()
	defer p.lock.Unlock()

	close(p.stop)
	p.closed = true
	p.cond.Broadcast()
}

// PendingPods returns all the pending pods in the queue. This function is
// used for debugging purposes in the scheduler cache dumper and comparer.
func (p *PriorityQueue) PendingPods() []*v1.Pod {
	p.lock.RLock()
	defer p.lock.RUnlock()
	var result []*v1.Pod
	for _, unit := range p.waitingUnitQ.List() {
		pInfos := unit.(*framework.QueuedUnitInfo).GetPods()
		for _, pInfo := range pInfos {
			result = append(result, pInfo.Pod)
		}
	}
	for _, unit := range p.unitBackoffQ.List() {
		pInfos := unit.(*framework.QueuedUnitInfo).GetPods()
		for _, pInfo := range pInfos {
			result = append(result, pInfo.Pod)
		}
	}
	for _, unit := range p.readyUnitQ.List() {
		pInfos := unit.(*framework.QueuedUnitInfo).GetPods()
		for _, pInfo := range pInfos {
			result = append(result, pInfo.Pod)
		}
	}
	return result
}

func (p *PriorityQueue) getUnit(key string) (*framework.QueuedUnitInfo, *heap.Heap) {
	// in the waiting unit queue
	if u, _, _ := p.waitingUnitQ.GetByKey(key); u != nil {
		klog.V(4).InfoS("Got unit from waitingUnitQ", "unitKey", key)
		return u.(*framework.QueuedUnitInfo), p.waitingUnitQ
	} else if u, _, _ := p.unitBackoffQ.GetByKey(key); u != nil {
		klog.V(4).InfoS("Got unit from unitBackoffQ", "unitKey", key)
		return u.(*framework.QueuedUnitInfo), p.unitBackoffQ
	} else if u, _, _ := p.readyUnitQ.GetByKey(key); u != nil {
		// in the ready unit queue
		klog.V(4).InfoS("Got unit from readyUnitQ", "unitKey", key)
		return u.(*framework.QueuedUnitInfo), p.readyUnitQ
	}
	return nil, nil
}

func (p *PriorityQueue) updateUnit(unit *framework.QueuedUnitInfo, theQueueUnitIn *heap.Heap) error {
	// get the key of the unit.
	key := unit.GetKey()
	// in the waiting unit queue
	if u, _, _ := theQueueUnitIn.GetByKey(key); u != nil {
		if p.readyUnitQ == theQueueUnitIn {
			// in the ready unit queue
			if !unit.ReadyToBePopulated() && p.cache.GetUnitSchedulingStatus(key) == status.ScheduledStatus {
				err := p.readyUnitQ.Delete(unit)
				if err != nil {
					return err
				}
				return p.waitingUnitQ.Add(unit)
			} else {
				return p.readyUnitQ.Update(nil, unit)
			}
		} else {
			return theQueueUnitIn.Update(nil, u)
		}
	}

	return nil
}

func (p *PriorityQueue) deleteUnit(unit *framework.QueuedUnitInfo, theQueueUnitIn *heap.Heap) error {
	// get the key of the unit.
	if theQueueUnitIn == nil {
		return fmt.Errorf("unit %s not found in the queue", unit.GetKey())
	} else {
		return theQueueUnitIn.Delete(unit)
	}
}

func (p *PriorityQueue) AddUnitPreemptor(unit *framework.QueuedUnitInfo) {
	p.lock.Lock()
	defer p.lock.Unlock()

	now := time.Now()
	unit.SetEnqueuedTimeStamp(time.Now())
	unit.Timestamp = now

	preUnit, queue := p.getUnit(unit.UnitKey)
	if preUnit != nil && queue != nil {
		queue.Delete(preUnit)
		for _, pod := range preUnit.GetPods() {
			if unit.GetPod(pod) == nil {
				unit.AddPod(pod)
			}
		}
	}
	p.unitBackoffQ.Add(unit)
}

// ActiveWaitingUnit checks and move unit from waitingQ to readyQ
func (p *PriorityQueue) ActiveWaitingUnit(unitKey string) {
	p.lock.Lock()
	defer p.lock.Unlock()

	unit, queue := p.getUnit(unitKey)
	if queue != p.waitingUnitQ || unit == nil {
		return
	}

	if !unit.ReadyToBePopulated() && p.cache.GetUnitSchedulingStatus(unit.GetKey()) != status.ScheduledStatus {
		return
	}

	if err := p.waitingUnitQ.Delete(unit); err != nil {
		klog.InfoS("Unable to delete unit from waitingQ", "unitKey", unit.GetKey())
		return
	}

	if err := p.readyUnitQ.Add(unit); err != nil {
		klog.InfoS("Unable to add unit to readyQ", "unitKey", unit.GetKey())
		return
	}

	// ready to be populated
	defer p.cond.Broadcast()

	pods := unit.GetPods()
	nPods := len(pods)
	if nPods <= 0 {
		return
	}

	for _, podInfo := range pods {
		klog.V(4).InfoS("Added pod to readyUnitQ", "pod", klog.KObj(podInfo.Pod))
		metrics.BinderQueueIncomingPodsAdd(podInfo.GetPodProperty(), "ready", PodAdd, float64(nPods))
	}
}
