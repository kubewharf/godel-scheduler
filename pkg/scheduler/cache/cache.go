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
	"k8s.io/klog/v2"

	commoncache "github.com/kubewharf/godel-scheduler/pkg/common/cache"
	framework "github.com/kubewharf/godel-scheduler/pkg/framework/api"
	"github.com/kubewharf/godel-scheduler/pkg/scheduler/cache/commonstores"
	nodestore "github.com/kubewharf/godel-scheduler/pkg/scheduler/cache/commonstores/node_store"
	podstore "github.com/kubewharf/godel-scheduler/pkg/scheduler/cache/commonstores/pod_store"
	unitstatusstore "github.com/kubewharf/godel-scheduler/pkg/scheduler/cache/commonstores/unit_status_store"
	"github.com/kubewharf/godel-scheduler/pkg/scheduler/cache/handler"
	"github.com/kubewharf/godel-scheduler/pkg/scheduler/metrics"
	"github.com/kubewharf/godel-scheduler/pkg/util/generationstore"
	"github.com/kubewharf/godel-scheduler/pkg/util/helper"
	metricsutil "github.com/kubewharf/godel-scheduler/pkg/util/metrics"
	unitstatus "github.com/kubewharf/godel-scheduler/pkg/util/unitstatus"
)

var cleanAssumedPeriod = 10 * time.Second

// New returns a SchedulerCache implementation.
// It automatically starts a go routine that manages expiration of assumed pods.
// "ttl" is how long the assumed pod will get expired.
// "stop" is the channel that would close the background goroutine.
// "schedulerName" identifies the scheduler
func New(handler handler.CacheHandler) SchedulerCache {
	cache := newSchedulerCache(handler)
	cache.run()
	return cache
}

type schedulerCache struct {
	handler handler.CacheHandler

	cacheMetrics *cacheMetrics

	// This mutex guards all fields within this cache struct.
	mu sync.RWMutex

	storeSwitch *CommonStoresSwitch
}

func newSchedulerCache(handler handler.CacheHandler) *schedulerCache {
	cacheMetrics := newCacheMetrics()

	sc := &schedulerCache{
		handler: handler,

		cacheMetrics: cacheMetrics,

		storeSwitch: makeStoreSwitch(handler, commonstores.Cache),
	}

	// NodeStore and PodStore are mandatory, so we don't care if they are nil.
	nodeStore, podStore := sc.storeSwitch.Find(nodestore.Name), sc.storeSwitch.Find(podstore.Name)
	nodeStore.(*nodestore.NodeStore).AfterAdd = func(n framework.NodeInfo) { cacheMetrics.update(n, 1) }
	nodeStore.(*nodestore.NodeStore).AfterDelete = func(n framework.NodeInfo) { cacheMetrics.update(n, -1) }

	handler.SetNodeHandler(nodeStore.(*nodestore.NodeStore).GetNodeInfo)
	handler.SetPodHandler(podStore.(*podstore.PodStore).GetPodState)

	handler.SetPodOpFunc(sc.podOp)

	return sc
}

// UpdateSnapshot takes a snapshot of cached NodeInfo map. This is called at
// beginning of every scheduling cycle.
// The snapshot only includes Nodes that are not deleted at the time this function is called.
// nodeinfo.GetNode() is guaranteed to be not nil for all the nodes in the snapshot.
// This function tracks generation number of NodeInfo and updates only the
// entries of an existing snapshot that have changed after the snapshot was taken.
func (cache *schedulerCache) UpdateSnapshot(snapshot *Snapshot) error {
	cache.mu.Lock()
	defer cache.mu.Unlock()
	klog.V(4).InfoS("Started UpdateSnapshot", "subCluster", snapshot.handler.SubCluster())
	start := time.Now()
	defer func() {
		cost := helper.SinceInSeconds(start)
		qos := metricsutil.SwitchTypeToQos(snapshot.handler.SwitchType())
		metrics.ObserveUpdateSnapshotAttemptAndLatency(snapshot.handler.SubCluster(), qos, cache.handler.SchedulerName(), cost)
		klog.V(4).InfoS("Completed UpdateSnapshot", "subCluster", snapshot.handler.SubCluster(), "cost", cost)
	}()

	return cache.storeSwitch.Range(
		func(cs commonstores.CommonStore) error {
			if s := snapshot.storeSwitch.Find(cs.Name()); s != nil {
				return cs.UpdateSnapshot(s)
			}
			return nil
		})
}

// podOp provides a callback function that triggers AddPod/RemovePod in some store.
// Specifically: when AssumedPods expire, these pods in other stores
// should be deleted via this callback.
// ATTENTION: this is lock free.
func (cache *schedulerCache) podOp(pod *v1.Pod, isAdd bool, skippStores sets.String) error {
	return cache.storeSwitch.Range(
		func(cs commonstores.CommonStore) error {
			if skippStores.Len() > 0 && skippStores.Has(string(cs.Name())) {
				return nil
			}
			if isAdd {
				return cs.AddPod(pod)
			}
			return cs.RemovePod(pod)
		})
}

func (cache *schedulerCache) run() {
	// TODO: Unify and organize metrics related information.
	go wait.Until(func() {
		cache.updateMetrics()
	}, cache.handler.Period(), cache.handler.StopCh())

	cache.storeSwitch.Range(func(cs commonstores.CommonStore) error {
		cs.PeriodWorker(&cache.mu)
		return nil
	})
}

// TODO: Extend the metrics related.
// updateMetrics updates cache size metric values for pods, assumed pods, and nodes
func (cache *schedulerCache) updateMetrics() {
	cache.mu.Lock()
	defer cache.mu.Unlock()

	podStore := cache.storeSwitch.Find(podstore.Name).(*podstore.PodStore)
	nodeStore := cache.storeSwitch.Find(nodestore.Name).(*nodestore.NodeStore)

	schedulerName := cache.handler.SchedulerName()
	metrics.CacheSize.WithLabelValues("pods", schedulerName).Set(float64(len(podStore.PodStates)))
	metrics.CacheSize.WithLabelValues("assumed_pods", schedulerName).Set(float64(len(podStore.AssumedPods)))
	cacheMetrics := cache.cacheMetrics

	metrics.CacheSize.WithLabelValues("nodeinfos", schedulerName).Set(float64(nodeStore.Len() - nodeStore.Deleted.Len()))
	metrics.CacheSize.WithLabelValues("nodes", schedulerName).Set(float64(cacheMetrics.nodeTotalCount))
	metrics.CacheSize.WithLabelValues("nmnodes", schedulerName).Set(float64(cacheMetrics.nmnodeTotalCount))
	metrics.CacheSize.WithLabelValues("hybrid_nodes", schedulerName).Set(float64(cacheMetrics.hybridHostTotalCount))
}

// -------------------------------------- Other Interface --------------------------------------

func (cache *schedulerCache) GetUnitStatus(unitKey string) unitstatus.UnitStatus {
	cache.mu.RLock()
	defer cache.mu.RUnlock()
	return cache.storeSwitch.Find(unitstatusstore.Name).(*unitstatusstore.UnitStatusStore).GetUnitStatus(unitKey)
}

func (cache *schedulerCache) SetUnitSchedulingStatus(unitKey string, status unitstatus.SchedulingStatus) {
	cache.mu.Lock()
	defer cache.mu.Unlock()
	cache.storeSwitch.Find(unitstatusstore.Name).(*unitstatusstore.UnitStatusStore).SetUnitSchedulingStatus(unitKey, status)
}

func (cache *schedulerCache) GetUnitSchedulingStatus(unitKey string) unitstatus.SchedulingStatus {
	cache.mu.RLock()
	defer cache.mu.RUnlock()
	return cache.storeSwitch.Find(unitstatusstore.Name).(*unitstatusstore.UnitStatusStore).GetUnitSchedulingStatus(unitKey)
}

func (cache *schedulerCache) FinishReserving(pod *v1.Pod) error {
	return cache.finishReserving(pod, time.Now())
}

// finishReserving exists to make tests determinitistic by injecting now as an argument
func (cache *schedulerCache) finishReserving(pod *v1.Pod, now time.Time) error {
	cache.mu.RLock()
	defer cache.mu.RUnlock()
	return cache.storeSwitch.Find(podstore.Name).(*podstore.PodStore).FinishReserving(pod, now)
}

// Snapshot takes a snapshot of the current scheduler cache. This is used for
// debugging purposes only and shouldn't be confused with UpdateSnapshot
// function.
// This method is expensive, and should be only used in non-critical path.
func (cache *schedulerCache) Dump() *commoncache.Dump {
	cache.mu.RLock()
	defer cache.mu.RUnlock()

	// TODO: Cleanup and extend the Dump.
	nodeStore, podStore := cache.storeSwitch.Find(nodestore.Name), cache.storeSwitch.Find(podstore.Name)

	nodes := nodeStore.(*nodestore.NodeStore).AllNodesClone()
	assumedPods := make(map[string]bool, len(podStore.(*podstore.PodStore).AssumedPods))
	for k, v := range podStore.(*podstore.PodStore).AssumedPods {
		assumedPods[k] = v
	}

	return &commoncache.Dump{
		Nodes:       nodes,
		AssumedPods: assumedPods,
	}
}

func (cache *schedulerCache) IsAssumedPod(pod *v1.Pod) (bool, error) {
	cache.mu.RLock()
	defer cache.mu.RUnlock()
	return cache.storeSwitch.Find(podstore.Name).(*podstore.PodStore).IsAssumedPod(pod)
}

func (cache *schedulerCache) IsCachedPod(pod *v1.Pod) (bool, error) {
	cache.mu.RLock()
	defer cache.mu.RUnlock()
	return cache.storeSwitch.Find(podstore.Name).(*podstore.PodStore).IsCachedPod(pod)
}

// GetPod might return a pod for which its node has already been deleted from
// the main cache. This is useful to properly process pod update events.
func (cache *schedulerCache) GetPod(pod *v1.Pod) (*v1.Pod, error) {
	cache.mu.RLock()
	defer cache.mu.RUnlock()

	return cache.storeSwitch.Find(podstore.Name).(*podstore.PodStore).GetPod(pod)
}

func (cache *schedulerCache) SetNodeInPartition(nodeName string) error {
	cache.mu.Lock()
	defer cache.mu.Unlock()
	// Only call nodestore
	return cache.storeSwitch.Find(nodestore.Name).(*nodestore.NodeStore).SetNodeInPartition(nodeName)
}

func (cache *schedulerCache) SetNodeOutOfPartition(nodeName string) error {
	cache.mu.Lock()
	defer cache.mu.Unlock()
	// Only call nodestore
	return cache.storeSwitch.Find(nodestore.Name).(*nodestore.NodeStore).SetNodeOutOfPartition(nodeName)
}

func (cache *schedulerCache) NodeInThisPartition(nodeName string) bool {
	cache.mu.Lock()
	defer cache.mu.Unlock()
	// Only call nodestore
	return cache.storeSwitch.Find(nodestore.Name).(*nodestore.NodeStore).NodeInThisPartition(nodeName)
}

// PodCount returns the number of pods in the cache (including those from deleted nodes).
// DO NOT use outside of tests.
func (cache *schedulerCache) PodCount() (int, error) {
	cache.mu.RLock()
	defer cache.mu.RUnlock()
	count := 0
	for _, nodeInfo := range cache.storeSwitch.Find(nodestore.Name).(*nodestore.NodeStore).AllNodesClone() {
		count += nodeInfo.NumPods()
	}
	return count, nil
}

// ScrapeCollectable updates store with cache.nodeStore incrementally
func (cache *schedulerCache) ScrapeCollectable(store generationstore.RawStore) {
	cache.mu.RLock()
	defer cache.mu.RUnlock()

	cacheNodeStore := cache.storeSwitch.Find(nodestore.Name).(*nodestore.NodeStore).Store.(generationstore.ListStore)
	cacheNodeStore.UpdateRawStore(
		store,
		func(s string, obj generationstore.StoredObj) {
			nodeInfo := obj.(framework.NodeInfo)
			store.Set(s, Clone(nodeInfo))
		},
		generationstore.DefaultCleanFunc(cacheNodeStore, store))
}
