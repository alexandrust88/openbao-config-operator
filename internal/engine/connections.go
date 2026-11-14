package engine

import (
	"fmt"

	"k8s.io/apimachinery/pkg/api/meta"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/apiutil"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/predicate"

	commonv1alpha1 "github.com/alexandrust88/openbao-config-operator/api/common/v1alpha1"
	corev1alpha1 "github.com/alexandrust88/openbao-config-operator/api/core/v1alpha1"
)

// ConnectionIndex is the field index that maps a resource to the connection it references, in the form
// "OpenBaoConnection/<namespace>/<name>" or "ClusterOpenBaoConnection/<name>".
const ConnectionIndex = ".spec.connectionRef.key"

// ConnectionIndexFunc extracts the ConnectionIndex value; it is exported for fake clients in tests.
func ConnectionIndexFunc(o client.Object) []string {
	managed, ok := o.(commonv1alpha1.Managed)
	if !ok {
		return nil
	}
	return []string{ConnectionKey(o.GetNamespace(), managed.GetResourceSpec().ConnectionRef)}
}

// watchConnections re-queues the resources of a connection as soon as its Ready condition changes, instead of
// waiting for the ConnectionNotReady retry. Connection kinds that the scheme or the API server do not serve are
// skipped.
func (r *Reconciler[T]) watchConnections(mgr ctrl.Manager, b *builder.Builder) error {
	for _, conn := range []client.Object{&corev1alpha1.OpenBaoConnection{}, &corev1alpha1.ClusterOpenBaoConnection{}} {
		served, err := r.served(mgr, conn)
		if err != nil {
			return err
		}
		if !served {
			continue
		}
		enqueue, err := enqueueByIndex(mgr.GetClient(), r.scheme, r.newObj(), ConnectionIndex, func(c client.Object) []string {
			ref := commonv1alpha1.ConnectionReference{Kind: commonv1alpha1.ConnectionKindNamespaced, Name: c.GetName()}
			if _, cluster := c.(*corev1alpha1.ClusterOpenBaoConnection); cluster {
				ref.Kind = commonv1alpha1.ConnectionKindCluster
			}
			return []string{ConnectionKey(c.GetNamespace(), ref)}
		})
		if err != nil {
			return err
		}
		b.Watches(conn, enqueue, builder.WithPredicates(connectionReadyChanged()))
	}
	return nil
}

// served reports whether the scheme knows obj and the API server serves its kind, so that a watch on it can start.
func (r *Reconciler[T]) served(mgr ctrl.Manager, obj client.Object) (bool, error) {
	gvk, err := apiutil.GVKForObject(obj, r.scheme)
	if err != nil {
		return false, nil //nolint:nilerr // a kind outside the scheme is simply not watched
	}
	if _, err := mgr.GetRESTMapper().RESTMapping(gvk.GroupKind(), gvk.Version); err != nil {
		if meta.IsNoMatchError(err) {
			return false, nil
		}
		return false, fmt.Errorf("look up %s: %w", gvk.Kind, err)
	}
	return true, nil
}

type connectionStatusHolder interface {
	GetConnectionStatus() *corev1alpha1.ConnectionStatus
}

// connectionReadyChanged passes connection updates that change the status of the Ready condition.
func connectionReadyChanged() predicate.Funcs {
	ready := func(o client.Object) bool {
		holder, ok := o.(connectionStatusHolder)
		return ok && meta.IsStatusConditionTrue(holder.GetConnectionStatus().Conditions, commonv1alpha1.ConditionReady)
	}
	return predicate.Funcs{
		CreateFunc:  func(event.CreateEvent) bool { return false },
		DeleteFunc:  func(event.DeleteEvent) bool { return false },
		GenericFunc: func(event.GenericEvent) bool { return false },
		UpdateFunc: func(e event.UpdateEvent) bool {
			return e.ObjectOld != nil && e.ObjectNew != nil && ready(e.ObjectOld) != ready(e.ObjectNew)
		},
	}
}
