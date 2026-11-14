package identity

import (
	"context"
	"maps"
	"slices"
	"strings"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	commonv1alpha1 "github.com/alexandrust88/openbao-config-operator/api/common/v1alpha1"
	identityv1alpha1 "github.com/alexandrust88/openbao-config-operator/api/identity/v1alpha1"
	sysv1alpha1 "github.com/alexandrust88/openbao-config-operator/api/sys/v1alpha1"
	"github.com/alexandrust88/openbao-config-operator/internal/engine"
	"github.com/alexandrust88/openbao-config-operator/internal/openbao"
)

// oidcBao is an in-memory openbao.Client keyed by path.
type oidcBao struct {
	namespace string
	data      map[string]map[string]any
	writes    []string
	written   map[string]map[string]any
	deletes   []string
}

func newOIDCBao(data map[string]map[string]any) *oidcBao {
	if data == nil {
		data = map[string]map[string]any{}
	}
	return &oidcBao{data: data}
}

func (b *oidcBao) Read(_ context.Context, path string) (map[string]any, error) {
	if d, ok := b.data[path]; ok {
		return d, nil
	}
	return nil, openbao.ErrNotFound
}

func (b *oidcBao) Write(_ context.Context, path string, data map[string]any) (map[string]any, error) {
	b.writes = append(b.writes, path)
	if b.written == nil {
		b.written = map[string]map[string]any{}
	}
	b.written[path] = data
	return nil, nil
}

func (b *oidcBao) Delete(_ context.Context, path string) error {
	b.deletes = append(b.deletes, path)
	return nil
}

func (b *oidcBao) List(_ context.Context, path string) ([]string, error) {
	var keys []string
	for p := range b.data {
		if rest, ok := strings.CutPrefix(p, path+"/"); ok && !strings.Contains(rest, "/") {
			keys = append(keys, rest)
		}
	}
	if len(keys) == 0 {
		return nil, openbao.ErrNotFound
	}
	slices.Sort(keys)
	return keys, nil
}

func (b *oidcBao) WithNamespace(string) openbao.Client { return b }

func (b *oidcBao) Namespace() string { return b.namespace }

type oidcConns struct{ client openbao.Client }

func (c oidcConns) For(context.Context, string, commonv1alpha1.ConnectionReference) (openbao.Client, error) {
	return c.client, nil
}

// oidcSecrets is an engine.Resolver over fixed Secret values, keyed "<name>/<key>".
type oidcSecrets map[string]string

func (s oidcSecrets) SecretValue(_ context.Context, namespace string, ref commonv1alpha1.SecretKeySelector) ([]byte, error) {
	if v, ok := s[ref.Name+"/"+ref.Key]; ok {
		return []byte(v), nil
	}
	return nil, engine.ErrReferenceNotReady
}

func (oidcSecrets) Object(context.Context, client.ObjectKey, client.Object) error { return nil }

func oidcScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	scheme := runtime.NewScheme()
	for _, add := range []func(*runtime.Scheme) error{clientgoscheme.AddToScheme, identityv1alpha1.AddToScheme, sysv1alpha1.AddToScheme} {
		if err := add(scheme); err != nil {
			t.Fatal(err)
		}
	}
	return scheme
}

func oidcReader(t *testing.T, objs ...client.Object) client.Client {
	t.Helper()
	return fake.NewClientBuilder().WithScheme(oidcScheme(t)).WithObjects(objs...).Build()
}

// oidcReady marks a referent Ready with the given status.id.
func oidcReady[T commonv1alpha1.Managed](obj T, id string) T {
	status := obj.GetResourceStatus()
	status.ID = id
	status.Conditions = []metav1.Condition{{
		Type: commonv1alpha1.ConditionReady, Status: metav1.ConditionTrue, Reason: "Test", LastTransitionTime: metav1.Now(),
	}}
	return obj
}

func oidcMeta(name string) metav1.ObjectMeta {
	return metav1.ObjectMeta{Name: name, Namespace: litTeam}
}

func assertNoDiff(t *testing.T, desired, observed engine.Payload, fields engine.FieldSpec) {
	t.Helper()
	if differing := engine.Diff(desired, observed, fields); len(differing) != 0 {
		t.Errorf("unexpected differing keys %v", differing)
	}
}

func assertDiff(t *testing.T, desired, observed engine.Payload, fields engine.FieldSpec, want ...string) {
	t.Helper()
	if differing := engine.Diff(desired, observed, fields); !slices.Equal(differing, want) {
		t.Errorf("differing keys = %v, want %v", differing, want)
	}
}

// oidcCopy returns a shallow copy of a fixture so that a test can change one key.
func oidcCopy(p map[string]any, overrides map[string]any) map[string]any {
	out := maps.Clone(p)
	maps.Copy(out, overrides)
	return out
}
