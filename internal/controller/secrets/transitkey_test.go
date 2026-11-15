package secrets

import (
	"context"
	"reflect"
	"slices"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"

	commonv1alpha1 "github.com/alexandrust88/openbao-config-operator/api/common/v1alpha1"
	secretsv1alpha1 "github.com/alexandrust88/openbao-config-operator/api/secrets/v1alpha1"
	"github.com/alexandrust88/openbao-config-operator/internal/engine"
)

func fullTransitKey() *secretsv1alpha1.TransitKey {
	return &secretsv1alpha1.TransitKey{
		ObjectMeta: metav1.ObjectMeta{Name: "app", Namespace: "team"},
		Spec: secretsv1alpha1.TransitKeySpec{
			MountPath:            "transit",
			Type:                 "aes256-gcm96",
			Derived:              new(false),
			Exportable:           new(false),
			AllowPlaintextBackup: new(false),
			AutoRotatePeriod:     &metav1.Duration{Duration: 24 * time.Hour},
			MinDecryptionVersion: ptr.To[int32](1),
			MinEncryptionVersion: ptr.To[int32](0),
			DeletionAllowed:      new(false),
		},
	}
}

// transitGoldenRead is `bao read transit/keys/app` on OpenBao 2.7.2 for fullTransitKey.
func transitGoldenRead() map[string]any {
	return map[string]any{
		"allow_plaintext_backup": false, "auto_rotate_period": 86400, "deletion_allowed": false, "derived": false,
		"exportable": false, "imported_key": false, "keys": map[string]any{"1": 1794609027}, "latest_version": 1,
		"min_available_version": 0, "min_decryption_version": 1, "min_encryption_version": 0, "name": "app",
		"soft_deleted": false, "supports_decryption": true, "supports_derivation": true, "supports_encryption": true,
		"supports_signing": false, "type": "aes256-gcm96",
	}
}

func TestTransitKeyPath(t *testing.T) {
	k := fullTransitKey()
	if got := (TransitKeyAdapter{}).Path(k); got != "transit/keys/app" {
		t.Errorf("Path = %q", got)
	}
	k.Spec.MountPath, k.Spec.Name = "crypto/team", "billing"
	if got := (TransitKeyAdapter{}).Path(k); got != "crypto/team/keys/billing" {
		t.Errorf("Path = %q", got)
	}
}

func TestTransitKeyDesired(t *testing.T) {
	k := fullTransitKey()
	k.Spec.Type = "hmac"
	k.Spec.KeySize = ptr.To[int32](64)
	k.Spec.Derived = new(true)
	k.Spec.ConvergentEncryption = new(true)
	k.Spec.Exportable = new(true)
	k.Spec.RotateNonce = "2026-11"
	got, err := TransitKeyAdapter{}.Desired(context.Background(), nil, k)
	if err != nil {
		t.Fatal(err)
	}
	want := engine.Payload{
		"type": "hmac", "derived": true, "convergent_encryption": true, "key_size": int64(64), "exportable": true,
		"auto_rotate_period": int64(86400), "min_decryption_version": int64(1), "min_encryption_version": int64(0),
		"deletion_allowed": false,
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Desired = %v, want %v; the rotate nonce is not an OpenBao field", got, want)
	}

	minimal := &secretsv1alpha1.TransitKey{Spec: secretsv1alpha1.TransitKeySpec{Type: "aes256-gcm96", Exportable: new(false)}}
	got, _ = TransitKeyAdapter{}.Desired(context.Background(), nil, minimal)
	wantMinimal := engine.Payload{"type": "aes256-gcm96", "auto_rotate_period": int64(0), "min_decryption_version": int64(1),
		"min_encryption_version": int64(0), "deletion_allowed": false}
	if !reflect.DeepEqual(got, wantMinimal) {
		t.Errorf("Desired(minimal) = %v; unset fields carry the server default, false one-way fields are left out", got)
	}
}

func TestTransitKeyGoldenRead(t *testing.T) {
	a := TransitKeyAdapter{}
	k := fullTransitKey()
	desired, _ := a.Desired(context.Background(), nil, k)
	c := &engineStubClient{data: map[string]map[string]any{"transit/keys/app": transitGoldenRead()}}
	observed, found, err := a.Observe(context.Background(), c, k)
	if err != nil || !found {
		t.Fatalf("Observe = found %v, err %v", found, err)
	}
	if diff := engine.Diff(desired, observed, a.Fields()); len(diff) != 0 {
		t.Errorf("steady state differs in %v", diff)
	}

	tampered := transitGoldenRead()
	tampered["min_decryption_version"] = 2
	tampered["auto_rotate_period"] = 3600
	c.data["transit/keys/app"] = tampered
	observed, _, _ = a.Observe(context.Background(), c, k)
	if diff := engine.Diff(desired, observed, a.Fields()); !slices.Equal(diff, []string{"auto_rotate_period", "min_decryption_version"}) {
		t.Errorf("Diff = %v, want auto_rotate_period and min_decryption_version", diff)
	}
}

func TestTransitKeyObserve(t *testing.T) {
	a := TransitKeyAdapter{}
	k := fullTransitKey()
	c := &engineStubClient{data: map[string]map[string]any{}}
	if _, found, err := a.Observe(context.Background(), c, k); found || err != nil {
		t.Errorf("absent key: found %v, err %v", found, err)
	}

	other := transitGoldenRead()
	other["type"] = "chacha20-poly1305"
	c.data["transit/keys/app"] = other
	if _, _, err := a.Observe(context.Background(), c, k); err == nil {
		t.Error("a key with another type must be an error, it cannot be converged")
	}
}

func TestTransitKeyRotationNonce(t *testing.T) {
	a := TransitKeyAdapter{}
	k := fullTransitKey()
	k.Status.RotationNonce = "one"
	k.Spec.RotateNonce = "two"
	if got := a.RotationNonce(k); got != "two" {
		t.Errorf("RotationNonce = %q, want the spec value two", got)
	}
	k.Spec.RotateNonce = ""
	if got := a.RotationNonce(k); got != "one" {
		t.Errorf("RotationNonce after removal = %q, want the recorded one so that nothing is written", got)
	}
}

func TestTransitKeyWrite(t *testing.T) {
	a := TransitKeyAdapter{}
	k := fullTransitKey()
	k.Spec.Type = "external-key"
	k.Spec.ExternalKeyRef = "hsm:app"
	k.Spec.AutoRotatePeriod = nil
	k.Spec.RotateNonce = "one"
	desired, _ := a.Desired(context.Background(), nil, k)
	c := &engineStubClient{}
	if _, err := a.Write(context.Background(), c, k, desired, true); err != nil {
		t.Fatal(err)
	}
	want := []engineStubCall{
		{op: "write", path: "transit/keys/app", data: map[string]any{"type": "external-key", "derived": false, "external_key_ref": "hsm:app",
			"auto_rotate_period": int64(0)}},
		{op: "write", path: "transit/keys/app/config", data: map[string]any{
			"min_decryption_version": int64(1), "min_encryption_version": int64(0), "deletion_allowed": false, "auto_rotate_period": int64(0)}},
	}
	if !reflect.DeepEqual(c.calls, want) {
		t.Errorf("create calls = %v, want %v", c.calls, want)
	}

	k.Status.Acquired = commonv1alpha1.AcquiredCreated
	k.Status.RotatedNonce = "one"
	c.calls = nil
	update := engine.StripCreateOnly(desired, a.Fields())
	if _, err := a.Write(context.Background(), c, k, update, false); err != nil {
		t.Fatal(err)
	}
	if len(c.calls) != 1 || c.calls[0].path != "transit/keys/app/config" {
		t.Errorf("update without a new nonce: calls = %v, want only the config write", c.calls)
	}

	k.Spec.RotateNonce = "two"
	c.calls = nil
	if _, err := a.Write(context.Background(), c, k, update, false); err != nil {
		t.Fatal(err)
	}
	if len(c.calls) != 2 || c.calls[1].path != "transit/keys/app/rotate" {
		t.Errorf("update with a new nonce: calls = %v, want config then rotate", c.calls)
	}
}

func TestKeyMaterialKindsAreDestructive(t *testing.T) {
	for _, kind := range []engine.Destructive{TransitKeyAdapter{}, PKIIssuerAdapter{}, PKIKeyAdapter{}, SSHCAAdapter{}, TOTPKeyAdapter{}} {
		if !kind.Destructive() {
			t.Errorf("%T must need the destructive-delete annotation", kind)
		}
	}
}

func TestTransitKeyDelete(t *testing.T) {
	a := TransitKeyAdapter{}
	k := fullTransitKey()
	c := &engineStubClient{data: map[string]map[string]any{"transit/keys/app": transitGoldenRead()}}
	if err := a.Delete(context.Background(), c, k); err != nil {
		t.Fatal(err)
	}
	want := []engineStubCall{
		{op: "write", path: "transit/keys/app/config", data: map[string]any{"deletion_allowed": true}},
		{op: "delete", path: "transit/keys/app"},
	}
	if !reflect.DeepEqual(c.calls, want) {
		t.Errorf("calls = %v, want %v", c.calls, want)
	}

	c = &engineStubClient{}
	if err := a.Delete(context.Background(), c, k); err != nil || len(c.calls) != 0 {
		t.Errorf("absent key: err %v, calls %v", err, c.calls)
	}
}

func TestTransitKeyWriteStatus(t *testing.T) {
	k := fullTransitKey()
	k.Spec.RotateNonce = "two"
	observed := transitGoldenRead()
	observed["latest_version"] = 3
	if err := (TransitKeyAdapter{}).WriteStatus(context.Background(), nil, k, observed); err != nil {
		t.Fatal(err)
	}
	if k.Status.LatestVersion != 3 || k.Status.RotatedNonce != "two" {
		t.Errorf("status = %+v", k.Status)
	}
	k.Spec.RotateNonce = ""
	if err := (TransitKeyAdapter{}).WriteStatus(context.Background(), nil, k, observed); err != nil {
		t.Fatal(err)
	}
	if k.Status.RotatedNonce != "two" {
		t.Errorf("rotatedNonce after removing spec.rotateNonce = %q, want two kept", k.Status.RotatedNonce)
	}
}
