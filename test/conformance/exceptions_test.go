//go:build conformance

package conformance

import (
	"os"
	"path/filepath"
	"testing"

	"sigs.k8s.io/yaml"
)

// Kind-level tests an exceptions.yaml entry may declare as not measurable (n/a) for a kind.
const (
	confTestExamples       = "examples"
	confTestNoWrite        = "nowrite"
	confTestDefaults       = "defaults"
	confTestOptionalFields = "optional-fields"
)

// confException is one entry of exceptions.yaml: a field whose removal cannot restore the OpenBao default, or a
// kind-level test that cannot be measured for the kind.
type confException struct {
	Kind   string `json:"kind"`
	Field  string `json:"field"`
	Test   string `json:"test"`
	Reason string `json:"reason"`
}

// confLoadExceptions returns the reasons of exceptions.yaml keyed by "<kind>/<field>" or "<kind>/<test>".
func confLoadExceptions(t *testing.T, root string) map[string]string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(root, "test", "conformance", "exceptions.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	var entries []confException
	if err := yaml.Unmarshal(raw, &entries); err != nil {
		t.Fatalf("exceptions.yaml: %v", err)
	}
	out := make(map[string]string, len(entries))
	for _, e := range entries {
		if e.Kind == "" || e.Reason == "" || (e.Field == "") == (e.Test == "") {
			t.Fatalf("exceptions.yaml: entry %+v needs kind, reason and either field or test", e)
		}
		switch e.Test {
		case "", confTestExamples, confTestNoWrite, confTestDefaults, confTestOptionalFields:
		default:
			t.Fatalf("exceptions.yaml: entry %+v: unknown test %q", e, e.Test)
		}
		key := e.Kind + "/" + e.Field + e.Test
		if _, exists := out[key]; exists {
			t.Fatalf("exceptions.yaml: duplicate %s", key)
		}
		out[key] = e.Reason
	}
	return out
}
