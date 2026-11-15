//go:build integration

package integration

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/intstr"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/config"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
	"sigs.k8s.io/controller-runtime/pkg/metrics"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"

	commonv1alpha1 "github.com/alexandrust88/openbao-config-operator/api/common/v1alpha1"
	secretsv1alpha1 "github.com/alexandrust88/openbao-config-operator/api/secrets/v1alpha1"
	"github.com/alexandrust88/openbao-config-operator/internal/controller/secrets"
	"github.com/alexandrust88/openbao-config-operator/internal/engine"
	"github.com/alexandrust88/openbao-config-operator/internal/openbao"
	"github.com/alexandrust88/openbao-config-operator/test/testenv"
)

const (
	secretsKV2       = "itest-kv2"
	secretsKVAppPath = "team/app"
	secretsKV1       = "itest-kv1"
)

// TestIntegrationSecrets runs the KVConfig, KVSecret and RandomSecret controllers against bao server -dev.
func TestIntegrationSecrets(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	e := secretsSetup(ctx, t)

	mustWrite(ctx, t, e.bao, "sys/mounts/"+secretsKV2, map[string]any{"type": "kv", "options": map[string]any{"version": "2"}})
	mustWrite(ctx, t, e.bao, "sys/mounts/"+secretsKV1, map[string]any{"type": "kv", "options": map[string]any{"version": "1"}})
	waitFor(t, "kv v2 mount ready", func() error {
		_, err := e.bao.Read(ctx, secretsKV2+"/config")
		return err
	})

	t.Run("kvconfig", func(t *testing.T) { testKVConfig(ctx, t, e) })
	t.Run("kvsecret", func(t *testing.T) { testKVSecret(ctx, t, e) })
	t.Run("kvsecret-v1", func(t *testing.T) { testKVSecretV1(ctx, t, e) })
	t.Run("kvsecret-soft-deleted", func(t *testing.T) { testKVSecretSoftDeleted(ctx, t, e) })
	t.Run("kvsecret-path-history", func(t *testing.T) { testKVSecretPathHistory(ctx, t, e) })
	t.Run("randomsecret", func(t *testing.T) { testRandomSecret(ctx, t, e) })
	t.Run("randomsecret-policy-and-adoption", func(t *testing.T) { testRandomSecretPolicyAndAdoption(ctx, t, e) })
	t.Run("randomsecret-path-history", func(t *testing.T) { testRandomSecretPathHistory(ctx, t, e) })
}

// testKVConfig also turns on cas_required for the later subtests, which proves the check-and-set writes.
func testKVConfig(ctx context.Context, t *testing.T, e env) {
	path := secretsKV2 + "/config"
	cfg := &secretsv1alpha1.KVConfig{
		ObjectMeta: metav1.ObjectMeta{Name: "itest-kv2", Namespace: namespace},
		Spec: secretsv1alpha1.KVConfigSpec{
			ResourceSpec:       secretsAdoptSpec(""),
			MountPath:          secretsKV2,
			MaxVersions:        new(int32(5)),
			CASRequired:        new(true),
			DeleteVersionAfter: new(intstr.FromString("768h")),
		},
	}
	mustCreate(ctx, t, e.k8s, cfg)
	want := map[string]string{"max_versions": "5", "cas_required": "true", "delete_version_after": "768h0m0s"}
	waitFor(t, "kv config written", func() error { return secretsExpectFields(ctx, e.bao, path, want) })
	waitFor(t, "kv config ready", func() error { return expectReady(ctx, e.k8s, cfg) })

	mustWrite(ctx, t, e.bao, path, map[string]any{"max_versions": 10})
	waitFor(t, "kv config drift repaired", func() error { return secretsExpectFields(ctx, e.bao, path, want) })

	secretsExpectNoWrites(t, "KVConfig")

	if err := e.k8s.Delete(ctx, cfg); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "kv config resource gone", func() error { return expectGone(ctx, e.k8s, cfg) })
	if err := secretsExpectFields(ctx, e.bao, path, want); err != nil {
		t.Errorf("orphaned config changed: %v", err)
	}
}

func testKVSecret(ctx context.Context, t *testing.T, e env) {
	const path = secretsKVAppPath
	secretsPut(ctx, t, e.bao, path, map[string]any{"owner": "other"})
	db := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "kv-app-db", Namespace: namespace},
		Data:       map[string][]byte{"username": []byte("app"), "password": []byte("first")},
	}
	mustCreate(ctx, t, e.k8s, db)
	mustCreate(ctx, t, e.k8s, &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "kv-app-api", Namespace: namespace},
		Data:       map[string][]byte{"token": []byte("tok")},
	})
	kv := &secretsv1alpha1.KVSecret{
		ObjectMeta: metav1.ObjectMeta{Name: "itest-app", Namespace: namespace, Annotations: secretsAllowDestructiveDelete},
		Spec: secretsv1alpha1.KVSecretSpec{
			ResourceSpec: secretsAdoptSpec(commonv1alpha1.DeletionPolicyDelete),
			MountPath:    secretsKV2,
			Path:         path,
			DataFrom:     []secretsv1alpha1.KVSecretDataFrom{{SecretRef: commonv1alpha1.LocalObjectReference{Name: "kv-app-db"}}},
			Data: map[string]secretsv1alpha1.KVSecretDataSource{
				"api-token": {SecretKeyRef: commonv1alpha1.SecretKeySelector{Name: "kv-app-api", Key: "token"}},
			},
		},
	}
	mustCreate(ctx, t, e.k8s, kv)

	want := map[string]any{"owner": "other", "username": "app", "password": "first", "api-token": "tok"}
	waitFor(t, "kv secret merged into existing data", func() error { return secretsExpectKV(ctx, e.bao, want) })
	waitFor(t, "kv secret ready", func() error { return expectReady(ctx, e.k8s, kv) })
	if !slices.Equal(kv.Status.ManagedKeys, []string{"api-token", "password", "username"}) {
		t.Errorf("managed keys = %v", kv.Status.ManagedKeys)
	}

	secretsExpectNoWrites(t, "KVSecret")

	secretsPut(ctx, t, e.bao, path, map[string]any{"owner": "other", "password": "first", "api-token": "tok"})
	waitFor(t, "removed key restored", func() error { return secretsExpectKV(ctx, e.bao, want) })

	db.Data["password"] = []byte("second")
	if err := e.k8s.Update(ctx, db); err != nil {
		t.Fatal(err)
	}
	want["password"] = "second"
	waitFor(t, "rotated secret written", func() error { return secretsExpectKV(ctx, e.bao, want) })

	if err := e.k8s.Get(ctx, client.ObjectKeyFromObject(kv), kv); err != nil {
		t.Fatal(err)
	}
	kv.Spec.Data = nil
	if err := e.k8s.Update(ctx, kv); err != nil {
		t.Fatal(err)
	}
	delete(want, "api-token")
	waitFor(t, "key dropped from spec removed", func() error {
		if err := secretsExpectKV(ctx, e.bao, want); err != nil {
			return err
		}
		data, _, err := secretsReadKV(ctx, e.bao, path)
		if _, still := data["api-token"]; err == nil && still {
			return errors.New("api-token still present")
		}
		return err
	})

	if err := e.k8s.Delete(ctx, kv); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "kv secret resource gone", func() error { return expectGone(ctx, e.k8s, kv) })
	data, _, err := secretsReadKV(ctx, e.bao, path)
	if err != nil || len(data) != 1 || data["owner"] != "other" {
		t.Errorf("delete must keep only the foreign key: keys=%v err=%v", slices.Sorted(maps.Keys(data)), err)
	}

	sole := &secretsv1alpha1.KVSecret{
		ObjectMeta: metav1.ObjectMeta{Name: "itest-app-sole", Namespace: namespace, Annotations: secretsAllowDestructiveDelete},
		Spec: secretsv1alpha1.KVSecretSpec{
			ResourceSpec: secretsResourceSpec(commonv1alpha1.DeletionPolicyDelete),
			MountPath:    secretsKV2,
			Path:         "team/sole",
			DataFrom:     []secretsv1alpha1.KVSecretDataFrom{{SecretRef: commonv1alpha1.LocalObjectReference{Name: "kv-app-db"}}},
		},
	}
	mustCreate(ctx, t, e.k8s, sole)
	waitFor(t, "sole kv secret ready", func() error { return expectReady(ctx, e.k8s, sole) })
	if err := e.k8s.Delete(ctx, sole); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "kv secret owning every key deleted", func() error {
		return expectAbsent(ctx, e.bao, secretsKV2+"/metadata/team/sole")
	})
	waitFor(t, "sole kv secret resource gone", func() error { return expectGone(ctx, e.k8s, sole) })
}

// testKVSecretSoftDeleted proves the repair of a secret whose latest version is deleted: OpenBao answers its data
// read with 404 while the metadata keeps the version, so the write must use that version for check-and-set.
func testKVSecretSoftDeleted(ctx context.Context, t *testing.T, e env) {
	const path = "team/soft-deleted"
	mustCreate(ctx, t, e.k8s, &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "kv-soft-deleted", Namespace: namespace},
		Data:       map[string][]byte{"username": []byte("app")},
	})
	kv := &secretsv1alpha1.KVSecret{
		ObjectMeta: metav1.ObjectMeta{Name: "itest-soft-deleted", Namespace: namespace},
		Spec: secretsv1alpha1.KVSecretSpec{
			ResourceSpec: secretsResourceSpec(""),
			MountPath:    secretsKV2,
			Path:         path,
			DataFrom:     []secretsv1alpha1.KVSecretDataFrom{{SecretRef: commonv1alpha1.LocalObjectReference{Name: "kv-soft-deleted"}}},
		},
	}
	mustCreate(ctx, t, e.k8s, kv)
	waitFor(t, "soft-deleted scenario ready", func() error { return expectReady(ctx, e.k8s, kv) })

	if err := e.bao.Delete(ctx, secretsKV2+"/data/"+path); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "soft-deleted secret written again", func() error {
		data, version, err := secretsReadKV(ctx, e.bao, path)
		if err != nil {
			return err
		}
		if data["username"] != "app" || version != "2" {
			return fmt.Errorf("version %s not repaired", version)
		}
		return nil
	})
}

// testKVSecretPathHistory: a destructive delete removes the history only of a KV path the KVSecret created; on a
// path whose earlier versions predate it (latest version soft-deleted) it removes only its keys.
func testKVSecretPathHistory(ctx context.Context, t *testing.T, e env) {
	mustCreate(ctx, t, e.k8s, &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "kv-path-history", Namespace: namespace},
		Data:       map[string][]byte{"username": []byte("app")},
	})
	secretsPut(ctx, t, e.bao, "team/kv-history", map[string]any{"old": "o"})
	if err := e.bao.Delete(ctx, secretsKV2+"/data/team/kv-history"); err != nil {
		t.Fatal(err)
	}
	for path, purged := range map[string]bool{"team/kv-history": false, "team/kv-new": true} {
		kv := &secretsv1alpha1.KVSecret{
			ObjectMeta: metav1.ObjectMeta{Name: "itest-" + strings.ReplaceAll(path, "/", "-"), Namespace: namespace,
				Annotations: secretsAllowDestructiveDelete},
			Spec: secretsv1alpha1.KVSecretSpec{
				ResourceSpec: secretsResourceSpec(commonv1alpha1.DeletionPolicyDelete),
				MountPath:    secretsKV2,
				Path:         path,
				DataFrom:     []secretsv1alpha1.KVSecretDataFrom{{SecretRef: commonv1alpha1.LocalObjectReference{Name: "kv-path-history"}}},
			},
		}
		mustCreate(ctx, t, e.k8s, kv)
		waitFor(t, path+" kv secret ready", func() error { return expectReady(ctx, e.k8s, kv) })
		if kv.Status.PathCreated != purged {
			t.Errorf("%s: pathCreated = %v, want %v", path, kv.Status.PathCreated, purged)
		}
		if err := e.k8s.Delete(ctx, kv); err != nil {
			t.Fatal(err)
		}
		waitFor(t, path+" kv secret resource gone", func() error { return expectGone(ctx, e.k8s, kv) })
		err := expectAbsent(ctx, e.bao, secretsKV2+"/metadata/"+path)
		if purged && err != nil || !purged && err == nil {
			t.Errorf("%s: metadata absent = %v, want %v", path, err == nil, purged)
		}
	}
}

func testKVSecretV1(ctx context.Context, t *testing.T, e env) {
	const path = "team/legacy"
	mustCreate(ctx, t, e.k8s, &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "kv-legacy", Namespace: namespace},
		Data:       map[string][]byte{"user": []byte("legacy")},
	})
	kv := &secretsv1alpha1.KVSecret{
		ObjectMeta: metav1.ObjectMeta{Name: "itest-legacy", Namespace: namespace},
		Spec: secretsv1alpha1.KVSecretSpec{
			ResourceSpec: secretsResourceSpec(""),
			MountPath:    secretsKV1,
			Path:         path,
			KVVersion:    1,
			DataFrom:     []secretsv1alpha1.KVSecretDataFrom{{SecretRef: commonv1alpha1.LocalObjectReference{Name: "kv-legacy"}}},
		},
	}
	mustCreate(ctx, t, e.k8s, kv)
	waitFor(t, "kv v1 secret written", func() error {
		return expectData(ctx, e.bao, secretsKV1+"/"+path, map[string]any{"user": "legacy"})
	})
	waitFor(t, "kv v1 secret ready", func() error { return expectReady(ctx, e.k8s, kv) })

	mustWrite(ctx, t, e.bao, secretsKV1+"/"+path, map[string]any{"other": "x"})
	waitFor(t, "kv v1 removed key restored", func() error {
		return expectData(ctx, e.bao, secretsKV1+"/"+path, map[string]any{"user": "legacy", "other": "x"})
	})

	if err := e.k8s.Delete(ctx, kv); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "kv v1 resource gone", func() error { return expectGone(ctx, e.k8s, kv) })
	if err := expectData(ctx, e.bao, secretsKV1+"/"+path, map[string]any{"user": "legacy"}); err != nil {
		t.Errorf("orphaned kv v1 secret: %v", err)
	}
}

func testRandomSecret(ctx context.Context, t *testing.T, e env) {
	const path = "team/random"
	rs := &secretsv1alpha1.RandomSecret{
		ObjectMeta: metav1.ObjectMeta{Name: "itest-random", Namespace: namespace},
		Spec: secretsv1alpha1.RandomSecretSpec{
			ResourceSpec:   secretsResourceSpec(""),
			MountPath:      secretsKV2,
			Path:           path,
			Key:            "password",
			Generator:      &secretsv1alpha1.RandomSecretGenerator{Length: 24, Charset: "abcdefghij0123456789"},
			WriteSecretRef: &commonv1alpha1.LocalObjectReference{Name: "itest-random-out"},
		},
	}
	mustCreate(ctx, t, e.k8s, rs)

	var first string
	waitFor(t, "random value generated", func() error {
		v, err := secretsRandomValue(ctx, e, path, "itest-random-out")
		first = v
		return err
	})
	if len(first) != 24 || strings.Trim(first, "abcdefghij0123456789") != "" {
		t.Errorf("generated value does not match the generator")
	}
	waitFor(t, "random secret ready", func() error { return expectReady(ctx, e.k8s, rs) })

	secretsExpectNoWrites(t, "RandomSecret")
	if v, _ := secretsRandomValue(ctx, e, path, "itest-random-out"); v != first {
		t.Error("value changed at rest")
	}

	if err := e.k8s.Get(ctx, client.ObjectKeyFromObject(rs), rs); err != nil {
		t.Fatal(err)
	}
	rs.Spec.RefreshNonce = "1"
	if err := e.k8s.Update(ctx, rs); err != nil {
		t.Fatal(err)
	}
	var second string
	waitFor(t, "refreshNonce regenerates", func() error {
		v, err := secretsRandomValue(ctx, e, path, "itest-random-out")
		if err == nil && v == first {
			return errors.New("value not regenerated yet")
		}
		second = v
		return err
	})

	secretsPut(ctx, t, e.bao, path, map[string]any{"other": "x"})
	waitFor(t, "removed value regenerated", func() error {
		v, err := secretsRandomValue(ctx, e, path, "itest-random-out")
		if err == nil && (v == second || v == "") {
			return errors.New("value not regenerated yet")
		}
		return err
	})

	if err := e.k8s.Delete(ctx, rs); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "random secret resource gone", func() error { return expectGone(ctx, e.k8s, rs) })
	if data, _, err := secretsReadKV(ctx, e.bao, path); err != nil || data["password"] == nil {
		t.Errorf("orphaned random value missing: %v", err)
	}
}

func testRandomSecretPolicyAndAdoption(ctx context.Context, t *testing.T, e env) {
	mustWrite(ctx, t, e.bao, "sys/policies/password/itest-pin", map[string]any{
		"policy": "length = 12\nrule \"charset\" {\n  charset = \"0123456789\"\n}\n",
	})
	policy := &secretsv1alpha1.RandomSecret{
		ObjectMeta: metav1.ObjectMeta{Name: "itest-pin", Namespace: namespace},
		Spec: secretsv1alpha1.RandomSecretSpec{
			ResourceSpec:       secretsResourceSpec(""),
			MountPath:          secretsKV2,
			Path:               "team/pin",
			Key:                "pin",
			PasswordPolicyName: "itest-pin",
		},
	}
	mustCreate(ctx, t, e.k8s, policy)
	waitFor(t, "policy value generated", func() error {
		data, _, err := secretsReadKV(ctx, e.bao, "team/pin")
		if err != nil {
			return err
		}
		pin, _ := data["pin"].(string)
		if len(pin) != 12 || strings.Trim(pin, "0123456789") != "" {
			return errors.New("pin missing or not from the policy")
		}
		return nil
	})
	waitFor(t, "policy random secret ready", func() error { return expectReady(ctx, e.k8s, policy) })

	secretsPut(ctx, t, e.bao, "team/adopt", map[string]any{"password": "existing"})
	adopt := &secretsv1alpha1.RandomSecret{
		ObjectMeta: metav1.ObjectMeta{Name: "itest-adopt", Namespace: namespace},
		Spec: secretsv1alpha1.RandomSecretSpec{
			ResourceSpec:   secretsAdoptSpec(""),
			MountPath:      secretsKV2,
			Path:           "team/adopt",
			RefreshNonce:   "initial",
			WriteSecretRef: &commonv1alpha1.LocalObjectReference{Name: "itest-adopt-out"},
		},
	}
	mustCreate(ctx, t, e.k8s, adopt)
	waitFor(t, "adopted value kept and copied", func() error {
		v, err := secretsRandomValue(ctx, e, "team/adopt", "itest-adopt-out")
		if err == nil && v != "existing" {
			return errors.New("adopted value was replaced")
		}
		return err
	})
	waitFor(t, "adopted random secret ready", func() error { return expectReady(ctx, e.k8s, adopt) })
}

// testRandomSecretPathHistory: a destructive delete removes the history only of a KV path the RandomSecret created;
// on a path whose earlier versions predate it (latest version soft-deleted) it removes only its key.
func testRandomSecretPathHistory(ctx context.Context, t *testing.T, e env) {
	secretsPut(ctx, t, e.bao, "team/random-history", map[string]any{"old": "o"})
	if err := e.bao.Delete(ctx, secretsKV2+"/data/team/random-history"); err != nil {
		t.Fatal(err)
	}
	for path, purged := range map[string]bool{"team/random-history": false, "team/random-new": true} {
		rs := &secretsv1alpha1.RandomSecret{
			ObjectMeta: metav1.ObjectMeta{Name: "itest-" + strings.ReplaceAll(path, "/", "-"), Namespace: namespace,
				Annotations: secretsAllowDestructiveDelete},
			Spec: secretsv1alpha1.RandomSecretSpec{
				ResourceSpec: secretsResourceSpec(commonv1alpha1.DeletionPolicyDelete),
				MountPath:    secretsKV2,
				Path:         path,
			},
		}
		mustCreate(ctx, t, e.k8s, rs)
		waitFor(t, path+" random secret ready", func() error { return expectReady(ctx, e.k8s, rs) })
		if rs.Status.PathCreated != purged {
			t.Errorf("%s: pathCreated = %v, want %v", path, rs.Status.PathCreated, purged)
		}
		if err := e.k8s.Delete(ctx, rs); err != nil {
			t.Fatal(err)
		}
		waitFor(t, path+" random secret resource gone", func() error { return expectGone(ctx, e.k8s, rs) })
		err := expectAbsent(ctx, e.bao, secretsKV2+"/metadata/"+path)
		if purged && err != nil || !purged && err == nil {
			t.Errorf("%s: metadata absent = %v, want %v", path, err == nil, purged)
		}
	}
}

// secretsRandomValue returns the password stored in OpenBao after checking the output Secret holds the same.
func secretsRandomValue(ctx context.Context, e env, path, outputSecret string) (string, error) {
	const key = "password"
	data, _, err := secretsReadKV(ctx, e.bao, path)
	if err != nil {
		return "", err
	}
	value, _ := data[key].(string)
	if value == "" {
		return "", fmt.Errorf("key %s absent", key)
	}
	var out corev1.Secret
	if err := e.k8s.Get(ctx, client.ObjectKey{Namespace: namespace, Name: outputSecret}, &out); err != nil {
		return "", err
	}
	if string(out.Data[key]) != value {
		return "", errors.New("output Secret differs from OpenBao")
	}
	return value, nil
}

func secretsResourceSpec(deletion commonv1alpha1.DeletionPolicy) commonv1alpha1.ResourceSpec {
	return commonv1alpha1.ResourceSpec{
		ConnectionRef:  commonv1alpha1.ConnectionReference{Name: "dev"},
		DeletionPolicy: deletion,
	}
}

var secretsAllowDestructiveDelete = map[string]string{commonv1alpha1.AnnotationAllowDestructiveDelete: "true"}

// secretsAdoptSpec is for resources whose OpenBao object already exists: a mount's config, or a KV secret that
// holds keys of other writers.
func secretsAdoptSpec(deletion commonv1alpha1.DeletionPolicy) commonv1alpha1.ResourceSpec {
	spec := secretsResourceSpec(deletion)
	spec.CreationPolicy = commonv1alpha1.CreationPolicyCreateOrAdopt
	return spec
}

func secretsReadKV(ctx context.Context, bao openbao.Client, path string) (map[string]any, json.Number, error) {
	resp, err := bao.Read(ctx, secretsKV2+"/data/"+path)
	if err != nil {
		return nil, "", err
	}
	metadata, _ := resp["metadata"].(map[string]any)
	version, _ := metadata["version"].(json.Number)
	data, _ := resp["data"].(map[string]any)
	return data, version, nil
}

// secretsPut replaces a KV v2 secret with check-and-set, as the mount requires after testKVConfig.
func secretsPut(ctx context.Context, t *testing.T, bao openbao.Client, path string, data map[string]any) {
	t.Helper()
	_, version, err := secretsReadKV(ctx, bao, path)
	if err != nil && !errors.Is(err, openbao.ErrNotFound) {
		t.Fatal(err)
	}
	if version == "" {
		version = "0"
	}
	mustWrite(ctx, t, bao, secretsKV2+"/data/"+path, map[string]any{"data": data, "options": map[string]any{"cas": version}})
}

// secretsExpectKV checks that the secret of the KVSecret scenario holds want.
func secretsExpectKV(ctx context.Context, bao openbao.Client, want map[string]any) error {
	data, _, err := secretsReadKV(ctx, bao, secretsKVAppPath)
	if err != nil {
		return err
	}
	for key, value := range want {
		if data[key] != value {
			return fmt.Errorf("key %s differs", key)
		}
	}
	return nil
}

func secretsExpectFields(ctx context.Context, bao openbao.Client, path string, want map[string]string) error {
	data, err := bao.Read(ctx, path)
	if err != nil {
		return err
	}
	for key, value := range want {
		if got := fmt.Sprint(data[key]); got != value {
			return fmt.Errorf("%s = %s, want %s", key, got, value)
		}
	}
	return nil
}

// secretsExpectNoWrites checks that openbao_writes_total for kind stays flat over three drift intervals.
func secretsExpectNoWrites(t *testing.T, kind string) {
	t.Helper()
	before := secretsWrites(t, kind)
	if before == 0 {
		t.Fatalf("no %s writes counted; the metric lookup is broken", kind)
	}
	stayFor(t, kind+" writes nothing at rest", 3*driftInterval, func() error {
		if now := secretsWrites(t, kind); now != before {
			return fmt.Errorf("writes went from %v to %v", before, now)
		}
		return nil
	})
}

func secretsWrites(t *testing.T, kind string) float64 {
	t.Helper()
	families, err := metrics.Registry.Gather()
	if err != nil {
		t.Fatal(err)
	}
	for _, family := range families {
		if family.GetName() != "openbao_writes_total" {
			continue
		}
		for _, m := range family.GetMetric() {
			for _, label := range m.GetLabel() {
				if label.GetName() == "kind" && label.GetValue() == kind {
					return m.GetCounter().GetValue()
				}
			}
		}
	}
	return 0
}

func secretsSetup(ctx context.Context, t *testing.T) env {
	t.Helper()
	root := repoRoot(t)
	baoBin, assets := secretsPrerequisites(t, root)

	addr := startBao(t, baoBin)
	bao, err := openbao.NewPool().Client(ctx, "itest-secrets", openbao.Config{
		Address: addr,
		Timeout: 10 * time.Second,
		Auth: openbao.TokenAuth{
			CredentialKey: "itest-secrets",
			Token:         func(context.Context) (string, error) { return rootToken, nil },
		},
	})
	if err != nil {
		t.Fatalf("openbao client: %v", err)
	}

	crds := make([]string, 0, 3)
	for _, name := range []string{"kvsecrets", "randomsecrets", "kvconfigs"} {
		crds = append(crds, filepath.Join(root, "config", "crd", "bases", "secrets.openbaoconfig.io_"+name+".yaml"))
	}
	testEnv := &envtest.Environment{
		BinaryAssetsDirectory: assets,
		CRDInstallOptions:     envtest.CRDInstallOptions{Paths: crds, ErrorIfPathMissing: true},
	}
	cfg, err := testEnv.Start()
	if err != nil {
		t.Fatalf("envtest: %v", err)
	}
	t.Cleanup(func() {
		if err := testEnv.Stop(); err != nil {
			t.Logf("stop envtest: %v", err)
		}
	})

	scheme := runtime.NewScheme()
	for _, add := range []func(*runtime.Scheme) error{clientgoscheme.AddToScheme, secretsv1alpha1.AddToScheme} {
		if err := add(scheme); err != nil {
			t.Fatal(err)
		}
	}
	mgr, err := ctrl.NewManager(cfg, ctrl.Options{
		Scheme:                 scheme,
		Metrics:                metricsserver.Options{BindAddress: "0"},
		HealthProbeBindAddress: "0",
		Controller:             config.Controller{SkipNameValidation: new(true)},
	})
	if err != nil {
		t.Fatal(err)
	}
	conns := staticConnections{client: bao}
	opts := itestEngineOptions()
	for _, setupKind := range []func(ctrl.Manager, engine.Connections, engine.Options) error{
		secrets.SetupKVConfig, secrets.SetupKVSecret, secrets.SetupRandomSecret,
	} {
		if err := setupKind(mgr, conns, opts); err != nil {
			t.Fatal(err)
		}
	}

	mgrCtx, stopMgr := context.WithCancel(ctx)
	done := make(chan error, 1)
	go func() { done <- mgr.Start(mgrCtx) }()
	t.Cleanup(func() {
		stopMgr()
		if err := <-done; err != nil {
			t.Logf("manager: %v", err)
		}
	})
	return env{k8s: mgr.GetClient(), bao: bao}
}

func secretsPrerequisites(t *testing.T, root string) (baoBin, assets string) {
	t.Helper()
	return testenv.RequireBao(t), testenv.RequireAssets(t, root)
}
