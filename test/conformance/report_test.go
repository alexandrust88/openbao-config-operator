//go:build conformance

package conformance

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// confWriteReport writes the coverage matrix: kind | example applied | no-write pass | default test pass |
// failing fields | notes.
func confWriteReport(t *testing.T, path string, e *confEnv, results []*confResult) {
	t.Helper()
	var noWritePass, noWriteFail, defPass, defFail, skipped int
	for _, r := range results {
		switch {
		case r.skip != "":
			skipped++
			continue
		case strings.HasPrefix(r.noWrite, confPass):
			noWritePass++
		case strings.HasPrefix(r.noWrite, confFail):
			noWriteFail++
		}
		switch {
		case r.defaults == confPass:
			defPass++
		case strings.HasPrefix(r.defaults, confFail):
			defFail++
		}
	}
	var b strings.Builder
	fmt.Fprintf(&b, "# conformance matrix\n\n")
	fmt.Fprintf(&b, "generated %s by `go test -tags conformance ./test/conformance/` (drift interval %s, "+
		"`bao server -dev`, envtest, manager %s).\n\n", time.Now().Format(time.RFC3339), e.drift, e.managerSource)
	fmt.Fprintf(&b, "kinds: %d; skipped %d; no-write pass %d / fail %d; default test pass %d / fail %d.\n\n",
		len(results), skipped, noWritePass, noWriteFail, defPass, defFail)
	b.WriteString("no-write: examples applied together (root namespace, deletionPolicy Delete), Ready, then " +
		"3.5 drift intervals: openbao_writes_total{kind} must not move. default: per kind its own manager, " +
		"namespace and OpenBao namespace; minimal spec (required fields, creationPolicy CreateOrAdopt) then each " +
		"optional top-level field set (1 write), removed (1 write) and one more pass (0 writes). n/a = could " +
		"not be measured and declared in exceptions.yaml (see notes); an undeclared n/a is a FAIL; event keys in " +
		"[] name differing fields.\n\n")
	b.WriteString("| kind | example applied | no-write pass | default test pass | failing fields | notes |\n")
	b.WriteString("|---|---|---|---|---|---|\n")
	for _, r := range results {
		example, noWrite, defaults := r.example, r.noWrite, r.defaults
		notes := r.notes
		if r.skip != "" {
			example, noWrite, defaults = "-", "skipped", "skipped"
			notes = []string{r.skip}
		}
		fmt.Fprintf(&b, "| %s | %s | %s | %s | %s | %s |\n", r.kind.Kind, confCell(example), confCell(noWrite),
			confCell(defaults), confCell(strings.Join(r.failing, "; ")), confCell(strings.Join(notes, "; ")))
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(b.String()), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Logf("matrix written to %s", path)
}

func confCell(s string) string {
	if s == "" {
		return "-"
	}
	return strings.NewReplacer("|", "/", "\n", " ").Replace(s)
}

func confSprintf(format string, args ...any) string {
	return strings.ReplaceAll(fmt.Sprintf(format, args...), "\n", " ")
}
