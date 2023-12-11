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

package scheduler_maintainer

import (
	"reflect"
	"testing"
	"time"

	schedulerapi "github.com/kubewharf/godel-scheduler-api/pkg/apis/scheduling/v1alpha1"
	"github.com/kubewharf/godel-scheduler-api/pkg/client/clientset/versioned/fake"
	crdinformers "github.com/kubewharf/godel-scheduler-api/pkg/client/informers/externalversions"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func newSimpleActiveScheduler(schedulerName string) *schedulerapi.Scheduler {
	now := metav1.Now()
	return &schedulerapi.Scheduler{
		ObjectMeta: metav1.ObjectMeta{
			Name: schedulerName,
		},
		Spec: schedulerapi.SchedulerSpec{},
		Status: schedulerapi.SchedulerStatus{
			LastUpdateTime: &now,
		},
	}
}

func newSimpleInActiveScheduler(schedulerName string) *schedulerapi.Scheduler {
	t, _ := time.Parse(time.RFC3339, "2006-01-02T15:04:05Z07:00")
	oldTime := metav1.NewTime(t)
	return &schedulerapi.Scheduler{
		ObjectMeta: metav1.ObjectMeta{
			Name: schedulerName,
		},
		Spec: schedulerapi.SchedulerSpec{},
		Status: schedulerapi.SchedulerStatus{
			LastUpdateTime: &oldTime,
		},
	}
}

func TestSchedulerMaintainer_GetSchedulersWithMostAndLeastNumberOfNodes(t *testing.T) {
	tests := []struct {
		name             string
		schedulers       []*schedulerapi.Scheduler
		nodesToScheduler map[string]string
		want             *SchedulersWithMostAndLeastNumberOfNodes
	}{
		{
			name:       "only one schedulers exist",
			schedulers: []*schedulerapi.Scheduler{newSimpleActiveScheduler("test-schedulers-0")},
			nodesToScheduler: map[string]string{
				"node0": "test-schedulers-0",
				"node1": "test-schedulers-0",
			},
			want: &SchedulersWithMostAndLeastNumberOfNodes{
				MostNumberOfNodesSchedulerName:  "test-schedulers-0",
				MostNumberOfNodes:               2,
				LeastNumberOfNodesSchedulerName: "test-schedulers-0",
				LeastNumberOfNodes:              2,
			},
		},
		{
			name:       "two scheduler with different node list length",
			schedulers: []*schedulerapi.Scheduler{newSimpleActiveScheduler("test-schedulers-0"), newSimpleActiveScheduler("test-schedulers-1")},
			nodesToScheduler: map[string]string{
				"node0": "test-schedulers-0",
				"node1": "test-schedulers-0",
				"node2": "test-schedulers-0",
				"node3": "test-schedulers-1",
			},
			want: &SchedulersWithMostAndLeastNumberOfNodes{
				MostNumberOfNodesSchedulerName:  "test-schedulers-0",
				MostNumberOfNodes:               3,
				LeastNumberOfNodesSchedulerName: "test-schedulers-1",
				LeastNumberOfNodes:              1,
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fakeCli := fake.NewSimpleClientset()
			informerFactory := crdinformers.NewSharedInformerFactory(fakeCli, 0)
			maintainer := NewSchedulerMaintainer(fakeCli, informerFactory.Scheduling().V1alpha1().Schedulers().Lister())
			for _, scheduler := range tt.schedulers {
				maintainer.AddScheduler(scheduler)
			}

			for nodeName, schedulerName := range tt.nodesToScheduler {
				maintainer.addNodeToGodelScheduler(schedulerName, nodeName)
			}

			if got := maintainer.GetSchedulersWithMostAndLeastNumberOfNodes(); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("GetSchedulersWithMostAndLeastNumberOfNodes() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestSchedulerMaintainer_GetGeneralActiveSchedulerWithLeastNumberOfNodes(t *testing.T) {
	tests := []struct {
		name             string
		schedulers       []*schedulerapi.Scheduler
		nodesToScheduler map[string]string
		want             *SchedulersWithLeastNumberOfNodes
	}{
		{
			name:       "only one schedulers exist",
			schedulers: []*schedulerapi.Scheduler{newSimpleActiveScheduler("test-schedulers-0")},
			nodesToScheduler: map[string]string{
				"node0": "test-schedulers-0",
				"node1": "test-schedulers-0",
			},
			want: &SchedulersWithLeastNumberOfNodes{
				LeastNumberOfNodesSchedulerName: "test-schedulers-0",
				LeastNumberOfNodes:              2,
			},
		},
		{
			name:       "two scheduler with different node list length",
			schedulers: []*schedulerapi.Scheduler{newSimpleActiveScheduler("test-schedulers-0"), newSimpleActiveScheduler("test-schedulers-1")},
			nodesToScheduler: map[string]string{
				"node0": "test-schedulers-0",
				"node1": "test-schedulers-0",
				"node2": "test-schedulers-0",
				"node3": "test-schedulers-1",
			},
			want: &SchedulersWithLeastNumberOfNodes{
				LeastNumberOfNodesSchedulerName: "test-schedulers-1",
				LeastNumberOfNodes:              1,
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fakeCli := fake.NewSimpleClientset()
			informerFactory := crdinformers.NewSharedInformerFactory(fakeCli, 0)
			maintainer := NewSchedulerMaintainer(fakeCli, informerFactory.Scheduling().V1alpha1().Schedulers().Lister())
			for _, scheduler := range tt.schedulers {
				maintainer.AddScheduler(scheduler)
			}

			for nodeName, schedulerName := range tt.nodesToScheduler {
				maintainer.addNodeToGodelScheduler(schedulerName, nodeName)
			}

			if got := maintainer.GetGeneralActiveSchedulerWithLeastNumberOfNodes(); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("GetGeneralActiveSchedulerWithLeastNumberOfNodes() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestSchedulerMaintainer_GetEarliestInactiveCloneScheduler(t *testing.T) {
	tests := []struct {
		name       string
		schedulers []*schedulerapi.Scheduler
		want       string
	}{
		{
			name: "no inactive schedulers",
			schedulers: []*schedulerapi.Scheduler{
				newSimpleActiveScheduler("test-scheduler-0"),
				newSimpleActiveScheduler("test-scheduler-1"),
			},
			want: "",
		},
		{
			name: "return the earliest in active scheduler",
			schedulers: []*schedulerapi.Scheduler{
				newSimpleActiveScheduler("test-scheduler-0"),
				newSimpleInActiveScheduler("test-scheduler-1"),
			},
			want: "test-scheduler-1",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fakeCli := fake.NewSimpleClientset()
			informerFactory := crdinformers.NewSharedInformerFactory(fakeCli, 0)
			maintainer := NewSchedulerMaintainer(fakeCli, informerFactory.Scheduling().V1alpha1().Schedulers().Lister())
			for _, scheduler := range tt.schedulers {
				maintainer.AddScheduler(scheduler)
				maintainer.UpdateScheduler(scheduler, scheduler)
			}

			got := maintainer.GetEarliestInactiveCloneScheduler()
			if tt.want == "" && got == nil {
				return
			}

			if tt.want != "" && got == nil {
				t.Errorf("want scheduler name %v, get nil scheduelr", tt.want)
				return
			}

			if tt.want != got.GetScheduler().Name {
				t.Errorf("GetEarliestInactiveCloneScheduler() = %v, want %v", got, tt.want)
				return
			}
		})
	}
}
