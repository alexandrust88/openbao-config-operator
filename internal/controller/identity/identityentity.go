package identity

import (
	"context"
	"maps"

	ctrl "sigs.k8s.io/controller-runtime"

	identityv1alpha1 "github.com/alexandrust88/openbao-config-operator/api/identity/v1alpha1"
	"github.com/alexandrust88/openbao-config-operator/internal/engine"
	"github.com/alexandrust88/openbao-config-operator/internal/openbao"
)

// +kubebuilder:rbac:groups=identity.obco.dev,resources=identityentities,verbs=get;list;watch;update;patch
// +kubebuilder:rbac:groups=identity.obco.dev,resources=identityentities/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=identity.obco.dev,resources=identityentities/finalizers,verbs=update

// SetupIdentityEntity registers the IdentityEntity controller with the manager.
func SetupIdentityEntity(mgr ctrl.Manager, conns engine.Connections, opts engine.Options) error {
	r := engine.New[*identityv1alpha1.IdentityEntity](
		mgr.GetClient(),
		mgr.GetScheme(),
		mgr.GetEventRecorder("identityentity-controller"),
		conns,
		IdentityEntityAdapter{},
		func() *identityv1alpha1.IdentityEntity { return &identityv1alpha1.IdentityEntity{} },
		opts,
	)
	return r.SetupWithManager(mgr)
}

// IdentityEntityAdapter maps an IdentityEntity onto identity/entity/name/<name>, which reads and upserts by name.
type IdentityEntityAdapter struct{}

var (
	_ engine.Adapter[*identityv1alpha1.IdentityEntity]      = IdentityEntityAdapter{}
	_ engine.StatusWriter[*identityv1alpha1.IdentityEntity] = IdentityEntityAdapter{}
	_ engine.Marker[*identityv1alpha1.IdentityEntity]       = IdentityEntityAdapter{}
	_ engine.Destructive                                    = IdentityEntityAdapter{}
)

// Path implements engine.Adapter.
func (IdentityEntityAdapter) Path(e *identityv1alpha1.IdentityEntity) string {
	return "identity/entity/name/" + specOrObjectName(e.Spec.Name, e.Name)
}

// Desired implements engine.Adapter.
func (IdentityEntityAdapter) Desired(_ context.Context, _ engine.Resolver, e *identityv1alpha1.IdentityEntity) (engine.Payload, error) {
	return engine.Payload{
		"policies": listOrEmpty(e.Spec.Policies),
		"metadata": mapOrEmpty(e.Spec.Metadata),
		"disabled": boolOr(e.Spec.Disabled, false),
	}, nil
}

// Fields implements engine.Adapter.
func (IdentityEntityAdapter) Fields() engine.FieldSpec {
	return engine.FieldSpec{Sets: []string{"policies"}}
}

// Destructive implements engine.Destructive: deleting an entity erases its enrolled MFA TOTP secrets.
func (IdentityEntityAdapter) Destructive() bool { return true }

// WriteStatus implements engine.StatusWriter.
func (IdentityEntityAdapter) WriteStatus(_ context.Context, _ openbao.Client, e *identityv1alpha1.IdentityEntity, observed engine.Payload) error {
	e.Status.ID, _ = observed["id"].(string)
	return nil
}

// Mark implements engine.Marker.
func (IdentityEntityAdapter) Mark(_ *identityv1alpha1.IdentityEntity, desired engine.Payload, m engine.OwnershipMarker) engine.Payload {
	return markMetadata(desired, m)
}

// MarkerOf implements engine.Marker.
func (IdentityEntityAdapter) MarkerOf(observed engine.Payload) (engine.OwnershipMarker, bool) {
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
