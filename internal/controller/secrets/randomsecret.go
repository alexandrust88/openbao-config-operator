package secrets

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"maps"
	"math/big"
	"slices"

	ctrl "sigs.k8s.io/controller-runtime"

	secretsv1alpha1 "github.com/alexandrust88/openbao-config-operator/api/secrets/v1alpha1"
	"github.com/alexandrust88/openbao-config-operator/internal/engine"
	"github.com/alexandrust88/openbao-config-operator/internal/openbao"
)

// +kubebuilder:rbac:groups=secrets.obco.dev,resources=randomsecrets,verbs=get;list;watch;update;patch
// +kubebuilder:rbac:groups=secrets.obco.dev,resources=randomsecrets/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=secrets.obco.dev,resources=randomsecrets/finalizers,verbs=update
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch;create;update

// SetupRandomSecret registers the RandomSecret controller with the manager.
func SetupRandomSecret(mgr ctrl.Manager, conns engine.Connections, opts engine.Options) error {
	r := engine.New[*secretsv1alpha1.RandomSecret](
		mgr.GetClient(),
		mgr.GetScheme(),
		mgr.GetEventRecorder("randomsecret-controller"),
		conns,
		RandomSecretAdapter{},
		func() *secretsv1alpha1.RandomSecret { return &secretsv1alpha1.RandomSecret{} },
		opts,
	)
	return r.SetupWithManager(mgr)
}

// RandomSecretAdapter generates a value once and stores it under one key of a KV secret.
//
// The desired payload holds only pseudo keys that are never sent: they are write-only, so a change of
// refreshNonce or writeSecretRef changes the write-only hash and makes the engine call Write, which decides
// whether to regenerate. Observe reports the secret as absent while the key holds no value.
type RandomSecretAdapter struct{}

const (
	randomNonceKey       = "refresh_nonce"
	randomWriteSecretKey = "write_secret_ref"

	defaultRandomKey     = "password"
	defaultRandomLength  = 32
	defaultRandomCharset = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
)

var (
	_ engine.Adapter[*secretsv1alpha1.RandomSecret]      = RandomSecretAdapter{}
	_ engine.Observer[*secretsv1alpha1.RandomSecret]     = RandomSecretAdapter{}
	_ engine.Writer[*secretsv1alpha1.RandomSecret]       = RandomSecretAdapter{}
	_ engine.Deleter[*secretsv1alpha1.RandomSecret]      = RandomSecretAdapter{}
	_ engine.OutputWriter[*secretsv1alpha1.RandomSecret] = RandomSecretAdapter{}
	_ engine.Destructive                                 = RandomSecretAdapter{}

	_ engine.OutputSecretNamer[*secretsv1alpha1.RandomSecret] = RandomSecretAdapter{}
	_ engine.OutputRefresher[*secretsv1alpha1.RandomSecret]   = RandomSecretAdapter{}
)

// Path implements engine.Adapter.
func (RandomSecretAdapter) Path(s *secretsv1alpha1.RandomSecret) string {
	return kvDataPath(s.Spec.MountPath, s.Spec.Path, s.Spec.KVVersion)
}

// Desired implements engine.Adapter.
func (RandomSecretAdapter) Desired(_ context.Context, _ engine.Resolver, s *secretsv1alpha1.RandomSecret) (engine.Payload, error) {
	p := engine.Payload{}
	if s.Spec.RefreshNonce != "" {
		p[randomNonceKey] = s.Spec.RefreshNonce
	}
	if s.Spec.WriteSecretRef != nil {
		p[randomWriteSecretKey] = s.Spec.WriteSecretRef.Name
	}
	return p, nil
}

// Fields implements engine.Adapter.
func (RandomSecretAdapter) Fields() engine.FieldSpec {
	return engine.FieldSpec{WriteOnly: []string{randomNonceKey, randomWriteSecretKey}}
}

// Destructive implements engine.Destructive: deleting the resource removes the generated value from OpenBao.
func (RandomSecretAdapter) Destructive() bool { return true }

// Observe implements engine.Observer. The value itself is never returned.
func (RandomSecretAdapter) Observe(ctx context.Context, c openbao.Client, s *secretsv1alpha1.RandomSecret) (engine.Payload, bool, error) {
	current, _, _, err := readKV(ctx, c, s.Spec.MountPath, s.Spec.Path, s.Spec.KVVersion)
	if err != nil {
		return nil, false, err
	}
	if _, ok := randomValue(current, s); !ok {
		return nil, false, nil
	}
	return engine.Payload{}, true, nil
}

// Write implements engine.Writer. It generates a new value when the key holds none, or when refreshNonce changed
// on a resource that already owns the value; otherwise it keeps the stored value, for example on adoption or
// when only writeSecretRef changed, and returns engine.ErrNothingWritten so the engine delivers it through
// RefreshOutputs. The response carries a generated value only for Outputs.
func (RandomSecretAdapter) Write(ctx context.Context, c openbao.Client, s *secretsv1alpha1.RandomSecret, _ engine.Payload,
	_ bool) (engine.Payload, error) {
	current, version, found, err := readKV(ctx, c, s.Spec.MountPath, s.Spec.Path, s.Spec.KVVersion)
	if err != nil {
		return nil, err
	}
	if s.Status.Acquired == "" {
		s.Status.PathCreated = !found && version == 0
	}
	_, ok := randomValue(current, s)
	if ok && (s.Status.Acquired == "" || s.Spec.RefreshNonce == s.Status.RefreshNonce) {
		s.Status.RefreshNonce = s.Spec.RefreshNonce
		return nil, engine.ErrNothingWritten
	}
	value, err := generateRandom(ctx, c, s)
	if err != nil {
		return nil, err
	}
	merged := maps.Clone(current)
	if merged == nil {
		merged = map[string]any{}
	}
	merged[randomKey(s)] = value
	if err := writeKV(ctx, c, s.Spec.MountPath, s.Spec.Path, s.Spec.KVVersion, merged, version); err != nil {
		return nil, err
	}
	s.Status.RefreshNonce = s.Spec.RefreshNonce
	if s.Spec.WriteSecretRef == nil {
		return nil, nil
	}
	return engine.Payload{randomKey(s): value}, nil
}

// Outputs implements engine.OutputWriter.
func (RandomSecretAdapter) Outputs(s *secretsv1alpha1.RandomSecret, response engine.Payload) (string, map[string][]byte, bool) {
	if s.Spec.WriteSecretRef == nil {
		return "", nil, false
	}
	value, ok := response[randomKey(s)].(string)
	if !ok {
		return "", nil, false
	}
	return s.Spec.WriteSecretRef.Name, map[string][]byte{randomKey(s): []byte(value)}, true
}

// RefreshOutputs implements engine.OutputRefresher: the stored value is delivered again, for example after the
// output Secret was deleted or its first write failed.
func (RandomSecretAdapter) RefreshOutputs(ctx context.Context, c openbao.Client, s *secretsv1alpha1.RandomSecret,
	_ engine.Payload) (engine.Payload, error) {
	if s.Spec.WriteSecretRef == nil {
		return nil, nil
	}
	current, _, _, err := readKV(ctx, c, s.Spec.MountPath, s.Spec.Path, s.Spec.KVVersion)
	if err != nil {
		return nil, err
	}
	value, ok := randomValue(current, s)
	if !ok {
		return nil, nil
	}
	return engine.Payload{randomKey(s): value}, nil
}

// OutputSecretName implements engine.OutputSecretNamer.
func (RandomSecretAdapter) OutputSecretName(s *secretsv1alpha1.RandomSecret) string {
	if s.Spec.WriteSecretRef == nil {
		return ""
	}
	return s.Spec.WriteSecretRef.Name
}

// Delete implements engine.Deleter. It removes only the generated key and keeps the rest of the secret; when no other
// key remains and the resource created the path itself (status.pathCreated) and allows destructive deletes, it also
// deletes the history on KV version 2.
func (RandomSecretAdapter) Delete(ctx context.Context, c openbao.Client, s *secretsv1alpha1.RandomSecret) error {
	if s.Status.PurgeThroughVersion != nil {
		if !kvOwnsPath(s) || !s.Status.PathCreated {
			return nil
		}
		return kvDestroyHistory(ctx, c, s.Spec.MountPath, s.Spec.Path, *s.Status.PurgeThroughVersion)
	}
	if err := kvRemoveKeys(ctx, c, s.Spec.MountPath, s.Spec.Path, s.Spec.KVVersion, []string{randomKey(s)}); err != nil {
		return err
	}
	if !kvOwnsPath(s) || !s.Status.PathCreated {
		return nil
	}
	return kvPreparePurge(ctx, c, s.Spec.MountPath, s.Spec.Path, s.Spec.KVVersion, &s.Status.PurgeThroughVersion)
}

func randomKey(s *secretsv1alpha1.RandomSecret) string {
	if s.Spec.Key != "" {
		return s.Spec.Key
	}
	return defaultRandomKey
}

func randomValue(current map[string]any, s *secretsv1alpha1.RandomSecret) (string, bool) {
	value, ok := current[randomKey(s)].(string)
	return value, ok && value != ""
}

func generateRandom(ctx context.Context, c openbao.Client, s *secretsv1alpha1.RandomSecret) (string, error) {
	if name := s.Spec.PasswordPolicyName; name != "" {
		resp, err := c.Read(ctx, "sys/policies/password/"+name+"/generate")
		if errors.Is(err, openbao.ErrNotFound) {
			return "", fmt.Errorf("password policy %q not found: %w", name, engine.ErrReferenceNotReady)
		}
		if err != nil {
			return "", fmt.Errorf("generate from password policy %q: %w", name, err)
		}
		value, ok := resp["password"].(string)
		if !ok || value == "" {
			return "", fmt.Errorf("password policy %q returned no password", name)
		}
		return value, nil
	}
	length, charset := int32(defaultRandomLength), defaultRandomCharset
	if g := s.Spec.Generator; g != nil {
		if g.Length > 0 {
			length = g.Length
		}
		if g.Charset != "" {
			charset = g.Charset
		}
	}
	return randomString(int(length), charset)
}

// randomString draws length characters uniformly from the distinct characters of charset.
func randomString(length int, charset string) (string, error) {
	runes := []rune(charset)
	slices.Sort(runes)
	runes = slices.Compact(runes)
	if len(runes) < 2 {
		return "", errors.New("generator charset needs at least two distinct characters")
	}
	limit := big.NewInt(int64(len(runes)))
	out := make([]rune, length)
	for i := range out {
		n, err := rand.Int(rand.Reader, limit)
		if err != nil {
			return "", fmt.Errorf("read random: %w", err)
		}
		out[i] = runes[n.Int64()]
	}
	return string(out), nil
}
