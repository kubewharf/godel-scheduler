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
	"time"

	"k8s.io/klog/v2"

	"github.com/kubewharf/godel-scheduler/pkg/common/metrics"
)

type SortedQueue interface {
	// AddPodInfo will add pod to queue if not exists. If exists, update it
	AddPodInfo(podInfo *QueuedPodInfo) error
	// PopPodInfo will pop pod from the queue
	PopPodInfo() (*QueuedPodInfo, error)
	// PodInfoExist checks whether pod exists in the queue
	PodInfoExist(podInfo *QueuedPodInfo) bool
	// UpdatePodInfo will update pod in queue if exists. If not exists, add it
	UpdatePodInfo(podInfo *QueuedPodInfo) error
	// RemovePodInfo will remove pod from the queue if exists
	RemovePodInfo(podInfo *QueuedPodInfo) error
	// Close the queue
	Close()
}

type SortedFIFO struct {
	fifo *MetricsFIFO
}

var _ = SortedQueue(&SortedFIFO{})

func NewSortedFIFO(metricRecorder metrics.MetricRecorder) *SortedFIFO {
	sf := &SortedFIFO{
		fifo: NewMetricsFIFO(metricRecorder, func(old, new interface{}) {
			existed, ok := old.(*QueuedPodInfo)
			if !ok {
				klog.InfoS("Failed to parse old object to *QueuedPodInfo", "oldObject", old)
				return
			}
			podInfo, ok := new.(*QueuedPodInfo)
			if !ok {
				klog.InfoS("Failed to parse new object to *QueuedPodInfo", "newObject", new)
				return
			}
			podInfo.Timestamp = existed.Timestamp
			podInfo.InitialAddedTimestamp = existed.InitialAddedTimestamp
		}),
	}
	return sf
}

func (s *SortedFIFO) AddPodInfo(podInfo *QueuedPodInfo) error {
	start := time.Now()

	if podInfo.Timestamp.IsZero() {
		podInfo.Timestamp = start
	}
	if podInfo.InitialAddedTimestamp.IsZero() {
		podInfo.InitialAddedTimestamp = start
	}

	return s.fifo.Add(podInfo)
}

func (s *SortedFIFO) UpdatePodInfo(podInfo *QueuedPodInfo) error {
	now := time.Now()
	if podInfo.Timestamp.IsZero() {
		podInfo.Timestamp = now
	}
	if podInfo.InitialAddedTimestamp.IsZero() {
		podInfo.InitialAddedTimestamp = now
	}

	return s.fifo.Update(podInfo)
}

func (s *SortedFIFO) PopPodInfo() (*QueuedPodInfo, error) {
	result, err := s.fifo.Pop()
	if err != nil {
		return nil, err
	}
	return result.(*QueuedPodInfo), err
}

func (s *SortedFIFO) PodInfoExist(podInfo *QueuedPodInfo) bool {
	return s.fifo.Exists(podInfo)
}

func (s *SortedFIFO) RemovePodInfo(podInfo *QueuedPodInfo) error {
	return s.fifo.Delete(podInfo)
}

func (s *SortedFIFO) Close() {
	s.fifo.Close()
}
