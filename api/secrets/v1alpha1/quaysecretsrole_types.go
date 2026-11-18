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

// The kind of Quay account a robot account is created in: organization or user.
// +kubebuilder:validation:Enum=organization;user
type QuayNamespaceType string

const (
	QuayNamespaceTypeOrganization QuayNamespaceType = "organization"
	QuayNamespaceTypeUser         QuayNamespaceType = "user"
)

// A repository permission granted to a robot account: admin, read or write.
// +kubebuilder:validation:Enum=admin;read;write
type QuayPermission string

const (
	QuayPermissionAdmin QuayPermission = "admin"
	QuayPermissionRead  QuayPermission = "read"
	QuayPermissionWrite QuayPermission = "write"
)

// A team role granted to a robot account: admin, creator or member.
// +kubebuilder:validation:Enum=admin;creator;member
type QuayTeamRole string

const (
	QuayTeamRoleAdmin   QuayTeamRole = "admin"
	QuayTeamRoleCreator QuayTeamRole = "creator"
	QuayTeamRoleMember  QuayTeamRole = "member"
)

// The robot account a Quay role or static role manages.
type QuayRobotAccount struct {
	// namespaceName is the Quay organization or user the robot account belongs to (`namespace_name`). Example: `billing`.
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=256
	// +required
	NamespaceName string `json:"namespaceName"`

	// namespaceType is the kind of account namespaceName is (`namespace_type`). Allowed: organization, user. Defaults to organization.
	// +optional
	NamespaceType QuayNamespaceType `json:"namespaceType,omitempty"`

	// createRepositories allows the robot account to create repositories (`create_repositories`). Defaults to false.
	// +optional
	CreateRepositories *bool `json:"createRepositories,omitempty"`

	// defaultPermission is granted on every current repository of an organization, and on repositories created later through an organization prototype (`default_permission`). Allowed: admin, read, write. Entries in repositories win.
	// +optional
	DefaultPermission QuayPermission `json:"defaultPermission,omitempty"`

	// teams maps organization team names to the role the robot account gets in that team (`teams`). Allowed roles: admin, creator, member. Up to 64. Example: `{deployers: member}`.
	// +kubebuilder:validation:MaxProperties=64
	// +optional
	Teams map[string]QuayTeamRole `json:"teams,omitempty"`

	// repositories maps repository names to the permission the robot account gets on that repository (`repositories`). Allowed: admin, read, write. Up to 256. Example: `{billing-api: write}`.
	// +kubebuilder:validation:MaxProperties=256
	// +optional
	Repositories map[string]QuayPermission `json:"repositories,omitempty"`
}

// +kubebuilder:validation:XValidation:rule="has(self.mountPath) == has(oldSelf.mountPath) && (!has(self.mountPath) || self.mountPath == oldSelf.mountPath)",message="mountPath is immutable"
// +kubebuilder:validation:XValidation:rule="has(self.name) == has(oldSelf.name) && (!has(self.name) || self.name == oldSelf.name)",message="name is immutable"

// spec is the desired role of the Quay secrets plugin at `<mountPath>/roles/<name>`. Each credential request creates a new robot account that is removed when its lease ends.
type QuaySecretsRoleSpec struct {
	commonv1alpha1.ResourceSpec `json:",inline"`

	// mountPath is the path the Quay secrets plugin is mounted at. Defaults to `quay`. Immutable. Example: `quay`.
	// +kubebuilder:default=quay
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=256
	// +kubebuilder:validation:Pattern=`^[A-Za-z0-9_.-]+(/[A-Za-z0-9_.-]+)*$`
	// +optional
	MountPath string `json:"mountPath,omitempty"`

	// name is the role name in OpenBao, lower case because OpenBao lowercases role names. Defaults to metadata.name. Immutable. Example: `ci-push`.
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=128
	// +kubebuilder:validation:Pattern=`^[a-z0-9_]([a-z0-9_.-]*[a-z0-9_])?$`
	// +optional
	Name string `json:"name,omitempty"`

	QuayRobotAccount `json:",inline"`

	// ttl is the default lease duration of issued robot accounts (`ttl`), a duration. Defaults to the mount's default lease TTL. Example: `1h`.
	// +kubebuilder:validation:Type=string
	// +kubebuilder:validation:Pattern=`^(0|([0-9]+(\.[0-9]+)?(ns|us|ms|s|m|h))+)$`
	// +optional
	TTL *metav1.Duration `json:"ttl,omitempty"`

	// maxTTL is the maximum lease duration of issued robot accounts (`max_ttl`), a duration. Defaults to the mount's max lease TTL. Example: `24h`.
	// +kubebuilder:validation:Type=string
	// +kubebuilder:validation:Pattern=`^(0|([0-9]+(\.[0-9]+)?(ns|us|ms|s|m|h))+)$`
	// +optional
	MaxTTL *metav1.Duration `json:"maxTTL,omitempty"`
}

// status is the observed state of the Quay secrets role.
type QuaySecretsRoleStatus struct {
	commonv1alpha1.ResourceStatus `json:",inline"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:categories=obco;obco-secrets
// +kubebuilder:printcolumn:name="Ready",type=string,JSONPath=`.status.conditions[?(@.type=="Ready")].status`
// +kubebuilder:printcolumn:name="Synced",type=string,JSONPath=`.status.conditions[?(@.type=="Synced")].status`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// QuaySecretsRole manages a role of a Quay secrets plugin mount (external plugin) at `<mount>/roles/<name>`: each request to `<mount>/creds/<name>` creates a short-lived robot account with the listed permissions.
//
// Example:
//
//	spec:
//	  connectionRef:
//	    name: openbao
//	  mountPath: quay
//	  name: ci-push
//	  namespaceName: billing
//	  repositories:
//	    billing-api: write
type QuaySecretsRole struct {
	metav1.TypeMeta `json:",inline"`

	// +optional
	metav1.ObjectMeta `json:"metadata,omitzero"`

	// +required
	Spec QuaySecretsRoleSpec `json:"spec"`

	// +optional
	Status QuaySecretsRoleStatus `json:"status,omitzero"`
}

// GetResourceSpec implements commonv1alpha1.Managed.
func (o *QuaySecretsRole) GetResourceSpec() *commonv1alpha1.ResourceSpec { return &o.Spec.ResourceSpec }

// GetResourceStatus implements commonv1alpha1.Managed.
func (o *QuaySecretsRole) GetResourceStatus() *commonv1alpha1.ResourceStatus {
	return &o.Status.ResourceStatus
}

// +kubebuilder:object:root=true

// QuaySecretsRoleList contains a list of QuaySecretsRole.
type QuaySecretsRoleList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []QuaySecretsRole `json:"items"`
}

func init() {
	SchemeBuilder.Register(&QuaySecretsRole{}, &QuaySecretsRoleList{})
}
