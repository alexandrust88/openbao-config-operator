package auth

import (
	"context"

	ctrl "sigs.k8s.io/controller-runtime"

	authv1alpha1 "github.com/alexandrust88/openbao-config-operator/api/auth/v1alpha1"
	"github.com/alexandrust88/openbao-config-operator/internal/engine"
)

// +kubebuilder:rbac:groups=auth.openbaoconfig.io,resources=jwtcelroles,verbs=get;list;watch;update;patch
// +kubebuilder:rbac:groups=auth.openbaoconfig.io,resources=jwtcelroles/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=auth.openbaoconfig.io,resources=jwtcelroles/finalizers,verbs=update

// SetupJWTCELRole registers the JWTCELRole controller with the manager.
func SetupJWTCELRole(mgr ctrl.Manager, conns engine.Connections, opts engine.Options) error {
	r := engine.New[*authv1alpha1.JWTCELRole](
		mgr.GetClient(),
		mgr.GetScheme(),
		mgr.GetEventRecorder("jwtcelrole-controller"),
		conns,
		JWTCELRoleAdapter{},
		func() *authv1alpha1.JWTCELRole { return &authv1alpha1.JWTCELRole{} },
		opts,
	)
	return r.SetupWithManager(mgr)
}

// celRoleLeewayDefaults are the values OpenBao stores when a CEL role write omits a leeway
// (internal/builtin/credential/jwt/path_cel_role.go).
var celRoleLeewayDefaults = map[string]int64{
	keyExpirationLeeway: 150,
	keyNotBeforeLeeway:  150,
	keyClockSkewLeeway:  60,
}

// JWTCELRoleAdapter maps a JWTCELRole onto auth/<mount>/cel/role/<name>.
type JWTCELRoleAdapter struct{}

var (
	_ engine.Adapter[*authv1alpha1.JWTCELRole] = JWTCELRoleAdapter{}
	_ engine.DriftTier                         = JWTCELRoleAdapter{}
)

// Path implements engine.Adapter.
func (JWTCELRoleAdapter) Path(j *authv1alpha1.JWTCELRole) string {
	return "auth/" + authMount(j.Spec.MountPath, "jwt") + "/cel/role/" + authName(j.Spec.Name, j.Name)
}

// Desired implements engine.Adapter. The write replaces the whole role, so every field is sent, unset ones as
// the value OpenBao would store for them.
func (JWTCELRoleAdapter) Desired(_ context.Context, _ engine.Resolver, j *authv1alpha1.JWTCELRole) (engine.Payload, error) {
	s := j.Spec
	variables := make([]any, len(s.CELProgram.Variables))
	for i, v := range s.CELProgram.Variables {
		variables[i] = map[string]any{"name": v.Name, "expression": v.Expression}
	}
	p := engine.Payload{
		"cel_program":     map[string]any{"expression": s.CELProgram.Expression, "variables": variables},
		"message":         s.Message,
		keyBoundAudiences: builtinList(s.BoundAudiences),
	}
	if err := putLeeways(p, s.JWTLeeways, celRoleLeewayDefaults); err != nil {
		return nil, err
	}
	return p, nil
}

// Fields implements engine.Adapter.
func (JWTCELRoleAdapter) Fields() engine.FieldSpec {
	return engine.FieldSpec{Sets: []string{keyBoundAudiences}}
}

// DriftTier implements engine.DriftTier.
func (JWTCELRoleAdapter) DriftTier() engine.Tier { return engine.TierAccessCritical }
