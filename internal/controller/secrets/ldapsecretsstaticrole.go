package secrets

import (
	"context"

	ctrl "sigs.k8s.io/controller-runtime"

	secretsv1alpha1 "github.com/alexandrust88/openbao-config-operator/api/secrets/v1alpha1"
	"github.com/alexandrust88/openbao-config-operator/internal/engine"
)

// +kubebuilder:rbac:groups=secrets.openbaoconfig.io,resources=ldapsecretsstaticroles,verbs=get;list;watch;update;patch
// +kubebuilder:rbac:groups=secrets.openbaoconfig.io,resources=ldapsecretsstaticroles/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=secrets.openbaoconfig.io,resources=ldapsecretsstaticroles/finalizers,verbs=update

// SetupLDAPSecretsStaticRole registers the LDAPSecretsStaticRole controller with the manager.
func SetupLDAPSecretsStaticRole(mgr ctrl.Manager, conns engine.Connections, opts engine.Options) error {
	return setupPlugin(mgr, conns, opts, "ldapsecretsstaticrole", LDAPSecretsStaticRoleAdapter{},
		func() *secretsv1alpha1.LDAPSecretsStaticRole { return &secretsv1alpha1.LDAPSecretsStaticRole{} })
}

// LDAPSecretsStaticRoleAdapter maps an LDAPSecretsStaticRole onto <mountPath>/static-role/<name>. The plugin refuses
// skip_import_rotation on an update and never returns it, so it is create-only.
type LDAPSecretsStaticRoleAdapter struct{}

var _ engine.Adapter[*secretsv1alpha1.LDAPSecretsStaticRole] = LDAPSecretsStaticRoleAdapter{}

// Path implements engine.Adapter.
func (LDAPSecretsStaticRoleAdapter) Path(r *secretsv1alpha1.LDAPSecretsStaticRole) string {
	return pluginMountPath(r.Spec.MountPath, "ldap") + "/static-role/" + specName(r.Spec.Name, r.Name)
}

// Desired implements engine.Adapter.
func (LDAPSecretsStaticRoleAdapter) Desired(_ context.Context, _ engine.Resolver, r *secretsv1alpha1.LDAPSecretsStaticRole) (engine.Payload, error) {
	p := engine.Payload{
		"username":        r.Spec.Username,
		"rotation_period": int64(r.Spec.RotationPeriod.Seconds()),
	}
	optString(p, "dn", r.Spec.DN)
	optBool(p, "skip_import_rotation", r.Spec.SkipImportRotation)
	return p, nil
}

// Fields implements engine.Adapter.
func (LDAPSecretsStaticRoleAdapter) Fields() engine.FieldSpec {
	return engine.FieldSpec{
		CreateOnly: []string{"skip_import_rotation"},
		Durations:  []string{"rotation_period"},
	}
}
