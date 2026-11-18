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
// +kubebuilder:validation:XValidation:rule="self.path == oldSelf.path",message="path is immutable"
// +kubebuilder:validation:XValidation:rule="has(self.kvVersion) == has(oldSelf.kvVersion) && (!has(self.kvVersion) || self.kvVersion == oldSelf.kvVersion)",message="kvVersion is immutable"
// +kubebuilder:validation:XValidation:rule="has(self.dataFrom) || has(self.data)",message="set dataFrom or data"

// spec is the desired KV secret; its values come from Kubernetes Secrets.
//
// The resource owns the keys it declares: other keys at the same path are kept, and keys removed from the spec are removed from OpenBao. Values are write-only: a changed Secret is written, but a value changed in OpenBao is not compared or reverted; a key removed in OpenBao is written again.
type KVSecretSpec struct {
	commonv1alpha1.ResourceSpec `json:",inline"`

	// mountPath is the path of the KV secrets engine mount, without leading or trailing slashes. Immutable. Example: `secret`.
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=128
	// +kubebuilder:validation:Pattern=`^[A-Za-z0-9_.-]+(/[A-Za-z0-9_.-]+)*$`
	// +required
	MountPath string `json:"mountPath"`

	// path is the secret path inside the mount, without the `data/` prefix of KV version 2 (the operator adds it). Immutable. Example: `apps/billing/db`.
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=512
	// +kubebuilder:validation:Pattern=`^[^/]+(/[^/]+)*$`
	// +required
	Path string `json:"path"`

	// kvVersion is the version of the KV engine at mountPath: 1 writes `<mountPath>/<path>`, 2 writes `<mountPath>/data/<path>`. Allowed: 1, 2. Defaults to 2. Immutable.
	// +kubebuilder:validation:Enum=1;2
	// +kubebuilder:default=2
	// +optional
	KVVersion int32 `json:"kvVersion,omitempty"`

	// dataFrom copies every key of each listed Secret into the OpenBao secret; a later entry wins over an earlier one. Values are read from the Secrets and never stored in status. 1-16 entries.
	// +kubebuilder:validation:MinItems=1
	// +kubebuilder:validation:MaxItems=16
	// +listType=atomic
	// +optional
	DataFrom []KVSecretDataFrom `json:"dataFrom,omitempty"`

	// data maps a key of the OpenBao secret to one Secret key and wins over dataFrom. Values are read from the Secret keys and never stored in status. 1-64 keys. Example: `password: {secretKeyRef: {name: db-creds, key: password}}`.
	// +kubebuilder:validation:MinProperties=1
	// +kubebuilder:validation:MaxProperties=64
	// +optional
	Data map[string]KVSecretDataSource `json:"data,omitempty"`
}

// A Secret whose keys are all copied into the OpenBao secret.
type KVSecretDataFrom struct {
	// secretRef names a Secret in the resource's namespace. Example: `{name: db-creds}`.
	// +required
	SecretRef commonv1alpha1.LocalObjectReference `json:"secretRef"`
}

// The source of the value of one key of the OpenBao secret.
type KVSecretDataSource struct {
	// secretKeyRef selects a key of a Secret in the resource's namespace; the value is read from it and never stored in status. Example: `{name: db-creds, key: password}`.
	// +required
	SecretKeyRef commonv1alpha1.SecretKeySelector `json:"secretKeyRef"`
}

// status is the observed state of the KV secret.
type KVSecretStatus struct {
	commonv1alpha1.ResourceStatus `json:",inline"`

	// managedKeys are the keys of the OpenBao secret written by this resource (names only, never values).
	// +listType=set
	// +optional
	ManagedKeys []string `json:"managedKeys,omitempty"`

	// pathCreated is true when this resource created the KV secret rather than writing its keys to an existing one; only then does a destructive delete also remove the secret's version history.
	// +optional
	PathCreated bool `json:"pathCreated,omitempty"`

	// purgeThroughVersion is the persisted upper version bound for destructive KV v2 cleanup. Newer versions and metadata are retained.
	// +kubebuilder:validation:Minimum=1
	// +optional
	PurgeThroughVersion *int64 `json:"purgeThroughVersion,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:shortName=baokvs,categories=obco;obco-secrets
// +kubebuilder:printcolumn:name="Ready",type=string,JSONPath=`.status.conditions[?(@.type=="Ready")].status`
// +kubebuilder:printcolumn:name="Synced",type=string,JSONPath=`.status.conditions[?(@.type=="Synced")].status`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// KVSecret writes a secret to a KV secrets engine at `<mount>/data/<path>` (version 2) or `<mount>/<path>` (version 1), with values read from Kubernetes Secrets. Use it to seed static credentials into OpenBao; it owns only the keys it declares.
//
// Example:
//
//	spec:
//	  connectionRef:
//	    name: openbao
//	  mountPath: secret
//	  path: apps/billing/db
//	  data:
//	    password:
//	      secretKeyRef: {name: db-creds, key: password}
type KVSecret struct {
	metav1.TypeMeta `json:",inline"`

	// +optional
	metav1.ObjectMeta `json:"metadata,omitzero"`

	// +required
	Spec KVSecretSpec `json:"spec"`

	// +optional
	Status KVSecretStatus `json:"status,omitzero"`
}

// GetResourceSpec implements commonv1alpha1.Managed.
func (s *KVSecret) GetResourceSpec() *commonv1alpha1.ResourceSpec { return &s.Spec.ResourceSpec }

// GetResourceStatus implements commonv1alpha1.Managed.
func (s *KVSecret) GetResourceStatus() *commonv1alpha1.ResourceStatus {
	return &s.Status.ResourceStatus
}

// +kubebuilder:object:root=true

// KVSecretList contains a list of KVSecret.
type KVSecretList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []KVSecret `json:"items"`
}

func init() {
	SchemeBuilder.Register(&KVSecret{}, &KVSecretList{})
}
