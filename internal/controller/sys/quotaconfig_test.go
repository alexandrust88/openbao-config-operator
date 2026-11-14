package sys

import (
	"context"
	"slices"
	"testing"

	sysv1alpha1 "github.com/alexandrust88/openbao-config-operator/api/sys/v1alpha1"
)

// quotaDefaultRead is the read of sys/quotas/config on a fresh server.
func quotaDefaultRead() map[string]any {
	paths := make([]any, 0, len(quotaDefaultExemptPaths))
	for _, p := range quotaDefaultExemptPaths {
		paths = append(paths, p)
	}
	return map[string]any{
		"enable_rate_limit_audit_logging":    false,
		"enable_rate_limit_response_headers": false,
		"rate_limit_exempt_paths":            paths,
	}
}

func TestQuotaConfigDesired(t *testing.T) {
	a := QuotaConfigAdapter{}
	ctx := context.Background()
	unset, _ := a.Desired(ctx, nil, &sysv1alpha1.QuotaConfig{})
	if !slices.Equal(unset["rate_limit_exempt_paths"].([]string), quotaDefaultExemptPaths) {
		t.Errorf("unset exempt paths = %v, want the OpenBao defaults", unset["rate_limit_exempt_paths"])
	}
	empty, _ := a.Desired(ctx, nil, &sysv1alpha1.QuotaConfig{Spec: sysv1alpha1.QuotaConfigSpec{RateLimitExemptPaths: []string{}}})
	if len(empty["rate_limit_exempt_paths"].([]string)) != 0 {
		t.Errorf("empty exempt paths = %v", empty["rate_limit_exempt_paths"])
	}
	if len(unset) != 3 || unset["enable_rate_limit_audit_logging"] != false {
		t.Errorf("desired = %v, want all three fields", unset)
	}
}

func TestQuotaConfigObserve(t *testing.T) {
	a := QuotaConfigAdapter{}
	ctx := context.Background()
	q := &sysv1alpha1.QuotaConfig{}
	c := newConfigClient(map[string]map[string]any{quotaConfigPath: quotaDefaultRead()})
	observed, found, err := a.Observe(ctx, c, q)
	if err != nil || !found {
		t.Fatalf("found=%v err=%v", found, err)
	}
	desired, _ := a.Desired(ctx, nil, q)
	assertInSync(t, desired, observed, a.Fields())

	q.Spec.EnableRateLimitAuditLogging = true
	desired, _ = a.Desired(ctx, nil, q)
	assertDiffers(t, desired, observed, a.Fields(), "enable_rate_limit_audit_logging")

	_, _, err = a.Observe(ctx, newConfigClient(nil).WithNamespace(testNamespace), q)
	assertUnsupported(t, err)
}

func TestQuotaConfigDeleteResets(t *testing.T) {
	c := newConfigClient(nil)
	if err := (QuotaConfigAdapter{}).Delete(context.Background(), c, &sysv1alpha1.QuotaConfig{}); err != nil {
		t.Fatal(err)
	}
	written := c.writes[quotaConfigPath]
	if written["enable_rate_limit_audit_logging"] != false || !slices.Equal(written["rate_limit_exempt_paths"].([]string), quotaDefaultExemptPaths) {
		t.Errorf("reset payload = %v", written)
	}
}
