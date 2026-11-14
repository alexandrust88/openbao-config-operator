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
// +kubebuilder:validation:XValidation:rule="has(self.passwordSecretRef) != has(self.passwordHashSecretRef)",message="set exactly one of passwordSecretRef and passwordHashSecretRef"

// Desired state of a userpass user, written to auth/<mountPath>/users/<name>. Set exactly one of passwordSecretRef and passwordHashSecretRef.
type UserpassUserSpec struct {
	commonv1alpha1.ResourceSpec `json:",inline"`

	// Path of the userpass auth method, without the auth/ prefix. Defaults to userpass. Immutable. Example: userpass
	// +kubebuilder:default=userpass
	// +kubebuilder:validation:MaxLength=128
	// +kubebuilder:validation:Pattern=`^[A-Za-z0-9_.-]+(/[A-Za-z0-9_.-]+)*$`
	// +optional
	MountPath string `json:"mountPath,omitempty"`

	// Username in OpenBao. Defaults to metadata.name. OpenBao stores usernames in lower case. Immutable. Example: breakglass
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=128
	// +kubebuilder:validation:Pattern=`^[a-z0-9_]([a-z0-9_.-]*[a-z0-9_])?$`
	// +optional
	Name string `json:"name,omitempty"`

	// Secret key holding the user's password. Read from this Secret key; the value is never stored in status. OpenBao never returns it, so only a hash of it is kept in status.writeOnlyHash and the password is rewritten only when the Secret changes. Maps to `password`. Example: {name: breakglass-password, key: password}
	// +optional
	PasswordSecretRef *commonv1alpha1.SecretKeySelector `json:"passwordSecretRef,omitempty"`

	// Secret key holding a bcrypt hash of the password, used instead of passwordSecretRef. Read from this Secret key; the value is never stored in status. Write-only: only a hash of it is kept in status.writeOnlyHash. Maps to `password_hash`. Example: {name: breakglass-password, key: bcrypt}
	// +optional
	PasswordHashSecretRef *commonv1alpha1.SecretKeySelector `json:"passwordHashSecretRef,omitempty"`

	TokenFields `json:",inline"`
}

// Observed state of the user, written by the operator. Passwords never appear here.
type UserpassUserStatus struct {
	commonv1alpha1.ResourceStatus `json:",inline"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:categories=openbao
// +kubebuilder:printcolumn:name="Ready",type=string,JSONPath=`.status.conditions[?(@.type=="Ready")].status`
// +kubebuilder:printcolumn:name="Synced",type=string,JSONPath=`.status.conditions[?(@.type=="Synced")].status`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// UserpassUser manages a user of an OpenBao userpass auth method at auth/<mountPath>/users/<name>. Use it for break-glass or service users that log in with a username and password kept in a Kubernetes Secret.
//
// Example:
//
//	spec:
//	  connectionRef:
//	    name: openbao
//	  name: breakglass
//	  passwordSecretRef:
//	    name: breakglass-password
//	    key: password
//	  tokenPolicies: [admin]
type UserpassUser struct {
	metav1.TypeMeta `json:",inline"`

	// +optional
	metav1.ObjectMeta `json:"metadata,omitzero"`

	// +required
	Spec UserpassUserSpec `json:"spec"`

	// +optional
	Status UserpassUserStatus `json:"status,omitzero"`
}

// GetResourceSpec implements commonv1alpha1.Managed.
func (u *UserpassUser) GetResourceSpec() *commonv1alpha1.ResourceSpec { return &u.Spec.ResourceSpec }

// GetResourceStatus implements commonv1alpha1.Managed.
func (u *UserpassUser) GetResourceStatus() *commonv1alpha1.ResourceStatus {
	return &u.Status.ResourceStatus
}

// +kubebuilder:object:root=true

// UserpassUserList contains a list of UserpassUser.
type UserpassUserList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []UserpassUser `json:"items"`
}

func init() {
	SchemeBuilder.Register(&UserpassUser{}, &UserpassUserList{})
}
