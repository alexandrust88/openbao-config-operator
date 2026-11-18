package secrets

import (
	"context"

	ctrl "sigs.k8s.io/controller-runtime"

	secretsv1alpha1 "github.com/alexandrust88/openbao-config-operator/api/secrets/v1alpha1"
	"github.com/alexandrust88/openbao-config-operator/internal/engine"
)

// +kubebuilder:rbac:groups=secrets.obco.dev,resources=kubernetessecretsroles,verbs=get;list;watch;update;patch
// +kubebuilder:rbac:groups=secrets.obco.dev,resources=kubernetessecretsroles/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=secrets.obco.dev,resources=kubernetessecretsroles/finalizers,verbs=update

// SetupKubernetesSecretsRole registers the KubernetesSecretsRole controller with the manager.
func SetupKubernetesSecretsRole(mgr ctrl.Manager, conns engine.Connections, opts engine.Options) error {
	r := engine.New[*secretsv1alpha1.KubernetesSecretsRole](
		mgr.GetClient(),
		mgr.GetScheme(),
		mgr.GetEventRecorder("kubernetessecretsrole-controller"),
		conns,
		KubernetesSecretsRoleAdapter{},
		func() *secretsv1alpha1.KubernetesSecretsRole { return &secretsv1alpha1.KubernetesSecretsRole{} },
		opts,
	)
	return r.SetupWithManager(mgr)
}

// KubernetesSecretsRoleAdapter maps a KubernetesSecretsRole onto <mount>/roles/<name>.
type KubernetesSecretsRoleAdapter struct{}

var _ engine.Adapter[*secretsv1alpha1.KubernetesSecretsRole] = KubernetesSecretsRoleAdapter{}

// Path implements engine.Adapter.
func (KubernetesSecretsRoleAdapter) Path(r *secretsv1alpha1.KubernetesSecretsRole) string {
	return r.Spec.MountPath + "/roles/" + specName(r.Spec.Name, r.Name)
}

// Desired implements engine.Adapter.
func (KubernetesSecretsRoleAdapter) Desired(_ context.Context, _ engine.Resolver, r *secretsv1alpha1.KubernetesSecretsRole) (engine.Payload, error) {
	s := r.Spec
	p := engine.Payload{}
	orList(p, "allowed_kubernetes_namespaces", s.AllowedKubernetesNamespaces)
	orString(p, "allowed_kubernetes_namespace_selector", s.AllowedKubernetesNamespaceSelector, "")
	orSeconds(p, "token_default_ttl", s.TokenDefaultTTL, 0)
	orSeconds(p, "token_max_ttl", s.TokenMaxTTL, 0)
	orList(p, "token_default_audiences", s.TokenDefaultAudiences)
	orString(p, "service_account_name", s.ServiceAccountName, "")
	orString(p, "kubernetes_role_name", s.KubernetesRoleName, "")
	orString(p, "kubernetes_role_type", s.KubernetesRoleType, "Role")
	orString(p, "generated_role_rules", s.GeneratedRoleRules, "")
	orString(p, "name_template", s.NameTemplate, "")
	orMap(p, "extra_labels", s.ExtraLabels)
	orMap(p, "extra_annotations", s.ExtraAnnotations)
	return p, nil
}

// Fields implements engine.Adapter.
func (KubernetesSecretsRoleAdapter) Fields() engine.FieldSpec {
	return engine.FieldSpec{
		Sets:      []string{"allowed_kubernetes_namespaces", "token_default_audiences"},
		Durations: []string{"token_default_ttl", "token_max_ttl"},
	}
}
