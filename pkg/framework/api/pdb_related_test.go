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
	"reflect"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"

	testing_helper "github.com/kubewharf/godel-scheduler/pkg/testing-helper"
)

var (
	OwnerTypeDaemonSet  = "DaemonSet"
	OwnerTypeReplicaSet = "ReplicaSet"
)

func TestPDBItemRemoveAllOwners(t *testing.T) {
	pdbItem := &pdbItemImpl{
		pdb:            testing_helper.MakePdb().Obj(),
		pdbSelector:    labels.Nothing(),
		replicaSetKeys: NewGenerationStringSet("default/rs1", "default/rs2"),
		daemonSetKeys:  NewGenerationStringSet("default/ds1"),
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
		replicaSetKeys: NewGenerationStringSet("default/rs1", "default/rs2"),
		daemonSetKeys:  NewGenerationStringSet("default/ds1"),
	}

	pdbItem.AddOwner(OwnerTypeDaemonSet, "default/ds3")
	pdbItem.RemoveOwner(OwnerTypeDaemonSet, "default/ds1")
	pdbItem.AddOwner(OwnerTypeReplicaSet, "default/rs3")
	pdbItem.RemoveOwner(OwnerTypeReplicaSet, "default/rs2")

	expectedReplicaSetKeys := NewGenerationStringSet("default/rs1", "default/rs3")
	gotReplicaSetKeys := pdbItem.GetRelatedOwnersByType(OwnerTypeReplicaSet)
	if !expectedReplicaSetKeys.Equal(NewGenerationStringSet(gotReplicaSetKeys...)) {
		t.Errorf("exoected replicaset: %v, but got: %v", expectedReplicaSetKeys, gotReplicaSetKeys)
	}

	expectedDaemonSetKeys := NewGenerationStringSet("default/ds3")
	gotDaemonSetKeys := pdbItem.GetRelatedOwnersByType(OwnerTypeDaemonSet)
	if !expectedDaemonSetKeys.Equal(NewGenerationStringSet(gotDaemonSetKeys...)) {
		t.Errorf("exoected daemonset: %v, but got: %v", expectedDaemonSetKeys, gotDaemonSetKeys)
	}
}

func TestPDBItemReplace(t *testing.T) {
	labelSelector1 := testing_helper.MakeLabelSelector().In("a", []string{"a"}).Obj()
	selector1, _ := metav1.LabelSelectorAsSelector(labelSelector1)
	existing := &pdbItemImpl{
		pdb:            testing_helper.MakePdb().Namespace("default").Name("pdb").DisruptionsAllowed(0).Selector(labelSelector1).Obj(),
		pdbSelector:    selector1,
		replicaSetKeys: NewGenerationStringSet("default/rs1"),
		daemonSetKeys:  NewGenerationStringSet("default/ds1"),
	}

	labelSelector2 := testing_helper.MakeLabelSelector().In("b", []string{"b"}).Obj()
	selector2, _ := metav1.LabelSelectorAsSelector(labelSelector2)
	cached := &pdbItemImpl{
		pdb:            testing_helper.MakePdb().Namespace("default").Name("pdb").DisruptionsAllowed(1).Selector(labelSelector2).Obj(),
		pdbSelector:    selector2,
		replicaSetKeys: NewGenerationStringSet("default/rs1", "default/rs2"),
		daemonSetKeys:  NewGenerationStringSet("default/rs2"),
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
		replicaSetKeys: NewGenerationStringSet("default/rs1"),
		daemonSetKeys:  NewGenerationStringSet("default/ds1"),
	}

	item2 := item1.Clone()
	if !reflect.DeepEqual(item1, item2) {
		t.Errorf("expected: %v, but got: %v", item1, item2)
	}
}
