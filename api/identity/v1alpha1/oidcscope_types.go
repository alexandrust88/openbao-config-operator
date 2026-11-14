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

// spec is the desired OIDC provider scope at `identity/oidc/scope/<name>`.
type OIDCScopeSpec struct {
	commonv1alpha1.ResourceSpec `json:",inline"`

	// name is the scope name in OpenBao. Defaults to metadata.name. Letters, digits, `_`, `.`, `@` and `-`; `openid` is reserved. Immutable. Example: `groups`.
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=128
	// +kubebuilder:validation:Pattern=`^\w([\w.@-]*\w)?$`
	// +optional
	Name string `json:"name,omitempty"`

	// template is a JSON template of the claims the scope adds, using OpenBao identity templating (`template`). JSON text, not base64. Up to 64 KiB. Example: `{"groups": {{identity.entity.groups.names}}}`.
	// +kubebuilder:validation:MaxLength=65536
	// +kubebuilder:validation:Pattern=`^\s*\{`
	// +optional
	Template string `json:"template,omitempty"`

	// description is a free-text description of the scope (`description`). Up to 1024 characters. Example: `Group membership`.
	// +kubebuilder:validation:MaxLength=1024
	// +optional
	Description string `json:"description,omitempty"`
}

// status is the observed state of the OIDC scope.
type OIDCScopeStatus struct {
	commonv1alpha1.ResourceStatus `json:",inline"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:categories=openbao
// +kubebuilder:printcolumn:name="Ready",type=string,JSONPath=`.status.conditions[?(@.type=="Ready")].status`
// +kubebuilder:printcolumn:name="Synced",type=string,JSONPath=`.status.conditions[?(@.type=="Synced")].status`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`
// +kubebuilder:validation:XValidation:rule="has(self.spec.name) ? self.spec.name != 'openid' : self.metadata.name != 'openid'",message="the scope name openid is reserved"

// OIDCScope manages a scope of the OpenBao OIDC provider at `identity/oidc/scope/<name>`: a template of claims added to ID tokens when a client requests the scope. List it in an OIDCProvider's scopesSupported.
//
// Example:
//
//	spec:
//	  connectionRef:
//	    name: openbao
//	  name: groups
//	  template: '{"groups": {{identity.entity.groups.names}}}'
type OIDCScope struct {
	metav1.TypeMeta `json:",inline"`

	// +optional
	metav1.ObjectMeta `json:"metadata,omitzero"`

	// +required
	Spec OIDCScopeSpec `json:"spec"`

	// +optional
	Status OIDCScopeStatus `json:"status,omitzero"`
}

// GetResourceSpec implements commonv1alpha1.Managed.
func (o *OIDCScope) GetResourceSpec() *commonv1alpha1.ResourceSpec { return &o.Spec.ResourceSpec }

// GetResourceStatus implements commonv1alpha1.Managed.
func (o *OIDCScope) GetResourceStatus() *commonv1alpha1.ResourceStatus {
	return &o.Status.ResourceStatus
}

// +kubebuilder:object:root=true

// OIDCScopeList contains a list of OIDCScope.
type OIDCScopeList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []OIDCScope `json:"items"`
}

func init() {
	SchemeBuilder.Register(&OIDCScope{}, &OIDCScopeList{})
}
