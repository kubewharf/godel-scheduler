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

package loadawarestore

import (
	"github.com/kubewharf/godel-scheduler/pkg/util/generationstore"
	podutil "github.com/kubewharf/godel-scheduler/pkg/util/pod"
	katalystv1alpha1 "github.com/kubewharf/katalyst-api/pkg/apis/node/v1alpha1"

	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/sets"
)

type podBasicInfo struct {
	Key              string
	MilliCPU, Memory int64
	PodResourceType  podutil.PodResourceType
}

func newPodBasicInfo(pod *v1.Pod) (*podBasicInfo, error) {
	resourceType, err := podutil.GetPodResourceType(pod)
	if err != nil {
		return nil, err
	}
	return &podBasicInfo{
		Key:             podutil.GetPodKey(pod),
		MilliCPU:        podutil.GetPodRequest(pod, v1.ResourceCPU, resource.DecimalSI).MilliValue(),
		Memory:          podutil.GetPodRequest(pod, v1.ResourceMemory, resource.DecimalSI).Value(),
		PodResourceType: resourceType,
	}, nil
}

// ----------------------------------- PodMetricInfos -----------------------------------

type PodMetricInfos struct {
	ProfileMilliCPU, ProfileMEM int64
	RequestMilliCPU, RequestMEM int64
	ProfilePods                 sets.String
}

func newPodMetricInfos(cnr *katalystv1alpha1.CustomNodeResource, existingPods map[string]podBasicInfo, resourceType podutil.PodResourceType) *PodMetricInfos {
	var profileMilliCPU, profileMEM int64
	var requestMilliCPU, requestMEM int64
	pods := sets.NewString()

	if cnr != nil && cnr.Status.NodeMetricStatus != nil {
		groupMetric := cnr.Status.NodeMetricStatus.GroupMetric
		for i := range groupMetric {
			if podutil.GetResourceTypeFromQoS(groupMetric[i].QoSLevel) == resourceType {
				u := groupMetric[i].GenericUsage
				profileMilliCPU += u.CPU.MilliValue()
				profileMEM += u.Memory.Value()
				pods.Insert(groupMetric[i].PodList...)
			}
		}
	}

	for key, pInfo := range existingPods {
		if pInfo.PodResourceType == resourceType && !pods.Has(key) {
			requestMilliCPU += pInfo.MilliCPU
			requestMEM += pInfo.Memory
		}
	}

	return &PodMetricInfos{
		ProfileMilliCPU: profileMilliCPU,
		ProfileMEM:      profileMEM,
		RequestMilliCPU: requestMilliCPU,
		RequestMEM:      requestMEM,
		ProfilePods:     pods,
	}
}

func (i *PodMetricInfos) Clone() *PodMetricInfos {
	clone := *i // No need to DeepCopy sets for Snapshot
	return &clone
}

// ----------------------------------- NodeMetricInfo -----------------------------------

// ATTENTION: the NodeMetricInfo's lifecycle is accompanied by CNR, not CNR.Status.NodeMetricStatus
type NodeMetricInfo struct {
	name             string
	cnrExist         bool
	updateTime       metav1.Time
	gtPodMetricInfos *PodMetricInfos
	bePodMetricInfos *PodMetricInfos
	allPods          map[string]podBasicInfo
	generation       int64
}

var _ generationstore.StoredObj = &NodeMetricInfo{}

func NewNodeMetricInfo(nodeName string, cnr *katalystv1alpha1.CustomNodeResource) *NodeMetricInfo {
	return &NodeMetricInfo{
		name:             nodeName,
		cnrExist:         cnr != nil,
		updateTime:       parseUpdateTimeFromCNR(cnr),
		gtPodMetricInfos: newPodMetricInfos(cnr, nil, podutil.GuaranteedPod),
		bePodMetricInfos: newPodMetricInfos(cnr, nil, podutil.BestEffortPod),
		allPods:          make(map[string]podBasicInfo),
	}
}

func (i *NodeMetricInfo) Reset(cnr *katalystv1alpha1.CustomNodeResource) {
	i.cnrExist = cnr != nil
	i.updateTime = parseUpdateTimeFromCNR(cnr)
	i.gtPodMetricInfos = newPodMetricInfos(cnr, i.allPods, podutil.GuaranteedPod)
	i.bePodMetricInfos = newPodMetricInfos(cnr, i.allPods, podutil.BestEffortPod)
}

func (i *NodeMetricInfo) PodOp(pInfo *podBasicInfo, isCacheStore, isAdd bool) {
	if isAdd {
		i.allPods[pInfo.Key] = *pInfo
	} else {
		delete(i.allPods, pInfo.Key)
	}

	var infos *PodMetricInfos
	if pInfo.PodResourceType == podutil.GuaranteedPod {
		infos = i.gtPodMetricInfos
	} else {
		infos = i.bePodMetricInfos
	}
	if isCacheStore && infos.ProfilePods.Has(pInfo.Key) {
		// 1. We won't read ProfilePods in Snapshot, because we believe that 'unscheduled Pod should not appear in ProfilePods'.
		// 2. We only care about pods that have not been counted by NodeMetric.
		return
	}

	if isAdd {
		infos.RequestMilliCPU += pInfo.MilliCPU
		infos.RequestMEM += pInfo.Memory
	} else {
		infos.RequestMilliCPU -= pInfo.MilliCPU
		infos.RequestMEM -= pInfo.Memory
	}
}

func (i *NodeMetricInfo) GetGeneration() int64 {
	return i.generation
}

func (i *NodeMetricInfo) SetGeneration(generation int64) {
	i.generation = generation
}

func (i *NodeMetricInfo) CanBeRecycle() bool {
	if i == nil {
		return true
	}
	return !i.cnrExist && len(i.allPods) == 0
}

func (i *NodeMetricInfo) Clone() *NodeMetricInfo {
	return &NodeMetricInfo{
		name:             i.name,
		cnrExist:         i.cnrExist,
		updateTime:       i.updateTime,
		gtPodMetricInfos: i.gtPodMetricInfos.Clone(),
		bePodMetricInfos: i.bePodMetricInfos.Clone(),
		allPods:          cloneAllPods(i.allPods),
		generation:       i.generation,
	}
}

func parseUpdateTimeFromCNR(cnr *katalystv1alpha1.CustomNodeResource) metav1.Time {
	if cnr == nil || cnr.Status.NodeMetricStatus == nil {
		return metav1.Time{}
	}
	return cnr.Status.NodeMetricStatus.UpdateTime
}

func cloneAllPods(allPods map[string]podBasicInfo) map[string]podBasicInfo {
	ret := make(map[string]podBasicInfo, len(allPods))
	for key, info := range allPods {
		ret[key] = info
	}
	return ret
}
