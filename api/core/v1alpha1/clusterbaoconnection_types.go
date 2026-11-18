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
)

// ClusterBaoConnectionSpec defines which BaoServer the platform connects to and how it logs in.
type ClusterBaoConnectionSpec struct {
	ConnectionSpec `json:",inline"`
	// How the operator logs in to OpenBao for this connection: set exactly one of token, kubernetes, appRole and jwt. Every reference names its Kubernetes namespace.
	// +required
	Auth ClusterConnectionAuth `json:"auth"`
	// Label selector over Kubernetes namespaces whose resources may reference this connection; a resource in any other namespace gets reason ConnectionDenied. Unset allows no namespace; an empty selector ({}) allows every namespace. Example: {matchLabels: {openbao.io/platform: "true"}}
	// +optional
	AllowedNamespaces *metav1.LabelSelector `json:"allowedNamespaces,omitempty"`
}

// ClusterConnectionAuth selects exactly one login method. Every reference names its namespace.
// +kubebuilder:validation:XValidation:rule="[has(self.token), has(self.kubernetes), has(self.appRole), has(self.jwt)].filter(x, x).size() == 1",message="set exactly one of token, kubernetes, appRole and jwt"
type ClusterConnectionAuth struct {
	// Use an existing OpenBao token from a Secret or a file in the operator pod. Prefer kubernetes or jwt, which need no long-lived credential.
	// +optional
	Token *ClusterTokenAuth `json:"token,omitempty"`
	// Log in to an OpenBao kubernetes auth mount (auth/<mountPath>/login) with a ServiceAccount token: a named ServiceAccount's short-lived token, or the operator's own injected token.
	// +optional
	Kubernetes *ClusterKubernetesAuth `json:"kubernetes,omitempty"`
	// Log in to an OpenBao approle auth mount (auth/<mountPath>/login) with a role ID and secret ID read from Secrets.
	// +optional
	AppRole *ClusterAppRoleAuth `json:"appRole,omitempty"`
	// Log in to an OpenBao jwt auth mount (auth/<mountPath>/login) with a ServiceAccount token: a named ServiceAccount's short-lived token, or the operator's own injected token.
	// +optional
	JWT *ClusterJWTAuth `json:"jwt,omitempty"`
}

// ClusterTokenAuth uses a token stored in a Secret or in a file mounted into the operator pod.
// +kubebuilder:validation:ExactlyOneOf=secretRef;filesystem
type ClusterTokenAuth struct {
	// Secret key that holds the OpenBao token. Read at each login, so rotation needs no restart; never stored in status. Mutually exclusive with filesystem.
	// +optional
	SecretRef *NamespacedSecretKeySelector `json:"secretRef,omitempty"`
	// Read the token from a file in the operator pod, such as an OpenBao agent sink, at every login. A missing file sets reason CredentialsNotFound. Mutually exclusive with secretRef.
	// +optional
	Filesystem *FilesystemTokenSource `json:"filesystem,omitempty"`
}

// FilesystemTokenSource is a token file mounted into the operator pod.
type FilesystemTokenSource struct {
	// Absolute path of the token file inside the operator pod; never in the automounted ServiceAccount token directory. Example: /var/run/openbao/token
	// +kubebuilder:validation:MaxLength=512
	// +kubebuilder:validation:Pattern=`^(/[^/]+)+$`
	// +kubebuilder:validation:XValidation:rule="!(self + '/').startsWith('/var/run/secrets/kubernetes.io/serviceaccount/')",message="the automounted ServiceAccount token is a Kubernetes API credential and is never sent to OpenBao"
	// +required
	Path string `json:"path"`
}

// InjectedIdentity logs in as the operator itself, with the ServiceAccount token the kubelet projects into the operator pod. The file is read at every login, so kubelet rotation needs no restart.
type InjectedIdentity struct {
	// Path of a projected ServiceAccount token file with a dedicated audience in the operator pod, so the token sent to OpenBao is not valid against the Kubernetes API server. The automounted token directory (/var/run/secrets/kubernetes.io/serviceaccount/) is refused. Default: /var/run/secrets/openbao/token, where the chart mounts a token with audience openbao-config-operator.
	// +kubebuilder:default="/var/run/secrets/openbao/token"
	// +kubebuilder:validation:MaxLength=512
	// +kubebuilder:validation:Pattern=`^(/[^/]+)+$`
	// +kubebuilder:validation:XValidation:rule="!(self + '/').startsWith('/var/run/secrets/kubernetes.io/serviceaccount/')",message="the automounted ServiceAccount token is a Kubernetes API credential and is never sent to OpenBao"
	// +optional
	TokenPath string `json:"tokenPath,omitempty"`
}

// ClusterKubernetesAuth logs in to the OpenBao kubernetes auth method with a ServiceAccount token.
// +kubebuilder:validation:AtMostOneOf=serviceAccountRef;injectedIdentity
// +kubebuilder:validation:XValidation:rule="has(self.serviceAccountRef) || !has(self.audiences)",message="audiences require serviceAccountRef: an injected identity token gets its audience from its projected volume"
// +kubebuilder:validation:XValidation:rule="!has(self.serviceAccountRef) || (has(self.audiences) && size(self.audiences) > 0)",message="serviceAccountRef requires audiences: a token without them is valid against the Kubernetes API server"
type ClusterKubernetesAuth struct {
	// Path of the kubernetes auth mount, without the auth/ prefix. Default: kubernetes. Example: kubernetes
	// +kubebuilder:default=kubernetes
	// +kubebuilder:validation:MaxLength=128
	// +kubebuilder:validation:Pattern=`^[A-Za-z0-9_-][A-Za-z0-9._-]*(/[A-Za-z0-9_-][A-Za-z0-9._-]*)*$`
	// +optional
	MountPath string `json:"mountPath,omitempty"`
	// Name of the OpenBao kubernetes auth role to log in with (the login `role` parameter). Example: openbao-config-operator
	// +kubebuilder:validation:MinLength=1
	// +required
	Role string `json:"role"`
	// ServiceAccount, in any namespace, whose short-lived token (TokenRequest) is presented. Mutually exclusive with injectedIdentity.
	// +optional
	ServiceAccountRef *NamespacedObjectReference `json:"serviceAccountRef,omitempty"`
	// Present the operator's own ServiceAccount token from a file in its pod. When neither serviceAccountRef nor injectedIdentity is set, injectedIdentity with the default tokenPath is used. Mutually exclusive with serviceAccountRef.
	// +optional
	InjectedIdentity *InjectedIdentity `json:"injectedIdentity,omitempty"`
	// Audiences of the token requested for serviceAccountRef, required with it and only allowed with it; they must match the OpenBao role's audience. An audience the Kubernetes API server accepts is refused. At most 16 entries. Example: ["openbao"]
	// +kubebuilder:validation:MaxItems=16
	// +kubebuilder:validation:items:MinLength=1
	// +kubebuilder:validation:items:MaxLength=256
	// +optional
	Audiences []string `json:"audiences,omitempty"`
}

// ClusterJWTAuth logs in to the OpenBao jwt auth method with a ServiceAccount token.
// +kubebuilder:validation:AtMostOneOf=serviceAccountRef;injectedIdentity
// +kubebuilder:validation:XValidation:rule="has(self.serviceAccountRef) || !has(self.audiences)",message="audiences require serviceAccountRef: an injected identity token gets its audience from its projected volume"
// +kubebuilder:validation:XValidation:rule="!has(self.serviceAccountRef) || (has(self.audiences) && size(self.audiences) > 0)",message="serviceAccountRef requires audiences: a token without them is valid against the Kubernetes API server"
type ClusterJWTAuth struct {
	// Path of the jwt auth mount, without the auth/ prefix. Default: jwt. Example: jwt
	// +kubebuilder:default=jwt
	// +kubebuilder:validation:MaxLength=128
	// +kubebuilder:validation:Pattern=`^[A-Za-z0-9_-][A-Za-z0-9._-]*(/[A-Za-z0-9_-][A-Za-z0-9._-]*)*$`
	// +optional
	MountPath string `json:"mountPath,omitempty"`
	// Name of the OpenBao jwt auth role to log in with (the login `role` parameter). Example: openbao-config-operator
	// +kubebuilder:validation:MinLength=1
	// +required
	Role string `json:"role"`
	// ServiceAccount, in any namespace, whose short-lived token (TokenRequest) is presented as the JWT. Mutually exclusive with injectedIdentity.
	// +optional
	ServiceAccountRef *NamespacedObjectReference `json:"serviceAccountRef,omitempty"`
	// Present the operator's own ServiceAccount token from a file in its pod. When neither serviceAccountRef nor injectedIdentity is set, injectedIdentity with the default tokenPath is used. Mutually exclusive with serviceAccountRef.
	// +optional
	InjectedIdentity *InjectedIdentity `json:"injectedIdentity,omitempty"`
	// Audiences of the token requested for serviceAccountRef, required with it and only allowed with it; they must match the jwt role's bound_audiences. An audience the Kubernetes API server accepts is refused. At most 16 entries. Example: ["openbao"]
	// +kubebuilder:validation:MaxItems=16
	// +kubebuilder:validation:items:MinLength=1
	// +kubebuilder:validation:items:MaxLength=256
	// +optional
	Audiences []string `json:"audiences,omitempty"`
}

// ClusterAppRoleAuth logs in to the OpenBao approle auth method.
type ClusterAppRoleAuth struct {
	// Path of the approle auth mount, without the auth/ prefix. Default: approle. Example: approle
	// +kubebuilder:default=approle
	// +kubebuilder:validation:MaxLength=128
	// +kubebuilder:validation:Pattern=`^[A-Za-z0-9_-][A-Za-z0-9._-]*(/[A-Za-z0-9_-][A-Za-z0-9._-]*)*$`
	// +optional
	MountPath string `json:"mountPath,omitempty"`
	// Secret key that holds the AppRole role ID (the login `role_id` parameter). Read at each login; never stored in status.
	// +required
	RoleIDSecretRef NamespacedSecretKeySelector `json:"roleIDSecretRef"`
	// Secret key that holds the AppRole secret ID (the login `secret_id` parameter). Read at each login, so rotation needs no restart; never stored in status.
	// +required
	SecretIDSecretRef NamespacedSecretKeySelector `json:"secretIDSecretRef"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Cluster,shortName=cbaoconn,categories=obco;obco-core
// +kubebuilder:printcolumn:name="Ready",type=string,JSONPath=`.status.conditions[?(@.type=="Ready")].status`
// +kubebuilder:printcolumn:name="Server",type=string,JSONPath=`.spec.serverRef.name`
// +kubebuilder:printcolumn:name="Version",type=string,JSONPath=`.status.version`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// ClusterBaoConnection is a cluster-scoped login to a BaoServer owned by the platform team, usable by resources in every namespace its allowedNamespaces selects (spec.connectionRef.kind: ClusterBaoConnection). Unlike BaoConnection it may log in as the operator itself (injected identity or a token file). It writes nothing to OpenBao: the operator logs in once, caches and renews the token, and reports Ready when login and auth/token/lookup-self succeed.
//
// Example:
//
//	spec:
//	  serverRef:
//	    name: openbao
//	  auth:
//	    kubernetes:
//	      role: openbao-config-operator
//	      injectedIdentity:
//	        tokenPath: /var/run/secrets/openbao/token
//	  allowedNamespaces:
//	    matchLabels:
//	      openbao.io/platform: "true"
type ClusterBaoConnection struct {
	metav1.TypeMeta `json:",inline"`

	// +optional
	metav1.ObjectMeta `json:"metadata,omitzero"`

	// Desired state of the connection: which server, which OpenBao namespace, how to log in and which Kubernetes namespaces may use it.
	// +required
	Spec ClusterBaoConnectionSpec `json:"spec"`

	// Observed state of the connection: server health copied from the BaoServer and the login result.
	// +optional
	Status ConnectionStatus `json:"status,omitzero"`
}

// +kubebuilder:object:root=true

// ClusterBaoConnectionList contains a list of ClusterBaoConnection
type ClusterBaoConnectionList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []ClusterBaoConnection `json:"items"`
}

func init() {
	SchemeBuilder.Register(&ClusterBaoConnection{}, &ClusterBaoConnectionList{})
}
