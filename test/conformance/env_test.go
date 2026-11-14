//go:build conformance

package conformance

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
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
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
)

const (
	confRootToken     = "root"
	confOperatorMount = "conf-operator"
	confOperatorNS    = "default"
	confConnection    = "conf"
	confApproleSecret = "conf-approle"
)

// confBao is a `bao server -dev` driven over plain HTTP with the root token, for prerequisites only; the
// operator itself logs in through AppRole.
type confBao struct {
	addr string
	http *http.Client
}

func confStartBao(t *testing.T, bin, logDir string) *confBao {
	t.Helper()
	listen := "127.0.0.1:" + strconv.Itoa(confRandomPort(t))
	log := confCreateFile(t, filepath.Join(logDir, "bao.log"))
	cmd := exec.Command(bin, "server", "-dev", "-dev-root-token-id="+confRootToken, "-dev-listen-address="+listen)
	cmd.Stdout, cmd.Stderr = log, log
	if err := cmd.Start(); err != nil {
		t.Fatalf("start bao: %v", err)
	}
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	})
	b := &confBao{addr: "http://" + listen, http: &http.Client{Timeout: 10 * time.Second}}
	if err := confPoll(30*time.Second, func() error {
		_, code, err := b.do("", http.MethodGet, "sys/health", nil)
		if err == nil && code != http.StatusOK {
			err = fmt.Errorf("health status %d", code)
		}
		return err
	}); err != nil {
		t.Fatalf("bao not healthy: %v", err)
	}
	return b
}

// do sends one request in the given OpenBao namespace and returns the decoded body; a status of 400 or more is
// an error that carries the server's error strings (dev server, test data only).
func (b *confBao) do(namespace, method, path string, body any) (map[string]any, int, error) {
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
	req.Header.Set("X-Vault-Token", confRootToken)
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
		return out, resp.StatusCode, fmt.Errorf("%s %s: status %d: %v", method, path, resp.StatusCode, out["errors"])
	}
	return out, resp.StatusCode, nil
}

// prepareOperator creates the operator's AppRole in the root namespace with a policy that covers every
// namespace, and returns its role and secret IDs.
func (b *confBao) prepareOperator() (roleID, secretID string, err error) {
	for _, step := range []struct {
		path string
		body map[string]any
	}{
		{"sys/auth/" + confOperatorMount, map[string]any{"type": "approle"}},
		{"sys/policies/acl/conf-operator", map[string]any{"policy": "path \"*\" {\n  capabilities = " +
			"[\"create\", \"read\", \"update\", \"patch\", \"delete\", \"list\", \"sudo\"]\n}\n"}},
		{"auth/" + confOperatorMount + "/role/operator", map[string]any{"token_policies": []string{"conf-operator"},
			"token_ttl": "4h", "token_max_ttl": "8h"}},
	} {
		if _, _, err := b.do("", http.MethodPost, step.path, step.body); err != nil {
			return "", "", err
		}
	}
	resp, _, err := b.do("", http.MethodGet, "auth/"+confOperatorMount+"/role/operator/role-id", nil)
	if err != nil {
		return "", "", err
	}
	data, _ := resp["data"].(map[string]any)
	roleID, _ = data["role_id"].(string)
	resp, _, err = b.do("", http.MethodPost, "auth/"+confOperatorMount+"/role/operator/secret-id", nil)
	if err != nil {
		return "", "", err
	}
	data, _ = resp["data"].(map[string]any)
	secretID, _ = data["secret_id"].(string)
	if roleID == "" || secretID == "" {
		return "", "", errors.New("approle returned no role or secret id")
	}
	return roleID, secretID, nil
}

func (b *confBao) createNamespace(name string) error {
	_, _, err := b.do("", http.MethodPost, "sys/namespaces/"+name, map[string]any{})
	return err
}

// ensureMount enables an auth method or secrets engine at path in namespace unless something is mounted there.
func (b *confBao) ensureMount(namespace string, m confMount, path string) error {
	list := "sys/mounts"
	if m.auth {
		list = "sys/auth"
	}
	resp, _, err := b.do(namespace, http.MethodGet, list, nil)
	if err != nil {
		return err
	}
	if _, ok := resp[strings.Trim(path, "/")+"/"]; ok {
		return nil
	}
	body := map[string]any{"type": m.typ}
	if m.version != "" {
		body["options"] = map[string]any{"version": m.version}
	}
	_, _, err = b.do(namespace, http.MethodPost, list+"/"+strings.Trim(path, "/"), body)
	return err
}

// confCluster is envtest with every CRD installed, a kubeconfig for the manager and an unstructured client.
type confCluster struct {
	k8s        client.Client
	kubeconfig string
}

func confStartCluster(t *testing.T, assets, crdDir, dir string) confCluster {
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
	user, err := testEnv.AddUser(envtest.User{Name: "conformance", Groups: []string{"system:masters"}}, cfg)
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
	return confCluster{k8s: k8s, kubeconfig: kubeconfig}
}

// confCreateServer creates the cluster-scoped OpenBaoServer named confConnection that admits every namespace;
// only CRD sets with the OpenBaoServer kind (v2) have it.
func confCreateServer(ctx context.Context, k8s client.Client, addr string) error {
	server := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "core.openbaoconfig.io/v1alpha1",
		"kind":       "OpenBaoServer",
		"metadata":   map[string]any{confKeyName: confConnection},
		"spec": map[string]any{"address": addr, "allowInsecureHTTP": true, "requestTimeout": "10s",
			"allowedNamespaces": map[string]any{}},
	}}
	return k8s.Create(ctx, server)
}

// confConnect creates the namespace, the AppRole credentials Secret and an OpenBaoConnection named
// confConnection whose base OpenBao namespace is baoNamespace (root when empty), and waits for it to be Ready.
// With an OpenBaoServer (v2) the connection names it; otherwise it carries the address itself.
func confConnect(ctx context.Context, e *confEnv, ns, baoNamespace string) error {
	k8s := e.cluster.k8s
	if err := k8s.Create(ctx, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: ns}}); err != nil &&
		!apierrors.IsAlreadyExists(err) {
		return err
	}
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: confApproleSecret, Namespace: ns},
		Data:       map[string][]byte{"role-id": []byte(e.roleID), "secret-id": []byte(e.secretID)},
	}
	if err := k8s.Create(ctx, secret); err != nil {
		return err
	}
	spec := map[string]any{
		"auth": map[string]any{"appRole": map[string]any{
			"mountPath":         confOperatorMount,
			"roleIDSecretRef":   map[string]any{confKeyName: confApproleSecret, "key": "role-id"},
			"secretIDSecretRef": map[string]any{confKeyName: confApproleSecret, "key": "secret-id"},
		}},
	}
	if e.serverKind {
		spec["serverRef"] = map[string]any{confKeyName: confConnection}
	} else {
		spec["address"], spec["requestTimeout"] = e.bao.addr, "10s"
	}
	if baoNamespace != "" {
		spec["namespace"] = baoNamespace
		spec["authNamespace"] = ""
	}
	conn := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "core.openbaoconfig.io/v1alpha1",
		"kind":       "OpenBaoConnection",
		"metadata":   map[string]any{confKeyName: confConnection, "namespace": ns},
		"spec":       spec,
	}}
	if err := k8s.Create(ctx, conn); err != nil {
		return err
	}
	return confPoll(time.Minute, func() error {
		state := confState(ctx, k8s, conn)
		if !state.ready {
			return fmt.Errorf("connection %s/%s not ready: %s", ns, confConnection, state.message)
		}
		return nil
	})
}

// confManager is one operator process started from the ./cmd binary.
type confManager struct {
	cmd        *exec.Cmd
	metricsURL string
	done       chan struct{}
}

func confStartManager(t *testing.T, e *confEnv, watch, logPath string) *confManager {
	t.Helper()
	metrics := "127.0.0.1:" + strconv.Itoa(confFreePort(t))
	probe := "127.0.0.1:" + strconv.Itoa(confFreePort(t))
	log := confCreateFile(t, logPath)
	args := make([]string, 0, 8+len(e.managerArgs))
	args = append(args,
		"--kubeconfig="+e.cluster.kubeconfig,
		"--metrics-bind-address="+metrics,
		"--metrics-secure=false",
		"--health-probe-bind-address="+probe,
		"--drift-interval-critical="+e.drift.String(),
		"--drift-interval-standard="+e.drift.String(),
		"--watch-namespaces="+watch,
		// One manager per namespace runs in parallel; a shared lease would let only one of them reconcile.
		"--leader-elect=false",
	)
	args = append(args, e.managerArgs...)
	cmd := exec.Command(e.managerBin, append(args, strings.Fields(os.Getenv("CONF_MANAGER_ARGS"))...)...)
	cmd.Env = append(os.Environ(), "POD_NAMESPACE="+confOperatorNS)
	cmd.Stdout, cmd.Stderr = log, log
	if err := cmd.Start(); err != nil {
		t.Fatalf("start manager: %v", err)
	}
	m := &confManager{cmd: cmd, metricsURL: "http://" + metrics + "/metrics", done: make(chan struct{})}
	go func() { _ = cmd.Wait(); close(m.done) }()
	t.Cleanup(m.stop)
	if err := confPoll(time.Minute, func() error { return confHTTPOK("http://" + probe + "/readyz") }); err != nil {
		t.Fatalf("manager not ready (log %s): %v", logPath, err)
	}
	return m
}

func (m *confManager) stop() {
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

// confMetrics holds openbao_writes_total by kind and controller_runtime_reconcile_total by controller.
type confMetrics struct {
	writes     map[string]float64
	reconciles map[string]float64
}

func (m *confManager) scrape() (confMetrics, error) {
	out := confMetrics{writes: map[string]float64{}, reconciles: map[string]float64{}}
	resp, err := http.Get(m.metricsURL) //nolint:gosec // local test endpoint
	if err != nil {
		return out, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return out, fmt.Errorf("metrics: status %d", resp.StatusCode)
	}
	sc := bufio.NewScanner(resp.Body)
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	for sc.Scan() {
		line := sc.Text()
		if line == "" || line[0] == '#' {
			continue
		}
		cut := strings.LastIndexByte(line, ' ')
		if cut < 0 {
			continue
		}
		value, err := strconv.ParseFloat(line[cut+1:], 64)
		if err != nil {
			continue
		}
		name, labels := confParseSeries(line[:cut])
		switch name {
		case "openbao_writes_total":
			out.writes[labels["kind"]] += value
		case "controller_runtime_reconcile_total":
			out.reconciles[labels["controller"]] += value
		}
	}
	return out, sc.Err()
}

func confParseSeries(series string) (string, map[string]string) {
	name, labelText, ok := strings.Cut(series, "{")
	if !ok {
		return series, nil
	}
	labels := map[string]string{}
	for pair := range strings.SplitSeq(strings.TrimSuffix(labelText, "}"), ",") {
		if k, v, ok := strings.Cut(pair, "="); ok {
			labels[k] = strings.Trim(v, `"`)
		}
	}
	return name, labels
}

// confRandomPort picks a free port in 20000-29999 for the bao dev server.
func confRandomPort(t *testing.T) int {
	t.Helper()
	for range 50 {
		port := 20000 + rand.IntN(10000)
		l, err := net.Listen("tcp", "127.0.0.1:"+strconv.Itoa(port))
		if err == nil {
			_ = l.Close()
			return port
		}
	}
	t.Fatal("no free port in 20000-29999")
	return 0
}

func confFreePort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = l.Close() }()
	return l.Addr().(*net.TCPAddr).Port
}

func confHTTPOK(url string) error {
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

func confCreateFile(t *testing.T, path string) *os.File {
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

func confPoll(timeout time.Duration, check func() error) error {
	deadline := time.Now().Add(timeout)
	for {
		err := check()
		if err == nil {
			return nil
		}
		if time.Now().After(deadline) {
			return err
		}
		time.Sleep(250 * time.Millisecond)
	}
}
