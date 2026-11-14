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

// Whether an OIDC client can keep a client secret confidential: confidential or public.
// +kubebuilder:validation:Enum=confidential;public
// +kubebuilder:validation:MaxLength=16
type OIDCClientType string

const (
	OIDCClientTypeConfidential OIDCClientType = "confidential"
	OIDCClientTypePublic       OIDCClientType = "public"
)

// +kubebuilder:validation:XValidation:rule="has(self.name) == has(oldSelf.name) && (!has(self.name) || self.name == oldSelf.name)",message="name is immutable"
// +kubebuilder:validation:XValidation:rule="has(self.key) == has(oldSelf.key) && (!has(self.key) || self.key == oldSelf.key)",message="key is immutable"
// +kubebuilder:validation:XValidation:rule="has(self.clientType) == has(oldSelf.clientType) && (!has(self.clientType) || self.clientType == oldSelf.clientType)",message="clientType is immutable"
// +kubebuilder:validation:XValidation:rule="!(has(self.clientType) && self.clientType == 'public' && has(self.clientCredentials) && self.clientCredentials)",message="clientCredentials requires clientType confidential"

// spec is the desired client of the OpenBao OIDC provider at `identity/oidc/client/<name>`. OpenBao generates the client ID and, for a confidential client, the client secret; set writeSecretRef to receive them.
type OIDCClientSpec struct {
	commonv1alpha1.ResourceSpec `json:",inline"`

	// name is the client name in OpenBao. Defaults to metadata.name. Letters, digits, `_`, `.`, `@` and `-`. Immutable. Example: `grafana`.
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=128
	// +kubebuilder:validation:Pattern=`^\w([\w.@-]*\w)?$`
	// +optional
	Name string `json:"name,omitempty"`

	// key is the name of the OIDC key that signs the client's tokens (`key`); an OIDCKey or the built-in `default`. Defaults to `default`. Immutable. Example: `apps`.
	// +kubebuilder:default=default
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=128
	// +optional
	Key string `json:"key,omitempty"`

	// clientType is `confidential` (the client holds a client secret) or `public` (`client_type`). Defaults to confidential. Immutable.
	// +kubebuilder:default=confidential
	// +optional
	ClientType OIDCClientType `json:"clientType,omitempty"`

	// redirectURIs are the allowed redirect URIs (`redirect_uris`); a request's redirect_uri must match one exactly. Up to 64. Example: `[https://grafana.example.com/login/generic_oauth]`.
	// +kubebuilder:validation:MaxItems=64
	// +kubebuilder:validation:items:MinLength=1
	// +kubebuilder:validation:items:MaxLength=512
	// +listType=set
	// +optional
	RedirectURIs []string `json:"redirectURIs,omitempty"`

	// assignments are the names of OIDC assignments that select the entities and groups allowed to use the client (`assignments`). Each must exist; `allow_all` allows every entity. Up to 64. Example: `[billing-users]`.
	// +kubebuilder:validation:MaxItems=64
	// +kubebuilder:validation:items:MinLength=1
	// +kubebuilder:validation:items:MaxLength=128
	// +listType=set
	// +optional
	Assignments []string `json:"assignments,omitempty"`

	// idTokenTTL is the lifetime of ID tokens (`id_token_ttl`), a duration. Must not exceed the key's verificationTTL. Defaults to `24h`. Example: `1h`.
	// +optional
	IDTokenTTL *metav1.Duration `json:"idTokenTTL,omitempty"`

	// accessTokenTTL is the lifetime of access tokens (`access_token_ttl`), a duration. Defaults to `24h`. Example: `1h`.
	// +optional
	AccessTokenTTL *metav1.Duration `json:"accessTokenTTL,omitempty"`

	// authorizationCode allows the authorization code flow (`authorization_code`). Defaults to true.
	// +optional
	AuthorizationCode *bool `json:"authorizationCode,omitempty"`

	// clientCredentials allows the client credentials flow (`client_credentials`); confidential clients only. Defaults to false.
	// +optional
	ClientCredentials *bool `json:"clientCredentials,omitempty"`

	// writeSecretRef names a Secret in the resource's namespace that the operator creates and owns, with the keys `client_id` and, for a confidential client, `client_secret`. The secret is never stored in status. Example: `{name: grafana-oidc}`.
	// +optional
	WriteSecretRef *commonv1alpha1.LocalObjectReference `json:"writeSecretRef,omitempty"`
}

// status is the observed state of the OIDC client; `id` holds the client ID.
type OIDCClientStatus struct {
	commonv1alpha1.ResourceStatus `json:",inline"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:categories=openbao
// +kubebuilder:printcolumn:name="Client ID",type=string,JSONPath=`.status.id`,priority=1
// +kubebuilder:printcolumn:name="Ready",type=string,JSONPath=`.status.conditions[?(@.type=="Ready")].status`
// +kubebuilder:printcolumn:name="Synced",type=string,JSONPath=`.status.conditions[?(@.type=="Synced")].status`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// OIDCClient manages an application that logs users in through the OpenBao OIDC provider, at `identity/oidc/client/<name>`. Use it with an OIDCProvider, an OIDCAssignment and writeSecretRef to hand the generated client ID and secret to the application.
//
// Example:
//
//	spec:
//	  connectionRef:
//	    name: openbao
//	  name: grafana
//	  redirectURIs:
//	    - https://grafana.example.com/login/generic_oauth
//	  assignments: [billing-users]
//	  writeSecretRef:
//	    name: grafana-oidc
type OIDCClient struct {
	metav1.TypeMeta `json:",inline"`

	// +optional
	metav1.ObjectMeta `json:"metadata,omitzero"`

	// +required
	Spec OIDCClientSpec `json:"spec"`

	// +optional
	Status OIDCClientStatus `json:"status,omitzero"`
}

// GetResourceSpec implements commonv1alpha1.Managed.
func (o *OIDCClient) GetResourceSpec() *commonv1alpha1.ResourceSpec { return &o.Spec.ResourceSpec }

// GetResourceStatus implements commonv1alpha1.Managed.
func (o *OIDCClient) GetResourceStatus() *commonv1alpha1.ResourceStatus {
	return &o.Status.ResourceStatus
}

// +kubebuilder:object:root=true

// OIDCClientList contains a list of OIDCClient.
type OIDCClientList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []OIDCClient `json:"items"`
}

func init() {
	SchemeBuilder.Register(&OIDCClient{}, &OIDCClientList{})
}
