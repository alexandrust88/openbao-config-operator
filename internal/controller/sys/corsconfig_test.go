package sys

import (
	"context"
	"testing"

	sysv1alpha1 "github.com/alexandrust88/openbao-config-operator/api/sys/v1alpha1"
)

func newCORSConfig(headers ...string) *sysv1alpha1.CORSConfig {
	return &sysv1alpha1.CORSConfig{Spec: sysv1alpha1.CORSConfigSpec{
		AllowedOrigins:   []string{"https://ui.example.com", "https://admin.example.com"},
		AllowedHeaders:   headers,
		AllowCredentials: true,
	}}
}

// corsRead is the read of an enabled CORS config, standard headers first (vault/cors.go Enable).
func corsRead(extra ...any) map[string]any {
	headers := []any{}
	for _, h := range corsStandardHeaders {
		headers = append(headers, h)
	}
	return map[string]any{
		litEnabled:          true,
		"allowed_origins":   []any{"https://admin.example.com", "https://ui.example.com"},
		"allowed_headers":   append(headers, extra...),
		"allow_credentials": true,
	}
}

func TestCORSConfigDesired(t *testing.T) {
	a := CORSConfigAdapter{}
	desired, _ := a.Desired(context.Background(), nil, newCORSConfig())
	if headers, ok := desired["allowed_headers"].([]string); !ok || headers == nil || len(headers) != 0 {
		t.Errorf("allowed_headers = %#v, want an empty list so removed headers are cleared", desired["allowed_headers"])
	}
	if desired["allow_credentials"] != true || len(desired["allowed_origins"].([]string)) != 2 {
		t.Errorf("desired = %v", desired)
	}
	if a.Path(newCORSConfig()) != "sys/config/cors" {
		t.Error("path")
	}
}

func TestCORSConfigObserve(t *testing.T) {
	a := CORSConfigAdapter{}
	ctx := context.Background()
	tests := []struct {
		name    string
		spec    *sysv1alpha1.CORSConfig
		read    map[string]any
		differs string
	}{
		{name: "standard headers only", spec: newCORSConfig(), read: corsRead()},
		{name: "extra header", spec: newCORSConfig("X-Custom"), read: corsRead("X-Custom")},
		{name: "spec repeats a standard header", spec: newCORSConfig("Content-Type"), read: corsRead("Content-Type")},
		{name: "header added out of band", spec: newCORSConfig(), read: corsRead("X-Intruder"), differs: "allowed_headers"},
		{name: "origin changed", spec: newCORSConfig(), read: func() map[string]any {
			r := corsRead()
			r["allowed_origins"] = []any{"*"}
			return r
		}(), differs: "allowed_origins"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := newConfigClient(map[string]map[string]any{corsConfigPath: tt.read})
			observed, found, err := a.Observe(ctx, c, tt.spec)
			if err != nil || !found {
				t.Fatalf("found=%v err=%v", found, err)
			}
			desired, _ := a.Desired(ctx, nil, tt.spec)
			if tt.differs == "" {
				assertInSync(t, desired, observed, a.Fields())
			} else {
				assertDiffers(t, desired, observed, a.Fields(), tt.differs)
			}
		})
	}
}

func TestCORSConfigObserveDisabledIsAbsent(t *testing.T) {
	c := newConfigClient(map[string]map[string]any{corsConfigPath: {litEnabled: false}})
	_, found, err := CORSConfigAdapter{}.Observe(context.Background(), c, newCORSConfig())
	if err != nil || found {
		t.Errorf("found=%v err=%v, want absent", found, err)
	}
}

func TestCORSConfigObserveChildNamespace(t *testing.T) {
	c := newConfigClient(nil).WithNamespace(testNamespace)
	_, _, err := CORSConfigAdapter{}.Observe(context.Background(), c, newCORSConfig())
	assertUnsupported(t, err)
}
