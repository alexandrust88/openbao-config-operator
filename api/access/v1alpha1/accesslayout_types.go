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

// The level a role gets in one OpenBao namespace.
type AccessRoleLevel struct {
	// OpenBao namespace, relative to spec.namespace; empty (default) means spec.namespace itself. Each namespace may appear once per role, and must have every auth method in spec.authMounts. Example: team-a
	// +kubebuilder:default=""
	// +kubebuilder:validation:MaxLength=255
	// +kubebuilder:validation:Pattern=`^([A-Za-z0-9_-]+(/[A-Za-z0-9_-]+)*)?$`
	// +optional
	Namespace string `json:"namespace"`

	// Name of the AccessLevel, in the same Kubernetes namespace, granted on spec.paths in this OpenBao namespace. Example: writer
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=253
	// +required
	Level string `json:"level"`
}

// One row of the layout matrix: a role of people and its level in each OpenBao namespace.
type AccessRole struct {
	// Name of the role: lowercase letters, digits and "-", starting with a letter, at most 32 characters. It is part of the policy and group names and replaces {role} in the group alias. Example: developer
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=32
	// +kubebuilder:validation:Pattern=`^[a-z][a-z0-9-]{0,31}$`
	// +required
	Name string `json:"name"`

	// Level of the role in each OpenBao namespace, at most 32 entries keyed by namespace. A namespace that is not listed gets nothing for this role.
	// +kubebuilder:validation:MaxItems=32
	// +listType=map
	// +listMapKey=namespace
	// +optional
	Levels []AccessRoleLevel `json:"levels,omitempty"`
}

// +kubebuilder:validation:XValidation:rule="!has(self.groupAliasTemplate) || self.groupAliasTemplate.contains('{role}')",message="groupAliasTemplate must contain {role}"
// +kubebuilder:validation:XValidation:rule="!has(self.groupAliasTemplate) || self.groupAliasTemplate.contains('{org}') == (has(self.organizations) && size(self.organizations) > 0)",message="groupAliasTemplate contains {org} exactly when organizations are set"
// +kubebuilder:validation:XValidation:rule="self.paths.all(p, !p.path.contains('{sa.') && (!has(p.metadataPath) || !p.metadataPath.contains('{sa.')))",message="{sa.namespace} and {sa.name} are only valid in AccessGrant rules"
// +kubebuilder:validation:XValidation:rule="!self.paths.exists(p, p.path.contains('{org}') || (has(p.metadataPath) && p.metadataPath.contains('{org}'))) || (has(self.organizations) && size(self.organizations) > 0)",message="paths that use {org} require organizations"

// AccessLayoutSpec gives people, by identity provider group, access to KV version 2 secrets. For each role, OpenBao namespace and organization it writes one ACL policy, layout_<namespace>_<name>_<role>[_<org>], and for each auth method one external group of the same name plus _<authMount>, with that policy and a group alias that matches the identity provider group. An update that deletes a policy or group the layout created before (a role, namespace, organization or auth mount removed from the spec) requires the annotation openbaoconfig.io/allow-disruptive-update: "true"; narrowing a level or a path rewrites the policies without it.
type AccessLayoutSpec struct {
	commonv1alpha1.ResourceSpec `json:",inline"`

	// Paths of the OIDC or JWT auth methods people log in with, without the auth/ prefix, 1 to 4 entries. Each gets its own external group and group alias per policy; every OpenBao namespace of the layout must have each of them. Example: [oidc]
	// +kubebuilder:validation:MinItems=1
	// +kubebuilder:validation:MaxItems=4
	// +kubebuilder:validation:items:MaxLength=64
	// +kubebuilder:validation:items:Pattern=`^[A-Za-z0-9_-]+$`
	// +listType=set
	// +required
	AuthMounts []string `json:"authMounts"`

	// When set, every role is repeated once per organization: {org} in paths and in groupAliasTemplate takes each value. Required when a path uses {org}. Lowercase letters, digits and "-", at most 40 characters. Example: [acme, globex]
	// +kubebuilder:validation:MaxItems=32
	// +kubebuilder:validation:items:MaxLength=40
	// +kubebuilder:validation:items:Pattern=`^[a-z0-9]([a-z0-9-]{0,38}[a-z0-9])?$`
	// +listType=set
	// +optional
	Organizations []string `json:"organizations,omitempty"`

	// Name of the group alias, which must equal the group name the identity provider sends in its groups claim; maps to the alias `name`. {role} is replaced by the role name and {org} by the organization; it must contain {role}, and {org} exactly when organizations are set. Default: "{role}", or "{org}_{role}" when organizations are set. Example: vault-{org}-{role}
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=128
	// +kubebuilder:validation:Pattern=`^([A-Za-z0-9._-]|\{role\}|\{org\})+$`
	// +optional
	GroupAliasTemplate string `json:"groupAliasTemplate,omitempty"`

	// Secret paths on KV version 2 mounts that each role's level applies to, 1 to 8 entries. {sa.namespace} and {sa.name} are not allowed here (they only exist for ServiceAccount logins).
	// +kubebuilder:validation:MinItems=1
	// +kubebuilder:validation:MaxItems=8
	// +required
	Paths []AccessPath `json:"paths"`

	// The access matrix: for each role, its level per OpenBao namespace, 1 to 16 roles keyed by name.
	// +kubebuilder:validation:MinItems=1
	// +kubebuilder:validation:MaxItems=16
	// +listType=map
	// +listMapKey=name
	// +required
	Roles []AccessRole `json:"roles"`
}

// AccessLayoutStatus is the observed state of an AccessLayout.
type AccessLayoutStatus struct {
	commonv1alpha1.ResourceStatus `json:",inline"`

	// Every OpenBao object the layout owns: its ACL policies and external groups with their aliases. With deletionPolicy Delete, deleting the layout deletes exactly these.
	// +optional
	Children []AccessChild `json:"children,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:categories=openbao
// +kubebuilder:printcolumn:name="Ready",type=string,JSONPath=`.status.conditions[?(@.type=="Ready")].status`
// +kubebuilder:printcolumn:name="Synced",type=string,JSONPath=`.status.conditions[?(@.type=="Synced")].status`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// AccessLayout gives people, by identity provider group, access to KV version 2 secrets (the human access model). For each role, OpenBao namespace and organization it writes an ACL policy, sys/policies/acl/layout_<namespace>_<name>_<role>[_<org>], and per auth mount an external group identity/group/name/<policy>_<mount> with that policy and a group alias matching the identity provider group. An update that deletes a policy or group the layout created before needs the annotation openbaoconfig.io/allow-disruptive-update: "true". Default deletionPolicy Orphan keeps the objects.
//
// Example:
//
//	spec:
//	  connectionRef:
//	    name: openbao
//	  authMounts: [oidc]
//	  paths:
//	    - mount: secret
//	      path: team-a/*
//	  roles:
//	    - name: developer
//	      levels:
//	        - level: writer
type AccessLayout struct {
	metav1.TypeMeta `json:",inline"`

	// +optional
	metav1.ObjectMeta `json:"metadata,omitzero"`

	// Desired state: the auth mounts, secret paths and the role-by-namespace access matrix.
	// +required
	Spec AccessLayoutSpec `json:"spec"`

	// Observed state reported by the operator: conditions and the owned OpenBao objects.
	// +optional
	Status AccessLayoutStatus `json:"status,omitzero"`
}

// GetResourceSpec implements commonv1alpha1.Managed.
func (l *AccessLayout) GetResourceSpec() *commonv1alpha1.ResourceSpec { return &l.Spec.ResourceSpec }

// GetResourceStatus implements commonv1alpha1.Managed.
func (l *AccessLayout) GetResourceStatus() *commonv1alpha1.ResourceStatus {
	return &l.Status.ResourceStatus
}

// +kubebuilder:object:root=true

// AccessLayoutList contains a list of AccessLayout.
type AccessLayoutList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []AccessLayout `json:"items"`
}

func init() {
	SchemeBuilder.Register(&AccessLayout{}, &AccessLayoutList{})
}
