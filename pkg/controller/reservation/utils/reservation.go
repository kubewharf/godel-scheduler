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

package utils

import (
	"fmt"

	"github.com/kubewharf/godel-scheduler-api/pkg/apis/scheduling/v1alpha1"
	"github.com/kubewharf/godel-scheduler/pkg/util/pod"
)

func GetReservationIndex(res *v1alpha1.Reservation) string {
	if res != nil && res.Annotations != nil {
		return res.Annotations[pod.ReservationIndexAnnotation]
	}
	return ""
}

func GetReservationOwner(res *v1alpha1.Reservation) string {
	if res == nil {
		return ""
	}

	owner := res.Status.CurrentOwners
	if len(owner.Namespace)+len(owner.Name)+len(owner.UID) == 0 {
		return ""
	}

	return fmt.Sprintf("%s/%s/%s", owner.Namespace, owner.Name, owner.UID)
}
