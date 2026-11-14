//go:build conformance

package conformance

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	confExamplesNS    = "conf-examples"
	confReadyTimeout  = 3 * time.Minute
	confDeleteTimeout = time.Minute
)

// confNoWrite applies the examples of every selected built-in kind in one namespace (root OpenBao namespace),
// waits for Ready, lets every object pass through further drift reconciles and asserts that
// openbao_writes_total did not move for the kind. The objects are deleted afterwards (deletionPolicy Delete).
func confNoWrite(ctx context.Context, t *testing.T, e *confEnv, c *confCatalog, results []*confResult) {
	k8s := e.cluster.k8s
	mgr := confStartManager(t, e, confExamplesNS, filepath.Join(e.logDir, "manager-nowrite.log"))
	defer mgr.stop()
	if err := confConnect(ctx, e, confExamplesNS, ""); err != nil {
		t.Fatal(err)
	}

	byKind := map[string]*confResult{}
	for _, r := range results {
		if r.skip == "" {
			byKind[r.kind.Kind] = r
		}
	}
	own, refs := confNoWriteObjects(c, byKind)
	created, createErrs := confCreateAll(ctx, t, e, append(append([]*unstructured.Unstructured{}, own...), refs...))
	states := confWaitAllReady(ctx, k8s, byKind, created)

	before, err := mgr.scrape()
	if err != nil {
		t.Fatal(err)
	}
	since := time.Now()
	time.Sleep(time.Duration(3.5 * float64(e.drift)))
	after, err := mgr.scrape()
	if err != nil {
		t.Fatal(err)
	}

	ownCount := map[string]int{}
	for _, o := range own {
		ownCount[o.GetKind()]++
	}
	for kind, r := range byKind {
		ready := 0
		for name, s := range states[kind] {
			if s.ready {
				ready++
			} else {
				r.note("example not ready: %s %s", name, s)
			}
		}
		r.example = fmt.Sprintf("%d/%d ready", ready, ownCount[kind])
		for _, msg := range createErrs[kind] {
			r.note("example rejected: %s", msg)
		}
		writes := after.writes[kind] - before.writes[kind]
		reconciles := after.reconciles[r.kind.controller()] - before.reconciles[r.kind.controller()]
		r.noWrite = confNoWriteVerdict(len(created[kind]), ready, writes, reconciles)
		if why, ok := strings.CutPrefix(r.noWrite, "n/a ("); ok {
			r.noWrite = e.na(r, confTestNoWrite, strings.TrimSuffix(why, ")"))
		}
		if writes > 0 {
			for _, keys := range confUpdateNotes(ctx, k8s, confExamplesNS, kind, "", since) {
				r.note("no-write: %s", keys)
			}
		}
	}

	confDeleteAll(ctx, t, k8s, created, byKind)
}

func confNoWriteVerdict(n, ready int, writes, reconciles float64) string {
	switch {
	case n == 0:
		return "n/a (nothing created)"
	case writes > 0:
		return fmt.Sprintf("%s (+%d writes)", confFail, int(writes))
	case ready == 0:
		return "n/a (none ready)"
	case reconciles < float64(ready):
		return fmt.Sprintf("n/a (only %d reconciles)", int(reconciles))
	case ready < n:
		return fmt.Sprintf("%s (%d/%d ready)", confPass, ready, n)
	}
	return confPass
}

// confNoWriteObjects returns the rewritten example documents of every kind under test, and the referents they
// need that are not themselves under test (plugin kinds left out).
func confNoWriteObjects(c *confCatalog, byKind map[string]*confResult) (own, refs []*unstructured.Unstructured) {
	seen := map[string]bool{}
	for _, r := range byKind {
		docs := r.kind.examples()
		if len(r.kind.GenDocs) == 0 && strings.Contains(r.kind.ExampleFile, "examples-generated") {
			r.note("example from examples-generated")
		}
		for _, d := range docs {
			seen[d.GetKind()+"/"+d.GetName()] = true
			own = append(own, confRewrite(d, confExamplesNS, true))
		}
	}
	for _, r := range byKind {
		for _, d := range c.referents(r.kind, r.kind.examples(), true) {
			if key := d.GetKind() + "/" + d.GetName(); !seen[key] && !confPluginKind(c, d.GetKind()) {
				seen[key] = true
				refs = append(refs, confRewrite(d, confExamplesNS, true))
			}
		}
	}
	return own, refs
}

// confCreateAll creates Secrets first, then everything else, and enables the mounts each object needs unless
// an AuthMount or SecretsMount document creates that mount itself.
func confCreateAll(ctx context.Context, t *testing.T, e *confEnv, all []*unstructured.Unstructured) (
	map[string][]*unstructured.Unstructured, map[string][]string) {
	managed := confManagedMountPaths(all)
	created := map[string][]*unstructured.Unstructured{}
	createErrs := map[string][]string{}
	for _, secretsFirst := range []bool{true, false} {
		for _, obj := range all {
			if (obj.GetKind() == confKindSecret) != secretsFirst {
				continue
			}
			err := confCreate(ctx, e.cluster.k8s, obj)
			switch {
			case obj.GetKind() == confKindSecret:
				if err != nil && !apierrors.IsAlreadyExists(err) {
					t.Logf("create Secret %s: %v", obj.GetName(), err)
				}
			case err != nil:
				createErrs[obj.GetKind()] = append(createErrs[obj.GetKind()], obj.GetName()+": "+confShortErr(err))
			default:
				created[obj.GetKind()] = append(created[obj.GetKind()], obj)
				if m, path, ok := confMountOf(obj); ok && !managed[confMountKey(m.auth, path)] {
					if err := e.bao.ensureMount("", m, path); err != nil {
						t.Logf("mount %s for %s/%s: %v", path, obj.GetKind(), obj.GetName(), err)
					}
				}
			}
		}
	}
	return created, createErrs
}

// confWaitAllReady polls the created objects of the kinds under test until all are Ready or confReadyTimeout.
func confWaitAllReady(ctx context.Context, k8s client.Client, byKind map[string]*confResult,
	created map[string][]*unstructured.Unstructured) map[string]map[string]confObjState {
	states := map[string]map[string]confObjState{}
	deadline := time.Now().Add(confReadyTimeout)
	for {
		pending := 0
		for kind := range byKind {
			states[kind] = map[string]confObjState{}
			for _, o := range created[kind] {
				s := confState(ctx, k8s, o)
				states[kind][o.GetName()] = s
				if !s.ready {
					pending++
				}
			}
		}
		if pending == 0 || time.Now().After(deadline) {
			return states
		}
		time.Sleep(time.Second)
	}
}

// confDeleteAll deletes every created object, waits for the finalizers, then strips the finalizers of the
// objects that are still there so they cannot write during the default test.
func confDeleteAll(ctx context.Context, t *testing.T, k8s client.Client,
	created map[string][]*unstructured.Unstructured, byKind map[string]*confResult) {
	for _, objs := range created {
		for _, o := range objs {
			if err := k8s.Delete(ctx, o); client.IgnoreNotFound(err) != nil {
				t.Logf("delete %s/%s: %v", o.GetKind(), o.GetName(), err)
			}
		}
	}
	deadline := time.Now().Add(confDeleteTimeout)
	for kind, objs := range created {
		for _, o := range objs {
			err := confPoll(time.Until(deadline), func() error {
				cur := &unstructured.Unstructured{}
				cur.SetGroupVersionKind(o.GroupVersionKind())
				if err := k8s.Get(ctx, client.ObjectKeyFromObject(o), cur); apierrors.IsNotFound(err) {
					return nil
				}
				return fmt.Errorf("still present")
			})
			if err == nil {
				continue
			}
			s := confState(ctx, k8s, o)
			if r := byKind[kind]; r != nil {
				r.note("example delete stuck: %s %s", o.GetName(), s.String())
			}
			cur := &unstructured.Unstructured{}
			cur.SetGroupVersionKind(o.GroupVersionKind())
			if err := k8s.Get(ctx, client.ObjectKeyFromObject(o), cur); err == nil {
				patch := client.MergeFrom(cur.DeepCopy())
				cur.SetFinalizers(nil)
				_ = k8s.Patch(ctx, cur, patch)
			}
		}
	}
}

// confCreate creates obj; when the CRD rejects deletionPolicy Delete (kinds without a delete endpoint) it
// retries with the example's own policy.
func confCreate(ctx context.Context, k8s client.Client, obj *unstructured.Unstructured) error {
	err := k8s.Create(ctx, obj)
	if confIsInvalid(err) && strings.Contains(err.Error(), "deletionPolicy") {
		unstructured.RemoveNestedField(obj.Object, "spec", "deletionPolicy")
		err = k8s.Create(ctx, obj)
	}
	return err
}

func confPluginKind(c *confCatalog, kind string) bool {
	for _, k := range c.kinds {
		if k.Kind == kind {
			return k.plugin()
		}
	}
	return false
}

// confUpdateNotes returns the notes of the engine's Updated events for kind (and name, when set) observed at
// or after since; a note names the differing keys, never values.
func confUpdateNotes(ctx context.Context, k8s client.Client, ns, kind, name string, since time.Time) []string {
	list := &unstructured.UnstructuredList{}
	list.SetAPIVersion("events.k8s.io/v1")
	list.SetKind("EventList")
	if err := k8s.List(ctx, list, client.InNamespace(ns)); err != nil {
		return []string{"events: " + err.Error()}
	}
	seen := map[string]bool{}
	var out []string
	for _, ev := range list.Items {
		reason, _, _ := unstructured.NestedString(ev.Object, "reason")
		rk, _, _ := unstructured.NestedString(ev.Object, "regarding", "kind")
		rn, _, _ := unstructured.NestedString(ev.Object, "regarding", "name")
		if reason != "Updated" || rk != kind || (name != "" && rn != name) {
			continue
		}
		last, _, _ := unstructured.NestedString(ev.Object, "series", "lastObservedTime")
		if last == "" {
			last, _, _ = unstructured.NestedString(ev.Object, "eventTime")
		}
		if at, err := time.Parse(time.RFC3339Nano, last); err == nil && at.Before(since.Add(-time.Second)) {
			continue
		}
		note, _, _ := unstructured.NestedString(ev.Object, "note")
		if _, keys, ok := strings.Cut(note, ": "); ok {
			note = keys
		}
		if note = rn + " [" + note + "]"; !seen[note] {
			seen[note] = true
			out = append(out, note)
		}
	}
	return out
}

func confShortErr(err error) string {
	msg := err.Error()
	if len(msg) > 200 {
		msg = msg[:200] + "..."
	}
	return strings.ReplaceAll(msg, "|", "/")
}
