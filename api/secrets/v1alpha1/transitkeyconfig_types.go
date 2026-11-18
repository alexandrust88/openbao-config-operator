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

// +kubebuilder:validation:XValidation:rule="self.mountPath == oldSelf.mountPath",message="mountPath is immutable"
// +kubebuilder:validation:XValidation:rule="!has(self.deletionPolicy) || self.deletionPolicy == 'Orphan'",message="the transit key configuration cannot be deleted; deletionPolicy must be Orphan"

// spec is the desired configuration of a Transit secrets engine at `<mountPath>/config/keys`. An omitted setting is reset to the server default. deletionPolicy must be Orphan.
type TransitKeyConfigSpec struct {
	commonv1alpha1.ResourceSpec `json:",inline"`

	// mountPath is the path of the Transit secrets engine mount, without leading or trailing slashes. Immutable. Example: `transit`.
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=128
	// +kubebuilder:validation:Pattern=`^[A-Za-z0-9_.-]+(/[A-Za-z0-9_.-]+)*$`
	// +required
	MountPath string `json:"mountPath"`

	// disableUpsert forbids creating encryption keys that do not already exist (`disable_upsert`). Defaults to false.
	// +optional
	DisableUpsert *bool `json:"disableUpsert,omitempty"`
}

// status is the observed state of the Transit key configuration.
type TransitKeyConfigStatus struct {
	commonv1alpha1.ResourceStatus `json:",inline"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:categories=obco;obco-secrets
// +kubebuilder:printcolumn:name="Ready",type=string,JSONPath=`.status.conditions[?(@.type=="Ready")].status`
// +kubebuilder:printcolumn:name="Synced",type=string,JSONPath=`.status.conditions[?(@.type=="Synced")].status`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// TransitKeyConfig sets the mount-wide key configuration of a Transit secrets engine at `<mount>/config/keys`. A singleton per mount; it is never deleted.
//
// Example:
//
//	spec:
//	  connectionRef:
//	    name: openbao
//	  mountPath: transit
//	  disableUpsert: false
type TransitKeyConfig struct {
	metav1.TypeMeta `json:",inline"`

	// +optional
	metav1.ObjectMeta `json:"metadata,omitzero"`

	// +required
	Spec TransitKeyConfigSpec `json:"spec"`

	// +optional
	Status TransitKeyConfigStatus `json:"status,omitzero"`
}

// GetResourceSpec implements commonv1alpha1.Managed.
func (c *TransitKeyConfig) GetResourceSpec() *commonv1alpha1.ResourceSpec {
	return &c.Spec.ResourceSpec
}

// GetResourceStatus implements commonv1alpha1.Managed.
func (c *TransitKeyConfig) GetResourceStatus() *commonv1alpha1.ResourceStatus {
	return &c.Status.ResourceStatus
}

// +kubebuilder:object:root=true

// TransitKeyConfigList contains a list of TransitKeyConfig.
type TransitKeyConfigList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []TransitKeyConfig `json:"items"`
}

func init() {
	SchemeBuilder.Register(&TransitKeyConfig{}, &TransitKeyConfigList{})
}
