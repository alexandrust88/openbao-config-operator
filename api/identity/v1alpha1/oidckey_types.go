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

// The algorithm a named key signs identity tokens with.
// +kubebuilder:validation:Enum=RS256;RS384;RS512;ES256;ES384;ES512;EdDSA
type OIDCSigningAlgorithm string

// +kubebuilder:validation:XValidation:rule="has(self.name) == has(oldSelf.name) && (!has(self.name) || self.name == oldSelf.name)",message="name is immutable"

// spec is the desired named signing key at `identity/oidc/key/<name>`. The key `default` exists on every server; an OIDCKey may manage its settings, but deleting the resource never deletes it.
type OIDCKeySpec struct {
	commonv1alpha1.ResourceSpec `json:",inline"`

	// name is the key name in OpenBao. Defaults to metadata.name. Letters, digits, `_`, `.`, `@` and `-`. Immutable. Example: `apps`.
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=128
	// +kubebuilder:validation:Pattern=`^\w([\w.@-]*\w)?$`
	// +optional
	Name string `json:"name,omitempty"`

	// rotationPeriod is how often OpenBao generates a new key pair (`rotation_period`), a duration. Defaults to `24h`. Example: `168h`.
	// +optional
	RotationPeriod *metav1.Duration `json:"rotationPeriod,omitempty"`

	// verificationTTL is how long the public part of a rotated key stays published for verification (`verification_ttl`), a duration. At most ten times rotationPeriod. Defaults to `24h`. Example: `24h`.
	// +optional
	VerificationTTL *metav1.Duration `json:"verificationTTL,omitempty"`

	// algorithm is the signing algorithm (`algorithm`). Allowed: RS256, RS384, RS512, ES256, ES384, ES512, EdDSA. Defaults to RS256.
	// +optional
	Algorithm OIDCSigningAlgorithm `json:"algorithm,omitempty"`

	// allowedClientIDs are the client IDs of OIDC roles and clients allowed to sign with this key (`allowed_client_ids`). `*` allows all; an empty list allows none. Up to 256. Example: `["*"]`.
	// +kubebuilder:validation:MaxItems=256
	// +kubebuilder:validation:items:MinLength=1
	// +kubebuilder:validation:items:MaxLength=128
	// +listType=set
	// +optional
	AllowedClientIDs []string `json:"allowedClientIDs,omitempty"`
}

// status is the observed state of the OIDC key.
type OIDCKeyStatus struct {
	commonv1alpha1.ResourceStatus `json:",inline"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:categories=obco;obco-identity
// +kubebuilder:printcolumn:name="Ready",type=string,JSONPath=`.status.conditions[?(@.type=="Ready")].status`
// +kubebuilder:printcolumn:name="Synced",type=string,JSONPath=`.status.conditions[?(@.type=="Synced")].status`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// OIDCKey manages a named key at `identity/oidc/key/<name>` that signs OpenBao identity tokens and OIDC provider tokens. OIDCRole and OIDCClient objects name it in their key field.
//
// Example:
//
//	spec:
//	  connectionRef:
//	    name: openbao
//	  name: apps
//	  algorithm: ES256
//	  rotationPeriod: 168h
//	  allowedClientIDs: ["*"]
type OIDCKey struct {
	metav1.TypeMeta `json:",inline"`

	// +optional
	metav1.ObjectMeta `json:"metadata,omitzero"`

	// +required
	Spec OIDCKeySpec `json:"spec"`

	// +optional
	Status OIDCKeyStatus `json:"status,omitzero"`
}

// GetResourceSpec implements commonv1alpha1.Managed.
func (o *OIDCKey) GetResourceSpec() *commonv1alpha1.ResourceSpec { return &o.Spec.ResourceSpec }

// GetResourceStatus implements commonv1alpha1.Managed.
func (o *OIDCKey) GetResourceStatus() *commonv1alpha1.ResourceStatus { return &o.Status.ResourceStatus }

// +kubebuilder:object:root=true

// OIDCKeyList contains a list of OIDCKey.
type OIDCKeyList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []OIDCKey `json:"items"`
}

func init() {
	SchemeBuilder.Register(&OIDCKey{}, &OIDCKeyList{})
}
