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

// Desired state of the user mapping, written to auth/<mountPath>/map/users/<name>.
type GitHubAuthUserSpec struct {
	commonv1alpha1.ResourceSpec `json:",inline"`

	// Path of the GitHub auth method, without the auth/ prefix. Defaults to github. Immutable. Example: github/example-org
	// +kubebuilder:default=github
	// +kubebuilder:validation:MaxLength=128
	// +kubebuilder:validation:Pattern=`^[A-Za-z0-9_.-]+(/[A-Za-z0-9_.-]+)*$`
	// +optional
	MountPath string `json:"mountPath,omitempty"`

	// GitHub login of the user. Defaults to metadata.name; set it for logins that are not valid Kubernetes names, such as names with upper case letters. Must not contain "/". Immutable. Example: octocat
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=256
	// +kubebuilder:validation:Pattern=`^[^/]+$`
	// +optional
	Name string `json:"name,omitempty"`

	// OpenBao policy names attached to the user's tokens, in addition to the policies of the user's teams; lower case, no commas or spaces. Maps to `value` (sent comma separated). Example: [break-glass]
	// +kubebuilder:validation:MaxItems=256
	// +kubebuilder:validation:items:MinLength=1
	// +kubebuilder:validation:items:MaxLength=128
	// +kubebuilder:validation:items:Pattern=`^[^A-Z,\s]+$`
	// +listType=set
	// +optional
	Policies []string `json:"policies,omitempty"`
}

// Observed state of the user mapping, written by the operator.
type GitHubAuthUserStatus struct {
	commonv1alpha1.ResourceStatus `json:",inline"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:categories=openbao
// +kubebuilder:printcolumn:name="Ready",type=string,JSONPath=`.status.conditions[?(@.type=="Ready")].status`
// +kubebuilder:printcolumn:name="Synced",type=string,JSONPath=`.status.conditions[?(@.type=="Synced")].status`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// GitHubAuthUser manages the policy mapping of a GitHub user in an OpenBao GitHub auth method at auth/<mountPath>/map/users/<name>. Use it to give one member of the configured organization policies in addition to those of their teams. Needs the github auth plugin enabled at auth/<mountPath>.
//
// Example:
//
//	spec:
//	  connectionRef:
//	    name: openbao
//	  mountPath: github
//	  name: octocat
//	  policies: [break-glass]
type GitHubAuthUser struct {
	metav1.TypeMeta `json:",inline"`

	// +optional
	metav1.ObjectMeta `json:"metadata,omitzero"`

	// +required
	Spec GitHubAuthUserSpec `json:"spec"`

	// +optional
	Status GitHubAuthUserStatus `json:"status,omitzero"`
}

// GetResourceSpec implements commonv1alpha1.Managed.
func (o *GitHubAuthUser) GetResourceSpec() *commonv1alpha1.ResourceSpec { return &o.Spec.ResourceSpec }

// GetResourceStatus implements commonv1alpha1.Managed.
func (o *GitHubAuthUser) GetResourceStatus() *commonv1alpha1.ResourceStatus {
	return &o.Status.ResourceStatus
}

// +kubebuilder:object:root=true

// GitHubAuthUserList contains a list of GitHubAuthUser.
type GitHubAuthUserList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []GitHubAuthUser `json:"items"`
}

func init() {
	SchemeBuilder.Register(&GitHubAuthUser{}, &GitHubAuthUserList{})
}
