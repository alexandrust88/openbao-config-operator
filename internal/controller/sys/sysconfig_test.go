package sys

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"sigs.k8s.io/controller-runtime/pkg/client"

	commonv1alpha1 "github.com/alexandrust88/openbao-config-operator/api/common/v1alpha1"
	"github.com/alexandrust88/openbao-config-operator/internal/engine"
	"github.com/alexandrust88/openbao-config-operator/internal/openbao"
)

// configClient is an in-memory openbao.Client for the sys config adapters.
type configClient struct {
	ns      string
	data    map[string]map[string]any
	errs    map[string]error
	writes  map[string]map[string]any
	deletes []string
}

func newConfigClient(data map[string]map[string]any) *configClient {
	return &configClient{data: data, errs: map[string]error{}, writes: map[string]map[string]any{}}
}

func (c *configClient) Read(_ context.Context, path string) (map[string]any, error) {
	if err := c.errs["read "+path]; err != nil {
		return nil, err
	}
	if d, ok := c.data[path]; ok {
		return d, nil
	}
	return nil, &openbao.ResponseError{StatusCode: 404}
}

func (c *configClient) Write(_ context.Context, path string, data map[string]any) (map[string]any, error) {
	if err := c.errs["write "+path]; err != nil {
		return nil, err
	}
	c.writes[path] = data
	return nil, nil
}

func (c *configClient) Delete(_ context.Context, path string) error {
	c.deletes = append(c.deletes, path)
	return nil
}

func (c *configClient) List(context.Context, string) ([]string, error) {
	return nil, openbao.ErrNotFound
}

func (c *configClient) WithNamespace(ns string) openbao.Client {
	clone := *c
	clone.ns = ns
	return &clone
}

func (c *configClient) Namespace() string { return c.ns }

// secretResolver resolves SecretKeySelectors from a map of "name/key" to value.
type secretResolver map[string]string

func (s secretResolver) SecretValue(_ context.Context, _ string, ref commonv1alpha1.SecretKeySelector) ([]byte, error) {
	if v, ok := s[ref.Name+"/"+ref.Key]; ok {
		return []byte(v), nil
	}
	return nil, fmt.Errorf("secret %s: %w", ref.Name, engine.ErrReferenceNotReady)
}

func (secretResolver) Object(context.Context, client.ObjectKey, client.Object) error { return nil }

func badRequest(msg string) error {
	return &openbao.ResponseError{StatusCode: 400, Errors: []string{msg}}
}

func assertUnsupported(t *testing.T, err error) {
	t.Helper()
	if !errors.Is(err, engine.ErrUnsupported) {
		t.Fatalf("err = %v, want engine.ErrUnsupported", err)
	}
}

func assertInSync(t *testing.T, desired, observed engine.Payload, fields engine.FieldSpec) {
	t.Helper()
	if differing := engine.Diff(desired, observed, fields); len(differing) != 0 {
		t.Errorf("differing keys %v, want none", differing)
	}
}

func assertDiffers(t *testing.T, desired, observed engine.Payload, fields engine.FieldSpec, key string) {
	t.Helper()
	differing := engine.Diff(desired, observed, fields)
	if len(differing) != 1 || differing[0] != key {
		t.Errorf("differing keys %v, want [%s]", differing, key)
	}
}

func TestRequireRootNamespace(t *testing.T) {
	for ns, ok := range map[string]bool{"": true, "root": true, "/": true, testNamespace: false, "team/a/": false} {
		err := requireRootNamespace(newConfigClient(nil).WithNamespace(ns), "Kind")
		if ok != (err == nil) {
			t.Errorf("namespace %q: err = %v", ns, err)
		}
		if err != nil {
			assertUnsupported(t, err)
		}
	}
}

func TestBadRequestContains(t *testing.T) {
	if !badRequestContains(fmt.Errorf("read: %w", badRequest("x: raft storage is not in use")), raftNotInUse) {
		t.Error("wrapped 400 with message not matched")
	}
	if badRequestContains(&openbao.ResponseError{StatusCode: 500, Errors: []string{raftNotInUse}}, raftNotInUse) {
		t.Error("500 matched")
	}
	if badRequestContains(badRequest("other"), raftNotInUse) {
		t.Error("other message matched")
	}
}

// TestSysAdapterTraits pins which kinds the engine adopts whatever the creationPolicy, which it guards against
// deleting data, the Orphan deletion default and the access-critical drift tier.
func TestSysAdapterTraits(t *testing.T) {
	singletons := []engine.Singleton{
		CORSConfigAdapter{}, LoggerAdapter{}, QuotaConfigAdapter{}, KeyRotationConfigAdapter{}, RaftAutopilotAdapter{},
	}
	for _, s := range singletons {
		if !s.Singleton() {
			t.Errorf("%T must be a singleton", s)
		}
	}
	for _, d := range []engine.Destructive{AuthMountAdapter{}, SecretsMountAdapter{}, OpenBaoNamespaceAdapter{}} {
		if !d.Destructive() {
			t.Errorf("%T must be destructive", d)
		}
	}
	for _, a := range []any{PolicyAdapter{}, PasswordPolicyAdapter{}, RateLimitQuotaAdapter{}, UIHeaderAdapter{},
		AuditRequestHeaderAdapter{}, WorkflowAdapter{}, PluginAdapter{}} {
		if _, ok := a.(engine.Singleton); ok {
			t.Errorf("%T is created by the operator and must not be a singleton", a)
		}
	}
	for _, a := range []engine.DriftTier{PolicyAdapter{}, AuthMountAdapter{}, AuditAdapter{}} {
		if a.DriftTier() != engine.TierAccessCritical {
			t.Errorf("%T must be access-critical", a)
		}
	}
}
