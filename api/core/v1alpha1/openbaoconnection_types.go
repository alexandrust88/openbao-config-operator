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

// OpenBaoConnectionSpec defines which OpenBaoServer a tenant connects to and how it logs in. The server must allow this namespace, and every requested token audience must be in the server's allowedAudiences.
type OpenBaoConnectionSpec struct {
	ConnectionSpec `json:",inline"`
	// How the operator logs in to OpenBao for this connection: set exactly one of token, kubernetes, appRole and jwt. Every referenced Secret or ServiceAccount is in this connection's namespace.
	// +required
	Auth ConnectionAuth `json:"auth"`
}

// ConnectionAuth selects exactly one login method. Every reference is in the connection's namespace.
// +kubebuilder:validation:XValidation:rule="[has(self.token), has(self.kubernetes), has(self.appRole), has(self.jwt)].filter(x, x).size() == 1",message="set exactly one of token, kubernetes, appRole and jwt"
type ConnectionAuth struct {
	// Use an existing OpenBao token stored in a Secret. Prefer kubernetes or jwt, which need no long-lived credential.
	// +optional
	Token *TokenAuth `json:"token,omitempty"`
	// Log in to an OpenBao kubernetes auth mount (auth/<mountPath>/login) with a short-lived token of a ServiceAccount in this namespace.
	// +optional
	Kubernetes *KubernetesAuth `json:"kubernetes,omitempty"`
	// Log in to an OpenBao approle auth mount (auth/<mountPath>/login) with a role ID and secret ID read from Secrets.
	// +optional
	AppRole *AppRoleAuth `json:"appRole,omitempty"`
	// Log in to an OpenBao jwt auth mount (auth/<mountPath>/login) with a short-lived token of a ServiceAccount in this namespace.
	// +optional
	JWT *JWTAuth `json:"jwt,omitempty"`
}

// TokenAuth uses a token stored in a Secret.
type TokenAuth struct {
	// Secret key that holds the OpenBao token. Read at each login, so rotation needs no restart; never stored in status.
	// +required
	SecretRef commonv1alpha1.SecretKeySelector `json:"secretRef"`
}

// KubernetesAuth logs in to the OpenBao kubernetes auth method with a TokenRequest token of a ServiceAccount.
// +kubebuilder:validation:XValidation:rule="has(self.serviceAccountRef)",message="serviceAccountRef is required: a namespaced connection never logs in as the operator"
// +kubebuilder:validation:XValidation:rule="has(self.audiences) && size(self.audiences) > 0",message="audiences is required: a token for the API server audience is a Kubernetes credential and must not be sent to OpenBao"
type KubernetesAuth struct {
	// Path of the kubernetes auth mount, without the auth/ prefix. Default: kubernetes. Example: kubernetes-tenants
	// +kubebuilder:default=kubernetes
	// +kubebuilder:validation:MaxLength=128
	// +kubebuilder:validation:Pattern=`^[A-Za-z0-9_-][A-Za-z0-9._-]*(/[A-Za-z0-9_-][A-Za-z0-9._-]*)*$`
	// +optional
	MountPath string `json:"mountPath,omitempty"`
	// Name of the OpenBao kubernetes auth role to log in with (the login `role` parameter). Example: team-a-operator
	// +kubebuilder:validation:MinLength=1
	// +required
	Role string `json:"role"`
	// ServiceAccount in this namespace whose short-lived token (TokenRequest) is presented. Required: a namespaced connection never logs in as the operator itself.
	// +optional
	ServiceAccountRef *commonv1alpha1.LocalObjectReference `json:"serviceAccountRef,omitempty"`
	// Audiences of the requested token; they must match the OpenBao role's audience and be listed in the server's allowedAudiences. Required, so the token is never valid against the Kubernetes API server. 1 to 16 entries. Example: ["openbao"]
	// +kubebuilder:validation:MinItems=1
	// +kubebuilder:validation:MaxItems=16
	// +kubebuilder:validation:items:MinLength=1
	// +kubebuilder:validation:items:MaxLength=256
	// +optional
	Audiences []string `json:"audiences,omitempty"`
}

// JWTAuth logs in to the OpenBao jwt auth method with a TokenRequest token of a ServiceAccount.
// +kubebuilder:validation:XValidation:rule="has(self.serviceAccountRef)",message="serviceAccountRef is required: a namespaced connection never logs in as the operator"
// +kubebuilder:validation:XValidation:rule="has(self.audiences) && size(self.audiences) > 0",message="audiences is required: a token for the API server audience is a Kubernetes credential and must not be sent to OpenBao"
type JWTAuth struct {
	// Path of the jwt auth mount, without the auth/ prefix. Default: jwt. Example: jwt-k8s
	// +kubebuilder:default=jwt
	// +kubebuilder:validation:MaxLength=128
	// +kubebuilder:validation:Pattern=`^[A-Za-z0-9_-][A-Za-z0-9._-]*(/[A-Za-z0-9_-][A-Za-z0-9._-]*)*$`
	// +optional
	MountPath string `json:"mountPath,omitempty"`
	// Name of the OpenBao jwt auth role to log in with (the login `role` parameter). Example: team-a-operator
	// +kubebuilder:validation:MinLength=1
	// +required
	Role string `json:"role"`
	// ServiceAccount in this namespace whose short-lived token (TokenRequest) is presented as the JWT. Required: a namespaced connection never logs in as the operator itself.
	// +optional
	ServiceAccountRef *commonv1alpha1.LocalObjectReference `json:"serviceAccountRef,omitempty"`
	// Audiences of the requested token; they must match the jwt role's bound_audiences and be listed in the server's allowedAudiences. Required, so the token is never valid against the Kubernetes API server. 1 to 16 entries. Example: ["openbao"]
	// +kubebuilder:validation:MinItems=1
	// +kubebuilder:validation:MaxItems=16
	// +kubebuilder:validation:items:MinLength=1
	// +kubebuilder:validation:items:MaxLength=256
	// +optional
	Audiences []string `json:"audiences,omitempty"`
}

// AppRoleAuth logs in to the OpenBao approle auth method.
type AppRoleAuth struct {
	// Path of the approle auth mount, without the auth/ prefix. Default: approle. Example: approle
	// +kubebuilder:default=approle
	// +kubebuilder:validation:MaxLength=128
	// +kubebuilder:validation:Pattern=`^[A-Za-z0-9_-][A-Za-z0-9._-]*(/[A-Za-z0-9_-][A-Za-z0-9._-]*)*$`
	// +optional
	MountPath string `json:"mountPath,omitempty"`
	// Secret key that holds the AppRole role ID (the login `role_id` parameter). Read at each login; never stored in status.
	// +required
	RoleIDSecretRef commonv1alpha1.SecretKeySelector `json:"roleIDSecretRef"`
	// Secret key that holds the AppRole secret ID (the login `secret_id` parameter). Read at each login, so rotation needs no restart; never stored in status.
	// +required
	SecretIDSecretRef commonv1alpha1.SecretKeySelector `json:"secretIDSecretRef"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:categories=openbao
// +kubebuilder:printcolumn:name="Ready",type=string,JSONPath=`.status.conditions[?(@.type=="Ready")].status`
// +kubebuilder:printcolumn:name="Server",type=string,JSONPath=`.spec.serverRef.name`
// +kubebuilder:printcolumn:name="Version",type=string,JSONPath=`.status.version`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// OpenBaoConnection is a tenant's login to an OpenBaoServer, usable only by resources in its own Kubernetes namespace through spec.connectionRef. The server must allow this namespace and every requested token audience. It writes nothing to OpenBao: the operator logs in once, caches and renews the token, and reports Ready when login and auth/token/lookup-self succeed. What the connection may configure is bounded by the OpenBao policy of its login role.
//
// Example:
//
//	spec:
//	  serverRef:
//	    name: openbao
//	  namespace: team-a
//	  auth:
//	    kubernetes:
//	      role: team-a-operator
//	      serviceAccountRef:
//	        name: openbao-config
//	      audiences: ["openbao"]
type OpenBaoConnection struct {
	metav1.TypeMeta `json:",inline"`

	// +optional
	metav1.ObjectMeta `json:"metadata,omitzero"`

	// Desired state of the connection: which server, which OpenBao namespace and how to log in.
	// +required
	Spec OpenBaoConnectionSpec `json:"spec"`

	// Observed state of the connection: server health copied from the OpenBaoServer and the login result.
	// +optional
	Status ConnectionStatus `json:"status,omitzero"`
}

// +kubebuilder:object:root=true

// OpenBaoConnectionList contains a list of OpenBaoConnection
type OpenBaoConnectionList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []OpenBaoConnection `json:"items"`
}

func init() {
	SchemeBuilder.Register(&OpenBaoConnection{}, &OpenBaoConnectionList{})
}
