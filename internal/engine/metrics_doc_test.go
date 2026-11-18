package engine

import (
	"os"
	"regexp"
	"slices"
	"strings"
	"testing"

	"sigs.k8s.io/controller-runtime/pkg/metrics"

	commonv1alpha1 "github.com/alexandrust88/openbao-config-operator/api/common/v1alpha1"
)

// openbaoClientMetrics are documented in the architecture table and registered by internal/openbao, whose own test
// checks their labels.
var openbaoClientMetrics = []string{
	"obco_logins_total", "obco_request_duration_seconds", "obco_requests_total", "obco_token_renewals_total",
}

// connectionMetrics are documented in the architecture table and registered by internal/controller/core.
var connectionMetrics = []string{"obco_connection_ready"}

// documentedMetrics parses the metric table of the Observability section in docs/architecture.md into metric name ->
// label names.
func documentedMetrics(t *testing.T) map[string][]string {
	t.Helper()
	raw, err := os.ReadFile("../../docs/architecture.md")
	if err != nil {
		t.Fatal(err)
	}
	_, section, ok := strings.Cut(string(raw), "\n## Observability\n")
	if !ok {
		t.Fatal("docs/architecture.md has no Observability section")
	}
	section, _, _ = strings.Cut(section, "\n## ")
	name := regexp.MustCompile("`(obco_[a-z_]+)`")
	out := map[string][]string{}
	for line := range strings.SplitSeq(section, "\n") {
		cells := strings.Split(line, "|")
		if len(cells) < 4 || !name.MatchString(cells[1]) {
			continue
		}
		labels := strings.Split(strings.TrimSpace(cells[2]), ", ")
		slices.Sort(labels)
		for _, m := range name.FindAllStringSubmatch(cells[1], -1) {
			out[m[1]] = labels
		}
	}
	return out
}

func TestDocumentedEngineMetricsAreRegistered(t *testing.T) {
	_ = newHarness(t, testAdapter{})
	writesTotal.WithLabelValues(t.Name())
	driftRepairs.WithLabelValues(t.Name())
	conflictsTotal.WithLabelValues(t.Name())
	driftLag.WithLabelValues(t.Name())
	budgetExhausted.WithLabelValues(t.Name())
	resourceReady.WithLabelValues(t.Name(), testNamespace, t.Name(), commonv1alpha1.ReasonAvailable)
	defer resourceReady.DeleteLabelValues(t.Name(), testNamespace, t.Name(), commonv1alpha1.ReasonAvailable)

	families, err := metrics.Registry.Gather()
	if err != nil {
		t.Fatal(err)
	}
	gathered := map[string][]string{}
	for _, f := range families {
		if len(f.GetMetric()) == 0 {
			continue
		}
		var labels []string
		for _, l := range f.GetMetric()[0].GetLabel() {
			labels = append(labels, l.GetName())
		}
		slices.Sort(labels)
		gathered[f.GetName()] = labels
	}

	documented := documentedMetrics(t)
	if len(documented) == 0 {
		t.Fatal("no metric rows parsed from the architecture Observability table")
	}
	for metric, labels := range documented {
		if slices.Contains(openbaoClientMetrics, metric) || slices.Contains(connectionMetrics, metric) {
			continue
		}
		got, ok := gathered[metric]
		if !ok {
			t.Errorf("%s is documented but not in the controller-runtime registry", metric)
			continue
		}
		if !slices.Equal(got, labels) {
			t.Errorf("%s labels = %v, documented %v", metric, got, labels)
		}
	}
	for _, metric := range slices.Concat(openbaoClientMetrics, connectionMetrics) {
		if _, ok := documented[metric]; !ok {
			t.Errorf("%s is registered outside the engine but missing from the architecture table", metric)
		}
	}
}
