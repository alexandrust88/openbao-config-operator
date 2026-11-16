//go:build integration

// The bao, envtest and manager helpers follow test/nsmatrix/env_test.go; keep the two in step.

package livekit

import (
	"bytes"
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

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
)

const (
	baoRootToken     = "root"
	operatorMount    = "livekit-operator"
	tenantMount      = "livekit-tenant"
	allCapsPolicy    = "path \"*\" {\n  capabilities = [\"create\", \"read\", \"update\", \"patch\", \"delete\", \"list\", \"sudo\"]\n}\n"
	pollInterval     = 500 * time.Millisecond
	operatorPodNSEnv = "default"
)

// bao is one `bao server -dev`, driven with the root token for setup only; the operator logs in through AppRole.
type bao struct {
	addr string
	http *http.Client
}

// startBao starts a dev server that also allows audit devices through the API and has an empty plugin directory.
func startBao(t *testing.T, bin, dir, name string) *bao {
	t.Helper()
	listen := "127.0.0.1:" + strconv.Itoa(freePort(t))
	plugins := filepath.Join(dir, name+"-plugins")
	for _, d := range []string{plugins, filepath.Join(dir, name+"-home")} {
		if err := os.MkdirAll(d, 0o750); err != nil {
			t.Fatal(err)
		}
	}
	// OpenBao checks executable paths after resolving symlinks, including macOS temporary directories.
	plugins, err := filepath.EvalSymlinks(plugins)
	if err != nil {
		t.Fatal(err)
	}
	config := filepath.Join(dir, name+"-bao.hcl")
	hcl := fmt.Sprintf("unsafe_allow_api_audit_creation = true\nplugin_directory = %q\n", plugins)
	if err := os.WriteFile(config, []byte(hcl), 0o600); err != nil {
		t.Fatal(err)
	}
	log := createFile(t, filepath.Join(dir, name+"-bao.log"))
	cmd := exec.Command(bin, "server", "-dev", "-dev-root-token-id="+baoRootToken, "-dev-listen-address="+listen,
		"-config="+config)
	cmd.Stdout, cmd.Stderr = log, log
	// Each dev server writes its token helper file to $HOME; concurrent servers sharing one HOME race on it.
	cmd.Env = append(os.Environ(), "HOME="+filepath.Join(dir, name+"-home"))
	if err := cmd.Start(); err != nil {
		t.Fatalf("start bao: %v", err)
	}
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	})
	b := &bao{addr: "http://" + listen, http: &http.Client{Timeout: 10 * time.Second}}
	if err := poll(30*time.Second, func() error {
		_, code, err := b.do("", http.MethodGet, "sys/health", nil)
		if err == nil && code != http.StatusOK {
			err = fmt.Errorf("health status %d", code)
		}
		return err
	}); err != nil {
		t.Fatalf("bao %s not healthy: %v", name, err)
	}
	return b
}

// do sends one request with the root token in an OpenBao namespace; a status of 400 or more is an error that
// carries the server's error strings (dev server, test data only).
func (b *bao) do(namespace, method, path string, body any) (map[string]any, int, error) {
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
	req.Header.Set("X-Vault-Token", baoRootToken)
	if namespace != "" {
		req.Header.Set("X-Vault-Namespace", namespace)
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
	if resp.StatusCode >= 400 && path != "sys/health" {
		return out, resp.StatusCode, fmt.Errorf("%s %s in %q: status %d: %v", method, path, namespace,
			resp.StatusCode, out["errors"])
	}
	return out, resp.StatusCode, nil
}

// approle holds the credentials of one AppRole role.
type approle struct{ roleID, secretID string }

// prepare creates the tenant namespace and two AppRole logins: an operator in the root namespace with a policy over
// every namespace, and a tenant inside the tenant namespace whose tokens reach that namespace and below only.
func (b *bao) prepare(tenantNS string) (operator, tenant approle, err error) {
	for _, step := range []struct {
		ns, path string
		body     map[string]any
	}{
		{"", "sys/namespaces/" + tenantNS, map[string]any{}},
		{"", "sys/auth/" + operatorMount, map[string]any{"type": "approle"}},
		{"", "sys/policies/acl/livekit-operator", map[string]any{"policy": allCapsPolicy}},
		{"", "auth/" + operatorMount + "/role/operator", map[string]any{"token_policies": []string{"livekit-operator"},
			"token_ttl": "4h", "token_max_ttl": "8h"}},
		{tenantNS, "sys/auth/" + tenantMount, map[string]any{"type": "approle"}},
		{tenantNS, "sys/policies/acl/livekit-tenant", map[string]any{"policy": allCapsPolicy}},
		{tenantNS, "auth/" + tenantMount + "/role/operator", map[string]any{"token_policies": []string{"livekit-tenant"},
			"token_ttl": "4h", "token_max_ttl": "8h"}},
	} {
		if _, _, err := b.do(step.ns, http.MethodPost, step.path, step.body); err != nil {
			return operator, tenant, err
		}
	}
	if operator, err = b.approle("", operatorMount); err != nil {
		return operator, tenant, err
	}
	tenant, err = b.approle(tenantNS, tenantMount)
	return operator, tenant, err
}

func (b *bao) approle(ns, mount string) (approle, error) {
	resp, _, err := b.do(ns, http.MethodGet, "auth/"+mount+"/role/operator/role-id", nil)
	if err != nil {
		return approle{}, err
	}
	data, _ := resp["data"].(map[string]any)
	roleID, _ := data["role_id"].(string)
	resp, _, err = b.do(ns, http.MethodPost, "auth/"+mount+"/role/operator/secret-id", nil)
	if err != nil {
		return approle{}, err
	}
	data, _ = resp["data"].(map[string]any)
	secretID, _ := data["secret_id"].(string)
	if roleID == "" || secretID == "" {
		return approle{}, errors.New("approle returned no role or secret id")
	}
	return approle{roleID: roleID, secretID: secretID}, nil
}

// cluster is envtest with every CRD installed, a kubeconfig for the manager and kubectl, and a client.
type cluster struct {
	k8s        client.Client
	cfg        *rest.Config
	kubeconfig string
}

func startCluster(t *testing.T, assets, crdDir, dir string) cluster {
	t.Helper()
	testEnv := &envtest.Environment{
		BinaryAssetsDirectory:   assets,
		ControlPlaneStopTimeout: time.Minute,
		CRDInstallOptions:       envtest.CRDInstallOptions{Paths: []string{crdDir}, ErrorIfPathMissing: true},
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
	user, err := testEnv.AddUser(envtest.User{Name: "livekit", Groups: []string{"system:masters"}}, cfg)
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
	harness := rest.CopyConfig(cfg)
	harness.QPS, harness.Burst = 500, 1000
	scheme := runtime.NewScheme()
	if err := clientgoscheme.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	k8s, err := client.New(harness, client.Options{Scheme: scheme})
	if err != nil {
		t.Fatal(err)
	}
	return cluster{k8s: k8s, cfg: harness, kubeconfig: kubeconfig}
}

// manager is one operator process started from the ./cmd binary.
type manager struct {
	cmd  *exec.Cmd
	done chan struct{}
}

func buildManager(t *testing.T, root string) string {
	t.Helper()
	if bin := os.Getenv("LIVEKIT_MANAGER_BIN"); bin != "" {
		return bin
	}
	bin := filepath.Join(t.TempDir(), "manager")
	build := exec.Command("go", "build", "-o", bin, "./cmd/")
	build.Dir = root
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build manager: %v\n%s", err, output)
	}
	return bin
}

func startManager(t *testing.T, bin, kubeconfig, watch, logPath string, drift time.Duration) {
	t.Helper()
	probe := "127.0.0.1:" + strconv.Itoa(freePort(t))
	args := []string{
		"--kubeconfig=" + kubeconfig,
		"--metrics-bind-address=0",
		"--health-probe-bind-address=" + probe,
		"--leader-elect=false",
		"--drift-interval-critical=" + drift.String(),
		"--drift-interval-standard=" + drift.String(),
		"--watch-namespaces=" + watch,
	}
	help, _ := exec.Command(bin, "--help").CombinedOutput()
	for _, flag := range []string{"write-budget-per-minute=-1", "cluster-id=livekit"} {
		if name, _, _ := strings.Cut(flag, "="); strings.Contains(string(help), name) {
			args = append(args, "--"+flag)
		}
	}
	cmd := exec.Command(bin, args...)
	cmd.Env = append(os.Environ(), "POD_NAMESPACE="+operatorPodNSEnv)
	log := createFile(t, logPath)
	cmd.Stdout, cmd.Stderr = log, log
	if err := cmd.Start(); err != nil {
		t.Fatalf("start manager: %v", err)
	}
	m := &manager{cmd: cmd, done: make(chan struct{})}
	go func() { _ = cmd.Wait(); close(m.done) }()
	t.Cleanup(m.stop)
	if err := poll(time.Minute, func() error { return httpOK("http://" + probe + "/readyz") }); err != nil {
		t.Fatalf("manager not ready (log %s): %v", logPath, err)
	}
}

func (m *manager) stop() {
	select {
	case <-m.done:
		return
	default:
	}
	_ = m.cmd.Process.Signal(syscall.SIGTERM)
	select {
	case <-m.done:
	case <-time.After(20 * time.Second):
		_ = m.cmd.Process.Kill()
		<-m.done
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

func createFile(t *testing.T, path string) *os.File {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		t.Fatal(err)
	}
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = f.Close() })
	return f
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
		time.Sleep(pollInterval)
	}
}

// objState is the engine-reported state of one object.
type objState struct {
	ready           bool
	reason, message string
}

func stateOf(cur *unstructured.Unstructured) objState {
	gen := cur.GetGeneration()
	observed, _, _ := unstructured.NestedInt64(cur.Object, "status", "observedGeneration")
	var s objState
	synced := false
	conds, _, _ := unstructured.NestedSlice(cur.Object, "status", "conditions")
	found := false
	for _, c := range conds {
		cond, _ := c.(map[string]any)
		condGen, _, _ := unstructured.NestedInt64(cond, "observedGeneration")
		reason, _ := cond["reason"].(string)
		message, _ := cond["message"].(string)
		switch {
		case cond["type"] == "Ready":
			found = true
			s.ready = cond["status"] == "True" && observed == gen && condGen == gen
			if s.reason == "" {
				s.reason, s.message = reason, message
			}
		case cond["type"] == "Synced" && cond["status"] == "False" && condGen == gen:
			s.reason, s.message = reason, message
		case cond["type"] == "Synced" && cond["status"] == "True" && condGen == gen:
			synced = true
		}
	}
	if !found && s.reason == "" {
		s.reason, s.message = "NoStatus", "no Ready condition"
	}
	policy, _, _ := unstructured.NestedString(cur.Object, keySpec, "managementPolicy")
	if managed(cur) && policy != "Observe" && !synced {
		s.ready = false
	}
	return s
}
