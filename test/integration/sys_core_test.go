//go:build integration

package integration

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/utils/ptr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/config"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	"sigs.k8s.io/controller-runtime/pkg/metrics"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"

	commonv1alpha1 "github.com/alexandrust88/openbao-config-operator/api/common/v1alpha1"
	sysv1alpha1 "github.com/alexandrust88/openbao-config-operator/api/sys/v1alpha1"
	"github.com/alexandrust88/openbao-config-operator/internal/controller/sys"
	"github.com/alexandrust88/openbao-config-operator/internal/engine"
	"github.com/alexandrust88/openbao-config-operator/internal/openbao"
	"github.com/alexandrust88/openbao-config-operator/test/testenv"
)

// TestIntegrationSysCore runs OpenBaoNamespace, AuthMount, SecretsMount, PasswordPolicy, RateLimitQuota and the
// Policy flags against envtest and bao server -dev.
func TestIntegrationSysCore(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	e := sysCoreSetup(ctx, t)

	t.Run("openbaonamespace", func(t *testing.T) { testOpenBaoNamespace(ctx, t, e) })
	t.Run("authmount", func(t *testing.T) { testAuthMount(ctx, t, e) })
	t.Run("secretsmount", func(t *testing.T) { testSecretsMount(ctx, t, e) })
	t.Run("passwordpolicy", func(t *testing.T) { testPasswordPolicy(ctx, t, e) })
	t.Run("ratelimitquota", func(t *testing.T) { testRateLimitQuota(ctx, t, e) })
	t.Run("ratelimitquota defaults", func(t *testing.T) { testSysRevQuotaDefaults(ctx, t, e) })
	t.Run("policy flags", func(t *testing.T) { testSysRevPolicyFlags(ctx, t, e) })
	t.Run("policy accessor refs", func(t *testing.T) { testSysRevPolicyAccessor(ctx, t, e) })
}

// testSysRevPolicyAccessor checks ${accessor:<name>} in a Policy: the accessor of the referenced AuthMount is
// written, and a recreated mount with a new accessor rewrites the policy.
func testSysRevPolicyAccessor(ctx context.Context, t *testing.T, e env) {
	const policyPath = "sys/policies/acl/itest-accessor"
	m := &sysv1alpha1.AuthMount{
		ObjectMeta: metav1.ObjectMeta{Name: "itest-acc", Namespace: namespace},
		Spec:       sysv1alpha1.AuthMountSpec{ResourceSpec: sysCoreSpec(), Type: "userpass"},
	}
	mustCreate(ctx, t, e.k8s, m)
	waitFor(t, "auth mount ready", func() error { return expectReady(ctx, e.k8s, m) })
	first := m.Status.ID

	p := &sysv1alpha1.Policy{
		ObjectMeta: metav1.ObjectMeta{Name: "itest-accessor", Namespace: namespace},
		Spec: sysv1alpha1.PolicySpec{
			ResourceSpec: sysRevDeleteSpec(),
			Policy:       "path \"kv/{{identity.entity.aliases.${accessor:up}.name}}/*\" {\n  capabilities = [\"read\"]\n}\n",
			AccessorRefs: map[string]sysv1alpha1.PolicyAccessorRef{
				"up": {AuthMountRef: &commonv1alpha1.LocalObjectReference{Name: m.Name}},
			},
		},
	}
	mustCreate(ctx, t, e.k8s, p)
	sysRevPolicyText := func(accessor string) func() error {
		return func() error {
			want := "path \"kv/{{identity.entity.aliases." + accessor + ".name}}/*\" {\n  capabilities = [\"read\"]\n}\n"
			return expectPolicy(ctx, e.bao, policyPath, want)
		}
	}
	waitFor(t, "policy rendered with the accessor", sysRevPolicyText(first))
	waitFor(t, "policy ready", func() error { return expectReady(ctx, e.k8s, p) })
	if p.Status.ID != "itest-accessor" {
		t.Errorf("status.id = %q, want the policy name", p.Status.ID)
	}

	if err := e.bao.Delete(ctx, "sys/auth/itest-acc"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "auth mount recreated with a new accessor", func() error {
		if err := e.k8s.Get(ctx, client.ObjectKeyFromObject(m), m); err != nil {
			return err
		}
		if m.Status.ID == "" || m.Status.ID == first {
			return fmt.Errorf("status.id = %q, want a new accessor", m.Status.ID)
		}
		return nil
	})
	waitFor(t, "policy rewritten with the new accessor", sysRevPolicyText(m.Status.ID))
	expectNoWrites(t, "Policy")

	if err := e.k8s.Delete(ctx, p); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "accessor policy deleted", func() error { return expectAbsent(ctx, e.bao, policyPath) })
}

// testSysRevQuotaDefaults checks that a write, which replaces every setting of a quota, is repaired to the
// server defaults of the fields the spec leaves unset.
func testSysRevQuotaDefaults(ctx context.Context, t *testing.T, e env) {
	const path = "sys/quotas/rate-limit/itest-quota-global"
	q := &sysv1alpha1.RateLimitQuota{
		ObjectMeta: metav1.ObjectMeta{Name: "itest-quota-global", Namespace: namespace},
		Spec:       sysv1alpha1.RateLimitQuotaSpec{ResourceSpec: sysRevDeleteSpec(), Rate: "50"},
	}
	mustCreate(ctx, t, e.k8s, q)
	want := map[string]any{"rate": "50", "path": "", "interval": "1", "block_interval": "0", "inheritable": "true"}
	waitFor(t, "global quota created", func() error { return expectFields(ctx, e.bao, path, want) })
	waitFor(t, "global quota ready", func() error { return expectReady(ctx, e.k8s, q) })

	mustWrite(ctx, t, e.bao, path, map[string]any{"rate": 50, "interval": 9, "block_interval": 3, "inheritable": false})
	waitFor(t, "global quota defaults repaired", func() error { return expectFields(ctx, e.bao, path, want) })
	expectNoWrites(t, "RateLimitQuota")

	if err := e.k8s.Delete(ctx, q); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "global quota deleted", func() error { return expectAbsent(ctx, e.bao, path) })
}

// testSysRevPolicyFlags checks the policy flags, which every write replaces, and the check-and-set write that a
// policy with casRequired needs.
func testSysRevPolicyFlags(ctx context.Context, t *testing.T, e env) {
	const path = "sys/policies/acl/itest-flags"
	text := "path \"kv/{{identity.entity.name}}/*\" {\n  capabilities = [\"read\"]\n}\n"
	p := &sysv1alpha1.Policy{
		ObjectMeta: metav1.ObjectMeta{Name: "itest-flags", Namespace: namespace},
		Spec: sysv1alpha1.PolicySpec{
			ResourceSpec:                    sysRevDeleteSpec(),
			Policy:                          text,
			CASRequired:                     true,
			AllowSlashesInIdentityTemplates: true,
		},
	}
	mustCreate(ctx, t, e.k8s, p)
	want := map[string]any{
		"cas_required": "true", "allow_slashes_in_identity_templates": "true",
		"allow_wildcards_in_identity_templates": "false",
	}
	waitFor(t, "policy created", func() error { return expectFields(ctx, e.bao, path, want) })
	waitFor(t, "policy ready", func() error { return expectReady(ctx, e.k8s, p) })

	current, err := e.bao.Read(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	mustWrite(ctx, t, e.bao, path, map[string]any{"policy": text, "cas_required": true, "cas": current["version"]})
	waitFor(t, "policy flags repaired", func() error { return expectFields(ctx, e.bao, path, want) })
	expectNoWrites(t, "Policy")

	if err := e.k8s.Delete(ctx, p); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "policy deleted", func() error { return expectAbsent(ctx, e.bao, path) })
}

// sysRevDeleteSpec is sysCoreSpec with deletionPolicy Delete, which a resource needs to be removed from OpenBao.
func sysRevDeleteSpec() commonv1alpha1.ResourceSpec {
	spec := sysCoreSpec()
	spec.DeletionPolicy = commonv1alpha1.DeletionPolicyDelete
	return spec
}

// sysRevDestructive lets a Delete policy remove a kind that holds data.
func sysRevDestructive(obj client.Object) {
	obj.SetAnnotations(map[string]string{commonv1alpha1.AnnotationAllowDestructiveDelete: "true"})
}

// sysRevAllowDestructive annotates a live resource so that its guarded delete proceeds.
func sysRevAllowDestructive(ctx context.Context, t *testing.T, e env, obj client.Object) {
	t.Helper()
	patch := client.MergeFrom(obj.DeepCopyObject().(client.Object))
	sysRevDestructive(obj)
	if err := e.k8s.Patch(ctx, obj, patch); err != nil {
		t.Fatalf("annotate %s: %v", obj.GetName(), err)
	}
}

func sysCoreSpec() commonv1alpha1.ResourceSpec {
	return commonv1alpha1.ResourceSpec{ConnectionRef: commonv1alpha1.ConnectionReference{Name: "dev"}}
}

func testOpenBaoNamespace(ctx context.Context, t *testing.T, e env) {
	parent := &sysv1alpha1.OpenBaoNamespace{
		ObjectMeta: metav1.ObjectMeta{Name: "itest-parent", Namespace: namespace},
		Spec: sysv1alpha1.OpenBaoNamespaceSpec{
			ResourceSpec:   sysCoreSpec(),
			CustomMetadata: map[string]string{"team": "a"},
		},
	}
	parent.Spec.DeletionPolicy = commonv1alpha1.DeletionPolicyDelete
	child := &sysv1alpha1.OpenBaoNamespace{
		ObjectMeta: metav1.ObjectMeta{Name: "itest-child", Namespace: namespace},
		Spec:       sysv1alpha1.OpenBaoNamespaceSpec{ResourceSpec: sysCoreSpec()},
	}
	child.Spec.Namespace = "itest-parent"
	child.Spec.DeletionPolicy = commonv1alpha1.DeletionPolicyDelete
	sysRevDestructive(parent)
	sysRevDestructive(child)
	mustCreate(ctx, t, e.k8s, parent)
	mustCreate(ctx, t, e.k8s, child)

	const parentPath = "sys/namespaces/itest-parent"
	inParent := e.bao.WithNamespace("itest-parent")
	waitFor(t, "parent created", func() error {
		return expectMetadata(ctx, e.bao, parentPath, map[string]any{"team": "a"})
	})
	waitFor(t, "child created", func() error { return expectPresent(ctx, inParent, "sys/namespaces/itest-child") })
	waitFor(t, "parent ready", func() error { return expectReady(ctx, e.k8s, parent) })
	waitFor(t, "child ready", func() error { return expectReady(ctx, e.k8s, child) })
	if parent.Status.ID == "" {
		t.Error("status.id of the parent is empty")
	}

	renamed := parent.DeepCopy()
	renamed.Spec.Name = "other"
	if err := e.k8s.Update(ctx, renamed); !apierrors.IsInvalid(err) {
		t.Errorf("update of spec.name: got %v, want it rejected as immutable", err)
	}

	mustWrite(ctx, t, e.bao, parentPath, map[string]any{"custom_metadata": map[string]any{"team": "x", "extra": "y"}})
	waitFor(t, "custom metadata repaired", func() error {
		return expectMetadata(ctx, e.bao, parentPath, map[string]any{"team": "a"})
	})
	expectNoWrites(t, "OpenBaoNamespace")

	if err := e.k8s.Delete(ctx, parent); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "parent deletion blocked by child", func() error {
		return expectSynced(ctx, e.k8s, parent, commonv1alpha1.ReasonCleanupBlocked, "child namespaces")
	})
	if err := expectPresent(ctx, e.bao, parentPath); err != nil {
		t.Fatalf("parent must stay while it has children: %v", err)
	}
	if err := e.k8s.Delete(ctx, child); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "child resource gone", func() error { return expectGone(ctx, e.k8s, child) })
	waitFor(t, "parent deleted", func() error { return expectAbsent(ctx, e.bao, parentPath) })
	waitFor(t, "parent resource gone", func() error { return expectGone(ctx, e.k8s, parent) })

	orphan := &sysv1alpha1.OpenBaoNamespace{
		ObjectMeta: metav1.ObjectMeta{Name: "itest-orphan", Namespace: namespace},
		Spec:       sysv1alpha1.OpenBaoNamespaceSpec{ResourceSpec: sysCoreSpec()},
	}
	mustCreate(ctx, t, e.k8s, orphan)
	waitFor(t, "orphan ready", func() error { return expectReady(ctx, e.k8s, orphan) })
	if err := e.k8s.Delete(ctx, orphan); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "orphan resource gone", func() error { return expectGone(ctx, e.k8s, orphan) })
	if err := expectPresent(ctx, e.bao, "sys/namespaces/itest-orphan"); err != nil {
		t.Errorf("default deletion policy must keep the namespace: %v", err)
	}
}

func testAuthMount(ctx context.Context, t *testing.T, e env) {
	m := &sysv1alpha1.AuthMount{
		ObjectMeta: metav1.ObjectMeta{Name: "itest-userpass", Namespace: namespace},
		Spec: sysv1alpha1.AuthMountSpec{
			ResourceSpec: sysCoreSpec(),
			Path:         "itest-up",
			Type:         "userpass",
			MountFields: sysv1alpha1.MountFields{
				Description:             new("integration"),
				DefaultLeaseTTL:         &metav1.Duration{Duration: time.Hour},
				AuditNonHMACRequestKeys: []string{"b", "a"},
			},
			TokenType:   sysv1alpha1.MountTokenTypeBatch,
			UserLockout: &sysv1alpha1.UserLockoutConfig{Threshold: ptr.To[int32](5), Duration: &metav1.Duration{Duration: 15 * time.Minute}},
		},
	}
	m.Spec.DeletionPolicy = commonv1alpha1.DeletionPolicyDelete
	mustCreate(ctx, t, e.k8s, m)

	waitFor(t, "auth method enabled", func() error {
		return expectMount(ctx, e.bao, "auth", "itest-up", map[string]any{
			"type": "userpass", "description": "integration",
			"config.default_lease_ttl": "3600", "config.token_type": "batch",
			"config.user_lockout_config.user_lockout_threshold": "5",
		})
	})
	waitFor(t, "auth mount ready", func() error { return expectReady(ctx, e.k8s, m) })
	if !strings.HasPrefix(m.Status.ID, "auth_userpass_") {
		t.Errorf("status.id = %q, want the mount accessor", m.Status.ID)
	}

	changed := m.DeepCopy()
	changed.Spec.Type = "approle"
	if err := e.k8s.Update(ctx, changed); !apierrors.IsInvalid(err) {
		t.Errorf("update of spec.type: got %v, want it rejected as immutable", err)
	}

	mustWrite(ctx, t, e.bao, "sys/auth/itest-up/tune", map[string]any{"description": "tampered"})
	waitFor(t, "description repaired", func() error {
		return expectMount(ctx, e.bao, "auth", "itest-up", map[string]any{"description": "integration"})
	})
	mustWrite(ctx, t, e.bao, "sys/auth/itest-up/tune", map[string]any{"audit_non_hmac_request_keys": []string{"a", "b"}})
	expectNoWrites(t, "AuthMount")
	if err := expectMount(ctx, e.bao, "auth", "itest-up", map[string]any{"config.audit_non_hmac_request_keys": "[a b]"}); err != nil {
		t.Errorf("reordered set rewritten: %v", err)
	}

	if err := e.k8s.Delete(ctx, m); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "auth mount deletion guarded", func() error {
		return expectSynced(ctx, e.k8s, m, commonv1alpha1.ReasonDeleteGuarded, commonv1alpha1.AnnotationAllowDestructiveDelete)
	})
	if err := expectMount(ctx, e.bao, "auth", "itest-up", map[string]any{"type": "userpass"}); err != nil {
		t.Fatalf("a guarded delete must keep the mount: %v", err)
	}
	sysRevAllowDestructive(ctx, t, e, m)
	waitFor(t, "auth method disabled", func() error { return expectNoMount(ctx, e.bao, "auth", "itest-up") })
	waitFor(t, "auth mount resource gone", func() error { return expectGone(ctx, e.k8s, m) })
}

func testSecretsMount(ctx context.Context, t *testing.T, e env) {
	m := &sysv1alpha1.SecretsMount{
		ObjectMeta: metav1.ObjectMeta{Name: "itest-kv", Namespace: namespace},
		Spec: sysv1alpha1.SecretsMountSpec{
			ResourceSpec: sysCoreSpec(),
			Type:         "kv-v2",
			MountFields: sysv1alpha1.MountFields{
				MaxLeaseTTL:  &metav1.Duration{Duration: 2 * time.Hour},
				ForceNoCache: new(true),
			},
		},
	}
	mustCreate(ctx, t, e.k8s, m)

	waitFor(t, "secrets engine enabled", func() error {
		return expectMount(ctx, e.bao, "mounts", "itest-kv", map[string]any{
			"type": "kv", "options.version": "2", "config.max_lease_ttl": "7200", "config.force_no_cache": "true",
		})
	})
	waitFor(t, "secrets mount ready", func() error { return expectReady(ctx, e.k8s, m) })
	if !strings.HasPrefix(m.Status.ID, "kv_") {
		t.Errorf("status.id = %q, want the mount accessor", m.Status.ID)
	}

	mustWrite(ctx, t, e.bao, "sys/mounts/itest-kv/tune", map[string]any{"max_lease_ttl": "60"})
	waitFor(t, "max lease ttl repaired", func() error {
		return expectMount(ctx, e.bao, "mounts", "itest-kv", map[string]any{"config.max_lease_ttl": "7200"})
	})
	expectNoWrites(t, "SecretsMount")

	if err := e.k8s.Delete(ctx, m); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "secrets mount resource gone", func() error { return expectGone(ctx, e.k8s, m) })
	if err := expectMount(ctx, e.bao, "mounts", "itest-kv", map[string]any{"type": "kv"}); err != nil {
		t.Errorf("default deletion policy must keep the mount: %v", err)
	}
}

func testPasswordPolicy(ctx context.Context, t *testing.T, e env) {
	const path = "sys/policies/password/itest-pw"
	text := "length = 24\nrule \"charset\" {\n  charset = \"abcdefghijklmnopqrstuvwxyz0123456789\"\n}\n"
	p := &sysv1alpha1.PasswordPolicy{
		ObjectMeta: metav1.ObjectMeta{Name: "itest-pw", Namespace: namespace},
		Spec:       sysv1alpha1.PasswordPolicySpec{ResourceSpec: sysRevDeleteSpec(), Policy: text},
	}
	mustCreate(ctx, t, e.k8s, p)
	waitFor(t, "password policy created", func() error { return expectPolicy(ctx, e.bao, path, text) })
	waitFor(t, "password policy ready", func() error { return expectReady(ctx, e.k8s, p) })

	mustWrite(ctx, t, e.bao, path, map[string]any{"policy": "length = 8\nrule \"charset\" {\n  charset = \"ab\"\n}\n"})
	waitFor(t, "password policy repaired", func() error { return expectPolicy(ctx, e.bao, path, text) })
	expectNoWrites(t, "PasswordPolicy")

	if err := e.k8s.Delete(ctx, p); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "password policy deleted", func() error { return expectAbsent(ctx, e.bao, path) })
	waitFor(t, "password policy resource gone", func() error { return expectGone(ctx, e.k8s, p) })
}

func testRateLimitQuota(ctx context.Context, t *testing.T, e env) {
	const path = "sys/quotas/rate-limit/itest-quota"
	mustWrite(ctx, t, e.bao, "sys/mounts/itest-quota-kv", map[string]any{"type": "kv"})
	q := &sysv1alpha1.RateLimitQuota{
		ObjectMeta: metav1.ObjectMeta{Name: "itest-quota", Namespace: namespace},
		Spec: sysv1alpha1.RateLimitQuotaSpec{
			ResourceSpec:  sysRevDeleteSpec(),
			Path:          "itest-quota-kv",
			Rate:          "1000",
			Interval:      &metav1.Duration{Duration: time.Second},
			BlockInterval: &metav1.Duration{Duration: 10 * time.Second},
		},
	}
	mustCreate(ctx, t, e.k8s, q)
	want := map[string]any{"rate": "1000", "path": "itest-quota-kv/", "block_interval": "10"}
	waitFor(t, "quota created", func() error { return expectFields(ctx, e.bao, path, want) })
	waitFor(t, "quota ready", func() error { return expectReady(ctx, e.k8s, q) })

	namespaced := &sysv1alpha1.RateLimitQuota{
		ObjectMeta: metav1.ObjectMeta{Name: "itest-quota-ns", Namespace: namespace},
		Spec:       sysv1alpha1.RateLimitQuotaSpec{ResourceSpec: sysCoreSpec(), Rate: "10"},
	}
	namespaced.Spec.Namespace = "team"
	if err := e.k8s.Create(ctx, namespaced); !apierrors.IsInvalid(err) || !strings.Contains(err.Error(), "root namespace") {
		t.Errorf("namespaced quota: got %v, want it rejected", err)
	}

	mustWrite(ctx, t, e.bao, path, map[string]any{"path": "itest-quota-kv", "rate": 5, "block_interval": 10})
	waitFor(t, "quota repaired", func() error { return expectFields(ctx, e.bao, path, want) })
	expectNoWrites(t, "RateLimitQuota")

	if err := e.k8s.Delete(ctx, q); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "quota deleted", func() error { return expectAbsent(ctx, e.bao, path) })
	waitFor(t, "quota resource gone", func() error { return expectGone(ctx, e.k8s, q) })
}

// expectNoWrites asserts that the engine sends no write for kind over three drift intervals.
func expectNoWrites(t *testing.T, kind string) {
	t.Helper()
	// Let the repair that a waitFor observed finish its status patch before counting.
	time.Sleep(driftInterval)
	before := sysCoreWrites(t, kind)
	time.Sleep(3 * driftInterval)
	if after := sysCoreWrites(t, kind); after != before {
		t.Errorf("%s: %v writes at rest, want none", kind, after-before)
	}
}

func sysCoreWrites(t *testing.T, kind string) float64 {
	t.Helper()
	families, err := metrics.Registry.Gather()
	if err != nil {
		t.Fatal(err)
	}
	for _, family := range families {
		if family.GetName() != "openbao_writes_total" {
			continue
		}
		for _, m := range family.GetMetric() {
			for _, label := range m.GetLabel() {
				if label.GetName() == "kind" && label.GetValue() == kind {
					return m.GetCounter().GetValue()
				}
			}
		}
	}
	return 0
}

func expectPresent(ctx context.Context, bao openbao.Client, path string) error {
	_, err := bao.Read(ctx, path)
	return err
}

func expectMetadata(ctx context.Context, bao openbao.Client, path string, want map[string]any) error {
	data, err := bao.Read(ctx, path)
	if err != nil {
		return err
	}
	got, _ := data["custom_metadata"].(map[string]any)
	user := map[string]any{}
	for key, value := range got {
		if !strings.HasPrefix(key, sysv1alpha1.CustomMetadataMarkerPrefix) {
			user[key] = value
		}
	}
	if !reflect.DeepEqual(user, want) {
		return fmt.Errorf("custom_metadata = %v, want %v plus the ownership marker", got, want)
	}
	if got[sysv1alpha1.CustomMetadataMarkerPrefix+"uid"] == nil {
		return fmt.Errorf("custom_metadata = %v has no ownership marker", got)
	}
	return nil
}

// expectFields compares the fmt.Sprint form of dotted keys in a read.
func expectFields(ctx context.Context, bao openbao.Client, path string, want map[string]any) error {
	data, err := bao.Read(ctx, path)
	if err != nil {
		return err
	}
	return compareDotted(data, want)
}

func expectMount(ctx context.Context, bao openbao.Client, table, path string, want map[string]any) error {
	mounts, err := bao.Read(ctx, "sys/"+table)
	if err != nil {
		return err
	}
	entry, ok := mounts[path+"/"].(map[string]any)
	if !ok {
		return fmt.Errorf("no mount at %s", path)
	}
	return compareDotted(entry, want)
}

func expectNoMount(ctx context.Context, bao openbao.Client, table, path string) error {
	mounts, err := bao.Read(ctx, "sys/"+table)
	if err != nil {
		return err
	}
	if _, ok := mounts[path+"/"]; ok {
		return fmt.Errorf("mount %s still exists", path)
	}
	return nil
}

func compareDotted(data map[string]any, want map[string]any) error {
	for key, value := range want {
		var got any = data
		for part := range strings.SplitSeq(key, ".") {
			m, _ := got.(map[string]any)
			got = m[part]
		}
		if fmt.Sprint(got) != fmt.Sprint(value) {
			return fmt.Errorf("%s = %v, want %v", key, got, value)
		}
	}
	return nil
}

func expectSynced(ctx context.Context, k8s client.Client, obj engine.Object, reason, message string) error {
	if err := k8s.Get(ctx, client.ObjectKeyFromObject(obj), obj); err != nil {
		return err
	}
	c := meta.FindStatusCondition(obj.GetResourceStatus().Conditions, commonv1alpha1.ConditionSynced)
	if c == nil || c.Reason != reason || !strings.Contains(c.Message, message) {
		return fmt.Errorf("synced condition = %+v, want reason %s with %q", c, reason, message)
	}
	return nil
}

func sysCoreSetup(ctx context.Context, t *testing.T) env {
	t.Helper()
	ctrl.SetLogger(zap.New(zap.WriteTo(os.Stderr), zap.UseDevMode(true)))
	root := repoRoot(t)

	baoBin := testenv.RequireBao(t)
	assets := testenv.RequireAssets(t, root)

	addr := startBao(t, baoBin)
	bao, err := openbao.NewPool().Client(ctx, "itest-sys", openbao.Config{
		Address: addr,
		Timeout: 10 * time.Second,
		Auth: openbao.TokenAuth{
			CredentialKey: "itest-sys",
			Token:         func(context.Context) (string, error) { return rootToken, nil },
		},
	})
	if err != nil {
		t.Fatalf("openbao client: %v", err)
	}

	names := []string{
		"openbaonamespaces", "authmounts", "secretsmounts", "passwordpolicies", "ratelimitquotas", "policies", "plugins",
	}
	crds := make([]string, 0, len(names))
	for _, name := range names {
		crds = append(crds, filepath.Join(root, "config", "crd", "bases", "sys.openbaoconfig.io_"+name+".yaml"))
	}
	testEnv := &envtest.Environment{
		BinaryAssetsDirectory: assets,
		CRDInstallOptions:     envtest.CRDInstallOptions{Paths: crds, ErrorIfPathMissing: true},
	}
	cfg, err := testEnv.Start()
	if err != nil {
		t.Fatalf("envtest: %v", err)
	}
	t.Cleanup(func() {
		if err := testEnv.Stop(); err != nil {
			t.Logf("stop envtest: %v", err)
		}
	})

	scheme := runtime.NewScheme()
	for _, add := range []func(*runtime.Scheme) error{clientgoscheme.AddToScheme, sysv1alpha1.AddToScheme} {
		if err := add(scheme); err != nil {
			t.Fatal(err)
		}
	}
	skipNameValidation := true
	mgr, err := ctrl.NewManager(cfg, ctrl.Options{
		Scheme:                 scheme,
		Metrics:                metricsserver.Options{BindAddress: "0"},
		HealthProbeBindAddress: "0",
		Controller:             config.Controller{SkipNameValidation: &skipNameValidation},
	})
	if err != nil {
		t.Fatal(err)
	}
	conns := staticConnections{client: bao}
	opts := itestEngineOptions()
	for _, setupKind := range []func(ctrl.Manager, engine.Connections, engine.Options) error{
		sys.SetupOpenBaoNamespace, sys.SetupAuthMount, sys.SetupSecretsMount, sys.SetupPasswordPolicy, sys.SetupRateLimitQuota,
		sys.SetupPolicy,
	} {
		if err := setupKind(mgr, conns, opts); err != nil {
			t.Fatal(err)
		}
	}

	mgrCtx, stopMgr := context.WithCancel(ctx)
	done := make(chan error, 1)
	go func() { done <- mgr.Start(mgrCtx) }()
	t.Cleanup(func() {
		stopMgr()
		if err := <-done; err != nil {
			t.Logf("manager: %v", err)
		}
	})
	return env{k8s: mgr.GetClient(), bao: bao}
}
