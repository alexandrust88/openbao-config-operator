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

// spec is the desired configuration of the Terraform Cloud secrets plugin at `<mountPath>/config`: the HCP Terraform or Terraform Enterprise instance and its API token. deletionPolicy must be Orphan.
type TerraformCloudSecretsConfigSpec struct {
	commonv1alpha1.ResourceSpec `json:",inline"`

	// mountPath is the path the Terraform Cloud secrets plugin is mounted at. Defaults to `terraform`. Immutable. Example: `terraform`.
	// +kubebuilder:default=terraform
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=256
	// +kubebuilder:validation:Pattern=`^[A-Za-z0-9_.-]+(/[A-Za-z0-9_.-]+)*$`
	// +optional
	MountPath string `json:"mountPath,omitempty"`

	// address is the URL of the Terraform instance (`address`). Defaults to `https://app.terraform.io`. Example: `https://tfe.example.com`.
	// +kubebuilder:validation:XValidation:rule="!self.matches('://[^/?#]*@')",message="address must not contain credentials (user:password@ or token@): use the secret reference fields"
	// +kubebuilder:validation:MaxLength=2048
	// +optional
	Address string `json:"address,omitempty"`

	// basePath is the base path of the Terraform API (`base_path`). Defaults to `/api/v2/`.
	// +kubebuilder:validation:MaxLength=256
	// +optional
	BasePath string `json:"basePath,omitempty"`

	// tokenSecretRef selects the Secret key holding the Terraform API token the plugin uses (`token`). Read from a Secret key; never stored in status. Write-only. Example: `{name: tfc-token, key: token}`.
	// +required
	TokenSecretRef commonv1alpha1.SecretKeySelector `json:"tokenSecretRef"`

	// explicitMaxTTL is the expiration the plugin sets on the token it creates when it rotates its own token (`explicit_max_ttl`), a duration. Unset or 0 lets Terraform apply its default expiry. Needs a plugin version that supports it. Example: `8760h`.
	// +kubebuilder:validation:Type=string
	// +kubebuilder:validation:Pattern=`^(0|([0-9]+(\.[0-9]+)?(ns|us|ms|s|m|h))+)$`
	// +optional
	ExplicitMaxTTL *metav1.Duration `json:"explicitMaxTTL,omitempty"`
}

// status is the observed state of the Terraform Cloud secrets plugin configuration.
type TerraformCloudSecretsConfigStatus struct {
	commonv1alpha1.ResourceStatus `json:",inline"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:categories=openbao
// +kubebuilder:printcolumn:name="Ready",type=string,JSONPath=`.status.conditions[?(@.type=="Ready")].status`
// +kubebuilder:printcolumn:name="Synced",type=string,JSONPath=`.status.conditions[?(@.type=="Synced")].status`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// TerraformCloudSecretsConfig sets the instance and API token of a Terraform Cloud secrets plugin mount (external plugin) at `<mount>/config`, which issues HCP Terraform or Terraform Enterprise API tokens. A singleton per mount; it is never deleted.
//
// Example:
//
//	spec:
//	  connectionRef:
//	    name: openbao
//	  mountPath: terraform
//	  tokenSecretRef: {name: tfc-token, key: token}
type TerraformCloudSecretsConfig struct {
	metav1.TypeMeta `json:",inline"`

	// +optional
	metav1.ObjectMeta `json:"metadata,omitzero"`

	// +required
	Spec TerraformCloudSecretsConfigSpec `json:"spec"`

	// +optional
	Status TerraformCloudSecretsConfigStatus `json:"status,omitzero"`
}

// GetResourceSpec implements commonv1alpha1.Managed.
func (o *TerraformCloudSecretsConfig) GetResourceSpec() *commonv1alpha1.ResourceSpec {
	return &o.Spec.ResourceSpec
}

// GetResourceStatus implements commonv1alpha1.Managed.
func (o *TerraformCloudSecretsConfig) GetResourceStatus() *commonv1alpha1.ResourceStatus {
	return &o.Status.ResourceStatus
}

// +kubebuilder:object:root=true

// TerraformCloudSecretsConfigList contains a list of TerraformCloudSecretsConfig.
type TerraformCloudSecretsConfigList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []TerraformCloudSecretsConfig `json:"items"`
}

func init() {
	SchemeBuilder.Register(&TerraformCloudSecretsConfig{}, &TerraformCloudSecretsConfigList{})
}
