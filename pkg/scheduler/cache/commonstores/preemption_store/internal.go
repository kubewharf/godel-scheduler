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

package preemptionstore

import (
	"fmt"

	"k8s.io/apimachinery/pkg/api/errors"
	corelister "k8s.io/client-go/listers/core/v1"
	"k8s.io/klog/v2"

	framework "github.com/kubewharf/godel-scheduler/pkg/framework/api"
	"github.com/kubewharf/godel-scheduler/pkg/util/generationstore"
	podutil "github.com/kubewharf/godel-scheduler/pkg/util/pod"
)

var _ generationstore.StoredObj = &PreemptionDetailForNode{}

type PreemptionDetailForNode struct {
	// 1. preemption items from pod's nominated node annotation and
	// 2. preemption items saved in scheduling process
	//    before updating nominated node in apiserver & deleting victims in apiserver
	VictimToPreemptors generationstore.Store
	Generation         int64
}

func NewCachePreemptionDetailForNode() *PreemptionDetailForNode {
	return &PreemptionDetailForNode{
		VictimToPreemptors: generationstore.NewListStore(),
	}
}

func NewSnapshotPreemptionDetailForNode() *PreemptionDetailForNode {
	return &PreemptionDetailForNode{
		VictimToPreemptors: generationstore.NewRawStore(),
	}
}

func (item *PreemptionDetailForNode) GetGeneration() int64 {
	return item.Generation
}

func (item *PreemptionDetailForNode) SetGeneration(g int64) {
	item.Generation = g
}

func (item *PreemptionDetailForNode) AddPreemptItem(victimKey, preemptorKey string) {
	var s framework.GenerationStringSet
	if obj := item.VictimToPreemptors.Get(victimKey); obj != nil {
		s = obj.(framework.GenerationStringSet)
	} else {
		s = framework.NewGenerationStringSet()
	}
	s.Insert(preemptorKey)
	item.VictimToPreemptors.Set(victimKey, s)
}

func (item *PreemptionDetailForNode) RemovePreemptItem(victimKey, preemptorKey string) {
	var s framework.GenerationStringSet
	if obj := item.VictimToPreemptors.Get(victimKey); obj != nil {
		s = obj.(framework.GenerationStringSet)
		s.Delete(preemptorKey)
		if s.Len() == 0 {
			item.VictimToPreemptors.Delete(victimKey)
		} else {
			item.VictimToPreemptors.Set(victimKey, s)
		}
	}
}

func (item *PreemptionDetailForNode) GetPreemptorsByVictim(victimKey string) []string {
	if item == nil {
		return nil
	}
	obj := item.VictimToPreemptors.Get(victimKey)
	if obj == nil {
		return nil
	}
	return obj.(framework.GenerationStringSet).Strings()
}

func (item *PreemptionDetailForNode) IsEmpty() bool {
	return item.VictimToPreemptors.Len() == 0
}

func (item *PreemptionDetailForNode) CleanUpResidualPreemptionItems(podLister corelister.PodLister) {
	item.VictimToPreemptors.Range(func(victimKey string, obj generationstore.StoredObj) {
		if obj == nil {
			return
		}
		s := obj.(framework.GenerationStringSet)
		s.Range(func(preemptorKey string) {
			namespace, name, uid, err := podutil.ParsePodKey(preemptorKey)
			if err != nil {
				klog.InfoS("Failed to parse preemptor key", "err", err, "preemptorKey", preemptorKey)
				return
			}
			preemptor, err := podLister.Pods(namespace).Get(name)
			if err != nil {
				if !errors.IsNotFound(err) {
					klog.InfoS("Failed to get preemptor", "err", err, "preemptorKey", preemptorKey)
					return
				}
			} else {
				if preemptor.UID == uid {
					return
				}
			}
			s.Delete(preemptorKey)
			klog.V(4).InfoS("Cleaned up residual preemption item", "victim", victimKey, "preemptor", preemptorKey)
		})
		if s.Len() == 0 {
			item.VictimToPreemptors.Delete(victimKey)
		}
	})
}

type PreemptionDetails struct {
	NodeToVictims generationstore.Store
}

func NewSnapshotPreemptionDetails() *PreemptionDetails {
	return &PreemptionDetails{
		NodeToVictims: generationstore.NewRawStore(),
	}
}

func NewCachePreemptionDetails() *PreemptionDetails {
	return &PreemptionDetails{
		NodeToVictims: generationstore.NewListStore(),
	}
}

func (pDetail *PreemptionDetails) AddPreemptItem(nodeName, victimKey, preemptorKey string) {
	if pDetail == nil {
		return
	}
	var detailForNode *PreemptionDetailForNode
	if obj := pDetail.NodeToVictims.Get(nodeName); obj != nil {
		detailForNode = obj.(*PreemptionDetailForNode)
	} else {
		if _, ok := pDetail.NodeToVictims.(generationstore.ListStore); ok {
			detailForNode = NewCachePreemptionDetailForNode()
		} else {
			detailForNode = NewSnapshotPreemptionDetailForNode()
		}
	}
	detailForNode.AddPreemptItem(victimKey, preemptorKey)
	pDetail.NodeToVictims.Set(nodeName, detailForNode)
}

func (pDetail *PreemptionDetails) RemovePreemptItem(nodeName, victimKey, preemptorKey string) {
	if pDetail == nil {
		return
	}
	if obj := pDetail.NodeToVictims.Get(nodeName); obj != nil {
		detailForNode := obj.(*PreemptionDetailForNode)
		detailForNode.RemovePreemptItem(victimKey, preemptorKey)
		if detailForNode.IsEmpty() {
			pDetail.NodeToVictims.Delete(nodeName)
		} else {
			pDetail.NodeToVictims.Set(nodeName, detailForNode)
		}
	}
}

func (pDetail *PreemptionDetails) GetPreemptorsByVictim(nodeName, victimKey string) []string {
	if pDetail == nil {
		return nil
	}
	if obj := pDetail.NodeToVictims.Get(nodeName); obj != nil {
		detailForNode := obj.(*PreemptionDetailForNode)
		return detailForNode.GetPreemptorsByVictim(victimKey)
	}
	return nil
}

func (pDetail *PreemptionDetails) CleanUpResidualPreemptionItems(podLister corelister.PodLister) {
	pDetail.NodeToVictims.Range(func(nodeName string, obj generationstore.StoredObj) {
		if obj == nil {
			return
		}
		detailForNode := obj.(*PreemptionDetailForNode)
		detailForNode.CleanUpResidualPreemptionItems(podLister)
		if detailForNode.IsEmpty() {
			pDetail.NodeToVictims.Delete(nodeName)
		}
	})
}

func (pDetail *PreemptionDetails) String() string {
	if pDetail == nil {
		return ""
	}
	return fmt.Sprintf("%v", *pDetail)
}
