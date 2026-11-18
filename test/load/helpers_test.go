//go:build load

package load

import (
	"math"
	"reflect"
	"slices"
	"testing"
	"time"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

func TestParseSeries(t *testing.T) {
	name, labels := parseSeries(`obco_requests_total{code="200",connection="c",method="GET"}`)
	if name != "obco_requests_total" || labels["code"] != "200" || labels["method"] != "GET" {
		t.Fatalf("got %q %v", name, labels)
	}
	if name, labels := parseSeries("go_goroutines"); name != "go_goroutines" || labels != nil {
		t.Fatalf("got %q %v", name, labels)
	}
}

func TestSnapshotAddSkipsConnectionControllers(t *testing.T) {
	s := snapshot{}
	s.add("controller_runtime_reconcile_total", map[string]string{"controller": "policy.sys", "result": "success"}, 3)
	s.add("controller_runtime_reconcile_total", map[string]string{"controller": "baoconnection", "result": "success"}, 5)
	if s["controller_runtime_reconcile_total"] != 3 || s["controller_runtime_reconcile_total:result=success"] != 3 {
		t.Fatalf("got %v", s)
	}
}

func TestCarryKeepsDeletedConnectionCounters(t *testing.T) {
	seen := map[string]float64{}
	first := map[string]float64{`obco_requests_total{code="200",connection="a"}`: 5,
		`obco_requests_total{code="200",connection="b"}`: 7, "go_goroutines": 10}
	carry(seen, first)
	second := map[string]float64{`obco_requests_total{code="200",connection="a"}`: 6, "go_goroutines": 9}
	carry(seen, second)
	if got := aggregate(second)["obco_requests_total"]; got != 13 {
		t.Fatalf("obco_requests_total = %v, want 13: a deleted connection must not lower the sum", got)
	}
	if got := aggregate(second)["go_goroutines"]; got != 9 {
		t.Fatalf("go_goroutines = %v, want 9", got)
	}
	third := map[string]float64{`obco_requests_total{code="200",connection="a"}`: 6,
		`obco_requests_total{code="200",connection="b"}`: 1}
	carry(seen, third)
	if got := aggregate(third)["obco_requests_total"]; got != 13 {
		t.Fatalf("obco_requests_total = %v, want 13: a restarted series must not lower the sum", got)
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
	if alias.GetAnnotations()["obco.dev/allow-destructive-delete"] != "true" {
		t.Fatal("generated objects must allow destructive delete")
	}
}

func TestParseTreeShape(t *testing.T) {
	got, err := parseTreeShape("5x3x4")
	if err != nil || got != (treeShape{Parents: 5, Children: 3, KV: 4, Roles: 3, Policies: 3}) {
		t.Fatalf("got %+v, %v", got, err)
	}
	if got, err := parseTreeShape("1x2x3x4x5"); err != nil || got.Roles != 4 || got.Policies != 5 {
		t.Fatalf("got %+v, %v", got, err)
	}
	for _, bad := range []string{"5x3", "5x0x4", "5x3x4x1", "axbxc", "5x3x-4"} {
		if _, err := parseTreeShape(bad); err == nil {
			t.Errorf("parseTreeShape(%q): want an error", bad)
		}
	}
}

func TestPlanTree(t *testing.T) {
	s := treeShape{Parents: 2, Children: 3, KV: 4, Roles: 2, Policies: 1}
	objects := planTree(s, "ca")
	// Per namespace: the namespace, the auth mount, its config, the kv mounts, the roles and the policies.
	if want := s.namespaces()*(3+s.KV+s.Roles+s.Policies) + s.Parents; len(objects) != want {
		t.Fatalf("got %d objects, want %d", len(objects), want)
	}
	byName := map[string]object{}
	last := 0
	for _, o := range objects {
		level := slices.Index(treeLevels, o.level)
		if level < last {
			t.Fatalf("%s at level %s comes after level %s", o.key(), o.level, treeLevels[last])
		}
		last = level
		byName[o.name] = o
	}

	spec := func(name string) map[string]any {
		u := byName[name].unstructured()
		s, _, _ := unstructured.NestedMap(u.Object, "spec")
		return s
	}
	for name, want := range map[string]map[string]any{
		"p001":                {"connectionRef": map[string]any{litName: "bao"}},
		"p002-c03":            {"connectionRef": map[string]any{litName: "bao-p002"}},
		"p001-kv-04":          {"connectionRef": map[string]any{litName: "bao"}, "openbaoNamespace": "p001", "deletionPolicy": "Delete"},
		"p002-c03-k8s-role-2": {"connectionRef": map[string]any{litName: "bao-p002"}, "openbaoNamespace": "p002-c03", litMountPath: "p002-c03-k8s", "deletionPolicy": "Delete"},
		"p002-c03-k8s-config": {"connectionRef": map[string]any{litName: "bao-p002"}, "openbaoNamespace": "p002-c03", litMountPath: "p002-c03-k8s"},
		"bao-p002":            {"openbaoNamespace": "p002", "openbaoAuthNamespace": ""},
	} {
		got := spec(name)
		for k, v := range want {
			if !reflect.DeepEqual(got[k], v) {
				t.Errorf("%s: spec.%s = %v, want %v", name, k, got[k], v)
			}
		}
	}
	if _, ok := spec("p001")["deletionPolicy"]; ok {
		t.Error("namespaces must keep the default deletionPolicy")
	}
	if _, ok := spec("bao-p002")["connectionRef"]; ok {
		t.Error("a connection has no connectionRef")
	}
	if got := byName["p002-c03-pol-1"].baoNS; got != "p002/p002-c03" {
		t.Errorf("child policy lives in %q, want p002/p002-c03", got)
	}
}

func TestParseScenarios(t *testing.T) {
	if got, err := parseScenarios("ramp,delete"); err != nil || !slices.Equal(got, []string{"ramp", "delete"}) {
		t.Fatalf("got %v, %v", got, err)
	}
	for _, bad := range []string{"drfit", "ramp,drfit", "ramp,,delete", ""} {
		if _, err := parseScenarios(bad); err == nil {
			t.Errorf("parseScenarios(%q): want an error", bad)
		}
	}
}
