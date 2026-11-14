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

// +kubebuilder:validation:XValidation:rule="self.mountPath == oldSelf.mountPath",message="mountPath is immutable"
// +kubebuilder:validation:XValidation:rule="has(self.name) == has(oldSelf.name) && (!has(self.name) || self.name == oldSelf.name)",message="name is immutable"
// +kubebuilder:validation:XValidation:rule="self.username == oldSelf.username",message="username is immutable"

// spec is the desired static role of a database secrets engine at `<mountPath>/static-roles/<name>`. OpenBao rotates the password of the existing database user when the role is created and then every rotationPeriod; a read of the role never returns the password.
type DatabaseStaticRoleSpec struct {
	commonv1alpha1.ResourceSpec `json:",inline"`
	DatabaseCredentialFields    `json:",inline"`

	// mountPath is the path of the database secrets engine mount, without leading or trailing slashes. Immutable. Example: `database`.
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=128
	// +kubebuilder:validation:Pattern=`^[A-Za-z0-9_.-]+(/[A-Za-z0-9_.-]+)*$`
	// +required
	MountPath string `json:"mountPath"`

	// name is the static role name in OpenBao. Defaults to metadata.name. Immutable. Example: `billing-reporting`.
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=128
	// +kubebuilder:validation:Pattern=`^\w([\w.-]*\w)?$`
	// +optional
	Name string `json:"name,omitempty"`

	// dbName is the name of the database connection the role uses (`db_name`); the connection must list the role in allowedRoles. Example: `billing-postgres`.
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=128
	// +required
	DBName string `json:"dbName"`

	// username is the existing database user whose password OpenBao manages (`username`). Immutable. Example: `reporting`.
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=256
	// +required
	Username string `json:"username"`

	// rotationPeriod is how often OpenBao rotates the password (`rotation_period`), a duration of at least `5s`. Example: `24h`.
	// +kubebuilder:validation:XValidation:rule="duration(self) >= duration('5s')",message="rotationPeriod must be at least 5s"
	// +kubebuilder:validation:Type=string
	// +kubebuilder:validation:Pattern=`^(0|([0-9]+(\.[0-9]+)?(ns|us|ms|s|m|h))+)$`
	// +required
	RotationPeriod metav1.Duration `json:"rotationPeriod"`

	// rotationStatements are the database statements that set the new password (`rotation_statements`), with `{{name}}` and `{{password}}` templates. Empty uses the plugin default. Up to 64.
	// +kubebuilder:validation:MaxItems=64
	// +kubebuilder:validation:items:MinLength=1
	// +kubebuilder:validation:items:MaxLength=8192
	// +optional
	RotationStatements []string `json:"rotationStatements,omitempty"`
}

// status is the observed state of the database static role.
type DatabaseStaticRoleStatus struct {
	commonv1alpha1.ResourceStatus `json:",inline"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:categories=openbao
// +kubebuilder:printcolumn:name="Ready",type=string,JSONPath=`.status.conditions[?(@.type=="Ready")].status`
// +kubebuilder:printcolumn:name="Synced",type=string,JSONPath=`.status.conditions[?(@.type=="Synced")].status`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// DatabaseStaticRole manages a static role of a database secrets engine at `<mount>/static-roles/<name>`: OpenBao owns and rotates the password of an existing database user, served at `<mount>/static-creds/<name>`.
//
// Example:
//
//	spec:
//	  connectionRef:
//	    name: openbao
//	  mountPath: database
//	  name: billing-reporting
//	  dbName: billing-postgres
//	  username: reporting
//	  rotationPeriod: 24h
type DatabaseStaticRole struct {
	metav1.TypeMeta `json:",inline"`

	// +optional
	metav1.ObjectMeta `json:"metadata,omitzero"`

	// +required
	Spec DatabaseStaticRoleSpec `json:"spec"`

	// +optional
	Status DatabaseStaticRoleStatus `json:"status,omitzero"`
}

// GetResourceSpec implements commonv1alpha1.Managed.
func (r *DatabaseStaticRole) GetResourceSpec() *commonv1alpha1.ResourceSpec {
	return &r.Spec.ResourceSpec
}

// GetResourceStatus implements commonv1alpha1.Managed.
func (r *DatabaseStaticRole) GetResourceStatus() *commonv1alpha1.ResourceStatus {
	return &r.Status.ResourceStatus
}

// +kubebuilder:object:root=true

// DatabaseStaticRoleList contains a list of DatabaseStaticRole.
type DatabaseStaticRoleList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []DatabaseStaticRole `json:"items"`
}

func init() {
	SchemeBuilder.Register(&DatabaseStaticRole{}, &DatabaseStaticRoleList{})
}
