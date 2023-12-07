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

package queue

import (
	"testing"
)

func TestGetScore(t *testing.T) {
	for _, test := range []struct {
		name          string
		priority      int32
		minScore      int64
		attempts      int64
		step          float64
		expectedScore float64
	}{
		{
			name:          "priority is greater than minScore, attempts is 0, step is 5",
			priority:      10,
			minScore:      0,
			attempts:      0,
			step:          5,
			expectedScore: 10,
		},
		{
			name:          "priority is greater than minScore, attempts is 1, step is 5",
			priority:      10,
			minScore:      0,
			attempts:      1,
			step:          5,
			expectedScore: 5,
		},
		{
			name:          "priority is greater than minScore, attempts is 2, step is 5",
			priority:      10,
			minScore:      0,
			attempts:      2,
			step:          5,
			expectedScore: 0,
		},
		{
			name:          "priority is greater than minScore, attempts is 3, step is 5",
			priority:      10,
			minScore:      0,
			attempts:      3,
			step:          5,
			expectedScore: -5,
		},
		{
			name:          "priority is greater than minScore, attempts is 4, step is 5",
			priority:      10,
			minScore:      0,
			attempts:      4,
			step:          5,
			expectedScore: 10,
		},
		{
			name:          "priority is greater than minScore, attempts is 3, step is 3",
			priority:      10,
			minScore:      0,
			attempts:      3,
			step:          3,
			expectedScore: 1,
		},
		{
			name:          "priority is greater than minScore, attempts is 4, step is 3",
			priority:      10,
			minScore:      0,
			attempts:      4,
			step:          3,
			expectedScore: -2,
		},
		{
			name:          "priority is greater than minScore, attempts is 5, step is 3",
			priority:      10,
			minScore:      0,
			attempts:      5,
			step:          3,
			expectedScore: 10,
		},
		{
			name:          "priority is greater than minScore, attempts is 7, step is 3",
			priority:      10,
			minScore:      0,
			attempts:      7,
			step:          3,
			expectedScore: 4,
		},
		{
			name:          "priority is equal to minScore, attempts is 1, step is 3",
			priority:      10,
			minScore:      10,
			attempts:      1,
			step:          3,
			expectedScore: 7,
		},
		{
			name:          "priority is equal to minScore, attempts is 2, step is 3",
			priority:      10,
			minScore:      10,
			attempts:      2,
			step:          3,
			expectedScore: 10,
		},
		{
			name:          "priority is equal to minScore, attempts is 3, step is 3",
			priority:      10,
			minScore:      10,
			attempts:      3,
			step:          3,
			expectedScore: 7,
		},
		{
			name:          "priority is equal to minScore, attempts is 0, step is 3",
			priority:      10,
			minScore:      10,
			attempts:      0,
			step:          3,
			expectedScore: 10,
		},
		{
			name:          "priority is equal to minScore, attempts is 0, step is 0",
			priority:      10,
			minScore:      10,
			attempts:      1,
			step:          3,
			expectedScore: 7,
		},
		{
			name:          "priority is 10, minScore is 0, attempts is 0, step is 2.5",
			priority:      10,
			minScore:      0,
			attempts:      1,
			step:          2.5,
			expectedScore: 7.5,
		},
		{
			name:          "priority is 10, minScore is 0, attempts is 3, step is 4.5",
			priority:      10,
			minScore:      0,
			attempts:      3,
			step:          4.5,
			expectedScore: -3.5,
		},
		{
			name:          "priority is 10, minScore is 0, attempts is 4, step is 4.5",
			priority:      10,
			minScore:      0,
			attempts:      4,
			step:          4.5,
			expectedScore: 10,
		},
		{
			name:          "priority is 10, minScore is 0, attempts is 4, step is 4.5",
			priority:      10,
			minScore:      0,
			attempts:      5,
			step:          4.5,
			expectedScore: 5.5,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			actual := getScore(int64(test.priority), test.minScore, test.attempts, test.step)
			if actual != test.expectedScore {
				t.Errorf("expected %v, got %v", test.expectedScore, actual)
			}
		})
	}
}
