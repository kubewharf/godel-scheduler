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
	"io/ioutil"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/stretchr/testify/assert"

	"github.com/kubewharf/godel-scheduler/cmd/scheduler/app/config"
	"github.com/kubewharf/godel-scheduler/pkg/binder"
	schedulerconfig "github.com/kubewharf/godel-scheduler/pkg/scheduler/apis/config"

	"k8s.io/apimachinery/pkg/runtime"
	utilpointer "k8s.io/utils/pointer"
)

var (
	TRUE  = true
	FALSE = false

	FLOAT64_3  = 3.0
	FLOAT64_10 = 10.0

	INT32_0  = int32(0)
	INT32_10 = int32(10)
	INT32_20 = int32(20)
	INT32_40 = int32(40)

	INT64_1   = int64(1)
	INT64_2   = int64(2)
	INT64_100 = int64(100)
	INT64_256 = int64(256)
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

func TestLoadFileV1beta1(t *testing.T) {
	ops, err := NewOptions()
	if err != nil {
		t.Error(err)
	}
	ops.SecureServing.BindPort = 0

	ops.ConfigFile = "../../../../test/static/scheduler_config_v1beta1.yaml"
	cfg := &config.Config{}
	if err := ops.ApplyTo(cfg); err != nil {
		t.Errorf("fail to apply config: %v", err)
	}

	// ComponentConfig
	{
		if cfg.ComponentConfig.ClientConnection.Burst != 1500 {
			t.Errorf("expect ClientConnection.Burst: 1500, got: %v", cfg.ComponentConfig.ClientConnection.Burst)
		}
		if cfg.ComponentConfig.ClientConnection.QPS != 1000 {
			t.Errorf("expect ClientConnection.QPS: 1000, got: %v", cfg.ComponentConfig.ClientConnection.QPS)
		}
		if cfg.ComponentConfig.SchedulerRenewIntervalSeconds != 100 {
			t.Errorf("expect SchedulerRenewIntervalSeconds: 100, got: %v", cfg.ComponentConfig.SchedulerRenewIntervalSeconds)
		}
		if *cfg.ComponentConfig.SubClusterKey != "nodeLevel" {
			t.Errorf("expect ClientConnection.Burst: nodeLevel, got: %v", *cfg.ComponentConfig.SubClusterKey)
		}
	}

	// DefaultProfile
	{
		expectedProfile := &schedulerconfig.GodelSchedulerProfile{
			SubClusterName: "",
			BasePluginsForKubelet: &schedulerconfig.Plugins{
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
			},
			BasePluginsForNM: &schedulerconfig.Plugins{
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
			},
			PluginConfigs: []schedulerconfig.PluginConfig{
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
			},
			PercentageOfNodesToScore:          &INT32_0,
			IncreasedPercentageOfNodesToScore: &INT32_0,
			DisablePreemption:                 &FALSE,
			CandidatesSelectPolicy:            utilpointer.StringPtr(schedulerconfig.CandidateSelectPolicyRandom),
			BetterSelectPolicies: &schedulerconfig.StringSlice{
				schedulerconfig.BetterPreemptionPolicyAscending,
				schedulerconfig.BetterPreemptionPolicyDichotomy,
			},
			BlockQueue: &FALSE,

			// UnitQueueSortPlugin: &schedulerconfig.Plugin{
			// 	Name: "DefaultUnitQueueSort",
			// },
			AttemptImpactFactorOnPriority: &FLOAT64_3,
			UnitInitialBackoffSeconds:     &INT64_1,
			UnitMaxBackoffSeconds:         &INT64_100,
			MaxWaitingDeletionDuration:    120,
		}

		profile := cfg.ComponentConfig.DefaultProfile
		if diff := cmp.Diff(expectedProfile, profile); len(diff) > 0 {
			t.Errorf("defaultProfile got diff: %s", diff)
		}
	}

	// SubClusterProfiles: subCluster 1
	{
		expectedProfile := &schedulerconfig.GodelSchedulerProfile{
			SubClusterName:             "subCluster 1",
			DisablePreemption:          &TRUE,
			MaxWaitingDeletionDuration: 300,
			UnitQueueSortPlugin: &schedulerconfig.Plugin{
				Name: "FCFS",
			},
			BasePluginsForKubelet: &schedulerconfig.Plugins{
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
			},
			PluginConfigs: []schedulerconfig.PluginConfig{
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
			},
		}

		profile := getSubClusterProfile(cfg.ComponentConfig, "subCluster 1")
		if diff := cmp.Diff(expectedProfile, profile); len(diff) > 0 {
			t.Errorf("subCluster 1 got diff: %s", diff)
		}
	}

	// SubClusterProfiles: subCluster blockqueue
	{
		expectedProfile := &schedulerconfig.GodelSchedulerProfile{
			SubClusterName:    "subCluster blockqueue",
			DisablePreemption: &FALSE,
			BlockQueue:        &TRUE,

			UnitQueueSortPlugin: &schedulerconfig.Plugin{
				Name: "FCFS",
			},
		}
		profile := getSubClusterProfile(cfg.ComponentConfig, "subCluster blockqueue")
		if diff := cmp.Diff(expectedProfile, profile); len(diff) > 0 {
			t.Errorf("subCluster blockqueue got diff: %s", diff)
		}
	}

	// SubClusterProfiles: subCluster priorityqueue
	{
		expectedProfile := &schedulerconfig.GodelSchedulerProfile{
			SubClusterName:    "subCluster priorityqueue",
			DisablePreemption: &FALSE,

			UnitQueueSortPlugin: &schedulerconfig.Plugin{
				Name: "FCFS",
			},
		}
		profile := getSubClusterProfile(cfg.ComponentConfig, "subCluster priorityqueue")
		if diff := cmp.Diff(expectedProfile, profile); len(diff) > 0 {
			t.Errorf("subCluster priorityqueue got diff: %s", diff)
		}
	}

	// SubClusterProfiles: subCluster different percentageOfNodesToScore & unitInitialBackoffSeconds & unitMaxBackoffSeconds
	{
		expectedProfile := &schedulerconfig.GodelSchedulerProfile{
			SubClusterName:                    "subCluster different percentageOfNodesToScore & unitInitialBackoffSeconds & unitMaxBackoffSeconds",
			PercentageOfNodesToScore:          &INT32_20,
			IncreasedPercentageOfNodesToScore: &INT32_40,
			UnitInitialBackoffSeconds:         &INT64_2,
			UnitMaxBackoffSeconds:             &INT64_256,
		}
		profile := getSubClusterProfile(cfg.ComponentConfig, "subCluster different percentageOfNodesToScore & unitInitialBackoffSeconds & unitMaxBackoffSeconds")
		if diff := cmp.Diff(expectedProfile, profile); len(diff) > 0 {
			t.Errorf("subCluster different percentageOfNodesToScore & unitInitialBackoffSeconds & unitMaxBackoffSeconds got diff: %s", diff)
		}
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

func TestLoadFileV1beta1ForPreemptionDefault(t *testing.T) {
	ops, err := NewOptions()
	if err != nil {
		t.Error(err)
	}
	ops.SecureServing.BindPort = 0

	fileName := "../../../../test/static/scheduler_config_v1beta1_preemption_default.yaml"
	replaceFileName := "../../../../test/static/scheduler_config_v1beta1_preemption_default_temp.yaml"
	if err := replaceFile(fileName, replaceFileName, "{{BindPort}}", "10253"); err != nil {
		t.Error(err)
	}

	ops.ConfigFile = replaceFileName
	cfg := &config.Config{}
	if err := ops.ApplyTo(cfg); err != nil {
		t.Errorf("fail to apply config: %v", err)
	}

	os.Remove(replaceFileName)

	// DefaultProfile
	{
		expectedProfile := &schedulerconfig.GodelSchedulerProfile{
			CandidatesSelectPolicy: utilpointer.String(schedulerconfig.CandidateSelectPolicyRandom),
			BetterSelectPolicies: &schedulerconfig.StringSlice{
				schedulerconfig.BetterPreemptionPolicyAscending,
				schedulerconfig.BetterPreemptionPolicyDichotomy,
			},
		}

		profile := cfg.ComponentConfig.DefaultProfile
		if diff := cmp.Diff(expectedProfile.CandidatesSelectPolicy, profile.CandidatesSelectPolicy); len(diff) > 0 {
			t.Errorf("defaultProfile got diff: %s", diff)
		}
		if diff := cmp.Diff(expectedProfile.BetterSelectPolicies, profile.BetterSelectPolicies); len(diff) > 0 {
			t.Errorf("defaultProfile got diff: %s", diff)
		}
	}

	// SubClusterProfiles: subCluster 1
	{
		expectedProfile := &schedulerconfig.GodelSchedulerProfile{}

		profile := getSubClusterProfile(cfg.ComponentConfig, "subCluster 1")
		if diff := cmp.Diff(expectedProfile.CandidatesSelectPolicy, profile.CandidatesSelectPolicy); len(diff) > 0 {
			t.Errorf("subCluster 1 got diff: %s", diff)
		}
		if diff := cmp.Diff(expectedProfile.BetterSelectPolicies, profile.BetterSelectPolicies); len(diff) > 0 {
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
		profile := getSubClusterProfile(cfg.ComponentConfig, "subCluster 2")
		if diff := cmp.Diff(expectedProfile.CandidatesSelectPolicy, profile.CandidatesSelectPolicy); len(diff) > 0 {
			t.Errorf("subCluster 2 got diff: %s", diff)
		}
		if diff := cmp.Diff(expectedProfile.BetterSelectPolicies, profile.BetterSelectPolicies); len(diff) > 0 {
			t.Errorf("subCluster 2 got diff: %s", diff)
		}
	}
}

func TestLoadFileV1beta1ForPreemptionProfileConfig(t *testing.T) {
	ops, err := NewOptions()
	if err != nil {
		t.Error(err)
	}
	ops.SecureServing.BindPort = 0

	fileName := "../../../../test/static/scheduler_config_v1beta1_preemption_profile_config.yaml"
	replaceFileName := "../../../../test/static/scheduler_config_v1beta1_preemption_profile_config_temp.yaml"
	if err := replaceFile(fileName, replaceFileName, "{{BindPort}}", "10257"); err != nil {
		t.Error(err)
	}

	ops.ConfigFile = replaceFileName
	cfg := &config.Config{}
	if err := ops.ApplyTo(cfg); err != nil {
		t.Errorf("fail to apply config: %v", err)
	}

	os.Remove(replaceFileName)

	// DefaultProfile
	{
		expectedProfile := &schedulerconfig.GodelSchedulerProfile{
			CandidatesSelectPolicy: utilpointer.String(schedulerconfig.CandidateSelectPolicyRandom),
			BetterSelectPolicies: &schedulerconfig.StringSlice{
				schedulerconfig.BetterPreemptionPolicyDichotomy,
				schedulerconfig.BetterPreemptionPolicyAscending,
			},
		}

		profile := cfg.ComponentConfig.DefaultProfile
		if diff := cmp.Diff(expectedProfile.CandidatesSelectPolicy, profile.CandidatesSelectPolicy); len(diff) > 0 {
			t.Errorf("defaultProfile got diff: %s", diff)
		}
		if diff := cmp.Diff(expectedProfile.BetterSelectPolicies, profile.BetterSelectPolicies); len(diff) > 0 {
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

		profile := getSubClusterProfile(cfg.ComponentConfig, "subCluster 1")
		if diff := cmp.Diff(expectedProfile.CandidatesSelectPolicy, profile.CandidatesSelectPolicy); len(diff) > 0 {
			t.Errorf("subCluster 1 got diff: %s", diff)
		}
		if diff := cmp.Diff(expectedProfile.BetterSelectPolicies, profile.BetterSelectPolicies); len(diff) > 0 {
			t.Errorf("subCluster 1 got diff: %s", diff)
		}
	}

	// SubClusterProfiles: subCluster 2
	{
		expectedProfile := &schedulerconfig.GodelSchedulerProfile{}
		profile := getSubClusterProfile(cfg.ComponentConfig, "subCluster 2")
		if diff := cmp.Diff(expectedProfile.CandidatesSelectPolicy, profile.CandidatesSelectPolicy); len(diff) > 0 {
			t.Errorf("subCluster 2 got diff: %s", diff)
		}
		if diff := cmp.Diff(expectedProfile.BetterSelectPolicies, profile.BetterSelectPolicies); len(diff) > 0 {
			t.Errorf("subCluster 2 got diff: %s", diff)
		}
	}
}

func TestOptions_EnableEmbeddedBinder_Default(t *testing.T) {
	opts, err := NewOptions()
	assert.NoError(t, err)
	assert.False(t, opts.EnableEmbeddedBinder, "EnableEmbeddedBinder should default to false")
}

func TestOptions_EnableEmbeddedBinder_Flag(t *testing.T) {
	tests := []struct {
		name     string
		args     []string
		expected bool
	}{
		{
			name:     "flag set to true",
			args:     []string{"--enable-embedded-binder=true"},
			expected: true,
		},
		{
			name:     "flag set to false",
			args:     []string{"--enable-embedded-binder=false"},
			expected: false,
		},
		{
			name:     "flag without value defaults to true",
			args:     []string{"--enable-embedded-binder"},
			expected: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			opts, err := NewOptions()
			assert.NoError(t, err)

			nfs := opts.Flags()
			fs := nfs.FlagSet("embedded binder")
			err = fs.Parse(tt.args)
			assert.NoError(t, err)
			assert.Equal(t, tt.expected, opts.EnableEmbeddedBinder)
		})
	}
}

func TestOptions_BinderConfig_Flags(t *testing.T) {
	tests := []struct {
		name           string
		args           []string
		wantMaxRetries int
		wantTimeout    time.Duration
		wantMaxLocal   int
	}{
		{
			name:           "default values when no flags",
			args:           []string{},
			wantMaxRetries: binder.DefaultMaxBindRetries,
			wantTimeout:    binder.DefaultBindTimeout,
			wantMaxLocal:   binder.DefaultMaxLocalRetries,
		},
		{
			name:           "custom max-bind-retries",
			args:           []string{"--max-bind-retries=10"},
			wantMaxRetries: 10,
			wantTimeout:    binder.DefaultBindTimeout,
			wantMaxLocal:   binder.DefaultMaxLocalRetries,
		},
		{
			name:           "custom bind-timeout",
			args:           []string{"--bind-timeout=60s"},
			wantMaxRetries: binder.DefaultMaxBindRetries,
			wantTimeout:    60 * time.Second,
			wantMaxLocal:   binder.DefaultMaxLocalRetries,
		},
		{
			name:           "custom max-local-retries",
			args:           []string{"--max-local-retries=8"},
			wantMaxRetries: binder.DefaultMaxBindRetries,
			wantTimeout:    binder.DefaultBindTimeout,
			wantMaxLocal:   8,
		},
		{
			name:           "all custom values",
			args:           []string{"--max-bind-retries=5", "--bind-timeout=45s", "--max-local-retries=12"},
			wantMaxRetries: 5,
			wantTimeout:    45 * time.Second,
			wantMaxLocal:   12,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			opts, err := NewOptions()
			assert.NoError(t, err)

			nfs := opts.Flags()
			fs := nfs.FlagSet("embedded binder")
			err = fs.Parse(tt.args)
			assert.NoError(t, err)
			assert.Equal(t, tt.wantMaxRetries, opts.EmbeddedBinderConfig.MaxBindRetries)
			assert.Equal(t, tt.wantTimeout, opts.EmbeddedBinderConfig.BindTimeout)
			assert.Equal(t, tt.wantMaxLocal, opts.EmbeddedBinderConfig.MaxLocalRetries)
		})
	}
}

func TestOptions_BinderConfig_InvalidValues(t *testing.T) {
	tests := []struct {
		name   string
		setup  func(o *Options)
		errMsg string
	}{
		{
			name: "negative MaxBindRetries",
			setup: func(o *Options) {
				o.EnableEmbeddedBinder = true
				o.EmbeddedBinderConfig.MaxBindRetries = -1
			},
			errMsg: "MaxBindRetries",
		},
		{
			name: "zero BindTimeout",
			setup: func(o *Options) {
				o.EnableEmbeddedBinder = true
				o.EmbeddedBinderConfig.BindTimeout = 0
			},
			errMsg: "BindTimeout",
		},
		{
			name: "negative MaxLocalRetries",
			setup: func(o *Options) {
				o.EnableEmbeddedBinder = true
				o.EmbeddedBinderConfig.MaxLocalRetries = -1
			},
			errMsg: "MaxLocalRetries",
		},
		{
			name: "invalid config not validated when embedded binder disabled",
			setup: func(o *Options) {
				o.EnableEmbeddedBinder = false
				o.EmbeddedBinderConfig.MaxBindRetries = -1
				o.EmbeddedBinderConfig.BindTimeout = 0
			},
			errMsg: "", // no error expected
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			opts, err := NewOptions()
			assert.NoError(t, err)
			tt.setup(opts)

			errs := opts.Validate()
			if tt.errMsg != "" {
				found := false
				for _, e := range errs {
					if strings.Contains(e.Error(), tt.errMsg) {
						found = true
						break
					}
				}
				assert.True(t, found, "expected error containing %q, got: %v", tt.errMsg, errs)
			} else {
				// Filter to only embedded binder related errors
				for _, e := range errs {
					assert.NotContains(t, e.Error(), "MaxBindRetries")
					assert.NotContains(t, e.Error(), "BindTimeout")
					assert.NotContains(t, e.Error(), "MaxLocalRetries")
				}
			}
		})
	}
}
