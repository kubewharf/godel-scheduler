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

package deletedmarkerstore

import (
	"fmt"
	"sync"
	"time"

	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/klog/v2"

	"github.com/kubewharf/godel-scheduler/pkg/binder/cache/commonstores"
	commoncache "github.com/kubewharf/godel-scheduler/pkg/common/cache"
	commonstore "github.com/kubewharf/godel-scheduler/pkg/common/store"
	framework "github.com/kubewharf/godel-scheduler/pkg/framework/api"
	podutil "github.com/kubewharf/godel-scheduler/pkg/util/pod"
)

const Name commonstore.StoreName = "DeletedMarkerStore"

func (dms *DeletedMarkerStore) Name() commonstore.StoreName {
	return Name
}

func init() {
	commonstores.GlobalRegistries.Register(
		Name,
		func(h commoncache.CacheHandler) bool { return true },
		NewCacheDeletedMarkerStore,
		NewSnapshotDeletedMarkerStore)
}

// -------------------------------------- DeletedMarkerStore --------------------------------------

type DeletedMarkerStore struct {
	commonstore.BaseStore
	storeType commonstore.StoreType
	handler   commoncache.CacheHandler

	Store map[string]*markInfo
}

var _ commonstore.Store = &DeletedMarkerStore{}

func NewCacheDeletedMarkerStore(handler commoncache.CacheHandler) commonstore.Store {
	return &DeletedMarkerStore{
		BaseStore: commonstore.NewBaseStore(),
		storeType: commonstore.Cache,
		handler:   handler,

		Store: make(map[string]*markInfo),
	}
}

func NewSnapshotDeletedMarkerStore(handler commoncache.CacheHandler) commonstore.Store {
	return &DeletedMarkerStore{
		BaseStore: commonstore.NewBaseStore(),
		storeType: commonstore.Snapshot,
		handler:   handler,

		Store: make(map[string]*markInfo),
	}
}

func (dms *DeletedMarkerStore) AddPod(pod *v1.Pod) error {
	if !podutil.BoundPod(pod) {
		return nil
	}
	// Do nothing for DeletedMarkerStore.
	return nil
}

func (dms *DeletedMarkerStore) UpdatePod(oldPod *v1.Pod, newPod *v1.Pod) error {
	key, err := framework.GetPodKey(oldPod)
	if err != nil {
		return err
	}
	ps, _ := dms.handler.GetPodState(key)

	// TODO: revisit this rule.
	if ps != nil && !podutil.BoundPod(newPod) {
		key, err := framework.GetPodKey(newPod)
		if err != nil {
			return err
		}
		delete(dms.Store, key)
	}
	return nil
}

func (dms *DeletedMarkerStore) DeletePod(pod *v1.Pod) error {
	if !podutil.BoundPod(pod) {
		return nil
	}
	key, err := framework.GetPodKey(pod)
	if err != nil {
		return err
	}
	delete(dms.Store, key)
	return nil
}

func (dms *DeletedMarkerStore) UpdateSnapshot(_ commonstore.Store) error {
	return nil
}

func (dms *DeletedMarkerStore) PeriodWorker(mu *sync.RWMutex) {
	go wait.Until(func() {
		dms.CleanupPodDeletionMarker(mu, time.Now())
	}, dms.handler.Period(), dms.handler.StopCh())
}

// -------------------------------------- Other Interface --------------------------------------

const PodMarkerExpirationPeriod = 600 * time.Second

type markInfo struct {
	preemptorKey         string
	markerExpirationTime *time.Time
}

func (dms *DeletedMarkerStore) MarkPodToDelete(pod, preemptor *v1.Pod) error {
	key, err := framework.GetPodKey(pod)
	if err != nil {
		return err
	}
	pKey, err := framework.GetPodKey(preemptor)
	if err != nil {
		return err
	}

	ps, assumed := dms.handler.GetPodState(key)
	if ps != nil && !assumed {
		if ps.Pod.Spec.NodeName != pod.Spec.NodeName {
			klog.InfoS("Pod was assumed to be on the assumed node but got added to the current node", "podUID", key, "pod", klog.KObj(pod), "assumedNode", pod.Spec.NodeName, "currentNode", ps.Pod.Spec.NodeName)
			klog.ErrorS(nil, "Binder cache was corrupted and can badly affect scheduling decisions")
			klog.FlushAndExit(klog.ExitFlushTimeout, 1)
		}
		dl := time.Now().Add(PodMarkerExpirationPeriod)
		info := &markInfo{
			preemptorKey:         pKey,
			markerExpirationTime: &dl,
		}
		dms.Store[key] = info
		return nil
	}

	return fmt.Errorf("pod %v is not found in binder cache, so cannot be marked to delete", key)
}

func (dms *DeletedMarkerStore) RemoveDeletePodMarker(pod, preemptor *v1.Pod) error {
	podKey, err := framework.GetPodKey(pod)
	if err != nil {
		return err
	}
	preemptorKey, err := framework.GetPodKey(preemptor)
	if err != nil {
		return err
	}

	info, ok := dms.Store[podKey]
	if !ok {
		return nil
	}
	if info.preemptorKey != preemptorKey {
		return fmt.Errorf("Pod Marker isn't set by this preemptor %s", preemptorKey)
	}
	delete(dms.Store, podKey)
	return nil
}

func (dms *DeletedMarkerStore) RemoveDeletePodMarkerByKey(podKey, preemptorKey string) error {
	info, ok := dms.Store[podKey]
	if !ok {
		return nil
	}
	if info.preemptorKey != preemptorKey {
		return fmt.Errorf("Pod Marker isn't set by this preemptor %s", preemptorKey)
	}
	delete(dms.Store, podKey)
	return nil
}

func (dms *DeletedMarkerStore) IsPodMarkedToDelete(pod *v1.Pod) (bool, error) {
	key, err := framework.GetPodKey(pod)
	if err != nil {
		return false, err
	}

	info, ok := dms.Store[key]
	return ok && info != nil, nil
}

func (dms *DeletedMarkerStore) CleanupPodDeletionMarker(mu *sync.RWMutex, now time.Time) {
	mu.Lock()
	defer mu.Unlock()

	for key, info := range dms.Store {
		ps, _ := dms.handler.GetPodState(key)
		if ps == nil {
			klog.ErrorS(nil, "Key found in podsMarkedToBeDeleted set but not in podStates. Potentially a logical error.")
			klog.FlushAndExit(klog.ExitFlushTimeout, 1)
		}

		if now.After(*info.markerExpirationTime) {
			klog.InfoS("WARN: Pod deletion marker expired", "pod", klog.KObj(ps.Pod))
			delete(dms.Store, key)
		}
	}
}
