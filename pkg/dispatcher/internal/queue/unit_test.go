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
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/kubewharf/godel-scheduler-api/pkg/apis/scheduling/v1alpha1"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/client-go/tools/events"
)

var podGroupKey = "default/pg"

func makePodGroup() *v1alpha1.PodGroup {
	return &v1alpha1.PodGroup{
		ObjectMeta: v1.ObjectMeta{
			Namespace: "default",
			Name:      "pg",
		},
		Spec: v1alpha1.PodGroupSpec{
			MinMember: 3,
		},
		Status: v1alpha1.PodGroupStatus{
			Phase: v1alpha1.PodGroupPending,
		},
	}
}

func makeScheduledPodGroup() *v1alpha1.PodGroup {
	pg := makePodGroup()
	pg.Status.Phase = v1alpha1.PodGroupFinished
	return pg
}

const (
	AddPodGroupOp int = iota
	DeletePodGroupOp
	UpdatePodGroupOp
	AddPodOp
	DeletePodOp
	AddUnSortedPodOp
	DeleteUnSortedPodOp
	UnitInfoPopOp
	UnitInfoEnqueueOp
	GetAssignedOp
	AssignOp
)

func TestUnitInfos(t *testing.T) {
	tests := []struct {
		name string
		ops  []operation
		want []string
	}{
		{
			name: "normal pending pods",
			ops: []operation{
				{
					op: AddPodGroupOp,
				},
				{
					op: UpdatePodGroupOp,
				},
				{
					op:  AddUnSortedPodOp,
					key: "p0",
				},
				{
					op:  AddUnSortedPodOp,
					key: "p1",
				},
				{
					op:  AddUnSortedPodOp,
					key: "p2",
				},
			},
			want: []string{"p0", "p1", "p2"},
		},
		{
			name: "scheduled pod",
			ops: []operation{
				{
					op: AddPodGroupOp,
				},
				{
					op:  AddPodOp,
					key: "p0",
				},
				{
					op:  AddPodOp,
					key: "p1",
				},
				{
					op:  AddPodOp,
					key: "p2",
				},
			},
			want: []string{},
		},
		{
			name: "scheduled pod and pending pod",
			ops: []operation{
				{
					op: AddPodGroupOp,
				},
				{
					op:  AddPodOp,
					key: "p0",
				},
				{
					op:  AddUnSortedPodOp,
					key: "p1",
				},
				{
					op:  AddPodOp,
					key: "p2",
				},
			},
			want: []string{"p1"},
		},
		{
			name: "scheduled pod and pending pod but poped",
			ops: []operation{
				{
					op: AddPodGroupOp,
				},
				{
					op:  AddPodOp,
					key: "p0",
				},
				{
					op:  AddPodOp,
					key: "p1",
				},
				{
					op:  AddUnSortedPodOp,
					key: "p2",
				},
				{
					op: UnitInfoPopOp,
				},
			},
			want: []string{},
		},
		{
			name: "asign and get scheduler",
			ops: []operation{
				{
					op: AddPodGroupOp,
				},
				{
					op:  AssignOp,
					key: "scheduler-0",
				},
				{
					op:  GetAssignedOp,
					key: "scheduler-0",
				},
			},
			want: []string{},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			infos := NewUnitInfos(events.NewFakeRecorder(1000))

			for _, singleOp := range tt.ops {
				switch singleOp.op {
				case AddPodGroupOp:
					infos.AddPodGroup(makePodGroup())
				case DeletePodGroupOp:
					infos.DeletePodGroup(makePodGroup())
				case UpdatePodGroupOp:
					infos.UpdatePodGroup(nil, makePodGroup())
				case AddPodOp:
					infos.AddPod(podGroupKey, makePodKey(singleOp.key))
				case DeletePodOp:
					infos.DeletePod(podGroupKey, makePodKey(singleOp.key))
				case AddUnSortedPodOp:
					infos.AddUnSortedPodInfo(podGroupKey, makeQueuedPodInfo(singleOp.key))
				case DeleteUnSortedPodOp:
					infos.DeleteUnSortedPodInfo(podGroupKey, makeQueuedPodInfo(singleOp.key))
				case UnitInfoPopOp:
					infos.Pop()
				case UnitInfoEnqueueOp:
					infos.Enqueue(makeQueuedPodInfo(singleOp.key))
				case GetAssignedOp:
					got := infos.GetAssignedSchedulerForPodGroupUnit(makePodGroup())
					if diff := cmp.Diff(got, singleOp.key); len(diff) > 0 {
						t.Errorf("Unexpected got diff: %v", diff)
					}
				case AssignOp:
					infos.AssignSchedulerToPodGroupUnit(makePodGroup(), singleOp.key, true)
				}
			}
			infos.(*unitInfos).populate()

			got := []string{}
			for len(infos.(*unitInfos).readyUnitPods.List()) > 0 {
				info, _ := infos.Pop()
				got = append(got, parsePodKey(info.PodKey))
			}
			x, y := sets.NewString(got...).List(), sets.NewString(tt.want...).List()
			if diff := cmp.Diff(x, y); len(diff) > 0 {
				t.Errorf("Unexpected got diff: %v", diff)
			}

			infos.DeletePodGroup(makePodGroup())
			for _, singleOp := range tt.ops {
				if singleOp.op == AddPodOp || singleOp.op == AddUnSortedPodOp {
					infos.DeletePod(podGroupKey, makePodKey(singleOp.key))
					infos.DeleteUnSortedPodInfo(podGroupKey, makeQueuedPodInfo(singleOp.key))
				}
			}
		})
	}
}
