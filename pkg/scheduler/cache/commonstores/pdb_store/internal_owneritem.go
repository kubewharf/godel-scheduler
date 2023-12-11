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
	framework "github.com/kubewharf/godel-scheduler/pkg/framework/api"
	"github.com/kubewharf/godel-scheduler/pkg/util/generationstore"
)

// -------------------------------------- ownerItem --------------------------------------

type OwnerItemImpl struct {
	ownerLabels map[string]string
	Pdbs        framework.GenerationStringSet
	pdbUpdated  bool
	generation  int64
}

var (
	_ framework.OwnerItem       = &OwnerItemImpl{}
	_ generationstore.StoredObj = &OwnerItemImpl{}
)

func (o *OwnerItemImpl) GetGeneration() int64 {
	return o.generation
}

func (o *OwnerItemImpl) SetGeneration(generation int64) {
	o.generation = generation
}

func (o *OwnerItemImpl) GetOwnerLabels() map[string]string {
	labels := map[string]string{}
	for k, v := range o.ownerLabels {
		labels[k] = v
	}
	return labels
}

func (o *OwnerItemImpl) AddPDB(key string) {
	o.Pdbs.Insert(key)
}

func (o *OwnerItemImpl) RemovePDB(key string) {
	o.Pdbs.Delete(key)
}

func (o *OwnerItemImpl) GetRelatedPDBs() []string {
	return o.Pdbs.Strings()
}

func (o *OwnerItemImpl) SetPDBUpdated() {
	o.pdbUpdated = true
}

func (o *OwnerItemImpl) GetPDBUpdated() bool {
	return o.pdbUpdated
}

func (o *OwnerItemImpl) Replace(item framework.OwnerItem) {
	o.ownerLabels = item.GetOwnerLabels()
	o.pdbUpdated = item.GetPDBUpdated()
	o.Pdbs = framework.NewGenerationStringSet(item.GetRelatedPDBs()...)
	o.generation = item.GetGeneration()
}

func (o *OwnerItemImpl) Clone() framework.OwnerItem {
	labels := map[string]string{}
	for k, v := range o.ownerLabels {
		labels[k] = v
	}
	return &OwnerItemImpl{
		ownerLabels: labels,
		pdbUpdated:  o.pdbUpdated,
		Pdbs:        framework.NewGenerationStringSet(o.Pdbs.Strings()...),
		generation:  o.generation,
	}
}
