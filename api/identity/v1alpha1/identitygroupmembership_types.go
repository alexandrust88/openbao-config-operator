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

// +kubebuilder:validation:XValidation:rule="[has(self.groupID), has(self.groupName), has(self.groupRef), has(self.groupSelector)].filter(x, x).size() == 1",message="set exactly one of groupID, groupName, groupRef and groupSelector"
// +kubebuilder:validation:XValidation:rule="has(self.groupID) == has(oldSelf.groupID) && (!has(self.groupID) || self.groupID == oldSelf.groupID) && has(self.groupName) == has(oldSelf.groupName) && (!has(self.groupName) || self.groupName == oldSelf.groupName) && has(self.groupRef) == has(oldSelf.groupRef) && (!has(self.groupRef) || self.groupRef == oldSelf.groupRef) && has(self.groupSelector) == has(oldSelf.groupSelector) && (!has(self.groupSelector) || self.groupSelector == oldSelf.groupSelector)",message="the group reference is immutable"
// +kubebuilder:validation:XValidation:rule="[has(self.entityID), has(self.entityName), has(self.entityRef), has(self.entitySelector), has(self.memberGroupID), has(self.memberGroupName), has(self.memberGroupRef), has(self.memberGroupSelector)].filter(x, x).size() == 1",message="set exactly one of entityID, entityName, entityRef, entitySelector, memberGroupID, memberGroupName, memberGroupRef and memberGroupSelector"

// spec selects one internal group and one member (entity or group) source. Each IdentityGroupMembership adds and removes only the members it selects; members added in any other way are left alone. Set exactly one group field and exactly one member field.
type IdentityGroupMembershipSpec struct {
	commonv1alpha1.ResourceSpec `json:",inline"`

	// groupID is the OpenBao ID of the internal group. Immutable. Example: `5f1c2d3e-4a5b-6c7d-8e9f-0a1b2c3d4e5f`.
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=128
	// +optional
	GroupID string `json:"groupID,omitempty"`

	// groupName is the name of the internal group in OpenBao; its ID is looked up by name. Immutable. Example: `billing-admins`.
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=256
	// +kubebuilder:validation:Pattern=`^[^/\s]+$`
	// +optional
	GroupName string `json:"groupName,omitempty"`

	// groupRef names an IdentityGroup in this namespace; the group ID is taken from its status. Immutable. Example: `{name: billing-admins}`.
	// +optional
	GroupRef *commonv1alpha1.LocalObjectReference `json:"groupRef,omitempty"`

	// groupSelector selects exactly one IdentityGroup in this namespace by its labels; the group ID is taken from its status. Immutable.
	// +optional
	GroupSelector *commonv1alpha1.LabelSelector `json:"groupSelector,omitempty"`

	// entityID is the OpenBao ID of an entity to add (`member_entity_ids`). Example: `0b1c6a2e-5d1f-4a7e-9b2e-3c4d5e6f7a8b`.
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=128
	// +optional
	EntityID string `json:"entityID,omitempty"`

	// entityName is the name of an entity in OpenBao to add; its ID is looked up by name. Example: `alice`.
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=256
	// +kubebuilder:validation:Pattern=`^[^/\s]+$`
	// +optional
	EntityName string `json:"entityName,omitempty"`

	// entityRef names an IdentityEntity in this namespace to add; the entity ID is taken from its status. Example: `{name: alice}`.
	// +optional
	EntityRef *commonv1alpha1.LocalObjectReference `json:"entityRef,omitempty"`

	// entitySelector adds every IdentityEntity in this namespace that carries these labels.
	// +optional
	EntitySelector *commonv1alpha1.LabelSelector `json:"entitySelector,omitempty"`

	// memberGroupID is the OpenBao ID of a group to add as a subgroup (`member_group_ids`). Example: `7a8b9c0d-1e2f-3a4b-5c6d-7e8f9a0b1c2d`.
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=128
	// +optional
	MemberGroupID string `json:"memberGroupID,omitempty"`

	// memberGroupName is the name of a group in OpenBao to add as a subgroup; its ID is looked up by name. Example: `billing-oncall`.
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=256
	// +kubebuilder:validation:Pattern=`^[^/\s]+$`
	// +optional
	MemberGroupName string `json:"memberGroupName,omitempty"`

	// memberGroupRef names an IdentityGroup in this namespace to add as a subgroup; the group ID is taken from its status. Example: `{name: billing-oncall}`.
	// +optional
	MemberGroupRef *commonv1alpha1.LocalObjectReference `json:"memberGroupRef,omitempty"`

	// memberGroupSelector adds every IdentityGroup in this namespace that carries these labels as a subgroup.
	// +optional
	MemberGroupSelector *commonv1alpha1.LabelSelector `json:"memberGroupSelector,omitempty"`
}

// status is the observed state of the membership: the members it added, so that only those are removed later.
type IdentityGroupMembershipStatus struct {
	commonv1alpha1.ResourceStatus `json:",inline"`

	// groupID is the OpenBao ID of the group the members were added to.
	// +optional
	GroupID string `json:"groupID,omitempty"`

	// memberEntityIDs are the entity IDs this resource added to the group.
	// +optional
	MemberEntityIDs []string `json:"memberEntityIDs,omitempty"`

	// memberGroupIDs are the subgroup IDs this resource added to the group.
	// +optional
	MemberGroupIDs []string `json:"memberGroupIDs,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:categories=obco;obco-identity
// +kubebuilder:printcolumn:name="Group",type=string,JSONPath=`.status.groupID`,priority=1
// +kubebuilder:printcolumn:name="Ready",type=string,JSONPath=`.status.conditions[?(@.type=="Ready")].status`
// +kubebuilder:printcolumn:name="Synced",type=string,JSONPath=`.status.conditions[?(@.type=="Synced")].status`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// IdentityGroupMembership adds entities or subgroups to an internal OpenBao group (`member_entity_ids`, `member_group_ids` of `identity/group/id/<id>`). Several may select the same member: it stays while any of them selects it. External groups take no memberships. With deletionPolicy Delete, deletion removes only the members it added.
//
// Example:
//
//	spec:
//	  connectionRef:
//	    name: openbao
//	  groupRef:
//	    name: billing-admins
//	  entityRef:
//	    name: alice
type IdentityGroupMembership struct {
	metav1.TypeMeta `json:",inline"`

	// +optional
	metav1.ObjectMeta `json:"metadata,omitzero"`

	// +required
	Spec IdentityGroupMembershipSpec `json:"spec"`

	// +optional
	Status IdentityGroupMembershipStatus `json:"status,omitzero"`
}

// GetResourceSpec implements commonv1alpha1.Managed.
func (m *IdentityGroupMembership) GetResourceSpec() *commonv1alpha1.ResourceSpec {
	return &m.Spec.ResourceSpec
}

// GetResourceStatus implements commonv1alpha1.Managed.
func (m *IdentityGroupMembership) GetResourceStatus() *commonv1alpha1.ResourceStatus {
	return &m.Status.ResourceStatus
}

// +kubebuilder:object:root=true

// IdentityGroupMembershipList contains a list of IdentityGroupMembership.
type IdentityGroupMembershipList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []IdentityGroupMembership `json:"items"`
}

func init() {
	SchemeBuilder.Register(&IdentityGroupMembership{}, &IdentityGroupMembershipList{})
}
