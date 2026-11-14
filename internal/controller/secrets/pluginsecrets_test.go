package secrets

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/yaml"

	commonv1alpha1 "github.com/alexandrust88/openbao-config-operator/api/common/v1alpha1"
	secretsv1alpha1 "github.com/alexandrust88/openbao-config-operator/api/secrets/v1alpha1"
	"github.com/alexandrust88/openbao-config-operator/internal/engine"
	"github.com/alexandrust88/openbao-config-operator/internal/openbao"
)

const (
	pluginTestNamespace = "examples"
	pluginKeyAddress    = "address"
	pluginTeamAdmin     = "admin"
)

var pluginDocSeparator = regexp.MustCompile(`(?m)^---\s*$`)

// pluginFakeResolver serves Secret values keyed by "<name>/<key>" in pluginTestNamespace.
type pluginFakeResolver map[string]string

func (r pluginFakeResolver) SecretValue(_ context.Context, namespace string, ref commonv1alpha1.SecretKeySelector) ([]byte, error) {
	value, ok := r[ref.Name+"/"+ref.Key]
	if !ok || namespace != pluginTestNamespace {
		return nil, fmt.Errorf("secret %s/%s key %s: %w", namespace, ref.Name, ref.Key, engine.ErrReferenceNotReady)
	}
	return []byte(value), nil
}

func (pluginFakeResolver) Object(context.Context, client.ObjectKey, client.Object) error {
	return errors.New("not used")
}

// pluginFakeBao serves reads from data and records writes.
type pluginFakeBao struct {
	openbao.Client
	data    map[string]map[string]any
	readErr error
	writes  map[string]map[string]any
}

func (c *pluginFakeBao) Read(_ context.Context, path string) (map[string]any, error) {
	if c.readErr != nil {
		return nil, c.readErr
	}
	if d, ok := c.data[path]; ok {
		return maps.Clone(d), nil
	}
	return nil, openbao.ErrNotFound
}

func (c *pluginFakeBao) Write(_ context.Context, path string, data map[string]any) (map[string]any, error) {
	if c.writes == nil {
		c.writes = map[string]map[string]any{}
	}
	c.writes[path] = data
	return nil, nil
}

// loadPluginExample decodes examples/secrets/<stem>.yaml into obj and returns the stringData of its Secrets.
func loadPluginExample(t *testing.T, stem string, obj engine.Object) pluginFakeResolver {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "..", "..", "examples", "secrets", stem+".yaml"))
	if err != nil {
		t.Fatal(err)
	}
	secrets := pluginFakeResolver{}
	for _, doc := range pluginDocSeparator.Split(string(raw), -1) {
		var meta metav1.TypeMeta
		if err := yaml.Unmarshal([]byte(doc), &meta); err != nil {
			t.Fatal(err)
		}
		if meta.Kind == "Secret" {
			var s corev1.Secret
			if err := yaml.UnmarshalStrict([]byte(doc), &s); err != nil {
				t.Fatal(err)
			}
			for k, v := range s.StringData {
				secrets[s.Name+"/"+k] = v
			}
			continue
		}
		if err := yaml.UnmarshalStrict([]byte(doc), obj); err != nil {
			t.Fatalf("decode %s: %v", stem, err)
		}
	}
	obj.SetNamespace(pluginTestNamespace)
	return secrets
}

func loadPluginFixture(t *testing.T, name string) map[string]any {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata", "plugins", name))
	if err != nil {
		t.Fatal(err)
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	return out
}

func canonicalPluginJSON(t *testing.T, v any) string {
	t.Helper()
	encoded, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	var generic any
	if err := json.Unmarshal(encoded, &generic); err != nil {
		t.Fatal(err)
	}
	out, _ := json.MarshalIndent(generic, "", "  ")
	return string(out)
}

// testPluginGolden checks a kind against its full example: path, golden payload, no diff against a golden read,
// every managed key reported once changed, secrets only in write-only keys, and Secret rotation changing the hash.
func testPluginGolden[T engine.Object](t *testing.T, stem string, obj T, a engine.Adapter[T], wantPath string, extraReads map[string]string) {
	ctx := context.Background()
	secrets := loadPluginExample(t, stem, obj)

	if got := a.Path(obj); got != wantPath {
		t.Errorf("path = %q, want %q", got, wantPath)
	}
	desired, err := a.Desired(ctx, secrets, obj)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := canonicalPluginJSON(t, desired), canonicalPluginJSON(t, loadPluginFixture(t, stem+".payload.json")); got != want {
		t.Errorf("payload differs from golden\n got: %s\nwant: %s", got, want)
	}

	fields := a.Fields()
	secretValues := slices.Collect(maps.Values(secrets))
	for key, value := range desired {
		if s, ok := value.(string); ok && slices.Contains(secretValues, s) && !slices.Contains(fields.WriteOnly, key) {
			t.Errorf("key %s carries a Secret value but is not write-only", key)
		}
	}

	bao := &pluginFakeBao{data: map[string]map[string]any{wantPath: loadPluginFixture(t, stem+".read.json")}}
	for path, file := range extraReads {
		bao.data[path] = loadPluginFixture(t, file)
	}
	observe := func() engine.Payload {
		t.Helper()
		if o, ok := any(a).(engine.Observer[T]); ok {
			observed, found, err := o.Observe(ctx, bao, obj)
			if err != nil || !found {
				t.Fatalf("observe: found=%v err=%v", found, err)
			}
			return observed
		}
		observed, err := bao.Read(ctx, wantPath)
		if err != nil {
			t.Fatal(err)
		}
		return observed
	}
	if differing := engine.Diff(desired, observe(), fields); len(differing) != 0 {
		t.Errorf("golden read differs on %v", differing)
	}

	for key := range desired {
		if slices.Contains(fields.WriteOnly, key) || slices.Contains(fields.CreateOnly, key) {
			continue
		}
		for path, data := range bao.data {
			if _, ok := data[key]; ok {
				saved := data[key]
				data[key] = "__changed__"
				if differing := engine.Diff(desired, observe(), fields); !slices.Contains(differing, key) {
					t.Errorf("changed %s at %s not reported (differing %v)", key, path, differing)
				}
				data[key] = saved
			}
		}
	}

	if len(fields.WriteOnly) == 0 {
		return
	}
	hash := engine.WriteOnlyHash([]byte("test-key"), desired, fields)
	rotated := pluginFakeResolver{}
	for k, v := range secrets {
		rotated[k] = v + "-rotated"
	}
	rotatedDesired, err := a.Desired(ctx, rotated, obj)
	if err != nil {
		t.Fatal(err)
	}
	if hash == "" || engine.WriteOnlyHash([]byte("test-key"), rotatedDesired, fields) == hash {
		t.Error("write-only hash did not change with the Secret")
	}
	_, err = a.Desired(ctx, pluginFakeResolver{}, obj)
	if !errors.Is(err, engine.ErrReferenceNotReady) {
		t.Errorf("missing Secret: err = %v, want ErrReferenceNotReady", err)
	}
	for _, v := range secretValues {
		if err != nil && strings.Contains(err.Error(), v) {
			t.Error("error leaks a Secret value")
		}
	}
}

func TestPluginGolden(t *testing.T) {
	tests := map[string]func(t *testing.T){
		"githubsecretsconfig": func(t *testing.T) {
			testPluginGolden(t, "githubsecretsconfig", &secretsv1alpha1.GitHubSecretsConfig{}, GitHubSecretsConfigAdapter{}, "github/config", nil)
		},
		"githubsecretsrole": func(t *testing.T) {
			testPluginGolden(t, "githubsecretsrole", &secretsv1alpha1.GitHubSecretsRole{}, GitHubSecretsRoleAdapter{}, "github/permissionset/ci-read-contents", nil)
		},
		"ldapsecretsconfig": func(t *testing.T) {
			testPluginGolden(t, "ldapsecretsconfig", &secretsv1alpha1.LDAPSecretsConfig{}, LDAPSecretsConfigAdapter{}, "ldap/config", nil)
		},
		"ldapsecretsdynamicrole": func(t *testing.T) {
			testPluginGolden(t, "ldapsecretsdynamicrole", &secretsv1alpha1.LDAPSecretsDynamicRole{}, LDAPSecretsDynamicRoleAdapter{}, "ldap/role/app-readers", nil)
		},
		"ldapsecretsstaticrole": func(t *testing.T) {
			testPluginGolden(t, "ldapsecretsstaticrole", &secretsv1alpha1.LDAPSecretsStaticRole{}, LDAPSecretsStaticRoleAdapter{}, "ldap/static-role/backup-service", nil)
		},
		"mongodbatlassecretsconfig": func(t *testing.T) {
			testPluginGolden(t, "mongodbatlassecretsconfig", &secretsv1alpha1.MongoDBAtlasSecretsConfig{}, MongoDBAtlasSecretsConfigAdapter{}, "mongodbatlas/config", nil)
		},
		"mongodbatlassecretsrole": func(t *testing.T) {
			testPluginGolden(t, "mongodbatlassecretsrole", &secretsv1alpha1.MongoDBAtlasSecretsRole{}, MongoDBAtlasSecretsRoleAdapter{}, "mongodbatlas/roles/project-reader", nil)
		},
		"nomadsecretsconfig": func(t *testing.T) {
			testPluginGolden(t, "nomadsecretsconfig", &secretsv1alpha1.NomadSecretsConfig{}, NomadSecretsConfigAdapter{}, "nomad/config/access",
				map[string]string{"nomad/config/lease": "nomadsecretsconfig.lease.read.json"})
		},
		"nomadsecretsrole": func(t *testing.T) {
			testPluginGolden(t, "nomadsecretsrole", &secretsv1alpha1.NomadSecretsRole{}, NomadSecretsRoleAdapter{}, "nomad/role/deployer", nil)
		},
		"quaysecretsconfig": func(t *testing.T) {
			testPluginGolden(t, "quaysecretsconfig", &secretsv1alpha1.QuaySecretsConfig{}, QuaySecretsConfigAdapter{}, "quay/config", nil)
		},
		"quaysecretsrole": func(t *testing.T) {
			testPluginGolden(t, "quaysecretsrole", &secretsv1alpha1.QuaySecretsRole{}, QuaySecretsRoleAdapter{}, "quay/roles/ci-push", nil)
		},
		"quaysecretsstaticrole": func(t *testing.T) {
			testPluginGolden(t, "quaysecretsstaticrole", &secretsv1alpha1.QuaySecretsStaticRole{}, QuaySecretsStaticRoleAdapter{}, "quay/static-roles/mirror-pull", nil)
		},
		"terraformcloudsecretsconfig": func(t *testing.T) {
			testPluginGolden(t, "terraformcloudsecretsconfig", &secretsv1alpha1.TerraformCloudSecretsConfig{}, TerraformCloudSecretsConfigAdapter{}, "terraform/config", nil)
		},
		"terraformcloudsecretsrole": func(t *testing.T) {
			testPluginGolden(t, "terraformcloudsecretsrole", &secretsv1alpha1.TerraformCloudSecretsRole{}, TerraformCloudSecretsRoleAdapter{}, "terraform/role/platform-team", nil)
		},
	}
	for name, run := range tests {
		t.Run(name, run)
	}
}

func TestPluginPathDefaults(t *testing.T) {
	meta := metav1.ObjectMeta{Name: "from-metadata", Namespace: pluginTestNamespace}
	tests := map[string]struct{ got, want string }{
		"github role":      {GitHubSecretsRoleAdapter{}.Path(&secretsv1alpha1.GitHubSecretsRole{ObjectMeta: meta}), "github/permissionset/from-metadata"},
		"ldap static role": {LDAPSecretsStaticRoleAdapter{}.Path(&secretsv1alpha1.LDAPSecretsStaticRole{ObjectMeta: meta}), "ldap/static-role/from-metadata"},
		"atlas role":       {MongoDBAtlasSecretsRoleAdapter{}.Path(&secretsv1alpha1.MongoDBAtlasSecretsRole{ObjectMeta: meta}), "mongodbatlas/roles/from-metadata"},
		"nomad config":     {NomadSecretsConfigAdapter{}.Path(&secretsv1alpha1.NomadSecretsConfig{ObjectMeta: meta}), "nomad/config/access"},
		"quay static role": {QuaySecretsStaticRoleAdapter{}.Path(&secretsv1alpha1.QuaySecretsStaticRole{ObjectMeta: meta}), "quay/static-roles/from-metadata"},
		"terraform role": {TerraformCloudSecretsRoleAdapter{}.Path(&secretsv1alpha1.TerraformCloudSecretsRole{
			ObjectMeta: meta, Spec: secretsv1alpha1.TerraformCloudSecretsRoleSpec{MountPath: "tfc/prod"},
		}), "tfc/prod/role/from-metadata"},
	}
	for name, tt := range tests {
		if tt.got != tt.want {
			t.Errorf("%s: path = %q, want %q", name, tt.got, tt.want)
		}
	}
}

func TestPluginMinimalPayloads(t *testing.T) {
	ctx := context.Background()
	meta := metav1.ObjectMeta{Name: "minimal", Namespace: pluginTestNamespace}
	ref := commonv1alpha1.SecretKeySelector{Name: "s", Key: "k"}
	secrets := pluginFakeResolver{"s/k": "fake"}
	keys := func(p engine.Payload, err error) []string {
		if err != nil {
			t.Fatal(err)
		}
		return slices.Sorted(maps.Keys(p))
	}
	tests := map[string]struct {
		got  []string
		want []string
	}{
		"github role": {keys(GitHubSecretsRoleAdapter{}.Desired(ctx, nil, &secretsv1alpha1.GitHubSecretsRole{
			ObjectMeta: meta, Spec: secretsv1alpha1.GitHubSecretsRoleSpec{OrganizationName: "org"},
		})), []string{"org_name"}},
		"ldap config": {keys(LDAPSecretsConfigAdapter{}.Desired(ctx, secrets, &secretsv1alpha1.LDAPSecretsConfig{
			ObjectMeta: meta, Spec: secretsv1alpha1.LDAPSecretsConfigSpec{BindDN: "cn=x", BindPassSecretRef: ref},
		})), []string{"binddn", "bindpass"}},
		"nomad config": {keys(NomadSecretsConfigAdapter{}.Desired(ctx, secrets, &secretsv1alpha1.NomadSecretsConfig{
			ObjectMeta: meta, Spec: secretsv1alpha1.NomadSecretsConfigSpec{Address: "https://nomad:4646", TokenSecretRef: ref},
		})), []string{pluginKeyAddress, "token"}},
		"nomad management role": {keys(NomadSecretsRoleAdapter{}.Desired(ctx, nil, &secretsv1alpha1.NomadSecretsRole{
			ObjectMeta: meta, Spec: secretsv1alpha1.NomadSecretsRoleSpec{Type: secretsv1alpha1.NomadTokenTypeManagement},
		})), []string{"policies", "type"}},
		"quay static role": {keys(QuaySecretsStaticRoleAdapter{}.Desired(ctx, nil, &secretsv1alpha1.QuaySecretsStaticRole{
			ObjectMeta: meta, Spec: secretsv1alpha1.QuaySecretsStaticRoleSpec{QuayRobotAccount: secretsv1alpha1.QuayRobotAccount{NamespaceName: "org"}},
		})), []string{"namespace_name"}},
		"terraform config": {keys(TerraformCloudSecretsConfigAdapter{}.Desired(ctx, secrets, &secretsv1alpha1.TerraformCloudSecretsConfig{
			ObjectMeta: meta, Spec: secretsv1alpha1.TerraformCloudSecretsConfigSpec{TokenSecretRef: ref},
		})), []string{"token"}},
	}
	for name, tt := range tests {
		if !slices.Equal(tt.got, tt.want) {
			t.Errorf("%s: keys = %v, want %v", name, tt.got, tt.want)
		}
	}
}

func TestNomadSecretsConfigWriteSplitsPaths(t *testing.T) {
	ctx := context.Background()
	cfg := &secretsv1alpha1.NomadSecretsConfig{Spec: secretsv1alpha1.NomadSecretsConfigSpec{MountPath: "nomad-eu"}}
	a := NomadSecretsConfigAdapter{}

	bao := &pluginFakeBao{}
	if _, err := a.Write(ctx, bao, cfg, engine.Payload{pluginKeyAddress: "a", "token": "t", "ttl": int64(60)}, true); err != nil {
		t.Fatal(err)
	}
	if got := canonicalPluginJSON(t, bao.writes); got != canonicalPluginJSON(t, map[string]any{
		"nomad-eu/config/access": map[string]any{pluginKeyAddress: "a", "token": "t"},
		"nomad-eu/config/lease":  map[string]any{"ttl": 60},
	}) {
		t.Errorf("writes = %s", got)
	}

	bao = &pluginFakeBao{}
	if _, err := a.Write(ctx, bao, cfg, engine.Payload{pluginKeyAddress: "a"}, false); err != nil {
		t.Fatal(err)
	}
	if _, ok := bao.writes["nomad-eu/config/lease"]; ok || len(bao.writes) != 1 {
		t.Errorf("lease written without lease keys: %v", slices.Collect(maps.Keys(bao.writes)))
	}
}

func TestNomadSecretsConfigObserve(t *testing.T) {
	ctx := context.Background()
	cfg := &secretsv1alpha1.NomadSecretsConfig{}
	a := NomadSecretsConfigAdapter{}

	if _, found, err := a.Observe(ctx, &pluginFakeBao{}, cfg); found || err != nil {
		t.Errorf("absent access: found=%v err=%v", found, err)
	}
	observed, found, err := a.Observe(ctx, &pluginFakeBao{data: map[string]map[string]any{
		"nomad/config/access": {pluginKeyAddress: "a"},
	}}, cfg)
	if !found || err != nil || len(observed) != 1 {
		t.Errorf("no lease config: observed=%v found=%v err=%v", observed, found, err)
	}
	denied := &openbao.ResponseError{StatusCode: 403}
	if _, _, err := a.Observe(ctx, &pluginFakeBao{readErr: denied}, cfg); !errors.Is(err, denied) {
		t.Errorf("403 must be returned, got %v", err)
	}
}

func TestQuayObserveNormalisesJSON(t *testing.T) {
	ctx := context.Background()
	role := &secretsv1alpha1.QuaySecretsRole{
		ObjectMeta: metav1.ObjectMeta{Name: "r"},
		Spec: secretsv1alpha1.QuaySecretsRoleSpec{QuayRobotAccount: secretsv1alpha1.QuayRobotAccount{
			NamespaceName: "org",
			Teams:         map[string]secretsv1alpha1.QuayTeamRole{"b": "member", "a": pluginTeamAdmin},
		}},
	}
	a := QuaySecretsRoleAdapter{}
	desired, err := a.Desired(ctx, nil, role)
	if err != nil {
		t.Fatal(err)
	}
	tests := map[string]struct {
		teams  any
		inSync bool
	}{
		"object":             {teams: map[string]any{"a": pluginTeamAdmin, "b": "member"}, inSync: true},
		"spaced json string": {teams: `{ "b": "member", "a": "admin" }`, inSync: true},
		"changed role":       {teams: map[string]any{"a": "member", "b": "member"}, inSync: false},
		"extra team":         {teams: map[string]any{"a": pluginTeamAdmin, "b": "member", "c": "member"}, inSync: false},
		"absent":             {teams: nil, inSync: false},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			data := map[string]any{"namespace_name": "org"}
			if tt.teams != nil {
				data["teams"] = tt.teams
			}
			observed, found, err := a.Observe(ctx, &pluginFakeBao{data: map[string]map[string]any{"quay/roles/r": data}}, role)
			if err != nil || !found {
				t.Fatalf("found=%v err=%v", found, err)
			}
			differing := engine.Diff(desired, observed, a.Fields())
			if inSync := len(differing) == 0; inSync != tt.inSync {
				t.Errorf("in sync = %v, want %v (differing %v)", inSync, tt.inSync, differing)
			}
		})
	}
	if _, found, err := a.Observe(ctx, &pluginFakeBao{}, role); found || err != nil {
		t.Errorf("absent role: found=%v err=%v", found, err)
	}
}

func TestLDAPSecretsStaticRoleSkipImportRotationIsCreateOnly(t *testing.T) {
	role := &secretsv1alpha1.LDAPSecretsStaticRole{
		ObjectMeta: metav1.ObjectMeta{Name: "r"},
		Spec: secretsv1alpha1.LDAPSecretsStaticRoleSpec{
			Username: "svc", RotationPeriod: metav1.Duration{Duration: time.Hour}, SkipImportRotation: new(true),
		},
	}
	a := LDAPSecretsStaticRoleAdapter{}
	desired, err := a.Desired(context.Background(), nil, role)
	if err != nil {
		t.Fatal(err)
	}
	observed := map[string]any{"dn": "", "username": "svc", "rotation_period": float64(3600)}
	if differing := engine.Diff(desired, observed, a.Fields()); len(differing) != 0 {
		t.Errorf("create-only key compared: %v", differing)
	}
	if _, ok := engine.StripCreateOnly(desired, a.Fields())["skip_import_rotation"]; ok {
		t.Error("skip_import_rotation sent on update, which the plugin refuses")
	}
}

func TestGitHubSecretsConfigObserve(t *testing.T) {
	ctx := context.Background()
	cfg := &secretsv1alpha1.GitHubSecretsConfig{}
	a := GitHubSecretsConfigAdapter{}
	defaults := map[string]any{"app_id": 0, "base_url": "https://api.github.com/", "exclude_repository_metadata": false, "prv_key": ""}
	tests := map[string]struct {
		bao       *pluginFakeBao
		wantFound bool
		wantErr   bool
	}{
		"not configured": {bao: &pluginFakeBao{data: map[string]map[string]any{"github/config": defaults}}},
		"configured": {bao: &pluginFakeBao{data: map[string]map[string]any{
			"github/config": {"app_id": 1, "base_url": "https://api.github.com/", "prv_key": "<configured>"},
		}}, wantFound: true},
		"missing mount": {bao: &pluginFakeBao{}},
		"denied":        {bao: &pluginFakeBao{readErr: &openbao.ResponseError{StatusCode: 403}}, wantErr: true},
	}
	for name, tt := range tests {
		_, found, err := a.Observe(ctx, tt.bao, cfg)
		if found != tt.wantFound || (err != nil) != tt.wantErr {
			t.Errorf("%s: found=%v err=%v", name, found, err)
		}
	}
}

func TestTerraformCloudSecretsRoleObserveOrganizationType(t *testing.T) {
	ctx := context.Background()
	role := &secretsv1alpha1.TerraformCloudSecretsRole{
		ObjectMeta: metav1.ObjectMeta{Name: "org"},
		Spec: secretsv1alpha1.TerraformCloudSecretsRoleSpec{
			Organization: "example-org", CredentialType: secretsv1alpha1.TerraformCloudCredentialTypeOrganization,
		},
	}
	a := TerraformCloudSecretsRoleAdapter{}
	desired, err := a.Desired(ctx, nil, role)
	if err != nil {
		t.Fatal(err)
	}
	read := func(data map[string]any) engine.Payload {
		t.Helper()
		observed, found, err := a.Observe(ctx, &pluginFakeBao{data: map[string]map[string]any{"terraform/role/org": data}}, role)
		if err != nil || !found {
			t.Fatalf("found=%v err=%v", found, err)
		}
		return observed
	}
	if differing := engine.Diff(desired, read(map[string]any{
		"name": "org", "organization": "example-org", "ttl": 0, "max_ttl": 0,
	}), a.Fields()); len(differing) != 0 {
		t.Errorf("organization role differs on %v", differing)
	}
	if differing := engine.Diff(desired, read(map[string]any{
		"name": "org", "organization": "example-org", "team_id": "team-x", "credential_type": "team", "ttl": 0, "max_ttl": 0,
	}), a.Fields()); !slices.Contains(differing, "credential_type") {
		t.Errorf("team credential type not reported, differing %v", differing)
	}
	if _, found, err := a.Observe(ctx, &pluginFakeBao{}, role); found || err != nil {
		t.Errorf("absent role: found=%v err=%v", found, err)
	}
}

func TestNomadSecretsRoleManagementClearsPolicies(t *testing.T) {
	role := &secretsv1alpha1.NomadSecretsRole{
		ObjectMeta: metav1.ObjectMeta{Name: "ops"},
		Spec:       secretsv1alpha1.NomadSecretsRoleSpec{Type: secretsv1alpha1.NomadTokenTypeManagement},
	}
	a := NomadSecretsRoleAdapter{}
	desired, err := a.Desired(context.Background(), nil, role)
	if err != nil {
		t.Fatal(err)
	}
	if policies, ok := desired["policies"].([]string); !ok || len(policies) != 0 {
		t.Errorf("policies = %#v, want an empty list", desired["policies"])
	}
	observed := map[string]any{"type": "management", "global": false, "policies": []any{}}
	if differing := engine.Diff(desired, observed, a.Fields()); len(differing) != 0 {
		t.Errorf("management role differs on %v", differing)
	}
}

func TestPluginSecretRefs(t *testing.T) {
	ref := func(name string) commonv1alpha1.SecretKeySelector {
		return commonv1alpha1.SecretKeySelector{Name: name, Key: "k"}
	}
	refPtr := func(name string) *commonv1alpha1.SecretKeySelector { r := ref(name); return &r }
	tests := map[string]struct {
		got  []string
		want []string
	}{
		"ldap": {LDAPSecretsConfigAdapter{}.SecretRefs(&secretsv1alpha1.LDAPSecretsConfig{Spec: secretsv1alpha1.LDAPSecretsConfigSpec{
			BindPassSecretRef: ref("bind"), ClientTLSKeySecretRef: refPtr("tls"),
		}}), []string{"bind", "tls"}},
		"nomad token only": {NomadSecretsConfigAdapter{}.SecretRefs(&secretsv1alpha1.NomadSecretsConfig{Spec: secretsv1alpha1.NomadSecretsConfigSpec{
			TokenSecretRef: ref("token"),
		}}), []string{"token"}},
		"nomad": {NomadSecretsConfigAdapter{}.SecretRefs(&secretsv1alpha1.NomadSecretsConfig{Spec: secretsv1alpha1.NomadSecretsConfigSpec{
			TokenSecretRef: ref("token"), ClientKeySecretRef: refPtr("key"),
		}}), []string{"token", "key"}},
		"github": {GitHubSecretsConfigAdapter{}.SecretRefs(&secretsv1alpha1.GitHubSecretsConfig{Spec: secretsv1alpha1.GitHubSecretsConfigSpec{
			PrivateKeySecretRef: ref("app"),
		}}), []string{"app"}},
	}
	for name, tt := range tests {
		if !slices.Equal(tt.got, tt.want) {
			t.Errorf("%s: refs = %v, want %v", name, tt.got, tt.want)
		}
	}
}
