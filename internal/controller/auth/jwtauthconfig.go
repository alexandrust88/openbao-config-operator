package auth

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"

	ctrl "sigs.k8s.io/controller-runtime"

	authv1alpha1 "github.com/alexandrust88/openbao-config-operator/api/auth/v1alpha1"
	commonv1alpha1 "github.com/alexandrust88/openbao-config-operator/api/common/v1alpha1"
	"github.com/alexandrust88/openbao-config-operator/internal/engine"
)

// +kubebuilder:rbac:groups=auth.openbaoconfig.io,resources=jwtauthconfigs,verbs=get;list;watch;update;patch
// +kubebuilder:rbac:groups=auth.openbaoconfig.io,resources=jwtauthconfigs/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=auth.openbaoconfig.io,resources=jwtauthconfigs/finalizers,verbs=update
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch

// SetupJWTAuthConfig registers the JWTAuthConfig controller with the manager.
func SetupJWTAuthConfig(mgr ctrl.Manager, conns engine.Connections, opts engine.Options) error {
	r := engine.New[*authv1alpha1.JWTAuthConfig](
		mgr.GetClient(),
		mgr.GetScheme(),
		mgr.GetEventRecorder("jwtauthconfig-controller"),
		conns,
		JWTAuthConfigAdapter{},
		func() *authv1alpha1.JWTAuthConfig { return &authv1alpha1.JWTAuthConfig{} },
		opts,
	)
	return r.SetupWithManager(mgr)
}

// JWTAuthConfigAdapter maps a JWTAuthConfig onto auth/<mount>/config.
type JWTAuthConfigAdapter struct{}

var (
	_ engine.Adapter[*authv1alpha1.JWTAuthConfig]          = JWTAuthConfigAdapter{}
	_ engine.DriftTier                                     = JWTAuthConfigAdapter{}
	_ engine.FieldsProvider[*authv1alpha1.JWTAuthConfig]   = JWTAuthConfigAdapter{}
	_ engine.SecretReferencer[*authv1alpha1.JWTAuthConfig] = JWTAuthConfigAdapter{}
)

// Path implements engine.Adapter.
func (JWTAuthConfigAdapter) Path(j *authv1alpha1.JWTAuthConfig) string {
	return "auth/" + authMount(j.Spec.MountPath, "jwt") + "/config"
}

// Desired implements engine.Adapter. The write replaces the whole configuration, so every stored field is sent,
// unset ones as their zero value, and the client secret is part of every write.
func (JWTAuthConfigAdapter) Desired(ctx context.Context, r engine.Resolver, j *authv1alpha1.JWTAuthConfig) (engine.Payload, error) {
	s := j.Spec
	providerConfig, err := jwtProviderConfig(ctx, r, j.Namespace, s.ProviderConfig)
	if err != nil {
		return nil, err
	}
	p := engine.Payload{
		"oidc_discovery_url":            s.OIDCDiscoveryURL,
		"oidc_discovery_ca_pem":         s.OIDCDiscoveryCAPEM,
		"oidc_client_id":                s.OIDCClientID,
		"oidc_response_mode":            string(s.OIDCResponseMode),
		"oidc_response_types":           builtinList(s.OIDCResponseTypes),
		"jwks_url":                      s.JWKSURL,
		"jwks_ca_pem":                   s.JWKSCAPEM,
		"jwt_validation_pubkeys":        builtinList(s.JWTValidationPubkeys),
		"jwt_supported_algs":            builtinList(s.JWTSupportedAlgs),
		"bound_issuer":                  s.BoundIssuer,
		"default_role":                  s.DefaultRole,
		"provider_config":               providerConfig,
		"override_allowed_server_names": builtinList(s.OverrideAllowedServerNames),
	}
	if s.OIDCClientSecretSecretRef != nil {
		secret, err := authSecret(ctx, r, j.Namespace, *s.OIDCClientSecretSecretRef)
		if err != nil {
			return nil, err
		}
		p["oidc_client_secret"] = secret
	}
	p["namespace_in_state"] = s.NamespaceInState == nil || *s.NamespaceInState
	if s.SkipJWKSValidation != nil {
		p["skip_jwks_validation"] = *s.SkipJWKSValidation
	}
	return p, nil
}

// Fields implements engine.Adapter. skip_jwks_validation is not stored, so it is hashed like a secret.
func (JWTAuthConfigAdapter) Fields() engine.FieldSpec {
	return engine.FieldSpec{
		WriteOnly: []string{"oidc_client_secret", "skip_jwks_validation"},
		Sets:      []string{"oidc_response_types", "jwt_validation_pubkeys", "jwt_supported_algs", "override_allowed_server_names"},
	}
}

// FieldsFor implements engine.FieldsProvider. OpenBao strips sensitive provider keys from the read, so a
// provider configuration with Secret input is tracked through the write-only hash.
func (JWTAuthConfigAdapter) FieldsFor(_ context.Context, _ engine.Resolver, j *authv1alpha1.JWTAuthConfig) (engine.FieldSpec, error) {
	if j.Spec.ProviderConfig == nil || j.Spec.ProviderConfig.SecretRef == nil {
		return engine.FieldSpec{}, nil
	}
	return engine.FieldSpec{WriteOnly: []string{"provider_config"}}, nil
}

// DriftTier implements engine.DriftTier.
func (JWTAuthConfigAdapter) DriftTier() engine.Tier { return engine.TierAccessCritical }

// SecretRefs implements engine.SecretReferencer.
func (JWTAuthConfigAdapter) SecretRefs(j *authv1alpha1.JWTAuthConfig) []string {
	refs := []*commonv1alpha1.SecretKeySelector{j.Spec.OIDCClientSecretSecretRef}
	if j.Spec.ProviderConfig != nil {
		refs = append(refs, j.Spec.ProviderConfig.SecretRef)
	}
	var names []string
	for _, ref := range refs {
		if ref != nil {
			names = append(names, ref.Name)
		}
	}
	return names
}

// jwtProviderConfig builds provider_config from the provider name, the settings and the Secret keys merged over them.
func jwtProviderConfig(ctx context.Context, r engine.Resolver, namespace string, pc *authv1alpha1.JWTProviderConfig) (map[string]any, error) {
	out := map[string]any{}
	if pc == nil {
		return out, nil
	}
	for k, v := range pc.Settings {
		var value any
		if err := json.Unmarshal(v.Raw, &value); err != nil {
			return nil, fmt.Errorf("providerConfig.settings.%s: %w", k, err)
		}
		out[k] = value
	}
	if pc.SecretRef != nil {
		raw, err := r.SecretValue(ctx, namespace, *pc.SecretRef)
		if err != nil {
			return nil, err
		}
		var sensitive map[string]any
		if err := json.Unmarshal(raw, &sensitive); err != nil {
			return nil, fmt.Errorf("secret %s key %s is not a JSON object", pc.SecretRef.Name, pc.SecretRef.Key)
		}
		maps.Copy(out, sensitive)
	}
	out["provider"] = string(pc.Provider)
	return out, nil
}
