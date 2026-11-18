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

// +kubebuilder:rbac:groups=secrets.obco.dev,resources=rabbitmqroles,verbs=get;list;watch;update;patch
// +kubebuilder:rbac:groups=secrets.obco.dev,resources=rabbitmqroles/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=secrets.obco.dev,resources=rabbitmqroles/finalizers,verbs=update

// SetupRabbitMQRole registers the RabbitMQRole controller with the manager.
func SetupRabbitMQRole(mgr ctrl.Manager, conns engine.Connections, opts engine.Options) error {
	r := engine.New[*secretsv1alpha1.RabbitMQRole](
		mgr.GetClient(),
		mgr.GetScheme(),
		mgr.GetEventRecorder("rabbitmqrole-controller"),
		conns,
		RabbitMQRoleAdapter{},
		func() *secretsv1alpha1.RabbitMQRole { return &secretsv1alpha1.RabbitMQRole{} },
		opts,
	)
	return r.SetupWithManager(mgr)
}

// RabbitMQRoleAdapter maps a RabbitMQRole onto <mount>/roles/<name>.
type RabbitMQRoleAdapter struct{}

var (
	_ engine.Adapter[*secretsv1alpha1.RabbitMQRole]  = RabbitMQRoleAdapter{}
	_ engine.Observer[*secretsv1alpha1.RabbitMQRole] = RabbitMQRoleAdapter{}
)

// Path implements engine.Adapter.
func (RabbitMQRoleAdapter) Path(r *secretsv1alpha1.RabbitMQRole) string {
	return r.Spec.MountPath + "/roles/" + specName(r.Spec.Name, r.Name)
}

// Desired implements engine.Adapter. OpenBao takes tags as a comma-separated string and the permission maps
// as JSON strings; the maps are encoded with sorted keys so Observe can compare them as text.
func (RabbitMQRoleAdapter) Desired(_ context.Context, _ engine.Resolver, r *secretsv1alpha1.RabbitMQRole) (engine.Payload, error) {
	p := engine.Payload{"vhosts": "{}", "vhost_topics": "{}"}
	orCSV(p, "tags", r.Spec.Tags)
	if len(r.Spec.VHosts) > 0 {
		vhosts := map[string]map[string]string{}
		for vhost, perm := range r.Spec.VHosts {
			vhosts[vhost] = map[string]string{"configure": perm.Configure, "write": perm.Write, "read": perm.Read}
		}
		encoded, err := json.Marshal(vhosts)
		if err != nil {
			return nil, err
		}
		p["vhosts"] = string(encoded)
	}
	if len(r.Spec.VHostTopics) > 0 {
		topics := map[string]map[string]map[string]string{}
		for vhost, exchanges := range r.Spec.VHostTopics {
			topics[vhost] = map[string]map[string]string{}
			for exchange, perm := range exchanges {
				topics[vhost][exchange] = map[string]string{"write": perm.Write, "read": perm.Read}
			}
		}
		encoded, err := json.Marshal(topics)
		if err != nil {
			return nil, err
		}
		p["vhost_topics"] = string(encoded)
	}
	return p, nil
}

// Fields implements engine.Adapter.
func (RabbitMQRoleAdapter) Fields() engine.FieldSpec { return engine.FieldSpec{} }

// Observe implements engine.Observer. The read returns the permission maps as objects; they are encoded
// back to the JSON text Desired produces.
func (a RabbitMQRoleAdapter) Observe(ctx context.Context, c openbao.Client, r *secretsv1alpha1.RabbitMQRole) (engine.Payload, bool, error) {
	path := a.Path(r)
	observed, err := c.Read(ctx, path)
	if errors.Is(err, openbao.ErrNotFound) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("read %s: %w", path, err)
	}
	for _, key := range []string{"vhosts", "vhost_topics"} {
		if value, ok := observed[key].(map[string]any); ok {
			encoded, err := json.Marshal(value)
			if err != nil {
				return nil, false, fmt.Errorf("encode %s of %s: %w", key, path, err)
			}
			observed[key] = string(encoded)
		}
	}
	return observed, true, nil
}
