package openbao

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/alexandrust88/openbao-config-operator/test/testenv"
)

// startBaoDev starts `bao server -dev` with extra command-line arguments and returns its address.
func startBaoDev(t *testing.T, args ...string) string {
	t.Helper()
	if testing.Short() {
		t.Skip("integration test")
	}
	return testenv.StartBao(t, testenv.RequireBao(t), "root", args...)
}

func TestIntegrationBaoDev(t *testing.T) {
	addr := startBaoDev(t)
	ctx := t.Context()

	info, err := Probe(ctx, Config{Address: addr})
	if err != nil {
		t.Fatalf("probe: %v", err)
	}
	if info.Flavor != FlavorOpenBao || info.Version == "" || info.Standby {
		t.Errorf("probe = %+v", info)
	}

	p := NewPool()
	root, err := p.Client(ctx, "it-root", Config{
		Address: addr,
		Auth:    TokenAuth{CredentialKey: "root", Token: static("root")},
	})
	if err != nil {
		t.Fatalf("pool client: %v", err)
	}

	t.Run("missing mount is a 400", func(t *testing.T) {
		_, err := root.Read(ctx, "sys/mounts/does-not-exist")
		var re *ResponseError
		if !errors.As(err, &re) || re.StatusCode != 400 || errors.Is(err, ErrNotFound) {
			t.Fatalf("got %v, want ResponseError 400", err)
		}
	})

	t.Run("policy crud", func(t *testing.T) {
		policyCRUD(t, root, "it-policy")
	})

	t.Run("namespace", func(t *testing.T) {
		if _, err := root.Write(ctx, "sys/namespaces/team", nil); err != nil {
			t.Fatalf("create namespace: %v", err)
		}
		team := root.WithNamespace("team")
		if team.Namespace() != "team" || root.Namespace() != "" {
			t.Fatalf("namespaces: team=%q root=%q", team.Namespace(), root.Namespace())
		}
		if _, err := team.Write(ctx, "sys/policies/acl/team-policy", map[string]any{fieldPolicy: `path "kv/*" { capabilities = ["read"] }`}); err != nil {
			t.Fatalf("write in namespace: %v", err)
		}
		rootKeys, err := root.List(ctx, "sys/policies/acl")
		if err != nil {
			t.Fatal(err)
		}
		if slices.Contains(rootKeys, "team-policy") {
			t.Error("team-policy leaked into the root namespace")
		}
		policyCRUD(t, team, "team-policy")
	})

	t.Run("approle login", func(t *testing.T) {
		approleLogin(ctx, t, p, root, addr)
	})
}

func policyCRUD(t *testing.T, c Client, name string) {
	t.Helper()
	ctx := t.Context()
	path := "sys/policies/acl/" + name
	if _, err := c.Write(ctx, path, map[string]any{fieldPolicy: `path "secret/*" { capabilities = ["read"] }`}); err != nil {
		t.Fatalf("write: %v", err)
	}
	data, err := c.Read(ctx, path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if data["name"] != name {
		t.Errorf("read name = %v, want %s", data["name"], name)
	}
	keys, err := c.List(ctx, "sys/policies/acl")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if !slices.Contains(keys, name) {
		t.Errorf("list %v does not contain %s", keys, name)
	}
	if err := c.Delete(ctx, path); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := c.Read(ctx, path); !errors.Is(err, ErrNotFound) {
		t.Errorf("read after delete: got %v, want ErrNotFound", err)
	}
}

func approleLogin(ctx context.Context, t *testing.T, p *Pool, root Client, addr string) {
	t.Helper()
	mustWrite := func(path string, data map[string]any) map[string]any {
		t.Helper()
		out, err := root.Write(ctx, path, data)
		if err != nil {
			t.Fatalf("write %s: %v", path, err)
		}
		return out
	}
	mustWrite("sys/auth/approle", map[string]any{"type": "approle"})
	mustWrite("sys/policies/acl/operator", map[string]any{fieldPolicy: `path "sys/policies/acl/*" { capabilities = ["create", "read", "update", "delete", "list"] }`})
	mustWrite("auth/approle/role/operator", map[string]any{"token_policies": "operator", "token_ttl": "1h"})
	roleData, err := root.Read(ctx, "auth/approle/role/operator/role-id")
	if err != nil {
		t.Fatal(err)
	}
	roleID, _ := roleData["role_id"].(string)
	secretID, _ := mustWrite("auth/approle/role/operator/secret-id", nil)["secret_id"].(string)

	op, err := p.Client(ctx, "it-approle", Config{
		Address: addr,
		Auth:    AppRoleAuth{CredentialKey: "operator", RoleID: static(roleID), SecretID: static(secretID)},
	})
	if err != nil {
		t.Fatalf("approle pool client: %v", err)
	}
	policyCRUD(t, op, "from-approle")
	if _, err := op.Read(ctx, "sys/auth"); err == nil {
		t.Error("approle token can read sys/auth; policy not applied")
	}
}
