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

package movementstore

import (
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/sets"
	utilfeature "k8s.io/apiserver/pkg/util/feature"

	schedulingv1a1 "github.com/kubewharf/godel-scheduler-api/pkg/apis/scheduling/v1alpha1"
	commoncache "github.com/kubewharf/godel-scheduler/pkg/common/cache"
	commonstore "github.com/kubewharf/godel-scheduler/pkg/common/store"
	"github.com/kubewharf/godel-scheduler/pkg/features"
	framework "github.com/kubewharf/godel-scheduler/pkg/framework/api"
	"github.com/kubewharf/godel-scheduler/pkg/framework/utils"
	"github.com/kubewharf/godel-scheduler/pkg/scheduler/cache/commonstores"
	podutil "github.com/kubewharf/godel-scheduler/pkg/util/pod"
)

const Name commonstore.StoreName = "MovementStore"

func (c *MovementStore) Name() commonstore.StoreName {
	return Name
}

func init() {
	commonstores.GlobalRegistries.Register(
		Name,
		func(h commoncache.CacheHandler) bool {
			return utilfeature.DefaultFeatureGate.Enabled(features.SupportRescheduling)
		},
		NewCacheMovementStore,
		NewSnapshotMovementStore)
}

// -------------------------------------- movementStore --------------------------------------

type MovementStore struct {
	commonstore.BaseStore
	storeType commonstore.StoreType
	handler   commoncache.CacheHandler

	store *framework.MovementInfo
}

func NewCacheMovementStore(handler commoncache.CacheHandler) commonstore.Store {
	return &MovementStore{
		BaseStore: commonstore.NewBaseStore(),
		storeType: commonstore.Cache,
		handler:   handler,

		store: framework.NewCacheMovementInfo(),
	}
}

func NewSnapshotMovementStore(handler commoncache.CacheHandler) commonstore.Store {
	return &MovementStore{
		BaseStore: commonstore.NewBaseStore(),
		storeType: commonstore.Snapshot,
		handler:   handler,

		store: framework.NewSnapshotMovementInfo(),
	}
}

func (s *MovementStore) AddMovement(movement *schedulingv1a1.Movement) error {
	s.store.AddMovement(movement)
	return nil
}

func (s *MovementStore) UpdateMovement(oldMovement, newMovement *schedulingv1a1.Movement) error {
	s.store.RemoveMovement(oldMovement)
	s.store.AddMovement(newMovement)
	return nil
}

func (s *MovementStore) DeleteMovement(movement *schedulingv1a1.Movement) error {
	s.store.RemoveMovement(movement)
	return nil
}

func (s *MovementStore) AddPod(pod *v1.Pod) error {
	if !podutil.BoundPod(pod) && !podutil.AssumedPodOfGodel(pod, s.handler.SchedulerType()) {
		return nil
	}
	s.store.AddAssumedPod(pod, utils.GetNodeNameFromPod(pod))
	return nil
}

func (s *MovementStore) UpdatePod(oldPod, newPod *v1.Pod) error {
	// Remove the oldPod if existed.
	{
		key, err := framework.GetPodKey(oldPod)
		if err != nil {
			return err
		}
		if ps, _ := s.handler.GetPodState(key); ps != nil {
			// Use the pod stored in Cache instead of oldPod.
			if err := s.DeletePod(ps.Pod); err != nil {
				return err
			}
		}
	}
	// Add the newPod if needed.
	{
		if err := s.AddPod(newPod); err != nil {
			return err
		}
	}
	return nil
}

func (s *MovementStore) DeletePod(pod *v1.Pod) error {
	if !podutil.BoundPod(pod) && !podutil.AssumedPodOfGodel(pod, s.handler.SchedulerType()) {
		return nil
	}
	s.store.RemoveAssumedPod(pod, utils.GetNodeNameFromPod(pod))
	return nil
}

func (s *MovementStore) AssumePod(podInfo *framework.CachePodInfo) error {
	s.store.AddAssumedPod(podInfo.Pod, utils.GetNodeNameFromPod(podInfo.Pod))
	return nil
}

func (s *MovementStore) ForgetPod(podInfo *framework.CachePodInfo) error {
	s.store.RemoveAssumedPod(podInfo.Pod, utils.GetNodeNameFromPod(podInfo.Pod))
	return nil
}

func (s *MovementStore) UpdateSnapshot(store commonstore.Store) error {
	s.store.UpdateMovementInfo(store.(*MovementStore).store)
	return nil
}

// -------------------------------- Used in Snapshot --------------------------------

type StoreHandle interface {
	GetAvailableSuggestionTimesForNodes(ownerKey string) map[string]int64
	GetSuggestedMovementAndNodes(ownerKey string) map[string][]*framework.MovementDetailOnNode
	GetDeletedPodsFromMovement(movementName string) sets.String
}

var _ StoreHandle = &MovementStore{}

func (s *MovementStore) GetAvailableSuggestionTimesForNodes(ownerKey string) map[string]int64 {
	return s.store.GetAvailableSuggestionTimesForNodes(ownerKey)
}

func (s *MovementStore) GetSuggestedMovementAndNodes(ownerKey string) map[string][]*framework.MovementDetailOnNode {
	return s.store.GetSuggestedMovementAndNodes(ownerKey)
}

func (s *MovementStore) GetDeletedPodsFromMovement(movementName string) sets.String {
	return s.store.GetDeletedPodsFromMovement(movementName)
}
