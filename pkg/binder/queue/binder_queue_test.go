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
	"time"

	"github.com/stretchr/testify/assert"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/kubewharf/godel-scheduler/pkg/binder/cache"
	commoncache "github.com/kubewharf/godel-scheduler/pkg/common/cache"
	framework "github.com/kubewharf/godel-scheduler/pkg/framework/api"
	"github.com/kubewharf/godel-scheduler/pkg/plugins/unitqueuesort"
	"github.com/kubewharf/godel-scheduler/pkg/util/pod"
)

var (
	lowPriority, highPriority = int32(0), int32(1000)
	mediumPriority            = (lowPriority + highPriority) / 2
	activeQPod                = v1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "hpp",
			Namespace: "ns1",
			UID:       "hppns1",
			Annotations: map[string]string{
				pod.AssumedNodeAnnotationKey: "node-1",
			},
		},
		Spec: v1.PodSpec{
			Priority: &highPriority,
		},
	}
)

var activeQPodUpdate = v1.Pod{
	ObjectMeta: metav1.ObjectMeta{
		Name:      "hpp",
		Namespace: "ns1",
		UID:       "hppns1",
		Annotations: map[string]string{
			pod.AssumedNodeAnnotationKey: "node-2",
		},
	},
	Spec: v1.PodSpec{
		Priority: &highPriority,
	},
}

var activeQPodLowPriority = v1.Pod{
	ObjectMeta: metav1.ObjectMeta{
		Name:      "lpp",
		Namespace: "ns1",
		UID:       "lppns1",
		Annotations: map[string]string{
			pod.AssumedNodeAnnotationKey: "node-2",
		},
	},
	Spec: v1.PodSpec{
		Priority: &lowPriority,
	},
}

var preemptionQPod = v1.Pod{
	ObjectMeta: metav1.ObjectMeta{
		Name:      "phpp",
		Namespace: "ns1",
		UID:       "phppns1",
		Annotations: map[string]string{
			pod.NominatedNodeAnnotationKey: "node-1",
		},
	},
	Spec: v1.PodSpec{
		Priority: &lowPriority,
	},
}

var crossNodeQPod = v1.Pod{
	ObjectMeta: metav1.ObjectMeta{
		Name:      "crnhpp",
		Namespace: "ns1",
		UID:       "crnhppns1",
		Annotations: map[string]string{
			pod.AssumedNodeAnnotationKey:      "node-1",
			pod.AssumedCrossNodeAnnotationKey: "true",
		},
	},
	Spec: v1.PodSpec{
		Priority: &mediumPriority,
	},
}

var cacheHandler = commoncache.MakeCacheHandlerWrapper().
	Period(10 * time.Second).PodAssumedTTL(30 * time.Second).StopCh(make(chan struct{})).
	ComponentName("godel-binder").Obj()

func newDefaultUnitQueueSort() framework.UnitLessFunc {
	sort := &unitqueuesort.DefaultUnitQueueSort{}
	return sort.Less
}

func TestPriorityQueue_Add(t *testing.T) {
	binderCache := cache.New(cacheHandler)
	q := NewPriorityQueue(newDefaultUnitQueueSort(), nil, nil, binderCache)
	if err := q.Add(&activeQPod); err != nil {
		t.Errorf("add failed: %v", err)
	}
	assert.Equal(t, 1, q.readyUnitQ.Len())
	assert.Equal(t, 0, q.unitBackoffQ.Len())

	assert.Equal(t, 1, len(q.PendingPods()))

	if err := q.Update(&activeQPod, &activeQPodUpdate); err != nil {
		t.Errorf("update failed: %v", err)
	}
	assert.Equal(t, 1, q.readyUnitQ.Len())

	if err := q.Add(&activeQPodLowPriority); err != nil {
		t.Errorf("add failed: %v", err)
	}
	assert.Equal(t, 2, q.readyUnitQ.Len())

	if err := q.Delete(&activeQPodLowPriority); err != nil {
		t.Errorf("delete failed: %v", err)
	}
	assert.Equal(t, 1, q.readyUnitQ.Len())

	unit, err := q.Pop()
	pod := unit.GetPods()[0]
	assert.NoError(t, err)
	assert.Equal(t, activeQPodUpdate.Name, pod.Pod.Name)
	assert.Equal(t, 0, q.waitingUnitQ.Len())
}

func TestMoveToReadyQueue(t *testing.T) {
	binderCache := cache.New(cacheHandler)
	q := NewPriorityQueue(newDefaultUnitQueueSort(), nil, nil, binderCache)
	podInfo := q.newQueuedPodInfo(&preemptionQPod)
	podInfo.Attempts = 2
	q.AddUnitPreemptor(&framework.QueuedUnitInfo{
		ScheduleUnit: &framework.SinglePodUnit{Pod: podInfo},
	})
	assert.Equal(t, 1, q.unitBackoffQ.Len())

	time.Sleep(2 * time.Second)

	q.moveToReadyQueue()
	assert.Equal(t, 0, q.unitBackoffQ.Len())
	assert.Equal(t, 1, q.readyUnitQ.Len())
	assert.Equal(t, 1, len(q.PendingPods()))
}
