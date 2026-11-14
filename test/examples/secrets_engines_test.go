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

// TestSecretsEngineExamples applies the TransitKey, SSHCA, SSHRole and TOTPKey samples with a server-side dry
// run and checks their CEL rules against envtest.
func TestSecretsEngineExamples(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	k8s := secretsStartEnv(t, root, []string{
		"secrets.openbaoconfig.io_transitkeys.yaml",
		"secrets.openbaoconfig.io_sshcas.yaml",
		"secrets.openbaoconfig.io_sshroles.yaml",
		"secrets.openbaoconfig.io_totpkeys.yaml",
	})
	ctx := context.Background()
	if err := k8s.Create(ctx, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "openbao-config"}}); err != nil {
		t.Fatal(err)
	}

	for _, file := range []string{"transitkey.yaml", "sshca.yaml", "sshrole.yaml", "totpkey.yaml"} {
		t.Run(file, func(t *testing.T) {
			for _, obj := range secretsDecode(t, filepath.Join(root, "examples", "secrets", file)) {
				if err := k8s.Create(ctx, obj, client.DryRunAll, client.FieldValidation("Strict")); err != nil {
					t.Errorf("%s %s: %v", obj.GetKind(), obj.GetName(), err)
				}
			}
		})
	}

	const head = "apiVersion: secrets.openbaoconfig.io/v1alpha1\nmetadata: {name: x, namespace: openbao-config}\n"
	t.Run("validation", func(t *testing.T) {
		for _, tt := range []struct {
			name, doc, wantErr string
		}{
			{name: "transit convergent without derived", wantErr: "convergentEncryption requires derived",
				doc: "kind: TransitKey\nspec: {connectionRef: {name: openbao}, convergentEncryption: true}"},
			{name: "transit short rotation", wantErr: "autoRotatePeriod must be 0 or at least 1h",
				doc: "kind: TransitKey\nspec: {connectionRef: {name: openbao}, autoRotatePeriod: 30m}"},
			{name: "transit rotation disabled", doc: "kind: TransitKey\nspec: {connectionRef: {name: openbao}, autoRotatePeriod: 0s}"},
			{name: "transit keySize on aes", wantErr: "keySize applies only to hmac",
				doc: "kind: TransitKey\nspec: {connectionRef: {name: openbao}, keySize: 64}"},
			{name: "transit external key without ref", wantErr: "externalKeyRef is required",
				doc: "kind: TransitKey\nspec: {connectionRef: {name: openbao}, type: external-key}"},
			{name: "transit min versions", wantErr: "minEncryptionVersion must be 0 or at least minDecryptionVersion",
				doc: "kind: TransitKey\nspec: {connectionRef: {name: openbao}, minDecryptionVersion: 3, minEncryptionVersion: 2}"},
			{name: "sshca public key alone", wantErr: "publicKey and privateKeySecretRef must be set together",
				doc: "kind: SSHCA\nspec: {connectionRef: {name: openbao}, publicKey: ssh-ed25519 AAAA}"},
			{name: "sshca import with keyType", wantErr: "keyType and keyBits apply only to a generated CA key",
				doc: "kind: SSHCA\nspec: {connectionRef: {name: openbao}, keyType: ed25519, publicKey: k, privateKeySecretRef: {name: s, key: k}}"},
			{name: "sshrole ca without certificate type", wantErr: "a ca role needs allowUserCertificates or allowHostCertificates",
				doc: "kind: SSHRole\nspec: {connectionRef: {name: openbao}, keyType: ca}"},
			{name: "sshrole otp without user", wantErr: "an otp role needs defaultUser",
				doc: "kind: SSHRole\nspec: {connectionRef: {name: openbao}, keyType: otp}"},
			{name: "sshrole port on ca", wantErr: "apply only to otp roles",
				doc: "kind: SSHRole\nspec: {connectionRef: {name: openbao}, keyType: ca, allowUserCertificates: true, port: 22}"},
			{name: "sshrole ttl on otp", wantErr: "certificate fields apply only to ca roles",
				doc: "kind: SSHRole\nspec: {connectionRef: {name: openbao}, keyType: otp, defaultUser: u, ttl: 1h}"},
			{name: "sshrole ttl over max", wantErr: "ttl must not exceed maxTTL",
				doc: "kind: SSHRole\nspec: {connectionRef: {name: openbao}, keyType: ca, allowUserCertificates: true, ttl: 2h, maxTTL: 1h}"},
			{name: "sshrole bad cidr", wantErr: "cidrList entries must be CIDR blocks",
				doc: "kind: SSHRole\nspec: {connectionRef: {name: openbao}, keyType: otp, defaultUser: u, cidrList: [nope]}"},
			{name: "totp no source", wantErr: "set exactly one of generate, keySecretRef and urlSecretRef",
				doc: "kind: TOTPKey\nspec: {connectionRef: {name: openbao}}"},
			{name: "totp two sources", wantErr: "set exactly one of generate, keySecretRef and urlSecretRef",
				doc: "kind: TOTPKey\nspec: {connectionRef: {name: openbao}, keySecretRef: {name: s, key: k}, urlSecretRef: {name: s, key: u}}"},
			{name: "totp generate without issuer", wantErr: "a generated key needs issuer and accountName",
				doc: "kind: TOTPKey\nspec: {connectionRef: {name: openbao}, generate: {writeSecretRef: {name: out}}}"},
			{name: "totp generate without output", wantErr: "writeSecretRef is required unless exported is false",
				doc: "kind: TOTPKey\nspec: {connectionRef: {name: openbao}, issuer: i, accountName: a, generate: {}}"},
			{name: "totp generate not exported", doc: "kind: TOTPKey\nspec: {connectionRef: {name: openbao}, issuer: i, accountName: a, generate: {exported: false}}"},
			{name: "totp url with issuer", wantErr: "come from the URL",
				doc: "kind: TOTPKey\nspec: {connectionRef: {name: openbao}, issuer: i, urlSecretRef: {name: s, key: u}}"},
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
		for _, tt := range []struct {
			kind, spec string
			change     map[string]any
			wantErr    string
		}{
			{kind: "TransitKey", spec: "{connectionRef: {name: openbao}}", change: map[string]any{"mountPath": "other"}, wantErr: "mountPath is immutable"},
			{kind: "TransitKey", spec: "{connectionRef: {name: openbao}}", change: map[string]any{"name": "other"}, wantErr: "name is immutable"},
			{kind: "TransitKey", spec: "{connectionRef: {name: openbao}}", change: map[string]any{"type": "rsa-2048"}, wantErr: "type is immutable"},
			{kind: "TransitKey", spec: "{connectionRef: {name: openbao}}", change: map[string]any{"derived": true}, wantErr: "derived is immutable"},
			{kind: "TransitKey", spec: "{connectionRef: {name: openbao}, exportable: true}", change: map[string]any{"exportable": false}, wantErr: "exportable cannot be disabled"},
			{kind: "TransitKey", spec: "{connectionRef: {name: openbao}}", change: map[string]any{"exportable": true, "deletionAllowed": true, "rotateNonce": "n"}},
			{kind: "SSHCA", spec: "{connectionRef: {name: openbao}, keyType: ed25519}", change: map[string]any{"keyType": "rsa"}, wantErr: "keyType is immutable"},
			{kind: "SSHCA", spec: "{connectionRef: {name: openbao}}", change: map[string]any{"publicKey": "k", "privateKeySecretRef": map[string]any{"name": "s", "key": "k"}}, wantErr: "switching between a generated and an imported CA key"},
			{kind: "SSHCA", spec: "{connectionRef: {name: openbao}, publicKey: k1, privateKeySecretRef: {name: s, key: k}}", change: map[string]any{"publicKey": "k2"}},
			{kind: "SSHRole", spec: "{connectionRef: {name: openbao}, keyType: otp, defaultUser: u}", change: map[string]any{"mountPath": "other"}, wantErr: "mountPath is immutable"},
			{kind: "SSHRole", spec: "{connectionRef: {name: openbao}, keyType: otp, defaultUser: u}", change: map[string]any{"defaultUser": "v"}},
			{kind: "TOTPKey", spec: "{connectionRef: {name: openbao}, issuer: i, accountName: a, generate: {writeSecretRef: {name: out}}}", change: map[string]any{"issuer": "j"}, wantErr: "issuer and accountName of a generated key are immutable"},
			{kind: "TOTPKey", spec: "{connectionRef: {name: openbao}, issuer: i, accountName: a, generate: {writeSecretRef: {name: out}}}", change: map[string]any{"digits": int64(8)}, wantErr: "period, algorithm, digits and skew of a generated key are immutable"},
			{kind: "TOTPKey", spec: "{connectionRef: {name: openbao}, issuer: i, accountName: a, generate: {writeSecretRef: {name: out}}}", change: map[string]any{"generate": map[string]any{"writeSecretRef": map[string]any{"name": "other"}}}, wantErr: "generate is immutable"},
			{kind: "TOTPKey", spec: "{connectionRef: {name: openbao}, issuer: i, keySecretRef: {name: s, key: k}}", change: map[string]any{"issuer": "j", "digits": int64(8)}},
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
