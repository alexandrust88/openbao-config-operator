package secrets

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"slices"

	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	commonv1alpha1 "github.com/alexandrust88/openbao-config-operator/api/common/v1alpha1"
	secretsv1alpha1 "github.com/alexandrust88/openbao-config-operator/api/secrets/v1alpha1"
	"github.com/alexandrust88/openbao-config-operator/internal/engine"
	"github.com/alexandrust88/openbao-config-operator/internal/openbao"
)

// +kubebuilder:rbac:groups=secrets.obco.dev,resources=totpkeys,verbs=get;list;watch;update;patch
// +kubebuilder:rbac:groups=secrets.obco.dev,resources=totpkeys/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=secrets.obco.dev,resources=totpkeys/finalizers,verbs=update
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch;create;update

// SetupTOTPKey registers the TOTPKey controller with the manager.
func SetupTOTPKey(mgr ctrl.Manager, conns engine.Connections, opts engine.Options) error {
	r := engine.New[*secretsv1alpha1.TOTPKey](
		mgr.GetClient(),
		mgr.GetScheme(),
		mgr.GetEventRecorder("totpkey-controller"),
		conns,
		TOTPKeyAdapter{Secrets: mgr.GetAPIReader(), HashKey: opts.HashKey},
		func() *secretsv1alpha1.TOTPKey { return &secretsv1alpha1.TOTPKey{} },
		opts,
	)
	return r.SetupWithManager(mgr)
}

// TOTPKeyAdapter maps a TOTPKey onto <mount>/keys/<name>. Secrets reads the output Secret to confirm a generated
// key's URL reached it; HashKey keys the fingerprint of that URL kept in status.id and in the Secret.
type TOTPKeyAdapter struct {
	Secrets client.Reader
	HashKey []byte
}

var (
	_ engine.Adapter[*secretsv1alpha1.TOTPKey]      = TOTPKeyAdapter{}
	_ engine.Observer[*secretsv1alpha1.TOTPKey]     = TOTPKeyAdapter{}
	_ engine.Writer[*secretsv1alpha1.TOTPKey]       = TOTPKeyAdapter{}
	_ engine.OutputWriter[*secretsv1alpha1.TOTPKey] = TOTPKeyAdapter{}

	_ engine.OutputSecretNamer[*secretsv1alpha1.TOTPKey] = TOTPKeyAdapter{}

	_ engine.SecretReferencer[*secretsv1alpha1.TOTPKey] = TOTPKeyAdapter{}
	_ engine.Destructive                                = TOTPKeyAdapter{}
)

// Key of the output Secret that holds the fingerprint of the generated key's URL, so a Secret left over from an
// earlier key is not taken for the current one.
const totpFingerprintKey = "fingerprint"

// totpUnreadable are the inputs a read of the key never returns.
var totpUnreadable = []string{"generate", "exported", "key_size", "qr_size", "skew"}

// Path implements engine.Adapter.
func (TOTPKeyAdapter) Path(k *secretsv1alpha1.TOTPKey) string {
	return k.Spec.MountPath + "/keys/" + specName(k.Spec.Name, k.Name)
}

// Desired implements engine.Adapter.
func (TOTPKeyAdapter) Desired(ctx context.Context, r engine.Resolver, k *secretsv1alpha1.TOTPKey) (engine.Payload, error) {
	p := totpParams(k)
	for key, ref := range map[string]*commonv1alpha1.SecretKeySelector{"key": k.Spec.KeySecretRef, "url": k.Spec.URLSecretRef} {
		if ref == nil {
			continue
		}
		value, err := r.SecretValue(ctx, k.Namespace, *ref)
		if err != nil {
			return nil, err
		}
		p[key] = string(value)
	}
	return p, nil
}

// totpParams returns the desired keys that do not come from a Secret.
func totpParams(k *secretsv1alpha1.TOTPKey) engine.Payload {
	s := k.Spec
	p := engine.Payload{}
	if s.Generate != nil {
		p["generate"] = true
		optInt32(p, "key_size", s.Generate.KeySize)
		optBool(p, "exported", s.Generate.Exported)
		optInt32(p, "qr_size", s.Generate.QRSize)
	}
	optString(p, "issuer", s.Issuer)
	optString(p, "account_name", s.AccountName)
	if s.URLSecretRef == nil {
		orSeconds(p, "period", s.Period, 30)
		orString(p, "algorithm", s.Algorithm, "SHA1")
		orInt32(p, "digits", s.Digits, 6)
	} else {
		optSeconds(p, "period", s.Period)
		optString(p, "algorithm", s.Algorithm)
		optInt32(p, "digits", s.Digits)
	}
	optInt32(p, "skew", s.Skew)
	return p
}

// Fields implements engine.Adapter.
func (TOTPKeyAdapter) Fields() engine.FieldSpec {
	return engine.FieldSpec{
		WriteOnly: []string{"key", "url"},
		Durations: []string{"period"},
	}
}

// Destructive implements engine.Destructive: deleting the resource destroys the TOTP seed.
func (TOTPKeyAdapter) Destructive() bool { return true }

// Observe implements engine.Observer. Every write replaces the key, and with generate it creates a new secret,
// so a generated key that exists is reported as desired and never rewritten. Inputs a read never returns are
// reported as desired for imported keys. A generated key whose URL is not in the output Secret is an error, never
// Ready.
func (a TOTPKeyAdapter) Observe(ctx context.Context, c openbao.Client, k *secretsv1alpha1.TOTPKey) (engine.Payload, bool, error) {
	path := a.Path(k)
	observed, err := c.Read(ctx, path)
	if errors.Is(err, openbao.ErrNotFound) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("read %s: %w", path, err)
	}
	output := oneTimeOutput{secret: totpOutputSecret(k), generated: k.Status.Path != "", valueKey: "url",
		idKey: totpFingerprintKey, id: k.Status.ID, what: "the otpauth URL of TOTPKey " + k.Name}
	if err := output.check(ctx, a.Secrets, k); err != nil {
		return nil, false, err
	}
	if observed == nil {
		observed = engine.Payload{}
	}
	for key, value := range totpParams(k) {
		if k.Spec.Generate != nil || slices.Contains(totpUnreadable, key) {
			observed[key] = value
		}
	}
	return observed, true, nil
}

// Write implements engine.Writer. A generated key's URL is fingerprinted into status.id before the output Secret is
// written, so a failed Secret write leaves the status naming a key the Secret does not hold.
func (a TOTPKeyAdapter) Write(ctx context.Context, c openbao.Client, k *secretsv1alpha1.TOTPKey, desired engine.Payload,
	_ bool) (engine.Payload, error) {
	response, err := c.Write(ctx, a.Path(k), desired)
	if err != nil {
		return nil, err
	}
	if url, _ := response["url"].(string); url != "" {
		k.Status.ID = a.fingerprint(url)
	}
	return response, nil
}

// fingerprint is an HMAC rather than a plain hash because status is readable more widely than the Secret and a
// small keySize would let a plain hash of the URL be brute-forced.
func (a TOTPKeyAdapter) fingerprint(url string) string {
	mac := hmac.New(sha256.New, a.HashKey)
	mac.Write([]byte(url))
	return hex.EncodeToString(mac.Sum(nil))
}

// Outputs implements engine.OutputWriter: the otpauth URL and QR code OpenBao returns once for a generated key.
func (a TOTPKeyAdapter) Outputs(k *secretsv1alpha1.TOTPKey, response engine.Payload) (string, map[string][]byte, bool) {
	g := k.Spec.Generate
	if g == nil || g.WriteSecretRef == nil {
		return "", nil, false
	}
	url, _ := response["url"].(string)
	if url == "" {
		return "", nil, false
	}
	data := map[string][]byte{"url": []byte(url), totpFingerprintKey: []byte(a.fingerprint(url))}
	if barcode, ok := response["barcode"].(string); ok && barcode != "" {
		data["barcode"] = []byte(barcode)
	}
	return g.WriteSecretRef.Name, data, true
}

// OutputSecretName implements engine.OutputSecretNamer.
func (TOTPKeyAdapter) OutputSecretName(k *secretsv1alpha1.TOTPKey) string {
	return totpOutputSecret(k)
}

func totpOutputSecret(k *secretsv1alpha1.TOTPKey) string {
	g := k.Spec.Generate
	if g == nil || g.WriteSecretRef == nil || (g.Exported != nil && !*g.Exported) {
		return ""
	}
	return g.WriteSecretRef.Name
}

// SecretRefs implements engine.SecretReferencer.
func (TOTPKeyAdapter) SecretRefs(k *secretsv1alpha1.TOTPKey) []string {
	switch {
	case k.Spec.KeySecretRef != nil:
		return []string{k.Spec.KeySecretRef.Name}
	case k.Spec.URLSecretRef != nil:
		return []string{k.Spec.URLSecretRef.Name}
	}
	return nil
}
