package sys

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	commonv1alpha1 "github.com/alexandrust88/openbao-config-operator/api/common/v1alpha1"
	sysv1alpha1 "github.com/alexandrust88/openbao-config-operator/api/sys/v1alpha1"
	"github.com/alexandrust88/openbao-config-operator/internal/engine"
)

const testPluginSHA = "275239824e00e61b0a220e61a41791c7e9b4bd726f8b0c27077a338f8131c9dc"

func newPlugin(version string) *sysv1alpha1.Plugin {
	return &sysv1alpha1.Plugin{
		ObjectMeta: metav1.ObjectMeta{Name: "ldap", Namespace: "platform", Generation: 2},
		Spec: sysv1alpha1.PluginSpec{
			Type:    authTable,
			Name:    litOpenbaoPluginAuthLdap,
			Command: litOpenbaoPluginAuthLdap,
			SHA256:  testPluginSHA,
			Args:    []string{"--tls-skip-verify"},
			Env: []sysv1alpha1.PluginEnvVar{
				{Name: "HTTPS_PROXY", SecretKeyRef: commonv1alpha1.SecretKeySelector{Name: "plugin-env", Key: "proxy"}},
			},
			Version: version,
		},
	}
}

// pluginRead is the read of an unversioned external plugin; env is never returned.
func pluginRead() map[string]any {
	return map[string]any{
		litName: litOpenbaoPluginAuthLdap, litCommand: litOpenbaoPluginAuthLdap, "args": []any{"--tls-skip-verify"},
		litSha256: testPluginSHA, litBuiltin: false, litVersion: "", "oci": false, "declarative": false,
	}
}

func catalogListing(entries ...map[string]any) map[string]any {
	detailed := make([]any, 0, 1+len(entries))
	detailed = append(detailed, map[string]any{keyType: authTable, litName: litKubernetes, litVersion: "v2.7.2+builtin.bao", litBuiltin: true})
	for _, e := range entries {
		detailed = append(detailed, e)
	}
	return map[string]any{authTable: []any{litKubernetes}, "detailed": detailed}
}

func TestPluginDesired(t *testing.T) {
	a := PluginAdapter{}
	p := newPlugin("0.21.0")
	desired, err := a.Desired(context.Background(), secretResolver{litPluginEnvProxy: "http://proxy:3128"}, p)
	if err != nil {
		t.Fatal(err)
	}
	if desired[litVersion] != litV0210 || desired[litSha256] != testPluginSHA || desired["oci"] != false {
		t.Errorf("desired = %v", desired)
	}
	if env := desired["env"].([]string); !slices.Equal(env, []string{"HTTPS_PROXY=http://proxy:3128"}) {
		t.Errorf("env = %v", env)
	}
	if !slices.Contains(a.Fields().WriteOnly, "env") || engine.WriteOnlyHash([]byte("k"), desired, a.Fields()) == "" {
		t.Error("env must be write-only and hashed")
	}
	if a.Path(p) != "sys/plugins/catalog/auth/openbao-plugin-auth-ldap" {
		t.Errorf("path %q", a.Path(p))
	}
	if refs := a.SecretRefs(p); !slices.Equal(refs, []string{"plugin-env"}) {
		t.Errorf("secret refs = %v", refs)
	}

	_, err = a.Desired(context.Background(), secretResolver{}, p)
	if !errors.Is(err, engine.ErrReferenceNotReady) || strings.Contains(err.Error(), "proxy:3128") {
		t.Errorf("missing secret: err = %v", err)
	}

	p.Spec.Args, p.Spec.Env = nil, nil
	desired, _ = a.Desired(context.Background(), secretResolver{}, p)
	if args, ok := desired["args"].([]string); !ok || args == nil {
		t.Errorf("args = %#v, want an empty list so removed args are cleared", desired["args"])
	}
	if _, ok := desired["env"]; ok {
		t.Error("env must be omitted when the spec has none")
	}
}

func TestPluginObserveUnversioned(t *testing.T) {
	a := PluginAdapter{}
	ctx := context.Background()
	p := newPlugin("")
	path := a.Path(p)
	desired, _ := a.Desired(ctx, secretResolver{litPluginEnvProxy: "x"}, p)

	c := newConfigClient(map[string]map[string]any{path: pluginRead()})
	observed, found, err := a.Observe(ctx, c, p)
	if err != nil || !found {
		t.Fatalf("found=%v err=%v", found, err)
	}
	assertInSync(t, desired, observed, a.Fields())

	c.data[path]["args"] = []any{"--other"}
	observed, _, _ = a.Observe(ctx, c, p)
	assertDiffers(t, desired, observed, a.Fields(), "args")

	builtin := map[string]any{litName: litOpenbaoPluginAuthLdap, litBuiltin: true, litCommand: ""}
	c = newConfigClient(map[string]map[string]any{path: builtin, pluginCatalogPath: catalogListing()})
	if _, found, err := a.Observe(ctx, c, p); err != nil || found {
		t.Errorf("builtin shadow: found=%v err=%v, want absent", found, err)
	}

	selfVersioned := map[string]any{keyType: authTable, litName: litOpenbaoPluginAuthLdap, litVersion: litV0210, litBuiltin: false}
	c = newConfigClient(map[string]map[string]any{pluginCatalogPath: catalogListing(selfVersioned)})
	if _, _, err := a.Observe(ctx, c, p); err == nil || !strings.Contains(err.Error(), litV0210) {
		t.Errorf("self-reported version: err = %v, want a hint to set spec.version", err)
	}

	_, _, err = a.Observe(ctx, newConfigClient(nil).WithNamespace(testNamespace), p)
	assertUnsupported(t, err)
}

func TestPluginObserveVersioned(t *testing.T) {
	a := PluginAdapter{}
	ctx := context.Background()
	p := newPlugin(litV0210)
	desired, _ := a.Desired(ctx, secretResolver{litPluginEnvProxy: "x"}, p)
	entry := map[string]any{keyType: authTable, litName: litOpenbaoPluginAuthLdap, litVersion: litV0210, litBuiltin: false,
		litSha256: testPluginSHA}
	other := map[string]any{keyType: authTable, litName: litOpenbaoPluginAuthLdap, litVersion: "v0.20.0", litBuiltin: false,
		litSha256: strings.Repeat("0", 64)}
	c := newConfigClient(map[string]map[string]any{pluginCatalogPath: catalogListing(other, entry)})

	observed, found, err := a.Observe(ctx, c, p)
	if err != nil || !found {
		t.Fatalf("found=%v err=%v", found, err)
	}
	if differing := engine.Diff(desired, observed, a.Fields()); !slices.Equal(differing, []string{"args", litCommand, "oci"}) {
		t.Errorf("unreconciled generation: differing %v, want the unreadable fields so the spec is written", differing)
	}

	p.Status.Acquired, p.Status.ObservedGeneration = commonv1alpha1.AcquiredCreated, p.Generation
	p.Status.Conditions = []metav1.Condition{
		{Type: commonv1alpha1.ConditionReady, Status: metav1.ConditionTrue, Reason: commonv1alpha1.ReasonAvailable, ObservedGeneration: p.Generation - 1},
		{Type: commonv1alpha1.ConditionSynced, Status: metav1.ConditionFalse, Reason: commonv1alpha1.ReasonConnectionNotReady, ObservedGeneration: p.Generation},
	}
	observed, _, _ = a.Observe(ctx, c, p)
	if differing := engine.Diff(desired, observed, a.Fields()); !slices.Equal(differing, []string{"args", litCommand, "oci"}) {
		t.Errorf("after a failed pass: differing %v, want the unreadable fields so the spec is written", differing)
	}

	p.Status.Conditions = []metav1.Condition{
		{Type: commonv1alpha1.ConditionReady, Status: metav1.ConditionTrue, Reason: commonv1alpha1.ReasonAvailable, ObservedGeneration: p.Generation},
		{Type: commonv1alpha1.ConditionSynced, Status: metav1.ConditionTrue, Reason: commonv1alpha1.ReasonReconcileSuccess, ObservedGeneration: p.Generation},
	}
	observed, _, _ = a.Observe(ctx, c, p)
	assertInSync(t, desired, observed, a.Fields())

	entry[litSha256] = strings.Repeat("f", 64)
	observed, _, _ = a.Observe(ctx, c, p)
	assertDiffers(t, desired, observed, a.Fields(), litSha256)

	c = newConfigClient(map[string]map[string]any{pluginCatalogPath: catalogListing(other)})
	if _, found, err := a.Observe(ctx, c, p); err != nil || found {
		t.Errorf("unregistered version: found=%v err=%v", found, err)
	}
}
