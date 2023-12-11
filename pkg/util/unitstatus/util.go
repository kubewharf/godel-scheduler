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
	schedulingv1a1 "github.com/kubewharf/godel-scheduler-api/pkg/apis/scheduling/v1alpha1"
	v1 "k8s.io/api/core/v1"
)

func ParsePodGroupSchedulingStatus(pg *schedulingv1a1.PodGroup) SchedulingStatus {
	// status is UnknownStatus by default
	var status SchedulingStatus
	switch pg.Status.Phase {
	case schedulingv1a1.PodGroupPending, schedulingv1a1.PodGroupPreScheduling:
		status = PendingStatus
	case schedulingv1a1.PodGroupScheduled, schedulingv1a1.PodGroupRunning, schedulingv1a1.PodGroupFinished, schedulingv1a1.PodGroupFailed:
		status = ScheduledStatus
	case schedulingv1a1.PodGroupTimeout:
		status = TimeoutStatus
	case schedulingv1a1.PodGroupUnknown:
		status = UnknownStatus
	}
	return status
}

func ParseSinglePodSchedulingStatus(pod *v1.Pod) SchedulingStatus {
	// status is UnknownStatus by default
	var status SchedulingStatus
	switch pod.Status.Phase {
	case v1.PodPending:
		status = PendingStatus
	case v1.PodRunning, v1.PodSucceeded, v1.PodFailed:
		status = ScheduledStatus
	case v1.PodUnknown:
		status = UnknownStatus
	}
	return status
}
