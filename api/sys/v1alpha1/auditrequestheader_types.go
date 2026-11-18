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

// +kubebuilder:validation:XValidation:rule="!has(self.openbaoNamespace) || self.openbaoNamespace == ''",message="AuditRequestHeader exists only in the root namespace: leave openbaoNamespace empty"
// +kubebuilder:validation:XValidation:rule="has(self.name) == has(oldSelf.name) && (!has(self.name) || self.name == oldSelf.name)",message="name is immutable"

// AuditRequestHeaderSpec makes the audit devices record an HTTP request header, at sys/config/auditing/request-headers/<name>. Audited headers exist only in the root namespace.
type AuditRequestHeaderSpec struct {
	commonv1alpha1.ResourceSpec `json:",inline"`

	// Name of the HTTP request header to record; case-insensitive. Default: metadata.name. Immutable: it cannot be added, changed or removed after creation. Example: X-Forwarded-For
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=128
	// +kubebuilder:validation:Pattern=`^[A-Za-z0-9]([A-Za-z0-9_.-]*[A-Za-z0-9])?$`
	// +optional
	Name string `json:"name,omitempty"`

	// Whether the header value is HMAC-hashed in the audit log, like other sensitive values, instead of logged in plain text. Maps to `hmac`. Default: false. Example: true
	// +optional
	HMAC bool `json:"hmac,omitempty"`
}

// AuditRequestHeaderStatus is the observed state of an AuditRequestHeader.
type AuditRequestHeaderStatus struct {
	commonv1alpha1.ResourceStatus `json:",inline"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:categories=obco;obco-sys
// +kubebuilder:printcolumn:name="Ready",type=string,JSONPath=`.status.conditions[?(@.type=="Ready")].status`
// +kubebuilder:printcolumn:name="Synced",type=string,JSONPath=`.status.conditions[?(@.type=="Synced")].status`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// AuditRequestHeader makes the OpenBao audit devices record an HTTP request header, at sys/config/auditing/request-headers/<name>, in the root namespace only. Use it to keep client headers such as X-Forwarded-For in the audit log. The default deletionPolicy Orphan keeps the setting; Delete stops recording.
//
// Example:
//
//	spec:
//	  connectionRef:
//	    kind: ClusterBaoConnection
//	    name: openbao
//	  name: X-Forwarded-For
//	  hmac: false
type AuditRequestHeader struct {
	metav1.TypeMeta `json:",inline"`

	// +optional
	metav1.ObjectMeta `json:"metadata,omitzero"`

	// Desired state of the audited request header.
	// +required
	Spec AuditRequestHeaderSpec `json:"spec"`

	// Observed state reported by the operator.
	// +optional
	Status AuditRequestHeaderStatus `json:"status,omitzero"`
}

// GetResourceSpec implements commonv1alpha1.Managed.
func (h *AuditRequestHeader) GetResourceSpec() *commonv1alpha1.ResourceSpec {
	return &h.Spec.ResourceSpec
}

// GetResourceStatus implements commonv1alpha1.Managed.
func (h *AuditRequestHeader) GetResourceStatus() *commonv1alpha1.ResourceStatus {
	return &h.Status.ResourceStatus
}

// +kubebuilder:object:root=true

// AuditRequestHeaderList contains a list of AuditRequestHeader.
type AuditRequestHeaderList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []AuditRequestHeader `json:"items"`
}

func init() {
	SchemeBuilder.Register(&AuditRequestHeader{}, &AuditRequestHeaderList{})
}
