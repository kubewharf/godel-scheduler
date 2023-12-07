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

package bitplace

type fenwick struct {
	items []int
	n     int
}

func lowbit(x int) int {
	return x & -x
}

func (f *fenwick) add(x, v int) {
	for i := x; i <= f.n; i += lowbit(i) {
		f.items[i] += v
	}
}

func (f *fenwick) sum(x int) int {
	ret := 0
	for i := x; i != 0; i -= lowbit(i) {
		ret += f.items[i]
	}
	return ret
}

type BitPlace interface {
	Free(int)
	Alloc() int
	Clean()
}

type bitPlaceImpl struct {
	f *fenwick
}

func New(n int) BitPlace {
	return &bitPlaceImpl{
		f: &fenwick{
			items: make([]int, n+1),
			n:     n,
		},
	}
}

func (b *bitPlaceImpl) has(x int) bool {
	if x <= 0 {
		return false
	}
	return b.f.sum(x)-b.f.sum(x-1) > 0
}

func (b *bitPlaceImpl) Free(x int) {
	x++
	if !b.has(x) {
		return
	}
	b.f.add(x, -1)
}

func (b *bitPlaceImpl) Alloc() int {
	findUnusedIdx := func() int {
		l, r := 1, b.f.n+1
		for l < r {
			mid := (l + r) >> 1
			if b.f.sum(mid) >= mid {
				l = mid + 1
			} else {
				r = mid
			}
		}
		return l
	}
	idx := findUnusedIdx()
	if idx > b.f.n {
		return -1
	}
	b.f.add(idx, 1)
	return idx - 1
}

func (b *bitPlaceImpl) Clean() {
	b.f.items = make([]int, b.f.n+1)
}
