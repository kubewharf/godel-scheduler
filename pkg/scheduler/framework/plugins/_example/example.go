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

package example

import (
	"context"

	framework "github.com/kubewharf/godel-scheduler/pkg/framework/api"
	examplestore "github.com/kubewharf/godel-scheduler/pkg/scheduler/cache/commonstores/_example_store"
	"github.com/kubewharf/godel-scheduler/pkg/scheduler/framework/handle"

	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

const Name = "Example"

type ExamplePlugin struct {
	handle       handle.PodFrameworkHandle
	pluginHandle examplestore.StoreHandle
}

var (
	_ framework.FilterPlugin = &ExamplePlugin{}
	_ framework.ScorePlugin  = &ExamplePlugin{}
)

func (a *ExamplePlugin) Name() string {
	return Name
}

func New(_ runtime.Object, handle handle.PodFrameworkHandle) (framework.Plugin, error) {
	var pluginHandle examplestore.StoreHandle
	if ins := handle.FindStore(examplestore.Name); ins != nil {
		pluginHandle = ins.(examplestore.StoreHandle)
	}
	return &ExamplePlugin{handle: handle, pluginHandle: pluginHandle}, nil
}

func (ep *ExamplePlugin) Filter(ctx context.Context, state *framework.CycleState, pod *v1.Pod, nodeInfo framework.NodeInfo) *framework.Status {
	count := ep.pluginHandle.GetPodCount(nodeInfo.GetNodeName())
	if count > 100 {
		return framework.NewStatus(framework.Error, "exceed threshold")
	}
	return nil
}

func (ep *ExamplePlugin) Score(ctx context.Context, state *framework.CycleState, p *v1.Pod, nodeName string) (int64, *framework.Status) {
	count := ep.pluginHandle.GetPodCount(nodeName)
	return 100 - int64(count), nil
}

// ScoreExtensions of the Score plugin.
func (ep *ExamplePlugin) ScoreExtensions() framework.ScoreExtensions {
	return nil
}
