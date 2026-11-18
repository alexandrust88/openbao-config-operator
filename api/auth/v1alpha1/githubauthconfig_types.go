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
// +kubebuilder:validation:XValidation:rule="!has(self.deletionPolicy) || self.deletionPolicy == 'Orphan'",message="deletionPolicy must be Orphan: the config endpoint cannot be deleted"

// Desired state of the GitHub auth method configuration, written to auth/<mountPath>/config. deletionPolicy must be Orphan because the config endpoint cannot be deleted.
type GitHubAuthConfigSpec struct {
	commonv1alpha1.ResourceSpec `json:",inline"`

	// Path of the GitHub auth method, without the auth/ prefix. Defaults to github. Immutable. Example: github/example-org
	// +kubebuilder:default=github
	// +kubebuilder:validation:MaxLength=128
	// +kubebuilder:validation:Pattern=`^[A-Za-z0-9_.-]+(/[A-Za-z0-9_.-]+)*$`
	// +optional
	MountPath string `json:"mountPath,omitempty"`

	// GitHub organization whose members may log in. Required. Maps to `organization`. Example: example-org
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=256
	// +required
	Organization string `json:"organization"`

	// Numeric ID of the organization. Set it when OpenBao cannot reach GitHub: when unset, OpenBao looks the ID up through the GitHub API on every write. Maps to `organization_id`. Example: 1234567
	// +kubebuilder:validation:Minimum=1
	// +optional
	OrganizationID *int64 `json:"organizationID,omitempty"`

	// API endpoint of a GitHub Enterprise Server; leave unset for github.com. OpenBao appends a trailing slash when it is missing. Maps to `base_url`. Example: https://github.example.com/api/v3/
	// +kubebuilder:validation:MaxLength=2048
	// +optional
	BaseURL string `json:"baseURL,omitempty"`

	TokenFields `json:",inline"`
}

// Observed state of the GitHub auth method configuration, written by the operator.
type GitHubAuthConfigStatus struct {
	commonv1alpha1.ResourceStatus `json:",inline"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:categories=obco;obco-auth
// +kubebuilder:printcolumn:name="Ready",type=string,JSONPath=`.status.conditions[?(@.type=="Ready")].status`
// +kubebuilder:printcolumn:name="Synced",type=string,JSONPath=`.status.conditions[?(@.type=="Synced")].status`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// GitHubAuthConfig manages the configuration of an OpenBao GitHub auth method at auth/<mountPath>/config. Use it to set the GitHub organization whose members log in with a personal access token, and the token settings of those logins. Needs the github auth plugin enabled at auth/<mountPath>; there is one config per mount and it is never deleted.
//
// Example:
//
//	spec:
//	  connectionRef:
//	    name: openbao
//	  mountPath: github
//	  organization: example-org
//	  organizationID: 1234567
//	  tokenTTL: 8h
type GitHubAuthConfig struct {
	metav1.TypeMeta `json:",inline"`

	// +optional
	metav1.ObjectMeta `json:"metadata,omitzero"`

	// +required
	Spec GitHubAuthConfigSpec `json:"spec"`

	// +optional
	Status GitHubAuthConfigStatus `json:"status,omitzero"`
}

// GetResourceSpec implements commonv1alpha1.Managed.
func (o *GitHubAuthConfig) GetResourceSpec() *commonv1alpha1.ResourceSpec {
	return &o.Spec.ResourceSpec
}

// GetResourceStatus implements commonv1alpha1.Managed.
func (o *GitHubAuthConfig) GetResourceStatus() *commonv1alpha1.ResourceStatus {
	return &o.Status.ResourceStatus
}

// +kubebuilder:object:root=true

// GitHubAuthConfigList contains a list of GitHubAuthConfig.
type GitHubAuthConfigList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []GitHubAuthConfig `json:"items"`
}

func init() {
	SchemeBuilder.Register(&GitHubAuthConfig{}, &GitHubAuthConfigList{})
}
