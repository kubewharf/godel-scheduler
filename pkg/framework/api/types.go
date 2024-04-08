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
	"strings"
	"sync"
	"time"

	"github.com/google/go-cmp/cmp"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/util/sets"
	utilfeature "k8s.io/apiserver/pkg/util/feature"
	"k8s.io/client-go/tools/events"

	binderconfig "github.com/kubewharf/godel-scheduler/pkg/binder/apis/config"
	"github.com/kubewharf/godel-scheduler/pkg/scheduler/apis/config"
	godelutil "github.com/kubewharf/godel-scheduler/pkg/util"
	"github.com/kubewharf/godel-scheduler/pkg/util/features"
	"github.com/kubewharf/godel-scheduler/pkg/util/generationstore"
	"github.com/kubewharf/godel-scheduler/pkg/util/helper"
	podutil "github.com/kubewharf/godel-scheduler/pkg/util/pod"
	"github.com/kubewharf/godel-scheduler/pkg/util/tracing"
)

var (
	// ErrNoNodesAvailable is used to describe the error that no nodes available to schedule pods.
	ErrNoNodesAvailable = fmt.Errorf("no nodes available to schedule pods")
	// ErrNoNodeGroupsAvailable is used to describe the error that no node groups available to schedule units.
	ErrNoNodeGroupsAvailable = fmt.Errorf("no node groups available to schedule units")
	// ErrPreemptFailed is used to describe the error that preempt failed for one pod.
	ErrPreemptFailed = fmt.Errorf("preempt failed")
)

// ActionType is an integer to represent one type of resource change.
// Different ActionTypes can be bit-wised to compose new semantics.
type ActionType int64

// Constants for ActionTypes.
const (
	Add    ActionType = 1 << iota // 1
	Delete                        // 10
	// UpdateNodeXYZ is only applicable for Node events.
	UpdateNodeAllocatable // 100
	UpdateNodeLabel       // 1000
	UpdateNodeTaint       // 10000
	UpdateNodeCondition   // 100000

	All ActionType = 1<<iota - 1 // 111111

	// Use the general Update type if you don't either know or care the specific sub-Update type to use.
	Update = UpdateNodeAllocatable | UpdateNodeLabel | UpdateNodeTaint | UpdateNodeCondition
)

// GVK is short for group/version/kind, which can uniquely represent a particular API resource.
type GVK string

// Constants for GVKs.
const (
	Pod                   GVK = "Pod"
	Node                  GVK = "Node"
	PersistentVolume      GVK = "PersistentVolume"
	PersistentVolumeClaim GVK = "PersistentVolumeClaim"
	Service               GVK = "Service"
	StorageClass          GVK = "storage.k8s.io/StorageClass"
	CSINode               GVK = "storage.k8s.io/CSINode"
	WildCard              GVK = "*"
)

// WildCardEvent semantically matches all resources on all actions.
var WildCardEvent = ClusterEvent{Resource: WildCard, ActionType: All}

// ClusterEvent abstracts how a system resource's state gets changed.
// Resource represents the standard API resources such as Pod, Node, etc.
// ActionType denotes the specific change such as Add, Update or Delete.
type ClusterEvent struct {
	Resource   GVK
	ActionType ActionType
}

// RecorderFactory builds an EventRecorder for a given scheduler name.
type RecorderFactory func(string) events.EventRecorder

// QueuedPodInfo is a Pod wrapper with additional information related to
// the pod's status in the scheduling queue, such as the timestamp when
// it's added to the queue.
type QueuedPodInfo struct {
	Pod *v1.Pod

	// The time pod added to the scheduling queue.
	Timestamp time.Time
	// Number of schedule attempts before successfully scheduled.
	// It's used to record the # attempts metric.
	Attempts int
	// The time when the pod is added to the queue for the first time. The pod may be added
	// back to the queue multiple times before it's successfully scheduled.
	// It shouldn't be updated once initialized. It's used to record the e2e scheduling
	// latency for a pod.
	InitialAttemptTimestamp time.Time
	// The time when the pod is first attempted to preempt victim pods. There can be
	// multiple attempts for this pod to wait on victim pods to be preempted, this
	// timestamp tracks the first time. It is used to timeout if the victim preemption
	// takes more time.
	InitialPreemptAttemptTimestamp time.Time

	// Fields below are only used by binder, need to move this out of QueuedPodInfo and put them into BinderQueuedPodInfo
	// TODO: (liumeng) implement BinderQueuedPodInfo ?
	// assumedPod
	ReservedPod *v1.Pod
	// NominatedNode contains victims and nominated node for the pod
	NominatedNode *NominatedNode

	// NoConflictStillInHandling indicates the pod passed the conflict checking phase
	// NewlyAssumedButStillInHandling indicates the pod passed the conflict checking phase
	// but is still in processing
	// TODO: when this field is moved from QueuedPodInfo to BindQueuedPodInfo, add more fine-grained states to avoid duplicated checking in binder workflow
	// for example: VictimsDeleting, ReadyForBinding ...
	NewlyAssumedButStillInHandling bool
	AllVolumeBound                 bool

	// PodProperty is used by metrics and tracing
	podProperty *PodProperty

	// which queue the object pending in
	queueStage string

	// QueueSpan is used to record queue updates
	QueueSpan *tracing.SpanInfo

	OwnerReferenceKey string // TODO: revisit this.
}

// DeepCopy returns a deep copy of the QueuedPodInfo object.
func (pqi *QueuedPodInfo) DeepCopy() *QueuedPodInfo {
	return &QueuedPodInfo{
		Pod:                            pqi.Pod.DeepCopy(),
		Timestamp:                      pqi.Timestamp,
		Attempts:                       pqi.Attempts,
		InitialAttemptTimestamp:        pqi.InitialAttemptTimestamp,
		InitialPreemptAttemptTimestamp: pqi.InitialPreemptAttemptTimestamp,
		NominatedNode:                  pqi.NominatedNode.DeepCopy(),
		podProperty:                    pqi.podProperty,
		NewlyAssumedButStillInHandling: pqi.NewlyAssumedButStillInHandling,
		AllVolumeBound:                 pqi.AllVolumeBound,
		OwnerReferenceKey:              pqi.OwnerReferenceKey,
	}
}

func (pqi *QueuedPodInfo) UpdateQueueStage(queueStage string) {
	if queueStage == pqi.queueStage {
		return
	}

	pqi.queueStage = queueStage
	if pqi.QueueSpan == nil {
		return
	}
	pqi.QueueSpan.WithFields(tracing.WithQueueField(queueStage))
}

func (pqi *QueuedPodInfo) GetPodProperty() *PodProperty {
	if pqi.podProperty != nil {
		return pqi.podProperty
	}
	if pqi.Pod == nil {
		return nil
	}

	pqi.podProperty = ExtractPodProperty(pqi.Pod)
	return pqi.podProperty
}

// QueuedUnitInfo is a Unit wrapper with additional information related to
// the unit's status in the scheduling queue, such as the timestamp when
// it's added to the queue.
type QueuedUnitInfo struct {
	UnitKey string
	ScheduleUnit
	// The time unit added to the scheduling queue (will be refreshed).
	Timestamp time.Time
	// Number of schedule attempts before successfully scheduled.
	// It's used to record the # attempts metric.
	Attempts int

	// The time when the unit is added to the queue for the first time. The unit may
	// back to the queue multiple times before it's successfully scheduled.
	// It shouldn't be updated once initialized. It's used to record the e2e sched
	// latency for a pod.
	InitialAttemptTimestamp time.Time

	// QueuePriorityScore is calculated according to pod.Spec, combined with priority. It should not change if no changes in pod.Spec.
	QueuePriorityScore float64
}

var (
	_ StoredUnit     = &QueuedUnitInfo{}
	_ ObservableUnit = &QueuedUnitInfo{}
)

// NewQueuedUnitInfo builds a QueuedPodInfo object.
func NewQueuedUnitInfo(unitKey string, unit ScheduleUnit, clock godelutil.Clock) *QueuedUnitInfo {
	now := clock.Now()
	// PodGroupUnit has its own priority
	priority := unit.GetPriority()
	return &QueuedUnitInfo{
		UnitKey:                 unitKey,
		ScheduleUnit:            unit,
		Timestamp:               now,
		InitialAttemptTimestamp: now,
		QueuePriorityScore:      float64(priority),
	}
}

func (qui *QueuedUnitInfo) String() string {
	if qui == nil {
		return "nil"
	}
	return qui.UnitKey
}

// AffinityTerm is a processed version of v1.PodAffinityTerm.
type AffinityTerm struct {
	Namespaces  sets.String
	Selector    labels.Selector
	TopologyKey string
}

// WeightedAffinityTerm is a "processed" representation of v1.WeightedAffinityTerm.
type WeightedAffinityTerm struct {
	AffinityTerm
	Weight int32
}

func newAffinityTerm(pod *v1.Pod, term *v1.PodAffinityTerm) (*AffinityTerm, error) {
	namespaces := godelutil.GetNamespacesFromPodAffinityTerm(pod, term)
	selector, err := metav1.LabelSelectorAsSelector(term.LabelSelector)
	if err != nil {
		return nil, err
	}
	return &AffinityTerm{Namespaces: namespaces, Selector: selector, TopologyKey: term.TopologyKey}, nil
}

// getAffinityTerms receives a Pod and affinity terms and returns the namespaces and
// selectors of the terms.
func getAffinityTerms(pod *v1.Pod, v1Terms []v1.PodAffinityTerm) ([]AffinityTerm, error) {
	if v1Terms == nil {
		return nil, nil
	}

	var terms []AffinityTerm
	for _, term := range v1Terms {
		t, err := newAffinityTerm(pod, &term)
		if err != nil {
			// We get here if the label selector failed to process
			return nil, err
		}
		terms = append(terms, *t)
	}
	return terms, nil
}

// getWeightedAffinityTerms returns the list of processed affinity terms.
func getWeightedAffinityTerms(pod *v1.Pod, v1Terms []v1.WeightedPodAffinityTerm) ([]WeightedAffinityTerm, error) {
	if v1Terms == nil {
		return nil, nil
	}

	var terms []WeightedAffinityTerm
	for _, term := range v1Terms {
		t, err := newAffinityTerm(pod, &term.PodAffinityTerm)
		if err != nil {
			// We get here if the label selector failed to process
			return nil, err
		}
		terms = append(terms, WeightedAffinityTerm{AffinityTerm: *t, Weight: term.Weight})
	}
	return terms, nil
}

// ImageStateSummary provides summarized information about the state of an image.
type ImageStateSummary struct {
	// Size of the image
	Size int64
	// Used to track how many nodes have this image
	NumNodes int
}

type NumaTopologyStatus struct {
	// if pod allocation info exits in both cnr and pod, using cnr
	// key: podNamespace/podName/podUID
	podAllocations map[string]*PodAllocation
	topology       map[int]*NumaStatus

	requestsOfSharedCores  *Resource
	availableOfSharedCores *Resource

	socketToFreeNumasOfConflictResources map[int]sets.Int
}

func (s *NumaTopologyStatus) Equal(o *NumaTopologyStatus) bool {
	return cmp.Equal(s.podAllocations, o.podAllocations) && cmp.Equal(s.topology, o.topology)
}

type PodAllocation struct {
	agent           bool
	numaAllocations map[int]*v1.ResourceList
}

type NumaStatus struct {
	socketId         int
	resourceStatuses map[string]*ResourceStatus
}

func NewNumaStatus(socketID int, monopolized bool, resources map[string]*ResourceStatus) *NumaStatus {
	return &NumaStatus{
		socketId:         socketID,
		resourceStatuses: resources,
	}
}

func (numaStatus *NumaStatus) GetSocket() int {
	return numaStatus.socketId
}

type ResourceStatus struct {
	Allocatable *resource.Quantity
	Available   *resource.Quantity
	Users       sets.String
}

// initializeNodeTransientInfo initializes transient information pertaining to node.
func initializeNodeTransientInfo() nodeTransientInfo {
	return nodeTransientInfo{AllocatableVolumesCount: 0, RequestedVolumes: 0}
}

// nodeTransientInfo contains transient node information while scheduling.
type nodeTransientInfo struct {
	// AllocatableVolumesCount contains number of volumes that could be attached to node.
	AllocatableVolumesCount int
	// Requested number of volumes on a particular node.
	RequestedVolumes int
}

// TransientSchedulerInfo is a transient structure which is destructed at the end of each scheduling cycle.
// It consists of items that are valid for a scheduling cycle and is used for message passing across predicates and
// priorities. Some examples which could be used as fields are number of volumes being used on node, current utilization
// on node etc.
// IMPORTANT NOTE: Make sure that each field in this structure is documented along with usage. Expand this structure
// only when absolutely needed as this data structure will be created and destroyed during every scheduling cycle.
type TransientSchedulerInfo struct {
	TransientLock sync.Mutex
	// NodeTransInfo holds the information related to nodeTransientInformation. NodeName is the key here.
	TransNodeInfo nodeTransientInfo
}

// NewTransientSchedulerInfo returns a new scheduler transient structure with initialized values.
func NewTransientSchedulerInfo() *TransientSchedulerInfo {
	tsi := &TransientSchedulerInfo{
		TransNodeInfo: initializeNodeTransientInfo(),
	}
	return tsi
}

// ResetTransientSchedulerInfo resets the TransientSchedulerInfo.
func (transientSchedInfo *TransientSchedulerInfo) ResetTransientSchedulerInfo() {
	transientSchedInfo.TransientLock.Lock()
	defer transientSchedInfo.TransientLock.Unlock()
	// Reset TransientNodeInfo.
	transientSchedInfo.TransNodeInfo.AllocatableVolumesCount = 0
	transientSchedInfo.TransNodeInfo.RequestedVolumes = 0
}

func (transientSchedInfo *TransientSchedulerInfo) Equal(o *TransientSchedulerInfo) bool {
	return cmp.Equal(transientSchedInfo.TransNodeInfo, o.TransNodeInfo)
}

// Resource is a collection of compute resource.
type Resource struct {
	MilliCPU         int64
	Memory           int64
	EphemeralStorage int64
	// We store allowedPodNumber (which is Node.Status.Allocatable.Pods().Value())
	// explicitly as int, to avoid conversions and improve performance.
	AllowedPodNumber int
	// ScalarResources
	ScalarResources map[v1.ResourceName]int64
}

// NewResource creates a Resource from ResourceList
func NewResource(rl v1.ResourceList) *Resource {
	r := &Resource{}
	r.Add(rl)
	return r
}

// NewResource creates a Resource from ResourceList pointer
func NewResourceFromPtr(rl *v1.ResourceList) *Resource {
	if rl == nil {
		return &Resource{}
	}
	return NewResource(*rl)
}

// Add adds ResourceList into Resource.
func (r *Resource) Add(rl v1.ResourceList) {
	if r == nil {
		return
	}

	for rName, rQuant := range rl {
		switch rName {
		case v1.ResourceCPU:
			r.MilliCPU += rQuant.MilliValue()
		case v1.ResourceMemory:
			r.Memory += rQuant.Value()
		case v1.ResourcePods:
			r.AllowedPodNumber += int(rQuant.Value())
		case v1.ResourceEphemeralStorage:
			if utilfeature.DefaultFeatureGate.Enabled(features.LocalStorageCapacityIsolation) {
				// if the local storage capacity isolation feature gate is disabled, pods request 0 disk.
				r.EphemeralStorage += rQuant.Value()
			}
		default:
			if helper.IsScalarResourceName(rName) {
				r.AddScalar(rName, rQuant.Value())
			}
		}
	}
}

// Sub subs ResourceList into Resource.
func (r *Resource) Sub(rl v1.ResourceList) {
	if r == nil {
		return
	}

	for rName, rQuant := range rl {
		switch rName {
		case v1.ResourceCPU:
			r.MilliCPU -= rQuant.MilliValue()
		case v1.ResourceMemory:
			r.Memory -= rQuant.Value()
		case v1.ResourcePods:
			r.AllowedPodNumber -= int(rQuant.Value())
		case v1.ResourceEphemeralStorage:
			if utilfeature.DefaultFeatureGate.Enabled(features.LocalStorageCapacityIsolation) {
				// if the local storage capacity isolation feature gate is disabled, pods request 0 disk.
				r.EphemeralStorage -= rQuant.Value()
			}
		default:
			if helper.IsScalarResourceName(rName) {
				r.AddScalar(rName, -rQuant.Value())
			}
		}
	}
}

// AddResource adds another Resource, this method could avoid Resource to ResourceList conversion
func (r *Resource) AddResource(resource *Resource) {
	if r == nil || resource == nil {
		return
	}

	r.MilliCPU += resource.MilliCPU
	r.Memory += resource.Memory
	r.AllowedPodNumber += resource.AllowedPodNumber
	if utilfeature.DefaultFeatureGate.Enabled(features.LocalStorageCapacityIsolation) {
		r.EphemeralStorage += resource.EphemeralStorage
	}

	for rName, rQuant := range resource.ScalarResources {
		if quant, ok := r.getScalarResourceQuantity(rName); ok {
			r.SetScalar(rName, rQuant+quant)
		} else {
			r.SetScalar(rName, rQuant)
		}
	}
}

func (r *Resource) getScalarResourceQuantity(name v1.ResourceName) (int64, bool) {
	if !helper.IsScalarResourceName(name) {
		return 0, false
	}

	if rQuant, ok := r.ScalarResources[name]; ok {
		return rQuant, ok
	}
	return 0, false
}

func (r *Resource) SubResource(resource *Resource) {
	if r == nil || resource == nil {
		return
	}

	r.MilliCPU -= resource.MilliCPU
	r.Memory -= resource.Memory
	r.AllowedPodNumber -= resource.AllowedPodNumber
	if utilfeature.DefaultFeatureGate.Enabled(features.LocalStorageCapacityIsolation) {
		r.EphemeralStorage -= resource.EphemeralStorage
	}

	for rName, rQuant := range resource.ScalarResources {
		quant, _ := r.getScalarResourceQuantity(rName)
		r.SetScalar(rName, quant-rQuant)
	}
}

// ResourceList returns a resource list of this resource.
func (r *Resource) ResourceList() v1.ResourceList {
	result := v1.ResourceList{
		v1.ResourceCPU:              *resource.NewMilliQuantity(r.MilliCPU, resource.DecimalSI),
		v1.ResourceMemory:           *resource.NewQuantity(r.Memory, resource.BinarySI),
		v1.ResourcePods:             *resource.NewQuantity(int64(r.AllowedPodNumber), resource.BinarySI),
		v1.ResourceEphemeralStorage: *resource.NewQuantity(r.EphemeralStorage, resource.BinarySI),
	}
	for rName, rQuant := range r.ScalarResources {
		if helper.IsHugePageResourceName(rName) {
			result[rName] = *resource.NewQuantity(rQuant, resource.BinarySI)
		} else {
			result[rName] = *resource.NewQuantity(rQuant, resource.DecimalSI)
		}
	}
	return result
}

// Clone returns a copy of this resource.
func (r *Resource) Clone() *Resource {
	res := &Resource{
		MilliCPU:         r.MilliCPU,
		Memory:           r.Memory,
		AllowedPodNumber: r.AllowedPodNumber,
		EphemeralStorage: r.EphemeralStorage,
	}
	if r.ScalarResources != nil {
		res.ScalarResources = make(map[v1.ResourceName]int64)
		for k, v := range r.ScalarResources {
			res.ScalarResources[k] = v
		}
	}
	return res
}

// AddScalar adds a resource by a scalar value of this resource.
func (r *Resource) AddScalar(name v1.ResourceName, quantity int64) {
	r.SetScalar(name, r.ScalarResources[name]+quantity)
}

// SetScalar sets a resource by a scalar value of this resource.
func (r *Resource) SetScalar(name v1.ResourceName, quantity int64) {
	// Lazily allocate scalar resource map.
	if r.ScalarResources == nil {
		r.ScalarResources = map[v1.ResourceName]int64{}
	}
	r.ScalarResources[name] = quantity
}

// SetMaxResource compares with ResourceList and takes max value for each Resource.
func (r *Resource) SetMaxResource(rl v1.ResourceList) {
	if r == nil {
		return
	}

	for rName, rQuantity := range rl {
		switch rName {
		case v1.ResourceMemory:
			if mem := rQuantity.Value(); mem > r.Memory {
				r.Memory = mem
			}
		case v1.ResourceCPU:
			if cpu := rQuantity.MilliValue(); cpu > r.MilliCPU {
				r.MilliCPU = cpu
			}
		case v1.ResourceEphemeralStorage:
			if ephemeralStorage := rQuantity.Value(); ephemeralStorage > r.EphemeralStorage {
				r.EphemeralStorage = ephemeralStorage
			}
		default:
			if helper.IsScalarResourceName(rName) {
				value := rQuantity.Value()
				if value > r.ScalarResources[rName] {
					r.SetScalar(rName, value)
				}
			}
		}
	}
}

// SetMinResource compares with ResourceList and takes min value for each Resource.
func (r *Resource) SetMinResource(rl v1.ResourceList) {
	if r == nil {
		return
	}

	for rName, rQuantity := range rl {
		switch rName {
		case v1.ResourceMemory:
			if mem := rQuantity.Value(); mem < r.Memory {
				r.Memory = mem
			}
		case v1.ResourceCPU:
			if cpu := rQuantity.MilliValue(); cpu < r.MilliCPU {
				r.MilliCPU = cpu
			}
		case v1.ResourceEphemeralStorage:
			if ephemeralStorage := rQuantity.Value(); ephemeralStorage < r.EphemeralStorage {
				r.EphemeralStorage = ephemeralStorage
			}
		default:
			if helper.IsScalarResourceName(rName) {
				value := rQuantity.Value()
				if value < r.ScalarResources[rName] {
					r.SetScalar(rName, value)
				}
			}
		}
	}
}

// for scalar resources, just check these in available list
func (r *Resource) Satisfy(request *Resource) bool {
	if r == nil {
		r = &Resource{}
	}
	if request == nil {
		return true
	}
	if request.MilliCPU > 0 && r.MilliCPU < request.MilliCPU {
		return false
	}
	if request.Memory > 0 && r.Memory < request.Memory {
		return false
	}
	if utilfeature.DefaultFeatureGate.Enabled(features.LocalStorageCapacityIsolation) {
		if request.EphemeralStorage > 0 && r.EphemeralStorage < request.EphemeralStorage {
			return false
		}
	}
	for rName, rVal := range r.ScalarResources {
		if request.ScalarResources[rName] > 0 && rVal < request.ScalarResources[rName] {
			return false
		}
	}
	return true
}

func (r *Resource) IsZero() bool {
	return r.MilliCPU == 0 && r.Memory == 0 && r.EphemeralStorage == 0 && len(r.ScalarResources) == 0
}

// DefaultBindAllHostIP defines the default ip address used to bind to all host.
const DefaultBindAllHostIP = "0.0.0.0"

// ProtocolPort represents a protocol port pair, e.g. tcp:80.
type ProtocolPort struct {
	Protocol string
	Port     int32
}

// NewProtocolPort creates a ProtocolPort instance.
func NewProtocolPort(protocol string, port int32) *ProtocolPort {
	pp := &ProtocolPort{
		Protocol: protocol,
		Port:     port,
	}

	if len(pp.Protocol) == 0 {
		pp.Protocol = string(v1.ProtocolTCP)
	}

	return pp
}

// HostPortInfo stores mapping from ip to a set of ProtocolPort
type HostPortInfo map[string]map[ProtocolPort]struct{}

// Add adds (ip, protocol, port) to HostPortInfo
func (h HostPortInfo) Add(ip, protocol string, port int32) {
	if port <= 0 {
		return
	}

	h.sanitize(&ip, &protocol)

	pp := NewProtocolPort(protocol, port)
	if _, ok := h[ip]; !ok {
		h[ip] = map[ProtocolPort]struct{}{
			*pp: {},
		}
		return
	}

	h[ip][*pp] = struct{}{}
}

// Remove removes (ip, protocol, port) from HostPortInfo
func (h HostPortInfo) Remove(ip, protocol string, port int32) {
	if port <= 0 {
		return
	}

	h.sanitize(&ip, &protocol)

	pp := NewProtocolPort(protocol, port)
	if m, ok := h[ip]; ok {
		delete(m, *pp)
		if len(h[ip]) == 0 {
			delete(h, ip)
		}
	}
}

// Len returns the total number of (ip, protocol, port) tuple in HostPortInfo
func (h HostPortInfo) Len() int {
	length := 0
	for _, m := range h {
		length += len(m)
	}
	return length
}

// CheckConflict checks if the input (ip, protocol, port) conflicts with the existing
// ones in HostPortInfo.
func (h HostPortInfo) CheckConflict(ip, protocol string, port int32) bool {
	if port <= 0 {
		return false
	}

	h.sanitize(&ip, &protocol)

	pp := NewProtocolPort(protocol, port)

	// If ip is 0.0.0.0 check all IP (protocol, port) pair
	if ip == DefaultBindAllHostIP {
		for _, m := range h {
			if _, ok := m[*pp]; ok {
				return true
			}
		}
		return false
	}

	// If ip isn't 0.0.0.0, only check IP and 0.0.0.0's (protocol, port) pair
	for _, key := range []string{DefaultBindAllHostIP, ip} {
		if m, ok := h[key]; ok {
			if _, ok2 := m[*pp]; ok2 {
				return true
			}
		}
	}

	return false
}

// sanitize the parameters
func (h HostPortInfo) sanitize(ip, protocol *string) {
	if len(*ip) == 0 {
		*ip = DefaultBindAllHostIP
	}
	if len(*protocol) == 0 {
		*protocol = string(v1.ProtocolTCP)
	}
}

// Victims represents:
// Pods:  a group of pods expected to be preempted.
// PreemptionState: save state of all victims in each node
type Victims struct {
	Pods            []*v1.Pod
	PreemptionState *CycleState
}

func (v Victims) String() string {
	var victimPods []string
	for _, pod := range v.Pods {
		victimPods = append(victimPods, podutil.GetPodKey(pod))
	}
	return fmt.Sprintf("{pods: %v, state: %v}", victimPods, v.PreemptionState)
}

// MetaPod represent identifier for a v1.Pod
type MetaPod struct {
	UID string
}

// MetaVictims represents:
//
//	pods:  a group of pods expected to be preempted.
//	  Only Pod identifiers will be sent and user are expect to get v1.Pod in their own way.
//	numPDBViolations: the count of violations of PodDisruptionBudget
type MetaVictims struct {
	Pods             []*MetaPod
	NumPDBViolations int64
}

type NominatedNode struct {
	// NodeName is the node where the pod is supposed to place
	NodeName string `json:"node"`
	// VictimPods is the collection of all victim pods name. VictimPods should not be nil or empty.
	// If VictimPods is empty, this pod are supposed to be assumed
	VictimPods VictimPods `json:"victims"`
}

func (nn *NominatedNode) DeepCopy() *NominatedNode {
	if nn == nil {
		return nil
	}
	return &NominatedNode{
		NodeName:   nn.NodeName,
		VictimPods: nn.VictimPods.DeepCopy(),
	}
}

func (nn *NominatedNode) Marshall() string {
	if nn == nil {
		return ""
	}
	return fmt.Sprintf("nodeName: %s, victimPods: %v", nn.NodeName, nn.VictimPods.Marshall())
}

type VictimPods []VictimPod

func (vp VictimPods) DeepCopy() VictimPods {
	count := len(vp)
	vps := make([]VictimPod, count)
	for index, item := range vp {
		vps[index] = VictimPod{
			Name:      item.Name,
			Namespace: item.Namespace,
			UID:       item.UID,
		}
	}
	return vps
}

func (vp VictimPods) Marshall() string {
	count := len(vp)
	vps := make([]string, count)
	for index, item := range vp {
		vps[index] = fmt.Sprintf("%s/%s/%s", item.Namespace, item.Name, item.UID)
	}
	return strings.Join(vps, ",")
}

type VictimPod struct {
	Name      string `json:"name"`
	Namespace string `json:"namespace"`
	UID       string `json:"uid"`
}

type PotentialVictims []PotentialVictim

type PotentialVictim struct {
	Queue       string `json:"queue"`
	Application string `json:"application"`
}

type PodGroupSchedulingStatus string

// These internal status should map to api definition
const (
	Waiting    PodGroupSchedulingStatus = "Waiting"
	Successful PodGroupSchedulingStatus = "Successful"
	Failed     PodGroupSchedulingStatus = "Failed"
)

type PluginCollectionSet map[string]*PluginCollection

type PluginMap map[string]Plugin

const DefaultPluginWeight = 1

type PluginCollection struct {
	// Filters is a set of plugins that should be invoked when filtering out nodes that cannot run the Pod.
	Filters []*PluginSpec

	// Scores is a set of plugins that should be invoked when ranking nodes that have passed the filtering phase.
	Scores []*PluginSpec

	// Searchings is a set of plugins that should be invoked in preemption phase
	Searchings []*VictimSearchingPluginCollectionSpec

	Sortings []*PluginSpec
}

type VictimSearchingPluginCollectionSpec struct {
	ForceQuickPassVal  bool
	EnableQuickPassVal bool
	RejectNotSureVal   bool
	Plugins            []*PluginSpec
}

func NewVictimSearchingPluginCollectionSpec(preemptions []config.Plugin, enableQuickPass, forceQuickPass, rejectNotSure bool) *VictimSearchingPluginCollectionSpec {
	pluginCollection := &VictimSearchingPluginCollectionSpec{
		ForceQuickPassVal:  forceQuickPass,
		EnableQuickPassVal: enableQuickPass,
		RejectNotSureVal:   rejectNotSure,
	}
	for _, preemption := range preemptions {
		pluginCollection.Plugins = append(pluginCollection.Plugins, NewPluginSpec(preemption.Name))
	}
	return pluginCollection
}

func NewVictimCheckingPluginCollectionSpec(preemptions []binderconfig.Plugin, enableQuickPass, forceQuickPass bool) *VictimCheckingPluginCollectionSpec {
	pluginCollection := &VictimCheckingPluginCollectionSpec{
		EnableQuickPassVal: enableQuickPass,
		ForceQuickPassVal:  forceQuickPass,
	}
	for _, preemption := range preemptions {
		pluginCollection.Plugins = append(pluginCollection.Plugins, NewPluginSpec(preemption.Name))
	}
	return pluginCollection
}

func (pps *VictimSearchingPluginCollectionSpec) GetSearchingPlugins() []*PluginSpec {
	return pps.Plugins
}

func (pps *VictimSearchingPluginCollectionSpec) EnableQuickPass() bool {
	return pps.EnableQuickPassVal
}

func (pps *VictimSearchingPluginCollectionSpec) ForceQuickPass() bool {
	return pps.ForceQuickPassVal
}

func (pps *VictimSearchingPluginCollectionSpec) RejectNotSure() bool {
	return pps.RejectNotSureVal
}

type VictimCheckingPluginCollectionSpec struct {
	ForceQuickPassVal  bool
	EnableQuickPassVal bool
	Plugins            []*PluginSpec
}

func (pps *VictimCheckingPluginCollectionSpec) GetSearchingPlugins() []*PluginSpec {
	return pps.Plugins
}

func (pps *VictimCheckingPluginCollectionSpec) EnableQuickPass() bool {
	return pps.EnableQuickPassVal
}

func (pps *VictimCheckingPluginCollectionSpec) ForceQuickPass() bool {
	return pps.ForceQuickPassVal
}

type PluginSpec struct {
	Name   string
	Weight int64
}

func NewPluginSpec(name string) *PluginSpec {
	return &PluginSpec{
		Name:   name,
		Weight: DefaultPluginWeight,
	}
}

func NewPluginSpecWithWeight(name string, weight int64) *PluginSpec {
	return &PluginSpec{
		Name:   name,
		Weight: weight,
	}
}

func (ps *PluginSpec) GetName() string {
	return ps.Name
}

func (ps *PluginSpec) SetWeight(weight int64) {
	ps.Weight = weight
}

func (ps *PluginSpec) GetWeight() int64 {
	return ps.Weight
}

type PotentialVictimsInNodes struct {
	mu sync.RWMutex
	// key is node name, value is victim keys
	potentialVictims map[string][]string
}

func NewPotentialVictimsInNodes() *PotentialVictimsInNodes {
	return &PotentialVictimsInNodes{
		potentialVictims: map[string][]string{},
	}
}

func (pv *PotentialVictimsInNodes) SetPotentialVictims(node string, potentialVictims []string) {
	pv.mu.Lock()
	defer pv.mu.Unlock()

	pv.potentialVictims[node] = potentialVictims
}

func (pv *PotentialVictimsInNodes) GetPotentialVictims(node string) []string {
	pv.mu.RLock()
	defer pv.mu.RUnlock()

	return pv.potentialVictims[node]
}

func IsConflictResourcesForDifferentQoS(resourceName v1.ResourceName) bool {
	switch resourceName {
	case v1.ResourceCPU:
		return true
	case v1.ResourceMemory:
		return true
	}
	return false
}

func TransferGenerationStore(cache, snapshot generationstore.Store) (generationstore.ListStore, generationstore.RawStore) {
	if _, ok := cache.(generationstore.ListStore); !ok {
		panic("can not transfer store to ListStore")
	}
	if _, ok := snapshot.(generationstore.RawStore); !ok {
		panic("can not transfer store to RawStore")
	}
	return cache.(generationstore.ListStore), snapshot.(generationstore.RawStore)
}

type CachePodState struct {
	Pod *v1.Pod
	// Used by assumedPod to determinate expiration.
	Deadline *time.Time
	// Used to block cache from expiring assumedPod if binding still runs
	BindingFinished bool
}

type GenerationStringSetRangeFunc func(key string)

type GenerationStringSet interface {
	Has(string) bool
	Insert(...string)
	Delete(...string)
	Len() int
	GetGeneration() int64
	SetGeneration(generation int64)
	Equal(GenerationStringSet) bool
	Reset(GenerationStringSet)
	Range(GenerationStringSetRangeFunc)
	Strings() []string
}

type GenerationStringSetImpl struct {
	set        sets.String
	generation int64
}

var (
	_ GenerationStringSet       = &GenerationStringSetImpl{}
	_ generationstore.StoredObj = &GenerationStringSetImpl{}
)

func NewGenerationStringSet(strings ...string) GenerationStringSet {
	return &GenerationStringSetImpl{
		set:        sets.NewString(strings...),
		generation: 0,
	}
}

func (s *GenerationStringSetImpl) Has(string string) bool {
	return s.set.Has(string)
}

func (s *GenerationStringSetImpl) Insert(strings ...string) {
	s.set.Insert(strings...)
}

func (s *GenerationStringSetImpl) Delete(strings ...string) {
	s.set.Delete(strings...)
}

func (s *GenerationStringSetImpl) Len() int {
	return s.set.Len()
}

func (s *GenerationStringSetImpl) GetGeneration() int64 {
	return s.generation
}

func (s *GenerationStringSetImpl) SetGeneration(generation int64) {
	s.generation = generation
}

func (s *GenerationStringSetImpl) Equal(store GenerationStringSet) bool {
	if s == nil {
		return store == nil
	}
	if s.Len() != store.Len() {
		return false
	}
	for k := range s.set {
		if !store.Has(k) {
			return false
		}
	}
	return true
}

func (s *GenerationStringSetImpl) Reset(store GenerationStringSet) {
	if s == nil {
		return
	}
	set := sets.NewString()
	store.Range(func(key string) {
		set.Insert(key)
	})
	s.set = set
	s.generation = store.GetGeneration()
}

func (s *GenerationStringSetImpl) Range(f GenerationStringSetRangeFunc) {
	if s == nil {
		return
	}
	for k := range s.set {
		f(k)
	}
}

func (s *GenerationStringSetImpl) Strings() []string {
	strs := []string{}
	for k := range s.set {
		strs = append(strs, k)
	}
	return strs
}

// In order to minimize computational overhead as much as possible, only the CPU/mem fields are used here.
// In the future, field expansion needs to be carried out based on actual needs
type LoadAwareNodeUsage struct {
	RequestMilliCPU int64
	RequestMEM      int64
	ProfileMilliCPU int64
	ProfileMEM      int64
}

type LoadAwareNodeMetricInfo struct {
	Name                 string
	UpdateTime           metav1.Time
	ProfileMilliCPUUsage int64
	ProfileMEMUsage      int64
}
