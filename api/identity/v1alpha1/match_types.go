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
	commonv1alpha1 "github.com/alexandrust88/openbao-config-operator/api/common/v1alpha1"
)

// Selects OpenBao entities by exactly one of an ID, an OpenBao name, an Entity object or a label selector over Entity objects.
// +kubebuilder:validation:XValidation:rule="[has(self.entityID), has(self.entityName), has(self.entityRef), has(self.entitySelector)].filter(x, x).size() == 1",message="set exactly one of entityID, entityName, entityRef and entitySelector"
type EntityMatch struct {
	// entityID is the OpenBao ID of an entity. Example: `0b1c6a2e-5d1f-4a7e-9b2e-3c4d5e6f7a8b`.
	// +kubebuilder:validation:MaxLength=128
	// +optional
	EntityID string `json:"entityID,omitempty"`
	// entityName is the OpenBao name of an entity, resolved to its ID through the connection. Example: `alice`.
	// +kubebuilder:validation:MaxLength=128
	// +optional
	EntityName string `json:"entityName,omitempty"`
	// entityRef names an Entity in the same namespace; it must be Ready. Example: `{name: alice}`.
	// +optional
	EntityRef *commonv1alpha1.LocalObjectReference `json:"entityRef,omitempty"`
	// entitySelector selects every Entity in the same namespace that carries these labels.
	// +optional
	EntitySelector *commonv1alpha1.LabelSelector `json:"entitySelector,omitempty"`
}

// Selects OpenBao identity groups by exactly one of an ID, an OpenBao name, a Group object or a label selector over Group objects.
// +kubebuilder:validation:XValidation:rule="[has(self.groupID), has(self.groupName), has(self.groupRef), has(self.groupSelector)].filter(x, x).size() == 1",message="set exactly one of groupID, groupName, groupRef and groupSelector"
type GroupMatch struct {
	// groupID is the OpenBao ID of a group. Example: `5f1c2d3e-4a5b-6c7d-8e9f-0a1b2c3d4e5f`.
	// +kubebuilder:validation:MaxLength=128
	// +optional
	GroupID string `json:"groupID,omitempty"`
	// groupName is the OpenBao name of a group, resolved to its ID through the connection. Example: `billing-admins`.
	// +kubebuilder:validation:MaxLength=128
	// +optional
	GroupName string `json:"groupName,omitempty"`
	// groupRef names a Group in the same namespace; it must be Ready. Example: `{name: billing-admins}`.
	// +optional
	GroupRef *commonv1alpha1.LocalObjectReference `json:"groupRef,omitempty"`
	// groupSelector selects every Group in the same namespace that carries these labels.
	// +optional
	GroupSelector *commonv1alpha1.LabelSelector `json:"groupSelector,omitempty"`
}

// Selects auth mounts by exactly one of an accessor, a mount path, an AuthMount object or a label selector over AuthMount objects.
// +kubebuilder:validation:XValidation:rule="[has(self.mountAccessor), has(self.mountPath), has(self.mountRef), has(self.mountSelector)].filter(x, x).size() == 1",message="set exactly one of mountAccessor, mountPath, mountRef and mountSelector"
type AuthMountMatch struct {
	// mountAccessor is the accessor of an auth mount. Example: `auth_userpass_1a2b3c4d`.
	// +kubebuilder:validation:MaxLength=128
	// +optional
	MountAccessor string `json:"mountAccessor,omitempty"`
	// mountPath is the path of an auth mount without the `auth/` prefix, resolved to its accessor through the connection. Example: `userpass`.
	// +kubebuilder:validation:MaxLength=256
	// +optional
	MountPath string `json:"mountPath,omitempty"`
	// mountRef names an AuthMount in the same namespace; it must be Ready. Example: `{name: userpass}`.
	// +optional
	MountRef *commonv1alpha1.LocalObjectReference `json:"mountRef,omitempty"`
	// mountSelector selects every AuthMount in the same namespace that carries these labels.
	// +optional
	MountSelector *commonv1alpha1.LabelSelector `json:"mountSelector,omitempty"`
}

// Selects login MFA methods by exactly one of a method ID, an OpenBao method name, an MFAMethod object or a label selector over MFAMethod objects.
// +kubebuilder:validation:XValidation:rule="[has(self.methodID), has(self.methodName), has(self.methodRef), has(self.methodSelector)].filter(x, x).size() == 1",message="set exactly one of methodID, methodName, methodRef and methodSelector"
type MFAMethodMatch struct {
	// methodID is the OpenBao ID of an MFA method. Example: `a1b2c3d4-e5f6-7a8b-9c0d-e1f2a3b4c5d6`.
	// +kubebuilder:validation:MaxLength=64
	// +optional
	MethodID string `json:"methodID,omitempty"`
	// methodName is the OpenBao name of an MFA method in the same OpenBao namespace, resolved to its ID through the connection. Example: `totp-main`.
	// +kubebuilder:validation:MaxLength=128
	// +optional
	MethodName string `json:"methodName,omitempty"`
	// methodRef names an MFAMethod in the same namespace; it must be Ready. Example: `{name: totp-main}`.
	// +optional
	MethodRef *commonv1alpha1.LocalObjectReference `json:"methodRef,omitempty"`
	// methodSelector selects every MFAMethod in the same namespace that carries these labels.
	// +optional
	MethodSelector *commonv1alpha1.LabelSelector `json:"methodSelector,omitempty"`
}

// Selects OIDC clients by exactly one of a client ID, an OpenBao client name, an OIDCClient object or a label selector over OIDCClient objects.
// +kubebuilder:validation:XValidation:rule="[has(self.clientID), has(self.clientName), has(self.clientRef), has(self.clientSelector)].filter(x, x).size() == 1",message="set exactly one of clientID, clientName, clientRef and clientSelector"
type OIDCClientMatch struct {
	// clientID is an OIDC client ID; `*` allows every client. Example: `Ab3dE5fG7hI9jK1lM3nO5pQ7rS9tU1vW`.
	// +kubebuilder:validation:MaxLength=128
	// +optional
	ClientID string `json:"clientID,omitempty"`
	// clientName is the OpenBao name of an OIDC client, resolved to its client ID through the connection. Example: `grafana`.
	// +kubebuilder:validation:MaxLength=128
	// +optional
	ClientName string `json:"clientName,omitempty"`
	// clientRef names an OIDCClient in the same namespace; it must be Ready. Example: `{name: grafana}`.
	// +optional
	ClientRef *commonv1alpha1.LocalObjectReference `json:"clientRef,omitempty"`
	// clientSelector selects every OIDCClient in the same namespace that carries these labels.
	// +optional
	ClientSelector *commonv1alpha1.LabelSelector `json:"clientSelector,omitempty"`
}
