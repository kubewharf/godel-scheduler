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
	"strconv"
	"testing"
)

func TestRawStore(t *testing.T) {
	objs := []testingObj{}
	for i := 0; i < 10; i++ {
		objs = append(objs, newTestingObj(strconv.Itoa(i)))
	}

	// Get, Set
	{
		store := NewRawStore()
		for i := 0; i < 5; i++ {
			store.Set(objs[i].GetKey(), objs[i])
		}

		if store.Len() != 5 {
			t.Errorf("Length not equal! get = %v, want = %v", store.Len(), 5)
		}
		for i := 0; i < 5; i++ {
			if get := store.Get(objs[i].GetKey()); get != objs[i] {
				t.Errorf("Obj not equal! get = %v, want = %v", get, objs[i])
			}
		}
		for i := 5; i < 10; i++ {
			if get := store.Get(objs[i].GetKey()); get != nil {
				t.Errorf("Obj should not exist! get = %v", get)
			}
		}

		store.ConditionRange(func(key string, obj StoredObj) bool {
			index, _ := strconv.Atoi(key)
			if obj != objs[index] {
				t.Errorf("Obj not equal in store! key = %v, get = %v, want = %v", key, obj, objs[index])
			}
			return true
		})
	}

	// Set, Get, Delete
	{
		store := NewRawStore()
		for i := 0; i < 10; i++ {
			store.Set(objs[i].GetKey(), objs[i])
		}
		for i := 5; i < 10; i++ {
			store.Delete(objs[i].GetKey())
		}
		for i := 5; i < 10; i++ {
			if store.Get(objs[i].GetKey()) != nil {
				t.Errorf("Obj expected to be deleted! key = %v", objs[i].GetKey())
			}
		}

		store.ConditionRange(func(key string, obj StoredObj) bool {
			index, _ := strconv.Atoi(key)
			if obj != objs[index] {
				t.Errorf("Obj not equal in store! key = %v, get = %v, want = %v", key, obj, objs[index])
			}
			return true
		})
	}
}
