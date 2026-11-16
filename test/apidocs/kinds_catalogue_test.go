package apidocs

import (
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

const (
	repoRoot       = "../.."
	kindsDoc       = repoRoot + "/docs/kinds.md"
	pluginCRDsList = repoRoot + "/hack/chart/plugin-crds.txt"
)

// catalogueRow is one kind row of docs/kinds.md.
type catalogueRow struct {
	kind, availability, evidence string
}

func loadCatalogue(t *testing.T) (rows []catalogueRow, doc string) {
	t.Helper()
	raw, err := os.ReadFile(kindsDoc)
	if err != nil {
		t.Fatal(err)
	}
	doc = string(raw)
	for line := range strings.SplitSeq(doc, "\n") {
		if !strings.HasPrefix(line, "| `") && !strings.HasPrefix(line, "| [`") {
			continue
		}
		cols := strings.Split(strings.ReplaceAll(line, `\|`, "/"), "|")
		if len(cols) != 9 {
			t.Fatalf("kinds.md row has %d columns, want 7: %s", len(cols)-2, line)
		}
		rows = append(rows, catalogueRow{
			kind:         regexp.MustCompile("`([^`]+)`").FindStringSubmatch(cols[1])[1],
			availability: strings.TrimSpace(cols[3]),
			evidence:     strings.TrimSpace(cols[6]),
		})
	}
	return rows, doc
}

func TestKindsCatalogueMatchesCRDs(t *testing.T) {
	rows, doc := loadCatalogue(t)
	raw, err := os.ReadFile(pluginCRDsList)
	if err != nil {
		t.Fatal(err)
	}
	pluginFiles := map[string]bool{}
	for f := range strings.FieldsSeq(string(raw)) {
		pluginFiles[f] = true
	}

	rowsByKind := map[string][]catalogueRow{}
	for _, r := range rows {
		rowsByKind[r.kind] = append(rowsByKind[r.kind], r)
	}
	crds := loadCRDs(t)
	crdKinds := map[string]bool{}
	builtin, core := 0, 0
	for _, crd := range crds {
		kind := crd.Spec.Names.Kind
		crdKinds[kind] = true
		plugin := pluginFiles[crd.Spec.Group+"_"+crd.Spec.Names.Plural+".yaml"]
		if !plugin {
			builtin++
			if strings.HasPrefix(crd.Spec.Group, "core.") {
				core++
			}
		}
		matches := rowsByKind[kind]
		if len(matches) != 1 {
			t.Errorf("CRD kind %s has %d rows in kinds.md, want 1", kind, len(matches))
			continue
		}
		if got := matches[0].availability; plugin != strings.HasPrefix(got, "external-plugin") {
			t.Errorf("%s: availability %q, but plugin-crds.txt lists it: %v", kind, got, plugin)
		}
	}
	for kind := range rowsByKind {
		if !crdKinds[kind] {
			t.Errorf("kinds.md row %s has no CRD in %s", kind, crdDir)
		}
	}

	counts := regexp.MustCompile(`(?m)^Counts:[^\n]*(?:\n[^\n]+)*`).FindString(doc)
	if counts == "" {
		t.Fatal("kinds.md has no Counts: paragraph")
	}
	for label, want := range map[string]int{
		"rows":                len(crds),
		"built-in kinds":      builtin,
		"plugin-gated kinds":  len(crds) - builtin,
		"typed OpenBao kinds": builtin - core,
	} {
		m := regexp.MustCompile(`(\d+)\s+` + strings.ReplaceAll(label, " ", `\s+`) + `\b`).FindStringSubmatch(counts)
		if m == nil {
			t.Errorf("kinds.md Counts: paragraph does not state the number of %s", label)
			continue
		}
		if got, _ := strconv.Atoi(m[1]); got != want {
			t.Errorf("kinds.md states %d %s, the CRDs give %d", got, label, want)
		}
	}
}

// TestKindsCatalogueEvidenceExists resolves every path in the evidence column: a bare file name is under
// test/integration, anything with a slash is relative to the repository root.
func TestKindsCatalogueEvidenceExists(t *testing.T) {
	rows, _ := loadCatalogue(t)
	quoted := regexp.MustCompile("`([^`]+)`")
	for _, r := range rows {
		for _, m := range quoted.FindAllStringSubmatch(r.evidence, -1) {
			path := filepath.Join(repoRoot, "test", "integration", m[1])
			if strings.Contains(m[1], "/") {
				path = filepath.Join(repoRoot, m[1])
			}
			if _, err := os.Stat(path); err != nil {
				t.Errorf("%s: evidence %s: %v", r.kind, m[1], err)
			}
		}
	}
}
