//go:build integration

package livekit

import (
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"sigs.k8s.io/yaml"
)

type coverageExclusion struct {
	Kind     string `json:"kind"`
	Reason   string `json:"reason"`
	File     string `json:"file"`
	Test     string `json:"test"`
	Scenario string `json:"scenario"`
}

type coverageRow struct {
	Kind      string   `json:"kind"`
	Group     string   `json:"group"`
	Class     string   `json:"class"`
	Reason    string   `json:"reason,omitempty"`
	Evidence  string   `json:"evidence,omitempty"`
	Scenarios []string `json:"scenarios,omitempty"`
	Ready     int      `json:"readyObjects"`
}

func coverageExclusions(t testing.TB, root string, kinds map[string]*kindInfo) map[string]coverageExclusion {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(root, "test/livekit/coverage-exclusions.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	var entries []coverageExclusion
	if err := yaml.UnmarshalStrict(raw, &entries); err != nil {
		t.Fatal(err)
	}
	out := map[string]coverageExclusion{}
	for _, e := range entries {
		k, exists := kinds[e.Kind]
		if !exists || !k.Builtin || e.Reason == "" || e.File == "" || e.Test == "" || e.Scenario == "" {
			t.Fatalf("invalid live coverage exclusion: %+v", e)
		}
		if _, exists := out[e.Kind]; exists {
			t.Fatalf("duplicate live coverage exclusion: %s", e.Kind)
		}
		source, err := parser.ParseFile(token.NewFileSet(), filepath.Join(root, e.File), nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		functions := map[string]bool{}
		for _, decl := range source.Decls {
			if fn, ok := decl.(*ast.FuncDecl); ok && fn.Recv == nil {
				functions[fn.Name.Name] = true
			}
		}
		if !strings.HasPrefix(e.Test, "Test") || !functions[e.Test] || !functions[e.Scenario] {
			t.Fatalf("%s: dedicated test/scenario does not exist in %s", e.Kind, e.File)
		}
		out[e.Kind] = e
	}
	return out
}

// liveInventory checks the executed overlay union, not merely YAML files that may never be included in a build.
func liveInventory(t testing.TB) []coverageRow {
	t.Helper()
	root := repoRoot(t)
	kinds := crdKinds(t, root)
	excluded := coverageExclusions(t, root, kinds)
	exceptions := loadExceptions(t)
	kustomize := kustomizeBin(t, root)
	covered := map[string][]string{}
	for overlay := range strings.SplitSeq(defaultE2EOverlays, ",") {
		dir := filepath.Join(kitDir(root), "overlays", overlay)
		r := &run{overlay: overlay, prefix: overlayPrefix(t, dir)}
		for _, obj := range render(t, kustomize, dir) {
			if !isOperatorKind(obj) || excuse(exceptions, r, obj) != "" {
				continue
			}
			kind := obj.GetKind()
			if kinds[kind] == nil {
				t.Fatalf("%s: rendered kind %s has no CRD", overlay, kind)
			}
			covered[kind] = append(covered[kind], overlay+":"+obj.GetName())
		}
	}
	pluginFiles, err := os.ReadFile(filepath.Join(root, "hack/chart/plugin-crds.txt"))
	if err != nil {
		t.Fatal(err)
	}
	pluginKinds := map[string]bool{}
	for file := range strings.FieldsSeq(string(pluginFiles)) {
		for _, obj := range decodeFile(t, filepath.Join(root, "config/crd/bases", file)) {
			names, _, _ := unstructured.NestedString(obj.Object, "spec", "names", "kind")
			pluginKinds[names] = true
		}
	}
	rows := make([]coverageRow, 0, len(kinds))
	for _, k := range sortedKinds(kinds) {
		if pluginKinds[k.Kind] == k.Builtin {
			t.Fatalf("%s: plugin-crds.txt and catalogue availability disagree", k.Kind)
		}
		r := coverageRow{Kind: k.Kind, Group: k.Group, Scenarios: covered[k.Kind]}
		switch {
		case !k.Builtin:
			r.Class, r.Reason = "plugin-excluded", "Not compiled into pinned OpenBao; needs its external plugin and backend. Payload/schema tests are not live coverage."
		case excluded[k.Kind].Kind != "":
			e := excluded[k.Kind]
			r.Class, r.Reason, r.Evidence = "dedicated-suite", e.Reason, e.File+":"+e.Test+"/"+e.Scenario
			if len(r.Scenarios) > 0 {
				t.Errorf("%s now has an unexcused overlay scenario; remove its obsolete coverage exclusion", k.Kind)
			}
		default:
			r.Class = "live-overlay"
			if len(r.Scenarios) == 0 {
				t.Errorf("%s has no unexcused scenario in the default executed overlays", k.Kind)
			}
		}
		rows = append(rows, r)
	}
	return rows
}

func TestLiveInventory(t *testing.T) {
	rows := liveInventory(t)
	counts := map[string]int{}
	for _, row := range rows {
		counts[row.Class]++
	}
	t.Logf("%d CRDs: %d live-overlay, %d dedicated-suite, %d plugin-excluded; inventory is not proof of a successful live run",
		len(rows), counts["live-overlay"], counts["dedicated-suite"], counts["plugin-excluded"])
	if path := os.Getenv("LIVEKIT_INVENTORY_OUT"); path != "" {
		writeInventory(t, path, rows)
	}
}

func writeInventory(t testing.TB, path string, rows []coverageRow) {
	t.Helper()
	raw, err := json.MarshalIndent(rows, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, append(raw, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
}

func verifyLiveInventory(t testing.TB, rows []coverageRow, runs []*run, complete bool) {
	t.Helper()
	for i := range rows {
		for _, r := range runs {
			if strings.HasPrefix(r.overlay, overlayObserve) {
				continue
			}
			for _, object := range r.rows {
				if object.kind == rows[i].Kind && object.state.ready {
					rows[i].Ready++
				}
			}
		}
		if complete && rows[i].Class == "live-overlay" && rows[i].Ready == 0 {
			t.Errorf("%s: full live run produced no current Ready object", rows[i].Kind)
		}
	}
}

func completeOverlays(requested []string) bool {
	for overlay := range strings.SplitSeq(defaultE2EOverlays, ",") {
		if !slices.Contains(requested, overlay) {
			return false
		}
	}
	return true
}

func reportLiveInventory(t testing.TB, out string, rows []coverageRow, runs []*run, requested []string) {
	t.Helper()
	complete := os.Getenv("LIVEKIT_COMPONENTS") == "" && completeOverlays(requested)
	verifyLiveInventory(t, rows, runs, complete)
	writeInventory(t, out+".coverage.json", rows)
	if !complete {
		t.Log("partial live run: coverage artifact records observed kinds, without claiming all-kind runtime coverage")
	}
}
