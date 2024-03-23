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
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
)

// TideNodePoolSpec defines the desired state of TideNodePool
type TideNodePoolSpec struct {
	// INSERT ADDITIONAL SPEC FIELDS - desired state of cluster
	// Important: Run "make" to regenerate code after modifying this file
	EvictStrategy EvictStrategy `json:"evictStrategy,omitempty"`
	NodeConfigs   NodeConfigs   `json:"nodeConfigs"`
}

type EvictStrategy struct {
	Type string `json:"type"`
	// +optional
	Watermark Watermark `json:"watermark,omitempty"`
}

type EvictStrategyType string

const (
	WatermarkStrategy EvictStrategyType = "watermark"
)

type Watermark struct {
	// +optional
	EvictOnlinePodTaint *TaintOption `json:"evictOnlinePodTaint,omitempty"`
	// +optional
	EvictOfflinePodTaint *TaintOption `json:"evictOfflinePodTaint,omitempty"`
}

type NodeConfigs struct {
	NodeSelector map[string]string `json:"nodeSelector,omitempty"`
	// +optional
	OnlineLabel *LabelOption `json:"onlineLabel,omitempty"`
	// +optional
	OfflineLabel *LabelOption `json:"offlineLabel,omitempty"`
	// +optional
	TideLabel *LabelOption `json:"tideLabel,omitempty"`
	// +optional
	Reserve ReserveOptions `json:"reserve,omitempty"`
}

type PodSelector struct {
	MatchLabels map[string]string `json:"matchLabels,omitempty"`
}

type ReserveOptions struct {
	// +optional
	Online *intstr.IntOrString `json:"online,omitempty"`
	// +optional
	Offline *intstr.IntOrString `json:"offline,omitempty"`
}

type LabelOption struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

type TaintOption struct {
	Key    string `json:"key"`
	Value  string `json:"value"`
	Effect string `json:"effect,omitempty"`
}

// TideNodePoolStatus defines the observed state of TideNodePool
type TideNodePoolStatus struct {
	// INSERT ADDITIONAL STATUS FIELD - define observed state of cluster
	// Important: Run "make" to regenerate code after modifying this file
	// +optional
	ReserveNodes ReserveNodes `json:"reserveNodes,omitempty"`
	// +optional
	TideNodes TideNodes `json:"tideNodes,omitempty"`
}

type ReserveNodes struct {
	OnlineNodes  []string `json:"onlineNodes,omitempty"`
	OfflineNodes []string `json:"offlineNodes,omitempty"`
}

type TideNodes struct {
	Nodes []string `json:"nodes,omitempty"`
}

// +genclient
// +genclient:nonNamespaced
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Cluster,shortName=tnp

// TideNodePool is the Schema for the tidenodepools API
type TideNodePool struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec TideNodePoolSpec `json:"spec,omitempty"`
	// +optional
	Status TideNodePoolStatus `json:"status,omitempty"`
}

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// TideNodePoolList contains a list of TideNodePool
type TideNodePoolList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []TideNodePool `json:"items"`
}
