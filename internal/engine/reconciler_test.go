package engine

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/tools/events"
	"k8s.io/client-go/util/workqueue"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	commonv1alpha1 "github.com/alexandrust88/openbao-config-operator/api/common/v1alpha1"
	"github.com/alexandrust88/openbao-config-operator/internal/openbao"
)

var testGV = schema.GroupVersion{Group: "test.openbaoconfig.io", Version: "v1alpha1"}

type testSpec struct {
	commonv1alpha1.ResourceSpec `json:",inline"`
	Data                        map[string]string                 `json:"data,omitempty"`
	PasswordSecretRef           *commonv1alpha1.SecretKeySelector `json:"passwordSecretRef,omitempty"`
}

type testResource struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`
	Spec              testSpec                      `json:"spec"`
	Status            commonv1alpha1.ResourceStatus `json:"status,omitempty"`
}

func (o *testResource) GetResourceSpec() *commonv1alpha1.ResourceSpec     { return &o.Spec.ResourceSpec }
func (o *testResource) GetResourceStatus() *commonv1alpha1.ResourceStatus { return &o.Status }

func (o *testResource) DeepCopyObject() runtime.Object {
	out := &testResource{TypeMeta: o.TypeMeta}
	o.DeepCopyInto(&out.ObjectMeta)
	o.Spec.DeepCopyInto(&out.Spec.ResourceSpec)
	out.Spec.Data = maps.Clone(o.Spec.Data)
	if o.Spec.PasswordSecretRef != nil {
		ref := *o.Spec.PasswordSecretRef
		out.Spec.PasswordSecretRef = &ref
	}
	o.Status.DeepCopyInto(&out.Status)
	return out
}

type testResourceList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []testResource `json:"items"`
}

func (l *testResourceList) DeepCopyObject() runtime.Object {
	out := &testResourceList{TypeMeta: l.TypeMeta}
	l.DeepCopyInto(&out.ListMeta)
	for i := range l.Items {
		out.Items = append(out.Items, *l.Items[i].DeepCopyObject().(*testResource))
	}
	return out
}

type testAdapter struct{}

func (testAdapter) Path(obj *testResource) string { return "sys/policies/acl/" + obj.Name }

func (testAdapter) Desired(ctx context.Context, r Resolver, obj *testResource) (Payload, error) {
	desired := Payload{}
	for k, v := range obj.Spec.Data {
		desired[k] = v
	}
	if ref := obj.Spec.PasswordSecretRef; ref != nil {
		value, err := r.SecretValue(ctx, obj.Namespace, *ref)
		if err != nil {
			return nil, err
		}
		desired[keyPassword] = string(value)
	}
	return desired, nil
}

func (testAdapter) Fields() FieldSpec { return FieldSpec{WriteOnly: []string{keyPassword}} }

func (testAdapter) SecretRefs(obj *testResource) []string {
	if obj.Spec.PasswordSecretRef == nil {
		return nil
	}
	return []string{obj.Spec.PasswordSecretRef.Name}
}

type fakeStore struct {
	mu                     sync.Mutex
	objects                map[string]Payload
	response               Payload
	reads, writes, deletes int
}

func (s *fakeStore) get(path string) (Payload, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	p, ok := s.objects[path]
	return maps.Clone(p), ok
}

// putPolicy stores p at policyPath, as an out-of-band change would.
func (s *fakeStore) putPolicy(p Payload) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.objects[policyPath] = maps.Clone(p)
}

func (s *fakeStore) deleteCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.deletes
}

func (s *fakeStore) counts() (reads, writes int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.reads, s.writes
}

type fakeBao struct {
	store *fakeStore
	ns    string
}

func (c *fakeBao) Read(_ context.Context, path string) (map[string]any, error) {
	c.store.mu.Lock()
	defer c.store.mu.Unlock()
	c.store.reads++
	p, ok := c.store.objects[joinPath(c.ns, path)]
	if !ok {
		return nil, openbao.ErrNotFound
	}
	return maps.Clone(p), nil
}

func (c *fakeBao) Write(_ context.Context, path string, data map[string]any) (map[string]any, error) {
	c.store.mu.Lock()
	defer c.store.mu.Unlock()
	c.store.writes++
	c.store.objects[joinPath(c.ns, path)] = maps.Clone(data)
	return maps.Clone(c.store.response), nil
}

func (c *fakeBao) Delete(_ context.Context, path string) error {
	c.store.mu.Lock()
	defer c.store.mu.Unlock()
	c.store.deletes++
	key := joinPath(c.ns, path)
	if _, ok := c.store.objects[key]; !ok {
		return openbao.ErrNotFound
	}
	delete(c.store.objects, key)
	return nil
}

func (c *fakeBao) List(context.Context, string) ([]string, error) { return nil, openbao.ErrNotFound }

func (c *fakeBao) WithNamespace(ns string) openbao.Client {
	return &fakeBao{store: c.store, ns: joinPath(c.ns, ns)}
}

func (c *fakeBao) Namespace() string { return c.ns }

type fakeConnections struct {
	mu     sync.Mutex
	client openbao.Client
	err    error
}

func (f *fakeConnections) For(context.Context, string, commonv1alpha1.ConnectionReference) (openbao.Client, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return nil, f.err
	}
	return f.client, nil
}

func (f *fakeConnections) setErr(err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.err = err
}

type harness struct {
	t        *testing.T
	r        *Reconciler[*testResource]
	k8s      client.Client
	store    *fakeStore
	conns    *fakeConnections
	recorder *events.FakeRecorder
}

type harnessAdapter interface {
	Adapter[*testResource]
	SecretReferencer[*testResource]
}

func newHarness(t *testing.T, adapter harnessAdapter, objs ...client.Object) *harness {
	t.Helper()
	return newHarnessWith(t, adapter, Options{}, objs...)
}

func newHarnessWith(t *testing.T, adapter harnessAdapter, opts Options, objs ...client.Object) *harness {
	t.Helper()
	scheme := runtime.NewScheme()
	if err := corev1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	scheme.AddKnownTypes(testGV, &testResource{}, &testResourceList{})
	metav1.AddToGroupVersion(scheme, testGV)
	k8s := fake.NewClientBuilder().
		WithScheme(scheme).
		WithStatusSubresource(&testResource{}).
		WithIndex(&testResource{}, SecretRefIndex, SecretRefsIndexFunc[*testResource](adapter)).
		WithIndex(&testResource{}, PathIndex, PathIndexFunc).
		WithObjects(objs...).
		Build()
	store := &fakeStore{objects: map[string]Payload{}}
	conns := &fakeConnections{client: &fakeBao{store: store, ns: litAdmin}}
	recorder := events.NewFakeRecorder(100)
	r := New(k8s, scheme, recorder, conns, adapter, func() *testResource { return &testResource{} }, opts)
	r.shared.addKind(testGV.WithKind(litTestResource))
	return &harness{t: t, r: r, k8s: k8s, store: store, conns: conns, recorder: recorder}
}

func newResource(name string) *testResource {
	return &testResource{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: testNamespace, Generation: 1},
		Spec: testSpec{
			ResourceSpec: commonv1alpha1.ResourceSpec{ConnectionRef: commonv1alpha1.ConnectionReference{Name: "bao"}},
			Data:         map[string]string{keyPolicy: testPolicyText},
		},
	}
}

// deletable opts obj in to deletionPolicy Delete.
func deletable(obj *testResource) *testResource {
	obj.Spec.DeletionPolicy = commonv1alpha1.DeletionPolicyDelete
	return obj
}

func (h *harness) reconcile() (ctrl.Result, error) {
	h.t.Helper()
	return h.r.Reconcile(context.Background(), ctrl.Request{NamespacedName: client.ObjectKey{Namespace: testNamespace, Name: testName}})
}

func (h *harness) mustReconcile() ctrl.Result {
	h.t.Helper()
	res, err := h.reconcile()
	if err != nil {
		h.t.Fatalf("reconcile: %v", err)
	}
	return res
}

func (h *harness) get() *testResource {
	h.t.Helper()
	obj := &testResource{}
	if err := h.k8s.Get(context.Background(), client.ObjectKey{Namespace: testNamespace, Name: testName}, obj); err != nil {
		h.t.Fatalf("get: %v", err)
	}
	return obj
}

func (h *harness) update(obj *testResource) {
	h.t.Helper()
	obj.Generation++
	if err := h.k8s.Update(context.Background(), obj); err != nil {
		h.t.Fatalf("update: %v", err)
	}
}

func (h *harness) drainEvents() []string {
	var out []string
	for {
		select {
		case e := <-h.recorder.Events:
			out = append(out, e)
		default:
			return out
		}
	}
}

func assertCondition(t *testing.T, obj *testResource, conditionType string, status metav1.ConditionStatus, reason string) {
	t.Helper()
	c := meta.FindStatusCondition(obj.Status.Conditions, conditionType)
	if c == nil || c.Status != status || c.Reason != reason {
		t.Fatalf("condition %s = %+v, want %s/%s", conditionType, c, status, reason)
	}
}

const (
	testName   = "p"
	policyPath = "admin/sys/policies/acl/" + testName
)

func TestReconcileCreateAndSteadyState(t *testing.T) {
	h := newHarness(t, testAdapter{}, deletable(newResource(testName)))

	res := h.mustReconcile()
	if res.RequeueAfter < 48*time.Minute || res.RequeueAfter > 72*time.Minute {
		t.Fatalf("RequeueAfter = %v, want the standard tier 1h +-20%%", res.RequeueAfter)
	}
	if got, ok := h.store.get(policyPath); !ok || got[keyPolicy] != testPolicyText {
		t.Fatalf("store = %v, %v", got, ok)
	}
	obj := h.get()
	assertCondition(t, obj, commonv1alpha1.ConditionReady, metav1.ConditionTrue, commonv1alpha1.ReasonAvailable)
	assertCondition(t, obj, commonv1alpha1.ConditionSynced, metav1.ConditionTrue, commonv1alpha1.ReasonReconcileSuccess)
	if obj.Status.Path != policyPath || obj.Status.Namespace != litAdmin || obj.Status.Acquired != commonv1alpha1.AcquiredCreated ||
		obj.Status.ObservedGeneration != 1 {
		t.Fatalf("status = %+v", obj.Status)
	}
	if len(obj.Finalizers) != 1 || obj.Finalizers[0] != DefaultFinalizer {
		t.Fatalf("finalizers = %v", obj.Finalizers)
	}
	if ev := h.drainEvents(); len(ev) != 1 || !strings.Contains(ev[0], "Created") {
		t.Fatalf("events = %v", ev)
	}

	_, writesBefore := h.store.counts()
	h.mustReconcile()
	h.mustReconcile()
	_, writesAfter := h.store.counts()
	if writesAfter != writesBefore {
		t.Fatalf("steady state wrote %d times", writesAfter-writesBefore)
	}
	if after := h.get(); after.ResourceVersion != obj.ResourceVersion {
		t.Fatalf("steady state updated the object: %s -> %s", obj.ResourceVersion, after.ResourceVersion)
	}
	if ev := h.drainEvents(); len(ev) != 0 {
		t.Fatalf("steady state events = %v", ev)
	}
}

func TestReconcileRepairsDrift(t *testing.T) {
	h := newHarness(t, testAdapter{}, newResource(testName))
	h.mustReconcile()
	h.drainEvents()

	h.store.putPolicy(Payload{keyPolicy: "changed", "secret_value_never_logged": "x"})
	h.mustReconcile()

	if got, _ := h.store.get(policyPath); got[keyPolicy] != testPolicyText {
		t.Fatalf("drift not repaired: %v", got)
	}
	ev := h.drainEvents()
	if len(ev) != 1 || !strings.Contains(ev[0], "Updated") || !strings.Contains(ev[0], keyPolicy) ||
		strings.Contains(ev[0], "changed") || strings.Contains(ev[0], "path \"*\"") {
		t.Fatalf("events = %v", ev)
	}
}

func TestReconcileCreatePolicyConflict(t *testing.T) {
	obj := newResource(testName)
	obj.Spec.CreationPolicy = commonv1alpha1.CreationPolicyCreate
	h := newHarness(t, testAdapter{}, obj)
	h.store.putPolicy(Payload{keyPolicy: "theirs"})

	res := h.mustReconcile()
	if res.RequeueAfter == 0 {
		t.Fatal("conflict did not requeue")
	}
	if _, writes := h.store.counts(); writes != 0 {
		t.Fatalf("conflict wrote %d times", writes)
	}
	got := h.get()
	assertCondition(t, got, commonv1alpha1.ConditionSynced, metav1.ConditionFalse, commonv1alpha1.ReasonConflict)
	assertCondition(t, got, commonv1alpha1.ConditionReady, metav1.ConditionFalse, commonv1alpha1.ReasonConflict)
	if got.Status.Acquired != "" || got.Status.Path != "" {
		t.Fatalf("conflict recorded ownership: %+v", got.Status)
	}
}

func TestReconcileAdopt(t *testing.T) {
	t.Run("absent object is not created", func(t *testing.T) {
		obj := newResource(testName)
		obj.Spec.CreationPolicy = commonv1alpha1.CreationPolicyAdopt
		h := newHarness(t, testAdapter{}, obj)
		if res := h.mustReconcile(); res.RequeueAfter == 0 {
			t.Fatal("did not requeue")
		}
		if _, writes := h.store.counts(); writes != 0 {
			t.Fatalf("wrote %d times", writes)
		}
		assertCondition(t, h.get(), commonv1alpha1.ConditionSynced, metav1.ConditionFalse, commonv1alpha1.ReasonNotFound)
	})
	t.Run("existing object is adopted", func(t *testing.T) {
		obj := newResource(testName)
		obj.Spec.CreationPolicy = commonv1alpha1.CreationPolicyAdopt
		h := newHarness(t, testAdapter{}, obj)
		h.store.putPolicy(Payload{keyPolicy: testPolicyText})
		h.mustReconcile()
		if _, writes := h.store.counts(); writes != 0 {
			t.Fatalf("adopting a matching object wrote %d times", writes)
		}
		if got := h.get(); got.Status.Acquired != commonv1alpha1.AcquiredAdopted {
			t.Fatalf("acquired = %q", got.Status.Acquired)
		}
	})
}

func TestReconcileWriteOnlyHashUsesOptionsKey(t *testing.T) {
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: credsSecret, Namespace: testNamespace},
		Data:       map[string][]byte{keyPassword: []byte("one")},
	}
	obj := newResource(testName)
	obj.Spec.PasswordSecretRef = &commonv1alpha1.SecretKeySelector{Name: credsSecret, Key: keyPassword}
	h := newHarness(t, testAdapter{}, obj, secret)
	key := []byte("0123456789abcdef0123456789abcdef")
	h.r = New(h.k8s, h.r.scheme, h.recorder, h.conns, h.r.adapter, h.r.newObj, Options{HashKey: key})

	h.mustReconcile()
	desired := Payload{keyPolicy: testPolicyText, keyPassword: "one"}
	got := h.get()
	want := WriteOnlyHash(ObjectHashKey(key, got.UID, testGV.WithKind(litTestResource).GroupKind()), desired, testAdapter{}.Fields())
	if got.Status.WriteOnlyHash != want {
		t.Fatalf("writeOnlyHash = %q, want the HMAC under the resource's key derived from Options.HashKey %q",
			got.Status.WriteOnlyHash, want)
	}
}

// TestWriteOnlyHashDiffersAcrossResources: two resources with the same write-only value get different hashes, so a
// tenant cannot hash a guess through its own resource and compare it with another resource's status.
func TestWriteOnlyHashDiffersAcrossResources(t *testing.T) {
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: credsSecret, Namespace: testNamespace},
		Data:       map[string][]byte{keyPassword: []byte("Summer2026!")},
	}
	victim, probe := v2At(testName, time.Now(), "u-victim"), v2At("probe", time.Now(), "u-probe")
	probe.Spec.Data = map[string]string{keyPolicy: "probe"}
	for _, obj := range []*testResource{victim, probe} {
		obj.Spec.PasswordSecretRef = &commonv1alpha1.SecretKeySelector{Name: credsSecret, Key: keyPassword}
	}
	h := newHarnessWith(t, testAdapter{}, Options{HashKey: []byte("0123456789abcdef0123456789abcdef")}, victim, probe, secret)
	for _, name := range []string{testName, "probe"} {
		if _, err := h.reconcileName(name); err != nil {
			t.Fatal(err)
		}
	}
	a, b := h.getName(testName).Status.WriteOnlyHash, h.getName("probe").Status.WriteOnlyHash
	if a == "" || a == b {
		t.Fatalf("writeOnlyHash of two resources with the same password: %q and %q, want different", a, b)
	}
}

func TestReconcileWriteOnlyHashForcesWrite(t *testing.T) {
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: credsSecret, Namespace: testNamespace},
		Data:       map[string][]byte{keyPassword: []byte("one")},
	}
	obj := newResource(testName)
	obj.Spec.PasswordSecretRef = &commonv1alpha1.SecretKeySelector{Name: credsSecret, Key: keyPassword}
	h := newHarness(t, testAdapter{}, obj, secret)

	h.mustReconcile()
	hash := h.get().Status.WriteOnlyHash
	if hash == "" {
		t.Fatal("writeOnlyHash not recorded")
	}
	h.mustReconcile()
	if _, writes := h.store.counts(); writes != 1 {
		t.Fatalf("writes = %d before rotation, want 1", writes)
	}
	h.drainEvents()

	secret.Data[keyPassword] = []byte("two")
	if err := h.k8s.Update(context.Background(), secret); err != nil {
		t.Fatal(err)
	}
	h.mustReconcile()
	if _, writes := h.store.counts(); writes != 2 {
		t.Fatalf("writes = %d after rotation, want 2", writes)
	}
	if got, _ := h.store.get(policyPath); got[keyPassword] != "two" {
		t.Fatal("rotated password not written")
	}
	if h.get().Status.WriteOnlyHash == hash {
		t.Fatal("writeOnlyHash unchanged after rotation")
	}
	for _, e := range h.drainEvents() {
		if strings.Contains(e, "two") || strings.Contains(e, "one") {
			t.Fatalf("event leaks a secret value: %q", e)
		}
	}
}

func TestReconcileMissingSecretIsReferenceNotReady(t *testing.T) {
	obj := newResource(testName)
	obj.Spec.PasswordSecretRef = &commonv1alpha1.SecretKeySelector{Name: "missing", Key: keyPassword}
	h := newHarness(t, testAdapter{}, obj)
	res, err := h.reconcile()
	if err != nil || res.RequeueAfter == 0 {
		t.Fatalf("reconcile = %+v, %v; want RequeueAfter with nil error", res, err)
	}
	assertCondition(t, h.get(), commonv1alpha1.ConditionSynced, metav1.ConditionFalse, commonv1alpha1.ReasonReferenceNotReady)
}

func TestReconcileDriftDetectionDisabledSkipsRead(t *testing.T) {
	obj := newResource(testName)
	obj.Spec.DriftDetection = commonv1alpha1.DriftDetectionDisabled
	h := newHarness(t, testAdapter{}, obj)
	if res := h.mustReconcile(); res.RequeueAfter != 0 {
		t.Fatalf("RequeueAfter = %v, want none", res.RequeueAfter)
	}
	readsBefore, _ := h.store.counts()
	h.mustReconcile()
	if reads, _ := h.store.counts(); reads != readsBefore {
		t.Fatal("unchanged resource with drift detection disabled was read")
	}

	got := h.get()
	got.Spec.Data[keyPolicy] = "new"
	h.update(got)
	h.mustReconcile()
	if stored, _ := h.store.get(policyPath); stored[keyPolicy] != "new" {
		t.Fatal("spec change not written")
	}
}

func TestReconcileConnectionNotReady(t *testing.T) {
	h := newHarness(t, testAdapter{}, deletable(newResource(testName)))
	h.conns.setErr(ErrConnectionNotReady)
	res, err := h.reconcile()
	if err != nil || res.RequeueAfter == 0 {
		t.Fatalf("reconcile = %+v, %v; want RequeueAfter with nil error", res, err)
	}
	got := h.get()
	assertCondition(t, got, commonv1alpha1.ConditionSynced, metav1.ConditionFalse, commonv1alpha1.ReasonConnectionNotReady)
	if len(got.Finalizers) != 0 {
		t.Fatalf("finalizer added before the connection was ready: %v", got.Finalizers)
	}

	h.conns.setErr(ErrConnectionDenied)
	if _, err := h.reconcile(); err != nil {
		t.Fatal(err)
	}
	assertCondition(t, h.get(), commonv1alpha1.ConditionSynced, metav1.ConditionFalse, commonv1alpha1.ReasonConnectionDenied)
}

func TestReconcileDeletion(t *testing.T) {
	t.Run("delete policy removes the object in a sub-namespace", func(t *testing.T) {
		obj := newResource(testName)
		obj.Spec.Namespace = "team"
		h := newHarness(t, testAdapter{}, deletable(obj))
		h.mustReconcile()
		const path = "admin/team/sys/policies/acl/p"
		if got := h.get(); got.Status.Path != path || got.Status.Namespace != "admin/team" {
			t.Fatalf("status = %+v", got.Status)
		}
		if err := h.k8s.Delete(context.Background(), h.get()); err != nil {
			t.Fatal(err)
		}
		h.mustReconcile()
		if _, ok := h.store.get(path); ok {
			t.Fatal("object not deleted from openbao")
		}
		if err := h.k8s.Get(context.Background(), client.ObjectKey{Namespace: testNamespace, Name: testName}, &testResource{}); err == nil {
			t.Fatal("finalizer not removed")
		}
	})
	t.Run("already deleted in openbao is success", func(t *testing.T) {
		h := newHarness(t, testAdapter{}, deletable(newResource(testName)))
		h.mustReconcile()
		h.store.mu.Lock()
		delete(h.store.objects, policyPath)
		h.store.mu.Unlock()
		if err := h.k8s.Delete(context.Background(), h.get()); err != nil {
			t.Fatal(err)
		}
		h.mustReconcile()
	})
	t.Run("orphan keeps the object", func(t *testing.T) {
		h := newHarness(t, testAdapter{}, newResource(testName))
		h.mustReconcile()
		got := h.get()
		if len(got.Finalizers) != 0 {
			t.Fatalf("orphan resource has finalizers %v", got.Finalizers)
		}
		if err := h.k8s.Delete(context.Background(), got); err != nil {
			t.Fatal(err)
		}
		if _, ok := h.store.get(policyPath); !ok {
			t.Fatal("orphaned object was deleted")
		}
	})
	t.Run("switching to orphan removes the finalizer", func(t *testing.T) {
		h := newHarness(t, testAdapter{}, deletable(newResource(testName)))
		h.mustReconcile()
		got := h.get()
		got.Spec.DeletionPolicy = commonv1alpha1.DeletionPolicyOrphan
		h.update(got)
		h.mustReconcile()
		if f := h.get().Finalizers; len(f) != 0 {
			t.Fatalf("finalizers = %v", f)
		}
	})
	t.Run("never acquired is not deleted", func(t *testing.T) {
		obj := newResource(testName)
		obj.Spec.CreationPolicy = commonv1alpha1.CreationPolicyCreate
		h := newHarness(t, testAdapter{}, deletable(obj))
		h.store.putPolicy(Payload{keyPolicy: "theirs"})
		h.mustReconcile()
		if err := h.k8s.Delete(context.Background(), h.get()); err != nil {
			t.Fatal(err)
		}
		h.mustReconcile()
		if _, ok := h.store.get(policyPath); !ok {
			t.Fatal("object owned by someone else was deleted")
		}
	})
}

func TestReconcileCleanupBlocked(t *testing.T) {
	h := newHarness(t, testAdapter{}, deletable(newResource(testName)))
	h.mustReconcile()
	h.conns.setErr(ErrConnectionNotReady)
	if err := h.k8s.Delete(context.Background(), h.get()); err != nil {
		t.Fatal(err)
	}
	if _, err := h.reconcile(); !errors.Is(err, ErrConnectionNotReady) {
		t.Fatalf("reconcile error = %v, want ErrConnectionNotReady", err)
	}
	got := h.get()
	if len(got.Finalizers) != 1 {
		t.Fatalf("finalizer removed while cleanup was blocked: %v", got.Finalizers)
	}
	assertCondition(t, got, commonv1alpha1.ConditionSynced, metav1.ConditionFalse, commonv1alpha1.ReasonCleanupBlocked)
	if _, ok := h.store.get(policyPath); !ok {
		t.Fatal("object deleted while cleanup was blocked")
	}

	h.conns.setErr(nil)
	h.mustReconcile()
	if _, ok := h.store.get(policyPath); ok {
		t.Fatal("object not deleted after the connection recovered")
	}
}

func TestReconcileRejectsEscapingNamespace(t *testing.T) {
	obj := newResource(testName)
	obj.Spec.Namespace = "../other"
	h := newHarness(t, testAdapter{}, obj)
	if _, err := h.reconcile(); !errors.Is(err, reconcile.TerminalError(nil)) || !errors.Is(err, ErrInvalidSpec) {
		t.Fatalf("err = %v, want a terminal ErrInvalidSpec", err)
	}
	got := h.get()
	assertCondition(t, got, commonv1alpha1.ConditionReady, metav1.ConditionFalse, commonv1alpha1.ReasonInvalidSpec)
	assertCondition(t, got, commonv1alpha1.ConditionSynced, metav1.ConditionFalse, commonv1alpha1.ReasonInvalidSpec)
	if got.Status.ObservedGeneration != got.Generation {
		t.Fatalf("observedGeneration = %d, want %d", got.Status.ObservedGeneration, got.Generation)
	}
	if _, writes := h.store.counts(); writes != 0 {
		t.Fatalf("writes = %d for an escaping namespace", writes)
	}
}

func TestReconcileStallsWhenConnectionNamespaceChanges(t *testing.T) {
	h := newHarness(t, testAdapter{}, deletable(newResource(testName)))
	h.mustReconcile()
	h.conns.mu.Lock()
	h.conns.client = &fakeBao{store: h.store, ns: "elsewhere"}
	h.conns.mu.Unlock()

	if _, err := h.reconcile(); err != nil {
		t.Fatalf("err = %v, want a stall", err)
	}
	got := h.get()
	assertCondition(t, got, commonv1alpha1.ConditionSynced, metav1.ConditionFalse, commonv1alpha1.ReasonNamespaceChanged)
	if _, writes := h.store.counts(); writes != 1 {
		t.Fatalf("writes = %d, want no write in the new namespace", writes)
	}
	if got.Status.Namespace != litAdmin {
		t.Fatalf("status.namespace = %q, want the recorded admin", got.Status.Namespace)
	}

	if err := h.k8s.Delete(context.Background(), got); err != nil {
		t.Fatal(err)
	}
	if _, err := h.reconcile(); err == nil {
		t.Fatal("deleted outside the connection namespace")
	}
	assertCondition(t, h.get(), commonv1alpha1.ConditionSynced, metav1.ConditionFalse, commonv1alpha1.ReasonCleanupBlocked)
	if _, ok := h.store.get(policyPath); !ok {
		t.Fatal("object deleted through the wrong namespace")
	}
}

func TestDeleteComparesNamespaceSegments(t *testing.T) {
	for _, recorded := range []string{"adminx", "admin/../other", "other/admin"} {
		t.Run(recorded, func(t *testing.T) {
			obj := newResource(testName)
			obj.Finalizers = []string{DefaultFinalizer}
			obj.Spec.DeletionPolicy = commonv1alpha1.DeletionPolicyDelete
			obj.Status = commonv1alpha1.ResourceStatus{
				Acquired: commonv1alpha1.AcquiredCreated, Namespace: recorded, Path: recorded + "/sys/policies/acl/p",
			}
			h := newHarness(t, testAdapter{}, obj)
			if err := h.k8s.Delete(context.Background(), h.get()); err != nil {
				t.Fatal(err)
			}
			if _, err := h.reconcile(); err == nil {
				t.Fatal("delete outside the connection namespace succeeded")
			}
			if deletes := h.store.deleteCount(); deletes != 0 {
				t.Fatalf("deletes = %d", deletes)
			}
		})
	}
}

// nsMissingBao answers like OpenBao for a namespace that does not exist yet.
type nsMissingBao struct{ *fakeBao }

func (nsMissingBao) Read(context.Context, string) (map[string]any, error) {
	return nil, &openbao.ResponseError{StatusCode: 404, Errors: []string{"namespace not found"}}
}

func (nsMissingBao) Write(context.Context, string, map[string]any) (map[string]any, error) {
	return nil, &openbao.ResponseError{StatusCode: 404, Errors: []string{"namespace not found"}}
}

func TestReconcileMissingNamespaceIsReferenceNotReady(t *testing.T) {
	h := newHarness(t, testAdapter{}, newResource(testName))
	h.conns.client = nsMissingBao{&fakeBao{store: h.store, ns: litAdmin}}
	res, err := h.reconcile()
	assertFirstReferenceRetry(t, res, err)
	assertCondition(t, h.get(), commonv1alpha1.ConditionSynced, metav1.ConditionFalse, commonv1alpha1.ReasonReferenceNotReady)
}

// notReadyObserver reports a referent that is not Ready from Observe.
type notReadyObserver struct{ testAdapter }

func (notReadyObserver) Observe(context.Context, openbao.Client, *testResource) (Payload, bool, error) {
	return nil, false, fmt.Errorf("mount kv: %w", ErrReferenceNotReady)
}

func TestReconcileObserverReferenceNotReadyStalls(t *testing.T) {
	h := newHarness(t, notReadyObserver{}, newResource(testName))
	res, err := h.reconcile()
	assertFirstReferenceRetry(t, res, err)
	assertCondition(t, h.get(), commonv1alpha1.ConditionSynced, metav1.ConditionFalse, commonv1alpha1.ReasonReferenceNotReady)
}

func TestEnqueueForSecret(t *testing.T) {
	user := newResource("user")
	user.Spec.PasswordSecretRef = &commonv1alpha1.SecretKeySelector{Name: credsSecret, Key: keyPassword}
	other := newResource("other")
	h := newHarness(t, testAdapter{}, user, other)

	enqueue, err := EnqueueForSecret(h.k8s, h.r.scheme, &testResource{})
	if err != nil {
		t.Fatal(err)
	}
	queue := workqueue.NewTypedRateLimitingQueue(workqueue.DefaultTypedControllerRateLimiter[reconcile.Request]())
	defer queue.ShutDown()
	// The engine watches Secrets metadata-only, so the handler sees PartialObjectMetadata.
	secret := &metav1.PartialObjectMetadata{ObjectMeta: metav1.ObjectMeta{Name: credsSecret, Namespace: testNamespace}}
	enqueue.Create(context.Background(), event.CreateEvent{Object: secret}, queue)

	if queue.Len() != 1 {
		t.Fatalf("queued %d requests, want 1", queue.Len())
	}
	req, _ := queue.Get()
	if req.Name != "user" || req.Namespace != testNamespace {
		t.Fatalf("queued %v", req)
	}
}

func TestReconcileDefaultCreationPolicyConflictsWithIdenticalObject(t *testing.T) {
	h := newHarness(t, testAdapter{}, newResource(testName))
	h.store.putPolicy(Payload{keyPolicy: testPolicyText})

	h.mustReconcile()
	if _, writes := h.store.counts(); writes != 0 {
		t.Fatalf("conflict wrote %d times", writes)
	}
	got := h.get()
	assertCondition(t, got, commonv1alpha1.ConditionReady, metav1.ConditionFalse, commonv1alpha1.ReasonConflict)
	if got.Status.Acquired != "" {
		t.Fatalf("acquired = %q, want empty", got.Status.Acquired)
	}
}

func TestReconcileCreatePolicyConflictsWithWriteOnlyInputs(t *testing.T) {
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: credsSecret, Namespace: testNamespace},
		Data:       map[string][]byte{keyPassword: []byte("one")},
	}
	obj := newResource(testName)
	obj.Spec.CreationPolicy = commonv1alpha1.CreationPolicyCreate
	obj.Spec.PasswordSecretRef = &commonv1alpha1.SecretKeySelector{Name: credsSecret, Key: keyPassword}
	h := newHarness(t, testAdapter{}, obj, secret)
	h.store.putPolicy(Payload{keyPolicy: testPolicyText})

	h.mustReconcile()
	if _, writes := h.store.counts(); writes != 0 {
		t.Fatalf("conflict wrote %d times", writes)
	}
	assertCondition(t, h.get(), commonv1alpha1.ConditionSynced, metav1.ConditionFalse, commonv1alpha1.ReasonConflict)
}

// providerAdapter declares "password" write-only only through FieldsFor, and hides it on read as OpenBao does.
type providerAdapter struct{ testAdapter }

func (providerAdapter) Fields() FieldSpec { return FieldSpec{Sets: []string{keyPolicies}} }

func (providerAdapter) FieldsFor(_ context.Context, _ Resolver, obj *testResource) (FieldSpec, error) {
	if obj.Spec.PasswordSecretRef == nil {
		return FieldSpec{}, nil
	}
	return FieldSpec{WriteOnly: []string{keyPassword}}, nil
}

func (a providerAdapter) Observe(ctx context.Context, c openbao.Client, obj *testResource) (Payload, bool, error) {
	observed, err := c.Read(ctx, a.Path(obj))
	if errors.Is(err, openbao.ErrNotFound) {
		return nil, false, nil
	}
	delete(observed, keyPassword)
	return observed, err == nil, err
}

func TestReconcileFieldsProviderRotation(t *testing.T) {
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: credsSecret, Namespace: testNamespace},
		Data:       map[string][]byte{keyPassword: []byte("one")},
	}
	obj := newResource(testName)
	obj.Spec.PasswordSecretRef = &commonv1alpha1.SecretKeySelector{Name: credsSecret, Key: keyPassword}
	h := newHarness(t, providerAdapter{}, obj, secret)

	h.mustReconcile()
	hash := h.get().Status.WriteOnlyHash
	if hash == "" {
		t.Fatal("writeOnlyHash not recorded from FieldsFor")
	}
	h.mustReconcile()
	if _, writes := h.store.counts(); writes != 1 {
		t.Fatalf("writes = %d in steady state, want 1", writes)
	}

	secret.Data[keyPassword] = []byte("two")
	if err := h.k8s.Update(context.Background(), secret); err != nil {
		t.Fatal(err)
	}
	h.mustReconcile()
	if _, writes := h.store.counts(); writes != 2 {
		t.Fatalf("writes = %d after rotation, want 2", writes)
	}
	if h.get().Status.WriteOnlyHash == hash {
		t.Fatal("writeOnlyHash unchanged after rotation")
	}
}

func TestFieldsMerge(t *testing.T) {
	h := newHarness(t, providerAdapter{})
	obj := newResource(testName)
	obj.Spec.PasswordSecretRef = &commonv1alpha1.SecretKeySelector{Name: credsSecret, Key: keyPassword}
	got, err := h.r.fields(context.Background(), h.r.resolver, obj)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(got.WriteOnly, []string{keyPassword}) || !slices.Equal(got.Sets, []string{keyPolicies}) {
		t.Fatalf("fields = %+v", got)
	}
}

// outputAdapter stores secret_id from the write response in the Secret "<name>-out".
type outputAdapter struct{ testAdapter }

func (outputAdapter) Outputs(obj *testResource, response Payload) (string, map[string][]byte, bool) {
	id, ok := response[keySecretID].(string)
	if !ok {
		return "", nil, false
	}
	return obj.Name + "-out", map[string][]byte{keySecretID: []byte(id)}, true
}

// writerOutputAdapter writes through Writer and returns its own response.
type writerOutputAdapter struct{ outputAdapter }

func (a writerOutputAdapter) Write(ctx context.Context, c openbao.Client, obj *testResource, desired Payload,
	_ bool) (Payload, error) {
	if _, err := c.Write(ctx, a.Path(obj), desired); err != nil {
		return nil, err
	}
	return Payload{keySecretID: "from-writer"}, nil
}

func assertOutputSecret(t *testing.T, h *harness, want string) {
	t.Helper()
	var secret corev1.Secret
	if err := h.k8s.Get(context.Background(), client.ObjectKey{Namespace: testNamespace, Name: outputSecretName}, &secret); err != nil {
		t.Fatalf("output secret: %v", err)
	}
	if string(secret.Data[keySecretID]) != want || secret.Type != corev1.SecretTypeOpaque ||
		secret.Labels["app.kubernetes.io/managed-by"] != DefaultFieldOwner {
		t.Fatalf("output secret = type %q labels %v", secret.Type, secret.Labels)
	}
	owner := metav1.GetControllerOf(&secret)
	if owner == nil || owner.Kind != litTestResource || owner.Name != testName {
		t.Fatalf("controller ref = %+v", owner)
	}
	status := fmt.Sprintf("%+v", h.get().Status)
	if strings.Contains(status, want) {
		t.Fatal("output value leaked into status")
	}
	for _, e := range h.drainEvents() {
		if strings.Contains(e, want) {
			t.Fatalf("output value leaked into event %q", e)
		}
	}
}

func TestReconcileOutputSecret(t *testing.T) {
	t.Run("default write path", func(t *testing.T) {
		h := newHarness(t, outputAdapter{}, newResource(testName))
		h.store.response = Payload{keySecretID: "from-client"}
		h.mustReconcile()
		assertOutputSecret(t, h, "from-client")
	})
	t.Run("writer path", func(t *testing.T) {
		h := newHarness(t, writerOutputAdapter{}, newResource(testName))
		h.mustReconcile()
		assertOutputSecret(t, h, "from-writer")
		if _, ok := h.store.get(policyPath); !ok {
			t.Fatal("writer did not write")
		}
	})
	t.Run("no secret when ok is false", func(t *testing.T) {
		h := newHarness(t, outputAdapter{}, newResource(testName))
		h.mustReconcile()
		var secret corev1.Secret
		err := h.k8s.Get(context.Background(), client.ObjectKey{Namespace: testNamespace, Name: outputSecretName}, &secret)
		if err == nil {
			t.Fatal("output secret written without outputs")
		}
	})
	t.Run("foreign secret is not overwritten", func(t *testing.T) {
		foreign := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{Name: outputSecretName, Namespace: testNamespace},
			Data:       map[string][]byte{"tls.key": []byte("keep")},
		}
		h := newHarness(t, outputAdapter{}, newResource(testName), foreign)
		h.store.response = Payload{keySecretID: "from-client"}
		if _, err := h.reconcile(); err != nil {
			t.Fatalf("err = %v, want a stall without error backoff", err)
		}
		assertCondition(t, h.get(), commonv1alpha1.ConditionSynced, metav1.ConditionFalse, commonv1alpha1.ReasonSecretNotOwned)
		var secret corev1.Secret
		if err := h.k8s.Get(context.Background(), client.ObjectKeyFromObject(foreign), &secret); err != nil {
			t.Fatal(err)
		}
		if string(secret.Data["tls.key"]) != "keep" || len(secret.Data) != 1 {
			t.Fatalf("foreign secret data changed: %v", slices.Collect(maps.Keys(secret.Data)))
		}
	})
}

// echoAdapter fails every write with an error that quotes the payload, as some OpenBao endpoints do.
type echoAdapter struct{ testAdapter }

func (echoAdapter) Write(_ context.Context, _ openbao.Client, _ *testResource, desired Payload, _ bool) (Payload, error) {
	return nil, fmt.Errorf("invalid value %q: %w", desired[keyPassword], &openbao.ResponseError{StatusCode: 400})
}

func TestReconcileRedactsWriteOnlyValuesInErrors(t *testing.T) {
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: credsSecret, Namespace: testNamespace},
		Data:       map[string][]byte{keyPassword: []byte("hunter2-secret")},
	}
	obj := newResource(testName)
	obj.Spec.PasswordSecretRef = &commonv1alpha1.SecretKeySelector{Name: credsSecret, Key: keyPassword}
	h := newHarness(t, echoAdapter{}, obj, secret)

	_, err := h.reconcile()
	var re *openbao.ResponseError
	if err == nil || !errors.As(err, &re) {
		t.Fatalf("err = %v, want a wrapped ResponseError", err)
	}
	if strings.Contains(err.Error(), "hunter2-secret") {
		t.Fatalf("returned error leaks a write-only value: %v", err)
	}
	synced := meta.FindStatusCondition(h.get().Status.Conditions, commonv1alpha1.ConditionSynced)
	if synced == nil || strings.Contains(synced.Message, "hunter2-secret") || !strings.Contains(synced.Message, "<redacted>") {
		t.Fatalf("Synced condition = %+v", synced)
	}
}

func assertFirstReferenceRetry(t *testing.T, res ctrl.Result, err error) {
	t.Helper()
	if err != nil || res.RequeueAfter < minReferenceRetry*4/5 || res.RequeueAfter > minReferenceRetry*6/5 {
		t.Fatalf("result = %+v, err = %v; want a ReferenceNotReady stall after about %v", res, err, minReferenceRetry)
	}
}

func TestReferenceRetryBacksOff(t *testing.T) {
	obj := newResource(testName)
	for _, tt := range []struct {
		waited   time.Duration
		min, max time.Duration
	}{
		{0, 1600 * time.Millisecond, 2400 * time.Millisecond},
		{10 * time.Second, 8 * time.Second, 12 * time.Second},
		{10 * time.Minute, 24 * time.Second, 36 * time.Second},
	} {
		obj.Status.Conditions = []metav1.Condition{{
			Type: commonv1alpha1.ConditionSynced, Status: metav1.ConditionFalse,
			Reason: commonv1alpha1.ReasonReferenceNotReady, LastTransitionTime: metav1.NewTime(time.Now().Add(-tt.waited)),
		}}
		if got := referenceRetry(obj); got < tt.min || got > tt.max {
			t.Errorf("after waiting %v: retry in %v, want %v..%v", tt.waited, got, tt.min, tt.max)
		}
	}
}

// refAdapter references a peer testResource by name through spec.data["peer"], or any peer when it is "*".
type refAdapter struct{ testAdapter }

func (refAdapter) ReferencedKinds() []client.Object { return []client.Object{&testResource{}} }

func (refAdapter) References(obj *testResource) []ObjectReference {
	switch peer := obj.Spec.Data["peer"]; peer {
	case "":
		return nil
	case "*":
		return []ObjectReference{{Kind: litTestResource}}
	default:
		return []ObjectReference{{Kind: litTestResource, Name: peer}}
	}
}

func TestEnqueueForReferent(t *testing.T) {
	byName, bySelector, unrelated := newResource("by-name"), newResource("by-selector"), newResource("unrelated")
	byName.Spec.Data = map[string]string{"peer": "referent"}
	bySelector.Spec.Data = map[string]string{"peer": "*"}
	h := newHarness(t, testAdapter{}, byName, bySelector, unrelated)
	k8s := fake.NewClientBuilder().WithScheme(h.r.scheme).
		WithIndex(&testResource{}, ReferenceIndex, ReferencesIndexFunc[*testResource](refAdapter{})).
		WithObjects(byName, bySelector, unrelated).Build()

	enqueue, err := EnqueueForReferent(k8s, h.r.scheme, &testResource{})
	if err != nil {
		t.Fatal(err)
	}
	queue := workqueue.NewTypedRateLimitingQueue(workqueue.DefaultTypedControllerRateLimiter[reconcile.Request]())
	defer queue.ShutDown()
	referent := newResource("referent")
	enqueue.Update(context.Background(), event.UpdateEvent{ObjectOld: referent, ObjectNew: referent}, queue)

	var got []string
	for queue.Len() > 0 {
		req, _ := queue.Get()
		got = append(got, req.Name)
		queue.Done(req)
	}
	slices.Sort(got)
	if !slices.Equal(got, []string{"by-name", "by-selector"}) {
		t.Fatalf("queued %v, want by-name and by-selector", got)
	}
}

// unsupportedAdapter fails writes the way a kind whose plugin is not installed does.
type unsupportedAdapter struct{ testAdapter }

func (unsupportedAdapter) Write(context.Context, openbao.Client, *testResource, Payload, bool) (Payload, error) {
	return nil, UnsupportedIfMountMissing(&openbao.ResponseError{StatusCode: 400,
		Errors: []string{`no handler for route "gcp/roles/x". route entry not found.`}})
}

func TestReconcileUnsupported(t *testing.T) {
	h := newHarness(t, unsupportedAdapter{}, newResource(testName))
	res, err := h.reconcile()
	if err != nil {
		t.Fatalf("err = %v, want nil so the controller does not back off", err)
	}
	if res.RequeueAfter < 48*time.Minute || res.RequeueAfter > 72*time.Minute {
		t.Fatalf("RequeueAfter = %v, want the jittered drift interval", res.RequeueAfter)
	}
	obj := h.get()
	assertCondition(t, obj, commonv1alpha1.ConditionReady, metav1.ConditionFalse, commonv1alpha1.ReasonUnsupported)
	assertCondition(t, obj, commonv1alpha1.ConditionSynced, metav1.ConditionFalse, commonv1alpha1.ReasonUnsupported)
}

func TestUnsupportedIfMountMissing(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		{"404", fmt.Errorf("write x: %w", &openbao.ResponseError{StatusCode: 404}), true},
		{"no handler", &openbao.ResponseError{StatusCode: 400, Errors: []string{`no handler for route "x"`}}, true},
		{"other 400", &openbao.ResponseError{StatusCode: 400, Errors: []string{"invalid ttl"}}, false},
		{"403", &openbao.ResponseError{StatusCode: 403}, false},
		{"plain", errors.New("boom"), false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := UnsupportedIfMountMissing(tt.err)
			if errors.Is(got, ErrUnsupported) != tt.want || !errors.Is(got, tt.err) {
				t.Fatalf("UnsupportedIfMountMissing(%v) = %v", tt.err, got)
			}
		})
	}
}

// mountRefAdapter writes under a mount whose path it resolves at reconcile time.
type mountRefAdapter struct {
	testAdapter
	mount *atomic.Pointer[string]
}

func (a mountRefAdapter) ResolvePath(context.Context, Resolver, openbao.Client, *testResource) (string, error) {
	mount := a.mount.Load()
	if mount == nil {
		return "", fmt.Errorf("mount: %w", ErrReferenceNotReady)
	}
	return *mount + "/role/" + testName, nil
}

func TestReconcilePathResolver(t *testing.T) {
	mount := &atomic.Pointer[string]{}
	h := newHarness(t, mountRefAdapter{mount: mount}, deletable(newResource(testName)))

	res, err := h.reconcile()
	assertFirstReferenceRetry(t, res, err)
	assertCondition(t, h.get(), commonv1alpha1.ConditionSynced, metav1.ConditionFalse, commonv1alpha1.ReasonReferenceNotReady)

	first := "auth/kubernetes"
	mount.Store(&first)
	h.mustReconcile()
	if got := h.get().Status.Path; got != "admin/auth/kubernetes/role/p" {
		t.Fatalf("status.path = %q", got)
	}
	if _, ok := h.store.get("admin/auth/kubernetes/role/p"); !ok {
		t.Fatal("object not written at the resolved path")
	}

	second := "auth/other"
	mount.Store(&second)
	if err := h.k8s.Delete(context.Background(), h.get()); err != nil {
		t.Fatal(err)
	}
	h.mustReconcile()
	if _, ok := h.store.get("admin/auth/kubernetes/role/p"); ok {
		t.Fatal("delete did not use the recorded status.path")
	}
}

// asyncAdapter reports ErrInProgress from its first `pending` writes and deletes.
type asyncAdapter struct {
	testAdapter
	pendingWrites, pendingDeletes *atomic.Int32
}

func (a asyncAdapter) Write(ctx context.Context, c openbao.Client, obj *testResource, desired Payload, _ bool) (Payload, error) {
	if a.pendingWrites.Add(-1) >= 0 {
		return nil, fmt.Errorf("enable mount: %w", ErrInProgress)
	}
	return c.Write(ctx, a.Path(obj), desired)
}

func (a asyncAdapter) Delete(ctx context.Context, c openbao.Client, obj *testResource) error {
	if a.pendingDeletes.Add(-1) >= 0 {
		return fmt.Errorf("namespace deletion: %w", ErrInProgress)
	}
	return c.Delete(ctx, a.Path(obj))
}

func assertInProgress(t *testing.T, h *harness, res ctrl.Result, err error) {
	t.Helper()
	if err != nil || res.RequeueAfter != 10*time.Second {
		t.Fatalf("reconcile = %+v, %v; want RequeueAfter 10s with nil error", res, err)
	}
	assertCondition(t, h.get(), commonv1alpha1.ConditionSynced, metav1.ConditionFalse, commonv1alpha1.ReasonReconcileInProgress)
}

func TestReconcileInProgress(t *testing.T) {
	adapter := asyncAdapter{
		pendingWrites:  &atomic.Int32{},
		pendingDeletes: &atomic.Int32{},
	}
	adapter.pendingWrites.Store(1)
	adapter.pendingDeletes.Store(1)
	h := newHarness(t, adapter, deletable(newResource(testName)))

	res, err := h.reconcile()
	assertInProgress(t, h, res, err)
	h.mustReconcile()
	assertCondition(t, h.get(), commonv1alpha1.ConditionReady, metav1.ConditionTrue, commonv1alpha1.ReasonAvailable)

	if err := h.k8s.Delete(context.Background(), h.get()); err != nil {
		t.Fatal(err)
	}
	res, err = h.reconcile()
	assertInProgress(t, h, res, err)
	if f := h.get().Finalizers; len(f) != 1 {
		t.Fatalf("finalizer removed while deletion was in progress: %v", f)
	}
	h.mustReconcile()
	if _, ok := h.store.get(policyPath); ok {
		t.Fatal("object not deleted after the operation completed")
	}
}

func TestRedactWriteOnlyNestedValues(t *testing.T) {
	desired := Payload{
		keyPassword: map[string]any{"provider": Payload{"client_secret": "nested-secret"}, "keys": []any{"list-secret", 42}},
		keyPolicy:   "visible-policy",
	}
	fields := FieldSpec{WriteOnly: []string{keyPassword}}
	err := redactWriteOnly(errors.New("bad nested-secret, list-secret, visible-policy, 42"), desired, fields)
	if msg := err.Error(); strings.Contains(msg, "nested-secret") || strings.Contains(msg, "list-secret") ||
		!strings.Contains(msg, "visible-policy") || !strings.Contains(msg, "<redacted>") {
		t.Fatalf("redacted error = %q", msg)
	}
}
