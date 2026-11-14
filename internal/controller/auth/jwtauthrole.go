package auth

import (
	"context"

	ctrl "sigs.k8s.io/controller-runtime"

	authv1alpha1 "github.com/alexandrust88/openbao-config-operator/api/auth/v1alpha1"
	"github.com/alexandrust88/openbao-config-operator/internal/engine"
	"github.com/alexandrust88/openbao-config-operator/internal/openbao"
)

// +kubebuilder:rbac:groups=auth.openbaoconfig.io,resources=jwtauthroles,verbs=get;list;watch;update;patch
// +kubebuilder:rbac:groups=auth.openbaoconfig.io,resources=jwtauthroles/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=auth.openbaoconfig.io,resources=jwtauthroles/finalizers,verbs=update

// SetupJWTAuthRole registers the JWTAuthRole controller with the manager.
func SetupJWTAuthRole(mgr ctrl.Manager, conns engine.Connections, opts engine.Options) error {
	r := engine.New[*authv1alpha1.JWTAuthRole](
		mgr.GetClient(),
		mgr.GetScheme(),
		mgr.GetEventRecorder("jwtauthrole-controller"),
		conns,
		JWTAuthRoleAdapter{},
		func() *authv1alpha1.JWTAuthRole { return &authv1alpha1.JWTAuthRole{} },
		opts,
	)
	return r.SetupWithManager(mgr)
}

// JWTAuthRoleAdapter maps a JWTAuthRole onto auth/<mount>/role/<name>.
type JWTAuthRoleAdapter struct{}

var (
	_ engine.Adapter[*authv1alpha1.JWTAuthRole]  = JWTAuthRoleAdapter{}
	_ engine.DriftTier                           = JWTAuthRoleAdapter{}
	_ engine.Observer[*authv1alpha1.JWTAuthRole] = JWTAuthRoleAdapter{}
)

// jwtRoleLeewayDefaults are the leeways a JWT role stores when they are unset; 0 means the built-in default.
var jwtRoleLeewayDefaults = map[string]int64{keyExpirationLeeway: 0, keyNotBeforeLeeway: 0, keyClockSkewLeeway: 0}

// Path implements engine.Adapter.
func (JWTAuthRoleAdapter) Path(j *authv1alpha1.JWTAuthRole) string {
	return "auth/" + authMount(j.Spec.MountPath, "jwt") + "/role/" + authName(j.Spec.Name, j.Name)
}

// Desired implements engine.Adapter. Unset fields are sent as the OpenBao default, so removing one resets it.
func (JWTAuthRoleAdapter) Desired(ctx context.Context, r engine.Resolver, j *authv1alpha1.JWTAuthRole) (engine.Payload, error) {
	s := j.Spec
	p := engine.Payload{
		"role_type":                      string(orDefault(s.RoleType, authv1alpha1.JWTRoleTypeJWT)),
		"user_claim":                     s.UserClaim,
		"user_claim_json_pointer":        builtinBool(s.UserClaimJSONPointer),
		keyBoundAudiences:                builtinList(s.BoundAudiences),
		"bound_subject":                  s.BoundSubject,
		"bound_claims_type":              string(orDefault(s.BoundClaimsType, "string")),
		"bound_claims":                   orEmpty(s.BoundClaims),
		"claim_mappings":                 orEmpty(s.ClaimMappings),
		"groups_claim":                   s.GroupsClaim,
		"oidc_scopes":                    builtinList(s.OIDCScopes),
		"allowed_redirect_uris":          builtinList(s.AllowedRedirectURIs),
		"callback_mode":                  string(orDefault(s.CallbackMode, "client")),
		"poll_interval":                  int64(0),
		"max_age":                        int64(0),
		"oauth2_metadata":                builtinList(s.OAuth2Metadata),
		"verbose_oidc_logging":           builtinBool(s.VerboseOIDCLogging),
		"token_policies_template_claims": builtinBool(s.TokenPoliciesTemplateClaims),
		"oidc_disable_confirmation":      builtinBool(s.OIDCDisableConfirmation),
	}
	if s.PollInterval != nil {
		p["poll_interval"] = int64(*s.PollInterval)
	}
	putSeconds(p, "max_age", s.MaxAge)
	if err := putLeeways(p, s.JWTLeeways, jwtRoleLeewayDefaults); err != nil {
		return nil, err
	}
	if err := putTokenFields(ctx, r, j, p, s.TokenFields); err != nil {
		return nil, err
	}
	putTokenFieldDefaults(p)
	return p, nil
}

// Fields implements engine.Adapter. Leeways are signed seconds on both sides, so they are plain numbers.
func (JWTAuthRoleAdapter) Fields() engine.FieldSpec {
	return MergeFieldSpecs(engine.FieldSpec{
		Sets:      []string{keyBoundAudiences, "oidc_scopes", "allowed_redirect_uris", "oauth2_metadata"},
		Durations: []string{"max_age"},
	}, TokenFieldSpec)
}

// DriftTier implements engine.DriftTier.
func (JWTAuthRoleAdapter) DriftTier() engine.Tier { return engine.TierAccessCritical }

// Observe implements engine.Observer. The read omits poll_interval when it is 0.
func (a JWTAuthRoleAdapter) Observe(ctx context.Context, c openbao.Client, j *authv1alpha1.JWTAuthRole) (engine.Payload, bool, error) {
	return readWithDefaults(ctx, c, a.Path(j), engine.Payload{"poll_interval": 0})
}

func orEmpty[V any](m map[string]V) map[string]V {
	if m == nil {
		return map[string]V{}
	}
	return m
}

func orDefault[S ~string](v, fallback S) S {
	if v == "" {
		return fallback
	}
	return v
}
