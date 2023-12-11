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

package config

import (
	"fmt"
	"strconv"
	"strings"

	v1 "k8s.io/api/core/v1"
	"k8s.io/klog/v2"

	framework "github.com/kubewharf/godel-scheduler/pkg/framework/api"
	podutil "github.com/kubewharf/godel-scheduler/pkg/util/pod"
)

const (
	Delimiter = ","
	Separator = ":"
)

type Constraint struct {
	PluginName string
	Weight     int64
}

func validatedConstraint(option string) (name string, weight int64, err error) {
	weight = 1
	options := strings.Split(option, Separator)
	size := len(options)
	if size > 2 {
		err = fmt.Errorf("invalid option %v, too many options", option)
		return
	}
	name = strings.TrimSpace(options[0])
	if name == "" {
		err = fmt.Errorf("invalid option %v, plugin name is empty", option)
		return
	}
	if size == 2 {
		weight, err = strconv.ParseInt(options[1], 10, 64)
		if err != nil {
			err = fmt.Errorf("invalid option %v, weight value should be int", option)
			return
		}
		if weight < 1 {
			err = fmt.Errorf("invalid option %v, weight value must be greater than 0", option)
			return
		}
	}
	return
}

// GetConstraints retrieve Constraint and weight value from pod
func GetConstraints(pod *v1.Pod, constraintType string) ([]Constraint, error) {
	podKey := podutil.GetPodKey(pod)

	data, ok := pod.Annotations[constraintType]
	if !ok {
		klog.V(5).InfoS("Pod doesn't defines constraints", "pod", podKey, "constraintType", constraintType)
		return []Constraint{}, nil
	}
	constraintItems := strings.Split(data, Delimiter)
	if len(constraintItems) == 0 {
		klog.V(5).InfoS("Pod defines empty constraints", "pod", podKey, "constraintType", constraintType)
		return []Constraint{}, nil
	}

	constraintList, err := getConstraints(constraintItems)
	if err != nil {
		klog.ErrorS(err, "Get constraints error", "pod", podKey, "constraintType", constraintType)
	}
	return constraintList, err
}

// GetConstraintsFromUnit retrieve Constraint and weight value from ScheduleUnit
func GetConstraintsFromUnit(unit framework.ScheduleUnit, constraintType string) ([]Constraint, error) {
	unitKey := unit.GetKey()

	data, ok := unit.GetAnnotations()[constraintType]
	if !ok {
		klog.V(5).InfoS("Unit doesn't defines constraints", "unit", unitKey, "constraintType", constraintType)
		return []Constraint{}, nil
	}
	constraintItems := strings.Split(data, Delimiter)
	if len(constraintItems) == 0 {
		klog.V(5).InfoS("Unit defines empty constraints", "uni", unitKey, "constraintType", constraintType)
		return []Constraint{}, nil
	}

	constraintList, err := getConstraints(constraintItems)
	if err != nil {
		klog.ErrorS(err, "Get constraints error", "unit", unitKey, "constraintType", constraintType)
	}
	return constraintList, err
}

// getConstraints parses Constraint from string constraint.
func getConstraints(constraintItems []string) ([]Constraint, error) {
	constraintsIndex := map[string]int{}
	constraintList := make([]Constraint, 0)
	// if constraints duplicates, use the last one and last one in the order
	for _, item := range constraintItems {
		name, weight, err := validatedConstraint(item)
		if err != nil {
			err = fmt.Errorf("failed to parse constraint %s", err)
			return []Constraint{}, err
		}
		constraint := Constraint{
			name,
			weight,
		}
		if index, exists := constraintsIndex[name]; exists {
			constraintList[index] = constraint
		} else {
			constraintsIndex[name] = len(constraintList)
			constraintList = append(constraintList, constraint)
		}
	}

	return constraintList, nil
}
