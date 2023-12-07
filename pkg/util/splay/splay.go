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
	"strings"
)

type Comparable interface {
	// Compare defines a comparison function in splay that returns `true` if and only if the
	// current element is strictly greater than the incoming element.
	Compare(Comparable) bool
}

type MaintainInfo interface {
	// Maintain defines the maintenance operation in the splay, which contains the properties
	// of the subtree rooted at the current node. We will update the properties of the current
	// node based on its left and right children.
	Maintain(MaintainInfo, MaintainInfo) MaintainInfo
}

// StoredObj defines all the methods that need to be implemented by the element being stored.
type StoredObj interface {
	// Key returns the unique key used by the object in the splay.
	Key() string
	// String implements the String interface.
	String() string

	// MakeMaintainInfo return a MaintainInfo used by the StoredObj.
	MakeMaintainInfo() MaintainInfo

	Comparable
}

// maintainInfoForLookup defines one of the simplest MaintainInfo implementations for lookups only.
type maintainInfoForLookup struct{}

func (o maintainInfoForLookup) String() string {
	return ""
}

func (o maintainInfoForLookup) Maintain(l, r MaintainInfo) MaintainInfo { return o }

// storedObjForLookup defines one of the simplest StoredObj implementations for lookups only.
type storedObjForLookup struct{ key string }

func (o *storedObjForLookup) Key() string                    { return o.key }
func (o *storedObjForLookup) String() string                 { return o.key }
func (o *storedObjForLookup) Maintain(_, _ StoredObj)        {}
func (o *storedObjForLookup) MakeMaintainInfo() MaintainInfo { return maintainInfoForLookup{} }
func (o *storedObjForLookup) Compare(Comparable) bool        { return false }
func NewStoredObjForLookup(key string) StoredObj {
	return &storedObjForLookup{
		key: key,
	}
}

var (
	_ MaintainInfo = maintainInfoForLookup{}
	_ StoredObj    = &storedObjForLookup{}
	_ Splay        = &splay{}

	nilObj = NewStoredObjForLookup("NilObj")
	minObj = NewStoredObjForLookup("MinObj")
	maxObj = NewStoredObjForLookup("MaxObj")
)

type (
	// RangeFunc visit objects by inorder traversal.
	RangeFunc func(StoredObj)
	// ConditionRangeFunc visit objects by inorder traversal.
	ConditionRangeFunc func(StoredObj) bool
)

type node struct {
	lchild, rchild int
	parent         int
	key            string
	obj            StoredObj
}

func newNode(o StoredObj, p int) node {
	return node{
		parent: p,
		key:    o.Key(),
		obj:    o,
	}
}

// Splay defines all methods of the splay-tree.
type Splay interface {
	// Insert a StoredObj into the splay. Returns true if successful.
	Insert(StoredObj) bool
	// Delete a StoredObj from the splay. Returns true if successful.
	Delete(StoredObj) bool
	// Get a StoredObj from the splay.
	Get(StoredObj) StoredObj
	// Partition will bring together all objects strictly smaller than the current object
	// in a subtree and return the root's MaintainInfo of the subtree.
	Partition(Comparable) MaintainInfo
	// Range traverses the entire splay in mid-order.
	Range(RangeFunc)
	// ConditionRange traverses the entire splay in mid-order and ends the access immediately
	// if ConditionRangeFunc returns false.
	ConditionRange(ConditionRangeFunc)
	// Len returns the number of all objects in the splay.
	Len() int
	// String implements the String interface.
	String() string
	// Clone return a clone of the Splay.
	Clone() Splay
	// PrintTree outputs splay in the form of a tree diagram.
	PrintTree() string
}

type splay struct {
	root       int
	minv, maxv int
	hash       map[string]int
	items      []node
	infos      []MaintainInfo
	count      int

	chooseChildIndex func(Comparable, int) int
	maintain         func(int)
}

func NewSplay() Splay {
	s := &splay{
		minv:  1,
		maxv:  2,
		hash:  make(map[string]int),
		items: []node{newNode(nilObj, -1), newNode(minObj, 0), newNode(maxObj, 1)},
		infos: []MaintainInfo{nilObj.MakeMaintainInfo(), minObj.MakeMaintainInfo(), maxObj.MakeMaintainInfo()},
		count: 2,
	}
	s.items[s.minv].rchild, s.items[s.maxv].parent = s.maxv, s.minv
	s.root = s.minv
	s.chooseChildIndex = func(o Comparable, n int) int {
		if n == s.minv || n != s.maxv && o.Compare(s.items[n].obj) {
			return 1
		}
		return 0
	}
	s.maintain = func(i int) {
		n := &s.items[i]
		var leftChildInfo, rightChildInfo MaintainInfo
		if n.lchild != 0 && n.lchild != s.minv {
			leftChildInfo = s.infos[n.lchild]
		}
		if n.rchild != 0 && n.rchild != s.maxv {
			rightChildInfo = s.infos[n.rchild]
		}
		s.infos[i] = s.infos[i].Maintain(leftChildInfo, rightChildInfo)
	}
	return s
}

func (s *splay) Insert(v StoredObj) bool {
	if i, ok := s.hash[v.Key()]; ok {
		s.items[i].obj = v
		return false
	}
	p, n := 0, s.root
	for n != 0 {
		p = n
		if s.chooseChildIndex(v, n) == 1 {
			n = s.items[n].rchild
		} else {
			n = s.items[n].lchild
		}
	}

	{
		s.items = append(s.items, newNode(v, p))
		s.infos = append(s.infos, v.MakeMaintainInfo())
		s.count++
		s.hash[v.Key()] = s.count
		n = s.count
	}
	if p != 0 {
		if s.chooseChildIndex(v, p) == 1 {
			s.items[p].rchild = n
		} else {
			s.items[p].lchild = n
		}
	}
	s.splay(n, 0)
	return true
}

func (s *splay) Delete(v StoredObj) bool {
	i, ok := s.hash[v.Key()]
	if !ok {
		return false
	}
	s.splay(i, 0)
	find := func(dir int) (ret int) {
		if dir == 0 {
			for ret = s.items[i].lchild; s.items[ret].rchild != 0; ret = s.items[ret].rchild {
			}
		} else {
			for ret = s.items[i].rchild; s.items[ret].lchild != 0; ret = s.items[ret].lchild {
			}
		}
		return
	}
	pre, nxt := find(0), find(1)
	s.splay(pre, 0)
	s.splay(nxt, pre)
	s.items[nxt].lchild = 0
	s.maintain(nxt)
	s.maintain(pre)
	delete(s.hash, v.Key())

	lastIndex := s.count
	if i != lastIndex {
		lastNode := &s.items[lastIndex]
		lastParent, lastLeftChild, lastRightChild := lastNode.parent, lastNode.lchild, lastNode.rchild
		if lastParent != 0 {
			if s.getChildIndex(lastIndex, lastParent) == 1 {
				s.items[lastParent].rchild = i
			} else {
				s.items[lastParent].lchild = i
			}
		}
		if lastLeftChild != 0 {
			s.items[lastLeftChild].parent = i
		}
		if lastRightChild != 0 {
			s.items[lastRightChild].parent = i
		}
		s.hash[lastNode.key] = i
		s.items[i] = s.items[lastIndex]
		s.infos[i] = s.infos[lastIndex]
		if s.root == lastIndex {
			s.root = i
		}
	}
	s.items = s.items[:s.count]
	s.infos = s.infos[:s.count]
	s.count--
	return true
}

func (s *splay) Get(obj StoredObj) StoredObj {
	i, ok := s.hash[obj.Key()]
	if !ok {
		return nil
	}
	return s.items[i].obj
}

func (s *splay) Partition(obj Comparable) MaintainInfo {
	s.splay(s.minv, 0)
	var next int
	for p := s.root; p != 0; {
		if s.chooseChildIndex(obj, p) == 1 {
			p = s.items[p].rchild
		} else {
			next = p
			p = s.items[p].lchild
		}
	}
	s.splay(next, s.minv)
	p := s.items[next].lchild
	if p == 0 {
		return nil
	}
	return s.infos[p]
}

func (s *splay) Range(f RangeFunc) {
	var dfs func(int)
	dfs = func(i int) {
		if i == 0 {
			return
		}
		dfs(s.items[i].lchild)
		if i != s.minv && i != s.maxv {
			f(s.items[i].obj)
		}
		dfs(s.items[i].rchild)
	}
	dfs(s.root)
}

func (s *splay) ConditionRange(f ConditionRangeFunc) {
	var dfs func(int)
	dfs = func(i int) {
		if i == 0 {
			return
		}
		dfs(s.items[i].lchild)
		if i != s.minv && i != s.maxv {
			if !f(s.items[i].obj) {
				return
			}
		}
		dfs(s.items[i].rchild)
	}
	dfs(s.root)
}

func (s *splay) Len() int {
	return s.count - 2
}

func (s *splay) String() string {
	output := &strings.Builder{}
	var dfs func(int)
	dfs = func(i int) {
		if i == 0 {
			return
		}
		dfs(s.items[i].lchild)
		if i != s.minv && i != s.maxv {
			output.WriteString(s.items[i].key + ",")
		}
		dfs(s.items[i].rchild)
	}
	dfs(s.root)
	return output.String()
}

func (s *splay) Clone() Splay {
	clone := NewSplay().(*splay)
	hash, items, infos := make(map[string]int, len(s.hash)), make([]node, len(s.hash)+3), make([]MaintainInfo, len(s.hash)+3)

	copy(items, s.items)
	copy(infos, s.infos)
	len := len(s.items)
	for i := 3; i < len; i++ {
		hash[items[i].key] = i
	}

	clone.hash, clone.items, clone.infos, clone.count, clone.root = hash, items, infos, s.count, s.root
	return clone
}

func (s *splay) PrintTree() string {
	output := &strings.Builder{}
	var dfs func(int, *strings.Builder, bool)
	dfs = func(i int, prefixBuilder *strings.Builder, isBottom bool) {
		prefix := prefixBuilder.String()
		handleSon := func(j int, flag bool) {
			if j == 0 {
				return
			}
			nextPrefixBuilder := &strings.Builder{}
			nextPrefixBuilder.WriteString(prefix)
			if isBottom != flag {
				nextPrefixBuilder.WriteString("│   ")
			} else {
				nextPrefixBuilder.WriteString("    ")
			}
			dfs(j, nextPrefixBuilder, flag)
		}
		handleSon(s.items[i].rchild, false)
		output.WriteString(prefix)
		if isBottom {
			output.WriteString("└── ")
		} else {
			output.WriteString("┌── ")
		}
		output.WriteString(s.items[i].obj.String() + "(" + strconv.Itoa(i) + ")")
		output.WriteByte('\n')
		handleSon(s.items[i].lchild, true)
	}
	output.WriteString("SplayRoot:" + "root=" + strconv.Itoa(s.root) + "\n")
	dfs(s.root, &strings.Builder{}, true)
	return output.String()
}

// getChildIndex indicates whether `x` is the right child of `y`.
func (s *splay) getChildIndex(x, y int) int {
	if y != 0 && s.items[y].rchild == x {
		return 1
	}
	return 0
}

func (s *splay) rotate(x int) {
	y := s.items[x].parent
	z := s.items[y].parent
	k := s.getChildIndex(x, y)
	if z != 0 {
		if s.getChildIndex(y, z) == 1 {
			s.items[z].rchild = x
		} else {
			s.items[z].lchild = x
		}
	}
	s.items[x].parent = z
	if k == 1 {
		s.items[y].rchild = s.items[x].lchild
		if s.items[x].lchild != 0 {
			s.items[s.items[x].lchild].parent = y
		}
		s.items[x].lchild = y
	} else {
		s.items[y].lchild = s.items[x].rchild
		if s.items[x].rchild != 0 {
			s.items[s.items[x].rchild].parent = y
		}
		s.items[x].rchild = y
	}
	s.items[y].parent = x
	s.maintain(y)
	s.maintain(x)
}

func (s *splay) splay(x, k int) {
	for s.items[x].parent != k {
		y := s.items[x].parent
		z := s.items[y].parent
		if z != k {
			if s.getChildIndex(x, y) != s.getChildIndex(y, z) {
				s.rotate(x)
			} else {
				s.rotate(y)
			}
		}
		s.rotate(x)
	}
	if k == 0 {
		s.root = x
	}
}
