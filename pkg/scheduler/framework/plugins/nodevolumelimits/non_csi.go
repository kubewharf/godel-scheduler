/*
Copyright 2018 The Kubernetes Authors.

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

package nodevolumelimits

import (
	"context"

	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"

	framework "github.com/kubewharf/godel-scheduler/pkg/framework/api"
	"github.com/kubewharf/godel-scheduler/pkg/plugins/nodevolumelimits"
	"github.com/kubewharf/godel-scheduler/pkg/plugins/podlauncher"
	"github.com/kubewharf/godel-scheduler/pkg/scheduler/framework/handle"
)

var (
	azureDiskPlugin *nodevolumelimits.NonCSILimits
	cinderPlugin    *nodevolumelimits.NonCSILimits
	ebsPlugin       *nodevolumelimits.NonCSILimits
	gcepdPlugin     *nodevolumelimits.NonCSILimits
)

// AzureDiskName is the name of the plugin used in the plugin registry and configurations.
const AzureDiskName = "AzureDiskLimits"

// NewAzureDisk returns function that initializes a new plugin and returns it.
func NewAzureDisk(_ runtime.Object, handle handle.PodFrameworkHandle) (framework.Plugin, error) {
	var err error
	informerFactory := handle.SharedInformerFactory()
	azureDiskPlugin, err = nodevolumelimits.NewAzureDisk(informerFactory)
	if err != nil {
		return nil, err
	}
	return newNonCSILimits(AzureDiskName, azureDiskPlugin), nil
}

// CinderName is the name of the plugin used in the plugin registry and configurations.
const CinderName = "CinderLimits"

// NewCinder returns function that initializes a new plugin and returns it.
func NewCinder(_ runtime.Object, handle handle.PodFrameworkHandle) (framework.Plugin, error) {
	var err error
	informerFactory := handle.SharedInformerFactory()
	cinderPlugin, err = nodevolumelimits.NewCinder(informerFactory)
	if err != nil {
		return nil, err
	}
	return newNonCSILimits(CinderName, cinderPlugin), nil
}

// EBSName is the name of the plugin used in the plugin registry and configurations.
const EBSName = "EBSLimits"

// NewEBS returns function that initializes a new plugin and returns it.
func NewEBS(_ runtime.Object, handle handle.PodFrameworkHandle) (framework.Plugin, error) {
	var err error
	informerFactory := handle.SharedInformerFactory()
	ebsPlugin, err = nodevolumelimits.NewEBS(informerFactory)
	if err != nil {
		return nil, err
	}
	return newNonCSILimits(EBSName, ebsPlugin), nil
}

// GCEPDName is the name of the plugin used in the plugin registry and configurations.
const GCEPDName = "GCEPDLimits"

// NewGCEPD returns function that initializes a new plugin and returns it.
func NewGCEPD(_ runtime.Object, handle handle.PodFrameworkHandle) (framework.Plugin, error) {
	var err error
	informerFactory := handle.SharedInformerFactory()
	gcepdPlugin, err = nodevolumelimits.NewGCEPD(informerFactory)
	if err != nil {
		return nil, err
	}
	return newNonCSILimits(GCEPDName, gcepdPlugin), nil
}

func newNonCSILimits(name string, nonCSILimitsPlugin *nodevolumelimits.NonCSILimits) framework.Plugin {
	return &nonCSILimits{
		name:               name,
		nonCSILimitsPlugin: nonCSILimitsPlugin,
	}
}

// nonCSILimits contains information to check the max number of volumes for a plugin.
type nonCSILimits struct {
	name               string
	nonCSILimitsPlugin *nodevolumelimits.NonCSILimits
}

var _ framework.FilterPlugin = &nonCSILimits{}

// Name returns name of the plugin. It is used in logs, etc.
func (pl *nonCSILimits) Name() string {
	return pl.name
}

// Filter invoked at the filter extension point.
func (pl *nonCSILimits) Filter(ctx context.Context, s *framework.CycleState, pod *v1.Pod, nodeInfo framework.NodeInfo) *framework.Status {
	_, status := podlauncher.NodeFits(s, pod, nodeInfo)
	if status != nil {
		return status
	}

	// If a pod doesn't have any volume attached to it, the predicate will always be true.
	// Thus we make a fast path for it, to avoid unnecessary computations in this case.
	pl.nonCSILimitsPlugin.FitsNonCSILimits(ctx, s, pod, nodeInfo)
	return nil
}
