package sys

import (
	"context"
	"slices"

	ctrl "sigs.k8s.io/controller-runtime"

	sysv1alpha1 "github.com/alexandrust88/openbao-config-operator/api/sys/v1alpha1"
	"github.com/alexandrust88/openbao-config-operator/internal/engine"
	"github.com/alexandrust88/openbao-config-operator/internal/openbao"
)

// +kubebuilder:rbac:groups=sys.openbaoconfig.io,resources=corsconfigs,verbs=get;list;watch;update;patch
// +kubebuilder:rbac:groups=sys.openbaoconfig.io,resources=corsconfigs/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=sys.openbaoconfig.io,resources=corsconfigs/finalizers,verbs=update

const corsConfigPath = "sys/config/cors"

// corsStandardHeaders are added by OpenBao to every allowed_headers list (vault/cors.go StdAllowedHeaders).
var corsStandardHeaders = []string{
	"Content-Type",
	"Authorization",
	"X-Vault-Token",
	"X-Vault-MFA",
	"X-Vault-No-Request-Forwarding",
	"X-Vault-Wrap-Format",
	"X-Vault-Wrap-TTL",
	"X-Requested-With",
	"X-Vault-AWS-IAM-Server-ID",
	"X-Vault-Policy-Override",
}

// SetupCORSConfig registers the CORSConfig controller with the manager.
func SetupCORSConfig(mgr ctrl.Manager, conns engine.Connections, opts engine.Options) error {
	r := engine.New[*sysv1alpha1.CORSConfig](
		mgr.GetClient(),
		mgr.GetScheme(),
		mgr.GetEventRecorder("corsconfig-controller"),
		conns,
		CORSConfigAdapter{},
		func() *sysv1alpha1.CORSConfig { return &sysv1alpha1.CORSConfig{} },
		opts,
	)
	return r.SetupWithManager(mgr)
}

// CORSConfigAdapter maps a CORSConfig onto sys/config/cors. Disabled CORS counts as absent; deleting it
// disables CORS.
type CORSConfigAdapter struct{}

var (
	_ engine.Adapter[*sysv1alpha1.CORSConfig]  = CORSConfigAdapter{}
	_ engine.Observer[*sysv1alpha1.CORSConfig] = CORSConfigAdapter{}
	_ engine.Singleton                         = CORSConfigAdapter{}
)

// Path implements engine.Adapter.
func (CORSConfigAdapter) Path(*sysv1alpha1.CORSConfig) string { return corsConfigPath }

// Desired implements engine.Adapter. Every write replaces the whole configuration, so all fields are sent.
func (CORSConfigAdapter) Desired(_ context.Context, _ engine.Resolver, c *sysv1alpha1.CORSConfig) (engine.Payload, error) {
	return engine.Payload{
		"allowed_origins":   c.Spec.AllowedOrigins,
		"allowed_headers":   stringsOrEmpty(c.Spec.AllowedHeaders),
		"allow_credentials": c.Spec.AllowCredentials,
	}, nil
}

// Fields implements engine.Adapter.
func (CORSConfigAdapter) Fields() engine.FieldSpec {
	return engine.FieldSpec{Sets: []string{"allowed_origins", "allowed_headers"}}
}

// Observe implements engine.Observer. The standard headers OpenBao adds are removed from allowed_headers
// unless the spec lists them.
func (CORSConfigAdapter) Observe(ctx context.Context, c openbao.Client, cfg *sysv1alpha1.CORSConfig) (engine.Payload, bool, error) {
	if err := requireRootNamespace(c, "CORSConfig"); err != nil {
		return nil, false, err
	}
	observed, err := c.Read(ctx, corsConfigPath)
	if err != nil {
		return nil, false, err
	}
	if enabled, _ := observed["enabled"].(bool); !enabled {
		return nil, false, nil
	}
	headers := []any{}
	raw, _ := observed["allowed_headers"].([]any)
	for _, h := range raw {
		name, _ := h.(string)
		if slices.Contains(corsStandardHeaders, name) && !slices.Contains(cfg.Spec.AllowedHeaders, name) {
			continue
		}
		headers = append(headers, h)
	}
	observed["allowed_headers"] = headers
	return observed, true, nil
}

// Singleton implements engine.Singleton: the setting always exists, so it is adopted whatever the creationPolicy.
func (CORSConfigAdapter) Singleton() bool { return true }
