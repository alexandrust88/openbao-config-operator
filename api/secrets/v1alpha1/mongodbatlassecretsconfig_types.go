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
// +kubebuilder:validation:XValidation:rule="!has(self.deletionPolicy) || self.deletionPolicy == 'Orphan'",message="deletionPolicy must be Orphan: the plugin configuration is kept when the resource is deleted"

// spec is the desired programmatic API key configuration of the MongoDB Atlas secrets plugin at `<mountPath>/config`. deletionPolicy must be Orphan.
type MongoDBAtlasSecretsConfigSpec struct {
	commonv1alpha1.ResourceSpec `json:",inline"`

	// mountPath is the path the MongoDB Atlas secrets plugin is mounted at. Defaults to `mongodbatlas`. Immutable. Example: `mongodbatlas`.
	// +kubebuilder:default=mongodbatlas
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=256
	// +kubebuilder:validation:Pattern=`^[A-Za-z0-9_.-]+(/[A-Za-z0-9_.-]+)*$`
	// +optional
	MountPath string `json:"mountPath,omitempty"`

	// publicKey is the public part of the MongoDB Atlas programmatic API key (`public_key`). Example: `abcdefgh`.
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=256
	// +required
	PublicKey string `json:"publicKey"`

	// privateKeySecretRef selects the Secret key holding the private part of the API key (`private_key`). Read from a Secret key; never stored in status. Write-only. Example: `{name: atlas-api, key: private-key}`.
	// +required
	PrivateKeySecretRef commonv1alpha1.SecretKeySelector `json:"privateKeySecretRef"`
}

// status is the observed state of the MongoDB Atlas secrets plugin configuration.
type MongoDBAtlasSecretsConfigStatus struct {
	commonv1alpha1.ResourceStatus `json:",inline"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:categories=obco;obco-secrets
// +kubebuilder:printcolumn:name="Ready",type=string,JSONPath=`.status.conditions[?(@.type=="Ready")].status`
// +kubebuilder:printcolumn:name="Synced",type=string,JSONPath=`.status.conditions[?(@.type=="Synced")].status`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// MongoDBAtlasSecretsConfig sets the programmatic API key of a MongoDB Atlas secrets plugin mount (external plugin) at `<mount>/config`, which OpenBao uses to create short-lived Atlas API keys. A singleton per mount; it is never deleted.
//
// Example:
//
//	spec:
//	  connectionRef:
//	    name: openbao
//	  mountPath: mongodbatlas
//	  publicKey: abcdefgh
//	  privateKeySecretRef: {name: atlas-api, key: private-key}
type MongoDBAtlasSecretsConfig struct {
	metav1.TypeMeta `json:",inline"`

	// +optional
	metav1.ObjectMeta `json:"metadata,omitzero"`

	// +required
	Spec MongoDBAtlasSecretsConfigSpec `json:"spec"`

	// +optional
	Status MongoDBAtlasSecretsConfigStatus `json:"status,omitzero"`
}

// GetResourceSpec implements commonv1alpha1.Managed.
func (o *MongoDBAtlasSecretsConfig) GetResourceSpec() *commonv1alpha1.ResourceSpec {
	return &o.Spec.ResourceSpec
}

// GetResourceStatus implements commonv1alpha1.Managed.
func (o *MongoDBAtlasSecretsConfig) GetResourceStatus() *commonv1alpha1.ResourceStatus {
	return &o.Status.ResourceStatus
}

// +kubebuilder:object:root=true

// MongoDBAtlasSecretsConfigList contains a list of MongoDBAtlasSecretsConfig.
type MongoDBAtlasSecretsConfigList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []MongoDBAtlasSecretsConfig `json:"items"`
}

func init() {
	SchemeBuilder.Register(&MongoDBAtlasSecretsConfig{}, &MongoDBAtlasSecretsConfigList{})
}
