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

package examplestore

import (
	v1 "k8s.io/api/core/v1"
	"k8s.io/apiserver/pkg/features"
	utilfeature "k8s.io/apiserver/pkg/util/feature"

	commoncache "github.com/kubewharf/godel-scheduler/pkg/common/cache"
	commonstore "github.com/kubewharf/godel-scheduler/pkg/common/store"
	framework "github.com/kubewharf/godel-scheduler/pkg/framework/api"
	"github.com/kubewharf/godel-scheduler/pkg/scheduler/cache/commonstores"
	"github.com/kubewharf/godel-scheduler/pkg/util/generationstore"
	podutil "github.com/kubewharf/godel-scheduler/pkg/util/pod"
)

// ================================================ ATTENTION ================================================
// This is just an example of a storage intervention and will not actually be used when the scheduler is running.
// ================================================ ATTENTION ================================================

// Define the index names of the stores, each of which should be different from the other.
const Name commonstore.StoreName = "ExampleStore"

func (s *ExampleStore) Name() commonstore.StoreName {
	return Name
}

// Implement the `init` function and register the storage to the registry when the program starts.
func init() {
	commonstores.GlobalRegistries.Register(
		// Args0: Name of the store.
		Name,
		// Args1: Whether the store should be built. Returning true means the store should be built.
		//	Specifically: you can use feature gate or use the handler's method to determine if you
		//	need to build the storage.
		func(h commoncache.CacheHandler) bool {
			// For example:
			if h.IsStoreEnabled("PreemptionStore") && utilfeature.DefaultFeatureGate.Enabled(features.DryRun) {
				return true
			}
			return false
		},
		// Args2: Provide function pointers for new cache storage.
		NewCache,
		// Args3: Provide function pointers for new snapshot storage.
		NewSnapshot)
}

// ---------------------------------------------------------------------------------------

// ExampleStore implementing the Store interface.
type ExampleStore struct {
	// BaseStore is the `anonymous field` that implements all the ClusterEventsHandler interface as a base class.
	commonstore.BaseStore
	// storeType specifies whether the current store belong to Cache / Snapshot.
	storeType commonstore.StoreType
	// handler provides access to public data or some advanced operations.
	handler commoncache.CacheHandler

	// Storage field support customization.
	// In general, we use generationstore.ListStore in Cache and generationstore.RawStore in snapshot,
	// and such data structures support multiple levels of nesting.
	Store generationstore.Store
}

func NewCache(handler commoncache.CacheHandler) commonstore.Store {
	return &ExampleStore{
		// NewBaseStore returns a basic implementation of BaseStore, which does not do any operations.
		BaseStore: commonstore.NewBaseStore(),
		storeType: commonstore.Cache,
		handler:   handler,

		Store: generationstore.NewListStore(),
	}
}

func NewSnapshot(handler commoncache.CacheHandler) commonstore.Store {
	return &ExampleStore{
		// NewBaseStore returns a basic implementation of BaseStore, which does not do any operations.
		BaseStore: commonstore.NewBaseStore(),
		storeType: commonstore.Snapshot,
		handler:   handler,

		Store: generationstore.NewRawStore(),
	}
}

// -------------------------------------- ClusterEventsHandler --------------------------------------

// For a specific store, it needs to explicitly know which events it needs to care about, and manually
// implement specific function methods to override BaseStore's function methods.
//
// For example:	ExampleStore needs to care about the Add/Update/Delete event of Pods, but only needs
// to care about Update event of Node. Then it only needs to implement the corresponding four function
// methods.

func (s *ExampleStore) AddPod(pod *v1.Pod) error {
	// For a specific store, it may only care about pods that meet specific conditions, so it is able
	// to do filtering here based on specific objects.
	if !podutil.BoundPod(pod) && !podutil.AssumedPodOfGodel(pod, s.handler.SchedulerType()) {
		return nil
	}
	return s.podOp(pod)
}

// ATTENTION: Previously, assumed pods were converted to bound pods by AddPod, but this is actually a
// pod update event.
// At this point, the oldPod does not carry information about the assume, so you MUST try to get the
// state of the once-assumed pod from the Cache.
func (s *ExampleStore) UpdatePod(oldPod *v1.Pod, newPod *v1.Pod) error {
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

func (s *ExampleStore) DeletePod(pod *v1.Pod) error {
	// For a specific store, it may only care about pods that meet specific conditions, so it is able
	// to do filtering here based on specific objects.
	if !podutil.BoundPod(pod) && !podutil.AssumedPodOfGodel(pod, s.handler.SchedulerType()) {
		return nil
	}
	return s.podOp(pod)
}

func (s *ExampleStore) UpdateNode(oldNode, newNode *v1.Node) error {
	return s.nodeOp(newNode)
}

// AssumePod/ForgetPod will be called by Cache/Snapshot at the same time. If there is different logic
// it can be distinguished by storeType.
func (s *ExampleStore) AssumePod(podInfo *framework.CachePodInfo) error {
	if s.storeType == commonstore.Snapshot {
		return nil
	}
	// Do something and return.
	// ...
	return nil
}

// AssumePod/ForgetPod will be called by Cache/Snapshot at the same time. If there is different logic
// it can be distinguished by storeType.
func (s *ExampleStore) ForgetPod(podInfo *framework.CachePodInfo) error {
	if s.storeType == commonstore.Snapshot {
		return nil
	}
	// Do something and return.
	// ...
	return nil
}

// UpdateSnapshot synchronize the data in the Cache to Snapshot, generally using generationstore for
// incremental updates.
func (s *ExampleStore) UpdateSnapshot(store commonstore.Store) error {
	cache, snapshot := framework.TransferGenerationStore(s.Store, store.(*ExampleStore).Store)
	cache.UpdateRawStore(
		snapshot,
		func(s string, so generationstore.StoredObj) {
			// Clone..
		},
		generationstore.DefaultCleanFunc(cache, snapshot),
	)
	return nil
}

// -------------------------------------- Internal Function --------------------------------------

func (s *ExampleStore) podOp(pod *v1.Pod) error {
	// Do something and return.
	// ...
	return nil
}

func (s *ExampleStore) nodeOp(node *v1.Node) error {
	// Do something and return.
	// ...
	return nil
}

// -------------------------------------- Plugin Handle --------------------------------------

type StoreHandle interface {
	GetPodCount(nodeName string) int
}

var _ StoreHandle = &ExampleStore{}

func (s *ExampleStore) GetPodCount(nodeName string) int {
	storedObj := s.Store.Get(nodeName)
	if storedObj == nil {
		return 0
	}
	node := storedObj.(ExampleStoreNode)
	return node.PodCount
}
