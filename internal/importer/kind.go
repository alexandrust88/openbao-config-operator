package importer

import (
	"context"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"unicode"

	"sigs.k8s.io/controller-runtime/pkg/client/apiutil"

	commonv1alpha1 "github.com/alexandrust88/openbao-config-operator/api/common/v1alpha1"
	"github.com/alexandrust88/openbao-config-operator/internal/engine"
)

// runner imports every object of one kind in a scope.
type runner interface {
	run(ctx context.Context, s *scope)
}

// listed is an object found in OpenBao, with its identity set in the spec and the parts of its resource name.
type listed[T engine.Object] struct {
	obj   T
	parts []string
}

// kind imports one managed kind through its adapter.
type kind[T engine.Object] struct {
	adapter engine.Adapter[T]
	newObj  func() T
	// list finds the objects of the kind and sets the spec fields that identify each one.
	list func(ctx context.Context, s *scope) ([]listed[T], error)
	// identity names the spec fields list sets; they are never taken from the read.
	identity []string
	// secrets returns write-only keys to reference from a Secret although their spec field is optional.
	secrets func(observed engine.Payload) []string
	// fill maps what the generic mapping cannot, after it ran.
	fill func(ctx context.Context, s *scope, obj T, observed engine.Payload) error
	// prefill fills the spec from a plain read of Path before the Observer runs, for Observers that compare
	// create-only fields with the spec.
	prefill bool
	// rootOnly kinds exist only in the root namespace.
	rootOnly bool
	// noDrift skips reading and comparing for kinds whose desired state lives in Secrets, such as KV data: only
	// list and fill run.
	noDrift bool
}

func (k kind[T]) run(ctx context.Context, s *scope) {
	gvk, err := apiutil.GVKForObject(k.newObj(), s.scheme)
	if err != nil {
		s.note("%T: %v", k.newObj(), err)
		return
	}
	if k.rootOnly && !s.root() {
		return
	}
	items, err := k.list(ctx, s)
	if err != nil {
		s.note("%s in OpenBao namespace %q not imported: %v", gvk.Kind, s.c.Namespace(), err)
		return
	}
	for _, item := range items {
		s.prepare(item.obj, gvk, item.parts...)
		if err := k.importOne(ctx, s, item.obj); err != nil {
			s.note("%s not imported: %v", s.label(item.obj), err)
		}
	}
}

func (k kind[T]) importOne(ctx context.Context, s *scope, obj T) error {
	if k.noDrift {
		if err := k.fill(ctx, s, obj, nil); err != nil {
			return err
		}
		s.redactURLs(obj)
		s.result.Objects = append(s.result.Objects, obj)
		return nil
	}
	spec := specValue(obj)
	if k.prefill {
		raw, err := s.c.Read(ctx, k.adapter.Path(obj))
		if err != nil {
			return fmt.Errorf("read %s: %w", k.adapter.Path(obj), err)
		}
		fill(spec, raw, k.identity)
	}
	observed, found, err := observe(ctx, k.adapter, s.c, obj)
	if err != nil || !found {
		return err
	}
	fs, err := fieldSpec(ctx, k.adapter, obj)
	if err != nil {
		return err
	}
	set, unmapped := fill(spec, withAliases(observed, fs.Aliases), k.identity)
	if k.fill != nil {
		if err := k.fill(ctx, s, obj, observed); err != nil {
			return err
		}
	}
	s.placeholders(obj, spec, observed, fs.WriteOnly, k.secrets)
	for _, path := range unmapped {
		s.note("%s: spec.%s not imported, its OpenBao value has an unexpected type", s.label(obj), path)
	}
	if observed, found, err = observe(ctx, k.adapter, s.c, obj); err != nil || !found {
		return err
	}
	if err := k.minimize(ctx, obj, set, observed); err != nil {
		return err
	}
	s.redactURLs(obj)
	dropEmptyStructs(spec)
	differing, err := diff(ctx, k.adapter, obj, observed)
	if err != nil {
		return err
	}
	if len(differing) > 0 {
		s.note("%s: Observe will report drift on %s", s.label(obj), strings.Join(differing, ", "))
	}
	s.result.Objects = append(s.result.Objects, obj)
	return nil
}

// minimize clears every filled field that holds an "unset" value (false, 0, "") when clearing it adds no drift,
// so the resource lists only what differs from the defaults the adapter sends.
func (k kind[T]) minimize(ctx context.Context, obj T, set []setField, observed engine.Payload) error {
	base, err := diff(ctx, k.adapter, obj, observed)
	if err != nil {
		return err
	}
	for _, f := range set {
		if !isZero(f.value) {
			continue
		}
		saved := reflect.New(f.value.Type()).Elem()
		saved.Set(f.value)
		f.value.Set(reflect.Zero(f.value.Type()))
		got, err := diff(ctx, k.adapter, obj, observed)
		if err != nil || !subset(got, base) {
			f.value.Set(saved)
			continue
		}
		base = got
	}
	return nil
}

func subset(a, b []string) bool {
	for _, key := range a {
		if !slices.Contains(b, key) {
			return false
		}
	}
	return true
}

// placeholders points every Secret selector of spec that OpenBao has a value for at a Secret named after the
// resource, keyed by the OpenBao field name, and records it. A selector is set when the field is required, the
// read reports "<key>_set", or the kind's secrets func names the key.
func (s *scope) placeholders(obj engine.Object, spec reflect.Value, observed engine.Payload, writeOnly []string,
	extra func(engine.Payload) []string) {
	var wanted []string
	if extra != nil {
		wanted = extra(observed)
	}
	for _, f := range fields(spec) {
		if baseType(f.value.Type()) != secretRefType || !isZero(f.value) && f.value.Kind() == reflect.Pointer {
			continue
		}
		key := writeOnlyKey(f.name, writeOnly)
		reported, _ := observed[key+"_set"].(bool)
		if f.optional && !reported && !slices.Contains(wanted, key) {
			continue
		}
		s.setPlaceholder(obj, f.value, "spec."+f.name, key)
	}
}

// setPlaceholder sets v, a SecretKeySelector or a pointer to one, to the resource's placeholder Secret.
func (s *scope) setPlaceholder(obj engine.Object, v reflect.Value, field, key string) {
	sel := commonv1alpha1.SecretKeySelector{Name: obj.GetName(), Key: key}
	if v.Kind() == reflect.Pointer {
		v.Set(reflect.ValueOf(&sel))
	} else {
		v.Set(reflect.ValueOf(sel))
	}
	s.result.Placeholders = append(s.result.Placeholders,
		Placeholder{Object: s.label(obj), Field: field, Secret: sel.Name, Key: sel.Key})
}

// writeOnlyKey returns the OpenBao key of a "<field>SecretRef" spec field.
func writeOnlyKey(field string, writeOnly []string) string {
	base := strings.TrimSuffix(field, "SecretRef")
	for _, key := range writeOnly {
		if norm(key) == norm(base) {
			return key
		}
	}
	return snake(base)
}

// snake turns a camelCase name into snake_case: "tokenReviewerJWT" becomes "token_reviewer_jwt".
func snake(name string) string {
	runes := []rune(name)
	var b strings.Builder
	for i, r := range runes {
		if unicode.IsUpper(r) && i > 0 {
			prevLower := !unicode.IsUpper(runes[i-1])
			nextLower := i+1 < len(runes) && unicode.IsLower(runes[i+1])
			if prevLower || nextLower {
				b.WriteByte('_')
			}
		}
		b.WriteRune(unicode.ToLower(r))
	}
	return b.String()
}
