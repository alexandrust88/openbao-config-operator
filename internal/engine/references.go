package engine

import (
	"context"
	"fmt"

	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/apiutil"
	"sigs.k8s.io/controller-runtime/pkg/handler"
)

// ReferenceIndex is the field index that maps a resource to the Kubernetes objects it references.
const ReferenceIndex = ".spec.objectRefs"

// ObjectReference names a referenced Kubernetes object in the referring resource's namespace. Kind is the
// referent's Kind; an empty Name means any object of that kind, for selector references.
type ObjectReference struct {
	Kind string
	Name string
}

// ReferenceWatcher is implemented by adapters whose kind references other managed objects (xxxRef or
// xxxSelector fields). The engine watches the referenced kinds and re-queues a resource as soon as one of its
// referents changes, instead of waiting for the ReferenceNotReady retry.
type ReferenceWatcher[T Object] interface {
	// ReferencedKinds returns one empty object per referenced kind.
	ReferencedKinds() []client.Object
	// References returns the objects obj references.
	References(obj T) []ObjectReference
}

func referenceKey(kind, name string) string {
	if name == "" {
		name = "*"
	}
	return kind + "/" + name
}

// ReferencesIndexFunc extracts the ReferenceIndex values; it is exported for fake clients in tests.
func ReferencesIndexFunc[T Object](refs ReferenceWatcher[T]) client.IndexerFunc {
	return func(o client.Object) []string {
		obj, ok := o.(T)
		if !ok {
			return nil
		}
		var keys []string
		for _, ref := range refs.References(obj) {
			keys = append(keys, referenceKey(ref.Kind, ref.Name))
		}
		return keys
	}
}

// EnqueueForReferent maps a referent to the resources of kind obj in its namespace that reference it by name or
// by a selector over its kind.
func EnqueueForReferent(reader client.Reader, scheme *runtime.Scheme, obj client.Object) (handler.EventHandler, error) {
	return enqueueByIndex(reader, scheme, obj, ReferenceIndex, func(referent client.Object) []string {
		gvk, err := apiutil.GVKForObject(referent, scheme)
		if err != nil {
			return nil
		}
		return []string{referenceKey(gvk.Kind, referent.GetName()), referenceKey(gvk.Kind, "")}
	})
}

func indexReferences[T Object](ctx context.Context, indexer client.FieldIndexer, obj T, refs ReferenceWatcher[T]) error {
	if err := indexer.IndexField(ctx, obj, ReferenceIndex, ReferencesIndexFunc(refs)); err != nil {
		return fmt.Errorf("index references: %w", err)
	}
	return nil
}
