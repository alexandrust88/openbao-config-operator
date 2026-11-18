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

// +kubebuilder:validation:XValidation:rule="has(self.name) == has(oldSelf.name) && (!has(self.name) || self.name == oldSelf.name)",message="name is immutable"

// spec is the desired OIDC provider at `identity/oidc/provider/<name>`. The provider `default` exists on every server; an OIDCProvider may manage its settings, but deleting the resource never deletes it.
type OIDCProviderSpec struct {
	commonv1alpha1.ResourceSpec `json:",inline"`

	// name is the provider name in OpenBao. Defaults to metadata.name. Letters, digits, `_`, `.`, `@` and `-`. Immutable. Example: `corp`.
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=128
	// +kubebuilder:validation:Pattern=`^\w([\w.@-]*\w)?$`
	// +optional
	Name string `json:"name,omitempty"`

	// issuer is the scheme, host and optional port used in the `iss` claim (`issuer`); OpenBao appends the provider path. Unset uses the OpenBao `api_addr`. Example: `https://openbao.example.com:8200`.
	// +kubebuilder:validation:MaxLength=255
	// +kubebuilder:validation:Pattern=`^https?://[^/?#\s]+$`
	// +optional
	Issuer string `json:"issuer,omitempty"`

	// allowedClients are the OIDC clients allowed to use the provider (`allowed_client_ids`). A clientID of `*` allows every client. Up to 256. Example: `[{clientRef: {name: grafana}}]`.
	// +kubebuilder:validation:MaxItems=256
	// +listType=atomic
	// +optional
	AllowedClients []OIDCClientMatch `json:"allowedClients,omitempty"`

	// scopesSupported are names of OIDC scopes the provider offers (`scopes_supported`). Each scope must exist. Up to 64. Example: `[groups, email]`.
	// +kubebuilder:validation:MaxItems=64
	// +kubebuilder:validation:items:MinLength=1
	// +kubebuilder:validation:items:MaxLength=128
	// +listType=set
	// +optional
	ScopesSupported []string `json:"scopesSupported,omitempty"`
}

// status is the observed state of the OIDC provider.
type OIDCProviderStatus struct {
	commonv1alpha1.ResourceStatus `json:",inline"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:categories=obco;obco-identity
// +kubebuilder:printcolumn:name="Ready",type=string,JSONPath=`.status.conditions[?(@.type=="Ready")].status`
// +kubebuilder:printcolumn:name="Synced",type=string,JSONPath=`.status.conditions[?(@.type=="Synced")].status`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// OIDCProvider manages an OpenID Connect provider served by OpenBao at `identity/oidc/provider/<name>`. Use it to let applications (OIDCClient) log users in with their OpenBao identity; its discovery document is at `/v1/identity/oidc/provider/<name>/.well-known/openid-configuration`.
//
// Example:
//
//	spec:
//	  connectionRef:
//	    name: openbao
//	  name: corp
//	  allowedClients:
//	    - clientRef: {name: grafana}
//	  scopesSupported: [groups]
type OIDCProvider struct {
	metav1.TypeMeta `json:",inline"`

	// +optional
	metav1.ObjectMeta `json:"metadata,omitzero"`

	// +required
	Spec OIDCProviderSpec `json:"spec"`

	// +optional
	Status OIDCProviderStatus `json:"status,omitzero"`
}

// GetResourceSpec implements commonv1alpha1.Managed.
func (o *OIDCProvider) GetResourceSpec() *commonv1alpha1.ResourceSpec { return &o.Spec.ResourceSpec }

// GetResourceStatus implements commonv1alpha1.Managed.
func (o *OIDCProvider) GetResourceStatus() *commonv1alpha1.ResourceStatus {
	return &o.Status.ResourceStatus
}

// +kubebuilder:object:root=true

// OIDCProviderList contains a list of OIDCProvider.
type OIDCProviderList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []OIDCProvider `json:"items"`
}

func init() {
	SchemeBuilder.Register(&OIDCProvider{}, &OIDCProviderList{})
}
