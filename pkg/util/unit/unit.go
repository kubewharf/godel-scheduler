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

package unit

import (
	"time"

	"github.com/kubewharf/godel-scheduler-api/pkg/apis/scheduling/v1alpha1"
	v1 "k8s.io/api/core/v1"

	framework "github.com/kubewharf/godel-scheduler/pkg/framework/api"
	podAnnotations "github.com/kubewharf/godel-scheduler/pkg/util/pod"
)

const (
	PodGroupFinalOpLock = "godel.bytedance.com/podgroup-final-op-lock"
)

// TODO: move to util package
// GetPodGroupName return pod group name
func GetPodGroupName(pod *v1.Pod) string {
	pgName := pod.GetAnnotations()[podAnnotations.PodGroupNameAnnotationKey]
	if len(pgName) != 0 {
		return pgName
	}

	return ""
}

// GetPodGroupFullName get namespaced group name from pod annotations
func GetPodGroupFullName(pod *v1.Pod) string {
	pgName := GetPodGroupName(pod)
	if len(pgName) == 0 {
		return ""
	}

	return pod.Namespace + "/" + pgName
}

func GetPodGroupKey(podGroup *v1alpha1.PodGroup) string {
	return podGroup.Namespace + "/" + podGroup.Name
}

func GetWaitTimeDuration(scheduleTimeout time.Duration, podGroup *v1alpha1.PodGroup) time.Duration {
	if podGroup != nil && podGroup.Spec.ScheduleTimeoutSeconds != nil {
		return time.Duration(*podGroup.Spec.ScheduleTimeoutSeconds) * time.Second
	}

	return scheduleTimeout
}

func GetUnitKeyFromPodGroup(key string) string {
	return string(framework.PodGroupUnitType) + "/" + key
}

func GetUnitKeyFromPod(pod *v1.Pod) string {
	return GetUnitKeyFromPodGroup(GetPodGroupFullName(pod))
}

// PopulatePodGroupFinalOp fills the annotation of the PodGroup to implement a reentrant lock and
// determines whether the locking is successful by the return value.
// Note: After calling this function, the corresponding `Update` operation MUST be executed.
//
// Use specific fields `PodGroupFinalOpLock` as ReentrantLock in the podgroup annotation to avoid
// exceptions caused by the binder and controller operating the podgroup at the same time.
func PopulatePodGroupFinalOp(pg *v1alpha1.PodGroup, op string) bool {
	if pg == nil {
		return false
	}
	if pg.Annotations == nil {
		// TODO: return false directly?
		// return false
		pg.Annotations = make(map[string]string)
	}
	if operator, ok := pg.Annotations[PodGroupFinalOpLock]; ok {
		return operator == op
	}
	pg.Annotations[PodGroupFinalOpLock] = op
	return true
}

func PodGroupFinalOp(pg *v1alpha1.PodGroup) string {
	if pg != nil && pg.Annotations != nil {
		return pg.Annotations[PodGroupFinalOpLock]
	}
	return ""
}

func PodGroupFinalState(p v1alpha1.PodGroupPhase) bool {
	return p == v1alpha1.PodGroupScheduled || p == v1alpha1.PodGroupTimeout
}
