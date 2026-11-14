package access

import (
	"context"
	"maps"
	"path"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	accessv1alpha1 "github.com/alexandrust88/openbao-config-operator/api/access/v1alpha1"
	sysv1alpha1 "github.com/alexandrust88/openbao-config-operator/api/sys/v1alpha1"
	"github.com/alexandrust88/openbao-config-operator/internal/openbao"
)

// fakeStore is an in-memory OpenBao keyed by namespace and path; it logs every write and delete.
type fakeStore struct {
	data    map[string]map[string]any
	log     []string
	onWrite func(key string, data map[string]any) map[string]any
}

type fakeBao struct {
	openbao.Client
	ns    string
	store *fakeStore
}

func newFakeBao() fakeBao {
	return fakeBao{store: &fakeStore{data: map[string]map[string]any{}}}
}

func (f fakeBao) key(p string) string { return path.Join(f.ns, p) }

func (f fakeBao) seed(p string, data map[string]any) { f.store.data[f.key(p)] = data }

func (f fakeBao) Read(_ context.Context, p string) (map[string]any, error) {
	if data, ok := f.store.data[f.key(p)]; ok {
		return maps.Clone(data), nil
	}
	return nil, openbao.ErrNotFound
}

func (f fakeBao) Write(_ context.Context, p string, data map[string]any) (map[string]any, error) {
	key := f.key(p)
	f.store.log = append(f.store.log, "write "+key)
	if f.store.onWrite != nil {
		return f.store.onWrite(key, data), nil
	}
	f.store.data[key] = maps.Clone(data)
	return nil, nil
}

func (f fakeBao) Delete(_ context.Context, p string) error {
	key := f.key(p)
	f.store.log = append(f.store.log, "delete "+key)
	if _, ok := f.store.data[key]; !ok {
		return openbao.ErrNotFound
	}
	delete(f.store.data, key)
	return nil
}

func (f fakeBao) WithNamespace(ns string) openbao.Client {
	return fakeBao{ns: path.Join(f.ns, ns), store: f.store}
}

func (f fakeBao) Namespace() string { return f.ns }

func testScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	scheme := runtime.NewScheme()
	if err := clientgoscheme.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	if err := accessv1alpha1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	if err := sysv1alpha1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	return scheme
}

func fakeClient(t *testing.T, objs ...client.Object) client.Client {
	t.Helper()
	return fake.NewClientBuilder().WithScheme(testScheme(t)).WithObjects(objs...).
		WithStatusSubresource(&accessv1alpha1.AccessLevel{}).Build()
}

func newLevel(name string, spec accessv1alpha1.AccessLevelSpec) *accessv1alpha1.AccessLevel {
	return &accessv1alpha1.AccessLevel{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "team"}, Spec: spec}
}

var (
	readLevel = accessv1alpha1.AccessLevelSpec{Data: []accessv1alpha1.AccessCapability{"read"},
		Metadata: []accessv1alpha1.AccessCapability{"read", "list"}}
	writeLevel = accessv1alpha1.AccessLevelSpec{Data: []accessv1alpha1.AccessCapability{"create", "read", "update"},
		Metadata: []accessv1alpha1.AccessCapability{"read", "list"}}
)
