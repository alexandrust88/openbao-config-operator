package secrets

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"

	commonv1alpha1 "github.com/alexandrust88/openbao-config-operator/api/common/v1alpha1"
	secretsv1alpha1 "github.com/alexandrust88/openbao-config-operator/api/secrets/v1alpha1"
	"github.com/alexandrust88/openbao-config-operator/internal/engine"
	"github.com/alexandrust88/openbao-config-operator/internal/openbao"
)

var testHashKey = []byte("kv-test-hash-key")

// kvFakeClient stores read responses by path and records writes and deletes. A response in notFound is
// returned as a 404 that carries data, the way the client reports a deleted KV version 2 secret.
type kvFakeClient struct {
	openbao.Client
	reads    map[string]map[string]any
	notFound map[string]map[string]any
	writes   map[string]map[string]any
	deletes  []string
}

func newKVFakeClient(reads map[string]map[string]any) *kvFakeClient {
	if reads == nil {
		reads = map[string]map[string]any{}
	}
	return &kvFakeClient{reads: reads, writes: map[string]map[string]any{}}
}

func (c *kvFakeClient) Read(_ context.Context, path string) (map[string]any, error) {
	if d, ok := c.reads[path]; ok {
		return d, nil
	}
	if d, ok := c.notFound[path]; ok {
		return nil, &openbao.NotFoundDataError{ResponseError: &openbao.ResponseError{StatusCode: 404}, Data: d}
	}
	return nil, openbao.ErrNotFound
}

func (c *kvFakeClient) Write(_ context.Context, path string, data map[string]any) (map[string]any, error) {
	c.writes[path] = data
	return nil, nil
}

func (c *kvFakeClient) Delete(_ context.Context, path string) error {
	c.deletes = append(c.deletes, path)
	return nil
}

// kvFakeResolver serves Secrets from memory the way engine.NewResolver does.
type kvFakeResolver struct {
	secrets map[string]map[string][]byte
}

func (r kvFakeResolver) SecretValue(_ context.Context, ns string, ref commonv1alpha1.SecretKeySelector) ([]byte, error) {
	data, ok := r.secrets[ref.Name]
	if !ok {
		return nil, fmt.Errorf("secret %s/%s not found: %w", ns, ref.Name, engine.ErrReferenceNotReady)
	}
	v, ok := data[ref.Key]
	if !ok {
		return nil, fmt.Errorf("secret %s/%s has no key %q: %w", ns, ref.Name, ref.Key, engine.ErrReferenceNotReady)
	}
	return v, nil
}

func (r kvFakeResolver) Object(_ context.Context, key client.ObjectKey, obj client.Object) error {
	data, ok := r.secrets[key.Name]
	if !ok {
		return fmt.Errorf("%w: %w", engine.ErrReferenceNotReady,
			apierrors.NewNotFound(schema.GroupResource{Resource: "secrets"}, key.Name))
	}
	obj.(*corev1.Secret).Data = maps.Clone(data)
	return nil
}

func newKVSecret(version int32) *secretsv1alpha1.KVSecret {
	return &secretsv1alpha1.KVSecret{
		ObjectMeta: metav1.ObjectMeta{Name: "app", Namespace: "team"},
		Spec: secretsv1alpha1.KVSecretSpec{
			MountPath: "kv",
			Path:      "team/app",
			KVVersion: version,
			DataFrom: []secretsv1alpha1.KVSecretDataFrom{
				{SecretRef: commonv1alpha1.LocalObjectReference{Name: "db"}},
				{SecretRef: commonv1alpha1.LocalObjectReference{Name: "db-override"}},
			},
			Data: map[string]secretsv1alpha1.KVSecretDataSource{
				"api-token": {SecretKeyRef: commonv1alpha1.SecretKeySelector{Name: "api", Key: "token"}},
				"username":  {SecretKeyRef: commonv1alpha1.SecretKeySelector{Name: "api", Key: "user"}},
			},
		},
	}
}

func kvResolver() kvFakeResolver {
	return kvFakeResolver{secrets: map[string]map[string][]byte{
		"db":          {"username": []byte("db-user"), "password": []byte("s3cr3t-db")},
		"db-override": {"password": []byte("s3cr3t-override")},
		"api":         {"token": []byte("s3cr3t-token"), "user": []byte("api-user")},
	}}
}

func TestKVSecretPath(t *testing.T) {
	a := KVSecretAdapter{}
	for version, want := range map[int32]string{0: "kv/data/team/app", 1: "kv/team/app", 2: "kv/data/team/app"} {
		if got := a.Path(newKVSecret(version)); got != want {
			t.Errorf("version %d: path %q, want %q", version, got, want)
		}
	}
}

func TestKVSecretDesiredMergesInOrder(t *testing.T) {
	desired, err := KVSecretAdapter{}.Desired(context.Background(), kvResolver(), newKVSecret(2))
	if err != nil {
		t.Fatal(err)
	}
	wantValues := map[string]any{"username": "api-user", "password": "s3cr3t-override", "api-token": "s3cr3t-token"}
	if !maps.Equal(desired[kvValuesKey].(map[string]any), wantValues) {
		t.Errorf("values = %v", desired[kvValuesKey])
	}
	wantKeys := map[string]any{"username": true, "password": true, "api-token": true}
	if !maps.Equal(desired[kvKeysKey].(map[string]any), wantKeys) {
		t.Errorf("keys = %v", desired[kvKeysKey])
	}
	if len(desired) != 2 {
		t.Errorf("unexpected payload keys: %v", slices.Collect(maps.Keys(desired)))
	}
}

func TestKVSecretDesiredMissingSecret(t *testing.T) {
	for _, missing := range []string{"db", "api"} {
		r := kvResolver()
		delete(r.secrets, missing)
		_, err := KVSecretAdapter{}.Desired(context.Background(), r, newKVSecret(2))
		if !errors.Is(err, engine.ErrReferenceNotReady) {
			t.Errorf("missing %s: err = %v, want ErrReferenceNotReady", missing, err)
		}
		if err != nil && strings.Contains(err.Error(), "s3cr3t") {
			t.Errorf("error leaks a value: %v", err)
		}
	}
}

func TestKVSecretSteadyStateAgainstGoldenRead(t *testing.T) {
	// Captured from bao server -dev 2.7.2 (kv v2 read of <mount>/data/<path>) plus a foreign key.
	golden := map[string]any{
		"data": map[string]any{"username": "api-user", "password": "changed-out-of-band", "api-token": "x", "owner": "other"},
		"metadata": map[string]any{
			"created_time": "2026-11-13T22:26:53.665645Z", "custom_metadata": nil, "deletion_time": "",
			"destroyed": false, "version": json.Number("3"),
		},
	}
	a := KVSecretAdapter{}
	s := newKVSecret(2)
	desired, err := a.Desired(context.Background(), kvResolver(), s)
	if err != nil {
		t.Fatal(err)
	}
	observed, found, err := a.Observe(context.Background(), newKVFakeClient(map[string]map[string]any{"kv/data/team/app": golden}), s)
	if err != nil || !found {
		t.Fatalf("found=%v err=%v", found, err)
	}
	if _, leaked := observed[kvValuesKey]; leaked {
		t.Errorf("observe returned values: %v", observed)
	}
	if d := engine.Diff(desired, observed, a.Fields()); len(d) != 0 {
		t.Errorf("steady state differs on %v", d)
	}

	delete(golden["data"].(map[string]any), "username")
	observed, _, _ = a.Observe(context.Background(), newKVFakeClient(map[string]map[string]any{"kv/data/team/app": golden}), s)
	if d := engine.Diff(desired, observed, a.Fields()); !slices.Equal(d, []string{kvKeysKey}) {
		t.Errorf("removed key: differing %v, want [%s]", d, kvKeysKey)
	}
}

func TestKVSecretWriteOnlyHash(t *testing.T) {
	a := KVSecretAdapter{}
	r := kvResolver()
	first, _ := a.Desired(context.Background(), r, newKVSecret(2))
	r.secrets["api"]["token"] = []byte("rotated")
	second, _ := a.Desired(context.Background(), r, newKVSecret(2))
	h1, h2 := engine.WriteOnlyHash(testHashKey, first, a.Fields()), engine.WriteOnlyHash(testHashKey, second, a.Fields())
	if h1 == "" || h1 == h2 {
		t.Errorf("hash must be set and change with a value: %q %q", h1, h2)
	}
}

func TestKVSecretObserveAbsent(t *testing.T) {
	for name, notFound := range map[string]map[string]map[string]any{
		"missing":      nil,
		"soft deleted": kvSoftDeleted("kv/data/team/app", 1),
	} {
		c := newKVFakeClient(nil)
		c.notFound = notFound
		_, found, err := KVSecretAdapter{}.Observe(context.Background(), c, newKVSecret(2))
		if err != nil || found {
			t.Errorf("%s: found=%v err=%v, want absent", name, found, err)
		}
	}
}

func TestKVSecretWriteV2MergesWithCAS(t *testing.T) {
	a := KVSecretAdapter{}
	s := newKVSecret(2)
	s.Status.ManagedKeys = []string{"api-token", "dropped", "password", "username"}
	c := newKVFakeClient(map[string]map[string]any{"kv/data/team/app": {
		"data":     map[string]any{"owner": "other", "dropped": "old", "password": "old"},
		"metadata": map[string]any{"version": json.Number("7")},
	}})
	desired, _ := a.Desired(context.Background(), kvResolver(), s)
	if _, err := a.Write(context.Background(), c, s, desired, false); err != nil {
		t.Fatal(err)
	}
	body := c.writes["kv/data/team/app"]
	want := map[string]any{"owner": "other", "username": "api-user", "password": "s3cr3t-override", "api-token": "s3cr3t-token"}
	if !maps.Equal(body["data"].(map[string]any), want) {
		t.Errorf("data = %v", body["data"])
	}
	if cas := body["options"].(map[string]any)["cas"]; cas != int64(7) {
		t.Errorf("cas = %v, want 7", cas)
	}
	if !slices.Equal(s.Status.ManagedKeys, []string{"api-token", "password", "username"}) {
		t.Errorf("managed keys = %v", s.Status.ManagedKeys)
	}
}

func TestKVSecretWriteV1AndCreate(t *testing.T) {
	a := KVSecretAdapter{}
	s := newKVSecret(1)
	c := newKVFakeClient(nil)
	desired, _ := a.Desired(context.Background(), kvResolver(), s)
	if _, err := a.Write(context.Background(), c, s, desired, true); err != nil {
		t.Fatal(err)
	}
	want := map[string]any{"username": "api-user", "password": "s3cr3t-override", "api-token": "s3cr3t-token"}
	if !maps.Equal(c.writes["kv/team/app"], want) {
		t.Errorf("v1 body = %v", c.writes["kv/team/app"])
	}

	s2 := newKVSecret(2)
	c2 := newKVFakeClient(nil)
	if _, err := a.Write(context.Background(), c2, s2, desired, true); err != nil {
		t.Fatal(err)
	}
	if cas := c2.writes["kv/data/team/app"]["options"].(map[string]any)["cas"]; cas != int64(0) {
		t.Errorf("create cas = %v, want 0", cas)
	}
}

func TestKVWriteRepairsSoftDeletedSecret(t *testing.T) {
	c := newKVFakeClient(nil)
	c.notFound = kvSoftDeleted("kv/data/team/app", 3)
	maps.Copy(c.notFound, kvSoftDeleted("kv/data/team/db", 3))
	kv := newKVSecret(2)
	desired, _ := KVSecretAdapter{}.Desired(context.Background(), kvResolver(), kv)
	if _, err := (KVSecretAdapter{}).Write(context.Background(), c, kv, desired, false); err != nil {
		t.Fatal(err)
	}
	if _, err := (RandomSecretAdapter{}).Write(context.Background(), c, newRandomSecret(), nil, false); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"kv/data/team/app", "kv/data/team/db"} {
		if cas := c.writes[path]["options"].(map[string]any)["cas"]; cas != int64(3) {
			t.Errorf("%s: cas = %v, want the current version 3 of the deleted secret", path, cas)
		}
	}
}

// kvSoftDeleted is the 404 body OpenBao returns for a KV version 2 secret whose latest version is deleted.
func kvSoftDeleted(path string, version int) map[string]map[string]any {
	return map[string]map[string]any{path: {"data": nil, "metadata": map[string]any{
		"deletion_time": "2026-11-14T06:58:29.65265Z", "destroyed": false, "version": json.Number(fmt.Sprint(version)),
	}}}
}

func TestKVSecretDeleteRemovesOnlyManagedKeys(t *testing.T) {
	managed := []string{"api-token", "password", "username"}
	tests := map[string]struct {
		version    int32
		reads      map[string]map[string]any
		wantWrites map[string]map[string]any
		wantDelete []string
	}{
		"v2 foreign key kept": {
			version: 2,
			reads: map[string]map[string]any{"kv/data/team/app": {
				"data":     map[string]any{"owner": "other", "password": "x", "username": "y"},
				"metadata": map[string]any{"version": json.Number("4")},
			}},
			wantWrites: map[string]map[string]any{"kv/data/team/app": {
				"data": map[string]any{"owner": "other"}, "options": map[string]any{"cas": int64(4)},
			}},
		},
		"v2 all keys owned": {
			version: 2,
			reads: map[string]map[string]any{"kv/data/team/app": {
				"data":     map[string]any{"password": "x", "username": "y"},
				"metadata": map[string]any{"version": json.Number("4")},
			}},
			wantWrites: map[string]map[string]any{"kv/data/team/app": {
				"data": map[string]any{}, "options": map[string]any{"cas": int64(4)},
			}},
		},
		"v1 foreign key kept": {
			version:    1,
			reads:      map[string]map[string]any{"kv/team/app": {"owner": "other", "api-token": "x"}},
			wantWrites: map[string]map[string]any{"kv/team/app": {"owner": "other"}},
		},
		"v1 all keys owned": {
			version:    1,
			reads:      map[string]map[string]any{"kv/team/app": {"api-token": "x"}},
			wantDelete: []string{"kv/team/app"},
		},
		"no managed key left": {
			version: 2,
			reads: map[string]map[string]any{"kv/data/team/app": {
				"data": map[string]any{"owner": "other"}, "metadata": map[string]any{"version": json.Number("4")},
			}},
		},
		"absent": {version: 2},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			s := newKVSecret(tt.version)
			s.Status.ManagedKeys = managed
			c := newKVFakeClient(tt.reads)
			if err := (KVSecretAdapter{}).Delete(context.Background(), c, s); err != nil {
				t.Fatal(err)
			}
			if !slices.Equal(c.deletes, tt.wantDelete) {
				t.Errorf("deletes %v, want %v", c.deletes, tt.wantDelete)
			}
			if fmt.Sprint(c.writes) != fmt.Sprint(tt.wantWrites) {
				t.Errorf("writes %v, want %v", c.writes, tt.wantWrites)
			}
		})
	}
}

func TestKVSecretSecretRefs(t *testing.T) {
	got := KVSecretAdapter{}.SecretRefs(newKVSecret(2))
	if !slices.Equal(got, []string{"api", "db", "db-override"}) {
		t.Errorf("secret refs = %v", got)
	}
}

func TestKVKindsAreDestructive(t *testing.T) {
	for name, kind := range map[string]engine.Destructive{"KVSecret": KVSecretAdapter{}, "RandomSecret": RandomSecretAdapter{}} {
		if !kind.Destructive() {
			t.Errorf("%s must need the destructive-delete annotation", name)
		}
	}
}
