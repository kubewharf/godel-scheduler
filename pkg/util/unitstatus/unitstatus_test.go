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

package status

import (
	"fmt"
	"testing"
)

func TestSchedulingStatus_SchedulingStatusString(t *testing.T) {
	tests := []struct {
		name string
		s    SchedulingStatus
		want string
	}{
		{
			name: "default",
			want: "UnknownStatus",
		},
		{
			name: "UnknownStatus",
			s:    UnknownStatus,
			want: "UnknownStatus",
		},
		{
			name: "PendingStatus",
			s:    PendingStatus,
			want: "PendingStatus",
		},
		{
			name: "ScheduledStatus",
			s:    ScheduledStatus,
			want: "ScheduledStatus",
		},
		{
			name: "TimeoutStatus",
			s:    TimeoutStatus,
			want: "TimeoutStatus",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := fmt.Sprint(tt.s); got != tt.want {
				t.Errorf("fmt.Sprint(tt.s) = %v, want %v", got, tt.want)
			}
			if got := tt.s.String(); got != tt.want {
				t.Errorf("tt.s.String() = %v, want %v", got, tt.want)
			}
		})
	}
}
