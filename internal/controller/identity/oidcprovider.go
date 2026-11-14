package identity

import (
	"context"
	"errors"
	"strings"

	ctrl "sigs.k8s.io/controller-runtime"

	identityv1alpha1 "github.com/alexandrust88/openbao-config-operator/api/identity/v1alpha1"
	"github.com/alexandrust88/openbao-config-operator/internal/engine"
	"github.com/alexandrust88/openbao-config-operator/internal/openbao"
	"github.com/alexandrust88/openbao-config-operator/internal/refs"
)

// +kubebuilder:rbac:groups=identity.openbaoconfig.io,resources=oidcproviders,verbs=get;list;watch;update;patch
// +kubebuilder:rbac:groups=identity.openbaoconfig.io,resources=oidcproviders/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=identity.openbaoconfig.io,resources=oidcproviders/finalizers,verbs=update
// +kubebuilder:rbac:groups=identity.openbaoconfig.io,resources=oidcclients,verbs=get;list;watch

// SetupOIDCProvider registers the OIDCProvider controller with the manager.
func SetupOIDCProvider(mgr ctrl.Manager, conns engine.Connections, opts engine.Options) error {
	r := engine.New[*identityv1alpha1.OIDCProvider](
		mgr.GetClient(),
		mgr.GetScheme(),
		mgr.GetEventRecorder("oidcprovider-controller"),
		conns,
		OIDCProviderAdapter{Reader: mgr.GetClient(), Conns: conns},
		func() *identityv1alpha1.OIDCProvider { return &identityv1alpha1.OIDCProvider{} },
		opts,
	)
	return r.SetupWithManager(mgr)
}

// OIDCProviderAdapter maps an OIDCProvider onto identity/oidc/provider/<name>. Reader and Conns resolve the
// allowed client references.
type OIDCProviderAdapter struct {
	Reader refs.Reader
	Conns  engine.Connections
}

var (
	_ engine.Adapter[*identityv1alpha1.OIDCProvider]  = OIDCProviderAdapter{}
	_ engine.Observer[*identityv1alpha1.OIDCProvider] = OIDCProviderAdapter{}
	_ engine.Deleter[*identityv1alpha1.OIDCProvider]  = OIDCProviderAdapter{}
)

// Path implements engine.Adapter.
func (OIDCProviderAdapter) Path(p *identityv1alpha1.OIDCProvider) string {
	return "identity/oidc/provider/" + oidcName(p.Spec.Name, p.Name)
}

// Desired implements engine.Adapter.
func (a OIDCProviderAdapter) Desired(ctx context.Context, _ engine.Resolver, p *identityv1alpha1.OIDCProvider) (engine.Payload, error) {
	ids, err := resolveMatches(ctx, refs.For(a.Reader, a.Conns, p), oidcClientTarget, oidcClientMatchRefs(p.Spec.AllowedClients))
	if err != nil {
		return nil, err
	}
	return engine.Payload{"issuer": p.Spec.Issuer, "allowed_client_ids": ids,
		"scopes_supported": listOrEmpty(p.Spec.ScopesSupported)}, nil
}

// Fields implements engine.Adapter.
func (OIDCProviderAdapter) Fields() engine.FieldSpec {
	return engine.FieldSpec{Sets: []string{"allowed_client_ids", "scopes_supported"}}
}

// Observe implements engine.Observer. The read returns the effective issuer with "/v1/<namespace>identity/oidc/
// provider/<name>" appended; only the configured scheme, host and port are compared. Without spec.issuer the
// effective issuer derives from the server's api_addr and cannot be told apart from a configured one, so it is not
// compared; every write still sends "" to clear a configured issuer.
func (a OIDCProviderAdapter) Observe(ctx context.Context, c openbao.Client, p *identityv1alpha1.OIDCProvider) (engine.Payload, bool, error) {
	observed, err := c.Read(ctx, a.Path(p))
	if errors.Is(err, openbao.ErrNotFound) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	if issuer, ok := observed["issuer"].(string); ok {
		observed["issuer"], _, _ = strings.Cut(issuer, "/v1/")
	}
	if p.Spec.Issuer == "" {
		observed["issuer"] = ""
	}
	return observed, true, nil
}

// Delete implements engine.Deleter. The built-in default provider is never deleted.
func (a OIDCProviderAdapter) Delete(ctx context.Context, c openbao.Client, p *identityv1alpha1.OIDCProvider) error {
	if oidcName(p.Spec.Name, p.Name) == builtinOIDCName {
		return nil
	}
	return c.Delete(ctx, a.Path(p))
}
