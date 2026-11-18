package core

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	commonv1alpha1 "github.com/alexandrust88/openbao-config-operator/api/common/v1alpha1"
	corev1alpha1 "github.com/alexandrust88/openbao-config-operator/api/core/v1alpha1"
	"github.com/alexandrust88/openbao-config-operator/internal/connection"
	"github.com/alexandrust88/openbao-config-operator/internal/openbao"
)

const (
	connName          = "bao"
	testAddress       = "https://bao:8200"
	tokenKey          = "token"
	openbaoName       = "openbao"
	errExactlyOneAuth = "set exactly one of token, kubernetes, appRole and jwt"
	alicePath         = "auth/userpass/users/alice"
	keyPassword       = "password"
	policiesA         = `{"token_policies":["a"]}`
	keyTokenPolicies  = "token_policies"
	keyTokenTTL       = "token_ttl"
	teamA             = "team-a"
	tenantLabel       = "openbao.io/tenant"
	testVersion       = "2.7.1"
	litSub            = "sub"
	litRoot           = "root"
	litFilesystem     = "filesystem"
	litAuth           = "auth"
	kindConnection    = "BaoConnection"
)

type lookupSelfClient struct {
	openbao.Client
	readErr error
}

func (f lookupSelfClient) Read(_ context.Context, path string) (map[string]any, error) {
	if path != lookupSelfPath {
		return nil, openbao.ErrNotFound
	}
	return map[string]any{}, f.readErr
}

type recordingPool struct {
	client      openbao.Client
	invalidated []string
}

func (p *recordingPool) Client(context.Context, string, openbao.Config) (openbao.Client, error) {
	return p.client, nil
}

func (p *recordingPool) Invalidate(key string) { p.invalidated = append(p.invalidated, key) }

// testServer is a Ready BaoServer that admits namespaces labelled as tenants and the openbao audience.
func testServer() *corev1alpha1.BaoServer {
	return &corev1alpha1.BaoServer{
		ObjectMeta: metav1.ObjectMeta{Name: openbaoName, Generation: 1},
		Spec: corev1alpha1.BaoServerSpec{
			Address:           testAddress,
			AllowedNamespaces: &metav1.LabelSelector{MatchLabels: map[string]string{tenantLabel: "true"}},
			AllowedAudiences:  []string{openbaoName},
		},
		Status: corev1alpha1.BaoServerStatus{
			Version: testVersion,
			Conditions: []metav1.Condition{{
				Type: commonv1alpha1.ConditionReady, Status: metav1.ConditionTrue, Reason: commonv1alpha1.ReasonAvailable,
				ObservedGeneration: 1, LastTransitionTime: metav1.Now(),
			}},
		},
	}
}

func tenantNamespace(name string) *corev1.Namespace {
	return &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: name, Labels: map[string]string{tenantLabel: "true"}}}
}

func connectionFakeClient(objs ...client.Object) client.WithWatch {
	return fake.NewClientBuilder().
		WithScheme(connectionTestScheme).
		WithObjects(objs...).
		WithStatusSubresource(&corev1alpha1.BaoConnection{}, &corev1alpha1.ClusterBaoConnection{},
			&corev1alpha1.BaoServer{}).
		WithIndex(&corev1alpha1.BaoConnection{}, referenceIndex, connection.References).
		WithIndex(&corev1alpha1.BaoConnection{}, serverIndex, serverName).
		WithIndex(&corev1alpha1.ClusterBaoConnection{}, serverIndex, serverName).
		WithIndex(&corev1alpha1.BaoServer{}, referenceIndex, connection.References).
		WithIndex(&corev1alpha1.BaoEndpoint{}, connectionUseIndex, connectionInUse).
		Build()
}

func newConnectionReconciler(t *testing.T, pool connection.Pool, objs ...client.Object) *connectionReconciler {
	t.Helper()
	c := connectionFakeClient(objs...)
	return &connectionReconciler{
		Client:    c,
		resolver:  &connection.Resolver{Client: c, Pool: pool},
		pool:      pool,
		kind:      commonv1alpha1.ConnectionKindNamespaced,
		newObject: func() connectionObject { return &corev1alpha1.BaoConnection{} },
		newList:   func() client.ObjectList { return &corev1alpha1.BaoConnectionList{} },
		managed:   []func() client.ObjectList{func() client.ObjectList { return &corev1alpha1.BaoEndpointList{} }},
	}
}

func tokenConnection() (*corev1alpha1.BaoConnection, *corev1.Secret) {
	conn := &corev1alpha1.BaoConnection{
		ObjectMeta: metav1.ObjectMeta{Name: connName, Namespace: teamA, UID: "uid-1", Generation: 1},
		Spec: corev1alpha1.BaoConnectionSpec{
			ConnectionSpec: corev1alpha1.ConnectionSpec{ServerRef: corev1alpha1.ServerReference{Name: openbaoName}},
			Auth: corev1alpha1.ConnectionAuth{Token: &corev1alpha1.TokenAuth{
				SecretRef: commonv1alpha1.SecretKeySelector{Name: "bao-token", Key: tokenKey},
			}},
		},
	}
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "bao-token", Namespace: teamA},
		Data:       map[string][]byte{tokenKey: []byte("s.abc")},
	}
	return conn, secret
}

func healthyProbe(context.Context, openbao.Config) (openbao.ServerInfo, error) {
	return openbao.ServerInfo{Version: testVersion, Flavor: "OpenBao", Initialized: true}, nil
}

func reconcileAndGet(t *testing.T, r *connectionReconciler) (ctrl.Result, *corev1alpha1.BaoConnection) {
	t.Helper()
	key := types.NamespacedName{Namespace: teamA, Name: connName}
	res, err := r.Reconcile(context.Background(), ctrl.Request{NamespacedName: key})
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	got := &corev1alpha1.BaoConnection{}
	if err := r.Get(context.Background(), key, got); err != nil {
		t.Fatal(err)
	}
	return res, got
}

func TestReconcileReady(t *testing.T) {
	conn, secret := tokenConnection()
	pool := &recordingPool{client: lookupSelfClient{}}
	r := newConnectionReconciler(t, pool, testServer(), tenantNamespace(teamA), conn, secret)

	res, got := reconcileAndGet(t, r)
	if res.RequeueAfter < probeInterval || res.RequeueAfter > probeInterval*6/5 {
		t.Errorf("RequeueAfter = %v, want a jittered %v", res.RequeueAfter, probeInterval)
	}
	s := got.Status
	if !meta.IsStatusConditionTrue(s.Conditions, commonv1alpha1.ConditionReady) {
		t.Fatalf("not Ready: %+v", s.Conditions)
	}
	if !s.Authenticated || s.Version != testVersion || s.ObservedGeneration != 1 || s.LastProbeTime == nil {
		t.Errorf("unexpected status %+v", s)
	}

	_, again := reconcileAndGet(t, r)
	if again.ResourceVersion != got.ResourceVersion {
		t.Errorf("an unchanged check wrote the status (resourceVersion %s -> %s)", got.ResourceVersion, again.ResourceVersion)
	}
}

func TestReconcileNotReady(t *testing.T) {
	sealed := testServer()
	sealed.Status.Sealed = true
	sealed.Status.Conditions[0].Status = metav1.ConditionFalse
	sealed.Status.Conditions[0].Reason = corev1alpha1.ReasonSealed
	stale := testServer()
	stale.Generation = 2
	cases := []struct {
		name        string
		server      *corev1alpha1.BaoServer
		namespace   *corev1.Namespace
		client      openbao.Client
		noSecret    bool
		wantReason  string
		wantMessage string
	}{
		{name: "server missing", wantReason: corev1alpha1.ReasonServerNotReady, wantMessage: "baoserver not found"},
		{
			name: "server sealed", server: sealed,
			wantReason: corev1alpha1.ReasonServerNotReady, wantMessage: "BaoServer openbao is not ready: Sealed",
		},
		{
			name: "server not probed for its generation", server: stale,
			wantReason: corev1alpha1.ReasonServerNotReady, wantMessage: "is not ready",
		},
		{
			name: "namespace not admitted", server: testServer(),
			namespace:  &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: teamA}},
			wantReason: commonv1alpha1.ReasonConnectionDenied, wantMessage: "namespace team-a is not in allowedNamespaces",
		},
		{
			name: "authentication failed", server: testServer(),
			client:     lookupSelfClient{readErr: errors.New("permission denied")},
			wantReason: corev1alpha1.ReasonAuthenticationFailed,
		},
		{
			name: "base namespace missing", server: testServer(),
			client:     lookupSelfClient{readErr: &openbao.ResponseError{StatusCode: 404, Errors: []string{"namespace not found"}}},
			wantReason: commonv1alpha1.ReasonReferenceNotReady,
		},
		{name: "missing secret", server: testServer(), noSecret: true, wantReason: corev1alpha1.ReasonCredentialsNotFound},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			conn, secret := tokenConnection()
			ns := tc.namespace
			if ns == nil {
				ns = tenantNamespace(teamA)
			}
			objs := []client.Object{conn, ns}
			if tc.server != nil {
				objs = append(objs, tc.server)
			}
			if !tc.noSecret {
				objs = append(objs, secret)
			}
			pool := &recordingPool{client: tc.client}
			r := newConnectionReconciler(t, pool, objs...)

			res, got := reconcileAndGet(t, r)
			if res.RequeueAfter == 0 {
				t.Error("want a requeue")
			}
			if tc.wantReason == commonv1alpha1.ReasonReferenceNotReady && res.RequeueAfter > 5*time.Second {
				t.Errorf("requeue after %v, want a short retry while the namespace is missing", res.RequeueAfter)
			}
			cond := meta.FindStatusCondition(got.Status.Conditions, commonv1alpha1.ConditionReady)
			if cond == nil || cond.Status != metav1.ConditionFalse || cond.Reason != tc.wantReason ||
				!strings.Contains(cond.Message, tc.wantMessage) {
				t.Errorf("Ready = %+v, want False/%s containing %q", cond, tc.wantReason, tc.wantMessage)
			}
			if got.Status.Authenticated {
				t.Error("Authenticated must be false")
			}
			if tc.wantReason == commonv1alpha1.ReasonConnectionDenied && len(pool.invalidated) != 1 {
				t.Errorf("invalidated = %v, want the denied connection's client dropped", pool.invalidated)
			}
		})
	}
}

func TestReconcileCopiesServerState(t *testing.T) {
	server := testServer()
	server.Status.Standby = true
	conn, secret := tokenConnection()
	r := newConnectionReconciler(t, &recordingPool{client: lookupSelfClient{}}, server, tenantNamespace(teamA), conn, secret)
	_, got := reconcileAndGet(t, r)
	if got.Status.Version != testVersion || !got.Status.Standby || got.Status.Sealed {
		t.Errorf("status = %+v, want the server's version and standby", got.Status)
	}
}

func TestReconcileInvalidatesOnDelete(t *testing.T) {
	conn, secret := tokenConnection()
	pool := &recordingPool{client: lookupSelfClient{}}
	r := newConnectionReconciler(t, pool, testServer(), tenantNamespace(teamA), conn, secret)
	reconcileAndGet(t, r)

	current := &corev1alpha1.BaoConnection{}
	if err := r.Get(context.Background(), client.ObjectKeyFromObject(conn), current); err != nil {
		t.Fatal(err)
	}
	current.Spec.Namespace = "tenants/other"
	current.Generation = 2
	if err := r.Update(context.Background(), current); err != nil {
		t.Fatal(err)
	}
	reconcileAndGet(t, r)
	if len(pool.invalidated) != 0 {
		t.Fatalf("invalidated = %v on a spec change; the pool fingerprints the config itself", pool.invalidated)
	}

	if err := r.Delete(context.Background(), current); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Reconcile(context.Background(), ctrl.Request{NamespacedName: client.ObjectKeyFromObject(conn)}); err != nil {
		t.Fatal(err)
	}
	if len(pool.invalidated) != 1 || pool.invalidated[0] != "uid-1" {
		t.Errorf("invalidated = %v, want [uid-1]", pool.invalidated)
	}
}

func TestConnectionEnqueues(t *testing.T) {
	conn, secret := tokenConnection()
	server := testServer()
	ns := tenantNamespace(teamA)
	r := newConnectionReconciler(t, &recordingPool{}, server, ns, conn, secret)
	ctx := context.Background()
	want := client.ObjectKeyFromObject(conn)

	for name, requests := range map[string][]reconcile.Request{
		"secret":    r.referencing("Secret")(ctx, secret),
		"server":    r.usingServer(ctx, server),
		"namespace": r.inNamespace(ctx, ns),
	} {
		if len(requests) != 1 || requests[0].NamespacedName != want {
			t.Errorf("%s: requests = %v, want %v", name, requests, want)
		}
	}
	other := secret.DeepCopy()
	other.Namespace = "team-b"
	otherServer := server.DeepCopy()
	otherServer.Name = "other"
	for name, requests := range map[string][]reconcile.Request{
		"secret in another namespace": r.referencing("Secret")(ctx, other),
		"another server":              r.usingServer(ctx, otherServer),
		"another namespace":           r.inNamespace(ctx, tenantNamespace("team-b")),
	} {
		if len(requests) != 0 {
			t.Errorf("%s enqueued %v", name, requests)
		}
	}
}
