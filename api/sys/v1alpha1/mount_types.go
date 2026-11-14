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

// ListingVisibility controls whether a mount is listed in the unauthenticated UI mount listing.
// +kubebuilder:validation:Enum=unauth;hidden
type ListingVisibility string

const (
	ListingVisibilityUnauth ListingVisibility = "unauth"
	ListingVisibilityHidden ListingVisibility = "hidden"
)

// Value of one plugin-specific mount option, as a string. Example: "2"
// +kubebuilder:validation:MaxLength=1024
type MountOption string

// MountFields are the settings shared by auth method and secrets engine mounts.
//
// +kubebuilder:validation:XValidation:rule="has(self.local) == has(oldSelf.local) && (!has(self.local) || self.local == oldSelf.local)",message="local is immutable"
// +kubebuilder:validation:XValidation:rule="has(self.sealWrap) == has(oldSelf.sealWrap) && (!has(self.sealWrap) || self.sealWrap == oldSelf.sealWrap)",message="sealWrap is immutable"
// +kubebuilder:validation:XValidation:rule="has(self.externalEntropyAccess) == has(oldSelf.externalEntropyAccess) && (!has(self.externalEntropyAccess) || self.externalEntropyAccess == oldSelf.externalEntropyAccess)",message="externalEntropyAccess is immutable"
// +kubebuilder:validation:XValidation:rule="has(self.forceNoCache) == has(oldSelf.forceNoCache) && (!has(self.forceNoCache) || self.forceNoCache == oldSelf.forceNoCache)",message="forceNoCache is immutable"
// +kubebuilder:validation:XValidation:rule="has(self.pluginRef) == has(oldSelf.pluginRef) && (!has(self.pluginRef) || self.pluginRef == oldSelf.pluginRef)",message="pluginRef is immutable"
// +kubebuilder:validation:XValidation:rule="!has(self.defaultLeaseTTL) || !has(self.maxLeaseTTL) || duration(self.maxLeaseTTL) == duration('0s') || duration(self.defaultLeaseTTL) <= duration(self.maxLeaseTTL)",message="defaultLeaseTTL must not exceed maxLeaseTTL"
type MountFields struct {
	// Human-readable description shown in the mount table. Maps to `description`. Example: Team A secrets
	// +kubebuilder:validation:MaxLength=1024
	// +optional
	Description *string `json:"description,omitempty"`

	// Marks the mount as local to this server cluster. Maps to `local`. Default: false. Immutable: it cannot be added, changed or removed after creation.
	// +optional
	Local *bool `json:"local,omitempty"`

	// Enables seal wrapping of the mount's storage. Maps to `seal_wrap`. Default: false. Immutable: it cannot be added, changed or removed after creation.
	// +optional
	SealWrap *bool `json:"sealWrap,omitempty"`

	// Gives the mount access to the server's external entropy source. Maps to `external_entropy_access`. Default: false. Immutable: it cannot be added, changed or removed after creation.
	// +optional
	ExternalEntropyAccess *bool `json:"externalEntropyAccess,omitempty"`

	// Disables caching of the mount's storage reads. Maps to `force_no_cache`. Default: false. Immutable: it cannot be added, changed or removed after creation.
	// +optional
	ForceNoCache *bool `json:"forceNoCache,omitempty"`

	// Plugin version the mount runs, for a registered versioned plugin; empty uses the built-in or unversioned plugin. Maps to `plugin_version`. Example: v0.20.0
	// +kubebuilder:validation:MaxLength=128
	// +optional
	PluginVersion string `json:"pluginVersion,omitempty"`

	// Plugin in this namespace that registers the external plugin this mount runs. The mount waits until the Plugin is Ready, takes its version when pluginVersion is unset, and requires type to be the Plugin's catalog name. Immutable. Example: {name: openbao-plugin-auth-ldap}
	// +optional
	PluginRef *commonv1alpha1.LocalObjectReference `json:"pluginRef,omitempty"`

	// Plugin-specific mount options as string key/value pairs, sent when the mount is enabled. Maps to `options`. Example: {"version": "2"} for a KV version 2 engine.
	// +kubebuilder:validation:MaxProperties=32
	// +optional
	Options map[string]MountOption `json:"options,omitempty"`

	// Default lifetime of leases and tokens issued through this mount, as a duration; 0s uses the system default. Maps to `default_lease_ttl`. Must not exceed maxLeaseTTL. Example: 1h
	// +kubebuilder:validation:Type=string
	// +kubebuilder:validation:Pattern=`^(0|([0-9]+(\.[0-9]+)?(ns|us|ms|s|m|h))+)$`
	// +optional
	DefaultLeaseTTL *metav1.Duration `json:"defaultLeaseTTL,omitempty"`

	// Maximum lifetime of leases and tokens issued through this mount, as a duration; 0s uses the system maximum. Maps to `max_lease_ttl`. Example: 24h
	// +kubebuilder:validation:Type=string
	// +kubebuilder:validation:Pattern=`^(0|([0-9]+(\.[0-9]+)?(ns|us|ms|s|m|h))+)$`
	// +optional
	MaxLeaseTTL *metav1.Duration `json:"maxLeaseTTL,omitempty"`

	// Whether the mount appears in the unauthenticated mount listing of the UI. Maps to `listing_visibility`. Allowed values: unauth (listed), hidden (not listed). Default: unset, which is not listed.
	// +optional
	ListingVisibility ListingVisibility `json:"listingVisibility,omitempty"`

	// Request keys that audit devices log in plain text instead of HMAC-hashed. Maps to `audit_non_hmac_request_keys`; order does not matter. Example: ["role"]
	// +kubebuilder:validation:MaxItems=64
	// +kubebuilder:validation:items:MinLength=1
	// +kubebuilder:validation:items:MaxLength=256
	// +listType=set
	// +optional
	AuditNonHMACRequestKeys []string `json:"auditNonHMACRequestKeys,omitempty"`

	// Response keys that audit devices log in plain text instead of HMAC-hashed. Maps to `audit_non_hmac_response_keys`; order does not matter. Example: ["lease_duration"]
	// +kubebuilder:validation:MaxItems=64
	// +kubebuilder:validation:items:MinLength=1
	// +kubebuilder:validation:items:MaxLength=256
	// +listType=set
	// +optional
	AuditNonHMACResponseKeys []string `json:"auditNonHMACResponseKeys,omitempty"`

	// HTTP request headers passed through to the plugin backend. Maps to `passthrough_request_headers`; order does not matter. Example: ["X-Request-Id"]
	// +kubebuilder:validation:MaxItems=64
	// +kubebuilder:validation:items:MinLength=1
	// +kubebuilder:validation:items:MaxLength=256
	// +listType=set
	// +optional
	PassthroughRequestHeaders []string `json:"passthroughRequestHeaders,omitempty"`

	// HTTP response headers the plugin backend is allowed to set. Maps to `allowed_response_headers`; order does not matter. Example: ["Location"]
	// +kubebuilder:validation:MaxItems=64
	// +kubebuilder:validation:items:MinLength=1
	// +kubebuilder:validation:items:MaxLength=256
	// +listType=set
	// +optional
	AllowedResponseHeaders []string `json:"allowedResponseHeaders,omitempty"`
}
