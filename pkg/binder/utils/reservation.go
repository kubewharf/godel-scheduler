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
	"context"
	"strings"
	"time"

	"github.com/kubewharf/godel-scheduler/pkg/util"
	podutil "github.com/kubewharf/godel-scheduler/pkg/util/pod"

	schedulingv1a1 "github.com/kubewharf/godel-scheduler-api/pkg/apis/scheduling/v1alpha1"
	godelclient "github.com/kubewharf/godel-scheduler-api/pkg/client/clientset/versioned"
	"github.com/kubewharf/godel-scheduler-api/pkg/client/listers/scheduling/v1alpha1"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/klog/v2"
)

const (
	RetryAttemptsOfUpdatingReservation = 3
	RetryWaitTime                      = 1 * time.Second
)

func shouldSkipUpdateReservationStatus(reservation *schedulingv1a1.Reservation) bool {
	if reservation == nil {
		return true
	}

	switch reservation.Status.Phase {
	case schedulingv1a1.ResourceReserved:
		return false
	case schedulingv1a1.ReservationMatched:
		return true
	// TODO: for gang
	case schedulingv1a1.PendingForReserve:
		return true
		/*
			case schedulingv1a1.PodFailedToReserve:
				return true
		*/
	case schedulingv1a1.ReservationTimeOut:
		return true
	default:
		return false
	}
}

func UpdateReservationStatus(crdClient godelclient.Interface, lister v1alpha1.ReservationLister, assumedPod, placeholderPod *v1.Pod, succeed bool) error {
	return util.Retry(RetryAttemptsOfUpdatingReservation, RetryWaitTime, func() error {
		ns := strings.TrimSuffix(placeholderPod.Namespace, podutil.ReservationPlaceholderPostFix)
		name := strings.TrimSuffix(placeholderPod.Name, podutil.ReservationPlaceholderPostFix)

		reservation, err := lister.Reservations(ns).Get(name)
		if err != nil {
			klog.ErrorS(err, "Failed to get reservation", "reservation", podutil.GetReservationKey(reservation))
			return err
		}
		newPrr := reservation.DeepCopy()
		// skip pod reservation request is not in reserved status.
		if shouldSkipUpdateReservationStatus(reservation) {
			klog.V(4).InfoS("Skip updating pod reservation request", "reservation", podutil.GetReservationKey(reservation))
			// not retry
			return nil
		}

		if succeed {
			newPrr.Status.CurrentOwners = schedulingv1a1.CurrentOwners{
				Name:      assumedPod.Name,
				Namespace: assumedPod.Namespace,
				UID:       assumedPod.UID,
			}
			newPrr.Status.Phase = schedulingv1a1.ReservationMatched
		}

		_, err = crdClient.SchedulingV1alpha1().Reservations(reservation.Namespace).UpdateStatus(context.Background(), newPrr, metav1.UpdateOptions{})
		if err != nil {
			klog.ErrorS(err, "Failed to update reservation", "reservation", podutil.GetReservationKey(reservation))
			return err
		}

		klog.V(4).InfoS("Successfully update reservation", "reservation", podutil.GetReservationKey(reservation))
		return nil
	})
}
