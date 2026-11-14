package refs

import (
	"context"
	"errors"
	"slices"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	commonv1alpha1 "github.com/alexandrust88/openbao-config-operator/api/common/v1alpha1"
	sysv1alpha1 "github.com/alexandrust88/openbao-config-operator/api/sys/v1alpha1"
	"github.com/alexandrust88/openbao-config-operator/internal/engine"
	"github.com/alexandrust88/openbao-config-operator/internal/openbao"
)

// policyTarget stands in for any managed referent kind.
var policyTarget = Target{
	GVK: sysv1alpha1.GroupVersion.WithKind("Policy"),
	Lookup: func(_ context.Context, _ openbao.Client, name string) (string, error) {
		return "looked-up-" + name, nil
	},
}

type readClient struct {
	openbao.Client
	data map[string]map[string]any
	err  error
	ns   string
}

func (c readClient) Namespace() string { return c.ns }

func (c readClient) Read(_ context.Context, path string) (map[string]any, error) {
	if c.err != nil {
		return nil, c.err
	}
	if d, ok := c.data[path]; ok {
		return d, nil
	}
	return nil, openbao.ErrNotFound
}

func referent(name, id string, ready bool, labels map[string]string) *sysv1alpha1.Policy {
	p := &sysv1alpha1.Policy{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: litTeam, Labels: labels}}
	p.Status.ID = id
	status := metav1.ConditionFalse
	if ready {
		status = metav1.ConditionTrue
	}
	p.Status.Conditions = []metav1.Condition{{
		Type: commonv1alpha1.ConditionReady, Status: status, Reason: "Test", LastTransitionTime: metav1.Now(),
	}}
	return p
}

func newResolver(t *testing.T, objs ...*sysv1alpha1.Policy) Resolver {
	t.Helper()
	scheme := runtime.NewScheme()
	if err := sysv1alpha1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	b := fake.NewClientBuilder().WithScheme(scheme)
	for _, o := range objs {
		b = b.WithObjects(o)
	}
	return WithClient(b.Build(), readClient{}, litTeam)
}

func TestOne(t *testing.T) {
	r := newResolver(t,
		referent("a", "id-a", true, map[string]string{litApp: "x", litTier: "1"}),
		referent("b", litIdB, true, map[string]string{litApp: "x", litTier: "2"}),
		referent(litPending, "", false, map[string]string{litApp: litPending}),
		referent("no-id", "", true, nil),
	)
	tests := []struct {
		name     string
		ref      Ref
		want     string
		notReady bool
		wantErr  bool
	}{
		{name: "literal", ref: Ref{Literal: "abc"}, want: "abc"},
		{name: "openbao name", ref: Ref{Name: "reader"}, want: "looked-up-reader"},
		{name: "object", ref: Ref{ObjectName: "a"}, want: "id-a"},
		{name: "object missing", ref: Ref{ObjectName: "gone"}, notReady: true},
		{name: "object not ready", ref: Ref{ObjectName: litPending}, notReady: true},
		{name: "object ready without id", ref: Ref{ObjectName: "no-id"}, notReady: true},
		{name: "selector one match", ref: Ref{MatchLabels: map[string]string{litTier: "2"}}, want: litIdB},
		{name: "selector two matches", ref: Ref{MatchLabels: map[string]string{litApp: "x"}}, notReady: true},
		{name: "selector no match", ref: Ref{MatchLabels: map[string]string{litApp: "none"}}, notReady: true},
		{name: "selector match not ready", ref: Ref{MatchLabels: map[string]string{litApp: litPending}}, notReady: true},
		{name: "nothing set", ref: Ref{}, wantErr: true},
		{name: "two forms set", ref: Ref{Literal: "x", ObjectName: "a"}, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := r.One(context.Background(), policyTarget, tt.ref)
			switch {
			case tt.notReady:
				if !errors.Is(err, engine.ErrReferenceNotReady) {
					t.Fatalf("err = %v, want ErrReferenceNotReady", err)
				}
			case tt.wantErr:
				if err == nil || errors.Is(err, engine.ErrReferenceNotReady) {
					t.Fatalf("err = %v, want a spec error", err)
				}
			case err != nil:
				t.Fatal(err)
			case got != tt.want:
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestAll(t *testing.T) {
	r := newResolver(t,
		referent("b", litIdB, true, map[string]string{litApp: "x"}),
		referent("a", "id-a", true, map[string]string{litApp: "x"}),
		referent("dup", "id-a", true, map[string]string{litApp: "x"}),
		referent(litPending, "", false, map[string]string{litApp: "y"}),
	)
	got, err := r.All(context.Background(), policyTarget, Ref{MatchLabels: map[string]string{litApp: "x"}})
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"id-a", litIdB}; !slices.Equal(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
	got, err = r.All(context.Background(), policyTarget, Ref{MatchLabels: map[string]string{litApp: "none"}})
	if err != nil || len(got) != 0 {
		t.Errorf("no match: got %v, %v", got, err)
	}
	if _, err := r.All(context.Background(), policyTarget, Ref{MatchLabels: map[string]string{litApp: "y"}}); !errors.Is(err, engine.ErrReferenceNotReady) {
		t.Errorf("not ready match: err = %v", err)
	}
	got, err = r.All(context.Background(), policyTarget, Ref{ObjectName: "b"})
	if err != nil || !slices.Equal(got, []string{litIdB}) {
		t.Errorf("object: got %v, %v", got, err)
	}
}

func TestIsZero(t *testing.T) {
	if !(Ref{MatchLabels: map[string]string{}}).IsZero() || (Ref{ObjectName: "x"}).IsZero() {
		t.Error("IsZero")
	}
}

func TestUnregisteredTarget(t *testing.T) {
	r := newResolver(t)
	if _, err := r.One(context.Background(), Entity, Ref{ObjectName: "e"}); err == nil || errors.Is(err, engine.ErrReferenceNotReady) {
		t.Errorf("err = %v, want a registration error", err)
	}
}

type fakeConnections struct {
	client openbao.Client
	err    error
}

func (f fakeConnections) For(context.Context, string, commonv1alpha1.ConnectionReference) (openbao.Client, error) {
	return f.client, f.err
}

type nsClient struct {
	openbao.Client
	ns string
}

func (c nsClient) WithNamespace(ns string) openbao.Client { return nsClient{ns: c.ns + "/" + ns} }
func (c nsClient) Namespace() string                      { return c.ns }

func TestForScopesTheClient(t *testing.T) {
	p := referent("p", "", false, nil)
	p.Spec.ConnectionRef = commonv1alpha1.ConnectionReference{Name: "bao"}
	p.Spec.Namespace = "child"
	r := For(nil, fakeConnections{client: nsClient{ns: "tenant"}}, p)
	c, err := r.OpenBao(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if c.Namespace() != "tenant/child" {
		t.Errorf("namespace = %q", c.Namespace())
	}
	if r.Namespace != litTeam {
		t.Errorf("kubernetes namespace = %q", r.Namespace)
	}

	failing := For(nil, fakeConnections{err: engine.ErrConnectionNotReady}, p)
	if _, err := failing.One(context.Background(), policyTarget, Ref{Name: "x"}); !errors.Is(err, engine.ErrConnectionNotReady) {
		t.Errorf("err = %v", err)
	}
}

func TestLookups(t *testing.T) {
	c := readClient{data: map[string]map[string]any{
		"sys/auth": {
			"token/":         map[string]any{litAccessor: "auth_token_1", "type": "token"},
			"team/userpass/": map[string]any{litAccessor: "auth_userpass_2", "type": "userpass"},
		},
		"identity/entity/name/alice": {"id": "entity-1", "name": "alice"},
		"identity/group/name/devs":   {"id": "group-1", "name": "devs"},
	}}
	ctx := context.Background()
	for _, path := range []string{"team/userpass", "/team/userpass/"} {
		if got, err := AuthMountAccessor(ctx, c, path); err != nil || got != "auth_userpass_2" {
			t.Errorf("mount %q: got %q, %v", path, got, err)
		}
	}
	if _, err := AuthMountAccessor(ctx, c, litTeam); !errors.Is(err, engine.ErrReferenceNotReady) {
		t.Errorf("missing mount: err = %v", err)
	}
	if got, err := EntityID(ctx, c, "alice"); err != nil || got != "entity-1" {
		t.Errorf("entity: got %q, %v", got, err)
	}
	if got, err := GroupID(ctx, c, "devs"); err != nil || got != "group-1" {
		t.Errorf("group: got %q, %v", got, err)
	}
	if _, err := EntityID(ctx, c, "bob"); !errors.Is(err, engine.ErrReferenceNotReady) {
		t.Errorf("missing entity: err = %v", err)
	}
	denied := readClient{err: &openbao.ResponseError{StatusCode: 403}}
	if _, err := GroupID(ctx, denied, "devs"); err == nil || errors.Is(err, engine.ErrReferenceNotReady) {
		t.Errorf("denied: err = %v, want a real error", err)
	}
	if _, err := AuthMountAccessor(ctx, denied, "x"); err == nil || errors.Is(err, engine.ErrReferenceNotReady) {
		t.Errorf("denied mount: err = %v, want a real error", err)
	}
}

func mountReferent(name, namespace, path string) *sysv1alpha1.AuthMount {
	m := &sysv1alpha1.AuthMount{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: litTeam}}
	m.Status.ID = "acc-" + name
	m.Status.Namespace = namespace
	m.Status.Path = path
	m.Status.Conditions = []metav1.Condition{{
		Type: commonv1alpha1.ConditionReady, Status: metav1.ConditionTrue, Reason: "Test", LastTransitionTime: metav1.Now(),
	}}
	return m
}

func TestReferentMustShareOpenBaoNamespace(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := sysv1alpha1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	k8s := fake.NewClientBuilder().WithScheme(scheme).WithObjects(
		mountReferent("same", "tenants/a", "tenants/a/sys/auth/kubernetes"),
		mountReferent("other", "", "sys/auth/kubernetes"),
	).Build()
	r := WithClient(k8s, readClient{ns: "tenants/a/"}, litTeam)

	if got, err := r.One(context.Background(), AuthMount, Ref{ObjectName: "same"}); err != nil || got != "acc-same" {
		t.Fatalf("same namespace: got %q, err %v", got, err)
	}
	if _, err := r.One(context.Background(), AuthMount, Ref{ObjectName: "other"}); err == nil {
		t.Fatal("accepted the accessor of a mount in another OpenBao namespace")
	}
}

func TestAuthMountPath(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := sysv1alpha1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	labelled := mountReferent(litK8s, "tenants/a", "tenants/a/sys/auth/clusters/east")
	labelled.Labels = map[string]string{litCluster: "east"}
	k8s := fake.NewClientBuilder().WithScheme(scheme).WithObjects(labelled).Build()
	bao := readClient{ns: "tenants/a", data: map[string]map[string]any{
		"sys/auth": {"clusters/east/": map[string]any{litAccessor: "auth_kubernetes_1"}},
	}}
	r := WithClient(k8s, bao, litTeam)
	tests := []struct {
		name     string
		ref      Ref
		want     string
		notReady bool
	}{
		{name: "mountPath", ref: Ref{Name: "/clusters/east/"}, want: litClustersEast},
		{name: "mountRef", ref: Ref{ObjectName: litK8s}, want: litClustersEast},
		{name: "mountSelector", ref: Ref{MatchLabels: map[string]string{litCluster: "east"}}, want: litClustersEast},
		{name: litAccessor, ref: Ref{Literal: "auth_kubernetes_1"}, want: litClustersEast},
		{name: "unknown accessor", ref: Ref{Literal: "auth_x"}, notReady: true},
		{name: "missing mountRef", ref: Ref{ObjectName: "absent"}, notReady: true},
		{name: "empty selector match", ref: Ref{MatchLabels: map[string]string{litCluster: "west"}}, notReady: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := r.AuthMountPath(context.Background(), tt.ref)
			if tt.notReady {
				if !errors.Is(err, engine.ErrReferenceNotReady) {
					t.Fatalf("err = %v, want ErrReferenceNotReady", err)
				}
				return
			}
			if err != nil || got != tt.want {
				t.Fatalf("got %q, err %v; want %q", got, err, tt.want)
			}
		})
	}
}

func TestRefWatch(t *testing.T) {
	tests := []struct {
		ref  Ref
		want engine.ObjectReference
		ok   bool
	}{
		{Ref{ObjectName: litK8s}, engine.ObjectReference{Kind: "AuthMount", Name: litK8s}, true},
		{Ref{MatchLabels: map[string]string{"a": "b"}}, engine.ObjectReference{Kind: "AuthMount"}, true},
		{Ref{Name: "kubernetes"}, engine.ObjectReference{}, false},
		{Ref{Literal: "auth_x"}, engine.ObjectReference{}, false},
	}
	for _, tt := range tests {
		if got, ok := tt.ref.Watch(AuthMount); got != tt.want || ok != tt.ok {
			t.Errorf("Watch(%+v) = %+v, %v", tt.ref, got, ok)
		}
	}
}

// clusterClient is a readClient on a server that reports a cluster id.
type clusterClient struct {
	readClient
	id string
}

func (c clusterClient) ClusterID(context.Context) (string, error) { return c.id, nil }

func TestReferentMustMatchServerAndGeneration(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := sysv1alpha1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	same := mountReferent("same", "", "sys/auth/kubernetes")
	same.Status.ServerID = "cluster-a"
	other := mountReferent("other", "", "sys/auth/kubernetes")
	other.Status.ServerID = "cluster-b"
	stale := mountReferent("stale", "", "sys/auth/kubernetes")
	stale.Status.ServerID = "cluster-a"
	stale.Generation = 2
	stale.Status.Conditions[0].ObservedGeneration = 1
	k8s := fake.NewClientBuilder().WithScheme(scheme).WithObjects(same, other, stale).Build()
	r := WithClient(k8s, clusterClient{id: "cluster-a"}, litTeam)

	if got, err := r.AuthMountPath(context.Background(), Ref{ObjectName: "same"}); err != nil || got != litK8sMount {
		t.Fatalf("same server: got %q, err %v", got, err)
	}
	for _, name := range []string{"other", "stale"} {
		if _, err := r.AuthMountPath(context.Background(), Ref{ObjectName: name}); !errors.Is(err, engine.ErrReferenceNotReady) {
			t.Errorf("%s: err = %v, want ErrReferenceNotReady", name, err)
		}
	}
}
