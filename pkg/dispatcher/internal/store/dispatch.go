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
	"math"
	"math/rand"
	"sync"
	"time"

	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/client-go/tools/cache"
	"k8s.io/klog/v2"

	podutil "github.com/kubewharf/godel-scheduler/pkg/util/pod"
	unitutil "github.com/kubewharf/godel-scheduler/pkg/util/unit"
)

type podInfo struct {
	podKey    string
	gangID    string
	scheduler string
	// The time pod added to the scheduling queue.
	timeStamp time.Time
}

func NewPodInfo(pod *v1.Pod, scheduler string) *podInfo {
	return &podInfo{
		podKey:    podutil.GetPodKey(pod),
		gangID:    unitutil.GetPodGroupFullName(pod),
		scheduler: scheduler,
		timeStamp: time.Now(),
	}
}

// the pod must be dispatched pod here, so the scheduler annotation has already been set
func (p *podInfo) getScheduler() string {
	return p.scheduler
}

func (p *podInfo) getGangID() string {
	return p.gangID
}

type podStore map[string]sets.String

func (ps podStore) addPod(key, podID string) {
	if val, ok := ps[key]; !ok {
		ps[key] = sets.NewString(podID)
	} else {
		val.Insert(podID)
	}
}

func (ps podStore) removePod(key, podID string) {
	if val, ok := ps[key]; !ok {
		return
	} else {
		val.Delete(podID)
		if val.Len() == 0 {
			delete(ps, key)
		}
	}
}

func (ps podStore) getLeastGroup() string {
	var ret string
	max := math.MaxInt32
	for k, v := range ps {
		if v.Len() < max {
			max = v.Len()
			ret = k
		}
	}
	return ret
}

// TODO: i don't think we should do expiration operations(remove pod directly) in dispatcher
// we should react based on pod events and scheduler liveness changes
// TODO: figure out what we can do if schedulers dies
type DispatchInfo interface {
	AddPod(pod *v1.Pod)
	RemovePod(pod *v1.Pod)
	RemovePodByKey(key string)
	AddPodInAdvance(pod *v1.Pod, scheduler string)
	UpdatePodInAdvance(pod *v1.Pod, scheduler string)
	GetMostIdleSchedulerAndAddPodInAdvance(pod *v1.Pod) string
	AddScheduler(schedulerName string)
	DeleteScheduler(schedulerName string)
	GetPodsOfOneScheduler(schedulerName string) []string
}

type dispatchInfo struct {
	lock            sync.RWMutex
	Pods            map[string]*podInfo
	SchedulerToPods podStore

	Schedulers map[string]struct{}
}

func NewDispatchInfo() DispatchInfo {
	return &dispatchInfo{
		Pods:            make(map[string]*podInfo),
		SchedulerToPods: make(podStore),
		Schedulers:      make(map[string]struct{}),
	}
}

func (dq *dispatchInfo) AddScheduler(schedulerName string) {
	dq.lock.Lock()
	defer dq.lock.Unlock()
	if len(schedulerName) == 0 {
		return
	}
	dq.Schedulers[schedulerName] = struct{}{}
}

func (dq *dispatchInfo) DeleteScheduler(schedulerName string) {
	dq.lock.Lock()
	defer dq.lock.Unlock()

	delete(dq.Schedulers, schedulerName)
}

// TODO: do we need to cache whole pod structs in dispatch info ?  pod key is enough ?
// but since the calling frequency of this function will not be high, it is ok for now
func (dq *dispatchInfo) GetPodsOfOneScheduler(schedulerName string) []string {
	dq.lock.Lock()
	defer dq.lock.Unlock()

	if len(dq.SchedulerToPods[schedulerName]) == 0 {
		return nil
	}

	results := make([]string, 0, len(dq.SchedulerToPods))
	for key := range dq.SchedulerToPods[schedulerName] {
		if pInfo := dq.Pods[key]; pInfo != nil {
			results = append(results, pInfo.podKey)
		}
	}

	return results
}

func (dq *dispatchInfo) addPod(pod *v1.Pod, scheduler string) {
	key, err := cache.MetaNamespaceKeyFunc(pod)
	if err != nil {
		return
	}
	podInfo := NewPodInfo(pod, scheduler)
	dq.Pods[key] = podInfo
	dq.SchedulerToPods.addPod(podInfo.getScheduler(), key)
}

func (dq *dispatchInfo) AddPod(pod *v1.Pod) {
	dq.lock.Lock()
	defer dq.lock.Unlock()
	dq.addPod(pod, pod.Annotations[podutil.SchedulerAnnotationKey])
}

func (dq *dispatchInfo) RemovePod(pod *v1.Pod) {
	dq.lock.Lock()
	defer dq.lock.Unlock()
	dq.removePod(pod)
}

func (dq *dispatchInfo) RemovePodByKey(key string) {
	dq.lock.Lock()
	defer dq.lock.Unlock()
	_, ok := dq.Pods[key]
	if !ok {
		return
	}
	dq.removeFromScheduler(key)
	delete(dq.Pods, key)
}

func (dq *dispatchInfo) removePod(pod *v1.Pod) {
	key, err := cache.MetaNamespaceKeyFunc(pod)
	if err != nil {
		return
	}
	_, ok := dq.Pods[key]
	if !ok {
		return
	}
	dq.removeFromScheduler(key)
	delete(dq.Pods, key)
}

func (dq *dispatchInfo) removeFromScheduler(podID string) {
	if len(podID) == 0 {
		return
	}
	podInfo, ok := dq.Pods[podID]
	if !ok {
		return
	}
	dq.SchedulerToPods.removePod(podInfo.getScheduler(), podID)
}

func (dq *dispatchInfo) AddPodInAdvance(pod *v1.Pod, scheduler string) {
	dq.lock.Lock()
	defer dq.lock.Unlock()
	dq.addPod(pod, scheduler)
}

func (dq *dispatchInfo) UpdatePodInAdvance(pod *v1.Pod, scheduler string) {
	dq.lock.Lock()
	defer dq.lock.Unlock()
	dq.removePod(pod)
	dq.addPod(pod, scheduler)
}

// 选择节点数最少的 scheduler
func (dq *dispatchInfo) GetMostIdleSchedulerAndAddPodInAdvance(pod *v1.Pod) string {
	dq.lock.Lock()
	defer dq.lock.Unlock()

	result := ""
	max := math.MaxInt32
	// Ref: https://en.wikipedia.org/wiki/reservoir_sampling for more details about Reservoir Sampling.
	var randomPoolSize int
	for schedulerName := range dq.Schedulers {
		cnt := 0
		if dq.SchedulerToPods[schedulerName] != nil {
			cnt = dq.SchedulerToPods[schedulerName].Len()
		}

		if cnt < max {
			randomPoolSize = 1
			max = cnt
			result = schedulerName
		} else if cnt == max {
			randomPoolSize++
			if rand.Intn(randomPoolSize) == 0 {
				result = schedulerName
			}
		}
	}
	if result != "" {
		dq.addPod(pod, result)
	}
	return result
}

type OwnerInfo interface {
	AddDispatchedUnboundPod(pod *v1.Pod, schedulerName string)
	SetDispatchedUnboundPod(pod *v1.Pod, schedulerName string) string
	DeleteDispatchedUnboundPod(pod *v1.Pod)
	SelectSchedulerAndSetDispatchedUnboundPod(pod *v1.Pod) string
}

type ownerInfo struct {
	// key is owner name
	ownerToUnboundPods map[string]*ownerPodsInfo
	// key is pod key, value is owner name
	podToOwner map[string]string
	lock       sync.RWMutex
}

type ownerPodsInfo struct {
	schedulerName string
	unBoundPods   sets.String
}

func NewOwnerInfo() *ownerInfo {
	return &ownerInfo{
		ownerToUnboundPods: map[string]*ownerPodsInfo{},
		podToOwner:         map[string]string{},
	}
}

func newOwnerPodsInfo() *ownerPodsInfo {
	return &ownerPodsInfo{
		unBoundPods: sets.NewString(),
	}
}

func (oInfo *ownerInfo) AddDispatchedUnboundPod(pod *v1.Pod, schedulerName string) {
	if schedulerName == "" {
		return
	}
	podKey := podutil.GeneratePodKey(pod)
	podOwner := podutil.GetPodOwnerInfoKey(pod)
	if podOwner == "" {
		return
	}

	oInfo.lock.Lock()
	defer oInfo.lock.Unlock()
	oInfo.addPod(podOwner, podKey, schedulerName)
}

func (oInfo *ownerInfo) SetDispatchedUnboundPod(pod *v1.Pod, schedulerName string) string {
	if schedulerName == "" {
		return schedulerName
	}
	podKey := podutil.GeneratePodKey(pod)
	podOwner := podutil.GetPodOwnerInfoKey(pod)
	if podOwner == "" {
		return schedulerName
	}
	oInfo.lock.Lock()
	defer oInfo.lock.Unlock()
	existingScheduler := oInfo.getOwnerScheduler(podOwner)
	if existingScheduler != "" && existingScheduler != schedulerName {
		klog.InfoS("WARN: Scheduler was ever assigned to pod, so could not set the newly selected scheduler to that owner", "schedulerName", existingScheduler, "pod", klog.KObj(pod), "podKey", podutil.GeneratePodKey(pod), "NewSchedulerName", schedulerName)
		schedulerName = existingScheduler
	}
	oInfo.addPod(podOwner, podKey, schedulerName)
	return schedulerName
}

func (oInfo *ownerInfo) addPod(podOwner, podKey, schedulerName string) {
	if oInfo.ownerToUnboundPods == nil {
		oInfo.ownerToUnboundPods[podOwner] = newOwnerPodsInfo()
	}
	ownerPodsInfo, ok := oInfo.ownerToUnboundPods[podOwner]
	if !ok || ownerPodsInfo == nil {
		oInfo.ownerToUnboundPods[podOwner] = newOwnerPodsInfo()
		ownerPodsInfo = oInfo.ownerToUnboundPods[podOwner]
	}
	ownerPodsInfo.unBoundPods.Insert(podKey)
	ownerPodsInfo.schedulerName = schedulerName

	oInfo.podToOwner[podKey] = podOwner
}

func (oInfo *ownerInfo) DeleteDispatchedUnboundPod(pod *v1.Pod) {
	oInfo.lock.Lock()
	defer oInfo.lock.Unlock()

	podKey := podutil.GeneratePodKey(pod)
	podOwner := oInfo.podToOwner[podKey]
	ownerPodsInfo := oInfo.ownerToUnboundPods[podOwner]
	if ownerPodsInfo != nil {
		ownerPodsInfo.unBoundPods.Delete(podKey)
		if ownerPodsInfo.unBoundPods.Len() == 0 {
			delete(oInfo.ownerToUnboundPods, podOwner)
		}
	}

	delete(oInfo.podToOwner, podKey)
}

func (oInfo *ownerInfo) SelectSchedulerAndSetDispatchedUnboundPod(pod *v1.Pod) string {
	podKey := podutil.GeneratePodKey(pod)
	podOwner := podutil.GetPodOwnerInfoKey(pod)
	if podOwner == "" {
		return ""
	}
	oInfo.lock.Lock()
	defer oInfo.lock.Unlock()
	schedulerName := oInfo.getOwnerScheduler(podOwner)
	if schedulerName != "" {
		oInfo.addPod(podOwner, podKey, schedulerName)
	}
	return schedulerName
}

func (oInfo *ownerInfo) getOwnerScheduler(podOwner string) string {
	ownerPodsInfo := oInfo.ownerToUnboundPods[podOwner]
	if ownerPodsInfo != nil {
		return ownerPodsInfo.schedulerName
	}
	return ""
}
