package secrets

import (
	"context"
	"encoding/json"
	"errors"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"testing"

	commonv1alpha1 "github.com/alexandrust88/openbao-config-operator/api/common/v1alpha1"
	secretsv1alpha1 "github.com/alexandrust88/openbao-config-operator/api/secrets/v1alpha1"
	"github.com/alexandrust88/openbao-config-operator/internal/engine"
)

// TestRabbitMQConfigWritesOnlyDifferingEndpoints starts from the lease read of OpenBao 2.7.2 after the sample was
// written and checks which endpoints a write sends for each kind of drift.
func TestRabbitMQConfigWritesOnlyDifferingEndpoints(t *testing.T) {
	ctx := context.Background()
	raw, err := os.ReadFile(filepath.Join("testdata", "rabbitmq", "rabbitmqconfig.lease.read.json"))
	if err != nil {
		t.Fatal(err)
	}
	var leaseRead map[string]any
	if err := json.Unmarshal(raw, &leaseRead); err != nil {
		t.Fatal(err)
	}
	key := []byte("test-key")
	a := RabbitMQConfigAdapter{hashKey: key}
	written, _ := a.Desired(ctx, rabbitResolver("first"), newRabbitMQConfig())
	rotated, _ := a.Desired(ctx, rabbitResolver("second"), newRabbitMQConfig())
	settled := func() *credClient {
		return &credClient{reads: map[string]map[string]any{"rabbitmq/config/lease": maps.Clone(leaseRead)}}
	}
	driftedLease := func() *credClient {
		c := settled()
		c.reads["rabbitmq/config/lease"]["ttl"] = 60.0
		return c
	}

	cfg := newRabbitMQConfig()
	cfg.Status.Acquired = commonv1alpha1.AcquiredCreated
	cfg.Status.WriteOnlyHash = engine.WriteOnlyHash(engine.ObjectHashKey(key, cfg.UID, secretsv1alpha1.GroupVersion.WithKind("RabbitMQConfig").GroupKind()), written, a.Fields())
	observed, _, err := a.Observe(ctx, settled(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	if diff := engine.Diff(written, observed, a.Fields()); len(diff) != 0 {
		t.Fatalf("settled read differs on %v", diff)
	}

	const lease, connection = "rabbitmq/config/lease", "rabbitmq/config/connection"
	for name, tt := range map[string]struct {
		bao     *credClient
		desired engine.Payload
		want    []string
	}{
		"in sync":         {bao: settled(), desired: written},
		"connection only": {bao: settled(), desired: rotated, want: []string{connection}},
		"lease only":      {bao: driftedLease(), desired: written, want: []string{lease}},
		"both":            {bao: driftedLease(), desired: rotated, want: []string{lease, connection}},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := a.Write(ctx, tt.bao, cfg, tt.desired, false)
			if tt.want == nil && !errors.Is(err, engine.ErrNothingWritten) || tt.want != nil && err != nil {
				t.Fatalf("err = %v", err)
			}
			if !slices.Equal(tt.bao.writes, tt.want) {
				t.Errorf("writes = %v, want %v", tt.bao.writes, tt.want)
			}
		})
	}

	t.Run("failed connection write", func(t *testing.T) {
		bao := &credClient{failPaths: map[string]bool{connection: true}}
		if _, err := a.Write(ctx, bao, cfg, rotated, false); err == nil {
			t.Fatal("want the connection error")
		}
		bao.reads = settled().reads
		bao.writes = nil
		_, _ = a.Write(ctx, bao, cfg, rotated, false)
		if !slices.Equal(bao.writes, []string{connection}) {
			t.Errorf("retry writes = %v, want only the connection", bao.writes)
		}
	})
}
