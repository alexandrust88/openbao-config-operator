//go:build conformance

package conformance

import (
	"context"
	"fmt"
	"testing"
	"time"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/client-go/util/retry"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// AccessLevel is an event-driven Kubernetes template, not an OpenBao resource.
// Its contract is current-generation Ready after edits and zero OpenBao writes;
// requiring a periodic reconcile or one write per field would assert the wrong contract.
func confTemplateDefaults(ctx context.Context, t *testing.T, e *confEnv, mgr *confManager, r *confResult,
	base, obj *unstructured.Unstructured, extras map[string]bool) {
	t.Helper()
	r.note("template contract: generation-driven Ready after set/remove; zero writes observed for two drift intervals per step; no periodic requeue")
	check := func(field string, mutate func(map[string]any)) {
		before, err := mgr.scrape()
		if err != nil {
			r.fail(field, "metrics before: %s", err)
			return
		}
		if mutate != nil {
			err = retry.RetryOnConflict(retry.DefaultRetry, func() error {
				cur := &unstructured.Unstructured{}
				cur.SetGroupVersionKind(obj.GroupVersionKind())
				if err := e.cluster.k8s.Get(ctx, client.ObjectKeyFromObject(obj), cur); err != nil {
					return err
				}
				spec, _ := cur.Object["spec"].(map[string]any)
				mutate(spec)
				return e.cluster.k8s.Update(ctx, cur)
			})
			if err != nil {
				r.fail(field, "update: %s", err)
				return
			}
		}
		if state := confWaitReady(ctx, e.cluster.k8s, obj, confChangeTimeout); !state.ready {
			r.fail(field, "generation not ready: %s", state)
			return
		}
		// Preserve a real silence window despite there being no periodic work to count.
		select {
		case <-ctx.Done():
			r.fail(field, "observation cancelled: %s", ctx.Err())
			return
		case <-time.After(2 * e.drift):
		}
		after, err := mgr.scrape()
		if err != nil {
			r.fail(field, "metrics after: %s", err)
			return
		}
		if writes := after.writes[r.kind.Kind] - before.writes[r.kind.Kind]; writes != 0 {
			r.fail(field, "template made %g OpenBao writes", writes)
		}
	}
	check("(minimal)", nil)
	for _, field := range r.kind.optionalFields() {
		if extras[field] {
			continue
		}
		value, ok := confValueFor(r.kind, base, field)
		if !ok {
			r.fail(field, "no example value for template field")
			continue
		}
		if confZero(value) {
			if alternate, ok := confAlternative(r.kind.property(field), value); ok {
				value = alternate
			}
		}
		started := time.Now()
		check(fmt.Sprintf("%s set", field), func(spec map[string]any) { spec[field] = value })
		check(fmt.Sprintf("%s remove", field), func(spec map[string]any) { delete(spec, field) })
		elapsed := time.Since(started)
		r.fieldsDuration += elapsed
		r.fieldsTested++
		t.Logf("%s.%s template set/remove checked in %s", r.kind.Kind, field, elapsed.Round(time.Millisecond))
	}
	r.note("template test: %d optional fields tested", r.fieldsTested)
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.failing) > 0 {
		r.defaults = fmt.Sprintf("%s (%d)", confFail, len(r.failing))
	} else {
		r.defaults = confPass
	}
}
