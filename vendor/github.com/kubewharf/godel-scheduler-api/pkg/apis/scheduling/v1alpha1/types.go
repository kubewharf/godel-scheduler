package v1alpha1

import (
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
)

// +genclient
// +genclient:nonNamespaced
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
// +kubebuilder:resource:scope=Cluster
// +kubebuilder:subresource:status

// Scheduler captures information about a scheduler
// Scheduler objects are non-namespaced.
type Scheduler struct {
	metav1.TypeMeta `json:",inline"`
	// +optional
	metav1.ObjectMeta `json:"metadata,omitempty"`

	// Spec defines the behavior of a Scheduler.
	// +optional
	Spec SchedulerSpec `json:"spec,omitempty"`

	// Status represents the current information about a Scheduler. This data may not be up
	// to date.
	// +optional
	Status SchedulerStatus `json:"status,omitempty"`
}

type SchedulerSpec struct {
	// TODO: populate more info if necessary
}

type SchedulerStatus struct {
	// +optional
	Phase SchedulerPhase `json:"phase,omitempty"`
	// LastUpdateTime is the latest update time.
	// It should be first time initialized when this crd is created
	// +optional
	LastUpdateTime *metav1.Time `json:"lastUpdateTime,omitempty"`

	// CurrentHost is the host the current scheduler leader runs on
	// +optional
	CurrentHost string `json:"currentHost,omitempty"`

	// TODO: populate more info here
	// TODO: investigate whether we need a separate API object for this
	// +optional
	MetricsStatus *SchedulerAggregatedMetricsStatus `json:"metricsStatus,omitempty"`

	// SchedulerNodePartitionName is the name of NodePartition which contains all the nodes in this scheduler's partition
	// +optional
	SchedulerNodePartitionName string `json:"schedulerNodePartitionName,omitempty"`
}

type SchedulerPhase string

const (
	Active   SchedulerPhase = "Active"
	InActive SchedulerPhase = "InActive"
)

type SchedulerAggregatedMetricsStatus struct {
	// Throughput     int    // pods per second

	// pending pods in the pending queue
	// +optional
	PendingPods *int `json:"pendingPods,omitempty"`

	// TODO: add resource metrics
	// ResourceCapacity   ResourceList
	// ResourceUsed       ResourceList
	// ResourceUsage      PartitionResourceUsage
}

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// SchedulerList is a collection of Scheduler objects.
type SchedulerList struct {
	metav1.TypeMeta `json:",inline"`

	// Standard list metadata
	// More info: https://git.k8s.io/community/contributors/devel/api-conventions.md#metadata
	// +optional
	metav1.ListMeta `json:"metadata,omitempty"`

	// items is the list of Schedulers
	Items []Scheduler `json:"items"`
}

// PodGroup indicates a collection of Pods which will be used for batch workloads.
// +genclient
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="Status",type="string",JSONPath=".status.phase",priority=1,description="The current status of the PodGroup"
// +kubebuilder:printcolumn:name="MinMember",type="integer",JSONPath=".spec.minMember",priority=1,description="The minimal number of members/tasks to run the PodGroup"
// +kubebuilder:printcolumn:name="TimeoutSeconds",type="integer",JSONPath=".spec.scheduleTimeoutSeconds",priority=1,description="The maximal time of tasks to wait before running the PodGroup"
// +kubebuilder:printcolumn:name="Age",type="date",JSONPath=".metadata.creationTimestamp"
type PodGroup struct {
	metav1.TypeMeta `json:",inline"`
	// Standard object's metadata.
	// More info: https://git.k8s.io/community/contributors/devel/api-conventions.md#metadata
	// +optional
	metav1.ObjectMeta `json:"metadata,omitempty"`

	// Specification of the desired behavior of the pod group.
	// More info: https://git.k8s.io/community/contributors/devel/api-conventions.md#spec-and-status
	Spec PodGroupSpec `json:"spec,omitempty"`

	// Status represents the current information about a pod group.
	// This data may not be up to date.
	// +optional
	Status PodGroupStatus `json:"status,omitempty"`
}

// PodGroupSpec represents the template of a pod group.
type PodGroupSpec struct {
	// MinMember defines the minimal number of members/tasks to run the pod group;
	// if there's not enough resources to start all tasks, the scheduler
	// will not start anyone.
	MinMember int32 `json:"minMember,omitempty"`

	// If specified, indicates the PodGroup's priority. "system-node-critical" and
	// "system-cluster-critical" are two special reserved keywords which indicate the highest priorities.
	// Any other name must be defined by creating a PriorityClass object with that name.
	// If not specified, the PodGroup priority will be default.
	// If default priority class doesn't exist, the PodGroup priority will be zero.
	// +optional
	PriorityClassName string `json:"priorityClassName,omitempty"`

	// ScheduleTimeoutSeconds defines the maximal time of tasks to wait before run the pod group;
	// +optional
	ScheduleTimeoutSeconds *int32 `json:"scheduleTimeoutSeconds,omitempty"`

	// Application indicates the podGroup belongs to a logical Application
	// This will be used for coordinate with features like drf and faire share.
	// +optional
	Application string `json:"application,omitempty"`

	// Affinity shows the affinity/anti-affinity rules that scheduler needs to follow
	// when scheduling instances of this pod group.
	// +optional
	Affinity *Affinity `json:"affinity,omitempty"`
}

// Affinity contains all the affinity and anti affinity rules
type Affinity struct {
	// +optional
	PodGroupAffinity *PodGroupAffinity `json:"podGroupAffinity,omitempty"`

	// +optional
	PodGroupAntiAffinity *PodGroupAntiAffinity `json:"podGroupAntiAffinity,omitempty"`
}

// PodGroupAffinity defines the specific affinity rules
type PodGroupAffinity struct {
	// Required represents a set of affinity terms, all of them MUST be satisfied
	// +optional
	Required []PodGroupAffinityTerm `json:"required,omitempty"`

	// Preferred represents a set of affinity terms that don't necessarily have to be satisfied.
	// But we need to try to satisfy
	// +optional
	Preferred []PodGroupAffinityTerm `json:"preferred,omitempty"`

	// SortRules indicates how the nodeGroups, aggregated by required or
	// preferred affinity, are sorted. The rule's index in slice is the sort
	// sequence. If this is not defined, we will sort nodeGroups in default sort rule.
	// +optional
	SortRules []SortRule `json:"sortRules,omitempty"`

	// NodeSelector specifies the requirements that must be met for the
	// podGroup's pods to fit on the nodes.
	// +optional
	NodeSelector *v1.NodeSelector `json:"nodeSelector,omitempty"`
}

// PodGroupAntiAffinity defines the specific anti affinity rules
type PodGroupAntiAffinity struct {
	// Required represents a set of affinity terms, all of them MUST NOT be satisfied
	// +optional
	Required []PodGroupAffinityTerm `json:"required,omitempty"`

	// Preferred represents a set of affinity terms that we need to try not to satisfy them
	// +optional
	Preferred []PodGroupAffinityTerm `json:"preferred,omitempty"`
}

// PodGroupAffinityTerm defines terms of pod group affinity
type PodGroupAffinityTerm struct {
	// TopologyKey is the specific topology name that all related tasks should be scheduled to
	// Empty topologyKey is not allowed.
	TopologyKey string `json:"topologyKey,omitempty"`
}

// SortRule defines the rule for sorting items.
type SortRule struct {
	// Resource defines the resource name for sorting.
	Resource SortResource `json:"resource"`

	// Dimension may be Capacity or Available for the moment.
	// We allow this field to be empty for backward compatibility.
	Dimension SortDimension `json:"dimension,omitempty"`

	// Order may be either Ascending or Descending
	Order SortOrder `json:"order"`
}

// SortResource is the resource that items are sorted by.
type SortResource string

const (
	// GPUResource means items are sorted by GPU resource.
	GPUResource SortResource = "GPU"

	// CPUResource means items are sorted by CPU resource.
	CPUResource SortResource = "CPU"

	// MemoryResource means items are sorted by Memory Resource.
	MemoryResource SortResource = "Memory"
)

type SortDimension string

const (
	Capacity SortDimension = "Capacity"

	Available SortDimension = "Available"
)

// SortOrder is the order of items.
type SortOrder string

const (
	// AscendingOrder is the order which items are increasing in a dimension.
	AscendingOrder SortOrder = "Ascending"

	// DescendingOrder is the order which items are decreasing in a dimension.
	DescendingOrder SortOrder = "Descending"
)

// PodGroupPhase is the phase of a pod group at the current time.
type PodGroupPhase string

const (
	// PodPending means the pod group has been accepted and less than `minMember` pods have been created.
	PodGroupPending PodGroupPhase = "Pending"

	// PodGroupPreScheduling means all of pods has been are waiting to be scheduled, but godel-scheduler
	// can not allocate enough resources to it.
	PodGroupPreScheduling PodGroupPhase = "PreScheduling"

	// PodGroupScheduled means `spec.minMember` pods of PodGroups have been scheduled.
	PodGroupScheduled PodGroupPhase = "Scheduled"

	// PodGroupRunning means `spec.minMember` pods of PodGroups has been in running phase.
	PodGroupRunning PodGroupPhase = "Running"

	// PodGroupTimeout means scheduler doesn't permit `spec.minMember` pod in given or default timeout period.
	PodGroupTimeout PodGroupPhase = "Timeout"

	// PodGroupFinished means all of `spec.minMember` pods runs successfully.
	PodGroupFinished PodGroupPhase = "Finished"

	// PodGroupFailed means at least one of `spec.minMember` pods is failed.
	PodGroupFailed PodGroupPhase = "Failed"

	// PodGroupUnknown is reserved for some known situation that pod group controller can not determine the status.
	PodGroupUnknown PodGroupPhase = "Unknown"
)

// PodGroupCondition contains details for the current state of this pod group.
type PodGroupCondition struct {
	// Phase is the current phase of PodGroup
	Phase PodGroupPhase `json:"phase,omitempty"`

	// Status is the status of the condition.
	Status v1.ConditionStatus `json:"status,omitempty"`

	// Last time the phase transitioned from another to current phase.
	// +optional
	LastTransitionTime metav1.Time `json:"lastTransitionTime,omitempty"`

	// Unique, one-word, CamelCase reason for the phase's last transition.
	// +optional
	Reason string `json:"reason,omitempty"`

	// Human-readable message indicating details about last transition.
	// +optional
	Message string `json:"message,omitempty"`
}

// PodGroupStatus represents the current state of a pod group.
type PodGroupStatus struct {
	// Current phase of PodGroup.
	Phase PodGroupPhase `json:"phase,omitempty"`

	// The conditions of PodGroup.
	// +optional
	Conditions []PodGroupCondition `json:"conditions,omitempty"`

	// OccupiedBy marks the workload (e.g., deployment, job) name that occupy the PodGroup.
	// It is empty if not initialized.
	OccupiedBy string `json:"occupiedBy,omitempty"`

	// The number of actively pending pods.
	// +optional
	Pending int32 `json:"pending,omitempty"`

	// The number of actively running pods.
	// +optional
	Running int32 `json:"running,omitempty"`

	// The number of pods which reached phase Succeeded.
	// +optional
	Succeeded int32 `json:"succeeded,omitempty"`

	// The number of pods which reached phase Failed.
	// +optional
	Failed int32 `json:"failed,omitempty"`

	// ScheduleStartTime of the pod group
	ScheduleStartTime *metav1.Time `json:"scheduleStartTime,omitempty"`
}

// Reserve for pod group controller to use
type PodGroupConditionDetail string

const (
	// PodGroupReady is that PodGroup has reached scheduling restriction
	PodGroupReady PodGroupConditionDetail = "pod group is ready"
	// PodGroupNotReady is that PodGroup has not yet reached the scheduling restriction
	PodGroupNotReady PodGroupConditionDetail = "pod group is not ready"
)

// PodGroupList is a collection of pod groups.
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
type PodGroupList struct {
	metav1.TypeMeta `json:",inline"`
	// Standard list metadata
	// +optional
	metav1.ListMeta `json:"metadata,omitempty"`

	// Items is the list of PodGroup
	Items []PodGroup `json:"items"`
}

// Movement stores necessary information for rescheduling decisions.
// It has a group of task movements which need to be performed in one attempt, movement creator, movement generation and so on.
// +genclient
// +genclient:nonNamespaced
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
// +kubebuilder:resource:scope=Cluster
// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
type Movement struct {
	metav1.TypeMeta `json:",inline"`

	// Standard object's metadata.
	// +optional
	metav1.ObjectMeta `json:"metadata,omitempty"`

	// Spec defines the behavior of a Movement.
	// +optional
	Spec MovementSpec `json:"spec,omitempty"`

	// Status describes the current status of a Movement.
	// This data may not be up to date.
	// +optional
	Status MovementStatus `json:"status,omitempty"`
}

// MovementSpec describes the attributes that a Movement is created with.
type MovementSpec struct {
	// A list of tasks need to be rescheduled
	// +optional
	DeletedTasks []*TaskInfo `json:"deletedTasks,omitempty"`

	// Creator of this movement.
	// +optional
	Creator string `json:"creator,omitempty"`

	// Generation of this movement. When a new round of rescheduling decisions are made, generation is bumped.
	// +optional
	Generation uint `json:"generation,omitempty"`
}

// Current status of this movement.
type MovementStatus struct {
	// A list of movement info for all owners
	// +optional
	Owners []*Owner `json:"owners,omitempty"`

	// Names of godel schedulers which have received this movement crd
	// +optional
	NotifiedSchedulers []string `json:"notifiedSchedulers,omitempty"`
}

// Contain node suggestions for each owner
type Owner struct {
	// Information of task owner
	Owner *OwnerInfo `json:"owner"`

	// Information of suggested nodes
	RecommendedNodes []*RecommendedNode `json:"recommendedNodes,omitempty"`

	// Tasks scheduled successfully but not use suggestion
	// +optional
	MismatchedTasks []*TaskInfo `json:"mismatchedTasks,omitempty"`
}

// Contain desired state and actual state for node
type RecommendedNode struct {
	// Node Name
	Node string `json:"node,omitempty"`

	// Desired pod count for this node
	DesiredPodCount int64 `json:"desiredPodCount,omitempty"`

	// Pods using this suggestion
	ActualPods []*TaskInfo `json:"actualPods,omitempty"`
}

// Information of each task needs to be rescheduled
type TaskInfo struct {
	// Task name, e.g. pod name
	Name string `json:"name"`

	// Task namespace, e.g. pod namespace
	Namespace string `json:"namespace"`

	// Task uid, e.g. pod uid
	UID types.UID `json:"uid"`

	// Node of this task
	Node string `json:"node"`
}

// Information of task owner
type OwnerInfo struct {
	// Owner type, e.g. ReplicaSet/StatefulSet...
	Type string `json:"type"`

	// Owner name
	Name string `json:"name"`

	// Owner namespace
	Namespace string `json:"namespace"`

	// Owner uid
	UID types.UID `json:"uid"`
}

// MovementList is a collection of  movements.
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
type MovementList struct {
	metav1.TypeMeta `json:",inline"`

	// Standard list metadata
	// +optional
	metav1.ListMeta `json:"metadata,omitempty"`

	// Items is the list of Movement
	Items []Movement `json:"items"`
}
