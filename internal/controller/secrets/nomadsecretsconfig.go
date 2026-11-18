package secrets

import (
	"context"
	"errors"

	ctrl "sigs.k8s.io/controller-runtime"

	secretsv1alpha1 "github.com/alexandrust88/openbao-config-operator/api/secrets/v1alpha1"
	"github.com/alexandrust88/openbao-config-operator/internal/engine"
	"github.com/alexandrust88/openbao-config-operator/internal/openbao"
)

// +kubebuilder:rbac:groups=secrets.obco.dev,resources=nomadsecretsconfigs,verbs=get;list;watch;update;patch
// +kubebuilder:rbac:groups=secrets.obco.dev,resources=nomadsecretsconfigs/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=secrets.obco.dev,resources=nomadsecretsconfigs/finalizers,verbs=update
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch

// SetupNomadSecretsConfig registers the NomadSecretsConfig controller with the manager.
func SetupNomadSecretsConfig(mgr ctrl.Manager, conns engine.Connections, opts engine.Options) error {
	opts = withHashKey(opts)
	return setupPlugin(mgr, conns, opts, "nomadsecretsconfig", NomadSecretsConfigAdapter{hashKey: opts.HashKey},
		func() *secretsv1alpha1.NomadSecretsConfig { return &secretsv1alpha1.NomadSecretsConfig{} })
}

// nomadLeaseKeys are the payload keys written to <mountPath>/config/lease; every other key goes to config/access.
var nomadLeaseKeys = []string{"ttl", "max_ttl"}

// NomadSecretsConfigAdapter maps a NomadSecretsConfig onto <mountPath>/config/access and <mountPath>/config/lease.
type NomadSecretsConfigAdapter struct {
	hashKey []byte
}

var (
	_ engine.Adapter[*secretsv1alpha1.NomadSecretsConfig]          = NomadSecretsConfigAdapter{}
	_ engine.Observer[*secretsv1alpha1.NomadSecretsConfig]         = NomadSecretsConfigAdapter{}
	_ engine.Writer[*secretsv1alpha1.NomadSecretsConfig]           = NomadSecretsConfigAdapter{}
	_ engine.SecretReferencer[*secretsv1alpha1.NomadSecretsConfig] = NomadSecretsConfigAdapter{}
)

// Path implements engine.Adapter. It names the access path, which decides whether the config exists.
func (NomadSecretsConfigAdapter) Path(c *secretsv1alpha1.NomadSecretsConfig) string {
	return nomadConfigPath(c) + "/access"
}

// Desired implements engine.Adapter. The payload holds the keys of both paths; Write splits them.
func (NomadSecretsConfigAdapter) Desired(ctx context.Context, r engine.Resolver, c *secretsv1alpha1.NomadSecretsConfig) (engine.Payload, error) {
	s := c.Spec
	p := engine.Payload{"address": s.Address}
	if s.MaxTokenNameLength != nil {
		p["max_token_name_length"] = int64(*s.MaxTokenNameLength)
	}
	optString(p, "ca_cert", s.CACert)
	optString(p, "client_cert", s.ClientCert)
	optSeconds(p, "ttl", s.TTL)
	optSeconds(p, "max_ttl", s.MaxTTL)
	if err := putPluginSecret(ctx, r, c.Namespace, p, "token", &s.TokenSecretRef); err != nil {
		return nil, err
	}
	if err := putPluginSecret(ctx, r, c.Namespace, p, "client_key", s.ClientKeySecretRef); err != nil {
		return nil, err
	}
	return p, nil
}

// Fields implements engine.Adapter.
func (NomadSecretsConfigAdapter) Fields() engine.FieldSpec {
	return engine.FieldSpec{
		WriteOnly: []string{"token", "client_key"},
		Durations: []string{"ttl", "max_ttl"},
	}
}

// Observe implements engine.Observer: the access read, with the lease keys of the lease read merged in.
func (NomadSecretsConfigAdapter) Observe(ctx context.Context, c openbao.Client, cfg *secretsv1alpha1.NomadSecretsConfig) (engine.Payload, bool, error) {
	observed, err := c.Read(ctx, nomadConfigPath(cfg)+"/access")
	if errors.Is(err, openbao.ErrNotFound) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	if observed == nil {
		observed = engine.Payload{}
	}
	lease, err := c.Read(ctx, nomadConfigPath(cfg)+"/lease")
	if err != nil && !errors.Is(err, openbao.ErrNotFound) {
		return nil, false, err
	}
	for _, key := range nomadLeaseKeys {
		if value, ok := lease[key]; ok {
			observed[key] = value
		}
	}
	return observed, true, nil
}

// Write implements engine.Writer. The lease is written first, only when the spec sets a lease key that differs from
// the server; the access settings follow only when they are new, differ or their write-only inputs changed.
func (a NomadSecretsConfigAdapter) Write(ctx context.Context, c openbao.Client, cfg *secretsv1alpha1.NomadSecretsConfig, desired engine.Payload, create bool) (engine.Payload, error) {
	lease, access := splitPayload(desired, nomadLeaseKeys)
	var wroteLease bool
	if len(lease) > 0 {
		var err error
		if wroteLease, err = writeIfDiffers(ctx, c, nomadConfigPath(cfg)+"/lease", lease, a.Fields()); err != nil {
			return nil, err
		}
	}
	accessPath := nomadConfigPath(cfg) + "/access"
	if create || writeOnlyChanged(a.hashKey, cfg, "NomadSecretsConfig", desired, a.Fields()) {
		if _, err := c.Write(ctx, accessPath, access); err != nil {
			return nil, err
		}
		return nil, nil
	}
	wroteAccess, err := writeIfDiffers(ctx, c, accessPath, access, a.Fields())
	if err != nil {
		return nil, err
	}
	if !wroteLease && !wroteAccess {
		return nil, engine.ErrNothingWritten
	}
	return nil, nil
}

// SecretRefs implements engine.SecretReferencer.
func (NomadSecretsConfigAdapter) SecretRefs(c *secretsv1alpha1.NomadSecretsConfig) []string {
	return pluginSecretNames(&c.Spec.TokenSecretRef, c.Spec.ClientKeySecretRef)
}

func nomadConfigPath(c *secretsv1alpha1.NomadSecretsConfig) string {
	return pluginMountPath(c.Spec.MountPath, "nomad") + "/config"
}
