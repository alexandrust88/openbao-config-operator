package sys

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"slices"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	commonv1alpha1 "github.com/alexandrust88/openbao-config-operator/api/common/v1alpha1"

	sysv1alpha1 "github.com/alexandrust88/openbao-config-operator/api/sys/v1alpha1"
	"github.com/alexandrust88/openbao-config-operator/internal/engine"
	"github.com/alexandrust88/openbao-config-operator/internal/openbao"
)

type recordedWrite struct {
	path string
	data map[string]any
}

// recordingBao serves reads from a map and records writes and deletes.
type recordingBao struct {
	openbao.Client
	reads     map[string]map[string]any
	readErr   error
	deleteErr error
	namespace string
	writes    []recordedWrite
	deletes   []string
}

func (f *recordingBao) Read(_ context.Context, path string) (map[string]any, error) {
	if f.readErr != nil {
		return nil, f.readErr
	}
	if d, ok := f.reads[path]; ok {
		return d, nil
	}
	return nil, openbao.ErrNotFound
}

func (f *recordingBao) Namespace() string { return f.namespace }

func (f *recordingBao) Write(_ context.Context, path string, data map[string]any) (map[string]any, error) {
	f.writes = append(f.writes, recordedWrite{path: path, data: data})
	return nil, nil
}

func (f *recordingBao) Delete(_ context.Context, path string) error {
	f.deletes = append(f.deletes, path)
	return f.deleteErr
}

// decode parses a golden read the way the OpenBao client does, with numbers as json.Number.
func decodeRead(t *testing.T, raw string) map[string]any {
	t.Helper()
	d := json.NewDecoder(bytes.NewBufferString(raw))
	d.UseNumber()
	var out map[string]any
	if err := d.Decode(&out); err != nil {
		t.Fatal(err)
	}
	return out
}

func metaDuration(d time.Duration) *metav1.Duration { return &metav1.Duration{Duration: d} }

// goldenAuthTable is GET sys/auth from bao server -dev 2.7.2 after enabling and tuning userpass at up/.
const goldenAuthTable = `{
  "token/": {"accessor": "auth_token_1", "config": {"default_lease_ttl": 0, "max_lease_ttl": 0, "force_no_cache": false, "token_type": "default-service"}, "type": "ns_token", "description": "token based credentials", "local": false, "seal_wrap": false, "external_entropy_access": false, "options": null, "plugin_version": "", "running_plugin_version": "v2.7.2+builtin.bao", "running_sha256": "", "uuid": "u1"},
  "up/": {
    "accessor": "auth_userpass_cb7a8dad",
    "config": {
      "allowed_response_headers": ["X-B"],
      "audit_non_hmac_request_keys": ["a", "b"],
      "default_lease_ttl": 3600,
      "force_no_cache": false,
      "listing_visibility": "unauth",
      "max_lease_ttl": 7200,
      "passthrough_request_headers": ["X-A"],
      "token_type": "batch",
      "user_lockout_config": {
        "user_lockout_counter_reset_duration": 600,
        "user_lockout_disable": false,
        "user_lockout_duration": 900,
        "user_lockout_threshold": 5
      }
    },
    "deprecation_status": "supported",
    "description": "d",
    "external_entropy_access": false,
    "local": false,
    "options": null,
    "plugin_version": "",
    "running_plugin_version": "v2.7.2+builtin.bao",
    "running_sha256": "",
    "seal_wrap": false,
    "type": "userpass",
    "uuid": "7f3d4223-c0f0-d4b3-76d5-ec8a4e9ce01c"
  }
}`

// goldenMountsTable is GET sys/mounts from bao server -dev 2.7.2 after `bao secrets enable -path=kv2 kv-v2`.
const goldenMountsTable = `{
  "kv2/": {
    "accessor": "kv_2481bc10",
    "config": {"default_lease_ttl": 0, "force_no_cache": false, "max_lease_ttl": 0},
    "deprecation_status": "supported",
    "description": "",
    "external_entropy_access": false,
    "local": false,
    "options": {"version": "2"},
    "plugin_version": "",
    "running_plugin_version": "v2.7.2+builtin.bao",
    "running_sha256": "",
    "seal_wrap": false,
    "type": "kv",
    "uuid": "ea29f325-0337-b403-458a-e465ae3b3d65"
  }
}`

func fullAuthMount() *sysv1alpha1.AuthMount {
	return &sysv1alpha1.AuthMount{
		ObjectMeta: metav1.ObjectMeta{Name: litUserpass, Namespace: testNamespace},
		Spec: sysv1alpha1.AuthMountSpec{
			Path: "up",
			Type: litUserpass,
			MountFields: sysv1alpha1.MountFields{
				Description:               new("d"),
				Local:                     new(false),
				SealWrap:                  new(false),
				ExternalEntropyAccess:     new(false),
				ForceNoCache:              new(false),
				DefaultLeaseTTL:           metaDuration(time.Hour),
				MaxLeaseTTL:               metaDuration(2 * time.Hour),
				ListingVisibility:         sysv1alpha1.ListingVisibilityUnauth,
				AuditNonHMACRequestKeys:   []string{"b", "a"},
				PassthroughRequestHeaders: []string{"X-A"},
				AllowedResponseHeaders:    []string{"X-B"},
			},
			TokenType: sysv1alpha1.MountTokenTypeBatch,
			UserLockout: &sysv1alpha1.UserLockoutConfig{
				Threshold:            ptr.To[int32](5),
				Duration:             metaDuration(15 * time.Minute),
				CounterResetDuration: metaDuration(10 * time.Minute),
				Disable:              new(false),
			},
		},
	}
}

func TestAuthMountPath(t *testing.T) {
	m := fullAuthMount()
	if got := (AuthMountAdapter{}).Path(m); got != "sys/auth/up" {
		t.Errorf("spec path: got %q", got)
	}
	m.Spec.Path = ""
	if got := (AuthMountAdapter{}).Path(m); got != "sys/auth/userpass" {
		t.Errorf("default path: got %q", got)
	}
}

func TestAuthMountDesired(t *testing.T) {
	got, err := AuthMountAdapter{}.Desired(context.Background(), nil, fullAuthMount())
	if err != nil {
		t.Fatal(err)
	}
	want := engine.Payload{
		keyType:                               litUserpass,
		litDescription:                        "d",
		"local":                               false,
		"seal_wrap":                           false,
		"external_entropy_access":             false,
		keyForceNoCache:                       false,
		"default_lease_ttl":                   int64(3600),
		"max_lease_ttl":                       int64(7200),
		"listing_visibility":                  "unauth",
		"audit_non_hmac_request_keys":         []string{"b", "a"},
		"audit_non_hmac_response_keys":        []string{},
		"passthrough_request_headers":         []string{"X-A"},
		"allowed_response_headers":            []string{"X-B"},
		"token_type":                          "batch",
		"user_lockout_threshold":              int64(5),
		litUserLockoutDuration:                int64(900),
		"user_lockout_counter_reset_duration": int64(600),
		"user_lockout_disable":                false,
		"options":                             map[string]string{},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("desired =\n%v\nwant\n%v", got, want)
	}
	assertFieldSpecCovers(t, got, mountFieldSpec, "user_lockout_threshold")

	minimal := &sysv1alpha1.AuthMount{
		ObjectMeta: metav1.ObjectMeta{Name: litKubernetes},
		Spec:       sysv1alpha1.AuthMountSpec{Type: litKubernetes},
	}
	got, _ = AuthMountAdapter{}.Desired(context.Background(), nil, minimal)
	want = engine.Payload{
		keyType: litKubernetes, litDescription: "", "local": false, "seal_wrap": false, "external_entropy_access": false,
		keyForceNoCache: false, "default_lease_ttl": int64(0), "max_lease_ttl": int64(0), keyListingVisibility: "",
		"audit_non_hmac_request_keys": []string{}, "audit_non_hmac_response_keys": []string{},
		"passthrough_request_headers": []string{}, "allowed_response_headers": []string{}, "token_type": "default-service",
		"options": map[string]string{},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("unset fields must carry the OpenBao defaults, got %v", got)
	}
}

// assertFieldSpecCovers checks that every int64 key is a duration or a known count and every list is a set.
func assertFieldSpecCovers(t *testing.T, p engine.Payload, spec engine.FieldSpec, counts ...string) {
	t.Helper()
	for key, value := range p {
		switch value.(type) {
		case int64:
			if !slices.Contains(spec.Durations, key) && !slices.Contains(counts, key) {
				t.Errorf("%s is an int64 but not in FieldSpec.Durations", key)
			}
		case []string:
			if !slices.Contains(spec.Sets, key) {
				t.Errorf("%s is a list but not in FieldSpec.Sets", key)
			}
		}
	}
}

func TestAuthMountObserveGolden(t *testing.T) {
	tests := []struct {
		name      string
		mutate    func(entry map[string]any)
		differing []string
	}{
		{name: "in sync", mutate: func(map[string]any) {}},
		{name: "description changed", mutate: func(e map[string]any) { e[litDescription] = "other" }, differing: []string{litDescription}},
		{name: "tune ttl changed", mutate: func(e map[string]any) {
			e["config"].(map[string]any)["default_lease_ttl"] = json.Number("60")
		}, differing: []string{"default_lease_ttl"}},
		{name: "lockout changed", mutate: func(e map[string]any) {
			e["config"].(map[string]any)["user_lockout_config"].(map[string]any)[litUserLockoutDuration] = json.Number("60")
		}, differing: []string{litUserLockoutDuration}},
		{name: "set element removed", mutate: func(e map[string]any) {
			e["config"].(map[string]any)["audit_non_hmac_request_keys"] = []any{"a"}
		}, differing: []string{"audit_non_hmac_request_keys"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			table := decodeRead(t, goldenAuthTable)
			tt.mutate(table["up/"].(map[string]any))
			c := &recordingBao{reads: map[string]map[string]any{litSysAuth: table}}
			a := AuthMountAdapter{}
			m := fullAuthMount()
			observed, found, err := a.Observe(context.Background(), c, m)
			if err != nil || !found {
				t.Fatalf("found=%v err=%v", found, err)
			}
			desired, _ := a.Desired(context.Background(), nil, m)
			if got := engine.Diff(desired, observed, a.Fields()); !slices.Equal(got, tt.differing) {
				t.Errorf("differing = %v, want %v", got, tt.differing)
			}
		})
	}
}

func TestAuthMountObserveAbsent(t *testing.T) {
	a := AuthMountAdapter{}
	m := fullAuthMount()
	m.Spec.Path = "missing"
	c := &recordingBao{reads: map[string]map[string]any{litSysAuth: decodeRead(t, goldenAuthTable)}}
	if _, found, err := a.Observe(context.Background(), c, m); err != nil || found {
		t.Errorf("missing key: found=%v err=%v", found, err)
	}
	if _, found, err := a.Observe(context.Background(), &recordingBao{}, m); err != nil || found {
		t.Errorf("empty table: found=%v err=%v", found, err)
	}
	denied := &openbao.ResponseError{StatusCode: 403, Errors: []string{"permission denied"}}
	if _, _, err := a.Observe(context.Background(), &recordingBao{readErr: denied}, m); !errors.Is(err, denied) {
		t.Errorf("403 must be returned, got %v", err)
	}
}

// TestAuthMountObserveCreateOnlyChange checks that Observe reports a create-only difference instead of failing,
// so the engine can guard the recreate of this destructive kind.
func TestAuthMountObserveCreateOnlyChange(t *testing.T) {
	m := fullAuthMount()
	m.Spec.Type = "approle"
	m.Spec.ForceNoCache = new(true)
	c := &recordingBao{reads: map[string]map[string]any{litSysAuth: decodeRead(t, goldenAuthTable)}}
	a := AuthMountAdapter{}
	observed, found, err := a.Observe(context.Background(), c, m)
	if err != nil || !found {
		t.Fatalf("found=%v err=%v", found, err)
	}
	desired, _ := a.Desired(context.Background(), nil, m)
	createOnly := engine.Payload{keyType: desired[keyType], keyForceNoCache: desired[keyForceNoCache]}
	if got := engine.Diff(createOnly, observed, engine.FieldSpec{}); !slices.Equal(got, []string{keyForceNoCache, keyType}) {
		t.Errorf("create-only differences = %v, want force_no_cache and type", got)
	}
}

func TestAuthMountWrite(t *testing.T) {
	a := AuthMountAdapter{}
	m := fullAuthMount()
	m.Spec.PluginVersion = litV100
	desired, _ := a.Desired(context.Background(), nil, m)

	c := &recordingBao{}
	if _, err := a.Write(context.Background(), c, m, desired, true); err != nil {
		t.Fatal(err)
	}
	if len(c.writes) != 2 || c.writes[0].path != "sys/auth/up" || c.writes[1].path != "sys/auth/up/tune" {
		t.Fatalf("writes = %v, want enable then tune", c.writes)
	}
	wantEnable := map[string]any{
		keyType: litUserpass, litDescription: "d", "local": false, "seal_wrap": false, "external_entropy_access": false,
		"config": map[string]any{
			"plugin_version": litV100, keyForceNoCache: false, "default_lease_ttl": int64(3600), "max_lease_ttl": int64(7200),
			keyListingVisibility: "unauth", "token_type": "batch", "audit_non_hmac_request_keys": []string{"b", "a"},
			"audit_non_hmac_response_keys": []string{}, "passthrough_request_headers": []string{"X-A"},
			"allowed_response_headers": []string{"X-B"},
		},
		"options": map[string]string{},
	}
	if !reflect.DeepEqual(c.writes[0].data, wantEnable) {
		t.Errorf("enable = %v, want %v", c.writes[0].data, wantEnable)
	}
	wantTune := map[string]any{"user_lockout_config": map[string]any{
		"lockout_threshold": "5", "lockout_duration": "900", "lockout_counter_reset_duration": "600", "lockout_disable": false,
	}}
	if !reflect.DeepEqual(c.writes[1].data, wantTune) {
		t.Errorf("create tune = %v, want only user_lockout_config %v", c.writes[1].data, wantTune)
	}

	c = &recordingBao{}
	update := engine.StripCreateOnly(desired, a.Fields())
	if _, err := a.Write(context.Background(), c, m, update, false); err != nil {
		t.Fatal(err)
	}
	if len(c.writes) != 1 || c.writes[0].path != "sys/auth/up/tune" {
		t.Fatalf("update writes = %v, want tune only", c.writes)
	}
	if c.writes[0].data[litDescription] != "d" || c.writes[0].data["plugin_version"] != litV100 {
		t.Errorf("update tune = %v, want description and plugin_version", c.writes[0].data)
	}
}

// TestMountOptionsRemoved checks that a tune deletes the stored options the spec dropped, except the KV version,
// and that an unmanaged KV version is not compared.
func TestMountOptionsRemoved(t *testing.T) {
	a := SecretsMountAdapter{}
	m := &sysv1alpha1.SecretsMount{ObjectMeta: metav1.ObjectMeta{Name: "kv"}, Spec: sysv1alpha1.SecretsMountSpec{Type: "kv"}}
	table := map[string]any{"kv/": map[string]any{"type": "kv", "accessor": "kv_1", "config": map[string]any{},
		"options": map[string]any{litVersion: "1", "stale": "x"}}}
	c := &recordingBao{reads: map[string]map[string]any{"sys/mounts": table}}
	desired, _ := a.Desired(context.Background(), nil, m)
	observed, _, err := a.Observe(context.Background(), c, m)
	if err != nil {
		t.Fatal(err)
	}
	if got := engine.Diff(desired, observed, a.Fields()); !slices.Contains(got, "options") {
		t.Errorf("a stored option the spec does not set must differ, got %v", got)
	}
	if _, err := a.Write(context.Background(), c, m, engine.StripCreateOnly(desired, a.Fields()), false); err != nil {
		t.Fatal(err)
	}
	if got, want := c.writes[0].data["options"], map[string]string{"stale": ""}; !reflect.DeepEqual(got, want) {
		t.Errorf("tune options = %v, want %v", got, want)
	}

	table["kv/"].(map[string]any)["options"] = map[string]any{litVersion: "1"}
	observed, _, _ = a.Observe(context.Background(), c, m)
	if got := engine.Diff(desired, observed, a.Fields()); slices.Contains(got, "options") {
		t.Errorf("an unmanaged KV version must not differ, got %v", got)
	}
}

func TestAuthMountWriteStatus(t *testing.T) {
	m := fullAuthMount()
	if err := (AuthMountAdapter{}).WriteStatus(context.Background(), nil, m, engine.Payload{"accessor": "auth_userpass_1"}); err != nil {
		t.Fatal(err)
	}
	if m.Status.ID != "auth_userpass_1" {
		t.Errorf("status.id = %q", m.Status.ID)
	}
}

func TestSecretsMountKVAlias(t *testing.T) {
	a := SecretsMountAdapter{}
	m := &sysv1alpha1.SecretsMount{
		ObjectMeta: metav1.ObjectMeta{Name: "kv2"},
		Spec:       sysv1alpha1.SecretsMountSpec{Type: "kv-v2"},
	}
	desired, err := a.Desired(context.Background(), nil, m)
	if err != nil {
		t.Fatal(err)
	}
	if desired[keyType] != "kv" || !reflect.DeepEqual(desired["options"], map[string]string{litVersion: "2"}) {
		t.Errorf("desired = %v, want type kv with options.version 2", desired)
	}
	if m.Spec.Options != nil {
		t.Error("Desired must not mutate the spec")
	}

	c := &recordingBao{reads: map[string]map[string]any{"sys/mounts": decodeRead(t, goldenMountsTable)}}
	observed, found, err := a.Observe(context.Background(), c, m)
	if err != nil || !found {
		t.Fatalf("found=%v err=%v", found, err)
	}
	if got := engine.Diff(desired, observed, a.Fields()); len(got) != 0 {
		t.Errorf("differing = %v, want none", got)
	}
	if a.Path(m) != "sys/mounts/kv2" {
		t.Errorf("path = %q", a.Path(m))
	}
}

func TestSecretsMountWriteCreate(t *testing.T) {
	a := SecretsMountAdapter{}
	m := &sysv1alpha1.SecretsMount{
		ObjectMeta: metav1.ObjectMeta{Name: "pki"},
		Spec: sysv1alpha1.SecretsMountSpec{Type: "pki", MountFields: sysv1alpha1.MountFields{
			MaxLeaseTTL: metaDuration(87600 * time.Hour),
		}},
	}
	desired, _ := a.Desired(context.Background(), nil, m)
	c := &recordingBao{}
	if _, err := a.Write(context.Background(), c, m, desired, true); err != nil {
		t.Fatal(err)
	}
	if len(c.writes) != 1 || c.writes[0].path != "sys/mounts/pki" {
		t.Fatalf("writes = %v, want one enable carrying the config", c.writes)
	}
	config, _ := c.writes[0].data["config"].(map[string]any)
	if config["max_lease_ttl"] != int64(315360000) || config[keyListingVisibility] != "" || config[keyForceNoCache] != false {
		t.Errorf("enable config = %v", config)
	}
}

// TestSecretsMountDefaults checks an empty spec against a mount enabled with defaults: the read omits
// listing_visibility and the lists, and options is compared on the keys the spec sets.
func TestSecretsMountDefaults(t *testing.T) {
	a := SecretsMountAdapter{}
	m := &sysv1alpha1.SecretsMount{ObjectMeta: metav1.ObjectMeta{Name: "kv2"}, Spec: sysv1alpha1.SecretsMountSpec{Type: "kv"}}
	c := &recordingBao{reads: map[string]map[string]any{"sys/mounts": decodeRead(t, goldenMountsTable)}}
	observed, found, err := a.Observe(context.Background(), c, m)
	if err != nil || !found {
		t.Fatalf("found=%v err=%v", found, err)
	}
	desired, _ := a.Desired(context.Background(), nil, m)
	assertInSync(t, desired, observed, a.Fields())

	observed["listing_visibility"] = "unauth"
	observed["description"] = "tampered"
	if got := engine.Diff(desired, observed, a.Fields()); !slices.Equal(got, []string{litDescription, keyListingVisibility}) {
		t.Errorf("differing = %v, want description and listing_visibility back to their defaults", got)
	}
}

func sysRevPluginReader(t *testing.T, plugins ...*sysv1alpha1.Plugin) client.Reader {
	t.Helper()
	scheme := runtime.NewScheme()
	if err := sysv1alpha1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	b := fake.NewClientBuilder().WithScheme(scheme)
	for _, p := range plugins {
		b = b.WithObjects(p)
	}
	return b.Build()
}

func sysRevPlugin(typ sysv1alpha1.PluginType, version string, ready bool) *sysv1alpha1.Plugin {
	p := &sysv1alpha1.Plugin{
		ObjectMeta: metav1.ObjectMeta{Name: litOpenbaoPluginAuthLdap, Namespace: testNamespace},
		Spec:       sysv1alpha1.PluginSpec{Type: typ, Command: litOpenbaoPluginAuthLdap, Version: version},
	}
	if ready {
		p.Status.Conditions = []metav1.Condition{{Type: commonv1alpha1.ConditionReady, Status: metav1.ConditionTrue,
			Reason: commonv1alpha1.ReasonAvailable, LastTransitionTime: metav1.Now()}}
	}
	return p
}

func TestMountPluginRef(t *testing.T) {
	ctx := context.Background()
	ldap := "openbao-plugin-auth-ldap"
	m := &sysv1alpha1.AuthMount{
		ObjectMeta: metav1.ObjectMeta{Name: "ldap", Namespace: testNamespace},
		Spec: sysv1alpha1.AuthMountSpec{Type: ldap, MountFields: sysv1alpha1.MountFields{
			PluginRef: &commonv1alpha1.LocalObjectReference{Name: ldap},
		}},
	}
	a := AuthMountAdapter{Reader: sysRevPluginReader(t, sysRevPlugin(sysv1alpha1.PluginTypeAuth, "0.3.0", true))}
	desired, err := a.Desired(ctx, nil, m)
	if err != nil {
		t.Fatal(err)
	}
	if desired["plugin_version"] != "v0.3.0" {
		t.Errorf("plugin_version = %v, want the Plugin's version", desired["plugin_version"])
	}
	if got := a.References(m); !reflect.DeepEqual(got, []engine.ObjectReference{{Kind: "Plugin", Name: ldap}}) {
		t.Errorf("references = %v", got)
	}

	for name, tt := range map[string]struct {
		reader client.Reader
		typ    string
		want   error
	}{
		"missing":   {reader: sysRevPluginReader(t), typ: ldap, want: engine.ErrReferenceNotReady},
		"not ready": {reader: sysRevPluginReader(t, sysRevPlugin(sysv1alpha1.PluginTypeAuth, "", false)), typ: ldap, want: engine.ErrReferenceNotReady},
		"secret plugin": {reader: sysRevPluginReader(t, sysRevPlugin(sysv1alpha1.PluginTypeSecret, "", true)), typ: ldap,
			want: engine.ErrInvalidSpec},
		"other type": {reader: sysRevPluginReader(t, sysRevPlugin(sysv1alpha1.PluginTypeAuth, "", true)), typ: litUserpass,
			want: engine.ErrInvalidSpec},
	} {
		mm := m.DeepCopy()
		mm.Spec.Type = tt.typ
		if _, err := (AuthMountAdapter{Reader: tt.reader}).Desired(ctx, nil, mm); !errors.Is(err, tt.want) {
			t.Errorf("%s: err = %v, want %v", name, err, tt.want)
		}
	}
}

func TestMountPluginNotInCatalog(t *testing.T) {
	c := newConfigClient(nil)
	c.errs["write sys/auth/ldap"] = badRequest("plugin not found in the catalog: openbao-plugin-auth-ldap")
	err := writeMount(context.Background(), c, authTable, "ldap", engine.Payload{keyType: "openbao-plugin-auth-ldap"}, true)
	if !errors.Is(err, engine.ErrPluginNotRegistered) {
		t.Fatalf("err = %v, want engine.ErrPluginNotRegistered", err)
	}
}

// goldenFreshUserpass is the sys/auth entry of `bao auth enable -path=up userpass` with no tune (OpenBao 2.7.2).
const goldenFreshUserpass = `{"up/": {"accessor":"auth_userpass_575d02bf","config":{"default_lease_ttl":0,"force_no_cache":false,` +
	`"max_lease_ttl":0,"token_type":"default-service"},"deprecation_status":"supported","description":"",` +
	`"external_entropy_access":false,"local":false,"options":{},"plugin_version":"",` +
	`"running_plugin_version":"v2.7.2+builtin.bao","running_sha256":"","seal_wrap":false,"type":"userpass",` +
	`"uuid":"16d02245-e9be-0b71-b1cf-4544a62263bc"}}`

func TestAuthMountDefaults(t *testing.T) {
	a := AuthMountAdapter{}
	m := &sysv1alpha1.AuthMount{ObjectMeta: metav1.ObjectMeta{Name: "up"}, Spec: sysv1alpha1.AuthMountSpec{Type: litUserpass}}
	c := &recordingBao{reads: map[string]map[string]any{litSysAuth: decodeRead(t, goldenFreshUserpass)}}
	observed, found, err := a.Observe(context.Background(), c, m)
	if err != nil || !found {
		t.Fatalf("found=%v err=%v", found, err)
	}
	desired, _ := a.Desired(context.Background(), nil, m)
	assertInSync(t, desired, observed, a.Fields())
}
