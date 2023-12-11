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
	"fmt"

	"github.com/google/go-cmp/cmp"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/kubewharf/godel-scheduler/pkg/util"
	podutil "github.com/kubewharf/godel-scheduler/pkg/util/pod"
	"github.com/kubewharf/godel-scheduler/pkg/util/splay"
)

// PodPartitionInfo records a pod Priority and a PodResourceType, which we use to partition all ordered pods in Splay-Tree.
// More precisely, we partition all pods for which `p.Compare` returns false into a subtree.
// For now, we perform the `p.Compare` operation based on Priority.
// This will be used when we store PodInfo in splay-tree.
type PodPartitionInfo struct {
	// Considering the queue-related preemption, we need to make a temporary change to the priority of the preemptor.
	// To handle the case that the priority is math.MaxInt32, we use int64 to store the pod's priority directly.
	priorityUpper int64
	priorityLower int64
	resourceType  podutil.PodResourceType
}

func NewPartitionInfo(priorityLower, priorityUpper int64, resourceType podutil.PodResourceType) *PodPartitionInfo {
	return &PodPartitionInfo{
		priorityLower: priorityLower,
		priorityUpper: priorityUpper,
		resourceType:  resourceType,
	}
}

func (p *PodPartitionInfo) Compare(o splay.Comparable) bool {
	return p.priorityUpper > o.(*PodInfo).PodPriority
}

var _ splay.Comparable = &PodPartitionInfo{}

// PodMaintainableInfo implements splay.MaintainInfo.
// It maintains the subtree information rooted at the current node from the bottom up
// based on the left and right subtree information.
// Currently, the information maintained is the sum of all MilliCPU/Memory requests in a subtree.
// This will be used when we store PodInfo in splay-tree.
type PodMaintainableInfo struct {
	PodInfo  *PodInfo
	MilliCPU int64
	Memory   int64
}

var _ splay.MaintainInfo = PodMaintainableInfo{}

func (o PodMaintainableInfo) Maintain(l splay.MaintainInfo, r splay.MaintainInfo) splay.MaintainInfo {
	o.MilliCPU = o.PodInfo.Res.MilliCPU
	o.Memory = o.PodInfo.Res.Memory
	if l != nil {
		o.MilliCPU += l.(PodMaintainableInfo).MilliCPU
		o.Memory += l.(PodMaintainableInfo).Memory
	}
	if r != nil {
		o.MilliCPU += r.(PodMaintainableInfo).MilliCPU
		o.Memory += r.(PodMaintainableInfo).Memory
	}
	return o
}

// PodInfo is a wrapper to a Pod with additional pre-computed information to
// accelerate processing. This information is typically immutable (e.g., pre-processed
// inter-pod affinity selectors).
// This will be stored in splay-tree.
type PodInfo struct {
	Pod                        *v1.Pod
	RequiredAffinityTerms      []AffinityTerm
	RequiredAntiAffinityTerms  []AffinityTerm
	PreferredAffinityTerms     []WeightedAffinityTerm
	PreferredAntiAffinityTerms []WeightedAffinityTerm
	ParseError                 error

	Res              Resource
	Non0CPU, Non0Mem int64

	PodResourceType      podutil.PodResourceType
	PodResourceTypeError error

	PodKey        string
	PodPriority   int64
	IsSharedCores bool

	PodGroupName string
	PodLauncher  podutil.PodLauncher

	PodPreemptionInfo PodPreemptionInfo
}

type PodPreemptionInfo struct {
	CanBePreempted int

	StartTime               *metav1.Time
	ProtectionDuration      int64
	ProtectionDurationExist bool

	Labels    string
	OwnerType string
	OwnerKey  string
}

var (
	_ splay.Comparable = &PodInfo{}
	_ splay.StoredObj  = &PodInfo{}
)

// NewPodInfo return a new PodInfo
func NewPodInfo(pod *v1.Pod) *PodInfo {
	var preferredAffinityTerms []v1.WeightedPodAffinityTerm
	var preferredAntiAffinityTerms []v1.WeightedPodAffinityTerm
	if affinity := pod.Spec.Affinity; affinity != nil {
		if a := affinity.PodAffinity; a != nil {
			preferredAffinityTerms = a.PreferredDuringSchedulingIgnoredDuringExecution
		}
		if a := affinity.PodAntiAffinity; a != nil {
			preferredAntiAffinityTerms = a.PreferredDuringSchedulingIgnoredDuringExecution
		}
	}

	// Attempt to parse the affinity terms
	var parseErr error
	requiredAffinityTerms, err := getAffinityTerms(pod, util.GetPodAffinityTerms(pod.Spec.Affinity))
	if err != nil {
		parseErr = fmt.Errorf("requiredAffinityTerms: %w", err)
	}
	requiredAntiAffinityTerms, err := getAffinityTerms(pod, util.GetPodRequiredAntiAffinityTerms(pod.Spec.Affinity))
	if err != nil {
		parseErr = fmt.Errorf("requiredAntiAffinityTerms: %w", err)
	}
	weightedAffinityTerms, err := getWeightedAffinityTerms(pod, preferredAffinityTerms)
	if err != nil {
		parseErr = fmt.Errorf("preferredAffinityTerms: %w", err)
	}
	weightedAntiAffinityTerms, err := getWeightedAffinityTerms(pod, preferredAntiAffinityTerms)
	if err != nil {
		parseErr = fmt.Errorf("preferredAntiAffinityTerms: %w", err)
	}

	podResourceType, podResourceTypeError := podutil.GetPodResourceType(pod)

	res, non0CPU, non0Mem := CalculateResource(pod)

	protectionDuration, protectionDurationExist := podutil.GetProtectionDuration(podutil.GeneratePodKey(pod), pod.Annotations)
	ownerType, ownerKey := podutil.GetOwnerInfo(pod)
	podLauncher, _ := podutil.GetPodLauncher(pod)

	return &PodInfo{
		Pod:                        pod,
		RequiredAffinityTerms:      requiredAffinityTerms,
		RequiredAntiAffinityTerms:  requiredAntiAffinityTerms,
		PreferredAffinityTerms:     weightedAffinityTerms,
		PreferredAntiAffinityTerms: weightedAntiAffinityTerms,
		ParseError:                 parseErr,

		Res:     res,
		Non0CPU: non0CPU,
		Non0Mem: non0Mem,

		PodResourceType:      podResourceType,
		PodResourceTypeError: podResourceTypeError,

		IsSharedCores: podutil.IsSharedCores(pod),
		PodKey:        string(pod.UID),
		PodPriority:   int64(podutil.GetPodPriority(pod)),

		PodGroupName: podutil.GetPodGroupName(pod),
		PodLauncher:  podLauncher,

		PodPreemptionInfo: PodPreemptionInfo{
			// use pod.Status.StartTime instead of podutil.GetPodStartTime
			StartTime:               pod.Status.StartTime,
			CanBePreempted:          podutil.CanPodBePreempted(pod),
			ProtectionDuration:      protectionDuration,
			ProtectionDurationExist: protectionDurationExist,
			Labels:                  util.FormatLabels(pod.Labels),
			OwnerType:               ownerType,
			OwnerKey:                ownerKey,
		},
	}
}

func (pi *PodInfo) MakeMaintainInfo() splay.MaintainInfo {
	return PodMaintainableInfo{
		PodInfo:  pi,
		MilliCPU: pi.Res.MilliCPU,
		Memory:   pi.Res.Memory,
	}
}

// GPU, Socket, CPU, MEM
var lessIsImportant = []bool{false, false, true, true}

func (pi *PodInfo) Compare(o splay.Comparable) bool {
	pi2 := o.(*PodInfo)
	if pi.PodPriority != pi2.PodPriority {
		return pi.PodPriority > pi2.PodPriority
	}
	if pi.PodPreemptionInfo.StartTime != nil || pi2.PodPreemptionInfo.StartTime != nil {
		if pi.PodPreemptionInfo.StartTime != nil && pi2.PodPreemptionInfo.StartTime != nil {
			if !pi.PodPreemptionInfo.StartTime.Equal(pi2.PodPreemptionInfo.StartTime) {
				return pi.PodPreemptionInfo.StartTime.Before(pi2.PodPreemptionInfo.StartTime)
			}
		} else {
			return pi.PodPreemptionInfo.StartTime != nil
		}
	}
	return pi.PodKey > o.(*PodInfo).PodKey
}

func (pi *PodInfo) Equal(pi2 *PodInfo) bool {
	return cmp.Equal(pi.Pod, pi2.Pod)
}

func (pi *PodInfo) Key() string {
	return pi.PodKey
}

func (pi *PodInfo) String() string {
	return podutil.GeneratePodKey(pi.Pod)
}

type podFilterFunc func(*PodInfo) bool

type PodInfoMaintainer struct {
	// bePodsMayBePreempted holds all best-effort pods that may be preempted.
	bePodsMayBePreempted splay.Splay
	// key is priority value, value is pods count
	prioritiesForBEPodsMayBePreempted map[int64]int
	// only set when clone
	prioritiesSliceForBEPodsMayBePreempted []int64
	// gtPodsMayBePreempted holds all guaranteed pods that may be preempted.
	gtPodsMayBePreempted splay.Splay
	// key is priority value, value is pods count
	prioritiesForGTPodsMayBePreempted map[int64]int
	// only set when clone
	prioritiesSliceForGTPodsMayBePreempted []int64
	// neverBePreempted holds all the pods that will never be preempted, whether it belongs to BE or GT.
	neverBePreempted map[string]*PodInfo
	// All pods that cannot pass the filter functions will never be preempted.
	podFilters []podFilterFunc

	podsWithAffinity             PodInfoSlice
	podsWithRequiredAntiAffinity PodInfoSlice
}

func NewPodInfoMaintainer(pods ...*PodInfo) *PodInfoMaintainer {
	m := &PodInfoMaintainer{
		bePodsMayBePreempted: splay.NewSplay(),
		gtPodsMayBePreempted: splay.NewSplay(),
		neverBePreempted:     make(map[string]*PodInfo),

		podsWithAffinity:             NewPodInfoSlice(),
		podsWithRequiredAntiAffinity: NewPodInfoSlice(),

		prioritiesForBEPodsMayBePreempted: map[int64]int{},
		prioritiesForGTPodsMayBePreempted: map[int64]int{},
	}
	m.podFilters = []podFilterFunc{
		func(pi *PodInfo) bool {
			return pi.PodResourceTypeError == nil
		},
		func(pi *PodInfo) bool {
			return podutil.BoundPod(pi.Pod)
		},
	}
	for i := range pods {
		m.AddPodInfo(pods[i])
	}
	return m
}

func (m *PodInfoMaintainer) Len() int {
	return m.bePodsMayBePreempted.Len() + m.gtPodsMayBePreempted.Len() + len(m.neverBePreempted)
}

func (m *PodInfoMaintainer) GetPods() []*PodInfo {
	pods := make([]*PodInfo, 0, m.bePodsMayBePreempted.Len()+m.gtPodsMayBePreempted.Len()+len(m.neverBePreempted))
	rangeSplay := func(s splay.Splay) {
		s.Range(func(so splay.StoredObj) {
			pods = append(pods, so.(*PodInfo))
		})
	}
	rangeSplay(m.bePodsMayBePreempted)
	rangeSplay(m.gtPodsMayBePreempted)
	for _, v := range m.neverBePreempted {
		pods = append(pods, v)
	}
	return pods
}

func (m *PodInfoMaintainer) AddPodInfo(p *PodInfo) {
	if podsWithAffinity(p.Pod) {
		m.podsWithAffinity.Add(p)
	}
	if podsWithRequiredAntiAffinity(p.Pod) {
		m.podsWithRequiredAntiAffinity.Add(p)
	}

	for i := range m.podFilters {
		if !m.podFilters[i](p) {
			m.neverBePreempted[p.PodKey] = p
			return
		}
	}
	if p.PodResourceType == podutil.GuaranteedPod {
		m.gtPodsMayBePreempted.Insert(p)
		m.prioritiesForGTPodsMayBePreempted[p.PodPriority]++
	} else {
		m.bePodsMayBePreempted.Insert(p)
		m.prioritiesForBEPodsMayBePreempted[p.PodPriority]++
	}
}

func (m *PodInfoMaintainer) RemovePodInfo(p *PodInfo) {
	if podsWithAffinity(p.Pod) {
		m.podsWithAffinity.Del(p)
	}
	if podsWithRequiredAntiAffinity(p.Pod) {
		m.podsWithRequiredAntiAffinity.Del(p)
	}

	if _, ok := m.neverBePreempted[p.PodKey]; ok {
		delete(m.neverBePreempted, p.PodKey)
		return
	}
	if p.PodResourceType == podutil.GuaranteedPod {
		m.gtPodsMayBePreempted.Delete(p)
		m.prioritiesForGTPodsMayBePreempted[p.PodPriority]--
		if m.prioritiesForGTPodsMayBePreempted[p.PodPriority] <= 0 {
			delete(m.prioritiesForGTPodsMayBePreempted, p.PodPriority)
		}
	} else {
		m.bePodsMayBePreempted.Delete(p)
		m.prioritiesForBEPodsMayBePreempted[p.PodPriority]--
		if m.prioritiesForBEPodsMayBePreempted[p.PodPriority] <= 0 {
			delete(m.prioritiesForBEPodsMayBePreempted, p.PodPriority)
		}
	}
}

func (m *PodInfoMaintainer) GetPodInfo(key string) *PodInfo {
	if p, ok := m.neverBePreempted[key]; ok {
		return p
	}
	obj := splay.NewStoredObjForLookup(key)
	if o := m.gtPodsMayBePreempted.Get(obj); o != nil {
		return o.(*PodInfo)
	}
	if o := m.bePodsMayBePreempted.Get(obj); o != nil {
		return o.(*PodInfo)
	}
	return nil
}

func (m *PodInfoMaintainer) GetVictimCandidates(partitionInfo *PodPartitionInfo) []*PodInfo {
	var s splay.Splay
	if partitionInfo.resourceType == podutil.GuaranteedPod {
		s = m.gtPodsMayBePreempted
	} else {
		s = m.bePodsMayBePreempted
	}
	ret := make([]*PodInfo, 0)
	s.ConditionRange(func(so splay.StoredObj) bool {
		pi := so.(*PodInfo)
		if pi.PodPriority <= partitionInfo.priorityLower {
			return true
		}
		if pi.PodPriority >= partitionInfo.priorityUpper {
			return false
		}
		ret = append(ret, so.(*PodInfo))
		return true
	})
	return ret
}

func (m *PodInfoMaintainer) GetMaintainableInfoByPartition(partitionInfo *PodPartitionInfo) PodMaintainableInfo {
	var s splay.Splay
	if partitionInfo.resourceType == podutil.GuaranteedPod {
		s = m.gtPodsMayBePreempted
	} else {
		s = m.bePodsMayBePreempted
	}
	if o := s.Partition(partitionInfo); o != nil {
		return o.(PodMaintainableInfo)
	}
	return PodMaintainableInfo{}
}

func (m *PodInfoMaintainer) GetPodsWithAffinity() []*PodInfo {
	return m.podsWithAffinity.Pods()
}

func (m *PodInfoMaintainer) GetPodsWithRequiredAntiAffinity() []*PodInfo {
	return m.podsWithRequiredAntiAffinity.Pods()
}

func (m *PodInfoMaintainer) Range(f func(*PodInfo)) {
	rangeSplay := func(s splay.Splay) {
		s.Range(func(so splay.StoredObj) {
			f(so.(*PodInfo))
		})
	}
	rangeSplay(m.bePodsMayBePreempted)
	rangeSplay(m.gtPodsMayBePreempted)
	for _, v := range m.neverBePreempted {
		f(v)
	}
}

func (m *PodInfoMaintainer) Clone() *PodInfoMaintainer {
	clone := NewPodInfoMaintainer()
	clone.bePodsMayBePreempted, clone.gtPodsMayBePreempted = m.bePodsMayBePreempted.Clone(), m.gtPodsMayBePreempted.Clone()

	clone.prioritiesSliceForGTPodsMayBePreempted = make([]int64, len(m.prioritiesForGTPodsMayBePreempted))
	clone.prioritiesForGTPodsMayBePreempted = make(map[int64]int, len(m.prioritiesForGTPodsMayBePreempted))
	clonePrioritiesForPodsMayBePreempted(clone.prioritiesSliceForGTPodsMayBePreempted, clone.prioritiesForGTPodsMayBePreempted, m.prioritiesForGTPodsMayBePreempted)

	clone.prioritiesSliceForBEPodsMayBePreempted = make([]int64, len(m.prioritiesForBEPodsMayBePreempted))
	clone.prioritiesForBEPodsMayBePreempted = make(map[int64]int, len(m.prioritiesForBEPodsMayBePreempted))
	clonePrioritiesForPodsMayBePreempted(clone.prioritiesSliceForBEPodsMayBePreempted, clone.prioritiesForBEPodsMayBePreempted, m.prioritiesForBEPodsMayBePreempted)

	neverBePreempted := make(map[string]*PodInfo, len(m.neverBePreempted))
	for k, v := range m.neverBePreempted {
		neverBePreempted[k] = v
	}
	clone.neverBePreempted = neverBePreempted
	clone.podsWithAffinity = m.podsWithAffinity.Clone()
	clone.podsWithRequiredAntiAffinity = m.podsWithRequiredAntiAffinity.Clone()
	return clone
}

func clonePrioritiesForPodsMayBePreempted(prioritiesSliceForPodsMayBePreempted []int64,
	prioritiesForPodsMayBePreempted map[int64]int,
	existingPriorities map[int64]int,
) {
	index := 0
	for k, v := range existingPriorities {
		prioritiesForPodsMayBePreempted[k] = v
		prioritiesSliceForPodsMayBePreempted[index] = k
		index++
	}
}

func (m *PodInfoMaintainer) Equal(o *PodInfoMaintainer) bool {
	// 1. Check the `bePodsMayBePreempted` and `gtPodsMayBePreempted`.
	{
		getPods := func(s splay.Splay) []*PodInfo {
			pods := make([]*PodInfo, 0, s.Len())
			s.Range(func(so splay.StoredObj) {
				pods = append(pods, so.(*PodInfo))
			})
			return pods
		}
		if !cmp.Equal(getPods(m.bePodsMayBePreempted), getPods(o.bePodsMayBePreempted)) {
			return false
		}
		if !cmp.Equal(getPods(m.gtPodsMayBePreempted), getPods(o.gtPodsMayBePreempted)) {
			return false
		}
	}
	// 2. Check the `neverBePreempted`.
	{
		if len(m.neverBePreempted) != len(o.neverBePreempted) {
			return false
		}
		for k, v1 := range m.neverBePreempted {
			if v2, ok := o.neverBePreempted[k]; !ok || !cmp.Equal(v1, v2) {
				return false
			}
		}
	}
	// 3. Check the `podsWithAffinity` and `podsWithRequiredAntiAffinity`.
	{
		if !m.podsWithAffinity.Equal(o.podsWithAffinity) ||
			!m.podsWithRequiredAntiAffinity.Equal(o.podsWithRequiredAntiAffinity) {
			return false
		}
	}
	// 4. Check the `prioritiesForGTPodsMayBePreempted` and `prioritiesForBEPodsMayBePreempted`
	{
		if !cmp.Equal(m.prioritiesForGTPodsMayBePreempted, o.prioritiesForGTPodsMayBePreempted) ||
			!cmp.Equal(m.prioritiesForBEPodsMayBePreempted, o.prioritiesForBEPodsMayBePreempted) {
			return false
		}
	}
	return true
}

func (m *PodInfoMaintainer) GetPrioritiesForPodsMayBePreempted(resourceType podutil.PodResourceType) []int64 {
	var prioritiesSlice []int64
	switch resourceType {
	case podutil.GuaranteedPod:
		prioritiesSlice = m.prioritiesSliceForGTPodsMayBePreempted
	case podutil.BestEffortPod:
		prioritiesSlice = m.prioritiesSliceForBEPodsMayBePreempted
	}
	prioritiesSliceCopy := make([]int64, len(prioritiesSlice))
	copy(prioritiesSliceCopy, prioritiesSlice)
	return prioritiesSliceCopy
}

// ------------------------------------- Affinity Related -------------------------------------

// PodInfoSlice maintains a linear PodInfo's slice. The time complexity of all methods is O(1).
type PodInfoSlice interface {
	Add(*PodInfo) bool
	Del(*PodInfo) bool
	Pods() []*PodInfo
	Len() int
	Clone() PodInfoSlice
	Equal(PodInfoSlice) bool
}

// PodInfoSliceImpl holds a mapping of string keys to slice index, and slice to PodInfo.
// When adding a PodInfo, we will place it directly at the end of the slice.
// When deleting a PodInfo, we swap it with the last PodInfo and delete the last PodInfo.
type PodInfoSliceImpl struct {
	hash  map[string]int
	items []*PodInfo
	count int
}

var _ PodInfoSlice = &PodInfoSliceImpl{}

func NewPodInfoSlice() PodInfoSlice {
	return &PodInfoSliceImpl{
		hash:  make(map[string]int),
		items: make([]*PodInfo, 0),
		count: 0,
	}
}

func (hs *PodInfoSliceImpl) Add(pi *PodInfo) bool {
	key := pi.PodKey
	if p, ok := hs.hash[key]; !ok {
		hs.items = append(hs.items, pi)
		hs.hash[key] = hs.count
		hs.count++
		return true
	} else {
		hs.items[p] = pi
		return false
	}
}

func (hs *PodInfoSliceImpl) Del(pi *PodInfo) bool {
	key := pi.PodKey
	if px, ok := hs.hash[key]; ok {
		hs.count--
		y := hs.items[hs.count]
		hs.items[px], hs.hash[y.PodKey] = y, px
		delete(hs.hash, key)
		hs.items = hs.items[:hs.count]
		return true
	}
	return false
}

func (hs *PodInfoSliceImpl) Pods() []*PodInfo {
	return hs.items
}

func (hs *PodInfoSliceImpl) Len() int {
	return hs.count
}

func (hs *PodInfoSliceImpl) Clone() PodInfoSlice {
	hash, items := make(map[string]int, hs.count), make([]*PodInfo, hs.count)
	for k, v := range hs.hash {
		hash[k] = v
	}
	copy(items, hs.items)
	return &PodInfoSliceImpl{
		hash:  hash,
		items: items,
		count: hs.count,
	}
}

func (hs *PodInfoSliceImpl) Equal(pis PodInfoSlice) bool {
	o := pis.(*PodInfoSliceImpl)
	if hs.count != o.count {
		return false
	}
	for k, v1 := range hs.hash {
		if v2, ok := o.hash[k]; !ok || !cmp.Equal(hs.items[v1], o.items[v2]) {
			return false
		}
	}
	return true
}

func podsWithAffinity(p *v1.Pod) bool {
	affinity := p.Spec.Affinity
	return affinity != nil && (affinity.PodAffinity != nil || affinity.PodAntiAffinity != nil)
}

func podsWithRequiredAntiAffinity(p *v1.Pod) bool {
	affinity := p.Spec.Affinity
	return affinity != nil && affinity.PodAntiAffinity != nil &&
		len(affinity.PodAntiAffinity.RequiredDuringSchedulingIgnoredDuringExecution) != 0
}
