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
	"time"

	framework "github.com/kubewharf/godel-scheduler/pkg/framework/api"
	"github.com/kubewharf/godel-scheduler/pkg/scheduler/apis/config"
	loadawarestore "github.com/kubewharf/godel-scheduler/pkg/scheduler/cache/commonstores/load_aware_store"
	"github.com/kubewharf/godel-scheduler/pkg/scheduler/framework/handle"
	"github.com/kubewharf/godel-scheduler/pkg/util"
	podutil "github.com/kubewharf/godel-scheduler/pkg/util/pod"

	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/sets"
)

const (
	NodeMetricEstimatorName = "nodeMetricEstimator"

	DefaultNodeMetricExpirationSeconds = 30 // TODO: revisit this.
)

type NodeMetricEstimator struct {
	// resources indicates the resource names estimator interested in
	resources map[podutil.PodResourceType]sets.String

	filterExpiredNodeMetrics    bool
	nodeMetricExpirationSeconds int64
	usageThresholds             map[v1.ResourceName]int64

	// EstimatedScalingFactors indicates the factor when estimating resource usage.
	// Is CPU scaling factor is 80, estimated CPU = 80 / 100 * request.cpu
	estimatedScalingFactors map[v1.ResourceName]int64

	handle       handle.PodFrameworkHandle
	pluginHandle loadawarestore.StoreHandle
}

func NewNodeMetricEstimator(args *config.LoadAwareArgs, handle handle.PodFrameworkHandle) (Estimator, error) {
	resources := make(map[podutil.PodResourceType]sets.String)
	for _, res := range args.Resources {
		resourceSet := resources[res.ResourceType]
		if resourceSet == nil {
			resourceSet = sets.NewString()
		}
		resourceSet.Insert(res.Name)
		resources[res.ResourceType] = resourceSet
	}

	if args.NodeMetricExpirationSeconds < 0 {
		return nil, fmt.Errorf("invalid negative NodeMetricExpirationSeconds")
	} else if args.NodeMetricExpirationSeconds == 0 {
		args.NodeMetricExpirationSeconds = DefaultNodeMetricExpirationSeconds
	}

	var pluginHandle loadawarestore.StoreHandle
	if ins := handle.FindStore(loadawarestore.Name); ins != nil {
		pluginHandle = ins.(loadawarestore.StoreHandle)
	}

	return &NodeMetricEstimator{
		resources: resources,

		filterExpiredNodeMetrics:    args.FilterExpiredNodeMetrics,
		nodeMetricExpirationSeconds: args.NodeMetricExpirationSeconds,
		usageThresholds:             args.UsageThresholds,

		estimatedScalingFactors: args.EstimatedScalingFactors,

		handle:       handle,
		pluginHandle: pluginHandle,
	}, nil
}

func (e *NodeMetricEstimator) Name() string {
	return NodeMetricEstimatorName
}

func (e *NodeMetricEstimator) ValidateNode(nodeInfo framework.NodeInfo, resourceType podutil.PodResourceType) *framework.Status {
	nodeMetricInfo := e.pluginHandle.GetLoadAwareNodeMetricInfo(nodeInfo.GetNodeName(), resourceType)
	if nodeMetricInfo == nil {
		// TODO: revisit the policy.
		return framework.NewStatus(framework.Error, fmt.Sprintf("no metric info on %v", nodeInfo.GetNodeName()))
	}

	if e.filterExpiredNodeMetrics {
		boundaryTime := metav1.NewTime(time.Now().Add(time.Duration(-e.nodeMetricExpirationSeconds * int64(time.Second))))
		if nodeMetricInfo.UpdateTime.Before(&boundaryTime) {
			return framework.NewStatus(framework.UnschedulableAndUnresolvable, "NodeMetricInfo has expired and cannot be used")
		}
	}

	if len(e.usageThresholds) > 0 {
		allocatable := framework.NewResource(nil)
		switch resourceType {
		case podutil.GuaranteedPod:
			allocatable = nodeInfo.GetGuaranteedAllocatable()
		case podutil.BestEffortPod:
			allocatable = nodeInfo.GetBestEffortAllocatable()
		}
		for resourceName, threshold := range e.usageThresholds {
			// TODO: support more resources.
			var usage, total int64
			switch resourceName {
			case v1.ResourceCPU:
				usage = nodeMetricInfo.ProfileMilliCPUUsage
				total = allocatable.MilliCPU
			case v1.ResourceMemory:
				usage = nodeMetricInfo.ProfileMEMUsage
				total = allocatable.Memory
			}
			if float64(usage) > float64(threshold)/100.0*float64(total) {
				// Because we only judge utilization based on metric information, preemption is useless.
				return framework.NewStatus(framework.UnschedulableAndUnresolvable, "NodeMetricInfo usage exceeds threshold")
			}
		}
	}

	return nil
}

func (e *NodeMetricEstimator) EstimatePod(pod *v1.Pod) (*framework.Resource, error) {
	resourceType, err := podutil.GetPodResourceType(pod)
	if err != nil {
		return nil, err
	}
	requests, _ := PodRequestsAndLimits(pod)
	return framework.NewResource(e.scalingResource(requests, resourceType)), nil
}

func (e *NodeMetricEstimator) EstimateNode(nodeInfo framework.NodeInfo, resourceType podutil.PodResourceType) (*framework.Resource, error) {
	nodeUsage := e.pluginHandle.GetLoadAwareNodeUsage(nodeInfo.GetNodeName(), resourceType)
	var estimatedResource *framework.Resource
	{
		estimatedResource = framework.NewResource(e.scalingResource(v1.ResourceList{
			v1.ResourceCPU:    *resource.NewMilliQuantity(nodeUsage.RequestMilliCPU, resource.DecimalSI),
			v1.ResourceMemory: *resource.NewQuantity(nodeUsage.RequestMEM, resource.BinarySI),
		}, resourceType))
	}
	{
		estimatedResource.MilliCPU += nodeUsage.ProfileMilliCPU
		estimatedResource.Memory += nodeUsage.ProfileMEM
	}
	return estimatedResource, nil
}

// ------------------------------ internal function ------------------------------

func (e *NodeMetricEstimator) scalingResource(resource v1.ResourceList, resourceType podutil.PodResourceType) v1.ResourceList {
	ret := make(v1.ResourceList)
	// only consider interested resources
	for resourceName := range e.resources[resourceType] {
		v := resource[v1.ResourceName(resourceName)]
		if v.IsZero() {
			// TODO: revisit this.
			if q := util.GetNonZeroQuantityForResource(v1.ResourceName(resourceName), resource); q != nil {
				v = *q
			}
		}

		// Data correction based on scaling factors.
		if factor, ok := e.estimatedScalingFactors[v1.ResourceName(resourceName)]; ok {
			switch v1.ResourceName(resourceName) {
			case v1.ResourceCPU:
				milliValue := int64(float64(v.MilliValue()) * float64(factor) / 100.0)
				v.SetMilli(milliValue)
			case v1.ResourceMemory:
				value := int64(float64(v.Value()) * float64(factor) / 100.0)
				v.Set(value)
			}
		}

		ret[v1.ResourceName(resourceName)] = v
	}
	return ret
}
