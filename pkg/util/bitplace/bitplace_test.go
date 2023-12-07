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

import (
	"testing"
)

func TestBitPlace(t *testing.T) {
	obj := New(1e5)
	ops := []string{"alloc", "alloc", "free", "alloc", "alloc", "alloc", "alloc", "free"}
	wants := []int{0, 1, 1, 1, 2, 3, 4, 4}

	if len(ops) != len(wants) {
		t.Errorf("len(ops) != len(got)\n")
	}

	for i := 0; i < len(ops); i++ {
		op, want := ops[i], wants[i]
		switch op {
		case "alloc":
			got := obj.Alloc()
			if want != got {
				t.Errorf("Unexpect result, want: %v, got: %v\n", want, got)
			}
		case "free":
			obj.Free(want)
		default:
			t.Errorf("Invalid op: %v\n", op)
		}
	}
}
