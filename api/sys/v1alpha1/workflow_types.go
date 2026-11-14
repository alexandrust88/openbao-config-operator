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

// +kubebuilder:validation:XValidation:rule="has(self.path) == has(oldSelf.path) && (!has(self.path) || self.path == oldSelf.path)",message="path is immutable"

// WorkflowSpec defines an OpenBao workflow at sys/workflows/manage/<path>: a declarative sequence of API requests (a profile) that callers run through sys/workflows/execute/<path>. Workflows exist only in OpenBao; on HashiCorp Vault the resource reports that the server does not support it.
type WorkflowSpec struct {
	commonv1alpha1.ResourceSpec `json:",inline"`

	// Path of the workflow, the <path> in sys/workflows/manage/<path>. Default: metadata.name. Immutable. Example: team/generate-password
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=256
	// +kubebuilder:validation:Pattern=`^[A-Za-z0-9_.-]+(/[A-Za-z0-9_.-]+)*$`
	// +optional
	Path string `json:"path,omitempty"`

	// Human-readable description of the workflow. Maps to `description`. Example: Generates a password and stores it in KV.
	// +kubebuilder:validation:MaxLength=1024
	// +optional
	Description string `json:"description,omitempty"`

	// Workflow definition in HCL or JSON: optional input and output blocks and one or more flow blocks of request blocks. Maps to `workflow`. Changes in whitespace only are not written. Example: flow "f" { request "r" { operation = "read" path = "sys/health" } }
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=262144
	// +required
	Workflow string `json:"workflow"`

	// Whether every change of the workflow must pass the current version as check-and-set value; the operator sends it on each update. Maps to `cas_required`. Default: false. Example: true
	// +optional
	CASRequired bool `json:"casRequired,omitempty"`

	// Whether callers may run the workflow without a token through sys/workflows/unauthed-execute/<path>. Use with care. Maps to `allow_unauthenticated`. Default: false. Example: false
	// +optional
	AllowUnauthenticated bool `json:"allowUnauthenticated,omitempty"`
}

// WorkflowStatus is the observed state of a Workflow.
type WorkflowStatus struct {
	commonv1alpha1.ResourceStatus `json:",inline"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:categories=openbao
// +kubebuilder:printcolumn:name="Ready",type=string,JSONPath=`.status.conditions[?(@.type=="Ready")].status`
// +kubebuilder:printcolumn:name="Synced",type=string,JSONPath=`.status.conditions[?(@.type=="Synced")].status`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// Workflow manages an OpenBao workflow at sys/workflows/manage/<path>: a declarative sequence of API requests (a profile) that callers run through sys/workflows/execute/<path>. deletionPolicy defaults to Orphan; Delete removes the workflow.
//
// Example:
//
//	spec:
//	  connectionRef:
//	    name: openbao
//	  path: team/health
//	  workflow: |
//	    flow "check" {
//	      request "health" {
//	        operation = "read"
//	        path      = "sys/health"
//	      }
//	    }
type Workflow struct {
	metav1.TypeMeta `json:",inline"`

	// +optional
	metav1.ObjectMeta `json:"metadata,omitzero"`

	// Desired state of the OpenBao workflow.
	// +required
	Spec WorkflowSpec `json:"spec"`

	// Observed state reported by the operator: conditions, OpenBao path and ownership.
	// +optional
	Status WorkflowStatus `json:"status,omitzero"`
}

// GetResourceSpec implements commonv1alpha1.Managed.
func (w *Workflow) GetResourceSpec() *commonv1alpha1.ResourceSpec { return &w.Spec.ResourceSpec }

// GetResourceStatus implements commonv1alpha1.Managed.
func (w *Workflow) GetResourceStatus() *commonv1alpha1.ResourceStatus {
	return &w.Status.ResourceStatus
}

// +kubebuilder:object:root=true

// WorkflowList contains a list of Workflow.
type WorkflowList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []Workflow `json:"items"`
}

func init() {
	SchemeBuilder.Register(&Workflow{}, &WorkflowList{})
}
