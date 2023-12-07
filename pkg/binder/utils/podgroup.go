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
	"fmt"
	"time"

	pgclientset "github.com/kubewharf/godel-scheduler-api/pkg/client/clientset/versioned"
	pglister "github.com/kubewharf/godel-scheduler-api/pkg/client/listers/scheduling/v1alpha1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/klog/v2"

	"github.com/kubewharf/godel-scheduler/pkg/framework/api"
	"github.com/kubewharf/godel-scheduler/pkg/util"
	unitutil "github.com/kubewharf/godel-scheduler/pkg/util/unit"
)

const MaxRetryAttempts = 3 // TODO: 5 will cause a timeout in UT (30s)

func LockPodGroupStatus(
	pgClient pgclientset.Interface, pgLister pglister.PodGroupLister,
	unit api.ScheduleUnit, operator string,
) error {
	// record event and update condition for PodGroup
	pg, err := pgLister.PodGroups(unit.GetNamespace()).Get(unit.GetName())
	if err != nil {
		return err
	}

	switch unitutil.PodGroupFinalOp(pg) {
	case "":
		// Do nothing here, will try to lock PodGroup later.
	case operator:
		klog.V(4).InfoS("PodGroup final operation has been locked successfully by binder before, so skip update PodGroup", "unit", unit.GetKey())
		return nil
	default:
		klog.ErrorS(nil, "Reject unit in bindUnit because PodGroup has been locked by others(eg:pg-controller)", "unit", unit.GetKey())
		return fmt.Errorf("Failed to lock podgroup final operation: " + unit.GetKey())
	}

	pgCopy := pg.DeepCopy()
	if !unitutil.PopulatePodGroupFinalOp(pgCopy, operator) {
		klog.ErrorS(nil, "Reject unit in bindUnit because failed to lock PodGroup", "unit", unit.GetKey())
		return fmt.Errorf("Failed to lock podgroup final operation: " + unit.GetKey())
	}

	successFlag := false

	util.Retry(MaxRetryAttempts, time.Second,
		func() error {
			_, err = pgClient.SchedulingV1alpha1().PodGroups(unit.GetNamespace()).Update(context.TODO(), pgCopy,
				metav1.UpdateOptions{})
			if err != nil {
				if errors.IsConflict(err) || errors.IsNotFound(err) {
					// For Conflict/NotFound errors, retrying is meaningless. So directly `return nil` to end the retry process.
					klog.ErrorS(err, "Failed to update PodGroup object and quick break", "unit", unit.GetKey())
					return nil
				}
				klog.ErrorS(err, "Failed to update PodGroup object", "unit", unit.GetKey())
				return err
			}
			successFlag = true
			return nil
		},
	)

	if successFlag {
		klog.V(4).InfoS("PodGroup final operation is locked successfully by binder.", "unit", unit.GetKey())
		return nil
	}
	klog.ErrorS(nil, "Reject unit in bindUnit because failed to update PodGroup object", "unit", unit.GetKey())
	return fmt.Errorf("Failed to lock podgroup final operation: " + unit.GetKey())
}
