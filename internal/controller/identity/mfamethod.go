package identity

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"

	ctrl "sigs.k8s.io/controller-runtime"

	commonv1alpha1 "github.com/alexandrust88/openbao-config-operator/api/common/v1alpha1"
	identityv1alpha1 "github.com/alexandrust88/openbao-config-operator/api/identity/v1alpha1"
	"github.com/alexandrust88/openbao-config-operator/internal/engine"
	"github.com/alexandrust88/openbao-config-operator/internal/openbao"
)

// +kubebuilder:rbac:groups=identity.obco.dev,resources=mfamethods,verbs=get;list;watch;update;patch
// +kubebuilder:rbac:groups=identity.obco.dev,resources=mfamethods/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=identity.obco.dev,resources=mfamethods/finalizers,verbs=update
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch

// SetupMFAMethod registers the MFAMethod controller with the manager.
func SetupMFAMethod(mgr ctrl.Manager, conns engine.Connections, opts engine.Options) error {
	r := engine.New[*identityv1alpha1.MFAMethod](
		mgr.GetClient(),
		mgr.GetScheme(),
		mgr.GetEventRecorder("mfamethod-controller"),
		conns,
		MFAMethodAdapter{},
		func() *identityv1alpha1.MFAMethod { return &identityv1alpha1.MFAMethod{} },
		opts,
	)
	return r.SetupWithManager(mgr)
}

// MFAMethodAdapter maps an MFAMethod onto identity/mfa/method/<type>/<id>. The ID is assigned by OpenBao on the
// first write; until status.id holds it, the method is found by name.
type MFAMethodAdapter struct{}

var (
	_ engine.Adapter[*identityv1alpha1.MFAMethod]      = MFAMethodAdapter{}
	_ engine.Observer[*identityv1alpha1.MFAMethod]     = MFAMethodAdapter{}
	_ engine.Writer[*identityv1alpha1.MFAMethod]       = MFAMethodAdapter{}
	_ engine.Deleter[*identityv1alpha1.MFAMethod]      = MFAMethodAdapter{}
	_ engine.StatusWriter[*identityv1alpha1.MFAMethod] = MFAMethodAdapter{}

	_ engine.SecretReferencer[*identityv1alpha1.MFAMethod] = MFAMethodAdapter{}
	_ engine.FieldsProvider[*identityv1alpha1.MFAMethod]   = MFAMethodAdapter{}
	_ engine.Destructive                                   = MFAMethodAdapter{}
)

// Path implements engine.Adapter: the method itself once its ID is known, the type collection before.
func (MFAMethodAdapter) Path(m *identityv1alpha1.MFAMethod) string {
	if m.Status.ID != "" {
		return mfaCollection(m) + "/" + m.Status.ID
	}
	return mfaCollection(m)
}

// Desired implements engine.Adapter. Every write replaces the whole method configuration, so it carries the
// write-only keys too.
func (MFAMethodAdapter) Desired(ctx context.Context, r engine.Resolver, m *identityv1alpha1.MFAMethod) (engine.Payload, error) {
	p := engine.Payload{"method_name": oidcName(m.Spec.Name, m.Name)}
	secret := func(key string, ref commonv1alpha1.SecretKeySelector) error {
		value, err := r.SecretValue(ctx, m.Namespace, ref)
		if err != nil {
			return err
		}
		p[key] = string(value)
		return nil
	}
	switch {
	case m.Spec.TOTP != nil:
		t := m.Spec.TOTP
		p["issuer"] = t.Issuer
		p["period"] = secondsOr(t.Period, 30)
		p["key_size"] = intOr(t.KeySize, 20)
		p["qr_size"] = intOr(t.QRSize, 200)
		p["algorithm"] = stringOr(string(t.Algorithm), "SHA1")
		p["digits"] = intOr(t.Digits, 6)
		p["skew"] = intOr(t.Skew, 1)
		p["max_validation_attempts"] = intOr(t.MaxValidationAttempts, 5)
	case m.Spec.Duo != nil:
		d := m.Spec.Duo
		if err := secret("secret_key", d.SecretKeySecretRef); err != nil {
			return nil, err
		}
		if err := secret("integration_key", d.IntegrationKeySecretRef); err != nil {
			return nil, err
		}
		p["api_hostname"] = d.APIHostname
		p["push_info"] = d.PushInfo
		p["use_passcode"] = boolOr(d.UsePasscode, false)
		p["username_format"] = d.UsernameFormat
	case m.Spec.Okta != nil:
		o := m.Spec.Okta
		if err := secret("api_token", o.APITokenSecretRef); err != nil {
			return nil, err
		}
		p["org_name"] = o.OrgName
		p["base_url"] = stringOr(o.BaseURL, "okta.com")
		p["primary_email"] = boolOr(o.PrimaryEmail, false)
		p["username_format"] = o.UsernameFormat
	case m.Spec.PingID != nil:
		settings, err := r.SecretValue(ctx, m.Namespace, m.Spec.PingID.SettingsFileSecretRef)
		if err != nil {
			return nil, err
		}
		p["settings_file_base64"] = base64.StdEncoding.EncodeToString(settings)
		p["username_format"] = m.Spec.PingID.UsernameFormat
	default:
		return nil, fmt.Errorf("spec sets none of totp, duo, okta and pingID: %w", engine.ErrInvalidSpec)
	}
	return p, nil
}

// Fields implements engine.Adapter. primary_email is not secret, but OpenBao never returns it.
func (MFAMethodAdapter) Fields() engine.FieldSpec {
	return engine.FieldSpec{
		WriteOnly: []string{"secret_key", "integration_key", "api_token", "settings_file_base64", "primary_email"},
		Aliases:   map[string]string{"name": "method_name", "pushinfo": "push_info"},
		Durations: []string{"period"},
	}
}

// Destructive implements engine.Destructive: deleting a TOTP method destroys the TOTP secret of every enrolled
// entity.
func (MFAMethodAdapter) Destructive() bool { return true }

// FieldsFor implements engine.FieldsProvider. A PingID method read omits username_format, so there it is tracked
// through the write-only hash.
func (MFAMethodAdapter) FieldsFor(_ context.Context, _ engine.Resolver, m *identityv1alpha1.MFAMethod) (engine.FieldSpec, error) {
	if m.Spec.PingID == nil {
		return engine.FieldSpec{}, nil
	}
	return engine.FieldSpec{WriteOnly: []string{"username_format"}}, nil
}

// Observe implements engine.Observer: by status.id when known, otherwise by name within the type collection.
func (MFAMethodAdapter) Observe(ctx context.Context, c openbao.Client, m *identityv1alpha1.MFAMethod) (engine.Payload, bool, error) {
	if m.Status.ID == "" {
		return findMFAMethod(ctx, c, mfaCollection(m), oidcName(m.Spec.Name, m.Name))
	}
	path := mfaCollection(m) + "/" + m.Status.ID
	observed, err := c.Read(ctx, path)
	if errors.Is(err, openbao.ErrNotFound) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("read %s: %w", path, err)
	}
	return observed, true, nil
}

// Write implements engine.Writer. A create posts to the type collection, which upserts by method_name; an
// update with a known ID writes the method itself.
func (MFAMethodAdapter) Write(ctx context.Context, c openbao.Client, m *identityv1alpha1.MFAMethod, desired engine.Payload, create bool) (engine.Payload, error) {
	path := mfaCollection(m)
	if !create && m.Status.ID != "" {
		path += "/" + m.Status.ID
	}
	response, err := c.Write(ctx, path, desired)
	if err != nil {
		return nil, fmt.Errorf("write %s: %w", path, err)
	}
	return response, nil
}

// Delete implements engine.Deleter. Without status.id, as after a create whose status patch was lost, the method
// is found by name.
func (a MFAMethodAdapter) Delete(ctx context.Context, c openbao.Client, m *identityv1alpha1.MFAMethod) error {
	id := m.Status.ID
	if id == "" {
		method, found, err := a.Observe(ctx, c, m)
		if err != nil || !found {
			return err
		}
		id, _ = method["id"].(string)
		if id == "" {
			return nil
		}
	}
	return c.Delete(ctx, mfaCollection(m)+"/"+id)
}

// WriteStatus implements engine.StatusWriter: status.id is the method ID.
func (MFAMethodAdapter) WriteStatus(_ context.Context, _ openbao.Client, m *identityv1alpha1.MFAMethod, observed engine.Payload) error {
	if id, ok := observed["id"].(string); ok && id != "" {
		m.Status.ID = id
	}
	return nil
}

// SecretRefs implements engine.SecretReferencer.
func (MFAMethodAdapter) SecretRefs(m *identityv1alpha1.MFAMethod) []string {
	switch {
	case m.Spec.Duo != nil:
		return []string{m.Spec.Duo.SecretKeySecretRef.Name, m.Spec.Duo.IntegrationKeySecretRef.Name}
	case m.Spec.Okta != nil:
		return []string{m.Spec.Okta.APITokenSecretRef.Name}
	case m.Spec.PingID != nil:
		return []string{m.Spec.PingID.SettingsFileSecretRef.Name}
	}
	return nil
}

// mfaCollection is identity/mfa/method/<type>.
func mfaCollection(m *identityv1alpha1.MFAMethod) string {
	switch {
	case m.Spec.Duo != nil:
		return "identity/mfa/method/duo"
	case m.Spec.Okta != nil:
		return "identity/mfa/method/okta"
	case m.Spec.PingID != nil:
		return "identity/mfa/method/pingid"
	}
	return "identity/mfa/method/totp"
}
