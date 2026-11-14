package engine

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus/testutil"
	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/apiutil"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	commonv1alpha1 "github.com/alexandrust88/openbao-config-operator/api/common/v1alpha1"
	"github.com/alexandrust88/openbao-config-operator/internal/openbao"
)

const (
	clusterOne = "cluster-1"
	clusterTwo = "cluster-2"
	foreignTxt = "foreign"
)

// idBao is a fakeBao on a server that reports a cluster id.
type idBao struct {
	*fakeBao
	id string
}

func (c idBao) ClusterID(context.Context) (string, error) { return c.id, nil }

func (c idBao) WithNamespace(ns string) openbao.Client {
	return idBao{fakeBao: c.fakeBao.WithNamespace(ns).(*fakeBao), id: c.id}
}

func (h *harness) useServer(store *fakeStore, id string) {
	h.conns.mu.Lock()
	defer h.conns.mu.Unlock()
	h.conns.client = idBao{fakeBao: &fakeBao{store: store, ns: litAdmin}, id: id}
}

func (h *harness) deleteResource() {
	h.t.Helper()
	if err := h.k8s.Delete(context.Background(), h.get()); err != nil {
		h.t.Fatal(err)
	}
}

func TestServerChangeNeverWritesOrDeletes(t *testing.T) {
	h := newHarness(t, testAdapter{}, deletable(newResource(testName)))
	h.useServer(h.store, clusterOne)
	h.mustReconcile()
	if got := h.get(); got.Status.ServerID != clusterOne {
		t.Fatalf("serverID = %q, want %q", got.Status.ServerID, clusterOne)
	}

	other := &fakeStore{objects: map[string]Payload{policyPath: {keyPolicy: foreignTxt}}}
	h.useServer(other, clusterTwo)
	h.mustReconcile()
	if _, writes := other.counts(); writes != 0 {
		t.Fatalf("wrote %d times to the object of another server", writes)
	}
	assertCondition(t, h.get(), commonv1alpha1.ConditionSynced, "False", commonv1alpha1.ReasonServerChanged)

	h.deleteResource()
	h.mustReconcile()
	if got, ok := other.get(policyPath); !ok || got[keyPolicy] != foreignTxt || other.deleteCount() != 0 {
		t.Fatal("deleted the object of another server")
	}
	if _, ok := h.store.get(policyPath); !ok {
		t.Fatal("deleted the object on the original server through another connection")
	}
}

func TestDeleteLeavesObjectWithForeignMarker(t *testing.T) {
	for name, marker := range map[string]Payload{
		"another cluster":  v2Marked(v2Marker, "cluster-b"),
		"another resource": v2Marked("someone-else", v2Cluster),
	} {
		t.Run(name, func(t *testing.T) {
			obj := deletable(v2Owned(v2At(testName, time.Now(), v2Marker), policyPath))
			obj.Finalizers = []string{DefaultFinalizer}
			h := newHarnessWith(t, v2MarkerAdapter{}, Options{ClusterID: v2Cluster}, obj)
			h.store.putPolicy(marker)
			h.deleteResource()
			if _, err := h.reconcile(); err != nil {
				t.Fatal(err)
			}
			if _, ok := h.store.get(policyPath); !ok || h.store.deleteCount() != 0 {
				t.Fatal("deleted an object whose marker names another owner")
			}
		})
	}
}

// raceAdapter lets a second resource create the shared path while the first one's absent read is in flight.
type raceAdapter struct {
	v2SharedAdapter
	during *func()
}

func (a raceAdapter) Observe(ctx context.Context, c openbao.Client, obj *testResource) (Payload, bool, error) {
	observed, err := c.Read(ctx, a.Path(obj))
	if hook := *a.during; hook != nil && obj.Name == v2Older {
		*a.during = nil
		hook()
	}
	if errors.Is(err, openbao.ErrNotFound) {
		return nil, false, nil
	}
	return observed, err == nil, err
}

func TestConcurrentCreatesHaveOneWriter(t *testing.T) {
	now := time.Now()
	var during func()
	h := newHarness(t, raceAdapter{during: &during}, v2At(v2Older, now.Add(-time.Hour), "u-old"), v2At(testName, now, "u-new"))
	during = func() {
		if _, err := h.reconcileName(testName); err != nil {
			t.Error(err)
		}
	}
	if _, err := h.reconcileName(v2Older); err != nil {
		t.Fatal(err)
	}
	if h.writes() != 1 {
		t.Fatalf("two creates of one path wrote %d times", h.writes())
	}
	owners := 0
	for _, name := range []string{v2Older, testName} {
		if h.getName(name).Status.Acquired != "" {
			owners++
		}
	}
	if owners != 1 {
		t.Fatalf("%d resources own the path", owners)
	}
}

// refusingWriter answers the first create with a 400.
type refusingWriter struct {
	testAdapter
	refusals *int
}

func (a refusingWriter) Write(ctx context.Context, c openbao.Client, obj *testResource, desired Payload, _ bool) (Payload, error) {
	if *a.refusals > 0 {
		*a.refusals--
		return nil, &openbao.ResponseError{StatusCode: 400, Errors: []string{"invalid"}}
	}
	return c.Write(ctx, a.Path(obj), desired)
}

func TestFailedCreateDoesNotAuthoriseAdoption(t *testing.T) {
	refusals := 1
	h := newHarness(t, refusingWriter{refusals: &refusals}, newResource(testName))
	if _, err := h.reconcile(); err == nil {
		t.Fatal("want the create error")
	}
	h.store.putPolicy(Payload{keyPolicy: foreignTxt})
	if _, err := h.reconcile(); err != nil {
		t.Fatal(err)
	}
	if got, _ := h.store.get(policyPath); got[keyPolicy] != foreignTxt {
		t.Fatal("overwrote an object somebody else created after the failed create")
	}
	assertCondition(t, h.get(), commonv1alpha1.ConditionSynced, "False", commonv1alpha1.ReasonConflict)
}

func TestPendingClaimIsIndexed(t *testing.T) {
	obj := newResource(testName)
	obj.Status.Path = policyPath
	if got := PathIndexFunc(client.Object(obj)); len(got) != 1 || got[0] != policyPath {
		t.Fatalf("PathIndexFunc = %v, want the pending claim", got)
	}
}

// cleanupConns counts which resolution path each client came from.
type cleanupConns struct {
	*fakeConnections
	normal, cleanup int
}

func (c *cleanupConns) For(ctx context.Context, ns string, ref commonv1alpha1.ConnectionReference) (openbao.Client, error) {
	c.normal++
	return c.fakeConnections.For(ctx, ns, ref)
}

func (c *cleanupConns) ForCleanup(ctx context.Context, ns string, ref commonv1alpha1.ConnectionReference) (openbao.Client, error) {
	c.cleanup++
	return c.fakeConnections.For(ctx, ns, ref)
}

func TestCleanupUsesForCleanupOnly(t *testing.T) {
	h := newHarness(t, testAdapter{}, deletable(newResource(testName)))
	h.useServer(h.store, clusterOne)
	conns := &cleanupConns{fakeConnections: h.conns}
	h.r.conns = conns
	h.mustReconcile()
	if conns.cleanup != 0 || conns.normal == 0 {
		t.Fatalf("reconcile resolved %d normal, %d cleanup connections", conns.normal, conns.cleanup)
	}
	normal := conns.normal
	h.deleteResource()
	h.mustReconcile()
	if conns.cleanup != 1 || conns.normal != normal {
		t.Fatalf("delete resolved %d normal, %d cleanup connections", conns.normal-normal, conns.cleanup)
	}
	if _, ok := h.store.get(policyPath); ok {
		t.Fatal("cleanup did not delete")
	}
}

func TestCleanupRefusesAnotherServer(t *testing.T) {
	h := newHarness(t, testAdapter{}, deletable(newResource(testName)))
	h.useServer(h.store, clusterOne)
	h.mustReconcile()
	other := &fakeStore{objects: map[string]Payload{policyPath: {keyPolicy: foreignTxt}}}
	h.useServer(other, clusterTwo)
	h.r.conns = &cleanupConns{fakeConnections: h.conns}
	h.deleteResource()
	h.mustReconcile()
	if _, ok := other.get(policyPath); !ok || other.deleteCount() != 0 {
		t.Fatal("cleanup deleted the object of another server")
	}
}

type undeliveredObserver struct{ testAdapter }

func (undeliveredObserver) Observe(context.Context, openbao.Client, *testResource) (Payload, bool, error) {
	return nil, false, fmt.Errorf("the key is not in its Secret: %w", ErrOutputNotDelivered)
}

func TestOutputNotDeliveredReason(t *testing.T) {
	h := newHarness(t, undeliveredObserver{}, newResource(testName))
	res, err := h.reconcile()
	if err != nil || res.RequeueAfter == 0 {
		t.Fatalf("res %+v err %v, want a requeue without error", res, err)
	}
	assertCondition(t, h.get(), commonv1alpha1.ConditionReady, "False", commonv1alpha1.ReasonOutputNotDelivered)
}

func TestObserveRoundTripKeepsServerBinding(t *testing.T) {
	h := newHarness(t, testAdapter{}, deletable(newResource(testName)))
	h.useServer(h.store, clusterOne)
	h.mustReconcile()
	other := &fakeStore{objects: map[string]Payload{policyPath: {keyPolicy: foreignTxt}}}
	h.useServer(other, clusterTwo)
	h.mustReconcile()

	for _, policy := range []commonv1alpha1.ManagementPolicy{commonv1alpha1.ManagementPolicyObserve, commonv1alpha1.ManagementPolicyManage} {
		got := h.get()
		got.Spec.ManagementPolicy = policy
		h.update(got)
		h.mustReconcile()
		got = h.get()
		if got.Status.ServerID != clusterOne {
			t.Fatalf("%s rebound serverID to %q", policy, got.Status.ServerID)
		}
		assertCondition(t, got, commonv1alpha1.ConditionSynced, "False", commonv1alpha1.ReasonServerChanged)
	}
	h.deleteResource()
	h.mustReconcile()
	if got, ok := other.get(policyPath); !ok || got[keyPolicy] != foreignTxt || other.deleteCount() != 0 {
		t.Fatal("changed the object of another server after an Observe round trip")
	}
}

// onceAdapter mimics PKIKey and TOTPKey: the value comes back once, and Observe fails while the Secret lacks it.
type onceAdapter struct {
	outputAdapter
	k8s client.Reader
}

func (a onceAdapter) Observe(ctx context.Context, c openbao.Client, obj *testResource) (Payload, bool, error) {
	p, err := c.Read(ctx, a.Path(obj))
	if errors.Is(err, openbao.ErrNotFound) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	if err := a.k8s.Get(ctx, client.ObjectKey{Namespace: obj.Namespace, Name: obj.Name + "-out"}, &corev1.Secret{}); err != nil {
		return nil, false, ErrOutputNotDelivered
	}
	return p, true, nil
}

func (onceAdapter) OutputSecretName(obj *testResource) string { return obj.Name + "-out" }

func TestUndeliveredOutputIsDeletedAndRecreated(t *testing.T) {
	h := newHarness(t, onceAdapter{}, deletable(newResource(testName)))
	h.r.adapter = onceAdapter{k8s: h.k8s}
	h.store.response = Payload{keySecretID: "once"}
	deliver := false
	h.r.client = interceptor.NewClient(h.k8s.(client.WithWatch), interceptor.Funcs{
		Create: func(ctx context.Context, c client.WithWatch, obj client.Object, opts ...client.CreateOption) error {
			if _, ok := obj.(*corev1.Secret); ok && !deliver {
				return errors.New("transient api error")
			}
			return c.Create(ctx, obj, opts...)
		},
	})
	if _, err := h.reconcile(); err == nil {
		t.Fatal("want the delivery error")
	}
	deliver = true
	h.mustReconcile()
	assertCondition(t, h.get(), commonv1alpha1.ConditionReady, "False", commonv1alpha1.ReasonOutputNotDelivered)
	if st := h.get().Status; st.Acquired != commonv1alpha1.AcquiredCreated || st.Path != policyPath {
		t.Fatalf("the created object is not owned: %+v", st)
	}

	h.deleteResource()
	h.mustReconcile()
	if _, ok := h.store.get(policyPath); ok {
		t.Fatal("deleting with deletionPolicy Delete left the object with the undelivered value in OpenBao")
	}
	if err := h.k8s.Create(context.Background(), deletable(newResource(testName))); err != nil {
		t.Fatal(err)
	}
	h.mustReconcile()
	assertCondition(t, h.get(), commonv1alpha1.ConditionReady, "True", commonv1alpha1.ReasonAvailable)
}

// pendingAdapter mimics an intermediate PKIIssuer: the first write creates the key, and the issuer is not readable
// until a later step.
type pendingAdapter struct{ testAdapter }

func (pendingAdapter) Observe(context.Context, openbao.Client, *testResource) (Payload, bool, error) {
	return nil, false, nil
}

func (pendingAdapter) WriteStatus(context.Context, openbao.Client, *testResource, Payload) error {
	return fmt.Errorf("the issuer is not signed yet: %w", ErrInProgress)
}

func TestCreateThenInProgressStaysDeletable(t *testing.T) {
	h := newHarness(t, pendingAdapter{}, deletable(newResource(testName)))
	if _, err := h.reconcile(); err != nil {
		t.Fatal(err)
	}
	if st := h.get().Status; st.Acquired != commonv1alpha1.AcquiredCreated || st.Path != policyPath {
		t.Fatalf("dropped the claim of a written object: %+v", st)
	}
	h.deleteResource()
	h.mustReconcile()
	if _, ok := h.store.get(policyPath); ok {
		t.Fatal("deleting during the in-progress window left the written object in OpenBao")
	}
}

// staleIDBao still reports its cached cluster id while the server behind the address is now another one.
type staleIDBao struct {
	idBao
	fresh string
}

func (c staleIDBao) FreshClusterID(context.Context) (string, error) { return c.fresh, nil }

func (c staleIDBao) WithNamespace(ns string) openbao.Client {
	return staleIDBao{idBao: c.idBao.WithNamespace(ns).(idBao), fresh: c.fresh}
}

func (h *harness) useStaleServer(store *fakeStore, cached, fresh string) {
	h.conns.mu.Lock()
	defer h.conns.mu.Unlock()
	h.conns.client = staleIDBao{idBao: idBao{fakeBao: &fakeBao{store: store, ns: litAdmin}, id: cached}, fresh: fresh}
}

func TestStaleClusterIDNeverCreatesOrDeletes(t *testing.T) {
	t.Run("create", func(t *testing.T) {
		h := newHarness(t, testAdapter{}, newResource(testName))
		h.useStaleServer(h.store, clusterOne, clusterTwo)
		if _, err := h.reconcile(); err == nil {
			t.Fatal("want the server change")
		}
		if h.writes() != 0 {
			t.Fatal("created an object under a stale cluster id")
		}
		assertCondition(t, h.get(), commonv1alpha1.ConditionSynced, "False", commonv1alpha1.ReasonServerChanged)
	})
	t.Run("delete", func(t *testing.T) {
		h := newHarness(t, testAdapter{}, deletable(newResource(testName)))
		h.useServer(h.store, clusterOne)
		h.mustReconcile()
		other := &fakeStore{objects: map[string]Payload{policyPath: {keyPolicy: foreignTxt}}}
		h.useStaleServer(other, clusterOne, clusterTwo)
		h.deleteResource()
		h.mustReconcile()
		if _, ok := other.get(policyPath); !ok || other.deleteCount() != 0 {
			t.Fatal("deleted the object of the server now behind the address")
		}
	})
}

// lateCreateAdapter reports the shared path absent once and creates it meanwhile, as the older resource's create
// landing between the newer resource's two reads would.
type lateCreateAdapter struct {
	v2SharedAdapter
	pending *bool
}

func (a lateCreateAdapter) Observe(ctx context.Context, c openbao.Client, obj *testResource) (Payload, bool, error) {
	if *a.pending {
		*a.pending = false
		_, err := c.Write(ctx, a.Path(obj), Payload{keyPolicy: foreignTxt})
		return nil, false, err
	}
	observed, err := c.Read(ctx, a.Path(obj))
	if errors.Is(err, openbao.ErrNotFound) {
		return nil, false, nil
	}
	return observed, err == nil, err
}

func TestCreateOrAdoptRaceRereadsClaims(t *testing.T) {
	now := time.Now()
	newer := v2At(testName, now, "u-new")
	newer.Spec.CreationPolicy = commonv1alpha1.CreationPolicyCreateOrAdopt
	pending := true
	h := newHarness(t, lateCreateAdapter{pending: &pending},
		v2Owned(v2At(v2Older, now.Add(-time.Hour), "u-old"), v2SharedAbs), newer)
	h.r.client = interceptor.NewClient(h.k8s.(client.WithWatch), interceptor.Funcs{
		List: func(ctx context.Context, c client.WithWatch, list client.ObjectList, opts ...client.ListOption) error {
			listOpts := &client.ListOptions{}
			listOpts.ApplyOptions(opts)
			if listOpts.FieldSelector != nil {
				return nil
			}
			return c.List(ctx, list, opts...)
		},
	})
	if _, err := h.reconcile(); err != nil {
		t.Fatal(err)
	}
	got := h.get()
	if got.Status.Acquired != "" || h.writes() != 1 {
		t.Fatalf("adopted the object the older resource just created: %+v, %d writes", got.Status, h.writes())
	}
	assertCondition(t, got, commonv1alpha1.ConditionSynced, "False", commonv1alpha1.ReasonConflict)
}

func TestFailedUpdateIsRetriedWithDriftDetectionDisabled(t *testing.T) {
	refusals := 0
	obj := newResource(testName)
	obj.Spec.DriftDetection = commonv1alpha1.DriftDetectionDisabled
	h := newHarness(t, refusingWriter{refusals: &refusals}, obj)
	h.mustReconcile()
	got := h.get()
	got.Spec.Data = map[string]string{keyPolicy: foreignTxt}
	h.update(got)
	refusals = 1
	if _, err := h.reconcile(); err == nil {
		t.Fatal("want the write error")
	}
	if _, err := h.reconcile(); err != nil {
		t.Fatal(err)
	}
	if stored, _ := h.store.get(policyPath); stored[keyPolicy] != foreignTxt {
		t.Fatal("a failed write left the spec change unapplied for good")
	}
}

func TestObserveLeavesOwnershipAlone(t *testing.T) {
	obj := v2Owned(newResource(testName), policyPath)
	obj.Spec.ManagementPolicy = commonv1alpha1.ManagementPolicyObserve
	h := newHarness(t, testAdapter{}, obj)
	h.useServer(h.store, clusterOne)
	h.store.putPolicy(Payload{keyPolicy: testPolicyText})
	h.mustReconcile()
	got := h.get().Status
	if got.ServerID != "" || got.Acquired != commonv1alpha1.AcquiredCreated || got.Path != policyPath || got.Namespace != litAdmin {
		t.Fatalf("Observe changed the ownership fields: %+v", got)
	}
}

// slashAdapter gives testName's object a trailing slash, as an OpenBaoEndpoint path may have, and the others none.
type slashAdapter struct{ testAdapter }

func (slashAdapter) Path(obj *testResource) string {
	if obj.Name == testName {
		return v2SharedPath + "/"
	}
	return v2SharedPath
}

func TestTrailingSlashIsTheSamePath(t *testing.T) {
	now := time.Now()
	newer := v2At(testName, now, "u-new")
	newer.Spec.CreationPolicy = commonv1alpha1.CreationPolicyCreateOrAdopt
	h := newHarness(t, slashAdapter{}, v2Owned(v2At(v2Older, now.Add(-time.Hour), "u-old"), v2SharedAbs), newer)
	h.store.objects[v2SharedAbs] = Payload{keyPolicy: foreignTxt}
	if _, err := h.reconcile(); err != nil {
		t.Fatal(err)
	}
	if h.writes() != 0 {
		t.Fatalf("wrote %d times to a path an older resource owns, spelled with a trailing slash", h.writes())
	}
	assertCondition(t, h.get(), commonv1alpha1.ConditionSynced, "False", commonv1alpha1.ReasonConflict)
}

func TestRecordedTrailingSlashStaysOwned(t *testing.T) {
	obj := v2Owned(newResource(testName), policyPath+"/")
	h := newHarness(t, testAdapter{}, obj)
	h.store.putPolicy(Payload{keyPolicy: testPolicyText})
	h.mustReconcile()
	assertCondition(t, h.get(), commonv1alpha1.ConditionReady, "True", commonv1alpha1.ReasonAvailable)
	if got := PathIndexFunc(client.Object(obj)); len(got) != 1 || got[0] != policyPath {
		t.Fatalf("PathIndexFunc = %v, want the canonical path", got)
	}
}

// settlingWriter finds, once undo is set, that the drift it was called for is gone and writes nothing.
type settlingWriter struct {
	testAdapter
	undo *func()
}

func (a settlingWriter) Write(ctx context.Context, c openbao.Client, obj *testResource, desired Payload, _ bool) (Payload, error) {
	if *a.undo != nil {
		(*a.undo)()
		return nil, ErrNothingWritten
	}
	return c.Write(ctx, a.Path(obj), desired)
}

func TestNothingWrittenIsNoWrite(t *testing.T) {
	var undo func()
	h := newHarnessWith(t, settlingWriter{undo: &undo}, Options{WriteBudgetPerMinute: 1}, newResource(testName))
	h.mustReconcile()
	created, _ := h.store.get(policyPath)
	h.store.putPolicy(Payload{keyPolicy: foreignTxt})
	undo = func() { h.store.putPolicy(created) }
	before := testutil.ToFloat64(writesTotal.WithLabelValues(h.r.kind))
	h.drainEvents()
	h.mustReconcile()
	assertCondition(t, h.get(), commonv1alpha1.ConditionReady, "True", commonv1alpha1.ReasonAvailable)
	if got := testutil.ToFloat64(writesTotal.WithLabelValues(h.r.kind)) - before; got != 0 {
		t.Errorf("openbao_writes_total grew by %v for a write that sent nothing", got)
	}
	for _, e := range h.drainEvents() {
		if strings.Contains(e, "Updated") {
			t.Errorf("event for a write that sent nothing: %s", e)
		}
	}
	if ok, _ := h.r.shared.budget.take(ConnectionKey(testNamespace, newResource(testName).Spec.ConnectionRef)); !ok {
		t.Error("a write that sent nothing spent the write budget")
	}
}

// TestLiveOwnerCheckListsOnlyClaimingKinds: the live re-read of claims before a create lists the own kind, and only
// the other kinds the cached index shows claiming the path (none here: the cache does not show the claim yet), page
// by page and only in the watched namespaces.
func TestLiveOwnerCheckListsOnlyClaimingKinds(t *testing.T) {
	now := time.Now()
	pending := true
	newer := v2At(testName, now, "u-new")
	h := newHarnessWith(t, lateCreateAdapter{pending: &pending}, Options{Namespaces: []string{testNamespace, "other"}},
		v2Owned(v2At(v2Older, now.Add(-time.Hour), "u-old"), v2SharedAbs), newer)
	builder := fake.NewClientBuilder().WithScheme(h.r.scheme).WithStatusSubresource(&testResource{}).
		WithIndex(&testResource{}, PathIndex, PathIndexFunc).
		WithObjects(v2Owned(v2At(v2Older, now.Add(-time.Hour), "u-old"), v2SharedAbs), newer)
	for _, kind := range []client.Object{&corev1.ConfigMap{}, &corev1.Service{}, &corev1.ServiceAccount{}, &corev1.Pod{}} {
		builder = builder.WithIndex(kind, PathIndex, PathIndexFunc)
		gvk, err := apiutil.GVKForObject(kind, h.r.scheme)
		if err != nil {
			t.Fatal(err)
		}
		h.r.shared.addKind(gvk)
	}
	var live []string
	h.k8s = builder.Build()
	h.r.client = interceptor.NewClient(h.k8s.(client.WithWatch), interceptor.Funcs{
		List: func(ctx context.Context, c client.WithWatch, list client.ObjectList, opts ...client.ListOption) error {
			listOpts := &client.ListOptions{}
			listOpts.ApplyOptions(opts)
			if listOpts.FieldSelector != nil {
				return nil
			}
			if listOpts.Limit == 0 {
				t.Errorf("live LIST of %T without a page limit", list)
			}
			live = append(live, fmt.Sprintf("%T/%s", list, listOpts.Namespace))
			return c.List(ctx, list, opts...)
		},
	})
	if _, err := h.reconcile(); err != nil {
		t.Fatal(err)
	}
	want := []string{"*engine.testResourceList/" + testNamespace, "*engine.testResourceList/other"}
	if !slices.Equal(live, want) {
		t.Errorf("live LISTs = %v, want %v", live, want)
	}
	assertCondition(t, h.get(), commonv1alpha1.ConditionSynced, "False", commonv1alpha1.ReasonConflict)
}
