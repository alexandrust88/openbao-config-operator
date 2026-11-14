package secrets

import (
	"context"
	"errors"
	"reflect"
	"slices"
	"strings"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"

	commonv1alpha1 "github.com/alexandrust88/openbao-config-operator/api/common/v1alpha1"
	secretsv1alpha1 "github.com/alexandrust88/openbao-config-operator/api/secrets/v1alpha1"
	"github.com/alexandrust88/openbao-config-operator/internal/engine"
	"github.com/alexandrust88/openbao-config-operator/internal/openbao"
)

const (
	sshcaTestIssuerID = "3e82cbf1-636e-155c-10ea-7cf8644e18c0"
)

const sshCAPublicKey = "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIOxDc4qw0Sn1Yckpx76YjSolzM59lII4lqAWLzc39icT"

// sshCAGoldenRead is `bao read ssh/config/ca` on OpenBao 2.7.2; the stored key keeps its trailing newline.
func sshCAGoldenRead() map[string]any {
	return map[string]any{
		"issuer_id":   sshcaTestIssuerID,
		"issuer_name": "",
		"public_key":  sshCAPublicKey + "\n",
	}
}

func generatedSSHCA() *secretsv1alpha1.SSHCA {
	return &secretsv1alpha1.SSHCA{
		ObjectMeta: metav1.ObjectMeta{Name: "ca", Namespace: "team"},
		Spec:       secretsv1alpha1.SSHCASpec{MountPath: "ssh", KeyType: "ssh-ed25519", KeyBits: ptr.To[int32](0)},
	}
}

func importedSSHCA() *secretsv1alpha1.SSHCA {
	return &secretsv1alpha1.SSHCA{
		ObjectMeta: metav1.ObjectMeta{Name: "ca", Namespace: "team"},
		Spec: secretsv1alpha1.SSHCASpec{
			MountPath:           "ssh",
			PublicKey:           sshCAPublicKey,
			PrivateKeySecretRef: &commonv1alpha1.SecretKeySelector{Name: "ssh-ca", Key: "private"},
		},
	}
}

func TestSSHCADesired(t *testing.T) {
	a := SSHCAAdapter{}
	got, err := a.Desired(context.Background(), engineStubResolver{}, generatedSSHCA())
	if err != nil {
		t.Fatal(err)
	}
	want := engine.Payload{"generate_signing_key": true, "key_type": "ssh-ed25519", "key_bits": int64(0)}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Desired(generated) = %v, want %v", got, want)
	}

	r := engineStubResolver{"ssh-ca/private": "fake-private-key"}
	got, err = a.Desired(context.Background(), r, importedSSHCA())
	if err != nil {
		t.Fatal(err)
	}
	want = engine.Payload{"generate_signing_key": false, "public_key": sshCAPublicKey, "private_key": "fake-private-key"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Desired(imported) = %v, want %v", got, want)
	}
	if path := a.Path(importedSSHCA()); path != "ssh/config/ca" {
		t.Errorf("Path = %q", path)
	}

	_, err = a.Desired(context.Background(), engineStubResolver{}, importedSSHCA())
	if !errors.Is(err, engine.ErrReferenceNotReady) {
		t.Errorf("missing Secret: err = %v, want ErrReferenceNotReady", err)
	}
}

func TestSSHCAGoldenRead(t *testing.T) {
	a := SSHCAAdapter{}
	r := engineStubResolver{"ssh-ca/private": "fake-private-key"}
	for name, ca := range map[string]*secretsv1alpha1.SSHCA{"generated": generatedSSHCA(), "imported": importedSSHCA()} {
		desired, _ := a.Desired(context.Background(), r, ca)
		c := &engineStubClient{data: map[string]map[string]any{"ssh/config/ca": sshCAGoldenRead()}}
		observed, found, err := a.Observe(context.Background(), c, ca)
		if err != nil || !found {
			t.Fatalf("%s: found %v, err %v", name, found, err)
		}
		if diff := engine.Diff(desired, observed, a.Fields()); len(diff) != 0 {
			t.Errorf("%s: steady state differs in %v", name, diff)
		}
	}

	ca := importedSSHCA()
	desired, _ := a.Desired(context.Background(), r, ca)
	other := sshCAGoldenRead()
	other["public_key"] = "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIOtherOtherOtherOtherOtherOtherOtherOther\n"
	c := &engineStubClient{data: map[string]map[string]any{"ssh/config/ca": other}}
	observed, _, _ := a.Observe(context.Background(), c, ca)
	if diff := engine.Diff(desired, observed, a.Fields()); !slices.Equal(diff, []string{"public_key"}) {
		t.Errorf("Diff = %v, want public_key", diff)
	}

	rotated := engineStubResolver{"ssh-ca/private": "fake-private-key-2"}
	desired2, _ := a.Desired(context.Background(), rotated, ca)
	if engine.WriteOnlyHash([]byte("k"), desired, a.Fields()) == engine.WriteOnlyHash([]byte("k"), desired2, a.Fields()) {
		t.Error("a new private key must change the write-only hash")
	}
}

func TestSSHCAObserveAbsent(t *testing.T) {
	a := SSHCAAdapter{}
	noIssuer := &openbao.ResponseError{StatusCode: 400, Errors: []string{"no default issuer currently configured"}}
	c := &engineStubClient{errs: map[string]error{"ssh/config/ca": noIssuer}}
	if _, found, err := a.Observe(context.Background(), c, generatedSSHCA()); found || err != nil {
		t.Errorf("no default issuer: found %v, err %v, want absent", found, err)
	}
	c.errs["ssh/config/ca"] = &openbao.ResponseError{StatusCode: 400, Errors: []string{"permission problem"}}
	if _, _, err := a.Observe(context.Background(), c, generatedSSHCA()); err == nil {
		t.Error("another 400 must stay an error")
	}
}

func TestSSHCAWrite(t *testing.T) {
	a := SSHCAAdapter{}
	ca := importedSSHCA()
	desired, _ := a.Desired(context.Background(), engineStubResolver{"ssh-ca/private": "fake-private-key"}, ca)
	c := &engineStubClient{responses: map[string]map[string]any{"ssh/config/ca": sshCAGoldenRead()}}
	if _, err := a.Write(context.Background(), c, ca, desired, false); err != nil {
		t.Fatal(err)
	}
	if len(c.calls) != 2 || c.calls[1].path != "ssh/config/issuers" ||
		c.calls[1].data["default"] != sshcaTestIssuerID {
		t.Errorf("calls = %v, want config/ca then the default issuer", c.calls)
	}
	if err := a.WriteStatus(context.Background(), c, ca, sshCAGoldenRead()); err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(ca.Status.PublicKey) != sshCAPublicKey || ca.Status.ID != sshcaTestIssuerID {
		t.Errorf("status = %+v", ca.Status)
	}
	if refs := a.SecretRefs(ca); !slices.Equal(refs, []string{"ssh-ca"}) {
		t.Errorf("SecretRefs = %v", refs)
	}
}
