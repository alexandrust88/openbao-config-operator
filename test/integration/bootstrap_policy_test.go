//go:build integration

package integration

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/alexandrust88/openbao-config-operator/internal/openbao"
	"github.com/alexandrust88/openbao-config-operator/test/testenv"
	baoapi "github.com/openbao/openbao/api/v2"
)

// TestIntegrationBootstrapPolicy verifies the shipped policy with a restricted token against real OpenBao.
func TestIntegrationBootstrapPolicy(t *testing.T) {
	ctx := context.Background()
	addr := startBao(t, testenv.RequireBao(t))
	admin, err := baoapi.NewClient(&baoapi.Config{Address: addr})
	if err != nil {
		t.Fatal(err)
	}
	admin.SetToken(rootToken)
	raw, err := os.ReadFile(filepath.Join(repoRoot(t), "examples/self-init/openbao.hcl"))
	if err != nil {
		t.Fatal(err)
	}
	_, body, ok := strings.Cut(string(raw), "policy = <<-EOF\n")
	if !ok {
		t.Fatal("self-init policy heredoc missing")
	}
	policy, _, ok := strings.Cut(body, "\n      EOF")
	if !ok {
		t.Fatal("self-init policy heredoc is not terminated")
	}
	for path, data := range map[string]map[string]any{
		"sys/auth/kubernetes":                      {"type": "kubernetes"},
		"sys/policies/acl/openbao-config-operator": {"policy": policy},
	} {
		if _, err := admin.Logical().WriteWithContext(ctx, path, data); err != nil {
			t.Fatal(err)
		}
	}
	role := map[string]any{"bound_service_account_names": []string{"operator"}, "bound_service_account_namespaces": []string{"operator"}, "token_policies": []string{"openbao-config-operator"}}
	if _, err := admin.Logical().WriteWithContext(ctx, "auth/kubernetes/role/openbao-config-operator", role); err != nil {
		t.Fatal(err)
	}
	token, err := admin.Auth().Token().CreateWithContext(ctx, &baoapi.TokenCreateRequest{Policies: []string{"openbao-config-operator"}, TTL: "5m"})
	if err != nil {
		t.Fatal(err)
	}
	if token == nil || token.Auth == nil {
		t.Fatal("missing test token")
	}
	limited, err := openbao.NewPool().Client(ctx, "bootstrap-review", openbao.Config{
		Address: addr, Timeout: 5 * time.Second,
		Auth: openbao.TokenAuth{CredentialKey: "bootstrap-review", Token: func(context.Context) (string, error) { return token.Auth.ClientToken, nil }},
	})
	if err != nil {
		t.Fatal(err)
	}
	for path, data := range map[string]map[string]any{
		"sys/policies/acl/openbao-config-operator":     {"policy": policy},
		"sys/auth/kubernetes":                          {"type": "kubernetes"},
		"sys/auth/kubernetes/tune":                     {"description": "must not change"},
		"auth/kubernetes/config":                       {"kubernetes_host": "https://kubernetes.default.svc"},
		"auth/kubernetes/role/openbao-config-operator": role,
	} {
		t.Run(path, func(t *testing.T) {
			_, err := limited.Write(ctx, path, data)
			requireBootstrapDenied(t, err)
			requireBootstrapDenied(t, limited.Delete(ctx, path))
		})
	}
	// A working canary distinguishes ACL denial from an expired or otherwise unusable token.
	if _, err := limited.Write(ctx, "sys/policies/acl/bootstrap-canary", map[string]any{"policy": `path "secret/data/canary" { capabilities = ["read"] }`}); err != nil {
		t.Fatal(err)
	}
	if err := limited.Delete(ctx, "sys/policies/acl/bootstrap-canary"); err != nil {
		t.Fatal(err)
	}
	stored, err := admin.Logical().ReadWithContext(ctx, "sys/policies/acl/openbao-config-operator")
	if err != nil {
		t.Fatal(err)
	}
	if stored == nil || stored.Data["policy"] != policy {
		t.Fatal("protected policy changed")
	}
	if _, err := admin.Logical().ReadWithContext(ctx, "auth/kubernetes/role/openbao-config-operator"); err != nil {
		t.Fatal(err)
	}
}

func requireBootstrapDenied(t *testing.T, err error) {
	t.Helper()
	var response *openbao.ResponseError
	if errors.Is(err, openbao.ErrPermissionDenied) {
		return
	}
	if errors.As(err, &response) && response.StatusCode == 403 {
		return
	}
	t.Fatalf("expected policy denial, got %v", err)
}
