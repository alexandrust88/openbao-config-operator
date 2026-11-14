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

// Desired state of an LDAP group mapping, written to auth/<mountPath>/groups/<name>.
type LDAPAuthGroupSpec struct {
	commonv1alpha1.ResourceSpec `json:",inline"`

	// Path of the LDAP auth method, without the auth/ prefix. Defaults to ldap. Immutable. Example: ldap
	// +kubebuilder:default=ldap
	// +kubebuilder:validation:MaxLength=128
	// +kubebuilder:validation:Pattern=`^[A-Za-z0-9_.-]+(/[A-Za-z0-9_.-]+)*$`
	// +optional
	MountPath string `json:"mountPath,omitempty"`

	// Name of the LDAP group as returned by the groupAttr attribute. Defaults to metadata.name; set it for names that are not valid Kubernetes names, such as names with upper case letters or spaces. Must not contain /. Immutable. Example: Platform Admins
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=256
	// +kubebuilder:validation:Pattern=`^[^/]+$`
	// +optional
	Name string `json:"name,omitempty"`

	// OpenBao policy names attached to the tokens of members of the group. Lower case, no commas or spaces. Maps to `policies`. Example: [platform-admin]
	// +kubebuilder:validation:MaxItems=256
	// +kubebuilder:validation:items:MinLength=1
	// +kubebuilder:validation:items:MaxLength=128
	// +kubebuilder:validation:items:Pattern=`^[^A-Z,\s]+$`
	// +listType=set
	// +optional
	Policies []string `json:"policies,omitempty"`
}

// Observed state of the group mapping, written by the operator.
type LDAPAuthGroupStatus struct {
	commonv1alpha1.ResourceStatus `json:",inline"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:categories=openbao
// +kubebuilder:printcolumn:name="Ready",type=string,JSONPath=`.status.conditions[?(@.type=="Ready")].status`
// +kubebuilder:printcolumn:name="Synced",type=string,JSONPath=`.status.conditions[?(@.type=="Synced")].status`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// LDAPAuthGroup manages an LDAP group mapping of an OpenBao LDAP auth method at auth/<mountPath>/groups/<name>. Use it to give every member of an LDAP or Active Directory group a set of OpenBao policies at login.
//
// Example:
//
//	spec:
//	  connectionRef:
//	    name: openbao
//	  mountPath: ldap
//	  name: platform-admins
//	  policies: [platform-admin]
type LDAPAuthGroup struct {
	metav1.TypeMeta `json:",inline"`

	// +optional
	metav1.ObjectMeta `json:"metadata,omitzero"`

	// +required
	Spec LDAPAuthGroupSpec `json:"spec"`

	// +optional
	Status LDAPAuthGroupStatus `json:"status,omitzero"`
}

// GetResourceSpec implements commonv1alpha1.Managed.
func (o *LDAPAuthGroup) GetResourceSpec() *commonv1alpha1.ResourceSpec { return &o.Spec.ResourceSpec }

// GetResourceStatus implements commonv1alpha1.Managed.
func (o *LDAPAuthGroup) GetResourceStatus() *commonv1alpha1.ResourceStatus {
	return &o.Status.ResourceStatus
}

// +kubebuilder:object:root=true

// LDAPAuthGroupList contains a list of LDAPAuthGroup.
type LDAPAuthGroupList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []LDAPAuthGroup `json:"items"`
}

func init() {
	SchemeBuilder.Register(&LDAPAuthGroup{}, &LDAPAuthGroupList{})
}
