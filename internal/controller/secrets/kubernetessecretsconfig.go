package secrets

import (
	"context"

	ctrl "sigs.k8s.io/controller-runtime"

	secretsv1alpha1 "github.com/alexandrust88/openbao-config-operator/api/secrets/v1alpha1"
	"github.com/alexandrust88/openbao-config-operator/internal/engine"
)

// +kubebuilder:rbac:groups=secrets.openbaoconfig.io,resources=kubernetessecretsconfigs,verbs=get;list;watch;update;patch
// +kubebuilder:rbac:groups=secrets.openbaoconfig.io,resources=kubernetessecretsconfigs/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=secrets.openbaoconfig.io,resources=kubernetessecretsconfigs/finalizers,verbs=update
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch

// SetupKubernetesSecretsConfig registers the KubernetesSecretsConfig controller with the manager.
func SetupKubernetesSecretsConfig(mgr ctrl.Manager, conns engine.Connections, opts engine.Options) error {
	r := engine.New[*secretsv1alpha1.KubernetesSecretsConfig](
		mgr.GetClient(),
		mgr.GetScheme(),
		mgr.GetEventRecorder("kubernetessecretsconfig-controller"),
		conns,
		KubernetesSecretsConfigAdapter{},
		func() *secretsv1alpha1.KubernetesSecretsConfig { return &secretsv1alpha1.KubernetesSecretsConfig{} },
		opts,
	)
	return r.SetupWithManager(mgr)
}

// KubernetesSecretsConfigAdapter maps a KubernetesSecretsConfig onto <mount>/config.
type KubernetesSecretsConfigAdapter struct{}

var (
	_ engine.Adapter[*secretsv1alpha1.KubernetesSecretsConfig]          = KubernetesSecretsConfigAdapter{}
	_ engine.SecretReferencer[*secretsv1alpha1.KubernetesSecretsConfig] = KubernetesSecretsConfigAdapter{}
)

// Path implements engine.Adapter.
func (KubernetesSecretsConfigAdapter) Path(c *secretsv1alpha1.KubernetesSecretsConfig) string {
	return c.Spec.MountPath + "/config"
}

// Desired implements engine.Adapter.
func (KubernetesSecretsConfigAdapter) Desired(ctx context.Context, r engine.Resolver, c *secretsv1alpha1.KubernetesSecretsConfig) (engine.Payload, error) {
	p := engine.Payload{}
	optString(p, "kubernetes_host", c.Spec.KubernetesHost)
	orString(p, "kubernetes_ca_cert", c.Spec.KubernetesCACert, "")
	orFalse(p, "disable_local_ca_jwt", c.Spec.DisableLocalCAJWT)
	if ref := c.Spec.ServiceAccountJWTSecretRef; ref != nil {
		jwt, err := r.SecretValue(ctx, c.Namespace, *ref)
		if err != nil {
			return nil, err
		}
		p["service_account_jwt"] = string(jwt)
	}
	return p, nil
}

// Fields implements engine.Adapter.
func (KubernetesSecretsConfigAdapter) Fields() engine.FieldSpec {
	return engine.FieldSpec{WriteOnly: []string{"service_account_jwt"}}
}

// SecretRefs implements engine.SecretReferencer.
func (KubernetesSecretsConfigAdapter) SecretRefs(c *secretsv1alpha1.KubernetesSecretsConfig) []string {
	if c.Spec.ServiceAccountJWTSecretRef == nil {
		return nil
	}
	return []string{c.Spec.ServiceAccountJWTSecretRef.Name}
}
