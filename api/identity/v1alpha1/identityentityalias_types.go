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
// +kubebuilder:validation:XValidation:rule="[has(self.entityID), has(self.entityName), has(self.entityRef), has(self.entitySelector)].filter(x, x).size() == 1",message="set exactly one of entityID, entityName, entityRef and entitySelector"

// spec is the desired entity alias: the link between a login name on one auth mount and an entity. OpenBao identifies the alias by the auth mount accessor and the alias name. Set exactly one of mountAccessor, mountPath, mountRef and mountSelector, and exactly one of entityID, entityName, entityRef and entitySelector.
type IdentityEntityAliasSpec struct {
	commonv1alpha1.ResourceSpec `json:",inline"`

	// aliasName is the name the auth method reports at login (`name`), such as a user name, a ServiceAccount UID or the value of the OIDC user claim. Immutable. Example: `alice@example.com`.
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=512
	// +required
	AliasName string `json:"aliasName"`

	// mountAccessor is the accessor of the auth mount (`mount_accessor`). Immutable. Example: `auth_userpass_1a2b3c4d`.
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

	// entityID is the OpenBao ID of the entity the alias belongs to (`canonical_id`). Example: `0b1c6a2e-5d1f-4a7e-9b2e-3c4d5e6f7a8b`.
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=128
	// +optional
	EntityID string `json:"entityID,omitempty"`

	// entityName is the name of the entity in OpenBao; its ID is looked up by name. Example: `alice`.
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=256
	// +kubebuilder:validation:Pattern=`^[^/\s]+$`
	// +optional
	EntityName string `json:"entityName,omitempty"`

	// entityRef names an IdentityEntity in this namespace; the entity ID is taken from its status. Example: `{name: alice}`.
	// +optional
	EntityRef *commonv1alpha1.LocalObjectReference `json:"entityRef,omitempty"`

	// entitySelector selects exactly one IdentityEntity in this namespace by its labels; the entity ID is taken from its status.
	// +optional
	EntitySelector *commonv1alpha1.LabelSelector `json:"entitySelector,omitempty"`

	// customMetadata is free-form key/value data attached to the alias (`custom_metadata`), usable in ACL policy templates. Replaced as a whole; unset clears it. Up to 64 keys. Example: `{department: finance}`.
	// +kubebuilder:validation:MaxProperties=64
	// +optional
	CustomMetadata map[string]string `json:"customMetadata,omitempty"`
}

// status is the observed state of the entity alias; `id` holds the OpenBao alias ID.
type IdentityEntityAliasStatus struct {
	commonv1alpha1.ResourceStatus `json:",inline"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:categories=obco;obco-identity
// +kubebuilder:printcolumn:name="Alias",type=string,JSONPath=`.spec.aliasName`
// +kubebuilder:printcolumn:name="Ready",type=string,JSONPath=`.status.conditions[?(@.type=="Ready")].status`
// +kubebuilder:printcolumn:name="Synced",type=string,JSONPath=`.status.conditions[?(@.type=="Synced")].status`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// IdentityEntityAlias links a login on an auth mount to an OpenBao identity entity (`identity/entity-alias`). Use it to make logins from one or more auth methods resolve to the same entity, so they share its policies and metadata.
//
// Example:
//
//	spec:
//	  connectionRef:
//	    name: openbao
//	  aliasName: alice@example.com
//	  mountPath: oidc
//	  entityRef:
//	    name: alice
type IdentityEntityAlias struct {
	metav1.TypeMeta `json:",inline"`

	// +optional
	metav1.ObjectMeta `json:"metadata,omitzero"`

	// +required
	Spec IdentityEntityAliasSpec `json:"spec"`

	// +optional
	Status IdentityEntityAliasStatus `json:"status,omitzero"`
}

// GetResourceSpec implements commonv1alpha1.Managed.
func (a *IdentityEntityAlias) GetResourceSpec() *commonv1alpha1.ResourceSpec {
	return &a.Spec.ResourceSpec
}

// GetResourceStatus implements commonv1alpha1.Managed.
func (a *IdentityEntityAlias) GetResourceStatus() *commonv1alpha1.ResourceStatus {
	return &a.Status.ResourceStatus
}

// +kubebuilder:object:root=true

// IdentityEntityAliasList contains a list of IdentityEntityAlias.
type IdentityEntityAliasList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []IdentityEntityAlias `json:"items"`
}

func init() {
	SchemeBuilder.Register(&IdentityEntityAlias{}, &IdentityEntityAliasList{})
}
