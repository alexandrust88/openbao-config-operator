package secrets

import (
	"context"
	"errors"
	"maps"
	"slices"
	"testing"

	secretsv1alpha1 "github.com/alexandrust88/openbao-config-operator/api/secrets/v1alpha1"
	"github.com/alexandrust88/openbao-config-operator/internal/engine"
)

// TestNomadSecretsConfigWritesOnlyDifferingEndpoints starts from the golden access and lease reads of the sample
// and checks which endpoints a write sends for each kind of drift. Nomad is an external plugin, so a recording fake
// client stands in for OpenBao.
func TestNomadSecretsConfigWritesOnlyDifferingEndpoints(t *testing.T) {
	ctx := context.Background()
	key := []byte("test-key")
	a := NomadSecretsConfigAdapter{hashKey: key}
	cfg := &secretsv1alpha1.NomadSecretsConfig{}
	secrets := loadPluginExample(t, "nomadsecretsconfig", cfg)
	written, err := a.Desired(ctx, secrets, cfg)
	if err != nil {
		t.Fatal(err)
	}
	rotatedSecrets := pluginFakeResolver{}
	for k, v := range secrets {
		rotatedSecrets[k] = v + "-rotated"
	}
	rotated, err := a.Desired(ctx, rotatedSecrets, cfg)
	if err != nil {
		t.Fatal(err)
	}
	cfg.Status.WriteOnlyHash = engine.WriteOnlyHash(engine.ObjectHashKey(key, cfg.UID, secretsv1alpha1.GroupVersion.WithKind("NomadSecretsConfig").GroupKind()), written, a.Fields())

	access, lease := a.Path(cfg), nomadConfigPath(cfg)+"/lease"
	settled := func() *credClient {
		return &credClient{reads: map[string]map[string]any{
			access: loadPluginFixture(t, "nomadsecretsconfig.read.json"),
			lease:  loadPluginFixture(t, "nomadsecretsconfig.lease.read.json"),
		}}
	}
	observed, _, err := a.Observe(ctx, settled(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	if diff := engine.Diff(written, observed, a.Fields()); len(diff) != 0 {
		t.Fatalf("settled read differs on %v", diff)
	}
	drift := func(path, field string, value any) *credClient {
		c := settled()
		c.reads[path] = maps.Clone(c.reads[path])
		c.reads[path][field] = value
		return c
	}
	driftedBoth := drift(lease, "ttl", 60.0)
	driftedBoth.reads[access]["address"] = "https://other:4646"

	for name, tt := range map[string]struct {
		bao     *credClient
		desired engine.Payload
		want    []string
	}{
		"in sync":                {bao: settled(), desired: written},
		"write-only inputs only": {bao: settled(), desired: rotated, want: []string{access}},
		"access drift only":      {bao: drift(access, "address", "https://other:4646"), desired: written, want: []string{access}},
		"lease only":             {bao: drift(lease, "ttl", 60.0), desired: written, want: []string{lease}},
		"both":                   {bao: driftedBoth, desired: written, want: []string{lease, access}},
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
}
