package sys

import (
	"context"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	sysv1alpha1 "github.com/alexandrust88/openbao-config-operator/api/sys/v1alpha1"
)

func TestLogger(t *testing.T) {
	a := LoggerAdapter{}
	ctx := context.Background()
	l := &sysv1alpha1.Logger{
		ObjectMeta: metav1.ObjectMeta{Name: litCoreClusterListener},
		Spec:       sysv1alpha1.LoggerSpec{Level: litDebug},
	}
	if a.Path(l) != "sys/loggers/core.cluster-listener" {
		t.Fatalf("path %q", a.Path(l))
	}
	desired, _ := a.Desired(ctx, nil, l)
	if len(desired) != 1 || desired["level"] != litDebug {
		t.Errorf("desired = %v", desired)
	}

	c := newConfigClient(map[string]map[string]any{a.Path(l): {litCoreClusterListener: litDebug}})
	observed, found, err := a.Observe(ctx, c, l)
	if err != nil || !found {
		t.Fatalf("found=%v err=%v", found, err)
	}
	assertInSync(t, desired, observed, a.Fields())

	c = newConfigClient(map[string]map[string]any{a.Path(l): {litCoreClusterListener: "info"}})
	observed, _, _ = a.Observe(ctx, c, l)
	assertDiffers(t, desired, observed, a.Fields(), "level")

	c = newConfigClient(map[string]map[string]any{a.Path(l): {}})
	if _, found, err := a.Observe(ctx, c, l); err != nil || found {
		t.Errorf("unknown logger: found=%v err=%v", found, err)
	}
	_, _, err = a.Observe(ctx, newConfigClient(nil).WithNamespace(testNamespace), l)
	assertUnsupported(t, err)
}
