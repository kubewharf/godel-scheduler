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

package podlauncherchecker

import (
	"reflect"
	"testing"

	v1 "k8s.io/api/core/v1"

	framework "github.com/kubewharf/godel-scheduler/pkg/framework/api"
	testing_helper "github.com/kubewharf/godel-scheduler/pkg/testing-helper"
	podutil "github.com/kubewharf/godel-scheduler/pkg/util/pod"
)

func TestPodLauncherChecker(t *testing.T) {
	tests := []struct {
		name           string
		pod            *v1.Pod
		expectedStatus *framework.Status
	}{
		{
			name:           "pod launcher is kubelet",
			pod:            testing_helper.MakePod().Annotation(podutil.PodLauncherAnnotationKey, string(podutil.Kubelet)).Obj(),
			expectedStatus: framework.NewStatus(framework.PreemptionNotSure, ""),
		},
		{
			name:           "pod launcher is node manager",
			pod:            testing_helper.MakePod().Annotation(podutil.PodLauncherAnnotationKey, string(podutil.NodeManager)).Obj(),
			expectedStatus: framework.NewStatus(framework.PreemptionFail, "pod with launcher node manager can not be preempted"),
		},
		{
			name:           "pod launcher is nil",
			pod:            testing_helper.MakePod().Obj(),
			expectedStatus: framework.NewStatus(framework.PreemptionNotSure, ""),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			podLauncherChecker := &PodLauncherChecker{}
			podInfo := framework.NewPodInfo(tt.pod)
			gotCode, gotMsg := podLauncherChecker.VictimSearching(nil, podInfo, nil, nil, nil)
			gotStatus := framework.NewStatus(gotCode, gotMsg)
			if !reflect.DeepEqual(tt.expectedStatus, gotStatus) {
				t.Errorf("expected status: %v, but got: %v", tt.expectedStatus, gotStatus)
			}
		})
	}
}
