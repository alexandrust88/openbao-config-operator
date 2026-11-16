//go:build integration

package livekit

import (
	"bytes"
	"crypto/x509"
	"encoding/pem"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/alexandrust88/openbao-config-operator/test/testenv"
)

func TestServerTLSFixture(t *testing.T) {
	if _, err := exec.LookPath("openssl"); err != nil {
		testenv.SkipOrFail(t, "openssl is required for the server TLS fixture")
	}
	dir := t.TempDir()
	script := `#!/usr/bin/env bash
set -euo pipefail
if [ "$1" = -n ]; then shift 2; fi
case "$1" in
 get) exit 1 ;;
 apply) cat >/dev/null ;;
 create)
  for arg in "$@"; do
   case "$arg" in
    --from-file=*) value=${arg#--from-file=}; cp "${value#*=}" "$FIXTURE_OUT/${value%%=*}" ;;
   esac
  done ;;
 *) exit 1 ;;
esac
`
	if err := os.WriteFile(filepath.Join(dir, "kubectl"), []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(filepath.Join(repoRoot(t), "hack/e2e/tls.sh"))
	cmd.Env = append(os.Environ(), "PATH="+dir+string(os.PathListSeparator)+os.Getenv("PATH"), "FIXTURE_OUT="+dir)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("generate server TLS fixture: %v\n%s", err, out)
	}
	read := func(name string) *x509.Certificate {
		raw, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatal(err)
		}
		block, _ := pem.Decode(raw)
		if block == nil {
			t.Fatalf("%s is not PEM", name)
		}
		cert, err := x509.ParseCertificate(block.Bytes)
		if err != nil {
			t.Fatal(err)
		}
		return cert
	}
	ca, leaf := read("ca.crt"), read("tls.crt")
	if len(ca.SubjectKeyId) == 0 || !bytes.Equal(ca.SubjectKeyId, leaf.AuthorityKeyId) {
		t.Fatal("TLS chain lacks matching subject/authority key identifiers required by strict TLS clients")
	}
	roots := x509.NewCertPool()
	roots.AddCert(ca)
	for _, host := range []string{"localhost", "127.0.0.1", "openbao-active.openbao.svc"} {
		if _, err := leaf.Verify(x509.VerifyOptions{Roots: roots, DNSName: host}); err != nil {
			t.Fatal(err)
		}
	}
}
