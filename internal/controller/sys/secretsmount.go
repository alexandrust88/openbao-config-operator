package sys

import (
	"context"

	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	sysv1alpha1 "github.com/alexandrust88/openbao-config-operator/api/sys/v1alpha1"
	"github.com/alexandrust88/openbao-config-operator/internal/engine"
	"github.com/alexandrust88/openbao-config-operator/internal/openbao"
)

// +kubebuilder:rbac:groups=sys.obco.dev,resources=secretsmounts,verbs=get;list;watch;update;patch
// +kubebuilder:rbac:groups=sys.obco.dev,resources=secretsmounts/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=sys.obco.dev,resources=secretsmounts/finalizers,verbs=update
// +kubebuilder:rbac:groups=sys.obco.dev,resources=plugins,verbs=get;list;watch

// SetupSecretsMount registers the SecretsMount controller with the manager.
func SetupSecretsMount(mgr ctrl.Manager, conns engine.Connections, opts engine.Options) error {
	r := engine.New[*sysv1alpha1.SecretsMount](
		mgr.GetClient(),
		mgr.GetScheme(),
		mgr.GetEventRecorder("secretsmount-controller"),
		conns,
		SecretsMountAdapter{Reader: mgr.GetClient()},
		func() *sysv1alpha1.SecretsMount { return &sysv1alpha1.SecretsMount{} },
		opts,
	)
	return r.SetupWithManager(mgr)
}

// SecretsMountAdapter maps a SecretsMount onto sys/mounts/<path>.
type SecretsMountAdapter struct {
	// Reader reads the Plugin that spec.pluginRef names; a zero adapter serves mounts without it.
	Reader client.Reader
}

var (
	_ engine.Adapter[*sysv1alpha1.SecretsMount]          = SecretsMountAdapter{}
	_ engine.Observer[*sysv1alpha1.SecretsMount]         = SecretsMountAdapter{}
	_ engine.Writer[*sysv1alpha1.SecretsMount]           = SecretsMountAdapter{}
	_ engine.Deleter[*sysv1alpha1.SecretsMount]          = SecretsMountAdapter{}
	_ engine.StatusWriter[*sysv1alpha1.SecretsMount]     = SecretsMountAdapter{}
	_ engine.Destructive                                 = SecretsMountAdapter{}
	_ engine.ReferenceWatcher[*sysv1alpha1.SecretsMount] = SecretsMountAdapter{}
)

// kvVersions maps the KV type aliases to the version option OpenBao stores them as.
var kvVersions = map[string]string{"kv-v1": "1", "kv-v2": "2"}

// Path implements engine.Adapter.
func (SecretsMountAdapter) Path(m *sysv1alpha1.SecretsMount) string {
	return "sys/mounts/" + secretsMountPath(m)
}

// Desired implements engine.Adapter. The aliases kv-v1 and kv-v2 are written as type kv with a version option,
// which is how OpenBao stores and returns them.
func (a SecretsMountAdapter) Desired(ctx context.Context, _ engine.Resolver, m *sysv1alpha1.SecretsMount) (engine.Payload, error) {
	p := mountPayload(m.Spec.Type, m.Spec.MountFields)
	if ref := m.Spec.PluginRef; ref != nil {
		version, err := mountPlugin(ctx, a.Reader, m.Namespace, m.Spec.Type, sysv1alpha1.PluginTypeSecret, ref)
		if err != nil {
			return nil, err
		}
		if m.Spec.PluginVersion == "" && version != "" {
			p["plugin_version"] = version
		}
	}
	if version, ok := kvVersions[m.Spec.Type]; ok {
		options, _ := p["options"].(map[string]string)
		if options == nil {
			options = map[string]string{}
		}
		options[optionVersion] = version
		p[keyType] = "kv"
		p["options"] = options
	}
	return p, nil
}

// Fields implements engine.Adapter.
func (SecretsMountAdapter) Fields() engine.FieldSpec { return mountFieldSpec }

// Observe implements engine.Observer.
func (SecretsMountAdapter) Observe(ctx context.Context, c openbao.Client, m *sysv1alpha1.SecretsMount) (engine.Payload, bool, error) {
	observed, found, err := observeMount(ctx, c, secretsTable, secretsMountPath(m))
	if found {
		_, managed := m.Spec.Options[optionVersion]
		dropUnmanagedVersion(observed, managed || kvVersions[m.Spec.Type] != "")
	}
	return observed, found, err
}

// Write implements engine.Writer.
func (SecretsMountAdapter) Write(ctx context.Context, c openbao.Client, m *sysv1alpha1.SecretsMount, desired engine.Payload, create bool) (engine.Payload, error) {
	return nil, writeMount(ctx, c, secretsTable, secretsMountPath(m), desired, create)
}

// Delete implements engine.Deleter.
func (a SecretsMountAdapter) Delete(ctx context.Context, c openbao.Client, m *sysv1alpha1.SecretsMount) error {
	return deleteMount(ctx, c, a.Path(m))
}

// WriteStatus implements engine.StatusWriter: status.id is the mount accessor.
func (SecretsMountAdapter) WriteStatus(_ context.Context, _ openbao.Client, m *sysv1alpha1.SecretsMount, observed engine.Payload) error {
	writeMountAccessor(&m.Status.ResourceStatus, observed)
	return nil
}

func secretsMountPath(m *sysv1alpha1.SecretsMount) string { return nameOrDefault(m.Spec.Path, m.Name) }

// Destructive implements engine.Destructive: deleting it destroys the data it holds.
func (SecretsMountAdapter) Destructive() bool { return true }

// ReferencedKinds implements engine.ReferenceWatcher.
func (SecretsMountAdapter) ReferencedKinds() []client.Object {
	return []client.Object{&sysv1alpha1.Plugin{}}
}

// References implements engine.ReferenceWatcher: the Plugin that pluginRef names.
func (SecretsMountAdapter) References(m *sysv1alpha1.SecretsMount) []engine.ObjectReference {
	return pluginReferences(m.Spec.PluginRef)
}
