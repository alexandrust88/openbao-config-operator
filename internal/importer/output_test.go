package importer

import (
	"bytes"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"sigs.k8s.io/controller-runtime/pkg/client"

	sysv1alpha1 "github.com/alexandrust88/openbao-config-operator/api/sys/v1alpha1"
)

func testPolicy(name string) *sysv1alpha1.ACLPolicy {
	p := &sysv1alpha1.ACLPolicy{}
	p.SetGroupVersionKind(sysv1alpha1.GroupVersion.WithKind("ACLPolicy"))
	p.Name, p.Namespace = name, litObco
	p.Spec.Name, p.Spec.Policy = name, `path "x" { capabilities = ["read"] }`
	return p
}

func TestWriteStream(t *testing.T) {
	var buf bytes.Buffer
	if err := WriteStream(&buf, []client.Object{testPolicy("a"), testPolicy("b")}); err != nil {
		t.Fatal(err)
	}
	want := `apiVersion: sys.obco.dev/v1alpha1
kind: ACLPolicy
metadata:
  name: a
  namespace: obco
spec:
  connectionRef:
    name: ""
  name: a
  policy: path "x" { capabilities = ["read"] }
---
apiVersion: sys.obco.dev/v1alpha1
kind: ACLPolicy
metadata:
  name: b
  namespace: obco
spec:
  connectionRef:
    name: ""
  name: b
  policy: path "x" { capabilities = ["read"] }
`
	if got := buf.String(); got != want {
		t.Errorf("stream:\n%s\nwant:\n%s", got, want)
	}
}

func TestWriteDir(t *testing.T) {
	dir := t.TempDir()
	files, err := WriteDir(filepath.Join(dir, "out"), []client.Object{testPolicy("a"), testPolicy("b")})
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(dir, "out", "sys-aclpolicy.yaml")
	if !slices.Equal(files, []string{want}) {
		t.Fatalf("files = %v, want %v", files, []string{want})
	}
	data, err := os.ReadFile(want)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Count(data, []byte("kind: ACLPolicy")) != 2 {
		t.Errorf("file holds:\n%s", data)
	}
	if err := os.Chmod(want, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := WriteDir(filepath.Join(dir, "out"), []client.Object{testPolicy("a")}); err != nil {
		t.Fatal(err)
	}
	if info, err := os.Stat(want); err != nil || info.Mode().Perm() != 0o600 {
		t.Errorf("mode = %v, %v; want 0600 even for a file that existed", info.Mode().Perm(), err)
	}
}
