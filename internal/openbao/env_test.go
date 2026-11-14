package openbao

import (
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
)

// TestClientIgnoresOperatorEnvironment guards against BAO_*/VAULT_* variables of the operator pod reaching a
// connection: only the connection spec may decide address, identity, namespace and TLS.
func TestClientIgnoresOperatorEnvironment(t *testing.T) {
	agent := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("request to %s went to BAO_AGENT_ADDR", r.URL.Path)
	}))
	t.Cleanup(agent.Close)
	for _, prefix := range []string{"BAO_", "VAULT_"} {
		for name, value := range map[string]string{
			"ADDR":            agent.URL,
			"AGENT_ADDR":      agent.URL,
			"TOKEN":           "operator-env-token",
			"NAMESPACE":       "operator-env-namespace",
			"SKIP_VERIFY":     "true",
			"CACERT":          "/nonexistent/ca.pem",
			"CLIENT_CERT":     "/nonexistent/cert.pem",
			"CLIENT_KEY":      "/nonexistent/key.pem",
			"TLS_SERVER_NAME": "evil.example",
			"HTTP_PROXY":      "http://127.0.0.1:1",
			"PROXY_ADDR":      "http://127.0.0.1:1",
			"SRV_LOOKUP":      "true",
			"RATE_LIMIT":      "0.001",
			"MAX_RETRIES":     "0",
			"CLIENT_TIMEOUT":  "1ns",
		} {
			t.Setenv(prefix+name, value)
		}
	}
	f := newFakeServer(t)
	f.resource = func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Vault-Token") == "operator-env-token" {
			t.Errorf("request to %s carried the environment token", r.URL.Path)
		}
		if ns := r.Header.Get("X-Vault-Namespace"); ns != "" {
			t.Errorf("request to %s carried namespace %q", r.URL.Path, ns)
		}
		writeJSON(w, http.StatusOK, map[string]any{fieldData: map[string]any{}})
	}
	c, _ := newTestClient(t, appRoleConfig(f.srv.URL))
	for range 3 {
		if _, err := c.Read(t.Context(), "kv/x"); err != nil {
			t.Fatal(err)
		}
	}

	ac, _, err := newAPIClient(Config{Address: testAddress}, "")
	if err != nil {
		t.Fatal(err)
	}
	if ac.Token() != "" || ac.Namespace() != "" || ac.Address() != testAddress {
		t.Fatalf("api client token set %v, namespace %q, address %q", ac.Token() != "", ac.Namespace(), ac.Address())
	}
	tr, ok := ac.CloneConfig().HttpClient.Transport.(*http.Transport)
	if !ok || tr.TLSClientConfig.InsecureSkipVerify || tr.TLSClientConfig.ServerName != "" ||
		len(tr.TLSClientConfig.Certificates) != 0 || tr.TLSClientConfig.RootCAs != nil {
		t.Fatal("environment changed the TLS configuration")
	}
}

// TestAuthNamespaceRouting checks that login runs in AuthNamespace while requests run in Namespace.
func TestAuthNamespaceRouting(t *testing.T) {
	var mu sync.Mutex
	seen := map[string]string{}
	f := newFakeServer(t)
	inner := http.HandlerFunc(f.ServeHTTP)
	f.srv.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		seen[r.URL.Path] = r.Header.Get("X-Vault-Namespace")
		mu.Unlock()
		inner(w, r)
	})
	cfg := appRoleConfig(f.srv.URL)
	cfg.Namespace = "tenants/a"
	cfg.AuthNamespace = "tenants"
	c, _ := newTestClient(t, cfg)
	if _, err := c.Read(t.Context(), "kv/x"); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if got := seen["/v1/auth/approle/login"]; got != "tenants" {
		t.Errorf("login namespace = %q, want tenants", got)
	}
	if got := seen["/v1/kv/x"]; got != "tenants/a" {
		t.Errorf("read namespace = %q, want tenants/a", got)
	}
}

// TestClientIgnoresProxyEnvironment guards against HTTP(S)_PROXY steering OpenBao traffic, token header included,
// through a proxy the connection spec never named.
func TestClientIgnoresProxyEnvironment(t *testing.T) {
	var proxied atomic.Int32
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		proxied.Add(1)
		w.WriteHeader(http.StatusBadGateway)
	}))
	t.Cleanup(proxy.Close)
	t.Setenv("HTTP_PROXY", proxy.URL)
	t.Setenv("HTTPS_PROXY", proxy.URL)
	t.Setenv("NO_PROXY", "")

	_, hc, err := newAPIClient(Config{Address: "http://openbao.example.invalid:8200"}, "")
	if err != nil {
		t.Fatal(err)
	}
	if tr, ok := hc.Transport.(*http.Transport); !ok || tr.Proxy != nil {
		t.Fatal("transport keeps a proxy function")
	}
	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, "http://openbao.example.invalid:8200/v1/sys/health", nil)
	if err != nil {
		t.Fatal(err)
	}
	if resp, err := hc.Do(req); err == nil {
		_ = resp.Body.Close()
	}
	if n := proxied.Load(); n != 0 {
		t.Fatalf("proxy from the environment received %d requests", n)
	}
}
