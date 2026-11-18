//go:build integration

package livekit

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func verifyPausedPolicies(ctx context.Context, t testing.TB, k8s client.Client, r *run) {
	t.Helper()
	for _, obj := range r.objs {
		if obj.GetKind() != "ACLPolicy" || obj.GetAnnotations()["obco.dev/paused"] != "true" {
			continue
		}
		cur := obj.DeepCopy()
		if err := k8s.Get(ctx, client.ObjectKeyFromObject(obj), cur); err != nil {
			t.Fatal(err)
		}
		conditions, _, _ := unstructured.NestedSlice(cur.Object, "status", "conditions")
		paused := false
		if len(conditions) == 1 {
			condition, ok := conditions[0].(map[string]any)
			paused = ok && condition["type"] == "Synced" && condition["status"] == "False" &&
				condition["reason"] == "Paused" && condition["observedGeneration"] == cur.GetGeneration()
		}
		if !paused {
			t.Errorf("%s: paused policy %s must report current Synced=False/Paused without Ready", r.overlay, obj.GetName())
		}
		name, _, _ := unstructured.NestedString(obj.Object, keySpec, keyName)
		if name == "" {
			name = obj.GetName()
		}
		_, code, err := r.bao.do(resourceNamespace(t, r.objs, obj), http.MethodGet, "sys/policies/acl/"+name, nil)
		if code != http.StatusNotFound {
			t.Errorf("%s: paused policy %s exists or cannot be checked: HTTP %d: %v", r.overlay, obj.GetName(), code, err)
		}
	}
}

func resourceNamespace(t testing.TB, objs []*unstructured.Unstructured, obj *unstructured.Unstructured) string {
	t.Helper()
	name, _, _ := unstructured.NestedString(obj.Object, keySpec, "connectionRef", keyName)
	kind, _, _ := unstructured.NestedString(obj.Object, keySpec, "connectionRef", "kind")
	if kind == "" {
		kind = "BaoConnection"
	}
	for _, conn := range objs {
		if conn.GetKind() != kind || conn.GetName() != name {
			continue
		}
		base, _, _ := unstructured.NestedString(conn.Object, keySpec, "openbaoNamespace")
		target, _, _ := unstructured.NestedString(obj.Object, keySpec, "openbaoNamespace")
		return strings.Trim(strings.Trim(base, "/")+"/"+strings.Trim(target, "/"), "/")
	}
	t.Fatalf("%s/%s has no rendered connection %s/%s", obj.GetKind(), obj.GetName(), kind, name)
	return ""
}
