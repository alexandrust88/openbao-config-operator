package auth

import (
	"context"
	"strings"

	ctrl "sigs.k8s.io/controller-runtime"

	authv1alpha1 "github.com/alexandrust88/openbao-config-operator/api/auth/v1alpha1"
	"github.com/alexandrust88/openbao-config-operator/internal/engine"
	"github.com/alexandrust88/openbao-config-operator/internal/openbao"
)

// +kubebuilder:rbac:groups=auth.openbaoconfig.io,resources=awsauthcertificates,verbs=get;list;watch;update;patch
// +kubebuilder:rbac:groups=auth.openbaoconfig.io,resources=awsauthcertificates/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=auth.openbaoconfig.io,resources=awsauthcertificates/finalizers,verbs=update

// SetupAWSAuthCertificate registers the AWSAuthCertificate controller with the manager.
func SetupAWSAuthCertificate(mgr ctrl.Manager, conns engine.Connections, opts engine.Options) error {
	return setupPluginKind(mgr, conns, opts, "awsauthcertificate-controller", AWSAuthCertificateAdapter{},
		func() *authv1alpha1.AWSAuthCertificate { return &authv1alpha1.AWSAuthCertificate{} })
}

// AWSAuthCertificateAdapter maps an AWSAuthCertificate onto auth/<mountPath>/config/certificate/<name>.
type AWSAuthCertificateAdapter struct{}

var (
	_ engine.Adapter[*authv1alpha1.AWSAuthCertificate]  = AWSAuthCertificateAdapter{}
	_ engine.DriftTier                                  = AWSAuthCertificateAdapter{}
	_ engine.Observer[*authv1alpha1.AWSAuthCertificate] = AWSAuthCertificateAdapter{}
)

// Path implements engine.Adapter.
func (AWSAuthCertificateAdapter) Path(o *authv1alpha1.AWSAuthCertificate) string {
	return "auth/" + authMount(o.Spec.MountPath, "aws") + "/config/certificate/" + authName(o.Spec.Name, o.Name)
}

// Desired implements engine.Adapter.
func (AWSAuthCertificateAdapter) Desired(_ context.Context, _ engine.Resolver, o *authv1alpha1.AWSAuthCertificate) (engine.Payload, error) {
	p := engine.Payload{"aws_public_cert": o.Spec.AWSPublicCert}
	putString(p, "type", o.Spec.Type)
	return p, nil
}

// Fields implements engine.Adapter.
func (AWSAuthCertificateAdapter) Fields() engine.FieldSpec { return engine.FieldSpec{} }

// DriftTier implements engine.DriftTier.
func (AWSAuthCertificateAdapter) DriftTier() engine.Tier { return engine.TierAccessCritical }

// Observe implements engine.Observer. A stored certificate that differs from the spec only in surrounding
// whitespace is reported as the spec text; a missing auth method is reported as ErrAuthMountMissing.
func (a AWSAuthCertificateAdapter) Observe(ctx context.Context, c openbao.Client, o *authv1alpha1.AWSAuthCertificate) (engine.Payload, bool, error) {
	observed, found, err := observeExternalAuth(ctx, c, a.Path(o), authMount(o.Spec.MountPath, "aws"), "aws")
	if !found || err != nil {
		return observed, found, err
	}
	if stored, ok := observed["aws_public_cert"].(string); ok && strings.TrimSpace(stored) == strings.TrimSpace(o.Spec.AWSPublicCert) {
		observed["aws_public_cert"] = o.Spec.AWSPublicCert
	}
	return observed, true, nil
}
