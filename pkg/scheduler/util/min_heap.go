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

package util

import (
	"fmt"

	"github.com/kubewharf/godel-scheduler/pkg/framework/api"
)

// MinHeap implements a min-heap with the smallest of all data stored at the top of the heap.
// We need to control the lock of this data structure at the upper level.
type MinHeap struct {
	size        int
	scoredNodes []api.NodeScore
}

// NewMinHeap return a pointer to MinHeap.
// We need to control the lock of this data structure at the upper level.
func NewMinHeap() *MinHeap {
	return &MinHeap{}
}

func Parent(i int) int {
	if i == 0 {
		return 0
	}
	return (i - 1) / 2
}

func LeftChild(i int) int {
	return 2*i + 1
}

func (heap *MinHeap) IsEmpty() bool {
	return heap.size == 0
}

func (heap *MinHeap) GetSize() int {
	return heap.size
}

func (heap *MinHeap) GetMinVal() (api.NodeScore, error) {
	if heap.IsEmpty() {
		return api.NodeScore{}, fmt.Errorf("GetMinVal failed, MinHeap is empty")
	}
	return heap.scoredNodes[0], nil
}

func siftDown(heap *MinHeap, parI int) {
	var minI int
	for {
		leftI := LeftChild(parI)
		switch {
		case leftI+1 > heap.size:
			return
		case leftI+2 > heap.size:
			if heap.scoredNodes[parI].Score > heap.scoredNodes[leftI].Score {
				heap.scoredNodes[parI], heap.scoredNodes[leftI] = heap.scoredNodes[leftI],
					heap.scoredNodes[parI]
			}
			return
		case heap.scoredNodes[leftI].Score <= heap.scoredNodes[leftI+1].Score:
			minI = leftI
		case heap.scoredNodes[leftI].Score > heap.scoredNodes[leftI+1].Score:
			minI = leftI + 1
		}

		if heap.scoredNodes[parI].Score > heap.scoredNodes[minI].Score {
			heap.scoredNodes[parI], heap.scoredNodes[minI] = heap.scoredNodes[minI],
				heap.scoredNodes[parI]
		}
		parI = minI
	}
}

func (heap *MinHeap) SiftUp(priorityNode api.NodeScore) {
	heap.scoredNodes = append(heap.scoredNodes, priorityNode)
	parI := Parent(heap.size)
	childI := heap.size

	for heap.scoredNodes[parI].Score > heap.scoredNodes[childI].Score {
		heap.scoredNodes[parI], heap.scoredNodes[childI] = heap.scoredNodes[childI],
			heap.scoredNodes[parI]
		childI = parI
		parI = Parent(parI)
	}
	heap.size++
}

func (heap *MinHeap) SiftDown() (api.NodeScore, error) {
	minVal, err := heap.GetMinVal()
	if err != nil {
		return minVal, err
	}

	heap.size--
	heap.scoredNodes[0], heap.scoredNodes[heap.size] = heap.scoredNodes[heap.size], api.NodeScore{}

	siftDown(heap, 0)
	return minVal, nil
}

func (heap *MinHeap) Replace(priorityNode api.NodeScore) error {
	_, err := heap.GetMinVal()
	if err != nil {
		return fmt.Errorf("GetMinVal error: %+v", err)
	}
	heap.scoredNodes[0] = priorityNode

	siftDown(heap, 0)
	return nil
}

// TopHighestScoreNodes Get the top m high score nodes
func TopHighestScoreNodes(priorityNodes []api.NodeScore, m int) (resp []api.NodeScore) {
	h := NewMinHeap()

	for i := 0; i < m; i++ {
		h.SiftUp(priorityNodes[i])
	}

	minVal, _ := h.GetMinVal()
	for _, v := range priorityNodes[m:] {
		if v.Score > minVal.Score {
			h.Replace(v)
			minVal, _ = h.GetMinVal()
		}
	}
	for i := 0; i < m; i++ {
		v, _ := h.SiftDown()
		resp = append(resp, v)
	}
	return
}
