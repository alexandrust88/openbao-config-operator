package secrets

import (
	"context"

	ctrl "sigs.k8s.io/controller-runtime"

	secretsv1alpha1 "github.com/alexandrust88/openbao-config-operator/api/secrets/v1alpha1"
	"github.com/alexandrust88/openbao-config-operator/internal/engine"
	"github.com/alexandrust88/openbao-config-operator/internal/openbao"
)

// +kubebuilder:rbac:groups=secrets.obco.dev,resources=databasestaticroles,verbs=get;list;watch;update;patch
// +kubebuilder:rbac:groups=secrets.obco.dev,resources=databasestaticroles/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=secrets.obco.dev,resources=databasestaticroles/finalizers,verbs=update
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch

// SetupDatabaseStaticRole registers the DatabaseStaticRole controller with the manager.
func SetupDatabaseStaticRole(mgr ctrl.Manager, conns engine.Connections, opts engine.Options) error {
	r := engine.New[*secretsv1alpha1.DatabaseStaticRole](
		mgr.GetClient(),
		mgr.GetScheme(),
		mgr.GetEventRecorder("databasestaticrole-controller"),
		conns,
		DatabaseStaticRoleAdapter{},
		func() *secretsv1alpha1.DatabaseStaticRole { return &secretsv1alpha1.DatabaseStaticRole{} },
		opts,
	)
	return r.SetupWithManager(mgr)
}

// DatabaseStaticRoleAdapter maps a DatabaseStaticRole onto <mount>/static-roles/<name>.
type DatabaseStaticRoleAdapter struct{}

var (
	_ engine.Adapter[*secretsv1alpha1.DatabaseStaticRole]          = DatabaseStaticRoleAdapter{}
	_ engine.Observer[*secretsv1alpha1.DatabaseStaticRole]         = DatabaseStaticRoleAdapter{}
	_ engine.Writer[*secretsv1alpha1.DatabaseStaticRole]           = DatabaseStaticRoleAdapter{}
	_ engine.SecretReferencer[*secretsv1alpha1.DatabaseStaticRole] = DatabaseStaticRoleAdapter{}
)

// Path implements engine.Adapter.
func (DatabaseStaticRoleAdapter) Path(r *secretsv1alpha1.DatabaseStaticRole) string {
	return r.Spec.MountPath + "/static-roles/" + specName(r.Spec.Name, r.Name)
}

// Desired implements engine.Adapter.
func (DatabaseStaticRoleAdapter) Desired(ctx context.Context, res engine.Resolver, r *secretsv1alpha1.DatabaseStaticRole) (engine.Payload, error) {
	p := engine.Payload{
		"db_name":         r.Spec.DBName,
		"username":        r.Spec.Username,
		"rotation_period": int64(r.Spec.RotationPeriod.Seconds()),
	}
	if err := putCredentialFields(ctx, res, r.Namespace, p, &r.Spec.DatabaseCredentialFields); err != nil {
		return nil, err
	}
	orList(p, "rotation_statements", r.Spec.RotationStatements)
	return p, nil
}

// Fields implements engine.Adapter.
func (DatabaseStaticRoleAdapter) Fields() engine.FieldSpec {
	return engine.FieldSpec{
		WriteOnly: []string{databaseCAPrivateKey},
		Partial:   []string{databaseCredentialConfig},
		Durations: []string{"rotation_period"},
	}
}

// Observe implements engine.Observer.
func (a DatabaseStaticRoleAdapter) Observe(ctx context.Context, c openbao.Client, r *secretsv1alpha1.DatabaseStaticRole) (engine.Payload, bool, error) {
	return readDatabaseRole(ctx, c, a.Path(r))
}

// Write implements engine.Writer.
func (a DatabaseStaticRoleAdapter) Write(ctx context.Context, c openbao.Client, r *secretsv1alpha1.DatabaseStaticRole,
	desired engine.Payload, _ bool) (engine.Payload, error) {
	return writeDatabaseRole(ctx, c, a.Path(r), desired)
}

// SecretRefs implements engine.SecretReferencer.
func (DatabaseStaticRoleAdapter) SecretRefs(r *secretsv1alpha1.DatabaseStaticRole) []string {
	return credentialSecretNames(&r.Spec.DatabaseCredentialFields)
}
