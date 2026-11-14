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

// spec is the desired dynamic role of the LDAP secrets plugin at `<mountPath>/role/<name>`. Each credential request runs creationLDIF to create a directory entry, and deletionLDIF runs when the lease ends. The LDIF fields are templates rendered with `{{.Username}}`, `{{.Password}}` and `{{.DN}}`. Give them as plain text: OpenBao also accepts base64 but stores the decoded text, which would never match the spec.
type LDAPSecretsDynamicRoleSpec struct {
	commonv1alpha1.ResourceSpec `json:",inline"`

	// mountPath is the path the LDAP secrets plugin is mounted at. Defaults to `ldap`. Immutable. Example: `ldap`.
	// +kubebuilder:default=ldap
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=256
	// +kubebuilder:validation:Pattern=`^[A-Za-z0-9_.-]+(/[A-Za-z0-9_.-]+)*$`
	// +optional
	MountPath string `json:"mountPath,omitempty"`

	// name is the role name in OpenBao, lower case because OpenBao lowercases role names. Defaults to metadata.name. Immutable. Example: `app-users`.
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=128
	// +kubebuilder:validation:Pattern=`^[a-z0-9_]([a-z0-9_.-]*[a-z0-9_])?$`
	// +optional
	Name string `json:"name,omitempty"`

	// creationLDIF is the templated LDIF that creates the account (`creation_ldif`); it may contain several entries. Example: `dn: cn={{.Username}},ou=users,dc=example,dc=com ...`.
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=65536
	// +required
	CreationLDIF string `json:"creationLDIF"`

	// deletionLDIF is the templated LDIF that deletes the account when its lease ends (`deletion_ldif`). Example: `dn: cn={{.Username}},ou=users,dc=example,dc=com\nchangetype: delete`.
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=65536
	// +required
	DeletionLDIF string `json:"deletionLDIF"`

	// rollbackLDIF is the templated LDIF run when creationLDIF fails part way (`rollback_ldif`). Empty uses deletionLDIF.
	// +kubebuilder:validation:MaxLength=65536
	// +optional
	RollbackLDIF string `json:"rollbackLDIF,omitempty"`

	// usernameTemplate is the template that generates the user name (`username_template`), available as `{{.Username}}` in the LDIF. Example: `v_{{.RoleName}}_{{random 8}}`.
	// +kubebuilder:validation:MaxLength=1024
	// +optional
	UsernameTemplate string `json:"usernameTemplate,omitempty"`

	// defaultTTL is the default lease duration of issued credentials (`default_ttl`), a duration. Defaults to the mount's default lease TTL. Example: `1h`.
	// +kubebuilder:validation:Type=string
	// +kubebuilder:validation:Pattern=`^(0|([0-9]+(\.[0-9]+)?(ns|us|ms|s|m|h))+)$`
	// +optional
	DefaultTTL *metav1.Duration `json:"defaultTTL,omitempty"`

	// maxTTL is the maximum lease duration of issued credentials (`max_ttl`), a duration. Defaults to the mount's max lease TTL. Example: `24h`.
	// +kubebuilder:validation:Type=string
	// +kubebuilder:validation:Pattern=`^(0|([0-9]+(\.[0-9]+)?(ns|us|ms|s|m|h))+)$`
	// +optional
	MaxTTL *metav1.Duration `json:"maxTTL,omitempty"`
}

// status is the observed state of the LDAP dynamic role.
type LDAPSecretsDynamicRoleStatus struct {
	commonv1alpha1.ResourceStatus `json:",inline"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:categories=openbao
// +kubebuilder:printcolumn:name="Ready",type=string,JSONPath=`.status.conditions[?(@.type=="Ready")].status`
// +kubebuilder:printcolumn:name="Synced",type=string,JSONPath=`.status.conditions[?(@.type=="Synced")].status`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// LDAPSecretsDynamicRole manages a dynamic role of an LDAP secrets plugin mount (external plugin) at `<mount>/role/<name>`: each request to `<mount>/creds/<name>` creates a short-lived directory account from LDIF templates.
//
// Example:
//
//	spec:
//	  connectionRef:
//	    name: openbao
//	  mountPath: ldap
//	  name: app-users
//	  creationLDIF: |
//	    dn: cn={{.Username}},ou=users,dc=example,dc=com
//	    objectClass: person
//	  deletionLDIF: |
//	    dn: cn={{.Username}},ou=users,dc=example,dc=com
//	    changetype: delete
type LDAPSecretsDynamicRole struct {
	metav1.TypeMeta `json:",inline"`

	// +optional
	metav1.ObjectMeta `json:"metadata,omitzero"`

	// +required
	Spec LDAPSecretsDynamicRoleSpec `json:"spec"`

	// +optional
	Status LDAPSecretsDynamicRoleStatus `json:"status,omitzero"`
}

// GetResourceSpec implements commonv1alpha1.Managed.
func (o *LDAPSecretsDynamicRole) GetResourceSpec() *commonv1alpha1.ResourceSpec {
	return &o.Spec.ResourceSpec
}

// GetResourceStatus implements commonv1alpha1.Managed.
func (o *LDAPSecretsDynamicRole) GetResourceStatus() *commonv1alpha1.ResourceStatus {
	return &o.Status.ResourceStatus
}

// +kubebuilder:object:root=true

// LDAPSecretsDynamicRoleList contains a list of LDAPSecretsDynamicRole.
type LDAPSecretsDynamicRoleList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []LDAPSecretsDynamicRole `json:"items"`
}

func init() {
	SchemeBuilder.Register(&LDAPSecretsDynamicRole{}, &LDAPSecretsDynamicRoleList{})
}
