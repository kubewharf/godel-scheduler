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

package options

import (
	"reflect"
	"testing"

	"github.com/kubewharf/godel-scheduler/cmd/binder/app/config"
	binderconfig "github.com/kubewharf/godel-scheduler/pkg/binder/apis/config"
)

func TestLoadProfile(t *testing.T) {
	ops, _ := NewOptions()
	ops.ConfigFile = "../../../../test/static/binder_config_v1beta1.yaml"
	cfg := &config.Config{}
	if err := ops.ApplyTo(cfg); err != nil {
		t.Errorf("fail to apply config: %v", err)
	}
	expectedPreemptionCollections := []binderconfig.VictimCheckingPluginCollection{
		{
			Plugins: []binderconfig.Plugin{
				{Name: "PDBChecker"},
			},
		},
	}
	var expectedPluginConfigs []binderconfig.PluginConfig
	if !reflect.DeepEqual(expectedPreemptionCollections, cfg.BinderConfig.Profile.Plugins.VictimChecking.PluginCollections) {
		t.Errorf("expected: %v, but got: %v", expectedPreemptionCollections, cfg.BinderConfig.Profile.Plugins.VictimChecking.PluginCollections)
	}
	if !reflect.DeepEqual(expectedPluginConfigs, cfg.BinderConfig.Profile.PreemptionPluginConfigs) {
		t.Errorf("expected: %v, but got: %v", expectedPluginConfigs, cfg.BinderConfig.Profile.PreemptionPluginConfigs)
	}
}
