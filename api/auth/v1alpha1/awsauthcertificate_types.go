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

// Desired state of an AWS public certificate, written to auth/<mountPath>/config/certificate/<name>. The certificate verifies the signature of EC2 instance identity documents.
type AWSAuthCertificateSpec struct {
	commonv1alpha1.ResourceSpec `json:",inline"`

	// Path of the AWS auth method, without the auth/ prefix. Defaults to aws. Immutable. Example: aws
	// +kubebuilder:default=aws
	// +kubebuilder:validation:MaxLength=128
	// +kubebuilder:validation:Pattern=`^[A-Za-z0-9_.-]+(/[A-Za-z0-9_.-]+)*$`
	// +optional
	MountPath string `json:"mountPath,omitempty"`

	// Name of the certificate in OpenBao. Defaults to metadata.name. Immutable. Example: eu-central-1
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=128
	// +kubebuilder:validation:Pattern=`^[A-Za-z0-9_]([A-Za-z0-9_.-]*[A-Za-z0-9_])?$`
	// +optional
	Name string `json:"name,omitempty"`

	// AWS public certificate for the region of the instances, in PEM format (not a secret). Required. Maps to `aws_public_cert`. Example: "-----BEGIN CERTIFICATE-----\nMIIC..."
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=16384
	// +kubebuilder:validation:Pattern=`^\s*-----BEGIN CERTIFICATE-----`
	// +required
	AWSPublicCert string `json:"awsPublicCert"`

	// Type of signature the certificate verifies: pkcs7 (the PKCS#7 signature of the identity document) or identity (the plain identity signature). OpenBao defaults to pkcs7. Maps to `type`. Example: identity
	// +kubebuilder:validation:Enum=pkcs7;identity
	// +optional
	Type string `json:"type,omitempty"`
}

// Observed state of the certificate, written by the operator.
type AWSAuthCertificateStatus struct {
	commonv1alpha1.ResourceStatus `json:",inline"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:categories=openbao
// +kubebuilder:printcolumn:name="Ready",type=string,JSONPath=`.status.conditions[?(@.type=="Ready")].status`
// +kubebuilder:printcolumn:name="Synced",type=string,JSONPath=`.status.conditions[?(@.type=="Synced")].status`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// AWSAuthCertificate manages an AWS public certificate of an OpenBao AWS auth method at auth/<mountPath>/config/certificate/<name>. Use it for ec2 logins in regions or partitions whose instance identity document signature the plugin cannot verify with its built-in certificates. The AWS auth plugin must be enabled at mountPath (AuthMount).
//
// Example:
//
//	spec:
//	  connectionRef:
//	    name: openbao
//	  mountPath: aws
//	  name: eu-central-1
//	  type: pkcs7
//	  awsPublicCert: |
//	    -----BEGIN CERTIFICATE-----
//	    ...
//	    -----END CERTIFICATE-----
type AWSAuthCertificate struct {
	metav1.TypeMeta `json:",inline"`

	// +optional
	metav1.ObjectMeta `json:"metadata,omitzero"`

	// +required
	Spec AWSAuthCertificateSpec `json:"spec"`

	// +optional
	Status AWSAuthCertificateStatus `json:"status,omitzero"`
}

// GetResourceSpec implements commonv1alpha1.Managed.
func (o *AWSAuthCertificate) GetResourceSpec() *commonv1alpha1.ResourceSpec {
	return &o.Spec.ResourceSpec
}

// GetResourceStatus implements commonv1alpha1.Managed.
func (o *AWSAuthCertificate) GetResourceStatus() *commonv1alpha1.ResourceStatus {
	return &o.Status.ResourceStatus
}

// +kubebuilder:object:root=true

// AWSAuthCertificateList contains a list of AWSAuthCertificate.
type AWSAuthCertificateList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []AWSAuthCertificate `json:"items"`
}

func init() {
	SchemeBuilder.Register(&AWSAuthCertificate{}, &AWSAuthCertificateList{})
}
