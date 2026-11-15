//go:build load

// Package load runs the manager binary against envtest and a local OpenBao and measures ramp-up, steady state,
// drift repair, an OpenBao outage and deletion at a configurable object count. See docs/operate/load-testing.md and
// hack/load/run.sh.
package load

import (
	"context"
	"fmt"
	"math/rand/v2"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"k8s.io/apimachinery/pkg/runtime/schema"

	"github.com/alexandrust88/openbao-config-operator/test/testenv"
)

const (
	samplePeriod  = 2 * time.Second
	rateBucket    = 10 * time.Second
	probeKeyCount = 5
	refsDelay     = 5 * time.Second
	// treeDriftSample stays below the default write budget of 60 rewrites per minute, so every repair lands
	// within one drift interval.
	treeDriftSample = 50
)

type config struct {
	n               int
	kinds           string
	tree            treeShape
	scenarios       []string
	driftInterval   time.Duration
	maxConcurrent   int
	writeBudget     int
	outage          time.Duration
	steadyIntervals int
	workers         int
	out             string
	managerBin      string
	crdDir          string
	baoBin          string
	assets          string
	root            string
}

func loadConfig(t *testing.T) config {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	cfg := config{
		n:               envInt(t, "LOAD_N", 200),
		kinds:           envString("LOAD_KINDS", "simple"),
		driftInterval:   envDuration(t, "LOAD_DRIFT_INTERVAL", time.Minute),
		maxConcurrent:   envInt(t, "LOAD_MAX_CONCURRENT", 4),
		writeBudget:     envBudget(t),
		outage:          envDuration(t, "LOAD_OUTAGE", time.Minute),
		steadyIntervals: envInt(t, "LOAD_STEADY_INTERVALS", 3),
		workers:         envInt(t, "LOAD_WORKERS", 16),
		managerBin:      os.Getenv("LOAD_MANAGER_BIN"),
		crdDir:          envString("LOAD_CRD_DIR", filepath.Join(root, "config", "crd", "bases")),
		baoBin:          testenv.RequireBao(t),
		assets:          testenv.RequireAssets(t, root),
		root:            root,
	}
	if cfg.scenarios, err = parseScenarios(envString("LOAD_SCENARIOS", strings.Join(knownScenarios, ","))); err != nil {
		t.Fatal(err)
	}
	if cfg.kinds == kindSetTree {
		if cfg.tree, err = parseTreeShape(envString("LOAD_TREE", "100x10x20x3x3")); err != nil {
			t.Fatal(err)
		}
	}
	cfg.out = envString("LOAD_OUT", filepath.Join(root, ".tmp", "night", "loadtest",
		fmt.Sprintf("%s-n%d-mcr%d", cfg.kinds, cfg.n, cfg.maxConcurrent)))
	if cfg.managerBin == "" {
		cfg.managerBin = filepath.Join(t.TempDir(), "manager")
		build := exec.Command("go", "build", "-o", cfg.managerBin, "./cmd/")
		build.Dir = root
		if out, err := build.CombinedOutput(); err != nil {
			t.Fatalf("build manager: %v\n%s", err, out)
		}
	}
	return cfg
}

var knownScenarios = []string{"ramp", "steady", "drift", "outage", "delete"}

// parseScenarios rejects an unknown scenario, so a typo cannot leave a run without any phase.
func parseScenarios(list string) ([]string, error) {
	scenarios := strings.Split(list, ",")
	for _, s := range scenarios {
		if !slices.Contains(knownScenarios, s) {
			return nil, fmt.Errorf("unknown scenario %q in %q, want a comma-separated list of %s", s, list,
				strings.Join(knownScenarios, ", "))
		}
	}
	return scenarios, nil
}

func envString(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func envInt(t *testing.T, key string, def int) int {
	t.Helper()
	v := os.Getenv(key)
	if v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil || n <= 0 {
		t.Fatalf("%s=%q: want a positive integer", key, v)
	}
	return n
}

// envBudget reads LOAD_WRITE_BUDGET, the manager's --write-budget-per-minute; 0 keeps the manager default and a
// negative value disables the budget.
func envBudget(t *testing.T) int {
	t.Helper()
	v := os.Getenv("LOAD_WRITE_BUDGET")
	if v == "" {
		return 0
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		t.Fatalf("LOAD_WRITE_BUDGET=%q: want an integer", v)
	}
	return n
}

func envDuration(t *testing.T, key string, def time.Duration) time.Duration {
	t.Helper()
	v := os.Getenv(key)
	if v == "" {
		return def
	}
	d, err := time.ParseDuration(v)
	if err != nil || d <= 0 {
		t.Fatalf("%s=%q: want a positive duration", key, v)
	}
	return d
}

// results is the JSON document written to <out>/results.json.
type results struct {
	N             int            `json:"n"`
	Kinds         string         `json:"kinds"`
	KindNames     []string       `json:"kindNames"`
	DriftInterval string         `json:"driftInterval"`
	MaxConcurrent int            `json:"maxConcurrentReconciles"`
	WriteBudget   int            `json:"writeBudgetPerMinute"`
	Machine       string         `json:"machine"`
	LoadAvg       []string       `json:"loadAverageStartEnd"`
	Baseline      window         `json:"baseline"`
	Ramp          *rampResult    `json:"ramp,omitempty"`
	Steady        *steadyResult  `json:"steady,omitempty"`
	Drift         *driftResult   `json:"drift,omitempty"`
	Outage        *outageResult  `json:"outage,omitempty"`
	Delete        *deleteResult  `json:"delete,omitempty"`
	Tree          *treeResult    `json:"tree,omitempty"`
	Identity      []identityMark `json:"identityCounts"`
	Failures      []string       `json:"failures,omitempty"`
	Phases        map[string]any `json:"phaseOffsetsSeconds"`
}

// identityMark records the identity counts at a phase boundary.
type identityMark struct {
	Phase string `json:"phase"`
	identityCounts
}

type rampResult struct {
	CreateSeconds float64         `json:"createSeconds"`
	AllReadySec   float64         `json:"allReadySeconds"`
	ReadyMs       dist            `json:"readyLatencyMs"`
	ReadyMsByKind map[string]dist `json:"readyLatencyMsByKind"`
	// ReadyMsByLevel groups the tree set by its levels, from the parent namespaces down to the child objects.
	ReadyMsByLevel map[string]dist `json:"readyLatencyMsByLevel,omitempty"`
	ObjectsPerSec  float64         `json:"readyObjectsPerSecond"`
	// ReferrerMs is the time from applying the referents until each referrer was ready.
	ReferrerMs dist   `json:"referrerReadyAfterReferentsMs"`
	NotReady   int    `json:"notReady"`
	Window     window `json:"window"`
}

type steadyResult struct {
	Window                 window  `json:"window"`
	RequestsPerReconcile   float64 `json:"requestsPerReconcile"`
	ExpectedReconcilesPerS float64 `json:"expectedReconcilesPerSecond"`
	ObservedReconcilesPerS float64 `json:"observedReconcilesPerSecond"`
	ExpectedRequestsPerS   float64 `json:"expectedRequestsPerSecond"`
	RequestRate10s         dist    `json:"requestRatePer10sBucket"`
	ReconcileRate10s       dist    `json:"reconcileRatePer10sBucket"`
}

type driftResult struct {
	Tampered         int    `json:"tampered"`
	RepairMs         dist   `json:"repairMs"`
	NotRepaired      int    `json:"notRepaired"`
	Window           window `json:"window"`
	ProbeObjects     int    `json:"unmanagedKeyProbeObjects"`
	ProbeWrites      int    `json:"unmanagedKeyProbeWrites"`
	PostRepairWindow window `json:"postRepairWindow"`
}

type outageResult struct {
	OutageSeconds          float64 `json:"outageSeconds"`
	BaoRestartSeconds      float64 `json:"baoRestartToActiveSeconds"`
	ObjectsWentUnhealthy   int     `json:"objectsWentUnhealthy"`
	ConnectionWentNotReady bool    `json:"connectionWentNotReady"`
	ConnectionRecoverySec  float64 `json:"connectionRecoverySeconds"`
	AllHealthySec          float64 `json:"allHealthyAfterRestartSeconds"`
	RecoveryMs             dist    `json:"objectRecoveryAfterRestartMs"`
	NotRecovered           int     `json:"notRecovered"`
	During                 window  `json:"during"`
	Recovery               window  `json:"recovery"`
	RequestRate2sDuring    dist    `json:"requestRatePer2sDuring"`
	RequestRate2sRecovery  dist    `json:"requestRatePer2sRecovery"`
	ReconcileRate2sRecov   dist    `json:"reconcileRatePer2sRecovery"`
}

type deleteResult struct {
	AllGoneSeconds float64  `json:"allGoneSeconds"`
	FinalizeMs     dist     `json:"finalizeLatencyMs"`
	NotGone        int      `json:"notGone"`
	RemainingInBao []string `json:"remainingInOpenBao,omitempty"`
	Window         window   `json:"window"`
}

// loadRun holds everything one TestLoad run shares between phases.
type loadRun struct {
	t          *testing.T
	cfg        config
	ctx        context.Context
	c          cluster
	bao        *baoServer
	mgr        *manager
	tr         *tracker
	s          *sampler
	objects    []object
	keys       []string
	res        *results
	start      time.Time
	managerLog string
}

func TestLoad(t *testing.T) {
	cfg := loadConfig(t)
	kinds, err := kindSet(cfg.kinds)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(cfg.out, 0o750); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	r := setupRun(ctx, t, cfg, kinds)

	if has(cfg.scenarios, "ramp") {
		r.ramp()
	}
	if has(cfg.scenarios, "steady") {
		r.steady()
	}
	if has(cfg.scenarios, "drift") {
		r.drift()
	}
	if has(cfg.scenarios, "outage") {
		r.outage()
	}
	if has(cfg.scenarios, "delete") {
		r.deleteAll()
	}
	r.finish()
}

func has(list []string, s string) bool { return slices.Contains(list, s) }

func setupRun(ctx context.Context, t *testing.T, cfg config, kinds []kind) *loadRun {
	t.Helper()
	baoLog := mustCreateFile(t, filepath.Join(cfg.out, "bao.log"))
	managerLog := filepath.Join(cfg.out, "manager.log")
	mgrLog := mustCreateFile(t, managerLog)

	bao := newBaoServer(t, cfg.baoBin, t.TempDir(), baoLog)
	t.Cleanup(bao.stop)
	if err := bao.start(); err != nil {
		t.Fatal(err)
	}
	if err := bao.initialize(); err != nil {
		t.Fatal(err)
	}
	if err := bao.unseal(); err != nil {
		t.Fatal(err)
	}
	roleID, secretID, err := prepareBao(bao)
	if err != nil {
		t.Fatal(err)
	}

	c := startCluster(t, cfg, t.TempDir())
	// Informers must stop before envtest, whose apiserver otherwise waits for their watches on shutdown.
	watchCtx, stopWatch := context.WithCancel(ctx)
	t.Cleanup(stopWatch)
	tr := newTracker()
	gvrs := map[schema.GroupVersionResource]string{connectionGVR: kindConnection}
	kindNames := make([]string, 0, len(kinds))
	for _, k := range kinds {
		gvrs[k.gvr] = k.gvk.Kind
		kindNames = append(kindNames, k.gvk.Kind)
	}
	if err := watch(watchCtx, c.dyn, tr, gvrs); err != nil {
		t.Fatal(err)
	}
	createConnection(ctx, t, c, bao.addr, roleID, secretID)

	mgr := startManager(t, cfg, c.kubeconfig, mgrLog)
	connKey := "OpenBaoConnection/" + connectionName
	if err := poll(2*time.Minute, func() error {
		if !tr.get(connKey).healthy {
			return fmt.Errorf("connection %s not ready", connectionName)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	objects := plan(kinds, cfg.n)
	var tree *treeResult
	if cfg.kinds == kindSetTree {
		objects = planTree(cfg.tree, c.caPEM)
		tree = &treeResult{Shape: cfg.tree, Objects: len(objects), Connections: cfg.tree.Parents}
		cfg.n = len(objects) - cfg.tree.Parents
	}
	r := &loadRun{
		t: t, cfg: cfg, ctx: ctx, c: c, bao: bao, mgr: mgr, tr: tr, managerLog: managerLog,
		objects: objects, start: time.Now(),
		res: &results{N: cfg.n, Kinds: cfg.kinds, KindNames: kindNames, DriftInterval: cfg.driftInterval.String(),
			MaxConcurrent: cfg.maxConcurrent, WriteBudget: cfg.writeBudget, Machine: fmt.Sprintf("%s/%s, %d CPUs", runtime.GOOS, runtime.GOARCH,
				runtime.NumCPU()), Phases: map[string]any{}, Tree: tree},
	}
	for _, o := range r.objects {
		r.keys = append(r.keys, o.key())
	}
	r.s = startSampler(mgr.metricsURL, mgr.cmd.Process.Pid, func() int { return int(bao.pid.Load()) }, samplePeriod)
	t.Cleanup(r.s.close)
	baseStart := time.Now()
	time.Sleep(10 * time.Second)
	r.res.Baseline = r.window(baseStart, time.Now())
	r.res.LoadAvg = append(r.res.LoadAvg, loadAverage())
	r.markIdentity("baseline")
	return r
}

func mustCreateFile(t *testing.T, path string) *os.File {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = f.Close() })
	return f
}

func (r *loadRun) mark(phase string, at time.Time) {
	r.res.Phases[phase] = seconds(at.Sub(r.start))
}

// markIdentity records the identity counts after phase. Design rule 1: only the managed Entity and EntityAlias
// objects may change them; token accessors grow by one per generated connection, because the operator logs in
// once per connection, and shrink back when the connections are deleted.
func (r *loadRun) markIdentity(phase string) {
	c, err := countIdentity(r.bao)
	if err != nil {
		r.t.Errorf("count identity after %s: %v", phase, err)
		return
	}
	if phase == "baseline" {
		r.res.Identity = append(r.res.Identity, identityMark{Phase: phase, identityCounts: c})
		return
	}
	base := r.res.Identity[0]
	entities, aliases, connections := 0, 0, 0
	if phase != "delete" {
		for _, o := range r.objects {
			switch o.kind.gvk.Kind {
			case "Entity":
				entities++
			case "EntityAlias":
				aliases++
			case kindConnection:
				connections++
			}
		}
	}
	want := identityCounts{TokenAccessors: base.TokenAccessors + connections, Entities: base.Entities + entities,
		EntityAliases: base.EntityAliases + aliases}
	// A deleted connection revokes its token in the background.
	for deadline := time.Now().Add(30 * time.Second); c != want && time.Now().Before(deadline); {
		time.Sleep(time.Second)
		if c, err = countIdentity(r.bao); err != nil {
			r.t.Errorf("count identity after %s: %v", phase, err)
			return
		}
	}
	r.res.Identity = append(r.res.Identity, identityMark{Phase: phase, identityCounts: c})
	if c != want {
		r.fail("identity after %s: got %+v, want %+v", phase, c, want)
	}
}

func (r *loadRun) window(from, to time.Time) window {
	return newWindow(r.s.window(from, to), countErrorLines(r.managerLog))
}

func (r *loadRun) fail(format string, args ...any) {
	msg := fmt.Sprintf(format, args...)
	r.t.Error(msg)
	r.res.Failures = append(r.res.Failures, msg)
}

// waitAll polls until every key satisfies fn or timeout passes, and returns how many do not.
func (r *loadRun) waitAll(timeout time.Duration, fn func(objectState) bool) int {
	return r.waitKeys(r.keys, timeout, fn)
}

func (r *loadRun) waitKeys(keys []string, timeout time.Duration, fn func(objectState) bool) int {
	deadline := time.Now().Add(timeout)
	for {
		missing := len(keys) - r.tr.count(keys, fn)
		if missing == 0 || time.Now().After(deadline) {
			return missing
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func (r *loadRun) readyTimeout() time.Duration {
	return 5*time.Minute + time.Duration(r.cfg.n)*100*time.Millisecond
}

func (r *loadRun) ramp() {
	start := time.Now()
	r.mark("rampStart", start)
	referents, err := createObjects(r.ctx, r.c.k8s, r.tr, r.objects, r.cfg.workers, refsDelay)
	if err != nil {
		r.t.Fatal(err)
	}
	created := time.Now()
	notReady := r.waitAll(r.readyTimeout(), func(s objectState) bool { return !s.firstHealthy.IsZero() })
	var latencies, afterReferents []float64
	byKind, byLevel := map[string][]float64{}, map[string][]float64{}
	var last time.Time
	for _, o := range r.objects {
		st := r.tr.get(o.key())
		if st.firstHealthy.IsZero() {
			continue
		}
		ms := float64(st.firstHealthy.Sub(st.created).Milliseconds())
		latencies = append(latencies, ms)
		byKind[o.kind.gvk.Kind] = append(byKind[o.kind.gvk.Kind], ms)
		if o.level != "" {
			byLevel[o.level] = append(byLevel[o.level], ms)
		}
		if o.kind.referrer {
			afterReferents = append(afterReferents, float64(st.firstHealthy.Sub(referents).Milliseconds()))
		}
		if st.firstHealthy.After(last) {
			last = st.firstHealthy
		}
	}
	r.mark("rampAllReady", last)
	r.res.Ramp = &rampResult{
		CreateSeconds: seconds(created.Sub(start)),
		AllReadySec:   seconds(last.Sub(start)),
		ReadyMs:       summarize(latencies),
		ReadyMsByKind: map[string]dist{},
		ReferrerMs:    summarize(afterReferents),
		NotReady:      notReady,
		Window:        r.window(start, last),
	}
	for k, v := range byKind {
		r.res.Ramp.ReadyMsByKind[k] = summarize(v)
	}
	if len(byLevel) > 0 {
		r.res.Ramp.ReadyMsByLevel = map[string]dist{}
		for k, v := range byLevel {
			r.res.Ramp.ReadyMsByLevel[k] = summarize(v)
		}
	}
	if r.res.Ramp.AllReadySec > 0 {
		r.res.Ramp.ObjectsPerSec = round(float64(len(latencies)) / r.res.Ramp.AllReadySec)
	}
	if notReady > 0 {
		r.fail("ramp: %d objects never became ready", notReady)
	}
	r.markIdentity("ramp")
	if r.res.Tree != nil {
		r.markTree("ramp", r.cfg.tree.counts())
	}
	r.t.Logf("ramp: created in %.1fs, all ready in %.1fs (%.1f objects/s), p50 %.0fms p95 %.0fms p99 %.0fms",
		r.res.Ramp.CreateSeconds, r.res.Ramp.AllReadySec, r.res.Ramp.ObjectsPerSec, r.res.Ramp.ReadyMs.P50,
		r.res.Ramp.ReadyMs.P95, r.res.Ramp.ReadyMs.P99)
}

func (r *loadRun) steady() {
	// The first sample of the window must come after the last ramp write.
	time.Sleep(samplePeriod + time.Second)
	start := time.Now()
	r.mark("steadyStart", start)
	time.Sleep(time.Duration(r.cfg.steadyIntervals) * r.cfg.driftInterval)
	end := time.Now()
	r.mark("steadyEnd", end)
	samples := r.s.window(start, end)
	w := newWindow(samples, countErrorLines(r.managerLog))
	interval := r.cfg.driftInterval.Seconds()
	res := &steadyResult{
		Window:                 w,
		ExpectedReconcilesPerS: round(float64(r.cfg.n) / interval),
		RequestRate10s:         summarize(rates(samples, "openbao_requests_total", rateBucket)),
		ReconcileRate10s:       summarize(rates(samples, "controller_runtime_reconcile_total", rateBucket)),
	}
	if w.Seconds > 0 {
		res.ObservedReconcilesPerS = round(w.Reconciles / w.Seconds)
	}
	if w.Reconciles > 0 {
		res.RequestsPerReconcile = round(w.OpenBaoRequests / w.Reconciles)
		res.ExpectedRequestsPerS = round(res.RequestsPerReconcile * float64(r.cfg.n) / interval)
	}
	r.res.Steady = res
	if w.Writes != 0 {
		r.fail("steady: %v OpenBao writes, want 0 (%v)", w.Writes, w.WritesByKind)
	}
	if patches := w.KubeByMethod["PATCH"] + w.KubeByMethod["PUT"] + w.KubeByMethod["POST"]; patches != 0 {
		r.fail("steady: %v Kubernetes writes, want 0 (%v)", patches, w.KubeByMethod)
	}
	r.markIdentity("steady")
	r.t.Logf("steady: %.2f reconciles/s (expected %.2f), %.2f requests/s, writes %v",
		res.ObservedReconcilesPerS, res.ExpectedReconcilesPerS, w.OpenBaoPerSecond, w.Writes)
}

// drift tampers with a tenth of the Policy and KVSecret objects (at most treeDriftSample in the tree set) and times
// their repair. A few other KVSecrets get an unmanaged key; their KV version counts the operator's writes to them
// exactly.
func (r *loadRun) drift() {
	var candidates []object
	for _, o := range r.objects {
		if o.kind.gvk.Kind == kindPolicy || o.kind.gvk.Kind == kindKVSecret {
			candidates = append(candidates, o)
		}
	}
	rand.Shuffle(len(candidates), func(i, j int) { candidates[i], candidates[j] = candidates[j], candidates[i] })
	count := min(max(r.cfg.n/10, 1), len(candidates))
	if r.res.Tree != nil {
		count = min(count, treeDriftSample)
	}
	targets := candidates[:count]
	var probes []object
	for _, o := range candidates[count:] {
		if o.kind.gvk.Kind == kindKVSecret && len(probes) < probeKeyCount {
			probes = append(probes, o)
		}
	}

	start := time.Now()
	r.mark("driftStart", start)
	if err := parallel(targets, r.cfg.workers, func(o object) error { return tamper(r.bao, o) }); err != nil {
		r.t.Fatal(err)
	}
	for _, o := range probes {
		if err := addUnmanagedKey(r.bao, o); err != nil {
			r.t.Fatal(err)
		}
	}
	probeVersions := map[string]int{}
	for _, o := range probes {
		v, _, err := kvState(r.bao, o)
		if err != nil {
			r.t.Fatal(err)
		}
		probeVersions[o.name] = v
	}
	repairedAt := map[string]time.Time{}
	deadline := start.Add(2*r.cfg.driftInterval + time.Minute)
	for len(repairedAt) < len(targets) && time.Now().Before(deadline) {
		for _, o := range targets {
			if _, done := repairedAt[o.key()]; done {
				continue
			}
			ok, err := repaired(r.bao, o)
			if err == nil && ok {
				repairedAt[o.key()] = time.Now()
			}
		}
		time.Sleep(500 * time.Millisecond)
	}
	end := time.Now()
	repairs := make([]float64, 0, len(repairedAt))
	for _, at := range repairedAt {
		repairs = append(repairs, float64(at.Sub(start).Milliseconds()))
	}
	r.mark("driftRepaired", end)

	// Two more intervals reconcile every object at least once more: a repaired object must not be written again,
	// and a probe object at most once.
	time.Sleep(samplePeriod + time.Second)
	postStart := time.Now()
	time.Sleep(2 * r.cfg.driftInterval)
	postEnd := time.Now()
	r.mark("driftPostEnd", postEnd)
	post := r.window(postStart, postEnd)
	probeWrites := 0
	for _, o := range probes {
		v, _, err := kvState(r.bao, o)
		if err != nil {
			r.t.Fatal(err)
		}
		probeWrites += v - probeVersions[o.name]
	}
	r.res.Drift = &driftResult{
		Tampered:         len(targets),
		RepairMs:         summarize(repairs),
		NotRepaired:      len(targets) - len(repairedAt),
		Window:           r.window(start, end),
		ProbeObjects:     len(probes),
		ProbeWrites:      probeWrites,
		PostRepairWindow: post,
	}
	if n := r.res.Drift.NotRepaired; n > 0 {
		r.fail("drift: %d of %d tampered objects not repaired", n, len(targets))
	}
	if other := post.Writes - float64(probeWrites); other > 0 {
		r.fail("drift: %v writes after repair besides the probes (%v), want 0", other, post.WritesByKind)
	}
	if r.res.Drift.ProbeWrites > len(probes) {
		r.fail("drift: %v writes to %d KVSecrets with an unmanaged key over %s, want at most one each",
			r.res.Drift.ProbeWrites, len(probes), postEnd.Sub(start).Round(time.Second))
	}
	r.markIdentity("drift")
	r.t.Logf("drift: %d tampered, repair p50 %.0fms p95 %.0fms max %.0fms, post-repair writes %v",
		len(targets), r.res.Drift.RepairMs.P50, r.res.Drift.RepairMs.P95, r.res.Drift.RepairMs.Max, post.Writes)
}

func (r *loadRun) outage() {
	connKey := "OpenBaoConnection/" + connectionName
	start := time.Now()
	r.mark("outageStart", start)
	r.bao.stop()
	time.Sleep(r.cfg.outage)
	restart := time.Now()
	if err := r.bao.start(); err != nil {
		r.t.Fatal(err)
	}
	if err := r.bao.unseal(); err != nil {
		r.t.Fatal(err)
	}
	active := time.Now()
	r.mark("outageBaoActive", active)

	deadline := active.Add(10 * time.Minute)
	for time.Now().Before(deadline) {
		conn := r.tr.get(connKey)
		missing := len(r.keys) - r.tr.count(r.keys, func(s objectState) bool { return s.healthy })
		if conn.healthy && missing == 0 {
			break
		}
		time.Sleep(200 * time.Millisecond)
	}
	allHealthy := time.Now()
	r.mark("outageRecovered", allHealthy)

	conn := r.tr.get(connKey)
	res := &outageResult{
		OutageSeconds:          seconds(restart.Sub(start)),
		BaoRestartSeconds:      seconds(active.Sub(restart)),
		ConnectionWentNotReady: conn.unhealthyAt.After(start),
		AllHealthySec:          seconds(allHealthy.Sub(active)),
		During:                 r.window(start, active),
		Recovery:               r.window(active, allHealthy),
	}
	if res.ConnectionWentNotReady && conn.recoveredAt.After(start) {
		res.ConnectionRecoverySec = seconds(conn.recoveredAt.Sub(active))
	}
	var recoveries []float64
	for _, k := range r.keys {
		st := r.tr.get(k)
		if !st.unhealthyAt.After(start) {
			continue
		}
		res.ObjectsWentUnhealthy++
		if !st.healthy || !st.recoveredAt.After(st.unhealthyAt) {
			res.NotRecovered++
			continue
		}
		recoveries = append(recoveries, float64(st.recoveredAt.Sub(active).Milliseconds()))
	}
	res.RecoveryMs = summarize(recoveries)
	during := r.s.window(start, active)
	recovery := r.s.window(active, allHealthy)
	res.RequestRate2sDuring = summarize(rates(during, "openbao_requests_total", samplePeriod))
	res.RequestRate2sRecovery = summarize(rates(recovery, "openbao_requests_total", samplePeriod))
	res.ReconcileRate2sRecov = summarize(rates(recovery, "controller_runtime_reconcile_total", samplePeriod))
	r.res.Outage = res
	if res.NotRecovered > 0 || !conn.healthy {
		r.fail("outage: %d objects not recovered, connection healthy %v", res.NotRecovered, conn.healthy)
	}
	r.markIdentity("outage")
	r.t.Logf("outage: %d objects went unhealthy, connection not-ready %v (recovered %.1fs after restart), "+
		"all healthy %.1fs after restart, recovery writes %v",
		res.ObjectsWentUnhealthy, res.ConnectionWentNotReady, res.ConnectionRecoverySec, res.AllHealthySec,
		res.Recovery.Writes)
}

func (r *loadRun) deleteAll() {
	start := time.Now()
	r.mark("deleteStart", start)
	notGone := 0
	for _, stage := range r.deleteStages() {
		if err := deleteObjects(r.ctx, r.c.k8s, r.tr, stage, r.cfg.workers); err != nil {
			r.t.Fatal(err)
		}
		keys := make([]string, 0, len(stage))
		for _, o := range stage {
			keys = append(keys, o.key())
		}
		notGone += r.waitKeys(keys, r.readyTimeout(), func(s objectState) bool { return !s.gone.IsZero() })
	}
	var latencies []float64
	var last time.Time
	for _, k := range r.keys {
		st := r.tr.get(k)
		if st.gone.IsZero() {
			continue
		}
		latencies = append(latencies, float64(st.gone.Sub(st.deleteRequested).Milliseconds()))
		if st.gone.After(last) {
			last = st.gone
		}
	}
	r.mark("deleteAllGone", last)
	left, err := remaining(r.bao)
	if err != nil {
		r.t.Error(err)
	}
	r.res.Delete = &deleteResult{
		AllGoneSeconds: seconds(last.Sub(start)),
		FinalizeMs:     summarize(latencies),
		NotGone:        notGone,
		RemainingInBao: left,
		Window:         r.window(start, last),
	}
	if notGone > 0 || len(left) > 0 {
		r.fail("delete: %d resources not finalized, Policy/KVSecret objects left in OpenBao: %v", notGone, left)
	}
	r.markIdentity("delete")
	if r.res.Tree != nil {
		// Namespaces and the kubernetes auth configs keep the default deletionPolicy Orphan; the namespaces stay
		// and the configs go with their auth mounts.
		r.markTree("delete", treeCounts{Namespaces: r.cfg.tree.namespaces()})
	}
	r.t.Logf("delete: all gone in %.1fs, finalize p50 %.0fms p95 %.0fms", r.res.Delete.AllGoneSeconds,
		r.res.Delete.FinalizeMs.P50, r.res.Delete.FinalizeMs.P95)
}

// deleteStages returns the objects in the order they are deleted: the tree set level by level, children before
// their parents and each connection after everything that uses it; the other sets all at once.
func (r *loadRun) deleteStages() [][]object {
	if r.res.Tree == nil {
		return [][]object{r.objects}
	}
	byLevel := map[string][]object{}
	for _, o := range r.objects {
		byLevel[o.level] = append(byLevel[o.level], o)
	}
	var stages [][]object
	for _, level := range slices.Backward(treeLevels) {
		stages = append(stages, byLevel[level])
	}
	return stages
}

func (r *loadRun) finish() {
	r.res.LoadAvg = append(r.res.LoadAvg, loadAverage())
	samples := r.s.all()
	out := func(name string) string { return filepath.Join(r.cfg.out, name) }
	for name, write := range map[string]func() error{
		"results.json":   func() error { return writeJSON(out("results.json"), r.res) },
		"timeseries.csv": func() error { return writeTimeseries(out("timeseries.csv"), r.start, samples) },
		"objects.csv":    func() error { return writeObjects(out("objects.csv"), r.tr, r.objects) },
		"events.txt":     func() error { return writeEvents(r.ctx, r.c.k8s, out("events.txt")) },
	} {
		if err := write(); err != nil {
			r.t.Errorf("write %s: %v", name, err)
		}
	}
	r.t.Logf("results in %s", r.cfg.out)
}

// loadAverage returns the host's 1, 5 and 15 minute load averages; other work on the host skews every timing.
func loadAverage() string {
	out, err := exec.Command("sysctl", "-n", "vm.loadavg").Output()
	if err != nil {
		return ""
	}
	return strings.Trim(strings.TrimSpace(string(out)), "{} ")
}
