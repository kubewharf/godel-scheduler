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

package unitqueuesort

import (
	"testing"
	"time"

	framework "github.com/kubewharf/godel-scheduler/pkg/framework/api"
)

func TestEarliestCreationTimestamp_Less(t *testing.T) {
	earlistSort := &FCFS{}
	lowPriority, highPriority := int32(10), int32(100)
	lowPriorityClass, highPriorityClass := "low", "high"
	t1 := time.Now()
	t2 := t1.Add(time.Second)
	for _, tt := range []struct {
		name     string
		u1       *framework.QueuedUnitInfo
		u2       *framework.QueuedUnitInfo
		expected bool
	}{
		{
			name:     "u1.priority less than u2.priority",
			u1:       createUnit(t1, lowPriorityClass, lowPriority),
			u2:       createUnit(t1, highPriorityClass, highPriority),
			expected: false, // u2 should be ahead of u1 in the queue
		},
		{
			name:     "u1.priority greater than u2.priority",
			u1:       createUnit(t1, highPriorityClass, highPriority),
			u2:       createUnit(t1, lowPriorityClass, lowPriority),
			expected: true, // u1 should be ahead of u2 in the queue
		},
		{
			name:     "empty priority class and same priority score. u1 is created earlier than u2",
			u1:       createUnit(t1, "", lowPriority),
			u2:       createUnit(t2, "", lowPriority),
			expected: true, // u1 should be ahead of u2 in the queue
		},
		{
			name:     "equal priority. u1 is created earlier than u2",
			u1:       createUnit(t1, lowPriorityClass, lowPriority),
			u2:       createUnit(t2, lowPriorityClass, lowPriority),
			expected: true, // u1 should be ahead of u2 in the queue
		},
		{
			name:     "equal priority. u2 is created earlier than u1",
			u1:       createUnit(t2, lowPriorityClass, lowPriority),
			u2:       createUnit(t1, lowPriorityClass, lowPriority),
			expected: false, // u2 should be ahead of u1 in the queue
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if got := earlistSort.Less(tt.u1, tt.u2); got != tt.expected {
				t.Errorf("expected %v, got %v", tt.expected, got)
			}
		})
	}
}
