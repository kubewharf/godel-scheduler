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

package pdbstore

import (
	policy "k8s.io/api/policy/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/klog/v2"

	framework "github.com/kubewharf/godel-scheduler/pkg/framework/api"
	"github.com/kubewharf/godel-scheduler/pkg/scheduler/cache/commonstores"
	preemptionstore "github.com/kubewharf/godel-scheduler/pkg/scheduler/cache/commonstores/preemption_store"
	"github.com/kubewharf/godel-scheduler/pkg/scheduler/cache/handler"
	"github.com/kubewharf/godel-scheduler/pkg/util"
	"github.com/kubewharf/godel-scheduler/pkg/util/generationstore"
)

const Name commonstores.StoreName = "PdbStore"

func (c *PdbStore) Name() commonstores.StoreName {
	return Name
}

func init() {
	commonstores.GlobalRegistry.Register(
		Name,
		func(h handler.CacheHandler) bool { return h.IsStoreEnabled(string(preemptionstore.Name)) },
		NewCache,
		NewSnapshot)
}

// ---------------------------------------------------------------------------------------

// -------------------------------------- PdbStore --------------------------------------

// PdbStore is used to cache pdb and pdb selectors
// Operation of this struct is not thread-safe, should ensure thread-safe by callers.
type PdbStore struct {
	commonstores.BaseStore
	storeType commonstores.StoreType
	handler   handler.CacheHandler

	// key is replicaset namespace/name
	ReplicaSets generationstore.Store
	// key is daemonset namespace/name
	DaemonSets generationstore.Store
	// key is pdb namespace/name
	Pdbs generationstore.Store
}

var _ commonstores.CommonStore = &PdbStore{}

func NewCache(handler handler.CacheHandler) commonstores.CommonStore {
	return &PdbStore{
		BaseStore: commonstores.NewBaseStore(),
		storeType: commonstores.Cache,
		handler:   handler,

		ReplicaSets: generationstore.NewListStore(),
		DaemonSets:  generationstore.NewListStore(),
		Pdbs:        generationstore.NewListStore(),
	}
}

func NewSnapshot(handler handler.CacheHandler) commonstores.CommonStore {
	return &PdbStore{
		BaseStore: commonstores.NewBaseStore(),
		storeType: commonstores.Snapshot,
		handler:   handler,

		ReplicaSets: generationstore.NewRawStore(),
		DaemonSets:  generationstore.NewRawStore(),
		Pdbs:        generationstore.NewRawStore(),
	}
}

func (s *PdbStore) AddPDB(pdb *policy.PodDisruptionBudget) error {
	key := util.GetPDBKey(pdb)
	var storedPDB framework.PDBItem
	if obj := s.Pdbs.Get(key); obj != nil {
		storedPDB = obj.(framework.PDBItem)
		storedPDB.SetPDB(pdb)
	} else {
		storedPDB = NewPDBItemImpl(pdb)
	}
	s.Pdbs.Set(key, storedPDB)
	s.addPDB(storedPDB, pdb)
	return nil
}

func (s *PdbStore) UpdatePDB(oldPdb, newPdb *policy.PodDisruptionBudget) error {
	key := util.GetPDBKey(newPdb)
	var storedPDB framework.PDBItem
	if obj := s.Pdbs.Get(key); obj != nil {
		storedPDB = obj.(framework.PDBItem)
		storedPDB.SetPDB(newPdb)
	} else {
		storedPDB = NewPDBItemImpl(newPdb)
	}
	s.Pdbs.Set(key, storedPDB)
	if oldPdb.Spec.Selector.String() == newPdb.Spec.Selector.String() {
		return nil
	}
	s.removePDB(key)
	s.addPDB(storedPDB, newPdb)
	return nil
}

func (s *PdbStore) DeletePDB(pdb *policy.PodDisruptionBudget) error {
	key := util.GetPDBKey(pdb)
	s.removePDB(key)
	s.Pdbs.Delete(key)
	return nil
}

func (s *PdbStore) AddOwner(ownerType, ownerKey string, ownerLabels map[string]string) error {
	var ownerStore generationstore.Store
	switch ownerType {
	case util.OwnerTypeReplicaSet:
		ownerStore = s.ReplicaSets
	case util.OwnerTypeDaemonSet:
		ownerStore = s.DaemonSets
	default:
		return nil
	}

	var matchedPDBs []string
	s.Pdbs.Range(func(key string, obj generationstore.StoredObj) {
		pdbItem := obj.(framework.PDBItem)
		selector := pdbItem.GetPDBSelector()
		if !selector.Matches(labels.Set(ownerLabels)) {
			return
		}
		pdbItem.AddOwner(ownerType, ownerKey)
		s.Pdbs.Set(key, pdbItem)
		matchedPDBs = append(matchedPDBs, key)
	})

	ownerItem := &OwnerItemImpl{ownerLabels, framework.NewGenerationStringSet(matchedPDBs...), false, 0}
	ownerStore.Set(ownerKey, ownerItem)

	return nil
}

func (s *PdbStore) DeleteOwner(ownerType, ownerKey string) error {
	var ownerStore generationstore.Store
	switch ownerType {
	case util.OwnerTypeReplicaSet:
		ownerStore = s.ReplicaSets
	case util.OwnerTypeDaemonSet:
		ownerStore = s.DaemonSets
	default:
		return nil
	}

	if obj := ownerStore.Get(ownerKey); obj != nil {
		ownerItem := obj.(framework.OwnerItem)
		for _, pdbKey := range ownerItem.GetRelatedPDBs() {
			if pdbObj := s.Pdbs.Get(pdbKey); pdbObj != nil {
				pdbItem := pdbObj.(framework.PDBItem)
				pdbItem.RemoveOwner(ownerType, ownerKey)
				s.Pdbs.Set(pdbKey, pdbItem)
			}
		}
	}

	ownerStore.Delete(ownerKey)
	return nil
}

func (c *PdbStore) UpdateOwner(ownerType, key string, oldLabels, newLabels map[string]string) error {
	if util.EqualMap(oldLabels, newLabels) {
		return nil
	}
	_, _, oldPDBs := c.GetPDBsForOwner(ownerType, key)
	c.DeleteOwner(ownerType, key)
	c.AddOwner(ownerType, key, newLabels)
	_, _, newPDBs := c.GetPDBsForOwner(ownerType, key)
	oldPDBSet := sets.NewString(oldPDBs...)
	newPDBSet := sets.NewString(newPDBs...)
	if !oldPDBSet.Equal(newPDBSet) {
		c.SetPDBUpdated(ownerType, key)
	}
	return nil
}

func updateOwners(cacheStore, snapshotStore generationstore.Store) {
	cache, snapshot := framework.TransferGenerationStore(cacheStore, snapshotStore)
	cache.UpdateRawStore(
		snapshot,
		func(key string, obj generationstore.StoredObj) {
			item := obj.(framework.OwnerItem)
			var existing framework.OwnerItem
			if obj := snapshot.Get(key); obj != nil {
				existing = obj.(framework.OwnerItem)
			} else {
				existing = &OwnerItemImpl{}
			}
			existing.Replace(item.Clone())
			snapshot.Set(key, existing)
		},
		generationstore.DefaultCleanFunc(cache, snapshot),
	)
}

func updatePDBs(cacheStore, snapshotStore generationstore.Store) {
	cache, snapshot := framework.TransferGenerationStore(cacheStore, snapshotStore)
	cache.UpdateRawStore(
		snapshot,
		func(key string, obj generationstore.StoredObj) {
			item := obj.(framework.PDBItem)
			var existing framework.PDBItem
			if obj := snapshot.Get(key); obj != nil {
				existing = obj.(framework.PDBItem)
			} else {
				existing = NewPDBItemImpl(nil)
			}
			existing.Replace(item.Clone())
			snapshot.Set(key, existing)
		},
		generationstore.DefaultCleanFunc(cache, snapshot),
	)
}

func (s *PdbStore) UpdateSnapshot(store commonstores.CommonStore) error {
	updateOwners(s.ReplicaSets, store.(*PdbStore).ReplicaSets)
	updateOwners(s.DaemonSets, store.(*PdbStore).DaemonSets)
	updatePDBs(s.Pdbs, store.(*PdbStore).Pdbs)
	return nil
}

// -------------------------------------- Other Interface --------------------------------------

func (s *PdbStore) addPDB(storedPDB framework.PDBItem, pdb *policy.PodDisruptionBudget) {
	key := util.GetPDBKey(pdb)
	selector, err := metav1.LabelSelectorAsSelector(pdb.Spec.Selector)
	if err != nil {
		klog.InfoS("Failed to get selector from pdb.Spec", "err", err)
		return
	}
	storedPDB.SetPDBSelector(selector)

	storedPDB.RemoveAllOwners()
	s.ReplicaSets.Range(func(rs string, obj generationstore.StoredObj) {
		ownerItem := obj.(framework.OwnerItem)
		if selector.Matches(labels.Set(ownerItem.GetOwnerLabels())) {
			ownerItem.AddPDB(key)
			storedPDB.AddOwner(util.OwnerTypeReplicaSet, rs)
		} else {
			ownerItem.RemovePDB(key)
		}
		s.ReplicaSets.Set(rs, ownerItem)
	})
	s.DaemonSets.Range(func(ds string, obj generationstore.StoredObj) {
		ownerItem := obj.(framework.OwnerItem)
		if selector.Matches(labels.Set(ownerItem.GetOwnerLabels())) {
			ownerItem.AddPDB(key)
			storedPDB.AddOwner(util.OwnerTypeDaemonSet, ds)
		} else {
			ownerItem.RemovePDB(key)
		}
		s.DaemonSets.Set(ds, ownerItem)
	})
}

func (s *PdbStore) removePDB(pdbKey string) {
	obj := s.Pdbs.Get(pdbKey)
	if obj == nil {
		return
	}
	pdbItem := obj.(framework.PDBItem)
	pdbItem.RemovePDBFromOwner(func(ownerType, ownerKey string) {
		var store generationstore.Store
		switch ownerType {
		case util.OwnerTypeReplicaSet:
			store = s.ReplicaSets

		case util.OwnerTypeDaemonSet:
			store = s.DaemonSets
		}
		if store == nil {
			return
		}
		storeObj := store.Get(ownerKey)
		if storeObj == nil {
			return
		}
		ownerItem := storeObj.(framework.OwnerItem)
		ownerItem.RemovePDB(pdbKey)
		store.Set(ownerKey, ownerItem)
	})
}

func (s *PdbStore) GetPDBItemList() []framework.PDBItem {
	pdbItemList := make([]framework.PDBItem, 0, s.Pdbs.Len())
	s.Pdbs.Range(func(_ string, obj generationstore.StoredObj) {
		pdbItem := obj.(framework.PDBItem)
		if pdb := pdbItem.GetPDB(); pdb != nil {
			pdbItemList = append(pdbItemList, pdbItem)
		}
	})
	return pdbItemList
}

func (s *PdbStore) GetPDBsForOwner(ownerType, ownerKey string) (bool, bool, []string) {
	var ownerStore generationstore.Store
	switch ownerType {
	case util.OwnerTypeDaemonSet:
		ownerStore = s.DaemonSets
	case util.OwnerTypeReplicaSet:
		ownerStore = s.ReplicaSets
	}
	if ownerStore == nil {
		return false, false, nil
	}
	if obj := ownerStore.Get(ownerKey); obj != nil {
		ownerItem := obj.(framework.OwnerItem)
		return true, ownerItem.GetPDBUpdated(), ownerItem.GetRelatedPDBs()
	}
	return false, false, nil
}

func (s *PdbStore) GetOwnerLabels(ownerType, ownerKey string) map[string]string {
	var ownerStore generationstore.Store
	switch ownerType {
	case util.OwnerTypeDaemonSet:
		ownerStore = s.DaemonSets
	case util.OwnerTypeReplicaSet:
		ownerStore = s.ReplicaSets
	}
	if ownerStore == nil {
		return nil
	}
	if obj := ownerStore.Get(ownerKey); obj != nil {
		ownerItem := obj.(framework.OwnerItem)
		return ownerItem.GetOwnerLabels()
	}
	return nil
}

func (s *PdbStore) SetPDBUpdated(ownerType, ownerKey string) {
	var ownerStore generationstore.Store
	switch ownerType {
	case util.OwnerTypeDaemonSet:
		ownerStore = s.DaemonSets
	case util.OwnerTypeReplicaSet:
		ownerStore = s.ReplicaSets
	}
	if ownerStore == nil {
		return
	}
	if obj := ownerStore.Get(ownerKey); obj != nil {
		ownerItem := obj.(framework.OwnerItem)
		ownerItem.SetPDBUpdated()
		ownerStore.Set(ownerKey, ownerItem)
	}
}

func (s *PdbStore) GetOwnersForPDB(key, ownerType string) []string {
	obj := s.Pdbs.Get(key)
	if obj == nil {
		return nil
	}
	pdbItem := obj.(framework.PDBItem)
	return pdbItem.GetRelatedOwnersByType(ownerType)
}
