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

	"k8s.io/apimachinery/pkg/util/sets"

	"github.com/kubewharf/godel-scheduler/pkg/framework/api"
)

// SchedulingFailureCategory describe the reason for unschedulable Pod.
type SchedulingFailureCategory string

const (
	// UnexpectedError happened during the workflow of Scheduler or Binder.
	UnexpectedError SchedulingFailureCategory = "UnexpectedError"
	// InsufficientResourcesError implies Scheduler doesn't find a schedulable node because of insufficient resources.
	InsufficientResourcesError SchedulingFailureCategory = "InsufficientResources"
)

// SchedulingPhase describe the scheduling phase.
type SchedulingPhase string

const (
	Scheduling SchedulingPhase = "Scheduling"
)

// UnitSchedulingDetails interpret the scheduling category for podgroup.
type UnitSchedulingDetails struct {
	phase          SchedulingPhase
	allPods        int
	successfulPods sets.String
	// podError records the failed error of every failed Pod
	podError map[string]error
}

// NewUnitSchedulingDetails returns a interpreter instance.
func NewUnitSchedulingDetails(phase SchedulingPhase, allPods int) *UnitSchedulingDetails {
	return &UnitSchedulingDetails{
		phase:          phase,
		allPods:        allPods,
		successfulPods: sets.NewString(),
		podError:       make(map[string]error),
	}
}

func (details *UnitSchedulingDetails) AddSuccessfulPods(podKey ...string) {
	if details == nil {
		return
	}
	details.successfulPods.Insert(podKey...)
}

func (details *UnitSchedulingDetails) AddPodsError(err error, podKey ...string) {
	if details == nil {
		return
	}
	if err == nil {
		return
	}

	for _, key := range podKey {
		details.podError[key] = err
	}

	details.successfulPods.Delete(podKey...)
}

func (details *UnitSchedulingDetails) GetPodError(podKey string) error {
	if details == nil {
		return nil
	}

	if err, ok := details.podError[podKey]; ok {
		return err
	}

	return nil
}

// errorToFailureCategory converts error to SchedulingFailureCategory. The caller has to make sure the error is not nil.
func errorToFailureCategory(err error) SchedulingFailureCategory {
	if err == nil {
		return UnexpectedError
	}

	switch err.(type) {
	case api.PreemptionError:
		return InsufficientResourcesError
	case *api.FitError:
		return InsufficientResourcesError
	default:
		return UnexpectedError
	}
}

func (details *UnitSchedulingDetails) aggregatePodErrors() map[SchedulingFailureCategory]int {
	if details == nil {
		return nil
	}

	category := make(map[SchedulingFailureCategory]int)
	for _, err := range details.podError {
		category[errorToFailureCategory(err)]++
	}
	return category
}

// FailureMessage returns the detailed failure message about the scheduling.
// If all Pods are successful, returns "".
func (details *UnitSchedulingDetails) FailureMessage() string {
	if details == nil {
		return ""
	}

	successfulPods := len(details.successfulPods)
	failedPods := len(details.podError)
	unHandledPods := details.allPods - successfulPods - failedPods

	return fmt.Sprintf("allPods=%d, unHandledPods=%d, successfulPods=%d, failedPods=%d",
		details.allPods, unHandledPods, successfulPods, failedPods)
}

func (details *UnitSchedulingDetails) GetErrors() []error {
	errs := []error{}
	for podKey, err := range details.podError {
		errs = append(errs, fmt.Errorf("pod: %v, err: %v", podKey, err))
	}
	return errs
}
