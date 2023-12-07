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
	"sync/atomic"
)

func nextGeneration(generation *int64) int64 {
	return atomic.AddInt64(generation, 1)
}

// ------------------- ListItem -------------------
type ListItem struct {
	// key is the `key` used by the map in ListStore.
	key string
	// StoreObj holds the real object.
	StoredObj
	next *ListItem
	prev *ListItem
}

func newListItem(key string, obj StoredObj) *ListItem {
	return &ListItem{
		key:       key,
		StoredObj: obj,
	}
}

func (item *ListItem) Obj() StoredObj {
	if item == nil {
		return nil
	}
	return item.StoredObj
}

func (item *ListItem) Next() *ListItem {
	if item == nil {
		return nil
	}
	return item.next
}

func (item *ListItem) remove() {
	if item == nil {
		return
	}
	if item.prev != nil {
		item.prev.next = item.next
	}
	if item.next != nil {
		item.next.prev = item.prev
	}
}

// ListStoreImpl implement the ListStore interface.
// We will store all the objects in hashmap and linked-list, and maintain the StoredObj's generation
// according to ListStoreImpl.generation.
// And the linked-list will be organized in descending order of generation.
type ListStoreImpl struct {
	store      map[string]*ListItem
	head       *ListItem
	generation int64 // generation hold the global max-generation in this store.
}

var (
	_ Store     = &ListStoreImpl{}
	_ ListStore = &ListStoreImpl{}
)

func NewListStore() ListStore {
	return &ListStoreImpl{
		store: make(map[string]*ListItem),
	}
}

func (s *ListStoreImpl) Get(key string) StoredObj {
	if s == nil {
		return nil
	}
	item, ok := s.store[key]
	if !ok || item == nil {
		return nil
	}
	return item.StoredObj
}

// Set will update the ListItem if it has been existed, or create a new ListItem to hold it.
// The ListStoreImpl.generation will be updated and the ListItem will be moved to head.
func (s *ListStoreImpl) Set(key string, obj StoredObj) {
	if s == nil {
		return
	}
	var item *ListItem
	if item = s.store[key]; item != nil {
		s.remove(item)
		item.StoredObj = obj
	} else {
		item = newListItem(key, obj)
	}
	obj.SetGeneration(nextGeneration(&s.generation))
	if s.head != nil {
		s.head.prev = item
	}
	item.next, item.prev = s.head, nil
	s.head = item
	s.store[key] = item
}

func (s *ListStoreImpl) Delete(key string) {
	if s == nil {
		return
	}
	if item, ok := s.store[key]; ok && item != nil {
		s.remove(item)
	}
	delete(s.store, key)
}

func (s *ListStoreImpl) Front() *ListItem {
	if s == nil {
		return nil
	}
	return s.head
}

func (s *ListStoreImpl) Len() int {
	if s == nil {
		return 0
	}
	return len(s.store)
}

// UpdateRawStore update RawStore according the generation.
// We will update the RawStore by linked-list firstly, and by RawStore.UpdatedSet. Finally refresh the
// RawStore.generation and do cleanup.
func (s *ListStoreImpl) UpdateRawStore(store RawStore, cloneFunc CloneFunc, cleanFunc CleanFunc) {
	if s == nil || store == nil {
		return
	}
	storedGeneration := store.GetGeneration()
	for e := s.Front(); e != nil; e = e.Next() {
		if e.GetGeneration() <= storedGeneration {
			break
		}
		cloneFunc(e.key, e.StoredObj)
		store.UpdatedSet().Delete(e.key)
	}
	for key := range store.UpdatedSet() {
		if s.store[key] != nil {
			cloneFunc(key, s.store[key].StoredObj)
		}
	}
	store.ResetUpdatedSet()
	store.SetGeneration(s.generation)
	cleanFunc()
}

func (s *ListStoreImpl) Range(f StoreRangeFunc) {
	if s == nil {
		return
	}
	for k, item := range s.store {
		f(k, item.StoredObj)
	}
}

func (s *ListStoreImpl) ConditionRange(f StoreConditionRangeFunc) bool {
	if s == nil {
		return false
	}
	for k, item := range s.store {
		if !f(k, item.StoredObj) {
			return true
		}
	}
	return false
}

func (s *ListStoreImpl) Keys() []string {
	if s == nil {
		return nil
	}
	ret := make([]string, 0, s.Len())
	for k := range s.store {
		ret = append(ret, k)
	}
	return ret
}

func (s *ListStoreImpl) String() string {
	if s == nil {
		return ""
	}
	items := []string{}
	for k, item := range s.store {
		items = append(items, fmt.Sprintf("{%v:%v}", k, item.StoredObj))
	}
	sort.Strings(items)
	return fmt.Sprintf("{Store items: %v}", items)
}

func (s *ListStoreImpl) remove(item *ListItem) {
	if s == nil || item == nil {
		return
	}
	item.remove()
	if s.head == item {
		s.head = item.Next()
	}
}

// DefaultCleanFunc is to remove all the elements that no longer exist in the ListStore
// but are still in the RawStore.
func DefaultCleanFunc(cache ListStore, snapshot RawStore) CleanFunc {
	return func() {
		if cache == nil || snapshot == nil {
			return
		}
		if cache.Len() != snapshot.Len() {
			diff := snapshot.Len() - cache.Len()
			snapshot.ConditionRange(func(key string, _ StoredObj) bool {
				if diff <= 0 {
					// Quick break the range loop.
					return false
				}
				if cache.Get(key) == nil {
					snapshot.Delete(key)
					diff--
				}
				return true
			})
		}
	}
}
