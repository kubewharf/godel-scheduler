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

package generationstore

import "k8s.io/apimachinery/pkg/util/sets"

// StoredObj defines the methods that all the objects stored in the generationstore must have.
type StoredObj interface {
	GetGeneration() int64
	SetGeneration(int64)
}

type (
	StoreRangeFunc func(string, StoredObj)

	// The return value of StoreConditionRangeFunc means whether to continue the traversal process.
	StoreConditionRangeFunc func(string, StoredObj) bool
)

// Store defines the field that the some-datastructure will hold if it needs generationstore.
// The Store field's real object is either ListStore or RawStore.
// ATTENTION: We need to control the lock of this data structure at the upper level.
type Store interface {
	Get(string) StoredObj
	Set(string, StoredObj)
	Delete(string)
	Len() int
	Keys() []string
	String() string
	Range(StoreRangeFunc)
	ConditionRange(StoreConditionRangeFunc) bool
}

// ListStore defines the methods of ListStore.
// ATTENTION: We need to control the lock of this data structure at the upper level.
type ListStore interface {
	Store
	Front() *ListItem
	UpdateRawStore(RawStore, CloneFunc, CleanFunc)
}

// RawStore defines the methods of RawStore.
// ATTENTION: We need to control the lock of this data structure at the upper level.
type RawStore interface {
	Store
	SetGeneration(int64)
	GetGeneration() int64
	UpdatedSet() sets.String
	ResetUpdatedSet()
}

type (
	// CloneFunc defines the clone function used in UpdateRawStore.
	CloneFunc func(string, StoredObj)
	// CleanFunc defines the cleanup function used in UpdateRawStore.
	CleanFunc func()

	// HashStore defines the data model used for traversal.
	HashStore map[string]StoredObj
)
