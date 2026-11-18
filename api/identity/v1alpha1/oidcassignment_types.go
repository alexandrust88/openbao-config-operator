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

// +kubebuilder:validation:XValidation:rule="has(self.name) == has(oldSelf.name) && (!has(self.name) || self.name == oldSelf.name)",message="name is immutable"
// +kubebuilder:validation:XValidation:rule="!has(self.name) || self.name != 'allow_all'",message="the assignment allow_all is built in and cannot be changed"

// spec is the desired OIDC assignment at `identity/oidc/assignment/<name>`: the entities and groups allowed to authenticate through the OIDC clients that cite it. The lists are written as a whole; members removed from the spec are removed in OpenBao.
type OIDCAssignmentSpec struct {
	commonv1alpha1.ResourceSpec `json:",inline"`

	// name is the assignment name in OpenBao. Defaults to metadata.name. Letters, digits, `_`, `.`, `@` and `-`; `allow_all` is built in and cannot be managed. Immutable. Example: `billing-users`.
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=128
	// +kubebuilder:validation:Pattern=`^\w([\w.@-]*\w)?$`
	// +optional
	Name string `json:"name,omitempty"`

	// entities are the entities in the assignment (`entity_ids`). Up to 256. Example: `[{entityRef: {name: alice}}]`.
	// +kubebuilder:validation:MaxItems=256
	// +listType=atomic
	// +optional
	Entities []EntityMatch `json:"entities,omitempty"`

	// groups are identity groups in the assignment; their member entities are included (`group_ids`). Up to 256. Example: `[{groupName: billing-admins}]`.
	// +kubebuilder:validation:MaxItems=256
	// +listType=atomic
	// +optional
	Groups []GroupMatch `json:"groups,omitempty"`
}

// status is the observed state of the OIDC assignment.
type OIDCAssignmentStatus struct {
	commonv1alpha1.ResourceStatus `json:",inline"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:categories=obco;obco-identity
// +kubebuilder:printcolumn:name="Ready",type=string,JSONPath=`.status.conditions[?(@.type=="Ready")].status`
// +kubebuilder:printcolumn:name="Synced",type=string,JSONPath=`.status.conditions[?(@.type=="Synced")].status`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// OIDCAssignment manages an assignment of the OpenBao OIDC provider at `identity/oidc/assignment/<name>`: the entities and groups allowed to log in to the OIDCClient objects that list it in assignments.
//
// Example:
//
//	spec:
//	  connectionRef:
//	    name: openbao
//	  name: billing-users
//	  groups:
//	    - groupRef: {name: billing-admins}
type OIDCAssignment struct {
	metav1.TypeMeta `json:",inline"`

	// +optional
	metav1.ObjectMeta `json:"metadata,omitzero"`

	// +required
	Spec OIDCAssignmentSpec `json:"spec"`

	// +optional
	Status OIDCAssignmentStatus `json:"status,omitzero"`
}

// GetResourceSpec implements commonv1alpha1.Managed.
func (o *OIDCAssignment) GetResourceSpec() *commonv1alpha1.ResourceSpec { return &o.Spec.ResourceSpec }

// GetResourceStatus implements commonv1alpha1.Managed.
func (o *OIDCAssignment) GetResourceStatus() *commonv1alpha1.ResourceStatus {
	return &o.Status.ResourceStatus
}

// +kubebuilder:object:root=true

// OIDCAssignmentList contains a list of OIDCAssignment.
type OIDCAssignmentList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []OIDCAssignment `json:"items"`
}

func init() {
	SchemeBuilder.Register(&OIDCAssignment{}, &OIDCAssignmentList{})
}
