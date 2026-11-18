package core

import (
	"context"
	"slices"
	"strings"
	"time"

	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	commonv1alpha1 "github.com/alexandrust88/openbao-config-operator/api/common/v1alpha1"
	"github.com/alexandrust88/openbao-config-operator/internal/engine"
)

const (
	// connectionFinalizer holds a connection while a resource still needs it to clean up in OpenBao.
	connectionFinalizer = "obco.dev/connection-in-use"
	// serverFinalizer holds a BaoServer while a connection still references it.
	serverFinalizer = "obco.dev/server-in-use"
	// connectionUseIndex maps a managed resource that holds the engine finalizer to its connection key.
	connectionUseIndex = "spec.connectionRef.inUse"
	inUseRecheck       = 5 * time.Second
)

// indexManagedKinds indexes every managed kind in the manager's scheme by the connection it references while it
// holds the engine finalizer, and returns a list constructor per kind. Kinds whose CRD is not installed are
// skipped: they can hold no connection.
func indexManagedKinds(ctx context.Context, mgr ctrl.Manager) ([]func() client.ObjectList, error) {
	scheme := mgr.GetScheme()
	var gvks []schema.GroupVersionKind
	for gvk := range scheme.AllKnownTypes() {
		if strings.HasSuffix(gvk.Group, ".obco.dev") && !strings.HasSuffix(gvk.Kind, "List") {
			gvks = append(gvks, gvk)
		}
	}
	slices.SortFunc(gvks, func(a, b schema.GroupVersionKind) int { return strings.Compare(a.String(), b.String()) })

	var lists []func() client.ObjectList
	for _, gvk := range gvks {
		obj, err := scheme.New(gvk)
		if err != nil {
			return nil, err
		}
		managed, ok := obj.(client.Object)
		if _, isManaged := obj.(commonv1alpha1.Managed); !ok || !isManaged {
			continue
		}
		listGVK := gvk.GroupVersion().WithKind(gvk.Kind + "List")
		if _, err := scheme.New(listGVK); err != nil {
			return nil, err
		}
		err = mgr.GetFieldIndexer().IndexField(ctx, managed, connectionUseIndex, connectionInUse)
		if meta.IsNoMatchError(err) {
			logf.FromContext(ctx).Info("Skipped kind without CRD for connection use tracking", "kind", gvk.String())
			continue
		}
		if err != nil {
			return nil, err
		}
		lists = append(lists, func() client.ObjectList {
			list, _ := scheme.New(listGVK)
			return list.(client.ObjectList)
		})
	}
	return lists, nil
}

// connectionInUse is the connectionUseIndex value of a managed resource: its connection key while it holds the
// engine finalizer, so it may still need the connection to delete its OpenBao object.
func connectionInUse(obj client.Object) []string {
	m, ok := obj.(commonv1alpha1.Managed)
	if !ok || !controllerutil.ContainsFinalizer(obj, engine.DefaultFinalizer) {
		return nil
	}
	return []string{engine.ConnectionKey(obj.GetNamespace(), m.GetResourceSpec().ConnectionRef)}
}

// anyListed reports whether one of the lists has an item matching opts for which counts reports true.
func anyListed(ctx context.Context, c client.Reader, lists []func() client.ObjectList,
	counts func(client.Object) (bool, error), opts ...client.ListOption) (bool, error) {
	for _, newList := range lists {
		list := newList()
		if err := c.List(ctx, list, opts...); err != nil {
			return false, err
		}
		found := false
		err := meta.EachListItem(list, func(item runtime.Object) error {
			obj, ok := item.(client.Object)
			if !ok || found {
				return nil
			}
			var err error
			found, err = counts(obj)
			return err
		})
		if err != nil || found {
			return found, err
		}
	}
	return false, nil
}

// countAll is the anyListed filter that counts every item.
func countAll(client.Object) (bool, error) { return true, nil }

// ensureFinalizer adds finalizer to obj when it is missing.
func ensureFinalizer(ctx context.Context, c client.Client, obj client.Object, finalizer string) error {
	if controllerutil.ContainsFinalizer(obj, finalizer) {
		return nil
	}
	base := obj.DeepCopyObject().(client.Object)
	controllerutil.AddFinalizer(obj, finalizer)
	return c.Patch(ctx, obj, client.MergeFromWithOptions(base, client.MergeFromWithOptimisticLock{}))
}

// releaseFinalizer removes finalizer from obj.
func releaseFinalizer(ctx context.Context, c client.Client, obj client.Object, finalizer string) error {
	base := obj.DeepCopyObject().(client.Object)
	controllerutil.RemoveFinalizer(obj, finalizer)
	return client.IgnoreNotFound(c.Patch(ctx, obj, client.MergeFromWithOptions(base, client.MergeFromWithOptimisticLock{})))
}
