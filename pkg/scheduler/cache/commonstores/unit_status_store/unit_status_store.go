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
	schedulingv1a1 "github.com/kubewharf/godel-scheduler-api/pkg/apis/scheduling/v1alpha1"
	v1 "k8s.io/api/core/v1"

	framework "github.com/kubewharf/godel-scheduler/pkg/framework/api"
	"github.com/kubewharf/godel-scheduler/pkg/framework/utils"
	"github.com/kubewharf/godel-scheduler/pkg/scheduler/cache/commonstores"
	"github.com/kubewharf/godel-scheduler/pkg/scheduler/cache/handler"
	podutil "github.com/kubewharf/godel-scheduler/pkg/util/pod"
	unitutil "github.com/kubewharf/godel-scheduler/pkg/util/unit"
	unitstatus "github.com/kubewharf/godel-scheduler/pkg/util/unitstatus"
)

const Name commonstores.StoreName = "UnitStatusStore"

func (c *UnitStatusStore) Name() commonstores.StoreName {
	return Name
}

func init() {
	commonstores.GlobalRegistry.Register(
		Name,
		func(h handler.CacheHandler) bool { return true },
		NewCache,
		NewSnapshot)
}

// ---------------------------------------------------------------------------------------

type UnitStatusStore struct {
	commonstores.BaseStore
	storeType commonstores.StoreType
	handler   handler.CacheHandler

	Store *unitstatus.UnitStatusMap // Only be used in Cache.
}

var _ commonstores.CommonStore = &UnitStatusStore{}

func NewCache(handler handler.CacheHandler) commonstores.CommonStore {
	return &UnitStatusStore{
		BaseStore: commonstores.NewBaseStore(),
		storeType: commonstores.Cache,
		handler:   handler,

		Store: unitstatus.NewUnitStatusMap(),
	}
}

func NewSnapshot(handler handler.CacheHandler) commonstores.CommonStore {
	return &UnitStatusStore{
		BaseStore: commonstores.NewBaseStore(),
		storeType: commonstores.Snapshot,
		handler:   handler,

		Store: unitstatus.NewUnitStatusMap(),
	}
}

func (s *UnitStatusStore) AddPod(pod *v1.Pod) error {
	if !podutil.BoundPod(pod) && !podutil.AssumedPodOfGodel(pod, s.handler.SchedulerType(), s.handler.TakeOverDefaultScheduler()) {
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
			if err := s.RemovePod(ps.Pod); err != nil {
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

func (s *UnitStatusStore) RemovePod(pod *v1.Pod) error {
	if !podutil.BoundPod(pod) && !podutil.AssumedPodOfGodel(pod, s.handler.SchedulerType(), s.handler.TakeOverDefaultScheduler()) {
		return nil
	}
	return s.podOp(pod, false)
}

func (s *UnitStatusStore) AddPodGroup(podGroup *schedulingv1a1.PodGroup) error {
	s.Store.SetUnitSchedulingStatus(unitutil.GetUnitKeyFromPodGroup(unitutil.GetPodGroupKey(podGroup)), unitstatus.ParsePodGroupSchedulingStatus(podGroup))
	return nil
}

func (s *UnitStatusStore) UpdatePodGroup(oldPodGroup, newPodGroup *schedulingv1a1.PodGroup) error {
	s.Store.DeleteUnitSchedulingStatus(unitutil.GetUnitKeyFromPodGroup(unitutil.GetPodGroupKey(oldPodGroup)))
	s.Store.SetUnitSchedulingStatus(unitutil.GetUnitKeyFromPodGroup(unitutil.GetPodGroupKey(newPodGroup)), unitstatus.ParsePodGroupSchedulingStatus(newPodGroup))
	return nil
}

func (s *UnitStatusStore) RemovePodGroup(podGroup *schedulingv1a1.PodGroup) error {
	s.Store.DeleteUnitSchedulingStatus(unitutil.GetUnitKeyFromPodGroup(unitutil.GetPodGroupKey(podGroup)))
	return nil
}

func (s *UnitStatusStore) AssumePod(podInfo *framework.CachePodInfo) error {
	if s.storeType == commonstores.Snapshot {
		return nil
	}
	return s.podOp(podInfo.Pod, true)
}

func (s *UnitStatusStore) ForgetPod(podInfo *framework.CachePodInfo) error {
	if s.storeType == commonstores.Snapshot {
		return nil
	}
	return s.podOp(podInfo.Pod, false)
}

func (s *UnitStatusStore) UpdateSnapshot(_ commonstores.CommonStore) error {
	return nil
}

// -------------------------------------- Other Interface --------------------------------------

func (s *UnitStatusStore) podOp(pod *v1.Pod, isAdd bool) error {
	if isAdd {
		s.Store.AddUnitRunningPods(utils.GetUnitIdentifier(pod), pod)
	} else {
		s.Store.DeleteUnitRunningPods(utils.GetUnitIdentifier(pod), pod)
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
