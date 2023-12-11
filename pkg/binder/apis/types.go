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

package apis

import framework "github.com/kubewharf/godel-scheduler/pkg/framework/api"

type BinderPluginCollection struct {
	// CheckTopology is a set of plugins that should be invoked when Binder try to check whether the pod can run on the topology
	CheckTopology []string

	// CheckConflicts is a set of plugins that should be invoked when Binder try to check whether the pod can run on the node
	CheckConflicts []string

	// Reserves is a set of plugins invoked when reserving/unreserving resources
	// after a node is assigned to run the pod.
	Reserves []string

	// Permits is a set of plugins that control binding of a Pod. These plugins can prevent or delay binding of a Pod.
	Permits []string

	// PreBinds is a set of plugins that should be invoked before a pod is bound.
	PreBinds []string

	// Binds is a set of plugins that should be invoked at "Bind" extension point of the scheduling framework.
	// The scheduler call these plugins in order. Scheduler skips the rest of these plugins as soon as one returns success.
	Binds []string

	// PostBinds is a set of plugins that should be invoked after a pod is successfully bound.
	PostBinds []string

	VictimCheckings []*framework.VictimCheckingPluginCollectionSpec
}
