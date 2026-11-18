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

// +kubebuilder:validation:XValidation:rule="has(self.path) == has(oldSelf.path) && (!has(self.path) || self.path == oldSelf.path)",message="path is immutable"
// +kubebuilder:validation:XValidation:rule="self.type == oldSelf.type",message="type is immutable"
// +kubebuilder:validation:XValidation:rule="!has(oldSelf.options) || !('version' in oldSelf.options) || oldSelf.options['version'] != '2' || (has(self.options) && 'version' in self.options && self.options['version'] == '2')",message="a KV version 2 mount cannot be downgraded to version 1"
// +kubebuilder:validation:XValidation:rule="!(self.type in ['kv-v1', 'kv-v2']) || !has(self.options) || !('version' in self.options)",message="set the KV version through type kv-v1/kv-v2 or options.version, not both"

// SecretsMountSpec defines a secrets engine enabled at sys/mounts/<path>.
type SecretsMountSpec struct {
	commonv1alpha1.ResourceSpec `json:",inline"`

	// Path the secrets engine is enabled at, without slashes at either end. Default: metadata.name. Immutable: it cannot be added, changed or removed after creation. Example: secret
	// +kubebuilder:validation:MaxLength=256
	// +kubebuilder:validation:Pattern=`^[A-Za-z0-9_.-]+(/[A-Za-z0-9_.-]+)*$`
	// +optional
	Path string `json:"path,omitempty"`

	// Secrets engine type: a built-in engine such as kv, kv-v2, pki, transit or database, or the name of a registered plugin. Maps to `type`; kv-v1 and kv-v2 are written as type kv with options version 1 or 2, so do not also set options.version. Immutable; a KV version 2 mount cannot go back to version 1. Example: kv-v2
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=128
	// +required
	Type string `json:"type"`

	MountFields `json:",inline"`
}

// SecretsMountStatus is the observed state of a SecretsMount. status.id holds the mount accessor.
type SecretsMountStatus struct {
	commonv1alpha1.ResourceStatus `json:",inline"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:shortName=baosm,categories=obco;obco-sys
// +kubebuilder:printcolumn:name="Type",type=string,JSONPath=`.spec.type`
// +kubebuilder:printcolumn:name="Ready",type=string,JSONPath=`.status.conditions[?(@.type=="Ready")].status`
// +kubebuilder:printcolumn:name="Synced",type=string,JSONPath=`.status.conditions[?(@.type=="Synced")].status`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// SecretsMount enables and tunes an OpenBao secrets engine at sys/mounts/<path>, such as a KV, PKI, transit or database engine. Create it before the secrets kinds that configure the engine. status.id holds the mount accessor. The default deletionPolicy Orphan keeps the mount; Delete disables it and destroys all its data.
//
// Example:
//
//	spec:
//	  connectionRef:
//	    name: openbao
//	  type: kv-v2
//	  path: secret
//	  description: Team A application secrets
//	  maxLeaseTTL: 24h
type SecretsMount struct {
	metav1.TypeMeta `json:",inline"`

	// +optional
	metav1.ObjectMeta `json:"metadata,omitzero"`

	// Desired state of the secrets engine mount.
	// +required
	Spec SecretsMountSpec `json:"spec"`

	// Observed state reported by the operator; id holds the mount accessor.
	// +optional
	Status SecretsMountStatus `json:"status,omitzero"`
}

// GetResourceSpec implements commonv1alpha1.Managed.
func (m *SecretsMount) GetResourceSpec() *commonv1alpha1.ResourceSpec { return &m.Spec.ResourceSpec }

// GetResourceStatus implements commonv1alpha1.Managed.
func (m *SecretsMount) GetResourceStatus() *commonv1alpha1.ResourceStatus {
	return &m.Status.ResourceStatus
}

// +kubebuilder:object:root=true

// SecretsMountList contains a list of SecretsMount.
type SecretsMountList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []SecretsMount `json:"items"`
}

func init() {
	SchemeBuilder.Register(&SecretsMount{}, &SecretsMountList{})
}
