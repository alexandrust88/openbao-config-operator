package sys

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/events"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	commonv1alpha1 "github.com/alexandrust88/openbao-config-operator/api/common/v1alpha1"
	sysv1alpha1 "github.com/alexandrust88/openbao-config-operator/api/sys/v1alpha1"
	"github.com/alexandrust88/openbao-config-operator/internal/engine"
	"github.com/alexandrust88/openbao-config-operator/internal/openbao"
)

const testWorkflow = "flow \"health\" {\n  request \"read\" {\n    operation = \"read\"\n    path = \"sys/health\"\n  }\n}\n"

func newWorkflow() *sysv1alpha1.Workflow {
	return &sysv1alpha1.Workflow{
		ObjectMeta: metav1.ObjectMeta{Name: "health"},
		Spec: sysv1alpha1.WorkflowSpec{
			Path: "team/health", Description: "reads health", Workflow: testWorkflow, CASRequired: true,
		},
	}
}

func workflowRead(text string) map[string]any {
	return map[string]any{
		litPath: "team/health", litVersion: json.Number("3"), "workflow": text, litDescription: "reads health",
		keyCASRequired: true, "allow_unauthenticated": false,
	}
}

func TestWorkflowDesiredAndObserve(t *testing.T) {
	a := WorkflowAdapter{}
	ctx := context.Background()
	w := newWorkflow()
	if a.Path(w) != "sys/workflows/manage/team/health" {
		t.Fatalf("path %q", a.Path(w))
	}
	desired, _ := a.Desired(ctx, nil, w)
	if len(desired) != 4 || desired["allow_unauthenticated"] != false {
		t.Errorf("desired = %v, want all four fields", desired)
	}

	reformatted := "\nflow \"health\" {\r\n\trequest \"read\" {\n operation = \"read\"\n path = \"sys/health\"\n}\n}"
	c := newConfigClient(map[string]map[string]any{a.Path(w): workflowRead(reformatted)})
	observed, found, err := a.Observe(ctx, c, w)
	if err != nil || !found {
		t.Fatalf("found=%v err=%v", found, err)
	}
	assertInSync(t, desired, observed, a.Fields())

	c = newConfigClient(map[string]map[string]any{a.Path(w): workflowRead(`flow "other" {}`)})
	observed, _, _ = a.Observe(ctx, c, w)
	assertDiffers(t, desired, observed, a.Fields(), "workflow")

	if _, found, err := a.Observe(ctx, newConfigClient(nil), w); err != nil || found {
		t.Errorf("missing workflow: found=%v err=%v", found, err)
	}
}

func TestWorkflowWriteSendsCAS(t *testing.T) {
	a := WorkflowAdapter{}
	ctx := context.Background()
	w := newWorkflow()
	path := a.Path(w)
	desired, _ := a.Desired(ctx, nil, w)

	c := newConfigClient(nil)
	if _, err := a.Write(ctx, c, w, desired, true); err != nil {
		t.Fatal(err)
	}
	if c.writes[path]["cas"] != -1 {
		t.Errorf("create cas = %v, want -1", c.writes[path]["cas"])
	}
	if _, ok := desired["cas"]; ok {
		t.Error("Write mutated the desired payload")
	}

	c = newConfigClient(map[string]map[string]any{path: workflowRead(testWorkflow)})
	if _, err := a.Write(ctx, c, w, desired, false); err != nil {
		t.Fatal(err)
	}
	if c.writes[path]["cas"] != json.Number("3") {
		t.Errorf("update cas = %v, want the current version 3", c.writes[path]["cas"])
	}

	c = newConfigClient(nil)
	c.errs["write "+path] = &openbao.ResponseError{StatusCode: 404, Errors: []string{"unsupported path"}}
	_, err := a.Write(ctx, c, w, desired, true)
	assertUnsupported(t, err)
}

// TestWorkflowMissingNamespaceIsReferenceNotReady: a workflow in an OpenBao namespace that does not exist yet waits
// for it (ReferenceNotReady) instead of being reported as a server without the workflows API.
func TestWorkflowMissingNamespaceIsReferenceNotReady(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := errors.Join(corev1.AddToScheme(scheme), sysv1alpha1.AddToScheme(scheme)); err != nil {
		t.Fatal(err)
	}
	w := newWorkflow()
	w.Namespace = testNamespace
	k8s := fake.NewClientBuilder().WithScheme(scheme).WithStatusSubresource(w).WithObjects(w).
		WithIndex(w, engine.PathIndex, engine.PathIndexFunc).Build()
	path := WorkflowAdapter{}.Path(w)
	bao := newConfigClient(nil)
	missing := &openbao.ResponseError{StatusCode: 404, Errors: []string{"namespace not found"}}
	bao.errs["read "+path], bao.errs["write "+path] = missing, missing
	r := engine.New(k8s, scheme, events.NewFakeRecorder(100), sysRevConns{bao}, WorkflowAdapter{},
		func() *sysv1alpha1.Workflow { return &sysv1alpha1.Workflow{} }, engine.Options{})
	_, _ = r.Reconcile(context.Background(), ctrl.Request{NamespacedName: client.ObjectKeyFromObject(w)})

	got := &sysv1alpha1.Workflow{}
	if err := k8s.Get(context.Background(), client.ObjectKeyFromObject(w), got); err != nil {
		t.Fatal(err)
	}
	if synced := meta.FindStatusCondition(got.Status.Conditions, commonv1alpha1.ConditionSynced); synced == nil ||
		synced.Reason != commonv1alpha1.ReasonReferenceNotReady {
		t.Errorf("Synced = %+v, want reason %s", synced, commonv1alpha1.ReasonReferenceNotReady)
	}
}
