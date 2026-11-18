package core

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
	"sigs.k8s.io/yaml"

	commonv1alpha1 "github.com/alexandrust88/openbao-config-operator/api/common/v1alpha1"
	corev1alpha1 "github.com/alexandrust88/openbao-config-operator/api/core/v1alpha1"
	"github.com/alexandrust88/openbao-config-operator/internal/connection"
	"github.com/alexandrust88/openbao-config-operator/internal/engine"
	"github.com/alexandrust88/openbao-config-operator/test/testenv"
)

const (
	projectedTokenPath       = "/var/run/secrets/openbao/token"
	errExactlyOneTokenSource = "exactly one of the fields in [secretRef filesystem] must be set"
	errAtMostOneIdentity     = "at most one of the fields in [serviceAccountRef injectedIdentity] may be set"
	errServiceAccountNeeded  = "serviceAccountRef is required"
	sinkTokenPath            = "/vault/sink/token"
	errAutomountedToken      = "the automounted ServiceAccount token is a Kubernetes API credential"
	fieldName                = "name"
	fieldPath                = "path"
	fieldServerRef           = "serverRef"
)

var (
	connectionTestScheme = runtime.NewScheme()
	// envtestClient talks to envtest; it is nil when the envtest binaries are absent.
	envtestClient client.Client
	// envtestConfig and envtestEnv belong to envtestClient.
	envtestConfig *rest.Config
	envtestEnv    *envtest.Environment
)

func TestMain(m *testing.M) {
	if err := clientgoscheme.AddToScheme(connectionTestScheme); err != nil {
		panic(err)
	}
	if err := corev1alpha1.AddToScheme(connectionTestScheme); err != nil {
		panic(err)
	}
	os.Exit(runWithEnvtest(m))
}

func runWithEnvtest(m *testing.M) int {
	root := filepath.Join("..", "..", "..")
	assets := testenv.Assets(root)
	if assets == "" {
		return m.Run()
	}
	crds := filepath.Join(root, "config", "crd", "bases")
	env := &envtest.Environment{
		BinaryAssetsDirectory: assets,
		ErrorIfCRDPathMissing: true,
		CRDDirectoryPaths: []string{
			filepath.Join(crds, "core.obco.dev_baoconnections.yaml"),
			filepath.Join(crds, "core.obco.dev_clusterbaoconnections.yaml"),
			filepath.Join(crds, "core.obco.dev_baoservers.yaml"),
			filepath.Join(crds, "core.obco.dev_baoendpoints.yaml"),
		},
	}
	cfg, err := env.Start()
	if err != nil {
		fmt.Fprintf(os.Stderr, "start envtest: %v\n", err)
		return 1
	}
	defer func() { _ = env.Stop() }()
	envtestClient, err = client.New(cfg, client.Options{Scheme: connectionTestScheme})
	if err != nil {
		fmt.Fprintf(os.Stderr, "envtest client: %v\n", err)
		return 1
	}
	envtestConfig, envtestEnv = cfg, env
	return m.Run()
}

func requireEnvtest(t *testing.T) {
	t.Helper()
	if envtestClient == nil {
		testenv.SkipOrFail(t, "envtest binaries not found; set KUBEBUILDER_ASSETS")
	}
}

var validationNamespace = "validation"

func ensureValidationNamespace(t *testing.T) {
	t.Helper()
	ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: validationNamespace}}
	if err := envtestClient.Create(context.Background(), ns); err != nil && !apierrors.IsAlreadyExists(err) {
		t.Fatal(err)
	}
}

func saRef() *commonv1alpha1.LocalObjectReference {
	return &commonv1alpha1.LocalObjectReference{Name: "x"}
}

func serverSpec() corev1alpha1.ConnectionSpec {
	return corev1alpha1.ConnectionSpec{ServerRef: corev1alpha1.ServerReference{Name: openbaoName}}
}

func TestBaoConnectionValidation(t *testing.T) {
	requireEnvtest(t)
	ensureValidationNamespace(t)
	token := &corev1alpha1.TokenAuth{SecretRef: commonv1alpha1.SecretKeySelector{Name: "t", Key: tokenKey}}

	cases := []struct {
		name    string
		auth    corev1alpha1.ConnectionAuth
		mutate  func(*corev1alpha1.BaoConnectionSpec)
		wantErr string
	}{
		{name: tokenKey, auth: corev1alpha1.ConnectionAuth{Token: token}},
		{
			name: "kubernetes with service account",
			auth: corev1alpha1.ConnectionAuth{Kubernetes: &corev1alpha1.KubernetesAuth{
				Role: "r", ServiceAccountRef: saRef(), Audiences: []string{openbaoName},
			}},
		},
		{
			name:    "kubernetes without audiences",
			auth:    corev1alpha1.ConnectionAuth{Kubernetes: &corev1alpha1.KubernetesAuth{Role: "r", ServiceAccountRef: saRef()}},
			wantErr: "audiences is required",
		},
		{
			name:    "jwt without audiences",
			auth:    corev1alpha1.ConnectionAuth{JWT: &corev1alpha1.JWTAuth{Role: "r", ServiceAccountRef: saRef()}},
			wantErr: "audiences is required",
		},
		{
			name:    "kubernetes without service account",
			auth:    corev1alpha1.ConnectionAuth{Kubernetes: &corev1alpha1.KubernetesAuth{Role: "r"}},
			wantErr: errServiceAccountNeeded,
		},
		{
			name:    "jwt without service account",
			auth:    corev1alpha1.ConnectionAuth{JWT: &corev1alpha1.JWTAuth{Role: "r"}},
			wantErr: errServiceAccountNeeded,
		},
		{name: "no auth", wantErr: errExactlyOneAuth},
		{
			name: "two auths",
			auth: corev1alpha1.ConnectionAuth{
				Token:      token,
				Kubernetes: &corev1alpha1.KubernetesAuth{Role: "r", ServiceAccountRef: saRef(), Audiences: []string{openbaoName}},
			},
			wantErr: errExactlyOneAuth,
		},
		{
			name:    "empty server name",
			auth:    corev1alpha1.ConnectionAuth{Token: token},
			mutate:  func(s *corev1alpha1.BaoConnectionSpec) { s.ServerRef.Name = "" },
			wantErr: "spec.serverRef.name",
		},
	}
	for i, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			conn := &corev1alpha1.BaoConnection{
				ObjectMeta: metav1.ObjectMeta{Name: fmt.Sprintf("conn-%d", i), Namespace: validationNamespace},
				Spec:       corev1alpha1.BaoConnectionSpec{ConnectionSpec: serverSpec(), Auth: tc.auth},
			}
			if tc.mutate != nil {
				tc.mutate(&conn.Spec)
			}
			assertConnectionCreate(t, conn, tc.wantErr)
		})
	}
}

func TestBaoConnectionDefaults(t *testing.T) {
	requireEnvtest(t)
	ensureValidationNamespace(t)
	conn := &corev1alpha1.BaoConnection{
		ObjectMeta: metav1.ObjectMeta{Name: "defaults", Namespace: validationNamespace},
		Spec: corev1alpha1.BaoConnectionSpec{
			ConnectionSpec: serverSpec(),
			Auth: corev1alpha1.ConnectionAuth{JWT: &corev1alpha1.JWTAuth{
				Role: "r", ServiceAccountRef: saRef(), Audiences: []string{openbaoName},
			}},
		},
	}
	if err := envtestClient.Create(context.Background(), conn); err != nil {
		t.Fatal(err)
	}
	if conn.Spec.Auth.JWT.MountPath != "jwt" {
		t.Errorf("default not applied: mountPath=%q", conn.Spec.Auth.JWT.MountPath)
	}
}

func TestClusterBaoConnectionValidation(t *testing.T) {
	requireEnvtest(t)
	cases := []struct {
		name    string
		auth    corev1alpha1.ClusterConnectionAuth
		wantErr string
	}{
		{
			name: "kubernetes with operator token",
			auth: corev1alpha1.ClusterConnectionAuth{Kubernetes: &corev1alpha1.ClusterKubernetesAuth{Role: "r"}},
		},
		{
			name: "kubernetes with namespaced service account",
			auth: corev1alpha1.ClusterConnectionAuth{Kubernetes: &corev1alpha1.ClusterKubernetesAuth{
				Role:              "r",
				ServiceAccountRef: &corev1alpha1.NamespacedObjectReference{Namespace: openbaoName, Name: "x"},
				Audiences:         []string{openbaoName},
			}},
		},
		{
			name: "audiences without service account",
			auth: corev1alpha1.ClusterConnectionAuth{JWT: &corev1alpha1.ClusterJWTAuth{
				Role: "r", Audiences: []string{openbaoName},
			}},
			wantErr: "audiences require serviceAccountRef",
		},
		{
			name: "service account without audiences",
			auth: corev1alpha1.ClusterConnectionAuth{Kubernetes: &corev1alpha1.ClusterKubernetesAuth{
				Role: "r", ServiceAccountRef: &corev1alpha1.NamespacedObjectReference{Namespace: openbaoName, Name: "x"},
			}},
			wantErr: "serviceAccountRef requires audiences",
		},
		{
			name: "jwt service account without audiences",
			auth: corev1alpha1.ClusterConnectionAuth{JWT: &corev1alpha1.ClusterJWTAuth{
				Role: "r", ServiceAccountRef: &corev1alpha1.NamespacedObjectReference{Namespace: openbaoName, Name: "x"},
			}},
			wantErr: "serviceAccountRef requires audiences",
		},
		{
			name: "automounted token path",
			auth: corev1alpha1.ClusterConnectionAuth{Kubernetes: &corev1alpha1.ClusterKubernetesAuth{
				Role: "r", InjectedIdentity: &corev1alpha1.InjectedIdentity{TokenPath: "/var/run/secrets/kubernetes.io/serviceaccount/token"},
			}},
			wantErr: errAutomountedToken,
		},
		{
			name: "automounted token file",
			auth: corev1alpha1.ClusterConnectionAuth{Token: &corev1alpha1.ClusterTokenAuth{
				Filesystem: &corev1alpha1.FilesystemTokenSource{Path: "/var/run/secrets/kubernetes.io/serviceaccount/token"},
			}},
			wantErr: errAutomountedToken,
		},
		{
			name: "token ref without namespace",
			auth: corev1alpha1.ClusterConnectionAuth{Token: &corev1alpha1.ClusterTokenAuth{
				SecretRef: &corev1alpha1.NamespacedSecretKeySelector{Name: "t", Key: tokenKey},
			}},
			wantErr: "spec.auth.token.secretRef.namespace",
		},
		{name: "no auth", wantErr: errExactlyOneAuth},
		{
			name: "jwt with injected identity",
			auth: corev1alpha1.ClusterConnectionAuth{JWT: &corev1alpha1.ClusterJWTAuth{
				Role: "r", InjectedIdentity: &corev1alpha1.InjectedIdentity{TokenPath: projectedTokenPath},
			}},
		},
		{
			name: "service account and injected identity",
			auth: corev1alpha1.ClusterConnectionAuth{Kubernetes: &corev1alpha1.ClusterKubernetesAuth{
				Role:              "r",
				ServiceAccountRef: &corev1alpha1.NamespacedObjectReference{Namespace: openbaoName, Name: "x"},
				InjectedIdentity:  &corev1alpha1.InjectedIdentity{},
			}},
			wantErr: errAtMostOneIdentity,
		},
		{
			name: "jwt service account and injected identity",
			auth: corev1alpha1.ClusterConnectionAuth{JWT: &corev1alpha1.ClusterJWTAuth{
				Role:              "r",
				ServiceAccountRef: &corev1alpha1.NamespacedObjectReference{Namespace: openbaoName, Name: "x"},
				InjectedIdentity:  &corev1alpha1.InjectedIdentity{},
			}},
			wantErr: errAtMostOneIdentity,
		},
		{
			name: "injected identity with audiences",
			auth: corev1alpha1.ClusterConnectionAuth{Kubernetes: &corev1alpha1.ClusterKubernetesAuth{
				Role: "r", InjectedIdentity: &corev1alpha1.InjectedIdentity{}, Audiences: []string{openbaoName},
			}},
			wantErr: "audiences require serviceAccountRef",
		},
		{
			name: "relative token path",
			auth: corev1alpha1.ClusterConnectionAuth{JWT: &corev1alpha1.ClusterJWTAuth{
				Role: "r", InjectedIdentity: &corev1alpha1.InjectedIdentity{TokenPath: tokenKey},
			}},
			wantErr: "spec.auth.jwt.injectedIdentity.tokenPath",
		},
		{
			name: "filesystem token",
			auth: corev1alpha1.ClusterConnectionAuth{Token: &corev1alpha1.ClusterTokenAuth{
				Filesystem: &corev1alpha1.FilesystemTokenSource{Path: sinkTokenPath},
			}},
		},
		{
			name: "filesystem and secret token",
			auth: corev1alpha1.ClusterConnectionAuth{Token: &corev1alpha1.ClusterTokenAuth{
				SecretRef:  &corev1alpha1.NamespacedSecretKeySelector{Namespace: openbaoName, Name: "t", Key: tokenKey},
				Filesystem: &corev1alpha1.FilesystemTokenSource{Path: sinkTokenPath},
			}},
			wantErr: errExactlyOneTokenSource,
		},
		{
			name:    "token without source",
			auth:    corev1alpha1.ClusterConnectionAuth{Token: &corev1alpha1.ClusterTokenAuth{}},
			wantErr: errExactlyOneTokenSource,
		},
		{
			name: "relative filesystem path",
			auth: corev1alpha1.ClusterConnectionAuth{Token: &corev1alpha1.ClusterTokenAuth{
				Filesystem: &corev1alpha1.FilesystemTokenSource{Path: "sink/token"},
			}},
			wantErr: "spec.auth.token.filesystem.path",
		},
	}
	for i, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			conn := &corev1alpha1.ClusterBaoConnection{
				ObjectMeta: metav1.ObjectMeta{Name: fmt.Sprintf("cluster-conn-%d", i)},
				Spec:       corev1alpha1.ClusterBaoConnectionSpec{ConnectionSpec: serverSpec(), Auth: tc.auth},
			}
			assertConnectionCreate(t, conn, tc.wantErr)
		})
	}
}

func TestClusterBaoConnectionInjectedIdentityDefault(t *testing.T) {
	requireEnvtest(t)
	conn := &corev1alpha1.ClusterBaoConnection{
		ObjectMeta: metav1.ObjectMeta{Name: "injected-default"},
		Spec: corev1alpha1.ClusterBaoConnectionSpec{
			ConnectionSpec: serverSpec(),
			Auth: corev1alpha1.ClusterConnectionAuth{Kubernetes: &corev1alpha1.ClusterKubernetesAuth{
				Role: "r", InjectedIdentity: &corev1alpha1.InjectedIdentity{},
			}},
		},
	}
	if err := envtestClient.Create(context.Background(), conn); err != nil {
		t.Fatal(err)
	}
	if got := conn.Spec.Auth.Kubernetes.InjectedIdentity.TokenPath; got != connection.DefaultTokenFile {
		t.Errorf("tokenPath = %q, want %q", got, connection.DefaultTokenFile)
	}
}

// TestBaoConnectionRejectsOperatorSources proves the namespaced schema has no injected identity or file
// source: strict field validation rejects them, and the default validation prunes them so the
// serviceAccountRef and secretRef rules still apply.
func TestBaoConnectionRejectsOperatorSources(t *testing.T) {
	requireEnvtest(t)
	ensureValidationNamespace(t)
	cases := []struct {
		name    string
		auth    map[string]any
		strict  bool
		wantErr string
	}{
		{
			name: "injected identity strict",
			auth: map[string]any{"kubernetes": map[string]any{
				"role": "r", "serviceAccountRef": map[string]any{fieldName: "x"}, "audiences": []any{openbaoName},
				"injectedIdentity": map[string]any{"tokenPath": projectedTokenPath},
			}},
			strict:  true,
			wantErr: `unknown field "spec.auth.kubernetes.injectedIdentity"`,
		},
		{
			name: "injected identity pruned",
			auth: map[string]any{"jwt": map[string]any{
				"role": "r", "injectedIdentity": map[string]any{},
			}},
			wantErr: errServiceAccountNeeded,
		},
		{
			name: "filesystem token strict",
			auth: map[string]any{tokenKey: map[string]any{
				"secretRef":   map[string]any{fieldName: "t", "key": tokenKey},
				litFilesystem: map[string]any{fieldPath: sinkTokenPath},
			}},
			strict:  true,
			wantErr: `unknown field "spec.auth.token.filesystem"`,
		},
		{
			name:    "filesystem token pruned",
			auth:    map[string]any{tokenKey: map[string]any{litFilesystem: map[string]any{fieldPath: sinkTokenPath}}},
			wantErr: "spec.auth.token.secretRef",
		},
	}
	for i, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			conn := &unstructured.Unstructured{Object: map[string]any{
				"apiVersion": corev1alpha1.GroupVersion.String(),
				"kind":       kindConnection,
				"metadata":   map[string]any{"name": fmt.Sprintf("operator-source-%d", i), "namespace": validationNamespace},
				"spec": map[string]any{
					fieldServerRef: map[string]any{fieldName: openbaoName}, litAuth: tc.auth,
				},
			}}
			var opts []client.CreateOption
			if tc.strict {
				opts = append(opts, client.FieldValidation("Strict"))
			}
			err := envtestClient.Create(context.Background(), conn, opts...)
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("err = %v, want it to contain %q", err, tc.wantErr)
			}
		})
	}
}

func TestCoreConnectionExamples(t *testing.T) {
	requireEnvtest(t)
	var files []string
	for _, pattern := range []string{"*connection*.yaml", "*server*.yaml"} {
		matches, err := filepath.Glob(filepath.Join("..", "..", "..", "examples", "core", pattern))
		if err != nil {
			t.Fatal(err)
		}
		files = append(files, matches...)
	}
	if len(files) == 0 {
		t.Fatal("no connection examples found")
	}
	for _, file := range files {
		data, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		for i, doc := range strings.Split(string(data), "\n---\n") {
			obj := &unstructured.Unstructured{}
			if err := yaml.Unmarshal([]byte(doc), &obj.Object); err != nil {
				t.Fatalf("%s #%d: %v", file, i, err)
			}
			if len(obj.Object) == 0 {
				continue
			}
			if obj.GetNamespace() != "" {
				ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: obj.GetNamespace()}}
				if err := envtestClient.Create(context.Background(), ns); err != nil && !apierrors.IsAlreadyExists(err) {
					t.Fatal(err)
				}
			}
			err := envtestClient.Create(context.Background(), obj, client.DryRunAll, client.FieldValidation("Strict"))
			if err != nil {
				t.Errorf("%s #%d %s: %v", file, i, obj.GetName(), err)
			}
		}
	}
}

func assertConnectionCreate(t *testing.T, obj client.Object, wantErr string) {
	t.Helper()
	err := envtestClient.Create(context.Background(), obj)
	if wantErr == "" {
		if err != nil {
			t.Fatalf("create: %v", err)
		}
		return
	}
	if !apierrors.IsInvalid(err) {
		t.Fatalf("err = %v, want Invalid", err)
	}
	if !strings.Contains(err.Error(), wantErr) {
		t.Errorf("err = %v, want it to contain %q", err, wantErr)
	}
}

// TestClusterConnectionAllowedNamespacesEnvtest proves that an empty allowedNamespaces survives the API server
// round trip, so unset (deny every namespace) and {} (allow every namespace) stay distinct.
func TestClusterConnectionAllowedNamespacesEnvtest(t *testing.T) {
	requireEnvtest(t)
	ensureValidationNamespace(t)
	ctx := context.Background()
	server := &corev1alpha1.BaoServer{
		ObjectMeta: metav1.ObjectMeta{Name: "allowedns"},
		Spec:       corev1alpha1.BaoServerSpec{Address: testAddress},
	}
	if err := envtestClient.Create(ctx, server); err != nil {
		t.Fatal(err)
	}
	r := &connection.Resolver{Client: envtestClient}
	for name, tc := range map[string]struct {
		allowed *metav1.LabelSelector
		denied  bool
	}{
		"unset":    {denied: true},
		"empty":    {allowed: &metav1.LabelSelector{}},
		"selector": {allowed: &metav1.LabelSelector{MatchLabels: map[string]string{tenantLabel: "true"}}, denied: true},
	} {
		t.Run(name, func(t *testing.T) {
			conn := &corev1alpha1.ClusterBaoConnection{
				ObjectMeta: metav1.ObjectMeta{Name: "allowedns-" + name},
				Spec: corev1alpha1.ClusterBaoConnectionSpec{
					ConnectionSpec: corev1alpha1.ConnectionSpec{ServerRef: corev1alpha1.ServerReference{Name: server.Name}},
					Auth: corev1alpha1.ClusterConnectionAuth{Kubernetes: &corev1alpha1.ClusterKubernetesAuth{
						Role: "r", InjectedIdentity: &corev1alpha1.InjectedIdentity{},
					}},
					AllowedNamespaces: tc.allowed,
				},
			}
			if err := envtestClient.Create(ctx, conn); err != nil {
				t.Fatal(err)
			}
			ref := commonv1alpha1.ConnectionReference{Kind: commonv1alpha1.ConnectionKindCluster, Name: conn.Name}
			_, err := r.ServerFor(ctx, validationNamespace, ref)
			if tc.denied != errors.Is(err, engine.ErrConnectionDenied) || (!tc.denied && err != nil) {
				t.Errorf("err = %v, want denied %v", err, tc.denied)
			}
		})
	}
}

// TestDriftIntervalFloorEnvtest proves the shared ResourceSpec CEL refuses a driftInterval under 30s.
func TestDriftIntervalFloorEnvtest(t *testing.T) {
	requireEnvtest(t)
	ensureValidationNamespace(t)
	for i, tc := range []struct{ interval, wantErr string }{
		{"1ns", "driftInterval must be at least 30s"},
		{"29s", "driftInterval must be at least 30s"},
		{"30s", ""},
		{"1h", ""},
	} {
		d, err := time.ParseDuration(tc.interval)
		if err != nil {
			t.Fatal(err)
		}
		endpoint := &corev1alpha1.BaoEndpoint{
			ObjectMeta: metav1.ObjectMeta{Name: fmt.Sprintf("drift-floor-%d", i), Namespace: validationNamespace},
			Spec: corev1alpha1.BaoEndpointSpec{
				ResourceSpec: commonv1alpha1.ResourceSpec{
					ConnectionRef: commonv1alpha1.ConnectionReference{Name: openbaoName},
					DriftInterval: &metav1.Duration{Duration: d},
				},
				Path: "sys/policies/acl/drift-floor",
				Data: &runtime.RawExtension{Raw: []byte(`{"policy":""}`)},
			},
		}
		assertConnectionCreate(t, endpoint, tc.wantErr)
	}
}
