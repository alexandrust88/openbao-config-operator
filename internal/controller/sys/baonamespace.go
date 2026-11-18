package sys

import (
	"context"
	"errors"
	"fmt"
	"maps"

	ctrl "sigs.k8s.io/controller-runtime"

	sysv1alpha1 "github.com/alexandrust88/openbao-config-operator/api/sys/v1alpha1"
	"github.com/alexandrust88/openbao-config-operator/internal/engine"
	"github.com/alexandrust88/openbao-config-operator/internal/openbao"
)

// +kubebuilder:rbac:groups=sys.obco.dev,resources=baonamespaces,verbs=get;list;watch;update;patch
// +kubebuilder:rbac:groups=sys.obco.dev,resources=baonamespaces/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=sys.obco.dev,resources=baonamespaces/finalizers,verbs=update

// SetupBaoNamespace registers the BaoNamespace controller with the manager.
func SetupBaoNamespace(mgr ctrl.Manager, conns engine.Connections, opts engine.Options) error {
	r := engine.New[*sysv1alpha1.BaoNamespace](
		mgr.GetClient(),
		mgr.GetScheme(),
		mgr.GetEventRecorder("baonamespace-controller"),
		conns,
		BaoNamespaceAdapter{},
		func() *sysv1alpha1.BaoNamespace { return &sysv1alpha1.BaoNamespace{} },
		opts,
	)
	return r.SetupWithManager(mgr)
}

// BaoNamespaceAdapter maps a BaoNamespace onto sys/namespaces/<name> in its parent namespace.
type BaoNamespaceAdapter struct{}

var (
	_ engine.Adapter[*sysv1alpha1.BaoNamespace]      = BaoNamespaceAdapter{}
	_ engine.Observer[*sysv1alpha1.BaoNamespace]     = BaoNamespaceAdapter{}
	_ engine.Deleter[*sysv1alpha1.BaoNamespace]      = BaoNamespaceAdapter{}
	_ engine.StatusWriter[*sysv1alpha1.BaoNamespace] = BaoNamespaceAdapter{}
	_ engine.Destructive                             = BaoNamespaceAdapter{}
)

// Path implements engine.Adapter.
func (BaoNamespaceAdapter) Path(n *sysv1alpha1.BaoNamespace) string {
	return "sys/namespaces/" + namespaceName(n)
}

// Desired implements engine.Adapter. custom_metadata is always sent, empty when the spec sets none, because a
// write replaces the stored set.
func (BaoNamespaceAdapter) Desired(_ context.Context, _ engine.Resolver, n *sysv1alpha1.BaoNamespace) (engine.Payload, error) {
	metadata := make(map[string]string, len(n.Spec.CustomMetadata))
	maps.Copy(metadata, n.Spec.CustomMetadata)
	return engine.Payload{namespaceMetadataKey: metadata}, nil
}

// Fields implements engine.Adapter.
func (BaoNamespaceAdapter) Fields() engine.FieldSpec { return engine.FieldSpec{} }

// Observe implements engine.Observer.
func (a BaoNamespaceAdapter) Observe(ctx context.Context, c openbao.Client, n *sysv1alpha1.BaoNamespace) (engine.Payload, bool, error) {
	observed, err := c.Read(ctx, a.Path(n))
	if errors.Is(err, openbao.ErrNotFound) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	if tainted, _ := observed["tainted"].(bool); tainted {
		return nil, false, fmt.Errorf("namespace %s is still being deleted: %w", namespaceName(n), engine.ErrInProgress)
	}
	return observed, true, nil
}

// Delete implements engine.Deleter. OpenBao deletes a namespace asynchronously, so Delete reports
// engine.ErrInProgress until a read no longer finds it.
func (a BaoNamespaceAdapter) Delete(ctx context.Context, c openbao.Client, n *sysv1alpha1.BaoNamespace) error {
	path := a.Path(n)
	if err := c.Delete(ctx, path); err != nil && !errors.Is(err, openbao.ErrNotFound) {
		if hasChildNamespaces(err) {
			return fmt.Errorf("namespace %s contains child namespaces; delete them first: %w", namespaceName(n), err)
		}
		return err
	}
	_, err := c.Read(ctx, path)
	if errors.Is(err, openbao.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	return fmt.Errorf("namespace %s is being deleted: %w", namespaceName(n), engine.ErrInProgress)
}

// WriteStatus implements engine.StatusWriter: status.id is the namespace ID.
func (BaoNamespaceAdapter) WriteStatus(_ context.Context, _ openbao.Client, n *sysv1alpha1.BaoNamespace, observed engine.Payload) error {
	if id, ok := observed["id"].(string); ok {
		n.Status.ID = id
	}
	return nil
}

// hasChildNamespaces matches the refusal to delete a parent namespace: 409 in the code, 400 on the wire in 2.7.
func hasChildNamespaces(err error) bool {
	return statusIs(err, 409) || badRequestContains(err, "child namespaces")
}

func namespaceName(n *sysv1alpha1.BaoNamespace) string { return nameOrDefault(n.Spec.Name, n.Name) }

// Destructive implements engine.Destructive: deleting it destroys the data it holds.
func (BaoNamespaceAdapter) Destructive() bool { return true }

// Ownership marker keys in custom_metadata; the CRD refuses user keys with this prefix.
const (
	namespaceMetadataKey  = "custom_metadata"
	namespaceManagedByKey = sysv1alpha1.CustomMetadataMarkerPrefix + "managed-by"
	namespaceClusterIDKey = sysv1alpha1.CustomMetadataMarkerPrefix + "cluster-id"
	namespaceUIDKey       = sysv1alpha1.CustomMetadataMarkerPrefix + "uid"
)

// Mark implements engine.Marker: the marker goes into a copy of custom_metadata.
func (BaoNamespaceAdapter) Mark(_ *sysv1alpha1.BaoNamespace, desired engine.Payload, m engine.OwnershipMarker) engine.Payload {
	user, _ := desired[namespaceMetadataKey].(map[string]string)
	metadata := make(map[string]string, len(user)+3)
	maps.Copy(metadata, user)
	metadata[namespaceManagedByKey] = m.ManagedBy
	metadata[namespaceUIDKey] = m.UID
	if m.ClusterID != "" {
		metadata[namespaceClusterIDKey] = m.ClusterID
	}
	marked := maps.Clone(desired)
	marked[namespaceMetadataKey] = metadata
	return marked
}

// MarkerOf implements engine.Marker.
func (BaoNamespaceAdapter) MarkerOf(observed engine.Payload) (engine.OwnershipMarker, bool) {
	metadata, _ := observed[namespaceMetadataKey].(map[string]any)
	uid, _ := metadata[namespaceUIDKey].(string)
	if uid == "" {
		return engine.OwnershipMarker{}, false
	}
	managedBy, _ := metadata[namespaceManagedByKey].(string)
	clusterID, _ := metadata[namespaceClusterIDKey].(string)
	return engine.OwnershipMarker{ManagedBy: managedBy, ClusterID: clusterID, UID: uid}, true
}
