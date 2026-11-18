package auth

import (
	"context"

	ctrl "sigs.k8s.io/controller-runtime"

	authv1alpha1 "github.com/alexandrust88/openbao-config-operator/api/auth/v1alpha1"
	"github.com/alexandrust88/openbao-config-operator/internal/engine"
	"github.com/alexandrust88/openbao-config-operator/internal/openbao"
)

// +kubebuilder:rbac:groups=auth.obco.dev,resources=kerberosauthgroups,verbs=get;list;watch;update;patch
// +kubebuilder:rbac:groups=auth.obco.dev,resources=kerberosauthgroups/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=auth.obco.dev,resources=kerberosauthgroups/finalizers,verbs=update

// SetupKerberosAuthGroup registers the KerberosAuthGroup controller with the manager.
func SetupKerberosAuthGroup(mgr ctrl.Manager, conns engine.Connections, opts engine.Options) error {
	return setupPluginKind(mgr, conns, opts, "kerberosauthgroup-controller", KerberosAuthGroupAdapter{},
		func() *authv1alpha1.KerberosAuthGroup { return &authv1alpha1.KerberosAuthGroup{} })
}

// KerberosAuthGroupAdapter maps a KerberosAuthGroup onto auth/<mountPath>/groups/<name> of a Kerberos auth method.
type KerberosAuthGroupAdapter struct{}

var (
	_ engine.Adapter[*authv1alpha1.KerberosAuthGroup]  = KerberosAuthGroupAdapter{}
	_ engine.DriftTier                                 = KerberosAuthGroupAdapter{}
	_ engine.Observer[*authv1alpha1.KerberosAuthGroup] = KerberosAuthGroupAdapter{}
)

// Path implements engine.Adapter.
func (KerberosAuthGroupAdapter) Path(o *authv1alpha1.KerberosAuthGroup) string {
	return "auth/" + authMount(o.Spec.MountPath, "kerberos") + "/groups/" + authName(o.Spec.Name, o.Name)
}

// Desired implements engine.Adapter.
func (KerberosAuthGroupAdapter) Desired(_ context.Context, _ engine.Resolver, o *authv1alpha1.KerberosAuthGroup) (engine.Payload, error) {
	p := engine.Payload{}
	putList(p, pluginPoliciesKey, o.Spec.Policies)
	return p, nil
}

// Fields implements engine.Adapter.
func (KerberosAuthGroupAdapter) Fields() engine.FieldSpec {
	return engine.FieldSpec{Sets: []string{pluginPoliciesKey}}
}

// DriftTier implements engine.DriftTier.
func (KerberosAuthGroupAdapter) DriftTier() engine.Tier { return engine.TierAccessCritical }

// Observe implements engine.Observer. When the object is absent it checks that the auth method is enabled.
func (a KerberosAuthGroupAdapter) Observe(ctx context.Context, c openbao.Client, o *authv1alpha1.KerberosAuthGroup) (engine.Payload, bool, error) {
	return observeExternalAuth(ctx, c, a.Path(o), authMount(o.Spec.MountPath, "kerberos"), "kerberos")
}
