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
// +kubebuilder:validation:XValidation:rule="self.accountID == oldSelf.accountID",message="accountID is immutable"

// Desired state of an STS role for one AWS account, written to auth/<mountPath>/config/sts/<accountID>.
type AWSAuthSTSRoleSpec struct {
	commonv1alpha1.ResourceSpec `json:",inline"`

	// Path of the AWS auth method, without the auth/ prefix. Defaults to aws. Immutable. Example: aws
	// +kubebuilder:default=aws
	// +kubebuilder:validation:MaxLength=128
	// +kubebuilder:validation:Pattern=`^[A-Za-z0-9_.-]+(/[A-Za-z0-9_.-]+)*$`
	// +optional
	MountPath string `json:"mountPath,omitempty"`

	// AWS account ID the STS role is used for, as 12 digits; also the last element of the OpenBao path. Required. Immutable. Example: "123456789012"
	// +kubebuilder:validation:MaxLength=12
	// +kubebuilder:validation:Pattern=`^[0-9]{12}$`
	// +required
	AccountID string `json:"accountID"`

	// ARN of the IAM role OpenBao assumes to verify logins from the account. Required. Maps to `sts_role`. Example: arn:aws:iam::123456789012:role/openbao-auth
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=2048
	// +kubebuilder:validation:Pattern=`^arn:`
	// +required
	STSRole string `json:"stsRole"`

	// External ID OpenBao passes when it assumes the role; set it when the role's trust policy requires one. Maps to `external_id`. Example: openbao-prod
	// +kubebuilder:validation:MaxLength=1224
	// +optional
	ExternalID string `json:"externalID,omitempty"`
}

// Observed state of the STS role, written by the operator.
type AWSAuthSTSRoleStatus struct {
	commonv1alpha1.ResourceStatus `json:",inline"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:categories=obco;obco-auth
// +kubebuilder:printcolumn:name="Ready",type=string,JSONPath=`.status.conditions[?(@.type=="Ready")].status`
// +kubebuilder:printcolumn:name="Synced",type=string,JSONPath=`.status.conditions[?(@.type=="Synced")].status`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// AWSAuthSTSRole manages the STS role an OpenBao AWS auth method assumes for one AWS account, at auth/<mountPath>/config/sts/<accountID>. Use it for cross-account logins: OpenBao assumes this role to look up EC2 instances and IAM principals of another account. The AWS auth plugin must be enabled at mountPath (AuthMount).
//
// Example:
//
//	spec:
//	  connectionRef:
//	    name: openbao
//	  mountPath: aws
//	  accountID: "123456789012"
//	  stsRole: arn:aws:iam::123456789012:role/openbao-auth
type AWSAuthSTSRole struct {
	metav1.TypeMeta `json:",inline"`

	// +optional
	metav1.ObjectMeta `json:"metadata,omitzero"`

	// +required
	Spec AWSAuthSTSRoleSpec `json:"spec"`

	// +optional
	Status AWSAuthSTSRoleStatus `json:"status,omitzero"`
}

// GetResourceSpec implements commonv1alpha1.Managed.
func (o *AWSAuthSTSRole) GetResourceSpec() *commonv1alpha1.ResourceSpec { return &o.Spec.ResourceSpec }

// GetResourceStatus implements commonv1alpha1.Managed.
func (o *AWSAuthSTSRole) GetResourceStatus() *commonv1alpha1.ResourceStatus {
	return &o.Status.ResourceStatus
}

// +kubebuilder:object:root=true

// AWSAuthSTSRoleList contains a list of AWSAuthSTSRole.
type AWSAuthSTSRoleList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []AWSAuthSTSRole `json:"items"`
}

func init() {
	SchemeBuilder.Register(&AWSAuthSTSRole{}, &AWSAuthSTSRoleList{})
}
