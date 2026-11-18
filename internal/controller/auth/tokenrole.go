package auth

import (
	"context"

	ctrl "sigs.k8s.io/controller-runtime"

	authv1alpha1 "github.com/alexandrust88/openbao-config-operator/api/auth/v1alpha1"
	"github.com/alexandrust88/openbao-config-operator/internal/engine"
	"github.com/alexandrust88/openbao-config-operator/internal/openbao"
)

// +kubebuilder:rbac:groups=auth.obco.dev,resources=tokenroles,verbs=get;list;watch;update;patch
// +kubebuilder:rbac:groups=auth.obco.dev,resources=tokenroles/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=auth.obco.dev,resources=tokenroles/finalizers,verbs=update

// SetupTokenRole registers the TokenRole controller with the manager.
func SetupTokenRole(mgr ctrl.Manager, conns engine.Connections, opts engine.Options) error {
	r := engine.New[*authv1alpha1.TokenRole](
		mgr.GetClient(),
		mgr.GetScheme(),
		mgr.GetEventRecorder("tokenrole-controller"),
		conns,
		TokenRoleAdapter{},
		func() *authv1alpha1.TokenRole { return &authv1alpha1.TokenRole{} },
		opts,
	)
	return r.SetupWithManager(mgr)
}

// TokenRoleAdapter maps a TokenRole onto auth/token/roles/<name>.
type TokenRoleAdapter struct{}

var (
	_ engine.Adapter[*authv1alpha1.TokenRole]  = TokenRoleAdapter{}
	_ engine.DriftTier                         = TokenRoleAdapter{}
	_ engine.Observer[*authv1alpha1.TokenRole] = TokenRoleAdapter{}
)

// Path implements engine.Adapter.
func (TokenRoleAdapter) Path(t *authv1alpha1.TokenRole) string {
	return "auth/token/roles/" + authName(t.Spec.Name, t.Name)
}

// Desired implements engine.Adapter. Unset fields are sent as the OpenBao default, so removing one resets it. The
// token store does not accept token_type "default"; its own default is default-service.
func (TokenRoleAdapter) Desired(_ context.Context, _ engine.Resolver, t *authv1alpha1.TokenRole) (engine.Payload, error) {
	s := t.Spec
	p := engine.Payload{
		"allowed_policies":         builtinList(s.AllowedPolicies),
		"disallowed_policies":      builtinList(s.DisallowedPolicies),
		"allowed_policies_glob":    builtinList(s.AllowedPoliciesGlob),
		"disallowed_policies_glob": builtinList(s.DisallowedPoliciesGlob),
		"allowed_entity_aliases":   builtinList(s.AllowedEntityAliases),
		"orphan":                   builtinBool(s.Orphan),
		"renewable":                s.Renewable == nil || *s.Renewable,
		"path_suffix":              s.PathSuffix,
	}
	PutTokenBaseFields(p, s.TokenBaseFields)
	if p[keyTokenType] == nil || p[keyTokenType] == string(authv1alpha1.TokenTypeDefault) {
		p[keyTokenType] = "default-service"
	}
	putDefaults(p, engine.Payload{
		keyTokenExplicitMaxTTL:  int64(0),
		keyTokenPeriod:          int64(0),
		keyTokenBoundCIDRs:      []string{},
		keyTokenNumUses:         int64(0),
		keyTokenNoDefaultPolicy: false,
	})
	return p, nil
}

// Fields implements engine.Adapter.
func (TokenRoleAdapter) Fields() engine.FieldSpec {
	return MergeFieldSpecs(TokenBaseFieldSpec, engine.FieldSpec{
		Sets: []string{"allowed_policies", "disallowed_policies", "allowed_policies_glob", "disallowed_policies_glob",
			"allowed_entity_aliases"},
	})
}

// DriftTier implements engine.DriftTier.
func (TokenRoleAdapter) DriftTier() engine.Tier { return engine.TierAccessCritical }

// Observe implements engine.Observer. The read omits token_num_uses when it is 0.
func (a TokenRoleAdapter) Observe(ctx context.Context, c openbao.Client, t *authv1alpha1.TokenRole) (engine.Payload, bool, error) {
	return readWithDefaults(ctx, c, a.Path(t), engine.Payload{keyTokenNumUses: 0})
}
