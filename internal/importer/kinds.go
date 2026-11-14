package importer

import (
	"context"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"maps"
	"reflect"
	"slices"
	"strings"

	authv1alpha1 "github.com/alexandrust88/openbao-config-operator/api/auth/v1alpha1"
	commonv1alpha1 "github.com/alexandrust88/openbao-config-operator/api/common/v1alpha1"
	identityv1alpha1 "github.com/alexandrust88/openbao-config-operator/api/identity/v1alpha1"
	secretsv1alpha1 "github.com/alexandrust88/openbao-config-operator/api/secrets/v1alpha1"
	sysv1alpha1 "github.com/alexandrust88/openbao-config-operator/api/sys/v1alpha1"
	authcontroller "github.com/alexandrust88/openbao-config-operator/internal/controller/auth"
	identitycontroller "github.com/alexandrust88/openbao-config-operator/internal/controller/identity"
	secretscontroller "github.com/alexandrust88/openbao-config-operator/internal/controller/secrets"
	syscontroller "github.com/alexandrust88/openbao-config-operator/internal/controller/sys"
	"github.com/alexandrust88/openbao-config-operator/internal/engine"
	"github.com/alexandrust88/openbao-config-operator/internal/openbao"
)

// sensitiveAuditOptions are audit device options that can carry credentials, such as the headers of an http
// device; they are imported as Secret references, never as values.
var sensitiveAuditOptions = []string{"headers"}

// Mounts the server creates itself; they have no typed kind to adopt.
var (
	systemAuthMounts    = []string{"token"}
	systemSecretsMounts = []string{"sys", "identity", "cubbyhole"}
)

// Spec fields that identify an object, set from the listing and never from the read.
const (
	fieldMountPath = "mountPath"
	fieldName      = "name"
	fieldPath      = "path"
	fieldType      = "type"
)

var mountIdentity = []string{fieldMountPath, fieldName}

// kinds lists every importable kind in emission order. base is the client of the imported connection, which
// adapters use to resolve OpenBao names.
func kinds(base openbao.Client) []runner {
	conns := staticConns{c: base}
	return []runner{
		kind[*sysv1alpha1.OpenBaoNamespace]{
			adapter: syscontroller.OpenBaoNamespaceAdapter{}, newObj: newObj[sysv1alpha1.OpenBaoNamespace],
			identity: []string{fieldName},
			list: names("sys/namespaces", nil, func(n string) *sysv1alpha1.OpenBaoNamespace {
				o := &sysv1alpha1.OpenBaoNamespace{}
				o.Spec.Name = n
				return o
			}),
		},
		kind[*sysv1alpha1.Policy]{
			adapter: syscontroller.PolicyAdapter{}, newObj: newObj[sysv1alpha1.Policy], identity: []string{fieldName},
			list: names("sys/policies/acl", []string{"root"}, func(n string) *sysv1alpha1.Policy {
				o := &sysv1alpha1.Policy{}
				o.Spec.Name = n
				return o
			}),
		},
		kind[*sysv1alpha1.PasswordPolicy]{
			adapter: syscontroller.PasswordPolicyAdapter{}, newObj: newObj[sysv1alpha1.PasswordPolicy],
			identity: []string{fieldName},
			list: names("sys/policies/password", nil, func(n string) *sysv1alpha1.PasswordPolicy {
				o := &sysv1alpha1.PasswordPolicy{}
				o.Spec.Name = n
				return o
			}),
		},
		kind[*sysv1alpha1.AuthMount]{
			adapter: syscontroller.AuthMountAdapter{}, newObj: newObj[sysv1alpha1.AuthMount],
			identity: []string{fieldPath, fieldType},
			list: mounts(func(s *scope) map[string]mount { return s.auth }, systemAuthMounts,
				func(path string, m mount) *sysv1alpha1.AuthMount {
					o := &sysv1alpha1.AuthMount{}
					o.Spec.Path, o.Spec.Type = path, m.typ
					return o
				}),
		},
		kind[*sysv1alpha1.SecretsMount]{
			adapter: syscontroller.SecretsMountAdapter{}, newObj: newObj[sysv1alpha1.SecretsMount],
			identity: []string{fieldPath, fieldType},
			list: mounts(func(s *scope) map[string]mount { return s.secrets }, systemSecretsMounts,
				func(path string, m mount) *sysv1alpha1.SecretsMount {
					o := &sysv1alpha1.SecretsMount{}
					o.Spec.Path, o.Spec.Type = path, m.typ
					// The observe hides a KV version the spec does not name; an import must keep it.
					if v, ok := m.options["version"].(string); ok {
						o.Spec.Options = map[string]sysv1alpha1.MountOption{"version": sysv1alpha1.MountOption(v)}
					}
					return o
				}),
		},
		kind[*sysv1alpha1.Audit]{
			adapter: syscontroller.AuditAdapter{}, newObj: newObj[sysv1alpha1.Audit],
			identity: []string{fieldPath, fieldType, "optionsSecretRefs"}, rootOnly: true,
			fill: fillAudit,
			list: auditDevices,
		},
		kind[*sysv1alpha1.RateLimitQuota]{
			adapter: syscontroller.RateLimitQuotaAdapter{}, newObj: newObj[sysv1alpha1.RateLimitQuota],
			identity: []string{fieldName}, rootOnly: true,
			list: names("sys/quotas/rate-limit", nil, func(n string) *sysv1alpha1.RateLimitQuota {
				o := &sysv1alpha1.RateLimitQuota{}
				o.Spec.Name = n
				return o
			}),
		},
		kind[*authv1alpha1.KubernetesAuthConfig]{
			adapter: authcontroller.KubernetesAuthConfigAdapter{}, newObj: newObj[authv1alpha1.KubernetesAuthConfig],
			identity: []string{fieldMountPath},
			list: authConfigs([]string{"kubernetes"}, func(m string) *authv1alpha1.KubernetesAuthConfig {
				o := &authv1alpha1.KubernetesAuthConfig{}
				o.Spec.MountPath = m
				return o
			}),
		},
		kind[*authv1alpha1.KubernetesAuthRole]{
			adapter: authcontroller.KubernetesAuthRoleAdapter{}, newObj: newObj[authv1alpha1.KubernetesAuthRole],
			identity: mountIdentity,
			list: authItems([]string{"kubernetes"}, "role", func(m, n string) *authv1alpha1.KubernetesAuthRole {
				o := &authv1alpha1.KubernetesAuthRole{}
				o.Spec.MountPath, o.Spec.Name = m, n
				return o
			}),
		},
		kind[*authv1alpha1.JWTAuthConfig]{
			adapter: authcontroller.JWTAuthConfigAdapter{}, newObj: newObj[authv1alpha1.JWTAuthConfig],
			identity: []string{fieldMountPath},
			secrets: func(observed engine.Payload) []string {
				if id, _ := observed["oidc_client_id"].(string); id != "" {
					return []string{"oidc_client_secret"}
				}
				return nil
			},
			list: authConfigs([]string{"jwt", "oidc"}, func(m string) *authv1alpha1.JWTAuthConfig {
				o := &authv1alpha1.JWTAuthConfig{}
				o.Spec.MountPath = m
				return o
			}),
		},
		kind[*authv1alpha1.JWTAuthRole]{
			adapter: authcontroller.JWTAuthRoleAdapter{}, newObj: newObj[authv1alpha1.JWTAuthRole],
			identity: mountIdentity,
			list: authItems([]string{"jwt", "oidc"}, "role", func(m, n string) *authv1alpha1.JWTAuthRole {
				o := &authv1alpha1.JWTAuthRole{}
				o.Spec.MountPath, o.Spec.Name = m, n
				return o
			}),
		},
		kind[*authv1alpha1.AppRole]{
			adapter: authcontroller.AppRoleAdapter{}, newObj: newObj[authv1alpha1.AppRole], identity: mountIdentity,
			list: authItems([]string{"approle"}, "role", func(m, n string) *authv1alpha1.AppRole {
				o := &authv1alpha1.AppRole{}
				o.Spec.MountPath, o.Spec.Name = m, n
				return o
			}),
		},
		kind[*authv1alpha1.UserpassUser]{
			adapter: authcontroller.UserpassUserAdapter{}, newObj: newObj[authv1alpha1.UserpassUser],
			identity: mountIdentity,
			secrets:  func(engine.Payload) []string { return []string{"password"} },
			list: authItems([]string{"userpass"}, "users", func(m, n string) *authv1alpha1.UserpassUser {
				o := &authv1alpha1.UserpassUser{}
				o.Spec.MountPath, o.Spec.Name = m, n
				return o
			}),
		},
		kind[*authv1alpha1.CertAuthConfig]{
			adapter: authcontroller.CertAuthConfigAdapter{}, newObj: newObj[authv1alpha1.CertAuthConfig],
			identity: []string{fieldMountPath},
			list: authConfigs([]string{"cert"}, func(m string) *authv1alpha1.CertAuthConfig {
				o := &authv1alpha1.CertAuthConfig{}
				o.Spec.MountPath = m
				return o
			}),
		},
		kind[*authv1alpha1.CertAuthRole]{
			adapter: authcontroller.CertAuthRoleAdapter{}, newObj: newObj[authv1alpha1.CertAuthRole],
			identity: mountIdentity,
			list: authItems([]string{"cert"}, "certs", func(m, n string) *authv1alpha1.CertAuthRole {
				o := &authv1alpha1.CertAuthRole{}
				o.Spec.MountPath, o.Spec.Name = m, n
				return o
			}),
		},
		kind[*authv1alpha1.TokenRole]{
			adapter: authcontroller.TokenRoleAdapter{}, newObj: newObj[authv1alpha1.TokenRole],
			identity: []string{fieldName},
			fill: func(_ context.Context, _ *scope, o *authv1alpha1.TokenRole, _ engine.Payload) error {
				// The token store reports its default token type as default-service, which is what unset sends.
				if o.Spec.TokenType == "default-service" {
					o.Spec.TokenType = ""
				}
				return nil
			},
			list: names("auth/token/roles", nil, func(n string) *authv1alpha1.TokenRole {
				o := &authv1alpha1.TokenRole{}
				o.Spec.Name = n
				return o
			}),
		},
		kind[*identityv1alpha1.Entity]{
			adapter: identitycontroller.EntityAdapter{}, newObj: newObj[identityv1alpha1.Entity],
			identity: []string{fieldName},
			list: names("identity/entity/name", nil, func(n string) *identityv1alpha1.Entity {
				o := &identityv1alpha1.Entity{}
				o.Spec.Name = n
				return o
			}),
		},
		kind[*identityv1alpha1.EntityAlias]{
			adapter: identitycontroller.EntityAliasAdapter{Conns: conns}, newObj: newObj[identityv1alpha1.EntityAlias],
			identity: []string{"aliasName", "mountAccessor", "mountPath", "entityID", "entityName"},
			list:     entityAliases,
		},
		kind[*identityv1alpha1.Group]{
			adapter: identitycontroller.GroupAdapter{}, newObj: newObj[identityv1alpha1.Group],
			identity: []string{fieldName},
			list: names("identity/group/name", nil, func(n string) *identityv1alpha1.Group {
				o := &identityv1alpha1.Group{}
				o.Spec.Name = n
				return o
			}),
		},
		kind[*identityv1alpha1.GroupAlias]{
			adapter: identitycontroller.GroupAliasAdapter{Conns: conns}, newObj: newObj[identityv1alpha1.GroupAlias],
			identity: []string{"aliasName", "mountAccessor", "mountPath", "groupID", "groupName"},
			list:     groupAliases,
		},
		kind[*identityv1alpha1.OIDCKey]{
			adapter: identitycontroller.OIDCKeyAdapter{}, newObj: newObj[identityv1alpha1.OIDCKey],
			identity: []string{fieldName},
			list: names("identity/oidc/key", nil, func(n string) *identityv1alpha1.OIDCKey {
				o := &identityv1alpha1.OIDCKey{}
				o.Spec.Name = n
				return o
			}),
		},
		kind[*identityv1alpha1.OIDCScope]{
			adapter: identitycontroller.OIDCScopeAdapter{}, newObj: newObj[identityv1alpha1.OIDCScope],
			identity: []string{fieldName},
			list: names("identity/oidc/scope", nil, func(n string) *identityv1alpha1.OIDCScope {
				o := &identityv1alpha1.OIDCScope{}
				o.Spec.Name = n
				return o
			}),
		},
		kind[*identityv1alpha1.OIDCClient]{
			adapter: identitycontroller.OIDCClientAdapter{}, newObj: newObj[identityv1alpha1.OIDCClient],
			identity: []string{fieldName},
			list: names("identity/oidc/client", nil, func(n string) *identityv1alpha1.OIDCClient {
				o := &identityv1alpha1.OIDCClient{}
				o.Spec.Name = n
				return o
			}),
		},
		kind[*identityv1alpha1.OIDCAssignment]{
			adapter: identitycontroller.OIDCAssignmentAdapter{Conns: conns}, newObj: newObj[identityv1alpha1.OIDCAssignment],
			identity: []string{fieldName},
			fill:     fillOIDCAssignment,
			list: names("identity/oidc/assignment", []string{"allow_all"}, func(n string) *identityv1alpha1.OIDCAssignment {
				o := &identityv1alpha1.OIDCAssignment{}
				o.Spec.Name = n
				return o
			}),
		},
		kind[*identityv1alpha1.OIDCProvider]{
			adapter: identitycontroller.OIDCProviderAdapter{Conns: conns}, newObj: newObj[identityv1alpha1.OIDCProvider],
			identity: []string{fieldName},
			fill:     fillOIDCProvider,
			list: names("identity/oidc/provider", nil, func(n string) *identityv1alpha1.OIDCProvider {
				o := &identityv1alpha1.OIDCProvider{}
				o.Spec.Name = n
				return o
			}),
		},
		kind[*secretsv1alpha1.PKIIssuer]{
			adapter: secretscontroller.PKIIssuerAdapter{}, newObj: newObj[secretsv1alpha1.PKIIssuer],
			identity: []string{fieldMountPath, fieldName, fieldType, "key", "certificate", "signer"},
			list:     pkiIssuers,
		},
		kind[*secretsv1alpha1.PKIConfig]{
			adapter: secretscontroller.PKIConfigAdapter{}, newObj: newObj[secretsv1alpha1.PKIConfig],
			identity: []string{fieldMountPath, "issuers"},
			fill:     fillPKIConfig,
			list: secretsConfigs([]string{"pki"}, func(m string) *secretsv1alpha1.PKIConfig {
				o := &secretsv1alpha1.PKIConfig{}
				o.Spec.MountPath = m
				o.Spec.URLs = &secretsv1alpha1.PKIURLsConfig{}
				o.Spec.CRL = &secretsv1alpha1.PKICRLConfig{}
				o.Spec.Cluster = &secretsv1alpha1.PKIClusterConfig{}
				o.Spec.Issuers = &secretsv1alpha1.PKIIssuersConfig{}
				return o
			}),
		},
		kind[*secretsv1alpha1.PKIRole]{
			adapter: secretscontroller.PKIRoleAdapter{}, newObj: newObj[secretsv1alpha1.PKIRole],
			identity: mountIdentity,
			fill: func(_ context.Context, _ *scope, o *secretsv1alpha1.PKIRole, observed engine.Payload) error {
				if ref, _ := observed["issuer_ref"].(string); ref != "" && ref != "default" {
					o.Spec.Issuer = &secretsv1alpha1.PKIIssuerReference{IssuerName: ref}
				}
				return nil
			},
			list: secretsItems([]string{"pki"}, "roles", func(m, n string) *secretsv1alpha1.PKIRole {
				o := &secretsv1alpha1.PKIRole{}
				o.Spec.MountPath, o.Spec.Name = m, n
				return o
			}),
		},
		kind[*secretsv1alpha1.TransitKey]{
			adapter: secretscontroller.TransitKeyAdapter{}, newObj: newObj[secretsv1alpha1.TransitKey],
			identity: append(slices.Clone(mountIdentity), "rotateNonce"), prefill: true,
			list: secretsItems([]string{"transit"}, "keys", func(m, n string) *secretsv1alpha1.TransitKey {
				o := &secretsv1alpha1.TransitKey{}
				o.Spec.MountPath, o.Spec.Name = m, n
				return o
			}),
		},
		kind[*secretsv1alpha1.SSHRole]{
			adapter: secretscontroller.SSHRoleAdapter{}, newObj: newObj[secretsv1alpha1.SSHRole],
			identity: mountIdentity,
			list: secretsItems([]string{"ssh"}, "roles", func(m, n string) *secretsv1alpha1.SSHRole {
				o := &secretsv1alpha1.SSHRole{}
				o.Spec.MountPath, o.Spec.Name = m, n
				return o
			}),
		},
		kind[*secretsv1alpha1.DatabaseConnection]{
			adapter: secretscontroller.DatabaseConnectionAdapter{}, newObj: newObj[secretsv1alpha1.DatabaseConnection],
			identity: append(slices.Clone(mountIdentity), "postgresql", "mysql", "cassandra", "influxdb", "valkey",
				"pluginConfig", "rotateRootOnCreate"),
			fill: fillDatabaseConnection,
			secrets: func(observed engine.Payload) []string {
				if user, _ := observed["username"].(string); user != "" {
					return []string{"password"}
				}
				return nil
			},
			list: secretsItems([]string{"database"}, "config", func(m, n string) *secretsv1alpha1.DatabaseConnection {
				o := &secretsv1alpha1.DatabaseConnection{}
				o.Spec.MountPath, o.Spec.Name = m, n
				return o
			}),
		},
		kind[*secretsv1alpha1.DatabaseRole]{
			adapter: secretscontroller.DatabaseRoleAdapter{}, newObj: newObj[secretsv1alpha1.DatabaseRole],
			identity: mountIdentity,
			list: secretsItems([]string{"database"}, "roles", func(m, n string) *secretsv1alpha1.DatabaseRole {
				o := &secretsv1alpha1.DatabaseRole{}
				o.Spec.MountPath, o.Spec.Name = m, n
				return o
			}),
		},
		kind[*secretsv1alpha1.KVSecret]{
			adapter: secretscontroller.KVSecretAdapter{}, newObj: newObj[secretsv1alpha1.KVSecret],
			noDrift: true,
			fill: func(_ context.Context, s *scope, o *secretsv1alpha1.KVSecret, _ engine.Payload) error {
				o.Spec.DataFrom = []secretsv1alpha1.KVSecretDataFrom{{
					SecretRef: commonv1alpha1.LocalObjectReference{Name: o.Name},
				}}
				s.result.Placeholders = append(s.result.Placeholders,
					Placeholder{Object: s.label(o), Field: "spec.dataFrom[0].secretRef", Secret: o.Name})
				return nil
			},
			list: kvPaths,
		},
	}
}

func newObj[T any, P interface {
	*T
	engine.Object
}]() P {
	return P(new(T))
}

// names lists path and builds one object per key, without a trailing "/"; keys in skip are left out.
func names[T engine.Object](path string, skip []string, build func(name string) T) func(context.Context, *scope) ([]listed[T], error) {
	return func(ctx context.Context, s *scope) ([]listed[T], error) {
		keys, err := s.keys(ctx, path)
		if err != nil {
			return nil, err
		}
		var out []listed[T]
		for _, key := range keys {
			key = strings.TrimSuffix(key, "/")
			if !slices.Contains(skip, key) {
				out = append(out, listed[T]{obj: build(key), parts: []string{key}})
			}
		}
		return out, nil
	}
}

// mounts builds one object per entry of a mount table, leaving out the mounts the server owns.
func mounts[T engine.Object](table func(*scope) map[string]mount, skip []string,
	build func(path string, m mount) T) func(context.Context, *scope) ([]listed[T], error) {
	return func(_ context.Context, s *scope) ([]listed[T], error) {
		entries := table(s)
		paths := make([]string, 0, len(entries))
		for path := range entries {
			if !slices.Contains(skip, path) {
				paths = append(paths, path)
			}
		}
		slices.Sort(paths)
		out := make([]listed[T], 0, len(paths))
		for _, path := range paths {
			out = append(out, listed[T]{obj: build(path, entries[path]), parts: []string{path}})
		}
		return out, nil
	}
}

// authConfigs builds one object per auth mount of the given types.
func authConfigs[T engine.Object](types []string, build func(mount string) T) func(context.Context, *scope) ([]listed[T], error) {
	return func(_ context.Context, s *scope) ([]listed[T], error) {
		paths := mountsOf(s.auth, types...)
		out := make([]listed[T], 0, len(paths))
		for _, m := range paths {
			out = append(out, listed[T]{obj: build(m), parts: []string{m}})
		}
		return out, nil
	}
}

// secretsConfigs builds one object per secrets mount of the given types.
func secretsConfigs[T engine.Object](types []string, build func(mount string) T) func(context.Context, *scope) ([]listed[T], error) {
	return func(_ context.Context, s *scope) ([]listed[T], error) {
		paths := mountsOf(s.secrets, types...)
		out := make([]listed[T], 0, len(paths))
		for _, m := range paths {
			out = append(out, listed[T]{obj: build(m), parts: []string{m}})
		}
		return out, nil
	}
}

// authItems lists auth/<mount>/<sub> on every auth mount of the given types.
func authItems[T engine.Object](types []string, sub string, build func(mount, name string) T) func(context.Context, *scope) ([]listed[T], error) {
	return func(ctx context.Context, s *scope) ([]listed[T], error) {
		return mountItems(ctx, s, mountsOf(s.auth, types...), func(m string) string { return "auth/" + m + "/" + sub }, build)
	}
}

// secretsItems lists <mount>/<sub> on every secrets mount of the given types.
func secretsItems[T engine.Object](types []string, sub string, build func(mount, name string) T) func(context.Context, *scope) ([]listed[T], error) {
	return func(ctx context.Context, s *scope) ([]listed[T], error) {
		return mountItems(ctx, s, mountsOf(s.secrets, types...), func(m string) string { return m + "/" + sub }, build)
	}
}

func mountItems[T engine.Object](ctx context.Context, s *scope, paths []string, listPath func(string) string,
	build func(mount, name string) T) ([]listed[T], error) {
	var out []listed[T]
	for _, m := range paths {
		keys, err := s.keys(ctx, listPath(m))
		if err != nil {
			return nil, fmt.Errorf("list %s: %w", listPath(m), err)
		}
		for _, key := range keys {
			out = append(out, listed[T]{obj: build(m, key), parts: []string{m, key}})
		}
	}
	return out, nil
}

// authPath returns the path of the auth mount with accessor, or "".
func (s *scope) authPath(accessor string) string {
	for path, m := range s.auth {
		if m.accessor == accessor {
			return path
		}
	}
	return ""
}

func entityAliases(ctx context.Context, s *scope) ([]listed[*identityv1alpha1.EntityAlias], error) {
	entities, err := s.keys(ctx, "identity/entity/name")
	if err != nil {
		return nil, err
	}
	var out []listed[*identityv1alpha1.EntityAlias]
	for _, entity := range entities {
		data, err := s.c.Read(ctx, "identity/entity/name/"+entity)
		if err != nil {
			return nil, fmt.Errorf("read entity %s: %w", entity, err)
		}
		aliases, _ := data["aliases"].([]any)
		for _, raw := range aliases {
			alias, _ := raw.(map[string]any)
			name, _ := alias["name"].(string)
			accessor, _ := alias["mount_accessor"].(string)
			mountPath := s.authPath(accessor)
			if name == "" || mountPath == "" {
				s.note("EntityAlias %q of entity %s not imported: auth mount %s not found", name, entity, accessor)
				continue
			}
			o := &identityv1alpha1.EntityAlias{}
			o.Spec.AliasName, o.Spec.MountPath, o.Spec.EntityName = name, mountPath, entity
			out = append(out, listed[*identityv1alpha1.EntityAlias]{obj: o, parts: []string{entity, mountPath, name}})
		}
	}
	return out, nil
}

func groupAliases(ctx context.Context, s *scope) ([]listed[*identityv1alpha1.GroupAlias], error) {
	groups, err := s.keys(ctx, "identity/group/name")
	if err != nil {
		return nil, err
	}
	var out []listed[*identityv1alpha1.GroupAlias]
	for _, group := range groups {
		data, err := s.c.Read(ctx, "identity/group/name/"+group)
		if err != nil {
			return nil, fmt.Errorf("read group %s: %w", group, err)
		}
		alias, _ := data["alias"].(map[string]any)
		name, _ := alias["name"].(string)
		if name == "" {
			continue
		}
		accessor, _ := alias["mount_accessor"].(string)
		mountPath := s.authPath(accessor)
		if mountPath == "" {
			s.note("GroupAlias %q of group %s not imported: auth mount %s not found", name, group, accessor)
			continue
		}
		o := &identityv1alpha1.GroupAlias{}
		o.Spec.AliasName, o.Spec.MountPath, o.Spec.GroupName = name, mountPath, group
		out = append(out, listed[*identityv1alpha1.GroupAlias]{obj: o, parts: []string{group, mountPath, name}})
	}
	return out, nil
}

// idNames maps the IDs of the objects listed at byName (identity/entity/name, identity/group/name or
// identity/oidc/client) to their names, reading idKey from each object. The map is cached per scope.
func (s *scope) idNames(ctx context.Context, byName, idKey string) (map[string]string, error) {
	if m, ok := s.cache[byName]; ok {
		return m, nil
	}
	names, err := s.keys(ctx, byName)
	if err != nil {
		return nil, err
	}
	m := map[string]string{}
	for _, name := range names {
		path := byName + "/" + name
		data, err := s.c.Read(ctx, path)
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", path, err)
		}
		if id, _ := data[idKey].(string); id != "" {
			m[id] = name
		}
	}
	s.cache[byName] = m
	return m, nil
}

func stringList(raw any) []string {
	items, _ := raw.([]any)
	out := make([]string, 0, len(items))
	for _, item := range items {
		if s, ok := item.(string); ok && s != "" {
			out = append(out, s)
		}
	}
	return out
}

func fillOIDCAssignment(ctx context.Context, s *scope, o *identityv1alpha1.OIDCAssignment, observed engine.Payload) error {
	entities, err := s.idNames(ctx, "identity/entity/name", "id")
	if err != nil {
		return err
	}
	groups, err := s.idNames(ctx, "identity/group/name", "id")
	if err != nil {
		return err
	}
	for _, id := range stringList(observed["entity_ids"]) {
		match := identityv1alpha1.EntityMatch{EntityID: id}
		if name, ok := entities[id]; ok {
			match = identityv1alpha1.EntityMatch{EntityName: name}
		}
		o.Spec.Entities = append(o.Spec.Entities, match)
	}
	for _, id := range stringList(observed["group_ids"]) {
		match := identityv1alpha1.GroupMatch{GroupID: id}
		if name, ok := groups[id]; ok {
			match = identityv1alpha1.GroupMatch{GroupName: name}
		}
		o.Spec.Groups = append(o.Spec.Groups, match)
	}
	return nil
}

func fillOIDCProvider(ctx context.Context, s *scope, o *identityv1alpha1.OIDCProvider, observed engine.Payload) error {
	ids := stringList(observed["allowed_client_ids"])
	if len(ids) == 0 {
		return nil
	}
	clients, err := s.idNames(ctx, "identity/oidc/client", "client_id")
	if err != nil {
		return err
	}
	for _, id := range ids {
		match := identityv1alpha1.OIDCClientMatch{ClientID: id}
		if name, ok := clients[id]; ok {
			match = identityv1alpha1.OIDCClientMatch{ClientName: name}
		}
		o.Spec.AllowedClients = append(o.Spec.AllowedClients, match)
	}
	return nil
}

func pkiIssuers(ctx context.Context, s *scope) ([]listed[*secretsv1alpha1.PKIIssuer], error) {
	var out []listed[*secretsv1alpha1.PKIIssuer]
	for _, m := range mountsOf(s.secrets, "pki") {
		ids, err := s.keys(ctx, m+"/issuers")
		if err != nil {
			return nil, fmt.Errorf("list %s/issuers: %w", m, err)
		}
		for _, id := range ids {
			data, err := s.c.Read(ctx, m+"/issuer/"+id)
			if err != nil {
				return nil, fmt.Errorf("read %s/issuer/%s: %w", m, id, err)
			}
			name, _ := data["issuer_name"].(string)
			certPEM, _ := data["certificate"].(string)
			cn := commonName(certPEM)
			if name == "" || cn == "" {
				s.note("PKIIssuer %s in mount %s not imported: it needs an issuer name and a certificate common name", id, m)
				continue
			}
			o := &secretsv1alpha1.PKIIssuer{}
			o.Spec.MountPath, o.Spec.Name = m, name
			o.Spec.Type = secretsv1alpha1.PKIIssuerType("root")
			if len(stringList(data["ca_chain"])) > 1 {
				o.Spec.Type = secretsv1alpha1.PKIIssuerType("intermediate")
			}
			o.Spec.Certificate.CommonName = cn
			out = append(out, listed[*secretsv1alpha1.PKIIssuer]{obj: o, parts: []string{m, name}})
		}
	}
	return out, nil
}

func commonName(certPEM string) string {
	block, _ := pem.Decode([]byte(certPEM))
	if block == nil {
		return ""
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return ""
	}
	return cert.Subject.CommonName
}

func fillPKIConfig(ctx context.Context, s *scope, o *secretsv1alpha1.PKIConfig, observed engine.Payload) error {
	defaultID, _ := observed["issuers.default"].(string)
	if defaultID == "" {
		o.Spec.Issuers = nil
		return nil
	}
	name := defaultID
	issuer, err := s.c.Read(ctx, o.Spec.MountPath+"/issuer/"+defaultID)
	if err != nil {
		return fmt.Errorf("read the default issuer of %s: %w", o.Spec.MountPath, err)
	}
	if n, _ := issuer["issuer_name"].(string); n != "" {
		name = n
	}
	o.Spec.Issuers.Default = secretsv1alpha1.PKIIssuerReference{IssuerName: name}
	if follows, ok := observed["issuers.default_follows_latest_issuer"].(bool); ok && follows {
		o.Spec.Issuers.DefaultFollowsLatestIssuer = &follows
	}
	return nil
}

// fillDatabaseConnection maps the connection settings onto the plugin's section of the spec, or onto pluginConfig
// for a plugin without one.
func fillDatabaseConnection(ctx context.Context, s *scope, o *secretsv1alpha1.DatabaseConnection, _ engine.Payload) error {
	path := o.Spec.MountPath + "/config/" + o.Spec.Name
	read, err := s.c.Read(ctx, path)
	if err != nil {
		return fmt.Errorf("read %s: %w", path, err)
	}
	details, _ := read["connection_details"].(map[string]any)
	section := databaseSection(o)
	if !section.IsValid() {
		config := map[string]string{}
		for key, value := range details {
			if str, ok := value.(string); ok && str != "" {
				config[key] = str
			}
		}
		if len(config) > 0 {
			o.Spec.PluginConfig = config
		}
		return nil
	}
	_, unmapped := fill(section, details, nil)
	for _, field := range unmapped {
		s.note("%s: connection setting %s not imported, its OpenBao value has an unexpected type", s.label(o), field)
	}
	if url, _ := details["connection_url"].(string); strings.Contains(url, "xxxxx") {
		s.note("%s: OpenBao redacts the password in connection_url; replace it before managementPolicy Manage", s.label(o))
	}
	return nil
}

// databaseSection sets the spec section that matches the plugin and returns it, or an invalid Value when the
// plugin has no typed section.
func databaseSection(o *secretsv1alpha1.DatabaseConnection) reflect.Value {
	switch plugin := o.Spec.PluginName; {
	case plugin == "postgresql-database-plugin":
		o.Spec.PostgreSQL = &secretsv1alpha1.DatabasePostgreSQLConfig{}
		return reflect.ValueOf(o.Spec.PostgreSQL).Elem()
	case slices.Contains([]string{"mysql-database-plugin", "mysql-aurora-database-plugin", "mysql-rds-database-plugin",
		"mysql-legacy-database-plugin"}, plugin):
		o.Spec.MySQL = &secretsv1alpha1.DatabaseMySQLConfig{}
		return reflect.ValueOf(o.Spec.MySQL).Elem()
	case plugin == "cassandra-database-plugin":
		o.Spec.Cassandra = &secretsv1alpha1.DatabaseCassandraConfig{}
		return reflect.ValueOf(o.Spec.Cassandra).Elem()
	case plugin == "influxdb-database-plugin":
		o.Spec.InfluxDB = &secretsv1alpha1.DatabaseInfluxDBConfig{}
		return reflect.ValueOf(o.Spec.InfluxDB).Elem()
	case plugin == "valkey-database-plugin" || plugin == "redis-database-plugin":
		o.Spec.Valkey = &secretsv1alpha1.DatabaseValkeyConfig{}
		return reflect.ValueOf(o.Spec.Valkey).Elem()
	}
	return reflect.Value{}
}

// kvPaths walks every KV mount when KV paths are included. Only key names are listed; values are never read.
func kvPaths(ctx context.Context, s *scope) ([]listed[*secretsv1alpha1.KVSecret], error) {
	if !s.opts.IncludeKVPaths {
		return nil, nil
	}
	var out []listed[*secretsv1alpha1.KVSecret]
	for _, m := range mountsOf(s.secrets, "kv", "generic") {
		version := int32(1)
		if v, _ := s.secrets[m].options["version"].(string); v == "2" {
			version = 2
		}
		paths, err := s.kvWalk(ctx, m, version, "")
		if err != nil {
			return nil, err
		}
		for _, p := range paths {
			o := &secretsv1alpha1.KVSecret{}
			o.Spec.MountPath, o.Spec.Path, o.Spec.KVVersion = m, p, version
			out = append(out, listed[*secretsv1alpha1.KVSecret]{obj: o, parts: []string{m, p}})
		}
	}
	return out, nil
}

func (s *scope) kvWalk(ctx context.Context, mountPath string, version int32, prefix string) ([]string, error) {
	listPath := mountPath + "/" + prefix
	if version == 2 {
		listPath = mountPath + "/metadata/" + prefix
	}
	keys, err := s.keys(ctx, strings.TrimSuffix(listPath, "/"))
	if err != nil {
		return nil, fmt.Errorf("list %s: %w", listPath, err)
	}
	var out []string
	for _, key := range keys {
		if !strings.HasSuffix(key, "/") {
			out = append(out, prefix+key)
			continue
		}
		nested, err := s.kvWalk(ctx, mountPath, version, prefix+key)
		if err != nil {
			return nil, err
		}
		out = append(out, nested...)
	}
	return out, nil
}

func auditDevices(ctx context.Context, s *scope) ([]listed[*sysv1alpha1.Audit], error) {
	table, err := s.c.Read(ctx, "sys/audit")
	if errors.Is(err, openbao.ErrNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read sys/audit: %w", err)
	}
	var out []listed[*sysv1alpha1.Audit] //nolint:prealloc // entries that are not devices are skipped
	for _, key := range slices.Sorted(maps.Keys(table)) {
		entry, ok := table[key].(map[string]any)
		typ, _ := entry["type"].(string)
		if !ok || typ == "" {
			continue
		}
		path := strings.TrimSuffix(key, "/")
		o := &sysv1alpha1.Audit{}
		o.Spec.Path, o.Spec.Type = path, sysv1alpha1.AuditType(typ)
		out = append(out, listed[*sysv1alpha1.Audit]{obj: o, parts: []string{path}})
	}
	return out, nil
}

// fillAudit moves options that can carry credentials, and URLs that do, out of spec.options into Secret references.
func fillAudit(_ context.Context, s *scope, o *sysv1alpha1.Audit, _ engine.Payload) error {
	for _, key := range slices.Sorted(maps.Keys(o.Spec.Options)) {
		if _, credentials := redactURL(string(o.Spec.Options[key])); !credentials && !slices.Contains(sensitiveAuditOptions, key) {
			continue
		}
		delete(o.Spec.Options, key)
		if o.Spec.OptionsSecretRefs == nil {
			o.Spec.OptionsSecretRefs = map[string]commonv1alpha1.SecretKeySelector{}
		}
		ref := commonv1alpha1.SecretKeySelector{Name: o.Name, Key: key}
		o.Spec.OptionsSecretRefs[key] = ref
		s.result.Placeholders = append(s.result.Placeholders, Placeholder{Object: s.label(o),
			Field: "spec.optionsSecretRefs." + key, Secret: ref.Name, Key: ref.Key})
	}
	return nil
}
