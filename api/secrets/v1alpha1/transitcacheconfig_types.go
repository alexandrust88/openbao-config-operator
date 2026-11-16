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

// +kubebuilder:validation:XValidation:rule="self.mountPath == oldSelf.mountPath",message="mountPath is immutable"
// +kubebuilder:validation:XValidation:rule="!has(self.deletionPolicy) || self.deletionPolicy == 'Orphan'",message="the transit cache configuration cannot be deleted; deletionPolicy must be Orphan"
// +kubebuilder:validation:XValidation:rule="!has(self.size) || self.size == 0 || self.size >= 10",message="size must be 0 or at least 10"

// spec is the desired configuration of a Transit secrets engine cache at `<mountPath>/cache-config`. An omitted setting is reset to the server default. deletionPolicy must be Orphan.
type TransitCacheConfigSpec struct {
	commonv1alpha1.ResourceSpec `json:",inline"`

	// mountPath is the path of the Transit secrets engine mount, without leading or trailing slashes. Immutable. Example: `transit`.
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=128
	// +kubebuilder:validation:Pattern=`^[A-Za-z0-9_.-]+(/[A-Za-z0-9_.-]+)*$`
	// +required
	MountPath string `json:"mountPath"`

	// size is the number of cache entries (`size`). 0 means an unlimited cache, which is the server default. When set and non-zero, must be at least 10. Example: `1000`.
	// +kubebuilder:validation:Minimum=0
	// +kubebuilder:validation:Maximum=1000000
	// +optional
	Size *int32 `json:"size,omitempty"`
}

// status is the observed state of the Transit cache configuration.
type TransitCacheConfigStatus struct {
	commonv1alpha1.ResourceStatus `json:",inline"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:categories=openbao
// +kubebuilder:printcolumn:name="Ready",type=string,JSONPath=`.status.conditions[?(@.type=="Ready")].status`
// +kubebuilder:printcolumn:name="Synced",type=string,JSONPath=`.status.conditions[?(@.type=="Synced")].status`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// TransitCacheConfig sets the encryption-cache configuration of a Transit secrets engine at `<mount>/cache-config`.
//
// Example:
//
//	spec:
//	  connectionRef:
//	    name: openbao
//	  mountPath: transit
//	  size: 0
type TransitCacheConfig struct {
	metav1.TypeMeta `json:",inline"`

	// +optional
	metav1.ObjectMeta `json:"metadata,omitzero"`

	// +required
	Spec TransitCacheConfigSpec `json:"spec"`

	// +optional
	Status TransitCacheConfigStatus `json:"status,omitzero"`
}

// GetResourceSpec implements commonv1alpha1.Managed.
func (c *TransitCacheConfig) GetResourceSpec() *commonv1alpha1.ResourceSpec {
	return &c.Spec.ResourceSpec
}

// GetResourceStatus implements commonv1alpha1.Managed.
func (c *TransitCacheConfig) GetResourceStatus() *commonv1alpha1.ResourceStatus {
	return &c.Status.ResourceStatus
}

// +kubebuilder:object:root=true

// TransitCacheConfigList contains a list of TransitCacheConfig.
type TransitCacheConfigList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []TransitCacheConfig `json:"items"`
}

func init() {
	SchemeBuilder.Register(&TransitCacheConfig{}, &TransitCacheConfigList{})
}
