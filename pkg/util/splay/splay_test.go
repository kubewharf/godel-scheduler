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

package splay

import (
	"strconv"
	"testing"
)

type info struct {
	obj *obj
	sz  int
}

func (o info) Maintain(ls, rs MaintainInfo) MaintainInfo {
	o.sz = 1
	if ls != nil {
		o.sz += ls.(info).sz
	}
	if rs != nil {
		o.sz += rs.(info).sz
	}
	return o
}

type obj struct {
	k int
	v int
}

var (
	_ Comparable = &obj{}
	_ StoredObj  = &obj{}
)

func makeObj(k, v int) StoredObj {
	return &obj{k: k, v: v}
}

func (o *obj) Key() string                    { return strconv.Itoa(o.k) }
func (o *obj) String() string                 { return o.Key() }
func (o *obj) MakeMaintainInfo() MaintainInfo { return info{obj: o, sz: 1} }
func (o *obj) Clone() StoredObj               { return o }
func (o *obj) Compare(so Comparable) bool     { return o.v > so.(*obj).v }

func Test_Splay(t *testing.T) {
	s := NewSplay()
	t.Log(s.PrintTree())

	for i := 1; i < 10; i++ {
		for j := 1; j < 4; j++ {
			if s.Get(makeObj(i*10+j, i)) != nil {
				t.Errorf("There shouldn't be %v in splay.", i*10+j)
			}
		}
		for j := 1; j < 4; j++ {
			s.Insert(makeObj(i*10+j, i))
		}
		if s.Len() != i*3 {
			t.Errorf("There are %v items in splay, expect %v", s.Len(), i*3)
		}
		t.Logf("After i=%v got splay: %s\n", i, s)
	}
	for j := 1; j < 4; j++ {
		for i := 1; i < 10; i++ {
			if s.Get(makeObj(i*10+j, i)) == nil {
				t.Errorf("There should be %v in splay.", i*10+j)
			}
			s.Delete(makeObj(i*10+j, i))
		}
		t.Logf("After j=%v got splay: %s\n", j, s)
	}
}

func Test_Rotate(t *testing.T) {
	s := NewSplay()
	t.Log(s.PrintTree())

	for i := 1; i < 10; i++ {
		for j := 1; j < 4; j++ {
			s.Insert(makeObj(i*10+j, i))
		}
	}
	t.Log(s.PrintTree())
	for j := 1; j < 2; j++ {
		for i := 1; i < 10; i++ {
			if s.Get(makeObj(i*10+j, i)) == nil {
				t.Errorf("There should be %v in splay.", i*10+j)
			}
			s.Delete(makeObj(i*10+j, i))
		}
		t.Logf("After j=%v got splay: %s\n", j, s)
	}
	t.Log(s.PrintTree())
	s.Partition(makeObj(59, 5))
	t.Log(s.PrintTree())
	for j := 2; j < 4; j++ {
		for i := 1; i < 10; i++ {
			if s.Get(makeObj(i*10+j, i)) == nil {
				t.Errorf("There should be %v in splay.", i*10+j)
			}
			s.Delete(makeObj(i*10+j, i))
		}
		t.Logf("After j=%v got splay: %s\n", j, s)
	}
	t.Log(s.PrintTree())

	s.Partition(makeObj(59, 5))
}

func Test_splay_Clone(t *testing.T) {
	s := NewSplay()
	for i := 1; i < 10; i++ {
		for j := 1; j < 4; j++ {
			s.Insert(makeObj(i*10+j, i))
		}
	}

	o := s.Clone()

	if o.Len() != s.Len() {
		t.Errorf("Expect length %v, got %v\n", s.Len(), o.Len())
	}

	s.Range(func(so StoredObj) {
		origin := so.(*obj)
		if o.Get(so) != origin {
			t.Errorf("Expect object %v, got %v\n", origin, o.Get(so))
		}
	})

	t.Logf(s.PrintTree())
	t.Logf(o.PrintTree())

	o.Partition(makeObj(59, 5))

	t.Logf(s.PrintTree())
	t.Logf(o.PrintTree())
}
