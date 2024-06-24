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

package loadawarestore

import (
	katalystv1alpha1 "github.com/kubewharf/katalyst-api/pkg/apis/node/v1alpha1"
	v1 "k8s.io/api/core/v1"

	commoncache "github.com/kubewharf/godel-scheduler/pkg/common/cache"
	commonstore "github.com/kubewharf/godel-scheduler/pkg/common/store"
	framework "github.com/kubewharf/godel-scheduler/pkg/framework/api"
	"github.com/kubewharf/godel-scheduler/pkg/framework/utils"
	"github.com/kubewharf/godel-scheduler/pkg/scheduler/cache/commonstores"
	"github.com/kubewharf/godel-scheduler/pkg/util/generationstore"
	podutil "github.com/kubewharf/godel-scheduler/pkg/util/pod"
)

const Name commonstore.StoreName = "LoadAwareStore"

func (s *LoadAwareStore) Name() commonstore.StoreName {
	return Name
}

func init() {
	commonstores.GlobalRegistries.Register(
		Name,
		func(h commoncache.CacheHandler) bool { return true },
		NewCache,
		NewSnapshot)
}

// ---------------------------------------------------------------------------------------

// LoadAwareStore implementing the CommonStore interface.
type LoadAwareStore struct {
	commonstore.BaseStore
	storeType commonstore.StoreType
	handler   commoncache.CacheHandler

	// NodeMetricInfo
	Store generationstore.Store
}

func NewCache(handler commoncache.CacheHandler) commonstore.Store {
	return &LoadAwareStore{
		BaseStore: commonstore.NewBaseStore(),
		storeType: commonstore.Cache,
		handler:   handler,

		Store: generationstore.NewListStore(),
	}
}

func NewSnapshot(handler commoncache.CacheHandler) commonstore.Store {
	return &LoadAwareStore{
		BaseStore: commonstore.NewBaseStore(),
		storeType: commonstore.Snapshot,
		handler:   handler,

		Store: generationstore.NewRawStore(),
	}
}

// -------------------------------------- ClusterCache --------------------------------------

func (s *LoadAwareStore) AddCNR(cnr *katalystv1alpha1.CustomNodeResource) error {
	return s.nodeMetricOp(cnr, true)
}

func (s *LoadAwareStore) UpdateCNR(oldCNR, newCNR *katalystv1alpha1.CustomNodeResource) error {
	if err := s.nodeMetricOp(oldCNR, false); err != nil {
		return err
	}
	return s.nodeMetricOp(newCNR, true)
}

func (s *LoadAwareStore) DeleteCNR(cnr *katalystv1alpha1.CustomNodeResource) error {
	return s.nodeMetricOp(cnr, true)
}

func (s *LoadAwareStore) AddPod(pod *v1.Pod) error {
	if !podutil.BoundPod(pod) && !podutil.AssumedPodOfGodel(pod, s.handler.SchedulerType()) {
		return nil
	}
	return s.podOp(pod, true)
}

func (s *LoadAwareStore) UpdatePod(oldPod *v1.Pod, newPod *v1.Pod) error {
	// Remove the oldPod if existed.
	{
		key, err := framework.GetPodKey(oldPod)
		if err != nil {
			return err
		}
		if ps, _ := s.handler.GetPodState(key); ps != nil {
			// Use the pod stored in Cache instead of oldPod.
			if err := s.DeletePod(ps.Pod); err != nil {
				return err
			}
		}
	}
	// Add the newPod if needed.
	{
		if err := s.AddPod(newPod); err != nil {
			return err
		}
	}
	return nil
}

func (s *LoadAwareStore) DeletePod(pod *v1.Pod) error {
	if !podutil.BoundPod(pod) && !podutil.AssumedPodOfGodel(pod, s.handler.SchedulerType()) {
		return nil
	}
	return s.podOp(pod, false)
}

func (s *LoadAwareStore) AssumePod(podInfo *framework.CachePodInfo) error {
	return s.podOp(podInfo.Pod, true)
}

func (s *LoadAwareStore) ForgetPod(podInfo *framework.CachePodInfo) error {
	return s.podOp(podInfo.Pod, false)
}

// UpdateSnapshot synchronize the data in the Cache to Snapshot, generally using generationstore for
// incremental updates.
func (s *LoadAwareStore) UpdateSnapshot(store commonstore.Store) error {
	cache, snapshot := framework.TransferGenerationStore(s.Store, store.(*LoadAwareStore).Store)
	cache.UpdateRawStore(
		snapshot,
		func(key string, so generationstore.StoredObj) {
			snapshot.Set(key, so.(*NodeMetricInfo).Clone())
		},
		generationstore.DefaultCleanFunc(cache, snapshot),
	)
	return nil
}

// -------------------------------------- Internal Function --------------------------------------

func (s *LoadAwareStore) podOp(pod *v1.Pod, isAdd bool) error {
	nodeName := utils.GetNodeNameFromPod(pod)
	if nodeName == "" {
		return nil
	}
	pInfo, err := newPodBasicInfo(pod)
	if err != nil {
		return err
	}

	var nodeMetricInfo *NodeMetricInfo
	if isAdd {
		if nodeMetricInfoObj := s.Store.Get(nodeName); nodeMetricInfoObj != nil {
			nodeMetricInfo = nodeMetricInfoObj.(*NodeMetricInfo)
		} else {
			nodeMetricInfo = NewNodeMetricInfo(nodeName, nil)
		}
		nodeMetricInfo.PodOp(pInfo, s.storeType == commonstore.Cache, true)
		s.Store.Set(nodeName, nodeMetricInfo)
	} else {
		if nodeMetricInfoObj := s.Store.Get(nodeName); nodeMetricInfoObj != nil {
			nodeMetricInfo = nodeMetricInfoObj.(*NodeMetricInfo)
		} else {
			return nil
		}
		nodeMetricInfo.PodOp(pInfo, s.storeType == commonstore.Cache, false)
		if nodeMetricInfo.CanBeRecycle() {
			s.Store.Delete(nodeName)
		} else {
			s.Store.Set(nodeName, nodeMetricInfo)
		}
	}
	return nil
}

func (s *LoadAwareStore) nodeMetricOp(cnr *katalystv1alpha1.CustomNodeResource, isAdd bool) error {
	nodeName := cnr.Name
	if len(nodeName) == 0 {
		return nil
	}

	var nodeMetricInfo *NodeMetricInfo
	if isAdd {
		if nodeMetricInfoObj := s.Store.Get(nodeName); nodeMetricInfoObj != nil {
			nodeMetricInfo = nodeMetricInfoObj.(*NodeMetricInfo)
			nodeMetricInfo.Reset(cnr)
		} else {
			nodeMetricInfo = NewNodeMetricInfo(nodeName, cnr)
		}
		s.Store.Set(nodeName, nodeMetricInfo)
	} else {
		if nodeMetricInfoObj := s.Store.Get(nodeName); nodeMetricInfoObj != nil {
			nodeMetricInfo = nodeMetricInfoObj.(*NodeMetricInfo)
		} else {
			return nil
		}
		nodeMetricInfo.Reset(nil) // Use nil to delete cnr informations.
		if nodeMetricInfo.CanBeRecycle() {
			s.Store.Delete(nodeName)
		} else {
			s.Store.Set(nodeName, nodeMetricInfo)
		}
	}
	return nil
}

// -------------------------------------- Other Interface --------------------------------------

type StoreHandle interface {
	GetLoadAwareNodeMetricInfo(nodeName string, resourceType podutil.PodResourceType) *framework.LoadAwareNodeMetricInfo
	GetLoadAwareNodeUsage(nodeName string, resourceType podutil.PodResourceType) *framework.LoadAwareNodeUsage
}

var _ StoreHandle = &LoadAwareStore{}

func (s *LoadAwareStore) GetLoadAwareNodeMetricInfo(nodeName string, resourceType podutil.PodResourceType) *framework.LoadAwareNodeMetricInfo {
	nodeMetricInfoObj := s.Store.Get(nodeName)
	if nodeMetricInfoObj == nil {
		return nil
	}

	nodeMetricInfo := nodeMetricInfoObj.(*NodeMetricInfo)
	if !nodeMetricInfo.cnrExist {
		return nil
	}

	var cpuUsage, memUsage int64
	if resourceType == podutil.GuaranteedPod {
		cpuUsage = nodeMetricInfo.gtPodMetricInfos.ProfileMilliCPU
		memUsage = nodeMetricInfo.gtPodMetricInfos.ProfileMEM
	} else {
		cpuUsage = nodeMetricInfo.bePodMetricInfos.ProfileMilliCPU
		memUsage = nodeMetricInfo.bePodMetricInfos.ProfileMEM
	}

	return &framework.LoadAwareNodeMetricInfo{
		Name:                 nodeName,
		UpdateTime:           nodeMetricInfo.updateTime,
		ProfileMilliCPUUsage: cpuUsage,
		ProfileMEMUsage:      memUsage,
	}
}

func (s *LoadAwareStore) GetLoadAwareNodeUsage(nodeName string, resourceType podutil.PodResourceType) *framework.LoadAwareNodeUsage {
	nodeMetricInfoObj := s.Store.Get(nodeName)
	if nodeMetricInfoObj == nil {
		// Considering that the function will be called during the scheduling process, the node must exist.
		// Otherwise, it will naturally panic.
		nodeInfo := s.handler.GetNodeInfo(nodeName)
		var resource *framework.Resource
		if resourceType == podutil.GuaranteedPod {
			resource = nodeInfo.GetGuaranteedRequested()
		} else {
			resource = nodeInfo.GetBestEffortRequested()
		}
		return &framework.LoadAwareNodeUsage{
			RequestMilliCPU: resource.MilliCPU,
			RequestMEM:      resource.Memory,
		}
	}

	nodeMetricInfo := nodeMetricInfoObj.(*NodeMetricInfo)
	var podMetricsInfos *PodMetricInfos
	if resourceType == podutil.GuaranteedPod {
		podMetricsInfos = nodeMetricInfo.gtPodMetricInfos
	} else {
		podMetricsInfos = nodeMetricInfo.bePodMetricInfos
	}
	return &framework.LoadAwareNodeUsage{
		ProfileMilliCPU: podMetricsInfos.ProfileMilliCPU,
		ProfileMEM:      podMetricsInfos.ProfileMEM,
		RequestMilliCPU: podMetricsInfos.RequestMilliCPU,
		RequestMEM:      podMetricsInfos.RequestMEM,
	}
}
