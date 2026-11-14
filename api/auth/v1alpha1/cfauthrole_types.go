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

// Desired state of a Cloud Foundry auth role, written to auth/<mountPath>/roles/<name>. Every bound list that is set must match the login; unset lists do not restrict.
type CFAuthRoleSpec struct {
	commonv1alpha1.ResourceSpec `json:",inline"`

	// Path of the Cloud Foundry auth method, without the auth/ prefix. Defaults to cf. Immutable. Example: cf/prod
	// +kubebuilder:default=cf
	// +kubebuilder:validation:MaxLength=128
	// +kubebuilder:validation:Pattern=`^[A-Za-z0-9_.-]+(/[A-Za-z0-9_.-]+)*$`
	// +optional
	MountPath string `json:"mountPath,omitempty"`

	// Name of the role in OpenBao, used in the login request. Defaults to metadata.name. Immutable. Example: payments-app
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=128
	// +kubebuilder:validation:Pattern=`^[A-Za-z0-9_]([A-Za-z0-9_.-]*[A-Za-z0-9_])?$`
	// +optional
	Name string `json:"name,omitempty"`

	// Cloud Foundry application GUIDs allowed to log in. Unset allows any application. Maps to `bound_application_ids`. Example: [2c6dbe0c-7d9d-4c2c-a3b5-0e7b0c4c4d1e]
	// +kubebuilder:validation:MaxItems=64
	// +kubebuilder:validation:items:MinLength=1
	// +kubebuilder:validation:items:MaxLength=512
	// +listType=set
	// +optional
	BoundApplicationIDs []string `json:"boundApplicationIDs,omitempty"`

	// Cloud Foundry space GUIDs whose applications may log in. Unset allows any space. Maps to `bound_space_ids`. Example: [3d2eba6b-ef19-44d5-91dd-1975b0db5cc9]
	// +kubebuilder:validation:MaxItems=64
	// +kubebuilder:validation:items:MinLength=1
	// +kubebuilder:validation:items:MaxLength=512
	// +listType=set
	// +optional
	BoundSpaceIDs []string `json:"boundSpaceIDs,omitempty"`

	// Cloud Foundry organization GUIDs whose applications may log in. Unset allows any organization. Maps to `bound_organization_ids`. Example: [34a878d0-c2f9-4521-ba73-a9f664e82c7b]
	// +kubebuilder:validation:MaxItems=64
	// +kubebuilder:validation:items:MinLength=1
	// +kubebuilder:validation:items:MaxLength=512
	// +listType=set
	// +optional
	BoundOrganizationIDs []string `json:"boundOrganizationIDs,omitempty"`

	// Application instance GUIDs allowed to log in. Unset allows any instance. Maps to `bound_instance_ids`. Example: [8a3c9f2e-1b4d-4e5f-9a6b-7c8d9e0f1a2b]
	// +kubebuilder:validation:MaxItems=64
	// +kubebuilder:validation:items:MinLength=1
	// +kubebuilder:validation:items:MaxLength=512
	// +listType=set
	// +optional
	BoundInstanceIDs []string `json:"boundInstanceIDs,omitempty"`

	// When true, OpenBao skips the check that the login comes from the IP address in the instance identity certificate; use it when logins pass through a proxy. Defaults to false. Maps to `disable_ip_matching`. Example: true
	// +optional
	DisableIPMatching *bool `json:"disableIPMatching,omitempty"`

	TokenFields `json:",inline"`
}

// Observed state of the role, written by the operator.
type CFAuthRoleStatus struct {
	commonv1alpha1.ResourceStatus `json:",inline"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:categories=openbao
// +kubebuilder:printcolumn:name="Ready",type=string,JSONPath=`.status.conditions[?(@.type=="Ready")].status`
// +kubebuilder:printcolumn:name="Synced",type=string,JSONPath=`.status.conditions[?(@.type=="Synced")].status`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// CFAuthRole manages a role of an OpenBao Cloud Foundry auth method at auth/<mountPath>/roles/<name>. Use it to let Cloud Foundry application instances log in with their instance identity certificate and receive tokens with the listed policies. Needs the cf auth plugin registered and enabled at auth/<mountPath>.
//
// Example:
//
//	spec:
//	  connectionRef:
//	    name: openbao
//	  boundSpaceIDs: [3d2eba6b-ef19-44d5-91dd-1975b0db5cc9]
//	  boundApplicationIDs: [2c6dbe0c-7d9d-4c2c-a3b5-0e7b0c4c4d1e]
//	  tokenPolicies: [app-read]
//	  tokenTTL: 1h
type CFAuthRole struct {
	metav1.TypeMeta `json:",inline"`

	// +optional
	metav1.ObjectMeta `json:"metadata,omitzero"`

	// +required
	Spec CFAuthRoleSpec `json:"spec"`

	// +optional
	Status CFAuthRoleStatus `json:"status,omitzero"`
}

// GetResourceSpec implements commonv1alpha1.Managed.
func (o *CFAuthRole) GetResourceSpec() *commonv1alpha1.ResourceSpec { return &o.Spec.ResourceSpec }

// GetResourceStatus implements commonv1alpha1.Managed.
func (o *CFAuthRole) GetResourceStatus() *commonv1alpha1.ResourceStatus {
	return &o.Status.ResourceStatus
}

// +kubebuilder:object:root=true

// CFAuthRoleList contains a list of CFAuthRole.
type CFAuthRoleList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []CFAuthRole `json:"items"`
}

func init() {
	SchemeBuilder.Register(&CFAuthRole{}, &CFAuthRoleList{})
}
