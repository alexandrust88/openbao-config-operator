//go:build load

package load

import (
	"bufio"
	"context"
	"fmt"
	"maps"
	"math"
	"net/http"
	"os/exec"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/dynamic/dynamicinformer"
	"k8s.io/client-go/tools/cache"
)

// snapshot holds aggregated manager metrics, keyed as "<metric>" or "<metric>:<label>=<value>".
type snapshot map[string]float64

// scrape reads the manager's Prometheus text endpoint and returns every series with its value.
func scrape(url string) (map[string]float64, error) {
	resp, err := http.Get(url) //nolint:gosec // local test endpoint
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("metrics: status %d", resp.StatusCode)
	}
	series := map[string]float64{}
	sc := bufio.NewScanner(resp.Body)
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	for sc.Scan() {
		line := sc.Text()
		if line == "" || line[0] == '#' {
			continue
		}
		cut := strings.LastIndexByte(line, ' ')
		if cut < 0 {
			continue
		}
		value, err := strconv.ParseFloat(line[cut+1:], 64)
		if err != nil {
			continue
		}
		series[line[:cut]] = value
	}
	return series, sc.Err()
}

// aggregate keeps the series the report needs, summed over controllers and connections.
func aggregate(series map[string]float64) snapshot {
	s := snapshot{}
	for line, value := range series {
		name, labels := parseSeries(line)
		s.add(name, labels, value)
	}
	return s
}

// perConnectionCounters lose their series when a connection is deleted, and the token revoke that follows can
// start them again from zero; carry keeps their highest value so the sums stay monotonic.
var perConnectionCounters = []string{"obco_requests_total{", "obco_request_duration_seconds_bucket{",
	"obco_logins_total{"}

// carry adds to series the per-connection counters seen before and missing or lower now, and remembers them.
func carry(seen, series map[string]float64) {
	for line, value := range series {
		if slices.ContainsFunc(perConnectionCounters, func(p string) bool { return strings.HasPrefix(line, p) }) {
			seen[line] = max(value, seen[line])
		}
	}
	maps.Copy(series, seen)
}

func parseSeries(series string) (string, map[string]string) {
	name, rest, ok := strings.Cut(series, "{")
	if !ok {
		return series, nil
	}
	labels := map[string]string{}
	for pair := range strings.SplitSeq(strings.TrimSuffix(rest, "}"), ",") {
		k, v, ok := strings.Cut(pair, "=")
		if ok {
			labels[k] = strings.Trim(v, `"`)
		}
	}
	return name, labels
}

// isOperatorController skips the two connection controllers so reconcile counts cover managed kinds only.
func isOperatorController(labels map[string]string) bool {
	c := labels["controller"]
	return c != "baoconnection" && c != "clusterbaoconnection"
}

func (s snapshot) add(name string, labels map[string]string, v float64) {
	switch name {
	case "obco_requests_total":
		s[name] += v
		s[name+":method="+labels["method"]] += v
		s[name+":code="+labels["code"]] += v
	case "obco_request_duration_seconds_bucket":
		s[name+":le="+labels["le"]] += v
	case "obco_logins_total":
		s[name] += v
	case "obco_writes_total":
		s[name] += v
		s[name+":kind="+labels["kind"]] += v
	case "controller_runtime_reconcile_total":
		if isOperatorController(labels) {
			s[name] += v
			s[name+":result="+labels["result"]] += v
		}
	case "controller_runtime_reconcile_errors_total":
		if isOperatorController(labels) {
			s[name] += v
		}
	case "controller_runtime_reconcile_time_seconds_bucket":
		if isOperatorController(labels) {
			s[name+":le="+labels["le"]] += v
		}
	case "workqueue_depth", "workqueue_retries_total", "workqueue_adds_total":
		s[name] += v
	case "rest_client_requests_total":
		s[name] += v
		s[name+":method="+labels["method"]] += v
	case "go_goroutines", "go_memstats_heap_inuse_bytes", "process_cpu_seconds_total",
		"process_resident_memory_bytes", "process_open_fds":
		s[name] = v
	}
}

// delta returns b-a for every key of b.
func delta(a, b snapshot) snapshot {
	d := snapshot{}
	for k, v := range b {
		d[k] = v - a[k]
	}
	return d
}

// quantile estimates a quantile from the cumulative "<metric>:le=" buckets of s, as histogram_quantile does.
func (s snapshot) quantile(metric string, q float64) float64 {
	type bucket struct{ le, count float64 }
	var buckets []bucket
	prefix := metric + ":le="
	for k, v := range s {
		if !strings.HasPrefix(k, prefix) {
			continue
		}
		le, err := strconv.ParseFloat(strings.TrimPrefix(k, prefix), 64)
		if err != nil {
			continue
		}
		buckets = append(buckets, bucket{le, v})
	}
	sort.Slice(buckets, func(i, j int) bool { return buckets[i].le < buckets[j].le })
	if len(buckets) == 0 || buckets[len(buckets)-1].count == 0 {
		return 0
	}
	rank := q * buckets[len(buckets)-1].count
	prevLE, prevCount := 0.0, 0.0
	for _, b := range buckets {
		if b.count >= rank {
			if math.IsInf(b.le, 1) {
				return prevLE
			}
			if b.count == prevCount {
				return b.le
			}
			return prevLE + (b.le-prevLE)*(rank-prevCount)/(b.count-prevCount)
		}
		prevLE, prevCount = b.le, b.count
	}
	return prevLE
}

// sample is one point of the time series.
type sample struct {
	at      time.Time
	metrics snapshot
	rssKB   float64
	cpuSec  float64
	baoKB   float64
}

// sampler scrapes the manager, and reads the manager's and OpenBao's ps stats, every period until stopped.
type sampler struct {
	mu      sync.Mutex
	samples []sample
	seen    map[string]float64
	stop    chan struct{}
	done    chan struct{}
}

func startSampler(url string, pid int, baoPID func() int, period time.Duration) *sampler {
	s := &sampler{stop: make(chan struct{}), done: make(chan struct{}), seen: map[string]float64{}}
	go func() {
		defer close(s.done)
		ticker := time.NewTicker(period)
		defer ticker.Stop()
		for {
			s.take(url, pid, baoPID())
			select {
			case <-s.stop:
				return
			case <-ticker.C:
			}
		}
	}()
	return s
}

func (s *sampler) take(url string, pid, baoPID int) {
	at := time.Now()
	series, err := scrape(url)
	if err != nil {
		return
	}
	carry(s.seen, series)
	m := aggregate(series)
	rss, cpu := psStats(pid)
	var baoRSS float64
	if baoPID != 0 {
		baoRSS, _ = psStats(baoPID)
	}
	s.mu.Lock()
	s.samples = append(s.samples, sample{at: at, metrics: m, rssKB: rss, cpuSec: cpu, baoKB: baoRSS})
	s.mu.Unlock()
}

func (s *sampler) close() {
	close(s.stop)
	<-s.done
}

// window returns the samples from the last one taken at or before from to the first one taken at or after to,
// so the deltas of the first and last sample cover [from, to]. It waits for that last sample.
func (s *sampler) window(from, to time.Time) []sample {
	deadline := time.Now().Add(3 * samplePeriod)
	for time.Now().Before(deadline) && !s.has(to) {
		time.Sleep(100 * time.Millisecond)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	first, last := 0, len(s.samples)-1
	for i, x := range s.samples {
		if !x.at.After(from) {
			first = i
		}
		if !x.at.Before(to) {
			last = i
			break
		}
	}
	if last < first {
		return nil
	}
	return append([]sample(nil), s.samples[first:last+1]...)
}

// has reports whether a sample was taken at or after at.
func (s *sampler) has(at time.Time) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.samples) > 0 && !s.samples[len(s.samples)-1].at.Before(at)
}

func (s *sampler) all() []sample {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]sample(nil), s.samples...)
}

// psStats returns the resident set size in KiB and the accumulated CPU time in seconds of pid.
func psStats(pid int) (float64, float64) {
	out, err := exec.Command("ps", "-o", "rss=,time=", "-p", strconv.Itoa(pid)).Output()
	if err != nil {
		return 0, 0
	}
	fields := strings.Fields(string(out))
	if len(fields) != 2 {
		return 0, 0
	}
	rss, _ := strconv.ParseFloat(fields[0], 64)
	return rss, parseCPUTime(fields[1])
}

// parseCPUTime parses ps time as [[dd-]hh:]mm:ss.ff.
func parseCPUTime(s string) float64 {
	days := 0.0
	if d, rest, ok := strings.Cut(s, "-"); ok {
		days, _ = strconv.ParseFloat(d, 64)
		s = rest
	}
	total := 0.0
	for part := range strings.SplitSeq(s, ":") {
		v, _ := strconv.ParseFloat(part, 64)
		total = total*60 + v
	}
	return days*86400 + total
}

// objectState is what the tracker knows about one resource.
type objectState struct {
	created, firstHealthy, deleteRequested, gone time.Time
	healthy                                      bool
	// unhealthyAt and recoveredAt are the last transitions away from and back to healthy after the first
	// healthy observation.
	unhealthyAt, recoveredAt time.Time
}

// tracker follows the Ready and Synced conditions of every resource through informers.
type tracker struct {
	mu   sync.Mutex
	objs map[string]*objectState
}

func newTracker() *tracker { return &tracker{objs: map[string]*objectState{}} }

func (tr *tracker) entry(key string) *objectState {
	e, ok := tr.objs[key]
	if !ok {
		e = &objectState{}
		tr.objs[key] = e
	}
	return e
}

func (tr *tracker) created(key string, at time.Time) {
	tr.mu.Lock()
	defer tr.mu.Unlock()
	tr.entry(key).created = at
}

func (tr *tracker) deleteRequested(key string, at time.Time) {
	tr.mu.Lock()
	defer tr.mu.Unlock()
	tr.entry(key).deleteRequested = at
}

func (tr *tracker) observe(key string, healthy bool, at time.Time) {
	tr.mu.Lock()
	defer tr.mu.Unlock()
	e := tr.entry(key)
	switch {
	case healthy && e.firstHealthy.IsZero():
		e.firstHealthy = at
	case healthy && !e.healthy:
		e.recoveredAt = at
	case !healthy && e.healthy:
		e.unhealthyAt = at
	}
	e.healthy = healthy
}

func (tr *tracker) remove(key string, at time.Time) {
	tr.mu.Lock()
	defer tr.mu.Unlock()
	e := tr.entry(key)
	e.gone = at
	e.healthy = false
}

func (tr *tracker) get(key string) objectState {
	tr.mu.Lock()
	defer tr.mu.Unlock()
	return *tr.entry(key)
}

// count returns how many of keys satisfy fn.
func (tr *tracker) count(keys []string, fn func(objectState) bool) int {
	tr.mu.Lock()
	defer tr.mu.Unlock()
	n := 0
	for _, k := range keys {
		if fn(*tr.entry(k)) {
			n++
		}
	}
	return n
}

// healthy reports Ready=True and Synced=True for the current generation. A connection has no Synced condition.
func healthy(u *unstructured.Unstructured) bool {
	generation := u.GetGeneration()
	observed, _, _ := unstructured.NestedInt64(u.Object, "status", "observedGeneration")
	if observed != generation {
		return false
	}
	conditions, _, _ := unstructured.NestedSlice(u.Object, "status", "conditions")
	want := map[string]bool{"Ready": false, "Synced": u.GetKind() == kindConnection}
	for _, c := range conditions {
		m, _ := c.(map[string]any)
		t, _ := m[litType].(string)
		if _, ok := want[t]; ok && m["status"] == "True" {
			want[t] = true
		}
	}
	return want["Ready"] && want["Synced"]
}

// watch starts informers for gvrs in the object namespace and feeds the tracker.
func watch(ctx context.Context, dyn dynamic.Interface, tr *tracker, gvrs map[schema.GroupVersionResource]string) error {
	factory := dynamicinformer.NewFilteredDynamicSharedInformerFactory(dyn, 0, objectNamespace, nil)
	for gvr, kindName := range gvrs {
		key := func(obj any) (string, *unstructured.Unstructured) {
			if tomb, ok := obj.(cache.DeletedFinalStateUnknown); ok {
				obj = tomb.Obj
			}
			u, _ := obj.(*unstructured.Unstructured)
			if u == nil {
				return "", nil
			}
			return kindName + "/" + u.GetName(), u
		}
		_, err := factory.ForResource(gvr).Informer().AddEventHandler(cache.ResourceEventHandlerFuncs{
			AddFunc: func(obj any) {
				if k, u := key(obj); u != nil {
					tr.observe(k, healthy(u), time.Now())
				}
			},
			UpdateFunc: func(_, obj any) {
				if k, u := key(obj); u != nil {
					tr.observe(k, healthy(u), time.Now())
				}
			},
			DeleteFunc: func(obj any) {
				if k, u := key(obj); u != nil {
					tr.remove(k, time.Now())
				}
			},
		})
		if err != nil {
			return err
		}
	}
	factory.Start(ctx.Done())
	for gvr, synced := range factory.WaitForCacheSync(ctx.Done()) {
		if !synced {
			return fmt.Errorf("informer for %s did not sync", gvr)
		}
	}
	return nil
}
