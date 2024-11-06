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
	"time"

	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	clientset "k8s.io/client-go/kubernetes"

	"github.com/kubewharf/godel-scheduler/pkg/binder/metrics"
	framework "github.com/kubewharf/godel-scheduler/pkg/framework/api"
	"github.com/kubewharf/godel-scheduler/pkg/util"
	podutil "github.com/kubewharf/godel-scheduler/pkg/util/pod"
)

func CleanupPodAnnotations(client clientset.Interface, pod *v1.Pod) error {
	podCopy := pod.DeepCopy()
	if podCopy.Annotations == nil {
		podCopy.Annotations = map[string]string{}
	}

	delete(podCopy.Annotations, podutil.AssumedNodeAnnotationKey)
	delete(podCopy.Annotations, podutil.AssumedCrossNodeAnnotationKey)
	delete(podCopy.Annotations, podutil.NominatedNodeAnnotationKey)
	delete(podCopy.Annotations, podutil.FailedSchedulersAnnotationKey)
	delete(podCopy.Annotations, podutil.MicroTopologyKey)
	delete(podCopy.Annotations, podutil.MovementNameKey)
	delete(podCopy.Annotations, podutil.MatchedReservationPlaceholderKey)

	// reset pod state to dispatched
	podCopy.Annotations[podutil.PodStateAnnotationKey] = string(podutil.PodDispatched)

	startTime := time.Now()
	err := util.PatchPod(client, pod, podCopy)
	if err != nil {
		metrics.PodOperatingLatencyObserve(framework.ExtractPodProperty(pod), metrics.FailureResult, metrics.PatchPod, metrics.SinceInSeconds(startTime))
		return err
	}
	metrics.PodOperatingLatencyObserve(framework.ExtractPodProperty(pod), metrics.SuccessResult, metrics.PatchPod, metrics.SinceInSeconds(startTime))
	return nil
}

func GroupPodsByLauncher(pods map[types.UID]*v1.Pod) (map[podutil.PodLauncher][]*v1.Pod, error) {
	podMap := make(map[podutil.PodLauncher][]*v1.Pod)

	for _, pod := range pods {
		podLanucher, err := podutil.GetPodLauncher(pod)
		if err != nil {
			return nil, err
		}
		if _, exists := podMap[podLanucher]; !exists {
			podMap[podLanucher] = []*v1.Pod{pod}
		} else {
			podMap[podLanucher] = append(podMap[podLanucher], pod)
		}
	}

	return podMap, nil
}
