package secrets

import (
	"context"
	"errors"
	"fmt"
	"slices"

	ctrl "sigs.k8s.io/controller-runtime"

	commonv1alpha1 "github.com/alexandrust88/openbao-config-operator/api/common/v1alpha1"
	secretsv1alpha1 "github.com/alexandrust88/openbao-config-operator/api/secrets/v1alpha1"
	"github.com/alexandrust88/openbao-config-operator/internal/engine"
	"github.com/alexandrust88/openbao-config-operator/internal/openbao"
)

// +kubebuilder:rbac:groups=secrets.openbaoconfig.io,resources=transitkeys,verbs=get;list;watch;update;patch
// +kubebuilder:rbac:groups=secrets.openbaoconfig.io,resources=transitkeys/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=secrets.openbaoconfig.io,resources=transitkeys/finalizers,verbs=update

// SetupTransitKey registers the TransitKey controller with the manager.
func SetupTransitKey(mgr ctrl.Manager, conns engine.Connections, opts engine.Options) error {
	r := engine.New[*secretsv1alpha1.TransitKey](
		mgr.GetClient(),
		mgr.GetScheme(),
		mgr.GetEventRecorder("transitkey-controller"),
		conns,
		TransitKeyAdapter{},
		func() *secretsv1alpha1.TransitKey { return &secretsv1alpha1.TransitKey{} },
		opts,
	)
	return r.SetupWithManager(mgr)
}

// TransitKeyAdapter maps a TransitKey onto <mount>/keys/<name> (creation) and <mount>/keys/<name>/config.
type TransitKeyAdapter struct{}

var (
	_ engine.Adapter[*secretsv1alpha1.TransitKey]       = TransitKeyAdapter{}
	_ engine.Observer[*secretsv1alpha1.TransitKey]      = TransitKeyAdapter{}
	_ engine.Writer[*secretsv1alpha1.TransitKey]        = TransitKeyAdapter{}
	_ engine.Deleter[*secretsv1alpha1.TransitKey]       = TransitKeyAdapter{}
	_ engine.StatusWriter[*secretsv1alpha1.TransitKey]  = TransitKeyAdapter{}
	_ engine.RotationNonce[*secretsv1alpha1.TransitKey] = TransitKeyAdapter{}
)

var (
	transitCreateOnly = []string{"type", "derived", "convergent_encryption", "key_size"}
	transitConfigKeys = []string{"min_decryption_version", "min_encryption_version", "deletion_allowed", "exportable",
		"allow_plaintext_backup", "auto_rotate_period"}
)

// Path implements engine.Adapter.
func (TransitKeyAdapter) Path(k *secretsv1alpha1.TransitKey) string {
	return k.Spec.MountPath + "/keys/" + specName(k.Spec.Name, k.Name)
}

// Desired implements engine.Adapter. exportable and allowPlaintextBackup are sent only when true, because
// OpenBao never turns them off again.
func (TransitKeyAdapter) Desired(_ context.Context, _ engine.Resolver, k *secretsv1alpha1.TransitKey) (engine.Payload, error) {
	s := k.Spec
	p := engine.Payload{"type": string(s.Type)}
	optBool(p, "derived", s.Derived)
	if s.ConvergentEncryption != nil && *s.ConvergentEncryption {
		p["convergent_encryption"] = true
	}
	optInt32(p, "key_size", s.KeySize)
	if s.Exportable != nil && *s.Exportable {
		p["exportable"] = true
	}
	if s.AllowPlaintextBackup != nil && *s.AllowPlaintextBackup {
		p["allow_plaintext_backup"] = true
	}
	orSeconds(p, "auto_rotate_period", s.AutoRotatePeriod, 0)
	orInt32(p, "min_decryption_version", s.MinDecryptionVersion, 1)
	orInt32(p, "min_encryption_version", s.MinEncryptionVersion, 0)
	orFalse(p, "deletion_allowed", s.DeletionAllowed)
	return p, nil
}

// Fields implements engine.Adapter.
func (TransitKeyAdapter) Fields() engine.FieldSpec {
	return engine.FieldSpec{
		CreateOnly: transitCreateOnly,
		Durations:  []string{"auto_rotate_period"},
	}
}

// Observe implements engine.Observer. A key that exists with a different type, derivation or size cannot be
// converged and is reported as an error.
func (a TransitKeyAdapter) Observe(ctx context.Context, c openbao.Client, k *secretsv1alpha1.TransitKey) (engine.Payload, bool, error) {
	path := a.Path(k)
	observed, err := c.Read(ctx, path)
	if errors.Is(err, openbao.ErrNotFound) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("read %s: %w", path, err)
	}
	if observed == nil {
		observed = engine.Payload{}
	}
	desired, _ := a.Desired(ctx, nil, k)
	if differing := engine.Diff(desired, observed, engine.FieldSpec{WriteOnly: transitMutableKeys(desired)}); len(differing) > 0 {
		return nil, false, fmt.Errorf("transit key %s exists with different %v, which cannot be changed", path, differing)
	}
	return observed, true, nil
}

// transitMutableKeys returns the desired keys that are not create-only, so that Observe compares only the
// create-only ones.
func transitMutableKeys(desired engine.Payload) []string {
	var keys []string
	for key := range desired {
		if !slices.Contains(transitCreateOnly, key) {
			keys = append(keys, key)
		}
	}
	return keys
}

// Write implements engine.Writer: create the key with its creation parameters, then write its config, then
// rotate it when spec.rotateNonce changed on an owned key.
func (a TransitKeyAdapter) Write(ctx context.Context, c openbao.Client, k *secretsv1alpha1.TransitKey,
	desired engine.Payload, create bool) (engine.Payload, error) {
	path := a.Path(k)
	if create {
		params := engine.Payload{}
		for _, key := range slices.Concat(transitCreateOnly, []string{"exportable", "allow_plaintext_backup", "auto_rotate_period"}) {
			if v, ok := desired[key]; ok {
				params[key] = v
			}
		}
		optString(params, "external_key_ref", k.Spec.ExternalKeyRef)
		if _, err := c.Write(ctx, path, params); err != nil {
			return nil, fmt.Errorf("create %s: %w", path, err)
		}
	}
	config := engine.Payload{}
	for _, key := range transitConfigKeys {
		if v, ok := desired[key]; ok {
			config[key] = v
		}
	}
	if len(config) > 0 {
		if _, err := c.Write(ctx, path+"/config", config); err != nil {
			return nil, fmt.Errorf("write %s/config: %w", path, err)
		}
	}
	if !create && k.Status.Acquired != "" && k.Spec.RotateNonce != "" && k.Spec.RotateNonce != k.Status.RotatedNonce {
		if _, err := c.Write(ctx, path+"/rotate", engine.Payload{}); err != nil {
			return nil, fmt.Errorf("rotate %s: %w", path, err)
		}
		// Recorded at once, so a failure later in the pass does not rotate the key again.
		k.Status.RotatedNonce = k.Spec.RotateNonce
	}
	return nil, nil
}

// Delete implements engine.Deleter. OpenBao refuses to delete a key until deletion_allowed is set.
func (a TransitKeyAdapter) Delete(ctx context.Context, c openbao.Client, k *secretsv1alpha1.TransitKey) error {
	path := a.Path(k)
	if _, err := c.Read(ctx, path); err != nil {
		if errors.Is(err, openbao.ErrNotFound) {
			return nil
		}
		return fmt.Errorf("read %s: %w", path, err)
	}
	if _, err := c.Write(ctx, path+"/config", engine.Payload{"deletion_allowed": true}); err != nil {
		return fmt.Errorf("allow deletion of %s: %w", path, err)
	}
	if err := c.Delete(ctx, path); err != nil && !errors.Is(err, openbao.ErrNotFound) {
		return fmt.Errorf("delete %s: %w", path, err)
	}
	return nil
}

// WriteStatus implements engine.StatusWriter: the latest key version and the applied rotate nonce, which is kept
// when spec.rotateNonce is removed.
func (TransitKeyAdapter) WriteStatus(_ context.Context, _ openbao.Client, k *secretsv1alpha1.TransitKey, observed engine.Payload) error {
	if version, ok := asInt64(observed["latest_version"]); ok {
		k.Status.LatestVersion = version
	}
	if k.Spec.RotateNonce != "" && k.Spec.ManagementPolicy != commonv1alpha1.ManagementPolicyObserve {
		k.Status.RotatedNonce = k.Spec.RotateNonce
	}
	return nil
}

// RotationNonce implements engine.RotationNonce: a new spec.rotateNonce makes the engine call Write, which rotates
// the key. The nonce is a trigger, not an OpenBao setting, so removing it keeps the recorded nonce and writes nothing.
func (TransitKeyAdapter) RotationNonce(k *secretsv1alpha1.TransitKey) string {
	if k.Spec.RotateNonce == "" {
		return k.Status.RotationNonce
	}
	return k.Spec.RotateNonce
}
