package auth

import (
	"context"
	"errors"
	"net/http"
	"slices"
	"strings"

	ctrl "sigs.k8s.io/controller-runtime"

	authv1alpha1 "github.com/alexandrust88/openbao-config-operator/api/auth/v1alpha1"
	"github.com/alexandrust88/openbao-config-operator/internal/engine"
	"github.com/alexandrust88/openbao-config-operator/internal/openbao"
)

// +kubebuilder:rbac:groups=auth.obco.dev,resources=certauthcrls,verbs=get;list;watch;update;patch
// +kubebuilder:rbac:groups=auth.obco.dev,resources=certauthcrls/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=auth.obco.dev,resources=certauthcrls/finalizers,verbs=update

// SetupCertAuthCRL registers the CertAuthCRL controller with the manager.
func SetupCertAuthCRL(mgr ctrl.Manager, conns engine.Connections, opts engine.Options) error {
	r := engine.New[*authv1alpha1.CertAuthCRL](
		mgr.GetClient(),
		mgr.GetScheme(),
		mgr.GetEventRecorder("certauthcrl-controller"),
		conns,
		CertAuthCRLAdapter{},
		func() *authv1alpha1.CertAuthCRL { return &authv1alpha1.CertAuthCRL{} },
		opts,
	)
	return r.SetupWithManager(mgr)
}

// CertAuthCRLAdapter maps a CertAuthCRL onto auth/<mount>/crls/<name>.
type CertAuthCRLAdapter struct{}

var (
	_ engine.Adapter[*authv1alpha1.CertAuthCRL]  = CertAuthCRLAdapter{}
	_ engine.Observer[*authv1alpha1.CertAuthCRL] = CertAuthCRLAdapter{}
	_ engine.Deleter[*authv1alpha1.CertAuthCRL]  = CertAuthCRLAdapter{}
	_ engine.DriftTier                           = CertAuthCRLAdapter{}
)

func certAuthCRLName(specName, metaName string) string {
	return strings.ToLower(authName(specName, metaName))
}

// Path implements engine.Adapter.
func (CertAuthCRLAdapter) Path(c *authv1alpha1.CertAuthCRL) string {
	return "auth/" + authMount(c.Spec.MountPath, "cert") + "/crls/" + certAuthCRLName(c.Spec.Name, c.Name)
}

// Desired implements engine.Adapter.
func (CertAuthCRLAdapter) Desired(_ context.Context, _ engine.Resolver, c *authv1alpha1.CertAuthCRL) (engine.Payload, error) {
	p := engine.Payload{}
	putString(p, "crl", c.Spec.CRL)
	putString(p, "url", c.Spec.URL)
	return p, nil
}

// Fields implements engine.Adapter.
func (CertAuthCRLAdapter) Fields() engine.FieldSpec {
	return engine.FieldSpec{WriteOnly: []string{"crl"}}
}

// Observe implements engine.Observer.
func (a CertAuthCRLAdapter) Observe(ctx context.Context, c openbao.Client, crl *authv1alpha1.CertAuthCRL) (engine.Payload, bool, error) {
	data, err := c.Read(ctx, a.Path(crl))
	if errors.Is(err, openbao.ErrNotFound) || certCRLAbsent(err) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	out := engine.Payload{}
	if cdp, _ := data["cdp"].(map[string]any); cdp != nil {
		if u, _ := cdp["url"].(string); u != "" {
			out["url"] = u
		}
	}
	return out, true, nil
}

// Delete implements engine.Deleter. A missing CRL is HTTP 400, which the default deleter would retry forever.
func (a CertAuthCRLAdapter) Delete(ctx context.Context, c openbao.Client, crl *authv1alpha1.CertAuthCRL) error {
	err := c.Delete(ctx, a.Path(crl))
	if err == nil || errors.Is(err, openbao.ErrNotFound) || certCRLAbsent(err) {
		return nil
	}
	return err
}

// DriftTier implements engine.DriftTier.
func (CertAuthCRLAdapter) DriftTier() engine.Tier { return engine.TierAccessCritical }

// certCRLAbsent reports a missing CRL; this path answers 400, not 404, when the CRL is absent.
func certCRLAbsent(err error) bool {
	re, ok := errors.AsType[*openbao.ResponseError](err)
	if !ok || re.StatusCode != http.StatusBadRequest {
		return false
	}
	return slices.ContainsFunc(re.Errors, func(e string) bool { return strings.Contains(e, "no such CRL") })
}
