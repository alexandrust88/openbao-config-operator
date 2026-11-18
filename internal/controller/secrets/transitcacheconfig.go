package secrets

import (
	"context"
	"errors"

	ctrl "sigs.k8s.io/controller-runtime"

	secretsv1alpha1 "github.com/alexandrust88/openbao-config-operator/api/secrets/v1alpha1"
	"github.com/alexandrust88/openbao-config-operator/internal/engine"
	"github.com/alexandrust88/openbao-config-operator/internal/openbao"
)

// +kubebuilder:rbac:groups=secrets.obco.dev,resources=transitcacheconfigs,verbs=get;list;watch;update;patch
// +kubebuilder:rbac:groups=secrets.obco.dev,resources=transitcacheconfigs/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=secrets.obco.dev,resources=transitcacheconfigs/finalizers,verbs=update

// SetupTransitCacheConfig registers the TransitCacheConfig controller with the manager.
func SetupTransitCacheConfig(mgr ctrl.Manager, conns engine.Connections, opts engine.Options) error {
	r := engine.New[*secretsv1alpha1.TransitCacheConfig](
		mgr.GetClient(),
		mgr.GetScheme(),
		mgr.GetEventRecorder("transitcacheconfig-controller"),
		conns,
		TransitCacheConfigAdapter{},
		func() *secretsv1alpha1.TransitCacheConfig { return &secretsv1alpha1.TransitCacheConfig{} },
		opts,
	)
	return r.SetupWithManager(mgr)
}

// TransitCacheConfigAdapter maps a TransitCacheConfig onto <mount>/cache-config. The path has no delete, so the default policy is Orphan.
type TransitCacheConfigAdapter struct{}

var (
	_ engine.Adapter[*secretsv1alpha1.TransitCacheConfig]  = TransitCacheConfigAdapter{}
	_ engine.Observer[*secretsv1alpha1.TransitCacheConfig] = TransitCacheConfigAdapter{}
	_ engine.Singleton                                     = TransitCacheConfigAdapter{}
)

// Path implements engine.Adapter.
func (TransitCacheConfigAdapter) Path(c *secretsv1alpha1.TransitCacheConfig) string {
	return c.Spec.MountPath + "/cache-config"
}

// Desired implements engine.Adapter.
func (TransitCacheConfigAdapter) Desired(_ context.Context, _ engine.Resolver, c *secretsv1alpha1.TransitCacheConfig) (engine.Payload, error) {
	p := engine.Payload{}
	orInt32(p, "size", c.Spec.Size, 0)
	return p, nil
}

// Fields implements engine.Adapter.
func (TransitCacheConfigAdapter) Fields() engine.FieldSpec {
	return engine.FieldSpec{}
}

// Singleton implements engine.Singleton: the setting always exists, so it is adopted whatever the creationPolicy.
func (TransitCacheConfigAdapter) Singleton() bool { return true }

// Observe implements engine.Observer. A read that reports caching disabled is returned as an error:
// size 0 is an unlimited cache, and treating the error as absent makes every reconcile write again.
func (TransitCacheConfigAdapter) Observe(ctx context.Context, c openbao.Client, cfg *secretsv1alpha1.TransitCacheConfig) (engine.Payload, bool, error) {
	observed, err := c.Read(ctx, TransitCacheConfigAdapter{}.Path(cfg))
	if errors.Is(err, openbao.ErrNotFound) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	size, ok := asInt64(observed["size"])
	if !ok {
		return engine.Payload{"size": observed["size"]}, true, nil
	}
	return engine.Payload{"size": size}, true, nil
}
