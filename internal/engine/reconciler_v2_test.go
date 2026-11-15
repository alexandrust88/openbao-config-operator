package engine

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
	dto "github.com/prometheus/client_model/go"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/event"

	commonv1alpha1 "github.com/alexandrust88/openbao-config-operator/api/common/v1alpha1"
	corev1alpha1 "github.com/alexandrust88/openbao-config-operator/api/core/v1alpha1"
	"github.com/alexandrust88/openbao-config-operator/internal/openbao"
)

const (
	v2SharedPath = "sys/policies/acl/shared"
	v2SharedAbs  = litAdmin + "/" + v2SharedPath
	v2Older      = "older"
	v2Marker     = "marker"
	v2Cluster    = "cluster-a"
	v2TypeOld    = "kv1"
	v2TypeNew    = "kv2"
	v2Changed    = "changed-in-spec"
	v2Accessor   = "accessor"
	v2BindPass   = "bindpass"
	v2Initial    = "initial-secret"
	v2ServerOne  = "server-1"
)

// v2SharedAdapter writes every resource to the same path, so two resources compete for it.
type v2SharedAdapter struct{ testAdapter }

func (v2SharedAdapter) Path(*testResource) string { return v2SharedPath }

func v2At(name string, created time.Time, uid string) *testResource {
	obj := newResource(name)
	obj.CreationTimestamp = metav1.NewTime(created)
	obj.UID = types.UID(uid)
	return obj
}

func v2Owned(obj *testResource, path string) *testResource {
	obj.Status.Acquired = commonv1alpha1.AcquiredCreated
	obj.Status.Path = path
	obj.Status.Namespace = litAdmin
	return obj
}

func (h *harness) reconcileName(name string) (ctrl.Result, error) {
	h.t.Helper()
	return h.r.Reconcile(context.Background(), ctrl.Request{NamespacedName: client.ObjectKey{Namespace: testNamespace, Name: name}})
}

func (h *harness) getName(name string) *testResource {
	h.t.Helper()
	obj := &testResource{}
	if err := h.k8s.Get(context.Background(), client.ObjectKey{Namespace: testNamespace, Name: name}, obj); err != nil {
		h.t.Fatalf("get %s: %v", name, err)
	}
	return obj
}

func (h *harness) writes() int {
	_, writes := h.store.counts()
	return writes
}

func TestV2PathIndexConflict(t *testing.T) {
	now := time.Now()
	t.Run("newer resource on the same path never writes", func(t *testing.T) {
		older := v2Owned(v2At(v2Older, now.Add(-time.Hour), "u-old"), v2SharedAbs)
		newer := v2At(testName, now, "u-new")
		h := newHarness(t, v2SharedAdapter{}, older, newer)
		before := testutil.ToFloat64(conflictsTotal.WithLabelValues(litTestResource))

		res := h.mustReconcile()
		if h.writes() != 0 {
			t.Fatalf("newer resource wrote %d times", h.writes())
		}
		if res.RequeueAfter == 0 {
			t.Fatal("conflict did not requeue")
		}
		got := h.get()
		assertCondition(t, got, commonv1alpha1.ConditionReady, metav1.ConditionFalse, commonv1alpha1.ReasonConflict)
		if got.Status.Acquired != "" || got.Status.Path != "" {
			t.Fatalf("conflicting resource kept a claim: %+v", got.Status)
		}
		if !strings.Contains(v2Condition(got, commonv1alpha1.ConditionSynced).Message, v2Older) {
			t.Fatalf("message does not name the owner: %q", v2Condition(got, commonv1alpha1.ConditionSynced).Message)
		}
		if after := testutil.ToFloat64(conflictsTotal.WithLabelValues(litTestResource)); after != before+1 {
			t.Fatalf("openbao_conflicts_total = %v, want %v", after, before+1)
		}
	})
	t.Run("older resource keeps reconciling", func(t *testing.T) {
		older := v2Owned(v2At(v2Older, now.Add(-time.Hour), "u-old"), v2SharedAbs)
		newer := v2Owned(v2At(testName, now, "u-new"), v2SharedAbs)
		h := newHarness(t, v2SharedAdapter{}, older, newer)
		if _, err := h.reconcileName(v2Older); err != nil {
			t.Fatal(err)
		}
		assertCondition(t, h.getName(v2Older), commonv1alpha1.ConditionReady, metav1.ConditionTrue, commonv1alpha1.ReasonAvailable)
		h.mustReconcile()
		assertCondition(t, h.get(), commonv1alpha1.ConditionReady, metav1.ConditionFalse, commonv1alpha1.ReasonConflict)
	})
	t.Run("same timestamp keeps the existing claim", func(t *testing.T) {
		claimed := v2Owned(v2At("z", now, "u-z"), v2SharedAbs)
		h := newHarness(t, v2SharedAdapter{}, claimed, v2At("a", now, "u-a"))
		if _, err := h.reconcileName("a"); err != nil {
			t.Fatal(err)
		}
		assertCondition(t, h.getName("a"), commonv1alpha1.ConditionReady, metav1.ConditionFalse, commonv1alpha1.ReasonConflict)
	})
	t.Run("two claims with the same timestamp are decided by name", func(t *testing.T) {
		h := newHarness(t, v2SharedAdapter{}, v2Owned(v2At("a", now, "u-a"), v2SharedAbs), v2Owned(v2At("b", now, "u-b"), v2SharedAbs))
		if _, err := h.reconcileName("b"); err != nil {
			t.Fatal(err)
		}
		assertCondition(t, h.getName("b"), commonv1alpha1.ConditionReady, metav1.ConditionFalse, commonv1alpha1.ReasonConflict)
		if _, err := h.reconcileName("a"); err != nil {
			t.Fatal(err)
		}
		assertCondition(t, h.getName("a"), commonv1alpha1.ConditionReady, metav1.ConditionTrue, commonv1alpha1.ReasonAvailable)
	})
	t.Run("another server is no conflict", func(t *testing.T) {
		older := v2Owned(v2At(v2Older, now.Add(-time.Hour), "u-old"), v2SharedAbs)
		older.Spec.ConnectionRef.Name = "other-server"
		h := newHarness(t, v2SharedAdapter{}, older, v2At(testName, now, "u-new"))
		h.r.conns = &v2ServerConns{fakeConnections: h.conns, keys: map[string]string{
			newResource(testName).Spec.ConnectionRef.Name: v2ServerOne, "other-server": "server-2"}}
		h.mustReconcile()
		assertCondition(t, h.get(), commonv1alpha1.ConditionReady, metav1.ConditionTrue, commonv1alpha1.ReasonAvailable)
	})
	t.Run("observe-only resources own nothing", func(t *testing.T) {
		older := v2Owned(v2At(v2Older, now.Add(-time.Hour), "u-old"), v2SharedAbs)
		older.Spec.ManagementPolicy = commonv1alpha1.ManagementPolicyObserve
		h := newHarness(t, v2SharedAdapter{}, older, v2At(testName, now, "u-new"))
		h.mustReconcile()
		assertCondition(t, h.get(), commonv1alpha1.ConditionReady, metav1.ConditionTrue, commonv1alpha1.ReasonAvailable)
	})
	t.Run("delete leaves the object of an older owner", func(t *testing.T) {
		older := v2Owned(v2At(v2Older, now.Add(-time.Hour), "u-old"), v2SharedAbs)
		newer := deletable(v2Owned(v2At(testName, now, "u-new"), v2SharedAbs))
		newer.Finalizers = []string{DefaultFinalizer}
		h := newHarness(t, v2SharedAdapter{}, older, newer)
		h.store.objects[v2SharedAbs] = Payload{keyPolicy: testPolicyText}
		if err := h.k8s.Delete(context.Background(), h.get()); err != nil {
			t.Fatal(err)
		}
		h.mustReconcile()
		if _, ok := h.store.get(v2SharedAbs); !ok || h.store.deleteCount() != 0 {
			t.Fatal("deleted the object of the older owner")
		}
	})
}

func v2Condition(obj *testResource, conditionType string) metav1.Condition {
	for _, c := range obj.Status.Conditions {
		if c.Type == conditionType {
			return c
		}
	}
	return metav1.Condition{}
}

// v2ServerConns names a server per connection and can pause one.
type v2ServerConns struct {
	*fakeConnections
	keys   map[string]string
	paused bool
}

func (c *v2ServerConns) ServerFor(_ context.Context, _ string, ref commonv1alpha1.ConnectionReference) (Server, error) {
	return Server{Key: c.keys[ref.Name], Paused: c.paused}, nil
}

func TestV2SpecPathChangeIsNotOwnership(t *testing.T) {
	obj := v2Owned(newResource(testName), litAdmin+"/sys/policies/acl/old-name")
	h := newHarness(t, testAdapter{}, obj)
	h.store.putPolicy(Payload{keyPolicy: "foreign"})
	h.mustReconcile()
	if h.writes() != 0 {
		t.Fatal("took over an object at a new path without creationPolicy Adopt")
	}
	assertCondition(t, h.get(), commonv1alpha1.ConditionSynced, metav1.ConditionFalse, commonv1alpha1.ReasonConflict)
}

// v2MarkerAdapter keeps the ownership marker in the "metadata" map of the object.
type v2MarkerAdapter struct{ testAdapter }

func (v2MarkerAdapter) Mark(_ *testResource, desired Payload, m OwnershipMarker) Payload {
	out := maps.Clone(desired)
	out["metadata"] = map[string]any{"managed-by": m.ManagedBy, "cluster": m.ClusterID, "uid": m.UID}
	return out
}

func (v2MarkerAdapter) MarkerOf(observed Payload) (OwnershipMarker, bool) {
	md, ok := observed["metadata"].(map[string]any)
	if !ok {
		return OwnershipMarker{}, false
	}
	m := OwnershipMarker{}
	m.ManagedBy, _ = md["managed-by"].(string)
	m.ClusterID, _ = md["cluster"].(string)
	m.UID, _ = md["uid"].(string)
	return m, m.UID != ""
}

func v2Marked(uid, cluster string) Payload {
	return Payload{keyPolicy: testPolicyText, "metadata": map[string]any{
		"managed-by": DefaultFieldOwner, "cluster": cluster, "uid": uid,
	}}
}

func TestV2Marker(t *testing.T) {
	opts := Options{ClusterID: v2Cluster}
	t.Run("create writes the marker", func(t *testing.T) {
		h := newHarnessWith(t, v2MarkerAdapter{}, opts, v2At(testName, time.Now(), v2Marker))
		h.mustReconcile()
		got, _ := h.store.get(policyPath)
		if md, _ := got["metadata"].(map[string]any); md["uid"] != v2Marker || md["cluster"] != v2Cluster {
			t.Fatalf("stored = %v", got)
		}
	})
	t.Run("own marker survives a lost status without a write", func(t *testing.T) {
		h := newHarnessWith(t, v2MarkerAdapter{}, opts, v2At(testName, time.Now(), v2Marker))
		h.store.putPolicy(v2Marked(v2Marker, v2Cluster))
		h.mustReconcile()
		if h.writes() != 0 {
			t.Fatalf("wrote %d times", h.writes())
		}
		if got := h.get(); got.Status.Acquired == "" {
			t.Fatal("not acquired")
		}
	})
	t.Run("marker of another cluster is a conflict even for Adopt", func(t *testing.T) {
		obj := v2At(testName, time.Now(), v2Marker)
		obj.Spec.CreationPolicy = commonv1alpha1.CreationPolicyAdopt
		h := newHarnessWith(t, v2MarkerAdapter{}, opts, obj)
		h.store.putPolicy(v2Marked(v2Marker, "cluster-b"))
		h.mustReconcile()
		if h.writes() != 0 {
			t.Fatal("wrote over another cluster's object")
		}
		assertCondition(t, h.get(), commonv1alpha1.ConditionSynced, metav1.ConditionFalse, commonv1alpha1.ReasonConflict)
	})
	t.Run("stale marker wins over status", func(t *testing.T) {
		obj := v2Owned(v2At(testName, time.Now(), v2Marker), policyPath)
		h := newHarnessWith(t, v2MarkerAdapter{}, opts, obj)
		h.store.putPolicy(v2Marked("someone-else", v2Cluster))
		h.mustReconcile()
		assertCondition(t, h.get(), commonv1alpha1.ConditionSynced, metav1.ConditionFalse, commonv1alpha1.ReasonConflict)
	})
	t.Run("adopt rewrites a stale marker", func(t *testing.T) {
		obj := v2At(testName, time.Now(), v2Marker)
		obj.Spec.CreationPolicy = commonv1alpha1.CreationPolicyAdopt
		h := newHarnessWith(t, v2MarkerAdapter{}, opts, obj)
		h.store.putPolicy(v2Marked("someone-else", v2Cluster))
		h.mustReconcile()
		got, _ := h.store.get(policyPath)
		if md, _ := got["metadata"].(map[string]any); md["uid"] != v2Marker {
			t.Fatalf("marker not rewritten: %v", got)
		}
		if h.get().Status.Acquired != commonv1alpha1.AcquiredAdopted {
			t.Fatal("not adopted")
		}
	})
}

// v2FlakyWriter applies its first write but loses the response, after the engine claimed the path.
type v2FlakyWriter struct {
	testAdapter
	failures *int
}

func (a v2FlakyWriter) Write(ctx context.Context, c openbao.Client, obj *testResource, desired Payload, _ bool) (Payload, error) {
	response, err := c.Write(ctx, a.Path(obj), desired)
	if *a.failures > 0 {
		*a.failures--
		return nil, errors.New("connection reset")
	}
	return response, err
}

func TestV2ClaimBeforeCreate(t *testing.T) {
	failures := 1
	h := newHarness(t, v2FlakyWriter{failures: &failures}, newResource(testName))
	if _, err := h.reconcile(); err == nil {
		t.Fatal("want the write error")
	}
	if got := h.get(); got.Status.Path != policyPath || got.Status.Acquired != "" {
		t.Fatalf("want an unacquired claim before the create: %+v", got.Status)
	}
	h.mustReconcile()
	got := h.get()
	assertCondition(t, got, commonv1alpha1.ConditionReady, metav1.ConditionTrue, commonv1alpha1.ReasonAvailable)
	if got.Status.Acquired != commonv1alpha1.AcquiredCreated {
		t.Fatalf("acquired = %q", got.Status.Acquired)
	}
}

func TestV2ClaimSurvivesLostStatusAfterCreate(t *testing.T) {
	obj := newResource(testName)
	obj.Status.Path, obj.Status.Namespace = policyPath, litAdmin
	h := newHarness(t, testAdapter{}, obj)
	h.store.putPolicy(Payload{keyPolicy: testPolicyText})
	h.mustReconcile()
	if got := h.get(); got.Status.Acquired != commonv1alpha1.AcquiredCreated {
		t.Fatalf("own create taken for a foreign object: %+v", got.Status)
	}
}

// v2CollectionAdapter creates every resource by a POST to one collection path, failing for testName.
type v2CollectionAdapter struct{ testAdapter }

func (v2CollectionAdapter) Path(*testResource) string { return "identity/entity-alias" }

func (v2CollectionAdapter) Observe(context.Context, openbao.Client, *testResource) (Payload, bool, error) {
	return nil, false, nil
}

func (v2CollectionAdapter) SkipReadBack(*testResource) bool { return true }

func (a v2CollectionAdapter) Write(ctx context.Context, c openbao.Client, obj *testResource, desired Payload, _ bool) (Payload, error) {
	if obj.Name == testName {
		return nil, &openbao.ResponseError{StatusCode: 400, Errors: []string{"invalid canonical id"}}
	}
	return c.Write(ctx, a.Path(obj)+"/"+obj.Name, desired)
}

func TestV2FailingCollectionCreateBlocksNoOne(t *testing.T) {
	now := time.Now()
	h := newHarness(t, v2CollectionAdapter{}, v2At(testName, now.Add(-time.Hour), "u-fail"), v2At("second", now, "u-ok"))
	if _, err := h.reconcile(); err == nil {
		t.Fatal("want the create error")
	}
	if _, err := h.reconcileName("second"); err != nil {
		t.Fatal(err)
	}
	assertCondition(t, h.getName("second"), commonv1alpha1.ConditionReady, metav1.ConditionTrue, commonv1alpha1.ReasonAvailable)
}

// v2DestructiveAdapter holds data and has a create-only "type".
type v2DestructiveAdapter struct{ testAdapter }

func (v2DestructiveAdapter) Destructive() bool { return true }

func (v2DestructiveAdapter) Fields() FieldSpec {
	return FieldSpec{WriteOnly: []string{keyPassword}, CreateOnly: []string{keyType}}
}

func v2AllowDestructive(obj *testResource) {
	if obj.Annotations == nil {
		obj.Annotations = map[string]string{}
	}
	obj.Annotations[commonv1alpha1.AnnotationAllowDestructiveDelete] = annotationTrue
}

func TestV2DestructiveDelete(t *testing.T) {
	h := newHarness(t, v2DestructiveAdapter{}, deletable(newResource(testName)))
	h.mustReconcile()
	if err := h.k8s.Delete(context.Background(), h.get()); err != nil {
		t.Fatal(err)
	}
	res := h.mustReconcile()
	got := h.get()
	if !controllerutil.ContainsFinalizer(got, DefaultFinalizer) || h.store.deleteCount() != 0 || res.RequeueAfter != 0 {
		t.Fatalf("guard did not hold: finalizers %v deletes %d requeue %v", got.Finalizers, h.store.deleteCount(), res.RequeueAfter)
	}
	assertCondition(t, got, commonv1alpha1.ConditionSynced, metav1.ConditionFalse, commonv1alpha1.ReasonDeleteGuarded)

	v2AllowDestructive(got)
	if err := h.k8s.Update(context.Background(), got); err != nil {
		t.Fatal(err)
	}
	h.mustReconcile()
	if _, ok := h.store.get(policyPath); ok {
		t.Fatal("annotated destructive delete did not delete")
	}
}

func TestV2DestructiveRecreate(t *testing.T) {
	obj := newResource(testName)
	obj.Spec.Data[keyType] = v2TypeOld
	h := newHarness(t, v2DestructiveAdapter{}, obj)
	h.mustReconcile()

	got := h.get()
	got.Spec.Data[keyType] = v2TypeNew
	h.update(got)
	h.mustReconcile()
	if stored, _ := h.store.get(policyPath); stored[keyType] != v2TypeOld || h.store.deleteCount() != 0 {
		t.Fatalf("recreated without the annotation: %v", stored)
	}
	assertCondition(t, h.get(), commonv1alpha1.ConditionReady, metav1.ConditionFalse, commonv1alpha1.ReasonDeleteGuarded)

	got = h.get()
	v2AllowDestructive(got)
	if err := h.k8s.Update(context.Background(), got); err != nil {
		t.Fatal(err)
	}
	h.mustReconcile()
	if stored, _ := h.store.get(policyPath); stored[keyType] != v2TypeNew || h.store.deleteCount() != 1 {
		t.Fatalf("not recreated: %v, deletes %d", stored, h.store.deleteCount())
	}
	assertCondition(t, h.get(), commonv1alpha1.ConditionReady, metav1.ConditionTrue, commonv1alpha1.ReasonAvailable)
}

// v2GoneParentDeleter answers like OpenBao for a path under a mount that no longer exists.
type v2GoneParentDeleter struct{ testAdapter }

func (v2GoneParentDeleter) Delete(context.Context, openbao.Client, *testResource) error {
	return &openbao.ResponseError{StatusCode: 400, Errors: []string{`no handler for route "gone/role/x". route entry not found.`}}
}

func TestV2DeleteWithParentGone(t *testing.T) {
	h := newHarness(t, v2GoneParentDeleter{}, deletable(newResource(testName)))
	h.mustReconcile()
	if err := h.k8s.Delete(context.Background(), h.get()); err != nil {
		t.Fatal(err)
	}
	h.mustReconcile()
	if err := h.k8s.Get(context.Background(), client.ObjectKey{Namespace: testNamespace, Name: testName}, &testResource{}); err == nil {
		t.Fatal("finalizer kept although the parent mount is gone")
	}
}

// v2NamedOutputAdapter names its output Secret up front.
type v2NamedOutputAdapter struct{ outputAdapter }

func (v2NamedOutputAdapter) OutputSecretName(obj *testResource) string { return obj.Name + "-out" }

func TestV2OutputSecretNotOwnedBeforeWrite(t *testing.T) {
	foreign := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: outputSecretName, Namespace: testNamespace}}
	h := newHarness(t, v2NamedOutputAdapter{}, newResource(testName), foreign)
	h.store.response = Payload{keySecretID: "once"}
	if _, err := h.reconcile(); err != nil {
		t.Fatalf("err = %v, want a stall", err)
	}
	if h.writes() != 0 {
		t.Fatal("wrote to OpenBao although the output Secret is not ours")
	}
	assertCondition(t, h.get(), commonv1alpha1.ConditionSynced, metav1.ConditionFalse, commonv1alpha1.ReasonSecretNotOwned)
}

type v2CriticalAdapter struct{ testAdapter }

func (v2CriticalAdapter) DriftTier() Tier { return TierAccessCritical }

func assertBetween(t *testing.T, got, low, high time.Duration) {
	t.Helper()
	if got < low || got > high {
		t.Fatalf("RequeueAfter = %v, want %v..%v", got, low, high)
	}
}

func TestV2DriftTiers(t *testing.T) {
	t.Run("access-critical default", func(t *testing.T) {
		h := newHarness(t, v2CriticalAdapter{}, newResource(testName))
		assertBetween(t, h.mustReconcile().RequeueAfter, 12*time.Minute, 18*time.Minute)
	})
	t.Run("tier options", func(t *testing.T) {
		opts := Options{DriftIntervalCritical: time.Minute, DriftIntervalStandard: 10 * time.Minute}
		h := newHarnessWith(t, v2CriticalAdapter{}, opts, newResource(testName))
		assertBetween(t, h.mustReconcile().RequeueAfter, 48*time.Second, 72*time.Second)
		h = newHarnessWith(t, testAdapter{}, opts, newResource(testName))
		assertBetween(t, h.mustReconcile().RequeueAfter, 8*time.Minute, 12*time.Minute)
	})
	t.Run("spec.driftInterval overrides the tier", func(t *testing.T) {
		obj := newResource(testName)
		obj.Spec.DriftInterval = &metav1.Duration{Duration: 5 * time.Minute}
		h := newHarness(t, v2CriticalAdapter{}, obj)
		assertBetween(t, h.mustReconcile().RequeueAfter, 4*time.Minute, 6*time.Minute)
	})
	t.Run("spec.driftInterval below the floor", func(t *testing.T) {
		obj := newResource(testName)
		obj.Spec.DriftInterval = &metav1.Duration{Duration: time.Nanosecond}
		h := newHarness(t, testAdapter{}, obj)
		assertBetween(t, h.mustReconcile().RequeueAfter, MinDriftInterval*4/5, MinDriftInterval*6/5)
	})
}

func TestV2DriftRepairsAndLag(t *testing.T) {
	h := newHarness(t, testAdapter{}, newResource(testName))
	repairs := func() float64 { return testutil.ToFloat64(driftRepairs.WithLabelValues(litTestResource)) }
	start := repairs()
	h.mustReconcile()
	if repairs() != start {
		t.Fatal("a create counted as a drift repair")
	}
	got := h.get()
	got.Spec.Data[keyPolicy] = v2Changed
	h.update(got)
	h.mustReconcile()
	if repairs() != start {
		t.Fatal("a spec change counted as a drift repair")
	}

	key := types.NamespacedName{Namespace: testNamespace, Name: testName}
	h.r.due.set(key, time.Now().Add(-90*time.Second))
	h.store.putPolicy(Payload{keyPolicy: "out-of-band"})
	h.mustReconcile()
	if repairs() != start+1 {
		t.Fatalf("openbao_drift_repairs_total = %v, want %v", repairs(), start+1)
	}
	lag := testutil.ToFloat64(driftLag.WithLabelValues(ConnectionKey(testNamespace, newResource(testName).Spec.ConnectionRef)))
	if lag < 90 || lag > 120 {
		t.Fatalf("openbao_drift_lag_seconds = %v, want about 90", lag)
	}
}

// v2DeniedBao answers every read with a 403 that is not a token problem.
type v2DeniedBao struct{ *fakeBao }

func (v2DeniedBao) Read(context.Context, string) (map[string]any, error) {
	return nil, errors.Join(openbao.ErrPermissionDenied, &openbao.ResponseError{StatusCode: 403, Errors: []string{"permission denied"}})
}

func TestV2PermissionDeniedIsNotAbsent(t *testing.T) {
	h := newHarness(t, testAdapter{}, newResource(testName))
	h.conns.client = v2DeniedBao{&fakeBao{store: h.store, ns: litAdmin}}
	if _, err := h.reconcile(); err == nil {
		t.Fatal("want an error so the controller backs off")
	}
	if h.writes() != 0 {
		t.Fatal("a 403 read was treated as absent and created")
	}
	assertCondition(t, h.get(), commonv1alpha1.ConditionSynced, metav1.ConditionFalse, commonv1alpha1.ReasonPermissionDenied)
}

func TestV2ErrorsAreNotPrefixedTwice(t *testing.T) {
	h := newHarness(t, testAdapter{}, newResource(testName))
	h.conns.client = v2FailingWriteBao{&fakeBao{store: h.store, ns: litAdmin}}
	_, err := h.reconcile()
	if err == nil || strings.Count(err.Error(), "sys/policies/acl/p") != 1 {
		t.Fatalf("err = %v, want the path once", err)
	}
}

type v2FailingWriteBao struct{ *fakeBao }

func (v2FailingWriteBao) Write(_ context.Context, path string, _ map[string]any) (map[string]any, error) {
	return nil, errors.New("write " + path + ": openbao: status 500")
}

// v2DroppingBao keeps only the policy key of every write, as a server that ignores a field would.
type v2DroppingBao struct {
	*fakeBao
	activeReads *int
}

func (c v2DroppingBao) Write(ctx context.Context, path string, data map[string]any) (map[string]any, error) {
	return c.fakeBao.Write(ctx, path, map[string]any{keyPolicy: data[keyPolicy]})
}

func (c v2DroppingBao) ActiveNode() openbao.Client {
	*c.activeReads++
	return c.fakeBao
}

type v2SkipReadBack struct{ testAdapter }

func (v2SkipReadBack) SkipReadBack(*testResource) bool { return true }

func TestV2ReadAfterWrite(t *testing.T) {
	obj := newResource(testName)
	obj.Spec.Data["ignored_by_server"] = "x"
	t.Run("unconfirmed write is OutOfSync with backoff", func(t *testing.T) {
		h := newHarness(t, testAdapter{}, obj.DeepCopyObject().(*testResource))
		active := 0
		h.conns.client = v2DroppingBao{fakeBao: &fakeBao{store: h.store, ns: litAdmin}, activeReads: &active}
		res, err := h.reconcile()
		if err != nil {
			t.Fatal(err)
		}
		assertBetween(t, res.RequeueAfter, time.Second, 3*time.Second)
		got := h.get()
		assertCondition(t, got, commonv1alpha1.ConditionReady, metav1.ConditionFalse, commonv1alpha1.ReasonOutOfSync)
		if !strings.Contains(v2Condition(got, commonv1alpha1.ConditionReady).Message, "ignored_by_server") {
			t.Fatalf("message does not name the key: %q", v2Condition(got, commonv1alpha1.ConditionReady).Message)
		}
		if got.Status.Acquired != commonv1alpha1.AcquiredCreated || got.Status.Path != policyPath {
			t.Fatalf("status of the written object not recorded: %+v", got.Status)
		}
		if active != 1 {
			t.Fatalf("read-back used the active node %d times, want 1", active)
		}
	})
	t.Run("read-back can be skipped", func(t *testing.T) {
		h := newHarness(t, v2SkipReadBack{}, obj.DeepCopyObject().(*testResource))
		active := 0
		h.conns.client = v2DroppingBao{fakeBao: &fakeBao{store: h.store, ns: litAdmin}, activeReads: &active}
		h.mustReconcile()
		assertCondition(t, h.get(), commonv1alpha1.ConditionReady, metav1.ConditionTrue, commonv1alpha1.ReasonAvailable)
		if active != 0 {
			t.Fatal("skipped read-back still read")
		}
	})
}

// v2WriteOnceAdapter sends v2BindPass once, and again only for a new spec.data["nonce"].
type v2WriteOnceAdapter struct{ testAdapter }

func (v2WriteOnceAdapter) Fields() FieldSpec {
	return FieldSpec{WriteOnly: []string{keyPassword}, WriteOnce: []string{v2BindPass}, ServerOwned: []string{v2Accessor}}
}

func (a v2WriteOnceAdapter) Desired(ctx context.Context, r Resolver, obj *testResource) (Payload, error) {
	desired, err := a.testAdapter.Desired(ctx, r, obj)
	if err != nil {
		return nil, err
	}
	desired[v2BindPass] = v2Initial
	desired[v2Accessor] = "set-by-server"
	delete(desired, "nonce")
	return desired, nil
}

func (v2WriteOnceAdapter) RotationNonce(obj *testResource) string { return obj.Spec.Data["nonce"] }

// v2LastWrite records the payload of the last write.
type v2LastWrite struct {
	*fakeBao
	last *Payload
}

func (c v2LastWrite) Write(ctx context.Context, path string, data map[string]any) (map[string]any, error) {
	*c.last = maps.Clone(data)
	stored := maps.Clone(data)
	delete(stored, v2BindPass)
	return c.fakeBao.Write(ctx, path, stored)
}

func TestV2WriteOnceAndServerOwned(t *testing.T) {
	h := newHarness(t, v2WriteOnceAdapter{}, newResource(testName))
	var last Payload
	h.conns.client = v2LastWrite{fakeBao: &fakeBao{store: h.store, ns: litAdmin}, last: &last}

	h.mustReconcile()
	if last[v2BindPass] != v2Initial {
		t.Fatalf("create did not send the write-once field: %v", slices.Collect(maps.Keys(last)))
	}
	if _, ok := last[v2Accessor]; ok {
		t.Fatal("server-owned field sent")
	}
	assertCondition(t, h.get(), commonv1alpha1.ConditionReady, metav1.ConditionTrue, commonv1alpha1.ReasonAvailable)

	got := h.get()
	got.Spec.Data[keyPolicy] = v2Changed
	h.update(got)
	h.mustReconcile()
	if _, ok := last[v2BindPass]; ok || last[keyPolicy] != v2Changed {
		t.Fatalf("update re-sent the write-once field: %v", slices.Collect(maps.Keys(last)))
	}

	writes := h.writes()
	h.mustReconcile()
	if h.writes() != writes {
		t.Fatal("steady state wrote")
	}

	got = h.get()
	got.Spec.Data["nonce"] = "rotate-1"
	h.update(got)
	h.mustReconcile()
	if last[v2BindPass] != v2Initial {
		t.Fatal("a new rotation nonce did not re-send the write-once field")
	}
	if h.get().Status.RotationNonce != "rotate-1" {
		t.Fatalf("status.rotationNonce = %q", h.get().Status.RotationNonce)
	}
}

func TestV2WriteBudget(t *testing.T) {
	h := newHarnessWith(t, testAdapter{}, Options{WriteBudgetPerMinute: 1}, newResource(testName), newResource("second"))
	exhausted := func() float64 {
		return testutil.ToFloat64(budgetExhausted.WithLabelValues(ConnectionKey(testNamespace, newResource(testName).Spec.ConnectionRef)))
	}
	start := exhausted()
	for _, name := range []string{testName, "second"} {
		if _, err := h.reconcileName(name); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range []string{testName, "second"} {
		got := h.getName(name)
		got.Spec.Data[keyPolicy] = v2Changed
		h.update(got)
		if _, err := h.reconcileName(name); err != nil {
			t.Fatal(err)
		}
	}
	if h.writes() != 4 || exhausted() != start {
		t.Fatalf("writes = %d, exhausted = %v; creates and spec changes must not wait for the budget", h.writes(), exhausted()-start)
	}

	for _, name := range []string{testName, "second"} {
		budgetTamper(h, name)
	}
	h.drainEvents()
	if _, err := h.reconcileName(testName); err != nil {
		t.Fatal(err)
	}
	res, err := h.reconcileName("second")
	if err != nil {
		t.Fatalf("err = %v, want nil", err)
	}
	if h.writes() != 5 {
		t.Fatalf("writes = %d, want 5: the second drift repair must wait for the budget", h.writes())
	}
	assertBetween(t, res.RequeueAfter, 50*time.Second, 2*time.Minute)
	held := h.getName("second")
	assertCondition(t, held, commonv1alpha1.ConditionSynced, metav1.ConditionFalse, commonv1alpha1.ReasonWriteBudgetExceeded)
	if exhausted() != start+1 {
		t.Fatalf("openbao_write_budget_exhausted_total = %v, want %v", exhausted(), start+1)
	}
	h.drainEvents()
	if _, err := h.reconcileName("second"); err != nil {
		t.Fatal(err)
	}
	if events := h.drainEvents(); len(events) != 0 {
		t.Fatalf("event repeated: %v", events)
	}
	if again := h.getName("second"); again.ResourceVersion != held.ResourceVersion {
		t.Fatal("a retry held back again patched the status; the condition message must not change")
	}
}

func TestWriteBudgetWindow(t *testing.T) {
	now := time.Unix(1000, 0)
	b := newWriteBudget(2)
	b.now = func() time.Time { return now }
	if ok, _ := b.take("s"); !ok {
		t.Fatal("first write refused")
	}
	now = now.Add(20 * time.Second)
	if ok, _ := b.take("s"); !ok {
		t.Fatal("second write refused")
	}
	if ok, _ := b.take("other"); !ok {
		t.Fatal("budgets are per server")
	}
	ok, wait := b.take("s")
	if ok || wait != 40*time.Second {
		t.Fatalf("take = %v, %v; want false, 40s", ok, wait)
	}
	now = now.Add(40 * time.Second)
	if ok, _ := b.take("s"); !ok {
		t.Fatal("window did not slide")
	}
	if ok, _ := newWriteBudget(-1).take("s"); !ok {
		t.Fatal("negative budget limits")
	}
	if newWriteBudget(0).perMinute != DefaultWriteBudgetPerMinute {
		t.Fatal("zero budget is not the default")
	}
}

func v2Observing(obj *testResource) *testResource {
	obj.Spec.ManagementPolicy = commonv1alpha1.ManagementPolicyObserve
	return obj
}

func TestV2ObserveOnly(t *testing.T) {
	t.Run("drift is reported, never written", func(t *testing.T) {
		h := newHarness(t, testAdapter{}, deletable(v2Observing(newResource(testName))))
		h.store.putPolicy(Payload{keyPolicy: "other"})
		h.mustReconcile()
		got := h.get()
		if h.writes() != 0 || len(got.Finalizers) != 0 {
			t.Fatalf("observe wrote %d times, finalizers %v", h.writes(), got.Finalizers)
		}
		assertCondition(t, got, commonv1alpha1.ConditionReady, metav1.ConditionTrue, commonv1alpha1.ReasonObserved)
		assertCondition(t, got, commonv1alpha1.ConditionSynced, metav1.ConditionFalse, commonv1alpha1.ReasonDrifted)
		if events := h.drainEvents(); len(events) != 1 || !strings.Contains(events[0], keyPolicy) || strings.Contains(events[0], "other") {
			t.Fatalf("events = %v, want one naming the key and not the value", events)
		}
		h.mustReconcile()
		if events := h.drainEvents(); len(events) != 0 {
			t.Fatalf("drift event repeated: %v", events)
		}
	})
	t.Run("in sync is Observed", func(t *testing.T) {
		h := newHarness(t, testAdapter{}, v2Observing(newResource(testName)))
		h.store.putPolicy(Payload{keyPolicy: testPolicyText})
		h.mustReconcile()
		assertCondition(t, h.get(), commonv1alpha1.ConditionSynced, metav1.ConditionTrue, commonv1alpha1.ReasonObserved)
	})
	t.Run("absent is NotFound", func(t *testing.T) {
		h := newHarness(t, testAdapter{}, v2Observing(newResource(testName)))
		h.mustReconcile()
		if h.writes() != 0 {
			t.Fatal("observe created")
		}
		assertCondition(t, h.get(), commonv1alpha1.ConditionReady, metav1.ConditionFalse, commonv1alpha1.ReasonNotFound)
	})
	t.Run("switching to Observe drops the finalizer and deletes nothing", func(t *testing.T) {
		h := newHarness(t, testAdapter{}, deletable(newResource(testName)))
		h.mustReconcile()
		got := h.get()
		got.Spec.ManagementPolicy = commonv1alpha1.ManagementPolicyObserve
		h.update(got)
		h.mustReconcile()
		if err := h.k8s.Delete(context.Background(), h.get()); err != nil {
			t.Fatal(err)
		}
		if _, ok := h.store.get(policyPath); !ok || h.store.deleteCount() != 0 {
			t.Fatal("observe-only resource deleted its object")
		}
	})
}

func v2Paused(obj *testResource) *testResource {
	obj.Annotations = map[string]string{commonv1alpha1.AnnotationPaused: annotationTrue}
	return obj
}

func TestV2Pause(t *testing.T) {
	t.Run("annotation on the resource", func(t *testing.T) {
		h := newHarness(t, testAdapter{}, v2Paused(newResource(testName)))
		res := h.mustReconcile()
		reads, writes := h.store.counts()
		if reads != 0 || writes != 0 || res.RequeueAfter != 0 {
			t.Fatalf("paused resource read %d, wrote %d, requeue %v", reads, writes, res.RequeueAfter)
		}
		assertCondition(t, h.get(), commonv1alpha1.ConditionSynced, metav1.ConditionFalse, commonv1alpha1.ReasonPaused)
	})
	t.Run("annotation on the Namespace", func(t *testing.T) {
		ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: testNamespace,
			Annotations: map[string]string{commonv1alpha1.AnnotationPaused: annotationTrue}}}
		h := newHarness(t, testAdapter{}, newResource(testName), ns)
		res := h.mustReconcile()
		if h.writes() != 0 {
			t.Fatal("wrote while the namespace is paused")
		}
		assertBetween(t, res.RequeueAfter, 48*time.Second, 72*time.Second)
	})
	t.Run("paused server", func(t *testing.T) {
		h := newHarness(t, testAdapter{}, newResource(testName))
		h.r.conns = &v2ServerConns{fakeConnections: h.conns, keys: map[string]string{
			newResource(testName).Spec.ConnectionRef.Name: v2ServerOne}, paused: true}
		h.mustReconcile()
		if h.writes() != 0 {
			t.Fatal("wrote while the server is paused")
		}
		assertCondition(t, h.get(), commonv1alpha1.ConditionSynced, metav1.ConditionFalse, commonv1alpha1.ReasonPaused)
	})
	t.Run("deletion keeps the finalizer", func(t *testing.T) {
		h := newHarness(t, testAdapter{}, deletable(newResource(testName)))
		h.mustReconcile()
		got := v2Paused(h.get())
		if err := h.k8s.Update(context.Background(), got); err != nil {
			t.Fatal(err)
		}
		if err := h.k8s.Delete(context.Background(), h.get()); err != nil {
			t.Fatal(err)
		}
		h.mustReconcile()
		if !controllerutil.ContainsFinalizer(h.get(), DefaultFinalizer) || h.store.deleteCount() != 0 {
			t.Fatal("paused deletion went ahead")
		}
	})
}

type v2SingletonAdapter struct{ testAdapter }

func (v2SingletonAdapter) Singleton() bool { return true }

func TestV2SingletonIsAdopted(t *testing.T) {
	h := newHarness(t, v2SingletonAdapter{}, newResource(testName))
	h.store.putPolicy(Payload{keyPolicy: "server default"})
	h.mustReconcile()
	got := h.get()
	if got.Status.Acquired != commonv1alpha1.AcquiredAdopted {
		t.Fatalf("acquired = %q, want Adopted", got.Status.Acquired)
	}
	if stored, _ := h.store.get(policyPath); stored[keyPolicy] != testPolicyText {
		t.Fatal("singleton not converged")
	}
}

func TestAnnotationChangedPredicate(t *testing.T) {
	p := annotationChanged(commonv1alpha1.AnnotationPaused)
	old := newResource(testName)
	changed := v2Paused(newResource(testName))
	if !p.Update(event.UpdateEvent{ObjectOld: old, ObjectNew: changed}) {
		t.Fatal("pause annotation change filtered")
	}
	other := newResource(testName)
	other.Annotations = map[string]string{"unrelated": "x"}
	if p.Update(event.UpdateEvent{ObjectOld: old, ObjectNew: other}) {
		t.Fatal("unrelated annotation passed")
	}
}

func TestV2Attribution(t *testing.T) {
	h := newHarness(t, testAdapter{}, newResource(testName))
	if got, want := h.r.attribution(newResource(testName)), testGV.Group+"/"+litTestResource+"/"+testNamespace+"/"+testName; got != want {
		t.Fatalf("attribution = %q, want %q", got, want)
	}
}

func v2ReadySeries(name string) int {
	ch := make(chan prometheus.Metric, 1000)
	resourceReady.Collect(ch)
	close(ch)
	n := 0
	for m := range ch {
		var out dto.Metric
		if err := m.Write(&out); err != nil {
			continue
		}
		for _, l := range out.GetLabel() {
			if l.GetName() == "name" && l.GetValue() == name {
				n++
			}
		}
	}
	return n
}

func TestV2ReadyGauge(t *testing.T) {
	const name = "gauge-resource"
	h := newHarness(t, testAdapter{}, newResource(name))
	gauge := func(reason string) float64 {
		return testutil.ToFloat64(resourceReady.WithLabelValues(litTestResource, testNamespace, name, reason))
	}
	if _, err := h.reconcileName(name); err != nil {
		t.Fatal(err)
	}
	if gauge(commonv1alpha1.ReasonAvailable) != 1 {
		t.Fatal("ready resource not reported as 1")
	}
	h.conns.setErr(ErrConnectionNotReady)
	if _, err := h.reconcileName(name); err != nil {
		t.Fatal(err)
	}
	if gauge(commonv1alpha1.ReasonConnectionNotReady) != 0 || v2ReadySeries(name) != 1 {
		t.Fatalf("want one series at 0, have %d", v2ReadySeries(name))
	}
	if err := h.k8s.Delete(context.Background(), h.getName(name)); err != nil {
		t.Fatal(err)
	}
	if _, err := h.reconcileName(name); err != nil {
		t.Fatal(err)
	}
	if v2ReadySeries(name) != 0 {
		t.Fatal("series kept after the resource was deleted")
	}
}

func TestV2TerminatingNamespaceReleasesFinalizer(t *testing.T) {
	ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: testNamespace, Finalizers: []string{"test"}}}
	h := newHarness(t, testAdapter{}, deletable(newResource(testName)), ns)
	h.mustReconcile()
	h.conns.setErr(ErrConnectionNotReady)
	if err := h.k8s.Delete(context.Background(), h.get()); err != nil {
		t.Fatal(err)
	}
	if _, err := h.reconcile(); err == nil {
		t.Fatal("a live Namespace must keep the finalizer and report CleanupBlocked")
	}
	if err := h.k8s.Delete(context.Background(), ns); err != nil {
		t.Fatal(err)
	}
	h.mustReconcile()
	if err := h.k8s.Get(context.Background(), client.ObjectKey{Namespace: testNamespace, Name: testName}, &testResource{}); err == nil {
		t.Fatal("finalizer kept in a terminating Namespace whose connection is gone")
	}
	if _, ok := h.store.get(policyPath); !ok {
		t.Fatal("the object must stay in OpenBao")
	}
}

func TestV2ObserveSkipsMissingWriteOnlySecrets(t *testing.T) {
	obj := v2Observing(newResource(testName))
	obj.Spec.PasswordSecretRef = &commonv1alpha1.SecretKeySelector{Name: "not-created-yet", Key: keyPassword}
	h := newHarness(t, testAdapter{}, obj)
	h.store.putPolicy(Payload{keyPolicy: testPolicyText})
	h.mustReconcile()
	got := h.get()
	assertCondition(t, got, commonv1alpha1.ConditionReady, metav1.ConditionTrue, commonv1alpha1.ReasonObserved)
	assertCondition(t, got, commonv1alpha1.ConditionSynced, metav1.ConditionTrue, commonv1alpha1.ReasonObserved)
	if got.Status.WriteOnlyHash != "" {
		t.Fatal("observe-only resource recorded a write-only hash")
	}
}

type v2SharedPathAdapter struct{ v2MarkerAdapter }

func (v2SharedPathAdapter) Path(*testResource) string { return v2SharedPath }

func (v2SharedPathAdapter) SharedPath() bool { return true }

func TestV2SharedPathKindsDoNotConflict(t *testing.T) {
	now := time.Now()
	older := v2Owned(v2At(v2Older, now.Add(-time.Hour), "u-old"), v2SharedAbs)
	newer := v2At(testName, now, "u-new")
	newer.Spec.CreationPolicy = commonv1alpha1.CreationPolicyAdopt
	h := newHarness(t, v2SharedPathAdapter{}, older, newer)
	h.store.objects[v2SharedAbs] = Payload{keyPolicy: "edges"}
	h.mustReconcile()
	assertCondition(t, h.get(), commonv1alpha1.ConditionReady, metav1.ConditionTrue, commonv1alpha1.ReasonAvailable)
	if stored, _ := h.store.get(v2SharedAbs); stored["metadata"] != nil {
		t.Fatalf("shared-path kind wrote an ownership marker: %v", stored)
	}
}

// v2IDPathAdapter creates at a collection path and addresses the object by its server-assigned id afterwards.
type v2IDPathAdapter struct{ testAdapter }

func (v2IDPathAdapter) Path(obj *testResource) string {
	if obj.Status.ID == "" {
		return "identity/entity-alias"
	}
	return "identity/entity-alias/id/" + obj.Status.ID
}

func (v2IDPathAdapter) WriteStatus(_ context.Context, _ openbao.Client, obj *testResource, _ Payload) error {
	obj.Status.ID = "alias-1"
	return nil
}

func TestV2PathRecordedAfterStatusWriter(t *testing.T) {
	h := newHarness(t, v2IDPathAdapter{}, newResource(testName))
	h.mustReconcile()
	if got := h.get().Status.Path; got != litAdmin+"/identity/entity-alias/id/alias-1" {
		t.Fatalf("status.path = %q, want the id path", got)
	}
}

func TestConnectionIndexAndReadyPredicate(t *testing.T) {
	obj := newResource(testName)
	if got := ConnectionIndexFunc(obj); !slices.Equal(got, []string{"OpenBaoConnection/" + testNamespace + "/bao"}) {
		t.Fatalf("index = %v", got)
	}
	obj.Spec.ConnectionRef.Kind = commonv1alpha1.ConnectionKindCluster
	if got := ConnectionIndexFunc(obj); !slices.Equal(got, []string{"ClusterOpenBaoConnection/bao"}) {
		t.Fatalf("cluster index = %v", got)
	}

	p := connectionReadyChanged()
	notReady := &corev1alpha1.OpenBaoConnection{}
	ready := &corev1alpha1.OpenBaoConnection{}
	meta.SetStatusCondition(&ready.Status.Conditions, metav1.Condition{
		Type: commonv1alpha1.ConditionReady, Status: metav1.ConditionTrue, Reason: "Ready"})
	if !p.Update(event.UpdateEvent{ObjectOld: notReady, ObjectNew: ready}) {
		t.Fatal("Ready transition filtered")
	}
	if p.Update(event.UpdateEvent{ObjectOld: ready, ObjectNew: ready.DeepCopy()}) {
		t.Fatal("update without a Ready change passed")
	}
	if p.Create(event.CreateEvent{Object: ready}) {
		t.Fatal("create passed")
	}
}

// TestConnectionPredicatePassesReadyToReadyChanges: a connection that stays Ready but points somewhere else, has been
// re-probed for a new spec, or is being deleted re-queues its resources.
func TestConnectionPredicatePassesReadyToReadyChanges(t *testing.T) {
	p := connectionReadyChanged()
	ready := &corev1alpha1.OpenBaoConnection{}
	ready.Generation = 1
	ready.Status.ObservedGeneration = 1
	meta.SetStatusCondition(&ready.Status.Conditions, metav1.Condition{
		Type: commonv1alpha1.ConditionReady, Status: metav1.ConditionTrue, Reason: "Ready", ObservedGeneration: 1})
	for name, edit := range map[string]func(*corev1alpha1.OpenBaoConnection){
		"spec change": func(c *corev1alpha1.OpenBaoConnection) {
			c.Spec.Namespace = "team-b"
			c.Generation = 2
		},
		"new spec probed": func(c *corev1alpha1.OpenBaoConnection) { c.Status.ObservedGeneration = 2 },
		"deletion":        func(c *corev1alpha1.OpenBaoConnection) { c.DeletionTimestamp = &metav1.Time{Time: time.Now()} },
	} {
		changed := ready.DeepCopy()
		edit(changed)
		if !p.Update(event.UpdateEvent{ObjectOld: ready, ObjectNew: changed}) {
			t.Errorf("%s filtered", name)
		}
	}
	if !p.Delete(event.DeleteEvent{Object: ready}) {
		t.Error("delete filtered")
	}
	probed := ready.DeepCopy()
	probed.Status.LastProbeTime = &metav1.Time{Time: time.Now()}
	if p.Update(event.UpdateEvent{ObjectOld: ready, ObjectNew: probed}) {
		t.Error("a probe that changed nothing passed")
	}
}

type v2MissingPluginWriter struct{ testAdapter }

func (v2MissingPluginWriter) Write(context.Context, openbao.Client, *testResource, Payload, bool) (Payload, error) {
	return nil, fmt.Errorf("enable sys/auth/ldap: %w", ErrPluginNotRegistered)
}

func TestV2PluginNotRegistered(t *testing.T) {
	h := newHarness(t, v2MissingPluginWriter{}, newResource(testName))
	res, err := h.reconcile()
	if err != nil {
		t.Fatalf("err = %v, want nil so the controller does not back off", err)
	}
	assertBetween(t, res.RequeueAfter, 48*time.Minute, 72*time.Minute)
	assertCondition(t, h.get(), commonv1alpha1.ConditionReady, metav1.ConditionFalse, commonv1alpha1.ReasonPluginNotRegistered)
}
