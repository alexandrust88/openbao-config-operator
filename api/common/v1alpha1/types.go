// Package v1alpha1 contains the spec and status types shared by every openbao-config-operator kind.
// +kubebuilder:object:generate=true
package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// ConnectionKind names the kind of connection a resource references.
// +kubebuilder:validation:Enum=OpenBaoConnection;ClusterOpenBaoConnection
type ConnectionKind string

const (
	ConnectionKindNamespaced ConnectionKind = "OpenBaoConnection"
	ConnectionKindCluster    ConnectionKind = "ClusterOpenBaoConnection"
)

// ConnectionReference selects the connection used to reach OpenBao.
type ConnectionReference struct {
	// Kind of the connection: OpenBaoConnection (in the same Kubernetes namespace as this resource) or ClusterOpenBaoConnection (cluster-scoped; its allowedNamespaces must select this namespace). Default: OpenBaoConnection.
	// +kubebuilder:default=OpenBaoConnection
	// +optional
	Kind ConnectionKind `json:"kind,omitempty"`
	// Name of the OpenBaoConnection or ClusterOpenBaoConnection. Example: openbao
	// +required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=253
	Name string `json:"name"`
}

// CreationPolicy controls how an object that already exists in OpenBao is treated.
// +kubebuilder:validation:Enum=Create;Adopt;CreateOrAdopt
type CreationPolicy string

const (
	CreationPolicyCreate        CreationPolicy = "Create"
	CreationPolicyAdopt         CreationPolicy = "Adopt"
	CreationPolicyCreateOrAdopt CreationPolicy = "CreateOrAdopt"
)

// DeletionPolicy controls what happens in OpenBao when the resource is deleted.
// +kubebuilder:validation:Enum=Delete;Orphan
type DeletionPolicy string

const (
	DeletionPolicyDelete DeletionPolicy = "Delete"
	DeletionPolicyOrphan DeletionPolicy = "Orphan"
)

// DriftDetection controls whether the operator re-reads OpenBao to repair out-of-band changes.
// +kubebuilder:validation:Enum=Enabled;Disabled
type DriftDetection string

const (
	DriftDetectionEnabled  DriftDetection = "Enabled"
	DriftDetectionDisabled DriftDetection = "Disabled"
)

// A key of a Kubernetes Secret in the same namespace as this resource; the value is read at each write and never stored in status.
type SecretKeySelector struct {
	// Name of the Kubernetes Secret, in the same namespace as this resource. Example: openbao-credentials
	// +required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=253
	Name string `json:"name"`
	// Key in the Secret's data whose value is used. Example: password
	// +required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=253
	Key string `json:"key"`
}

// Selects objects in the same Kubernetes namespace as this resource whose labels match every entry.
type LabelSelector struct {
	// Labels an object must carry, all of them, to be selected. Example: {team: payments}
	// +kubebuilder:validation:MinProperties=1
	// +kubebuilder:validation:MaxProperties=16
	// +kubebuilder:validation:XValidation:rule="self.all(k, size(k) <= 317 && size(self[k]) <= 63)",message="label keys must be at most 317 and values at most 63 characters"
	// +required
	MatchLabels map[string]string `json:"matchLabels"`
}

// Reference by name to an object in the same Kubernetes namespace as this resource.
type LocalObjectReference struct {
	// Name of the referenced object, in the same namespace as this resource. Example: my-app
	// +required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=253
	Name string `json:"name"`
}

// ResourceSpec is embedded inline in the spec of every managed kind.
//
// +kubebuilder:validation:XValidation:rule="has(self.__namespace__) == has(oldSelf.__namespace__) && (!has(self.__namespace__) || self.__namespace__ == oldSelf.__namespace__)",message="namespace is immutable"
// +kubebuilder:validation:XValidation:rule="self.connectionRef == oldSelf.connectionRef",message="connectionRef is immutable"
type ResourceSpec struct {
	// Connection used to reach and log in to OpenBao. Immutable.
	// +required
	ConnectionRef ConnectionReference `json:"connectionRef"`
	// OpenBao namespace of the object, relative to the connection's base namespace; empty means the connection namespace itself. Segments are separated by "/", without leading or trailing slash. Immutable: it cannot be added, changed or removed after creation. Example: team-a/apps
	// +kubebuilder:validation:MaxLength=256
	// +kubebuilder:validation:Pattern=`^[A-Za-z0-9_-][A-Za-z0-9._-]*(/[A-Za-z0-9_-][A-Za-z0-9._-]*)*$`
	// +optional
	Namespace string `json:"namespace,omitempty"`
	// How an object that already exists in OpenBao, and is not yet owned by this resource, is treated. Create (default): create the object; an existing one is refused with reason Conflict. Adopt: take over an existing object and never create one. CreateOrAdopt: create it, or take over an existing one. Settings that always exist in OpenBao (configuration singletons) need Adopt or CreateOrAdopt.
	// +kubebuilder:default=Create
	// +optional
	CreationPolicy CreationPolicy `json:"creationPolicy,omitempty"`
	// What happens in OpenBao when this resource is deleted. Orphan (default): the OpenBao object stays. Delete: the object is removed (a configuration singleton is reset to its defaults); data holders such as mounts, namespaces and KV data are removed only while the resource carries the annotation openbaoconfig.io/allow-destructive-delete: "true". Kinds whose OpenBao object cannot be deleted reject Delete.
	// +kubebuilder:default=Orphan
	// +optional
	DeletionPolicy DeletionPolicy `json:"deletionPolicy,omitempty"`
	// Whether OpenBao is re-read periodically to detect and repair out-of-band changes. Enabled (default): re-read every driftInterval. Disabled: write only when the spec or a referenced Secret changes.
	// +kubebuilder:default=Enabled
	// +optional
	DriftDetection DriftDetection `json:"driftDetection,omitempty"`
	// How often OpenBao is re-read for drift, as a duration of at least 30s. Default: the operator's interval for the kind (15m for kinds that grant access such as policies, auth mounts and roles, 1h for the rest), with jitter. Example: 30m
	// +kubebuilder:validation:Type=string
	// +kubebuilder:validation:MaxLength=32
	// +kubebuilder:validation:XValidation:rule="duration(self) >= duration('30s')",message="driftInterval must be at least 30s"
	// +optional
	DriftInterval *metav1.Duration `json:"driftInterval,omitempty"`
	// Whether the operator writes to OpenBao. Manage (default): converge OpenBao to the spec, create and delete as the policies say. Observe: only read the object and report drift in the conditions; never write or delete.
	// +kubebuilder:default=Manage
	// +optional
	ManagementPolicy ManagementPolicy `json:"managementPolicy,omitempty"`
}

// ManagementPolicy selects whether the operator writes to OpenBao.
// +kubebuilder:validation:Enum=Manage;Observe
type ManagementPolicy string

const (
	ManagementPolicyManage  ManagementPolicy = "Manage"
	ManagementPolicyObserve ManagementPolicy = "Observe"
)

// AnnotationPaused set to "true" on a managed object, its Kubernetes Namespace or its OpenBaoServer stops reconciling (no reads, writes or deletes) until removed, so an incident change is not reverted.
const AnnotationPaused = "openbaoconfig.io/paused"

// AnnotationAllowDestructiveDelete must be "true" before a Delete policy removes a destructive object (mount, namespace, KV data) or an update recreates one.
const AnnotationAllowDestructiveDelete = "openbaoconfig.io/allow-destructive-delete"

// AcquiredState records how the operator came to own an OpenBao object.
type AcquiredState string

const (
	AcquiredCreated AcquiredState = "Created"
	AcquiredAdopted AcquiredState = "Adopted"
)

// Condition types and reasons shared by every kind.
const (
	ConditionReady  = "Ready"
	ConditionSynced = "Synced"

	ReasonAvailable           = "Available"
	ReasonOutOfSync           = "OutOfSync"
	ReasonReconcileSuccess    = "ReconcileSuccess"
	ReasonReconcileError      = "ReconcileError"
	ReasonConnectionNotReady  = "ConnectionNotReady"
	ReasonConnectionDenied    = "ConnectionDenied"
	ReasonReferenceNotReady   = "ReferenceNotReady"
	ReasonConflict            = "Conflict"
	ReasonNotFound            = "NotFound"
	ReasonUnsupported         = "Unsupported"
	ReasonCleanupBlocked      = "CleanupBlocked"
	ReasonInvalidSpec         = "InvalidSpec"
	ReasonReconcileInProgress = "ReconcileInProgress"
	ReasonNamespaceChanged    = "NamespaceChanged"
	ReasonPermissionDenied    = "PermissionDenied"
	ReasonDeleteGuarded       = "DeleteGuarded"
	ReasonPluginNotRegistered = "PluginNotRegistered"
	ReasonSecretNotOwned      = "SecretNotOwned"
	ReasonPaused              = "Paused"
	ReasonWriteBudgetExceeded = "WriteBudgetExceeded"
	ReasonObserved            = "Observed"
	ReasonDrifted             = "Drifted"
	ReasonOrphaned            = "Orphaned"
	ReasonServerChanged       = "ServerChanged"
	ReasonOutputNotDelivered  = "OutputNotDelivered"
	ReasonRootOnlyKind        = "RootOnlyKind"
)

// ResourceStatus is embedded inline in the status of every managed kind.
type ResourceStatus struct {
	// Current state as conditions: Ready (the OpenBao object matches the spec) and Synced (the last reconcile succeeded), each with a reason such as Available, Drifted, Conflict, ConnectionNotReady or PermissionDenied, and the generation it describes.
	// +listType=map
	// +listMapKey=type
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
	// The metadata.generation of this resource that the operator last reconciled.
	// +optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`
	// Full OpenBao API path of the object, including its namespace, as last written; deletion uses it even after the spec changed. Example: team-a/sys/policies/acl/app-read
	// +optional
	Path string `json:"path,omitempty"`
	// Absolute OpenBao namespace the object was written in; empty means the root namespace. If it later differs from the spec, reconciling stops with reason NamespaceChanged. Example: team-a/apps
	// +optional
	Namespace string `json:"namespace,omitempty"`
	// Cluster ID of the OpenBao server the object was acquired on. If the connection later reaches another server, reconciling stops with reason ServerChanged and nothing is written or deleted. Example: 80f7de92-c12e-e960-4754-255c32fc6b53
	// +optional
	ServerID string `json:"serverID,omitempty"`
	// How the operator came to own the object: Created (it created it) or Adopted (it took over an existing one). Empty until owned.
	// +optional
	Acquired AcquiredState `json:"acquired,omitempty"`
	// Keyed hash (HMAC-SHA256) of the write-only inputs, such as passwords read from Secrets; a change forces a write. It never contains or reveals the values.
	// +optional
	WriteOnlyHash string `json:"writeOnlyHash,omitempty"`
	// Identifier OpenBao generated for the object (an entity, group or alias ID, or a mount accessor), for kinds that have one; other resources reference it. Example: 6c9f3e0a-1b2c-4d5e-8f90-123456789abc
	// +optional
	ID string `json:"id,omitempty"`
	// Value of the resource's rotation nonce when one-time inputs, such as a root credential that OpenBao rotates afterwards, were last sent; changing the nonce in the spec sends them again.
	// +optional
	RotationNonce string `json:"rotationNonce,omitempty"`
}

// Managed is implemented by every kind the generic engine reconciles.
// +kubebuilder:object:generate=false
type Managed interface {
	GetResourceSpec() *ResourceSpec
	GetResourceStatus() *ResourceStatus
}
