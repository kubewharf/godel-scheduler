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

package handle

import (
	v1 "k8s.io/api/core/v1"
	"k8s.io/client-go/informers"
	clientset "k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/events"

	crdinformers "github.com/kubewharf/godel-scheduler-api/pkg/client/informers/externalversions"
	commonstore "github.com/kubewharf/godel-scheduler/pkg/common/store"
	framework "github.com/kubewharf/godel-scheduler/pkg/framework/api"
	unitstatus "github.com/kubewharf/godel-scheduler/pkg/util/unitstatus"
)

// PodFrameworkHandle provides data and some tools that plugins can use in Scheduler. It is
// passed to the plugin factories at the time of plugin initialization. Plugins
// must store and use this handle to call framework functions.
type PodFrameworkHandle interface {
	// SwitchType indicates the cluster binary code corresponding to the current workflow.
	// It will be used to resolve BE/GT qos.
	SwitchType() framework.SwitchType
	SubCluster() string
	SchedulerName() string

	// SnapshotSharedLister returns listers from the latest NodeInfo Snapshot. The snapshot
	// is taken at the beginning of a scheduling cycle and remains unchanged until
	// a pod finishes "Permit" point. There is no guarantee that the information
	// remains unchanged in the binding phase of scheduling, so plugins in the binding
	// cycle (pre-bind/bind/post-bind/un-reserve plugin) should not use it,
	// otherwise a concurrent read/write error might occur, they should use scheduler
	// cache instead.
	SnapshotSharedLister() framework.SharedLister

	// ClientSet returns a kubernetes clientSet.
	ClientSet() clientset.Interface
	SharedInformerFactory() informers.SharedInformerFactory
	CRDSharedInformerFactory() crdinformers.SharedInformerFactory
	GetFrameworkForPod(*v1.Pod) (framework.SchedulerFramework, error)

	FindStore(storeName commonstore.StoreName) commonstore.Store

	// TODO: cleanup the following function methods.

	// Note: The function's underlying access is Snapshot, Snapshot operations are lock-free.
	SetPotentialVictims(node string, potentialVictims []string)
	// Note: The function's underlying access is Snapshot, Snapshot operations are lock-free.
	GetPotentialVictims(node string) []string

	GetPreemptionFrameworkForPod(*v1.Pod) framework.SchedulerPreemptionFramework
	GetPreemptionPolicy(deployName string) string
	CachePreemptionPolicy(deployName string, policyName string)
	CleanupPreemptionPolicyForPodOwner()
}

type UnitFrameworkHandle interface {
	SwitchType() framework.SwitchType
	SubCluster() string
	SchedulerName() string
	EventRecorder() events.EventRecorder

	GetUnitStatus(string) unitstatus.UnitStatus
	IsAssumedPod(pod *v1.Pod) (bool, error)
	IsCachedPod(pod *v1.Pod) (bool, error)
	GetNodeInfo(nodeName string) framework.NodeInfo

	FindStore(commonstore.StoreName) commonstore.Store
}
