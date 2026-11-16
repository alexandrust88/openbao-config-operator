package secrets

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"strings"

	ctrl "sigs.k8s.io/controller-runtime"

	secretsv1alpha1 "github.com/alexandrust88/openbao-config-operator/api/secrets/v1alpha1"
	sysv1alpha1 "github.com/alexandrust88/openbao-config-operator/api/sys/v1alpha1"
	"github.com/alexandrust88/openbao-config-operator/internal/engine"
	"github.com/alexandrust88/openbao-config-operator/internal/openbao"
)

// +kubebuilder:rbac:groups=secrets.openbaoconfig.io,resources=kvsecretmetadata,verbs=get;list;watch;update;patch
// +kubebuilder:rbac:groups=secrets.openbaoconfig.io,resources=kvsecretmetadata/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=secrets.openbaoconfig.io,resources=kvsecretmetadata/finalizers,verbs=update

// SetupKVSecretMetadata registers the KVSecretMetadata controller with the manager.
func SetupKVSecretMetadata(mgr ctrl.Manager, conns engine.Connections, opts engine.Options) error {
	r := engine.New[*secretsv1alpha1.KVSecretMetadata](
		mgr.GetClient(),
		mgr.GetScheme(),
		mgr.GetEventRecorder("kvsecretmetadata-controller"),
		conns,
		KVSecretMetadataAdapter{},
		func() *secretsv1alpha1.KVSecretMetadata { return &secretsv1alpha1.KVSecretMetadata{} },
		opts,
	)
	return r.SetupWithManager(mgr)
}

// KVSecretMetadataAdapter maps a KVSecretMetadata onto <mount>/metadata/<path>.
type KVSecretMetadataAdapter struct{}

const kvMetadataCustomKey = "custom_metadata"

var (
	_ engine.Adapter[*secretsv1alpha1.KVSecretMetadata]        = KVSecretMetadataAdapter{}
	_ engine.FieldsProvider[*secretsv1alpha1.KVSecretMetadata] = KVSecretMetadataAdapter{}
	_ engine.Marker[*secretsv1alpha1.KVSecretMetadata]         = KVSecretMetadataAdapter{}
	_ engine.Writer[*secretsv1alpha1.KVSecretMetadata]         = KVSecretMetadataAdapter{}
	_ engine.Destructive                                       = KVSecretMetadataAdapter{}

	kvMetadataManagedByKey = sysv1alpha1.CustomMetadataMarkerPrefix + "managed-by"
	kvMetadataClusterIDKey = sysv1alpha1.CustomMetadataMarkerPrefix + "cluster-id"
	kvMetadataUIDKey       = sysv1alpha1.CustomMetadataMarkerPrefix + "uid"
)

// Path implements engine.Adapter.
func (KVSecretMetadataAdapter) Path(m *secretsv1alpha1.KVSecretMetadata) string {
	return m.Spec.MountPath + "/metadata/" + m.Spec.Path
}

// Desired implements engine.Adapter.
func (KVSecretMetadataAdapter) Desired(_ context.Context, _ engine.Resolver, m *secretsv1alpha1.KVSecretMetadata) (engine.Payload, error) {
	p := engine.Payload{}
	optInt32(p, "max_versions", m.Spec.MaxVersions)
	optBool(p, "cas_required", m.Spec.CASRequired)
	optBool(p, "metadata_cas_required", m.Spec.MetadataCASRequired)
	if m.Spec.DeleteVersionAfter != nil {
		seconds, err := durationSeconds(*m.Spec.DeleteVersionAfter)
		if err != nil {
			return nil, fmt.Errorf("spec.deleteVersionAfter: %w", err)
		}
		p["delete_version_after"] = seconds
	}
	if m.Spec.CustomMetadata != nil {
		p[kvMetadataCustomKey] = maps.Clone(m.Spec.CustomMetadata)
	}
	return p, nil
}

// Fields implements engine.Adapter.
func (KVSecretMetadataAdapter) Fields() engine.FieldSpec {
	return engine.FieldSpec{
		Durations: []string{"delete_version_after"},
		ServerOwned: []string{
			"created_time", "current_version", "deletion_time",
			"oldest_version", "updated_time", "versions",
		},
	}
}

// FieldsFor implements engine.FieldsProvider. An omitted customMetadata is not the whole map, so extra stored keys
// are not drift; the marker keys are still compared.
func (KVSecretMetadataAdapter) FieldsFor(_ context.Context, _ engine.Resolver, m *secretsv1alpha1.KVSecretMetadata) (engine.FieldSpec, error) {
	if m.Spec.CustomMetadata != nil {
		return engine.FieldSpec{}, nil
	}
	return engine.FieldSpec{Partial: []string{kvMetadataCustomKey}}, nil
}

// Destructive implements engine.Destructive: deleting the resource destroys the secret and every version.
func (KVSecretMetadataAdapter) Destructive() bool { return true }

// Write implements engine.Writer. Once metadata check-and-set is required, OpenBao rejects a metadata write that
// does not send metadata_cas, so the current metadata version is attached here and kept out of the diff. The stored
// keys and that version come from the same read.
func (a KVSecretMetadataAdapter) Write(ctx context.Context, c openbao.Client, m *secretsv1alpha1.KVSecretMetadata, desired engine.Payload, _ bool) (engine.Payload, error) {
	payload := maps.Clone(desired)
	existing, err := c.Read(ctx, a.Path(m))
	if err != nil && !errors.Is(err, openbao.ErrNotFound) {
		return nil, err
	}
	if m.Spec.CustomMetadata == nil {
		kvMetadataMergeStored(payload, existing)
	}
	version, required, err := kvMetadataCAS(ctx, c, m.Spec.MountPath, existing)
	if err != nil {
		return nil, err
	}
	if required {
		payload["metadata_cas"] = version
	}
	return c.Write(ctx, a.Path(m), payload)
}

// kvMetadataMergeStored copies stored user keys into an omitted custom_metadata map so writing the ownership
// marker does not drop them. A set map replaces the stored keys and skips this.
func kvMetadataMergeStored(payload engine.Payload, existing map[string]any) {
	if existing == nil {
		return
	}
	stored, _ := existing[kvMetadataCustomKey].(map[string]any)
	merged := make(map[string]string, len(stored)+3)
	for k, v := range stored {
		if strings.HasPrefix(k, sysv1alpha1.CustomMetadataMarkerPrefix) {
			continue
		}
		s, ok := v.(string)
		if ok {
			merged[k] = s
		}
	}
	marked, _ := payload[kvMetadataCustomKey].(map[string]string)
	maps.Copy(merged, marked)
	payload[kvMetadataCustomKey] = merged
}

func kvMetadataCAS(ctx context.Context, c openbao.Client, mount string, existing map[string]any) (version int64, required bool, err error) {
	if existing != nil {
		version, _ = asInt64(existing["current_metadata_version"])
		if on, _ := existing["metadata_cas_required"].(bool); on {
			return version, true, nil
		}
	}
	config, err := c.Read(ctx, mount+"/config")
	if errors.Is(err, openbao.ErrNotFound) {
		return version, false, nil
	}
	if err != nil {
		return 0, false, err
	}
	on, _ := config["metadata_cas_required"].(bool)
	return version, on, nil
}

// Mark implements engine.Marker.
func (KVSecretMetadataAdapter) Mark(_ *secretsv1alpha1.KVSecretMetadata, desired engine.Payload, marker engine.OwnershipMarker) engine.Payload {
	user, _ := desired[kvMetadataCustomKey].(map[string]string)
	metadata := make(map[string]string, len(user)+3)
	maps.Copy(metadata, user)
	metadata[kvMetadataManagedByKey] = marker.ManagedBy
	metadata[kvMetadataUIDKey] = marker.UID
	if marker.ClusterID != "" {
		metadata[kvMetadataClusterIDKey] = marker.ClusterID
	}
	marked := maps.Clone(desired)
	marked[kvMetadataCustomKey] = metadata
	return marked
}

// MarkerOf implements engine.Marker.
func (KVSecretMetadataAdapter) MarkerOf(observed engine.Payload) (engine.OwnershipMarker, bool) {
	metadata, _ := observed[kvMetadataCustomKey].(map[string]any)
	uid, _ := metadata[kvMetadataUIDKey].(string)
	if uid == "" {
		return engine.OwnershipMarker{}, false
	}
	managedBy, _ := metadata[kvMetadataManagedByKey].(string)
	clusterID, _ := metadata[kvMetadataClusterIDKey].(string)
	return engine.OwnershipMarker{ManagedBy: managedBy, ClusterID: clusterID, UID: uid}, true
}
