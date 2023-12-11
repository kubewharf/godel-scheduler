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

package api

import (
	"errors"
	"fmt"
	"strconv"
	"sync"

	nodev1alpha1 "github.com/kubewharf/godel-scheduler-api/pkg/apis/node/v1alpha1"
	schedulingv1a1 "github.com/kubewharf/godel-scheduler-api/pkg/apis/scheduling/v1alpha1"
	katalystv1alpha1 "github.com/kubewharf/katalyst-api/pkg/apis/node/v1alpha1"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/apimachinery/pkg/util/sets"
	utilfeature "k8s.io/apiserver/pkg/util/feature"
	"k8s.io/klog/v2"

	godelfeatures "github.com/kubewharf/godel-scheduler/pkg/features"
	godelutil "github.com/kubewharf/godel-scheduler/pkg/util"
	"github.com/kubewharf/godel-scheduler/pkg/util/features"
	"github.com/kubewharf/godel-scheduler/pkg/util/generationstore"
	"github.com/kubewharf/godel-scheduler/pkg/util/helper"
	podutil "github.com/kubewharf/godel-scheduler/pkg/util/pod"
)

type NodeInfo interface {
	GetNodeName() string
	GetGeneration() int64
	SetGeneration(generation int64)
	GetNodeLabels(podLauncher podutil.PodLauncher) map[string]string
	GetUsedPorts() HostPortInfo
	SetUsedPorts(HostPortInfo)

	GetGuaranteedRequested() *Resource
	GetGuaranteedNonZeroRequested() *Resource
	GetGuaranteedAllocatable() *Resource
	GetGuaranteedCapacity() *Resource
	GetBestEffortRequested() *Resource
	GetBestEffortNonZeroRequested() *Resource
	GetBestEffortAllocatable() *Resource
	SetGuaranteedRequested(*Resource)
	SetGuaranteedNonZeroRequested(*Resource)
	SetGuaranteedAllocatable(*Resource)
	SetGuaranteedCapacity(*Resource)
	SetBestEffortRequested(*Resource)
	SetBestEffortNonZeroRequested(*Resource)
	SetBestEffortAllocatable(*Resource)

	GetNodeInSchedulerPartition() bool   // TODO
	SetNodeInSchedulerPartition(bool)    // TODO
	GetNMNodeInSchedulerPartition() bool // TODO
	SetNMNodeInSchedulerPartition(bool)  // TODO

	GetImageStates() map[string]*ImageStateSummary
	SetImageStates(map[string]*ImageStateSummary)
	SetTransientInfo(info *TransientSchedulerInfo)
	GetTransientInfo() *TransientSchedulerInfo

	GetNode() *v1.Node
	GetNMNode() *nodev1alpha1.NMNode
	GetCNR() *katalystv1alpha1.CustomNodeResource
	SetNode(node *v1.Node) error
	SetNMNode(nmNode *nodev1alpha1.NMNode) error
	SetCNR(cnr *katalystv1alpha1.CustomNodeResource) error
	RemoveNode()
	RemoveNMNode()
	RemoveCNR()
	ObjectIsNil() bool
	SetNodePartition(inSchedulerPartition bool) error

	Clone() NodeInfo
	String() string

	GetPodsWithAffinity() []*PodInfo
	GetPodsWithRequiredAntiAffinity() []*PodInfo

	AddPod(pod *v1.Pod)
	RemovePod(pod *v1.Pod, preempt bool) error
	GetPods() []*PodInfo
	NumPods() int
	GetVictimCandidates(partitionInfo *PodPartitionInfo) []*PodInfo
	GetOccupiableResources(partitionInfo *PodPartitionInfo) *Resource

	VolumeLimits() map[v1.ResourceName]int64

	GetNumaTopologyStatus() *NumaTopologyStatus
	GetResourcesAvailableForSharedCoresPods(unavailableNumaList []int) *Resource
	GetResourcesRequestsOfSharedCoresPods() *Resource

	GetPrioritiesForPodsMayBePreempted(resourceType podutil.PodResourceType) []int64
}

var (
	_ NodeInfo                  = &NodeInfoImpl{}
	_ generationstore.StoredObj = &NodeInfoImpl{}
)

// NodeInfoImpl is node level aggregated information.
type NodeInfoImpl struct {
	// Overall node information, collected by k8s.
	Node *v1.Node

	// node information, collected by node manager
	NMNode *nodev1alpha1.NMNode

	// cnr stores additional/custom node information, such as numa topology, etc.
	CNR *katalystv1alpha1.CustomNodeResource

	// Whether Node is in the partition of the scheduler
	NodeInSchedulerPartition bool

	// Whether NMNode is in the partition of the scheduler
	NMNodeInSchedulerPartition bool

	// PodInfoMaintainer maintain all the pods running on the node.
	PodInfoMaintainer *PodInfoMaintainer

	// Ports allocated on the node.
	UsedPorts HostPortInfo

	// Total requested resources of all guaranteed pods on this node. This includes
	// assumed pods, which scheduler has sent for binding, but may not be scheduled
	// yet.
	GuaranteedRequested *Resource
	// Total requested resources of all guaranteed pods on this node with a minimum
	// value applied to each container's CPU and memory requests. This does not reflect
	// the actual resource requests for this node, but is used to avoid scheduling
	// many zero-request pods onto one node.
	GuaranteedNonZeroRequested *Resource
	// We store guaranteed allocatedResources (which is the minimum of Node.Status.Allocatable.*
	// and CNR.Status.ResourceAllocatable.*) explicitly
	// as int64, to avoid conversions and accessing map.
	GuaranteedAllocatable *Resource
	// GuaranteedCapacity is capacity resource of node
	GuaranteedCapacity *Resource

	// Total requested resources of all best-effort pods on this node. This includes assumed
	// pods, which scheduler has sent for binding, but may not be scheduled yet.
	BestEffortRequested *Resource
	// Total requested resources of all best-effort pods on this node with a minimum value
	// applied to each container's CPU and memory requests. This does not reflect
	// the actual resource requests for this node, but is used to avoid scheduling
	// many zero-request pods onto one node.
	BestEffortNonZeroRequested *Resource
	// We store best-effort allocatedResources (which is CNR.Status.BestEffortResourceAllocatable.*) explicitly
	// as int64, to avoid conversions and accessing map.
	BestEffortAllocatable *Resource

	// ImageStates holds the entry of an image if and only if this image is on the node. The entry can be used for
	// checking an image's existence and advanced usage (e.g., image locality scheduling policy) based on the image
	// state information.
	ImageStates map[string]*ImageStateSummary

	// TransientInfo holds the information pertaining to a scheduling cycle. This will be destructed at the end of
	// scheduling cycle.
	TransientInfo *TransientSchedulerInfo

	// Whenever NodeInfoImpl changes, generation is bumped.
	// This is used to avoid cloning it if the object didn't change.
	Generation int64

	NumaTopologyStatus *NumaTopologyStatus

	mu sync.RWMutex
}

// NewNodeInfo returns a ready to use empty NodeInfo object.
// If any pods are given in arguments, their information will be aggregated in
// the returned object.
func NewNodeInfo(pods ...*v1.Pod) NodeInfo {
	ni := &NodeInfoImpl{
		PodInfoMaintainer:          NewPodInfoMaintainer(),
		GuaranteedRequested:        &Resource{},
		GuaranteedNonZeroRequested: &Resource{},
		GuaranteedAllocatable:      &Resource{},
		GuaranteedCapacity:         &Resource{},
		BestEffortRequested:        &Resource{},
		BestEffortNonZeroRequested: &Resource{},
		BestEffortAllocatable:      &Resource{},
		TransientInfo:              NewTransientSchedulerInfo(),
		Generation:                 0,
		UsedPorts:                  make(HostPortInfo),
		ImageStates:                make(map[string]*ImageStateSummary),
	}
	if utilfeature.DefaultFeatureGate.Enabled(godelfeatures.NonNativeResourceSchedulingSupport) {
		ni.NumaTopologyStatus = newNumaTopologyStatus(NewResource(nil))
	}
	for _, pod := range pods {
		ni.AddPod(pod)
	}
	return ni
}

// GetNodeName returns the node name of the node info. Both Node and NMNode should share the same node name.
func (n *NodeInfoImpl) GetNodeName() string {
	n.mu.RLock()
	defer n.mu.RUnlock()
	return n.getNodeName()
}

func (n *NodeInfoImpl) getNodeName() string {
	if n == nil {
		return ""
	}
	if n.Node != nil {
		return n.Node.Name
	}
	if n.NMNode != nil {
		return n.NMNode.Name
	}
	return ""
}

func (n *NodeInfoImpl) GetGeneration() int64 {
	n.mu.RLock()
	defer n.mu.RUnlock()
	return n.Generation
}

func (n *NodeInfoImpl) SetGeneration(generation int64) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.Generation = generation
}

func (n *NodeInfoImpl) GetNodeLabels(podLauncher podutil.PodLauncher) map[string]string {
	n.mu.RLock()
	defer n.mu.RUnlock()
	switch podLauncher {
	case podutil.Kubelet:
		if n.Node != nil {
			return n.Node.Labels
		}
	case podutil.NodeManager:
		if n.NMNode != nil {
			return n.NMNode.Labels
		}
	}
	return map[string]string{}
}

func (n *NodeInfoImpl) GetUsedPorts() HostPortInfo {
	n.mu.RLock()
	defer n.mu.RUnlock()
	return n.UsedPorts
}

func (n *NodeInfoImpl) SetUsedPorts(info HostPortInfo) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.UsedPorts = info
}

func (n *NodeInfoImpl) GetPodsWithAffinity() []*PodInfo {
	n.mu.RLock()
	defer n.mu.RUnlock()
	return n.PodInfoMaintainer.GetPodsWithAffinity()
}

func (n *NodeInfoImpl) GetPodsWithRequiredAntiAffinity() []*PodInfo {
	n.mu.RLock()
	defer n.mu.RUnlock()
	return n.PodInfoMaintainer.GetPodsWithRequiredAntiAffinity()
}

func (n *NodeInfoImpl) GetNodeInSchedulerPartition() bool {
	n.mu.RLock()
	defer n.mu.RUnlock()
	return n.NodeInSchedulerPartition
}

func (n *NodeInfoImpl) GetNMNodeInSchedulerPartition() bool {
	n.mu.RLock()
	defer n.mu.RUnlock()
	return n.NMNodeInSchedulerPartition
}

func (n *NodeInfoImpl) SetNodeInSchedulerPartition(flag bool) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.NodeInSchedulerPartition = flag
}

func (n *NodeInfoImpl) SetNMNodeInSchedulerPartition(flag bool) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.NMNodeInSchedulerPartition = flag
}

func (n *NodeInfoImpl) GetPods() []*PodInfo {
	n.mu.RLock()
	defer n.mu.RUnlock()
	return n.PodInfoMaintainer.GetPods()
}

func (n *NodeInfoImpl) NumPods() int {
	n.mu.RLock()
	defer n.mu.RUnlock()
	return n.PodInfoMaintainer.Len()
}

// GetVictimCandidates returns all pods under the same PodResourceType that may be preempted.
// Whether or not they are actually victims depends on the cluster state and the currently preemptor.
func (n *NodeInfoImpl) GetVictimCandidates(partitionInfo *PodPartitionInfo) []*PodInfo {
	n.mu.RLock()
	defer n.mu.RUnlock()
	return n.PodInfoMaintainer.GetVictimCandidates(partitionInfo)
}

// GetOccupiableResources does a partition on the podinfomaintainer's Splay-Tree based on the PodPartitionInfo
// and returns the sum of the resources in the partition and the unused resources.
// All pods in a partition have the possibility of being preempted, which is a rough judgment.
func (n *NodeInfoImpl) GetOccupiableResources(partitionInfo *PodPartitionInfo) *Resource {
	n.mu.RLock()
	defer n.mu.RUnlock()
	info := n.PodInfoMaintainer.GetMaintainableInfoByPartition(partitionInfo)
	if partitionInfo.resourceType == podutil.GuaranteedPod {
		info.MilliCPU += n.GuaranteedAllocatable.MilliCPU - n.GuaranteedRequested.MilliCPU
		info.Memory += n.GuaranteedAllocatable.Memory - n.GuaranteedRequested.Memory
	} else {
		info.MilliCPU += n.BestEffortAllocatable.MilliCPU - n.BestEffortRequested.MilliCPU
		info.Memory += n.BestEffortAllocatable.Memory - n.BestEffortRequested.Memory
	}
	return &Resource{
		MilliCPU: info.MilliCPU,
		Memory:   info.Memory,
	}
}

func (n *NodeInfoImpl) GetGuaranteedRequested() *Resource {
	n.mu.RLock()
	defer n.mu.RUnlock()
	return n.GuaranteedRequested
}

func (n *NodeInfoImpl) GetGuaranteedNonZeroRequested() *Resource {
	n.mu.RLock()
	defer n.mu.RUnlock()
	return n.GuaranteedNonZeroRequested
}

func (n *NodeInfoImpl) GetGuaranteedAllocatable() *Resource {
	n.mu.RLock()
	defer n.mu.RUnlock()
	return n.GuaranteedAllocatable
}

func (n *NodeInfoImpl) GetGuaranteedCapacity() *Resource {
	n.mu.RLock()
	defer n.mu.RUnlock()
	return n.GuaranteedCapacity
}

func (n *NodeInfoImpl) GetBestEffortRequested() *Resource {
	n.mu.RLock()
	defer n.mu.RUnlock()
	return n.BestEffortRequested
}

func (n *NodeInfoImpl) GetBestEffortNonZeroRequested() *Resource {
	n.mu.RLock()
	defer n.mu.RUnlock()
	return n.BestEffortNonZeroRequested
}

func (n *NodeInfoImpl) GetBestEffortAllocatable() *Resource {
	n.mu.RLock()
	defer n.mu.RUnlock()
	return n.BestEffortAllocatable
}

func (n *NodeInfoImpl) SetGuaranteedRequested(r *Resource) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.GuaranteedRequested = r
}

func (n *NodeInfoImpl) SetGuaranteedNonZeroRequested(r *Resource) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.GuaranteedNonZeroRequested = r
}

func (n *NodeInfoImpl) SetGuaranteedAllocatable(r *Resource) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.GuaranteedAllocatable = r
}

func (n *NodeInfoImpl) SetGuaranteedCapacity(r *Resource) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.GuaranteedCapacity = r
}

func (n *NodeInfoImpl) SetBestEffortRequested(r *Resource) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.BestEffortRequested = r
}

func (n *NodeInfoImpl) SetBestEffortNonZeroRequested(r *Resource) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.BestEffortNonZeroRequested = r
}

func (n *NodeInfoImpl) SetBestEffortAllocatable(r *Resource) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.BestEffortAllocatable = r
}

func (n *NodeInfoImpl) SetImageStates(summary map[string]*ImageStateSummary) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.ImageStates = summary
}

func (n *NodeInfoImpl) GetImageStates() map[string]*ImageStateSummary {
	n.mu.RLock()
	defer n.mu.RUnlock()
	return n.ImageStates
}

func (n *NodeInfoImpl) SetTransientInfo(info *TransientSchedulerInfo) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.TransientInfo = info
}

func (n *NodeInfoImpl) GetTransientInfo() *TransientSchedulerInfo {
	n.mu.RLock()
	defer n.mu.RUnlock()
	return n.TransientInfo
}

// Node returns overall information about this node.
func (n *NodeInfoImpl) GetNode() *v1.Node {
	n.mu.RLock()
	defer n.mu.RUnlock()
	return n.getNode()
}

func (n *NodeInfoImpl) getNode() *v1.Node {
	if n == nil {
		return nil
	}
	return n.Node
}

// NMNode returns the NMNode info
func (n *NodeInfoImpl) GetNMNode() *nodev1alpha1.NMNode {
	if n == nil {
		return nil
	}
	n.mu.RLock()
	defer n.mu.RUnlock()
	return n.NMNode
}

func (n *NodeInfoImpl) ObjectIsNil() bool {
	if n == nil {
		return true
	}
	n.mu.RLock()
	defer n.mu.RUnlock()
	return n.NMNode == nil && n.Node == nil
}

// GetCNR returns overall information about this CNR.
func (n *NodeInfoImpl) GetCNR() *katalystv1alpha1.CustomNodeResource {
	if n == nil {
		return nil
	}
	n.mu.RLock()
	defer n.mu.RUnlock()
	return n.CNR
}

// TODO: remove some unused fields
// Clone returns a copy of this node.
func (n *NodeInfoImpl) Clone() NodeInfo {
	n.mu.RLock()
	defer n.mu.RUnlock()

	clone := &NodeInfoImpl{
		Node:                       n.Node,
		NMNode:                     n.NMNode,
		CNR:                        n.CNR,
		NodeInSchedulerPartition:   n.NodeInSchedulerPartition,
		NMNodeInSchedulerPartition: n.NMNodeInSchedulerPartition,
		PodInfoMaintainer:          n.PodInfoMaintainer.Clone(),
		GuaranteedRequested:        n.GuaranteedRequested.Clone(),
		GuaranteedNonZeroRequested: n.GuaranteedNonZeroRequested.Clone(),
		GuaranteedAllocatable:      n.GuaranteedAllocatable.Clone(),
		GuaranteedCapacity:         n.GuaranteedCapacity.Clone(),
		BestEffortRequested:        n.BestEffortRequested.Clone(),
		BestEffortNonZeroRequested: n.BestEffortNonZeroRequested.Clone(),
		BestEffortAllocatable:      n.BestEffortAllocatable.Clone(),
		TransientInfo:              n.TransientInfo,
		UsedPorts:                  make(HostPortInfo),
		ImageStates:                n.ImageStates,
		Generation:                 n.Generation,
		NumaTopologyStatus:         n.NumaTopologyStatus.clone(),
	}
	if len(n.UsedPorts) > 0 {
		// HostPortInfo is a map-in-map struct
		// make sure it's deep copied
		for ip, portMap := range n.UsedPorts {
			clone.UsedPorts[ip] = make(map[ProtocolPort]struct{})
			for protocolPort, v := range portMap {
				clone.UsedPorts[ip][protocolPort] = v
			}
		}
	}
	return clone
}

// VolumeLimits returns volume limits associated with the node
func (n *NodeInfoImpl) VolumeLimits() map[v1.ResourceName]int64 {
	n.mu.RLock()
	defer n.mu.RUnlock()
	volumeLimits := map[v1.ResourceName]int64{}
	if n.GuaranteedAllocatable != nil {
		for k, v := range n.GuaranteedAllocatable.ScalarResources {
			if helper.IsAttachableVolumeResourceName(k) {
				volumeLimits[k] = v
			}
		}
	}

	return volumeLimits
}

// String returns representation of human readable format of this NodeInfoImpl.
func (n *NodeInfoImpl) String() string {
	n.mu.RLock()
	defer n.mu.RUnlock()
	podKeys := make([]string, 0, n.PodInfoMaintainer.Len())
	n.PodInfoMaintainer.Range(func(pi *PodInfo) {
		podKeys = append(podKeys, pi.Pod.Name)
	})
	return fmt.Sprintf("&NodeInfoImpl{Pods:%v, GuaranteedRequestedResource:%#v, GuaranteedNonZeroRequest: %#v, UsedPort: %#v, GuarranteedAllocatableResource:%#v, "+
		"GuarranteedCapacityResource:%#v, BestEffortRequestedResource: %#v, BestEffortNonZeroRequest: %#v, BestEffortAllocatableResource: %#v}",
		podKeys, n.GuaranteedRequested, n.GuaranteedNonZeroRequested, n.UsedPorts, n.GuaranteedAllocatable, n.GuaranteedCapacity,
		n.BestEffortRequested, n.BestEffortNonZeroRequested, n.BestEffortAllocatable)
}

// update node info based on the pod and sign.
// The sign will be set to `+1` when AddPod and to `-1` when RemovePod.
func (n *NodeInfoImpl) update(podInfo *PodInfo, sign int64, preempt bool) {
	var requested, nonZeroRequested **Resource
	switch podInfo.PodResourceType {
	case podutil.GuaranteedPod:
		requested, nonZeroRequested = &n.GuaranteedRequested, &n.GuaranteedNonZeroRequested
	case podutil.BestEffortPod:
		requested, nonZeroRequested = &n.BestEffortRequested, &n.BestEffortNonZeroRequested
	default:
		klog.InfoS("Failed to parse resource type for pod", "pod", klog.KObj(podInfo.Pod), "err", podInfo.PodResourceTypeError)
		// if pod annotation is illegal but should be added to cache, the pod is considered as a guaranteed pod
		// TODO we need a mechanism to handle bound pod with illegal pod resource type
		requested, nonZeroRequested = &n.GuaranteedRequested, &n.GuaranteedNonZeroRequested
	}

	(*requested).MilliCPU += sign * podInfo.Res.MilliCPU
	(*requested).Memory += sign * podInfo.Res.Memory
	(*requested).EphemeralStorage += sign * podInfo.Res.EphemeralStorage
	if (*requested).ScalarResources == nil && len(podInfo.Res.ScalarResources) > 0 {
		(*requested).ScalarResources = map[v1.ResourceName]int64{}
	}
	for rName, rQuant := range podInfo.Res.ScalarResources {
		(*requested).ScalarResources[rName] += sign * rQuant
	}
	(*nonZeroRequested).MilliCPU += sign * podInfo.Non0CPU
	(*nonZeroRequested).Memory += sign * podInfo.Non0Mem

	// Consume ports when pod added or release ports when pod removed.
	n.updateUsedPorts(podInfo.Pod, sign > 0)

	// update non-native resources
	n.NumaTopologyStatus.updateNonNativeResource(podInfo, sign > 0, preempt)
}

// AddPod adds pod information to this NodeInfoImpl.
// If resource type is not set or illegal, the pod is considered as a GuaranteedPod,
// and resource requests are added to guaranteed request
func (n *NodeInfoImpl) AddPod(pod *v1.Pod) {
	n.mu.Lock()
	defer n.mu.Unlock()
	podInfo := NewPodInfo(pod)
	n.PodInfoMaintainer.AddPodInfo(podInfo)
	n.update(podInfo, 1, false)
}

// RemovePod subtracts pod information from this NodeInfoImpl.
// If resource type is not set or illegal, the pod is considered as a GuaranteedPod,
// and resource requests are removed from guaranteed request
func (n *NodeInfoImpl) RemovePod(pod *v1.Pod, preempt bool) error {
	n.mu.Lock()
	defer n.mu.Unlock()
	k, err := GetPodKey(pod)
	if err != nil {
		return err
	}

	if p := n.PodInfoMaintainer.GetPodInfo(k); p != nil {
		n.PodInfoMaintainer.RemovePodInfo(p)
		n.update(p, -1, preempt)
		return nil
	}
	return fmt.Errorf("no corresponding pod %s in pods of node %s", pod.Name, n.getNodeName())
}

// resourceRequest = max(sum(podSpec.Containers), podSpec.InitContainers) + overHead
func CalculateResource(pod *v1.Pod) (res Resource, non0CPU int64, non0Mem int64) {
	resPtr := &res
	for _, c := range pod.Spec.Containers {
		resPtr.Add(c.Resources.Requests)
		non0CPUReq, non0MemReq := godelutil.GetNonzeroRequests(&c.Resources.Requests)
		non0CPU += non0CPUReq
		non0Mem += non0MemReq
		// No non-zero resources for GPUs or opaque resources.
	}
	for _, ic := range pod.Spec.InitContainers {
		resPtr.SetMaxResource(ic.Resources.Requests)
		non0CPUReq, non0MemReq := godelutil.GetNonzeroRequests(&ic.Resources.Requests)
		if non0CPU < non0CPUReq {
			non0CPU = non0CPUReq
		}

		if non0Mem < non0MemReq {
			non0Mem = non0MemReq
		}
	}

	// If Overhead is being utilized, add to the total requests for the pod
	if pod.Spec.Overhead != nil && utilfeature.DefaultFeatureGate.Enabled(features.PodOverhead) {
		resPtr.Add(pod.Spec.Overhead)
		if _, found := pod.Spec.Overhead[v1.ResourceCPU]; found {
			non0CPU += pod.Spec.Overhead.Cpu().MilliValue()
		}

		if _, found := pod.Spec.Overhead[v1.ResourceMemory]; found {
			non0Mem += pod.Spec.Overhead.Memory().Value()
		}
	}
	return res, non0CPU, non0Mem
}

// updateUsedPorts updates the UsedPorts of NodeInfoImpl.
func (n *NodeInfoImpl) updateUsedPorts(pod *v1.Pod, add bool) {
	for j := range pod.Spec.Containers {
		container := &pod.Spec.Containers[j]
		for k := range container.Ports {
			podPort := &container.Ports[k]
			if add {
				n.UsedPorts.Add(podPort.HostIP, string(podPort.Protocol), podPort.HostPort)
			} else {
				n.UsedPorts.Remove(podPort.HostIP, string(podPort.Protocol), podPort.HostPort)
			}
		}
	}
}

// SetNode sets the overall node information collected by k8s.
func (n *NodeInfoImpl) SetNode(node *v1.Node) error {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.Node = node
	n.setGuaranteedAllocatableResource()
	n.setGuaranteedCapacityResource()
	n.TransientInfo = NewTransientSchedulerInfo()
	return nil
}

// SetNodePartition sets whether the node is in partition of scheduler
func (n *NodeInfoImpl) SetNodePartition(inSchedulerPartition bool) error {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.NodeInSchedulerPartition = inSchedulerPartition
	n.NMNodeInSchedulerPartition = inSchedulerPartition
	return nil
}

// SetNMNode sets the overall node information collected by node manager.
func (n *NodeInfoImpl) SetNMNode(nmNode *nodev1alpha1.NMNode) error {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.NMNode = nmNode
	n.setGuaranteedAllocatableResource()
	n.setGuaranteedCapacityResource()
	return nil
}

func (n *NodeInfoImpl) GetPrioritiesForPodsMayBePreempted(resourceType podutil.PodResourceType) []int64 {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.PodInfoMaintainer.GetPrioritiesForPodsMayBePreempted(resourceType)
}

func parseNodeResourceProperties(cnr *katalystv1alpha1.CustomNodeResource) (map[string]sets.String, map[string]*resource.Quantity) {
	discrete := make(map[string]sets.String)
	numeric := make(map[string]*resource.Quantity)
	for _, property := range cnr.Spec.NodeResourceProperties {
		if len(property.PropertyValues) > 0 {
			if discrete[property.PropertyName] == nil {
				discrete[property.PropertyName] = sets.NewString()
			}
			discrete[property.PropertyName].Insert(property.PropertyValues...)
		}
		if property.PropertyQuantity != nil {
			numeric[property.PropertyName] = property.PropertyQuantity
		}
	}
	return discrete, numeric
}

// SetCNR sets extend information about node, such as YARN and numa topology, etc.
func (n *NodeInfoImpl) SetCNR(cnr *katalystv1alpha1.CustomNodeResource) error {
	n.mu.Lock()
	defer n.mu.Unlock()

	n.CNR = cnr
	n.NumaTopologyStatus.parseNumaTopologyStatus(cnr, n.PodInfoMaintainer)
	n.BestEffortAllocatable = NewResourceFromPtr(cnr.Status.Resources.Allocatable)
	return nil
}

// Notice that the return value cannot be modified in the place where the function is called
func (n *NodeInfoImpl) GetNumaTopologyStatus() *NumaTopologyStatus {
	n.mu.RLock()
	defer n.mu.RUnlock()

	return n.NumaTopologyStatus
}

func (n *NodeInfoImpl) GetResourcesAvailableForSharedCoresPods(unavailableNumaList []int) *Resource {
	n.mu.RLock()
	defer n.mu.RUnlock()

	return n.NumaTopologyStatus.GetResourcesAvailableForSharedCoresPods(unavailableNumaList)
}

func (n *NodeInfoImpl) GetResourcesRequestsOfSharedCoresPods() *Resource {
	n.mu.RLock()
	defer n.mu.RUnlock()

	return n.NumaTopologyStatus.GetResourcesRequestsOfSharedCoresPods()
}

func newNumaTopologyStatus(requestsOfSharedCores *Resource) *NumaTopologyStatus {
	return &NumaTopologyStatus{
		podAllocations:         make(map[string]*PodAllocation),
		topology:               make(map[int]*NumaStatus),
		requestsOfSharedCores:  requestsOfSharedCores,
		availableOfSharedCores: NewResource(nil),

		socketToFreeNumasOfConflictResources: map[int]sets.Int{},
	}
}

func (n *NumaTopologyStatus) clone() *NumaTopologyStatus {
	if n == nil {
		return nil
	}
	clone := newNumaTopologyStatus(n.requestsOfSharedCores.Clone())
	clone.availableOfSharedCores = n.availableOfSharedCores.Clone()
	for pod, podAllocation := range n.podAllocations {
		if podAllocation == nil {
			clone.podAllocations[pod] = nil
			continue
		}
		if clone.podAllocations[pod] == nil {
			clone.podAllocations[pod] = &PodAllocation{
				numaAllocations: make(map[int]*v1.ResourceList),
			}
		}
		clone.podAllocations[pod].agent = podAllocation.agent
		for numaId, resourceList := range podAllocation.numaAllocations {
			resourceListClone := resourceList.DeepCopy()
			clone.podAllocations[pod].numaAllocations[numaId] = &resourceListClone
		}
	}
	for numa, topology := range n.topology {
		clone.topology[numa] = topology.clone()
	}
	for socket, numaSet := range n.socketToFreeNumasOfConflictResources {
		if clone.socketToFreeNumasOfConflictResources[socket] == nil {
			clone.socketToFreeNumasOfConflictResources[socket] = sets.NewInt()
		}
		clone.socketToFreeNumasOfConflictResources[socket].Insert(numaSet.UnsortedList()...)
	}
	return clone
}

func (n *NumaTopologyStatus) updateNonNativeResource(podInfo *PodInfo, isAdd, preempt bool) {
	if n == nil {
		return
	}

	pod := podInfo.Pod
	if isAdd {
		n.AddPod(podInfo)
	} else {
		podKey := podutil.GeneratePodKey(pod)
		allocation := n.podAllocations[podKey]

		var notChangedNumaTopology bool = true
		if allocation != nil {
			notChangedNumaTopology = allocation.agent
		}

		// if preempt, change Available & Users, reserve Allocations
		// else if not agent, change Available & Users, remove Allocations
		// else if agent, don't change Available & Users, reserve Allocations
		n.RemovePod(podInfo, preempt, notChangedNumaTopology)
	}
}

func (n *NumaTopologyStatus) removeCNR() {
	if n == nil {
		return
	}
	numaTopologyStatus := newNumaTopologyStatus(n.requestsOfSharedCores)
	*n = *numaTopologyStatus
}

func (n *NumaTopologyStatus) parseNumaTopologyStatus(cnr *katalystv1alpha1.CustomNodeResource, pods *PodInfoMaintainer) {
	if n == nil {
		return
	}

	numaTopologyStatus := newNumaTopologyStatus(n.requestsOfSharedCores)
	numaTopologyStatus.podAllocations = make(map[string]*PodAllocation)

	for _, topoZone := range cnr.Status.TopologyZone {
		if topoZone == nil {
			continue
		}
		if topoZone.Type != katalystv1alpha1.TopologyTypeSocket {
			continue
		}
		socketIDStr := topoZone.Name
		socketID, _ := strconv.Atoi(socketIDStr)
		for _, topoChildren := range topoZone.Children {
			if topoChildren.Type != katalystv1alpha1.TopologyTypeNuma {
				continue
			}
			numaIDStr := topoChildren.Name
			numaID, _ := strconv.Atoi(numaIDStr)
			topology := &NumaStatus{
				socketId:         socketID,
				resourceStatuses: make(map[string]*ResourceStatus),
			}
			if topoChildren.Resources.Allocatable != nil {
				for resourceName, resourceAllocatable := range *topoChildren.Resources.Allocatable {
					resourceAllocatableClone := resourceAllocatable.DeepCopy()
					resourceAvailableClone := resourceAllocatable.DeepCopy()
					if topology.resourceStatuses[resourceName.String()] == nil {
						topology.resourceStatuses[resourceName.String()] = newResourceStatus()
					}
					resourceStatus := topology.resourceStatuses[resourceName.String()]
					resourceStatus.Allocatable = &resourceAllocatableClone
					resourceStatus.Available = &resourceAvailableClone
					numaTopologyStatus.updateAvailableOfSharedCores(true, true, resourceName, resourceStatus.Allocatable)
				}
				numaTopologyStatus.updateFreeNumaForConflictResources(true, numaID, socketID)
			}
			for _, allocation := range topoChildren.Allocations {
				podKey := allocation.Consumer
				podRequests := allocation.Requests
				if podRequests == nil {
					continue
				}
				if numaTopologyStatus.podAllocations[podKey] == nil {
					numaTopologyStatus.podAllocations[podKey] = &PodAllocation{}
				}
				numaTopologyStatus.podAllocations[podKey].agent = true
				if numaTopologyStatus.podAllocations[podKey].numaAllocations == nil {
					numaTopologyStatus.podAllocations[podKey].numaAllocations = make(map[int]*v1.ResourceList)
				}
				if numaTopologyStatus.podAllocations[podKey].numaAllocations[numaID] == nil {
					numaTopologyStatus.podAllocations[podKey].numaAllocations[numaID] = &v1.ResourceList{}
				}
				for resourceName, resourceSize := range *podRequests {
					if resourceSize.MilliValue() == 0 {
						continue
					}
					resourceSizeClone := resourceSize.DeepCopy()
					if topology.resourceStatuses[resourceName.String()] == nil {
						topology.resourceStatuses[resourceName.String()] = newResourceStatus()
					}
					resourceStatus := topology.resourceStatuses[resourceName.String()]
					if resourceStatus.Available == nil {
						resourceStatus.Available = &resource.Quantity{}
					}
					(*numaTopologyStatus.podAllocations[podKey].numaAllocations[numaID])[resourceName] = resourceSizeClone
				}
			}
			numaTopologyStatus.topology[numaID] = topology
		}
	}

	for podKey := range n.podAllocations {
		if _, ok := numaTopologyStatus.podAllocations[podKey]; ok {
			continue
		}
		_, _, uid, err := podutil.ParsePodKey(podKey)
		if err != nil {
			klog.InfoS("Failed to parse pod key", "podKey", podKey, "err", err)
			continue
		}
		podInfo := pods.GetPodInfo(string(uid))
		if podInfo == nil {
			continue
		}
		microTopologyVal, ok := podInfo.Pod.GetAnnotations()[podutil.MicroTopologyKey]
		if !ok {
			continue
		}
		numaAllocation, err := godelutil.UnmarshalMicroTopology(microTopologyVal)
		if err != nil {
			klog.InfoS("Failed to parse micro topology from annotation", "microTopologyAnnValue", microTopologyVal, "err", err)
			continue
		}
		if len(numaAllocation) == 0 {
			continue
		}
		numaTopologyStatus.podAllocations[podKey] = &PodAllocation{
			agent:           false,
			numaAllocations: numaAllocation,
		}
	}
	for podKey, podAllocation := range numaTopologyStatus.podAllocations {
		if podAllocation == nil {
			continue
		}
		for numaId, numaAllocation := range podAllocation.numaAllocations {
			var isFree bool = true
			numaStatus := numaTopologyStatus.topology[numaId]
			for resourceName, resourceStatus := range numaStatus.resourceStatuses {
				podRequest := (*numaAllocation)[v1.ResourceName(resourceName)]
				if podRequest.MilliValue() == 0 {
					continue
				}
				resourceStatus.Available.Sub(podRequest)
				isEmpty := resourceStatus.Users.Len() == 0
				resourceStatus.Users.Insert(podKey)
				numaTopologyStatus.updateAvailableOfSharedCores(false, isEmpty, v1.ResourceName(resourceName), resourceStatus.Allocatable)
				if IsConflictResourcesForDifferentQoS(v1.ResourceName(resourceName)) {
					isFree = false
				}
			}
			if !isFree {
				numaTopologyStatus.updateFreeNumaForConflictResources(false, numaId, numaStatus.socketId)
			}
		}
	}

	*n = *numaTopologyStatus
}

func (n *NumaTopologyStatus) GetNumaNum() int {
	return len(n.topology)
}

func (n *NumaTopologyStatus) GetNumaList() []int {
	var numaList []int
	for numaID := range n.topology {
		numaList = append(numaList, numaID)
	}
	return numaList
}

func (n *NumaTopologyStatus) GetSocketList() []int {
	sockets := sets.NewInt()
	for _, numaStatus := range n.topology {
		sockets.Insert(numaStatus.socketId)
	}
	return sockets.List()
}

func (n *NumaTopologyStatus) GetFreeNumasInSocketForConflictResources(socket int) sets.Int {
	return n.socketToFreeNumasOfConflictResources[socket]
}

func (n *NumaTopologyStatus) GetNumaTopology() map[int]*NumaStatus {
	return n.topology
}

func (n *NumaTopologyStatus) GetFreeResourcesInNuma(numa int) v1.ResourceList {
	availableResources := v1.ResourceList{}
	numaStatus := n.topology[numa]
	for resourceName, resourceStatus := range numaStatus.resourceStatuses {
		available := resourceStatus.Available.DeepCopy()
		availableResources[v1.ResourceName(resourceName)] = available
	}
	return availableResources
}

func (n *NumaTopologyStatus) GetFreeResourcesInNumaList(numas []int) v1.ResourceList {
	availableResources := v1.ResourceList{}
	for _, numa := range numas {
		numaStatus := n.topology[numa]
		for resourceName, resourceStatus := range numaStatus.resourceStatuses {
			available := availableResources[v1.ResourceName(resourceName)]
			available.Add(*resourceStatus.Available)
			availableResources[v1.ResourceName(resourceName)] = available
		}
	}
	return availableResources
}

func (n *NumaTopologyStatus) IsNumaEmptyForConflictResources(numa int) bool {
	numaStatus := n.topology[numa]
	if numaStatus == nil {
		return true
	}
	return numaStatus.IsEmptyForConflictResources()
}

func (n *NumaTopologyStatus) AddPod(podInfo *PodInfo) {
	if podInfo.IsSharedCores && podInfo.PodResourceType == podutil.GuaranteedPod {
		n.requestsOfSharedCores.AddResource(&podInfo.Res)
	}

	var agent bool
	pod := podInfo.Pod
	podKey := podutil.GeneratePodKey(pod)
	if n.podAllocations[podKey] != nil {
		agent = n.podAllocations[podKey].agent
	}
	// update podAllocations according to pod annotation
	if microTopologyVal, ok := pod.GetAnnotations()[podutil.MicroTopologyKey]; ok && !agent {
		numaAllocation, err := godelutil.UnmarshalMicroTopology(microTopologyVal)
		if err != nil {
			klog.InfoS("Failed to unmarshal micro topology for pod", "podKey", podKey, "microTopologyAnnValue", microTopologyVal, "err", err)
			return
		}
		if len(numaAllocation) > 0 {
			n.podAllocations[podKey] = &PodAllocation{
				numaAllocations: numaAllocation,
			}
		}
	}
	// add pod to available
	if n.podAllocations[podKey] == nil {
		return
	}
	for numaId, resourceList := range n.podAllocations[podKey].numaAllocations {
		numaStatus := n.topology[numaId]
		if numaStatus == nil {
			continue
		}
		var isFree bool = true
		for resourceName, resourceQuan := range *resourceList {
			resourceStatus := numaStatus.resourceStatuses[string(resourceName)]
			if resourceStatus.Users.Has(podKey) {
				continue
			}
			resourceStatus.Available.Sub(resourceQuan)
			isEmpty := resourceStatus.Users.Len() == 0
			resourceStatus.Users.Insert(podKey)
			n.updateAvailableOfSharedCores(false, isEmpty, resourceName, resourceStatus.Allocatable)
			if IsConflictResourcesForDifferentQoS(resourceName) {
				isFree = false
			}
		}
		if !isFree {
			n.updateFreeNumaForConflictResources(false, numaId, numaStatus.socketId)
		}
	}
}

func (n *NumaTopologyStatus) updateFreeNumaForConflictResources(isAdd bool, numaId, socketId int) {
	if isAdd {
		if n.socketToFreeNumasOfConflictResources[socketId] == nil {
			n.socketToFreeNumasOfConflictResources[socketId] = sets.NewInt()
		}
		n.socketToFreeNumasOfConflictResources[socketId].Insert(numaId)
	} else {
		n.socketToFreeNumasOfConflictResources[socketId].Delete(numaId)
		if n.socketToFreeNumasOfConflictResources[socketId].Len() == 0 {
			delete(n.socketToFreeNumasOfConflictResources, socketId)
		}
	}
}

func (n *NumaTopologyStatus) updateAvailableOfSharedCores(isAdd, needUpdate bool, resourceName v1.ResourceName, resourceVal *resource.Quantity) {
	if needUpdate && IsConflictResourcesForDifferentQoS(resourceName) && resourceVal != nil {
		if isAdd {
			n.availableOfSharedCores.Add(v1.ResourceList{resourceName: *resourceVal})
		} else {
			n.availableOfSharedCores.Sub(v1.ResourceList{resourceName: *resourceVal})
		}
	}
}

func (n *NumaTopologyStatus) RemovePod(podInfo *PodInfo, preempt, notChangedNumaTopology bool) {
	if podInfo.IsSharedCores && podInfo.PodResourceType == podutil.GuaranteedPod {
		n.requestsOfSharedCores.SubResource(&podInfo.Res)
	}
	if !preempt && notChangedNumaTopology {
		return
	}

	pod := podInfo.Pod
	podKey := podutil.GeneratePodKey(pod)
	if n.podAllocations[podKey] == nil {
		return
	}
	for numaId, resourceList := range n.podAllocations[podKey].numaAllocations {
		numaStatus := n.topology[numaId]
		if numaStatus == nil {
			continue
		}
		var isFree bool = true
		for resourceName, resourceQuan := range *resourceList {
			resourceStatus := numaStatus.resourceStatuses[string(resourceName)]
			if !resourceStatus.Users.Has(podKey) {
				continue
			}
			resourceStatus.Available.Add(resourceQuan)
			resourceStatus.Users.Delete(podKey)
			isEmpty := resourceStatus.Users.Len() == 0
			n.updateAvailableOfSharedCores(true, isEmpty, resourceName, resourceStatus.Allocatable)
			if IsConflictResourcesForDifferentQoS(resourceName) && !isEmpty {
				isFree = false
			}
		}
		if isFree {
			for resourceName, resourceStatus := range numaStatus.resourceStatuses {
				if IsConflictResourcesForDifferentQoS(v1.ResourceName(resourceName)) && resourceStatus.Users.Len() > 0 {
					isFree = false
				}
			}
			if isFree {
				n.updateFreeNumaForConflictResources(true, numaId, numaStatus.socketId)
			}
		}
	}
	if !preempt && !notChangedNumaTopology {
		delete(n.podAllocations, podKey)
	}
}

func (n *NumaTopologyStatus) GetResourcesAvailableForSharedCoresPods(unavailableNumaList []int) *Resource {
	if len(unavailableNumaList) == 0 {
		return n.availableOfSharedCores
	}
	resourcesAvailable := n.availableOfSharedCores.Clone()
	for _, numaId := range unavailableNumaList {
		numaStatus := n.topology[numaId]
		// No need to compare non-conflict resources for shared cores
		// Just make sure the total amount is enough
		for resourceName, resourceStatus := range numaStatus.resourceStatuses {
			if !IsConflictResourcesForDifferentQoS(v1.ResourceName(resourceName)) {
				continue
			}
			if resourceStatus != nil && resourceStatus.Users.Len() == 0 && resourceStatus.Allocatable != nil {
				resourcesAvailable.Sub(v1.ResourceList{v1.ResourceName(resourceName): *resourceStatus.Allocatable})
			}
		}
	}
	return resourcesAvailable
}

func (n *NumaTopologyStatus) GetResourcesRequestsOfSharedCoresPods() *Resource {
	return n.requestsOfSharedCores
}

func (n *NumaTopologyStatus) HasResourceInTopology(rName v1.ResourceName) bool {
	for _, numaStatus := range n.topology {
		if _, ok := numaStatus.resourceStatuses[rName.String()]; ok {
			return true
		}
		return false
	}
	return false
}

func (alloc *PodAllocation) Equal(alloc2 *PodAllocation) bool {
	if alloc == nil {
		alloc = &PodAllocation{}
	}
	if alloc2 == nil {
		alloc2 = &PodAllocation{}
	}
	if alloc.agent != alloc2.agent {
		return false
	}
	if alloc.numaAllocations == nil {
		alloc.numaAllocations = make(map[int]*v1.ResourceList)
	}
	if alloc2.numaAllocations == nil {
		alloc2.numaAllocations = make(map[int]*v1.ResourceList)
	}
	if len(alloc.numaAllocations) != len(alloc2.numaAllocations) {
		return false
	}
	for numaId, resourceList := range alloc.numaAllocations {
		if resourceList == nil {
			resourceList = &v1.ResourceList{}
		}
		if alloc2.numaAllocations[numaId] == nil {
			alloc2.numaAllocations[numaId] = &v1.ResourceList{}
		}
		if !resourceListEqual(*resourceList, *alloc2.numaAllocations[numaId]) {
			return false
		}
	}
	return true
}

func resourceListEqual(list1, list2 v1.ResourceList) bool {
	if len(list1) != len(list2) {
		return false
	}
	for resourceName, resourceQuan := range list1 {
		if !resourceQuan.Equal(list2[resourceName]) {
			return false
		}
	}
	return true
}

func (n *NumaStatus) clone() *NumaStatus {
	if n == nil {
		return nil
	}
	clone := &NumaStatus{
		socketId:         n.socketId,
		resourceStatuses: make(map[string]*ResourceStatus),
	}

	for resourceName, resourceStatus := range n.resourceStatuses {
		resourceStatusClone := resourceStatus.clone()
		clone.resourceStatuses[resourceName] = resourceStatusClone
	}

	return clone
}

// GetNumaInfo return the numa's information in string's format
func (n *NumaStatus) GetNumaInfo() string {
	numaInfo := fmt.Sprintf("NumaTopologyInfo: \nSocketId: %v\n", n.socketId)
	numaInfo += "Resource Status: \n"
	for resourceName, resourceStatus := range n.resourceStatuses {
		numaInfo += fmt.Sprintf("ResourceName: %v\nresourceStatus:\n%+v\n", resourceName, resourceStatus)
	}
	return numaInfo
}

func (n *NumaStatus) IsEmptyForConflictResources() bool {
	for resourceName, resourceStatus := range n.resourceStatuses {
		if resourceStatus.Users.Len() == 0 {
			continue
		}
		if !IsConflictResourcesForDifferentQoS(v1.ResourceName(resourceName)) {
			continue
		}
		return false
	}
	return true
}

func (n *NumaStatus) Equal(n2 *NumaStatus) bool {
	if n == nil {
		n = &NumaStatus{}
	}
	if n2 == nil {
		n2 = &NumaStatus{}
	}
	if n.socketId != n2.socketId {
		return false
	}
	if len(n.resourceStatuses) != len(n2.resourceStatuses) {
		return false
	}
	for resourceName, resourceStatus := range n.resourceStatuses {
		if !resourceStatus.Equal(n2.resourceStatuses[resourceName]) {
			return false
		}
	}
	return true
}

func newResourceStatus() *ResourceStatus {
	return &ResourceStatus{
		Allocatable: &resource.Quantity{},
		Available:   &resource.Quantity{},
		Users:       sets.NewString(),
	}
}

func (s *ResourceStatus) clone() *ResourceStatus {
	allocatable := s.Allocatable.DeepCopy()
	available := s.Available.DeepCopy()
	clone := &ResourceStatus{
		Allocatable: &allocatable,
		Available:   &available,
		Users:       sets.NewString(s.Users.UnsortedList()...),
	}
	return clone
}

func (s *ResourceStatus) Equal(s2 *ResourceStatus) bool {
	if s == nil {
		s = &ResourceStatus{}
	}
	if s2 == nil {
		s2 = &ResourceStatus{}
	}
	if !QuantityEqual(s.Allocatable, s2.Allocatable) {
		return false
	}
	if !QuantityEqual(s.Available, s2.Available) {
		return false
	}
	if !s.Users.Equal(s2.Users) {
		return false
	}

	return true
}

func QuantityEqual(r1, r2 *resource.Quantity) bool {
	if r1 == nil {
		r1 = &resource.Quantity{}
	}
	if r2 == nil {
		r2 = &resource.Quantity{}
	}
	if r1.MilliValue() != r2.MilliValue() {
		return false
	}
	return true
}

// setGuaranteedAllocatableResource sets guaranteed allocatable resource about node, based on Node and NMNode.
func (n *NodeInfoImpl) setGuaranteedAllocatableResource() {
	switch {
	case n.Node == nil && n.NMNode == nil:
		n.GuaranteedAllocatable = &Resource{}
	case n.Node != nil && n.NMNode == nil:
		n.GuaranteedAllocatable = NewResource(n.Node.Status.Allocatable)
	case n.Node == nil && n.NMNode != nil:
		n.GuaranteedAllocatable = NewResourceFromPtr(n.NMNode.Status.ResourceAllocatable)
	case n.Node != nil && n.NMNode != nil:
		n.GuaranteedAllocatable = getAllocatableResources(n.Node, n.NMNode)
	}
}

// setGuaranteedCapacityResource sets guaranteed capacity resource about node, based on Node and NMNode.
func (n *NodeInfoImpl) setGuaranteedCapacityResource() {
	switch {
	case n.Node == nil && n.NMNode == nil:
		n.GuaranteedCapacity = &Resource{}
	case n.Node != nil && n.NMNode == nil:
		n.GuaranteedCapacity = NewResource(n.Node.Status.Capacity)
	case n.Node == nil && n.NMNode != nil:
		n.GuaranteedCapacity = NewResourceFromPtr(n.NMNode.Status.ResourceCapacity)
	case n.Node != nil && n.NMNode != nil:
		n.GuaranteedCapacity = NewResource(n.Node.Status.Capacity)
	}
}

// getAllocatableResources returns the allocatable resources from node and nmnode.
// If allocatable resources exist in both node and nmnode, use node.Allocatable -(nmnode.Capacity-nmnode.Allocatable)
// If
func getAllocatableResources(node *v1.Node, nmNode *nodev1alpha1.NMNode) *Resource {
	resourceList := node.Status.Allocatable.DeepCopy()
	if resourceList == nil {
		resourceList = make(v1.ResourceList)
	}
	nmNodeCapacity := nmNode.Status.ResourceCapacity
	if nmNodeCapacity == nil {
		nmNodeCapacity = &v1.ResourceList{}
	}
	nmNodeAllocatable := nmNode.Status.ResourceAllocatable
	if nmNodeAllocatable == nil {
		nmNodeAllocatable = &v1.ResourceList{}
	}
	for rName, rQuantity := range *nmNodeAllocatable {
		if quantity, ok := resourceList[rName]; ok {
			if rQuant, ok := (*nmNodeCapacity)[rName]; ok {
				rQuant.Sub(rQuantity)
				if rQuant.Sign() < 0 {
					rQuant.Set(0)
				}
				quantity.Sub(rQuant)
				if quantity.Sign() < 0 {
					quantity.Set(0)
				}
				resourceList[rName] = quantity
			}
			continue
		}
		resourceList[rName] = rQuantity
	}
	return NewResourceFromPtr(&resourceList)
}

// RemoveNode removes the node object, leaving all other tracking information.
func (n *NodeInfoImpl) RemoveNode() {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.Node = nil
	n.setGuaranteedAllocatableResource()
	n.setGuaranteedCapacityResource()
}

func (n *NodeInfoImpl) RemoveNMNode() {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.NMNode = nil
	n.setGuaranteedAllocatableResource()
	n.setGuaranteedCapacityResource()
}

// RemoveCNR removes the CNR object, leaving all other tracking information.
func (n *NodeInfoImpl) RemoveCNR() {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.CNR = nil
	n.BestEffortAllocatable = &Resource{}
	n.NumaTopologyStatus.removeCNR()
}

// GetPodKey returns the string key of a pod.
func GetPodKey(pod *v1.Pod) (string, error) {
	uid := string(pod.UID)
	if len(uid) == 0 {
		return "", errors.New("cannot get cache key for pod with empty UID")
	}
	return uid, nil
}

// GetPodGroupKey returns the string key of a podGroup.
func GetPodGroupKey(podGroup *schedulingv1a1.PodGroup) (string, error) {
	uid := string(podGroup.UID)
	if len(uid) == 0 {
		return "", errors.New("cannot get cache key for PodGroup with empty UID")
	}
	return uid, nil
}
