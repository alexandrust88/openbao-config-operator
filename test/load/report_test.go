//go:build load

package load

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"math"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	eventsv1 "k8s.io/api/events/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// dist summarises a set of values.
type dist struct {
	Count  int     `json:"count"`
	Min    float64 `json:"min"`
	P50    float64 `json:"p50"`
	P95    float64 `json:"p95"`
	P99    float64 `json:"p99"`
	Max    float64 `json:"max"`
	Mean   float64 `json:"mean"`
	Stddev float64 `json:"stddev"`
}

func summarize(values []float64) dist {
	if len(values) == 0 {
		return dist{}
	}
	sorted := slices.Clone(values)
	slices.Sort(sorted)
	sum := 0.0
	for _, v := range sorted {
		sum += v
	}
	mean := sum / float64(len(sorted))
	variance := 0.0
	for _, v := range sorted {
		variance += (v - mean) * (v - mean)
	}
	at := func(q float64) float64 { return sorted[int(math.Ceil(q*float64(len(sorted))))-1] }
	return dist{
		Count: len(sorted), Min: sorted[0], P50: at(0.5), P95: at(0.95), P99: at(0.99), Max: sorted[len(sorted)-1],
		Mean: mean, Stddev: math.Sqrt(variance / float64(len(sorted))),
	}
}

func seconds(d time.Duration) float64 { return math.Round(d.Seconds()*1000) / 1000 }

// window aggregates the manager metrics and process stats between two instants.
type window struct {
	Seconds             float64            `json:"seconds"`
	OpenBaoRequests     float64            `json:"openbaoRequests"`
	OpenBaoByMethod     map[string]float64 `json:"openbaoByMethod"`
	OpenBaoErrors       float64            `json:"openbaoTransportErrors"`
	OpenBaoPerSecond    float64            `json:"openbaoRequestsPerSecond"`
	OpenBaoLatencyMs    [3]float64         `json:"openbaoLatencyP50P95P99Ms"`
	Logins              float64            `json:"logins"`
	Writes              float64            `json:"writes"`
	WritesByKind        map[string]float64 `json:"writesByKind,omitempty"`
	Reconciles          float64            `json:"reconciles"`
	ReconcilesByResult  map[string]float64 `json:"reconcilesByResult"`
	ReconcileErrors     float64            `json:"reconcileErrors"`
	ReconcileLatencyMs  [3]float64         `json:"reconcileLatencyP50P95P99Ms"`
	KubeRequests        float64            `json:"kubeRequests"`
	KubeByMethod        map[string]float64 `json:"kubeByMethod"`
	WorkqueueRetries    float64            `json:"workqueueRetries"`
	PeakWorkqueueDepth  float64            `json:"peakWorkqueueDepth"`
	PeakRSSMiB          float64            `json:"peakRssMiB"`
	EndRSSMiB           float64            `json:"endRssMiB"`
	CPUSeconds          float64            `json:"cpuSeconds"`
	MeanCPUPercent      float64            `json:"meanCpuPercent"`
	PeakGoroutines      float64            `json:"peakGoroutines"`
	EndGoroutines       float64            `json:"endGoroutines"`
	PeakHeapInuseMiB    float64            `json:"peakHeapInuseMiB"`
	ManagerErrorLogLine int                `json:"managerErrorLogLines"`
}

func newWindow(samples []sample, errorLines int) window {
	w := window{ManagerErrorLogLine: errorLines}
	if len(samples) < 2 {
		return w
	}
	first, last := samples[0], samples[len(samples)-1]
	d := delta(first.metrics, last.metrics)
	w.Seconds = seconds(last.at.Sub(first.at))
	w.OpenBaoRequests = d["openbao_requests_total"]
	w.OpenBaoByMethod = labelled(d, "openbao_requests_total:method=")
	w.OpenBaoErrors = d["openbao_requests_total:code=error"]
	if w.Seconds > 0 {
		w.OpenBaoPerSecond = round(w.OpenBaoRequests / w.Seconds)
	}
	for i, q := range []float64{0.5, 0.95, 0.99} {
		w.OpenBaoLatencyMs[i] = round(d.quantile("openbao_request_duration_seconds_bucket", q) * 1000)
		w.ReconcileLatencyMs[i] = round(d.quantile("controller_runtime_reconcile_time_seconds_bucket", q) * 1000)
	}
	w.Logins = d["openbao_logins_total"]
	w.Writes = d["openbao_writes_total"]
	w.WritesByKind = labelled(d, "openbao_writes_total:kind=")
	w.Reconciles = d["controller_runtime_reconcile_total"]
	w.ReconcilesByResult = labelled(d, "controller_runtime_reconcile_total:result=")
	w.ReconcileErrors = d["controller_runtime_reconcile_errors_total"]
	w.KubeRequests = d["rest_client_requests_total"]
	w.KubeByMethod = labelled(d, "rest_client_requests_total:method=")
	w.WorkqueueRetries = d["workqueue_retries_total"]
	w.CPUSeconds = round(last.cpuSec - first.cpuSec)
	if w.Seconds > 0 {
		w.MeanCPUPercent = round(100 * w.CPUSeconds / w.Seconds)
	}
	for _, s := range samples {
		w.PeakWorkqueueDepth = max(w.PeakWorkqueueDepth, s.metrics["workqueue_depth"])
		w.PeakRSSMiB = max(w.PeakRSSMiB, round(s.rssKB/1024))
		w.PeakGoroutines = max(w.PeakGoroutines, s.metrics["go_goroutines"])
		w.PeakHeapInuseMiB = max(w.PeakHeapInuseMiB, round(s.metrics["go_memstats_heap_inuse_bytes"]/(1<<20)))
	}
	w.EndRSSMiB = round(last.rssKB / 1024)
	w.EndGoroutines = last.metrics["go_goroutines"]
	return w
}

func labelled(s snapshot, prefix string) map[string]float64 {
	out := map[string]float64{}
	for k, v := range s {
		if label, ok := strings.CutPrefix(k, prefix); ok && v != 0 {
			out[label] = v
		}
	}
	return out
}

func round(v float64) float64 { return math.Round(v*100) / 100 }

// rates returns the per-second increase of metric between consecutive bucket boundaries of samples.
func rates(samples []sample, metric string, bucket time.Duration) []float64 {
	if len(samples) == 0 {
		return nil
	}
	var out []float64
	prev := samples[0]
	for _, s := range samples[1:] {
		if s.at.Sub(prev.at) < bucket {
			continue
		}
		out = append(out, round((s.metrics[metric]-prev.metrics[metric])/s.at.Sub(prev.at).Seconds()))
		prev = s
	}
	return out
}

// countErrorLines counts the manager log lines at error level so far.
func countErrorLines(path string) int {
	f, err := os.Open(path)
	if err != nil {
		return 0
	}
	defer func() { _ = f.Close() }()
	n := 0
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	for sc.Scan() {
		if strings.Contains(sc.Text(), `"level":"error"`) {
			n++
		}
	}
	return n
}

func writeJSON(path string, v any) error {
	raw, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(raw, '\n'), 0o600)
}

// writeTimeseries dumps every sample as CSV, relative to start.
func writeTimeseries(path string, start time.Time, samples []sample) error {
	var b strings.Builder
	b.WriteString("t_s,rss_mib,cpu_s,goroutines,heap_inuse_mib,openbao_requests,openbao_errors,writes," +
		"reconciles,reconcile_errors,workqueue_depth,workqueue_retries,kube_requests\n")
	for _, s := range samples {
		m := s.metrics
		fmt.Fprintf(&b, "%.1f,%.1f,%.2f,%.0f,%.1f,%.0f,%.0f,%.0f,%.0f,%.0f,%.0f,%.0f,%.0f\n",
			s.at.Sub(start).Seconds(), s.rssKB/1024, s.cpuSec, m["go_goroutines"],
			m["go_memstats_heap_inuse_bytes"]/(1<<20), m["openbao_requests_total"],
			m["openbao_requests_total:code=error"], m["openbao_writes_total"], m["controller_runtime_reconcile_total"],
			m["controller_runtime_reconcile_errors_total"], m["workqueue_depth"], m["workqueue_retries_total"],
			m["rest_client_requests_total"])
	}
	return os.WriteFile(path, []byte(b.String()), 0o600)
}

// writeObjects dumps per-object timings in milliseconds.
func writeObjects(path string, tr *tracker, objects []object) error {
	var b strings.Builder
	b.WriteString("object,ready_ms,finalize_ms\n")
	ms := func(from, to time.Time) string {
		if from.IsZero() || to.IsZero() {
			return ""
		}
		return fmt.Sprintf("%d", to.Sub(from).Milliseconds())
	}
	for _, o := range objects {
		st := tr.get(o.key())
		fmt.Fprintf(&b, "%s,%s,%s\n", o.key(), ms(st.created, st.firstHealthy), ms(st.deleteRequested, st.gone))
	}
	return os.WriteFile(filepath.Clean(path), []byte(b.String()), 0o600)
}

// writeEvents dumps the operator's events in the object namespace, counted by reason and note with the object
// name removed, so repeated writes show which fields the engine found different.
func writeEvents(ctx context.Context, k8s client.Client, path string) error {
	var list eventsv1.EventList
	if err := k8s.List(ctx, &list, client.InNamespace(objectNamespace)); err != nil {
		return err
	}
	counts := map[string]int{}
	for _, e := range list.Items {
		n := 1
		if e.Series != nil {
			n = int(e.Series.Count)
		}
		note := strings.ReplaceAll(e.Note, e.Regarding.Name, "<name>")
		counts[e.Regarding.Kind+" "+e.Reason+": "+note] += n
	}
	keys := slices.Sorted(maps.Keys(counts))
	var b strings.Builder
	for _, k := range keys {
		fmt.Fprintf(&b, "%6d %s\n", counts[k], k)
	}
	return os.WriteFile(path, []byte(b.String()), 0o600)
}
