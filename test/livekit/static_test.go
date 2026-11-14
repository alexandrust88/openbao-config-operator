//go:build integration

package livekit

import (
	"fmt"
	"os"
	"path"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"testing"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"sigs.k8s.io/yaml"
)

// exceptions are the merged files of test/livekit/exceptions.
type exceptions struct {
	// FullFields lists, per kind, spec fields that no full example sets, with the reason.
	FullFields map[string]map[string]string `json:"fullFields"`
	// NotReady lists objects that do not reach Ready in the end-to-end run, with the reason.
	NotReady []notReadyException `json:"notReady"`
}

type notReadyException struct {
	Kind string `json:"kind"`
	// Name is a glob on metadata.name without the overlay prefix; empty matches every name.
	Name string `json:"name"`
	// Overlays are globs on the overlay (<mode>/<variant>); empty matches every overlay.
	Overlays []string `json:"overlays"`
	Reason   string   `json:"reason"`
}

func (e notReadyException) matches(overlay, kind, name string) bool {
	if e.Kind != kind {
		return false
	}
	if e.Name != "" {
		if ok, _ := path.Match(e.Name, name); !ok {
			return false
		}
	}
	if len(e.Overlays) == 0 {
		return true
	}
	for _, o := range e.Overlays {
		if ok, _ := path.Match(o, overlay); ok {
			return true
		}
	}
	return false
}

// loadExceptions merges every file in test/livekit/exceptions; a kind's fullFields may come from several files.
func loadExceptions(t testing.TB) exceptions {
	t.Helper()
	files, err := filepath.Glob(filepath.Join("exceptions", "*.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	all := exceptions{FullFields: map[string]map[string]string{}}
	for _, f := range files {
		raw, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		var e exceptions
		if err := yaml.UnmarshalStrict(raw, &e); err != nil {
			t.Fatalf("%s: %v", f, err)
		}
		for kind, fields := range e.FullFields {
			if all.FullFields[kind] == nil {
				all.FullFields[kind] = map[string]string{}
			}
			for field, reason := range fields {
				if reason == "" {
					t.Fatalf("%s: fullFields %s.%s has no reason", f, kind, field)
				}
				all.FullFields[kind][field] = reason
			}
		}
		for _, n := range e.NotReady {
			if n.Kind == "" || n.Reason == "" {
				t.Fatalf("%s: notReady entry without kind or reason: %+v", f, n)
			}
		}
		all.NotReady = append(all.NotReady, e.NotReady...)
	}
	return all
}

// TestRender builds every overlay and checks the naming contract: references resolve inside the build or name
// a declared input, OpenBao names carry the overlay prefix, and outputs are written to prefixed Secrets.
func TestRender(t *testing.T) {
	root := repoRoot(t)
	kit := kitDir(root)
	kustomize := kustomizeBin(t, root)
	p := params(t, kit)
	comps := components(t, kit)
	compDirs := make([]string, 0, len(comps))
	for _, c := range comps {
		compDirs = append(compDirs, c.dir)
	}
	inputs := map[string]bool{}
	for _, in := range inputSecrets(t, compDirs...) {
		inputs[in.secret] = true
	}
	selfNamed := selfNamedFields(t, kit)
	for _, overlay := range overlays(t, kit) {
		t.Run(overlay, func(t *testing.T) {
			objs := render(t, kustomize, filepath.Join(kit, overlay))
			names := map[string]bool{}
			for _, o := range objs {
				names[o.GetName()] = true
			}
			for _, o := range objs {
				if !isOperatorKind(o) {
					continue
				}
				prefix := o.GetLabels()[labelOverlay] + "-"
				if prefix == "-" {
					t.Errorf("%s/%s: no %s label", o.GetKind(), o.GetName(), labelOverlay)
					continue
				}
				id := o.GetKind() + "/" + o.GetName()
				if !strings.HasPrefix(o.GetName(), prefix) {
					t.Errorf("%s: name lacks the overlay prefix %q", id, prefix)
				}
				for _, r := range refs(o.Object[keySpec], "") {
					checkRef(t, id, r, prefix, names, inputs, p)
				}
				if coreKinds[o.GetKind()] {
					continue
				}
				for _, field := range selfNamed[o.GetKind()] {
					if v, ok, _ := unstructured.NestedString(o.Object, keySpec, field); ok && v != o.GetName() {
						t.Errorf("%s: spec.%s %q must equal metadata.name, so the overlay prefix reaches OpenBao", id, field, v)
					}
				}
				for _, mp := range mountPaths(o.Object[keySpec]) {
					if !strings.HasPrefix(mp, prefix) {
						t.Errorf("%s: mountPath %q names no mount of this overlay (want prefix %q)", id, mp, prefix)
					}
				}
			}
		})
	}
}

type ref struct{ field, name string }

// refs returns every name under a field ending in Ref or Refs, with the field name.
func refs(v any, field string) []ref {
	var out []ref
	switch x := v.(type) {
	case map[string]any:
		if name, ok := x[keyName].(string); ok && (strings.HasSuffix(field, "Ref") || strings.HasSuffix(field, "Refs")) {
			out = append(out, ref{field: field, name: name})
		}
		for k, child := range x {
			out = append(out, refs(child, k)...)
		}
	case []any:
		for _, child := range x {
			out = append(out, refs(child, field)...)
		}
	}
	return out
}

// mountPaths returns every string value of a mountPath field below v.
func mountPaths(v any) []string {
	var out []string
	switch x := v.(type) {
	case map[string]any:
		for k, child := range x {
			if s, ok := child.(string); ok && k == "mountPath" {
				out = append(out, s)
			} else {
				out = append(out, mountPaths(child)...)
			}
		}
	case []any:
		for _, child := range x {
			out = append(out, mountPaths(child)...)
		}
	}
	return out
}

func isInputSecretRef(field string) bool {
	return field != "writeSecretRef" && (strings.Contains(field, "Secret") || strings.Contains(field, "secret"))
}

func checkRef(t *testing.T, id string, r ref, prefix string, names, inputs map[string]bool, p map[string]string) {
	t.Helper()
	switch {
	case r.field == "serviceAccountRef":
		if !slices.Contains([]string{p["rootServiceAccount"], p["tenantServiceAccount"], p["childServiceAccount"]}, r.name) {
			t.Errorf("%s: serviceAccountRef %q is not a ServiceAccount from the live-params literals", id, r.name)
		}
	case r.field == "configMapRef":
		if r.name != p["caConfigMap"] && !names[r.name] {
			t.Errorf("%s: configMapRef %q is neither caConfigMap nor in the build", id, r.name)
		}
	case r.field == "writeSecretRef":
		if !strings.HasPrefix(r.name, prefix) {
			t.Errorf("%s: writeSecretRef %q lacks the overlay prefix %q; name it after its resource", id, r.name, prefix)
		}
	case isInputSecretRef(r.field):
		if !inputs[r.name] {
			t.Errorf("%s: %s %q is not declared in any input-secrets.txt", id, r.field, r.name)
		}
	default:
		if !names[r.name] {
			t.Errorf("%s: %s %q is not in the build (a reference kustomize did not rename? see base/nameref.yaml)",
				id, r.field, r.name)
		}
	}
}

// selfNamedFields reads base/nameref.yaml and returns, per kind, the spec fields that name the object itself in
// OpenBao (a fieldSpec of the kind under its own referent entry).
func selfNamedFields(t testing.TB, kit string) map[string][]string {
	t.Helper()
	out := map[string][]string{}
	for _, nr := range nameRefConfig(t, kit) {
		for _, fs := range nr.FieldSpecs {
			if fs.Kind == nr.Kind && strings.Count(fs.Path, "/") == 1 {
				out[nr.Kind] = append(out[nr.Kind], strings.TrimPrefix(fs.Path, "spec/"))
			}
		}
	}
	return out
}

type nameRef struct {
	Kind       string `json:"kind"`
	FieldSpecs []struct {
		Kind string `json:"kind"`
		Path string `json:"path"`
	} `json:"fieldSpecs"`
}

func nameRefConfig(t testing.TB, kit string) []nameRef {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(kit, "base", "nameref.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	var cfg struct {
		NameReference []nameRef `json:"nameReference"`
	}
	if err := yaml.UnmarshalStrict(raw, &cfg); err != nil {
		t.Fatal(err)
	}
	return cfg.NameReference
}

// TestNameRefs fails when a reference field of a built-in CRD is missing from base/nameref.yaml, so a namePrefix
// would leave it pointing at the unprefixed name. Input Secrets and ServiceAccounts live outside the build.
func TestNameRefs(t *testing.T) {
	root := repoRoot(t)
	kit := kitDir(root)
	covered := map[string]bool{}
	for _, nr := range nameRefConfig(t, kit) {
		for _, fs := range nr.FieldSpecs {
			covered[fs.Kind+" "+fs.Path] = true
		}
	}
	for _, k := range sortedKinds(crdKinds(t, root)) {
		if !k.Builtin {
			continue
		}
		for _, p := range refPaths(k.Spec, "spec") {
			field := p[strings.LastIndex(strings.TrimSuffix(p, "/name"), "/")+1 : len(p)-len("/name")]
			if field == "serviceAccountRef" || isInputSecretRef(field) {
				continue
			}
			if !covered[k.Kind+" "+p] && !covered[" "+p] {
				t.Errorf("%s: %s is not in examples/live/base/nameref.yaml", k.Kind, p)
			}
		}
	}
}

// refPaths returns the kustomize field paths (slash separated, lists traversed implicitly) of every name under a
// property ending in Ref or Refs.
func refPaths(schema map[string]any, prefix string) []string {
	var out []string
	props, _ := schema["properties"].(map[string]any)
	for k, raw := range props {
		v, _ := raw.(map[string]any)
		if v["type"] == "array" {
			v, _ = v["items"].(map[string]any)
		}
		p := prefix + "/" + k
		sub, _ := v["properties"].(map[string]any)
		if _, ok := sub[keyName]; ok && (strings.HasSuffix(k, "Ref") || strings.HasSuffix(k, "Refs")) {
			out = append(out, p+"/name")
		}
		out = append(out, refPaths(v, p)...)
	}
	return out
}

func sortedKinds(m map[string]*kindInfo) []*kindInfo {
	out := make([]*kindInfo, 0, len(m))
	for _, k := range m {
		out = append(out, k)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Kind < out[j].Kind })
	return out
}

// TestCoverage fails when a built-in kind has no minimal or no full example in the kit, and keeps the coverage
// table in examples/live/coverage.md current (LIVEKIT_UPDATE=1 rewrites it).
func TestCoverage(t *testing.T) {
	root := repoRoot(t)
	kit := kitDir(root)
	kinds := crdKinds(t, root)
	cover := map[string]map[string][]string{}
	for _, c := range components(t, kit) {
		for _, d := range c.docs {
			if !isOperatorKind(d) {
				continue
			}
			if cover[d.GetKind()] == nil {
				cover[d.GetKind()] = map[string][]string{}
			}
			if !slices.Contains(cover[d.GetKind()][c.variant], c.scenario) {
				cover[d.GetKind()][c.variant] = append(cover[d.GetKind()][c.variant], c.scenario)
			}
		}
	}
	var b strings.Builder
	b.WriteString("# Live kit coverage\n\nGenerated by `make test-livekit` (`LIVEKIT_UPDATE=1` rewrites it). " +
		"Every built-in kind and the components that hold its minimal and full examples.\n\n" +
		"| kind | group | minimal | full |\n|------|-------|---------|------|\n")
	n := 0
	for _, k := range sortedKinds(kinds) {
		if !k.Builtin {
			continue
		}
		n++
		mins, fulls := cover[k.Kind][variantMin], cover[k.Kind][variantFull]
		if len(mins) == 0 {
			t.Errorf("%s: no minimal example in examples/live/components/*/minimal", k.Kind)
		}
		if len(fulls) == 0 {
			t.Errorf("%s: no full example in examples/live/components/*/full", k.Kind)
		}
		fmt.Fprintf(&b, "| `%s` | %s | %s | %s |\n", k.Kind, strings.SplitN(k.Group, ".", 2)[0],
			strings.Join(mins, ", "), strings.Join(fulls, ", "))
	}
	fmt.Fprintf(&b, "\n%d built-in kinds.\n", n)
	out := filepath.Join(kit, "coverage.md")
	if os.Getenv("LIVEKIT_UPDATE") != "" {
		if err := os.WriteFile(out, []byte(b.String()), 0o600); err != nil {
			t.Fatal(err)
		}
		return
	}
	got, err := os.ReadFile(out)
	if err != nil || string(got) != b.String() {
		t.Errorf("examples/live/coverage.md is stale; run LIVEKIT_UPDATE=1 make test-livekit")
	}
}

// TestFullFields fails when a spec field of a built-in kind is set by none of the kind's full examples, unless
// an exceptions file names it with a reason. One-of alternatives are covered by further documents of the kind.
func TestFullFields(t *testing.T) {
	root := repoRoot(t)
	kit := kitDir(root)
	exc := loadExceptions(t)
	docs := map[string][]*unstructured.Unstructured{}
	for _, c := range components(t, kit) {
		if c.variant != variantFull {
			continue
		}
		for _, d := range c.docs {
			docs[d.GetKind()] = append(docs[d.GetKind()], d)
		}
	}
	for _, k := range sortedKinds(crdKinds(t, root)) {
		if !k.Builtin {
			continue
		}
		known := map[string]bool{}
		var missing []string
		for _, p := range leafPaths(k.Spec, "") {
			known[p] = true
			if p == "namespace" {
				continue
			}
			set := false
			for _, d := range docs[k.Kind] {
				if hasPath(d.Object[keySpec], strings.Split(p, ".")) {
					set = true
					break
				}
			}
			if set {
				if _, ok := exc.FullFields[k.Kind][p]; ok {
					t.Errorf("%s: an exceptions file excuses spec.%s, but a full example sets it", k.Kind, p)
				}
				continue
			}
			if _, ok := exc.FullFields[k.Kind][p]; !ok {
				missing = append(missing, p)
			}
		}
		for p := range exc.FullFields[k.Kind] {
			if !known[p] {
				t.Errorf("%s: an exceptions file names spec.%s, which the CRD does not have", k.Kind, p)
			}
		}
		if len(missing) > 0 {
			sort.Strings(missing)
			t.Errorf("%s: %d spec fields set by no full example: %s", k.Kind, len(missing), strings.Join(missing, ", "))
		}
	}
	for kind := range exc.FullFields {
		if docs[kind] == nil {
			t.Errorf("an exceptions file names %s, which has no full example", kind)
		}
	}
}

// leafPaths returns the dotted paths of every leaf below a schema; a list of objects adds "[]" to its segment and a
// map or a list of scalars is a leaf.
func leafPaths(schema map[string]any, prefix string) []string {
	var out []string
	props, _ := schema["properties"].(map[string]any)
	for k, raw := range props {
		v, _ := raw.(map[string]any)
		p := k
		if prefix != "" {
			p = prefix + "." + k
		}
		if v["type"] == "array" {
			items, _ := v["items"].(map[string]any)
			if _, ok := items["properties"]; ok {
				out = append(out, leafPaths(items, p+"[]")...)
				continue
			}
		}
		if _, ok := v["properties"]; ok {
			out = append(out, leafPaths(v, p)...)
			continue
		}
		out = append(out, p)
	}
	return out
}

func hasPath(v any, segs []string) bool {
	if len(segs) == 0 {
		return true
	}
	m, ok := v.(map[string]any)
	if !ok {
		return false
	}
	seg := segs[0]
	if list, ok := strings.CutSuffix(seg, "[]"); ok {
		items, _ := m[list].([]any)
		for _, item := range items {
			if hasPath(item, segs[1:]) {
				return true
			}
		}
		return false
	}
	child, ok := m[seg]
	return ok && hasPath(child, segs[1:])
}
