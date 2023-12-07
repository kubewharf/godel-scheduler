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
	"fmt"
	"strings"

	"github.com/kubewharf/godel-scheduler/pkg/framework/api"
)

// SchedulingFailure describe the reason for unschedulable Pod.
type SchedulingFailure string

const (
	// UnexpectedError happened during the workflow of Scheduler or Binder.
	UnexpectedError SchedulingFailure = "UnexpectedError"
	// InsufficientResourcesError implies Scheduler doesn't find a schedulable node because of insufficient resources.
	InsufficientResourcesError SchedulingFailure = "InsufficientResources"
	// ConcurrencyConflictsError implies Binder can't resolve conflicts from Scheduler instances.
	ConcurrencyConflictsError SchedulingFailure = "ConcurrencyConflicts"
)

// SchedulingPhase describe the scheduling phase.
type SchedulingPhase string

const (
	Scheduling SchedulingPhase = "Scheduling"
	Binding    SchedulingPhase = "Binding"
)

// UnitSchedulingDetails interpret the scheduling failures for podgroup.
type UnitSchedulingDetails struct {
	phase          SchedulingPhase
	allPods        int
	successfulPods int
	failedPods     int
	// for scheduler: unHandledPods = allPods - successfulPods - failedPods
	// for binder: unHandledPods = allPods - failedPods
	// There are no successful Pods when binder rejects a PodGroupUnit
	unHandledPods int
	// failures records the failure for failed Pods
	failures map[SchedulingFailure]int
	// unitError records the failed error of the unit, it's nil if the unit is successful.
	// It will only be set when assume or applyToCache operation failed.
	unitError error
	// podError records the failed error of every failed Pod
	podError map[string]error
}

// NewUnitSchedulingDetails returns an interpreter instance.
func NewUnitSchedulingDetails(phase SchedulingPhase, allPods int) *UnitSchedulingDetails {
	return &UnitSchedulingDetails{
		phase:   phase,
		allPods: allPods,
		// all pods are initialized to unhandled pods
		unHandledPods: allPods,
		failures:      make(map[SchedulingFailure]int),
		podError:      make(map[string]error),
	}
}

// AddSuccess adds a successful Pod.
func (details *UnitSchedulingDetails) AddSuccess() {
	if details == nil {
		return
	}
	details.successfulPods++
	details.unHandledPods--
}

// RemoveNSuccess removes n Pods from successfulPods to failedPods, and adds its error.
func (details *UnitSchedulingDetails) RemoveNSuccess(n int, err error) {
	if details == nil {
		return
	}

	if n > details.successfulPods {
		n = details.successfulPods
	}

	details.successfulPods -= n
	if err != nil {
		details.failures[details.errorToFailure(err)] += n
	}
}

// SetUnitError sets the failed error of the whole unit.
func (details *UnitSchedulingDetails) SetUnitError(err error) {
	if details == nil {
		return
	}

	details.unitError = err

	// adjust the failedPods and successfulPods
	details.failedPods += details.successfulPods
	details.successfulPods = 0

	// if the whole unit failed due to assume or apply cache operation error,
	// there is no need to record the error for each pod
	details.podError = make(map[string]error)
}

// AddPodError adds a pod scheduling error.
func (details *UnitSchedulingDetails) AddPodError(podKey string, err error) {
	if details == nil || err == nil {
		return
	}

	details.podError[podKey] = err
	details.AddError(err)
}

func (details *UnitSchedulingDetails) GetPodError(podKey string) error {
	if details == nil {
		return nil
	}

	// if the whole unit failed, return the unit error
	if details.unitError != nil {
		return details.unitError
	}

	if err, ok := details.podError[podKey]; ok {
		return err
	}

	return nil
}

// errorToFailure converts error to SchedulingFailure. The caller has to make sure the error is not nil.
func (details *UnitSchedulingDetails) errorToFailure(err error) SchedulingFailure {
	if details == nil {
		return UnexpectedError
	}

	switch err.(type) {
	case api.PreemptionError:
		return InsufficientResourcesError
	case *api.FitError:
		if details.phase == Scheduling {
			return InsufficientResourcesError
		} else {
			return ConcurrencyConflictsError
		}
	default:
		return UnexpectedError
	}
}

// AddError adds a failed Pod, and records failure according to error type.
func (details *UnitSchedulingDetails) AddError(podError error) {
	if details == nil {
		return
	}

	if podError == nil {
		return
	}

	details.failures[details.errorToFailure(podError)]++
	details.failedPods++
	details.unHandledPods--
	return
}

// FailureReason returns scheduling failure reason.
// It describes what kind of failure has happened during the scheduling.
// If all Pods are successful, returns "".
func (details *UnitSchedulingDetails) FailureReason() string {
	if details == nil {
		return ""
	}

	if details.successfulPods == details.allPods {
		return ""
	}

	// all Pods quick failed, it must something internal error.
	if details.unHandledPods == details.allPods {
		return fmt.Sprintf("[%v]: %v", details.phase, UnexpectedError)
	}

	if details.failedPods > 0 {
		var failures []string
		for r := range details.failures {
			failures = append(failures, string(r))
		}

		return fmt.Sprintf("[%v]: %v", details.phase, strings.Join(failures, ", "))
	}

	return ""
}

// FailureMessage returns the detailed failure message about the scheduling.
// If all Pods are successful, returns "".
func (details *UnitSchedulingDetails) FailureMessage() string {
	if details == nil {
		return ""
	}

	var failureDetails []string
	for r, d := range details.failures {
		failureDetails = append(failureDetails, fmt.Sprintf("%v:%d", r, d))
	}

	failureMsg := "[No failure]"
	if msg := strings.Join(failureDetails, ", "); msg != "" {
		failureMsg = "[" + msg + "]"
	}

	return fmt.Sprintf("allPods=%d, unHandledPods=%d, successfulPods=%d, failedPods=%d %s",
		details.allPods, details.unHandledPods, details.successfulPods, details.failedPods, failureMsg)
}

func (details *UnitSchedulingDetails) GetErrors() []error {
	errs := []error{}
	if details.unitError != nil {
		errs = append(errs, details.unitError)
	}

	for podKey, err := range details.podError {
		errs = append(errs, fmt.Errorf("pod: %v, err: %v", podKey, err))
	}
	return errs
}
