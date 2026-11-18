//go:build integration

package integration

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	ctrlconfig "sigs.k8s.io/controller-runtime/pkg/config"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	"sigs.k8s.io/controller-runtime/pkg/metrics"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"

	commonv1alpha1 "github.com/alexandrust88/openbao-config-operator/api/common/v1alpha1"
	sysv1alpha1 "github.com/alexandrust88/openbao-config-operator/api/sys/v1alpha1"
	"github.com/alexandrust88/openbao-config-operator/internal/controller/sys"
	"github.com/alexandrust88/openbao-config-operator/internal/engine"
	"github.com/alexandrust88/openbao-config-operator/internal/openbao"
	"github.com/alexandrust88/openbao-config-operator/test/testenv"
)

const (
	audConfigured = "configured"
	audDev        = "dev"
	audHeaders    = `{"Authorization":["Bearer itest-fake"]}`
)

// audConnections resolves connectionRef.name to one of the two test servers.
type audConnections map[string]openbao.Client

func (c audConnections) For(_ context.Context, _ string, ref commonv1alpha1.ConnectionReference) (openbao.Client, error) {
	if bao, ok := c[ref.Name]; ok {
		return bao, nil
	}
	return nil, engine.ErrConnectionNotReady
}

type audEnv struct {
	k8s        client.Client
	configured openbao.Client
	dev        openbao.Client
	dir        string
}

// TestIntegrationSysAudit runs the Audit controller against a server whose configuration allows API audit
// creation and declares one audit device, and against bao server -dev, which refuses API audit creation.
func TestIntegrationSysAudit(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	e := audSetup(ctx, t)

	t.Run("create drift delete", func(t *testing.T) { testAudLifecycle(ctx, t, e) })
	t.Run("adopt declared device", func(t *testing.T) { testAudAdoptDeclared(ctx, t, e) })
	t.Run("api creation refused", func(t *testing.T) { testAudRefused(ctx, t, e) })
}

func audSpec(conn string, deletion commonv1alpha1.DeletionPolicy) commonv1alpha1.ResourceSpec {
	return commonv1alpha1.ResourceSpec{ConnectionRef: commonv1alpha1.ConnectionReference{Name: conn}, DeletionPolicy: deletion}
}

func testAudLifecycle(ctx context.Context, t *testing.T, e audEnv) {
	receiver := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) }))
	t.Cleanup(receiver.Close)
	mustCreate(ctx, t, e.k8s, &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "itest-siem", Namespace: namespace},
		Data:       map[string][]byte{"headers": []byte(audHeaders)},
	})
	audit := &sysv1alpha1.Audit{
		ObjectMeta: metav1.ObjectMeta{
			Name:        "itest-siem",
			Namespace:   namespace,
			Annotations: map[string]string{commonv1alpha1.AnnotationAllowDestructiveDelete: "true"},
		},
		Spec: sysv1alpha1.AuditSpec{
			ResourceSpec: audSpec(audConfigured, commonv1alpha1.DeletionPolicyDelete),
			Path:         "itest/siem",
			Type:         "http",
			Description:  "itest siem forwarder",
			Options:      map[string]sysv1alpha1.AuditOptionValue{"uri": sysv1alpha1.AuditOptionValue(receiver.URL), "format": "json"},
			OptionsSecretRefs: map[string]commonv1alpha1.SecretKeySelector{
				"headers": {Name: "itest-siem", Key: "headers"},
			},
		},
	}
	mustCreate(ctx, t, e.k8s, audit)
	want := map[string]any{"type": "http", "description": "itest siem forwarder",
		"options": map[string]any{"uri": receiver.URL, "format": "json", "headers": audHeaders}}
	waitFor(t, "audit device enabled", func() error { return audExpectDevice(ctx, e.configured, "itest/siem/", want) })
	waitFor(t, "audit ready", func() error { return expectReady(ctx, e.k8s, audit) })
	audExpectNoWrites(t)

	audDisable(ctx, t, e.configured, "itest/siem")
	waitFor(t, "disabled device enabled again", func() error { return audExpectDevice(ctx, e.configured, "itest/siem/", want) })

	audDisable(ctx, t, e.configured, "itest/siem")
	mustWrite(ctx, t, e.configured, "sys/audit/itest/siem", map[string]any{"type": "http",
		"description": "itest siem forwarder",
		"options":     map[string]any{"uri": receiver.URL, "format": "json", "headers": audHeaders, "log_raw": "true"}})
	waitFor(t, "changed device reported", func() error {
		return audExpectReason(ctx, e.k8s, audit, commonv1alpha1.ReasonUnsupported, "cannot change an audit device in place")
	})
	audExpectNoWrites(t)

	audDisable(ctx, t, e.configured, "itest/siem")
	waitFor(t, "device enabled from the spec", func() error { return audExpectDevice(ctx, e.configured, "itest/siem/", want) })
	waitFor(t, "audit ready again", func() error { return expectReady(ctx, e.k8s, audit) })

	if err := e.k8s.Delete(ctx, audit); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "audit device disabled", func() error { return audExpectNoDevice(ctx, e.configured, "itest/siem/") })
	waitFor(t, "audit resource gone", func() error { return expectGone(ctx, e.k8s, audit) })
}

func testAudAdoptDeclared(ctx context.Context, t *testing.T, e audEnv) {
	declaredLog := filepath.Join(e.dir, "declared.log")
	audit := &sysv1alpha1.Audit{
		ObjectMeta: metav1.ObjectMeta{Name: "declared", Namespace: namespace},
		Spec: sysv1alpha1.AuditSpec{
			ResourceSpec: commonv1alpha1.ResourceSpec{
				ConnectionRef:  commonv1alpha1.ConnectionReference{Name: audConfigured},
				CreationPolicy: commonv1alpha1.CreationPolicyAdopt,
			},
			Type:        "file",
			Description: "declared in config",
			Options:     map[string]sysv1alpha1.AuditOptionValue{"file_path": sysv1alpha1.AuditOptionValue(declaredLog)},
		},
	}
	before := audWrites(t)
	mustCreate(ctx, t, e.k8s, audit)
	waitFor(t, "declared device adopted", func() error { return expectReady(ctx, e.k8s, audit) })
	if audit.Status.Acquired != commonv1alpha1.AcquiredAdopted {
		t.Errorf("acquired = %q, want Adopted", audit.Status.Acquired)
	}
	if got := audWrites(t); got != before {
		t.Errorf("adoption wrote %v times", got-before)
	}

	patch := client.MergeFrom(audit.DeepCopy())
	audit.Spec.DeletionPolicy = commonv1alpha1.DeletionPolicyDelete
	if err := e.k8s.Patch(ctx, audit, patch); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "finalizer added", func() error {
		if err := e.k8s.Get(ctx, client.ObjectKeyFromObject(audit), audit); err != nil {
			return err
		}
		if len(audit.Finalizers) == 0 {
			return fmt.Errorf("no finalizer yet")
		}
		return nil
	})
	if err := e.k8s.Delete(ctx, audit); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "delete guarded", func() error {
		return audExpectReason(ctx, e.k8s, audit, commonv1alpha1.ReasonDeleteGuarded, commonv1alpha1.AnnotationAllowDestructiveDelete)
	})
	patch = client.MergeFrom(audit.DeepCopy())
	audit.Annotations = map[string]string{commonv1alpha1.AnnotationAllowDestructiveDelete: "true"}
	if err := e.k8s.Patch(ctx, audit, patch); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "config-managed device kept", func() error {
		return audExpectReason(ctx, e.k8s, audit, commonv1alpha1.ReasonCleanupBlocked, "deletionPolicy to Orphan")
	})
	patch = client.MergeFrom(audit.DeepCopy())
	audit.Spec.DeletionPolicy = commonv1alpha1.DeletionPolicyOrphan
	if err := e.k8s.Patch(ctx, audit, patch); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "orphaned resource gone", func() error { return expectGone(ctx, e.k8s, audit) })
	waitFor(t, "declared device still enabled", func() error {
		return audExpectDevice(ctx, e.configured, "declared/", map[string]any{"type": "file"})
	})
}

func testAudRefused(ctx context.Context, t *testing.T, e audEnv) {
	audit := &sysv1alpha1.Audit{
		ObjectMeta: metav1.ObjectMeta{Name: "refused", Namespace: namespace},
		Spec: sysv1alpha1.AuditSpec{
			ResourceSpec: audSpec(audDev, ""),
			Type:         "file",
			Options:      map[string]sysv1alpha1.AuditOptionValue{"file_path": "discard"},
		},
	}
	mustCreate(ctx, t, e.k8s, audit)
	waitFor(t, "api creation reported unsupported", func() error {
		return audExpectReason(ctx, e.k8s, audit, commonv1alpha1.ReasonUnsupported, "unsafe_allow_api_audit_creation")
	})
	if err := audExpectNoDevice(ctx, e.dev, "refused/"); err != nil {
		t.Error(err)
	}
}

func audExpectDevice(ctx context.Context, bao openbao.Client, path string, want map[string]any) error {
	table, err := bao.Read(ctx, "sys/audit")
	if err != nil {
		return err
	}
	device, ok := table[path].(map[string]any)
	if !ok {
		return fmt.Errorf("audit device %s not enabled", path)
	}
	for key, value := range want {
		if fmt.Sprint(device[key]) != fmt.Sprint(value) {
			return fmt.Errorf("audit device %s: %s differs", path, key)
		}
	}
	return nil
}

func audExpectNoDevice(ctx context.Context, bao openbao.Client, path string) error {
	table, err := bao.Read(ctx, "sys/audit")
	if err != nil {
		return err
	}
	if _, ok := table[path]; ok {
		return fmt.Errorf("audit device %s still enabled", path)
	}
	return nil
}

func audExpectReason(ctx context.Context, k8s client.Client, obj engine.Object, reason, message string) error {
	if err := k8s.Get(ctx, client.ObjectKeyFromObject(obj), obj); err != nil {
		return err
	}
	synced := meta.FindStatusCondition(obj.GetResourceStatus().Conditions, commonv1alpha1.ConditionSynced)
	if synced == nil || synced.Reason != reason || !strings.Contains(synced.Message, message) {
		return fmt.Errorf("synced = %+v, want reason %s with %q", synced, reason, message)
	}
	return nil
}

func audDisable(ctx context.Context, t *testing.T, bao openbao.Client, path string) {
	t.Helper()
	if err := bao.Delete(ctx, "sys/audit/"+path); err != nil {
		t.Fatalf("disable %s: %v", path, err)
	}
}

func audExpectNoWrites(t *testing.T) {
	t.Helper()
	before := audWrites(t)
	stayFor(t, "Audit writes nothing", 3*driftInterval, func() error {
		if got := audWrites(t); got != before {
			return fmt.Errorf("writes went from %v to %v", before, got)
		}
		return nil
	})
}

func audWrites(t *testing.T) float64 {
	t.Helper()
	families, err := metrics.Registry.Gather()
	if err != nil {
		t.Fatal(err)
	}
	for _, family := range families {
		if family.GetName() != "obco_writes_total" {
			continue
		}
		for _, m := range family.GetMetric() {
			for _, label := range m.GetLabel() {
				if label.GetName() == "kind" && label.GetValue() == "Audit" {
					return m.GetCounter().GetValue()
				}
			}
		}
	}
	return 0
}

func audSetup(ctx context.Context, t *testing.T) audEnv {
	t.Helper()
	ctrl.SetLogger(zap.New(zap.WriteTo(os.Stderr), zap.UseDevMode(true)))
	root := repoRoot(t)

	baoBin := testenv.RequireBao(t)
	assets := testenv.RequireAssets(t, root)

	dir := t.TempDir()
	configuredAddr, token := audStartConfigured(t, baoBin, dir)
	configured := audClient(ctx, t, "itest-audit-configured", configuredAddr, token)
	dev := audClient(ctx, t, "itest-audit-dev", startBao(t, baoBin), rootToken)

	testEnv := &envtest.Environment{
		BinaryAssetsDirectory: assets,
		CRDInstallOptions: envtest.CRDInstallOptions{
			Paths:              []string{filepath.Join(root, "config", "crd", "bases", "sys.obco.dev_audits.yaml")},
			ErrorIfPathMissing: true,
		},
	}
	cfg, err := testEnv.Start()
	if err != nil {
		t.Fatalf("envtest: %v", err)
	}
	t.Cleanup(func() {
		if err := testEnv.Stop(); err != nil {
			t.Logf("stop envtest: %v", err)
		}
	})

	scheme := runtime.NewScheme()
	for _, add := range []func(*runtime.Scheme) error{clientgoscheme.AddToScheme, sysv1alpha1.AddToScheme} {
		if err := add(scheme); err != nil {
			t.Fatal(err)
		}
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
	conns := audConnections{audConfigured: configured, audDev: dev}
	if err := sys.SetupAudit(mgr, conns, itestEngineOptions()); err != nil {
		t.Fatal(err)
	}

	mgrCtx, stopMgr := context.WithCancel(ctx)
	done := make(chan error, 1)
	go func() { done <- mgr.Start(mgrCtx) }()
	t.Cleanup(func() {
		stopMgr()
		if err := <-done; err != nil {
			t.Logf("manager: %v", err)
		}
	})
	return audEnv{k8s: mgr.GetClient(), configured: configured, dev: dev, dir: dir}
}

func audClient(ctx context.Context, t *testing.T, key, addr, token string) openbao.Client {
	t.Helper()
	bao, err := openbao.NewPool().Client(ctx, key, openbao.Config{
		Address: addr,
		Timeout: 10 * time.Second,
		Auth: openbao.TokenAuth{
			CredentialKey: key,
			Token:         func(context.Context) (string, error) { return token, nil },
		},
	})
	if err != nil {
		t.Fatalf("openbao client: %v", err)
	}
	return bao
}

// audStartConfigured starts a non-dev server with in-memory storage whose configuration allows API audit creation
// and declares the file device "declared", then initialises and unseals it. It returns the address and root token.
func audStartConfigured(t *testing.T, bin, dir string) (string, string) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	listen := listener.Addr().String()
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	config := fmt.Sprintf(`storage "inmem" {}
listener "tcp" {
  address     = %q
  tls_disable = true
}
disable_mlock                   = true
unsafe_allow_api_audit_creation = true
audit "file" "declared" {
  description = "declared in config"
  options {
    file_path = %q
  }
}
`, listen, filepath.Join(dir, "declared.log"))
	configPath := filepath.Join(dir, "bao.hcl")
	if err := os.WriteFile(configPath, []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(bin, "server", "-config="+configPath)
	if err := cmd.Start(); err != nil {
		t.Fatalf("start bao: %v", err)
	}
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	})

	addr := "http://" + listen
	waitFor(t, "bao listening", func() error {
		resp, err := http.Get(addr + "/v1/sys/health")
		if err != nil {
			return err
		}
		return resp.Body.Close()
	})
	var initResp struct {
		Keys      []string `json:"keys"`
		RootToken string   `json:"root_token"`
	}
	audPut(t, addr+"/v1/sys/init", map[string]any{"secret_shares": 1, "secret_threshold": 1}, &initResp)
	if len(initResp.Keys) != 1 || initResp.RootToken == "" {
		t.Fatal("init returned no key or root token")
	}
	audPut(t, addr+"/v1/sys/unseal", map[string]any{"key": initResp.Keys[0]}, nil)
	waitFor(t, "bao unsealed and active", func() error {
		resp, err := http.Get(addr + "/v1/sys/health")
		if err != nil {
			return err
		}
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			return fmt.Errorf("health status %d", resp.StatusCode)
		}
		return nil
	})
	return addr, initResp.RootToken
}

func audPut(t *testing.T, url string, body map[string]any, out any) {
	t.Helper()
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	req, err := http.NewRequest(http.MethodPut, url, bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("PUT %s: status %d", url, resp.StatusCode)
	}
	if out != nil {
		if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
			t.Fatal(err)
		}
	}
}
