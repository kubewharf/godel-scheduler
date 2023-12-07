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

package scheduler

import (
	"io/ioutil"
	"os"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/stretchr/testify/assert"

	"github.com/kubewharf/godel-scheduler/cmd/scheduler/app/config"
	"github.com/kubewharf/godel-scheduler/cmd/scheduler/app/options"
	framework "github.com/kubewharf/godel-scheduler/pkg/framework/api"
	schedulerconfig "github.com/kubewharf/godel-scheduler/pkg/scheduler/apis/config"

	"k8s.io/apimachinery/pkg/runtime"
	utilpointer "k8s.io/utils/pointer"
)

var (
	TRUE  = true
	FALSE = false

	FLOAT64_3 = 3.0

	INT32_0   = int32(0)
	INT32_10  = int32(10)
	INT32_100 = int32(100)

	INT64_1   = int64(1)
	INT64_2   = int64(2)
	INT64_100 = int64(100)
	INT64_256 = int64(256)

	ComparePolicyMoreIsImportant string = "MoreIsImportant"
	ComparePolicyLessIsImportant string = "LessIsImportant"
	candidatesSelectPolicyBetter string = "Better"
	candidatesSelectPolicyBest   string = "Best"

	testBasePluginsForKubelet = &schedulerconfig.Plugins{
		Filter: &schedulerconfig.PluginSet{
			Plugins: []schedulerconfig.Plugin{
				{
					Name: "PodLauncher",
				}, {
					Name: "NodeUnschedulable",
				}, {
					Name: "NodeResourcesFit",
				}, {
					Name: "NodePorts",
				}, {
					Name: "VolumeBinding",
				}, {
					Name: "NodeAffinity",
				}, {
					Name: "TaintToleration",
				},
			},
		},
		Score: &schedulerconfig.PluginSet{
			Plugins: []schedulerconfig.Plugin{
				{
					Name:   "NodeResourcesMostAllocated",
					Weight: 8,
				}, {
					Name:   "NodePreferAvoidPods",
					Weight: 10,
				}, {
					Name:   "TaintToleration",
					Weight: 1,
				},
			},
		},
		VictimSearching: &schedulerconfig.VictimSearchingPluginSet{
			PluginCollections: []schedulerconfig.VictimSearchingPluginCollection{
				{
					Plugins: []schedulerconfig.Plugin{
						{Name: "PDBChecker"},
					},
				},
			},
		},
		Sorting: &schedulerconfig.PluginSet{
			Plugins: []schedulerconfig.Plugin{
				{
					Name: "MinHighestPriority",
				},
				{
					Name: "MinPrioritySum",
				},
			},
		},
	}
	testBasePluginsForNM = &schedulerconfig.Plugins{
		Filter: &schedulerconfig.PluginSet{
			Plugins: []schedulerconfig.Plugin{
				{
					Name: "PodLauncher",
				}, {
					Name: "NodeUnschedulable",
				}, {
					Name: "NodeResourcesFit",
				}, {
					Name: "NodePorts",
				}, {
					Name: "VolumeBinding",
				}, {
					Name: "NodeAffinity",
				}, {
					Name: "TaintToleration",
				},
			},
		},
		Score: &schedulerconfig.PluginSet{
			Plugins: []schedulerconfig.Plugin{
				{
					Name:   "NodeResourcesLeastAllocated",
					Weight: 8,
				}, {
					Name:   "TaintToleration",
					Weight: 1,
				},
			},
		},
	}
	testPluginConfigs = []schedulerconfig.PluginConfig{
		{
			Name: "NodeResourcesMostAllocated",
			Args: runtime.RawExtension{
				Raw: []uint8{123, 34, 114, 101, 115, 111, 117, 114, 99, 101, 115, 34, 58, 91, 123, 34, 110, 97, 109, 101, 34, 58, 34, 110, 118, 105, 100, 105, 97, 46, 99, 111, 109, 47, 103, 112, 117, 34, 44, 34, 119, 101, 105, 103, 104, 116, 34, 58, 49, 48, 125, 44, 123, 34, 110, 97, 109, 101, 34, 58, 34, 99, 112, 117, 34, 44, 34, 119, 101, 105, 103, 104, 116, 34, 58, 49, 125, 44, 123, 34, 110, 97, 109, 101, 34, 58, 34, 109, 101, 109, 111, 114, 121, 34, 44, 34, 119, 101, 105, 103, 104, 116, 34, 58, 49, 125, 93, 125},
				Object: &schedulerconfig.NodeResourcesMostAllocatedArgs{
					Resources: []schedulerconfig.ResourceSpec{
						{
							Name:   "nvidia.com/gpu",
							Weight: 10,
						}, {
							Name:   "cpu",
							Weight: 1,
						}, {
							Name:   "memory",
							Weight: 1,
						},
					},
				},
			},
		},
	}
	testPreemptionPluginConfigs = []schedulerconfig.PluginConfig{}
)

func getSubClusterProfile(compomentConfig schedulerconfig.GodelSchedulerConfiguration, subCluster string) *schedulerconfig.GodelSchedulerProfile {
	var ret *schedulerconfig.GodelSchedulerProfile
	for i, p := range compomentConfig.SubClusterProfiles {
		if p.SubClusterName == subCluster {
			if ret == nil {
				ret = &compomentConfig.SubClusterProfiles[i]
			} else {
				panic("Duplicate subcluster naming")
			}
		}
	}
	return ret
}

func TestLoadAndRenderFileV1beta1(t *testing.T) {
	ops, err := options.NewOptions()
	if err != nil {
		t.Error(err)
	}
	ops.SecureServing.BindPort = 0

	ops.ConfigFile = "../../test/static/scheduler_config_v1beta1.yaml"
	cfg := &config.Config{}
	if err := ops.ApplyTo(cfg); err != nil {
		t.Errorf("fail to apply config: %v", err)
	}

	defaultProfile := newDefaultSubClusterConfig(cfg.ComponentConfig.DefaultProfile)
	// DefaultProfile
	{
		expectedProfile := &subClusterConfig{
			PercentageOfNodesToScore: 0,

			UseBlockQueue:                 false,
			UnitInitialBackoffSeconds:     1,
			UnitMaxBackoffSeconds:         100,
			AttemptImpactFactorOnPriority: 3.0,

			BasePlugins:             renderBasePlugin(NewBasePlugins(), testBasePluginsForKubelet, testBasePluginsForNM),
			PluginConfigs:           testPluginConfigs,
			PreemptionPluginConfigs: testPreemptionPluginConfigs,
			UnitQueueSortPlugin:     framework.NewPluginSpec("DefaultUnitQueueSort"),

			DisablePreemption:      false,
			CandidatesSelectPolicy: schedulerconfig.CandidateSelectPolicyRandom,
			BetterSelectPolicies:   []string{schedulerconfig.BetterPreemptionPolicyAscending, schedulerconfig.BetterPreemptionPolicyDichotomy},

			EnableStore: map[string]bool{"PreemptionStore": true},
		}
		if diff := cmp.Diff(expectedProfile, defaultProfile); len(diff) > 0 {
			t.Errorf("defaultProfile got diff: %s", diff)
		}
		t.Log(expectedProfile.String())
	}

	// SubClusterProfiles: subCluster priorityqueue
	// Fields that are not set should use defaultProfile.
	{
		setPreemptionPluginConfigs := []schedulerconfig.PluginConfig{}

		name := "subCluster priorityqueue"
		subClusterProfile := newSubClusterConfigFromDefaultConfig(getSubClusterProfile(cfg.ComponentConfig, name), defaultProfile)
		expectedProfile := &subClusterConfig{
			PercentageOfNodesToScore: 0,

			UseBlockQueue:                 false,
			UnitInitialBackoffSeconds:     1,
			UnitMaxBackoffSeconds:         100,
			AttemptImpactFactorOnPriority: 3.0,

			BasePlugins:             renderBasePlugin(NewBasePlugins(), testBasePluginsForKubelet, testBasePluginsForNM),
			PluginConfigs:           testPluginConfigs,
			PreemptionPluginConfigs: setPreemptionPluginConfigs,
			UnitQueueSortPlugin:     framework.NewPluginSpec("FCFS"),

			DisablePreemption:      false,
			CandidatesSelectPolicy: schedulerconfig.CandidateSelectPolicyRandom,
			BetterSelectPolicies:   []string{schedulerconfig.BetterPreemptionPolicyAscending, schedulerconfig.BetterPreemptionPolicyDichotomy},

			EnableStore: map[string]bool{"PreemptionStore": true},
		}
		if diff := cmp.Diff(expectedProfile, subClusterProfile); len(diff) > 0 {
			t.Errorf("subClusterProfile got diff: %s", diff)
		}
		t.Log(expectedProfile.String())
	}

	// SubClusterProfiles: subCluster blockqueue
	// Fields that are not set should use defaultProfile.
	{
		setPreemptionPluginConfigs := []schedulerconfig.PluginConfig{}

		name := "subCluster blockqueue"
		subClusterProfile := newSubClusterConfigFromDefaultConfig(getSubClusterProfile(cfg.ComponentConfig, name), defaultProfile)
		expectedProfile := &subClusterConfig{
			PercentageOfNodesToScore: 0,

			UseBlockQueue:                 true,
			UnitInitialBackoffSeconds:     1,
			UnitMaxBackoffSeconds:         100,
			AttemptImpactFactorOnPriority: 3.0,

			BasePlugins:             renderBasePlugin(NewBasePlugins(), testBasePluginsForKubelet, testBasePluginsForNM),
			PluginConfigs:           testPluginConfigs,
			PreemptionPluginConfigs: setPreemptionPluginConfigs,
			UnitQueueSortPlugin:     framework.NewPluginSpec("FCFS"),

			DisablePreemption:      false,
			CandidatesSelectPolicy: schedulerconfig.CandidateSelectPolicyRandom,
			BetterSelectPolicies:   []string{schedulerconfig.BetterPreemptionPolicyAscending, schedulerconfig.BetterPreemptionPolicyDichotomy},

			EnableStore: map[string]bool{"PreemptionStore": true},
		}
		if diff := cmp.Diff(expectedProfile, subClusterProfile); len(diff) > 0 {
			t.Errorf("subClusterProfile got diff: %s", diff)
		}
		t.Log(expectedProfile.String())
	}

	// SubClusterProfiles: subCluster different percentageOfNodesToScore & unitInitialBackoffSeconds & unitMaxBackoffSeconds
	// Manually set fields should use the set values.
	{
		name := "subCluster different percentageOfNodesToScore & unitInitialBackoffSeconds & unitMaxBackoffSeconds"
		subClusterProfile := newSubClusterConfigFromDefaultConfig(getSubClusterProfile(cfg.ComponentConfig, name), defaultProfile)
		expectedProfile := &subClusterConfig{
			PercentageOfNodesToScore:          20,
			IncreasedPercentageOfNodesToScore: 40,

			UseBlockQueue:                 false,
			UnitInitialBackoffSeconds:     2,
			UnitMaxBackoffSeconds:         256,
			AttemptImpactFactorOnPriority: 3.0,

			BasePlugins:             renderBasePlugin(NewBasePlugins(), testBasePluginsForKubelet, testBasePluginsForNM),
			PluginConfigs:           testPluginConfigs,
			PreemptionPluginConfigs: testPreemptionPluginConfigs,
			UnitQueueSortPlugin:     framework.NewPluginSpec("DefaultUnitQueueSort"),

			DisablePreemption:      false,
			CandidatesSelectPolicy: schedulerconfig.CandidateSelectPolicyRandom,
			BetterSelectPolicies:   []string{schedulerconfig.BetterPreemptionPolicyAscending, schedulerconfig.BetterPreemptionPolicyDichotomy},

			EnableStore: map[string]bool{"PreemptionStore": true},
		}
		if diff := cmp.Diff(expectedProfile, subClusterProfile); len(diff) > 0 {
			t.Errorf("subClusterProfile got diff: %s", diff)
		}
		t.Log(expectedProfile.String())
	}

	// SubClusterProfiles: subCluster 1
	// Manually set BasePlugins and PluginConfigs should use the set values.
	{
		setBasePluginsForKubelet := &schedulerconfig.Plugins{
			Filter: &schedulerconfig.PluginSet{
				Plugins: []schedulerconfig.Plugin{
					{
						Name: "PodLauncher",
					},
				},
			},
			Score: &schedulerconfig.PluginSet{
				Plugins: []schedulerconfig.Plugin{
					{
						Name:   "NodeResourcesMostAllocated",
						Weight: 8,
					},
				},
			},
			VictimSearching: &schedulerconfig.VictimSearchingPluginSet{
				PluginCollections: []schedulerconfig.VictimSearchingPluginCollection{
					{
						Plugins: []schedulerconfig.Plugin{
							{Name: "PDBChecker"},
						},
					},
				},
			},
			Sorting: &schedulerconfig.PluginSet{
				Plugins: []schedulerconfig.Plugin{
					{
						Name: "MaxMinGPURemain",
					},
					{
						Name: "MaxMinNumaRemain",
					},
				},
			},
		}
		setPluginConfigs := []schedulerconfig.PluginConfig{
			{
				Name: "NodeResourcesMostAllocated",
				Args: runtime.RawExtension{
					Raw: []uint8{123, 34, 114, 101, 115, 111, 117, 114, 99, 101, 115, 34, 58, 91, 123, 34, 110, 97, 109, 101, 34, 58, 34, 110, 118, 105, 100, 105, 97, 46, 99, 111, 109, 47, 103, 112, 117, 34, 44, 34, 119, 101, 105, 103, 104, 116, 34, 58, 49, 125, 44, 123, 34, 110, 97, 109, 101, 34, 58, 34, 99, 112, 117, 34, 44, 34, 119, 101, 105, 103, 104, 116, 34, 58, 49, 125, 44, 123, 34, 110, 97, 109, 101, 34, 58, 34, 109, 101, 109, 111, 114, 121, 34, 44, 34, 119, 101, 105, 103, 104, 116, 34, 58, 49, 125, 93, 125},
					Object: &schedulerconfig.NodeResourcesMostAllocatedArgs{
						Resources: []schedulerconfig.ResourceSpec{
							{
								Name:   "nvidia.com/gpu",
								Weight: 1,
							}, {
								Name:   "cpu",
								Weight: 1,
							}, {
								Name:   "memory",
								Weight: 1,
							},
						},
					},
				},
			},
		}
		setPreemptionPluginConfigs := []schedulerconfig.PluginConfig{}

		name := "subCluster 1"
		subClusterProfile := newSubClusterConfigFromDefaultConfig(getSubClusterProfile(cfg.ComponentConfig, name), defaultProfile)
		expectedProfile := &subClusterConfig{
			PercentageOfNodesToScore: 0,

			UseBlockQueue:                 false,
			UnitInitialBackoffSeconds:     1,
			UnitMaxBackoffSeconds:         100,
			AttemptImpactFactorOnPriority: 3.0,

			BasePlugins:             renderBasePlugin(NewBasePlugins(), setBasePluginsForKubelet, nil),
			PluginConfigs:           setPluginConfigs,
			PreemptionPluginConfigs: setPreemptionPluginConfigs,
			UnitQueueSortPlugin:     framework.NewPluginSpec("FCFS"),

			DisablePreemption:      true,
			CandidatesSelectPolicy: schedulerconfig.CandidateSelectPolicyRandom,
			BetterSelectPolicies:   []string{schedulerconfig.BetterPreemptionPolicyAscending, schedulerconfig.BetterPreemptionPolicyDichotomy},

			EnableStore: map[string]bool{"PreemptionStore": false},
		}
		assert.Equal(t, expectedProfile, subClusterProfile)
		t.Log(expectedProfile.String())
	}

	// Finally revisit DefaultProfile again, it should NOT be affected by the configuration rendering of the sub-cluster.
	{
		expectedProfile := &subClusterConfig{
			PercentageOfNodesToScore: 0,

			UseBlockQueue:                 false,
			UnitInitialBackoffSeconds:     1,
			UnitMaxBackoffSeconds:         100,
			AttemptImpactFactorOnPriority: 3.0,

			BasePlugins:             renderBasePlugin(NewBasePlugins(), testBasePluginsForKubelet, testBasePluginsForNM),
			PluginConfigs:           testPluginConfigs,
			PreemptionPluginConfigs: testPreemptionPluginConfigs,
			UnitQueueSortPlugin:     framework.NewPluginSpec("DefaultUnitQueueSort"),

			DisablePreemption:      false,
			CandidatesSelectPolicy: schedulerconfig.CandidateSelectPolicyRandom,
			BetterSelectPolicies:   []string{schedulerconfig.BetterPreemptionPolicyAscending, schedulerconfig.BetterPreemptionPolicyDichotomy},

			EnableStore: map[string]bool{"PreemptionStore": true},
		}
		if diff := cmp.Diff(expectedProfile, defaultProfile); len(diff) > 0 {
			t.Errorf("defaultProfile got diff: %s", diff)
		}
		t.Log(expectedProfile.String())
	}
}

func replaceFile(fileName, replaceFileName, oldStr, newStr string) error {
	file, err := os.Open(fileName)
	if err != nil {
		return err
	}
	defer file.Close()
	content, err := ioutil.ReadAll(file)
	if err != nil {
		return err
	}
	contentStr := string(content)
	contentStr = strings.ReplaceAll(contentStr, oldStr, newStr)
	err = ioutil.WriteFile(replaceFileName, []byte(contentStr), 0o644)
	if err != nil {
		return err
	}
	return nil
}

func TestLoadAndRenderFileV1beta1PreemptionDefault(t *testing.T) {
	ops, err := options.NewOptions()
	if err != nil {
		t.Error(err)
	}
	ops.SecureServing.BindPort = 0

	fileName := "../../test/static/scheduler_config_v1beta1_preemption_default.yaml"
	replaceFileName := "../../test/static/scheduler_config_v1beta1_preemption_default_temp.yaml"
	if err := replaceFile(fileName, replaceFileName, "{{BindPort}}", "10253"); err != nil {
		t.Error(err)
	}

	ops.ConfigFile = replaceFileName
	cfg := &config.Config{}
	if err := ops.ApplyTo(cfg); err != nil {
		t.Errorf("fail to apply config: %v", err)
	}

	os.Remove(replaceFileName)

	defaultProfile := newDefaultSubClusterConfig(cfg.ComponentConfig.DefaultProfile)
	// DefaultProfile
	{
		expectedProfile := &schedulerconfig.GodelSchedulerProfile{
			CandidatesSelectPolicy: utilpointer.String(schedulerconfig.CandidateSelectPolicyRandom),
			BetterSelectPolicies: &schedulerconfig.StringSlice{
				schedulerconfig.BetterPreemptionPolicyAscending,
				schedulerconfig.BetterPreemptionPolicyDichotomy,
			},
		}

		if diff := cmp.Diff(*expectedProfile.CandidatesSelectPolicy, defaultProfile.CandidatesSelectPolicy); len(diff) > 0 {
			t.Errorf("defaultProfile got diff: %s", diff)
		}
		if diff := cmp.Diff([]string(*expectedProfile.BetterSelectPolicies), defaultProfile.BetterSelectPolicies); len(diff) > 0 {
			t.Errorf("defaultProfile got diff: %s", diff)
		}
	}

	// SubClusterProfiles: subCluster 1
	{
		expectedProfile := &schedulerconfig.GodelSchedulerProfile{
			CandidatesSelectPolicy: utilpointer.String(schedulerconfig.CandidateSelectPolicyRandom),
			BetterSelectPolicies: &schedulerconfig.StringSlice{
				schedulerconfig.BetterPreemptionPolicyAscending,
				schedulerconfig.BetterPreemptionPolicyDichotomy,
			},
		}

		profile := newSubClusterConfigFromDefaultConfig(getSubClusterProfile(cfg.ComponentConfig, "subCluster 1"), defaultProfile)
		if diff := cmp.Diff(*expectedProfile.CandidatesSelectPolicy, profile.CandidatesSelectPolicy); len(diff) > 0 {
			t.Errorf("subCluster 1 got diff: %s", diff)
		}
		if diff := cmp.Diff([]string(*expectedProfile.BetterSelectPolicies), profile.BetterSelectPolicies); len(diff) > 0 {
			t.Errorf("subCluster 1 got diff: %s", diff)
		}
	}

	// SubClusterProfiles: subCluster 2
	{
		expectedProfile := &schedulerconfig.GodelSchedulerProfile{
			CandidatesSelectPolicy: utilpointer.String(schedulerconfig.CandidateSelectPolicyBetter),
			BetterSelectPolicies: &schedulerconfig.StringSlice{
				schedulerconfig.BetterPreemptionPolicyDichotomy,
				schedulerconfig.BetterPreemptionPolicyAscending,
			},
		}
		profile := newSubClusterConfigFromDefaultConfig(getSubClusterProfile(cfg.ComponentConfig, "subCluster 2"), defaultProfile)
		if diff := cmp.Diff(*expectedProfile.CandidatesSelectPolicy, profile.CandidatesSelectPolicy); len(diff) > 0 {
			t.Errorf("subCluster 2 got diff: %s", diff)
		}
		if diff := cmp.Diff([]string(*expectedProfile.BetterSelectPolicies), profile.BetterSelectPolicies); len(diff) > 0 {
			t.Errorf("subCluster 2 got diff: %s", diff)
		}
	}
}

func TestLoadAndRenderFileV1beta1PreemptionProfileConfig(t *testing.T) {
	ops, err := options.NewOptions()
	if err != nil {
		t.Error(err)
	}
	ops.SecureServing.BindPort = 0

	fileName := "../../test/static/scheduler_config_v1beta1_preemption_profile_config.yaml"
	replaceFileName := "../../test/static/scheduler_config_v1beta1_preemption_profile_config_temp.yaml"
	if err := replaceFile(fileName, replaceFileName, "{{BindPort}}", "10257"); err != nil {
		t.Error(err)
	}

	ops.ConfigFile = replaceFileName
	cfg := &config.Config{}
	if err := ops.ApplyTo(cfg); err != nil {
		t.Errorf("fail to apply config: %v", err)
	}

	os.Remove(replaceFileName)

	defaultProfile := newDefaultSubClusterConfig(cfg.ComponentConfig.DefaultProfile)
	// DefaultProfile
	{
		expectedProfile := &schedulerconfig.GodelSchedulerProfile{
			CandidatesSelectPolicy: utilpointer.String(schedulerconfig.CandidateSelectPolicyRandom),
			BetterSelectPolicies: &schedulerconfig.StringSlice{
				schedulerconfig.BetterPreemptionPolicyDichotomy,
				schedulerconfig.BetterPreemptionPolicyAscending,
			},
		}

		if diff := cmp.Diff(*expectedProfile.CandidatesSelectPolicy, defaultProfile.CandidatesSelectPolicy); len(diff) > 0 {
			t.Errorf("defaultProfile got diff: %s", diff)
		}
		if diff := cmp.Diff([]string(*expectedProfile.BetterSelectPolicies), defaultProfile.BetterSelectPolicies); len(diff) > 0 {
			t.Errorf("defaultProfile got diff: %s", diff)
		}
	}

	// SubClusterProfiles: subCluster 1
	{
		expectedProfile := &schedulerconfig.GodelSchedulerProfile{
			CandidatesSelectPolicy: utilpointer.String(schedulerconfig.CandidateSelectPolicyBest),
			BetterSelectPolicies: &schedulerconfig.StringSlice{
				schedulerconfig.BetterPreemptionPolicyAscending,
				schedulerconfig.BetterPreemptionPolicyDichotomy,
			},
		}

		profile := newSubClusterConfigFromDefaultConfig(getSubClusterProfile(cfg.ComponentConfig, "subCluster 1"), defaultProfile)
		if diff := cmp.Diff(*expectedProfile.CandidatesSelectPolicy, profile.CandidatesSelectPolicy); len(diff) > 0 {
			t.Errorf("subCluster 1 got diff: %s", diff)
		}
		if diff := cmp.Diff([]string(*expectedProfile.BetterSelectPolicies), profile.BetterSelectPolicies); len(diff) > 0 {
			t.Errorf("subCluster 1 got diff: %s", diff)
		}
	}

	// SubClusterProfiles: subCluster 2
	{
		expectedProfile := &schedulerconfig.GodelSchedulerProfile{
			CandidatesSelectPolicy: utilpointer.String(schedulerconfig.CandidateSelectPolicyRandom),
			BetterSelectPolicies: &schedulerconfig.StringSlice{
				schedulerconfig.BetterPreemptionPolicyDichotomy,
				schedulerconfig.BetterPreemptionPolicyAscending,
			},
		}
		profile := newSubClusterConfigFromDefaultConfig(getSubClusterProfile(cfg.ComponentConfig, "subCluster 2"), defaultProfile)
		if diff := cmp.Diff(*expectedProfile.CandidatesSelectPolicy, profile.CandidatesSelectPolicy); len(diff) > 0 {
			t.Errorf("subCluster 2 got diff: %s", diff)
		}
		if diff := cmp.Diff([]string(*expectedProfile.BetterSelectPolicies), profile.BetterSelectPolicies); len(diff) > 0 {
			t.Errorf("subCluster 2 got diff: %s", diff)
		}
	}
}
