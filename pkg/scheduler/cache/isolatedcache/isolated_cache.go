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

import "time"

type IsolatedCache interface {
	// cache node for pods based on owner
	// we will only cache node for a small period of time, so memory increasing won't be unacceptable
	CacheNodeForPodOwner(podOwner string, nodeName string, nodeGroup string) error

	// cache nodes for pods based on owner
	// we will only cache nodes for a small period of time, so memory increasing won't be unacceptable
	CacheAscendingOrderNodesForPodOwner(podOwner string, nodeGroup string, nodeNames []string) error

	// IsNodeInCachedMap checks if the node is still in cached nodes
	IsNodeInCachedMap(podOwner string, nodeName string) (bool, string)

	// GetOrderedNodesForPodOwner gets ordered nodes from cached nodes
	GetOrderedNodesForPodOwner(podOwner string) []string

	// DeleteNodeForPodOwner deletes node from cached nodes
	DeleteNodeForPodOwner(podOwner string, nodeName string) error

	// CleanupCachedNodesForPodOwner cleanup cached nodes.
	// It should be called inertly during scheduling to maintain infomations without lock.
	CleanupCachedNodesForPodOwner()

	GetPreemptionPolicy(deployName string) string
	CachePreemptionPolicy(deployName string, policyName string)
	CleanupPreemptionPolicyForPodOwner()
}

type isolatedCache struct {
	// cached nodes for pod owner
	cachedNodesForPodOwner cachedNodes
	latestCleanupTimestamp time.Time

	cachedPreemptionPoliciesForPodOwner    cachedPreemptionPolicies
	latestPreemptionPoliesCleanupTimestamp time.Time
}

func NewIsolatedCache() IsolatedCache {
	return &isolatedCache{
		cachedNodesForPodOwner:                 newCachedNodes(),
		latestCleanupTimestamp:                 time.Now(),
		cachedPreemptionPoliciesForPodOwner:    newCachedPreemptionPolicies(),
		latestPreemptionPoliesCleanupTimestamp: time.Now(),
	}
}
