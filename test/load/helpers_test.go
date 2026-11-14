//go:build load

package load

import (
	"math"
	"testing"
	"time"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

func TestParseSeries(t *testing.T) {
	name, labels := parseSeries(`openbao_requests_total{code="200",connection="c",method="GET"}`)
	if name != "openbao_requests_total" || labels["code"] != "200" || labels["method"] != "GET" {
		t.Fatalf("got %q %v", name, labels)
	}
	if name, labels := parseSeries("go_goroutines"); name != "go_goroutines" || labels != nil {
		t.Fatalf("got %q %v", name, labels)
	}
}

func TestSnapshotAddSkipsConnectionControllers(t *testing.T) {
	s := snapshot{}
	s.add("controller_runtime_reconcile_total", map[string]string{"controller": "policy.sys", "result": "success"}, 3)
	s.add("controller_runtime_reconcile_total", map[string]string{"controller": "openbaoconnection", "result": "success"}, 5)
	if s["controller_runtime_reconcile_total"] != 3 || s["controller_runtime_reconcile_total:result=success"] != 3 {
		t.Fatalf("got %v", s)
	}
}

func TestQuantile(t *testing.T) {
	s := snapshot{
		"h:le=0.1":  50,
		"h:le=0.2":  90,
		"h:le=0.4":  100,
		"h:le=+Inf": 100,
	}
	for q, want := range map[float64]float64{0.5: 0.1, 0.7: 0.15, 0.95: 0.3} {
		if got := s.quantile("h", q); math.Abs(got-want) > 1e-9 {
			t.Errorf("quantile(%v) = %v, want %v", q, got, want)
		}
	}
	if got := (snapshot{}).quantile("h", 0.5); got != 0 {
		t.Errorf("empty histogram: got %v", got)
	}
}

func TestParseCPUTime(t *testing.T) {
	for in, want := range map[string]float64{"0:01.50": 1.5, "2:03.00": 123, "1:00:00.00": 3600, "1-00:00:01.00": 86401} {
		if got := parseCPUTime(in); math.Abs(got-want) > 1e-9 {
			t.Errorf("parseCPUTime(%q) = %v, want %v", in, got, want)
		}
	}
}

func TestSummarize(t *testing.T) {
	d := summarize([]float64{5, 1, 4, 2, 3})
	if d.Count != 5 || d.Min != 1 || d.Max != 5 || d.P50 != 3 || d.P95 != 5 || d.Mean != 3 {
		t.Fatalf("got %+v", d)
	}
	if summarize(nil) != (dist{}) {
		t.Fatal("empty input must give a zero dist")
	}
}

func TestRates(t *testing.T) {
	start := time.Unix(0, 0)
	samples := []sample{
		{at: start, metrics: snapshot{"m": 0}},
		{at: start.Add(5 * time.Second), metrics: snapshot{"m": 10}},
		{at: start.Add(10 * time.Second), metrics: snapshot{"m": 40}},
		{at: start.Add(20 * time.Second), metrics: snapshot{"m": 40}},
	}
	got := rates(samples, "m", 10*time.Second)
	if len(got) != 2 || got[0] != 4 || got[1] != 0 {
		t.Fatalf("got %v, want [4 0]", got)
	}
}

func TestTrackerTransitions(t *testing.T) {
	tr := newTracker()
	t0 := time.Unix(100, 0)
	tr.created("k", t0)
	tr.observe("k", false, t0.Add(time.Second))
	tr.observe("k", true, t0.Add(2*time.Second))
	tr.observe("k", false, t0.Add(3*time.Second))
	tr.observe("k", true, t0.Add(4*time.Second))
	st := tr.get("k")
	if !st.firstHealthy.Equal(t0.Add(2*time.Second)) || !st.unhealthyAt.Equal(t0.Add(3*time.Second)) ||
		!st.recoveredAt.Equal(t0.Add(4*time.Second)) || !st.healthy {
		t.Fatalf("got %+v", st)
	}
}

func TestPlanReferrers(t *testing.T) {
	objects := plan([]kind{entityAliasKind, entityKind}, 4)
	alias := objects[0].unstructured()
	ref, _, _ := unstructured.NestedString(alias.Object, "spec", "entityRef", litName)
	if ref != objects[1].name {
		t.Fatalf("alias %s references %q, want %q", objects[0].name, ref, objects[1].name)
	}
	if alias.GetAnnotations()["openbaoconfig.io/allow-destructive-delete"] != "true" {
		t.Fatal("generated objects must allow destructive delete")
	}
}
