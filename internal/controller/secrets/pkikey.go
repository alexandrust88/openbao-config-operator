package secrets

import (
	"context"
	"fmt"

	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	secretsv1alpha1 "github.com/alexandrust88/openbao-config-operator/api/secrets/v1alpha1"
	"github.com/alexandrust88/openbao-config-operator/internal/engine"
	"github.com/alexandrust88/openbao-config-operator/internal/openbao"
)

// +kubebuilder:rbac:groups=secrets.obco.dev,resources=pkikeys,verbs=get;list;watch;update;patch
// +kubebuilder:rbac:groups=secrets.obco.dev,resources=pkikeys/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=secrets.obco.dev,resources=pkikeys/finalizers,verbs=update
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch;create;update

// SetupPKIKey registers the PKIKey controller with the manager.
func SetupPKIKey(mgr ctrl.Manager, conns engine.Connections, opts engine.Options) error {
	r := engine.New[*secretsv1alpha1.PKIKey](
		mgr.GetClient(),
		mgr.GetScheme(),
		mgr.GetEventRecorder("pkikey-controller"),
		conns,
		PKIKeyAdapter{Secrets: mgr.GetAPIReader()},
		func() *secretsv1alpha1.PKIKey { return &secretsv1alpha1.PKIKey{} },
		opts,
	)
	return r.SetupWithManager(mgr)
}

// PKIKeyAdapter maps a PKIKey onto <mount>/key/<name>. The key is generated or imported on create; an update
// only renames it through its ID. Secrets reads the output Secret to confirm an exported key reached it.
type PKIKeyAdapter struct {
	Secrets client.Reader
}

var (
	_ engine.Adapter[*secretsv1alpha1.PKIKey]      = PKIKeyAdapter{}
	_ engine.Observer[*secretsv1alpha1.PKIKey]     = PKIKeyAdapter{}
	_ engine.Writer[*secretsv1alpha1.PKIKey]       = PKIKeyAdapter{}
	_ engine.Deleter[*secretsv1alpha1.PKIKey]      = PKIKeyAdapter{}
	_ engine.StatusWriter[*secretsv1alpha1.PKIKey] = PKIKeyAdapter{}
	_ engine.OutputWriter[*secretsv1alpha1.PKIKey] = PKIKeyAdapter{}

	_ engine.OutputSecretNamer[*secretsv1alpha1.PKIKey] = PKIKeyAdapter{}

	_ engine.SecretReferencer[*secretsv1alpha1.PKIKey] = PKIKeyAdapter{}
	_ engine.Destructive                               = PKIKeyAdapter{}
)

// Path implements engine.Adapter.
func (PKIKeyAdapter) Path(k *secretsv1alpha1.PKIKey) string {
	return pkiMount(k.Spec.MountPath) + "/key/" + specName(k.Spec.Name, k.Name)
}

// Desired implements engine.Adapter.
func (PKIKeyAdapter) Desired(ctx context.Context, r engine.Resolver, k *secretsv1alpha1.PKIKey) (engine.Payload, error) {
	p := engine.Payload{"key_name": specName(k.Spec.Name, k.Name)}
	if ref := k.Spec.PEMBundleSecretRef; ref != nil {
		bundle, err := r.SecretValue(ctx, k.Namespace, *ref)
		if err != nil {
			return nil, err
		}
		p["pem_bundle"] = string(bundle)
	}
	return p, nil
}

// Fields implements engine.Adapter. The imported key is sent on create only and never read back.
func (PKIKeyAdapter) Fields() engine.FieldSpec {
	return engine.FieldSpec{WriteOnly: []string{"pem_bundle"}, CreateOnly: []string{"pem_bundle"}}
}

// Destructive implements engine.Destructive: deleting the resource destroys the private key.
func (PKIKeyAdapter) Destructive() bool { return true }

// Observe implements engine.Observer, reading by key ID once known. An exported key whose private key is not in
// the output Secret is an error, never Ready.
func (a PKIKeyAdapter) Observe(ctx context.Context, c openbao.Client, k *secretsv1alpha1.PKIKey) (engine.Payload, bool, error) {
	observed, found, err := pkiReadRef(ctx, c, pkiMount(k.Spec.MountPath), "key", pkiKeyLookup(k))
	if err != nil || !found {
		return nil, false, err
	}
	output := oneTimeOutput{secret: pkiKeyOutputSecret(k), generated: k.Status.ID != "", valueKey: "private_key",
		idKey: outputKeyIDKey, id: k.Status.ID, what: "the private key of PKIKey " + k.Name}
	if err := output.check(ctx, a.Secrets, k); err != nil {
		return nil, false, err
	}
	return observed, true, nil
}

// Write implements engine.Writer.
func (PKIKeyAdapter) Write(ctx context.Context, c openbao.Client, k *secretsv1alpha1.PKIKey, desired engine.Payload,
	create bool) (engine.Payload, error) {
	mount := pkiMount(k.Spec.MountPath)
	path := mount + "/key/" + pkiKeyLookup(k)
	payload := engine.Payload{"key_name": desired["key_name"]}
	if create {
		path = mount + "/keys/generate/" + string(pkiKeyCreation(k))
		if bundle, ok := desired["pem_bundle"]; ok {
			path = mount + "/keys/import"
			payload["pem_bundle"] = bundle
		}
		optString(payload, "key_type", string(k.Spec.KeyType))
		optInt32(payload, "key_bits", k.Spec.KeyBits)
		optString(payload, "external_key_ref", k.Spec.ExternalKeyRef)
	}
	response, err := c.Write(ctx, path, payload)
	if err != nil {
		return nil, fmt.Errorf("write %s: %w", path, err)
	}
	if id := pkiString(response["key_id"]); id != "" {
		k.Status.ID = id
	}
	return response, nil
}

// Delete implements engine.Deleter. OpenBao refuses to delete a key that an issuer still uses.
func (PKIKeyAdapter) Delete(ctx context.Context, c openbao.Client, k *secretsv1alpha1.PKIKey) error {
	return pkiDeleteRef(ctx, c, pkiMount(k.Spec.MountPath), "key", pkiKeyLookup(k))
}

// WriteStatus implements engine.StatusWriter.
func (PKIKeyAdapter) WriteStatus(_ context.Context, _ openbao.Client, k *secretsv1alpha1.PKIKey, observed engine.Payload) error {
	if id := pkiString(observed["key_id"]); id != "" {
		k.Status.ID = id
	}
	return nil
}

// Outputs implements engine.OutputWriter: an exported private key goes to writeSecretRef once.
func (PKIKeyAdapter) Outputs(k *secretsv1alpha1.PKIKey, response engine.Payload) (string, map[string][]byte, bool) {
	key := pkiString(response["private_key"])
	if k.Spec.WriteSecretRef == nil || key == "" {
		return "", nil, false
	}
	return k.Spec.WriteSecretRef.Name, map[string][]byte{
		"private_key":  []byte(key),
		"key_type":     []byte(pkiString(response["key_type"])),
		outputKeyIDKey: []byte(pkiString(response["key_id"])),
	}, true
}

// OutputSecretName implements engine.OutputSecretNamer.
func (PKIKeyAdapter) OutputSecretName(k *secretsv1alpha1.PKIKey) string {
	return pkiKeyOutputSecret(k)
}

func pkiKeyOutputSecret(k *secretsv1alpha1.PKIKey) string {
	if k.Spec.WriteSecretRef == nil || pkiKeyCreation(k) != secretsv1alpha1.PKIKeyCreationExported {
		return ""
	}
	return k.Spec.WriteSecretRef.Name
}

// SecretRefs implements engine.SecretReferencer.
func (PKIKeyAdapter) SecretRefs(k *secretsv1alpha1.PKIKey) []string {
	if k.Spec.PEMBundleSecretRef == nil {
		return nil
	}
	return []string{k.Spec.PEMBundleSecretRef.Name}
}

func pkiKeyLookup(k *secretsv1alpha1.PKIKey) string {
	if k.Status.ID != "" {
		return k.Status.ID
	}
	return specName(k.Spec.Name, k.Name)
}

func pkiKeyCreation(k *secretsv1alpha1.PKIKey) secretsv1alpha1.PKIKeyCreation {
	if k.Spec.Type == "" {
		return secretsv1alpha1.PKIKeyCreationInternal
	}
	return k.Spec.Type
}

// pkiReadyKey returns the referenced PKIKey once it is Ready and has a key ID.
func pkiReadyKey(ctx context.Context, r engine.Resolver, namespace, name string) (*secretsv1alpha1.PKIKey, error) {
	key := &secretsv1alpha1.PKIKey{}
	if err := r.Object(ctx, client.ObjectKey{Namespace: namespace, Name: name}, key); err != nil {
		return nil, fmt.Errorf("PKIKey %s/%s: %w", namespace, name, err)
	}
	if key.Status.ID == "" || !pkiReady(key.Status.Conditions) {
		return nil, fmt.Errorf("PKIKey %s/%s is not Ready: %w", namespace, name, engine.ErrReferenceNotReady)
	}
	return key, nil
}
