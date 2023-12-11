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

import (
	"fmt"
	"sort"

	"k8s.io/apimachinery/pkg/util/sets"
)

// RawStoreImpl implement the RawStore interface.
// StoredObj will be stored in a raw hashmap, and RawStoreImpl.generation hold
// the max-generation of these items.
// ATTENTION: We need to control the lock of this data structure at the upper level.
type RawStoreImpl struct {
	store HashStore
	// generation record the latest item's generation.
	generation int64
	// updatedSet record all the items that may be changed in RawStoreImpl.
	// We need refresh these items in UpdateRawStore.
	updatedSet sets.String
}

var (
	_ Store    = &RawStoreImpl{}
	_ RawStore = &RawStoreImpl{}
)

func NewRawStore() RawStore {
	return &RawStoreImpl{
		store:      make(HashStore),
		updatedSet: sets.NewString(),
	}
}

func (s *RawStoreImpl) Get(key string) StoredObj {
	if s == nil {
		return nil
	}
	item, ok := s.store[key]
	if !ok || item == nil {
		return nil
	}
	return item
}

func (s *RawStoreImpl) Set(key string, obj StoredObj) {
	if s == nil {
		return
	}
	s.store[key] = obj
	s.updatedSet.Insert(key)
}

func (s *RawStoreImpl) Delete(key string) {
	if s == nil {
		return
	}
	delete(s.store, key)
	s.updatedSet.Insert(key)
}

func (s *RawStoreImpl) Len() int {
	if s == nil {
		return 0
	}
	return len(s.store)
}

func (s *RawStoreImpl) Range(f StoreRangeFunc) {
	if s == nil {
		return
	}
	for k, item := range s.store {
		f(k, item)
	}
}

func (s *RawStoreImpl) ConditionRange(f StoreConditionRangeFunc) bool {
	if s == nil {
		return false
	}
	for k, item := range s.store {
		if !f(k, item) {
			return true
		}
	}
	return false
}

func (s *RawStoreImpl) Keys() []string {
	if s == nil {
		return nil
	}
	ret := make([]string, 0, s.Len())
	for k := range s.store {
		ret = append(ret, k)
	}
	return ret
}

func (s *RawStoreImpl) SetGeneration(generation int64) {
	if s == nil {
		return
	}
	s.generation = generation
}

func (s *RawStoreImpl) GetGeneration() int64 {
	if s == nil {
		return 0
	}
	return s.generation
}

func (s *RawStoreImpl) UpdatedSet() sets.String {
	if s == nil {
		return sets.NewString()
	}
	return s.updatedSet
}

func (s *RawStoreImpl) ResetUpdatedSet() {
	if s == nil {
		return
	}
	s.updatedSet = sets.NewString()
}

func (s *RawStoreImpl) String() string {
	if s == nil {
		return ""
	}
	items := []string{}
	for k, item := range s.store {
		items = append(items, fmt.Sprintf("{%v:%v}", k, item))
	}
	sort.Strings(items)
	return fmt.Sprintf("{Store items: %v}", items)
}
