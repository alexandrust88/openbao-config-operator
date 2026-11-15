package openbao

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

// CredentialFunc returns a credential such as a token, a JWT or an AppRole secret-id.
type CredentialFunc func(ctx context.Context) (string, error)

// TokenAuth uses a static token. The token is looked up to learn its TTL, so a renewable or periodic token
// is renewed like a login token.
type TokenAuth struct {
	// CredentialKey identifies the credential source, such as the Secret name, key and a digest of the value.
	CredentialKey string
	Token         CredentialFunc
}

// KubernetesAuth logs in with a ServiceAccount JWT against the kubernetes auth method.
type KubernetesAuth struct {
	// MountPath defaults to "kubernetes" and must not start with "auth/".
	MountPath     string
	Role          string
	CredentialKey string
	JWT           CredentialFunc
}

// AppRoleAuth logs in with a role-id and an optional secret-id against the approle auth method.
type AppRoleAuth struct {
	// MountPath defaults to "approle" and must not start with "auth/".
	MountPath     string
	CredentialKey string
	RoleID        CredentialFunc
	// SecretID may be nil for a role with bind_secret_id=false.
	SecretID CredentialFunc
}

// JWTAuth logs in with a JWT against the jwt auth method.
type JWTAuth struct {
	// MountPath defaults to "jwt" and must not start with "auth/".
	MountPath     string
	Role          string
	CredentialKey string
	JWT           CredentialFunc
}

// tokenLookuper is implemented by the pool's LoginClient so TokenAuth can learn the TTL of a static token.
type tokenLookuper interface {
	lookupSelf(ctx context.Context, token string) (ttl time.Duration, renewable bool, err error)
}

// Login implements Auth.
func (a TokenAuth) Login(ctx context.Context, c LoginClient) (string, time.Duration, bool, error) {
	token, err := credential(ctx, "token", a.Token)
	if err != nil {
		return "", 0, false, err
	}
	lk, ok := c.(tokenLookuper)
	if !ok {
		return token, 0, false, nil
	}
	ttl, renewable, err := lk.lookupSelf(ctx, token)
	if err != nil {
		return "", 0, false, fmt.Errorf("token lookup: %w", err)
	}
	return token, ttl, renewable, nil
}

// CacheKey implements Auth.
func (a TokenAuth) CacheKey() string {
	return cacheKey("token", a.CredentialKey)
}

// Login implements Auth.
func (a KubernetesAuth) Login(ctx context.Context, c LoginClient) (string, time.Duration, bool, error) {
	return roleJWTLogin(ctx, c, "kubernetes", a.MountPath, a.Role, a.JWT)
}

// CacheKey implements Auth.
func (a KubernetesAuth) CacheKey() string {
	return cacheKey("kubernetes", a.MountPath, a.Role, a.CredentialKey)
}

// Login implements Auth.
func (a JWTAuth) Login(ctx context.Context, c LoginClient) (string, time.Duration, bool, error) {
	return roleJWTLogin(ctx, c, "jwt", a.MountPath, a.Role, a.JWT)
}

// CacheKey implements Auth.
func (a JWTAuth) CacheKey() string {
	return cacheKey("jwt", a.MountPath, a.Role, a.CredentialKey)
}

// Login implements Auth.
func (a AppRoleAuth) Login(ctx context.Context, c LoginClient) (string, time.Duration, bool, error) {
	path, err := loginPath(a.MountPath, "approle")
	if err != nil {
		return "", 0, false, err
	}
	roleID, err := credential(ctx, "role-id", a.RoleID)
	if err != nil {
		return "", 0, false, err
	}
	data := map[string]any{"role_id": roleID}
	if a.SecretID != nil {
		secretID, err := credential(ctx, "secret-id", a.SecretID)
		if err != nil {
			return "", 0, false, err
		}
		data["secret_id"] = secretID
	}
	resp, err := c.Write(ctx, path, data)
	if err != nil {
		return "", 0, false, fmt.Errorf("approle login: %w", err)
	}
	return parseLogin(resp)
}

// CacheKey implements Auth.
func (a AppRoleAuth) CacheKey() string {
	return cacheKey("approle", a.MountPath, a.CredentialKey)
}

func roleJWTLogin(ctx context.Context, c LoginClient, method, mount, role string, jwt CredentialFunc) (string, time.Duration, bool, error) {
	path, err := loginPath(mount, method)
	if err != nil {
		return "", 0, false, err
	}
	if role == "" {
		return "", 0, false, fmt.Errorf("%s login: role is required", method)
	}
	token, err := credential(ctx, "jwt", jwt)
	if err != nil {
		return "", 0, false, err
	}
	resp, err := c.Write(ctx, path, map[string]any{"role": role, "jwt": token})
	if err != nil {
		return "", 0, false, fmt.Errorf("%s login: %w", method, err)
	}
	return parseLogin(resp)
}

func loginPath(mount, defaultMount string) (string, error) {
	m := strings.Trim(mount, "/")
	if m == "" {
		m = defaultMount
	}
	if m == "auth" || strings.HasPrefix(m, "auth/") {
		return "", fmt.Errorf("mount path %q must not start with auth/", mount)
	}
	if err := ValidatePath(m); err != nil {
		return "", fmt.Errorf("mount path: %w", err)
	}
	return "auth/" + m + "/login", nil
}

func credential(ctx context.Context, name string, fn CredentialFunc) (string, error) {
	if fn == nil {
		return "", fmt.Errorf("%s source is not configured", name)
	}
	v, err := fn(ctx)
	if err != nil {
		return "", fmt.Errorf("get %s: %w", name, err)
	}
	if v == "" {
		return "", fmt.Errorf("%s is empty", name)
	}
	return v, nil
}

func cacheKey(parts ...string) string {
	return strings.Join(parts, "\x00")
}

// parseLogin reads the auth block that the pool's LoginClient returns for a login response.
func parseLogin(resp map[string]any) (string, time.Duration, bool, error) {
	token, _ := resp["client_token"].(string)
	if token == "" {
		return "", 0, false, errors.New("login response has no client token")
	}
	renewable, _ := resp["renewable"].(bool)
	var seconds int64
	switch v := resp["lease_duration"].(type) {
	case int:
		seconds = int64(v)
	case int64:
		seconds = v
	case float64:
		seconds = int64(v)
	case json.Number:
		seconds, _ = v.Int64()
	}
	return token, time.Duration(seconds) * time.Second, renewable, nil
}
