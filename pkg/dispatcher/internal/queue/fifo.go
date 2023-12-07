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
	"sync"
	"time"

	"k8s.io/klog/v2"

	"github.com/kubewharf/godel-scheduler/pkg/common/metrics"
	"github.com/kubewharf/godel-scheduler/pkg/util/helper"
)

// danglingIndicesThreshold is the threshold between the number of indices and the number of items.
const danglingIndicesThreshold = 100

type UpdateHandler func(old, new interface{})

type MetricsFIFO struct {
	lock           sync.RWMutex
	cond           sync.Cond
	metricRecorder metrics.MetricRecorder

	// We depend on the property that every key in `items` is also in `queue`
	items   map[string]interface{}
	queue   []string
	keyFunc KeyFunc

	updateHandler UpdateHandler

	// Indication the queue is closed.
	// Used to indicate a queue is closed so a control loop can exit when a queue is empty.
	// Currently, not used to gate any of CRED operations.
	closed     bool
	closedLock sync.Mutex
}

func NewMetricsFIFO(metricRecorder metrics.MetricRecorder, handler UpdateHandler) *MetricsFIFO {
	sf := &MetricsFIFO{
		items:          map[string]interface{}{},
		queue:          []string{},
		keyFunc:        podInfoKeyFunc,
		updateHandler:  handler,
		metricRecorder: metricRecorder,
	}
	sf.cond.L = &sf.lock
	return sf
}

// update add object to queue. If exists, update existing one.
// return error of the updating and whether pod is existed in the queue.
// Must acquire lock before using updatePod
func (f *MetricsFIFO) update(obj interface{}) error {
	id, err := f.keyFunc(obj)
	if err != nil {
		return KeyError{Obj: obj, Err: err}
	}
	existed, exists := f.items[id]
	if exists {
		f.updateHandler(existed, obj)
	} else {
		if f.metricRecorder != nil {
			f.metricRecorder.Inc(obj)
		}
		f.queue = append(f.queue, id)
	}
	f.items[id] = obj
	f.cond.Broadcast()
	return err
}

func (f *MetricsFIFO) Add(obj interface{}) error {
	start := time.Now()
	defer func() {
		if f.metricRecorder != nil {
			f.metricRecorder.AddingLatencyInSeconds(obj, helper.SinceInSeconds(start))
		}
	}()
	f.lock.Lock()
	defer f.lock.Unlock()

	return f.update(obj)
}

func (f *MetricsFIFO) Update(obj interface{}) error {
	f.lock.Lock()
	defer f.lock.Unlock()

	return f.update(obj)
}

func (f *MetricsFIFO) Pop() (interface{}, error) {
	f.lock.Lock()
	defer f.lock.Unlock()
	for {
		for len(f.queue) == 0 {
			// When the queue is empty, invocation of Pop() is blocked until new item is enqueued.
			// When Close() is called, the f.closed is set and the condition is broadcasted.
			// Which causes this loop to continue and return from the Pop().
			if f.IsClosed() {
				return nil, ErrFIFOClosed
			}

			f.cond.Wait()
		}
		id := f.queue[0]
		f.queue = f.queue[1:]
		item, ok := f.items[id]
		if !ok {
			// Item may have been deleted subsequently.
			continue
		}
		delete(f.items, id)
		if f.metricRecorder != nil {
			f.metricRecorder.Dec(item)
		}
		return item, nil
	}
}

func (f *MetricsFIFO) Exists(obj interface{}) bool {
	id, err := f.keyFunc(obj)
	if err != nil {
		klog.InfoS("Failed to get key", "err", err)
		return false
	}
	f.lock.RLock()
	defer f.lock.RUnlock()

	_, exists := f.items[id]
	return exists
}

func (f *MetricsFIFO) Delete(obj interface{}) error {
	id, err := f.keyFunc(obj)
	if err != nil {
		return KeyError{Obj: obj, Err: err}
	}
	f.lock.Lock()
	defer f.lock.Unlock()

	if item, ok := f.items[id]; ok {
		if f.metricRecorder != nil {
			f.metricRecorder.Dec(item)
		}

		// remove dangling indices every time when an item is deleted is too expensive,
		// only do it when the number of dangling indices is larger than a threshold.
		if len(f.queue)-len(f.items) > danglingIndicesThreshold {
			f.removeDanglingIndices()
		}

		delete(f.items, id)
	}
	return nil
}

func (f *MetricsFIFO) removeDanglingIndices() {
	index := 0
	for _, id := range f.queue {
		if _, ok := f.items[id]; ok {
			f.queue[index] = id
			index++
		}
	}
	f.queue = f.queue[:index]
}

func (f *MetricsFIFO) Close() {
	f.closedLock.Lock()
	defer f.closedLock.Unlock()
	f.closed = true
	if f.metricRecorder != nil {
		f.metricRecorder.Clear()
	}
	f.cond.Broadcast()
}

// IsClosed checks if the queue is closed
func (f *MetricsFIFO) IsClosed() bool {
	f.closedLock.Lock()
	defer f.closedLock.Unlock()
	return f.closed
}
