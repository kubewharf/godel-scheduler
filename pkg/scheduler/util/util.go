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

package util

import (
	framework "github.com/kubewharf/godel-scheduler/pkg/framework/api"
)

func GetListIndex(pluginList framework.PluginList) framework.PluginOrder {
	pluginOrder := make(framework.PluginOrder)
	if pluginList == nil {
		return pluginOrder
	}
	for index, name := range pluginList.List() {
		pluginOrder[name] = index
	}
	return pluginOrder
}

func FilterTrueKeys(h map[string]bool) []string {
	var ret []string
	for k, v := range h {
		if v {
			ret = append(ret, k)
		}
	}
	return ret
}
