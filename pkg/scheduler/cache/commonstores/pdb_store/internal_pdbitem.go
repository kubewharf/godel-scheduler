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
	"k8s.io/apimachinery/pkg/labels"

	framework "github.com/kubewharf/godel-scheduler/pkg/framework/api"
	"github.com/kubewharf/godel-scheduler/pkg/util"
	"github.com/kubewharf/godel-scheduler/pkg/util/generationstore"
)

type pdbItemImpl struct {
	pdb            *policy.PodDisruptionBudget
	pdbSelector    labels.Selector
	replicaSetKeys framework.GenerationStringSet
	daemonSetKeys  framework.GenerationStringSet
	generation     int64
}

var (
	_ framework.PDBItem         = &pdbItemImpl{}
	_ generationstore.StoredObj = &pdbItemImpl{}
)

func NewPDBItemImpl(pdb *policy.PodDisruptionBudget) *pdbItemImpl {
	return &pdbItemImpl{pdb, nil, framework.NewGenerationStringSet(), framework.NewGenerationStringSet(), 0}
}

func (p *pdbItemImpl) GetPDB() *policy.PodDisruptionBudget {
	return p.pdb
}

func (p *pdbItemImpl) SetPDB(pdb *policy.PodDisruptionBudget) {
	p.pdb = pdb
}

func (p *pdbItemImpl) GetPDBSelector() labels.Selector {
	return p.pdbSelector
}

func (p *pdbItemImpl) SetPDBSelector(selector labels.Selector) {
	p.pdbSelector = selector
}

func (p *pdbItemImpl) RemoveAllOwners() {
	p.replicaSetKeys = framework.NewGenerationStringSet()
	p.daemonSetKeys = framework.NewGenerationStringSet()
}

func (p *pdbItemImpl) AddOwner(ownerType, key string) {
	switch ownerType {
	case util.OwnerTypeReplicaSet:
		p.replicaSetKeys.Insert(key)
	case util.OwnerTypeDaemonSet:
		p.daemonSetKeys.Insert(key)
	default:
		return
	}
}

func (p *pdbItemImpl) RemoveOwner(ownerType, key string) {
	switch ownerType {
	case util.OwnerTypeReplicaSet:
		p.replicaSetKeys.Delete(key)
	case util.OwnerTypeDaemonSet:
		p.daemonSetKeys.Delete(key)
	default:
		return
	}
}

func (p *pdbItemImpl) GetRelatedOwnersByType(ownerType string) []string {
	switch ownerType {
	case util.OwnerTypeReplicaSet:
		return p.replicaSetKeys.Strings()
	case util.OwnerTypeDaemonSet:
		return p.daemonSetKeys.Strings()
	}
	return nil
}

func (p *pdbItemImpl) RemovePDBFromOwner(removeFunc func(string, string)) {
	p.replicaSetKeys.Range(func(rs string) {
		removeFunc(util.OwnerTypeReplicaSet, rs)
	})
	p.daemonSetKeys.Range(func(ds string) {
		removeFunc(util.OwnerTypeDaemonSet, ds)
	})
}

func (p *pdbItemImpl) GetGeneration() int64 {
	return p.generation
}

func (p *pdbItemImpl) SetGeneration(generation int64) {
	p.generation = generation
}

func (p *pdbItemImpl) Replace(item framework.PDBItem) {
	if item == nil {
		return
	}
	p.pdb = item.GetPDB()
	p.pdbSelector = item.GetPDBSelector()
	p.daemonSetKeys.Reset(framework.NewGenerationStringSet(item.GetRelatedOwnersByType(util.OwnerTypeDaemonSet)...))
	p.replicaSetKeys.Reset(framework.NewGenerationStringSet(item.GetRelatedOwnersByType(util.OwnerTypeReplicaSet)...))
	p.generation = item.GetGeneration()
}

func (p *pdbItemImpl) Clone() framework.PDBItem {
	return &pdbItemImpl{
		pdb:            p.pdb.DeepCopy(),
		pdbSelector:    p.pdbSelector.DeepCopySelector(),
		generation:     p.generation,
		replicaSetKeys: framework.NewGenerationStringSet(p.replicaSetKeys.Strings()...),
		daemonSetKeys:  framework.NewGenerationStringSet(p.daemonSetKeys.Strings()...),
	}
}
