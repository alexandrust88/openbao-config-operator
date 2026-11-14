package auth

import (
	"context"
	"encoding/json"
	"fmt"

	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/yaml"

	authv1alpha1 "github.com/alexandrust88/openbao-config-operator/api/auth/v1alpha1"
	"github.com/alexandrust88/openbao-config-operator/internal/engine"
	"github.com/alexandrust88/openbao-config-operator/internal/openbao"
)

// +kubebuilder:rbac:groups=auth.openbaoconfig.io,resources=kubernetesauthroles,verbs=get;list;watch;update;patch
// +kubebuilder:rbac:groups=auth.openbaoconfig.io,resources=kubernetesauthroles/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=auth.openbaoconfig.io,resources=kubernetesauthroles/finalizers,verbs=update

// SetupKubernetesAuthRole registers the KubernetesAuthRole controller with the manager.
func SetupKubernetesAuthRole(mgr ctrl.Manager, conns engine.Connections, opts engine.Options) error {
	r := engine.New[*authv1alpha1.KubernetesAuthRole](
		mgr.GetClient(),
		mgr.GetScheme(),
		mgr.GetEventRecorder("kubernetesauthrole-controller"),
		conns,
		KubernetesAuthRoleAdapter{},
		func() *authv1alpha1.KubernetesAuthRole { return &authv1alpha1.KubernetesAuthRole{} },
		opts,
	)
	return r.SetupWithManager(mgr)
}

const namespaceSelectorKey = "bound_service_account_namespace_selector"

// KubernetesAuthRoleAdapter maps a KubernetesAuthRole onto auth/<mount>/role/<name>.
type KubernetesAuthRoleAdapter struct{}

var (
	_ engine.Adapter[*authv1alpha1.KubernetesAuthRole]  = KubernetesAuthRoleAdapter{}
	_ engine.DriftTier                                  = KubernetesAuthRoleAdapter{}
	_ engine.Observer[*authv1alpha1.KubernetesAuthRole] = KubernetesAuthRoleAdapter{}
)

// Path implements engine.Adapter.
func (KubernetesAuthRoleAdapter) Path(k *authv1alpha1.KubernetesAuthRole) string {
	return "auth/" + authMount(k.Spec.MountPath, "kubernetes") + "/role/" + authName(k.Spec.Name, k.Name)
}

// Desired implements engine.Adapter. Unset fields are sent as the OpenBao default, so removing one resets it.
func (KubernetesAuthRoleAdapter) Desired(ctx context.Context, r engine.Resolver, k *authv1alpha1.KubernetesAuthRole) (engine.Payload, error) {
	s := k.Spec
	selector, err := canonicalSelector(s.BoundServiceAccountNamespaceSelector)
	if err != nil {
		return nil, err
	}
	p := engine.Payload{
		"bound_service_account_names":      s.BoundServiceAccountNames,
		"bound_service_account_namespaces": builtinList(s.BoundServiceAccountNamespaces),
		namespaceSelectorKey:               selector,
		"audience":                         s.Audience,
		"alias_name_source":                string(orDefault(s.AliasNameSource, authv1alpha1.AliasNameSourceServiceAccountUID)),
	}
	if err := putTokenFields(ctx, r, k, p, s.TokenFields); err != nil {
		return nil, err
	}
	putTokenFieldDefaults(p)
	return p, nil
}

// Fields implements engine.Adapter.
func (KubernetesAuthRoleAdapter) Fields() engine.FieldSpec {
	return MergeFieldSpecs(engine.FieldSpec{
		Sets: []string{"bound_service_account_names", "bound_service_account_namespaces"},
	}, TokenFieldSpec)
}

// DriftTier implements engine.DriftTier.
func (KubernetesAuthRoleAdapter) DriftTier() engine.Tier { return engine.TierAccessCritical }

// Observe implements engine.Observer. The read omits an empty audience. The stored namespace selector is
// re-encoded the way Desired encodes it, so a selector written as YAML or with another key order is not drift.
func (a KubernetesAuthRoleAdapter) Observe(ctx context.Context, c openbao.Client, k *authv1alpha1.KubernetesAuthRole) (engine.Payload, bool, error) {
	observed, found, err := readWithDefaults(ctx, c, a.Path(k), engine.Payload{"audience": ""})
	if !found || err != nil {
		return nil, found, err
	}
	if stored, ok := observed[namespaceSelectorKey].(string); ok && stored != "" {
		var selector authv1alpha1.NamespaceLabelSelector
		if yaml.Unmarshal([]byte(stored), &selector) == nil {
			if canonical, err := canonicalSelector(&selector); err == nil {
				observed[namespaceSelectorKey] = canonical
			}
		}
	}
	return observed, true, nil
}

func canonicalSelector(selector *authv1alpha1.NamespaceLabelSelector) (string, error) {
	if selector == nil {
		return "", nil
	}
	encoded, err := json.Marshal(authv1alpha1.NamespaceLabelSelector{MatchLabels: selector.MatchLabels})
	if err != nil {
		return "", fmt.Errorf("encode boundServiceAccountNamespaceSelector: %w", err)
	}
	return string(encoded), nil
}
