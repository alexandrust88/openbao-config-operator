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
// +kubebuilder:validation:XValidation:rule="!has(self.deletionPolicy) || self.deletionPolicy == 'Orphan'",message="deletionPolicy must be Orphan: an auth method config has no delete operation"
// +kubebuilder:validation:XValidation:rule="!has(self.clientSecretSecretRef) || has(self.clientID)",message="clientSecretSecretRef requires clientID"

// Desired configuration of an Azure auth method, written to auth/<mountPath>/config. clientSecretSecretRef requires clientID; deletionPolicy must be Orphan.
type AzureAuthConfigSpec struct {
	commonv1alpha1.ResourceSpec `json:",inline"`

	// Path of the Azure auth method, without the auth/ prefix. Defaults to azure. Immutable. Example: azure
	// +kubebuilder:default=azure
	// +kubebuilder:validation:MaxLength=128
	// +kubebuilder:validation:Pattern=`^[A-Za-z0-9_.-]+(/[A-Za-z0-9_.-]+)*$`
	// +optional
	MountPath string `json:"mountPath,omitempty"`

	// ID of the Microsoft Entra ID (Azure AD) tenant whose identities log in. Required. Maps to `tenant_id`. Example: 00000000-0000-0000-0000-000000000000
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=64
	// +required
	TenantID string `json:"tenantID"`

	// Application ID URI that login tokens must carry as their audience. Required. Maps to `resource`. Example: https://management.azure.com/
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=2048
	// +required
	Resource string `json:"resource"`

	// Azure cloud: AzurePublicCloud, AzureUSGovernmentCloud, AzureChinaCloud or AzureGermanCloud. OpenBao defaults to AzurePublicCloud. Maps to `environment`. Example: AzureUSGovernmentCloud
	// +kubebuilder:validation:Enum=AzurePublicCloud;AzureUSGovernmentCloud;AzureChinaCloud;AzureGermanCloud
	// +optional
	Environment string `json:"environment,omitempty"`

	// Client ID of the application OpenBao uses to query Azure APIs, for example to check virtual machine and scale set bindings. Unset makes OpenBao use its managed identity. Maps to `client_id`. Example: 11111111-1111-1111-1111-111111111111
	// +kubebuilder:validation:MaxLength=64
	// +optional
	ClientID string `json:"clientID,omitempty"`

	// Secret key holding the client secret of the application; requires clientID. Read from this Secret key; the value is never stored in status. Write-only: OpenBao never returns it, so only a hash of it is tracked and a change of the Secret triggers a rewrite. Maps to `client_secret`. Example: {name: azure-auth, key: client-secret}
	// +optional
	ClientSecretSecretRef *commonv1alpha1.SecretKeySelector `json:"clientSecretSecretRef,omitempty"`

	// Maximum number of retries of a failed Azure API call. OpenBao defaults to 3. Maps to `max_retries`. Example: 5
	// +kubebuilder:validation:Minimum=0
	// +optional
	MaxRetries *int32 `json:"maxRetries,omitempty"`

	// Initial delay before retrying a failed Azure API call, as a duration; it grows exponentially. OpenBao defaults to 4s. Maps to `retry_delay`. Example: 2s
	// +optional
	RetryDelay *metav1.Duration `json:"retryDelay,omitempty"`

	// Maximum delay between retries of a failed Azure API call, as a duration. OpenBao defaults to 60s. Maps to `max_retry_delay`. Example: 30s
	// +optional
	MaxRetryDelay *metav1.Duration `json:"maxRetryDelay,omitempty"`
}

// Observed state of the configuration, written by the operator.
type AzureAuthConfigStatus struct {
	commonv1alpha1.ResourceStatus `json:",inline"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:categories=openbao
// +kubebuilder:printcolumn:name="Ready",type=string,JSONPath=`.status.conditions[?(@.type=="Ready")].status`
// +kubebuilder:printcolumn:name="Synced",type=string,JSONPath=`.status.conditions[?(@.type=="Synced")].status`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// AzureAuthConfig manages the configuration of an OpenBao Azure auth method at auth/<mountPath>/config. Use it to set the Microsoft Entra ID tenant and audience that Azure managed identity logins are checked against, and the credentials OpenBao uses to query Azure. The Azure auth plugin must be enabled at mountPath (AuthMount). Deleting it leaves the settings in OpenBao.
//
// Example:
//
//	spec:
//	  connectionRef:
//	    name: openbao
//	  mountPath: azure
//	  tenantID: 00000000-0000-0000-0000-000000000000
//	  resource: https://management.azure.com/
//	  clientID: 11111111-1111-1111-1111-111111111111
//	  clientSecretSecretRef: {name: azure-auth, key: client-secret}
type AzureAuthConfig struct {
	metav1.TypeMeta `json:",inline"`

	// +optional
	metav1.ObjectMeta `json:"metadata,omitzero"`

	// +required
	Spec AzureAuthConfigSpec `json:"spec"`

	// +optional
	Status AzureAuthConfigStatus `json:"status,omitzero"`
}

// GetResourceSpec implements commonv1alpha1.Managed.
func (o *AzureAuthConfig) GetResourceSpec() *commonv1alpha1.ResourceSpec { return &o.Spec.ResourceSpec }

// GetResourceStatus implements commonv1alpha1.Managed.
func (o *AzureAuthConfig) GetResourceStatus() *commonv1alpha1.ResourceStatus {
	return &o.Status.ResourceStatus
}

// +kubebuilder:object:root=true

// AzureAuthConfigList contains a list of AzureAuthConfig.
type AzureAuthConfigList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []AzureAuthConfig `json:"items"`
}

func init() {
	SchemeBuilder.Register(&AzureAuthConfig{}, &AzureAuthConfigList{})
}
