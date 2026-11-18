//go:build conformance

package conformance

import (
	"cmp"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
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
	fmt.Fprintf(&b, "Timing: setup %s; no-write %s (waiting for examples %s, cleanup %s); defaults %s with %d workers. "+
		"The 4-second default drift interval and the measured reconcile counts are unchanged by concurrency. "+
		"Set `CONF_PARALLELISM=1..16` to tune worker count.\n\n",
		e.setupDuration.Round(time.Millisecond), e.noWriteDuration.Round(time.Millisecond),
		e.readyDuration.Round(time.Millisecond), e.cleanupDuration.Round(time.Millisecond),
		e.defaultsDuration.Round(time.Millisecond), e.parallelism)
	b.WriteString("no-write: examples applied together (root namespace, deletionPolicy Delete), Ready, then " +
		"3.5 drift intervals: obco_writes_total{kind} must not move. default: per kind its own manager, " +
		"namespace and OpenBao namespace; minimal spec (required fields, creationPolicy CreateOrAdopt) then each " +
		"optional top-level field set (1 write), removed (1 write) and one more pass (0 writes). " +
		"AccessLevel instead checks current-generation Ready after each template edit and zero writes over two drift intervals; it has no periodic requeue. n/a = could " +
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
	b.WriteString("\n## Default-test timings\n\n| kind | total | startup | minimal ready | field checks | fields tested |\n|---|---:|---:|---:|---:|---:|\n")
	slow := append([]*confResult(nil), results...)
	slices.SortStableFunc(slow, func(a, b *confResult) int { return cmp.Compare(b.duration, a.duration) })
	for _, r := range slow {
		if r.duration == 0 {
			continue
		}
		fmt.Fprintf(&b, "| %s | %s | %s | %s | %s | %d |\n", r.kind.Kind, r.duration.Round(time.Millisecond),
			r.startupDuration.Round(time.Millisecond), r.minimalDuration.Round(time.Millisecond),
			r.fieldsDuration.Round(time.Millisecond), r.fieldsTested)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(b.String()), 0o600); err != nil {
		t.Fatal(err)
	}
	confWriteTimings(t, strings.TrimSuffix(path, filepath.Ext(path))+"-timings.json", e, results)
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

func confWriteTimings(t *testing.T, path string, e *confEnv, results []*confResult) {
	t.Helper()
	type kindTiming struct {
		Kind         string  `json:"kind"`
		Total        float64 `json:"totalSeconds"`
		Startup      float64 `json:"startupSeconds"`
		Minimal      float64 `json:"minimalSeconds"`
		Fields       float64 `json:"fieldSeconds"`
		FieldsTested int     `json:"fieldsTested"`
	}
	out := struct {
		Workers  int          `json:"workers"`
		Setup    float64      `json:"setupSeconds"`
		NoWrite  float64      `json:"noWriteSeconds"`
		Ready    float64      `json:"readySeconds"`
		Cleanup  float64      `json:"cleanupSeconds"`
		Defaults float64      `json:"defaultsSeconds"`
		Kinds    []kindTiming `json:"kinds"`
	}{Workers: e.parallelism, Setup: e.setupDuration.Seconds(), NoWrite: e.noWriteDuration.Seconds(),
		Ready: e.readyDuration.Seconds(), Cleanup: e.cleanupDuration.Seconds(), Defaults: e.defaultsDuration.Seconds()}

	for _, r := range results {
		if r.duration > 0 {
			out.Kinds = append(out.Kinds, kindTiming{Kind: r.kind.Kind, Total: r.duration.Seconds(), Startup: r.startupDuration.Seconds(),
				Minimal: r.minimalDuration.Seconds(), Fields: r.fieldsDuration.Seconds(), FieldsTested: r.fieldsTested})
		}
	}
	data, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, append(data, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
}
