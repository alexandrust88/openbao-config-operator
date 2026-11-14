package sys

import (
	"context"
	"slices"

	ctrl "sigs.k8s.io/controller-runtime"

	sysv1alpha1 "github.com/alexandrust88/openbao-config-operator/api/sys/v1alpha1"
	"github.com/alexandrust88/openbao-config-operator/internal/engine"
	"github.com/alexandrust88/openbao-config-operator/internal/openbao"
)

// +kubebuilder:rbac:groups=sys.openbaoconfig.io,resources=quotaconfigs,verbs=get;list;watch;update;patch
// +kubebuilder:rbac:groups=sys.openbaoconfig.io,resources=quotaconfigs/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=sys.openbaoconfig.io,resources=quotaconfigs/finalizers,verbs=update

const (
	quotaConfigPath  = "sys/quotas/config"
	quotaExemptPaths = "rate_limit_exempt_paths"
)

// quotaDefaultExemptPaths is the OpenBao default of rate_limit_exempt_paths (vault/quotas/quotas.go).
var quotaDefaultExemptPaths = []string{
	"sys/generate-recovery-token/attempt",
	"sys/generate-recovery-token/update",
	"sys/generate-root/attempt",
	"sys/generate-root/update",
	"sys/health",
	"sys/seal-status",
	"sys/unseal",
}

// SetupQuotaConfig registers the QuotaConfig controller with the manager.
func SetupQuotaConfig(mgr ctrl.Manager, conns engine.Connections, opts engine.Options) error {
	r := engine.New[*sysv1alpha1.QuotaConfig](
		mgr.GetClient(),
		mgr.GetScheme(),
		mgr.GetEventRecorder("quotaconfig-controller"),
		conns,
		QuotaConfigAdapter{},
		func() *sysv1alpha1.QuotaConfig { return &sysv1alpha1.QuotaConfig{} },
		opts,
	)
	return r.SetupWithManager(mgr)
}

// QuotaConfigAdapter maps a QuotaConfig onto sys/quotas/config. The path has no DELETE; deleting resets it.
type QuotaConfigAdapter struct{}

var (
	_ engine.Adapter[*sysv1alpha1.QuotaConfig]  = QuotaConfigAdapter{}
	_ engine.Observer[*sysv1alpha1.QuotaConfig] = QuotaConfigAdapter{}
	_ engine.Deleter[*sysv1alpha1.QuotaConfig]  = QuotaConfigAdapter{}
	_ engine.Singleton                          = QuotaConfigAdapter{}
)

// Path implements engine.Adapter.
func (QuotaConfigAdapter) Path(*sysv1alpha1.QuotaConfig) string { return quotaConfigPath }

// Desired implements engine.Adapter. Every write replaces the whole configuration, so all fields are sent;
// an unset rateLimitExemptPaths keeps the OpenBao defaults.
func (QuotaConfigAdapter) Desired(_ context.Context, _ engine.Resolver, q *sysv1alpha1.QuotaConfig) (engine.Payload, error) {
	paths := q.Spec.RateLimitExemptPaths
	if paths == nil {
		paths = slices.Clone(quotaDefaultExemptPaths)
	}
	return engine.Payload{
		quotaExemptPaths:                     paths,
		"enable_rate_limit_audit_logging":    q.Spec.EnableRateLimitAuditLogging,
		"enable_rate_limit_response_headers": q.Spec.EnableRateLimitResponseHeaders,
	}, nil
}

// Fields implements engine.Adapter.
func (QuotaConfigAdapter) Fields() engine.FieldSpec {
	return engine.FieldSpec{Sets: []string{quotaExemptPaths}}
}

// Observe implements engine.Observer.
func (QuotaConfigAdapter) Observe(ctx context.Context, c openbao.Client, _ *sysv1alpha1.QuotaConfig) (engine.Payload, bool, error) {
	if err := requireRootNamespace(c, "QuotaConfig"); err != nil {
		return nil, false, err
	}
	observed, err := c.Read(ctx, quotaConfigPath)
	if err != nil {
		return nil, false, err
	}
	return observed, true, nil
}

// Delete implements engine.Deleter by writing the OpenBao defaults.
func (QuotaConfigAdapter) Delete(ctx context.Context, c openbao.Client, _ *sysv1alpha1.QuotaConfig) error {
	_, err := c.Write(ctx, quotaConfigPath, engine.Payload{
		quotaExemptPaths:                     quotaDefaultExemptPaths,
		"enable_rate_limit_audit_logging":    false,
		"enable_rate_limit_response_headers": false,
	})
	return err
}

// Singleton implements engine.Singleton: the setting always exists, so it is adopted whatever the creationPolicy.
func (QuotaConfigAdapter) Singleton() bool { return true }
