package auth

import (
	"context"

	ctrl "sigs.k8s.io/controller-runtime"

	authv1alpha1 "github.com/alexandrust88/openbao-config-operator/api/auth/v1alpha1"
	commonv1alpha1 "github.com/alexandrust88/openbao-config-operator/api/common/v1alpha1"
	"github.com/alexandrust88/openbao-config-operator/internal/engine"
)

// +kubebuilder:rbac:groups=auth.openbaoconfig.io,resources=userpassusers,verbs=get;list;watch;update;patch
// +kubebuilder:rbac:groups=auth.openbaoconfig.io,resources=userpassusers/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=auth.openbaoconfig.io,resources=userpassusers/finalizers,verbs=update
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch

// SetupUserpassUser registers the UserpassUser controller with the manager.
func SetupUserpassUser(mgr ctrl.Manager, conns engine.Connections, opts engine.Options) error {
	r := engine.New[*authv1alpha1.UserpassUser](
		mgr.GetClient(),
		mgr.GetScheme(),
		mgr.GetEventRecorder("userpassuser-controller"),
		conns,
		UserpassUserAdapter{},
		func() *authv1alpha1.UserpassUser { return &authv1alpha1.UserpassUser{} },
		opts,
	)
	return r.SetupWithManager(mgr)
}

// UserpassUserAdapter maps a UserpassUser onto auth/<mount>/users/<name>. The password is write-only: OpenBao
// stores a bcrypt hash and never returns it, so only a change of the Secret causes a write.
type UserpassUserAdapter struct{}

var (
	_ engine.Adapter[*authv1alpha1.UserpassUser]          = UserpassUserAdapter{}
	_ engine.DriftTier                                    = UserpassUserAdapter{}
	_ engine.SecretReferencer[*authv1alpha1.UserpassUser] = UserpassUserAdapter{}
)

// Path implements engine.Adapter.
func (UserpassUserAdapter) Path(u *authv1alpha1.UserpassUser) string {
	return "auth/" + authMount(u.Spec.MountPath, "userpass") + "/users/" + authName(u.Spec.Name, u.Name)
}

// Desired implements engine.Adapter.
func (UserpassUserAdapter) Desired(ctx context.Context, r engine.Resolver, u *authv1alpha1.UserpassUser) (engine.Payload, error) {
	p := engine.Payload{}
	for key, ref := range map[string]*commonv1alpha1.SecretKeySelector{
		"password":      u.Spec.PasswordSecretRef,
		"password_hash": u.Spec.PasswordHashSecretRef,
	} {
		if ref == nil {
			continue
		}
		value, err := r.SecretValue(ctx, u.Namespace, *ref)
		if err != nil {
			return nil, err
		}
		p[key] = string(value)
	}
	if err := putTokenFields(ctx, r, u, p, u.Spec.TokenFields); err != nil {
		return nil, err
	}
	putTokenFieldDefaults(p)
	return p, nil
}

// Fields implements engine.Adapter.
func (UserpassUserAdapter) Fields() engine.FieldSpec {
	return MergeFieldSpecs(TokenFieldSpec, engine.FieldSpec{WriteOnly: []string{"password", "password_hash"}})
}

// DriftTier implements engine.DriftTier.
func (UserpassUserAdapter) DriftTier() engine.Tier { return engine.TierAccessCritical }

// SecretRefs implements engine.SecretReferencer.
func (UserpassUserAdapter) SecretRefs(u *authv1alpha1.UserpassUser) []string {
	var names []string
	for _, ref := range []*commonv1alpha1.SecretKeySelector{u.Spec.PasswordSecretRef, u.Spec.PasswordHashSecretRef} {
		if ref != nil {
			names = append(names, ref.Name)
		}
	}
	return names
}
