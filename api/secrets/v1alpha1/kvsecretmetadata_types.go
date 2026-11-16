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
// +kubebuilder:validation:XValidation:rule="self.path == oldSelf.path",message="path is immutable"

// spec is the desired per-secret metadata of a KV version 2 secret at `<mountPath>/metadata/<path>`. An omitted setting is left as OpenBao stored it. A set customMetadata map replaces the stored keys. Deleting the resource with deletionPolicy Delete removes the secret and every version.
type KVSecretMetadataSpec struct {
	commonv1alpha1.ResourceSpec `json:",inline"`

	// mountPath is the path of the KV version 2 secrets engine mount, without leading or trailing slashes. Immutable. Example: `secret`.
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=128
	// +kubebuilder:validation:Pattern=`^[A-Za-z0-9_.-]+(/[A-Za-z0-9_.-]+)*$`
	// +required
	MountPath string `json:"mountPath"`

	// path is the secret path inside the mount, without the `metadata/` prefix. Immutable. Example: `apps/billing/db`.
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=512
	// +kubebuilder:validation:Pattern=`^[^/]+(/[^/]+)*$`
	// +required
	Path string `json:"path"`

	// maxVersions is the number of versions kept for this secret (`max_versions`). Unset leaves the stored value. Example: `20`.
	// +kubebuilder:validation:Minimum=0
	// +optional
	MaxVersions *int32 `json:"maxVersions,omitempty"`

	// casRequired requires every secret write to pass a check-and-set version (`cas_required`). Unset leaves the stored value.
	// +optional
	CASRequired *bool `json:"casRequired,omitempty"`

	// metadataCASRequired requires every metadata write to pass a check-and-set version (`metadata_cas_required`). Unset leaves the stored value.
	// +optional
	MetadataCASRequired *bool `json:"metadataCASRequired,omitempty"`

	// deleteVersionAfter is how long a version of this secret is kept (`delete_version_after`): integer seconds or a duration in whole s, m or h. 0 clears the setting. Example: `768h`.
	// +kubebuilder:validation:XValidation:rule="type(self) == int ? self >= 0 : (self.matches('^[0-9]+$') || self.matches('^([0-9]+(s|m|h))+$'))",message="must be integer seconds or a duration in whole s, m or h"
	// +optional
	DeleteVersionAfter *intstr.IntOrString `json:"deleteVersionAfter,omitempty"`

	// customMetadata is version-independent information about the secret (`custom_metadata`). At most 61 entries, leaving room for the three ownership-marker keys; keys and values must be non-empty, and the key prefix `openbaoconfig-` is reserved. An omitted map is left as stored. A set map replaces the stored keys.
	// +kubebuilder:validation:MaxProperties=61
	// +kubebuilder:validation:XValidation:rule="self.all(k, size(k) > 0 && size(k) <= 128 && !k.startsWith('openbaoconfig-') && size(self[k]) > 0 && size(self[k]) <= 512)",message="customMetadata keys are 1-128 characters without the reserved prefix openbaoconfig-, values 1-512 characters"
	// +optional
	CustomMetadata map[string]string `json:"customMetadata,omitempty"`
}

// status is the observed state of the KV secret metadata.
type KVSecretMetadataStatus struct {
	commonv1alpha1.ResourceStatus `json:",inline"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:categories=openbao
// +kubebuilder:printcolumn:name="Ready",type=string,JSONPath=`.status.conditions[?(@.type=="Ready")].status`
// +kubebuilder:printcolumn:name="Synced",type=string,JSONPath=`.status.conditions[?(@.type=="Synced")].status`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// KVSecretMetadata sets the per-secret metadata of one KV version 2 secret at `<mount>/metadata/<path>`: version retention, check-and-set and custom metadata. It does not write secret values. Delete removes the secret and every version, so it requires openbaoconfig.io/allow-destructive-delete.
//
// Example:
//
//	spec:
//	  connectionRef:
//	    name: openbao
//	  mountPath: secret
//	  path: team/app
//	  maxVersions: 5
type KVSecretMetadata struct {
	metav1.TypeMeta `json:",inline"`

	// +optional
	metav1.ObjectMeta `json:"metadata,omitzero"`

	// +required
	Spec KVSecretMetadataSpec `json:"spec"`

	// +optional
	Status KVSecretMetadataStatus `json:"status,omitzero"`
}

// GetResourceSpec implements commonv1alpha1.Managed.
func (m *KVSecretMetadata) GetResourceSpec() *commonv1alpha1.ResourceSpec {
	return &m.Spec.ResourceSpec
}

// GetResourceStatus implements commonv1alpha1.Managed.
func (m *KVSecretMetadata) GetResourceStatus() *commonv1alpha1.ResourceStatus {
	return &m.Status.ResourceStatus
}

// +kubebuilder:object:root=true

// KVSecretMetadataList contains a list of KVSecretMetadata.
type KVSecretMetadataList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []KVSecretMetadata `json:"items"`
}

func init() {
	SchemeBuilder.Register(&KVSecretMetadata{}, &KVSecretMetadataList{})
}
