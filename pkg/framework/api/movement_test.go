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

package api

import (
	"encoding/json"
	"errors"
	"reflect"
	"testing"

	schedulingv1a1 "github.com/kubewharf/godel-scheduler-api/pkg/apis/scheduling/v1alpha1"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/sets"

	"github.com/kubewharf/godel-scheduler/pkg/util/generationstore"
	podutil "github.com/kubewharf/godel-scheduler/pkg/util/pod"
)

func newTaskInfo(namespace, name, uid string) *schedulingv1a1.TaskInfo {
	return &schedulingv1a1.TaskInfo{
		Namespace: namespace,
		Name:      name,
		UID:       types.UID(uid),
	}
}

func newOwnerInfo(ownerType, namespace, name, uid string) *schedulingv1a1.OwnerInfo {
	return &schedulingv1a1.OwnerInfo{
		Type:      ownerType,
		Namespace: namespace,
		Name:      name,
		UID:       types.UID(uid),
	}
}

func makePodWithOwner(namespace, name, uid, node string, annotations map[string]string, owner metav1.OwnerReference) *v1.Pod {
	return &v1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Namespace:   namespace,
			Name:        name,
			UID:         types.UID(uid),
			Annotations: annotations,
			OwnerReferences: []metav1.OwnerReference{
				owner,
			},
		},
		Spec: v1.PodSpec{
			NodeName: node,
		},
	}
}

// ---------------- test MovementInfo struct ----------------

func extractMovement(infos map[string][]*MovementDetailOnNode) map[string]string {
	ret := make(map[string]string)
	for k, vs := range infos {
		for _, v := range vs {
			ret[k] = v.MovementName
		}
	}
	return ret
}

func TestSnapshotMovementInfo(t *testing.T) {
	mi := NewSnapshotMovementInfo()
	assumedPod1 := makePodWithOwner("default", "p1", "p1", "",
		map[string]string{podutil.MovementNameKey: "m1", podutil.AssumedNodeAnnotationKey: "n1"},
		metav1.OwnerReference{Kind: podutil.ReplicaSetKind, Name: "rs1", UID: "rs1"})
	mi.AddAssumedPod(assumedPod1, "n1")
	movement := &schedulingv1a1.Movement{
		ObjectMeta: metav1.ObjectMeta{
			Name: "m1",
		},
		Spec: schedulingv1a1.MovementSpec{
			DeletedTasks: []*schedulingv1a1.TaskInfo{
				newTaskInfo("default", "p11", "p11"),
			},
		},
		Status: schedulingv1a1.MovementStatus{
			Owners: []*schedulingv1a1.Owner{
				{
					Owner: newOwnerInfo("ReplicaSet", "default", "rs1", "rs1"),
					RecommendedNodes: []*schedulingv1a1.RecommendedNode{
						{
							Node:            "n1",
							DesiredPodCount: 2,
							ActualPods: []*schedulingv1a1.TaskInfo{
								newTaskInfo("default", "p1", "p1"),
							},
						},
					},
				},
			},
		},
	}
	gotSuggestedInfo := extractMovement(mi.GetSuggestedMovementAndNodes("ReplicaSet/default/rs1/rs1"))
	if len(gotSuggestedInfo) > 0 {
		t.Errorf("expected suggested info: nil, but got: %v", gotSuggestedInfo)
	}
	mi.GetDeletedPodsFromMovement("m1")
	mi.AddMovement(movement)
	gotSuggestedInfo = extractMovement(mi.GetSuggestedMovementAndNodes("ReplicaSet/default/rs1/rs1"))
	expectedSuggestedInfo := map[string]string{"n1": "m1"}
	if !reflect.DeepEqual(expectedSuggestedInfo, gotSuggestedInfo) {
		t.Errorf("expected suggested info: %v, but got: %v", expectedSuggestedInfo, gotSuggestedInfo)
	}

	gotSuggestionTimes := mi.GetAvailableSuggestionTimesForNodes("ReplicaSet/default/rs1/rs1")
	expectedSuggestionsTimes := map[string]int64{"n1": 1}
	if !reflect.DeepEqual(expectedSuggestionsTimes, gotSuggestionTimes) {
		t.Errorf("expected suggestion times: %v, got suggestion times: %v", expectedSuggestionsTimes, gotSuggestionTimes)
	}

	assumedPod2 := makePodWithOwner("default", "p2", "p2", "n1",
		map[string]string{podutil.MovementNameKey: "m1"},
		metav1.OwnerReference{Kind: podutil.ReplicaSetKind, Name: "rs1", UID: "rs1"})
	mi.AddAssumedPod(assumedPod2, "n1")
	gotSuggestedInfo = extractMovement(mi.GetSuggestedMovementAndNodes("ReplicaSet/default/rs1/rs1"))
	if len(gotSuggestedInfo) > 0 {
		t.Errorf("expected suggested info: nil, but got: %v", gotSuggestedInfo)
	}
	gotSuggestionTimes = mi.GetAvailableSuggestionTimesForNodes("ReplicaSet/default/rs1/rs1")
	expectedSuggestionsTimes = map[string]int64{}
	if !reflect.DeepEqual(expectedSuggestionsTimes, gotSuggestionTimes) {
		t.Errorf("expected suggestion times: %v, got suggestion times: %v", expectedSuggestionsTimes, gotSuggestionTimes)
	}
	gotDeletedPods := mi.GetDeletedPodsFromMovement("m1")
	expectedDeletedPods := sets.NewString("default/p11/p11")
	if !reflect.DeepEqual(expectedDeletedPods, gotDeletedPods) {
		t.Errorf("expected deleted pods: %v, but got: %v", expectedDeletedPods, gotDeletedPods)
	}
	mi.MovementStates.ConditionRange(func(movementName string, obj generationstore.StoredObj) bool {
		if obj.GetGeneration() != 0 {
			t.Errorf("expected generation: 0, but got: %d", obj.GetGeneration())
		}
		return true
	})
	mi.OwnersToMovements.ConditionRange(func(ownerKey string, obj generationstore.StoredObj) bool {
		if obj.GetGeneration() != 0 {
			t.Errorf("expected generation: 0, but got: %d", obj.GetGeneration())
		}
		return true
	})
	mi.RemoveMovement(movement)
	if mi.OwnersToMovements.Len() > 0 {
		t.Errorf("OwnersToMovements should be empty, but got %d", mi.OwnersToMovements.Len())
	}
	if mi.MovementStates.Len() != 1 {
		t.Errorf("MovementStates should have one item, but got %d", mi.MovementStates.Len())
	}
	mi.RemoveAssumedPod(assumedPod1, getNodeNameFromPod(assumedPod1))
	if mi.MovementStates.Len() != 1 {
		t.Errorf("MovementStates should have one item, but got %d", mi.MovementStates.Len())
	}
	mi.RemoveAssumedPod(assumedPod2, getNodeNameFromPod(assumedPod2))
	if mi.MovementStates.Len() > 0 {
		t.Errorf("MovementStates should be empty, but got %d", mi.MovementStates.Len())
	}
}

func TestCacheMovementInfo(t *testing.T) {
	mi := NewCacheMovementInfo()
	assumedPod1 := makePodWithOwner("default", "p1", "p1", "",
		map[string]string{podutil.MovementNameKey: "m1", podutil.AssumedNodeAnnotationKey: "n1"},
		metav1.OwnerReference{Kind: podutil.ReplicaSetKind, Name: "rs1", UID: "rs1"})
	// will change generation
	mi.AddAssumedPod(assumedPod1, "n1")
	msListerStore := mi.MovementStates.(generationstore.ListStore)
	if msListerStore.Len() != 1 {
		t.Errorf("expected MovementStates: 1, but got: %d", msListerStore.Len())
	}
	omListerStore := mi.OwnersToMovements.(generationstore.ListStore)
	if omListerStore.Len() > 0 {
		t.Errorf("OwnersToMovements should be empty, but got: %d", omListerStore.Len())
	}

	movement := &schedulingv1a1.Movement{
		ObjectMeta: metav1.ObjectMeta{
			Name: "m1",
		},
		Spec: schedulingv1a1.MovementSpec{
			DeletedTasks: []*schedulingv1a1.TaskInfo{
				newTaskInfo("default", "p11", "p11"),
			},
		},
		Status: schedulingv1a1.MovementStatus{
			Owners: []*schedulingv1a1.Owner{
				{
					Owner: newOwnerInfo("ReplicaSet", "default", "rs1", "rs1"),
					RecommendedNodes: []*schedulingv1a1.RecommendedNode{
						{
							Node:            "n1",
							DesiredPodCount: 2,
							ActualPods: []*schedulingv1a1.TaskInfo{
								newTaskInfo("default", "p1", "p1"),
							},
						},
					},
				},
			},
		},
	}
	gotSuggestedInfo := extractMovement(mi.GetSuggestedMovementAndNodes("ReplicaSet/default/rs1/rs1"))
	if len(gotSuggestedInfo) > 0 {
		t.Errorf("expected suggested info: nil, but got: %v", gotSuggestedInfo)
	}
	mi.GetDeletedPodsFromMovement("m1")
	// will change generation
	mi.AddMovement(movement)
	gotSuggestedInfo = extractMovement(mi.GetSuggestedMovementAndNodes("ReplicaSet/default/rs1/rs1"))
	expectedSuggestedInfo := map[string]string{"n1": "m1"}
	if !reflect.DeepEqual(expectedSuggestedInfo, gotSuggestedInfo) {
		t.Errorf("expected suggested info: %v, but got: %v", expectedSuggestedInfo, gotSuggestedInfo)
	}
	if msListerStore.Len() != 1 {
		t.Errorf("expected MovementStates: 1, but got: %d", msListerStore.Len())
	}
	if omListerStore.Len() != 1 {
		t.Errorf("expected OwnersToMovements: 1, but got: %d", omListerStore.Len())
	}
	if msListerStore.Front().GetGeneration() != 2 {
		t.Errorf("expected generation: 2, but got: %d", msListerStore.Front().GetGeneration())
	}
	if omListerStore.Front().GetGeneration() != 1 {
		t.Errorf("expected generation: 1, but got: %d", omListerStore.Front().GetGeneration())
	}

	gotSuggestionTimes := mi.GetAvailableSuggestionTimesForNodes("ReplicaSet/default/rs1/rs1")
	expectedSuggestionsTimes := map[string]int64{"n1": 1}
	if !reflect.DeepEqual(expectedSuggestionsTimes, gotSuggestionTimes) {
		t.Errorf("expected suggestion times: %v, got suggestion times: %v", expectedSuggestionsTimes, gotSuggestionTimes)
	}

	assumedPod2 := makePodWithOwner("default", "p2", "p2", "n1",
		map[string]string{podutil.MovementNameKey: "m1"},
		metav1.OwnerReference{Kind: podutil.ReplicaSetKind, Name: "rs1", UID: "rs1"})
	// will change generation
	mi.AddAssumedPod(assumedPod2, "n1")
	gotSuggestedInfo = extractMovement(mi.GetSuggestedMovementAndNodes("ReplicaSet/default/rs1/rs1"))
	if len(gotSuggestedInfo) > 0 {
		t.Errorf("expected suggested info: nil, but got: %v", gotSuggestedInfo)
	}
	gotSuggestionTimes = mi.GetAvailableSuggestionTimesForNodes("ReplicaSet/default/rs1/rs1")
	expectedSuggestionsTimes = map[string]int64{}
	if !reflect.DeepEqual(expectedSuggestionsTimes, gotSuggestionTimes) {
		t.Errorf("expected suggestion times: %v, got suggestion times: %v", expectedSuggestionsTimes, gotSuggestionTimes)
	}
	gotDeletedPods := mi.GetDeletedPodsFromMovement("m1")
	expectedDeletedPods := sets.NewString("default/p11/p11")
	if !reflect.DeepEqual(expectedDeletedPods, gotDeletedPods) {
		t.Errorf("expected deleted pods: %v, but got: %v", expectedDeletedPods, gotDeletedPods)
	}
	if msListerStore.Len() != 1 {
		t.Errorf("expected MovementStates: 1, but got: %d", msListerStore.Len())
	}
	if omListerStore.Len() != 1 {
		t.Errorf("expected OwnersToMovements: 1, but got: %d", omListerStore.Len())
	}
	if msListerStore.Front().GetGeneration() != 3 {
		t.Errorf("expected generation: 3, but got: %d", msListerStore.Front().GetGeneration())
	}
	if omListerStore.Front().GetGeneration() != 1 {
		t.Errorf("expected generation: 1, but got: %d", omListerStore.Front().GetGeneration())
	}

	mi.RemoveMovement(movement)
	if mi.OwnersToMovements.Len() > 0 {
		t.Errorf("OwnersToMovements should be empty, but got %d", mi.OwnersToMovements.Len())
	}
	if mi.MovementStates.Len() != 1 {
		t.Errorf("MovementStates should have one item, but got %d", mi.MovementStates.Len())
	}
	if msListerStore.Front().GetGeneration() != 4 {
		t.Errorf("expected generation: 4, but got: %d", msListerStore.Front().GetGeneration())
	}

	mi.RemoveAssumedPod(assumedPod1, getNodeNameFromPod(assumedPod1))
	if mi.MovementStates.Len() != 1 {
		t.Errorf("MovementStates should have one item, but got %d", mi.MovementStates.Len())
	}
	if msListerStore.Front().GetGeneration() != 5 {
		t.Errorf("expected generation: 5, but got: %d", msListerStore.Front().GetGeneration())
	}

	mi.RemoveAssumedPod(assumedPod2, getNodeNameFromPod(assumedPod2))
	if mi.MovementStates.Len() > 0 {
		t.Errorf("MovementStates should be empty, but got %d", mi.MovementStates.Len())
	}

	miNew := NewSnapshotMovementInfo()
	miNew.AddMovement(movement)
	miNew.AddAssumedPod(assumedPod1, "n1")
	mi.UpdateMovementInfo(miNew)
	if miNew.MovementStates.Len() > 0 {
		t.Errorf("expected MovementStates: 0, but got: %d", miNew.MovementStates.Len())
	}
	if miNew.OwnersToMovements.Len() > 0 {
		t.Errorf("expected OwnersToMovements: 0, but got: %d", miNew.OwnersToMovements.Len())
	}

	mi.AddMovement(movement)
	mi.AddAssumedPod(assumedPod1, "n1")
	mi.UpdateMovementInfo(miNew)
	if miNew.MovementStates.Len() > 1 {
		t.Errorf("expected MovementStates: 1, but got: %d", miNew.MovementStates.Len())
	}
	if miNew.OwnersToMovements.Len() > 1 {
		t.Errorf("expected OwnersToMovements: 1, but got: %d", miNew.OwnersToMovements.Len())
	}
	miNew.MovementStates.ConditionRange(func(movementName string, obj generationstore.StoredObj) bool {
		if obj.GetGeneration() != 7 {
			t.Errorf("expected generation: 7, but got: %d", obj.GetGeneration())
		}
		return true
	})
	miNew.OwnersToMovements.ConditionRange(func(ownerKey string, obj generationstore.StoredObj) bool {
		if obj.GetGeneration() != 2 {
			t.Errorf("expected generation: 2, but got: %d", obj.GetGeneration())
		}
		return true
	})
	msRawStore := miNew.MovementStates.(generationstore.RawStore)
	if msRawStore.GetGeneration() != 7 {
		t.Errorf("expected generation: 7, but got: %d", msRawStore.GetGeneration())
	}
	omRawStore := miNew.OwnersToMovements.(generationstore.RawStore)
	if omRawStore.GetGeneration() != 2 {
		t.Errorf("expected generation: 2, but got: %d", omRawStore.GetGeneration())
	}
}

// ---------------- test MovementState struct ----------------

func TestSnapshotMovementState(t *testing.T) {
	ms := NewSnapshotMovementState()
	movement := &schedulingv1a1.Movement{
		ObjectMeta: metav1.ObjectMeta{
			Name: "m1",
		},
		Status: schedulingv1a1.MovementStatus{
			Owners: []*schedulingv1a1.Owner{
				{
					Owner: newOwnerInfo("ReplicaSet", "default", "rs1", "rs1"),
					RecommendedNodes: []*schedulingv1a1.RecommendedNode{
						{
							Node:            "n1",
							DesiredPodCount: 2,
							ActualPods: []*schedulingv1a1.TaskInfo{
								newTaskInfo("default", "p1", "p1"),
							},
						},
					},
				},
			},
		},
	}
	// add movement
	ms.AddMovement(movement)
	// get nodes still have suggestion
	gotNodes := extractNodeList(ms.GetNodesStillHaveSuggestion("ReplicaSet/default/rs1/rs1"))
	expectedNodes := []string{"n1"}
	if !reflect.DeepEqual(expectedNodes, gotNodes) {
		t.Errorf("expected nodes: %v, got nodes: %v", expectedNodes, gotNodes)
	}
	// add assumed pod
	ms.AddAssumedPod("default/p2/p2", "ReplicaSet/default/rs1/rs1", "n1")
	deletedPods := []string{"default/p11/p11"}
	// set deleted pods
	ms.SetDeletedPods(deletedPods)
	gotDeletedPods := ms.GetDeletedPods()
	if !reflect.DeepEqual(sets.NewString(deletedPods...), gotDeletedPods) {
		t.Errorf("expected deleted pods: %v, but got: %v", deletedPods, gotDeletedPods)
	}
	// get nodes still have suggestion
	gotNodes = extractNodeList(ms.GetNodesStillHaveSuggestion("ReplicaSet/default/rs1/rs1"))
	expectedNodes = nil
	if !reflect.DeepEqual(expectedNodes, gotNodes) {
		t.Errorf("expected nodes: %v, got nodes: %v", expectedNodes, gotNodes)
	}
	// add assumed pods
	ms.AddAssumedPod("default/p3/p3", "ReplicaSet/default/rs1/rs1", "n1")
	// get nodes still have suggestion
	gotNodes = extractNodeList(ms.GetNodesStillHaveSuggestion("ReplicaSet/default/rs1/rs1"))
	expectedNodes = nil
	if !reflect.DeepEqual(expectedNodes, gotNodes) {
		t.Errorf("expected nodes: %v, got nodes: %v", expectedNodes, gotNodes)
	}
	// get node suggestions
	gotNodeSuggestions := ms.GetAvailableNodeSuggestions("ReplicaSet/default/rs1/rs1")
	expectedNodeSuggestions := map[string]int64{}
	if !reflect.DeepEqual(expectedNodeSuggestions, gotNodeSuggestions) {
		t.Errorf("expected node suggestions: %v, but got: %v", expectedNodeSuggestions, gotNodeSuggestions)
	}
	// get node suggestions
	gotNodeSuggestions = ms.GetAvailableNodeSuggestions("n2")
	expectedNodeSuggestions = nil
	if !reflect.DeepEqual(expectedNodeSuggestions, gotNodeSuggestions) {
		t.Errorf("expected node suggestions: %v, but got: %v", expectedNodeSuggestions, gotNodeSuggestions)
	}
	// get owner list
	expectedOwnerList := []string{"ReplicaSet/default/rs1/rs1"}
	gotOwnerList := ms.GetOwnerList()
	if !reflect.DeepEqual(expectedOwnerList, gotOwnerList) {
		t.Errorf("expected node suggestions: %v, but got: %v", expectedOwnerList, gotOwnerList)
	}
	// remove assumed pods
	ms.RemoveAssumedPod("default/p2/p2", "ReplicaSet/default/rs1/rs1", "n1")
	gotNodes = extractNodeList(ms.GetNodesStillHaveSuggestion("ReplicaSet/default/rs1/rs1"))
	expectedNodes = nil
	if !reflect.DeepEqual(expectedNodes, gotNodes) {
		t.Errorf("expected nodes: %v, got nodes: %v", expectedNodes, gotNodes)
	}
	isEmpty := ms.DeleteMovement()
	if isEmpty {
		t.Error("movement state is not empty")
	}
	ms.RemoveAssumedPod("default/p3/p3", "ReplicaSet/default/rs1/rs1", "n1")
	isEmpty = ms.DeleteMovement()
	if !isEmpty {
		t.Error("movement state is empty")
	}
	if ms.GetOwnerMovements().Len() > 0 {
		t.Error("movement state is empty")
	}
}

func TestCacheMovementState(t *testing.T) {
	ms := NewCacheMovementState()
	movement := &schedulingv1a1.Movement{
		ObjectMeta: metav1.ObjectMeta{
			Name: "m1",
		},
		Status: schedulingv1a1.MovementStatus{
			Owners: []*schedulingv1a1.Owner{
				{
					Owner: newOwnerInfo("ReplicaSet", "default", "rs1", "rs1"),
					RecommendedNodes: []*schedulingv1a1.RecommendedNode{
						{
							Node:            "n1",
							DesiredPodCount: 2,
							ActualPods: []*schedulingv1a1.TaskInfo{
								newTaskInfo("default", "p1", "p1"),
							},
						},
					},
				},
			},
		},
	}
	// add movement
	ms.AddMovement(movement)
	// get nodes still have suggestion
	gotNodes := extractNodeList(ms.GetNodesStillHaveSuggestion("ReplicaSet/default/rs1/rs1"))
	expectedNodes := []string{"n1"}
	if !reflect.DeepEqual(expectedNodes, gotNodes) {
		t.Errorf("expected nodes: %v, got nodes: %v", expectedNodes, gotNodes)
	}
	store := ms.GetOwnerMovements()
	listStore := store.(generationstore.ListStore)
	if listStore.Front().GetGeneration() != 1 {
		t.Errorf("expected generation: 1, but got: %d", listStore.Front().GetGeneration())
	}
	// add assumed pod
	ms.AddAssumedPod("default/p2/p2", "ReplicaSet/default/rs1/rs1", "n1")
	deletedPods := []string{"default/p11/p11"}
	if listStore.Front().GetGeneration() != 2 {
		t.Errorf("expected generation: 2, but got: %d", listStore.Front().GetGeneration())
	}
	// set deleted pods
	ms.SetDeletedPods(deletedPods)
	gotDeletedPods := ms.GetDeletedPods()
	if !reflect.DeepEqual(sets.NewString(deletedPods...), gotDeletedPods) {
		t.Errorf("expected deleted pods: %v, but got: %v", deletedPods, gotDeletedPods)
	}
	if listStore.Front().GetGeneration() != 2 {
		t.Errorf("expected generation: 2, but got: %d", listStore.Front().GetGeneration())
	}
	// get nodes still have suggestion
	gotNodes = extractNodeList(ms.GetNodesStillHaveSuggestion("ReplicaSet/default/rs1/rs1"))
	expectedNodes = nil
	if !reflect.DeepEqual(expectedNodes, gotNodes) {
		t.Errorf("expected nodes: %v, got nodes: %v", expectedNodes, gotNodes)
	}
	// add assumed pods
	ms.AddAssumedPod("default/p3/p3", "ReplicaSet/default/rs1/rs1", "n1")
	// get nodes still have suggestion
	gotNodes = extractNodeList(ms.GetNodesStillHaveSuggestion("ReplicaSet/default/rs1/rs1"))
	expectedNodes = nil
	if !reflect.DeepEqual(expectedNodes, gotNodes) {
		t.Errorf("expected nodes: %v, got nodes: %v", expectedNodes, gotNodes)
	}
	// get node suggestions
	gotNodeSuggestions := ms.GetAvailableNodeSuggestions("ReplicaSet/default/rs1/rs1")
	expectedNodeSuggestions := map[string]int64{}
	if !reflect.DeepEqual(expectedNodeSuggestions, gotNodeSuggestions) {
		t.Errorf("expected node suggestions: %v, but got: %v", expectedNodeSuggestions, gotNodeSuggestions)
	}
	// get node suggestions
	gotNodeSuggestions = ms.GetAvailableNodeSuggestions("n2")
	expectedNodeSuggestions = nil
	if !reflect.DeepEqual(expectedNodeSuggestions, gotNodeSuggestions) {
		t.Errorf("expected node suggestions: %v, but got: %v", expectedNodeSuggestions, gotNodeSuggestions)
	}
	// get owner list
	expectedOwnerList := []string{"ReplicaSet/default/rs1/rs1"}
	gotOwnerList := ms.GetOwnerList()
	if !reflect.DeepEqual(expectedOwnerList, gotOwnerList) {
		t.Errorf("expected node suggestions: %v, but got: %v", expectedOwnerList, gotOwnerList)
	}
	if listStore.Front().GetGeneration() != 3 {
		t.Errorf("expected generation: 3, but got: %d", listStore.Front().GetGeneration())
	}
	// remove assumed pods
	ms.RemoveAssumedPod("default/p2/p2", "ReplicaSet/default/rs1/rs1", "n1")
	gotNodes = extractNodeList(ms.GetNodesStillHaveSuggestion("ReplicaSet/default/rs1/rs1"))
	expectedNodes = nil
	if !reflect.DeepEqual(expectedNodes, gotNodes) {
		t.Errorf("expected nodes: %v, got nodes: %v", expectedNodes, gotNodes)
	}
	if listStore.Front().GetGeneration() != 4 {
		t.Errorf("expected generation: 4, but got: %d", listStore.Front().GetGeneration())
	}
	isEmpty := ms.DeleteMovement()
	if isEmpty {
		t.Error("movement state is not empty")
	}
	if listStore.Front().GetGeneration() != 5 {
		t.Errorf("expected generation: 5, but got: %d", listStore.Front().GetGeneration())
	}
	isEmpty = ms.RemoveAssumedPod("default/p3/p3", "ReplicaSet/default/rs1/rs1", "n1")
	if !isEmpty {
		t.Error("movement state is empty")
	}
	if store.Len() > 0 {
		t.Error("movement state is empty")
	}

	ms.AddMovement(movement)
	if listStore.Front().GetGeneration() != 7 {
		t.Errorf("expected generation: 7, but got: %d", listStore.Front().GetGeneration())
	}
	msNew := NewSnapshotMovementState()
	msNew.SetGeneration(1)
	ms.UpdateMovementState(msNew)
	if msNew.GetGeneration() != 0 {
		t.Errorf("reset generation failed, got: %d", msNew.GetGeneration())
	}
	msNew.GetOwnerMovements().ConditionRange(func(ownerKey string, obj generationstore.StoredObj) bool {
		if obj.GetGeneration() != 7 {
			t.Errorf("expected generation: 7, but got: %d", obj.GetGeneration())
		}
		return true
	})
}

// ---------------- test OwnerMovement struct ----------------

func TestSnapshotOwnerMovement(t *testing.T) {
	omi := NewSnapshotOwnerMovement()
	actualPods := []*schedulingv1a1.TaskInfo{
		newTaskInfo("p1", "p1", "p1"),
	}
	omi.AddDesiredAndActualState("node1", 1, nil)
	omi.AddDesiredAndActualState("node2", 2, actualPods)

	gotNodeSuggestions := omi.GetAvailableNodeSuggestions()
	expectedNodeSuggestions := map[string]int64{
		"node1": 1,
		"node2": 1,
	}
	if !reflect.DeepEqual(expectedNodeSuggestions, gotNodeSuggestions) {
		t.Errorf("expected node suggestions: %v, but got: %v", expectedNodeSuggestions, gotNodeSuggestions)
	}
	gotNodes := sets.NewString(extractNodeList(omi.GetNodesStillHaveSuggestion())...)
	expectedNodes := sets.NewString("node1", "node2")
	if !expectedNodes.Equal(gotNodes) {
		t.Errorf("expected feasible nodes: %v, but got: %v", expectedNodes, gotNodes)
	}

	omi.AddAssumedPod("p1/p1/p1", "node1")
	gotNodes = sets.NewString(extractNodeList(omi.GetNodesStillHaveSuggestion())...)
	expectedNodes = sets.NewString("node2")
	if !expectedNodes.Equal(gotNodes) {
		t.Errorf("expected feasible nodes: %v, but got: %v", expectedNodes, gotNodes)
	}

	omi.RemoveAssumedPod("p1/p1/p1", "node1")
	gotNodes = sets.NewString(extractNodeList(omi.GetNodesStillHaveSuggestion())...)
	expectedNodes = sets.NewString("node1", "node2")
	if !expectedNodes.Equal(gotNodes) {
		t.Errorf("expected feasible nodes: %v, but got: %v", expectedNodes, gotNodes)
	}
	omi.GetNodeSuggestionsStore().ConditionRange(func(nodeName string, obj generationstore.StoredObj) bool {
		generation := obj.GetGeneration()
		if generation != 0 {
			t.Errorf("expected generation for node %s: 0, but got: %d", nodeName, generation)
		}
		return true
	})

	isEmpty := omi.DeleteMovement()
	if !isEmpty {
		t.Errorf("expected empty owner movement")
	}
	if len(omi.GetAvailableNodeSuggestions()) > 0 {
		t.Errorf("expected node suggestions: nil, but got: %v", omi.GetAvailableNodeSuggestions())
	}
	if len(omi.GetNodesStillHaveSuggestion()) > 0 {
		t.Errorf("expected feasible nodes: nil, but got: %v", omi.GetNodesStillHaveSuggestion())
	}
	if omi.GetNodeSuggestionsStore().Len() > 0 {
		t.Errorf("expected node suggestions: nil, but got nodes: %v", omi.GetNodeSuggestionsStore().Keys())
	}
	omi.GetNodeSuggestionsStore().ConditionRange(func(nodeName string, obj generationstore.StoredObj) bool {
		generation := obj.GetGeneration()
		if generation != 0 {
			t.Errorf("expected generation for node %s: 0, but got: %d", nodeName, generation)
		}
		return true
	})
}

func extractNodeList(nodes map[string]int64) []string {
	var ret []string
	for k := range nodes {
		ret = append(ret, k)
	}
	return ret
}

func TestCacheOwnerMovement(t *testing.T) {
	omi := NewCacheOwnerMovement()
	actualPods := []*schedulingv1a1.TaskInfo{
		newTaskInfo("p1", "p1", "p1"),
	}
	omi.AddDesiredAndActualState("node1", 1, nil)
	omi.AddDesiredAndActualState("node2", 2, actualPods)

	gotNodeSuggestions := omi.GetAvailableNodeSuggestions()
	expectedNodeSuggestions := map[string]int64{
		"node1": 1,
		"node2": 1,
	}
	if !reflect.DeepEqual(expectedNodeSuggestions, gotNodeSuggestions) {
		t.Errorf("expected node suggestions: %v, but got: %v", expectedNodeSuggestions, gotNodeSuggestions)
	}
	gotNodes := sets.NewString(extractNodeList(omi.GetNodesStillHaveSuggestion())...)
	expectedNodes := sets.NewString("node1", "node2")
	if !expectedNodes.Equal(gotNodes) {
		t.Errorf("expected feasible nodes: %v, but got: %v", expectedNodes, gotNodes)
	}
	if omi.GetGeneration() != 0 {
		t.Errorf("expected generation: 0, but got: %d", omi.GetGeneration())
	}
	store := omi.GetNodeSuggestionsStore()
	listStore := store.(generationstore.ListStore)
	if listStore.Front().GetGeneration() != 2 {
		t.Errorf("expected generation: 2, but got: %d", listStore.Front().GetGeneration())
	}

	omi.AddAssumedPod("p1/p1/p1", "node1")
	gotNodes = sets.NewString(extractNodeList(omi.GetNodesStillHaveSuggestion())...)
	expectedNodes = sets.NewString("node2")
	if !expectedNodes.Equal(gotNodes) {
		t.Errorf("expected feasible nodes: %v, but got: %v", expectedNodes, gotNodes)
	}
	if omi.GetGeneration() != 0 {
		t.Errorf("expected generation: 0, but got: %d", omi.GetGeneration())
	}
	if listStore.Front().GetGeneration() != 3 {
		t.Errorf("expected generation: 3, but got: %d", listStore.Front().GetGeneration())
	}

	omi.RemoveAssumedPod("p1/p1/p1", "node1")
	gotNodes = sets.NewString(extractNodeList(omi.GetNodesStillHaveSuggestion())...)
	expectedNodes = sets.NewString("node1", "node2")
	if !expectedNodes.Equal(gotNodes) {
		t.Errorf("expected feasible nodes: %v, but got: %v", expectedNodes, gotNodes)
	}
	if omi.GetGeneration() != 0 {
		t.Errorf("expected generation: 0, but got: %d", omi.GetGeneration())
	}
	if listStore.Front().GetGeneration() != 4 {
		t.Errorf("expected generation: 4, but got: %d", listStore.Front().GetGeneration())
	}

	isEmpty := omi.DeleteMovement()
	if !isEmpty {
		t.Errorf("expected empty owner movement")
	}
	if len(omi.GetAvailableNodeSuggestions()) > 0 {
		t.Errorf("expected node suggestions: nil, but got: %v", omi.GetAvailableNodeSuggestions())
	}
	if len(omi.GetNodesStillHaveSuggestion()) > 0 {
		t.Errorf("expected feasible nodes: nil, but got: %v", omi.GetNodesStillHaveSuggestion())
	}
	if omi.GetNodeSuggestionsStore().Len() > 0 {
		t.Errorf("expected node suggestions: nil, but got nodes: %v", omi.GetNodeSuggestionsStore().Keys())
	}
	if omi.GetGeneration() != 0 {
		t.Errorf("expected generation: 0, but got: %d", omi.GetGeneration())
	}

	omiNew := NewSnapshotOwnerMovement()
	actualPods = []*schedulingv1a1.TaskInfo{
		newTaskInfo("p1", "p1", "p1"),
	}
	omiNew.AddDesiredAndActualState("node1", 1, nil)
	omiNew.AddDesiredAndActualState("node2", 2, actualPods)
	omiNew.AddAssumedPod("p2/p2/p2", "node1")
	omiNew.SetGeneration(1)
	omi.UpdateOwnerMovement(omiNew)
	if omiNew.GetNodeSuggestionsStore().Len() > 0 {
		t.Errorf("expected node suggestions: nil, but got nodes: %v", omiNew.GetNodeSuggestionsStore().Keys())
	}
	if omiNew.GetGeneration() != 0 {
		t.Errorf("expected generation: 0, but got: %d", omi.GetGeneration())
	}
}

// ---------------- test SuggestionForNode struct ----------------

func TestSuggestionForNode(t *testing.T) {
	suggestionForNode := NewSuggestionForNode()
	// test set operation
	suggestionForNode.SetDesiredPodCound(3)
	suggestionForNode.SetActualPods([]string{"p1/p1/p1"})
	suggestionForNode.SetAssumedPods([]string{"p2/p2/p2"})
	expectedResult := &SuggestionForNode{
		desiredPodCount: 3,
		actualPods:      NewGenerationStringSet("p1/p1/p1"),
		assumedPods:     NewGenerationStringSet("p2/p2/p2"),
		generation:      0,
	}
	if !reflect.DeepEqual(expectedResult, suggestionForNode) {
		t.Errorf("expected suggestion for node: %v, but got: %v", expectedResult, suggestionForNode)
	}

	// test add assumed pod
	suggestionForNode.AddAssumedPod("p3/p3/p3")
	expectedResult = &SuggestionForNode{
		desiredPodCount: 3,
		actualPods:      NewGenerationStringSet("p1/p1/p1"),
		assumedPods:     NewGenerationStringSet("p2/p2/p2", "p3/p3/p3"),
		generation:      0,
	}
	if !reflect.DeepEqual(expectedResult, suggestionForNode) {
		t.Errorf("expected suggestion for node: %v, but got: %v", expectedResult, suggestionForNode)
	}

	// test remove assumed pod,
	isEmpty := suggestionForNode.RemoveAssumedPod("p2/p2/p2")
	expectedResult = &SuggestionForNode{
		desiredPodCount: 3,
		actualPods:      NewGenerationStringSet("p1/p1/p1"),
		assumedPods:     NewGenerationStringSet("p3/p3/p3"),
		generation:      0,
	}
	if !reflect.DeepEqual(expectedResult, suggestionForNode) {
		t.Errorf("expected suggestion for node: %v, but got: %v", expectedResult, suggestionForNode)
	}
	if isEmpty {
		t.Errorf("suugestion for node is not empty")
	}

	// test get actual pods
	gotActualPods := suggestionForNode.GetActualPods()
	if !reflect.DeepEqual([]string{"p1/p1/p1"}, gotActualPods) {
		t.Errorf("expected actual pods: [p1/p1/p1], but got: %v", gotActualPods)
	}

	// test get assumed pods
	gotAssumedPods := suggestionForNode.GetAssumedPods()
	if !reflect.DeepEqual([]string{"p3/p3/p3"}, gotAssumedPods) {
		t.Errorf("expected actual pods: [p3/p3/p3], but got: %v", gotAssumedPods)
	}

	// test replace
	newSuggestionForNode := &SuggestionForNode{
		desiredPodCount: 0,
		actualPods:      NewGenerationStringSet(),
		assumedPods:     NewGenerationStringSet("p3/p3/p3"),
		generation:      1,
	}
	suggestionForNode.Replace(newSuggestionForNode)
	if !reflect.DeepEqual(newSuggestionForNode, suggestionForNode) {
		t.Errorf("expected suggestion for node: %v, but got: %v", newSuggestionForNode, suggestionForNode)
	}

	// test remove assumed pod, suggestion for node is empty after remove
	isEmpty = suggestionForNode.RemoveAssumedPod("p3/p3/p3")
	expectedResult = &SuggestionForNode{
		desiredPodCount: 0,
		actualPods:      NewGenerationStringSet(),
		assumedPods:     NewGenerationStringSet(),
		generation:      1,
	}
	if !reflect.DeepEqual(expectedResult, suggestionForNode) {
		t.Errorf("expected suggestion for node: %v, but got: %v", expectedResult, suggestionForNode)
	}
	if !isEmpty {
		t.Errorf("suugestion for node is empty")
	}
}

func getNodeNameFromPod(pod *v1.Pod) string {
	nodeName := ""
	if pod.Spec.NodeName != "" {
		nodeName = pod.Spec.NodeName
	} else if pod.Annotations[podutil.AssumedNodeAnnotationKey] != "" {
		nodeName = pod.Annotations[podutil.AssumedNodeAnnotationKey]
	} else if pod.Annotations[podutil.NominatedNodeAnnotationKey] != "" {
		if nominatedNode, err := getPodNominatedNode(pod); err == nil && nominatedNode != nil {
			nodeName = nominatedNode.NodeName
		}
	}

	return nodeName
}

func getPodNominatedNode(pod *v1.Pod) (*NominatedNode, error) {
	if pod == nil {
		return nil, errors.New("pod is nil")
	}
	var nominatedNode NominatedNode
	nominatedNodeData, ok := pod.Annotations[podutil.NominatedNodeAnnotationKey]
	if !ok {
		// concatenate strings instead of using fmt.Errorf to reduce possible peformance overhead
		errMsg := "pod " + pod.Name + " does not have " + podutil.NominatedNodeAnnotationKey + " annotation"
		return nil, errors.New(errMsg)
	}

	if err := json.Unmarshal([]byte(nominatedNodeData), &nominatedNode); err != nil {
		// concatenate strings instead of using fmt.Errorf to reduce possible peformance overhead
		errMsg := "failed to parse " + podutil.NominatedNodeAnnotationKey + " annotation for pod " + pod.Name + ", error is " + err.Error()
		return nil, errors.New(errMsg)
	}

	return &nominatedNode, nil
}
