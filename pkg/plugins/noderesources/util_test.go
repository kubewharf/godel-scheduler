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
	"fmt"
	"reflect"
	"testing"

	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	framework "github.com/kubewharf/godel-scheduler/pkg/framework/api"
	v1helper "github.com/kubewharf/godel-scheduler/pkg/util/helper"
	podutil "github.com/kubewharf/godel-scheduler/pkg/util/pod"
)

var (
	extendedResourceA     = v1.ResourceName("example.com/aaa")
	extendedResourceB     = v1.ResourceName("example.com/bbb")
	kubernetesIOResourceA = v1.ResourceName("kubernetes.io/something")
	kubernetesIOResourceB = v1.ResourceName("subdomain.kubernetes.io/something")
	hugePageResourceA     = v1helper.HugePageResourceName(resource.MustParse("2Mi"))
)

func makeResources(milliCPU, memory, pods, extendedA, storage, hugePageA int64) v1.NodeResources {
	return v1.NodeResources{
		Capacity: v1.ResourceList{
			v1.ResourceCPU:              *resource.NewMilliQuantity(milliCPU, resource.DecimalSI),
			v1.ResourceMemory:           *resource.NewQuantity(memory, resource.BinarySI),
			v1.ResourcePods:             *resource.NewQuantity(pods, resource.DecimalSI),
			extendedResourceA:           *resource.NewQuantity(extendedA, resource.DecimalSI),
			v1.ResourceEphemeralStorage: *resource.NewQuantity(storage, resource.BinarySI),
			hugePageResourceA:           *resource.NewQuantity(hugePageA, resource.BinarySI),
		},
	}
}

func makeAllocatableResources(milliCPU, memory, pods, extendedA, storage, hugePageA int64) v1.ResourceList {
	return v1.ResourceList{
		v1.ResourceCPU:              *resource.NewMilliQuantity(milliCPU, resource.DecimalSI),
		v1.ResourceMemory:           *resource.NewQuantity(memory, resource.BinarySI),
		v1.ResourcePods:             *resource.NewQuantity(pods, resource.DecimalSI),
		extendedResourceA:           *resource.NewQuantity(extendedA, resource.DecimalSI),
		v1.ResourceEphemeralStorage: *resource.NewQuantity(storage, resource.BinarySI),
		hugePageResourceA:           *resource.NewQuantity(hugePageA, resource.BinarySI),
	}
}

func newResourcePod(usage ...framework.Resource) *v1.Pod {
	containers := []v1.Container{}
	for _, req := range usage {
		containers = append(containers, v1.Container{
			Resources: v1.ResourceRequirements{Requests: req.ResourceList()},
		})
	}
	return &v1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Annotations: map[string]string{
				string(podutil.PodLauncherAnnotationKey):     string(podutil.Kubelet),
				string(podutil.PodResourceTypeAnnotationKey): string(podutil.GuaranteedPod),
			},
		},
		Spec: v1.PodSpec{
			Containers: containers,
		},
	}
}

func newResourceInitPod(pod *v1.Pod, usage ...framework.Resource) *v1.Pod {
	pod.Spec.InitContainers = newResourcePod(usage...).Spec.Containers
	return pod
}

func newResourceOverheadPod(pod *v1.Pod, overhead v1.ResourceList) *v1.Pod {
	pod.Spec.Overhead = overhead
	return pod
}

func getErrReason(rn v1.ResourceName) string {
	return fmt.Sprintf("Insufficient %v", rn)
}

func TestEnoughRequests(t *testing.T) {
	enoughPodsTests := []struct {
		pod                       *v1.Pod
		nodeInfo                  framework.NodeInfo
		name                      string
		wantInsufficientResources []InsufficientResource
		wantStatus                *framework.Status
	}{
		{
			pod: newResourcePod(),
			nodeInfo: framework.NewNodeInfo(
				newResourcePod(framework.Resource{MilliCPU: 10, Memory: 20})),
			name:                      "no resources requested always fits",
			wantInsufficientResources: []InsufficientResource{},
		},
		{
			pod: newResourcePod(framework.Resource{MilliCPU: 1, Memory: 1}),
			nodeInfo: framework.NewNodeInfo(
				newResourcePod(framework.Resource{MilliCPU: 10, Memory: 20})),
			name:                      "too many resources fails",
			wantStatus:                framework.NewStatus(framework.Unschedulable, getErrReason(v1.ResourceCPU), getErrReason(v1.ResourceMemory)),
			wantInsufficientResources: []InsufficientResource{{v1.ResourceCPU, getErrReason(v1.ResourceCPU), 1, 10, 10}, {v1.ResourceMemory, getErrReason(v1.ResourceMemory), 1, 20, 20}},
		},
		{
			pod: newResourceInitPod(newResourcePod(framework.Resource{MilliCPU: 1, Memory: 1}), framework.Resource{MilliCPU: 3, Memory: 1}),
			nodeInfo: framework.NewNodeInfo(
				newResourcePod(framework.Resource{MilliCPU: 8, Memory: 19})),
			name:                      "too many resources fails due to init container cpu",
			wantStatus:                framework.NewStatus(framework.Unschedulable, getErrReason(v1.ResourceCPU)),
			wantInsufficientResources: []InsufficientResource{{v1.ResourceCPU, getErrReason(v1.ResourceCPU), 3, 8, 10}},
		},
		{
			pod: newResourceInitPod(newResourcePod(framework.Resource{MilliCPU: 1, Memory: 1}), framework.Resource{MilliCPU: 3, Memory: 1}, framework.Resource{MilliCPU: 2, Memory: 1}),
			nodeInfo: framework.NewNodeInfo(
				newResourcePod(framework.Resource{MilliCPU: 8, Memory: 19})),
			name:                      "too many resources fails due to highest init container cpu",
			wantStatus:                framework.NewStatus(framework.Unschedulable, getErrReason(v1.ResourceCPU)),
			wantInsufficientResources: []InsufficientResource{{v1.ResourceCPU, getErrReason(v1.ResourceCPU), 3, 8, 10}},
		},
		{
			pod: newResourceInitPod(newResourcePod(framework.Resource{MilliCPU: 1, Memory: 1}), framework.Resource{MilliCPU: 1, Memory: 3}),
			nodeInfo: framework.NewNodeInfo(
				newResourcePod(framework.Resource{MilliCPU: 9, Memory: 19})),
			name:                      "too many resources fails due to init container memory",
			wantStatus:                framework.NewStatus(framework.Unschedulable, getErrReason(v1.ResourceMemory)),
			wantInsufficientResources: []InsufficientResource{{v1.ResourceMemory, getErrReason(v1.ResourceMemory), 3, 19, 20}},
		},
		{
			pod: newResourceInitPod(newResourcePod(framework.Resource{MilliCPU: 1, Memory: 1}), framework.Resource{MilliCPU: 1, Memory: 3}, framework.Resource{MilliCPU: 1, Memory: 2}),
			nodeInfo: framework.NewNodeInfo(
				newResourcePod(framework.Resource{MilliCPU: 9, Memory: 19})),
			name:                      "too many resources fails due to highest init container memory",
			wantStatus:                framework.NewStatus(framework.Unschedulable, getErrReason(v1.ResourceMemory)),
			wantInsufficientResources: []InsufficientResource{{v1.ResourceMemory, getErrReason(v1.ResourceMemory), 3, 19, 20}},
		},
		{
			pod: newResourceInitPod(newResourcePod(framework.Resource{MilliCPU: 1, Memory: 1}), framework.Resource{MilliCPU: 1, Memory: 1}),
			nodeInfo: framework.NewNodeInfo(
				newResourcePod(framework.Resource{MilliCPU: 9, Memory: 19})),
			name:                      "init container fits because it's the max, not sum, of containers and init containers",
			wantInsufficientResources: []InsufficientResource{},
		},
		{
			pod: newResourceInitPod(newResourcePod(framework.Resource{MilliCPU: 1, Memory: 1}), framework.Resource{MilliCPU: 1, Memory: 1}, framework.Resource{MilliCPU: 1, Memory: 1}),
			nodeInfo: framework.NewNodeInfo(
				newResourcePod(framework.Resource{MilliCPU: 9, Memory: 19})),
			name:                      "multiple init containers fit because it's the max, not sum, of containers and init containers",
			wantInsufficientResources: []InsufficientResource{},
		},
		{
			pod: newResourcePod(framework.Resource{MilliCPU: 1, Memory: 1}),
			nodeInfo: framework.NewNodeInfo(
				newResourcePod(framework.Resource{MilliCPU: 5, Memory: 5})),
			name:                      "both resources fit",
			wantInsufficientResources: []InsufficientResource{},
		},
		{
			pod: newResourcePod(framework.Resource{MilliCPU: 2, Memory: 1}),
			nodeInfo: framework.NewNodeInfo(
				newResourcePod(framework.Resource{MilliCPU: 9, Memory: 5})),
			name:                      "one resource memory fits",
			wantStatus:                framework.NewStatus(framework.Unschedulable, getErrReason(v1.ResourceCPU)),
			wantInsufficientResources: []InsufficientResource{{v1.ResourceCPU, getErrReason(v1.ResourceCPU), 2, 9, 10}},
		},
		{
			pod: newResourcePod(framework.Resource{MilliCPU: 1, Memory: 2}),
			nodeInfo: framework.NewNodeInfo(
				newResourcePod(framework.Resource{MilliCPU: 5, Memory: 19})),
			name:                      "one resource cpu fits",
			wantStatus:                framework.NewStatus(framework.Unschedulable, getErrReason(v1.ResourceMemory)),
			wantInsufficientResources: []InsufficientResource{{v1.ResourceMemory, getErrReason(v1.ResourceMemory), 2, 19, 20}},
		},
		{
			pod: newResourcePod(framework.Resource{MilliCPU: 5, Memory: 1}),
			nodeInfo: framework.NewNodeInfo(
				newResourcePod(framework.Resource{MilliCPU: 5, Memory: 19})),
			name:                      "equal edge case",
			wantInsufficientResources: []InsufficientResource{},
		},
		{
			pod: newResourceInitPod(newResourcePod(framework.Resource{MilliCPU: 4, Memory: 1}), framework.Resource{MilliCPU: 5, Memory: 1}),
			nodeInfo: framework.NewNodeInfo(
				newResourcePod(framework.Resource{MilliCPU: 5, Memory: 19})),
			name:                      "equal edge case for init container",
			wantInsufficientResources: []InsufficientResource{},
		},
		{
			pod:                       newResourcePod(framework.Resource{ScalarResources: map[v1.ResourceName]int64{extendedResourceA: 1}}),
			nodeInfo:                  framework.NewNodeInfo(newResourcePod(framework.Resource{})),
			name:                      "extended resource fits",
			wantInsufficientResources: []InsufficientResource{},
		},
		{
			pod:                       newResourceInitPod(newResourcePod(framework.Resource{}), framework.Resource{ScalarResources: map[v1.ResourceName]int64{extendedResourceA: 1}}),
			nodeInfo:                  framework.NewNodeInfo(newResourcePod(framework.Resource{})),
			name:                      "extended resource fits for init container",
			wantInsufficientResources: []InsufficientResource{},
		},
		{
			pod: newResourcePod(
				framework.Resource{MilliCPU: 1, Memory: 1, ScalarResources: map[v1.ResourceName]int64{extendedResourceA: 10}}),
			nodeInfo: framework.NewNodeInfo(
				newResourcePod(framework.Resource{MilliCPU: 0, Memory: 0, ScalarResources: map[v1.ResourceName]int64{extendedResourceA: 0}})),
			name:                      "extended resource capacity enforced",
			wantStatus:                framework.NewStatus(framework.Unschedulable, getErrReason(extendedResourceA)),
			wantInsufficientResources: []InsufficientResource{{extendedResourceA, getErrReason(extendedResourceA), 10, 0, 5}},
		},
		{
			pod: newResourceInitPod(newResourcePod(framework.Resource{}),
				framework.Resource{MilliCPU: 1, Memory: 1, ScalarResources: map[v1.ResourceName]int64{extendedResourceA: 10}}),
			nodeInfo: framework.NewNodeInfo(
				newResourcePod(framework.Resource{MilliCPU: 0, Memory: 0, ScalarResources: map[v1.ResourceName]int64{extendedResourceA: 0}})),
			name:                      "extended resource capacity enforced for init container",
			wantStatus:                framework.NewStatus(framework.Unschedulable, getErrReason(extendedResourceA)),
			wantInsufficientResources: []InsufficientResource{{extendedResourceA, getErrReason(extendedResourceA), 10, 0, 5}},
		},
		{
			pod: newResourcePod(
				framework.Resource{MilliCPU: 1, Memory: 1, ScalarResources: map[v1.ResourceName]int64{extendedResourceA: 1}}),
			nodeInfo: framework.NewNodeInfo(
				newResourcePod(framework.Resource{MilliCPU: 0, Memory: 0, ScalarResources: map[v1.ResourceName]int64{extendedResourceA: 5}})),
			name:                      "extended resource allocatable enforced",
			wantStatus:                framework.NewStatus(framework.Unschedulable, getErrReason(extendedResourceA)),
			wantInsufficientResources: []InsufficientResource{{extendedResourceA, getErrReason(extendedResourceA), 1, 5, 5}},
		},
		{
			pod: newResourceInitPod(newResourcePod(framework.Resource{}),
				framework.Resource{MilliCPU: 1, Memory: 1, ScalarResources: map[v1.ResourceName]int64{extendedResourceA: 1}}),
			nodeInfo: framework.NewNodeInfo(
				newResourcePod(framework.Resource{MilliCPU: 0, Memory: 0, ScalarResources: map[v1.ResourceName]int64{extendedResourceA: 5}})),
			name:                      "extended resource allocatable enforced for init container",
			wantStatus:                framework.NewStatus(framework.Unschedulable, getErrReason(extendedResourceA)),
			wantInsufficientResources: []InsufficientResource{{extendedResourceA, getErrReason(extendedResourceA), 1, 5, 5}},
		},
		{
			pod: newResourcePod(
				framework.Resource{MilliCPU: 1, Memory: 1, ScalarResources: map[v1.ResourceName]int64{extendedResourceA: 3}},
				framework.Resource{MilliCPU: 1, Memory: 1, ScalarResources: map[v1.ResourceName]int64{extendedResourceA: 3}}),
			nodeInfo: framework.NewNodeInfo(
				newResourcePod(framework.Resource{MilliCPU: 0, Memory: 0, ScalarResources: map[v1.ResourceName]int64{extendedResourceA: 2}})),
			name:                      "extended resource allocatable enforced for multiple containers",
			wantStatus:                framework.NewStatus(framework.Unschedulable, getErrReason(extendedResourceA)),
			wantInsufficientResources: []InsufficientResource{{extendedResourceA, getErrReason(extendedResourceA), 6, 2, 5}},
		},
		{
			pod: newResourceInitPod(newResourcePod(framework.Resource{}),
				framework.Resource{MilliCPU: 1, Memory: 1, ScalarResources: map[v1.ResourceName]int64{extendedResourceA: 3}},
				framework.Resource{MilliCPU: 1, Memory: 1, ScalarResources: map[v1.ResourceName]int64{extendedResourceA: 3}}),
			nodeInfo: framework.NewNodeInfo(
				newResourcePod(framework.Resource{MilliCPU: 0, Memory: 0, ScalarResources: map[v1.ResourceName]int64{extendedResourceA: 2}})),
			name:                      "extended resource allocatable admits multiple init containers",
			wantInsufficientResources: []InsufficientResource{},
		},
		{
			pod: newResourceInitPod(newResourcePod(framework.Resource{}),
				framework.Resource{MilliCPU: 1, Memory: 1, ScalarResources: map[v1.ResourceName]int64{extendedResourceA: 6}},
				framework.Resource{MilliCPU: 1, Memory: 1, ScalarResources: map[v1.ResourceName]int64{extendedResourceA: 3}}),
			nodeInfo: framework.NewNodeInfo(
				newResourcePod(framework.Resource{MilliCPU: 0, Memory: 0, ScalarResources: map[v1.ResourceName]int64{extendedResourceA: 2}})),
			name:                      "extended resource allocatable enforced for multiple init containers",
			wantStatus:                framework.NewStatus(framework.Unschedulable, getErrReason(extendedResourceA)),
			wantInsufficientResources: []InsufficientResource{{extendedResourceA, getErrReason(extendedResourceA), 6, 2, 5}},
		},
		{
			pod: newResourcePod(
				framework.Resource{MilliCPU: 1, Memory: 1, ScalarResources: map[v1.ResourceName]int64{extendedResourceB: 1}}),
			nodeInfo: framework.NewNodeInfo(
				newResourcePod(framework.Resource{MilliCPU: 0, Memory: 0})),
			name:                      "extended resource allocatable enforced for unknown resource",
			wantStatus:                framework.NewStatus(framework.Unschedulable, getErrReason(extendedResourceB)),
			wantInsufficientResources: []InsufficientResource{{extendedResourceB, getErrReason(extendedResourceB), 1, 0, 0}},
		},
		{
			pod: newResourceInitPod(newResourcePod(framework.Resource{}),
				framework.Resource{MilliCPU: 1, Memory: 1, ScalarResources: map[v1.ResourceName]int64{extendedResourceB: 1}}),
			nodeInfo: framework.NewNodeInfo(
				newResourcePod(framework.Resource{MilliCPU: 0, Memory: 0})),
			name:                      "extended resource allocatable enforced for unknown resource for init container",
			wantStatus:                framework.NewStatus(framework.Unschedulable, getErrReason(extendedResourceB)),
			wantInsufficientResources: []InsufficientResource{{extendedResourceB, getErrReason(extendedResourceB), 1, 0, 0}},
		},
		{
			pod: newResourcePod(
				framework.Resource{MilliCPU: 1, Memory: 1, ScalarResources: map[v1.ResourceName]int64{kubernetesIOResourceA: 10}}),
			nodeInfo: framework.NewNodeInfo(
				newResourcePod(framework.Resource{MilliCPU: 0, Memory: 0})),
			name:                      "kubernetes.io resource capacity enforced",
			wantStatus:                framework.NewStatus(framework.Unschedulable, getErrReason(kubernetesIOResourceA)),
			wantInsufficientResources: []InsufficientResource{{kubernetesIOResourceA, getErrReason(kubernetesIOResourceA), 10, 0, 0}},
		},
		{
			pod: newResourceInitPod(newResourcePod(framework.Resource{}),
				framework.Resource{MilliCPU: 1, Memory: 1, ScalarResources: map[v1.ResourceName]int64{kubernetesIOResourceB: 10}}),
			nodeInfo: framework.NewNodeInfo(
				newResourcePod(framework.Resource{MilliCPU: 0, Memory: 0})),
			name:                      "kubernetes.io resource capacity enforced for init container",
			wantStatus:                framework.NewStatus(framework.Unschedulable, getErrReason(kubernetesIOResourceB)),
			wantInsufficientResources: []InsufficientResource{{kubernetesIOResourceB, getErrReason(kubernetesIOResourceB), 10, 0, 0}},
		},
		{
			pod: newResourcePod(
				framework.Resource{MilliCPU: 1, Memory: 1, ScalarResources: map[v1.ResourceName]int64{hugePageResourceA: 10}}),
			nodeInfo: framework.NewNodeInfo(
				newResourcePod(framework.Resource{MilliCPU: 0, Memory: 0, ScalarResources: map[v1.ResourceName]int64{hugePageResourceA: 0}})),
			name:                      "hugepages resource capacity enforced",
			wantStatus:                framework.NewStatus(framework.Unschedulable, getErrReason(hugePageResourceA)),
			wantInsufficientResources: []InsufficientResource{{hugePageResourceA, getErrReason(hugePageResourceA), 10, 0, 5}},
		},
		{
			pod: newResourceInitPod(newResourcePod(framework.Resource{}),
				framework.Resource{MilliCPU: 1, Memory: 1, ScalarResources: map[v1.ResourceName]int64{hugePageResourceA: 10}}),
			nodeInfo: framework.NewNodeInfo(
				newResourcePod(framework.Resource{MilliCPU: 0, Memory: 0, ScalarResources: map[v1.ResourceName]int64{hugePageResourceA: 0}})),
			name:                      "hugepages resource capacity enforced for init container",
			wantStatus:                framework.NewStatus(framework.Unschedulable, getErrReason(hugePageResourceA)),
			wantInsufficientResources: []InsufficientResource{{hugePageResourceA, getErrReason(hugePageResourceA), 10, 0, 5}},
		},
		{
			pod: newResourcePod(
				framework.Resource{MilliCPU: 1, Memory: 1, ScalarResources: map[v1.ResourceName]int64{hugePageResourceA: 3}},
				framework.Resource{MilliCPU: 1, Memory: 1, ScalarResources: map[v1.ResourceName]int64{hugePageResourceA: 3}}),
			nodeInfo: framework.NewNodeInfo(
				newResourcePod(framework.Resource{MilliCPU: 0, Memory: 0, ScalarResources: map[v1.ResourceName]int64{hugePageResourceA: 2}})),
			name:                      "hugepages resource allocatable enforced for multiple containers",
			wantStatus:                framework.NewStatus(framework.Unschedulable, getErrReason(hugePageResourceA)),
			wantInsufficientResources: []InsufficientResource{{hugePageResourceA, getErrReason(hugePageResourceA), 6, 2, 5}},
		},
		{
			pod: newResourceOverheadPod(
				newResourcePod(framework.Resource{MilliCPU: 1, Memory: 1}),
				v1.ResourceList{v1.ResourceCPU: resource.MustParse("3m"), v1.ResourceMemory: resource.MustParse("13")},
			),
			nodeInfo:                  framework.NewNodeInfo(newResourcePod(framework.Resource{MilliCPU: 5, Memory: 5})),
			name:                      "resources + pod overhead fits",
			wantInsufficientResources: []InsufficientResource{},
		},
		{
			pod: newResourceOverheadPod(
				newResourcePod(framework.Resource{MilliCPU: 1, Memory: 1}),
				v1.ResourceList{v1.ResourceCPU: resource.MustParse("1m"), v1.ResourceMemory: resource.MustParse("15")},
			),
			nodeInfo:                  framework.NewNodeInfo(newResourcePod(framework.Resource{MilliCPU: 5, Memory: 5})),
			name:                      "requests + overhead does not fit for memory",
			wantStatus:                framework.NewStatus(framework.Unschedulable, getErrReason(v1.ResourceMemory)),
			wantInsufficientResources: []InsufficientResource{{v1.ResourceMemory, getErrReason(v1.ResourceMemory), 16, 5, 20}},
		},
		{
			pod: newResourcePod(
				framework.Resource{
					MilliCPU: 1,
					Memory:   1,
					ScalarResources: map[v1.ResourceName]int64{
						kubernetesIOResourceA: 1,
					},
				}),
			nodeInfo:   framework.NewNodeInfo(newResourcePod(framework.Resource{MilliCPU: 0, Memory: 0})),
			name:       "skip checking ignored extended resource via resource groups",
			wantStatus: framework.NewStatus(framework.Unschedulable, fmt.Sprintf("Insufficient %v", kubernetesIOResourceA)),
			wantInsufficientResources: []InsufficientResource{
				{
					ResourceName: kubernetesIOResourceA,
					Reason:       fmt.Sprintf("Insufficient %v", kubernetesIOResourceA),
					Requested:    1,
					Used:         0,
					Capacity:     0,
				},
			},
		},
	}

	for _, test := range enoughPodsTests {
		t.Run(test.name, func(t *testing.T) {
			node := v1.Node{Status: v1.NodeStatus{Capacity: makeResources(10, 20, 32, 5, 20, 5).Capacity, Allocatable: makeAllocatableResources(10, 20, 32, 5, 20, 5)}}
			test.nodeInfo.SetNode(&node)

			cycleState := framework.NewCycleState()
			framework.SetPodResourceTypeState(podutil.GuaranteedPod, cycleState)
			gotInsufficientResources := FitsRequest(ComputePodResourceRequest(cycleState, test.pod), test.nodeInfo, nil, nil)
			if !reflect.DeepEqual(gotInsufficientResources, test.wantInsufficientResources) {
				t.Errorf("insufficient resources do not match: %+v, want: %v", gotInsufficientResources, test.wantInsufficientResources)
			}
		})
	}
}

func TestNotEnoughRequests(t *testing.T) {
	notEnoughPodsTests := []struct {
		pod        *v1.Pod
		nodeInfo   framework.NodeInfo
		fits       bool
		name       string
		wantStatus *framework.Status
	}{
		{
			pod:        newResourcePod(),
			nodeInfo:   framework.NewNodeInfo(newResourcePod(framework.Resource{MilliCPU: 10, Memory: 20})),
			name:       "even without specified resources predicate fails when there's no space for additional pod",
			wantStatus: framework.NewStatus(framework.Unschedulable, "Too many pods"),
		},
		{
			pod:        newResourcePod(framework.Resource{MilliCPU: 1, Memory: 1}),
			nodeInfo:   framework.NewNodeInfo(newResourcePod(framework.Resource{MilliCPU: 5, Memory: 5})),
			name:       "even if both resources fit predicate fails when there's no space for additional pod",
			wantStatus: framework.NewStatus(framework.Unschedulable, "Too many pods"),
		},
		{
			pod:        newResourcePod(framework.Resource{MilliCPU: 5, Memory: 1}),
			nodeInfo:   framework.NewNodeInfo(newResourcePod(framework.Resource{MilliCPU: 5, Memory: 19})),
			name:       "even for equal edge case predicate fails when there's no space for additional pod",
			wantStatus: framework.NewStatus(framework.Unschedulable, "Too many pods"),
		},
		{
			pod:        newResourceInitPod(newResourcePod(framework.Resource{MilliCPU: 5, Memory: 1}), framework.Resource{MilliCPU: 5, Memory: 1}),
			nodeInfo:   framework.NewNodeInfo(newResourcePod(framework.Resource{MilliCPU: 5, Memory: 19})),
			name:       "even for equal edge case predicate fails when there's no space for additional pod due to init container",
			wantStatus: framework.NewStatus(framework.Unschedulable, "Too many pods"),
		},
	}
	for _, test := range notEnoughPodsTests {
		t.Run(test.name, func(t *testing.T) {
			node := v1.Node{Status: v1.NodeStatus{Capacity: v1.ResourceList{}, Allocatable: makeAllocatableResources(10, 20, 1, 0, 0, 0)}}
			test.nodeInfo.SetNode(&node)

			cycleState := framework.NewCycleState()
			framework.SetPodResourceTypeState(podutil.GuaranteedPod, cycleState)
			gotInsufficientResources := FitsRequest(ComputePodResourceRequest(cycleState, test.pod), test.nodeInfo, nil, nil)
			gotStatus := getStatus(gotInsufficientResources)
			if !reflect.DeepEqual(gotStatus, test.wantStatus) {
				t.Errorf("insufficient resources do not match: %+v, want: %v", gotInsufficientResources, test.wantStatus)
			}
		})
	}
}

func getStatus(gotInsufficientResources []InsufficientResource) (gotStatus *framework.Status) {
	if len(gotInsufficientResources) != 0 {
		// We will keep all failure reasons.
		failureReasons := make([]string, 0, len(gotInsufficientResources))
		for _, r := range gotInsufficientResources {
			failureReasons = append(failureReasons, r.Reason)
		}
		gotStatus = framework.NewStatus(framework.Unschedulable, failureReasons...)
	}
	return
}

func TestStorageRequests(t *testing.T) {
	storagePodsTests := []struct {
		pod        *v1.Pod
		nodeInfo   framework.NodeInfo
		name       string
		wantStatus *framework.Status
	}{
		{
			pod: newResourcePod(framework.Resource{MilliCPU: 1, Memory: 1}),
			nodeInfo: framework.NewNodeInfo(
				newResourcePod(framework.Resource{MilliCPU: 10, Memory: 10})),
			name:       "due to container scratch disk",
			wantStatus: framework.NewStatus(framework.Unschedulable, getErrReason(v1.ResourceCPU)),
		},
		{
			pod: newResourcePod(framework.Resource{MilliCPU: 1, Memory: 1}),
			nodeInfo: framework.NewNodeInfo(
				newResourcePod(framework.Resource{MilliCPU: 2, Memory: 10})),
			name: "pod fit",
		},
		{
			pod: newResourcePod(framework.Resource{EphemeralStorage: 25}),
			nodeInfo: framework.NewNodeInfo(
				newResourcePod(framework.Resource{MilliCPU: 2, Memory: 2})),
			name:       "storage ephemeral local storage request exceeds allocatable",
			wantStatus: framework.NewStatus(framework.Unschedulable, getErrReason(v1.ResourceEphemeralStorage)),
		},
		{
			pod: newResourcePod(framework.Resource{EphemeralStorage: 10}),
			nodeInfo: framework.NewNodeInfo(
				newResourcePod(framework.Resource{MilliCPU: 2, Memory: 2})),
			name: "pod fits",
		},
	}

	for _, test := range storagePodsTests {
		t.Run(test.name, func(t *testing.T) {
			node := v1.Node{Status: v1.NodeStatus{Capacity: makeResources(10, 20, 32, 5, 20, 5).Capacity, Allocatable: makeAllocatableResources(10, 20, 32, 5, 20, 5)}}
			test.nodeInfo.SetNode(&node)

			cycleState := framework.NewCycleState()
			framework.SetPodResourceTypeState(podutil.GuaranteedPod, cycleState)
			gotInsufficientResources := FitsRequest(ComputePodResourceRequest(cycleState, test.pod), test.nodeInfo, nil, nil)
			gotStatus := getStatus(gotInsufficientResources)
			if !reflect.DeepEqual(gotStatus, test.wantStatus) {
				t.Errorf("insufficient resources do not match: %+v, want: %v", gotInsufficientResources, test.wantStatus)
			}
		})
	}
}
