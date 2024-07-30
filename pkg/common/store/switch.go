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

package store

import (
	"sync"

	nodev1alpha1 "github.com/kubewharf/godel-scheduler-api/pkg/apis/node/v1alpha1"
	schedulingv1a1 "github.com/kubewharf/godel-scheduler-api/pkg/apis/scheduling/v1alpha1"
	katalystv1alpha1 "github.com/kubewharf/katalyst-api/pkg/apis/node/v1alpha1"
	v1 "k8s.io/api/core/v1"
	policy "k8s.io/api/policy/v1"
	utilerrors "k8s.io/apimachinery/pkg/util/errors"
	"k8s.io/klog/v2"

	commoncache "github.com/kubewharf/godel-scheduler/pkg/common/cache"
)

type CommonStoresSwitch interface {
	commoncache.ClusterEventsHandler
	Find(name StoreName) Store
	Range(f RangeFunc) error
}

type CommonStoresSwitchImpl struct {
	stores []Store
	mu     *sync.RWMutex
}

type RangeFunc func(Store) error

func (s *CommonStoresSwitchImpl) Find(name StoreName) Store {
	// When the slice is small, direct traversal will be faster than indexing in a map.
	// This is because the latter requires hash operations.
	for _, store := range s.stores {
		if store.Name() == name {
			return store
		}
	}
	return nil
}

func (s *CommonStoresSwitchImpl) Range(f RangeFunc) error {
	var errs []error
	for i := range s.stores {
		if err := f(s.stores[i]); err != nil {
			klog.ErrorS(err, "Error occurred in CommonStoresSwitch Range", "failureStore", s.stores[i].Name(), "index", i, "total", len(s.stores))
			errs = append(errs, err)
		}
	}
	return utilerrors.NewAggregate(errs)
}

func (s *CommonStoresSwitchImpl) String() string {
	var ret string
	for i := range s.stores {
		ret += string(s.stores[i].Name()) + ","
	}
	return ret
}

func MakeStoreSwitch(handler commoncache.CacheHandler, storeType StoreType, globalRegistries Registries, orderedStoreNames []StoreName) CommonStoresSwitch {
	var registry Registry
	switch storeType {
	case Cache:
		registry = globalRegistries.CacheRegistry()
	case Snapshot:
		registry = globalRegistries.SnapshotRegistry()
	default:
		panic("invalid store type when make StoreSwitch")
	}
	checkers := globalRegistries.FeatureGateCheckers()

	stores := make([]Store, 0)
	for i, name := range orderedStoreNames {
		checker, ok := checkers[name]
		if !ok || checker == nil {
			panic("Invalid commonstores registry checker")
		}
		if checker(handler) {
			newFunc, ok := registry[name]
			if !ok || newFunc == nil {
				panic("Invalid commonstores registry new function")
			}
			klog.V(4).InfoS("Registered Store successfully", "storeType", storeType, "subCluster", handler.SubCluster(), "idx", i, "name", name)
			stores = append(stores, newFunc(handler))
		} else {
			klog.V(4).InfoS("Skipped register Store because couldn't pass the checker", "storeType", storeType, "subCluster", handler.SubCluster(), "idx", i, "name", name)
		}
	}
	return &CommonStoresSwitchImpl{stores: stores, mu: handler.Mutex()}
}

// ------------------------------ ClusterEventsHandler ------------------------------

func (s *CommonStoresSwitchImpl) AddPod(pod *v1.Pod) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.Range(func(s Store) error { return s.AddPod(pod) })
}

func (s *CommonStoresSwitchImpl) UpdatePod(oldPod, newPod *v1.Pod) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.Range(func(s Store) error { return s.UpdatePod(oldPod, newPod) })
}

func (s *CommonStoresSwitchImpl) DeletePod(pod *v1.Pod) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.Range(func(s Store) error { return s.DeletePod(pod) })
}

func (s *CommonStoresSwitchImpl) AddNode(node *v1.Node) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.Range(func(s Store) error { return s.AddNode(node) })
}

func (s *CommonStoresSwitchImpl) UpdateNode(oldNode, newNode *v1.Node) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.Range(func(s Store) error { return s.UpdateNode(oldNode, newNode) })
}

func (s *CommonStoresSwitchImpl) DeleteNode(node *v1.Node) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.Range(func(s Store) error { return s.DeleteNode(node) })
}

func (s *CommonStoresSwitchImpl) AddNMNode(nmNode *nodev1alpha1.NMNode) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.Range(func(s Store) error { return s.AddNMNode(nmNode) })
}

func (s *CommonStoresSwitchImpl) UpdateNMNode(oldNMNode, newNMNode *nodev1alpha1.NMNode) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.Range(func(s Store) error { return s.UpdateNMNode(oldNMNode, newNMNode) })
}

func (s *CommonStoresSwitchImpl) DeleteNMNode(nmNode *nodev1alpha1.NMNode) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.Range(func(s Store) error { return s.DeleteNMNode(nmNode) })
}

func (s *CommonStoresSwitchImpl) AddCNR(cnr *katalystv1alpha1.CustomNodeResource) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.Range(func(s Store) error { return s.AddCNR(cnr) })
}

func (s *CommonStoresSwitchImpl) UpdateCNR(oldCNR, newCNR *katalystv1alpha1.CustomNodeResource) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.Range(func(s Store) error { return s.UpdateCNR(oldCNR, newCNR) })
}

func (s *CommonStoresSwitchImpl) DeleteCNR(cnr *katalystv1alpha1.CustomNodeResource) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.Range(func(s Store) error { return s.DeleteCNR(cnr) })
}

func (s *CommonStoresSwitchImpl) AddPodGroup(podGroup *schedulingv1a1.PodGroup) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.Range(func(s Store) error { return s.AddPodGroup(podGroup) })
}

func (s *CommonStoresSwitchImpl) DeletePodGroup(podGroup *schedulingv1a1.PodGroup) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.Range(func(s Store) error { return s.DeletePodGroup(podGroup) })
}

func (s *CommonStoresSwitchImpl) UpdatePodGroup(oldPodGroup, newPodGroup *schedulingv1a1.PodGroup) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.Range(func(s Store) error { return s.UpdatePodGroup(oldPodGroup, newPodGroup) })
}

func (s *CommonStoresSwitchImpl) AddPDB(pdb *policy.PodDisruptionBudget) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.Range(func(s Store) error { return s.AddPDB(pdb) })
}

func (s *CommonStoresSwitchImpl) UpdatePDB(oldPdb, newPdb *policy.PodDisruptionBudget) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.Range(func(s Store) error { return s.UpdatePDB(oldPdb, newPdb) })
}

func (s *CommonStoresSwitchImpl) DeletePDB(pdb *policy.PodDisruptionBudget) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.Range(func(s Store) error { return s.DeletePDB(pdb) })
}

func (s *CommonStoresSwitchImpl) AddOwner(ownerType, key string, labels map[string]string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.Range(func(s Store) error { return s.AddOwner(ownerType, key, labels) })
}

func (s *CommonStoresSwitchImpl) UpdateOwner(ownerType, key string, oldLabels, newLabels map[string]string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.Range(func(s Store) error { return s.UpdateOwner(ownerType, key, oldLabels, newLabels) })
}

func (s *CommonStoresSwitchImpl) DeleteOwner(ownerType, key string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.Range(func(s Store) error { return s.DeleteOwner(ownerType, key) })
}

func (s *CommonStoresSwitchImpl) AddMovement(movement *schedulingv1a1.Movement) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.Range(func(s Store) error { return s.AddMovement(movement) })
}

func (s *CommonStoresSwitchImpl) UpdateMovement(oldMovement, newMovement *schedulingv1a1.Movement) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.Range(func(s Store) error { return s.UpdateMovement(oldMovement, newMovement) })
}

func (s *CommonStoresSwitchImpl) DeleteMovement(movement *schedulingv1a1.Movement) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.Range(func(s Store) error { return s.DeleteMovement(movement) })
}
