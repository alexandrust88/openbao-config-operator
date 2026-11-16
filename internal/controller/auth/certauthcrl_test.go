package auth

import (
	"context"
	"net/http"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	authv1alpha1 "github.com/alexandrust88/openbao-config-operator/api/auth/v1alpha1"
	"github.com/alexandrust88/openbao-config-operator/internal/engine"
	"github.com/alexandrust88/openbao-config-operator/internal/openbao"
)

func TestCertAuthCRLPathAndDesired(t *testing.T) {
	a := CertAuthCRLAdapter{}
	c := &authv1alpha1.CertAuthCRL{
		ObjectMeta: metav1.ObjectMeta{Name: "Team-CA", Namespace: pluginTestNamespace},
		Spec: authv1alpha1.CertAuthCRLSpec{
			ResourceSpec: credConnection(),
			URL:          "https://example.com/ca.crl",
		},
	}
	if got := a.Path(c); got != "auth/cert/crls/team-ca" {
		t.Errorf("path = %q", got)
	}
	desired, err := a.Desired(context.Background(), nil, c)
	if err != nil {
		t.Fatal(err)
	}
	want := engine.Payload{"url": "https://example.com/ca.crl"}
	for k, v := range want {
		if desired[k] != v {
			t.Errorf("%s = %v, want %v", k, desired[k], v)
		}
	}
	if _, ok := desired["crl"]; ok {
		t.Error("crl must not be sent when only url is set")
	}

	c.Spec = authv1alpha1.CertAuthCRLSpec{
		ResourceSpec: credConnection(),
		MountPath:    "certs/team",
		Name:         "prod",
		CRL:          "-----BEGIN X509 CRL-----\nMIIB\n-----END X509 CRL-----",
	}
	c.Name = "prod"
	if got := a.Path(c); got != "auth/certs/team/crls/prod" {
		t.Errorf("path = %q", got)
	}
	desired, err = a.Desired(context.Background(), nil, c)
	if err != nil {
		t.Fatal(err)
	}
	if desired["crl"] != c.Spec.CRL {
		t.Errorf("crl = %v", desired["crl"])
	}
	if _, ok := desired["url"]; ok {
		t.Error("url must not be sent when only crl is set")
	}
}

func TestCertAuthCRLSteadyStateAgainstGoldenRead(t *testing.T) {
	a := CertAuthCRLAdapter{}
	c := &authv1alpha1.CertAuthCRL{
		ObjectMeta: metav1.ObjectMeta{Name: "team-ca", Namespace: pluginTestNamespace},
		Spec: authv1alpha1.CertAuthCRLSpec{
			ResourceSpec: credConnection(),
			URL:          "https://example.com/ca.crl",
		},
	}
	desired, err := a.Desired(context.Background(), nil, c)
	if err != nil {
		t.Fatal(err)
	}
	observed := map[string]any{"url": "https://example.com/ca.crl"}
	credExpectDiff(t, desired, observed, a.Fields())
}

type certCRLReadClient struct {
	openbao.Client
	path string
	err  error
	data map[string]any
}

func (c *certCRLReadClient) Read(_ context.Context, path string) (map[string]any, error) {
	if path == c.path && c.err != nil {
		return nil, c.err
	}
	if path == c.path && c.data != nil {
		return c.data, nil
	}
	return nil, openbao.ErrNotFound
}

func TestCertAuthCRLObserveAbsentCRL(t *testing.T) {
	a := CertAuthCRLAdapter{}
	c := &authv1alpha1.CertAuthCRL{
		ObjectMeta: metav1.ObjectMeta{Name: "missing", Namespace: pluginTestNamespace},
		Spec:       authv1alpha1.CertAuthCRLSpec{ResourceSpec: credConnection(), URL: "https://example.com/ca.crl"},
	}
	bao := &certCRLReadClient{
		path: "auth/cert/crls/missing",
		err: &openbao.ResponseError{
			StatusCode: http.StatusBadRequest,
			Errors:     []string{"no such CRL"},
		},
	}
	_, found, err := a.Observe(context.Background(), bao, c)
	if err != nil {
		t.Fatal(err)
	}
	if found {
		t.Error("found = true, want false for a 400 no such CRL response")
	}
	bao.err = openbao.ErrNotFound
	_, found, err = a.Observe(context.Background(), bao, c)
	if err != nil || found {
		t.Fatalf("404: found=%v err=%v", found, err)
	}
}

func TestCertAuthCRLObserveURLFromCDP(t *testing.T) {
	a := CertAuthCRLAdapter{}
	c := &authv1alpha1.CertAuthCRL{
		ObjectMeta: metav1.ObjectMeta{Name: "team-ca", Namespace: pluginTestNamespace},
		Spec:       authv1alpha1.CertAuthCRLSpec{ResourceSpec: credConnection(), URL: "https://example.com/ca.crl"},
	}
	bao := &certCRLReadClient{
		path: "auth/cert/crls/team-ca",
		data: map[string]any{
			"cdp":     map[string]any{"url": "https://example.com/ca.crl"},
			"serials": []any{"01"},
		},
	}
	got, found, err := a.Observe(context.Background(), bao, c)
	if err != nil || !found {
		t.Fatalf("observe: found=%v err=%v", found, err)
	}
	if got["url"] != "https://example.com/ca.crl" {
		t.Errorf("url = %v", got["url"])
	}
	if _, ok := got["serials"]; ok {
		t.Error("serials must not be returned from Observe")
	}
}

func (c *certCRLReadClient) Delete(context.Context, string) error { return c.err }

func TestCertAuthCRLDeleteAbsent(t *testing.T) {
	a := CertAuthCRLAdapter{}
	c := &authv1alpha1.CertAuthCRL{
		ObjectMeta: metav1.ObjectMeta{Name: "missing", Namespace: pluginTestNamespace},
		Spec:       authv1alpha1.CertAuthCRLSpec{ResourceSpec: credConnection()},
	}
	bao := &certCRLReadClient{
		err: &openbao.ResponseError{StatusCode: http.StatusBadRequest, Errors: []string{"no such CRL missing"}},
	}
	if err := a.Delete(context.Background(), bao, c); err != nil {
		t.Fatal(err)
	}
	bao.err = &openbao.ResponseError{StatusCode: http.StatusBadRequest, Errors: []string{"permission denied"}}
	if err := a.Delete(context.Background(), bao, c); err == nil {
		t.Fatal("a 400 that is not a missing CRL must be returned")
	}
}
