//go:build integration

// Package integration runs the Policy and OpenBaoEndpoint controllers against envtest and a real `bao server -dev`.
package integration

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/config"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"

	commonv1alpha1 "github.com/alexandrust88/openbao-config-operator/api/common/v1alpha1"
	corev1alpha1 "github.com/alexandrust88/openbao-config-operator/api/core/v1alpha1"
	sysv1alpha1 "github.com/alexandrust88/openbao-config-operator/api/sys/v1alpha1"
	"github.com/alexandrust88/openbao-config-operator/internal/controller/core"
	"github.com/alexandrust88/openbao-config-operator/internal/controller/sys"
	"github.com/alexandrust88/openbao-config-operator/internal/engine"
	"github.com/alexandrust88/openbao-config-operator/internal/openbao"
	"github.com/alexandrust88/openbao-config-operator/test/testenv"
)

const (
	rootToken     = "root"
	namespace     = "default"
	driftInterval = 2 * time.Second
	timeout       = 30 * time.Second
)

type staticConnections struct {
	client openbao.Client
}

func (s staticConnections) For(context.Context, string, commonv1alpha1.ConnectionReference) (openbao.Client, error) {
	return s.client, nil
}

// itestEngineOptions checks drift of every tier each driftInterval and lifts the write budget, which the drift
// repairs of a scenario would otherwise exhaust.
func itestEngineOptions() engine.Options {
	return engine.Options{DriftIntervalCritical: driftInterval, DriftIntervalStandard: driftInterval, WriteBudgetPerMinute: -1}
}

type env struct {
	k8s client.Client
	bao openbao.Client
}

func TestIntegration(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	e := setup(ctx, t)

	t.Run("policy", func(t *testing.T) { testPolicy(ctx, t, e) })
	t.Run("endpoint", func(t *testing.T) { testEndpoint(ctx, t, e) })
}

func testPolicy(ctx context.Context, t *testing.T, e env) {
	const path = "sys/policies/acl/itest-reader"
	text := "path \"secret/*\" {\n  capabilities = [\"read\"]\n}\n"
	policy := &sysv1alpha1.Policy{
		ObjectMeta: metav1.ObjectMeta{Name: "itest-reader", Namespace: namespace},
		Spec: sysv1alpha1.PolicySpec{
			ResourceSpec: commonv1alpha1.ResourceSpec{
				ConnectionRef:  commonv1alpha1.ConnectionReference{Name: "dev"},
				DeletionPolicy: commonv1alpha1.DeletionPolicyDelete,
			},
			Policy: text,
		},
	}
	mustCreate(ctx, t, e.k8s, policy)

	waitFor(t, "policy created", func() error { return expectPolicy(ctx, e.bao, path, text) })
	waitFor(t, "policy ready", func() error { return expectReady(ctx, e.k8s, policy) })

	for field, mutate := range map[string]func(*sysv1alpha1.PolicySpec){
		"namespace":     func(s *sysv1alpha1.PolicySpec) { s.Namespace = "other" },
		"name":          func(s *sysv1alpha1.PolicySpec) { s.Name = "other" },
		"connectionRef": func(s *sysv1alpha1.PolicySpec) { s.ConnectionRef.Name = "other" },
	} {
		changed := &sysv1alpha1.Policy{}
		if err := e.k8s.Get(ctx, client.ObjectKeyFromObject(policy), changed); err != nil {
			t.Fatal(err)
		}
		mutate(&changed.Spec)
		if err := e.k8s.Update(ctx, changed); !apierrors.IsInvalid(err) {
			t.Errorf("update of spec.%s: got %v, want it rejected as immutable", field, err)
		}
	}

	tampered := "path \"secret/*\" {\n  capabilities = [\"read\", \"delete\"]\n}\n"
	mustWrite(ctx, t, e.bao, path, map[string]any{"policy": tampered})
	waitFor(t, "out-of-band edit repaired", func() error { return expectPolicy(ctx, e.bao, path, text) })

	reformatted := "path \"secret/*\" {   \r\n\tcapabilities = [\"read\"]\r\n}"
	mustWrite(ctx, t, e.bao, path, map[string]any{"policy": reformatted})
	stayFor(t, "whitespace-only edit kept", 3*driftInterval, func() error { return expectPolicy(ctx, e.bao, path, reformatted) })

	if err := e.k8s.Delete(ctx, policy); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "policy deleted", func() error { return expectAbsent(ctx, e.bao, path) })
	waitFor(t, "policy resource gone", func() error { return expectGone(ctx, e.k8s, policy) })
}

func testEndpoint(ctx context.Context, t *testing.T, e env) {
	const path = "itest-kv/app"
	mustWrite(ctx, t, e.bao, "sys/mounts/itest-kv", map[string]any{"type": "kv"})

	mustCreate(ctx, t, e.k8s, &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "app-creds", Namespace: namespace},
		Data:       map[string][]byte{"payload": []byte(`{"password":"first"}`)},
	})
	endpoint := &corev1alpha1.OpenBaoEndpoint{
		ObjectMeta: metav1.ObjectMeta{Name: "itest-app", Namespace: namespace,
			Annotations: map[string]string{commonv1alpha1.AnnotationAllowDestructiveDelete: "true"}},
		Spec: corev1alpha1.OpenBaoEndpointSpec{
			ResourceSpec: commonv1alpha1.ResourceSpec{
				ConnectionRef:  commonv1alpha1.ConnectionReference{Name: "dev"},
				DeletionPolicy: commonv1alpha1.DeletionPolicyDelete,
			},
			Path:          path,
			Data:          &runtime.RawExtension{Raw: []byte(`{"user":"app","ttl":"1h"}`)},
			DataSecretRef: &commonv1alpha1.SecretKeySelector{Name: "app-creds", Key: "payload"},
		},
	}
	mustCreate(ctx, t, e.k8s, endpoint)

	want := map[string]any{"user": "app", "ttl": "1h", "password": "first"}
	waitFor(t, "endpoint written", func() error { return expectData(ctx, e.bao, path, want) })
	waitFor(t, "endpoint ready", func() error { return expectReady(ctx, e.k8s, endpoint) })

	mustWrite(ctx, t, e.bao, path, map[string]any{"user": "intruder", "ttl": "1h", "password": "first"})
	waitFor(t, "endpoint drift repaired", func() error { return expectData(ctx, e.bao, path, want) })

	if err := e.k8s.Delete(ctx, endpoint); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "endpoint deleted", func() error { return expectAbsent(ctx, e.bao, path) })
	waitFor(t, "endpoint resource gone", func() error { return expectGone(ctx, e.k8s, endpoint) })
}

func setup(ctx context.Context, t *testing.T) env {
	t.Helper()
	ctrl.SetLogger(zap.New(zap.WriteTo(os.Stderr), zap.UseDevMode(true)))
	root := repoRoot(t)

	baoBin := testenv.RequireBao(t)
	assets := testenv.RequireAssets(t, root)

	addr := startBao(t, baoBin)
	bao, err := openbao.NewPool().Client(ctx, "itest", openbao.Config{
		Address: addr,
		Timeout: 10 * time.Second,
		Auth: openbao.TokenAuth{
			CredentialKey: "itest",
			Token:         func(context.Context) (string, error) { return rootToken, nil },
		},
	})
	if err != nil {
		t.Fatalf("openbao client: %v", err)
	}

	testEnv := &envtest.Environment{
		BinaryAssetsDirectory: assets,
		CRDInstallOptions: envtest.CRDInstallOptions{
			Paths: []string{
				filepath.Join(root, "config", "crd", "bases", "sys.openbaoconfig.io_policies.yaml"),
				filepath.Join(root, "config", "crd", "bases", "sys.openbaoconfig.io_authmounts.yaml"),
				filepath.Join(root, "config", "crd", "bases", "core.openbaoconfig.io_openbaoendpoints.yaml"),
			},
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
	for _, add := range []func(*runtime.Scheme) error{clientgoscheme.AddToScheme, sysv1alpha1.AddToScheme, corev1alpha1.AddToScheme} {
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
	if err := sys.SetupPolicy(mgr, conns, opts); err != nil {
		t.Fatal(err)
	}
	if err := core.SetupOpenBaoEndpoint(mgr, conns, opts); err != nil {
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
	return env{k8s: mgr.GetClient(), bao: bao}
}

func startBao(t *testing.T, bin string) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	listen := listener.Addr().String()
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}

	cmd := exec.Command(bin, "server", "-dev", "-dev-root-token-id="+rootToken, "-dev-listen-address="+listen)
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

func repoRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	return root
}

func expectPolicy(ctx context.Context, bao openbao.Client, path, want string) error {
	data, err := bao.Read(ctx, path)
	if err != nil {
		return err
	}
	if got := data["policy"]; got != want {
		return fmt.Errorf("policy = %q, want %q", got, want)
	}
	return nil
}

func expectData(ctx context.Context, bao openbao.Client, path string, want map[string]any) error {
	data, err := bao.Read(ctx, path)
	if err != nil {
		return err
	}
	for key, value := range want {
		if data[key] != value {
			return fmt.Errorf("key %s differs", key)
		}
	}
	return nil
}

func expectAbsent(ctx context.Context, bao openbao.Client, path string) error {
	_, err := bao.Read(ctx, path)
	if errors.Is(err, openbao.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	return fmt.Errorf("%s still exists", path)
}

func expectReady(ctx context.Context, k8s client.Client, obj engine.Object) error {
	if err := k8s.Get(ctx, client.ObjectKeyFromObject(obj), obj); err != nil {
		return err
	}
	if !meta.IsStatusConditionTrue(obj.GetResourceStatus().Conditions, commonv1alpha1.ConditionReady) {
		return fmt.Errorf("not ready: %+v", obj.GetResourceStatus().Conditions)
	}
	return nil
}

func expectGone(ctx context.Context, k8s client.Client, obj client.Object) error {
	err := k8s.Get(ctx, client.ObjectKeyFromObject(obj), obj)
	if client.IgnoreNotFound(err) != nil {
		return err
	}
	if err == nil {
		return fmt.Errorf("%s still exists", obj.GetName())
	}
	return nil
}

func mustCreate(ctx context.Context, t *testing.T, k8s client.Client, obj client.Object) {
	t.Helper()
	if err := k8s.Create(ctx, obj); err != nil {
		t.Fatalf("create %s: %v", obj.GetName(), err)
	}
}

func mustWrite(ctx context.Context, t *testing.T, bao openbao.Client, path string, data map[string]any) {
	t.Helper()
	if _, err := bao.Write(ctx, path, data); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func waitFor(t *testing.T, what string, check func() error) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		err := check()
		if err == nil {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("%s: %v", what, err)
		}
		time.Sleep(250 * time.Millisecond)
	}
}

func stayFor(t *testing.T, what string, period time.Duration, check func() error) {
	t.Helper()
	deadline := time.Now().Add(period)
	for time.Now().Before(deadline) {
		if err := check(); err != nil {
			t.Fatalf("%s: %v", what, err)
		}
		time.Sleep(250 * time.Millisecond)
	}
}
