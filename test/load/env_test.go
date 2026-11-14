//go:build load

package load

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/dynamic"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
)

const (
	objectNamespace      = "load"
	operatorNS           = litDefault
	connectionName       = "bao"
	operatorAppRoleMount = "approle-operator"
)

// baoServer is a single-node OpenBao on raft storage. Unlike `bao server -dev` it keeps its data across a
// restart, which the outage scenario needs.
type baoServer struct {
	bin, dir, addr string
	log            *os.File
	cmd            *exec.Cmd
	unsealKey      string
	rootToken      string
	http           *http.Client
}

func newBaoServer(t *testing.T, bin, dir string, log *os.File) *baoServer {
	t.Helper()
	port := freePort(t)
	cluster := freePort(t)
	addr := "127.0.0.1:" + strconv.Itoa(port)
	config := fmt.Sprintf(`storage "raft" {
  path    = %q
  node_id = "load"
}
listener "tcp" {
  address     = %q
  tls_disable = true
}
api_addr      = "http://%s"
cluster_addr  = "http://127.0.0.1:%d"
disable_mlock = true
`, filepath.Join(dir, "raft"), addr, addr, cluster)
	if err := os.MkdirAll(filepath.Join(dir, "raft"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "bao.hcl"), []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}
	return &baoServer{bin: bin, dir: dir, addr: "http://" + addr, log: log,
		http: &http.Client{Timeout: time.Minute}}
}

func (b *baoServer) start() error {
	b.cmd = exec.Command(b.bin, "server", "-config="+filepath.Join(b.dir, "bao.hcl"))
	b.cmd.Stdout, b.cmd.Stderr = b.log, b.log
	if err := b.cmd.Start(); err != nil {
		return fmt.Errorf("start bao: %w", err)
	}
	return poll(30*time.Second, func() error {
		_, _, err := b.do(http.MethodGet, "sys/seal-status", nil)
		return err
	})
}

// initialize runs sys/init once with a single unseal key.
func (b *baoServer) initialize() error {
	resp, _, err := b.do(http.MethodPut, "sys/init", map[string]any{"secret_shares": 1, "secret_threshold": 1})
	if err != nil {
		return err
	}
	keys, _ := resp["keys"].([]any)
	if len(keys) != 1 {
		return errors.New("sys/init returned no unseal key")
	}
	b.unsealKey, _ = keys[0].(string)
	b.rootToken, _ = resp["root_token"].(string)
	return nil
}

// unseal unseals the server and waits until it is the active raft leader.
func (b *baoServer) unseal() error {
	if _, _, err := b.do(http.MethodPut, "sys/unseal", map[string]any{litKey: b.unsealKey}); err != nil {
		return err
	}
	return poll(60*time.Second, func() error {
		_, code, err := b.do(http.MethodGet, "sys/health", nil)
		if err != nil {
			return err
		}
		if code != http.StatusOK {
			return fmt.Errorf("health %d", code)
		}
		return nil
	})
}

func (b *baoServer) stop() {
	if b.cmd == nil || b.cmd.Process == nil {
		return
	}
	_ = b.cmd.Process.Signal(syscall.SIGTERM)
	done := make(chan struct{})
	go func() { _ = b.cmd.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(15 * time.Second):
		_ = b.cmd.Process.Kill()
		<-done
	}
	b.cmd = nil
}

// do sends one request and returns the decoded body. A status of 400 or above is an error, except on sys/health,
// which reports standby, sealed and uninitialised servers that way.
func (b *baoServer) do(method, path string, body any) (map[string]any, int, error) {
	var reader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return nil, 0, err
		}
		reader = bytes.NewReader(raw)
	}
	req, err := http.NewRequest(method, b.addr+"/v1/"+path, reader)
	if err != nil {
		return nil, 0, err
	}
	if b.rootToken != "" {
		req.Header.Set("X-Vault-Token", b.rootToken)
	}
	resp, err := b.http.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, resp.StatusCode, err
	}
	var out map[string]any
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, &out)
	}
	if resp.StatusCode >= 400 && !strings.HasPrefix(path, "sys/health") {
		return out, resp.StatusCode, fmt.Errorf("%s %s: status %d", method, path, resp.StatusCode)
	}
	return out, resp.StatusCode, nil
}

// manager is the operator binary under test.
type manager struct {
	cmd        *exec.Cmd
	metricsURL string
}

func startManager(t *testing.T, cfg config, kubeconfig string, log *os.File) *manager {
	t.Helper()
	metrics := "127.0.0.1:" + strconv.Itoa(freePort(t))
	probe := "127.0.0.1:" + strconv.Itoa(freePort(t))
	args := []string{
		"--kubeconfig=" + kubeconfig,
		"--metrics-bind-address=" + metrics,
		"--metrics-secure=false",
		"--health-probe-bind-address=" + probe,
		"--drift-interval-critical=" + cfg.driftInterval.String(),
		"--drift-interval-standard=" + cfg.driftInterval.String(),
		"--max-concurrent-reconciles=" + strconv.Itoa(cfg.maxConcurrent),
		"--watch-namespaces=" + objectNamespace,
	}
	if cfg.writeBudget != 0 {
		args = append(args, "--write-budget-per-minute="+strconv.Itoa(cfg.writeBudget))
	}
	cmd := exec.Command(cfg.managerBin, args...)
	cmd.Env = append(os.Environ(), "POD_NAMESPACE="+operatorNS)
	cmd.Stdout, cmd.Stderr = log, log
	if err := cmd.Start(); err != nil {
		t.Fatalf("start manager: %v", err)
	}
	t.Cleanup(func() {
		_ = cmd.Process.Signal(syscall.SIGTERM)
		_ = cmd.Wait()
	})
	m := &manager{cmd: cmd, metricsURL: "http://" + metrics + "/metrics"}
	if err := poll(60*time.Second, func() error { return httpOK("http://" + probe + "/readyz") }); err != nil {
		t.Fatalf("manager not ready: %v", err)
	}
	return m
}

// cluster is envtest plus the clients the harness uses.
type cluster struct {
	k8s        client.Client
	dyn        dynamic.Interface
	kubeconfig string
}

func startCluster(t *testing.T, cfg config, dir string) cluster {
	t.Helper()
	testEnv := &envtest.Environment{
		BinaryAssetsDirectory:   cfg.assets,
		ControlPlaneStopTimeout: time.Minute,
		CRDInstallOptions: envtest.CRDInstallOptions{
			Paths:              []string{cfg.crdDir},
			ErrorIfPathMissing: true,
		},
	}
	restCfg, err := testEnv.Start()
	if err != nil {
		t.Fatalf("envtest: %v", err)
	}
	t.Cleanup(func() {
		if err := testEnv.Stop(); err != nil {
			t.Logf("stop envtest: %v", err)
		}
	})
	user, err := testEnv.AddUser(envtest.User{Name: "load", Groups: []string{"system:masters"}}, restCfg)
	if err != nil {
		t.Fatal(err)
	}
	kc, err := user.KubeConfig()
	if err != nil {
		t.Fatal(err)
	}
	kubeconfig := filepath.Join(dir, "kubeconfig")
	if err := os.WriteFile(kubeconfig, kc, 0o600); err != nil {
		t.Fatal(err)
	}

	harnessCfg := rest.CopyConfig(restCfg)
	harnessCfg.QPS, harnessCfg.Burst = 2000, 4000
	scheme := runtime.NewScheme()
	if err := clientgoscheme.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	k8s, err := client.New(harnessCfg, client.Options{Scheme: scheme})
	if err != nil {
		t.Fatal(err)
	}
	dyn, err := dynamic.NewForConfig(harnessCfg)
	if err != nil {
		t.Fatal(err)
	}
	ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: objectNamespace}}
	if err := k8s.Create(context.Background(), ns); err != nil {
		t.Fatal(err)
	}
	return cluster{k8s: k8s, dyn: dyn, kubeconfig: kubeconfig}
}

// createConnection stores the operator's AppRole credentials in a Secret, declares the server as an OpenBaoServer
// and points an OpenBaoConnection at it; design rule 6 keeps the root token away from the operator.
func createConnection(ctx context.Context, t *testing.T, c cluster, addr, roleID, secretID string) {
	t.Helper()
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: litAppRoleSecret, Namespace: objectNamespace},
		Data:       map[string][]byte{"role-id": []byte(roleID), "secret-id": []byte(secretID)},
	}
	if err := c.k8s.Create(ctx, secret); err != nil {
		t.Fatal(err)
	}
	server := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "core.openbaoconfig.io/v1alpha1",
		"kind":       "OpenBaoServer",
		litMetadata:  map[string]any{litName: connectionName},
		"spec": map[string]any{
			"address":           addr,
			"allowInsecureHTTP": true,
			"requestTimeout":    "10s",
			"allowedNamespaces": map[string]any{"matchLabels": map[string]any{"kubernetes.io/metadata.name": objectNamespace}},
		},
	}}
	if err := c.k8s.Create(ctx, server); err != nil {
		t.Fatal(err)
	}
	conn := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "core.openbaoconfig.io/v1alpha1",
		"kind":       kindConnection,
		litMetadata:  map[string]any{litName: connectionName, "namespace": objectNamespace},
		"spec": map[string]any{
			"serverRef": map[string]any{litName: connectionName},
			"auth": map[string]any{"appRole": map[string]any{
				litMountPath:        operatorAppRoleMount,
				"roleIDSecretRef":   map[string]any{litName: litAppRoleSecret, litKey: "role-id"},
				"secretIDSecretRef": map[string]any{litName: litAppRoleSecret, litKey: "secret-id"},
			}},
		},
	}}
	if err := c.k8s.Create(ctx, conn); err != nil {
		t.Fatal(err)
	}
}

func freePort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = l.Close() }()
	return l.Addr().(*net.TCPAddr).Port
}

func httpOK(url string) error {
	resp, err := http.Get(url) //nolint:gosec // local test endpoint
	if err != nil {
		return err
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("%s: status %d", url, resp.StatusCode)
	}
	return nil
}

func poll(timeout time.Duration, check func() error) error {
	deadline := time.Now().Add(timeout)
	for {
		err := check()
		if err == nil {
			return nil
		}
		if time.Now().After(deadline) {
			return err
		}
		time.Sleep(200 * time.Millisecond)
	}
}
