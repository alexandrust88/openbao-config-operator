package core

import (
	"context"
	"errors"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
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
	"github.com/alexandrust88/openbao-config-operator/internal/openbao"
)

type probeFunc func(ctx context.Context, cfg openbao.Config) (openbao.ServerInfo, error)

// +kubebuilder:rbac:groups=core.obco.dev,resources=baoservers,verbs=get;list;watch;update;patch
// +kubebuilder:rbac:groups=core.obco.dev,resources=baoservers/finalizers,verbs=update
// +kubebuilder:rbac:groups=core.obco.dev,resources=baoservers/status,verbs=get;update;patch

// serverReconciler probes a BaoServer without a token and records whether it is reachable and unsealed.
type serverReconciler struct {
	client.Client
	resolver *connection.Resolver
	probe    probeFunc
}

func setupServerController(mgr ctrl.Manager, resolver *connection.Resolver, probe probeFunc) error {
	err := mgr.GetFieldIndexer().IndexField(context.Background(), &corev1alpha1.BaoServer{}, referenceIndex,
		connection.References)
	if err != nil {
		return err
	}
	r := &serverReconciler{Client: mgr.GetClient(), resolver: resolver, probe: probe}
	return ctrl.NewControllerManagedBy(mgr).
		Named("baoserver").
		For(&corev1alpha1.BaoServer{}, builder.WithPredicates(predicate.GenerationChangedPredicate{})).
		Watches(&corev1.Secret{}, handler.EnqueueRequestsFromMapFunc(r.referencing("Secret")), builder.OnlyMetadata).
		Watches(&corev1.ConfigMap{}, handler.EnqueueRequestsFromMapFunc(r.referencing("ConfigMap")), builder.OnlyMetadata).
		Complete(r)
}

// Reconcile probes the server and records the result. It always requeues to re-probe.
func (r *serverReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	server := &corev1alpha1.BaoServer{}
	if err := r.Get(ctx, req.NamespacedName, server); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	if !server.DeletionTimestamp.IsZero() {
		return r.finalize(ctx, server)
	}
	if err := ensureFinalizer(ctx, r.Client, server, serverFinalizer); err != nil {
		return ctrl.Result{}, err
	}

	base := server.DeepCopy()
	r.observe(ctx, server)
	server.Status.ObservedGeneration = server.Generation
	requeue := ctrl.Result{RequeueAfter: wait.Jitter(probeInterval, 0.2)}
	if equality.Semantic.DeepEqual(base.Status, server.Status) {
		return requeue, nil
	}
	now := metav1.Now()
	server.Status.LastProbeTime = &now
	if err := r.Status().Patch(ctx, server, client.MergeFrom(base)); err != nil {
		if apierrors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}
	return requeue, nil
}

// finalize keeps a server that is being deleted while a connection still references it, then releases it.
func (r *serverReconciler) finalize(ctx context.Context, server *corev1alpha1.BaoServer) (ctrl.Result, error) {
	if !controllerutil.ContainsFinalizer(server, serverFinalizer) {
		return ctrl.Result{}, nil
	}
	// A namespaced connection the server does not admit never used it, so it cannot hold the server.
	counts := func(conn client.Object) (bool, error) {
		if conn.GetNamespace() == "" {
			return true, nil
		}
		return r.resolver.ServerAdmits(ctx, server, conn.GetNamespace())
	}
	inUse, err := anyListed(ctx, r.Client, []func() client.ObjectList{
		func() client.ObjectList { return &corev1alpha1.BaoConnectionList{} },
		func() client.ObjectList { return &corev1alpha1.ClusterBaoConnectionList{} },
	}, counts, client.MatchingFields{serverIndex: server.Name})
	if err != nil {
		return ctrl.Result{}, err
	}
	if inUse {
		return ctrl.Result{RequeueAfter: inUseRecheck}, nil
	}
	return ctrl.Result{}, releaseFinalizer(ctx, r.Client, server, serverFinalizer)
}

func (r *serverReconciler) observe(ctx context.Context, server *corev1alpha1.BaoServer) {
	status := &server.Status
	fail := func(reason, message string) {
		setReady(&status.Conditions, server.Generation, metav1.ConditionFalse, reason, message)
	}

	if _, err := metav1.LabelSelectorAsSelector(server.Spec.AllowedNamespaces); err != nil {
		fail(commonv1alpha1.ReasonInvalidSpec, "invalid allowedNamespaces: "+err.Error())
		return
	}
	cfg, err := r.resolver.ServerConfig(ctx, server)
	if err != nil {
		reason := commonv1alpha1.ReasonInvalidSpec
		if errors.Is(err, connection.ErrReferenceNotFound) {
			reason = commonv1alpha1.ReasonReferenceNotReady
		}
		fail(reason, err.Error())
		return
	}
	info, err := r.probe(ctx, cfg)
	if err != nil {
		fail(corev1alpha1.ReasonUnreachable, err.Error())
		return
	}
	status.Version = info.Version
	status.Sealed = info.Sealed || !info.Initialized
	status.Standby = info.Standby
	if status.Sealed {
		fail(corev1alpha1.ReasonSealed, "server is sealed or not initialized")
		return
	}
	setReady(&status.Conditions, server.Generation, metav1.ConditionTrue, commonv1alpha1.ReasonAvailable,
		"reachable and unsealed")
}

func (r *serverReconciler) referencing(kind string) handler.MapFunc {
	return func(ctx context.Context, obj client.Object) []reconcile.Request {
		list := &corev1alpha1.BaoServerList{}
		value := connection.ReferenceIndexValue(kind, obj.GetNamespace(), obj.GetName())
		if err := r.List(ctx, list, client.MatchingFields{referenceIndex: value}); err != nil {
			logf.FromContext(ctx).Error(err, "Failed to list BaoServers referencing object", "kind", kind,
				"namespace", obj.GetNamespace(), "name", obj.GetName())
			return nil
		}
		requests := make([]reconcile.Request, 0, len(list.Items))
		for _, s := range list.Items {
			requests = append(requests, reconcile.Request{NamespacedName: client.ObjectKeyFromObject(&s)})
		}
		return requests
	}
}
