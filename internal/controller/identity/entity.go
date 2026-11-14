package identity

import (
	"context"
	"maps"

	ctrl "sigs.k8s.io/controller-runtime"

	identityv1alpha1 "github.com/alexandrust88/openbao-config-operator/api/identity/v1alpha1"
	"github.com/alexandrust88/openbao-config-operator/internal/engine"
	"github.com/alexandrust88/openbao-config-operator/internal/openbao"
)

// +kubebuilder:rbac:groups=identity.openbaoconfig.io,resources=entities,verbs=get;list;watch;update;patch
// +kubebuilder:rbac:groups=identity.openbaoconfig.io,resources=entities/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=identity.openbaoconfig.io,resources=entities/finalizers,verbs=update

// SetupEntity registers the Entity controller with the manager.
func SetupEntity(mgr ctrl.Manager, conns engine.Connections, opts engine.Options) error {
	r := engine.New[*identityv1alpha1.Entity](
		mgr.GetClient(),
		mgr.GetScheme(),
		mgr.GetEventRecorder("entity-controller"),
		conns,
		EntityAdapter{},
		func() *identityv1alpha1.Entity { return &identityv1alpha1.Entity{} },
		opts,
	)
	return r.SetupWithManager(mgr)
}

// EntityAdapter maps an Entity onto identity/entity/name/<name>, which reads and upserts by name.
type EntityAdapter struct{}

var (
	_ engine.Adapter[*identityv1alpha1.Entity]      = EntityAdapter{}
	_ engine.StatusWriter[*identityv1alpha1.Entity] = EntityAdapter{}
	_ engine.Marker[*identityv1alpha1.Entity]       = EntityAdapter{}
)

// Path implements engine.Adapter.
func (EntityAdapter) Path(e *identityv1alpha1.Entity) string {
	return "identity/entity/name/" + specOrObjectName(e.Spec.Name, e.Name)
}

// Desired implements engine.Adapter.
func (EntityAdapter) Desired(_ context.Context, _ engine.Resolver, e *identityv1alpha1.Entity) (engine.Payload, error) {
	return engine.Payload{
		"policies": listOrEmpty(e.Spec.Policies),
		"metadata": mapOrEmpty(e.Spec.Metadata),
		"disabled": boolOr(e.Spec.Disabled, false),
	}, nil
}

// Fields implements engine.Adapter.
func (EntityAdapter) Fields() engine.FieldSpec {
	return engine.FieldSpec{Sets: []string{"policies"}}
}

// WriteStatus implements engine.StatusWriter.
func (EntityAdapter) WriteStatus(_ context.Context, _ openbao.Client, e *identityv1alpha1.Entity, observed engine.Payload) error {
	e.Status.ID, _ = observed["id"].(string)
	return nil
}

// Mark implements engine.Marker.
func (EntityAdapter) Mark(_ *identityv1alpha1.Entity, desired engine.Payload, m engine.OwnershipMarker) engine.Payload {
	return markMetadata(desired, m)
}

// MarkerOf implements engine.Marker.
func (EntityAdapter) MarkerOf(observed engine.Payload) (engine.OwnershipMarker, bool) {
	return metadataMarker(observed)
}

// Ownership marker keys in entity and group metadata. OpenBao allows only [a-zA-Z0-9=/+_-] in metadata keys, so
// they carry a prefix instead of a domain; the CRDs refuse user keys with this prefix.
const (
	markerManagedByKey = identityv1alpha1.MetadataMarkerPrefix + "managed-by"
	markerClusterIDKey = identityv1alpha1.MetadataMarkerPrefix + "cluster-id"
	markerUIDKey       = identityv1alpha1.MetadataMarkerPrefix + "uid"
)

// markMetadata returns desired with the marker added to a copy of its metadata.
func markMetadata(desired engine.Payload, m engine.OwnershipMarker) engine.Payload {
	user, _ := desired["metadata"].(map[string]string)
	metadata := make(map[string]string, len(user)+3)
	maps.Copy(metadata, user)
	metadata[markerManagedByKey] = m.ManagedBy
	metadata[markerUIDKey] = m.UID
	if m.ClusterID != "" {
		metadata[markerClusterIDKey] = m.ClusterID
	}
	marked := maps.Clone(desired)
	marked["metadata"] = metadata
	return marked
}

// metadataMarker reads the marker from the metadata of an observed entity or group.
func metadataMarker(observed engine.Payload) (engine.OwnershipMarker, bool) {
	metadata, _ := observed["metadata"].(map[string]any)
	uid, _ := metadata[markerUIDKey].(string)
	if uid == "" {
		return engine.OwnershipMarker{}, false
	}
	managedBy, _ := metadata[markerManagedByKey].(string)
	clusterID, _ := metadata[markerClusterIDKey].(string)
	return engine.OwnershipMarker{ManagedBy: managedBy, ClusterID: clusterID, UID: uid}, true
}

func specOrObjectName(specName, objectName string) string {
	if specName != "" {
		return specName
	}
	return objectName
}
