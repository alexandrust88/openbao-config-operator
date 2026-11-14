package secrets

import (
	"context"
	"errors"
	"maps"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"

	commonv1alpha1 "github.com/alexandrust88/openbao-config-operator/api/common/v1alpha1"
	secretsv1alpha1 "github.com/alexandrust88/openbao-config-operator/api/secrets/v1alpha1"
	"github.com/alexandrust88/openbao-config-operator/internal/engine"
	"github.com/alexandrust88/openbao-config-operator/internal/openbao"
)

var pkiHashKey = []byte("pki-test-hash-key")

type pkiWrite struct {
	path string
	data map[string]any
}

// pkiFakeClient serves reads from a map, answers writes from a map and records every write and delete.
type pkiFakeClient struct {
	openbao.Client
	reads     map[string]map[string]any
	responses map[string]map[string]any
	writes    []pkiWrite
	deletes   []string
}

func (c *pkiFakeClient) Read(_ context.Context, path string) (map[string]any, error) {
	data, ok := c.reads[path]
	if !ok {
		if strings.Contains(path, "/issuer/") || strings.Contains(path, "/key/") {
			kind := "issuer"
			if strings.Contains(path, "/key/") {
				kind = "key"
			}
			return nil, &openbao.ResponseError{StatusCode: 500, Errors: []string{
				"1 error occurred:\n\t* unable to find PKI " + kind + " for reference: x\n\n"}}
		}
		return nil, openbao.ErrNotFound
	}
	return data, nil
}

func (c *pkiFakeClient) Write(_ context.Context, path string, data map[string]any) (map[string]any, error) {
	c.writes = append(c.writes, pkiWrite{path: path, data: data})
	return c.responses[path], nil
}

func (c *pkiFakeClient) Delete(_ context.Context, path string) error {
	c.deletes = append(c.deletes, path)
	return nil
}

func (c *pkiFakeClient) writePaths() []string {
	paths := make([]string, 0, len(c.writes))
	for _, w := range c.writes {
		paths = append(paths, w.path)
	}
	return paths
}

// pkiFakeResolver serves Secrets and referenced PKI objects from memory.
type pkiFakeResolver struct {
	secrets map[string]map[string][]byte
	objects map[string]client.Object
}

func (r pkiFakeResolver) SecretValue(_ context.Context, _ string, ref commonv1alpha1.SecretKeySelector) ([]byte, error) {
	value, ok := r.secrets[ref.Name][ref.Key]
	if !ok {
		return nil, engine.ErrReferenceNotReady
	}
	return value, nil
}

func (r pkiFakeResolver) Object(_ context.Context, key client.ObjectKey, obj client.Object) error {
	stored, ok := r.objects[key.Name]
	if !ok {
		return errors.Join(engine.ErrReferenceNotReady, apierrors.NewNotFound(schema.GroupResource{}, key.Name))
	}
	reflect.ValueOf(obj).Elem().Set(reflect.ValueOf(stored).Elem())
	return nil
}

func pkiConnection() commonv1alpha1.ResourceSpec {
	return commonv1alpha1.ResourceSpec{ConnectionRef: commonv1alpha1.ConnectionReference{Name: "openbao"}}
}

func pkiReadyCondition() []metav1.Condition {
	return []metav1.Condition{{Type: commonv1alpha1.ConditionReady, Status: metav1.ConditionTrue}}
}

func pkiReadyIssuerObject(mount, id string) *secretsv1alpha1.PKIIssuer {
	return &secretsv1alpha1.PKIIssuer{
		ObjectMeta: metav1.ObjectMeta{Name: "root", Namespace: "team"},
		Spec:       secretsv1alpha1.PKIIssuerSpec{ResourceSpec: pkiConnection(), MountPath: mount},
		Status: secretsv1alpha1.PKIIssuerStatus{ResourceStatus: commonv1alpha1.ResourceStatus{
			ID: id, Conditions: pkiReadyCondition(),
		}},
	}
}

func pkiDuration(d time.Duration) *metav1.Duration { return &metav1.Duration{Duration: d} }

func fullPKIRole() *secretsv1alpha1.PKIRole {
	return &secretsv1alpha1.PKIRole{
		ObjectMeta: metav1.ObjectMeta{Name: "web", Namespace: "team"},
		Spec: secretsv1alpha1.PKIRoleSpec{
			ResourceSpec:                  pkiConnection(),
			MountPath:                     "pki",
			Issuer:                        &secretsv1alpha1.PKIIssuerReference{IssuerName: "root-a"},
			TTL:                           pkiDuration(24 * time.Hour),
			MaxTTL:                        pkiDuration(72 * time.Hour),
			AllowLocalhost:                new(false),
			AllowedDomains:                []string{"example.org", "example.com"},
			AllowedDomainsTemplate:        new(false),
			AllowGlobsInIdentityTemplates: new(false),
			AllowBareDomains:              new(true),
			AllowSubdomains:               new(true),
			AllowGlobDomains:              new(false),
			AllowWildcardCertificates:     new(false),
			AllowAnyName:                  new(false),
			EnforceHostnames:              new(true),
			AllowIPSANs:                   new(true),
			AllowedIPSANsCIDR:             []string{"192.168.0.0/16", "10.0.0.0/8"},
			AllowedURISANs:                []string{"spiffe://example.org/*"},
			AllowedURISANsTemplate:        new(false),
			AllowedOtherSANs:              []string{"1.3.6.1.4.1.311.20.2.3;UTF8:*"},
			AllowedSerialNumbers:          []string{"A*"},
			AllowedUserIDs:                []string{"u*"},
			ServerFlag:                    new(true),
			ClientFlag:                    new(false),
			CodeSigningFlag:               new(false),
			EmailProtectionFlag:           new(false),
			KeyType:                       "ec",
			KeyBits:                       new(int32(256)),
			SignatureBits:                 new(int32(384)),
			UsePSS:                        new(false),
			KeyUsage:                      []string{"KeyAgreement", "DigitalSignature"},
			ExtKeyUsage:                   []string{"ServerAuth"},
			ExtKeyUsageOIDs:               []string{"1.3.6.1.5.5.7.3.1"},
			UseCSRCommonName:              new(true),
			UseCSRSANs:                    new(false),
			OU:                            []string{"Platform"},
			Organization:                  []string{"Example"},
			Country:                       []string{"DE"},
			Locality:                      []string{"Berlin"},
			Province:                      []string{"BE"},
			StreetAddress:                 []string{"Main 1"},
			PostalCode:                    []string{"10115"},
			GenerateLease:                 new(false),
			NoStore:                       new(false),
			RequireCN:                     new(true),
			CNValidations:                 []string{"hostname"},
			PolicyIdentifiers:             []string{"1.3.6.1.4.1.44947.1.1.1"},
			BasicConstraintsValidForNonCA: new(false),
			NotBeforeDuration:             pkiDuration(time.Minute),
			NotBefore:                     "2026-01-01T00:00:00Z",
			NotBeforeBound:                "permit",
			NotAfter:                      "2030-01-01T00:00:00Z",
			NotAfterBound:                 "permit",
		},
	}
}

// goldenPKIRoleRead is `bao read -format=json pki/roles/web | jq .data` on OpenBao 2.7.2 after writing the
// full role above.
var goldenPKIRoleRead = map[string]any{
	"allow_any_name": false, "allow_bare_domains": true, "allow_glob_domains": false,
	"allow_globs_in_identity_templates": false, "allow_ip_sans": true, "allow_localhost": false,
	"allow_subdomains": true, "allow_token_displayname": false, "allow_wildcard_certificates": false,
	"allowed_domains": []any{"example.com", "example.org"}, "allowed_domains_template": false,
	"allowed_ip_sans_cidr": []any{"10.0.0.0/8", "192.168.0.0/16"}, "allowed_other_sans": []any{"1.3.6.1.4.1.311.20.2.3;UTF8:*"},
	"allowed_serial_numbers": []any{"A*"}, "allowed_uri_sans": []any{"spiffe://example.org/*"},
	"allowed_uri_sans_template": false, "allowed_user_ids": []any{"u*"}, "basic_constraints_valid_for_non_ca": false,
	"client_flag": false, "cn_validations": []any{"hostname"}, "code_signing_flag": false, "country": []any{"DE"},
	"email_protection_flag": false, "enforce_hostnames": true, "ext_key_usage": []any{"ServerAuth"},
	"ext_key_usage_oids": []any{"1.3.6.1.5.5.7.3.1"}, "generate_lease": false, "issuer_ref": "root-a",
	"key_bits": 256, "key_type": "ec", "key_usage": []any{"DigitalSignature", "KeyAgreement"},
	"locality": []any{"Berlin"}, "max_ttl": 259200, "no_store": false, "not_after": "2030-01-01T00:00:00Z",
	"not_after_bound": "permit", "not_before": "2026-01-01T00:00:00Z", "not_before_bound": "permit",
	"not_before_duration": 60, "organization": []any{"Example"}, "ou": []any{"Platform"},
	"policy_identifiers": []any{"1.3.6.1.4.1.44947.1.1.1"}, "postal_code": []any{"10115"}, "province": []any{"BE"},
	"require_cn": true, "server_flag": true, "signature_bits": 384, "street_address": []any{"Main 1"},
	"ttl": 86400, "use_csr_common_name": true, "use_csr_sans": false, "use_pss": false,
}

func TestPKIRolePathAndDesired(t *testing.T) {
	a := PKIRoleAdapter{}
	role := fullPKIRole()
	if got := a.Path(role); got != "pki/roles/web" {
		t.Errorf("Path = %q", got)
	}
	role.Spec.Name, role.Spec.MountPath = "edge", ""
	if got := a.Path(role); got != "pki/roles/edge" {
		t.Errorf("Path with name and default mount = %q", got)
	}

	desired, err := a.Desired(context.Background(), pkiFakeResolver{}, fullPKIRole())
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"issuer_ref", "ttl", "max_ttl", "key_bits", "not_after_bound", "policy_identifiers"} {
		if _, ok := desired[key]; !ok {
			t.Errorf("desired has no %s", key)
		}
	}
	if desired["ttl"] != int64(86400) || desired["issuer_ref"] != "root-a" {
		t.Errorf("ttl = %v, issuer_ref = %v", desired["ttl"], desired["issuer_ref"])
	}
	if diff := engine.Diff(desired, goldenPKIRoleRead, a.Fields()); len(diff) != 0 {
		t.Errorf("steady state differs on %v", diff)
	}

	changed := maps.Clone(goldenPKIRoleRead)
	changed["allowed_domains"] = []any{"example.com"}
	changed["max_ttl"] = 3600
	if diff := engine.Diff(desired, changed, a.Fields()); !slices.Equal(diff, []string{"allowed_domains", "max_ttl"}) {
		t.Errorf("Diff = %v, want allowed_domains and max_ttl", diff)
	}

	minimal := &secretsv1alpha1.PKIRole{ObjectMeta: metav1.ObjectMeta{Name: "m"}, Spec: secretsv1alpha1.PKIRoleSpec{ResourceSpec: pkiConnection()}}
	if desired, _ := a.Desired(context.Background(), pkiFakeResolver{}, minimal); len(desired) != len(goldenPKIRoleRead)-1 {
		t.Errorf("every role field except allow_token_displayname must be sent, an unset one at its default: %v", desired)
	}
	minimal.Spec.KeyType = "ec"
	if desired, _ := a.Desired(context.Background(), pkiFakeResolver{}, minimal); desired["key_bits"] != int64(256) {
		t.Errorf("key_bits of an ec role without keyBits = %v, want the OpenBao default 256", desired["key_bits"])
	}
}

func TestPKIIssuerReferenceResolution(t *testing.T) {
	ctx := context.Background()
	role := fullPKIRole()
	role.Spec.Issuer = &secretsv1alpha1.PKIIssuerReference{IssuerRef: &commonv1alpha1.LocalObjectReference{Name: "root"}}

	resolver := pkiFakeResolver{objects: map[string]client.Object{"root": pkiReadyIssuerObject("pki", "id-1")}}
	desired, err := PKIRoleAdapter{}.Desired(ctx, resolver, role)
	if err != nil || desired["issuer_ref"] != "id-1" {
		t.Fatalf("issuer_ref = %v, err = %v", desired["issuer_ref"], err)
	}

	notReady := pkiReadyIssuerObject("pki", "")
	if _, err := (PKIRoleAdapter{}).Desired(ctx, pkiFakeResolver{objects: map[string]client.Object{"root": notReady}}, role); !errors.Is(err, engine.ErrReferenceNotReady) {
		t.Errorf("issuer without id: err = %v, want ErrReferenceNotReady", err)
	}
	if _, err := (PKIRoleAdapter{}).Desired(ctx, pkiFakeResolver{}, role); !errors.Is(err, engine.ErrReferenceNotReady) {
		t.Errorf("missing issuer: err = %v, want ErrReferenceNotReady", err)
	}
	other := pkiFakeResolver{objects: map[string]client.Object{"root": pkiReadyIssuerObject("pki-other", "id-1")}}
	if _, err := (PKIRoleAdapter{}).Desired(ctx, other, role); err == nil || errors.Is(err, engine.ErrReferenceNotReady) {
		t.Errorf("issuer in another mount: err = %v, want a spec error", err)
	}
}

func TestPKIACMEConfigSteadyState(t *testing.T) {
	a := PKIACMEConfigAdapter{}
	cfg := &secretsv1alpha1.PKIACMEConfig{
		ObjectMeta: metav1.ObjectMeta{Name: "acme"},
		Spec: secretsv1alpha1.PKIACMEConfigSpec{
			ResourceSpec:           pkiConnection(),
			Enabled:                new(true),
			AllowedIssuers:         []string{"root-a"},
			AllowedRoles:           []string{"web"},
			AllowRoleExtKeyUsage:   new(true),
			DefaultDirectoryPolicy: "role:web",
			DNSResolver:            new("10.0.0.53:53"),
			EABPolicy:              "not-required",
		},
	}
	if got := a.Path(cfg); got != "pki/config/acme" {
		t.Errorf("Path = %q", got)
	}
	desired, err := a.Desired(context.Background(), pkiFakeResolver{}, cfg)
	if err != nil {
		t.Fatal(err)
	}
	golden := map[string]any{
		"allow_role_ext_key_usage": true, "allowed_issuers": []any{"root-a"}, "allowed_roles": []any{"web"},
		"default_directory_policy": "role:web", "dns_resolver": "10.0.0.53:53", "eab_policy": "not-required", "enabled": true,
	}
	if len(desired) != len(golden) {
		t.Errorf("desired has %d keys, want %d: %v", len(desired), len(golden), desired)
	}
	if diff := engine.Diff(desired, golden, a.Fields()); len(diff) != 0 {
		t.Errorf("steady state differs on %v", diff)
	}
	golden["enabled"] = false
	if diff := engine.Diff(desired, golden, a.Fields()); !slices.Equal(diff, []string{"enabled"}) {
		t.Errorf("Diff = %v, want enabled", diff)
	}
}

func TestPKIAutoTidySteadyState(t *testing.T) {
	a := PKIAutoTidyAdapter{}
	tidy := &secretsv1alpha1.PKIAutoTidy{
		ObjectMeta: metav1.ObjectMeta{Name: "tidy"},
		Spec: secretsv1alpha1.PKIAutoTidySpec{
			ResourceSpec:                         pkiConnection(),
			MountPath:                            "pki",
			Enabled:                              new(true),
			IntervalDuration:                     pkiDuration(time.Hour),
			TidyCertStore:                        new(true),
			TidyRevokedCerts:                     new(true),
			TidyInvalidCerts:                     new(true),
			TidyRevokedCertIssuerAssociations:    new(true),
			TidyExpiredIssuers:                   new(true),
			TidyMoveLegacyCABundle:               new(true),
			TidyACME:                             new(true),
			SafetyBuffer:                         pkiDuration(72 * time.Hour),
			RevokedSafetyBuffer:                  pkiDuration(48 * time.Hour),
			IssuerSafetyBuffer:                   pkiDuration(8760 * time.Hour),
			ACMEAccountSafetyBuffer:              pkiDuration(720 * time.Hour),
			PageSize:                             new(int32(500)),
			PauseDuration:                        pkiDuration(time.Second),
			MaintainStoredCertificateCounts:      new(true),
			PublishStoredCertificateCountMetrics: new(true),
		},
	}
	if got := a.Path(tidy); got != "pki/config/auto-tidy" {
		t.Errorf("Path = %q", got)
	}
	desired, err := a.Desired(context.Background(), pkiFakeResolver{}, tidy)
	if err != nil {
		t.Fatal(err)
	}
	golden := map[string]any{
		"acme_account_safety_buffer": 2592000, "enabled": true, "interval_duration": 3600, "issuer_safety_buffer": 31536000,
		"maintain_stored_certificate_counts": true, "page_size": 500, "pause_duration": "1s",
		"publish_stored_certificate_count_metrics": true, "revoked_safety_buffer": 172800, "safety_buffer": 259200,
		"tidy_acme": true, "tidy_cert_store": true, "tidy_expired_issuers": true, "tidy_invalid_certs": true,
		"tidy_move_legacy_ca_bundle": true, "tidy_revoked_cert_issuer_associations": true, "tidy_revoked_certs": true,
	}
	if len(desired) != len(golden) {
		t.Errorf("desired has %d keys, want %d", len(desired), len(golden))
	}
	if diff := engine.Diff(desired, golden, a.Fields()); len(diff) != 0 {
		t.Errorf("steady state differs on %v", diff)
	}
	golden["interval_duration"] = 7200
	if diff := engine.Diff(desired, golden, a.Fields()); !slices.Equal(diff, []string{"interval_duration"}) {
		t.Errorf("Diff = %v, want interval_duration", diff)
	}
}

func fullPKIConfig() *secretsv1alpha1.PKIConfig {
	return &secretsv1alpha1.PKIConfig{
		ObjectMeta: metav1.ObjectMeta{Name: "pki", Namespace: "team"},
		Spec: secretsv1alpha1.PKIConfigSpec{
			ResourceSpec: pkiConnection(),
			MountPath:    "pki",
			URLs: &secretsv1alpha1.PKIURLsConfig{
				IssuingCertificates:        []string{"http://openbao.example.com/v1/pki/ca"},
				CRLDistributionPoints:      []string{"http://openbao.example.com/v1/pki/crl"},
				DeltaCRLDistributionPoints: []string{"http://openbao.example.com/v1/pki/crl/delta"},
				OCSPServers:                []string{"http://openbao.example.com/v1/pki/ocsp"},
				EnableTemplating:           new(false),
			},
			CRL: &secretsv1alpha1.PKICRLConfig{
				Expiry:                     pkiDuration(48 * time.Hour),
				Disable:                    new(false),
				OCSPDisable:                new(false),
				OCSPExpiry:                 pkiDuration(time.Hour),
				AutoRebuild:                new(true),
				AutoRebuildGracePeriod:     pkiDuration(12 * time.Hour),
				EnableDelta:                new(true),
				DeltaRebuildInterval:       pkiDuration(15 * time.Minute),
				AllowExpiredCertRevocation: new(false),
			},
			Cluster: &secretsv1alpha1.PKIClusterConfig{
				Path:    "https://openbao.example.com/v1/pki",
				AIAPath: "http://openbao.example.com/v1/pki",
			},
			Issuers: &secretsv1alpha1.PKIIssuersConfig{
				Default:                    secretsv1alpha1.PKIIssuerReference{IssuerName: "root-a"},
				DefaultFollowsLatestIssuer: new(false),
			},
		},
	}
}

// goldenPKIConfigReads are the reads of pki/config/* on OpenBao 2.7.2 after writing fullPKIConfig; the CRL
// durations are stored as written ("48h0m0s").
func goldenPKIConfigReads() map[string]map[string]any {
	return map[string]map[string]any{
		"pki/config/cluster": {"aia_path": "http://openbao.example.com/v1/pki", "path": "https://openbao.example.com/v1/pki"},
		"pki/config/urls": {
			"crl_distribution_points": []any{"http://openbao.example.com/v1/pki/crl"}, "delta_crl_distribution_points": []any{"http://openbao.example.com/v1/pki/crl/delta"},
			"enable_templating": false, "issuing_certificates": []any{"http://openbao.example.com/v1/pki/ca"}, "ocsp_servers": []any{"http://openbao.example.com/v1/pki/ocsp"},
		},
		"pki/config/crl": {
			"allow_expired_cert_revocation": false, "auto_rebuild": true, "auto_rebuild_grace_period": "12h", "delta_rebuild_interval": "15m0s",
			"disable": false, "enable_delta": true, "expiry": "48h0m0s", "ocsp_disable": false, "ocsp_expiry": "1h0m0s",
		},
		"pki/config/issuers": {"default": "116ce568-c573-0ca6-52c1-2a1eb7d5d41a", "default_follows_latest_issuer": false},
		"pki/issuer/root-a":  {"issuer_id": "116ce568-c573-0ca6-52c1-2a1eb7d5d41a", "issuer_name": "root-a"},
	}
}

func TestPKIConfigSteadyStateAndWrite(t *testing.T) {
	ctx := context.Background()
	a := PKIConfigAdapter{}
	cfg := fullPKIConfig()
	desired, err := a.Desired(ctx, pkiFakeResolver{}, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if desired["crl.expiry"] != "48h0m0s" || desired["issuers.default"] != "root-a" || desired["cluster.path"] == nil {
		t.Errorf("desired = %v", desired)
	}

	bao := &pkiFakeClient{reads: goldenPKIConfigReads()}
	observed, found, err := a.Observe(ctx, bao, cfg)
	if err != nil || !found {
		t.Fatalf("Observe: found=%v err=%v", found, err)
	}
	if observed["issuers.default"] != "root-a" {
		t.Errorf("default issuer ID was not mapped to the spec name: %v", observed["issuers.default"])
	}
	if diff := engine.Diff(desired, observed, a.Fields()); len(diff) != 0 {
		t.Errorf("steady state differs on %v", diff)
	}

	bao.reads["pki/config/issuers"]["default"] = "another-id"
	bao.reads["pki/config/crl"]["expiry"] = "72h"
	observed, _, _ = a.Observe(ctx, bao, cfg)
	if diff := engine.Diff(desired, observed, a.Fields()); !slices.Equal(diff, []string{"crl.expiry", "issuers.default"}) {
		t.Errorf("Diff = %v, want crl.expiry and issuers.default", diff)
	}

	if _, err := a.Write(ctx, bao, cfg, desired, false); err != nil {
		t.Fatal(err)
	}
	if got := bao.writePaths(); !slices.Equal(got, []string{"pki/config/crl", "pki/config/issuers"}) {
		t.Errorf("write order = %v", got)
	}
	if body := bao.writes[1].data; body["default"] != "root-a" || len(body) != 2 {
		t.Errorf("issuers body = %v", body)
	}

	only := &secretsv1alpha1.PKIConfig{Spec: secretsv1alpha1.PKIConfigSpec{ResourceSpec: pkiConnection(),
		Cluster: &secretsv1alpha1.PKIClusterConfig{Path: "https://openbao.example.com/v1/pki"}}}
	reads := &pkiFakeClient{reads: goldenPKIConfigReads()}
	observed, _, _ = a.Observe(ctx, reads, only)
	for key := range observed {
		if strings.HasPrefix(key, "issuers.") {
			t.Errorf("Observe read the unset issuers section: %s", key)
		}
	}
	desired, _ = a.Desired(ctx, nil, only)
	diff := engine.Diff(desired, observed, a.Fields())
	if !slices.Contains(diff, "urls.ocsp_servers") || !slices.Contains(diff, "crl.enable_delta") || slices.Contains(diff, "cluster.path") {
		t.Errorf("Diff = %v: unset sections must report reads away from their defaults", diff)
	}
}

func fullPKIIssuer() *secretsv1alpha1.PKIIssuer {
	return &secretsv1alpha1.PKIIssuer{
		ObjectMeta: metav1.ObjectMeta{Name: "root-a", Namespace: "team"},
		Spec: secretsv1alpha1.PKIIssuerSpec{
			ResourceSpec: pkiConnection(),
			MountPath:    "pki",
			Type:         secretsv1alpha1.PKIIssuerTypeRoot,
			Key: &secretsv1alpha1.PKIIssuerKey{
				Source: secretsv1alpha1.PKIKeySourceInternal, Name: "root-a-key", Type: "rsa", Bits: new(int32(2048)),
			},
			Certificate: secretsv1alpha1.PKIIssuerCertificate{
				CommonName: "Example Root CA", AltNames: []string{"ca.example.com", "pki@example.com"},
				Organization: []string{"Example"}, Country: []string{"DE"}, TTL: pkiDuration(87600 * time.Hour),
				MaxPathLength: new(int32(1)), PermittedDNSDomains: []string{"example.com"}, SignatureBits: new(int32(256)),
			},
			LeafNotAfterBehavior:         "truncate",
			Usage:                        []string{"issuing-certificates", "crl-signing"},
			ManualChain:                  []string{"root-a"},
			RevocationSignatureAlgorithm: "SHA256WithRSA",
			IssuingCertificates:          []string{"http://openbao.example.com/v1/pki/issuer/root-a/der"},
			CRLDistributionPoints:        []string{"http://openbao.example.com/v1/pki/issuer/root-a/crl/der"},
			OCSPServers:                  []string{"http://openbao.example.com/v1/pki/ocsp"},
			EnableAIAURLTemplating:       new(false),
		},
	}
}

// goldenPKIIssuerRead is `bao read -format=json pki/issuer/root-a | jq .data` on OpenBao 2.7.2 after writing the
// settings of fullPKIIssuer, without the PEM fields.
func goldenPKIIssuerRead() map[string]any {
	return map[string]any{
		"ca_chain": []any{"<pem>"}, "crl_distribution_points": []any{"http://openbao.example.com/v1/pki/issuer/root-a/crl/der"},
		"delta_crl_distribution_points": nil, "enable_aia_url_templating": false,
		"issuer_id": "116ce568-c573-0ca6-52c1-2a1eb7d5d41a", "issuer_name": "root-a",
		"issuing_certificates": []any{"http://openbao.example.com/v1/pki/issuer/root-a/der"},
		"key_id":               "2382070d-4661-ca19-40fa-a3fa4e195497", "leaf_not_after_behavior": "truncate",
		"manual_chain": []any{"116ce568-c573-0ca6-52c1-2a1eb7d5d41a"}, "ocsp_servers": []any{"http://openbao.example.com/v1/pki/ocsp"},
		"revocation_signature_algorithm": "SHA256WithRSA", "revoked": false, "usage": "crl-signing,issuing-certificates,read-only",
	}
}

func TestPKIIssuerSteadyState(t *testing.T) {
	ctx := context.Background()
	a := PKIIssuerAdapter{}
	issuer := fullPKIIssuer()
	issuer.Status.ID = "116ce568-c573-0ca6-52c1-2a1eb7d5d41a"
	if got := a.Path(issuer); got != "pki/issuer/root-a" {
		t.Errorf("Path = %q", got)
	}
	desired, err := a.Desired(ctx, pkiFakeResolver{}, issuer)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(desired["usage"].([]string), []string{"crl-signing", "issuing-certificates", "read-only"}) {
		t.Errorf("usage = %v, want read-only added", desired["usage"])
	}
	for key := range desired {
		if strings.HasPrefix(key, "_") {
			t.Errorf("created issuer still resolves create-time key %s", key)
		}
	}

	bao := &pkiFakeClient{reads: map[string]map[string]any{
		"pki/issuer/116ce568-c573-0ca6-52c1-2a1eb7d5d41a": goldenPKIIssuerRead(),
		"pki/issuer/root-a": goldenPKIIssuerRead(),
	}}
	observed, found, err := a.Observe(ctx, bao, issuer)
	if err != nil || !found {
		t.Fatalf("Observe: found=%v err=%v", found, err)
	}
	if diff := engine.Diff(desired, observed, a.Fields()); len(diff) != 0 {
		t.Errorf("steady state differs on %v", diff)
	}

	tampered := goldenPKIIssuerRead()
	tampered["usage"] = "crl-signing,read-only"
	tampered["revocation_signature_algorithm"] = "SHA512WithRSA"
	bao.reads["pki/issuer/116ce568-c573-0ca6-52c1-2a1eb7d5d41a"] = tampered
	observed, _, _ = a.Observe(ctx, bao, issuer)
	if diff := engine.Diff(desired, observed, a.Fields()); !slices.Equal(diff, []string{"revocation_signature_algorithm", "usage"}) {
		t.Errorf("Diff = %v", diff)
	}

	absent := fullPKIIssuer()
	absent.Status.ID = ""
	absent.Name, absent.Spec.Name = "gone", ""
	if _, found, err := a.Observe(ctx, &pkiFakeClient{}, absent); found || err != nil {
		t.Errorf("missing issuer: found=%v err=%v, want absent", found, err)
	}
}

// TestPKIIssuerUpdateKeepsName guards against an issuer write without issuer_name, which clears the name in
// OpenBao 2.7 and breaks every reference to the issuer by name.
func TestPKIIssuerUpdateKeepsName(t *testing.T) {
	ctx := context.Background()
	a := PKIIssuerAdapter{}
	issuer := fullPKIIssuer()
	issuer.Status.ID = "116ce568-c573-0ca6-52c1-2a1eb7d5d41a"
	drifted := goldenPKIIssuerRead()
	drifted["issuer_name"] = ""
	drifted["leaf_not_after_behavior"] = "permit"
	bao := &pkiFakeClient{reads: map[string]map[string]any{"pki/issuer/" + issuer.Status.ID: drifted}}

	desired, err := a.Desired(ctx, pkiFakeResolver{}, issuer)
	if err != nil {
		t.Fatal(err)
	}
	for _, payload := range []engine.Payload{desired, {"leaf_not_after_behavior": "truncate"}} {
		bao.writes = nil
		if _, err := a.Write(ctx, bao, issuer, payload, false); err != nil {
			t.Fatal(err)
		}
		if len(bao.writes) != 1 || bao.writes[0].path != "pki/issuer/"+issuer.Status.ID {
			t.Fatalf("writes = %v", bao.writePaths())
		}
		if body := bao.writes[0].data; body["issuer_name"] != "root-a" || body["leaf_not_after_behavior"] != "truncate" {
			t.Errorf("update body = %v, want issuer_name root-a", body)
		}
	}
}

func TestPKIIssuerCreateRoot(t *testing.T) {
	ctx := context.Background()
	a := PKIIssuerAdapter{}
	issuer := fullPKIIssuer()
	issuer.Spec.Key.Source = secretsv1alpha1.PKIKeySourceExported
	issuer.Spec.Key.PrivateKeyFormat = "pkcs8"
	issuer.Spec.WriteSecretRef = &commonv1alpha1.LocalObjectReference{Name: "root-key"}
	desired, err := a.Desired(ctx, pkiFakeResolver{}, issuer)
	if err != nil {
		t.Fatal(err)
	}
	generated := goldenPKIIssuerRead()
	generated["usage"] = "crl-signing,issuing-certificates,ocsp-signing,read-only"
	bao := &pkiFakeClient{
		reads: map[string]map[string]any{"pki/issuer/id-1": generated},
		responses: map[string]map[string]any{"pki/root/generate/exported": {
			"issuer_id": "id-1", "key_id": "key-1", "private_key": "-----BEGIN PRIVATE KEY-----", "private_key_type": "rsa",
		}},
	}
	response, err := a.Write(ctx, bao, issuer, desired, true)
	if err != nil {
		t.Fatal(err)
	}
	if issuer.Status.ID != "id-1" || issuer.Status.KeyID != "key-1" {
		t.Errorf("status = %+v", issuer.Status)
	}
	if got := bao.writePaths(); !slices.Equal(got, []string{"pki/root/generate/exported", "pki/issuer/id-1"}) {
		t.Fatalf("writes = %v", got)
	}
	gen := bao.writes[0].data
	for key, want := range map[string]any{
		"common_name": "Example Root CA", "issuer_name": "root-a", "key_name": "root-a-key", "key_type": "rsa",
		"key_bits": int64(2048), "ttl": int64(315360000), "max_path_length": int64(1), "format": "pem",
		"alt_names": "ca.example.com,pki@example.com", "private_key_format": "pkcs8", "signature_bits": int64(256),
	} {
		if !reflect.DeepEqual(gen[key], want) {
			t.Errorf("generate %s = %#v, want %#v", key, gen[key], want)
		}
	}
	if config := bao.writes[1].data; config["leaf_not_after_behavior"] != "truncate" || config["issuer_name"] != "root-a" {
		t.Errorf("config write = %v", config)
	}

	name, data, ok := a.Outputs(issuer, response)
	if !ok || name != "root-key" || string(data["private_key"]) != "-----BEGIN PRIVATE KEY-----" {
		t.Errorf("Outputs = %q %v %v", name, data, ok)
	}
	if _, _, ok := a.Outputs(issuer, nil); ok {
		t.Error("Outputs wrote a Secret for a response without a private key")
	}
}

func TestPKIIssuerCreateIntermediateInSteps(t *testing.T) {
	ctx := context.Background()
	a := PKIIssuerAdapter{}
	issuer := &secretsv1alpha1.PKIIssuer{
		ObjectMeta: metav1.ObjectMeta{Name: "int-a", Namespace: "team"},
		Spec: secretsv1alpha1.PKIIssuerSpec{
			ResourceSpec: pkiConnection(),
			MountPath:    "pki-int",
			Type:         secretsv1alpha1.PKIIssuerTypeIntermediate,
			Certificate: secretsv1alpha1.PKIIssuerCertificate{
				CommonName: "Example Intermediate", TTL: pkiDuration(43800 * time.Hour), AddBasicConstraints: new(true),
			},
			Signer: &secretsv1alpha1.PKIIssuerSigner{IssuerRef: &commonv1alpha1.LocalObjectReference{Name: "root"}},
		},
	}
	resolver := pkiFakeResolver{objects: map[string]client.Object{"root": pkiReadyIssuerObject("pki", "root-id")}}
	desired, err := a.Desired(ctx, resolver, issuer)
	if err != nil {
		t.Fatal(err)
	}
	if desired[pkiSignerMountKey] != "pki" || desired[pkiSignerIssuerKey] != "root-id" {
		t.Fatalf("signer not resolved: %v", desired)
	}

	bao := &pkiFakeClient{
		reads: map[string]map[string]any{"pki-int/issuer/int-id": {"issuer_id": "int-id", "issuer_name": "", "usage": "read-only"}},
		responses: map[string]map[string]any{
			"pki-int/intermediate/generate/internal": {"csr": "CSR", "key_id": "key-1"},
			"pki/issuer/root-id/sign-intermediate":   {"certificate": "INT\n", "ca_chain": []any{"INT", "ROOT"}},
			"pki-int/intermediate/set-signed":        {"mapping": map[string]any{"root-copy": "", "int-id": "key-1"}},
		},
	}
	if _, err := a.Write(ctx, bao, issuer, desired, true); err != nil {
		t.Fatal(err)
	}
	if issuer.Status.CSR != "CSR" || issuer.Status.KeyID != "key-1" || issuer.Status.ID != "" {
		t.Fatalf("after the CSR step status = %+v", issuer.Status)
	}
	if got := bao.writePaths(); !slices.Equal(got, []string{"pki-int/intermediate/generate/internal"}) {
		t.Fatalf("CSR step writes = %v", got)
	}
	if bao.writes[0].data["add_basic_constraints"] != true || bao.writes[0].data["ttl"] != nil {
		t.Errorf("CSR payload = %v", bao.writes[0].data)
	}
	if err := a.WriteStatus(ctx, bao, issuer, nil); !errors.Is(err, engine.ErrInProgress) {
		t.Errorf("WriteStatus while unsigned: err = %v, want ErrInProgress", err)
	}

	if _, err := a.Write(ctx, bao, issuer, desired, true); err != nil {
		t.Fatal(err)
	}
	want := []string{"pki-int/intermediate/generate/internal", "pki/issuer/root-id/sign-intermediate",
		"pki-int/intermediate/set-signed", "pki-int/issuer/int-id"}
	if got := bao.writePaths(); !slices.Equal(got, want) {
		t.Fatalf("writes = %v, want %v", got, want)
	}
	if sign := bao.writes[1].data; sign["csr"] != "CSR" || sign["ttl"] != int64(157680000) || sign["common_name"] != "Example Intermediate" {
		t.Errorf("sign payload = %v", sign)
	}
	if chain := bao.writes[2].data["certificate"]; chain != "INT\nROOT" {
		t.Errorf("set-signed certificate = %q", chain)
	}
	if name := bao.writes[3].data; name["issuer_name"] != "int-a" ||
		!slices.Equal(pkiStrings(name["usage"]), []string{"crl-signing", "issuing-certificates", "ocsp-signing", "read-only"}) {
		t.Errorf("naming write = %v", name)
	}
	if issuer.Status.ID != "int-id" || issuer.Status.CSR != "" {
		t.Errorf("final status = %+v", issuer.Status)
	}

	issuer.Status.ID = "int-id"
	desired, _ = a.Desired(ctx, pkiFakeResolver{}, issuer)
	if _, ok := desired[pkiSignerMountKey]; ok {
		t.Error("a created intermediate still resolves its signer")
	}
}

func TestPKIIssuerDelete(t *testing.T) {
	ctx := context.Background()
	issuer := fullPKIIssuer()
	issuer.Status.ID, issuer.Status.KeyID = "id-1", "key-1"
	bao := &pkiFakeClient{}
	if err := (PKIIssuerAdapter{}).Delete(ctx, bao, issuer); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(bao.deletes, []string{"pki/issuer/id-1", "pki/key/key-1"}) {
		t.Errorf("deletes = %v", bao.deletes)
	}

	issuer.Spec.Key = &secretsv1alpha1.PKIIssuerKey{Source: secretsv1alpha1.PKIKeySourceExisting,
		Existing: &secretsv1alpha1.PKIKeyReference{KeyName: "shared"}}
	bao = &pkiFakeClient{}
	if err := (PKIIssuerAdapter{}).Delete(ctx, bao, issuer); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(bao.deletes, []string{"pki/issuer/id-1"}) {
		t.Errorf("an existing key was deleted: %v", bao.deletes)
	}
}

func TestPKIKeyAdapter(t *testing.T) {
	ctx := context.Background()
	a := PKIKeyAdapter{}
	key := &secretsv1alpha1.PKIKey{
		ObjectMeta: metav1.ObjectMeta{Name: "imported", Namespace: "team"},
		Spec: secretsv1alpha1.PKIKeySpec{
			ResourceSpec:       pkiConnection(),
			Type:               secretsv1alpha1.PKIKeyCreationImport,
			PEMBundleSecretRef: &commonv1alpha1.SecretKeySelector{Name: "ca-key", Key: "tls.key"},
		},
	}
	if got := a.Path(key); got != "pki/key/imported" {
		t.Errorf("Path = %q", got)
	}
	if refs := a.SecretRefs(key); !slices.Equal(refs, []string{"ca-key"}) {
		t.Errorf("SecretRefs = %v", refs)
	}
	if _, err := a.Desired(ctx, pkiFakeResolver{}, key); !errors.Is(err, engine.ErrReferenceNotReady) {
		t.Errorf("missing Secret: err = %v", err)
	}
	resolver := pkiFakeResolver{secrets: map[string]map[string][]byte{"ca-key": {"tls.key": []byte("PEM-ONE")}}}
	desired, err := a.Desired(ctx, resolver, key)
	if err != nil {
		t.Fatal(err)
	}
	golden := map[string]any{"key_id": "key-1", "key_name": "imported", "key_type": "ec", "subject_key_id": "aa:bb"}
	if diff := engine.Diff(desired, golden, a.Fields()); len(diff) != 0 {
		t.Errorf("steady state differs on %v", diff)
	}
	first := engine.WriteOnlyHash(pkiHashKey, desired, a.Fields())
	resolver.secrets["ca-key"]["tls.key"] = []byte("PEM-TWO")
	rotated, _ := a.Desired(ctx, resolver, key)
	if first == "" || first == engine.WriteOnlyHash(pkiHashKey, rotated, a.Fields()) {
		t.Error("write-only hash did not follow the Secret")
	}

	bao := &pkiFakeClient{responses: map[string]map[string]any{"pki/keys/import": {"key_id": "key-1"}}}
	if _, err := a.Write(ctx, bao, key, desired, true); err != nil {
		t.Fatal(err)
	}
	if bao.writes[0].path != "pki/keys/import" || bao.writes[0].data["pem_bundle"] != "PEM-ONE" || key.Status.ID != "key-1" {
		t.Errorf("create write = %+v, status id %q", bao.writes[0], key.Status.ID)
	}
	if _, err := a.Write(ctx, bao, key, engine.StripCreateOnly(desired, a.Fields()), false); err != nil {
		t.Fatal(err)
	}
	if w := bao.writes[1]; w.path != "pki/key/key-1" || !reflect.DeepEqual(w.data, map[string]any{"key_name": "imported"}) {
		t.Errorf("update write = %+v", w)
	}

	exported := &secretsv1alpha1.PKIKey{
		ObjectMeta: metav1.ObjectMeta{Name: "signing"},
		Spec: secretsv1alpha1.PKIKeySpec{ResourceSpec: pkiConnection(), MountPath: "pki-int", Type: secretsv1alpha1.PKIKeyCreationExported,
			KeyType: "ec", KeyBits: new(int32(384)), WriteSecretRef: &commonv1alpha1.LocalObjectReference{Name: "signing-key"}},
	}
	desired, _ = a.Desired(ctx, pkiFakeResolver{}, exported)
	bao = &pkiFakeClient{responses: map[string]map[string]any{"pki-int/keys/generate/exported": {
		"key_id": "key-2", "key_type": "ec", "private_key": "-----BEGIN EC PRIVATE KEY-----",
	}}}
	response, err := a.Write(ctx, bao, exported, desired, true)
	if err != nil {
		t.Fatal(err)
	}
	if w := bao.writes[0].data; w["key_type"] != "ec" || w["key_bits"] != int64(384) || w["key_name"] != "signing" {
		t.Errorf("generate payload = %v", w)
	}
	name, data, ok := a.Outputs(exported, response)
	if !ok || name != "signing-key" || string(data["key_type"]) != "ec" {
		t.Errorf("Outputs = %q %v %v", name, data, ok)
	}
}

func TestPKIIsMissing(t *testing.T) {
	missing := &openbao.ResponseError{StatusCode: 500, Errors: []string{"* unable to find PKI issuer for reference: x"}}
	if !pkiIsMissing(missing, "issuer") || pkiIsMissing(missing, "key") {
		t.Error("500 unable-to-find not matched per kind")
	}
	if pkiIsMissing(&openbao.ResponseError{StatusCode: 500, Errors: []string{"storage failure"}}, "issuer") {
		t.Error("a real 500 was treated as absent")
	}
	if pkiIsMissing(&openbao.ResponseError{StatusCode: 400}, "issuer") || !pkiIsMissing(openbao.ErrNotFound, "issuer") {
		t.Error("400 or 404 handled wrongly")
	}
}
