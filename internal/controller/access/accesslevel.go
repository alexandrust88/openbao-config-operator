package access

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"k8s.io/apimachinery/pkg/api/equality"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	accessv1alpha1 "github.com/alexandrust88/openbao-config-operator/api/access/v1alpha1"
	commonv1alpha1 "github.com/alexandrust88/openbao-config-operator/api/common/v1alpha1"
	"github.com/alexandrust88/openbao-config-operator/internal/engine"
)

// +kubebuilder:rbac:groups=access.obco.dev,resources=accesslevels,verbs=get;list;watch;update;patch
// +kubebuilder:rbac:groups=access.obco.dev,resources=accesslevels/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=access.obco.dev,resources=accesslevels/finalizers,verbs=update
// +kubebuilder:rbac:groups=access.obco.dev,resources=accessgrants;accesslayouts,verbs=get;list;watch

// SetupAccessLevel registers the AccessLevel controller with the manager. A level writes nothing to
// OpenBao, so the connections and engine options are not used.
func SetupAccessLevel(mgr ctrl.Manager, _ engine.Connections, opts engine.Options) error {
	r := &AccessLevelReconciler{Client: mgr.GetClient(), FieldOwner: opts.FieldOwner}
	if r.FieldOwner == "" {
		r.FieldOwner = engine.DefaultFieldOwner
	}
	cited := handler.EnqueueRequestsFromMapFunc(citedLevels)
	return ctrl.NewControllerManagedBy(mgr).
		For(&accessv1alpha1.AccessLevel{}).
		Watches(&accessv1alpha1.AccessGrant{}, cited).
		Watches(&accessv1alpha1.AccessLayout{}, cited).
		Named("accesslevel.access.obco.dev").
		Complete(r)
}

// AccessLevelReconciler keeps the in-use finalizer and the citedBy status of AccessLevels.
type AccessLevelReconciler struct {
	Client     client.Client
	FieldOwner string
}

// Reconcile adds the in-use finalizer and releases it on deletion once nothing cites the level.
func (r *AccessLevelReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	level := &accessv1alpha1.AccessLevel{}
	if err := r.Client.Get(ctx, req.NamespacedName, level); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	citedBy, err := citersOf(ctx, r.Client, level.Namespace, level.Name)
	if err != nil {
		return ctrl.Result{}, err
	}
	deleting := !level.DeletionTimestamp.IsZero()
	if deleting && len(citedBy) == 0 {
		return ctrl.Result{}, r.patchFinalizer(ctx, level, controllerutil.RemoveFinalizer)
	}
	if !deleting {
		if err := r.patchFinalizer(ctx, level, controllerutil.AddFinalizer); err != nil {
			return ctrl.Result{}, err
		}
	}

	before := level.DeepCopy()
	status := &level.Status
	status.CitedBy = citedBy
	status.ObservedGeneration = level.Generation
	ready := metav1.Condition{Type: commonv1alpha1.ConditionReady, Status: metav1.ConditionTrue,
		Reason: commonv1alpha1.ReasonAvailable, ObservedGeneration: level.Generation}
	if deleting {
		ready.Status = metav1.ConditionFalse
		ready.Reason = accessv1alpha1.ReasonInUse
		ready.Message = "deletion waits until no resource cites the level: " + strings.Join(citedBy, ", ")
	}
	meta.SetStatusCondition(&status.Conditions, ready)
	meta.SetStatusCondition(&status.Conditions, metav1.Condition{Type: commonv1alpha1.ConditionSynced,
		Status: metav1.ConditionTrue, Reason: commonv1alpha1.ReasonReconcileSuccess, ObservedGeneration: level.Generation})
	if equality.Semantic.DeepEqual(before.Status, level.Status) {
		return ctrl.Result{}, nil
	}
	if err := r.Client.Status().Patch(ctx, level, client.MergeFrom(before), client.FieldOwner(r.FieldOwner)); client.IgnoreNotFound(err) != nil {
		return ctrl.Result{}, fmt.Errorf("patch status: %w", err)
	}
	return ctrl.Result{}, nil
}

func (r *AccessLevelReconciler) patchFinalizer(ctx context.Context, level *accessv1alpha1.AccessLevel,
	mutate func(client.Object, string) bool) error {
	base := level.DeepCopy()
	if !mutate(level, accessv1alpha1.InUseFinalizer) {
		return nil
	}
	patch := client.MergeFromWithOptions(base, client.MergeFromWithOptimisticLock{})
	if err := r.Client.Patch(ctx, level, patch, client.FieldOwner(r.FieldOwner)); client.IgnoreNotFound(err) != nil {
		return fmt.Errorf("patch finalizer: %w", err)
	}
	return nil
}

// citedLevels maps an AccessGrant or AccessLayout to the levels it cites; on an update both the old and
// the new object are mapped, so a level that lost its last citer is reconciled too.
func citedLevels(_ context.Context, obj client.Object) []reconcile.Request {
	var levels []string
	switch o := obj.(type) {
	case *accessv1alpha1.AccessGrant:
		levels = grantLevels(o)
	case *accessv1alpha1.AccessLayout:
		levels = layoutLevels(o)
	}
	slices.Sort(levels)
	requests := make([]reconcile.Request, 0, len(levels))
	for _, name := range slices.Compact(levels) {
		requests = append(requests, reconcile.Request{NamespacedName: client.ObjectKey{Namespace: obj.GetNamespace(), Name: name}})
	}
	return requests
}

// citersOf lists the AccessGrants and AccessLayouts in namespace that cite level, as <Kind>/<name>.
func citersOf(ctx context.Context, reader client.Reader, namespace, level string) ([]string, error) {
	grants, err := grantsCiting(ctx, reader, namespace, level)
	if err != nil {
		return nil, err
	}
	layouts, err := layoutsCiting(ctx, reader, namespace, level)
	if err != nil {
		return nil, err
	}
	var citers []string
	for _, name := range grants {
		citers = append(citers, "AccessGrant/"+name)
	}
	for _, name := range layouts {
		citers = append(citers, "AccessLayout/"+name)
	}
	return citers, nil
}

func grantsCiting(ctx context.Context, reader client.Reader, namespace, level string) ([]string, error) {
	var list accessv1alpha1.AccessGrantList
	if err := reader.List(ctx, &list, client.InNamespace(namespace)); err != nil {
		return nil, fmt.Errorf("list AccessGrants: %w", err)
	}
	var names []string
	for i := range list.Items {
		if slices.Contains(grantLevels(&list.Items[i]), level) {
			names = append(names, list.Items[i].Name)
		}
	}
	return names, nil
}

func layoutsCiting(ctx context.Context, reader client.Reader, namespace, level string) ([]string, error) {
	var list accessv1alpha1.AccessLayoutList
	if err := reader.List(ctx, &list, client.InNamespace(namespace)); err != nil {
		return nil, fmt.Errorf("list AccessLayouts: %w", err)
	}
	var names []string
	for i := range list.Items {
		if slices.Contains(layoutLevels(&list.Items[i]), level) {
			names = append(names, list.Items[i].Name)
		}
	}
	return names, nil
}

// enqueueCiters maps an AccessLevel to the resources in its namespace that cite it.
func enqueueCiters(reader client.Reader,
	citing func(ctx context.Context, reader client.Reader, namespace, level string) ([]string, error)) handler.EventHandler {
	return handler.EnqueueRequestsFromMapFunc(func(ctx context.Context, level client.Object) []reconcile.Request {
		names, err := citing(ctx, reader, level.GetNamespace(), level.GetName())
		if err != nil {
			ctrl.LoggerFrom(ctx).Error(err, "Failed to map an AccessLevel to its citers", "level", level.GetName())
			return nil
		}
		requests := make([]reconcile.Request, 0, len(names))
		for _, name := range names {
			requests = append(requests, reconcile.Request{NamespacedName: client.ObjectKey{Namespace: level.GetNamespace(), Name: name}})
		}
		return requests
	})
}
