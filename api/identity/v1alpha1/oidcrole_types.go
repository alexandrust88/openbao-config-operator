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

// spec is the desired identity token role at `identity/oidc/role/<name>`.
type OIDCRoleSpec struct {
	commonv1alpha1.ResourceSpec `json:",inline"`

	// name is the role name in OpenBao. Defaults to metadata.name. Letters, digits, `_`, `.`, `@` and `-`. Immutable. Example: `ci`.
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=128
	// +kubebuilder:validation:Pattern=`^\w([\w.@-]*\w)?$`
	// +optional
	Name string `json:"name,omitempty"`

	// key is the name of the OIDC key that signs the role's tokens (`key`). The key must exist and its allowedClientIDs must include the role's client ID or `*`. Example: `apps`.
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=128
	// +required
	Key string `json:"key"`

	// template is a JSON template of extra claims using OpenBao identity templating (`template`). JSON text, not base64. Up to 64 KiB. Example: `{"groups": {{identity.entity.groups.names}}}`.
	// +kubebuilder:validation:MaxLength=65536
	// +kubebuilder:validation:Pattern=`^\s*\{`
	// +optional
	Template string `json:"template,omitempty"`

	// ttl is the lifetime of tokens issued for the role (`ttl`), a duration. Must not exceed the key's verificationTTL. Defaults to `24h`. Example: `1h`.
	// +optional
	TTL *metav1.Duration `json:"ttl,omitempty"`

	// clientID is the role's client ID, the `aud` claim of its tokens (`client_id`). Empty lets OpenBao generate one, reported in `status.id`. Example: `ci-runner`.
	// +kubebuilder:validation:MaxLength=128
	// +optional
	ClientID string `json:"clientID,omitempty"`
}

// status is the observed state of the OIDC role; `id` holds the role's client ID.
type OIDCRoleStatus struct {
	commonv1alpha1.ResourceStatus `json:",inline"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:categories=openbao
// +kubebuilder:printcolumn:name="Ready",type=string,JSONPath=`.status.conditions[?(@.type=="Ready")].status`
// +kubebuilder:printcolumn:name="Synced",type=string,JSONPath=`.status.conditions[?(@.type=="Synced")].status`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// OIDCRole manages a role at `identity/oidc/role/<name>` that OpenBao identity tokens are issued against (`identity/oidc/token/<name>`). Use it to give workloads signed JWTs carrying their identity, for example to authenticate to other systems.
//
// Example:
//
//	spec:
//	  connectionRef:
//	    name: openbao
//	  name: ci
//	  key: apps
//	  ttl: 1h
type OIDCRole struct {
	metav1.TypeMeta `json:",inline"`

	// +optional
	metav1.ObjectMeta `json:"metadata,omitzero"`

	// +required
	Spec OIDCRoleSpec `json:"spec"`

	// +optional
	Status OIDCRoleStatus `json:"status,omitzero"`
}

// GetResourceSpec implements commonv1alpha1.Managed.
func (o *OIDCRole) GetResourceSpec() *commonv1alpha1.ResourceSpec { return &o.Spec.ResourceSpec }

// GetResourceStatus implements commonv1alpha1.Managed.
func (o *OIDCRole) GetResourceStatus() *commonv1alpha1.ResourceStatus {
	return &o.Status.ResourceStatus
}

// +kubebuilder:object:root=true

// OIDCRoleList contains a list of OIDCRole.
type OIDCRoleList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []OIDCRole `json:"items"`
}

func init() {
	SchemeBuilder.Register(&OIDCRole{}, &OIDCRoleList{})
}
