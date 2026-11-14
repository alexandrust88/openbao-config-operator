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
	"k8s.io/apimachinery/pkg/util/intstr"

	commonv1alpha1 "github.com/alexandrust88/openbao-config-operator/api/common/v1alpha1"
)

// +kubebuilder:validation:XValidation:rule="self.mountPath == oldSelf.mountPath",message="mountPath is immutable"
// +kubebuilder:validation:XValidation:rule="!has(self.deletionPolicy) || self.deletionPolicy == 'Orphan'",message="the KV configuration cannot be deleted; deletionPolicy must be Orphan"

// spec is the desired configuration of a KV version 2 secrets engine at `<mountPath>/config`. An omitted setting is reset to the server default. deletionPolicy must be Orphan.
type KVConfigSpec struct {
	commonv1alpha1.ResourceSpec `json:",inline"`

	// mountPath is the path of the KV version 2 secrets engine mount, without leading or trailing slashes. Immutable. Example: `secret`.
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=128
	// +kubebuilder:validation:Pattern=`^[A-Za-z0-9_.-]+(/[A-Za-z0-9_.-]+)*$`
	// +required
	MountPath string `json:"mountPath"`

	// maxVersions is the number of versions kept for each secret (`max_versions`); 0 means the server default of 10. Example: `20`.
	// +kubebuilder:validation:Minimum=0
	// +optional
	MaxVersions *int32 `json:"maxVersions,omitempty"`

	// casRequired requires every secret write to pass a check-and-set version (`cas_required`). Defaults to false.
	// +optional
	CASRequired *bool `json:"casRequired,omitempty"`

	// metadataCASRequired requires every metadata write to pass a check-and-set version (`metadata_cas_required`, OpenBao only). Defaults to false.
	// +optional
	MetadataCASRequired *bool `json:"metadataCASRequired,omitempty"`

	// deleteVersionAfter is how long a version is kept before it is deleted (`delete_version_after`): integer seconds or a duration in whole s, m or h. 0 clears the setting; -1 disables deletion on every secret of the mount. Example: `768h`.
	// +kubebuilder:validation:XValidation:rule="type(self) == int ? self >= -1 : (self == '-1' || self.matches('^[0-9]+$') || self.matches('^([0-9]+(s|m|h))+$'))",message="must be -1, integer seconds or a duration in whole s, m or h"
	// +optional
	DeleteVersionAfter *intstr.IntOrString `json:"deleteVersionAfter,omitempty"`
}

// status is the observed state of the KV configuration.
type KVConfigStatus struct {
	commonv1alpha1.ResourceStatus `json:",inline"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:categories=openbao
// +kubebuilder:printcolumn:name="Ready",type=string,JSONPath=`.status.conditions[?(@.type=="Ready")].status`
// +kubebuilder:printcolumn:name="Synced",type=string,JSONPath=`.status.conditions[?(@.type=="Synced")].status`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// KVConfig sets the engine-wide configuration of a KV version 2 secrets engine at `<mount>/config`: version retention and check-and-set enforcement. A singleton per mount; it is never deleted.
//
// Example:
//
//	spec:
//	  connectionRef:
//	    name: openbao
//	  mountPath: secret
//	  maxVersions: 20
//	  casRequired: true
//	  deleteVersionAfter: 768h
type KVConfig struct {
	metav1.TypeMeta `json:",inline"`

	// +optional
	metav1.ObjectMeta `json:"metadata,omitzero"`

	// +required
	Spec KVConfigSpec `json:"spec"`

	// +optional
	Status KVConfigStatus `json:"status,omitzero"`
}

// GetResourceSpec implements commonv1alpha1.Managed.
func (c *KVConfig) GetResourceSpec() *commonv1alpha1.ResourceSpec { return &c.Spec.ResourceSpec }

// GetResourceStatus implements commonv1alpha1.Managed.
func (c *KVConfig) GetResourceStatus() *commonv1alpha1.ResourceStatus {
	return &c.Status.ResourceStatus
}

// +kubebuilder:object:root=true

// KVConfigList contains a list of KVConfig.
type KVConfigList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []KVConfig `json:"items"`
}

func init() {
	SchemeBuilder.Register(&KVConfig{}, &KVConfigList{})
}
