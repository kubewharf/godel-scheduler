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
	"fmt"

	nodev1alpha1 "github.com/kubewharf/godel-scheduler-api/pkg/apis/node/v1alpha1"
	schedulingv1a1 "github.com/kubewharf/godel-scheduler-api/pkg/apis/scheduling/v1alpha1"
	katalystv1alpha1 "github.com/kubewharf/katalyst-api/pkg/apis/node/v1alpha1"
	v1 "k8s.io/api/core/v1"
	policy "k8s.io/api/policy/v1"

	framework "github.com/kubewharf/godel-scheduler/pkg/framework/api"
	"github.com/kubewharf/godel-scheduler/pkg/scheduler/cache/commonstores"
	podutil "github.com/kubewharf/godel-scheduler/pkg/util/pod"
)

func (cache *schedulerCache) assumedOrBoundPod(pod *v1.Pod) bool {
	return podutil.BoundPod(pod) || podutil.AssumedPodOfGodel(pod, cache.handler.SchedulerType())
}

func (cache *schedulerCache) AssumePod(podInfo *framework.CachePodInfo) error {
	key, err := framework.GetPodKey(podInfo.Pod)
	if err != nil {
		return err
	}

	cache.mu.Lock()
	defer cache.mu.Unlock()
	if podState, _ := cache.handler.GetPodState(key); podState != nil {
		return fmt.Errorf("pod %v was already in scheduler cache", key)
	}
	return cache.storeSwitch.Range(func(cs commonstores.CommonStore) error { return cs.AssumePod(podInfo) })
}

func (cache *schedulerCache) ForgetPod(podInfo *framework.CachePodInfo) error {
	key, err := framework.GetPodKey(podInfo.Pod)
	if err != nil {
		return err
	}

	cache.mu.Lock()
	defer cache.mu.Unlock()
	if _, assumed := cache.handler.GetPodState(key); !assumed {
		return nil
	}
	return cache.storeSwitch.Range(func(cs commonstores.CommonStore) error { return cs.ForgetPod(podInfo) })
}

func (cache *schedulerCache) AddPod(pod *v1.Pod) error {
	cache.mu.Lock()
	defer cache.mu.Unlock()
	return cache.storeSwitch.Range(func(cs commonstores.CommonStore) error { return cs.AddPod(pod) })
}

func (cache *schedulerCache) UpdatePod(oldPod, newPod *v1.Pod) error {
	cache.mu.Lock()
	defer cache.mu.Unlock()

	// ATTENTION: Ignore this call when `neither the old nor the new pod belongs to the cache and it has been assumed before`.
	if !cache.assumedOrBoundPod(oldPod) && !cache.assumedOrBoundPod(newPod) {
		key, err := framework.GetPodKey(oldPod)
		if err != nil {
			return err
		}
		if _, assumed := cache.handler.GetPodState(key); assumed {
			return nil
		}
	}
	return cache.storeSwitch.Range(func(cs commonstores.CommonStore) error { return cs.UpdatePod(oldPod, newPod) })
}

func (cache *schedulerCache) RemovePod(pod *v1.Pod) error {
	cache.mu.Lock()
	defer cache.mu.Unlock()

	// ATTENTION: In order to handle the case of event merging between update and delete, the pod stored in the cache
	// should be used if the corresponding pod exists in the cache.
	{
		key, err := framework.GetPodKey(pod)
		if err != nil {
			return err
		}
		if ps, _ := cache.handler.GetPodState(key); ps != nil {
			return cache.storeSwitch.Range(func(cs commonstores.CommonStore) error { return cs.RemovePod(ps.Pod) })
		}
	}
	return cache.storeSwitch.Range(func(cs commonstores.CommonStore) error { return cs.RemovePod(pod) })
}

func (cache *schedulerCache) AddNode(node *v1.Node) error {
	cache.mu.Lock()
	defer cache.mu.Unlock()
	return cache.storeSwitch.Range(func(cs commonstores.CommonStore) error { return cs.AddNode(node) })
}

func (cache *schedulerCache) AddNMNode(nmNode *nodev1alpha1.NMNode) error {
	cache.mu.Lock()
	defer cache.mu.Unlock()
	return cache.storeSwitch.Range(func(cs commonstores.CommonStore) error { return cs.AddNMNode(nmNode) })
}

func (cache *schedulerCache) AddCNR(cnr *katalystv1alpha1.CustomNodeResource) error {
	cache.mu.Lock()
	defer cache.mu.Unlock()
	return cache.storeSwitch.Range(func(cs commonstores.CommonStore) error { return cs.AddCNR(cnr) })
}

func (cache *schedulerCache) UpdateNode(oldNode, newNode *v1.Node) error {
	cache.mu.Lock()
	defer cache.mu.Unlock()
	return cache.storeSwitch.Range(func(cs commonstores.CommonStore) error { return cs.UpdateNode(oldNode, newNode) })
}

func (cache *schedulerCache) UpdateNMNode(oldNMNode, newNMNode *nodev1alpha1.NMNode) error {
	cache.mu.Lock()
	defer cache.mu.Unlock()

	return cache.storeSwitch.Range(func(cs commonstores.CommonStore) error { return cs.UpdateNMNode(oldNMNode, newNMNode) })
}

func (cache *schedulerCache) UpdateCNR(oldCNR, newCNR *katalystv1alpha1.CustomNodeResource) error {
	cache.mu.Lock()
	defer cache.mu.Unlock()
	return cache.storeSwitch.Range(func(cs commonstores.CommonStore) error { return cs.UpdateCNR(oldCNR, newCNR) })
}

// RemoveNode removes a node from the cache's tree.
// The node might still have pods because their deletion events didn't arrive
// yet. Those pods are considered removed from the cache, being the node tree
// the source of truth.
// However, we keep a ghost node with the list of pods until all pod deletion
// events have arrived. A ghost node is skipped from snapshots.
func (cache *schedulerCache) RemoveNode(node *v1.Node) error {
	cache.mu.Lock()
	defer cache.mu.Unlock()
	return cache.storeSwitch.Range(func(cs commonstores.CommonStore) error { return cs.RemoveNode(node) })
}

func (cache *schedulerCache) RemoveNMNode(nmNode *nodev1alpha1.NMNode) error {
	cache.mu.Lock()
	defer cache.mu.Unlock()
	return cache.storeSwitch.Range(func(cs commonstores.CommonStore) error { return cs.RemoveNMNode(nmNode) })
}

// RemoveCNR removes custom node resource.
// The node might be still in the node tree because their deletion events didn't arrive yet.
func (cache *schedulerCache) RemoveCNR(cnr *katalystv1alpha1.CustomNodeResource) error {
	cache.mu.Lock()
	defer cache.mu.Unlock()
	return cache.storeSwitch.Range(func(cs commonstores.CommonStore) error { return cs.RemoveCNR(cnr) })
}

func (cache *schedulerCache) AddPodGroup(podGroup *schedulingv1a1.PodGroup) error {
	cache.mu.Lock()
	defer cache.mu.Unlock()
	return cache.storeSwitch.Range(func(cs commonstores.CommonStore) error { return cs.AddPodGroup(podGroup) })
}

func (cache *schedulerCache) RemovePodGroup(podGroup *schedulingv1a1.PodGroup) error {
	cache.mu.Lock()
	defer cache.mu.Unlock()
	return cache.storeSwitch.Range(func(cs commonstores.CommonStore) error { return cs.RemovePodGroup(podGroup) })
}

func (cache *schedulerCache) UpdatePodGroup(oldPodGroup, newPodGroup *schedulingv1a1.PodGroup) error {
	cache.mu.Lock()
	defer cache.mu.Unlock()
	return cache.storeSwitch.Range(func(cs commonstores.CommonStore) error { return cs.UpdatePodGroup(oldPodGroup, newPodGroup) })
}

func (cache *schedulerCache) AddPDB(pdb *policy.PodDisruptionBudget) error {
	cache.mu.Lock()
	defer cache.mu.Unlock()
	return cache.storeSwitch.Range(func(cs commonstores.CommonStore) error { return cs.AddPDB(pdb) })
}

func (cache *schedulerCache) UpdatePDB(oldPdb, newPdb *policy.PodDisruptionBudget) error {
	cache.mu.Lock()
	defer cache.mu.Unlock()
	return cache.storeSwitch.Range(func(cs commonstores.CommonStore) error { return cs.UpdatePDB(oldPdb, newPdb) })
}

func (cache *schedulerCache) DeletePDB(pdb *policy.PodDisruptionBudget) error {
	cache.mu.Lock()
	defer cache.mu.Unlock()
	return cache.storeSwitch.Range(func(cs commonstores.CommonStore) error { return cs.DeletePDB(pdb) })
}

func (cache *schedulerCache) AddOwner(ownerType, key string, labels map[string]string) error {
	cache.mu.Lock()
	defer cache.mu.Unlock()
	return cache.storeSwitch.Range(func(cs commonstores.CommonStore) error { return cs.AddOwner(ownerType, key, labels) })
}

func (cache *schedulerCache) UpdateOwner(ownerType, key string, oldLabels, newLabels map[string]string) error {
	cache.mu.Lock()
	defer cache.mu.Unlock()
	return cache.storeSwitch.Range(func(cs commonstores.CommonStore) error { return cs.UpdateOwner(ownerType, key, oldLabels, newLabels) })
}

func (cache *schedulerCache) DeleteOwner(ownerType, key string) error {
	cache.mu.Lock()
	defer cache.mu.Unlock()
	return cache.storeSwitch.Range(func(cs commonstores.CommonStore) error { return cs.DeleteOwner(ownerType, key) })
}
