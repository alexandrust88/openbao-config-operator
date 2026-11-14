package auth

import (
	"context"
	"maps"
	"strings"

	ctrl "sigs.k8s.io/controller-runtime"

	authv1alpha1 "github.com/alexandrust88/openbao-config-operator/api/auth/v1alpha1"
	"github.com/alexandrust88/openbao-config-operator/internal/engine"
	"github.com/alexandrust88/openbao-config-operator/internal/openbao"
)

// +kubebuilder:rbac:groups=auth.openbaoconfig.io,resources=radiusauthconfigs,verbs=get;list;watch;update;patch
// +kubebuilder:rbac:groups=auth.openbaoconfig.io,resources=radiusauthconfigs/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=auth.openbaoconfig.io,resources=radiusauthconfigs/finalizers,verbs=update
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch

// SetupRADIUSAuthConfig registers the RADIUSAuthConfig controller with the manager.
func SetupRADIUSAuthConfig(mgr ctrl.Manager, conns engine.Connections, opts engine.Options) error {
	return setupPluginKind(mgr, conns, opts, "radiusauthconfig-controller", RADIUSAuthConfigAdapter{},
		func() *authv1alpha1.RADIUSAuthConfig { return &authv1alpha1.RADIUSAuthConfig{} })
}

const radiusUnregisteredPolicies = "unregistered_user_policies"

// RADIUSAuthConfigAdapter maps a RADIUSAuthConfig onto auth/<mountPath>/config of a RADIUS auth method.
type RADIUSAuthConfigAdapter struct{}

var (
	_ engine.Adapter[*authv1alpha1.RADIUSAuthConfig]          = RADIUSAuthConfigAdapter{}
	_ engine.DriftTier                                        = RADIUSAuthConfigAdapter{}
	_ engine.SecretReferencer[*authv1alpha1.RADIUSAuthConfig] = RADIUSAuthConfigAdapter{}
	_ engine.Writer[*authv1alpha1.RADIUSAuthConfig]           = RADIUSAuthConfigAdapter{}
)

// Path implements engine.Adapter.
func (RADIUSAuthConfigAdapter) Path(o *authv1alpha1.RADIUSAuthConfig) string {
	return "auth/" + authMount(o.Spec.MountPath, "radius") + "/config"
}

// Desired implements engine.Adapter.
func (RADIUSAuthConfigAdapter) Desired(ctx context.Context, r engine.Resolver, o *authv1alpha1.RADIUSAuthConfig) (engine.Payload, error) {
	secret, err := authSecret(ctx, r, o.Namespace, o.Spec.SecretSecretRef)
	if err != nil {
		return nil, err
	}
	p := engine.Payload{"host": o.Spec.Host, "secret": secret}
	pluginPut(p, "port", o.Spec.Port)
	putList(p, radiusUnregisteredPolicies, o.Spec.UnregisteredUserPolicies)
	putSeconds(p, "dial_timeout", o.Spec.DialTimeout)
	putSeconds(p, "read_timeout", o.Spec.ReadTimeout)
	pluginPut(p, "nas_port", o.Spec.NASPort)
	putString(p, "nas_identifier", o.Spec.NASIdentifier)
	if err := putTokenFields(ctx, r, o, p, o.Spec.TokenFields); err != nil {
		return nil, err
	}
	return p, nil
}

// Fields implements engine.Adapter.
func (RADIUSAuthConfigAdapter) Fields() engine.FieldSpec {
	return MergeFieldSpecs(engine.FieldSpec{
		WriteOnly: []string{"secret"},
		Sets:      []string{radiusUnregisteredPolicies},
		Durations: []string{"dial_timeout", "read_timeout"},
	}, TokenFieldSpec)
}

// DriftTier implements engine.DriftTier.
func (RADIUSAuthConfigAdapter) DriftTier() engine.Tier { return engine.TierAccessCritical }

// SecretRefs implements engine.SecretReferencer.
func (RADIUSAuthConfigAdapter) SecretRefs(o *authv1alpha1.RADIUSAuthConfig) []string {
	return []string{o.Spec.SecretSecretRef.Name}
}

// Observe implements engine.Observer. When the object is absent it checks that the auth method is enabled.
func (a RADIUSAuthConfigAdapter) Observe(ctx context.Context, c openbao.Client, o *authv1alpha1.RADIUSAuthConfig) (engine.Payload, bool, error) {
	return observeExternalAuth(ctx, c, a.Path(o), authMount(o.Spec.MountPath, "radius"), "radius")
}

// Write implements engine.Writer. OpenBao takes unregistered_user_policies as one comma-separated string and reads
// it back as a list.
func (a RADIUSAuthConfigAdapter) Write(ctx context.Context, c openbao.Client, o *authv1alpha1.RADIUSAuthConfig,
	desired engine.Payload, _ bool) (engine.Payload, error) {
	payload := maps.Clone(desired)
	if policies, ok := payload[radiusUnregisteredPolicies].([]string); ok {
		payload[radiusUnregisteredPolicies] = strings.Join(policies, ",")
	}
	return c.Write(ctx, a.Path(o), payload)
}
