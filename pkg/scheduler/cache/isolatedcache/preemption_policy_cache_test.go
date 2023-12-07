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
	"testing"
	"time"
)

func TestPreemptionPolicy(t *testing.T) {
	tests := []struct {
		name                string
		latestUpdateTime    time.Time
		cacheTime           time.Time
		expectedBeforeClean string
		expectedAfterClean  string
	}{
		{
			name:                "More than 24 hours since last cleanup, items cached more than 24 hours, need clean",
			latestUpdateTime:    time.Now().Add(-time.Hour * 25),
			cacheTime:           time.Now().Add(-time.Hour * 25),
			expectedBeforeClean: "Ascending",
			expectedAfterClean:  "",
		},
		{
			name:                "More than 24 hours since last cleanup, items cached not more than 24 hours, not need clean",
			latestUpdateTime:    time.Now().Add(-time.Hour * 25),
			cacheTime:           time.Now().Add(-time.Hour * 23),
			expectedBeforeClean: "Ascending",
			expectedAfterClean:  "Ascending",
		},
		{
			name:                "Not more than 24 hours since last cleanup, items cached more than 24 hours, not need clean",
			latestUpdateTime:    time.Now().Add(-time.Hour * 23),
			cacheTime:           time.Now().Add(-time.Hour * 25),
			expectedBeforeClean: "Ascending",
			expectedAfterClean:  "Ascending",
		},
		{
			name:                "Not more than 24 hours since last cleanup, items cached not more than 24 hours, not need clean",
			latestUpdateTime:    time.Now().Add(-time.Hour * 23),
			cacheTime:           time.Now().Add(-time.Hour * 25),
			expectedBeforeClean: "Ascending",
			expectedAfterClean:  "Ascending",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cache := &isolatedCache{
				latestPreemptionPoliesCleanupTimestamp: tt.latestUpdateTime,
				cachedPreemptionPoliciesForPodOwner:    newCachedPreemptionPolicies(),
			}
			podOwner := "dp"
			policy := "Ascending"
			cache.CachePreemptionPolicy(podOwner, policy)
			gotPolicy := cache.GetPreemptionPolicy(podOwner)
			if gotPolicy != tt.expectedBeforeClean {
				t.Errorf("expected %s but got %s", tt.expectedBeforeClean, gotPolicy)
			}
			cache.cachedPreemptionPoliciesForPodOwner[podOwner].settingTime = tt.cacheTime
			cache.CleanupPreemptionPolicyForPodOwner()
			gotPolicy = cache.GetPreemptionPolicy(podOwner)
			if gotPolicy != tt.expectedAfterClean {
				t.Errorf("expected %s but got %s", tt.expectedAfterClean, gotPolicy)
			}
		})
	}
}
