package secrets

import (
	"context"

	ctrl "sigs.k8s.io/controller-runtime"

	secretsv1alpha1 "github.com/alexandrust88/openbao-config-operator/api/secrets/v1alpha1"
	"github.com/alexandrust88/openbao-config-operator/internal/engine"
)

// +kubebuilder:rbac:groups=secrets.openbaoconfig.io,resources=pkiautotidies,verbs=get;list;watch;update;patch
// +kubebuilder:rbac:groups=secrets.openbaoconfig.io,resources=pkiautotidies/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=secrets.openbaoconfig.io,resources=pkiautotidies/finalizers,verbs=update

// SetupPKIAutoTidy registers the PKIAutoTidy controller with the manager.
func SetupPKIAutoTidy(mgr ctrl.Manager, conns engine.Connections, opts engine.Options) error {
	r := engine.New[*secretsv1alpha1.PKIAutoTidy](
		mgr.GetClient(),
		mgr.GetScheme(),
		mgr.GetEventRecorder("pkiautotidy-controller"),
		conns,
		PKIAutoTidyAdapter{},
		func() *secretsv1alpha1.PKIAutoTidy { return &secretsv1alpha1.PKIAutoTidy{} },
		opts,
	)
	return r.SetupWithManager(mgr)
}

// PKIAutoTidyAdapter maps a PKIAutoTidy onto <mount>/config/auto-tidy.
type PKIAutoTidyAdapter struct{}

var (
	_ engine.Adapter[*secretsv1alpha1.PKIAutoTidy] = PKIAutoTidyAdapter{}
	_ engine.Singleton                             = PKIAutoTidyAdapter{}
)

// Path implements engine.Adapter.
func (PKIAutoTidyAdapter) Path(t *secretsv1alpha1.PKIAutoTidy) string {
	return pkiMount(t.Spec.MountPath) + "/config/auto-tidy"
}

// Desired implements engine.Adapter. Every setting is sent, an unset one at its OpenBao default; an unset
// revoked_safety_buffer follows safety_buffer, as OpenBao does for a config that never set it.
func (PKIAutoTidyAdapter) Desired(_ context.Context, _ engine.Resolver, t *secretsv1alpha1.PKIAutoTidy) (engine.Payload, error) {
	s := &t.Spec
	p := engine.Payload{}
	orFalse(p, "enabled", s.Enabled)
	orSeconds(p, "interval_duration", s.IntervalDuration, 12*3600)
	orFalse(p, "tidy_cert_store", s.TidyCertStore)
	orFalse(p, "tidy_revoked_certs", s.TidyRevokedCerts)
	orFalse(p, "tidy_invalid_certs", s.TidyInvalidCerts)
	orFalse(p, "tidy_revoked_cert_issuer_associations", s.TidyRevokedCertIssuerAssociations)
	orFalse(p, "tidy_expired_issuers", s.TidyExpiredIssuers)
	orFalse(p, "tidy_move_legacy_ca_bundle", s.TidyMoveLegacyCABundle)
	orFalse(p, "tidy_acme", s.TidyACME)
	orSeconds(p, "safety_buffer", s.SafetyBuffer, 72*3600)
	orSeconds(p, "revoked_safety_buffer", s.RevokedSafetyBuffer, p["safety_buffer"].(int64))
	orSeconds(p, "issuer_safety_buffer", s.IssuerSafetyBuffer, 365*24*3600)
	orSeconds(p, "acme_account_safety_buffer", s.ACMEAccountSafetyBuffer, 30*24*3600)
	orInt32(p, "page_size", s.PageSize, 1000)
	p["pause_duration"] = "0s"
	pkiOptDurationString(p, "pause_duration", s.PauseDuration)
	orFalse(p, "maintain_stored_certificate_counts", s.MaintainStoredCertificateCounts)
	orFalse(p, "publish_stored_certificate_count_metrics", s.PublishStoredCertificateCountMetrics)
	return p, nil
}

// Fields implements engine.Adapter.
func (PKIAutoTidyAdapter) Fields() engine.FieldSpec {
	return engine.FieldSpec{Durations: []string{
		"interval_duration", "safety_buffer", "revoked_safety_buffer", "issuer_safety_buffer",
		"acme_account_safety_buffer", "pause_duration",
	}}
}

// Singleton implements engine.Singleton: the setting always exists, so it is adopted whatever the creationPolicy.
func (PKIAutoTidyAdapter) Singleton() bool { return true }
