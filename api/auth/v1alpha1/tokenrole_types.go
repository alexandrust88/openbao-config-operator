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
// +kubebuilder:validation:XValidation:rule="!has(self.tokenType) || self.tokenType != 'batch' || (has(self.orphan) && self.orphan && has(self.renewable) && !self.renewable && !has(self.tokenPeriod) && !has(self.tokenExplicitMaxTTL))",message="tokenType batch requires orphan true, renewable false and no tokenPeriod or tokenExplicitMaxTTL"

// Desired state of a token store role, written to auth/token/roles/<name>. tokenType batch requires orphan true, renewable false and no tokenPeriod or tokenExplicitMaxTTL.
type TokenRoleSpec struct {
	commonv1alpha1.ResourceSpec `json:",inline"`

	// Name of the token role in OpenBao. Defaults to metadata.name. Case sensitive. Immutable. Example: ci-runner
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=128
	// +kubebuilder:validation:Pattern=`^[A-Za-z0-9_]([A-Za-z0-9_.-]*[A-Za-z0-9_])?$`
	// +optional
	Name string `json:"name,omitempty"`

	// Policies a token created against this role may have. When set, a creator may only request a subset; unset allows any policy of the creating token. Lower case names. Maps to `allowed_policies`. Example: [app-read, app-write]
	// +kubebuilder:validation:MaxItems=256
	// +kubebuilder:validation:items:MaxLength=128
	// +kubebuilder:validation:items:Pattern=`^[^A-Z\s]+$`
	// +listType=set
	// +optional
	AllowedPolicies []string `json:"allowedPolicies,omitempty"`

	// Policies a token created against this role must never have; takes precedence over allowedPolicies. Lower case names. Maps to `disallowed_policies`. Example: [root, admin]
	// +kubebuilder:validation:MaxItems=256
	// +kubebuilder:validation:items:MaxLength=128
	// +kubebuilder:validation:items:Pattern=`^[^A-Z\s]+$`
	// +listType=set
	// +optional
	DisallowedPolicies []string `json:"disallowedPolicies,omitempty"`

	// Glob patterns of policies a token created against this role may have, in addition to allowedPolicies. Lower case. Maps to `allowed_policies_glob`. Example: ["app-*"]
	// +kubebuilder:validation:MaxItems=256
	// +kubebuilder:validation:items:MaxLength=128
	// +kubebuilder:validation:items:Pattern=`^[^A-Z\s]+$`
	// +listType=set
	// +optional
	AllowedPoliciesGlob []string `json:"allowedPoliciesGlob,omitempty"`

	// Glob patterns of policies a token created against this role must never have. Lower case. Maps to `disallowed_policies_glob`. Example: ["admin-*"]
	// +kubebuilder:validation:MaxItems=256
	// +kubebuilder:validation:items:MaxLength=128
	// +kubebuilder:validation:items:Pattern=`^[^A-Z\s]+$`
	// +listType=set
	// +optional
	DisallowedPoliciesGlob []string `json:"disallowedPoliciesGlob,omitempty"`

	// Entity alias names, or glob patterns, a token created against this role may be bound to. OpenBao stores them in lower case. Maps to `allowed_entity_aliases`. Example: ["ci-*"]
	// +kubebuilder:validation:MaxItems=256
	// +kubebuilder:validation:items:MaxLength=256
	// +kubebuilder:validation:items:Pattern=`^[^A-Z]+$`
	// +listType=set
	// +optional
	AllowedEntityAliases []string `json:"allowedEntityAliases,omitempty"`

	// Creates tokens without a parent, so they are not revoked when the creating token is revoked. Defaults to false. Maps to `orphan`. Example: true
	// +optional
	Orphan *bool `json:"orphan,omitempty"`

	// Allows tokens created against this role to be renewed past their initial TTL, up to the max TTL. OpenBao defaults to true. Maps to `renewable`. Example: false
	// +optional
	Renewable *bool `json:"renewable,omitempty"`

	// Suffix appended to the creation path (auth/token/create/<name>/<pathSuffix>) so tokens can be revoked by prefix. Letters, digits, underscore, dot and dash; must not contain "..". Maps to `path_suffix`. Example: v2
	// +kubebuilder:validation:MaxLength=128
	// +kubebuilder:validation:Pattern=`^\w[\w.-]+\w$`
	// +kubebuilder:validation:XValidation:rule="!self.contains('..')",message="pathSuffix must not contain '..'"
	// +optional
	PathSuffix string `json:"pathSuffix,omitempty"`

	// The token role accepts only these token fields. tokenType "default" means the server default (default-service).
	TokenBaseFields `json:",inline"`
}

// Observed state of the token role, written by the operator.
type TokenRoleStatus struct {
	commonv1alpha1.ResourceStatus `json:",inline"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:categories=openbao
// +kubebuilder:printcolumn:name="Ready",type=string,JSONPath=`.status.conditions[?(@.type=="Ready")].status`
// +kubebuilder:printcolumn:name="Synced",type=string,JSONPath=`.status.conditions[?(@.type=="Synced")].status`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// TokenRole manages a role of the OpenBao token store at auth/token/roles/<name>. Use it to let callers create tokens with auth/token/create/<name> under fixed policy, TTL and renewal limits, for example for orchestration tools that mint child tokens.
//
// Example:
//
//	spec:
//	  connectionRef:
//	    name: openbao
//	  allowedPolicies: [app-read]
//	  orphan: true
//	  tokenPeriod: 24h
//	  tokenType: service
type TokenRole struct {
	metav1.TypeMeta `json:",inline"`

	// +optional
	metav1.ObjectMeta `json:"metadata,omitzero"`

	// +required
	Spec TokenRoleSpec `json:"spec"`

	// +optional
	Status TokenRoleStatus `json:"status,omitzero"`
}

// GetResourceSpec implements commonv1alpha1.Managed.
func (t *TokenRole) GetResourceSpec() *commonv1alpha1.ResourceSpec { return &t.Spec.ResourceSpec }

// GetResourceStatus implements commonv1alpha1.Managed.
func (t *TokenRole) GetResourceStatus() *commonv1alpha1.ResourceStatus {
	return &t.Status.ResourceStatus
}

// +kubebuilder:object:root=true

// TokenRoleList contains a list of TokenRole.
type TokenRoleList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []TokenRole `json:"items"`
}

func init() {
	SchemeBuilder.Register(&TokenRole{}, &TokenRoleList{})
}
