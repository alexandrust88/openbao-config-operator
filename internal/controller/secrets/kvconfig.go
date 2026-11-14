package secrets

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"k8s.io/apimachinery/pkg/util/intstr"
	ctrl "sigs.k8s.io/controller-runtime"

	secretsv1alpha1 "github.com/alexandrust88/openbao-config-operator/api/secrets/v1alpha1"
	"github.com/alexandrust88/openbao-config-operator/internal/engine"
)

// +kubebuilder:rbac:groups=secrets.openbaoconfig.io,resources=kvconfigs,verbs=get;list;watch;update;patch
// +kubebuilder:rbac:groups=secrets.openbaoconfig.io,resources=kvconfigs/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=secrets.openbaoconfig.io,resources=kvconfigs/finalizers,verbs=update

// SetupKVConfig registers the KVConfig controller with the manager.
func SetupKVConfig(mgr ctrl.Manager, conns engine.Connections, opts engine.Options) error {
	r := engine.New[*secretsv1alpha1.KVConfig](
		mgr.GetClient(),
		mgr.GetScheme(),
		mgr.GetEventRecorder("kvconfig-controller"),
		conns,
		KVConfigAdapter{},
		func() *secretsv1alpha1.KVConfig { return &secretsv1alpha1.KVConfig{} },
		opts,
	)
	return r.SetupWithManager(mgr)
}

// KVConfigAdapter maps a KVConfig onto <mount>/config. The path has no delete, so the default policy is Orphan.
type KVConfigAdapter struct{}

var (
	_ engine.Adapter[*secretsv1alpha1.KVConfig] = KVConfigAdapter{}
	_ engine.Singleton                          = KVConfigAdapter{}
)

// Path implements engine.Adapter.
func (KVConfigAdapter) Path(c *secretsv1alpha1.KVConfig) string {
	return c.Spec.MountPath + "/config"
}

// Desired implements engine.Adapter.
func (KVConfigAdapter) Desired(_ context.Context, _ engine.Resolver, c *secretsv1alpha1.KVConfig) (engine.Payload, error) {
	p := engine.Payload{"delete_version_after": int64(0)}
	orInt32(p, "max_versions", c.Spec.MaxVersions, 0)
	orFalse(p, "cas_required", c.Spec.CASRequired)
	orFalse(p, "metadata_cas_required", c.Spec.MetadataCASRequired)
	if c.Spec.DeleteVersionAfter != nil {
		seconds, err := durationSeconds(*c.Spec.DeleteVersionAfter)
		if err != nil {
			return nil, fmt.Errorf("spec.deleteVersionAfter: %w", err)
		}
		p["delete_version_after"] = seconds
	}
	return p, nil
}

// Fields implements engine.Adapter.
func (KVConfigAdapter) Fields() engine.FieldSpec {
	return engine.FieldSpec{Durations: []string{"delete_version_after"}}
}

// Singleton implements engine.Singleton: the setting always exists, so it is adopted whatever the creationPolicy.
func (KVConfigAdapter) Singleton() bool { return true }

// durationSeconds converts integer seconds, a numeric string or a Go duration string into whole seconds.
func durationSeconds(v intstr.IntOrString) (int64, error) {
	if v.Type == intstr.Int {
		return int64(v.IntVal), nil
	}
	if n, err := strconv.ParseInt(v.StrVal, 10, 64); err == nil {
		return n, nil
	}
	d, err := time.ParseDuration(v.StrVal)
	if err != nil {
		return 0, fmt.Errorf("invalid duration %q", v.StrVal)
	}
	return int64(d / time.Second), nil
}
