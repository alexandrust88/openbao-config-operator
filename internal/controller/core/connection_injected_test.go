package core

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"os"
	"path/filepath"
	"testing"
	"time"

	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	commonv1alpha1 "github.com/alexandrust88/openbao-config-operator/api/common/v1alpha1"
	corev1alpha1 "github.com/alexandrust88/openbao-config-operator/api/core/v1alpha1"
	"github.com/alexandrust88/openbao-config-operator/internal/connection"
	"github.com/alexandrust88/openbao-config-operator/internal/openbao"
)

const (
	injectedConnName = "platform"
	injectedAudience = "openbao"
	injectedSubject  = "system:serviceaccount:openbao-config-operator:openbao-config-operator"
	injectedRole     = "operator"
	injectedJWTType  = "jwt"
)

func injectedClusterConnection(auth corev1alpha1.ClusterConnectionAuth) *corev1alpha1.ClusterOpenBaoConnection {
	return &corev1alpha1.ClusterOpenBaoConnection{
		ObjectMeta: metav1.ObjectMeta{Name: injectedConnName, UID: "uid-cluster", Generation: 1},
		Spec: corev1alpha1.ClusterOpenBaoConnectionSpec{
			ConnectionSpec:    corev1alpha1.ConnectionSpec{ServerRef: corev1alpha1.ServerReference{Name: openbaoName}},
			Auth:              auth,
			AllowedNamespaces: &metav1.LabelSelector{},
		},
	}
}

// injectedReconciler reconciles conn against a Ready OpenBaoServer at address.
func injectedReconciler(t *testing.T, pool connection.Pool, address string,
	conn *corev1alpha1.ClusterOpenBaoConnection) *connectionReconciler {
	t.Helper()
	server := testServer()
	server.Spec.Address, server.Spec.AllowInsecureHTTP = address, true
	c := connectionFakeClient(conn, server, tenantNamespace(teamA))
	return &connectionReconciler{
		Client:    c,
		resolver:  &connection.Resolver{Client: c, Pool: pool},
		pool:      pool,
		kind:      commonv1alpha1.ConnectionKindCluster,
		newObject: func() connectionObject { return &corev1alpha1.ClusterOpenBaoConnection{} },
		newList:   func() client.ObjectList { return &corev1alpha1.ClusterOpenBaoConnectionList{} },
	}
}

func injectedReconcileAndGet(t *testing.T, r *connectionReconciler) *corev1alpha1.ClusterOpenBaoConnection {
	t.Helper()
	key := types.NamespacedName{Name: injectedConnName}
	if _, err := r.Reconcile(context.Background(), ctrl.Request{NamespacedName: key}); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	got := &corev1alpha1.ClusterOpenBaoConnection{}
	if err := r.Get(context.Background(), key, got); err != nil {
		t.Fatal(err)
	}
	return got
}

func injectedWriteFile(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), tokenKey)
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// injectedSignJWT returns an RS256 JWT shaped like a projected ServiceAccount token.
func injectedSignJWT(t *testing.T, key *rsa.PrivateKey) string {
	t.Helper()
	now := time.Now()
	enc := func(v any) string {
		b, err := json.Marshal(v)
		if err != nil {
			t.Fatal(err)
		}
		return base64.RawURLEncoding.EncodeToString(b)
	}
	unsigned := enc(map[string]string{"alg": "RS256", "typ": "JWT"}) + "." + enc(map[string]any{
		"iss":  "https://kubernetes.default.svc",
		litSub: injectedSubject,
		"aud":  []string{injectedAudience},
		"iat":  now.Unix(),
		"nbf":  now.Add(-time.Minute).Unix(),
		"exp":  now.Add(time.Hour).Unix(),
	})
	digest := sha256.Sum256([]byte(unsigned))
	sig, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, digest[:])
	if err != nil {
		t.Fatal(err)
	}
	return unsigned + "." + base64.RawURLEncoding.EncodeToString(sig)
}

func injectedRootClient(t *testing.T, address string) openbao.Client {
	t.Helper()
	c, err := openbao.NewPool().Client(context.Background(), litRoot, openbao.Config{
		Address: address,
		Auth: openbao.TokenAuth{CredentialKey: litRoot, Token: func(context.Context) (string, error) {
			return litRoot, nil
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	return c
}

// TestReconcileInjectedIdentityAgainstDevServer logs in with a JWT read from a file, as the operator does with
// its projected ServiceAccount token, against a jwt auth mount that trusts a local key pair.
func TestReconcileInjectedIdentityAgainstDevServer(t *testing.T) {
	address := startDevBao(t)
	ctx := context.Background()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.MarshalPKIXPublicKey(&key.PublicKey)
	if err != nil {
		t.Fatal(err)
	}
	root := injectedRootClient(t, address)
	for _, w := range []struct {
		path string
		data map[string]any
	}{
		{"sys/auth/jwt-k8s", map[string]any{"type": injectedJWTType}},
		{"auth/jwt-k8s/config", map[string]any{
			"jwt_validation_pubkeys": []string{string(pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der}))},
			"bound_issuer":           "https://kubernetes.default.svc",
		}},
		{"auth/jwt-k8s/role/operator", map[string]any{
			"role_type":       injectedJWTType,
			"user_claim":      litSub,
			"bound_subject":   injectedSubject,
			"bound_audiences": []string{injectedAudience},
			"token_policies":  []string{"default"},
			"token_ttl":       "10m",
		}},
	} {
		if _, err := root.Write(ctx, w.path, w.data); err != nil {
			t.Fatalf("write %s: %v", w.path, err)
		}
	}

	tokenPath := injectedWriteFile(t, injectedSignJWT(t, key)+"\n")
	conn := injectedClusterConnection(corev1alpha1.ClusterConnectionAuth{JWT: &corev1alpha1.ClusterJWTAuth{
		MountPath:        "jwt-k8s",
		Role:             injectedRole,
		InjectedIdentity: &corev1alpha1.InjectedIdentity{TokenPath: tokenPath},
	}})
	pool := openbao.NewPool()
	r := injectedReconciler(t, pool, address, conn)

	got := injectedReconcileAndGet(t, r)
	if !meta.IsStatusConditionTrue(got.Status.Conditions, commonv1alpha1.ConditionReady) || !got.Status.Authenticated {
		t.Fatalf("not Ready: %+v", got.Status.Conditions)
	}
	ref := commonv1alpha1.ConnectionReference{Kind: commonv1alpha1.ConnectionKindCluster, Name: injectedConnName}
	c, err := r.resolver.For(ctx, teamA, ref)
	if err != nil {
		t.Fatalf("For: %v", err)
	}
	self, err := c.Read(ctx, lookupSelfPath)
	if err != nil {
		t.Fatal(err)
	}
	if tokenMeta, _ := self["meta"].(map[string]any); tokenMeta["role"] != injectedRole {
		t.Errorf("token metadata = %v, want a jwt-k8s login as role operator", self["meta"])
	}

	if err := os.WriteFile(tokenPath, []byte("not-a-jwt"), 0o600); err != nil {
		t.Fatal(err)
	}
	pool.Invalidate(connection.PoolKey(got))
	got = injectedReconcileAndGet(t, r)
	cond := meta.FindStatusCondition(got.Status.Conditions, commonv1alpha1.ConditionReady)
	if cond == nil || cond.Reason != corev1alpha1.ReasonAuthenticationFailed {
		t.Errorf("Ready = %+v, want AuthenticationFailed once the file holds an invalid token", cond)
	}
}

func TestReconcileFilesystemTokenAgainstDevServer(t *testing.T) {
	address := startDevBao(t)
	conn := injectedClusterConnection(corev1alpha1.ClusterConnectionAuth{Token: &corev1alpha1.ClusterTokenAuth{
		Filesystem: &corev1alpha1.FilesystemTokenSource{Path: injectedWriteFile(t, "root\n")},
	}})
	r := injectedReconciler(t, openbao.NewPool(), address, conn)

	got := injectedReconcileAndGet(t, r)
	if !meta.IsStatusConditionTrue(got.Status.Conditions, commonv1alpha1.ConditionReady) || !got.Status.Authenticated {
		t.Fatalf("not Ready: %+v", got.Status.Conditions)
	}
}

func TestReconcileMissingTokenFile(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "absent")
	for name, auth := range map[string]corev1alpha1.ClusterConnectionAuth{
		"injected identity": {Kubernetes: &corev1alpha1.ClusterKubernetesAuth{
			Role: injectedRole, InjectedIdentity: &corev1alpha1.InjectedIdentity{TokenPath: missing},
		}},
		"filesystem token": {Token: &corev1alpha1.ClusterTokenAuth{
			Filesystem: &corev1alpha1.FilesystemTokenSource{Path: missing},
		}},
	} {
		t.Run(name, func(t *testing.T) {
			pool := &recordingPool{client: lookupSelfClient{}}
			got := injectedReconcileAndGet(t, injectedReconciler(t, pool, testAddress, injectedClusterConnection(auth)))
			cond := meta.FindStatusCondition(got.Status.Conditions, commonv1alpha1.ConditionReady)
			if cond == nil || cond.Status != metav1.ConditionFalse || cond.Reason != corev1alpha1.ReasonCredentialsNotFound {
				t.Errorf("Ready = %+v, want False/%s", cond, corev1alpha1.ReasonCredentialsNotFound)
			}
		})
	}
}
