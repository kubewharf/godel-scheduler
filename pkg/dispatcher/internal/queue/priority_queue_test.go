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
	"testing"

	"github.com/google/go-cmp/cmp"
)

func TestPriorityQueue(t *testing.T) {
	tests := []struct {
		name string
		ops  []operation
		want []string
	}{
		{
			name: "normal case",
			ops: []operation{
				{
					op:  AddOp,
					key: "p0",
				},
				{
					op:  AddOp,
					key: "p1",
				},
				{
					op:  AddOp,
					key: "p2",
				},
				{
					op:  AddOp,
					key: "p3",
				},
				{
					op:  AddOp,
					key: "p4",
				},
				{
					op: PopOp,
				},
			},
			want: []string{"p3", "p2", "p1", "p0"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pQueue := NewPriorityQueue(func(i1, i2 interface{}) bool {
				x, y := i1.(*QueuedPodInfo), i2.(*QueuedPodInfo)
				return x.PodKey > y.PodKey
			})

			for _, singleOp := range tt.ops {
				info := makeQueuedPodInfo(singleOp.key)
				switch singleOp.op {
				case AddOp:
					pQueue.Push(info)
				case PopOp:
					pQueue.Pop()
				}
			}

			got := []string{}
			for pQueue.Len() > 0 {
				info := pQueue.Pop()
				got = append(got, parsePodKey(info.(*QueuedPodInfo).PodKey))
			}

			if diff := cmp.Diff(got, tt.want); len(diff) > 0 {
				t.Errorf("Unexpected got diff: %v", diff)
			}

			if !pQueue.Empty() {
				t.Errorf("Unexpected got non-empty priority queue")
			}
		})
	}
}
