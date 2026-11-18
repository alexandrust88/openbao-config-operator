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
// +kubebuilder:validation:XValidation:rule="!has(self.deletionPolicy) || self.deletionPolicy == 'Orphan'",message="deletionPolicy must be Orphan: the plugin configuration is kept when the resource is deleted"

// spec is the desired GitHub App configuration of a GitHub secrets plugin mount at `<mountPath>/config`. deletionPolicy must be Orphan.
type GitHubSecretsConfigSpec struct {
	commonv1alpha1.ResourceSpec `json:",inline"`

	// mountPath is the path the GitHub secrets plugin is mounted at. Defaults to `github`. Immutable. Example: `github`.
	// +kubebuilder:default=github
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=256
	// +kubebuilder:validation:Pattern=`^[A-Za-z0-9_.-]+(/[A-Za-z0-9_.-]+)*$`
	// +optional
	MountPath string `json:"mountPath,omitempty"`

	// appID is the application ID of the GitHub App (`app_id`). Example: `123456`.
	// +kubebuilder:validation:Minimum=1
	// +required
	AppID int64 `json:"appID"`

	// privateKeySecretRef selects the Secret key holding the PEM private key of the GitHub App (`prv_key`). Read from a Secret key; never stored in status. Write-only: OpenBao never returns it, so a change of the Secret forces a write. Example: `{name: github-app, key: private-key.pem}`.
	// +required
	PrivateKeySecretRef commonv1alpha1.SecretKeySelector `json:"privateKeySecretRef"`

	// baseURL is the base URL of the GitHub API, for GitHub Enterprise Server (`base_url`). Defaults to `https://api.github.com`. Example: `https://github.example.com/api/v3`.
	// +kubebuilder:validation:XValidation:rule="!self.matches('://[^/?#]*@')",message="baseURL must not contain credentials (user:password@ or token@): use the secret reference fields"
	// +kubebuilder:validation:MaxLength=2048
	// +optional
	BaseURL string `json:"baseURL,omitempty"`

	// excludeRepositoryMetadata returns only repository names, not full repository metadata, with tokens limited to repositories (`exclude_repository_metadata`). Defaults to false.
	// +optional
	ExcludeRepositoryMetadata *bool `json:"excludeRepositoryMetadata,omitempty"`
}

// status is the observed state of the GitHub secrets plugin configuration.
type GitHubSecretsConfigStatus struct {
	commonv1alpha1.ResourceStatus `json:",inline"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:categories=obco;obco-secrets
// +kubebuilder:printcolumn:name="Ready",type=string,JSONPath=`.status.conditions[?(@.type=="Ready")].status`
// +kubebuilder:printcolumn:name="Synced",type=string,JSONPath=`.status.conditions[?(@.type=="Synced")].status`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// GitHubSecretsConfig sets the GitHub App of a GitHub secrets plugin mount (external plugin) at `<mount>/config`. OpenBao uses the app's private key to issue short-lived installation tokens. A singleton per mount; it is never deleted.
//
// Example:
//
//	spec:
//	  connectionRef:
//	    name: openbao
//	  mountPath: github
//	  appID: 123456
//	  privateKeySecretRef: {name: github-app, key: private-key.pem}
type GitHubSecretsConfig struct {
	metav1.TypeMeta `json:",inline"`

	// +optional
	metav1.ObjectMeta `json:"metadata,omitzero"`

	// +required
	Spec GitHubSecretsConfigSpec `json:"spec"`

	// +optional
	Status GitHubSecretsConfigStatus `json:"status,omitzero"`
}

// GetResourceSpec implements commonv1alpha1.Managed.
func (o *GitHubSecretsConfig) GetResourceSpec() *commonv1alpha1.ResourceSpec {
	return &o.Spec.ResourceSpec
}

// GetResourceStatus implements commonv1alpha1.Managed.
func (o *GitHubSecretsConfig) GetResourceStatus() *commonv1alpha1.ResourceStatus {
	return &o.Status.ResourceStatus
}

// +kubebuilder:object:root=true

// GitHubSecretsConfigList contains a list of GitHubSecretsConfig.
type GitHubSecretsConfigList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []GitHubSecretsConfig `json:"items"`
}

func init() {
	SchemeBuilder.Register(&GitHubSecretsConfig{}, &GitHubSecretsConfigList{})
}
