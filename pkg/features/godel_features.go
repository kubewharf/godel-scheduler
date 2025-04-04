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

package features

import (
	"k8s.io/apimachinery/pkg/util/runtime"
	utilfeature "k8s.io/apiserver/pkg/util/feature"
	"k8s.io/component-base/featuregate"
)

const (
	// owner: @renyuquan
	// alpha: for now
	//
	// Allows to run node shuffler in dispatcher
	// If this feature gate is disabled, node partition must be logical.
	DispatcherNodeShuffle featuregate.Feature = "DispatcherNodeShuffle"

	// owner: @songxinyi.echo
	// alpha: for now
	//
	// Enables non native k8s resource scheduling support
	NonNativeResourceSchedulingSupport featuregate.Feature = "NonNativeResourceSchedulingSupport"

	// owner: @wuye
	// beta: for now
	//
	// Allows to change pod priority used in pending queue according to scheduling attempts
	// If this feature gate is enabled, priority in pending queue will be changed according to scheduling attempts
	DynamicSchedulingPriority featuregate.Feature = "DynamicSchedulingPriority"

	// owner: @zhangrenyu
	// alpha: for now
	//
	// Allows to scrape metrics from godel scheduler cache.
	SchedulerCacheScrape featuregate.Feature = "SchedulerCacheScrape"

	// owner: @songxinyi.echo
	// alpha: for now
	//
	// Allows to close same priority preemption
	DisableSamePriorityPreemption featuregate.Feature = "DisableSamePriorityPreemption"

	// owner: @libing.binacs
	// alpha: for now
	//
	// Allows to schedule pods concurrently.
	SchedulerConcurrentScheduling featuregate.Feature = "SchedulerConcurrentScheduling"

	// owner: @libing.binacs
	// alpha: for now
	//
	// Allows to schedule pods per subcluster concurrently.
	SchedulerSubClusterConcurrentScheduling featuregate.Feature = "SchedulerSubClusterConcurrentScheduling"

	// owner: @songxinyi.echo
	// alpha: for now
	//
	// Allows colocation
	EnableColocation featuregate.Feature = "EnableColocation"

	// owner: @songxinyi.echo
	// alpha: for now
	//
	// Allows to schedule based on movements generated by rescheduler.
	SupportRescheduling featuregate.Feature = "SupportRescheduling"

	// owner: @liuzhilei
	// alpha: for now
	//
	// Allows to trigger resource reservation in Godel.
	ResourceReservation featuregate.Feature = "ResourceReservation"
)

func init() {
	runtime.Must(utilfeature.DefaultMutableFeatureGate.Add(defaultKubernetesFeatureGates))
}

// defaultKubernetesFeatureGates consists of all known Kubernetes-specific feature keys.
// To add a new feature, define a key for it above and add it here. The features will be
// available throughout Kubernetes binaries.
var defaultKubernetesFeatureGates = map[featuregate.Feature]featuregate.FeatureSpec{
	DispatcherNodeShuffle:                   {Default: false, PreRelease: featuregate.Alpha},
	NonNativeResourceSchedulingSupport:      {Default: false, PreRelease: featuregate.Alpha},
	DynamicSchedulingPriority:               {Default: false, PreRelease: featuregate.Alpha},
	SchedulerCacheScrape:                    {Default: true, PreRelease: featuregate.Alpha},
	DisableSamePriorityPreemption:           {Default: false, PreRelease: featuregate.Alpha},
	SchedulerConcurrentScheduling:           {Default: true, PreRelease: featuregate.Alpha},
	SchedulerSubClusterConcurrentScheduling: {Default: true, PreRelease: featuregate.Alpha},
	EnableColocation:                        {Default: false, PreRelease: featuregate.Alpha},
	SupportRescheduling:                     {Default: false, PreRelease: featuregate.Alpha},
	ResourceReservation:                     {Default: false, PreRelease: featuregate.Alpha},
}
