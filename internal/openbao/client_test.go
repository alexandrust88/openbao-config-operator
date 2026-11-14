package openbao

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

const (
	fieldErrors   = "errors"
	fieldData     = "data"
	fieldPolicy   = "policy"
	testNamespace = "tenant"
	testAddress   = "https://bao.example:8200"

	errPermissionDeniedText = "permission denied"
)

type fakeClock struct {
	mu  sync.Mutex
	now time.Time
}

func newFakeClock() *fakeClock {
	return &fakeClock{now: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

// fakeServer issues tokens t1, t2, ... from an approle login and accepts only the latest one, unless
// resource overrides the handling of non-auth paths.
type fakeServer struct {
	mu        sync.Mutex
	logins    int
	renews    int
	loginTTL  int
	renewable bool
	renewTTL  int
	valid     string
	resource  http.HandlerFunc
	srv       *httptest.Server
}

func newFakeServer(t *testing.T) *fakeServer {
	t.Helper()
	f := &fakeServer{}
	f.srv = httptest.NewServer(f)
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	switch r.URL.Path {
	case "/v1/auth/approle/login":
		f.logins++
		f.valid = fmt.Sprintf("t%d", f.logins)
		body := map[string]any{"auth": map[string]any{
			"client_token": f.valid, "lease_duration": f.loginTTL, "renewable": f.renewable,
		}}
		f.mu.Unlock()
		writeJSON(w, http.StatusOK, body)
		return
	case "/v1/auth/token/renew-self":
		f.renews++
		body := map[string]any{"auth": map[string]any{
			"client_token": r.Header.Get("X-Vault-Token"), "lease_duration": f.renewTTL, "renewable": true,
		}}
		f.mu.Unlock()
		writeJSON(w, http.StatusOK, body)
		return
	}
	resource, valid := f.resource, f.valid
	f.mu.Unlock()
	if resource != nil {
		resource(w, r)
		return
	}
	if r.Header.Get("X-Vault-Token") != valid {
		writeJSON(w, http.StatusForbidden, map[string]any{fieldErrors: []string{errPermissionDeniedText}})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{fieldData: map[string]any{"ok": true}})
}

func (f *fakeServer) counts() (logins, renews int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.logins, f.renews
}

func writeJSON(w http.ResponseWriter, code int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(body)
}

func static(v string) CredentialFunc {
	return func(context.Context) (string, error) { return v, nil }
}

func appRoleConfig(addr string) Config {
	return Config{Address: addr, Auth: AppRoleAuth{CredentialKey: "k1", RoleID: static("role")}}
}

func newTestClient(t *testing.T, cfg Config) (Client, *fakeClock) {
	t.Helper()
	clk := newFakeClock()
	p := NewPool()
	p.now = clk.Now
	c, err := p.Client(t.Context(), t.Name(), cfg)
	if err != nil {
		t.Fatalf("pool client: %v", err)
	}
	t.Cleanup(func() { p.Invalidate(t.Name()) })
	return c, clk
}

func TestNotFoundMapping(t *testing.T) {
	f := newFakeServer(t)
	f.resource = func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/empty" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		writeJSON(w, http.StatusNotFound, map[string]any{fieldErrors: []string{}})
	}
	c, _ := newTestClient(t, appRoleConfig(f.srv.URL))
	ctx := t.Context()

	for _, path := range []string{"missing", "empty"} {
		if _, err := c.Read(ctx, path); !errors.Is(err, ErrNotFound) {
			t.Errorf("read %s: got %v, want ErrNotFound", path, err)
		}
		if _, err := c.List(ctx, path); !errors.Is(err, ErrNotFound) {
			t.Errorf("list %s: got %v, want ErrNotFound", path, err)
		}
	}
	if err := c.Delete(ctx, "missing"); !errors.Is(err, ErrNotFound) {
		t.Errorf("delete: got %v, want ErrNotFound", err)
	}
	if _, err := c.Write(ctx, "missing", map[string]any{"a": 1}); !errors.Is(err, ErrNotFound) {
		t.Errorf("write: got %v, want ErrNotFound", err)
	}
}

func TestNamespaceJoining(t *testing.T) {
	var mu sync.Mutex
	var gotNS string
	f := newFakeServer(t)
	f.resource = func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		gotNS = r.Header.Get("X-Vault-Namespace")
		mu.Unlock()
		writeJSON(w, http.StatusOK, map[string]any{fieldData: map[string]any{}})
	}
	cfg := appRoleConfig(f.srv.URL)
	cfg.Namespace = "/tenant/"
	c, _ := newTestClient(t, cfg)

	tests := []struct {
		client Client
		want   string
	}{
		{c, testNamespace},
		{c.WithNamespace(""), testNamespace},
		{c.WithNamespace("/team/"), "tenant/team"},
		{c.WithNamespace("team//a").WithNamespace("b"), "tenant/team/a/b"},
		{c.WithNamespace("../other"), "tenant/invalid..segment/other"},
	}
	for _, tt := range tests {
		if got := tt.client.Namespace(); got != tt.want {
			t.Errorf("Namespace() = %q, want %q", got, tt.want)
		}
		if _, err := tt.client.Read(t.Context(), "sys/x"); err != nil {
			t.Fatalf("read: %v", err)
		}
		mu.Lock()
		if gotNS != tt.want {
			t.Errorf("X-Vault-Namespace = %q, want %q", gotNS, tt.want)
		}
		mu.Unlock()
	}
	if c.Namespace() != testNamespace {
		t.Errorf("base client namespace changed to %q", c.Namespace())
	}
}

func TestNormalizeAddress(t *testing.T) {
	tests := []struct {
		in      string
		want    string
		wantErr bool
	}{
		{in: testAddress, want: testAddress},
		{in: "https://bao.example:8200/", want: testAddress},
		{in: "https://proxy.example/bao/", want: "https://proxy.example/bao"},
		{in: "https://bao.example/v1", wantErr: true},
		{in: "https://bao.example/v1/", wantErr: true},
		{in: "ftp://bao.example", wantErr: true},
		{in: "bao.example:8200", wantErr: true},
		{in: "https://bao.example?x=1", wantErr: true},
	}
	for _, tt := range tests {
		got, err := normalizeAddress(tt.in)
		if (err != nil) != tt.wantErr || got != tt.want {
			t.Errorf("normalizeAddress(%q) = %q, %v; want %q, err=%v", tt.in, got, err, tt.want, tt.wantErr)
		}
	}
}

func TestLoginPath(t *testing.T) {
	tests := []struct {
		mount   string
		want    string
		wantErr bool
	}{
		{mount: "", want: "auth/kubernetes/login"},
		{mount: "/k8s-a/", want: "auth/k8s-a/login"},
		{mount: "team/k8s", want: "auth/team/k8s/login"},
		{mount: "auth/kubernetes", wantErr: true},
		{mount: "/auth/kubernetes", wantErr: true},
		{mount: "../sys/k8s", wantErr: true},
		{mount: "team/../k8s", wantErr: true},
		{mount: "team//k8s", wantErr: true},
	}
	for _, tt := range tests {
		got, err := loginPath(tt.mount, "kubernetes")
		if (err != nil) != tt.wantErr || got != tt.want {
			t.Errorf("loginPath(%q) = %q, %v; want %q, err=%v", tt.mount, got, err, tt.want, tt.wantErr)
		}
	}
}

func TestCABundleRejectsGarbage(t *testing.T) {
	cfg := appRoleConfig("https://bao.example")
	cfg.CABundle = []byte("not a certificate")
	if _, err := NewPool().Client(t.Context(), "ca", cfg); err == nil {
		t.Fatal("expected an error for an invalid CA bundle")
	}
}

func TestResponseError(t *testing.T) {
	f := newFakeServer(t)
	f.resource = func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/sys/mounts/missing":
			writeJSON(w, http.StatusBadRequest, map[string]any{fieldErrors: []string{"no matching mount"}})
		case "/v1/raw":
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte("raw-body-content"))
		default:
			writeJSON(w, http.StatusNotFound, map[string]any{fieldErrors: []string{"not here"}})
		}
	}
	c, _ := newTestClient(t, appRoleConfig(f.srv.URL))
	ctx := t.Context()

	_, err := c.Read(ctx, "sys/mounts/missing")
	var re *ResponseError
	if !errors.As(err, &re) || re.StatusCode != http.StatusBadRequest || len(re.Errors) != 1 || re.Errors[0] != "no matching mount" {
		t.Fatalf("read: got %#v (%v), want ResponseError 400", re, err)
	}
	if errors.Is(err, ErrNotFound) {
		t.Error("a 400 must not match ErrNotFound")
	}
	if strings.Contains(err.Error(), f.srv.URL) {
		t.Errorf("error leaks the request URL: %v", err)
	}

	_, err = c.Read(ctx, "raw")
	if !errors.As(err, &re) || re.StatusCode != http.StatusBadRequest || len(re.Errors) != 0 {
		t.Fatalf("raw read: got %#v (%v)", re, err)
	}
	if strings.Contains(err.Error(), "raw-body-content") {
		t.Errorf("error leaks the response body: %v", err)
	}

	err = c.Delete(ctx, "gone")
	if !errors.As(err, &re) || re.StatusCode != http.StatusNotFound || !errors.Is(err, ErrNotFound) {
		t.Fatalf("delete: got %v, want ResponseError 404 matching ErrNotFound", err)
	}
	if _, err := c.Read(ctx, "gone"); !errors.As(err, &re) || re.StatusCode != http.StatusNotFound {
		t.Fatalf("read of a 404: got %v, want ResponseError 404", err)
	}
}

func TestTokenLookupErrorHidesBodyAndURL(t *testing.T) {
	f := newFakeServer(t)
	f.resource = func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/auth/token/lookup-self" && r.Header.Get("X-Vault-Namespace") == "gone" {
			writeJSON(w, http.StatusNotFound, map[string]any{fieldErrors: []string{"namespace not found"}})
			return
		}
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte("raw-body-content"))
	}
	auth := TokenAuth{CredentialKey: "k", Token: static("s.token")}

	_, err := NewPool().Client(t.Context(), t.Name(), Config{Address: f.srv.URL, Auth: auth})
	if statusCode(err) != http.StatusForbidden {
		t.Fatalf("login: got %v, want a ResponseError 403", err)
	}
	if strings.Contains(err.Error(), "raw-body-content") || strings.Contains(err.Error(), f.srv.URL) {
		t.Errorf("login error leaks the response body or the request URL: %v", err)
	}

	_, err = NewPool().Client(t.Context(), t.Name(), Config{Address: f.srv.URL, AuthNamespace: "gone", Auth: auth})
	if !errors.Is(err, ErrNamespaceNotFound) {
		t.Errorf("login in a missing namespace: got %v, want ErrNamespaceNotFound", err)
	}
}

func TestNotFoundKeepsServerErrors(t *testing.T) {
	f := newFakeServer(t)
	f.resource = func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/warned":
			writeJSON(w, http.StatusNotFound, map[string]any{"warnings": []string{"gone"}, fieldData: map[string]any{"a": 1}})
		default:
			writeJSON(w, http.StatusNotFound, map[string]any{fieldErrors: []string{"namespace not found"}})
		}
	}
	c, _ := newTestClient(t, appRoleConfig(f.srv.URL))

	_, err := c.Read(t.Context(), "warned")
	if !errors.Is(err, ErrNotFound) || errors.Is(err, ErrNamespaceNotFound) {
		t.Fatalf("404 with warnings: err = %v, want ErrNotFound", err)
	}
	if nf, ok := errors.AsType[*NotFoundDataError](err); !ok || nf.Data["a"] != json.Number("1") {
		t.Errorf("404 with data: err = %v, want NotFoundDataError with the response data", err)
	}
	_, readErr := c.Read(t.Context(), "kv/x")
	_, listErr := c.List(t.Context(), "kv/")
	for _, err := range []error{readErr, listErr} {
		if !errors.Is(err, ErrNotFound) || !errors.Is(err, ErrNamespaceNotFound) ||
			!strings.Contains(err.Error(), "namespace not found") {
			t.Fatalf("missing namespace: err = %v, want ErrNamespaceNotFound with the server message", err)
		}
		if _, ok := errors.AsType[*NotFoundDataError](err); ok {
			t.Errorf("missing namespace: a 404 without data must carry none")
		}
	}
}

// TestRedirectNotFollowed proves a redirect never re-sends the token to the host it names.
func TestRedirectNotFollowed(t *testing.T) {
	var leaked sync.Map
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		leaked.Store("token", r.Header.Get("X-Vault-Token"))
		writeJSON(w, http.StatusOK, map[string]any{fieldData: map[string]any{"ok": true}})
	}))
	t.Cleanup(other.Close)
	f := newFakeServer(t)
	f.resource = func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, other.URL+r.URL.Path, http.StatusTemporaryRedirect)
	}
	c, _ := newTestClient(t, appRoleConfig(f.srv.URL))

	got, err := c.Read(t.Context(), "redirected")
	if !errors.Is(err, errRedirect) {
		t.Fatalf("Read = %v, %v; want errRedirect", got, err)
	}
	if _, err := c.Write(t.Context(), "redirected", map[string]any{"a": "b"}); !errors.Is(err, errRedirect) {
		t.Fatalf("Write err = %v, want errRedirect", err)
	}
	if _, ok := leaked.Load("token"); ok {
		t.Fatal("the client followed the redirect to another host")
	}
}

func TestValidatePath(t *testing.T) {
	for _, ok := range []string{"sys/policies/acl/x", "sys/mounts/", "secret/data/a.b", "kv/..x/y", ""} {
		if err := ValidatePath(ok); err != nil {
			t.Errorf("ValidatePath(%q) = %v", ok, err)
		}
	}
	for _, bad := range []string{"secret/../sys/policies/acl/x", "..", ".", "kv/..", "a/./b", "a//b", "../x", "kv/../"} {
		if err := ValidatePath(bad); !errors.Is(err, ErrUncleanPath) {
			t.Errorf("ValidatePath(%q) = %v, want ErrUncleanPath", bad, err)
		}
	}
}

// TestUncleanPathNeverSent proves the client refuses an unclean path before any request leaves.
func TestUncleanPathNeverSent(t *testing.T) {
	f := newFakeServer(t)
	var hits sync.Map
	f.resource = func(w http.ResponseWriter, r *http.Request) {
		hits.Store(r.URL.Path, true)
		writeJSON(w, http.StatusOK, map[string]any{fieldData: map[string]any{}})
	}
	c, _ := newTestClient(t, appRoleConfig(f.srv.URL))
	const bad = "secret/../sys/policies/acl/evil"
	if _, err := c.Write(t.Context(), bad, map[string]any{fieldPolicy: "x"}); !errors.Is(err, ErrUncleanPath) {
		t.Fatalf("Write err = %v", err)
	}
	if _, err := c.Read(t.Context(), bad); !errors.Is(err, ErrUncleanPath) {
		t.Fatalf("Read err = %v", err)
	}
	if err := c.Delete(t.Context(), bad); !errors.Is(err, ErrUncleanPath) {
		t.Fatalf("Delete err = %v", err)
	}
	if _, err := c.List(t.Context(), bad); !errors.Is(err, ErrUncleanPath) {
		t.Fatalf("List err = %v", err)
	}
	hits.Range(func(k, _ any) bool {
		t.Errorf("request sent to %v", k)
		return true
	})
}

func TestValidateNamespaceRejectsEscapes(t *testing.T) {
	for ns, wantErr := range map[string]bool{
		"":                 false,
		"team-a":           false,
		"team-a/apps":      false,
		"..":               true,
		"../team-b":        true,
		"team-a/../team-b": true,
		"./team-a":         true,
		"/team-b":          true,
		"team-a/":          true,
		"team-a//apps":     true,
		"root":             true,
		"root/team-a":      true,
	} {
		if err := ValidateNamespace(ns); (err != nil) != wantErr {
			t.Errorf("ValidateNamespace(%q) = %v, want error %v", ns, err, wantErr)
		}
	}
}

// TestTrailingSlashSentCanonical proves "a/b/" and "a/b" reach OpenBao as one path, as the engine's ownership
// checks treat them.
func TestTrailingSlashSentCanonical(t *testing.T) {
	f := newFakeServer(t)
	var mu sync.Mutex
	var paths []string
	f.resource = func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		paths = append(paths, r.URL.Path)
		mu.Unlock()
		writeJSON(w, http.StatusOK, map[string]any{fieldData: map[string]any{}})
	}
	c, _ := newTestClient(t, appRoleConfig(f.srv.URL))
	if _, err := c.Write(t.Context(), "secret/foo/", map[string]any{fieldPolicy: "x"}); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Read(t.Context(), "secret/foo/"); err != nil {
		t.Fatal(err)
	}
	if err := c.Delete(t.Context(), "secret/foo/"); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	for _, p := range paths {
		if p != "/v1/secret/foo" {
			t.Errorf("request sent to %s, want /v1/secret/foo", p)
		}
	}
}
