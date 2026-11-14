package secrets

import (
	"context"
	"errors"
	"fmt"
	"maps"

	ctrl "sigs.k8s.io/controller-runtime"

	secretsv1alpha1 "github.com/alexandrust88/openbao-config-operator/api/secrets/v1alpha1"
	"github.com/alexandrust88/openbao-config-operator/internal/engine"
	"github.com/alexandrust88/openbao-config-operator/internal/openbao"
)

// +kubebuilder:rbac:groups=secrets.openbaoconfig.io,resources=databaseroles,verbs=get;list;watch;update;patch
// +kubebuilder:rbac:groups=secrets.openbaoconfig.io,resources=databaseroles/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=secrets.openbaoconfig.io,resources=databaseroles/finalizers,verbs=update
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch

// SetupDatabaseRole registers the DatabaseRole controller with the manager.
func SetupDatabaseRole(mgr ctrl.Manager, conns engine.Connections, opts engine.Options) error {
	r := engine.New[*secretsv1alpha1.DatabaseRole](
		mgr.GetClient(),
		mgr.GetScheme(),
		mgr.GetEventRecorder("databaserole-controller"),
		conns,
		DatabaseRoleAdapter{},
		func() *secretsv1alpha1.DatabaseRole { return &secretsv1alpha1.DatabaseRole{} },
		opts,
	)
	return r.SetupWithManager(mgr)
}

// DatabaseRoleAdapter maps a DatabaseRole onto <mount>/roles/<name>.
type DatabaseRoleAdapter struct{}

var (
	_ engine.Adapter[*secretsv1alpha1.DatabaseRole]          = DatabaseRoleAdapter{}
	_ engine.Observer[*secretsv1alpha1.DatabaseRole]         = DatabaseRoleAdapter{}
	_ engine.Writer[*secretsv1alpha1.DatabaseRole]           = DatabaseRoleAdapter{}
	_ engine.SecretReferencer[*secretsv1alpha1.DatabaseRole] = DatabaseRoleAdapter{}
)

// databaseCAPrivateKey is written inside credential_config but kept top-level in the desired payload, so the
// engine hashes it as write-only instead of comparing it.
const databaseCAPrivateKey = "ca_private_key"

// databaseCredentialConfig is read back with the generator defaults OpenBao fills in, so it is compared on the
// keys the spec sets.
const databaseCredentialConfig = "credential_config"

// Path implements engine.Adapter.
func (DatabaseRoleAdapter) Path(r *secretsv1alpha1.DatabaseRole) string {
	return r.Spec.MountPath + "/roles/" + specName(r.Spec.Name, r.Name)
}

// Desired implements engine.Adapter.
func (DatabaseRoleAdapter) Desired(ctx context.Context, res engine.Resolver, r *secretsv1alpha1.DatabaseRole) (engine.Payload, error) {
	p := engine.Payload{"db_name": r.Spec.DBName}
	if err := putCredentialFields(ctx, res, r.Namespace, p, &r.Spec.DatabaseCredentialFields); err != nil {
		return nil, err
	}
	orSeconds(p, "default_ttl", r.Spec.DefaultTTL, 0)
	orSeconds(p, "max_ttl", r.Spec.MaxTTL, 0)
	orList(p, "creation_statements", r.Spec.CreationStatements)
	orList(p, "revocation_statements", r.Spec.RevocationStatements)
	orList(p, "rollback_statements", r.Spec.RollbackStatements)
	orList(p, "renew_statements", r.Spec.RenewStatements)
	return p, nil
}

// Fields implements engine.Adapter.
func (DatabaseRoleAdapter) Fields() engine.FieldSpec {
	return engine.FieldSpec{
		WriteOnly: []string{databaseCAPrivateKey},
		Partial:   []string{databaseCredentialConfig},
		Durations: []string{"default_ttl", "max_ttl"},
	}
}

// Observe implements engine.Observer.
func (a DatabaseRoleAdapter) Observe(ctx context.Context, c openbao.Client, r *secretsv1alpha1.DatabaseRole) (engine.Payload, bool, error) {
	return readDatabaseRole(ctx, c, a.Path(r))
}

// Write implements engine.Writer.
func (a DatabaseRoleAdapter) Write(ctx context.Context, c openbao.Client, r *secretsv1alpha1.DatabaseRole, desired engine.Payload,
	_ bool) (engine.Payload, error) {
	return writeDatabaseRole(ctx, c, a.Path(r), desired)
}

// SecretRefs implements engine.SecretReferencer.
func (DatabaseRoleAdapter) SecretRefs(r *secretsv1alpha1.DatabaseRole) []string {
	return credentialSecretNames(&r.Spec.DatabaseCredentialFields)
}

func putCredentialFields(ctx context.Context, r engine.Resolver, namespace string, p engine.Payload,
	f *secretsv1alpha1.DatabaseCredentialFields) error {
	orString(p, "credential_type", string(f.CredentialType), string(secretsv1alpha1.DatabaseCredentialPassword))
	if len(f.CredentialConfig) > 0 {
		p[databaseCredentialConfig] = f.CredentialConfig
	}
	if f.CAPrivateKeySecretRef == nil {
		return nil
	}
	key, err := r.SecretValue(ctx, namespace, *f.CAPrivateKeySecretRef)
	if err != nil {
		return err
	}
	p[databaseCAPrivateKey] = string(key)
	return nil
}

func credentialSecretNames(f *secretsv1alpha1.DatabaseCredentialFields) []string {
	if f.CAPrivateKeySecretRef == nil {
		return nil
	}
	return []string{f.CAPrivateKeySecretRef.Name}
}

// writeDatabaseRole writes a dynamic or static role with the CA private key moved into credential_config.
func writeDatabaseRole(ctx context.Context, c openbao.Client, path string, desired engine.Payload) (engine.Payload, error) {
	body := desired
	if key, ok := desired[databaseCAPrivateKey]; ok {
		body = maps.Clone(desired)
		delete(body, databaseCAPrivateKey)
		config := map[string]any{databaseCAPrivateKey: key}
		if existing, ok := desired[databaseCredentialConfig].(map[string]string); ok {
			for k, v := range existing {
				config[k] = v
			}
		}
		body[databaseCredentialConfig] = config
	}
	if _, err := c.Write(ctx, path, body); err != nil {
		return nil, fmt.Errorf("write %s: %w", path, err)
	}
	return nil, nil
}

// readDatabaseRole reads a dynamic or static role. credential_config is written as strings but read with
// typed values (key_bits: 2048), so its values are compared as strings.
func readDatabaseRole(ctx context.Context, c openbao.Client, path string) (engine.Payload, bool, error) {
	observed, err := c.Read(ctx, path)
	if errors.Is(err, openbao.ErrNotFound) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("read %s: %w", path, err)
	}
	if config, ok := observed[databaseCredentialConfig].(map[string]any); ok {
		asStrings := make(map[string]any, len(config))
		for key, value := range config {
			if key != databaseCAPrivateKey {
				asStrings[key] = fmt.Sprint(value)
			}
		}
		observed[databaseCredentialConfig] = asStrings
	}
	return observed, true, nil
}
