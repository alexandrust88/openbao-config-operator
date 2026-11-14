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
// +kubebuilder:validation:XValidation:rule="!has(self.deletionPolicy) || self.deletionPolicy == 'Orphan'",message="deletionPolicy must be Orphan: the AWS secrets engine configuration cannot be deleted"
// +kubebuilder:validation:XValidation:rule="has(self.accessKeySecretRef) == has(self.secretKeySecretRef)",message="set both or neither of accessKeySecretRef and secretKeySecretRef"
// +kubebuilder:validation:XValidation:rule="has(self.lease) == has(self.leaseMax)",message="set both or neither of lease and leaseMax"

// spec is the desired root configuration of an AWS secrets engine at `<mountPath>/config/root` and its lease settings at `<mountPath>/config/lease`. The engine mount itself is managed by a SecretsMount. deletionPolicy must be Orphan.
type AWSSecretsConfigSpec struct {
	commonv1alpha1.ResourceSpec `json:",inline"`

	// mountPath is the path of the AWS secrets engine mount. Defaults to `aws`. Immutable. Example: `aws`.
	// +kubebuilder:default=aws
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=128
	// +kubebuilder:validation:Pattern=`^[A-Za-z0-9_.-]+(/[A-Za-z0-9_.-]+)*$`
	// +optional
	MountPath string `json:"mountPath,omitempty"`

	// accessKeySecretRef selects the Secret key that holds the AWS access key ID (`access_key`); set together with secretKeySecretRef. Without credentials the engine uses the AWS credentials of its environment (instance profile, environment variables). Read from a Secret key; never stored in status. Example: `{name: aws-root, key: access-key-id}`.
	// +optional
	AccessKeySecretRef *commonv1alpha1.SecretKeySelector `json:"accessKeySecretRef,omitempty"`

	// secretKeySecretRef selects the Secret key that holds the AWS secret access key (`secret_key`). Read from a Secret key; never stored in status. Write-only: a change of the Secret is written again. Example: `{name: aws-root, key: secret-access-key}`.
	// +optional
	SecretKeySecretRef *commonv1alpha1.SecretKeySelector `json:"secretKeySecretRef,omitempty"`

	// region is the AWS region for API calls (`region`). Defaults to us-east-1. Example: `eu-central-1`.
	// +kubebuilder:validation:MaxLength=64
	// +optional
	Region string `json:"region,omitempty"`

	// iamEndpoint is a custom IAM endpoint URL (`iam_endpoint`). Example: `https://iam.amazonaws.com`.
	// +kubebuilder:validation:MaxLength=2048
	// +optional
	IAMEndpoint string `json:"iamEndpoint,omitempty"`

	// stsEndpoint is a custom STS endpoint URL (`sts_endpoint`). Example: `https://sts.eu-central-1.amazonaws.com`.
	// +kubebuilder:validation:MaxLength=2048
	// +optional
	STSEndpoint string `json:"stsEndpoint,omitempty"`

	// maxRetries is the number of retries for recoverable AWS errors (`max_retries`); -1 uses the AWS SDK default. Defaults to -1.
	// +kubebuilder:validation:Minimum=-1
	// +optional
	MaxRetries *int32 `json:"maxRetries,omitempty"`

	// usernameTemplate is the template for the names of generated IAM users (`username_template`). Example: `{{printf "vault-%s-%s" (.RoleName) (random 20)}}`.
	// +kubebuilder:validation:MaxLength=1024
	// +optional
	UsernameTemplate string `json:"usernameTemplate,omitempty"`

	// lease is the default lease duration of generated credentials (`lease`), a duration; set together with leaseMax. Example: `1h`.
	// +kubebuilder:validation:Type=string
	// +kubebuilder:validation:Pattern=`^(0|([0-9]+(\.[0-9]+)?(ns|us|ms|s|m|h))+)$`
	// +optional
	Lease *metav1.Duration `json:"lease,omitempty"`

	// leaseMax is the maximum lease duration of generated credentials (`lease_max`), a duration. Example: `24h`.
	// +kubebuilder:validation:Type=string
	// +kubebuilder:validation:Pattern=`^(0|([0-9]+(\.[0-9]+)?(ns|us|ms|s|m|h))+)$`
	// +optional
	LeaseMax *metav1.Duration `json:"leaseMax,omitempty"`
}

// status is the observed state of the AWS secrets engine configuration.
type AWSSecretsConfigStatus struct {
	commonv1alpha1.ResourceStatus `json:",inline"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:categories=openbao
// +kubebuilder:printcolumn:name="Ready",type=string,JSONPath=`.status.conditions[?(@.type=="Ready")].status`
// +kubebuilder:printcolumn:name="Synced",type=string,JSONPath=`.status.conditions[?(@.type=="Synced")].status`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// AWSSecretsConfig sets the root credentials (`<mount>/config/root`) and lease settings (`<mount>/config/lease`) of an AWS secrets engine, an external plugin that must be registered. A singleton per mount; it is never deleted.
//
// Example:
//
//	spec:
//	  connectionRef:
//	    name: openbao
//	  mountPath: aws
//	  region: eu-central-1
//	  accessKeySecretRef: {name: aws-root, key: access-key-id}
//	  secretKeySecretRef: {name: aws-root, key: secret-access-key}
type AWSSecretsConfig struct {
	metav1.TypeMeta `json:",inline"`

	// +optional
	metav1.ObjectMeta `json:"metadata,omitzero"`

	// +required
	Spec AWSSecretsConfigSpec `json:"spec"`

	// +optional
	Status AWSSecretsConfigStatus `json:"status,omitzero"`
}

// GetResourceSpec implements commonv1alpha1.Managed.
func (o *AWSSecretsConfig) GetResourceSpec() *commonv1alpha1.ResourceSpec {
	return &o.Spec.ResourceSpec
}

// GetResourceStatus implements commonv1alpha1.Managed.
func (o *AWSSecretsConfig) GetResourceStatus() *commonv1alpha1.ResourceStatus {
	return &o.Status.ResourceStatus
}

// +kubebuilder:object:root=true

// AWSSecretsConfigList contains a list of AWSSecretsConfig.
type AWSSecretsConfigList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []AWSSecretsConfig `json:"items"`
}

func init() {
	SchemeBuilder.Register(&AWSSecretsConfig{}, &AWSSecretsConfigList{})
}
