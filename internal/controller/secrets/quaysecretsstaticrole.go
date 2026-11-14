package secrets

import (
	"context"

	ctrl "sigs.k8s.io/controller-runtime"

	secretsv1alpha1 "github.com/alexandrust88/openbao-config-operator/api/secrets/v1alpha1"
	"github.com/alexandrust88/openbao-config-operator/internal/engine"
	"github.com/alexandrust88/openbao-config-operator/internal/openbao"
)

// +kubebuilder:rbac:groups=secrets.openbaoconfig.io,resources=quaysecretsstaticroles,verbs=get;list;watch;update;patch
// +kubebuilder:rbac:groups=secrets.openbaoconfig.io,resources=quaysecretsstaticroles/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=secrets.openbaoconfig.io,resources=quaysecretsstaticroles/finalizers,verbs=update

// SetupQuaySecretsStaticRole registers the QuaySecretsStaticRole controller with the manager.
func SetupQuaySecretsStaticRole(mgr ctrl.Manager, conns engine.Connections, opts engine.Options) error {
	return setupPlugin(mgr, conns, opts, "quaysecretsstaticrole", QuaySecretsStaticRoleAdapter{},
		func() *secretsv1alpha1.QuaySecretsStaticRole { return &secretsv1alpha1.QuaySecretsStaticRole{} })
}

// QuaySecretsStaticRoleAdapter maps a QuaySecretsStaticRole onto <mountPath>/static-roles/<name>.
type QuaySecretsStaticRoleAdapter struct{}

var (
	_ engine.Adapter[*secretsv1alpha1.QuaySecretsStaticRole]  = QuaySecretsStaticRoleAdapter{}
	_ engine.Observer[*secretsv1alpha1.QuaySecretsStaticRole] = QuaySecretsStaticRoleAdapter{}
)

// Path implements engine.Adapter.
func (QuaySecretsStaticRoleAdapter) Path(r *secretsv1alpha1.QuaySecretsStaticRole) string {
	return pluginMountPath(r.Spec.MountPath, "quay") + "/static-roles/" + specName(r.Spec.Name, r.Name)
}

// Desired implements engine.Adapter.
func (QuaySecretsStaticRoleAdapter) Desired(_ context.Context, _ engine.Resolver, r *secretsv1alpha1.QuaySecretsStaticRole) (engine.Payload, error) {
	return quayRobotPayload(r.Spec.QuayRobotAccount)
}

// Fields implements engine.Adapter.
func (QuaySecretsStaticRoleAdapter) Fields() engine.FieldSpec { return engine.FieldSpec{} }

// Observe implements engine.Observer.
func (a QuaySecretsStaticRoleAdapter) Observe(ctx context.Context, c openbao.Client, r *secretsv1alpha1.QuaySecretsStaticRole) (engine.Payload, bool, error) {
	return quayObserve(ctx, c, a.Path(r))
}
