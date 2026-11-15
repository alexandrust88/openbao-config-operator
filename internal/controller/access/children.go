package access

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"path"
	"slices"
	"strings"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"sigs.k8s.io/controller-runtime/pkg/client"

	accessv1alpha1 "github.com/alexandrust88/openbao-config-operator/api/access/v1alpha1"
	commonv1alpha1 "github.com/alexandrust88/openbao-config-operator/api/common/v1alpha1"
	"github.com/alexandrust88/openbao-config-operator/internal/engine"
	"github.com/alexandrust88/openbao-config-operator/internal/openbao"
	"github.com/alexandrust88/openbao-config-operator/internal/refs"
)

// staleKey holds the owned objects that the spec no longer plans; desired is always empty.
const staleKey = "stale"

type getFunc func(ctx context.Context, key client.ObjectKey, obj client.Object) error

func policyChild(namespace, name string) accessv1alpha1.AccessChild {
	return accessv1alpha1.AccessChild{Kind: accessv1alpha1.AccessChildPolicy, Namespace: namespace,
		Path: "sys/policies/acl/" + name}
}

// childKey is the payload key of a child; it names the object in events.
func childKey(c accessv1alpha1.AccessChild) string {
	name := path.Base(c.Path)
	if c.Namespace != "" {
		name = c.Namespace + "/" + name
	}
	prefix := strings.ToLower(string(c.Kind))
	if c.Kind == accessv1alpha1.AccessChildKubernetesAuthRole {
		prefix = "role"
	}
	return prefix + ":" + name
}

func sameChild(a, b accessv1alpha1.AccessChild) bool {
	return a.Kind == b.Kind && a.Namespace == b.Namespace && a.Path == b.Path
}

func compareChildren(a, b accessv1alpha1.AccessChild) int {
	return cmp.Or(cmp.Compare(a.Namespace, b.Namespace), cmp.Compare(a.Path, b.Path), cmp.Compare(a.Kind, b.Kind))
}

// staleChildren returns the recorded children that planned no longer contains.
func staleChildren(recorded, planned []accessv1alpha1.AccessChild) []accessv1alpha1.AccessChild {
	var stale []accessv1alpha1.AccessChild
	for _, child := range recorded {
		if !slices.ContainsFunc(planned, func(p accessv1alpha1.AccessChild) bool { return sameChild(p, child) }) {
			stale = append(stale, child)
		}
	}
	return stale
}

func childPaths(children []accessv1alpha1.AccessChild) []string {
	paths := make([]string, 0, len(children))
	for _, child := range children {
		paths = append(paths, path.Join(child.Namespace, child.Path))
	}
	return paths
}

// errChildNotOwned refuses a planned child that exists in OpenBao but is not in the children ledger.
var errChildNotOwned = errors.New("exists and is not owned by this resource; set creationPolicy to Adopt or " +
	"CreateOrAdopt to take it over")

// checkChildren applies creationPolicy to each existing child of an acquired resource: one in the ledger is ours,
// any other is refused under Create. Before acquisition the engine applies creationPolicy to the children as a
// whole. Adopted children are never recorded, so a delete only removes what the resource created.
func checkChildren(m commonv1alpha1.Managed, recorded, existing []accessv1alpha1.AccessChild) error {
	switch m.GetResourceSpec().CreationPolicy {
	case commonv1alpha1.CreationPolicyAdopt, commonv1alpha1.CreationPolicyCreateOrAdopt:
		return nil
	}
	if observing(m) || m.GetResourceStatus().Acquired == "" {
		return nil
	}
	foreign := childPaths(staleChildren(existing, recorded))
	if len(foreign) == 0 {
		return nil
	}
	return fmt.Errorf("%s %w", strings.Join(foreign, ", "), errChildNotOwned)
}

// checkChildOwners refuses a planned child that another resource owns, whatever the creationPolicy.
func checkChildOwners(ctx context.Context, reader refs.Reader, c openbao.Client, m engine.Object,
	planned []accessv1alpha1.AccessChild) error {
	if reader == nil || observing(m) {
		return nil
	}
	serverID, _, err := engine.ClusterIDOf(ctx, c)
	if err != nil {
		return err
	}
	base := strings.Trim(c.Namespace(), "/")
	for _, child := range planned {
		absPath := openbao.CanonicalPath(path.Join(base, child.Namespace, child.Path))
		other, owned, err := engine.OtherChildOwner(ctx, reader, reader.Scheme(), m, absPath, serverID)
		if err != nil {
			return err
		}
		if !owned {
			continue
		}
		err = fmt.Errorf("%s is managed by %s", absPath, other)
		// ErrConflict drops the resource's claim, which would lock a Create resource out of the children it created.
		if m.GetResourceStatus().Acquired != "" {
			return err
		}
		return fmt.Errorf("%w: %w", engine.ErrConflict, err)
	}
	return nil
}

func recordChild(children *[]accessv1alpha1.AccessChild, child accessv1alpha1.AccessChild) {
	if slices.ContainsFunc(*children, func(c accessv1alpha1.AccessChild) bool { return sameChild(c, child) }) {
		return
	}
	*children = append(*children, child)
	slices.SortFunc(*children, compareChildren)
}

func forgetChild(children *[]accessv1alpha1.AccessChild, child accessv1alpha1.AccessChild) {
	*children = slices.DeleteFunc(*children, func(c accessv1alpha1.AccessChild) bool { return sameChild(c, child) })
}

func sortedChildren(children []accessv1alpha1.AccessChild) []accessv1alpha1.AccessChild {
	sorted := slices.Clone(children)
	slices.SortFunc(sorted, compareChildren)
	return sorted
}

func inNamespace(c openbao.Client, namespace string) openbao.Client {
	if namespace == "" {
		return c
	}
	return c.WithNamespace(namespace)
}

// deleteChildren deletes children and forgets each one in recorded once it is gone. Roles and groups go
// before policies, so an interrupted delete leaves less access, never more.
func deleteChildren(ctx context.Context, c openbao.Client, children []accessv1alpha1.AccessChild,
	recorded *[]accessv1alpha1.AccessChild) error {
	ordered := slices.Clone(children)
	isPolicy := func(c accessv1alpha1.AccessChild) int {
		if c.Kind == accessv1alpha1.AccessChildPolicy {
			return 1
		}
		return 0
	}
	slices.SortStableFunc(ordered, func(a, b accessv1alpha1.AccessChild) int {
		return cmp.Compare(isPolicy(a), isPolicy(b))
	})
	for _, child := range ordered {
		err := inNamespace(c, child.Namespace).Delete(ctx, child.Path)
		if err != nil && !errors.Is(err, openbao.ErrNotFound) {
			return fmt.Errorf("delete %s: %w", path.Join(child.Namespace, child.Path), err)
		}
		forgetChild(recorded, child)
	}
	return nil
}

// readIfExists reads p and reports found=false on a 404.
func readIfExists(ctx context.Context, c openbao.Client, p string) (map[string]any, bool, error) {
	data, err := c.Read(ctx, p)
	if errors.Is(err, openbao.ErrNotFound) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("read %s: %w", p, err)
	}
	return data, true, nil
}

// resolveLevels reads the cited AccessLevels of a namespace by name.
func resolveLevels(ctx context.Context, get getFunc, namespace string, names []string) (map[string]accessv1alpha1.AccessLevelSpec, error) {
	levels := make(map[string]accessv1alpha1.AccessLevelSpec, len(names))
	for _, name := range names {
		if _, ok := levels[name]; ok {
			continue
		}
		var level accessv1alpha1.AccessLevel
		if err := get(ctx, client.ObjectKey{Namespace: namespace, Name: name}, &level); err != nil {
			if apierrors.IsNotFound(err) && !errors.Is(err, engine.ErrReferenceNotReady) {
				err = fmt.Errorf("%w: %w", engine.ErrReferenceNotReady, err)
			}
			return nil, fmt.Errorf("access level %s: %w", name, err)
		}
		levels[name] = level.Spec
	}
	return levels, nil
}

// lookupAuthMount finds an auth method in the sys/auth table by path or by accessor and checks its type.
func lookupAuthMount(ctx context.Context, c openbao.Client, mountPath, accessor string, types ...string) (string, string, error) {
	table, err := c.Read(ctx, "sys/auth")
	if err != nil {
		return "", "", fmt.Errorf("read sys/auth: %w", err)
	}
	mountPath = strings.Trim(mountPath, "/")
	for key, raw := range table {
		entry, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		entryPath := strings.TrimSuffix(key, "/")
		entryAccessor, _ := entry["accessor"].(string)
		if (mountPath == "" || entryPath != mountPath) && (accessor == "" || entryAccessor != accessor) {
			continue
		}
		if entryType, _ := entry["type"].(string); !slices.Contains(types, entryType) {
			return "", "", fmt.Errorf("auth method %s has type %s, want %s", entryPath, entryType, strings.Join(types, " or "))
		}
		return entryPath, entryAccessor, nil
	}
	return "", "", fmt.Errorf("auth method %s not found in namespace %q: %w", cmp.Or(mountPath, accessor),
		strings.Trim(c.Namespace(), "/"), engine.ErrReferenceNotReady)
}

// observing reports managementPolicy Observe, under which the children ledger is left as Manage last wrote it.
func observing(m commonv1alpha1.Managed) bool {
	return m.GetResourceSpec().ManagementPolicy == commonv1alpha1.ManagementPolicyObserve
}
