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

// +kubebuilder:validation:XValidation:rule="!has(self.__namespace__) || self.__namespace__ == ''",message="UIHeader exists only in the root namespace: leave namespace empty"
// +kubebuilder:validation:XValidation:rule="has(self.name) == has(oldSelf.name) && (!has(self.name) || self.name == oldSelf.name)",message="name is immutable"

// UIHeaderSpec defines a custom HTTP response header that OpenBao returns with its web UI, at sys/config/ui/headers/<name>. UI headers exist only in the root namespace.
type UIHeaderSpec struct {
	commonv1alpha1.ResourceSpec `json:",inline"`

	// Name of the HTTP header, the <name> in sys/config/ui/headers/<name>; case-insensitive, and X-Vault-* headers cannot be set. Default: metadata.name. Immutable. Example: Content-Security-Policy
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=128
	// +kubebuilder:validation:Pattern=`^[A-Za-z0-9]([A-Za-z0-9_.-]*[A-Za-z0-9])?$`
	// +optional
	Name string `json:"name,omitempty"`

	// Values of the header, in order. Maps to `values`. Drift detection compares the first value only, because OpenBao returns only the first value of a header by default. Example: ["default-src 'self'"]
	// +kubebuilder:validation:MinItems=1
	// +kubebuilder:validation:MaxItems=16
	// +kubebuilder:validation:items:MinLength=1
	// +kubebuilder:validation:items:MaxLength=4096
	// +listType=atomic
	// +required
	Values []string `json:"values"`
}

// UIHeaderStatus is the observed state of a UIHeader.
type UIHeaderStatus struct {
	commonv1alpha1.ResourceStatus `json:",inline"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:categories=openbao
// +kubebuilder:validation:XValidation:rule="!(has(self.spec.name) ? self.spec.name : self.metadata.name).lowerAscii().startsWith('x-vault-')",message="X-Vault-* headers cannot be set"
// +kubebuilder:printcolumn:name="Ready",type=string,JSONPath=`.status.conditions[?(@.type=="Ready")].status`
// +kubebuilder:printcolumn:name="Synced",type=string,JSONPath=`.status.conditions[?(@.type=="Synced")].status`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// UIHeader manages a custom HTTP response header that OpenBao returns with its web UI, at sys/config/ui/headers/<name>, for example a Content-Security-Policy. Root namespace only. deletionPolicy defaults to Orphan; Delete removes the header.
//
// Example:
//
//	spec:
//	  connectionRef:
//	    name: openbao
//	  name: Content-Security-Policy
//	  values:
//	    - default-src 'self'
type UIHeader struct {
	metav1.TypeMeta `json:",inline"`

	// +optional
	metav1.ObjectMeta `json:"metadata,omitzero"`

	// Desired state of the OpenBao UI header.
	// +required
	Spec UIHeaderSpec `json:"spec"`

	// Observed state reported by the operator: conditions, OpenBao path and ownership.
	// +optional
	Status UIHeaderStatus `json:"status,omitzero"`
}

// GetResourceSpec implements commonv1alpha1.Managed.
func (h *UIHeader) GetResourceSpec() *commonv1alpha1.ResourceSpec { return &h.Spec.ResourceSpec }

// GetResourceStatus implements commonv1alpha1.Managed.
func (h *UIHeader) GetResourceStatus() *commonv1alpha1.ResourceStatus {
	return &h.Status.ResourceStatus
}

// +kubebuilder:object:root=true

// UIHeaderList contains a list of UIHeader.
type UIHeaderList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []UIHeader `json:"items"`
}

func init() {
	SchemeBuilder.Register(&UIHeader{}, &UIHeaderList{})
}
