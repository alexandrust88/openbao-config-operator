package engine

import (
	"cmp"
	"context"
	"fmt"
	"slices"
	"sync"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/apiutil"

	commonv1alpha1 "github.com/alexandrust88/openbao-config-operator/api/common/v1alpha1"
	"github.com/alexandrust88/openbao-config-operator/internal/openbao"
)

// PathIndex is the field index, registered on every managed kind, that maps a resource to the absolute OpenBao
// path (status.path) it owns.
const PathIndex = ".status.ownedPath"

// ChildOwner is implemented by composite resources that also own the child objects they created.
type ChildOwner interface {
	// OwnedChildPaths returns the absolute OpenBao paths of the created children.
	OwnedChildPaths() []string
}

// PathIndexFunc extracts the PathIndex value: status.path of a resource that manages its object, whether acquired or
// only claimed before its create, and the paths of the children a ChildOwner created. It is exported for fake clients
// in tests.
func PathIndexFunc(o client.Object) []string {
	if managed, ok := o.(commonv1alpha1.Managed); !ok || observeOnly(managed) {
		return nil
	}
	return recordedPaths(o)
}

// recordedPaths is PathIndexFunc whatever the management policy: a resource that only observes its object claims
// these paths as soon as it is switched to Manage.
func recordedPaths(o client.Object) []string {
	managed, ok := o.(commonv1alpha1.Managed)
	if !ok {
		return nil
	}
	status := managed.GetResourceStatus()
	if status.Path == "" {
		return nil
	}
	paths := []string{openbao.CanonicalPath(status.Path)}
	if children, ok := o.(ChildOwner); ok {
		for _, p := range children.OwnedChildPaths() {
			paths = append(paths, openbao.CanonicalPath(p))
		}
	}
	return paths
}

// OtherChildOwner returns a resource of a managed kind, other than self, that owns absPath, a child object of self, on
// the server with cluster id serverID: any such resource while self has not created absPath, and one that precedes
// self once it has. ok=false when self may write absPath. It reads reader through PathIndex.
func OtherChildOwner(ctx context.Context, reader client.Reader, scheme *runtime.Scheme, self Object, absPath,
	serverID string) (string, bool, error) {
	sharedMu.Lock()
	s := sharedByScheme[scheme]
	sharedMu.Unlock()
	if s == nil {
		return "", false, nil
	}
	selfGVK, err := apiutil.GVKForObject(self, scheme)
	if err != nil {
		return "", false, err
	}
	created := slices.Contains(PathIndexFunc(self), absPath)
	for _, gvk := range s.ownerKinds() {
		list, err := scheme.New(gvk.GroupVersion().WithKind(gvk.Kind + "List"))
		if err != nil {
			return "", false, fmt.Errorf("list type for %s: %w", gvk.Kind, err)
		}
		if err := reader.List(ctx, list.(client.ObjectList), client.MatchingFields{PathIndex: absPath}); err != nil {
			return "", false, fmt.Errorf("list %s by path: %w", gvk.Kind, err)
		}
		items, err := meta.ExtractList(list.(client.ObjectList))
		if err != nil {
			return "", false, err
		}
		for _, item := range items {
			other, ok := item.(Object)
			if !ok || other.GetUID() == self.GetUID() || !slices.Contains(PathIndexFunc(other), absPath) {
				continue
			}
			if recorded := other.GetResourceStatus().ServerID; recorded != "" && serverID != "" && recorded != serverID {
				continue
			}
			candidate := owner{kind: gvk.Kind, obj: other}
			if !created || precedes(candidate, owner{kind: selfGVK.Kind, obj: self}, absPath) {
				return candidate.String(), true, nil
			}
		}
	}
	return "", false, nil
}

func observeOnly(m commonv1alpha1.Managed) bool {
	return m.GetResourceSpec().ManagementPolicy == commonv1alpha1.ManagementPolicyObserve
}

// shared is the state every Reconciler built on one scheme (one manager) shares.
type shared struct {
	mu      sync.Mutex
	kinds   []schema.GroupVersionKind
	budget  *writeBudget
	ramp    recoveryRamp
	creates pathLocks
	live    map[schema.GroupVersionKind]*liveSnapshot
	// touched maps a path to when this process last changed a claim on it.
	touched  map[string]time.Time
	prunedAt time.Time
}

var (
	sharedMu       sync.Mutex
	sharedByScheme = map[*runtime.Scheme]*shared{}
)

func sharedFor(scheme *runtime.Scheme, perMinute int) *shared {
	sharedMu.Lock()
	defer sharedMu.Unlock()
	s, ok := sharedByScheme[scheme]
	if !ok {
		s = &shared{budget: newWriteBudget(perMinute)}
		sharedByScheme[scheme] = s
	}
	return s
}

// addKind makes the kind's resources part of every ownership check; its PathIndex must be registered.
func (s *shared) addKind(gvk schema.GroupVersionKind) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !slices.Contains(s.kinds, gvk) {
		s.kinds = append(s.kinds, gvk)
	}
}

func (s *shared) ownerKinds() []schema.GroupVersionKind {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.kinds)
}

// liveSnapshotTTL bounds how long one live LIST of a kind serves the ownership rechecks.
const liveSnapshotTTL = 10 * time.Second

// liveSnapshot is the time the last live LIST of one kind was sent and the claims it showed, path to claimants.
type liveSnapshot struct {
	mu     sync.Mutex
	start  time.Time
	claims map[string][]client.ObjectKey
}

func (s *shared) snapshot(gvk schema.GroupVersionKind) *liveSnapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.live == nil {
		s.live = map[schema.GroupVersionKind]*liveSnapshot{}
	}
	l, ok := s.live[gvk]
	if !ok {
		l = &liveSnapshot{}
		s.live[gvk] = l
	}
	return l
}

// touch records that a status write of this process, already answered, changed the claims on paths.
func (s *shared) touch(paths []string) {
	now := time.Now()
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.touched == nil {
		s.touched = map[string]time.Time{}
	}
	if now.Sub(s.prunedAt) > liveSnapshotTTL {
		for path, at := range s.touched {
			if now.Sub(at) > liveSnapshotTTL {
				delete(s.touched, path)
			}
		}
		s.prunedAt = now
	}
	for _, path := range paths {
		s.touched[path] = now
	}
}

func (s *shared) touchedSince(path string, since time.Time) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	at, ok := s.touched[path]
	return ok && !at.Before(since)
}

// owner is another resource that claims the same OpenBao path.
type owner struct {
	kind string
	obj  client.Object
}

func (o owner) String() string {
	if o.obj.GetNamespace() == "" {
		return o.kind + " " + o.obj.GetName()
	}
	return o.kind + " " + o.obj.GetNamespace() + "/" + o.obj.GetName()
}

// olderOwner returns the oldest resource of any managed kind, other than obj, that owns absPath on the same server
// and was created before obj; ok=false when obj may own the path. It reads the cache through PathIndex.
func (r *Reconciler[T]) olderOwner(ctx context.Context, obj T, absPath, server, serverID string) (owner, bool, error) {
	other, ok, _, err := r.findOlderOwner(ctx, r.shared.ownerKinds(), r.listCached(absPath), obj, absPath, server, serverID)
	return other, ok, err
}

// liveListPage bounds one page of a live LIST.
const liveListPage = 500

// olderOwnerLive is olderOwner read from the API server, which already shows a claim the cache may not have yet. It
// reads obj's own kind, the likely racer, and the other kinds whose cached index shows a claim on absPath, instead
// of every managed kind.
func (r *Reconciler[T]) olderOwnerLive(ctx context.Context, obj T, absPath, server, serverID string) (owner, bool, error) {
	_, _, kinds, err := r.findOlderOwner(ctx, r.shared.ownerKinds(), r.listCached(absPath), obj, absPath, server, serverID)
	if err != nil {
		return owner{}, false, err
	}
	own, err := apiutil.GVKForObject(obj, r.scheme)
	if err != nil {
		return owner{}, false, err
	}
	if !slices.Contains(kinds, own) {
		kinds = append(kinds, own)
	}
	other, ok, _, err := r.findOlderOwner(ctx, kinds, r.listLiveClaims(absPath), obj, absPath, server, serverID)
	return other, ok, err
}

// listLiveClaims lists the resources of a kind that claim absPath on the API server. One live LIST of the kind serves
// every path for liveSnapshotTTL, so that adopting many objects does not list the kind once per object. A claim
// written after that LIST is missing from it and maybe from the cache too: a path whose claims this process changed
// since then is listed again. The reused LIST keeps every resource that records the path, observing ones included,
// and they are read again, so a released claim does not stand and a switch from Observe to Manage is seen. l.mu is
// held across the LIST by design, so the concurrent rechecks of one kind wait for and share a single LIST.
func (r *Reconciler[T]) listLiveClaims(absPath string) listFunc {
	return func(ctx context.Context, gvk schema.GroupVersionKind) ([]runtime.Object, error) {
		l := r.shared.snapshot(gvk)
		l.mu.Lock()
		defer l.mu.Unlock()
		if time.Since(l.start) < liveSnapshotTTL && !r.shared.touchedSince(absPath, l.start) {
			return r.getLive(ctx, gvk, l.claims[absPath])
		}
		start := time.Now()
		items, err := r.listLive(ctx, gvk)
		if err != nil {
			return nil, err
		}
		l.start, l.claims = start, map[string][]client.ObjectKey{}
		for _, item := range items {
			if o, ok := item.(client.Object); ok {
				for _, path := range recordedPaths(o) {
					l.claims[path] = append(l.claims[path], client.ObjectKeyFromObject(o))
				}
			}
		}
		return items, nil
	}
}

// getLive gets the resources of a kind with keys from the API server and leaves out the deleted ones.
func (r *Reconciler[T]) getLive(ctx context.Context, gvk schema.GroupVersionKind, keys []client.ObjectKey) ([]runtime.Object, error) {
	var items []runtime.Object
	for _, key := range keys {
		obj, err := r.scheme.New(gvk)
		if err != nil {
			return nil, fmt.Errorf("object type for %s: %w", gvk.Kind, err)
		}
		err = r.liveReader().Get(ctx, key, obj.(client.Object))
		switch {
		case apierrors.IsNotFound(err):
			continue
		case err != nil:
			return nil, fmt.Errorf("get %s: %w", gvk.Kind, err)
		}
		items = append(items, obj)
	}
	return items, nil
}

func (r *Reconciler[T]) liveReader() client.Reader {
	if r.apiReader == nil {
		return r.client
	}
	return r.apiReader
}

type listFunc func(ctx context.Context, gvk schema.GroupVersionKind) ([]runtime.Object, error)

// listCached lists the cached resources of a kind that claim absPath.
func (r *Reconciler[T]) listCached(absPath string) listFunc {
	return func(ctx context.Context, gvk schema.GroupVersionKind) ([]runtime.Object, error) {
		list, err := r.newList(gvk)
		if err != nil {
			return nil, err
		}
		if err := r.client.List(ctx, list, client.MatchingFields{PathIndex: absPath}); err != nil {
			return nil, fmt.Errorf("list %s by path: %w", gvk.Kind, err)
		}
		return meta.ExtractList(list)
	}
}

// listLive lists the resources of a kind from the API server, page by page, in the watched namespaces only, since
// the operator may be granted nothing outside them.
func (r *Reconciler[T]) listLive(ctx context.Context, gvk schema.GroupVersionKind) ([]runtime.Object, error) {
	reader := r.liveReader()
	namespaces := r.opts.Namespaces
	if len(namespaces) == 0 {
		namespaces = []string{metav1.NamespaceAll}
	}
	var items []runtime.Object
	for _, namespace := range namespaces {
		next := ""
		for {
			list, err := r.newList(gvk)
			if err != nil {
				return nil, err
			}
			if err := reader.List(ctx, list, client.InNamespace(namespace), client.Limit(liveListPage),
				client.Continue(next)); err != nil {
				return nil, fmt.Errorf("list %s: %w", gvk.Kind, err)
			}
			page, err := meta.ExtractList(list)
			if err != nil {
				return nil, err
			}
			items = append(items, page...)
			if next = list.GetContinue(); next == "" {
				break
			}
		}
	}
	return items, nil
}

func (r *Reconciler[T]) newList(gvk schema.GroupVersionKind) (client.ObjectList, error) {
	list, err := r.scheme.New(gvk.GroupVersion().WithKind(gvk.Kind + "List"))
	if err != nil {
		return nil, fmt.Errorf("list type for %s: %w", gvk.Kind, err)
	}
	return list.(client.ObjectList), nil
}

// findOlderOwner returns the oldest older owner of absPath among kinds, and the kinds that have any other resource
// claiming absPath.
func (r *Reconciler[T]) findOlderOwner(ctx context.Context, kinds []schema.GroupVersionKind, list listFunc, obj T,
	absPath, server, serverID string) (owner, bool, []schema.GroupVersionKind, error) {
	var found []owner
	var claiming []schema.GroupVersionKind
	for _, gvk := range kinds {
		items, err := list(ctx, gvk)
		if err != nil {
			return owner{}, false, nil, err
		}
		for _, item := range items {
			other, ok := item.(Object)
			if !ok || other.GetUID() == obj.GetUID() || !slices.Contains(PathIndexFunc(other), absPath) {
				continue
			}
			if !slices.Contains(claiming, gvk) {
				claiming = append(claiming, gvk)
			}
			candidate := owner{kind: gvk.Kind, obj: other}
			if !precedes(candidate, owner{kind: r.kind, obj: obj}, absPath) {
				continue
			}
			if r.sameServer(ctx, other, server, serverID) {
				found = append(found, candidate)
			}
		}
	}
	if len(found) == 0 {
		return owner{}, false, claiming, nil
	}
	return slices.MinFunc(found, compareOwners), true, claiming, nil
}

// precedes reports whether candidate keeps absPath against self. Creation timestamps have a resolution of one
// second, so on a tie an existing claim wins over a resource that does not hold the path yet.
func precedes(candidate, self owner, absPath string) bool {
	ct, st := candidate.obj.GetCreationTimestamp(), self.obj.GetCreationTimestamp()
	if ct.Equal(&st) && !slices.Contains(PathIndexFunc(self.obj), absPath) {
		return true
	}
	return compareOwners(candidate, self) < 0
}

// compareOwners orders by creationTimestamp, then by kind, namespace and name so that ties have one winner.
func compareOwners(a, b owner) int {
	at, bt := a.obj.GetCreationTimestamp(), b.obj.GetCreationTimestamp()
	if !at.Equal(&bt) {
		if at.Before(&bt) {
			return -1
		}
		return 1
	}
	return cmp.Or(cmp.Compare(a.kind, b.kind), cmp.Compare(a.obj.GetNamespace(), b.obj.GetNamespace()),
		cmp.Compare(a.obj.GetName(), b.obj.GetName()))
}

func (r *Reconciler[T]) sameServer(ctx context.Context, other Object, server, serverID string) bool {
	if recorded := other.GetResourceStatus().ServerID; recorded != "" && serverID != "" {
		return recorded == serverID
	}
	s, err := r.server(ctx, other)
	// An unresolvable server cannot prove that the paths differ, so the older claim stands.
	return err != nil || s.Key == server
}

// server returns the server behind obj's connection; without a ServerResolver the connection is the server.
func (r *Reconciler[T]) server(ctx context.Context, obj Object) (Server, error) {
	ref := obj.GetResourceSpec().ConnectionRef
	if resolver, ok := r.conns.(ServerResolver); ok {
		return resolver.ServerFor(ctx, obj.GetNamespace(), ref)
	}
	return Server{Key: ConnectionKey(obj.GetNamespace(), ref)}, nil
}

// ClusterIDOf returns the cluster id of the server behind c; ok=false when c cannot report one.
func ClusterIDOf(ctx context.Context, c openbao.Client) (id string, ok bool, err error) {
	identifier, ok := c.(ClusterIdentifier)
	if !ok {
		return "", false, nil
	}
	id, err = identifier.ClusterID(ctx)
	if err != nil {
		return "", true, fmt.Errorf("read the cluster id: %w", err)
	}
	return id, true, nil
}

// freshClusterIdentifier is a ClusterIdentifier that can bypass its cached cluster id.
type freshClusterIdentifier interface {
	FreshClusterID(ctx context.Context) (string, error)
}

// serverID returns the durable identity of the server behind bao: its cluster id, or serverKey for clients that
// cannot report one. fresh skips the client's cache; destructive operations and creates use it.
func serverID(ctx context.Context, bao openbao.Client, serverKey string, fresh bool) (string, error) {
	if f, ok := bao.(freshClusterIdentifier); ok && fresh {
		id, err := f.FreshClusterID(ctx)
		if err != nil {
			return "", fmt.Errorf("read the cluster id: %w", err)
		}
		return id, nil
	}
	id, ok, err := ClusterIDOf(ctx, bao)
	if !ok {
		return serverKey, nil
	}
	return id, err
}

// pathLocks serialises the creates of one path on one server within the process, so that of two resources that
// both read the object as absent only the first creates it; the second reads it again and finds it.
type pathLocks struct {
	mu    sync.Mutex
	locks map[string]*pathLock
}

type pathLock struct {
	held  chan struct{}
	users int
}

func (l *pathLocks) lock(ctx context.Context, key string) (unlock func(), err error) {
	l.mu.Lock()
	if l.locks == nil {
		l.locks = map[string]*pathLock{}
	}
	pl, ok := l.locks[key]
	if !ok {
		pl = &pathLock{held: make(chan struct{}, 1)}
		l.locks[key] = pl
	}
	pl.users++
	l.mu.Unlock()
	leave := func() {
		l.mu.Lock()
		defer l.mu.Unlock()
		if pl.users--; pl.users == 0 {
			delete(l.locks, key)
		}
	}
	select {
	case pl.held <- struct{}{}:
		return func() { <-pl.held; leave() }, nil
	case <-ctx.Done():
		leave()
		return nil, ctx.Err()
	}
}

// ConnectionKey names the connection a resource in fromNamespace references: "OpenBaoConnection/<namespace>/<name>"
// or "ClusterOpenBaoConnection/<name>". It keys ConnectionIndex and labels the metrics.
func ConnectionKey(fromNamespace string, ref commonv1alpha1.ConnectionReference) string {
	if ref.Kind == commonv1alpha1.ConnectionKindCluster {
		return string(ref.Kind) + "/" + ref.Name
	}
	return string(commonv1alpha1.ConnectionKindNamespaced) + "/" + fromNamespace + "/" + ref.Name
}

// markerVerdict is the result of checking an observed object's ownership marker.
type markerVerdict int

const (
	markerAbsent markerVerdict = iota
	markerOurs
	markerStale
	markerForeignCluster
)

func (r *Reconciler[T]) checkMarker(obj T, observed Payload) markerVerdict {
	marker, ok := any(r.adapter).(Marker[T])
	if !ok || r.sharedPath() {
		return markerAbsent
	}
	m, ok := marker.MarkerOf(observed)
	switch {
	case !ok:
		return markerAbsent
	case m.ClusterID != r.opts.ClusterID:
		return markerForeignCluster
	case m.UID == string(obj.GetUID()):
		return markerOurs
	default:
		return markerStale
	}
}

func (r *Reconciler[T]) mark(obj T, desired Payload) Payload {
	marker, ok := any(r.adapter).(Marker[T])
	if !ok || r.sharedPath() {
		return desired
	}
	return marker.Mark(obj, desired, OwnershipMarker{
		ManagedBy: r.opts.FieldOwner,
		ClusterID: r.opts.ClusterID,
		UID:       string(obj.GetUID()),
	})
}
