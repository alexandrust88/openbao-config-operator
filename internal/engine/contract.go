// Package engine holds the generic reconciler shared by every managed kind.
package engine

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"

	"sigs.k8s.io/controller-runtime/pkg/client"

	commonv1alpha1 "github.com/alexandrust88/openbao-config-operator/api/common/v1alpha1"
	"github.com/alexandrust88/openbao-config-operator/internal/openbao"
)

// Payload is the JSON body written to or read from an OpenBao path.
type Payload = map[string]any

// Object is a managed custom resource.
type Object interface {
	client.Object
	commonv1alpha1.Managed
}

// FieldSpec declares how desired and observed payloads are compared for a kind.
type FieldSpec struct {
	// WriteOnly fields are never returned by a read; they are hashed into status.writeOnlyHash.
	WriteOnly []string
	// CreateOnly fields are sent on create, stripped from updates and never compared.
	CreateOnly []string
	// Aliases maps a key as read to the key as written, for example "token_period": "period".
	Aliases map[string]string
	// Sets are list fields compared without regard to order.
	Sets []string
	// Durations are compared in seconds, so "1h" equals 3600.
	Durations []string
	// ServerOwned fields are set by OpenBao (accessors, creation times); they are never compared and never sent.
	ServerOwned []string
	// Partial fields hold maps the server merges into or extends (Endpoint data, KV merges); they are compared on
	// the desired keys only, while every other map must match exactly. An entry ending in "*" matches keys by prefix.
	Partial []string
	// WriteOnce fields are sent on create and afterwards only when the adapter's RotationNonce changes; they are
	// never compared or hashed, so a credential OpenBao rotated itself is not overwritten by a stale Secret.
	WriteOnce []string
}

// Resolver resolves inputs that live in Kubernetes.
type Resolver interface {
	// SecretValue returns the value of a key of a Secret in namespace.
	SecretValue(ctx context.Context, namespace string, ref commonv1alpha1.SecretKeySelector) ([]byte, error)
	// Object reads a referenced object; adapters use it to resolve xxxRef fields from the referent's status.
	Object(ctx context.Context, key client.ObjectKey, obj client.Object) error
}

// Adapter maps one kind onto an OpenBao path and payload.
type Adapter[T Object] interface {
	// Path is the API path relative to the object's OpenBao namespace, without a leading slash.
	Path(obj T) string
	// Desired builds the payload to write.
	Desired(ctx context.Context, r Resolver, obj T) (Payload, error)
	// Fields describes comparison rules.
	Fields() FieldSpec
}

// FieldsProvider adds per-object comparison rules; the result is merged over Adapter.Fields().
type FieldsProvider[T Object] interface {
	FieldsFor(ctx context.Context, r Resolver, obj T) (FieldSpec, error)
}

// PathResolver replaces Adapter.Path for kinds whose path depends on a referenced object, such as a mount chosen by
// mountRef. The engine passes the resolved path to the default read and write and records it in status.path, so a
// delete still reaches the object after the referent changes. Errors wrapping ErrReferenceNotReady stall the
// resource.
type PathResolver[T Object] interface {
	ResolvePath(ctx context.Context, r Resolver, c openbao.Client, obj T) (string, error)
}

// Observer replaces the default read (client.Read(Path)). found=false means absent.
type Observer[T Object] interface {
	Observe(ctx context.Context, c openbao.Client, obj T) (observed Payload, found bool, err error)
}

// Writer replaces the default write (client.Write(Path, desired)) and returns the response payload, which may be nil.
type Writer[T Object] interface {
	Write(ctx context.Context, c openbao.Client, obj T, desired Payload, create bool) (Payload, error)
}

// OutputWriter turns a write response into a Kubernetes Secret owned by the object, for values OpenBao returns
// once. ok=false means no Secret is written for this response.
type OutputWriter[T Object] interface {
	Outputs(obj T, response Payload) (secretName string, data map[string][]byte, ok bool)
}

// OutputRefresher is implemented by OutputWriter adapters whose output OpenBao can return again, such as a stored
// generated value or a client secret that a read returns. After a pass that wrote nothing to OpenBao, the engine
// passes the observed object to RefreshOutputs and hands the result to Outputs, so a missing or stale output Secret
// is written again without an OpenBao write or a reported drift. Values OpenBao returns only once (exported private
// keys, TOTP seeds, secret IDs) cannot be refreshed; their adapters report ErrOutputNotDelivered instead.
type OutputRefresher[T Object] interface {
	RefreshOutputs(ctx context.Context, c openbao.Client, obj T, observed Payload) (response Payload, err error)
}

// OutputsDeliveredHook is implemented by adapters that clean up in OpenBao once the output Secret holds the current
// value, such as destroying replaced credentials. The engine calls it only with managementPolicy Manage, after the
// output step of a pass succeeded (a pass that wrote nothing has nothing new to deliver).
type OutputsDeliveredHook[T Object] interface {
	OutputsDelivered(ctx context.Context, c openbao.Client, obj T) error
}

// Deleter replaces the default delete (client.Delete(status.path)).
type Deleter[T Object] interface {
	Delete(ctx context.Context, c openbao.Client, obj T) error
}

// StatusWriter copies server-side values (ids, accessors) into the status after a successful sync.
type StatusWriter[T Object] interface {
	WriteStatus(ctx context.Context, c openbao.Client, obj T, observed Payload) error
}

// Errors returned by Connections.For.
var (
	ErrConnectionNotReady = errors.New("connection not ready")
	ErrConnectionDenied   = errors.New("connection not allowed from this namespace")
)

// Connections resolves a resource's connectionRef to an authenticated client scoped to the connection namespace.
type Connections interface {
	For(ctx context.Context, fromNamespace string, ref commonv1alpha1.ConnectionReference) (openbao.Client, error)
}

// CleanupConnections is implemented by Connections that can still serve the deletion of a resource after its
// connection began terminating. The engine uses ForCleanup only to delete; every other reconcile goes through For.
type CleanupConnections interface {
	ForCleanup(ctx context.Context, fromNamespace string, ref commonv1alpha1.ConnectionReference) (openbao.Client, error)
}

// ErrInProgress wraps errors from Observer, Writer and Deleter when OpenBao is still completing an asynchronous
// operation; the engine requeues without error backoff.
var ErrInProgress = errors.New("operation in progress")

// ErrConflict wraps errors from adapters when another resource owns the object; the engine reports the reason
// Conflict and drops this resource's claim, as for a path owned by an older resource.
var ErrConflict = errors.New("the object is managed by another resource")

// ErrReferenceNotReady wraps errors from adapters when a referenced object is missing or not Ready.
var ErrReferenceNotReady = errors.New("reference not ready")

// ErrOutputNotDelivered wraps errors from Observer when a value OpenBao returned once is not in the output Secret
// and cannot be read again; the engine reports the reason OutputNotDelivered.
var ErrOutputNotDelivered = errors.New("output not delivered")

// ErrInvalidSpec wraps errors from adapters when the spec, or a Secret it references, can never work as written,
// for example a payload that is not a JSON object. The engine reports the reason InvalidSpec and does not retry
// (reconcile.TerminalError); a spec or Secret change triggers the next attempt.
var ErrInvalidSpec = errors.New("invalid spec")

// ErrUnsupported wraps errors from adapters, Observer and Writer when the server cannot serve the kind, for example
// because its plugin or mount is missing; the engine reports the reason Unsupported and retries after the drift
// interval without error backoff.
var ErrUnsupported = errors.New("unsupported by the server")

// ErrPluginNotRegistered wraps errors from adapters when a mount needs a plugin that is not in the server's
// catalog; the engine reports the reason PluginNotRegistered and checks again after the drift interval without
// error backoff.
var ErrPluginNotRegistered = errors.New("plugin not registered")

// UnsupportedIfMountMissing wraps err with ErrUnsupported when OpenBao answered 404 or "no handler for route",
// which is how a missing mount or plugin shows. Other errors, and nil, are returned unchanged.
func UnsupportedIfMountMissing(err error) error {
	re, ok := errors.AsType[*openbao.ResponseError](err)
	if !ok {
		return err
	}
	if re.StatusCode == http.StatusNotFound ||
		slices.ContainsFunc(re.Errors, func(e string) bool { return strings.Contains(e, "no handler for route") }) {
		return fmt.Errorf("%w: %w", ErrUnsupported, err)
	}
	return err
}

// Destructive is implemented by adapters whose kind holds data (mounts, namespaces, KV data). For them a Delete
// policy, or an update that recreates the object because a CreateOnly field changed, takes effect only while the
// resource carries the annotation commonv1alpha1.AnnotationAllowDestructiveDelete="true"; otherwise the engine
// keeps the finalizer and reports the reason DeleteGuarded.
type Destructive interface {
	Destructive() bool
}

// Tier selects the default drift interval of a kind.
type Tier string

// Drift tiers; Options.DriftIntervalCritical and Options.DriftIntervalStandard set their intervals.
const (
	TierAccessCritical Tier = "AccessCritical"
	TierStandard       Tier = "Standard"
)

// DriftTier is implemented by adapters of kinds that grant or deny access (policies, auth mounts, auth roles and
// configs, groups); they report TierAccessCritical. Adapters without it are TierStandard.
type DriftTier interface {
	DriftTier() Tier
}

// OwnershipMarker identifies the custom resource that manages an OpenBao object.
type OwnershipMarker struct {
	// ManagedBy is the engine's field owner.
	ManagedBy string
	// ClusterID is Options.ClusterID of the operator that wrote the marker.
	ClusterID string
	// UID is the UID of the custom resource.
	UID string
}

// Marker is implemented by adapters whose OpenBao object carries metadata (entities, groups, namespaces, KV v2
// metadata). The engine writes the marker with every create and update and checks it before status.acquired, so
// ownership survives a lost status and a marker written by another cluster gives the reason Conflict.
type Marker[T Object] interface {
	// Mark returns desired with marker added in the object's metadata.
	Mark(obj T, desired Payload, marker OwnershipMarker) Payload
	// MarkerOf returns the marker of an observed object; ok=false when it carries none.
	MarkerOf(observed Payload) (marker OwnershipMarker, ok bool)
}

// RotationNonce is implemented by adapters with FieldSpec.WriteOnce fields. A nonce that differs from
// status.rotationNonce sends the write-once fields again; the engine records the nonce after the write.
type RotationNonce[T Object] interface {
	RotationNonce(obj T) string
}

// OutputSecretNamer names the Secret an OutputWriter writes, so the engine can refuse a Secret it does not own
// before it writes to OpenBao. An empty name means no Secret.
type OutputSecretNamer[T Object] interface {
	OutputSecretName(obj T) string
}

// ReadBackSkipper is implemented by adapters whose object cannot be read back right after a write, for example an
// Endpoint with disableRead; true skips the engine's read-after-write confirmation.
type ReadBackSkipper[T Object] interface {
	SkipReadBack(obj T) bool
}

// ClusterIdentifier is implemented by clients that can name the OpenBao cluster behind them (the cluster_id of
// sys/health, stable for the life of the cluster). The engine records it in status.serverID and never writes or
// deletes through a connection that now reaches another cluster.
type ClusterIdentifier interface {
	ClusterID(ctx context.Context) (string, error)
}

// Server identifies the OpenBao server behind a connection.
type Server struct {
	// Key is the same for every connection to one server, for example the BaoServer name.
	Key string
	// Paused reports that the server carries commonv1alpha1.AnnotationPaused="true".
	Paused bool
}

// ServerResolver is implemented by Connections that know the server behind a connection reference without
// logging in. The engine uses it for the path ownership index, the write budget and pause; without it every
// connection counts as its own server.
type ServerResolver interface {
	ServerFor(ctx context.Context, fromNamespace string, ref commonv1alpha1.ConnectionReference) (Server, error)
}

// ErrSecretNotOwned is returned when an output Secret exists and is not controlled by the resource; the engine
// reports the reason SecretNotOwned and leaves the Secret alone.
var ErrSecretNotOwned = errors.New("the Secret exists and is not controlled by this resource")

// SharedPath is implemented by adapters of kinds that deliberately write to a path another resource owns, such as
// IdentityGroupMembership on its IdentityGroup or claim-only edge kinds. They are left out of the path ownership index and
// conflict check, and get no ownership marker.
type SharedPath interface {
	SharedPath() bool
}

// Singleton is implemented by adapters of configuration that always exists with defaults (auth and engine config
// endpoints, sys singletons). The engine adopts the existing object whatever the creationPolicy, since it can never
// be created.
type Singleton interface {
	Singleton() bool
}
