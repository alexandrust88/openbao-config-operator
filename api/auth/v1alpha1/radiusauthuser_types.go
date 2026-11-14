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

// Desired state of a RADIUS user, written to auth/<mountPath>/users/<name>.
type RADIUSAuthUserSpec struct {
	commonv1alpha1.ResourceSpec `json:",inline"`

	// Path of the RADIUS auth method, without the auth/ prefix. Defaults to radius. Immutable. Example: radius
	// +kubebuilder:default=radius
	// +kubebuilder:validation:MaxLength=128
	// +kubebuilder:validation:Pattern=`^[A-Za-z0-9_.-]+(/[A-Za-z0-9_.-]+)*$`
	// +optional
	MountPath string `json:"mountPath,omitempty"`

	// RADIUS username in OpenBao. Defaults to metadata.name; set it for names that are not valid Kubernetes names, such as names with upper case letters. Must not contain "/". Immutable. Example: JDoe
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=256
	// +kubebuilder:validation:Pattern=`^[^/]+$`
	// +optional
	Name string `json:"name,omitempty"`

	// Policies attached to tokens of the user, in addition to the default policy. Lower case names. Maps to `policies`. Example: [ops-read]
	// +kubebuilder:validation:MaxItems=256
	// +kubebuilder:validation:items:MinLength=1
	// +kubebuilder:validation:items:MaxLength=128
	// +kubebuilder:validation:items:Pattern=`^[^A-Z,\s]+$`
	// +listType=set
	// +optional
	Policies []string `json:"policies,omitempty"`
}

// Observed state of the user, written by the operator.
type RADIUSAuthUserStatus struct {
	commonv1alpha1.ResourceStatus `json:",inline"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:categories=openbao
// +kubebuilder:printcolumn:name="Ready",type=string,JSONPath=`.status.conditions[?(@.type=="Ready")].status`
// +kubebuilder:printcolumn:name="Synced",type=string,JSONPath=`.status.conditions[?(@.type=="Synced")].status`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// RADIUSAuthUser manages the policies of a RADIUS user at auth/<mountPath>/users/<name>. Needs the RADIUS auth plugin. Use it to give a user that authenticates against the RADIUS server specific OpenBao policies.
//
// Example:
//
//	spec:
//	  connectionRef:
//	    name: openbao
//	  mountPath: radius
//	  name: jdoe
//	  policies: [ops-read]
type RADIUSAuthUser struct {
	metav1.TypeMeta `json:",inline"`

	// +optional
	metav1.ObjectMeta `json:"metadata,omitzero"`

	// +required
	Spec RADIUSAuthUserSpec `json:"spec"`

	// +optional
	Status RADIUSAuthUserStatus `json:"status,omitzero"`
}

// GetResourceSpec implements commonv1alpha1.Managed.
func (o *RADIUSAuthUser) GetResourceSpec() *commonv1alpha1.ResourceSpec { return &o.Spec.ResourceSpec }

// GetResourceStatus implements commonv1alpha1.Managed.
func (o *RADIUSAuthUser) GetResourceStatus() *commonv1alpha1.ResourceStatus {
	return &o.Status.ResourceStatus
}

// +kubebuilder:object:root=true

// RADIUSAuthUserList contains a list of RADIUSAuthUser.
type RADIUSAuthUserList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []RADIUSAuthUser `json:"items"`
}

func init() {
	SchemeBuilder.Register(&RADIUSAuthUser{}, &RADIUSAuthUserList{})
}
