package openbao

import (
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestProbe(t *testing.T) {
	tests := []struct {
		name, seal, health string
		want               ServerInfo
	}{
		{
			name:   "openbao",
			seal:   `{"type":"shamir","initialized":true,"sealed":false,"version":"2.7.2","commit_date":"2026-10-26T19:32:27Z"}`,
			health: `{"initialized":true,"sealed":false,"standby":true,"version":"2.7.2"}`,
			want:   ServerInfo{Version: "2.7.2", Flavor: FlavorOpenBao, Initialized: true, Standby: true},
		},
		{
			name:   "openbao before 2.6",
			seal:   `{"initialized":true,"sealed":true,"version":"2.5.0","build_date":"2025-01-01T00:00:00Z"}`,
			health: `{"initialized":true,"sealed":true,"standby":false,"version":"2.5.0"}`,
			want:   ServerInfo{Version: "2.5.0", Flavor: FlavorOpenBao, Initialized: true, Sealed: true},
		},
		{
			name:   "vault",
			seal:   `{"initialized":true,"sealed":false,"version":"1.14.4","build_date":"2023-09-22T21:29:05Z"}`,
			health: `{"initialized":true,"sealed":false,"standby":false,"performance_standby":false,"version":"1.14.4"}`,
			want:   ServerInfo{Version: "1.14.4", Flavor: FlavorVault, Initialized: true},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("X-Vault-Token") != "" {
					t.Errorf("probe sent a token")
				}
				if ns := r.Header.Get("X-Vault-Namespace"); ns != "" {
					t.Errorf("probe sent namespace %q, want the root namespace", ns)
				}
				switch r.URL.Path {
				case "/v1/sys/seal-status":
					_, _ = io.WriteString(w, tt.seal)
				case "/v1/sys/health":
					if r.URL.Query().Get("sealedcode") != "200" {
						w.WriteHeader(http.StatusServiceUnavailable)
					}
					_, _ = io.WriteString(w, tt.health)
				default:
					http.NotFound(w, r)
				}
			}))
			defer srv.Close()

			got, err := Probe(t.Context(), Config{Address: srv.URL, Namespace: "team"})
			if err != nil {
				t.Fatalf("probe: %v", err)
			}
			if got != tt.want {
				t.Errorf("got %+v, want %+v", got, tt.want)
			}
		})
	}
}
