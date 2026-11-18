package openbao

import (
	"os"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"sigs.k8s.io/controller-runtime/pkg/metrics"
)

// TestDocumentedClientMetricsAreScraped drives a login, a renewal and a request, then checks that the client metrics
// of the architecture Observability table are in the controller-runtime registry with the documented labels.
func TestDocumentedClientMetricsAreScraped(t *testing.T) {
	f := newFakeServer(t)
	f.loginTTL, f.renewTTL, f.renewable = 3600, 3600, true
	c, clk := newTestClient(t, appRoleConfig(f.srv.URL))
	clk.Advance(21 * time.Minute)
	if _, err := c.Read(t.Context(), "kv/x"); err != nil {
		t.Fatal(err)
	}

	raw, err := os.ReadFile("../../docs/architecture.md")
	if err != nil {
		t.Fatal(err)
	}
	_, section, _ := strings.Cut(string(raw), "\n## Observability\n")
	section, _, _ = strings.Cut(section, "\n## ")
	row := regexp.MustCompile("^\\| `(obco_[a-z_]+)` \\| ([a-z, ]+) \\|")
	documented := map[string][]string{}
	for line := range strings.SplitSeq(section, "\n") {
		if m := row.FindStringSubmatch(line); m != nil {
			labels := strings.Split(m[2], ", ")
			slices.Sort(labels)
			documented[m[1]] = labels
		}
	}

	families, err := metrics.Registry.Gather()
	if err != nil {
		t.Fatal(err)
	}
	gathered := map[string][]string{}
	for _, fam := range families {
		for _, m := range fam.GetMetric() {
			labels := make([]string, 0, len(m.GetLabel()))
			for _, l := range m.GetLabel() {
				labels = append(labels, l.GetName())
			}
			slices.Sort(labels)
			gathered[fam.GetName()] = labels
		}
	}
	for _, metric := range []string{"obco_requests_total", "obco_request_duration_seconds",
		"obco_logins_total", "obco_token_renewals_total"} {
		want, ok := documented[metric]
		if !ok {
			t.Errorf("%s is not in the architecture Observability table", metric)
			continue
		}
		if got, ok := gathered[metric]; !ok || !slices.Equal(got, want) {
			t.Errorf("%s scraped labels = %v (present %t), documented %v", metric, got, ok, want)
		}
	}
}
