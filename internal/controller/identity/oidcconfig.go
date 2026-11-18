package identity

import (
	"context"

	ctrl "sigs.k8s.io/controller-runtime"

	identityv1alpha1 "github.com/alexandrust88/openbao-config-operator/api/identity/v1alpha1"
	"github.com/alexandrust88/openbao-config-operator/internal/engine"
	"github.com/alexandrust88/openbao-config-operator/internal/openbao"
)

// +kubebuilder:rbac:groups=identity.obco.dev,resources=oidcconfigs,verbs=get;list;watch;update;patch
// +kubebuilder:rbac:groups=identity.obco.dev,resources=oidcconfigs/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=identity.obco.dev,resources=oidcconfigs/finalizers,verbs=update

const oidcConfigPath = "identity/oidc/config"

// SetupOIDCConfig registers the OIDCConfig controller with the manager.
func SetupOIDCConfig(mgr ctrl.Manager, conns engine.Connections, opts engine.Options) error {
	r := engine.New[*identityv1alpha1.OIDCConfig](
		mgr.GetClient(),
		mgr.GetScheme(),
		mgr.GetEventRecorder("oidcconfig-controller"),
		conns,
		OIDCConfigAdapter{},
		func() *identityv1alpha1.OIDCConfig { return &identityv1alpha1.OIDCConfig{} },
		opts,
	)
	return r.SetupWithManager(mgr)
}

// OIDCConfigAdapter maps an OIDCConfig onto the identity/oidc/config singleton.
type OIDCConfigAdapter struct{}

var (
	_ engine.Adapter[*identityv1alpha1.OIDCConfig] = OIDCConfigAdapter{}
	_ engine.Singleton                             = OIDCConfigAdapter{}
	_ engine.Deleter[*identityv1alpha1.OIDCConfig] = OIDCConfigAdapter{}
)

// Path implements engine.Adapter.
func (OIDCConfigAdapter) Path(*identityv1alpha1.OIDCConfig) string { return oidcConfigPath }

// Desired implements engine.Adapter.
func (OIDCConfigAdapter) Desired(_ context.Context, _ engine.Resolver, o *identityv1alpha1.OIDCConfig) (engine.Payload, error) {
	return engine.Payload{"issuer": o.Spec.Issuer}, nil
}

// Fields implements engine.Adapter.
func (OIDCConfigAdapter) Fields() engine.FieldSpec { return engine.FieldSpec{} }

// Singleton implements engine.Singleton: the configuration always exists, so it is adopted whatever the
// creationPolicy.
func (OIDCConfigAdapter) Singleton() bool { return true }

// Delete implements engine.Deleter. The singleton has no DELETE; an empty issuer restores the server default.
func (OIDCConfigAdapter) Delete(ctx context.Context, c openbao.Client, _ *identityv1alpha1.OIDCConfig) error {
	_, err := c.Write(ctx, oidcConfigPath, engine.Payload{"issuer": ""})
	return err
}
