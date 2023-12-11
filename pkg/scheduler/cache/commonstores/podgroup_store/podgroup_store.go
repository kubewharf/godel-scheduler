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

package podgroupstore

import (
	"fmt"

	schedulingv1a1 "github.com/kubewharf/godel-scheduler-api/pkg/apis/scheduling/v1alpha1"

	framework "github.com/kubewharf/godel-scheduler/pkg/framework/api"
	"github.com/kubewharf/godel-scheduler/pkg/scheduler/cache/commonstores"
	"github.com/kubewharf/godel-scheduler/pkg/scheduler/cache/handler"
	"github.com/kubewharf/godel-scheduler/pkg/util/generationstore"
	unitutil "github.com/kubewharf/godel-scheduler/pkg/util/unit"
)

const Name commonstores.StoreName = "PodGroupStore"

func (c *PodGroupStore) Name() commonstores.StoreName {
	return Name
}

func init() {
	commonstores.GlobalRegistry.Register(
		Name,
		func(h handler.CacheHandler) bool { return true },
		NewCache,
		NewSnapshot)
}

// ---------------------------------------------------------------------------------------

type PodGroupStore struct {
	commonstores.BaseStore
	storeType commonstores.StoreType
	handler   handler.CacheHandler

	store generationstore.Store
}

func NewCache(handler handler.CacheHandler) commonstores.CommonStore {
	return &PodGroupStore{
		BaseStore: commonstores.NewBaseStore(),
		storeType: commonstores.Cache,
		handler:   handler,

		store: generationstore.NewListStore(),
	}
}

func NewSnapshot(handler handler.CacheHandler) commonstores.CommonStore {
	return &PodGroupStore{
		BaseStore: commonstores.NewBaseStore(),
		storeType: commonstores.Snapshot,
		handler:   handler,

		store: generationstore.NewRawStore(),
	}
}

func (s *PodGroupStore) AddPodGroup(podGroup *schedulingv1a1.PodGroup) error {
	s.store.Set(unitutil.GetPodGroupKey(podGroup), framework.NewGenerationPodGroup(podGroup))
	return nil
}

func (s *PodGroupStore) RemovePodGroup(podGroup *schedulingv1a1.PodGroup) error {
	s.store.Delete(unitutil.GetPodGroupKey(podGroup))
	return nil
}

func (s *PodGroupStore) UpdatePodGroup(oldPodGroup, newPodGroup *schedulingv1a1.PodGroup) error {
	s.RemovePodGroup(oldPodGroup)
	s.AddPodGroup(newPodGroup)
	return nil
}

func (s *PodGroupStore) UpdateSnapshot(store commonstores.CommonStore) error {
	cache, snapshot := framework.TransferGenerationStore(s.store, store.(*PodGroupStore).store)
	cache.UpdateRawStore(
		snapshot,
		func(key string, obj generationstore.StoredObj) {
			pg := obj.(framework.GenerationPodGroup)
			snapshot.Set(key, pg.Clone())
		},
		generationstore.DefaultCleanFunc(cache, snapshot),
	)
	return nil
}

// -------------------------------------- Other Interface --------------------------------------

func (s *PodGroupStore) GetPodGroupInfo(podGroupName string) (*schedulingv1a1.PodGroup, error) {
	pgObj := s.store.Get(podGroupName)
	if pgObj == nil {
		return nil, fmt.Errorf("pod group %s not found", podGroupName)
	}
	pg := pgObj.(framework.GenerationPodGroup).GetPodGroup()
	if pg == nil {
		return nil, fmt.Errorf("pod group %s not found", podGroupName)
	}
	return pg, nil
}
