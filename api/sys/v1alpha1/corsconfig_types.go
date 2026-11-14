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

// +kubebuilder:validation:XValidation:rule="!has(self.__namespace__) || self.__namespace__ == ''",message="CORSConfig exists only in the root namespace: leave namespace empty"
// +kubebuilder:validation:XValidation:rule="!('*' in self.allowedOrigins) || size(self.allowedOrigins) == 1",message="'*' must be the only entry of allowedOrigins"

// CORSConfigSpec enables cross-origin requests to OpenBao at sys/config/cors. The setting is server wide and exists only in the root namespace. Deleting the resource with deletionPolicy Delete disables CORS.
type CORSConfigSpec struct {
	commonv1alpha1.ResourceSpec `json:",inline"`

	// Origins allowed to make cross-origin requests, or the single entry "*" to allow every origin. Maps to `allowed_origins`; order does not matter. Example: ["https://ui.example.com"]
	// +kubebuilder:validation:MinItems=1
	// +kubebuilder:validation:MaxItems=64
	// +kubebuilder:validation:items:MinLength=1
	// +kubebuilder:validation:items:MaxLength=512
	// +listType=set
	// +required
	AllowedOrigins []string `json:"allowedOrigins"`

	// Extra request headers allowed on cross-origin requests. Maps to `allowed_headers`; order does not matter. OpenBao always allows its standard headers (Content-Type, Authorization, X-Vault-Token and the other X-Vault-* headers) in addition. Example: ["X-Request-Id"]
	// +kubebuilder:validation:MaxItems=64
	// +kubebuilder:validation:items:MinLength=1
	// +kubebuilder:validation:items:MaxLength=256
	// +listType=set
	// +optional
	AllowedHeaders []string `json:"allowedHeaders,omitempty"`

	// Lets browsers send credentials, such as cookies or Kerberos authentication, with cross-origin requests. Maps to `allow_credentials`. Default: false. Example: true
	// +optional
	AllowCredentials bool `json:"allowCredentials,omitempty"`
}

// CORSConfigStatus is the observed state of a CORSConfig.
type CORSConfigStatus struct {
	commonv1alpha1.ResourceStatus `json:",inline"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:categories=openbao
// +kubebuilder:printcolumn:name="Ready",type=string,JSONPath=`.status.conditions[?(@.type=="Ready")].status`
// +kubebuilder:printcolumn:name="Synced",type=string,JSONPath=`.status.conditions[?(@.type=="Synced")].status`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// CORSConfig enables cross-origin (CORS) requests to OpenBao at sys/config/cors, a server-wide singleton in the root namespace. Use it when a browser application on another origin calls the OpenBao API. Create only one per server. The default deletionPolicy Orphan keeps the setting; Delete disables CORS.
//
// Example:
//
//	spec:
//	  connectionRef:
//	    kind: ClusterOpenBaoConnection
//	    name: openbao
//	  allowedOrigins:
//	    - https://ui.example.com
//	  allowedHeaders:
//	    - X-Request-Id
type CORSConfig struct {
	metav1.TypeMeta `json:",inline"`

	// +optional
	metav1.ObjectMeta `json:"metadata,omitzero"`

	// Desired CORS settings of the server.
	// +required
	Spec CORSConfigSpec `json:"spec"`

	// Observed state reported by the operator.
	// +optional
	Status CORSConfigStatus `json:"status,omitzero"`
}

// GetResourceSpec implements commonv1alpha1.Managed.
func (c *CORSConfig) GetResourceSpec() *commonv1alpha1.ResourceSpec { return &c.Spec.ResourceSpec }

// GetResourceStatus implements commonv1alpha1.Managed.
func (c *CORSConfig) GetResourceStatus() *commonv1alpha1.ResourceStatus {
	return &c.Status.ResourceStatus
}

// +kubebuilder:object:root=true

// CORSConfigList contains a list of CORSConfig.
type CORSConfigList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []CORSConfig `json:"items"`
}

func init() {
	SchemeBuilder.Register(&CORSConfig{}, &CORSConfigList{})
}
