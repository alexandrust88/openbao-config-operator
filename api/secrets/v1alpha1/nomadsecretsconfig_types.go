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
// +kubebuilder:validation:XValidation:rule="has(self.clientCert) == has(self.clientKeySecretRef)",message="set clientCert and clientKeySecretRef together"

// spec is the desired configuration of the Nomad secrets plugin: the cluster connection at `<mountPath>/config/access` and the lease limits at `<mountPath>/config/lease`. deletionPolicy must be Orphan.
type NomadSecretsConfigSpec struct {
	commonv1alpha1.ResourceSpec `json:",inline"`

	// mountPath is the path the Nomad secrets plugin is mounted at. Defaults to `nomad`. Immutable. Example: `nomad`.
	// +kubebuilder:default=nomad
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=256
	// +kubebuilder:validation:Pattern=`^[A-Za-z0-9_.-]+(/[A-Za-z0-9_.-]+)*$`
	// +optional
	MountPath string `json:"mountPath,omitempty"`

	// address is the URL of the Nomad API (`address`). Example: `https://nomad.example.com:4646`.
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:XValidation:rule="!self.matches('://[^/?#]*@')",message="address must not contain credentials (user:password@ or token@): use the secret reference fields"
	// +kubebuilder:validation:MaxLength=2048
	// +required
	Address string `json:"address"`

	// tokenSecretRef selects the Secret key holding the Nomad management token (`token`). Read from a Secret key; never stored in status. Write-only. Required because the plugin bootstraps the Nomad ACL system when configured without a token. Example: `{name: nomad-mgmt, key: token}`.
	// +required
	TokenSecretRef commonv1alpha1.SecretKeySelector `json:"tokenSecretRef"`

	// maxTokenNameLength caps the length of the names of generated Nomad tokens (`max_token_name_length`); 0 uses the plugin default. Example: `64`.
	// +kubebuilder:validation:Minimum=0
	// +optional
	MaxTokenNameLength *int32 `json:"maxTokenNameLength,omitempty"`

	// caCert is the PEM CA certificate that verifies the Nomad server certificate (`ca_cert`). Example: `-----BEGIN CERTIFICATE-----...`.
	// +kubebuilder:validation:MaxLength=65536
	// +optional
	CACert string `json:"caCert,omitempty"`

	// clientCert is the PEM client certificate presented to Nomad (`client_cert`); set together with clientKeySecretRef. Example: `-----BEGIN CERTIFICATE-----...`.
	// +kubebuilder:validation:MaxLength=65536
	// +optional
	ClientCert string `json:"clientCert,omitempty"`

	// clientKeySecretRef selects the Secret key holding the PEM key of clientCert (`client_key`). Read from a Secret key; never stored in status. Write-only. Example: `{name: nomad-client, key: tls.key}`.
	// +optional
	ClientKeySecretRef *commonv1alpha1.SecretKeySelector `json:"clientKeySecretRef,omitempty"`

	// ttl is the default lease duration of generated Nomad tokens (`ttl` of `config/lease`), a duration. Defaults to the mount's default lease TTL. Example: `1h`.
	// +kubebuilder:validation:Type=string
	// +kubebuilder:validation:Pattern=`^(0|([0-9]+(\.[0-9]+)?(ns|us|ms|s|m|h))+)$`
	// +optional
	TTL *metav1.Duration `json:"ttl,omitempty"`

	// maxTTL is the maximum lease duration of generated Nomad tokens (`max_ttl` of `config/lease`), a duration. Defaults to the mount's max lease TTL. Example: `24h`.
	// +kubebuilder:validation:Type=string
	// +kubebuilder:validation:Pattern=`^(0|([0-9]+(\.[0-9]+)?(ns|us|ms|s|m|h))+)$`
	// +optional
	MaxTTL *metav1.Duration `json:"maxTTL,omitempty"`
}

// status is the observed state of the Nomad secrets plugin configuration.
type NomadSecretsConfigStatus struct {
	commonv1alpha1.ResourceStatus `json:",inline"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:categories=obco;obco-secrets
// +kubebuilder:printcolumn:name="Ready",type=string,JSONPath=`.status.conditions[?(@.type=="Ready")].status`
// +kubebuilder:printcolumn:name="Synced",type=string,JSONPath=`.status.conditions[?(@.type=="Synced")].status`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// NomadSecretsConfig sets the cluster connection (`<mount>/config/access`) and lease limits (`<mount>/config/lease`) of a Nomad secrets plugin mount (external plugin), which issues Nomad ACL tokens. A singleton per mount; it is never deleted.
//
// Example:
//
//	spec:
//	  connectionRef:
//	    name: openbao
//	  mountPath: nomad
//	  address: https://nomad.example.com:4646
//	  tokenSecretRef: {name: nomad-mgmt, key: token}
type NomadSecretsConfig struct {
	metav1.TypeMeta `json:",inline"`

	// +optional
	metav1.ObjectMeta `json:"metadata,omitzero"`

	// +required
	Spec NomadSecretsConfigSpec `json:"spec"`

	// +optional
	Status NomadSecretsConfigStatus `json:"status,omitzero"`
}

// GetResourceSpec implements commonv1alpha1.Managed.
func (o *NomadSecretsConfig) GetResourceSpec() *commonv1alpha1.ResourceSpec {
	return &o.Spec.ResourceSpec
}

// GetResourceStatus implements commonv1alpha1.Managed.
func (o *NomadSecretsConfig) GetResourceStatus() *commonv1alpha1.ResourceStatus {
	return &o.Status.ResourceStatus
}

// +kubebuilder:object:root=true

// NomadSecretsConfigList contains a list of NomadSecretsConfig.
type NomadSecretsConfigList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []NomadSecretsConfig `json:"items"`
}

func init() {
	SchemeBuilder.Register(&NomadSecretsConfig{}, &NomadSecretsConfigList{})
}
