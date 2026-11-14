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

// The type of an identity group: internal or external.
// +kubebuilder:validation:Enum=internal;external
type GroupType string

const (
	// GroupTypeInternal groups list their members explicitly, through GroupMembership resources.
	GroupTypeInternal GroupType = "internal"
	// GroupTypeExternal groups get their members from an identity provider, through a GroupAlias.
	GroupTypeExternal GroupType = "external"
)

// +kubebuilder:validation:XValidation:rule="has(self.name) == has(oldSelf.name) && (!has(self.name) || self.name == oldSelf.name)",message="name is immutable"
// +kubebuilder:validation:XValidation:rule="has(self.type) == has(oldSelf.type) && (!has(self.type) || self.type == oldSelf.type)",message="type is immutable"

// spec is the desired identity group, written at `identity/group/name/<name>`. Members are not part of the group spec: they are added by GroupMembership objects (internal groups) or a GroupAlias (external groups).
type GroupSpec struct {
	commonv1alpha1.ResourceSpec `json:",inline"`

	// name is the group name in OpenBao (`name`). Defaults to metadata.name. No slashes or whitespace. Immutable. Example: `billing-admins`.
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=256
	// +kubebuilder:validation:Pattern=`^[^/\s]+$`
	// +optional
	Name string `json:"name,omitempty"`

	// type is `internal` (members listed in OpenBao) or `external` (members come from an identity provider through a GroupAlias) (`type`). Defaults to internal. Immutable.
	// +kubebuilder:default=internal
	// +optional
	Type GroupType `json:"type,omitempty"`

	// policies are ACL policy names granted to the members of the group (`policies`). Lower case; `root` is not allowed. Up to 64. Example: `[billing-admin]`.
	// +kubebuilder:validation:MaxItems=64
	// +kubebuilder:validation:items:MinLength=1
	// +kubebuilder:validation:items:MaxLength=128
	// +kubebuilder:validation:items:Pattern=`^[^A-Z,\s]+$`
	// +kubebuilder:validation:XValidation:rule="!self.exists(p, p == 'root')",message="policies cannot contain root"
	// +listType=set
	// +optional
	Policies []string `json:"policies,omitempty"`

	// metadata is free-form key/value data attached to the group (`metadata`), usable in ACL policy templates. Written as a whole: keys not listed, including keys added outside the operator, are removed. Up to 61 keys of `[a-zA-Z0-9=/+_-]`; the prefix `openbaoconfig-` holds the operator's ownership marker and is reserved. Example: `{owner: billing}`.
	// +kubebuilder:validation:MaxProperties=61
	// +kubebuilder:validation:XValidation:rule="self.all(k, k.matches('^[a-zA-Z0-9=/+_-]{1,128}$') && !k.startsWith('openbaoconfig-') && !k.startsWith('vault-') && size(self[k]) <= 512)",message="metadata keys are 1-128 characters of [a-zA-Z0-9=/+_-] without the reserved prefixes openbaoconfig- and vault-, values at most 512 characters"
	// +optional
	Metadata map[string]string `json:"metadata,omitempty"`
}

// status is the observed state of the group; `id` holds the OpenBao group ID.
type GroupStatus struct {
	commonv1alpha1.ResourceStatus `json:",inline"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:categories=openbao
// +kubebuilder:printcolumn:name="Type",type=string,JSONPath=`.spec.type`
// +kubebuilder:printcolumn:name="Ready",type=string,JSONPath=`.status.conditions[?(@.type=="Ready")].status`
// +kubebuilder:printcolumn:name="Synced",type=string,JSONPath=`.status.conditions[?(@.type=="Synced")].status`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// Group manages an OpenBao identity group at `identity/group/name/<name>`, which grants policies to its members. Use GroupMembership to add entities or subgroups to an internal group, or a GroupAlias to map an external group from an identity provider.
//
// Example:
//
//	spec:
//	  connectionRef:
//	    name: openbao
//	  name: billing-admins
//	  type: internal
//	  policies: [billing-admin]
type Group struct {
	metav1.TypeMeta `json:",inline"`

	// +optional
	metav1.ObjectMeta `json:"metadata,omitzero"`

	// +required
	Spec GroupSpec `json:"spec"`

	// +optional
	Status GroupStatus `json:"status,omitzero"`
}

// GetResourceSpec implements commonv1alpha1.Managed.
func (g *Group) GetResourceSpec() *commonv1alpha1.ResourceSpec { return &g.Spec.ResourceSpec }

// GetResourceStatus implements commonv1alpha1.Managed.
func (g *Group) GetResourceStatus() *commonv1alpha1.ResourceStatus { return &g.Status.ResourceStatus }

// +kubebuilder:object:root=true

// GroupList contains a list of Group.
type GroupList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []Group `json:"items"`
}

func init() {
	SchemeBuilder.Register(&Group{}, &GroupList{})
}
