package secrets

import (
	"context"

	ctrl "sigs.k8s.io/controller-runtime"

	secretsv1alpha1 "github.com/alexandrust88/openbao-config-operator/api/secrets/v1alpha1"
	"github.com/alexandrust88/openbao-config-operator/internal/engine"
	"github.com/alexandrust88/openbao-config-operator/internal/openbao"
)

// +kubebuilder:rbac:groups=secrets.obco.dev,resources=gcpstaticaccounts,verbs=get;list;watch;update;patch
// +kubebuilder:rbac:groups=secrets.obco.dev,resources=gcpstaticaccounts/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=secrets.obco.dev,resources=gcpstaticaccounts/finalizers,verbs=update

// SetupGCPStaticAccount registers the GCPStaticAccount controller with the manager.
func SetupGCPStaticAccount(mgr ctrl.Manager, conns engine.Connections, opts engine.Options) error {
	return setupPlugin(mgr, conns, opts, "gcpstaticaccount", GCPStaticAccountAdapter{},
		func() *secretsv1alpha1.GCPStaticAccount { return &secretsv1alpha1.GCPStaticAccount{} })
}

// GCPStaticAccountAdapter maps a GCPStaticAccount onto <mountPath>/static-account/<name>.
type GCPStaticAccountAdapter struct{}

var (
	_ engine.Adapter[*secretsv1alpha1.GCPStaticAccount]  = GCPStaticAccountAdapter{}
	_ engine.Observer[*secretsv1alpha1.GCPStaticAccount] = GCPStaticAccountAdapter{}
	_ engine.Writer[*secretsv1alpha1.GCPStaticAccount]   = GCPStaticAccountAdapter{}
)

// Path implements engine.Adapter.
func (GCPStaticAccountAdapter) Path(a *secretsv1alpha1.GCPStaticAccount) string {
	return gcpSecretsMount(a.Spec.MountPath) + "/static-account/" + specName(a.Spec.Name, a.Name)
}

// Desired implements engine.Adapter.
func (GCPStaticAccountAdapter) Desired(_ context.Context, _ engine.Resolver, a *secretsv1alpha1.GCPStaticAccount) (engine.Payload, error) {
	s := a.Spec
	p := engine.Payload{"service_account_email": s.ServiceAccountEmail}
	optString(p, "secret_type", string(s.SecretType))
	if s.Bindings != nil {
		p["bindings"] = gcpBindingsHCL(s.Bindings)
	}
	putPluginList(p, "token_scopes", s.TokenScopes)
	return p, nil
}

// Fields implements engine.Adapter.
func (GCPStaticAccountAdapter) Fields() engine.FieldSpec {
	return engine.FieldSpec{Sets: []string{"token_scopes"}}
}

// Observe implements engine.Observer.
func (ad GCPStaticAccountAdapter) Observe(ctx context.Context, c openbao.Client, a *secretsv1alpha1.GCPStaticAccount) (engine.Payload, bool, error) {
	return observeGCPBindings(ctx, c, ad.Path(a), a.Spec.Bindings)
}

// Write implements engine.Writer.
func (ad GCPStaticAccountAdapter) Write(ctx context.Context, c openbao.Client, a *secretsv1alpha1.GCPStaticAccount,
	desired engine.Payload, _ bool) (engine.Payload, error) {
	return writeCloud(ctx, c, ad.Path(a), desired)
}
