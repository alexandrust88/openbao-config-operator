package sys

import (
	"context"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	sysv1alpha1 "github.com/alexandrust88/openbao-config-operator/api/sys/v1alpha1"
)

func newUIHeader(specName string, values ...string) *sysv1alpha1.UIHeader {
	return &sysv1alpha1.UIHeader{
		ObjectMeta: metav1.ObjectMeta{Name: "csp", Namespace: testNamespace},
		Spec:       sysv1alpha1.UIHeaderSpec{Name: specName, Values: values},
	}
}

func TestUIHeaderPath(t *testing.T) {
	a := UIHeaderAdapter{}
	if got := a.Path(newUIHeader("", "x")); got != "sys/config/ui/headers/csp" {
		t.Errorf("default name: %q", got)
	}
	if got := a.Path(newUIHeader("Content-Security-Policy", "x")); got != "sys/config/ui/headers/Content-Security-Policy" {
		t.Errorf("spec name: %q", got)
	}
}

func TestUIHeaderObserve(t *testing.T) {
	a := UIHeaderAdapter{}
	ctx := context.Background()
	h := newUIHeader("X-Frame", "DENY", "SAMEORIGIN")
	desired, _ := a.Desired(ctx, nil, h)
	path := a.Path(h)

	c := newConfigClient(map[string]map[string]any{path: {"value": "DENY"}})
	observed, found, err := a.Observe(ctx, c, h)
	if err != nil || !found {
		t.Fatalf("found=%v err=%v", found, err)
	}
	assertInSync(t, desired, observed, a.Fields())

	c = newConfigClient(map[string]map[string]any{path: {"value": "ALLOW"}})
	observed, _, _ = a.Observe(ctx, c, h)
	assertDiffers(t, desired, observed, a.Fields(), "values")

	if _, found, err := a.Observe(ctx, newConfigClient(nil), h); err != nil || found {
		t.Errorf("missing header: found=%v err=%v", found, err)
	}
	_, _, err = a.Observe(ctx, newConfigClient(nil).WithNamespace(testNamespace), h)
	assertUnsupported(t, err)
}
