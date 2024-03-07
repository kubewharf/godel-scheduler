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

package cache

import (
	policy "k8s.io/api/policy/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"

	framework "github.com/kubewharf/godel-scheduler/pkg/framework/api"
	"github.com/kubewharf/godel-scheduler/pkg/util"
)

type pdbItem struct {
	pdb         *policy.PodDisruptionBudget
	pdbSelector labels.Selector
}

func (item *pdbItem) Clone() framework.PDBItem {
	return nil
}

func (item *pdbItem) GetPDB() *policy.PodDisruptionBudget {
	return item.pdb
}

func (item *pdbItem) SetPDB(pdb *policy.PodDisruptionBudget) {
	item.pdb = pdb
}

func (item *pdbItem) GetPDBSelector() labels.Selector {
	return item.pdbSelector
}

func (item *pdbItem) SetPDBSelector(pdbSelector labels.Selector) {
	item.pdbSelector = pdbSelector
}

func (item *pdbItem) GetGeneration() int64 {
	return 0
}

func (item *pdbItem) SetGeneration(int64) {}

func (item *pdbItem) Replace(framework.PDBItem) {}

func (item *pdbItem) RemoveAllOwners() {}

func (item *pdbItem) AddOwner(string, string) {}

func (item *pdbItem) RemoveOwner(string, string) {}

func (item *pdbItem) GetRelatedOwnersByType(string) []string {
	return nil
}

func (item *pdbItem) RemovePDBFromOwner(func(string, string)) {}

func (cache *binderCache) GetPDBItems() []framework.PDBItem {
	cache.mu.RLock()
	defer cache.mu.RUnlock()

	var items []framework.PDBItem
	for _, item := range cache.pdbItems {
		items = append(items, item)
	}
	return items
}

func (cache *binderCache) AddPDB(pdb *policy.PodDisruptionBudget) error {
	cache.mu.Lock()
	defer cache.mu.Unlock()

	var (
		item *pdbItem
		ok   bool
	)
	key := util.GetPDBKey(pdb)
	if item, ok = cache.pdbItems[key]; !ok {
		item = &pdbItem{}
		cache.pdbItems[key] = item
	}
	item.SetPDB(pdb)
	selector, err := metav1.LabelSelectorAsSelector(pdb.Spec.Selector)
	if err != nil {
		return err
	}
	item.SetPDBSelector(selector)

	return nil
}

func (cache *binderCache) UpdatePDB(oldPdb, newPdb *policy.PodDisruptionBudget) error {
	cache.mu.Lock()
	defer cache.mu.Unlock()

	var (
		item *pdbItem
		ok   bool
	)
	key := util.GetPDBKey(newPdb)
	if item, ok = cache.pdbItems[key]; !ok {
		item = &pdbItem{}
		cache.pdbItems[key] = item
	}
	item.SetPDB(newPdb)
	if oldPdb.Spec.Selector.String() == newPdb.Spec.Selector.String() {
		return nil
	}
	selector, err := metav1.LabelSelectorAsSelector(newPdb.Spec.Selector)
	if err != nil {
		return err
	}
	item.SetPDBSelector(selector)

	return nil
}

func (cache *binderCache) DeletePDB(pdb *policy.PodDisruptionBudget) error {
	cache.mu.Lock()
	defer cache.mu.Unlock()

	key := util.GetPDBKey(pdb)
	delete(cache.pdbItems, key)
	return nil
}
