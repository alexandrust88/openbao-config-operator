package secrets

import (
	"context"

	ctrl "sigs.k8s.io/controller-runtime"

	secretsv1alpha1 "github.com/alexandrust88/openbao-config-operator/api/secrets/v1alpha1"
	"github.com/alexandrust88/openbao-config-operator/internal/engine"
)

// +kubebuilder:rbac:groups=secrets.openbaoconfig.io,resources=mongodbatlassecretsroles,verbs=get;list;watch;update;patch
// +kubebuilder:rbac:groups=secrets.openbaoconfig.io,resources=mongodbatlassecretsroles/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=secrets.openbaoconfig.io,resources=mongodbatlassecretsroles/finalizers,verbs=update

// SetupMongoDBAtlasSecretsRole registers the MongoDBAtlasSecretsRole controller with the manager.
func SetupMongoDBAtlasSecretsRole(mgr ctrl.Manager, conns engine.Connections, opts engine.Options) error {
	return setupPlugin(mgr, conns, opts, "mongodbatlassecretsrole", MongoDBAtlasSecretsRoleAdapter{},
		func() *secretsv1alpha1.MongoDBAtlasSecretsRole { return &secretsv1alpha1.MongoDBAtlasSecretsRole{} })
}

// MongoDBAtlasSecretsRoleAdapter maps a MongoDBAtlasSecretsRole onto <mountPath>/roles/<name>.
type MongoDBAtlasSecretsRoleAdapter struct{}

var _ engine.Adapter[*secretsv1alpha1.MongoDBAtlasSecretsRole] = MongoDBAtlasSecretsRoleAdapter{}

// Path implements engine.Adapter.
func (MongoDBAtlasSecretsRoleAdapter) Path(r *secretsv1alpha1.MongoDBAtlasSecretsRole) string {
	return pluginMountPath(r.Spec.MountPath, "mongodbatlas") + "/roles/" + specName(r.Spec.Name, r.Name)
}

// Desired implements engine.Adapter.
func (MongoDBAtlasSecretsRoleAdapter) Desired(_ context.Context, _ engine.Resolver, r *secretsv1alpha1.MongoDBAtlasSecretsRole) (engine.Payload, error) {
	s := r.Spec
	p := engine.Payload{}
	optString(p, "organization_id", s.OrganizationID)
	optString(p, "project_id", s.ProjectID)
	putPluginList(p, "roles", s.Roles)
	putPluginList(p, "ip_addresses", s.IPAddresses)
	putPluginList(p, "cidr_blocks", s.CIDRBlocks)
	putPluginList(p, "project_roles", s.ProjectRoles)
	optSeconds(p, "ttl", s.TTL)
	optSeconds(p, "max_ttl", s.MaxTTL)
	return p, nil
}

// Fields implements engine.Adapter.
func (MongoDBAtlasSecretsRoleAdapter) Fields() engine.FieldSpec {
	return engine.FieldSpec{
		Sets:      []string{"roles", "ip_addresses", "cidr_blocks", "project_roles"},
		Durations: []string{"ttl", "max_ttl"},
	}
}
