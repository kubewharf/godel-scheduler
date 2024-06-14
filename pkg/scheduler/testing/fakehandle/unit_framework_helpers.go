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

package fakehandle

import (
	v1 "k8s.io/api/core/v1"
	"k8s.io/client-go/tools/events"

	commonstore "github.com/kubewharf/godel-scheduler/pkg/common/store"
	framework "github.com/kubewharf/godel-scheduler/pkg/framework/api"
	"github.com/kubewharf/godel-scheduler/pkg/scheduler/cache"
	"github.com/kubewharf/godel-scheduler/pkg/scheduler/framework/handle"
	unitstatus "github.com/kubewharf/godel-scheduler/pkg/util/unitstatus"
)

type MockUnitFrameworkHandle struct {
	Cache    cache.SchedulerCache
	Snapshot *cache.Snapshot
}

var _ handle.UnitFrameworkHandle = &MockUnitFrameworkHandle{}

func NewMockUnitFrameworkHandle(
	cache cache.SchedulerCache,
	snapshot *cache.Snapshot,
) *MockUnitFrameworkHandle {
	gs := &MockUnitFrameworkHandle{
		Cache:    cache,
		Snapshot: snapshot,
	}
	return gs
}

// ---------------------------------------------------------------------------------------------------------

func (gs *MockUnitFrameworkHandle) SchedulerName() string {
	return ""
}

func (gs *MockUnitFrameworkHandle) SwitchType() framework.SwitchType {
	return 0
}

func (gs *MockUnitFrameworkHandle) SubCluster() string {
	return ""
}

func (gs *MockUnitFrameworkHandle) EventRecorder() events.EventRecorder {
	return nil
}

func (gs *MockUnitFrameworkHandle) DisablePreemption() bool {
	return false
}

func (gs *MockUnitFrameworkHandle) GetUnitStatus(unitKey string) unitstatus.UnitStatus {
	return gs.Cache.GetUnitStatus(unitKey)
}

func (gs *MockUnitFrameworkHandle) IsCachedPod(pod *v1.Pod) (bool, error) {
	return gs.Cache.IsCachedPod(pod)
}

func (gs *MockUnitFrameworkHandle) GetNodeInfo(nodeName string) framework.NodeInfo {
	return gs.Snapshot.GetNodeInfo(nodeName)
}

func (mfh *MockUnitFrameworkHandle) FindStore(storeName commonstore.StoreName) commonstore.Store {
	return mfh.Snapshot.FindStore(storeName)
}

func (gs *MockUnitFrameworkHandle) IsAssumedPod(pod *v1.Pod) (bool, error) {
	return gs.Cache.IsAssumedPod(pod)
}
