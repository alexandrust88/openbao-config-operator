package sys

import (
	"context"
	"strconv"
	"sync"
	"time"

	"golang.org/x/sync/singleflight"

	"github.com/alexandrust88/openbao-config-operator/internal/openbao"
)

// mountTableTTL is how long one read of a mount table serves the mount observes of the same client. It covers the
// burst of reconciles that start-up, a resync or a requeue wave sends to the mounts of one namespace and stays far
// below engine.MinDriftInterval, so an out-of-band change is seen at most this much later.
const mountTableTTL = 2 * time.Second

// mountTableReadTimeout bounds a shared read, which runs detached from the context of the caller that started it.
const mountTableReadTimeout = time.Minute

// mountTableAttribution names a shared table read in the audit log in place of the resource that happened to start it.
const mountTableAttribution = "mount-table"

// identified is implemented by clients that name the session and namespace they send requests as.
type identified interface {
	Identity() string
}

type mountTableSnapshot struct {
	mounts map[string]any
	readAt time.Time
}

// mountTableCache shares one read of sys/auth or sys/mounts per client identity among the mount observes of a short
// window. A mount write or delete bumps the generation, which drops every snapshot and keeps a read that was in
// flight during the write from being stored or joined.
type mountTableCache struct {
	ttl    time.Duration
	now    func() time.Time
	flight singleflight.Group

	mu         sync.Mutex
	generation uint64
	snapshots  map[string]mountTableSnapshot
}

var mountTables = newMountTableCache()

func newMountTableCache() *mountTableCache {
	return &mountTableCache{ttl: mountTableTTL, now: time.Now, snapshots: map[string]mountTableSnapshot{}}
}

// read returns sys/<table>. The result is shared: callers must not modify it.
func (m *mountTableCache) read(ctx context.Context, c openbao.Client, table string) (map[string]any, error) {
	id, ok := c.(identified)
	if !ok {
		return c.Read(ctx, "sys/"+table)
	}
	key := id.Identity() + "\x00" + table
	m.mu.Lock()
	snapshot, cached := m.snapshots[key]
	generation := m.generation
	fresh := cached && m.now().Sub(snapshot.readAt) < m.ttl
	m.mu.Unlock()
	if fresh {
		return snapshot.mounts, nil
	}
	// Callers join only a read of their own rate-limit class; the read is attributed to no single resource.
	flightKey := key + "\x00" + strconv.FormatUint(generation, 10) + "\x00" + strconv.FormatBool(openbao.HasPriority(ctx))
	shared := m.flight.DoChan(flightKey, func() (any, error) {
		readAt := m.now()
		detached := openbao.WithResourceAttribution(context.WithoutCancel(ctx), mountTableAttribution)
		readCtx, cancel := context.WithTimeout(detached, mountTableReadTimeout)
		defer cancel()
		mounts, err := c.Read(readCtx, "sys/"+table)
		if err != nil {
			return nil, err
		}
		m.store(key, generation, mountTableSnapshot{mounts: mounts, readAt: readAt})
		return mounts, nil
	})
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case res := <-shared:
		if res.Err != nil {
			return nil, res.Err
		}
		return res.Val.(map[string]any), nil
	}
}

// store keeps snapshot unless a write happened since its read began, and drops expired snapshots so the cache holds
// only the identities read within one TTL.
func (m *mountTableCache) store(key string, generation uint64, snapshot mountTableSnapshot) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if generation != m.generation {
		return
	}
	now := m.now()
	for k, s := range m.snapshots {
		if now.Sub(s.readAt) >= m.ttl {
			delete(m.snapshots, k)
		}
	}
	m.snapshots[key] = snapshot
}

// invalidate drops every snapshot after a mount write or delete through any client.
func (m *mountTableCache) invalidate() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.generation++
	clear(m.snapshots)
}

// cloneJSON deep-copies the maps and slices of a decoded JSON value, so a caller may modify its copy of a shared
// snapshot.
func cloneJSON(v any) any {
	switch t := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(t))
		for k, e := range t {
			out[k] = cloneJSON(e)
		}
		return out
	case []any:
		out := make([]any, len(t))
		for i, e := range t {
			out[i] = cloneJSON(e)
		}
		return out
	}
	return v
}
