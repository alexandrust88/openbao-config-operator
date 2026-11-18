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

// An Azure role assigned at a scope to the generated service principal. Set roleName or roleID.
// +kubebuilder:validation:XValidation:rule="has(self.roleName) || has(self.roleID)",message="set roleName or roleID"
type AzureRoleAssignment struct {
	// roleName is the name of the Azure role (`role_name`). Example: `Reader`.
	// +kubebuilder:validation:MaxLength=256
	// +optional
	RoleName string `json:"roleName,omitempty"`

	// roleID is the full ID of the Azure role definition (`role_id`), under `/subscriptions/<id>/providers/Microsoft.Authorization/roleDefinitions/`. Example: `/subscriptions/<id>/providers/.../roleDefinitions/<guid>`.
	// +kubebuilder:validation:MaxLength=512
	// +optional
	RoleID string `json:"roleID,omitempty"`

	// scope is the Azure scope of the assignment (`scope`). Example: `/subscriptions/<id>/resourceGroups/billing`.
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=1024
	// +required
	Scope string `json:"scope"`
}

// A Microsoft Entra group the generated service principal joins.
type AzureGroupAssignment struct {
	// groupName is the display name of the group (`group_name`). Example: `billing-readers`.
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=256
	// +required
	GroupName string `json:"groupName"`

	// objectID is the object ID of the group (`object_id`); OpenBao looks it up from groupName when empty. Example: `aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee`.
	// +kubebuilder:validation:MaxLength=64
	// +optional
	ObjectID string `json:"objectID,omitempty"`
}

// The account types that may sign in to the generated application.
// +kubebuilder:validation:Enum=AzureADMyOrg;AzureADMultipleOrgs;AzureADandPersonalMicrosoftAccount;PersonalMicrosoftAccount
type AzureSignInAudience string

// +kubebuilder:validation:XValidation:rule="has(self.mountPath) == has(oldSelf.mountPath) && (!has(self.mountPath) || self.mountPath == oldSelf.mountPath)",message="mountPath is immutable"
// +kubebuilder:validation:XValidation:rule="has(self.name) == has(oldSelf.name) && (!has(self.name) || self.name == oldSelf.name)",message="name is immutable"
// +kubebuilder:validation:XValidation:rule="has(self.azureRoles) || has(self.azureGroups) || has(self.applicationObjectID)",message="set azureRoles, azureGroups or applicationObjectID"

// spec is the desired role of an Azure secrets engine at `<mountPath>/roles/<name>`. Set azureRoles, azureGroups or applicationObjectID.
type AzureSecretsRoleSpec struct {
	commonv1alpha1.ResourceSpec `json:",inline"`

	// mountPath is the path of the Azure secrets engine mount. Defaults to `azure`. Immutable. Example: `azure`.
	// +kubebuilder:default=azure
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=128
	// +kubebuilder:validation:Pattern=`^[A-Za-z0-9_.-]+(/[A-Za-z0-9_.-]+)*$`
	// +optional
	MountPath string `json:"mountPath,omitempty"`

	// name is the role name in OpenBao. Defaults to metadata.name. Immutable. Example: `billing-reader`.
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=128
	// +kubebuilder:validation:Pattern=`^[A-Za-z0-9_.-]+$`
	// +optional
	Name string `json:"name,omitempty"`

	// azureRoles are the Azure role assignments of the dynamic service principals (`azure_roles`, written as JSON). Example: `[{roleName: Reader, scope: /subscriptions/<id>}]`.
	// +kubebuilder:validation:MaxItems=32
	// +optional
	AzureRoles []AzureRoleAssignment `json:"azureRoles,omitempty"`

	// azureGroups are the Microsoft Entra groups the dynamic service principals join (`azure_groups`, written as JSON). Example: `[{groupName: billing-readers}]`.
	// +kubebuilder:validation:MaxItems=32
	// +optional
	AzureGroups []AzureGroupAssignment `json:"azureGroups,omitempty"`

	// applicationObjectID is the object ID of an existing application (`application_object_id`); credentials are then added to it instead of creating dynamic service principals. Example: `aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee`.
	// +kubebuilder:validation:MaxLength=64
	// +optional
	ApplicationObjectID string `json:"applicationObjectID,omitempty"`

	// persistApp keeps the generated application and service principal for the lifetime of the role (`persist_app`). Defaults to false.
	// +optional
	PersistApp *bool `json:"persistApp,omitempty"`

	// ttl is the default lease duration of generated credentials (`ttl`), a duration. Defaults to the mount's default lease TTL. Example: `1h`.
	// +kubebuilder:validation:Type=string
	// +kubebuilder:validation:Pattern=`^(0|([0-9]+(\.[0-9]+)?(ns|us|ms|s|m|h))+)$`
	// +optional
	TTL *metav1.Duration `json:"ttl,omitempty"`

	// maxTTL is the maximum lease duration of generated credentials (`max_ttl`), a duration. Defaults to the mount's max lease TTL. Example: `24h`.
	// +kubebuilder:validation:Type=string
	// +kubebuilder:validation:Pattern=`^(0|([0-9]+(\.[0-9]+)?(ns|us|ms|s|m|h))+)$`
	// +optional
	MaxTTL *metav1.Duration `json:"maxTTL,omitempty"`

	// explicitMaxTTL is the hard limit on the lifetime of generated credentials and dynamic service principals (`explicit_max_ttl`), a duration; a lease is not renewed past it. Example: `720h`.
	// +kubebuilder:validation:Type=string
	// +kubebuilder:validation:Pattern=`^(0|([0-9]+(\.[0-9]+)?(ns|us|ms|s|m|h))+)$`
	// +optional
	ExplicitMaxTTL *metav1.Duration `json:"explicitMaxTTL,omitempty"`

	// permanentlyDelete deletes generated applications and service principals permanently when their lease ends (`permanently_delete`), instead of moving them to the Entra recycle bin. Defaults to false.
	// +optional
	PermanentlyDelete *bool `json:"permanentlyDelete,omitempty"`

	// signInAudience restricts which account types may sign in to the generated application (`sign_in_audience`). Allowed: AzureADMyOrg, AzureADMultipleOrgs, AzureADandPersonalMicrosoftAccount, PersonalMicrosoftAccount.
	// +optional
	SignInAudience AzureSignInAudience `json:"signInAudience,omitempty"`

	// tags are Azure tags attached to the generated application (`tags`). Example: `[team:billing]`.
	// +kubebuilder:validation:MaxItems=50
	// +kubebuilder:validation:items:MaxLength=512
	// +listType=set
	// +optional
	Tags []string `json:"tags,omitempty"`
}

// status is the observed state of the Azure secrets role.
type AzureSecretsRoleStatus struct {
	commonv1alpha1.ResourceStatus `json:",inline"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:categories=obco;obco-secrets
// +kubebuilder:printcolumn:name="Ready",type=string,JSONPath=`.status.conditions[?(@.type=="Ready")].status`
// +kubebuilder:printcolumn:name="Synced",type=string,JSONPath=`.status.conditions[?(@.type=="Synced")].status`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// AzureSecretsRole manages a role of an Azure secrets engine (external plugin) at `<mount>/roles/<name>`: the role assignments and groups of the service principals issued from `<mount>/creds/<name>`, or an existing application to add credentials to.
//
// Example:
//
//	spec:
//	  connectionRef:
//	    name: openbao
//	  mountPath: azure
//	  name: billing-reader
//	  azureRoles:
//	    - roleName: Reader
//	      scope: /subscriptions/11111111-2222-3333-4444-555555555555
type AzureSecretsRole struct {
	metav1.TypeMeta `json:",inline"`

	// +optional
	metav1.ObjectMeta `json:"metadata,omitzero"`

	// +required
	Spec AzureSecretsRoleSpec `json:"spec"`

	// +optional
	Status AzureSecretsRoleStatus `json:"status,omitzero"`
}

// GetResourceSpec implements commonv1alpha1.Managed.
func (o *AzureSecretsRole) GetResourceSpec() *commonv1alpha1.ResourceSpec {
	return &o.Spec.ResourceSpec
}

// GetResourceStatus implements commonv1alpha1.Managed.
func (o *AzureSecretsRole) GetResourceStatus() *commonv1alpha1.ResourceStatus {
	return &o.Status.ResourceStatus
}

// +kubebuilder:object:root=true

// AzureSecretsRoleList contains a list of AzureSecretsRole.
type AzureSecretsRoleList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []AzureSecretsRole `json:"items"`
}

func init() {
	SchemeBuilder.Register(&AzureSecretsRole{}, &AzureSecretsRoleList{})
}
