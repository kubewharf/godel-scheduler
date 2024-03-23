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
	"k8s.io/apimachinery/pkg/runtime"
)

// ResourceRecommendSpec defines the desired state of ResourceRecommend
type ResourceRecommendSpec struct {
	// TargetRef points to the controller managing the set of pods for the recommenders to control。
	// e.g. Deployment.
	TargetRef CrossVersionObjectReference `json:"targetRef"`

	// ResourcePolicy controls how the recommender computes recommended resources.
	ResourcePolicy ResourcePolicy `json:"resourcePolicy"`
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

// ResourcePolicy controls how computes the recommended resources
type ResourcePolicy struct {
	// policy of algorithm, if not provided, using default
	AlgorithmPolicy AlgorithmPolicy `json:"algorithmPolicy"`

	// Per-container resource recommend policies.
	// +patchMergeKey=containerName
	// +patchStrategy=merge
	ContainerPolicies []ContainerResourcePolicy `json:"containerPolicies" patchStrategy:"merge" patchMergeKey:"containerName"`
}

type AlgorithmPolicy struct {
	// Recommender specify the Recommender to recommend
	// if end-user wants to define their own recommender,
	// they should manage this field to match their recommend implementations.
	// When the value is "default", the default recommender is used.
	// Default to "default"
	// +optional
	// +kubebuilder:default:=default
	Recommender string `json:"recommender,omitempty"`

	// Algorithm specifies the recommendation algorithm used by the
	// recommender, default to "percentile"
	// +optional
	// +kubebuilder:default:=percentile
	Algorithm Algorithm `json:"algorithm,omitempty"`

	// Extensions config by key-value format.
	// +optional
	// +kubebuilder:pruning:PreserveUnknownFields
	Extensions *runtime.RawExtension `json:"extensions,omitempty"`
}

// Algorithm is the recommended algorithm
type Algorithm string

// Algorithms
const (
	// AlgorithmPercentile is percentile recommender algorithm
	AlgorithmPercentile Algorithm = "percentile"
)

// ContainerResourcePolicy provides the policy for recommender to manage the given container
// resources, including the container name, resource kind, usage buffer etc.
type ContainerResourcePolicy struct {
	// Name of the container, or uses the wildcard "*".
	// If wildcard is used, the policy will apply to all containers under the workload
	ContainerName string `json:"containerName"`

	// ControlledResourcesPolicies controls how the recommenders computes recommended resources
	// for the container. If not specified, the recommenders computes recommended resources
	// for none of the container resources.
	ControlledResourcesPolicies []ContainerControlledResourcesPolicy `json:"controlledResourcesPolicies" patchStrategy:"merge"`
}

type ContainerControlledResourcesPolicy struct {
	// ResourceName is the name of the resource that is controlled.
	ResourceName v1.ResourceName `json:"resourceName"`

	// MinAllowed Specifies the minimal amount of resources that will be recommended
	// for the container. The default is no minimum.
	// +optional
	MinAllowed *resource.Quantity `json:"minAllowed,omitempty"`

	// MaxAllowed Specifies the maximum amount of resources that will be recommended
	// for the container. The default is no maximum.
	// +optional
	MaxAllowed *resource.Quantity `json:"maxAllowed,omitempty"`

	// BufferPercent is used to get extra resource buffer for the given containers
	// +optional
	BufferPercent *int32 `json:"bufferPercent,omitempty"`

	// ControlledValues specifies which resource values should be controlled.
	// Defaults to RequestsOnly.
	// +optional
	ControlledValues *ContainerControlledValues `json:"controlledValues,omitempty"`
}

// ContainerControlledValues controls which resource value should be recommended.
type ContainerControlledValues string

const (
	// ContainerControlledValuesRequestsAndLimits means resource request and limits
	// are recommended automatically.
	ContainerControlledValuesRequestsAndLimits ContainerControlledValues = "RequestsAndLimits"
	// ContainerControlledValuesRequestsOnly means only requested resource is recommended.
	ContainerControlledValuesRequestsOnly ContainerControlledValues = "RequestsOnly"
	// ContainerControlledValuesLimitsOnly means only limited resource is recommended.
	ContainerControlledValuesLimitsOnly ContainerControlledValues = "LimitsOnly"
)

// ResourceRecommendStatus defines the observed state of ResourceRecommend
type ResourceRecommendStatus struct {
	// LastRecommendationTime is last recommendation generation time
	// +optional
	LastRecommendationTime *metav1.Time `json:"lastRecommendationTime,omitempty"`

	// RecommendResources is the last recommendation of resources computed by
	// recommenders
	// +optional
	RecommendResources *RecommendResources `json:"recommendResources,omitempty"`

	// Conditions is the set of conditions required for this recommender to recommend,
	// and indicates whether those conditions are met.
	// +optional
	// +patchMergeKey=type
	// +patchStrategy=merge
	Conditions []ResourceRecommendCondition `json:"conditions,omitempty" patchStrategy:"merge" patchMergeKey:"type"`

	// ObservedGeneration used to record the generation number when status is updated.
	ObservedGeneration int64 `json:"observedGeneration"`
}

// RecommendResources is the recommendation of resources computed by recommenders for
// the controlled pods. Respects the container resource policy if present in the spec.
type RecommendResources struct {
	// Resources recommended by the recommenders for specific pod.
	// +optional
	// +patchMergeKey=podName
	// +patchStrategy=merge
	PodRecommendations []PodResources `json:"podRecommendations,omitempty"`

	// Resources recommended by the recommenders for each container.
	// +optional
	// +patchMergeKey=containerName
	// +patchStrategy=merge
	ContainerRecommendations []ContainerResources `json:"containerRecommendations,omitempty"`
}

// PodResources is the recommendation of resources computed by recommenders for a specific pod.
// Respects the container resource policy in the spec
type PodResources struct {
	// Name of the pod.
	PodName string `json:"podName"`

	// Resources recommended by the recommenders for each container.
	// +optional
	// +patchMergeKey=containerName
	// +patchStrategy=merge
	ContainerRecommendations []ContainerResources `json:"containerRecommendations,omitempty"`
}

// ContainerResources is the recommendations of resources computed by recommenders for a specific container.
// Respects the container resource policy in spec
type ContainerResources struct {
	// Name of the container.
	ContainerName string `json:"containerName"`
	// Requests indicates the recommenders resources for requests of this container
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
	Target v1.ResourceList `json:"target"`
	// The most recent recommended resources target computed by the recommender
	// for the controlled containers, based only on actual resource usage, not taking
	// into account the ContainerResourcePolicy (UsageBuffer).
	// +optional
	UncappedTarget v1.ResourceList `json:"uncappedTarget,omitempty"`
}

// ResourceRecommendCondition describes the state of
// a ResourceRecommender at a certain point.
type ResourceRecommendCondition struct {
	// type describes the current condition
	Type ResourceRecommendConditionType `json:"type"`
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

// ResourceRecommendConditionType are the valid conditions of a ResourceRecommend.
type ResourceRecommendConditionType string

const (
	// Validated indicates if initial validation is done
	Validated ResourceRecommendConditionType = "Validated"
	// Initialized indicates if the initialization is done
	Initialized ResourceRecommendConditionType = "Initialized"
	// RecommendationProvided indicates that recommender values have been provided
	RecommendationProvided ResourceRecommendConditionType = "RecommendationProvided"
)

// +genclient
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
// +kubebuilder:subresource:status
// +kubebuilder:resource:shortName=rec

// ResourceRecommend is the Schema for the resourcerecommends API
type ResourceRecommend struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   ResourceRecommendSpec   `json:"spec"`
	Status ResourceRecommendStatus `json:"status,omitempty"`
}

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// ResourceRecommendList contains a list of ResourceRecommend
type ResourceRecommendList struct {
	metav1.TypeMeta `json:",inline"`

	// Standard list metadata
	// More info: https://git.k8s.io/community/contributors/devel/api-conventions.md#metadata
	// +optional
	metav1.ListMeta `json:"metadata,omitempty"`

	Items []ResourceRecommend `json:"items"`
}
