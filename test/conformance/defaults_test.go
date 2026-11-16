//go:build conformance

package conformance

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/util/retry"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	confMinimalTimeout = 45 * time.Second
	confMaxCandidates  = 3
	confChangeTimeout  = 20 * time.Second
)

// confDefaults runs the default test for one kind in its own Kubernetes namespace, OpenBao namespace (root for
// root-only kinds) and manager process, so openbao_writes_total{kind} counts only the object under test:
// a minimal spec (required fields only) must not write on its second pass; then each optional field is set
// (one write), removed (one write restoring the default) and left alone for a further pass (no write).
func confDefaults(ctx context.Context, t *testing.T, e *confEnv, c *confCatalog, r *confResult) {
	k := r.kind
	k8s := e.cluster.k8s
	ns := "conf-" + strings.ToLower(k.Kind)
	baoNS := ns
	if confRootKinds[k.Kind] {
		baoNS = ""
		r.note("default test in the root OpenBao namespace")
	} else if err := e.bao.createNamespace(ns); err != nil {
		t.Fatalf("create OpenBao namespace %s: %v", ns, err)
	}
	mgr := confStartManager(t, e, ns, filepath.Join(e.logDir, "manager-"+ns+".log"))
	if err := confConnect(ctx, e, ns, baoNS); err != nil {
		t.Fatal(err)
	}

	for _, d := range c.referents(k, k.examples(), false) {
		obj := confRewrite(d, ns, false)
		if err := k8s.Create(ctx, obj); err != nil {
			if !apierrors.IsAlreadyExists(err) {
				r.note("referent %s/%s rejected: %s", d.GetKind(), d.GetName(), confShortErr(err))
			}
			continue
		}
		if m, path, ok := confMountOf(obj); ok {
			_ = e.bao.ensureMount(baoNS, m, path)
		}
	}

	candidates, err := confMinimal(ctx, k8s, k, ns)
	if err != nil {
		r.note("minimal spec rejected: %s", confShortErr(err))
		r.defaults = e.na(r, confTestDefaults, "minimal spec rejected by CRD")
		return
	}
	var base, obj *unstructured.Unstructured
	var extras map[string]bool
	for _, c := range candidates {
		if s, ok := confTryMinimal(ctx, e, baoNS, c.obj); !ok {
			r.note("minimal spec from %s not ready: %s", c.base.GetName(), s)
			continue
		}
		base, obj, extras = c.base, c.obj, c.extras
		break
	}
	if obj == nil {
		r.defaults = e.na(r, confTestDefaults, "minimal spec not ready")
		return
	}
	if len(extras) > 0 {
		r.note("minimal spec adds CEL-required %s", strings.Join(confKeys(extras), ","))
	}
	since := time.Now()
	if w := confSteady(mgr, k, 2, e.drift); w > 0 {
		r.fail("(minimal)", "second pass wrote %d time(s) %v", w,
			confUpdateNotes(ctx, k8s, ns, k.Kind, obj.GetName(), since))
	}

	var noValue, rejected []string
	tested := 0
	for _, field := range k.optionalFields() {
		if extras[field] {
			continue
		}
		if reason, ok := e.exceptions[k.Kind+"/spec."+field]; ok {
			r.note("%s: exception (%s)", field, reason)
			continue
		}
		value, ok := confValueFor(k, base, field)
		if !ok {
			noValue = append(noValue, field)
			continue
		}
		switch confField(ctx, e, mgr, r, obj, field, value) {
		case confFieldRejected:
			rejected = append(rejected, field)
		default:
			tested++
		}
	}
	r.note("default test: %d optional fields tested", tested)
	if tested == 0 {
		e.na(r, confTestOptionalFields, "no optional field tested")
	}
	if len(noValue) > 0 {
		r.note("no example value for %s", strings.Join(noValue, ","))
	}
	if len(rejected) > 0 {
		r.note("change rejected by CRD (immutable or dependent) for %s", strings.Join(rejected, ","))
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.failing) > 0 {
		r.defaults = fmt.Sprintf("%s (%d)", confFail, len(r.failing))
	} else {
		r.defaults = confPass
	}
}

type confFieldOutcome int

const (
	confFieldTested confFieldOutcome = iota
	confFieldRejected
)

// confField sets one optional field, removes it again and checks the writes of each step.
func confField(ctx context.Context, e *confEnv, mgr *confManager, r *confResult, obj *unstructured.Unstructured,
	field string, value any) confFieldOutcome {
	k := r.kind
	k8s := e.cluster.k8s
	set := func(v any) (int, confObjState, error) {
		return confChange(ctx, k8s, mgr, k, obj, e.drift, func(spec map[string]any) { spec[field] = v })
	}
	notes := func(since time.Time) []string {
		return confUpdateNotes(ctx, k8s, obj.GetNamespace(), k.Kind, obj.GetName(), since)
	}
	writes, state, err := set(value)
	if confIsInvalid(err) {
		return confFieldRejected
	}
	if err != nil {
		r.fail(field, "update: %s", confShortErr(err))
		return confFieldTested
	}
	if alt, ok := confAlternative(k.property(field), value); ok && state.ready && writes == 0 {
		value = alt
		writes, state, err = set(alt)
		if confIsInvalid(err) {
			r.note("%s: example value made no write and the alternative %v was rejected by the CRD", field, alt)
			_, _, _ = confChange(ctx, k8s, mgr, k, obj, e.drift, func(spec map[string]any) { delete(spec, field) })
			return confFieldRejected
		}
		if err != nil {
			r.fail(field, "update: %s", confShortErr(err))
			return confFieldTested
		}
	}
	since := time.Now()
	setOK := true
	switch {
	case !state.ready:
		r.fail(field, "set: not ready: %s", state)
		setOK = false
	case writes == 0 && confZero(value):
		r.note("%s: example value %v is a zero value and made no write", field, value)
		setOK = false
	case writes == 0:
		r.fail(field, "set: no write")
	case writes > 1:
		r.fail(field, "set: %d writes %v", writes, notes(since))
	}

	since = time.Now()
	writes, state, err = confChange(ctx, k8s, mgr, k, obj, e.drift, func(spec map[string]any) { delete(spec, field) })
	if err != nil {
		r.note("%s: removal rejected: %s", field, confShortErr(err))
		return confFieldTested
	}
	switch {
	case !state.ready:
		r.fail(field, "remove: not ready: %s", state)
		return confFieldTested
	case !setOK:
		return confFieldTested
	case k.Kind == "KVSecretMetadata" && writes == 0:
		r.note("%s: omission preserves the stored value without writing", field)
	case k.Kind == "KVSecretMetadata":
		r.fail(field, "remove: %d writes, want no write for retained metadata", writes)
	case writes == 0:
		r.fail(field, "remove: no write (default not restored)")
	case writes > 1:
		r.fail(field, "remove: %d writes %v", writes, notes(since))
	}
	since = time.Now()
	if w := confSteady(mgr, k, 1, e.drift); w > 0 {
		r.fail(field, "pass after remove wrote %d time(s) %v", w, notes(since))
	}
	return confFieldTested
}

// confCandidate is a minimal object built from the example document base.
type confCandidate struct {
	base, obj *unstructured.Unstructured
	extras    map[string]bool
}

// confMinimal builds, for up to confMaxCandidates example documents, the smallest object the CRD accepts: the
// required fields, else the required fields plus one field that a spec-level CEL rule mentions, else plus all
// of them.
func confMinimal(ctx context.Context, k8s client.Client, k *confKind, ns string) ([]confCandidate, error) {
	var lastErr error
	var out []confCandidate
	for _, d := range k.own(true) {
		if len(out) == confMaxCandidates {
			break
		}
		docSpec, _ := d.Object["spec"].(map[string]any)
		var cel []string
		for _, f := range confKeys(k.celFields()) {
			if _, ok := docSpec[f]; ok && !k.required()[f] {
				cel = append(cel, f)
			}
		}
		attempts := [][]string{nil}
		for _, f := range cel {
			attempts = append(attempts, []string{f})
		}
		if len(cel) > 1 {
			attempts = append(attempts, cel)
		}
		for _, extra := range attempts {
			spec := map[string]any{
				confConnectionRef: map[string]any{confKeyKind: confKindConnection, confKeyName: confConnection},
				"creationPolicy":  "CreateOrAdopt",
			}
			extras := map[string]bool{}
			for f := range k.required() {
				if v, ok := docSpec[f]; ok && !confCommonFields[f] {
					spec[f] = runtime.DeepCopyJSONValue(v)
				}
			}
			for _, f := range extra {
				spec[f] = runtime.DeepCopyJSONValue(docSpec[f])
				extras[f] = true
			}
			obj := &unstructured.Unstructured{Object: map[string]any{confKeySpec: spec}}
			obj.SetAPIVersion(d.GetAPIVersion())
			obj.SetKind(d.GetKind())
			obj.SetName(d.GetName())
			obj.SetNamespace(ns)
			dry := obj.DeepCopy()
			err := k8s.Create(ctx, dry, client.DryRunAll)
			if err == nil {
				if path, ok, _ := unstructured.NestedString(dry.Object, "spec", "mountPath"); ok {
					_ = unstructured.SetNestedField(obj.Object, path, "spec", "mountPath")
				}
				out = append(out, confCandidate{base: d, obj: obj, extras: extras})
				break
			}
			lastErr = err
			if !confIsInvalid(err) {
				break
			}
		}
	}
	if len(out) > 0 {
		return out, nil
	}
	if lastErr == nil {
		lastErr = errors.New("no example document")
	}
	return nil, lastErr
}

// confTryMinimal creates obj and waits for Ready; an object that does not get Ready is deleted again so the
// next candidate starts from a clean namespace.
func confTryMinimal(ctx context.Context, e *confEnv, baoNS string, obj *unstructured.Unstructured) (confObjState, bool) {
	k8s := e.cluster.k8s
	if m, path, ok := confMountOf(obj); ok {
		_ = e.bao.ensureMount(baoNS, m, path)
	}
	if err := k8s.Create(ctx, obj.DeepCopy()); err != nil {
		return confObjState{reason: "CreateFailed", message: confShortErr(err)}, false
	}
	s := confWaitReady(ctx, k8s, obj, confMinimalTimeout)
	if s.ready {
		return s, true
	}
	cur := &unstructured.Unstructured{}
	cur.SetGroupVersionKind(obj.GroupVersionKind())
	if err := k8s.Get(ctx, client.ObjectKeyFromObject(obj), cur); err == nil {
		patch := client.MergeFrom(cur.DeepCopy())
		cur.SetFinalizers(nil)
		_ = k8s.Patch(ctx, cur, patch)
		_ = k8s.Delete(ctx, cur)
	}
	_ = confPoll(confDeleteTimeout, func() error {
		err := k8s.Get(ctx, client.ObjectKeyFromObject(obj), cur)
		if apierrors.IsNotFound(err) {
			return nil
		}
		return errors.New("still present")
	})
	return s, false
}

// confChange applies mutate to the live object's spec, waits until the change is Ready and one further
// reconcile ran, and returns the writes for the kind in that window.
func confChange(ctx context.Context, k8s client.Client, mgr *confManager, k *confKind, obj *unstructured.Unstructured,
	drift time.Duration, mutate func(map[string]any)) (int, confObjState, error) {
	before, err := mgr.scrape()
	if err != nil {
		return 0, confObjState{}, err
	}
	err = retry.RetryOnConflict(retry.DefaultRetry, func() error {
		cur := &unstructured.Unstructured{}
		cur.SetGroupVersionKind(obj.GroupVersionKind())
		if err := k8s.Get(ctx, client.ObjectKeyFromObject(obj), cur); err != nil {
			return err
		}
		spec, _ := cur.Object["spec"].(map[string]any)
		mutate(spec)
		return k8s.Update(ctx, cur)
	})
	if err != nil {
		return 0, confObjState{}, err
	}
	state := confWaitReady(ctx, k8s, obj, confChangeTimeout)
	if state.ready {
		confWaitReconciles(mgr, k, 1, drift)
	}
	after, err := mgr.scrape()
	if err != nil {
		return 0, state, err
	}
	return int(after.writes[k.Kind] - before.writes[k.Kind]), state, nil
}

// confSteady waits for n further reconciles of the kind and returns the writes made meanwhile.
func confSteady(mgr *confManager, k *confKind, n int, drift time.Duration) int {
	before, err := mgr.scrape()
	if err != nil {
		return -1
	}
	confWaitReconciles(mgr, k, n, drift)
	after, err := mgr.scrape()
	if err != nil {
		return -1
	}
	return int(after.writes[k.Kind] - before.writes[k.Kind])
}

func confWaitReconciles(mgr *confManager, k *confKind, n int, drift time.Duration) {
	start, err := mgr.scrape()
	if err != nil {
		return
	}
	_ = confPoll(time.Duration(n+2)*drift, func() error {
		cur, err := mgr.scrape()
		if err != nil {
			return err
		}
		if cur.reconciles[k.controller()] < start.reconciles[k.controller()]+float64(n) {
			return errors.New("waiting for reconciles")
		}
		return nil
	})
}

func confWaitReady(ctx context.Context, k8s client.Client, obj *unstructured.Unstructured,
	timeout time.Duration) confObjState {
	var s confObjState
	_ = confPoll(timeout, func() error {
		if s = confState(ctx, k8s, obj); !s.ready {
			return errors.New("not ready")
		}
		return nil
	})
	return s
}

// confValueFor returns an example value for field: from the base document, another example document of the
// kind, or a generated example, in that order.
func confValueFor(k *confKind, base *unstructured.Unstructured, field string) (any, bool) {
	for _, d := range append([]*unstructured.Unstructured{base}, k.own(true)...) {
		if v, ok, _ := unstructured.NestedFieldCopy(d.Object, "spec", field); ok && v != nil {
			return v, true
		}
	}
	return nil, false
}

// confAlternative returns a second value to try when the example value made no write because it equals the
// server default: another enum value, the other boolean, the next integer, a longer duration, a suffixed
// string or a list without its last entry.
func confAlternative(prop map[string]any, v any) (any, bool) {
	if enum, ok := prop["enum"].([]any); ok {
		for _, e := range enum {
			if fmt.Sprint(e) != fmt.Sprint(v) && fmt.Sprint(e) != "" {
				return e, true
			}
		}
		return nil, false
	}
	switch x := v.(type) {
	case bool:
		return !x, true
	case int64:
		if limit, ok := prop["maximum"].(float64); ok && float64(x+1) > limit {
			return x - 1, x > 0
		}
		return x + 1, true
	case float64:
		return x + 1, true
	case []any:
		if len(x) > 1 {
			return x[:len(x)-1], true
		}
	case string:
		if d, err := time.ParseDuration(x); err == nil {
			if d <= 0 {
				return "1h", true
			}
			return (2 * d).String(), true
		}
		if _, hasPattern := prop["pattern"]; x != "" && !hasPattern && prop["format"] == nil {
			return x + "-conf", true
		}
	}
	return nil, false
}

func confZero(v any) bool {
	switch x := v.(type) {
	case nil:
		return true
	case bool:
		return !x
	case string:
		return x == ""
	case int64:
		return x == 0
	case float64:
		return x == 0
	case []any:
		return len(x) == 0
	case map[string]any:
		return len(x) == 0
	}
	return false
}

func confKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
