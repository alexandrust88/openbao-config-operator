package examples

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// sysCELKinds are the sys CRDs, installed together so that a CRD the apiserver rejects (CEL cost) fails here.
var sysCELKinds = []string{
	"audits", "auditrequestheaders", "authmounts", "corsconfigs", "keyrotationconfigs", "loggers", "openbaonamespaces",
	"passwordpolicies", "plugins", "policies", "quotaconfigs", "raftautopilots", "ratelimitquotas", "secretsmounts",
	"uiheaders", "workflows",
}

const sysCELSHA = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

// TestSysCELRules asserts the message of every CEL rule of the sys kinds: create rules with an invalid object,
// transition rules with an update of a valid one.
func TestSysCELRules(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	crds := make([]string, 0, len(sysCELKinds))
	for _, kind := range sysCELKinds {
		crds = append(crds, "sys.openbaoconfig.io_"+kind+".yaml")
	}
	k8s := secretsStartEnv(t, root, crds)
	ctx := context.Background()
	if err := k8s.Create(ctx, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "openbao-config"}}); err != nil {
		t.Fatal(err)
	}

	for _, tt := range []struct{ kind, spec, wantErr string }{
		{"Audit", "{type: file, namespace: team, options: {file_path: stdout}}", "Audit exists only in the root namespace"},
		{"Audit", "{type: file, options: {file_path: stdout}, optionsSecretRefs: {file_path: {name: s, key: k}}}",
			"an option is set in both options and optionsSecretRefs"},
		{"AuditRequestHeader", "{namespace: team}", "AuditRequestHeader exists only in the root namespace"},
		{"AuthMount", "{type: kubernetes, userLockout: {threshold: 3}}", "userLockout is supported only for"},
		{"AuthMount", "{type: userpass, defaultLeaseTTL: 2h, maxLeaseTTL: 1h}", "defaultLeaseTTL must not exceed maxLeaseTTL"},
		{"CORSConfig", "{namespace: team, allowedOrigins: [https://a.example]}", "CORSConfig exists only in the root namespace"},
		{"CORSConfig", "{allowedOrigins: ['*', https://a.example]}", "'*' must be the only entry of allowedOrigins"},
		{"KeyRotationConfig", "{interval: 1h}", "interval must be 0 (disabled) or at least 24h"},
		{"Logger", "{namespace: team, level: info}", "Logger exists only in the root namespace"},
		{"OpenBaoNamespace", "{name: sys}", "the namespace name is reserved in OpenBao"},
		{"OpenBaoNamespace", "{customMetadata: {openbaoconfig-uid: x}}", "without the reserved prefix openbaoconfig-"},
		{"Plugin", "{namespace: team, type: auth, command: p, sha256: " + sysCELSHA + "}", "Plugin exists only in the root namespace"},
		{"Plugin", "{type: auth, command: p, version: v1.0.0, deletionPolicy: Delete, sha256: " + sysCELSHA + "}",
			"a versioned plugin cannot be deregistered by the operator"},
		{"Plugin", "{type: auth, command: a..b, sha256: " + sysCELSHA + "}", "command must not contain '..'"},
		{"Policy", "{name: root, policy: x}", "the root and response-wrapping policies cannot be changed"},
		{"Policy", "{name: default, deletionPolicy: Delete, policy: x}", "the default policy cannot be deleted"},
		{"Policy", "{policy: x, accessorRefs: {Bad: {mountPath: k}}}", "accessorRefs names are 1-63 characters"},
		{"Policy", "{policy: x, accessorRefs: {k: {mountPath: k, authMountRef: {name: k}}}}", "exactly one of the fields in [authMountRef mountPath]"},
		{"QuotaConfig", "{namespace: team}", "QuotaConfig exists only in the root namespace"},
		{"RaftAutopilot", "{namespace: team}", "RaftAutopilot exists only in the root namespace"},
		{"RaftAutopilot", "{cleanupDeadServers: true, minQuorum: 2}", "cleanupDeadServers requires minQuorum of at least 3"},
		{"RaftAutopilot", "{lastContactThreshold: 500ms}", "lastContactThreshold must be at least 1s"},
		{"RaftAutopilot", "{deadServerLastContactThreshold: 30s}", "deadServerLastContactThreshold must be at least 1m"},
		{"RaftAutopilot", "{serverStabilizationTime: 500ms}", "serverStabilizationTime must be at least 1s"},
		{"RateLimitQuota", "{namespace: team, rate: '1'}", "rate limit quotas exist only in the root namespace"},
		{"RateLimitQuota", "{role: r, rate: '1'}", "role requires path to name an auth method mount"},
		{"RateLimitQuota", "{rate: '0'}", "rate must be greater than 0"},
		{"RateLimitQuota", "{rate: '1', interval: 500ms}", "interval must be at least 1s"},
		{"SecretsMount", "{type: kv-v2, options: {version: '2'}}", "set the KV version through type kv-v1/kv-v2 or options.version"},
		{"UIHeader", "{namespace: team, values: [x]}", "UIHeader exists only in the root namespace"},
		{"UIHeader", "{name: X-Vault-Token, values: [x]}", "X-Vault-* headers cannot be set"},
	} {
		t.Run("create "+tt.kind+" "+tt.wantErr, func(t *testing.T) {
			err := k8s.Create(ctx, sysCELObject(t, tt.kind, "invalid", tt.spec), client.DryRunAll)
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("err = %v, want it to contain %q", err, tt.wantErr)
			}
		})
	}

	for i, tt := range []struct {
		kind, spec, field string
		value             any
		wantErr           string
	}{
		{"Audit", "{type: file, path: a, options: {file_path: stdout}}", "path", "b", "path is immutable"},
		{"Audit", "{type: file, options: {file_path: stdout}}", "type", "syslog", "type is immutable"},
		{"Audit", "{type: file, description: a, options: {file_path: stdout}}", "description", "b", "description is immutable"},
		{"Audit", "{type: file, local: false, options: {file_path: stdout}}", "local", true, "local is immutable"},
		{"Audit", "{type: file, options: {file_path: stdout}}", "options", map[string]any{"file_path": "x"}, "options is immutable"},
		{"Audit", "{type: file, options: {file_path: stdout}}", "optionsSecretRefs",
			map[string]any{"hmac_key": map[string]any{"name": "s", "key": "k"}}, "optionsSecretRefs is immutable"},
		{"AuditRequestHeader", "{name: X-A}", "name", "X-B", "name is immutable"},
		{"AuthMount", "{type: userpass, path: a}", "path", "b", "path is immutable"},
		{"AuthMount", "{type: userpass}", "type", "approle", "type is immutable"},
		{"AuthMount", "{type: userpass, local: false}", "local", true, "local is immutable"},
		{"AuthMount", "{type: userpass}", "sealWrap", true, "sealWrap is immutable"},
		{"AuthMount", "{type: userpass}", "externalEntropyAccess", true, "externalEntropyAccess is immutable"},
		{"AuthMount", "{type: userpass}", "forceNoCache", true, "forceNoCache is immutable"},
		{"AuthMount", "{type: userpass}", "pluginRef", map[string]any{"name": "p"}, "pluginRef is immutable"},
		{"Logger", "{name: core, level: info}", "name", "expiration", "name is immutable"},
		{"OpenBaoNamespace", "{name: a}", "name", "b", "name is immutable"},
		{"PasswordPolicy", "{name: a, policy: x}", "name", "b", "name is immutable"},
		{"Plugin", "{type: auth, command: p, sha256: " + sysCELSHA + "}", "type", "secret", "type is immutable"},
		{"Plugin", "{type: auth, name: a, command: p, sha256: " + sysCELSHA + "}", "name", "b", "name is immutable"},
		{"Plugin", "{type: auth, version: v1.0.0, command: p, sha256: " + sysCELSHA + "}", "version", "v1.0.1",
			"version is immutable"},
		{"Policy", "{name: a, policy: x}", "name", "b", "name is immutable"},
		{"RateLimitQuota", "{name: a, rate: '1'}", "name", "b", "name is immutable"},
		{"RateLimitQuota", "{path: up, role: a, rate: '1'}", "role", "b", "role is immutable"},
		{"SecretsMount", "{type: kv, path: a}", "path", "b", "path is immutable"},
		{"SecretsMount", "{type: kv}", "type", "pki", "type is immutable"},
		{"SecretsMount", "{type: kv, options: {version: '2'}}", "options", map[string]any{"version": "1"},
			"a KV version 2 mount cannot be downgraded to version 1"},
		{"UIHeader", "{name: X-A, values: [x]}", "name", "X-B", "name is immutable"},
		{"Workflow", "{path: a, workflow: x}", "path", "b", "path is immutable"},
	} {
		t.Run(fmt.Sprintf("update %s %s", tt.kind, tt.wantErr), func(t *testing.T) {
			obj := sysCELObject(t, tt.kind, fmt.Sprintf("immutable-%d", i), tt.spec)
			if err := k8s.Create(ctx, obj); err != nil {
				t.Fatalf("create: %v", err)
			}
			if err := unstructured.SetNestedField(obj.Object, tt.value, "spec", tt.field); err != nil {
				t.Fatal(err)
			}
			if err := k8s.Update(ctx, obj, client.DryRunAll); err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("update of spec.%s: err = %v, want it to contain %q", tt.field, err, tt.wantErr)
			}
		})
	}
}

func sysCELObject(t *testing.T, kind, name, spec string) *unstructured.Unstructured {
	t.Helper()
	spec = strings.TrimSuffix(strings.TrimPrefix(spec, "{"), "}")
	return secretsDecodeString(t, fmt.Sprintf("apiVersion: sys.openbaoconfig.io/v1alpha1\nkind: %s\n"+
		"metadata: {name: %s, namespace: openbao-config}\nspec: {connectionRef: {name: openbao}, %s}", kind, name, spec))
}
