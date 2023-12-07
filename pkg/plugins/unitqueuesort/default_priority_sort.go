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

const (
	// Name is the name of the plugin used in the plugin registry and configurations.
	Name = "DefaultUnitQueueSort"
)

// DefaultUnitQueueSort is a plugin that implements Priority based sorting.
type DefaultUnitQueueSort struct{}

var _ framework.UnitQueueSortPlugin = &DefaultUnitQueueSort{}

// Name returns name of the plugin.
func (p DefaultUnitQueueSort) Name() string {
	return Name
}

// Less is the function used by the activeQ heap algorithm to sort pods.
// It sorts pods based on their priority. When priorities are equal, it uses
// PodQueueInfo.timestamp.
func (p DefaultUnitQueueSort) Less(uInfo1 *framework.QueuedUnitInfo, uInfo2 *framework.QueuedUnitInfo) bool {
	compareResult := ComparePriorityForDebug(uInfo1.GetAnnotations(), uInfo2.GetAnnotations())
	if compareResult != EQUAL {
		return compareResult == GREATER
	}
	score1, score2 := uInfo1.QueuePriorityScore, uInfo2.QueuePriorityScore
	if score1 != score2 {
		return score1 > score2
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

// New initializes a new plugin and returns it.
func New(_ runtime.Object) (framework.UnitQueueSortPlugin, error) {
	return &DefaultUnitQueueSort{}, nil
}
