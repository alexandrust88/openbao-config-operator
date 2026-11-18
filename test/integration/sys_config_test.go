//go:build integration

package integration

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net"
	"net/http"
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
	"sigs.k8s.io/controller-runtime/pkg/config"
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

const sysCfgWorkflow = "flow \"health\" {\n  request \"read\" {\n    operation = \"read\"\n    path = \"sys/health\"\n  }\n}\n"

// TestIntegrationSysConfig runs the sys singletons, UIHeader, AuditRequestHeader, BaoWorkflow and Plugin against
// envtest and bao server -dev (inmem storage, with a plugin directory).
func TestIntegrationSysConfig(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	e, pluginDir := sysCfgSetup(ctx, t)

	t.Run("corsconfig", func(t *testing.T) { testSysCfgCORS(ctx, t, e) })
	t.Run("uiheader", func(t *testing.T) { testSysCfgUIHeader(ctx, t, e) })
	t.Run("logger", func(t *testing.T) { testSysCfgLogger(ctx, t, e) })
	t.Run("auditrequestheader", func(t *testing.T) { testSysCfgAuditHeader(ctx, t, e) })
	t.Run("quotaconfig", func(t *testing.T) { testSysCfgQuota(ctx, t, e) })
	t.Run("keyrotationconfig", func(t *testing.T) { testSysCfgKeyRotation(ctx, t, e) })
	t.Run("raftautopilot", func(t *testing.T) { testSysCfgRaftAutopilot(ctx, t, e) })
	t.Run("workflow", func(t *testing.T) { testSysCfgWorkflow(ctx, t, e) })
	t.Run("plugin", func(t *testing.T) { testSysCfgPlugin(ctx, t, e, pluginDir) })
}

func sysCfgSpec(policy commonv1alpha1.DeletionPolicy) commonv1alpha1.ResourceSpec {
	return commonv1alpha1.ResourceSpec{
		ConnectionRef:  commonv1alpha1.ConnectionReference{Name: "dev"},
		DeletionPolicy: policy,
	}
}

func testSysCfgCORS(ctx context.Context, t *testing.T, e env) {
	const path = "sys/config/cors"
	cors := &sysv1alpha1.CORSConfig{
		ObjectMeta: metav1.ObjectMeta{Name: "itest-cors", Namespace: namespace},
		Spec: sysv1alpha1.CORSConfigSpec{
			ResourceSpec:     sysCfgSpec(commonv1alpha1.DeletionPolicyDelete),
			AllowedOrigins:   []string{"https://ui.example.com"},
			AllowedHeaders:   []string{"X-Custom"},
			AllowCredentials: true,
		},
	}
	mustCreate(ctx, t, e.k8s, cors)
	want := map[string]any{"enabled": true, "allowed_origins": []string{"https://ui.example.com"}, "allow_credentials": true}
	waitFor(t, "cors enabled", func() error { return sysCfgExpect(ctx, e.bao, path, want) })
	waitFor(t, "cors ready", func() error { return expectReady(ctx, e.k8s, cors) })
	sysCfgExpectNoWrites(t, "CORSConfig")

	mustWrite(ctx, t, e.bao, path, map[string]any{"allowed_origins": []string{"*"}})
	waitFor(t, "cors drift repaired", func() error { return sysCfgExpect(ctx, e.bao, path, want) })
	waitFor(t, "custom header kept", func() error {
		data, err := e.bao.Read(ctx, path)
		if err != nil {
			return err
		}
		if !strings.Contains(fmt.Sprint(data["allowed_headers"]), "X-Custom") {
			return fmt.Errorf("allowed_headers %v lacks X-Custom", data["allowed_headers"])
		}
		return nil
	})

	if err := e.k8s.Delete(ctx, cors); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "cors disabled", func() error { return sysCfgExpect(ctx, e.bao, path, map[string]any{"enabled": false}) })
	waitFor(t, "cors resource gone", func() error { return expectGone(ctx, e.k8s, cors) })
}

func testSysCfgUIHeader(ctx context.Context, t *testing.T, e env) {
	const path = "sys/config/ui/headers/X-Itest-Header"
	header := &sysv1alpha1.UIHeader{
		ObjectMeta: metav1.ObjectMeta{Name: "itest-header", Namespace: namespace},
		Spec: sysv1alpha1.UIHeaderSpec{
			ResourceSpec: sysCfgSpec(commonv1alpha1.DeletionPolicyDelete),
			Name:         "X-Itest-Header",
			Values:       []string{"one", "two"},
		},
	}
	mustCreate(ctx, t, e.k8s, header)
	waitFor(t, "ui header written", func() error { return sysCfgExpect(ctx, e.bao, path, map[string]any{"value": "one"}) })
	waitFor(t, "ui header ready", func() error { return expectReady(ctx, e.k8s, header) })
	sysCfgExpectNoWrites(t, "UIHeader")

	mustWrite(ctx, t, e.bao, path, map[string]any{"values": []string{"intruder"}})
	waitFor(t, "ui header repaired", func() error { return sysCfgExpect(ctx, e.bao, path, map[string]any{"value": "one"}) })

	if err := e.k8s.Delete(ctx, header); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "ui header deleted", func() error { return expectAbsent(ctx, e.bao, path) })
	waitFor(t, "ui header resource gone", func() error { return expectGone(ctx, e.k8s, header) })
}

func testSysCfgLogger(ctx context.Context, t *testing.T, e env) {
	const path = "sys/loggers/quotas"
	logger := &sysv1alpha1.Logger{
		ObjectMeta: metav1.ObjectMeta{Name: "quotas", Namespace: namespace},
		Spec:       sysv1alpha1.LoggerSpec{ResourceSpec: sysCfgSpec(""), Level: "error"},
	}
	mustCreate(ctx, t, e.k8s, logger)
	want := map[string]any{"quotas": "error"}
	waitFor(t, "logger level set", func() error { return sysCfgExpect(ctx, e.bao, path, want) })
	waitFor(t, "logger ready", func() error { return expectReady(ctx, e.k8s, logger) })
	sysCfgExpectNoWrites(t, "Logger")

	mustWrite(ctx, t, e.bao, path, map[string]any{"level": "trace"})
	waitFor(t, "logger level repaired", func() error { return sysCfgExpect(ctx, e.bao, path, want) })

	if err := e.k8s.Delete(ctx, logger); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "logger resource gone", func() error { return expectGone(ctx, e.k8s, logger) })
	stayFor(t, "orphaned logger keeps its level", driftInterval, func() error {
		return sysCfgExpect(ctx, e.bao, path, want)
	})
}

func testSysCfgAuditHeader(ctx context.Context, t *testing.T, e env) {
	const path = "sys/config/auditing/request-headers/X-Itest-Audit"
	header := &sysv1alpha1.AuditRequestHeader{
		ObjectMeta: metav1.ObjectMeta{Name: "itest-audit", Namespace: namespace},
		Spec: sysv1alpha1.AuditRequestHeaderSpec{
			ResourceSpec: sysCfgSpec(commonv1alpha1.DeletionPolicyDelete),
			Name:         "X-Itest-Audit",
			HMAC:         true,
		},
	}
	mustCreate(ctx, t, e.k8s, header)
	want := map[string]any{"X-Itest-Audit": map[string]any{"hmac": true}}
	waitFor(t, "audit header written", func() error { return sysCfgExpect(ctx, e.bao, path, want) })
	waitFor(t, "audit header ready", func() error { return expectReady(ctx, e.k8s, header) })
	sysCfgExpectNoWrites(t, "AuditRequestHeader")

	mustWrite(ctx, t, e.bao, path, map[string]any{"hmac": false})
	waitFor(t, "audit header repaired", func() error { return sysCfgExpect(ctx, e.bao, path, want) })

	if err := e.k8s.Delete(ctx, header); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "audit header deleted", func() error {
		_, err := e.bao.Read(ctx, path)
		if err == nil || !strings.Contains(err.Error(), "Could not find header") {
			return fmt.Errorf("read after delete: %v", err)
		}
		return nil
	})
	waitFor(t, "audit header resource gone", func() error { return expectGone(ctx, e.k8s, header) })
}

func testSysCfgQuota(ctx context.Context, t *testing.T, e env) {
	const path = "sys/quotas/config"
	quota := &sysv1alpha1.QuotaConfig{
		ObjectMeta: metav1.ObjectMeta{Name: "itest-quota", Namespace: namespace},
		Spec: sysv1alpha1.QuotaConfigSpec{
			ResourceSpec:                sysCfgSpec(commonv1alpha1.DeletionPolicyDelete),
			RateLimitExemptPaths:        []string{"sys/health", "sys/itest"},
			EnableRateLimitAuditLogging: true,
		},
	}
	mustCreate(ctx, t, e.k8s, quota)
	want := map[string]any{
		"rate_limit_exempt_paths":            []string{"sys/health", "sys/itest"},
		"enable_rate_limit_audit_logging":    true,
		"enable_rate_limit_response_headers": false,
	}
	waitFor(t, "quota config written", func() error { return sysCfgExpect(ctx, e.bao, path, want) })
	waitFor(t, "quota config ready", func() error { return expectReady(ctx, e.k8s, quota) })
	sysCfgExpectNoWrites(t, "QuotaConfig")

	mustWrite(ctx, t, e.bao, path, map[string]any{"rate_limit_exempt_paths": []string{"sys/health", "sys/itest"}})
	waitFor(t, "quota config repaired", func() error { return sysCfgExpect(ctx, e.bao, path, want) })

	if err := e.k8s.Delete(ctx, quota); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "quota config reset to defaults", func() error {
		return sysCfgExpect(ctx, e.bao, path, map[string]any{
			"enable_rate_limit_audit_logging": false,
			"rate_limit_exempt_paths": []string{
				"sys/generate-recovery-token/attempt", "sys/generate-recovery-token/update", "sys/generate-root/attempt",
				"sys/generate-root/update", "sys/health", "sys/seal-status", "sys/unseal",
			},
		})
	})
	waitFor(t, "quota config resource gone", func() error { return expectGone(ctx, e.k8s, quota) })
}

func testSysCfgKeyRotation(ctx context.Context, t *testing.T, e env) {
	const path = "sys/rotate/config"
	rotation := &sysv1alpha1.KeyRotationConfig{
		ObjectMeta: metav1.ObjectMeta{Name: "itest-rotation", Namespace: namespace},
		Spec: sysv1alpha1.KeyRotationConfigSpec{
			ResourceSpec:  sysCfgSpec(""),
			Enabled:       new(true),
			MaxOperations: 2_000_000,
			Interval:      "48h",
		},
	}
	mustCreate(ctx, t, e.k8s, rotation)
	want := map[string]any{"enabled": true, "max_operations": 2_000_000, "interval": "48h0m0s"}
	waitFor(t, "rotation config written", func() error { return sysCfgExpect(ctx, e.bao, path, want) })
	waitFor(t, "rotation config ready", func() error { return expectReady(ctx, e.k8s, rotation) })
	sysCfgExpectNoWrites(t, "KeyRotationConfig")

	mustWrite(ctx, t, e.bao, path, map[string]any{"interval": 0})
	waitFor(t, "rotation config repaired", func() error { return sysCfgExpect(ctx, e.bao, path, want) })

	if err := e.k8s.Delete(ctx, rotation); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "rotation config resource gone", func() error { return expectGone(ctx, e.k8s, rotation) })
	stayFor(t, "orphaned rotation config kept", driftInterval, func() error { return sysCfgExpect(ctx, e.bao, path, want) })
}

// testSysCfgRaftAutopilot checks the unsupported path: bao -dev uses inmem storage, which has no autopilot.
func testSysCfgRaftAutopilot(ctx context.Context, t *testing.T, e env) {
	autopilot := &sysv1alpha1.RaftAutopilot{
		ObjectMeta: metav1.ObjectMeta{Name: "itest-autopilot", Namespace: namespace},
		Spec: sysv1alpha1.RaftAutopilotSpec{
			ResourceSpec:       sysCfgSpec(""),
			CleanupDeadServers: new(true),
			MinQuorum:          new(int64(3)),
		},
	}
	mustCreate(ctx, t, e.k8s, autopilot)
	waitFor(t, "autopilot reports missing raft storage", func() error {
		if err := e.k8s.Get(ctx, client.ObjectKeyFromObject(autopilot), autopilot); err != nil {
			return err
		}
		c := meta.FindStatusCondition(autopilot.Status.Conditions, commonv1alpha1.ConditionSynced)
		if c == nil || c.Reason != commonv1alpha1.ReasonUnsupported || !strings.Contains(c.Message, "raft storage") {
			return fmt.Errorf("synced condition = %+v", c)
		}
		return nil
	})
	if err := e.k8s.Delete(ctx, autopilot); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "autopilot resource gone", func() error { return expectGone(ctx, e.k8s, autopilot) })
}

func testSysCfgWorkflow(ctx context.Context, t *testing.T, e env) {
	const path = "sys/workflows/manage/itest/health"
	workflow := &sysv1alpha1.BaoWorkflow{
		ObjectMeta: metav1.ObjectMeta{Name: "itest-workflow", Namespace: namespace},
		Spec: sysv1alpha1.BaoWorkflowSpec{
			ResourceSpec: sysCfgSpec(commonv1alpha1.DeletionPolicyDelete),
			Path:         "itest/health",
			Description:  "reads health",
			Workflow:     sysCfgWorkflow,
			CASRequired:  true,
		},
	}
	mustCreate(ctx, t, e.k8s, workflow)
	want := map[string]any{"workflow": sysCfgWorkflow, "description": "reads health", "cas_required": true}
	waitFor(t, "workflow written", func() error { return sysCfgExpect(ctx, e.bao, path, want) })
	waitFor(t, "workflow ready", func() error { return expectReady(ctx, e.k8s, workflow) })
	sysCfgExpectNoWrites(t, "BaoWorkflow")

	sysCfgWriteWorkflow(ctx, t, e.bao, path, map[string]any{"workflow": sysCfgWorkflow, "description": "tampered", "cas_required": true})
	waitFor(t, "workflow repaired despite cas_required", func() error { return sysCfgExpect(ctx, e.bao, path, want) })

	reformatted := strings.ReplaceAll(sysCfgWorkflow, "\n  ", "\n\t")
	sysCfgWriteWorkflow(ctx, t, e.bao, path, map[string]any{"workflow": reformatted, "description": "reads health", "cas_required": true})
	stayFor(t, "whitespace-only edit kept", 3*driftInterval, func() error {
		return sysCfgExpect(ctx, e.bao, path, map[string]any{"workflow": reformatted})
	})

	if err := e.k8s.Delete(ctx, workflow); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "workflow deleted", func() error { return expectAbsent(ctx, e.bao, path) })
	waitFor(t, "workflow resource gone", func() error { return expectGone(ctx, e.k8s, workflow) })
}

func testSysCfgPlugin(ctx context.Context, t *testing.T, e env, pluginDir string) {
	binary := []byte("#!/bin/sh\nexit 1\n")
	if err := os.WriteFile(filepath.Join(pluginDir, "itest-plugin"), binary, 0o755); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(binary)
	checksum := hex.EncodeToString(sum[:])

	env := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "itest-plugin-env", Namespace: namespace},
		Data:       map[string][]byte{"token": []byte("first")},
	}
	mustCreate(ctx, t, e.k8s, env)

	const path = "sys/plugins/catalog/secret/itest-fake"
	plugin := &sysv1alpha1.Plugin{
		ObjectMeta: metav1.ObjectMeta{Name: "itest-fake", Namespace: namespace},
		Spec: sysv1alpha1.PluginSpec{
			ResourceSpec: sysCfgSpec(commonv1alpha1.DeletionPolicyDelete),
			Type:         "secret",
			Command:      "itest-plugin",
			SHA256:       checksum,
			Args:         []string{"--one"},
			Env: []sysv1alpha1.PluginEnvVar{
				{Name: "TOKEN", SecretKeyRef: commonv1alpha1.SecretKeySelector{Name: env.Name, Key: "token"}},
			},
		},
	}
	mustCreate(ctx, t, e.k8s, plugin)
	want := map[string]any{"command": "itest-plugin", "sha256": checksum, "args": []string{"--one"}, "builtin": false}
	waitFor(t, "plugin registered", func() error { return sysCfgExpect(ctx, e.bao, path, want) })
	waitFor(t, "plugin ready", func() error { return expectReady(ctx, e.k8s, plugin) })
	sysCfgExpectNoWrites(t, "Plugin")

	mustWrite(ctx, t, e.bao, path, map[string]any{"command": "itest-plugin", "sha256": checksum, "args": []string{"--two"}})
	waitFor(t, "plugin repaired", func() error { return sysCfgExpect(ctx, e.bao, path, want) })

	before := sysCfgWrites(t, "Plugin")
	env.Data["token"] = []byte("second")
	if err := e.k8s.Update(ctx, env); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "rotated env secret re-registers the plugin", func() error {
		if got := sysCfgWrites(t, "Plugin"); got <= before {
			return fmt.Errorf("plugin writes %v, want more than %v", got, before)
		}
		return nil
	})

	versioned := &sysv1alpha1.Plugin{
		ObjectMeta: metav1.ObjectMeta{Name: "itest-fake-v", Namespace: namespace},
		Spec: sysv1alpha1.PluginSpec{
			ResourceSpec: sysCfgSpec(""),
			Type:         "secret",
			Command:      "itest-plugin",
			SHA256:       checksum,
			Version:      "1.2.3",
		},
	}
	mustCreate(ctx, t, e.k8s, versioned)
	waitFor(t, "versioned plugin registered", func() error { return sysCfgExpectCatalog(ctx, e.bao, "itest-fake-v", "v1.2.3") })
	waitFor(t, "versioned plugin ready", func() error { return expectReady(ctx, e.k8s, versioned) })
	sysCfgExpectNoWrites(t, "Plugin")

	if err := e.k8s.Delete(ctx, plugin); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "plugin deregistered", func() error { return expectAbsent(ctx, e.bao, path) })
	waitFor(t, "plugin resource gone", func() error { return expectGone(ctx, e.k8s, plugin) })
	if err := e.k8s.Delete(ctx, versioned); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "versioned plugin resource gone", func() error { return expectGone(ctx, e.k8s, versioned) })
	if err := sysCfgExpectCatalog(ctx, e.bao, "itest-fake-v", "v1.2.3"); err != nil {
		t.Errorf("orphaned versioned plugin: %v", err)
	}
}

// sysCfgExpect compares the keys of want with a read of path, as the engine compares them.
func sysCfgExpect(ctx context.Context, bao openbao.Client, path string, want map[string]any) error {
	data, err := bao.Read(ctx, path)
	if err != nil {
		return err
	}
	if differing := engine.Diff(want, data, engine.FieldSpec{}); len(differing) > 0 {
		return fmt.Errorf("%s: keys %v differ", path, differing)
	}
	return nil
}

func sysCfgExpectCatalog(ctx context.Context, bao openbao.Client, name, version string) error {
	data, err := bao.Read(ctx, "sys/plugins/catalog")
	if err != nil {
		return err
	}
	detailed, _ := data["detailed"].([]any)
	for _, raw := range detailed {
		entry, _ := raw.(map[string]any)
		if entry["name"] == name && entry["version"] == version {
			return nil
		}
	}
	return fmt.Errorf("plugin %s %s not in the catalog", name, version)
}

// sysCfgWriteWorkflow updates a workflow out of band with the current version as check-and-set value.
func sysCfgWriteWorkflow(ctx context.Context, t *testing.T, bao openbao.Client, path string, data map[string]any) {
	t.Helper()
	current, err := bao.Read(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	data["cas"] = current["version"]
	mustWrite(ctx, t, bao, path, data)
}

func sysCfgExpectNoWrites(t *testing.T, kind string) {
	t.Helper()
	before := sysCfgWrites(t, kind)
	stayFor(t, kind+" steady state writes nothing", 3*driftInterval, func() error {
		if got := sysCfgWrites(t, kind); got != before {
			return fmt.Errorf("writes went from %v to %v", before, got)
		}
		return nil
	})
}

func sysCfgWrites(t *testing.T, kind string) float64 {
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
				if label.GetName() == "kind" && label.GetValue() == kind {
					return m.GetCounter().GetValue()
				}
			}
		}
	}
	return 0
}

// sysCfgSetup starts bao -dev with a plugin directory, envtest with the sys config CRDs and a manager running
// their controllers. It returns the plugin directory with symlinks resolved, as OpenBao compares it.
func sysCfgSetup(ctx context.Context, t *testing.T) (env, string) {
	t.Helper()
	ctrl.SetLogger(zap.New(zap.WriteTo(os.Stderr), zap.UseDevMode(true)))
	root := repoRoot(t)

	baoBin := testenv.RequireBao(t)
	assets := testenv.RequireAssets(t, root)

	pluginDir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	addr := sysCfgStartBao(t, baoBin, pluginDir)
	bao, err := openbao.NewPool().Client(ctx, "itest-sys-config", openbao.Config{
		Address: addr,
		Timeout: 10 * time.Second,
		Auth: openbao.TokenAuth{
			CredentialKey: "itest-sys-config",
			Token:         func(context.Context) (string, error) { return rootToken, nil },
		},
	})
	if err != nil {
		t.Fatalf("openbao client: %v", err)
	}

	crds := make([]string, 0, 9)
	for _, name := range []string{"corsconfigs", "uiheaders", "loggers", "auditrequestheaders", "quotaconfigs",
		"keyrotationconfigs", "raftautopilots", "baoworkflows", "plugins"} {
		crds = append(crds, filepath.Join(root, "config", "crd", "bases", "sys.obco.dev_"+name+".yaml"))
	}
	testEnv := &envtest.Environment{
		BinaryAssetsDirectory: assets,
		CRDInstallOptions:     envtest.CRDInstallOptions{Paths: crds, ErrorIfPathMissing: true},
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
		Controller:             config.Controller{SkipNameValidation: new(true)},
	})
	if err != nil {
		t.Fatal(err)
	}
	conns := staticConnections{client: bao}
	opts := itestEngineOptions()
	for _, setupKind := range []func(ctrl.Manager, engine.Connections, engine.Options) error{
		sys.SetupCORSConfig, sys.SetupUIHeader, sys.SetupLogger, sys.SetupAuditRequestHeader, sys.SetupQuotaConfig,
		sys.SetupKeyRotationConfig, sys.SetupRaftAutopilot, sys.SetupBaoWorkflow, sys.SetupPlugin,
	} {
		if err := setupKind(mgr, conns, opts); err != nil {
			t.Fatal(err)
		}
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
	return env{k8s: mgr.GetClient(), bao: bao}, pluginDir
}

// sysCfgStartBao is startBao with -dev-plugin-dir. The directory must be empty at start: the dev server mounts
// every plugin it finds there and exits when one fails.
func sysCfgStartBao(t *testing.T, bin, pluginDir string) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	listen := listener.Addr().String()
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}

	cmd := exec.Command(bin, "server", "-dev", "-dev-root-token-id="+rootToken, "-dev-listen-address="+listen,
		"-dev-plugin-dir="+pluginDir)
	if err := cmd.Start(); err != nil {
		t.Fatalf("start bao: %v", err)
	}
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	})

	addr := "http://" + listen
	waitFor(t, "bao healthy", func() error {
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
	return addr
}
