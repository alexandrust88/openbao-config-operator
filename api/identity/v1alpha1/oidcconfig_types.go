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

// spec is the desired identity token configuration at `identity/oidc/config`. There is one per OpenBao namespace. With deletionPolicy Delete, deleting the resource resets the issuer to the server default.
type OIDCConfigSpec struct {
	commonv1alpha1.ResourceSpec `json:",inline"`

	// issuer is the URL used in the `iss` claim of identity tokens (`issuer`): a scheme, a host and an optional port, no path. An empty string restores the default, the OpenBao `api_addr`. Example: `https://openbao.example.com:8200`.
	// +kubebuilder:validation:MaxLength=255
	// +kubebuilder:validation:Pattern=`^(https?://[^/?#\s]+)?$`
	// +required
	Issuer string `json:"issuer"`
}

// status is the observed state of the OIDC configuration.
type OIDCConfigStatus struct {
	commonv1alpha1.ResourceStatus `json:",inline"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:categories=obco;obco-identity
// +kubebuilder:printcolumn:name="Ready",type=string,JSONPath=`.status.conditions[?(@.type=="Ready")].status`
// +kubebuilder:printcolumn:name="Synced",type=string,JSONPath=`.status.conditions[?(@.type=="Synced")].status`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// OIDCConfig sets the identity token (OIDC) configuration of an OpenBao namespace at `identity/oidc/config`, a singleton. Use it to set the issuer of identity tokens when clients reach OpenBao through another address than its `api_addr`.
//
// Example:
//
//	spec:
//	  connectionRef:
//	    name: openbao
//	  issuer: https://openbao.example.com
type OIDCConfig struct {
	metav1.TypeMeta `json:",inline"`

	// +optional
	metav1.ObjectMeta `json:"metadata,omitzero"`

	// +required
	Spec OIDCConfigSpec `json:"spec"`

	// +optional
	Status OIDCConfigStatus `json:"status,omitzero"`
}

// GetResourceSpec implements commonv1alpha1.Managed.
func (o *OIDCConfig) GetResourceSpec() *commonv1alpha1.ResourceSpec { return &o.Spec.ResourceSpec }

// GetResourceStatus implements commonv1alpha1.Managed.
func (o *OIDCConfig) GetResourceStatus() *commonv1alpha1.ResourceStatus {
	return &o.Status.ResourceStatus
}

// +kubebuilder:object:root=true

// OIDCConfigList contains a list of OIDCConfig.
type OIDCConfigList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []OIDCConfig `json:"items"`
}

func init() {
	SchemeBuilder.Register(&OIDCConfig{}, &OIDCConfigList{})
}
