package sys

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	commonv1alpha1 "github.com/alexandrust88/openbao-config-operator/api/common/v1alpha1"
	sysv1alpha1 "github.com/alexandrust88/openbao-config-operator/api/sys/v1alpha1"
	"github.com/alexandrust88/openbao-config-operator/internal/engine"
	"github.com/alexandrust88/openbao-config-operator/internal/openbao"
)

// Mount tables under sys/.
const (
	authTable            = "auth"
	secretsTable         = "mounts"
	keyType              = "type"
	keyListingVisibility = "listing_visibility"
	pluginNotInCatalog   = "plugin not found in the catalog"
	optionVersion        = "version"
)

// mountFieldSpec compares mounts. Desired is flat: the read's config block and user_lockout_config are
// flattened by observeMount, and writeMount nests them again.
var mountFieldSpec = engine.FieldSpec{
	CreateOnly: []string{keyType, "local", "seal_wrap", "external_entropy_access", keyForceNoCache},
	Sets: []string{
		"audit_non_hmac_request_keys", "audit_non_hmac_response_keys",
		"passthrough_request_headers", "allowed_response_headers",
	},
	Durations: []string{
		"default_lease_ttl", "max_lease_ttl",
		"user_lockout_duration", "user_lockout_counter_reset_duration",
	},
}

// enableKeys are sent when the mount is enabled; every other key is set through tune.
var enableKeys = []string{keyType, "description", "local", "seal_wrap", "external_entropy_access", "options"}

// enableConfigKeys are sent in the config block when the mount is enabled.
var enableConfigKeys = []string{"plugin_version", keyForceNoCache}

// lockoutKeys maps the flat read names of user_lockout_config to the names tune accepts.
var lockoutKeys = map[string]string{
	"user_lockout_threshold":              "lockout_threshold",
	"user_lockout_duration":               "lockout_duration",
	"user_lockout_counter_reset_duration": "lockout_counter_reset_duration",
	"user_lockout_disable":                "lockout_disable",
}

// mountPayload returns every mount field, an omitted one at its OpenBao default. plugin_version is sent only when
// set, because tune rejects "" and a pinned version cannot be unpinned. Tune merges options into the stored ones,
// so writeMount deletes the keys the spec dropped.
func mountPayload(typ string, f sysv1alpha1.MountFields) engine.Payload {
	p := engine.Payload{
		keyType:                        typ,
		"description":                  "",
		"local":                        false,
		"seal_wrap":                    false,
		"external_entropy_access":      false,
		keyForceNoCache:                false,
		"default_lease_ttl":            int64(0),
		"max_lease_ttl":                int64(0),
		keyListingVisibility:           "",
		"audit_non_hmac_request_keys":  stringsOrEmpty(f.AuditNonHMACRequestKeys),
		"audit_non_hmac_response_keys": stringsOrEmpty(f.AuditNonHMACResponseKeys),
		"passthrough_request_headers":  stringsOrEmpty(f.PassthroughRequestHeaders),
		"allowed_response_headers":     stringsOrEmpty(f.AllowedResponseHeaders),
	}
	if f.Description != nil {
		p["description"] = *f.Description
	}
	putBool(p, "local", f.Local)
	putBool(p, "seal_wrap", f.SealWrap)
	putBool(p, "external_entropy_access", f.ExternalEntropyAccess)
	putBool(p, keyForceNoCache, f.ForceNoCache)
	if f.PluginVersion != "" {
		p["plugin_version"] = f.PluginVersion
	}
	options := make(map[string]string, len(f.Options))
	for key, value := range f.Options {
		options[key] = string(value)
	}
	p["options"] = options
	putSeconds(p, "default_lease_ttl", f.DefaultLeaseTTL)
	putSeconds(p, "max_lease_ttl", f.MaxLeaseTTL)
	if f.ListingVisibility != "" {
		p[keyListingVisibility] = string(f.ListingVisibility)
	}
	return p
}

// observeMount finds path in the mount table. The table is used instead of sys/<table>/<path>, which answers
// 400 for a missing mount and, for secrets engines, matches the longest mount prefix. The read omits
// listing_visibility at its default. A create-only field that differs is left to the engine, which recreates a
// destructive kind only with the allow-destructive-delete annotation.
func observeMount(ctx context.Context, c openbao.Client, table, path string) (engine.Payload, bool, error) {
	mounts, err := c.Read(ctx, "sys/"+table)
	if errors.Is(err, openbao.ErrNotFound) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("read sys/%s: %w", table, err)
	}
	entry, ok := mounts[path+"/"].(map[string]any)
	if !ok {
		return nil, false, nil
	}
	observed := engine.Payload{}
	for key, value := range entry {
		if key != "config" {
			observed[key] = value
		}
	}
	config, _ := entry["config"].(map[string]any)
	for key, value := range config {
		if key != "user_lockout_config" {
			observed[key] = value
		}
	}
	lockout, _ := config["user_lockout_config"].(map[string]any)
	maps.Copy(observed, lockout)
	if _, ok := observed[keyListingVisibility]; !ok {
		observed[keyListingVisibility] = ""
	}
	return observed, true, nil
}

// writeMount enables the mount on create and tunes the remaining keys; an update only tunes.
func writeMount(ctx context.Context, c openbao.Client, table, path string, desired engine.Payload, create bool) error {
	base := "sys/" + table + "/" + path
	tune := engine.Payload{}
	lockout := map[string]any{}
	enable := engine.Payload{}
	config := map[string]any{}
	for key, value := range desired {
		switch {
		case create && slices.Contains(enableConfigKeys, key):
			config[key] = value
		case slices.Contains(mountFieldSpec.CreateOnly, key) || (create && slices.Contains(enableKeys, key)):
			enable[key] = value
		case lockoutKeys[key] != "":
			if b, ok := value.(bool); ok {
				lockout[lockoutKeys[key]] = b
			} else {
				lockout[lockoutKeys[key]] = fmt.Sprint(value)
			}
		default:
			tune[key] = value
		}
	}
	if len(lockout) > 0 {
		tune["user_lockout_config"] = lockout
	}
	if options, ok := tune["options"].(map[string]string); ok {
		withRemoved, err := withRemovedOptions(ctx, c, table, path, options)
		if err != nil {
			return err
		}
		tune["options"] = withRemoved
	}
	if len(config) > 0 {
		enable["config"] = config
	}
	if create {
		if _, err := c.Write(ctx, base, enable); err != nil {
			if badRequestContains(err, pluginNotInCatalog) {
				// Retrying cannot help until the plugin is registered; recheck at the drift interval.
				return fmt.Errorf("enable %s: %w: %w", base, engine.ErrPluginNotRegistered, err)
			}
			return fmt.Errorf("enable %s: %w", base, err)
		}
	}
	if len(tune) == 0 {
		return nil
	}
	if _, err := c.Write(ctx, base+"/tune", tune); err != nil {
		return fmt.Errorf("tune %s: %w", base, err)
	}
	return nil
}

// withRemovedOptions adds an empty value, which tune treats as a delete, for every stored option that options
// does not set. The KV version is skipped: OpenBao never removes it.
func withRemovedOptions(ctx context.Context, c openbao.Client, table, path string, options map[string]string) (map[string]string, error) {
	current, found, err := observeMount(ctx, c, table, path)
	if err != nil || !found {
		return options, err
	}
	stored, _ := current["options"].(map[string]any)
	out := maps.Clone(options)
	for key := range stored {
		if _, ok := out[key]; !ok && key != optionVersion {
			out[key] = ""
		}
	}
	return out, nil
}

// dropUnmanagedVersion hides the stored KV version option when the spec does not set it: OpenBao never removes
// it, so it would otherwise differ on every comparison.
func dropUnmanagedVersion(observed engine.Payload, managed bool) {
	if options, ok := observed["options"].(map[string]any); ok && !managed {
		delete(options, optionVersion)
	}
}

func writeMountAccessor(status *commonv1alpha1.ResourceStatus, observed engine.Payload) {
	if accessor, ok := observed["accessor"].(string); ok {
		status.ID = accessor
	}
}

func putBool(p engine.Payload, key string, v *bool) {
	if v != nil {
		p[key] = *v
	}
}

func putSeconds(p engine.Payload, key string, d *metav1.Duration) {
	if d != nil {
		p[key] = int64(d.Seconds())
	}
}

// mountPlugin checks the Plugin that pluginRef names: it must be Ready, register the plugin type the mount needs
// and carry the mount's type as its catalog name. It returns the Plugin's catalog version, which may be empty.
func mountPlugin(ctx context.Context, reader client.Reader, namespace, mountType string, pluginType sysv1alpha1.PluginType,
	ref *commonv1alpha1.LocalObjectReference) (string, error) {
	if reader == nil {
		return "", fmt.Errorf("pluginRef %s cannot be resolved without a Kubernetes reader", ref.Name)
	}
	plugin := &sysv1alpha1.Plugin{}
	if err := reader.Get(ctx, client.ObjectKey{Namespace: namespace, Name: ref.Name}, plugin); err != nil {
		if apierrors.IsNotFound(err) {
			return "", fmt.Errorf("the Plugin %s/%s does not exist: %w", namespace, ref.Name, engine.ErrReferenceNotReady)
		}
		return "", err
	}
	if !meta.IsStatusConditionTrue(plugin.Status.Conditions, commonv1alpha1.ConditionReady) {
		return "", fmt.Errorf("the Plugin %s/%s is not ready: %w", namespace, ref.Name, engine.ErrReferenceNotReady)
	}
	if plugin.Spec.Type != pluginType {
		return "", fmt.Errorf("the Plugin %s registers a %s plugin, this mount needs a %s plugin: %w", ref.Name,
			plugin.Spec.Type, pluginType, engine.ErrInvalidSpec)
	}
	if name := nameOrDefault(plugin.Spec.Name, plugin.Name); name != mountType {
		return "", fmt.Errorf("spec.type %q must be the catalog name %q of Plugin %s: %w", mountType, name, ref.Name,
			engine.ErrInvalidSpec)
	}
	return pluginVersion(plugin), nil
}

// pluginReferences returns the Plugin that pluginRef names, for engine.ReferenceWatcher.
func pluginReferences(ref *commonv1alpha1.LocalObjectReference) []engine.ObjectReference {
	if ref == nil {
		return nil
	}
	return []engine.ObjectReference{{Kind: "Plugin", Name: ref.Name}}
}
