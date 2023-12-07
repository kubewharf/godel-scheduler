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

	"github.com/google/go-cmp/cmp"
)

func TestSetGetPDBsAllowed(t *testing.T) {
	state := NewCycleState()
	allowed := []int32{1, 2, 3}
	SetPDBsAllowed(allowed, state)
	gotAllowed, err := GetPDBsAllowed(state)
	if err != nil {
		t.Errorf("failed to get pdbs allowed from state: %v", err)
	}
	gotAllowed[0]--
	if !reflect.DeepEqual(allowed, gotAllowed) {
		t.Errorf("expected get %v, but got %v", gotAllowed, allowed)
	}
}

func TestSetGetPodsCanNotBePreempted(t *testing.T) {
	state := NewCycleState()
	podKeys := []string{"p1", "p2"}
	for _, key := range podKeys {
		SetPodsCanNotBePreempted(key, state)
	}
	pods, err := GetPodsCanNotBePreempted(state)
	if err != nil {
		t.Errorf("failed to get pods can not be preempted: %v", err)
	}
	if !reflect.DeepEqual(podKeys, pods) {
		t.Errorf("expected %v but got %v", podKeys, pods)
	}
}

func TestSetGetMatchedPDBIndexes(t *testing.T) {
	state := NewCycleState()
	err := SetMatchedPDBIndexes("p1", []int{0, 1}, state)
	if err != nil {
		t.Errorf("failed to SetMatchedPDBIndexes: %v", err)
	}
	err = SetMatchedPDBIndexes("p2", []int{3, 4}, state)
	if err != nil {
		t.Errorf("failed to SetMatchedPDBIndexes: %v", err)
	}
	matchedPDBIndexesMap, err := GetMatchedPDBIndexes(state)
	if err != nil {
		t.Errorf("failed to GetMatchedPDBIndexes: %v", err)
	}
	expectedMatchedPDBIndexesMap := map[string][]int{
		"p1": {0, 1},
		"p2": {3, 4},
	}
	if !cmp.Equal(matchedPDBIndexesMap, expectedMatchedPDBIndexesMap) {
		t.Errorf("expected %v, but got %v", expectedMatchedPDBIndexesMap, matchedPDBIndexesMap)
	}
}

func TestSetGetIndexOfPDBKey(t *testing.T) {
	indexesMap := map[string]int{
		"pdb0": 0,
		"pdb1": 1,
		"pdb2": 2,
	}

	state := NewCycleState()
	SetIndexOfPDB(indexesMap, state)
	gotIndexesMap, err := GetIndexOfPDB(state)
	if err != nil {
		t.Errorf("failed to get indexes map: %v", err)
	}
	if !reflect.DeepEqual(indexesMap, gotIndexesMap) {
		t.Errorf("expected indexes map: %v, but got: %v", indexesMap, gotIndexesMap)
	}
}
