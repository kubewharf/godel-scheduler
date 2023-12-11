/*
Copyright 2022 The Katalyst Authors.

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

package v1alpha1

import (
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// +genclient
// +genclient:nonNamespaced
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
// +kubebuilder:resource:scope=Cluster,shortName=kcnr
// +kubebuilder:subresource:status

// CustomNodeResource captures information about a custom defined node resource
// CustomNodeResource objects are non-namespaced.
type CustomNodeResource struct {
	metav1.TypeMeta `json:",inline"`
	// +optional
	metav1.ObjectMeta `json:"metadata,omitempty"`

	// Spec defines the behavior of a CustomNodeResource.
	// +optional
	Spec CustomNodeResourceSpec `json:"spec,omitempty"`

	// Status represents the current information about a CustomNodeResource.
	// This data may not be up-to-date.
	// +optional
	Status CustomNodeResourceStatus `json:"status,omitempty"`
}

type CustomNodeResourceSpec struct {
	// +optional
	NodeResourceProperties []*Property `json:"nodeResourceProperties,omitempty"`

	// customized taint for katalyst, which may affect partial tasks
	// +optional
	Taints []*Taint `json:"taints,omitempty"`
}

type Taint struct {
	// Required. The taint key to be applied to a node.
	Key string `json:"key,omitempty"`
	// Required. The taint value corresponding to the taint key.
	// +optional
	Value string `json:"value,omitempty"`
	// Required. The effect of the taint on pods
	// that do not tolerate the taint.
	// Valid effects are NoScheduleForReclaimedTasks.
	Effect TaintEffect `json:"effect,omitempty"`
}

type TaintEffect string

const (
	// TaintEffectNoScheduleForReclaimedTasks
	// Do not allow new pods using reclaimed resources to schedule onto the node unless they tolerate the taint,
	// but allow all pods submitted to Kubelet without going through the scheduler
	// to start, and allow all already-running pods to continue running.
	// Enforced by the scheduler.
	TaintEffectNoScheduleForReclaimedTasks TaintEffect = "NoScheduleForReclaimedTasks"
)

type Property struct {
	// property name
	PropertyName string `json:"propertyName"`

	// values of the specific property
	// +optional
	PropertyValues []string `json:"propertyValues,omitempty"`

	// values of the quantity-types property
	// +optional
	PropertyQuantity *resource.Quantity `json:"propertyQuantity,omitempty"`
}

type CustomNodeResourceStatus struct {
	// Resources defines the numeric quantities in this node; for instance reclaimed resources for this node
	// +optional
	Resources Resources `json:"resources"`

	// +optional
	TopologyZone []*TopologyZone `json:"topologyZone,omitempty"`

	// TopologyPolicy indicates placement policy for scheduler or other centralized components to follow.
	// this policy (including topology scope) is defined in topology-manager, katalyst is
	// responsible to parse the policy, and transform to TopologyPolicy here.
	// +kubebuilder:default:=none
	TopologyPolicy TopologyPolicy `json:"topologyPolicy"`

	// Conditions is an array of current observed cnr conditions.
	// +optional
	Conditions []CNRCondition `json:"conditions,omitempty"`
}

type TopologyPolicy string

const (
	// TopologyPolicyNone policy is the default policy and does not perform any topology alignment.
	TopologyPolicyNone TopologyPolicy = "None"

	// TopologyPolicySingleNUMANodeContainerLevel represents single-numa-node policy and container level.
	TopologyPolicySingleNUMANodeContainerLevel TopologyPolicy = "SingleNUMANodeContainerLevel"

	// TopologyPolicySingleNUMANodePodLevel represents single-numa-node policy and pod level.
	TopologyPolicySingleNUMANodePodLevel TopologyPolicy = "SingleNUMANodePodLevel"

	// TopologyPolicyRestrictedContainerLevel represents restricted policy and container level.
	TopologyPolicyRestrictedContainerLevel TopologyPolicy = "RestrictedContainerLevel"

	// TopologyPolicyRestrictedPodLevel represents restricted policy and pod level.
	TopologyPolicyRestrictedPodLevel TopologyPolicy = "RestrictedPodLevel"

	// TopologyPolicyBestEffortContainerLevel represents best-effort policy and container level.
	TopologyPolicyBestEffortContainerLevel TopologyPolicy = "BestEffortContainerLevel"

	// TopologyPolicyBestEffortPodLevel represents best-effort policy and pod level.
	TopologyPolicyBestEffortPodLevel TopologyPolicy = "BestEffortPodLevel"
)

// CNRCondition contains condition information for a cnr.
type CNRCondition struct {
	// Type is the type of the condition.
	Type CNRConditionType `json:"type"`
	// Status of the condition, one of True, False, Unknown.
	Status v1.ConditionStatus `json:"status" `
	// Last time we got an update on a given condition.
	// +optional
	LastHeartbeatTime metav1.Time `json:"lastHeartbeatTime,omitempty"`
	// (brief) reason for the condition's last transition.
	// +optional
	Reason string `json:"reason,omitempty"`
	// Human-readable message indicating details about last transition.
	// +optional
	Message string `json:"message,omitempty"`
}

type CNRConditionType string

const (
	CNRAgentReady    CNRConditionType = "AgentReady"
	CNRAgentNotFound CNRConditionType = "AgentNotFound"
)

type TopologyZone struct {
	// Type represents which kind of resource this TopologyZone is for;
	// for instance, Socket, Numa, GPU, NIC, Disk and so on.
	Type TopologyType `json:"type"`

	// Name represents the name for the given type for resource; for instance,
	// - disk-for-log, disk-for-storage may have different usage or attributes, so we
	//   need separate structure to distinguish them.
	Name string `json:"name"`

	// Resources defines the numeric quantities in this TopologyZone; for instance,
	// - a TopologyZone with type TopologyTypeGPU may have both gpu and gpu-memory
	// - a TopologyZone with type TopologyTypeNIC may have both ingress and egress bandwidth
	// +optional
	Resources Resources `json:"resources"`

	// +optional
	// +patchMergeKey=name
	// +patchStrategy=merge
	Attributes []Attribute `json:"attributes,omitempty" patchStrategy:"merge" patchMergeKey:"name"`

	// +optional
	// +patchMergeKey=consumer
	// +patchStrategy=merge
	Allocations []*Allocation `json:"allocations,omitempty" patchStrategy:"merge" patchMergeKey:"consumer"`

	// Children represents the ownerships between multiple TopologyZone; for instance,
	// - a TopologyZone with type TopologyTypeSocket may have multiple childed TopologyZone
	//   with type TopologyTypeNuma to reflect the physical connections for a node
	// - a TopologyZone with type `nic` may have multiple childed TopologyZone with type `vf`
	//   to reflect the `physical and virtual` relations between devices
	// todo: in order to bypass the lacked functionality of recursive structure definition,
	//  we need to skip validation of this field for now; will re-add this validation logic
	//  if the community supports $ref, for more information, please
	//  refer to https://github.com/kubernetes/kubernetes/issues/62872
	// +optional
	// +kubebuilder:validation:Schemaless
	// +kubebuilder:pruning:PreserveUnknownFields
	Children []*TopologyZone `json:"children,omitempty"`

	// Siblings represents the relationship between TopologyZones at the same level; for instance,
	// the distance between NUMA nodes.
	// +optional
	// +patchMergeKey=name
	// +patchStrategy=merge
	Siblings []Sibling `json:"siblings,omitempty" patchStrategy:"merge" patchMergeKey:"name"`
}

type TopologyType string

const (
	// TopologyTypeSocket indicates socket-level topology
	TopologyTypeSocket TopologyType = "Socket"

	// TopologyTypeNuma indicates numa-level topology
	TopologyTypeNuma TopologyType = "Numa"

	// TopologyTypeGPU indicates a zone for gpu device
	TopologyTypeGPU TopologyType = "GPU"

	// TopologyTypeNIC indicates a zone for network device
	TopologyTypeNIC TopologyType = "NIC"
)

type Resources struct {
	// +optional
	Allocatable *v1.ResourceList `json:"allocatable,omitempty"`

	// +optional
	Capacity *v1.ResourceList `json:"capacity,omitempty"`
}

// Attribute records the resource-specified info with name-value pairs
type Attribute struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

type Allocation struct {
	Consumer string `json:"consumer"`
	// +optional
	Requests *v1.ResourceList `json:"requests,omitempty"`
}

// Sibling describes the relationship between two Zones.
type Sibling struct {
	// Type represents the type of this Sibling.
	// For instance, Socket, Numa, GPU, NIC, Disk and so on.
	Type TopologyType `json:"type"`

	// Name represents the name of this Sibling.
	Name string `json:"name"`

	// Attributes are the attributes of the relationship between two Zones.
	// For instance, the distance between tow NUMA nodes, the connection type between two GPUs, etc.
	// +patchMergeKey=name
	// +patchStrategy=merge
	Attributes []Attribute `json:"attributes,omitempty" patchStrategy:"merge" patchMergeKey:"name"`
}

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// CustomNodeResourceList is a collection of CustomNodeResource objects.
type CustomNodeResourceList struct {
	metav1.TypeMeta `json:",inline"`

	// Standard list metadata
	// More info: https://git.k8s.io/community/contributors/devel/api-conventions.md#metadata
	// +optional
	metav1.ListMeta `json:"metadata,omitempty"`

	// items is the list of CNRs
	Items []CustomNodeResource `json:"items"`
}
