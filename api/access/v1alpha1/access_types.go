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

const (
	// InUseFinalizer keeps an AccessLevel while an AccessGrant or AccessLayout cites it.
	InUseFinalizer = "openbaoconfig.io/in-use"
	// AllowDisruptiveUpdateAnnotation set to "true" lets an AccessLayout delete policies and groups it created before.
	AllowDisruptiveUpdateAnnotation = "openbaoconfig.io/allow-disruptive-update"
	// ReasonInUse is the Ready reason of an AccessLevel whose deletion waits for its citers.
	ReasonInUse = "InUse"
)

// An OpenBao ACL policy capability: create, read, update, patch, delete or list.
// +kubebuilder:validation:Enum=create;read;update;patch;delete;list
type AccessCapability string

// A pair of secret paths on a KV version 2 mount: one for secret data and one for secret metadata.
//
// A path is a list of segments separated by "/". A segment is a literal of letters, digits, ".", "_" and "-", or "+" (exactly one segment), or one of the tokens {org}, {sa.namespace} and {sa.name}. The last segment may also be "*" or end with "*" (any suffix). A token always fills a whole segment, and "." and ".." are not segments.
type AccessPath struct {
	// Path of the KV version 2 secrets engine, without leading or trailing slash. Example: secret
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=128
	// +kubebuilder:validation:Pattern=`^[A-Za-z0-9_-]+(/[A-Za-z0-9_-]+)*$`
	// +required
	Mount string `json:"mount"`

	// Secret path below the mount that the level's data capabilities apply to; the policy grants them on <mount>/data/<path>. The level's versions grant applies to the same path under delete/, undelete/ and destroy/. Example: apps/{sa.namespace}/*
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=256
	// +kubebuilder:validation:Pattern=`^((([A-Za-z0-9_-][A-Za-z0-9._-]*|\.[A-Za-z0-9_-][A-Za-z0-9._-]*|\.\.[A-Za-z0-9._-]+)|\+|\{org\}|\{sa\.namespace\}|\{sa\.name\})/)*(([A-Za-z0-9_-][A-Za-z0-9._-]*|\.[A-Za-z0-9_-][A-Za-z0-9._-]*|\.\.[A-Za-z0-9._-]+)\*?|\+|\{org\}|\{sa\.namespace\}|\{sa\.name\}|\*)$`
	// +required
	Path string `json:"path"`

	// Secret path below the mount that the level's metadata capabilities apply to; the policy grants them on <mount>/metadata/<metadataPath>. Same syntax as path. Default: the value of path. Example: apps/*
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=256
	// +kubebuilder:validation:Pattern=`^((([A-Za-z0-9_-][A-Za-z0-9._-]*|\.[A-Za-z0-9_-][A-Za-z0-9._-]*|\.\.[A-Za-z0-9._-]+)|\+|\{org\}|\{sa\.namespace\}|\{sa\.name\})/)*(([A-Za-z0-9_-][A-Za-z0-9._-]*|\.[A-Za-z0-9_-][A-Za-z0-9._-]*|\.\.[A-Za-z0-9._-]+)\*?|\+|\{org\}|\{sa\.namespace\}|\{sa\.name\}|\*)$`
	// +optional
	MetadataPath string `json:"metadataPath,omitempty"`
}

// AccessChildKind is the kind of an OpenBao object written by an AccessGrant or AccessLayout.
// +kubebuilder:validation:Enum=Policy;KubernetesAuthRole;Group
type AccessChildKind string

const (
	AccessChildPolicy             AccessChildKind = "Policy"
	AccessChildKubernetesAuthRole AccessChildKind = "KubernetesAuthRole"
	AccessChildGroup              AccessChildKind = "Group"
)

// One OpenBao object created and owned by this resource; with deletionPolicy Delete it is removed when the resource is deleted.
type AccessChild struct {
	// Kind of the OpenBao object: Policy (an ACL policy), KubernetesAuthRole (a Kubernetes auth role) or Group (an external identity group, together with its group alias).
	// +required
	Kind AccessChildKind `json:"kind"`

	// OpenBao namespace of the object, relative to status.namespace; empty means status.namespace itself. Example: team-a
	// +optional
	Namespace string `json:"namespace,omitempty"`

	// OpenBao API path of the object in its namespace. Example: sys/policies/acl/grant_apps_reader
	// +required
	Path string `json:"path"`
}
