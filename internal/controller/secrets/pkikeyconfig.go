package secrets

import (
	"context"
	"errors"

	ctrl "sigs.k8s.io/controller-runtime"

	secretsv1alpha1 "github.com/alexandrust88/openbao-config-operator/api/secrets/v1alpha1"
	"github.com/alexandrust88/openbao-config-operator/internal/engine"
	"github.com/alexandrust88/openbao-config-operator/internal/openbao"
)

// +kubebuilder:rbac:groups=secrets.openbaoconfig.io,resources=pkikeyconfigs,verbs=get;list;watch;update;patch
// +kubebuilder:rbac:groups=secrets.openbaoconfig.io,resources=pkikeyconfigs/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=secrets.openbaoconfig.io,resources=pkikeyconfigs/finalizers,verbs=update

// SetupPKIKeyConfig registers the PKIKeyConfig controller with the manager.
func SetupPKIKeyConfig(mgr ctrl.Manager, conns engine.Connections, opts engine.Options) error {
	r := engine.New[*secretsv1alpha1.PKIKeyConfig](
		mgr.GetClient(),
		mgr.GetScheme(),
		mgr.GetEventRecorder("pkikeyconfig-controller"),
		conns,
		PKIKeyConfigAdapter{},
		func() *secretsv1alpha1.PKIKeyConfig { return &secretsv1alpha1.PKIKeyConfig{} },
		opts,
	)
	return r.SetupWithManager(mgr)
}

// PKIKeyConfigAdapter maps a PKIKeyConfig onto <mount>/config/keys. The path has no delete, so the default policy is Orphan.
type PKIKeyConfigAdapter struct{}

var (
	_ engine.Adapter[*secretsv1alpha1.PKIKeyConfig]  = PKIKeyConfigAdapter{}
	_ engine.Observer[*secretsv1alpha1.PKIKeyConfig] = PKIKeyConfigAdapter{}
	_ engine.Singleton                               = PKIKeyConfigAdapter{}
)

// Path implements engine.Adapter.
func (PKIKeyConfigAdapter) Path(k *secretsv1alpha1.PKIKeyConfig) string {
	return pkiMount(k.Spec.MountPath) + "/config/keys"
}

// Desired implements engine.Adapter.
func (PKIKeyConfigAdapter) Desired(_ context.Context, _ engine.Resolver, k *secretsv1alpha1.PKIKeyConfig) (engine.Payload, error) {
	return engine.Payload{"default": k.Spec.DefaultKey}, nil
}

// Fields implements engine.Adapter.
func (PKIKeyConfigAdapter) Fields() engine.FieldSpec { return engine.FieldSpec{} }

// Singleton implements engine.Singleton: the setting always exists, so it is adopted whatever the creationPolicy.
func (PKIKeyConfigAdapter) Singleton() bool { return true }

// pkiKeyConfigObserved normalizes a config/keys read so a default stored as a key id does not drift when the spec names the key.
func pkiKeyConfigObserved(config engine.Payload, key engine.Payload, keyFound bool, defaultKey string) engine.Payload {
	if pkiString(config["default"]) == defaultKey {
		return config
	}
	if keyFound {
		configDefault := pkiString(config["default"])
		if configDefault == pkiString(key["id"]) || configDefault == pkiString(key["key_id"]) {
			return engine.Payload{"default": defaultKey}
		}
	}
	return config
}

// Observe implements engine.Observer.
func (PKIKeyConfigAdapter) Observe(ctx context.Context, c openbao.Client, k *secretsv1alpha1.PKIKeyConfig) (engine.Payload, bool, error) {
	mount := pkiMount(k.Spec.MountPath)
	data, err := c.Read(ctx, mount+"/config/keys")
	if errors.Is(err, openbao.ErrNotFound) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	defaultKey := k.Spec.DefaultKey
	if pkiString(data["default"]) == defaultKey {
		return data, true, nil
	}
	keyData, found, err := pkiReadRef(ctx, c, mount, "key", defaultKey)
	if err != nil {
		return nil, false, err
	}
	// OpenBao stores the key id when the write used a name.
	return pkiKeyConfigObserved(data, keyData, found, defaultKey), true, nil
}
