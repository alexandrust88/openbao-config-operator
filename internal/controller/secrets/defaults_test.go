package secrets

import (
	"context"
	"maps"
	"slices"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	commonv1alpha1 "github.com/alexandrust88/openbao-config-operator/api/common/v1alpha1"
	secretsv1alpha1 "github.com/alexandrust88/openbao-config-operator/api/secrets/v1alpha1"
	"github.com/alexandrust88/openbao-config-operator/internal/engine"
)

// defaultsCase checks that a spec with only its required fields matches the read of an object OpenBao created
// with the same input, and that a setting changed away from its default is reported, so removing a field from
// the spec resets it.
func defaultsCase[T engine.Object](t *testing.T, a engine.Adapter[T], obj T, r engine.Resolver, path string,
	read map[string]any, changedKey string, changedValue any) {
	t.Helper()
	desired, err := a.Desired(context.Background(), r, obj)
	if err != nil {
		t.Fatal(err)
	}
	observe := func(read map[string]any) engine.Payload {
		observer, ok := any(a).(engine.Observer[T])
		if !ok {
			return read
		}
		observed, found, err := observer.Observe(context.Background(), &credClient{reads: map[string]map[string]any{path: read}}, obj)
		if err != nil || !found {
			t.Fatalf("observe: found=%v err=%v", found, err)
		}
		return observed
	}
	if d := engine.Diff(desired, observe(maps.Clone(read)), a.Fields()); len(d) != 0 {
		t.Errorf("empty spec differs from the server defaults on %v", d)
	}
	changed := maps.Clone(read)
	changed[changedKey] = changedValue
	if d := engine.Diff(desired, observe(changed), a.Fields()); !slices.Contains(d, changedKey) {
		t.Errorf("non-default %s not reported: differing %v", changedKey, d)
	}
}

// TestSecretsEmptySpecDefaults uses reads captured from bao server -dev 2.7.2 after writing each object with only
// its required inputs.
func TestSecretsEmptySpecDefaults(t *testing.T) {
	meta := metav1.ObjectMeta{Name: "e", Namespace: "team"}
	t.Run("databaserole", func(t *testing.T) {
		defaultsCase(t, DatabaseRoleAdapter{},
			&secretsv1alpha1.DatabaseRole{ObjectMeta: meta, Spec: secretsv1alpha1.DatabaseRoleSpec{MountPath: "database", DBName: "pg"}},
			nil, "database/roles/e", map[string]any{
				"creation_statements": []any{}, "credential_type": "password", "db_name": "pg", "default_ttl": 0.0,
				"max_ttl": 0.0, "renew_statements": []any{}, "revocation_statements": []any{}, "rollback_statements": []any{},
			}, "max_ttl", 3600.0)
	})
	t.Run("databaseconnection", func(t *testing.T) {
		conn := &secretsv1alpha1.DatabaseConnection{ObjectMeta: metav1.ObjectMeta{Name: "pg", Namespace: "team"},
			Spec: secretsv1alpha1.DatabaseConnectionSpec{
				MountPath: "database", PluginName: "postgresql-database-plugin",
				PasswordSecretRef: &commonv1alpha1.SecretKeySelector{Name: "db-root", Key: "password"},
				PostgreSQL: &secretsv1alpha1.DatabasePostgreSQLConfig{DatabaseSQLConfig: secretsv1alpha1.DatabaseSQLConfig{
					ConnectionURL: "postgresql://{{username}}:{{password}}@localhost:1/x", Username: "u"}},
			}}
		defaultsCase(t, DatabaseConnectionAdapter{}, conn, dbResolver(), "database/config/pg", map[string]any{
			"allowed_roles": []any{}, "password_policy": "", "plugin_name": "postgresql-database-plugin", "plugin_version": "",
			"root_credentials_rotate_statements": []any{},
			"connection_details":                 map[string]any{"connection_url": "postgresql://{{username}}:{{password}}@localhost:1/x", "username": "u"},
		}, "allowed_roles", []any{"app"})
	})
	t.Run("transitkey", func(t *testing.T) {
		defaultsCase(t, TransitKeyAdapter{},
			&secretsv1alpha1.TransitKey{ObjectMeta: meta, Spec: secretsv1alpha1.TransitKeySpec{MountPath: "transit", Type: "aes256-gcm96"}},
			nil, "transit/keys/e", map[string]any{
				"allow_plaintext_backup": false, "auto_rotate_period": 0.0, "deletion_allowed": false, "derived": false,
				"exportable": false, "imported_key": false, "keys": map[string]any{"1": 1794612616.0}, "latest_version": 1.0,
				"min_available_version": 0.0, "min_decryption_version": 1.0, "min_encryption_version": 0.0, "name": "e",
				"soft_deleted": false, "supports_decryption": true, "supports_derivation": true, "supports_encryption": true,
				"supports_signing": false, "type": "aes256-gcm96",
			}, "deletion_allowed", true)
	})
	t.Run("rabbitmqrole", func(t *testing.T) {
		defaultsCase(t, RabbitMQRoleAdapter{},
			&secretsv1alpha1.RabbitMQRole{ObjectMeta: meta, Spec: secretsv1alpha1.RabbitMQRoleSpec{MountPath: "rabbitmq"}},
			nil, "rabbitmq/roles/e", map[string]any{"tags": "", "vhost_topics": map[string]any{}, "vhosts": map[string]any{}},
			"tags", "administrator")
	})
	t.Run("rabbitmqconfig lease", func(t *testing.T) {
		cfg := newRabbitMQConfig()
		cfg.Spec.Lease = nil
		defaultsCase(t, RabbitMQConfigAdapter{}, cfg, credResolver{"rabbit-admin": {"password": "fake"}},
			"rabbitmq/config/lease", map[string]any{"max_ttl": 0.0, "ttl": 0.0}, "ttl", 60.0)
	})
	t.Run("kubernetessecretsrole", func(t *testing.T) {
		defaultsCase(t, KubernetesSecretsRoleAdapter{},
			&secretsv1alpha1.KubernetesSecretsRole{ObjectMeta: meta, Spec: secretsv1alpha1.KubernetesSecretsRoleSpec{
				MountPath: "kubernetes", AllowedKubernetesNamespaces: []string{"a"}, ServiceAccountName: "s"}},
			nil, "kubernetes/roles/e", map[string]any{
				"allowed_kubernetes_namespace_selector": "", "allowed_kubernetes_namespaces": []any{"a"}, "extra_annotations": nil,
				"extra_labels": nil, "generated_role_rules": "", "kubernetes_role_name": "", "kubernetes_role_type": k8sTestRoleType,
				"name": "e", "name_template": "", "service_account_name": "s", "token_default_audiences": nil,
				"token_default_ttl": 0.0, "token_max_ttl": 0.0,
			}, "kubernetes_role_type", "ClusterRole")
	})
	t.Run("kubernetessecretsconfig", func(t *testing.T) {
		defaultsCase(t, KubernetesSecretsConfigAdapter{},
			&secretsv1alpha1.KubernetesSecretsConfig{ObjectMeta: meta, Spec: secretsv1alpha1.KubernetesSecretsConfigSpec{
				MountPath: "kubernetes", KubernetesHost: "https://k:6443"}},
			nil, "kubernetes/config", map[string]any{"disable_local_ca_jwt": false, "kubernetes_ca_cert": "", "kubernetes_host": "https://k:6443"},
			"disable_local_ca_jwt", true)
	})
	t.Run("sshrole ca", func(t *testing.T) {
		defaultsCase(t, SSHRoleAdapter{},
			&secretsv1alpha1.SSHRole{ObjectMeta: meta, Spec: secretsv1alpha1.SSHRoleSpec{
				MountPath: "ssh", KeyType: secretsv1alpha1.SSHRoleKeyTypeCA, AllowUserCertificates: new(true)}},
			nil, "ssh/roles/e", map[string]any{
				"algorithm_signer": "default", "allow_bare_domains": false, "allow_commas_in_identity_templates": false,
				"allow_empty_principals": false, "allow_host_certificates": false, "allow_subdomains": false,
				"allow_user_certificates": true, "allow_user_key_ids": false, "allow_wildcards_in_identity_templates": false,
				"allowed_critical_options": "", "allowed_domains": "", "allowed_domains_template": false, "allowed_extensions": "",
				"allowed_user_key_lengths": map[string]any{}, "allowed_users": "", "allowed_users_template": false,
				"default_critical_options": map[string]any{}, "default_extensions": map[string]any{},
				"default_extensions_template": false, "default_user": "", "default_user_template": false, "issuer_ref": "default",
				"key_id_format": "", "key_type": "ca", "max_ttl": 0.0, "not_before_duration": 30.0, "ttl": 0.0,
			}, "not_before_duration", 60.0)
	})
	t.Run("sshrole otp", func(t *testing.T) {
		defaultsCase(t, SSHRoleAdapter{},
			&secretsv1alpha1.SSHRole{ObjectMeta: meta, Spec: secretsv1alpha1.SSHRoleSpec{
				MountPath: "ssh", KeyType: secretsv1alpha1.SSHRoleKeyTypeOTP, DefaultUser: "x"}},
			nil, "ssh/roles/e", map[string]any{
				"allowed_users": "", "cidr_list": "", "default_user": "x", "exclude_cidr_list": "", "key_type": "otp", "port": 22.0,
			}, "port", 2222.0)
	})
	t.Run("totpkey imported", func(t *testing.T) {
		defaultsCase(t, TOTPKeyAdapter{},
			&secretsv1alpha1.TOTPKey{ObjectMeta: meta, Spec: secretsv1alpha1.TOTPKeySpec{
				MountPath: "totp", Issuer: "i", AccountName: "a",
				KeySecretRef: &commonv1alpha1.SecretKeySelector{Name: "seed", Key: "key"}}},
			credResolver{"seed": {"key": "fake-seed"}}, "totp/keys/e",
			map[string]any{"account_name": "a", "algorithm": totpTestAlgorithm, "digits": 6.0, "issuer": "i", "period": 30.0},
			"digits", 8.0)
	})
	t.Run("kvconfig", func(t *testing.T) {
		defaultsCase(t, KVConfigAdapter{},
			&secretsv1alpha1.KVConfig{ObjectMeta: meta, Spec: secretsv1alpha1.KVConfigSpec{MountPath: "kv2"}},
			nil, "kv2/config", map[string]any{
				"cas_required": false, "delete_version_after": "0s", "max_versions": 0.0, "metadata_cas_required": false,
			}, "delete_version_after", "768h0m0s")
	})
	t.Run("pkirole", func(t *testing.T) {
		defaultsCase(t, PKIRoleAdapter{},
			&secretsv1alpha1.PKIRole{ObjectMeta: meta, Spec: secretsv1alpha1.PKIRoleSpec{MountPath: "pki"}},
			nil, "pki/roles/e", map[string]any{
				"allow_any_name": false, "allow_bare_domains": false, "allow_glob_domains": false,
				"allow_globs_in_identity_templates": false, "allow_ip_sans": true, "allow_localhost": true, "allow_subdomains": false,
				"allow_token_displayname": false, "allow_wildcard_certificates": true, "allowed_domains": []any{},
				"allowed_domains_template": false, "allowed_ip_sans_cidr": []any{}, "allowed_other_sans": []any{},
				"allowed_serial_numbers": []any{}, "allowed_uri_sans": []any{}, "allowed_uri_sans_template": false,
				"allowed_user_ids": []any{}, "basic_constraints_valid_for_non_ca": false, "client_flag": true,
				"cn_validations": []any{"email", "hostname"}, "code_signing_flag": false, "country": []any{},
				"email_protection_flag": false, "enforce_hostnames": true, "ext_key_usage": []any{}, "ext_key_usage_oids": []any{},
				"generate_lease": false, "issuer_ref": "default", "key_bits": 2048.0, "key_type": "rsa",
				"key_usage": []any{"DigitalSignature", "KeyAgreement", "KeyEncipherment"}, "locality": []any{}, "max_ttl": 0.0,
				"no_store": false, "not_after": "", "not_after_bound": "permit", "not_before": "", "not_before_bound": "permit",
				"not_before_duration": 30.0, "organization": []any{}, "ou": []any{}, "policy_identifiers": []any{},
				"postal_code": []any{}, "province": []any{}, "require_cn": true, "server_flag": true, "signature_bits": 0.0,
				"street_address": []any{}, "ttl": 0.0, "use_csr_common_name": true, "use_csr_sans": true, "use_pss": false,
			}, "allow_localhost", false)
	})
	t.Run("pkiautotidy", func(t *testing.T) {
		defaultsCase(t, PKIAutoTidyAdapter{},
			&secretsv1alpha1.PKIAutoTidy{ObjectMeta: meta, Spec: secretsv1alpha1.PKIAutoTidySpec{MountPath: "pki"}},
			nil, "pki/config/auto-tidy", map[string]any{
				"acme_account_safety_buffer": 2592000.0, "enabled": false, "interval_duration": 43200.0,
				"issuer_safety_buffer": 31536000.0, "maintain_stored_certificate_counts": false, "page_size": 1000.0,
				"pause_duration": "0s", "publish_stored_certificate_count_metrics": false, "revoked_safety_buffer": 259200.0,
				"safety_buffer": 259200.0, "tidy_acme": false, "tidy_cert_store": false, "tidy_expired_issuers": false,
				"tidy_invalid_certs": false, "tidy_move_legacy_ca_bundle": false, "tidy_revoked_cert_issuer_associations": false,
				"tidy_revoked_certs": false,
			}, "revoked_safety_buffer", 3600.0)
	})
	t.Run("pkiacmeconfig", func(t *testing.T) {
		defaultsCase(t, PKIACMEConfigAdapter{},
			&secretsv1alpha1.PKIACMEConfig{ObjectMeta: meta, Spec: secretsv1alpha1.PKIACMEConfigSpec{MountPath: "pki"}},
			nil, "pki/config/acme", map[string]any{
				"allow_role_ext_key_usage": false, "allowed_issuers": []any{"*"}, "allowed_roles": []any{"*"},
				"default_directory_policy": "sign-verbatim", "dns_resolver": "", "eab_policy": "not-required", "enabled": false,
			}, "dns_resolver", "10.0.0.53:53")
	})
	t.Run("pkiissuer", func(t *testing.T) {
		defaultsCase(t, PKIIssuerAdapter{},
			&secretsv1alpha1.PKIIssuer{ObjectMeta: meta, Spec: secretsv1alpha1.PKIIssuerSpec{MountPath: "pki",
				Type: secretsv1alpha1.PKIIssuerTypeRoot}},
			nil, "pki/issuer/e", map[string]any{
				"crl_distribution_points": []any{}, "delta_crl_distribution_points": []any{},
				"issuer_id": "0c2c8621-9e0e-df88-0687-0cc84539d49b", "issuer_name": "e", "issuing_certificates": []any{},
				"key_id": "b0771b84-fcb0-c055-92a0-03108496d637", "leaf_not_after_behavior": "err", "manual_chain": nil,
				"ocsp_servers": []any{}, "revocation_signature_algorithm": "", "revoked": false,
				"usage": "crl-signing,issuing-certificates,ocsp-signing,read-only",
			}, "revocation_signature_algorithm", "ECDSAWithSHA384")
	})
}
