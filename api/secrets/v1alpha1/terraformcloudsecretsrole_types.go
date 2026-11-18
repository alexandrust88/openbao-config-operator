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

// The kind of Terraform API token a role issues.
// +kubebuilder:validation:Enum=organization;team;team_legacy;user
type TerraformCloudCredentialType string

const (
	TerraformCloudCredentialTypeOrganization TerraformCloudCredentialType = "organization"
	TerraformCloudCredentialTypeTeam         TerraformCloudCredentialType = "team"
	TerraformCloudCredentialTypeTeamLegacy   TerraformCloudCredentialType = "team_legacy"
	TerraformCloudCredentialTypeUser         TerraformCloudCredentialType = "user"
)

// +kubebuilder:validation:XValidation:rule="has(self.mountPath) == has(oldSelf.mountPath) && (!has(self.mountPath) || self.mountPath == oldSelf.mountPath)",message="mountPath is immutable"
// +kubebuilder:validation:XValidation:rule="has(self.name) == has(oldSelf.name) && (!has(self.name) || self.name == oldSelf.name)",message="name is immutable"
// +kubebuilder:validation:XValidation:rule="has(self.credentialType) == has(oldSelf.credentialType) && (!has(self.credentialType) || self.credentialType == oldSelf.credentialType)",message="credentialType is immutable"
// +kubebuilder:validation:XValidation:rule="!has(self.userID) || !(has(self.organization) || has(self.teamID))",message="userID cannot be combined with organization or teamID"
// +kubebuilder:validation:XValidation:rule="has(self.organization) || has(self.teamID) || has(self.userID)",message="set organization, teamID or userID"
// +kubebuilder:validation:XValidation:rule="!has(self.credentialType) || self.credentialType != 'team_legacy' || !(has(self.description) || has(self.ttl) || has(self.maxTTL))",message="team_legacy roles take no description, ttl or maxTTL"

// spec is the desired role of the Terraform Cloud secrets plugin at `<mountPath>/role/<name>`. An organization token is issued when only organization is set, a team token with teamID, a user token with userID.
//
// The plugin creates a new organization or legacy team token on every write of such a role, which revokes the previous one, so every change of the spec rotates that token.
type TerraformCloudSecretsRoleSpec struct {
	commonv1alpha1.ResourceSpec `json:",inline"`

	// mountPath is the path the Terraform Cloud secrets plugin is mounted at. Defaults to `terraform`. Immutable. Example: `terraform`.
	// +kubebuilder:default=terraform
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=256
	// +kubebuilder:validation:Pattern=`^[A-Za-z0-9_.-]+(/[A-Za-z0-9_.-]+)*$`
	// +optional
	MountPath string `json:"mountPath,omitempty"`

	// name is the role name in OpenBao, lower case because OpenBao lowercases role names. Defaults to metadata.name. Immutable. Example: `ci-team`.
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=128
	// +kubebuilder:validation:Pattern=`^[a-z0-9_]([a-z0-9_.-]*[a-z0-9_])?$`
	// +optional
	Name string `json:"name,omitempty"`

	// organization is the name of the Terraform organization (`organization`). Example: `example-org`.
	// +kubebuilder:validation:MaxLength=256
	// +optional
	Organization string `json:"organization,omitempty"`

	// teamID is the ID of the Terraform team (`team_id`). Example: `team-AbCdEfGh12345678`.
	// +kubebuilder:validation:MaxLength=64
	// +optional
	TeamID string `json:"teamID,omitempty"`

	// userID is the ID of the Terraform user (`user_id`); not combinable with organization or teamID. Example: `user-AbCdEfGh12345678`.
	// +kubebuilder:validation:MaxLength=64
	// +optional
	UserID string `json:"userID,omitempty"`

	// credentialType selects the token type explicitly (`credential_type`). Allowed: organization, team, team_legacy, user. Unset derives it from teamID and userID. Immutable.
	// +optional
	CredentialType TerraformCloudCredentialType `json:"credentialType,omitempty"`

	// description prefixes the name of issued team and user tokens, to identify them in the Terraform UI (`description`). Not for team_legacy. Example: `openbao-ci`.
	// +kubebuilder:validation:MaxLength=256
	// +optional
	Description string `json:"description,omitempty"`

	// ttl is the default lease duration of issued tokens (`ttl`), a duration. Not for team_legacy. Defaults to the mount's default lease TTL. Example: `1h`.
	// +kubebuilder:validation:Type=string
	// +kubebuilder:validation:Pattern=`^(0|([0-9]+(\.[0-9]+)?(ns|us|ms|s|m|h))+)$`
	// +optional
	TTL *metav1.Duration `json:"ttl,omitempty"`

	// maxTTL is the maximum lease duration of issued tokens (`max_ttl`), a duration. Not for team_legacy. Defaults to the mount's max lease TTL. Example: `24h`.
	// +kubebuilder:validation:Type=string
	// +kubebuilder:validation:Pattern=`^(0|([0-9]+(\.[0-9]+)?(ns|us|ms|s|m|h))+)$`
	// +optional
	MaxTTL *metav1.Duration `json:"maxTTL,omitempty"`
}

// status is the observed state of the Terraform Cloud secrets role.
type TerraformCloudSecretsRoleStatus struct {
	commonv1alpha1.ResourceStatus `json:",inline"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:categories=obco;obco-secrets
// +kubebuilder:printcolumn:name="Ready",type=string,JSONPath=`.status.conditions[?(@.type=="Ready")].status`
// +kubebuilder:printcolumn:name="Synced",type=string,JSONPath=`.status.conditions[?(@.type=="Synced")].status`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// TerraformCloudSecretsRole manages a role of a Terraform Cloud secrets plugin mount (external plugin) at `<mount>/role/<name>`: which organization, team or user API tokens are issued from `<mount>/creds/<name>`.
//
// Example:
//
//	spec:
//	  connectionRef:
//	    name: openbao
//	  mountPath: terraform
//	  name: ci-team
//	  organization: example-org
//	  teamID: team-AbCdEfGh12345678
//	  ttl: 1h
type TerraformCloudSecretsRole struct {
	metav1.TypeMeta `json:",inline"`

	// +optional
	metav1.ObjectMeta `json:"metadata,omitzero"`

	// +required
	Spec TerraformCloudSecretsRoleSpec `json:"spec"`

	// +optional
	Status TerraformCloudSecretsRoleStatus `json:"status,omitzero"`
}

// GetResourceSpec implements commonv1alpha1.Managed.
func (o *TerraformCloudSecretsRole) GetResourceSpec() *commonv1alpha1.ResourceSpec {
	return &o.Spec.ResourceSpec
}

// GetResourceStatus implements commonv1alpha1.Managed.
func (o *TerraformCloudSecretsRole) GetResourceStatus() *commonv1alpha1.ResourceStatus {
	return &o.Status.ResourceStatus
}

// +kubebuilder:object:root=true

// TerraformCloudSecretsRoleList contains a list of TerraformCloudSecretsRole.
type TerraformCloudSecretsRoleList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []TerraformCloudSecretsRole `json:"items"`
}

func init() {
	SchemeBuilder.Register(&TerraformCloudSecretsRole{}, &TerraformCloudSecretsRoleList{})
}
