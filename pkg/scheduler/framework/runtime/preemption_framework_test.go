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

package runtime

import (
	"reflect"
	"testing"

	v1 "k8s.io/api/core/v1"

	framework "github.com/kubewharf/godel-scheduler/pkg/framework/api"
	testinghelper "github.com/kubewharf/godel-scheduler/pkg/testing-helper"
)

type VictimSearchingPluginSucceed struct{}

var _ framework.VictimSearchingPlugin = &VictimSearchingPluginSucceed{}

func (plugin *VictimSearchingPluginSucceed) Name() string {
	return "VictimSearchingPluginSucceed"
}

func (plugin *VictimSearchingPluginSucceed) VictimSearching(*v1.Pod, *framework.PodInfo, *framework.CycleState, *framework.CycleState, *framework.VictimState) (framework.Code, string) {
	return framework.PreemptionSucceed, ""
}

type VictimSearchingPluginFail struct{}

var _ framework.VictimSearchingPlugin = &VictimSearchingPluginFail{}

func (plugin *VictimSearchingPluginFail) Name() string {
	return "VictimSearchingPluginFail"
}

func (plugin *VictimSearchingPluginFail) VictimSearching(*v1.Pod, *framework.PodInfo, *framework.CycleState, *framework.CycleState, *framework.VictimState) (framework.Code, string) {
	return framework.PreemptionFail, "failure"
}

type VictimSearchingPluginNotSure struct{}

var _ framework.VictimSearchingPlugin = &VictimSearchingPluginNotSure{}

func (plugin *VictimSearchingPluginNotSure) Name() string {
	return "VictimSearchingPluginFail"
}

func (plugin *VictimSearchingPluginNotSure) VictimSearching(*v1.Pod, *framework.PodInfo, *framework.CycleState, *framework.CycleState, *framework.VictimState) (framework.Code, string) {
	return framework.PreemptionNotSure, ""
}

func TestRunSearchingPlugins(t *testing.T) {
	pod := testinghelper.MakePod().Namespace("p").Name("p").UID("p").Obj()

	tests := []struct {
		name                          string
		collections                   []*framework.VictimSearchingPluginCollection
		expectedStatus                *framework.Status
		expectedPodsCanNotBePreempted []string
	}{
		{
			name: "force quick pass, pass",
			collections: []*framework.VictimSearchingPluginCollection{
				framework.NewVictimSearchingPluginCollection([]framework.VictimSearchingPlugin{
					&VictimSearchingPluginNotSure{},
					&VictimSearchingPluginSucceed{},
					&VictimSearchingPluginFail{},
				}, false, true, false),
				framework.NewVictimSearchingPluginCollection([]framework.VictimSearchingPlugin{
					&VictimSearchingPluginNotSure{},
					&VictimSearchingPluginFail{},
					&VictimSearchingPluginSucceed{},
				}, false, false, false),
			},
			expectedStatus:                framework.NewStatus(framework.PreemptionSucceed, ""),
			expectedPodsCanNotBePreempted: nil,
		},
		{
			name: "force quick pass, fail",
			collections: []*framework.VictimSearchingPluginCollection{
				framework.NewVictimSearchingPluginCollection([]framework.VictimSearchingPlugin{
					&VictimSearchingPluginNotSure{},
					&VictimSearchingPluginNotSure{},
					&VictimSearchingPluginNotSure{},
				}, false, true, false),
				framework.NewVictimSearchingPluginCollection([]framework.VictimSearchingPlugin{
					&VictimSearchingPluginNotSure{},
					&VictimSearchingPluginFail{},
					&VictimSearchingPluginSucceed{},
				}, false, false, false),
			},
			expectedStatus:                framework.NewStatus(framework.PreemptionFail, "failure"),
			expectedPodsCanNotBePreempted: nil,
		},
		{
			name: "enable quick pass",
			collections: []*framework.VictimSearchingPluginCollection{
				framework.NewVictimSearchingPluginCollection([]framework.VictimSearchingPlugin{
					&VictimSearchingPluginNotSure{},
					&VictimSearchingPluginSucceed{},
					&VictimSearchingPluginFail{},
				}, true, false, false),
				framework.NewVictimSearchingPluginCollection([]framework.VictimSearchingPlugin{
					&VictimSearchingPluginNotSure{},
					&VictimSearchingPluginFail{},
					&VictimSearchingPluginSucceed{},
				}, false, false, false),
			},
			expectedStatus:                framework.NewStatus(framework.PreemptionSucceed, ""),
			expectedPodsCanNotBePreempted: []string{"p/p/p"},
		},
		{
			name: "pass",
			collections: []*framework.VictimSearchingPluginCollection{
				framework.NewVictimSearchingPluginCollection([]framework.VictimSearchingPlugin{
					&VictimSearchingPluginNotSure{},
					&VictimSearchingPluginNotSure{},
					&VictimSearchingPluginNotSure{},
				}, false, false, false),
				framework.NewVictimSearchingPluginCollection([]framework.VictimSearchingPlugin{
					&VictimSearchingPluginNotSure{},
					&VictimSearchingPluginNotSure{},
					&VictimSearchingPluginSucceed{},
				}, false, false, false),
			},
			expectedStatus:                framework.NewStatus(framework.PreemptionSucceed, ""),
			expectedPodsCanNotBePreempted: nil,
		},
		{
			name: "fail",
			collections: []*framework.VictimSearchingPluginCollection{
				framework.NewVictimSearchingPluginCollection([]framework.VictimSearchingPlugin{
					&VictimSearchingPluginNotSure{},
					&VictimSearchingPluginSucceed{},
					&VictimSearchingPluginFail{},
				}, false, false, false),
				framework.NewVictimSearchingPluginCollection([]framework.VictimSearchingPlugin{
					&VictimSearchingPluginNotSure{},
					&VictimSearchingPluginFail{},
					&VictimSearchingPluginSucceed{},
				}, false, false, false),
			},
			expectedStatus:                framework.NewStatus(framework.PreemptionFail, "failure"),
			expectedPodsCanNotBePreempted: nil,
		},
		{
			name: "not sure",
			collections: []*framework.VictimSearchingPluginCollection{
				framework.NewVictimSearchingPluginCollection([]framework.VictimSearchingPlugin{
					&VictimSearchingPluginNotSure{},
					&VictimSearchingPluginNotSure{},
					&VictimSearchingPluginNotSure{},
				}, false, false, false),
				framework.NewVictimSearchingPluginCollection([]framework.VictimSearchingPlugin{
					&VictimSearchingPluginNotSure{},
					&VictimSearchingPluginNotSure{},
					&VictimSearchingPluginNotSure{},
				}, false, false, false),
			},
			expectedStatus:                framework.NewStatus(framework.PreemptionFail, "all plugins return not sure"),
			expectedPodsCanNotBePreempted: nil,
		},
		{
			name: "fail because reject not sure",
			collections: []*framework.VictimSearchingPluginCollection{
				framework.NewVictimSearchingPluginCollection([]framework.VictimSearchingPlugin{
					&VictimSearchingPluginNotSure{},
					&VictimSearchingPluginNotSure{},
					&VictimSearchingPluginSucceed{},
				}, false, false, false),
				framework.NewVictimSearchingPluginCollection([]framework.VictimSearchingPlugin{
					&VictimSearchingPluginNotSure{},
					&VictimSearchingPluginNotSure{},
					&VictimSearchingPluginNotSure{},
				}, false, false, true),
			},
			expectedStatus:                framework.NewStatus(framework.PreemptionFail, "reject not sure result for collection 1"),
			expectedPodsCanNotBePreempted: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := &GodelSchedulerPreemptionFramework{
				victimSearchingPlugins: tt.collections,
			}
			preemptionState := framework.NewCycleState()
			podInfo := framework.NewPodInfo(pod)
			gotCode, gotMsg := f.RunVictimSearchingPlugins(nil, podInfo, nil, preemptionState, nil)
			gotStatus := framework.NewStatus(gotCode, gotMsg)
			if !reflect.DeepEqual(tt.expectedStatus, gotStatus) {
				t.Errorf("expected status: %v, but got: %v", tt.expectedStatus, gotStatus)
			}
			podsCanNotBePreempted, _ := framework.GetPodsCanNotBePreempted(preemptionState)
			if !reflect.DeepEqual(tt.expectedPodsCanNotBePreempted, podsCanNotBePreempted) {
				t.Errorf("expected pods: %v, but got: %v", tt.expectedPodsCanNotBePreempted, podsCanNotBePreempted)
			}
		})
	}
}

func TestRunVictimSearchingPluginCollection(t *testing.T) {
	tests := []struct {
		name           string
		plugins        []framework.VictimSearchingPlugin
		expectedStatus *framework.Status
	}{
		{
			name: "succeed",
			plugins: []framework.VictimSearchingPlugin{
				&VictimSearchingPluginNotSure{},
				&VictimSearchingPluginSucceed{},
				&VictimSearchingPluginFail{},
			},
			expectedStatus: framework.NewStatus(framework.PreemptionSucceed, ""),
		},
		{
			name: "fail",
			plugins: []framework.VictimSearchingPlugin{
				&VictimSearchingPluginNotSure{},
				&VictimSearchingPluginFail{},
				&VictimSearchingPluginSucceed{},
			},
			expectedStatus: framework.NewStatus(framework.PreemptionFail, "failure"),
		},
		{
			name: "not sure",
			plugins: []framework.VictimSearchingPlugin{
				&VictimSearchingPluginNotSure{},
				&VictimSearchingPluginNotSure{},
				&VictimSearchingPluginNotSure{},
			},
			expectedStatus: framework.NewStatus(framework.PreemptionNotSure, ""),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pluginCollection := framework.NewVictimSearchingPluginCollection(tt.plugins, true, false, false)
			f := &GodelSchedulerPreemptionFramework{}
			gotCode, gotMsg := f.runVictimSearchingPluginCollection(pluginCollection, nil, nil, nil, nil, nil)
			gotStatus := framework.NewStatus(gotCode, gotMsg)
			if !reflect.DeepEqual(tt.expectedStatus, gotStatus) {
				t.Errorf("expected status: %v, but got: %v", tt.expectedStatus, gotStatus)
			}
		})
	}
}
