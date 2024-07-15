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
	"sync"
	"time"

	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/wait"

	commoncache "github.com/kubewharf/godel-scheduler/pkg/common/cache"
	commonstore "github.com/kubewharf/godel-scheduler/pkg/common/store"
	framework "github.com/kubewharf/godel-scheduler/pkg/framework/api"
	"github.com/kubewharf/godel-scheduler/pkg/framework/utils"
	"github.com/kubewharf/godel-scheduler/pkg/scheduler/cache/commonstores"
	"github.com/kubewharf/godel-scheduler/pkg/util/generationstore"
	podutil "github.com/kubewharf/godel-scheduler/pkg/util/pod"
)

const Name commonstore.StoreName = "PreemptionStore"

func (c *PreemptionStore) Name() commonstore.StoreName {
	return Name
}

func init() {
	commonstores.GlobalRegistries.Register(
		Name,
		func(h commoncache.CacheHandler) bool { return h.IsStoreEnabled(string(Name)) },
		NewCache,
		NewSnapshot)
}

// ---------------------------------------------------------------------------------------

// -------------------------------------- PreemptionStore --------------------------------------
type PreemptionStore struct {
	commonstore.BaseStore
	storeType commonstore.StoreType
	handler   commoncache.CacheHandler

	store *PreemptionDetails
}

var _ commonstore.Store = &PreemptionStore{}

func NewCache(handler commoncache.CacheHandler) commonstore.Store {
	return &PreemptionStore{
		BaseStore: commonstore.NewBaseStore(),
		storeType: commonstore.Cache,
		handler:   handler,

		store: NewCachePreemptionDetails(),
	}
}

func NewSnapshot(handler commoncache.CacheHandler) commonstore.Store {
	return &PreemptionStore{
		BaseStore: commonstore.NewBaseStore(),
		storeType: commonstore.Snapshot,
		handler:   handler,

		store: NewSnapshotPreemptionDetails(),
	}
}

func (s *PreemptionStore) AddPod(pod *v1.Pod) error {
	if !podutil.BoundPod(pod) && !podutil.AssumedPodOfGodel(pod, s.handler.SchedulerType()) {
		return nil
	}
	return s.podOp(pod, true)
}

func (s *PreemptionStore) UpdatePod(oldPod *v1.Pod, newPod *v1.Pod) error {
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

func (s *PreemptionStore) DeletePod(pod *v1.Pod) error {
	if !podutil.BoundPod(pod) && !podutil.AssumedPodOfGodel(pod, s.handler.SchedulerType()) {
		return nil
	}
	return s.podOp(pod, false)
}

func (s *PreemptionStore) AssumePod(podInfo *framework.CachePodInfo) error {
	return s.podOp(podInfo.Pod, true)
}

func (s *PreemptionStore) ForgetPod(podInfo *framework.CachePodInfo) error {
	return s.podOp(podInfo.Pod, false)
}

func (s *PreemptionStore) UpdateSnapshot(store commonstore.Store) error {
	updatePreemptionDetailForNodeItems := func(cacheStore, snapshotStore generationstore.Store) {
		cache, snapshot := framework.TransferGenerationStore(cacheStore, snapshotStore)
		cache.UpdateRawStore(
			snapshot,
			func(key string, obj generationstore.StoredObj) {
				set := obj.(framework.GenerationStringSet)
				var existing framework.GenerationStringSet
				if obj := snapshot.Get(key); obj != nil {
					existing = obj.(framework.GenerationStringSet)
				} else {
					existing = framework.NewGenerationStringSet()
				}
				existing.Reset(set)
				snapshot.Set(key, existing)
			},
			generationstore.DefaultCleanFunc(cache, snapshot),
		)
	}

	cache, snapshot := framework.TransferGenerationStore(s.store.NodeToVictims, store.(*PreemptionStore).store.NodeToVictims)
	cache.UpdateRawStore(
		snapshot,
		func(key string, obj generationstore.StoredObj) {
			detailForNode := obj.(*PreemptionDetailForNode)
			var existing *PreemptionDetailForNode
			if obj := snapshot.Get(key); obj != nil {
				existing = obj.(*PreemptionDetailForNode)
			} else {
				existing = NewSnapshotPreemptionDetailForNode()
			}

			existing.SetGeneration(detailForNode.GetGeneration())
			updatePreemptionDetailForNodeItems(detailForNode.VictimToPreemptors, existing.VictimToPreemptors)
			snapshot.Set(key, existing)
		},
		generationstore.DefaultCleanFunc(cache, snapshot),
	)
	return nil
}

func (s *PreemptionStore) PeriodWorker(mu *sync.RWMutex) {
	go wait.Until(func() {
		mu.Lock()
		defer mu.Unlock()
		s.store.CleanUpResidualPreemptionItems(s.handler.PodLister())
	}, time.Hour*24, s.handler.StopCh())
}

// -------------------------------------- Other Interface --------------------------------------

type StoreHandle interface {
	GetPreemptorsByVictim(node, victim string) []string
}

var _ StoreHandle = &PreemptionStore{}

func (s *PreemptionStore) GetPreemptorsByVictim(node, victim string) []string {
	return s.store.GetPreemptorsByVictim(node, victim)
}

func (s *PreemptionStore) podOp(pod *v1.Pod, isAdd bool) error {
	nominatedNode, err := utils.GetPodNominatedNode(pod)
	if err != nil {
		// Ignore this error.
		return nil
	}
	nodeName := nominatedNode.NodeName
	preemptorKey := podutil.GeneratePodKey(pod)
	for _, victimPod := range nominatedNode.VictimPods {
		victimKey := podutil.GetPodFullKey(victimPod.Namespace, victimPod.Name, victimPod.UID)
		if isAdd {
			s.store.AddPreemptItem(nodeName, victimKey, preemptorKey)
		} else {
			s.store.RemovePreemptItem(nodeName, victimKey, preemptorKey)
		}
	}
	return nil
}
