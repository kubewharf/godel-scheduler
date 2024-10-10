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

package reservation

import (
	"fmt"

	"github.com/kubewharf/godel-scheduler/pkg/framework/api"
	framework "github.com/kubewharf/godel-scheduler/pkg/framework/api"
	"github.com/kubewharf/godel-scheduler/pkg/util/generationstore"

	v1 "k8s.io/api/core/v1"
)

// NodeReservationInfo is a collection of reservationInfo belong to the same node
type NodeReservationInfo struct {
	// key is placeholder, value is ReservationInfoGroup
	groups     generationstore.Store
	generation int64
}

func (n *NodeReservationInfo) GetStore() generationstore.Store {
	return n.groups
}

func (n *NodeReservationInfo) GetGeneration() int64 {
	return n.generation
}

func (n *NodeReservationInfo) SetGeneration(generation int64) {
	n.generation = generation
}

func (n *NodeReservationInfo) SetReservationInfoGroup(placeholder string, group *ReservationInfoGroup) {
	n.groups.Set(placeholder, group)
}

func (n *NodeReservationInfo) DeleteReservationInfoGroup(placeholder string) {
	n.groups.Delete(placeholder)
}

func NewCacheNodeReservationInfo() *NodeReservationInfo {
	return &NodeReservationInfo{
		groups:     generationstore.NewListStore(),
		generation: 0,
	}
}

func NewSnapshotNodeReservationInfo() *NodeReservationInfo {
	return &NodeReservationInfo{
		groups:     generationstore.NewRawStore(),
		generation: 0,
	}
}

func (n *NodeReservationInfo) addReservationInfo(reservationInfo *ReservationInfo) {
	o := n.groups.Get(reservationInfo.placeholder)
	if o == nil {
		o = NewReservationInfoGroup()
	}
	riGroup := o.(*ReservationInfoGroup)
	riGroup.addReservationInfo(reservationInfo)
	n.groups.Set(reservationInfo.placeholder, o)
}

func (n *NodeReservationInfo) getReservationInfo(id *ReservationIdentifier) *ReservationInfo {
	o := n.groups.Get(id.placeholder)
	if o == nil {
		return nil
	}

	g := o.(*ReservationInfoGroup)
	return g.getReservationInfo(id)
}

func (n *NodeReservationInfo) deleteReservationInfo(id *ReservationIdentifier) {
	o := n.groups.Get(id.placeholder)
	if o == nil {
		return
	}

	g := o.(*ReservationInfoGroup)
	g.deleteReservationInfo(id)
	if g.len() == 0 {
		n.DeleteReservationInfoGroup(id.placeholder)
		return
	}

	n.SetReservationInfoGroup(id.placeholder, g)
}

func (n *NodeReservationInfo) listReservationInfos(placeholder string) []*ReservationInfo {
	o := n.groups.Get(placeholder)
	if o == nil {
		return nil
	}

	g := o.(*ReservationInfoGroup)
	return g.list()
}

func (n *NodeReservationInfo) Keys() []string {
	return n.groups.Keys()
}

func (n *NodeReservationInfo) len() int {
	return n.groups.Len()
}

func (n *NodeReservationInfo) UpdateSnapshot(snapshot *NodeReservationInfo, cloneFunc generationstore.CloneFunc, cleanFunc generationstore.CleanFunc) {
	if n == nil || snapshot == nil {
		return
	}

	cacheStore, snapshotStore := framework.TransferGenerationStore(n.groups, snapshot.groups)
	cacheStore.UpdateRawStore(snapshotStore, cloneFunc, cleanFunc)
}

// ------------------------------------------------------------------------------------------------------------------------

type NodeReservationStore struct {
	// nodeInfos is a collection of reservationInfo, each unit is a collection of reservation info on the same node
	// key is node name, value is reservationInfo
	nodeInfos                  generationstore.Store
	newNodeReservationInfoFunc func() *NodeReservationInfo
}

func NewCacheNodeReservationStore() *NodeReservationStore {
	return &NodeReservationStore{
		nodeInfos:                  generationstore.NewListStore(),
		newNodeReservationInfoFunc: NewCacheNodeReservationInfo,
	}
}

func NewSnapshotNodeReservationStore() *NodeReservationStore {
	return &NodeReservationStore{
		nodeInfos:                  generationstore.NewRawStore(),
		newNodeReservationInfoFunc: NewSnapshotNodeReservationInfo,
	}
}

func (r *NodeReservationStore) Len() int {
	return r.nodeInfos.Len()
}

func (r *NodeReservationStore) ConditionRange(f func(key string, nodeInfo *NodeReservationInfo) bool) bool {
	if r == nil {
		return false
	}

	return r.nodeInfos.ConditionRange(func(key string, obj generationstore.StoredObj) bool {
		nodeInfo := obj.(*NodeReservationInfo)
		return f(key, nodeInfo)
	})
}

func (r *NodeReservationStore) UpdateSnapshot(s *NodeReservationStore, clone generationstore.CloneFunc, clean generationstore.CleanFunc) {
	cacheInfos, snapshotInfos := r.nodeInfos, s.nodeInfos
	cacheStore, snapshotStore := framework.TransferGenerationStore(cacheInfos, snapshotInfos)
	cacheStore.UpdateRawStore(snapshotStore, clone, clean)
}

func (r *NodeReservationStore) SetNodeReservationInfo(nodeName string, nodeInfo *NodeReservationInfo) {
	r.nodeInfos.Set(nodeName, nodeInfo)
}

func (r *NodeReservationStore) GetNodeReservationInfo(nodeName string) *NodeReservationInfo {
	o := r.nodeInfos.Get(nodeName)
	if o == nil {
		return nil
	}

	return o.(*NodeReservationInfo)
}

func (r *NodeReservationStore) DeleteNodeReservationInfo(nodeName string) {
	r.nodeInfos.Delete(nodeName)
}

func (r *NodeReservationStore) AddReservationInfo(info *ReservationInfo) {
	if info == nil {
		return
	}

	nodeInfo := r.GetNodeReservationInfo(info.nodeName)
	if nodeInfo == nil {
		nodeInfo = r.newNodeReservationInfoFunc()
	}

	nodeInfo.addReservationInfo(info)
	r.SetNodeReservationInfo(info.nodeName, nodeInfo)
}

func (r *NodeReservationStore) GetReservationInfo(id *ReservationIdentifier) *ReservationInfo {
	nodeInfo := r.GetNodeReservationInfo(id.nodeName)
	if nodeInfo == nil {
		return nil
	}

	return nodeInfo.getReservationInfo(id)
}

func (r *NodeReservationStore) DeleteReservationInfo(id *ReservationIdentifier) {
	nodeInfo := r.GetNodeReservationInfo(id.nodeName)
	if nodeInfo == nil {
		return
	}

	nodeInfo.deleteReservationInfo(id)
	if nodeInfo.len() == 0 {
		r.DeleteNodeReservationInfo(id.nodeName)
		return
	}

	r.SetNodeReservationInfo(id.nodeName, nodeInfo)
}

func (r *NodeReservationStore) SetMatchedPodForReservation(id *ReservationIdentifier, matchedPod *v1.Pod) error {
	info := r.GetReservationInfo(id)
	if info == nil {
		return fmt.Errorf("reservation info not found, id: %v", id)
	}

	info.MatchedPod = matchedPod
	r.AddReservationInfo(info)
	return nil
}

func (r *NodeReservationStore) GetAvailablePlaceholderPodsOnNode(nodeName, placeholder string) (api.ReservationPlaceholderMap, error) {
	nodeInfo := r.GetNodeReservationInfo(nodeName)
	if nodeInfo == nil {
		return nil, fmt.Errorf("node reservation info not found")
	}

	reservationInfos := nodeInfo.listReservationInfos(placeholder)
	if reservationInfos == nil {
		return nil, fmt.Errorf("reservation info group not found")
	}

	placeholders := make(api.ReservationPlaceholderMap, 0)
	for _, reservationInfo := range reservationInfos {
		if reservationInfo.IsAvailable() {
			placeholders[reservationInfo.placeholderPodKey] = reservationInfo.PlaceholderPod
		}
	}
	return placeholders, nil
}

func (r *NodeReservationStore) GetAvailablePlaceholderPods(placeholder string) (api.ReservationPlaceholdersOfNodes, error) {
	placeholders := make(api.ReservationPlaceholdersOfNodes, 0)
	r.nodeInfos.Range(func(nodeName string, obj generationstore.StoredObj) {
		nodeInfo := obj.(*NodeReservationInfo)
		reservationInfos := nodeInfo.listReservationInfos(placeholder)
		if reservationInfos != nil {
			placeholderMap := make(api.ReservationPlaceholderMap, len(reservationInfos))
			for _, res := range reservationInfos {
				if res.IsAvailable() {
					placeholderMap[res.placeholderPodKey] = res.PlaceholderPod
				}
			}

			if len(placeholderMap) > 0 {
				placeholders[nodeName] = placeholderMap
			}
		}
		return
	})

	return placeholders, nil
}
