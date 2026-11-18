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

// +kubebuilder:validation:XValidation:rule="has(self.mountPath) == has(oldSelf.mountPath) && (!has(self.mountPath) || self.mountPath == oldSelf.mountPath)",message="mountPath is immutable"
// +kubebuilder:validation:XValidation:rule="has(self.name) == has(oldSelf.name) && (!has(self.name) || self.name == oldSelf.name)",message="name is immutable"
// +kubebuilder:validation:XValidation:rule="has(self.installationID) || has(self.organizationName)",message="set installationID or organizationName"

// spec is the desired permission set of the GitHub secrets plugin at `<mountPath>/permissionset/<name>`. Installation tokens issued for it are limited to the listed repositories and permissions; leaving them unset grants everything the GitHub App installation can access. Set installationID or organizationName.
type GitHubSecretsRoleSpec struct {
	commonv1alpha1.ResourceSpec `json:",inline"`

	// mountPath is the path the GitHub secrets plugin is mounted at. Defaults to `github`. Immutable. Example: `github`.
	// +kubebuilder:default=github
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=256
	// +kubebuilder:validation:Pattern=`^[A-Za-z0-9_.-]+(/[A-Za-z0-9_.-]+)*$`
	// +optional
	MountPath string `json:"mountPath,omitempty"`

	// name is the permission set name in OpenBao. Defaults to metadata.name. Immutable. Example: `ci-read`.
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=128
	// +kubebuilder:validation:Pattern=`^[A-Za-z0-9_]([A-Za-z0-9_.-]*[A-Za-z0-9_])?$`
	// +optional
	Name string `json:"name,omitempty"`

	// installationID is the ID of the GitHub App installation (`installation_id`); it wins over organizationName. Example: `987654`.
	// +kubebuilder:validation:Minimum=1
	// +optional
	InstallationID *int64 `json:"installationID,omitempty"`

	// organizationName is the GitHub organization that has the GitHub App installed (`org_name`). Example: `example-org`.
	// +kubebuilder:validation:MaxLength=128
	// +optional
	OrganizationName string `json:"organizationName,omitempty"`

	// repositories lists the names of the repositories the installation token can access (`repositories`). Example: `[billing-api]`.
	// +kubebuilder:validation:MaxItems=500
	// +kubebuilder:validation:items:MaxLength=128
	// +listType=set
	// +optional
	Repositories []string `json:"repositories,omitempty"`

	// repositoryIDs lists the IDs of the repositories the installation token can access (`repository_ids`). IDs, unlike names, never change, so prefer them in security-sensitive setups. Example: `[123456789]`.
	// +kubebuilder:validation:MaxItems=500
	// +listType=set
	// +optional
	RepositoryIDs []int64 `json:"repositoryIDs,omitempty"`

	// permissions maps GitHub App permission names to an access level (`permissions`). Up to 64. Example: `{contents: read, pull_requests: write}`.
	// +kubebuilder:validation:MaxProperties=64
	// +optional
	Permissions map[string]string `json:"permissions,omitempty"`

	// skipInaccessibleRepositories leaves out listed repositories the installation cannot access instead of failing the token request (`skip_inaccessible_repositories`). Defaults to false.
	// +optional
	SkipInaccessibleRepositories *bool `json:"skipInaccessibleRepositories,omitempty"`
}

// status is the observed state of the GitHub permission set.
type GitHubSecretsRoleStatus struct {
	commonv1alpha1.ResourceStatus `json:",inline"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:categories=obco;obco-secrets
// +kubebuilder:printcolumn:name="Ready",type=string,JSONPath=`.status.conditions[?(@.type=="Ready")].status`
// +kubebuilder:printcolumn:name="Synced",type=string,JSONPath=`.status.conditions[?(@.type=="Synced")].status`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// GitHubSecretsRole manages a permission set of a GitHub secrets plugin mount (external plugin) at `<mount>/permissionset/<name>`: the repositories and permissions of the installation tokens issued from `<mount>/token/<name>`.
//
// Example:
//
//	spec:
//	  connectionRef:
//	    name: openbao
//	  mountPath: github
//	  name: ci-read
//	  organizationName: example-org
//	  repositories: [billing-api]
//	  permissions: {contents: read}
type GitHubSecretsRole struct {
	metav1.TypeMeta `json:",inline"`

	// +optional
	metav1.ObjectMeta `json:"metadata,omitzero"`

	// +required
	Spec GitHubSecretsRoleSpec `json:"spec"`

	// +optional
	Status GitHubSecretsRoleStatus `json:"status,omitzero"`
}

// GetResourceSpec implements commonv1alpha1.Managed.
func (o *GitHubSecretsRole) GetResourceSpec() *commonv1alpha1.ResourceSpec {
	return &o.Spec.ResourceSpec
}

// GetResourceStatus implements commonv1alpha1.Managed.
func (o *GitHubSecretsRole) GetResourceStatus() *commonv1alpha1.ResourceStatus {
	return &o.Status.ResourceStatus
}

// +kubebuilder:object:root=true

// GitHubSecretsRoleList contains a list of GitHubSecretsRole.
type GitHubSecretsRoleList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []GitHubSecretsRole `json:"items"`
}

func init() {
	SchemeBuilder.Register(&GitHubSecretsRole{}, &GitHubSecretsRoleList{})
}
