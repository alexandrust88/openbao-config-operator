// Package coverage checks that every writable path in the pinned OpenBao OpenAPI snapshots is classified as typed,
// endpoint-only or action, and renders the coverage report.
package coverage

import (
	"cmp"
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"sigs.k8s.io/yaml"
)

// Classes a writable path can have.
const (
	ClassTyped        = "typed"
	ClassEndpointOnly = "endpoint-only"
	ClassAction       = "action"
)

// Unreviewed is the endpoint-only reason of a path nobody has classified yet.
const Unreviewed = "unreviewed"

// ClassificationFile is the classification file name inside the data directory.
const ClassificationFile = "classification.yaml"

var (
	snapshotName    = regexp.MustCompile(`^openapi-(\d+)\.(\d+)\.json$`)
	writableMethods = []string{"post", "put", "patch", "delete"}
)

// Snapshot is the writable surface of one OpenBao minor.
type Snapshot struct {
	Minor    string
	Version  string
	Writable []string
	major    int
	minor    int
}

// Entry is the classification of one path.
type Entry struct {
	Class string
	Value string
}

// Data is everything the check and the report need.
type Data struct {
	Snapshots      []Snapshot
	Classification map[string]map[string]string
	Kinds          map[string]bool
}

// Load reads every openapi-<minor>.json snapshot and the classification from dataDir, and the CRD kinds from crdDir.
func Load(dataDir, crdDir string) (*Data, error) {
	files, err := filepath.Glob(filepath.Join(dataDir, "openapi-*.json"))
	if err != nil {
		return nil, err
	}
	d := &Data{}
	for _, f := range files {
		s, err := LoadSnapshot(f)
		if err != nil {
			return nil, err
		}
		d.Snapshots = append(d.Snapshots, s)
	}
	if len(d.Snapshots) == 0 {
		return nil, fmt.Errorf("no openapi-<minor>.json snapshot in %s", dataDir)
	}
	slices.SortFunc(d.Snapshots, func(a, b Snapshot) int {
		return cmp.Or(cmp.Compare(a.major, b.major), cmp.Compare(a.minor, b.minor))
	})
	if d.Classification, err = LoadClassification(filepath.Join(dataDir, ClassificationFile)); err != nil {
		return nil, err
	}
	if d.Kinds, err = LoadKinds(crdDir); err != nil {
		return nil, err
	}
	return d, nil
}

// LoadSnapshot reads the writable path templates of an openapi-<major>.<minor>.json file.
func LoadSnapshot(path string) (Snapshot, error) {
	m := snapshotName.FindStringSubmatch(filepath.Base(path))
	if m == nil {
		return Snapshot{}, fmt.Errorf("snapshot %s: name is not openapi-<major>.<minor>.json", path)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return Snapshot{}, err
	}
	var doc struct {
		Info  struct{ Version string }
		Paths map[string]map[string]json.RawMessage
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		return Snapshot{}, fmt.Errorf("parse %s: %w", path, err)
	}
	s := Snapshot{Minor: m[1] + "." + m[2], Version: doc.Info.Version}
	s.major, _ = strconv.Atoi(m[1])
	s.minor, _ = strconv.Atoi(m[2])
	if !strings.HasPrefix(s.Version, s.Minor+".") {
		return Snapshot{}, fmt.Errorf("snapshot %s: info.version %q is not a %s release", path, s.Version, s.Minor)
	}
	for p, item := range doc.Paths {
		if slices.ContainsFunc(writableMethods, func(m string) bool { _, ok := item[m]; return ok }) {
			s.Writable = append(s.Writable, p)
		}
	}
	slices.Sort(s.Writable)
	return s, nil
}

// LoadClassification reads the classification file. A path listed twice is an error.
func LoadClassification(path string) (map[string]map[string]string, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var c map[string]map[string]string
	if err := yaml.UnmarshalStrict(raw, &c); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	return c, nil
}

// LoadKinds returns the kinds of the CustomResourceDefinitions in dir.
func LoadKinds(dir string) (map[string]bool, error) {
	files, err := filepath.Glob(filepath.Join(dir, "*.yaml"))
	if err != nil {
		return nil, err
	}
	kinds := map[string]bool{}
	for _, f := range files {
		raw, err := os.ReadFile(f)
		if err != nil {
			return nil, err
		}
		var crd struct {
			Kind string `json:"kind"`
			Spec struct {
				Names struct {
					Kind string `json:"kind"`
				} `json:"names"`
			} `json:"spec"`
		}
		if err := yaml.Unmarshal(raw, &crd); err != nil {
			return nil, fmt.Errorf("parse %s: %w", f, err)
		}
		if crd.Kind == "CustomResourceDefinition" && crd.Spec.Names.Kind != "" {
			kinds[crd.Spec.Names.Kind] = true
		}
	}
	if len(kinds) == 0 {
		return nil, fmt.Errorf("no CustomResourceDefinition in %s", dir)
	}
	return kinds, nil
}

// Problems lists every violation: an unclassified writable path, a path with zero or several classes, an unknown
// class, an empty reason, a typed kind without a CRD, and a classified path that no snapshot has.
func (d *Data) Problems() []string {
	var problems []string
	known := map[string]bool{}
	for _, s := range d.Snapshots {
		for _, p := range s.Writable {
			known[p] = true
			if _, ok := d.Classification[p]; !ok {
				problems = append(problems, fmt.Sprintf("%s: writable path %s is not classified", s.Minor, p))
			}
		}
	}
	for _, p := range slices.Sorted(maps.Keys(d.Classification)) {
		classes := d.Classification[p]
		if !known[p] {
			problems = append(problems, fmt.Sprintf("%s: classified but writable in no snapshot", p))
		}
		if len(classes) != 1 {
			problems = append(problems, fmt.Sprintf("%s: has %d classes, want exactly one", p, len(classes)))
			continue
		}
		e, _ := entry(classes)
		switch {
		case e.Class != ClassTyped && e.Class != ClassEndpointOnly && e.Class != ClassAction:
			problems = append(problems, fmt.Sprintf("%s: unknown class %q", p, e.Class))
		case strings.TrimSpace(e.Value) == "":
			problems = append(problems, fmt.Sprintf("%s: %s has no value", p, e.Class))
		case e.Class == ClassTyped && !d.Kinds[e.Value]:
			problems = append(problems, fmt.Sprintf("%s: typed kind %s has no CRD", p, e.Value))
		}
	}
	return problems
}

// Counts is the classification summary of one snapshot.
type Counts struct {
	Writable, Typed, EndpointOnly, Unreviewed, Action int
}

// Count summarises the classification of s. EndpointOnly excludes the unreviewed paths.
func (d *Data) Count(s Snapshot) Counts {
	c := Counts{Writable: len(s.Writable)}
	for _, p := range s.Writable {
		e, _ := entry(d.Classification[p])
		switch {
		case e.Class == ClassTyped:
			c.Typed++
		case e.Class == ClassEndpointOnly && e.Value == Unreviewed:
			c.Unreviewed++
		case e.Class == ClassEndpointOnly:
			c.EndpointOnly++
		case e.Class == ClassAction:
			c.Action++
		}
	}
	return c
}

// Report renders docs/api-coverage.md.
func (d *Data) Report() string {
	var b strings.Builder
	b.WriteString(reportHeader)
	b.WriteString("| OpenBao | writable paths | typed | Endpoint only | unreviewed | action | typed share of configuration |\n")
	b.WriteString("|---------|----------------|-------|---------------|------------|--------|------------------------------|\n")
	for _, s := range d.Snapshots {
		c := d.Count(s)
		fmt.Fprintf(&b, "| %s | %d | %s | %s | %s | %s | %s |\n", s.Version, c.Writable,
			share(c.Typed, c.Writable), share(c.EndpointOnly, c.Writable), share(c.Unreviewed, c.Writable),
			share(c.Action, c.Writable), percent(c.Typed, c.Typed+c.EndpointOnly+c.Unreviewed))
	}
	b.WriteString("\nTyped share of configuration is typed / (typed + Endpoint only + unreviewed): actions are not\n" +
		"configuration and are left out. This is a classification of path templates, not a percentage of runtime tests passed.\n\n" +
		"For OpenBao 2.7.2, 92.2% means 106 of 115 configuration paths. Those same 106 typed paths are\n" +
		"30.1% of all 352 writable paths; the remaining 237 action paths are outside configuration scope.\n\n" +
		"### Why the typed count changes between snapshots\n\n" +
		"The 2.7.2 snapshot omits 14 typed paths present in 2.6.5 and adds no typed paths: three Kerberos auth,\n" +
		"three LDAP auth, two RADIUS auth, and six LDAP/OpenLDAP secrets-engine paths. These integrations\n" +
		"require external plugins in the later server snapshot. Their CRDs remain in the plugin-gated catalogue;\n" +
		"their absence from the builtin snapshot does not mean the operator removed those kinds. Snapshot\n" +
		"coverage and live-tested compatibility are separate; see the [compatibility reference](reference/compatibility.md).\n\n## Endpoint-only paths\n\n" +
		"Configuration paths without a typed kind. Manage them with the `BaoEndpoint` kind; each is a candidate\n" +
		"for a typed kind.\n\n| path | OpenBao | reason |\n|------|---------|--------|\n")
	minors := map[string][]string{}
	for _, s := range d.Snapshots {
		for _, p := range s.Writable {
			minors[p] = append(minors[p], s.Minor)
		}
	}
	for _, p := range slices.Sorted(maps.Keys(d.Classification)) {
		e, ok := entry(d.Classification[p])
		if !ok || e.Class != ClassEndpointOnly {
			continue
		}
		fmt.Fprintf(&b, "| `%s` | %s | %s |\n", p, strings.Join(minors[p], ", "), e.Value)
	}
	return b.String()
}

const reportHeader = `---
description: "See how OpenBao configuration API coverage is measured, including typed paths, endpoint-only cases and documented exclusions."
---

# API coverage

<!-- generated by go run ./hack/apicoverage; do not edit -->

Every writable (POST, PUT, PATCH, DELETE) path template in the pinned OpenAPI snapshot of each supported
OpenBao minor is classified exactly once in ` + "`api-coverage/classification.yaml`" + `:

- **typed**: a kind of this operator configures the object behind the path.
- **Endpoint only**: configuration without a typed kind; use the ` + "`BaoEndpoint`" + ` kind.
  **unreviewed** paths are Endpoint only paths that nobody has classified yet.
- **action**: a one-off operation, not configuration (login, issue, sign, rotate, revoke, tidy, ...).

` + "`go test ./test/apicoverage/...`" + ` fails when a writable path is unclassified or classified twice, when a
typed entry names a kind without a CRD in ` + "`config/crd/bases`" + `, or when this file is stale.

The snapshots ` + "`api-coverage/openapi-<minor>.json`" + ` come from ` + "`bao server -dev`" + ` with every builtin auth
method and secrets engine mounted at its default path (kv v2 at ` + "`secret/`" + `, kv v1 at ` + "`kv/`" + `), fetched with
` + "`sys/internal/specs/openapi?generic_mount_paths=true`" + ` so mount paths appear as ` + "`{<type>_mount_path}`" + `. Add or
refresh a minor with ` + "`hack/apicoverage/snapshot.sh <bao-binary> api-coverage/openapi-<minor>.json`" + `, classify
the new paths, then run ` + "`go run ./hack/apicoverage`" + `. Kinds for engines that are external plugins in a
release (for example AWS, GCP, or LDAP on 2.7) are outside that release's snapshot.

## Coverage per release

`

func entry(classes map[string]string) (Entry, bool) {
	if len(classes) != 1 {
		return Entry{}, false
	}
	for c, v := range classes {
		return Entry{Class: c, Value: v}, true
	}
	return Entry{}, false
}

func share(n, total int) string {
	return fmt.Sprintf("%d (%s)", n, percent(n, total))
}

func percent(n, total int) string {
	if total == 0 {
		return "-"
	}
	return fmt.Sprintf("%.1f%%", 100*float64(n)/float64(total))
}
