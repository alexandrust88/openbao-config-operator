package sys

import (
	"context"

	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	sysv1alpha1 "github.com/alexandrust88/openbao-config-operator/api/sys/v1alpha1"
	"github.com/alexandrust88/openbao-config-operator/internal/engine"
	"github.com/alexandrust88/openbao-config-operator/internal/openbao"
)

// +kubebuilder:rbac:groups=sys.openbaoconfig.io,resources=authmounts,verbs=get;list;watch;update;patch
// +kubebuilder:rbac:groups=sys.openbaoconfig.io,resources=authmounts/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=sys.openbaoconfig.io,resources=authmounts/finalizers,verbs=update
// +kubebuilder:rbac:groups=sys.openbaoconfig.io,resources=plugins,verbs=get;list;watch

// SetupAuthMount registers the AuthMount controller with the manager.
func SetupAuthMount(mgr ctrl.Manager, conns engine.Connections, opts engine.Options) error {
	r := engine.New[*sysv1alpha1.AuthMount](
		mgr.GetClient(),
		mgr.GetScheme(),
		mgr.GetEventRecorder("authmount-controller"),
		conns,
		AuthMountAdapter{Reader: mgr.GetClient()},
		func() *sysv1alpha1.AuthMount { return &sysv1alpha1.AuthMount{} },
		opts,
	)
	return r.SetupWithManager(mgr)
}

// AuthMountAdapter maps an AuthMount onto sys/auth/<path>.
type AuthMountAdapter struct {
	// Reader reads the Plugin that spec.pluginRef names; a zero adapter serves mounts without it.
	Reader client.Reader
}

var (
	_ engine.Adapter[*sysv1alpha1.AuthMount]          = AuthMountAdapter{}
	_ engine.Observer[*sysv1alpha1.AuthMount]         = AuthMountAdapter{}
	_ engine.Writer[*sysv1alpha1.AuthMount]           = AuthMountAdapter{}
	_ engine.StatusWriter[*sysv1alpha1.AuthMount]     = AuthMountAdapter{}
	_ engine.Destructive                              = AuthMountAdapter{}
	_ engine.DriftTier                                = AuthMountAdapter{}
	_ engine.ReferenceWatcher[*sysv1alpha1.AuthMount] = AuthMountAdapter{}
)

// Path implements engine.Adapter.
func (AuthMountAdapter) Path(m *sysv1alpha1.AuthMount) string {
	return "sys/auth/" + authMountPath(m)
}

// Desired implements engine.Adapter. The user lockout settings use their flat read names; Write nests them. They
// are managed only when set: unset, the mount follows the server's user_lockout stanza, which a tune cannot
// restore once a mount has its own settings.
func (a AuthMountAdapter) Desired(ctx context.Context, _ engine.Resolver, m *sysv1alpha1.AuthMount) (engine.Payload, error) {
	p := mountPayload(m.Spec.Type, m.Spec.MountFields)
	if ref := m.Spec.PluginRef; ref != nil {
		version, err := mountPlugin(ctx, a.Reader, m.Namespace, m.Spec.Type, sysv1alpha1.PluginTypeAuth, ref)
		if err != nil {
			return nil, err
		}
		if m.Spec.PluginVersion == "" && version != "" {
			p["plugin_version"] = version
		}
	}
	p["token_type"] = string(sysv1alpha1.MountTokenTypeDefaultService)
	if m.Spec.TokenType != "" {
		p["token_type"] = string(m.Spec.TokenType)
	}
	if l := m.Spec.UserLockout; l != nil {
		if l.Threshold != nil {
			p["user_lockout_threshold"] = int64(*l.Threshold)
		}
		putSeconds(p, "user_lockout_duration", l.Duration)
		putSeconds(p, "user_lockout_counter_reset_duration", l.CounterResetDuration)
		putBool(p, "user_lockout_disable", l.Disable)
	}
	return p, nil
}

// Fields implements engine.Adapter.
func (AuthMountAdapter) Fields() engine.FieldSpec { return mountFieldSpec }

// Observe implements engine.Observer.
func (AuthMountAdapter) Observe(ctx context.Context, c openbao.Client, m *sysv1alpha1.AuthMount) (engine.Payload, bool, error) {
	observed, found, err := observeMount(ctx, c, authTable, authMountPath(m))
	if found {
		_, managed := m.Spec.Options[optionVersion]
		dropUnmanagedVersion(observed, managed)
	}
	return observed, found, err
}

// Write implements engine.Writer.
func (AuthMountAdapter) Write(ctx context.Context, c openbao.Client, m *sysv1alpha1.AuthMount, desired engine.Payload, create bool) (engine.Payload, error) {
	return nil, writeMount(ctx, c, authTable, authMountPath(m), desired, create)
}

// WriteStatus implements engine.StatusWriter: status.id is the mount accessor.
func (AuthMountAdapter) WriteStatus(_ context.Context, _ openbao.Client, m *sysv1alpha1.AuthMount, observed engine.Payload) error {
	writeMountAccessor(&m.Status.ResourceStatus, observed)
	return nil
}

func authMountPath(m *sysv1alpha1.AuthMount) string { return nameOrDefault(m.Spec.Path, m.Name) }

// Destructive implements engine.Destructive: deleting it destroys the data it holds.
func (AuthMountAdapter) Destructive() bool { return true }

// DriftTier implements engine.DriftTier: it grants access, so drift is checked on the short interval.
func (AuthMountAdapter) DriftTier() engine.Tier { return engine.TierAccessCritical }

// ReferencedKinds implements engine.ReferenceWatcher.
func (AuthMountAdapter) ReferencedKinds() []client.Object {
	return []client.Object{&sysv1alpha1.Plugin{}}
}

// References implements engine.ReferenceWatcher: the Plugin that pluginRef names.
func (AuthMountAdapter) References(m *sysv1alpha1.AuthMount) []engine.ObjectReference {
	return pluginReferences(m.Spec.PluginRef)
}
