package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// Condition reasons specific to connections.
const (
	ReasonUnreachable          = "Unreachable"
	ReasonSealed               = "Sealed"
	ReasonAuthenticationFailed = "AuthenticationFailed"
	ReasonCredentialsNotFound  = "CredentialsNotFound"
	ReasonServerNotReady       = "ServerNotReady"
)

// DefaultCABundleKey is the key read from a CA bundle Secret or ConfigMap when none is set.
const DefaultCABundleKey = "ca.crt"

// ConnectionSpec holds the fields shared by BaoConnection and ClusterBaoConnection.
type ConnectionSpec struct {
	// BaoServer to connect to; it supplies the address, CA bundle and limits, and must allow this connection's namespace and token audiences.
	// +required
	ServerRef ServerReference `json:"serverRef"`
	// Base OpenBao namespace for every request made through this connection; a resource's own openbaoNamespace field is relative to it. Empty means the root namespace. Example: team-a
	// +optional
	// +kubebuilder:validation:MaxLength=256
	// +kubebuilder:validation:Pattern=`^[A-Za-z0-9_-][A-Za-z0-9._-]*(/[A-Za-z0-9_-][A-Za-z0-9._-]*)*$`
	Namespace string `json:"openbaoNamespace,omitempty"`
	// Absolute OpenBao namespace that holds the auth mount used to log in. Unset: log in in the connection's namespace. "": log in at the root namespace and operate in the child namespace set by openbaoNamespace. Example: team-a
	// +kubebuilder:validation:MaxLength=256
	// +kubebuilder:validation:Pattern=`^$|^[A-Za-z0-9_-][A-Za-z0-9._-]*(/[A-Za-z0-9_-][A-Za-z0-9._-]*)*$`
	// +optional
	AuthNamespace *string `json:"openbaoAuthNamespace,omitempty"`
}

// ServerReference names a BaoServer.
type ServerReference struct {
	// Name of the cluster-scoped BaoServer. Example: openbao
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=253
	// +required
	Name string `json:"name"`
}

// ConnectionStatus is the observed state of a connection.
type ConnectionStatus struct {
	// Current state as conditions: Ready is True when the server is Ready and login succeeded; otherwise the reason is, for example, ServerNotReady, Sealed, Unreachable, AuthenticationFailed, CredentialsNotFound or ConnectionDenied.
	// +listType=map
	// +listMapKey=type
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
	// The metadata.generation of this connection that the operator last checked.
	// +optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`
	// OpenBao version, copied from the BaoServer status. Example: 2.7.2
	// +optional
	Version string `json:"version,omitempty"`
	// True when the server is sealed or not initialized, copied from the BaoServer status.
	// +optional
	Sealed bool `json:"sealed"`
	// True when the probed node is a standby, copied from the BaoServer status.
	// +optional
	Standby bool `json:"standby"`
	// True when the last login and token lookup (auth/token/lookup-self) succeeded.
	// +optional
	Authenticated bool `json:"authenticated"`
	// When a check last changed this status; checks (about every minute) that find nothing new do not update it.
	// +optional
	LastProbeTime *metav1.Time `json:"lastProbeTime,omitempty"`
}

// NamespacedCABundleReference selects a PEM CA bundle in an object in an explicit namespace.
type NamespacedCABundleReference struct {
	// Kubernetes namespace of the Secret or ConfigMap. Example: openbao
	// +kubebuilder:validation:MinLength=1
	// +required
	Namespace string `json:"namespace"`
	// Name of the Secret or ConfigMap. Example: openbao-ca
	// +kubebuilder:validation:MinLength=1
	// +required
	Name string `json:"name"`
	// Key that holds the PEM bundle. Default: ca.crt. Example: ca.crt
	// +kubebuilder:default=ca.crt
	// +optional
	Key string `json:"key,omitempty"`
}

// NamespacedSecretKeySelector selects a key of a Secret in an explicit namespace.
type NamespacedSecretKeySelector struct {
	// Kubernetes namespace of the Secret. Example: openbao-operator
	// +kubebuilder:validation:MinLength=1
	// +required
	Namespace string `json:"namespace"`
	// Name of the Secret. Example: openbao-operator-approle
	// +kubebuilder:validation:MinLength=1
	// +required
	Name string `json:"name"`
	// Key in the Secret's data whose value is used. Example: secret-id
	// +kubebuilder:validation:MinLength=1
	// +required
	Key string `json:"key"`
}

// NamespacedObjectReference names an object in an explicit namespace.
type NamespacedObjectReference struct {
	// Kubernetes namespace of the object. Example: openbao-operator
	// +kubebuilder:validation:MinLength=1
	// +required
	Namespace string `json:"namespace"`
	// Name of the object. Example: openbao-config-operator
	// +kubebuilder:validation:MinLength=1
	// +required
	Name string `json:"name"`
}

// GetConnectionSpec returns the spec fields shared by both connection kinds.
func (c *BaoConnection) GetConnectionSpec() *ConnectionSpec { return &c.Spec.ConnectionSpec }

// GetConnectionSpec returns the spec fields shared by both connection kinds.
func (c *ClusterBaoConnection) GetConnectionSpec() *ConnectionSpec { return &c.Spec.ConnectionSpec }

// GetConnectionStatus returns the status shared by both connection kinds.
func (c *BaoConnection) GetConnectionStatus() *ConnectionStatus { return &c.Status }

// GetConnectionStatus returns the status shared by both connection kinds.
func (c *ClusterBaoConnection) GetConnectionStatus() *ConnectionStatus { return &c.Status }
