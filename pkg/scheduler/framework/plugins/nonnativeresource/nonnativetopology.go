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

package nonnativeresource

import (
	"context"
	"fmt"
	"sync"

	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/apimachinery/pkg/runtime"
	utilfeature "k8s.io/apiserver/pkg/util/feature"
	listerv1 "k8s.io/client-go/listers/core/v1"

	godelfeatures "github.com/kubewharf/godel-scheduler/pkg/features"
	framework "github.com/kubewharf/godel-scheduler/pkg/framework/api"
	"github.com/kubewharf/godel-scheduler/pkg/plugins/nonnativeresource"
	"github.com/kubewharf/godel-scheduler/pkg/scheduler/framework/handle"
	podutil "github.com/kubewharf/godel-scheduler/pkg/util/pod"
)

const (
	NonNativeTopologyName     = "NonNativeTopology"
	topologyPreFilterStateKey = "PreFilter" + NonNativeTopologyName
)

type topologyPreFilterState struct {
	resourcesRequests map[string]*resource.Quantity
	mu                sync.RWMutex
}

// Clone the prefilter state.
func (s *topologyPreFilterState) Clone() framework.StateData {
	return s
}

func newTopologyPreFilterState(resourcesRequests map[string]*resource.Quantity) *topologyPreFilterState {
	return &topologyPreFilterState{
		resourcesRequests: resourcesRequests,
	}
}

func getTopologyPreFilterState(cycleState *framework.CycleState) (*topologyPreFilterState, error) {
	c, err := cycleState.Read(topologyPreFilterStateKey)
	if err != nil {
		// preFilterState doesn't exist, likely PreFilter wasn't invoked.
		return nil, fmt.Errorf("error reading %q from cycleState: %v", topologyPreFilterStateKey, err)
	}

	s, ok := c.(*topologyPreFilterState)
	if !ok {
		return nil, fmt.Errorf("%+v convert to NonNativeTopology.preFilterState error", c)
	}
	return s, nil
}

type NonNativeTopology struct {
	podLister listerv1.PodLister
}

var (
	_ framework.FilterPlugin    = &NonNativeTopology{}
	_ framework.PreFilterPlugin = &NonNativeTopology{}
)

func NewNonNativeTopology(_ runtime.Object, handle handle.PodFrameworkHandle) (framework.Plugin, error) {
	informerFactory := handle.SharedInformerFactory()
	podLister := informerFactory.Core().V1().Pods().Lister()
	return &NonNativeTopology{
		podLister: podLister,
	}, nil
}

func (nonnative *NonNativeTopology) Name() string {
	return NonNativeTopologyName
}

func (nonnative *NonNativeTopology) PreFilter(_ context.Context, cycleState *framework.CycleState, pod *v1.Pod) *framework.Status {
	if !utilfeature.DefaultFeatureGate.Enabled(godelfeatures.NonNativeResourceSchedulingSupport) {
		return framework.NewStatus(framework.Error, fmt.Sprintf("featuregate %s is disabled", godelfeatures.NonNativeResourceSchedulingSupport))
	}
	// Get all resources requests
	resourcesRequests := podutil.GetPodRequests(pod)
	cycleState.Write(topologyPreFilterStateKey, newTopologyPreFilterState(resourcesRequests))
	return nil
}

// PreFilterExtensions returns prefilter extensions, pod add and remove.
func (nonnative *NonNativeTopology) PreFilterExtensions() framework.PreFilterExtensions {
	return nil
}

func (nonnative *NonNativeTopology) Filter(ctx context.Context, cycleState *framework.CycleState, pod *v1.Pod, nodeInfo framework.NodeInfo) *framework.Status {
	if !utilfeature.DefaultFeatureGate.Enabled(godelfeatures.NonNativeResourceSchedulingSupport) {
		return framework.NewStatus(framework.Error, fmt.Sprintf("featuregate %s is disabled", godelfeatures.NonNativeResourceSchedulingSupport))
	}

	resourceType, err := framework.GetPodResourceType(cycleState)
	if err != nil {
		return framework.NewStatus(framework.Error, "failed to get resource type from state")
	}

	s, err := getTopologyPreFilterState(cycleState)
	if err != nil {
		return framework.NewStatus(framework.Error, err.Error())
	}
	resourcesRequests := s.resourcesRequests
	return nonnativeresource.FeasibleNonNativeTopology(pod, resourceType, resourcesRequests, nodeInfo, nonnative.podLister)
}
