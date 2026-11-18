package secrets

import (
	"context"

	ctrl "sigs.k8s.io/controller-runtime"

	secretsv1alpha1 "github.com/alexandrust88/openbao-config-operator/api/secrets/v1alpha1"
	"github.com/alexandrust88/openbao-config-operator/internal/engine"
)

// +kubebuilder:rbac:groups=secrets.obco.dev,resources=nomadsecretsroles,verbs=get;list;watch;update;patch
// +kubebuilder:rbac:groups=secrets.obco.dev,resources=nomadsecretsroles/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=secrets.obco.dev,resources=nomadsecretsroles/finalizers,verbs=update

// SetupNomadSecretsRole registers the NomadSecretsRole controller with the manager.
func SetupNomadSecretsRole(mgr ctrl.Manager, conns engine.Connections, opts engine.Options) error {
	return setupPlugin(mgr, conns, opts, "nomadsecretsrole", NomadSecretsRoleAdapter{},
		func() *secretsv1alpha1.NomadSecretsRole { return &secretsv1alpha1.NomadSecretsRole{} })
}

// NomadSecretsRoleAdapter maps a NomadSecretsRole onto <mountPath>/role/<name>.
type NomadSecretsRoleAdapter struct{}

var _ engine.Adapter[*secretsv1alpha1.NomadSecretsRole] = NomadSecretsRoleAdapter{}

// Path implements engine.Adapter.
func (NomadSecretsRoleAdapter) Path(r *secretsv1alpha1.NomadSecretsRole) string {
	return pluginMountPath(r.Spec.MountPath, "nomad") + "/role/" + specName(r.Spec.Name, r.Name)
}

// Desired implements engine.Adapter. A management role sends an empty policy list, because the plugin keeps stored
// policies when the key is absent and then refuses the switch from a client role.
func (NomadSecretsRoleAdapter) Desired(_ context.Context, _ engine.Resolver, r *secretsv1alpha1.NomadSecretsRole) (engine.Payload, error) {
	p := engine.Payload{}
	optString(p, "type", string(r.Spec.Type))
	optBool(p, "global", r.Spec.Global)
	putPluginList(p, "policies", r.Spec.Policies)
	if r.Spec.Type == secretsv1alpha1.NomadTokenTypeManagement {
		p["policies"] = []string{}
	}
	return p, nil
}

// Fields implements engine.Adapter.
func (NomadSecretsRoleAdapter) Fields() engine.FieldSpec {
	return engine.FieldSpec{Sets: []string{"policies"}}
}
