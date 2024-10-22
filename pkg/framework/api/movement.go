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
	schedulingv1a1 "github.com/kubewharf/godel-scheduler-api/pkg/apis/scheduling/v1alpha1"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/sets"

	"github.com/kubewharf/godel-scheduler/pkg/util/generationstore"
	podutil "github.com/kubewharf/godel-scheduler/pkg/util/pod"
)

func transferStore(cacheStore, snapshotStore generationstore.Store) (generationstore.ListStore, generationstore.RawStore) {
	// cacheStore and snapshotStore can not be nil.
	if cacheStore == nil || snapshotStore == nil {
		return nil, nil
	}
	return cacheStore.(generationstore.ListStore), snapshotStore.(generationstore.RawStore)
}

//------------------------ MovementInfo ------------------------

type MovementInfo struct {
	// key is owner key, value is movement name set, changed when operate movement
	OwnersToMovements generationstore.Store
	// key is movement name, value is movement state, changed when operate movement & pod
	MovementStates generationstore.Store
}

func NewCacheMovementInfo() *MovementInfo {
	return &MovementInfo{
		OwnersToMovements: generationstore.NewListStore(),
		MovementStates:    generationstore.NewListStore(),
	}
}

func NewSnapshotMovementInfo() *MovementInfo {
	return &MovementInfo{
		OwnersToMovements: generationstore.NewRawStore(),
		MovementStates:    generationstore.NewRawStore(),
	}
}

func (mi *MovementInfo) AddMovement(movement *schedulingv1a1.Movement) {
	movementName := movement.Name
	var movementState MovementState
	if obj := mi.MovementStates.Get(movementName); obj != nil {
		movementState = obj.(MovementState)
	} else {
		if _, ok := mi.MovementStates.(generationstore.ListStore); ok {
			movementState = NewCacheMovementState()
		} else {
			movementState = NewSnapshotMovementState()
		}
	}

	// add MovementStates
	mi.MovementStates.Set(movementName, movementState)
	movementState.AddMovement(movement)

	// add OwnerToMovements
	for _, ownerMovement := range movement.Status.Owners {
		ownerKey := podutil.GetOwnerInfoKey(ownerMovement.Owner)
		var movementsForOwner GenerationStringSet
		if obj := mi.OwnersToMovements.Get(ownerKey); obj != nil {
			movementsForOwner = obj.(GenerationStringSet)
		} else {
			movementsForOwner = NewGenerationStringSet()
		}
		movementsForOwner.Insert(movementName)
		mi.OwnersToMovements.Set(ownerKey, movementsForOwner)
	}
}

func (mi *MovementInfo) RemoveMovement(movement *schedulingv1a1.Movement) {
	// remove from ownersToMovements
	obj := mi.MovementStates.Get(movement.Name)
	if obj == nil {
		return
	}
	movementState, ok := obj.(MovementState)
	if !ok {
		return
	}

	movementState.ProcessOwners(func(ownerKey string) {
		obj := mi.OwnersToMovements.Get(ownerKey)
		if obj == nil {
			return
		}
		ownerMovements := obj.(GenerationStringSet)
		ownerMovements.Delete(movement.Name)
		if ownerMovements.Len() == 0 {
			mi.OwnersToMovements.Delete(ownerKey)
		} else {
			mi.OwnersToMovements.Set(ownerKey, ownerMovements)
		}
	})

	// remove from movementStates
	if movementState.DeleteMovement() {
		mi.MovementStates.Delete(movement.Name)
	} else {
		mi.MovementStates.Set(movement.Name, movementState)
	}
}

func (mi *MovementInfo) AddAssumedPod(pod *v1.Pod, nodeName string) {
	// add into podsToMovementDetails
	podKey := podutil.GeneratePodKey(pod)
	movementName := podutil.GetMovementNameFromPod(pod)
	if movementName == "" {
		return
	}
	ownerKey := podutil.GetPodOwner(pod)

	// add into movementStates
	var (
		movementState MovementState
		ok            bool
	)
	if obj := mi.MovementStates.Get(movementName); obj != nil {
		if movementState, ok = obj.(MovementState); !ok {
			return
		}
	} else {
		if _, ok := mi.MovementStates.(generationstore.ListStore); ok {
			movementState = NewCacheMovementState()
		} else {
			movementState = NewSnapshotMovementState()
		}
	}
	mi.MovementStates.Set(movementName, movementState)
	movementState.AddAssumedPod(podKey, ownerKey, nodeName)
}

func (mi *MovementInfo) RemoveAssumedPod(pod *v1.Pod, nodeName string) {
	podKey := podutil.GeneratePodKey(pod)
	movementName := podutil.GetMovementNameFromPod(pod)
	if movementName == "" {
		return
	}
	ownerKey := podutil.GetPodOwnerInfoKey(pod)

	// remove from movementStates
	obj := mi.MovementStates.Get(movementName)
	if obj == nil {
		return
	}
	movementState, ok := obj.(MovementState)
	if !ok {
		return
	}
	if movementState.RemoveAssumedPod(podKey, ownerKey, nodeName) {
		mi.MovementStates.Delete(movementName)
	} else {
		mi.MovementStates.Set(movementName, movementState)
	}
}

func (mi *MovementInfo) GetAvailableSuggestionTimesForNodes(ownerKey string) map[string]int64 {
	obj := mi.OwnersToMovements.Get(ownerKey)
	if obj == nil {
		return nil
	}
	movementSet, ok := obj.(GenerationStringSet)
	if !ok {
		return nil
	}
	suggestionTimesForNodes := map[string]int64{}
	movementSet.Range(func(movementName string) {
		obj := mi.MovementStates.Get(movementName)
		if obj == nil {
			return
		}
		movementState, ok := obj.(MovementState)
		if !ok {
			return
		}
		for nodeName, times := range movementState.GetAvailableNodeSuggestions(ownerKey) {
			suggestionTimesForNodes[nodeName] += times
		}
	})
	return suggestionTimesForNodes
}

func (mi *MovementInfo) GetSuggestedMovementAndNodes(ownerKey string) map[string][]*MovementDetailOnNode {
	obj := mi.OwnersToMovements.Get(ownerKey)
	if obj == nil {
		return nil
	}
	movementSet, ok := obj.(GenerationStringSet)
	if !ok {
		return nil
	}

	suggestedMovementAndNodes := map[string][]*MovementDetailOnNode{}
	movementSet.Range(func(movementName string) {
		obj := mi.MovementStates.Get(movementName)
		if obj == nil {
			return
		}
		movementState, ok := obj.(MovementState)
		if !ok {
			return
		}

		nodeList := movementState.GetNodesStillHaveSuggestion(ownerKey)
		algorithmName := movementState.GetAlgorithmName()
		creationTimestamp := movementState.GetCreationTimestamp()
		for nodeName, leftCount := range nodeList {
			suggestedMovementAndNodes[nodeName] = append(suggestedMovementAndNodes[nodeName], &MovementDetailOnNode{movementName, algorithmName, leftCount, creationTimestamp, false})
		}
	})
	return suggestedMovementAndNodes
}

func (mi *MovementInfo) GetDeletedPodsFromMovement(movementName string) sets.String {
	obj := mi.MovementStates.Get(movementName)
	if obj == nil {
		return nil
	}
	movementState, ok := obj.(MovementState)
	if !ok {
		return nil
	}
	return movementState.GetDeletedPods()
}

func (mi *MovementInfo) UpdateMovementInfo(snapshotMovementInfo *MovementInfo) {
	// 1. ownersToMovements
	{
		cache, snapshot := transferStore(mi.OwnersToMovements, snapshotMovementInfo.OwnersToMovements)
		cache.UpdateRawStore(
			snapshot,
			func(key string, obj generationstore.StoredObj) {
				movementsForOwner := obj.(GenerationStringSet)
				var existing GenerationStringSet
				if obj := snapshot.Get(key); obj != nil {
					existing = obj.(GenerationStringSet)
				} else {
					existing = NewGenerationStringSet()
				}
				existing.Reset(movementsForOwner)
				snapshot.Set(key, existing)
			},
			generationstore.DefaultCleanFunc(cache, snapshot),
		)
	}
	// 2. movementStates
	{
		cache, snapshot := transferStore(mi.MovementStates, snapshotMovementInfo.MovementStates)
		cache.UpdateRawStore(
			snapshot,
			func(key string, obj generationstore.StoredObj) {
				movementState := obj.(MovementState)
				var existing MovementState
				if obj := snapshot.Get(key); obj != nil {
					existing = obj.(MovementState)
				} else {
					existing = NewSnapshotMovementState()
				}
				movementState.UpdateMovementState(existing)
				snapshot.Set(key, existing)
			},
			generationstore.DefaultCleanFunc(cache, snapshot),
		)
	}
}

//------------------------ MovementState ------------------------

type MovementState interface {
	AddMovement(movement *schedulingv1a1.Movement)
	GetOwnerMovements() generationstore.Store
	GetOwnerList() []string
	ProcessOwners(func(string))
	GetAvailableNodeSuggestions(string) map[string]int64
	DeleteMovement() bool
	AddAssumedPod(string, string, string)
	RemoveAssumedPod(string, string, string) bool
	GetDeletedPods() sets.String
	SetDeletedPods([]string)
	GetGeneration() int64
	SetGeneration(int64)
	UpdateMovementState(MovementState)
	GetNodesStillHaveSuggestion(string) map[string]int64
	GetAlgorithmName() string
	SetAlgorithmName(string)
	GetCreationTimestamp() metav1.Time
	SetCreationTimestamp(metav1.Time)
}

type MovementStateImpl struct {
	// key is owner key, value is movement items of this owner
	ownerMovements    generationstore.Store
	deletedPods       GenerationStringSet
	algorithmName     string
	creationTimestamp metav1.Time
	generation        int64
}

func NewCacheMovementState() MovementState {
	return &MovementStateImpl{
		ownerMovements: generationstore.NewListStore(),
		deletedPods:    NewGenerationStringSet(),
	}
}

func NewSnapshotMovementState() MovementState {
	return &MovementStateImpl{
		ownerMovements: generationstore.NewRawStore(),
		deletedPods:    NewGenerationStringSet(),
	}
}

func (movementState *MovementStateImpl) GetGeneration() int64 {
	return movementState.generation
}

func (movementState *MovementStateImpl) SetGeneration(generation int64) {
	movementState.generation = generation
}

func (movementState *MovementStateImpl) AddMovement(movement *schedulingv1a1.Movement) {
	movementState.algorithmName = movement.Spec.Creator
	movementState.creationTimestamp = movement.CreationTimestamp

	for _, ownerMovement := range movement.Status.Owners {
		ownerKey := podutil.GetOwnerInfoKey(ownerMovement.Owner)
		for _, nodeSuggestion := range ownerMovement.RecommendedNodes {
			movementState.addOwnerMovement(ownerKey, nodeSuggestion.Node, nodeSuggestion.DesiredPodCount, nodeSuggestion.ActualPods)
		}
	}

	// add deletedTasks
	podList := make([]string, len(movement.Spec.DeletedTasks))
	for i, task := range movement.Spec.DeletedTasks {
		podKey := podutil.GetPodFullKey(task.Namespace, task.Name, string(task.UID))
		podList[i] = podKey
	}
	movementState.SetDeletedPods(podList)
}

func (movementState *MovementStateImpl) GetOwnerList() []string {
	return movementState.ownerMovements.Keys()
}

func (movementState *MovementStateImpl) ProcessOwners(f func(string)) {
	movementState.ownerMovements.Range(func(ownerKey string, so generationstore.StoredObj) {
		f(ownerKey)
	})
}

func (movementState *MovementStateImpl) addOwnerMovement(ownerKey, nodeName string, desiredPodCound int64, actualPods []*schedulingv1a1.TaskInfo) {
	var (
		ownerMovement OwnerMovement
		ok            bool
	)
	if obj := movementState.ownerMovements.Get(ownerKey); obj != nil {
		if ownerMovement, ok = obj.(OwnerMovement); !ok {
			return
		}
	} else {
		if _, ok := movementState.ownerMovements.(generationstore.ListStore); ok {
			ownerMovement = NewCacheOwnerMovement()
		} else {
			ownerMovement = NewSnapshotOwnerMovement()
		}
	}
	movementState.ownerMovements.Set(ownerKey, ownerMovement)
	ownerMovement.AddDesiredAndActualState(nodeName, desiredPodCound, actualPods)
}

func (movementState *MovementStateImpl) GetAvailableNodeSuggestions(ownerKey string) map[string]int64 {
	obj := movementState.ownerMovements.Get(ownerKey)
	if obj == nil {
		return nil
	}
	ownerMovement, ok := obj.(OwnerMovement)
	if !ok {
		return nil
	}
	return ownerMovement.GetAvailableNodeSuggestions()
}

func (movementState *MovementStateImpl) DeleteMovement() bool {
	movementState.deletedPods = NewGenerationStringSet()
	isEmpty := true
	movementState.ownerMovements.Range(func(ownerKey string, obj generationstore.StoredObj) {
		if obj == nil {
			return
		}
		ownerMovement, ok := obj.(OwnerMovement)
		if !ok {
			return
		}
		if !ownerMovement.DeleteMovement() {
			isEmpty = false
		}
		movementState.ownerMovements.Set(ownerKey, ownerMovement)
	})
	return isEmpty
}

func (movementState *MovementStateImpl) AddAssumedPod(podKey, ownerKey, nodeName string) {
	var (
		ownerMovement OwnerMovement
		ok            bool
	)
	if obj := movementState.ownerMovements.Get(ownerKey); obj != nil {
		if ownerMovement, ok = obj.(OwnerMovement); !ok {
			return
		}
	} else {
		if _, ok := movementState.ownerMovements.(generationstore.ListStore); ok {
			ownerMovement = NewCacheOwnerMovement()
		} else {
			ownerMovement = NewSnapshotOwnerMovement()
		}
	}
	movementState.ownerMovements.Set(ownerKey, ownerMovement)
	ownerMovement.AddAssumedPod(podKey, nodeName)
}

func (movementState *MovementStateImpl) RemoveAssumedPod(podKey, ownerKey, nodeName string) bool {
	obj := movementState.ownerMovements.Get(ownerKey)
	if obj == nil {
		return true
	}
	ownerMovement, ok := obj.(OwnerMovement)
	if !ok {
		return true
	}
	movementState.ownerMovements.Set(ownerKey, ownerMovement)
	if ownerMovement.RemoveAssumedPod(podKey, nodeName) {
		movementState.ownerMovements.Delete(ownerKey)
	}
	return movementState.ownerMovements.Len() == 0
}

func (movementState *MovementStateImpl) GetDeletedPods() sets.String {
	podSet := sets.NewString()
	movementState.deletedPods.Range(func(podKey string) {
		podSet.Insert(podKey)
	})
	return podSet
}

func (movementState *MovementStateImpl) GetOwnerMovements() generationstore.Store {
	return movementState.ownerMovements
}

func (movementState *MovementStateImpl) UpdateMovementState(snapshotMovementState MovementState) {
	cache, snapshot := transferStore(movementState.ownerMovements, snapshotMovementState.GetOwnerMovements())
	cache.UpdateRawStore(
		snapshot,
		func(key string, obj generationstore.StoredObj) {
			movementForOwner := obj.(OwnerMovement)
			var existing OwnerMovement
			if obj := snapshot.Get(key); obj != nil {
				existing = obj.(OwnerMovement)
			} else {
				existing = NewSnapshotOwnerMovement()
			}
			movementForOwner.UpdateOwnerMovement(existing)
			snapshot.Set(key, existing)
		},
		generationstore.DefaultCleanFunc(cache, snapshot),
	)

	snapshotMovementState.SetAlgorithmName(movementState.GetAlgorithmName())
	snapshotMovementState.SetGeneration(movementState.GetGeneration())
	snapshotMovementState.SetDeletedPods(movementState.GetDeletedPods().UnsortedList())
	snapshotMovementState.SetCreationTimestamp(movementState.GetCreationTimestamp())
}

func (movementState *MovementStateImpl) SetDeletedPods(deletedPod []string) {
	if movementState.deletedPods == nil {
		movementState.deletedPods = NewGenerationStringSet()
	}
	movementState.deletedPods.Reset(NewGenerationStringSet(deletedPod...))
}

func (movementState *MovementStateImpl) GetNodesStillHaveSuggestion(ownerKey string) map[string]int64 {
	obj := movementState.ownerMovements.Get(ownerKey)
	if obj == nil {
		return nil
	}
	ownerMovement, ok := obj.(OwnerMovement)
	if !ok {
		return nil
	}
	return ownerMovement.GetNodesStillHaveSuggestion()
}

func (movementState *MovementStateImpl) GetAlgorithmName() string {
	return movementState.algorithmName
}

func (movementState *MovementStateImpl) SetAlgorithmName(algorithm string) {
	movementState.algorithmName = algorithm
}

func (movementState *MovementStateImpl) GetCreationTimestamp() metav1.Time {
	return movementState.creationTimestamp
}

func (movementState *MovementStateImpl) SetCreationTimestamp(t metav1.Time) {
	movementState.creationTimestamp = t
}

//------------------------ OwnerMovement ------------------------

type OwnerMovement interface {
	GetGeneration() int64
	SetGeneration(int64)
	AddDesiredAndActualState(string, int64, []*schedulingv1a1.TaskInfo)
	GetAvailableNodeSuggestions() map[string]int64
	DeleteMovement() bool
	AddAssumedPod(string, string)
	RemoveAssumedPod(string, string) bool
	GetNodesStillHaveSuggestion() map[string]int64
	GetNodeSuggestionsStore() generationstore.Store
	UpdateOwnerMovement(OwnerMovement)
}

type OwnerMovementImpl struct {
	// key is node name, value is SuggestionForNode
	nodeSuggestions generationstore.Store
	generation      int64
}

func (omi *OwnerMovementImpl) GetGeneration() int64 {
	return omi.generation
}

func (omi *OwnerMovementImpl) SetGeneration(generation int64) {
	omi.generation = generation
}

func (omi *OwnerMovementImpl) AddDesiredAndActualState(nodeName string, desiredPodCound int64, actualPods []*schedulingv1a1.TaskInfo) {
	var (
		suggestionForNode *SuggestionForNode
		ok                bool
	)
	if obj := omi.nodeSuggestions.Get(nodeName); obj != nil {
		if suggestionForNode, ok = obj.(*SuggestionForNode); !ok {
			return
		}
	} else {
		suggestionForNode = NewSuggestionForNode()
	}
	omi.nodeSuggestions.Set(nodeName, suggestionForNode)
	podList := make([]string, len(actualPods))
	for i, pod := range actualPods {
		podKey := podutil.GetPodFullKey(pod.Namespace, pod.Name, string(pod.UID))
		podList[i] = podKey
	}
	suggestionForNode.SetDesiredPodCound(desiredPodCound)
	suggestionForNode.SetActualPods(podList)
}

func (omi *OwnerMovementImpl) GetAvailableNodeSuggestions() map[string]int64 {
	nodeSuggestionTimes := map[string]int64{}
	omi.nodeSuggestions.Range(func(nodeName string, obj generationstore.StoredObj) {
		suggestionForNode, ok := obj.(*SuggestionForNode)
		if !ok {
			return
		}

		usedPods := sets.NewString()
		usedPods.Insert(suggestionForNode.GetActualPods()...)
		usedPods.Insert(suggestionForNode.GetAssumedPods()...)
		available := suggestionForNode.desiredPodCount - int64(usedPods.Len())
		if available > 0 {
			nodeSuggestionTimes[nodeName] += available
		}
	})
	return nodeSuggestionTimes
}

func (omi *OwnerMovementImpl) DeleteMovement() bool {
	isEmpty := true
	omi.nodeSuggestions.Range(func(nodeName string, obj generationstore.StoredObj) {
		if obj == nil {
			return
		}
		suggestionForNode, ok := obj.(*SuggestionForNode)
		if !ok {
			return
		}
		omi.nodeSuggestions.Set(nodeName, suggestionForNode)
		if suggestionForNode.DeleteMovement() {
			omi.nodeSuggestions.Delete(nodeName)
		} else {
			isEmpty = false
		}
	})
	return isEmpty
}

func (omi *OwnerMovementImpl) AddAssumedPod(podKey, nodeName string) {
	var (
		suggestionForNode *SuggestionForNode
		ok                bool
	)
	if obj := omi.nodeSuggestions.Get(nodeName); obj != nil {
		if suggestionForNode, ok = obj.(*SuggestionForNode); !ok {
			return
		}
	} else {
		suggestionForNode = NewSuggestionForNode()
	}
	omi.nodeSuggestions.Set(nodeName, suggestionForNode)
	suggestionForNode.AddAssumedPod(podKey)
}

func (omi *OwnerMovementImpl) RemoveAssumedPod(podKey, nodeName string) bool {
	obj := omi.nodeSuggestions.Get(nodeName)
	if obj == nil {
		return true
	}
	suggestionForNode, ok := obj.(*SuggestionForNode)
	if !ok {
		return true
	}
	omi.nodeSuggestions.Set(nodeName, suggestionForNode)
	if suggestionForNode.RemoveAssumedPod(podKey) {
		omi.nodeSuggestions.Delete(nodeName)
	}
	return omi.nodeSuggestions.Len() == 0
}

func (omi *OwnerMovementImpl) GetNodesStillHaveSuggestion() map[string]int64 {
	nodeList := make(map[string]int64)
	omi.nodeSuggestions.Range(func(nodeName string, obj generationstore.StoredObj) {
		if obj == nil {
			return
		}
		suggestionForNode, ok := obj.(*SuggestionForNode)
		if !ok {
			return
		}
		podList := sets.NewString()
		podList.Insert(suggestionForNode.GetActualPods()...)
		podList.Insert(suggestionForNode.GetAssumedPods()...)
		if leftCount := suggestionForNode.desiredPodCount - int64(podList.Len()); leftCount > 0 {
			nodeList[nodeName] = leftCount
		}
	})
	return nodeList
}

func (omi *OwnerMovementImpl) GetNodeSuggestionsStore() generationstore.Store {
	return omi.nodeSuggestions
}

func (omi *OwnerMovementImpl) UpdateOwnerMovement(snapshotOwnerMovement OwnerMovement) {
	cache, snapshot := transferStore(omi.nodeSuggestions, snapshotOwnerMovement.GetNodeSuggestionsStore())
	cache.UpdateRawStore(
		snapshot,
		func(key string, obj generationstore.StoredObj) {
			suggestionForNode := obj.(*SuggestionForNode)
			var existing *SuggestionForNode
			if obj := snapshot.Get(key); obj != nil {
				existing = obj.(*SuggestionForNode)
			} else {
				existing = NewSuggestionForNode()
			}
			existing.Replace(suggestionForNode)
			snapshot.Set(key, existing)
		},
		generationstore.DefaultCleanFunc(cache, snapshot),
	)
	snapshotOwnerMovement.SetGeneration(omi.GetGeneration())
}

func NewCacheOwnerMovement() OwnerMovement {
	return &OwnerMovementImpl{
		nodeSuggestions: generationstore.NewListStore(),
	}
}

func NewSnapshotOwnerMovement() OwnerMovement {
	return &OwnerMovementImpl{
		nodeSuggestions: generationstore.NewRawStore(),
	}
}

// ------------------------ SuggestionForNode ------------------------

type SuggestionForNode struct {
	desiredPodCount int64
	actualPods      GenerationStringSet
	assumedPods     GenerationStringSet
	generation      int64
}

func NewSuggestionForNode() *SuggestionForNode {
	return &SuggestionForNode{}
}

func (s *SuggestionForNode) GetGeneration() int64 {
	return s.generation
}

func (s *SuggestionForNode) SetGeneration(generation int64) {
	s.generation = generation
}

func (s *SuggestionForNode) SetDesiredPodCound(desiredPodCound int64) {
	s.desiredPodCount = desiredPodCound
}

func (s *SuggestionForNode) SetActualPods(actualPods []string) {
	s.actualPods = NewGenerationStringSet(actualPods...)
}

func (s *SuggestionForNode) SetAssumedPods(assumedPods []string) {
	s.assumedPods = NewGenerationStringSet(assumedPods...)
}

func (s *SuggestionForNode) DeleteMovement() bool {
	s.desiredPodCount = 0
	s.actualPods = NewGenerationStringSet()
	if s.assumedPods == nil || s.assumedPods.Len() == 0 {
		return true
	}
	return false
}

func (s *SuggestionForNode) AddAssumedPod(podKey string) {
	if s.assumedPods == nil {
		s.assumedPods = NewGenerationStringSet()
	}
	s.assumedPods.Insert(podKey)
}

func (s *SuggestionForNode) RemoveAssumedPod(podKey string) bool {
	if s.assumedPods != nil {
		s.assumedPods.Delete(podKey)
	}
	return (s.actualPods == nil || s.actualPods.Len() == 0) && (s.assumedPods == nil || s.assumedPods.Len() == 0) && s.desiredPodCount == 0
}

func (s *SuggestionForNode) GetActualPods() []string {
	if s.actualPods == nil {
		return nil
	}
	var actualPods []string
	s.actualPods.Range(func(podKey string) {
		actualPods = append(actualPods, podKey)
	})
	return actualPods
}

func (s *SuggestionForNode) GetAssumedPods() []string {
	if s.assumedPods == nil {
		return nil
	}
	var assumedPods []string
	s.assumedPods.Range(func(podKey string) {
		assumedPods = append(assumedPods, podKey)
	})
	return assumedPods
}

func (s *SuggestionForNode) Replace(newSuggestion *SuggestionForNode) {
	s.SetActualPods(newSuggestion.GetActualPods())
	s.SetAssumedPods(newSuggestion.GetAssumedPods())
	s.SetDesiredPodCound(newSuggestion.desiredPodCount)
	s.SetGeneration(newSuggestion.GetGeneration())
}

// ------------------------ MovementDetailForPod ------------------------

type MovementDetailForPod struct {
	// owner key of this pod
	owner string
	// movement name
	movement string
	// node name
	node string

	generation int64
}

func NewMovementDetailForPod(owner, movement, node string) *MovementDetailForPod {
	return &MovementDetailForPod{
		owner:    owner,
		movement: movement,
		node:     node,
	}
}

func (detail *MovementDetailForPod) GetGeneration() int64 {
	return detail.generation
}

func (detail *MovementDetailForPod) SetGeneration(generation int64) {
	detail.generation = generation
}

func (detail *MovementDetailForPod) Reset(newDetail *MovementDetailForPod) {
	*detail = *newDetail
}

// ------------------------ MovementDetailOnNode ------------------------

type MovementDetailOnNode struct {
	MovementName      string
	AlgorithmName     string
	AvailableCount    int64
	CreationTimestamp metav1.Time
	WaitingTimeout    bool
}
