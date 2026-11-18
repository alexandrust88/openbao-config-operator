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
	"k8s.io/apimachinery/pkg/runtime"

	commonv1alpha1 "github.com/alexandrust88/openbao-config-operator/api/common/v1alpha1"
)

// +kubebuilder:validation:XValidation:rule="self.path == oldSelf.path",message="path is immutable"
// +kubebuilder:validation:XValidation:rule="has(self.data) || has(self.dataSecretRef)",message="data or dataSecretRef is required"

// BaoEndpointSpec writes an arbitrary payload to an arbitrary OpenBao path.
type BaoEndpointSpec struct {
	commonv1alpha1.ResourceSpec `json:",inline"`

	// OpenBao API path the payload is written to (POST /v1/<path>), relative to the OpenBao namespace, without a leading slash or the v1/ prefix. Immutable. Example: auth/userpass/users/alice
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:Pattern=`^[^/].*$`
	// +required
	Path string `json:"path"`

	// Path read back to compare against data, for endpoints whose read lives elsewhere. Default: path. Example: sys/auth/userpass/tune
	// +kubebuilder:validation:Pattern=`^[^/].*$`
	// +optional
	ReadPath string `json:"readPath,omitempty"`

	// Path deleted when the resource is deleted with deletionPolicy Delete. Default: path. Example: sys/auth/userpass
	// +kubebuilder:validation:Pattern=`^[^/].*$`
	// +optional
	DeletePath string `json:"deletePath,omitempty"`

	// JSON object written to path, with the API's own parameter names. Only its keys are compared with the read; a key removed from data is not unset in OpenBao. Put secret values in dataSecretRef instead. Example: {"token_policies": ["default"], "token_ttl": "1h"}
	// +kubebuilder:validation:Type=object
	// +kubebuilder:pruning:PreserveUnknownFields
	// +optional
	Data *runtime.RawExtension `json:"data,omitempty"`

	// Secret key holding a JSON object merged over data, for secret parameters such as passwords. Its keys are write-only: never compared with the read, tracked by status.writeOnlyHash, and re-sent when the Secret changes. Read from a Secret key; never stored in status. One of data and dataSecretRef is required.
	// +optional
	DataSecretRef *commonv1alpha1.SecretKeySelector `json:"dataSecretRef,omitempty"`

	// Keys of data that a read does not return (write-only API parameters); they are sent but never compared, so they cause no endless rewrite. Example: ["password"]
	// +listType=set
	// +optional
	WriteOnlyFields []string `json:"writeOnlyFields,omitempty"`

	// When true, keys of data missing from the read response count as in sync instead of as drift. Default: false.
	// +optional
	IgnoreAbsentFields bool `json:"ignoreAbsentFields,omitempty"`

	// When true, the path is never read back: the payload is written on create and whenever the spec or the referenced Secret changes, and drift is not detected. For endpoints without a read. Default: false.
	// +optional
	DisableRead bool `json:"disableRead,omitempty"`

	// When true, nothing is deleted in OpenBao when the resource is deleted, even with deletionPolicy Delete. For endpoints without a delete. Default: false.
	// +optional
	DisableDelete bool `json:"disableDelete,omitempty"`
}

// BaoEndpointStatus is the observed state of a BaoEndpoint.
type BaoEndpointStatus struct {
	commonv1alpha1.ResourceStatus `json:",inline"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:shortName=baoep,categories=obco;obco-core
// +kubebuilder:printcolumn:name="Path",type=string,JSONPath=`.spec.path`
// +kubebuilder:printcolumn:name="Ready",type=string,JSONPath=`.status.conditions[?(@.type=="Ready")].status`
// +kubebuilder:printcolumn:name="Synced",type=string,JSONPath=`.status.conditions[?(@.type=="Synced")].status`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// BaoEndpoint writes any JSON payload to any OpenBao API path and keeps it in sync: the escape hatch for settings without a typed kind. Prefer a typed kind when one exists; it adds validation, references and exact drift handling. The default deletionPolicy Orphan leaves the path in place; Delete removes path (or deletePath).
//
// Example:
//
//	spec:
//	  connectionRef:
//	    name: openbao
//	  path: auth/userpass/users/alice
//	  data:
//	    token_policies: ["default"]
//	  dataSecretRef:
//	    name: alice-password
//	    key: payload.json
type BaoEndpoint struct {
	metav1.TypeMeta `json:",inline"`

	// +optional
	metav1.ObjectMeta `json:"metadata,omitzero"`

	// Desired state: the OpenBao path and the payload to keep there.
	// +required
	Spec BaoEndpointSpec `json:"spec"`

	// Observed state reported by the operator: conditions, the written path and namespace, and ownership.
	// +optional
	Status BaoEndpointStatus `json:"status,omitzero"`
}

// GetResourceSpec implements commonv1alpha1.Managed.
func (e *BaoEndpoint) GetResourceSpec() *commonv1alpha1.ResourceSpec { return &e.Spec.ResourceSpec }

// GetResourceStatus implements commonv1alpha1.Managed.
func (e *BaoEndpoint) GetResourceStatus() *commonv1alpha1.ResourceStatus {
	return &e.Status.ResourceStatus
}

// +kubebuilder:object:root=true

// BaoEndpointList contains a list of BaoEndpoint.
type BaoEndpointList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []BaoEndpoint `json:"items"`
}

func init() {
	SchemeBuilder.Register(&BaoEndpoint{}, &BaoEndpointList{})
}
