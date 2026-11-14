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
// +kubebuilder:validation:XValidation:rule="has(self.authMounts) || has(self.authMethodTypes) || has(self.entities) || has(self.groups)",message="set at least one of authMounts, authMethodTypes, entities and groups"

// spec is the desired login MFA enforcement at `identity/mfa/login-enforcement/<name>`: logins through the selected auth mounts or by the selected identities must pass the listed MFA methods. Target lists are written as a whole; targets removed from the spec are removed in OpenBao. Set at least one of authMounts, authMethodTypes, entities and groups.
type MFALoginEnforcementSpec struct {
	commonv1alpha1.ResourceSpec `json:",inline"`

	// name is the enforcement name in OpenBao. Defaults to metadata.name. Letters, digits, `_`, `.`, `@` and `-`. Immutable. Example: `require-totp`.
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=128
	// +kubebuilder:validation:Pattern=`^\w([\w.@-]*\w)?$`
	// +optional
	Name string `json:"name,omitempty"`

	// mfaMethods are the MFA methods a login must pass (`mfa_method_ids`). 1-32 entries. Example: `[{methodRef: {name: totp-main}}]`.
	// +kubebuilder:validation:MinItems=1
	// +kubebuilder:validation:MaxItems=32
	// +listType=atomic
	// +required
	MFAMethods []MFAMethodMatch `json:"mfaMethods"`

	// authMounts are the auth mounts whose logins require MFA (`auth_method_accessors`). Up to 64. Example: `[{mountPath: userpass}]`.
	// +kubebuilder:validation:MaxItems=64
	// +listType=atomic
	// +optional
	AuthMounts []AuthMountMatch `json:"authMounts,omitempty"`

	// authMethodTypes are auth method types whose logins require MFA (`auth_method_types`). At least one mount of each type must exist. Up to 32. Example: `[userpass, oidc]`.
	// +kubebuilder:validation:MaxItems=32
	// +kubebuilder:validation:items:MinLength=1
	// +kubebuilder:validation:items:MaxLength=64
	// +listType=set
	// +optional
	AuthMethodTypes []string `json:"authMethodTypes,omitempty"`

	// entities are the entities whose logins require MFA (`identity_entity_ids`). Up to 256. Example: `[{entityRef: {name: alice}}]`.
	// +kubebuilder:validation:MaxItems=256
	// +listType=atomic
	// +optional
	Entities []EntityMatch `json:"entities,omitempty"`

	// groups are identity groups whose members' logins require MFA (`identity_group_ids`). Up to 256. Example: `[{groupName: admins}]`.
	// +kubebuilder:validation:MaxItems=256
	// +listType=atomic
	// +optional
	Groups []GroupMatch `json:"groups,omitempty"`
}

// status is the observed state of the enforcement; `id` holds the OpenBao enforcement ID.
type MFALoginEnforcementStatus struct {
	commonv1alpha1.ResourceStatus `json:",inline"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:categories=openbao
// +kubebuilder:printcolumn:name="Ready",type=string,JSONPath=`.status.conditions[?(@.type=="Ready")].status`
// +kubebuilder:printcolumn:name="Synced",type=string,JSONPath=`.status.conditions[?(@.type=="Synced")].status`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// MFALoginEnforcement requires login MFA at `identity/mfa/login-enforcement/<name>` for the selected auth mounts, auth method types, entities or groups. Use it with one or more MFAMethod objects to force a second factor on sensitive logins.
//
// Example:
//
//	spec:
//	  connectionRef:
//	    name: openbao
//	  mfaMethods:
//	    - methodRef: {name: totp-main}
//	  authMounts:
//	    - mountPath: userpass
type MFALoginEnforcement struct {
	metav1.TypeMeta `json:",inline"`

	// +optional
	metav1.ObjectMeta `json:"metadata,omitzero"`

	// +required
	Spec MFALoginEnforcementSpec `json:"spec"`

	// +optional
	Status MFALoginEnforcementStatus `json:"status,omitzero"`
}

// GetResourceSpec implements commonv1alpha1.Managed.
func (m *MFALoginEnforcement) GetResourceSpec() *commonv1alpha1.ResourceSpec {
	return &m.Spec.ResourceSpec
}

// GetResourceStatus implements commonv1alpha1.Managed.
func (m *MFALoginEnforcement) GetResourceStatus() *commonv1alpha1.ResourceStatus {
	return &m.Status.ResourceStatus
}

// +kubebuilder:object:root=true

// MFALoginEnforcementList contains a list of MFALoginEnforcement.
type MFALoginEnforcementList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []MFALoginEnforcement `json:"items"`
}

func init() {
	SchemeBuilder.Register(&MFALoginEnforcement{}, &MFALoginEnforcementList{})
}
