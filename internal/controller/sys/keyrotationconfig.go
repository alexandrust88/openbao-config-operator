package sys

import (
	"context"
	"fmt"

	ctrl "sigs.k8s.io/controller-runtime"

	sysv1alpha1 "github.com/alexandrust88/openbao-config-operator/api/sys/v1alpha1"
	"github.com/alexandrust88/openbao-config-operator/internal/engine"
	"github.com/alexandrust88/openbao-config-operator/internal/openbao"
)

// +kubebuilder:rbac:groups=sys.obco.dev,resources=keyrotationconfigs,verbs=get;list;watch;update;patch
// +kubebuilder:rbac:groups=sys.obco.dev,resources=keyrotationconfigs/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=sys.obco.dev,resources=keyrotationconfigs/finalizers,verbs=update

const (
	keyRotationConfigPath = "sys/rotate/config"
	keyRotationInterval   = "interval"
	// keyRotationMaxOperations is the OpenBao default and maximum of max_operations (vault/barrier/keyring.go).
	keyRotationMaxOperations = int64(3_865_470_566)
)

// SetupKeyRotationConfig registers the KeyRotationConfig controller with the manager.
func SetupKeyRotationConfig(mgr ctrl.Manager, conns engine.Connections, opts engine.Options) error {
	r := engine.New[*sysv1alpha1.KeyRotationConfig](
		mgr.GetClient(),
		mgr.GetScheme(),
		mgr.GetEventRecorder("keyrotationconfig-controller"),
		conns,
		KeyRotationConfigAdapter{},
		func() *sysv1alpha1.KeyRotationConfig { return &sysv1alpha1.KeyRotationConfig{} },
		opts,
	)
	return r.SetupWithManager(mgr)
}

// KeyRotationConfigAdapter maps a KeyRotationConfig onto sys/rotate/config. The path has no DELETE; deleting
// resets it.
type KeyRotationConfigAdapter struct{}

var (
	_ engine.Adapter[*sysv1alpha1.KeyRotationConfig]  = KeyRotationConfigAdapter{}
	_ engine.Observer[*sysv1alpha1.KeyRotationConfig] = KeyRotationConfigAdapter{}
	_ engine.Deleter[*sysv1alpha1.KeyRotationConfig]  = KeyRotationConfigAdapter{}
	_ engine.Singleton                                = KeyRotationConfigAdapter{}
)

// Path implements engine.Adapter.
func (KeyRotationConfigAdapter) Path(*sysv1alpha1.KeyRotationConfig) string {
	return keyRotationConfigPath
}

// Desired implements engine.Adapter. Every field is sent, an omitted one at its OpenBao default, so an emptied
// field returns to the default.
func (KeyRotationConfigAdapter) Desired(_ context.Context, _ engine.Resolver, k *sysv1alpha1.KeyRotationConfig) (engine.Payload, error) {
	return keyRotationPayload(k.Spec), nil
}

func keyRotationPayload(spec sysv1alpha1.KeyRotationConfigSpec) engine.Payload {
	p := engine.Payload{"enabled": true, "max_operations": keyRotationMaxOperations, keyRotationInterval: "0"}
	if spec.Enabled != nil {
		p["enabled"] = *spec.Enabled
	}
	if spec.MaxOperations != 0 {
		p["max_operations"] = spec.MaxOperations
	}
	if spec.Interval != "" {
		p[keyRotationInterval] = spec.Interval
	}
	return p
}

// Fields implements engine.Adapter.
func (KeyRotationConfigAdapter) Fields() engine.FieldSpec {
	return engine.FieldSpec{Durations: []string{keyRotationInterval}}
}

// Observe implements engine.Observer. A namespace without its own barrier has no rotation config.
func (KeyRotationConfigAdapter) Observe(ctx context.Context, c openbao.Client, _ *sysv1alpha1.KeyRotationConfig) (engine.Payload, bool, error) {
	observed, err := c.Read(ctx, keyRotationConfigPath)
	if badRequestContains(err, "namespace is not sealable") {
		return nil, false, fmt.Errorf("KeyRotationConfig needs the root namespace or a sealable namespace: %w", engine.ErrUnsupported)
	}
	if err != nil {
		return nil, false, err
	}
	return observed, true, nil
}

// Delete implements engine.Deleter by writing the OpenBao defaults.
func (KeyRotationConfigAdapter) Delete(ctx context.Context, c openbao.Client, _ *sysv1alpha1.KeyRotationConfig) error {
	_, err := c.Write(ctx, keyRotationConfigPath, keyRotationPayload(sysv1alpha1.KeyRotationConfigSpec{}))
	if badRequestContains(err, "namespace is not sealable") {
		return nil
	}
	return err
}

// Singleton implements engine.Singleton: the setting always exists, so it is adopted whatever the creationPolicy.
func (KeyRotationConfigAdapter) Singleton() bool { return true }
