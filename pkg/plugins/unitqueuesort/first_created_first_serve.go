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

package unitqueuesort

import (
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/apimachinery/pkg/runtime"

	framework "github.com/kubewharf/godel-scheduler/pkg/framework/api"
	podutil "github.com/kubewharf/godel-scheduler/pkg/util/pod"
)

// FCFSName is the name of the plugin used in the plugin registry and configurations.
// FCFS means first created first serve, which is to sort units by creation timestamp in ascend order.
const FCFSName = "FCFS"

// FCFS is a plugin that implements Priority based sorting.
type FCFS struct{}

var _ framework.UnitQueueSortPlugin = &FCFS{}

// Name returns name of the plugin.
func (p *FCFS) Name() string {
	return FCFSName
}

// Less is the function used by the activeQ heap algorithm to sort pods.
// It sorts pods based on their priority. When priorities are equal, it uses
// QueuedUnitInfo.CreationTimestamp.
func (p *FCFS) Less(uInfo1 *framework.QueuedUnitInfo, uInfo2 *framework.QueuedUnitInfo) bool {
	compareResult := ComparePriorityForDebug(uInfo1.GetAnnotations(), uInfo2.GetAnnotations())
	if compareResult != EQUAL {
		return compareResult == GREATER
	}
	p1, p2 := uInfo1.GetPriority(), uInfo2.GetPriority()
	if p1 != p2 {
		return p1 > p2
	}

	creationTimestamp1, creationTimestamp2 := uInfo1.GetCreationTimestamp(), uInfo2.GetCreationTimestamp()
	if !creationTimestamp1.Equal(creationTimestamp2) {
		return creationTimestamp1.Before(creationTimestamp2)
	}

	if uInfo1.Type() == framework.SinglePodUnitType && uInfo2.Type() == framework.SinglePodUnitType {
		// We can ensure that `len(unitInfo.GetPods()) > 0` in scheduling queue.
		pInfo1, pInfo2 := uInfo1.GetPods()[0], uInfo2.GetPods()[0]

		p1CPURequest := podutil.GetPodRequest(pInfo1.Pod, v1.ResourceCPU, resource.DecimalSI)
		p2CPURequest := podutil.GetPodRequest(pInfo2.Pod, v1.ResourceCPU, resource.DecimalSI)
		if p1CPURequest.MilliValue() != p2CPURequest.MilliValue() {
			return p1CPURequest.MilliValue() > p2CPURequest.MilliValue()
		}

		p1MemoryRequest := podutil.GetPodRequest(pInfo1.Pod, v1.ResourceMemory, resource.BinarySI)
		p2MemoryRequest := podutil.GetPodRequest(pInfo2.Pod, v1.ResourceMemory, resource.BinarySI)
		if p1MemoryRequest.Value() != p2MemoryRequest.Value() {
			return p1MemoryRequest.Value() > p2MemoryRequest.Value()
		}
	}

	return uInfo1.Timestamp.Before(uInfo2.Timestamp)
}

// NewFCFS initializes a new plugin and returns it.
func NewFCFS(_ runtime.Object) (framework.UnitQueueSortPlugin, error) {
	return &FCFS{}, nil
}
