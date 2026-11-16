//go:build integration

package livekit

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"github.com/alexandrust88/openbao-config-operator/test/testenv"
)

func preparePluginFixtures(t *testing.T, root, logDir string, r *run, bin *string) {
	t.Helper()
	if !slices.ContainsFunc(r.objs, func(o *unstructured.Unstructured) bool { return o.GetKind() == "Plugin" }) {
		return
	}
	if *bin == "" {
		*bin = buildPluginFixture(t, root)
	}
	checksum := installPluginFixture(t, *bin, filepath.Join(logDir, strings.ReplaceAll(r.overlay, "/", "-")+"-plugins"))
	for _, obj := range r.objs {
		rewritePluginFixture(t, obj, checksum)
	}
}

func buildPluginFixture(t *testing.T, root string) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "livekit-plugin")
	cmd := exec.Command("go", "build", "-mod=readonly", "-trimpath", "-o", bin, ".")
	cmd.Dir = filepath.Join(root, "test", "livekit", "pluginfixture")
	cmd.Env = append(os.Environ(), "CGO_ENABLED=0")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build live plugin fixture: %v\n%s", err, out)
	}
	return bin
}

func installPluginFixture(t *testing.T, bin, dir string) string {
	t.Helper()
	raw, err := os.ReadFile(bin)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatal(err)
	}
	for _, command := range []string{"openbao-plugin-secrets-live-kit", "openbao-plugin-auth-live-kit"} {
		if err := os.WriteFile(filepath.Join(dir, command), raw, 0o750); err != nil {
			t.Fatal(err)
		}
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

func rewritePluginFixture(t *testing.T, obj *unstructured.Unstructured, checksum string) {
	t.Helper()
	if obj.GetKind() != "Plugin" {
		return
	}
	command, _, err := unstructured.NestedString(obj.Object, keySpec, "command")
	if err != nil {
		t.Fatal(err)
	}
	if command != "openbao-plugin-secrets-live-kit" && command != "openbao-plugin-auth-live-kit" {
		t.Fatalf("no live plugin fixture for command %q", command)
	}
	if oci, _, err := unstructured.NestedBool(obj.Object, keySpec, "oci"); err != nil || oci {
		t.Fatalf("binary fixture cannot cover OCI plugin %s: oci=%v err=%v", obj.GetName(), oci, err)
	}
	if err := unstructured.SetNestedField(obj.Object, checksum, keySpec, "sha256"); err != nil {
		t.Fatal(err)
	}
}

func verifyPluginFixtures(t *testing.T, r *run) {
	t.Helper()
	for _, obj := range r.objs {
		if obj.GetKind() != "SecretsMount" && obj.GetKind() != "AuthMount" {
			continue
		}
		if _, found, _ := unstructured.NestedMap(obj.Object, keySpec, "pluginRef"); !found {
			continue
		}
		path, _, _ := unstructured.NestedString(obj.Object, keySpec, "path")
		ns := resourceNamespace(t, r.objs, obj)
		kind := "secret"
		if obj.GetKind() == "AuthMount" {
			kind, path = "auth", "auth/"+path
		}
		if err := pluginHealth(r.bao, ns, path, kind); err != nil {
			t.Errorf("%s: mounted plugin %s: %v", r.overlay, obj.GetName(), err)
		}
	}
}

func pluginHealth(b *bao, ns, path, kind string) error {
	response, _, err := b.do(ns, http.MethodGet, path+"/health", nil)
	if err != nil {
		return err
	}
	data, _ := response["data"].(map[string]any)
	if data["fixture"] != "livekit" || data["type"] != kind {
		return fmt.Errorf("plugin health did not identify the %s fixture", kind)
	}
	return nil
}

func TestExternalPluginFixture(t *testing.T) {
	bin := testenv.RequireBao(t)
	root := repoRoot(t)
	fixture := buildPluginFixture(t, root)
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	b := startBao(t, bin, dir, "plugin-fixture")
	checksum := installPluginFixture(t, fixture, filepath.Join(dir, "plugin-fixture-plugins"))
	for _, kind := range []string{"secret", "auth"} {
		t.Run(kind, func(t *testing.T) {
			name, command := "livekit-secret", "openbao-plugin-secrets-live-kit"
			mountPath, healthPath := "sys/mounts/livekit-secret", "livekit-secret"
			if kind == "auth" {
				name, command = "livekit-auth", "openbao-plugin-auth-live-kit"
				mountPath, healthPath = "sys/auth/livekit-auth", "auth/livekit-auth"
			}
			if _, _, err := b.do("", http.MethodPost, "sys/plugins/catalog/"+kind+"/"+name,
				map[string]any{"command": command, "sha256": checksum, "args": []string{"-tls-skip-verify=false"}}); err != nil {
				t.Fatal(err)
			}
			if _, _, err := b.do("", http.MethodPost, mountPath, map[string]any{"type": name}); err != nil {
				t.Fatal(err)
			}
			if err := pluginHealth(b, "", healthPath, kind); err != nil {
				t.Fatal(err)
			}
		})
	}
}
