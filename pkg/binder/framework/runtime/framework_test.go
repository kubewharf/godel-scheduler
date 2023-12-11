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
)

type VictimCheckingPluginSucceed struct{}

var _ framework.VictimCheckingPlugin = &VictimCheckingPluginSucceed{}

func (plugin *VictimCheckingPluginSucceed) Name() string {
	return "SearchingPluginSucceed"
}

func (plugin *VictimCheckingPluginSucceed) VictimChecking(*v1.Pod, *v1.Pod, *framework.CycleState, *framework.CycleState) (framework.Code, string) {
	return framework.PreemptionSucceed, ""
}

type VictimCheckingPluginFail struct{}

var _ framework.VictimCheckingPlugin = &VictimCheckingPluginFail{}

func (plugin *VictimCheckingPluginFail) Name() string {
	return "SearchingPluginFail"
}

func (plugin *VictimCheckingPluginFail) VictimChecking(*v1.Pod, *v1.Pod, *framework.CycleState, *framework.CycleState) (framework.Code, string) {
	return framework.PreemptionFail, "failure"
}

type VictimCheckingPluginNotSure struct{}

var _ framework.VictimCheckingPlugin = &VictimCheckingPluginNotSure{}

func (plugin *VictimCheckingPluginNotSure) Name() string {
	return "SearchingPluginFail"
}

func (plugin *VictimCheckingPluginNotSure) VictimChecking(*v1.Pod, *v1.Pod, *framework.CycleState, *framework.CycleState) (framework.Code, string) {
	return framework.PreemptionNotSure, ""
}

func TestRunSearchingPluginCollection(t *testing.T) {
	tests := []struct {
		name           string
		plugins        []framework.VictimCheckingPlugin
		expectedStatus *framework.Status
	}{
		{
			name: "succeed",
			plugins: []framework.VictimCheckingPlugin{
				&VictimCheckingPluginNotSure{},
				&VictimCheckingPluginSucceed{},
				&VictimCheckingPluginFail{},
			},
			expectedStatus: framework.NewStatus(framework.PreemptionSucceed),
		},
		{
			name: "fail",
			plugins: []framework.VictimCheckingPlugin{
				&VictimCheckingPluginNotSure{},
				&VictimCheckingPluginFail{},
				&VictimCheckingPluginSucceed{},
			},
			expectedStatus: framework.NewStatus(framework.PreemptionFail, "failure"),
		},
		{
			name: "not sure",
			plugins: []framework.VictimCheckingPlugin{
				&VictimCheckingPluginNotSure{},
				&VictimCheckingPluginNotSure{},
				&VictimCheckingPluginNotSure{},
			},
			expectedStatus: framework.NewStatus(framework.PreemptionNotSure),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pluginCollection := framework.NewVictimCheckingPluginCollection(tt.plugins, true, false)
			f := &GodelFramework{}
			gotStatus := f.runVictimCheckingPluginCollection(pluginCollection, nil, nil, nil, nil)
			if !reflect.DeepEqual(tt.expectedStatus, gotStatus) {
				t.Errorf("expected status: %v, but got: %v", tt.expectedStatus, gotStatus)
			}
		})
	}
}

func TestRunSearchingPlugins(t *testing.T) {
	tests := []struct {
		name           string
		collections    []*framework.VictimCheckingPluginCollection
		expectedStatus *framework.Status
	}{
		{
			name: "force quick pass, pass",
			collections: []*framework.VictimCheckingPluginCollection{
				framework.NewVictimCheckingPluginCollection([]framework.VictimCheckingPlugin{
					&VictimCheckingPluginNotSure{},
					&VictimCheckingPluginSucceed{},
					&VictimCheckingPluginFail{},
				}, false, true),
				framework.NewVictimCheckingPluginCollection([]framework.VictimCheckingPlugin{
					&VictimCheckingPluginNotSure{},
					&VictimCheckingPluginFail{},
					&VictimCheckingPluginSucceed{},
				}, false, false),
			},
			expectedStatus: framework.NewStatus(framework.PreemptionSucceed),
		},
		{
			name: "force quick pass, fail",
			collections: []*framework.VictimCheckingPluginCollection{
				framework.NewVictimCheckingPluginCollection([]framework.VictimCheckingPlugin{
					&VictimCheckingPluginNotSure{},
					&VictimCheckingPluginNotSure{},
					&VictimCheckingPluginNotSure{},
				}, false, true),
				framework.NewVictimCheckingPluginCollection([]framework.VictimCheckingPlugin{
					&VictimCheckingPluginNotSure{},
					&VictimCheckingPluginFail{},
					&VictimCheckingPluginSucceed{},
				}, false, false),
			},
			expectedStatus: framework.NewStatus(framework.PreemptionFail, "failure"),
		},
		{
			name: "enable quick pass",
			collections: []*framework.VictimCheckingPluginCollection{
				framework.NewVictimCheckingPluginCollection([]framework.VictimCheckingPlugin{
					&VictimCheckingPluginNotSure{},
					&VictimCheckingPluginSucceed{},
					&VictimCheckingPluginFail{},
				}, true, false),
				framework.NewVictimCheckingPluginCollection([]framework.VictimCheckingPlugin{
					&VictimCheckingPluginNotSure{},
					&VictimCheckingPluginFail{},
					&VictimCheckingPluginSucceed{},
				}, false, false),
			},
			expectedStatus: framework.NewStatus(framework.PreemptionSucceed),
		},
		{
			name: "pass",
			collections: []*framework.VictimCheckingPluginCollection{
				framework.NewVictimCheckingPluginCollection([]framework.VictimCheckingPlugin{
					&VictimCheckingPluginNotSure{},
					&VictimCheckingPluginNotSure{},
					&VictimCheckingPluginNotSure{},
				}, false, false),
				framework.NewVictimCheckingPluginCollection([]framework.VictimCheckingPlugin{
					&VictimCheckingPluginNotSure{},
					&VictimCheckingPluginNotSure{},
					&VictimCheckingPluginSucceed{},
				}, false, false),
			},
			expectedStatus: framework.NewStatus(framework.PreemptionSucceed),
		},
		{
			name: "fail",
			collections: []*framework.VictimCheckingPluginCollection{
				framework.NewVictimCheckingPluginCollection([]framework.VictimCheckingPlugin{
					&VictimCheckingPluginNotSure{},
					&VictimCheckingPluginSucceed{},
					&VictimCheckingPluginFail{},
				}, false, false),
				framework.NewVictimCheckingPluginCollection([]framework.VictimCheckingPlugin{
					&VictimCheckingPluginNotSure{},
					&VictimCheckingPluginFail{},
					&VictimCheckingPluginSucceed{},
				}, false, false),
			},
			expectedStatus: framework.NewStatus(framework.PreemptionFail, "failure"),
		},
		{
			name: "not sure",
			collections: []*framework.VictimCheckingPluginCollection{
				framework.NewVictimCheckingPluginCollection([]framework.VictimCheckingPlugin{
					&VictimCheckingPluginNotSure{},
					&VictimCheckingPluginNotSure{},
					&VictimCheckingPluginNotSure{},
				}, false, false),
				framework.NewVictimCheckingPluginCollection([]framework.VictimCheckingPlugin{
					&VictimCheckingPluginNotSure{},
					&VictimCheckingPluginNotSure{},
					&VictimCheckingPluginNotSure{},
				}, false, false),
			},
			expectedStatus: framework.NewStatus(framework.PreemptionSucceed),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := &GodelFramework{
				victimCheckingPlugins: tt.collections,
			}
			gotStatus := f.RunVictimCheckingPlugins(nil, nil, nil, nil)
			if !reflect.DeepEqual(tt.expectedStatus, gotStatus) {
				t.Errorf("expected status: %v, but got: %v", tt.expectedStatus, gotStatus)
			}
		})
	}
}
