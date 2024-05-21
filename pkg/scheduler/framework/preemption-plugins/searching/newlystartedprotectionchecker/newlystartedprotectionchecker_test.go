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

package newlystartedprotectionchecker

import (
	"reflect"
	"testing"
	"time"

	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"

	defaultsconfig "github.com/kubewharf/godel-scheduler/pkg/apis/config"
	commoncache "github.com/kubewharf/godel-scheduler/pkg/common/cache"
	framework "github.com/kubewharf/godel-scheduler/pkg/framework/api"
	"github.com/kubewharf/godel-scheduler/pkg/scheduler/apis/config"
	godelcache "github.com/kubewharf/godel-scheduler/pkg/scheduler/cache"
	testing_helper "github.com/kubewharf/godel-scheduler/pkg/testing-helper"
	podutil "github.com/kubewharf/godel-scheduler/pkg/util/pod"
)

func TestNewlyStartedProtectionChecker(t *testing.T) {
	var minInterval int64 = 10
	now := time.Now()
	tests := []struct {
		name           string
		pod            *v1.Pod
		args           *config.StartRecentlyArgs
		expectedStatus *framework.Status
	}{
		{
			name: "use args, start recently, can not be preempted",
			pod:  testing_helper.MakePod().Label("name", "dp").StartTime(metav1.NewTime(now.Add(-9 * time.Second))).Obj(),
			args: &config.StartRecentlyArgs{
				PreemptMinIntervalSeconds: &minInterval,
			},
			expectedStatus: framework.NewStatus(framework.PreemptionFail, "pod start within 10s could not be preempted"),
		},
		{
			name: "use args, pod can be preempted",
			pod:  testing_helper.MakePod().Label("name", "dp").StartTime(metav1.NewTime(now.Add(-11 * time.Second))).Obj(),
			args: &config.StartRecentlyArgs{
				PreemptMinIntervalSeconds: &minInterval,
			},
			expectedStatus: framework.NewStatus(framework.PreemptionNotSure, ""),
		},
		{
			name: "use annotation, pod can not be preempted",
			pod: testing_helper.MakePod().Label("name", "dp").StartTime(metav1.NewTime(now.Add(-11*time.Second))).
				Annotation(podutil.ProtectionDurationFromPreemptionKey, "12").Obj(),
			args: &config.StartRecentlyArgs{
				PreemptMinIntervalSeconds: &minInterval,
			},
			expectedStatus: framework.NewStatus(framework.PreemptionFail, "pod start within 12s could not be preempted"),
		},
		{
			name: "use annotation, pod can be preempted",
			pod: testing_helper.MakePod().Label("name", "dp").StartTime(metav1.NewTime(now.Add(-9*time.Second))).
				Annotation(podutil.ProtectionDurationFromPreemptionKey, "8").Obj(),
			args: &config.StartRecentlyArgs{
				PreemptMinIntervalSeconds: &minInterval,
			},
			expectedStatus: framework.NewStatus(framework.PreemptionNotSure, ""),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cache := godelcache.New(commoncache.MakeCacheHandlerWrapper().
				ComponentName("").SchedulerType("").SubCluster(framework.DefaultSubCluster).
				PodAssumedTTL(time.Second).Period(10 * time.Second).StopCh(make(<-chan struct{})).
				EnableStore("PreemptionStore").
				Obj())
			snapshot := godelcache.NewEmptySnapshot(commoncache.MakeCacheHandlerWrapper().
				SubCluster(framework.DefaultSubCluster).SwitchType(framework.DefaultSubClusterSwitchType).
				EnableStore("PreemptionStore").
				Obj())
			cache.UpdateSnapshot(snapshot)
			checker := newFakeStartRecently(tt.args)
			podInfo := framework.NewPodInfo(tt.pod)
			gotCode, gotMsg := checker.VictimSearching(nil, podInfo, nil, nil, nil)
			gotStatus := framework.NewStatus(gotCode, gotMsg)
			if !reflect.DeepEqual(tt.expectedStatus, gotStatus) {
				t.Errorf("expected status: %v, but got: %v", tt.expectedStatus, gotStatus)
			}
		})
	}
}

func newFakeStartRecently(plArgs runtime.Object) *NewlyStartedProtectionChecker {
	args, _ := getNewlyStartedProtectionCheckerArgs(plArgs)
	preemptMinIntervalSeconds := defaultsconfig.DefaultPreemptMinIntervalSeconds
	if args != nil && args.PreemptMinIntervalSeconds != nil {
		preemptMinIntervalSeconds = *args.PreemptMinIntervalSeconds
	}
	return &NewlyStartedProtectionChecker{
		preemptMinIntervalSeconds: preemptMinIntervalSeconds,
	}
}
