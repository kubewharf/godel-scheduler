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
	"time"

	"github.com/kubewharf/godel-scheduler/pkg/binder/metrics"
	"github.com/kubewharf/godel-scheduler/pkg/framework/utils"
	podutil "github.com/kubewharf/godel-scheduler/pkg/util/pod"

	godelclient "github.com/kubewharf/godel-scheduler-api/pkg/client/clientset/versioned"
	"github.com/kubewharf/godel-scheduler-api/pkg/client/listers/scheduling/v1alpha1"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/klog/v2"
)

const (
	MaxRetryAttemptsForMovement int           = 20
	DefaultBackoff              time.Duration = 100 * time.Millisecond
)

// TODO: move to postbind plugin
// return needReEnqueue bool
func UpdateMovement(movementClient godelclient.Interface, movementLister v1alpha1.MovementLister, assumedPod *v1.Pod) bool {
	movementName := podutil.GetMovementNameFromPod(assumedPod)
	if movementName == "" {
		return false
	}
	nodeName := utils.GetNodeNameFromPod(assumedPod)
	ownerInfoKey := podutil.GetPodOwnerInfoKey(assumedPod)
	taskInfo := utils.GetTaskInfoFromPod(assumedPod)
	var (
		matchSuggestion bool
		breakSuggestion bool
		algorithm       string
		err             error
	)
	defer func() {
		if err != nil {
			if matchSuggestion {
				metrics.ObserveMovementUpdateAttempts(algorithm, "fail", "match")
			} else if breakSuggestion {
				metrics.ObserveMovementUpdateAttempts(algorithm, "fail", "break")
			}
			klog.ErrorS(err, "Failed to update movement for pod", "pod", podutil.GetPodKey(assumedPod), "movement", movementName)
		} else if matchSuggestion {
			metrics.ObserveMovementUpdateAttempts(algorithm, "succeed", "match")
		} else if breakSuggestion {
			metrics.ObserveMovementUpdateAttempts(algorithm, "succeed", "break")
		}
	}()
	for i := 0; i < MaxRetryAttemptsForMovement; i++ {
		err = func() error {
			matchSuggestion, breakSuggestion = false, false
			gotMovement, gotErr := movementLister.Get(movementName)
			if gotErr != nil {
				return gotErr
			}
			newMovement := gotMovement.DeepCopy()
			algorithm = gotMovement.Spec.Creator
			for _, ownerMovement := range newMovement.Status.Owners {
				if podutil.GetOwnerInfoKey(ownerMovement.Owner) != ownerInfoKey {
					continue
				}
				var allDesiredPodCount int
				for _, nodeSuggestion := range ownerMovement.RecommendedNodes {
					allDesiredPodCount += int(nodeSuggestion.DesiredPodCount)
					if nodeSuggestion.Node != nodeName {
						continue
					}
					if len(nodeSuggestion.ActualPods) >= int(nodeSuggestion.DesiredPodCount) {
						break
					}
					matchSuggestion = true
					nodeSuggestion.ActualPods = append(nodeSuggestion.ActualPods, taskInfo)
					break
				}
				if matchSuggestion {
					break
				}
				if len(ownerMovement.MismatchedTasks) >= allDesiredPodCount {
					break
				}
				breakSuggestion = true
				ownerMovement.MismatchedTasks = append(ownerMovement.MismatchedTasks, taskInfo)
				break
			}
			if matchSuggestion || breakSuggestion {
				_, updateErr := movementClient.SchedulingV1alpha1().Movements().UpdateStatus(context.Background(), newMovement, metav1.UpdateOptions{})
				return updateErr
			}
			return nil
		}()
		if err != nil {
			if errors.IsNotFound(err) {
				return false
			} else {
				time.Sleep(DefaultBackoff)
				continue
			}
		}
		return false
	}
	return true
}
