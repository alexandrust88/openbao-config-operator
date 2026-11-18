package secrets

import (
	"context"

	ctrl "sigs.k8s.io/controller-runtime"

	secretsv1alpha1 "github.com/alexandrust88/openbao-config-operator/api/secrets/v1alpha1"
	"github.com/alexandrust88/openbao-config-operator/internal/engine"
)

// +kubebuilder:rbac:groups=secrets.obco.dev,resources=sshroles,verbs=get;list;watch;update;patch
// +kubebuilder:rbac:groups=secrets.obco.dev,resources=sshroles/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=secrets.obco.dev,resources=sshroles/finalizers,verbs=update

// SetupSSHRole registers the SSHRole controller with the manager.
func SetupSSHRole(mgr ctrl.Manager, conns engine.Connections, opts engine.Options) error {
	r := engine.New[*secretsv1alpha1.SSHRole](
		mgr.GetClient(),
		mgr.GetScheme(),
		mgr.GetEventRecorder("sshrole-controller"),
		conns,
		SSHRoleAdapter{},
		func() *secretsv1alpha1.SSHRole { return &secretsv1alpha1.SSHRole{} },
		opts,
	)
	return r.SetupWithManager(mgr)
}

// SSHRoleAdapter maps an SSHRole onto <mount>/roles/<name>.
type SSHRoleAdapter struct{}

var _ engine.Adapter[*secretsv1alpha1.SSHRole] = SSHRoleAdapter{}

// Path implements engine.Adapter.
func (SSHRoleAdapter) Path(r *secretsv1alpha1.SSHRole) string {
	return r.Spec.MountPath + "/roles/" + specName(r.Spec.Name, r.Name)
}

// Desired implements engine.Adapter. OpenBao stores the list fields as comma-separated strings and returns
// them as written, so they are joined in spec order. A read returns only the fields of the role's key type, so
// only those are sent, with the server default for an omitted one.
func (SSHRoleAdapter) Desired(_ context.Context, _ engine.Resolver, r *secretsv1alpha1.SSHRole) (engine.Payload, error) {
	s := r.Spec
	p := engine.Payload{"key_type": string(s.KeyType)}
	orString(p, "default_user", s.DefaultUser, "")
	orCSV(p, "allowed_users", s.AllowedUsers)
	if s.KeyType == secretsv1alpha1.SSHRoleKeyTypeOTP {
		orCSV(p, "cidr_list", s.CIDRList)
		orCSV(p, "exclude_cidr_list", s.ExcludeCIDRList)
		orInt32(p, "port", s.Port, 22)
		return p, nil
	}
	orFalse(p, "default_user_template", s.DefaultUserTemplate)
	orFalse(p, "allowed_users_template", s.AllowedUsersTemplate)
	orCSV(p, "allowed_domains", s.AllowedDomains)
	orFalse(p, "allowed_domains_template", s.AllowedDomainsTemplate)
	orSeconds(p, "ttl", s.TTL, 0)
	orSeconds(p, "max_ttl", s.MaxTTL, 0)
	orCSV(p, "allowed_critical_options", s.AllowedCriticalOptions)
	orCSV(p, "allowed_extensions", s.AllowedExtensions)
	orMap(p, "default_critical_options", s.DefaultCriticalOptions)
	orMap(p, "default_extensions", s.DefaultExtensions)
	orFalse(p, "default_extensions_template", s.DefaultExtensionsTemplate)
	orFalse(p, "allow_user_certificates", s.AllowUserCertificates)
	orFalse(p, "allow_host_certificates", s.AllowHostCertificates)
	orFalse(p, "allow_bare_domains", s.AllowBareDomains)
	orFalse(p, "allow_subdomains", s.AllowSubdomains)
	orFalse(p, "allow_user_key_ids", s.AllowUserKeyIDs)
	orFalse(p, "allow_empty_principals", s.AllowEmptyPrincipals)
	orFalse(p, "allow_commas_in_identity_templates", s.AllowCommasInIdentityTemplates)
	orFalse(p, "allow_wildcards_in_identity_templates", s.AllowWildcardsInIdentityTemplates)
	orString(p, "key_id_format", s.KeyIDFormat, "")
	orMap(p, "allowed_user_key_lengths", s.AllowedUserKeyLengths)
	orString(p, "algorithm_signer", s.AlgorithmSigner, "default")
	orSeconds(p, "not_before_duration", s.NotBeforeDuration, 30)
	orString(p, "issuer_ref", s.IssuerRef, "default")
	return p, nil
}

// Fields implements engine.Adapter.
func (SSHRoleAdapter) Fields() engine.FieldSpec {
	return engine.FieldSpec{Durations: []string{"ttl", "max_ttl", "not_before_duration"}}
}
