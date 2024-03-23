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

package testing

import (
	"errors"
	"fmt"
	"time"

	schedulingv1a1 "github.com/kubewharf/godel-scheduler-api/pkg/apis/scheduling/v1alpha1"
	godelclient "github.com/kubewharf/godel-scheduler-api/pkg/client/clientset/versioned"
	crdinformers "github.com/kubewharf/godel-scheduler-api/pkg/client/informers/externalversions"
	v1 "k8s.io/api/core/v1"
	"k8s.io/client-go/informers"
	clientset "k8s.io/client-go/kubernetes"

	framework "github.com/kubewharf/godel-scheduler/pkg/framework/api"
	frameworkconfig "github.com/kubewharf/godel-scheduler/pkg/framework/api/config"
	godelcache "github.com/kubewharf/godel-scheduler/pkg/scheduler/cache"
	"github.com/kubewharf/godel-scheduler/pkg/scheduler/cache/isolatedcache"
	schedulerruntime "github.com/kubewharf/godel-scheduler/pkg/scheduler/framework/runtime"
	"github.com/kubewharf/godel-scheduler/pkg/scheduler/util"
	"github.com/kubewharf/godel-scheduler/pkg/util/constraints"
	podutil "github.com/kubewharf/godel-scheduler/pkg/util/pod"
)

const TestSchedulerName = "test-scheduler"

type MockSchedulerFrameworkHandle struct {
	clientSet                clientset.Interface
	crdClient                godelclient.Interface
	informerFactory          informers.SharedInformerFactory
	crdInformerFactory       crdinformers.SharedInformerFactory
	nodeInfoSnapshot         *godelcache.Snapshot
	podGroupInfoCache        map[string]*schedulingv1a1.PodGroup
	pluginRegistry           framework.PluginMap
	preemptionPluginRegistry framework.PluginMap
	orderedPluginRegistry    framework.PluginList
	cache                    godelcache.SchedulerCache
	potentialVictimsInNodes  *framework.PotentialVictimsInNodes
	basePlugins              *framework.PluginCollection
	isolationCache           isolatedcache.IsolatedCache
}

func (mfh *MockSchedulerFrameworkHandle) SwitchType() framework.SwitchType {
	return framework.SwitchTypeAll
}

func (mfh *MockSchedulerFrameworkHandle) SubCluster() string {
	return ""
}

func (mfh *MockSchedulerFrameworkHandle) SchedulerName() string {
	return ""
}

func (mfh *MockSchedulerFrameworkHandle) SnapshotSharedLister() framework.SharedLister {
	return mfh.nodeInfoSnapshot
}

func (mfh *MockSchedulerFrameworkHandle) ClientSet() clientset.Interface {
	return mfh.clientSet
}

func (mfh *MockSchedulerFrameworkHandle) SharedInformerFactory() informers.SharedInformerFactory {
	return mfh.informerFactory
}

func (mfh *MockSchedulerFrameworkHandle) CRDSharedInformerFactory() crdinformers.SharedInformerFactory {
	return mfh.crdInformerFactory
}

func (mfh *MockSchedulerFrameworkHandle) GetPDBItemList() []framework.PDBItem {
	return mfh.nodeInfoSnapshot.GetPDBItemList()
}

func (mfh *MockSchedulerFrameworkHandle) GetPDBItemListForOwner(ownerType, ownerKey string) (bool, bool, []string) {
	return mfh.nodeInfoSnapshot.GetPDBItemListForOwner(ownerType, ownerKey)
}

func (mfh *MockSchedulerFrameworkHandle) GetOwnerLabels(ownerType, ownerKey string) map[string]string {
	return mfh.nodeInfoSnapshot.GetOwnerLabels(ownerType, ownerKey)
}

func (mfh *MockSchedulerFrameworkHandle) GetLoadAwareNodeMetricInfo(nodeName string, resourceType podutil.PodResourceType) *framework.LoadAwareNodeMetricInfo {
	return mfh.nodeInfoSnapshot.GetLoadAwareNodeMetricInfo(nodeName, resourceType)
}

func (mfh *MockSchedulerFrameworkHandle) GetLoadAwareNodeUsage(nodeName string, resourceType podutil.PodResourceType) *framework.LoadAwareNodeUsage {
	return mfh.nodeInfoSnapshot.GetLoadAwareNodeUsage(nodeName, resourceType)
}

func (mfh *MockSchedulerFrameworkHandle) retrievePluginsFromPodConstraints(pod *v1.Pod, constraintAnnotationKey string) (*framework.PluginCollection, error) {
	podConstraints, err := frameworkconfig.GetConstraints(pod, constraintAnnotationKey)
	if err != nil {
		return nil, err
	}
	size := len(podConstraints)
	specs := make([]*framework.PluginSpec, size)
	for index, constraint := range podConstraints {
		specs[index] = framework.NewPluginSpecWithWeight(constraint.PluginName, constraint.Weight)
	}
	switch constraintAnnotationKey {
	case constraints.HardConstraintsAnnotationKey:
		return &framework.PluginCollection{
			Filters: specs,
		}, nil
	case constraints.SoftConstraintsAnnotationKey:
		return &framework.PluginCollection{
			Scores: specs,
		}, nil
	default:
		return nil, fmt.Errorf("unsupported constraintType %v", constraintAnnotationKey)
	}
}

func (mfh *MockSchedulerFrameworkHandle) GetFrameworkForPod(pod *v1.Pod) (f framework.SchedulerFramework, err error) {
	var hardConstraints, softConstraints *framework.PluginCollection
	if hardConstraints, err = mfh.retrievePluginsFromPodConstraints(pod, constraints.HardConstraintsAnnotationKey); err != nil {
		return
	}
	if softConstraints, err = mfh.retrievePluginsFromPodConstraints(pod, constraints.SoftConstraintsAnnotationKey); err != nil {
		return
	}
	return NewSchedulerPodFramework(mfh.pluginRegistry, mfh.orderedPluginRegistry, mfh.basePlugins, hardConstraints, softConstraints)
}

func (mfh *MockSchedulerFrameworkHandle) GetPodGroupInfo(podGroupName string) (*schedulingv1a1.PodGroup, error) {
	if len(mfh.podGroupInfoCache) == 0 {
		return nil, nil
	}

	if pg, ok := mfh.podGroupInfoCache[podGroupName]; ok {
		return pg, nil
	}

	return nil, errors.New("not found")
}

func (mfh *MockSchedulerFrameworkHandle) SetPotentialVictims(node string, potentialVictims []string) {
	mfh.potentialVictimsInNodes.SetPotentialVictims(node, potentialVictims)
}

func (mfh *MockSchedulerFrameworkHandle) GetPotentialVictims(node string) []string {
	return mfh.potentialVictimsInNodes.GetPotentialVictims(node)
}

func NewSchedulerPodFramework(pluginRegistry framework.PluginMap, orderedPluginRegistry framework.PluginList, basePlugins, hardConstraints, softConstraints *framework.PluginCollection) (framework.SchedulerFramework, error) {
	recorder := schedulerruntime.NewMetricsRecorder(1000, time.Second, framework.DefaultSubClusterSwitchType, framework.DefaultSubCluster, TestSchedulerName)
	pluginOrder := util.GetListIndex(orderedPluginRegistry)
	return schedulerruntime.NewPodFramework(pluginRegistry, pluginOrder, basePlugins, hardConstraints, softConstraints, recorder)
}

func (mfh *MockSchedulerFrameworkHandle) GetPreemptionFrameworkForPod(_ *v1.Pod) framework.SchedulerPreemptionFramework {
	return schedulerruntime.NewPreemptionFramework(mfh.preemptionPluginRegistry, mfh.basePlugins)
}

func (mfh *MockSchedulerFrameworkHandle) GetPreemptionPolicy(deployName string) string {
	return mfh.isolationCache.GetPreemptionPolicy(deployName)
}

func (mfh *MockSchedulerFrameworkHandle) CachePreemptionPolicy(deployName string, policyName string) {
	mfh.isolationCache.CachePreemptionPolicy(deployName, policyName)
}

func (mfh *MockSchedulerFrameworkHandle) CleanupPreemptionPolicyForPodOwner() {}

func NewSchedulerFrameworkHandle(
	client clientset.Interface,
	crdClient godelclient.Interface,
	informerFactory informers.SharedInformerFactory,
	crdInformerFactory crdinformers.SharedInformerFactory,
	cache godelcache.SchedulerCache,
	snapshot *godelcache.Snapshot,
	pluginRegistry framework.PluginMap,
	preemptionPluginRegistry framework.PluginMap,
	orderedPluginRegistry framework.PluginList,
	basePlugins *framework.PluginCollection,
) (framework.SchedulerFrameworkHandle, error) {
	return &MockSchedulerFrameworkHandle{
		clientSet:                client,
		crdClient:                crdClient,
		informerFactory:          informerFactory,
		crdInformerFactory:       crdInformerFactory,
		nodeInfoSnapshot:         snapshot,
		pluginRegistry:           pluginRegistry,
		preemptionPluginRegistry: preemptionPluginRegistry,
		orderedPluginRegistry:    orderedPluginRegistry,
		cache:                    cache,
		potentialVictimsInNodes:  framework.NewPotentialVictimsInNodes(),
		basePlugins:              basePlugins,
		isolationCache:           isolatedcache.NewIsolatedCache(),
	}, nil
}

func NewSchedulerFrameworkHandleWithPodGroup(
	client clientset.Interface,
	crdClient godelclient.Interface,
	informerFactory informers.SharedInformerFactory,
	snapshot *godelcache.Snapshot,
	podGroupInfoCache map[string]*schedulingv1a1.PodGroup,
) (framework.SchedulerFrameworkHandle, error) {
	return &MockSchedulerFrameworkHandle{
		clientSet:               client,
		crdClient:               crdClient,
		informerFactory:         informerFactory,
		nodeInfoSnapshot:        snapshot,
		podGroupInfoCache:       podGroupInfoCache,
		potentialVictimsInNodes: framework.NewPotentialVictimsInNodes(),
	}, nil
}
