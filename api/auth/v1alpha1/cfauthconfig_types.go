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
// +kubebuilder:validation:XValidation:rule="!has(self.deletionPolicy) || self.deletionPolicy == 'Orphan'",message="deletionPolicy must be Orphan: an auth method config has no delete operation"
// +kubebuilder:validation:XValidation:rule="has(self.cfUsername) == has(self.cfPasswordSecretRef)",message="set both or neither of cfUsername and cfPasswordSecretRef"
// +kubebuilder:validation:XValidation:rule="has(self.cfAPIMutualTLSCertificate) == has(self.cfAPIMutualTLSKeySecretRef)",message="set both or neither of cfAPIMutualTLSCertificate and cfAPIMutualTLSKeySecretRef"

// Desired state of the Cloud Foundry auth method configuration, written to auth/<mountPath>/config. cfUsername and cfPasswordSecretRef are set together, as are cfAPIMutualTLSCertificate and cfAPIMutualTLSKeySecretRef. deletionPolicy must be Orphan.
type CFAuthConfigSpec struct {
	commonv1alpha1.ResourceSpec `json:",inline"`

	// Path of the Cloud Foundry auth method, without the auth/ prefix. Defaults to cf. Immutable. Example: cf/prod
	// +kubebuilder:default=cf
	// +kubebuilder:validation:MaxLength=128
	// +kubebuilder:validation:Pattern=`^[A-Za-z0-9_.-]+(/[A-Za-z0-9_.-]+)*$`
	// +optional
	MountPath string `json:"mountPath,omitempty"`

	// PEM root CA certificates that sign Cloud Foundry instance identity certificates; logins must present a certificate chaining to one of them. Required, 1-16 entries. Maps to `identity_ca_certificates`. Example: ["-----BEGIN CERTIFICATE-----\nMIIC..."]
	// +kubebuilder:validation:MinItems=1
	// +kubebuilder:validation:MaxItems=16
	// +kubebuilder:validation:items:MinLength=1
	// +kubebuilder:validation:items:MaxLength=65536
	// +listType=set
	// +required
	IdentityCACertificates []string `json:"identityCACertificates"`

	// Full URL of the Cloud Foundry API, used to look up applications, spaces and organizations at login. Required. Maps to `cf_api_addr`. Example: https://api.sys.cf.example.com
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=2048
	// +required
	CFAPIAddr string `json:"cfAPIAddr"`

	// Cloud Foundry user OpenBao authenticates as to query the API. Set together with cfPasswordSecretRef. Maps to `cf_username`. Example: openbao
	// +kubebuilder:validation:MaxLength=256
	// +optional
	CFUsername string `json:"cfUsername,omitempty"`

	// Secret key holding the password of cfUsername. Read from this Secret key; the value is never stored in status, is write-only in OpenBao and is tracked by a hash, so a change of the Secret is written again. Maps to `cf_password`. Example: {name: cf-api, key: password}
	// +optional
	CFPasswordSecretRef *commonv1alpha1.SecretKeySelector `json:"cfPasswordSecretRef,omitempty"`

	// PEM CA certificates trusted for TLS to the Cloud Foundry API. Unset uses the system trust store. Maps to `cf_api_trusted_certificates`. Example: ["-----BEGIN CERTIFICATE-----\nMIIC..."]
	// +kubebuilder:validation:MaxItems=16
	// +kubebuilder:validation:items:MinLength=1
	// +kubebuilder:validation:items:MaxLength=65536
	// +listType=set
	// +optional
	CFAPITrustedCertificates []string `json:"cfAPITrustedCertificates,omitempty"`

	// PEM client certificate for mutual TLS to the Cloud Foundry API. Set together with cfAPIMutualTLSKeySecretRef. Maps to `cf_api_mutual_tls_certificate`. Example: "-----BEGIN CERTIFICATE-----\nMIIC..."
	// +kubebuilder:validation:MaxLength=65536
	// +optional
	CFAPIMutualTLSCertificate string `json:"cfAPIMutualTLSCertificate,omitempty"`

	// Secret key holding the PEM private key of cfAPIMutualTLSCertificate. Read from this Secret key; the value is never stored in status, is write-only in OpenBao and is tracked by a hash, so a change of the Secret is written again. Maps to `cf_api_mutual_tls_key`. Example: {name: cf-api-mtls, key: tls.key}
	// +optional
	CFAPIMutualTLSKeySecretRef *commonv1alpha1.SecretKeySelector `json:"cfAPIMutualTLSKeySecretRef,omitempty"`

	// Maximum age of a login signature, in seconds: how far in the past its signing time may be. OpenBao defaults to 300. Maps to `login_max_seconds_not_before`. Example: 300
	// +kubebuilder:validation:Minimum=0
	// +optional
	LoginMaxSecondsNotBefore *int32 `json:"loginMaxSecondsNotBefore,omitempty"`

	// Maximum clock skew into the future for a login signature, in seconds. OpenBao defaults to 60. Maps to `login_max_seconds_not_after`. Example: 60
	// +kubebuilder:validation:Minimum=0
	// +optional
	LoginMaxSecondsNotAfter *int32 `json:"loginMaxSecondsNotAfter,omitempty"`
}

// Observed state of the Cloud Foundry auth method configuration, written by the operator.
type CFAuthConfigStatus struct {
	commonv1alpha1.ResourceStatus `json:",inline"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:categories=obco;obco-auth
// +kubebuilder:printcolumn:name="Ready",type=string,JSONPath=`.status.conditions[?(@.type=="Ready")].status`
// +kubebuilder:printcolumn:name="Synced",type=string,JSONPath=`.status.conditions[?(@.type=="Synced")].status`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// CFAuthConfig manages the configuration of an OpenBao Cloud Foundry auth method at auth/<mountPath>/config. Use it to set the instance identity CAs and the Cloud Foundry API connection that CF logins are verified against. Needs the cf auth plugin registered and enabled at auth/<mountPath>; there is one config per mount and it is never deleted.
//
// Example:
//
//	spec:
//	  connectionRef:
//	    name: openbao
//	  identityCACertificates: ["-----BEGIN CERTIFICATE-----\n..."]
//	  cfAPIAddr: https://api.sys.cf.example.com
//	  cfUsername: openbao
//	  cfPasswordSecretRef:
//	    name: cf-api
//	    key: password
type CFAuthConfig struct {
	metav1.TypeMeta `json:",inline"`

	// +optional
	metav1.ObjectMeta `json:"metadata,omitzero"`

	// +required
	Spec CFAuthConfigSpec `json:"spec"`

	// +optional
	Status CFAuthConfigStatus `json:"status,omitzero"`
}

// GetResourceSpec implements commonv1alpha1.Managed.
func (o *CFAuthConfig) GetResourceSpec() *commonv1alpha1.ResourceSpec { return &o.Spec.ResourceSpec }

// GetResourceStatus implements commonv1alpha1.Managed.
func (o *CFAuthConfig) GetResourceStatus() *commonv1alpha1.ResourceStatus {
	return &o.Status.ResourceStatus
}

// +kubebuilder:object:root=true

// CFAuthConfigList contains a list of CFAuthConfig.
type CFAuthConfigList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []CFAuthConfig `json:"items"`
}

func init() {
	SchemeBuilder.Register(&CFAuthConfig{}, &CFAuthConfigList{})
}
