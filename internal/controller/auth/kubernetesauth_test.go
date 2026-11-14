package auth

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	authv1alpha1 "github.com/alexandrust88/openbao-config-operator/api/auth/v1alpha1"
	commonv1alpha1 "github.com/alexandrust88/openbao-config-operator/api/common/v1alpha1"
	"github.com/alexandrust88/openbao-config-operator/internal/engine"
	"github.com/alexandrust88/openbao-config-operator/internal/openbao"
)

// builtinResolver serves Secrets from a map keyed "<name>/<key>".
type builtinResolver map[string]string

func (r builtinResolver) SecretValue(_ context.Context, _ string, ref commonv1alpha1.SecretKeySelector) ([]byte, error) {
	v, ok := r[ref.Name+"/"+ref.Key]
	if !ok {
		return nil, engine.ErrReferenceNotReady
	}
	return []byte(v), nil
}

func (builtinResolver) Object(context.Context, client.ObjectKey, client.Object) error { return nil }

// builtinClient records writes and serves reads from a map keyed by path.
type builtinClient struct {
	openbao.Client
	data   map[string]map[string]any
	writes map[string]map[string]any
}

func (c *builtinClient) Read(_ context.Context, path string) (map[string]any, error) {
	if d, ok := c.data[path]; ok {
		return d, nil
	}
	return nil, openbao.ErrNotFound
}

func (c *builtinClient) Write(_ context.Context, path string, data map[string]any) (map[string]any, error) {
	if c.writes == nil {
		c.writes = map[string]map[string]any{}
	}
	c.writes[path] = data
	return nil, nil
}

func builtinDur(d time.Duration) *metav1.Duration { return &metav1.Duration{Duration: d} }

func fullKubernetesAuthConfig() *authv1alpha1.KubernetesAuthConfig {
	return &authv1alpha1.KubernetesAuthConfig{
		ObjectMeta: metav1.ObjectMeta{Name: "cluster", Namespace: pluginTestNamespace},
		Spec: authv1alpha1.KubernetesAuthConfigSpec{
			MountPath:                 "kubernetes-dev",
			KubernetesHost:            authTestKubeHost,
			KubernetesCACert:          "-----BEGIN CERTIFICATE-----\nCA\n-----END CERTIFICATE-----",
			TokenReviewerJWTSecretRef: &commonv1alpha1.SecretKeySelector{Name: "reviewer", Key: "token"},
			PEMKeys:                   []string{"-----BEGIN PUBLIC KEY-----\nK\n-----END PUBLIC KEY-----\n"},
			DisableLocalCAJWT:         new(true),
		},
	}
}

func TestKubernetesAuthConfigPath(t *testing.T) {
	k := fullKubernetesAuthConfig()
	if got := (KubernetesAuthConfigAdapter{}).Path(k); got != "auth/kubernetes-dev/config" {
		t.Errorf("path = %q", got)
	}
	k.Spec.MountPath = ""
	if got := (KubernetesAuthConfigAdapter{}).Path(k); got != "auth/kubernetes/config" {
		t.Errorf("default path = %q", got)
	}
}

func TestKubernetesAuthConfigDesired(t *testing.T) {
	k := fullKubernetesAuthConfig()
	got, err := KubernetesAuthConfigAdapter{}.Desired(context.Background(), builtinResolver{authLitReviewerToken: authLitReviewerJWT}, k)
	if err != nil {
		t.Fatal(err)
	}
	want := engine.Payload{
		authLitKubernetesHost:    authTestKubeHost,
		authLitKubernetesCACert:  k.Spec.KubernetesCACert,
		authLitPemKeys:           []string{"-----BEGIN PUBLIC KEY-----\nK\n-----END PUBLIC KEY-----"},
		authKeyDisableLocalCAJWT: true,
		"token_reviewer_jwt":     authLitReviewerJWT,
		tokenReviewerJWTSet:      true,
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("desired = %v\nwant %v", got, want)
	}

	minimal := &authv1alpha1.KubernetesAuthConfig{Spec: authv1alpha1.KubernetesAuthConfigSpec{KubernetesHost: authTestK8sHost}}
	got, err = KubernetesAuthConfigAdapter{}.Desired(context.Background(), builtinResolver{}, minimal)
	if err != nil {
		t.Fatal(err)
	}
	want = engine.Payload{
		authLitKubernetesHost:    authTestK8sHost,
		authLitKubernetesCACert:  "",
		authLitPemKeys:           []string{},
		authKeyDisableLocalCAJWT: false,
		tokenReviewerJWTSet:      false,
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("minimal desired = %v\nwant %v (a full-replace write must reset unset fields)", got, want)
	}
}

func TestKubernetesAuthConfigMissingSecret(t *testing.T) {
	_, err := KubernetesAuthConfigAdapter{}.Desired(context.Background(), builtinResolver{}, fullKubernetesAuthConfig())
	if !errors.Is(err, engine.ErrReferenceNotReady) {
		t.Errorf("err = %v, want ErrReferenceNotReady", err)
	}
}

func TestKubernetesAuthConfigSteadyState(t *testing.T) {
	a := KubernetesAuthConfigAdapter{}
	k := fullKubernetesAuthConfig()
	desired, err := a.Desired(context.Background(), builtinResolver{authLitReviewerToken: authLitReviewerJWT}, k)
	if err != nil {
		t.Fatal(err)
	}
	// read shape captured from OpenBao v2.7.2
	observed := engine.Payload{
		"disable_iss_validation": true,
		authKeyDisableLocalCAJWT: true,
		"issuer":                 "",
		authLitKubernetesCACert:  k.Spec.KubernetesCACert,
		authLitKubernetesHost:    authTestKubeHost,
		authLitPemKeys:           []any{"-----BEGIN PUBLIC KEY-----\nK\n-----END PUBLIC KEY-----"},
		tokenReviewerJWTSet:      true,
	}
	if d := engine.Diff(desired, observed, a.Fields()); len(d) != 0 {
		t.Errorf("steady state differs on %v", d)
	}

	observed[tokenReviewerJWTSet] = false
	if d := engine.Diff(desired, observed, a.Fields()); !reflect.DeepEqual(d, []string{tokenReviewerJWTSet}) {
		t.Errorf("cleared reviewer JWT: diff = %v", d)
	}

	observed[tokenReviewerJWTSet] = true
	observed[authLitKubernetesCACert] = ""
	if d := engine.Diff(desired, observed, a.Fields()); !reflect.DeepEqual(d, []string{authLitKubernetesCACert}) {
		t.Errorf("changed CA: diff = %v", d)
	}

	other, err := a.Desired(context.Background(), builtinResolver{authLitReviewerToken: "rotated.jwt"}, k)
	if err != nil {
		t.Fatal(err)
	}
	key := []byte("k")
	if engine.WriteOnlyHash(key, desired, a.Fields()) == engine.WriteOnlyHash(key, other, a.Fields()) {
		t.Error("a rotated reviewer JWT must change the write-only hash")
	}
}

func TestKubernetesAuthConfigWriteDropsCompareOnlyKey(t *testing.T) {
	a := KubernetesAuthConfigAdapter{}
	k := fullKubernetesAuthConfig()
	desired, err := a.Desired(context.Background(), builtinResolver{authLitReviewerToken: authLitReviewerJWT}, k)
	if err != nil {
		t.Fatal(err)
	}
	c := &builtinClient{}
	if _, err := a.Write(context.Background(), c, k, desired, false); err != nil {
		t.Fatal(err)
	}
	written := c.writes["auth/kubernetes-dev/config"]
	if _, ok := written[tokenReviewerJWTSet]; ok {
		t.Error("token_reviewer_jwt_set must not be written")
	}
	if written["token_reviewer_jwt"] != authLitReviewerJWT {
		t.Error("the reviewer JWT must be part of every write")
	}
	if _, ok := desired[tokenReviewerJWTSet]; !ok {
		t.Error("Write must not mutate desired")
	}
}

func fullKubernetesAuthRole() *authv1alpha1.KubernetesAuthRole {
	return &authv1alpha1.KubernetesAuthRole{
		ObjectMeta: metav1.ObjectMeta{Name: authTestApp, Namespace: pluginTestNamespace},
		Spec: authv1alpha1.KubernetesAuthRoleSpec{
			MountPath:                     "kubernetes",
			Name:                          "app-reader",
			BoundServiceAccountNames:      []string{authTestApp, authLitWorker},
			BoundServiceAccountNamespaces: []string{pluginTestNamespace},
			BoundServiceAccountNamespaceSelector: &authv1alpha1.NamespaceLabelSelector{
				MatchLabels: map[string]string{"tenant": "a", "env": "dev"},
			},
			Audience:        cloudLitOpenbao,
			AliasNameSource: authv1alpha1.AliasNameSourceServiceAccountName,
			TokenFields: authv1alpha1.TokenFields{
				TokenBaseFields: authv1alpha1.TokenBaseFields{
					TokenExplicitMaxTTL:  builtinDur(24 * time.Hour),
					TokenPeriod:          builtinDur(time.Hour),
					TokenBoundCIDRs:      []string{cloudCIDR, authTestHostCIDR},
					TokenNumUses:         new(int32(5)),
					TokenType:            authv1alpha1.TokenTypeService,
					TokenNoDefaultPolicy: new(true),
				},
				TokenTTL:            builtinDur(time.Hour),
				TokenMaxTTL:         builtinDur(2 * time.Hour),
				TokenPolicies:       []string{authLitRead, authLitWrite},
				TokenStrictlyBindIP: new(false),
			},
		},
	}
}

func TestKubernetesAuthRolePath(t *testing.T) {
	k := fullKubernetesAuthRole()
	if got := (KubernetesAuthRoleAdapter{}).Path(k); got != "auth/kubernetes/role/app-reader" {
		t.Errorf("path = %q", got)
	}
	k.Spec.Name, k.Spec.MountPath = "", "k8s/dev"
	if got := (KubernetesAuthRoleAdapter{}).Path(k); got != "auth/k8s/dev/role/app" {
		t.Errorf("default name path = %q", got)
	}
}

func TestKubernetesAuthRoleDesired(t *testing.T) {
	got, err := KubernetesAuthRoleAdapter{}.Desired(context.Background(), nil, fullKubernetesAuthRole())
	if err != nil {
		t.Fatal(err)
	}
	want := engine.Payload{
		"bound_service_account_names":      []string{authTestApp, authLitWorker},
		"bound_service_account_namespaces": []string{pluginTestNamespace},
		namespaceSelectorKey:               `{"matchLabels":{"env":"dev","tenant":"a"}}`,
		"audience":                         cloudLitOpenbao,
		authKeyAliasNameSource:             "serviceaccount_name",
		keyTokenExplicitMaxTTL:             int64(86400),
		keyTokenPeriod:                     int64(3600),
		keyTokenBoundCIDRs:                 []string{cloudCIDR, authTestHostIP},
		keyTokenNumUses:                    int64(5),
		keyTokenType:                       authLitService,
		keyTokenNoDefaultPolicy:            true,
		keyTokenTTL:                        int64(3600),
		keyTokenMaxTTL:                     int64(7200),
		keyTokenPolicies:                   []string{authLitRead, authLitWrite},
		keyTokenStrictlyBindIP:             false,
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("desired = %v\nwant %v", got, want)
	}

}

func TestKubernetesAuthRoleNeverSendsMatchExpressions(t *testing.T) {
	k := fullKubernetesAuthRole()
	k.Spec.BoundServiceAccountNamespaceSelector.MatchExpressions = []metav1.LabelSelectorRequirement{
		{Key: "env", Operator: metav1.LabelSelectorOpIn, Values: []string{"dev"}},
	}
	got, err := KubernetesAuthRoleAdapter{}.Desired(context.Background(), nil, k)
	if err != nil {
		t.Fatal(err)
	}
	if got[namespaceSelectorKey] != `{"matchLabels":{"env":"dev","tenant":"a"}}` {
		t.Errorf("selector = %v, want matchLabels only", got[namespaceSelectorKey])
	}
}

func TestKubernetesAuthRoleSteadyState(t *testing.T) {
	a := KubernetesAuthRoleAdapter{}
	k := fullKubernetesAuthRole()
	desired, err := a.Desired(context.Background(), nil, k)
	if err != nil {
		t.Fatal(err)
	}
	// read shape captured from OpenBao v2.7.2; the selector was written as YAML with another key order
	stored := map[string]any{
		authKeyAliasNameSource:             "serviceaccount_name",
		"audience":                         cloudLitOpenbao,
		"bound_service_account_names":      []any{authLitWorker, authTestApp},
		namespaceSelectorKey:               "matchLabels:\n  tenant: a\n  env: dev\n",
		"bound_service_account_namespaces": []any{pluginTestNamespace},
		keyTokenBoundCIDRs:                 []any{authTestHostIP, cloudCIDR},
		keyTokenExplicitMaxTTL:             86400,
		keyTokenMaxTTL:                     7200,
		keyTokenNoDefaultPolicy:            true,
		keyTokenNumUses:                    5,
		keyTokenPeriod:                     3600,
		keyTokenPolicies:                   []any{authLitWrite, authLitRead},
		keyTokenStrictlyBindIP:             false,
		keyTokenTTL:                        3600,
		keyTokenType:                       authLitService,
	}
	c := &builtinClient{data: map[string]map[string]any{a.Path(k): stored}}
	observed, found, err := a.Observe(context.Background(), c, k)
	if err != nil || !found {
		t.Fatalf("observe: found=%v err=%v", found, err)
	}
	if d := engine.Diff(desired, observed, a.Fields()); len(d) != 0 {
		t.Errorf("steady state differs on %v", d)
	}

	stored[namespaceSelectorKey] = `{"matchLabels":{"tenant":"b"}}`
	stored[keyTokenTTL] = 60
	observed, _, _ = a.Observe(context.Background(), c, k)
	if d := engine.Diff(desired, observed, a.Fields()); !reflect.DeepEqual(d, []string{namespaceSelectorKey, keyTokenTTL}) {
		t.Errorf("drift: diff = %v", d)
	}
}

func TestKubernetesAuthRoleObserveAbsent(t *testing.T) {
	_, found, err := KubernetesAuthRoleAdapter{}.Observe(context.Background(), &builtinClient{}, fullKubernetesAuthRole())
	if found || err != nil {
		t.Errorf("found=%v err=%v, want absent", found, err)
	}
}

func TestTokenFieldsPayloadAndFieldSpec(t *testing.T) {
	p := engine.Payload{}
	PutTokenFields(p, fullKubernetesAuthRole().Spec.TokenFields)
	for key := range p {
		if !strings.HasPrefix(key, "token_") {
			t.Errorf("unexpected key %s", key)
		}
	}
	if len(p) != 10 {
		t.Errorf("got %d token keys, want all 10", len(p))
	}
	for _, key := range TokenFieldSpec.Durations {
		if _, ok := p[key].(int64); !ok {
			t.Errorf("duration %s = %#v, want int64 seconds", key, p[key])
		}
	}
	for _, key := range TokenFieldSpec.Sets {
		if _, ok := p[key].([]string); !ok {
			t.Errorf("set %s = %#v, want a list", key, p[key])
		}
	}
	empty := engine.Payload{}
	PutTokenFields(empty, authv1alpha1.TokenFields{})
	if len(empty) != 0 {
		t.Errorf("unset token fields must add nothing, got %v", empty)
	}
	if got := hostCIDRsAsIPs([]string{"10.0.0.1/32", "fd00::1/128", cloudCIDR, "1.2.3.4"}); !reflect.DeepEqual(got, []string{"10.0.0.1", "fd00::1", cloudCIDR, "1.2.3.4"}) {
		t.Errorf("hostCIDRsAsIPs = %v", got)
	}
}
