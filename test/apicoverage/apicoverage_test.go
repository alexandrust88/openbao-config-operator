// Package apicoverage checks that every writable path of each pinned OpenBao OpenAPI snapshot is classified and
// that docs/api-coverage.md matches the classification.
package apicoverage

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/alexandrust88/openbao-config-operator/hack/apicoverage/coverage"
)

const (
	dataDir    = "../../api-coverage"
	crdDir     = "../../config/crd/bases"
	reportFile = "../../docs/api-coverage.md"
)

func TestAPICoverage(t *testing.T) {
	d, err := coverage.Load(dataDir, crdDir)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range d.Problems() {
		t.Error(p)
	}
	for _, s := range d.Snapshots {
		c := d.Count(s)
		t.Logf("%s: %d writable, %d typed, %d endpoint-only, %d unreviewed, %d action",
			s.Version, c.Writable, c.Typed, c.EndpointOnly, c.Unreviewed, c.Action)
	}
	want, err := os.ReadFile(reportFile)
	if err != nil {
		t.Fatal(err)
	}
	if d.Report() != string(want) {
		t.Error("docs/api-coverage.md is stale: run go run ./hack/apicoverage")
	}
}

const fixtureSnapshot = `{
  "info": {"version": "2.7.2"},
  "paths": {
    "/sys/policies/acl/{name}": {"get": {}, "post": {}, "delete": {}},
    "/sys/raw/{path}": {"put": {}},
    "/sys/tools/random": {"post": {}},
    "/sys/external-keys/configs/{config}": {"patch": {}},
    "/sys/health": {"get": {}}
  }
}`

const fixtureCRD = `apiVersion: apiextensions.k8s.io/v1
kind: CustomResourceDefinition
spec:
  names:
    kind: Policy
`

const validClassification = `"/sys/policies/acl/{name}": {typed: Policy}
"/sys/raw/{path}": {endpoint-only: break-glass only}
"/sys/tools/random": {action: random bytes}
"/sys/external-keys/configs/{config}": {endpoint-only: unreviewed}
`

func writeFixture(t *testing.T, classification string) (string, string) {
	t.Helper()
	data, crds := t.TempDir(), t.TempDir()
	files := map[string]string{
		filepath.Join(data, "openapi-2.7.json"):                   fixtureSnapshot,
		filepath.Join(data, coverage.ClassificationFile):          classification,
		filepath.Join(crds, "sys.openbaoconfig.io_policies.yaml"): fixtureCRD,
	}
	for name, content := range files {
		if err := os.WriteFile(name, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return data, crds
}

func TestValidClassificationCounts(t *testing.T) {
	d, err := coverage.Load(writeFixture(t, validClassification))
	if err != nil {
		t.Fatal(err)
	}
	if p := d.Problems(); len(p) > 0 {
		t.Fatalf("unexpected problems: %v", p)
	}
	got := d.Count(d.Snapshots[0])
	want := coverage.Counts{Writable: 4, Typed: 1, EndpointOnly: 1, Unreviewed: 1, Action: 1}
	if got != want {
		t.Fatalf("counts = %+v, want %+v", got, want)
	}
	row := "| 2.7.2 | 4 | 1 (25.0%) | 1 (25.0%) | 1 (25.0%) | 1 (25.0%) | 33.3% |"
	if !strings.Contains(d.Report(), row) {
		t.Fatalf("report has no row %q:\n%s", row, d.Report())
	}
}

func TestProblems(t *testing.T) {
	cases := []struct {
		name           string
		classification string
		want           string
	}{
		{
			name:           "unclassified",
			classification: strings.Replace(validClassification, `"/sys/tools/random": {action: random bytes}`+"\n", "", 1),
			want:           "2.7: writable path /sys/tools/random is not classified",
		},
		{
			name: "two classes",
			classification: strings.Replace(validClassification, "{action: random bytes}",
				"{action: random bytes, endpoint-only: random}", 1),
			want: "/sys/tools/random: has 2 classes, want exactly one",
		},
		{
			name:           "unknown kind",
			classification: strings.Replace(validClassification, "{typed: Policy}", "{typed: AclPolicy}", 1),
			want:           "/sys/policies/acl/{name}: typed kind AclPolicy has no CRD",
		},
		{
			name:           "stale",
			classification: validClassification + `"/sys/gone": {action: removed}` + "\n",
			want:           "/sys/gone: classified but writable in no snapshot",
		},
		{
			name:           "unknown class",
			classification: strings.Replace(validClassification, "{action: random bytes}", "{ignored: random bytes}", 1),
			want:           `/sys/tools/random: unknown class "ignored"`,
		},
		{
			name:           "empty reason",
			classification: strings.Replace(validClassification, "{action: random bytes}", `{action: ""}`, 1),
			want:           "/sys/tools/random: action has no value",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d, err := coverage.Load(writeFixture(t, tc.classification))
			if err != nil {
				t.Fatal(err)
			}
			problems := d.Problems()
			if len(problems) != 1 || problems[0] != tc.want {
				t.Fatalf("problems = %q, want [%q]", problems, tc.want)
			}
		})
	}
}

func TestDuplicatePathIsRejected(t *testing.T) {
	_, err := coverage.Load(writeFixture(t, validClassification+`"/sys/tools/random": {action: again}`+"\n"))
	if err == nil || !strings.Contains(err.Error(), "already set") {
		t.Fatalf("err = %v, want a duplicate key error", err)
	}
}

func TestSnapshotVersionMustMatchName(t *testing.T) {
	data, crds := writeFixture(t, validClassification)
	if err := os.Rename(filepath.Join(data, "openapi-2.7.json"), filepath.Join(data, "openapi-2.6.json")); err != nil {
		t.Fatal(err)
	}
	if _, err := coverage.Load(data, crds); err == nil || !strings.Contains(err.Error(), "is not a 2.6 release") {
		t.Fatalf("err = %v, want a version mismatch error", err)
	}
}
