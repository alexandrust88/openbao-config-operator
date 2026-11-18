package engine

import (
	"cmp"
	"context"
	cryptorand "crypto/rand"
	"errors"
	"fmt"
	"maps"
	"math/rand/v2"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/events"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/apiutil"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/metrics"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	commonv1alpha1 "github.com/alexandrust88/openbao-config-operator/api/common/v1alpha1"
	"github.com/alexandrust88/openbao-config-operator/internal/openbao"
)

// Defaults applied by New to zero Options fields.
const (
	DefaultFinalizer  = "obco.dev/finalizer"
	DefaultFieldOwner = "openbao-config-operator"
)

const (
	// retryInterval caps the backoff of unresolved references.
	retryInterval = 30 * time.Second
	// maxConnectionRetry caps the backoff while a connection is not ready; the connection watch re-queues sooner.
	maxConnectionRetry = time.Minute
	minReferenceRetry  = 2 * time.Second
	inProgressInterval = 10 * time.Second
	// pausedRecheck is how often a resource paused through its Namespace or server checks for the resume, since
	// those annotations do not trigger a reconcile of the resource.
	pausedRecheck  = time.Minute
	managedByLabel = "app.kubernetes.io/managed-by"
	annotationTrue = "true"
	labelKind      = "kind"
)

var (
	writesTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "obco_writes_total",
		Help: "Writes the engine sent to OpenBao, by kind.",
	}, []string{labelKind})
	registerMetrics sync.Once
)

// Options tunes a Reconciler.
type Options struct {
	// DriftIntervalCritical is the base period between drift checks of TierAccessCritical kinds (default 15m).
	DriftIntervalCritical time.Duration
	// DriftIntervalStandard is the base period between drift checks of the other kinds (default 1h).
	DriftIntervalStandard time.Duration
	// MaxConcurrentReconciles is passed to the controller.
	MaxConcurrentReconciles int
	// Finalizer guards deletion when the effective deletionPolicy is Delete.
	Finalizer string
	// FieldOwner is the field manager of the engine's patches and the managedBy of ownership markers.
	FieldOwner string
	// HashKey keys the HMAC in status.writeOnlyHash; see LoadOrCreateHashKey. When empty, New uses a random
	// per-process key, so every restart rewrites each object that has write-only inputs once.
	HashKey []byte
	// WriteBudgetPerMinute caps the writes per minute to one server (default 60, negative for no cap). Every
	// Reconciler of a manager shares the budget; the first one built sets the cap.
	WriteBudgetPerMinute int
	// ClusterID is written into ownership markers; a marker with another cluster id gives the reason Conflict.
	ClusterID string
	// Namespaces are the namespaces the manager watches; empty means all. Live reads past the cache stay in them.
	Namespaces []string
}

// +kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch;create;update
// +kubebuilder:rbac:groups="",resources=namespaces,verbs=get;list;watch
// +kubebuilder:rbac:groups=events.k8s.io,resources=events,verbs=create;patch

// SetupOption customises the controller builder, for example to add watches on referenced kinds.
type SetupOption func(*builder.Builder)

// Reconciler reconciles one managed kind through its Adapter.
type Reconciler[T Object] struct {
	client   client.Client
	scheme   *runtime.Scheme
	recorder events.EventRecorder
	conns    Connections
	adapter  Adapter[T]
	newObj   func() T
	resolver Resolver
	opts     Options
	kind     string
	group    string
	shared   *shared
	due      *schedule
	ready    *readiness
	// applied maps a resource to the digest of the desired payload its last successful pass applied or observed. A
	// payload rendered from referents (an accessor, an id) can change while the spec stays the same.
	applied sync.Map

	// apiReader reads past the cache; nil falls back to client.
	apiReader client.Reader
}

// New returns a Reconciler for the kind produced by newObj.
func New[T Object](c client.Client, scheme *runtime.Scheme, recorder events.EventRecorder, conns Connections,
	adapter Adapter[T], newObj func() T, opts Options) *Reconciler[T] {
	opts.DriftIntervalCritical = cmp.Or(opts.DriftIntervalCritical, DefaultDriftIntervalCritical)
	opts.DriftIntervalStandard = cmp.Or(opts.DriftIntervalStandard, DefaultDriftIntervalStandard)
	if opts.Finalizer == "" {
		opts.Finalizer = DefaultFinalizer
	}
	if opts.FieldOwner == "" {
		opts.FieldOwner = DefaultFieldOwner
	}
	if len(opts.HashKey) == 0 {
		opts.HashKey = make([]byte, hashKeySize)
		_, _ = cryptorand.Read(opts.HashKey)
	}
	registerMetrics.Do(func() {
		metrics.Registry.MustRegister(writesTotal, driftLag, driftRepairs, conflictsTotal, budgetExhausted, resourceReady)
	})
	r := &Reconciler[T]{
		client:   c,
		scheme:   scheme,
		recorder: recorder,
		conns:    conns,
		adapter:  adapter,
		newObj:   newObj,
		resolver: NewResolver(c),
		opts:     opts,
		shared:   sharedFor(scheme, opts.WriteBudgetPerMinute),
		due:      &schedule{},
		ready:    &readiness{},
	}
	if gvk, err := apiutil.GVKForObject(newObj(), scheme); err == nil {
		r.kind, r.group = gvk.Kind, gvk.Group
	}
	return r
}

// SetupWithManager registers the controller, the path ownership index, and watches on referenced Secrets and kinds.
func (r *Reconciler[T]) SetupWithManager(mgr ctrl.Manager, opts ...SetupOption) error {
	obj := r.newObj()
	gvk, err := apiutil.GVKForObject(obj, r.scheme)
	if err != nil {
		return err
	}
	if !r.sharedPath() {
		if err := mgr.GetFieldIndexer().IndexField(context.Background(), r.newObj(), PathIndex, PathIndexFunc); err != nil {
			return fmt.Errorf("index owned paths of %s: %w", gvk.Kind, err)
		}
		r.shared.addKind(gvk)
	}
	r.apiReader = mgr.GetAPIReader()
	if err := mgr.GetFieldIndexer().IndexField(context.Background(), r.newObj(), ConnectionIndex, ConnectionIndexFunc); err != nil {
		return fmt.Errorf("index connections of %s: %w", gvk.Kind, err)
	}
	b := ctrl.NewControllerManagedBy(mgr).
		For(obj, builder.WithPredicates(predicate.Or[client.Object](predicate.GenerationChangedPredicate{},
			annotationChanged(commonv1alpha1.AnnotationPaused, commonv1alpha1.AnnotationAllowDestructiveDelete)))).
		Named(strings.ToLower(gvk.Kind) + "." + gvk.Group).
		WithOptions(controller.Options{MaxConcurrentReconciles: r.opts.MaxConcurrentReconciles})
	if err := r.watchConnections(mgr, b); err != nil {
		return err
	}
	if r.destructive() {
		// A guarded delete gets no event of its own when its Namespace starts terminating, and must orphan then.
		b = b.Watches(&corev1.Namespace{}, handler.EnqueueRequestsFromMapFunc(r.deletingInNamespace),
			builder.WithPredicates(predicate.NewPredicateFuncs(func(o client.Object) bool {
				return !o.GetDeletionTimestamp().IsZero()
			})))
	}
	if _, ok := any(r.adapter).(OutputWriter[T]); ok {
		// A deleted or edited output Secret re-queues its owner, which writes it again or reports it undelivered.
		b = b.Owns(&corev1.Secret{}, builder.OnlyMetadata)
	}
	if refs, ok := any(r.adapter).(SecretReferencer[T]); ok {
		if err := IndexSecretRefs(context.Background(), mgr.GetFieldIndexer(), r.newObj(), refs); err != nil {
			return fmt.Errorf("index secret refs of %s: %w", gvk.Kind, err)
		}
		enqueue, err := EnqueueForSecret(mgr.GetClient(), r.scheme, r.newObj())
		if err != nil {
			return err
		}
		// Metadata only: the engine needs to know that a Secret changed, never to cache its data.
		b = b.Watches(&corev1.Secret{}, enqueue, builder.OnlyMetadata)
	}
	if refs, ok := any(r.adapter).(ReferenceWatcher[T]); ok {
		if err := indexReferences(context.Background(), mgr.GetFieldIndexer(), r.newObj(), refs); err != nil {
			return fmt.Errorf("%s: %w", gvk.Kind, err)
		}
		enqueue, err := EnqueueForReferent(mgr.GetClient(), r.scheme, r.newObj())
		if err != nil {
			return err
		}
		for _, kind := range refs.ReferencedKinds() {
			served, err := r.served(mgr, kind)
			if err != nil {
				return err
			}
			if served {
				b = b.Watches(kind, enqueue)
			}
		}
	}
	for _, opt := range opts {
		opt(b)
	}
	return b.Complete(r)
}

// annotationChanged passes updates that change one of keys; annotation edits do not bump the generation.
func annotationChanged(keys ...string) predicate.Funcs {
	return predicate.Funcs{UpdateFunc: func(e event.UpdateEvent) bool {
		if e.ObjectOld == nil || e.ObjectNew == nil {
			return false
		}
		return slices.ContainsFunc(keys, func(key string) bool {
			return e.ObjectOld.GetAnnotations()[key] != e.ObjectNew.GetAnnotations()[key]
		})
	}}
}

// Reconcile implements the sequence described in docs/architecture.md, section "The engine".
func (r *Reconciler[T]) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	obj := r.newObj()
	if err := r.client.Get(ctx, req.NamespacedName, obj); err != nil {
		if apierrors.IsNotFound(err) {
			r.forget(req.NamespacedName)
		}
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	ctx = openbao.WithResourceAttribution(ctx, r.attribution(obj))
	p := &pass[T]{obj: obj, before: obj.DeepCopyObject().(client.Object)}
	by, recheck, err := r.pausedBy(ctx, obj)
	if err != nil {
		return ctrl.Result{}, err
	}
	if by != "" {
		return r.pause(ctx, obj, p.before, by, recheck)
	}
	if !obj.GetDeletionTimestamp().IsZero() {
		return r.reconcileDelete(openbao.WithPriority(ctx), obj, p.before)
	}
	if res, done, err := r.connect(ctx, p); done {
		return res, err
	}
	if reconnecting(obj) {
		if err := r.shared.ramp.wait(ctx, p.server); err != nil {
			return ctrl.Result{}, err
		}
	}
	if res, done, err := r.prepare(ctx, p); done {
		return res, err
	}
	opCtx := ctx
	if !p.settled && !reconnecting(obj) {
		opCtx = openbao.WithPriority(ctx)
	}
	if res, done, err := r.locate(ctx, opCtx, p); done {
		return res, err
	}
	observed, found, err := r.observe(opCtx, p.bao, obj, p.path)
	if err != nil {
		return r.fail(ctx, obj, p.before, commonv1alpha1.ReasonReconcileError, err)
	}
	if p.settled {
		if lag, ok := r.due.lag(req.NamespacedName, time.Now()); ok {
			driftLag.WithLabelValues(ConnectionKey(obj.GetNamespace(), obj.GetResourceSpec().ConnectionRef)).Set(lag.Seconds())
		}
	}
	if observeOnly(obj) {
		return r.reconcileObserve(opCtx, p, observed, found)
	}
	return r.converge(ctx, opCtx, p, observed, found)
}

// pass carries the state of one reconcile of obj between its phases.
type pass[T Object] struct {
	obj    T
	before client.Object
	bao    openbao.Client
	server string
	// serverID is the durable identity of the server (its cluster id) that status.serverID is compared with.
	serverID string
	// desired, fields, hash, nonce and digest describe the spec; settled reports a drift check of an unchanged, Ready
	// spec.
	desired             Payload
	fields              FieldSpec
	hash, nonce, digest string
	settled             bool
	// path is relative to bao's namespace; absPath includes the namespace.
	path, namespace, absPath string
}

func (r *Reconciler[T]) attribution(obj T) string {
	return r.group + "/" + r.kind + "/" + obj.GetNamespace() + "/" + obj.GetName()
}

// connect resolves the connection, the OpenBao namespace and the server.
func (r *Reconciler[T]) connect(ctx context.Context, p *pass[T]) (ctrl.Result, bool, error) {
	obj, spec := p.obj, p.obj.GetResourceSpec()
	base, err := r.conns.For(ctx, obj.GetNamespace(), spec.ConnectionRef)
	if err != nil {
		res, err := r.connectionFailed(ctx, obj, p.before, err)
		return res, true, err
	}
	if err := openbao.ValidateNamespace(spec.Namespace); err != nil {
		res, err := r.fail(ctx, obj, p.before, commonv1alpha1.ReasonInvalidSpec,
			fmt.Errorf("spec.openbaoNamespace: %w: %w", ErrInvalidSpec, err))
		return res, true, err
	}
	p.bao = base
	if spec.Namespace != "" {
		p.bao = base.WithNamespace(spec.Namespace)
	}
	if res, done, err := r.refuseRootOnly(ctx, p); done {
		return res, done, err
	}
	if status := obj.GetResourceStatus(); status.Acquired != "" &&
		!slices.Equal(namespaceSegments(status.Namespace), namespaceSegments(p.bao.Namespace())) {
		// The connection's base namespace changed; writing now would create a second object elsewhere.
		err := fmt.Errorf("the object was acquired in OpenBao namespace %q but the connection now resolves to %q; "+
			"restore the connection namespace or recreate this resource", status.Namespace, p.bao.Namespace())
		r.recorder.Eventf(obj, nil, corev1.EventTypeWarning, commonv1alpha1.ReasonNamespaceChanged, "Reconcile",
			err.Error())
		res, err := r.stall(ctx, obj, p.before, commonv1alpha1.ReasonNamespaceChanged, err, r.jitter(obj))
		return res, true, err
	}
	server, err := r.server(ctx, obj)
	if err != nil {
		res, err := r.connectionFailed(ctx, obj, p.before, err)
		return res, true, err
	}
	p.server = server.Key
	if p.serverID, err = serverID(ctx, base, server.Key, false); err != nil {
		res, err := r.fail(ctx, obj, p.before, commonv1alpha1.ReasonReconcileError, err)
		return res, true, err
	}
	// Observe checks too: rebinding status.serverID there would turn another server's object into ours.
	if status := obj.GetResourceStatus(); status.ServerID != "" && status.ServerID != p.serverID && status.Path != "" {
		err := fmt.Errorf("the object was acquired on OpenBao server %s but the connection now reaches server %s; "+
			"restore the connection, or release the object as docs/architecture.md describes", status.ServerID, p.serverID)
		r.recorder.Eventf(obj, nil, corev1.EventTypeWarning, commonv1alpha1.ReasonServerChanged, "Reconcile", err.Error())
		res, err := r.stall(ctx, obj, p.before, commonv1alpha1.ReasonServerChanged, err, r.jitter(obj))
		return res, true, err
	}
	return ctrl.Result{}, false, nil
}

// prepare syncs the finalizer and builds the desired payload, its comparison rules, the write-only hash and the
// rotation nonce. A settled object with drift detection disabled stops here.
func (r *Reconciler[T]) prepare(ctx context.Context, p *pass[T]) (ctrl.Result, bool, error) {
	obj := p.obj
	if err := r.syncFinalizer(ctx, obj); err != nil {
		return ctrl.Result{}, true, err
	}
	p.before = obj.DeepCopyObject().(client.Object)
	resolver := r.resolver
	if observeOnly(obj) {
		resolver = observeResolver{r.resolver}
	}
	desired, err := r.adapter.Desired(ctx, resolver, obj)
	if err == nil {
		p.fields, err = r.fields(ctx, resolver, obj)
	}
	if err != nil {
		res, err := r.fail(ctx, obj, p.before, commonv1alpha1.ReasonReconcileError, err)
		return res, true, err
	}
	p.desired = desired
	if !observeOnly(obj) {
		p.desired = r.mark(obj, desired)
		hashKey := ObjectHashKey(r.opts.HashKey, obj.GetUID(), schema.GroupKind{Group: r.group, Kind: r.kind})
		p.hash = WriteOnlyHash(hashKey, p.desired, p.fields)
	}
	p.nonce = r.rotationNonce(obj)
	status := obj.GetResourceStatus()
	p.settled = (status.Acquired != "" || observeOnly(obj)) &&
		p.hash == status.WriteOnlyHash && p.nonce == status.RotationNonce && p.serverID == status.ServerID &&
		Reconciled(obj)
	p.digest = WriteOnlyHash(r.opts.HashKey, p.desired, FieldSpec{WriteOnly: slices.Collect(maps.Keys(p.desired))})
	applied, _ := r.applied.Load(client.ObjectKeyFromObject(obj))
	if _, refreshes := any(r.adapter).(OutputRefresher[T]); p.settled && driftDisabled(obj) && !refreshes &&
		applied == p.digest {
		r.recordReady(obj)
		return ctrl.Result{}, true, nil
	}
	return ctrl.Result{}, false, nil
}

// locate resolves the path and refuses a path that an older resource owns on the same server.
func (r *Reconciler[T]) locate(ctx, opCtx context.Context, p *pass[T]) (ctrl.Result, bool, error) {
	path, err := r.path(opCtx, p.bao, p.obj)
	if err == nil {
		if err = openbao.ValidatePath(path); err != nil {
			err = fmt.Errorf("%w: %w", ErrInvalidSpec, err)
		}
	}
	if err != nil {
		res, err := r.fail(ctx, p.obj, p.before, commonv1alpha1.ReasonReconcileError, err)
		return res, true, err
	}
	p.path = openbao.CanonicalPath(path)
	p.namespace = strings.Trim(p.bao.Namespace(), "/")
	p.absPath = joinPath(p.namespace, p.path)
	if observeOnly(p.obj) || r.sharedPath() {
		return ctrl.Result{}, false, nil
	}
	other, conflict, err := r.olderOwner(ctx, p.obj, p.absPath, p.server, p.serverID)
	if err != nil {
		res, err := r.fail(ctx, p.obj, p.before, commonv1alpha1.ReasonReconcileError, err)
		return res, true, err
	}
	if conflict {
		res, err := r.conflict(ctx, p.obj, p.before, fmt.Errorf("%s is managed by the older %s", p.absPath, other))
		return res, true, err
	}
	return ctrl.Result{}, false, nil
}

// converge applies ownership and creationPolicy, recreates a destructive object whose create-only fields changed
// when allowed, writes the difference and records the result.
func (r *Reconciler[T]) converge(ctx, opCtx context.Context, p *pass[T], observed Payload, found bool) (ctrl.Result, error) {
	obj := p.obj
	if r.recheckOwner(obj, p.absPath, found) {
		if !found {
			unlock, err := r.shared.creates.lock(opCtx, p.serverID+"\x00"+p.absPath)
			if err != nil {
				return ctrl.Result{}, err
			}
			defer unlock()
			if observed, found, err = r.observe(opCtx, p.bao, obj, p.path); err != nil {
				return r.fail(ctx, obj, p.before, commonv1alpha1.ReasonReconcileError, err)
			}
		}
		if res, done, err := r.checkLiveOwner(ctx, p, observed, found); done {
			return res, err
		}
	}
	acquired, reason, err := r.acquire(obj, p.absPath, observed, found)
	switch {
	case reason == commonv1alpha1.ReasonConflict:
		return r.conflict(ctx, obj, p.before, err)
	case err != nil:
		return r.stall(ctx, obj, p.before, reason, err, r.jitter(obj))
	}
	var recreate []string
	if found && r.destructive() {
		if recreate = createOnlyChanges(p.desired, observed, p.fields); len(recreate) > 0 && !allowsDestructive(obj) {
			return r.guard(ctx, obj, p.before, fmt.Errorf("changing %s recreates %s; set the annotation %s=%q to allow it",
				strings.Join(recreate, ", "), p.absPath, commonv1alpha1.AnnotationAllowDestructiveDelete, annotationTrue))
		}
	}
	if !found || len(recreate) > 0 {
		if err := r.confirmServer(opCtx, p); err != nil {
			return r.fail(ctx, obj, p.before, commonv1alpha1.ReasonServerChanged, err)
		}
	}
	if len(recreate) > 0 {
		if err := r.deleteAt(opCtx, p.bao, obj, p.path); err != nil {
			return r.fail(ctx, obj, p.before, commonv1alpha1.ReasonReconcileError, fmt.Errorf("recreate %s: %w", p.absPath, err))
		}
		r.recorder.Eventf(obj, nil, corev1.EventTypeNormal, "Recreated", "Delete",
			"deleted %s to recreate it: %s changed", p.absPath, strings.Join(recreate, ", "))
		found, observed = false, nil
	}
	var differing []string
	if found {
		differing = Diff(p.desired, observed, p.fields)
	}
	result, err := r.sync(opCtx, p, observed, found, differing)
	if err != nil && !found && obj.GetResourceStatus().Acquired == "" {
		r.releaseClaim(opCtx, p, result.sent)
	}
	var budgetErr *budgetError
	switch {
	case errors.As(err, &budgetErr):
		return r.overBudget(ctx, obj, p.before, budgetErr)
	case errors.Is(err, ErrSecretNotOwned):
		return r.stall(ctx, obj, p.before, commonv1alpha1.ReasonSecretNotOwned, err, r.jitter(obj))
	case err != nil:
		return r.fail(ctx, obj, p.before, commonv1alpha1.ReasonReconcileError, err)
	}
	if result.wrote && p.settled {
		driftRepairs.WithLabelValues(r.kind).Inc()
	}
	// A path that embeds a server-assigned id (copied to the status by the StatusWriter) is final only now.
	if path, err := r.path(opCtx, p.bao, obj); err == nil {
		p.path = openbao.CanonicalPath(path)
		p.absPath = joinPath(p.namespace, p.path)
	}
	status := obj.GetResourceStatus()
	status.Namespace = p.namespace
	status.Path = p.absPath
	status.ServerID = p.serverID
	status.Acquired = acquired
	status.WriteOnlyHash = p.hash
	status.RotationNonce = p.nonce
	status.ObservedGeneration = obj.GetGeneration()
	if result.unconfirmed != nil {
		return r.unconfirmed(ctx, obj, p.before, result.unconfirmed)
	}
	r.setConditions(obj, metav1.ConditionTrue, commonv1alpha1.ReasonAvailable,
		metav1.ConditionTrue, commonv1alpha1.ReasonReconcileSuccess, "")
	r.applied.Store(client.ObjectKeyFromObject(obj), p.digest)
	return r.requeueDrift(ctx, obj, p.before)
}

// Reconciled reports that the last pass applied the current generation: Ready and Synced are both True for it. A
// failed pass advances status.observedGeneration and may leave an earlier Ready as it was, so neither alone proves
// the spec was applied.
func Reconciled(obj Object) bool {
	return currentlyTrue(obj, commonv1alpha1.ConditionReady) && currentlyTrue(obj, commonv1alpha1.ConditionSynced)
}

func currentlyTrue(obj Object, conditionType string) bool {
	c := meta.FindStatusCondition(obj.GetResourceStatus().Conditions, conditionType)
	return c != nil && c.Status == metav1.ConditionTrue && c.ObservedGeneration == obj.GetGeneration()
}

func driftDisabled(obj Object) bool {
	return obj.GetResourceSpec().DriftDetection == commonv1alpha1.DriftDetectionDisabled
}

// requeueDrift patches the status and schedules the next drift check.
func (r *Reconciler[T]) requeueDrift(ctx context.Context, obj T, before client.Object) (ctrl.Result, error) {
	if err := r.patchStatus(ctx, obj, before); err != nil {
		return ctrl.Result{}, err
	}
	key := client.ObjectKeyFromObject(obj)
	if driftDisabled(obj) {
		r.due.forget(key)
		return ctrl.Result{}, nil
	}
	after := r.jitter(obj)
	r.due.set(key, time.Now().Add(after))
	return ctrl.Result{RequeueAfter: after}, nil
}

// reconcileObserve reports how the object differs from the spec without writing (managementPolicy Observe).
func (r *Reconciler[T]) reconcileObserve(ctx context.Context, p *pass[T], observed Payload, found bool) (ctrl.Result, error) {
	obj, before, absPath := p.obj, p.before, p.absPath
	status := obj.GetResourceStatus()
	if !found {
		return r.stall(ctx, obj, before, commonv1alpha1.ReasonNotFound, fmt.Errorf("%s does not exist", absPath),
			r.jitter(obj))
	}
	if sw, ok := any(r.adapter).(StatusWriter[T]); ok {
		if err := sw.WriteStatus(ctx, p.bao, obj, observed); err != nil {
			return r.fail(ctx, obj, before, commonv1alpha1.ReasonReconcileError, err)
		}
	}
	status.ObservedGeneration = obj.GetGeneration()
	// Observing never touches ownership; serverID of a resource without a path only names the server its observed
	// status (status.id) came from, which references check.
	if status.Path == "" && status.Acquired == "" {
		status.ServerID = p.serverID
	}
	differing := Diff(p.desired, observed, p.fields)
	if len(differing) == 0 {
		r.applied.Store(client.ObjectKeyFromObject(obj), p.digest)
		r.setConditions(obj, metav1.ConditionTrue, commonv1alpha1.ReasonObserved,
			metav1.ConditionTrue, commonv1alpha1.ReasonObserved, "")
		return r.requeueDrift(ctx, obj, before)
	}
	message := fmt.Sprintf("%s differs from the spec in: %s", absPath, strings.Join(differing, ", "))
	if previous := meta.FindStatusCondition(status.Conditions, commonv1alpha1.ConditionSynced); previous == nil ||
		previous.Reason != commonv1alpha1.ReasonDrifted || previous.Message != message {
		r.recorder.Eventf(obj, nil, corev1.EventTypeWarning, commonv1alpha1.ReasonDrifted, "Observe", message)
	}
	r.setConditions(obj, metav1.ConditionTrue, commonv1alpha1.ReasonObserved,
		metav1.ConditionFalse, commonv1alpha1.ReasonDrifted, message)
	return r.requeueDrift(ctx, obj, before)
}

// acquire applies the ownership rules and creationPolicy and returns the acquired state, or a stall reason and its
// cause. Ownership is checked by the server-side marker first, then by status.acquired with a matching path.
func (r *Reconciler[T]) acquire(obj T, absPath string, observed Payload, found bool) (commonv1alpha1.AcquiredState, string, error) {
	status := obj.GetResourceStatus()
	// A path without Acquired is the claim recorded before this resource's own create.
	ours := openbao.CanonicalPath(status.Path) == absPath
	creationPolicy := obj.GetResourceSpec().CreationPolicy
	if creationPolicy == "" {
		creationPolicy = commonv1alpha1.CreationPolicyCreate
	}
	if !found {
		if creationPolicy == commonv1alpha1.CreationPolicyAdopt && (!ours || status.Acquired != commonv1alpha1.AcquiredCreated) {
			return "", commonv1alpha1.ReasonNotFound, fmt.Errorf("%s does not exist and creationPolicy is Adopt", absPath)
		}
		return commonv1alpha1.AcquiredCreated, "", nil
	}
	ours, err := r.ownsExisting(obj, absPath, observed)
	switch {
	case err != nil:
		return "", commonv1alpha1.ReasonConflict, err
	case ours:
		return cmp.Or(status.Acquired, commonv1alpha1.AcquiredCreated), "", nil
	default:
		r.recorder.Eventf(obj, nil, corev1.EventTypeNormal, "Adopted", "Adopt", "adopted %s", absPath)
		return commonv1alpha1.AcquiredAdopted, "", nil
	}
}

// ownsExisting reports whether obj owns the existing object at absPath, or why it may not take it over; (false, nil)
// means it may adopt the object.
func (r *Reconciler[T]) ownsExisting(obj T, absPath string, observed Payload) (bool, error) {
	ours := openbao.CanonicalPath(obj.GetResourceStatus().Path) == absPath
	switch r.checkMarker(obj, observed) {
	case markerForeignCluster:
		return false, fmt.Errorf("%s carries the ownership marker of another cluster", absPath)
	case markerOurs:
		return true, nil
	case markerStale:
		ours = false
	case markerAbsent:
	}
	creationPolicy := cmp.Or(obj.GetResourceSpec().CreationPolicy, commonv1alpha1.CreationPolicyCreate)
	if !ours && creationPolicy == commonv1alpha1.CreationPolicyCreate && !r.singleton() {
		return false, fmt.Errorf("%s already exists and is not owned by this resource; "+
			"set creationPolicy to Adopt or CreateOrAdopt to take it over", absPath)
	}
	return ours, nil
}

// claim records the path and server in the status before a create, so that a lost status patch after the create
// cannot turn the operator's own object into a Conflict, and PathIndex shows the create to every other resource.
// Acquired stays unset until the create succeeded. Kinds created by a POST to a collection make no claim: the
// collection is shared by all their objects, and a lost status loses the server-assigned id anyway.
func (r *Reconciler[T]) claim(ctx context.Context, p *pass[T]) error {
	obj := p.obj
	status := obj.GetResourceStatus()
	if r.idInPath(obj) || status.Path == p.absPath && status.ServerID == p.serverID {
		return nil
	}
	status.Namespace = p.namespace
	status.Path = p.absPath
	status.ServerID = p.serverID
	if err := r.patchStatus(ctx, obj, p.before); err != nil {
		return err
	}
	p.before = obj.DeepCopyObject().(client.Object)
	return nil
}

// recheckOwner reports that the claims on absPath must be read live before acquire: before a create, and before
// obj adopts an existing object it has not acquired.
func (r *Reconciler[T]) recheckOwner(obj T, absPath string, found bool) bool {
	status := obj.GetResourceStatus()
	acquired := status.Acquired != "" && openbao.CanonicalPath(status.Path) == absPath
	return !r.sharedPath() && !r.idInPath(obj) && (!found || !acquired)
}

// checkLiveOwner stops with Conflict when an existing object, or one read as absent and created meanwhile, has its
// path claimed by an older resource. The claims are read from the API server, since the cache may not show that
// claim yet.
func (r *Reconciler[T]) checkLiveOwner(ctx context.Context, p *pass[T], observed Payload, found bool) (ctrl.Result, bool, error) {
	if !found {
		return ctrl.Result{}, false, nil
	}
	// acquire refuses it anyway; a Create stuck on an existing object must not list on every pass.
	if _, err := r.ownsExisting(p.obj, p.absPath, observed); err != nil {
		return ctrl.Result{}, false, nil
	}
	other, conflict, err := r.olderOwnerLive(ctx, p.obj, p.absPath, p.server, p.serverID)
	if err != nil {
		res, err := r.fail(ctx, p.obj, p.before, commonv1alpha1.ReasonReconcileError, err)
		return res, true, err
	}
	if conflict {
		res, err := r.conflict(ctx, p.obj, p.before, fmt.Errorf("%s is managed by the older %s", p.absPath, other))
		return res, true, err
	}
	return ctrl.Result{}, false, nil
}

// confirmServer re-reads the cluster id past the client's cache right before a create or a recreate, so that a
// different server behind the same address is not written under the binding of the old one.
func (r *Reconciler[T]) confirmServer(ctx context.Context, p *pass[T]) error {
	id, err := serverID(ctx, p.bao, p.server, true)
	if err != nil {
		return err
	}
	if id != p.serverID {
		return fmt.Errorf("%w: it was %s a moment ago and is %s now", errServerChanged, p.serverID, id)
	}
	return nil
}

// markCreated records and persists the acquisition as soon as the create succeeded, so that an error later in the
// pass (an output Secret that could not be written) leaves the object owned and deletable.
func (r *Reconciler[T]) markCreated(ctx context.Context, p *pass[T]) {
	obj := p.obj
	status := obj.GetResourceStatus()
	absPath := p.absPath
	if r.idInPath(obj) {
		if status.ID == "" {
			return
		}
		absPath = joinPath(p.namespace, openbao.CanonicalPath(r.adapter.Path(obj)))
	}
	status.Namespace, status.Path, status.ServerID = p.namespace, absPath, p.serverID
	status.Acquired = commonv1alpha1.AcquiredCreated
	status.WriteOnlyHash, status.RotationNonce = p.hash, p.nonce
	// A failed patch is retried by the status patch that ends this pass; the outputs must be delivered first.
	if err := r.patchStatus(ctx, obj, p.before); err == nil {
		p.before = obj.DeepCopyObject().(client.Object)
	}
}

// releaseClaim drops the claim of a create that wrote nothing, unless the object exists after all, so that only
// this resource's own create can later be taken for owned, never an object somebody else creates at the path.
func (r *Reconciler[T]) releaseClaim(ctx context.Context, p *pass[T], sent bool) {
	if sent {
		if _, found, err := r.observe(ctx, p.bao, p.obj, p.path); err != nil || found {
			return
		}
	}
	status := p.obj.GetResourceStatus()
	status.Path, status.Namespace, status.ServerID = "", "", ""
}

// idInPath reports a kind whose path embeds the server-assigned status.id; before the create the path is the
// collection the object is POSTed to.
func (r *Reconciler[T]) idInPath(obj T) bool {
	if _, ok := any(r.adapter).(PathResolver[T]); ok {
		return false
	}
	probe := obj.DeepCopyObject().(T)
	probe.GetResourceStatus().ID = obj.GetResourceStatus().ID + "-probe"
	return r.adapter.Path(probe) != r.adapter.Path(obj)
}

type syncResult struct {
	// sent reports that the write was sent, whatever its outcome.
	sent  bool
	wrote bool
	// unconfirmed is set when the read after the write did not return what was written.
	unconfirmed error
}

// budgetError reports that the server's write budget is spent. Its message leaves out the wait, so that a retry
// that is held back again does not patch the status.
type budgetError struct {
	server string
	wait   time.Duration
}

func (e *budgetError) Error() string {
	return fmt.Sprintf("the write budget of server %s is spent; the update is retried when the budget frees up", e.server)
}

// sync writes desired when the object is absent, differs, has new write-only inputs or a new rotation nonce, reads
// it back to confirm the write, and lets a StatusWriter copy server-side values into the status.
func (r *Reconciler[T]) sync(ctx context.Context, p *pass[T], observed Payload, found bool, differing []string) (syncResult, error) {
	obj, bao, path, desired, fields := p.obj, p.bao, p.path, p.desired, p.fields
	status := obj.GetResourceStatus()
	hashChanged := p.hash != status.WriteOnlyHash
	nonceChanged := p.nonce != status.RotationNonce
	var result syncResult
	if !found || len(differing) > 0 || hashChanged || nonceChanged {
		if err := r.checkOutputSecret(ctx, obj); err != nil {
			return result, err
		}
		_, regenerates := any(r.adapter).(OutputWriter[T])
		charged := rewrite(obj, found, hashChanged, regenerates)
		if charged {
			if ok, wait := r.shared.budget.take(p.server); !ok {
				budgetExhausted.WithLabelValues(p.server).Inc()
				return result, &budgetError{server: p.server, wait: wait}
			}
		}
		if !found {
			if err := r.claim(ctx, p); err != nil {
				return result, err
			}
		}
		result.sent = true
		response, err := r.write(ctx, bao, obj, path, writePayload(desired, fields, !found, nonceChanged), !found)
		switch {
		case errors.Is(err, ErrNothingWritten):
			if charged {
				r.shared.budget.refund(p.server)
			}
			if err := r.refreshOutputs(ctx, bao, obj, observed); err != nil {
				return result, err
			}
		case err != nil:
			return result, redactWriteOnly(err, desired, fields)
		default:
			result.wrote = true
			if !found {
				r.markCreated(ctx, p)
			}
			writesTotal.WithLabelValues(r.kind).Inc()
			r.recordWrite(obj, path, found, differing, hashChanged || nonceChanged)
			if err := r.writeOutputs(ctx, obj, response); err != nil {
				return result, err
			}
		}
		after, err := r.readBack(ctx, bao, obj, path, desired, fields)
		switch {
		case errors.Is(err, errUnconfirmed):
			result.unconfirmed = err
		case err != nil:
			return result, err
		}
		observed = after
	} else if err := r.refreshOutputs(ctx, bao, obj, observed); err != nil {
		return result, err
	}
	if hook, ok := any(r.adapter).(OutputsDeliveredHook[T]); ok {
		if err := hook.OutputsDelivered(ctx, bao, obj); err != nil {
			return result, err
		}
	}
	if sw, ok := any(r.adapter).(StatusWriter[T]); ok {
		return result, sw.WriteStatus(ctx, bao, obj, observed)
	}
	return result, nil
}

// ErrNothingWritten is returned by a Writer that sent no write request because the server already held the desired
// values, for example when they arrived between the engine's read and the write. The engine then records no write.
var ErrNothingWritten = errors.New("nothing to write")

// errUnconfirmed marks a read-back that does not match the write.
var errUnconfirmed = errors.New("OpenBao did not keep the written values")

// readBack reads the object after a write and returns the read and, when it differs from desired, the read together
// with an error wrapping errUnconfirmed that names the differing keys.
func (r *Reconciler[T]) readBack(ctx context.Context, bao openbao.Client, obj T, path string, desired Payload,
	fields FieldSpec) (Payload, error) {
	skipper, skip := any(r.adapter).(ReadBackSkipper[T])
	skip = skip && skipper.SkipReadBack(obj)
	_, needRead := any(r.adapter).(StatusWriter[T])
	if skip && !needRead {
		return nil, nil
	}
	observed, found, err := r.observe(ctx, bao, obj, path)
	switch {
	case err != nil:
		return nil, err
	case skip:
		return observed, nil
	case !found:
		return observed, fmt.Errorf("%w: %s is absent after the write", errUnconfirmed, path)
	}
	if differing := Diff(desired, observed, fields); len(differing) > 0 {
		return observed, fmt.Errorf("%w: %s reads back different %s", errUnconfirmed, path, strings.Join(differing, ", "))
	}
	return observed, nil
}

// unconfirmed reports a write that did not read back as written: a normalisation the adapter does not declare, or
// a read served before the write was visible. It retries with a backoff capped at the drift interval, so a kind that
// can never confirm writes only a few times instead of on every pass.
func (r *Reconciler[T]) unconfirmed(ctx context.Context, obj T, before client.Object, cause error) (ctrl.Result, error) {
	if synced := meta.FindStatusCondition(obj.GetResourceStatus().Conditions, commonv1alpha1.ConditionSynced); synced == nil ||
		synced.Reason != commonv1alpha1.ReasonOutOfSync {
		r.recorder.Eventf(obj, nil, corev1.EventTypeWarning, commonv1alpha1.ReasonOutOfSync, "Update", cause.Error())
	}
	after := backoff(obj, commonv1alpha1.ReasonOutOfSync, r.driftInterval(obj))
	r.setConditions(obj, metav1.ConditionFalse, commonv1alpha1.ReasonOutOfSync,
		metav1.ConditionFalse, commonv1alpha1.ReasonOutOfSync, cause.Error())
	if err := r.patchStatus(ctx, obj, before); err != nil {
		return ctrl.Result{}, err
	}
	return ctrl.Result{RequeueAfter: after}, nil
}

func (r *Reconciler[T]) reconcileDelete(ctx context.Context, obj T, before client.Object) (ctrl.Result, error) {
	if !controllerutil.ContainsFinalizer(obj, r.opts.Finalizer) {
		r.forget(client.ObjectKeyFromObject(obj))
		return ctrl.Result{}, nil
	}
	status := obj.GetResourceStatus()
	if r.deletionPolicy(obj) == commonv1alpha1.DeletionPolicyDelete && !observeOnly(obj) &&
		status.Acquired != "" && status.Path != "" {
		if r.destructive() && !allowsDestructive(obj) {
			if !r.namespaceTerminating(ctx, obj) {
				return r.guard(ctx, obj, before, fmt.Errorf("deleting %s destroys its data; set the annotation %s=%q to allow it",
					status.Path, commonv1alpha1.AnnotationAllowDestructiveDelete, annotationTrue))
			}
			// A terminating Namespace cannot wait for the annotation; orphaning is the only way out that destroys nothing.
			r.recorder.Eventf(obj, nil, corev1.EventTypeWarning, commonv1alpha1.ReasonOrphaned, "Delete",
				"left %s in OpenBao: the Namespace is terminating and the annotation %s is absent",
				status.Path, commonv1alpha1.AnnotationAllowDestructiveDelete)
			if err := r.patchFinalizer(ctx, obj, controllerutil.RemoveFinalizer); err != nil {
				return ctrl.Result{}, err
			}
			r.forget(client.ObjectKeyFromObject(obj))
			return ctrl.Result{}, nil
		}
		var other owner
		var owned bool
		var err error
		if !r.sharedPath() {
			other, owned, err = r.ownedByOther(ctx, obj)
		}
		switch {
		case err != nil:
			return r.cleanupBlocked(ctx, obj, before, err)
		case owned:
			r.recorder.Eventf(obj, nil, corev1.EventTypeWarning, commonv1alpha1.ReasonConflict, "Delete",
				"left %s in place: it is managed by the older %s", status.Path, other)
		default:
			err := r.deleteRemote(ctx, obj)
			switch {
			case errors.Is(err, errServerChanged), errors.Is(err, errForeignMarker):
				reason := commonv1alpha1.ReasonConflict
				if errors.Is(err, errServerChanged) {
					reason = commonv1alpha1.ReasonServerChanged
				}
				r.recorder.Eventf(obj, nil, corev1.EventTypeWarning, reason, "Delete", "left %s in OpenBao: %s",
					status.Path, err.Error())
			case errors.Is(err, ErrInProgress):
				return r.inProgress(ctx, obj, before, err)
			case errors.Is(err, ErrConnectionNotReady) && r.namespaceTerminating(ctx, obj):
				// The connection went with its Namespace; holding the finalizer would block the Namespace forever.
				r.recorder.Eventf(obj, nil, corev1.EventTypeWarning, commonv1alpha1.ReasonOrphaned, "Delete",
					"left %s in OpenBao: the Namespace is terminating and its connection is gone", status.Path)
			case err != nil:
				return r.cleanupBlocked(ctx, obj, before, err)
			default:
				r.recorder.Eventf(obj, nil, corev1.EventTypeNormal, "Deleted", "Delete", "deleted %s", status.Path)
			}
		}
	}
	if err := r.patchFinalizer(ctx, obj, controllerutil.RemoveFinalizer); err != nil {
		return ctrl.Result{}, err
	}
	r.forget(client.ObjectKeyFromObject(obj))
	return ctrl.Result{}, nil
}

// ownedByOther reports an older resource that owns status.path on the same server; the delete then leaves the
// object to it.
func (r *Reconciler[T]) ownedByOther(ctx context.Context, obj T) (owner, bool, error) {
	status := obj.GetResourceStatus()
	return r.olderOwner(ctx, obj, status.Path, r.deletingServer(ctx, obj).Key, status.ServerID)
}

// deletingServer returns the server of a resource being deleted, or its connection when the server, deleted along
// with it, no longer resolves.
func (r *Reconciler[T]) deletingServer(ctx context.Context, obj T) Server {
	server, err := r.server(ctx, obj)
	if err != nil {
		server.Key = ConnectionKey(obj.GetNamespace(), obj.GetResourceSpec().ConnectionRef)
	}
	return server
}

// namespaceTerminating reports that the Kubernetes Namespace of obj is being deleted.
func (r *Reconciler[T]) namespaceTerminating(ctx context.Context, obj T) bool {
	ns := &corev1.Namespace{}
	if err := r.client.Get(ctx, client.ObjectKey{Name: obj.GetNamespace()}, ns); err != nil {
		return false
	}
	return !ns.DeletionTimestamp.IsZero()
}

// deletingInNamespace returns the resources of this kind that are being deleted in the Namespace ns.
func (r *Reconciler[T]) deletingInNamespace(ctx context.Context, ns client.Object) []reconcile.Request {
	if len(r.opts.Namespaces) > 0 && !slices.Contains(r.opts.Namespaces, ns.GetName()) {
		return nil
	}
	var items []runtime.Object
	gvk, err := apiutil.GVKForObject(r.newObj(), r.scheme)
	var list client.ObjectList
	if err == nil {
		list, err = r.newList(gvk)
	}
	if err == nil {
		err = r.client.List(ctx, list, client.InNamespace(ns.GetName()))
	}
	if err == nil {
		items, err = meta.ExtractList(list)
	}
	if err != nil {
		log.FromContext(ctx).Error(err, "Listing resources in a terminating Namespace failed", "namespace", ns.GetName())
		return nil
	}
	var requests []reconcile.Request
	for _, item := range items {
		if o, ok := item.(client.Object); ok && !o.GetDeletionTimestamp().IsZero() {
			requests = append(requests, reconcile.Request{NamespacedName: client.ObjectKeyFromObject(o)})
		}
	}
	return requests
}

func (r *Reconciler[T]) cleanupBlocked(ctx context.Context, obj T, before client.Object, cause error) (ctrl.Result, error) {
	r.setSynced(obj, metav1.ConditionFalse, commonv1alpha1.ReasonCleanupBlocked, cause.Error())
	return ctrl.Result{}, errors.Join(cause, r.patchStatus(ctx, obj, before))
}

// Errors of deleteRemote for an object this resource no longer owns; the delete leaves it in OpenBao.
var (
	errServerChanged = errors.New("the connection now reaches another server than the one the object was acquired on")
	errForeignMarker = errors.New("the object's ownership marker names another owner")
)

func (r *Reconciler[T]) deleteRemote(ctx context.Context, obj T) error {
	status := obj.GetResourceStatus()
	connect := r.conns.For
	if cleanup, ok := r.conns.(CleanupConnections); ok {
		connect = cleanup.ForCleanup
	}
	base, err := connect(ctx, obj.GetNamespace(), obj.GetResourceSpec().ConnectionRef)
	if err != nil {
		return err
	}
	if status.ServerID != "" {
		id, err := serverID(ctx, base, r.deletingServer(ctx, obj).Key, true)
		if err != nil {
			return err
		}
		if id != status.ServerID {
			return errServerChanged
		}
	}
	recordedNamespace := strings.Trim(status.Namespace, "/")
	if err := openbao.ValidateNamespace(recordedNamespace); err != nil {
		return fmt.Errorf("status.openbaoNamespace: %w", err)
	}
	baseSegments, recorded := namespaceSegments(base.Namespace()), namespaceSegments(recordedNamespace)
	if len(recorded) < len(baseSegments) || !slices.Equal(recorded[:len(baseSegments)], baseSegments) {
		return fmt.Errorf("recorded namespace %q is outside the connection namespace %q; set deletionPolicy to "+
			"Orphan to release the resource", recordedNamespace, base.Namespace())
	}
	bao := base
	if relative := strings.Join(recorded[len(baseSegments):], "/"); relative != "" {
		bao = base.WithNamespace(relative)
	}
	path := strings.TrimPrefix(strings.TrimPrefix(status.Path, recordedNamespace), "/")
	if _, ok := any(r.adapter).(Marker[T]); ok && !r.sharedPath() {
		observed, found, err := r.observe(ctx, bao, obj, path)
		switch {
		case err != nil:
			return err
		case !found:
			return nil
		}
		if verdict := r.checkMarker(obj, observed); verdict == markerForeignCluster || verdict == markerStale {
			return errForeignMarker
		}
	}
	return r.deleteAt(ctx, bao, obj, path)
}

// deleteAt deletes the object through the Deleter, or path by default. An object whose path, parent mount or
// namespace is gone counts as deleted.
func (r *Reconciler[T]) deleteAt(ctx context.Context, bao openbao.Client, obj T, path string) error {
	var err error
	if deleter, ok := any(r.adapter).(Deleter[T]); ok {
		err = deleter.Delete(ctx, bao, obj)
	} else {
		err = bao.Delete(ctx, path)
	}
	if errors.Is(err, openbao.ErrNotFound) || errors.Is(UnsupportedIfMountMissing(err), ErrUnsupported) {
		return nil
	}
	return err
}

func (r *Reconciler[T]) syncFinalizer(ctx context.Context, obj T) error {
	want := r.deletionPolicy(obj) == commonv1alpha1.DeletionPolicyDelete && !observeOnly(obj)
	if want == controllerutil.ContainsFinalizer(obj, r.opts.Finalizer) {
		return nil
	}
	if want {
		return r.patchFinalizer(ctx, obj, controllerutil.AddFinalizer)
	}
	return r.patchFinalizer(ctx, obj, controllerutil.RemoveFinalizer)
}

func (r *Reconciler[T]) patchFinalizer(ctx context.Context, obj T, mutate func(client.Object, string) bool) error {
	base := obj.DeepCopyObject().(client.Object)
	if !mutate(obj, r.opts.Finalizer) {
		return nil
	}
	patch := client.MergeFromWithOptions(base, client.MergeFromWithOptimisticLock{})
	if err := r.client.Patch(ctx, obj, patch, client.FieldOwner(r.opts.FieldOwner)); client.IgnoreNotFound(err) != nil {
		return fmt.Errorf("patch finalizer: %w", err)
	}
	return nil
}

// pausedBy names what pauses obj (the resource, its Namespace or its server), or returns "". recheck reports that
// the pause does not come from the resource's own annotation, whose removal re-queues it.
func (r *Reconciler[T]) pausedBy(ctx context.Context, obj T) (by string, recheck bool, err error) {
	if obj.GetAnnotations()[commonv1alpha1.AnnotationPaused] == annotationTrue {
		return "the resource", false, nil
	}
	if name := obj.GetNamespace(); name != "" {
		ns := &corev1.Namespace{}
		err := r.client.Get(ctx, client.ObjectKey{Name: name}, ns)
		switch {
		case err == nil && ns.Annotations[commonv1alpha1.AnnotationPaused] == annotationTrue:
			return "Namespace " + name, true, nil
		case err != nil && !apierrors.IsNotFound(err):
			return "", false, fmt.Errorf("get namespace %s: %w", name, err)
		}
	}
	if resolver, ok := r.conns.(ServerResolver); ok {
		if server, err := resolver.ServerFor(ctx, obj.GetNamespace(), obj.GetResourceSpec().ConnectionRef); err == nil && server.Paused {
			return "server " + server.Key, true, nil
		}
	}
	return "", false, nil
}

// pause reports a paused resource; nothing is read, written or deleted and a deletion keeps the finalizer.
func (r *Reconciler[T]) pause(ctx context.Context, obj T, before client.Object, by string, recheck bool) (ctrl.Result, error) {
	r.setSynced(obj, metav1.ConditionFalse, commonv1alpha1.ReasonPaused,
		fmt.Sprintf("reconciliation is paused by the annotation %s on %s", commonv1alpha1.AnnotationPaused, by))
	if err := r.patchStatus(ctx, obj, before); err != nil {
		return ctrl.Result{}, err
	}
	if !recheck {
		return ctrl.Result{}, nil
	}
	return ctrl.Result{RequeueAfter: jitter(pausedRecheck)}, nil
}

// conflict reports that another resource or cluster owns the path. The claim in the status is dropped, so this
// resource never deletes the object and the owner stays the only entry in PathIndex.
func (r *Reconciler[T]) conflict(ctx context.Context, obj T, before client.Object, cause error) (ctrl.Result, error) {
	conflictsTotal.WithLabelValues(r.kind).Inc()
	status := obj.GetResourceStatus()
	if synced := meta.FindStatusCondition(status.Conditions, commonv1alpha1.ConditionSynced); synced == nil ||
		synced.Reason != commonv1alpha1.ReasonConflict {
		r.recorder.Eventf(obj, nil, corev1.EventTypeWarning, commonv1alpha1.ReasonConflict, "Acquire", cause.Error())
	}
	status.Acquired, status.Path, status.Namespace, status.ServerID = "", "", "", ""
	return r.stall(ctx, obj, before, commonv1alpha1.ReasonConflict, cause, r.jitter(obj))
}

// guard reports a destructive delete or recreate that waits for AnnotationAllowDestructiveDelete; the annotation
// change re-queues the resource.
func (r *Reconciler[T]) guard(ctx context.Context, obj T, before client.Object, cause error) (ctrl.Result, error) {
	if synced := meta.FindStatusCondition(obj.GetResourceStatus().Conditions, commonv1alpha1.ConditionSynced); synced == nil ||
		synced.Reason != commonv1alpha1.ReasonDeleteGuarded {
		r.recorder.Eventf(obj, nil, corev1.EventTypeWarning, commonv1alpha1.ReasonDeleteGuarded, "Delete", cause.Error())
	}
	if obj.GetDeletionTimestamp().IsZero() {
		return r.stall(ctx, obj, before, commonv1alpha1.ReasonDeleteGuarded, cause, r.jitter(obj))
	}
	r.setSynced(obj, metav1.ConditionFalse, commonv1alpha1.ReasonDeleteGuarded, cause.Error())
	return ctrl.Result{}, r.patchStatus(ctx, obj, before)
}

// overBudget holds a rewrite back until the server's write budget frees up.
func (r *Reconciler[T]) overBudget(ctx context.Context, obj T, before client.Object, cause *budgetError) (ctrl.Result, error) {
	status := obj.GetResourceStatus()
	if synced := meta.FindStatusCondition(status.Conditions, commonv1alpha1.ConditionSynced); synced == nil ||
		synced.Reason != commonv1alpha1.ReasonWriteBudgetExceeded {
		r.recorder.Eventf(obj, nil, corev1.EventTypeWarning, commonv1alpha1.ReasonWriteBudgetExceeded, "Update", cause.Error())
	}
	r.setSynced(obj, metav1.ConditionFalse, commonv1alpha1.ReasonWriteBudgetExceeded, cause.Error())
	if meta.FindStatusCondition(status.Conditions, commonv1alpha1.ConditionReady) == nil {
		r.setReady(obj, metav1.ConditionFalse, commonv1alpha1.ReasonWriteBudgetExceeded, cause.Error())
	}
	if err := r.patchStatus(ctx, obj, before); err != nil {
		return ctrl.Result{}, err
	}
	return ctrl.Result{RequeueAfter: budgetRetry(cause.wait)}, nil
}

// connectionFailed maps an error resolving the connection or its server to a condition.
func (r *Reconciler[T]) connectionFailed(ctx context.Context, obj T, before client.Object, err error) (ctrl.Result, error) {
	switch {
	case errors.Is(err, ErrConnectionNotReady):
		return r.stall(ctx, obj, before, commonv1alpha1.ReasonConnectionNotReady, err,
			backoff(obj, commonv1alpha1.ReasonConnectionNotReady, maxConnectionRetry))
	case errors.Is(err, ErrConnectionDenied):
		return r.stall(ctx, obj, before, commonv1alpha1.ReasonConnectionDenied, err, r.jitter(obj))
	default:
		return r.fail(ctx, obj, before, commonv1alpha1.ReasonReconcileError, err)
	}
}

// reconnecting reports the first pass after the connection of obj was not ready. After an outage every resource of a
// server comes back at once: these passes go through the server's recovery ramp, and their requests without
// priority, so the server's shared rate limit holds them to its drift share.
func reconnecting(obj Object) bool {
	synced := meta.FindStatusCondition(obj.GetResourceStatus().Conditions, commonv1alpha1.ConditionSynced)
	return synced != nil && synced.Reason == commonv1alpha1.ReasonConnectionNotReady
}

// fields merges the adapter's per-object FieldsProvider rules over Fields().
func (r *Reconciler[T]) fields(ctx context.Context, resolver Resolver, obj T) (FieldSpec, error) {
	spec := r.adapter.Fields()
	provider, ok := any(r.adapter).(FieldsProvider[T])
	if !ok {
		return spec, nil
	}
	extra, err := provider.FieldsFor(ctx, resolver, obj)
	if err != nil {
		return FieldSpec{}, err
	}
	aliases := maps.Clone(spec.Aliases)
	if len(extra.Aliases) > 0 && aliases == nil {
		aliases = map[string]string{}
	}
	maps.Copy(aliases, extra.Aliases)
	return FieldSpec{
		WriteOnly:   union(spec.WriteOnly, extra.WriteOnly),
		CreateOnly:  union(spec.CreateOnly, extra.CreateOnly),
		Aliases:     aliases,
		Sets:        union(spec.Sets, extra.Sets),
		Durations:   union(spec.Durations, extra.Durations),
		ServerOwned: union(spec.ServerOwned, extra.ServerOwned),
		WriteOnce:   union(spec.WriteOnce, extra.WriteOnce),
		Partial:     union(spec.Partial, extra.Partial),
	}, nil
}

func union(a, b []string) []string {
	merged := slices.Concat(a, b)
	slices.Sort(merged)
	return slices.Compact(merged)
}

// createOnlyChanges returns the CreateOnly keys whose desired value differs from the observed object. Keys that are
// also write-only cannot be compared and are skipped.
func createOnlyChanges(desired, observed Payload, fields FieldSpec) []string {
	createOnly := Payload{}
	for _, key := range fields.CreateOnly {
		if value, ok := desired[key]; ok && !slices.Contains(fields.WriteOnly, key) {
			createOnly[key] = value
		}
	}
	compare := fields
	compare.CreateOnly = nil
	return Diff(createOnly, observed, compare)
}

func allowsDestructive(obj client.Object) bool {
	return obj.GetAnnotations()[commonv1alpha1.AnnotationAllowDestructiveDelete] == annotationTrue
}

func (r *Reconciler[T]) sharedPath() bool {
	s, ok := any(r.adapter).(SharedPath)
	return ok && s.SharedPath()
}

func (r *Reconciler[T]) singleton() bool {
	s, ok := any(r.adapter).(Singleton)
	return ok && s.Singleton()
}

func (r *Reconciler[T]) destructive() bool {
	d, ok := any(r.adapter).(Destructive)
	return ok && d.Destructive()
}

func (r *Reconciler[T]) rotationNonce(obj T) string {
	if n, ok := any(r.adapter).(RotationNonce[T]); ok {
		return n.RotationNonce(obj)
	}
	return ""
}

func (r *Reconciler[T]) deletionPolicy(obj T) commonv1alpha1.DeletionPolicy {
	if policy := obj.GetResourceSpec().DeletionPolicy; policy != "" {
		return policy
	}
	return commonv1alpha1.DeletionPolicyOrphan
}

func (r *Reconciler[T]) path(ctx context.Context, bao openbao.Client, obj T) (string, error) {
	if resolver, ok := any(r.adapter).(PathResolver[T]); ok {
		return resolver.ResolvePath(ctx, r.resolver, bao, obj)
	}
	return r.adapter.Path(obj), nil
}

func (r *Reconciler[T]) observe(ctx context.Context, bao openbao.Client, obj T, path string) (Payload, bool, error) {
	if observer, ok := any(r.adapter).(Observer[T]); ok {
		return observer.Observe(ctx, bao, obj)
	}
	observed, err := bao.Read(ctx, path)
	if errors.Is(err, openbao.ErrNotFound) && !errors.Is(err, openbao.ErrNamespaceNotFound) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	return observed, true, nil
}

func (r *Reconciler[T]) write(ctx context.Context, bao openbao.Client, obj T, path string, payload Payload,
	create bool) (Payload, error) {
	var response Payload
	var err error
	if writer, ok := any(r.adapter).(Writer[T]); ok {
		response, err = writer.Write(ctx, bao, obj, payload, create)
	} else {
		response, err = bao.Write(ctx, path, payload)
	}
	if noRoute(err) {
		return nil, fmt.Errorf("%w: %w", ErrReferenceNotReady, err)
	}
	return response, err
}

// noRoute reports a 404 "no handler for route" on a write: the mount the path is under, or the mounts of a new
// namespace, do not exist yet.
func noRoute(err error) bool {
	re, ok := errors.AsType[*openbao.ResponseError](err)
	return ok && re.StatusCode == http.StatusNotFound &&
		slices.ContainsFunc(re.Errors, func(e string) bool { return strings.Contains(e, "no handler for route") })
}

// checkOutputSecret refuses, before anything is written to OpenBao, an output Secret this resource does not control.
func (r *Reconciler[T]) checkOutputSecret(ctx context.Context, obj T) error {
	namer, ok := any(r.adapter).(OutputSecretNamer[T])
	if !ok {
		return nil
	}
	name := namer.OutputSecretName(obj)
	if name == "" {
		return nil
	}
	secret := &corev1.Secret{}
	err := r.client.Get(ctx, client.ObjectKey{Namespace: obj.GetNamespace(), Name: name}, secret)
	switch {
	case apierrors.IsNotFound(err):
		return nil
	case err != nil:
		return fmt.Errorf("read output secret %s/%s: %w", obj.GetNamespace(), name, err)
	case !metav1.IsControlledBy(secret, obj):
		return fmt.Errorf("output secret %s/%s: %w", obj.GetNamespace(), name, ErrSecretNotOwned)
	}
	return nil
}

// writeOutputs stores one-time values from a write response in a Secret owned by obj.
func (r *Reconciler[T]) writeOutputs(ctx context.Context, obj T, response Payload) error {
	outputs, ok := any(r.adapter).(OutputWriter[T])
	if !ok {
		return nil
	}
	name, data, ok := outputs.Outputs(obj, response)
	if !ok {
		return nil
	}
	secret := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: obj.GetNamespace()}}
	_, err := controllerutil.CreateOrUpdate(ctx, r.client, secret, func() error {
		if secret.ResourceVersion != "" && !metav1.IsControlledBy(secret, obj) {
			return ErrSecretNotOwned
		}
		if secret.Labels == nil {
			secret.Labels = map[string]string{}
		}
		secret.Labels[managedByLabel] = DefaultFieldOwner
		secret.Type = corev1.SecretTypeOpaque
		secret.Data = data
		return controllerutil.SetControllerReference(obj, secret, r.scheme)
	})
	if err != nil {
		return fmt.Errorf("write output secret %s/%s: %w", obj.GetNamespace(), name, err)
	}
	return nil
}

// refreshOutputs writes the output Secret of an OutputRefresher again from the object's current value; the Secret
// write is a no-op while it already holds that value.
func (r *Reconciler[T]) refreshOutputs(ctx context.Context, bao openbao.Client, obj T, observed Payload) error {
	refresher, ok := any(r.adapter).(OutputRefresher[T])
	if !ok {
		return nil
	}
	response, err := refresher.RefreshOutputs(ctx, bao, obj, observed)
	if err == nil {
		err = r.writeOutputs(ctx, obj, response)
	}
	if err != nil {
		return fmt.Errorf("%w: %w", ErrOutputNotDelivered, err)
	}
	return nil
}

func (r *Reconciler[T]) recordWrite(obj T, path string, found bool, differing []string, inputsChanged bool) {
	if !found {
		r.recorder.Eventf(obj, nil, corev1.EventTypeNormal, "Created", "Create", "created %s", path)
		return
	}
	changed := differing
	if inputsChanged {
		changed = append(changed, "write-only inputs")
	}
	r.recorder.Eventf(obj, nil, corev1.EventTypeNormal, "Updated", "Update", "updated %s: %s", path,
		strings.Join(changed, ", "))
}

// inProgress reports an asynchronous OpenBao operation and polls for it without error backoff.
func (r *Reconciler[T]) inProgress(ctx context.Context, obj T, before client.Object, cause error) (ctrl.Result, error) {
	r.setSynced(obj, metav1.ConditionFalse, commonv1alpha1.ReasonReconcileInProgress, cause.Error())
	if err := r.patchStatus(ctx, obj, before); err != nil {
		return ctrl.Result{}, err
	}
	return ctrl.Result{RequeueAfter: inProgressInterval}, nil
}

// stall reports a condition that waits for an outside change and requeues without error backoff.
func (r *Reconciler[T]) stall(ctx context.Context, obj T, before client.Object, reason string, cause error,
	after time.Duration) (ctrl.Result, error) {
	r.setConditions(obj, metav1.ConditionFalse, reason, metav1.ConditionFalse, reason, cause.Error())
	if err := r.patchStatus(ctx, obj, before); err != nil {
		return ctrl.Result{}, err
	}
	return ctrl.Result{RequeueAfter: after}, nil
}

func (r *Reconciler[T]) fail(ctx context.Context, obj T, before client.Object, reason string, cause error) (ctrl.Result, error) {
	if errors.Is(cause, ErrInProgress) {
		return r.inProgress(ctx, obj, before, cause)
	}
	if errors.Is(cause, ErrConflict) {
		return r.conflict(ctx, obj, before, cause)
	}
	if errors.Is(cause, ErrInvalidSpec) {
		r.setConditions(obj, metav1.ConditionFalse, commonv1alpha1.ReasonInvalidSpec,
			metav1.ConditionFalse, commonv1alpha1.ReasonInvalidSpec, cause.Error())
		if err := r.patchStatus(ctx, obj, before); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{}, reconcile.TerminalError(cause)
	}
	if errors.Is(cause, ErrUnsupported) {
		return r.stall(ctx, obj, before, commonv1alpha1.ReasonUnsupported, cause, r.jitter(obj))
	}
	if errors.Is(cause, ErrPluginNotRegistered) {
		return r.stall(ctx, obj, before, commonv1alpha1.ReasonPluginNotRegistered, cause, r.jitter(obj))
	}
	if errors.Is(cause, ErrOutputNotDelivered) {
		return r.stall(ctx, obj, before, commonv1alpha1.ReasonOutputNotDelivered, cause,
			backoff(obj, commonv1alpha1.ReasonOutputNotDelivered, retryInterval))
	}
	if errors.Is(cause, ErrReferenceNotReady) || errors.Is(cause, openbao.ErrNamespaceNotFound) {
		after := referenceRetry(obj)
		log.FromContext(ctx).Info("Requeued until a dependency is ready", "after", after, "message", cause.Error())
		return r.stall(ctx, obj, before, commonv1alpha1.ReasonReferenceNotReady, cause, after)
	}
	if permissionDenied(cause) {
		reason = commonv1alpha1.ReasonPermissionDenied
	}
	r.setSynced(obj, metav1.ConditionFalse, reason, cause.Error())
	if ready := meta.FindStatusCondition(obj.GetResourceStatus().Conditions, commonv1alpha1.ConditionReady); ready == nil {
		r.setReady(obj, metav1.ConditionFalse, reason, cause.Error())
	}
	return ctrl.Result{}, errors.Join(cause, r.patchStatus(ctx, obj, before))
}

// permissionDenied reports a 403: the token is valid but its policy denies the request.
func permissionDenied(err error) bool {
	if errors.Is(err, openbao.ErrPermissionDenied) {
		return true
	}
	re, ok := errors.AsType[*openbao.ResponseError](err)
	return ok && re.StatusCode == http.StatusForbidden
}

func (r *Reconciler[T]) setConditions(obj T, ready metav1.ConditionStatus, readyReason string,
	synced metav1.ConditionStatus, syncedReason, message string) {
	r.setReady(obj, ready, readyReason, message)
	r.setSynced(obj, synced, syncedReason, message)
}

func (r *Reconciler[T]) setReady(obj T, status metav1.ConditionStatus, reason, message string) {
	r.setCondition(obj, commonv1alpha1.ConditionReady, status, reason, message)
}

func (r *Reconciler[T]) setSynced(obj T, status metav1.ConditionStatus, reason, message string) {
	r.setCondition(obj, commonv1alpha1.ConditionSynced, status, reason, message)
}

func (r *Reconciler[T]) setCondition(obj T, conditionType string, status metav1.ConditionStatus, reason, message string) {
	obj.GetResourceStatus().ObservedGeneration = obj.GetGeneration()
	meta.SetStatusCondition(&obj.GetResourceStatus().Conditions, metav1.Condition{
		Type:               conditionType,
		Status:             status,
		Reason:             reason,
		Message:            message,
		ObservedGeneration: obj.GetGeneration(),
	})
}

// recordReady sets obco_resource_ready from the Ready condition of obj.
func (r *Reconciler[T]) recordReady(obj T) {
	ready := meta.FindStatusCondition(obj.GetResourceStatus().Conditions, commonv1alpha1.ConditionReady)
	if ready == nil {
		return
	}
	r.ready.set(r.kind, client.ObjectKeyFromObject(obj), ready.Status == metav1.ConditionTrue, ready.Reason)
}

// forget drops the drift schedule and readiness series of a resource that is gone.
func (r *Reconciler[T]) forget(key types.NamespacedName) {
	r.due.forget(key)
	r.ready.forget(r.kind, key)
	r.applied.Delete(key)
}

func (r *Reconciler[T]) patchStatus(ctx context.Context, obj T, before client.Object) error {
	r.recordReady(obj)
	if equality.Semantic.DeepEqual(before, client.Object(obj)) {
		return nil
	}
	err := r.client.Status().Patch(ctx, obj, client.MergeFrom(before), client.FieldOwner(r.opts.FieldOwner))
	if claims, now := PathIndexFunc(before), PathIndexFunc(obj); !slices.Equal(claims, now) {
		r.shared.touch(append(claims, now...))
	}
	if client.IgnoreNotFound(err) != nil {
		return fmt.Errorf("patch status: %w", err)
	}
	return nil
}

// referenceRetry backs off while a reference stays unresolved, from minReferenceRetry up to retryInterval. Watched
// referents re-queue sooner.
func referenceRetry(obj Object) time.Duration {
	return backoff(obj, commonv1alpha1.ReasonReferenceNotReady, retryInterval)
}

// backoff returns a jittered delay that starts at minReferenceRetry and grows with the time Synced has been False
// with reason, roughly doubling each retry, capped at limit.
func backoff(obj Object, reason string, limit time.Duration) time.Duration {
	delay := minReferenceRetry
	synced := meta.FindStatusCondition(obj.GetResourceStatus().Conditions, commonv1alpha1.ConditionSynced)
	if synced != nil && synced.Status == metav1.ConditionFalse && synced.Reason == reason {
		delay = max(delay, time.Since(synced.LastTransitionTime.Time))
	}
	return jitter(min(delay, limit))
}

func jitter(d time.Duration) time.Duration {
	return time.Duration(float64(d) * (0.8 + 0.4*rand.Float64()))
}

func (r *Reconciler[T]) jitter(obj T) time.Duration {
	return jitter(r.driftInterval(obj))
}

// redactedError hides write-only values that OpenBao echoed in an error message; Unwrap keeps errors.Is working.
type redactedError struct {
	err error
	msg string
}

func (e *redactedError) Error() string { return e.msg }

func (e *redactedError) Unwrap() error { return e.err }

// minRedactLength keeps short values such as "true" from mangling unrelated words of the message.
const minRedactLength = 4

func redactWriteOnly(err error, desired Payload, fields FieldSpec) error {
	msg := err.Error()
	redacted := msg
	for _, key := range slices.Concat(fields.WriteOnly, fields.WriteOnce) {
		for _, value := range redactableStrings(desired[key]) {
			redacted = strings.ReplaceAll(redacted, value, "<redacted>")
		}
	}
	if redacted == msg {
		return err
	}
	return &redactedError{err: err, msg: redacted}
}

// redactableStrings returns every string of at least minRedactLength in value, including those nested in maps
// and lists.
func redactableStrings(value any) []string {
	switch v := value.(type) {
	case string:
		if len(v) >= minRedactLength {
			return []string{v}
		}
	case map[string]any:
		var out []string
		for _, item := range v {
			out = append(out, redactableStrings(item)...)
		}
		return out
	case []any:
		var out []string
		for _, item := range v {
			out = append(out, redactableStrings(item)...)
		}
		return out
	case []string:
		var out []string
		for _, item := range v {
			out = append(out, redactableStrings(item)...)
		}
		return out
	}
	return nil
}

// namespaceSegments splits an OpenBao namespace path into its non-empty segments.
func namespaceSegments(ns string) []string {
	var segments []string
	for s := range strings.SplitSeq(ns, "/") {
		if s != "" {
			segments = append(segments, s)
		}
	}
	return segments
}

func joinPath(namespace, path string) string {
	namespace = strings.Trim(namespace, "/")
	if namespace == "" {
		return path
	}
	return namespace + "/" + path
}
