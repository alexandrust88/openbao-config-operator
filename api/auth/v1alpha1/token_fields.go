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

// Type of token an auth role or token role issues: default, service or batch.
// +kubebuilder:validation:Enum=default;service;batch
type TokenType string

const (
	TokenTypeDefault TokenType = "default"
	TokenTypeService TokenType = "service"
	TokenTypeBatch   TokenType = "batch"
)

// Token parameters shared by auth method roles and token roles.
type TokenBaseFields struct {
	// Hard cap on the lifetime of issued tokens that renewals cannot extend, as a duration. Unset or 0 means no explicit cap. Maps to `token_explicit_max_ttl`. Example: 24h
	// +optional
	TokenExplicitMaxTTL *metav1.Duration `json:"tokenExplicitMaxTTL,omitempty"`

	// Makes issued tokens periodic: they never expire as long as they are renewed within this duration, and each renewal resets the TTL to it. Unset or 0 means not periodic. Maps to `token_period`. Example: 1h
	// +optional
	TokenPeriod *metav1.Duration `json:"tokenPeriod,omitempty"`

	// Client CIDR blocks or IP addresses allowed to use issued tokens. Unset allows any address. Maps to `token_bound_cidrs`. Example: [10.0.0.0/8, 192.168.1.10]
	// +kubebuilder:validation:MaxItems=64
	// +kubebuilder:validation:items:MaxLength=64
	// +kubebuilder:validation:XValidation:rule="self.all(c, isCIDR(c) || isIP(c))",message="tokenBoundCIDRs entries must be CIDR blocks or IP addresses"
	// +listType=set
	// +optional
	TokenBoundCIDRs []string `json:"tokenBoundCIDRs,omitempty"`

	// Maximum number of requests an issued token may make; 0 or unset means unlimited. Maps to `token_num_uses`. Example: 10
	// +kubebuilder:validation:Minimum=0
	// +optional
	TokenNumUses *int32 `json:"tokenNumUses,omitempty"`

	// Type of token to issue. One of default (the mount's default, normally service), service (renewable, can create child tokens) or batch (lightweight, not renewable). Maps to `token_type`. Example: service
	// +optional
	TokenType TokenType `json:"tokenType,omitempty"`

	// When true, the default policy is not attached to issued tokens; only the listed policies apply. Defaults to false. Maps to `token_no_default_policy`. Example: true
	// +optional
	TokenNoDefaultPolicy *bool `json:"tokenNoDefaultPolicy,omitempty"`
}

// Token parameters of an auth method role.
//
// +kubebuilder:validation:XValidation:rule="!has(self.tokenTTL) || !has(self.tokenMaxTTL) || duration(self.tokenTTL) <= duration(self.tokenMaxTTL)",message="tokenTTL must not exceed tokenMaxTTL"
type TokenFields struct {
	TokenBaseFields `json:",inline"`

	// Initial lifetime of issued tokens, as a duration. Unset or 0 uses the mount or system default TTL; must not exceed tokenMaxTTL. Maps to `token_ttl`. Example: 1h
	// +optional
	TokenTTL *metav1.Duration `json:"tokenTTL,omitempty"`

	// Maximum lifetime of issued tokens including renewals, as a duration. Unset or 0 uses the mount or system max TTL. Maps to `token_max_ttl`. Example: 8h
	// +optional
	TokenMaxTTL *metav1.Duration `json:"tokenMaxTTL,omitempty"`

	// OpenBao policy names attached to issued tokens, in addition to the default policy and the policies from policyRefs. Maps to `token_policies`. Example: [app-read, app-write]
	// +kubebuilder:validation:MaxItems=256
	// +kubebuilder:validation:items:MinLength=1
	// +kubebuilder:validation:items:MaxLength=128
	// +listType=set
	// +optional
	TokenPolicies []string `json:"tokenPolicies,omitempty"`

	// ACLPolicy resources in this Kubernetes namespace whose OpenBao policy names are added to tokenPolicies. The role waits until each ACLPolicy is Ready; each ACLPolicy must use the same connectionRef and namespace as the role. Example: [{name: app-read}]
	// +kubebuilder:validation:MaxItems=32
	// +listType=map
	// +listMapKey=name
	// +optional
	PolicyRefs []commonv1alpha1.LocalObjectReference `json:"policyRefs,omitempty"`

	// When true, issued tokens can only be used from the IP address of the client that logged in. Built-in auth methods only: an external plugin built on an older OpenBao SDK ignores it and never returns it. Defaults to false. Maps to `token_strictly_bind_ip`. Example: true
	// +optional
	TokenStrictlyBindIP *bool `json:"tokenStrictlyBindIP,omitempty"`
}
