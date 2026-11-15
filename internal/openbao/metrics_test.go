package openbao

import (
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus/testutil"
)

// TestSteadyStateLogsInOnce guards design rule 2: reconciles reuse the cached token, so OpenBao does not accumulate
// a lease (and, for some methods, an entity alias) per reconcile.
func TestSteadyStateLogsInOnce(t *testing.T) {
	f := newFakeServer(t)
	f.loginTTL = 3600
	c, clk := newTestClient(t, appRoleConfig(f.srv.URL))
	logins := loginsTotal.WithLabelValues(t.Name(), resultSuccess)

	for range 50 {
		if _, err := c.Read(t.Context(), "kv/x"); err != nil {
			t.Fatal(err)
		}
		clk.Advance(30 * time.Second)
	}
	if got, server := testutil.ToFloat64(logins), loginCount(f); got != 1 || server != 1 {
		t.Fatalf("logins after 50 reads in 25m: metric %v, server %d; want 1", got, server)
	}

	clk.Advance(20 * time.Minute)
	if _, err := c.Read(t.Context(), "kv/x"); err != nil {
		t.Fatal(err)
	}
	if got := testutil.ToFloat64(logins); got != 2 {
		t.Fatalf("logins after the token neared expiry = %v, want 2", got)
	}
}

func TestRenewableTokenIsRenewedNotReplaced(t *testing.T) {
	f := newFakeServer(t)
	f.loginTTL, f.renewTTL, f.renewable = 3600, 3600, true
	c, clk := newTestClient(t, appRoleConfig(f.srv.URL))

	clk.Advance(21 * time.Minute)
	if _, err := c.Read(t.Context(), "kv/x"); err != nil {
		t.Fatal(err)
	}
	renewals := testutil.ToFloat64(tokenRenewalsTotal.WithLabelValues(t.Name(), resultSuccess))
	logins := testutil.ToFloat64(loginsTotal.WithLabelValues(t.Name(), resultSuccess))
	if renewals != 1 || logins != 1 {
		t.Fatalf("renewals = %v, logins = %v; want 1 and 1", renewals, logins)
	}
}

func loginCount(f *fakeServer) int {
	logins, _ := f.counts()
	return logins
}
