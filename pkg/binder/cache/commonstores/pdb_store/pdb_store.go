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
	"k8s.io/klog/v2"

	"github.com/kubewharf/godel-scheduler/pkg/binder/cache/commonstores"
	commoncache "github.com/kubewharf/godel-scheduler/pkg/common/cache"
	commonstore "github.com/kubewharf/godel-scheduler/pkg/common/store"
	framework "github.com/kubewharf/godel-scheduler/pkg/framework/api"
	"github.com/kubewharf/godel-scheduler/pkg/util"
	"github.com/kubewharf/godel-scheduler/pkg/util/generationstore"
)

const Name commonstore.StoreName = "PdbStore"

func (s *PdbStore) Name() commonstore.StoreName {
	return Name
}

func init() {
	commonstores.GlobalRegistries.Register(
		Name,
		func(h commoncache.CacheHandler) bool { return true },
		NewCache,
		NewSnapshot)
}

// -------------------------------------- PdbStore --------------------------------------

// PdbStore is used to cache pdb and pdb selecors
// Operation of this struct is not thread-safe, should ensure thread-safe by callers.
type PdbStore struct {
	commonstore.BaseStore
	storeType commonstore.StoreType
	handler   commoncache.CacheHandler

	Pdbs generationstore.Store
}

var _ commonstore.Store = &PdbStore{}

func NewCache(handler commoncache.CacheHandler) commonstore.Store {
	return &PdbStore{
		BaseStore: commonstore.NewBaseStore(),
		storeType: commonstore.Cache,
		handler:   handler,

		Pdbs: generationstore.NewListStore(),
	}
}

func NewSnapshot(handler commoncache.CacheHandler) commonstore.Store {
	return &PdbStore{
		BaseStore: commonstore.NewBaseStore(),
		storeType: commonstore.Snapshot,
		handler:   handler,

		Pdbs: generationstore.NewRawStore(),
	}
}

func (s *PdbStore) AddPDB(pdb *policy.PodDisruptionBudget) error {
	key := util.GetPDBKey(pdb)
	var storedPDB framework.PDBItem
	if obj := s.Pdbs.Get(key); obj != nil {
		storedPDB = obj.(framework.PDBItem)
		storedPDB.SetPDB(pdb)
	} else {
		storedPDB = framework.NewPDBItemImpl(pdb)
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
		storedPDB = framework.NewPDBItemImpl(newPdb)
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

func (s *PdbStore) UpdateSnapshot(store commonstore.Store) error {
	return nil
}

// -------------------------------------- Internal Function --------------------------------------

func (s *PdbStore) addPDB(storedPDB framework.PDBItem, pdb *policy.PodDisruptionBudget) {
	selector, err := metav1.LabelSelectorAsSelector(pdb.Spec.Selector)
	if err != nil {
		klog.InfoS("Failed to get selector from pdb.Spec", "err", err)
		return
	}
	storedPDB.SetPDBSelector(selector)
}

func (s *PdbStore) removePDB(pdbKey string) { return }

// -------------------------------------- Other Interface --------------------------------------

type StoreHandle interface {
	GetPDBItemList() []framework.PDBItem
}

var _ StoreHandle = &PdbStore{}

func (s *PdbStore) GetPDBItemList() []framework.PDBItem {
	s.handler.Mutex().RLock()
	defer s.handler.Mutex().RUnlock()

	pdbItemList := make([]framework.PDBItem, 0, s.Pdbs.Len())
	s.Pdbs.Range(func(_ string, obj generationstore.StoredObj) {
		pdbItem := obj.(framework.PDBItem)
		if pdb := pdbItem.GetPDB(); pdb != nil {
			pdbItemList = append(pdbItemList, pdbItem)
		}
	})
	return pdbItemList
}
