package secrets

import (
	"context"
	"errors"
	"reflect"
	"slices"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	commonv1alpha1 "github.com/alexandrust88/openbao-config-operator/api/common/v1alpha1"
	secretsv1alpha1 "github.com/alexandrust88/openbao-config-operator/api/secrets/v1alpha1"
	"github.com/alexandrust88/openbao-config-operator/internal/engine"
)

func newRabbitMQConfig() *secretsv1alpha1.RabbitMQConfig {
	return &secretsv1alpha1.RabbitMQConfig{
		ObjectMeta: metav1.ObjectMeta{Name: "rabbit", Namespace: "team"},
		Spec: secretsv1alpha1.RabbitMQConfigSpec{
			MountPath:         "rabbitmq",
			ConnectionURI:     "http://rabbitmq:15672",
			Username:          "admin",
			PasswordSecretRef: commonv1alpha1.SecretKeySelector{Name: "rabbit-admin", Key: "password"},
			VerifyConnection:  new(false),
			PasswordPolicy:    "rabbit-policy",
			UsernameTemplate:  "{{.RoleName}}-{{random 8}}",
			Lease:             &secretsv1alpha1.RabbitMQLease{TTL: &metav1.Duration{Duration: time.Hour}},
		},
	}
}

func rabbitResolver(password string) credResolver {
	return credResolver{"rabbit-admin": {"password": password}}
}

func TestRabbitMQConfigDesired(t *testing.T) {
	a := RabbitMQConfigAdapter{}
	cfg := newRabbitMQConfig()
	if got := a.Path(cfg); got != "rabbitmq/config/connection" {
		t.Errorf("path = %q", got)
	}
	desired, err := a.Desired(context.Background(), rabbitResolver("fake-admin-password"), cfg)
	if err != nil {
		t.Fatal(err)
	}
	want := engine.Payload{
		"connection_uri":    "http://rabbitmq:15672",
		"username":          "admin",
		"password":          "fake-admin-password",
		"verify_connection": false,
		"password_policy":   "rabbit-policy",
		"username_template": "{{.RoleName}}-{{random 8}}",
		"ttl":               int64(3600),
		"max_ttl":           int64(0),
	}
	if !reflect.DeepEqual(desired, want) {
		t.Errorf("desired =\n%#v\nwant\n%#v", desired, want)
	}
	if _, err := a.Desired(context.Background(), credResolver{}, cfg); !errors.Is(err, engine.ErrReferenceNotReady) {
		t.Errorf("missing secret: err = %v", err)
	}
}

func TestRabbitMQConfigObserve(t *testing.T) {
	a := RabbitMQConfigAdapter{}
	ctx := context.Background()
	desired, _ := a.Desired(ctx, rabbitResolver("first"), newRabbitMQConfig())

	cfg := newRabbitMQConfig()
	if _, found, err := a.Observe(ctx, &credClient{}, cfg); found || err != nil {
		t.Errorf("never written: found=%v err=%v, want absent", found, err)
	}
	cfg.Status.Acquired = commonv1alpha1.AcquiredCreated
	observed, found, err := a.Observe(ctx, &credClient{}, cfg)
	if !found || err != nil {
		t.Fatalf("acquired without lease: found=%v err=%v", found, err)
	}
	if got := engine.Diff(desired, observed, a.Fields()); !slices.Equal(got, []string{"max_ttl", "ttl"}) {
		t.Errorf("missing lease: differing = %v", got)
	}

	c := &credClient{reads: map[string]map[string]any{"rabbitmq/config/lease": {"ttl": 3600.0, "max_ttl": 0.0}}}
	observed, _, _ = a.Observe(ctx, c, cfg)
	if got := engine.Diff(desired, observed, a.Fields()); len(got) != 0 {
		t.Errorf("in sync: differing = %v", got)
	}

	key := []byte("test-key")
	rotated, _ := a.Desired(ctx, rabbitResolver("second"), newRabbitMQConfig())
	if engine.WriteOnlyHash(key, desired, a.Fields()) == engine.WriteOnlyHash(key, rotated, a.Fields()) {
		t.Error("a rotated password must change the write-only hash")
	}
}

func TestRabbitMQConfigWrite(t *testing.T) {
	a := RabbitMQConfigAdapter{}
	for name, tt := range map[string]struct {
		desired engine.Payload
		writes  []string
	}{
		"with lease": {desired: engine.Payload{"connection_uri": "u", "ttl": int64(1), "max_ttl": int64(0)}, writes: []string{"rabbitmq/config/lease", "rabbitmq/config/connection"}},
	} {
		t.Run(name, func(t *testing.T) {
			c := &credClient{}
			if _, err := a.Write(context.Background(), c, newRabbitMQConfig(), tt.desired, true); err != nil {
				t.Fatal(err)
			}
			if !slices.Equal(c.writes, tt.writes) {
				t.Errorf("writes = %v, want %v", c.writes, tt.writes)
			}
		})
	}
}

func newRabbitMQRole() *secretsv1alpha1.RabbitMQRole {
	return &secretsv1alpha1.RabbitMQRole{
		ObjectMeta: metav1.ObjectMeta{Name: "app", Namespace: "team"},
		Spec: secretsv1alpha1.RabbitMQRoleSpec{
			MountPath: "rabbitmq",
			Tags:      []string{"management", "monitoring"},
			VHosts:    map[string]secretsv1alpha1.RabbitMQVHostPermission{"/": {Configure: ".*", Write: ".*", Read: ".*"}},
			VHostTopics: map[string]map[string]secretsv1alpha1.RabbitMQTopicPermission{
				"/": {"amq.topic": {Write: ".*"}},
			},
		},
	}
}

func TestRabbitMQRoleDesired(t *testing.T) {
	a := RabbitMQRoleAdapter{}
	r := newRabbitMQRole()
	if got := a.Path(r); got != "rabbitmq/roles/app" {
		t.Errorf("path = %q", got)
	}
	desired, err := a.Desired(context.Background(), nil, r)
	if err != nil {
		t.Fatal(err)
	}
	want := engine.Payload{
		"tags":         "management,monitoring",
		"vhosts":       `{"/":{"configure":".*","read":".*","write":".*"}}`,
		"vhost_topics": `{"/":{"amq.topic":{"read":"","write":".*"}}}`,
	}
	if !reflect.DeepEqual(desired, want) {
		t.Errorf("desired =\n%#v\nwant\n%#v", desired, want)
	}
}

func TestRabbitMQRoleGoldenRead(t *testing.T) {
	a := RabbitMQRoleAdapter{}
	r := newRabbitMQRole()
	desired, _ := a.Desired(context.Background(), nil, r)
	tests := []struct {
		name      string
		mutate    func(map[string]any)
		differing []string
	}{
		{name: "in sync", mutate: func(map[string]any) {}},
		{name: "vhost permission changed", mutate: func(read map[string]any) {
			read["vhosts"].(map[string]any)["/"].(map[string]any)["write"] = ""
		}, differing: []string{"vhosts"}},
		{name: "tags changed", mutate: func(read map[string]any) { read["tags"] = "management" }, differing: []string{"tags"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// GET rabbitmq/roles/r on OpenBao 2.7.2.
			read := map[string]any{
				"tags":         "management,monitoring",
				"vhost_topics": map[string]any{"/": map[string]any{"amq.topic": map[string]any{"read": "", "write": ".*"}}},
				"vhosts":       map[string]any{"/": map[string]any{"configure": ".*", "read": ".*", "write": ".*"}},
			}
			tt.mutate(read)
			c := &credClient{reads: map[string]map[string]any{"rabbitmq/roles/app": read}}
			observed, found, err := a.Observe(context.Background(), c, r)
			if err != nil || !found {
				t.Fatalf("found=%v err=%v", found, err)
			}
			if got := engine.Diff(desired, observed, a.Fields()); !slices.Equal(got, tt.differing) {
				t.Errorf("differing = %v, want %v", got, tt.differing)
			}
		})
	}
}
