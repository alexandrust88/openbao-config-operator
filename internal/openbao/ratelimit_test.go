package openbao

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"
)

func TestForbiddenWithValidTokenIsPermissionDenied(t *testing.T) {
	f := newFakeServer(t)
	f.resource = func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/auth/token/lookup-self" {
			writeJSON(w, http.StatusOK, map[string]any{fieldData: map[string]any{"ttl": 3600}})
			return
		}
		writeJSON(w, http.StatusForbidden, map[string]any{fieldErrors: []string{errPermissionDeniedText}})
	}
	c, clk := newTestClient(t, appRoleConfig(f.srv.URL))
	clk.Advance(10 * time.Second)

	_, err := c.Read(t.Context(), "secret/x")
	if !errors.Is(err, ErrPermissionDenied) || statusCode(err) != http.StatusForbidden {
		t.Fatalf("err = %v, want ErrPermissionDenied wrapping the 403", err)
	}
	if logins, _ := f.counts(); logins != 1 {
		t.Fatalf("logins = %d, want 1: a policy denial must not log in again", logins)
	}
}

func TestUnauthorizedLogsInAgain(t *testing.T) {
	f := newFakeServer(t)
	unauthorized := true
	f.resource = func(w http.ResponseWriter, _ *http.Request) {
		if unauthorized {
			unauthorized = false
			writeJSON(w, http.StatusUnauthorized, map[string]any{fieldErrors: []string{"token expired"}})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{fieldData: map[string]any{}})
	}
	c, clk := newTestClient(t, appRoleConfig(f.srv.URL))
	clk.Advance(10 * time.Second)

	if _, err := c.Read(t.Context(), "secret/x"); err != nil {
		t.Fatalf("read after a 401: %v", err)
	}
	if logins, _ := f.counts(); logins != 2 {
		t.Fatalf("logins = %d, want 2", logins)
	}
}

func TestServerLimiterIsSharedPerAddress(t *testing.T) {
	var ls limiters
	a := ls.forServer("https://Bao.example:8200/", RateLimit{QPS: 10, Burst: 10})
	b := ls.forServer("https://bao.example:8200", RateLimit{QPS: 10, Burst: 10})
	other := ls.forServer("https://other.example:8200", RateLimit{})
	if a != b || a == other {
		t.Fatal("clients of one server must share one limiter, other servers must not")
	}
}

func TestPriorityLaneSurvivesDriftLoad(t *testing.T) {
	l := newServerLimiter()
	l.set(RateLimit{QPS: 1, Burst: 10})
	for range 8 {
		if err := l.wait(t.Context()); err != nil {
			t.Fatal(err)
		}
	}
	short := func() context.Context {
		ctx, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
		t.Cleanup(cancel)
		return ctx
	}
	if err := l.wait(short()); err == nil {
		t.Fatal("a drift request exceeded the drift share of the limit")
	}
	if err := l.wait(WithPriority(short())); err != nil {
		t.Fatalf("a priority request found no capacity left: %v", err)
	}

	l.set(RateLimit{})
	for range 100 {
		if err := l.wait(short()); err != nil {
			t.Fatalf("unlimited server throttled a request: %v", err)
		}
	}
}
