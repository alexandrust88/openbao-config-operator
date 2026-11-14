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

// The Azure cloud an engine talks to.
// +kubebuilder:validation:Enum=AzurePublicCloud;AzureUSGovernmentCloud;AzureChinaCloud;AzureGermanCloud
type AzureEnvironment string

// +kubebuilder:validation:XValidation:rule="has(self.mountPath) == has(oldSelf.mountPath) && (!has(self.mountPath) || self.mountPath == oldSelf.mountPath)",message="mountPath is immutable"
// +kubebuilder:validation:XValidation:rule="has(self.clientIDSecretRef) == has(self.clientSecretSecretRef)",message="set both or neither of clientIDSecretRef and clientSecretSecretRef"

// spec is the desired configuration of an Azure secrets engine at `<mountPath>/config`. The engine mount itself is managed by a SecretsMount.
type AzureSecretsConfigSpec struct {
	commonv1alpha1.ResourceSpec `json:",inline"`

	// mountPath is the path of the Azure secrets engine mount. Defaults to `azure`. Immutable. Example: `azure`.
	// +kubebuilder:default=azure
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=128
	// +kubebuilder:validation:Pattern=`^[A-Za-z0-9_.-]+(/[A-Za-z0-9_.-]+)*$`
	// +optional
	MountPath string `json:"mountPath,omitempty"`

	// subscriptionID is the Azure subscription ID (`subscription_id`). Example: `11111111-2222-3333-4444-555555555555`.
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=64
	// +required
	SubscriptionID string `json:"subscriptionID"`

	// tenantID is the Microsoft Entra tenant ID (`tenant_id`). Example: `66666666-7777-8888-9999-000000000000`.
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=64
	// +required
	TenantID string `json:"tenantID"`

	// clientIDSecretRef selects the Secret key that holds the client ID of the application OpenBao uses (`client_id`); set together with clientSecretSecretRef. Without client credentials the engine uses the managed identity of its environment. Read from a Secret key; never stored in status. Example: `{name: azure-root, key: client-id}`.
	// +optional
	ClientIDSecretRef *commonv1alpha1.SecretKeySelector `json:"clientIDSecretRef,omitempty"`

	// clientSecretSecretRef selects the Secret key that holds the client secret of that application (`client_secret`). Read from a Secret key; never stored in status. Write-only: a change of the Secret is written again. Example: `{name: azure-root, key: client-secret}`.
	// +optional
	ClientSecretSecretRef *commonv1alpha1.SecretKeySelector `json:"clientSecretSecretRef,omitempty"`

	// environment is the Azure cloud (`environment`). Allowed: AzurePublicCloud, AzureUSGovernmentCloud, AzureChinaCloud, AzureGermanCloud. Defaults to AzurePublicCloud.
	// +optional
	Environment AzureEnvironment `json:"environment,omitempty"`

	// passwordPolicy is the OpenBao password policy that generates client secrets (`password_policy`). Empty uses the plugin default. Example: `azure`.
	// +kubebuilder:validation:MaxLength=128
	// +optional
	PasswordPolicy string `json:"passwordPolicy,omitempty"`

	// rootPasswordTTL is the lifetime of the client secret created by a root credential rotation (`root_password_ttl`), a duration. Unset uses the plugin default of about six months. Example: `2190h`.
	// +kubebuilder:validation:Type=string
	// +kubebuilder:validation:Pattern=`^(0|([0-9]+(\.[0-9]+)?(ns|us|ms|s|m|h))+)$`
	// +optional
	RootPasswordTTL *metav1.Duration `json:"rootPasswordTTL,omitempty"`
}

// status is the observed state of the Azure secrets engine configuration.
type AzureSecretsConfigStatus struct {
	commonv1alpha1.ResourceStatus `json:",inline"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:categories=openbao
// +kubebuilder:printcolumn:name="Ready",type=string,JSONPath=`.status.conditions[?(@.type=="Ready")].status`
// +kubebuilder:printcolumn:name="Synced",type=string,JSONPath=`.status.conditions[?(@.type=="Synced")].status`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// AzureSecretsConfig sets the configuration of an Azure secrets engine (external plugin) at `<mount>/config`: the subscription, tenant and the application credentials OpenBao uses to create service principals.
//
// Example:
//
//	spec:
//	  connectionRef:
//	    name: openbao
//	  mountPath: azure
//	  subscriptionID: 11111111-2222-3333-4444-555555555555
//	  tenantID: 66666666-7777-8888-9999-000000000000
//	  clientIDSecretRef: {name: azure-root, key: client-id}
//	  clientSecretSecretRef: {name: azure-root, key: client-secret}
type AzureSecretsConfig struct {
	metav1.TypeMeta `json:",inline"`

	// +optional
	metav1.ObjectMeta `json:"metadata,omitzero"`

	// +required
	Spec AzureSecretsConfigSpec `json:"spec"`

	// +optional
	Status AzureSecretsConfigStatus `json:"status,omitzero"`
}

// GetResourceSpec implements commonv1alpha1.Managed.
func (o *AzureSecretsConfig) GetResourceSpec() *commonv1alpha1.ResourceSpec {
	return &o.Spec.ResourceSpec
}

// GetResourceStatus implements commonv1alpha1.Managed.
func (o *AzureSecretsConfig) GetResourceStatus() *commonv1alpha1.ResourceStatus {
	return &o.Status.ResourceStatus
}

// +kubebuilder:object:root=true

// AzureSecretsConfigList contains a list of AzureSecretsConfig.
type AzureSecretsConfigList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []AzureSecretsConfig `json:"items"`
}

func init() {
	SchemeBuilder.Register(&AzureSecretsConfig{}, &AzureSecretsConfigList{})
}
