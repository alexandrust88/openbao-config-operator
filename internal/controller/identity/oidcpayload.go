package identity

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	commonv1alpha1 "github.com/alexandrust88/openbao-config-operator/api/common/v1alpha1"
	identityv1alpha1 "github.com/alexandrust88/openbao-config-operator/api/identity/v1alpha1"
	"github.com/alexandrust88/openbao-config-operator/internal/engine"
	"github.com/alexandrust88/openbao-config-operator/internal/openbao"
	"github.com/alexandrust88/openbao-config-operator/internal/refs"
)

// Targets for the OIDC and MFA references that the shared refs package does not define.
var (
	oidcClientTarget = refs.Target{GVK: identityv1alpha1.GroupVersion.WithKind("OIDCClient"), Lookup: oidcClientIDByName}
	mfaMethodTarget  = refs.Target{GVK: identityv1alpha1.GroupVersion.WithKind("MFAMethod"), Lookup: mfaMethodIDByName}
)

func oidcName(specName, objectName string) string {
	if specName != "" {
		return specName
	}
	return objectName
}

// oidcDefaultTTL is the server default, in seconds, of the OIDC key, role and client durations.
const oidcDefaultTTL = 24 * 60 * 60

// The helpers below send a field with the server default when the spec omits it, so that the payload carries
// every typed field and removing a field from the spec resets it in OpenBao.

func stringOr(value, def string) string {
	if value != "" {
		return value
	}
	return def
}

func secondsOr(d *metav1.Duration, def int64) int64 {
	if d != nil {
		return int64(d.Seconds())
	}
	return def
}

func boolOr(value *bool, def bool) bool {
	if value != nil {
		return *value
	}
	return def
}

func intOr(value *int32, def int64) int64 {
	if value != nil {
		return int64(*value)
	}
	return def
}

func listOrEmpty(value []string) []string {
	if value != nil {
		return value
	}
	return []string{}
}

func mapOrEmpty(value map[string]string) map[string]string {
	if value != nil {
		return value
	}
	return map[string]string{}
}

// resolveMatches resolves every reference and returns the sorted, distinct values; never nil, so the whole list
// is always written.
func resolveMatches(ctx context.Context, r refs.Resolver, t refs.Target, matches []refs.Ref) ([]string, error) {
	values := []string{}
	for _, m := range matches {
		resolved, err := r.All(ctx, t, m)
		if err != nil {
			return nil, err
		}
		values = append(values, resolved...)
	}
	slices.Sort(values)
	return slices.Compact(values), nil
}

func entityMatchRefs(matches []identityv1alpha1.EntityMatch) []refs.Ref {
	out := make([]refs.Ref, 0, len(matches))
	for _, m := range matches {
		out = append(out, refs.Ref{Literal: m.EntityID, Name: m.EntityName, ObjectName: objectRefName(m.EntityRef),
			MatchLabels: selectorLabels(m.EntitySelector)})
	}
	return out
}

func groupMatchRefs(matches []identityv1alpha1.GroupMatch) []refs.Ref {
	out := make([]refs.Ref, 0, len(matches))
	for _, m := range matches {
		out = append(out, refs.Ref{Literal: m.GroupID, Name: m.GroupName, ObjectName: objectRefName(m.GroupRef),
			MatchLabels: selectorLabels(m.GroupSelector)})
	}
	return out
}

func authMountMatchRefs(matches []identityv1alpha1.AuthMountMatch) []refs.Ref {
	out := make([]refs.Ref, 0, len(matches))
	for _, m := range matches {
		out = append(out, refs.Ref{Literal: m.MountAccessor, Name: m.MountPath, ObjectName: objectRefName(m.MountRef),
			MatchLabels: selectorLabels(m.MountSelector)})
	}
	return out
}

func mfaMethodMatchRefs(matches []identityv1alpha1.MFAMethodMatch) []refs.Ref {
	out := make([]refs.Ref, 0, len(matches))
	for _, m := range matches {
		out = append(out, refs.Ref{Literal: m.MethodID, Name: m.MethodName, ObjectName: objectRefName(m.MethodRef),
			MatchLabels: selectorLabels(m.MethodSelector)})
	}
	return out
}

func oidcClientMatchRefs(matches []identityv1alpha1.OIDCClientMatch) []refs.Ref {
	out := make([]refs.Ref, 0, len(matches))
	for _, m := range matches {
		out = append(out, refs.Ref{Literal: m.ClientID, Name: m.ClientName, ObjectName: objectRefName(m.ClientRef),
			MatchLabels: selectorLabels(m.ClientSelector)})
	}
	return out
}

func objectRefName(ref *commonv1alpha1.LocalObjectReference) string {
	if ref == nil {
		return ""
	}
	return ref.Name
}

func selectorLabels(s *commonv1alpha1.LabelSelector) map[string]string {
	if s == nil {
		return nil
	}
	return s.MatchLabels
}

// oidcClientIDByName returns the client ID of the OIDC client called name.
func oidcClientIDByName(ctx context.Context, c openbao.Client, name string) (string, error) {
	data, err := c.Read(ctx, "identity/oidc/client/"+name)
	if errors.Is(err, openbao.ErrNotFound) {
		return "", fmt.Errorf("oidc client %q not found: %w", name, engine.ErrReferenceNotReady)
	}
	if err != nil {
		return "", fmt.Errorf("read oidc client %q: %w", name, err)
	}
	id, _ := data["client_id"].(string)
	if id == "" {
		return "", fmt.Errorf("oidc client %q has no client_id: %w", name, engine.ErrReferenceNotReady)
	}
	return id, nil
}

// mfaMethodIDByName returns the ID of the MFA method called name in the client's OpenBao namespace.
func mfaMethodIDByName(ctx context.Context, c openbao.Client, name string) (string, error) {
	method, found, err := findMFAMethod(ctx, c, "identity/mfa/method", name)
	if err != nil {
		return "", err
	}
	id, _ := method["id"].(string)
	if !found || id == "" {
		return "", fmt.Errorf("mfa method %q not found: %w", name, engine.ErrReferenceNotReady)
	}
	return id, nil
}

// findMFAMethod lists the method IDs under collection and reads each until one has the given name and lives in
// the client's own OpenBao namespace; listings also include methods of parent namespaces.
func findMFAMethod(ctx context.Context, c openbao.Client, collection, name string) (engine.Payload, bool, error) {
	ids, err := c.List(ctx, collection)
	if errors.Is(err, openbao.ErrNotFound) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("list %s: %w", collection, err)
	}
	namespace := strings.Trim(c.Namespace(), "/")
	for _, id := range ids {
		method, err := c.Read(ctx, collection+"/"+id)
		if errors.Is(err, openbao.ErrNotFound) {
			continue
		}
		if err != nil {
			return nil, false, fmt.Errorf("read %s/%s: %w", collection, id, err)
		}
		path, _ := method["namespace_path"].(string)
		if method["name"] == name && strings.Trim(path, "/") == namespace {
			return method, true, nil
		}
	}
	return nil, false, nil
}
