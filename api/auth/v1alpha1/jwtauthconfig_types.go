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
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	commonv1alpha1 "github.com/alexandrust88/openbao-config-operator/api/common/v1alpha1"
)

// OIDCResponseMode is the OAuth2 response mode OpenBao requests from the OIDC provider.
// +kubebuilder:validation:Enum=query;form_post
type OIDCResponseMode string

// OAuth2 response type OpenBao requests from the OIDC provider: code or id_token.
// +kubebuilder:validation:Enum=code;id_token
type OIDCResponseType string

// JWTProvider names a provider-specific integration of the JWT/OIDC auth method.
// +kubebuilder:validation:Enum=azure;gsuite;secureauth;ibmisam;kubernetes
type JWTProvider string

// Provider-specific settings of a JWT/OIDC auth method, such as group fetching from Azure or Google Workspace.
type JWTProviderConfig struct {
	// Provider integration to enable. One of azure, gsuite, secureauth, ibmisam or kubernetes. kubernetes is a key source on its own; the others also need oidcDiscoveryURL. Required. Maps to `provider_config.provider`. Example: azure
	// +required
	Provider JWTProvider `json:"provider"`

	// Other non-sensitive provider keys as a JSON object, at most 64 keys. Put sensitive keys in secretRef instead. Maps to the remaining `provider_config` keys. Example: {fetch_groups: true, groups_recurse_max_depth: 5}
	// +kubebuilder:validation:MaxProperties=64
	// +optional
	Settings map[string]apiextensionsv1.JSON `json:"settings,omitempty"`

	// Secret key holding a JSON object merged over settings, for sensitive keys such as gsuite_service_account. Read from this Secret key; the value is never stored in status. OpenBao does not return sensitive keys, so when this is set the whole provider configuration is compared only through status.writeOnlyHash and a Secret change forces a write. Example: {name: gsuite-provider, key: config.json}
	// +optional
	SecretRef *commonv1alpha1.SecretKeySelector `json:"secretRef,omitempty"`
}

// Desired state of a JWT/OIDC auth method configuration, written to auth/<mountPath>/config. OpenBao replaces the whole configuration on every write, so unset fields are reset to their OpenBao defaults. Set exactly one key source: oidcDiscoveryURL, jwksURL, jwtValidationPubkeys or a kubernetes providerConfig. deletionPolicy must be Orphan.
//
// +kubebuilder:validation:XValidation:rule="has(self.mountPath) == has(oldSelf.mountPath) && (!has(self.mountPath) || self.mountPath == oldSelf.mountPath)",message="mountPath is immutable"
// +kubebuilder:validation:XValidation:rule="!has(self.deletionPolicy) || self.deletionPolicy == 'Orphan'",message="deletionPolicy must be Orphan: the auth method configuration cannot be deleted, only replaced"
// +kubebuilder:validation:XValidation:rule="[has(self.oidcDiscoveryURL), has(self.jwksURL), has(self.jwtValidationPubkeys), has(self.providerConfig) && self.providerConfig.provider == 'kubernetes'].filter(x, x).size() == 1",message="set exactly one of oidcDiscoveryURL, jwksURL, jwtValidationPubkeys and a kubernetes providerConfig"
// +kubebuilder:validation:XValidation:rule="has(self.oidcClientID) == has(self.oidcClientSecretSecretRef)",message="oidcClientID and oidcClientSecretSecretRef must be set together"
// +kubebuilder:validation:XValidation:rule="!has(self.oidcClientID) || has(self.oidcDiscoveryURL)",message="oidcClientID requires oidcDiscoveryURL"
// +kubebuilder:validation:XValidation:rule="!has(self.oidcResponseTypes) || !self.oidcResponseTypes.exists(t, t == 'id_token') || (has(self.oidcResponseMode) && self.oidcResponseMode == 'form_post')",message="the id_token response type requires oidcResponseMode form_post"
type JWTAuthConfigSpec struct {
	commonv1alpha1.ResourceSpec `json:",inline"`

	// Path of the JWT/OIDC auth method, without the auth/ prefix. Defaults to jwt. Immutable. Example: oidc
	// +kubebuilder:default=jwt
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=128
	// +kubebuilder:validation:Pattern=`^[A-Za-z0-9_.-]+(/[A-Za-z0-9_.-]+)*$`
	// +optional
	MountPath string `json:"mountPath,omitempty"`

	// Issuer URL used for OIDC discovery, without the /.well-known/openid-configuration suffix. Excludes jwksURL and jwtValidationPubkeys; required by oidcClientID. Maps to `oidc_discovery_url`. Example: https://keycloak.example.com/realms/platform
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=2048
	// +kubebuilder:validation:XValidation:rule="!self.contains('.well-known')",message="oidcDiscoveryURL must not contain the .well-known component"
	// +optional
	OIDCDiscoveryURL string `json:"oidcDiscoveryURL,omitempty"`

	// PEM-encoded CA certificate or chain used to verify TLS to oidcDiscoveryURL. Unset uses the system trust store. Maps to `oidc_discovery_ca_pem`. Example: "-----BEGIN CERTIFICATE-----\n..."
	// +kubebuilder:validation:MaxLength=65536
	// +optional
	OIDCDiscoveryCAPEM string `json:"oidcDiscoveryCAPEM,omitempty"`

	// OAuth2 client ID registered with the OIDC provider, needed for the browser OIDC login flow. Must be set together with oidcClientSecretSecretRef and requires oidcDiscoveryURL. Maps to `oidc_client_id`. Example: openbao
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=512
	// +optional
	OIDCClientID string `json:"oidcClientID,omitempty"`

	// Secret key holding the OAuth2 client secret. Read from this Secret key; the value is never stored in status. OpenBao never returns it, so it is compared only through status.writeOnlyHash and a Secret change forces a write. Must be set together with oidcClientID. Maps to `oidc_client_secret`. Example: {name: oidc-client, key: client-secret}
	// +optional
	OIDCClientSecretSecretRef *commonv1alpha1.SecretKeySelector `json:"oidcClientSecretSecretRef,omitempty"`

	// OAuth2 response mode of the OIDC login: query or form_post. OpenBao defaults to query; the id_token response type requires form_post. Maps to `oidc_response_mode`. Example: form_post
	// +optional
	OIDCResponseMode OIDCResponseMode `json:"oidcResponseMode,omitempty"`

	// OAuth2 response types to request: code, id_token or both. OpenBao defaults to [code]; id_token requires oidcResponseMode form_post. Maps to `oidc_response_types`. Example: [code]
	// +kubebuilder:validation:MaxItems=2
	// +listType=set
	// +optional
	OIDCResponseTypes []OIDCResponseType `json:"oidcResponseTypes,omitempty"`

	// URL of a JSON Web Key Set used to verify JWT signatures. Excludes oidcDiscoveryURL and jwtValidationPubkeys. Maps to `jwks_url`. Example: https://issuer.example.com/.well-known/jwks.json
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=2048
	// +optional
	JWKSURL string `json:"jwksURL,omitempty"`

	// PEM-encoded CA certificate or chain used to verify TLS to jwksURL. Unset uses the system trust store. Maps to `jwks_ca_pem`. Example: "-----BEGIN CERTIFICATE-----\n..."
	// +kubebuilder:validation:MaxLength=65536
	// +optional
	JWKSCAPEM string `json:"jwksCAPEM,omitempty"`

	// PEM-encoded public keys used to verify JWT signatures locally, without contacting the issuer. Excludes oidcDiscoveryURL and jwksURL. Maps to `jwt_validation_pubkeys`. Example: ["-----BEGIN PUBLIC KEY-----\n..."]
	// +kubebuilder:validation:MinItems=1
	// +kubebuilder:validation:MaxItems=32
	// +kubebuilder:validation:items:MaxLength=16384
	// +listType=set
	// +optional
	JWTValidationPubkeys []string `json:"jwtValidationPubkeys,omitempty"`

	// JWT signing algorithms accepted, such as RS256, RS512, ES256 or EdDSA. OpenBao defaults to RS256. Maps to `jwt_supported_algs`. Example: [RS256, ES256]
	// +kubebuilder:validation:MaxItems=16
	// +kubebuilder:validation:items:MaxLength=16
	// +listType=set
	// +optional
	JWTSupportedAlgs []string `json:"jwtSupportedAlgs,omitempty"`

	// Value the iss claim of every JWT must equal. Unset skips the issuer check. Maps to `bound_issuer`. Example: https://keycloak.example.com/realms/platform
	// +kubebuilder:validation:MaxLength=2048
	// +optional
	BoundIssuer string `json:"boundIssuer,omitempty"`

	// Role used when a login request does not name one. Unset makes the role mandatory at login. Maps to `default_role`. Example: default
	// +kubebuilder:validation:MaxLength=128
	// +kubebuilder:validation:Pattern=`^[a-z0-9_.-]*$`
	// +optional
	DefaultRole string `json:"defaultRole,omitempty"`

	// Provider-specific configuration, for example group fetching from Azure or Google Workspace, or kubernetes as the key source. Maps to `provider_config`. Example: {provider: azure, settings: {fetch_groups: true}}
	// +optional
	ProviderConfig *JWTProviderConfig `json:"providerConfig,omitempty"`

	// Host names accepted during TLS verification of oidcDiscoveryURL and jwksURL, instead of the host name in the URL. Maps to `override_allowed_server_names`. Example: [keycloak.internal]
	// +kubebuilder:validation:MaxItems=32
	// +kubebuilder:validation:items:MaxLength=253
	// +listType=set
	// +optional
	OverrideAllowedServerNames []string `json:"overrideAllowedServerNames,omitempty"`

	// When true, the OpenBao namespace is passed in the OIDC state parameter instead of as a query parameter, so redirect URIs need no namespace. OpenBao defaults to true for a new configuration and keeps the current value when unset. Maps to `namespace_in_state`. Example: true
	// +optional
	NamespaceInState *bool `json:"namespaceInState,omitempty"`

	// When true, the configuration is accepted even if oidcDiscoveryURL or jwksURL cannot be reached at write time. OpenBao does not store it; it is tracked through status.writeOnlyHash. Maps to `skip_jwks_validation`. Example: true
	// +optional
	SkipJWKSValidation *bool `json:"skipJWKSValidation,omitempty"`
}

// Observed state of the auth method configuration, written by the operator.
type JWTAuthConfigStatus struct {
	commonv1alpha1.ResourceStatus `json:",inline"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:categories=obco;obco-auth
// +kubebuilder:printcolumn:name="Ready",type=string,JSONPath=`.status.conditions[?(@.type=="Ready")].status`
// +kubebuilder:printcolumn:name="Synced",type=string,JSONPath=`.status.conditions[?(@.type=="Synced")].status`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// JWTAuthConfig manages the configuration of an OpenBao JWT/OIDC auth method at auth/<mountPath>/config. Use it to set how JWTs are verified (OIDC discovery, JWKS URL or static keys) and the OIDC client for browser logins; one per mount.
//
// Example:
//
//	spec:
//	  connectionRef:
//	    name: openbao
//	  mountPath: oidc
//	  oidcDiscoveryURL: https://keycloak.example.com/realms/platform
//	  oidcClientID: openbao
//	  oidcClientSecretSecretRef: {name: oidc-client, key: client-secret}
//	  defaultRole: default
//	  deletionPolicy: Orphan
type JWTAuthConfig struct {
	metav1.TypeMeta `json:",inline"`

	// +optional
	metav1.ObjectMeta `json:"metadata,omitzero"`

	// +required
	Spec JWTAuthConfigSpec `json:"spec"`

	// +optional
	Status JWTAuthConfigStatus `json:"status,omitzero"`
}

// GetResourceSpec implements commonv1alpha1.Managed.
func (j *JWTAuthConfig) GetResourceSpec() *commonv1alpha1.ResourceSpec { return &j.Spec.ResourceSpec }

// GetResourceStatus implements commonv1alpha1.Managed.
func (j *JWTAuthConfig) GetResourceStatus() *commonv1alpha1.ResourceStatus {
	return &j.Status.ResourceStatus
}

// +kubebuilder:object:root=true

// JWTAuthConfigList contains a list of JWTAuthConfig.
type JWTAuthConfigList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []JWTAuthConfig `json:"items"`
}

func init() {
	SchemeBuilder.Register(&JWTAuthConfig{}, &JWTAuthConfigList{})
}
