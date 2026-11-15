package sys

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	sysv1alpha1 "github.com/alexandrust88/openbao-config-operator/api/sys/v1alpha1"
	"github.com/alexandrust88/openbao-config-operator/internal/openbao"
)

// countingBao serves one mount table under an identity and counts the reads. Write and Delete apply onWrite and
// onDelete, standing in for the server's change.
type countingBao struct {
	openbao.Client
	id       string
	table    map[string]any
	readErr  error
	gate     chan struct{}
	reads    atomic.Int32
	priority atomic.Int32
	onWrite  func()
	onDelete func()
}

func (f *countingBao) Identity() string { return f.id }

func (f *countingBao) Read(ctx context.Context, _ string) (map[string]any, error) {
	f.reads.Add(1)
	if openbao.HasPriority(ctx) {
		f.priority.Add(1)
	}
	if f.gate != nil {
		<-f.gate
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if f.readErr != nil {
		return nil, f.readErr
	}
	return f.table, nil
}

func (f *countingBao) Write(context.Context, string, map[string]any) (map[string]any, error) {
	if f.onWrite != nil {
		f.onWrite()
	}
	return nil, nil
}

func (f *countingBao) Delete(context.Context, string) error {
	if f.onDelete != nil {
		f.onDelete()
	}
	return nil
}

func TestMountTableSharedWithinTTL(t *testing.T) {
	now := time.Unix(0, 0)
	cache := newMountTableCache()
	cache.now = func() time.Time { return now }
	c := &countingBao{id: "a", table: decodeRead(t, goldenAuthTable)}
	for range 5 {
		if _, err := cache.read(context.Background(), c, authTable); err != nil {
			t.Fatal(err)
		}
	}
	if got := c.reads.Load(); got != 1 {
		t.Fatalf("5 reads within the TTL sent %d table reads, want 1", got)
	}
	if _, err := cache.read(context.Background(), c, secretsTable); err != nil {
		t.Fatal(err)
	}
	other := &countingBao{id: "b", table: c.table}
	if _, err := cache.read(context.Background(), other, authTable); err != nil {
		t.Fatal(err)
	}
	if c.reads.Load() != 2 || other.reads.Load() != 1 {
		t.Errorf("another table or identity must read on its own: reads %d and %d", c.reads.Load(), other.reads.Load())
	}
	now = now.Add(mountTableTTL)
	if _, err := cache.read(context.Background(), c, authTable); err != nil {
		t.Fatal(err)
	}
	if got := c.reads.Load(); got != 3 {
		t.Errorf("a read after the TTL sent %d table reads in all, want 3", got)
	}
	if len(cache.snapshots) != 1 {
		t.Errorf("expired snapshots must be dropped, %d kept", len(cache.snapshots))
	}
}

func TestMountTableErrorsNotCached(t *testing.T) {
	cache := newMountTableCache()
	denied := &openbao.ResponseError{StatusCode: 403, Errors: []string{"permission denied"}}
	c := &countingBao{id: "a", table: decodeRead(t, goldenAuthTable), readErr: denied}
	for range 2 {
		if _, err := cache.read(context.Background(), c, authTable); !errors.Is(err, denied) {
			t.Fatalf("err = %v, want the 403", err)
		}
	}
	c.readErr = openbao.ErrNotFound
	if _, err := cache.read(context.Background(), c, authTable); !errors.Is(err, openbao.ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
	c.readErr = nil
	if _, err := cache.read(context.Background(), c, authTable); err != nil {
		t.Fatal(err)
	}
	if got := c.reads.Load(); got != 4 {
		t.Errorf("failed reads must not be cached: %d table reads, want 4", got)
	}
}

func TestMountTableConcurrentReadsShareOne(t *testing.T) {
	cache := newMountTableCache()
	c := &countingBao{id: "a", table: decodeRead(t, goldenAuthTable), gate: make(chan struct{})}
	const callers = 10
	var wg sync.WaitGroup
	errs := make(chan error, callers)
	for range callers {
		wg.Go(func() {
			mounts, err := cache.read(context.Background(), c, authTable)
			if err == nil && mounts["up/"] == nil {
				err = errors.New("the shared read lacks up/")
			}
			errs <- err
		})
	}
	for c.reads.Load() == 0 {
		time.Sleep(time.Millisecond)
	}
	// The callers wait in the singleflight call or, once it stored the snapshot, find it; either way one read.
	time.Sleep(50 * time.Millisecond)
	close(c.gate)
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Error(err)
		}
	}
	if got := c.reads.Load(); got != 1 {
		t.Errorf("%d concurrent reads sent %d table reads, want 1", callers, got)
	}
}

func TestMountTableReadAfterCancelledLeader(t *testing.T) {
	cache := newMountTableCache()
	c := &countingBao{id: "a", table: decodeRead(t, goldenAuthTable), gate: make(chan struct{})}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := cache.read(ctx, c, authTable)
		done <- err
	}()
	for c.reads.Load() == 0 {
		time.Sleep(time.Millisecond)
	}
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	close(c.gate)
	if _, err := cache.read(context.Background(), c, authTable); err != nil {
		t.Errorf("a cancelled caller must not fail the shared read: %v", err)
	}
}

func waitReads(t *testing.T, c *countingBao, want int32) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for c.reads.Load() < want {
		if time.Now().After(deadline) {
			t.Fatalf("%d table reads, want %d", c.reads.Load(), want)
		}
		time.Sleep(time.Millisecond)
	}
}

func TestMountTableInvalidateDuringRead(t *testing.T) {
	cache := newMountTableCache()
	before := &countingBao{id: "a", table: map[string]any{"before/": map[string]any{}}, gate: make(chan struct{})}
	after := &countingBao{id: "a", table: map[string]any{"after/": map[string]any{}}}
	done := make(chan error, 1)
	go func() {
		_, err := cache.read(context.Background(), before, authTable)
		done <- err
	}()
	waitReads(t, before, 1)
	cache.invalidate()
	mounts, err := cache.read(context.Background(), after, authTable)
	if err != nil || mounts["after/"] == nil {
		t.Fatalf("a read after invalidate joined the read in flight: mounts %v err %v", mounts, err)
	}
	close(before.gate)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if before.reads.Load()+after.reads.Load() != 2 {
		t.Errorf("%d table reads, want 2", before.reads.Load()+after.reads.Load())
	}
	if got := cache.snapshots["a\x00"+authTable].mounts; got["after/"] == nil {
		t.Errorf("the read in flight during invalidate was stored: %v", got)
	}
}

func TestMountTablePriorityReadsApart(t *testing.T) {
	cache := newMountTableCache()
	c := &countingBao{id: "a", table: decodeRead(t, goldenAuthTable), gate: make(chan struct{})}
	var wg sync.WaitGroup
	for i, ctx := range []context.Context{context.Background(), openbao.WithPriority(context.Background())} {
		wg.Go(func() {
			if _, err := cache.read(ctx, c, authTable); err != nil {
				t.Error(err)
			}
		})
		waitReads(t, c, int32(i+1))
	}
	close(c.gate)
	wg.Wait()
	if c.reads.Load() != 2 || c.priority.Load() != 1 {
		t.Errorf("reads %d, priority reads %d: a priority caller must not join a drift read", c.reads.Load(), c.priority.Load())
	}
}

// TestMountObserveAfterWriteAndDelete checks that the observes after a write or delete, such as the engine's
// read-back, read the table again instead of a snapshot from before the change.
func TestMountObserveAfterWriteAndDelete(t *testing.T) {
	mountTables.invalidate()
	a := AuthMountAdapter{}
	m := fullAuthMount()
	c := &countingBao{id: t.Name(), table: decodeRead(t, goldenAuthTable)}
	c.onWrite = func() {
		table := decodeRead(t, goldenAuthTable)
		table["up/"].(map[string]any)[litDescription] = "written"
		c.table = table
	}
	c.onDelete = func() { c.table = map[string]any{} }
	for range 3 {
		if _, found, err := a.Observe(context.Background(), c, m); err != nil || !found {
			t.Fatalf("found=%v err=%v", found, err)
		}
	}
	if got := c.reads.Load(); got != 1 {
		t.Fatalf("3 observes sent %d table reads, want 1", got)
	}

	if _, err := a.Write(context.Background(), c, m, mountPayload(litUserpass, m.Spec.MountFields), false); err != nil {
		t.Fatal(err)
	}
	observed, found, err := a.Observe(context.Background(), c, m)
	if err != nil || !found || observed[litDescription] != "written" {
		t.Fatalf("read-back after a write: found=%v err=%v description=%v", found, err, observed[litDescription])
	}

	if err := a.Delete(context.Background(), c, m); err != nil {
		t.Fatal(err)
	}
	if _, found, err := a.Observe(context.Background(), c, m); err != nil || found {
		t.Errorf("observe after a delete: found=%v err=%v, want absent", found, err)
	}
}

// TestMountObserveDoesNotModifySnapshot checks that dropping an unmanaged KV version from one observe leaves the
// shared snapshot intact for the next.
func TestMountObserveDoesNotModifySnapshot(t *testing.T) {
	mountTables.invalidate()
	a := SecretsMountAdapter{}
	c := &countingBao{id: t.Name(), table: decodeRead(t, goldenMountsTable)}
	unmanaged := &sysv1alpha1.SecretsMount{ObjectMeta: metav1.ObjectMeta{Name: "kv2"}, Spec: sysv1alpha1.SecretsMountSpec{Type: "kv"}}
	if _, _, err := a.Observe(context.Background(), c, unmanaged); err != nil {
		t.Fatal(err)
	}
	managed := unmanaged.DeepCopy()
	managed.Spec.Type = "kv-v2"
	observed, _, err := a.Observe(context.Background(), c, managed)
	if err != nil {
		t.Fatal(err)
	}
	if got := observed["options"].(map[string]any)[optionVersion]; got != "2" {
		t.Errorf("version option = %v, want 2", got)
	}
	if got := c.reads.Load(); got != 1 {
		t.Errorf("2 observes sent %d table reads, want 1", got)
	}
}
