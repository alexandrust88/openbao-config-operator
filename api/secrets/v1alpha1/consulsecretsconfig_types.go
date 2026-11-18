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
// +kubebuilder:validation:XValidation:rule="!has(self.deletionPolicy) || self.deletionPolicy == 'Orphan'",message="deletionPolicy must be Orphan: the Consul secrets engine configuration cannot be deleted"
// +kubebuilder:validation:XValidation:rule="has(self.clientCert) == has(self.clientKeySecretRef)",message="set clientCert and clientKeySecretRef together"

// spec is the desired access configuration of a Consul secrets engine at `<mountPath>/config/access`. Every write replaces the whole configuration. The engine mount itself is managed by a SecretsMount. deletionPolicy must be Orphan.
type ConsulSecretsConfigSpec struct {
	commonv1alpha1.ResourceSpec `json:",inline"`

	// mountPath is the path of the Consul secrets engine mount. Defaults to `consul`. Immutable. Example: `consul`.
	// +kubebuilder:default=consul
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=128
	// +kubebuilder:validation:Pattern=`^[A-Za-z0-9_.-]+(/[A-Za-z0-9_.-]+)*$`
	// +optional
	MountPath string `json:"mountPath,omitempty"`

	// address is the Consul server as host:port (`address`). Example: `consul.example.com:8501`.
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=1024
	// +required
	Address string `json:"address"`

	// scheme is the URL scheme used to reach Consul (`scheme`). Allowed: http, https. Defaults to http.
	// +kubebuilder:validation:Enum=http;https
	// +optional
	Scheme string `json:"scheme,omitempty"`

	// tokenSecretRef selects the Secret key that holds the Consul ACL token OpenBao uses to manage tokens (`token`). Read from a Secret key; never stored in status. Write-only: a change of the Secret is written again. ACL bootstrapping through OpenBao is not supported. Example: `{name: consul-mgmt, key: token}`.
	// +required
	TokenSecretRef commonv1alpha1.SecretKeySelector `json:"tokenSecretRef"`

	// caCert is the PEM CA certificate that verifies the Consul server (`ca_cert`). OpenBao does not return it on read, so only spec changes are applied. Example: `-----BEGIN CERTIFICATE-----...`.
	// +kubebuilder:validation:MaxLength=65536
	// +optional
	CACert string `json:"caCert,omitempty"`

	// clientCert is the PEM client certificate for TLS to Consul (`client_cert`); set together with clientKeySecretRef. OpenBao does not return it on read. Example: `-----BEGIN CERTIFICATE-----...`.
	// +kubebuilder:validation:MaxLength=65536
	// +optional
	ClientCert string `json:"clientCert,omitempty"`

	// clientKeySecretRef selects the Secret key that holds the PEM key of clientCert (`client_key`). Read from a Secret key; never stored in status. Write-only. Example: `{name: consul-client, key: tls.key}`.
	// +optional
	ClientKeySecretRef *commonv1alpha1.SecretKeySelector `json:"clientKeySecretRef,omitempty"`
}

// status is the observed state of the Consul secrets engine configuration.
type ConsulSecretsConfigStatus struct {
	commonv1alpha1.ResourceStatus `json:",inline"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:categories=obco;obco-secrets
// +kubebuilder:printcolumn:name="Ready",type=string,JSONPath=`.status.conditions[?(@.type=="Ready")].status`
// +kubebuilder:printcolumn:name="Synced",type=string,JSONPath=`.status.conditions[?(@.type=="Synced")].status`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// ConsulSecretsConfig sets the access configuration of a Consul secrets engine (external plugin) at `<mount>/config/access`: the Consul address and the management token OpenBao uses to create ACL tokens. A singleton per mount; it is never deleted.
//
// Example:
//
//	spec:
//	  connectionRef:
//	    name: openbao
//	  mountPath: consul
//	  address: consul.example.com:8501
//	  scheme: https
//	  tokenSecretRef: {name: consul-mgmt, key: token}
type ConsulSecretsConfig struct {
	metav1.TypeMeta `json:",inline"`

	// +optional
	metav1.ObjectMeta `json:"metadata,omitzero"`

	// +required
	Spec ConsulSecretsConfigSpec `json:"spec"`

	// +optional
	Status ConsulSecretsConfigStatus `json:"status,omitzero"`
}

// GetResourceSpec implements commonv1alpha1.Managed.
func (o *ConsulSecretsConfig) GetResourceSpec() *commonv1alpha1.ResourceSpec {
	return &o.Spec.ResourceSpec
}

// GetResourceStatus implements commonv1alpha1.Managed.
func (o *ConsulSecretsConfig) GetResourceStatus() *commonv1alpha1.ResourceStatus {
	return &o.Status.ResourceStatus
}

// +kubebuilder:object:root=true

// ConsulSecretsConfigList contains a list of ConsulSecretsConfig.
type ConsulSecretsConfigList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []ConsulSecretsConfig `json:"items"`
}

func init() {
	SchemeBuilder.Register(&ConsulSecretsConfig{}, &ConsulSecretsConfigList{})
}
