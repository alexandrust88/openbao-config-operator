package secrets

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"slices"

	ctrl "sigs.k8s.io/controller-runtime"

	secretsv1alpha1 "github.com/alexandrust88/openbao-config-operator/api/secrets/v1alpha1"
	"github.com/alexandrust88/openbao-config-operator/internal/engine"
	"github.com/alexandrust88/openbao-config-operator/internal/openbao"
)

// +kubebuilder:rbac:groups=secrets.openbaoconfig.io,resources=rabbitmqconfigs,verbs=get;list;watch;update;patch
// +kubebuilder:rbac:groups=secrets.openbaoconfig.io,resources=rabbitmqconfigs/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=secrets.openbaoconfig.io,resources=rabbitmqconfigs/finalizers,verbs=update
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch

// SetupRabbitMQConfig registers the RabbitMQConfig controller with the manager.
func SetupRabbitMQConfig(mgr ctrl.Manager, conns engine.Connections, opts engine.Options) error {
	opts = withHashKey(opts)
	r := engine.New[*secretsv1alpha1.RabbitMQConfig](
		mgr.GetClient(),
		mgr.GetScheme(),
		mgr.GetEventRecorder("rabbitmqconfig-controller"),
		conns,
		RabbitMQConfigAdapter{hashKey: opts.HashKey},
		func() *secretsv1alpha1.RabbitMQConfig { return &secretsv1alpha1.RabbitMQConfig{} },
		opts,
	)
	return r.SetupWithManager(mgr)
}

// RabbitMQConfigAdapter maps a RabbitMQConfig onto <mount>/config/connection and <mount>/config/lease.
type RabbitMQConfigAdapter struct {
	hashKey []byte
}

var (
	_ engine.Adapter[*secretsv1alpha1.RabbitMQConfig]          = RabbitMQConfigAdapter{}
	_ engine.Observer[*secretsv1alpha1.RabbitMQConfig]         = RabbitMQConfigAdapter{}
	_ engine.Writer[*secretsv1alpha1.RabbitMQConfig]           = RabbitMQConfigAdapter{}
	_ engine.SecretReferencer[*secretsv1alpha1.RabbitMQConfig] = RabbitMQConfigAdapter{}
)

var rabbitMQLeaseKeys = []string{"ttl", "max_ttl"}

// Path implements engine.Adapter.
func (RabbitMQConfigAdapter) Path(c *secretsv1alpha1.RabbitMQConfig) string {
	return c.Spec.MountPath + "/config/connection"
}

// Desired implements engine.Adapter: the connection settings and both lease durations.
func (RabbitMQConfigAdapter) Desired(ctx context.Context, r engine.Resolver, c *secretsv1alpha1.RabbitMQConfig) (engine.Payload, error) {
	password, err := r.SecretValue(ctx, c.Namespace, c.Spec.PasswordSecretRef)
	if err != nil {
		return nil, err
	}
	p := engine.Payload{
		"connection_uri": c.Spec.ConnectionURI,
		"username":       c.Spec.Username,
		"password":       string(password),
	}
	optBool(p, "verify_connection", c.Spec.VerifyConnection)
	optString(p, "password_policy", c.Spec.PasswordPolicy)
	optString(p, "username_template", c.Spec.UsernameTemplate)
	p["ttl"], p["max_ttl"] = int64(0), int64(0)
	if lease := c.Spec.Lease; lease != nil {
		optSeconds(p, "ttl", lease.TTL)
		optSeconds(p, "max_ttl", lease.MaxTTL)
	}
	return p, nil
}

// Fields implements engine.Adapter. The connection endpoint cannot be read, so all of its settings are
// write-only.
func (RabbitMQConfigAdapter) Fields() engine.FieldSpec {
	return engine.FieldSpec{
		WriteOnly: []string{"connection_uri", "username", "password", "verify_connection", "password_policy", "username_template"},
		Durations: rabbitMQLeaseKeys,
	}
}

// Observe implements engine.Observer. Only the lease is readable; the configuration counts as present once
// the operator has written it or a lease exists.
func (RabbitMQConfigAdapter) Observe(ctx context.Context, c openbao.Client, obj *secretsv1alpha1.RabbitMQConfig) (engine.Payload, bool, error) {
	path := obj.Spec.MountPath + "/config/lease"
	lease, err := c.Read(ctx, path)
	switch {
	case errors.Is(err, openbao.ErrNotFound):
		return engine.Payload{}, obj.Status.Acquired != "", nil
	case err != nil:
		return nil, false, fmt.Errorf("read %s: %w", path, err)
	case lease == nil:
		lease = engine.Payload{}
	}
	return lease, true, nil
}

// Write implements engine.Writer. The lease goes first and only when it differs from the server; the connection
// follows only when it is new or its write-only inputs changed, so that lease drift never re-validates the
// connection and a failed connection write does not resend the lease on retry.
func (a RabbitMQConfigAdapter) Write(ctx context.Context, c openbao.Client, obj *secretsv1alpha1.RabbitMQConfig, desired engine.Payload, create bool) (engine.Payload, error) {
	lease, connection := splitPayload(desired, rabbitMQLeaseKeys)
	leasePath := obj.Spec.MountPath + "/config/lease"
	wrote, err := writeIfDiffers(ctx, c, leasePath, lease, a.Fields())
	if err != nil {
		return nil, err
	}
	if create || writeOnlyChanged(a.hashKey, obj, "RabbitMQConfig", desired, a.Fields()) {
		path := a.Path(obj)
		if _, err := c.Write(ctx, path, connection); err != nil {
			return nil, fmt.Errorf("write %s: %w", path, err)
		}
		wrote = true
	}
	if !wrote {
		return nil, engine.ErrNothingWritten
	}
	return nil, nil
}

// splitPayload returns the keys of desired named in keys, and the rest.
func splitPayload(desired engine.Payload, keys []string) (picked, rest engine.Payload) {
	picked, rest = engine.Payload{}, engine.Payload{}
	for key, value := range desired {
		if slices.Contains(keys, key) {
			picked[key] = value
		} else {
			rest[key] = value
		}
	}
	return picked, rest
}

// writeIfDiffers writes body to path unless a read of path already holds it, and reports whether it wrote; an
// absent path counts as differing.
func writeIfDiffers(ctx context.Context, c openbao.Client, path string, body engine.Payload, fields engine.FieldSpec) (bool, error) {
	current, err := c.Read(ctx, path)
	if err != nil && !errors.Is(err, openbao.ErrNotFound) {
		return false, fmt.Errorf("read %s: %w", path, err)
	}
	if len(engine.Diff(body, current, fields)) == 0 {
		return false, nil
	}
	if _, err := c.Write(ctx, path, body); err != nil {
		return false, fmt.Errorf("write %s: %w", path, err)
	}
	return true, nil
}

// writeOnlyChanged reports whether the write-only inputs of desired differ from the last ones written to obj of
// kind, by the hash the engine keeps in status.writeOnlyHash. Without the engine's key every write counts as changed.
func writeOnlyChanged(key []byte, obj engine.Object, kind string, desired engine.Payload, fields engine.FieldSpec) bool {
	if len(key) == 0 {
		return true
	}
	objectKey := engine.ObjectHashKey(key, obj.GetUID(), secretsv1alpha1.GroupVersion.WithKind(kind).GroupKind())
	return engine.WriteOnlyHash(objectKey, desired, fields) != obj.GetResourceStatus().WriteOnlyHash
}

// withHashKey gives opts a hash key when it has none, so that the engine and the adapter hash with the same key.
func withHashKey(opts engine.Options) engine.Options {
	if len(opts.HashKey) == 0 {
		opts.HashKey = make([]byte, 32)
		_, _ = rand.Read(opts.HashKey)
	}
	return opts
}

// SecretRefs implements engine.SecretReferencer.
func (RabbitMQConfigAdapter) SecretRefs(c *secretsv1alpha1.RabbitMQConfig) []string {
	return []string{c.Spec.PasswordSecretRef.Name}
}
