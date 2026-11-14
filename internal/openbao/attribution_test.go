package openbao

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

const testResource = "Policy/team/reader"

func TestWithResourceAttributionSanitises(t *testing.T) {
	long := strings.Repeat("a", 300)
	tests := map[string]string{
		testResource:                testResource,
		"Policy/team/r\neader\x00é": testResource,
		long:                        long[:maxAttributionLength],
	}
	for in, want := range tests {
		if got := attribution(WithResourceAttribution(t.Context(), in)); got != want {
			t.Errorf("attribution(%q) = %q, want %q", in, got, want)
		}
	}
	if got := attribution(t.Context()); got != "" {
		t.Errorf("attribution without a value = %q", got)
	}
}

func TestAttributionHeaderIsSent(t *testing.T) {
	var mu sync.Mutex
	seen := map[string]string{}
	f := newFakeServer(t)
	f.resource = func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		seen[r.URL.Path] = r.Header.Get(AttributionHeader)
		mu.Unlock()
		writeJSON(w, http.StatusOK, map[string]any{fieldData: map[string]any{}})
	}
	c, _ := newTestClient(t, appRoleConfig(f.srv.URL))

	if _, err := c.Write(WithResourceAttribution(t.Context(), testResource), "kv/a", map[string]any{}); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Read(t.Context(), "kv/b"); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if got := seen["/v1/kv/a"]; got != testResource {
		t.Errorf("attributed write sent %q", got)
	}
	if got := seen["/v1/kv/b"]; got != "" {
		t.Errorf("unattributed read sent %q", got)
	}
}

// TestIntegrationAttributionInAuditLog checks the end-to-end purpose: with the header configured as a non-HMAC
// audited request header, the audit log names the custom resource behind a write.
func TestIntegrationAttributionInAuditLog(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "audit.log")
	config := filepath.Join(dir, "audit.hcl")
	stanza := fmt.Sprintf("audit \"file\" \"file\" {\n  options {\n    file_path = %q\n  }\n}\n", logPath)
	if err := os.WriteFile(config, []byte(stanza), 0o600); err != nil {
		t.Fatal(err)
	}
	addr := startBaoDev(t, "-config="+config)
	ctx := t.Context()
	root, err := NewPool().Client(ctx, "it-audit", Config{
		Address: addr,
		Auth:    TokenAuth{CredentialKey: "root", Token: static("root")},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := root.Write(ctx, "sys/config/auditing/request-headers/"+strings.ToLower(AttributionHeader),
		map[string]any{"hmac": false}); err != nil {
		t.Fatalf("audit request header: %v", err)
	}

	const resource = "Policy/team/audited-reader"
	if _, err := root.Write(WithResourceAttribution(ctx, resource), "sys/policies/acl/audited-reader",
		map[string]any{fieldPolicy: `path "kv/*" { capabilities = ["read"] }`}); err != nil {
		t.Fatal(err)
	}
	// A file audit device writes the request and response entries before the response is returned.
	log, err := os.ReadFile(logPath)
	if err != nil || !strings.Contains(string(log), resource) {
		t.Fatalf("audit log does not name %q (read err %v)", resource, err)
	}
}
