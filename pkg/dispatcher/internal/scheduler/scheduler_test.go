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

package scheduler

import (
	"reflect"
	"testing"

	schedulerapi "github.com/kubewharf/godel-scheduler-api/pkg/apis/scheduling/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

var testScheduler = &schedulerapi.Scheduler{
	ObjectMeta: metav1.ObjectMeta{
		Name: "test-scheduler",
	},
}

func TestGodelScheduler_Clone(t *testing.T) {
	type fields struct {
		schedulerName     string
		scheduler         *schedulerapi.Scheduler
		active            bool
		nodePartitionType string
		taskSelector      v1.LabelSelector
		nodeSelector      v1.LabelSelector
		nodes             map[string]struct{}
	}
	tests := []struct {
		name   string
		fields fields
		want   *GodelScheduler
	}{
		{
			name: "test godel scheduler clone",
			fields: fields{
				schedulerName: "test-scheduler",
				scheduler:     testScheduler,
				active:        true,
				nodes: map[string]struct{}{
					"node1": {},
					"node2": {},
					"node3": {},
				},
			},
			want: &GodelScheduler{
				schedulerName: "test-scheduler",
				scheduler:     testScheduler,
				active:        false,
				nodes: map[string]struct{}{
					"node1": {},
					"node2": {},
					"node3": {},
				},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gs := &GodelScheduler{
				schedulerName:     tt.fields.schedulerName,
				scheduler:         tt.fields.scheduler,
				active:            tt.fields.active,
				nodePartitionType: tt.fields.nodePartitionType,
				taskSelector:      tt.fields.taskSelector,
				nodeSelector:      tt.fields.nodeSelector,
				nodes:             tt.fields.nodes,
			}

			if got := gs.Clone(); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("Clone() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestGodelScheduler_GetNodes(t *testing.T) {
	tests := []struct {
		name         string
		addedNodes   []string
		removedNodes []string
		want         map[string]struct{}
	}{
		{
			name:         "test godel scheduler get nodes",
			addedNodes:   []string{"node1", "node2", "node3"},
			removedNodes: []string{"node0", "node1"},
			want: map[string]struct{}{
				"node2": {},
				"node3": {},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gs := NewGodelSchedulerWithSchedulerName("test-scheduler")
			for _, node := range tt.addedNodes {
				gs.AddNode(node)
			}

			for _, node := range tt.removedNodes {
				gs.RemoveNode(node)
			}

			if got := gs.GetNodes(); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("GetNodes() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestGodelScheduler_NodeExists(t *testing.T) {
	type args struct {
		nodeName string
	}
	tests := []struct {
		name         string
		addedNodes   []string
		removedNodes []string
		args         args
		want         bool
	}{
		{
			name:         "node not exist",
			addedNodes:   []string{"node1", "node2"},
			removedNodes: []string{},
			args:         args{"node0"},
			want:         false,
		},
		{
			name:         "node has been deleted",
			addedNodes:   []string{"node1", "node2"},
			removedNodes: []string{"node1"},
			args:         args{"node1"},
			want:         false,
		},
		{
			name:         "node exist",
			addedNodes:   []string{"node1", "node2"},
			removedNodes: []string{"node2"},
			args:         args{"node1"},
			want:         true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gs := NewGodelSchedulerWithSchedulerName("test-scheduler")
			for _, node := range tt.addedNodes {
				gs.AddNode(node)
			}

			for _, node := range tt.removedNodes {
				gs.RemoveNode(node)
			}

			if got := gs.NodeExists(tt.args.nodeName); got != tt.want {
				t.Errorf("NodeExists() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestGodelScheduler_GetScheduler(t *testing.T) {
	tests := []struct {
		name          string
		scheduler     *schedulerapi.Scheduler
		schedulerName string
		want          *schedulerapi.Scheduler
	}{
		{
			name:          "scheduler not exist",
			scheduler:     nil,
			schedulerName: testScheduler.Name,
			want:          nil,
		},
		{
			name:          "scheduler exist",
			scheduler:     testScheduler,
			schedulerName: testScheduler.Name,
			want:          testScheduler,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gs := NewGodelSchedulerWithSchedulerName(testScheduler.Name)
			gs.SetScheduler(tt.scheduler)
			if got := gs.GetScheduler(); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("GetScheduler() = %v, want %v", got, tt.want)
			}
		})
	}
}
