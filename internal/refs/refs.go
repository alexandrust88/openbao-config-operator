// Package refs resolves flexible references between managed kinds. A reference is exactly one of a literal
// OpenBao value, an OpenBao name or path, a Kubernetes object in the same namespace, or a label selector over
// such objects. Kubernetes referents must be Ready and provide the value in status.id.
package refs

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"

	commonv1alpha1 "github.com/alexandrust88/openbao-config-operator/api/common/v1alpha1"
	identityv1alpha1 "github.com/alexandrust88/openbao-config-operator/api/identity/v1alpha1"
	sysv1alpha1 "github.com/alexandrust88/openbao-config-operator/api/sys/v1alpha1"
	"github.com/alexandrust88/openbao-config-operator/internal/engine"
	"github.com/alexandrust88/openbao-config-operator/internal/openbao"
)

// Ref is one flexible reference. Exactly one field is set; CRDs enforce that with CEL.
type Ref struct {
	// Literal is the OpenBao value itself, such as an entity ID or a mount accessor.
	Literal string
	// Name is an OpenBao name or mount path, resolved through the connection.
	Name string
	// ObjectName names a Kubernetes object of the target kind in the referring object's namespace.
	ObjectName string
	// MatchLabels selects Kubernetes objects of the target kind in the referring object's namespace.
	MatchLabels map[string]string
}

// Target is the kind of object a Ref points at.
type Target struct {
	// GVK is the Kubernetes kind whose status.id holds the value.
	GVK schema.GroupVersionKind
	// Lookup resolves an OpenBao name or path to the value. It wraps engine.ErrReferenceNotReady when the
	// object does not exist.
	Lookup func(ctx context.Context, c openbao.Client, name string) (string, error)
}

// Targets shared by the kinds that reference auth mounts, entities and groups.
var (
	AuthMount    = Target{GVK: sysv1alpha1.GroupVersion.WithKind("AuthMount"), Lookup: AuthMountAccessor}
	SecretsMount = Target{GVK: sysv1alpha1.GroupVersion.WithKind("SecretsMount")}
	Entity       = Target{GVK: identityv1alpha1.GroupVersion.WithKind("IdentityEntity"), Lookup: EntityID}
	Group        = Target{GVK: identityv1alpha1.GroupVersion.WithKind("IdentityGroup"), Lookup: GroupID}
)

// Reader reads referents from Kubernetes; a controller-runtime client.Client satisfies it.
type Reader interface {
	client.Reader
	Scheme() *runtime.Scheme
}

// Resolver resolves the references of one object.
type Resolver struct {
	// Reader reads Kubernetes referents.
	Reader Reader
	// Namespace is the Kubernetes namespace of the referring object.
	Namespace string
	// OpenBao returns the client scoped to the referring object's OpenBao namespace. It is called only for
	// Name references.
	OpenBao func(ctx context.Context) (openbao.Client, error)
}

// For returns a Resolver for obj that reaches OpenBao the way the engine does: through the object's
// connectionRef, scoped to spec.openbaoNamespace.
func For(reader Reader, conns engine.Connections, obj engine.Object) Resolver {
	return Resolver{
		Reader:    reader,
		Namespace: obj.GetNamespace(),
		OpenBao: func(ctx context.Context) (openbao.Client, error) {
			spec := obj.GetResourceSpec()
			c, err := conns.For(ctx, obj.GetNamespace(), spec.ConnectionRef)
			if err != nil {
				return nil, err
			}
			if spec.Namespace != "" {
				c = c.WithNamespace(spec.Namespace)
			}
			return c, nil
		},
	}
}

// WithClient returns a Resolver that uses c, an already scoped client, for Name references.
func WithClient(reader Reader, c openbao.Client, namespace string) Resolver {
	return Resolver{
		Reader:    reader,
		Namespace: namespace,
		OpenBao:   func(context.Context) (openbao.Client, error) { return c, nil },
	}
}

// One resolves ref to a single value. A selector must match exactly one object.
func (r Resolver) One(ctx context.Context, t Target, ref Ref) (string, error) {
	if err := ref.validate(); err != nil {
		return "", err
	}
	if len(ref.MatchLabels) == 0 {
		return r.single(ctx, t, ref)
	}
	values, err := r.selected(ctx, t, ref.MatchLabels)
	if err != nil {
		return "", err
	}
	if len(values) != 1 {
		return "", fmt.Errorf("%s selector %v matches %d objects in %s, want exactly one: %w",
			t.GVK.Kind, ref.MatchLabels, len(values), r.Namespace, engine.ErrReferenceNotReady)
	}
	return values[0], nil
}

// All resolves ref to a sorted list of distinct values. A selector may match any number of objects.
func (r Resolver) All(ctx context.Context, t Target, ref Ref) ([]string, error) {
	if err := ref.validate(); err != nil {
		return nil, err
	}
	if len(ref.MatchLabels) == 0 {
		value, err := r.single(ctx, t, ref)
		if err != nil {
			return nil, err
		}
		return []string{value}, nil
	}
	values, err := r.selected(ctx, t, ref.MatchLabels)
	if err != nil {
		return nil, err
	}
	slices.Sort(values)
	return slices.Compact(values), nil
}

// Watch returns the engine.ObjectReference an adapter reports through engine.ReferenceWatcher for ref: the named
// object, or any object of the kind for a selector. ok is false for literal and OpenBao name references.
func (ref Ref) Watch(t Target) (engine.ObjectReference, bool) {
	switch {
	case ref.ObjectName != "":
		return engine.ObjectReference{Kind: t.GVK.Kind, Name: ref.ObjectName}, true
	case len(ref.MatchLabels) > 0:
		return engine.ObjectReference{Kind: t.GVK.Kind}, true
	default:
		return engine.ObjectReference{}, false
	}
}

// IsZero reports whether no form of the reference is set.
func (ref Ref) IsZero() bool {
	return ref.Literal == "" && ref.Name == "" && ref.ObjectName == "" && len(ref.MatchLabels) == 0
}

func (ref Ref) validate() error {
	set := 0
	for _, isSet := range []bool{ref.Literal != "", ref.Name != "", ref.ObjectName != "", len(ref.MatchLabels) > 0} {
		if isSet {
			set++
		}
	}
	if set != 1 {
		return fmt.Errorf("a reference needs exactly one of a literal, a name, an object or a selector, got %d", set)
	}
	return nil
}

func (r Resolver) single(ctx context.Context, t Target, ref Ref) (string, error) {
	switch {
	case ref.Literal != "":
		return ref.Literal, nil
	case ref.Name != "":
		if r.OpenBao == nil || t.Lookup == nil {
			return "", fmt.Errorf("%s %q cannot be resolved by name", t.GVK.Kind, ref.Name)
		}
		c, err := r.OpenBao(ctx)
		if err != nil {
			return "", err
		}
		return t.Lookup(ctx, c, ref.Name)
	default:
		objs, err := r.objects(ctx, t.GVK, ref)
		if err != nil {
			return "", err
		}
		status, err := r.readyStatus(ctx, t.GVK.Kind, objs[0])
		if err != nil {
			return "", err
		}
		return status.ID, nil
	}
}

func (r Resolver) selected(ctx context.Context, t Target, labels map[string]string) ([]string, error) {
	objs, err := r.objects(ctx, t.GVK, Ref{MatchLabels: labels})
	if err != nil {
		return nil, err
	}
	values := make([]string, 0, len(objs))
	for _, obj := range objs {
		status, err := r.readyStatus(ctx, t.GVK.Kind, obj)
		if err != nil {
			return nil, err
		}
		values = append(values, status.ID)
	}
	return values, nil
}

func (r Resolver) newObject(gvk schema.GroupVersionKind) (client.Object, error) {
	raw, err := r.Reader.Scheme().New(gvk)
	if err != nil {
		return nil, fmt.Errorf("%s is not registered: %w", gvk.Kind, err)
	}
	obj, ok := raw.(client.Object)
	if !ok {
		return nil, fmt.Errorf("%s is not an object", gvk.Kind)
	}
	return obj, nil
}

// readyStatus returns the status of a referent that is Ready for its current generation, has an ID, and was
// reconciled on the referring object's server and in its OpenBao namespace; an ID or path from another server or
// namespace would be rejected, misread or, worse, name a different object there. A referent without status.path
// was not written by the engine and has no recorded namespace to compare.
func (r Resolver) readyStatus(ctx context.Context, kind string, obj client.Object) (*commonv1alpha1.ResourceStatus,
	error) {
	managed, ok := obj.(commonv1alpha1.Managed)
	if !ok {
		return nil, fmt.Errorf("%s is not a managed kind", kind)
	}
	status := managed.GetResourceStatus()
	ready := meta.FindStatusCondition(status.Conditions, commonv1alpha1.ConditionReady)
	if ready == nil || ready.Status != metav1.ConditionTrue || ready.ObservedGeneration != obj.GetGeneration() ||
		status.ID == "" {
		return nil, fmt.Errorf("%s %s/%s is not ready: %w", kind, obj.GetNamespace(), obj.GetName(),
			engine.ErrReferenceNotReady)
	}
	if r.OpenBao == nil {
		return status, nil
	}
	c, err := r.OpenBao(ctx)
	if err != nil {
		return nil, err
	}
	id, ok, err := engine.ClusterIDOf(ctx, c)
	if err != nil {
		return nil, err
	}
	if ok && status.ServerID != id {
		return nil, fmt.Errorf("%s %s/%s was reconciled on OpenBao server %q, not %q like the referring resource: %w",
			kind, obj.GetNamespace(), obj.GetName(), status.ServerID, id, engine.ErrReferenceNotReady)
	}
	if status.Path == "" {
		return status, nil
	}
	if want := canonicalNamespace(c.Namespace()); canonicalNamespace(status.Namespace) != want {
		return nil, fmt.Errorf("%s %s/%s is in OpenBao namespace %q, not %q like the referring resource",
			kind, obj.GetNamespace(), obj.GetName(), status.Namespace, want)
	}
	return status, nil
}

func canonicalNamespace(ns string) string {
	return strings.Join(strings.FieldsFunc(ns, func(r rune) bool { return r == '/' }), "/")
}

// AuthMountPath resolves a mount reference (mountPath, mountRef, mountSelector or a literal accessor) to the path
// of an auth mount, without the "auth/" prefix or a trailing slash.
func (r Resolver) AuthMountPath(ctx context.Context, ref Ref) (string, error) {
	return r.mountPath(ctx, AuthMount.GVK, "sys/auth", ref)
}

// SecretsMountPath resolves a mount reference (mountPath, mountRef, mountSelector or a literal accessor) to the
// path of a secrets engine mount, without a trailing slash.
func (r Resolver) SecretsMountPath(ctx context.Context, ref Ref) (string, error) {
	return r.mountPath(ctx, SecretsMount.GVK, "sys/mounts", ref)
}

// mountPath reads a mount's path from the referent's status.path (sys/auth/<path> or sys/mounts/<path> under
// status.openbaoNamespace), or finds a literal accessor in the mount table.
func (r Resolver) mountPath(ctx context.Context, gvk schema.GroupVersionKind, table string, ref Ref) (string, error) {
	if err := ref.validate(); err != nil {
		return "", err
	}
	switch {
	case ref.Name != "":
		return strings.Trim(ref.Name, "/"), nil
	case ref.Literal != "":
		return r.mountPathByAccessor(ctx, table, ref.Literal)
	}
	objs, err := r.objects(ctx, gvk, ref)
	if err != nil {
		return "", err
	}
	if len(objs) != 1 {
		return "", fmt.Errorf("%s selector %v matches %d objects in %s, want exactly one: %w",
			gvk.Kind, ref.MatchLabels, len(objs), r.Namespace, engine.ErrReferenceNotReady)
	}
	status, err := r.readyStatus(ctx, gvk.Kind, objs[0])
	if err != nil {
		return "", err
	}
	relative := strings.TrimPrefix(strings.Trim(status.Path, "/"), canonicalNamespace(status.Namespace))
	path, ok := strings.CutPrefix(strings.Trim(relative, "/"), table+"/")
	if !ok || path == "" {
		return "", fmt.Errorf("%s %s/%s has no mount path in status.path: %w", gvk.Kind, objs[0].GetNamespace(),
			objs[0].GetName(), engine.ErrReferenceNotReady)
	}
	return path, nil
}

func (r Resolver) mountPathByAccessor(ctx context.Context, table, accessor string) (string, error) {
	if r.OpenBao == nil {
		return "", fmt.Errorf("mount accessor %q cannot be resolved without a connection", accessor)
	}
	c, err := r.OpenBao(ctx)
	if err != nil {
		return "", err
	}
	mounts, err := c.Read(ctx, table)
	if err != nil {
		return "", fmt.Errorf("read %s: %w", table, err)
	}
	for path, raw := range mounts {
		if entry, _ := raw.(map[string]any); entry["accessor"] == accessor {
			return strings.Trim(path, "/"), nil
		}
	}
	return "", fmt.Errorf("no mount in %s has accessor %q: %w", table, accessor, engine.ErrReferenceNotReady)
}

// objects returns the referent named by ref.ObjectName, or the referents ref.MatchLabels selects.
func (r Resolver) objects(ctx context.Context, gvk schema.GroupVersionKind, ref Ref) ([]client.Object, error) {
	if ref.ObjectName != "" {
		obj, err := r.newObject(gvk)
		if err != nil {
			return nil, err
		}
		key := client.ObjectKey{Namespace: r.Namespace, Name: ref.ObjectName}
		if err := r.Reader.Get(ctx, key, obj); err != nil {
			if client.IgnoreNotFound(err) == nil {
				return nil, fmt.Errorf("%s %s not found: %w", gvk.Kind, key, engine.ErrReferenceNotReady)
			}
			return nil, fmt.Errorf("read %s %s: %w", gvk.Kind, key, err)
		}
		return []client.Object{obj}, nil
	}
	listGVK := gvk.GroupVersion().WithKind(gvk.Kind + "List")
	raw, err := r.Reader.Scheme().New(listGVK)
	if err != nil {
		return nil, fmt.Errorf("%s is not registered: %w", listGVK.Kind, err)
	}
	list, ok := raw.(client.ObjectList)
	if !ok {
		return nil, fmt.Errorf("%s is not a list", listGVK.Kind)
	}
	if err := r.Reader.List(ctx, list, client.InNamespace(r.Namespace), client.MatchingLabels(ref.MatchLabels)); err != nil {
		return nil, fmt.Errorf("list %s: %w", listGVK.Kind, err)
	}
	items, err := meta.ExtractList(list)
	if err != nil {
		return nil, err
	}
	objs := make([]client.Object, 0, len(items))
	for _, item := range items {
		obj, ok := item.(client.Object)
		if !ok {
			return nil, fmt.Errorf("%s item is not an object", listGVK.Kind)
		}
		objs = append(objs, obj)
	}
	return objs, nil
}

// AuthMountAccessor returns the accessor of the auth mount at path, from the sys/auth table.
func AuthMountAccessor(ctx context.Context, c openbao.Client, path string) (string, error) {
	mounts, err := c.Read(ctx, "sys/auth")
	if err != nil {
		return "", fmt.Errorf("read sys/auth: %w", err)
	}
	entry, _ := mounts[strings.Trim(path, "/")+"/"].(map[string]any)
	accessor, _ := entry["accessor"].(string)
	if accessor == "" {
		return "", fmt.Errorf("auth mount %q not found: %w", path, engine.ErrReferenceNotReady)
	}
	return accessor, nil
}

// EntityID returns the ID of the entity called name.
func EntityID(ctx context.Context, c openbao.Client, name string) (string, error) {
	return idByName(ctx, c, "identity/entity/name/", "entity", name)
}

// GroupID returns the ID of the group called name.
func GroupID(ctx context.Context, c openbao.Client, name string) (string, error) {
	return idByName(ctx, c, "identity/group/name/", "group", name)
}

func idByName(ctx context.Context, c openbao.Client, prefix, what, name string) (string, error) {
	data, err := c.Read(ctx, prefix+name)
	if errors.Is(err, openbao.ErrNotFound) {
		return "", fmt.Errorf("%s %q not found: %w", what, name, engine.ErrReferenceNotReady)
	}
	if err != nil {
		return "", fmt.Errorf("read %s %q: %w", what, name, err)
	}
	id, _ := data["id"].(string)
	if id == "" {
		return "", fmt.Errorf("%s %q has no id: %w", what, name, engine.ErrReferenceNotReady)
	}
	return id, nil
}
