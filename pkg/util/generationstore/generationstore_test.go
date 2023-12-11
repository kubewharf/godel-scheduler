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

type testingObj interface {
	GetKey() string
	GetVal() string
	GetGeneration() int64
	SetGeneration(int64)
	Replace(testingObj)
}

type testingObjImpl struct {
	key, val   string
	generation int64
}

var (
	_ testingObj = &testingObjImpl{}
	_ StoredObj  = &testingObjImpl{}
)

func newTestingObj(value string) testingObj {
	return &testingObjImpl{
		key: value,
		val: value,
	}
}

func (o *testingObjImpl) GetKey() string {
	return o.key
}

func (o *testingObjImpl) GetVal() string {
	return o.val
}

func (o *testingObjImpl) GetGeneration() int64 {
	return o.generation
}

func (o *testingObjImpl) SetGeneration(generation int64) {
	o.generation = generation
}

func (o *testingObjImpl) Replace(obj testingObj) {
	o.key = obj.GetKey()
	o.val = obj.GetVal()
	// This generation can be ignored.
	o.generation = obj.GetGeneration()
}

func equalTestingObj(o1, o2 testingObj) bool {
	if (o1 == nil) != (o2 == nil) {
		return false
	}
	if o1 == nil || o2 == nil {
		return true
	}
	return o1.GetKey() == o2.GetKey() && o1.GetVal() == o2.GetVal()
}

func TestGenerationStore(t *testing.T) {
	cache, snapshot := NewListStore(), NewRawStore()

	objs := []testingObj{}
	for i := 0; i < 10; i++ {
		objs = append(objs, newTestingObj(strconv.Itoa(i)))
	}
	// Set cache, update snapshot.
	{
		for i := 0; i < 10; i++ {
			cache.Set(objs[i].GetKey(), objs[i])
		}

		// Insert dirty data
		snapshot.Set("a", newTestingObj(strconv.Itoa(0)))
		snapshot.Set("0", newTestingObj(strconv.Itoa(9)))

		cache.UpdateRawStore(
			snapshot,
			func(key string, obj StoredObj) {
				var existing testingObj
				if stored := snapshot.Get(key); stored != nil {
					existing = stored.(testingObj)
				} else {
					existing = &testingObjImpl{}
				}
				existing.Replace(obj.(testingObj))
				snapshot.Set(key, existing)
			},
			DefaultCleanFunc(cache, snapshot),
		)
		if cache.Len() != snapshot.Len() {
			t.Errorf("Length not equal! got = %v, want = %v", snapshot.Len(), cache.Len())
		}

		cache.Range(func(k string, o1 StoredObj) {
			if o2 := snapshot.Get(k); o2 == nil || !equalTestingObj(o1.(testingObj), o2.(testingObj)) {
				t.Errorf("Obj not equal between cache and snapshot! got = %v, want = %v", o2, o1)
			}
		})

		if obj := snapshot.Get("a"); obj != nil {
			t.Errorf("Dirty data still exist in snapshot! obj = %v", obj)
		}
	}
}
