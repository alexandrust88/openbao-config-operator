package connection

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	commonv1alpha1 "github.com/alexandrust88/openbao-config-operator/api/common/v1alpha1"
	corev1alpha1 "github.com/alexandrust88/openbao-config-operator/api/core/v1alpha1"
	"github.com/alexandrust88/openbao-config-operator/internal/engine"
	"github.com/alexandrust88/openbao-config-operator/internal/openbao"
)

const (
	connName        = "bao"
	clusterConnName = "platform"
	serverName      = "openbao"
	testAddress     = "https://bao:8200"
	testAudience    = "openbao"
	tenantLabel     = "openbao.io/tenant"
	tokenKey        = "token"
	kubernetesMount = "kubernetes"
	appRoleName     = "approle"
	teamA           = "team-a"
)

type fakePool struct {
	key string
	cfg openbao.Config
	err error
}

func (p *fakePool) Client(_ context.Context, key string, cfg openbao.Config) (openbao.Client, error) {
	p.key, p.cfg = key, cfg
	return nil, p.err
}

func (p *fakePool) Invalidate(string) {}

func readyStatus(generation int64) corev1alpha1.ConnectionStatus {
	return corev1alpha1.ConnectionStatus{Conditions: []metav1.Condition{{
		Type:               commonv1alpha1.ConditionReady,
		Status:             metav1.ConditionTrue,
		Reason:             commonv1alpha1.ReasonAvailable,
		ObservedGeneration: generation,
		LastTransitionTime: metav1.Now(),
	}}}
}

func namespacedConn(auth corev1alpha1.ConnectionAuth) *corev1alpha1.OpenBaoConnection {
	return &corev1alpha1.OpenBaoConnection{
		ObjectMeta: metav1.ObjectMeta{Name: connName, Namespace: teamA, UID: "uid-1", Generation: 3},
		Spec: corev1alpha1.OpenBaoConnectionSpec{
			ConnectionSpec: corev1alpha1.ConnectionSpec{
				ServerRef: corev1alpha1.ServerReference{Name: serverName},
				Namespace: litTenantPath,
			},
			Auth: auth,
		},
		Status: readyStatus(3),
	}
}

func clusterConn(auth corev1alpha1.ClusterConnectionAuth, allowed *metav1.LabelSelector) *corev1alpha1.ClusterOpenBaoConnection {
	return &corev1alpha1.ClusterOpenBaoConnection{
		ObjectMeta: metav1.ObjectMeta{Name: clusterConnName, UID: "uid-2", Generation: 1},
		Spec: corev1alpha1.ClusterOpenBaoConnectionSpec{
			ConnectionSpec:    corev1alpha1.ConnectionSpec{ServerRef: corev1alpha1.ServerReference{Name: serverName}},
			Auth:              auth,
			AllowedNamespaces: allowed,
		},
		Status: readyStatus(1),
	}
}

// tenantServer admits namespaces labelled as tenants and the openbao audience.
func tenantServer() *corev1alpha1.OpenBaoServer {
	return &corev1alpha1.OpenBaoServer{
		ObjectMeta: metav1.ObjectMeta{Name: serverName, Generation: 5},
		Spec: corev1alpha1.OpenBaoServerSpec{
			Address:           testAddress,
			AllowedNamespaces: &metav1.LabelSelector{MatchLabels: map[string]string{tenantLabel: litTrue}},
			AllowedAudiences:  []string{testAudience},
		},
	}
}

func tenantNamespace() *corev1.Namespace {
	return namespace(teamA, map[string]string{tenantLabel: litTrue})
}

// innerAuth returns the credential a cachedAuth wraps.
func innerAuth(t *testing.T, cfg openbao.Config) openbao.Auth {
	t.Helper()
	wrapped, ok := cfg.Auth.(cachedAuth)
	if !ok {
		t.Fatalf("auth is %T, want cachedAuth", cfg.Auth)
	}
	return wrapped.Auth
}

func namespace(name string, labels map[string]string) *corev1.Namespace {
	return &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: name, Labels: labels}}
}

func secret(ns, name string, data map[string]string) *corev1.Secret {
	s := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: name}, Data: map[string][]byte{}}
	for k, v := range data {
		s.Data[k] = []byte(v)
	}
	return s
}

func newResolver(t *testing.T, objs ...client.Object) (*Resolver, *fakePool) {
	t.Helper()
	r, pool, _ := newCountingResolver(t, objs...)
	return r, pool
}

// newCountingResolver also returns a counter of the TokenRequests sent through the resolver's client.
func newCountingResolver(t *testing.T, objs ...client.Object) (*Resolver, *fakePool, *int) {
	t.Helper()
	scheme := runtime.NewScheme()
	if err := clientgoscheme.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	if err := corev1alpha1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	pool := &fakePool{}
	objs = append(objs, tenantServer(), tenantNamespace())
	tokenRequests := 0
	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(dedupe(objs)...).
		WithInterceptorFuncs(interceptor.Funcs{SubResourceCreate: func(ctx context.Context, c client.Client,
			subResource string, obj, sub client.Object, opts ...client.SubResourceCreateOption) error {
			if subResource == "token" {
				tokenRequests++
			}
			return c.SubResource(subResource).Create(ctx, obj, sub, opts...)
		}}).
		Build()
	return &Resolver{Client: c, Pool: pool}, pool, &tokenRequests
}

// dedupe keeps the first object per kind and key, so a test can override the default server and namespace.
func dedupe(objs []client.Object) []client.Object {
	seen := map[string]bool{}
	var out []client.Object
	for _, o := range objs {
		id := fmt.Sprintf("%T/%s/%s", o, o.GetNamespace(), o.GetName())
		if !seen[id] {
			seen[id] = true
			out = append(out, o)
		}
	}
	return out
}

var tokenAuth = corev1alpha1.ConnectionAuth{
	Token: &corev1alpha1.TokenAuth{SecretRef: commonv1alpha1.SecretKeySelector{Name: "bao-token", Key: tokenKey}},
}

func TestForNamespacedToken(t *testing.T) {
	r, pool := newResolver(t, namespacedConn(tokenAuth), secret(teamA, "bao-token", map[string]string{tokenKey: "s.abc\n"}))
	ctx := context.Background()

	if _, err := r.For(ctx, teamA, commonv1alpha1.ConnectionReference{Name: connName}); err != nil {
		t.Fatalf("For: %v", err)
	}
	if pool.key != "uid-1" {
		t.Errorf("pool key = %q, want uid-1", pool.key)
	}
	if pool.cfg.Address != testAddress || pool.cfg.Namespace != litTenantPath {
		t.Errorf("unexpected config %+v", pool.cfg)
	}
	auth, ok := innerAuth(t, pool.cfg).(openbao.TokenAuth)
	if !ok {
		t.Fatalf("auth is %T, want openbao.TokenAuth", innerAuth(t, pool.cfg))
	}
	token, err := auth.Token(ctx)
	if err != nil || token != litToken {
		t.Errorf("Token() = %q, %v", token, err)
	}
	if strings.Contains(auth.CredentialKey, litToken) || !strings.HasPrefix(auth.CredentialKey, "Secret/team-a/bao-token/token@") {
		t.Errorf("credential key %q must name the secret version, not its value", auth.CredentialKey)
	}
}

func TestForNotReady(t *testing.T) {
	stale := namespacedConn(tokenAuth)
	stale.Generation = 4
	notReady := namespacedConn(tokenAuth)
	notReady.Name = "down"
	notReady.Status = corev1alpha1.ConnectionStatus{}
	r, _ := newResolver(t, stale, notReady)

	for _, name := range []string{connName, "down", "missing"} {
		_, err := r.For(context.Background(), teamA, commonv1alpha1.ConnectionReference{Name: name})
		if !errors.Is(err, engine.ErrConnectionNotReady) {
			t.Errorf("%s: err = %v, want ErrConnectionNotReady", name, err)
		}
	}
}

func TestForNamespacedIsolation(t *testing.T) {
	r, _ := newResolver(t, namespacedConn(tokenAuth))
	_, err := r.For(context.Background(), "team-b", commonv1alpha1.ConnectionReference{Name: connName})
	if !errors.Is(err, engine.ErrConnectionNotReady) {
		t.Errorf("err = %v, want ErrConnectionNotReady for a connection in another namespace", err)
	}
}

func TestForClusterAllowedNamespaces(t *testing.T) {
	auth := corev1alpha1.ClusterConnectionAuth{Token: &corev1alpha1.ClusterTokenAuth{
		SecretRef: &corev1alpha1.NamespacedSecretKeySelector{Namespace: litOpenBaoNS, Name: "root", Key: tokenKey},
	}}
	selector := &metav1.LabelSelector{MatchLabels: map[string]string{"openbao.io/tenant": litTrue}}
	r, pool := newResolver(t,
		clusterConn(auth, selector),
		namespace(teamA, map[string]string{"openbao.io/tenant": litTrue}),
		namespace("team-b", nil),
		secret(litOpenBaoNS, "root", map[string]string{tokenKey: "root"}),
	)
	ref := commonv1alpha1.ConnectionReference{Kind: commonv1alpha1.ConnectionKindCluster, Name: clusterConnName}

	_, err := r.For(context.Background(), "team-b", ref)
	if !errors.Is(err, engine.ErrConnectionDenied) {
		t.Errorf("team-b: err = %v, want ErrConnectionDenied", err)
	}
	if _, err := r.For(context.Background(), teamA, ref); err != nil {
		t.Errorf("team-a: %v", err)
	}
	if pool.key != "uid-2" {
		t.Errorf("pool key = %q", pool.key)
	}
}

// TestForClusterHidesConfigDetail keeps platform detail of a ClusterOpenBaoConnection (Secret names, login errors)
// out of the conditions of the resources that use it, while the connection controller's ConfigFor keeps it.
func TestForClusterHidesConfigDetail(t *testing.T) {
	auth := corev1alpha1.ClusterConnectionAuth{Token: &corev1alpha1.ClusterTokenAuth{
		SecretRef: &corev1alpha1.NamespacedSecretKeySelector{Namespace: "platform-secrets", Name: "bao-root", Key: tokenKey},
	}}
	conn := clusterConn(auth, &metav1.LabelSelector{})
	r, _ := newResolver(t, conn, namespace(teamA, nil))
	ref := commonv1alpha1.ConnectionReference{Kind: commonv1alpha1.ConnectionKindCluster, Name: clusterConnName}
	want := "ClusterOpenBaoConnection " + clusterConnName + " is not ready (see its status)"

	_, err := r.For(context.Background(), teamA, ref)
	if !errors.Is(err, ErrReferenceNotFound) || err.Error() != want {
		t.Errorf("missing Secret: err = %v, want ErrReferenceNotFound reading %q", err, want)
	}
	if _, err := r.Config(context.Background(), conn); err == nil || !strings.Contains(err.Error(), "platform-secrets/bao-root") {
		t.Errorf("Config err = %v, want the Secret named for the connection status", err)
	}

	r, pool := newResolver(t, conn, namespace(teamA, nil), secret("platform-secrets", "bao-root", map[string]string{tokenKey: "root"}))
	pool.err = fmt.Errorf("%w: token file /var/run/agent/token: denied", engine.ErrConnectionDenied)
	if _, err := r.For(context.Background(), teamA, ref); !errors.Is(err, engine.ErrConnectionDenied) || err.Error() != want {
		t.Errorf("login: err = %v, want ErrConnectionDenied reading %q", err, want)
	}
}

func TestForClusterSelectorUnsetDeniesEmptyAllows(t *testing.T) {
	auth := corev1alpha1.ClusterConnectionAuth{Kubernetes: &corev1alpha1.ClusterKubernetesAuth{
		MountPath: kubernetesMount, Role: litOperator,
	}}
	ref := commonv1alpha1.ConnectionReference{Kind: commonv1alpha1.ConnectionKindCluster, Name: clusterConnName}
	for name, tc := range map[string]struct {
		allowed *metav1.LabelSelector
		denied  bool
	}{
		"unset denies":  {allowed: nil, denied: true},
		"empty allows":  {allowed: &metav1.LabelSelector{}},
		"selector miss": {allowed: &metav1.LabelSelector{MatchLabels: map[string]string{tenantLabel: litTrue}}, denied: true},
	} {
		t.Run(name, func(t *testing.T) {
			r, _ := newResolver(t, clusterConn(auth, tc.allowed), namespace("anything", nil))
			r.TokenFile = writeTokenFile(t, testJWT("operator"))
			_, err := r.For(context.Background(), "anything", ref)
			if tc.denied != errors.Is(err, engine.ErrConnectionDenied) || (!tc.denied && err != nil) {
				t.Errorf("err = %v, want denied %v", err, tc.denied)
			}
		})
	}
}

func TestConfigServiceAccountTokenRequest(t *testing.T) {
	conn := namespacedConn(corev1alpha1.ConnectionAuth{Kubernetes: &corev1alpha1.KubernetesAuth{
		MountPath:         kubernetesMount,
		Role:              teamA,
		ServiceAccountRef: &commonv1alpha1.LocalObjectReference{Name: litConfigurer},
		Audiences:         []string{testAudience},
	}})
	sa := &corev1.ServiceAccount{ObjectMeta: metav1.ObjectMeta{Namespace: teamA, Name: litConfigurer}}
	r, _ := newResolver(t, conn, sa)
	ctx := context.Background()

	cfg, err := r.Config(ctx, conn)
	if err != nil {
		t.Fatal(err)
	}
	auth, ok := innerAuth(t, cfg).(openbao.KubernetesAuth)
	if !ok {
		t.Fatalf("auth is %T, want openbao.KubernetesAuth", innerAuth(t, cfg))
	}
	if auth.Role != teamA || auth.CredentialKey != "serviceaccount:team-a/configurer:openbao" {
		t.Errorf("unexpected auth %+v", auth)
	}
	if jwt, err := auth.JWT(ctx); err != nil || jwt == "" {
		t.Errorf("JWT() = %q, %v", jwt, err)
	}
}

func TestConfigNamespacedRequiresServiceAccount(t *testing.T) {
	for name, auth := range map[string]corev1alpha1.ConnectionAuth{
		kubernetesMount: {Kubernetes: &corev1alpha1.KubernetesAuth{Role: "x"}},
		litJwt:          {JWT: &corev1alpha1.JWTAuth{Role: "x"}},
	} {
		conn := namespacedConn(auth)
		r, _ := newResolver(t, conn)
		if _, err := r.Config(context.Background(), conn); !errors.Is(err, ErrMissingServiceAccount) {
			t.Errorf("%s: err = %v, want ErrMissingServiceAccount", name, err)
		}
	}
}

func TestConfigNamespacedRequiresAudiences(t *testing.T) {
	sa := &commonv1alpha1.LocalObjectReference{Name: litConfigurer}
	for name, auth := range map[string]corev1alpha1.ConnectionAuth{
		kubernetesMount: {Kubernetes: &corev1alpha1.KubernetesAuth{Role: "x", ServiceAccountRef: sa}},
		litJwt:          {JWT: &corev1alpha1.JWTAuth{Role: "x", ServiceAccountRef: sa}},
	} {
		conn := namespacedConn(auth)
		r, _ := newResolver(t, conn)
		if _, err := r.Config(context.Background(), conn); !errors.Is(err, ErrMissingAudiences) {
			t.Errorf("%s: err = %v, want ErrMissingAudiences", name, err)
		}
	}
}

func TestConfigClusterOperatorToken(t *testing.T) {
	operatorJWT := testJWT("operator")
	path := writeTokenFile(t, operatorJWT)
	conn := clusterConn(corev1alpha1.ClusterConnectionAuth{JWT: &corev1alpha1.ClusterJWTAuth{
		MountPath: litJwt, Role: litOperator,
	}}, nil)
	r, _ := newResolver(t, conn)
	r.TokenFile = path

	cfg, err := r.Config(context.Background(), conn)
	if err != nil {
		t.Fatal(err)
	}
	auth, ok := innerAuth(t, cfg).(openbao.JWTAuth)
	if !ok {
		t.Fatalf("auth is %T, want openbao.JWTAuth", innerAuth(t, cfg))
	}
	if jwt, err := auth.JWT(context.Background()); err != nil || jwt != operatorJWT {
		t.Errorf("JWT() = %q, %v", jwt, err)
	}
}

func TestConfigServerSettingsAndAppRole(t *testing.T) {
	conn := namespacedConn(corev1alpha1.ConnectionAuth{AppRole: &corev1alpha1.AppRoleAuth{
		MountPath:         appRoleName,
		RoleIDSecretRef:   commonv1alpha1.SecretKeySelector{Name: appRoleName, Key: "role-id"},
		SecretIDSecretRef: commonv1alpha1.SecretKeySelector{Name: appRoleName, Key: "secret-id"},
	}})
	authNamespace := ""
	conn.Spec.AuthNamespace = &authNamespace
	server := tenantServer()
	server.Spec.CABundle = &corev1alpha1.CABundleSource{
		ConfigMapRef: &corev1alpha1.NamespacedCABundleReference{Namespace: litOpenBaoNS, Name: "ca"},
	}
	server.Spec.RequestTimeout = &metav1.Duration{Duration: 7e9}
	server.Spec.RateLimit = &corev1alpha1.ServerRateLimit{QPS: 20}
	ca := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Namespace: litOpenBaoNS, Name: "ca"},
		Data:       map[string]string{"ca.crt": "PEM"},
	}
	r, _ := newResolver(t, server, conn, ca, secret(teamA, appRoleName, map[string]string{"role-id": "r", "secret-id": "s"}))
	ctx := context.Background()

	cfg, err := r.Config(ctx, conn)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Address != testAddress || string(cfg.CABundle) != "PEM" || cfg.Timeout.Seconds() != 7 ||
		cfg.Namespace != litTenantPath || cfg.AuthNamespace != "" {
		t.Errorf("unexpected config %+v", cfg)
	}
	if cfg.RateLimit != (openbao.RateLimit{QPS: 20, Burst: 20}) {
		t.Errorf("RateLimit = %+v, want qps 20 with burst defaulted to qps", cfg.RateLimit)
	}
	auth, ok := innerAuth(t, cfg).(openbao.AppRoleAuth)
	if !ok {
		t.Fatalf("auth is %T, want openbao.AppRoleAuth", innerAuth(t, cfg))
	}
	roleID, _ := auth.RoleID(ctx)
	secretID, _ := auth.SecretID(ctx)
	if roleID != "r" || secretID != "s" {
		t.Errorf("RoleID/SecretID = %q/%q", roleID, secretID)
	}

	if got, want := References(conn), []string{"Secret/team-a/approle"}; strings.Join(got, " ") != strings.Join(want, " ") {
		t.Errorf("References(connection) = %v, want %v", got, want)
	}
	if got, want := References(server), []string{"ConfigMap/openbao/ca"}; strings.Join(got, " ") != strings.Join(want, " ") {
		t.Errorf("References(server) = %v, want %v", got, want)
	}
}

func TestConfigServerCABundleMissing(t *testing.T) {
	server := tenantServer()
	server.Spec.CABundle = &corev1alpha1.CABundleSource{
		SecretRef: &corev1alpha1.NamespacedCABundleReference{Namespace: litOpenBaoNS, Name: "ca"},
	}
	r, _ := newResolver(t, server)
	if _, err := r.ServerConfig(context.Background(), server); !errors.Is(err, ErrReferenceNotFound) {
		t.Errorf("err = %v, want ErrReferenceNotFound", err)
	}
}

func TestConfigServerNotFound(t *testing.T) {
	conn := namespacedConn(tokenAuth)
	conn.Spec.ServerRef.Name = "absent"
	r, _ := newResolver(t, conn)
	_, err := r.Config(context.Background(), conn)
	if !errors.Is(err, ErrServerNotFound) || !errors.Is(err, engine.ErrConnectionNotReady) {
		t.Errorf("err = %v, want ErrServerNotFound and ErrConnectionNotReady", err)
	}
}

func TestCacheKeyCoversGenerationsAndCredential(t *testing.T) {
	conn := namespacedConn(tokenAuth)
	server := tenantServer()
	tokenSecret := secret(teamA, "bao-token", map[string]string{tokenKey: litToken})
	tokenSecret.ResourceVersion = "7"
	r, _ := newResolver(t, conn, tokenSecret)
	ctx := context.Background()

	key := func(conn *corev1alpha1.OpenBaoConnection, server *corev1alpha1.OpenBaoServer) string {
		t.Helper()
		cfg, err := r.ConfigFor(ctx, conn, server)
		if err != nil {
			t.Fatal(err)
		}
		return cfg.Auth.CacheKey()
	}
	base := key(conn, server)
	if !strings.HasPrefix(base, "uid-1/3/5/") || strings.Contains(base, litToken) || strings.Contains(base, "bao-token") {
		t.Errorf("cache key %q, want uid/generation/server generation/credential hash", base)
	}
	if key(conn, server) != base {
		t.Error("cache key changed without any change")
	}

	newer := conn.DeepCopy()
	newer.Generation = 4
	moved := server.DeepCopy()
	moved.Generation = 6
	for name, got := range map[string]string{
		"connection generation": key(newer, server),
		"server generation":     key(conn, moved),
	} {
		if got == base {
			t.Errorf("%s change kept cache key %q", name, got)
		}
	}

	tokenSecret.Data[tokenKey] = []byte("s.rotated")
	if err := r.Client.Update(ctx, tokenSecret); err != nil {
		t.Fatal(err)
	}
	if key(conn, server) == base {
		t.Error("credential rotation kept the cache key")
	}
}

func TestConfigMissingSecret(t *testing.T) {
	conn := namespacedConn(tokenAuth)
	r, _ := newResolver(t, conn)
	if _, err := r.Config(context.Background(), conn); !errors.Is(err, ErrReferenceNotFound) {
		t.Errorf("err = %v, want ErrReferenceNotFound", err)
	}
	r, _ = newResolver(t, conn, secret(teamA, "bao-token", map[string]string{"other": "x"}))
	if _, err := r.Config(context.Background(), conn); !errors.Is(err, ErrReferenceNotFound) {
		t.Errorf("missing key: err = %v, want ErrReferenceNotFound", err)
	}
}
