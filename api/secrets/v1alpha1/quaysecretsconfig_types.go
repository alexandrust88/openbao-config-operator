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
// +kubebuilder:validation:XValidation:rule="!has(self.deletionPolicy) || self.deletionPolicy == 'Orphan'",message="deletionPolicy must be Orphan: the plugin configuration is kept when the resource is deleted"

// spec is the desired configuration of the Quay secrets plugin at `<mountPath>/config`: the Quay instance it manages robot accounts in. deletionPolicy must be Orphan.
type QuaySecretsConfigSpec struct {
	commonv1alpha1.ResourceSpec `json:",inline"`

	// mountPath is the path the Quay secrets plugin is mounted at. Defaults to `quay`. Immutable. Example: `quay`.
	// +kubebuilder:default=quay
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=256
	// +kubebuilder:validation:Pattern=`^[A-Za-z0-9_.-]+(/[A-Za-z0-9_.-]+)*$`
	// +optional
	MountPath string `json:"mountPath,omitempty"`

	// url is the URL of the Quay instance (`url`). Example: `https://quay.example.com`.
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:XValidation:rule="!self.matches('://[^/?#]*@')",message="url must not contain credentials (user:password@ or token@): use the secret reference fields"
	// +kubebuilder:validation:MaxLength=2048
	// +required
	URL string `json:"url"`

	// tokenSecretRef selects the Secret key holding the Quay OAuth token the plugin uses (`token`). Read from a Secret key; never stored in status. Write-only. Example: `{name: quay-oauth, key: token}`.
	// +required
	TokenSecretRef commonv1alpha1.SecretKeySelector `json:"tokenSecretRef"`

	// caCertificate is the PEM CA certificate that verifies the Quay TLS certificate (`ca_certificate`). Example: `-----BEGIN CERTIFICATE-----...`.
	// +kubebuilder:validation:MaxLength=65536
	// +optional
	CACertificate string `json:"caCertificate,omitempty"`

	// disableSSLVerification skips verification of the Quay TLS certificate (`disable_ssl_verification`); not for production. Defaults to false.
	// +optional
	DisableSSLVerification *bool `json:"disableSSLVerification,omitempty"`
}

// status is the observed state of the Quay secrets plugin configuration.
type QuaySecretsConfigStatus struct {
	commonv1alpha1.ResourceStatus `json:",inline"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:categories=obco;obco-secrets
// +kubebuilder:printcolumn:name="Ready",type=string,JSONPath=`.status.conditions[?(@.type=="Ready")].status`
// +kubebuilder:printcolumn:name="Synced",type=string,JSONPath=`.status.conditions[?(@.type=="Synced")].status`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// QuaySecretsConfig sets the Quay instance of a Quay secrets plugin mount (external plugin) at `<mount>/config`, in which OpenBao creates robot accounts. A singleton per mount; it is never deleted.
//
// Example:
//
//	spec:
//	  connectionRef:
//	    name: openbao
//	  mountPath: quay
//	  url: https://quay.example.com
//	  tokenSecretRef: {name: quay-oauth, key: token}
type QuaySecretsConfig struct {
	metav1.TypeMeta `json:",inline"`

	// +optional
	metav1.ObjectMeta `json:"metadata,omitzero"`

	// +required
	Spec QuaySecretsConfigSpec `json:"spec"`

	// +optional
	Status QuaySecretsConfigStatus `json:"status,omitzero"`
}

// GetResourceSpec implements commonv1alpha1.Managed.
func (o *QuaySecretsConfig) GetResourceSpec() *commonv1alpha1.ResourceSpec {
	return &o.Spec.ResourceSpec
}

// GetResourceStatus implements commonv1alpha1.Managed.
func (o *QuaySecretsConfig) GetResourceStatus() *commonv1alpha1.ResourceStatus {
	return &o.Status.ResourceStatus
}

// +kubebuilder:object:root=true

// QuaySecretsConfigList contains a list of QuaySecretsConfig.
type QuaySecretsConfigList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []QuaySecretsConfig `json:"items"`
}

func init() {
	SchemeBuilder.Register(&QuaySecretsConfig{}, &QuaySecretsConfigList{})
}
