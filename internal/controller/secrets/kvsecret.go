// Package secrets holds the adapters for the secrets.openbaoconfig.io kinds.
package secrets

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"slices"

	corev1 "k8s.io/api/core/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	commonv1alpha1 "github.com/alexandrust88/openbao-config-operator/api/common/v1alpha1"
	secretsv1alpha1 "github.com/alexandrust88/openbao-config-operator/api/secrets/v1alpha1"
	"github.com/alexandrust88/openbao-config-operator/internal/engine"
	"github.com/alexandrust88/openbao-config-operator/internal/openbao"
)

// +kubebuilder:rbac:groups=secrets.openbaoconfig.io,resources=kvsecrets,verbs=get;list;watch;update;patch
// +kubebuilder:rbac:groups=secrets.openbaoconfig.io,resources=kvsecrets/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=secrets.openbaoconfig.io,resources=kvsecrets/finalizers,verbs=update
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch

// SetupKVSecret registers the KVSecret controller with the manager.
func SetupKVSecret(mgr ctrl.Manager, conns engine.Connections, opts engine.Options) error {
	r := engine.New[*secretsv1alpha1.KVSecret](
		mgr.GetClient(),
		mgr.GetScheme(),
		mgr.GetEventRecorder("kvsecret-controller"),
		conns,
		KVSecretAdapter{},
		func() *secretsv1alpha1.KVSecret { return &secretsv1alpha1.KVSecret{} },
		opts,
	)
	return r.SetupWithManager(mgr)
}

// KVSecretAdapter maps a KVSecret onto <mount>/data/<path> (KV version 2) or <mount>/<path> (version 1).
//
// The desired payload holds the values under kvValuesKey, which is write-only, and the set of key names under
// kvKeysKey, which is compared with the key names Observe reports. Values are never read back or compared.
type KVSecretAdapter struct{}

const (
	kvValuesKey = "data"
	kvKeysKey   = "keys"
)

var (
	_ engine.Adapter[*secretsv1alpha1.KVSecret]  = KVSecretAdapter{}
	_ engine.Observer[*secretsv1alpha1.KVSecret] = KVSecretAdapter{}
	_ engine.Writer[*secretsv1alpha1.KVSecret]   = KVSecretAdapter{}
	_ engine.Deleter[*secretsv1alpha1.KVSecret]  = KVSecretAdapter{}
	_ engine.Destructive                         = KVSecretAdapter{}

	_ engine.SecretReferencer[*secretsv1alpha1.KVSecret] = KVSecretAdapter{}
)

// Path implements engine.Adapter.
func (KVSecretAdapter) Path(s *secretsv1alpha1.KVSecret) string {
	return kvDataPath(s.Spec.MountPath, s.Spec.Path, s.Spec.KVVersion)
}

// Desired implements engine.Adapter: every key of the dataFrom Secrets, in order, then the data keys.
func (KVSecretAdapter) Desired(ctx context.Context, r engine.Resolver, s *secretsv1alpha1.KVSecret) (engine.Payload, error) {
	values := map[string]any{}
	for _, from := range s.Spec.DataFrom {
		var secret corev1.Secret
		key := client.ObjectKey{Namespace: s.Namespace, Name: from.SecretRef.Name}
		if err := r.Object(ctx, key, &secret); err != nil {
			return nil, fmt.Errorf("read secret %s/%s: %w", key.Namespace, key.Name, err)
		}
		for k, v := range secret.Data {
			values[k] = string(v)
		}
	}
	for k, source := range s.Spec.Data {
		v, err := r.SecretValue(ctx, s.Namespace, source.SecretKeyRef)
		if err != nil {
			return nil, err
		}
		values[k] = string(v)
	}
	return engine.Payload{kvValuesKey: values, kvKeysKey: kvKeyNames(values)}, nil
}

// Fields implements engine.Adapter.
func (KVSecretAdapter) Fields() engine.FieldSpec {
	return engine.FieldSpec{WriteOnly: []string{kvValuesKey}, Partial: []string{kvKeysKey}}
}

// Destructive implements engine.Destructive: deleting the resource removes secret values from OpenBao.
func (KVSecretAdapter) Destructive() bool { return true }

// Observe implements engine.Observer. It reports only the key names present in OpenBao.
func (KVSecretAdapter) Observe(ctx context.Context, c openbao.Client, s *secretsv1alpha1.KVSecret) (engine.Payload, bool, error) {
	current, _, found, err := readKV(ctx, c, s.Spec.MountPath, s.Spec.Path, s.Spec.KVVersion)
	if err != nil || !found {
		return nil, false, err
	}
	return engine.Payload{kvKeysKey: kvKeyNames(current)}, true, nil
}

// Write implements engine.Writer. It overlays the owned keys on the current secret, drops the keys that left the
// spec and writes with check-and-set on KV version 2.
func (KVSecretAdapter) Write(ctx context.Context, c openbao.Client, s *secretsv1alpha1.KVSecret, desired engine.Payload,
	_ bool) (engine.Payload, error) {
	values, _ := desired[kvValuesKey].(map[string]any)
	current, version, _, err := readKV(ctx, c, s.Spec.MountPath, s.Spec.Path, s.Spec.KVVersion)
	if err != nil {
		return nil, err
	}
	merged := maps.Clone(current)
	if merged == nil {
		merged = map[string]any{}
	}
	for _, k := range s.Status.ManagedKeys {
		delete(merged, k)
	}
	maps.Copy(merged, values)
	if err := writeKV(ctx, c, s.Spec.MountPath, s.Spec.Path, s.Spec.KVVersion, merged, version); err != nil {
		return nil, err
	}
	s.Status.ManagedKeys = slices.Sorted(maps.Keys(values))
	return nil, nil
}

// Delete implements engine.Deleter. It removes only the keys the resource wrote; when no other key remains and the
// resource created the path and allows destructive deletes, it also deletes the history on KV version 2.
func (KVSecretAdapter) Delete(ctx context.Context, c openbao.Client, s *secretsv1alpha1.KVSecret) error {
	if err := kvRemoveKeys(ctx, c, s.Spec.MountPath, s.Spec.Path, s.Spec.KVVersion, s.Status.ManagedKeys); err != nil {
		return err
	}
	if !kvOwnsPath(s) {
		return nil
	}
	return kvPurge(ctx, c, s.Spec.MountPath, s.Spec.Path, s.Spec.KVVersion)
}

// SecretRefs implements engine.SecretReferencer.
func (KVSecretAdapter) SecretRefs(s *secretsv1alpha1.KVSecret) []string {
	names := make([]string, 0, len(s.Spec.DataFrom)+len(s.Spec.Data))
	for _, from := range s.Spec.DataFrom {
		names = append(names, from.SecretRef.Name)
	}
	for _, source := range s.Spec.Data {
		names = append(names, source.SecretKeyRef.Name)
	}
	slices.Sort(names)
	return slices.Compact(names)
}

func kvVersion(v int32) int32 {
	if v == 1 {
		return 1
	}
	return 2
}

func kvDataPath(mount, path string, version int32) string {
	if kvVersion(version) == 1 {
		return mount + "/" + path
	}
	return mount + "/data/" + path
}

func kvKeyNames(values map[string]any) map[string]any {
	names := make(map[string]any, len(values))
	for k := range values {
		names[k] = true
	}
	return names
}

// readKV returns the current key/value map of a KV secret and, on version 2, its current version, which is
// also known for a secret whose latest version is deleted. found is false when there is no live data.
func readKV(ctx context.Context, c openbao.Client, mount, path string, version int32) (map[string]any, int64, bool, error) {
	apiPath := kvDataPath(mount, path, version)
	resp, err := c.Read(ctx, apiPath)
	if errors.Is(err, openbao.ErrNotFound) {
		nf, ok := errors.AsType[*openbao.NotFoundDataError](err)
		if !ok || kvVersion(version) == 1 {
			return nil, 0, false, nil
		}
		resp, err = nf.Data, nil
	}
	if err != nil {
		return nil, 0, false, fmt.Errorf("read %s: %w", apiPath, err)
	}
	if kvVersion(version) == 1 {
		return resp, 0, resp != nil, nil
	}
	var current int64
	if metadata, ok := resp["metadata"].(map[string]any); ok {
		current, err = kvInt(metadata["version"])
		if err != nil {
			return nil, 0, false, fmt.Errorf("read %s: metadata.version: %w", apiPath, err)
		}
	}
	data, _ := resp["data"].(map[string]any)
	return data, current, len(data) > 0, nil
}

// kvRemoveKeys removes keys from a KV secret and keeps the other keys. On version 2 it writes the remaining keys,
// possibly none, with check-and-set, so a key written meanwhile makes it fail instead of being lost. Version 1 has
// no check-and-set and refuses empty data, so a secret left without keys is deleted.
func kvRemoveKeys(ctx context.Context, c openbao.Client, mount, path string, version int32, keys []string) error {
	current, cas, found, err := readKV(ctx, c, mount, path, version)
	if err != nil || !found {
		return err
	}
	remaining := maps.Clone(current)
	for _, k := range keys {
		delete(remaining, k)
	}
	if len(remaining) == len(current) {
		return nil
	}
	if len(remaining) > 0 || kvVersion(version) == 2 {
		return writeKV(ctx, c, mount, path, version, remaining, cas)
	}
	apiPath := mount + "/" + path
	if err := c.Delete(ctx, apiPath); err != nil && !errors.Is(err, openbao.ErrNotFound) {
		return fmt.Errorf("delete %s: %w", apiPath, err)
	}
	return nil
}

// kvOwnsPath reports a resource that created its KV path and allows destructive deletes; only such a resource may
// delete the history of the path.
func kvOwnsPath(obj engine.Object) bool {
	return obj.GetResourceStatus().Acquired == commonv1alpha1.AcquiredCreated &&
		obj.GetAnnotations()[commonv1alpha1.AnnotationAllowDestructiveDelete] == "true"
}

// kvPurge deletes a KV version 2 secret with every version through its metadata, unless it holds live keys again.
func kvPurge(ctx context.Context, c openbao.Client, mount, path string, version int32) error {
	if kvVersion(version) == 1 {
		return nil
	}
	if _, _, found, err := readKV(ctx, c, mount, path, version); err != nil || found {
		return err
	}
	apiPath := mount + "/metadata/" + path
	if err := c.Delete(ctx, apiPath); err != nil && !errors.Is(err, openbao.ErrNotFound) {
		return fmt.Errorf("delete %s: %w", apiPath, err)
	}
	return nil
}

// writeKV replaces the data of a KV secret; on version 2 only if its current version is still cas.
func writeKV(ctx context.Context, c openbao.Client, mount, path string, version int32, data map[string]any, cas int64) error {
	apiPath := kvDataPath(mount, path, version)
	body := data
	if kvVersion(version) == 2 {
		body = map[string]any{"data": data, "options": map[string]any{"cas": cas}}
	}
	if _, err := c.Write(ctx, apiPath, body); err != nil {
		return fmt.Errorf("write %s: %w", apiPath, err)
	}
	return nil
}

func kvInt(v any) (int64, error) {
	switch n := v.(type) {
	case json.Number:
		return n.Int64()
	case float64:
		return int64(n), nil
	case int64:
		return n, nil
	case int:
		return int64(n), nil
	}
	return 0, fmt.Errorf("unexpected type %T", v)
}
