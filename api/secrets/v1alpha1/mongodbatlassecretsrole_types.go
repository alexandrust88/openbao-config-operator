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
// +kubebuilder:validation:XValidation:rule="has(self.organizationID) || has(self.projectID)",message="set organizationID or projectID"
// +kubebuilder:validation:XValidation:rule="has(self.projectRoles) == (has(self.organizationID) && has(self.projectID))",message="set projectRoles exactly when both organizationID and projectID are set"

// spec is the desired role of the MongoDB Atlas secrets plugin at `<mountPath>/roles/<name>`. Credentials are programmatic API keys of the organization, of the project, or of the organization assigned to the project when both IDs are set.
type MongoDBAtlasSecretsRoleSpec struct {
	commonv1alpha1.ResourceSpec `json:",inline"`

	// mountPath is the path the MongoDB Atlas secrets plugin is mounted at. Defaults to `mongodbatlas`. Immutable. Example: `mongodbatlas`.
	// +kubebuilder:default=mongodbatlas
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=256
	// +kubebuilder:validation:Pattern=`^[A-Za-z0-9_.-]+(/[A-Za-z0-9_.-]+)*$`
	// +optional
	MountPath string `json:"mountPath,omitempty"`

	// name is the role name in OpenBao, lower case because OpenBao lowercases role names. Defaults to metadata.name. Immutable. Example: `project-reader`.
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=128
	// +kubebuilder:validation:Pattern=`^[a-z0-9_]([a-z0-9_.-]*[a-z0-9_])?$`
	// +optional
	Name string `json:"name,omitempty"`

	// organizationID is the Atlas organization the API key belongs to (`organization_id`). Set this or projectID. Example: `5b23ff2f96e82130d0aaec13`.
	// +kubebuilder:validation:MaxLength=64
	// +optional
	OrganizationID string `json:"organizationID,omitempty"`

	// projectID is the Atlas project the API key belongs to (`project_id`). Example: `5cf5a45a9ccf6400e60981b6`.
	// +kubebuilder:validation:MaxLength=64
	// +optional
	ProjectID string `json:"projectID,omitempty"`

	// roles lists the Atlas roles granted to the API key (`roles`). At least one. Example: `[GROUP_READ_ONLY]`.
	// +kubebuilder:validation:MinItems=1
	// +kubebuilder:validation:MaxItems=32
	// +kubebuilder:validation:items:MaxLength=64
	// +listType=set
	// +required
	Roles []string `json:"roles"`

	// ipAddresses lists the IP addresses added to the access list of the API key (`ip_addresses`). Example: `[192.0.2.10]`.
	// +kubebuilder:validation:MaxItems=64
	// +kubebuilder:validation:items:MaxLength=64
	// +listType=set
	// +optional
	IPAddresses []string `json:"ipAddresses,omitempty"`

	// cidrBlocks lists the CIDR blocks added to the access list of the API key (`cidr_blocks`). Example: `[192.0.2.0/24]`.
	// +kubebuilder:validation:MaxItems=64
	// +kubebuilder:validation:items:MaxLength=64
	// +listType=set
	// +optional
	CIDRBlocks []string `json:"cidrBlocks,omitempty"`

	// projectRoles lists the project roles granted when an organization key is assigned to the project (`project_roles`). Required exactly when both organizationID and projectID are set. Example: `[GROUP_READ_ONLY]`.
	// +kubebuilder:validation:MaxItems=32
	// +kubebuilder:validation:items:MaxLength=64
	// +listType=set
	// +optional
	ProjectRoles []string `json:"projectRoles,omitempty"`

	// ttl is the default lease duration of issued API keys (`ttl`), a duration. Defaults to the mount's default lease TTL. Example: `1h`.
	// +kubebuilder:validation:Type=string
	// +kubebuilder:validation:Pattern=`^(0|([0-9]+(\.[0-9]+)?(ns|us|ms|s|m|h))+)$`
	// +optional
	TTL *metav1.Duration `json:"ttl,omitempty"`

	// maxTTL is the maximum lease duration of issued API keys (`max_ttl`), a duration. Defaults to the mount's max lease TTL. Example: `24h`.
	// +kubebuilder:validation:Type=string
	// +kubebuilder:validation:Pattern=`^(0|([0-9]+(\.[0-9]+)?(ns|us|ms|s|m|h))+)$`
	// +optional
	MaxTTL *metav1.Duration `json:"maxTTL,omitempty"`
}

// status is the observed state of the MongoDB Atlas secrets role.
type MongoDBAtlasSecretsRoleStatus struct {
	commonv1alpha1.ResourceStatus `json:",inline"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:categories=obco;obco-secrets
// +kubebuilder:printcolumn:name="Ready",type=string,JSONPath=`.status.conditions[?(@.type=="Ready")].status`
// +kubebuilder:printcolumn:name="Synced",type=string,JSONPath=`.status.conditions[?(@.type=="Synced")].status`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// MongoDBAtlasSecretsRole manages a role of a MongoDB Atlas secrets plugin mount (external plugin) at `<mount>/roles/<name>`: the Atlas roles and access list of the programmatic API keys issued from `<mount>/creds/<name>`.
//
// Example:
//
//	spec:
//	  connectionRef:
//	    name: openbao
//	  mountPath: mongodbatlas
//	  name: project-reader
//	  projectID: 5cf5a45a9ccf6400e60981b6
//	  roles: [GROUP_READ_ONLY]
type MongoDBAtlasSecretsRole struct {
	metav1.TypeMeta `json:",inline"`

	// +optional
	metav1.ObjectMeta `json:"metadata,omitzero"`

	// +required
	Spec MongoDBAtlasSecretsRoleSpec `json:"spec"`

	// +optional
	Status MongoDBAtlasSecretsRoleStatus `json:"status,omitzero"`
}

// GetResourceSpec implements commonv1alpha1.Managed.
func (o *MongoDBAtlasSecretsRole) GetResourceSpec() *commonv1alpha1.ResourceSpec {
	return &o.Spec.ResourceSpec
}

// GetResourceStatus implements commonv1alpha1.Managed.
func (o *MongoDBAtlasSecretsRole) GetResourceStatus() *commonv1alpha1.ResourceStatus {
	return &o.Status.ResourceStatus
}

// +kubebuilder:object:root=true

// MongoDBAtlasSecretsRoleList contains a list of MongoDBAtlasSecretsRole.
type MongoDBAtlasSecretsRoleList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []MongoDBAtlasSecretsRole `json:"items"`
}

func init() {
	SchemeBuilder.Register(&MongoDBAtlasSecretsRole{}, &MongoDBAtlasSecretsRoleList{})
}
