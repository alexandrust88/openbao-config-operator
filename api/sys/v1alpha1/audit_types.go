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

// AuditType is the kind of an audit device.
// +kubebuilder:validation:Enum=file;socket;syslog;http
type AuditType string

// Value of one audit device option, as a string. Example: /var/log/openbao/audit.log
// +kubebuilder:validation:MaxLength=4096
type AuditOptionValue string

// +kubebuilder:validation:XValidation:rule="!has(self.__namespace__) || self.__namespace__ == ''",message="Audit exists only in the root namespace: leave namespace empty"
// +kubebuilder:validation:XValidation:rule="has(self.path) == has(oldSelf.path) && (!has(self.path) || self.path == oldSelf.path)",message="path is immutable"
// +kubebuilder:validation:XValidation:rule="self.type == oldSelf.type",message="type is immutable"
// +kubebuilder:validation:XValidation:rule="has(self.description) == has(oldSelf.description) && (!has(self.description) || self.description == oldSelf.description)",message="description is immutable"
// +kubebuilder:validation:XValidation:rule="has(self.local) == has(oldSelf.local) && (!has(self.local) || self.local == oldSelf.local)",message="local is immutable"
// +kubebuilder:validation:XValidation:rule="has(self.options) == has(oldSelf.options) && (!has(self.options) || self.options == oldSelf.options)",message="options is immutable"
// +kubebuilder:validation:XValidation:rule="has(self.optionsSecretRefs) == has(oldSelf.optionsSecretRefs) && (!has(self.optionsSecretRefs) || self.optionsSecretRefs == oldSelf.optionsSecretRefs)",message="optionsSecretRefs is immutable"
// +kubebuilder:validation:XValidation:rule="!has(self.options) || !has(self.optionsSecretRefs) || self.optionsSecretRefs.all(k, !(k in self.options))",message="an option is set in both options and optionsSecretRefs"

// AuditSpec enables an audit device at sys/audit/<path>. Audit devices exist only in the root namespace and cannot be changed once enabled, so every field is immutable. OpenBao refuses to enable audit devices through the API unless the server configuration sets unsafe_allow_api_audit_creation; on other servers declare the device in the server configuration and use creationPolicy Adopt to assert that it exists.
type AuditSpec struct {
	commonv1alpha1.ResourceSpec `json:",inline"`

	// Path the audit device is enabled at, under sys/audit/. Default: metadata.name. Immutable: it cannot be added, changed or removed after creation. Example: file
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=256
	// +kubebuilder:validation:Pattern=`^[A-Za-z0-9_-][A-Za-z0-9._-]*(/[A-Za-z0-9_-][A-Za-z0-9._-]*)*$`
	// +optional
	Path string `json:"path,omitempty"`

	// Audit device type. Maps to `type`. Allowed values: file, socket, syslog, http. Immutable. Example: file
	// +required
	Type AuditType `json:"type"`

	// Human-readable description of the audit device. Maps to `description`. Immutable. Example: Main file audit log
	// +kubebuilder:validation:MaxLength=1024
	// +optional
	Description string `json:"description,omitempty"`

	// Marks the audit device as local to this server cluster. Maps to `local`. Default: false. Immutable.
	// +optional
	Local bool `json:"local,omitempty"`

	// Device options as string key/value pairs. Maps to `options`; the keys depend on the type, such as file_path, format, hmac_accessor or prefix (prefix needs allow_audit_log_prefixing in the server configuration). Immutable. Example: {"file_path": "/var/log/openbao/audit.log"}
	// +kubebuilder:validation:MaxProperties=32
	// +optional
	Options map[string]AuditOptionValue `json:"options,omitempty"`

	// Device options whose values come from Secret keys, for values that must not appear in the resource, such as the headers of an http device that carry a token. The map key is the option name and must not also be in options. Read from a Secret key; never stored in status. Immutable. Example: {"headers": {"name": "audit-http", "key": "headers"}}
	// +kubebuilder:validation:MaxProperties=8
	// +optional
	OptionsSecretRefs map[string]commonv1alpha1.SecretKeySelector `json:"optionsSecretRefs,omitempty"`
}

// AuditStatus is the observed state of an Audit.
type AuditStatus struct {
	commonv1alpha1.ResourceStatus `json:",inline"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:categories=openbao
// +kubebuilder:printcolumn:name="Type",type=string,JSONPath=`.spec.type`
// +kubebuilder:printcolumn:name="Ready",type=string,JSONPath=`.status.conditions[?(@.type=="Ready")].status`
// +kubebuilder:printcolumn:name="Synced",type=string,JSONPath=`.status.conditions[?(@.type=="Synced")].status`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// Audit enables an OpenBao audit device at sys/audit/<path>, in the root namespace only; every field is immutable. OpenBao accepts this only when the server configuration sets unsafe_allow_api_audit_creation; otherwise declare the device in the server configuration and use creationPolicy Adopt to assert it exists. The default deletionPolicy Orphan keeps the device; Delete needs the annotation openbaoconfig.io/allow-destructive-delete: "true".
//
// Example:
//
//	spec:
//	  connectionRef:
//	    kind: ClusterOpenBaoConnection
//	    name: openbao
//	  type: file
//	  path: file
//	  options:
//	    file_path: /var/log/openbao/audit.log
type Audit struct {
	metav1.TypeMeta `json:",inline"`

	// +optional
	metav1.ObjectMeta `json:"metadata,omitzero"`

	// Desired state of the audit device.
	// +required
	Spec AuditSpec `json:"spec"`

	// Observed state reported by the operator.
	// +optional
	Status AuditStatus `json:"status,omitzero"`
}

// GetResourceSpec implements commonv1alpha1.Managed.
func (a *Audit) GetResourceSpec() *commonv1alpha1.ResourceSpec {
	return &a.Spec.ResourceSpec
}

// GetResourceStatus implements commonv1alpha1.Managed.
func (a *Audit) GetResourceStatus() *commonv1alpha1.ResourceStatus {
	return &a.Status.ResourceStatus
}

// +kubebuilder:object:root=true

// AuditList contains a list of Audit.
type AuditList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []Audit `json:"items"`
}

func init() {
	SchemeBuilder.Register(&Audit{}, &AuditList{})
}
