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

// The kind of secret a GCP roleset or static account issues.
// +kubebuilder:validation:Enum=access_token;service_account_key
type GCPSecretType string

// IAM roles granted on one GCP resource.
type GCPBinding struct {
	// resource is the full resource name, or a relative name such as `projects/my-project/topics/my-topic`. Example: `//cloudresourcemanager.googleapis.com/projects/my-project`.
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=1024
	// +required
	Resource string `json:"resource"`

	// roles are the IAM roles granted on the resource. At least one. Example: `[roles/viewer]`.
	// +kubebuilder:validation:MinItems=1
	// +kubebuilder:validation:MaxItems=64
	// +kubebuilder:validation:items:MaxLength=256
	// +listType=set
	// +required
	Roles []string `json:"roles"`
}

// +kubebuilder:validation:XValidation:rule="has(self.mountPath) == has(oldSelf.mountPath) && (!has(self.mountPath) || self.mountPath == oldSelf.mountPath)",message="mountPath is immutable"
// +kubebuilder:validation:XValidation:rule="has(self.name) == has(oldSelf.name) && (!has(self.name) || self.name == oldSelf.name)",message="name is immutable"
// +kubebuilder:validation:XValidation:rule="self.project == oldSelf.project",message="project is immutable"
// +kubebuilder:validation:XValidation:rule="has(self.secretType) == has(oldSelf.secretType) && (!has(self.secretType) || self.secretType == oldSelf.secretType)",message="secretType is immutable"
// +kubebuilder:validation:XValidation:rule="(has(self.secretType) && self.secretType == 'service_account_key') != has(self.tokenScopes)",message="tokenScopes is required for secretType access_token (the default) and not allowed for service_account_key"

// spec is the desired roleset of a GCP secrets engine at `<mountPath>/roleset/<name>`. OpenBao creates a service account for the roleset and grants it the bindings.
type GCPRolesetSpec struct {
	commonv1alpha1.ResourceSpec `json:",inline"`

	// mountPath is the path of the GCP secrets engine mount. Defaults to `gcp`. Immutable. Example: `gcp`.
	// +kubebuilder:default=gcp
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=128
	// +kubebuilder:validation:Pattern=`^[A-Za-z0-9_.-]+(/[A-Za-z0-9_.-]+)*$`
	// +optional
	MountPath string `json:"mountPath,omitempty"`

	// name is the roleset name in OpenBao. Defaults to metadata.name. Immutable. Example: `bq-reader`.
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=128
	// +kubebuilder:validation:Pattern=`^[A-Za-z0-9_-]+$`
	// +optional
	Name string `json:"name,omitempty"`

	// secretType is the kind of secret the roleset issues (`secret_type`). Allowed: access_token, service_account_key. Defaults to access_token. Immutable.
	// +optional
	SecretType GCPSecretType `json:"secretType,omitempty"`

	// project is the GCP project of the roleset's service account (`project`). Immutable. Example: `my-project`.
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=128
	// +required
	Project string `json:"project"`

	// bindings are the IAM role bindings granted to the roleset's service account (`bindings`). One entry per resource; at least one. Example: `[{resource: projects/my-project, roles: [roles/viewer]}]`.
	// +kubebuilder:validation:MinItems=1
	// +kubebuilder:validation:MaxItems=64
	// +listType=map
	// +listMapKey=resource
	// +required
	Bindings []GCPBinding `json:"bindings"`

	// tokenScopes are the OAuth scopes of issued access tokens (`token_scopes`); required for access_token, not allowed for service_account_key. Example: `[https://www.googleapis.com/auth/cloud-platform]`.
	// +kubebuilder:validation:MaxItems=32
	// +kubebuilder:validation:items:MaxLength=256
	// +listType=set
	// +optional
	TokenScopes []string `json:"tokenScopes,omitempty"`
}

// status is the observed state of the roleset; `id` holds the email of the service account OpenBao created.
type GCPRolesetStatus struct {
	commonv1alpha1.ResourceStatus `json:",inline"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:categories=obco;obco-secrets
// +kubebuilder:printcolumn:name="Ready",type=string,JSONPath=`.status.conditions[?(@.type=="Ready")].status`
// +kubebuilder:printcolumn:name="Synced",type=string,JSONPath=`.status.conditions[?(@.type=="Synced")].status`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// GCPRoleset manages a roleset of a GCP secrets engine (external plugin) at `<mount>/roleset/<name>`: OpenBao creates a service account with the given IAM bindings and issues access tokens or keys for it.
//
// Example:
//
//	spec:
//	  connectionRef:
//	    name: openbao
//	  mountPath: gcp
//	  name: bq-reader
//	  project: my-project
//	  tokenScopes: [https://www.googleapis.com/auth/cloud-platform]
//	  bindings:
//	    - resource: //cloudresourcemanager.googleapis.com/projects/my-project
//	      roles: [roles/bigquery.dataViewer]
type GCPRoleset struct {
	metav1.TypeMeta `json:",inline"`

	// +optional
	metav1.ObjectMeta `json:"metadata,omitzero"`

	// +required
	Spec GCPRolesetSpec `json:"spec"`

	// +optional
	Status GCPRolesetStatus `json:"status,omitzero"`
}

// GetResourceSpec implements commonv1alpha1.Managed.
func (o *GCPRoleset) GetResourceSpec() *commonv1alpha1.ResourceSpec { return &o.Spec.ResourceSpec }

// GetResourceStatus implements commonv1alpha1.Managed.
func (o *GCPRoleset) GetResourceStatus() *commonv1alpha1.ResourceStatus {
	return &o.Status.ResourceStatus
}

// +kubebuilder:object:root=true

// GCPRolesetList contains a list of GCPRoleset.
type GCPRolesetList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []GCPRoleset `json:"items"`
}

func init() {
	SchemeBuilder.Register(&GCPRoleset{}, &GCPRolesetList{})
}
