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

// spec is the desired configuration of a Kubernetes secrets engine at `<mountPath>/config`: the cluster OpenBao creates service accounts and tokens in. An omitted kubernetesCACert or disableLocalCAJWT is reset to its default; an omitted kubernetesHost is left to OpenBao, which falls back to its own cluster when it runs in a pod.
type KubernetesSecretsConfigSpec struct {
	commonv1alpha1.ResourceSpec `json:",inline"`

	// mountPath is the path of the Kubernetes secrets engine mount, without leading or trailing slashes. Immutable. Example: `kubernetes`.
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=128
	// +kubebuilder:validation:Pattern=`^[A-Za-z0-9_.-]+(/[A-Za-z0-9_.-]+)*$`
	// +required
	MountPath string `json:"mountPath"`

	// kubernetesHost is the URL of the Kubernetes API server (`kubernetes_host`). Example: `https://kubernetes.default.svc`.
	// +kubebuilder:validation:MaxLength=2048
	// +optional
	KubernetesHost string `json:"kubernetesHost,omitempty"`

	// kubernetesCACert is the PEM-encoded CA certificate that verifies the Kubernetes API server (`kubernetes_ca_cert`). Empty uses the pod's CA unless disableLocalCAJWT is true. Example: `-----BEGIN CERTIFICATE-----...`.
	// +kubebuilder:validation:MaxLength=65536
	// +optional
	KubernetesCACert string `json:"kubernetesCACert,omitempty"`

	// serviceAccountJWTSecretRef selects the Secret key holding the token of the service account OpenBao uses to manage Kubernetes credentials (`service_account_jwt`). Read from a Secret key; never stored in status. Write-only: OpenBao never returns it; a change of the Secret is written again. Example: `{name: openbao-k8s, key: token}`.
	// +optional
	ServiceAccountJWTSecretRef *commonv1alpha1.SecretKeySelector `json:"serviceAccountJWTSecretRef,omitempty"`

	// disableLocalCAJWT stops OpenBao from falling back to the CA certificate and token of its own pod (`disable_local_ca_jwt`). Defaults to false.
	// +optional
	DisableLocalCAJWT *bool `json:"disableLocalCAJWT,omitempty"`
}

// status is the observed state of the Kubernetes secrets engine configuration.
type KubernetesSecretsConfigStatus struct {
	commonv1alpha1.ResourceStatus `json:",inline"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:categories=openbao
// +kubebuilder:printcolumn:name="Ready",type=string,JSONPath=`.status.conditions[?(@.type=="Ready")].status`
// +kubebuilder:printcolumn:name="Synced",type=string,JSONPath=`.status.conditions[?(@.type=="Synced")].status`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// KubernetesSecretsConfig sets the configuration of a Kubernetes secrets engine at `<mount>/config`: the cluster in which OpenBao creates service accounts, tokens and role bindings for KubernetesSecretsRole credentials.
//
// Example:
//
//	spec:
//	  connectionRef:
//	    name: openbao
//	  mountPath: kubernetes
//	  kubernetesHost: https://kubernetes.example.com:6443
//	  serviceAccountJWTSecretRef: {name: openbao-k8s, key: token}
type KubernetesSecretsConfig struct {
	metav1.TypeMeta `json:",inline"`

	// +optional
	metav1.ObjectMeta `json:"metadata,omitzero"`

	// +required
	Spec KubernetesSecretsConfigSpec `json:"spec"`

	// +optional
	Status KubernetesSecretsConfigStatus `json:"status,omitzero"`
}

// GetResourceSpec implements commonv1alpha1.Managed.
func (c *KubernetesSecretsConfig) GetResourceSpec() *commonv1alpha1.ResourceSpec {
	return &c.Spec.ResourceSpec
}

// GetResourceStatus implements commonv1alpha1.Managed.
func (c *KubernetesSecretsConfig) GetResourceStatus() *commonv1alpha1.ResourceStatus {
	return &c.Status.ResourceStatus
}

// +kubebuilder:object:root=true

// KubernetesSecretsConfigList contains a list of KubernetesSecretsConfig.
type KubernetesSecretsConfigList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []KubernetesSecretsConfig `json:"items"`
}

func init() {
	SchemeBuilder.Register(&KubernetesSecretsConfig{}, &KubernetesSecretsConfigList{})
}
