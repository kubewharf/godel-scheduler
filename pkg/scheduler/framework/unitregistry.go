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

package framework

import (
	"fmt"

	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/klog/v2"

	framework "github.com/kubewharf/godel-scheduler/pkg/framework/api"
	schedulerconfig "github.com/kubewharf/godel-scheduler/pkg/scheduler/apis/config"
	"github.com/kubewharf/godel-scheduler/pkg/scheduler/framework/handle"
	"github.com/kubewharf/godel-scheduler/pkg/scheduler/framework/unit_plugins/daemonset"
	"github.com/kubewharf/godel-scheduler/pkg/scheduler/framework/unit_plugins/joblevelaffinity"
	"github.com/kubewharf/godel-scheduler/pkg/scheduler/framework/unit_plugins/noop"
	"github.com/kubewharf/godel-scheduler/pkg/scheduler/framework/unit_plugins/virtualkubelet"
	"github.com/kubewharf/godel-scheduler/pkg/scheduler/util"
)

// UnitPluginFactory is a function that builds a plugin.
type UnitPluginFactory = func(configuration runtime.Object, handle handle.UnitFrameworkHandle) (framework.Plugin, error)

// UnitRegistry is a collection of all available plugins. The framework uses a
// registry to enable and initialize configured plugins.
// All plugins must be in the registry before initializing the framework.
type UnitRegistry map[string]UnitPluginFactory

// NewOrderedPluginRegistry builds the registry with all the filter plugins.
// If a filter plugin is not in the registry, it will be ignored.
// So a new plugin having Filter method need be added to the registry.
func NewOrderedUnitPluginRegistry() framework.PluginOrder {
	orderedPluginNames := &framework.OrderedPluginRegistry{
		Plugins: []string{
			daemonset.Name,
			virtualkubelet.Name,
			joblevelaffinity.Name,
			noop.Name,
		},
	}
	return util.GetListIndex(orderedPluginNames)
}

func NewUnitInTreeRegistry() UnitRegistry {
	return UnitRegistry{
		noop.Name:             noop.New,
		joblevelaffinity.Name: joblevelaffinity.New,
		daemonset.Name:        daemonset.New,
		virtualkubelet.Name:   virtualkubelet.New,
	}
}

func NewUnitPluginsRegistry(
	registry UnitRegistry,
	pluginArgs map[string]*schedulerconfig.PluginConfig, // TODO: Do we need UnitPlugin args in future?
	handler handle.UnitFrameworkHandle,
) framework.PluginMap {
	pluginMap := framework.PluginMap{}

	preparePlugin := func(pluginName string) error {
		var err error
		if _, ok := pluginMap[pluginName]; !ok {
			if pluginArgs[pluginName] != nil {
				pluginMap[pluginName], err = registry[pluginName](pluginArgs[pluginName].Args.Object, handler)
			} else {
				pluginMap[pluginName], err = registry[pluginName](nil, handler)
			}
		}
		if err != nil {
			return fmt.Errorf("error occurs when initializing plugin %v, error: %v", pluginName, err.Error())
		}
		return nil
	}

	for plName := range registry {
		if err := preparePlugin(plName); err != nil {
			klog.ErrorS(err, "Failed to initialize UnitSchedulingRegistry", "registry", registry, "pluginArgs", pluginArgs)
			klog.FlushAndExit(klog.ExitFlushTimeout, 1)
		}
	}

	return pluginMap
}
