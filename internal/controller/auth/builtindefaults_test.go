package auth

import (
	"context"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	authv1alpha1 "github.com/alexandrust88/openbao-config-operator/api/auth/v1alpha1"
	commonv1alpha1 "github.com/alexandrust88/openbao-config-operator/api/common/v1alpha1"
	"github.com/alexandrust88/openbao-config-operator/internal/engine"
)

// The reads below were captured from OpenBao v2.7.2 for objects written with only their required fields.
const (
	defaultsTokenFieldsRead = `"token_bound_cidrs":[],"token_explicit_max_ttl":0,"token_max_ttl":0,
		"token_no_default_policy":false,"token_num_uses":0,"token_period":0,"token_policies":[],
		"token_strictly_bind_ip":false,"token_ttl":0,"token_type":"default"`
	defaultsCertificate     = "-----BEGIN CERTIFICATE-----\nCA\n-----END CERTIFICATE-----\n"
	defaultsCertificateJSON = `-----BEGIN CERTIFICATE-----\nCA\n-----END CERTIFICATE-----\n`
)

// Test values shared by the builtin and credential auth tests.
const (
	authTestDemo             = "demo"
	authTestRoleID           = "c39eeec9-bd06-c208-d5ae-97defbf5ca0e"
	authTestHostCIDR         = "10.1.2.3/32"
	authTestHostIP           = "10.1.2.3"
	authKeyBindSecretID      = "bind_secret_id"
	authTestDefault          = "default"
	authTestDemoLogin        = "demo-login"
	authKeyDisableLocalCAJWT = "disable_local_ca_jwt"
	authTestK8sHost          = "https://k8s"
	authKeyAliasNameSource   = "alias_name_source"
	authKeyCallbackMode      = "callback_mode"
	authTestIssuer           = "https://idp.example.com/realms/a"
	authTestIDToken          = "id_token"
	authKeyDefaultRole       = "default_role"
	authTestGSuite           = "gsuite"
	authTestClient           = "client"
	authTestEmail            = "email"
	authKeyCELProgram        = "cel_program"
	authKeyExpression        = "expression"
	authTestKubeHost         = "https://kubernetes.default.svc"
	authTestApp              = "app"
	authTestGuest            = "guest"
	authTestAudit            = "audit"
	authTestAliceLogin       = "alice-login"
)

// Literals that several auth tests repeat.
const (
	authLitJWT              = "jwt"
	authLitKubernetesCACert = "kubernetes_ca_cert"
	authLitKubernetesHost   = "kubernetes_host"
	authLitName             = "name"
	authLitNamespaceInState = "namespace_in_state"
	authLitNewAccessor      = "new-accessor"
	authLitOidc             = "oidc"
	authLitPassword         = "password"
	authLitPemKeys          = "pem_keys"
	authLitProjectPath      = "project_path"
	authLitProviderConfig   = "provider_config"
	authLitRadius           = "radius"
	authLitRead             = "read"
	authLitReader           = "reader"
	authLitReaders          = "readers"
	authLitRegion           = "region"
	authLitReviewerJWT      = "reviewer.jwt"
	authLitRgApp            = "rg-app"
	authLitRole             = "role"
	authLitRoleID           = "role_id"
	authLitRoleType         = "role_type"
	authLitSafetyBuffer     = "safety_buffer"
	authLitService          = "service"
	authLitSp1              = "sp-1"
	authLitSpace1           = "space-1"
	authLitSub              = "sub"
	authLitSub1             = "sub-1"
	authLitSubnet0123       = "subnet-0123"
	authLitTrue             = "true"
	authLitType             = "type"
	authLitUniqueID         = "unique_id"
	authLitVpc0123          = "vpc-0123"
	authLitWorker           = "worker"
	authLitZone             = "zone"
	authLitReviewerToken    = "reviewer/token"
	authLitSp2              = "sp-2"
	authLitVmssApp          = "vmss-app"
	authLitWesteurope       = "westeurope"
	authLitWrite            = "write"
	authLitWriter           = "writer"
)

type defaultsCase struct {
	kind string
	// changed is a key whose server default the desired payload must assert.
	changed  string
	observed func(t *testing.T) (desired, observed engine.Payload, fields engine.FieldSpec)
}

// defaultsObserve builds the desired payload of obj and reads read through the adapter's Observer, if any.
func defaultsObserve[T engine.Object](t *testing.T, a engine.Adapter[T], obj T, read string) (engine.Payload, engine.Payload, engine.FieldSpec) {
	t.Helper()
	ctx := context.Background()
	desired, err := a.Desired(ctx, builtinResolver{"login/password": "fake"}, obj)
	if err != nil {
		t.Fatal(err)
	}
	observed := credJSON(t, read)
	if o, ok := any(a).(engine.Observer[T]); ok {
		path := a.Path(obj)
		c := &builtinClient{data: map[string]map[string]any{path: observed, path + "/role-id": {authLitRoleID: "generated"}}}
		var found bool
		if observed, found, err = o.Observe(ctx, c, obj); err != nil || !found {
			t.Fatalf("observe: found=%v err=%v", found, err)
		}
	}
	return desired, observed, a.Fields()
}

func defaultsCases() []defaultsCase {
	meta := metav1.ObjectMeta{Name: "empty", Namespace: pluginTestNamespace}
	return []defaultsCase{
		{"KubernetesAuthConfig", authKeyDisableLocalCAJWT, func(t *testing.T) (engine.Payload, engine.Payload, engine.FieldSpec) {
			obj := &authv1alpha1.KubernetesAuthConfig{ObjectMeta: meta, Spec: authv1alpha1.KubernetesAuthConfigSpec{
				KubernetesHost: authTestK8sHost, KubernetesCACert: defaultsCertificate,
			}}
			return defaultsObserve(t, KubernetesAuthConfigAdapter{}, obj, `{"disable_iss_validation":true,
				"disable_local_ca_jwt":false,"issuer":"","kubernetes_ca_cert":"`+defaultsCertificateJSON+`",
				"kubernetes_host":"https://k8s","pem_keys":[],"token_reviewer_jwt_set":false}`)
		}},
		{"KubernetesAuthRole", authKeyAliasNameSource, func(t *testing.T) (engine.Payload, engine.Payload, engine.FieldSpec) {
			obj := &authv1alpha1.KubernetesAuthRole{ObjectMeta: meta, Spec: authv1alpha1.KubernetesAuthRoleSpec{
				BoundServiceAccountNames: []string{"a"}, BoundServiceAccountNamespaces: []string{"b"},
			}}
			return defaultsObserve(t, KubernetesAuthRoleAdapter{}, obj, `{"alias_name_source":"serviceaccount_uid",
				"bound_service_account_names":["a"],"bound_service_account_namespace_selector":"",
				"bound_service_account_namespaces":["b"],`+defaultsTokenFieldsRead+`}`)
		}},
		{"JWTAuthConfig", authLitNamespaceInState, func(t *testing.T) (engine.Payload, engine.Payload, engine.FieldSpec) {
			obj := &authv1alpha1.JWTAuthConfig{ObjectMeta: meta, Spec: authv1alpha1.JWTAuthConfigSpec{JWKSURL: "https://keys"}}
			return defaultsObserve(t, JWTAuthConfigAdapter{}, obj, `{"bound_issuer":"","default_role":"","jwks_ca_pem":"",
				"jwks_url":"https://keys","jwt_supported_algs":[],"jwt_validation_pubkeys":[],"namespace_in_state":true,
				"oidc_client_id":"","oidc_discovery_ca_pem":"","oidc_discovery_url":"","oidc_response_mode":"",
				"oidc_response_types":[],"override_allowed_server_names":[],"provider_config":{},"status":"valid"}`)
		}},
		{"JWTAuthRole", authKeyCallbackMode, func(t *testing.T) (engine.Payload, engine.Payload, engine.FieldSpec) {
			obj := &authv1alpha1.JWTAuthRole{ObjectMeta: meta, Spec: authv1alpha1.JWTAuthRoleSpec{
				UserClaim: authLitSub, BoundAudiences: []string{"a"},
			}}
			return defaultsObserve(t, JWTAuthRoleAdapter{}, obj, `{"allowed_redirect_uris":null,"bound_audiences":["a"],
				"bound_claims":null,"bound_claims_type":"string","bound_subject":"","callback_mode":"client",
				"claim_mappings":null,"clock_skew_leeway":0,"expiration_leeway":0,"groups_claim":"","max_age":0,
				"not_before_leeway":0,"oauth2_metadata":null,"oidc_disable_confirmation":false,"oidc_scopes":null,
				"role_type":"jwt","token_policies_template_claims":false,"user_claim":"sub","user_claim_json_pointer":false,
				"verbose_oidc_logging":false,`+defaultsTokenFieldsRead+`}`)
		}},
		{"JWTCELRole", keyClockSkewLeeway, func(t *testing.T) (engine.Payload, engine.Payload, engine.FieldSpec) {
			obj := &authv1alpha1.JWTCELRole{ObjectMeta: meta, Spec: authv1alpha1.JWTCELRoleSpec{
				CELProgram: authv1alpha1.CELProgram{Expression: authLitTrue},
			}}
			return defaultsObserve(t, JWTCELRoleAdapter{}, obj, `{"bound_audiences":[],"cel_program":{"expression":"true"},
				"clock_skew_leeway":60,"expiration_leeway":150,"message":"","name":"empty","not_before_leeway":150}`)
		}},
		{"AppRole", authKeyBindSecretID, func(t *testing.T) (engine.Payload, engine.Payload, engine.FieldSpec) {
			obj := &authv1alpha1.AppRole{ObjectMeta: meta}
			return defaultsObserve(t, AppRoleAdapter{}, obj, `{"bind_secret_id":true,"local_secret_ids":false,
				"secret_id_bound_cidrs":null,"secret_id_num_uses":0,"secret_id_ttl":0,`+defaultsTokenFieldsRead+`}`)
		}},
		{"TokenRole", "renewable", func(t *testing.T) (engine.Payload, engine.Payload, engine.FieldSpec) {
			obj := &authv1alpha1.TokenRole{ObjectMeta: meta}
			return defaultsObserve(t, TokenRoleAdapter{}, obj, `{"allowed_entity_aliases":null,"allowed_policies":[],
				"allowed_policies_glob":[],"disallowed_policies":[],"disallowed_policies_glob":[],"explicit_max_ttl":0,
				"name":"empty","orphan":false,"path_suffix":"","period":0,"renewable":true,"token_explicit_max_ttl":0,
				"token_no_default_policy":false,"token_period":0,"token_type":"default-service"}`)
		}},
		{"CertAuthConfig", "ocsp_cache_size", func(t *testing.T) (engine.Payload, engine.Payload, engine.FieldSpec) {
			obj := &authv1alpha1.CertAuthConfig{ObjectMeta: meta}
			return defaultsObserve(t, CertAuthConfigAdapter{}, obj,
				`{"disable_binding":false,"enable_identity_alias_metadata":false,"ocsp_cache_size":0}`)
		}},
		{"CertAuthRole", "display_name", func(t *testing.T) (engine.Payload, engine.Payload, engine.FieldSpec) {
			obj := &authv1alpha1.CertAuthRole{ObjectMeta: meta, Spec: authv1alpha1.CertAuthRoleSpec{Certificate: defaultsCertificate}}
			return defaultsObserve(t, CertAuthRoleAdapter{}, obj, `{"allowed_common_names":null,"allowed_dns_sans":null,
				"allowed_email_sans":null,"allowed_metadata_extensions":null,"allowed_names":null,
				"allowed_organizational_units":null,"allowed_uri_sans":null,
				"certificate":"`+defaultsCertificateJSON+`","display_name":"empty","ocsp_ca_certificates":"",
				"ocsp_enabled":false,"ocsp_fail_open":false,"ocsp_query_all_servers":false,"ocsp_servers_override":null,
				"required_extensions":null,`+defaultsTokenFieldsRead+`}`)
		}},
		{"UserpassUser", keyTokenType, func(t *testing.T) (engine.Payload, engine.Payload, engine.FieldSpec) {
			obj := &authv1alpha1.UserpassUser{ObjectMeta: meta, Spec: authv1alpha1.UserpassUserSpec{
				PasswordSecretRef: &commonv1alpha1.SecretKeySelector{Name: "login", Key: authLitPassword},
			}}
			return defaultsObserve(t, UserpassUserAdapter{}, obj, `{`+defaultsTokenFieldsRead+`}`)
		}},
	}
}

// TestBuiltinAuthDefaults proves that a spec with only its required fields matches what OpenBao stores for such an
// object, so adoption writes nothing, and that each omitted field is still compared at its server default.
func TestBuiltinAuthDefaults(t *testing.T) {
	for _, tc := range defaultsCases() {
		t.Run(tc.kind, func(t *testing.T) {
			desired, observed, fields := tc.observed(t)
			if d := engine.Diff(desired, observed, fields); len(d) != 0 {
				t.Errorf("an empty spec differs from the server defaults on %v", d)
			}
			if _, ok := desired[tc.changed]; !ok {
				t.Fatalf("desired omits %s, so removing it from the spec would not reset it", tc.changed)
			}
			observed[tc.changed] = "changed out of band"
			if d := engine.Diff(desired, observed, fields); len(d) != 1 || d[0] != tc.changed {
				t.Errorf("a changed default must be reported, diff = %v", d)
			}
		})
	}
}
