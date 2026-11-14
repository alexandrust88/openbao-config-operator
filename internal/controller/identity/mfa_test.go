package identity

import (
	"context"
	"encoding/base64"
	"errors"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	commonv1alpha1 "github.com/alexandrust88/openbao-config-operator/api/common/v1alpha1"
	identityv1alpha1 "github.com/alexandrust88/openbao-config-operator/api/identity/v1alpha1"
	sysv1alpha1 "github.com/alexandrust88/openbao-config-operator/api/sys/v1alpha1"
	"github.com/alexandrust88/openbao-config-operator/internal/engine"
)

func secretRef(name, key string) commonv1alpha1.SecretKeySelector {
	return commonv1alpha1.SecretKeySelector{Name: name, Key: key}
}

var mfaSecrets = oidcSecrets{
	"duo/secret-key":      "duo-secret-fake",
	"duo/integration-key": litDuoIkeyFake,
	"okta/api-token":      "okta-token-fake",
	"ping/settings":       "use_base64_key=a\ntoken=t\n",
}

// The read fixtures were captured from OpenBao 2.7.2 (bao server -dev).
func TestMFAMethodSteadyState(t *testing.T) {
	tests := []struct {
		name     string
		spec     identityv1alpha1.MFAMethodSpec
		path     string
		desired  engine.Payload
		read     map[string]any
		mutation map[string]any
		changed  string
	}{
		{
			name: "totp",
			spec: identityv1alpha1.MFAMethodSpec{TOTP: &identityv1alpha1.MFATOTP{
				Issuer: litAcme, Period: oidcDuration(30 * time.Second), KeySize: new(int32(20)), QRSize: new(int32(200)),
				Algorithm: litSHA1, Digits: new(int32(6)), Skew: new(int32(1)), MaxValidationAttempts: new(int32(5)),
			}},
			path: litTOTPCollection,
			desired: engine.Payload{litMethodName: litOtp, litIssuer: litAcme, "period": int64(30), "key_size": int64(20),
				"qr_size": int64(200), litAlgorithm: litSHA1, litDigits: int64(6), "skew": int64(1), "max_validation_attempts": int64(5)},
			read: map[string]any{litAlgorithm: litSHA1, litDigits: 6, "id": "1f13b2d9-c353-fa14-ff51-55201d328681", litIssuer: litAcme,
				"key_size": 20, "max_validation_attempts": 5, litName: litOtp, litNamespaceID: litRoot, litNamespacePath: "",
				"period": 30, "qr_size": 200, "skew": 1, litType: "totp"},
			mutation: map[string]any{litDigits: 8},
			changed:  litDigits,
		},
		{
			name: litDuo,
			spec: identityv1alpha1.MFAMethodSpec{Duo: &identityv1alpha1.MFADuo{
				SecretKeySecretRef: secretRef(litDuo, "secret-key"), IntegrationKeySecretRef: secretRef(litDuo, "integration-key"),
				APIHostname: litDuoHost, PushInfo: litFoo, UsePasscode: new(true), UsernameFormat: litAliasName,
			}},
			path: "identity/mfa/method/duo",
			desired: engine.Payload{litMethodName: litOtp, "secret_key": "duo-secret-fake", "integration_key": litDuoIkeyFake,
				"api_hostname": litDuoHost, "push_info": litFoo, "use_passcode": true, litUsernameFormat: litAliasName},
			read: map[string]any{"api_hostname": litDuoHost, "id": "cd4a3f53-add1-0a3e-365d-6b2eacb3eb16",
				litMountAccessor: "", litName: litOtp, litNamespaceID: litRoot, litNamespacePath: "", "pushinfo": litFoo,
				litType: litDuo, "use_passcode": true, litUsernameFormat: litAliasName},
			mutation: map[string]any{"pushinfo": "bar"},
			changed:  "push_info",
		},
		{
			name: "okta",
			spec: identityv1alpha1.MFAMethodSpec{Okta: &identityv1alpha1.MFAOkta{
				OrgName: litAcmeLower, APITokenSecretRef: secretRef("okta", "api-token"), BaseURL: litOktaCom,
				PrimaryEmail: new(true), UsernameFormat: litEntityName,
			}},
			path: "identity/mfa/method/okta",
			desired: engine.Payload{litMethodName: litOtp, "api_token": "okta-token-fake", "org_name": litAcmeLower, "base_url": litOktaCom,
				"primary_email": true, litUsernameFormat: litEntityName},
			read: map[string]any{"base_url": litOktaCom, "id": "8fa90859-fd87-ec31-294c-ab7bfe2e98c1", litMountAccessor: "",
				litName: litOtp, litNamespaceID: litRoot, litNamespacePath: "", "org_name": litAcmeLower, litType: "okta",
				litUsernameFormat: litEntityName},
			mutation: map[string]any{litName: "renamed"},
			changed:  litMethodName,
		},
		{
			name: "pingid",
			spec: identityv1alpha1.MFAMethodSpec{PingID: &identityv1alpha1.MFAPingID{
				SettingsFileSecretRef: secretRef("ping", "settings"), UsernameFormat: litAliasName,
			}},
			path: "identity/mfa/method/pingid",
			desired: engine.Payload{litMethodName: litOtp, litUsernameFormat: litAliasName,
				"settings_file_base64": base64.StdEncoding.EncodeToString([]byte("use_base64_key=a\ntoken=t\n"))},
			read: map[string]any{"admin_url": "https://admin", "authenticator_url": "https://auth", "id": "7df6f12a-6d06-584c-3f55-00482cd032e1",
				"idp_url": "https://idp", litName: litOtp, litNamespaceID: litRoot, litNamespacePath: "", "org_alias": "org",
				litType: "pingid", "use_signature": true},
			mutation: map[string]any{litName: "renamed"},
			changed:  litMethodName,
		},
	}
	a := MFAMethodAdapter{}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := &identityv1alpha1.MFAMethod{ObjectMeta: oidcMeta(litOtp), Spec: tt.spec}
			if got := a.Path(m); got != tt.path {
				t.Errorf("path before id: %q, want %q", got, tt.path)
			}
			desired, err := a.Desired(context.Background(), mfaSecrets, m)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(desired, tt.desired) {
				t.Fatalf("desired %v", desired)
			}
			fields := mfaFields(t, m)
			assertNoDiff(t, desired, tt.read, fields)
			assertDiff(t, desired, oidcCopy(tt.read, tt.mutation), fields, tt.changed)

			if err := a.WriteStatus(context.Background(), nil, m, tt.read); err != nil {
				t.Fatal(err)
			}
			if got := a.Path(m); got != tt.path+"/"+tt.read["id"].(string) {
				t.Errorf("path after id: %q", got)
			}
		})
	}
}

func TestMFAMethodWriteOnlyHash(t *testing.T) {
	a := MFAMethodAdapter{}
	m := &identityv1alpha1.MFAMethod{ObjectMeta: oidcMeta(litDuo), Spec: identityv1alpha1.MFAMethodSpec{Duo: &identityv1alpha1.MFADuo{
		SecretKeySecretRef: secretRef(litDuo, "secret-key"), IntegrationKeySecretRef: secretRef(litDuo, "integration-key"),
		APIHostname: litDuoHost,
	}}}
	first, _ := a.Desired(context.Background(), mfaSecrets, m)
	rotated := oidcSecrets{"duo/secret-key": "rotated", "duo/integration-key": litDuoIkeyFake}
	second, _ := a.Desired(context.Background(), rotated, m)
	if engine.WriteOnlyHash([]byte("k"), first, a.Fields()) == engine.WriteOnlyHash([]byte("k"), second, a.Fields()) {
		t.Error("a rotated secret must change the write-only hash")
	}
	if got := a.SecretRefs(m); !slices.Equal(got, []string{litDuo, litDuo}) {
		t.Errorf("secret refs %v", got)
	}

	_, err := a.Desired(context.Background(), oidcSecrets{}, m)
	if !errors.Is(err, engine.ErrReferenceNotReady) {
		t.Errorf("missing secret: got %v", err)
	}
	if err != nil && strings.Contains(err.Error(), "duo-secret-fake") {
		t.Error("error leaks a secret value")
	}
}

// mfaFields merges the per-object rules of m over Fields(), as the engine does.
func mfaFields(t *testing.T, m *identityv1alpha1.MFAMethod) engine.FieldSpec {
	t.Helper()
	a := MFAMethodAdapter{}
	extra, err := a.FieldsFor(context.Background(), nil, m)
	if err != nil {
		t.Fatal(err)
	}
	fields := a.Fields()
	fields.WriteOnly = append(slices.Clone(fields.WriteOnly), extra.WriteOnly...)
	return fields
}

func TestMFAMethodPingIDUsernameFormatIsWriteOnly(t *testing.T) {
	a := MFAMethodAdapter{}
	m := &identityv1alpha1.MFAMethod{ObjectMeta: oidcMeta(litOtp), Spec: identityv1alpha1.MFAMethodSpec{PingID: &identityv1alpha1.MFAPingID{
		SettingsFileSecretRef: secretRef("ping", "settings"), UsernameFormat: litAliasName,
	}}}
	fields := mfaFields(t, m)
	first, err := a.Desired(context.Background(), mfaSecrets, m)
	if err != nil {
		t.Fatal(err)
	}
	m.Spec.PingID.UsernameFormat = litEntityName
	second, err := a.Desired(context.Background(), mfaSecrets, m)
	if err != nil {
		t.Fatal(err)
	}
	if engine.WriteOnlyHash([]byte("k"), first, fields) == engine.WriteOnlyHash([]byte("k"), second, fields) {
		t.Error("a changed pingid username_format must change the write-only hash")
	}

	duo := &identityv1alpha1.MFAMethod{Spec: identityv1alpha1.MFAMethodSpec{Duo: &identityv1alpha1.MFADuo{}}}
	if slices.Contains(mfaFields(t, duo).WriteOnly, litUsernameFormat) {
		t.Error("duo returns username_format on read, so it must stay compared")
	}
}

func TestMFAMethodObserveWriteDelete(t *testing.T) {
	ctx := context.Background()
	a := MFAMethodAdapter{}
	m := &identityv1alpha1.MFAMethod{ObjectMeta: oidcMeta(litOtp), Spec: identityv1alpha1.MFAMethodSpec{
		TOTP: &identityv1alpha1.MFATOTP{Issuer: litAcme},
	}}
	bao := newOIDCBao(map[string]map[string]any{
		"identity/mfa/method/totp/id-parent": {"id": "id-parent", litName: litOtp, litNamespacePath: ""},
		"identity/mfa/method/totp/id-other":  {"id": "id-other", litName: "other", litNamespacePath: "team/"},
		litTOTPMethodMine:                    {"id": litIDMine, litName: litOtp, litNamespacePath: "team/"},
	})
	bao.namespace = litTeam

	observed, found, err := a.Observe(ctx, bao, m)
	if err != nil || !found || observed["id"] != litIDMine {
		t.Fatalf("by name: found=%v id=%v err=%v", found, observed["id"], err)
	}

	if _, err := a.Write(ctx, bao, m, engine.Payload{litMethodName: litOtp}, true); err != nil {
		t.Fatal(err)
	}
	if err := a.Delete(ctx, bao, m); err != nil || !slices.Equal(bao.deletes, []string{litTOTPMethodMine}) {
		t.Errorf("delete without an id must find the method by name in its own namespace: %v %v", bao.deletes, err)
	}
	bao.deletes = nil

	m.Status.ID = litIDMine
	if _, found, _ := a.Observe(ctx, bao, m); !found {
		t.Error("by id: not found")
	}
	if _, err := a.Write(ctx, bao, m, engine.Payload{litMethodName: litOtp}, false); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Write(ctx, bao, m, engine.Payload{litMethodName: litOtp}, true); err != nil {
		t.Fatal(err)
	}
	want := []string{litTOTPCollection, litTOTPMethodMine, litTOTPCollection}
	if !slices.Equal(bao.writes, want) {
		t.Errorf("writes %v, want %v", bao.writes, want)
	}
	if err := a.Delete(ctx, bao, m); err != nil || !slices.Equal(bao.deletes, []string{litTOTPMethodMine}) {
		t.Errorf("deletes %v err %v", bao.deletes, err)
	}

	m.Status.ID = "gone"
	if _, found, err := a.Observe(ctx, bao, m); found || err != nil {
		t.Errorf("stale id: found=%v err=%v", found, err)
	}
	if _, found, err := a.Observe(ctx, newOIDCBao(nil), &identityv1alpha1.MFAMethod{ObjectMeta: oidcMeta("x"),
		Spec: identityv1alpha1.MFAMethodSpec{TOTP: &identityv1alpha1.MFATOTP{Issuer: "a"}}}); found || err != nil {
		t.Errorf("empty collection: found=%v err=%v", found, err)
	}
}

func TestMFALoginEnforcement(t *testing.T) {
	method := oidcReady(&identityv1alpha1.MFAMethod{ObjectMeta: oidcMeta(litOtp)}, "method-from-ref")
	mount := oidcReady(&sysv1alpha1.AuthMount{ObjectMeta: oidcMeta(litUserpass)}, "auth_userpass_ref")
	group := oidcReady(&identityv1alpha1.Group{ObjectMeta: oidcMeta("admins")}, "group-from-ref")
	bao := newOIDCBao(map[string]map[string]any{
		"identity/mfa/method/m-1": {"id": litM1, litName: "duo-login", litNamespacePath: ""},
		"sys/auth":                {"oidc/": map[string]any{litAccessor: litAuthOIDCName, litType: "oidc"}},
	})
	a := MFALoginEnforcementAdapter{Reader: oidcReader(t, method, mount, group), Conns: oidcConns{client: bao}}
	e := &identityv1alpha1.MFALoginEnforcement{ObjectMeta: oidcMeta("admins-mfa"), Spec: identityv1alpha1.MFALoginEnforcementSpec{
		MFAMethods: []identityv1alpha1.MFAMethodMatch{
			{MethodRef: &commonv1alpha1.LocalObjectReference{Name: litOtp}}, {MethodName: "duo-login"},
		},
		AuthMounts: []identityv1alpha1.AuthMountMatch{
			{MountRef: &commonv1alpha1.LocalObjectReference{Name: litUserpass}}, {MountPath: "oidc"}, {MountAccessor: litAuthCert1},
		},
		AuthMethodTypes: []string{litUserpass},
		Entities:        []identityv1alpha1.EntityMatch{{EntityID: litEntity1}},
		Groups:          []identityv1alpha1.GroupMatch{{GroupRef: &commonv1alpha1.LocalObjectReference{Name: "admins"}}},
	}}
	if got := (MFALoginEnforcementAdapter{}).Path(e); got != "identity/mfa/login-enforcement/admins-mfa" {
		t.Errorf("path %q", got)
	}
	desired, err := a.Desired(context.Background(), nil, e)
	if err != nil {
		t.Fatal(err)
	}
	want := engine.Payload{
		"mfa_method_ids":       []string{litM1, "method-from-ref"},
		litAuthMethodAccessors: []string{litAuthCert1, litAuthOIDCName, "auth_userpass_ref"},
		"auth_method_types":    []string{litUserpass},
		litIdentityEntityIDs:   []string{litEntity1},
		litIdentityGroupIDs:    []string{"group-from-ref"},
	}
	if !reflect.DeepEqual(desired, want) {
		t.Fatalf("desired %v", desired)
	}
	read := map[string]any{litAuthMethodAccessors: []any{"auth_userpass_ref", litAuthOIDCName, litAuthCert1},
		"auth_method_types": []any{litUserpass}, "id": "1120ec9c-a0a7-e253-058b-a16cde57ff56",
		litIdentityEntityIDs: []any{litEntity1}, litIdentityGroupIDs: []any{"group-from-ref"},
		"mfa_method_ids": []any{"method-from-ref", litM1}, litName: "admins-mfa", litNamespaceID: litRoot, litNamespacePath: ""}
	assertNoDiff(t, desired, read, a.Fields())
	assertDiff(t, desired, oidcCopy(read, map[string]any{litIdentityEntityIDs: []any{}}), a.Fields(), litIdentityEntityIDs)

	if err := a.WriteStatus(context.Background(), nil, e, read); err != nil || e.Status.ID != "1120ec9c-a0a7-e253-058b-a16cde57ff56" {
		t.Errorf("status.id %q err %v", e.Status.ID, err)
	}

	minimal := &identityv1alpha1.MFALoginEnforcement{ObjectMeta: oidcMeta("m"), Spec: identityv1alpha1.MFALoginEnforcementSpec{
		MFAMethods: []identityv1alpha1.MFAMethodMatch{{MethodID: litM1}}, AuthMethodTypes: []string{litUserpass},
	}}
	desired, err = a.Desired(context.Background(), nil, minimal)
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{litAuthMethodAccessors, litIdentityEntityIDs, litIdentityGroupIDs} {
		if v, ok := desired[key].([]string); !ok || len(v) != 0 {
			t.Errorf("%s = %#v, want an empty list so removed targets are cleared", key, desired[key])
		}
	}

	unknown := minimal.DeepCopy()
	unknown.Spec.MFAMethods = []identityv1alpha1.MFAMethodMatch{{MethodName: litMissing}}
	if _, err := a.Desired(context.Background(), nil, unknown); !errors.Is(err, engine.ErrReferenceNotReady) {
		t.Errorf("unknown method name: got %v", err)
	}
}

func TestMFAMethodWithoutTypeIsInvalid(t *testing.T) {
	m := &identityv1alpha1.MFAMethod{ObjectMeta: oidcMeta("otp")}
	if _, err := (MFAMethodAdapter{}).Desired(context.Background(), mfaSecrets, m); !errors.Is(err, engine.ErrInvalidSpec) {
		t.Errorf("err = %v, want ErrInvalidSpec", err)
	}
}
