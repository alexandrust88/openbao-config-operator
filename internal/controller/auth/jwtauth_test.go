package auth

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"

	authv1alpha1 "github.com/alexandrust88/openbao-config-operator/api/auth/v1alpha1"
	commonv1alpha1 "github.com/alexandrust88/openbao-config-operator/api/common/v1alpha1"
	"github.com/alexandrust88/openbao-config-operator/internal/engine"
)

const testPubKey = "-----BEGIN PUBLIC KEY-----\nK\n-----END PUBLIC KEY-----"

func fullJWTAuthConfig() *authv1alpha1.JWTAuthConfig {
	return &authv1alpha1.JWTAuthConfig{
		ObjectMeta: metav1.ObjectMeta{Name: authLitOidc, Namespace: pluginTestNamespace},
		Spec: authv1alpha1.JWTAuthConfigSpec{
			MountPath:                  authLitOidc,
			OIDCDiscoveryURL:           authTestIssuer,
			OIDCDiscoveryCAPEM:         "-----BEGIN CERTIFICATE-----\nCA\n-----END CERTIFICATE-----",
			OIDCClientID:               cloudLitOpenbao,
			OIDCClientSecretSecretRef:  &commonv1alpha1.SecretKeySelector{Name: authLitOidc, Key: "client-secret"},
			OIDCResponseMode:           "form_post",
			OIDCResponseTypes:          []authv1alpha1.OIDCResponseType{"code", authTestIDToken},
			JWTSupportedAlgs:           []string{"RS256", "ES256"},
			BoundIssuer:                authTestIssuer,
			DefaultRole:                authLitReader,
			OverrideAllowedServerNames: []string{"idp.internal"},
			NamespaceInState:           new(true),
			SkipJWKSValidation:         new(false),
		},
	}
}

func TestJWTAuthConfigDesired(t *testing.T) {
	a := JWTAuthConfigAdapter{}
	j := fullJWTAuthConfig()
	if got := a.Path(j); got != "auth/oidc/config" {
		t.Errorf("path = %q", got)
	}
	got, err := a.Desired(context.Background(), builtinResolver{"oidc/client-secret": "s3cret"}, j)
	if err != nil {
		t.Fatal(err)
	}
	want := engine.Payload{
		"oidc_discovery_url":            authTestIssuer,
		"oidc_discovery_ca_pem":         j.Spec.OIDCDiscoveryCAPEM,
		"oidc_client_id":                cloudLitOpenbao,
		"oidc_client_secret":            "s3cret",
		"oidc_response_mode":            "form_post",
		"oidc_response_types":           []string{"code", authTestIDToken},
		"jwks_url":                      "",
		"jwks_ca_pem":                   "",
		"jwt_validation_pubkeys":        []string{},
		"jwt_supported_algs":            []string{"RS256", "ES256"},
		"bound_issuer":                  authTestIssuer,
		authKeyDefaultRole:              authLitReader,
		authLitProviderConfig:           map[string]any{},
		"override_allowed_server_names": []string{"idp.internal"},
		authLitNamespaceInState:         true,
		"skip_jwks_validation":          false,
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("desired = %v\nwant %v", got, want)
	}
	if _, err := a.Desired(context.Background(), builtinResolver{}, j); !errors.Is(err, engine.ErrReferenceNotReady) {
		t.Errorf("missing secret: err = %v", err)
	}
}

func TestJWTAuthConfigSteadyState(t *testing.T) {
	a := JWTAuthConfigAdapter{}
	j := &authv1alpha1.JWTAuthConfig{Spec: authv1alpha1.JWTAuthConfigSpec{
		JWTValidationPubkeys: []string{testPubKey},
		BoundIssuer:          "https://issuer",
	}}
	desired, err := a.Desired(context.Background(), builtinResolver{}, j)
	if err != nil {
		t.Fatal(err)
	}
	// read shape captured from OpenBao v2.7.2
	observed := engine.Payload{
		"bound_issuer": "https://issuer", authKeyDefaultRole: "", "jwks_ca_pem": "", "jwks_url": "",
		"jwt_supported_algs": []any{}, "jwt_validation_pubkeys": []any{testPubKey}, authLitNamespaceInState: true,
		"oidc_client_id": "", "oidc_discovery_ca_pem": "", "oidc_discovery_url": "", "oidc_response_mode": "",
		"oidc_response_types": []any{}, "override_allowed_server_names": []any{}, authLitProviderConfig: map[string]any{},
		"status": "valid",
	}
	if d := engine.Diff(desired, observed, a.Fields()); len(d) != 0 {
		t.Errorf("steady state differs on %v", d)
	}
	observed[authKeyDefaultRole] = "intruder"
	if d := engine.Diff(desired, observed, a.Fields()); !reflect.DeepEqual(d, []string{authKeyDefaultRole}) {
		t.Errorf("out-of-band default_role: diff = %v", d)
	}
}

func TestJWTAuthConfigProviderSecret(t *testing.T) {
	a := JWTAuthConfigAdapter{}
	j := &authv1alpha1.JWTAuthConfig{
		ObjectMeta: metav1.ObjectMeta{Namespace: pluginTestNamespace},
		Spec: authv1alpha1.JWTAuthConfigSpec{
			ProviderConfig: &authv1alpha1.JWTProviderConfig{
				Provider:  authTestGSuite,
				Settings:  map[string]apiextensionsv1.JSON{"fetch_groups": {Raw: []byte(`true`)}},
				SecretRef: &commonv1alpha1.SecretKeySelector{Name: authTestGSuite, Key: "config"},
			},
		},
	}
	desired, err := a.Desired(context.Background(), builtinResolver{"gsuite/config": `{"gsuite_service_account":"{\"key\":\"x\"}"}`}, j)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]any{"provider": authTestGSuite, "fetch_groups": true, "gsuite_service_account": `{"key":"x"}`}
	if !reflect.DeepEqual(desired[authLitProviderConfig], want) {
		t.Errorf("provider_config = %v", desired[authLitProviderConfig])
	}
	fields, err := a.FieldsFor(context.Background(), nil, j)
	if err != nil || !reflect.DeepEqual(fields.WriteOnly, []string{authLitProviderConfig}) {
		t.Errorf("FieldsFor = %v, %v", fields, err)
	}
	if got := a.SecretRefs(j); !reflect.DeepEqual(got, []string{authTestGSuite}) {
		t.Errorf("SecretRefs = %v", got)
	}

	_, err = a.Desired(context.Background(), builtinResolver{"gsuite/config": "not json s3cret"}, j)
	if err == nil || strings.Contains(err.Error(), "s3cret") {
		t.Errorf("invalid secret JSON: err = %v, must fail without quoting the value", err)
	}
}

func fullJWTAuthRole() *authv1alpha1.JWTAuthRole {
	return &authv1alpha1.JWTAuthRole{
		ObjectMeta: metav1.ObjectMeta{Name: "ci", Namespace: pluginTestNamespace},
		Spec: authv1alpha1.JWTAuthRoleSpec{
			RoleType:             authv1alpha1.JWTRoleTypeJWT,
			UserClaim:            authLitSub,
			UserClaimJSONPointer: new(false),
			BoundAudiences:       []string{cloudLitOpenbao, "ci"},
			BoundSubject:         "project_path:group/app:ref_type:branch:ref:main",
			BoundClaimsType:      "glob",
			BoundClaims:          map[string][]string{authLitProjectPath: {"group/*"}},
			ClaimMappings:        map[string]string{authLitProjectPath: cloudLitProject},
			GroupsClaim:          "groups",
			OIDCScopes:           []string{"profile"},
			AllowedRedirectURIs:  []string{"https://openbao.example.com/ui/vault/auth/jwt/oidc/callback"},
			CallbackMode:         authTestClient,
			PollInterval:         new(int32(5)),
			MaxAge:               builtinDur(time.Hour),
			JWTLeeways: authv1alpha1.JWTLeeways{
				ExpirationLeeway: new(intstr.FromInt32(-1)),
				NotBeforeLeeway:  new(intstr.FromString("1m")),
				ClockSkewLeeway:  new(intstr.FromInt32(30)),
			},
			OAuth2Metadata:              []authv1alpha1.OAuth2MetadataName{authTestIDToken},
			VerboseOIDCLogging:          new(false),
			TokenPoliciesTemplateClaims: new(false),
			OIDCDisableConfirmation:     new(true),
			TokenFields: authv1alpha1.TokenFields{
				TokenTTL:      builtinDur(10 * time.Minute),
				TokenPolicies: []string{"ci"},
			},
		},
	}
}

func TestJWTAuthRoleDesired(t *testing.T) {
	a := JWTAuthRoleAdapter{}
	j := fullJWTAuthRole()
	if got := a.Path(j); got != "auth/jwt/role/ci" {
		t.Errorf("path = %q", got)
	}
	got, err := a.Desired(context.Background(), nil, j)
	if err != nil {
		t.Fatal(err)
	}
	want := engine.Payload{
		authLitRoleType:                  authLitJWT,
		"user_claim":                     authLitSub,
		"user_claim_json_pointer":        false,
		keyBoundAudiences:                []string{cloudLitOpenbao, "ci"},
		"bound_subject":                  "project_path:group/app:ref_type:branch:ref:main",
		"bound_claims_type":              "glob",
		"bound_claims":                   map[string][]string{authLitProjectPath: {"group/*"}},
		"claim_mappings":                 map[string]string{authLitProjectPath: cloudLitProject},
		"groups_claim":                   "groups",
		"oidc_scopes":                    []string{"profile"},
		"allowed_redirect_uris":          []string{"https://openbao.example.com/ui/vault/auth/jwt/oidc/callback"},
		authKeyCallbackMode:              authTestClient,
		"poll_interval":                  int64(5),
		"max_age":                        int64(3600),
		keyExpirationLeeway:              int64(-1),
		keyNotBeforeLeeway:               int64(60),
		keyClockSkewLeeway:               int64(30),
		"oauth2_metadata":                []string{authTestIDToken},
		"verbose_oidc_logging":           false,
		"token_policies_template_claims": false,
		"oidc_disable_confirmation":      true,
		keyTokenTTL:                      int64(600),
		keyTokenPolicies:                 []string{"ci"},
		keyTokenBoundCIDRs:               []string{},
		keyTokenExplicitMaxTTL:           int64(0),
		keyTokenMaxTTL:                   int64(0),
		keyTokenNoDefaultPolicy:          false,
		keyTokenNumUses:                  int64(0),
		keyTokenPeriod:                   int64(0),
		keyTokenStrictlyBindIP:           false,
		keyTokenType:                     authTestDefault,
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("desired = %v\nwant %v", got, want)
	}

}

func TestJWTAuthRoleSteadyState(t *testing.T) {
	a := JWTAuthRoleAdapter{}
	j := &authv1alpha1.JWTAuthRole{Spec: authv1alpha1.JWTAuthRoleSpec{
		RoleType:       authLitJWT,
		UserClaim:      authLitSub,
		BoundAudiences: []string{"a", "b"},
		BoundClaims:    map[string][]string{"grp": {"x", "y"}},
		ClaimMappings:  map[string]string{authTestEmail: authTestEmail},
		PollInterval:   new(int32(5)),
		MaxAge:         builtinDur(time.Hour),
		JWTLeeways: authv1alpha1.JWTLeeways{
			ExpirationLeeway: new(intstr.FromInt32(-1)),
			NotBeforeLeeway:  new(intstr.FromString("1m")),
		},
	}}
	desired, err := a.Desired(context.Background(), nil, j)
	if err != nil {
		t.Fatal(err)
	}
	// read shape captured from OpenBao v2.7.2
	observed := engine.Payload{
		"allowed_redirect_uris": nil, keyBoundAudiences: []any{"b", "a"}, "bound_claims": map[string]any{"grp": []any{"x", "y"}},
		"bound_claims_type": "string", "bound_subject": "", authKeyCallbackMode: authTestClient,
		"claim_mappings": map[string]any{authTestEmail: authTestEmail}, keyClockSkewLeeway: 0, keyExpirationLeeway: -1,
		"groups_claim": "", "max_age": 3600, keyNotBeforeLeeway: 60, "oauth2_metadata": nil,
		"oidc_disable_confirmation": false, "oidc_scopes": nil, "poll_interval": 5, authLitRoleType: authLitJWT,
		keyTokenBoundCIDRs: []any{}, keyTokenExplicitMaxTTL: 0, keyTokenMaxTTL: 0, keyTokenNoDefaultPolicy: false,
		keyTokenNumUses: 0, keyTokenPeriod: 0, keyTokenPolicies: []any{}, "token_policies_template_claims": false,
		keyTokenStrictlyBindIP: false, keyTokenTTL: 0, keyTokenType: authTestDefault, "user_claim": authLitSub,
		"user_claim_json_pointer": false, "verbose_oidc_logging": false,
	}
	if d := engine.Diff(desired, observed, a.Fields()); len(d) != 0 {
		t.Errorf("steady state differs on %v", d)
	}
	observed[authLitRoleType] = authLitOidc
	observed[keyExpirationLeeway] = 150
	if d := engine.Diff(desired, observed, a.Fields()); !reflect.DeepEqual(d, []string{keyExpirationLeeway, authLitRoleType}) {
		t.Errorf("drift: diff = %v", d)
	}
}

func TestLeewaySeconds(t *testing.T) {
	for _, tc := range []struct {
		in   intstr.IntOrString
		want int64
	}{
		{intstr.FromInt32(-1), -1},
		{intstr.FromInt32(0), 0},
		{intstr.FromInt32(90), 90},
		{intstr.FromString("30s"), 30},
		{intstr.FromString("2m"), 120},
		{intstr.FromString("1h30m"), 5400},
		{intstr.FromString("45"), 45},
	} {
		got, err := leewaySeconds(tc.in)
		if err != nil || got != tc.want {
			t.Errorf("leewaySeconds(%v) = %d, %v; want %d", tc.in.String(), got, err, tc.want)
		}
	}
	if _, err := leewaySeconds(intstr.FromString("soon")); err == nil {
		t.Error("an invalid duration must fail")
	}
}

func fullJWTCELRole() *authv1alpha1.JWTCELRole {
	return &authv1alpha1.JWTCELRole{
		ObjectMeta: metav1.ObjectMeta{Name: "ci-cel", Namespace: pluginTestNamespace},
		Spec: authv1alpha1.JWTCELRoleSpec{
			MountPath: authLitJWT,
			Name:      "ci",
			CELProgram: authv1alpha1.CELProgram{
				Variables:  []authv1alpha1.CELVariable{{Name: cloudLitProject, Expression: "claims.project_path"}},
				Expression: `project.startsWith("group/")`,
			},
			Message:        "project not allowed",
			BoundAudiences: []string{cloudLitOpenbao},
			JWTLeeways:     authv1alpha1.JWTLeeways{ClockSkewLeeway: new(intstr.FromInt32(-1))},
		},
	}
}

func TestJWTCELRoleDesired(t *testing.T) {
	a := JWTCELRoleAdapter{}
	j := fullJWTCELRole()
	if got := a.Path(j); got != "auth/jwt/cel/role/ci" {
		t.Errorf("path = %q", got)
	}
	got, err := a.Desired(context.Background(), nil, j)
	if err != nil {
		t.Fatal(err)
	}
	want := engine.Payload{
		authKeyCELProgram: map[string]any{
			authKeyExpression: `project.startsWith("group/")`,
			"variables":       []any{map[string]any{authLitName: cloudLitProject, authKeyExpression: "claims.project_path"}},
		},
		"message":           "project not allowed",
		keyBoundAudiences:   []string{cloudLitOpenbao},
		keyExpirationLeeway: int64(150),
		keyNotBeforeLeeway:  int64(150),
		keyClockSkewLeeway:  int64(-1),
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("desired = %v\nwant %v", got, want)
	}
}

func TestJWTCELRoleSteadyState(t *testing.T) {
	a := JWTCELRoleAdapter{}
	j := &authv1alpha1.JWTCELRole{Spec: authv1alpha1.JWTCELRoleSpec{CELProgram: authv1alpha1.CELProgram{Expression: authLitTrue}}}
	desired, err := a.Desired(context.Background(), nil, j)
	if err != nil {
		t.Fatal(err)
	}
	// read shape captured from OpenBao v2.7.2 after writing only cel_program
	observed := engine.Payload{
		keyBoundAudiences: []any{}, authKeyCELProgram: map[string]any{authKeyExpression: authLitTrue}, keyClockSkewLeeway: 60,
		keyExpirationLeeway: 150, "message": "", authLitName: "c2", keyNotBeforeLeeway: 150,
	}
	if d := engine.Diff(desired, observed, a.Fields()); len(d) != 0 {
		t.Errorf("steady state differs on %v", d)
	}
	observed[authKeyCELProgram] = map[string]any{authKeyExpression: "false"}
	if d := engine.Diff(desired, observed, a.Fields()); !reflect.DeepEqual(d, []string{authKeyCELProgram}) {
		t.Errorf("changed program: diff = %v", d)
	}
	observed[authKeyCELProgram] = map[string]any{authKeyExpression: authLitTrue, "variables": []any{map[string]any{authLitName: "x", authKeyExpression: "1"}}}
	if d := engine.Diff(desired, observed, a.Fields()); !reflect.DeepEqual(d, []string{authKeyCELProgram}) {
		t.Errorf("added variable: diff = %v", d)
	}
}
