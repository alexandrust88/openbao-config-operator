package openbao

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/openbao/openbao/api/v2"
	"golang.org/x/sync/singleflight"
)

// minReloginInterval stops a genuine 403 (a policy denial, not a dead token) from triggering a fresh login on
// every request.
const minReloginInterval = 5 * time.Second

// revokeTimeout bounds the best-effort revocation of a retired session's token.
const revokeTimeout = 5 * time.Second

// sharedLoginTimeout bounds a login that several callers wait for; it runs detached from each caller's context, so
// one cancelled reconcile does not fail the others.
const sharedLoginTimeout = time.Minute

// sessionIDs numbers sessions, so a client's Identity is never reused by a later session.
var sessionIDs atomic.Uint64

// errLoginInvalidated is returned to the callers of a login that Invalidate overtook.
var errLoginInvalidated = errors.New("connection invalidated during login, retry")

// Pool caches one authenticated client per connection. Tokens are refreshed lazily on the request path, so apart
// from a login in flight the pool runs no goroutines, and it needs no shutdown.
type Pool struct {
	mu       sync.Mutex
	entries  map[string]*poolEntry
	now      func() time.Time
	limiters limiters
	// logins lets concurrent cache misses of one connection and fingerprint share a single login.
	logins singleflight.Group
	// inflight tracks the running logins per key, so one that Invalidate overtook is retired instead of cached.
	inflight map[string]*inflightLogins
}

type inflightLogins struct {
	count         int
	invalidations uint64
}

type poolEntry struct {
	fingerprint string
	s           *session
}

// NewPool returns an empty Pool.
func NewPool() *Pool {
	return &Pool{entries: map[string]*poolEntry{}, inflight: map[string]*inflightLogins{}, now: time.Now}
}

// Client returns the cached client for key, or builds one and logs in. key identifies the connection, for
// example its UID; it is also the "connection" metrics label. A change of address, namespace, CA bundle,
// timeout or Auth.CacheKey replaces the cached client.
func (p *Pool) Client(ctx context.Context, key string, cfg Config) (Client, error) {
	if cfg.Auth == nil {
		return nil, errors.New("auth is not configured")
	}
	fp := fingerprint(cfg)
	limit := p.limiters.forServer(cfg.Address, cfg.RateLimit)

	if s := p.cached(key, fp); s != nil {
		return s.client(), nil
	}
	logins := p.logins.DoChan(key+"\x00"+fp, func() (any, error) {
		if s := p.cached(key, fp); s != nil {
			return s, nil
		}
		since := p.beginLogin(key)
		s, err := loggedInSession(ctx, cfg, key, p.now, limit)
		p.mu.Lock()
		defer p.mu.Unlock()
		invalidated := p.endLoginLocked(key, since)
		if err != nil {
			return nil, err
		}
		if invalidated {
			s.retire()
			return nil, errLoginInvalidated
		}
		if cur, ok := p.entries[key]; ok {
			cur.s.retire()
		}
		p.entries[key] = &poolEntry{fingerprint: fp, s: s}
		return s, nil
	})
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case res := <-logins:
		if res.Err != nil {
			return nil, res.Err
		}
		return res.Val.(*session).client(), nil
	}
}

// loggedInSession builds a session and logs it in, detached from the caller's cancellation.
func loggedInSession(ctx context.Context, cfg Config, key string, now func() time.Time, limit *serverLimiter) (*session, error) {
	s, err := newSession(cfg, key, now, limit)
	if err != nil {
		return nil, err
	}
	loginCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), sharedLoginTimeout)
	defer cancel()
	if _, err := s.token(loginCtx); err != nil {
		s.close()
		return nil, err
	}
	return s, nil
}

// beginLogin records a login of key and returns the invalidation count it started at.
func (p *Pool) beginLogin(key string) uint64 {
	p.mu.Lock()
	defer p.mu.Unlock()
	in := p.inflight[key]
	if in == nil {
		in = &inflightLogins{}
		p.inflight[key] = in
	}
	in.count++
	return in.invalidations
}

// endLoginLocked records the end of a login of key begun at invalidation count since and reports whether key was
// invalidated meanwhile. p.mu must be held.
func (p *Pool) endLoginLocked(key string, since uint64) bool {
	in := p.inflight[key]
	in.count--
	if in.count == 0 {
		delete(p.inflight, key)
	}
	return in.invalidations != since
}

func (p *Pool) cached(key, fp string) *session {
	p.mu.Lock()
	defer p.mu.Unlock()
	if e, ok := p.entries[key]; ok && e.fingerprint == fp {
		return e.s
	}
	return nil
}

// Invalidate drops the cached client for key and its metric series, and revokes the token it logged in for. A
// client already handed out logs in again on its next request.
func (p *Pool) Invalidate(key string) {
	p.mu.Lock()
	e, ok := p.entries[key]
	delete(p.entries, key)
	if in := p.inflight[key]; in != nil {
		in.invalidations++
	}
	p.mu.Unlock()
	if ok {
		e.s.retire()
	}
	forgetConnectionMetrics(key)
}

func fingerprint(cfg Config) string {
	h := sha256.New()
	for _, part := range [][]byte{
		[]byte(cfg.Address),
		[]byte(cfg.Namespace),
		[]byte(cfg.AuthNamespace),
		cfg.CABundle,
		[]byte(strconv.FormatInt(int64(cfg.Timeout), 10)),
		[]byte(cfg.Auth.CacheKey()),
	} {
		h.Write(part)
		h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil))
}

type session struct {
	id         uint64
	base       *api.Client
	httpc      *http.Client
	ns         string
	authNS     string
	auth       Auth
	now        func() time.Time
	connection string
	limit      *serverLimiter

	// deriveMu serialises api.Client.WithNamespace, which copies base without holding its lock.
	deriveMu sync.Mutex
	// loginSem serialises login and renewal I/O; unlike a mutex it can be abandoned when ctx ends.
	loginSem chan struct{}

	mu  sync.Mutex
	tok tokenState

	idMu        sync.Mutex
	clusterID   string
	clusterIDAt time.Time
}

type tokenState struct {
	token      string
	loginTTL   time.Duration
	renewable  bool
	refreshAt  time.Time
	loggedInAt time.Time
	// issued reports a token that a login request issued, as opposed to a static token that was only looked up.
	issued bool
}

func newSession(cfg Config, connection string, now func() time.Time, limit *serverLimiter) (*session, error) {
	base, httpc, err := newAPIClient(cfg, connection)
	if err != nil {
		return nil, err
	}
	httpc.Transport = &limitedTransport{next: httpc.Transport, limit: limit}
	return &session{
		id:         sessionIDs.Add(1),
		base:       base,
		httpc:      httpc,
		ns:         joinNamespace("", cfg.Namespace),
		authNS:     joinNamespace("", cfg.AuthNamespace),
		auth:       cfg.Auth,
		now:        now,
		connection: connection,
		limit:      limit,
		loginSem:   make(chan struct{}, 1),
	}, nil
}

func (s *session) client() Client {
	return &client{s: s, ns: s.ns}
}

func (s *session) close() {
	s.httpc.CloseIdleConnections()
}

// retire closes the session and revokes, in the background and best effort, the token it logged in for, so a
// dropped or replaced connection leaves no live token behind. A static token (TokenAuth), which no login issued,
// belongs to whoever issued it and is kept.
func (s *session) retire() {
	st := s.state()
	token := st.token
	if !st.issued || token == "" {
		s.close()
		return
	}
	go func() {
		defer s.close()
		ctx, cancel := context.WithTimeout(context.Background(), revokeTimeout)
		defer cancel()
		_ = s.derive(s.authNS, token).Auth().Token().RevokeSelfWithContext(WithPriority(withoutMetrics(ctx)), "")
	}()
}

func (s *session) derive(ns, token string) *api.Client {
	s.deriveMu.Lock()
	ac := s.base.WithNamespace(ns)
	s.deriveMu.Unlock()
	ac.SetToken(token)
	return ac
}

// do runs fn with a valid token. A 401 means the token is gone: it logs in once more and retries. A 403 is
// usually a policy denial, so it logs in again only when lookup-self shows the token itself is no longer valid;
// otherwise the error wraps ErrPermissionDenied.
func (s *session) do(ctx context.Context, ns string, fn func(*api.Client) error) error {
	call := func(ac *api.Client) error {
		if value := attribution(ctx); value != "" {
			ac.AddHeader(AttributionHeader, value)
		}
		return convertErr(fn(ac))
	}
	token, err := s.token(ctx)
	if err != nil {
		return err
	}
	err = call(s.derive(ns, token))
	switch statusCode(err) {
	case http.StatusUnauthorized:
	case http.StatusForbidden:
		if s.tokenValid(ctx, token) {
			return permissionDenied(err)
		}
	default:
		return err
	}
	fresh, retry, loginErr := s.relogin(ctx, token)
	if loginErr != nil {
		return errors.Join(err, loginErr)
	}
	if !retry {
		return permissionDenied(err)
	}
	return permissionDenied(call(s.derive(ns, fresh)))
}

// tokenValid reports whether lookup-self still accepts token.
func (s *session) tokenValid(ctx context.Context, token string) bool {
	_, _, err := loginClient{s: s}.lookupSelf(ctx, token)
	return err == nil
}

func permissionDenied(err error) error {
	if statusCode(err) == http.StatusForbidden {
		return fmt.Errorf("%w: %w", ErrPermissionDenied, err)
	}
	return err
}

func (s *session) acquire(ctx context.Context) error {
	select {
	case s.loginSem <- struct{}{}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (s *session) release() {
	<-s.loginSem
}

func (s *session) state() tokenState {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.tok
}

func (s *session) fresh(st tokenState) bool {
	return st.token != "" && (st.refreshAt.IsZero() || s.now().Before(st.refreshAt))
}

func (s *session) token(ctx context.Context) (string, error) {
	if st := s.state(); s.fresh(st) {
		return st.token, nil
	}
	if err := s.acquire(ctx); err != nil {
		return "", err
	}
	defer s.release()
	st := s.state()
	if s.fresh(st) {
		return st.token, nil
	}
	if st.token != "" && st.renewable && s.renew(ctx, st) {
		return st.token, nil
	}
	return s.login(ctx)
}

// relogin forces a login unless another request already replaced the stale token. It reports whether the
// caller should retry.
func (s *session) relogin(ctx context.Context, stale string) (string, bool, error) {
	if err := s.acquire(ctx); err != nil {
		return "", false, err
	}
	defer s.release()
	st := s.state()
	if st.token != stale {
		return st.token, st.token != "", nil
	}
	if s.now().Sub(st.loggedInAt) < minReloginInterval {
		return "", false, nil
	}
	token, err := s.login(ctx)
	if err != nil {
		return "", false, err
	}
	return token, true, nil
}

// renew extends the token. It reports false when renewal failed or the max TTL caps the new lease below a
// third of the login TTL, so the caller logs in again.
func (s *session) renew(ctx context.Context, st tokenState) bool {
	secret, err := s.derive(s.authNS, st.token).Auth().Token().RenewSelfWithContext(WithPriority(ctx),
		int(st.loginTTL.Seconds()))
	if err != nil || secret == nil || secret.Auth == nil {
		tokenRenewalsTotal.WithLabelValues(s.connection, resultFailure).Inc()
		return false
	}
	tokenRenewalsTotal.WithLabelValues(s.connection, resultSuccess).Inc()
	ttl := time.Duration(secret.Auth.LeaseDuration) * time.Second
	if ttl < st.loginTTL/3 {
		return false
	}
	st.renewable = secret.Auth.Renewable
	st.refreshAt = refreshAt(s.now(), ttl, st.renewable)
	s.mu.Lock()
	s.tok = st
	s.mu.Unlock()
	return true
}

func (s *session) login(ctx context.Context) (string, error) {
	var issued bool
	token, ttl, renewable, err := s.auth.Login(ctx, loginClient{s: s, wrote: &issued})
	if err == nil && token == "" {
		err = errors.New("empty token")
	}
	if err != nil {
		loginsTotal.WithLabelValues(s.connection, resultFailure).Inc()
		return "", fmt.Errorf("login: %w", err)
	}
	loginsTotal.WithLabelValues(s.connection, resultSuccess).Inc()
	now := s.now()
	s.mu.Lock()
	s.tok = tokenState{
		token:      token,
		loginTTL:   ttl,
		renewable:  renewable,
		refreshAt:  refreshAt(now, ttl, renewable),
		loggedInAt: now,
		issued:     issued,
	}
	s.mu.Unlock()
	return token, nil
}

// refreshAt renews a renewable token after a third of its TTL and replaces a non-renewable one after two
// thirds. A zero TTL never expires.
func refreshAt(now time.Time, ttl time.Duration, renewable bool) time.Time {
	switch {
	case ttl <= 0:
		return time.Time{}
	case renewable:
		return now.Add(ttl / 3)
	default:
		return now.Add(ttl * 2 / 3)
	}
}

// loginClient is the unauthenticated LoginClient. Write returns the auth block (client_token, accessor,
// lease_duration, renewable, policies) for a login response and the response data otherwise.
type loginClient struct {
	s *session
	// wrote, when set, records that a login request was sent.
	wrote *bool
}

func (l loginClient) Write(ctx context.Context, path string, data map[string]any) (map[string]any, error) {
	if l.wrote != nil {
		*l.wrote = true
	}
	secret, err := l.s.derive(l.s.authNS, "").Logical().WriteWithContext(WithPriority(ctx), path, data)
	if err != nil {
		return nil, wrapErr("write", path, convertErr(err))
	}
	if secret == nil {
		return nil, nil
	}
	if a := secret.Auth; a != nil {
		return map[string]any{
			"client_token":   a.ClientToken,
			"accessor":       a.Accessor,
			"lease_duration": a.LeaseDuration,
			"renewable":      a.Renewable,
			"policies":       a.Policies,
		}, nil
	}
	return secret.Data, nil
}

func (l loginClient) lookupSelf(ctx context.Context, token string) (time.Duration, bool, error) {
	secret, err := l.s.derive(l.s.authNS, token).Auth().Token().LookupSelfWithContext(WithPriority(ctx))
	if err != nil {
		return 0, false, convertErr(err)
	}
	ttl, err := secret.TokenTTL()
	if err != nil {
		return 0, false, err
	}
	renewable, err := secret.TokenIsRenewable()
	if err != nil {
		return 0, false, err
	}
	return ttl, renewable, nil
}
