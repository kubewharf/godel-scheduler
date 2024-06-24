/*
Copyright 2017 The Kubernetes Authors.

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

	framework "github.com/kubewharf/godel-scheduler/pkg/framework/api"
	utils "github.com/kubewharf/godel-scheduler/pkg/plugins/noderesources"
	"github.com/kubewharf/godel-scheduler/pkg/plugins/podlauncher"
	"github.com/kubewharf/godel-scheduler/pkg/scheduler/apis/config"
	"github.com/kubewharf/godel-scheduler/pkg/scheduler/framework/handle"
	podutil "github.com/kubewharf/godel-scheduler/pkg/util/pod"
)

var (
	_ framework.PreFilterPlugin = &Fit{}
	_ framework.FilterPlugin    = &Fit{}
)

const (
	// FitName is the name of the plugin used in the plugin registry and configurations.
	FitName = "NodeResourcesFit"

	// preFilterStateKey is the key in CycleState to NodeResourcesFit pre-computed data.
	// Using the name of the plugin will likely help us avoid collisions with other plugins.
	preFilterStateKey = "PreFilter" + FitName
)

// preFilterState computed at PreFilter and used at Filter.
type preFilterState struct {
	framework.Resource
	resourceType    podutil.PodResourceType
	ignorePodsLimit bool
	err             error
}

// Clone the prefilter state.
func (s *preFilterState) Clone() framework.StateData {
	return s
}

func (s *preFilterState) getPodRequest() *utils.PodRequest {
	return &utils.PodRequest{
		Resource:        s.Resource,
		ResourceType:    s.resourceType,
		IgnorePodsLimit: s.ignorePodsLimit,
		Err:             s.err,
	}
}

func newPreFilterState(state *utils.PodRequest) *preFilterState {
	return &preFilterState{
		state.Resource,
		state.ResourceType,
		state.IgnorePodsLimit,
		state.Err,
	}
}

// Fit is a plugin that checks if a node has sufficient resources.
type Fit struct {
	ignoredResources      sets.String
	ignoredResourceGroups sets.String
}

// Name returns name of the plugin. It is used in logs, etc.
func (f *Fit) Name() string {
	return FitName
}

func validateFitArgs(args config.NodeResourcesFitArgs) error {
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

// validateLabelName validates that the label name is correctly defined.
func validateLabelName(labelName string, fldPath *field.Path) field.ErrorList {
	allErrs := field.ErrorList{}
	for _, msg := range validation.IsQualifiedName(labelName) {
		allErrs = append(allErrs, field.Invalid(fldPath, labelName, msg))
	}
	return allErrs
}

// NewFit initializes a new plugin and returns it.
func NewFit(plArgs runtime.Object, _ handle.PodFrameworkHandle) (framework.Plugin, error) {
	args, err := getFitArgs(plArgs)
	if err != nil {
		return nil, err
	}

	if err := validateFitArgs(args); err != nil {
		return nil, err
	}

	return &Fit{
		ignoredResources:      sets.NewString(args.IgnoredResources...),
		ignoredResourceGroups: sets.NewString(args.IgnoredResourceGroups...),
	}, nil
}

func getFitArgs(obj runtime.Object) (config.NodeResourcesFitArgs, error) {
	if obj == nil {
		return config.NodeResourcesFitArgs{}, nil
	}
	ptr, ok := obj.(*config.NodeResourcesFitArgs)
	if !ok {
		return config.NodeResourcesFitArgs{}, fmt.Errorf("want args to be of type NodeResourcesFitArgs, got %T", obj)
	}
	return *ptr, nil
}

// PreFilter invoked at the prefilter extension point.
func (f *Fit) PreFilter(_ context.Context, cycleState *framework.CycleState, pod *v1.Pod) *framework.Status {
	state := utils.ComputePodResourceRequest(cycleState, pod)
	cycleState.Write(preFilterStateKey, newPreFilterState(state))
	return nil
}

// PreFilterExtensions returns prefilter extensions, pod add and remove.
func (f *Fit) PreFilterExtensions() framework.PreFilterExtensions {
	return nil
}

func getPreFilterState(cycleState *framework.CycleState) (*preFilterState, error) {
	c, err := cycleState.Read(preFilterStateKey)
	if err != nil {
		// preFilterState doesn't exist, likely PreFilter wasn't invoked.
		return nil, fmt.Errorf("error reading %q from cycleState: %v", preFilterStateKey, err)
	}

	s, ok := c.(*preFilterState)
	if !ok {
		return nil, fmt.Errorf("%+v  convert to NodeResourcesFit.preFilterState error", c)
	}
	return s, nil
}

// Filter invoked at the filter extension point.
// Checks if a node has sufficient resources, such as cpu, memory, gpu, opaque int resources etc to run a pod.
// It returns a list of insufficient resources, if empty, then the node has all the resources requested by the pod.
func (f *Fit) Filter(_ context.Context, cycleState *framework.CycleState, pod *v1.Pod, nodeInfo framework.NodeInfo) *framework.Status {
	s, err := getPreFilterState(cycleState)
	if err != nil {
		return framework.NewStatus(framework.Error, err.Error())
	}
	if s.err != nil {
		return framework.NewStatus(framework.Error, s.err.Error())
	}

	_, status := podlauncher.NodeFits(cycleState, pod, nodeInfo)
	if status != nil {
		return status
	}

	insufficientResources := utils.FitsRequest(s.getPodRequest(), nodeInfo, f.ignoredResources, f.ignoredResourceGroups)

	if len(insufficientResources) != 0 {
		// We will keep all failure reasons.
		failureReasons := make([]string, 0, len(insufficientResources))
		for _, r := range insufficientResources {
			failureReasons = append(failureReasons, r.Reason)
		}
		return framework.NewStatus(framework.Unschedulable, failureReasons...)
	}
	return nil
}
