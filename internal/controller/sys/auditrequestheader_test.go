package sys

import (
	"context"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	sysv1alpha1 "github.com/alexandrust88/openbao-config-operator/api/sys/v1alpha1"
)

func TestAuditRequestHeader(t *testing.T) {
	a := AuditRequestHeaderAdapter{}
	ctx := context.Background()
	h := &sysv1alpha1.AuditRequestHeader{
		ObjectMeta: metav1.ObjectMeta{Name: "forwarded-for"},
		Spec:       sysv1alpha1.AuditRequestHeaderSpec{Name: litXForwardedFor, HMAC: true},
	}
	path := a.Path(h)
	if path != "sys/config/auditing/request-headers/X-Forwarded-For" {
		t.Fatalf("path %q", path)
	}
	desired, _ := a.Desired(ctx, nil, h)

	c := newConfigClient(map[string]map[string]any{path: {litXForwardedFor: map[string]any{"hmac": true}}})
	observed, found, err := a.Observe(ctx, c, h)
	if err != nil || !found {
		t.Fatalf("found=%v err=%v", found, err)
	}
	assertInSync(t, desired, observed, a.Fields())

	c = newConfigClient(map[string]map[string]any{path: {litXForwardedFor: map[string]any{"hmac": false}}})
	observed, _, _ = a.Observe(ctx, c, h)
	assertDiffers(t, desired, observed, a.Fields(), "hmac")

	c = newConfigClient(nil)
	c.errs["read "+path] = badRequest(auditHeaderMissing)
	if _, found, err := a.Observe(ctx, c, h); err != nil || found {
		t.Errorf("unaudited header: found=%v err=%v, want absent", found, err)
	}
	c.errs["read "+path] = badRequest("permission denied")
	if _, _, err := a.Observe(ctx, c, h); err == nil {
		t.Error("other 400 must stay an error")
	}
	_, _, err = a.Observe(ctx, newConfigClient(nil).WithNamespace(testNamespace), h)
	assertUnsupported(t, err)
}
