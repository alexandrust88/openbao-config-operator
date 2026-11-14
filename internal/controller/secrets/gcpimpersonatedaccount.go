package secrets

import (
	"context"

	ctrl "sigs.k8s.io/controller-runtime"

	secretsv1alpha1 "github.com/alexandrust88/openbao-config-operator/api/secrets/v1alpha1"
	"github.com/alexandrust88/openbao-config-operator/internal/engine"
	"github.com/alexandrust88/openbao-config-operator/internal/openbao"
)

// +kubebuilder:rbac:groups=secrets.openbaoconfig.io,resources=gcpimpersonatedaccounts,verbs=get;list;watch;update;patch
// +kubebuilder:rbac:groups=secrets.openbaoconfig.io,resources=gcpimpersonatedaccounts/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=secrets.openbaoconfig.io,resources=gcpimpersonatedaccounts/finalizers,verbs=update

// SetupGCPImpersonatedAccount registers the GCPImpersonatedAccount controller with the manager.
func SetupGCPImpersonatedAccount(mgr ctrl.Manager, conns engine.Connections, opts engine.Options) error {
	return setupPlugin(mgr, conns, opts, "gcpimpersonatedaccount", GCPImpersonatedAccountAdapter{},
		func() *secretsv1alpha1.GCPImpersonatedAccount { return &secretsv1alpha1.GCPImpersonatedAccount{} })
}

// GCPImpersonatedAccountAdapter maps a GCPImpersonatedAccount onto <mountPath>/impersonated-account/<name>.
type GCPImpersonatedAccountAdapter struct{}

var (
	_ engine.Adapter[*secretsv1alpha1.GCPImpersonatedAccount] = GCPImpersonatedAccountAdapter{}
	_ engine.Writer[*secretsv1alpha1.GCPImpersonatedAccount]  = GCPImpersonatedAccountAdapter{}
)

// Path implements engine.Adapter.
func (GCPImpersonatedAccountAdapter) Path(a *secretsv1alpha1.GCPImpersonatedAccount) string {
	return gcpSecretsMount(a.Spec.MountPath) + "/impersonated-account/" + specName(a.Spec.Name, a.Name)
}

// Desired implements engine.Adapter.
func (GCPImpersonatedAccountAdapter) Desired(_ context.Context, _ engine.Resolver, a *secretsv1alpha1.GCPImpersonatedAccount) (engine.Payload, error) {
	p := engine.Payload{"service_account_email": a.Spec.ServiceAccountEmail, "token_scopes": a.Spec.TokenScopes}
	optSeconds(p, "ttl", a.Spec.TTL)
	return p, nil
}

// Fields implements engine.Adapter.
func (GCPImpersonatedAccountAdapter) Fields() engine.FieldSpec {
	return engine.FieldSpec{
		Sets:      []string{"token_scopes"},
		Durations: []string{"ttl"},
	}
}

// Write implements engine.Writer.
func (ad GCPImpersonatedAccountAdapter) Write(ctx context.Context, c openbao.Client, a *secretsv1alpha1.GCPImpersonatedAccount,
	desired engine.Payload, _ bool) (engine.Payload, error) {
	return writeCloud(ctx, c, ad.Path(a), desired)
}
