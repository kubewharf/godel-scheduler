package v1alpha1

import (
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// +genclient
// +genclient:nonNamespaced
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
// +kubebuilder:resource:scope=Cluster

// NMNode is the struct created for NodeManager to report and store node info
type NMNode struct {
	metav1.TypeMeta `json:",inline"`
	// +optional
	metav1.ObjectMeta `json:"metadata,omitempty"`

	// Spec defines the behavior of a NMNode.
	// +optional
	Spec NMNodeSpec `json:"spec,omitempty"`

	// Status represents the current information about a NMNode. This data may not be up
	// to date.
	// +optional
	Status NMNodeStatus `json:"status,omitempty"`
}

type NMNodeSpec struct {
	// If specified, the nmnode's taints.
	// TODO:should we move this to NMNode Status ?
	// +optional
	Taints []v1.Taint `json:"taints,omitempty"`
}

type NMNodeStatus struct {
	// store the resource info reported by RM(NM)
	// +optional
	ResourceCapacity *v1.ResourceList `json:"resourceCapacity,omitempty"`
	// +optional
	ResourceAllocatable *v1.ResourceList `json:"resourceAllocatable,omitempty"`
	// node status from Yarn perspective
	// +optional
	NodeStatus NodePhase `json:"nodeStatus,omitempty"`
	// +optional
	NodeCondition []*v1.NodeCondition `json:"nodeCondition,omitempty"`

	// machine status
	// +optional
	MachineStatus *MachineStatus `json:"machineStatus,omitempty"`
}

type NodePhase string

type MachineStatus struct {
	// +optional
	LoadAvgLastM *resource.Quantity `json:"loadAvgLastM,omitempty"`
}

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// NMNodeList is a collection of NMNode objects.
type NMNodeList struct {
	metav1.TypeMeta `json:",inline"`

	// Standard list metadata
	// More info: https://git.k8s.io/community/contributors/devel/api-conventions.md#metadata
	// +optional
	metav1.ListMeta `json:"metadata,omitempty"`

	// items is the list of NMNode
	Items []NMNode `json:"items"`
}
