package auth

import (
	"context"

	ctrl "sigs.k8s.io/controller-runtime"

	authv1alpha1 "github.com/alexandrust88/openbao-config-operator/api/auth/v1alpha1"
	"github.com/alexandrust88/openbao-config-operator/internal/engine"
	"github.com/alexandrust88/openbao-config-operator/internal/openbao"
)

// +kubebuilder:rbac:groups=auth.openbaoconfig.io,resources=certauthconfigs,verbs=get;list;watch;update;patch
// +kubebuilder:rbac:groups=auth.openbaoconfig.io,resources=certauthconfigs/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=auth.openbaoconfig.io,resources=certauthconfigs/finalizers,verbs=update

// SetupCertAuthConfig registers the CertAuthConfig controller with the manager.
func SetupCertAuthConfig(mgr ctrl.Manager, conns engine.Connections, opts engine.Options) error {
	r := engine.New[*authv1alpha1.CertAuthConfig](
		mgr.GetClient(),
		mgr.GetScheme(),
		mgr.GetEventRecorder("certauthconfig-controller"),
		conns,
		CertAuthConfigAdapter{},
		func() *authv1alpha1.CertAuthConfig { return &authv1alpha1.CertAuthConfig{} },
		opts,
	)
	return r.SetupWithManager(mgr)
}

// CertAuthConfigAdapter maps a CertAuthConfig onto auth/<mount>/config. The path always reads, so an existing
// config is adopted; it has no delete operation.
type CertAuthConfigAdapter struct{}

var (
	_ engine.Adapter[*authv1alpha1.CertAuthConfig]  = CertAuthConfigAdapter{}
	_ engine.DriftTier                              = CertAuthConfigAdapter{}
	_ engine.Observer[*authv1alpha1.CertAuthConfig] = CertAuthConfigAdapter{}
	_ engine.Singleton                              = CertAuthConfigAdapter{}
)

// Path implements engine.Adapter.
func (CertAuthConfigAdapter) Path(c *authv1alpha1.CertAuthConfig) string {
	return "auth/" + authMount(c.Spec.MountPath, "cert") + "/config"
}

// certOCSPCacheSizeDefault is the cache size OpenBao uses when none is stored; it rejects a written 0.
const certOCSPCacheSizeDefault = int64(100)

// Desired implements engine.Adapter. Unset fields are sent as the OpenBao default, so removing one resets it.
func (CertAuthConfigAdapter) Desired(_ context.Context, _ engine.Resolver, c *authv1alpha1.CertAuthConfig) (engine.Payload, error) {
	p := engine.Payload{
		"disable_binding":                builtinBool(c.Spec.DisableBinding),
		"enable_identity_alias_metadata": builtinBool(c.Spec.EnableIdentityAliasMetadata),
		"ocsp_cache_size":                certOCSPCacheSizeDefault,
	}
	credPutInt32(p, "ocsp_cache_size", c.Spec.OCSPCacheSize)
	return p, nil
}

// Fields implements engine.Adapter.
func (CertAuthConfigAdapter) Fields() engine.FieldSpec { return engine.FieldSpec{} }

// Singleton implements engine.Singleton: the setting always exists, so it is adopted whatever the creationPolicy.
func (CertAuthConfigAdapter) Singleton() bool { return true }

// DriftTier implements engine.DriftTier.
func (CertAuthConfigAdapter) DriftTier() engine.Tier { return engine.TierAccessCritical }

// Observe implements engine.Observer. A stored cache size of 0 is never written by the operator; OpenBao reads it
// for a config that was never written and then uses the default.
func (a CertAuthConfigAdapter) Observe(ctx context.Context, c openbao.Client, cfg *authv1alpha1.CertAuthConfig) (engine.Payload, bool, error) {
	observed, found, err := readWithDefaults(ctx, c, a.Path(cfg), nil)
	if n, ok := credInt64(observed["ocsp_cache_size"]); found && ok && n == 0 {
		observed["ocsp_cache_size"] = certOCSPCacheSizeDefault
	}
	return observed, found, err
}
