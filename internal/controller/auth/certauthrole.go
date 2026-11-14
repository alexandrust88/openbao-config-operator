package auth

import (
	"context"

	ctrl "sigs.k8s.io/controller-runtime"

	authv1alpha1 "github.com/alexandrust88/openbao-config-operator/api/auth/v1alpha1"
	"github.com/alexandrust88/openbao-config-operator/internal/engine"
)

// +kubebuilder:rbac:groups=auth.openbaoconfig.io,resources=certauthroles,verbs=get;list;watch;update;patch
// +kubebuilder:rbac:groups=auth.openbaoconfig.io,resources=certauthroles/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=auth.openbaoconfig.io,resources=certauthroles/finalizers,verbs=update

// SetupCertAuthRole registers the CertAuthRole controller with the manager.
func SetupCertAuthRole(mgr ctrl.Manager, conns engine.Connections, opts engine.Options) error {
	r := engine.New[*authv1alpha1.CertAuthRole](
		mgr.GetClient(),
		mgr.GetScheme(),
		mgr.GetEventRecorder("certauthrole-controller"),
		conns,
		CertAuthRoleAdapter{},
		func() *authv1alpha1.CertAuthRole { return &authv1alpha1.CertAuthRole{} },
		opts,
	)
	return r.SetupWithManager(mgr)
}

// CertAuthRoleAdapter maps a CertAuthRole onto auth/<mount>/certs/<name>.
type CertAuthRoleAdapter struct{}

var (
	_ engine.Adapter[*authv1alpha1.CertAuthRole] = CertAuthRoleAdapter{}
	_ engine.DriftTier                           = CertAuthRoleAdapter{}
)

// Path implements engine.Adapter.
func (CertAuthRoleAdapter) Path(c *authv1alpha1.CertAuthRole) string {
	return "auth/" + authMount(c.Spec.MountPath, "cert") + "/certs/" + authName(c.Spec.Name, c.Name)
}

// Desired implements engine.Adapter. Unset fields are sent as the OpenBao default, so removing one resets it; the
// display name defaults to the role name.
func (CertAuthRoleAdapter) Desired(ctx context.Context, r engine.Resolver, c *authv1alpha1.CertAuthRole) (engine.Payload, error) {
	s := c.Spec
	p := engine.Payload{
		"certificate":                  s.Certificate,
		"display_name":                 authName(s.DisplayName, authName(s.Name, c.Name)),
		"allowed_common_names":         builtinList(s.AllowedCommonNames),
		"allowed_dns_sans":             builtinList(s.AllowedDNSSANs),
		"allowed_email_sans":           builtinList(s.AllowedEmailSANs),
		"allowed_uri_sans":             builtinList(s.AllowedURISANs),
		"allowed_organizational_units": builtinList(s.AllowedOrganizationalUnits),
		"required_extensions":          builtinList(s.RequiredExtensions),
		"allowed_metadata_extensions":  builtinList(s.AllowedMetadataExtensions),
		"ocsp_enabled":                 builtinBool(s.OCSPEnabled),
		"ocsp_ca_certificates":         s.OCSPCACertificates,
		"ocsp_servers_override":        builtinList(s.OCSPServersOverride),
		"ocsp_fail_open":               builtinBool(s.OCSPFailOpen),
		"ocsp_query_all_servers":       builtinBool(s.OCSPQueryAllServers),
	}
	if err := putTokenFields(ctx, r, c, p, s.TokenFields); err != nil {
		return nil, err
	}
	putTokenFieldDefaults(p)
	return p, nil
}

// Fields implements engine.Adapter.
func (CertAuthRoleAdapter) Fields() engine.FieldSpec {
	return MergeFieldSpecs(TokenFieldSpec, engine.FieldSpec{
		Sets: []string{"allowed_common_names", "allowed_dns_sans", "allowed_email_sans", "allowed_uri_sans",
			"allowed_organizational_units", "required_extensions", "allowed_metadata_extensions"},
	})
}

// DriftTier implements engine.DriftTier.
func (CertAuthRoleAdapter) DriftTier() engine.Tier { return engine.TierAccessCritical }
