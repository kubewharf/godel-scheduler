/*
Copyright 2014 The Kubernetes Authors.

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
	"context"

	v1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	clientset "k8s.io/client-go/kubernetes"
	"k8s.io/klog/v2"

	framework "github.com/kubewharf/godel-scheduler/pkg/framework/api"
	"github.com/kubewharf/godel-scheduler/pkg/plugins/unitqueuesort"
	"github.com/kubewharf/godel-scheduler/pkg/scheduler/apis/config"
	godelcache "github.com/kubewharf/godel-scheduler/pkg/scheduler/cache"
	"github.com/kubewharf/godel-scheduler/pkg/scheduler/framework/plugins/coscheduling"
	"github.com/kubewharf/godel-scheduler/pkg/scheduler/framework/plugins/nodeaffinity"
	"github.com/kubewharf/godel-scheduler/pkg/scheduler/framework/plugins/nodeports"
	"github.com/kubewharf/godel-scheduler/pkg/scheduler/framework/plugins/noderesources"
	"github.com/kubewharf/godel-scheduler/pkg/scheduler/framework/plugins/nodeunschedulable"
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
	podutil "github.com/kubewharf/godel-scheduler/pkg/util/pod"
)

func basePluginsForKubelet() *framework.PluginCollection {
	basicPlugins := framework.PluginCollection{
		Filters: []*framework.PluginSpec{
			framework.NewPluginSpec(podlauncher.Name),
			framework.NewPluginSpec(coscheduling.Name),
			framework.NewPluginSpec(nodeunschedulable.Name),
			framework.NewPluginSpec(noderesources.FitName),
			framework.NewPluginSpec(nodeports.Name),
			framework.NewPluginSpec(volumebinding.Name),
			framework.NewPluginSpec(nodeaffinity.Name),
			framework.NewPluginSpec(tainttoleration.Name),
		},
		Searchings: []*framework.VictimSearchingPluginCollectionSpec{
			framework.NewVictimSearchingPluginCollectionSpec(
				[]config.Plugin{
					{Name: podlauncherchecker.PodLauncherCheckerName},
				},
				false,
				false,
				false,
			),
			framework.NewVictimSearchingPluginCollectionSpec(
				[]config.Plugin{
					{Name: preemptibilitychecker.PreemptibilityCheckerName},
				},
				false,
				false,
				false,
			),
			framework.NewVictimSearchingPluginCollectionSpec(
				[]config.Plugin{
					{Name: newlystartedprotectionchecker.NewlyStartedProtectionCheckerName},
				},
				false,
				false,
				false,
			),
			framework.NewVictimSearchingPluginCollectionSpec(
				[]config.Plugin{
					{Name: priorityvaluechecker.PriorityValueCheckerName},
				},
				false,
				false,
				true,
			),
			framework.NewVictimSearchingPluginCollectionSpec(
				[]config.Plugin{
					{Name: pdbchecker.PDBCheckerName},
				},
				false,
				false,
				false,
			),
		},
		Sortings: []*framework.PluginSpec{
			framework.NewPluginSpec(priority.MinHighestPriorityName),
			framework.NewPluginSpec(priority.MinPrioritySumName),
			framework.NewPluginSpec(victimscount.LeastVictimsName),
			framework.NewPluginSpec(starttime.LatestEarliestStartTimeName),
		},
	}
	return &basicPlugins
}

func basePluginsForNodeManager() *framework.PluginCollection {
	return &framework.PluginCollection{
		Filters: []*framework.PluginSpec{
			framework.NewPluginSpec(podlauncher.Name),
			framework.NewPluginSpec(coscheduling.Name),
			framework.NewPluginSpec(nodeunschedulable.Name),
			framework.NewPluginSpec(noderesources.FitName),
			framework.NewPluginSpec(nodeports.Name),
			framework.NewPluginSpec(volumebinding.Name),
			framework.NewPluginSpec(nodeaffinity.Name),
			framework.NewPluginSpec(tainttoleration.Name),
		},
	}
}

func NewBasePlugins() framework.PluginCollectionSet {
	basePlugins := make(framework.PluginCollectionSet)
	basePlugins[string(podutil.Kubelet)] = basePluginsForKubelet()
	basePlugins[string(podutil.NodeManager)] = basePluginsForNodeManager()
	return basePlugins
}

// defaultUnitQueueSortPluginSpec is the default sort unit plugin used in scheduling unit queue.
var defaultUnitQueueSortPluginSpec = framework.NewPluginSpec(unitqueuesort.Name)

// MakeDefaultErrorFunc construct a function to handle pod scheduler error, logs error only in Godel Scheduler
// Compared to K8S scheduler, adding pod to unschedulableQ not works for all situation, then we should change this Error handling function
func MakeDefaultErrorFunc(client clientset.Interface, schedulerCache godelcache.SchedulerCache) func(*framework.QueuedPodInfo, error) {
	return func(podInfo *framework.QueuedPodInfo, err error) {
		pod := podInfo.Pod
		if err == framework.ErrNoNodesAvailable {
			klog.InfoS("Failed to schedule pod; no nodes are registered to the cluster; waiting", "pod", klog.KObj(pod))
		} else if _, ok := err.(*framework.FitError); ok {
			klog.InfoS("Failed to schedule pod; no fit; waiting", "pod", klog.KObj(pod), "err", err)
		} else if apierrors.IsNotFound(err) {
			klog.InfoS("Failed to schedule pod; possibly due to node not found; waiting", "pod", klog.KObj(pod), "err", err)
			if errStatus, ok := err.(apierrors.APIStatus); ok && errStatus.Status().Details.Kind == "node" {
				nodeName := errStatus.Status().Details.Name
				// when node is not found, We do not remove the node right away. Trying again to get
				// the node and if the node is still not found, then remove it from the scheduler cache.
				_, err := client.CoreV1().Nodes().Get(context.TODO(), nodeName, metav1.GetOptions{})
				if err != nil && apierrors.IsNotFound(err) {
					node := v1.Node{ObjectMeta: metav1.ObjectMeta{Name: nodeName}}
					if err := schedulerCache.RemoveNode(&node); err != nil {
						klog.InfoS("Failed to remove node from the case as it was not found", "node", klog.KObj(&node))
					}
				}
			}
		} else {
			klog.InfoS("Failed to schedule pod", "pod", klog.KObj(pod), "err", err)
		}
	}
}

// renderBasePlugin sets the list of base plugins for pods should run
func renderBasePlugin(pluginCollection framework.PluginCollectionSet, baseKubeletPlugins, baseNMPlugins *config.Plugins) framework.PluginCollectionSet {
	getPlugins := func(plugins *config.Plugins, pluginCollection *framework.PluginCollection) {
		if plugins == nil {
			return
		}
		if plugins.Filter != nil {
			for _, plugin := range plugins.Filter.Plugins {
				pluginCollection.Filters = append(pluginCollection.Filters, framework.NewPluginSpec(plugin.Name))
			}
		}
		if plugins.Score != nil {
			for _, plugin := range plugins.Score.Plugins {
				if plugin.Weight == 0 {
					plugin.Weight = framework.DefaultPluginWeight
				}
				pluginCollection.Scores = append(pluginCollection.Scores, framework.NewPluginSpecWithWeight(plugin.Name, plugin.Weight))
			}
		}
		if plugins.VictimSearching != nil {
			pluginCollection.Searchings = make([]*framework.VictimSearchingPluginCollectionSpec, len(plugins.VictimSearching.PluginCollections))
			for i, plugin := range plugins.VictimSearching.PluginCollections {
				pluginCollection.Searchings[i] = framework.NewVictimSearchingPluginCollectionSpec(plugin.Plugins, plugin.EnableQuickPass, plugin.ForceQuickPass, plugin.RejectNotSure)
			}
		}
		if plugins.Sorting != nil {
			for _, plugin := range plugins.Sorting.Plugins {
				pluginCollection.Sortings = append(pluginCollection.Sortings, framework.NewPluginSpec(plugin.Name))
			}
		}
	}

	getPlugins(baseKubeletPlugins, pluginCollection[string(podutil.Kubelet)])
	getPlugins(baseNMPlugins, pluginCollection[string(podutil.NodeManager)])
	return pluginCollection
}

func profileNeedPreemption(profile *config.GodelSchedulerProfile) bool {
	if profile != nil && profile.DisablePreemption != nil {
		// If the DeisablePreemption has already been set, return it's value directly.
		return !*profile.DisablePreemption
	}
	// Otherwise, parse the value from default setting.
	return !config.DefaultDisablePreemption

}

func parseProfilesBoolConfiguration(options schedulerOptions, checker func(*config.GodelSchedulerProfile) bool) bool {
	if checker(options.defaultProfile) {
		return true
	}
	for k := range options.subClusterProfiles {
		profile := options.subClusterProfiles[k]
		if checker(&profile) {
			return true
		}
	}
	return false
}
