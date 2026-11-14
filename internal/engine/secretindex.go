package engine

import (
	"context"
	"fmt"

	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/apiutil"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

// SecretRefIndex is the field index that maps a resource to the names of the Secrets it reads.
const SecretRefIndex = ".spec.secretRefs"

// SecretReferencer is implemented by adapters whose kind reads Secrets in its own namespace.
type SecretReferencer[T Object] interface {
	SecretRefs(obj T) []string
}

// IndexSecretRefs registers SecretRefIndex for the kind of obj.
func IndexSecretRefs[T Object](ctx context.Context, indexer client.FieldIndexer, obj T, refs SecretReferencer[T]) error {
	return indexer.IndexField(ctx, obj, SecretRefIndex, SecretRefsIndexFunc(refs))
}

// SecretRefsIndexFunc extracts the indexed Secret names; it is exported for fake clients in tests.
func SecretRefsIndexFunc[T Object](refs SecretReferencer[T]) client.IndexerFunc {
	return func(o client.Object) []string {
		obj, ok := o.(T)
		if !ok {
			return nil
		}
		return refs.SecretRefs(obj)
	}
}

// EnqueueForSecret maps a Secret to the resources of kind obj in its namespace that reference it through
// SecretRefIndex.
func EnqueueForSecret(reader client.Reader, scheme *runtime.Scheme, obj client.Object) (handler.EventHandler, error) {
	return enqueueByIndex(reader, scheme, obj, SecretRefIndex, func(secret client.Object) []string {
		return []string{secret.GetName()}
	})
}

// enqueueByIndex maps a watched object to the resources of kind obj in its namespace whose index holds any of
// the values keys returns for it.
func enqueueByIndex(reader client.Reader, scheme *runtime.Scheme, obj client.Object, index string,
	keys func(client.Object) []string) (handler.EventHandler, error) {
	gvk, err := apiutil.GVKForObject(obj, scheme)
	if err != nil {
		return nil, err
	}
	listGVK := gvk.GroupVersion().WithKind(gvk.Kind + "List")
	if _, err := scheme.New(listGVK); err != nil {
		return nil, fmt.Errorf("list type for %s: %w", gvk.Kind, err)
	}
	return handler.EnqueueRequestsFromMapFunc(func(ctx context.Context, watched client.Object) []reconcile.Request {
		seen := map[types.NamespacedName]bool{}
		var requests []reconcile.Request
		for _, key := range keys(watched) {
			newList, err := scheme.New(listGVK)
			if err != nil {
				return nil
			}
			list := newList.(client.ObjectList)
			if err := reader.List(ctx, list, client.InNamespace(watched.GetNamespace()),
				client.MatchingFields{index: key}); err != nil {
				log.FromContext(ctx).Error(err, "List dependent resources", "kind", gvk.Kind, "index", index,
					"object", client.ObjectKeyFromObject(watched))
				return nil
			}
			items, err := meta.ExtractList(list)
			if err != nil {
				return nil
			}
			for _, item := range items {
				accessor, err := meta.Accessor(item)
				if err != nil {
					continue
				}
				name := types.NamespacedName{Namespace: accessor.GetNamespace(), Name: accessor.GetName()}
				if !seen[name] {
					seen[name] = true
					requests = append(requests, reconcile.Request{NamespacedName: name})
				}
			}
		}
		return requests
	}), nil
}
