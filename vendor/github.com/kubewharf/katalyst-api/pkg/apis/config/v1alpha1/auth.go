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

import metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
// +kubebuilder:object:root=true
// +kubebuilder:resource:path=authconfigurations,shortName=ac
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="AGE",type=date,JSONPath=.metadata.creationTimestamp
// +kubebuilder:printcolumn:name="SELECTOR",type=string,JSONPath=".spec.nodeLabelSelector"
// +kubebuilder:printcolumn:name="PRIORITY",type=string,JSONPath=".spec.priority"
// +kubebuilder:printcolumn:name="NODES",type=string,JSONPath=".spec.ephemeralSelector.nodeNames"
// +kubebuilder:printcolumn:name="DURATION",type=string,JSONPath=".spec.ephemeralSelector.lastDuration"
// +kubebuilder:printcolumn:name="VALID",type=string,JSONPath=".status.conditions[?(@.type==\"Valid\")].status"
// +kubebuilder:printcolumn:name="REASON",type=string,JSONPath=".status.conditions[?(@.type==\"Valid\")].reason"
// +kubebuilder:printcolumn:name="MESSAGE",type=string,JSONPath=".status.conditions[?(@.type==\"Valid\")].message"

// AuthConfiguration is the Schema for the configuration API used by authentication and authorization
type AuthConfiguration struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   AuthConfigurationSpec `json:"spec,omitempty"`
	Status GenericConfigStatus   `json:"status,omitempty"`
}

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
// +kubebuilder:object:root=true

// AuthConfigurationList contains a list of AuthConfiguration
type AuthConfigurationList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`

	Items []AuthConfiguration `json:"items"`
}

// AuthConfigurationSpec defines the desired state of AuthConfiguration
type AuthConfigurationSpec struct {
	GenericConfigSpec `json:",inline"`

	Config AuthConfig `json:"config,omitempty"`
}

type AuthConfig struct {
	// BasicAuthConfig is configuration related to basic access authentication
	// +optional
	BasicAuthConfig *BasicAuthConfig `json:"basicAuthConfig,omitempty"`

	// BasicAuthConfig is configuration about access control
	// +optional
	AccessControlConfig *AccessControlConfig `json:"accessControlConfig,omitempty"`
}

type BasicAuthConfig struct {
	// UserPasswordPairs is the list of valid username and corresponding password
	// +optional
	UserPasswordPairs []UserPasswordPair `json:"userPasswordPairs,omitempty"`
}

type UserPasswordPair struct {
	// +optional
	Username string `json:"username,omitempty"`
	// base64 encoded password
	// +optional
	Password string `json:"password,omitempty"`
}

type AccessControlConfig struct {
	// +optional
	AccessControlPolicies []AccessControlPolicy `json:"accessControlPolicies,omitempty"`
}

type AccessControlPolicy struct {
	// +optional
	Username string `json:"username,omitempty"`
	// +optional
	PolicyRule PolicyRule `json:"policyRule,omitempty"`
}

type PolicyRule struct {
	// +optional
	Resources []string `json:"resources,omitempty"`
}
