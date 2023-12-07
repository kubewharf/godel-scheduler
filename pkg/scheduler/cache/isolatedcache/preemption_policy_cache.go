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

package isolatedcache

import (
	"time"

	"k8s.io/klog/v2"
)

const (
	defaultPreemptionPoliciesCleanupInterval = time.Hour * 24

	defaultPreemptionPoliciesExpirationDuration = time.Hour * 24
)

type cachedPreemptionPolicies map[string]*preemptionPolicyItem

func newCachedPreemptionPolicies() cachedPreemptionPolicies {
	return make(map[string]*preemptionPolicyItem)
}

type preemptionPolicyItem struct {
	policyName  string
	settingTime time.Time
}

func (cache *isolatedCache) GetPreemptionPolicy(podOwner string) string {
	item := cache.cachedPreemptionPoliciesForPodOwner[podOwner]
	if item == nil {
		return ""
	}
	item.settingTime = time.Now()
	return item.policyName
}

func (cache *isolatedCache) CachePreemptionPolicy(podOwner, policyName string) {
	cache.cachedPreemptionPoliciesForPodOwner[podOwner] = &preemptionPolicyItem{
		policyName:  policyName,
		settingTime: time.Now(),
	}
}

func (cache *isolatedCache) CleanupPreemptionPolicyForPodOwner() {
	now := time.Now()
	if now.Before(cache.latestPreemptionPoliesCleanupTimestamp.Add(defaultPreemptionPoliciesCleanupInterval)) {
		return
	}
	cache.latestPreemptionPoliesCleanupTimestamp = now

	for podOwner, cachedPolicyItem := range cache.cachedPreemptionPoliciesForPodOwner {
		if cachedPolicyItem == nil {
			delete(cache.cachedPreemptionPoliciesForPodOwner, podOwner)
		}
		if now.After(cachedPolicyItem.settingTime.Add(defaultPreemptionPoliciesExpirationDuration)) {
			klog.V(4).InfoS("Cached preemption policy is expired", "preemptionPolicyName", cachedPolicyItem.policyName, "podOwnerName", podOwner)
			delete(cache.cachedPreemptionPoliciesForPodOwner, podOwner)
		}
	}
}
