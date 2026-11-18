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
// +kubebuilder:validation:XValidation:rule="has(self.crl) != has(self.url)",message="set exactly one of crl or url"

// Desired state of a certificate revocation list on a cert auth method, written to auth/<mountPath>/crls/<name>.
type CertAuthCRLSpec struct {
	commonv1alpha1.ResourceSpec `json:",inline"`

	// Path of the cert auth method, without the auth/ prefix. Defaults to cert. Immutable. Example: cert
	// +kubebuilder:default=cert
	// +kubebuilder:validation:MaxLength=128
	// +kubebuilder:validation:Pattern=`^[A-Za-z0-9_.-]+(/[A-Za-z0-9_.-]+)*$`
	// +optional
	MountPath string `json:"mountPath,omitempty"`

	// Name of the CRL in OpenBao. Defaults to metadata.name; stored in lower case. Immutable. Example: team-ca
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=128
	// +kubebuilder:validation:Pattern=`^[a-z0-9_]([a-z0-9_.-]*[a-z0-9_])?$`
	// +optional
	Name string `json:"name,omitempty"`

	// PEM-encoded CRL to serve from OpenBao. Maps to `crl`. Mutually exclusive with url. Example: "-----BEGIN X509 CRL-----\n...\n-----END X509 CRL-----"
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=262144
	// +optional
	CRL string `json:"crl,omitempty"`

	// URL of a CRL distribution point OpenBao fetches. Maps to `url`. Mutually exclusive with crl. Example: https://ca.example.com/ca.crl
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=2048
	// +optional
	URL string `json:"url,omitempty"`
}

// Observed state of the CRL, written by the operator.
type CertAuthCRLStatus struct {
	commonv1alpha1.ResourceStatus `json:",inline"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:categories=obco;obco-auth
// +kubebuilder:printcolumn:name="Ready",type=string,JSONPath=`.status.conditions[?(@.type=="Ready")].status`
// +kubebuilder:printcolumn:name="Synced",type=string,JSONPath=`.status.conditions[?(@.type=="Synced")].status`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// CertAuthCRL installs a certificate revocation list on a TLS certificate auth method at auth/<mountPath>/crls/<name>, either as a fetched URL or as embedded PEM data.
//
// Example:
//
//	spec:
//	  connectionRef:
//	    name: openbao
//	  url: https://example.com/ca.crl
type CertAuthCRL struct {
	metav1.TypeMeta `json:",inline"`

	// +optional
	metav1.ObjectMeta `json:"metadata,omitzero"`

	// +required
	Spec CertAuthCRLSpec `json:"spec"`

	// +optional
	Status CertAuthCRLStatus `json:"status,omitzero"`
}

// GetResourceSpec implements commonv1alpha1.Managed.
func (c *CertAuthCRL) GetResourceSpec() *commonv1alpha1.ResourceSpec { return &c.Spec.ResourceSpec }

// GetResourceStatus implements commonv1alpha1.Managed.
func (c *CertAuthCRL) GetResourceStatus() *commonv1alpha1.ResourceStatus {
	return &c.Status.ResourceStatus
}

// +kubebuilder:object:root=true

// CertAuthCRLList contains a list of CertAuthCRL.
type CertAuthCRLList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []CertAuthCRL `json:"items"`
}

func init() {
	SchemeBuilder.Register(&CertAuthCRL{}, &CertAuthCRLList{})
}
