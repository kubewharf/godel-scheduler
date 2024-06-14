/*
Copyright 2019 The Kubernetes Authors.

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

	framework "github.com/kubewharf/godel-scheduler/pkg/framework/api"
	schedulerconfig "github.com/kubewharf/godel-scheduler/pkg/scheduler/apis/config"
	"github.com/kubewharf/godel-scheduler/pkg/scheduler/framework/handle"
	"github.com/kubewharf/godel-scheduler/pkg/scheduler/framework/plugins/coscheduling"
	"github.com/kubewharf/godel-scheduler/pkg/scheduler/framework/plugins/imagelocality"
	"github.com/kubewharf/godel-scheduler/pkg/scheduler/framework/plugins/loadaware"
	"github.com/kubewharf/godel-scheduler/pkg/scheduler/framework/plugins/nodeaffinity"
	"github.com/kubewharf/godel-scheduler/pkg/scheduler/framework/plugins/nodelabel"
	"github.com/kubewharf/godel-scheduler/pkg/scheduler/framework/plugins/nodeports"
	"github.com/kubewharf/godel-scheduler/pkg/scheduler/framework/plugins/nodepreferavoidpods"
	"github.com/kubewharf/godel-scheduler/pkg/scheduler/framework/plugins/noderesources"
	"github.com/kubewharf/godel-scheduler/pkg/scheduler/framework/plugins/nodeunschedulable"
	"github.com/kubewharf/godel-scheduler/pkg/scheduler/framework/plugins/nodevolumelimits"
	"github.com/kubewharf/godel-scheduler/pkg/scheduler/framework/plugins/nonnativeresource"
	"github.com/kubewharf/godel-scheduler/pkg/scheduler/framework/plugins/podlauncher"
	"github.com/kubewharf/godel-scheduler/pkg/scheduler/framework/plugins/tainttoleration"
	"github.com/kubewharf/godel-scheduler/pkg/scheduler/framework/plugins/volumebinding"
	"github.com/kubewharf/godel-scheduler/pkg/scheduler/framework/preemption-plugins/searching/newlystartedprotectionchecker"
	"github.com/kubewharf/godel-scheduler/pkg/scheduler/framework/preemption-plugins/searching/pdbchecker"
	"github.com/kubewharf/godel-scheduler/pkg/scheduler/framework/preemption-plugins/searching/podlauncherchecker"
	"github.com/kubewharf/godel-scheduler/pkg/scheduler/framework/preemption-plugins/searching/preemptibilitychecker"
	"github.com/kubewharf/godel-scheduler/pkg/scheduler/framework/preemption-plugins/searching/priorityvaluechecker"
	"github.com/kubewharf/godel-scheduler/pkg/scheduler/framework/preemption-plugins/sorting/priority"
	starttime "github.com/kubewharf/godel-scheduler/pkg/scheduler/framework/preemption-plugins/sorting/start_time"
	victimscount "github.com/kubewharf/godel-scheduler/pkg/scheduler/framework/preemption-plugins/sorting/victims_count"
)

// PluginFactory is a function that builds a plugin.
type PluginFactory = func(configuration runtime.Object, handle handle.PodFrameworkHandle) (framework.Plugin, error)

// Registry is a collection of all available plugins. The framework uses a
// registry to enable and initialize configured plugins.
// All plugins must be in the registry before initializing the framework.
type Registry map[string]PluginFactory

// NewOrderedPluginRegistry builds the registry with all the filter plugins.
// If a filter plugin is not in the registry, it will be ignored.
// So a new plugin having Filter method need be added to the registry.
func NewOrderedPluginRegistry() framework.PluginList {
	return &framework.OrderedPluginRegistry{
		Plugins: []string{
			// only UnschedulableAndUnresolvable
			nodeaffinity.Name,
			tainttoleration.Name,
			nodeunschedulable.Name,
			podlauncher.Name,
			volumebinding.Name,
			nodelabel.Name,

			// UnschedulableAndUnresolvable or Unschedulable
			// ...

			// only Unschedulable
			nodeports.Name,
			noderesources.FitName,
			nodevolumelimits.CSIName,
			nodevolumelimits.CinderName,
			nodevolumelimits.AzureDiskName,
			nodevolumelimits.GCEPDName,
			nodevolumelimits.EBSName,
			nonnativeresource.NonNativeTopologyName,

			// always Success
			coscheduling.Name,
		},
	}
}

// NewInTreeRegistry builds the registry with all the in-tree plugins.
// A scheduler that runs out of tree plugins can register additional plugins
// through the WithFrameworkOutOfTreeRegistry option.
// For Godel Scheduler all in tree plugins are enabled
func NewInTreeRegistry() Registry {
	return Registry{
		coscheduling.Name:                       coscheduling.New,
		imagelocality.Name:                      imagelocality.New,
		nodeunschedulable.Name:                  nodeunschedulable.New,
		nodepreferavoidpods.Name:                nodepreferavoidpods.New,
		tainttoleration.Name:                    tainttoleration.New,
		nodeaffinity.Name:                       nodeaffinity.New,
		nodelabel.Name:                          nodelabel.New,
		nodeports.Name:                          nodeports.New,
		podlauncher.Name:                        podlauncher.New,
		volumebinding.Name:                      volumebinding.New,
		nonnativeresource.NonNativeTopologyName: nonnativeresource.NewNonNativeTopology,
		// TODO: remove it, use NonNativeResourceSelector & NonNativeTopology instead  @songxinyi.echo

		nodevolumelimits.CSIName:       nodevolumelimits.NewCSI,
		nodevolumelimits.CinderName:    nodevolumelimits.NewCinder,
		nodevolumelimits.AzureDiskName: nodevolumelimits.NewAzureDisk,
		nodevolumelimits.GCEPDName:     nodevolumelimits.NewGCEPD,
		nodevolumelimits.EBSName:       nodevolumelimits.NewEBS,

		noderesources.FitName:                   noderesources.NewFit,
		noderesources.MostAllocatedName:         noderesources.NewMostAllocated,
		noderesources.LeastAllocatedName:        noderesources.NewLeastAllocated,
		noderesources.BalancedAllocationName:    noderesources.NewBalancedAllocation,
		noderesources.AdaptiveCpuToMemRatioName: noderesources.NewAdaptiveCpuToMemRatio,
		noderesources.NodeResourcesAffinityName: noderesources.NewNodeResourcesAffinity,

		loadaware.Name: loadaware.NewLoadAware,
	}
}

func NewInTreePreemptionRegistry() Registry {
	return Registry{
		// preemption plugins
		podlauncherchecker.PodLauncherCheckerName:                       podlauncherchecker.NewPodLauncherChecker,
		preemptibilitychecker.PreemptibilityCheckerName:                 preemptibilitychecker.NewPreemptibilityChecker,
		pdbchecker.PDBCheckerName:                                       pdbchecker.NewPDBChecker,
		priorityvaluechecker.PriorityValueCheckerName:                   priorityvaluechecker.NewPriorityValueChecker,
		newlystartedprotectionchecker.NewlyStartedProtectionCheckerName: newlystartedprotectionchecker.NewNewlyStartedProtectionChecker,
		// sorting plugins
		priority.MinHighestPriorityName:       priority.NewMinHighestPriority,
		priority.MinPrioritySumName:           priority.NewMinPrioritySum,
		starttime.LatestEarliestStartTimeName: starttime.NewLatestEarliestStartTime,
		victimscount.LeastVictimsName:         victimscount.NewLeastVictims,
	}
}

// NewPluginsRegistry returns a registry instance having all plugins, where profile shows which plugins to use in default, registry indicates how to initialize each plugin
func NewPluginsRegistry(registry Registry, pluginArgs map[string]*schedulerconfig.PluginConfig, fh handle.PodFrameworkHandle) (framework.PluginMap, error) {
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
