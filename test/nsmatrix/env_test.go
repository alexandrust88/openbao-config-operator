//go:build integration

package nsmatrix

import (
	"bufio"
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
	nsmRootToken      = "root"
	nsmConnection     = "conn"
	nsmApproleSecret  = "nsm-approle"
	nsmOperatorMount  = "nsm-operator"
	nsmTenantMount    = "tenant"
	nsmOperatorNS     = "default"
	nsmAttribution    = "X-Openbao-Config-Resource"
	nsmTeamA          = "team-a"
	nsmTeamAApps      = "team-a/apps"
	nsmTeamB          = "team-b"
	nsmAllCapsPolicy  = "path \"*\" {\n  capabilities = [\"create\", \"read\", \"update\", \"patch\", \"delete\", \"list\", \"sudo\"]\n}\n"
	nsmPollInterval   = 250 * time.Millisecond
	nsmConnectTimeout = time.Minute
)

// nsmBao is one `bao server -dev` with a declarative file audit device, driven with the root token for setup and
// read-back only; the operator logs in through AppRole.
type nsmBao struct {
	addr     string
	auditLog string
	http     *http.Client
}

func nsmStartBao(t *testing.T, bin, dir, name string) *nsmBao {
	t.Helper()
	listen := "127.0.0.1:" + strconv.Itoa(nsmFreePort(t))
	auditLog := filepath.Join(dir, name+"-audit.log")
	config := filepath.Join(dir, name+"-bao.hcl")
	// The audit device is declared in the config because bao -dev refuses to enable one through the API.
	hcl := fmt.Sprintf("audit \"file\" \"nsm\" {\n  options {\n    file_path = %q\n  }\n}\n", auditLog)
	if err := os.WriteFile(config, []byte(hcl), 0o600); err != nil {
		t.Fatal(err)
	}
	log := nsmCreateFile(t, filepath.Join(dir, name+"-bao.log"))
	cmd := exec.Command(bin, "server", "-dev", "-dev-root-token-id="+nsmRootToken, "-dev-listen-address="+listen,
		"-config="+config)
	cmd.Stdout, cmd.Stderr = log, log
	if err := cmd.Start(); err != nil {
		t.Fatalf("start bao: %v", err)
	}
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	})
	b := &nsmBao{addr: "http://" + listen, auditLog: auditLog, http: &http.Client{Timeout: 10 * time.Second}}
	if err := nsmPoll(30*time.Second, func() error {
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

// do sends one request with the root token in the given OpenBao namespace; a status of 400 or more is an error
// that carries the server's error strings (dev server, test data only).
func (b *nsmBao) do(namespace, method, path string, body any) (map[string]any, int, error) {
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
	req.Header.Set("X-Vault-Token", nsmRootToken)
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
		return out, resp.StatusCode, fmt.Errorf("%s %s in %q: status %d: %v", method, path, namespace, resp.StatusCode,
			out["errors"])
	}
	return out, resp.StatusCode, nil
}

// approle holds the credentials of one AppRole role.
type approle struct{ roleID, secretID string }

// prepare creates team-a, team-a/apps and team-b, makes the audit device log the operator's attribution header,
// and creates two AppRole logins: nsm-operator in the root namespace with a policy over every namespace, and tenant
// in team-a with a policy written in team-a, so its tokens reach team-a and its descendants only.
func (b *nsmBao) prepare() (operator, tenant approle, err error) {
	for _, step := range []struct {
		ns, path string
		body     map[string]any
	}{
		{"", "sys/namespaces/" + nsmTeamA, map[string]any{}},
		{nsmTeamA, "sys/namespaces/apps", map[string]any{}},
		{"", "sys/namespaces/" + nsmTeamB, map[string]any{}},
		{"", "sys/config/auditing/request-headers/" + nsmAttribution, map[string]any{"hmac": false}},
		{"", "sys/auth/" + nsmOperatorMount, map[string]any{"type": "approle"}},
		{"", "sys/policies/acl/nsm-operator", map[string]any{"policy": nsmAllCapsPolicy}},
		{"", "auth/" + nsmOperatorMount + "/role/operator", map[string]any{"token_policies": []string{"nsm-operator"},
			"token_ttl": "4h", "token_max_ttl": "8h"}},
		{nsmTeamA, "sys/auth/" + nsmTenantMount, map[string]any{"type": "approle"}},
		{nsmTeamA, "sys/policies/acl/tenant-admin", map[string]any{"policy": nsmAllCapsPolicy}},
		{nsmTeamA, "auth/" + nsmTenantMount + "/role/operator", map[string]any{"token_policies": []string{"tenant-admin"},
			"token_ttl": "4h", "token_max_ttl": "8h"}},
	} {
		if _, _, err := b.do(step.ns, http.MethodPost, step.path, step.body); err != nil {
			return operator, tenant, err
		}
	}
	if operator, err = b.approle("", nsmOperatorMount); err != nil {
		return operator, tenant, err
	}
	tenant, err = b.approle(nsmTeamA, nsmTenantMount)
	return operator, tenant, err
}

func (b *nsmBao) approle(ns, mount string) (approle, error) {
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

// ensureMount enables an auth method or secrets engine at path in namespace unless something is mounted there.
func (b *nsmBao) ensureMount(namespace string, m nsmMount, path string) error {
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

// auditEntry is one response entry of the audit log.
type auditEntry struct {
	at        time.Time
	resource  string // value of the attribution header: <group>/<kind>/<k8s namespace>/<name>
	namespace string // absolute OpenBao namespace, without slashes; "" is the root
	operation string
	path      string
	failed    bool
}

// write reports a successful create, update or delete other than a login, a lookup sent as a write or a token
// self-service call.
func (a auditEntry) write() bool {
	if a.failed || (a.operation != "create" && a.operation != "update" && a.operation != "delete") {
		return false
	}
	return !strings.HasSuffix(a.path, "/login") && !strings.HasSuffix(a.path, "/lookup") &&
		!strings.HasPrefix(a.path, "identity/lookup/") && !strings.HasPrefix(a.path, "auth/token/renew-self") &&
		!strings.HasPrefix(a.path, "auth/token/lookup-self")
}

func (b *nsmBao) audit() ([]auditEntry, error) {
	f, err := os.Open(b.auditLog)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	var out []auditEntry
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 16<<20)
	for sc.Scan() {
		var raw struct {
			Type    string `json:"type"`
			Time    string `json:"time"`
			Error   string `json:"error"`
			Request struct {
				Operation string              `json:"operation"`
				Path      string              `json:"path"`
				Headers   map[string][]string `json:"headers"`
				Namespace struct {
					Path string `json:"path"`
				} `json:"namespace"`
			} `json:"request"`
		}
		if err := json.Unmarshal(sc.Bytes(), &raw); err != nil || raw.Type != "response" {
			continue
		}
		at, _ := time.Parse(time.RFC3339Nano, raw.Time)
		e := auditEntry{
			at:        at,
			namespace: strings.Trim(raw.Request.Namespace.Path, "/"),
			operation: raw.Request.Operation,
			path:      raw.Request.Path,
			failed:    raw.Error != "",
		}
		for k, v := range raw.Request.Headers {
			if strings.EqualFold(k, nsmAttribution) && len(v) > 0 {
				e.resource = v[0]
			}
		}
		out = append(out, e)
	}
	return out, sc.Err()
}

// nsmCluster is envtest with every CRD installed, a kubeconfig for the manager and an unstructured client.
type nsmCluster struct {
	k8s        client.Client
	kubeconfig string
}

func nsmStartCluster(t *testing.T, assets, crdDir, dir string) nsmCluster {
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
	user, err := testEnv.AddUser(envtest.User{Name: "nsmatrix", Groups: []string{"system:masters"}}, cfg)
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
	return nsmCluster{k8s: k8s, kubeconfig: kubeconfig}
}

// nsmConnect creates the Kubernetes namespace of mode m, its BaoServer, the AppRole Secret and the
// BaoConnection, and waits for the connection to be Ready.
func nsmConnect(ctx context.Context, k8s client.Client, m *nsmMode) error {
	if err := k8s.Create(ctx, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: m.k8sNS}}); err != nil &&
		!apierrors.IsAlreadyExists(err) {
		return err
	}
	server := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "core.obco.dev/v1alpha1",
		"kind":       "BaoServer",
		"metadata":   map[string]any{nsmKeyName: m.k8sNS},
		"spec": map[string]any{"address": m.bao.addr, "allowInsecureHTTP": true, "requestTimeout": "10s",
			"allowedNamespaces": map[string]any{}},
	}}
	if err := k8s.Create(ctx, server); err != nil {
		return err
	}
	login := m.operator
	mount := nsmOperatorMount
	if m.tenantLogin {
		login, mount = m.tenant, nsmTenantMount
	}
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: nsmApproleSecret, Namespace: m.k8sNS},
		Data:       map[string][]byte{"role-id": []byte(login.roleID), "secret-id": []byte(login.secretID)},
	}
	if err := k8s.Create(ctx, secret); err != nil {
		return err
	}
	spec := map[string]any{
		"serverRef": map[string]any{nsmKeyName: m.k8sNS},
		"auth": map[string]any{"appRole": map[string]any{
			"mountPath":         mount,
			"roleIDSecretRef":   map[string]any{nsmKeyName: nsmApproleSecret, "key": "role-id"},
			"secretIDSecretRef": map[string]any{nsmKeyName: nsmApproleSecret, "key": "secret-id"},
		}},
	}
	if m.connNamespace != "" {
		spec["openbaoNamespace"] = m.connNamespace
	}
	if m.authNamespace != nil {
		spec["openbaoAuthNamespace"] = *m.authNamespace
	}
	conn := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "core.obco.dev/v1alpha1",
		"kind":       nsmKindConnection,
		"metadata":   map[string]any{nsmKeyName: nsmConnection, "namespace": m.k8sNS},
		nsmKeySpec:   spec,
	}}
	if err := k8s.Create(ctx, conn); err != nil {
		return err
	}
	return nsmPoll(nsmConnectTimeout, func() error {
		if state := nsmState(ctx, k8s, conn); !state.ready {
			return fmt.Errorf("connection %s/%s not ready: %s", m.k8sNS, nsmConnection, state.message)
		}
		return nil
	})
}

// nsmManager is one operator process started from the ./cmd binary.
type nsmManager struct {
	cmd  *exec.Cmd
	done chan struct{}
}

func nsmStartManager(t *testing.T, bin, kubeconfig, watch, logPath string, drift time.Duration) *nsmManager {
	t.Helper()
	probe := "127.0.0.1:" + strconv.Itoa(nsmFreePort(t))
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
	for _, flag := range []string{"write-budget-per-minute=-1", "cluster-id=nsmatrix"} {
		if name, _, _ := strings.Cut(flag, "="); strings.Contains(string(help), name) {
			args = append(args, "--"+flag)
		}
	}
	cmd := exec.Command(bin, args...)
	cmd.Env = append(os.Environ(), "POD_NAMESPACE="+nsmOperatorNS)
	log := nsmCreateFile(t, logPath)
	cmd.Stdout, cmd.Stderr = log, log
	if err := cmd.Start(); err != nil {
		t.Fatalf("start manager: %v", err)
	}
	m := &nsmManager{cmd: cmd, done: make(chan struct{})}
	go func() { _ = cmd.Wait(); close(m.done) }()
	t.Cleanup(m.stop)
	if err := nsmPoll(time.Minute, func() error { return nsmHTTPOK("http://" + probe + "/readyz") }); err != nil {
		t.Fatalf("manager not ready (log %s): %v", logPath, err)
	}
	return m
}

func (m *nsmManager) stop() {
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

func nsmFreePort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = l.Close() }()
	return l.Addr().(*net.TCPAddr).Port
}

func nsmHTTPOK(url string) error {
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

func nsmCreateFile(t *testing.T, path string) *os.File {
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

func nsmPoll(timeout time.Duration, check func() error) error {
	deadline := time.Now().Add(timeout)
	for {
		err := check()
		if err == nil {
			return nil
		}
		if time.Now().After(deadline) {
			return err
		}
		time.Sleep(nsmPollInterval)
	}
}
