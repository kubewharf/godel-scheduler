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

package binder

import (
	"github.com/kubewharf/godel-scheduler/pkg/binder/apis/config"
	plugins "github.com/kubewharf/godel-scheduler/pkg/binder/framework/plugins/defaultpreemption"
	framework "github.com/kubewharf/godel-scheduler/pkg/framework/api"
)

var defaultBinderOptions = binderOptions{
	victimCheckingPluginSet: []*framework.VictimCheckingPluginCollectionSpec{
		framework.NewVictimCheckingPluginCollectionSpec(
			[]config.Plugin{
				{Name: plugins.PDBCheckerName},
			},
			false,
			false,
		),
	},
	preemptionPluginConfigs: map[string]*config.PluginConfig{},
	pluginConfigs:           map[string]*config.PluginConfig{},
}

type binderOptions struct {
	victimCheckingPluginSet []*framework.VictimCheckingPluginCollectionSpec
	preemptionPluginConfigs map[string]*config.PluginConfig
	pluginConfigs           map[string]*config.PluginConfig
}

// Option configures a Scheduler
type Option func(*binderOptions)

// WithPluginsAndConfigs sets Preemption Plugins and Configs, the default value is nil
func WithPluginsAndConfigs(profile *config.GodelBinderProfile) Option {
	return func(o *binderOptions) {
		if profile == nil {
			return
		}
		if profile.Plugins != nil && profile.Plugins.VictimChecking != nil {
			o.victimCheckingPluginSet = make([]*framework.VictimCheckingPluginCollectionSpec, len(profile.Plugins.VictimChecking.PluginCollections))
			for i, collection := range profile.Plugins.VictimChecking.PluginCollections {
				o.victimCheckingPluginSet[i] = framework.NewVictimCheckingPluginCollectionSpec(collection.Plugins, collection.EnableQuickPass, collection.ForceQuickPass)
			}
		}
		for index := range profile.PreemptionPluginConfigs {
			plugin := profile.PreemptionPluginConfigs[index]
			o.preemptionPluginConfigs[plugin.Name] = &plugin
		}
		for index := range profile.PluginConfigs {
			config := profile.PluginConfigs[index]
			o.pluginConfigs[config.Name] = &config
		}
	}
}

func renderOptions(opts ...Option) binderOptions {
	options := defaultBinderOptions
	for _, opt := range opts {
		opt(&options)
	}
	return options
}
