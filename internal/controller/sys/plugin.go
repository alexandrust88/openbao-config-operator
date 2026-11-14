package sys

import (
	"context"
	"errors"
	"fmt"
	"strings"

	ctrl "sigs.k8s.io/controller-runtime"

	sysv1alpha1 "github.com/alexandrust88/openbao-config-operator/api/sys/v1alpha1"
	"github.com/alexandrust88/openbao-config-operator/internal/engine"
	"github.com/alexandrust88/openbao-config-operator/internal/openbao"
)

// +kubebuilder:rbac:groups=sys.openbaoconfig.io,resources=plugins,verbs=get;list;watch;update;patch
// +kubebuilder:rbac:groups=sys.openbaoconfig.io,resources=plugins/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=sys.openbaoconfig.io,resources=plugins/finalizers,verbs=update
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch

const pluginCatalogPath = "sys/plugins/catalog"

// SetupPlugin registers the Plugin controller with the manager.
func SetupPlugin(mgr ctrl.Manager, conns engine.Connections, opts engine.Options) error {
	r := engine.New[*sysv1alpha1.Plugin](
		mgr.GetClient(),
		mgr.GetScheme(),
		mgr.GetEventRecorder("plugin-controller"),
		conns,
		PluginAdapter{},
		func() *sysv1alpha1.Plugin { return &sysv1alpha1.Plugin{} },
		opts,
	)
	return r.SetupWithManager(mgr)
}

// PluginAdapter maps a Plugin onto sys/plugins/catalog/<type>/<name>.
//
// A versioned plugin is read and deleted with a version query parameter that openbao.Client cannot send, so
// it is observed through the catalog listing, which carries sha256 and version only.
type PluginAdapter struct{}

var (
	_ engine.Adapter[*sysv1alpha1.Plugin]          = PluginAdapter{}
	_ engine.Observer[*sysv1alpha1.Plugin]         = PluginAdapter{}
	_ engine.SecretReferencer[*sysv1alpha1.Plugin] = PluginAdapter{}
	_ engine.ReadBackSkipper[*sysv1alpha1.Plugin]  = PluginAdapter{}
)

// SkipReadBack implements engine.ReadBackSkipper: the catalog listing of a versioned plugin cannot confirm command,
// args and oci.
func (PluginAdapter) SkipReadBack(p *sysv1alpha1.Plugin) bool { return p.Spec.Version != "" }

// Path implements engine.Adapter.
func (PluginAdapter) Path(p *sysv1alpha1.Plugin) string {
	return pluginCatalogPath + "/" + string(p.Spec.Type) + "/" + nameOrDefault(p.Spec.Name, p.Name)
}

// Desired implements engine.Adapter. A registration replaces the whole catalog entry, so args and oci are
// always sent; env values come from Secrets.
func (PluginAdapter) Desired(ctx context.Context, r engine.Resolver, p *sysv1alpha1.Plugin) (engine.Payload, error) {
	payload := engine.Payload{
		"sha256":  p.Spec.SHA256,
		"command": p.Spec.Command,
		"args":    stringsOrEmpty(p.Spec.Args),
		"oci":     p.Spec.OCI,
	}
	if version := pluginVersion(p); version != "" {
		payload["version"] = version
	}
	if len(p.Spec.Env) > 0 {
		env := make([]string, 0, len(p.Spec.Env))
		for _, e := range p.Spec.Env {
			value, err := r.SecretValue(ctx, p.Namespace, e.SecretKeyRef)
			if err != nil {
				return nil, fmt.Errorf("env %s: %w", e.Name, err)
			}
			env = append(env, e.Name+"="+string(value))
		}
		payload["env"] = env
	}
	return payload, nil
}

// Fields implements engine.Adapter.
func (PluginAdapter) Fields() engine.FieldSpec { return engine.FieldSpec{WriteOnly: []string{"env"}} }

// SecretRefs implements engine.SecretReferencer.
func (PluginAdapter) SecretRefs(p *sysv1alpha1.Plugin) []string {
	names := make([]string, 0, len(p.Spec.Env))
	for _, e := range p.Spec.Env {
		names = append(names, e.SecretKeyRef.Name)
	}
	return names
}

// Observe implements engine.Observer. A read of an unregistered name returns the builtin plugin of that name,
// which counts as absent.
func (a PluginAdapter) Observe(ctx context.Context, c openbao.Client, p *sysv1alpha1.Plugin) (engine.Payload, bool, error) {
	if err := requireRootNamespace(c, "Plugin"); err != nil {
		return nil, false, err
	}
	if p.Spec.Version != "" {
		return a.observeVersioned(ctx, c, p)
	}
	data, err := c.Read(ctx, a.Path(p))
	if err != nil && !errors.Is(err, openbao.ErrNotFound) {
		return nil, false, err
	}
	if err == nil {
		if builtin, _ := data["builtin"].(bool); !builtin {
			return data, true, nil
		}
	}
	// A plugin registered without a version is stored under the version its binary reports, if any.
	entry, err := catalogEntry(ctx, c, p, func(version string) bool { return version != "" })
	if err != nil {
		return nil, false, err
	}
	if entry != nil {
		return nil, false, fmt.Errorf("plugin %s is registered under the version %v that it reports; set spec.version",
			a.Path(p), entry["version"])
	}
	return nil, false, nil
}

// observeVersioned reports sha256 and version from the catalog listing. command, args and oci cannot be read:
// they are reported as the spec once the current generation is reconciled, so a spec change still writes, and the
// read after that write is skipped (SkipReadBack).
func (PluginAdapter) observeVersioned(ctx context.Context, c openbao.Client, p *sysv1alpha1.Plugin) (engine.Payload, bool, error) {
	version := pluginVersion(p)
	entry, err := catalogEntry(ctx, c, p, func(v string) bool { return v == version })
	if err != nil || entry == nil {
		return nil, false, err
	}
	observed := engine.Payload{"sha256": entry["sha256"], "version": entry["version"]}
	if p.Status.Acquired != "" && engine.Reconciled(p) {
		observed["command"] = p.Spec.Command
		observed["args"] = stringsOrEmpty(p.Spec.Args)
		observed["oci"] = p.Spec.OCI
	}
	return observed, true, nil
}

// catalogEntry returns the external catalog entry of the plugin's type and name whose version matches.
func catalogEntry(ctx context.Context, c openbao.Client, p *sysv1alpha1.Plugin, match func(version string) bool) (map[string]any, error) {
	data, err := c.Read(ctx, pluginCatalogPath)
	if errors.Is(err, openbao.ErrNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", pluginCatalogPath, err)
	}
	detailed, _ := data["detailed"].([]any)
	name := nameOrDefault(p.Spec.Name, p.Name)
	for _, raw := range detailed {
		entry, _ := raw.(map[string]any)
		builtin, _ := entry["builtin"].(bool)
		version, _ := entry["version"].(string)
		if !builtin && entry[keyType] == string(p.Spec.Type) && entry["name"] == name && match(version) {
			return entry, nil
		}
	}
	return nil, nil
}

// pluginVersion returns spec.version in the canonical form OpenBao stores, with a leading "v".
func pluginVersion(p *sysv1alpha1.Plugin) string {
	if p.Spec.Version == "" {
		return ""
	}
	return "v" + strings.TrimPrefix(p.Spec.Version, "v")
}
