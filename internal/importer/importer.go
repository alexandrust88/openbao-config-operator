// Package importer reads a live OpenBao server and builds the custom resources that describe it, with
// managementPolicy Observe and creationPolicy Adopt, so a running server can be adopted one object at a time.
package importer

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"reflect"
	"slices"
	"sort"
	"strings"

	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"

	authv1alpha1 "github.com/alexandrust88/openbao-config-operator/api/auth/v1alpha1"
	commonv1alpha1 "github.com/alexandrust88/openbao-config-operator/api/common/v1alpha1"
	corev1alpha1 "github.com/alexandrust88/openbao-config-operator/api/core/v1alpha1"
	identityv1alpha1 "github.com/alexandrust88/openbao-config-operator/api/identity/v1alpha1"
	secretsv1alpha1 "github.com/alexandrust88/openbao-config-operator/api/secrets/v1alpha1"
	sysv1alpha1 "github.com/alexandrust88/openbao-config-operator/api/sys/v1alpha1"
	"github.com/alexandrust88/openbao-config-operator/internal/engine"
	"github.com/alexandrust88/openbao-config-operator/internal/openbao"
)

// Options selects what is imported and how the emitted resources reach OpenBao.
type Options struct {
	// Connection is the connectionRef of every emitted resource; its namespace is the one the client reads.
	Connection commonv1alpha1.ConnectionReference
	// Namespace is the Kubernetes namespace of the emitted resources.
	Namespace string
	// Recursive also imports every child OpenBao namespace.
	Recursive bool
	// IncludeKVPaths emits a KVSecret skeleton for every KV path; values are never read.
	IncludeKVPaths bool
	// Server, when set, also emits the BaoServer and the connection that Connection names.
	Server *Server
}

// Placeholder is a write-only input OpenBao never returns: the emitted resource references a Secret that must be
// created before the resource is switched to managementPolicy Manage.
type Placeholder struct {
	Object string
	Field  string
	Secret string
	Key    string
}

// Result holds the imported resources, sorted by kind and name, and what could not be imported faithfully.
type Result struct {
	Objects      []client.Object
	Placeholders []Placeholder
	// Notes name objects that were skipped or will report drift, and fields that could not be mapped.
	Notes []string
}

// Import reads OpenBao through c, scoped to the namespace the connection points at, and returns the resources.
func Import(ctx context.Context, c openbao.Client, opts Options) (*Result, error) {
	scheme, err := newScheme()
	if err != nil {
		return nil, err
	}
	imp := &importer{opts: opts, base: c, scheme: scheme, used: map[string]map[string]bool{}, result: &Result{}}
	if err := imp.namespace(ctx, ""); err != nil {
		return nil, err
	}
	if opts.Server != nil {
		imp.result.Objects = append(imp.result.Objects, imp.serverObjects()...)
	}
	sortResult(imp.result)
	return imp.result, nil
}

func newScheme() (*runtime.Scheme, error) {
	scheme := runtime.NewScheme()
	for _, add := range []func(*runtime.Scheme) error{
		corev1alpha1.AddToScheme, sysv1alpha1.AddToScheme, authv1alpha1.AddToScheme, identityv1alpha1.AddToScheme, secretsv1alpha1.AddToScheme,
	} {
		if err := add(scheme); err != nil {
			return nil, err
		}
	}
	return scheme, nil
}

type importer struct {
	opts   Options
	base   openbao.Client
	scheme *runtime.Scheme
	used   map[string]map[string]bool
	result *Result
}

// mount is an entry of the sys/auth or sys/mounts table.
type mount struct {
	typ      string
	accessor string
	options  map[string]any
}

// scope is one OpenBao namespace being imported.
type scope struct {
	*importer
	c       openbao.Client
	ns      string
	auth    map[string]mount
	secrets map[string]mount
	cache   map[string]map[string]string
}

func (imp *importer) namespace(ctx context.Context, ns string) error {
	c := imp.base
	if ns != "" {
		c = imp.base.WithNamespace(ns)
	}
	s := &scope{importer: imp, c: c, ns: ns, cache: map[string]map[string]string{}}
	var err error
	if s.auth, err = s.mounts(ctx, "sys/auth"); err == nil {
		s.secrets, err = s.mounts(ctx, "sys/mounts")
	}
	if err != nil && ns != "" {
		s.note("OpenBao namespace %q not imported: %v", c.Namespace(), err)
		return nil
	}
	if err != nil {
		return err
	}
	for _, k := range kinds(imp.base) {
		k.run(ctx, s)
	}
	if !imp.opts.Recursive {
		return nil
	}
	children, err := s.keys(ctx, "sys/namespaces")
	if err != nil {
		return fmt.Errorf("list namespaces in %q: %w", c.Namespace(), err)
	}
	for _, child := range children {
		if err := imp.namespace(ctx, joinNamespace(ns, child)); err != nil {
			return err
		}
	}
	return nil
}

func joinNamespace(parent, child string) string {
	child = strings.Trim(child, "/")
	if parent == "" {
		return child
	}
	return parent + "/" + child
}

func (s *scope) mounts(ctx context.Context, table string) (map[string]mount, error) {
	data, err := s.c.Read(ctx, table)
	if err != nil {
		return nil, fmt.Errorf("read %s in %q: %w", table, s.c.Namespace(), err)
	}
	out := map[string]mount{}
	for path, raw := range data {
		entry, ok := raw.(map[string]any)
		if !ok || !strings.HasSuffix(path, "/") {
			continue
		}
		m := mount{}
		m.typ, _ = entry["type"].(string)
		m.accessor, _ = entry["accessor"].(string)
		m.options, _ = entry["options"].(map[string]any)
		out[strings.TrimSuffix(path, "/")] = m
	}
	return out, nil
}

// keys lists path; a missing path is an empty list. Keys come back sorted.
func (s *scope) keys(ctx context.Context, path string) ([]string, error) {
	keys, err := s.c.List(ctx, path)
	if errors.Is(err, openbao.ErrNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	slices.Sort(keys)
	return keys, nil
}

// mountsOf returns the sorted paths of the mounts in table whose type is one of types.
func mountsOf(table map[string]mount, types ...string) []string {
	var paths []string
	for path, m := range table {
		if slices.Contains(types, m.typ) {
			paths = append(paths, path)
		}
	}
	slices.Sort(paths)
	return paths
}

// root reports whether the scope is the root namespace of the server.
func (s *scope) root() bool {
	return strings.Trim(s.c.Namespace(), "/") == ""
}

func (s *scope) note(format string, args ...any) {
	s.result.Notes = append(s.result.Notes, fmt.Sprintf(format, args...))
}

// prepare sets the fields every emitted resource shares and gives obj a name unique within its kind.
func (s *scope) prepare(obj engine.Object, gvk schema.GroupVersionKind, parts ...string) {
	obj.GetObjectKind().SetGroupVersionKind(gvk)
	obj.SetNamespace(s.opts.Namespace)
	spec := obj.GetResourceSpec()
	spec.ConnectionRef = s.opts.Connection
	spec.Namespace = s.ns
	spec.CreationPolicy = commonv1alpha1.CreationPolicyAdopt
	spec.ManagementPolicy = commonv1alpha1.ManagementPolicyObserve

	used := s.used[gvk.Kind]
	if used == nil {
		used = map[string]bool{}
		s.used[gvk.Kind] = used
	}
	name := objectName(append([]string{s.ns}, parts...)...)
	unique := name
	for i := 2; used[unique]; i++ {
		unique = withSuffix(name, fmt.Sprintf("-%d", i))
	}
	used[unique] = true
	obj.SetName(unique)
}

func (s *scope) label(obj engine.Object) string {
	return obj.GetObjectKind().GroupVersionKind().Kind + "/" + obj.GetName()
}

// importResolver resolves Secret references to a fixed value; write-only keys are never compared, so the value
// never matters. Kubernetes references are not used by imported resources.
type importResolver struct{}

func (importResolver) SecretValue(context.Context, string, commonv1alpha1.SecretKeySelector) ([]byte, error) {
	return []byte("imported"), nil
}

func (importResolver) Object(_ context.Context, key client.ObjectKey, _ client.Object) error {
	return fmt.Errorf("%s: %w", key, engine.ErrReferenceNotReady)
}

// staticConns hands adapters that resolve OpenBao names the client of the imported connection.
type staticConns struct{ c openbao.Client }

func (s staticConns) For(context.Context, string, commonv1alpha1.ConnectionReference) (openbao.Client, error) {
	return s.c, nil
}

// fieldSpec merges the adapter's per-object rules over Fields(), as the engine does.
func fieldSpec[T engine.Object](ctx context.Context, a engine.Adapter[T], obj T) (engine.FieldSpec, error) {
	spec := a.Fields()
	provider, ok := any(a).(engine.FieldsProvider[T])
	if !ok {
		return spec, nil
	}
	extra, err := provider.FieldsFor(ctx, importResolver{}, obj)
	if err != nil {
		return engine.FieldSpec{}, err
	}
	aliases := maps.Clone(spec.Aliases)
	if aliases == nil {
		aliases = map[string]string{}
	}
	maps.Copy(aliases, extra.Aliases)
	return engine.FieldSpec{
		WriteOnly:  slices.Concat(spec.WriteOnly, extra.WriteOnly),
		CreateOnly: slices.Concat(spec.CreateOnly, extra.CreateOnly),
		Aliases:    aliases,
		Sets:       slices.Concat(spec.Sets, extra.Sets),
		Durations:  slices.Concat(spec.Durations, extra.Durations),
	}, nil
}

// observe reads obj the way the engine does: through the adapter's Observer, or a read of its path.
func observe[T engine.Object](ctx context.Context, a engine.Adapter[T], c openbao.Client, obj T) (engine.Payload, bool, error) {
	if observer, ok := any(a).(engine.Observer[T]); ok {
		return observer.Observe(ctx, c, obj)
	}
	path := a.Path(obj)
	if resolver, ok := any(a).(engine.PathResolver[T]); ok {
		var err error
		if path, err = resolver.ResolvePath(ctx, importResolver{}, c, obj); err != nil {
			return nil, false, err
		}
	}
	observed, err := c.Read(ctx, path)
	if errors.Is(err, openbao.ErrNotFound) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("read %s: %w", path, err)
	}
	return observed, true, nil
}

func diff[T engine.Object](ctx context.Context, a engine.Adapter[T], obj T, observed engine.Payload) ([]string, error) {
	desired, err := a.Desired(ctx, importResolver{}, obj)
	if err != nil {
		return nil, err
	}
	spec, err := fieldSpec(ctx, a, obj)
	if err != nil {
		return nil, err
	}
	return engine.Diff(desired, observed, spec), nil
}

// withAliases returns observed with each aliased read key also present under its write name.
func withAliases(observed engine.Payload, aliases map[string]string) engine.Payload {
	out := maps.Clone(observed)
	for read, write := range aliases {
		if value, ok := out[read]; ok {
			out[write] = value
		}
	}
	return out
}

// specValue returns the addressable Spec struct of obj.
func specValue(obj engine.Object) reflect.Value {
	return reflect.ValueOf(obj).Elem().FieldByName("Spec")
}

func sortResult(r *Result) {
	sort.SliceStable(r.Objects, func(i, j int) bool {
		ki, kj := r.Objects[i].GetObjectKind().GroupVersionKind(), r.Objects[j].GetObjectKind().GroupVersionKind()
		if ki.Group != kj.Group {
			return ki.Group < kj.Group
		}
		if ki.Kind != kj.Kind {
			return ki.Kind < kj.Kind
		}
		return r.Objects[i].GetName() < r.Objects[j].GetName()
	})
	sort.SliceStable(r.Placeholders, func(i, j int) bool {
		a, b := r.Placeholders[i], r.Placeholders[j]
		return a.Object+"\x00"+a.Field < b.Object+"\x00"+b.Field
	})
	slices.Sort(r.Notes)
}
