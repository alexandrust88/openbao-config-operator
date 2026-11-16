package secrets

import (
	"context"
	"errors"
	"slices"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"

	secretsv1alpha1 "github.com/alexandrust88/openbao-config-operator/api/secrets/v1alpha1"
	"github.com/alexandrust88/openbao-config-operator/internal/engine"
	"github.com/alexandrust88/openbao-config-operator/internal/openbao"
)

func TestKVSecretMetadataPathAndDesired(t *testing.T) {
	a := KVSecretMetadataAdapter{}
	after := intstr.FromString("768h")
	m := &secretsv1alpha1.KVSecretMetadata{
		ObjectMeta: metav1.ObjectMeta{Name: "meta", Namespace: "team"},
		Spec: secretsv1alpha1.KVSecretMetadataSpec{
			MountPath:          "secret",
			Path:               "team/app",
			MaxVersions:        new(int32(5)),
			CASRequired:        new(true),
			DeleteVersionAfter: &after,
			CustomMetadata:     map[string]string{"owner": "team"},
		},
	}
	if got := a.Path(m); got != "secret/metadata/team/app" {
		t.Errorf("path = %q", got)
	}
	desired, err := a.Desired(context.Background(), nil, m)
	if err != nil {
		t.Fatal(err)
	}
	if desired["max_versions"] != int64(5) || desired["cas_required"] != true || desired["delete_version_after"] != int64(768*3600) {
		t.Fatalf("desired = %v", desired)
	}
	if meta, _ := desired["custom_metadata"].(map[string]string); meta["owner"] != "team" {
		t.Fatalf("custom_metadata = %v", desired["custom_metadata"])
	}
	if _, ok := desired["metadata_cas_required"]; ok {
		t.Fatal("an unset metadataCASRequired must not be sent")
	}
	if _, ok := desired["metadata_cas"]; ok {
		t.Fatal("metadata_cas is attached at write time")
	}
	m.Spec.MetadataCASRequired = new(true)
	desired, err = a.Desired(context.Background(), nil, m)
	if err != nil {
		t.Fatal(err)
	}
	if desired["metadata_cas_required"] != true {
		t.Fatalf("metadata_cas_required = %v", desired["metadata_cas_required"])
	}
	if !a.Destructive() {
		t.Fatal("metadata delete must be destructive")
	}
}

func TestKVSecretMetadataSteadyStateAgainstGoldenRead(t *testing.T) {
	a := KVSecretMetadataAdapter{}
	after := intstr.FromString("768h")
	m := &secretsv1alpha1.KVSecretMetadata{
		Spec: secretsv1alpha1.KVSecretMetadataSpec{
			MountPath:          "secret",
			Path:               "team/app",
			MaxVersions:        new(int32(5)),
			CASRequired:        new(true),
			DeleteVersionAfter: &after,
			CustomMetadata:     map[string]string{"owner": "team"},
		},
	}
	desired, err := a.Desired(context.Background(), nil, m)
	if err != nil {
		t.Fatal(err)
	}
	golden := map[string]any{
		"cas_required":          true,
		"custom_metadata":       map[string]any{"owner": "team"},
		"delete_version_after":  "768h0m0s",
		"max_versions":          5.0,
		"current_version":       2.0,
		"metadata_cas_required": false,
		"versions":              map[string]any{"1": map[string]any{}, "2": map[string]any{}},
	}
	if d := engine.Diff(desired, golden, a.Fields()); len(d) != 0 {
		t.Fatalf("steady state differs on %v", d)
	}
	golden["max_versions"] = 10.0
	if d := engine.Diff(desired, golden, a.Fields()); !slices.Equal(d, []string{"max_versions"}) {
		t.Fatalf("differing %v", d)
	}
}

func TestKVSecretMetadataCustomMetadataReplaces(t *testing.T) {
	a := KVSecretMetadataAdapter{}
	m := &secretsv1alpha1.KVSecretMetadata{Spec: secretsv1alpha1.KVSecretMetadataSpec{
		MountPath: "secret", Path: "team/app", CustomMetadata: map[string]string{"owner": "team"},
	}}
	desired, err := a.Desired(context.Background(), nil, m)
	if err != nil {
		t.Fatal(err)
	}
	marker := engine.OwnershipMarker{ManagedBy: "obc", UID: "uid", ClusterID: "c"}
	marked := a.Mark(m, desired, marker)
	observed := map[string]any{"custom_metadata": map[string]any{
		"owner": "team", "stale": "gone",
		"openbaoconfig-managed-by": "obc", "openbaoconfig-uid": "uid", "openbaoconfig-cluster-id": "c",
	}}
	if d := engine.Diff(marked, observed, a.Fields()); !slices.Contains(d, "custom_metadata") {
		t.Fatalf("a key removed from the spec was not detected: %v", d)
	}
	m.Spec.CustomMetadata = nil
	desired, err = a.Desired(context.Background(), nil, m)
	if err != nil {
		t.Fatal(err)
	}
	marked = a.Mark(m, desired, marker)
	fields, err := a.FieldsFor(context.Background(), nil, m)
	if err != nil {
		t.Fatal(err)
	}
	kept := map[string]any{"custom_metadata": map[string]any{
		"owner":                    "team",
		"openbaoconfig-managed-by": "obc", "openbaoconfig-uid": "uid", "openbaoconfig-cluster-id": "c",
	}}
	if d := engine.Diff(marked, kept, fields); len(d) != 0 {
		t.Fatalf("an omitted customMetadata must keep stored keys: %v", d)
	}
	if meta, _ := marked["custom_metadata"].(map[string]string); meta["openbaoconfig-uid"] != "uid" {
		t.Fatalf("omitted customMetadata still needs the ownership marker: %v", marked["custom_metadata"])
	}
}

type kvMetaBao struct {
	meta    map[string]any
	metaErr error
	paths   []string
	wrote   map[string]any
}

func (b *kvMetaBao) Read(_ context.Context, path string) (map[string]any, error) {
	b.paths = append(b.paths, path)
	if b.metaErr != nil {
		return nil, b.metaErr
	}
	if b.meta == nil {
		return nil, openbao.ErrNotFound
	}
	return b.meta, nil
}

func (b *kvMetaBao) Write(_ context.Context, _ string, data map[string]any) (map[string]any, error) {
	b.wrote = data
	return nil, nil
}

func (b *kvMetaBao) Delete(context.Context, string) error { return nil }
func (b *kvMetaBao) List(context.Context, string) ([]string, error) {
	return nil, openbao.ErrNotFound
}
func (b *kvMetaBao) WithNamespace(string) openbao.Client { return b }
func (b *kvMetaBao) Namespace() string                   { return "" }

func TestKVSecretMetadataWriteUsesOneRead(t *testing.T) {
	a := KVSecretMetadataAdapter{}
	m := &secretsv1alpha1.KVSecretMetadata{Spec: secretsv1alpha1.KVSecretMetadataSpec{MountPath: "secret", Path: "team/app"}}
	desired := a.Mark(m, engine.Payload{}, engine.OwnershipMarker{ManagedBy: "obc", UID: "uid", ClusterID: "c"})
	bao := &kvMetaBao{meta: map[string]any{
		"custom_metadata":          map[string]any{"owner": "team"},
		"current_metadata_version": float64(4),
		"metadata_cas_required":    true,
	}}
	if _, err := a.Write(context.Background(), bao, m, desired, false); err != nil {
		t.Fatal(err)
	}
	if len(bao.paths) != 1 {
		t.Fatalf("reads = %v, want one", bao.paths)
	}
	meta, _ := bao.wrote["custom_metadata"].(map[string]string)
	if meta["owner"] != "team" || meta["openbaoconfig-uid"] != "uid" {
		t.Fatalf("custom_metadata = %v", meta)
	}
	if bao.wrote["metadata_cas"] != int64(4) {
		t.Fatalf("metadata_cas = %v", bao.wrote["metadata_cas"])
	}
}

func TestKVSecretMetadataWriteReadError(t *testing.T) {
	a := KVSecretMetadataAdapter{}
	m := &secretsv1alpha1.KVSecretMetadata{Spec: secretsv1alpha1.KVSecretMetadataSpec{MountPath: "secret", Path: "team/app"}}
	desired := a.Mark(m, engine.Payload{}, engine.OwnershipMarker{ManagedBy: "obc", UID: "uid"})
	bao := &kvMetaBao{metaErr: errors.New("timeout")}
	if _, err := a.Write(context.Background(), bao, m, desired, false); err == nil {
		t.Fatal("a failed read must not write")
	}
	if bao.wrote != nil {
		t.Fatalf("wrote %v after a failed read", bao.wrote)
	}
}
