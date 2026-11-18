package identity

import (
	"context"

	ctrl "sigs.k8s.io/controller-runtime"

	identityv1alpha1 "github.com/alexandrust88/openbao-config-operator/api/identity/v1alpha1"
	"github.com/alexandrust88/openbao-config-operator/internal/engine"
	"github.com/alexandrust88/openbao-config-operator/internal/openbao"
)

// +kubebuilder:rbac:groups=identity.obco.dev,resources=oidckeys,verbs=get;list;watch;update;patch
// +kubebuilder:rbac:groups=identity.obco.dev,resources=oidckeys/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=identity.obco.dev,resources=oidckeys/finalizers,verbs=update

// builtinOIDCName is the name of the key and the provider that OpenBao creates and refuses to delete.
const builtinOIDCName = "default"

// SetupOIDCKey registers the OIDCKey controller with the manager.
func SetupOIDCKey(mgr ctrl.Manager, conns engine.Connections, opts engine.Options) error {
	r := engine.New[*identityv1alpha1.OIDCKey](
		mgr.GetClient(),
		mgr.GetScheme(),
		mgr.GetEventRecorder("oidckey-controller"),
		conns,
		OIDCKeyAdapter{},
		func() *identityv1alpha1.OIDCKey { return &identityv1alpha1.OIDCKey{} },
		opts,
	)
	return r.SetupWithManager(mgr)
}

// OIDCKeyAdapter maps an OIDCKey onto identity/oidc/key/<name>.
type OIDCKeyAdapter struct{}

var (
	_ engine.Adapter[*identityv1alpha1.OIDCKey] = OIDCKeyAdapter{}
	_ engine.Deleter[*identityv1alpha1.OIDCKey] = OIDCKeyAdapter{}
)

// Path implements engine.Adapter.
func (OIDCKeyAdapter) Path(k *identityv1alpha1.OIDCKey) string {
	return "identity/oidc/key/" + oidcName(k.Spec.Name, k.Name)
}

// Desired implements engine.Adapter.
func (OIDCKeyAdapter) Desired(_ context.Context, _ engine.Resolver, k *identityv1alpha1.OIDCKey) (engine.Payload, error) {
	return engine.Payload{
		"rotation_period":    secondsOr(k.Spec.RotationPeriod, oidcDefaultTTL),
		"verification_ttl":   secondsOr(k.Spec.VerificationTTL, oidcDefaultTTL),
		"algorithm":          stringOr(string(k.Spec.Algorithm), "RS256"),
		"allowed_client_ids": listOrEmpty(k.Spec.AllowedClientIDs),
	}, nil
}

// Fields implements engine.Adapter.
func (OIDCKeyAdapter) Fields() engine.FieldSpec {
	return engine.FieldSpec{
		Sets:      []string{"allowed_client_ids"},
		Durations: []string{"rotation_period", "verification_ttl"},
	}
}

// Delete implements engine.Deleter. The built-in default key is never deleted.
func (a OIDCKeyAdapter) Delete(ctx context.Context, c openbao.Client, k *identityv1alpha1.OIDCKey) error {
	if oidcName(k.Spec.Name, k.Name) == builtinOIDCName {
		return nil
	}
	return c.Delete(ctx, a.Path(k))
}
