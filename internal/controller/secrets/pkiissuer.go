package secrets

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"strings"

	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	commonv1alpha1 "github.com/alexandrust88/openbao-config-operator/api/common/v1alpha1"
	secretsv1alpha1 "github.com/alexandrust88/openbao-config-operator/api/secrets/v1alpha1"
	"github.com/alexandrust88/openbao-config-operator/internal/engine"
	"github.com/alexandrust88/openbao-config-operator/internal/openbao"
)

// +kubebuilder:rbac:groups=secrets.openbaoconfig.io,resources=pkiissuers,verbs=get;list;watch;update;patch
// +kubebuilder:rbac:groups=secrets.openbaoconfig.io,resources=pkiissuers/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=secrets.openbaoconfig.io,resources=pkiissuers/finalizers,verbs=update
// +kubebuilder:rbac:groups=secrets.openbaoconfig.io,resources=pkikeys,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch;create;update;patch

// SetupPKIIssuer registers the PKIIssuer controller with the manager.
func SetupPKIIssuer(mgr ctrl.Manager, conns engine.Connections, opts engine.Options) error {
	r := engine.New[*secretsv1alpha1.PKIIssuer](
		mgr.GetClient(),
		mgr.GetScheme(),
		mgr.GetEventRecorder("pkiissuer-controller"),
		conns,
		PKIIssuerAdapter{Secrets: mgr.GetAPIReader()},
		func() *secretsv1alpha1.PKIIssuer { return &secretsv1alpha1.PKIIssuer{} },
		opts,
	)
	return r.SetupWithManager(mgr)
}

// PKIIssuerAdapter maps a PKIIssuer onto <mount>/issuer/<name>. Creating a root is one generate call; creating an
// intermediate takes two reconciles: the first generates the CSR and records it with the key ID in the status,
// the second signs it with the signer issuer, imports the certificate and names the new issuer. Status
// therefore carries the progress, so a failed signing step resumes without generating another key. Secrets reads
// the output Secret to confirm an exported key reached it.
type PKIIssuerAdapter struct {
	Secrets client.Reader
}

var (
	_ engine.Adapter[*secretsv1alpha1.PKIIssuer]      = PKIIssuerAdapter{}
	_ engine.Observer[*secretsv1alpha1.PKIIssuer]     = PKIIssuerAdapter{}
	_ engine.Writer[*secretsv1alpha1.PKIIssuer]       = PKIIssuerAdapter{}
	_ engine.Deleter[*secretsv1alpha1.PKIIssuer]      = PKIIssuerAdapter{}
	_ engine.StatusWriter[*secretsv1alpha1.PKIIssuer] = PKIIssuerAdapter{}
	_ engine.OutputWriter[*secretsv1alpha1.PKIIssuer] = PKIIssuerAdapter{}

	_ engine.OutputSecretNamer[*secretsv1alpha1.PKIIssuer] = PKIIssuerAdapter{}
)

// Keys that carry resolved references from Desired to Write while the issuer is being created. They are never
// sent to OpenBao and vanish from Desired once status.id is set.
const (
	pkiSignerMountKey  = "_signer_mount"
	pkiSignerIssuerKey = "_signer_issuer"
	pkiKeyRefKey       = "_key_ref"
)

// pkiIssuerConfigKeys are the settings of <mount>/issuer/<ref>. A write replaces all of them with defaults for
// the keys it omits, so updates send the current value of every key the spec does not set. OpenBao 2.7 applies
// delta_crl_distribution_points only on PATCH, which the client does not speak, so it is not managed.
var pkiIssuerConfigKeys = []string{
	"issuer_name", "leaf_not_after_behavior", "usage", "manual_chain", "revocation_signature_algorithm",
	"issuing_certificates", "crl_distribution_points", "ocsp_servers", "enable_aia_url_templating",
}

// Path implements engine.Adapter.
func (PKIIssuerAdapter) Path(i *secretsv1alpha1.PKIIssuer) string {
	return pkiMount(i.Spec.MountPath) + "/issuer/" + specName(i.Spec.Name, i.Name)
}

// Desired implements engine.Adapter. Every setting is sent, an unset one at its OpenBao default; an empty
// revocation_signature_algorithm lets OpenBao pick one from the issuer key.
func (PKIIssuerAdapter) Desired(ctx context.Context, r engine.Resolver, i *secretsv1alpha1.PKIIssuer) (engine.Payload, error) {
	s := &i.Spec
	p := engine.Payload{"issuer_name": specName(s.Name, i.Name)}
	orString(p, "leaf_not_after_behavior", s.LeafNotAfterBehavior, "err")
	usage := slices.Clone(s.Usage)
	if usage == nil {
		usage = []string{"issuing-certificates", "crl-signing", "ocsp-signing"}
	}
	if !slices.Contains(usage, "read-only") {
		usage = append(usage, "read-only")
	}
	slices.Sort(usage)
	p["usage"] = usage
	orList(p, "manual_chain", s.ManualChain)
	orString(p, "revocation_signature_algorithm", s.RevocationSignatureAlgorithm, "")
	orList(p, "issuing_certificates", s.IssuingCertificates)
	orList(p, "crl_distribution_points", s.CRLDistributionPoints)
	orList(p, "ocsp_servers", s.OCSPServers)
	orFalse(p, "enable_aia_url_templating", s.EnableAIAURLTemplating)
	if i.Status.ID != "" {
		return p, nil
	}
	return pkiCreationInputs(ctx, r, i, p)
}

// pkiCreationInputs adds the resolved signer and existing key that creating the issuer needs to desired.
func pkiCreationInputs(ctx context.Context, r engine.Resolver, i *secretsv1alpha1.PKIIssuer, desired engine.Payload) (engine.Payload, error) {
	s := &i.Spec
	if s.Signer != nil {
		signerMount, signerIssuer, err := pkiSigner(ctx, r, i)
		if err != nil {
			return nil, err
		}
		desired[pkiSignerMountKey] = signerMount
		desired[pkiSignerIssuerKey] = signerIssuer
	}
	if s.Key != nil && s.Key.Existing != nil {
		keyRef, err := pkiKeyRef(ctx, r, i, pkiMount(s.MountPath), *s.Key.Existing)
		if err != nil {
			return nil, err
		}
		desired[pkiKeyRefKey] = keyRef
	}
	return desired, nil
}

// Fields implements engine.Adapter.
func (PKIIssuerAdapter) Fields() engine.FieldSpec {
	return engine.FieldSpec{Sets: []string{"usage"}}
}

// Observe implements engine.Observer. It reads by issuer ID once known, so an out-of-band rename is repaired,
// and maps read shapes onto the spec: usage is read as a comma string, manual_chain as IDs, the revocation
// algorithm in its canonical case, and enable_aia_url_templating is omitted while no AIA URL is set. An exported
// key whose private key is not in the output Secret is an error, never Ready.
func (a PKIIssuerAdapter) Observe(ctx context.Context, c openbao.Client, i *secretsv1alpha1.PKIIssuer) (engine.Payload, bool, error) {
	observed, found, err := a.observe(ctx, c, i)
	if err != nil || (!found && i.Status.CSR == "") {
		return observed, found, err
	}
	output := oneTimeOutput{secret: pkiIssuerOutputSecret(i), generated: i.Status.KeyID != "", valueKey: "private_key",
		idKey: outputKeyIDKey, id: i.Status.KeyID, what: "the private key of PKIIssuer " + i.Name}
	if err := output.check(ctx, a.Secrets, i); err != nil {
		return nil, false, err
	}
	return observed, found, nil
}

func (a PKIIssuerAdapter) observe(ctx context.Context, c openbao.Client, i *secretsv1alpha1.PKIIssuer) (engine.Payload, bool, error) {
	// An intermediate with a pending CSR has no named issuer yet; skipping the read avoids the retried 500.
	if i.Status.ID == "" && i.Status.CSR != "" {
		return nil, false, nil
	}
	mount := pkiMount(i.Spec.MountPath)
	observed, found, err := pkiReadRef(ctx, c, mount, "issuer", pkiIssuerLookup(i))
	if err != nil || !found {
		return nil, false, err
	}
	if usage, ok := observed["usage"].(string); ok {
		observed["usage"] = strings.Split(usage, ",")
	}
	if _, ok := observed["enable_aia_url_templating"]; !ok {
		observed["enable_aia_url_templating"] = false
	}
	if alg, ok := observed["revocation_signature_algorithm"].(string); ok && strings.EqualFold(alg, i.Spec.RevocationSignatureAlgorithm) {
		observed["revocation_signature_algorithm"] = i.Spec.RevocationSignatureAlgorithm
	}
	if len(i.Spec.ManualChain) > 0 {
		ids, err := pkiIssuerIDs(ctx, c, mount, i.Spec.ManualChain)
		if err != nil {
			return nil, false, err
		}
		if ids != nil && slices.Equal(ids, pkiStrings(observed["manual_chain"])) {
			observed["manual_chain"] = i.Spec.ManualChain
		}
	}
	return observed, true, nil
}

// Write implements engine.Writer.
func (a PKIIssuerAdapter) Write(ctx context.Context, c openbao.Client, i *secretsv1alpha1.PKIIssuer, desired engine.Payload,
	create bool) (engine.Payload, error) {
	mount := pkiMount(i.Spec.MountPath)
	if !create {
		return nil, pkiWriteIssuerConfig(ctx, c, i, mount, pkiIssuerLookup(i), desired)
	}
	if i.Status.ID != "" {
		// status.id names an issuer deleted out of band, so Desired left out what recreating it needs.
		var err error
		if desired, err = pkiCreationInputs(ctx, engine.NewResolver(a.Secrets), i, maps.Clone(desired)); err != nil {
			return nil, err
		}
	}
	if i.Spec.Type == secretsv1alpha1.PKIIssuerTypeRoot {
		return a.generateRoot(ctx, c, i, mount, desired)
	}
	return a.buildIntermediate(ctx, c, i, mount, desired)
}

func (a PKIIssuerAdapter) generateRoot(ctx context.Context, c openbao.Client, i *secretsv1alpha1.PKIIssuer, mount string,
	desired engine.Payload) (engine.Payload, error) {
	payload := pkiGenerationPayload(i, desired)
	payload["issuer_name"] = desired["issuer_name"]
	pkiPutValidity(payload, i.Spec.Certificate)
	path := mount + "/root/generate/" + string(pkiKeySource(i))
	response, err := c.Write(ctx, path, payload)
	if err != nil {
		return nil, fmt.Errorf("write %s: %w", path, err)
	}
	i.Status.ID = pkiString(response["issuer_id"])
	i.Status.KeyID = pkiString(response["key_id"])
	if err := a.applyConfig(ctx, c, i, mount, desired); err != nil {
		// An exported key exists only in this response: hand it to the engine and leave the settings to the
		// next drift check, which writes and reports them again.
		if response["private_key"] != nil {
			return response, nil
		}
		return nil, err
	}
	return response, nil
}

// buildIntermediate runs the next step of creating an intermediate. The CSR step returns on its own so that
// the CSR, the key ID and an exported private key are stored before anything can fail.
func (a PKIIssuerAdapter) buildIntermediate(ctx context.Context, c openbao.Client, i *secretsv1alpha1.PKIIssuer, mount string,
	desired engine.Payload) (engine.Payload, error) {
	st := &i.Status
	if st.CSR == "" {
		payload := pkiGenerationPayload(i, desired)
		optBool(payload, "add_basic_constraints", i.Spec.Certificate.AddBasicConstraints)
		path := mount + "/intermediate/generate/" + string(pkiKeySource(i))
		response, err := c.Write(ctx, path, payload)
		if err != nil {
			return nil, fmt.Errorf("write %s: %w", path, err)
		}
		st.CSR = pkiString(response["csr"])
		st.KeyID = pkiString(response["key_id"])
		if st.CSR == "" || st.KeyID == "" {
			return nil, fmt.Errorf("write %s: response has no csr or key_id", path)
		}
		return response, nil
	}

	signerMount, _ := desired[pkiSignerMountKey].(string)
	signerIssuer, _ := desired[pkiSignerIssuerKey].(string)
	if signerMount == "" || signerIssuer == "" {
		return nil, fmt.Errorf("signer of intermediate %s is not resolved", i.Name)
	}
	sign := engine.Payload{"csr": st.CSR, "common_name": i.Spec.Certificate.CommonName, "format": "pem"}
	pkiPutSubject(sign, i.Spec.Certificate)
	pkiPutValidity(sign, i.Spec.Certificate)
	signPath := signerMount + "/issuer/" + signerIssuer + "/sign-intermediate"
	signed, err := c.Write(ctx, signPath, sign)
	if err != nil {
		return nil, fmt.Errorf("write %s: %w", signPath, err)
	}

	importPath := mount + "/intermediate/set-signed"
	imported, err := c.Write(ctx, importPath, engine.Payload{"certificate": pkiChainPEM(signed)})
	if err != nil {
		return nil, fmt.Errorf("write %s: %w", importPath, err)
	}
	id := pkiImportedIssuer(imported, st.KeyID)
	if id == "" {
		return nil, fmt.Errorf("write %s: no imported issuer uses key %s", importPath, st.KeyID)
	}
	st.ID = id
	st.CSR = ""
	return nil, pkiWriteIssuerConfig(ctx, c, i, mount, id, desired)
}

// applyConfig writes the issuer settings after generation only when they differ from what generation produced.
func (a PKIIssuerAdapter) applyConfig(ctx context.Context, c openbao.Client, i *secretsv1alpha1.PKIIssuer, mount string,
	desired engine.Payload) error {
	observed, found, err := a.observe(ctx, c, i)
	if err != nil {
		return err
	}
	if found && len(engine.Diff(pkiPublicPayload(desired), observed, a.Fields())) == 0 {
		return nil
	}
	return pkiWriteIssuerConfig(ctx, c, i, mount, i.Status.ID, desired)
}

// Delete implements engine.Deleter: the issuer, then its key unless the key was an existing one it only used.
func (PKIIssuerAdapter) Delete(ctx context.Context, c openbao.Client, i *secretsv1alpha1.PKIIssuer) error {
	mount := pkiMount(i.Spec.MountPath)
	if err := pkiDeleteRef(ctx, c, mount, "issuer", pkiIssuerLookup(i)); err != nil {
		return err
	}
	if i.Status.KeyID == "" || pkiKeySource(i) == secretsv1alpha1.PKIKeySourceExisting {
		return nil
	}
	return pkiDeleteRef(ctx, c, mount, "key", i.Status.KeyID)
}

// WriteStatus implements engine.StatusWriter. An intermediate whose CSR is not signed yet is reported in
// progress, so the engine comes back for the next step without waiting for the drift interval.
func (PKIIssuerAdapter) WriteStatus(_ context.Context, _ openbao.Client, i *secretsv1alpha1.PKIIssuer, observed engine.Payload) error {
	if observed == nil {
		return fmt.Errorf("issuer %s is not complete yet: %w", specName(i.Spec.Name, i.Name), engine.ErrInProgress)
	}
	i.Status.ID = pkiString(observed["issuer_id"])
	if i.Spec.ManagementPolicy == commonv1alpha1.ManagementPolicyObserve {
		return nil
	}
	i.Status.KeyID = pkiString(observed["key_id"])
	i.Status.CSR = ""
	return nil
}

// Outputs implements engine.OutputWriter: an exported private key goes to writeSecretRef once.
func (PKIIssuerAdapter) Outputs(i *secretsv1alpha1.PKIIssuer, response engine.Payload) (string, map[string][]byte, bool) {
	key := pkiString(response["private_key"])
	if i.Spec.WriteSecretRef == nil || key == "" {
		return "", nil, false
	}
	return i.Spec.WriteSecretRef.Name, map[string][]byte{
		"private_key":      []byte(key),
		"private_key_type": []byte(pkiString(response["private_key_type"])),
		outputKeyIDKey:     []byte(pkiString(response["key_id"])),
	}, true
}

// OutputSecretName implements engine.OutputSecretNamer.
func (PKIIssuerAdapter) OutputSecretName(i *secretsv1alpha1.PKIIssuer) string {
	return pkiIssuerOutputSecret(i)
}

func pkiIssuerOutputSecret(i *secretsv1alpha1.PKIIssuer) string {
	if i.Spec.WriteSecretRef == nil || pkiKeySource(i) != secretsv1alpha1.PKIKeySourceExported {
		return ""
	}
	return i.Spec.WriteSecretRef.Name
}

func pkiIssuerLookup(i *secretsv1alpha1.PKIIssuer) string {
	if i.Status.ID != "" {
		return i.Status.ID
	}
	return specName(i.Spec.Name, i.Name)
}

func pkiKeySource(i *secretsv1alpha1.PKIIssuer) secretsv1alpha1.PKIKeySource {
	if i.Spec.Key == nil || i.Spec.Key.Source == "" {
		return secretsv1alpha1.PKIKeySourceInternal
	}
	return i.Spec.Key.Source
}

// pkiSigner resolves the mount and issuer reference that sign an intermediate.
func pkiSigner(ctx context.Context, r engine.Resolver, i *secretsv1alpha1.PKIIssuer) (string, string, error) {
	s := i.Spec.Signer
	if s.IssuerRef == nil {
		mount := s.MountPath
		if mount == "" {
			mount = pkiMount(i.Spec.MountPath)
		}
		return mount, s.IssuerName, nil
	}
	signer, err := pkiReadyIssuer(ctx, r, i.Namespace, s.IssuerRef.Name)
	if err != nil {
		return "", "", err
	}
	if signer.Spec.Namespace != i.Spec.Namespace {
		return "", "", fmt.Errorf("signer PKIIssuer %s is in OpenBao namespace %q, want %q", signer.Name,
			signer.Spec.Namespace, i.Spec.Namespace)
	}
	return pkiMount(signer.Spec.MountPath), signer.Status.ID, nil
}

// pkiKeyRef resolves an existing key to a reference OpenBao accepts in mount.
func pkiKeyRef(ctx context.Context, r engine.Resolver, i *secretsv1alpha1.PKIIssuer, mount string,
	ref secretsv1alpha1.PKIKeyReference) (string, error) {
	if ref.KeyRef == nil {
		return ref.KeyName, nil
	}
	key, err := pkiReadyKey(ctx, r, i.Namespace, ref.KeyRef.Name)
	if err != nil {
		return "", err
	}
	if got := pkiMount(key.Spec.MountPath); got != mount || key.Spec.Namespace != i.Spec.Namespace {
		return "", fmt.Errorf("PKIKey %s is in mount %q of OpenBao namespace %q, want mount %q of namespace %q",
			key.Name, got, key.Spec.Namespace, mount, i.Spec.Namespace)
	}
	return key.Status.ID, nil
}

// pkiGenerationPayload holds the subject and key parameters shared by root generation and CSR generation.
func pkiGenerationPayload(i *secretsv1alpha1.PKIIssuer, desired engine.Payload) engine.Payload {
	cert := i.Spec.Certificate
	p := engine.Payload{"common_name": cert.CommonName, "format": "pem"}
	pkiPutSubject(p, cert)
	optInt32(p, "signature_bits", cert.SignatureBits)
	optBool(p, "use_pss", cert.UsePSS)
	if key := i.Spec.Key; key != nil {
		optString(p, "key_name", key.Name)
		optString(p, "key_type", string(key.Type))
		optInt32(p, "key_bits", key.Bits)
		optString(p, "external_key_ref", key.ExternalKeyRef)
		optString(p, "private_key_format", key.PrivateKeyFormat)
	}
	if ref, ok := desired[pkiKeyRefKey]; ok {
		p["key_ref"] = ref
	}
	return p
}

func pkiPutSubject(p engine.Payload, cert secretsv1alpha1.PKIIssuerCertificate) {
	if len(cert.AltNames) > 0 {
		p["alt_names"] = strings.Join(cert.AltNames, ",")
	}
	pkiOptList(p, "ip_sans", cert.IPSANs)
	pkiOptList(p, "uri_sans", cert.URISANs)
	pkiOptList(p, "other_sans", cert.OtherSANs)
	optBool(p, "exclude_cn_from_sans", cert.ExcludeCNFromSANs)
	pkiOptList(p, "ou", cert.OU)
	pkiOptList(p, "organization", cert.Organization)
	pkiOptList(p, "country", cert.Country)
	pkiOptList(p, "locality", cert.Locality)
	pkiOptList(p, "province", cert.Province)
	pkiOptList(p, "street_address", cert.StreetAddress)
	pkiOptList(p, "postal_code", cert.PostalCode)
	optString(p, "serial_number", cert.SerialNumber)
}

// pkiPutValidity adds the parameters that shape the issued CA certificate, used by root generation and signing.
func pkiPutValidity(p engine.Payload, cert secretsv1alpha1.PKIIssuerCertificate) {
	optSeconds(p, "ttl", cert.TTL)
	optString(p, "not_after", cert.NotAfter)
	optSeconds(p, "not_before_duration", cert.NotBeforeDuration)
	optInt32(p, "max_path_length", cert.MaxPathLength)
	pkiOptList(p, "permitted_dns_domains", cert.PermittedDNSDomains)
}

// pkiChainPEM returns the signed certificate followed by its parents, so set-signed can build the chain.
func pkiChainPEM(signed engine.Payload) string {
	certificate := strings.TrimSpace(pkiString(signed["certificate"]))
	parts := []string{certificate}
	for _, ca := range pkiStrings(signed["ca_chain"]) {
		if ca = strings.TrimSpace(ca); ca != "" && !slices.Contains(parts, ca) {
			parts = append(parts, ca)
		}
	}
	return strings.Join(parts, "\n")
}

// pkiImportedIssuer finds the issuer that set-signed bound to keyID in its issuer -> key mapping.
func pkiImportedIssuer(imported engine.Payload, keyID string) string {
	mapping, _ := imported["mapping"].(map[string]any)
	for issuerID, key := range mapping {
		if key == keyID {
			return issuerID
		}
	}
	return ""
}

// pkiWriteIssuerConfig updates the issuer at ref with the public keys of desired, keeping current values for
// the keys desired does not set. issuer_name always comes from the spec: a write without it clears the name.
func pkiWriteIssuerConfig(ctx context.Context, c openbao.Client, i *secretsv1alpha1.PKIIssuer, mount, ref string,
	desired engine.Payload) error {
	current, found, err := pkiReadRef(ctx, c, mount, "issuer", ref)
	if err != nil {
		return err
	}
	if !found {
		return fmt.Errorf("issuer %s/issuer/%s not found", mount, ref)
	}
	body := engine.Payload{}
	for _, key := range pkiIssuerConfigKeys {
		if value, ok := current[key]; ok && value != nil {
			body[key] = value
		}
	}
	maps.Copy(body, pkiPublicPayload(desired))
	body["issuer_name"] = specName(i.Spec.Name, i.Name)
	path := mount + "/issuer/" + ref
	if _, err := c.Write(ctx, path, body); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	return nil
}

func pkiPublicPayload(desired engine.Payload) engine.Payload {
	public := engine.Payload{}
	for key, value := range desired {
		if !strings.HasPrefix(key, "_") {
			public[key] = value
		}
	}
	return public
}

// pkiIssuerIDs resolves issuer names or IDs to IDs.
func pkiIssuerIDs(ctx context.Context, c openbao.Client, mount string, refs []string) ([]string, error) {
	ids := make([]string, 0, len(refs))
	for _, ref := range refs {
		issuer, found, err := pkiReadRef(ctx, c, mount, "issuer", ref)
		if err != nil {
			return nil, err
		}
		if !found {
			return nil, nil
		}
		ids = append(ids, pkiString(issuer["issuer_id"]))
	}
	return ids, nil
}

func pkiStrings(v any) []string {
	if values, ok := v.([]string); ok {
		return values
	}
	list, _ := v.([]any)
	out := make([]string, 0, len(list))
	for _, item := range list {
		if s, ok := item.(string); ok {
			out = append(out, s)
		}
	}
	return out
}
