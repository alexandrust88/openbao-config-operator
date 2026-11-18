package secrets

import (
	"context"

	ctrl "sigs.k8s.io/controller-runtime"

	secretsv1alpha1 "github.com/alexandrust88/openbao-config-operator/api/secrets/v1alpha1"
	"github.com/alexandrust88/openbao-config-operator/internal/engine"
)

// +kubebuilder:rbac:groups=secrets.obco.dev,resources=transitkeyconfigs,verbs=get;list;watch;update;patch
// +kubebuilder:rbac:groups=secrets.obco.dev,resources=transitkeyconfigs/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=secrets.obco.dev,resources=transitkeyconfigs/finalizers,verbs=update

// SetupTransitKeyConfig registers the TransitKeyConfig controller with the manager.
func SetupTransitKeyConfig(mgr ctrl.Manager, conns engine.Connections, opts engine.Options) error {
	r := engine.New[*secretsv1alpha1.TransitKeyConfig](
		mgr.GetClient(),
		mgr.GetScheme(),
		mgr.GetEventRecorder("transitkeyconfig-controller"),
		conns,
		TransitKeyConfigAdapter{},
		func() *secretsv1alpha1.TransitKeyConfig { return &secretsv1alpha1.TransitKeyConfig{} },
		opts,
	)
	return r.SetupWithManager(mgr)
}

// TransitKeyConfigAdapter maps a TransitKeyConfig onto <mount>/config/keys. The path has no delete, so the default policy is Orphan.
type TransitKeyConfigAdapter struct{}

var (
	_ engine.Adapter[*secretsv1alpha1.TransitKeyConfig] = TransitKeyConfigAdapter{}
	_ engine.Singleton                                  = TransitKeyConfigAdapter{}
)

// Path implements engine.Adapter.
func (TransitKeyConfigAdapter) Path(c *secretsv1alpha1.TransitKeyConfig) string {
	return c.Spec.MountPath + "/config/keys"
}

// Desired implements engine.Adapter.
func (TransitKeyConfigAdapter) Desired(_ context.Context, _ engine.Resolver, c *secretsv1alpha1.TransitKeyConfig) (engine.Payload, error) {
	p := engine.Payload{}
	orFalse(p, "disable_upsert", c.Spec.DisableUpsert)
	return p, nil
}

// Fields implements engine.Adapter.
func (TransitKeyConfigAdapter) Fields() engine.FieldSpec {
	return engine.FieldSpec{}
}

// Singleton implements engine.Singleton: the setting always exists, so it is adopted whatever the creationPolicy.
func (TransitKeyConfigAdapter) Singleton() bool { return true }
