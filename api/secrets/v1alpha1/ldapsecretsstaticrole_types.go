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
// +kubebuilder:validation:XValidation:rule="self.username == oldSelf.username",message="username is immutable"
// +kubebuilder:validation:XValidation:rule="has(self.dn) == has(oldSelf.dn) && (!has(self.dn) || self.dn == oldSelf.dn)",message="dn is immutable"

// spec is the desired static role of the LDAP secrets plugin at `<mountPath>/static-role/<name>`. OpenBao rotates the password of an existing directory account every rotationPeriod.
type LDAPSecretsStaticRoleSpec struct {
	commonv1alpha1.ResourceSpec `json:",inline"`

	// mountPath is the path the LDAP secrets plugin is mounted at. Defaults to `ldap`. Immutable. Example: `ldap`.
	// +kubebuilder:default=ldap
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=256
	// +kubebuilder:validation:Pattern=`^[A-Za-z0-9_.-]+(/[A-Za-z0-9_.-]+)*$`
	// +optional
	MountPath string `json:"mountPath,omitempty"`

	// name is the role name in OpenBao, lower case because OpenBao lowercases role names. Defaults to metadata.name. Immutable. Example: `svc-reporting`.
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=128
	// +kubebuilder:validation:Pattern=`^[a-z0-9_]([a-z0-9_.-]*[a-z0-9_])?$`
	// +optional
	Name string `json:"name,omitempty"`

	// username is the existing directory account whose password OpenBao rotates (`username`). Immutable. Example: `svc-reporting`.
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=256
	// +required
	Username string `json:"username"`

	// dn is the distinguished name of the existing account (`dn`); when set, the account is looked up by DN instead of by username. Immutable. Example: `cn=svc-reporting,ou=services,dc=example,dc=com`.
	// +kubebuilder:validation:MaxLength=1024
	// +optional
	DN string `json:"dn,omitempty"`

	// rotationPeriod is how often OpenBao rotates the password (`rotation_period`), a duration of at least `5s`. Example: `24h`.
	// +kubebuilder:validation:Type=string
	// +kubebuilder:validation:Pattern=`^(0|([0-9]+(\.[0-9]+)?(ns|us|ms|s|m|h))+)$`
	// +required
	RotationPeriod metav1.Duration `json:"rotationPeriod"`

	// skipImportRotation keeps the current password when the role is created instead of rotating it at once (`skip_import_rotation`). Applies only on creation. Defaults to the mount's skipStaticRoleImportRotation.
	// +optional
	SkipImportRotation *bool `json:"skipImportRotation,omitempty"`
}

// status is the observed state of the LDAP static role.
type LDAPSecretsStaticRoleStatus struct {
	commonv1alpha1.ResourceStatus `json:",inline"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:categories=openbao
// +kubebuilder:printcolumn:name="Ready",type=string,JSONPath=`.status.conditions[?(@.type=="Ready")].status`
// +kubebuilder:printcolumn:name="Synced",type=string,JSONPath=`.status.conditions[?(@.type=="Synced")].status`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// LDAPSecretsStaticRole manages a static role of an LDAP secrets plugin mount (external plugin) at `<mount>/static-role/<name>`: OpenBao owns and rotates the password of an existing directory account, served at `<mount>/static-cred/<name>`.
//
// Example:
//
//	spec:
//	  connectionRef:
//	    name: openbao
//	  mountPath: ldap
//	  name: svc-reporting
//	  username: svc-reporting
//	  rotationPeriod: 24h
type LDAPSecretsStaticRole struct {
	metav1.TypeMeta `json:",inline"`

	// +optional
	metav1.ObjectMeta `json:"metadata,omitzero"`

	// +required
	Spec LDAPSecretsStaticRoleSpec `json:"spec"`

	// +optional
	Status LDAPSecretsStaticRoleStatus `json:"status,omitzero"`
}

// GetResourceSpec implements commonv1alpha1.Managed.
func (o *LDAPSecretsStaticRole) GetResourceSpec() *commonv1alpha1.ResourceSpec {
	return &o.Spec.ResourceSpec
}

// GetResourceStatus implements commonv1alpha1.Managed.
func (o *LDAPSecretsStaticRole) GetResourceStatus() *commonv1alpha1.ResourceStatus {
	return &o.Status.ResourceStatus
}

// +kubebuilder:object:root=true

// LDAPSecretsStaticRoleList contains a list of LDAPSecretsStaticRole.
type LDAPSecretsStaticRoleList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []LDAPSecretsStaticRole `json:"items"`
}

func init() {
	SchemeBuilder.Register(&LDAPSecretsStaticRole{}, &LDAPSecretsStaticRoleList{})
}
