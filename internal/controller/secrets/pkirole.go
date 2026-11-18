package secrets

import (
	"context"

	ctrl "sigs.k8s.io/controller-runtime"

	secretsv1alpha1 "github.com/alexandrust88/openbao-config-operator/api/secrets/v1alpha1"
	"github.com/alexandrust88/openbao-config-operator/internal/engine"
)

// +kubebuilder:rbac:groups=secrets.obco.dev,resources=pkiroles,verbs=get;list;watch;update;patch
// +kubebuilder:rbac:groups=secrets.obco.dev,resources=pkiroles/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=secrets.obco.dev,resources=pkiroles/finalizers,verbs=update
// +kubebuilder:rbac:groups=secrets.obco.dev,resources=pkiissuers,verbs=get;list;watch

// SetupPKIRole registers the PKIRole controller with the manager.
func SetupPKIRole(mgr ctrl.Manager, conns engine.Connections, opts engine.Options) error {
	r := engine.New[*secretsv1alpha1.PKIRole](
		mgr.GetClient(),
		mgr.GetScheme(),
		mgr.GetEventRecorder("pkirole-controller"),
		conns,
		PKIRoleAdapter{},
		func() *secretsv1alpha1.PKIRole { return &secretsv1alpha1.PKIRole{} },
		opts,
	)
	return r.SetupWithManager(mgr)
}

// PKIRoleAdapter maps a PKIRole onto <mount>/roles/<name>.
type PKIRoleAdapter struct{}

var _ engine.Adapter[*secretsv1alpha1.PKIRole] = PKIRoleAdapter{}

// Path implements engine.Adapter.
func (PKIRoleAdapter) Path(r *secretsv1alpha1.PKIRole) string {
	return pkiMount(r.Spec.MountPath) + "/roles/" + specName(r.Spec.Name, r.Name)
}

// Desired implements engine.Adapter. Every field is sent, an unset one at its OpenBao default, because a role
// write replaces the whole role.
func (PKIRoleAdapter) Desired(ctx context.Context, res engine.Resolver, r *secretsv1alpha1.PKIRole) (engine.Payload, error) {
	s := &r.Spec
	p := engine.Payload{"issuer_ref": "default"}
	if s.Issuer != nil {
		ref, err := pkiIssuerRef(ctx, res, r, &s.ResourceSpec, pkiMount(s.MountPath), *s.Issuer)
		if err != nil {
			return nil, err
		}
		p["issuer_ref"] = ref
	}
	keyType := s.KeyType
	if keyType == "" {
		keyType = "rsa"
	}
	orSeconds(p, "ttl", s.TTL, 0)
	orSeconds(p, "max_ttl", s.MaxTTL, 0)
	orTrue(p, "allow_localhost", s.AllowLocalhost)
	orList(p, "allowed_domains", s.AllowedDomains)
	orFalse(p, "allowed_domains_template", s.AllowedDomainsTemplate)
	orFalse(p, "allow_globs_in_identity_templates", s.AllowGlobsInIdentityTemplates)
	orFalse(p, "allow_bare_domains", s.AllowBareDomains)
	orFalse(p, "allow_subdomains", s.AllowSubdomains)
	orFalse(p, "allow_glob_domains", s.AllowGlobDomains)
	orTrue(p, "allow_wildcard_certificates", s.AllowWildcardCertificates)
	orFalse(p, "allow_any_name", s.AllowAnyName)
	orTrue(p, "enforce_hostnames", s.EnforceHostnames)
	orTrue(p, "allow_ip_sans", s.AllowIPSANs)
	orList(p, "allowed_ip_sans_cidr", s.AllowedIPSANsCIDR)
	orList(p, "allowed_uri_sans", s.AllowedURISANs)
	orFalse(p, "allowed_uri_sans_template", s.AllowedURISANsTemplate)
	orList(p, "allowed_other_sans", s.AllowedOtherSANs)
	orList(p, "allowed_serial_numbers", s.AllowedSerialNumbers)
	orList(p, "allowed_user_ids", s.AllowedUserIDs)
	orTrue(p, "server_flag", s.ServerFlag)
	orTrue(p, "client_flag", s.ClientFlag)
	orFalse(p, "code_signing_flag", s.CodeSigningFlag)
	orFalse(p, "email_protection_flag", s.EmailProtectionFlag)
	p["key_type"] = keyType
	orInt32(p, "key_bits", s.KeyBits, pkiRoleDefaultKeyBits[keyType])
	orInt32(p, "signature_bits", s.SignatureBits, 0)
	orFalse(p, "use_pss", s.UsePSS)
	orListDefault(p, "key_usage", s.KeyUsage, pkiRoleDefaultKeyUsage)
	orList(p, "ext_key_usage", s.ExtKeyUsage)
	orList(p, "ext_key_usage_oids", s.ExtKeyUsageOIDs)
	orTrue(p, "use_csr_common_name", s.UseCSRCommonName)
	orTrue(p, "use_csr_sans", s.UseCSRSANs)
	orList(p, "ou", s.OU)
	orList(p, "organization", s.Organization)
	orList(p, "country", s.Country)
	orList(p, "locality", s.Locality)
	orList(p, "province", s.Province)
	orList(p, "street_address", s.StreetAddress)
	orList(p, "postal_code", s.PostalCode)
	orFalse(p, "generate_lease", s.GenerateLease)
	orFalse(p, "no_store", s.NoStore)
	orTrue(p, "require_cn", s.RequireCN)
	orListDefault(p, "cn_validations", s.CNValidations, pkiRoleDefaultCNValidations)
	orList(p, "policy_identifiers", s.PolicyIdentifiers)
	orFalse(p, "basic_constraints_valid_for_non_ca", s.BasicConstraintsValidForNonCA)
	orSeconds(p, "not_before_duration", s.NotBeforeDuration, 30)
	orString(p, "not_before", s.NotBefore, "")
	orString(p, "not_before_bound", s.NotBeforeBound, "permit")
	orString(p, "not_after", s.NotAfter, "")
	orString(p, "not_after_bound", s.NotAfterBound, "permit")
	return p, nil
}

// OpenBao defaults of a PKI role that are not zero values.
var (
	pkiRoleDefaultKeyBits       = map[string]int64{"rsa": 2048, "ec": 256, "mldsa": 44}
	pkiRoleDefaultKeyUsage      = []string{"DigitalSignature", "KeyAgreement", "KeyEncipherment"}
	pkiRoleDefaultCNValidations = []string{"email", "hostname"}
)

// Fields implements engine.Adapter.
func (PKIRoleAdapter) Fields() engine.FieldSpec {
	return engine.FieldSpec{
		Sets: []string{
			"allowed_domains", "allowed_ip_sans_cidr", "allowed_uri_sans", "allowed_other_sans",
			"allowed_serial_numbers", "allowed_user_ids", "key_usage", "ext_key_usage", "ext_key_usage_oids",
			"ou", "organization", "country", "locality", "province", "street_address", "postal_code",
			"cn_validations", "policy_identifiers",
		},
		Durations: []string{"ttl", "max_ttl", "not_before_duration"},
	}
}
