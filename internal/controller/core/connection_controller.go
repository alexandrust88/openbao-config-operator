// Package core holds the controllers of the core.obco.dev group.
package core

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/wait"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	commonv1alpha1 "github.com/alexandrust88/openbao-config-operator/api/common/v1alpha1"
	corev1alpha1 "github.com/alexandrust88/openbao-config-operator/api/core/v1alpha1"
	"github.com/alexandrust88/openbao-config-operator/internal/connection"
	"github.com/alexandrust88/openbao-config-operator/internal/engine"
	"github.com/alexandrust88/openbao-config-operator/internal/openbao"
)

const (
	probeInterval  = time.Minute
	referenceIndex = "spec.references"
	serverIndex    = "spec.serverRef.name"
	lookupSelfPath = "auth/token/lookup-self"
)

// The retry bounds while the connection's OpenBao namespace does not exist yet, which is usual when the connection
// and its BaoNamespace are applied together.
const (
	minNamespaceRetry = 2 * time.Second
	maxNamespaceRetry = 30 * time.Second
)

type connectionObject interface {
	client.Object
	GetConnectionSpec() *corev1alpha1.ConnectionSpec
	GetConnectionStatus() *corev1alpha1.ConnectionStatus
}

// +kubebuilder:rbac:groups=core.obco.dev,resources=baoconnections;clusterbaoconnections,verbs=get;list;watch;update;patch
// +kubebuilder:rbac:groups=core.obco.dev,resources=baoconnections/finalizers;clusterbaoconnections/finalizers,verbs=update
// +kubebuilder:rbac:groups=core.obco.dev,resources=baoconnections/status;clusterbaoconnections/status,verbs=get;update;patch
// +kubebuilder:rbac:groups="",resources=secrets;configmaps;namespaces,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=serviceaccounts/token,verbs=create
// +kubebuilder:rbac:urls=/.well-known/openid-configuration,verbs=get

// connectionReconciler checks one connection kind against its BaoServer and authenticates it.
type connectionReconciler struct {
	client.Client
	resolver  *connection.Resolver
	pool      connection.Pool
	kind      commonv1alpha1.ConnectionKind
	newObject func() connectionObject
	newList   func() client.ObjectList
	// managed lists the resources that may hold the connection; see indexManagedKinds.
	managed []func() client.ObjectList
	// keys holds the pool key per object name, so a delete can drop its client.
	keys sync.Map
}

// SetupConnectionControllers registers the BaoServer, BaoConnection and ClusterBaoConnection
// controllers and returns the resolver the managed kinds reach OpenBao through. A namespaced connection is never
// given a token for one of apiServerAudiences; connection.APIServerAudiences discovers them.
func SetupConnectionControllers(mgr ctrl.Manager, pool *openbao.Pool, apiServerAudiences []string) (
	*connection.Resolver, error) {
	resolver := &connection.Resolver{Client: mgr.GetClient(), Pool: pool, APIServerAudiences: apiServerAudiences}
	if err := setupServerController(mgr, resolver, openbao.Probe); err != nil {
		return nil, err
	}
	if err := setupConnectionControllers(mgr, resolver, pool); err != nil {
		return nil, err
	}
	return resolver, nil
}

func setupConnectionControllers(mgr ctrl.Manager, resolver *connection.Resolver, pool connection.Pool) error {
	managed, err := indexManagedKinds(context.Background(), mgr)
	if err != nil {
		return err
	}
	kinds := []struct {
		name      string
		kind      commonv1alpha1.ConnectionKind
		tenant    bool
		newObject func() connectionObject
		newList   func() client.ObjectList
	}{
		{
			name:      "baoconnection",
			kind:      commonv1alpha1.ConnectionKindNamespaced,
			tenant:    true,
			newObject: func() connectionObject { return &corev1alpha1.BaoConnection{} },
			newList:   func() client.ObjectList { return &corev1alpha1.BaoConnectionList{} },
		},
		{
			name:      "clusterbaoconnection",
			kind:      commonv1alpha1.ConnectionKindCluster,
			newObject: func() connectionObject { return &corev1alpha1.ClusterBaoConnection{} },
			newList:   func() client.ObjectList { return &corev1alpha1.ClusterBaoConnectionList{} },
		},
	}
	for _, kind := range kinds {
		indexer := mgr.GetFieldIndexer()
		if err := indexer.IndexField(context.Background(), kind.newObject(), referenceIndex, connection.References); err != nil {
			return err
		}
		if err := indexer.IndexField(context.Background(), kind.newObject(), serverIndex, serverName); err != nil {
			return err
		}
		r := &connectionReconciler{
			Client:    mgr.GetClient(),
			resolver:  resolver,
			pool:      pool,
			kind:      kind.kind,
			newObject: kind.newObject,
			newList:   kind.newList,
			managed:   managed,
		}
		b := ctrl.NewControllerManagedBy(mgr).
			Named(kind.name).
			For(kind.newObject(), builder.WithPredicates(predicate.GenerationChangedPredicate{})).
			Watches(&corev1.Secret{}, handler.EnqueueRequestsFromMapFunc(r.referencing("Secret")), builder.OnlyMetadata).
			Watches(&corev1alpha1.BaoServer{}, handler.EnqueueRequestsFromMapFunc(r.usingServer))
		if kind.tenant {
			b = b.Watches(&corev1.Namespace{}, handler.EnqueueRequestsFromMapFunc(r.inNamespace), builder.OnlyMetadata,
				builder.WithPredicates(predicate.LabelChangedPredicate{}))
		}
		if err := b.Complete(r); err != nil {
			return err
		}
	}
	return nil
}

func serverName(obj client.Object) []string {
	if c, ok := obj.(connectionObject); ok {
		return []string{c.GetConnectionSpec().ServerRef.Name}
	}
	return nil
}

// Reconcile checks the connection against its BaoServer, authenticates and records the result. It always
// requeues to re-check.
func (r *connectionReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	obj := r.newObject()
	if err := r.Get(ctx, req.NamespacedName, obj); err != nil {
		if apierrors.IsNotFound(err) {
			r.forget(req.NamespacedName)
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}
	if !obj.GetDeletionTimestamp().IsZero() {
		return r.finalize(ctx, obj, req.NamespacedName)
	}
	if err := ensureFinalizer(ctx, r.Client, obj, connectionFinalizer); err != nil {
		return ctrl.Result{}, err
	}

	key := connection.PoolKey(obj)
	if old, loaded := r.keys.Swap(req.NamespacedName, key); loaded && old.(string) != key {
		r.pool.Invalidate(old.(string))
	}

	base := obj.DeepCopyObject().(client.Object)
	status := obj.GetConnectionStatus()
	r.observe(ctx, obj, key, status)
	status.ObservedGeneration = obj.GetGeneration()
	ready := 0.0
	if meta.IsStatusConditionTrue(status.Conditions, commonv1alpha1.ConditionReady) {
		ready = 1
	}
	connectionReady.WithLabelValues(string(r.kind), req.Namespace, req.Name).Set(ready)
	requeue := ctrl.Result{RequeueAfter: wait.Jitter(probeInterval, 0.2)}
	if cond := meta.FindStatusCondition(status.Conditions, commonv1alpha1.ConditionReady); cond != nil &&
		cond.Reason == commonv1alpha1.ReasonReferenceNotReady {
		requeue.RequeueAfter = namespaceRetry(cond)
	}
	// A check that changes nothing writes nothing, so idle connections cost no API server writes.
	if equality.Semantic.DeepEqual(base, client.Object(obj)) {
		return requeue, nil
	}
	now := metav1.Now()
	status.LastProbeTime = &now
	if err := r.Status().Patch(ctx, obj, client.MergeFrom(base)); err != nil {
		return ctrl.Result{}, err
	}
	return requeue, nil
}

// finalize keeps a connection that is being deleted, unchanged and usable, while a resource still holds it to
// clean up in OpenBao, then releases it.
func (r *connectionReconciler) finalize(ctx context.Context, obj connectionObject, name types.NamespacedName) (
	ctrl.Result, error) {
	if controllerutil.ContainsFinalizer(obj, connectionFinalizer) {
		ref := commonv1alpha1.ConnectionReference{Kind: r.kind, Name: name.Name}
		counts := countAll
		if cluster, ok := obj.(*corev1alpha1.ClusterBaoConnection); ok {
			// A resource in a namespace the connection does not admit never used it, so its finalizer cannot hold it.
			counts = func(item client.Object) (bool, error) {
				return r.resolver.ClusterAdmits(ctx, cluster, item.GetNamespace())
			}
		}
		inUse, err := anyListed(ctx, r.Client, r.managed, counts,
			client.MatchingFields{connectionUseIndex: engine.ConnectionKey(name.Namespace, ref)})
		if err != nil {
			return ctrl.Result{}, err
		}
		if inUse {
			return ctrl.Result{RequeueAfter: inUseRecheck}, nil
		}
		if err := releaseFinalizer(ctx, r.Client, obj, connectionFinalizer); err != nil {
			return ctrl.Result{}, err
		}
	}
	r.forget(name)
	return ctrl.Result{}, nil
}

func (r *connectionReconciler) forget(name types.NamespacedName) {
	if old, loaded := r.keys.LoadAndDelete(name); loaded {
		r.pool.Invalidate(old.(string))
		r.resolver.Forget(old.(string))
	}
	connectionReady.DeleteLabelValues(string(r.kind), name.Namespace, name.Name)
}

func (r *connectionReconciler) observe(ctx context.Context, obj connectionObject, key string,
	status *corev1alpha1.ConnectionStatus) {
	status.Authenticated = false
	generation := obj.GetGeneration()
	fail := func(reason, message string) {
		setReady(&status.Conditions, generation, metav1.ConditionFalse, reason, message)
	}

	server, err := r.resolver.Server(ctx, obj)
	if err != nil {
		status.Version, status.Sealed, status.Standby = "", false, false
		fail(corev1alpha1.ReasonServerNotReady, err.Error())
		return
	}
	status.Version, status.Sealed, status.Standby = server.Status.Version, server.Status.Sealed, server.Status.Standby

	cfg, err := r.resolver.ConfigFor(ctx, obj, server)
	if err != nil {
		reason := commonv1alpha1.ReasonInvalidSpec
		switch {
		case errors.Is(err, engine.ErrConnectionDenied):
			reason = commonv1alpha1.ReasonConnectionDenied
			r.pool.Invalidate(key)
		case errors.Is(err, connection.ErrReferenceNotFound):
			reason = corev1alpha1.ReasonCredentialsNotFound
		}
		fail(reason, err.Error())
		return
	}

	if cond := meta.FindStatusCondition(server.Status.Conditions, commonv1alpha1.ConditionReady); cond == nil ||
		cond.Status != metav1.ConditionTrue || cond.ObservedGeneration != server.Generation {
		reason := "not probed yet"
		if cond != nil {
			reason = cond.Reason
		}
		fail(corev1alpha1.ReasonServerNotReady, fmt.Sprintf("BaoServer %s is not ready: %s", server.Name, reason))
		return
	}

	if err := r.authenticate(ctx, key, cfg); err != nil {
		reason := corev1alpha1.ReasonAuthenticationFailed
		if errors.Is(err, openbao.ErrNamespaceNotFound) {
			reason = commonv1alpha1.ReasonReferenceNotReady
		}
		fail(reason, err.Error())
		return
	}
	status.Authenticated = true
	setReady(&status.Conditions, generation, metav1.ConditionTrue, commonv1alpha1.ReasonAvailable,
		"connected and authenticated")
}

// namespaceRetry grows with the time the connection has been waiting for its namespace, from minNamespaceRetry
// up to maxNamespaceRetry.
func namespaceRetry(ready *metav1.Condition) time.Duration {
	waited := time.Since(ready.LastTransitionTime.Time)
	return wait.Jitter(min(max(waited, minNamespaceRetry), maxNamespaceRetry), 0.2)
}

func (r *connectionReconciler) authenticate(ctx context.Context, key string, cfg openbao.Config) error {
	c, err := r.pool.Client(ctx, key, cfg)
	if err != nil {
		return err
	}
	_, err = c.Read(ctx, lookupSelfPath)
	return err
}

func setReady(conditions *[]metav1.Condition, generation int64, value metav1.ConditionStatus, reason, message string) {
	meta.SetStatusCondition(conditions, metav1.Condition{
		Type:               commonv1alpha1.ConditionReady,
		Status:             value,
		ObservedGeneration: generation,
		Reason:             reason,
		Message:            message,
	})
}

func (r *connectionReconciler) referencing(kind string) handler.MapFunc {
	return func(ctx context.Context, obj client.Object) []reconcile.Request {
		value := connection.ReferenceIndexValue(kind, obj.GetNamespace(), obj.GetName())
		return r.enqueue(ctx, client.MatchingFields{referenceIndex: value})
	}
}

func (r *connectionReconciler) usingServer(ctx context.Context, obj client.Object) []reconcile.Request {
	return r.enqueue(ctx, client.MatchingFields{serverIndex: obj.GetName()})
}

// inNamespace re-checks the tenant connections of a namespace whose labels changed, since the labels decide
// which BaoServers admit them.
func (r *connectionReconciler) inNamespace(ctx context.Context, obj client.Object) []reconcile.Request {
	return r.enqueue(ctx, client.InNamespace(obj.GetName()))
}

func (r *connectionReconciler) enqueue(ctx context.Context, opts ...client.ListOption) []reconcile.Request {
	list := r.newList()
	if err := r.List(ctx, list, opts...); err != nil {
		logf.FromContext(ctx).Error(err, "Failed to list connections to enqueue")
		return nil
	}
	var requests []reconcile.Request
	_ = meta.EachListItem(list, func(item runtime.Object) error {
		o := item.(client.Object)
		requests = append(requests, reconcile.Request{NamespacedName: client.ObjectKeyFromObject(o)})
		return nil
	})
	return requests
}
