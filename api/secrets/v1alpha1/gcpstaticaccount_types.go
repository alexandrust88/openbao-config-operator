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
// +kubebuilder:validation:XValidation:rule="has(self.name) == has(oldSelf.name) && (!has(self.name) || self.name == oldSelf.name)",message="name is immutable"
// +kubebuilder:validation:XValidation:rule="self.serviceAccountEmail == oldSelf.serviceAccountEmail",message="serviceAccountEmail is immutable"
// +kubebuilder:validation:XValidation:rule="has(self.secretType) == has(oldSelf.secretType) && (!has(self.secretType) || self.secretType == oldSelf.secretType)",message="secretType is immutable"
// +kubebuilder:validation:XValidation:rule="(has(self.secretType) && self.secretType == 'service_account_key') != has(self.tokenScopes)",message="tokenScopes is required for secretType access_token (the default) and not allowed for service_account_key"

// spec is the desired static account of a GCP secrets engine at `<mountPath>/static-account/<name>`, which issues secrets for an existing service account.
type GCPStaticAccountSpec struct {
	commonv1alpha1.ResourceSpec `json:",inline"`

	// mountPath is the path of the GCP secrets engine mount. Defaults to `gcp`. Immutable. Example: `gcp`.
	// +kubebuilder:default=gcp
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=128
	// +kubebuilder:validation:Pattern=`^[A-Za-z0-9_.-]+(/[A-Za-z0-9_.-]+)*$`
	// +optional
	MountPath string `json:"mountPath,omitempty"`

	// name is the static account name in OpenBao. Defaults to metadata.name. Immutable. Example: `ci-deployer`.
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=128
	// +kubebuilder:validation:Pattern=`^[A-Za-z0-9_-]+$`
	// +optional
	Name string `json:"name,omitempty"`

	// secretType is the kind of secret the static account issues (`secret_type`). Allowed: access_token, service_account_key. Defaults to access_token. Immutable.
	// +optional
	SecretType GCPSecretType `json:"secretType,omitempty"`

	// serviceAccountEmail is the email of the existing GCP service account (`service_account_email`). Immutable. Example: `deployer@my-project.iam.gserviceaccount.com`.
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=254
	// +required
	ServiceAccountEmail string `json:"serviceAccountEmail"`

	// bindings are IAM role bindings OpenBao grants to the service account (`bindings`). Without bindings OpenBao manages no IAM policy for it. One entry per resource. Example: `[{resource: projects/my-project, roles: [roles/viewer]}]`.
	// +kubebuilder:validation:MinItems=1
	// +kubebuilder:validation:MaxItems=64
	// +listType=map
	// +listMapKey=resource
	// +optional
	Bindings []GCPBinding `json:"bindings,omitempty"`

	// tokenScopes are the OAuth scopes of issued access tokens (`token_scopes`); required for access_token, not allowed for service_account_key. Example: `[https://www.googleapis.com/auth/cloud-platform]`.
	// +kubebuilder:validation:MaxItems=32
	// +kubebuilder:validation:items:MaxLength=256
	// +listType=set
	// +optional
	TokenScopes []string `json:"tokenScopes,omitempty"`
}

// status is the observed state of the GCP static account.
type GCPStaticAccountStatus struct {
	commonv1alpha1.ResourceStatus `json:",inline"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:categories=obco;obco-secrets
// +kubebuilder:printcolumn:name="Ready",type=string,JSONPath=`.status.conditions[?(@.type=="Ready")].status`
// +kubebuilder:printcolumn:name="Synced",type=string,JSONPath=`.status.conditions[?(@.type=="Synced")].status`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// GCPStaticAccount manages a static account of a GCP secrets engine (external plugin) at `<mount>/static-account/<name>`, which issues access tokens or keys for an existing service account instead of creating one.
//
// Example:
//
//	spec:
//	  connectionRef:
//	    name: openbao
//	  mountPath: gcp
//	  name: ci-deployer
//	  serviceAccountEmail: deployer@my-project.iam.gserviceaccount.com
//	  tokenScopes: [https://www.googleapis.com/auth/cloud-platform]
type GCPStaticAccount struct {
	metav1.TypeMeta `json:",inline"`

	// +optional
	metav1.ObjectMeta `json:"metadata,omitzero"`

	// +required
	Spec GCPStaticAccountSpec `json:"spec"`

	// +optional
	Status GCPStaticAccountStatus `json:"status,omitzero"`
}

// GetResourceSpec implements commonv1alpha1.Managed.
func (o *GCPStaticAccount) GetResourceSpec() *commonv1alpha1.ResourceSpec {
	return &o.Spec.ResourceSpec
}

// GetResourceStatus implements commonv1alpha1.Managed.
func (o *GCPStaticAccount) GetResourceStatus() *commonv1alpha1.ResourceStatus {
	return &o.Status.ResourceStatus
}

// +kubebuilder:object:root=true

// GCPStaticAccountList contains a list of GCPStaticAccount.
type GCPStaticAccountList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []GCPStaticAccount `json:"items"`
}

func init() {
	SchemeBuilder.Register(&GCPStaticAccount{}, &GCPStaticAccountList{})
}
