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

// +kubebuilder:validation:XValidation:rule="(has(self.data) && size(self.data) > 0) || (has(self.metadata) && size(self.metadata) > 0) || (has(self.versions) && self.versions)",message="set at least one of data, metadata and versions"

// AccessLevelSpec is a named set of capabilities on KV version 2 data and metadata. It writes nothing to OpenBao; AccessGrant rules and AccessLayout roles in the same namespace cite it by name.
type AccessLevelSpec struct {
	// ACL capabilities granted on secret data, the policy path <mount>/data/<path>. Allowed: create, read, update, patch, delete, list. Example: [read, list]
	// +kubebuilder:validation:MaxItems=6
	// +listType=set
	// +optional
	Data []AccessCapability `json:"data,omitempty"`

	// ACL capabilities granted on secret metadata, the policy path <mount>/metadata/<metadataPath> (listing keys and reading versions need list and read here). Allowed: create, read, update, patch, delete, list. Example: [list]
	// +kubebuilder:validation:MaxItems=6
	// +listType=set
	// +optional
	Metadata []AccessCapability `json:"metadata,omitempty"`

	// When true, also grants update on <mount>/delete/<path>, <mount>/undelete/<path> and <mount>/destroy/<path>, which soft-delete, restore and permanently destroy individual secret versions. Default: false. At least one of data, metadata and versions must be set. Example: true
	// +optional
	Versions bool `json:"versions,omitempty"`
}

// AccessLevelStatus is the observed state of an AccessLevel.
type AccessLevelStatus struct {
	commonv1alpha1.ResourceStatus `json:",inline"`

	// AccessGrant and AccessLayout resources that cite this level, as <kind>/<name>. While the list is not empty, the openbaoconfig.io/in-use finalizer holds a deletion of the level. Example: AccessGrant/app-reader
	// +optional
	CitedBy []string `json:"citedBy,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:categories=openbao
// +kubebuilder:printcolumn:name="Ready",type=string,JSONPath=`.status.conditions[?(@.type=="Ready")].status`
// +kubebuilder:printcolumn:name="Synced",type=string,JSONPath=`.status.conditions[?(@.type=="Synced")].status`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// AccessLevel is a named, reusable set of ACL capabilities on KV version 2 secret data and metadata, such as reader, writer or owner. It writes nothing to OpenBao by itself: AccessGrant rules and AccessLayout roles in the same Kubernetes namespace cite it by name and render it into their policies. The openbaoconfig.io/in-use finalizer keeps it while anything cites it.
//
// Example:
//
//	spec:
//	  data: [read, list]
//	  metadata: [read, list]
//	  versions: false
type AccessLevel struct {
	metav1.TypeMeta `json:",inline"`

	// +optional
	metav1.ObjectMeta `json:"metadata,omitzero"`

	// Desired state: the capabilities this level grants wherever it is cited.
	// +required
	Spec AccessLevelSpec `json:"spec"`

	// Observed state reported by the operator: conditions and the resources that cite this level.
	// +optional
	Status AccessLevelStatus `json:"status,omitzero"`
}

// GetResourceStatus returns the common status of the level.
func (l *AccessLevel) GetResourceStatus() *commonv1alpha1.ResourceStatus {
	return &l.Status.ResourceStatus
}

// +kubebuilder:object:root=true

// AccessLevelList contains a list of AccessLevel.
type AccessLevelList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []AccessLevel `json:"items"`
}

func init() {
	SchemeBuilder.Register(&AccessLevel{}, &AccessLevelList{})
}
