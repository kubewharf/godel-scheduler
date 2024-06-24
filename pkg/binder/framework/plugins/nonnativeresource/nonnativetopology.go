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

	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	listerv1 "k8s.io/client-go/listers/core/v1"

	"github.com/kubewharf/godel-scheduler/pkg/binder/framework/handle"
	framework "github.com/kubewharf/godel-scheduler/pkg/framework/api"
	"github.com/kubewharf/godel-scheduler/pkg/plugins/nonnativeresource"
	podutil "github.com/kubewharf/godel-scheduler/pkg/util/pod"
)

const (
	Name = "NonNativeTopology"
)

type NonNativeTopology struct {
	podLister listerv1.PodLister
}

var _ framework.CheckConflictsPlugin = &NonNativeTopology{}

func New(_ runtime.Object, handle handle.BinderFrameworkHandle) (framework.Plugin, error) {
	informerFactory := handle.SharedInformerFactory()
	podLister := informerFactory.Core().V1().Pods().Lister()
	return &NonNativeTopology{
		podLister: podLister,
	}, nil
}

func (nonnative *NonNativeTopology) Name() string {
	return Name
}

func (nonnative *NonNativeTopology) CheckConflicts(_ context.Context, cycleState *framework.CycleState, pod *v1.Pod, nodeInfo framework.NodeInfo) *framework.Status {
	resourceType, err := framework.GetPodResourceType(cycleState)
	if err != nil {
		return framework.NewStatus(framework.Error, "failed to get resource type from state")
	}

	// Get all resources requests
	resourcesRequests := podutil.GetPodRequests(pod)

	return nonnativeresource.FeasibleNonNativeTopology(pod, resourceType, resourcesRequests, nodeInfo, nonnative.podLister)
}
