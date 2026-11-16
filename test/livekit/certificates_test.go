//go:build integration

package livekit

import (
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/alexandrust88/openbao-config-operator/test/testenv"
)

func TestDatabaseTLSFixtures(t *testing.T) {
	baoBin := testenv.RequireBao(t)
	if _, err := exec.LookPath("openssl"); err != nil {
		testenv.SkipOrFail(t, "openssl is required to generate live kit certificate fixtures")
	}
	dir := t.TempDir()
	// Capture the generated Secret files without needing a Kubernetes API server.
	kubectl := `#!/usr/bin/env bash
set -euo pipefail
case "$1" in
  get) exit 1 ;;
  create)
    for arg in "$@"; do
      case "$arg" in
        --from-file=*) value=${arg#--from-file=}; cp "${value#*=}" "$FIXTURE_OUT/${value%%=*}" ;;
      esac
    done ;;
  label) ;;
  *) exit 1 ;;
esac
`
	if err := os.WriteFile(filepath.Join(dir, "kubectl"), []byte(kubectl), 0o700); err != nil {
		t.Fatal(err)
	}
	inputs := "live-db-tls client.pem pem-bundle\nlive-db-tls client.json pem-json\n"
	if err := os.WriteFile(filepath.Join(dir, "input-secrets.txt"), []byte(inputs), 0o600); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(filepath.Join(kitDir(repoRoot(t)), "create-secrets.sh"), "--namespace", "fixture", dir)
	cmd.Env = append(os.Environ(), "PATH="+dir+string(os.PathListSeparator)+os.Getenv("PATH"), "FIXTURE_OUT="+dir)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("generate database TLS fixtures: %v\n%s", err, out)
	}
	b := startBao(t, baoBin, t.TempDir(), "database-tls")
	if _, _, err := b.do("", http.MethodPost, "sys/mounts/database", map[string]any{"type": "database"}); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ file, field string }{{"client.pem", "pem_bundle"}, {"client.json", "pem_json"}} {
		t.Run(tc.field, func(t *testing.T) {
			raw, err := os.ReadFile(filepath.Join(dir, tc.file))
			if err != nil {
				t.Fatal(err)
			}
			body := map[string]any{
				"plugin_name": "influxdb-database-plugin", "verify_connection": false,
				"host": "influxdb.example.invalid", "username": "fixture", "password": "fixture-only",
				"tls": true, tc.field: string(raw),
			}
			if _, _, err := b.do("", http.MethodPost, "database/config/"+tc.field, body); err != nil {
				t.Fatal(err)
			}
		})
	}
}
