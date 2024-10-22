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

package noderesources

import (
	"context"
	"fmt"
	"strings"

	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/apimachinery/pkg/util/validation"
	"k8s.io/apimachinery/pkg/util/validation/field"

	"github.com/kubewharf/godel-scheduler/pkg/binder/apis/config"
	"github.com/kubewharf/godel-scheduler/pkg/binder/framework/handle"
	framework "github.com/kubewharf/godel-scheduler/pkg/framework/api"
	utils "github.com/kubewharf/godel-scheduler/pkg/plugins/noderesources"
)

const (
	// ConflictCheckName is the name of the plugin used in the plugin registry and configurations.
	ConflictCheckName = "NodeResourcesCheck"
)

var _ framework.CheckConflictsPlugin = &ConflictCheck{}

// Fit is a plugin that checks if a node has sufficient resources.
type ConflictCheck struct {
	ignoredResources      sets.String
	ignoredResourceGroups sets.String
}

// Name returns name of the plugin. It is used in logs, etc.
func (pl *ConflictCheck) Name() string {
	return ConflictCheckName
}

func (pl *ConflictCheck) CheckConflicts(_ context.Context, state *framework.CycleState, pod *v1.Pod, nodeInfo framework.NodeInfo) *framework.Status {
	resourceRequest := utils.ComputePodResourceRequest(pod)
	if resourceRequest.Err != nil {
		return framework.NewStatus(framework.Error, resourceRequest.Err.Error())
	}

	insufficientResource := utils.FitsRequest(resourceRequest, nodeInfo, nil, nil)

	if insufficientResource != nil {
		return framework.NewStatus(framework.Unschedulable, insufficientResource.Reason)
	}
	return nil
}

func NewConflictCheck(plArgs runtime.Object, _ handle.BinderFrameworkHandle) (framework.Plugin, error) {
	args, err := getFitArgs(plArgs)
	if err != nil {
		return nil, err
	}

	if err := validateFitArgs(args); err != nil {
		return nil, err
	}

	return &ConflictCheck{}, nil
}

func getFitArgs(obj runtime.Object) (config.NodeResourcesCheckArgs, error) {
	if obj == nil {
		return config.NodeResourcesCheckArgs{}, nil
	}
	ptr, ok := obj.(*config.NodeResourcesCheckArgs)
	if !ok {
		return config.NodeResourcesCheckArgs{}, fmt.Errorf("want args to be  of type NodeResourcesCheckArgs, got %T", obj)
	}
	return *ptr, nil
}

// validateLabelName validates that the label name is correctly defined.
func validateLabelName(labelName string, fldPath *field.Path) field.ErrorList {
	allErrs := field.ErrorList{}
	for _, msg := range validation.IsQualifiedName(labelName) {
		allErrs = append(allErrs, field.Invalid(fldPath, labelName, msg))
	}
	return allErrs
}

func validateFitArgs(args config.NodeResourcesCheckArgs) error {
	var allErrs field.ErrorList
	resPath := field.NewPath("ignoredResources")
	for i, res := range args.IgnoredResources {
		path := resPath.Index(i)
		if errs := validateLabelName(res, path); len(errs) != 0 {
			allErrs = append(allErrs, errs...)
		}
	}

	groupPath := field.NewPath("ignoredResourceGroups")
	for i, group := range args.IgnoredResourceGroups {
		path := groupPath.Index(i)
		if strings.Contains(group, "/") {
			allErrs = append(allErrs, field.Invalid(path, group, "resource group name can't contain '/'"))
		}
		if errs := validateLabelName(group, path); len(errs) != 0 {
			allErrs = append(allErrs, errs...)
		}
	}

	if len(allErrs) == 0 {
		return nil
	}
	return allErrs.ToAggregate()
}
