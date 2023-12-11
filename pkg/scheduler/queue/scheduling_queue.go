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

// This file contains structures that implement scheduling queue types.
// Scheduling queues hold pods waiting to be scheduled. This file implements a/
// priority queue which has two sub queues. One sub-queue holds pods that are
// being considered for scheduling. This is called activeQ. Another queue holds
// pods that are already tried and are determined to be unschedulable. The latter
// is called unschedulableQ.

package queue

import (
	"time"

	"github.com/kubewharf/godel-scheduler-api/pkg/client/listers/scheduling/v1alpha1"
	v1 "k8s.io/api/core/v1"
	schedulingv1 "k8s.io/client-go/listers/scheduling/v1"

	framework "github.com/kubewharf/godel-scheduler/pkg/framework/api"
	"github.com/kubewharf/godel-scheduler/pkg/scheduler/cache"
	"github.com/kubewharf/godel-scheduler/pkg/util/heap"
)

const (
	// If the unit stays in unschedulableQ longer than the unschedulableQTimeInterval,
	// the unit will be moved from unschedulableQ to activeQ.
	unschedulableQTimeInterval = 60 * time.Second

	queueClosed = "scheduling queue is closed"
)

// SchedulingQueue is an interface for a queue to store pods waiting to be scheduled.
// The interface follows a pattern similar to cache.FIFO and cache.Heap and
// makes it easy to use those data structures as a SchedulingQueue.
type SchedulingQueue interface {
	// Run starts the goroutines managing the queue.
	Run()
	// Close closes the SchedulingQueue so that the goroutine which is
	// waiting to pop items can exit gracefully.
	Close()
	CanBeRecycle() bool

	Add(pod *v1.Pod) error
	// AddUnschedulableIfNotPresent adds an unschedulable pod back to scheduling queue.
	// The unitSchedulingCycle represents the current scheduling cycle number which can be
	// returned by calling SchedulingCycle().
	AddUnschedulableIfNotPresent(unitInfo *framework.QueuedUnitInfo, unitSchedulingCycle int64) error
	Update(oldPod, newPod *v1.Pod) error
	Delete(pod *v1.Pod) error
	AssignedPodAdded(pod *v1.Pod)
	AssignedPodUpdated(pod *v1.Pod)
	MoveAllToActiveOrBackoffQueue(event string)
	ActivePodGroupUnit(unitKey string)

	Pop() (*framework.QueuedUnitInfo, error)
	Peek() *framework.QueuedUnitInfo

	PendingPods() []*v1.Pod
	// NumUnschedulableUnits returns the number of unschedulable pods exist in the SchedulingQueue.
	NumUnschedulableUnits() int
	// SchedulingCycle returns the current number of scheduling cycle which is
	// cached by scheduling queue. Normally, incrementing this number whenever
	// a pod is popped (e.g. called Pop()) is enough.
	SchedulingCycle() int64
}

type SubQueue interface {
	Add(interface{}) error
	Update(interface{}, interface{}) error
	Delete(interface{}) error
	DeleteByKey(string) error
	GetByKey(string) (interface{}, bool, error)

	Peek() interface{}
	Pop() (interface{}, error)
	Process(f heap.ProcessFunc)

	String() string
	Len() int
	List() []interface{}
}

// NewSchedulingQueue initializes a priority queue as a new scheduling queue.
func NewSchedulingQueue(
	cache cache.SchedulerCache,
	pcLister schedulingv1.PriorityClassLister,
	pgLister v1alpha1.PodGroupLister,
	lessFn framework.UnitLessFunc,
	useBlockQueue bool,
	opts ...Option,
) SchedulingQueue {
	if useBlockQueue {
		return NewBlockQueue(cache, pcLister, pgLister, lessFn, opts...)
	}
	return NewPriorityQueue(cache, pcLister, pgLister, lessFn, opts...)
}
