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

package interpretabity

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	schedv1alpha1 "github.com/kubewharf/godel-scheduler-api/pkg/apis/scheduling/v1alpha1"
	pgclientset "github.com/kubewharf/godel-scheduler-api/pkg/client/clientset/versioned"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/strategicpatch"
	"k8s.io/klog/v2"

	unitutil "github.com/kubewharf/godel-scheduler/pkg/util/unit"
)

// UpdatePreSchedulingCondition updates first PodGroupCondition, which Phase is "PreScheduling"
func UpdatePreSchedulingCondition(details *UnitSchedulingDetails, pgcli pgclientset.Interface, pg *schedv1alpha1.PodGroup) (err error) {
	if pg.Status.Phase != schedv1alpha1.PodGroupPreScheduling {
		return fmt.Errorf("PodGroup:%v isn't in phase:%v", unitutil.GetPodGroupKey(pg), schedv1alpha1.PodGroupPreScheduling)
	}

	if details == nil {
		return fmt.Errorf("UnitSchedulingDetails is nil")
	}

	reason := details.FailureReason()
	message := details.FailureMessage()

	// no failure, don't update condition
	if reason == "" || message == "" {
		return nil
	}

	pgCopy := pg.DeepCopy()
	conditions := pgCopy.Status.Conditions

	index := -1
	for i := range conditions {
		if conditions[i].Phase == schedv1alpha1.PodGroupPreScheduling {
			index = i
			break
		}
	}

	if index >= 0 {
		conditions[index] = schedv1alpha1.PodGroupCondition{
			Phase:              schedv1alpha1.PodGroupPreScheduling,
			Status:             v1.ConditionTrue,
			LastTransitionTime: metav1.Now(),
			Reason:             details.FailureReason(),
			Message:            details.FailureMessage(),
		}
		return PatchPodGroupCondition(pgcli, pg, pgCopy)
	}

	return nil
}

// PatchPodGroupCondition calculates the delta bytes change from <old> to <new>,
// and then submit a request to API server to patch the podgroup changes.
func PatchPodGroupCondition(crdCli pgclientset.Interface, old, new *schedv1alpha1.PodGroup) (err error) {
	if crdCli == nil {
		return fmt.Errorf("client is nil")
	}
	defer func() {
		if r := recover(); r != nil {
			err = errors.New("json.Marshal panic")
			klog.ErrorS(err, "Panic in PatchPodGroupCondition, return directly.")
		}
	}()

	oldData, err := json.Marshal(old)
	if err != nil {
		return err
	}

	newData, err := json.Marshal(new)
	if err != nil {
		return err
	}
	patchBytes, err := strategicpatch.CreateTwoWayMergePatch(oldData, newData, &schedv1alpha1.PodGroup{})
	if err != nil {
		return fmt.Errorf("failed to create merge patch for podgroup %q/%q: %v", old.Namespace, old.Name, err)
	}
	_, err = crdCli.SchedulingV1alpha1().PodGroups(old.Namespace).Patch(context.TODO(), old.Name, types.MergePatchType, patchBytes, metav1.PatchOptions{}, "status", "conditions")
	return err
}
