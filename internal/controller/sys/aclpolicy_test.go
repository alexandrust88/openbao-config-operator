package sys

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	commonv1alpha1 "github.com/alexandrust88/openbao-config-operator/api/common/v1alpha1"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	sysv1alpha1 "github.com/alexandrust88/openbao-config-operator/api/sys/v1alpha1"
	"github.com/alexandrust88/openbao-config-operator/internal/engine"
	"github.com/alexandrust88/openbao-config-operator/internal/openbao"
)

type readClient struct {
	openbao.Client
	data map[string]map[string]any
}

func (c readClient) Read(_ context.Context, path string) (map[string]any, error) {
	if d, ok := c.data[path]; ok {
		return d, nil
	}
	return nil, openbao.ErrNotFound
}

func newPolicy(name, specName, text string) *sysv1alpha1.ACLPolicy {
	return &sysv1alpha1.ACLPolicy{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: testNamespace},
		Spec:       sysv1alpha1.ACLPolicySpec{Name: specName, Policy: text},
	}
}

func TestPolicyPath(t *testing.T) {
	a := ACLPolicyAdapter{}
	if got := a.Path(newPolicy("reader", "", "x")); got != "sys/policies/acl/reader" {
		t.Errorf("default name: got %q", got)
	}
	if got := a.Path(newPolicy("reader", "custom", "x")); got != "sys/policies/acl/custom" {
		t.Errorf("spec name: got %q", got)
	}
}

func TestPolicyDesired(t *testing.T) {
	text := "path \"kv/*\" {\n  capabilities = [\"read\"]\n}\n"
	desired, err := ACLPolicyAdapter{}.Desired(context.Background(), nil, newPolicy("p", "", text))
	if err != nil {
		t.Fatal(err)
	}
	want := engine.Payload{
		keyPolicy: text, keyCASRequired: false,
		"allow_wildcards_in_identity_templates": false, "allow_slashes_in_identity_templates": false,
	}
	if !reflect.DeepEqual(desired, want) {
		t.Errorf("desired = %v, want %v", desired, want)
	}
}

// TestPolicyObserveGolden uses a read of bao server -dev 2.7.2. A write replaces the flags, so a flag set out of
// band is drift.
func TestPolicyObserveGolden(t *testing.T) {
	text := "path \"a/*\" { capabilities = [\"read\"] }"
	read := map[string]any{
		"allow_slashes_in_identity_templates": false, "allow_wildcards_in_identity_templates": true,
		keyCASRequired: false, "modified": "2026-11-14T01:05:46.316193+02:00", litName: "p1", keyPolicy: text,
		litVersion: json.Number("1"),
	}
	a := ACLPolicyAdapter{}
	ctx := context.Background()
	p := newPolicy("p1", "", text)
	p.Spec.AllowWildcardsInIdentityTemplates = true
	c := readClient{data: map[string]map[string]any{litPolicyP1: read}}
	observed, found, err := a.Observe(ctx, c, p)
	if err != nil || !found {
		t.Fatalf("found=%v err=%v", found, err)
	}
	desired, _ := a.Desired(ctx, nil, p)
	assertInSync(t, desired, observed, a.Fields())

	p.Spec.AllowWildcardsInIdentityTemplates = false
	desired, _ = a.Desired(ctx, nil, p)
	assertDiffers(t, desired, observed, a.Fields(), "allow_wildcards_in_identity_templates")
}

func TestPolicyWriteSendsCAS(t *testing.T) {
	a := ACLPolicyAdapter{}
	ctx := context.Background()
	p := newPolicy("p1", "", "x")
	p.Spec.CASRequired = true
	desired, _ := a.Desired(ctx, nil, p)
	c := newConfigClient(map[string]map[string]any{litPolicyP1: {litVersion: json.Number("4")}})
	if _, err := a.Write(ctx, c, p, desired, true); err != nil {
		t.Fatal(err)
	}
	if got := c.writes[litPolicyP1]["cas"]; got != -1 {
		t.Errorf("create cas = %v, want -1", got)
	}
	if _, err := a.Write(ctx, c, p, desired, false); err != nil {
		t.Fatal(err)
	}
	if got := c.writes[litPolicyP1]["cas"]; got != json.Number("4") {
		t.Errorf("update cas = %v, want 4", got)
	}
	if _, ok := desired["cas"]; ok {
		t.Error("Write mutated desired")
	}

	noVersion := newConfigClient(map[string]map[string]any{litPolicyP1: {}})
	if _, err := a.Write(ctx, noVersion, p, desired, false); err != nil {
		t.Fatal(err)
	}
	if _, ok := noVersion.writes[litPolicyP1]["cas"]; ok {
		t.Error("cas sent although the server reports no version")
	}
}

func TestPolicyObserveNormalisesWhitespace(t *testing.T) {
	spec := "path \"kv/*\" {\n  capabilities = [\"read\"]\n}\n"
	tests := []struct {
		name   string
		stored string
		inSync bool
	}{
		{name: "identical", stored: spec, inSync: true},
		{name: "crlf and trailing spaces", stored: "path \"kv/*\" {   \r\n  capabilities = [\"read\"]\r\n}", inSync: true},
		{name: "indentation and blank lines", stored: "\npath \"kv/*\" {\n\n\tcapabilities = [\"read\"]\n}\n\n", inSync: true},
		{name: "changed capability", stored: "path \"kv/*\" {\n  capabilities = [\"list\"]\n}\n", inSync: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := newPolicy("p", "", spec)
			c := readClient{data: map[string]map[string]any{
				"sys/policies/acl/p": policyRead("p", tt.stored),
			}}
			a := ACLPolicyAdapter{}
			observed, found, err := a.Observe(context.Background(), c, p)
			if err != nil || !found {
				t.Fatalf("found=%v err=%v", found, err)
			}
			desired, _ := a.Desired(context.Background(), nil, p)
			differing := engine.Diff(desired, observed, a.Fields())
			if inSync := len(differing) == 0; inSync != tt.inSync {
				t.Errorf("in sync = %v, want %v (differing %v)", inSync, tt.inSync, differing)
			}
		})
	}
}

func TestPolicyObserveAbsent(t *testing.T) {
	_, found, err := ACLPolicyAdapter{}.Observe(context.Background(), readClient{}, newPolicy("p", "", "x"))
	if err != nil || found {
		t.Errorf("found=%v err=%v, want absent", found, err)
	}
}

// policyRead is the shape of GET sys/policies/acl/<name> on bao server -dev 2.7.2 with the flags at false.
func policyRead(name, text string) map[string]any {
	return map[string]any{
		"allow_slashes_in_identity_templates": false, "allow_wildcards_in_identity_templates": false,
		keyCASRequired: false, "modified": "2026-11-14T01:05:46.352244+02:00", litName: name, keyPolicy: text,
		litVersion: json.Number("2"),
	}
}

// policyRefsEnv returns a PolicyAdapter whose Kubernetes reader holds AuthMount kubernetes (accessor from its
// status) and whose connection serves a sys/auth table with the mount jwt.
func policyRefsEnv(t *testing.T, ready bool) (ACLPolicyAdapter, *configClient) {
	t.Helper()
	scheme := runtime.NewScheme()
	if err := sysv1alpha1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	mount := &sysv1alpha1.AuthMount{ObjectMeta: metav1.ObjectMeta{Name: litKubernetes, Namespace: testNamespace}}
	mount.Status.ID = "auth_kubernetes_1a2b"
	if ready {
		mount.Status.Conditions = []metav1.Condition{{Type: commonv1alpha1.ConditionReady, Status: metav1.ConditionTrue,
			Reason: commonv1alpha1.ReasonAvailable, LastTransitionTime: metav1.Now()}}
	}
	bao := newConfigClient(map[string]map[string]any{
		litSysAuth: {"jwt/": map[string]any{"accessor": "auth_jwt_9f8e", keyType: "jwt"}},
	})
	reader := fake.NewClientBuilder().WithScheme(scheme).WithObjects(mount).Build()
	return ACLPolicyAdapter{Reader: reader, Conns: sysRevConns{bao}}, bao
}

type sysRevConns struct{ c openbao.Client }

func (s sysRevConns) For(context.Context, string, commonv1alpha1.ConnectionReference) (openbao.Client, error) {
	return s.c, nil
}

func TestPolicyAccessorRefs(t *testing.T) {
	ctx := context.Background()
	a, bao := policyRefsEnv(t, true)
	text := `path "kv/{{identity.entity.aliases.${accessor:k8s}.metadata.service_account_name}}/*" { capabilities = ["read"] }
path "jwt/{{identity.entity.aliases.${accessor:jwt}.name}}/*" { capabilities = ["read"] }
path "again/${accessor:k8s}" { capabilities = ["list"] }`
	p := newPolicy("p", "", text)
	p.Spec.AccessorRefs = map[string]sysv1alpha1.ACLPolicyAccessorRef{
		"k8s": {AuthMountRef: &commonv1alpha1.LocalObjectReference{Name: litKubernetes}},
		"jwt": {MountPath: "jwt"},
	}
	desired, err := a.Desired(ctx, nil, p)
	if err != nil {
		t.Fatal(err)
	}
	want := `path "kv/{{identity.entity.aliases.auth_kubernetes_1a2b.metadata.service_account_name}}/*" { capabilities = ["read"] }
path "jwt/{{identity.entity.aliases.auth_jwt_9f8e.name}}/*" { capabilities = ["read"] }
path "again/auth_kubernetes_1a2b" { capabilities = ["list"] }`
	if desired[keyPolicy] != want {
		t.Errorf("rendered policy =\n%s\nwant\n%s", desired[keyPolicy], want)
	}
	if p.Spec.Policy != text {
		t.Error("Desired mutated the spec")
	}

	bao.data["sys/policies/acl/p"] = policyRead("p", strings.ReplaceAll(want, "\n", "\n\n"))
	observed, found, err := a.Observe(ctx, bao, p)
	if err != nil || !found {
		t.Fatalf("found=%v err=%v", found, err)
	}
	assertInSync(t, desired, observed, a.Fields())

	if got := a.References(p); !reflect.DeepEqual(got, []engine.ObjectReference{{Kind: "AuthMount", Name: litKubernetes}}) {
		t.Errorf("references = %v", got)
	}
}

func TestPolicyAccessorRefsErrors(t *testing.T) {
	ctx := context.Background()
	unknown := newPolicy("p", "", `path "a/${accessor:nope}" { capabilities = ["read"] }`)
	a, _ := policyRefsEnv(t, true)
	if _, err := a.Desired(ctx, nil, unknown); !errors.Is(err, engine.ErrInvalidSpec) {
		t.Errorf("unknown placeholder: err = %v, want ErrInvalidSpec", err)
	}

	notReady, _ := policyRefsEnv(t, false)
	p := newPolicy("p", "", `path "a/${accessor:k8s}" { capabilities = ["read"] }`)
	p.Spec.AccessorRefs = map[string]sysv1alpha1.ACLPolicyAccessorRef{
		"k8s": {AuthMountRef: &commonv1alpha1.LocalObjectReference{Name: litKubernetes}},
	}
	if _, err := notReady.Desired(ctx, nil, p); !errors.Is(err, engine.ErrReferenceNotReady) {
		t.Errorf("AuthMount not ready: err = %v, want ErrReferenceNotReady", err)
	}
	p.Spec.AccessorRefs = map[string]sysv1alpha1.ACLPolicyAccessorRef{"k8s": {MountPath: "missing"}}
	if _, err := a.Desired(ctx, nil, p); !errors.Is(err, engine.ErrReferenceNotReady) {
		t.Errorf("missing mount path: err = %v, want ErrReferenceNotReady", err)
	}

	templated := newPolicy("p", "", `path "a/{{identity.entity.id}}" { capabilities = ["read"] }`)
	desired, err := ACLPolicyAdapter{}.Desired(ctx, nil, templated)
	if err != nil || desired[keyPolicy] != templated.Spec.Policy {
		t.Errorf("a policy without placeholders must pass unchanged: %v, %v", desired[keyPolicy], err)
	}
}

func TestPolicyWriteStatus(t *testing.T) {
	p := newPolicy("Ignored", "team/app-read", "x")
	if err := (ACLPolicyAdapter{}).WriteStatus(context.Background(), nil, p, nil); err != nil {
		t.Fatal(err)
	}
	if p.Status.ID != "team/app-read" {
		t.Errorf("status.id = %q", p.Status.ID)
	}
	p = newPolicy("Reader", "", "x")
	_ = ACLPolicyAdapter{}.WriteStatus(context.Background(), nil, p, nil)
	if p.Status.ID != "reader" {
		t.Errorf("status.id = %q, want the name lowercased as OpenBao stores it", p.Status.ID)
	}
}
