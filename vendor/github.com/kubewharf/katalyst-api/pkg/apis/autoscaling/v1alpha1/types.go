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
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

// +genclient
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
// +kubebuilder:subresource:status
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
	TargetRef CrossVersionObjectReference `json:"targetRef"`

	// Describes the rules on how changes are applied to the pods.
	// If not specified, all fields in the `PodUpdatePolicy` are set to their default values.
	// +optional
	UpdatePolicy PodUpdatePolicy `json:"updatePolicy,omitempty"`

	// Controls how the autoscaler computes recommended resources.
	// The resource policy may be used to set constraints on the recommendations
	// for individual containers. If not specified, the autoscaler computes recommended
	// resources for all containers in the pod, without additional constraints.
	// +optional
	ResourcePolicy PodResourcePolicy `json:"resourcePolicy,omitempty"`
}

// KatalystVerticalPodAutoscalerStatus describes the runtime state of the autoscaler.
type KatalystVerticalPodAutoscalerStatus struct {
	// The most recently computed amount of resources provided by the
	// autoscaler for the controlled pods.
	// +optional
	PodResources []PodResources `json:"podResources,omitempty"`

	// ContainerResources is the most recently computed amount of resources for the controlled containers
	// it will be overwritten by PodResources
	// +optional
	ContainerResources []ContainerResources `json:"containerResources,omitempty"`

	// Conditions is the set of conditions required for this autoscaler to scale its target,
	// and indicates whether those conditions are met.
	// +optional
	// +patchMergeKey=type
	// +patchStrategy=merge
	Conditions []VerticalPodAutoscalerCondition `json:"conditions,omitempty" patchStrategy:"merge" patchMergeKey:"type"`
}

// CrossVersionObjectReference contains enough information to let you identify the referred resource.
// +structType=atomic
type CrossVersionObjectReference struct {
	// Kind of the referent
	Kind string `json:"kind"`
	// Name of the referent
	Name string `json:"name"`
	// API version of the referent
	// +optional
	APIVersion string `json:"apiVersion,omitempty"`
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

// PodResourcePolicy controls how autoscaler computes the recommended resources
// for containers belonging to the pod. There can be at most one entry for every
// named container.
type PodResourcePolicy struct {
	// policy of algorithm, if no algorithm is provided, using default
	// in-tree recommendation logic instead.
	// +optional
	AlgorithmPolicy AlgorithmPolicy `json:"algorithmPolicy,omitempty"`

	// Per-container resource policies.
	// +optional
	// +patchMergeKey=containerName
	// +patchStrategy=merge
	ContainerPolicies []ContainerResourcePolicy `json:"containerPolicies,omitempty" patchStrategy:"merge" patchMergeKey:"containerName"`
}

type AlgorithmPolicy struct {
	// Recommenders of the Algorithm used to this Pod;
	// if end-user wants to define their own recommendation algorithms,
	// they should manage this field to match their recommend implementations.
	// +optional
	Recommender string `json:"recommender,omitempty"`

	// Granularity of the Algorithm server recommendation result
	// The default is 'Default'.
	// +kubebuilder:default:=Default
	Granularity AlgorithmGranularity `json:"granularity"`

	// Extensions config by key-value format.
	// +optional
	// +kubebuilder:pruning:PreserveUnknownFields
	Extensions *runtime.RawExtension `json:"extensions,omitempty"`
}

// ContainerResourcePolicy controls how autoscaler computes the recommended
// resources for a specific container.
type ContainerResourcePolicy struct {
	// Name of the container or DefaultContainerResourcePolicy, in which
	// case the policy is used by the containers that don't have their own
	// policy specified.
	ContainerName *string `json:"containerName"`

	// Specifies the minimal amount of resources that will be recommended
	// for the container. The default is no minimum.
	// +optional
	MinAllowed v1.ResourceList `json:"minAllowed,omitempty"`

	// Specifies the maximum amount of resources that will be recommended
	// for the container. The default is no maximum.
	// +optional
	MaxAllowed v1.ResourceList `json:"maxAllowed,omitempty"`

	// Specifies the type of recommendations that will be computed
	// (and possibly applied) by VPA.
	// If not specified, the default of [ResourceCPU] will be used.
	// +kubebuilder:default:={cpu}
	ControlledResources []v1.ResourceName `json:"controlledResources,omitempty" patchStrategy:"merge"`

	// Specifies which resource values should be controlled.
	// The default is "RequestsAndLimits".
	// +kubebuilder:default:=RequestsAndLimits
	ControlledValues ContainerControlledValues `json:"controlledValues"`

	// ResourceResizePolicy specifies how Kubernetes should handle resource resize.
	// The default is "None"
	// +kubebuilder:default:=None
	ResourceResizePolicy ResourceResizePolicy `json:"resourceResizePolicy"`
}

// AlgorithmGranularity decide which granularity of recommended result
// +kubebuilder:validation:Enum=Default;Pod
type AlgorithmGranularity string

const (
	// AlgorithmGranularityDefault means every pod has the same recommendation
	AlgorithmGranularityDefault AlgorithmGranularity = "Default"
	// AlgorithmGranularityPod means different pods have different recommendation
	AlgorithmGranularityPod AlgorithmGranularity = "Pod"
)

// ContainerControlledValues controls which resource value should be autoscaled.
// +kubebuilder:validation:Enum=RequestsAndLimits;RequestsOnly;LimitsOnly
type ContainerControlledValues string

const (
	// ContainerControlledValuesRequestsAndLimits means resource request and limits
	// are scaled automatically. The limit is scaled proportionally to the request.
	ContainerControlledValuesRequestsAndLimits ContainerControlledValues = "RequestsAndLimits"
	// ContainerControlledValuesRequestsOnly means only requested resource is autoscaled.
	ContainerControlledValuesRequestsOnly ContainerControlledValues = "RequestsOnly"
	// ContainerControlledValuesLimitsOnly means only requested resource is autoscaled.
	ContainerControlledValuesLimitsOnly ContainerControlledValues = "LimitsOnly"
)

// ResourceResizePolicy specifies how Kubernetes should handle resource resize.
type ResourceResizePolicy string

// These are the valid resource resize policy values:
const (
	// ResourceResizePolicyRestart tells Kubernetes to resize the container in-place
	// without restarting it, if possible. Kubernetes may however choose to
	// restart the container if it is unable to actuate resize without a
	// restart. For e.g. the runtime doesn't support restart-free resizing.
	ResourceResizePolicyRestart ResourceResizePolicy = "Restart"
	// ResourceResizePolicyNone tells Kubernetes to resize the container in-place
	// by stopping and starting the container when new resources are applied.
	// This is needed for legacy applications. For e.g. java apps using the
	// -xmxN flag which are unable to use resized memory without restarting.
	ResourceResizePolicyNone ResourceResizePolicy = "None"
)

type PodResources struct {
	// Name of the pod.
	PodName *string `json:"podName,omitempty"`
	// Resources recommended by the autoscaler for each container.
	ContainerResources []ContainerResources `json:"containerRecommendations,omitempty"`
}

// ContainerResources is the recommendation of resources computed by
// autoscaler for a specific container. Respects the container resource policy
// if present in the spec. In particular the recommendation is not produced for
// containers with `ContainerScalingMode` set to 'Off'.
type ContainerResources struct {
	// Name of the container.
	ContainerName *string `json:"containerName,omitempty"`
	// Requests indicates the recommendation resources for requests of this container
	// +optional
	Requests *ContainerResourceList `json:"requests,omitempty"`
	// Limits indicates the recommendation resources for limits of this container
	// +optional
	Limits *ContainerResourceList `json:"limits,omitempty"`
}

// ContainerResourceList is used to represent the resourceLists
type ContainerResourceList struct {
	// Current indicates the real resource configuration from the view of CRI interface.
	// +optional
	Current v1.ResourceList `json:"current,omitempty"`
	// Recommended amount of resources. Observes ContainerResourcePolicy.
	// +optional
	Target v1.ResourceList `json:"target,omitempty"`
	// The most recent recommended resources target computed by the autoscaler
	// for the controlled pods, based only on actual resource usage, not taking
	// into account the ContainerResourcePolicy.
	// May differ from the Recommendation if the actual resource usage causes
	// the target to violate the ContainerResourcePolicy (lower than MinAllowed
	// or higher that MaxAllowed).
	// Used only as status indication, will not affect actual resource assignment.
	// +optional
	UncappedTarget v1.ResourceList `json:"uncappedTarget,omitempty"`
	// Minimum recommended amount of resources. Observes ContainerResourcePolicy.
	// This amount is not guaranteed to be sufficient for the application to operate in a stable way, however
	// running with less resources is likely to have significant impact on performance/availability.
	// +optional
	LowerBound v1.ResourceList `json:"lowerBound,omitempty"`
	// Maximum recommended amount of resources. Observes ContainerResourcePolicy.
	// Any resources allocated beyond this value are likely wasted. This value may be larger than the maximum
	// amount of application is actually capable of consuming.
	// +optional
	UpperBound v1.ResourceList `json:"upperBound,omitempty"`
}

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

// +genclient
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
// +kubebuilder:subresource:status
// +kubebuilder:resource:shortName=vparec

// VerticalPodAutoscalerRecommendation captures information about a VPA Recommendation
type VerticalPodAutoscalerRecommendation struct {
	metav1.TypeMeta `json:",inline"`
	// +optional
	metav1.ObjectMeta `json:"metadata,omitempty"`

	// Spec defines the behavior of a VerticalPodAutoscalerRecommendation.
	// +optional
	Spec VerticalPodAutoscalerRecommendationSpec `json:"spec,omitempty"`

	// Status represents the current information about a VerticalPodAutoscalerRecommendation.
	// +optional
	Status VerticalPodAutoscalerRecommendationStatus `json:"status,omitempty"`
}

// VerticalPodAutoscalerRecommendationSpec is the specification of the behavior of the autoscaler.
// OwnerReference is needed to point to corresponding VPA CRD
type VerticalPodAutoscalerRecommendationSpec struct {
	// The most recently computed amount of resources recommended by the
	// autoscaler for the controlled pods.
	// +optional
	PodRecommendations []RecommendedPodResources `json:"podRecommendations,omitempty"`
	// default container resources recommended by the
	// autoscaler for the controlled containers.
	// It will be overwritten by PodRecommendations
	// +optional
	ContainerRecommendations []RecommendedContainerResources `json:"containerRecommendations,omitempty"`
}

// VerticalPodAutoscalerRecommendationStatus is the recommendation of resources computed by
type VerticalPodAutoscalerRecommendationStatus struct {
	// PodRecommendations is the most recently recommendation handled by the controller.
	// +optional
	PodRecommendations []RecommendedPodResources `json:"podRecommendations,omitempty"`

	// ContainerRecommendations is the most recently defaultRecommendation handled by the controller
	// +optional
	ContainerRecommendations []RecommendedContainerResources `json:"containerRecommendations,omitempty"`

	// Conditions is the set of conditions required for this vparec to scale its target,
	// and indicates whether those conditions are met.
	// +optional
	// +patchMergeKey=type
	// +patchStrategy=merge
	Conditions []VerticalPodAutoscalerRecommendationCondition `json:"conditions,omitempty" patchStrategy:"merge" patchMergeKey:"type"`
}

type RecommendedPodResources struct {
	// Name of the pod.
	PodName *string `json:"podName"`
	// Resources recommended by the autoscaler for each container.
	// +optional
	ContainerRecommendations []RecommendedContainerResources `json:"containerRecommendations,omitempty"`
}

// RecommendedContainerResources is the recommendation of resources computed by
// autoscaler for a specific container. Respects the container resource policy
// if present in the spec. In particular the recommendation is not produced for
// containers with `ContainerScalingMode` set to 'Off'.
type RecommendedContainerResources struct {
	// Name of the container.
	ContainerName *string `json:"containerName"`
	// Requests indicates the recommendation resources for requests of this container
	// +optional
	Requests *RecommendedRequestResources `json:"requests,omitempty"`
	// Limits indicates the recommendation resources for limits of this container
	// +optional
	Limits *RecommendedRequestResources `json:"limits,omitempty"`
}

// RecommendedRequestResources is used to represent the resourceLists
type RecommendedRequestResources struct {
	// Resources indicates the recommended resources in quantity format.
	Resources v1.ResourceList `json:"resources,omitempty"`
}

// VerticalPodAutoscalerRecommendationConditionType are the valid conditions of
// a VerticalPodAutoscalerRecommendation.
type VerticalPodAutoscalerRecommendationConditionType string

const (
	// NoVPAMatched indicates that vparec didn't match any vpa.
	NoVPAMatched VerticalPodAutoscalerRecommendationConditionType = "NoVPAMatched"
	// RecommendationUpdatedToVPA indicates that the recommendation was written in VPA status
	RecommendationUpdatedToVPA VerticalPodAutoscalerRecommendationConditionType = "RecommendationUpdatedToVPA"
)

// VerticalPodAutoscalerRecommendationCondition describes the state of
// a VerticalPodAutoscalerRecommendation at a certain point.
type VerticalPodAutoscalerRecommendationCondition struct {
	// type describes the current condition
	Type VerticalPodAutoscalerRecommendationConditionType `json:"type"`
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

// KatalystVerticalPodAutoscalerList is a collection of CNR objects.
type KatalystVerticalPodAutoscalerList struct {
	metav1.TypeMeta `json:",inline"`

	// Standard list metadata
	// More info: https://git.k8s.io/community/contributors/devel/api-conventions.md#metadata
	// +optional
	metav1.ListMeta `json:"metadata,omitempty"`

	// items is the list of CNRs
	Items []KatalystVerticalPodAutoscaler `json:"items"`
}

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// VerticalPodAutoscalerRecommendationList is a collection of CNR objects.
type VerticalPodAutoscalerRecommendationList struct {
	metav1.TypeMeta `json:",inline"`

	// Standard list metadata
	// More info: https://git.k8s.io/community/contributors/devel/api-conventions.md#metadata
	// +optional
	metav1.ListMeta `json:"metadata,omitempty"`

	// items is the list of CNRs
	Items []VerticalPodAutoscalerRecommendation `json:"items"`
}
