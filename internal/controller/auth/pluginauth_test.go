package auth

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"maps"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/yaml"

	authv1alpha1 "github.com/alexandrust88/openbao-config-operator/api/auth/v1alpha1"
	commonv1alpha1 "github.com/alexandrust88/openbao-config-operator/api/common/v1alpha1"
	sysv1alpha1 "github.com/alexandrust88/openbao-config-operator/api/sys/v1alpha1"
	"github.com/alexandrust88/openbao-config-operator/internal/engine"
	"github.com/alexandrust88/openbao-config-operator/internal/openbao"
)

const (
	pluginTestNamespace = "team"
	pluginSysAuth       = "sys/auth"
)

// pluginBao serves reads from data; sys/auth lists the auth mounts in mounts.
type pluginBao struct {
	openbao.Client
	data   map[string]map[string]any
	mounts []string
}

func (b pluginBao) Read(_ context.Context, path string) (map[string]any, error) {
	if path == pluginSysAuth {
		listed := map[string]any{}
		for _, m := range b.mounts {
			listed[m+"/"] = map[string]any{}
		}
		return listed, nil
	}
	if d, ok := b.data[path]; ok {
		return maps.Clone(d), nil
	}
	return nil, openbao.ErrNotFound
}

// pluginCase is one external-plugin auth kind: the full spec comes from its examples/auth manifest, the expected
// path and payload from testdata/<kind>.golden.json, and the server's read shape from testdata/<kind>.read.json.
type pluginCase struct {
	kind       string
	path       string
	desired    func(engine.Resolver) (engine.Payload, error)
	observe    func(openbao.Client) (engine.Payload, bool, error)
	fields     engine.FieldSpec
	secretRefs []string
	secrets    []client.Object
	// connectionRef is the example's connection, which the referenced Policy shares.
	connectionRef commonv1alpha1.ConnectionReference
	golden        struct {
		Path    string         `json:"path"`
		Payload map[string]any `json:"payload"`
	}
	read struct {
		Data   map[string]any `json:"data"`
		Tamper string         `json:"tamper"`
	}
}

func (c pluginCase) mount() string { return strings.Split(c.path, "/")[1] }

// resolver serves the example's Secrets and the Ready Policy that the examples reference in policyRefs.
func (c pluginCase) resolver(withSecrets bool) engine.Resolver {
	scheme := runtime.NewScheme()
	if err := clientgoscheme.AddToScheme(scheme); err != nil {
		panic(err)
	}
	if err := sysv1alpha1.AddToScheme(scheme); err != nil {
		panic(err)
	}
	policy := &sysv1alpha1.Policy{ObjectMeta: metav1.ObjectMeta{Name: "obco-team-a-reader", Namespace: pluginTestNamespace}}
	policy.Spec.ConnectionRef = c.connectionRef
	policy.Spec.Name = "team-a-reader"
	policy.Status.Conditions = []metav1.Condition{{Type: commonv1alpha1.ConditionReady, Status: metav1.ConditionTrue}}
	builder := fake.NewClientBuilder().WithScheme(scheme).WithObjects(policy)
	if withSecrets {
		builder = builder.WithObjects(c.secrets...)
	}
	return engine.NewResolver(builder.Build())
}

// isSecret reports whether s is a Secret value of the example, raw or base64-encoded (the Kerberos keytab).
func (c pluginCase) isSecret(s string) bool {
	for _, obj := range c.secrets {
		for _, v := range obj.(*corev1.Secret).Data {
			if s == string(v) || s == base64.StdEncoding.EncodeToString(v) {
				return true
			}
		}
	}
	return false
}

func loadPluginCase[T engine.Object](t *testing.T, kind string, a engine.Adapter[T], obj T) pluginCase {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "..", "..", "examples", "auth", kind+".yaml"))
	if err != nil {
		t.Fatal(err)
	}
	c := pluginCase{kind: kind}
	for doc := range bytes.SplitSeq(raw, []byte("\n---\n")) {
		doc = append(slices.Clone(doc), '\n')
		if bytes.Contains(doc, []byte("\nkind: Secret\n")) {
			secret := &corev1.Secret{}
			if err := yaml.UnmarshalStrict(doc, secret); err != nil {
				t.Fatal(err)
			}
			secret.Namespace = pluginTestNamespace
			secret.Data = map[string][]byte{}
			for k, v := range secret.StringData {
				secret.Data[k] = []byte(v)
			}
			c.secrets = append(c.secrets, secret)
			continue
		}
		if err := yaml.UnmarshalStrict(doc, obj); err != nil {
			t.Fatal(err)
		}
	}
	obj.SetNamespace(pluginTestNamespace)
	pluginReadJSON(t, kind+".golden.json", &c.golden)
	pluginReadJSON(t, kind+".read.json", &c.read)

	c.path = a.Path(obj)
	c.connectionRef = obj.GetResourceSpec().ConnectionRef
	c.desired = func(r engine.Resolver) (engine.Payload, error) { return a.Desired(context.Background(), r, obj) }
	c.fields = a.Fields()
	o, ok := any(a).(engine.Observer[T])
	if !ok {
		t.Fatalf("%s: adapter has no Observer", kind)
	}
	c.observe = func(cl openbao.Client) (engine.Payload, bool, error) { return o.Observe(context.Background(), cl, obj) }
	if s, ok := any(a).(engine.SecretReferencer[T]); ok {
		c.secretRefs = s.SecretRefs(obj)
	}
	return c
}

// pluginReadJSON decodes numbers as json.Number, as the OpenBao client does.
func pluginReadJSON(t *testing.T, name string, into any) {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if err := decoder.Decode(into); err != nil {
		t.Fatalf("%s: %v", name, err)
	}
}

func pluginCases(t *testing.T) []pluginCase {
	t.Helper()
	return []pluginCase{
		loadPluginCase(t, "githubauthconfig", GitHubAuthConfigAdapter{}, &authv1alpha1.GitHubAuthConfig{}),
		loadPluginCase(t, "githubauthteam", GitHubAuthTeamAdapter{}, &authv1alpha1.GitHubAuthTeam{}),
		loadPluginCase(t, "githubauthuser", GitHubAuthUserAdapter{}, &authv1alpha1.GitHubAuthUser{}),
		loadPluginCase(t, "kerberosauthconfig", KerberosAuthConfigAdapter{}, &authv1alpha1.KerberosAuthConfig{}),
		loadPluginCase(t, "kerberosauthldapconfig", KerberosAuthLDAPConfigAdapter{}, &authv1alpha1.KerberosAuthLDAPConfig{}),
		loadPluginCase(t, "kerberosauthgroup", KerberosAuthGroupAdapter{}, &authv1alpha1.KerberosAuthGroup{}),
		loadPluginCase(t, "ldapauthconfig", LDAPAuthConfigAdapter{}, &authv1alpha1.LDAPAuthConfig{}),
		loadPluginCase(t, "ldapauthgroup", LDAPAuthGroupAdapter{}, &authv1alpha1.LDAPAuthGroup{}),
		loadPluginCase(t, "ldapauthuser", LDAPAuthUserAdapter{}, &authv1alpha1.LDAPAuthUser{}),
		loadPluginCase(t, "oktaauthconfig", OktaAuthConfigAdapter{}, &authv1alpha1.OktaAuthConfig{}),
		loadPluginCase(t, "oktaauthgroup", OktaAuthGroupAdapter{}, &authv1alpha1.OktaAuthGroup{}),
		loadPluginCase(t, "oktaauthuser", OktaAuthUserAdapter{}, &authv1alpha1.OktaAuthUser{}),
		loadPluginCase(t, "radiusauthconfig", RADIUSAuthConfigAdapter{}, &authv1alpha1.RADIUSAuthConfig{}),
		loadPluginCase(t, "radiusauthuser", RADIUSAuthUserAdapter{}, &authv1alpha1.RADIUSAuthUser{}),
	}
}

func pluginCanonical(t *testing.T, v any) any {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	var out any
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func TestPluginAuthGoldenPayload(t *testing.T) {
	for _, tc := range pluginCases(t) {
		t.Run(tc.kind, func(t *testing.T) {
			if tc.path != tc.golden.Path {
				t.Errorf("path = %q, want %q", tc.path, tc.golden.Path)
			}
			desired, err := tc.desired(tc.resolver(true))
			if err != nil {
				t.Fatal(err)
			}
			if got, want := pluginCanonical(t, desired), pluginCanonical(t, tc.golden.Payload); !reflect.DeepEqual(got, want) {
				gotJSON, _ := json.MarshalIndent(got, "", "  ")
				t.Errorf("desired payload differs from golden:\n%s", gotJSON)
			}
		})
	}
}

func TestPluginAuthSteadyState(t *testing.T) {
	for _, tc := range pluginCases(t) {
		t.Run(tc.kind, func(t *testing.T) {
			desired, err := tc.desired(tc.resolver(true))
			if err != nil {
				t.Fatal(err)
			}
			bao := pluginBao{data: map[string]map[string]any{tc.path: tc.read.Data}, mounts: []string{tc.mount()}}
			observed, found, err := tc.observe(bao)
			if err != nil || !found {
				t.Fatalf("found=%v err=%v", found, err)
			}
			if differing := engine.Diff(desired, observed, tc.fields); len(differing) != 0 {
				t.Errorf("golden read differs on %v", differing)
			}

			tampered := maps.Clone(tc.read.Data)
			tampered[tc.read.Tamper] = "tampered"
			bao.data[tc.path] = tampered
			if observed, _, err = tc.observe(bao); err != nil {
				t.Fatal(err)
			}
			if differing := engine.Diff(desired, observed, tc.fields); !slices.Equal(differing, []string{tc.read.Tamper}) {
				t.Errorf("tampered %s: differing = %v", tc.read.Tamper, differing)
			}
		})
	}
}

func TestPluginAuthObserveAbsent(t *testing.T) {
	for _, tc := range pluginCases(t) {
		t.Run(tc.kind, func(t *testing.T) {
			if _, found, err := tc.observe(pluginBao{mounts: []string{tc.mount()}}); err != nil || found {
				t.Errorf("mount enabled: found=%v err=%v, want absent", found, err)
			}
			if _, found, err := tc.observe(pluginBao{}); found || !errors.Is(err, ErrAuthMountMissing) {
				t.Errorf("mount missing: found=%v err=%v, want ErrAuthMountMissing", found, err)
			}
		})
	}
}

// TestPluginAuthSecrets checks every secret input: it is referenced for the Secret watch, write-only and hashed,
// and a missing Secret waits as ErrReferenceNotReady without quoting any value.
func TestPluginAuthSecrets(t *testing.T) {
	for _, tc := range pluginCases(t) {
		t.Run(tc.kind, func(t *testing.T) {
			var names []string
			for _, s := range tc.secrets {
				names = append(names, s.GetName())
			}
			if refs := slices.Compact(slices.Sorted(slices.Values(tc.secretRefs))); !slices.Equal(refs, names) {
				t.Errorf("secret refs = %v, want the example's Secrets %v", refs, names)
			}
			if len(tc.secrets) == 0 {
				return
			}
			desired, err := tc.desired(tc.resolver(true))
			if err != nil {
				t.Fatal(err)
			}
			var secretKeys []string
			for key, value := range desired {
				if s, ok := value.(string); ok && tc.isSecret(s) {
					secretKeys = append(secretKeys, key)
				}
			}
			if len(secretKeys) == 0 {
				t.Fatal("no secret value in the payload")
			}
			hashKey := []byte("test-key")
			before := engine.WriteOnlyHash(hashKey, desired, tc.fields)
			for _, key := range secretKeys {
				if !slices.Contains(tc.fields.WriteOnly, key) {
					t.Errorf("%s holds a secret but is not write-only", key)
				}
				rotated := maps.Clone(desired)
				rotated[key] = "rotated"
				if before == "" || engine.WriteOnlyHash(hashKey, rotated, tc.fields) == before {
					t.Errorf("hash does not follow %s", key)
				}
			}

			_, err = tc.desired(tc.resolver(false))
			if !errors.Is(err, engine.ErrReferenceNotReady) {
				t.Fatalf("missing Secret: err = %v, want ErrReferenceNotReady", err)
			}
			for key := range desired {
				if s, ok := desired[key].(string); ok && tc.isSecret(s) && strings.Contains(err.Error(), s) {
					t.Errorf("error quotes the value of %s", key)
				}
			}
		})
	}
}

func TestPluginAuthUnsetFieldsOmitted(t *testing.T) {
	ldapGroup := &authv1alpha1.LDAPAuthGroup{}
	ldapGroup.Name = "ops"
	if got := (LDAPAuthGroupAdapter{}).Path(ldapGroup); got != "auth/ldap/groups/ops" {
		t.Errorf("default mount and name: %q", got)
	}
	okta := &authv1alpha1.OktaAuthConfig{Spec: authv1alpha1.OktaAuthConfigSpec{OrgName: "example"}}
	desired, err := (OktaAuthConfigAdapter{}).Desired(context.Background(), nil, okta)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(desired, engine.Payload{"org_name": "example"}) {
		t.Errorf("unset fields must stay out of the payload: %v", desired)
	}
	desired, _ = (GitHubAuthTeamAdapter{}).Desired(context.Background(), nil, &authv1alpha1.GitHubAuthTeam{})
	if !reflect.DeepEqual(desired, engine.Payload{"value": ""}) {
		t.Errorf("team without policies: %v", desired)
	}
}

func TestPluginSplitComma(t *testing.T) {
	if got := pluginSplitComma(" a, b,,c "); !slices.Equal(got, []string{"a", "b", "c"}) {
		t.Errorf("split = %v", got)
	}
	if got := pluginSplitComma(""); got != nil {
		t.Errorf("empty = %v", got)
	}
}
