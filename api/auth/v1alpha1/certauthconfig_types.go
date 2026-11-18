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

// +kubebuilder:validation:XValidation:rule="has(self.mountPath) == has(oldSelf.mountPath) && (!has(self.mountPath) || self.mountPath == oldSelf.mountPath)",message="mountPath is immutable"
// +kubebuilder:validation:XValidation:rule="!has(self.deletionPolicy) || self.deletionPolicy == 'Orphan'",message="the cert auth config cannot be deleted; deletionPolicy must be Orphan"
// +kubebuilder:validation:XValidation:rule="!has(self.creationPolicy) || self.creationPolicy != 'Create'",message="the cert auth config always exists; set creationPolicy to Adopt or CreateOrAdopt"

// Desired state of the cert auth method configuration at auth/<mountPath>/config. creationPolicy must be Adopt or CreateOrAdopt and deletionPolicy must be Orphan.
type CertAuthConfigSpec struct {
	commonv1alpha1.ResourceSpec `json:",inline"`

	// Path of the cert auth method, without the auth/ prefix. Defaults to cert. Immutable. Example: cert
	// +kubebuilder:default=cert
	// +kubebuilder:validation:MaxLength=128
	// +kubebuilder:validation:Pattern=`^[A-Za-z0-9_.-]+(/[A-Za-z0-9_.-]+)*$`
	// +optional
	MountPath string `json:"mountPath,omitempty"`

	// When true, token renewal skips checking that the presented client certificate matches the one used at login. Defaults to false. Maps to `disable_binding`. Example: true
	// +optional
	DisableBinding *bool `json:"disableBinding,omitempty"`

	// When true, certificate metadata, including the role's allowedMetadataExtensions, is stored in the entity alias. Defaults to false. Maps to `enable_identity_alias_metadata`. Example: true
	// +optional
	EnableIdentityAliasMetadata *bool `json:"enableIdentityAliasMetadata,omitempty"`

	// Number of entries in the in-memory OCSP response cache shared by all certificate roles. 2 to 100000; OpenBao defaults to 100. Maps to `ocsp_cache_size`. Example: 1000
	// +kubebuilder:validation:Minimum=2
	// +kubebuilder:validation:Maximum=100000
	// +optional
	OCSPCacheSize *int32 `json:"ocspCacheSize,omitempty"`
}

// Observed state of the configuration, written by the operator.
type CertAuthConfigStatus struct {
	commonv1alpha1.ResourceStatus `json:",inline"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:categories=obco;obco-auth
// +kubebuilder:printcolumn:name="Ready",type=string,JSONPath=`.status.conditions[?(@.type=="Ready")].status`
// +kubebuilder:printcolumn:name="Synced",type=string,JSONPath=`.status.conditions[?(@.type=="Synced")].status`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// CertAuthConfig manages the mount-wide settings of an OpenBao TLS certificate auth method at auth/<mountPath>/config. The settings always exist, so the resource adopts them (creationPolicy Adopt or CreateOrAdopt) and is never deleted from OpenBao.
//
// Example:
//
//	spec:
//	  connectionRef:
//	    name: openbao
//	  creationPolicy: Adopt
//	  deletionPolicy: Orphan
//	  mountPath: cert
//	  ocspCacheSize: 1000
type CertAuthConfig struct {
	metav1.TypeMeta `json:",inline"`

	// +optional
	metav1.ObjectMeta `json:"metadata,omitzero"`

	// +required
	Spec CertAuthConfigSpec `json:"spec"`

	// +optional
	Status CertAuthConfigStatus `json:"status,omitzero"`
}

// GetResourceSpec implements commonv1alpha1.Managed.
func (c *CertAuthConfig) GetResourceSpec() *commonv1alpha1.ResourceSpec { return &c.Spec.ResourceSpec }

// GetResourceStatus implements commonv1alpha1.Managed.
func (c *CertAuthConfig) GetResourceStatus() *commonv1alpha1.ResourceStatus {
	return &c.Status.ResourceStatus
}

// +kubebuilder:object:root=true

// CertAuthConfigList contains a list of CertAuthConfig.
type CertAuthConfigList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []CertAuthConfig `json:"items"`
}

func init() {
	SchemeBuilder.Register(&CertAuthConfig{}, &CertAuthConfigList{})
}
