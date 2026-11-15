package secrets

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	ctrl "sigs.k8s.io/controller-runtime"

	secretsv1alpha1 "github.com/alexandrust88/openbao-config-operator/api/secrets/v1alpha1"
	"github.com/alexandrust88/openbao-config-operator/internal/engine"
	"github.com/alexandrust88/openbao-config-operator/internal/openbao"
)

// +kubebuilder:rbac:groups=secrets.openbaoconfig.io,resources=sshcas,verbs=get;list;watch;update;patch
// +kubebuilder:rbac:groups=secrets.openbaoconfig.io,resources=sshcas/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=secrets.openbaoconfig.io,resources=sshcas/finalizers,verbs=update
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch

// SetupSSHCA registers the SSHCA controller with the manager.
func SetupSSHCA(mgr ctrl.Manager, conns engine.Connections, opts engine.Options) error {
	r := engine.New[*secretsv1alpha1.SSHCA](
		mgr.GetClient(),
		mgr.GetScheme(),
		mgr.GetEventRecorder("sshca-controller"),
		conns,
		SSHCAAdapter{},
		func() *secretsv1alpha1.SSHCA { return &secretsv1alpha1.SSHCA{} },
		opts,
	)
	return r.SetupWithManager(mgr)
}

// SSHCAAdapter maps an SSHCA onto <mount>/config/ca.
type SSHCAAdapter struct{}

var (
	_ engine.Adapter[*secretsv1alpha1.SSHCA]      = SSHCAAdapter{}
	_ engine.Observer[*secretsv1alpha1.SSHCA]     = SSHCAAdapter{}
	_ engine.Writer[*secretsv1alpha1.SSHCA]       = SSHCAAdapter{}
	_ engine.StatusWriter[*secretsv1alpha1.SSHCA] = SSHCAAdapter{}

	_ engine.SecretReferencer[*secretsv1alpha1.SSHCA] = SSHCAAdapter{}
	_ engine.Destructive                              = SSHCAAdapter{}
)

// Path implements engine.Adapter.
func (SSHCAAdapter) Path(c *secretsv1alpha1.SSHCA) string { return c.Spec.MountPath + "/config/ca" }

// Desired implements engine.Adapter.
func (SSHCAAdapter) Desired(ctx context.Context, r engine.Resolver, c *secretsv1alpha1.SSHCA) (engine.Payload, error) {
	ref := c.Spec.PrivateKeySecretRef
	if ref == nil {
		p := engine.Payload{"generate_signing_key": true}
		optString(p, "key_type", c.Spec.KeyType)
		optInt32(p, "key_bits", c.Spec.KeyBits)
		return p, nil
	}
	privateKey, err := r.SecretValue(ctx, c.Namespace, *ref)
	if err != nil {
		return nil, err
	}
	return engine.Payload{
		"generate_signing_key": false,
		"public_key":           c.Spec.PublicKey,
		"private_key":          string(privateKey),
	}, nil
}

// Fields implements engine.Adapter.
func (SSHCAAdapter) Fields() engine.FieldSpec {
	return engine.FieldSpec{WriteOnly: []string{"private_key"}}
}

// Destructive implements engine.Destructive: deleting config/ca purges every signing key of the mount.
func (SSHCAAdapter) Destructive() bool { return true }

// Observe implements engine.Observer. A mount without a default issuer answers 400, which means absent here.
// A generated key is never replaced, so its generation inputs are reported as desired; the public key of an
// imported pair is compared without surrounding whitespace.
func (a SSHCAAdapter) Observe(ctx context.Context, c openbao.Client, ca *secretsv1alpha1.SSHCA) (engine.Payload, bool, error) {
	path := a.Path(ca)
	observed, err := c.Read(ctx, path)
	if errors.Is(err, openbao.ErrNotFound) || isNoDefaultIssuer(err) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("read %s: %w", path, err)
	}
	if observed == nil {
		observed = engine.Payload{}
	}
	observed["generate_signing_key"] = ca.Spec.PrivateKeySecretRef == nil
	if ca.Spec.PrivateKeySecretRef == nil {
		optString(observed, "key_type", ca.Spec.KeyType)
		optInt32(observed, "key_bits", ca.Spec.KeyBits)
	}
	if stored, ok := observed["public_key"].(string); ok && ca.Spec.PublicKey != "" &&
		strings.TrimSpace(stored) == strings.TrimSpace(ca.Spec.PublicKey) {
		observed["public_key"] = ca.Spec.PublicKey
	}
	return observed, true, nil
}

func isNoDefaultIssuer(err error) bool {
	var re *openbao.ResponseError
	if !errors.As(err, &re) || re.StatusCode != 400 {
		return false
	}
	return slices.ContainsFunc(re.Errors, func(msg string) bool {
		return strings.Contains(msg, "no default issuer currently configured")
	})
}

// Write implements engine.Writer. Importing a key pair that already exists as an issuer returns that issuer
// without making it the default again, so the issuer is set as default explicitly.
func (a SSHCAAdapter) Write(ctx context.Context, c openbao.Client, ca *secretsv1alpha1.SSHCA, desired engine.Payload,
	_ bool) (engine.Payload, error) {
	path := a.Path(ca)
	response, err := c.Write(ctx, path, desired)
	if err != nil {
		return nil, fmt.Errorf("write %s: %w", path, err)
	}
	if id, ok := response["issuer_id"].(string); ok && id != "" && ca.Spec.PrivateKeySecretRef != nil {
		if _, err := c.Write(ctx, ca.Spec.MountPath+"/config/issuers", engine.Payload{"default": id}); err != nil {
			return nil, fmt.Errorf("set default issuer of %s: %w", ca.Spec.MountPath, err)
		}
	}
	return nil, nil
}

// WriteStatus implements engine.StatusWriter: the CA public key and the issuer id.
func (SSHCAAdapter) WriteStatus(_ context.Context, _ openbao.Client, ca *secretsv1alpha1.SSHCA, observed engine.Payload) error {
	if key, ok := observed["public_key"].(string); ok {
		ca.Status.PublicKey = key
	}
	if id, ok := observed["issuer_id"].(string); ok {
		ca.Status.ID = id
	}
	return nil
}

// SecretRefs implements engine.SecretReferencer.
func (SSHCAAdapter) SecretRefs(ca *secretsv1alpha1.SSHCA) []string {
	if ca.Spec.PrivateKeySecretRef == nil {
		return nil
	}
	return []string{ca.Spec.PrivateKeySecretRef.Name}
}
