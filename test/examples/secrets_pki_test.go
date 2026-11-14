package examples

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// TestSecretsPKIExamples applies the PKI samples with a server-side dry run and checks the CEL rules of the PKI
// kinds against envtest.
func TestSecretsPKIExamples(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	k8s := secretsStartEnv(t, root, []string{
		"secrets.openbaoconfig.io_pkiissuers.yaml",
		"secrets.openbaoconfig.io_pkikeys.yaml",
		"secrets.openbaoconfig.io_pkiroles.yaml",
		"secrets.openbaoconfig.io_pkiconfigs.yaml",
		"secrets.openbaoconfig.io_pkiacmeconfigs.yaml",
		"secrets.openbaoconfig.io_pkiautotidies.yaml",
	})
	ctx := context.Background()
	if err := k8s.Create(ctx, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "openbao-config"}}); err != nil {
		t.Fatal(err)
	}

	for _, file := range []string{"pkiissuer.yaml", "pkikey.yaml", "pkirole.yaml", "pkiconfig.yaml", "pkiacmeconfig.yaml", "pkiautotidy.yaml"} {
		t.Run(file, func(t *testing.T) {
			for _, obj := range secretsDecode(t, filepath.Join(root, "examples", "secrets", file)) {
				if err := k8s.Create(ctx, obj, client.DryRunAll, client.FieldValidation("Strict")); err != nil {
					t.Errorf("%s %s: %v", obj.GetKind(), obj.GetName(), err)
				}
			}
		})
	}

	const head = "apiVersion: secrets.openbaoconfig.io/v1alpha1\nmetadata: {name: x, namespace: openbao-config}\n"
	const conn = "connectionRef: {name: openbao}"
	const cert = "certificate: {commonName: ca}"
	t.Run("validation", func(t *testing.T) {
		for _, tt := range []struct {
			name, doc, wantErr string
		}{
			{name: "root", doc: "kind: PKIIssuer\nspec: {" + conn + ", type: root, " + cert + "}"},
			{name: "intermediate without signer", wantErr: "signer is required for an intermediate",
				doc: "kind: PKIIssuer\nspec: {" + conn + ", type: intermediate, " + cert + "}"},
			{name: "root with signer", wantErr: "signer is required for an intermediate and not allowed for a root",
				doc: "kind: PKIIssuer\nspec: {" + conn + ", type: root, " + cert + ", signer: {issuerName: r}}"},
			{name: "signer with both refs", wantErr: "set exactly one of issuerName and issuerRef",
				doc: "kind: PKIIssuer\nspec: {" + conn + ", type: intermediate, " + cert + ", signer: {issuerName: r, issuerRef: {name: r}}}"},
			{name: "signer mount with ref", wantErr: "mountPath applies only to issuerName",
				doc: "kind: PKIIssuer\nspec: {" + conn + ", type: intermediate, " + cert + ", signer: {mountPath: pki, issuerRef: {name: r}}}"},
			{name: "exported without secret", wantErr: "writeSecretRef is required with key source exported",
				doc: "kind: PKIIssuer\nspec: {" + conn + ", type: root, " + cert + ", key: {source: exported}}"},
			{name: "secret without export", wantErr: "writeSecretRef is required with key source exported",
				doc: "kind: PKIIssuer\nspec: {" + conn + ", type: root, " + cert + ", writeSecretRef: {name: s}}"},
			{name: "existing without key", wantErr: "existing is required with source existing",
				doc: "kind: PKIIssuer\nspec: {" + conn + ", type: root, " + cert + ", key: {source: existing}}"},
			{name: "existing with type", wantErr: "existing is required with source existing and excludes name, type and bits",
				doc: "kind: PKIIssuer\nspec: {" + conn + ", type: root, " + cert + ", key: {source: existing, type: ec, existing: {keyName: k}}}"},
			{name: "kms without ref", wantErr: "externalKeyRef is required with source kms",
				doc: "kind: PKIIssuer\nspec: {" + conn + ", type: root, " + cert + ", key: {source: kms}}"},
			{name: "pkcs8 on internal", wantErr: "privateKeyFormat applies only to source exported",
				doc: "kind: PKIIssuer\nspec: {" + conn + ", type: root, " + cert + ", key: {privateKeyFormat: pkcs8}}"},
			{name: "ttl and notAfter", wantErr: "set at most one of ttl and notAfter",
				doc: "kind: PKIIssuer\nspec: {" + conn + ", type: root, certificate: {commonName: ca, ttl: 1h, notAfter: '2030-01-01T00:00:00Z'}}"},
			{name: "basic constraints on root", wantErr: "addBasicConstraints applies only to an intermediate",
				doc: "kind: PKIIssuer\nspec: {" + conn + ", type: root, certificate: {commonName: ca, addBasicConstraints: true}}"},
			{name: "reserved issuer name", wantErr: "default is a reserved issuer name",
				doc: "kind: PKIIssuer\nspec: {" + conn + ", type: root, name: default, " + cert + "}"},
			{name: "revocation algorithm of another key type", wantErr: "revocationSignatureAlgorithm must match key.type",
				doc: "kind: PKIIssuer\nspec: {" + conn + ", type: root, " + cert + ", revocationSignatureAlgorithm: ECDSAWithSHA384}"},
			{name: "aia templating without urls", wantErr: "enableAIAURLTemplating requires at least one of issuingCertificates",
				doc: "kind: PKIIssuer\nspec: {" + conn + ", type: root, " + cert + ", enableAIAURLTemplating: true}"},
			{name: "key exported without secret", wantErr: "writeSecretRef is required with type exported",
				doc: "kind: PKIKey\nspec: {" + conn + ", type: exported}"},
			{name: "key import without secret ref", wantErr: "pemBundleSecretRef is required with type import",
				doc: "kind: PKIKey\nspec: {" + conn + ", type: import}"},
			{name: "key bits on import", wantErr: "keyType and keyBits apply only to type internal or exported",
				doc: "kind: PKIKey\nspec: {" + conn + ", type: import, keyBits: 256, pemBundleSecretRef: {name: s, key: k}}"},
			{name: "role ttl over max", wantErr: "ttl must not exceed maxTTL",
				doc: "kind: PKIRole\nspec: {" + conn + ", ttl: 2h, maxTTL: 1h}"},
			{name: "role bits on ed25519", wantErr: "is not allowed for ed25519 or any",
				doc: "kind: PKIRole\nspec: {" + conn + ", keyType: ed25519, keyBits: 256}"},
			{name: "role ec bits on default rsa", wantErr: "keyBits must be 2048, 3072, 4096 or 8192 for keyType rsa",
				doc: "kind: PKIRole\nspec: {" + conn + ", keyBits: 256}"},
			{name: "role lease without store", wantErr: "generateLease requires the certificate to be stored",
				doc: "kind: PKIRole\nspec: {" + conn + ", generateLease: true, noStore: true}"},
			{name: "role non-canonical cidr", wantErr: "entries must be canonical CIDRs",
				doc: "kind: PKIRole\nspec: {" + conn + ", allowedIPSANsCIDR: [10.1.2.3/8]}"},
			{name: "role two issuer refs", wantErr: "set exactly one of issuerName and issuerRef",
				doc: "kind: PKIRole\nspec: {" + conn + ", issuer: {issuerName: a, issuerRef: {name: b}}}"},
			{name: "config without section", wantErr: "set at least one of urls, crl, cluster and issuers",
				doc: "kind: PKIConfig\nspec: {" + conn + "}"},
			{name: "config delete", wantErr: "deletionPolicy must be Orphan",
				doc: "kind: PKIConfig\nspec: {" + conn + ", deletionPolicy: Delete, cluster: {path: 'https://bao/v1/pki'}}"},
			{name: "config issuers without default", wantErr: "spec.issuers.default",
				doc: "kind: PKIConfig\nspec: {" + conn + ", issuers: {defaultFollowsLatestIssuer: true}}"},
			{name: "config cluster path not a url", wantErr: "spec.cluster.path",
				doc: "kind: PKIConfig\nspec: {" + conn + ", cluster: {path: openbao}}"},
			{name: "acme wildcard with others", wantErr: "'*' in allowedRoles must be the only entry",
				doc: "kind: PKIACMEConfig\nspec: {" + conn + ", allowedRoles: ['*', web]}"},
			{name: "acme bad directory policy", wantErr: "spec.defaultDirectoryPolicy",
				doc: "kind: PKIACMEConfig\nspec: {" + conn + ", defaultDirectoryPolicy: allow}"},
			{name: "acme delete", wantErr: "deletionPolicy must be Orphan",
				doc: "kind: PKIACMEConfig\nspec: {" + conn + ", deletionPolicy: Delete}"},
			{name: "tidy enabled without operation", wantErr: "enabled requires at least one tidy operation",
				doc: "kind: PKIAutoTidy\nspec: {" + conn + ", enabled: true}"},
			{name: "tidy metrics without counts", wantErr: "publishStoredCertificateCountMetrics requires maintainStoredCertificateCounts",
				doc: "kind: PKIAutoTidy\nspec: {" + conn + ", publishStoredCertificateCountMetrics: true}"},
			{name: "tidy delete", wantErr: "deletionPolicy must be Orphan",
				doc: "kind: PKIAutoTidy\nspec: {" + conn + ", deletionPolicy: Delete}"},
		} {
			err := k8s.Create(ctx, secretsDecodeString(t, head+tt.doc), client.DryRunAll)
			switch {
			case tt.wantErr == "" && err != nil:
				t.Errorf("%s: %v", tt.name, err)
			case tt.wantErr != "" && (!apierrors.IsInvalid(err) || !strings.Contains(err.Error(), tt.wantErr)):
				t.Errorf("%s: got %v, want invalid with %q", tt.name, err, tt.wantErr)
			}
		}
	})

	t.Run("immutable", func(t *testing.T) {
		const root = "{" + conn + ", type: root, " + cert + "}"
		for _, tt := range []struct {
			kind, spec string
			change     map[string]any
			wantErr    string
		}{
			{kind: "PKIIssuer", spec: root, change: map[string]any{"mountPath": "other"}, wantErr: "mountPath is immutable"},
			{kind: "PKIIssuer", spec: root, change: map[string]any{"name": "other"}, wantErr: "name is immutable"},
			{kind: "PKIIssuer", spec: root, change: map[string]any{"type": "intermediate"}, wantErr: "type is immutable"},
			{kind: "PKIIssuer", spec: root, change: map[string]any{"certificate": map[string]any{"commonName": "other"}}, wantErr: "certificate is immutable"},
			{kind: "PKIIssuer", spec: root, change: map[string]any{"key": map[string]any{"type": "ec"}}, wantErr: "key is immutable"},
			{kind: "PKIIssuer", spec: root, change: map[string]any{"leafNotAfterBehavior": "truncate", "usage": []any{"issuing-certificates"}}},
			{kind: "PKIKey", spec: "{" + conn + ", keyType: ec}", change: map[string]any{"keyType": "rsa"}, wantErr: "keyType is immutable"},
			{kind: "PKIKey", spec: "{" + conn + "}", change: map[string]any{"type": "kms", "externalKeyRef": "k"}, wantErr: "type is immutable"},
			{kind: "PKIRole", spec: "{" + conn + "}", change: map[string]any{"name": "other"}, wantErr: "name is immutable"},
			{kind: "PKIRole", spec: "{" + conn + "}", change: map[string]any{"mountPath": "other"}, wantErr: "mountPath is immutable"},
			{kind: "PKIRole", spec: "{" + conn + "}", change: map[string]any{"maxTTL": "2h"}},
			{kind: "PKIConfig", spec: "{" + conn + ", crl: {expiry: 24h}}", change: map[string]any{"mountPath": "other"}, wantErr: "mountPath is immutable"},
			{kind: "PKIACMEConfig", spec: "{" + conn + "}", change: map[string]any{"mountPath": "other"}, wantErr: "mountPath is immutable"},
			{kind: "PKIAutoTidy", spec: "{" + conn + "}", change: map[string]any{"mountPath": "other"}, wantErr: "mountPath is immutable"},
		} {
			obj := secretsDecodeString(t, "apiVersion: secrets.openbaoconfig.io/v1alpha1\nkind: "+tt.kind+
				"\nmetadata: {generateName: immutable-, namespace: openbao-config}\nspec: "+tt.spec)
			if err := k8s.Create(ctx, obj); err != nil {
				t.Fatalf("%s %s: %v", tt.kind, tt.spec, err)
			}
			changed := obj.DeepCopy()
			for field, value := range tt.change {
				if err := unstructured.SetNestedField(changed.Object, value, "spec", field); err != nil {
					t.Fatal(err)
				}
			}
			err := k8s.Update(ctx, changed)
			switch {
			case tt.wantErr == "" && err != nil:
				t.Errorf("%s %v: %v", tt.kind, tt.change, err)
			case tt.wantErr != "" && (!apierrors.IsInvalid(err) || !strings.Contains(err.Error(), tt.wantErr)):
				t.Errorf("%s %v: got %v, want invalid with %q", tt.kind, tt.change, err, tt.wantErr)
			}
		}
	})
}
