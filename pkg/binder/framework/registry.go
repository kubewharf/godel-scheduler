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

	"github.com/kubewharf/godel-scheduler/pkg/binder/apis/config"
	"github.com/kubewharf/godel-scheduler/pkg/binder/framework/handle"
	"github.com/kubewharf/godel-scheduler/pkg/binder/framework/plugins/defaultbinder"
	"github.com/kubewharf/godel-scheduler/pkg/binder/framework/plugins/defaultpreemption"
	"github.com/kubewharf/godel-scheduler/pkg/binder/framework/plugins/nodeports"
	"github.com/kubewharf/godel-scheduler/pkg/binder/framework/plugins/noderesources"
	"github.com/kubewharf/godel-scheduler/pkg/binder/framework/plugins/nodevolumelimits"
	"github.com/kubewharf/godel-scheduler/pkg/binder/framework/plugins/nonnativeresource"
	"github.com/kubewharf/godel-scheduler/pkg/binder/framework/plugins/volumebinding"
	framework "github.com/kubewharf/godel-scheduler/pkg/framework/api"
)

// PluginFactory is a function that builds a plugin.
type PluginFactory = func(configuration runtime.Object, pfh handle.BinderFrameworkHandle) (framework.Plugin, error)

// Registry is a collection of all available plugins. The framework uses a
// registry to enable and initialize configured plugins.
// All plugins must be in the registry before initializing the framework.
type Registry map[string]PluginFactory

// NewInTreeRegistry builds the registry with all the in-tree plugins.
// A scheduler that runs out of tree plugins can register additional plugins
// through the WithFrameworkOutOfTreeRegistry option.
// For Godel Scheduler all in tree plugins are enabled
func NewInTreeRegistry() Registry {
	return Registry{
		defaultbinder.Name:              defaultbinder.New,
		noderesources.ConflictCheckName: noderesources.NewConflictCheck,
		nodevolumelimits.CSIName:        nodevolumelimits.NewCSI,
		nodevolumelimits.CinderName:     nodevolumelimits.NewCinder,
		nodevolumelimits.AzureDiskName:  nodevolumelimits.NewAzureDisk,
		nodevolumelimits.GCEPDName:      nodevolumelimits.NewGCEPD,
		nodevolumelimits.EBSName:        nodevolumelimits.NewEBS,
		volumebinding.Name:              volumebinding.New,
		nodeports.Name:                  nodeports.New,
		nonnativeresource.Name:          nonnativeresource.New,
	}
}

func NewInTreePreemptionRegistry() Registry {
	return Registry{
		// preemption plugins
		defaultpreemption.PDBCheckerName: defaultpreemption.NewPDBChecker,
	}
}

// NewPluginsRegistry returns a registry instance having all plugins, where profile shows which plugins to use in default, registry indicates how to initialize each plugin
func NewPluginsRegistry(registry Registry, pluginArgs map[string]*config.PluginConfig, fh handle.BinderFrameworkHandle) (framework.PluginMap, error) {
	pluginMap := framework.PluginMap{}

	var err error

	preparePlugin := func(pluginName string) error {
		if _, ok := pluginMap[pluginName]; !ok {
			if pluginArgs[pluginName] != nil {
				pluginMap[pluginName], err = registry[pluginName](pluginArgs[pluginName].Args.Object, fh)
			} else {
				pluginMap[pluginName], err = registry[pluginName](nil, fh)
			}
		}
		if err != nil {
			err = fmt.Errorf("error occurs when initializing plugin %v, error: %v", pluginName, err.Error())
			return err
		}
		return nil
	}

	for plName := range registry {
		err = preparePlugin(plName)
		if err != nil {
			err = fmt.Errorf("plugin %v initialization failed: %v", plName, err.Error())
			return nil, err
		}
	}

	return pluginMap, nil
}
