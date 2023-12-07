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
	"encoding/json"
	"reflect"
	"strings"

	framework "github.com/kubewharf/godel-scheduler/pkg/framework/api"
	"github.com/kubewharf/godel-scheduler/pkg/scheduler/apis/config"
	preemptionstore "github.com/kubewharf/godel-scheduler/pkg/scheduler/cache/commonstores/preemption_store"
)

type schedulerOptions struct {
	defaultProfile     *config.GodelSchedulerProfile
	subClusterProfiles map[string]config.GodelSchedulerProfile

	renewInterval int64
	subClusterKey string
}

// Option configures a Scheduler
type Option func(*schedulerOptions)

func WithDefaultProfile(profile *config.GodelSchedulerProfile) Option {
	return func(o *schedulerOptions) {
		o.defaultProfile = profile
	}
}

func WithSubClusterProfiles(profiles []config.GodelSchedulerProfile) Option {
	return func(o *schedulerOptions) {
		subClusterProfiles := make(map[string]config.GodelSchedulerProfile, 0)
		for _, profile := range profiles {
			subClusterProfiles[profile.SubClusterName] = profile
		}
		o.subClusterProfiles = subClusterProfiles
	}
}

// WithRenewInterval sets renew interval for Scheduler in seconds, the default value is 30
func WithRenewInterval(renewInterval int64) Option {
	return func(o *schedulerOptions) {
		o.renewInterval = renewInterval
	}
}

func WithSubClusterKey(key string) Option {
	return func(o *schedulerOptions) {
		o.subClusterKey = key
	}
}

var defaultSchedulerOptions = schedulerOptions{
	renewInterval: config.DefaultRenewIntervalInSeconds,
	subClusterKey: config.DefaultSubClusterKey,
}

func renderOptions(opts ...Option) schedulerOptions {
	options := defaultSchedulerOptions
	for _, opt := range opts {
		opt(&options)
	}
	return options
}

type subClusterConfig struct {
	PercentageOfNodesToScore          int32
	IncreasedPercentageOfNodesToScore int32

	UseBlockQueue                 bool
	UnitInitialBackoffSeconds     int64
	UnitMaxBackoffSeconds         int64
	AttemptImpactFactorOnPriority float64

	BasePlugins             framework.PluginCollectionSet
	PluginConfigs           []config.PluginConfig
	PreemptionPluginConfigs []config.PluginConfig
	UnitQueueSortPlugin     *framework.PluginSpec

	DisablePreemption      bool
	CandidatesSelectPolicy string
	BetterSelectPolicies   []string

	EnableStore map[string]bool
}

func (c *subClusterConfig) complete(profile *config.GodelSchedulerProfile) {
	if profile == nil {
		return
	}
	if profile.PercentageOfNodesToScore != nil {
		c.PercentageOfNodesToScore = *profile.PercentageOfNodesToScore
	}
	if profile.IncreasedPercentageOfNodesToScore != nil {
		c.IncreasedPercentageOfNodesToScore = *profile.IncreasedPercentageOfNodesToScore
	}

	if profile.BlockQueue != nil {
		c.UseBlockQueue = *profile.BlockQueue
	}
	if profile.UnitInitialBackoffSeconds != nil {
		c.UnitInitialBackoffSeconds = *profile.UnitInitialBackoffSeconds
	}
	if profile.UnitMaxBackoffSeconds != nil {
		c.UnitMaxBackoffSeconds = *profile.UnitMaxBackoffSeconds
	}
	if profile.AttemptImpactFactorOnPriority != nil {
		c.AttemptImpactFactorOnPriority = *profile.AttemptImpactFactorOnPriority
	}
	if profile.BasePluginsForKubelet != nil || profile.BasePluginsForNM != nil {
		c.BasePlugins = renderBasePlugin(NewBasePlugins(), profile.BasePluginsForKubelet, profile.BasePluginsForNM)
	}
	if profile.PluginConfigs != nil {
		c.PluginConfigs = profile.PluginConfigs
	}
	if profile.PreemptionPluginConfigs != nil {
		c.PreemptionPluginConfigs = profile.PreemptionPluginConfigs
	}
	if profile.UnitQueueSortPlugin != nil {
		c.UnitQueueSortPlugin = framework.NewPluginSpec(profile.UnitQueueSortPlugin.Name)
	}

	if profile.DisablePreemption != nil {
		c.DisablePreemption = *profile.DisablePreemption
		c.EnableStore[string(preemptionstore.Name)] = !(*profile.DisablePreemption)
	}
	if profile.CandidatesSelectPolicy != nil {
		c.CandidatesSelectPolicy = *profile.CandidatesSelectPolicy
	}
	if profile.BetterSelectPolicies != nil {
		c.BetterSelectPolicies = *profile.BetterSelectPolicies
	}
}

// String by JSON format. This content can be identified on `https://jsonformatter.curiousconcept.com/#`
func (c *subClusterConfig) String() string {
	bytes, _ := json.Marshal(c)
	return strings.Replace(string(bytes), "\"", "", -1)
}

func (c *subClusterConfig) Equal(other *subClusterConfig) bool {
	return reflect.DeepEqual(c, other)
}

func newDefaultSubClusterConfig(profile *config.GodelSchedulerProfile) *subClusterConfig {
	c := &subClusterConfig{
		PercentageOfNodesToScore:          config.DefaultPercentageOfNodesToScore,
		IncreasedPercentageOfNodesToScore: config.DefaultIncreasedPercentageOfNodesToScore,

		UseBlockQueue:                 config.DefaultBlockQueue,
		UnitInitialBackoffSeconds:     config.DefaultUnitInitialBackoffInSeconds,
		UnitMaxBackoffSeconds:         config.DefaultUnitMaxBackoffInSeconds,
		AttemptImpactFactorOnPriority: config.DefaultAttemptImpactFactorOnPriority,

		BasePlugins:             NewBasePlugins(),
		PluginConfigs:           []config.PluginConfig{},
		PreemptionPluginConfigs: []config.PluginConfig{},
		UnitQueueSortPlugin:     defaultUnitQueueSortPluginSpec,

		DisablePreemption:      config.DefaultDisablePreemption,
		CandidatesSelectPolicy: config.CandidateSelectPolicyRandom,
		BetterSelectPolicies:   []string{config.BetterPreemptionPolicyAscending, config.BetterPreemptionPolicyDichotomy},

		// Construct EnableStore according to hard-coding default values.
		EnableStore: map[string]bool{
			string(preemptionstore.Name): !config.DefaultDisablePreemption,
		},
	}
	c.complete(profile)
	return c
}

func newSubClusterConfigFromDefaultConfig(profile *config.GodelSchedulerProfile, defaultConfig *subClusterConfig) *subClusterConfig {
	c := &subClusterConfig{
		PercentageOfNodesToScore:          defaultConfig.PercentageOfNodesToScore,
		IncreasedPercentageOfNodesToScore: defaultConfig.IncreasedPercentageOfNodesToScore,

		UseBlockQueue:                 defaultConfig.UseBlockQueue,
		UnitInitialBackoffSeconds:     defaultConfig.UnitInitialBackoffSeconds,
		UnitMaxBackoffSeconds:         defaultConfig.UnitMaxBackoffSeconds,
		AttemptImpactFactorOnPriority: defaultConfig.AttemptImpactFactorOnPriority,

		BasePlugins:             defaultConfig.BasePlugins,
		PluginConfigs:           defaultConfig.PluginConfigs,
		PreemptionPluginConfigs: defaultConfig.PreemptionPluginConfigs,
		UnitQueueSortPlugin:     defaultConfig.UnitQueueSortPlugin,

		DisablePreemption:      defaultConfig.DisablePreemption,
		CandidatesSelectPolicy: defaultConfig.CandidatesSelectPolicy,
		BetterSelectPolicies:   defaultConfig.BetterSelectPolicies,
	}
	c.EnableStore = make(map[string]bool, len(defaultConfig.EnableStore))
	for k, v := range defaultConfig.EnableStore {
		c.EnableStore[k] = v
	}

	c.complete(profile)
	return c
}
