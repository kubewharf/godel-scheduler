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

// CSILimits is a plugin that checks node volume limits.
type CSILimits struct {
	csiLimitsPlugin *nodevolumelimits.CSILimits
}

var _ framework.FilterPlugin = &CSILimits{}

// Name is the name of the plugin used in the plugin registry and configurations.
const CSIName = "NodeVolumeLimits"

// Name returns name of the plugin. It is used in logs, etc.
func (pl *CSILimits) Name() string {
	return CSIName
}

// Filter invoked at the filter extension point.
func (pl *CSILimits) Filter(ctx context.Context, s *framework.CycleState, pod *v1.Pod, nodeInfo framework.NodeInfo) *framework.Status {
	_, status := podlauncher.NodeFits(s, pod, nodeInfo)
	if status != nil {
		return status
	}

	return pl.csiLimitsPlugin.FitsCSILimits(ctx, s, pod, nodeInfo)
}

// New initializes a new plugin and returns it.
func NewCSI(_ runtime.Object, handle handle.PodFrameworkHandle) (framework.Plugin, error) {
	informerFactory := handle.SharedInformerFactory()
	csiLimitsPlugin := nodevolumelimits.NewCSILimits(informerFactory)
	return &CSILimits{
		csiLimitsPlugin: csiLimitsPlugin,
	}, nil
}
