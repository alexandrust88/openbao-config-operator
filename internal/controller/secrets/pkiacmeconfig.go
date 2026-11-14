package secrets

import (
	"context"

	ctrl "sigs.k8s.io/controller-runtime"

	secretsv1alpha1 "github.com/alexandrust88/openbao-config-operator/api/secrets/v1alpha1"
	"github.com/alexandrust88/openbao-config-operator/internal/engine"
)

// +kubebuilder:rbac:groups=secrets.openbaoconfig.io,resources=pkiacmeconfigs,verbs=get;list;watch;update;patch
// +kubebuilder:rbac:groups=secrets.openbaoconfig.io,resources=pkiacmeconfigs/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=secrets.openbaoconfig.io,resources=pkiacmeconfigs/finalizers,verbs=update

// SetupPKIACMEConfig registers the PKIACMEConfig controller with the manager.
func SetupPKIACMEConfig(mgr ctrl.Manager, conns engine.Connections, opts engine.Options) error {
	r := engine.New[*secretsv1alpha1.PKIACMEConfig](
		mgr.GetClient(),
		mgr.GetScheme(),
		mgr.GetEventRecorder("pkiacmeconfig-controller"),
		conns,
		PKIACMEConfigAdapter{},
		func() *secretsv1alpha1.PKIACMEConfig { return &secretsv1alpha1.PKIACMEConfig{} },
		opts,
	)
	return r.SetupWithManager(mgr)
}

// PKIACMEConfigAdapter maps a PKIACMEConfig onto <mount>/config/acme.
type PKIACMEConfigAdapter struct{}

var (
	_ engine.Adapter[*secretsv1alpha1.PKIACMEConfig] = PKIACMEConfigAdapter{}
	_ engine.Singleton                               = PKIACMEConfigAdapter{}
)

// Path implements engine.Adapter.
func (PKIACMEConfigAdapter) Path(c *secretsv1alpha1.PKIACMEConfig) string {
	return pkiMount(c.Spec.MountPath) + "/config/acme"
}

// Desired implements engine.Adapter. Every setting is sent, an unset one at its OpenBao default.
func (PKIACMEConfigAdapter) Desired(_ context.Context, _ engine.Resolver, c *secretsv1alpha1.PKIACMEConfig) (engine.Payload, error) {
	s := &c.Spec
	p := engine.Payload{}
	orFalse(p, "enabled", s.Enabled)
	orListDefault(p, "allowed_issuers", s.AllowedIssuers, []string{"*"})
	orListDefault(p, "allowed_roles", s.AllowedRoles, []string{"*"})
	orFalse(p, "allow_role_ext_key_usage", s.AllowRoleExtKeyUsage)
	orString(p, "default_directory_policy", s.DefaultDirectoryPolicy, "sign-verbatim")
	p["dns_resolver"] = ""
	if s.DNSResolver != nil {
		p["dns_resolver"] = *s.DNSResolver
	}
	orString(p, "eab_policy", s.EABPolicy, "not-required")
	return p, nil
}

// Fields implements engine.Adapter.
func (PKIACMEConfigAdapter) Fields() engine.FieldSpec {
	return engine.FieldSpec{Sets: []string{"allowed_issuers", "allowed_roles"}}
}

// Singleton implements engine.Singleton: the setting always exists, so it is adopted whatever the creationPolicy.
func (PKIACMEConfigAdapter) Singleton() bool { return true }
