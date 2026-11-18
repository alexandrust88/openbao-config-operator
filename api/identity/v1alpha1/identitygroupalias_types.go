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

// +kubebuilder:validation:XValidation:rule="self.aliasName == oldSelf.aliasName",message="aliasName is immutable"
// +kubebuilder:validation:XValidation:rule="[has(self.mountAccessor), has(self.mountPath), has(self.mountRef), has(self.mountSelector)].filter(x, x).size() == 1",message="set exactly one of mountAccessor, mountPath, mountRef and mountSelector"
// +kubebuilder:validation:XValidation:rule="has(self.mountAccessor) == has(oldSelf.mountAccessor) && (!has(self.mountAccessor) || self.mountAccessor == oldSelf.mountAccessor) && has(self.mountPath) == has(oldSelf.mountPath) && (!has(self.mountPath) || self.mountPath == oldSelf.mountPath) && has(self.mountRef) == has(oldSelf.mountRef) && (!has(self.mountRef) || self.mountRef == oldSelf.mountRef) && has(self.mountSelector) == has(oldSelf.mountSelector) && (!has(self.mountSelector) || self.mountSelector == oldSelf.mountSelector)",message="the auth mount reference is immutable"
// +kubebuilder:validation:XValidation:rule="[has(self.groupID), has(self.groupName), has(self.groupRef), has(self.groupSelector)].filter(x, x).size() == 1",message="set exactly one of groupID, groupName, groupRef and groupSelector"

// spec is the desired group alias: the link between a group name reported by an auth method (for example a Keycloak group in the OIDC groups claim) and an external group. OpenBao identifies the alias by the auth mount accessor and the alias name. Set exactly one of mountAccessor, mountPath, mountRef and mountSelector, and exactly one of groupID, groupName, groupRef and groupSelector.
type IdentityGroupAliasSpec struct {
	commonv1alpha1.ResourceSpec `json:",inline"`

	// aliasName is the group name the auth method reports at login (`name`). Immutable. Example: `billing-admins`.
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=512
	// +required
	AliasName string `json:"aliasName"`

	// mountAccessor is the accessor of the auth mount (`mount_accessor`). Immutable. Example: `auth_oidc_1a2b3c4d`.
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=128
	// +optional
	MountAccessor string `json:"mountAccessor,omitempty"`

	// mountPath is the path of the auth mount without the `auth/` prefix; its accessor is looked up in `sys/auth`, which needs read access there. Immutable. Example: `oidc`.
	// +kubebuilder:validation:MaxLength=128
	// +kubebuilder:validation:Pattern=`^[A-Za-z0-9_.-]+(/[A-Za-z0-9_.-]+)*$`
	// +optional
	MountPath string `json:"mountPath,omitempty"`

	// mountRef names an AuthMount in this namespace; its accessor is taken from the AuthMount status. Immutable. Example: `{name: oidc}`.
	// +optional
	MountRef *commonv1alpha1.LocalObjectReference `json:"mountRef,omitempty"`

	// mountSelector selects exactly one AuthMount in this namespace by its labels; its accessor is taken from the AuthMount status. Immutable.
	// +optional
	MountSelector *commonv1alpha1.LabelSelector `json:"mountSelector,omitempty"`

	// groupID is the OpenBao ID of the external group the alias belongs to (`canonical_id`). Example: `5f1c2d3e-4a5b-6c7d-8e9f-0a1b2c3d4e5f`.
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=128
	// +optional
	GroupID string `json:"groupID,omitempty"`

	// groupName is the name of the external group in OpenBao; its ID is looked up by name. Example: `billing-admins`.
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=256
	// +kubebuilder:validation:Pattern=`^[^/\s]+$`
	// +optional
	GroupName string `json:"groupName,omitempty"`

	// groupRef names an IdentityGroup of type external in this namespace; the group ID is taken from its status. Example: `{name: billing-admins}`.
	// +optional
	GroupRef *commonv1alpha1.LocalObjectReference `json:"groupRef,omitempty"`

	// groupSelector selects exactly one IdentityGroup of type external in this namespace by its labels; the group ID is taken from its status.
	// +optional
	GroupSelector *commonv1alpha1.LabelSelector `json:"groupSelector,omitempty"`
}

// status is the observed state of the group alias; `id` holds the OpenBao alias ID.
type IdentityGroupAliasStatus struct {
	commonv1alpha1.ResourceStatus `json:",inline"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:categories=obco;obco-identity
// +kubebuilder:printcolumn:name="Alias",type=string,JSONPath=`.spec.aliasName`
// +kubebuilder:printcolumn:name="Ready",type=string,JSONPath=`.status.conditions[?(@.type=="Ready")].status`
// +kubebuilder:printcolumn:name="Synced",type=string,JSONPath=`.status.conditions[?(@.type=="Synced")].status`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// IdentityGroupAlias links a group of an identity provider to an external OpenBao group (`identity/group-alias`). Use it so that users whose login reports that group (for example in the OIDC groups claim) become members of the external group and get its policies. A group has at most one alias.
//
// Example:
//
//	spec:
//	  connectionRef:
//	    name: openbao
//	  aliasName: billing-admins
//	  mountPath: oidc
//	  groupRef:
//	    name: billing-admins
type IdentityGroupAlias struct {
	metav1.TypeMeta `json:",inline"`

	// +optional
	metav1.ObjectMeta `json:"metadata,omitzero"`

	// +required
	Spec IdentityGroupAliasSpec `json:"spec"`

	// +optional
	Status IdentityGroupAliasStatus `json:"status,omitzero"`
}

// GetResourceSpec implements commonv1alpha1.Managed.
func (a *IdentityGroupAlias) GetResourceSpec() *commonv1alpha1.ResourceSpec {
	return &a.Spec.ResourceSpec
}

// GetResourceStatus implements commonv1alpha1.Managed.
func (a *IdentityGroupAlias) GetResourceStatus() *commonv1alpha1.ResourceStatus {
	return &a.Status.ResourceStatus
}

// +kubebuilder:object:root=true

// IdentityGroupAliasList contains a list of IdentityGroupAlias.
type IdentityGroupAliasList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []IdentityGroupAlias `json:"items"`
}

func init() {
	SchemeBuilder.Register(&IdentityGroupAlias{}, &IdentityGroupAliasList{})
}
