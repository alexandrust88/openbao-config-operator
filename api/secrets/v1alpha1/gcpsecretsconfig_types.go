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
// +kubebuilder:validation:XValidation:rule="!has(self.deletionPolicy) || self.deletionPolicy == 'Orphan'",message="deletionPolicy must be Orphan: the GCP secrets engine configuration cannot be deleted"

// spec is the desired configuration of a GCP secrets engine at `<mountPath>/config`. The engine mount itself is managed by a SecretsMount. deletionPolicy must be Orphan.
type GCPSecretsConfigSpec struct {
	commonv1alpha1.ResourceSpec `json:",inline"`

	// mountPath is the path of the GCP secrets engine mount. Defaults to `gcp`. Immutable. Example: `gcp`.
	// +kubebuilder:default=gcp
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=128
	// +kubebuilder:validation:Pattern=`^[A-Za-z0-9_.-]+(/[A-Za-z0-9_.-]+)*$`
	// +optional
	MountPath string `json:"mountPath,omitempty"`

	// credentialsSecretRef selects the Secret key that holds the JSON key of the service account OpenBao uses (`credentials`). Without it the engine uses the Application Default Credentials of its environment. Read from a Secret key; never stored in status. Write-only: a change of the Secret is written again. Example: `{name: gcp-root, key: key.json}`.
	// +optional
	CredentialsSecretRef *commonv1alpha1.SecretKeySelector `json:"credentialsSecretRef,omitempty"`

	// ttl is the default lease duration of service account keys (`ttl`), a duration. Defaults to the mount's default lease TTL. Example: `1h`.
	// +kubebuilder:validation:Type=string
	// +kubebuilder:validation:Pattern=`^(0|([0-9]+(\.[0-9]+)?(ns|us|ms|s|m|h))+)$`
	// +optional
	TTL *metav1.Duration `json:"ttl,omitempty"`

	// maxTTL is the maximum lease duration of service account keys (`max_ttl`), a duration. Defaults to the mount's max lease TTL. Example: `24h`.
	// +kubebuilder:validation:Type=string
	// +kubebuilder:validation:Pattern=`^(0|([0-9]+(\.[0-9]+)?(ns|us|ms|s|m|h))+)$`
	// +optional
	MaxTTL *metav1.Duration `json:"maxTTL,omitempty"`
}

// status is the observed state of the GCP secrets engine configuration.
type GCPSecretsConfigStatus struct {
	commonv1alpha1.ResourceStatus `json:",inline"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:categories=obco;obco-secrets
// +kubebuilder:printcolumn:name="Ready",type=string,JSONPath=`.status.conditions[?(@.type=="Ready")].status`
// +kubebuilder:printcolumn:name="Synced",type=string,JSONPath=`.status.conditions[?(@.type=="Synced")].status`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// GCPSecretsConfig sets the configuration of a GCP secrets engine (external plugin) at `<mount>/config`: the service account credentials OpenBao uses and the lease limits. A singleton per mount; it is never deleted.
//
// Example:
//
//	spec:
//	  connectionRef:
//	    name: openbao
//	  mountPath: gcp
//	  credentialsSecretRef: {name: gcp-root, key: key.json}
//	  ttl: 1h
type GCPSecretsConfig struct {
	metav1.TypeMeta `json:",inline"`

	// +optional
	metav1.ObjectMeta `json:"metadata,omitzero"`

	// +required
	Spec GCPSecretsConfigSpec `json:"spec"`

	// +optional
	Status GCPSecretsConfigStatus `json:"status,omitzero"`
}

// GetResourceSpec implements commonv1alpha1.Managed.
func (o *GCPSecretsConfig) GetResourceSpec() *commonv1alpha1.ResourceSpec {
	return &o.Spec.ResourceSpec
}

// GetResourceStatus implements commonv1alpha1.Managed.
func (o *GCPSecretsConfig) GetResourceStatus() *commonv1alpha1.ResourceStatus {
	return &o.Status.ResourceStatus
}

// +kubebuilder:object:root=true

// GCPSecretsConfigList contains a list of GCPSecretsConfig.
type GCPSecretsConfigList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []GCPSecretsConfig `json:"items"`
}

func init() {
	SchemeBuilder.Register(&GCPSecretsConfig{}, &GCPSecretsConfigList{})
}
