package secrets

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"net/url"
	"slices"
	"strings"

	ctrl "sigs.k8s.io/controller-runtime"

	commonv1alpha1 "github.com/alexandrust88/openbao-config-operator/api/common/v1alpha1"
	secretsv1alpha1 "github.com/alexandrust88/openbao-config-operator/api/secrets/v1alpha1"
	"github.com/alexandrust88/openbao-config-operator/internal/engine"
	"github.com/alexandrust88/openbao-config-operator/internal/openbao"
)

// +kubebuilder:rbac:groups=secrets.openbaoconfig.io,resources=databaseconnections,verbs=get;list;watch;update;patch
// +kubebuilder:rbac:groups=secrets.openbaoconfig.io,resources=databaseconnections/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=secrets.openbaoconfig.io,resources=databaseconnections/finalizers,verbs=update
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch

// SetupDatabaseConnection registers the DatabaseConnection controller with the manager.
func SetupDatabaseConnection(mgr ctrl.Manager, conns engine.Connections, opts engine.Options) error {
	r := engine.New[*secretsv1alpha1.DatabaseConnection](
		mgr.GetClient(),
		mgr.GetScheme(),
		mgr.GetEventRecorder("databaseconnection-controller"),
		conns,
		DatabaseConnectionAdapter{},
		func() *secretsv1alpha1.DatabaseConnection { return &secretsv1alpha1.DatabaseConnection{} },
		opts,
	)
	return r.SetupWithManager(mgr)
}

// DatabaseConnectionAdapter maps a DatabaseConnection onto <mount>/config/<name>.
type DatabaseConnectionAdapter struct{}

var (
	_ engine.Adapter[*secretsv1alpha1.DatabaseConnection]          = DatabaseConnectionAdapter{}
	_ engine.FieldsProvider[*secretsv1alpha1.DatabaseConnection]   = DatabaseConnectionAdapter{}
	_ engine.Observer[*secretsv1alpha1.DatabaseConnection]         = DatabaseConnectionAdapter{}
	_ engine.Writer[*secretsv1alpha1.DatabaseConnection]           = DatabaseConnectionAdapter{}
	_ engine.SecretReferencer[*secretsv1alpha1.DatabaseConnection] = DatabaseConnectionAdapter{}
)

// Path implements engine.Adapter.
func (DatabaseConnectionAdapter) Path(c *secretsv1alpha1.DatabaseConnection) string {
	return c.Spec.MountPath + "/config/" + specName(c.Spec.Name, c.Name)
}

// Desired implements engine.Adapter. Connection settings are flat in the write shape; secret material is
// resolved from the referenced Secrets.
func (DatabaseConnectionAdapter) Desired(ctx context.Context, r engine.Resolver, c *secretsv1alpha1.DatabaseConnection) (engine.Payload, error) {
	p := databaseConnectionDetails(c)
	p["plugin_name"] = c.Spec.PluginName
	optString(p, "plugin_version", c.Spec.PluginVersion)
	optBool(p, "verify_connection", c.Spec.VerifyConnection)
	orList(p, "allowed_roles", c.Spec.AllowedRoles)
	orList(p, "root_rotation_statements", c.Spec.RootRotationStatements)
	orString(p, "password_policy", c.Spec.PasswordPolicy, "")
	for key, ref := range databaseConnectionSecretRefs(c) {
		value, err := r.SecretValue(ctx, c.Namespace, *ref)
		if err != nil {
			return nil, err
		}
		p[key] = string(value)
	}
	return p, nil
}

// Fields implements engine.Adapter.
func (DatabaseConnectionAdapter) Fields() engine.FieldSpec {
	return engine.FieldSpec{
		WriteOnly: []string{"password", "private_key", "tls_certificate_key", "pem_bundle", "pem_json", "verify_connection"},
		Aliases:   map[string]string{"root_credentials_rotate_statements": "root_rotation_statements"},
		Sets:      []string{"allowed_roles"},
		Durations: []string{"max_connection_lifetime", "connect_timeout", "socket_keep_alive"},
	}
}

// FieldsFor implements engine.FieldsProvider. After rotateRootOnCreate only OpenBao knows the root
// credentials, so the stale ones from the Secret are sent on create only.
func (DatabaseConnectionAdapter) FieldsFor(_ context.Context, _ engine.Resolver, c *secretsv1alpha1.DatabaseConnection) (engine.FieldSpec, error) {
	if !c.Spec.RotateRootOnCreate {
		return engine.FieldSpec{}, nil
	}
	return engine.FieldSpec{CreateOnly: []string{"password", "private_key"}}, nil
}

// Observe implements engine.Observer. The read nests the connection settings under connection_details and
// redacts the password of connection_url; both are mapped back to the write shape.
func (a DatabaseConnectionAdapter) Observe(ctx context.Context, c openbao.Client, obj *secretsv1alpha1.DatabaseConnection) (engine.Payload, bool, error) {
	path := a.Path(obj)
	read, err := c.Read(ctx, path)
	if errors.Is(err, openbao.ErrNotFound) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("read %s: %w", path, err)
	}
	observed := engine.Payload{}
	if details, ok := read["connection_details"].(map[string]any); ok {
		maps.Copy(observed, details)
	}
	for key, value := range read {
		if key != "connection_details" {
			observed[key] = value
		}
	}
	if want, ok := databaseConnectionDetails(obj)["connection_url"].(string); ok {
		if got, _ := observed["connection_url"].(string); got != want && got == redactURL(want) {
			observed["connection_url"] = want
		}
	}
	return observed, true, nil
}

// Write implements engine.Writer. With rotateRootOnCreate a new connection gets its root credentials
// rotated once; if that fails the connection is removed again, so the next reconcile creates and rotates.
func (a DatabaseConnectionAdapter) Write(ctx context.Context, c openbao.Client, obj *secretsv1alpha1.DatabaseConnection, desired engine.Payload, create bool) (engine.Payload, error) {
	path := a.Path(obj)
	if _, err := c.Write(ctx, path, desired); err != nil {
		return nil, fmt.Errorf("write %s: %w", path, err)
	}
	if !create || !obj.Spec.RotateRootOnCreate {
		return nil, nil
	}
	rotatePath := obj.Spec.MountPath + "/rotate-root/" + specName(obj.Spec.Name, obj.Name)
	if _, err := c.Write(ctx, rotatePath, map[string]any{}); err != nil {
		rotateErr := fmt.Errorf("rotate root credentials of %s: %w", path, err)
		if delErr := c.Delete(ctx, path); delErr != nil && !errors.Is(delErr, openbao.ErrNotFound) {
			return nil, errors.Join(rotateErr, fmt.Errorf("remove %s after failed rotation: %w", path, delErr))
		}
		return nil, rotateErr
	}
	return nil, nil
}

// SecretRefs implements engine.SecretReferencer.
func (DatabaseConnectionAdapter) SecretRefs(c *secretsv1alpha1.DatabaseConnection) []string {
	refs := databaseConnectionSecretRefs(c)
	names := make([]string, 0, len(refs))
	for _, ref := range refs {
		names = append(names, ref.Name)
	}
	slices.Sort(names)
	return slices.Compact(names)
}

// databaseConnectionSecretRefs returns the set secret references by their OpenBao key.
func databaseConnectionSecretRefs(c *secretsv1alpha1.DatabaseConnection) map[string]*commonv1alpha1.SecretKeySelector {
	refs := map[string]*commonv1alpha1.SecretKeySelector{
		"password":    c.Spec.PasswordSecretRef,
		"private_key": c.Spec.PrivateKeySecretRef,
	}
	if s := c.Spec.MySQL; s != nil {
		refs["tls_certificate_key"] = s.TLSCertificateKeySecretRef
	}
	for _, tls := range []*secretsv1alpha1.DatabaseTLSPemConfig{cassandraTLS(c), influxDBTLS(c)} {
		if tls != nil {
			refs["pem_bundle"] = tls.PemBundleSecretRef
			refs["pem_json"] = tls.PemJSONSecretRef
		}
	}
	maps.DeleteFunc(refs, func(_ string, ref *commonv1alpha1.SecretKeySelector) bool { return ref == nil })
	return refs
}

func cassandraTLS(c *secretsv1alpha1.DatabaseConnection) *secretsv1alpha1.DatabaseTLSPemConfig {
	if c.Spec.Cassandra == nil {
		return nil
	}
	return &c.Spec.Cassandra.DatabaseTLSPemConfig
}

func influxDBTLS(c *secretsv1alpha1.DatabaseConnection) *secretsv1alpha1.DatabaseTLSPemConfig {
	if c.Spec.InfluxDB == nil {
		return nil
	}
	return &c.Spec.InfluxDB.DatabaseTLSPemConfig
}

// databaseConnectionDetails returns the non-secret connection settings: pluginConfig with the plugin section
// over it.
func databaseConnectionDetails(c *secretsv1alpha1.DatabaseConnection) engine.Payload {
	p := engine.Payload{}
	for key, value := range c.Spec.PluginConfig {
		p[key] = value
	}
	if s := c.Spec.PostgreSQL; s != nil {
		putSQLConfig(p, &s.DatabaseSQLConfig)
		optBool(p, "disable_escaping", s.DisableEscaping)
		optString(p, "password_authentication", s.PasswordAuthentication)
	}
	if s := c.Spec.MySQL; s != nil {
		putSQLConfig(p, &s.DatabaseSQLConfig)
		optString(p, "tls_ca", s.TLSCA)
		optString(p, "tls_server_name", s.TLSServerName)
		optBool(p, "tls_skip_verify", s.TLSSkipVerify)
	}
	if s := c.Spec.Cassandra; s != nil {
		putTLSPemConfig(p, &s.DatabaseTLSPemConfig)
		p["hosts"] = strings.Join(s.Hosts, ",")
		optInt32(p, "port", s.Port)
		optInt32(p, "protocol_version", s.ProtocolVersion)
		optString(p, "username", s.Username)
		optString(p, "tls_server_name", s.TLSServerName)
		optBool(p, "skip_verification", s.SkipVerification)
		optSeconds(p, "socket_keep_alive", s.SocketKeepAlive)
		optString(p, "consistency", s.Consistency)
		optString(p, "local_datacenter", s.LocalDatacenter)
	}
	if s := c.Spec.InfluxDB; s != nil {
		putTLSPemConfig(p, &s.DatabaseTLSPemConfig)
		p["host"] = s.Host
		optInt32(p, "port", s.Port)
		p["username"] = s.Username
	}
	if s := c.Spec.Valkey; s != nil {
		optString(p, "connection_url", s.ConnectionURL)
		optString(p, "host", s.Host)
		optInt32(p, "port", s.Port)
		optString(p, "username", s.Username)
		optBool(p, "tls", s.TLS)
		optBool(p, "insecure_tls", s.InsecureTLS)
		optString(p, "ca_cert", s.CACert)
	}
	return p
}

func putSQLConfig(p engine.Payload, s *secretsv1alpha1.DatabaseSQLConfig) {
	p["connection_url"] = s.ConnectionURL
	optString(p, "username", s.Username)
	optInt32(p, "max_open_connections", s.MaxOpenConnections)
	optInt32(p, "max_idle_connections", s.MaxIdleConnections)
	optSeconds(p, "max_connection_lifetime", s.MaxConnectionLifetime)
	optString(p, "username_template", s.UsernameTemplate)
}

func putTLSPemConfig(p engine.Payload, s *secretsv1alpha1.DatabaseTLSPemConfig) {
	optBool(p, "tls", s.TLS)
	optBool(p, "insecure_tls", s.InsecureTLS)
	optString(p, "tls_min_version", s.TLSMinVersion)
	optSeconds(p, "connect_timeout", s.ConnectTimeout)
	optString(p, "username_template", s.UsernameTemplate)
}

// redactURL mirrors the server, which returns connection_url with any password replaced.
func redactURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return raw
	}
	return u.Redacted()
}
