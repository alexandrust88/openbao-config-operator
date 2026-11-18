package auth

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"strings"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"

	authv1alpha1 "github.com/alexandrust88/openbao-config-operator/api/auth/v1alpha1"
	commonv1alpha1 "github.com/alexandrust88/openbao-config-operator/api/common/v1alpha1"
	"github.com/alexandrust88/openbao-config-operator/internal/engine"
	"github.com/alexandrust88/openbao-config-operator/internal/openbao"
)

type credWrite struct {
	path string
	data map[string]any
}

// credBao is an in-memory openbao.Client: reads come from reads, writes are recorded and answered by write.
type credBao struct {
	openbao.Client
	reads  map[string]map[string]any
	write  func(path string, data map[string]any) (map[string]any, error)
	writes []credWrite
}

func (c *credBao) Read(_ context.Context, path string) (map[string]any, error) {
	if d, ok := c.reads[path]; ok {
		return maps.Clone(d), nil
	}
	return nil, openbao.ErrNotFound
}

func (c *credBao) Write(_ context.Context, path string, data map[string]any) (map[string]any, error) {
	c.writes = append(c.writes, credWrite{path: path, data: data})
	if c.write == nil {
		return nil, nil
	}
	return c.write(path, data)
}

func (c *credBao) wrote(path string) []map[string]any {
	var out []map[string]any
	for _, w := range c.writes {
		if w.path == path {
			out = append(out, w.data)
		}
	}
	return out
}

// credResolver serves Secrets and AppRoles from memory, with the default Resolver's not-found errors.
type credResolver struct {
	secrets  map[string]map[string][]byte
	appRoles map[string]*authv1alpha1.AppRole
}

func (r credResolver) SecretValue(_ context.Context, namespace string, ref commonv1alpha1.SecretKeySelector) ([]byte, error) {
	data, ok := r.secrets[ref.Name]
	if !ok {
		return nil, fmt.Errorf("secret %s/%s not found: %w", namespace, ref.Name, engine.ErrReferenceNotReady)
	}
	value, ok := data[ref.Key]
	if !ok {
		return nil, fmt.Errorf("secret %s/%s has no key %q: %w", namespace, ref.Name, ref.Key, engine.ErrReferenceNotReady)
	}
	return value, nil
}

func (r credResolver) Object(_ context.Context, key client.ObjectKey, obj client.Object) error {
	switch o := obj.(type) {
	case *corev1.Secret:
		if data, ok := r.secrets[key.Name]; ok {
			*o = corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: key.Name, Namespace: key.Namespace}, Data: data}
			return nil
		}
		return fmt.Errorf("%w: %w", engine.ErrReferenceNotReady,
			apierrors.NewNotFound(schema.GroupResource{Resource: "secrets"}, key.Name))
	case *authv1alpha1.AppRole:
		if role, ok := r.appRoles[key.Name]; ok {
			*o = *role.DeepCopy()
			return nil
		}
		return fmt.Errorf("%w: %w", engine.ErrReferenceNotReady,
			apierrors.NewNotFound(schema.GroupResource{Group: "auth.obco.dev", Resource: "approles"}, key.Name))
	}
	return fmt.Errorf("unexpected object %T", obj)
}

func credDuration(d time.Duration) *metav1.Duration { return &metav1.Duration{Duration: d} }

// credJSON decodes a captured `bao read -format=json ... | jq .data` object with numbers as json.Number, as the
// OpenBao client returns them.
func credJSON(t *testing.T, text string) map[string]any {
	t.Helper()
	var out map[string]any
	dec := json.NewDecoder(strings.NewReader(text))
	dec.UseNumber()
	if err := dec.Decode(&out); err != nil {
		t.Fatalf("decode fixture: %v", err)
	}
	return out
}

func credConnection() commonv1alpha1.ResourceSpec {
	return commonv1alpha1.ResourceSpec{ConnectionRef: commonv1alpha1.ConnectionReference{Name: cloudLitOpenbao}}
}

func credExpectDiff(t *testing.T, desired, observed engine.Payload, fields engine.FieldSpec, want ...string) {
	t.Helper()
	got := engine.Diff(desired, observed, fields)
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("Diff = %v, want %v", got, want)
	}
}
