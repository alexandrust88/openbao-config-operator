package openbao

import (
	"context"
	"net/http"
	"strings"
	"sync"

	"golang.org/x/time/rate"
)

// driftShare is the part of a server's rate limit that requests without WithPriority may use, so drift reads
// never starve reconciles of spec changes.
const driftShare = 0.8

type priorityKey struct{}

type attributionKey struct{}

// AttributionHeader carries the managed resource behind a request, so an OpenBao audit device that records the
// header (sys/config/auditing/request-headers, hmac=false) ties each write to its custom resource.
const AttributionHeader = "X-Openbao-Config-Resource"

const maxAttributionLength = 256

// WithResourceAttribution makes every request sent with ctx carry AttributionHeader set to value, reduced to
// printable ASCII and at most 256 characters.
func WithResourceAttribution(ctx context.Context, value string) context.Context {
	clean := strings.Map(func(r rune) rune {
		if r < 0x20 || r > 0x7e {
			return -1
		}
		return r
	}, value)
	if len(clean) > maxAttributionLength {
		clean = clean[:maxAttributionLength]
	}
	return context.WithValue(ctx, attributionKey{}, clean)
}

func attribution(ctx context.Context) string {
	value, _ := ctx.Value(attributionKey{}).(string)
	return value
}

// WithPriority marks the requests made with ctx as spec-change work: they may use the whole rate limit of the
// server, while other requests (drift checks) are held to driftShare of it.
func WithPriority(ctx context.Context) context.Context {
	return context.WithValue(ctx, priorityKey{}, true)
}

func hasPriority(ctx context.Context) bool {
	priority, _ := ctx.Value(priorityKey{}).(bool)
	return priority
}

// serverLimiter is one token bucket shared by every client of a server, plus a smaller bucket for drift reads.
// Nil buckets mean no limit.
type serverLimiter struct {
	mu         sync.Mutex
	cfg        RateLimit
	all, drift *rate.Limiter
}

func newServerLimiter() *serverLimiter {
	return &serverLimiter{}
}

// set replaces the buckets, full, when the limit changes.
func (l *serverLimiter) set(rl RateLimit) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if rl == l.cfg {
		return
	}
	l.cfg = rl
	if rl.QPS <= 0 {
		l.all, l.drift = nil, nil
		return
	}
	burst := max(rl.Burst, 1)
	l.all = rate.NewLimiter(rate.Limit(rl.QPS), burst)
	l.drift = rate.NewLimiter(rate.Limit(rl.QPS*driftShare), max(int(float64(burst)*driftShare), 1))
}

func (l *serverLimiter) wait(ctx context.Context) error {
	l.mu.Lock()
	all, drift := l.all, l.drift
	l.mu.Unlock()
	if all == nil {
		return nil
	}
	if !hasPriority(ctx) {
		if err := drift.Wait(ctx); err != nil {
			return err
		}
	}
	return all.Wait(ctx)
}

// limitedTransport takes a token from the server's limiter for every HTTP attempt, so the SDK's retries, logins and
// token renewals count against the limit too. Login and token traffic carries WithPriority.
type limitedTransport struct {
	next  http.RoundTripper
	limit *serverLimiter
}

func (t *limitedTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if err := t.limit.wait(req.Context()); err != nil {
		return nil, err
	}
	return t.next.RoundTrip(req)
}

// CloseIdleConnections lets http.Client.CloseIdleConnections reach the wrapped transport.
func (t *limitedTransport) CloseIdleConnections() { closeIdle(t.next) }

func closeIdle(rt http.RoundTripper) {
	if c, ok := rt.(interface{ CloseIdleConnections() }); ok {
		c.CloseIdleConnections()
	}
}

// limiters holds one serverLimiter per server address.
type limiters struct {
	mu      sync.Mutex
	servers map[string]*serverLimiter
}

// forServer returns the limiter of address and applies rl to it; the last connection to set a limit wins.
func (ls *limiters) forServer(address string, rl RateLimit) *serverLimiter {
	key := address
	if normalized, err := normalizeAddress(address); err == nil {
		key = strings.ToLower(normalized)
	}
	ls.mu.Lock()
	defer ls.mu.Unlock()
	if ls.servers == nil {
		ls.servers = map[string]*serverLimiter{}
	}
	l, ok := ls.servers[key]
	if !ok {
		l = newServerLimiter()
		ls.servers[key] = l
	}
	l.set(rl)
	return l
}
