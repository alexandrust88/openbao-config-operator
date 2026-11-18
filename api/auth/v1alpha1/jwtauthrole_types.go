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

// JWTRoleType is the login flow of a JWT/OIDC role.
// +kubebuilder:validation:Enum=jwt;oidc
type JWTRoleType string

const (
	JWTRoleTypeJWT  JWTRoleType = "jwt"
	JWTRoleTypeOIDC JWTRoleType = "oidc"
)

// BoundClaimsType selects how bound claim values are matched.
// +kubebuilder:validation:Enum=string;glob
type BoundClaimsType string

// CallbackMode is the OIDC callback flow of a role.
// +kubebuilder:validation:Enum=client;direct;device
type CallbackMode string

// OAuth2 token that OpenBao copies into the token metadata: access_token, id_token or refresh_token.
// +kubebuilder:validation:Enum=access_token;id_token;refresh_token
type OAuth2MetadataName string

// JWTLeeways are the clock skew allowances of JWT claim validation. Each value is a number of seconds, a duration such as "30s" or "2m", or -1 to disable the check.
type JWTLeeways struct {
	// Clock skew allowed when checking the exp claim: a number of seconds, a duration such as 30s or 2m, or -1 to disable the check. 0 or unset uses the OpenBao default of 150 seconds. Maps to `expiration_leeway`. Example: 30s
	// +kubebuilder:validation:XValidation:rule="type(self) == int ? self >= -1 : self.matches('^([0-9]+(s|m|h))+$')",message="must be -1, a number of seconds or a duration such as 30s"
	// +optional
	ExpirationLeeway *intstr.IntOrString `json:"expirationLeeway,omitempty"`

	// Clock skew allowed when checking the nbf claim: a number of seconds, a duration such as 30s or 2m, or -1 to disable the check. 0 or unset uses the OpenBao default of 150 seconds. Maps to `not_before_leeway`. Example: 30s
	// +kubebuilder:validation:XValidation:rule="type(self) == int ? self >= -1 : self.matches('^([0-9]+(s|m|h))+$')",message="must be -1, a number of seconds or a duration such as 30s"
	// +optional
	NotBeforeLeeway *intstr.IntOrString `json:"notBeforeLeeway,omitempty"`

	// Clock skew allowed on every time-based claim (iat, exp, nbf): a number of seconds, a duration such as 30s or 2m, or -1 to disable it. 0 or unset uses the OpenBao default of 60 seconds. Maps to `clock_skew_leeway`. Example: 60
	// +kubebuilder:validation:XValidation:rule="type(self) == int ? self >= -1 : self.matches('^([0-9]+(s|m|h))+$')",message="must be -1, a number of seconds or a duration such as 30s"
	// +optional
	ClockSkewLeeway *intstr.IntOrString `json:"clockSkewLeeway,omitempty"`
}

// Desired state of a JWT/OIDC auth role, written to auth/<mountPath>/role/<name>. An oidc role needs allowedRedirectURIs; a jwt role needs at least one of boundAudiences, boundSubject, boundClaims and tokenBoundCIDRs.
//
// +kubebuilder:validation:XValidation:rule="has(self.mountPath) == has(oldSelf.mountPath) && (!has(self.mountPath) || self.mountPath == oldSelf.mountPath)",message="mountPath is immutable"
// +kubebuilder:validation:XValidation:rule="has(self.name) == has(oldSelf.name) && (!has(self.name) || self.name == oldSelf.name)",message="name is immutable"
// +kubebuilder:validation:XValidation:rule="!has(self.roleType) || self.roleType != 'oidc' || has(self.allowedRedirectURIs)",message="allowedRedirectURIs is required for an oidc role"
// +kubebuilder:validation:XValidation:rule="(has(self.roleType) && self.roleType == 'oidc') || has(self.boundAudiences) || has(self.boundSubject) || has(self.boundClaims) || has(self.tokenBoundCIDRs)",message="a jwt role needs at least one of boundAudiences, boundSubject, boundClaims and tokenBoundCIDRs"
type JWTAuthRoleSpec struct {
	commonv1alpha1.ResourceSpec `json:",inline"`

	// Path of the JWT/OIDC auth method, without the auth/ prefix. Defaults to jwt. Immutable. Example: oidc
	// +kubebuilder:default=jwt
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=128
	// +kubebuilder:validation:Pattern=`^[A-Za-z0-9_.-]+(/[A-Za-z0-9_.-]+)*$`
	// +optional
	MountPath string `json:"mountPath,omitempty"`

	// Name of the role in OpenBao, used in the login request. Defaults to metadata.name. Immutable. Example: developers
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=128
	// +kubebuilder:validation:Pattern=`^[a-z0-9_]([a-z0-9_.-]*[a-z0-9_])?$`
	// +optional
	Name string `json:"name,omitempty"`

	// Login flow of the role: jwt (the client presents a JWT) or oidc (browser-based OIDC flow). Defaults to jwt. Maps to `role_type`. Example: oidc
	// +kubebuilder:default=jwt
	// +optional
	RoleType JWTRoleType `json:"roleType,omitempty"`

	// Claim whose value becomes the entity alias name of a login. Required. Maps to `user_claim`. Example: sub
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=256
	// +required
	UserClaim string `json:"userClaim"`

	// When true, userClaim is read as a JSON pointer, to reach nested claims. Defaults to false. Maps to `user_claim_json_pointer`. Example: true
	// +optional
	UserClaimJSONPointer *bool `json:"userClaimJSONPointer,omitempty"`

	// Values accepted in the aud claim; a login matching any of them is allowed. Maps to `bound_audiences`. Example: [openbao]
	// +kubebuilder:validation:MinItems=1
	// +kubebuilder:validation:MaxItems=64
	// +kubebuilder:validation:items:MaxLength=512
	// +listType=set
	// +optional
	BoundAudiences []string `json:"boundAudiences,omitempty"`

	// Value the sub claim must equal. Unset skips the subject check. Maps to `bound_subject`. Example: system:serviceaccount:ci:runner
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=512
	// +optional
	BoundSubject string `json:"boundSubject,omitempty"`

	// How boundClaims values are matched: string (exact) or glob (* wildcards). Defaults to string. Maps to `bound_claims_type`. Example: glob
	// +kubebuilder:default=string
	// +optional
	BoundClaimsType BoundClaimsType `json:"boundClaimsType,omitempty"`

	// Claims a login must carry, each mapped to its accepted values: every claim must match, and any one of its values is enough. Maps to `bound_claims`. Example: {groups: [platform-admins], project_path: [iam/*]}
	// +kubebuilder:validation:MinProperties=1
	// +kubebuilder:validation:MaxProperties=64
	// +optional
	BoundClaims map[string][]string `json:"boundClaims,omitempty"`

	// Claims copied into the entity alias metadata, as claim name to metadata key. Maps to `claim_mappings`. Example: {email: email, preferred_username: username}
	// +kubebuilder:validation:MaxProperties=64
	// +optional
	ClaimMappings map[string]string `json:"claimMappings,omitempty"`

	// Claim holding the group names used as external identity group aliases. Unset disables group aliases. Maps to `groups_claim`. Example: groups
	// +kubebuilder:validation:MaxLength=256
	// +optional
	GroupsClaim string `json:"groupsClaim,omitempty"`

	// OIDC scopes requested in addition to openid during the OIDC flow. Maps to `oidc_scopes`. Example: [profile, email, groups]
	// +kubebuilder:validation:MaxItems=64
	// +kubebuilder:validation:items:MaxLength=256
	// +listType=set
	// +optional
	OIDCScopes []string `json:"oidcScopes,omitempty"`

	// Redirect URIs accepted during the OIDC flow, for example the OpenBao UI and CLI callbacks. Required for an oidc role. Maps to `allowed_redirect_uris`. Example: [https://openbao.example.com/ui/vault/auth/oidc/oidc/callback, http://localhost:8250/oidc/callback]
	// +kubebuilder:validation:MinItems=1
	// +kubebuilder:validation:MaxItems=64
	// +kubebuilder:validation:items:MaxLength=2048
	// +listType=set
	// +optional
	AllowedRedirectURIs []string `json:"allowedRedirectURIs,omitempty"`

	// OIDC callback flow: client (the browser redirects to the client), direct (the provider calls OpenBao) or device (device authorization flow). Defaults to client. Maps to `callback_mode`. Example: device
	// +kubebuilder:default=client
	// +optional
	CallbackMode CallbackMode `json:"callbackMode,omitempty"`

	// Polling interval in seconds for the device and direct callback flows. Unset uses the provider's value for the device flow, otherwise 5. Maps to `poll_interval`. Example: 5
	// +kubebuilder:validation:Minimum=1
	// +optional
	PollInterval *int32 `json:"pollInterval,omitempty"`

	// Maximum time allowed since the user last authenticated with the OIDC provider, as a duration; older sessions must re-authenticate. Unset disables the check. Maps to `max_age`. Example: 1h
	// +optional
	MaxAge *metav1.Duration `json:"maxAge,omitempty"`

	JWTLeeways `json:",inline"`

	// OAuth2 tokens copied into the token metadata: any of access_token, id_token and refresh_token. Maps to `oauth2_metadata`. Example: [id_token]
	// +kubebuilder:validation:MaxItems=3
	// +listType=set
	// +optional
	OAuth2Metadata []OAuth2MetadataName `json:"oauth2Metadata,omitempty"`

	// When true, OpenBao logs received OIDC tokens and claims at debug level. May leak sensitive data; not for production. Defaults to false. Maps to `verbose_oidc_logging`. Example: false
	// +optional
	VerboseOIDCLogging *bool `json:"verboseOIDCLogging,omitempty"`

	// When true, tokenPolicies entries are rendered as templates over the JWT claims, for example a policy per group. Defaults to false. Maps to `token_policies_template_claims`. Example: true
	// +optional
	TokenPoliciesTemplateClaims *bool `json:"tokenPoliciesTemplateClaims,omitempty"`

	// When true, the OpenBao confirmation page of the OIDC flow is skipped. OpenBao only (not Vault). Defaults to false. Maps to `oidc_disable_confirmation`. Example: true
	// +optional
	OIDCDisableConfirmation *bool `json:"oidcDisableConfirmation,omitempty"`

	TokenFields `json:",inline"`
}

// Observed state of the role, written by the operator.
type JWTAuthRoleStatus struct {
	commonv1alpha1.ResourceStatus `json:",inline"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:shortName=baojwtr,categories=obco;obco-auth
// +kubebuilder:printcolumn:name="Ready",type=string,JSONPath=`.status.conditions[?(@.type=="Ready")].status`
// +kubebuilder:printcolumn:name="Synced",type=string,JSONPath=`.status.conditions[?(@.type=="Synced")].status`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// JWTAuthRole manages a role of an OpenBao JWT/OIDC auth method at auth/<mountPath>/role/<name>. Use it to map JWT claims (CI jobs, workload identities) or OIDC browser logins to tokens with the listed policies.
//
// Example:
//
//	spec:
//	  connectionRef:
//	    name: openbao
//	  mountPath: jwt
//	  userClaim: sub
//	  boundAudiences: [openbao]
//	  boundClaims: {project_path: [iam/vault-stack]}
//	  tokenPolicies: [ci-read]
//	  tokenTTL: 15m
type JWTAuthRole struct {
	metav1.TypeMeta `json:",inline"`

	// +optional
	metav1.ObjectMeta `json:"metadata,omitzero"`

	// +required
	Spec JWTAuthRoleSpec `json:"spec"`

	// +optional
	Status JWTAuthRoleStatus `json:"status,omitzero"`
}

// GetResourceSpec implements commonv1alpha1.Managed.
func (j *JWTAuthRole) GetResourceSpec() *commonv1alpha1.ResourceSpec { return &j.Spec.ResourceSpec }

// GetResourceStatus implements commonv1alpha1.Managed.
func (j *JWTAuthRole) GetResourceStatus() *commonv1alpha1.ResourceStatus {
	return &j.Status.ResourceStatus
}

// +kubebuilder:object:root=true

// JWTAuthRoleList contains a list of JWTAuthRole.
type JWTAuthRoleList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []JWTAuthRole `json:"items"`
}

func init() {
	SchemeBuilder.Register(&JWTAuthRole{}, &JWTAuthRoleList{})
}
