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

package podstore

import (
	"fmt"
	"sync"
	"time"

	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/klog/v2"

	framework "github.com/kubewharf/godel-scheduler/pkg/framework/api"
	"github.com/kubewharf/godel-scheduler/pkg/framework/utils"
	"github.com/kubewharf/godel-scheduler/pkg/scheduler/cache/commonstores"
	"github.com/kubewharf/godel-scheduler/pkg/scheduler/cache/handler"
	podutil "github.com/kubewharf/godel-scheduler/pkg/util/pod"
)

const Name commonstores.StoreName = "PodStore"

func (c *PodStore) Name() commonstores.StoreName {
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

type PodStore struct {
	commonstores.BaseStore
	storeType commonstores.StoreType
	handler   handler.CacheHandler

	// The following data will only be used in the cache

	// a set of assumed pod keys.
	// The key could further be used to get an entry in PodStates.
	AssumedPods map[string]bool
	// a map from pod key to podState.
	PodStates map[string]*framework.CachePodState
}

var _ commonstores.CommonStore = &PodStore{}

func NewCache(handler handler.CacheHandler) commonstores.CommonStore {
	return &PodStore{
		BaseStore: commonstores.NewBaseStore(),
		storeType: commonstores.Cache,
		handler:   handler,

		AssumedPods: make(map[string]bool),
		PodStates:   make(map[string]*framework.CachePodState),
	}
}

func NewSnapshot(handler handler.CacheHandler) commonstores.CommonStore {
	return &PodStore{
		BaseStore: commonstores.NewBaseStore(),
		storeType: commonstores.Snapshot,
		handler:   handler,

		AssumedPods: make(map[string]bool),
		PodStates:   make(map[string]*framework.CachePodState),
	}
}

func (s *PodStore) AddPod(pod *v1.Pod) error {
	if !podutil.BoundPod(pod) && !podutil.AssumedPodOfGodel(pod, s.handler.SchedulerType()) {
		return nil
	}
	return s.podOp(pod, true)
}

// ATTENTION: Previously, assumed pods were converted to bound pods by AddPod, but this is actually a
// pod update event.
// At this point, the oldPod does not carry information about the assume, so you MUST try to get the
// state of the once-assumed pod from the Cache.
func (s *PodStore) UpdatePod(oldPod *v1.Pod, newPod *v1.Pod) error {
	// Remove the oldPod if existed.
	{
		key, err := framework.GetPodKey(oldPod)
		if err != nil {
			return err
		}
		if ps, _ := s.GetPodState(key); ps != nil {
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

func (s *PodStore) RemovePod(pod *v1.Pod) error {
	if !podutil.BoundPod(pod) && !podutil.AssumedPodOfGodel(pod, s.handler.SchedulerType()) {
		return nil
	}
	return s.podOp(pod, false)
}

func (s *PodStore) AssumePod(podInfo *framework.CachePodInfo) error {
	if s.storeType == commonstores.Snapshot {
		return nil
	}

	key, err := framework.GetPodKey(podInfo.Pod)
	if err != nil {
		return err
	}
	if _, ok := s.PodStates[key]; ok {
		return fmt.Errorf("pod %v is in the cache, so can't be assumed", key)
	}
	s.PodStates[key] = &framework.CachePodState{Pod: podInfo.Pod}
	s.AssumedPods[key] = true

	return nil
}

func (s *PodStore) ForgetPod(podInfo *framework.CachePodInfo) error {
	if s.storeType == commonstores.Snapshot {
		return nil
	}

	key, err := framework.GetPodKey(podInfo.Pod)
	if err != nil {
		return err
	}
	currState, ok := s.PodStates[key]
	if ok {
		originalNodeName := utils.GetNodeNameFromPod(currState.Pod)
		newNodeName := utils.GetNodeNameFromPod(podInfo.Pod)
		if originalNodeName != newNodeName {
			klog.ErrorS(nil, "Unexpected arguments from forget function", "pod", klog.KObj(podInfo.Pod), "originalNodeName", originalNodeName, "newNodeName", newNodeName)
			klog.FlushAndExit(klog.ExitFlushTimeout, 1)
		}
	} else {
		return nil
	}

	// Only assumed pod can be forgotten.
	if ok && s.AssumedPods[key] {
		delete(s.AssumedPods, key)
		delete(s.PodStates, key)
		return nil
	}

	return fmt.Errorf("pod %v/%v wasn't assumed so cannot be forgotten", podInfo.Pod.Namespace, podInfo.Pod.Name)
}

func (s *PodStore) UpdateSnapshot(_ commonstores.CommonStore) error {
	return nil
}

func (s *PodStore) PeriodWorker(mu *sync.RWMutex) {
	go wait.Until(func() {
		s.CleanupExpiredAssumedPods(mu, time.Now())
	}, s.handler.Period(), s.handler.StopCh())
}

// -------------------------------------- Other Interface --------------------------------------

func (s *PodStore) podOp(pod *v1.Pod, isAdd bool) error {
	key, err := framework.GetPodKey(pod)
	if err != nil {
		return err
	}

	if isAdd {
		_, ok := s.PodStates[key]
		if ok {
			return fmt.Errorf("pod %v was already in added state", key)
		}
		// ATTENTION: for add pod, set pod state directly.
		s.PodStates[key] = &framework.CachePodState{Pod: pod}
	} else {
		_, ok := s.PodStates[key]
		if !ok {
			return fmt.Errorf("pod %v is not found in scheduler cache, so cannot be removed from it", key)
		}
		// ATTENTION: The original logic was due to the upper level filter function, but now we no longer need it.
		// if cache.assumedPods[key] {
		// 	return fmt.Errorf("pod %v is assumed in scheduler cache, so cannot be removed from it", key)
		// }
		delete(s.PodStates, key)
		delete(s.AssumedPods, key)
	}
	return nil
}

func (s *PodStore) IsAssumedPod(pod *v1.Pod) (bool, error) {
	key, err := framework.GetPodKey(pod)
	if err != nil {
		return false, err
	}

	b, found := s.AssumedPods[key]
	if !found {
		return false, nil
	}
	return b, nil
}

func (s *PodStore) GetPod(pod *v1.Pod) (*v1.Pod, error) {
	key, err := framework.GetPodKey(pod)
	if err != nil {
		return nil, err
	}

	podState, ok := s.PodStates[key]
	if !ok {
		return nil, fmt.Errorf("pod %v does not exist in scheduler cache", key)
	}
	return podState.Pod, nil
}

func (s *PodStore) GetPodState(key string) (*framework.CachePodState, bool) {
	podState, ok := s.PodStates[key]
	if !ok {
		return nil, false
	}
	return podState, s.AssumedPods[key]
}

func (s *PodStore) FinishReserving(pod *v1.Pod, now time.Time) error {
	key, err := framework.GetPodKey(pod)
	if err != nil {
		return err
	}

	klog.V(5).InfoS("Finished binding for pod. It can be expired", "pod", klog.KObj(pod))
	currState, ok := s.PodStates[key]
	if ok && s.AssumedPods[key] {
		dl := now.Add(s.handler.TTL())
		currState.BindingFinished = true
		currState.Deadline = &dl
	}
	return nil
}

// cleanupAssumedPods exists for making test deterministic by taking time as input argument.
// It also reports metrics on the cache size for nodes, pods, and assumed pods.
func (s *PodStore) CleanupExpiredAssumedPods(mu *sync.RWMutex, now time.Time) {
	mu.Lock()
	defer mu.Unlock()

	// The size of assumedPods should be small
	assumedPods, podStates := s.AssumedPods, s.PodStates
	for key := range assumedPods {
		ps, ok := podStates[key]
		if !ok {
			klog.ErrorS(nil, "Key found in assumed set but not in podStates. Potentially a logical error")
			klog.FlushAndExit(klog.ExitFlushTimeout, 1)
		}
		if !ps.BindingFinished {
			klog.V(5).InfoS("Couldn't expire cache for pod while binding is still in progress", "pod", klog.KObj(ps.Pod))
			continue
		}
		if now.After(*ps.Deadline) {
			klog.InfoS("WARN: cached pod was expired", "pod", klog.KObj(ps.Pod))

			s.handler.PodOp(ps.Pod, false, sets.NewString()) // Need skip reservation.
		}
	}
}
