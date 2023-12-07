/*
Copyright 2014 The Kubernetes Authors.

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
	"context"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/clock"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes/fake"
	"k8s.io/client-go/tools/cache"

	framework "github.com/kubewharf/godel-scheduler/pkg/framework/api"
	godelcache "github.com/kubewharf/godel-scheduler/pkg/scheduler/cache"
	"github.com/kubewharf/godel-scheduler/pkg/scheduler/cache/handler"
	godelqueue "github.com/kubewharf/godel-scheduler/pkg/scheduler/queue"
)

const (
	podInitialBackoffDurationSeconds = 1
	podMaxBackoffDurationSeconds     = 10
	testSchedulerName                = "test-scheduler"
)

var (
	disablePodPreemption = false
	testSchedulerSysName = "test-scheduler"
)

// Test configures a scheduler from a policies defined in a file
// It combines some configurable constraints with some pre-defined ones
func TestCreateFromConfig(t *testing.T) {
	// TODO(20201103): add config support later
}

func TestCreateFromConfigWithHardPodAffinitySymmetricWeight(t *testing.T) {
	// TODO(20201103): add config support later
}

func TestCreateFromEmptyConfig(t *testing.T) {
	// TODO(20201103): add config support later
}

// Test configures a scheduler from a policy that does not specify any
// constraints.
func TestCreateFromConfigWithUnspecifiedPredicatesOrPriorities(t *testing.T) {
	// TODO(20201103): add config support later
}

func TestDefaultErrorFunc(t *testing.T) {
	testPod := &v1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "test-pod", Namespace: "default"}}
	testPodUpdated := testPod.DeepCopy()
	testPodUpdated.Labels = map[string]string{"foo": ""}

	tests := []struct {
		name                       string
		injectErr                  error
		podUpdatedDuringScheduling bool // pod is updated during a scheduling cycle
		podDeletedDuringScheduling bool // pod is deleted during a scheduling cycle
		expect                     *v1.Pod
	}{
		{
			name:                       "pod is updated during a scheduling cycle",
			injectErr:                  nil,
			podUpdatedDuringScheduling: true,
			expect:                     testPodUpdated,
		},
		{
			name:      "pod is not updated during a scheduling cycle",
			injectErr: nil,
			expect:    testPod,
		},
		{
			name:                       "pod is deleted during a scheduling cycle",
			injectErr:                  nil,
			podDeletedDuringScheduling: true,
			expect:                     nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			stopCh := make(chan struct{})
			defer close(stopCh)

			client := fake.NewSimpleClientset(&v1.PodList{Items: []v1.Pod{*testPod}})
			informerFactory := informers.NewSharedInformerFactory(client, 0)
			podInformer := informerFactory.Core().V1().Pods()
			// Need to add/update/delete testPod to the store.
			podInformer.Informer().GetStore().Add(testPod)

			queue := godelqueue.NewPriorityQueue(nil, nil, nil, nil, godelqueue.WithClock(clock.NewFakeClock(time.Now())))

			schedulerCache := godelcache.New(handler.MakeCacheHandlerWrapper().
				SchedulerName("").SchedulerType("").SubCluster(framework.DefaultSubCluster).
				TTL(30 * time.Second).Period(10 * time.Second).StopCh(make(<-chan struct{})).
				EnableStore("PreemptionStore").
				Obj())

			queue.Add(testPod)
			queue.Pop()

			if tt.podUpdatedDuringScheduling {
				podInformer.Informer().GetStore().Update(testPodUpdated)
				queue.Update(testPod, testPodUpdated)
			}
			if tt.podDeletedDuringScheduling {
				podInformer.Informer().GetStore().Delete(testPod)
				queue.Delete(testPod)
			}

			testPodInfo := &framework.QueuedPodInfo{Pod: testPod}
			testUnit := framework.NewSinglePodUnit(testPodInfo)
			testUnitInfo := &framework.QueuedUnitInfo{
				UnitKey:      testUnit.GetKey(),
				ScheduleUnit: testUnit,
			}
			errFunc := MakeDefaultErrorFunc(client, schedulerCache)
			errFunc(testPodInfo, tt.injectErr)

			if !tt.podDeletedDuringScheduling {
				queue.AddUnschedulableIfNotPresent(testUnitInfo, queue.SchedulingCycle())
			}

			got := getPodFromPriorityQueue(queue, testPod)

			if diff := cmp.Diff(tt.expect, got); diff != "" {
				t.Errorf("Unexpected pod (-want, +got): %s", diff)
			}
		})
	}
}

// getPodFromPriorityQueue is the function used in the TestDefaultErrorFunc test to get
// the specific pod from the given priority queue. It returns the found pod in the priority queue.
func getPodFromPriorityQueue(queue *godelqueue.PriorityQueue, pod *v1.Pod) *v1.Pod {
	podList := queue.PendingPods()
	if len(podList) == 0 {
		return nil
	}

	queryPodKey, err := cache.MetaNamespaceKeyFunc(pod)
	if err != nil {
		return nil
	}

	for _, foundPod := range podList {
		foundPodKey, err := cache.MetaNamespaceKeyFunc(foundPod)
		if err != nil {
			return nil
		}

		if foundPodKey == queryPodKey {
			return foundPod
		}
	}

	return nil
}

type TestPlugin struct {
	name string
}

var (
	_ framework.ScorePlugin  = &TestPlugin{}
	_ framework.FilterPlugin = &TestPlugin{}
)

func (t *TestPlugin) Name() string {
	return t.name
}

func (t *TestPlugin) Score(ctx context.Context, state *framework.CycleState, p *v1.Pod, nodeName string) (int64, *framework.Status) {
	return 1, nil
}

func (t *TestPlugin) ScoreExtensions() framework.ScoreExtensions {
	return nil
}

func (t *TestPlugin) Filter(ctx context.Context, state *framework.CycleState, pod *v1.Pod, nodeInfo framework.NodeInfo) *framework.Status {
	return nil
}
