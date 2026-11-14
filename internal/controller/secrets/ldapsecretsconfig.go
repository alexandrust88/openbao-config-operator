package secrets

import (
	"context"

	ctrl "sigs.k8s.io/controller-runtime"

	secretsv1alpha1 "github.com/alexandrust88/openbao-config-operator/api/secrets/v1alpha1"
	"github.com/alexandrust88/openbao-config-operator/internal/engine"
)

// +kubebuilder:rbac:groups=secrets.openbaoconfig.io,resources=ldapsecretsconfigs,verbs=get;list;watch;update;patch
// +kubebuilder:rbac:groups=secrets.openbaoconfig.io,resources=ldapsecretsconfigs/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=secrets.openbaoconfig.io,resources=ldapsecretsconfigs/finalizers,verbs=update
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch

// SetupLDAPSecretsConfig registers the LDAPSecretsConfig controller with the manager.
func SetupLDAPSecretsConfig(mgr ctrl.Manager, conns engine.Connections, opts engine.Options) error {
	return setupPlugin(mgr, conns, opts, "ldapsecretsconfig", LDAPSecretsConfigAdapter{},
		func() *secretsv1alpha1.LDAPSecretsConfig { return &secretsv1alpha1.LDAPSecretsConfig{} })
}

// LDAPSecretsConfigAdapter maps an LDAPSecretsConfig onto <mountPath>/config. The read omits client_tls_cert, so it
// is write-only like the key it pairs with.
type LDAPSecretsConfigAdapter struct{}

var (
	_ engine.Adapter[*secretsv1alpha1.LDAPSecretsConfig]          = LDAPSecretsConfigAdapter{}
	_ engine.SecretReferencer[*secretsv1alpha1.LDAPSecretsConfig] = LDAPSecretsConfigAdapter{}
)

// Path implements engine.Adapter.
func (LDAPSecretsConfigAdapter) Path(c *secretsv1alpha1.LDAPSecretsConfig) string {
	return pluginMountPath(c.Spec.MountPath, "ldap") + "/config"
}

// Desired implements engine.Adapter.
func (LDAPSecretsConfigAdapter) Desired(ctx context.Context, r engine.Resolver, c *secretsv1alpha1.LDAPSecretsConfig) (engine.Payload, error) {
	s := c.Spec
	p := engine.Payload{"binddn": s.BindDN}
	optString(p, "url", s.URL)
	optString(p, "userdn", s.UserDN)
	optString(p, "userattr", s.UserAttr)
	optString(p, "upndomain", s.UPNDomain)
	optString(p, "certificate", s.Certificate)
	optString(p, "client_tls_cert", s.ClientTLSCert)
	optBool(p, "insecure_tls", s.InsecureTLS)
	optBool(p, "starttls", s.StartTLS)
	optString(p, "tls_min_version", string(s.TLSMinVersion))
	optString(p, "tls_max_version", string(s.TLSMaxVersion))
	optSeconds(p, "connection_timeout", s.ConnectionTimeout)
	optSeconds(p, "request_timeout", s.RequestTimeout)
	optString(p, "password_policy", s.PasswordPolicy)
	optString(p, "schema", string(s.Schema))
	optBool(p, "skip_static_role_import_rotation", s.SkipStaticRoleImportRotation)
	if err := putPluginSecret(ctx, r, c.Namespace, p, "bindpass", &s.BindPassSecretRef); err != nil {
		return nil, err
	}
	if err := putPluginSecret(ctx, r, c.Namespace, p, "client_tls_key", s.ClientTLSKeySecretRef); err != nil {
		return nil, err
	}
	return p, nil
}

// Fields implements engine.Adapter.
func (LDAPSecretsConfigAdapter) Fields() engine.FieldSpec {
	return engine.FieldSpec{
		WriteOnly: []string{"bindpass", "client_tls_cert", "client_tls_key"},
		Durations: []string{"connection_timeout", "request_timeout"},
	}
}

// SecretRefs implements engine.SecretReferencer.
func (LDAPSecretsConfigAdapter) SecretRefs(c *secretsv1alpha1.LDAPSecretsConfig) []string {
	return pluginSecretNames(&c.Spec.BindPassSecretRef, c.Spec.ClientTLSKeySecretRef)
}
