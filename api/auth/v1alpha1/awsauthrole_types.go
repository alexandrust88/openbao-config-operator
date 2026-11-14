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
// +kubebuilder:validation:XValidation:rule="(has(self.authType) ? self.authType : 'iam') == (has(oldSelf.authType) ? oldSelf.authType : 'iam')",message="authType is immutable"
// +kubebuilder:validation:XValidation:rule="(has(self.authType) ? self.authType : 'iam') == 'ec2' || (!has(self.roleTag) && !has(self.allowInstanceMigration) && !has(self.disallowReauthentication))",message="roleTag, allowInstanceMigration and disallowReauthentication apply only to authType ec2"
// +kubebuilder:validation:XValidation:rule="(has(self.authType) ? self.authType : 'iam') == 'iam' || (!has(self.boundIAMPrincipalARNs) && !has(self.inferredEntityType) && !has(self.inferredAWSRegion) && !has(self.resolveAWSUniqueIDs))",message="boundIAMPrincipalARNs, inferredEntityType, inferredAWSRegion and resolveAWSUniqueIDs apply only to authType iam"
// +kubebuilder:validation:XValidation:rule="(has(self.authType) ? self.authType : 'iam') == 'ec2' || has(self.inferredEntityType) || (!has(self.boundAMIIDs) && !has(self.boundAccountIDs) && !has(self.boundRegions) && !has(self.boundVPCIDs) && !has(self.boundSubnetIDs) && !has(self.boundIAMRoleARNs) && !has(self.boundIAMInstanceProfileARNs) && !has(self.boundEC2InstanceIDs))",message="the EC2 bound fields need authType ec2, or authType iam with inferredEntityType"
// +kubebuilder:validation:XValidation:rule="has(self.inferredEntityType) == has(self.inferredAWSRegion)",message="set both or neither of inferredEntityType and inferredAWSRegion"
// +kubebuilder:validation:XValidation:rule="(has(self.resolveAWSUniqueIDs) ? self.resolveAWSUniqueIDs : true) || !(has(oldSelf.resolveAWSUniqueIDs) ? oldSelf.resolveAWSUniqueIDs : true)",message="resolveAWSUniqueIDs cannot change from true to false"

// Desired state of an AWS auth role, written to auth/<mountPath>/role/<name>. The EC2 bound fields (boundAMIIDs to boundEC2InstanceIDs) need authType ec2, or authType iam with inferredEntityType; a login must match every bound list that is set.
type AWSAuthRoleSpec struct {
	commonv1alpha1.ResourceSpec `json:",inline"`

	// Path of the AWS auth method, without the auth/ prefix. Defaults to aws. Immutable. Example: aws
	// +kubebuilder:default=aws
	// +kubebuilder:validation:MaxLength=128
	// +kubebuilder:validation:Pattern=`^[A-Za-z0-9_.-]+(/[A-Za-z0-9_.-]+)*$`
	// +optional
	MountPath string `json:"mountPath,omitempty"`

	// Name of the role in OpenBao, used in the login request. Defaults to metadata.name. Immutable. Example: app
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=128
	// +kubebuilder:validation:Pattern=`^[A-Za-z0-9_]([A-Za-z0-9_.-]*[A-Za-z0-9_])?$`
	// +optional
	Name string `json:"name,omitempty"`

	// Login method the role allows: iam (signed sts:GetCallerIdentity request) or ec2 (signed instance identity document). Defaults to iam. Immutable. Maps to `auth_type`. Example: ec2
	// +kubebuilder:validation:Enum=iam;ec2
	// +kubebuilder:validation:MaxLength=3
	// +kubebuilder:default=iam
	// +optional
	AuthType string `json:"authType,omitempty"`

	// AMI IDs the logging-in EC2 instance must be started from. Maps to `bound_ami_id`. Example: [ami-0123456789abcdef0]
	// +kubebuilder:validation:MaxItems=64
	// +kubebuilder:validation:items:MinLength=1
	// +kubebuilder:validation:items:MaxLength=512
	// +listType=set
	// +optional
	BoundAMIIDs []string `json:"boundAMIIDs,omitempty"`

	// AWS account IDs the logging-in EC2 instance must belong to. Maps to `bound_account_id`. Example: ["123456789012"]
	// +kubebuilder:validation:MaxItems=64
	// +kubebuilder:validation:items:MinLength=1
	// +kubebuilder:validation:items:MaxLength=512
	// +listType=set
	// +optional
	BoundAccountIDs []string `json:"boundAccountIDs,omitempty"`

	// AWS regions the logging-in EC2 instance must run in. Maps to `bound_region`. Example: [eu-central-1]
	// +kubebuilder:validation:MaxItems=64
	// +kubebuilder:validation:items:MinLength=1
	// +kubebuilder:validation:items:MaxLength=512
	// +listType=set
	// +optional
	BoundRegions []string `json:"boundRegions,omitempty"`

	// VPC IDs the logging-in EC2 instance must run in. Maps to `bound_vpc_id`. Example: [vpc-0123456789abcdef0]
	// +kubebuilder:validation:MaxItems=64
	// +kubebuilder:validation:items:MinLength=1
	// +kubebuilder:validation:items:MaxLength=512
	// +listType=set
	// +optional
	BoundVPCIDs []string `json:"boundVPCIDs,omitempty"`

	// Subnet IDs the logging-in EC2 instance must run in. Maps to `bound_subnet_id`. Example: [subnet-0123456789abcdef0]
	// +kubebuilder:validation:MaxItems=64
	// +kubebuilder:validation:items:MinLength=1
	// +kubebuilder:validation:items:MaxLength=512
	// +listType=set
	// +optional
	BoundSubnetIDs []string `json:"boundSubnetIDs,omitempty"`

	// IAM role ARNs, one of which must be in the instance profile of the logging-in EC2 instance; a trailing "*" matches a prefix. Maps to `bound_iam_role_arn`. Example: [arn:aws:iam::123456789012:role/app]
	// +kubebuilder:validation:MaxItems=64
	// +kubebuilder:validation:items:MinLength=1
	// +kubebuilder:validation:items:MaxLength=512
	// +listType=set
	// +optional
	BoundIAMRoleARNs []string `json:"boundIAMRoleARNs,omitempty"`

	// Instance profile ARNs the logging-in EC2 instance must have one of; a trailing "*" matches a prefix. Maps to `bound_iam_instance_profile_arn`. Example: [arn:aws:iam::123456789012:instance-profile/app]
	// +kubebuilder:validation:MaxItems=64
	// +kubebuilder:validation:items:MinLength=1
	// +kubebuilder:validation:items:MaxLength=512
	// +listType=set
	// +optional
	BoundIAMInstanceProfileARNs []string `json:"boundIAMInstanceProfileARNs,omitempty"`

	// EC2 instance IDs allowed to log in. Maps to `bound_ec2_instance_id`. Example: [i-0123456789abcdef0]
	// +kubebuilder:validation:MaxItems=64
	// +kubebuilder:validation:items:MinLength=1
	// +kubebuilder:validation:items:MaxLength=512
	// +listType=set
	// +optional
	BoundEC2InstanceIDs []string `json:"boundEC2InstanceIDs,omitempty"`

	// IAM principal ARNs (users or roles) allowed to log in with authType iam; a trailing "*" matches a prefix. Maps to `bound_iam_principal_arn`. Example: [arn:aws:iam::123456789012:role/app]
	// +kubebuilder:validation:MaxItems=64
	// +kubebuilder:validation:items:MinLength=1
	// +kubebuilder:validation:items:MaxLength=512
	// +listType=set
	// +optional
	BoundIAMPrincipalARNs []string `json:"boundIAMPrincipalARNs,omitempty"`

	// EC2 instance tag key that holds a role tag, which can further restrict the role's policies and settings; only for authType ec2. Maps to `role_tag`. Example: OpenBaoRole
	// +kubebuilder:validation:MaxLength=127
	// +optional
	RoleTag string `json:"roleTag,omitempty"`

	// Makes an iam login be checked as a login of the inferred entity, so the EC2 bound fields apply; requires inferredAWSRegion and authType iam. Allowed value: ec2_instance. Maps to `inferred_entity_type`. Example: ec2_instance
	// +kubebuilder:validation:Enum=ec2_instance
	// +kubebuilder:validation:MaxLength=16
	// +optional
	InferredEntityType string `json:"inferredEntityType,omitempty"`

	// AWS region in which the inferred entity is looked up; requires inferredEntityType. Maps to `inferred_aws_region`. Example: eu-central-1
	// +kubebuilder:validation:MaxLength=64
	// +optional
	InferredAWSRegion string `json:"inferredAWSRegion,omitempty"`

	// When true, boundIAMPrincipalARNs are resolved to AWS unique IDs, so a deleted and recreated principal with the same ARN cannot log in. Only for authType iam. OpenBao defaults to true; it cannot be changed from true to false. Maps to `resolve_aws_unique_ids`. Example: false
	// +optional
	ResolveAWSUniqueIDs *bool `json:"resolveAWSUniqueIDs,omitempty"`

	// When true, an EC2 instance may log in again after it moved to a new host (its pending time changed); use with caution. Only for authType ec2. Defaults to false. Maps to `allow_instance_migration`. Example: true
	// +optional
	AllowInstanceMigration *bool `json:"allowInstanceMigration,omitempty"`

	// When true, each EC2 instance can log in only once; later logins are refused. Only for authType ec2. Defaults to false. Maps to `disallow_reauthentication`. Example: true
	// +optional
	DisallowReauthentication *bool `json:"disallowReauthentication,omitempty"`

	TokenFields `json:",inline"`
}

// Observed state of the role, written by the operator.
type AWSAuthRoleStatus struct {
	commonv1alpha1.ResourceStatus `json:",inline"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:categories=openbao
// +kubebuilder:printcolumn:name="Ready",type=string,JSONPath=`.status.conditions[?(@.type=="Ready")].status`
// +kubebuilder:printcolumn:name="Synced",type=string,JSONPath=`.status.conditions[?(@.type=="Synced")].status`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// AWSAuthRole manages a role of an OpenBao AWS auth method at auth/<mountPath>/role/<name>. Use it to let AWS workloads log in with signed IAM credentials (authType iam) or an EC2 instance identity document (authType ec2) and receive an OpenBao token with the listed policies. The AWS auth plugin must be enabled at mountPath (AuthMount).
//
// Example:
//
//	spec:
//	  connectionRef:
//	    name: openbao
//	  mountPath: aws
//	  authType: iam
//	  boundIAMPrincipalARNs: [arn:aws:iam::123456789012:role/app]
//	  tokenPolicies: [app-read]
//	  tokenTTL: 1h
type AWSAuthRole struct {
	metav1.TypeMeta `json:",inline"`

	// +optional
	metav1.ObjectMeta `json:"metadata,omitzero"`

	// +required
	Spec AWSAuthRoleSpec `json:"spec"`

	// +optional
	Status AWSAuthRoleStatus `json:"status,omitzero"`
}

// GetResourceSpec implements commonv1alpha1.Managed.
func (o *AWSAuthRole) GetResourceSpec() *commonv1alpha1.ResourceSpec { return &o.Spec.ResourceSpec }

// GetResourceStatus implements commonv1alpha1.Managed.
func (o *AWSAuthRole) GetResourceStatus() *commonv1alpha1.ResourceStatus {
	return &o.Status.ResourceStatus
}

// +kubebuilder:object:root=true

// AWSAuthRoleList contains a list of AWSAuthRole.
type AWSAuthRoleList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []AWSAuthRole `json:"items"`
}

func init() {
	SchemeBuilder.Register(&AWSAuthRole{}, &AWSAuthRoleList{})
}
