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

// BaoNamespaceSpec defines an OpenBao namespace at sys/namespaces/<name>. spec.openbaoNamespace selects the parent namespace, so a nested namespace is created from its parent.
type BaoNamespaceSpec struct {
	commonv1alpha1.ResourceSpec `json:",inline"`

	// Name of the namespace in OpenBao: a single path segment without slashes or whitespace, other than the reserved names ".", "..", root, sys, audit, auth, cubbyhole and identity. It is created under the parent namespace given by openbaoNamespace. Default: metadata.name. Immutable: it cannot be added, changed or removed after creation. Example: team-a
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=256
	// +kubebuilder:validation:Pattern=`^([^/\s.]|[^/\s.][^/\s]|\.[^/\s.]|[^/\s]{3}|[^/\sr][^/\s]{3}|r[^/\so][^/\s]{2}|ro[^/\so][^/\s]|roo[^/\st]|[^/\s]{5,})$`
	// +optional
	Name string `json:"name,omitempty"`

	// User-defined key/value pairs stored with the namespace. Maps to `custom_metadata`; the map replaces the stored set, so keys missing here are removed. At most 61 entries; the key prefix `openbaoconfig-` holds the operator's ownership marker and is reserved. Example: {"owner": "team-a"}
	// +kubebuilder:validation:MaxProperties=61
	// +kubebuilder:validation:XValidation:rule="self.all(k, size(k) <= 128 && !k.startsWith('openbaoconfig-') && size(self[k]) <= 512)",message="customMetadata keys are at most 128 characters without the reserved prefix openbaoconfig-, values at most 512 characters"
	// +optional
	CustomMetadata map[string]string `json:"customMetadata,omitempty"`
}

// BaoNamespaceStatus is the observed state of a BaoNamespace. status.id holds the namespace ID.
type BaoNamespaceStatus struct {
	commonv1alpha1.ResourceStatus `json:",inline"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:shortName=baons,categories=obco;obco-sys
// +kubebuilder:printcolumn:name="Ready",type=string,JSONPath=`.status.conditions[?(@.type=="Ready")].status`
// +kubebuilder:printcolumn:name="Synced",type=string,JSONPath=`.status.conditions[?(@.type=="Synced")].status`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`
// +kubebuilder:validation:XValidation:rule="has(self.spec.name) ? !(self.spec.name in ['.', '..', 'root', 'sys', 'audit', 'auth', 'cubbyhole', 'identity']) : !(self.metadata.name in ['root', 'sys', 'audit', 'auth', 'cubbyhole', 'identity'])",message="the namespace name is reserved in OpenBao"

// BaoNamespace creates an OpenBao namespace at sys/namespaces/<name> inside the parent namespace selected by openbaoNamespace (the connection namespace when unset). Use it to give a team or tenant its own isolated policies, mounts and identities. status.id holds the namespace ID. The default deletionPolicy Orphan keeps the namespace; Delete removes it with everything inside, and waits while it still has child namespaces.
//
// Example:
//
//	spec:
//	  connectionRef:
//	    name: openbao
//	  name: team-a
//	  customMetadata:
//	    owner: team-a
type BaoNamespace struct {
	metav1.TypeMeta `json:",inline"`

	// +optional
	metav1.ObjectMeta `json:"metadata,omitzero"`

	// Desired state of the OpenBao namespace.
	// +required
	Spec BaoNamespaceSpec `json:"spec"`

	// Observed state reported by the operator; id holds the OpenBao namespace ID.
	// +optional
	Status BaoNamespaceStatus `json:"status,omitzero"`
}

// GetResourceSpec implements commonv1alpha1.Managed.
func (n *BaoNamespace) GetResourceSpec() *commonv1alpha1.ResourceSpec {
	return &n.Spec.ResourceSpec
}

// GetResourceStatus implements commonv1alpha1.Managed.
func (n *BaoNamespace) GetResourceStatus() *commonv1alpha1.ResourceStatus {
	return &n.Status.ResourceStatus
}

// +kubebuilder:object:root=true

// BaoNamespaceList contains a list of BaoNamespace.
type BaoNamespaceList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []BaoNamespace `json:"items"`
}

// CustomMetadataMarkerPrefix starts the namespace custom_metadata keys that hold the operator's ownership marker.
const CustomMetadataMarkerPrefix = "openbaoconfig-"

func init() {
	SchemeBuilder.Register(&BaoNamespace{}, &BaoNamespaceList{})
}
