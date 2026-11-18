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

// Desired state of an LDAP user entry, written to auth/<mountPath>/users/<name>.
type LDAPAuthUserSpec struct {
	commonv1alpha1.ResourceSpec `json:",inline"`

	// Path of the LDAP auth method, without the auth/ prefix. Defaults to ldap. Immutable. Example: ldap
	// +kubebuilder:default=ldap
	// +kubebuilder:validation:MaxLength=128
	// +kubebuilder:validation:Pattern=`^[A-Za-z0-9_.-]+(/[A-Za-z0-9_.-]+)*$`
	// +optional
	MountPath string `json:"mountPath,omitempty"`

	// LDAP login name of the user. Defaults to metadata.name; set it for names that are not valid Kubernetes names, such as names with upper case letters. Must not contain /. Immutable. Example: jdoe
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=256
	// +kubebuilder:validation:Pattern=`^[^/]+$`
	// +optional
	Name string `json:"name,omitempty"`

	// OpenBao-side LDAP group names (see LDAPAuthGroup) whose policies the user gets in addition to the groups found in LDAP. No commas. OpenBao stores them in lower case unless the auth method sets caseSensitiveNames. Maps to `groups`. Example: [platform-admins]
	// +kubebuilder:validation:MaxItems=256
	// +kubebuilder:validation:items:MinLength=1
	// +kubebuilder:validation:items:MaxLength=256
	// +kubebuilder:validation:items:Pattern=`^[^,]+$`
	// +listType=set
	// +optional
	Groups []string `json:"groups,omitempty"`

	// OpenBao policy names attached to the tokens of the user, in addition to those of the user's groups. Lower case, no commas or spaces. Maps to `policies`. Example: [jdoe-personal]
	// +kubebuilder:validation:MaxItems=256
	// +kubebuilder:validation:items:MinLength=1
	// +kubebuilder:validation:items:MaxLength=128
	// +kubebuilder:validation:items:Pattern=`^[^A-Z,\s]+$`
	// +listType=set
	// +optional
	Policies []string `json:"policies,omitempty"`
}

// Observed state of the user entry, written by the operator.
type LDAPAuthUserStatus struct {
	commonv1alpha1.ResourceStatus `json:",inline"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:categories=obco;obco-auth
// +kubebuilder:printcolumn:name="Ready",type=string,JSONPath=`.status.conditions[?(@.type=="Ready")].status`
// +kubebuilder:printcolumn:name="Synced",type=string,JSONPath=`.status.conditions[?(@.type=="Synced")].status`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// LDAPAuthUser manages an LDAP user entry of an OpenBao LDAP auth method at auth/<mountPath>/users/<name>. Use it to give one LDAP user extra OpenBao policies or groups beyond what LDAP group membership grants.
//
// Example:
//
//	spec:
//	  connectionRef:
//	    name: openbao
//	  mountPath: ldap
//	  name: jdoe
//	  groups: [platform-admins]
//	  policies: [jdoe-personal]
type LDAPAuthUser struct {
	metav1.TypeMeta `json:",inline"`

	// +optional
	metav1.ObjectMeta `json:"metadata,omitzero"`

	// +required
	Spec LDAPAuthUserSpec `json:"spec"`

	// +optional
	Status LDAPAuthUserStatus `json:"status,omitzero"`
}

// GetResourceSpec implements commonv1alpha1.Managed.
func (o *LDAPAuthUser) GetResourceSpec() *commonv1alpha1.ResourceSpec { return &o.Spec.ResourceSpec }

// GetResourceStatus implements commonv1alpha1.Managed.
func (o *LDAPAuthUser) GetResourceStatus() *commonv1alpha1.ResourceStatus {
	return &o.Status.ResourceStatus
}

// +kubebuilder:object:root=true

// LDAPAuthUserList contains a list of LDAPAuthUser.
type LDAPAuthUserList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []LDAPAuthUser `json:"items"`
}

func init() {
	SchemeBuilder.Register(&LDAPAuthUser{}, &LDAPAuthUserList{})
}
