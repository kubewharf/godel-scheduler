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

package cache

import (
	"sync"
	"time"

	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/apimachinery/pkg/util/wait"

	"github.com/kubewharf/godel-scheduler/pkg/binder/cache/commonstores"
	deletedmarkerstore "github.com/kubewharf/godel-scheduler/pkg/binder/cache/commonstores/deleted_marker_store"
	nodestore "github.com/kubewharf/godel-scheduler/pkg/binder/cache/commonstores/node_store"
	podstore "github.com/kubewharf/godel-scheduler/pkg/binder/cache/commonstores/pod_store"
	unitstatusstore "github.com/kubewharf/godel-scheduler/pkg/binder/cache/commonstores/unit_status_store"
	"github.com/kubewharf/godel-scheduler/pkg/binder/metrics"
	commoncache "github.com/kubewharf/godel-scheduler/pkg/common/cache"
	commonstore "github.com/kubewharf/godel-scheduler/pkg/common/store"
	framework "github.com/kubewharf/godel-scheduler/pkg/framework/api"
	unitstatus "github.com/kubewharf/godel-scheduler/pkg/util/unitstatus"
)

// New returns a BinderCache implementation.
// It automatically starts a go routine that manages expiration of assumed pods.
// "ttl" is how long the assumed pod will get expired.
// "stop" is the channel that would close the background goroutine.
// "schedulerName" identifies the scheduler
func New(handler commoncache.CacheHandler) BinderCache {
	cache := newBinderCache(handler)
	cache.run()
	return cache
}

type binderCache struct {
	commonstore.CommonStoresSwitch

	handler commoncache.CacheHandler
	mu      *sync.RWMutex
}

func newBinderCache(handler commoncache.CacheHandler) *binderCache {
	bc := &binderCache{
		CommonStoresSwitch: commonstore.MakeStoreSwitch(handler, commonstore.Cache, commonstores.GlobalRegistries, orderedStoreNames),

		handler: handler,
		mu:      handler.Mutex(),
	}

	// NodeStore and PodStore are mandatory, so we don't care if they are nil.
	nodeStore, podStore := bc.CommonStoresSwitch.Find(nodestore.Name), bc.CommonStoresSwitch.Find(podstore.Name)
	handler.SetNodeHandler(nodeStore.(*nodestore.NodeStore).GetNodeInfo)
	handler.SetPodHandler(podStore.(*podstore.PodStore).GetPodState)

	handler.SetPodOpFunc(bc.podOp)

	return bc
}

func (cache *binderCache) podOp(pod *v1.Pod, isAdd bool, skippedStores sets.String) error {
	return cache.CommonStoresSwitch.Range(
		func(cs commonstore.Store) error {
			if skippedStores.Len() > 0 && skippedStores.Has(string(cs.Name())) {
				return nil
			}
			if isAdd {
				return cs.AddPod(pod)
			}
			return cs.DeletePod(pod)
		})
}

func (cache *binderCache) run() {
	// TODO: Unify and organize metrics related information.
	go wait.Until(func() {
		cache.updateMetrics()
	}, cache.handler.Period(), cache.handler.StopCh())

	cache.CommonStoresSwitch.Range(func(cs commonstore.Store) error {
		cs.PeriodWorker(cache.handler.Mutex())
		return nil
	})
}

// TODO: Extend the metrics related.
// updateMetrics updates cache size metric values for pods, assumed pods, and nodes
func (cache *binderCache) updateMetrics() {
	cache.mu.Lock()
	defer cache.mu.Unlock()

	podStore := cache.CommonStoresSwitch.Find(podstore.Name).(*podstore.PodStore)
	nodeStore := cache.CommonStoresSwitch.Find(nodestore.Name).(*nodestore.NodeStore)

	metrics.CacheSize.WithLabelValues("assumed_pods").Set(float64(len(podStore.AssumedPods)))
	metrics.CacheSize.WithLabelValues("pods").Set(float64(len(podStore.PodStates)))
	metrics.CacheSize.WithLabelValues("nodes").Set(float64(nodeStore.Len() - nodeStore.Deleted.Len()))
}

// -------------------------------------- Other Interface --------------------------------------

func (cache *binderCache) FinishBinding(pod *v1.Pod) error {
	return cache.finishBinding(pod, time.Now())
}

// finishBinding exists to make tests determinitistic by injecting now as an argument
func (cache *binderCache) finishBinding(pod *v1.Pod, now time.Time) error {
	cache.mu.RLock()
	defer cache.mu.RUnlock()
	return cache.CommonStoresSwitch.Find(podstore.Name).(*podstore.PodStore).FinishBinding(pod, now)
}

func (cache *binderCache) Dump() *commoncache.Dump {
	cache.mu.RLock()
	defer cache.mu.RUnlock()

	// TODO: Cleanup and extend the Dump.
	nodeStore, podStore := cache.CommonStoresSwitch.Find(nodestore.Name).(*nodestore.NodeStore), cache.CommonStoresSwitch.Find(podstore.Name).(*podstore.PodStore)

	nodes := nodeStore.AllNodesClone()
	assumedPods := make(map[string]bool, len(podStore.AssumedPods))
	for k, v := range podStore.AssumedPods {
		assumedPods[k] = v
	}

	return &commoncache.Dump{
		Nodes:       nodes,
		AssumedPods: assumedPods,
	}
}

func (cache *binderCache) IsAssumedPod(pod *v1.Pod) (bool, error) {
	cache.mu.RLock()
	defer cache.mu.RUnlock()
	return cache.CommonStoresSwitch.Find(podstore.Name).(*podstore.PodStore).IsAssumedPod(pod)
}

// GetPod might return a pod for which its node has already been deleted from
// the main cache. This is useful to properly process pod update events.
func (cache *binderCache) GetPod(pod *v1.Pod) (*v1.Pod, error) {
	cache.mu.RLock()
	defer cache.mu.RUnlock()
	return cache.CommonStoresSwitch.Find(podstore.Name).(*podstore.PodStore).GetPod(pod)
}

func (cache *binderCache) GetNodeInfo(nodeName string) framework.NodeInfo {
	cache.mu.RLock()
	defer cache.mu.RUnlock()
	return cache.CommonStoresSwitch.Find(nodestore.Name).(*nodestore.NodeStore).GetNodeInfo(nodeName)
}

func (cache *binderCache) SetUnitSchedulingStatus(unitKey string, status unitstatus.SchedulingStatus) {
	cache.mu.Lock()
	defer cache.mu.Unlock()
	cache.CommonStoresSwitch.Find(unitstatusstore.Name).(*unitstatusstore.UnitStatusStore).SetUnitSchedulingStatus(unitKey, status)
}

func (cache *binderCache) GetUnitSchedulingStatus(unitKey string) unitstatus.SchedulingStatus {
	cache.mu.RLock()
	defer cache.mu.RUnlock()
	return cache.CommonStoresSwitch.Find(unitstatusstore.Name).(*unitstatusstore.UnitStatusStore).GetUnitSchedulingStatus(unitKey)
}

func (cache *binderCache) MarkPodToDelete(pod, preemptor *v1.Pod) error {
	cache.mu.Lock()
	defer cache.mu.Unlock()
	return cache.CommonStoresSwitch.Find(deletedmarkerstore.Name).(*deletedmarkerstore.DeletedMarkerStore).MarkPodToDelete(pod, preemptor)
}

func (cache *binderCache) RemoveDeletePodMarker(pod, preemptor *v1.Pod) error {
	cache.mu.Lock()
	defer cache.mu.Unlock()
	return cache.CommonStoresSwitch.Find(deletedmarkerstore.Name).(*deletedmarkerstore.DeletedMarkerStore).RemoveDeletePodMarker(pod, preemptor)
}

func (cache *binderCache) RemoveDeletePodMarkerByKey(podKey, preemptorKey string) error {
	cache.mu.Lock()
	defer cache.mu.Unlock()
	return cache.CommonStoresSwitch.Find(deletedmarkerstore.Name).(*deletedmarkerstore.DeletedMarkerStore).RemoveDeletePodMarkerByKey(podKey, preemptorKey)
}

func (cache *binderCache) IsPodMarkedToDelete(pod *v1.Pod) (bool, error) {
	cache.mu.RLock()
	defer cache.mu.RUnlock()
	return cache.CommonStoresSwitch.Find(deletedmarkerstore.Name).(*deletedmarkerstore.DeletedMarkerStore).IsPodMarkedToDelete(pod)
}

func (cache *binderCache) GetUnitStatus(unitKey string) unitstatus.UnitStatus {
	cache.mu.RLock()
	defer cache.mu.RUnlock()
	return cache.CommonStoresSwitch.Find(unitstatusstore.Name).(*unitstatusstore.UnitStatusStore).GetUnitStatus(unitKey)
}

func (cache *binderCache) FindStore(storeName commonstore.StoreName) commonstore.Store {
	cache.mu.RLock()
	defer cache.mu.RUnlock()
	return cache.CommonStoresSwitch.Find(storeName)
}
