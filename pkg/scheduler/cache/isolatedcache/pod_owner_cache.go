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
	defaultNodeCacheCleanupInterval = 2 * time.Minute

	defaultNodeCacheExpirationDuration = 3 * time.Minute
)

type nodeTmpInfo struct {
	settingTime time.Time
	// score int64
	nodeGroup string
}

// type NodesSet map[string]nodeTmpInfo

type cachedNodes map[string]*podOwnerCachedNodes

func newCachedNodes() cachedNodes {
	return make(map[string]*podOwnerCachedNodes)
}

type podOwnerCachedNodes struct {
	// nodeMap is used to store actual node items
	nodeMap map[string]*nodeTmpInfo
	// nodeSlice is used to show the order of cached nodes
	// since deleting items from slice has some cost,
	// we will not remove the cached node from slice in `DeleteNodeForPodOwner`
	// so some nodes in this slice may not exist in nodeMap
	nodeSlice []string
}

func newPodOwnerCachedNodes() *podOwnerCachedNodes {
	return &podOwnerCachedNodes{
		nodeMap:   make(map[string]*nodeTmpInfo),
		nodeSlice: make([]string, 0),
	}
}

// CacheNodeForPodOwner caches node for pods based on owner
// We will only cache node for a small period of time, so memory increasing won't be unacceptable
func (cache *isolatedCache) CacheNodeForPodOwner(podOwner string, nodeName string, nodeGroup string) error {
	if cache.cachedNodesForPodOwner == nil {
		cache.cachedNodesForPodOwner = make(map[string]*podOwnerCachedNodes)
	}
	if cache.cachedNodesForPodOwner[podOwner] == nil {
		cache.cachedNodesForPodOwner[podOwner] = newPodOwnerCachedNodes()
	}
	cache.cachedNodesForPodOwner[podOwner].nodeMap[nodeName] = &nodeTmpInfo{
		settingTime: time.Now(),
		nodeGroup:   nodeGroup,
	}
	// this may cause redundant nodes, but it doesn't matter since the source of true of nodeMap
	cache.cachedNodesForPodOwner[podOwner].nodeSlice = append(cache.cachedNodesForPodOwner[podOwner].nodeSlice, nodeName)
	return nil
}

// CacheAscendingOrderNodesForPodOwner caches nodes for pods based on owner
// We will only cache nodes for a small period of time, so memory increasing won't be unacceptable
// We assume the caller has already sorted the nodes (ascending order) and the function is called according to the node order
func (cache *isolatedCache) CacheAscendingOrderNodesForPodOwner(podOwner string, nodeGroup string, nodeNames []string) error {
	if cache.cachedNodesForPodOwner == nil {
		cache.cachedNodesForPodOwner = make(map[string]*podOwnerCachedNodes)
	}
	if cache.cachedNodesForPodOwner[podOwner] == nil {
		cache.cachedNodesForPodOwner[podOwner] = newPodOwnerCachedNodes()
	}

	// reverse the node order
	for i := len(nodeNames) - 1; i >= 0; i-- {
		cache.cachedNodesForPodOwner[podOwner].nodeMap[nodeNames[i]] = &nodeTmpInfo{
			settingTime: time.Now(),
			nodeGroup:   nodeGroup,
		}
		// this may cause redundant nodes, but it doesn't matter since the source of true of nodeMap
		cache.cachedNodesForPodOwner[podOwner].nodeSlice = append(cache.cachedNodesForPodOwner[podOwner].nodeSlice, nodeNames[i])
	}

	return nil
}

func (cache *isolatedCache) IsNodeInCachedMap(podOwner string, nodeName string) (bool, string) {
	if cache.cachedNodesForPodOwner == nil || cache.cachedNodesForPodOwner[podOwner] == nil {
		return false, ""
	}
	if cache.cachedNodesForPodOwner[podOwner].nodeMap == nil || cache.cachedNodesForPodOwner[podOwner].nodeMap[nodeName] == nil {
		return false, ""
	}
	return true, cache.cachedNodesForPodOwner[podOwner].nodeMap[nodeName].nodeGroup
}

// get nodes from cache
func (cache *isolatedCache) GetOrderedNodesForPodOwner(podOwner string) []string {
	if cache.cachedNodesForPodOwner == nil || cache.cachedNodesForPodOwner[podOwner] == nil {
		return nil
	}

	return cache.cachedNodesForPodOwner[podOwner].nodeSlice
}

// delete cached node from scheduler cache
func (cache *isolatedCache) DeleteNodeForPodOwner(podOwner string, nodeName string) error {
	if cache.cachedNodesForPodOwner == nil {
		return nil
	}
	if cache.cachedNodesForPodOwner[podOwner] == nil {
		return nil
	}
	if cache.cachedNodesForPodOwner[podOwner].nodeMap == nil {
		delete(cache.cachedNodesForPodOwner, podOwner)
		return nil
	}

	delete(cache.cachedNodesForPodOwner[podOwner].nodeMap, nodeName)

	if len(cache.cachedNodesForPodOwner[podOwner].nodeMap) == 0 {
		delete(cache.cachedNodesForPodOwner, podOwner)
	}
	return nil
}

func (cache *isolatedCache) CleanupCachedNodesForPodOwner() {
	now := time.Now()
	if now.Sub(cache.latestCleanupTimestamp) < defaultNodeCacheCleanupInterval {
		return
	}
	cache.latestCleanupTimestamp = now

	for podOwner, cachedNodes := range cache.cachedNodesForPodOwner {
		for nodeName, tmpInfo := range cachedNodes.nodeMap {
			if now.After(tmpInfo.settingTime.Add(defaultNodeCacheExpirationDuration)) {
				klog.V(4).InfoS("Cached node was expired", "node", nodeName, "podOwner", podOwner)
				delete(cachedNodes.nodeMap, nodeName)
			}
		}
		if len(cachedNodes.nodeMap) == 0 {
			delete(cache.cachedNodesForPodOwner, podOwner)
		}
	}
}
