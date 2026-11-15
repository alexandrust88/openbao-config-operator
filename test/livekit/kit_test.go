//go:build integration

// Package livekit checks the live test kit in examples/live: every overlay renders with kustomize, every built-in
// kind has a minimal and a full example and the full examples set every spec field, every rendered object passes
// the API server's validation of the real CRDs, and the root and child overlays reach Ready against
// `bao server -dev` with the manager built from ./cmd.
package livekit

import (
	"bytes"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	utilyaml "k8s.io/apimachinery/pkg/util/yaml"
	"sigs.k8s.io/yaml"

	"github.com/alexandrust88/openbao-config-operator/test/testenv"
)

const (
	keyName       = "name"
	keySpec       = "spec"
	kindSecret    = "Secret"
	kindConfigMap = "ConfigMap"
	labelNested   = "live.openbaoconfig.io/nested"
	labelOverlay  = "live.openbaoconfig.io/overlay"
	variantMin    = "minimal"
	variantFull   = "full"
)

// coreKinds are configured by the kit's base and modes rather than by the scenario components.
var coreKinds = map[string]bool{"OpenBaoServer": true, "OpenBaoConnection": true, "ClusterOpenBaoConnection": true}

func repoRoot(t testing.TB) string {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	return root
}

func kitDir(root string) string { return filepath.Join(root, "examples", "live") }

// kustomizeBin returns $KUSTOMIZE or bin/kustomize, and skips t (fails it in CI) when neither exists.
func kustomizeBin(t testing.TB, root string) string {
	t.Helper()
	bin := os.Getenv("KUSTOMIZE")
	if bin == "" {
		bin = filepath.Join(root, "bin", "kustomize")
	}
	if _, err := os.Stat(bin); err != nil {
		testenv.SkipOrFail(t, "kustomize not found; run make kustomize or set KUSTOMIZE")
	}
	return bin
}

// render runs kustomize build on dir and decodes the output.
func render(t testing.TB, kustomize, dir string) []*unstructured.Unstructured {
	t.Helper()
	cmd := exec.Command(kustomize, "build", dir)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("kustomize build %s: %v\n%s", dir, err, stderr.String())
	}
	return decode(t, dir, out)
}

func decode(t testing.TB, origin string, raw []byte) []*unstructured.Unstructured {
	t.Helper()
	decoder := utilyaml.NewYAMLOrJSONDecoder(bytes.NewReader(raw), 4096)
	var objs []*unstructured.Unstructured
	for {
		obj := &unstructured.Unstructured{}
		err := decoder.Decode(&obj.Object)
		if errors.Is(err, io.EOF) {
			return objs
		}
		if err != nil {
			t.Fatalf("decode %s: %v", origin, err)
		}
		if len(obj.Object) > 0 {
			objs = append(objs, obj)
		}
	}
}

func decodeFile(t testing.TB, path string) []*unstructured.Unstructured {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return decode(t, path, raw)
}

// overlays returns the overlay directories below examples/live/overlays (<mode>/<variant>), relative to the kit.
func overlays(t testing.TB, kit string) []string {
	t.Helper()
	matches, err := filepath.Glob(filepath.Join(kit, "overlays", "*", "*", "kustomization.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	out := make([]string, 0, len(matches))
	for _, m := range matches {
		rel, _ := filepath.Rel(kit, filepath.Dir(m))
		out = append(out, rel)
	}
	sort.Strings(out)
	if len(out) == 0 {
		t.Fatal("no overlays found")
	}
	return out
}

// component is one scenario variant directory: components/<scenario>/<variant>.
type component struct {
	scenario, variant, dir string
	docs                   []*unstructured.Unstructured
}

// components loads every scenario variant with the documents its kustomization lists as resources, plus a
// pseudo-component "connections" made of the base and the root mode, which declare the connection kinds.
func components(t testing.TB, kit string) []component {
	t.Helper()
	matches, err := filepath.Glob(filepath.Join(kit, "components", "*", "*", "kustomization.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	out := make([]component, 0, len(matches)+2)
	for _, m := range matches {
		dir := filepath.Dir(m)
		c := component{scenario: filepath.Base(filepath.Dir(dir)), variant: filepath.Base(dir), dir: dir}
		c.docs = resourcesOf(t, dir)
		out = append(out, c)
	}
	conn := resourcesOf(t, filepath.Join(kit, "base"))
	conn = append(conn, resourcesOf(t, filepath.Join(kit, "modes", "root"))...)
	for _, v := range []string{variantMin, variantFull} {
		out = append(out, component{scenario: "connections", variant: v, dir: filepath.Join(kit, "base"), docs: conn})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].scenario != out[j].scenario {
			return out[i].scenario < out[j].scenario
		}
		return out[i].variant < out[j].variant
	})
	return out
}

func resourcesOf(t testing.TB, dir string) []*unstructured.Unstructured {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(dir, "kustomization.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	var k struct {
		Resources []string `json:"resources"`
	}
	if err := yaml.Unmarshal(raw, &k); err != nil {
		t.Fatalf("%s: %v", dir, err)
	}
	docs := make([]*unstructured.Unstructured, 0, len(k.Resources))
	for _, r := range k.Resources {
		docs = append(docs, decodeFile(t, filepath.Join(dir, r))...)
	}
	return docs
}

// kindInfo is one CRD: its kind, group and spec schema.
type kindInfo struct {
	Kind, Group string
	Builtin     bool
	Namespaced  bool
	Spec        map[string]any
}

// crdKinds reads every CRD in config/crd/bases and marks the kinds docs/kinds.md lists as built in.
func crdKinds(t testing.TB, root string) map[string]*kindInfo {
	t.Helper()
	avail := availability(t, filepath.Join(root, "docs", "kinds.md"))
	files, err := filepath.Glob(filepath.Join(root, "config", "crd", "bases", "*.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]*kindInfo{}
	for _, f := range files {
		raw, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		var crd struct {
			Spec struct {
				Group string `json:"group"`
				Scope string `json:"scope"`
				Names struct {
					Kind string `json:"kind"`
				} `json:"names"`
				Versions []struct {
					Schema struct {
						OpenAPIV3Schema map[string]any `json:"openAPIV3Schema"`
					} `json:"schema"`
				} `json:"versions"`
			} `json:"spec"`
		}
		if err := yaml.Unmarshal(raw, &crd); err != nil {
			t.Fatalf("%s: %v", f, err)
		}
		if len(crd.Spec.Versions) == 0 {
			t.Fatalf("%s: CRD has no versions", f)
		}
		props, _ := crd.Spec.Versions[0].Schema.OpenAPIV3Schema["properties"].(map[string]any)
		spec, _ := props[keySpec].(map[string]any)
		a := avail[crd.Spec.Names.Kind]
		if !strings.HasPrefix(a, "builtin") && !strings.HasPrefix(a, "external-plugin") {
			t.Fatalf("%s: missing or unknown availability %q in docs/kinds.md", crd.Spec.Names.Kind, a)
		}
		out[crd.Spec.Names.Kind] = &kindInfo{
			Kind:       crd.Spec.Names.Kind,
			Group:      crd.Spec.Group,
			Builtin:    !strings.HasPrefix(a, "external-plugin"),
			Namespaced: crd.Spec.Scope == "Namespaced",
			Spec:       spec,
		}
	}
	return out
}

// availability reads the availability column of docs/kinds.md by kind.
func availability(t testing.TB, path string) map[string]string {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]string{}
	for line := range strings.SplitSeq(string(raw), "\n") {
		if !strings.HasPrefix(line, "| `") {
			continue
		}
		cols := strings.Split(strings.ReplaceAll(line, `\|`, "/"), "|")
		if len(cols) < 4 {
			continue
		}
		out[strings.Trim(strings.TrimSpace(cols[1]), "`")] = strings.TrimSpace(cols[3])
	}
	return out
}

// inputSecret is one line of an input-secrets.txt: a key of a Secret the user (or create-secrets.sh) creates.
type inputSecret struct{ secret, key, generator string }

func inputSecrets(t testing.TB, dirs ...string) []inputSecret {
	t.Helper()
	var out []inputSecret
	for _, dir := range dirs {
		raw, err := os.ReadFile(filepath.Join(dir, "input-secrets.txt"))
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			t.Fatal(err)
		}
		for line := range strings.SplitSeq(string(raw), "\n") {
			line, _, _ = strings.Cut(line, "#")
			f := strings.Fields(line)
			if len(f) == 0 {
				continue
			}
			if len(f) != 3 {
				t.Fatalf("%s/input-secrets.txt: want <secret> <key> <generator>, got %q", dir, line)
			}
			out = append(out, inputSecret{f[0], f[1], f[2]})
		}
	}
	return out
}

// params reads the live-params literals of examples/live/params/kustomization.yaml.
func params(t testing.TB, kit string) map[string]string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(kit, "params", "kustomization.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	var k struct {
		ConfigMapGenerator []struct {
			Name     string   `json:"name"`
			Literals []string `json:"literals"`
		} `json:"configMapGenerator"`
	}
	if err := yaml.Unmarshal(raw, &k); err != nil {
		t.Fatal(err)
	}
	out := map[string]string{}
	for _, g := range k.ConfigMapGenerator {
		if g.Name != "live-params" {
			continue
		}
		for _, lit := range g.Literals {
			if key, v, ok := strings.Cut(lit, "="); ok {
				out[key] = v
			}
		}
	}
	if len(out) == 0 {
		t.Fatalf("%s/params/kustomization.yaml: no live-params literals", kit)
	}
	return out
}

// overlayPrefix returns the namePrefix of an overlay kustomization ("" when it sets none).
func overlayPrefix(t testing.TB, dir string) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(dir, "kustomization.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	var k struct {
		NamePrefix string `json:"namePrefix"`
	}
	if err := yaml.Unmarshal(raw, &k); err != nil {
		t.Fatal(err)
	}
	return k.NamePrefix
}

// managed reports an object reconciled through the engine (it has a connectionRef).
func managed(o *unstructured.Unstructured) bool {
	_, found, _ := unstructured.NestedMap(o.Object, keySpec, "connectionRef")
	return found
}

func isOperatorKind(o *unstructured.Unstructured) bool {
	return strings.HasSuffix(o.GroupVersionKind().Group, ".openbaoconfig.io")
}
