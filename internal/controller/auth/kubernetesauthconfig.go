package auth

import (
	"context"

	ctrl "sigs.k8s.io/controller-runtime"

	authv1alpha1 "github.com/alexandrust88/openbao-config-operator/api/auth/v1alpha1"
	"github.com/alexandrust88/openbao-config-operator/internal/engine"
	"github.com/alexandrust88/openbao-config-operator/internal/openbao"
)

// +kubebuilder:rbac:groups=auth.obco.dev,resources=kubernetesauthconfigs,verbs=get;list;watch;update;patch
// +kubebuilder:rbac:groups=auth.obco.dev,resources=kubernetesauthconfigs/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=auth.obco.dev,resources=kubernetesauthconfigs/finalizers,verbs=update
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch

// SetupKubernetesAuthConfig registers the KubernetesAuthConfig controller with the manager.
func SetupKubernetesAuthConfig(mgr ctrl.Manager, conns engine.Connections, opts engine.Options) error {
	r := engine.New[*authv1alpha1.KubernetesAuthConfig](
		mgr.GetClient(),
		mgr.GetScheme(),
		mgr.GetEventRecorder("kubernetesauthconfig-controller"),
		conns,
		KubernetesAuthConfigAdapter{},
		func() *authv1alpha1.KubernetesAuthConfig { return &authv1alpha1.KubernetesAuthConfig{} },
		opts,
	)
	return r.SetupWithManager(mgr)
}

// tokenReviewerJWTSet is compared against the read but never written: it detects a token reviewer JWT that was
// cleared or set out of band, which the write-only hash cannot see.
const tokenReviewerJWTSet = "token_reviewer_jwt_set"

// KubernetesAuthConfigAdapter maps a KubernetesAuthConfig onto auth/<mount>/config.
type KubernetesAuthConfigAdapter struct{}

var (
	_ engine.Adapter[*authv1alpha1.KubernetesAuthConfig]          = KubernetesAuthConfigAdapter{}
	_ engine.DriftTier                                            = KubernetesAuthConfigAdapter{}
	_ engine.Writer[*authv1alpha1.KubernetesAuthConfig]           = KubernetesAuthConfigAdapter{}
	_ engine.SecretReferencer[*authv1alpha1.KubernetesAuthConfig] = KubernetesAuthConfigAdapter{}
)

// Path implements engine.Adapter.
func (KubernetesAuthConfigAdapter) Path(k *authv1alpha1.KubernetesAuthConfig) string {
	return "auth/" + authMount(k.Spec.MountPath, "kubernetes") + "/config"
}

// Desired implements engine.Adapter. The write replaces the whole configuration, so every field is sent,
// unset ones as their zero value, and the token reviewer JWT is part of every write.
func (KubernetesAuthConfigAdapter) Desired(ctx context.Context, r engine.Resolver, k *authv1alpha1.KubernetesAuthConfig) (engine.Payload, error) {
	s := k.Spec
	p := engine.Payload{
		"kubernetes_host":      s.KubernetesHost,
		"kubernetes_ca_cert":   s.KubernetesCACert,
		"pem_keys":             builtinList(s.PEMKeys),
		"disable_local_ca_jwt": builtinBool(s.DisableLocalCAJWT),
		tokenReviewerJWTSet:    s.TokenReviewerJWTSecretRef != nil,
	}
	if s.TokenReviewerJWTSecretRef != nil {
		jwt, err := authSecret(ctx, r, k.Namespace, *s.TokenReviewerJWTSecretRef)
		if err != nil {
			return nil, err
		}
		p["token_reviewer_jwt"] = jwt
	}
	return p, nil
}

// Fields implements engine.Adapter.
func (KubernetesAuthConfigAdapter) Fields() engine.FieldSpec {
	return engine.FieldSpec{WriteOnly: []string{"token_reviewer_jwt"}}
}

// DriftTier implements engine.DriftTier.
func (KubernetesAuthConfigAdapter) DriftTier() engine.Tier { return engine.TierAccessCritical }

// Write implements engine.Writer.
func (a KubernetesAuthConfigAdapter) Write(ctx context.Context, c openbao.Client, k *authv1alpha1.KubernetesAuthConfig,
	desired engine.Payload, _ bool) (engine.Payload, error) {
	return writeWithout(ctx, c, a.Path(k), desired, tokenReviewerJWTSet)
}

// SecretRefs implements engine.SecretReferencer.
func (KubernetesAuthConfigAdapter) SecretRefs(k *authv1alpha1.KubernetesAuthConfig) []string {
	if k.Spec.TokenReviewerJWTSecretRef == nil {
		return nil
	}
	return []string{k.Spec.TokenReviewerJWTSecretRef.Name}
}
