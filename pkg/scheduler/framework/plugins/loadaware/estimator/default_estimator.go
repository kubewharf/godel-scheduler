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

package estimator

import (
	"fmt"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/sets"

	framework "github.com/kubewharf/godel-scheduler/pkg/framework/api"
	"github.com/kubewharf/godel-scheduler/pkg/scheduler/apis/config"
	"github.com/kubewharf/godel-scheduler/pkg/scheduler/framework/handle"
	"github.com/kubewharf/godel-scheduler/pkg/util"
	podutil "github.com/kubewharf/godel-scheduler/pkg/util/pod"
)

const (
	DefaultEstimatorName = "defaultEstimator"

	// DefaultMilliCPURequest defines default milli cpu request number.
	DefaultMilliCPURequest int64 = 250 // 0.25 core
	// DefaultMemoryRequest defines default memory request size.
	DefaultMemoryRequest int64 = 200 * 1024 * 1024 // 200 MiB
)

type DefaultEstimator struct {
	// resources indicates the resource names estimator interested in
	resources map[podutil.PodResourceType]sets.String
}

func NewDefaultEstimator(args *config.LoadAwareArgs, handle handle.PodFrameworkHandle) (Estimator, error) {
	resources := make(map[podutil.PodResourceType]sets.String)
	for _, res := range args.Resources {
		resourceSet := resources[res.ResourceType]
		if resourceSet == nil {
			resourceSet = sets.NewString()
		}
		resourceSet.Insert(res.Name)
		resources[res.ResourceType] = resourceSet
	}
	return &DefaultEstimator{
		resources: resources,
	}, nil
}

func (e *DefaultEstimator) Name() string {
	return DefaultEstimatorName
}

func (e *DefaultEstimator) ValidateNode(_ framework.NodeInfo, _ podutil.PodResourceType) *framework.Status {
	return nil
}

func (e *DefaultEstimator) EstimatePod(pod *corev1.Pod) (*framework.Resource, error) {
	resourceType, err := podutil.GetPodResourceType(pod)
	if err != nil {
		return nil, err
	}
	requests, _ := PodRequestsAndLimits(pod)
	resourceList := make(corev1.ResourceList)

	// only consider interested resources
	for resourceName := range e.resources[resourceType] {
		v := requests[corev1.ResourceName(resourceName)]
		if v.IsZero() {
			if q := util.GetNonZeroQuantityForResource(corev1.ResourceName(resourceName), requests); q != nil {
				v = *q
			}
		}
		resourceList[corev1.ResourceName(resourceName)] = v
	}

	return framework.NewResource(resourceList), nil
}

func (e *DefaultEstimator) EstimateNode(nodeInfo framework.NodeInfo, resourceType podutil.PodResourceType) (*framework.Resource, error) {
	switch resourceType {
	case podutil.GuaranteedPod:
		return nodeInfo.GetGuaranteedNonZeroRequested(), nil
	case podutil.BestEffortPod:
		return nodeInfo.GetBestEffortNonZeroRequested(), nil
	default:
		return nil, fmt.Errorf("invalid resource typr %v", resourceType)
	}
}
