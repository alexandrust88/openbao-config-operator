package sys

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	ctrl "sigs.k8s.io/controller-runtime"

	sysv1alpha1 "github.com/alexandrust88/openbao-config-operator/api/sys/v1alpha1"
	"github.com/alexandrust88/openbao-config-operator/internal/engine"
	"github.com/alexandrust88/openbao-config-operator/internal/openbao"
)

// +kubebuilder:rbac:groups=sys.openbaoconfig.io,resources=ratelimitquotas,verbs=get;list;watch;update;patch
// +kubebuilder:rbac:groups=sys.openbaoconfig.io,resources=ratelimitquotas/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=sys.openbaoconfig.io,resources=ratelimitquotas/finalizers,verbs=update

// SetupRateLimitQuota registers the RateLimitQuota controller with the manager.
func SetupRateLimitQuota(mgr ctrl.Manager, conns engine.Connections, opts engine.Options) error {
	r := engine.New[*sysv1alpha1.RateLimitQuota](
		mgr.GetClient(),
		mgr.GetScheme(),
		mgr.GetEventRecorder("ratelimitquota-controller"),
		conns,
		RateLimitQuotaAdapter{},
		func() *sysv1alpha1.RateLimitQuota { return &sysv1alpha1.RateLimitQuota{} },
		opts,
	)
	return r.SetupWithManager(mgr)
}

// RateLimitQuotaAdapter maps a RateLimitQuota onto sys/quotas/rate-limit/<name>.
type RateLimitQuotaAdapter struct{}

var (
	_ engine.Adapter[*sysv1alpha1.RateLimitQuota]  = RateLimitQuotaAdapter{}
	_ engine.Observer[*sysv1alpha1.RateLimitQuota] = RateLimitQuotaAdapter{}
)

// Path implements engine.Adapter.
func (RateLimitQuotaAdapter) Path(q *sysv1alpha1.RateLimitQuota) string {
	return "sys/quotas/rate-limit/" + nameOrDefault(q.Spec.Name, q.Name)
}

// Desired implements engine.Adapter. A write replaces every setting except role, so all of them are sent with
// the server defaults for unset fields: an interval of 1s, no block interval, and inheritable for a global quota.
func (RateLimitQuotaAdapter) Desired(_ context.Context, _ engine.Resolver, q *sysv1alpha1.RateLimitQuota) (engine.Payload, error) {
	rate, err := strconv.ParseFloat(q.Spec.Rate, 64)
	if err != nil {
		return nil, fmt.Errorf("spec.rate %q is not a number", q.Spec.Rate)
	}
	p := engine.Payload{
		"rate":           rate,
		"path":           q.Spec.Path,
		"interval":       int64(1),
		keyBlockInterval: int64(0),
		"inheritable":    q.Spec.Path == "",
	}
	if q.Spec.Role != "" {
		p["role"] = q.Spec.Role
	}
	putSeconds(p, "interval", q.Spec.Interval)
	putSeconds(p, keyBlockInterval, q.Spec.BlockInterval)
	putBool(p, "inheritable", q.Spec.Inheritable)
	return p, nil
}

// Fields implements engine.Adapter.
func (RateLimitQuotaAdapter) Fields() engine.FieldSpec {
	return engine.FieldSpec{Durations: []string{"interval", keyBlockInterval}}
}

// Observe implements engine.Observer. Quotas exist only in the root namespace, so a connection scoped to a child
// namespace is unsupported. OpenBao returns a mount path with a trailing slash ("team/kv/"), so a stored path that
// differs from the spec only in surrounding slashes is reported as the spec path. An update keeps the stored
// role, so a different one is an error instead of a write that changes nothing.
func (a RateLimitQuotaAdapter) Observe(ctx context.Context, c openbao.Client, q *sysv1alpha1.RateLimitQuota) (engine.Payload, bool, error) {
	if err := requireRootNamespace(c, "RateLimitQuota"); err != nil {
		return nil, false, err
	}
	observed, err := c.Read(ctx, a.Path(q))
	if errors.Is(err, openbao.ErrNotFound) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	if role, _ := observed["role"].(string); role != q.Spec.Role {
		return nil, false, fmt.Errorf("the quota %s has role %q, the spec wants %q; it can only be set when the quota is created",
			a.Path(q), role, q.Spec.Role)
	}
	if stored, ok := observed["path"].(string); ok && strings.Trim(stored, "/") == strings.Trim(q.Spec.Path, "/") {
		observed["path"] = q.Spec.Path
	}
	return observed, true, nil
}
