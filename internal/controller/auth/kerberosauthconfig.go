package auth

import (
	"context"
	"encoding/base64"

	ctrl "sigs.k8s.io/controller-runtime"

	authv1alpha1 "github.com/alexandrust88/openbao-config-operator/api/auth/v1alpha1"
	"github.com/alexandrust88/openbao-config-operator/internal/engine"
	"github.com/alexandrust88/openbao-config-operator/internal/openbao"
)

// +kubebuilder:rbac:groups=auth.obco.dev,resources=kerberosauthconfigs,verbs=get;list;watch;update;patch
// +kubebuilder:rbac:groups=auth.obco.dev,resources=kerberosauthconfigs/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=auth.obco.dev,resources=kerberosauthconfigs/finalizers,verbs=update
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch

// SetupKerberosAuthConfig registers the KerberosAuthConfig controller with the manager.
func SetupKerberosAuthConfig(mgr ctrl.Manager, conns engine.Connections, opts engine.Options) error {
	return setupPluginKind(mgr, conns, opts, "kerberosauthconfig-controller", KerberosAuthConfigAdapter{},
		func() *authv1alpha1.KerberosAuthConfig { return &authv1alpha1.KerberosAuthConfig{} })
}

// KerberosAuthConfigAdapter maps a KerberosAuthConfig onto auth/<mountPath>/config of a Kerberos auth method.
type KerberosAuthConfigAdapter struct{}

var (
	_ engine.Adapter[*authv1alpha1.KerberosAuthConfig]          = KerberosAuthConfigAdapter{}
	_ engine.DriftTier                                          = KerberosAuthConfigAdapter{}
	_ engine.SecretReferencer[*authv1alpha1.KerberosAuthConfig] = KerberosAuthConfigAdapter{}
)

// Path implements engine.Adapter.
func (KerberosAuthConfigAdapter) Path(o *authv1alpha1.KerberosAuthConfig) string {
	return "auth/" + authMount(o.Spec.MountPath, "kerberos") + "/config"
}

// Desired implements engine.Adapter.
func (KerberosAuthConfigAdapter) Desired(ctx context.Context, r engine.Resolver, o *authv1alpha1.KerberosAuthConfig) (engine.Payload, error) {
	keytab, err := r.SecretValue(ctx, o.Namespace, o.Spec.KeytabSecretRef)
	if err != nil {
		return nil, err
	}
	p := engine.Payload{
		"service_account": o.Spec.ServiceAccount,
		"keytab":          base64.StdEncoding.EncodeToString(keytab),
	}
	pluginPut(p, "remove_instance_name", o.Spec.RemoveInstanceName)
	pluginPut(p, "add_group_aliases", o.Spec.AddGroupAliases)
	return p, nil
}

// Fields implements engine.Adapter.
func (KerberosAuthConfigAdapter) Fields() engine.FieldSpec {
	return engine.FieldSpec{WriteOnly: []string{"keytab"}}
}

// DriftTier implements engine.DriftTier.
func (KerberosAuthConfigAdapter) DriftTier() engine.Tier { return engine.TierAccessCritical }

// SecretRefs implements engine.SecretReferencer.
func (KerberosAuthConfigAdapter) SecretRefs(o *authv1alpha1.KerberosAuthConfig) []string {
	return []string{o.Spec.KeytabSecretRef.Name}
}

// Observe implements engine.Observer. When the object is absent it checks that the auth method is enabled.
func (a KerberosAuthConfigAdapter) Observe(ctx context.Context, c openbao.Client, o *authv1alpha1.KerberosAuthConfig) (engine.Payload, bool, error) {
	return observeExternalAuth(ctx, c, a.Path(o), authMount(o.Spec.MountPath, "kerberos"), "kerberos")
}
