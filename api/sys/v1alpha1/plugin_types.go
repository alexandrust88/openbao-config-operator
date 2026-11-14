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

// PluginType is the kind of backend a plugin implements.
// +kubebuilder:validation:Enum=auth;database;secret
type PluginType string

// Plugin types of the catalog.
const (
	PluginTypeAuth     PluginType = "auth"
	PluginTypeDatabase PluginType = "database"
	PluginTypeSecret   PluginType = "secret"
)

// An environment variable of the plugin process whose value is read from a Secret key.
type PluginEnvVar struct {
	// Name of the environment variable, sent as NAME=value in `env`. Example: PLUGIN_LOG_LEVEL
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=128
	// +kubebuilder:validation:Pattern=`^[A-Za-z_][A-Za-z0-9_]*$`
	// +required
	Name string `json:"name"`

	// Secret key, in the same namespace as this resource, that holds the variable's value. Read from a Secret key; never stored in status.
	// +required
	SecretKeyRef commonv1alpha1.SecretKeySelector `json:"secretKeyRef"`
}

// +kubebuilder:validation:XValidation:rule="!has(self.__namespace__) || self.__namespace__ == ''",message="Plugin exists only in the root namespace: leave namespace empty"
// +kubebuilder:validation:XValidation:rule="self.type == oldSelf.type",message="type is immutable"
// +kubebuilder:validation:XValidation:rule="has(self.name) == has(oldSelf.name) && (!has(self.name) || self.name == oldSelf.name)",message="name is immutable"
// +kubebuilder:validation:XValidation:rule="has(self.version) == has(oldSelf.version) && (!has(self.version) || self.version == oldSelf.version)",message="version is immutable: register a new version with a new resource"
// +kubebuilder:validation:XValidation:rule="!has(self.version) || !has(self.deletionPolicy) || self.deletionPolicy == 'Orphan'",message="a versioned plugin cannot be deregistered by the operator: use deletionPolicy Orphan"
// +kubebuilder:validation:XValidation:rule="!self.command.contains('..')",message="command must not contain '..'"

// PluginSpec registers an external plugin in the OpenBao plugin catalog at sys/plugins/catalog/<type>/<name>. The plugin binary must already be in the server's plugin_directory. The catalog exists only in the root namespace. Deregistering a plugin breaks the mounts that use it, so deletion defaults to Orphan.
type PluginSpec struct {
	commonv1alpha1.ResourceSpec `json:",inline"`

	// Kind of backend the plugin implements, the <type> in sys/plugins/catalog/<type>/<name>: auth, database or secret. Immutable. Example: secret
	// +required
	Type PluginType `json:"type"`

	// Name of the plugin in the catalog, the <name> in sys/plugins/catalog/<type>/<name>; mounts use it as their type. Default: metadata.name. Immutable. Example: openbao-plugin-secrets-nomad
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=128
	// +kubebuilder:validation:Pattern=`^[A-Za-z0-9_][A-Za-z0-9_.+-]*$`
	// +optional
	Name string `json:"name,omitempty"`

	// File name of the plugin binary, relative to the server's plugin_directory; must not contain "..". Pass arguments in args. Maps to `command`. Example: openbao-plugin-secrets-nomad
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=256
	// +kubebuilder:validation:Pattern=`^[A-Za-z0-9_][A-Za-z0-9_.+-]*$`
	// +required
	Command string `json:"command"`

	// Hex-encoded SHA-256 checksum of the plugin binary (64 lowercase hex characters). Maps to `sha256`. Example: 2c26b46b68ffc68ff99b453c1d30413413422d706483bfa0f98a5e886266e7ae
	// +kubebuilder:validation:Pattern=`^[a-f0-9]{64}$`
	// +required
	SHA256 string `json:"sha256"`

	// Arguments passed to the plugin binary, in order. Maps to `args`. Example: ["-tls-skip-verify=false"]
	// +kubebuilder:validation:MaxItems=64
	// +kubebuilder:validation:items:MaxLength=1024
	// +listType=atomic
	// +optional
	Args []string `json:"args,omitempty"`

	// Environment variables of the plugin process, each read from a Secret key and sent as NAME=value in `env`. OpenBao never returns them, so a changed Secret is detected by its hash (status.writeOnlyHash) and written again; the values are never stored in status.
	// +kubebuilder:validation:MaxItems=64
	// +listType=map
	// +listMapKey=name
	// +optional
	Env []PluginEnvVar `json:"env,omitempty"`

	// Semantic version of the plugin; a leading "v" is added if missing. Maps to `version`. A plugin binary that reports its own version must be registered with that version. Drift detection of a versioned plugin covers sha256 only, and a versioned plugin requires deletionPolicy Orphan. Immutable: register a new version with a new resource. Example: v0.21.0
	// +kubebuilder:validation:MaxLength=128
	// +kubebuilder:validation:Pattern=`^v?[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z.-]+)?(\+[0-9A-Za-z.-]+)?$`
	// +optional
	Version string `json:"version,omitempty"`

	// Whether the server downloads the plugin as an OCI image (declared in the plugin stanza of the server configuration) instead of using a binary placed in plugin_directory. Maps to `oci`. Default: false. Example: true
	// +optional
	OCI bool `json:"oci,omitempty"`
}

// PluginStatus is the observed state of a Plugin.
type PluginStatus struct {
	commonv1alpha1.ResourceStatus `json:",inline"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:categories=openbao
// +kubebuilder:printcolumn:name="Type",type=string,JSONPath=`.spec.type`
// +kubebuilder:printcolumn:name="Version",type=string,JSONPath=`.spec.version`
// +kubebuilder:printcolumn:name="Ready",type=string,JSONPath=`.status.conditions[?(@.type=="Ready")].status`
// +kubebuilder:printcolumn:name="Synced",type=string,JSONPath=`.status.conditions[?(@.type=="Synced")].status`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// Plugin registers an external plugin in the OpenBao plugin catalog at sys/plugins/catalog/<type>/<name>, so mounts can use it. The binary must already be in the server's plugin_directory (or be an OCI plugin). Root namespace only. deletionPolicy defaults to Orphan, because deregistering breaks the mounts that use the plugin; Delete deregisters an unversioned plugin.
//
// Example:
//
//	spec:
//	  connectionRef:
//	    name: openbao
//	  type: secret
//	  name: openbao-plugin-secrets-nomad
//	  command: openbao-plugin-secrets-nomad
//	  sha256: 2c26b46b68ffc68ff99b453c1d30413413422d706483bfa0f98a5e886266e7ae
//	  version: v0.21.0
type Plugin struct {
	metav1.TypeMeta `json:",inline"`

	// +optional
	metav1.ObjectMeta `json:"metadata,omitzero"`

	// Desired state of the plugin catalog entry.
	// +required
	Spec PluginSpec `json:"spec"`

	// Observed state reported by the operator: conditions, OpenBao path, ownership and the hash of env values.
	// +optional
	Status PluginStatus `json:"status,omitzero"`
}

// GetResourceSpec implements commonv1alpha1.Managed.
func (p *Plugin) GetResourceSpec() *commonv1alpha1.ResourceSpec { return &p.Spec.ResourceSpec }

// GetResourceStatus implements commonv1alpha1.Managed.
func (p *Plugin) GetResourceStatus() *commonv1alpha1.ResourceStatus { return &p.Status.ResourceStatus }

// +kubebuilder:object:root=true

// PluginList contains a list of Plugin.
type PluginList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []Plugin `json:"items"`
}

func init() {
	SchemeBuilder.Register(&Plugin{}, &PluginList{})
}
