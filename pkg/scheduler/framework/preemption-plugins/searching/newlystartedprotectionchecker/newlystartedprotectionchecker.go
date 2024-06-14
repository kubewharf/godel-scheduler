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
	"fmt"
	"time"

	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"

	defaultsconfig "github.com/kubewharf/godel-scheduler/pkg/apis/config"
	framework "github.com/kubewharf/godel-scheduler/pkg/framework/api"
	"github.com/kubewharf/godel-scheduler/pkg/scheduler/apis/config"
	"github.com/kubewharf/godel-scheduler/pkg/scheduler/framework/handle"
)

const NewlyStartedProtectionCheckerName string = "NewlyStartedProtectionChecker"

type NewlyStartedProtectionChecker struct {
	preemptMinIntervalSeconds int64
}

var _ framework.VictimSearchingPlugin = &NewlyStartedProtectionChecker{}

// New initializes a new plugin and returns it.
func NewNewlyStartedProtectionChecker(plArgs runtime.Object, handle handle.PodFrameworkHandle) (framework.Plugin, error) {
	args, err := getNewlyStartedProtectionCheckerArgs(plArgs)
	if err != nil {
		return nil, fmt.Errorf("failed to new StartRecently plugin: %v", err)
	}
	preemptMinIntervalSeconds := defaultsconfig.DefaultPreemptMinIntervalSeconds
	if args != nil && args.PreemptMinIntervalSeconds != nil {
		preemptMinIntervalSeconds = *args.PreemptMinIntervalSeconds
	}
	checker := &NewlyStartedProtectionChecker{
		preemptMinIntervalSeconds: preemptMinIntervalSeconds,
	}
	return checker, nil
}

func (nspc *NewlyStartedProtectionChecker) Name() string {
	return NewlyStartedProtectionCheckerName
}

func (nspc *NewlyStartedProtectionChecker) VictimSearching(preemptor *v1.Pod, podInfo *framework.PodInfo, _, _ *framework.CycleState, _ *framework.VictimState) (framework.Code, string) {
	minIntervalSeconds := nspc.preemptMinIntervalSeconds
	if podInfo.PodPreemptionInfo.ProtectionDurationExist {
		// first check according to pod annotation
		minIntervalSeconds = podInfo.PodPreemptionInfo.ProtectionDuration
	}

	if minIntervalSeconds <= 0 {
		return framework.PreemptionNotSure, ""
	}
	startTime := podInfo.PodPreemptionInfo.StartTime
	if startTime == nil {
		return framework.PreemptionFail, "pod without start time could not be preempted"
	}
	nowTime := time.Now()
	limitTime := startTime.Add(time.Duration(minIntervalSeconds) * time.Second)
	if nowTime.Before(limitTime) {
		return framework.PreemptionFail, fmt.Sprintf("pod start within %ds could not be preempted", minIntervalSeconds)
	}
	return framework.PreemptionNotSure, ""
}

func getNewlyStartedProtectionCheckerArgs(obj runtime.Object) (*config.StartRecentlyArgs, error) {
	if obj == nil {
		return nil, nil
	}
	ptr, ok := obj.(*config.StartRecentlyArgs)
	if !ok {
		return nil, fmt.Errorf("want args to be of type StartRecentlyArgs, got %T", obj)
	}
	return ptr, nil
}
