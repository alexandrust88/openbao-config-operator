package engine

import (
	"context"
	"math/rand/v2"
	"sync"
	"time"

	"golang.org/x/time/rate"
	"k8s.io/apimachinery/pkg/api/meta"

	commonv1alpha1 "github.com/alexandrust88/openbao-config-operator/api/common/v1alpha1"
)

// DefaultWriteBudgetPerMinute caps the rewrites per minute to one server when Options.WriteBudgetPerMinute is zero.
const DefaultWriteBudgetPerMinute = 60

const budgetWindow = time.Minute

// Each server takes the first passes of its resources after a connection outage at recoveryPerSecond, so the
// backlog that the connection watch re-queues at once reaches the restarted server as a ramp.
const (
	recoveryPerSecond = 100
	recoveryBurst     = 10
)

// writeBudget is a sliding-window circuit breaker on rewrites per server, so a change that touches every object
// (an operator upgrade that changes a declared default) cannot rewrite thousands of objects at once.
type writeBudget struct {
	mu        sync.Mutex
	perMinute int
	writes    map[string][]time.Time
	now       func() time.Time
}

// newWriteBudget returns a budget of perMinute writes per server; zero means the default, negative no limit.
func newWriteBudget(perMinute int) *writeBudget {
	if perMinute == 0 {
		perMinute = DefaultWriteBudgetPerMinute
	}
	return &writeBudget{perMinute: perMinute, writes: map[string][]time.Time{}, now: time.Now}
}

// take spends one write for server; when the window is full it returns false and the wait until a write frees.
func (b *writeBudget) take(server string) (bool, time.Duration) {
	if b.perMinute < 0 {
		return true, 0
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	now := b.now()
	recent := b.writes[server]
	first := 0
	for first < len(recent) && now.Sub(recent[first]) >= budgetWindow {
		first++
	}
	recent = recent[first:]
	if len(recent) >= b.perMinute {
		b.writes[server] = recent
		return false, recent[0].Add(budgetWindow).Sub(now)
	}
	b.writes[server] = append(recent, now)
	return true, 0
}

// refund gives back the latest write taken for server, for a write that sent nothing.
func (b *writeBudget) refund(server string) {
	if b.perMinute < 0 {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if recent := b.writes[server]; len(recent) > 0 {
		b.writes[server] = recent[:len(recent)-1]
	}
}

// rewrite reports a write that spends the budget: an update of an object this resource acquired earlier whose spec
// and write-only inputs did not change since its conditions were last set (a drift repair, a changed default).
// Creates, adoptions and writes after a spec change or a changed input Secret (inputsChanged) are user-intended and
// never wait for the budget. Without a Ready condition to compare with, the write counts as a rewrite. A write of a
// kind that regenerates one-time outputs (a new secret ID while the last one is undelivered) counts even before
// the object is acquired.
func rewrite(obj Object, found, inputsChanged, regenerates bool) bool {
	status := obj.GetResourceStatus()
	if !found || inputsChanged || status.Acquired == "" && !regenerates {
		return false
	}
	ready := meta.FindStatusCondition(status.Conditions, commonv1alpha1.ConditionReady)
	return ready == nil || ready.ObservedGeneration == obj.GetGeneration()
}

// budgetRetry spreads the retries of held-back writes over a whole window after the first slot frees, so that the
// waiting objects do not all wake together when the oldest write leaves the window.
func budgetRetry(wait time.Duration) time.Duration {
	return max(wait, time.Second) + time.Duration(rand.Int64N(int64(budgetWindow)))
}

// recoveryRamp holds one token bucket per server for the passes that follow ConnectionNotReady.
type recoveryRamp struct {
	mu      sync.Mutex
	servers map[string]*rate.Limiter
}

// wait blocks until server may take one more recovering pass, or ctx ends.
func (r *recoveryRamp) wait(ctx context.Context, server string) error {
	r.mu.Lock()
	if r.servers == nil {
		r.servers = map[string]*rate.Limiter{}
	}
	limiter, ok := r.servers[server]
	if !ok {
		limiter = rate.NewLimiter(recoveryPerSecond, recoveryBurst)
		r.servers[server] = limiter
	}
	r.mu.Unlock()
	return limiter.Wait(ctx)
}
