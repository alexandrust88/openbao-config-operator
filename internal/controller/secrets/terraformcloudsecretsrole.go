package secrets

import (
	"context"
	"errors"

	ctrl "sigs.k8s.io/controller-runtime"

	secretsv1alpha1 "github.com/alexandrust88/openbao-config-operator/api/secrets/v1alpha1"
	"github.com/alexandrust88/openbao-config-operator/internal/engine"
	"github.com/alexandrust88/openbao-config-operator/internal/openbao"
)

// +kubebuilder:rbac:groups=secrets.openbaoconfig.io,resources=terraformcloudsecretsroles,verbs=get;list;watch;update;patch
// +kubebuilder:rbac:groups=secrets.openbaoconfig.io,resources=terraformcloudsecretsroles/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=secrets.openbaoconfig.io,resources=terraformcloudsecretsroles/finalizers,verbs=update

// SetupTerraformCloudSecretsRole registers the TerraformCloudSecretsRole controller with the manager.
func SetupTerraformCloudSecretsRole(mgr ctrl.Manager, conns engine.Connections, opts engine.Options) error {
	return setupPlugin(mgr, conns, opts, "terraformcloudsecretsrole", TerraformCloudSecretsRoleAdapter{},
		func() *secretsv1alpha1.TerraformCloudSecretsRole { return &secretsv1alpha1.TerraformCloudSecretsRole{} })
}

// TerraformCloudSecretsRoleAdapter maps a TerraformCloudSecretsRole onto <mountPath>/role/<name>.
type TerraformCloudSecretsRoleAdapter struct{}

var (
	_ engine.Adapter[*secretsv1alpha1.TerraformCloudSecretsRole]  = TerraformCloudSecretsRoleAdapter{}
	_ engine.Observer[*secretsv1alpha1.TerraformCloudSecretsRole] = TerraformCloudSecretsRoleAdapter{}
)

// Path implements engine.Adapter.
func (TerraformCloudSecretsRoleAdapter) Path(r *secretsv1alpha1.TerraformCloudSecretsRole) string {
	return pluginMountPath(r.Spec.MountPath, "terraform") + "/role/" + specName(r.Spec.Name, r.Name)
}

// Desired implements engine.Adapter.
func (TerraformCloudSecretsRoleAdapter) Desired(_ context.Context, _ engine.Resolver, r *secretsv1alpha1.TerraformCloudSecretsRole) (engine.Payload, error) {
	s := r.Spec
	p := engine.Payload{}
	optString(p, "organization", s.Organization)
	optString(p, "team_id", s.TeamID)
	optString(p, "user_id", s.UserID)
	optString(p, "credential_type", string(s.CredentialType))
	optString(p, "description", s.Description)
	optSeconds(p, "ttl", s.TTL)
	optSeconds(p, "max_ttl", s.MaxTTL)
	return p, nil
}

// Fields implements engine.Adapter.
func (TerraformCloudSecretsRoleAdapter) Fields() engine.FieldSpec {
	return engine.FieldSpec{Durations: []string{"ttl", "max_ttl"}}
}

// Observe implements engine.Observer. The plugin omits credential_type from the read of an organization role, so
// a role that has only an organization is reported with the organization credential type.
func (a TerraformCloudSecretsRoleAdapter) Observe(ctx context.Context, c openbao.Client, r *secretsv1alpha1.TerraformCloudSecretsRole) (engine.Payload, bool, error) {
	observed, err := c.Read(ctx, a.Path(r))
	if errors.Is(err, openbao.ErrNotFound) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	if observed == nil {
		observed = engine.Payload{}
	}
	if _, ok := observed["credential_type"]; !ok && observed["organization"] != nil {
		observed["credential_type"] = string(secretsv1alpha1.TerraformCloudCredentialTypeOrganization)
	}
	return observed, true, nil
}
