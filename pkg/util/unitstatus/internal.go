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

package status

import (
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
)

// unitSchedulingStatusMap only be used in this package
type unitSchedulingStatusMap map[string]SchedulingStatus

func (m unitSchedulingStatusMap) get(unitKey string) SchedulingStatus {
	status, ok := m[unitKey]
	if !ok {
		return UnknownStatus
	}
	return status
}

func (m unitSchedulingStatusMap) set(unitKey string, status SchedulingStatus) {
	m[unitKey] = status
}

func (m unitSchedulingStatusMap) delete(unitKey string) {
	delete(m, unitKey)
}

// PodSet key:Pod.UID value:Pod
type PodSet map[types.UID]*v1.Pod

// unitPodSetMap key:UnitKey value:PodSet
type unitPodSetMap map[string]PodSet

func (m unitPodSetMap) addUnitPods(unitKey string, pods ...*v1.Pod) {
	if _, ok := m[unitKey]; !ok {
		m[unitKey] = make(PodSet)
	}
	podSet := m[unitKey]
	for i := range pods {
		podSet[pods[i].UID] = pods[i]
	}
}

func (m unitPodSetMap) getUnitPods(unitKey string) (pods []*v1.Pod) {
	pods = []*v1.Pod{}
	if _, ok := m[unitKey]; !ok {
		return
	}
	podSet := m[unitKey]
	for i := range podSet {
		pods = append(pods, podSet[i])
	}
	return
}

func (m unitPodSetMap) deleteUnitPods(unitKey string, pods ...*v1.Pod) {
	if _, ok := m[unitKey]; !ok {
		return
	}
	podSet := m[unitKey]
	for i := range pods {
		delete(podSet, pods[i].UID)
	}
	if len(podSet) == 0 {
		delete(m, unitKey)
	}
}
