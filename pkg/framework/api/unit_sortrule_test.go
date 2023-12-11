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

import "testing"

func TestSortRuleValid(t *testing.T) {
	testCases := []struct {
		name  string
		rule  *SortRule
		valid bool
	}{
		{
			name: "valid sort rule",
			rule: &SortRule{
				Resource:  CPUResource,
				Dimension: Capacity,
				Order:     AscendingOrder,
			},
			valid: true,
		},
		{
			name: "invalid resource",
			rule: &SortRule{
				Resource:  "a100",
				Dimension: Capacity,
				Order:     AscendingOrder,
			},
			valid: false,
		},
		{
			name: "invalid order",
			rule: &SortRule{
				Resource:  MemoryResource,
				Dimension: Capacity,
				Order:     "inc",
			},
			valid: false,
		},
		{
			name: "invalid dimension",
			rule: &SortRule{
				Resource:  MemoryResource,
				Dimension: "cap",
				Order:     "inc",
			},
			valid: false,
		},
	}

	for i := range testCases {
		tc := &testCases[i]
		t.Run(tc.name, func(t *testing.T) {
			if tc.valid != tc.rule.Valid() {
				t.Fatal("wrong sort rule")
			}
		})
	}
}
