package core

import (
	"context"
	"crypto/x509"
	"encoding/pem"
	"os"
	"path/filepath"
	"testing"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	commonv1alpha1 "github.com/alexandrust88/openbao-config-operator/api/common/v1alpha1"
	corev1alpha1 "github.com/alexandrust88/openbao-config-operator/api/core/v1alpha1"
	"github.com/alexandrust88/openbao-config-operator/internal/connection"
	"github.com/alexandrust88/openbao-config-operator/internal/openbao"
)

const (
	devServerName   = "bao-dev"
	devTenantNS     = "bao-dev-tenant"
	devOutsiderNS   = "bao-dev-outsider"
	devPlatformNS   = "bao-dev-platform"
	devTenantLabel  = "bao-dev"
	devJWTMount     = "jwt-k8s"
	devTenantRole   = "tenant"
	devConfigurerSA = "configurer"
)

// envtestSAPublicKey returns the PEM public key the envtest API server signs ServiceAccount tokens with.
func envtestSAPublicKey(t *testing.T) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(envtestEnv.ControlPlane.APIServer.CertDir, "sa-signer.crt"))
	if err != nil {
		t.Fatal(err)
	}
	block, _ := pem.Decode(data)
	if block == nil {
		t.Fatal("sa-signer.crt holds no PEM block")
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.MarshalPKIXPublicKey(cert.PublicKey)
	if err != nil {
		t.Fatal(err)
	}
	return string(pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der}))
}

func devTenantConnection(name, namespace, audience string) *corev1alpha1.BaoConnection {
	return &corev1alpha1.BaoConnection{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace},
		Spec: corev1alpha1.BaoConnectionSpec{
			ConnectionSpec: corev1alpha1.ConnectionSpec{ServerRef: corev1alpha1.ServerReference{Name: devServerName}},
			Auth: corev1alpha1.ConnectionAuth{JWT: &corev1alpha1.JWTAuth{
				MountPath:         devJWTMount,
				Role:              devTenantRole,
				ServiceAccountRef: &commonv1alpha1.LocalObjectReference{Name: devConfigurerSA},
				Audiences:         []string{audience},
			}},
		},
	}
}

// devConfigureJWT mounts a jwt auth method that trusts envtest ServiceAccount tokens for the tenant's
// ServiceAccount and the openbao audience.
func devConfigureJWT(t *testing.T, address, issuer string) {
	t.Helper()
	ctx := context.Background()
	root := injectedRootClient(t, address)
	for _, w := range []struct {
		path string
		data map[string]any
	}{
		{"sys/auth/" + devJWTMount, map[string]any{"type": injectedJWTType}},
		{"auth/" + devJWTMount + "/config", map[string]any{
			"jwt_validation_pubkeys": []string{envtestSAPublicKey(t)},
			"bound_issuer":           issuer,
		}},
		{"auth/" + devJWTMount + "/role/" + devTenantRole, map[string]any{
			"role_type":       injectedJWTType,
			"user_claim":      litSub,
			"bound_subject":   "system:serviceaccount:" + devTenantNS + ":" + devConfigurerSA,
			"bound_audiences": []string{openbaoName},
			"token_policies":  []string{"default"},
			"token_ttl":       "10m",
		}},
	} {
		if _, err := root.Write(ctx, w.path, w.data); err != nil {
			t.Fatalf("write %s: %v", w.path, err)
		}
	}

}

func devObjects(address string) []client.Object {
	return []client.Object{
		&corev1alpha1.BaoServer{
			ObjectMeta: metav1.ObjectMeta{Name: devServerName},
			Spec: corev1alpha1.BaoServerSpec{
				Address:           address,
				AllowInsecureHTTP: true,
				AllowedNamespaces: &metav1.LabelSelector{MatchLabels: map[string]string{tenantLabel: devTenantLabel}},
				AllowedAudiences:  []string{openbaoName},
				RateLimit:         &corev1alpha1.ServerRateLimit{QPS: 100},
			},
		},
		&corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: devTenantNS,
			Labels: map[string]string{tenantLabel: devTenantLabel}}},
		&corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: devOutsiderNS}},
		&corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: devPlatformNS}},
		&corev1.ServiceAccount{ObjectMeta: metav1.ObjectMeta{Name: devConfigurerSA, Namespace: devTenantNS}},
		&corev1.ServiceAccount{ObjectMeta: metav1.ObjectMeta{Name: devConfigurerSA, Namespace: devOutsiderNS}},
		&corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{Name: litRoot, Namespace: devPlatformNS},
			Data:       map[string][]byte{tokenKey: []byte(litRoot)},
		},
		devTenantConnection("outsider", devOutsiderNS, openbaoName),
		devTenantConnection("wrong-audience", devTenantNS, "vault"),
		devTenantConnection("tenant", devTenantNS, openbaoName),
		&corev1alpha1.ClusterBaoConnection{
			ObjectMeta: metav1.ObjectMeta{Name: "bao-dev-platform"},
			Spec: corev1alpha1.ClusterBaoConnectionSpec{
				ConnectionSpec:    corev1alpha1.ConnectionSpec{ServerRef: corev1alpha1.ServerReference{Name: devServerName}},
				AllowedNamespaces: &metav1.LabelSelector{},
				Auth: corev1alpha1.ClusterConnectionAuth{Token: &corev1alpha1.ClusterTokenAuth{
					SecretRef: &corev1alpha1.NamespacedSecretKeySelector{Namespace: devPlatformNS, Name: litRoot, Key: tokenKey},
				}},
			},
		},
	}
}

// TestServerAndConnectionsAgainstDevServer runs the BaoServer, BaoConnection and ClusterBaoConnection
// controllers against envtest and bao -dev. The admitted tenant logs in with a real TokenRequest token; a tenant in
// a namespace or with an audience the server does not allow is denied without any TokenRequest.
func TestServerAndConnectionsAgainstDevServer(t *testing.T) {
	requireEnvtest(t)
	address := startDevBao(t)
	ctx := context.Background()
	issuer := envtestEnv.ControlPlane.APIServer.SecureServing.URL("https", "/").String()

	devConfigureJWT(t, address, issuer)

	objs := devObjects(address)
	for _, obj := range objs {
		if err := envtestClient.Create(ctx, obj); err != nil {
			t.Fatalf("create %s: %v", obj.GetName(), err)
		}
	}

	base, err := client.NewWithWatch(envtestConfig, client.Options{Scheme: connectionTestScheme})
	if err != nil {
		t.Fatal(err)
	}
	tokenRequests := 0
	c := interceptor.NewClient(base, interceptor.Funcs{SubResourceCreate: func(ctx context.Context, c client.Client,
		subResource string, obj, sub client.Object, opts ...client.SubResourceCreateOption) error {
		if subResource == tokenKey {
			tokenRequests++
		}
		return c.SubResource(subResource).Create(ctx, obj, sub, opts...)
	}})
	pool := openbao.NewPool()
	resolver := &connection.Resolver{Client: c, Pool: pool, APIServerAudiences: []string{issuer}}
	servers := &serverReconciler{Client: c, resolver: resolver, probe: openbao.Probe}
	tenants := &connectionReconciler{Client: c, resolver: resolver, pool: pool,
		newObject: func() connectionObject { return &corev1alpha1.BaoConnection{} },
		newList:   func() client.ObjectList { return &corev1alpha1.BaoConnectionList{} },
	}
	platform := &connectionReconciler{Client: c, resolver: resolver, pool: pool,
		newObject: func() connectionObject { return &corev1alpha1.ClusterBaoConnection{} },
		newList:   func() client.ObjectList { return &corev1alpha1.ClusterBaoConnectionList{} },
	}

	ready := func(t *testing.T, r reconcile.Reconciler, obj client.Object, conditions func() []metav1.Condition) *metav1.Condition {
		t.Helper()
		if _, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: client.ObjectKeyFromObject(obj)}); err != nil {
			t.Fatalf("reconcile %s: %v", obj.GetName(), err)
		}
		if err := envtestClient.Get(ctx, client.ObjectKeyFromObject(obj), obj); err != nil {
			t.Fatal(err)
		}
		return meta.FindStatusCondition(conditions(), commonv1alpha1.ConditionReady)
	}

	server := &corev1alpha1.BaoServer{ObjectMeta: metav1.ObjectMeta{Name: devServerName}}
	if cond := ready(t, servers, server, func() []metav1.Condition { return server.Status.Conditions }); cond == nil ||
		cond.Status != metav1.ConditionTrue || server.Status.Version == "" || server.Status.Sealed {
		t.Fatalf("server Ready = %+v, status %+v", cond, server.Status)
	}

	for _, name := range []struct{ name, namespace string }{{"outsider", devOutsiderNS}, {"wrong-audience", devTenantNS}} {
		conn := &corev1alpha1.BaoConnection{ObjectMeta: metav1.ObjectMeta{Name: name.name, Namespace: name.namespace}}
		cond := ready(t, tenants, conn, func() []metav1.Condition { return conn.Status.Conditions })
		if cond == nil || cond.Reason != commonv1alpha1.ReasonConnectionDenied || conn.Status.Authenticated {
			t.Errorf("%s: Ready = %+v, want ConnectionDenied", name.name, cond)
		}
		ref := commonv1alpha1.ConnectionReference{Name: name.name}
		if _, err := resolver.For(ctx, name.namespace, ref); err == nil {
			t.Errorf("%s: For succeeded for a denied connection", name.name)
		}
	}
	if tokenRequests != 0 {
		t.Fatalf("%d TokenRequests sent for denied connections", tokenRequests)
	}

	tenant := &corev1alpha1.BaoConnection{ObjectMeta: metav1.ObjectMeta{Name: "tenant", Namespace: devTenantNS}}
	if cond := ready(t, tenants, tenant, func() []metav1.Condition { return tenant.Status.Conditions }); cond == nil ||
		cond.Status != metav1.ConditionTrue || !tenant.Status.Authenticated || tenant.Status.Version != server.Status.Version {
		t.Fatalf("tenant Ready = %+v, status %+v", cond, tenant.Status)
	}
	if tokenRequests == 0 {
		t.Error("the admitted tenant logged in without a TokenRequest")
	}
	tc, err := resolver.For(ctx, devTenantNS, commonv1alpha1.ConnectionReference{Name: "tenant"})
	if err != nil {
		t.Fatalf("For tenant: %v", err)
	}
	self, err := tc.Read(ctx, lookupSelfPath)
	if err != nil {
		t.Fatal(err)
	}
	if tokenMeta, _ := self["meta"].(map[string]any); tokenMeta["role"] != devTenantRole {
		t.Errorf("tenant token metadata = %v, want a %s login as role %s", self["meta"], devJWTMount, devTenantRole)
	}

	cluster := &corev1alpha1.ClusterBaoConnection{ObjectMeta: metav1.ObjectMeta{Name: "bao-dev-platform"}}
	if cond := ready(t, platform, cluster, func() []metav1.Condition { return cluster.Status.Conditions }); cond == nil ||
		cond.Status != metav1.ConditionTrue || !cluster.Status.Authenticated {
		t.Fatalf("cluster connection Ready = %+v", cond)
	}
	clusterRef := commonv1alpha1.ConnectionReference{Kind: commonv1alpha1.ConnectionKindCluster, Name: cluster.Name}
	if _, err := resolver.For(ctx, devOutsiderNS, clusterRef); err != nil {
		t.Errorf("cluster connection from a namespace the server does not admit to tenants: %v", err)
	}
}
