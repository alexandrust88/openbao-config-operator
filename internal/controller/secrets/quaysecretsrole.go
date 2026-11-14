package secrets

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	ctrl "sigs.k8s.io/controller-runtime"

	secretsv1alpha1 "github.com/alexandrust88/openbao-config-operator/api/secrets/v1alpha1"
	"github.com/alexandrust88/openbao-config-operator/internal/engine"
	"github.com/alexandrust88/openbao-config-operator/internal/openbao"
)

// +kubebuilder:rbac:groups=secrets.openbaoconfig.io,resources=quaysecretsroles,verbs=get;list;watch;update;patch
// +kubebuilder:rbac:groups=secrets.openbaoconfig.io,resources=quaysecretsroles/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=secrets.openbaoconfig.io,resources=quaysecretsroles/finalizers,verbs=update

// SetupQuaySecretsRole registers the QuaySecretsRole controller with the manager.
func SetupQuaySecretsRole(mgr ctrl.Manager, conns engine.Connections, opts engine.Options) error {
	return setupPlugin(mgr, conns, opts, "quaysecretsrole", QuaySecretsRoleAdapter{},
		func() *secretsv1alpha1.QuaySecretsRole { return &secretsv1alpha1.QuaySecretsRole{} })
}

// quayJSONKeys are written as JSON strings, which the plugin parses, and read back as objects.
var quayJSONKeys = []string{"teams", "repositories"}

// QuaySecretsRoleAdapter maps a QuaySecretsRole onto <mountPath>/roles/<name>.
type QuaySecretsRoleAdapter struct{}

var (
	_ engine.Adapter[*secretsv1alpha1.QuaySecretsRole]  = QuaySecretsRoleAdapter{}
	_ engine.Observer[*secretsv1alpha1.QuaySecretsRole] = QuaySecretsRoleAdapter{}
)

// Path implements engine.Adapter.
func (QuaySecretsRoleAdapter) Path(r *secretsv1alpha1.QuaySecretsRole) string {
	return pluginMountPath(r.Spec.MountPath, "quay") + "/roles/" + specName(r.Spec.Name, r.Name)
}

// Desired implements engine.Adapter.
func (QuaySecretsRoleAdapter) Desired(_ context.Context, _ engine.Resolver, r *secretsv1alpha1.QuaySecretsRole) (engine.Payload, error) {
	p, err := quayRobotPayload(r.Spec.QuayRobotAccount)
	if err != nil {
		return nil, err
	}
	optSeconds(p, "ttl", r.Spec.TTL)
	optSeconds(p, "max_ttl", r.Spec.MaxTTL)
	return p, nil
}

// Fields implements engine.Adapter.
func (QuaySecretsRoleAdapter) Fields() engine.FieldSpec {
	return engine.FieldSpec{Durations: []string{"ttl", "max_ttl"}}
}

// Observe implements engine.Observer.
func (a QuaySecretsRoleAdapter) Observe(ctx context.Context, c openbao.Client, r *secretsv1alpha1.QuaySecretsRole) (engine.Payload, bool, error) {
	return quayObserve(ctx, c, a.Path(r))
}

func quayRobotPayload(s secretsv1alpha1.QuayRobotAccount) (engine.Payload, error) {
	p := engine.Payload{"namespace_name": s.NamespaceName}
	optString(p, "namespace_type", string(s.NamespaceType))
	optBool(p, "create_repositories", s.CreateRepositories)
	optString(p, "default_permission", string(s.DefaultPermission))
	for key, value := range map[string]any{"teams": s.Teams, "repositories": s.Repositories} {
		if encoded, err := quayJSON(value); err != nil {
			return nil, fmt.Errorf("%s: %w", key, err)
		} else if encoded != "" {
			p[key] = encoded
		}
	}
	return p, nil
}

// quayJSON returns the compact JSON of a map with sorted keys, or "" for an empty map.
func quayJSON(value any) (string, error) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	if s := string(encoded); s != "null" && s != "{}" {
		return s, nil
	}
	return "", nil
}

// quayObserve reads a Quay role and re-encodes teams and repositories in the canonical form Desired writes.
func quayObserve(ctx context.Context, c openbao.Client, path string) (engine.Payload, bool, error) {
	observed, err := c.Read(ctx, path)
	if errors.Is(err, openbao.ErrNotFound) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	if observed == nil {
		observed = engine.Payload{}
	}
	for _, key := range quayJSONKeys {
		value, ok := observed[key]
		if !ok || value == nil {
			continue
		}
		if s, isString := value.(string); isString {
			var decoded map[string]any
			if json.Unmarshal([]byte(s), &decoded) != nil {
				continue
			}
			value = decoded
		}
		if encoded, err := quayJSON(value); err == nil {
			observed[key] = encoded
			if encoded == "" {
				delete(observed, key)
			}
		}
	}
	return observed, true, nil
}
