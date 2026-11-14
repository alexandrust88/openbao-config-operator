//go:build integration

package integration

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/config"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"

	commonv1alpha1 "github.com/alexandrust88/openbao-config-operator/api/common/v1alpha1"
	sysv1alpha1 "github.com/alexandrust88/openbao-config-operator/api/sys/v1alpha1"
	"github.com/alexandrust88/openbao-config-operator/internal/controller/sys"
	"github.com/alexandrust88/openbao-config-operator/internal/engine"
	"github.com/alexandrust88/openbao-config-operator/test/testenv"
)

const sysRaftPath = "sys/storage/raft/autopilot/configuration"

// TestIntegrationSysRaft runs RaftAutopilot against a one-node OpenBao server on raft storage: omitted fields at
// their defaults, drift repair, min_quorum 0 without a write loop, and the resetting delete.
func TestIntegrationSysRaft(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	root := repoRoot(t)
	baoBin := testenv.RequireBao(t)
	assets := testenv.RequireAssets(t, root)
	addr, token := sysRaftStart(t, baoBin, t.TempDir())
	bao := audClient(ctx, t, "itest-sys-raft", addr, token)
	k8s := sysRaftManager(ctx, t, root, assets, staticConnections{client: bao})

	autopilot := &sysv1alpha1.RaftAutopilot{
		ObjectMeta: metav1.ObjectMeta{Name: "itest-raft", Namespace: namespace},
		Spec: sysv1alpha1.RaftAutopilotSpec{
			ResourceSpec:    sysCfgSpec(commonv1alpha1.DeletionPolicyDelete),
			MaxTrailingLogs: new(int64(500)),
		},
	}
	mustCreate(ctx, t, k8s, autopilot)
	defaults := map[string]any{
		"cleanup_dead_servers": false, "last_contact_threshold": "10s", "dead_server_last_contact_threshold": "24h0m0s",
		"max_trailing_logs": 500, "min_quorum": 0, "server_stabilization_time": "10s",
	}
	waitFor(t, "autopilot written", func() error { return sysCfgExpect(ctx, bao, sysRaftPath, defaults) })
	waitFor(t, "autopilot ready", func() error { return expectReady(ctx, k8s, autopilot) })

	mustWrite(ctx, t, bao, sysRaftPath, map[string]any{"max_trailing_logs": 700, "server_stabilization_time": "30s"})
	waitFor(t, "autopilot repaired", func() error { return sysCfgExpect(ctx, bao, sysRaftPath, defaults) })

	sysRaftUpdate(ctx, t, k8s, autopilot, func(s *sysv1alpha1.RaftAutopilotSpec) {
		s.CleanupDeadServers = new(true)
		s.MinQuorum = new(int64(3))
	})
	waitFor(t, "quorum written", func() error {
		return sysCfgExpect(ctx, bao, sysRaftPath, map[string]any{"cleanup_dead_servers": true, "min_quorum": 3})
	})

	sysRaftUpdate(ctx, t, k8s, autopilot, func(s *sysv1alpha1.RaftAutopilotSpec) {
		s.CleanupDeadServers = nil
		s.MinQuorum = new(int64(0))
	})
	waitFor(t, "cleanup turned off", func() error {
		return sysCfgExpect(ctx, bao, sysRaftPath, map[string]any{"cleanup_dead_servers": false})
	})
	waitFor(t, "autopilot ready with min_quorum 0", func() error { return expectReady(ctx, k8s, autopilot) })
	// The running min_quorum stays at 3 until a restart; the operator must not try to rewrite it forever.
	sysCfgExpectNoWrites(t, "RaftAutopilot")

	if err := k8s.Delete(ctx, autopilot); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "autopilot resource gone", func() error { return expectGone(ctx, k8s, autopilot) })
	if err := sysCfgExpect(ctx, bao, sysRaftPath, map[string]any{"max_trailing_logs": 1000}); err != nil {
		t.Errorf("delete must restore the defaults: %v", err)
	}
}

func sysRaftUpdate(ctx context.Context, t *testing.T, k8s client.Client, obj *sysv1alpha1.RaftAutopilot,
	mutate func(*sysv1alpha1.RaftAutopilotSpec)) {
	t.Helper()
	if err := k8s.Get(ctx, client.ObjectKeyFromObject(obj), obj); err != nil {
		t.Fatal(err)
	}
	mutate(&obj.Spec)
	if err := k8s.Update(ctx, obj); err != nil {
		t.Fatal(err)
	}
}

// sysRaftManager starts envtest with the RaftAutopilot CRD and a manager running its controller.
func sysRaftManager(ctx context.Context, t *testing.T, root, assets string, conns engine.Connections) client.Client {
	t.Helper()
	testEnv := &envtest.Environment{
		BinaryAssetsDirectory: assets,
		CRDInstallOptions: envtest.CRDInstallOptions{ErrorIfPathMissing: true, Paths: []string{
			filepath.Join(root, "config", "crd", "bases", "sys.openbaoconfig.io_raftautopilots.yaml"),
		}},
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
	skipNameValidation := true
	mgr, err := ctrl.NewManager(cfg, ctrl.Options{
		Scheme:                 scheme,
		Metrics:                metricsserver.Options{BindAddress: "0"},
		HealthProbeBindAddress: "0",
		Controller:             config.Controller{SkipNameValidation: &skipNameValidation},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := sys.SetupRaftAutopilot(mgr, conns, itestEngineOptions()); err != nil {
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
	return mgr.GetClient()
}

// sysRaftStart starts, initialises and unseals a one-node server on raft storage and returns its address and root
// token.
func sysRaftStart(t *testing.T, bin, dir string) (string, string) {
	t.Helper()
	listen, cluster := sysRaftFreeAddr(t), sysRaftFreeAddr(t)
	hcl := fmt.Sprintf(`storage "raft" {
  path    = %q
  node_id = "n1"
}
listener "tcp" {
  address         = %q
  cluster_address = %q
  tls_disable     = true
}
api_addr     = "http://%s"
cluster_addr = "http://%s"
`, dir, listen, cluster, listen, cluster)
	configPath := filepath.Join(dir, "bao.hcl")
	if err := os.WriteFile(configPath, []byte(hcl), 0o600); err != nil {
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
		resp, err := http.Get(addr + "/v1/sys/seal-status")
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
	waitFor(t, "raft leader active", func() error {
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

func sysRaftFreeAddr(t *testing.T) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := listener.Addr().String()
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	return addr
}
