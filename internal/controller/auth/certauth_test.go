package auth

import (
	"context"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	authv1alpha1 "github.com/alexandrust88/openbao-config-operator/api/auth/v1alpha1"
	"github.com/alexandrust88/openbao-config-operator/internal/engine"
)

const credTestCert = "-----BEGIN CERTIFICATE-----\nMIIBfake\n-----END CERTIFICATE-----\n"

// certRoleRead is `bao read -format=json auth/cert/certs/web | jq .data` from OpenBao 2.7.2, certificate replaced.
const certRoleRead = `{"allowed_common_names":["a.example.com","b.example.com"],"allowed_dns_sans":null,
"allowed_email_sans":null,"allowed_metadata_extensions":null,"allowed_names":null,
"allowed_organizational_units":null,"allowed_uri_sans":null,"certificate":"-----BEGIN CERTIFICATE-----\nMIIBfake\n-----END CERTIFICATE-----\n",
"display_name":"web","ocsp_ca_certificates":"","ocsp_enabled":false,"ocsp_fail_open":false,
"ocsp_query_all_servers":false,"ocsp_servers_override":null,"required_extensions":null,"token_bound_cidrs":[],
"token_explicit_max_ttl":0,"token_max_ttl":0,"token_no_default_policy":false,"token_num_uses":0,"token_period":0,
"token_policies":["p"],"token_strictly_bind_ip":false,"token_ttl":3600,"token_type":"default"}`

func TestCertAuthConfig(t *testing.T) {
	c := &authv1alpha1.CertAuthConfig{
		ObjectMeta: metav1.ObjectMeta{Name: "cert", Namespace: pluginTestNamespace},
		Spec: authv1alpha1.CertAuthConfigSpec{
			ResourceSpec:                credConnection(),
			MountPath:                   "certs/team",
			DisableBinding:              new(false),
			EnableIdentityAliasMetadata: new(false),
			OCSPCacheSize:               new(int32(50)),
		},
	}
	adapter := CertAuthConfigAdapter{}
	if got := adapter.Path(c); got != "auth/certs/team/config" {
		t.Errorf("path %q", got)
	}
	desired, err := adapter.Desired(context.Background(), nil, c)
	if err != nil {
		t.Fatal(err)
	}
	observed := credJSON(t, `{"disable_binding":false,"enable_identity_alias_metadata":false,"ocsp_cache_size":50}`)
	credExpectDiff(t, desired, observed, adapter.Fields())
	observed["ocsp_cache_size"] = 100
	credExpectDiff(t, desired, observed, adapter.Fields(), "ocsp_cache_size")
}

func TestCertAuthRole(t *testing.T) {
	c := &authv1alpha1.CertAuthRole{
		ObjectMeta: metav1.ObjectMeta{Name: "web", Namespace: pluginTestNamespace},
		Spec: authv1alpha1.CertAuthRoleSpec{
			ResourceSpec:       credConnection(),
			Certificate:        credTestCert,
			AllowedCommonNames: []string{"b.example.com", "a.example.com"},
			OCSPEnabled:        new(false),
			TokenFields: authv1alpha1.TokenFields{
				TokenTTL:      credDuration(time.Hour),
				TokenPolicies: []string{"p"},
			},
		},
	}
	adapter := CertAuthRoleAdapter{}
	if got := adapter.Path(c); got != "auth/cert/certs/web" {
		t.Errorf("path %q", got)
	}
	desired, err := adapter.Desired(context.Background(), nil, c)
	if err != nil {
		t.Fatal(err)
	}
	if desired["display_name"] != "web" {
		t.Errorf("display_name = %v, want the role name", desired["display_name"])
	}
	observed := credJSON(t, certRoleRead)
	credExpectDiff(t, desired, observed, adapter.Fields())

	c.Spec.OCSPServersOverride = []string{"http://ocsp-a", "http://ocsp-b"}
	desired, _ = adapter.Desired(context.Background(), nil, c)
	observed["ocsp_servers_override"] = []any{"http://ocsp-b", "http://ocsp-a"}
	credExpectDiff(t, desired, observed, adapter.Fields(), "ocsp_servers_override")
}

func TestCertAuthConfigIsSingleton(t *testing.T) {
	var s engine.Singleton = CertAuthConfigAdapter{}
	if !s.Singleton() {
		t.Error("CertAuthConfig always exists and must be adopted whatever the creationPolicy")
	}
}
