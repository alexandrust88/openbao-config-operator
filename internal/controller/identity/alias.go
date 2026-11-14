package identity

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"k8s.io/apimachinery/pkg/api/meta"
	"sigs.k8s.io/controller-runtime/pkg/client"

	commonv1alpha1 "github.com/alexandrust88/openbao-config-operator/api/common/v1alpha1"
	"github.com/alexandrust88/openbao-config-operator/internal/engine"
	"github.com/alexandrust88/openbao-config-operator/internal/openbao"
	"github.com/alexandrust88/openbao-config-operator/internal/refs"
)

// Keys of an entity or group alias payload.
const (
	aliasNameKey        = "name"
	aliasAccessorKey    = "mount_accessor"
	aliasCanonicalIDKey = "canonical_id"
)

// aliasKey is the identity of an entity or group alias in OpenBao: (mount accessor, name).
type aliasKey struct {
	accessor string
	name     string
}

func (k aliasKey) matches(alias map[string]any) bool {
	name, _ := alias[aliasNameKey].(string)
	accessor, _ := alias[aliasAccessorKey].(string)
	return accessor == k.accessor && strings.EqualFold(name, k.name)
}

func authMountRef(accessor, path string, ref *commonv1alpha1.LocalObjectReference, sel *commonv1alpha1.LabelSelector) refs.Ref {
	return refs.Ref{Literal: accessor, Name: path, ObjectName: objectRefName(ref), MatchLabels: selectorLabels(sel)}
}

func aliasPath(collection, id string) string {
	if id == "" {
		return collection
	}
	return collection + "/id/" + id
}

// readAlias reads an alias by its recorded ID; found is false when there is no ID or the alias is gone.
func readAlias(ctx context.Context, c openbao.Client, collection, id string) (engine.Payload, bool, error) {
	if id == "" {
		return nil, false, nil
	}
	path := collection + "/id/" + id
	alias, err := c.Read(ctx, path)
	if errors.Is(err, openbao.ErrNotFound) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("read %s: %w", path, err)
	}
	return alias, true, nil
}

// checkAliasOwner returns an error wrapping engine.ErrConflict when a resource of list's kind, other than obj, holds collection/id/<id> on
// the server behind c. Before obj has acquired the alias its path may be the shared collection, and the engine's
// path index prefers the older resource, so neither stops a takeover of an alias that another resource manages.
func checkAliasOwner(ctx context.Context, reader client.Reader, c openbao.Client, obj engine.Object,
	list client.ObjectList, collection, id string) error {
	if observing(obj) {
		return nil
	}
	absPath := collection + "/id/" + id
	if ns := strings.Trim(c.Namespace(), "/"); ns != "" {
		absPath = ns + "/" + absPath
	}
	if err := reader.List(ctx, list, client.MatchingFields{engine.PathIndex: absPath}); err != nil {
		return fmt.Errorf("list owners of %s: %w", absPath, err)
	}
	items, err := meta.ExtractList(list)
	if err != nil {
		return err
	}
	clusterID, _, err := engine.ClusterIDOf(ctx, c)
	if err != nil {
		return err
	}
	for _, item := range items {
		other, ok := item.(engine.Object)
		if !ok || other.GetUID() == obj.GetUID() || !slices.Contains(engine.PathIndexFunc(other), absPath) {
			continue
		}
		if recorded := other.GetResourceStatus().ServerID; recorded != "" && clusterID != "" && recorded != clusterID {
			continue
		}
		return fmt.Errorf("%w: %s is managed by %s/%s", engine.ErrConflict, absPath, other.GetNamespace(), other.GetName())
	}
	return nil
}

// lookup posts an alias lookup to identity/lookup/entity or identity/lookup/group. OpenBao answers 204,
// which the client returns as nil data, when nothing matches.
func lookup(ctx context.Context, c openbao.Client, path string, key aliasKey) (engine.Payload, error) {
	data, err := c.Write(ctx, path, map[string]any{"alias_name": key.name, "alias_mount_accessor": key.accessor})
	if err != nil {
		return nil, fmt.Errorf("write %s: %w", path, err)
	}
	return data, nil
}

// aliasIDFrom returns the ID of an existing alias from observe, which reads it by status.id or looks it up.
func aliasIDFrom(observe func() (engine.Payload, bool, error), aliasName string) (string, error) {
	alias, found, err := observe()
	if err != nil {
		return "", err
	}
	id, _ := alias["id"].(string)
	if !found || id == "" {
		return "", fmt.Errorf("alias %q has no ID to update", aliasName)
	}
	return id, nil
}

// writeAlias creates an alias on collection, or updates the existing one by ID.
func writeAlias(ctx context.Context, c openbao.Client, collection string, desired engine.Payload, create bool,
	id func() (string, error)) (engine.Payload, error) {
	path := collection
	if !create {
		aliasID, err := id()
		if err != nil {
			return nil, err
		}
		path += "/id/" + aliasID
	}
	response, err := c.Write(ctx, path, desired)
	if err != nil {
		return nil, fmt.Errorf("write %s: %w", path, err)
	}
	return response, nil
}

func deleteAlias(ctx context.Context, c openbao.Client, collection, id string) error {
	if id == "" {
		return nil
	}
	path := collection + "/id/" + id
	if err := c.Delete(ctx, path); err != nil && !errors.Is(err, openbao.ErrNotFound) {
		return fmt.Errorf("delete %s: %w", path, err)
	}
	return nil
}
