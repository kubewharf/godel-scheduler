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

	v1 "k8s.io/api/core/v1"

	schedulingv1a1 "github.com/kubewharf/godel-scheduler-api/pkg/apis/scheduling/v1alpha1"
	commonstore "github.com/kubewharf/godel-scheduler/pkg/common/store"
	framework "github.com/kubewharf/godel-scheduler/pkg/framework/api"
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
	return cache.CommonStoresSwitch.Range(func(cs commonstore.Store) error { return cs.AssumePod(podInfo) })
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
	return cache.CommonStoresSwitch.Range(func(cs commonstore.Store) error { return cs.ForgetPod(podInfo) })
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
	return cache.CommonStoresSwitch.Range(func(cs commonstore.Store) error { return cs.UpdatePod(oldPod, newPod) })
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
			return cache.CommonStoresSwitch.Range(func(cs commonstore.Store) error { return cs.DeletePod(ps.Pod) })
		}
	}
	return cache.CommonStoresSwitch.Range(func(cs commonstore.Store) error { return cs.DeletePod(pod) })
}

func (cache *schedulerCache) AddReservation(request *schedulingv1a1.Reservation) error {
	cache.mu.Lock()
	defer cache.mu.Unlock()
	return cache.CommonStoresSwitch.Range(func(cs commonstore.Store) error {
		return cs.AddReservation(request)
	})
}

func (cache *schedulerCache) UpdateReservation(oldRequest, newRequest *schedulingv1a1.Reservation) error {
	cache.mu.Lock()
	defer cache.mu.Unlock()
	return cache.CommonStoresSwitch.Range(func(cs commonstore.Store) error {
		return cs.UpdateReservation(oldRequest, newRequest)
	})
}

func (cache *schedulerCache) DeleteReservation(request *schedulingv1a1.Reservation) error {
	cache.mu.Lock()
	defer cache.mu.Unlock()
	return cache.CommonStoresSwitch.Range(func(cs commonstore.Store) error {
		return cs.DeleteReservation(request)
	})
}
