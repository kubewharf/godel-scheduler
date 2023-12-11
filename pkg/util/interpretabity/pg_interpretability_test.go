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
	"reflect"
	"testing"

	"github.com/kubewharf/godel-scheduler/pkg/framework/api"
)

func TestSchedulingFailureInterpreter(t *testing.T) {
	allMember := 5

	type args struct {
		errors []error
		phase  SchedulingPhase
	}

	tests := []struct {
		name               string
		args               args
		wantSuccessfulPods int
		wantFailedPods     int
		wantUnHandledPods  int
		wantFailures       map[SchedulingFailure]int
	}{
		{
			name: "scheduling",
			args: args{
				errors: []error{errors.New("internal error"), &api.FitError{}, api.NewPreemptionError("", 0, api.NewStatus(api.Unschedulable)), nil, nil},
				phase:  Scheduling,
			},
			wantFailedPods:     3,
			wantSuccessfulPods: 2,
			wantUnHandledPods:  0,
			wantFailures: map[SchedulingFailure]int{
				UnexpectedError:            1,
				InsufficientResourcesError: 2,
			},
		},
		{
			name: "binding",
			args: args{
				errors: []error{errors.New("internal error"), &api.FitError{}, api.NewPreemptionError("", 0, api.NewStatus(api.Error)), nil, nil},
				phase:  Binding,
			},
			wantFailedPods:     3,
			wantSuccessfulPods: 2,
			wantUnHandledPods:  0,
			wantFailures: map[SchedulingFailure]int{
				UnexpectedError:            1,
				ConcurrencyConflictsError:  1,
				InsufficientResourcesError: 1,
			},
		},
		{
			name: "all quickfail",
			args: args{
				errors: []error{},
				phase:  Binding,
			},
			wantFailedPods:     0,
			wantSuccessfulPods: 0,
			wantUnHandledPods:  5,
			wantFailures:       map[SchedulingFailure]int{},
		},
		{
			name: "all success",
			args: args{
				errors: []error{nil, nil, nil, nil, nil},
				phase:  Binding,
			},
			wantFailedPods:     0,
			wantSuccessfulPods: 5,
			wantUnHandledPods:  0,
			wantFailures:       map[SchedulingFailure]int{},
		},
		{
			name: "quickfail",
			args: args{
				errors: []error{errors.New("internal error"), nil, nil},
				phase:  Binding,
			},
			wantFailedPods:     1,
			wantSuccessfulPods: 2,
			wantUnHandledPods:  2,
			wantFailures: map[SchedulingFailure]int{
				UnexpectedError: 1,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			details := NewUnitSchedulingDetails(tt.args.phase, allMember)
			for _, err := range tt.args.errors {
				if err == nil {
					details.AddSuccess()
				} else {
					details.AddError(err)
				}
			}

			if details.failedPods != tt.wantFailedPods {
				t.Errorf("wrong failedPods. got:%d, want:%d;", details.failedPods, tt.wantFailedPods)
			}

			if details.successfulPods != tt.wantSuccessfulPods {
				t.Errorf("wrong successfulPods. got:%d, want:%d;", details.successfulPods, tt.wantSuccessfulPods)
			}

			if details.unHandledPods != tt.wantUnHandledPods {
				t.Errorf("wrong quickfailPods. got:%d, want:%d;", details.unHandledPods, tt.wantUnHandledPods)
			}

			if !reflect.DeepEqual(details.failures, tt.wantFailures) {
				t.Errorf("wrong failures. got: %+v, want: %+v", details.failures, tt.wantFailures)
			}
		})
	}
}
