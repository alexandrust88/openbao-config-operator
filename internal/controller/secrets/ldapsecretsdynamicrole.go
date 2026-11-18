package secrets

import (
	"context"

	ctrl "sigs.k8s.io/controller-runtime"

	secretsv1alpha1 "github.com/alexandrust88/openbao-config-operator/api/secrets/v1alpha1"
	"github.com/alexandrust88/openbao-config-operator/internal/engine"
)

// +kubebuilder:rbac:groups=secrets.obco.dev,resources=ldapsecretsdynamicroles,verbs=get;list;watch;update;patch
// +kubebuilder:rbac:groups=secrets.obco.dev,resources=ldapsecretsdynamicroles/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=secrets.obco.dev,resources=ldapsecretsdynamicroles/finalizers,verbs=update

// SetupLDAPSecretsDynamicRole registers the LDAPSecretsDynamicRole controller with the manager.
func SetupLDAPSecretsDynamicRole(mgr ctrl.Manager, conns engine.Connections, opts engine.Options) error {
	return setupPlugin(mgr, conns, opts, "ldapsecretsdynamicrole", LDAPSecretsDynamicRoleAdapter{},
		func() *secretsv1alpha1.LDAPSecretsDynamicRole { return &secretsv1alpha1.LDAPSecretsDynamicRole{} })
}

// LDAPSecretsDynamicRoleAdapter maps an LDAPSecretsDynamicRole onto <mountPath>/role/<name>.
type LDAPSecretsDynamicRoleAdapter struct{}

var _ engine.Adapter[*secretsv1alpha1.LDAPSecretsDynamicRole] = LDAPSecretsDynamicRoleAdapter{}

// Path implements engine.Adapter.
func (LDAPSecretsDynamicRoleAdapter) Path(r *secretsv1alpha1.LDAPSecretsDynamicRole) string {
	return pluginMountPath(r.Spec.MountPath, "ldap") + "/role/" + specName(r.Spec.Name, r.Name)
}

// Desired implements engine.Adapter. The LDIF templates are sent verbatim; OpenBao renders them per request.
func (LDAPSecretsDynamicRoleAdapter) Desired(_ context.Context, _ engine.Resolver, r *secretsv1alpha1.LDAPSecretsDynamicRole) (engine.Payload, error) {
	p := engine.Payload{
		"creation_ldif": r.Spec.CreationLDIF,
		"deletion_ldif": r.Spec.DeletionLDIF,
	}
	optString(p, "rollback_ldif", r.Spec.RollbackLDIF)
	optString(p, "username_template", r.Spec.UsernameTemplate)
	optSeconds(p, "default_ttl", r.Spec.DefaultTTL)
	optSeconds(p, "max_ttl", r.Spec.MaxTTL)
	return p, nil
}

// Fields implements engine.Adapter.
func (LDAPSecretsDynamicRoleAdapter) Fields() engine.FieldSpec {
	return engine.FieldSpec{Durations: []string{"default_ttl", "max_ttl"}}
}
