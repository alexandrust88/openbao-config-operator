package engine

import (
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"k8s.io/apimachinery/pkg/types"
)

// Default drift intervals of the two tiers.
const (
	DefaultDriftIntervalCritical = 15 * time.Minute
	DefaultDriftIntervalStandard = time.Hour
)

var (
	driftLag = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "obco_drift_lag_seconds",
		Help: "How far the last drift read of a connection ran behind its schedule.",
	}, []string{"connection"})
	driftRepairs = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "obco_drift_repairs_total",
		Help: "Writes that repaired an out-of-band change, by kind.",
	}, []string{labelKind})
	conflictsTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "obco_conflicts_total",
		Help: "Reconciles refused because another resource owns the OpenBao path or object, by kind.",
	}, []string{labelKind})
	resourceReady = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "obco_resource_ready",
		Help: "1 when a managed resource is Ready, else 0, labelled with the reason of its Ready condition.",
	}, []string{labelKind, "namespace", "name", "reason"})
	budgetExhausted = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "obco_write_budget_exhausted_total",
		Help: "Writes held back because the server's write budget was spent, by server.",
	}, []string{"server"})
)

// MinDriftInterval is the shortest spec.driftInterval the engine honours, so a resource cannot turn drift detection
// into a hot read loop against a shared server.
const MinDriftInterval = 30 * time.Second

// driftInterval returns spec.driftInterval (at least MinDriftInterval), else the interval of the adapter's tier.
func (r *Reconciler[T]) driftInterval(obj T) time.Duration {
	if d := obj.GetResourceSpec().DriftInterval; d != nil && d.Duration > 0 {
		return max(d.Duration, MinDriftInterval)
	}
	if tier, ok := any(r.adapter).(DriftTier); ok && tier.DriftTier() == TierAccessCritical {
		return r.opts.DriftIntervalCritical
	}
	return r.opts.DriftIntervalStandard
}

// readiness keeps obco_resource_ready at one series per resource, deleting the series of a previous reason.
type readiness struct {
	mu     sync.Mutex
	reason map[types.NamespacedName]string
}

func (r *readiness) set(kind string, key types.NamespacedName, ready bool, reason string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.reason == nil {
		r.reason = map[types.NamespacedName]string{}
	}
	if previous, ok := r.reason[key]; ok && previous != reason {
		resourceReady.DeleteLabelValues(kind, key.Namespace, key.Name, previous)
	}
	r.reason[key] = reason
	value := 0.0
	if ready {
		value = 1
	}
	resourceReady.WithLabelValues(kind, key.Namespace, key.Name, reason).Set(value)
}

func (r *readiness) forget(kind string, key types.NamespacedName) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if previous, ok := r.reason[key]; ok {
		resourceReady.DeleteLabelValues(kind, key.Namespace, key.Name, previous)
		delete(r.reason, key)
	}
}

// schedule remembers when each resource's next drift check is due, to measure obco_drift_lag_seconds.
type schedule struct {
	mu  sync.Mutex
	due map[types.NamespacedName]time.Time
}

func (s *schedule) set(key types.NamespacedName, at time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.due == nil {
		s.due = map[types.NamespacedName]time.Time{}
	}
	s.due[key] = at
}

func (s *schedule) forget(key types.NamespacedName) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.due, key)
}

// lag returns how long after its due time the drift check of key ran; ok=false when none was scheduled.
func (s *schedule) lag(key types.NamespacedName, now time.Time) (time.Duration, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	at, ok := s.due[key]
	if !ok {
		return 0, false
	}
	return max(now.Sub(at), 0), true
}
