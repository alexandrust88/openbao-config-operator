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
// +kubebuilder:validation:XValidation:rule="!has(self.deletionPolicy) || self.deletionPolicy == 'Orphan'",message="the PKI keys configuration cannot be deleted; deletionPolicy must be Orphan"
// +kubebuilder:validation:XValidation:rule="self.defaultKey != 'default'",message="defaultKey cannot be the reserved name default"

// spec is the desired default signing key configuration at `<mountPath>/config/keys`. The OpenBao path has no delete; deletionPolicy must be Orphan.
type PKIKeyConfigSpec struct {
	commonv1alpha1.ResourceSpec `json:",inline"`

	// mountPath is the path of the PKI secrets engine mount. Defaults to `pki`. Immutable. Example: `pki-int`.
	// +kubebuilder:default=pki
	// +kubebuilder:validation:MaxLength=128
	// +kubebuilder:validation:Pattern=`^[A-Za-z0-9_.-]+(/[A-Za-z0-9_.-]+)*$`
	// +optional
	MountPath string `json:"mountPath,omitempty"`

	// defaultKey is the default signing key for the mount (`default`), by name or id. The name `default` is reserved. Example: `example-key`.
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=128
	// +kubebuilder:validation:Pattern=`^[A-Za-z0-9_.:-]+$`
	// +required
	DefaultKey string `json:"defaultKey"`
}

// status is the observed state of the keys configuration.
type PKIKeyConfigStatus struct {
	commonv1alpha1.ResourceStatus `json:",inline"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:categories=openbao
// +kubebuilder:printcolumn:name="Ready",type=string,JSONPath=`.status.conditions[?(@.type=="Ready")].status`
// +kubebuilder:printcolumn:name="Synced",type=string,JSONPath=`.status.conditions[?(@.type=="Synced")].status`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// PKIKeyConfig sets the default signing key of a PKI secrets engine at `<mountPath>/config/keys`. OpenBao only.
//
// Example:
//
//	spec:
//	  connectionRef:
//	    name: openbao
//	  mountPath: pki
//	  defaultKey: example-key
type PKIKeyConfig struct {
	metav1.TypeMeta `json:",inline"`

	// +optional
	metav1.ObjectMeta `json:"metadata,omitzero"`

	// +required
	Spec PKIKeyConfigSpec `json:"spec"`

	// +optional
	Status PKIKeyConfigStatus `json:"status,omitzero"`
}

// GetResourceSpec implements commonv1alpha1.Managed.
func (k *PKIKeyConfig) GetResourceSpec() *commonv1alpha1.ResourceSpec { return &k.Spec.ResourceSpec }

// GetResourceStatus implements commonv1alpha1.Managed.
func (k *PKIKeyConfig) GetResourceStatus() *commonv1alpha1.ResourceStatus {
	return &k.Status.ResourceStatus
}

// +kubebuilder:object:root=true

// PKIKeyConfigList contains a list of PKIKeyConfig.
type PKIKeyConfigList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []PKIKeyConfig `json:"items"`
}

func init() {
	SchemeBuilder.Register(&PKIKeyConfig{}, &PKIKeyConfigList{})
}
