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

package interpretabity

import (
	"errors"
	"testing"

	"github.com/kubewharf/godel-scheduler/pkg/framework/api"
)

func TestSchedulingFailureInterpreter(t *testing.T) {
	allMember := 5

	type args struct {
		errors map[string]error
		phase  SchedulingPhase
	}

	tests := []struct {
		name               string
		args               args
		wantSuccessfulPods int
		wantFailedPods     int
		wantUnHandledPods  int
	}{
		{
			name: "scheduling",
			args: args{
				errors: map[string]error{
					"pod1": errors.New("internal error"),
					"pod2": &api.FitError{},
					"pod3": api.NewPreemptionError("", 0, api.NewStatus(api.Unschedulable)),
					"pod4": nil,
					"pod5": nil,
				},
				phase: Scheduling,
			},
			wantFailedPods:     3,
			wantSuccessfulPods: 2,
			wantUnHandledPods:  0,
		},

		{
			name: "all quickfail",
			args: args{
				errors: map[string]error{},
				phase:  Scheduling,
			},
			wantFailedPods:     0,
			wantSuccessfulPods: 0,
			wantUnHandledPods:  5,
		},
		{
			name: "all success",
			args: args{
				errors: map[string]error{
					"pod1": nil,
					"pod2": nil,
					"pod3": nil,
					"pod4": nil,
					"pod5": nil,
				},
				phase: Scheduling,
			},
			wantFailedPods:     0,
			wantSuccessfulPods: 5,
			wantUnHandledPods:  0,
		},
		{
			name: "quickfail",
			args: args{
				errors: map[string]error{
					"pod1": errors.New("internal error"),
					"pod2": nil,
					"pod3": nil,
				},
				phase: Scheduling,
			},
			wantFailedPods:     1,
			wantSuccessfulPods: 2,
			wantUnHandledPods:  2,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			details := NewUnitSchedulingDetails(tt.args.phase, allMember)
			for podKey, err := range tt.args.errors {
				if err == nil {
					details.AddSuccessfulPods(podKey)
				} else {
					details.AddPodsError(err, podKey)
				}
			}

			successfulPods := len(details.successfulPods)
			failedPods := len(details.podError)
			unHandledPods := details.allPods - successfulPods - failedPods

			if failedPods != tt.wantFailedPods {
				t.Errorf("wrong failedPods. got:%d, want:%d;", failedPods, tt.wantFailedPods)
			}

			if successfulPods != tt.wantSuccessfulPods {
				t.Errorf("wrong successfulPods. got:%d, want:%d;", successfulPods, tt.wantSuccessfulPods)
			}

			if unHandledPods != tt.wantUnHandledPods {
				t.Errorf("wrong quickfailPods. got:%d, want:%d;", unHandledPods, tt.wantUnHandledPods)
			}
		})
	}
}
