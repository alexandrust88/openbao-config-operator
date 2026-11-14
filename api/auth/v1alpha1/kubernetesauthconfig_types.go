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

// Desired state of the Kubernetes auth method configuration at auth/<mountPath>/config. OpenBao replaces the whole configuration on every write, so fields left unset are reset to their OpenBao defaults. deletionPolicy must be Orphan.
//
// +kubebuilder:validation:XValidation:rule="has(self.mountPath) == has(oldSelf.mountPath) && (!has(self.mountPath) || self.mountPath == oldSelf.mountPath)",message="mountPath is immutable"
// +kubebuilder:validation:XValidation:rule="!has(self.deletionPolicy) || self.deletionPolicy == 'Orphan'",message="deletionPolicy must be Orphan: the auth method configuration cannot be deleted, only replaced"
// +kubebuilder:validation:XValidation:rule="!has(self.disableLocalCAJWT) || !self.disableLocalCAJWT || has(self.kubernetesCACert)",message="kubernetesCACert is required when disableLocalCAJWT is true"
type KubernetesAuthConfigSpec struct {
	commonv1alpha1.ResourceSpec `json:",inline"`

	// Path of the Kubernetes auth method, without the auth/ prefix. Defaults to kubernetes. Immutable. Example: kubernetes/prod-cluster
	// +kubebuilder:default=kubernetes
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=128
	// +kubebuilder:validation:Pattern=`^[A-Za-z0-9_.-]+(/[A-Za-z0-9_.-]+)*$`
	// +optional
	MountPath string `json:"mountPath,omitempty"`

	// Kubernetes API server OpenBao calls for TokenReview: a host, a host:port pair or a URL. Required. Maps to `kubernetes_host`. Example: https://kubernetes.default.svc
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=2048
	// +required
	KubernetesHost string `json:"kubernetesHost"`

	// PEM-encoded CA certificate OpenBao uses to verify the Kubernetes API server. When unset, OpenBao uses its pod's local CA certificate; required when disableLocalCAJWT is true. Maps to `kubernetes_ca_cert`. Example: "-----BEGIN CERTIFICATE-----\n...\n-----END CERTIFICATE-----"
	// +kubebuilder:validation:MaxLength=65536
	// +optional
	KubernetesCACert string `json:"kubernetesCACert,omitempty"`

	// Secret key holding the JWT OpenBao uses as bearer token to call the TokenReview API. When unset, OpenBao uses its local ServiceAccount token, or else the JWT of the client logging in. Read from this Secret key; the value is never stored in status. Write-only: only a hash is kept in status.writeOnlyHash and a change of the Secret forces a write. Maps to `token_reviewer_jwt`. Example: {name: openbao-reviewer, key: token}
	// +optional
	TokenReviewerJWTSecretRef *commonv1alpha1.SecretKeySelector `json:"tokenReviewerJWTSecretRef,omitempty"`

	// PEM-encoded public keys or certificates used to verify ServiceAccount JWT signatures locally instead of through TokenReview. Maps to `pem_keys`. Example: ["-----BEGIN PUBLIC KEY-----\n...\n-----END PUBLIC KEY-----"]
	// +kubebuilder:validation:MaxItems=32
	// +kubebuilder:validation:items:MaxLength=16384
	// +listType=atomic
	// +optional
	PEMKeys []string `json:"pemKeys,omitempty"`

	// When true, OpenBao does not fall back to its pod's local CA certificate and ServiceAccount token; kubernetesCACert is then required. Defaults to false. Maps to `disable_local_ca_jwt`. Example: true
	// +optional
	DisableLocalCAJWT *bool `json:"disableLocalCAJWT,omitempty"`
}

// Observed state of the configuration, written by the operator. The token reviewer JWT never appears here.
type KubernetesAuthConfigStatus struct {
	commonv1alpha1.ResourceStatus `json:",inline"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:categories=openbao
// +kubebuilder:printcolumn:name="Ready",type=string,JSONPath=`.status.conditions[?(@.type=="Ready")].status`
// +kubebuilder:printcolumn:name="Synced",type=string,JSONPath=`.status.conditions[?(@.type=="Synced")].status`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// KubernetesAuthConfig manages the configuration of an OpenBao Kubernetes auth method at auth/<mountPath>/config. Use it to point the mount at a Kubernetes API server so pods can log in with their ServiceAccount token. OpenBao replaces the whole configuration on every write; unset fields go back to OpenBao defaults.
//
// Example:
//
//	spec:
//	  connectionRef:
//	    name: openbao
//	  deletionPolicy: Orphan
//	  mountPath: kubernetes
//	  kubernetesHost: https://kubernetes.default.svc
//	  disableLocalCAJWT: false
type KubernetesAuthConfig struct {
	metav1.TypeMeta `json:",inline"`

	// +optional
	metav1.ObjectMeta `json:"metadata,omitzero"`

	// +required
	Spec KubernetesAuthConfigSpec `json:"spec"`

	// +optional
	Status KubernetesAuthConfigStatus `json:"status,omitzero"`
}

// GetResourceSpec implements commonv1alpha1.Managed.
func (k *KubernetesAuthConfig) GetResourceSpec() *commonv1alpha1.ResourceSpec {
	return &k.Spec.ResourceSpec
}

// GetResourceStatus implements commonv1alpha1.Managed.
func (k *KubernetesAuthConfig) GetResourceStatus() *commonv1alpha1.ResourceStatus {
	return &k.Status.ResourceStatus
}

// +kubebuilder:object:root=true

// KubernetesAuthConfigList contains a list of KubernetesAuthConfig.
type KubernetesAuthConfigList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []KubernetesAuthConfig `json:"items"`
}

func init() {
	SchemeBuilder.Register(&KubernetesAuthConfig{}, &KubernetesAuthConfigList{})
}
