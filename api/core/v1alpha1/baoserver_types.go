package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// BaoServerSpec describes an OpenBao server and which tenants may connect to it.
// +kubebuilder:validation:XValidation:rule="self.address.startsWith('https://') || (has(self.allowInsecureHTTP) && self.allowInsecureHTTP)",message="address must use https unless allowInsecureHTTP is true"
type BaoServerSpec struct {
	// URL of the OpenBao API, without the /v1 suffix and without credentials (user:password@). Must be https unless allowInsecureHTTP is true. Only a BaoServer sets the address the operator calls; connections reference the server. Example: https://openbao.openbao.svc:8200
	// +kubebuilder:validation:MaxLength=2048
	// +kubebuilder:validation:Pattern=`^https?://[^\s/?#@]+(/[^\s?#]*)?$`
	// +kubebuilder:validation:XValidation:rule="!self.matches('/v1/?$')",message="address must not end with /v1"
	// +required
	Address string `json:"address"`
	// Allow an http:// address. Tokens, login JWTs and every secret input then cross the network in clear text: use it only for a local development server. Default: false.
	// +optional
	AllowInsecureHTTP bool `json:"allowInsecureHTTP,omitempty"`
	// PEM CA bundle that verifies the server's TLS certificate, from exactly one of a Secret or a ConfigMap. Unset uses the operator's system roots.
	// +optional
	CABundle *CABundleSource `json:"caBundle,omitempty"`
	// Label selector over Kubernetes namespaces whose BaoConnections may use this server. Unset allows no namespace; an empty selector ({}) allows every namespace. ClusterBaoConnections are not restricted by it. Example: {matchLabels: {openbao.io/tenant: "true"}}
	// +optional
	AllowedNamespaces *metav1.LabelSelector `json:"allowedNamespaces,omitempty"`
	// Token audiences a BaoConnection may request for its ServiceAccount; every audience a connection asks for must be listed. The audiences of the Kubernetes API server are always refused, even when listed, because such a token is a Kubernetes credential. At most 16 entries. Example: ["openbao"]
	// +kubebuilder:validation:MaxItems=16
	// +kubebuilder:validation:items:MinLength=1
	// +kubebuilder:validation:items:MaxLength=256
	// +listType=set
	// +optional
	AllowedAudiences []string `json:"allowedAudiences,omitempty"`
	// Client-side token bucket for the requests the operator sends to this server, shared by every connection to it. Unset means no limit.
	// +optional
	RateLimit *ServerRateLimit `json:"rateLimit,omitempty"`
	// Maximum time one request to OpenBao may take, as a duration. Default: 60s. Example: 30s
	// +optional
	RequestTimeout *metav1.Duration `json:"requestTimeout,omitempty"`
}

// CABundleSource selects a PEM CA bundle in a Secret or a ConfigMap.
// +kubebuilder:validation:ExactlyOneOf=secretRef;configMapRef
type CABundleSource struct {
	// Secret that holds the PEM CA bundle. Mutually exclusive with configMapRef.
	// +optional
	SecretRef *NamespacedCABundleReference `json:"secretRef,omitempty"`
	// ConfigMap that holds the PEM CA bundle, for example one filled by trust-manager. Mutually exclusive with secretRef.
	// +optional
	ConfigMapRef *NamespacedCABundleReference `json:"configMapRef,omitempty"`
}

// ServerRateLimit is a token bucket for the requests sent to one server.
type ServerRateLimit struct {
	// Sustained number of requests per second, 1 to 100000. Example: 50
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=100000
	// +required
	QPS int32 `json:"qps"`
	// Number of requests that may be sent at once above the sustained rate, 1 to 100000. Default: the value of qps. Example: 100
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=100000
	// +optional
	Burst int32 `json:"burst,omitempty"`
}

// BaoServerStatus is the observed state of the server.
type BaoServerStatus struct {
	// Current state as conditions: Ready is True when the server is reachable, initialized and unsealed; otherwise the reason is Unreachable, Sealed, InvalidSpec or ReferenceNotReady (CA bundle missing).
	// +listType=map
	// +listMapKey=type
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
	// The metadata.generation of this server that the operator last probed against.
	// +optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`
	// OpenBao version reported by the server. Example: 2.7.2
	// +optional
	Version string `json:"version,omitempty"`
	// True when the server is sealed or not initialized; no connection to it is Ready then.
	// +optional
	Sealed bool `json:"sealed"`
	// True when the probed node is a standby rather than the active node.
	// +optional
	Standby bool `json:"standby"`
	// When a probe last changed this status; probes (about every minute) that find nothing new do not update it.
	// +optional
	LastProbeTime *metav1.Time `json:"lastProbeTime,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Cluster,shortName=baosrv,categories=obco;obco-core
// +kubebuilder:printcolumn:name="Ready",type=string,JSONPath=`.status.conditions[?(@.type=="Ready")].status`
// +kubebuilder:printcolumn:name="Address",type=string,JSONPath=`.spec.address`
// +kubebuilder:printcolumn:name="Version",type=string,JSONPath=`.status.version`
// +kubebuilder:printcolumn:name="Sealed",type=boolean,JSONPath=`.status.sealed`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// BaoServer is a cluster-scoped OpenBao server that the platform team makes available to connections. It is the only place that sets the address, CA bundle, request rate and timeout the operator uses, and it decides which namespaces and token audiences tenants' BaoConnections may use. It writes nothing to OpenBao; the operator probes sys/health and sys/seal-status and reports the version and seal state.
//
// Example:
//
//	spec:
//	  address: https://openbao.openbao.svc:8200
//	  caBundle:
//	    configMapRef:
//	      namespace: openbao
//	      name: openbao-ca
//	  allowedNamespaces:
//	    matchLabels:
//	      openbao.io/tenant: "true"
//	  allowedAudiences: ["openbao"]
type BaoServer struct {
	metav1.TypeMeta `json:",inline"`

	// +optional
	metav1.ObjectMeta `json:"metadata,omitzero"`

	// Desired state of the server: address, trust, tenant allow-lists and request limits.
	// +required
	Spec BaoServerSpec `json:"spec"`

	// Observed state of the server, reported by the operator's periodic probe.
	// +optional
	Status BaoServerStatus `json:"status,omitzero"`
}

// +kubebuilder:object:root=true

// BaoServerList contains a list of BaoServer
type BaoServerList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []BaoServer `json:"items"`
}

func init() {
	SchemeBuilder.Register(&BaoServer{}, &BaoServerList{})
}
