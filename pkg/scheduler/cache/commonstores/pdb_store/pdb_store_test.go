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
	"reflect"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"

	framework "github.com/kubewharf/godel-scheduler/pkg/framework/api"
	testing_helper "github.com/kubewharf/godel-scheduler/pkg/testing-helper"
	"github.com/kubewharf/godel-scheduler/pkg/util"
	"github.com/kubewharf/godel-scheduler/pkg/util/generationstore"
)

func TestPDBItemRemoveAllOwners(t *testing.T) {
	pdbItem := &pdbItemImpl{
		pdb:            testing_helper.MakePdb().Obj(),
		pdbSelector:    labels.Nothing(),
		replicaSetKeys: framework.NewGenerationStringSet("default/rs1", "default/rs2"),
		daemonSetKeys:  framework.NewGenerationStringSet("default/ds1"),
	}

	pdbItem.RemoveAllOwners()
	if pdbItem.replicaSetKeys.Len() > 0 || pdbItem.daemonSetKeys.Len() > 0 {
		t.Errorf("failed to remove all owners")
	}
}

func TestPDBItemAddRemoveOwner(t *testing.T) {
	pdbItem := &pdbItemImpl{
		pdb:            testing_helper.MakePdb().Obj(),
		pdbSelector:    labels.Nothing(),
		replicaSetKeys: framework.NewGenerationStringSet("default/rs1", "default/rs2"),
		daemonSetKeys:  framework.NewGenerationStringSet("default/ds1"),
	}

	pdbItem.AddOwner(util.OwnerTypeDaemonSet, "default/ds3")
	pdbItem.RemoveOwner(util.OwnerTypeDaemonSet, "default/ds1")
	pdbItem.AddOwner(util.OwnerTypeReplicaSet, "default/rs3")
	pdbItem.RemoveOwner(util.OwnerTypeReplicaSet, "default/rs2")

	expectedReplicaSetKeys := framework.NewGenerationStringSet("default/rs1", "default/rs3")
	gotReplicaSetKeys := pdbItem.GetRelatedOwnersByType(util.OwnerTypeReplicaSet)
	if !expectedReplicaSetKeys.Equal(framework.NewGenerationStringSet(gotReplicaSetKeys...)) {
		t.Errorf("exoected replicaset: %v, but got: %v", expectedReplicaSetKeys, gotReplicaSetKeys)
	}

	expectedDaemonSetKeys := framework.NewGenerationStringSet("default/ds3")
	gotDaemonSetKeys := pdbItem.GetRelatedOwnersByType(util.OwnerTypeDaemonSet)
	if !expectedDaemonSetKeys.Equal(framework.NewGenerationStringSet(gotDaemonSetKeys...)) {
		t.Errorf("exoected daemonset: %v, but got: %v", expectedDaemonSetKeys, gotDaemonSetKeys)
	}
}

func TestPDBItemReplace(t *testing.T) {
	labelSelector1 := testing_helper.MakeLabelSelector().In("a", []string{"a"}).Obj()
	selector1, _ := metav1.LabelSelectorAsSelector(labelSelector1)
	existing := &pdbItemImpl{
		pdb:            testing_helper.MakePdb().Namespace("default").Name("pdb").DisruptionsAllowed(0).Selector(labelSelector1).Obj(),
		pdbSelector:    selector1,
		replicaSetKeys: framework.NewGenerationStringSet("default/rs1"),
		daemonSetKeys:  framework.NewGenerationStringSet("default/ds1"),
	}

	labelSelector2 := testing_helper.MakeLabelSelector().In("b", []string{"b"}).Obj()
	selector2, _ := metav1.LabelSelectorAsSelector(labelSelector2)
	cached := &pdbItemImpl{
		pdb:            testing_helper.MakePdb().Namespace("default").Name("pdb").DisruptionsAllowed(1).Selector(labelSelector2).Obj(),
		pdbSelector:    selector2,
		replicaSetKeys: framework.NewGenerationStringSet("default/rs1", "default/rs2"),
		daemonSetKeys:  framework.NewGenerationStringSet("default/rs2"),
	}
	existing.Replace(cached)

	if !reflect.DeepEqual(existing, cached) {
		t.Errorf("expected: %v, but got: %v", cached, existing)
	}
}

func TestPDBItemClone(t *testing.T) {
	labelSelector1 := testing_helper.MakeLabelSelector().In("a", []string{"a"}).Obj()
	selector1, _ := metav1.LabelSelectorAsSelector(labelSelector1)
	item1 := &pdbItemImpl{
		pdb:            testing_helper.MakePdb().Namespace("default").Name("pdb").DisruptionsAllowed(0).Selector(labelSelector1).Obj(),
		pdbSelector:    selector1,
		replicaSetKeys: framework.NewGenerationStringSet("default/rs1"),
		daemonSetKeys:  framework.NewGenerationStringSet("default/ds1"),
	}

	item2 := item1.Clone()
	if !reflect.DeepEqual(item1, item2) {
		t.Errorf("expected: %v, but got: %v", item1, item2)
	}
}

func TestOwnerItemReplace(t *testing.T) {
	existing := &OwnerItemImpl{
		ownerLabels: map[string]string{"a": "a"},
		Pdbs:        framework.NewGenerationStringSet("default/pdb1"),
		pdbUpdated:  false,
		generation:  0,
	}

	cached := &OwnerItemImpl{
		ownerLabels: map[string]string{"a": "a", "b": "b"},
		Pdbs:        framework.NewGenerationStringSet("default/pdb2"),
		pdbUpdated:  true,
		generation:  2,
	}

	existing.Replace(cached)
	if !reflect.DeepEqual(cached, existing) {
		t.Errorf("expected owner item: %v, but got: %v", cached, existing)
	}
}

func TestOwnerItemClone(t *testing.T) {
	item1 := &OwnerItemImpl{
		ownerLabels: map[string]string{"a": "a"},
		Pdbs:        framework.NewGenerationStringSet("default/pdb1"),
		pdbUpdated:  false,
		generation:  0,
	}
	item2 := item1.Clone()
	if !reflect.DeepEqual(item1, item2) {
		t.Errorf("expected owner item: %v, but got: %v", item1, item2)
	}
}

func TestPDBStoreOperation(t *testing.T) {
	store := &PdbStore{
		ReplicaSets: generationstore.NewRawStore(),
		DaemonSets:  generationstore.NewRawStore(),
		Pdbs:        generationstore.NewRawStore(),
	}

	oldLabelSelector := testing_helper.MakeLabelSelector().In("a", []string{"a"}).Obj()
	oldPDB := testing_helper.MakePdb().Namespace("default").Name("pdb").Selector(oldLabelSelector).DisruptionsAllowed(0).Obj()
	newLabelSelector := testing_helper.MakeLabelSelector().In("b", []string{"b"}).Obj()
	newPDB := testing_helper.MakePdb().Namespace("default").Name("pdb").Selector(newLabelSelector).DisruptionsAllowed(1).Obj()

	store.AddPDB(oldPDB)
	gotExist, gotPDBUpdated, gotPDBs := store.GetPDBsForOwner(util.OwnerTypeReplicaSet, "default/rs")
	gotOwnerLabels := store.GetOwnerLabels(util.OwnerTypeReplicaSet, "default/rs")
	expectedExist := false
	expectedPDBUpdated := false
	var expectedPDBs []string
	var expectedOwnerLabels map[string]string
	if expectedExist != gotExist {
		t.Errorf("expected %v but got %v", expectedExist, gotExist)
	}
	if expectedPDBUpdated != gotPDBUpdated {
		t.Errorf("expected %v but got %v", expectedPDBUpdated, gotPDBUpdated)
	}
	if !reflect.DeepEqual(expectedPDBs, gotPDBs) {
		t.Errorf("expected %v but got %v", expectedPDBs, gotPDBs)
	}
	if !reflect.DeepEqual(expectedOwnerLabels, gotOwnerLabels) {
		t.Errorf("expected %v but got %v", expectedOwnerLabels, gotOwnerLabels)
	}

	store.AddOwner(util.OwnerTypeReplicaSet, "default/rs", map[string]string{"b": "b"})
	gotExist, gotPDBUpdated, gotPDBs = store.GetPDBsForOwner(util.OwnerTypeReplicaSet, "default/rs")
	gotOwnerLabels = store.GetOwnerLabels(util.OwnerTypeReplicaSet, "default/rs")
	expectedExist = true
	expectedPDBUpdated = false
	expectedPDBs = []string{}
	expectedOwnerLabels = map[string]string{"b": "b"}
	if expectedExist != gotExist {
		t.Errorf("expected %v but got %v", expectedExist, gotExist)
	}
	if expectedPDBUpdated != gotPDBUpdated {
		t.Errorf("expected %v but got %v", expectedPDBUpdated, gotPDBUpdated)
	}
	if !reflect.DeepEqual(expectedPDBs, gotPDBs) {
		t.Errorf("expected %v but got %v", expectedPDBs, gotPDBs)
	}
	if !reflect.DeepEqual(expectedOwnerLabels, gotOwnerLabels) {
		t.Errorf("expected %v but got %v", expectedOwnerLabels, gotOwnerLabels)
	}

	store.UpdatePDB(oldPDB, newPDB)
	gotExist, gotPDBUpdated, gotPDBs = store.GetPDBsForOwner(util.OwnerTypeReplicaSet, "default/rs")
	gotOwnerLabels = store.GetOwnerLabels(util.OwnerTypeReplicaSet, "default/rs")
	expectedExist = true
	expectedPDBUpdated = false
	expectedPDBs = []string{"default/pdb"}
	expectedOwnerLabels = map[string]string{"b": "b"}
	if expectedExist != gotExist {
		t.Errorf("expected %v but got %v", expectedExist, gotExist)
	}
	if expectedPDBUpdated != gotPDBUpdated {
		t.Errorf("expected %v but got %v", expectedPDBUpdated, gotPDBUpdated)
	}
	if !reflect.DeepEqual(expectedPDBs, gotPDBs) {
		t.Errorf("expected %v but got %v", expectedPDBs, gotPDBs)
	}
	if !reflect.DeepEqual(expectedOwnerLabels, gotOwnerLabels) {
		t.Errorf("expected %v but got %v", expectedOwnerLabels, gotOwnerLabels)
	}

	store.SetPDBUpdated(util.OwnerTypeReplicaSet, "default/rs")
	gotExist, gotPDBUpdated, gotPDBs = store.GetPDBsForOwner(util.OwnerTypeReplicaSet, "default/rs")
	gotOwnerLabels = store.GetOwnerLabels(util.OwnerTypeReplicaSet, "default/rs")
	expectedExist = true
	expectedPDBUpdated = true
	expectedPDBs = []string{"default/pdb"}
	expectedOwnerLabels = map[string]string{"b": "b"}
	if expectedExist != gotExist {
		t.Errorf("expected %v but got %v", expectedExist, gotExist)
	}
	if expectedPDBUpdated != gotPDBUpdated {
		t.Errorf("expected %v but got %v", expectedPDBUpdated, gotPDBUpdated)
	}
	if !reflect.DeepEqual(expectedPDBs, gotPDBs) {
		t.Errorf("expected %v but got %v", expectedPDBs, gotPDBs)
	}
	if !reflect.DeepEqual(expectedOwnerLabels, gotOwnerLabels) {
		t.Errorf("expected %v but got %v", expectedOwnerLabels, gotOwnerLabels)
	}

	store.DeleteOwner(util.OwnerTypeReplicaSet, "default/rs")
	gotExist, gotPDBUpdated, gotPDBs = store.GetPDBsForOwner(util.OwnerTypeReplicaSet, "default/rs")
	gotOwnerLabels = store.GetOwnerLabels(util.OwnerTypeReplicaSet, "default/rs")
	expectedExist = false
	expectedPDBUpdated = false
	expectedPDBs = nil
	expectedOwnerLabels = nil
	if expectedExist != gotExist {
		t.Errorf("expected %v but got %v", expectedExist, gotExist)
	}
	if expectedPDBUpdated != gotPDBUpdated {
		t.Errorf("expected %v but got %v", expectedPDBUpdated, gotPDBUpdated)
	}
	if !reflect.DeepEqual(expectedPDBs, gotPDBs) {
		t.Errorf("expected %v but got %v", expectedPDBs, gotPDBs)
	}
	if !reflect.DeepEqual(expectedOwnerLabels, gotOwnerLabels) {
		t.Errorf("expected %v but got %v", expectedOwnerLabels, gotOwnerLabels)
	}

	store.DeletePDB(newPDB)
	gotExist, gotPDBUpdated, gotPDBs = store.GetPDBsForOwner(util.OwnerTypeReplicaSet, "default/rs")
	gotOwnerLabels = store.GetOwnerLabels(util.OwnerTypeReplicaSet, "default/rs")
	expectedExist = false
	expectedPDBUpdated = false
	expectedPDBs = nil
	expectedOwnerLabels = nil
	if expectedExist != gotExist {
		t.Errorf("expected %v but got %v", expectedExist, gotExist)
	}
	if expectedPDBUpdated != gotPDBUpdated {
		t.Errorf("expected %v but got %v", expectedPDBUpdated, gotPDBUpdated)
	}
	if !reflect.DeepEqual(expectedPDBs, gotPDBs) {
		t.Errorf("expected %v but got %v", expectedPDBs, gotPDBs)
	}
	if !reflect.DeepEqual(expectedOwnerLabels, gotOwnerLabels) {
		t.Errorf("expected %v but got %v", expectedOwnerLabels, gotOwnerLabels)
	}
}

func TestOperateOwner(t *testing.T) {
	cache := NewCache(nil)

	labelSelector := testing_helper.MakeLabelSelector().In("a", []string{"a"}).Obj()
	pdb := testing_helper.MakePdb().Namespace("default").Name("pdb").Selector(labelSelector).DisruptionsAllowed(0).Obj()
	cache.AddPDB(pdb)

	ownerKey := "default/ds"
	cache.AddOwner(util.OwnerTypeDaemonSet, ownerKey, map[string]string{"b": "b"})
	gotExist, gotPDBUpdated, gotPDBs := cache.(*PdbStore).GetPDBsForOwner(util.OwnerTypeDaemonSet, ownerKey)
	gotOwnerLabels := cache.(*PdbStore).GetOwnerLabels(util.OwnerTypeDaemonSet, ownerKey)
	expectedExist := true
	expectedPDBUpdated := false
	expectedPDBs := []string{}
	expectedOwnerLabels := map[string]string{"b": "b"}
	if expectedExist != gotExist {
		t.Errorf("expected %v but got %v", expectedExist, gotExist)
	}
	if expectedPDBUpdated != gotPDBUpdated {
		t.Errorf("expected %v but got %v", expectedPDBUpdated, gotPDBUpdated)
	}
	if !reflect.DeepEqual(expectedPDBs, gotPDBs) {
		t.Errorf("expected %v but got %v", expectedPDBs, gotPDBs)
	}
	if !reflect.DeepEqual(expectedOwnerLabels, gotOwnerLabels) {
		t.Errorf("expected %v but got %v", expectedOwnerLabels, gotOwnerLabels)
	}

	cache.UpdateOwner(util.OwnerTypeDaemonSet, ownerKey, map[string]string{"b": "b"}, map[string]string{"a": "a"})
	gotExist, gotPDBUpdated, gotPDBs = cache.(*PdbStore).GetPDBsForOwner(util.OwnerTypeDaemonSet, ownerKey)
	gotOwnerLabels = cache.(*PdbStore).GetOwnerLabels(util.OwnerTypeDaemonSet, ownerKey)
	expectedExist = true
	expectedPDBUpdated = true
	expectedPDBs = []string{"default/pdb"}
	expectedOwnerLabels = map[string]string{"a": "a"}
	if expectedExist != gotExist {
		t.Errorf("expected %v but got %v", expectedExist, gotExist)
	}
	if expectedPDBUpdated != gotPDBUpdated {
		t.Errorf("expected %v but got %v", expectedPDBUpdated, gotPDBUpdated)
	}
	if !reflect.DeepEqual(expectedPDBs, gotPDBs) {
		t.Errorf("expected %v but got %v", expectedPDBs, gotPDBs)
	}
	if !reflect.DeepEqual(expectedOwnerLabels, gotOwnerLabels) {
		t.Errorf("expected %v but got %v", expectedOwnerLabels, gotOwnerLabels)
	}

	cache.DeleteOwner(util.OwnerTypeDaemonSet, ownerKey)
	gotExist, gotPDBUpdated, gotPDBs = cache.(*PdbStore).GetPDBsForOwner(util.OwnerTypeDaemonSet, ownerKey)
	gotOwnerLabels = cache.(*PdbStore).GetOwnerLabels(util.OwnerTypeDaemonSet, ownerKey)
	expectedExist = false
	expectedPDBUpdated = false
	expectedPDBs = nil
	expectedOwnerLabels = nil
	if expectedExist != gotExist {
		t.Errorf("expected %v but got %v", expectedExist, gotExist)
	}
	if expectedPDBUpdated != gotPDBUpdated {
		t.Errorf("expected %v but got %v", expectedPDBUpdated, gotPDBUpdated)
	}
	if !reflect.DeepEqual(expectedPDBs, gotPDBs) {
		t.Errorf("expected %v but got %v", expectedPDBs, gotPDBs)
	}
	if !reflect.DeepEqual(expectedOwnerLabels, gotOwnerLabels) {
		t.Errorf("expected %v but got %v", expectedOwnerLabels, gotOwnerLabels)
	}
}

func TestUpdatePDBSnapshot(t *testing.T) {
	cache := NewCache(nil)
	snapshot := NewSnapshot(nil)

	labelSelector := testing_helper.MakeLabelSelector().In("a", []string{"a"}).Obj()
	pdb := testing_helper.MakePdb().Namespace("default").Name("pdb").Selector(labelSelector).DisruptionsAllowed(0).Obj()
	cache.AddPDB(pdb)
	ownerKey := "default/ds"
	cache.AddOwner(util.OwnerTypeDaemonSet, ownerKey, map[string]string{"b": "b"})
	cache.UpdateSnapshot(snapshot)
	gotExist, gotPDBUpdated, gotPDBs := snapshot.(*PdbStore).GetPDBsForOwner(util.OwnerTypeDaemonSet, ownerKey)
	gotOwnerLabels := snapshot.(*PdbStore).GetOwnerLabels(util.OwnerTypeDaemonSet, ownerKey)
	gotOwners := snapshot.(*PdbStore).GetOwnersForPDB(util.GetPDBKey(pdb), util.OwnerTypeReplicaSet)
	expectedExist := true
	expectedPDBUpdated := false
	expectedPDBs := []string{}
	expectedOwnerLabels := map[string]string{"b": "b"}
	expectedOwners := []string{}
	if expectedExist != gotExist {
		t.Errorf("expected %v but got %v", expectedExist, gotExist)
	}
	if expectedPDBUpdated != gotPDBUpdated {
		t.Errorf("expected %v but got %v", expectedPDBUpdated, gotPDBUpdated)
	}
	if !reflect.DeepEqual(expectedPDBs, gotPDBs) {
		t.Errorf("expected %v but got %v", expectedPDBs, gotPDBs)
	}
	if !reflect.DeepEqual(expectedOwnerLabels, gotOwnerLabels) {
		t.Errorf("expected %v but got %v", expectedOwnerLabels, gotOwnerLabels)
	}
	if !reflect.DeepEqual(expectedOwners, gotOwners) {
		t.Errorf("expected %v but got %v", expectedOwners, gotOwners)
	}

	cache.UpdateOwner(util.OwnerTypeDaemonSet, ownerKey, map[string]string{"b": "b"}, map[string]string{"a": "a"})
	cache.UpdateSnapshot(snapshot)
	gotExist, gotPDBUpdated, gotPDBs = snapshot.(*PdbStore).GetPDBsForOwner(util.OwnerTypeDaemonSet, ownerKey)
	gotOwnerLabels = snapshot.(*PdbStore).GetOwnerLabels(util.OwnerTypeDaemonSet, ownerKey)
	gotOwners = snapshot.(*PdbStore).GetOwnersForPDB(util.GetPDBKey(pdb), util.OwnerTypeDaemonSet)
	expectedExist = true
	expectedPDBUpdated = true
	expectedPDBs = []string{"default/pdb"}
	expectedOwnerLabels = map[string]string{"a": "a"}
	expectedOwners = []string{ownerKey}
	if expectedExist != gotExist {
		t.Errorf("expected %v but got %v", expectedExist, gotExist)
	}
	if expectedPDBUpdated != gotPDBUpdated {
		t.Errorf("expected %v but got %v", expectedPDBUpdated, gotPDBUpdated)
	}
	if !reflect.DeepEqual(expectedPDBs, gotPDBs) {
		t.Errorf("expected %v but got %v", expectedPDBs, gotPDBs)
	}
	if !reflect.DeepEqual(expectedOwnerLabels, gotOwnerLabels) {
		t.Errorf("expected %v but got %v", expectedOwnerLabels, gotOwnerLabels)
	}
	if !reflect.DeepEqual(expectedOwners, gotOwners) {
		t.Errorf("expected %v but got %v", expectedOwners, gotOwners)
	}

	pdbNew := testing_helper.MakePdb().Namespace("default").Name("pdb").DisruptionsAllowed(0).Obj()
	cache.UpdatePDB(pdb, pdbNew)
	cache.UpdateSnapshot(snapshot)
	gotExist, gotPDBUpdated, gotPDBs = snapshot.(*PdbStore).GetPDBsForOwner(util.OwnerTypeDaemonSet, ownerKey)
	gotOwnerLabels = snapshot.(*PdbStore).GetOwnerLabels(util.OwnerTypeDaemonSet, ownerKey)
	gotOwners = snapshot.(*PdbStore).GetOwnersForPDB(util.GetPDBKey(pdb), util.OwnerTypeReplicaSet)
	expectedExist = true
	expectedPDBUpdated = true
	expectedPDBs = []string{}
	expectedOwnerLabels = map[string]string{"a": "a"}
	expectedOwners = []string{}
	if expectedExist != gotExist {
		t.Errorf("expected %v but got %v", expectedExist, gotExist)
	}
	if expectedPDBUpdated != gotPDBUpdated {
		t.Errorf("expected %v but got %v", expectedPDBUpdated, gotPDBUpdated)
	}
	if !reflect.DeepEqual(expectedPDBs, gotPDBs) {
		t.Errorf("expected %v but got %v", expectedPDBs, gotPDBs)
	}
	if !reflect.DeepEqual(expectedOwnerLabels, gotOwnerLabels) {
		t.Errorf("expected %v but got %v", expectedOwnerLabels, gotOwnerLabels)
	}
	if !reflect.DeepEqual(expectedOwners, gotOwners) {
		t.Errorf("expected %v but got %v", expectedOwners, gotOwners)
	}
}
