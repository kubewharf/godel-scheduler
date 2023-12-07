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
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func makeQueuedPodInfo(key string) *QueuedPodInfo {
	queuedPodInfo, _ := NewQueuedPodInfo(&v1.Pod{ObjectMeta: metav1.ObjectMeta{Name: key, Namespace: "default"}})
	return queuedPodInfo
}

func makePodKey(key string) string {
	return "default" + "/" + key
}

func parsePodKey(podKey string) string {
	slice := strings.Split(podKey, "/")
	return slice[1]
}

const (
	AddOp int = iota
	DeleteOp
	UpdateOp
	PopOp
)

type operation struct {
	op  int
	key string
}

func TestMetricsFIFO(t *testing.T) {
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
					op:  UpdateOp,
					key: "p3",
				},
				{
					op:  DeleteOp,
					key: "p1",
				},
				{
					op: PopOp,
				},
			},
			want: []string{"p2", "p3", "p4"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fifo := NewMetricsFIFO(nil, func(old, new interface{}) {})

			for _, singleOp := range tt.ops {
				info := makeQueuedPodInfo(singleOp.key)
				switch singleOp.op {
				case AddOp:
					fifo.Add(info)
				case DeleteOp:
					fifo.Delete(info)
				case UpdateOp:
					fifo.Update(info)
				case PopOp:
					fifo.Pop()
				}
			}

			got := []string{}
			for len(fifo.items) > 0 {
				info, _ := fifo.Pop()
				got = append(got, parsePodKey(info.(*QueuedPodInfo).PodKey))
			}

			for _, singleOp := range tt.ops {
				info := makeQueuedPodInfo(singleOp.key)
				if existing := fifo.Exists(info); existing {
					t.Errorf("Unexpected pod info in FIFO: %v", info.PodKey)
				}
			}

			if diff := cmp.Diff(got, tt.want); len(diff) > 0 {
				t.Errorf("Unexpected got diff: %v", diff)
			}

			fifo.Close()
			if !fifo.IsClosed() {
				t.Errorf("Unexpected FIFO not closed")
			}
		})
	}
}
