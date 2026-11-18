/*
Copyright 2026.

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

	commonv1alpha1 "github.com/alexandrust88/openbao-config-operator/api/common/v1alpha1"
)

// +kubebuilder:validation:XValidation:rule="!has(self.interval) || self.interval == '0' || duration(self.interval) == duration('0s') || duration(self.interval) >= duration('24h')",message="interval must be 0 (disabled) or at least 24h"

// KeyRotationConfigSpec configures the automatic rotation of the barrier encryption key at sys/rotate/config. This is the key that encrypts OpenBao storage, not a secret rotation. It applies to the root namespace or to a sealable namespace. A field that is not set is kept at its OpenBao default. Deleting the resource with deletionPolicy Delete restores the OpenBao defaults (enabled, maximum operations, no interval).
type KeyRotationConfigSpec struct {
	commonv1alpha1.ResourceSpec `json:",inline"`

	// Turns automatic barrier key rotation on or off. Maps to `enabled`. OpenBao default: true. Example: true
	// +optional
	Enabled *bool `json:"enabled,omitempty"`

	// Number of encryption operations after which the barrier key is rotated. Maps to `max_operations`. Range 1000000 to 3865470566; OpenBao default: 3865470566. Example: 1000000000
	// +kubebuilder:validation:Minimum=1000000
	// +kubebuilder:validation:Maximum=3865470566
	// +optional
	MaxOperations int64 `json:"maxOperations,omitempty"`

	// Time after which the barrier key is rotated, as a duration. Maps to `interval`. "0" turns time-based rotation off (the OpenBao default); any other value must be at least 24h. Example: 720h
	// +kubebuilder:validation:MaxLength=32
	// +kubebuilder:validation:Pattern=`^(0|([0-9]+(ns|us|ms|s|m|h))+)$`
	// +optional
	Interval string `json:"interval,omitempty"`
}

// KeyRotationConfigStatus is the observed state of a KeyRotationConfig.
type KeyRotationConfigStatus struct {
	commonv1alpha1.ResourceStatus `json:",inline"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:categories=obco;obco-sys
// +kubebuilder:printcolumn:name="Ready",type=string,JSONPath=`.status.conditions[?(@.type=="Ready")].status`
// +kubebuilder:printcolumn:name="Synced",type=string,JSONPath=`.status.conditions[?(@.type=="Synced")].status`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// KeyRotationConfig sets the automatic rotation of the barrier encryption key (the key that encrypts OpenBao storage, not a secret) at sys/rotate/config, a singleton of the root namespace or a sealable namespace. The setting always exists, so use creationPolicy Adopt or CreateOrAdopt; only the fields set are written. The default deletionPolicy Orphan keeps the setting; Delete restores the OpenBao defaults.
//
// Example:
//
//	spec:
//	  connectionRef:
//	    kind: ClusterBaoConnection
//	    name: openbao
//	  creationPolicy: CreateOrAdopt
//	  enabled: true
//	  interval: 720h
type KeyRotationConfig struct {
	metav1.TypeMeta `json:",inline"`

	// +optional
	metav1.ObjectMeta `json:"metadata,omitzero"`

	// Desired barrier key rotation settings.
	// +required
	Spec KeyRotationConfigSpec `json:"spec"`

	// Observed state reported by the operator.
	// +optional
	Status KeyRotationConfigStatus `json:"status,omitzero"`
}

// GetResourceSpec implements commonv1alpha1.Managed.
func (k *KeyRotationConfig) GetResourceSpec() *commonv1alpha1.ResourceSpec {
	return &k.Spec.ResourceSpec
}

// GetResourceStatus implements commonv1alpha1.Managed.
func (k *KeyRotationConfig) GetResourceStatus() *commonv1alpha1.ResourceStatus {
	return &k.Status.ResourceStatus
}

// +kubebuilder:object:root=true

// KeyRotationConfigList contains a list of KeyRotationConfig.
type KeyRotationConfigList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []KeyRotationConfig `json:"items"`
}

func init() {
	SchemeBuilder.Register(&KeyRotationConfig{}, &KeyRotationConfigList{})
}
