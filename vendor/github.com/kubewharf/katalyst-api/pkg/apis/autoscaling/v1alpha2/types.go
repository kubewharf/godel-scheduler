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

package v1alpha2

import (
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	recommendationapi "github.com/kubewharf/katalyst-api/pkg/apis/recommendation/v1alpha1"
)

// +genclient
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
// +kubebuilder:subresource:status
// +kubebuilder:storageversion
// +kubebuilder:resource:shortName=kvpa

// KatalystVerticalPodAutoscaler captures information about a VPA object
type KatalystVerticalPodAutoscaler struct {
	metav1.TypeMeta `json:",inline"`
	// +optional
	metav1.ObjectMeta `json:"metadata,omitempty"`

	// Spec defines the behavior of a KatalystVerticalPodAutoscaler.
	// +optional
	Spec KatalystVerticalPodAutoscalerSpec `json:"spec,omitempty"`

	// Status represents the current information about a KatalystVerticalPodAutoscaler.
	// +optional
	Status KatalystVerticalPodAutoscalerStatus `json:"status,omitempty"`
}

// KatalystVerticalPodAutoscalerSpec is the specification of the behavior of the autoscaler.
type KatalystVerticalPodAutoscalerSpec struct {
	// TargetRef points to the controller managing the set of pods for the
	// autoscaler to control - e.g. Deployment, StatefulSet.
	TargetRef recommendationapi.CrossVersionObjectReference `json:"targetRef"`

	// Controls how the autoscaler computes recommended resources.
	// The resource policy may be used to set constraints on the recommendations
	// for individual containers. If not specified, the autoscaler computes recommended
	// resources for all containers in the pod, without additional constraints.
	// +optional
	ResourcePolicy recommendationapi.ResourcePolicy `json:"resourcePolicy,omitempty"`

	// Describes the rules on how changes are applied to the pods.
	// If not specified, all fields in the `PodUpdatePolicy` are set to their default values.
	// +optional
	UpdatePolicy PodUpdatePolicy `json:"updatePolicy,omitempty"`
}

// KatalystVerticalPodAutoscalerStatus describes the runtime state of the autoscaler.
type KatalystVerticalPodAutoscalerStatus struct {
	// RecommendResources is the last recommendation of resources computed by
	// recommenders
	// +optional
	RecommendResources recommendationapi.RecommendResources `json:"recommendResources,omitempty"`

	// Conditions is the set of conditions required for this autoscaler to scale its target,
	// and indicates whether those conditions are met.
	// +optional
	// +patchMergeKey=type
	// +patchStrategy=merge
	Conditions []VerticalPodAutoscalerCondition `json:"conditions,omitempty" patchStrategy:"merge" patchMergeKey:"type"`
}

// PodUpdatePolicy describes the rules on how changes are applied to the pods.
type PodUpdatePolicy struct {
	// PodUpdatingStrategy controls the method autoscaler applies changes to the pod resources.
	// The default is 'Off'.
	// +kubebuilder:default:=Off
	PodUpdatingStrategy PodUpdatingStrategy `json:"podUpdatingStrategy,omitempty"`

	// PodMatchingStrategy controls the timing autoscaler applies changes to the pod resources.
	// The default is 'All'.
	// +kubebuilder:default:=All
	PodMatchingStrategy PodMatchingStrategy `json:"podMatchingStrategy,omitempty"`

	// PodApplyStrategy controls the applying scope autoscaler should obey to.
	// The default is 'Group'.
	// +kubebuilder:default:=Group
	PodApplyStrategy PodApplyStrategy `json:"podApplyStrategy,omitempty"`
}

// PodUpdatingStrategy controls the method autoscaler applies changes to the pod resources.
// +kubebuilder:validation:Enum=Off;Recreate;Inplace;BestEffortInplace
type PodUpdatingStrategy string

const (
	// PodUpdatingStrategyOff means that autoscaler never changes Pod resources.
	// The provider still sets the resources in the KatalystVerticalPodAutoscaler object.
	// This can be used for a "dry run".
	PodUpdatingStrategyOff PodUpdatingStrategy = "Off"

	// PodUpdatingStrategyRecreate means that autoscaler assigns resources on pod
	// creation and additionally can update them during the lifetime of the
	// pod by always deleting and recreating the pod.
	PodUpdatingStrategyRecreate PodUpdatingStrategy = "Recreate"

	// PodUpdatingStrategyInplace means that autoscaler assigns resources only
	// during the lifetime of pod without re-creating the pod
	PodUpdatingStrategyInplace PodUpdatingStrategy = "Inplace"

	// PodUpdatingStrategyBestEffortInplace means that autoscaler will try to inplace
	// update resources, and fallback to recreate if not available.
	PodUpdatingStrategyBestEffortInplace PodUpdatingStrategy = "BestEffortInplace"
)

// PodMatchingStrategy controls the timing autoscaler applies changes to the pod resources.
// +kubebuilder:validation:Enum=PodMatchingStrategyForHistoricalPod;PodMatchingStrategyForFreshPod;All
type PodMatchingStrategy string

const (
	// PodMatchingStrategyForHistoricalPod means that autoscaler will only apply resources
	// for pods that are created before this VPA CR.
	PodMatchingStrategyForHistoricalPod PodMatchingStrategy = "ForHistoricalPod"
	// PodMatchingStrategyForFreshPod means that autoscaler assigns resources on pod
	// creation and also change them during the lifetime of the pod. Pods that are created before
	// this VPA CR won't be changed.
	PodMatchingStrategyForFreshPod PodMatchingStrategy = "ForFreshPod"
	// PodMatchingStrategyAll means that autoscaler will reconcile and update all pods
	// that match up with the target reference.
	PodMatchingStrategyAll PodMatchingStrategy = "All"
)

// PodApplyStrategy controls the applying scope autoscaler should obey to.
// +kubebuilder:validation:Enum=Pod;Group
type PodApplyStrategy string

const (
	// PodApplyStrategyStrategyPod means that autoscaler consider each pod as individual one,
	// the applied status (successes or failed) would affect each other pod.
	PodApplyStrategyStrategyPod PodApplyStrategy = "Pod"
	// PodApplyStrategyStrategyGroup means that autoscaler must make sure the recommendation for pods
	// can be applied before it performs updating actions.
	PodApplyStrategyStrategyGroup PodApplyStrategy = "Group"
)

// VerticalPodAutoscalerConditionType are the valid conditions of
// a KatalystVerticalPodAutoscaler.
type VerticalPodAutoscalerConditionType string

const (
	// NoPodsMatched indicates that target reference used with VPA object didn't match any pods.
	NoPodsMatched VerticalPodAutoscalerConditionType = "NoPodsMatched"
	// RecommendationUpdated indicates that the recommendation was written in Pod Annotations
	RecommendationUpdated VerticalPodAutoscalerConditionType = "RecommendationUpdated"
	// RecommendationApplied indicates that the recommendation was finally applied in Pod
	RecommendationApplied VerticalPodAutoscalerConditionType = "RecommendationApplied"
)

// VerticalPodAutoscalerCondition describes the state of
// a KatalystVerticalPodAutoscaler at a certain point.
type VerticalPodAutoscalerCondition struct {
	// type describes the current condition
	Type VerticalPodAutoscalerConditionType `json:"type"`
	// status is the status of the condition (True, False, Unknown)
	Status v1.ConditionStatus `json:"status"`
	// lastTransitionTime is the last time the condition transitioned from
	// one status to another
	// +optional
	LastTransitionTime metav1.Time `json:"lastTransitionTime,omitempty"`
	// reason is the reason for the condition's last transition.
	// +optional
	Reason string `json:"reason,omitempty"`
	// message is a human-readable explanation containing details about
	// the transition
	// +optional
	Message string `json:"message,omitempty"`
}

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// KatalystVerticalPodAutoscalerList is a collection of KatalystVerticalPodAutoscaler objects.
type KatalystVerticalPodAutoscalerList struct {
	metav1.TypeMeta `json:",inline"`

	// Standard list metadata
	// More info: https://git.k8s.io/community/contributors/devel/api-conventions.md#metadata
	// +optional
	metav1.ListMeta `json:"metadata,omitempty"`

	// items is the list of KatalystVerticalPodAutoscalers
	Items []KatalystVerticalPodAutoscaler `json:"items"`
}
