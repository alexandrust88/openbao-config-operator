package openbao

import (
	"context"
	"errors"
	"net/http"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
	dto "github.com/prometheus/client_model/go"
)

func TestReloginOnRevokedToken(t *testing.T) {
	f := newFakeServer(t)
	c, clk := newTestClient(t, appRoleConfig(f.srv.URL))

	f.mu.Lock()
	f.valid = "revoked"
	f.mu.Unlock()
	clk.Advance(10 * time.Second)

	if _, err := c.Read(t.Context(), "secret/x"); err != nil {
		t.Fatalf("read after revocation: %v", err)
	}
	if logins, _ := f.counts(); logins != 2 {
		t.Errorf("logins = %d, want 2", logins)
	}
}

func TestPolicyDenialReloginsOnce(t *testing.T) {
	f := newFakeServer(t)
	f.resource = func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusForbidden, map[string]any{fieldErrors: []string{errPermissionDeniedText}})
	}
	c, clk := newTestClient(t, appRoleConfig(f.srv.URL))
	clk.Advance(10 * time.Second)

	_, err := c.Read(t.Context(), "secret/x")
	if statusCode(err) != http.StatusForbidden {
		t.Fatalf("read: got %v, want 403", err)
	}
	if logins, _ := f.counts(); logins != 2 {
		t.Fatalf("logins = %d, want 2", logins)
	}
	if _, err := c.Read(t.Context(), "secret/x"); statusCode(err) != http.StatusForbidden {
		t.Fatalf("second read: got %v, want 403", err)
	}
	if logins, _ := f.counts(); logins != 2 {
		t.Errorf("logins = %d after a denial within %s, want 2", logins, minReloginInterval)
	}
}

func TestRenewalTiming(t *testing.T) {
	f := newFakeServer(t)
	f.loginTTL, f.renewable, f.renewTTL = 30, true, 30
	c, clk := newTestClient(t, appRoleConfig(f.srv.URL))
	read := func() {
		t.Helper()
		if _, err := c.Read(t.Context(), "secret/x"); err != nil {
			t.Fatalf("read: %v", err)
		}
	}
	expect := func(wantLogins, wantRenews int) {
		t.Helper()
		if logins, renews := f.counts(); logins != wantLogins || renews != wantRenews {
			t.Fatalf("logins=%d renews=%d, want %d %d", logins, renews, wantLogins, wantRenews)
		}
	}

	clk.Advance(9 * time.Second)
	read()
	expect(1, 0)

	clk.Advance(2 * time.Second)
	read()
	expect(1, 1)

	f.mu.Lock()
	f.renewTTL = 5
	f.mu.Unlock()
	clk.Advance(11 * time.Second)
	read()
	expect(2, 2)
}

func TestNonRenewableTokenRelogin(t *testing.T) {
	f := newFakeServer(t)
	f.loginTTL = 30
	c, clk := newTestClient(t, appRoleConfig(f.srv.URL))

	clk.Advance(19 * time.Second)
	if _, err := c.Read(t.Context(), "secret/x"); err != nil {
		t.Fatalf("read: %v", err)
	}
	if logins, _ := f.counts(); logins != 1 {
		t.Fatalf("logins = %d before 2/3 of the TTL, want 1", logins)
	}
	clk.Advance(2 * time.Second)
	if _, err := c.Read(t.Context(), "secret/x"); err != nil {
		t.Fatalf("read: %v", err)
	}
	if logins, renews := f.counts(); logins != 2 || renews != 0 {
		t.Errorf("logins=%d renews=%d, want 2 0", logins, renews)
	}
}

func TestPoolCache(t *testing.T) {
	f := newFakeServer(t)
	p := NewPool()
	ctx := t.Context()
	cfg := appRoleConfig(f.srv.URL)

	for range 2 {
		if _, err := p.Client(ctx, "conn", cfg); err != nil {
			t.Fatal(err)
		}
	}
	if logins, _ := f.counts(); logins != 1 {
		t.Fatalf("logins = %d for an unchanged config, want 1", logins)
	}

	cfg.Auth = AppRoleAuth{CredentialKey: "k2", RoleID: static("role")}
	if _, err := p.Client(ctx, "conn", cfg); err != nil {
		t.Fatal(err)
	}
	if logins, _ := f.counts(); logins != 2 {
		t.Fatalf("logins = %d after a credential change, want 2", logins)
	}

	p.Invalidate("conn")
	if _, err := p.Client(ctx, "conn", cfg); err != nil {
		t.Fatal(err)
	}
	if logins, _ := f.counts(); logins != 3 {
		t.Fatalf("logins = %d after Invalidate, want 3", logins)
	}
}

// TestPoolConcurrentMissesLoginOnce covers a restart or credential rotation: every reconcile of a connection misses
// the cache at once, and only one of them may log in.
func TestPoolConcurrentMissesLoginOnce(t *testing.T) {
	f := newFakeServer(t)
	p := NewPool()
	t.Cleanup(func() { p.Invalidate(t.Name()) })
	const callers = 50
	var arrived atomic.Int32
	allArrived := make(chan struct{})
	gatedRole := func(ctx context.Context) (string, error) {
		select {
		case <-allArrived:
			return "role", nil
		case <-ctx.Done():
			return "", ctx.Err()
		}
	}
	cfg := Config{Address: f.srv.URL, Auth: AppRoleAuth{CredentialKey: "k1", RoleID: gatedRole}}

	var wg sync.WaitGroup
	for range callers {
		wg.Go(func() {
			if arrived.Add(1) == callers {
				close(allArrived)
			}
			c, err := p.Client(t.Context(), t.Name(), cfg)
			if err != nil {
				t.Errorf("pool client: %v", err)
				return
			}
			if _, err := c.Read(t.Context(), "secret/x"); err != nil {
				t.Errorf("read: %v", err)
			}
		})
	}
	wg.Wait()
	if logins, _ := f.counts(); logins != 1 {
		t.Errorf("logins = %d for concurrent cache misses, want 1", logins)
	}
}

func TestPoolSharedLoginOutlivesACancelledCaller(t *testing.T) {
	f := newFakeServer(t)
	p := NewPool()
	t.Cleanup(func() { p.Invalidate(t.Name()) })
	started, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	var logins atomic.Int32
	gated := func(ctx context.Context) (string, error) {
		logins.Add(1)
		once.Do(func() { close(started) })
		select {
		case <-release:
			return "role", nil
		case <-ctx.Done():
			return "", ctx.Err()
		}
	}
	cfg := Config{Address: f.srv.URL, Auth: AppRoleAuth{CredentialKey: "k1", RoleID: gated}}

	first, cancelFirst := context.WithCancel(t.Context())
	firstErr, secondErr := make(chan error, 1), make(chan error, 1)
	go func() { _, err := p.Client(first, t.Name(), cfg); firstErr <- err }()
	<-started
	cancelFirst()
	select {
	case err := <-firstErr:
		if !errors.Is(err, context.Canceled) {
			t.Errorf("cancelled caller: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("a cancelled caller kept waiting for the shared login")
	}
	// The login is still held at release, so the second caller either joins it or, after it, finds the cached
	// client; a login aborted with the first caller would make it log in a second time.
	go func() { _, err := p.Client(t.Context(), t.Name(), cfg); secondErr <- err }()
	close(release)
	if err := <-secondErr; err != nil {
		t.Errorf("the cancelled caller aborted the shared login: %v", err)
	}
	if n := logins.Load(); n != 1 {
		t.Errorf("logins = %d, want the one shared login", n)
	}
}

func TestConcurrentRequestsLoginOnce(t *testing.T) {
	f := newFakeServer(t)
	f.loginTTL = 30
	c, clk := newTestClient(t, appRoleConfig(f.srv.URL))
	clk.Advance(25 * time.Second)

	var wg sync.WaitGroup
	for range 20 {
		wg.Go(func() {
			if _, err := c.WithNamespace("team").Read(t.Context(), "secret/x"); err != nil {
				t.Errorf("read: %v", err)
			}
		})
	}
	wg.Wait()
	if logins, _ := f.counts(); logins != 2 {
		t.Errorf("logins = %d, want 2", logins)
	}
}

func TestRetryOn5xxAndMetrics(t *testing.T) {
	var mu sync.Mutex
	hits := 0
	f := newFakeServer(t)
	f.resource = func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		hits++
		n := hits
		mu.Unlock()
		if n == 1 {
			writeJSON(w, http.StatusServiceUnavailable, map[string]any{fieldErrors: []string{"busy"}})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{fieldData: map[string]any{"keys": []string{"a", "b"}}})
	}
	c, _ := newTestClient(t, appRoleConfig(f.srv.URL))

	keys, err := c.List(t.Context(), "sys/policies/acl")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(keys) != 2 || hits != 2 {
		t.Fatalf("keys=%v hits=%d, want 2 keys after one retry", keys, hits)
	}
	if got := testutil.ToFloat64(requestsTotal.WithLabelValues(t.Name(), "LIST", "503")); got != 1 {
		t.Errorf("openbao_requests_total{code=503} = %v, want 1", got)
	}
	if got := testutil.ToFloat64(requestsTotal.WithLabelValues(t.Name(), "LIST", "200")); got != 1 {
		t.Errorf("openbao_requests_total{code=200} = %v, want 1", got)
	}
	if got := testutil.ToFloat64(requestsTotal.WithLabelValues(t.Name(), "PUT", "200")); got != 1 {
		t.Errorf("login requests = %v, want 1", got)
	}
}

func TestClusterIDFollowsAServerReplacedBehindTheAddress(t *testing.T) {
	f := newFakeServer(t)
	var id atomic.Value
	id.Store("cluster-a")
	f.resource = func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/sys/health" {
			writeJSON(w, http.StatusNotFound, map[string]any{})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"cluster_id": id.Load()})
	}
	c, clk := newTestClient(t, appRoleConfig(f.srv.URL))
	cached := func() string {
		t.Helper()
		got, err := c.(*client).ClusterID(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		return got
	}
	if got := cached(); got != "cluster-a" {
		t.Fatalf("cluster id = %q", got)
	}
	id.Store("cluster-b")
	clk.Advance(time.Minute)
	if got := cached(); got != "cluster-b" {
		t.Errorf("cluster id = %q a minute after the server was replaced, want cluster-b", got)
	}
	id.Store("cluster-c")
	fresh, ok := c.(interface {
		FreshClusterID(context.Context) (string, error)
	})
	if !ok {
		t.Fatal("the client cannot bypass its cached cluster id")
	}
	if got, err := fresh.FreshClusterID(t.Context()); err != nil || got != "cluster-c" {
		t.Errorf("fresh cluster id = %q, %v, want cluster-c", got, err)
	}
	if got := cached(); got != "cluster-c" {
		t.Errorf("the fresh read did not refresh the cache: %q", got)
	}
}

// TestRateLimitCoversRetries: every HTTP attempt takes a token from the server's limiter, including the SDK's own
// retry of a 503, so a retry cannot exceed the configured QPS.
func TestRateLimitCoversRetries(t *testing.T) {
	f := newFakeServer(t)
	var mu sync.Mutex
	var attempts []time.Time
	f.resource = func(w http.ResponseWriter, _ *http.Request) {
		mu.Lock()
		attempts = append(attempts, time.Now())
		first := len(attempts) == 1
		mu.Unlock()
		if first {
			writeJSON(w, http.StatusServiceUnavailable, map[string]any{fieldErrors: []string{"standby"}})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"data": map[string]any{}})
	}
	cfg := appRoleConfig(f.srv.URL)
	cfg.RateLimit = RateLimit{QPS: 1, Burst: 1}
	c, _ := newTestClient(t, cfg)
	if _, err := c.Read(WithPriority(t.Context()), "secret/x"); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(attempts) != 2 {
		t.Fatalf("attempts = %d, want the 503 and its retry", len(attempts))
	}
	if gap := attempts[1].Sub(attempts[0]); gap < 800*time.Millisecond {
		t.Errorf("retry sent %s after the first attempt, want at least the 1s of QPS 1", gap)
	}
}

// TestInvalidateRevokesLoginToken: dropping a connection revokes the token the pool logged in for, but never a
// static token, which belongs to whoever issued it.
func TestInvalidateRevokesLoginToken(t *testing.T) {
	for name, tc := range map[string]struct {
		auth       Auth
		wantRevoke bool
	}{
		"approle": {auth: AppRoleAuth{CredentialKey: "k1", RoleID: static("role")}, wantRevoke: true},
		"token":   {auth: TokenAuth{CredentialKey: "k1", Token: static("static-token")}},
	} {
		t.Run(name, func(t *testing.T) {
			f := newFakeServer(t)
			revoked := make(chan string, 1)
			f.resource = func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/v1/auth/token/revoke-self":
					revoked <- r.Header.Get("X-Vault-Token")
					w.WriteHeader(http.StatusNoContent)
				case "/v1/auth/token/lookup-self":
					writeJSON(w, http.StatusOK, map[string]any{"data": map[string]any{"ttl": 0, "renewable": false}})
				default:
					w.WriteHeader(http.StatusNotFound)
				}
			}
			p := NewPool()
			if _, err := p.Client(t.Context(), "conn", Config{Address: f.srv.URL, Auth: tc.auth}); err != nil {
				t.Fatal(err)
			}
			p.Invalidate("conn")
			select {
			case token := <-revoked:
				if !tc.wantRevoke || token != "t1" {
					t.Errorf("revoke-self with token %q, want revoke=%v of the login token", token, tc.wantRevoke)
				}
			case <-time.After(2 * time.Second):
				if tc.wantRevoke {
					t.Error("no revoke-self after Invalidate")
				}
			}
		})
	}
}

// TestInvalidateForgetsMetrics: the revocation that retire sends after Invalidate must not bring back the metric
// series Invalidate dropped.
func TestInvalidateForgetsMetrics(t *testing.T) {
	f := newFakeServer(t)
	revoked := make(chan struct{})
	f.resource = func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/auth/token/revoke-self" {
			defer close(revoked)
		}
		w.WriteHeader(http.StatusNoContent)
	}
	key := t.Name()
	p := NewPool()
	c, err := p.Client(t.Context(), key, appRoleConfig(f.srv.URL))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.Write(t.Context(), "kv/x", map[string]any{}); err != nil {
		t.Fatal(err)
	}
	if connectionSeries(t, key) == 0 {
		t.Fatal("no series recorded before Invalidate")
	}
	p.Invalidate(key)
	select {
	case <-revoked:
	case <-time.After(5 * time.Second):
		t.Fatal("no revoke-self after Invalidate")
	}
	for deadline := time.Now().Add(500 * time.Millisecond); time.Now().Before(deadline); time.Sleep(10 * time.Millisecond) {
		if n := connectionSeries(t, key); n != 0 {
			t.Fatalf("%d series of the invalidated connection after its token was revoked, want none", n)
		}
	}
}

// connectionSeries counts the series of the pool's metrics that carry connection as their connection label.
func connectionSeries(t *testing.T, connection string) int {
	t.Helper()
	ch := make(chan prometheus.Metric)
	go func() {
		for _, c := range []prometheus.Collector{requestsTotal, requestDuration, loginsTotal, tokenRenewalsTotal} {
			c.Collect(ch)
		}
		close(ch)
	}()
	n := 0
	for m := range ch {
		var pb dto.Metric
		if err := m.Write(&pb); err != nil {
			t.Fatal(err)
		}
		for _, l := range pb.GetLabel() {
			if l.GetName() == connectionLabel && l.GetValue() == connection {
				n++
			}
		}
	}
	return n
}

// TestInvalidateDuringLoginRetiresTheLogin: a login that finishes after Invalidate must not bring the dropped client
// back into the pool, and the token it logged in for is revoked like any retired session's.
func TestInvalidateDuringLoginRetiresTheLogin(t *testing.T) {
	f := newFakeServer(t)
	revoked := make(chan string, 1)
	f.resource = func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/auth/token/revoke-self" {
			revoked <- r.Header.Get("X-Vault-Token")
		}
		w.WriteHeader(http.StatusNoContent)
	}
	started, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	gated := func(ctx context.Context) (string, error) {
		once.Do(func() { close(started) })
		select {
		case <-release:
			return "role", nil
		case <-ctx.Done():
			return "", ctx.Err()
		}
	}
	p := NewPool()
	cfg := Config{Address: f.srv.URL, Auth: AppRoleAuth{CredentialKey: "k1", RoleID: gated}}

	clientErr := make(chan error, 1)
	go func() { _, err := p.Client(t.Context(), "conn", cfg); clientErr <- err }()
	<-started
	p.Invalidate("conn")
	close(release)
	if err := <-clientErr; err == nil {
		t.Error("Client returned the client of a login that Invalidate overtook")
	}
	if p.cached("conn", fingerprint(cfg)) != nil {
		t.Error("the overtaken login is cached after Invalidate")
	}
	select {
	case token := <-revoked:
		if token != "t1" {
			t.Errorf("revoke-self with token %q, want the overtaken login token", token)
		}
	case <-time.After(5 * time.Second):
		t.Error("the overtaken login token was not revoked")
	}

	if _, err := p.Client(t.Context(), "conn", cfg); err != nil {
		t.Fatal(err)
	}
	if logins, _ := f.counts(); logins != 2 {
		t.Errorf("logins = %d, want a fresh login after the overtaken one", logins)
	}
	p.Invalidate("conn")
}
