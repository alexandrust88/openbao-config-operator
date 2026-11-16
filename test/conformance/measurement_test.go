//go:build conformance

package conformance

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func TestConformanceSteadyRequiresObservation(t *testing.T) {
	for _, tc := range []struct {
		name     string
		advances bool
		status   int
		wantErr  bool
	}{
		{"observed reconcile", true, http.StatusOK, false},
		{"no reconcile", false, http.StatusOK, true},
		{"metrics unavailable", false, http.StatusServiceUnavailable, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var requests atomic.Int64
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tc.status)
				var reconciles int64
				if tc.advances {
					reconciles = requests.Add(1)
				}
				_, _ = fmt.Fprintf(w, "go_goroutines 10\nopenbao_writes_total{kind=\"Policy\"} 0\n"+
					"controller_runtime_reconcile_total{controller=\"policy.sys.openbaoconfig.io\",result=\"success\"} %d\n", reconciles)
			}))
			defer server.Close()
			mgr := &confManager{metricsURL: server.URL}
			kind := &confKind{Kind: "Policy", Group: "sys.openbaoconfig.io"}
			writes, err := confSteady(mgr, kind, 1, time.Nanosecond)
			if (err != nil) != tc.wantErr {
				t.Fatalf("steady writes=%d err=%v, want error=%v", writes, err, tc.wantErr)
			}
			if !tc.wantErr && writes != 0 {
				t.Fatalf("writes=%d, want zero", writes)
			}
		})
	}
}
