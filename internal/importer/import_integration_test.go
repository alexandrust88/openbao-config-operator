//go:build integration

package importer

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"flag"
	"fmt"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	ctrlconfig "sigs.k8s.io/controller-runtime/pkg/config"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"
	"sigs.k8s.io/yaml"

	commonv1alpha1 "github.com/alexandrust88/openbao-config-operator/api/common/v1alpha1"
	corev1alpha1 "github.com/alexandrust88/openbao-config-operator/api/core/v1alpha1"
	authcontroller "github.com/alexandrust88/openbao-config-operator/internal/controller/auth"
	identitycontroller "github.com/alexandrust88/openbao-config-operator/internal/controller/identity"
	secretscontroller "github.com/alexandrust88/openbao-config-operator/internal/controller/secrets"
	syscontroller "github.com/alexandrust88/openbao-config-operator/internal/controller/sys"
	"github.com/alexandrust88/openbao-config-operator/internal/engine"
	"github.com/alexandrust88/openbao-config-operator/internal/openbao"
	"github.com/alexandrust88/openbao-config-operator/test/testenv"
)

var updateGolden = flag.Bool("update", false, "rewrite testdata/import.golden.yaml")

const importRootToken = "root"

// importAuditConfig declares audit devices in the server configuration; OpenBao 2.7 refuses to enable one
// through the API. The http device's URL carries synthetic credentials that the import must not emit.
const importAuditConfig = `audit "file" "declared" {
  description = "declared in config"
  options {
    file_path = %q
  }
}
audit "http" "siem" {
  options {
    uri = %q
  }
}
`

// TestImportIntegration seeds a `bao server -dev`, imports it and checks the golden YAML, then applies the YAML to
// envtest and runs every controller in Observe mode: each resource must become Ready and Synced with reason
// Observed, and the operator must not send a single write.
func TestImportIntegration(t *testing.T) {
	bin := testenv.RequireBao(t)
	assets := testenv.RequireAssets(t, filepath.Join("..", ".."))
	ctx := t.Context()
	dir := t.TempDir()
	siem := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	t.Cleanup(siem.Close)
	siemURI := strings.Replace(siem.URL, "://", "://synthetic-user:synthetic-pass@", 1) + "/ingest?token=synthetic-token"
	addr := importStartBao(t, bin, dir, siemURI)
	bao, err := openbao.NewPool().Client(ctx, "import-itest", openbao.Config{
		Address: addr,
		Timeout: 10 * time.Second,
		Auth: openbao.TokenAuth{
			CredentialKey: "import-itest",
			Token:         func(context.Context) (string, error) { return importRootToken, nil },
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	importSeed(t, bao)

	conn := commonv1alpha1.ConnectionReference{Kind: commonv1alpha1.ConnectionKindNamespaced, Name: "openbao"}
	result, err := Import(ctx, bao, Options{Connection: conn, Namespace: litObco, Recursive: true, IncludeKVPaths: true,
		Server: &Server{Name: "openbao", Address: addr}})
	if err != nil {
		t.Fatal(err)
	}
	wantNotes := []string{"Audit/siem: Observe will report drift on options", "BaoConnection/openbao logs in with Kubernetes auth role openbao as ServiceAccount " +
		"obco/openbao (audience openbao): create both, or edit spec.auth"}
	if !reflect.DeepEqual(result.Notes, wantNotes) {
		t.Errorf("notes:\n%s", strings.Join(result.Notes, "\n"))
	}
	wantPlaceholders := []string{
		"Audit/siem spec.optionsSecretRefs.uri siem/uri",
		"DatabaseConnection/database-pg spec.passwordSecretRef database-pg/password",
		"KVSecret/secret-app-config spec.dataFrom[0].secretRef secret-app-config/",
		"KVSecret/secret-top spec.dataFrom[0].secretRef secret-top/",
		"KubernetesAuthConfig/kubernetes spec.tokenReviewerJWTSecretRef kubernetes/token_reviewer_jwt",
		"UserpassUser/userpass-alice spec.passwordSecretRef userpass-alice/password",
	}
	placeholders := make([]string, 0, len(result.Placeholders))
	for _, p := range result.Placeholders {
		placeholders = append(placeholders, fmt.Sprintf("%s %s %s/%s", p.Object, p.Field, p.Secret, p.Key))
	}
	if !reflect.DeepEqual(placeholders, wantPlaceholders) {
		t.Errorf("placeholders:\n%s", strings.Join(placeholders, "\n"))
	}

	var stream bytes.Buffer
	if err := WriteStream(&stream, result.Objects); err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(stream.Bytes(), []byte("synthetic")) {
		t.Error("the emitted YAML holds the audit device's credentials")
	}
	golden := bytes.ReplaceAll(stream.Bytes(), []byte(addr), []byte("http://bao.test"))
	importCheckGolden(t, bytes.ReplaceAll(golden, []byte(dir), []byte("/audit")))

	docs := importDecode(t, stream.Bytes())
	writes := &importWriteGuard{Client: bao, count: &atomic.Int64{}}
	k8s := importStartManager(t, assets, docs, writes)
	importApply(t, k8s, docs, result.Placeholders, map[string]map[string][]byte{
		"secret-app-config": {"user": []byte("seed-user")},
		"secret-top":        {"a": []byte("b")},
		"siem":              {"uri": []byte(siemURI)},
	})
	importWaitObserved(t, k8s, docs)
	if n := writes.count.Load(); n != 0 {
		t.Errorf("the operator sent %d writes in Observe mode", n)
	}
}

func importStartBao(t *testing.T, bin, dir, siemURI string) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	listen := listener.Addr().String()
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	config := filepath.Join(dir, "audit.hcl")
	if err := os.WriteFile(config, fmt.Appendf(nil, importAuditConfig, filepath.Join(dir, "audit.log"), siemURI), 0o600); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(bin, "server", "-dev", "-dev-root-token-id="+importRootToken, "-dev-listen-address="+listen,
		"-config="+config)
	if err := cmd.Start(); err != nil {
		t.Fatalf("start bao: %v", err)
	}
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	})
	addr := "http://" + listen
	deadline := time.Now().Add(30 * time.Second)
	for {
		resp, err := http.Get(addr + "/v1/sys/health")
		if err == nil {
			_ = resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return addr
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("bao not healthy: %v", err)
		}
		time.Sleep(200 * time.Millisecond)
	}
}

// importSeed creates one or more objects of every importable kind through the OpenBao API.
func importSeed(t *testing.T, bao openbao.Client) {
	t.Helper()
	ctx := t.Context()
	write := func(c openbao.Client, path string, data map[string]any) map[string]any {
		t.Helper()
		resp, err := c.Write(ctx, path, data)
		if err != nil {
			t.Fatalf("seed %s: %v", path, err)
		}
		return resp
	}
	read := func(path, key string) string {
		t.Helper()
		data, err := bao.Read(ctx, path)
		if err != nil {
			t.Fatalf("seed read %s: %v", path, err)
		}
		return fmt.Sprint(data[key])
	}
	accessor := func(mount string) string {
		t.Helper()
		data, err := bao.Read(ctx, "sys/auth")
		if err != nil {
			t.Fatal(err)
		}
		return fmt.Sprint(data[mount+"/"].(map[string]any)["accessor"])
	}
	caPEM, pubPEM := importTestPEM(t)

	write(bao, "sys/namespaces/team-a", nil)
	write(bao.WithNamespace("team-a"), "sys/namespaces/sub", nil)
	write(bao.WithNamespace("team-a"), "sys/policies/acl/nsp", map[string]any{litPolicy: `path "x" { capabilities = ["read"] }`})
	write(bao, "sys/policies/acl/app", map[string]any{litPolicy: `path "secret/data/app/*" { capabilities = ["read"] }`})
	write(bao, "sys/policies/password/pp", map[string]any{litPolicy: "length=20\nrule \"charset\" { charset = \"abcdefghijklmnopqrstuvwxyz\" }"})
	write(bao, "sys/quotas/rate-limit/global", map[string]any{"rate": 100, "interval": "1s"})
	for _, typ := range []string{litKubernetes, "jwt", "approle", "userpass", "cert"} {
		write(bao, "sys/auth/"+typ, map[string]any{litType: typ})
	}
	write(bao, "auth/kubernetes/config", map[string]any{"kubernetes_host": litK8sHost,
		litTokenReviewerJWT: "reviewer.jwt.value", "disable_local_ca_jwt": true, "kubernetes_ca_cert": string(caPEM)})
	write(bao, "auth/kubernetes/role/app", map[string]any{"bound_service_account_names": litApp,
		"bound_service_account_namespaces": "apps", litTokenPolicies: litApp, "token_ttl": "1h"})
	write(bao, "auth/jwt/config", map[string]any{"bound_issuer": "https://issuer.example",
		"jwt_validation_pubkeys": []string{string(pubPEM)}})
	write(bao, "auth/jwt/role/ci", map[string]any{"role_type": "jwt", "user_claim": "sub", "bound_audiences": "ci",
		litTokenPolicies: litApp})
	write(bao, "auth/approle/role/svc", map[string]any{"role_id": "11111111-2222-3333-4444-555555555555",
		litTokenPolicies: litApp, "secret_id_ttl": "10m"})
	write(bao, "auth/userpass/users/alice", map[string]any{litPassword: "seed-password", litTokenPolicies: litApp})
	write(bao, "auth/cert/config", map[string]any{"disable_binding": true})
	write(bao, "auth/cert/certs/web", map[string]any{"certificate": string(caPEM), "allowed_common_names": "web.example",
		litTokenPolicies: litApp})
	write(bao, "auth/token/roles/ops", map[string]any{"allowed_policies": litApp, "orphan": true})

	write(bao, "identity/entity", map[string]any{litName: "alice", "policies": litApp, "metadata": map[string]any{"team": "a"}})
	entityID := read("identity/entity/name/alice", "id")
	write(bao, "identity/entity-alias", map[string]any{litName: "alice", "mount_accessor": accessor("userpass"),
		"canonical_id": entityID})
	write(bao, "identity/group", map[string]any{litName: "devs", "policies": litApp, litType: "external"})
	write(bao, "identity/group-alias", map[string]any{litName: "devs-ext", "mount_accessor": accessor("jwt"),
		"canonical_id": read("identity/group/name/devs", "id")})
	write(bao, "identity/oidc/key/k1", map[string]any{"algorithm": "RS256", "allowed_client_ids": "*"})
	write(bao, "identity/oidc/assignment/a1", map[string]any{"entity_ids": entityID})
	write(bao, "identity/oidc/client/c1", map[string]any{"key": "k1", "assignments": "a1",
		"redirect_uris": "https://app.example/cb"})
	write(bao, "identity/oidc/scope/s1", map[string]any{"template": `{"groups": {{identity.entity.groups.names}} }`,
		litDescription: "groups"})
	write(bao, "identity/oidc/provider/p1", map[string]any{"allowed_client_ids": read("identity/oidc/client/c1", "client_id"),
		"scopes_supported": "s1"})

	for _, typ := range []string{"pki", "transit", "ssh", "database"} {
		write(bao, "sys/mounts/"+typ, map[string]any{litType: typ})
	}
	write(bao, "pki/root/generate/internal", map[string]any{"common_name": "root.example", "issuer_name": "root-ca", "ttl": "720h"})
	write(bao, "pki/roles/web", map[string]any{"allowed_domains": "example.com", "allow_subdomains": true, "max_ttl": "72h"})
	write(bao, "pki/config/urls", map[string]any{"issuing_certificates": "http://pki.example/ca"})
	write(bao, "transit/keys/k1", map[string]any{litType: "aes256-gcm96"})
	write(bao, "ssh/roles/r1", map[string]any{"key_type": "ca", "allow_user_certificates": true, "allowed_users": "ubuntu",
		"default_user": "ubuntu", "ttl": "1h"})
	write(bao, "database/config/pg", map[string]any{"plugin_name": "postgresql-database-plugin",
		"connection_url": "postgresql://{{username}}:{{password}}@db:5432/app", "username": "admin",
		litPassword: "seed-password", "verify_connection": false, "allowed_roles": "ro"})
	write(bao, "database/roles/ro", map[string]any{"db_name": "pg", "creation_statements": `CREATE ROLE "{{name}}";`,
		"default_ttl": "1h", "max_ttl": "24h"})
	write(bao, "secret/data/app/config", map[string]any{"data": map[string]any{"user": "seed-user"}})
	write(bao, "secret/data/top", map[string]any{"data": map[string]any{"a": "b"}})
}

// importTestPEM returns a self-signed CA certificate with CN k8s-ca and the PKIX public key of its key.
func importTestPEM(t *testing.T) (caPEM, pubPEM []byte) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "k8s-ca"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(24 * time.Hour),
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageCertSign,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	pub, err := x509.MarshalPKIXPublicKey(&key.PublicKey)
	if err != nil {
		t.Fatal(err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}),
		pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: pub})
}

func importCheckGolden(t *testing.T, got []byte) {
	t.Helper()
	// The seed generates its key material per run, so the golden keeps the PEM type but not the body.
	pemBody := regexp.MustCompile(`(-----BEGIN [A-Z ]+-----\n)(?s:.*?)(\n( *)-----END [A-Z ]+-----)`)
	got = pemBody.ReplaceAll(got, []byte("${1}${3}generated-at-test-time${2}"))
	path := filepath.Join("testdata", "import.golden.yaml")
	if *updateGolden {
		if err := os.WriteFile(path, got, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read golden (run with -update to create it): %v", err)
	}
	if !bytes.Equal(got, want) {
		t.Errorf("import differs from %s (run with -update after checking the change):\n%s", path, got)
	}
}

func importDecode(t *testing.T, stream []byte) []*unstructured.Unstructured {
	t.Helper()
	var docs []*unstructured.Unstructured //nolint:prealloc // the number of documents is not known up front
	for doc := range strings.SplitSeq(string(stream), "\n---\n") {
		u := &unstructured.Unstructured{}
		if err := yaml.Unmarshal([]byte(doc), &u.Object); err != nil {
			t.Fatalf("decode %q: %v", doc, err)
		}
		docs = append(docs, u)
	}
	return docs
}

// importWriteGuard counts and refuses every write and delete the operator sends, except identity lookups, which
// OpenBao serves as POST but which change nothing.
type importWriteGuard struct {
	openbao.Client
	count *atomic.Int64
}

func (g *importWriteGuard) Write(ctx context.Context, path string, data map[string]any) (map[string]any, error) {
	if strings.HasPrefix(path, "identity/lookup/") {
		return g.Client.Write(ctx, path, data)
	}
	g.count.Add(1)
	return nil, errors.New("write refused in Observe mode")
}

func (g *importWriteGuard) Delete(context.Context, string) error {
	g.count.Add(1)
	return errors.New("delete refused in Observe mode")
}

func (g *importWriteGuard) WithNamespace(ns string) openbao.Client {
	return &importWriteGuard{Client: g.Client.WithNamespace(ns), count: g.count}
}

// importSetups registers the controller of every importable kind.
var importSetups = []func(ctrl.Manager, engine.Connections, engine.Options) error{
	syscontroller.SetupBaoNamespace, syscontroller.SetupACLPolicy, syscontroller.SetupPasswordPolicy,
	syscontroller.SetupAuthMount, syscontroller.SetupSecretsMount, syscontroller.SetupAudit,
	syscontroller.SetupRateLimitQuota,
	authcontroller.SetupKubernetesAuthConfig, authcontroller.SetupKubernetesAuthRole, authcontroller.SetupJWTAuthConfig,
	authcontroller.SetupJWTAuthRole, authcontroller.SetupAppRole, authcontroller.SetupUserpassUser,
	authcontroller.SetupCertAuthConfig, authcontroller.SetupCertAuthRole, authcontroller.SetupTokenRole,
	identitycontroller.SetupIdentityEntity, identitycontroller.SetupIdentityEntityAlias, identitycontroller.SetupIdentityGroup,
	identitycontroller.SetupIdentityGroupAlias, identitycontroller.SetupOIDCKey, identitycontroller.SetupOIDCScope,
	identitycontroller.SetupOIDCClient, identitycontroller.SetupOIDCAssignment, identitycontroller.SetupOIDCProvider,
	secretscontroller.SetupPKIIssuer, secretscontroller.SetupPKIConfig, secretscontroller.SetupPKIRole,
	secretscontroller.SetupTransitKey, secretscontroller.SetupSSHRole, secretscontroller.SetupDatabaseConnection,
	secretscontroller.SetupDatabaseRole, secretscontroller.SetupKVSecret,
}

// importStartManager installs every CRD into envtest, since controllers also watch kinds they reference, and starts a
// manager running every importable controller against bao.
func importStartManager(t *testing.T, assets string, docs []*unstructured.Unstructured, bao openbao.Client) client.Client {
	t.Helper()
	wanted := map[schema.GroupKind]bool{}
	for _, u := range docs {
		wanted[u.GroupVersionKind().GroupKind()] = true
	}
	files, err := filepath.Glob(filepath.Join("..", "..", "config", "crd", "bases", "*.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	crds := make([]*apiextensionsv1.CustomResourceDefinition, 0, len(files))
	for _, file := range files {
		data, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		crd := &apiextensionsv1.CustomResourceDefinition{}
		if err := yaml.Unmarshal(data, crd); err != nil {
			t.Fatalf("%s: %v", file, err)
		}
		crds = append(crds, crd)
		delete(wanted, schema.GroupKind{Group: crd.Spec.Group, Kind: crd.Spec.Names.Kind})
	}
	if len(wanted) > 0 {
		t.Fatalf("no CRD for %v", wanted)
	}
	testEnv := &envtest.Environment{BinaryAssetsDirectory: assets, CRDInstallOptions: envtest.CRDInstallOptions{CRDs: crds}}
	cfg, err := testEnv.Start()
	if err != nil {
		t.Fatalf("envtest: %v", err)
	}
	t.Cleanup(func() {
		if err := testEnv.Stop(); err != nil {
			t.Logf("stop envtest: %v", err)
		}
	})
	scheme, err := newScheme()
	if err != nil {
		t.Fatal(err)
	}
	if err := clientgoscheme.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	mgr, err := ctrl.NewManager(cfg, ctrl.Options{
		Scheme:                 scheme,
		Metrics:                metricsserver.Options{BindAddress: "0"},
		HealthProbeBindAddress: "0",
		Controller:             ctrlconfig.Controller{SkipNameValidation: new(true)},
	})
	if err != nil {
		t.Fatal(err)
	}
	conns := staticConns{c: bao}
	for _, setup := range importSetups {
		if err := setup(mgr, conns, engine.Options{}); err != nil {
			t.Fatal(err)
		}
	}
	ctx, stop := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- mgr.Start(ctx) }()
	t.Cleanup(func() {
		stop()
		if err := <-done; err != nil {
			t.Logf("manager: %v", err)
		}
	})
	return mgr.GetClient()
}

// importApply creates the placeholder Secrets and every emitted resource. Write-only placeholders get a dummy
// value, which Observe never compares; KV Secrets and compared options get the seeded data.
func importApply(t *testing.T, k8s client.Client, docs []*unstructured.Unstructured, placeholders []Placeholder,
	kvData map[string]map[string][]byte) {
	t.Helper()
	ctx := t.Context()
	if err := k8s.Create(ctx, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: litObco}}); err != nil {
		t.Fatal(err)
	}
	secrets := map[string]*corev1.Secret{}
	for _, p := range placeholders {
		secret, ok := secrets[p.Secret]
		if !ok {
			secret = &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: p.Secret, Namespace: litObco}, Data: kvData[p.Secret]}
			if secret.Data == nil {
				secret.Data = map[string][]byte{}
			}
			secrets[p.Secret] = secret
		}
		if _, seeded := secret.Data[p.Key]; p.Key != "" && !seeded {
			secret.Data[p.Key] = []byte("placeholder")
		}
	}
	for _, secret := range secrets {
		if err := k8s.Create(ctx, secret); err != nil {
			t.Fatal(err)
		}
	}
	for _, u := range docs {
		if err := k8s.Create(ctx, u.DeepCopy()); err != nil {
			t.Errorf("create %s/%s: %v", u.GetKind(), u.GetName(), err)
		}
	}
}

// importWaitObserved waits until every managed resource reports Ready and Synced with reason Observed.
func importWaitObserved(t *testing.T, k8s client.Client, docs []*unstructured.Unstructured) {
	t.Helper()
	deadline := time.Now().Add(90 * time.Second)
	for _, u := range docs {
		if u.GroupVersionKind().Group == corev1alpha1.GroupVersion.Group {
			continue
		}
		var last string
		for {
			got := &unstructured.Unstructured{}
			got.SetGroupVersionKind(u.GroupVersionKind())
			err := k8s.Get(t.Context(), client.ObjectKeyFromObject(u), got)
			if err == nil {
				ready, synced := importCondition(got, commonv1alpha1.ConditionReady),
					importCondition(got, commonv1alpha1.ConditionSynced)
				if ready == "True/"+commonv1alpha1.ReasonObserved && synced == "True/"+commonv1alpha1.ReasonObserved {
					break
				}
				last = fmt.Sprintf("Ready %s, Synced %s: %v", ready, synced, got.Object["status"])
			} else {
				last = err.Error()
			}
			if time.Now().After(deadline) {
				t.Errorf("%s/%s not observed: %s", u.GetKind(), u.GetName(), last)
				break
			}
			time.Sleep(200 * time.Millisecond)
		}
	}
}

func importCondition(u *unstructured.Unstructured, conditionType string) string {
	conditions, _, _ := unstructured.NestedSlice(u.Object, "status", "conditions")
	for _, raw := range conditions {
		c, _ := raw.(map[string]any)
		if c["type"] == conditionType {
			return fmt.Sprintf("%v/%v", c["status"], c["reason"])
		}
	}
	return "absent"
}
