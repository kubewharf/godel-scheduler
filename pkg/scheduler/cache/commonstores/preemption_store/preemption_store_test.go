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

package preemptionstore

import (
	"fmt"
	"testing"
	"time"

	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/sets"

	commoncache "github.com/kubewharf/godel-scheduler/pkg/common/cache"
	framework "github.com/kubewharf/godel-scheduler/pkg/framework/api"
	testing_helper "github.com/kubewharf/godel-scheduler/pkg/testing-helper"
	"github.com/kubewharf/godel-scheduler/pkg/util/generationstore"
	podutil "github.com/kubewharf/godel-scheduler/pkg/util/pod"
)

func makeGenerationPreemptionDetailForNode(data map[string]sets.String) *PreemptionDetailForNode {
	store := generationstore.NewListStore()
	for key := range data {
		obj := framework.NewGenerationStringSet()
		for v := range data[key] {
			obj.Insert(v)
		}
		store.Set(key, obj)
	}
	return &PreemptionDetailForNode{
		VictimToPreemptors: store,
	}
}

func makeGenerationPreemptionDetails(data map[string]map[string]sets.String) *PreemptionDetails {
	store := generationstore.NewListStore()
	for key := range data {
		obj := makeGenerationPreemptionDetailForNode(data[key])
		store.Set(key, obj)
	}
	return &PreemptionDetails{
		NodeToVictims: store,
	}
}

func TestAddPreemptItems(t *testing.T) {
	tests := []struct {
		name                      string
		pod                       *v1.Pod
		expectedPreemptionDetails *PreemptionDetails
	}{
		{
			name: "add pod with no victims",
			pod: testing_helper.MakePod().Namespace("p").Name("p").UID("p").
				Annotation(podutil.PodStateAnnotationKey, string(podutil.PodAssumed)).Obj(),
			expectedPreemptionDetails: makeGenerationPreemptionDetails(nil),
		},
		{
			name: "add pod with victims",
			pod: testing_helper.MakePod().Namespace("p").Name("p").UID("p").
				Annotation(podutil.PodStateAnnotationKey, string(podutil.PodAssumed)).
				Annotation(podutil.NominatedNodeAnnotationKey, "{\"node\":\"n\",\"victims\":[{\"name\":\"p1\",\"namespace\":\"p1\",\"uid\":\"p1\"}, {\"name\":\"p2\",\"namespace\":\"p2\",\"uid\":\"p2\"}]}").Obj(),
			expectedPreemptionDetails: makeGenerationPreemptionDetails(
				map[string]map[string]sets.String{
					"n": {
						"p1/p1/p1": sets.NewString("p/p/p"),
						"p2/p2/p2": sets.NewString("p/p/p"),
					},
				},
			),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cache := NewCache(commoncache.MakeCacheHandlerWrapper().EnableStore(string(Name)).Obj())
			cache.AssumePod(framework.MakeCachePodInfoWrapper().Pod(tt.pod).Obj())
			if !EqualPreemptionDetails(tt.expectedPreemptionDetails, cache.(*PreemptionStore).store) {
				t.Errorf("The nodeInfoList is incorrect. Expected %v , got %v", tt.expectedPreemptionDetails, cache.(*PreemptionStore).store)
			}
		})
	}
}

func TestRemovePreemptItems(t *testing.T) {
	tests := []struct {
		name                   string
		pod                    *v1.Pod
		originPreemptDetails   *PreemptionDetails
		expectedPreemptDetails *PreemptionDetails
	}{
		{
			name: "remove pod which is a victim",
			pod: testing_helper.MakePod().Namespace("p").Name("p").UID("p").
				Annotation(podutil.PodStateAnnotationKey, string(podutil.PodAssumed)).
				Annotation(podutil.SchedulerAnnotationKey, "x").Obj(),
			originPreemptDetails: makeGenerationPreemptionDetails(
				map[string]map[string]sets.String{
					"n": {
						"p/p/p": sets.NewString("p1/p1/p1"),
					},
				},
			),
			expectedPreemptDetails: makeGenerationPreemptionDetails(
				map[string]map[string]sets.String{
					"n": {
						"p/p/p": sets.NewString("p1/p1/p1"),
					},
				},
			),
		},
		{
			name: "remove pod with victims",
			pod: testing_helper.MakePod().Namespace("p").Name("p").UID("p").
				Annotation(podutil.PodStateAnnotationKey, string(podutil.PodAssumed)).
				Annotation(podutil.SchedulerAnnotationKey, "x").
				Annotation(podutil.NominatedNodeAnnotationKey, "{\"node\":\"n\",\"victims\":[{\"name\":\"p1\",\"namespace\":\"p1\",\"uid\":\"p1\"}, {\"name\":\"p2\",\"namespace\":\"p2\",\"uid\":\"p2\"}]}").Obj(),
			originPreemptDetails: makeGenerationPreemptionDetails(
				map[string]map[string]sets.String{
					"n": {
						"p1/p1/p1": sets.NewString("p/p/p"),
						"p2/p2/p2": sets.NewString("p/p/p"),
					},
				},
			),
			expectedPreemptDetails: makeGenerationPreemptionDetails(nil),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cache := NewCache(commoncache.MakeCacheHandlerWrapper().EnableStore(string(Name)).Obj())
			cache.(*PreemptionStore).store = tt.originPreemptDetails
			cache.DeletePod(tt.pod)
			if !EqualPreemptionDetails(tt.expectedPreemptDetails, cache.(*PreemptionStore).store) {
				t.Errorf("The nodeInfoList is incorrect. Expected %v , got %v", tt.expectedPreemptDetails, cache.(*PreemptionStore).store)
			}
		})
	}
}

func EqualPreemptionDetails(p1, p2 *PreemptionDetails) bool {
	if (p1 == nil) != (p2 == nil) {
		return false
	}
	if p1 == nil && p2 == nil {
		return true
	}
	return EqualNodeToVictims(p1.NodeToVictims, p2.NodeToVictims)
}

func EqualNodeToVictims(i1, i2 generationstore.Store) bool {
	if (i1 == nil) != (i2 == nil) {
		return false
	}
	if i1 != nil && i2 != nil {
		if i1.Len() != i2.Len() {
			return false
		}
		if i1.ConditionRange(func(key string, obj generationstore.StoredObj) bool {
			o1, o2 := obj, i2.Get(key)
			if (o1 == nil) != (o2 == nil) {
				return false
			}
			if o1 != nil && o2 != nil {
				o1Item := o1.(*PreemptionDetailForNode)
				o2Item := o2.(*PreemptionDetailForNode)
				if !EqualVictimToPreemptors(o1Item.VictimToPreemptors, o2Item.VictimToPreemptors) {
					return false
				}
			}
			return true
		}) {
			return false
		}
	}
	return true
}

func EqualVictimToPreemptors(i1, i2 generationstore.Store) bool {
	if (i1 == nil) != (i2 == nil) {
		return false
	}
	if i1 != nil && i2 != nil {
		if i1.Len() != i2.Len() {
			return false
		}
		if i1.ConditionRange(func(key string, obj generationstore.StoredObj) bool {
			o1, o2 := obj, i2.Get(key)
			if (o1 == nil) != (o2 == nil) {
				return false
			}
			if o1 != nil && o2 != nil {
				if !o1.(framework.GenerationStringSet).Equal(o2.(framework.GenerationStringSet)) {
					return false
				}
			}
			return true
		}) {
			return false
		}
	}
	return true
}

func TestUpdatePreemptInfoSnapshot(t *testing.T) {
	foo1 := testing_helper.MakePod().Namespace("foo1").Name("foo1").UID("foo1").Node("n1").
		Annotation(podutil.NominatedNodeAnnotationKey, "{\"node\":\"n1\",\"victims\":[{\"name\":\"p1\",\"namespace\":\"p1\",\"uid\":\"p1\"},{\"name\":\"p2\",\"namespace\":\"p2\",\"uid\":\"p2\"}]}").Obj()
	foo2 := testing_helper.MakePod().Namespace("foo2").Name("foo2").UID("foo2").Node("n1").
		Annotation(podutil.NominatedNodeAnnotationKey, "{\"node\":\"n1\",\"victims\":[{\"name\":\"p2\",\"namespace\":\"p2\",\"uid\":\"p2\"}]}").Obj()
	foo3 := testing_helper.MakePod().Namespace("foo3").Name("foo3").UID("foo3").Node("n2").
		Annotation(podutil.NominatedNodeAnnotationKey, "{\"node\":\"n2\",\"victims\":[{\"name\":\"p3\",\"namespace\":\"p3\",\"uid\":\"p3\"}]}").Obj()

	podLister := testing_helper.NewFakePodLister(nil)
	handler := commoncache.MakeCacheHandlerWrapper().PodLister(podLister).EnableStore(string(Name)).Obj()

	cache := NewCache(handler)
	snapshot := NewSnapshot(handler)

	// cache add foo1/foo3
	cache.AddPod(foo1)
	cache.AddPod(foo3)
	cache.UpdateSnapshot(snapshot)
	expected := makeGenerationPreemptionDetails(
		map[string]map[string]sets.String{
			"n1": {
				"p1/p1/p1": sets.NewString("foo1/foo1/foo1"),
				"p2/p2/p2": sets.NewString("foo1/foo1/foo1"),
			},
			"n2": {
				"p3/p3/p3": sets.NewString("foo3/foo3/foo3"),
			},
		},
	)
	if !EqualPreemptionDetails(expected, snapshot.(*PreemptionStore).store) {
		t.Errorf("expected: %v, but got: %v", expected, snapshot.(*PreemptionStore).store)
	}

	// snapshot add foo2
	snapshot.AddPod(foo2)
	cache.UpdateSnapshot(snapshot)
	expected = makeGenerationPreemptionDetails(
		map[string]map[string]sets.String{
			"n1": {
				"p1/p1/p1": sets.NewString("foo1/foo1/foo1"),
				"p2/p2/p2": sets.NewString("foo1/foo1/foo1"),
			},
			"n2": {
				"p3/p3/p3": sets.NewString("foo3/foo3/foo3"),
			},
		},
	)
	if !EqualPreemptionDetails(expected, snapshot.(*PreemptionStore).store) {
		t.Errorf("expected: %v, but got: %v", expected, snapshot.(*PreemptionStore).store)
	}

	// cache add foo2
	cache.AddPod(foo2)
	cache.UpdateSnapshot(snapshot)
	expected = makeGenerationPreemptionDetails(
		map[string]map[string]sets.String{
			"n1": {
				"p1/p1/p1": sets.NewString("foo1/foo1/foo1"),
				"p2/p2/p2": sets.NewString("foo1/foo1/foo1", "foo2/foo2/foo2"),
			},
			"n2": {
				"p3/p3/p3": sets.NewString("foo3/foo3/foo3"),
			},
		},
	)
	if !EqualPreemptionDetails(expected, snapshot.(*PreemptionStore).store) {
		t.Errorf("expected: %v, but got: %v", expected, snapshot.(*PreemptionStore).store)
	}

	// snapshot remove foo2
	snapshot.DeletePod(foo2)
	cache.UpdateSnapshot(snapshot)
	expected = makeGenerationPreemptionDetails(
		map[string]map[string]sets.String{
			"n1": {
				"p1/p1/p1": sets.NewString("foo1/foo1/foo1"),
				"p2/p2/p2": sets.NewString("foo1/foo1/foo1", "foo2/foo2/foo2"),
			},
			"n2": {
				"p3/p3/p3": sets.NewString("foo3/foo3/foo3"),
			},
		},
	)
	if !EqualPreemptionDetails(expected, snapshot.(*PreemptionStore).store) {
		t.Errorf("expected: %v, but got: %v", expected, snapshot.(*PreemptionStore).store)
	}

	// cache remove foo1
	cache.DeletePod(foo1)
	cache.UpdateSnapshot(snapshot)
	expected = makeGenerationPreemptionDetails(
		map[string]map[string]sets.String{
			"n1": {
				"p2/p2/p2": sets.NewString("foo2/foo2/foo2"),
			},
			"n2": {
				"p3/p3/p3": sets.NewString("foo3/foo3/foo3"),
			},
		},
	)
	if !EqualPreemptionDetails(expected, snapshot.(*PreemptionStore).store) {
		t.Errorf("expected: %v, but got: %v", expected, snapshot.(*PreemptionStore).store)
	}

	// snapshot remove foo1
	snapshot.DeletePod(foo1)
	cache.UpdateSnapshot(snapshot)
	expected = makeGenerationPreemptionDetails(
		map[string]map[string]sets.String{
			"n1": {
				"p2/p2/p2": sets.NewString("foo2/foo2/foo2"),
			},
			"n2": {
				"p3/p3/p3": sets.NewString("foo3/foo3/foo3"),
			},
		},
	)
	if !EqualPreemptionDetails(expected, snapshot.(*PreemptionStore).store) {
		t.Errorf("expected: %v, but got: %v", expected, snapshot.(*PreemptionStore).store)
	}

	// cache remove foo2
	cache.DeletePod(foo2)
	cache.UpdateSnapshot(snapshot)
	expected = makeGenerationPreemptionDetails(
		map[string]map[string]sets.String{
			"n2": {
				"p3/p3/p3": sets.NewString("foo3/foo3/foo3"),
			},
		},
	)
	if !EqualPreemptionDetails(expected, snapshot.(*PreemptionStore).store) {
		t.Errorf("expected: %v, but got: %v", expected, snapshot.(*PreemptionStore).store)
	}

	// cache remove foo3
	cache.DeletePod(foo3)
	cache.UpdateSnapshot(snapshot)
	expected = makeGenerationPreemptionDetails(
		map[string]map[string]sets.String{},
	)
	if !EqualPreemptionDetails(expected, snapshot.(*PreemptionStore).store) {
		t.Errorf("expected: %v, but got: %v", expected, snapshot.(*PreemptionStore).store)
	}
}

func BenchmarkUpdatePreemptInfoSnapshot(b *testing.B) {
	podLister := testing_helper.NewFakePodLister(nil)
	handler := commoncache.MakeCacheHandlerWrapper().PodLister(podLister).EnableStore(string(Name)).Obj()

	cache := NewCache(handler)
	nodeCount := 20000
	podCountInEachNode := 5
	podsInNodes := make([][]*v1.Pod, nodeCount*podCountInEachNode)
	for i := 0; i < nodeCount; i++ {
		podsInNodes[i] = make([]*v1.Pod, podCountInEachNode)
		nodeName := fmt.Sprintf("%d", i)
		for j := 0; j < podCountInEachNode; j++ {
			preemptor := fmt.Sprintf("preemptor-%d-%d", i, j)
			victim := fmt.Sprintf("victim-%d-%d", i, j)
			pod := testing_helper.MakePod().Namespace(preemptor).Name(preemptor).UID(preemptor).Node(nodeName).
				Annotation(podutil.NominatedNodeAnnotationKey, fmt.Sprintf("{\"node\":\"%s\",\"victims\":[{\"name\":\"%s\",\"namespace\":\"%s\",\"uid\":\"%s\"}]}", nodeName, victim, victim, victim)).Obj()
			cache.AddPod(pod)
			podsInNodes[i][j] = pod
		}
	}

	type testcase struct {
		name string
		node int
	}

	testcases := []testcase{
		{name: "20k node, 100k pod, remove 100 node, remove 500 pod", node: 100},
		{name: "20k node, 100k pod, remove 500 node, remove 2500 pod", node: 500},
		{name: "20k node, 100k pod, remove 1000 node, remove 5000 pod", node: 1000},
		{name: "20k node, 100k pod, remove 5000 node, remove 25000 pod", node: 5000},
	}

	for _, tc := range testcases {
		var totalCost float64
		for i := 0; i < 100; i++ {
			snapshot := NewSnapshot(handler)
			cache.UpdateSnapshot(snapshot)

			for i := 0; i < tc.node; i++ {
				for j := 0; j < podCountInEachNode; j++ {
					cache.DeletePod(podsInNodes[i][j])
				}
			}

			start := time.Now()
			cache.UpdateSnapshot(snapshot)
			cost := float64(time.Since(start).Milliseconds())
			totalCost += cost
		}
		cost := totalCost / 100
		fmt.Printf("testcase[%v] avg cost: %v\n", tc.name, cost)
	}
}

func TestCleanUpResidualPreemptionItems(t *testing.T) {
	existingPods := []*v1.Pod{
		testing_helper.MakePod().Namespace("foo1").Name("foo1").UID("foo1").Node("n1").
			Annotation(podutil.NominatedNodeAnnotationKey, "{\"node\":\"n1\",\"victims\":[{\"name\":\"p1\",\"namespace\":\"p1\",\"uid\":\"p1\"}]}").Obj(),
		testing_helper.MakePod().Namespace("foo2").Name("foo2").UID("foo2").Node("n1").
			Annotation(podutil.NominatedNodeAnnotationKey, "{\"node\":\"n1\",\"victims\":[{\"name\":\"p2\",\"namespace\":\"p2\",\"uid\":\"p2\"}]}").Obj(),
		testing_helper.MakePod().Namespace("foo3").Name("foo3").UID("foo3").Node("n2").
			Annotation(podutil.NominatedNodeAnnotationKey, "{\"node\":\"n2\",\"victims\":[{\"name\":\"p3\",\"namespace\":\"p3\",\"uid\":\"p3\"}]}").Obj(),
	}
	deletedPods := []*v1.Pod{
		testing_helper.MakePod().Namespace("foo4").Name("foo4").UID("foo4").Node("n1").
			Annotation(podutil.NominatedNodeAnnotationKey, "{\"node\":\"n1\",\"victims\":[{\"name\":\"p1\",\"namespace\":\"p1\",\"uid\":\"p1\"}]}").Obj(),
		testing_helper.MakePod().Namespace("foo5").Name("foo5").UID("foo5").Node("n3").
			Annotation(podutil.NominatedNodeAnnotationKey, "{\"node\":\"n3\",\"victims\":[{\"name\":\"p4\",\"namespace\":\"p4\",\"uid\":\"p4\"},{\"name\":\"p5\",\"namespace\":\"p5\",\"uid\":\"p5\"}]}").Obj(),
	}
	podLister := testing_helper.NewFakePodLister(existingPods)
	cache := NewCache(commoncache.MakeCacheHandlerWrapper().PodLister(podLister).EnableStore(string(Name)).Obj())
	for _, pod := range append(existingPods, deletedPods...) {
		cache.AddPod(pod)
	}
	expectedPreemptionDetails := makeGenerationPreemptionDetails(
		map[string]map[string]sets.String{
			"n1": {
				"p1/p1/p1": sets.NewString("foo1/foo1/foo1", "foo4/foo4/foo4"),
				"p2/p2/p2": sets.NewString("foo2/foo2/foo2"),
			},
			"n2": {
				"p3/p3/p3": sets.NewString("foo3/foo3/foo3"),
			},
			"n3": {
				"p4/p4/p4": sets.NewString("foo5/foo5/foo5"),
				"p5/p5/p5": sets.NewString("foo5/foo5/foo5"),
			},
		},
	)
	if !EqualPreemptionDetails(expectedPreemptionDetails, cache.(*PreemptionStore).store) {
		t.Errorf("expected: %v, but got %v", expectedPreemptionDetails, cache.(*PreemptionStore).store)
	}

	cache.(*PreemptionStore).store.CleanUpResidualPreemptionItems(podLister)
	expectedPreemptionDetails = makeGenerationPreemptionDetails(
		map[string]map[string]sets.String{
			"n1": {
				"p1/p1/p1": sets.NewString("foo1/foo1/foo1"),
				"p2/p2/p2": sets.NewString("foo2/foo2/foo2"),
			},
			"n2": {
				"p3/p3/p3": sets.NewString("foo3/foo3/foo3"),
			},
		},
	)
	if !EqualPreemptionDetails(expectedPreemptionDetails, cache.(*PreemptionStore).store) {
		t.Errorf("expected: %v, but got %v", expectedPreemptionDetails, cache.(*PreemptionStore).store)
	}
}
