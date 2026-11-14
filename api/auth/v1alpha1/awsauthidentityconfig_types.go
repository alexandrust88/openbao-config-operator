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

// Desired identity settings of an AWS auth method, written to auth/<mountPath>/config/identity. deletionPolicy must be Orphan.
type AWSAuthIdentityConfigSpec struct {
	commonv1alpha1.ResourceSpec `json:",inline"`

	// Path of the AWS auth method, without the auth/ prefix. Defaults to aws. Immutable. Example: aws
	// +kubebuilder:default=aws
	// +kubebuilder:validation:MaxLength=128
	// +kubebuilder:validation:Pattern=`^[A-Za-z0-9_.-]+(/[A-Za-z0-9_.-]+)*$`
	// +optional
	MountPath string `json:"mountPath,omitempty"`

	// Value used as the entity alias name of iam logins: role_id, unique_id (the IAM principal's unique ID), full_arn or canonical_arn. OpenBao defaults to role_id. Maps to `iam_alias`. Example: unique_id
	// +kubebuilder:validation:Enum=role_id;unique_id;full_arn;canonical_arn
	// +optional
	IAMAlias string `json:"iamAlias,omitempty"`

	// Fields added as metadata to tokens and alias of iam logins, such as account_id, auth_type, canonical_arn, client_arn, client_user_id, inferred_aws_region, inferred_entity_id or inferred_entity_type. Unset keeps the OpenBao value (the method's default set). The value "default" is not accepted; list the fields explicitly. Maps to `iam_metadata`. Example: [account_id, auth_type, canonical_arn]
	// +kubebuilder:validation:MaxItems=64
	// +kubebuilder:validation:XValidation:rule="!self.exists(m, m == 'default')",message="list the metadata fields explicitly instead of default, which OpenBao reads back as the expanded list"
	// +kubebuilder:validation:items:MinLength=1
	// +kubebuilder:validation:items:MaxLength=512
	// +listType=set
	// +optional
	IAMMetadata []string `json:"iamMetadata,omitempty"`

	// Value used as the entity alias name of ec2 logins: role_id, instance_id or image_id. OpenBao defaults to role_id. Maps to `ec2_alias`. Example: instance_id
	// +kubebuilder:validation:Enum=role_id;instance_id;image_id
	// +optional
	EC2Alias string `json:"ec2Alias,omitempty"`

	// Fields added as metadata to tokens and alias of ec2 logins, such as account_id, ami_id, auth_type, instance_id or region. Unset keeps the OpenBao value (the method's default set). The value "default" is not accepted; list the fields explicitly. Maps to `ec2_metadata`. Example: [account_id, instance_id, region]
	// +kubebuilder:validation:MaxItems=64
	// +kubebuilder:validation:XValidation:rule="!self.exists(m, m == 'default')",message="list the metadata fields explicitly instead of default, which OpenBao reads back as the expanded list"
	// +kubebuilder:validation:items:MinLength=1
	// +kubebuilder:validation:items:MaxLength=512
	// +listType=set
	// +optional
	EC2Metadata []string `json:"ec2Metadata,omitempty"`
}

// Observed state of the identity settings, written by the operator.
type AWSAuthIdentityConfigStatus struct {
	commonv1alpha1.ResourceStatus `json:",inline"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:categories=openbao
// +kubebuilder:printcolumn:name="Ready",type=string,JSONPath=`.status.conditions[?(@.type=="Ready")].status`
// +kubebuilder:printcolumn:name="Synced",type=string,JSONPath=`.status.conditions[?(@.type=="Synced")].status`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// AWSAuthIdentityConfig manages how an OpenBao AWS auth method builds entity alias names and token metadata, at auth/<mountPath>/config/identity. Use it to change the alias of iam or ec2 logins, for example to the full ARN, or to choose the metadata on issued tokens. The AWS auth plugin must be enabled at mountPath (AuthMount). Deleting it leaves the settings in OpenBao.
//
// Example:
//
//	spec:
//	  connectionRef:
//	    name: openbao
//	  mountPath: aws
//	  iamAlias: full_arn
//	  iamMetadata: [account_id, auth_type, canonical_arn]
type AWSAuthIdentityConfig struct {
	metav1.TypeMeta `json:",inline"`

	// +optional
	metav1.ObjectMeta `json:"metadata,omitzero"`

	// +required
	Spec AWSAuthIdentityConfigSpec `json:"spec"`

	// +optional
	Status AWSAuthIdentityConfigStatus `json:"status,omitzero"`
}

// GetResourceSpec implements commonv1alpha1.Managed.
func (o *AWSAuthIdentityConfig) GetResourceSpec() *commonv1alpha1.ResourceSpec {
	return &o.Spec.ResourceSpec
}

// GetResourceStatus implements commonv1alpha1.Managed.
func (o *AWSAuthIdentityConfig) GetResourceStatus() *commonv1alpha1.ResourceStatus {
	return &o.Status.ResourceStatus
}

// +kubebuilder:object:root=true

// AWSAuthIdentityConfigList contains a list of AWSAuthIdentityConfig.
type AWSAuthIdentityConfigList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []AWSAuthIdentityConfig `json:"items"`
}

func init() {
	SchemeBuilder.Register(&AWSAuthIdentityConfig{}, &AWSAuthIdentityConfigList{})
}
