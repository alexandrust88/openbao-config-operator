package secrets

import (
	"context"

	ctrl "sigs.k8s.io/controller-runtime"

	secretsv1alpha1 "github.com/alexandrust88/openbao-config-operator/api/secrets/v1alpha1"
	"github.com/alexandrust88/openbao-config-operator/internal/engine"
	"github.com/alexandrust88/openbao-config-operator/internal/openbao"
)

// +kubebuilder:rbac:groups=secrets.openbaoconfig.io,resources=consulsecretsroles,verbs=get;list;watch;update;patch
// +kubebuilder:rbac:groups=secrets.openbaoconfig.io,resources=consulsecretsroles/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=secrets.openbaoconfig.io,resources=consulsecretsroles/finalizers,verbs=update

// SetupConsulSecretsRole registers the ConsulSecretsRole controller with the manager.
func SetupConsulSecretsRole(mgr ctrl.Manager, conns engine.Connections, opts engine.Options) error {
	return setupPlugin(mgr, conns, opts, "consulsecretsrole", ConsulSecretsRoleAdapter{},
		func() *secretsv1alpha1.ConsulSecretsRole { return &secretsv1alpha1.ConsulSecretsRole{} })
}

// ConsulSecretsRoleAdapter maps a ConsulSecretsRole onto <mountPath>/roles/<name>.
type ConsulSecretsRoleAdapter struct{}

var (
	_ engine.Adapter[*secretsv1alpha1.ConsulSecretsRole] = ConsulSecretsRoleAdapter{}
	_ engine.Writer[*secretsv1alpha1.ConsulSecretsRole]  = ConsulSecretsRoleAdapter{}
)

// Path implements engine.Adapter.
func (ConsulSecretsRoleAdapter) Path(r *secretsv1alpha1.ConsulSecretsRole) string {
	return consulSecretsMount(r.Spec.MountPath) + "/roles/" + specName(r.Spec.Name, r.Name)
}

// Desired implements engine.Adapter.
func (ConsulSecretsRoleAdapter) Desired(_ context.Context, _ engine.Resolver, r *secretsv1alpha1.ConsulSecretsRole) (engine.Payload, error) {
	s := r.Spec
	p := engine.Payload{}
	putPluginList(p, "consul_policies", s.ConsulPolicies)
	putPluginList(p, "consul_roles", s.ConsulRoles)
	putPluginList(p, "service_identities", s.ServiceIdentities)
	putPluginList(p, "node_identities", s.NodeIdentities)
	optString(p, "consul_namespace", s.ConsulNamespace)
	optString(p, "partition", s.Partition)
	optBool(p, "local", s.Local)
	optSeconds(p, "ttl", s.TTL)
	optSeconds(p, "max_ttl", s.MaxTTL)
	return p, nil
}

// Fields implements engine.Adapter.
func (ConsulSecretsRoleAdapter) Fields() engine.FieldSpec {
	return engine.FieldSpec{
		Sets:      []string{"consul_policies", "consul_roles", "service_identities", "node_identities"},
		Durations: []string{"ttl", "max_ttl"},
	}
}

// Write implements engine.Writer.
func (a ConsulSecretsRoleAdapter) Write(ctx context.Context, c openbao.Client, r *secretsv1alpha1.ConsulSecretsRole,
	desired engine.Payload, _ bool) (engine.Payload, error) {
	return writeCloud(ctx, c, a.Path(r), desired)
}
