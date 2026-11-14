package engine

import (
	"fmt"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	commonv1alpha1 "github.com/alexandrust88/openbao-config-operator/api/common/v1alpha1"
)

// budgetTamper changes the stored object of name out of band, so its next reconcile is a drift repair.
func budgetTamper(h *harness, name string) {
	h.store.mu.Lock()
	defer h.store.mu.Unlock()
	h.store.objects[litAdmin+"/sys/policies/acl/"+name] = Payload{keyPolicy: "out-of-band"}
}

func TestBudgetRewrite(t *testing.T) {
	acquired := func(readyGeneration int64) *testResource {
		obj := newResource(testName)
		obj.Generation = 2
		obj.Status.Acquired = commonv1alpha1.AcquiredCreated
		if readyGeneration > 0 {
			obj.Status.Conditions = []metav1.Condition{{Type: commonv1alpha1.ConditionReady, Status: metav1.ConditionTrue,
				Reason: commonv1alpha1.ReasonAvailable, ObservedGeneration: readyGeneration}}
		}
		return obj
	}
	cases := []struct {
		name          string
		obj           *testResource
		found         bool
		inputsChanged bool
		regenerates   bool
		want          bool
	}{
		{"create", newResource(testName), false, false, false, false},
		{"adoption", newResource(testName), true, false, false, false},
		{"spec change", acquired(1), true, false, false, false},
		{"changed input Secret", acquired(2), true, true, false, false},
		{"drift repair or changed default", acquired(2), true, false, false, true},
		{"acquired without Ready", acquired(0), true, false, false, true},
		{"regenerated output before acquisition", newResource(testName), true, false, true, true},
	}
	for _, tc := range cases {
		if got := rewrite(tc.obj, tc.found, tc.inputsChanged, tc.regenerates); got != tc.want {
			t.Errorf("%s: rewrite = %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestBudgetRetrySpread(t *testing.T) {
	const wait = 10 * time.Second
	const bands = 5
	var hits [bands]int
	for range 1000 {
		got := budgetRetry(wait)
		if got < wait || got >= wait+budgetWindow {
			t.Fatalf("budgetRetry = %v, want %v..%v", got, wait, wait+budgetWindow)
		}
		hits[(got-wait)*bands/budgetWindow]++
	}
	for band, n := range hits {
		if n < 100 {
			t.Fatalf("band %d of the window got %d of 1000 retries (%v): the retries bunch up", band, n, hits)
		}
	}
	if got := budgetRetry(0); got < time.Second {
		t.Fatalf("budgetRetry(0) = %v, want at least 1s", got)
	}
}

func TestConnectionNotReadyBackoff(t *testing.T) {
	h := newHarness(t, testAdapter{}, newResource(testName))
	h.conns.setErr(ErrConnectionNotReady)
	res, err := h.reconcile()
	if err != nil {
		t.Fatal(err)
	}
	assertBetween(t, res.RequeueAfter, 1600*time.Millisecond, 2400*time.Millisecond)

	for _, age := range []struct{ since, low, high time.Duration }{
		{20 * time.Second, 16 * time.Second, 25 * time.Second},
		{time.Hour, 48 * time.Second, 72 * time.Second},
	} {
		got := h.get()
		for i := range got.Status.Conditions {
			got.Status.Conditions[i].LastTransitionTime = metav1.NewTime(time.Now().Add(-age.since))
		}
		if err := h.k8s.Status().Update(t.Context(), got); err != nil {
			t.Fatal(err)
		}
		res, err := h.reconcile()
		if err != nil {
			t.Fatal(err)
		}
		assertBetween(t, res.RequeueAfter, age.low, age.high)
	}
	if !reconnecting(h.get()) {
		t.Fatal("the pass after ConnectionNotReady is not low priority")
	}

	h.conns.setErr(nil)
	h.mustReconcile()
	if reconnecting(h.get()) {
		t.Fatal("a Ready resource still counts as reconnecting")
	}
}

func TestRecoveryRampPacesEachServer(t *testing.T) {
	var ramp recoveryRamp
	const passes = recoveryBurst + recoveryPerSecond/5
	start := time.Now()
	for range passes {
		if err := ramp.wait(t.Context(), "a"); err != nil {
			t.Fatal(err)
		}
	}
	if took := time.Since(start); took < 150*time.Millisecond {
		t.Fatalf("%d recovering passes took %v, want about 200ms", passes, took)
	}
	start = time.Now()
	if err := ramp.wait(t.Context(), "b"); err != nil || time.Since(start) > 50*time.Millisecond {
		t.Fatalf("server b waited for server a: %v", err)
	}
}

// TestBudgetSecretRotationVersusTamper proves that rotating a Secret that 100 objects use spends none of the budget,
// while tampering with the same 100 objects out of band is held back by it.
func TestBudgetSecretRotationVersusTamper(t *testing.T) {
	const objects, perMinute = 100, 10
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: credsSecret, Namespace: testNamespace},
		Data:       map[string][]byte{keyPassword: []byte("one")},
	}
	objs := append(make([]client.Object, 0, 1+objects), secret)
	names := make([]string, objects)
	for i := range names {
		names[i] = fmt.Sprintf("budget-%d", i)
		obj := newResource(names[i])
		obj.Spec.PasswordSecretRef = &commonv1alpha1.SecretKeySelector{Name: credsSecret, Key: keyPassword}
		objs = append(objs, obj)
	}
	h := newHarnessWith(t, testAdapter{}, Options{WriteBudgetPerMinute: perMinute}, objs...)
	reconcileAll := func() {
		t.Helper()
		for _, name := range names {
			if _, err := h.reconcileName(name); err != nil {
				t.Fatal(err)
			}
			h.drainEvents()
		}
	}
	reconcileAll()

	secret.Data[keyPassword] = []byte("two")
	if err := h.k8s.Update(t.Context(), secret); err != nil {
		t.Fatal(err)
	}
	before := h.writes()
	reconcileAll()
	if got := h.writes() - before; got != objects {
		t.Fatalf("rotation wrote %d of %d objects; a changed input Secret must not wait for the budget", got, objects)
	}

	for _, name := range names {
		budgetTamper(h, name)
	}
	before = h.writes()
	reconcileAll()
	if got := h.writes() - before; got != perMinute {
		t.Fatalf("tamper repairs = %d, want %d: drift repairs must wait for the budget", got, perMinute)
	}
}
