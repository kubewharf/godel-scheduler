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

package unitstatusstore

import (
	v1 "k8s.io/api/core/v1"

	schedulingv1a1 "github.com/kubewharf/godel-scheduler-api/pkg/apis/scheduling/v1alpha1"
	"github.com/kubewharf/godel-scheduler/pkg/binder/cache/commonstores"
	commoncache "github.com/kubewharf/godel-scheduler/pkg/common/cache"
	commonstore "github.com/kubewharf/godel-scheduler/pkg/common/store"
	framework "github.com/kubewharf/godel-scheduler/pkg/framework/api"
	"github.com/kubewharf/godel-scheduler/pkg/framework/utils"
	podutil "github.com/kubewharf/godel-scheduler/pkg/util/pod"
	unitutil "github.com/kubewharf/godel-scheduler/pkg/util/unit"
	unitstatus "github.com/kubewharf/godel-scheduler/pkg/util/unitstatus"
)

const Name commonstore.StoreName = "UnitStatusStore"

func (s *UnitStatusStore) Name() commonstore.StoreName {
	return Name
}

func init() {
	commonstores.GlobalRegistries.Register(
		Name,
		func(h commoncache.CacheHandler) bool { return true },
		NewCache,
		NewSnapshot)
}

// -------------------------------------- UnitStatusStore --------------------------------------

type UnitStatusStore struct {
	commonstore.BaseStore
	storeType commonstore.StoreType
	handler   commoncache.CacheHandler

	Store *unitstatus.UnitStatusMap // Only be used in Cache.
}

var _ commonstore.Store = &UnitStatusStore{}

func NewCache(handler commoncache.CacheHandler) commonstore.Store {
	return &UnitStatusStore{
		BaseStore: commonstore.NewBaseStore(),
		storeType: commonstore.Cache,
		handler:   handler,

		Store: unitstatus.NewUnitStatusMap(),
	}
}

func NewSnapshot(handler commoncache.CacheHandler) commonstore.Store {
	return &UnitStatusStore{
		BaseStore: commonstore.NewBaseStore(),
		storeType: commonstore.Snapshot,
		handler:   handler,

		Store: unitstatus.NewUnitStatusMap(),
	}
}

func (s *UnitStatusStore) AddPod(pod *v1.Pod) error {
	if !podutil.BoundPod(pod) {
		return nil
	}
	return s.podOp(pod, true)
}

func (s *UnitStatusStore) UpdatePod(oldPod *v1.Pod, newPod *v1.Pod) error {
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

func (s *UnitStatusStore) DeletePod(pod *v1.Pod) error {
	if !podutil.BoundPod(pod) {
		return nil
	}
	return s.podOp(pod, false)
}

func (s *UnitStatusStore) AddPodGroup(podGroup *schedulingv1a1.PodGroup) error {
	s.Store.SetUnitSchedulingStatus(GetUnitKeyFromPodGroup(unitutil.GetPodGroupKey(podGroup)), unitstatus.ParsePodGroupSchedulingStatus(podGroup))
	return nil
}

func (s *UnitStatusStore) UpdatePodGroup(oldPodGroup, newPodGroup *schedulingv1a1.PodGroup) error {
	s.Store.DeleteUnitSchedulingStatus(GetUnitKeyFromPodGroup(unitutil.GetPodGroupKey(oldPodGroup)))
	s.Store.SetUnitSchedulingStatus(GetUnitKeyFromPodGroup(unitutil.GetPodGroupKey(newPodGroup)), unitstatus.ParsePodGroupSchedulingStatus(newPodGroup))
	return nil
}

func (s *UnitStatusStore) RemovePodGroup(podGroup *schedulingv1a1.PodGroup) error {
	s.Store.DeleteUnitSchedulingStatus(GetUnitKeyFromPodGroup(unitutil.GetPodGroupKey(podGroup)))
	return nil
}

func (s *UnitStatusStore) AssumePod(podInfo *framework.CachePodInfo) error {
	if s.storeType == commonstore.Snapshot {
		return nil
	}
	return s.podOp(podInfo.Pod, true)
}

func (s *UnitStatusStore) ForgetPod(podInfo *framework.CachePodInfo) error {
	if s.storeType == commonstore.Snapshot {
		return nil
	}
	return s.podOp(podInfo.Pod, false)
}

func (s *UnitStatusStore) UpdateSnapshot(_ commonstore.Store) error {
	return nil
}

// -------------------------------------- Other Interface --------------------------------------

func (s *UnitStatusStore) podOp(pod *v1.Pod, isAdd bool) error {
	unitKey := utils.GetUnitIdentifier(pod)
	if isAdd {
		s.Store.AddUnitRunningPods(unitKey, pod)
	} else {
		s.Store.DeleteUnitRunningPods(unitKey, pod)
	}
	return nil
}

func (s *UnitStatusStore) GetUnitStatus(unitKey string) unitstatus.UnitStatus {
	return s.Store.GetUnitStatus(unitKey)
}

func (s *UnitStatusStore) SetUnitSchedulingStatus(unitKey string, status unitstatus.SchedulingStatus) {
	s.Store.SetUnitSchedulingStatus(unitKey, status)
}

func (s *UnitStatusStore) GetUnitSchedulingStatus(unitKey string) unitstatus.SchedulingStatus {
	return s.Store.GetUnitSchedulingStatus(unitKey)
}

func GetUnitKeyFromPodGroup(key string) string {
	return string(framework.PodGroupUnitType) + "/" + key
}
