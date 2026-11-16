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

// PKICELVariable is a named CEL expression that later variables and the main expression can use.
type PKICELVariable struct {
	// name of the variable, referenced by later variables and the main expression. Maps to `cel_program.variables[].name`. Example: is_wildcard
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=128
	// +kubebuilder:validation:Pattern=`^[A-Za-z_][A-Za-z0-9_]*$`
	// +required
	Name string `json:"name"`

	// CEL expression that computes the variable. Maps to `cel_program.variables[].expression`. Example: `request.common_name.endsWith(".example.com")`
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=2048
	// +required
	Expression string `json:"expression"`
}

// PKICELProgram is a CEL program evaluated when a certificate is issued through the role.
type PKICELProgram struct {
	// variables evaluated in order before the main expression, at most 32. Maps to `cel_program.variables`. Example: [{name: is_wildcard, expression: `request.common_name.startsWith("*.")`}]
	// +kubebuilder:validation:MaxItems=32
	// +listType=atomic
	// +optional
	Variables []PKICELVariable `json:"variables,omitempty"`

	// main CEL expression for certificate policy. Maps to `cel_program.expression`. Example: `ValidationOutput{template: CertTemplate{Subject: PKIX.Name{CommonName: request.common_name}}}`
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=8192
	// +required
	Expression string `json:"expression"`
}

// spec is the desired CEL role of a PKI secrets engine at `<mountPath>/cel/roles/<name>`.
//
// +kubebuilder:validation:XValidation:rule="has(self.mountPath) == has(oldSelf.mountPath) && (!has(self.mountPath) || self.mountPath == oldSelf.mountPath)",message="mountPath is immutable"
// +kubebuilder:validation:XValidation:rule="has(self.name) == has(oldSelf.name) && (!has(self.name) || self.name == oldSelf.name)",message="name is immutable"
type PKICELRoleSpec struct {
	commonv1alpha1.ResourceSpec `json:",inline"`

	// mountPath is the path of the PKI secrets engine mount. Defaults to `pki`. Immutable. Example: `pki-int`.
	// +kubebuilder:default=pki
	// +kubebuilder:validation:MaxLength=128
	// +kubebuilder:validation:Pattern=`^[A-Za-z0-9_.-]+(/[A-Za-z0-9_.-]+)*$`
	// +optional
	MountPath string `json:"mountPath,omitempty"`

	// name is the CEL role name in OpenBao. Defaults to metadata.name. Immutable. Example: `strict-cn`.
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=128
	// +kubebuilder:validation:Pattern=`^[A-Za-z0-9_]([A-Za-z0-9_.-]*[A-Za-z0-9_])?$`
	// +optional
	Name string `json:"name,omitempty"`

	// celProgram is the CEL program that authorizes certificate issuance. Required. Maps to `cel_program`. A successful expression returns ValidationOutput; a string or boolean result denies issuance.
	// +required
	CELProgram PKICELProgram `json:"celProgram"`
}

// status is the observed state of the CEL role.
type PKICELRoleStatus struct {
	commonv1alpha1.ResourceStatus `json:",inline"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:categories=openbao
// +kubebuilder:printcolumn:name="Ready",type=string,JSONPath=`.status.conditions[?(@.type=="Ready")].status`
// +kubebuilder:printcolumn:name="Synced",type=string,JSONPath=`.status.conditions[?(@.type=="Synced")].status`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// PKICELRole manages a CEL-backed PKI role at `<mountPath>/cel/roles/<name>`. OpenBao only.
//
// Example:
//
//	spec:
//	  connectionRef:
//	    name: openbao
//	  mountPath: pki
//	  celProgram:
//	    expression: 'ValidationOutput{template: CertTemplate{Subject: PKIX.Name{CommonName: request.common_name}}}'
type PKICELRole struct {
	metav1.TypeMeta `json:",inline"`

	// +optional
	metav1.ObjectMeta `json:"metadata,omitzero"`

	// +required
	Spec PKICELRoleSpec `json:"spec"`

	// +optional
	Status PKICELRoleStatus `json:"status,omitzero"`
}

// GetResourceSpec implements commonv1alpha1.Managed.
func (r *PKICELRole) GetResourceSpec() *commonv1alpha1.ResourceSpec { return &r.Spec.ResourceSpec }

// GetResourceStatus implements commonv1alpha1.Managed.
func (r *PKICELRole) GetResourceStatus() *commonv1alpha1.ResourceStatus {
	return &r.Status.ResourceStatus
}

// +kubebuilder:object:root=true

// PKICELRoleList contains a list of PKICELRole.
type PKICELRoleList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []PKICELRole `json:"items"`
}

func init() {
	SchemeBuilder.Register(&PKICELRole{}, &PKICELRoleList{})
}
