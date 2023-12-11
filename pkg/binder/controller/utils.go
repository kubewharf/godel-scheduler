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

package controller

import (
	schedulingv1a1 "github.com/kubewharf/godel-scheduler-api/pkg/apis/scheduling/v1alpha1"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	corelister "k8s.io/client-go/listers/core/v1"

	podAnnotations "github.com/kubewharf/godel-scheduler/pkg/util/pod"
	podutil "github.com/kubewharf/godel-scheduler/pkg/util/pod"
)

// getPodGroupNameFromAnnotation get pod group name from pod group annotation key
func getPodGroupNameFromAnnotation(pod *v1.Pod) string {
	return pod.Annotations[podAnnotations.PodGroupNameAnnotationKey]
}

func getPodGroupConditionByPhase(pg *schedulingv1a1.PodGroup, phase schedulingv1a1.PodGroupPhase) (exist bool, condition schedulingv1a1.PodGroupCondition) {
	for _, condition := range pg.Status.Conditions {
		if condition.Phase == phase {
			return true, condition
		}
	}
	return false, schedulingv1a1.PodGroupCondition{}
}

func GetPodGroupScheduleStartTime(pg *schedulingv1a1.PodGroup) metav1.Time {
	if pg.Status.ScheduleStartTime != nil {
		return *pg.Status.ScheduleStartTime
	}

	if exist, condition := getPodGroupConditionByPhase(pg, schedulingv1a1.PodGroupPreScheduling); exist {
		return condition.LastTransitionTime
	}

	if exist, condition := getPodGroupConditionByPhase(pg, schedulingv1a1.PodGroupPending); exist {
		return condition.LastTransitionTime
	}

	return metav1.Time{}
}

func GetPodGroupConditionTimeStampByPhase(pg *schedulingv1a1.PodGroup, phase schedulingv1a1.PodGroupPhase) metav1.Time {
	if len(phase) == 0 {
		return pg.CreationTimestamp
	}

	if exist, condition := getPodGroupConditionByPhase(pg, phase); exist {
		return condition.LastTransitionTime
	}
	return metav1.Time{}
}

func GetAllPods(podLister corelister.PodLister, pgNamespace, pgName string) ([]*v1.Pod, error) {
	selector := labels.Set(map[string]string{
		podutil.PodGroupNameAnnotationKey: pgName,
	}).AsSelector()
	pods, err := podLister.Pods(pgNamespace).List(selector)

	return pods, err
}
