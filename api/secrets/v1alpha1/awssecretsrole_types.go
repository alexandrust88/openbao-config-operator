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

// The kind of AWS credential a role issues.
// +kubebuilder:validation:Enum=iam_user;assumed_role;federation_token;session_token
type AWSCredentialType string

// +kubebuilder:validation:XValidation:rule="has(self.mountPath) == has(oldSelf.mountPath) && (!has(self.mountPath) || self.mountPath == oldSelf.mountPath)",message="mountPath is immutable"
// +kubebuilder:validation:XValidation:rule="has(self.name) == has(oldSelf.name) && (!has(self.name) || self.name == oldSelf.name)",message="name is immutable"
// +kubebuilder:validation:XValidation:rule="!has(self.roleARNs) || self.credentialType == 'assumed_role'",message="roleARNs requires credentialType assumed_role"
// +kubebuilder:validation:XValidation:rule="(!has(self.userPath) && !has(self.permissionsBoundaryARN)) || self.credentialType == 'iam_user'",message="userPath and permissionsBoundaryARN require credentialType iam_user"
// +kubebuilder:validation:XValidation:rule="(!has(self.defaultSTSTTL) && !has(self.maxSTSTTL)) || self.credentialType == 'assumed_role' || self.credentialType == 'federation_token'",message="defaultSTSTTL and maxSTSTTL require credentialType assumed_role or federation_token"

// spec is the desired role of an AWS secrets engine at `<mountPath>/roles/<name>`.
type AWSSecretsRoleSpec struct {
	commonv1alpha1.ResourceSpec `json:",inline"`

	// mountPath is the path of the AWS secrets engine mount. Defaults to `aws`. Immutable. Example: `aws`.
	// +kubebuilder:default=aws
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=128
	// +kubebuilder:validation:Pattern=`^[A-Za-z0-9_.-]+(/[A-Za-z0-9_.-]+)*$`
	// +optional
	MountPath string `json:"mountPath,omitempty"`

	// name is the role name in OpenBao. Defaults to metadata.name. Immutable. Example: `s3-reader`.
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=128
	// +kubebuilder:validation:Pattern=`^[A-Za-z0-9_.@-]+$`
	// +optional
	Name string `json:"name,omitempty"`

	// credentialType is the kind of credential the role issues (`credential_type`). Allowed: iam_user, assumed_role, federation_token, session_token. Required.
	// +required
	CredentialType AWSCredentialType `json:"credentialType"`

	// roleARNs lists the AWS roles that may be assumed (`role_arns`); assumed_role only. Example: `[arn:aws:iam::123456789012:role/s3-reader]`.
	// +kubebuilder:validation:MaxItems=64
	// +kubebuilder:validation:items:MaxLength=2048
	// +listType=set
	// +optional
	RoleARNs []string `json:"roleARNs,omitempty"`

	// policyARNs lists existing IAM policies attached to the issued credentials (`policy_arns`). Example: `[arn:aws:iam::aws:policy/AmazonS3ReadOnlyAccess]`.
	// +kubebuilder:validation:MaxItems=64
	// +kubebuilder:validation:items:MaxLength=2048
	// +listType=set
	// +optional
	PolicyARNs []string `json:"policyARNs,omitempty"`

	// policyDocument is an IAM policy in JSON attached to the issued credentials (`policy_document`). Example: `{"Version": "2012-10-17", "Statement": [...]}`.
	// +kubebuilder:validation:MaxLength=65536
	// +optional
	PolicyDocument string `json:"policyDocument,omitempty"`

	// iamGroups lists IAM groups whose policies apply to the issued credentials (`iam_groups`). Example: `[readers]`.
	// +kubebuilder:validation:MaxItems=64
	// +kubebuilder:validation:items:MaxLength=128
	// +listType=set
	// +optional
	IAMGroups []string `json:"iamGroups,omitempty"`

	// iamTags are tags set on generated IAM users (`iam_tags`). Up to 50. Example: `{team: billing}`.
	// +kubebuilder:validation:MaxProperties=50
	// +optional
	IAMTags map[string]string `json:"iamTags,omitempty"`

	// sessionTags are session tags set on assumed-role credentials (`session_tags`). Up to 50. Example: `{project: orders}`.
	// +kubebuilder:validation:MaxProperties=50
	// +optional
	SessionTags map[string]string `json:"sessionTags,omitempty"`

	// externalID is the external ID passed when assuming a role (`external_id`). Example: `openbao-prod`.
	// +kubebuilder:validation:MaxLength=1224
	// +optional
	ExternalID string `json:"externalID,omitempty"`

	// defaultSTSTTL is the default lifetime of STS credentials (`default_sts_ttl`), a duration; assumed_role or federation_token only. Example: `1h`.
	// +kubebuilder:validation:Type=string
	// +kubebuilder:validation:Pattern=`^(0|([0-9]+(\.[0-9]+)?(ns|us|ms|s|m|h))+)$`
	// +optional
	DefaultSTSTTL *metav1.Duration `json:"defaultSTSTTL,omitempty"`

	// maxSTSTTL is the maximum lifetime of STS credentials (`max_sts_ttl`), a duration; assumed_role or federation_token only. Example: `12h`.
	// +kubebuilder:validation:Type=string
	// +kubebuilder:validation:Pattern=`^(0|([0-9]+(\.[0-9]+)?(ns|us|ms|s|m|h))+)$`
	// +optional
	MaxSTSTTL *metav1.Duration `json:"maxSTSTTL,omitempty"`

	// userPath is the IAM path of generated users (`user_path`); iam_user only. Defaults to `/`. Example: `/openbao/`.
	// +kubebuilder:validation:MaxLength=512
	// +kubebuilder:validation:Pattern=`^/(.*/)?$`
	// +optional
	UserPath string `json:"userPath,omitempty"`

	// permissionsBoundaryARN is the permissions boundary attached to generated users (`permissions_boundary_arn`); iam_user only. Example: `arn:aws:iam::123456789012:policy/boundary`.
	// +kubebuilder:validation:MaxLength=2048
	// +optional
	PermissionsBoundaryARN string `json:"permissionsBoundaryARN,omitempty"`

	// mfaSerialNumber is the ARN or serial number of the MFA device of the IAM user that requests credentials (`mfa_serial_number`). Example: `arn:aws:iam::123456789012:mfa/ci`.
	// +kubebuilder:validation:MaxLength=256
	// +optional
	MFASerialNumber string `json:"mfaSerialNumber,omitempty"`
}

// status is the observed state of the AWS secrets role.
type AWSSecretsRoleStatus struct {
	commonv1alpha1.ResourceStatus `json:",inline"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:categories=obco;obco-secrets
// +kubebuilder:printcolumn:name="Ready",type=string,JSONPath=`.status.conditions[?(@.type=="Ready")].status`
// +kubebuilder:printcolumn:name="Synced",type=string,JSONPath=`.status.conditions[?(@.type=="Synced")].status`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// AWSSecretsRole manages a role of an AWS secrets engine (external plugin) at `<mount>/roles/<name>`: the kind of AWS credential issued from `<mount>/creds/<name>` and the IAM policies it carries.
//
// Example:
//
//	spec:
//	  connectionRef:
//	    name: openbao
//	  mountPath: aws
//	  name: s3-reader
//	  credentialType: assumed_role
//	  roleARNs: [arn:aws:iam::123456789012:role/s3-reader]
type AWSSecretsRole struct {
	metav1.TypeMeta `json:",inline"`

	// +optional
	metav1.ObjectMeta `json:"metadata,omitzero"`

	// +required
	Spec AWSSecretsRoleSpec `json:"spec"`

	// +optional
	Status AWSSecretsRoleStatus `json:"status,omitzero"`
}

// GetResourceSpec implements commonv1alpha1.Managed.
func (o *AWSSecretsRole) GetResourceSpec() *commonv1alpha1.ResourceSpec { return &o.Spec.ResourceSpec }

// GetResourceStatus implements commonv1alpha1.Managed.
func (o *AWSSecretsRole) GetResourceStatus() *commonv1alpha1.ResourceStatus {
	return &o.Status.ResourceStatus
}

// +kubebuilder:object:root=true

// AWSSecretsRoleList contains a list of AWSSecretsRole.
type AWSSecretsRoleList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []AWSSecretsRole `json:"items"`
}

func init() {
	SchemeBuilder.Register(&AWSSecretsRole{}, &AWSSecretsRoleList{})
}
