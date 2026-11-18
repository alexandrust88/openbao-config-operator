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

// Named CEL expression that later variables and the main expression can use.
type CELVariable struct {
	// Name of the variable, referenced by later variables and the main expression. Letters, digits and underscores, not starting with a digit. Maps to `cel_program.variables[].name`. Example: is_admin
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=128
	// +kubebuilder:validation:Pattern=`^[A-Za-z_][A-Za-z0-9_]*$`
	// +required
	Name string `json:"name"`

	// CEL expression that computes the variable; it can read the JWT claims as claims. Maps to `cel_program.variables[].expression`. Example: "'platform-admins' in claims.groups"
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=16384
	// +required
	Expression string `json:"expression"`
}

// CEL program evaluated over the JWT claims at login.
type CELProgram struct {
	// Variables evaluated in order before the main expression, at most 32. Maps to `cel_program.variables`. Example: [{name: is_admin, expression: "'admins' in claims.groups"}]
	// +kubebuilder:validation:MaxItems=32
	// +listType=atomic
	// +optional
	Variables []CELVariable `json:"variables,omitempty"`

	// Main CEL expression, evaluated over the JWT claims. It returns true to allow the login, false or a string message to deny it, or a pb.Auth object that describes the token to issue (policies, TTLs, metadata). Required. Maps to `cel_program.expression`. Example: "claims.sub == 'ci-runner' ? pb.Auth{policies: ['ci-read']} : false"
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=16384
	// +required
	Expression string `json:"expression"`
}

// Desired state of a CEL role of a JWT auth method, written to auth/<mountPath>/cel/role/<name>. OpenBao replaces the whole role on every write, so unset fields are reset to their OpenBao defaults.
//
// +kubebuilder:validation:XValidation:rule="has(self.mountPath) == has(oldSelf.mountPath) && (!has(self.mountPath) || self.mountPath == oldSelf.mountPath)",message="mountPath is immutable"
// +kubebuilder:validation:XValidation:rule="has(self.name) == has(oldSelf.name) && (!has(self.name) || self.name == oldSelf.name)",message="name is immutable"
type JWTCELRoleSpec struct {
	commonv1alpha1.ResourceSpec `json:",inline"`

	// Path of the JWT auth method, without the auth/ prefix. Defaults to jwt. Immutable. Example: jwt
	// +kubebuilder:default=jwt
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=128
	// +kubebuilder:validation:Pattern=`^[A-Za-z0-9_.-]+(/[A-Za-z0-9_.-]+)*$`
	// +optional
	MountPath string `json:"mountPath,omitempty"`

	// Name of the CEL role in OpenBao, used in the auth/<mountPath>/cel/login request. Defaults to metadata.name. Immutable. Example: ci-runner
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=128
	// +kubebuilder:validation:Pattern=`^[a-z0-9_]([a-z0-9_.-]*[a-z0-9_])?$`
	// +optional
	Name string `json:"name,omitempty"`

	// CEL program that authorizes the login and shapes the issued token. OpenBao applies no claim checks beyond signature, leeways and boundAudiences; the program must validate everything else. Required. Maps to `cel_program`. Example: {expression: "claims.sub == 'ci-runner'"}
	// +required
	CELProgram CELProgram `json:"celProgram"`

	// Static error message returned when the program denies a login. Maps to `message`. Example: permission denied
	// +kubebuilder:validation:MaxLength=1024
	// +optional
	Message string `json:"message,omitempty"`

	// Values accepted in the aud claim; any match is enough. Unlike a JWTAuthRole, a JWT without an aud claim is not rejected, so the program must check it when needed. Maps to `bound_audiences`. Example: [openbao]
	// +kubebuilder:validation:MaxItems=64
	// +kubebuilder:validation:items:MaxLength=512
	// +listType=set
	// +optional
	BoundAudiences []string `json:"boundAudiences,omitempty"`

	JWTLeeways `json:",inline"`
}

// Observed state of the CEL role, written by the operator.
type JWTCELRoleStatus struct {
	commonv1alpha1.ResourceStatus `json:",inline"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:categories=obco;obco-auth
// +kubebuilder:printcolumn:name="Ready",type=string,JSONPath=`.status.conditions[?(@.type=="Ready")].status`
// +kubebuilder:printcolumn:name="Synced",type=string,JSONPath=`.status.conditions[?(@.type=="Synced")].status`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// JWTCELRole manages a CEL role of an OpenBao JWT auth method at auth/<mountPath>/cel/role/<name>. OpenBao only. Use it when a CEL program over the JWT claims, rather than fixed bound claims, must decide the login and the issued token.
//
// Example:
//
//	spec:
//	  connectionRef:
//	    name: openbao
//	  mountPath: jwt
//	  boundAudiences: [openbao]
//	  celProgram:
//	    expression: "claims.sub == 'ci-runner' ? pb.Auth{policies: ['ci-read']} : false"
//	  message: permission denied
type JWTCELRole struct {
	metav1.TypeMeta `json:",inline"`

	// +optional
	metav1.ObjectMeta `json:"metadata,omitzero"`

	// +required
	Spec JWTCELRoleSpec `json:"spec"`

	// +optional
	Status JWTCELRoleStatus `json:"status,omitzero"`
}

// GetResourceSpec implements commonv1alpha1.Managed.
func (j *JWTCELRole) GetResourceSpec() *commonv1alpha1.ResourceSpec { return &j.Spec.ResourceSpec }

// GetResourceStatus implements commonv1alpha1.Managed.
func (j *JWTCELRole) GetResourceStatus() *commonv1alpha1.ResourceStatus {
	return &j.Status.ResourceStatus
}

// +kubebuilder:object:root=true

// JWTCELRoleList contains a list of JWTCELRole.
type JWTCELRoleList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []JWTCELRole `json:"items"`
}

func init() {
	SchemeBuilder.Register(&JWTCELRole{}, &JWTCELRoleList{})
}
