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
// +kubebuilder:validation:XValidation:rule="!has(self.deletionPolicy) || self.deletionPolicy == 'Orphan'",message="deletionPolicy must be Orphan: an auth method config has no delete operation"
// +kubebuilder:validation:XValidation:rule="has(self.accessKeySecretRef) == has(self.secretKeySecretRef)",message="set both or neither of accessKeySecretRef and secretKeySecretRef"
// +kubebuilder:validation:XValidation:rule="!has(self.stsRegion) || has(self.stsEndpoint)",message="stsRegion requires stsEndpoint"

// Desired AWS client configuration of an AWS auth method, written to auth/<mountPath>/config/client. Set both or neither of accessKeySecretRef and secretKeySecretRef; deletionPolicy must be Orphan.
type AWSAuthConfigSpec struct {
	commonv1alpha1.ResourceSpec `json:",inline"`

	// Path of the AWS auth method, without the auth/ prefix. Defaults to aws. Immutable. Example: aws
	// +kubebuilder:default=aws
	// +kubebuilder:validation:MaxLength=128
	// +kubebuilder:validation:Pattern=`^[A-Za-z0-9_.-]+(/[A-Za-z0-9_.-]+)*$`
	// +optional
	MountPath string `json:"mountPath,omitempty"`

	// Secret key holding the AWS access key ID OpenBao uses to call AWS APIs. Read from this Secret key; the value is never stored in status. Requires secretKeySecretRef. Unset makes OpenBao use the credentials of its environment, such as an instance profile. Maps to `access_key`. Example: {name: aws-auth, key: access-key}
	// +optional
	AccessKeySecretRef *commonv1alpha1.SecretKeySelector `json:"accessKeySecretRef,omitempty"`

	// Secret key holding the AWS secret access key that belongs to the access key. Read from this Secret key; the value is never stored in status. Write-only: OpenBao never returns it, so only a hash of it is tracked and a change of the Secret triggers a rewrite. Maps to `secret_key`. Example: {name: aws-auth, key: secret-key}
	// +optional
	SecretKeySecretRef *commonv1alpha1.SecretKeySelector `json:"secretKeySecretRef,omitempty"`

	// URL that overrides the endpoint used for EC2 API calls, for example a VPC endpoint. Maps to `endpoint`. Example: https://ec2.eu-central-1.amazonaws.com
	// +kubebuilder:validation:MaxLength=2048
	// +optional
	EC2Endpoint string `json:"ec2Endpoint,omitempty"`

	// URL that overrides the endpoint used for IAM API calls. Maps to `iam_endpoint`. Example: https://iam.amazonaws.com
	// +kubebuilder:validation:MaxLength=2048
	// +optional
	IAMEndpoint string `json:"iamEndpoint,omitempty"`

	// URL that overrides the endpoint used for STS API calls, such as a regional STS endpoint. Maps to `sts_endpoint`. Example: https://sts.eu-central-1.amazonaws.com
	// +kubebuilder:validation:MaxLength=2048
	// +optional
	STSEndpoint string `json:"stsEndpoint,omitempty"`

	// AWS region used to sign STS API calls to stsEndpoint; requires stsEndpoint. Maps to `sts_region`. Example: eu-central-1
	// +kubebuilder:validation:MaxLength=64
	// +optional
	STSRegion string `json:"stsRegion,omitempty"`

	// When true, STS calls use the region from the client's signed login request instead of stsRegion. Defaults to false. Maps to `use_sts_region_from_client`. Example: true
	// +optional
	UseSTSRegionFromClient *bool `json:"useSTSRegionFromClient,omitempty"`

	// Value that iam logins must sign in the X-Vault-AWS-IAM-Server-ID header, which stops a signed login request from being replayed against another server. Unset disables the check. Maps to `iam_server_id_header_value`. Example: openbao.example.com
	// +kubebuilder:validation:MaxLength=1024
	// +optional
	IAMServerIDHeaderValue string `json:"iamServerIDHeaderValue,omitempty"`

	// Extra header names allowed in the signed STS request of an iam login. Maps to `allowed_sts_header_values`. Example: [X-Custom-Header]
	// +kubebuilder:validation:MaxItems=64
	// +kubebuilder:validation:items:MinLength=1
	// +kubebuilder:validation:items:MaxLength=512
	// +listType=set
	// +optional
	AllowedSTSHeaderValues []string `json:"allowedSTSHeaderValues,omitempty"`

	// Number of retries for recoverable AWS API errors; -1 uses the AWS SDK default. OpenBao defaults to -1. Maps to `max_retries`. Example: 5
	// +kubebuilder:validation:Minimum=-1
	// +optional
	MaxRetries *int32 `json:"maxRetries,omitempty"`
}

// Observed state of the client configuration, written by the operator.
type AWSAuthConfigStatus struct {
	commonv1alpha1.ResourceStatus `json:",inline"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:categories=openbao
// +kubebuilder:printcolumn:name="Ready",type=string,JSONPath=`.status.conditions[?(@.type=="Ready")].status`
// +kubebuilder:printcolumn:name="Synced",type=string,JSONPath=`.status.conditions[?(@.type=="Synced")].status`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// AWSAuthConfig manages the AWS client configuration of an OpenBao AWS auth method at auth/<mountPath>/config/client. Use it to give OpenBao the credentials and endpoints it uses to verify iam and ec2 logins; without credentials OpenBao uses its environment (instance profile, IRSA). The AWS auth plugin must be enabled at mountPath (AuthMount). Deleting it leaves the settings in OpenBao.
//
// Example:
//
//	spec:
//	  connectionRef:
//	    name: openbao
//	  mountPath: aws
//	  accessKeySecretRef: {name: aws-auth, key: access-key}
//	  secretKeySecretRef: {name: aws-auth, key: secret-key}
//	  iamServerIDHeaderValue: openbao.example.com
type AWSAuthConfig struct {
	metav1.TypeMeta `json:",inline"`

	// +optional
	metav1.ObjectMeta `json:"metadata,omitzero"`

	// +required
	Spec AWSAuthConfigSpec `json:"spec"`

	// +optional
	Status AWSAuthConfigStatus `json:"status,omitzero"`
}

// GetResourceSpec implements commonv1alpha1.Managed.
func (o *AWSAuthConfig) GetResourceSpec() *commonv1alpha1.ResourceSpec { return &o.Spec.ResourceSpec }

// GetResourceStatus implements commonv1alpha1.Managed.
func (o *AWSAuthConfig) GetResourceStatus() *commonv1alpha1.ResourceStatus {
	return &o.Status.ResourceStatus
}

// +kubebuilder:object:root=true

// AWSAuthConfigList contains a list of AWSAuthConfig.
type AWSAuthConfigList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []AWSAuthConfig `json:"items"`
}

func init() {
	SchemeBuilder.Register(&AWSAuthConfig{}, &AWSAuthConfigList{})
}
